// Backoff for server-side upload throttling (HTTP 429).
//
// resumable.js has no notion of throttling. 429 is not in its `permanentErrors`,
// so it does retry — but with `chunkRetryInterval` unset it retries *immediately*,
// and `maxChunkRetries` counts those attempts. Against a token bucket that refills
// a couple of times a second, four attempts land within milliseconds, all fail,
// and the library reports the file as permanently failed. A limiter meant to slow
// an upload down would instead kill it, which is worse than having no limiter.
//
// Both option lookups happen through `getOpt` at retry time, and the `fileRetry`
// event fires synchronously *before* them, so a handler on that event can adjust
// the retry policy for the retry that is about to be scheduled. That is the hook
// this module uses.

const THROTTLED_STATUS = 429;

// Baseline retry policy, exported so the uploaders and this module cannot drift:
// the ceiling raised while throttled must be restored to the same value.
export const BASE_MAX_CHUNK_RETRIES = 3;

// While throttled, retries are cheap (the request is refused before any work) and
// spaced out, so a much higher ceiling is what turns a burst of 429s into a pause
// instead of a failure.
export const THROTTLED_MAX_CHUNK_RETRIES = 12;

const MIN_BACKOFF_MS = 1000;
const MAX_BACKOFF_MS = 16000;
const JITTER_RATIO = 0.25;

// How long the raised retry ceiling outlives the retry it was raised for.
//
// The ceiling has to be sticky because the options are shared by every chunk of
// every file, while the events that set them are per-chunk. Without this, a
// network error on an unrelated file restores maxChunkRetries to the baseline,
// and a throttled chunk that has already used those attempts is failed by
// `status()` — which reads the ceiling *before* the retry event fires, so this
// module never gets the chance to raise it again.
//
// The window covers the scheduled wait plus the round trip of the attempt after
// it. Its only cost when nothing is throttled is a briefly generous retry
// ceiling, which is harmless; the interval is restored immediately either way,
// so ordinary network retries stay fast.
const THROTTLE_STICKY_GRACE_MS = 15000;

// Per-instance expiry of the raised ceiling. A WeakMap rather than a field on
// the instance: no shared module state between two uploaders on one page, and
// nothing to clean up.
const throttledUntil = new WeakMap();

// parseRetryAfterMs reads the server's Retry-After. Only delta-seconds is
// accepted: it is the form SesameFS sends, and guessing at an HTTP-date would
// risk a wildly wrong wait from a clock skew.
export function parseRetryAfterMs(headerValue) {
  if (!headerValue) return null;
  const seconds = Number(String(headerValue).trim());
  if (!Number.isFinite(seconds) || seconds < 0) return null;
  return seconds * 1000;
}

// backoffMs is the wait before the next attempt: the server's own figure when it
// gave one, otherwise exponential from the attempt count. Jitter is applied in
// both cases — several chunks of the same file are throttled at once, and without
// it they would retry in lockstep and re-collide on the same empty bucket.
//
// The jitter is one-sided when the server named a time. Retry-After is a floor,
// not an estimate: spreading below it would retry into a bucket the server has
// just told us is still empty. Around our own exponential guess there is no such
// floor, so jitter goes both ways there.
export function backoffMs(retryAfterHeader, attempt, random = Math.random) {
  const fromHeader = parseRetryAfterMs(retryAfterHeader);
  const base = fromHeader === null
    ? Math.min(MIN_BACKOFF_MS * Math.pow(2, Math.max(0, attempt)), MAX_BACKOFF_MS)
    : Math.max(fromHeader, MIN_BACKOFF_MS);

  const spread = fromHeader === null ? random() * 2 - 1 : random();
  const jitter = base * JITTER_RATIO * spread;
  return Math.max(MIN_BACKOFF_MS, Math.round(base + jitter));
}

// findThrottledChunk returns the chunk whose response was a 429, if any. The
// event carries the file, not the chunk, and `abort()` runs after the event, so
// the responses are still readable here.
function findThrottledChunk(resumableFile) {
  const chunks = (resumableFile && resumableFile.chunks) || [];
  for (const chunk of chunks) {
    if (chunk && chunk.xhr && chunk.xhr.status === THROTTLED_STATUS) {
      return chunk;
    }
  }
  return null;
}

// noteUploadRetry adjusts the retry policy for the retry about to be scheduled,
// and reports whether this one was caused by throttling.
//
// The two options it touches are restored on different schedules, and the
// difference is the point:
//
//   chunkRetryInterval is restored immediately on any non-429 retry, so a
//   network blip retries at once instead of inheriting a multi-second wait. It
//   is re-applied per 429 anyway, so a throttled chunk loses nothing.
//
//   maxChunkRetries stays raised until the sticky window lapses. resumable.js
//   reads it in `status()` BEFORE emitting the retry event, so lowering it while
//   a throttled chunk still has a retry pending fails that chunk outright, with
//   no event that would let this module raise it back.
export function noteUploadRetry(resumable, resumableFile, now = Date.now) {
  if (!resumable || !resumable.opts) return false;

  const chunk = findThrottledChunk(resumableFile);
  if (!chunk) {
    resumable.opts.chunkRetryInterval = undefined;
    if ((throttledUntil.get(resumable) || 0) <= now()) {
      resumable.opts.maxChunkRetries = BASE_MAX_CHUNK_RETRIES;
      throttledUntil.delete(resumable);
    }
    if (resumableFile) resumableFile.isThrottled = false;
    return false;
  }

  let retryAfter = null;
  try {
    retryAfter = chunk.xhr.getResponseHeader('Retry-After');
  } catch (e) {
    // A header read can throw on an aborted xhr in some browsers; the
    // exponential path below is the fallback that makes this non-fatal.
    retryAfter = null;
  }

  const delay = backoffMs(retryAfter, chunk.retries || 0);
  resumable.opts.chunkRetryInterval = delay;
  resumable.opts.maxChunkRetries = THROTTLED_MAX_CHUNK_RETRIES;
  throttledUntil.set(resumable, now() + delay + THROTTLE_STICKY_GRACE_MS);
  if (resumableFile) resumableFile.isThrottled = true;
  return true;
}

// clearThrottleState is called when a file finishes or fails for good, so a
// stale "server is busy" hint does not outlive the condition.
export function clearThrottleState(resumableFile) {
  if (resumableFile) resumableFile.isThrottled = false;
}
