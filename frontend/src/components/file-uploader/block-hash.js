// Pure, environment-agnostic hash helper shared by the block-hasher web worker
// and its unit test. It uses the Web Crypto API (crypto.subtle), which is
// available both in the browser worker and in Node (globalThis.crypto), so the
// exact code that runs in production is the code under test.
//
// Only SHA-256 is computed: it is the internal/storage identity (check/upload, S3
// key, refs, GC, dedup) and the only hash the client sends. The EXTERNAL Seafile
// block ID (SHA-1) is derived server-side from blocks.sha1 at commit, so the
// browser no longer pays a second per-block digest.

export function toHex(buffer) {
  const bytes = new Uint8Array(buffer);
  let out = '';
  for (let i = 0; i < bytes.length; i++) {
    out += bytes[i].toString(16).padStart(2, '0');
  }
  return out;
}

// hashBlockBytes computes the SHA-256 of one block's already-read bytes and
// returns { sha256, size }. `subtle` is injectable for tests; it defaults to the
// ambient crypto.subtle (worker/browser).
export async function hashBlockBytes(buf, subtle) {
  const crypt = subtle || (typeof crypto !== 'undefined' ? crypto.subtle : undefined);
  const sha256Digest = await crypt.digest('SHA-256', buf);
  return {
    sha256: toHex(sha256Digest),
    size: buf.byteLength,
  };
}
