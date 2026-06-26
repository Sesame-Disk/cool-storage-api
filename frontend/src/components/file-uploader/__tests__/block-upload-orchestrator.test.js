import { isStallError, uploadFileViaBlocks } from '../block-upload-orchestrator';
import { createBlockLimiter } from '../../../utils/block-upload-limiter';

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
        { index: 0, sha1: 's0', sha256: 'h0', size: 50 },
        { index: 1, sha1: 's1', sha256: 'h1', size: 50 },
      ],
      size: 100,
    });

    const res = await uploadFileViaBlocks(makeFile(100), {
      repoID: 'r1', parentDir: '/', api, hashFn, concurrency: 2, blockSize: 50,
    });

    // Control-plane calls now carry a bounded timeout so a half-open socket can't
    // hang the flow forever.
    expect(api.createBlockUploadSession).toHaveBeenCalledWith('r1', '/', expect.objectContaining({ timeout: expect.any(Number) }));
    expect(api.checkBlocks).toHaveBeenCalledWith(['h0', 'h1'], 'sess1', expect.objectContaining({ timeout: expect.any(Number) }));
    // Only the block reported missing is uploaded (dedup/resume).
    expect(uploaded).toEqual(['h1']);
    expect(api.uploadBlock).toHaveBeenCalledTimes(1);
    // Commit carries the full ordered dual-hash manifest (sha256 = storage id,
    // sha1 = external Seafile block id for desktop/mobile download compat).
    const manifest = api.createFileFromBlocks.mock.calls[0][1];
    expect(manifest.blocks).toEqual([
      { sha1: 's0', sha256: 'h0', size: 50 },
      { sha1: 's1', sha256: 'h1', size: 50 },
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
      blocks: [{ index: 0, sha1: 's0', sha256: 'h0', size: 50 }],
      size: 50,
    });

    const res = await uploadFileViaBlocks(makeFile(50), { repoID: 'r', api, hashFn, blockSize: 50 });

    expect(uploaded).toEqual(['h0']); // re-uploaded the block the commit demanded
    expect(commitCalls).toBe(2);
    expect(res[0].name).toBe('f');
    // Both commit attempts carry the dual-hash manifest (no sha1: undefined on retry).
    api.createFileFromBlocks.mock.calls.forEach(([, manifest]) => {
      expect(manifest.blocks).toEqual([{ sha1: 's0', sha256: 'h0', size: 50 }]);
    });
  });

  test('retries the commit on "commit still in progress" with backoff until the idempotent result', async () => {
    let commitCalls = 0;
    const api = {
      createBlockUploadSession: jest.fn().mockResolvedValue({ data: { session_id: 's' } }),
      checkBlocks: jest.fn().mockResolvedValue({ data: { missing: [] } }),
      uploadBlock: jest.fn().mockResolvedValue({ data: {} }),
      createFileFromBlocks: jest.fn().mockImplementation(() => {
        commitCalls += 1;
        if (commitCalls < 3) {
          const err = new Error('in progress');
          err.response = { status: 409, data: { code: 'commit_in_progress', error: 'commit still in progress; retry' } };
          return Promise.reject(err);
        }
        return Promise.resolve({ data: [{ name: 'f', id: 'i', size: '10' }] });
      }),
    };
    const hashFn = jest.fn().mockResolvedValue({ blocks: [{ index: 0, sha1: 's0', sha256: 'h0', size: 10 }], size: 10 });

    const res = await uploadFileViaBlocks(makeFile(10), {
      repoID: 'r', api, hashFn, blockSize: 50, commitRetryBaseMs: 1,
    });

    // The two "in progress" responses are retried; the third returns the result.
    expect(commitCalls).toBe(3);
    expect(res[0].name).toBe('f');
  });

  test('does not retry a permanent 409 (different file) as "in progress"', async () => {
    const api = {
      createBlockUploadSession: jest.fn().mockResolvedValue({ data: { session_id: 's' } }),
      checkBlocks: jest.fn().mockResolvedValue({ data: { missing: [] } }),
      uploadBlock: jest.fn().mockResolvedValue({ data: {} }),
      createFileFromBlocks: jest.fn().mockRejectedValue(
        Object.assign(new Error('different'), {
          response: { status: 409, data: { code: 'session_committed_different_file', error: 'session already committed a different file' } },
        }),
      ),
    };
    const hashFn = jest.fn().mockResolvedValue({ blocks: [{ index: 0, sha1: 's0', sha256: 'h0', size: 10 }], size: 10 });

    await expect(
      uploadFileViaBlocks(makeFile(10), { repoID: 'r', api, hashFn, blockSize: 50, commitRetryBaseMs: 1 }),
    ).rejects.toThrow('different');
    // A permanent 409 is thrown on the first attempt, never retried as in-progress.
    expect(api.createFileFromBlocks).toHaveBeenCalledTimes(1);
  });

  test('does not retry the old ambiguous 409 message without an explicit retryable code', async () => {
    const api = {
      createBlockUploadSession: jest.fn().mockResolvedValue({ data: { session_id: 's' } }),
      checkBlocks: jest.fn().mockResolvedValue({ data: { missing: [] } }),
      uploadBlock: jest.fn().mockResolvedValue({ data: {} }),
      createFileFromBlocks: jest.fn().mockRejectedValue(
        Object.assign(new Error('ambiguous'), {
          response: { status: 409, data: { error: 'session already committed a different file or commit is still in progress' } },
        }),
      ),
    };
    const hashFn = jest.fn().mockResolvedValue({ blocks: [{ index: 0, sha1: 's0', sha256: 'h0', size: 10 }], size: 10 });

    await expect(
      uploadFileViaBlocks(makeFile(10), { repoID: 'r', api, hashFn, blockSize: 50, commitRetryBaseMs: 1 }),
    ).rejects.toThrow('ambiguous');
    expect(api.createFileFromBlocks).toHaveBeenCalledTimes(1);
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
    const hashFn = jest.fn().mockResolvedValue({ blocks: [{ index: 0, sha1: 's0', sha256: 'h0', size: 10 }], size: 10 });

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

describe('phase reporting (onPhase)', () => {
  test('emits hashing -> checking -> uploading -> saving in order', async () => {
    const phases = [];
    const api = {
      createBlockUploadSession: jest.fn().mockResolvedValue({ data: { session_id: 's' } }),
      checkBlocks: jest.fn().mockResolvedValue({ data: { missing: ['h0'] } }),
      uploadBlock: jest.fn().mockResolvedValue({ data: {} }),
      createFileFromBlocks: jest.fn().mockResolvedValue({ data: [{ name: 'f', id: 'i', size: '10' }] }),
    };
    const hashFn = jest.fn().mockResolvedValue({ blocks: [{ index: 0, sha1: 's0', sha256: 'h0', size: 10 }], size: 10 });

    await uploadFileViaBlocks(makeFile(10), {
      repoID: 'r', api, hashFn, blockSize: 50, onPhase: (p) => phases.push(p),
    });

    expect(phases).toEqual(['hashing', 'checking', 'uploading', 'saving']);
  });
});

describe('dedup plan reporting (onPlan)', () => {
  test('reports uploaded vs deduplicated bytes from the missing set', async () => {
    // 3 blocks of 50 bytes (last is 30) = 130 bytes total; only block 1 is missing,
    // so 50 bytes are uploaded and 80 (blocks 0 + 2) were already on the server.
    let plan = null;
    const api = {
      createBlockUploadSession: jest.fn().mockResolvedValue({ data: { session_id: 's' } }),
      checkBlocks: jest.fn().mockResolvedValue({ data: { missing: ['h1'] } }),
      uploadBlock: jest.fn().mockResolvedValue({ data: {} }),
      createFileFromBlocks: jest.fn().mockResolvedValue({ data: [{ name: 'f', id: 'i', size: '130' }] }),
    };
    const hashFn = jest.fn().mockResolvedValue({
      blocks: [
        { index: 0, sha256: 'h0', size: 50 },
        { index: 1, sha256: 'h1', size: 50 },
        { index: 2, sha256: 'h2', size: 30 },
      ],
      size: 130,
    });

    await uploadFileViaBlocks(makeFile(130), {
      repoID: 'r', api, hashFn, blockSize: 50, onPlan: (p) => { plan = p; },
    });

    expect(plan).toEqual({ totalBytes: 130, uploadBytes: 50, dedupedBytes: 80 });
  });

  test('reports zero dedup when every block is missing', async () => {
    let plan = null;
    const api = {
      createBlockUploadSession: jest.fn().mockResolvedValue({ data: { session_id: 's' } }),
      checkBlocks: jest.fn().mockResolvedValue({ data: { missing: ['h0'] } }),
      uploadBlock: jest.fn().mockResolvedValue({ data: {} }),
      createFileFromBlocks: jest.fn().mockResolvedValue({ data: [{ name: 'f', id: 'i', size: '10' }] }),
    };
    const hashFn = jest.fn().mockResolvedValue({ blocks: [{ index: 0, sha256: 'h0', size: 10 }], size: 10 });

    await uploadFileViaBlocks(makeFile(10), {
      repoID: 'r', api, hashFn, blockSize: 50, onPlan: (p) => { plan = p; },
    });

    expect(plan).toEqual({ totalBytes: 10, uploadBytes: 10, dedupedBytes: 0 });
  });
});

describe('stall watchdog', () => {
  test('a stalled block upload is retried (not treated as a user cancel) and then succeeds', async () => {
    let attempt = 0;
    const api = {
      createBlockUploadSession: jest.fn().mockResolvedValue({ data: { session_id: 's' } }),
      checkBlocks: jest.fn().mockResolvedValue({ data: { missing: ['h0'] } }),
      // First attempt never resolves until the watchdog aborts its signal; the
      // retry resolves normally. This is the "connection dropped mid-block" case.
      uploadBlock: jest.fn().mockImplementation((session, hash, data, config) => {
        attempt += 1;
        if (attempt === 1) {
          return new Promise((_resolve, reject) => {
            config.signal.addEventListener('abort', () => {
              const err = new Error('canceled');
              err.name = 'AbortError';
              reject(err);
            });
          });
        }
        return Promise.resolve({ data: {} });
      }),
      createFileFromBlocks: jest.fn().mockResolvedValue({ data: [{ name: 'f', id: 'i', size: '10' }] }),
    };
    const hashFn = jest.fn().mockResolvedValue({ blocks: [{ index: 0, sha1: 's0', sha256: 'h0', size: 10 }], size: 10 });

    const res = await uploadFileViaBlocks(makeFile(10), {
      repoID: 'r', api, hashFn, blockSize: 50, retries: 3,
      blockStallTimeoutMs: 20, // tiny so the first attempt's stall fires fast
    });

    expect(attempt).toBe(2); // stalled once, retried once, succeeded
    expect(res[0].name).toBe('f');
  });

  test('isStallError distinguishes a stall from an abort', () => {
    const stall = new Error('x'); stall.name = 'StallTimeoutError';
    const abort = new Error('y'); abort.name = 'AbortError';
    expect(isStallError(stall)).toBe(true);
    expect(isStallError(abort)).toBe(false);
    expect(isStallError(null)).toBe(false);
  });
});

describe('global concurrency limiter (shared across files)', () => {
  // hashOf returns N blocks tagged by file (sha256 = `${tag}${i}` so the first char
  // identifies the file in the upload-order trace).
  const hashOf = (tag, n) => jest.fn().mockResolvedValue({
    blocks: Array.from({ length: n }, (_, i) => ({ index: i, sha1: `${tag}sha1-${i}`, sha256: `${tag}${i}`, size: 1 })),
    size: n,
  });

  // The limiter starts adaptive at 1; feed healthy samples so it reaches `target`
  // before the upload, to exercise the ceiling at >1 deterministically.
  const rampTo = (limiter, target) => {
    for (let i = 0; i < 100 && limiter.getEffective() < target; i += 1) {
      limiter.noteBitrate(1000000);
    }
  };

  test('never exceeds the shared ceiling across multiple files (no N×max)', async () => {
    const track = { active: 0, peak: 0 };
    const api = {
      createBlockUploadSession: jest.fn().mockResolvedValue({ data: { session_id: 's' } }),
      checkBlocks: jest.fn((batch) => Promise.resolve({ data: { missing: batch.slice() } })),
      uploadBlock: jest.fn(() => {
        track.active += 1;
        track.peak = Math.max(track.peak, track.active);
        return new Promise((resolve) => setTimeout(() => { track.active -= 1; resolve({ data: {} }); }, 0));
      }),
      createFileFromBlocks: jest.fn().mockResolvedValue({ data: [{ name: 'f', id: 'i', size: '1' }] }),
    };
    const limiter = createBlockLimiter({ maxConcurrency: 2 });
    rampTo(limiter, 2);

    await Promise.all([
      uploadFileViaBlocks(makeFile(4), { repoID: 'r', api, hashFn: hashOf('A', 4), blockSize: 1, limiter }),
      uploadFileViaBlocks(makeFile(4), { repoID: 'r', api, hashFn: hashOf('B', 4), blockSize: 1, limiter }),
      uploadFileViaBlocks(makeFile(4), { repoID: 'r', api, hashFn: hashOf('C', 4), blockSize: 1, limiter }),
    ]);

    // 3 files × 4 blocks were uploaded, concurrency reaches the ceiling but never
    // exceeds it (proves the shared cap, not N×max).
    expect(api.uploadBlock).toHaveBeenCalledTimes(12);
    expect(track.peak).toBe(2);
    expect(limiter.getInFlight()).toBe(0);
  });

  test('does not starve later files: another file starts before the first finishes all its blocks', async () => {
    const starts = [];
    const api = {
      createBlockUploadSession: jest.fn().mockResolvedValue({ data: { session_id: 's' } }),
      checkBlocks: jest.fn((batch) => Promise.resolve({ data: { missing: batch.slice() } })),
      uploadBlock: jest.fn((session, hash) => {
        starts.push(hash[0]); // file tag
        return new Promise((resolve) => setTimeout(() => resolve({ data: {} }), 0));
      }),
      createFileFromBlocks: jest.fn().mockResolvedValue({ data: [{ name: 'f', id: 'i', size: '1' }] }),
    };
    const limiter = createBlockLimiter({ maxConcurrency: 2 });
    rampTo(limiter, 2);

    await Promise.all([
      uploadFileViaBlocks(makeFile(6), { repoID: 'r', api, hashFn: hashOf('A', 6), blockSize: 1, limiter }),
      uploadFileViaBlocks(makeFile(6), { repoID: 'r', api, hashFn: hashOf('B', 6), blockSize: 1, limiter }),
      uploadFileViaBlocks(makeFile(6), { repoID: 'r', api, hashFn: hashOf('C', 6), blockSize: 1, limiter }),
    ]);

    // A worker re-queues at the back after each block, so a later file gets a turn
    // before the first file drains all of its blocks (no FIFO monopoly).
    const lastA = starts.lastIndexOf('A');
    const firstNonA = starts.findIndex((t) => t !== 'A');
    expect(firstNonA).toBeGreaterThanOrEqual(0);
    expect(firstNonA).toBeLessThan(lastA);
  });

  test('a block waiting for a slot is NOT uploaded after its file is cancelled', async () => {
    const calls = [];
    let releaseA;
    const api = {
      createBlockUploadSession: jest.fn().mockResolvedValue({ data: { session_id: 's' } }),
      checkBlocks: jest.fn((batch) => Promise.resolve({ data: { missing: batch.slice() } })),
      uploadBlock: jest.fn((session, hash) => {
        calls.push(hash);
        return new Promise((resolve) => {
          if (hash[0] === 'A') {
            releaseA = () => resolve({ data: {} }); // A holds the only slot until we let go
          }
        });
      }),
      createFileFromBlocks: jest.fn().mockResolvedValue({ data: [{ name: 'f', id: 'i', size: '1' }] }),
    };
    const limiter = createBlockLimiter({ maxConcurrency: 1 });
    const ctrlB = new AbortController();

    const pA = uploadFileViaBlocks(makeFile(1), { repoID: 'r', api, hashFn: hashOf('A', 1), blockSize: 1, limiter });
    await new Promise((r) => setTimeout(r, 5)); // let A take the only slot
    const pB = uploadFileViaBlocks(makeFile(1), {
      repoID: 'r', api, hashFn: hashOf('B', 1), blockSize: 1, limiter, signal: ctrlB.signal,
    });
    await new Promise((r) => setTimeout(r, 5)); // B's block is now queued behind A

    ctrlB.abort(); // cancel B while it waits for a slot
    await expect(pB).rejects.toHaveProperty('name', 'AbortError');
    expect(calls).toEqual(['A0']); // B never uploaded

    releaseA();
    await pA;
    expect(calls).toEqual(['A0']); // still only A even after the slot freed
    expect(limiter.getInFlight()).toBe(0);
  });

  test('a failed block upload tells the limiter to back off (noteFailure)', async () => {
    let attempt = 0;
    const api = {
      createBlockUploadSession: jest.fn().mockResolvedValue({ data: { session_id: 's' } }),
      checkBlocks: jest.fn((batch) => Promise.resolve({ data: { missing: batch.slice() } })),
      uploadBlock: jest.fn(() => {
        attempt += 1;
        if (attempt === 1) {
          return Promise.reject(new Error('network'));
        }
        return Promise.resolve({ data: {} });
      }),
      createFileFromBlocks: jest.fn().mockResolvedValue({ data: [{ name: 'f', id: 'i', size: '1' }] }),
    };
    const limiter = {
      acquire: jest.fn().mockResolvedValue(() => {}),
      noteFailure: jest.fn(),
      getMaxConcurrency: () => 1,
    };

    await uploadFileViaBlocks(makeFile(1), { repoID: 'r', api, hashFn: hashOf('A', 1), blockSize: 1, limiter, retries: 3 });

    // The first attempt failed (non-abort) → limiter backs off; the retry succeeds.
    expect(limiter.noteFailure).toHaveBeenCalledTimes(1);
    expect(attempt).toBe(2);
  });
});

