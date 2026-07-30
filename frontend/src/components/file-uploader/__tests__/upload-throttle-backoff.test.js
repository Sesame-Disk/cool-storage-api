import {
  BASE_MAX_CHUNK_RETRIES,
  THROTTLED_MAX_CHUNK_RETRIES,
  backoffMs,
  clearChunkThrottleState,
  clearThrottleState,
  noteUploadRetry,
  parseRetryAfterMs,
} from '../upload-throttle-backoff';
import fs from 'fs';
import Resumablejs from '@seafile/resumablejs';

// The defect this guards: resumable.js retries a 429 immediately (its
// chunkRetryInterval is unset) and counts those attempts against
// maxChunkRetries, so four attempts land within milliseconds against a bucket
// that refills a couple of times a second and the file is reported as
// permanently failed. A server-side limiter meant to slow an upload down would
// instead kill it.

const makeResumable = () => ({ opts: { chunkRetryInterval: undefined, maxChunkRetries: BASE_MAX_CHUNK_RETRIES } });

const makeFile = (chunks) => ({ chunks });

const makeChunk = ({ retries = 0 }) => ({
  retries,
  opts: {},
});

const makeRetryInfo = (status, retryAfter = null) => ({ status, retryAfter });

describe('resumable.js retry contract', () => {
  const OriginalXMLHttpRequest = global.XMLHttpRequest;

  it('captures response metadata before abort and makes delayed retries cancelable', () => {
    const source = fs.readFileSync(require.resolve('@seafile/resumablejs'), 'utf8');
    expect(source).toContain("$.resumableObj.fire('fileRetry', $, chunk, retryInfo)");
    expect(source).toContain("$.resumableObj.fire('fileChunkSuccess', $, chunk)");
    expect(source).toContain("retryInfo.retryAfter = $.xhr ? $.xhr.getResponseHeader('Retry-After') : null");
    expect(source).toContain("$.getOpt('throttledMaxChunkRetries')");
    expect(source).toContain('throttledMaxChunkRetries:30');
    expect(THROTTLED_MAX_CHUNK_RETRIES).toBe(30);
    expect(source).toContain('$.retryTimer = setTimeout(function()');
    expect(source).toContain('clearTimeout($.retryTimer)');
    // The patch rewrites a file it may have already rewritten, so "the new code
    // is present" is not enough: a superseded copy left above it would run
    // first, and it reads the response header without the guard below.
    expect(source.match(/var retryInfo/g)).toHaveLength(1);
  });

  class ControlledXHR {
    static instances = [];

    constructor() {
      this.listeners = {};
      this.upload = { addEventListener: jest.fn() };
      this.readyState = 0;
      this.status = 0;
      this.responseText = '';
      this.retryAfter = null;
      ControlledXHR.instances.push(this);
    }

    addEventListener = (event, listener) => {
      this.listeners[event] = listener;
    };

    open = () => {
      this.readyState = 1;
    };

    setRequestHeader = jest.fn();

    send = jest.fn();

    abort = jest.fn();

    getResponseHeader = (name) => (name === 'Retry-After' ? this.retryAfter : null);

    respond(status, retryAfter = null) {
      this.status = status;
      this.retryAfter = retryAfter;
      this.readyState = 4;
      this.listeners.load({ target: this });
    }
  }

  const startUpload = () => {
    const resumable = new Resumablejs({
      target: '/upload',
      chunkSize: 4,
      forceChunkSize: true,
      simultaneousUploads: 1,
      maxChunkRetries: BASE_MAX_CHUNK_RETRIES,
      throttledMaxChunkRetries: THROTTLED_MAX_CHUNK_RETRIES,
      testChunks: false,
    });
    const retries = [];
    const errors = [];
    const successes = [];
    resumable.on('fileRetry', (file, chunk, retryInfo) => {
      retries.push({ file, chunk, retryInfo });
      noteUploadRetry(resumable, file, chunk, retryInfo);
    });
    resumable.on('fileChunkSuccess', (file, chunk) => clearChunkThrottleState(file, chunk));
    resumable.on('fileError', (...args) => errors.push(args));
    resumable.on('fileSuccess', (...args) => successes.push(args));
    resumable.on('fileAdded', () => resumable.upload());
    resumable.addFile(new File(['data'], 'data.bin', { type: 'application/octet-stream' }));
    jest.runOnlyPendingTimers();
    return { resumable, retries, errors, successes };
  };

  beforeEach(() => {
    jest.useFakeTimers();
    jest.spyOn(Math, 'random').mockReturnValue(0);
    ControlledXHR.instances = [];
    global.XMLHttpRequest = ControlledXHR;
  });

  afterEach(() => {
    global.XMLHttpRequest = OriginalXMLHttpRequest;
    jest.useRealTimers();
    jest.restoreAllMocks();
  });

  it('delays a real 429 retry until Retry-After has elapsed', () => {
    const { resumable, retries } = startUpload();
    expect(ControlledXHR.instances).toHaveLength(1);
    expect(ControlledXHR.instances[0].send).toHaveBeenCalledTimes(1);

    ControlledXHR.instances[0].respond(429, '2');

    expect(retries).toHaveLength(1);
    expect(retries[0].chunk).toBeDefined();
    expect(retries[0].retryInfo).toEqual({ status: 429, retryAfter: '2' });
    expect(ControlledXHR.instances).toHaveLength(1);

    jest.advanceTimersByTime(1999);
    expect(ControlledXHR.instances).toHaveLength(1);
    jest.advanceTimersByTime(1);
    expect(ControlledXHR.instances).toHaveLength(2);
    expect(ControlledXHR.instances[1].send).toHaveBeenCalledTimes(1);
    ControlledXHR.instances[1].respond(200);
    expect(resumable.files[0].isThrottled).toBe(false);
  });

  it('keeps a fourth 429 retryable instead of emitting fileError', () => {
    const { resumable, retries, errors } = startUpload();
    resumable.files[0].chunks[0].retries = BASE_MAX_CHUNK_RETRIES;

    ControlledXHR.instances[0].respond(429, '2');

    expect(retries).toHaveLength(1);
    expect(errors).toHaveLength(0);
    expect(resumable.files[0].chunks[0].opts.maxChunkRetries).toBe(THROTTLED_MAX_CHUNK_RETRIES);
  });

  it('survives sustained 429 saturation and continues after the server recovers', () => {
    const { resumable, retries, errors, successes } = startUpload();
    const file = resumable.files[0];

    for (let attempt = 0; attempt < 13; attempt++) {
      ControlledXHR.instances[attempt].respond(429, '1');
      expect(errors).toHaveLength(0);
      expect(file.isThrottled).toBe(true);

      jest.advanceTimersByTime(file.chunks[0].opts.chunkRetryInterval);
      expect(ControlledXHR.instances).toHaveLength(attempt + 2);
    }

    expect(retries).toHaveLength(13);
    expect(file.chunks[0].retries).toBe(13);
    ControlledXHR.instances[13].respond(200);

    expect(errors).toHaveLength(0);
    expect(successes).toHaveLength(1);
    expect(file.isThrottled).toBe(false);
    expect(file.isComplete()).toBe(true);
  });

  it('cancels a delayed retry when the file is cancelled', () => {
    const { resumable } = startUpload();
    ControlledXHR.instances[0].respond(429, '2');
    expect(ControlledXHR.instances).toHaveLength(1);

    resumable.files[0].cancel();
    jest.advanceTimersByTime(10000);

    expect(ControlledXHR.instances).toHaveLength(1);
  });
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

  it('uses the server figure when it is higher than its own guess', () => {
    expect(backoffMs('5', 0, noAddedJitter)).toBe(5000);
  });

  it('keeps exponential growth when the server floor is lower', () => {
    expect(backoffMs('1', 0, noAddedJitter)).toBe(1000);
    expect(backoffMs('1', 1, noAddedJitter)).toBe(2000);
    expect(backoffMs('1', 4, noAddedJitter)).toBe(16000);
    expect(backoffMs('1', 13, noAddedJitter)).toBe(16000);
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
    const chunk = makeChunk({ retries: 0 });
    const file = makeFile([chunk]);

    expect(noteUploadRetry(resumable, file, chunk, makeRetryInfo(429, '3'))).toBe(true);
    expect(chunk.opts.chunkRetryInterval).toBeGreaterThanOrEqual(3000);
    expect(chunk.opts.maxChunkRetries).toBe(THROTTLED_MAX_CHUNK_RETRIES);
    expect(file.isThrottled).toBe(true);
  });

  // The regression proper: with the library's own policy, three immediate
  // attempts is all a throttled chunk gets.
  it('leaves room for the bucket to refill', () => {
    const resumable = makeResumable();
    const chunk = makeChunk({ retries: 0 });
    const file = makeFile([chunk]);

    noteUploadRetry(resumable, file, chunk, makeRetryInfo(429, '5'));

    const totalWait = chunk.opts.chunkRetryInterval * chunk.opts.maxChunkRetries;
    expect(totalWait).toBeGreaterThan(5000);
  });

  it('keeps retry policy isolated between concurrent chunks', () => {
    const resumable = makeResumable();
    const throttledChunk = makeChunk({ retries: 3 });
    const networkChunk = makeChunk({ retries: 0 });
    const file = makeFile([throttledChunk, networkChunk]);

    expect(noteUploadRetry(resumable, file, throttledChunk, makeRetryInfo(429, '5'))).toBe(true);
    expect(noteUploadRetry(resumable, file, networkChunk, makeRetryInfo(0))).toBe(false);

    expect(throttledChunk.opts.maxChunkRetries).toBe(THROTTLED_MAX_CHUNK_RETRIES);
    expect(throttledChunk.opts.chunkRetryInterval).toBeGreaterThanOrEqual(5000);
    expect(networkChunk.opts.maxChunkRetries).toBeUndefined();
    expect(networkChunk.opts.chunkRetryInterval).toBeUndefined();
    expect(resumable.opts.maxChunkRetries).toBe(BASE_MAX_CHUNK_RETRIES);
    expect(file.isThrottled).toBe(true);
    expect(clearChunkThrottleState(file, throttledChunk)).toBe(true);
    expect(file.isThrottled).toBe(false);
  });

  it('keeps the busy state until every throttled chunk recovers', () => {
    const resumable = makeResumable();
    const first = makeChunk({});
    const second = makeChunk({});
    const file = makeFile([first, second]);

    noteUploadRetry(resumable, file, first, makeRetryInfo(429, '2'));
    noteUploadRetry(resumable, file, second, makeRetryInfo(429, '2'));
    expect(clearChunkThrottleState(file, first)).toBe(false);
    expect(file.isThrottled).toBe(true);
    expect(clearChunkThrottleState(file, second)).toBe(true);
    expect(file.isThrottled).toBe(false);
  });

  it('falls back to exponential backoff when the server sends no Retry-After', () => {
    const resumable = makeResumable();
    const chunk = makeChunk({ retries: 2 });
    const file = makeFile([chunk]);

    expect(noteUploadRetry(resumable, file, chunk, makeRetryInfo(429))).toBe(true);
    expect(chunk.opts.chunkRetryInterval).toBeGreaterThanOrEqual(1000);
  });

  it('uses the response metadata captured before resumable clears the XHR', () => {
    const resumable = makeResumable();
    const chunk = makeChunk({ retries: 0 });
    const file = makeFile([chunk]);

    expect(noteUploadRetry(resumable, file, chunk, makeRetryInfo(429, '7'))).toBe(true);
    expect(chunk.opts.chunkRetryInterval).toBeGreaterThanOrEqual(7000);
  });

  it('is inert without a resumable instance', () => {
    const chunk = makeChunk({});
    expect(noteUploadRetry(null, makeFile([chunk]), chunk, makeRetryInfo(429))).toBe(false);
    expect(noteUploadRetry({}, makeFile([chunk]), chunk, makeRetryInfo(429))).toBe(false);
  });

  it('handles a retry with no triggering chunk', () => {
    const resumable = makeResumable();
    expect(noteUploadRetry(resumable, makeFile(undefined), null, makeRetryInfo(429))).toBe(false);
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
