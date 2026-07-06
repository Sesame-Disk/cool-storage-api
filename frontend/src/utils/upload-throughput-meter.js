// createUploadThroughputMeter - a sliding-window throughput meter fed by REAL wire
// bytes, replacing the old per-event instantaneous delta (getBitrate /
// sampleBlockUploadBitrate) that recomputed a bits/s figure over a single fixed
// ~500ms window ONLY on progress events, and so collapsed to ~0 B/s whenever a single
// transfer was in flight (sparse/bursty events on fast localhost). See
// docs/WEB-BLOCK-UPLOAD.md item 6.
//
// Design:
//   - Bytes are bucketed by time (default 250ms) so the retained sample count is
//     HARD-bounded to ~windowMs/bucketMs regardless of how verbose onUploadProgress
//     is - no per-event array growth / shift() churn. Each bucket still records the
//     REAL first/last byte timestamps inside it, so warm-up / inactivity decisions do
//     not inherit an artificial +/- bucketMs skew from bucket boundaries.
//   - rate() averages over a trailing window (default 3s) so a single slow/fast
//     transfer reads smoothly and decays to 0 only after a full window of real
//     inactivity.
//   - rate({ minSpanMs }) lets ADAPTIVE consumers (concurrency limiters) ignore an
//     immature first burst (returns 0 until the window has that much history), so a
//     warm-up spike does not bias the ramp. The UI reads with minSpanMs = 0.
//   - Non-positive deltas (a progress reset / retry re-baseline) are ignored, so a
//     drop never produces a negative or fake rate.
//
// IMPORTANT: use ONE meter per independently-controlled path. The UI may display the
// SUM of the per-path meters, but each adaptive controller must be fed only its own
// path's meter - mixing legacy and block bytes into a shared adaptive signal makes
// one path's throughput inflate the other's ramp.
export function createUploadThroughputMeter({ windowMs = 3000, bucketMs = 250, now = Date.now } = {}) {
    let cumulative = 0;
    // Each bucket covers [t, t + bucketMs); startCumulative is the running total of all
    // bytes recorded BEFORE this bucket, so bytes-in-window = cumulative -
    // buckets[0].startCumulative. firstAt/lastAt keep the REAL event times inside the
    // bucket so minSpanMs and inactivity are measured from actual byte movement, not
    // from the bucket edges.
    let buckets = [];
    const prune = (at) => {
        const cutoff = at - windowMs;
        // Keep buckets[0] as the baseline that straddles/precedes the window start; drop
        // a bucket only once its SUCCESSOR has fully passed the cutoff.
        while (buckets.length >= 2 && buckets[1].t + bucketMs <= cutoff) {
            buckets.shift();
        }
    };
    return {
        // addBytes records a positive delta of real wire bytes into its time bucket.
        addBytes(delta, at = now()) {
            const d = Number(delta);
            if (!Number.isFinite(d) || d <= 0) {
                return; // ignore reset/negative/zero deltas - no fake or negative rate
            }
            const bucketStart = Math.floor(at / bucketMs) * bucketMs;
            const last = buckets[buckets.length - 1];
            if (!last || last.t !== bucketStart) {
                buckets.push({ t: bucketStart, startCumulative: cumulative, firstAt: at, lastAt: at });
            } else {
                last.firstAt = Math.min(last.firstAt, at);
                last.lastAt = Math.max(last.lastAt, at);
            }
            cumulative += d;
            prune(at);
        },
        // rate returns bits/s over the trailing window. minSpanMs > 0 makes it hold at 0
        // until the window has at least that much history (for adaptive consumers).
        rate(at = now(), { minSpanMs = 0 } = {}) {
            if (buckets.length === 0) {
                return 0;
            }
            prune(at);
            const last = buckets[buckets.length - 1];
            // Real inactivity: no bytes for a full window, measured from the last byte
            // actually seen (not the end of its bucket).
            if (at - last.lastAt >= windowMs) {
                return 0;
            }
            const baseline = buckets[0];
            const bytes = cumulative - baseline.startCumulative;
            const spanMs = Math.min(windowMs, at - baseline.firstAt);
            if (spanMs <= 0 || spanMs < minSpanMs) {
                return 0;
            }
            return Math.max(0, (bytes / spanMs) * 1000 * 8);
        },
        reset() {
            cumulative = 0;
            buckets = [];
        },
        // Retained bucket count - exposed for tests asserting the buffer stays bounded.
        sampleCount() {
            return buckets.length;
        },
    };
}
