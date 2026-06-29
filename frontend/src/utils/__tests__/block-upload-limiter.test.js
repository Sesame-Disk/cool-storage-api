import { createBlockLimiter } from '../block-upload-limiter';

const flush = () => new Promise((resolve) => setTimeout(resolve, 0));

// Feed high-bitrate samples until the adaptive ceiling reaches `target`. A tiny
// blockSize (1 byte) is used so the minimum-bitrate gate never blocks ramps.
const rampTo = (limiter, target) => {
  for (let i = 0; i < 100 && limiter.getEffective() < target; i += 1) {
    limiter.noteBitrate(1000000);
  }
};

describe('createBlockLimiter (adaptive global ceiling)', () => {
  test('never lets more than the live ceiling acquire at once; release wakes the next waiter FIFO', async () => {
    const limiter = createBlockLimiter({ maxConcurrency: 2, blockSize: 1 });
    rampTo(limiter, 2); // climb from the conservative start (1) to the ceiling
    expect(limiter.getEffective()).toBe(2);
    const r1 = await limiter.acquire({});
    const r2 = await limiter.acquire({});
    expect(limiter.getInFlight()).toBe(2);

    let third = false;
    let fourth = false;
    const p3 = limiter.acquire({}).then((rel) => { third = true; return rel; });
    const p4 = limiter.acquire({}).then((rel) => { fourth = true; return rel; });
    await flush();
    // Both are blocked while the 2 slots are held.
    expect(third).toBe(false);
    expect(fourth).toBe(false);
    expect(limiter.getWaiterCount()).toBe(2);

    r1(); // free one slot → p3 (FIFO front) wakes, p4 still waits
    const r3 = await p3;
    expect(third).toBe(true);
    expect(fourth).toBe(false);
    expect(limiter.getInFlight()).toBe(2);

    r2();
    const r4 = await p4;
    expect(fourth).toBe(true);

    r3();
    r4();
    expect(limiter.getInFlight()).toBe(0);
  });

  test('acquire with an already-aborted signal rejects without taking a slot', async () => {
    const limiter = createBlockLimiter({ maxConcurrency: 2 });
    const controller = new AbortController();
    controller.abort();
    await expect(limiter.acquire({ signal: controller.signal })).rejects.toHaveProperty('name', 'AbortError');
    expect(limiter.getInFlight()).toBe(0);
  });

  test('a waiter whose signal aborts is removed from the queue and never acquires', async () => {
    const limiter = createBlockLimiter({ maxConcurrency: 1 });
    const held = await limiter.acquire({}); // fills the only slot
    const controller = new AbortController();
    let acquired = false;
    const p = limiter.acquire({ signal: controller.signal });
    p.then(() => { acquired = true; }).catch(() => {});
    await flush();
    expect(limiter.getWaiterCount()).toBe(1);

    controller.abort();
    await expect(p).rejects.toHaveProperty('name', 'AbortError');
    expect(limiter.getWaiterCount()).toBe(0);

    held(); // releasing must NOT resurrect the aborted waiter
    await flush();
    expect(acquired).toBe(false);
    expect(limiter.getInFlight()).toBe(0);
  });

  test('release is idempotent — a double release does not over-free slots', async () => {
    const limiter = createBlockLimiter({ maxConcurrency: 1 });
    const rel = await limiter.acquire({});
    rel();
    rel();
    expect(limiter.getInFlight()).toBe(0);
    const rel2 = await limiter.acquire({});
    expect(limiter.getInFlight()).toBe(1);
    rel2();
  });

  test('ceiling comes from maxConcurrency and is clamped to >= 1', () => {
    expect(createBlockLimiter({ maxConcurrency: 3 }).getMaxConcurrency()).toBe(3);
    expect(createBlockLimiter({ maxConcurrency: 0 }).getMaxConcurrency()).toBe(1);
    expect(createBlockLimiter({}).getMaxConcurrency()).toBe(1);
  });

  test('reset rejects queued waiters and returns to the conservative start (1)', async () => {
    const limiter = createBlockLimiter({ maxConcurrency: 2, blockSize: 1 });
    rampTo(limiter, 2);
    expect(limiter.getEffective()).toBe(2);
    const held1 = await limiter.acquire({}); // fill both slots
    const held2 = await limiter.acquire({});
    const p = limiter.acquire({}); // queues behind the 2 held slots
    await flush();
    expect(limiter.getWaiterCount()).toBe(1);

    limiter.reset();
    await expect(p).rejects.toHaveProperty('name', 'AbortError');
    expect(limiter.getWaiterCount()).toBe(0);
    expect(limiter.getEffective()).toBe(1); // back to the conservative start
    held1();
    held2();
  });
});

describe('createBlockLimiter adaptive ramp', () => {
  test('starts at 1 and climbs one step per run of stable samples up to max', () => {
    // Use tiny blockSize so the minimum-bitrate gate never blocks.
    const limiter = createBlockLimiter({ maxConcurrency: 3, blockSize: 1 });
    expect(limiter.getEffective()).toBe(1);

    // 3 stable samples -> one ramp step (1->2).
    limiter.noteBitrate(1000000);
    limiter.noteBitrate(1000000);
    expect(limiter.getEffective()).toBe(1);
    limiter.noteBitrate(1000000);
    expect(limiter.getEffective()).toBe(2);

    // SECOND ramp needs 5 stable samples (NEXT_RAMP_SAMPLES) and a 5% gain.
    // Provide a higher bitrate to satisfy the gain check.
    limiter.noteBitrate(2000000); // stableSamples=1
    limiter.noteBitrate(2000000); // stableSamples=2
    limiter.noteBitrate(2000000); // stableSamples=3
    limiter.noteBitrate(2000000); // stableSamples=4
    limiter.noteBitrate(2000000); // stableSamples=5 -> ramp to 3
    expect(limiter.getEffective()).toBe(3);

    // Extra healthy samples must NOT push effective past max (3).
    limiter.noteBitrate(2000000);
    limiter.noteBitrate(2000000);
    limiter.noteBitrate(2000000);
    expect(limiter.getEffective()).toBe(3); // never above the configured ceiling
  });

  test('drops to 1 only after two consecutive sharp bitrate collapses', () => {
    const limiter = createBlockLimiter({ maxConcurrency: 3, blockSize: 1 });
    rampTo(limiter, 2);
    expect(limiter.getEffective()).toBe(2);

    // DROP_RATIO = 0.55: one low sample is treated as noise on a healthy LAN;
    // the second consecutive drop confirms the collapse and degrades.
    limiter.noteBitrate(1000000 * 0.5);
    expect(limiter.getEffective()).toBe(2);

    limiter.noteBitrate(1000000 * 0.5);
    expect(limiter.getEffective()).toBe(1);
  });

  test('resets stable samples on instability (bitrate below floor)', () => {
    const limiter = createBlockLimiter({ maxConcurrency: 3, blockSize: 1 });
    // Two stable samples.
    limiter.noteBitrate(1000000);
    limiter.noteBitrate(1000000);
    // STABLE_FLOOR_RATIO = 0.7: bitrate between 55% and 70% of smoothed is
    // "unstable" -- resets stableSamples but does NOT degrade.
    limiter.noteBitrate(1000000 * 0.6);
    expect(limiter.getEffective()).toBe(1);

    // Must re-accumulate samples from 0 after instability.
    limiter.noteBitrate(1000000);
    limiter.noteBitrate(1000000);
    limiter.noteBitrate(1000000); // 3rd stable sample -> ramp
    expect(limiter.getEffective()).toBe(2);
  });

  test('minimum bitrate gate blocks ramp when throughput is too low', () => {
    // Default blockSize = 8 MB -> minBitrate for 2 slots ~= 11.2 Mbps.
    // With a 1 Mbps sample, the ramp must be blocked.
    const limiter = createBlockLimiter({ maxConcurrency: 3 });
    limiter.noteBitrate(1000000); // seed (1 Mbps)
    limiter.noteBitrate(1000000);
    limiter.noteBitrate(1000000); // 3 stable samples but below min bitrate
    expect(limiter.getEffective()).toBe(1); // gate blocks the ramp
  });

  test('gain check blocks ramp to 3 without throughput improvement', () => {
    const limiter = createBlockLimiter({ maxConcurrency: 3, blockSize: 1 });
    rampTo(limiter, 2); // effective=2, lastRampBitrate ~= 1 Mbps
    expect(limiter.getEffective()).toBe(2);

    // 5 stable samples at the same bitrate -> gain check blocks: smoothed 1 Mbps
    // < lastRampBitrate (1 Mbps) * 1.05 = 1.05 Mbps
    limiter.noteBitrate(1000000);
    limiter.noteBitrate(1000000);
    limiter.noteBitrate(1000000);
    limiter.noteBitrate(1000000);
    limiter.noteBitrate(1000000);
    expect(limiter.getEffective()).toBe(2); // gain check blocked
  });

  test('gain check passes when throughput improves >5%', () => {
    const limiter = createBlockLimiter({ maxConcurrency: 3, blockSize: 1 });
    rampTo(limiter, 2); // effective=2, lastRampBitrate ~= 1 Mbps

    // 5 stable samples at >5% higher bitrate -> gain check passes.
    limiter.noteBitrate(1100000); // 10% above lastRampBitrate
    limiter.noteBitrate(1100000);
    limiter.noteBitrate(1100000);
    limiter.noteBitrate(1100000);
    limiter.noteBitrate(1100000); // 5th -> ramp
    expect(limiter.getEffective()).toBe(3);
  });

  test('degrade is unconditional; cooldown gates the ramp (matches resumable behavior)', () => {
    const limiter = createBlockLimiter({ maxConcurrency: 3, blockSize: 1 });
    rampTo(limiter, 2);
    expect(limiter.getEffective()).toBe(2);

    limiter.noteFailure(); // degrades to 1, starts 10 s cooldown
    expect(limiter.getEffective()).toBe(1);

    // Re-seed smoothedBitrate so we can test that degrade fires unconditionally.
    limiter.noteBitrate(1000000);

    // A sharp drop below DROP_RATIO -- cooldown does NOT prevent the degrade.
    limiter.noteBitrate(1);
    expect(limiter.getEffective()).toBe(1); // still 1 (was already 1 after failure)

    // Cooldown DOES block ramp: healthy samples are ignored during cooldown.
    limiter.noteBitrate(1000000);
    limiter.noteBitrate(1000000);
    limiter.noteBitrate(1000000);
    expect(limiter.getEffective()).toBe(1); // ramp blocked by cooldown
  });

  test('noteFailure and noteRetry drop to 1 immediately with cooldown', () => {
    const a = createBlockLimiter({ maxConcurrency: 3, blockSize: 1 });
    rampTo(a, 2);
    a.noteFailure();
    expect(a.getEffective()).toBe(1);

    const b = createBlockLimiter({ maxConcurrency: 3, blockSize: 1 });
    rampTo(b, 2);
    b.noteRetry();
    expect(b.getEffective()).toBe(1);
  });

  test('ignores zero/idle samples (pure-hashing phase neither ramps nor degrades)', () => {
    const limiter = createBlockLimiter({ maxConcurrency: 3, blockSize: 1 });
    rampTo(limiter, 2);
    expect(limiter.getEffective()).toBe(2);
    limiter.noteBitrate(0);
    limiter.noteBitrate(0);
    limiter.noteBitrate(0);
    expect(limiter.getEffective()).toBe(2); // unchanged
  });
});
