import {
  BASE_MAX_CHUNK_RETRIES,
  THROTTLED_MAX_CHUNK_RETRIES,
  backoffMs,
  clearThrottleState,
  noteUploadRetry,
  parseRetryAfterMs,
} from '../upload-throttle-backoff';

// The defect this guards: resumable.js retries a 429 immediately (its
// chunkRetryInterval is unset) and counts those attempts against
// maxChunkRetries, so four attempts land within milliseconds against a bucket
// that refills a couple of times a second and the file is reported as
// permanently failed. A server-side limiter meant to slow an upload down would
// instead kill it.

const makeResumable = () => ({ opts: { chunkRetryInterval: undefined, maxChunkRetries: BASE_MAX_CHUNK_RETRIES } });

const makeFile = (chunks) => ({ chunks });

const makeChunk = ({ status, retryAfter = null, retries = 0 }) => ({
  retries,
  xhr: {
    status,
    getResponseHeader: (name) => (name === 'Retry-After' ? retryAfter : null),
  },
});

describe('parseRetryAfterMs', () => {
  it('reads delta-seconds', () => {
    expect(parseRetryAfterMs('5')).toBe(5000);
    expect(parseRetryAfterMs(' 2 ')).toBe(2000);
  });

  // An HTTP-date is deliberately not parsed: a clock skew would turn it into a
  // wildly wrong wait, and it is not a form SesameFS sends.
  it('ignores anything that is not a plain number', () => {
    expect(parseRetryAfterMs('Wed, 21 Oct 2026 07:28:00 GMT')).toBeNull();
    expect(parseRetryAfterMs('')).toBeNull();
    expect(parseRetryAfterMs(null)).toBeNull();
    expect(parseRetryAfterMs('-1')).toBeNull();
  });
});

describe('backoffMs', () => {
  // The two paths jitter differently, so "no jitter added" is a different roll
  // for each: the exponential path spreads symmetrically around its guess (0.5
  // is its midpoint), the Retry-After path only ever adds (0 adds nothing).
  const midExponential = () => 0.5;
  const noAddedJitter = () => 0;

  it('honours the server figure over its own guess', () => {
    expect(backoffMs('5', 0, noAddedJitter)).toBe(5000);
  });

  it('never waits less than a second, even when told zero', () => {
    expect(backoffMs('0', 0, noAddedJitter)).toBe(1000);
    expect(backoffMs(null, 0, midExponential)).toBe(1000);
  });

  it('grows exponentially and then stops growing when the server says nothing', () => {
    expect(backoffMs(null, 1, midExponential)).toBe(2000);
    expect(backoffMs(null, 3, midExponential)).toBe(8000);
    expect(backoffMs(null, 10, midExponential)).toBe(16000);
  });

  // Several chunks of one file are throttled at once. Without jitter they would
  // retry in lockstep and re-collide on the same empty bucket.
  it('spreads concurrent retries apart', () => {
    const low = backoffMs('8', 0, () => 0);
    const high = backoffMs('8', 0, () => 1);
    expect(low).toBeLessThan(high);
    expect(low).toBeGreaterThanOrEqual(1000);
  });

  // Retry-After is a floor, not an estimate. Symmetric jitter would sometimes
  // retry before the bucket the server just described as empty has refilled —
  // at Retry-After: 2 that is a retry at 1.5s, guaranteed to be refused again.
  it('never retries earlier than the server asked', () => {
    for (const roll of [0, 0.25, 0.5, 0.75, 1]) {
      expect(backoffMs('2', 0, () => roll)).toBeGreaterThanOrEqual(2000);
      expect(backoffMs('8', 3, () => roll)).toBeGreaterThanOrEqual(8000);
    }
  });
});

describe('noteUploadRetry', () => {
  it('spaces out the retry and raises the ceiling on a 429', () => {
    const resumable = makeResumable();
    const file = makeFile([makeChunk({ status: 429, retryAfter: '3' })]);

    expect(noteUploadRetry(resumable, file)).toBe(true);
    expect(resumable.opts.chunkRetryInterval).toBeGreaterThanOrEqual(1000);
    expect(resumable.opts.maxChunkRetries).toBe(THROTTLED_MAX_CHUNK_RETRIES);
    expect(file.isThrottled).toBe(true);
  });

  // The regression proper: with the library's own policy, three immediate
  // attempts is all a throttled chunk gets.
  it('leaves room for the bucket to refill', () => {
    const resumable = makeResumable();
    const file = makeFile([makeChunk({ status: 429, retryAfter: '5' })]);

    noteUploadRetry(resumable, file);

    const totalWait = resumable.opts.chunkRetryInterval * resumable.opts.maxChunkRetries;
    expect(totalWait).toBeGreaterThan(5000);
  });

  // A network blip must not inherit multi-second waits from an earlier
  // throttled stretch; undefined is the library's "retry immediately".
  it('restores the retry interval immediately on a retry that is not throttling', () => {
    const resumable = makeResumable();
    let clock = 1_000_000;
    const now = () => clock;
    noteUploadRetry(resumable, makeFile([makeChunk({ status: 429, retryAfter: '5' })]), now);

    const file = makeFile([makeChunk({ status: 0 })]);
    expect(noteUploadRetry(resumable, file, now)).toBe(false);
    expect(resumable.opts.chunkRetryInterval).toBeUndefined();
    expect(file.isThrottled).toBe(false);
  });

  // The regression for the shared-options hazard. The options live on the
  // resumable instance, but the events that set them are per-chunk: one chunk
  // holding sustained 429s while another hits a network error. resumable.js
  // reads maxChunkRetries in status() BEFORE emitting the retry event, so if the
  // network error lowers the ceiling, the throttled chunk — which has already
  // spent the baseline attempts — is failed outright and this module never gets
  // an event to raise it back.
  it('keeps the raised ceiling while a throttled retry is still pending', () => {
    const resumable = makeResumable();
    let clock = 1_000_000;
    const now = () => clock;

    // Chunk A is throttled and has already burned the baseline attempts.
    const throttledFile = makeFile([makeChunk({ status: 429, retryAfter: '5', retries: 3 })]);
    expect(noteUploadRetry(resumable, throttledFile, now)).toBe(true);
    expect(resumable.opts.maxChunkRetries).toBe(THROTTLED_MAX_CHUNK_RETRIES);

    // Chunk B, on an unrelated file, fails with a network error a moment later.
    clock += 100;
    const networkFile = makeFile([makeChunk({ status: 0 })]);
    expect(noteUploadRetry(resumable, networkFile, now)).toBe(false);

    expect(resumable.opts.maxChunkRetries).toBe(THROTTLED_MAX_CHUNK_RETRIES);
    // The interval is still restored, so the network retry is not slowed down.
    expect(resumable.opts.chunkRetryInterval).toBeUndefined();
  });

  it('restores the ceiling once the throttled retry can no longer be pending', () => {
    const resumable = makeResumable();
    let clock = 1_000_000;
    const now = () => clock;

    noteUploadRetry(resumable, makeFile([makeChunk({ status: 429, retryAfter: '5' })]), now);

    // Well past the scheduled wait (5s plus jitter) and the grace covering its
    // round trip. Deliberately not the exact boundary: the jitter makes the wait
    // itself non-deterministic, and a test that straddles it would be flaky.
    clock += 60000;
    noteUploadRetry(resumable, makeFile([makeChunk({ status: 0 })]), now);

    expect(resumable.opts.maxChunkRetries).toBe(BASE_MAX_CHUNK_RETRIES);
  });

  // Two uploaders can exist on one page; neither may inherit the other's window.
  it('keeps the sticky window per resumable instance', () => {
    const throttledInstance = makeResumable();
    const otherInstance = makeResumable();
    let clock = 1_000_000;
    const now = () => clock;

    noteUploadRetry(throttledInstance, makeFile([makeChunk({ status: 429, retryAfter: '5' })]), now);
    noteUploadRetry(otherInstance, makeFile([makeChunk({ status: 0 })]), now);

    expect(throttledInstance.opts.maxChunkRetries).toBe(THROTTLED_MAX_CHUNK_RETRIES);
    expect(otherInstance.opts.maxChunkRetries).toBe(BASE_MAX_CHUNK_RETRIES);
  });

  it('falls back to exponential backoff when the server sends no Retry-After', () => {
    const resumable = makeResumable();
    const file = makeFile([makeChunk({ status: 429, retryAfter: null, retries: 2 })]);

    expect(noteUploadRetry(resumable, file)).toBe(true);
    expect(resumable.opts.chunkRetryInterval).toBeGreaterThanOrEqual(1000);
  });

  // Some browsers throw reading a header off an aborted xhr. That must degrade
  // to the exponential path, not take the upload down with it.
  it('survives a header read that throws', () => {
    const resumable = makeResumable();
    const file = makeFile([{
      retries: 0,
      xhr: {
        status: 429,
        getResponseHeader: () => { throw new Error('InvalidStateError'); },
      },
    }]);

    expect(noteUploadRetry(resumable, file)).toBe(true);
    expect(resumable.opts.chunkRetryInterval).toBeGreaterThanOrEqual(1000);
  });

  it('is inert without a resumable instance', () => {
    expect(noteUploadRetry(null, makeFile([makeChunk({ status: 429 })]))).toBe(false);
    expect(noteUploadRetry({}, makeFile([makeChunk({ status: 429 })]))).toBe(false);
  });

  it('handles a file with no chunks', () => {
    const resumable = makeResumable();
    expect(noteUploadRetry(resumable, makeFile(undefined))).toBe(false);
    expect(noteUploadRetry(resumable, undefined)).toBe(false);
  });
});

describe('clearThrottleState', () => {
  it('drops the hint so it does not outlive the condition', () => {
    const file = { isThrottled: true };
    clearThrottleState(file);
    expect(file.isThrottled).toBe(false);
    expect(() => clearThrottleState(undefined)).not.toThrow();
  });
});
