// Throughput-parity regression tests for the web block (CAS) upload flow.
//
// These lock in the two behaviours that brought block upload up to resumable.js
// throughput, and guard against silently regressing back to the ~2x-slower path:
//   1. blocks are streamed straight to the transport as a Blob (no second full
//      read of the file + heap materialisation per block), exactly like
//      resumable.js hands its slice to XHR;
//   2. after a transport failure the adaptive cooldown gates the RAMP (not the
//      degrade), so a link that just failed backs off for the penalty window
//      instead of being slammed back to full concurrency.
//
// Run: npx jest src/components/file-uploader/__tests__/block-upload-throughput-regression.test.js

import { uploadFileViaBlocks } from '../block-upload-orchestrator';
import { createBlockLimiter } from '../../../utils/block-upload-limiter';

jest.mock('../../../utils/seafile-api', () => ({ seafileAPI: {} }));
jest.mock('../../../utils/constants', () => ({ enableBlockUpload: true, blockUploadThresholdMB: 64 }));

// A File stand-in whose slice() returns a streamable Blob-like object. We tag it
// so the test can tell whether the orchestrator streamed the Blob straight to the
// transport (good — what resumable does) or eagerly read it into a JS ArrayBuffer
// (bad — a second full read of the file + heap materialisation per block).
function makeStreamableFile(size, name = 'big.bin') {
  return {
    name,
    size,
    slice: (start, end) => {
      const blob = {
        __isStreamableBlob: true,
        size: end - start,
        // arrayBuffer is still offered (the real Blob has it) but using it means
        // the bytes were materialised in JS instead of streamed from disk.
        arrayBuffer: () => Promise.resolve(new ArrayBuffer(end - start)),
      };
      return blob;
    },
  };
}

describe('block bytes are streamed, not eagerly read into the JS heap', () => {
  // resumable.js hands the Blob slice straight to XHR, which streams it disk→socket
  // with no JS-side copy and a single read. The block flow instead does
  // `file.slice(...).arrayBuffer()` in getBlockData, fully materialising every 8 MB
  // block in the JS heap AND re-reading the file a second time (the worker already
  // read it once to hash). That double-read + serial read-then-upload per worker was
  // the ~2x throughput gap on multi-GB files; getBlockData now returns the Blob slice.
  test('uploadBlock receives the streamable Blob slice (not an ArrayBuffer)', async () => {
    let receivedData = null;
    const api = {
      createBlockUploadSession: jest.fn().mockResolvedValue({ data: { session_id: 's' } }),
      checkBlocks: jest.fn().mockResolvedValue({ data: { missing: ['h0'] } }),
      uploadBlock: jest.fn((session, hash, data) => {
        receivedData = data;
        return Promise.resolve({ data: {} });
      }),
      createFileFromBlocks: jest.fn().mockResolvedValue({ data: [{ name: 'big.bin', id: 'i', size: '8' }] }),
    };
    const hashFn = jest.fn().mockResolvedValue({
      blocks: [{ index: 0, sha1: 's0', sha256: 'h0', size: 8 }],
      size: 8,
    });

    await uploadFileViaBlocks(makeStreamableFile(8), { repoID: 'r', api, hashFn, blockSize: 8 });

    expect(receivedData).not.toBeNull();
    // The slice must reach the transport as a streamable Blob, not an eager read.
    expect(receivedData.__isStreamableBlob).toBe(true);
    expect(receivedData instanceof ArrayBuffer).toBe(false);
  });
});

describe('cooldown after a failure gates the RAMP (resumable parity)', () => {
  // The reference engine (upload-finalization.js:updateAdaptiveUploadConcurrency)
  // returns early — NO ramp — while `now < state.cooldownUntil`, and degrades
  // UNCONDITIONALLY on a bitrate collapse. The block limiter mirrors this: a link
  // that JUST failed must back off for the 10 s penalty window instead of being
  // slammed back to full concurrency within ~1–2 s (which thrashes a flaky link).
  test('healthy samples do NOT ramp back up during the post-failure cooldown', () => {
    jest.useFakeTimers();
    try {
      jest.setSystemTime(0);
      const limiter = createBlockLimiter({ maxConcurrency: 3, blockSize: 1 });

      // Climb to 2, then take a real transport failure → drop to 1 + 10 s cooldown.
      for (let i = 0; i < 10 && limiter.getEffective() < 2; i += 1) {
        limiter.noteBitrate(1000000);
      }
      expect(limiter.getEffective()).toBe(2);
      limiter.noteFailure();
      expect(limiter.getEffective()).toBe(1);

      // Still inside the cooldown window (t = 1 s): healthy samples must be ignored
      // for ramping, exactly like resumable holds concurrency down after a penalty.
      jest.setSystemTime(1000);
      for (let i = 0; i < 12; i += 1) {
        limiter.noteBitrate(1000000);
      }
      // The limiter must hold at 1 during the cooldown (no ramp on the penalty window).
      expect(limiter.getEffective()).toBe(1);
    } finally {
      jest.useRealTimers();
    }
  });
});
