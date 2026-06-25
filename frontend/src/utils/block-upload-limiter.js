// Global concurrency limiter for web block (content-addressed / CAS) uploads.
//
// WHY: each block-upload file runs its own orchestrator. Without a SHARED limiter
// every file uploaded its blocks with its own fixed pool, so N large files dropped
// at once opened N×pool concurrent requests — there was no cross-file ceiling. This
// limiter is a single semaphore shared by every block upload in the component, so
// the TOTAL number of blocks on the wire never exceeds the configured ceiling
// (`simultaneous_uploads`, sourced from config — never hardcoded here).
//
// ADAPTIVE: the live ceiling `effective` starts at 1 and climbs toward `max` while
// the link stays healthy (sustained throughput), and drops back to 1 when the link
// degrades (a sustained bitrate collapse, or a block upload failure/retry). This
// mirrors the legacy resumable adaptive engine's intent — "start at 1 and ramp up to
// the configured ceiling when the link stays healthy" — for the block flow, fed by
// `noteBitrate` (aggregate block throughput), `noteFailure`, and `noteRetry`.

// Ramp tuning (sample-based so it is deterministic and testable without timers; a
// "sample" is one noteBitrate call, throttled upstream to ~one per 500 ms):
const RAMP_MIN_SAMPLES = 3;   // consecutive healthy samples before a +1 ramp-up step
const DEGRADE_MIN_SAMPLES = 2; // consecutive low samples before dropping to 1
const DROP_RATIO = 0.6;       // sample < DROP_RATIO × smoothed ⇒ "low" (link degraded)
const EMA_ALPHA = 0.4;        // smoothing weight for the throughput baseline

function createAbortError() {
  if (typeof DOMException === 'function') {
    return new DOMException('Upload aborted', 'AbortError');
  }
  const error = new Error('Upload aborted');
  error.name = 'AbortError';
  return error;
}

// createBlockLimiter returns a shared async semaphore.
//   acquire({ signal }) -> Promise<release>
//     - resolves with an idempotent release() once a slot is free;
//     - if `signal` is already aborted, rejects immediately WITHOUT taking a slot;
//     - if it is waiting and `signal` aborts, it is removed from the queue and
//       rejects (no "ghost" upload that starts after the user cancelled).
//   release() (the resolved value) frees the slot and hands it to the next waiter.
export function createBlockLimiter({ maxConcurrency } = {}) {
  const max = Math.max(1, Math.floor(Number(maxConcurrency) || 1));
  // effective is the live ceiling acquire() honours. Starts conservative (1) and is
  // moved by the adaptive ramp between 1 and max.
  let effective = 1;
  let inFlight = 0;
  const waiters = []; // FIFO: { resolve, reject, signal, onAbort }

  // Adaptive ramp state.
  let smoothed = 0;        // EMA of healthy throughput samples (bits/s)
  let healthySamples = 0;  // consecutive healthy samples since the last ramp-up
  let lowSamples = 0;      // consecutive low samples (toward a degrade)

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

  // setEffective moves the live ceiling within [1, max]. Lowering it does NOT abort
  // in-flight uploads (we cannot un-send a block); it just stops NEW acquires until
  // inFlight falls below the new ceiling. Raising it pumps queued waiters.
  const setEffective = (n) => {
    effective = Math.max(1, Math.min(max, Math.floor(Number(n) || 1)));
    pump();
  };

  const degradeToOne = () => {
    healthySamples = 0;
    lowSamples = 0;
    setEffective(1);
  };

  // noteBitrate feeds one aggregate-throughput sample (bits/s) into the ramp. A run
  // of healthy samples ramps effective up by one step; a run of low samples (a
  // sustained collapse vs the smoothed baseline) drops it to 1. Idle/zero samples
  // (e.g. the pure-hashing phase, before any bytes are on the wire) are ignored so
  // they neither ramp nor degrade.
  const noteBitrate = (bitsPerSecond) => {
    const v = Math.max(0, Number(bitsPerSecond) || 0);
    if (v <= 0) {
      return;
    }
    if (smoothed === 0) {
      smoothed = v;
      healthySamples = 1;
      lowSamples = 0;
      return;
    }
    if (v < DROP_RATIO * smoothed) {
      lowSamples += 1;
      healthySamples = 0;
      if (lowSamples >= DEGRADE_MIN_SAMPLES) {
        smoothed = v; // re-baseline at the degraded level
        degradeToOne();
      }
      return;
    }
    // Healthy (stable or improving): update the baseline and count toward a ramp-up.
    lowSamples = 0;
    smoothed = EMA_ALPHA * v + (1 - EMA_ALPHA) * smoothed;
    healthySamples += 1;
    if (healthySamples >= RAMP_MIN_SAMPLES && effective < max) {
      healthySamples = 0; // cooldown: another full run is needed before the next step
      setEffective(effective + 1);
    }
  };

  // A real block-upload failure/retry (stall, timeout, transport error) is a strong
  // "link is unhealthy" signal — drop straight to 1.
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
    effective = 1;
    smoothed = 0;
    healthySamples = 0;
    lowSamples = 0;
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
