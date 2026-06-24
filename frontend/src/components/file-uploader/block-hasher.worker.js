/* eslint-disable no-restricted-globals */
// Web Worker: split a File into fixed-size blocks and compute BOTH the SHA-256 and
// the SHA-1 of each block off the main thread, so hashing a multi-GB file never
// freezes the UI. Posts incremental {type:'progress'} messages and a final
// {type:'done'} with an ordered manifest of { index, sha1, sha256, size } entries.
//
// Why two hashes: SHA-256 is the internal/storage identity (check/upload, S3 key,
// refs, GC, dedup); SHA-1 is the EXTERNAL Seafile block ID the backend writes into
// the file fs_object so the desktop/mobile sync client (which requires 40-hex
// SHA-1 block IDs) can parse and download the file. Both digests run over the same
// in-memory block buffer (one slice/read of the file, two separate digests).
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
    const { sha1, sha256, size: blockSizeBytes } = await hashBlockBytes(buf);
    blocks.push({ index, sha1, sha256, size: blockSizeBytes });
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
