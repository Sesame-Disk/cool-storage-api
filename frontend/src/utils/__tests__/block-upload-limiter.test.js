import { createBlockLimiter } from '../block-upload-limiter';

const flush = () => new Promise((resolve) => setTimeout(resolve, 0));

describe('createBlockLimiter (static global ceiling)', () => {
  test('never lets more than max acquire at once; release wakes the next waiter FIFO', async () => {
    const limiter = createBlockLimiter({ maxConcurrency: 2 });
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

  test('reset rejects queued waiters and restores the ceiling', async () => {
    const limiter = createBlockLimiter({ maxConcurrency: 1 });
    const held = await limiter.acquire({});
    const p = limiter.acquire({});
    await flush();
    expect(limiter.getWaiterCount()).toBe(1);

    limiter.reset();
    await expect(p).rejects.toHaveProperty('name', 'AbortError');
    expect(limiter.getWaiterCount()).toBe(0);
    held();
  });
});
