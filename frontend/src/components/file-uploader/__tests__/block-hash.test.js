import { hashBlockBytes, toHex } from '../block-hash';

// Use Node's Web Crypto (same API surface as the worker's crypto.subtle) so the
// exact production hashing code runs under test, independent of jsdom's crypto.
const nodeCrypto = require('crypto');
const subtle = nodeCrypto.webcrypto.subtle;

function bufOf(str) {
  const b = Buffer.from(str);
  return b.buffer.slice(b.byteOffset, b.byteOffset + b.byteLength);
}

describe('block-hash dual hashing', () => {
  test('returns the correct SHA-1 (40 hex) and SHA-256 (64 hex) per block', async () => {
    const data = Buffer.from('abc');
    const { sha1, sha256, size } = await hashBlockBytes(bufOf('abc'), subtle);

    expect(size).toBe(3);
    expect(sha1).toHaveLength(40);
    expect(sha256).toHaveLength(64);
    // Cross-check against an independent implementation and known vectors.
    expect(sha1).toBe(nodeCrypto.createHash('sha1').update(data).digest('hex'));
    expect(sha256).toBe(nodeCrypto.createHash('sha256').update(data).digest('hex'));
    expect(sha1).toBe('a9993e364706816aba3e25717850c26c9cd0d89d');
    expect(sha256).toBe('ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad');
  });

  test('both hashes derive from the same bytes and are deterministic', async () => {
    const a = await hashBlockBytes(bufOf('the same block bytes'), subtle);
    const b = await hashBlockBytes(bufOf('the same block bytes'), subtle);
    expect(a).toEqual(b);
    expect(a.sha1).not.toBe(a.sha256);
  });

  test('different content yields different hashes', async () => {
    const a = await hashBlockBytes(bufOf('block A'), subtle);
    const b = await hashBlockBytes(bufOf('block B'), subtle);
    expect(a.sha1).not.toBe(b.sha1);
    expect(a.sha256).not.toBe(b.sha256);
  });

  test('toHex zero-pads single-digit bytes', () => {
    const buf = new Uint8Array([0x00, 0x0f, 0xff]).buffer;
    expect(toHex(buf)).toBe('000fff');
  });
});
