// Global concurrency limiter for web block (content-addressed / CAS) uploads.
//
// WHY: each block-upload file runs its own orchestrator. Without a SHARED limiter
// every file uploaded its blocks with its own fixed pool, so N large files dropped
// at once opened N×pool concurrent requests — there was no cross-file ceiling. This
// limiter is a single semaphore shared by every block upload in the component, so
// the TOTAL number of blocks on the wire never exceeds the configured ceiling
// (`simultaneous_uploads`, sourced from config — never hardcoded here).
//
// PR2 (this module): STATIC ceiling — `effective === max` always. A later PR adds an
// adaptive ramp (start at 1, climb to `max` while the link stays healthy, drop back
// to 1 when it degrades) by moving `effective` via setMax/note* hooks; the acquire
// machinery here already honours a changing `effective`.

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
  // effective is the live ceiling acquire() honours. Static (= max) in this PR.
  let effective = max;
  let inFlight = 0;
  const waiters = []; // FIFO: { resolve, reject, signal, onAbort }

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

  // setMax moves the live ceiling within [1, max]. Used by the adaptive ramp (later
  // PR); included here so acquire/pump already react to a changing ceiling.
  const setMax = (n) => {
    effective = Math.max(1, Math.min(max, Math.floor(Number(n) || 1)));
    pump();
  };

  // reset drops any queued waiters (their session ended/was cancelled) and restores
  // the static ceiling. It does NOT zero inFlight: live uploads release their own
  // slot via the finally release(), so the count self-heals.
  const reset = () => {
    while (waiters.length > 0) {
      const waiter = waiters.shift();
      detachAbort(waiter);
      waiter.reject(createAbortError());
    }
    effective = max;
  };

  return {
    acquire,
    setMax,
    reset,
    getEffective: () => effective,
    getMaxConcurrency: () => max,
    getInFlight: () => inFlight,
    getWaiterCount: () => waiters.length,
  };
}
