// Shared upload throughput meter for BOTH the legacy resumable path and the block
// (CAS) path. It replaces the old per-path instantaneous delta (getBitrate /
// sampleBlockUploadBitrate), which recomputed a bits/s figure over a single fixed
// ~500ms window ONLY when a progress event arrived. At low concurrency (the adaptive
// client starts at one slot) progress events are sparse/bursty — especially on fast
// localhost where an 8 MB chunk/block completes between window boundaries — so most
// windows saw no fresh sample and the readout collapsed to ~0, only stabilizing once
// concurrency climbed. See docs/WEB-BLOCK-UPLOAD.md item 6.
//
// This meter instead averages REAL wire bytes over a trailing sliding window (~3s),
// is recomputed on a timer (not only on progress events), and decays to 0 only after
// genuine inactivity. Feed it monotonic byte DELTAS from whatever real-bytes source
// each path has (block: onTransferProgress; legacy: the per-file loaded-bytes delta).

// createUploadThroughputMeter builds an isolated meter. `windowMs` is the averaging
// horizon; `now` is injectable for tests.
export function createUploadThroughputMeter({ windowMs = 3000, now = Date.now } = {}) {
    let cumulative = 0;          // total wire bytes fed since the last reset
    // samples: ascending-by-time [{ t, cumulative }]. We keep every sample inside the
    // trailing window plus ONE sample at/just before the window start, so the window
    // baseline total is always known even when events are sparse.
    let samples = [];

    const pruneBefore = (at) => {
        const cutoff = at - windowMs;
        // Drop samples older than the cutoff, but always keep the newest such sample
        // (samples[0]) as the window baseline: stop once the SECOND sample is still
        // newer than the cutoff.
        while (samples.length >= 2 && samples[1].t <= cutoff) {
            samples.shift();
        }
    };

    return {
        // addBytes records a positive delta of real wire bytes. Non-positive deltas
        // (a progress reset on retry, or a spurious repeat) are ignored so a reset can
        // never produce a negative delta or a fake spike.
        addBytes(delta, at = now()) {
            const d = Number(delta);
            if (!Number.isFinite(d) || d <= 0) {
                return;
            }
            if (samples.length === 0) {
                // Seed a zero baseline at the first byte so the first window has a
                // proper start total instead of dividing by nothing.
                samples.push({ t: at, cumulative: 0 });
            }
            cumulative += d;
            samples.push({ t: at, cumulative });
            pruneBefore(at);
        },

        // rate returns bits/s averaged over the trailing window. It is 0 before any
        // bytes and 0 again after a full window of inactivity, so a stalled/finished
        // upload decays to 0 instead of freezing on its last reading.
        rate(at = now()) {
            if (samples.length === 0) {
                return 0;
            }
            pruneBefore(at);
            const last = samples[samples.length - 1];
            // Real inactivity: no new bytes for a whole window → 0.
            if (at - last.t >= windowMs) {
                return 0;
            }
            const baseline = samples[0];
            const bytes = cumulative - baseline.cumulative;
            // Average over the elapsed window, capped at windowMs (so a burst is spread
            // across the horizon, not divided by a near-zero sample span → no ~0 and no
            // extreme sawtooth). Before we have windowMs of history, use the real span.
            const spanMs = Math.min(windowMs, at - baseline.t);
            if (spanMs <= 0) {
                return 0;
            }
            return Math.max(0, (bytes / spanMs) * 1000 * 8);
        },

        reset() {
            cumulative = 0;
            samples = [];
        },
    };
}
