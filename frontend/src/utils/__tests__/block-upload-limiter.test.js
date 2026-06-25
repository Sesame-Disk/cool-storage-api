import { createBlockLimiter } from '../block-upload-limiter';

const flush = () => new Promise((resolve) => setTimeout(resolve, 0));

// Feed healthy throughput samples until the adaptive ceiling reaches `target`.
const rampTo = (limiter, target) => {
  for (let i = 0; i < 100 && limiter.getEffective() < target; i += 1) {
    limiter.noteBitrate(1000000);
  }
};

describe('createBlockLimiter (adaptive global ceiling)', () => {
  test('never lets more than the live ceiling acquire at once; release wakes the next waiter FIFO', async () => {
    const limiter = createBlockLimiter({ maxConcurrency: 2 });
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
    const limiter = createBlockLimiter({ maxConcurrency: 2 });
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
  test('starts at 1 and climbs one step per run of healthy samples up to max', () => {
    const limiter = createBlockLimiter({ maxConcurrency: 3 });
    expect(limiter.getEffective()).toBe(1);

    // 3 healthy samples ⇒ one ramp step (1→2).
    limiter.noteBitrate(1000000);
    limiter.noteBitrate(1000000);
    expect(limiter.getEffective()).toBe(1);
    limiter.noteBitrate(1000000);
    expect(limiter.getEffective()).toBe(2);

    // Another run ⇒ 2→3, then it caps at max.
    limiter.noteBitrate(1000000);
    limiter.noteBitrate(1000000);
    limiter.noteBitrate(1000000);
    expect(limiter.getEffective()).toBe(3);
    rampTo(limiter, 4);
    expect(limiter.getEffective()).toBe(3); // never above the configured ceiling
  });

  test('drops to 1 on a sustained bitrate collapse', () => {
    const limiter = createBlockLimiter({ maxConcurrency: 3 });
    rampTo(limiter, 3);
    expect(limiter.getEffective()).toBe(3);

    // A single dip is tolerated (jitter); a sustained collapse drops to 1.
    limiter.noteBitrate(100000); // < 60% of the ~1e6 baseline
    expect(limiter.getEffective()).toBe(3);
    limiter.noteBitrate(100000);
    expect(limiter.getEffective()).toBe(1);
  });

  test('noteFailure and noteRetry drop to 1 immediately', () => {
    const a = createBlockLimiter({ maxConcurrency: 3 });
    rampTo(a, 3);
    a.noteFailure();
    expect(a.getEffective()).toBe(1);

    const b = createBlockLimiter({ maxConcurrency: 3 });
    rampTo(b, 3);
    b.noteRetry();
    expect(b.getEffective()).toBe(1);
  });

  test('ignores zero/idle samples (pure-hashing phase neither ramps nor degrades)', () => {
    const limiter = createBlockLimiter({ maxConcurrency: 3 });
    rampTo(limiter, 2);
    expect(limiter.getEffective()).toBe(2);
    limiter.noteBitrate(0);
    limiter.noteBitrate(0);
    limiter.noteBitrate(0);
    expect(limiter.getEffective()).toBe(2); // unchanged
  });
});
