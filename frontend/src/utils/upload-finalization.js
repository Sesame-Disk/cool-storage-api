const DEFAULT_PREPARING_TIME = -1;

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

    return uploadingChunks.every(chunkBytesLoaded);
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
    return !resumableFile.isSaved && Boolean(resumableFile.isFinalizing);
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
    if (options.resetRemainingTime) {
        resumableFile.remainingTime = DEFAULT_PREPARING_TIME;
    }
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
