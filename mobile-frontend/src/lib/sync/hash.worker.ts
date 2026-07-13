// Web Worker: compute a stable content hash for a local file off the main
// thread, so hashing diff candidates during a sync pass never freezes the UI.
// Modelled on the web app's block-hasher.worker.js — same 8 MiB blocking, same
// SHA-256 (never SHA-1) — but here the goal is *change detection*, so we fold
// the per-block digests into a single content hash the diff can compare.
//
// Reading block-by-block (rather than one giant arrayBuffer) keeps memory flat
// on multi-GB files.

const BLOCK_SIZE = 8 * 1024 * 1024; // 8 MiB — matches api.uploadBlockSize

async function sha256Hex(data: ArrayBuffer): Promise<string> {
  const digest = await crypto.subtle.digest('SHA-256', data);
  return Array.from(new Uint8Array(digest))
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('');
}

async function hashFile(file: File, blockSize: number): Promise<string> {
  const size = file.size;
  if (size === 0) return sha256Hex(new ArrayBuffer(0));
  const count = Math.max(1, Math.ceil(size / blockSize));
  const blockHashes: string[] = [];
  for (let index = 0; index < count; index++) {
    const start = index * blockSize;
    const end = Math.min(start + blockSize, size);
    const buf = await file.slice(start, end).arrayBuffer();
    blockHashes.push(await sha256Hex(buf));
  }
  // Single block -> its digest IS the file hash. Multiple -> fold the ordered
  // block digests into one deterministic hash (Merkle-style root).
  if (blockHashes.length === 1) return blockHashes[0];
  return sha256Hex(new TextEncoder().encode(blockHashes.join(':')).buffer as ArrayBuffer);
}

self.onmessage = async (e: MessageEvent) => {
  const { id, file, blockSize } = e.data || {};
  try {
    const contentHash = await hashFile(file, blockSize || BLOCK_SIZE);
    self.postMessage({ id, type: 'done', contentHash, size: file.size });
  } catch (err) {
    self.postMessage({ id, type: 'error', message: (err as Error)?.message || String(err) });
  }
};
