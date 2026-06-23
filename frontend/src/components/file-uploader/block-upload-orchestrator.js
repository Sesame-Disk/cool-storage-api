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

// hashFileWithWorker splits + SHA-256-hashes a File off the main thread.
// Resolves to { blocks: [{index, sha256, size}], size }.
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
  api, concurrency, retries, onBlockUploaded, signal,
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
        return signal ? api.uploadBlock(session, hash, data, { signal }) : api.uploadBlock(session, hash, data);
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

function buildManifest(blocks) {
  return blocks.map((b) => ({ sha256: b.sha256, size: b.size }));
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
  onHashProgress,
  onUploadProgress,
  signal,
} = {}) {
  if (!repoID) throw new Error('repoID is required');
  const name = filename || file.name;

  throwIfAborted(signal);

  // 1. Server-issued session.
  const sessionResp = signal
    ? await api.createBlockUploadSession(repoID, parentDir, { signal })
    : await api.createBlockUploadSession(repoID, parentDir);
  const session = sessionResp.data.session_id;

  // 2. Hash blocks off the main thread.
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
    const checkResp = signal
      ? await api.checkBlocks(batch, session, { signal })
      : await api.checkBlocks(batch, session);
    const batchMissing = (checkResp.data && checkResp.data.missing) || [];
    for (let j = 0; j < batchMissing.length; j += 1) {
      missing.push(batchMissing[j]);
    }
  }

  // 4. Upload missing blocks.
  await uploadMissingBlocks(session, missing, blockIndexByHash, getBlockData, {
    api, concurrency, retries, onBlockUploaded: onUploadProgress, signal,
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

  try {
    throwIfAborted(signal);
    const resp = signal
      ? await api.createFileFromBlocks(repoID, manifest, { signal })
      : await api.createFileFromBlocks(repoID, manifest);
    return resp.data;
  } catch (err) {
    const needs = err && err.response && err.response.data && err.response.data.needs_upload;
    if (Array.isArray(needs) && needs.length > 0) {
      await uploadMissingBlocks(session, needs, blockIndexByHash, getBlockData, {
        api, concurrency, retries, onBlockUploaded: onUploadProgress, signal,
      });
      throwIfAborted(signal);
      const resp = signal
        ? await api.createFileFromBlocks(repoID, manifest, { signal })
        : await api.createFileFromBlocks(repoID, manifest);
      return resp.data;
    }
    throw err;
  }
}
