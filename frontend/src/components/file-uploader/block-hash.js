// Pure, environment-agnostic dual-hash helper shared by the block-hasher web
// worker and its unit test. It uses the Web Crypto API (crypto.subtle), which is
// available both in the browser worker and in Node (globalThis.crypto), so the
// exact code that runs in production is the code under test.
//
// Why two hashes: SHA-256 is the internal/storage identity (check/upload, S3 key,
// refs, GC, dedup); SHA-1 is the EXTERNAL Seafile block ID the backend writes into
// the file fs_object so the desktop/mobile sync client (which requires 40-hex
// SHA-1 block IDs) can parse and download the file. Both come from the same bytes.

export function toHex(buffer) {
  const bytes = new Uint8Array(buffer);
  let out = '';
  for (let i = 0; i < bytes.length; i++) {
    out += bytes[i].toString(16).padStart(2, '0');
  }
  return out;
}

// hashBlockBytes computes the SHA-1 and SHA-256 of one block's already-read bytes
// (two separate digests over the same in-memory buffer, run concurrently) and
// returns { sha1, sha256, size }. `subtle` is injectable for tests; it defaults to
// the ambient crypto.subtle (worker/browser).
export async function hashBlockBytes(buf, subtle) {
  const crypt = subtle || (typeof crypto !== 'undefined' ? crypto.subtle : undefined);
  const [sha256Digest, sha1Digest] = await Promise.all([
    crypt.digest('SHA-256', buf),
    crypt.digest('SHA-1', buf),
  ]);
  return {
    sha1: toHex(sha1Digest),
    sha256: toHex(sha256Digest),
    size: buf.byteLength,
  };
}
