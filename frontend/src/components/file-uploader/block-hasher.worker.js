/* eslint-disable no-restricted-globals */
// Web Worker: split a File into fixed-size blocks and compute the SHA-256 of each
// block off the main thread, so hashing a multi-GB file never freezes the UI. Posts
// incremental {type:'progress'} messages and a final {type:'done'} with an ordered
// manifest of { index, sha256, size } entries.
//
// Only SHA-256 is computed (the internal/storage identity used by check/upload, S3
// key, refs, GC, dedup). The EXTERNAL Seafile block ID (SHA-1) is derived
// server-side from blocks.sha1 at commit, so the worker does a single digest per
// block (one slice/read of the file) instead of two.
//
// BLOCK_SIZE MUST match the backend (api.uploadBlockSize / v2.WebUploadBlockSize
// = 8 MB) so the blocks line up with what the rest of the system expects.

import { hashBlockBytes } from './block-hash';

const BLOCK_SIZE = 8 * 1024 * 1024;

async function hashFile(file, blockSize) {
  const size = file.size;
  const count = Math.max(1, Math.ceil(size / blockSize));
  const blocks = [];
  let hashedBytes = 0;
  for (let index = 0; index < count; index++) {
    const start = index * blockSize;
    const end = Math.min(start + blockSize, size);
    const buf = await file.slice(start, end).arrayBuffer();
    const { sha256, size: blockSizeBytes } = await hashBlockBytes(buf);
    blocks.push({ index, sha256, size: blockSizeBytes });
    hashedBytes += blockSizeBytes;
    self.postMessage({ type: 'progress', hashedBytes, totalBytes: size });
  }
  return blocks;
}

self.onmessage = async (e) => {
  const { file, blockSize } = e.data || {};
  try {
    const blocks = await hashFile(file, blockSize || BLOCK_SIZE);
    self.postMessage({ type: 'done', blocks, size: file.size });
  } catch (err) {
    self.postMessage({ type: 'error', message: (err && err.message) || String(err) });
  }
};
