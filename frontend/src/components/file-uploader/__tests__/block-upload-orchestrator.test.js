import { uploadFileViaBlocks } from '../block-upload-orchestrator';

jest.mock('../../../utils/seafile-api', () => ({ seafileAPI: {} }));
jest.mock('../../../utils/constants', () => ({ enableBlockUpload: true, blockUploadThresholdMB: 64 }));

// Minimal File stand-in: only what the orchestrator uses (size + slice().arrayBuffer()).
function makeFile(size, name = 'big.bin') {
  return {
    name,
    size,
    slice: (start, end) => ({
      arrayBuffer: () => Promise.resolve(new ArrayBuffer(end - start)),
    }),
  };
}

describe('uploadFileViaBlocks', () => {
  test('uploads only the missing blocks then commits the ordered manifest', async () => {
    const uploaded = [];
    const api = {
      createBlockUploadSession: jest.fn().mockResolvedValue({ data: { session_id: 'sess1' } }),
      checkBlocks: jest.fn().mockResolvedValue({ data: { existing: ['h0'], missing: ['h1'] } }),
      uploadBlock: jest.fn().mockImplementation((s, hash) => {
        uploaded.push(hash);
        return Promise.resolve({ data: {} });
      }),
      createFileFromBlocks: jest.fn().mockResolvedValue({
        data: [{ name: 'big.bin', id: 'fid', size: '100' }],
      }),
    };
    const hashFn = jest.fn().mockResolvedValue({
      blocks: [
        { index: 0, sha256: 'h0', size: 50 },
        { index: 1, sha256: 'h1', size: 50 },
      ],
      size: 100,
    });

    const res = await uploadFileViaBlocks(makeFile(100), {
      repoID: 'r1', parentDir: '/', api, hashFn, concurrency: 2, blockSize: 50,
    });

    expect(api.createBlockUploadSession).toHaveBeenCalledWith('r1', '/');
    expect(api.checkBlocks).toHaveBeenCalledWith(['h0', 'h1'], 'sess1');
    // Only the block reported missing is uploaded (dedup/resume).
    expect(uploaded).toEqual(['h1']);
    expect(api.uploadBlock).toHaveBeenCalledTimes(1);
    // Commit carries the full ordered manifest.
    const manifest = api.createFileFromBlocks.mock.calls[0][1];
    expect(manifest.blocks).toEqual([
      { sha256: 'h0', size: 50 },
      { sha256: 'h1', size: 50 },
    ]);
    expect(manifest.session).toBe('sess1');
    expect(res).toEqual([{ name: 'big.bin', id: 'fid', size: '100' }]);
  });

  test('re-uploads blocks the commit reports as needs_upload, then retries once', async () => {
    let commitCalls = 0;
    const uploaded = [];
    const api = {
      createBlockUploadSession: jest.fn().mockResolvedValue({ data: { session_id: 's' } }),
      checkBlocks: jest.fn().mockResolvedValue({ data: { missing: [] } }),
      uploadBlock: jest.fn().mockImplementation((s, hash) => {
        uploaded.push(hash);
        return Promise.resolve({ data: {} });
      }),
      createFileFromBlocks: jest.fn().mockImplementation(() => {
        commitCalls += 1;
        if (commitCalls === 1) {
          const err = new Error('conflict');
          err.response = { status: 409, data: { needs_upload: ['h0'] } };
          return Promise.reject(err);
        }
        return Promise.resolve({ data: [{ name: 'f', id: 'i', size: '50' }] });
      }),
    };
    const hashFn = jest.fn().mockResolvedValue({
      blocks: [{ index: 0, sha256: 'h0', size: 50 }],
      size: 50,
    });

    const res = await uploadFileViaBlocks(makeFile(50), { repoID: 'r', api, hashFn, blockSize: 50 });

    expect(uploaded).toEqual(['h0']); // re-uploaded the block the commit demanded
    expect(commitCalls).toBe(2);
    expect(res[0].name).toBe('f');
  });

  test('propagates a non-recoverable commit error', async () => {
    const api = {
      createBlockUploadSession: jest.fn().mockResolvedValue({ data: { session_id: 's' } }),
      checkBlocks: jest.fn().mockResolvedValue({ data: { missing: [] } }),
      uploadBlock: jest.fn().mockResolvedValue({ data: {} }),
      createFileFromBlocks: jest.fn().mockRejectedValue(
        Object.assign(new Error('quota'), { response: { status: 403, data: { error: 'storage quota exceeded' } } }),
      ),
    };
    const hashFn = jest.fn().mockResolvedValue({ blocks: [{ index: 0, sha256: 'h0', size: 10 }], size: 10 });

    await expect(
      uploadFileViaBlocks(makeFile(10), { repoID: 'r', api, hashFn, blockSize: 50 }),
    ).rejects.toThrow('quota');
  });
});

test('aborts before starting when the caller signal is already cancelled', async () => {
  const controller = new AbortController();
  controller.abort();
  const api = {
    createBlockUploadSession: jest.fn(),
    checkBlocks: jest.fn(),
    uploadBlock: jest.fn(),
    createFileFromBlocks: jest.fn(),
  };

  await expect(
    uploadFileViaBlocks(makeFile(10), { repoID: 'r', api, signal: controller.signal }),
  ).rejects.toHaveProperty('name', 'AbortError');

  expect(api.createBlockUploadSession).not.toHaveBeenCalled();
});

