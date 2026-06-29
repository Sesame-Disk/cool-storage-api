// Global concurrency limiter for web block (content-addressed / CAS) uploads.
//
// WHY: each block-upload file runs its own orchestrator. Without a SHARED limiter
// every file uploaded its blocks with its own fixed pool, so N large files dropped
// at once opened N×pool concurrent requests — there was no cross-file ceiling. This
// limiter is a single semaphore shared by every block upload in the component, so
// the TOTAL number of blocks on the wire never exceeds the configured ceiling
// (`simultaneous_uploads`, sourced from config — never hardcoded here).
//
// ADAPTIVE POLICY (mirrors the legacy resumable adaptive engine in
// upload-finalization.js:updateAdaptiveUploadConcurrency):
//   - The live ceiling `effective` starts at MIN_CONCURRENCY (1) and climbs toward
//     `max` while the link stays healthy.
//   - `noteBitrate` feeds aggregate block throughput samples. It runs the same state
//     machine as resumable: EMA smoothing, drop detection, stability floor, minimum
//     bitrate per slot, stable-samples counting, gain check above 2 slots, and
//     cooldown after a degrade.
//   - `noteFailure` / `noteRetry` drop to MIN_CONCURRENCY with a 10 s cooldown.
//   - `noteSuccess` is intentionally absent: block completions alone do not justify
//     more concurrency — only sustained throughput does.

const MIN_CONCURRENCY = 1;
const DROP_RATIO = 0.55;           // bitrate < smoothed × 0.55 → degrade
const SMOOTHING_FACTOR = 0.7;      // EMA weight on previous smoothed bitrate
const STABLE_FLOOR_RATIO = 0.7;    // bitrate < smoothed × 0.7 → reset stable samples
const FIRST_RAMP_SAMPLES = 3;      // stable samples needed for 1→2 ramp
const NEXT_RAMP_SAMPLES = 5;       // stable samples needed for each subsequent ramp
const GAIN_RATIO = 1.05;           // minimum throughput gain to justify >2 slots
const COOLDOWN_MS = 10000;         // cooldown after a degrade
const DEFAULT_BLOCK_SIZE = 8 * 1024 * 1024; // must match backend WebUploadBlockSize

function createAbortError() {
  if (typeof DOMException === 'function') {
    return new DOMException('Upload aborted', 'AbortError');
  }
  const error = new Error('Upload aborted');
  error.name = 'AbortError';
  return error;
}

// minimumBitrateForSlots returns the minimum smoothed bitrate (bits/s) needed to
// justify running `slotCount` concurrent block uploads. Formula matches resumable's
// minimumBitrateForSlots: target per-block completion of max(3, 12 / slotCount) s.
const minimumBitrateForSlots = (blockBits, slotCount) => {
  if (blockBits <= 0 || slotCount <= MIN_CONCURRENCY) {
    return 0;
  }
  const targetSecondsPerBlock = Math.max(3, 12 / slotCount);
  return blockBits / targetSecondsPerBlock;
};

// createBlockLimiter returns a shared async semaphore.
//   acquire({ signal }) -> Promise<release>
//     - resolves with an idempotent release() once a slot is free;
//     - if `signal` is already aborted, rejects immediately WITHOUT taking a slot;
//     - if it is waiting and `signal` aborts, it is removed from the queue and
//       rejects (no "ghost" upload that starts after the user cancelled).
//   release() (the resolved value) frees the slot and hands it to the next waiter.
export function createBlockLimiter({ maxConcurrency, blockSize = DEFAULT_BLOCK_SIZE } = {}) {
  const max = Math.max(1, Math.floor(Number(maxConcurrency) || 1));
  const blockBits = Math.max(0, Math.floor(Number(blockSize) || 0)) * 8;

  // effective is the live ceiling acquire() honours. Starts conservative (1) and is
  // moved by the adaptive ramp between MIN_CONCURRENCY and max.
  let effective = MIN_CONCURRENCY;
  let inFlight = 0;
  const waiters = []; // FIFO: { resolve, reject, signal, onAbort }

  // Adaptive ramp state (mirrors resumable's adaptive state).
  let stableSamples = 0;
  let smoothedBitrate = 0;
  let lastBitrate = 0;
  let lastRampBitrate = 0;
  let cooldownUntil = 0;

  const detachAbort = (waiter) => {
    if (waiter.signal && waiter.onAbort) {
      waiter.signal.removeEventListener('abort', waiter.onAbort);
      waiter.onAbort = null;
    }
  };

  const makeRelease = () => {
    let released = false;
    return () => {
      if (released) {
        return; // idempotent: a double release must not free someone else's slot
      }
      released = true;
      inFlight -= 1;
      pump();
    };
  };

  // Hand free slots to queued waiters in FIFO order. A worker that finishes a block
  // and calls acquire() again goes to the BACK of this queue, so multiple files
  // interleave fairly instead of the first file monopolising every slot.
  function pump() {
    while (waiters.length > 0 && inFlight < effective) {
      const waiter = waiters.shift();
      detachAbort(waiter);
      inFlight += 1;
      waiter.resolve(makeRelease());
    }
  }

  const acquire = ({ signal } = {}) => {
    if (signal && signal.aborted) {
      return Promise.reject(createAbortError());
    }
    if (inFlight < effective) {
      inFlight += 1;
      return Promise.resolve(makeRelease());
    }
    return new Promise((resolve, reject) => {
      const waiter = { resolve, reject, signal, onAbort: null };
      if (signal) {
        waiter.onAbort = () => {
          const idx = waiters.indexOf(waiter);
          if (idx !== -1) {
            waiters.splice(idx, 1);
          }
          reject(createAbortError());
        };
        signal.addEventListener('abort', waiter.onAbort, { once: true });
      }
      waiters.push(waiter);
    });
  };

  // setEffective moves the live ceiling within [MIN_CONCURRENCY, max]. Lowering it
  // does NOT abort in-flight uploads (we cannot un-send a block); it just stops NEW
  // acquires until inFlight falls below the new ceiling. Raising it pumps waiters.
  const setEffective = (n) => {
    const was = effective;
    effective = Math.max(MIN_CONCURRENCY, Math.min(max, Math.floor(Number(n) || 1)));
    pump();
  };

  // degradeToOne drops to MIN_CONCURRENCY with cooldown and resets all adaptive
  // state. Called on sustained bitrate collapse, failure, or retry.
  const degradeToOne = (now = Date.now()) => {
    stableSamples = 0;
    smoothedBitrate = 0;
    lastBitrate = 0;
    lastRampBitrate = 0;
    cooldownUntil = now + COOLDOWN_MS;
    setEffective(MIN_CONCURRENCY);
  };

  // noteBitrate feeds one aggregate-throughput sample (bits/s) into the adaptive
  // state machine. Implements the same policy as resumable's
  // updateAdaptiveUploadConcurrency: EMA smoothing, drop detection, stability floor,
  // minimum bitrate per slot, stable-samples gate, and gain check for >2 slots.
  // Idle/zero samples (e.g. the pure-hashing phase, before bytes are on the wire)
  // are ignored.
  const noteBitrate = (bitsPerSecond) => {
    const v = Math.max(0, Number(bitsPerSecond) || 0);
    if (v <= 0) {
      return;
    }

    const previousSmoothed = smoothedBitrate;

    // Degrade on a sharp sustained drop — unconditional, matching resumable.
    // After a failure/retry the cooldown prevents a premature ramp, not the
    // degrade itself (the link IS in trouble — trust the measurement).
    if (previousSmoothed > 0 && v < previousSmoothed * DROP_RATIO) {
      degradeToOne();
      return;
    }

    // EMA smoothing (first sample seeds directly).
    smoothedBitrate = previousSmoothed > 0
      ? previousSmoothed * SMOOTHING_FACTOR + v * (1 - SMOOTHING_FACTOR)
      : v;
    lastBitrate = v;

    // Reset stable samples on instability (bitrate drifted below the floor).
    if (previousSmoothed > 0 && v < previousSmoothed * STABLE_FLOOR_RATIO) {
      stableSamples = 0;
      return;
    }

    stableSamples += 1;

    // Cooldown gate: after a failure/retry/degrade, wait out the penalty window
    // before ramping. Stable samples still accumulate so the ramp fires
    // immediately once the cooldown expires — same as resumable.
    if (Date.now() < cooldownUntil) {
      return;
    }

    const nextSlotCount = effective + 1;
    if (nextSlotCount > max) {
      return; // already at the ceiling
    }

    // Minimum bitrate gate: the smoothed throughput must be enough to justify
    // completing a block in a reasonable time with one more slot.
    if (smoothedBitrate < minimumBitrateForSlots(blockBits, nextSlotCount)) {
      return;
    }

    const requiredSamples = nextSlotCount === 2 ? FIRST_RAMP_SAMPLES : NEXT_RAMP_SAMPLES;
    if (stableSamples < requiredSamples) {
      return;
    }

    // Gain check: above 2 slots, require meaningful throughput improvement vs the
    // last ramp point (avoids adding concurrency that does not move the needle).
    if (nextSlotCount > 2 && lastRampBitrate > 0 && smoothedBitrate < lastRampBitrate * GAIN_RATIO) {
      return;
    }

    // All checks passed — ramp up.
    stableSamples = 0;
    lastRampBitrate = smoothedBitrate;
    setEffective(nextSlotCount);
  };

  // A real block-upload failure/retry (stall, timeout, transport error) is a strong
  // signal — drop straight to MIN_CONCURRENCY with cooldown.
  const noteFailure = () => degradeToOne();
  const noteRetry = () => degradeToOne();

  // reset drops any queued waiters (their session ended/was cancelled) and returns
  // to the conservative start (effective = 1, cleared ramp state). It does NOT zero
  // inFlight: live uploads release their own slot via the finally release(), so the
  // count self-heals.
  const reset = () => {
    while (waiters.length > 0) {
      const waiter = waiters.shift();
      detachAbort(waiter);
      waiter.reject(createAbortError());
    }
    effective = MIN_CONCURRENCY;
    stableSamples = 0;
    smoothedBitrate = 0;
    lastBitrate = 0;
    lastRampBitrate = 0;
    cooldownUntil = 0;
  };

  return {
    acquire,
    noteBitrate,
    noteFailure,
    noteRetry,
    reset,
    getEffective: () => effective,
    getMaxConcurrency: () => max,
    getInFlight: () => inFlight,
    getWaiterCount: () => waiters.length,
  };
}
