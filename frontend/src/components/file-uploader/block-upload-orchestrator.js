// Orchestrates the web content-addressed (block) upload flow:
//   session -> hash (worker) -> check -> upload missing -> commit (from manifest)
//
// Resume and dedup are implicit: the "missing" set returned by /blocks/check is
// the resume state, recomputed from shared storage, so this is multi-node and
// survives retries without any in-memory/offset tracking. The module is
// framework-agnostic and dependency-injected so it can be unit-tested without a
// real Worker or network (pass `api` and `hashFn`).

import { seafileAPI } from '../../utils/seafile-api';
import { enableBlockUpload, blockUploadThresholdMB } from '../../utils/constants';

export const BLOCK_SIZE = 8 * 1024 * 1024; // must match backend WebUploadBlockSize

// Max hashes per /blocks/check request — must stay under the server cap (10000).
const CHECK_BATCH_SIZE = 5000;

// axios defaults to timeout:0 (wait forever). A control-plane call (session,
// check, commit) transfers little data and should never hang a multi-GB upload
// indefinitely on a half-open socket, so cap it.
export const CONTROL_PLANE_TIMEOUT_MS = 120000;

// A single block upload has no hard time bound (8 MB over a slow link can be
// slow but healthy), so instead of a fixed timeout it is guarded by an
// inactivity watchdog: if NO bytes move for this long the request is aborted and
// retried. This is what turns a silently dropped connection ("Saving..." forever)
// into a bounded retry instead of an infinite hang.
export const BLOCK_STALL_TIMEOUT_MS = 30000;

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

// uploadBlockWithStallGuard uploads one block and aborts+rejects it if no bytes
// move for `stallMs`. axios will not time out a half-open socket on its own
// (timeout:0), so without this a dropped connection mid-block hangs the whole
// file forever with no error and no retry. On a stall it rejects with a
// StallTimeoutError (NOT an AbortError) so the caller's retry loop kicks in; a
// real user cancel still propagates through the chained outer signal.
function uploadBlockWithStallGuard(api, session, hash, data, { signal, stallMs }) {
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

  let stalled = false;
  let timer;
  const armStallTimer = () => {
    window.clearTimeout(timer);
    timer = window.setTimeout(() => {
      stalled = true;
      controller.abort();
    }, stallMs);
  };
  armStallTimer();

  const config = {
    signal: controller.signal,
    // Every progress event = bytes are still moving → reset the watchdog.
    onUploadProgress: () => armStallTimer(),
  };

  return api.uploadBlock(session, hash, data, config)
    .then((res) => res)
    .catch((err) => {
      // The abort we triggered for a stall surfaces as a cancel; rethrow it as a
      // retryable stall instead so it is not swallowed as a user cancellation.
      if (stalled && !(signal && signal.aborted)) {
        throw createStallError();
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
// feature flag on, file at/above the size threshold, library not encrypted, and
// the browser supports the required APIs.
export function shouldUseBlockUpload(file, { encrypted = false } = {}) {
  if (!enableBlockUpload || encrypted) return false;
  if (!browserSupportsBlockUpload()) return false;
  const thresholdBytes = (blockUploadThresholdMB || 64) * 1024 * 1024;
  return file && file.size >= thresholdBytes;
}

// hashFileWithWorker splits + hashes a File off the main thread, computing both
// SHA-256 (storage identity) and SHA-1 (external Seafile block ID) per block.
// Resolves to { blocks: [{index, sha1, sha256, size}], size }.
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

async function withRetry(fn, attempts, { signal } = {}) {
  let lastErr;
  for (let i = 1; i <= attempts; i++) {
    throwIfAborted(signal);
    try {
      return await fn();
    } catch (err) {
      lastErr = err;
      if (isAbortError(err) || (signal && signal.aborted)) {
        throw err;
      }
      if (i < attempts) {
        await waitWithAbort(100 * 2 ** (i - 1), signal);
      }
    }
  }
  throw lastErr;
}

// uploadMissingBlocks uploads the given block hashes with bounded concurrency
// using a fixed pool of workers that pull from a shared cursor.
// `getBlockData(index)` returns a Promise of the raw bytes for that block.
async function uploadMissingBlocks(session, missing, blockIndexByHash, getBlockData, {
  api, concurrency, retries, onBlockUploaded, signal, stallMs,
}) {
  let cursor = 0;
  let uploadedCount = 0;

  const worker = async () => {
    for (;;) {
      throwIfAborted(signal);
      const i = cursor;
      cursor += 1;
      if (i >= missing.length) return;
      const hash = missing[i];
      const index = blockIndexByHash[hash];
      // eslint-disable-next-line no-await-in-loop
      await withRetry(async () => {
        throwIfAborted(signal);
        const data = await getBlockData(index);
        throwIfAborted(signal);
        return uploadBlockWithStallGuard(api, session, hash, data, { signal, stallMs });
      }, retries, { signal });
      uploadedCount += 1;
      if (onBlockUploaded) onBlockUploaded(uploadedCount, missing.length);
    }
  };

  const poolSize = Math.min(concurrency, missing.length) || 0;
  const workers = [];
  for (let w = 0; w < poolSize; w += 1) {
    workers.push(worker());
  }
  await Promise.all(workers);
}

// buildManifest emits the dual-hash commit manifest. sha256 is the storage
// identity (check/upload/refs); sha1 is the external Seafile block ID the backend
// writes into the file fs_object for desktop/mobile download compatibility.
function buildManifest(blocks) {
  return blocks.map((b) => ({ sha1: b.sha1, sha256: b.sha256, size: b.size }));
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

// commitFromManifest posts the commit, retrying ONLY the transient "commit still
// in progress" 409 with bounded exponential backoff. Every other outcome (success,
// needs_upload conflict, permanent 409, abort, other errors) is returned/thrown to
// the caller unchanged so the existing needs_upload path and fallbacks still work.
async function commitFromManifest(api, repoID, manifest, { signal, attempts, baseMs, timeout }) {
  let lastErr;
  for (let i = 1; i <= attempts; i += 1) {
    throwIfAborted(signal);
    try {
      // eslint-disable-next-line no-await-in-loop
      const resp = await api.createFileFromBlocks(repoID, manifest, { signal, timeout });
      return resp.data;
    } catch (err) {
      lastErr = err;
      if (isAbortError(err) || (signal && signal.aborted)) throw err;
      if (isCommitInProgress(err) && i < attempts) {
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
  blockSize = BLOCK_SIZE,
  concurrency = 3,
  retries = 3,
  commitRetries = 6,
  commitRetryBaseMs = 500,
  blockStallTimeoutMs = BLOCK_STALL_TIMEOUT_MS,
  controlPlaneTimeoutMs = CONTROL_PLANE_TIMEOUT_MS,
  onHashProgress,
  onUploadProgress,
  onPhase,
  signal,
} = {}) {
  if (!repoID) throw new Error('repoID is required');
  const name = filename || file.name;
  // Control-plane (session/check/commit) config: bound the wait so a half-open
  // socket cannot hang the flow forever. signal may be undefined (axios ignores it).
  const ctrlConfig = { signal, timeout: controlPlaneTimeoutMs };

  // emitPhase reports the coarse flow phase ('hashing' | 'uploading' | 'saving')
  // so the UI can render an accurate state instead of inferring it from the
  // legacy resumable.js chunk/remainingTime heuristics (which do not apply to a
  // block-upload entry and otherwise read as "Saving..." the whole time).
  const emitPhase = (phase) => { if (onPhase) onPhase(phase); };

  throwIfAborted(signal);

  // 1. Server-issued session.
  const sessionResp = await api.createBlockUploadSession(repoID, parentDir, ctrlConfig);
  const session = sessionResp.data.session_id;

  // 2. Hash blocks off the main thread.
  emitPhase('hashing');
  const { blocks, size } = await hashFn(file, { blockSize, onProgress: onHashProgress, signal });
  const blockIndexByHash = {};
  blocks.forEach((b) => { blockIndexByHash[b.sha256] = b.index; });

  const getBlockData = (index) => {
    const start = index * blockSize;
    const end = Math.min(start + blockSize, size);
    return file.slice(start, end).arrayBuffer();
  };

  // 3. Which blocks are missing (de-duplicated hash set). Batched to stay within
  //    the server's per-request hash cap (CHECK_BATCH_SIZE < 10000).
  const uniqueHashes = Array.from(new Set(blocks.map((b) => b.sha256)));
  const missing = [];
  for (let i = 0; i < uniqueHashes.length; i += CHECK_BATCH_SIZE) {
    throwIfAborted(signal);
    const batch = uniqueHashes.slice(i, i + CHECK_BATCH_SIZE);
    // eslint-disable-next-line no-await-in-loop
    const checkResp = await api.checkBlocks(batch, session, ctrlConfig);
    const batchMissing = (checkResp.data && checkResp.data.missing) || [];
    for (let j = 0; j < batchMissing.length; j += 1) {
      missing.push(batchMissing[j]);
    }
  }

  // 4. Upload missing blocks.
  emitPhase('uploading');
  await uploadMissingBlocks(session, missing, blockIndexByHash, getBlockData, {
    api, concurrency, retries, onBlockUploaded: onUploadProgress, signal, stallMs: blockStallTimeoutMs,
  });

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

  const commitOpts = { signal, attempts: commitRetries, baseMs: commitRetryBaseMs, timeout: controlPlaneTimeoutMs };
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
        api, concurrency, retries, onBlockUploaded: onUploadProgress, signal, stallMs: blockStallTimeoutMs,
      });
      throwIfAborted(signal);
      emitPhase('saving');
      return await commitFromManifest(api, repoID, manifest, commitOpts);
    }
    throw err;
  }
}
