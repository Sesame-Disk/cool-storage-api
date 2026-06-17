const DEFAULT_PREPARING_TIME = -1;
const RETRYABLE_UPLOAD_CONFLICT_ERROR = 'library was modified concurrently; retry the upload';

const normalizeUploadResponseStatus = (status) => {
    const numericStatus = Number(status);
    return Number.isInteger(numericStatus) && numericStatus > 0 ? numericStatus : 0;
};

const parseUploadErrorPayload = (message) => {
    if (!message) {
        return null;
    }

    if (typeof message === 'object') {
        return message;
    }

    if (typeof message !== 'string') {
        return null;
    }

    try {
        return JSON.parse(message.replace(/\n/g, ''));
    } catch (error) {
        void error;
        return null;
    }
};

const getFileChunks = (resumableFile) => {
    if (!resumableFile || !Array.isArray(resumableFile.chunks)) {
        return [];
    }

    return resumableFile.chunks;
};

// A finalize entry is the {name, id, size} object the server returns once a file
// is fully written. An intermediate-chunk ack ({"success":true}) has neither a
// name nor an id, so it is rejected here. `id` may legitimately be "" for an
// empty-directory marker, so we only require the field to be present.
const isFinalizeEntry = (entry) => {
    return Boolean(entry)
        && typeof entry === 'object'
        && !Array.isArray(entry)
        && 'id' in entry
        && typeof entry.name === 'string';
};

// Replace-mode finalize returns the raw file id as plain text (not JSON). An
// intermediate ack ({"success":true}) is valid JSON, so JSON.parse succeeding
// means the body is structured and therefore NOT a raw id.
const extractRawFinalizeId = (responseText) => {
    if (typeof responseText !== 'string') {
        return '';
    }
    const trimmed = responseText.trim();
    if (!trimmed) {
        return '';
    }
    try {
        JSON.parse(trimmed);
        return '';
    } catch (error) {
        void error;
        return trimmed;
    }
};

// Pull the finalize entry out of a chunk response body. Returns null for an
// intermediate ack or any body that does not carry file metadata.
export const parseUploadSuccessEntry = (message) => {
    if (message && typeof message === 'object') {
        if (Array.isArray(message)) {
            return isFinalizeEntry(message[0]) ? message[0] : null;
        }
        return isFinalizeEntry(message) ? message : null;
    }

    if (typeof message !== 'string' || message === '') {
        return null;
    }

    let parsed;
    try {
        parsed = JSON.parse(message);
    } catch (error) {
        void error;
        return null;
    }

    if (Array.isArray(parsed)) {
        return isFinalizeEntry(parsed[0]) ? parsed[0] : null;
    }

    return null;
};

const captureFinalizeResponse = (resumableFile, xhr) => {
    if (!resumableFile || !xhr) {
        return;
    }

    const status = normalizeUploadResponseStatus(xhr.status);
    if (status !== 200 && status !== 201) {
        return;
    }

    const responseText = typeof xhr.responseText === 'string' ? xhr.responseText : '';
    if (!responseText) {
        return;
    }

    const entry = parseUploadSuccessEntry(responseText);
    if (entry) {
        resumableFile.finalizeResult = entry;
        return;
    }

    const rawId = extractRawFinalizeId(responseText);
    if (rawId) {
        resumableFile.finalizeResultRaw = rawId;
    }
};

// Resolve the authoritative finalize result for a file, regardless of which
// chunk's body resumable.js happened to hand to fileSuccess. Prefers the
// directly-delivered message, then falls back to the metadata captured from
// whichever chunk actually carried it. Returns { entry } for normal/folder
// uploads, { id } for replace uploads, or null when no finalize metadata is
// available anywhere.
export const resolveUploadSuccessResult = (resumableFile, message, isReplace) => {
    if (isReplace) {
        const rawId = extractRawFinalizeId(typeof message === 'string' ? message : '');
        if (rawId) {
            return { id: rawId };
        }
        if (resumableFile && resumableFile.finalizeResultRaw) {
            return { id: resumableFile.finalizeResultRaw };
        }
        if (resumableFile && resumableFile.finalizeResult && resumableFile.finalizeResult.id) {
            return { id: resumableFile.finalizeResult.id };
        }
        return null;
    }

    const entry = parseUploadSuccessEntry(message);
    if (entry) {
        return { entry };
    }
    if (resumableFile && resumableFile.finalizeResult) {
        return { entry: resumableFile.finalizeResult };
    }
    return null;
};

const getChunkStatus = (chunk) => {
    if (!chunk || typeof chunk.status !== 'function') {
        return '';
    }

    return chunk.status();
};

const chunkBytesLoaded = (chunk) => {
    const loaded = Number(chunk?.loaded || 0);
    const startByte = Number(chunk?.startByte || 0);
    const endByte = Number(chunk?.endByte || 0);
    return loaded >= Math.max(0, endByte - startByte);
};

const chunkProgressLooksComplete = (chunk) => {
    if (!chunk || typeof chunk.progress !== 'function') {
        return false;
    }

    // ResumableJS caps an active XHR at 95% until the server response arrives.
    // Seeing that cap means browser bytes are done and the request is waiting
    // on server-side finalization.
    return chunk.progress() >= 0.94;
};

const isAwaitingServerFinalize = (resumableFile) => {
    const chunks = getFileChunks(resumableFile);
    if (chunks.length === 0) {
        return false;
    }

    if (chunks.some(chunk => getChunkStatus(chunk) === 'pending')) {
        return false;
    }

    const uploadingChunks = chunks.filter(chunk => getChunkStatus(chunk) === 'uploading');
    if (uploadingChunks.length === 0) {
        return false;
    }

    return uploadingChunks.every(chunk => chunkBytesLoaded(chunk) || chunkProgressLooksComplete(chunk));
};

const hasFinalizingFiles = (resumable) => {
    if (!resumable || !Array.isArray(resumable.files)) {
        return false;
    }

    return resumable.files.some(file => file.isFinalizing && !file.isSaved && !file.error);
};

const getFinalizingFiles = (resumable) => {
    if (!resumable || !Array.isArray(resumable.files)) {
        return [];
    }

    return resumable.files.filter(file => file.isFinalizing && !file.isSaved && !file.error);
};

const releaseInactiveFinalizeSlot = (resumable) => {
    if (!resumable || !resumable._finalizeSlotOwner) {
        return;
    }

    const owner = getFinalizingFiles(resumable).find(file => file.uniqueIdentifier === resumable._finalizeSlotOwner);
    if (!owner) {
        resumable._finalizeSlotOwner = null;
    }
};

const grantTemporaryFinalizeSlot = (resumable) => {
    if (!resumable || typeof resumable.uploadNextChunk !== 'function') {
        return false;
    }

    releaseInactiveFinalizeSlot(resumable);
    if (resumable._finalizeSlotOwner) {
        return false;
    }

    const file = getFinalizingFiles(resumable)[0];
    if (!file) {
        return false;
    }
    const started = resumable.uploadNextChunk();
    resumable._finalizeSlotOwner = started ? file.uniqueIdentifier : null;
    return started;
};

export const getBaselineSimultaneousUploads = (configuredUploads) => {
    return configuredUploads || 1;
};

export const isFileSaving = (resumableFile) => {
    return !resumableFile.isSaved && (
        Boolean(resumableFile.isFinalizing) ||
        isAwaitingServerFinalize(resumableFile) ||
        resumableFile.remainingTime === 0
    );
};

export const maybeStartPendingUploadDuringFinalize = (resumable) => {
    if (!hasFinalizingFiles(resumable)) {
        return false;
    }

    return grantTemporaryFinalizeSlot(resumable);
};

export const clearFileUploadRuntimeState = (resumableFile, options = {}) => {
    if (!resumableFile) {
        return;
    }

    resumableFile.isFinalizing = false;
    resumableFile.lastUploadResponseStatus = null;
    resumableFile.suppressNextUploadErrorToast = false;
    resumableFile.finalizeResult = null;
    resumableFile.finalizeResultRaw = null;
    if (options.resetRemainingTime) {
        resumableFile.remainingTime = DEFAULT_PREPARING_TIME;
    }
};

export const trackUploadResponseStatus = (resumableFile, resumableChunk) => {
    if (!resumableFile || !resumableChunk || !resumableChunk.xhr) {
        return;
    }

    const xhr = resumableChunk.xhr;
    // Guard per-XHR, not per-chunk: resumable.js reuses the same chunk object
    // with a fresh XHR on retry, so a per-chunk flag would leave the retry's XHR
    // untracked (no status/finalize capture). Re-attach whenever the XHR changes.
    if (resumableChunk._sesamefsTrackedXhr === xhr) {
        return;
    }
    resumableChunk._sesamefsTrackedXhr = xhr;
    xhr.addEventListener('readystatechange', () => {
        if (xhr.readyState !== 4) {
            return;
        }

        resumableFile.lastUploadResponseStatus = normalizeUploadResponseStatus(xhr.status);
        // Capture finalize metadata from whichever chunk carried it. resumable.js
        // only hands fileSuccess the body of the last-finishing chunk, which may
        // be an intermediate ack; this lets onFileUploadSuccess recover the real
        // {name, id, size} (or raw id) no matter which chunk responded last.
        captureFinalizeResponse(resumableFile, xhr);
    });
};

export const shouldAutoRetryUploadConflict = (resumableFile, message) => {
    if (!resumableFile || resumableFile.finalizeConflictAutoRetried) {
        return false;
    }

    if (normalizeUploadResponseStatus(resumableFile.lastUploadResponseStatus) === 409) {
        return true;
    }

    const payload = parseUploadErrorPayload(message);
    if (normalizeUploadResponseStatus(payload && payload.status) === 409) {
        return true;
    }

    if (payload && payload.error === RETRYABLE_UPLOAD_CONFLICT_ERROR) {
        return true;
    }

    return typeof message === 'string' && message.includes(RETRYABLE_UPLOAD_CONFLICT_ERROR);
};

export const markUploadConflictAutoRetry = (resumableFile) => {
    if (!resumableFile) {
        return;
    }

    resumableFile.finalizeConflictAutoRetried = true;
    resumableFile.suppressNextUploadErrorToast = true;
};

export const resetUploadConflictAutoRetry = (resumableFile) => {
    if (!resumableFile) {
        return;
    }

    resumableFile.finalizeConflictAutoRetried = false;
    resumableFile.suppressNextUploadErrorToast = false;
};

export const consumeSuppressedUploadErrorToast = (resumableFile) => {
    if (!resumableFile || !resumableFile.suppressNextUploadErrorToast) {
        return false;
    }

    resumableFile.suppressNextUploadErrorToast = false;
    return true;
};

export const moveUploadToRetryState = (uploadFileList, retryFileList, resumableFile, error, options = {}) => {
    const { resetAutoRetry = false } = options;
    const nextRetryFileList = Array.isArray(retryFileList) ? retryFileList.slice() : [];
    const nextUploadFileList = Array.isArray(uploadFileList) ? uploadFileList.map(item => {
        if (!resumableFile || item.uniqueIdentifier !== resumableFile.uniqueIdentifier) {
            return item;
        }

        if (!nextRetryFileList.some(retryItem => retryItem.uniqueIdentifier === item.uniqueIdentifier)) {
            nextRetryFileList.push(item);
        }
        clearFileUploadRuntimeState(item);
        if (resetAutoRetry) {
            resetUploadConflictAutoRetry(item);
        }
        item.error = error;
        return item;
    }) : [];

    return {
        retryFileList: nextRetryFileList,
        uploadFileList: nextUploadFileList,
    };
};

export const maybeMarkFileFinalizing = (resumableFile, resumable, baseline) => {
    if (!resumableFile || resumableFile.isSaved || resumableFile.error || resumableFile.isFinalizing) {
        return false;
    }

    if (!isAwaitingServerFinalize(resumableFile)) {
        return false;
    }

    resumableFile.isFinalizing = true;
    // Resumable.upload() is a no-op while any chunk XHR is active. Keep one
    // queue-level replacement slot so a saving file can unblock the next upload
    // without letting effective concurrency grow with every finalizing file.
    grantTemporaryFinalizeSlot(resumable);
    return true;
};

export const restoreUploadConcurrencyIfIdle = (resumable, baseline) => {
    releaseInactiveFinalizeSlot(resumable);
    grantTemporaryFinalizeSlot(resumable);
    void baseline;
};
