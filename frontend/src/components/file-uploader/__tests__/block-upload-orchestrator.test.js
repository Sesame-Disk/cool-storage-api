import { isStallError, shouldUseBlockUpload, uploadFileViaBlocks } from '../block-upload-orchestrator';
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

describe('shouldUseBlockUpload (encrypted-library gate)', () => {
  const bigFile = makeFile(100 * 1024 * 1024); // 100 MB, well above the 64 MB threshold

  test('an encrypted library never routes through the block flow, even for a large file', () => {
    // Regression guard for the "encrypted libraries are not supported by the block
    // upload flow" 409: the block flow computes SHA-256 block IDs over PLAINTEXT on
    // the client, which is incompatible with server-side Seafile block encryption,
    // so encrypted libraries must stay on the resumable path. shouldUseBlockUpload
    // short-circuits on `encrypted` before any size/browser check — so a large file
    // in an encrypted library is NOT eligible.
    expect(shouldUseBlockUpload(bigFile, { encrypted: true })).toBe(false);
  });

  test('fails closed: a missing/undefined encrypted flag keeps files on resumable', () => {
    // The original bug was upstream — the in-app FileUploader was not given
    // repoEncrypted, so this gate saw undefined and (before the fix) assumed
    // not-encrypted. Now only a POSITIVE `encrypted === false` diverts to blocks, so
    // a parent that forgets the prop degrades to a safe resumable fallback, never a 409.
    expect(shouldUseBlockUpload(bigFile, {})).toBe(false);        // prop omitted
    expect(shouldUseBlockUpload(bigFile, { encrypted: undefined })).toBe(false);
    expect(shouldUseBlockUpload(bigFile, { encrypted: null })).toBe(false);
    expect(shouldUseBlockUpload(bigFile, { encrypted: 0 })).toBe(false); // int, not strict false
  });
});

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
    expect(api.createBlockUploadSession).toHaveBeenCalledWith('r1', '/', 100, expect.objectContaining({ timeout: expect.any(Number) }));
    expect(api.checkBlocks).toHaveBeenCalledWith(['h0', 'h1'], 'sess1', expect.objectContaining({ timeout: expect.any(Number) }));
    // Only the block reported missing is uploaded (dedup/resume).
    expect(uploaded).toEqual(['h1']);
    expect(api.uploadBlock).toHaveBeenCalledTimes(1);
    // Commit carries the full ordered manifest (sha256 = storage id + size only;
    // the external Seafile SHA-1 is derived server-side from blocks.sha1).
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
      blocks: [{ index: 0, sha1: 's0', sha256: 'h0', size: 50 }],
      size: 50,
    });

    const res = await uploadFileViaBlocks(makeFile(50), { repoID: 'r', api, hashFn, blockSize: 50 });

    expect(uploaded).toEqual(['h0']); // re-uploaded the block the commit demanded
    expect(commitCalls).toBe(2);
    expect(res[0].name).toBe('f');
    // Both commit attempts carry the same {sha256, size} manifest.
    api.createFileFromBlocks.mock.calls.forEach(([, manifest]) => {
      expect(manifest.blocks).toEqual([{ sha256: 'h0', size: 50 }]);
    });
  });

  test('reuses a cached hash plan on retry when the server block size matches', async () => {
    let cachedHashPlan = null;
    const api = {
      createBlockUploadSession: jest.fn().mockResolvedValue({ data: { session_id: 's', block_size: 50 } }),
      checkBlocks: jest.fn().mockResolvedValue({ data: { missing: [] } }),
      uploadBlock: jest.fn().mockResolvedValue({ data: {} }),
      createFileFromBlocks: jest.fn().mockResolvedValue({ data: [{ name: 'f', id: 'i', size: '50' }] }),
    };
    const hashFn = jest.fn().mockResolvedValue({
      blocks: [{ index: 0, sha256: 'h0', size: 50 }],
      size: 50,
    });
    const file = makeFile(50);

    await uploadFileViaBlocks(file, {
      repoID: 'r', api, hashFn, blockSize: 50, onHashCache: (cache) => { cachedHashPlan = cache; },
    });
    await uploadFileViaBlocks(file, {
      repoID: 'r', api, hashFn, blockSize: 50, hashCache: cachedHashPlan, onHashCache: (cache) => { cachedHashPlan = cache; },
    });

    expect(hashFn).toHaveBeenCalledTimes(1);
    expect(api.createBlockUploadSession).toHaveBeenCalledTimes(2);
    expect(api.checkBlocks).toHaveBeenCalledTimes(2);
  });

  test('retries /blocks/check on a transient 502 without re-hashing', async () => {
    const randomSpy = jest.spyOn(Math, 'random').mockReturnValue(0);
    let checkCalls = 0;
    const api = {
      createBlockUploadSession: jest.fn().mockResolvedValue({ data: { session_id: 's', block_size: 10 } }),
      checkBlocks: jest.fn().mockImplementation(() => {
        checkCalls += 1;
        if (checkCalls === 1) {
          return Promise.reject(Object.assign(new Error('bad gateway'), {
            response: { status: 502, data: { error: 'bad gateway' } },
          }));
        }
        return Promise.resolve({ data: { missing: [] } });
      }),
      uploadBlock: jest.fn().mockResolvedValue({ data: {} }),
      createFileFromBlocks: jest.fn().mockResolvedValue({ data: [{ name: 'f', id: 'i', size: '10' }] }),
    };
    const hashFn = jest.fn().mockResolvedValue({ blocks: [{ index: 0, sha256: 'h0', size: 10 }], size: 10 });

    try {
      await uploadFileViaBlocks(makeFile(10), {
        repoID: 'r', api, hashFn, blockSize: 10, controlPlaneRetryBaseMs: 1,
      });

      expect(checkCalls).toBe(2);
      expect(hashFn).toHaveBeenCalledTimes(1);
    } finally {
      randomSpy.mockRestore();
    }
  });

  test('normalizes controlPlaneRetries=0 to one real attempt instead of throwing undefined', async () => {
    const api = {
      createBlockUploadSession: jest.fn().mockRejectedValue(Object.assign(new Error('bad gateway'), {
        response: { status: 502, data: { error: 'bad gateway' } },
      })),
      checkBlocks: jest.fn(),
      uploadBlock: jest.fn(),
      createFileFromBlocks: jest.fn(),
    };
    const hashFn = jest.fn();

    await expect(
      uploadFileViaBlocks(makeFile(10), {
        repoID: 'r', api, hashFn, blockSize: 10, controlPlaneRetries: 0,
      }),
    ).rejects.toThrow('bad gateway');

    expect(api.createBlockUploadSession).toHaveBeenCalledTimes(1);
    expect(hashFn).not.toHaveBeenCalled();
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

  test('retries the commit on a transient 502 and preserves the uploaded work', async () => {
    let commitCalls = 0;
    const api = {
      createBlockUploadSession: jest.fn().mockResolvedValue({ data: { session_id: 's' } }),
      checkBlocks: jest.fn().mockResolvedValue({ data: { missing: ['h0'] } }),
      uploadBlock: jest.fn().mockResolvedValue({ data: {} }),
      createFileFromBlocks: jest.fn().mockImplementation(() => {
        commitCalls += 1;
        if (commitCalls === 1) {
          return Promise.reject(Object.assign(new Error('bad gateway'), {
            response: { status: 502, data: { error: 'bad gateway' } },
          }));
        }
        return Promise.resolve({ data: [{ name: 'f', id: 'i', size: '10' }] });
      }),
    };
    const hashFn = jest.fn().mockResolvedValue({ blocks: [{ index: 0, sha256: 'h0', size: 10 }], size: 10 });

    const res = await uploadFileViaBlocks(makeFile(10), {
      repoID: 'r', api, hashFn, blockSize: 10, commitRetryBaseMs: 1,
    });

    expect(api.uploadBlock).toHaveBeenCalledTimes(1);
    expect(hashFn).toHaveBeenCalledTimes(1);
    expect(commitCalls).toBe(2);
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

describe('upload slot handoff', () => {
  test('announces readiness after checking and waits before upload/commit work starts', async () => {
    const events = [];
    let releaseUploadSlot;
    const waitForUploadSlot = jest.fn(() => new Promise((resolve) => {
      releaseUploadSlot = () => {
        events.push('slot:released');
        resolve();
      };
    }));
    const onReadyForUpload = jest.fn(() => {
      events.push('ready');
    });
    const api = {
      createBlockUploadSession: jest.fn().mockResolvedValue({ data: { session_id: 's' } }),
      checkBlocks: jest.fn().mockResolvedValue({ data: { missing: ['h0'] } }),
      uploadBlock: jest.fn().mockImplementation(() => {
        events.push('upload');
        return Promise.resolve({ data: {} });
      }),
      createFileFromBlocks: jest.fn().mockImplementation(() => {
        events.push('commit');
        return Promise.resolve({ data: [{ name: 'f', id: 'i', size: '10' }] });
      }),
    };
    const hashFn = jest.fn().mockResolvedValue({ blocks: [{ index: 0, sha1: 's0', sha256: 'h0', size: 10 }], size: 10 });

    const promise = uploadFileViaBlocks(makeFile(10), {
      repoID: 'r',
      api,
      hashFn,
      blockSize: 50,
      onReadyForUpload,
      waitForUploadSlot,
    });

    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(onReadyForUpload).toHaveBeenCalledWith({ missingCount: 1, totalCount: 1 });
    expect(waitForUploadSlot).toHaveBeenCalledTimes(1);
    expect(events).toEqual(['ready']);
    expect(api.uploadBlock).not.toHaveBeenCalled();
    expect(api.createFileFromBlocks).not.toHaveBeenCalled();

    releaseUploadSlot();
    await promise;

    expect(events).toEqual(['ready', 'slot:released', 'upload', 'commit']);
  });

  test('skips the uploading phase when no blocks are missing, but still waits for the slot before commit', async () => {
    const phases = [];
    const api = {
      createBlockUploadSession: jest.fn().mockResolvedValue({ data: { session_id: 's' } }),
      checkBlocks: jest.fn().mockResolvedValue({ data: { missing: [] } }),
      uploadBlock: jest.fn().mockResolvedValue({ data: {} }),
      createFileFromBlocks: jest.fn().mockResolvedValue({ data: [{ name: 'f', id: 'i', size: '10' }] }),
    };
    const hashFn = jest.fn().mockResolvedValue({ blocks: [{ index: 0, sha1: 's0', sha256: 'h0', size: 10 }], size: 10 });

    await uploadFileViaBlocks(makeFile(10), {
      repoID: 'r',
      api,
      hashFn,
      blockSize: 50,
      onPhase: (phase) => phases.push(phase),
      waitForUploadSlot: () => Promise.resolve(),
    });

    expect(api.uploadBlock).not.toHaveBeenCalled();
    expect(phases).toEqual(['hashing', 'checking', 'saving']);
  });
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
    const limiter = createBlockLimiter({ maxConcurrency: 2, blockSize: 1 });
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
    const limiter = createBlockLimiter({ maxConcurrency: 2, blockSize: 1 });
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

  test('a transient block failure that recovers on retry calls noteRetry but not noteFailure', async () => {
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
      acquire: jest.fn().mockResolvedValue(() => { }),
      noteRetry: jest.fn(),
      noteFailure: jest.fn(),
      getMaxConcurrency: () => 1,
    };

    await uploadFileViaBlocks(makeFile(1), { repoID: 'r', api, hashFn: hashOf('A', 1), blockSize: 1, limiter, retries: 3 });

    // The failed first attempt backs off the retry path (resumable parity), but the
    // recovering retry means it is NOT a hard failure -> noteFailure must NOT fire.
    expect(limiter.noteRetry).toHaveBeenCalledTimes(1);
    expect(limiter.noteFailure).not.toHaveBeenCalled();
    expect(attempt).toBe(2);
  });

  test('noteFailure fires only when ALL retries are exhausted', async () => {
    let attempt = 0;
    const api = {
      createBlockUploadSession: jest.fn().mockResolvedValue({ data: { session_id: 's' } }),
      checkBlocks: jest.fn((batch) => Promise.resolve({ data: { missing: batch.slice() } })),
      uploadBlock: jest.fn(() => {
        attempt += 1;
        return Promise.reject(new Error('network'));
      }),
      createFileFromBlocks: jest.fn().mockResolvedValue({ data: [{ name: 'f', id: 'i', size: '1' }] }),
    };
    const limiter = {
      acquire: jest.fn().mockResolvedValue(() => { }),
      noteRetry: jest.fn(),
      noteFailure: jest.fn(),
      getMaxConcurrency: () => 1,
    };

    await expect(
      uploadFileViaBlocks(makeFile(1), { repoID: 'r', api, hashFn: hashOf('A', 1), blockSize: 1, limiter, retries: 3 }),
    ).rejects.toThrow('network');

    // Every failed attempt backs off the retry path; the final exhaustion also
    // marks a hard failure once.
    expect(limiter.noteRetry).toHaveBeenCalledTimes(3);
    expect(limiter.noteFailure).toHaveBeenCalledTimes(1);
    expect(attempt).toBe(3);
  });

  test('a 429 (per-user cap) is honored via Retry-After and does not fail the upload', async () => {
    let attempt = 0;
    const api = {
      createBlockUploadSession: jest.fn().mockResolvedValue({ data: { session_id: 's' } }),
      checkBlocks: jest.fn((batch) => Promise.resolve({ data: { missing: batch.slice() } })),
      uploadBlock: jest.fn(() => {
        attempt += 1;
        if (attempt <= 2) {
          const err = new Error('rate limited');
          err.response = { status: 429, headers: { 'retry-after': '0.05' } };
          return Promise.reject(err);
        }
        return Promise.resolve({ data: {} });
      }),
      createFileFromBlocks: jest.fn().mockResolvedValue({ data: [{ name: 'f', id: 'i', size: '1' }] }),
    };
    const limiter = {
      acquire: jest.fn().mockResolvedValue(() => { }),
      noteRetry: jest.fn(),
      noteFailure: jest.fn(),
      getMaxConcurrency: () => 1,
    };

    // retries:1 proves the 429 waits are SOFT — they do NOT consume the hard retry
    // budget (2 backpressure waits then success, even with only one hard attempt).
    await uploadFileViaBlocks(makeFile(1), { repoID: 'r', api, hashFn: hashOf('A', 1), blockSize: 1, limiter, retries: 1 });

    expect(attempt).toBe(3); // two 429s then a success — the block still uploads
    expect(limiter.noteRetry).toHaveBeenCalledTimes(2); // each 429 tells the limiter to back off
    expect(limiter.noteFailure).not.toHaveBeenCalled(); // backpressure is never a hard failure
  });

  test('a 429 on session creation (per-user session cap) is waited out, not a hard failure', async () => {
    let sessionAttempt = 0;
    const api = {
      createBlockUploadSession: jest.fn(() => {
        sessionAttempt += 1;
        if (sessionAttempt <= 2) {
          const err = new Error('too many concurrent uploads');
          err.response = { status: 429, headers: { 'retry-after': '0.01' } };
          return Promise.reject(err);
        }
        return Promise.resolve({ data: { session_id: 's' } });
      }),
      checkBlocks: jest.fn((batch) => Promise.resolve({ data: { missing: batch.slice() } })),
      uploadBlock: jest.fn().mockResolvedValue({ data: {} }),
      createFileFromBlocks: jest.fn().mockResolvedValue({ data: [{ name: 'f', id: 'i', size: '1' }] }),
    };

    // controlPlaneRetries:1 proves the session 429 waits are SOFT — two backpressure
    // waits then success, even with only a single hard control-plane attempt.
    const res = await uploadFileViaBlocks(makeFile(1), {
      repoID: 'r', api, hashFn: hashOf('A', 1), blockSize: 1, controlPlaneRetries: 1, controlPlaneRetryBaseMs: 1,
    });

    expect(sessionAttempt).toBe(3); // two 429s then a minted session
    expect(api.createFileFromBlocks).toHaveBeenCalledTimes(1); // the upload still commits
    expect(res).toEqual([{ name: 'f', id: 'i', size: '1' }]);
  });

  test('a block-delete 409 honors Retry-After without consuming the hard retry budget', async () => {
    let attempt = 0;
    const api = {
      createBlockUploadSession: jest.fn().mockResolvedValue({ data: { session_id: 's' } }),
      checkBlocks: jest.fn((batch) => Promise.resolve({ data: { missing: batch.slice() } })),
      uploadBlock: jest.fn(() => {
        attempt += 1;
        if (attempt === 1) {
          const err = new Error('block is being deleted; retry the upload');
          err.response = {
            status: 409,
            headers: { 'retry-after': '0.001' },
            data: { code: 'block_delete_in_progress' },
          };
          return Promise.reject(err);
        }
        return Promise.resolve({ data: {} });
      }),
      createFileFromBlocks: jest.fn().mockResolvedValue({ data: [{ name: 'f', id: 'i', size: '1' }] }),
    };
    const limiter = {
      acquire: jest.fn().mockResolvedValue(() => { }),
      noteRetry: jest.fn(),
      noteFailure: jest.fn(),
      getMaxConcurrency: () => 1,
    };

    await uploadFileViaBlocks(makeFile(1), { repoID: 'r', api, hashFn: hashOf('A', 1), blockSize: 1, limiter, retries: 1 });

    expect(attempt).toBe(2);
    expect(limiter.noteRetry).toHaveBeenCalledTimes(1);
    expect(limiter.noteFailure).not.toHaveBeenCalled();
  });

  test('a terminal staging-cap 429 is surfaced immediately instead of being soft-retried', async () => {
    let attempt = 0;
    const api = {
      createBlockUploadSession: jest.fn().mockResolvedValue({ data: { session_id: 's' } }),
      checkBlocks: jest.fn((batch) => Promise.resolve({ data: { missing: batch.slice() } })),
      uploadBlock: jest.fn(() => {
        attempt += 1;
        const err = new Error('session staging limit reached; commit the file or start a new upload');
        err.response = {
          status: 429,
          headers: { 'retry-after': '1' },
          data: { code: 'staging_cap_reached' },
        };
        return Promise.reject(err);
      }),
      createFileFromBlocks: jest.fn(),
    };
    const limiter = {
      acquire: jest.fn().mockResolvedValue(() => { }),
      noteRetry: jest.fn(),
      noteFailure: jest.fn(),
      getMaxConcurrency: () => 1,
    };

    await expect(
      uploadFileViaBlocks(makeFile(1), { repoID: 'r', api, hashFn: hashOf('A', 1), blockSize: 1, limiter, retries: 3 }),
    ).rejects.toThrow('session staging limit reached; commit the file or start a new upload');

    expect(attempt).toBe(1);
    expect(limiter.noteRetry).toHaveBeenCalledTimes(1);
    expect(limiter.noteFailure).toHaveBeenCalledTimes(1);
    expect(api.createFileFromBlocks).not.toHaveBeenCalled();
  });
});
