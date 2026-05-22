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
    if (options.resetRemainingTime) {
        resumableFile.remainingTime = DEFAULT_PREPARING_TIME;
    }
};

export const trackUploadResponseStatus = (resumableFile, resumableChunk) => {
    if (!resumableFile || !resumableChunk || !resumableChunk.xhr || resumableChunk._sesamefsResponseTrackerAttached) {
        return;
    }

    const xhr = resumableChunk.xhr;
    resumableChunk._sesamefsResponseTrackerAttached = true;
    xhr.addEventListener('readystatechange', () => {
        if (xhr.readyState !== 4) {
            return;
        }

        resumableFile.lastUploadResponseStatus = normalizeUploadResponseStatus(xhr.status);
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
