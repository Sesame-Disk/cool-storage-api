import {
    clearFileUploadRuntimeState,
    markUploadConflictAutoRetry,
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
});