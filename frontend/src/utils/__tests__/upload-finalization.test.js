import {
    clearFileUploadRuntimeState,
    consumeSuppressedUploadErrorToast,
    getInitialSimultaneousUploads,
    initializeAdaptiveUploadConcurrency,
    isFileSaving,
    isLibraryEncryptedError,
    markUploadConflictAutoRetry,
    maybeStartPendingUploadDuringFinalize,
    moveUploadToRetryState,
    noteAdaptiveUploadFailure,
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

describe('isLibraryEncryptedError', () => {
    test('detects the backend "Library is encrypted" 403 (upload-link fetch)', () => {
        expect(isLibraryEncryptedError({
            response: { status: 403, data: { error: 'Library is encrypted', error_msg: 'unlock it' } },
        })).toBe(true);
    });

    test('detects a 403 carrying lib_need_decrypt', () => {
        expect(isLibraryEncryptedError({ response: { status: 403, data: { lib_need_decrypt: true } } })).toBe(true);
    });

    test('ignores other 403s and non-403 errors', () => {
        expect(isLibraryEncryptedError({ response: { status: 403, data: { error: 'Permission denied' } } })).toBe(false);
        expect(isLibraryEncryptedError({ response: { status: 500, data: { error: 'Library is encrypted' } } })).toBe(false);
        expect(isLibraryEncryptedError({ response: { status: 403 } })).toBe(false);
        expect(isLibraryEncryptedError(new Error('network'))).toBe(false);
        expect(isLibraryEncryptedError(null)).toBe(false);
    });
});

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

    test('small-file-only queues keep the configured parallelism instead of staying serialized', () => {
        const { resumable } = createFakeResumable({
            configuredUploads: 3,
            files: [
                { id: 'small-1', pendingChunks: 1, size: eightMiB },
                { id: 'small-2', pendingChunks: 1, size: eightMiB },
                { id: 'small-3', pendingChunks: 1, size: eightMiB },
            ],
        });
        const cleanup = initializeAdaptiveUploadConcurrency(resumable, 3);

        expect(resumable.uploadNextChunk()).toBe(true);
        expect(resumable.opts.simultaneousUploads).toBe(3);
        expect(countUploadingChunks(resumable)).toBe(3);

        cleanup();
    });

    test('small-file-only queues still back off to one slot on 429 and stay there during cooldown', () => {
        const { resumable } = createFakeResumable({
            configuredUploads: 3,
            files: [
                { id: 'small-1', pendingChunks: 2, size: eightMiB },
                { id: 'small-2', pendingChunks: 2, size: eightMiB },
                { id: 'small-3', pendingChunks: 2, size: eightMiB },
            ],
        });
        const cleanup = initializeAdaptiveUploadConcurrency(resumable, 3);

        const completeAllUploading = () => {
            resumable.files.forEach(file => file.chunks.forEach(chunk => {
                if (chunk._status === 'uploading') {
                    chunk._status = 'success';
                }
            }));
        };

        resumable.uploadNextChunk();
        expect(resumable.opts.simultaneousUploads).toBe(3);
        expect(countUploadingChunks(resumable)).toBe(3);

        // Server backpressure must lower the target even though the queue has no
        // adaptive-eligible (large) file to drive the ramp logic.
        noteAdaptiveUploadFailure(resumable, { lastUploadResponseStatus: 429 }, 'rate limited');
        expect(resumable.opts.simultaneousUploads).toBe(1);

        // During the cooldown window the queue must not refill back up to 3.
        completeAllUploading();
        resumable.uploadNextChunk();
        expect(resumable.opts.simultaneousUploads).toBe(1);
        expect(countUploadingChunks(resumable)).toBe(1);

        // Once the cooldown expires, small-file batches recover to the ceiling.
        resumable._sesamefsAdaptiveUpload.cooldownUntil = 0;
        completeAllUploading();
        resumable.uploadNextChunk();
        expect(resumable.opts.simultaneousUploads).toBe(3);

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

    test('server backpressure (429) and 5xx lower the adaptive target, client 4xx does not', () => {
        const { resumable, files } = createFakeResumable();
        const cleanup = initializeAdaptiveUploadConcurrency(resumable, 3);
        const resumableFile = files[0];

        resumable.uploadNextChunk();
        for (let sample = 0; sample < 3; sample++) {
            updateAdaptiveUploadConcurrency(resumable, resumableFile, 20 * 1024 * 1024);
        }
        expect(resumable.opts.simultaneousUploads).toBe(2);

        // A plain client error (e.g. 409 conflict) is not a concurrency signal.
        expect(noteAdaptiveUploadFailure(resumable, { lastUploadResponseStatus: 409 }, 'conflict')).toBe(false);
        expect(resumable.opts.simultaneousUploads).toBe(2);

        // 429 is the server explicitly asking for less parallelism.
        expect(noteAdaptiveUploadFailure(resumable, { lastUploadResponseStatus: 429 }, 'slow down')).toBe(true);
        expect(resumable.opts.simultaneousUploads).toBe(1);

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

    test('finalizing files can still use spare configured capacity without exceeding the ceiling', () => {
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
    test('a finalizing file keeps one replacement slot open across later chunk completions', () => {
        const { resumable, files } = createFakeResumable({
            files: [
                { id: 'saving-file', uploadingChunks: 1, pendingChunks: 0, isFinalizing: true, size: eightMiB * 6 },
                { id: 'queued-file', pendingChunks: 2, uploadingChunks: 0, size: eightMiB * 6 },
            ],
        });
        const cleanup = initializeAdaptiveUploadConcurrency(resumable, 3);
        const queuedFile = files[1];

        expect(maybeStartPendingUploadDuringFinalize(resumable)).toBe(true);
        expect(countUploadingChunks(resumable)).toBe(2);

        completeOneUploadingChunk(queuedFile);
        expect(countUploadingChunks(resumable)).toBe(1);

        expect(resumable.uploadNextChunk()).toBe(true);
        expect(countUploadingChunks(resumable)).toBe(2);

        cleanup();
    });

    test('files awaiting server finalize can free a replacement slot even before the UI flag is set', () => {
        const { resumable } = createFakeResumable({
            files: [
                { id: 'saving-file', uploadingChunks: 1, pendingChunks: 0, isFinalizing: false, size: eightMiB * 6 },
                { id: 'queued-file', pendingChunks: 1, uploadingChunks: 0, size: eightMiB * 6 },
            ],
        });
        const cleanup = initializeAdaptiveUploadConcurrency(resumable, 3);

        expect(countUploadingChunks(resumable)).toBe(1);
        expect(maybeStartPendingUploadDuringFinalize(resumable)).toBe(true);
        expect(countUploadingChunks(resumable)).toBe(2);

        cleanup();
    });

    test('finalizing files do not open an extra slot once the configured ceiling is already full', () => {
        const { resumable, files } = createFakeResumable({
            configuredUploads: 3,
            files: [
                { id: 'active-file', pendingChunks: 6, size: eightMiB * 6 },
                { id: 'queued-file', pendingChunks: 6, size: eightMiB * 6 },
            ],
        });
        const cleanup = initializeAdaptiveUploadConcurrency(resumable, 3);
        const activeFile = files[0];
        const finalizingFile = files[1];

        resumable.uploadNextChunk();

        for (let sample = 0; sample < 3; sample++) {
            updateAdaptiveUploadConcurrency(resumable, activeFile, 20 * 1024 * 1024);
        }
        for (let sample = 0; sample < 5; sample++) {
            updateAdaptiveUploadConcurrency(resumable, activeFile, 30 * 1024 * 1024);
        }

        expect(resumable.opts.simultaneousUploads).toBe(3);
        expect(countUploadingChunks(resumable)).toBe(3);

        finalizingFile.isFinalizing = true;

        expect(maybeStartPendingUploadDuringFinalize(resumable)).toBe(false);
        expect(countUploadingChunks(resumable)).toBe(3);

        cleanup();
    });
    test('server-side finalize waits do not degrade adaptive concurrency on bitrate drops', () => {
        const { resumable, files } = createFakeResumable({
            configuredUploads: 3,
            files: [
                { id: 'active-file', pendingChunks: 6, size: eightMiB * 6 },
                { id: 'queued-file', pendingChunks: 6, size: eightMiB * 6 },
            ],
        });
        const cleanup = initializeAdaptiveUploadConcurrency(resumable, 3);
        const activeFile = files[0];
        const finalizingFile = files[1];

        resumable.uploadNextChunk();
        for (let sample = 0; sample < 3; sample++) {
            updateAdaptiveUploadConcurrency(resumable, activeFile, 20 * 1024 * 1024);
        }
        for (let sample = 0; sample < 5; sample++) {
            updateAdaptiveUploadConcurrency(resumable, activeFile, 30 * 1024 * 1024);
        }

        const state = resumable._sesamefsAdaptiveUpload;
        expect(state.effective).toBe(3);
        expect(state.cooldownUntil).toBe(0);

        finalizingFile.isFinalizing = false;
        finalizingFile.chunks = [makeChunk('uploading')];

        expect(updateAdaptiveUploadConcurrency(resumable, activeFile, 5 * 1024 * 1024)).toBe(false);
        expect(state.effective).toBe(3);
        expect(state.cooldownUntil).toBe(0);

        cleanup();
    });

    test('server backpressure still lowers adaptive concurrency even while a file is awaiting finalize', () => {
        const { resumable, files } = createFakeResumable({
            configuredUploads: 3,
            files: [
                { id: 'active-file', pendingChunks: 6, size: eightMiB * 6 },
                { id: 'queued-file', pendingChunks: 6, size: eightMiB * 6 },
            ],
        });
        const cleanup = initializeAdaptiveUploadConcurrency(resumable, 3);
        const activeFile = files[0];
        const finalizingFile = files[1];

        resumable.uploadNextChunk();
        for (let sample = 0; sample < 3; sample++) {
            updateAdaptiveUploadConcurrency(resumable, activeFile, 20 * 1024 * 1024);
        }
        for (let sample = 0; sample < 5; sample++) {
            updateAdaptiveUploadConcurrency(resumable, activeFile, 30 * 1024 * 1024);
        }

        const state = resumable._sesamefsAdaptiveUpload;
        finalizingFile.isFinalizing = false;
        finalizingFile.chunks = [makeChunk('uploading')];

        expect(noteAdaptiveUploadFailure(resumable, { lastUploadResponseStatus: 429 }, 'rate limited')).toBe(true);
        expect(state.effective).toBe(1);
        expect(state.cooldownUntil).toBeGreaterThan(0);

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

describe('block-upload entry state (isFileSaving)', () => {
    // Regression: a block entry parked remainingTime at 0, which made the legacy
    // resumable heuristic report "Saving..." for the entire upload (the 3% bug).
    test('a block entry mid-upload is NOT saving even with remainingTime 0', () => {
        const entry = { isBlockUpload: true, _phase: 'uploading', remainingTime: 0, isSaved: false };
        expect(isFileSaving(entry)).toBe(false);
    });

    test('a block entry is saving ONLY during the commit phase', () => {
        expect(isFileSaving({ isBlockUpload: true, _phase: 'hashing' })).toBe(false);
        expect(isFileSaving({ isBlockUpload: true, _phase: 'uploading' })).toBe(false);
        expect(isFileSaving({ isBlockUpload: true, _phase: 'saving' })).toBe(true);
    });

    test('a saved block entry is never saving', () => {
        expect(isFileSaving({ isBlockUpload: true, _phase: 'saving', isSaved: true })).toBe(false);
    });

    test('non-block resumable files keep the legacy remainingTime heuristic', () => {
        expect(isFileSaving({ remainingTime: 0, isSaved: false })).toBe(true);
        expect(isFileSaving({ remainingTime: 5, isSaved: false })).toBe(false);
    });
});

// The per-entry block-upload bitrate sampler was replaced by the shared sliding-
// window meter; its tests live in utils/__tests__/upload-throughput-meter.test.js.
