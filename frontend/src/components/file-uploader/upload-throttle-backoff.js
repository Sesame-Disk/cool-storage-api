// Backoff for server-side upload throttling (HTTP 429).
//
// resumable.js has no notion of throttling. 429 is not in its `permanentErrors`,
// so it does retry — but with `chunkRetryInterval` unset it retries *immediately*,
// and `maxChunkRetries` counts those attempts. Against a token bucket that refills
// a couple of times a second, four attempts land within milliseconds, all fail,
// and the library reports the file as permanently failed. A limiter meant to slow
// an upload down would instead kill it, which is worse than having no limiter.
//
// SesameFS patches the pinned resumable.js so fileRetry receives the triggering
// chunk and response metadata captured before the library clears its XHR. Both
// option lookups happen through `getOpt` after that event, so per-chunk options
// can adjust the retry that is about to be scheduled without affecting another
// concurrent chunk.

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

// noteUploadRetry adjusts the retry policy for the retry about to be scheduled,
// and reports whether this one was caused by throttling.
//
// The options live on the triggering chunk. This is load-bearing: instance-level
// options are shared by every chunk and caused unrelated retries to overwrite one
// another. The raised ceiling remains with this chunk for its lifetime; permanent
// HTTP errors are still permanent, while transient failures after a 429 retain
// enough attempts to survive sustained throttling.
export function noteUploadRetry(resumable, resumableFile, chunk, retryInfo) {
  if (!resumable || !resumable.opts || !chunk) return false;

  chunk.opts = chunk.opts || {};
  if (!retryInfo || retryInfo.status !== THROTTLED_STATUS) {
    delete chunk.opts.chunkRetryInterval;
    return false;
  }

  chunk.opts.chunkRetryInterval = backoffMs(retryInfo.retryAfter, chunk.retries || 0);
  chunk.opts.maxChunkRetries = THROTTLED_MAX_CHUNK_RETRIES;
  if (resumableFile) resumableFile.isThrottled = true;
  return true;
}

// clearThrottleState is called when a file finishes or fails for good, so a
// stale "server is busy" hint does not outlive the condition.
export function clearThrottleState(resumableFile) {
  if (resumableFile) resumableFile.isThrottled = false;
}
