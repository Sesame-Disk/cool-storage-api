import { hashBlockBytes, toHex } from '../block-hash';

// Use Node's Web Crypto (same API surface as the worker's crypto.subtle) so the
// exact production hashing code runs under test, independent of jsdom's crypto. If
// crypto.webcrypto is absent (very old Node), skip rather than throw at import time,
// which would otherwise fail the whole frontend suite.
const nodeCrypto = require('crypto');
const subtle = (nodeCrypto.webcrypto && nodeCrypto.webcrypto.subtle) || null;
const describeIfCrypto = subtle ? describe : describe.skip;

function bufOf(str) {
  const b = Buffer.from(str);
  return b.buffer.slice(b.byteOffset, b.byteOffset + b.byteLength);
}

describeIfCrypto('block-hash SHA-256 hashing', () => {
  test('returns the correct SHA-256 (64 hex) per block and no sha1', async () => {
    const data = Buffer.from('abc');
    const { sha1, sha256, size } = await hashBlockBytes(bufOf('abc'), subtle);

    expect(size).toBe(3);
    expect(sha1).toBeUndefined();
    expect(sha256).toHaveLength(64);
    // Cross-check against an independent implementation and a known vector.
    expect(sha256).toBe(nodeCrypto.createHash('sha256').update(data).digest('hex'));
    expect(sha256).toBe('ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad');
  });

  test('is deterministic for the same bytes', async () => {
    const a = await hashBlockBytes(bufOf('the same block bytes'), subtle);
    const b = await hashBlockBytes(bufOf('the same block bytes'), subtle);
    expect(a).toEqual(b);
  });

  test('different content yields different hashes', async () => {
    const a = await hashBlockBytes(bufOf('block A'), subtle);
    const b = await hashBlockBytes(bufOf('block B'), subtle);
    expect(a.sha256).not.toBe(b.sha256);
  });

  test('toHex zero-pads single-digit bytes', () => {
    const buf = new Uint8Array([0x00, 0x0f, 0xff]).buffer;
    expect(toHex(buf)).toBe('000fff');
  });
});
