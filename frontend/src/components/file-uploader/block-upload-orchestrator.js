// Orchestrates the web content-addressed (block) upload flow:
//   session -> hash (worker) -> check -> upload missing -> commit (from manifest)
//
// Resume and dedup are implicit: the "missing" set returned by /blocks/check is
// the resume state, recomputed from shared storage, so this is multi-node and
// survives retries without any in-memory/offset tracking. The module is
// framework-agnostic and dependency-injected so it can be unit-tested without a
// real Worker or network (pass `api` and `hashFn`).

import { seafileAPI } from '../../utils/seafile-api';
import { enableBlockUpload, blockUploadThresholdMB, blockUploadBlockSizeMB } from '../../utils/constants';

// BLOCK_SIZE is the FALLBACK content-addressed block size, sourced from config
// (web_uploads.web_block_upload_block_size_mb) rather than hardcoded. The server
// is authoritative: uploadFileViaBlocks prefers the block_size echoed in the
// block-upload-session response so the client always splits exactly as the
// backend's file-from-blocks commit validates.
export const BLOCK_SIZE = (Number(blockUploadBlockSizeMB) || 8) * 1024 * 1024;

// Max hashes per /blocks/check request — must stay under the server cap (10000).
const CHECK_BATCH_SIZE = 5000;

// axios defaults to timeout:0 (wait forever). A control-plane call (session,
// check, commit) transfers little data and should never hang a multi-GB upload
// indefinitely on a half-open socket, so cap it.
export const CONTROL_PLANE_TIMEOUT_MS = 120000;
const CONTROL_PLANE_RETRY_ATTEMPTS = 3;
const CONTROL_PLANE_RETRY_BASE_MS = 500;

// Commits can legitimately run much longer than session/check because the server
// may need to verify a large manifest, promote refs and serialize metadata.
export const COMMIT_TIMEOUT_MS = 300000;

// A single block upload has no hard time bound (8 MB over a slow link can be
// slow but healthy), so instead of a fixed timeout it is guarded by an
// inactivity watchdog: if NO bytes move for this long the request is aborted and
// retried. This is what turns a silently dropped connection ("Saving..." forever)
// into a bounded retry instead of an infinite hang.
export const BLOCK_STALL_TIMEOUT_MS = 30000;

// Once the browser has finished sending a block, give the backend/S3 path extra
// time to acknowledge it before treating the request as a retryable timeout.
const BLOCK_RESPONSE_TIMEOUT_MS = 120000;

function createAbortError() {
  if (typeof DOMException === 'function') {
    return new DOMException('Upload aborted', 'AbortError');
  }
  const error = new Error('Upload aborted');
  error.name = 'AbortError';
  return error;
}

export function isAbortError(error) {
  return Boolean(error) && (
    error.name === 'AbortError'
    || error.code === 'ERR_CANCELED'
    || error.code === 'ABORT_ERR'
  );
}

// A stall is NOT a user abort: it must be retried, not treated as a cancellation.
// We give it its own error name so withRetry retries it and runBlockUpload does
// not mistake it for a deliberate cancel.
export function isStallError(error) {
  return Boolean(error) && error.name === 'StallTimeoutError';
}

function createStallError() {
  const error = new Error('Upload stalled: no progress');
  error.name = 'StallTimeoutError';
  return error;
}

function createBlockResponseTimeoutError() {
  const error = new Error('Block upload timed out waiting for server response');
  error.name = 'BlockResponseTimeoutError';
  return error;
}

function isRequestTimeoutError(error) {
  if (!error) {
    return false;
  }
  if (error.code === 'ECONNABORTED') {
    return true;
  }
  return String(error.message || '').toLowerCase().includes('timeout');
}

function isRetriableGatewayStatus(status) {
  return status === 502 || status === 503 || status === 504;
}

// isRetriableControlPlaneError reports transient failures where retrying the
// light control-plane request is safe: upstream/proxy 5xx, bounded request
// timeout, or a network failure with no HTTP response.
function isRetriableControlPlaneError(error) {
  if (!error || isAbortError(error)) {
    return false;
  }
  const status = error && error.response && error.response.status;
  if (isRetriableGatewayStatus(status) || isRequestTimeoutError(error)) {
    return true;
  }
  if (error.response) {
    return false;
  }
  const code = String(error.code || '').toUpperCase();
  if (code === 'ERR_NETWORK' || code === 'ECONNRESET' || code === 'ETIMEDOUT' || code === 'EAI_AGAIN') {
    return true;
  }
  return String(error.message || '').toLowerCase().includes('network');
}

function normalizeAttempts(value, fallback) {
  const fallbackCount = Math.max(1, Math.floor(Number(fallback) || 1));
  const count = Number(value);
  if (!Number.isFinite(count)) {
    return fallbackCount;
  }
  return Math.max(1, Math.floor(count));
}

async function withControlPlaneRetry(fn, attempts, { signal, baseMs = CONTROL_PLANE_RETRY_BASE_MS } = {}) {
  const maxAttempts = normalizeAttempts(attempts, CONTROL_PLANE_RETRY_ATTEMPTS);
  let hardAttempt = 0;
  let softWaits = 0;
  for (; ;) {
    throwIfAborted(signal);
    try {
      // eslint-disable-next-line no-await-in-loop
      return await fn();
    } catch (err) {
      if (isAbortError(err) || (signal && signal.aborted)) {
        throw err;
      }
      // A 429 on a control-plane call (block-upload-session over the per-user
      // uncommitted-session cap → Retry-After 5, or /blocks/check over its
      // rate-limit) is transient backpressure, NEVER terminal — the only terminal
      // staging rejection is the 413 fail-fast at session creation, which is not a
      // 429. So wait out the server's Retry-After WITHOUT consuming the hard retry
      // budget (bounded by MAX_BACKPRESSURE_WAITS so a permanently saturated cap
      // cannot loop forever), mirroring the block path's withRetry. Session
      // creation is only retried here, on FAILURE, so no partial session leaks.
      if (is429Error(err) && !isTerminal429Error(err) && softWaits < MAX_BACKPRESSURE_WAITS) {
        softWaits += 1;
        const jitter = Math.floor(Math.random() * 250);
        // eslint-disable-next-line no-await-in-loop
        await waitWithAbort(retryAfterMs(err, baseMs) + jitter, signal);
        continue;
      }
      hardAttempt += 1;
      if (!isRetriableControlPlaneError(err) || hardAttempt >= maxAttempts) {
        throw err;
      }
      const jitter = Math.floor(Math.random() * 250);
      // eslint-disable-next-line no-await-in-loop
      await waitWithAbort(Math.min(baseMs * 2 ** (hardAttempt - 1), 5000) + jitter, signal);
    }
  }
}

// uploadBlockWithStallGuard uploads one block and aborts+rejects it if no bytes
// move for `stallMs`. axios will not time out a half-open socket on its own
// (timeout:0), so without this a dropped connection mid-block hangs the whole
// file forever with no error and no retry. On a stall it rejects with a
// StallTimeoutError (NOT an AbortError) so the caller's retry loop kicks in; a
// real user cancel still propagates through the chained outer signal.
function uploadBlockWithStallGuard(api, session, hash, data, {
  signal,
  stallMs,
  responseTimeoutMs,
  onTransferProgress,
}) {
  const canAbort = typeof AbortController === 'function';
  if (!stallMs || stallMs <= 0 || !canAbort) {
    return signal ? api.uploadBlock(session, hash, data, { signal }) : api.uploadBlock(session, hash, data);
  }

  const controller = new AbortController();
  let onOuterAbort;
  if (signal) {
    if (signal.aborted) {
      controller.abort();
    } else {
      onOuterAbort = () => controller.abort();
      signal.addEventListener('abort', onOuterAbort, { once: true });
    }
  }

  let timeoutError = null;
  let timer;
  const blockBytes = (
    data && typeof data.byteLength === 'number'
      ? data.byteLength
      : (data && typeof data.size === 'number' ? data.size : 0)
  );
  let lastLoaded = 0;
  const armTimer = (timeoutMs, errorFactory) => {
    window.clearTimeout(timer);
    if (!timeoutMs || timeoutMs <= 0) {
      return;
    }
    timer = window.setTimeout(() => {
      timeoutError = errorFactory();
      controller.abort();
    }, timeoutMs);
  };
  armTimer(stallMs, createStallError);

  const config = {
    signal: controller.signal,
    // Every progress event = bytes are still moving → reset the watchdog.
    onUploadProgress: (event) => {
      const loaded = Math.max(lastLoaded, Number(event && event.loaded) || 0);
      const delta = Math.max(0, loaded - lastLoaded);
      if (delta > 0 && onTransferProgress) {
        onTransferProgress(delta);
      }
      lastLoaded = loaded;

      const totalBytes = Math.max(blockBytes, Number(event && event.total) || 0);
      if (totalBytes > 0 && loaded >= totalBytes) {
        armTimer(responseTimeoutMs, createBlockResponseTimeoutError);
        return;
      }

      armTimer(stallMs, createStallError);
    },
  };

  return api.uploadBlock(session, hash, data, config)
    .then((res) => res)
    .catch((err) => {
      // The abort we triggered for a timeout surfaces as a cancel; rethrow it as
      // a retryable transport timeout instead so it is not swallowed as a user
      // cancellation.
      if (timeoutError && !(signal && signal.aborted)) {
        throw timeoutError;
      }
      throw err;
    })
    .finally(() => {
      window.clearTimeout(timer);
      if (signal && onOuterAbort) {
        signal.removeEventListener('abort', onOuterAbort);
      }
    });
}

function throwIfAborted(signal) {
  if (signal && signal.aborted) {
    throw createAbortError();
  }
}

function waitWithAbort(ms, signal) {
  if (!signal) {
    return new Promise((resolve) => setTimeout(resolve, ms));
  }

  return new Promise((resolve, reject) => {
    let timer;
    const onAbort = () => {
      window.clearTimeout(timer);
      signal.removeEventListener('abort', onAbort);
      reject(createAbortError());
    };

    if (signal.aborted) {
      reject(createAbortError());
      return;
    }

    timer = window.setTimeout(() => {
      signal.removeEventListener('abort', onAbort);
      resolve();
    }, ms);

    signal.addEventListener('abort', onAbort, { once: true });
  });
}

// browserSupportsBlockUpload reports whether the runtime can run the flow
// (Web Worker + SubtleCrypto over a secure context). When false, callers fall
// back to the resumable.js uploader.
export function browserSupportsBlockUpload() {
  return (
    typeof Worker !== 'undefined' &&
    typeof crypto !== 'undefined' &&
    !!crypto.subtle &&
    typeof crypto.subtle.digest === 'function'
  );
}

// shouldUseBlockUpload decides whether a given file goes through the block flow:
// feature flag on, file at/above the size threshold, library POSITIVELY known to be
// non-encrypted, and the browser supports the required APIs.
//
// Fail closed on `encrypted`: the block flow rejects encrypted libraries server-side
// (SHA-256 block IDs are over plaintext), so we only divert when we can confirm the
// library is not encrypted — i.e. `encrypted === false`. Anything else (undefined
// because a parent forgot to pass repoEncrypted, null, or truthy) keeps the file on
// the resumable path. Callers pass the library's real encrypted boolean RAW (the repo
// detail endpoint returns a proper bool) — do NOT coerce with `!!`, which would turn
// an unknown (undefined) into `false` and wrongly enable the block flow.
export function shouldUseBlockUpload(file, { encrypted } = {}) {
  if (!enableBlockUpload || encrypted !== false) return false;
  if (!browserSupportsBlockUpload()) return false;
  const thresholdBytes = (blockUploadThresholdMB || 64) * 1024 * 1024;
  return file && file.size >= thresholdBytes;
}

// hashFileWithWorker splits + hashes a File off the main thread, computing the
// SHA-256 (storage identity) per block. Resolves to { blocks: [{index, sha256,
// size}], size }. The external Seafile block ID (SHA-1) is derived server-side.
export function hashFileWithWorker(file, { blockSize = BLOCK_SIZE, onProgress, signal } = {}) {
  return new Promise((resolve, reject) => {
    let worker;
    let settled = false;
    let onAbort;

    const finish = (fn, value) => {
      if (settled) {
        return;
      }
      settled = true;
      if (signal && onAbort) {
        signal.removeEventListener('abort', onAbort);
      }
      fn(value);
    };

    try {
      // Lazy require so babel-jest never parses import.meta (see factory module).
      // eslint-disable-next-line global-require
      const { createBlockHasherWorker } = require('./block-hasher-worker-factory');
      worker = createBlockHasherWorker();
    } catch (err) {
      finish(reject, err);
      return;
    }

    if (signal) {
      onAbort = () => {
        worker.terminate();
        finish(reject, createAbortError());
      };
      if (signal.aborted) {
        onAbort();
        return;
      }
      signal.addEventListener('abort', onAbort, { once: true });
    }

    worker.onmessage = (e) => {
      const msg = e.data || {};
      if (msg.type === 'progress') {
        if (onProgress) onProgress(msg.hashedBytes, msg.totalBytes);
      } else if (msg.type === 'done') {
        worker.terminate();
        finish(resolve, { blocks: msg.blocks, size: msg.size });
      } else if (msg.type === 'error') {
        worker.terminate();
        finish(reject, new Error(msg.message || 'hashing failed'));
      }
    };
    worker.onerror = (err) => {
      worker.terminate();
      finish(reject, err);
    };
    worker.postMessage({ file, blockSize });
  });
}

// is429Error reports whether an error is the server's per-user block-upload
// concurrency backpressure (429). This is NOT a failure — it is the server
// asking the client to slow down (item 18: max_concurrent_block_uploads_per_user).
export function is429Error(error) {
  return Boolean(error && error.response && error.response.status === 429);
}

function isTerminal429Error(error) {
  if (!is429Error(error)) {
    return false;
  }
  const data = (error && error.response && error.response.data) || {};
  return String(data.code || '').toLowerCase() === 'staging_cap_reached';
}

function isBlockDeleteInProgressError(error) {
  const response = error && error.response;
  const data = (response && response.data) || {};
  return Boolean(response && response.status === 409 && String(data.code || '').toLowerCase() === 'block_delete_in_progress');
}

// retryAfterMs reads the server's Retry-After hint (seconds) from a retryable response,
// clamped to a sane range, so the client waits as long as the server asked
// instead of the much shorter default exponential backoff (which could exhaust
// the retry budget before a slot frees). Falls back to fallbackMs when absent.
export function retryAfterMs(error, fallbackMs = 1000) {
  const headers = (error && error.response && error.response.headers) || {};
  const raw = headers['retry-after'] !== undefined ? headers['retry-after'] : headers['Retry-After'];
  const secs = Number(raw);
  if (Number.isFinite(secs) && secs > 0) {
    return Math.min(Math.max(secs * 1000, 100), 30000);
  }
  return fallbackMs;
}

// Cap how many times a single block waits out 429 backpressure so a permanently
// saturated cap cannot loop forever; well above what a healthy link needs (a
// slot frees as soon as any of the user's in-flight blocks completes).
const MAX_BACKPRESSURE_WAITS = 8;

// withRetry runs fn up to `attempts` times with exponential backoff. Retryable
// 429 backpressure and block-delete 409 fences are SOFT: they honor Retry-After
// and do not consume the hard retry budget, bounded by MAX_BACKPRESSURE_WAITS.
// The caller's
// per-attempt hook (uploadMissingBlocks) still tells the local limiter to back
// off on every failed attempt, including a 429, so client concurrency drops in
// response to the server signal.
async function withRetry(fn, attempts, { signal } = {}) {
  const maxAttempts = normalizeAttempts(attempts, 3);
  let lastErr;
  let hardAttempt = 0;
  let softWaits = 0;
  for (; ;) {
    throwIfAborted(signal);
    try {
      return await fn();
    } catch (err) {
      lastErr = err;
      if (isAbortError(err) || (signal && signal.aborted)) {
        throw err;
      }
      const softRetry = (is429Error(err) && !isTerminal429Error(err)) || isBlockDeleteInProgressError(err);
      if (softRetry && softWaits < MAX_BACKPRESSURE_WAITS) {
        softWaits += 1;
        const jitter = Math.floor(Math.random() * 250);
        await waitWithAbort(retryAfterMs(err) + jitter, signal);
        continue;
      }
      // A terminal 429 (session staging cap reached) will NEVER clear by resending
      // the same block — its bucket does not drain until the file commits — so
      // surface it immediately instead of burning hard retries (and re-uploading
      // the block body) on a doomed request.
      if (isTerminal429Error(err)) {
        throw err;
      }
      hardAttempt += 1;
      if (hardAttempt >= maxAttempts) {
        break;
      }
      await waitWithAbort(100 * 2 ** (hardAttempt - 1), signal);
    }
  }
  throw lastErr;
}

function buildFileFingerprint(file) {
  if (!file) {
    return null;
  }
  return {
    name: String(file.name || ''),
    lastModified: Number(file.lastModified) || 0,
    relativePath: String(file.relativePath || file.webkitRelativePath || ''),
  };
}

function buildHashPlanCache(blocks, size, blockSize, file) {
  const blockIndexByHash = {};
  blocks.forEach((b) => { blockIndexByHash[b.sha256] = b.index; });
  return {
    blockSize,
    size,
    blocks,
    fileFingerprint: buildFileFingerprint(file),
    blockIndexByHash,
    uniqueHashes: Array.from(new Set(blocks.map((b) => b.sha256))),
  };
}

function sameFileFingerprint(expected, actual) {
  return Boolean(expected && actual)
    && expected.name === actual.name
    && expected.lastModified === actual.lastModified
    && expected.relativePath === actual.relativePath;
}

function restoreHashPlanFromCache(hashCache, file, blockSize) {
  if (!hashCache || !Array.isArray(hashCache.blocks) || !Array.isArray(hashCache.uniqueHashes)) {
    return null;
  }
  if (hashCache.size !== file.size || hashCache.blockSize !== blockSize) {
    return null;
  }
  if (!hashCache.blockIndexByHash || typeof hashCache.blockIndexByHash !== 'object') {
    return null;
  }
  if (!sameFileFingerprint(hashCache.fileFingerprint, buildFileFingerprint(file))) {
    return null;
  }
  return {
    blocks: hashCache.blocks,
    size: hashCache.size,
    blockIndexByHash: hashCache.blockIndexByHash,
    uniqueHashes: hashCache.uniqueHashes,
  };
}

// uploadMissingBlocks uploads the given block hashes, gating EACH block on a slot
// from the shared global `limiter` so the total number of blocks on the wire across
// ALL files never exceeds the configured ceiling. `getBlockData(index)` returns a
// Promise of the raw bytes for that block.
//
// Anti-starvation: this file spawns at most `limiter.getMaxConcurrency()` workers
// (NOT one waiter per block), and each worker re-acquires a slot AFTER finishing a
// block — so it re-queues at the back of the limiter's FIFO and multiple files
// interleave fairly instead of the first file holding every slot until it is done.
// `getBlockData` (local byte read) runs OUTSIDE the slot; only the network upload
// holds it. When no limiter is injected (unit tests) it falls back to a plain pool
// of `concurrency` workers with no global gating.
async function uploadMissingBlocks(session, missing, blockIndexByHash, getBlockData, {
  api, concurrency, retries, onBlockUploaded, onTransferProgress, signal, stallMs, responseTimeoutMs, limiter,
}) {
  let cursor = 0;
  let uploadedCount = 0;

  const uploadOne = async (hash, index) => {
    try {
      return await withRetry(async () => {
        throwIfAborted(signal);
        const data = await getBlockData(index);
        throwIfAborted(signal);
        if (!limiter) {
          return uploadBlockWithStallGuard(api, session, hash, data, { signal, stallMs, responseTimeoutMs, onTransferProgress });
        }
        const release = await limiter.acquire({ signal });
        try {
          return await uploadBlockWithStallGuard(api, session, hash, data, { signal, stallMs, responseTimeoutMs, onTransferProgress });
        } catch (err) {
          // Match resumable parity: every real transport retry backs the adaptive
          // limiter off immediately, even if a later attempt succeeds.
          if (limiter.noteRetry && !isAbortError(err) && !(signal && signal.aborted)) {
            limiter.noteRetry();
          }
          throw err;
        } finally {
          release();
        }
      }, retries, { signal });
    } catch (err) {
      // A final failure still counts as a hard degrade after the per-retry backoffs.
      if (limiter && limiter.noteFailure && !isAbortError(err) && !(signal && signal.aborted)) {
        limiter.noteFailure();
      }
      throw err;
    }
  };

  const worker = async () => {
    for (; ;) {
      throwIfAborted(signal);
      const i = cursor;
      cursor += 1;
      if (i >= missing.length) return;
      const hash = missing[i];
      // eslint-disable-next-line no-await-in-loop
      await uploadOne(hash, blockIndexByHash[hash]);
      uploadedCount += 1;
      if (onBlockUploaded) onBlockUploaded(uploadedCount, missing.length);
    }
  };

  // Cap workers at the global ceiling (a single file may use every slot) without
  // exceeding the number of blocks. No limiter → fall back to `concurrency`.
  const cap = limiter ? limiter.getMaxConcurrency() : concurrency;
  const poolSize = Math.min(cap || 1, missing.length) || 0;
  const workers = [];
  for (let w = 0; w < poolSize; w += 1) {
    workers.push(worker());
  }
  await Promise.all(workers);
}

// buildManifest emits the commit manifest. Only sha256 (the storage identity used
// for check/upload/refs) and size are sent; the external Seafile block ID (SHA-1)
// is derived server-side from blocks.sha1 at commit, so the client no longer
// computes or sends it.
function buildManifest(blocks) {
  return blocks.map((b) => ({ sha256: b.sha256, size: b.size }));
}

// isCommitInProgress detects the backend's transient "winner still finalizing"
// response: a 409 whose body is NOT a needs_upload conflict and whose message says
// the commit is still in progress. Newer backends tag it with
// code=commit_in_progress; older backends only return the exact retryable message.
// Because the commit is idempotent (R7), the correct client behavior is to back
// off and re-ask until the result is returned, rather than treating it as a
// failure. Permanent 409s (different file, parent_dir mismatch, encrypted) are
// left to propagate.
function isCommitInProgress(err) {
  const resp = err && err.response;
  if (!resp || resp.status !== 409) return false;
  const data = resp.data || {};
  if (Array.isArray(data.needs_upload)) return false;
  if (String(data.code || '').toLowerCase() === 'commit_in_progress') return true;
  return String(data.error || '').trim().toLowerCase() === 'commit still in progress; retry';
}

// commitFromManifest posts the commit, retrying transient commit-finalizing
// conflicts plus control-plane transport failures (timeout / 502 / 503 / 504 /
// network) with bounded exponential backoff. Every other outcome (success,
// needs_upload conflict, permanent 409, abort, other errors) is returned/thrown
// to the caller unchanged so the existing needs_upload path and fallbacks still
// work.
async function commitFromManifest(api, repoID, manifest, { signal, attempts, baseMs, timeout }) {
  const maxAttempts = normalizeAttempts(attempts, 6);
  let lastErr;
  for (let i = 1; i <= maxAttempts; i += 1) {
    throwIfAborted(signal);
    try {
      // eslint-disable-next-line no-await-in-loop
      const resp = await api.createFileFromBlocks(repoID, manifest, { signal, timeout });
      return resp.data;
    } catch (err) {
      lastErr = err;
      if (isAbortError(err) || (signal && signal.aborted)) throw err;
      if ((isCommitInProgress(err) || isRetriableControlPlaneError(err)) && i < maxAttempts) {
        // eslint-disable-next-line no-await-in-loop
        await waitWithAbort(Math.min(baseMs * 2 ** (i - 1), 5000), signal);
        // eslint-disable-next-line no-continue
        continue;
      }
      throw err;
    }
  }
  throw lastErr;
}

// uploadFileViaBlocks runs the full flow and resolves to the commit response
// (array with { name, id, size }). Throws on unrecoverable errors so the caller
// can fall back to the legacy uploader.
export async function uploadFileViaBlocks(file, {
  repoID,
  parentDir = '/',
  filename,
  replace = false,
  api = seafileAPI,
  hashFn = hashFileWithWorker,
  hashCache = null,
  blockSize = BLOCK_SIZE,
  // Global per-component concurrency limiter shared across all block uploads; it
  // bounds the TOTAL blocks on the wire to the configured ceiling. `concurrency` is
  // only the fallback pool size when no limiter is injected (unit tests). No more
  // hardcoded per-file "3" — production always injects `limiter`.
  limiter = null,
  concurrency = 1,
  retries = 3,
  commitRetries = 6,
  commitRetryBaseMs = 500,
  blockStallTimeoutMs = BLOCK_STALL_TIMEOUT_MS,
  blockResponseTimeoutMs = BLOCK_RESPONSE_TIMEOUT_MS,
  controlPlaneTimeoutMs = CONTROL_PLANE_TIMEOUT_MS,
  controlPlaneRetries = CONTROL_PLANE_RETRY_ATTEMPTS,
  controlPlaneRetryBaseMs = CONTROL_PLANE_RETRY_BASE_MS,
  commitTimeoutMs = COMMIT_TIMEOUT_MS,
  onHashProgress,
  onHashCache,
  onUploadProgress,
  onTransferProgress,
  onPhase,
  onPlan,
  waitForUploadSlot,
  onReadyForUpload,
  signal,
} = {}) {
  if (!repoID) throw new Error('repoID is required');
  const name = filename || file.name;
  // Control-plane (session/check/commit) config: bound the wait so a half-open
  // socket cannot hang the flow forever. signal may be undefined (axios ignores it).
  const ctrlConfig = { signal, timeout: controlPlaneTimeoutMs };

  // emitPhase reports the flow phase ('hashing' | 'checking' | 'uploading' |
  // 'saving') so the UI can render an accurate state instead of inferring it from
  // the legacy resumable.js chunk/remainingTime heuristics (which do not apply to a
  // block-upload entry and otherwise read as "Saving..." the whole time).
  const emitPhase = (phase) => { if (onPhase) onPhase(phase); };

  throwIfAborted(signal);

  // 1. Server-issued session.
  const sessionResp = await withControlPlaneRetry(
    () => api.createBlockUploadSession(repoID, parentDir, file && file.size, ctrlConfig),
    controlPlaneRetries,
    { signal, baseMs: controlPlaneRetryBaseMs },
  );
  const session = sessionResp.data.session_id;

  // The server is authoritative on the CAS block size: hash/slice to exactly the
  // size the file-from-blocks commit will validate. Fall back to the configured
  // default only if the session omits it (older server).
  const serverBlockSize = Number(sessionResp.data && sessionResp.data.block_size);
  if (Number.isFinite(serverBlockSize) && serverBlockSize > 0) {
    blockSize = serverBlockSize;
  }

  // 2. Hash blocks off the main thread unless we already have a cache for THIS
  // exact file size + server-authoritative block size. A retry gets a fresh
  // session and re-checks against the server, but need not burn CPU re-hashing
  // the same bytes.
  let blocks;
  let size;
  let blockIndexByHash;
  let uniqueHashes;
  const cachedHashPlan = restoreHashPlanFromCache(hashCache, file, blockSize);
  if (cachedHashPlan) {
    ({ blocks, size, blockIndexByHash, uniqueHashes } = cachedHashPlan);
    if (onHashProgress && size > 0) {
      onHashProgress(size, size);
    }
  } else {
    emitPhase('hashing');
    const hashed = await hashFn(file, { blockSize, onProgress: onHashProgress, signal });
    const nextHashCache = buildHashPlanCache(hashed.blocks, hashed.size, blockSize, file);
    ({ blocks, size, blockIndexByHash, uniqueHashes } = nextHashCache);
    if (onHashCache) {
      onHashCache(nextHashCache);
    }
  }

  const getBlockData = (index) => {
    const start = index * blockSize;
    const end = Math.min(start + blockSize, size);
    return file.slice(start, end);
  };

  // 3. Which blocks are missing (de-duplicated hash set). Batched to stay within
  //    the server's per-request hash cap (CHECK_BATCH_SIZE < 10000).
  emitPhase('checking');
  const missing = [];
  for (let i = 0; i < uniqueHashes.length; i += CHECK_BATCH_SIZE) {
    throwIfAborted(signal);
    const batch = uniqueHashes.slice(i, i + CHECK_BATCH_SIZE);
    // eslint-disable-next-line no-await-in-loop
    const checkResp = await withControlPlaneRetry(
      () => api.checkBlocks(batch, session, ctrlConfig),
      controlPlaneRetries,
      { signal, baseMs: controlPlaneRetryBaseMs },
    );
    const batchMissing = (checkResp.data && checkResp.data.missing) || [];
    for (let j = 0; j < batchMissing.length; j += 1) {
      missing.push(batchMissing[j]);
    }
  }

  // Report the dedup plan from the AUTHORITATIVE missing set (not wire bytes,
  // which include retries): every UNIQUE block present on the server is a save.
  // uploadBytes counts each missing block once at its real size; dedupedBytes is
  // the rest of the logical file (shared/repeated blocks already on the server).
  if (onPlan) {
    const lastBlockIndex = blocks.length - 1;
    const blockBytes = (index) => (
      index === lastBlockIndex ? size - (lastBlockIndex * blockSize) : blockSize
    );
    let uploadBytes = 0;
    for (let i = 0; i < missing.length; i += 1) {
      const index = blockIndexByHash[missing[i]];
      if (index !== undefined) {
        uploadBytes += blockBytes(index);
      }
    }
    uploadBytes = Math.min(uploadBytes, size);
    onPlan({ totalBytes: size, uploadBytes, dedupedBytes: Math.max(0, size - uploadBytes) });
  }

  if (onReadyForUpload) {
    onReadyForUpload({ missingCount: missing.length, totalCount: blocks.length });
  }
  if (waitForUploadSlot) {
    await waitForUploadSlot({ signal, missingCount: missing.length, totalCount: blocks.length });
    throwIfAborted(signal);
  }

  // 4. Upload missing blocks.
  if (missing.length > 0) {
    emitPhase('uploading');
    await uploadMissingBlocks(session, missing, blockIndexByHash, getBlockData, {
      api,
      concurrency,
      limiter,
      retries,
      onBlockUploaded: onUploadProgress,
      onTransferProgress,
      signal,
      stallMs: blockStallTimeoutMs,
      responseTimeoutMs: blockResponseTimeoutMs,
    });
  }

  // 5. Commit from the ordered manifest. If the server reports some blocks are
  //    not commit-ready (race / orphan), re-upload exactly those once and retry.
  const manifest = {
    session,
    parent_dir: parentDir,
    filename: name,
    replace,
    size,
    blocks: buildManifest(blocks),
  };

  const commitOpts = { signal, attempts: commitRetries, baseMs: commitRetryBaseMs, timeout: commitTimeoutMs };
  try {
    throwIfAborted(signal);
    emitPhase('saving');
    return await commitFromManifest(api, repoID, manifest, commitOpts);
  } catch (err) {
    // The winner of a concurrent commit finishes the file; "commit still in
    // progress" is already retried inside commitFromManifest. Here we only handle
    // the other recoverable case: the server says some blocks are not commit-ready
    // (race / orphan) — re-upload exactly those once and retry the commit.
    const needs = err && err.response && err.response.data && err.response.data.needs_upload;
    if (Array.isArray(needs) && needs.length > 0) {
      emitPhase('uploading');
      await uploadMissingBlocks(session, needs, blockIndexByHash, getBlockData, {
        api,
        concurrency,
        limiter,
        retries,
        onBlockUploaded: onUploadProgress,
        onTransferProgress,
        signal,
        stallMs: blockStallTimeoutMs,
        responseTimeoutMs: blockResponseTimeoutMs,
      });
      throwIfAborted(signal);
      emitPhase('saving');
      return await commitFromManifest(api, repoID, manifest, commitOpts);
    }
    throw err;
  }
}
