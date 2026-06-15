import {
    clearFileUploadRuntimeState,
    consumeSuppressedUploadErrorToast,
    markUploadConflictAutoRetry,
    moveUploadToRetryState,
    parseUploadSuccessEntry,
    resetUploadConflictAutoRetry,
    resolveUploadSuccessResult,
    shouldAutoRetryUploadConflict,
    trackUploadResponseStatus,
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

    test('clearing runtime state drops captured finalize metadata so retries start clean', () => {
        const resumableFile = { finalizeResult: finalizeEntry, finalizeResultRaw: 'deadbeefcafe' };
        clearFileUploadRuntimeState(resumableFile);
        expect(resumableFile.finalizeResult).toBeNull();
        expect(resumableFile.finalizeResultRaw).toBeNull();
    });
});
