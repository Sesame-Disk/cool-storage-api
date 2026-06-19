const DEFAULT_PREPARING_TIME = -1;
const RETRYABLE_UPLOAD_CONFLICT_ERROR = 'library was modified concurrently; retry the upload';
const ADAPTIVE_UPLOAD_MIN_CONCURRENCY = 1;
const ADAPTIVE_UPLOAD_MIN_FILE_CHUNKS = 3;
const ADAPTIVE_UPLOAD_STABLE_FLOOR_RATIO = 0.7;
const ADAPTIVE_UPLOAD_DROP_RATIO = 0.55;
const ADAPTIVE_UPLOAD_SMOOTHING_FACTOR = 0.7;
const ADAPTIVE_UPLOAD_FIRST_RAMP_SAMPLES = 3;
const ADAPTIVE_UPLOAD_NEXT_RAMP_SAMPLES = 5;
const ADAPTIVE_UPLOAD_GAIN_RATIO = 1.05;
const ADAPTIVE_UPLOAD_COOLDOWN_MS = 10000;

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

const getResumableOption = (resumable, optionName) => {
    if (!resumable) {
        return undefined;
    }
    if (typeof resumable.getOpt === 'function') {
        return resumable.getOpt(optionName);
    }
    return resumable.opts ? resumable.opts[optionName] : undefined;
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

const isTrackedFinalizingFile = (resumableFile) => {
    return Boolean(resumableFile)
        && !resumableFile.isSaved
        && !resumableFile.error
        && (
            Boolean(resumableFile.isFinalizing) ||
            isAwaitingServerFinalize(resumableFile)
        );
};

const hasFinalizingFiles = (resumable) => {
    if (!resumable || !Array.isArray(resumable.files)) {
        return false;
    }

    return resumable.files.some(isTrackedFinalizingFile);
};

const getFinalizingFiles = (resumable) => {
    if (!resumable || !Array.isArray(resumable.files)) {
        return [];
    }

    return resumable.files.filter(isTrackedFinalizingFile);
};

const getAdaptiveUploadState = (resumable) => {
    return resumable ? resumable._sesamefsAdaptiveUpload || null : null;
};

const hasAdaptiveEligibleWork = (resumable) => {
    if (!resumable || !Array.isArray(resumable.files) || resumable.files.length === 0) {
        return false;
    }

    for (const file of resumable.files) {
        if (!file || file.isSaved || file.error) {
            continue;
        }

        const chunks = getFileChunks(file);
        if (chunks.length === 0 || !isAdaptiveEligibleFile(file, resumable)) {
            continue;
        }

        for (const chunk of chunks) {
            const status = getChunkStatus(chunk);
            if (status === 'pending' || status === 'uploading') {
                return true;
            }
        }
    }

    return false;
};

const desiredUploadConcurrency = (resumable, options = {}) => {
    const state = getAdaptiveUploadState(resumable);
    if (!state) {
        return ADAPTIVE_UPLOAD_MIN_CONCURRENCY;
    }

    let desired = state.effective;

    // Small-file-only queues never ramp (their files are not adaptive-eligible),
    // so they default to the full configured ceiling for parallelism. But a
    // recent retry/429/5xx/network drop sets a cooldown and lowers `effective`;
    // honor that backoff window instead of jumping back to `max` on the next
    // chunk, otherwise backpressure would be neutralized for small-file batches.
    const now = options.now ?? Date.now();
    const isCoolingDown = state.cooldownUntil > now;
    if (!isCoolingDown && !hasAdaptiveEligibleWork(resumable)) {
        desired = state.max;
    }

    // A file whose browser bytes are fully sent but whose last chunk is still
    // waiting on the server finalize should not permanently consume one of the
    // active upload slots. Keep one replacement slot available while that
    // finalize is outstanding so later chunk completions can keep refilling the
    // queue until the file is truly done.
    if (options.allowExtraSlot || (options.includeFinalizeReplacement && hasFinalizingFiles(resumable))) {
        desired++;
    }

    return Math.max(ADAPTIVE_UPLOAD_MIN_CONCURRENCY, Math.min(state.max, desired));
};

const syncResumableSimultaneousUploads = (resumable) => {
    if (!resumable || !resumable.opts) {
        return;
    }

    resumable.opts.simultaneousUploads = desiredUploadConcurrency(resumable);
};

// Hot path: called from updateAdaptiveUploadConcurrency on every fileProgress
// event, so iterate with plain loops to avoid per-call intermediate arrays.
const countUploadingChunks = (resumable) => {
    if (!resumable || !Array.isArray(resumable.files)) {
        return 0;
    }

    let count = 0;
    for (const file of resumable.files) {
        const chunks = getFileChunks(file);
        for (const chunk of chunks) {
            if (getChunkStatus(chunk) === 'uploading') {
                count++;
            }
        }
    }
    return count;
};

const hasPendingChunks = (resumable) => {
    if (!resumable || !Array.isArray(resumable.files)) {
        return false;
    }

    for (const file of resumable.files) {
        const chunks = getFileChunks(file);
        for (const chunk of chunks) {
            if (getChunkStatus(chunk) === 'pending') {
                return true;
            }
        }
    }
    return false;
};

const chunkSizeBitsForResumable = (resumable) => {
    const chunkSizeBytes = Number(getResumableOption(resumable, 'chunkSize')) || 0;
    if (chunkSizeBytes <= 0) {
        return 0;
    }
    return chunkSizeBytes * 8;
};

const minimumBitrateForSlots = (resumable, slotCount) => {
    const chunkBits = chunkSizeBitsForResumable(resumable);
    if (chunkBits <= 0 || slotCount <= ADAPTIVE_UPLOAD_MIN_CONCURRENCY) {
        return 0;
    }

    const targetSecondsPerChunk = Math.max(3, 12 / slotCount);
    return chunkBits / targetSecondsPerChunk;
};

const isAdaptiveEligibleFile = (resumableFile, resumable) => {
    const chunkSizeBytes = Number(getResumableOption(resumable, 'chunkSize')) || 0;
    if (chunkSizeBytes <= 0) {
        return true;
    }
    return Number(resumableFile?.size || 0) >= chunkSizeBytes * ADAPTIVE_UPLOAD_MIN_FILE_CHUNKS;
};

const createAdaptiveUploadState = (configuredUploads) => {
    return {
        max: Math.max(ADAPTIVE_UPLOAD_MIN_CONCURRENCY, Number(configuredUploads) || ADAPTIVE_UPLOAD_MIN_CONCURRENCY),
        effective: ADAPTIVE_UPLOAD_MIN_CONCURRENCY,
        stableSamples: 0,
        smoothedBitrate: 0,
        lastBitrate: 0,
        lastRampBitrate: 0,
        cooldownUntil: 0,
    };
};

const startNextUploadChunk = (resumable, options = {}) => {
    if (!resumable) {
        return false;
    }

    const originalUploadNextChunk = resumable._sesamefsOriginalUploadNextChunk || resumable.uploadNextChunk;
    if (typeof originalUploadNextChunk !== 'function') {
        return false;
    }

    const allowedSlots = options.target ?? desiredUploadConcurrency(resumable, { ...options, includeFinalizeReplacement: true });
    if (countUploadingChunks(resumable) >= allowedSlots) {
        return false;
    }

    return Boolean(originalUploadNextChunk.call(resumable));
};

const fillUploadConcurrencySlots = (resumable, precomputedTarget) => {
    const targetSlots = precomputedTarget ?? desiredUploadConcurrency(resumable, { includeFinalizeReplacement: true });
    let started = false;

    for (let attempts = 0; attempts < targetSlots; attempts++) {
        if (countUploadingChunks(resumable) >= targetSlots) {
            break;
        }
        if (!startNextUploadChunk(resumable, { target: targetSlots })) {
            break;
        }
        started = true;
    }

    return started;
};

const resetAdaptiveMetrics = (state) => {
    if (!state) {
        return;
    }

    state.stableSamples = 0;
    state.smoothedBitrate = 0;
    state.lastBitrate = 0;
    state.lastRampBitrate = 0;
    state.cooldownUntil = 0;
};

const setEffectiveUploadConcurrency = (resumable, nextEffectiveSlots, options = {}) => {
    const state = getAdaptiveUploadState(resumable);
    if (!resumable || !state) {
        return false;
    }

    const clampedSlots = Math.max(ADAPTIVE_UPLOAD_MIN_CONCURRENCY, Math.min(state.max, Number(nextEffectiveSlots) || ADAPTIVE_UPLOAD_MIN_CONCURRENCY));
    if (state.effective === clampedSlots) {
        if (options.refill) {
            fillUploadConcurrencySlots(resumable);
        }
        return false;
    }

    state.effective = clampedSlots;
    syncResumableSimultaneousUploads(resumable);
    if (options.refill) {
        fillUploadConcurrencySlots(resumable);
    }
    return true;
};

const degradeAdaptiveUploadConcurrency = (resumable, reason, options = {}) => {
    const state = getAdaptiveUploadState(resumable);
    if (!resumable || !state || state.max <= ADAPTIVE_UPLOAD_MIN_CONCURRENCY) {
        return false;
    }

    const now = options.now ?? Date.now();
    state.stableSamples = 0;
    state.smoothedBitrate = 0;
    state.lastBitrate = 0;
    state.lastRampBitrate = 0;
    state.cooldownUntil = now + ADAPTIVE_UPLOAD_COOLDOWN_MS;
    void reason;
    const changed = setEffectiveUploadConcurrency(resumable, ADAPTIVE_UPLOAD_MIN_CONCURRENCY);
    // For small-file-only queues `effective` is already 1, so setEffective
    // early-returns without syncing; force the now-cooling target into opts so
    // the backoff actually takes hold instead of staying at the ceiling.
    syncResumableSimultaneousUploads(resumable);
    return changed;
};

const extractAdaptivePenaltyStatus = (resumableFile, message) => {
    const trackedStatus = normalizeUploadResponseStatus(resumableFile && resumableFile.lastUploadResponseStatus);
    if (trackedStatus > 0) {
        return trackedStatus;
    }

    const payload = parseUploadErrorPayload(message);
    return normalizeUploadResponseStatus(payload && payload.status);
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
    if (!resumable) {
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
    const started = startNextUploadChunk(resumable, { allowExtraSlot: true });
    resumable._finalizeSlotOwner = started ? file.uniqueIdentifier : null;
    return started;
};

export const getBaselineSimultaneousUploads = (configuredUploads) => {
    return Math.max(ADAPTIVE_UPLOAD_MIN_CONCURRENCY, Number(configuredUploads) || ADAPTIVE_UPLOAD_MIN_CONCURRENCY);
};

export const getInitialSimultaneousUploads = (configuredUploads) => {
    void configuredUploads;
    return ADAPTIVE_UPLOAD_MIN_CONCURRENCY;
};

export const initializeAdaptiveUploadConcurrency = (resumable, configuredUploads) => {
    if (!resumable || typeof resumable.uploadNextChunk !== 'function') {
        return () => { };
    }

    if (typeof resumable._sesamefsAdaptiveCleanup === 'function') {
        resumable._sesamefsAdaptiveCleanup();
    }

    resumable._sesamefsOriginalUploadNextChunk = resumable.uploadNextChunk;
    resumable._sesamefsAdaptiveUpload = createAdaptiveUploadState(configuredUploads);
    if (resumable.opts) {
        resumable.opts.simultaneousUploads = ADAPTIVE_UPLOAD_MIN_CONCURRENCY;
    }
    resumable.uploadNextChunk = function () {
        // Compute the concurrency target once per poke and thread it through the
        // start/fill calls so this hot path does a single files×chunks scan
        // instead of recomputing it for every slot it tries to fill.
        const target = desiredUploadConcurrency(resumable, { includeFinalizeReplacement: true });
        if (resumable.opts) {
            resumable.opts.simultaneousUploads = target;
        }
        const started = startNextUploadChunk(resumable, { target });
        if (started) {
            fillUploadConcurrencySlots(resumable, target);
        }
        return started;
    };

    const handleNetworkChange = () => {
        degradeAdaptiveUploadConcurrency(resumable, 'network-change');
    };

    if (typeof window !== 'undefined' && window.addEventListener) {
        window.addEventListener('offline', handleNetworkChange);
        window.addEventListener('online', handleNetworkChange);
    }

    const cleanup = () => {
        if (typeof window !== 'undefined' && window.removeEventListener) {
            window.removeEventListener('offline', handleNetworkChange);
            window.removeEventListener('online', handleNetworkChange);
        }
        if (resumable._sesamefsOriginalUploadNextChunk) {
            resumable.uploadNextChunk = resumable._sesamefsOriginalUploadNextChunk;
        }
        delete resumable._sesamefsOriginalUploadNextChunk;
        delete resumable._sesamefsAdaptiveCleanup;
        delete resumable._sesamefsAdaptiveUpload;
    };

    resumable._sesamefsAdaptiveCleanup = cleanup;
    return cleanup;
};

export const resetAdaptiveUploadConcurrency = (resumable, configuredUploads) => {
    if (!resumable) {
        return false;
    }

    let state = getAdaptiveUploadState(resumable);
    if (!state) {
        state = createAdaptiveUploadState(configuredUploads);
        resumable._sesamefsAdaptiveUpload = state;
    }

    state.max = getBaselineSimultaneousUploads(configuredUploads || state.max);
    resetAdaptiveMetrics(state);
    resumable._finalizeSlotOwner = null;
    const changed = setEffectiveUploadConcurrency(resumable, ADAPTIVE_UPLOAD_MIN_CONCURRENCY);
    syncResumableSimultaneousUploads(resumable);
    return changed;
};

export const updateAdaptiveUploadConcurrency = (resumable, resumableFile, uploadBitrate) => {
    const state = getAdaptiveUploadState(resumable);
    const bitrate = Number(uploadBitrate);
    if (!state || state.max <= ADAPTIVE_UPLOAD_MIN_CONCURRENCY || !Number.isFinite(bitrate) || bitrate <= 0) {
        return false;
    }
    if (!isAdaptiveEligibleFile(resumableFile, resumable) || !hasPendingChunks(resumable)) {
        return false;
    }

    const previousSmoothedBitrate = state.smoothedBitrate;
    const now = Date.now();
    if (previousSmoothedBitrate > 0 && bitrate < previousSmoothedBitrate * ADAPTIVE_UPLOAD_DROP_RATIO) {
        return degradeAdaptiveUploadConcurrency(resumable, 'bitrate-drop', { now });
    }

    state.smoothedBitrate = previousSmoothedBitrate > 0
        ? (previousSmoothedBitrate * ADAPTIVE_UPLOAD_SMOOTHING_FACTOR) + (bitrate * (1 - ADAPTIVE_UPLOAD_SMOOTHING_FACTOR))
        : bitrate;
    state.lastBitrate = bitrate;

    if (previousSmoothedBitrate > 0 && bitrate < previousSmoothedBitrate * ADAPTIVE_UPLOAD_STABLE_FLOOR_RATIO) {
        state.stableSamples = 0;
        return false;
    }

    state.stableSamples++;
    if (now < state.cooldownUntil) {
        return false;
    }

    const nextSlotCount = state.effective + 1;
    if (nextSlotCount > state.max) {
        return false;
    }
    if (state.smoothedBitrate < minimumBitrateForSlots(resumable, nextSlotCount)) {
        return false;
    }

    const requiredStableSamples = nextSlotCount === 2 ? ADAPTIVE_UPLOAD_FIRST_RAMP_SAMPLES : ADAPTIVE_UPLOAD_NEXT_RAMP_SAMPLES;
    if (state.stableSamples < requiredStableSamples) {
        return false;
    }
    if (nextSlotCount > 2 && state.lastRampBitrate > 0 && state.smoothedBitrate < state.lastRampBitrate * ADAPTIVE_UPLOAD_GAIN_RATIO) {
        return false;
    }

    state.stableSamples = 0;
    state.lastRampBitrate = state.smoothedBitrate;
    return setEffectiveUploadConcurrency(resumable, nextSlotCount, { refill: true });
};

export const noteAdaptiveUploadRetry = (resumable) => {
    return degradeAdaptiveUploadConcurrency(resumable, 'retry');
};

export const noteAdaptiveUploadFailure = (resumable, resumableFile, message) => {
    const status = extractAdaptivePenaltyStatus(resumableFile, message);
    // Back off on transport-level trouble (unknown/no status, payload-too-large,
    // rate limiting, and any 5xx). 429 in particular is the server explicitly
    // asking for less concurrency, so it must lower the adaptive target too.
    if (!message || status === 0 || status === 413 || status === 429 || status >= 500) {
        return degradeAdaptiveUploadConcurrency(resumable, 'failure');
    }
    return false;
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
    const state = getAdaptiveUploadState(resumable);
    if (state) {
        state.max = getBaselineSimultaneousUploads(baseline || state.max);
    }
    releaseInactiveFinalizeSlot(resumable);
    grantTemporaryFinalizeSlot(resumable);
    fillUploadConcurrencySlots(resumable);
};
