import {
    clearFileUploadRuntimeState,
    consumeSuppressedUploadErrorToast,
    getInitialSimultaneousUploads,
    initializeAdaptiveUploadConcurrency,
    markUploadConflictAutoRetry,
    maybeStartPendingUploadDuringFinalize,
    moveUploadToRetryState,
    noteAdaptiveUploadRetry,
    parseUploadSuccessEntry,
    resetAdaptiveUploadConcurrency,
    resetUploadConflictAutoRetry,
    resolveUploadSuccessResult,
    shouldAutoRetryUploadConflict,
    trackUploadResponseStatus,
    updateAdaptiveUploadConcurrency,
} from '../upload-finalization';

// Minimal XMLHttpRequest stand-in: records 'readystatechange' listeners and
// replays them once the response is "received" so we can drive the same code
// path the browser does after a chunk completes.
class FakeXhr {
    constructor(status, responseText) {
        this.readyState = 0;
        this.status = status;
        this.responseText = responseText;
        this._listeners = {};
    }

    addEventListener(type, callback) {
        (this._listeners[type] = this._listeners[type] || []).push(callback);
    }

    complete() {
        this.readyState = 4;
        (this._listeners.readystatechange || []).forEach(callback => callback());
    }
}

const eightMiB = 8 * 1024 * 1024;

const makeChunk = (status) => ({
    _status: status,
    loaded: status === 'uploading' ? eightMiB : 0,
    startByte: 0,
    endByte: eightMiB,
    status() {
        return this._status;
    },
    progress() {
        return this._status === 'uploading' ? 0.5 : 1;
    },
});

const createFakeFile = ({ id, pendingChunks = 0, uploadingChunks = 0, size = eightMiB * 6, isFinalizing = false }) => ({
    uniqueIdentifier: id,
    size,
    isFinalizing,
    isSaved: false,
    error: null,
    chunks: [
        ...Array.from({ length: uploadingChunks }, () => makeChunk('uploading')),
        ...Array.from({ length: pendingChunks }, () => makeChunk('pending')),
    ],
});

const createFakeResumable = ({ configuredUploads = 3, files = [{ id: 'file-1', pendingChunks: 6 }] } = {}) => {
    const queueFiles = files.map(file => createFakeFile(file));
    const resumable = {
        files: queueFiles,
        opts: {
            simultaneousUploads: configuredUploads,
            chunkSize: eightMiB,
        },
        getOpt(optionName) {
            return this.opts[optionName];
        },
        uploadNextChunk() {
            for (const file of this.files) {
                const nextChunk = file.chunks.find(chunk => chunk._status === 'pending');
                if (!nextChunk) {
                    continue;
                }
                nextChunk._status = 'uploading';
                nextChunk.loaded = eightMiB;
                return true;
            }
            return false;
        },
    };

    return { resumable, files: queueFiles };
};

const countUploadingChunks = (resumable) => {
    return resumable.files.reduce((count, file) => {
        return count + file.chunks.filter(chunk => chunk._status === 'uploading').length;
    }, 0);
};

const completeOneUploadingChunk = (file) => {
    const chunk = file.chunks.find(candidate => candidate._status === 'uploading');
    if (chunk) {
        chunk._status = 'success';
    }
};

describe('upload finalization helpers', () => {
    test('auto-retries the finalize conflict only once per file until reset', () => {
        const resumableFile = {};
        const message = JSON.stringify({ error: 'library was modified concurrently; retry the upload' });

        expect(shouldAutoRetryUploadConflict(resumableFile, message)).toBe(true);

        markUploadConflictAutoRetry(resumableFile);
        expect(shouldAutoRetryUploadConflict(resumableFile, message)).toBe(false);

        resetUploadConflictAutoRetry(resumableFile);
        expect(shouldAutoRetryUploadConflict(resumableFile, message)).toBe(true);
    });

    test('ignores non-conflict and malformed messages', () => {
        expect(shouldAutoRetryUploadConflict({}, JSON.stringify({ error: 'storage quota exceeded' }))).toBe(false);
        expect(shouldAutoRetryUploadConflict({}, 'not-json')).toBe(false);
        expect(shouldAutoRetryUploadConflict(null, JSON.stringify({ error: 'library was modified concurrently; retry the upload' }))).toBe(false);
    });

    test('prefers tracked http 409 status over response text and clears it with runtime state', () => {
        const resumableFile = { lastUploadResponseStatus: 409 };

        expect(shouldAutoRetryUploadConflict(resumableFile, JSON.stringify({ error: 'storage quota exceeded' }))).toBe(true);

        clearFileUploadRuntimeState(resumableFile);
        expect(shouldAutoRetryUploadConflict(resumableFile, JSON.stringify({ error: 'storage quota exceeded' }))).toBe(false);
    });

    test('suppresses the next global upload error toast only once after marking an auto-retry', () => {
        const resumableFile = {};

        markUploadConflictAutoRetry(resumableFile);

        expect(consumeSuppressedUploadErrorToast(resumableFile)).toBe(true);
        expect(consumeSuppressedUploadErrorToast(resumableFile)).toBe(false);
    });

    test('moves a failed auto-retry back to manual retry state and resets auto-retry flags', () => {
        const resumableFile = {
            uniqueIdentifier: 'file-1',
            isFinalizing: true,
            lastUploadResponseStatus: 409,
            finalizeConflictAutoRetried: true,
            suppressNextUploadErrorToast: true,
        };
        const uploadFileList = [resumableFile];

        const nextState = moveUploadToRetryState(uploadFileList, [], resumableFile, 'link refresh failed', { resetAutoRetry: true });

        expect(nextState.retryFileList).toHaveLength(1);
        expect(nextState.retryFileList[0]).toBe(resumableFile);
        expect(nextState.uploadFileList[0].error).toBe('link refresh failed');
        expect(nextState.uploadFileList[0].isFinalizing).toBe(false);
        expect(nextState.uploadFileList[0].lastUploadResponseStatus).toBeNull();
        expect(nextState.uploadFileList[0].finalizeConflictAutoRetried).toBe(false);
        expect(nextState.uploadFileList[0].suppressNextUploadErrorToast).toBe(false);
    });
});

describe('adaptive upload concurrency helpers', () => {
    test('starts every upload session at one slot even when the configured max is higher', () => {
        expect(getInitialSimultaneousUploads(3)).toBe(1);

        const { resumable } = createFakeResumable();
        const cleanup = initializeAdaptiveUploadConcurrency(resumable, 3);

        expect(resumable.uploadNextChunk()).toBe(true);
        expect(resumable.uploadNextChunk()).toBe(false);
        expect(countUploadingChunks(resumable)).toBe(1);
        expect(resumable.opts.simultaneousUploads).toBe(1);

        cleanup();
    });

    test('ramps up toward the configured ceiling on stable high-throughput uploads', () => {
        const { resumable, files } = createFakeResumable();
        const cleanup = initializeAdaptiveUploadConcurrency(resumable, 3);
        const resumableFile = files[0];

        resumable.uploadNextChunk();

        for (let sample = 0; sample < 3; sample++) {
            updateAdaptiveUploadConcurrency(resumable, resumableFile, 20 * 1024 * 1024);
        }
        expect(resumable.opts.simultaneousUploads).toBe(2);
        expect(countUploadingChunks(resumable)).toBe(2);

        for (let sample = 0; sample < 5; sample++) {
            updateAdaptiveUploadConcurrency(resumable, resumableFile, 30 * 1024 * 1024);
        }
        expect(resumable.opts.simultaneousUploads).toBe(3);
        expect(countUploadingChunks(resumable)).toBe(3);

        cleanup();
    });

    test('drops back to one slot on retry and lets in-flight extras drain without aborting them', () => {
        const { resumable, files } = createFakeResumable();
        const cleanup = initializeAdaptiveUploadConcurrency(resumable, 3);
        const resumableFile = files[0];

        resumable.uploadNextChunk();
        for (let sample = 0; sample < 3; sample++) {
            updateAdaptiveUploadConcurrency(resumable, resumableFile, 20 * 1024 * 1024);
        }
        expect(countUploadingChunks(resumable)).toBe(2);

        noteAdaptiveUploadRetry(resumable);
        expect(resumable.opts.simultaneousUploads).toBe(1);

        completeOneUploadingChunk(resumableFile);
        expect(resumable.uploadNextChunk()).toBe(false);
        expect(countUploadingChunks(resumable)).toBe(1);

        cleanup();
    });

    test('network changes reset the adaptive target back to one slot', () => {
        const { resumable, files } = createFakeResumable();
        const cleanup = initializeAdaptiveUploadConcurrency(resumable, 3);
        const resumableFile = files[0];

        resumable.uploadNextChunk();
        for (let sample = 0; sample < 3; sample++) {
            updateAdaptiveUploadConcurrency(resumable, resumableFile, 20 * 1024 * 1024);
        }
        expect(resumable.opts.simultaneousUploads).toBe(2);

        window.dispatchEvent(new Event('offline'));
        expect(resumable.opts.simultaneousUploads).toBe(1);

        cleanup();
    });

    test('finalizing files can still grant one temporary extra slot above the adaptive target', () => {
        const { resumable } = createFakeResumable({
            files: [
                { id: 'saving-file', uploadingChunks: 1, pendingChunks: 0, isFinalizing: true, size: eightMiB * 6 },
                { id: 'queued-file', pendingChunks: 2, uploadingChunks: 0, size: eightMiB * 6 },
            ],
        });
        const cleanup = initializeAdaptiveUploadConcurrency(resumable, 3);

        expect(countUploadingChunks(resumable)).toBe(1);
        expect(maybeStartPendingUploadDuringFinalize(resumable)).toBe(true);
        expect(countUploadingChunks(resumable)).toBe(2);

        resetAdaptiveUploadConcurrency(resumable, 3);
        cleanup();
    });
});

describe('upload finalize result resolution', () => {
    const finalizeEntry = { name: 'big.zip', id: 'abc123', size: '2684354560' };

    test('parses a finalize array into its entry and rejects intermediate acks', () => {
        expect(parseUploadSuccessEntry(JSON.stringify([finalizeEntry]))).toEqual(finalizeEntry);
        expect(parseUploadSuccessEntry(JSON.stringify({ success: true }))).toBeNull();
        expect(parseUploadSuccessEntry('[]')).toBeNull();
        expect(parseUploadSuccessEntry('not-json')).toBeNull();
        expect(parseUploadSuccessEntry('')).toBeNull();
        expect(parseUploadSuccessEntry(null)).toBeNull();
    });

    test('resolves directly from a finalize-array message', () => {
        expect(resolveUploadSuccessResult({}, JSON.stringify([finalizeEntry]), false)).toEqual({ entry: finalizeEntry });
    });

    test('falls back to captured metadata when fileSuccess receives an intermediate ack', () => {
        // Reproduces production: the finalize chunk and an intermediate chunk are
        // both in flight. The finalize chunk responds first with the file array;
        // the intermediate ack ({"success":true}) responds last, so resumable.js
        // hands THAT body to fileSuccess. Before the fix this crashed on .id.
        const resumableFile = {};
        const finalizeChunk = { xhr: new FakeXhr(200, JSON.stringify([finalizeEntry])) };
        const ackChunk = { xhr: new FakeXhr(200, JSON.stringify({ success: true })) };

        trackUploadResponseStatus(resumableFile, finalizeChunk);
        trackUploadResponseStatus(resumableFile, ackChunk);

        finalizeChunk.xhr.complete();
        ackChunk.xhr.complete();

        expect(resumableFile.finalizeResult).toEqual(finalizeEntry);
        expect(resolveUploadSuccessResult(resumableFile, ackChunk.xhr.responseText, false)).toEqual({ entry: finalizeEntry });
    });

    test('a late intermediate ack does not clobber already-captured finalize metadata', () => {
        const resumableFile = {};
        const finalizeChunk = { xhr: new FakeXhr(200, JSON.stringify([finalizeEntry])) };
        const ackChunk = { xhr: new FakeXhr(200, JSON.stringify({ success: true })) };

        trackUploadResponseStatus(resumableFile, finalizeChunk);
        trackUploadResponseStatus(resumableFile, ackChunk);

        finalizeChunk.xhr.complete();
        ackChunk.xhr.complete();

        expect(resumableFile.finalizeResult).toEqual(finalizeEntry);
    });

    test('replace mode resolves the raw file id and falls back to captured raw id', () => {
        expect(resolveUploadSuccessResult({}, 'deadbeefcafe', true)).toEqual({ id: 'deadbeefcafe' });

        const resumableFile = {};
        const finalizeChunk = { xhr: new FakeXhr(200, 'deadbeefcafe') };
        const ackChunk = { xhr: new FakeXhr(200, JSON.stringify({ success: true })) };

        trackUploadResponseStatus(resumableFile, finalizeChunk);
        trackUploadResponseStatus(resumableFile, ackChunk);

        finalizeChunk.xhr.complete();
        ackChunk.xhr.complete();

        expect(resumableFile.finalizeResultRaw).toBe('deadbeefcafe');
        expect(resolveUploadSuccessResult(resumableFile, JSON.stringify({ success: true }), true)).toEqual({ id: 'deadbeefcafe' });
    });

    test('returns null when no finalize metadata is available anywhere', () => {
        expect(resolveUploadSuccessResult({}, JSON.stringify({ success: true }), false)).toBeNull();
        expect(resolveUploadSuccessResult({}, JSON.stringify({ success: true }), true)).toBeNull();
    });

    test('re-tracks a reused chunk when resumable.js swaps in a fresh XHR on retry', () => {
        // resumable.js keeps the same chunk object across retries but assigns a
        // new XHR. A per-chunk guard would leave the retry XHR untracked; the
        // per-XHR guard must re-attach so the retry response is still captured.
        const resumableFile = {};
        const chunk = { xhr: new FakeXhr(0, '') };

        trackUploadResponseStatus(resumableFile, chunk);
        chunk.xhr.complete(); // first attempt failed (status 0, no body)
        expect(resumableFile.finalizeResult).toBeUndefined();

        // Retry: same chunk object, brand-new XHR carrying the finalize array.
        chunk.xhr = new FakeXhr(200, JSON.stringify([finalizeEntry]));
        trackUploadResponseStatus(resumableFile, chunk);
        chunk.xhr.complete();

        expect(resumableFile.finalizeResult).toEqual(finalizeEntry);
    });

    test('clearing runtime state drops captured finalize metadata so retries start clean', () => {
        const resumableFile = { finalizeResult: finalizeEntry, finalizeResultRaw: 'deadbeefcafe' };
        clearFileUploadRuntimeState(resumableFile);
        expect(resumableFile.finalizeResult).toBeNull();
        expect(resumableFile.finalizeResultRaw).toBeNull();
    });
});
