import {
    clearFileUploadRuntimeState,
    consumeSuppressedUploadErrorToast,
    markUploadConflictAutoRetry,
    moveUploadToRetryState,
    resetUploadConflictAutoRetry,
    shouldAutoRetryUploadConflict,
} from '../upload-finalization';

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
