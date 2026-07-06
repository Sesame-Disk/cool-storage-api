import { createUploadThroughputMeter } from '../upload-throughput-meter';
// A controllable clock so the sliding window is deterministic (no real timers).
const makeClock = (start = 0) => {
  let t = start;
  return { now: () => t, advance: (ms) => { t += ms; }, set: (ms) => { t = ms; } };
};
const MB = 1024 * 1024;
const bitsPerSec = (bytes, ms) => (bytes / ms) * 1000 * 8;
describe('createUploadThroughputMeter', () => {
  test('a single 8 MB block with sparse events still reads > 0 while active', () => {
    const clock = makeClock();
    const meter = createUploadThroughputMeter({ windowMs: 3000, now: clock.now });
    meter.addBytes(8 * MB);        // one coarse delta, the whole block at once
    clock.advance(200);            // timer tick, no new bytes yet
    // The old instantaneous calc read ~0 here; the sliding window must not.
    expect(meter.rate()).toBeGreaterThan(0);
  });
  test('steady bursty events average to a stable, non-sawtooth rate', () => {
    const clock = makeClock();
    const meter = createUploadThroughputMeter({ windowMs: 3000, now: clock.now });
    // 8 MB every 500 ms = 16 MB/s sustained, but delivered as coarse bursts.
    for (let i = 0; i < 12; i += 1) {
      meter.addBytes(8 * MB);
      clock.advance(500);
    }
    // Sample the rate at a few ticks between bursts; it should stay in a tight band
    // around the true 16 MB/s, never collapsing to 0 or spiking wildly.
    const target = bitsPerSec(16 * MB, 1000);
    const readings = [];
    for (let i = 0; i < 4; i += 1) {
      readings.push(meter.rate());
      clock.advance(120); // between-burst ticks, no new bytes
    }
    readings.forEach((r) => {
      expect(r).toBeGreaterThan(target * 0.6);
      expect(r).toBeLessThan(target * 1.6);
    });
  });
  test('decays to 0 after a full window of inactivity', () => {
    const clock = makeClock();
    const meter = createUploadThroughputMeter({ windowMs: 3000, now: clock.now });
    meter.addBytes(8 * MB);
    clock.advance(1000);
    expect(meter.rate()).toBeGreaterThan(0); // still within the window
    clock.advance(3000); // 4s total since last byte -> past the window
    expect(meter.rate()).toBe(0);
  });
  test('a new burst after an idle gap longer than the window ignores the pre-gap bytes', () => {
    const clock = makeClock();
    const meter = createUploadThroughputMeter({ windowMs: 3000, bucketMs: 250, now: clock.now });
    // A big transfer, then a long stall well past the window.
    meter.addBytes(100 * MB);
    clock.advance(10000); // 10s idle -> fully decayed
    expect(meter.rate()).toBe(0);
    // A fresh, small burst arrives. Its rate must reflect ONLY the new bytes over the
    // new span, not the 100 MB baseline that expired 10s ago (the stale-baseline bug).
    meter.addBytes(MB);
    clock.advance(500);
    meter.addBytes(MB);
    const r = meter.rate();
    // ~2 MB over ~500ms of real history, NOT (100 MB + 2 MB) mis-spread over the window.
    expect(r).toBeGreaterThan(0);
    expect(r).toBeLessThan(bitsPerSec(2 * MB, 500) * 1.5);
    // The adaptive reading is gated to EXACTLY 0: only ~500ms of history exists after
    // the gap, below the 1s warm-up floor, so no inflated spike can feed the ramp.
    expect(meter.rate(clock.now(), { minSpanMs: 1000 })).toBe(0);
  });
  test('irregular event cadence (jittery progress / throttled timers) still tracks the true rate', () => {
    const clock = makeClock();
    const meter = createUploadThroughputMeter({ windowMs: 3000, bucketMs: 250, now: clock.now });
    // Sustained ~8 MB/s delivered with irregular gaps between progress events and
    // irregular rate() reads, as a backgrounded/throttled tab (or a very fast localhost
    // firing sparse, bursty events) would produce.
    const gaps = [10, 400, 30, 900, 5, 600, 50, 700, 20, 500]; // ms
    gaps.forEach((gap) => {
      meter.addBytes(Math.round(8 * MB * (gap / 1000))); // bytes that 8 MB/s moves in gap
      clock.advance(gap);
      // Never collapses to 0 mid-transfer despite the jitter (the original-bug symptom).
      expect(meter.rate()).toBeGreaterThan(0);
    });
    const target = bitsPerSec(8 * MB, 1000);
    const r = meter.rate();
    expect(r).toBeGreaterThan(target * 0.5);
    expect(r).toBeLessThan(target * 1.7);
    // Bucketing keeps the buffer bounded no matter how irregular the cadence was.
    expect(meter.sampleCount()).toBeLessThanOrEqual(3000 / 250 + 2);
  });
  test('ignores non-positive deltas so a progress reset makes no negative/fake rate', () => {
    const clock = makeClock();
    const meter = createUploadThroughputMeter({ windowMs: 3000, now: clock.now });
    meter.addBytes(4 * MB);
    meter.addBytes(-2 * MB); // a retry/reset delta - must be ignored
    meter.addBytes(0);
    clock.advance(500);
    const r = meter.rate();
    expect(r).toBeGreaterThanOrEqual(0);
    // Only the 4 MB counted: rate ~= 4MB over the 500ms span, not inflated/negative.
    expect(r).toBeCloseTo(bitsPerSec(4 * MB, 500), 0);
  });
  test('rate is 0 before any bytes and after reset', () => {
    const clock = makeClock();
    const meter = createUploadThroughputMeter({ windowMs: 3000, now: clock.now });
    expect(meter.rate()).toBe(0);
    meter.addBytes(MB);
    clock.advance(100);
    expect(meter.rate()).toBeGreaterThan(0);
    meter.reset();
    expect(meter.rate()).toBe(0);
  });
  test('retained sample count stays bounded under a verbose event stream (time bucketing)', () => {
    const clock = makeClock();
    const meter = createUploadThroughputMeter({ windowMs: 3000, bucketMs: 250, now: clock.now });
    // 3000 events across 3s (one every ~1ms) must NOT grow one sample per event.
    for (let i = 0; i < 3000; i += 1) {
      meter.addBytes(1024);
      clock.advance(1);
    }
    // Bounded to ~windowMs/bucketMs (12) + a small constant, never the event count.
    expect(meter.sampleCount()).toBeLessThanOrEqual(3000 / 250 + 2);
    expect(meter.rate()).toBeGreaterThan(0);
  });
  test('minSpanMs holds the adaptive reading at 0 until the window is mature, without hiding it from the UI', () => {
    const clock = makeClock();
    const meter = createUploadThroughputMeter({ windowMs: 3000, now: clock.now });
    meter.addBytes(8 * MB);
    clock.advance(300); // only 300ms of history
    // UI (minSpanMs 0) shows a value early...
    expect(meter.rate()).toBeGreaterThan(0);
    // ...but an adaptive consumer requiring a 1s-mature window sees 0 (no warm-up spike).
    expect(meter.rate(clock.now(), { minSpanMs: 1000 })).toBe(0);
    meter.addBytes(8 * MB);
    clock.advance(900); // now ~1.2s of history
    expect(meter.rate(clock.now(), { minSpanMs: 1000 })).toBeGreaterThan(0);
  });
  test('minSpanMs is measured from the first real byte time, not the start of its bucket', () => {
    const clock = makeClock(249);
    const meter = createUploadThroughputMeter({ windowMs: 3000, bucketMs: 250, now: clock.now });
    meter.addBytes(8 * MB); // lands at the very end of bucket [0, 250)
    clock.set(1000);
    // Real history is only 751ms, so the adaptive reading must still be gated off.
    expect(meter.rate(clock.now(), { minSpanMs: 1000 })).toBe(0);
    clock.set(1249);
    expect(meter.rate(clock.now(), { minSpanMs: 1000 })).toBeGreaterThan(0);
  });
  test('inactivity decay is measured from the last real byte time, not the end of its bucket', () => {
    const clock = makeClock(1);
    const meter = createUploadThroughputMeter({ windowMs: 3000, bucketMs: 250, now: clock.now });
    meter.addBytes(8 * MB); // lands at the very start of bucket [0, 250)
    clock.set(3000);
    expect(meter.rate()).toBeGreaterThan(0);
    clock.set(3001);
    expect(meter.rate()).toBe(0);
  });
  test('averages over the window: a burst is spread across the horizon, not divided by a near-zero span', () => {
    const clock = makeClock();
    const meter = createUploadThroughputMeter({ windowMs: 2000, now: clock.now });
    // Warm up so we have a full window of history.
    meter.addBytes(MB); clock.advance(1000);
    meter.addBytes(MB); clock.advance(1000);
    // A fresh 10 MB burst arrives; the reading right after must reflect the windowed
    // average, not 10MB / (tiny span).
    meter.addBytes(10 * MB);
    const r = meter.rate();
    // bytes in the trailing 2s window ~= 1MB + 10MB (the first 1MB fell out), over 2s.
    expect(r).toBeLessThan(bitsPerSec(12 * MB, 2000) * 1.2);
    expect(r).toBeGreaterThan(0);
  });
});
