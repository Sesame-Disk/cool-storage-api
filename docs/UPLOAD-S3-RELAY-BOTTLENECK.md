# Upload Hang After Progress Bar Hits 100% — Architectural Analysis

**Status:** Pre-production blocker
**Scope:** Web UI uploads (`@seafile/resumablejs` → `/seafhttp/upload-api/{token}`)
**Author:** Architecture review, 2026-05-08
**Related docs:** [UPLOAD-DOWNLOAD-ANALYSIS.md](./UPLOAD-DOWNLOAD-ANALYSIS.md), [diagrams/upload-download-flow.md](./diagrams/upload-download-flow.md)

---

## TL;DR

The browser UI shows the upload at **100 % well before the file is actually persisted**. After the bar hits 100 % the user must continue waiting — typically **~as long again as the upload itself** — while the API node forwards bytes to S3. This is not a UI bug. It is an architectural property of the current design:

> **The API node is a store-and-forward relay.** Every byte of every upload traverses the API pod twice — once on ingress (browser → temp file on the pod's local disk) and once on egress (temp file → S3). The egress phase only **starts** when ingress is fully complete.

Total wall-clock time perceived by the user is roughly:

```
total ≈ size / ingress_bw  +  size / egress_bw
      ≈ 2 × wire_time           (when ingress_bw ≈ egress_bw)
```

A 1 GB upload at 25 MB/s effective ingress and similar egress takes ~80 seconds total: ~40 s of browser progress (visible) plus ~40 s of "Saving…" hang (invisible).

---

## Where the symptom comes from in code

### The chunked write path is fast and concurrent (good)

`internal/api/seafhttp.go:949` `HandleUpload` — chunked branch starting at line 1071.

Each Resumable.js chunk arrives with a `Content-Range` header and is streamed directly to a temp file on the pod's local disk:

```go
// internal/api/seafhttp.go:1071-1091
if isChunked {
    upload, err := chunkManager.GetOrCreateUpload(tokenStr, filename, parentDir, total)
    ...
    if err := upload.WriteChunkFromReader(file, start, end); err != nil {
        ...
    }
    if !upload.TryStartFinalization() {
        c.JSON(http.StatusOK, gin.H{"success": true})
        return
    }
    ...
}
```

Each non-final chunk POST returns `200 OK` immediately. The browser's progress bar advances to 100% the instant the **last chunk's body** is fully written to the wire — which happens **before** the response to that final POST arrives.

### The S3 phase only starts after the last byte lands (the bottleneck)

`finalizeUploadStreaming` is invoked synchronously from the **last chunk's HTTP handler** (`internal/api/seafhttp.go:1096`). Its work begins only after `TryStartFinalization` returns true, i.e. after every byte of the file is on the API node's disk:

```go
// internal/api/seafhttp.go:1242-1397
func (h *SeafHTTPHandler) finalizeUploadStreaming(...) {
    reader, err := upload.GetReader()              // open assembled temp file
    ...
    sem := make(chan struct{}, finalizeUploadConcurrency)  // = 8
    for {
        buf := make([]byte, uploadBlockSize)       // = 8 MB
        n, readErr := io.ReadFull(reader, buf)     // SINGLE-THREADED reader
        ...
        eg.Go(func() error {
            // per block: SHA-1 → encrypt → SHA-256 → S3 PUT → 3 Cassandra INSERTs
        })
    }
    eg.Wait()                                       // blocks the HTTP handler
    ... commit metadata ...
}
```

While `eg.Wait()` is running, the **last chunk's HTTP request is held open**. The browser sees a fully-uploaded file and a pending response, hence the "100 %, but spinning" experience.

### Why the S3 phase is roughly as long as the upload

The dominant factor is the **API pod's egress bandwidth to S3**, not CPU. Even with eight parallel `PutBlockAuto` workers, all of them share the same NIC. If user → API ingress runs at ~X MB/s and API → S3 egress runs at ~X MB/s on the same pod, both phases take the same amount of time. The two phases do **not overlap**, so wall time is the sum.

Additional contributors that prevent the S3 phase from being faster than ingress:

| # | Constraint | File / line | Effect on time |
|---|---|---|---|
| 1 | Reader is single-threaded; workers fan out from one read pipeline | `seafhttp.go:1286` `io.ReadFull(reader, buf)` | Caps reader at NVMe read speed. Not the limit in practice, but couples block dispatch to disk read. |
| 2 | 8-way S3 PUT parallelism cap | `seafhttp.go:944` `finalizeUploadConcurrency = 8` | At ~150 ms/PUT for an 8 MB block, 8-way → ~430 Mb/s aggregate to S3. Often near the pod's egress ceiling anyway. |
| 3 | Synchronous Cassandra INSERTs per block (3 round-trips per block: forward mapping, reverse mapping, block metadata) | `seafhttp.go:1351-1370` | For 1 GB at 8 MB blocks = 384 INSERTs. Parallelized 8-wide but not batched — adds 1–3 s per GB. |
| 4 | Per-upload mutex on the temp-file writer | `seafhttp.go:544-545` (`cu.mu.Lock()` held across `io.Copy`) | Resumable.js's `simultaneousUploads` is partly defeated; chunks queue on a single lock. Limits ingress parallelism, not egress, but worth fixing. |
| 5 | AES-CBC is full-buffer per 8 MB block | `crypto.EncryptBlockSeafile` | Acceptable at 8 MB; not the bottleneck. |
| 6 | Whole assembled file lives on the pod's local disk during finalize | `chunkManager` temp dir | Capacity risk under load (N concurrent uploads × file size). |

### Sequenced timeline

```
 t=0 ─────────── client uploads ─────────── t=T1   t=T1 ──── server pushes to S3 ──── t=T2
 ┌────────────┐                ┌─────────────────────────┐                ┌─────┐
 │  Browser   │ POST chunks    │ API node                │  PUT 8 MB blks │ S3  │
 │ Resumable  │──────────────▶ │  /tmp/<token>/<file>    │ ──────────────▶│     │
 │   .js      │  (200 each)    │  (single mutex, serial  │  8 in flight   └─────┘
 └────────────┘                │   writes to one fd)     │
        ▲                      │                         │  ⤴ Cassandra: 3 INSERTs/block
        │  progress bar = 100% │  finalizeUploadStreaming│       (sync, per-block)
        │  at t=T1             │   reader: ONE thread    │
        │                      │   workers: 8 (sem)      │
        │   ◀────── 200 OK to last chunk arrives at t=T2 (browser hangs T2-T1) ─────
```

For a 1 GB file on a typical deployment:

| Phase | Time | What FE shows |
|---|---|---|
| Wire upload (chunks → temp) | ~40 s | progress 0 → 100 % |
| **Hang** waiting for last-chunk response | ~30–40 s | "100 %" / spinner |
| Finalize complete, file appears | — | done |

---

## Proposed solutions

Two options. They are complementary: Option A is the structural fix; Option B is a UX mitigation that pairs with it.

### Option A — Pipelined finalize (incremental, in-place)

**Idea:** flush completed 8 MB **prefix** boundaries to S3 **as they fill**, rather than waiting for the full upload to land.

Resumable.js sends chunks in order most of the time. As soon as a contiguous prefix of the temp file passes an 8 MB boundary, that 8 MB block is eligible for the same per-block work that `finalizeUploadStreaming` does today (SHA-1 → encrypt → S3 PUT → mappings). When the last chunk arrives, only the **tail** of the file (≤ 8 MB plus any out-of-order gaps) and the metadata commit remain.

**Wall-clock effect:** the S3 phase now overlaps with ingress. Total time approaches `max(ingress, egress)` instead of `ingress + egress`. For a 1 GB upload this roughly **halves** perceived wait.

**Code sketch:**
- Per `ChunkUpload`, track `flushedPrefix int64` (bytes already shipped to S3) alongside the existing `Ranges`.
- After each `WriteChunkFromReader` call, while `Ranges[0].Start == 0 && Ranges[0].End - flushedPrefix >= uploadBlockSize`, dispatch one block to a per-upload bounded worker pool.
- `TryStartFinalization` becomes "all bytes received AND all prefix blocks committed"; the final-chunk handler waits on the existing `errgroup` plus the metadata commit.
- Out-of-order tails are handled by the existing reader-driven loop, but on a much smaller residual file.

**Risks / things to test:**
- Resumable.js retry behavior (a chunk can be re-sent). Re-flushing a block is fine because S3 PUT is idempotent on the SHA-256 key, but the ref-count math (`AccountBlockOnce`) must remain idempotent — there is already a test for this (`TestChunkUploadAccountBlockOnceSurvivesFinalizeRetry`); extend it.
- Out-of-order delivery (rare with Resumable.js but possible). The prefix watcher must handle gaps cleanly; in those cases the block stays unflushed until the gap fills.
- An upload that aborts mid-flight may leave **flushed but uncommitted** blocks in S3. These are GC-reachable today via the existing block-GC path, but the test suite must verify it.

### Option B — Honest progress UI (cosmetic)

**Idea:** even without changing the data plane, give the FE a way to show what the server is doing during the hang.

Two simple ways:
- The last-chunk POST body returns periodic progress JSON via `Transfer-Encoding: chunked` while finalize is running. Resumable.js doesn't natively render this; it would require a small custom hook.
- A second endpoint `GET /seafhttp/upload-status/:token` returns `{phase: "finalizing", blocks_flushed: 12, blocks_total: 128}` and the FE polls it at, e.g., 1 Hz once the last chunk has been sent.

This does **not** make uploads faster. It just stops users from thinking the app is broken. Recommended as a stopgap if A/B can't ship before launch.

---

## Recommendation

1. **Ship Option A before prod.** It is local to `seafhttp.go` + `ChunkUpload`. Halves perceived wait. No FE changes. No new endpoints. Encrypted libraries unaffected. Works against any object-store backend (S3, GCS, Azure, MinIO, on-prem) — no backend lock-in.
2. **Ship Option B alongside it.** Cheap UX win, makes the residual S3 wait honest while the structural fix is rolling out.

The longer-term scaling concern — that every byte traverses the API pod twice — is real, but the answer to it should remain **backend-agnostic**. Anything that solves it (e.g. per-region API workers closer to storage, parallel egress connections, better block-store interfaces) must keep working across our pluggable `ObjectStore`/`BlockStore` implementations. Solutions that couple us to a single cloud provider are out of scope.

---

## How to verify the fix — testing plan

We need both **white-box unit tests** (in `internal/api/`, can run without infra) and **black-box integration tests** (in `internal/integration/`, run against a live stack). The codebase already has an integration test scaffold we can reuse: `helpers_test.go` (`testClient` with token auth), `upload_download_test.go` (`uploadFileThroughLink`, `TestUploadAndDownloadRoundTrip`), and a `//go:build integration` tag that the standard suite respects (`docs/TESTING.md:218-219`).

### A. Reproducing the bug today (regression test the symptom we're fixing)

Until Option A lands, the symptom is "time-to-200 on the last chunk ≫ time-to-write the last chunk's bytes." We can encode that as a measurable test.

#### Unit test: `TestFinalize_DoesNotStartUntilLastChunk` (`internal/api/seafhttp_test.go`)

Goal: prove that no S3 PUT happens before `TryStartFinalization` returns true.

- Use the existing `mockObjectStore` (already in the file) but wrap it in a counter that records timestamps for every `Put` call.
- Drive a real `ChunkManager` + `ChunkUpload` from the test, write N-1 chunks, assert `mockObjectStore.PutCount == 0`.
- Write the final chunk, run finalize, assert `PutCount == ceil(total/uploadBlockSize)`.
- This **passes today** and **continues to pass after Option A is implemented as a no-op fallback** (Option A only changes the *timing*, not the eventual count).

#### Unit test: `TestFinalize_TimingHangsOnLastChunk` (`internal/api/seafhttp_test.go`)

Goal: encode the bottleneck as a property the fix will invert.

- Use an `mockObjectStore` whose `Put` blocks for `D = 50 ms`.
- Send N chunks sequentially. Measure `chunk_i_response_latency`.
- Assert the **last** chunk's latency `>= (numBlocks/8) * D`, while non-final chunks return in `<< D`.
- After Option A lands, this becomes a **negative** test: rename to `TestPipelinedFinalize_LatencyAmortized`; assert each chunk's latency is `< 2 * D` (i.e., per-chunk work is overlapped, not stacked at the end).

#### Integration test: `TestUploadProgressVsFinalizationLatency` (`internal/integration/upload_download_test.go`)

Goal: black-box, end-to-end timing assertion against the running API.

- Build a 64 MB file (`crypto/rand`, fixed seed) so it spans 8 blocks.
- Issue chunked POSTs with `Content-Range: bytes a-b/64MB`, 8 MB chunks.
- For each POST, record `t_request_start` and `t_response_received`.
- Today: expect `responseLatency[last] - responseLatency[median(non-last)] > 200 ms` (call this `gap_today`).
- After Option A: expect `gap < 100 ms` (configurable; the ratio `gap_after / gap_today < 0.3` is the actual property to assert).
- Tag with `//go:build integration`. Skips cleanly when the stack isn't running, like the rest of the suite.

### B. Tests that pass today and must keep passing (correctness regressions)

These do not change with the fix; they exist to guard against breaking correctness while changing concurrency.

#### Existing — keep green

- `TestUploadAndDownloadRoundTrip` (`internal/integration/upload_download_test.go:64`) — full FE flow.
- `TestDuplicateSeafhttpUploadIncrementsBlockRefCount` (line 229) — dedup ref-count.
- `TestChunkUploadWriteAndRead` (`internal/api/seafhttp_test.go:1144`) — basic chunk assembly.
- `TestChunkUploadIsComplete_OutOfOrderLastChunk` (line 1205) — out-of-order ranges.
- `TestChunkUploadWriteDuringFinalizationIsIdempotentOnly` (line 1253).
- `TestChunkUploadAccountBlockOnceSurvivesFinalizeRetry` (line 1303) — finalize retry idempotence.
- `TestChunkJanitor_*` (lines 85–214) — temp-file cleanup.

#### New — required for Option A

1. **`TestUpload_BlockBoundary_Sizes`** (unit) — file sizes `{1, uploadBlockSize-1, uploadBlockSize, uploadBlockSize+1, 8*uploadBlockSize, 8*uploadBlockSize+1}`. Round-trip in encrypted and unencrypted libraries. Verify exact byte equality on download. Catches off-by-one when prefix flushing crosses boundaries.
2. **`TestUpload_OutOfOrderChunks`** (unit, then integration) — issue chunks in order `[2,0,1,3]` with the integration HTTP client. Verify the resulting file matches the source. Today's code handles this; Option A's prefix-watcher must not regress it.
3. **`TestUpload_ChunkRetransmit`** (unit) — re-send chunk 1 after chunk 3 has already arrived. Must remain a no-op (not corrupt the file, not double-flush a block).
4. **`TestUpload_ParallelChunkPosts`** (integration) — open 8 simultaneous `Content-Range` POSTs. Verify the file is correct **and** that the per-chunk mutex (currently `cu.mu.Lock()` across `io.Copy`) is not regressed back into existence after a `pwrite`-based fix.
5. **`TestUpload_ClientDisconnectMidStream`** — abort the connection mid-chunk. Verify (a) temp file gets cleaned up by the janitor within `trackerTTL`; (b) no orphan blocks left in S3 for partial flushes (Option A specifically introduces this risk).
6. **`TestUpload_EncryptedLibrary_LargeFile`** (integration) — required for prod sign-off. Today there is no integration test for encrypted upload of large files (per `docs/UPLOAD-DOWNLOAD-ANALYSIS.md`). Add one: 64 MB file, encrypted library, upload → download → byte-equal.

### C. Tests specific to the fix (validate the property we're claiming)

#### Option A (pipelined finalize)

1. **`TestPipelinedFinalize_FlushesBeforeLastChunk`** (unit) — counting `mockObjectStore`. Send all chunks except the last. Assert `PutCount == numBlocks - 1` (the prefix has been flushed; only the trailing block waits for the final chunk and the metadata commit).
2. **`TestPipelinedFinalize_OutOfOrderHoldsPrefix`** — send chunks `[2,3,0,1]`. After chunk 2 arrives, no flush is allowed (no contiguous prefix). After 0 arrives, blocks 0 may flush. After 1 arrives, blocks 1, 2, 3 all flush in order. Assert ordering.
3. **`TestPipelinedFinalize_BenchmarkLatency`** (integration, optional `-bench`) — measures end-to-end latency for a 256 MB upload with a configurable artificial backend delay. Property: `total ≤ 1.2 × max(ingress_time, egress_time)`. Acts as a regression guardrail against future code that sneaks sequential work back in.

#### Block splitter determinism (Phase 1; Phase 1.5 if it ships)

These guard the dedup-preserving property the design depends on.

1. **`TestBlockSplitter_FixedSize_Deterministic`** (unit, Phase 1) — `FixedSizeSplitter{8 MB}` on the same input bytes always produces the same boundaries and SHA-256 IDs. Trivial today but necessary to lock the invariant.
2. **`TestBlockSplitter_FastCDC_DeterministicAcrossInputs`** (unit, Phase 1.5 only) — same content fed in two passes through a `FastCDCSplitter{2,8,32 MB}` produces **identical** SHA-256 ID lists. Exists to prevent any future variant (e.g. a revived `NewFastCDCFromSpeed`) from regressing the determinism property.
3. **`TestUpload_DedupAcrossDifferentClients`** (integration, Phase 1+) — upload the same 64 MB file twice from two different clients (different IPs, different sessions). Assert `ref_count` on the resulting blocks is **2**. Catches any regression where transport details leak into storage block identity.

### D. How to run

Existing test infrastructure already supports this; no new tooling needed.

```bash
# Unit tests for the chunk manager and finalize path
go test ./internal/api/... -run 'ChunkUpload|Finalize|Pipelined' -v

# Integration tests against a live stack
docker compose --profile test run --rm --build go-integration-test \
  -run 'TestUploadProgressVsFinalizationLatency|TestUpload_'

# Or via the existing wrapper
./scripts/test.sh go-integration
```

The new tests follow the existing patterns:
- Unit tests use `mockObjectStore` (already present in `seafhttp_test.go`) and the real `ChunkManager` with `t.TempDir()`.
- Integration tests use `adminClient`, `createTestLibrary`, `uploadFileThroughLink`, and the `//go:build integration` tag, exactly as `upload_download_test.go` does today.

---

## Decision matrix

| | Option A (pipelined) | Option B (UX) |
|---|---|---|
| **Eliminates the post-100% hang** | Mostly (down to a small tail) | No |
| **Removes API pod as bandwidth bottleneck** | No | No |
| **Removes temp-disk requirement** | No | No |
| **FE changes required** | None | Small |
| **Encryption design changes** | None | None |
| **Backend lock-in (S3 / GCS / Azure / on-prem)** | None | None |
| **Code surface** | `seafhttp.go` only | One small status endpoint |
| **Time to ship (rough)** | days | hours |
| **Recommended for v1.0 launch?** | **Yes** | Yes (alongside A) |

---

## Open questions

- What is the deployed pod's measured egress bandwidth to the object store? If it's already saturated by 8-way `PutBlockAuto`, Option A's gain is closer to ~30 % than 50 %. We should measure with `benchmark-upload-download.sh` (`docs/benchmarks/`) before/after to set realistic expectations.
- Does the FE bootstrap actually set `appPageOptions.resumableSimultaneousUploads`? If not, the default is **1** (`frontend/src/utils/constants.js:65`), in which case ingress is fully serialized and the upload phase is itself slower than it needs to be — independent of Option A. Worth checking and bumping to 4–8.
- The longer-term scaling concern (API pod as bandwidth funnel) is real but needs a backend-agnostic answer. Candidate directions worth exploring: per-region API workers co-located with their object store, more aggressive parallelism in `finalizeUploadConcurrency`, or making the `BlockStore` interface streaming-aware so it can multiplex more PUTs over a single connection. Concrete proposal pending benchmark numbers.

---

## Adaptive chunking — designed but not wired

There is a complete adaptive-chunking implementation in `internal/chunker/adaptive.go` that **no production code path calls**. Before the roadmap, this needs to be made visible because it changes how Phase 1 (`blocksink.StreamFile`) should be designed.

### What's there today

`internal/chunker/adaptive.go` (281 lines) provides:

- `SpeedProbe.Probe(ctx, w)` — writes a 1 MB probe payload (configurable, `cfg.ProbeSize`) and times it; stores `bytesPerSecond`. Honors `ProbeTimeout` (30s default).
- `AdaptiveChunker` — converts measured speed to a target chunk size with `optimalSize = bytesPerSecond * targetSeconds`, clamped to `[AbsoluteMin=2 MB, AbsoluteMax=256 MB]` around an `InitialSize=16 MB`. Default target is **8 seconds per chunk**.
- `AdjustOnTimeout(factor)` — halves chunk size on timeout (default factor 0.5), with floor enforcement.
- `AdjustOnSuccess(actualDuration, factor)` — grows chunk size by 25 % (default) when the upload completed faster than the target.
- `GetChunkSizes()` returns `min = avg/4, max = avg*4` for FastCDC's variable-boundary mode.
- `NewFastCDCFromSpeed()` builds a FastCDC chunker from current speed.
- `RecommendedChunkSize(bytesPerSecond, targetSeconds)` and `SpeedCategory(bps)` for diagnostics.

Configuration scaffolding is in `internal/config/config.go:580-612`:

```go
type ChunkingConfig struct {
    Algorithm     string         `yaml:"algorithm"`      // fastcdc
    HashAlgorithm string         `yaml:"hash_algorithm"` // sha256
    Adaptive      AdaptiveConfig `yaml:"adaptive"`
    Probe         ProbeConfig    `yaml:"probe"`
    Retry         RetryConfig    `yaml:"retry"`          // ChunkTimeout, MaxRetries, ReduceOnTimeout, ReduceOnFailure, BackoffBase, BackoffMaxJitter
}

type AdaptiveConfig struct {
    Enabled       bool  `yaml:"enabled"`
    AbsoluteMin   int64 `yaml:"absolute_min"`   // 2 MB floor
    AbsoluteMax   int64 `yaml:"absolute_max"`   // 256 MB ceiling
    InitialSize   int64 `yaml:"initial_size"`   // 16 MB starting point
    TargetSeconds int   `yaml:"target_seconds"` // 8s default
}
```

### What's actually used today

A grep for `chunker.` outside the chunker package:

```
internal/storage/blocks.go:75   (uses chunker.Block as a struct type only)
internal/storage/blocks.go:221  (uses chunker.Block as a struct type only)
```

That's it. No upload handler instantiates `AdaptiveChunker` or `SpeedProbe`. The current upload-path block size comes from two **fixed** constants in unrelated files:

- Server: `seafhttp.go:936` — `const uploadBlockSize = 8 * 1024 * 1024` (used both for the temp-file read loop and as the storage block boundary).
- FE: `bootstrap.go:462` and `sharelink_view.go:{201,413}` bootstrap `resumableUploadFileBlockSize: h.config.WebUploads.ResumableChunkSizeMB`, which defaults to **8** in `config.go:697`. Static, server-wide.

So `Adaptive.Enabled` does nothing today regardless of how it's set in YAML. The `Retry` block (chunk timeouts, max retries, exponential backoff) is similarly inert.

### Why adaptive wire-size doesn't earn its keep on its own

It's tempting to plumb the speed probe into the FE so chunks shrink on slow links and grow on fast ones. But adaptive **wire** chunking only matters if it changes how bytes are *stored* — and we're committing to fixed-size storage blocks for v1. Re-cutting variable-size wire chunks back into fixed 8 MB blocks at the temp file is just bookkeeping; the user-perceived metrics (total upload time, retry surface, dedup) are governed almost entirely by the storage-block size and the egress pipelining (Option A), not by the wire chunk size.

The two axes are coupled, not independent:

| Storage block size | Sensible wire chunk size | Adaptive wire chunking pays off? |
|---|---|---|
| Fixed (today, and Phase 1) | **Same as the storage block** — one number, one concept. | **No.** A probe to pick a size that gets re-cut server-side is theatre. |
| Variable (FastCDC, Phase 1.5) | Pure transport, decoupled from storage boundaries. Server runs FastCDC over the reassembled stream. | **Maybe.** This is the only configuration where wire size has independent meaning, and only here can the speed probe earn its complexity. |

We're choosing fixed-size storage blocks for v1. So the right move is to **delete the wire/storage distinction from the v1 design** and align both at one number (8 MB today, configurable per deploy). The `AdaptiveChunker` and `SpeedProbe` code stays in `internal/chunker/` as reference for the FastCDC future, but is not wired in.

### What this means for the roadmap

- Phase 1 (`blocksink.StreamFile`) takes a single `BlockSize` (or, equivalently, a `BlockSplitter` whose only v1 implementation is `FixedSizeSplitter{8 MB}`). No probe, no adaptive policy.
- Phase 1.5 (FastCDC, opt-in) is the **only** phase that re-introduces a wire-vs-storage distinction, and **if and only if** that phase actually ships does the adaptive-probe question come back on the table.
- The "Phase 4.5: plumb adaptive wire-chunk into the FE" item is removed — it solves a problem we don't have.
- `chunker.NewFastCDCFromSpeed` is dead by construction (it conflates the axes); when Phase 1.5 ships, it should be deleted along with it. The static-FastCDC implementation lives next to it instead.

---

## Upload endpoint inventory & convergence roadmap

The fix above only touches **one** of four upload data paths the API currently exposes. Each path was added at a different time for a different client, and they have drifted. They all eventually call into the same `BlockStore`, but everything **above** that — body parsing, hashing, encryption, dedup, mapping writes, fs/commit metadata — is duplicated and inconsistent. Before we can claim that "uploads are fast" as a property of the system, we need every endpoint to share the same fast path.

### The four data-receiving upload endpoints

| # | Route | Handler | Used by | Body shape | Streams body? | Splits into 8 MB blocks? | Parallel S3 PUTs? | Resumable? | Encryption applied? |
|---|---|---|---|---|---|---|---|---|---|
| 1 | `POST /seafhttp/upload-api/:token` (chunked branch) | `HandleUpload` (`internal/api/seafhttp.go:949`, chunked at 1071) | Web UI (Resumable.js), upload links, share links, MD editor image upload | multipart + `Content-Range` | yes (chunk → temp file via `WriteChunkFromReader`) | **yes** (`uploadBlockSize = 8 MB`) | **yes** (`finalizeUploadConcurrency = 8`) | yes (offset-based) | yes, server-side per block |
| 2 | `POST /seafhttp/upload-api/:token` (non-chunked branch) | same handler, fallback at `seafhttp.go:1129` | Same clients when no `Content-Range` is sent | multipart, single body | **no** (`io.ReadAll(file)` at line 1129) | **no** (file = 1 block) | n/a (single object) | no | yes, full-buffer |
| 3 | `POST /api/v2.1/repos/:repo_id/upload` (and `/api2/...`) | `UploadFile` (`internal/api/v2/files.go:2673`) | REST clients, `repo-seatable-integration-dialog.js`, automation | multipart, single body | **no** (`io.ReadAll(file)` at line 2731) | **no** (file = 1 block) | n/a | no | yes, full-buffer |
| 4 | `PUT /seafhttp/repo/:repo_id/block/:block_id` | `PutBlock` (`internal/api/sync.go:858`) | SeaDrive, Seafile mobile sync clients | raw body, one block | **no** (`io.ReadAll(c.Request.Body)` at line 889) | n/a (client already chunked) | n/a (client decides parallelism) | n/a | **no** (client encrypts before PUT) |
| 5 | `POST /api/v2/blocks/upload` | `UploadBlock` (`internal/api/v2/blocks.go:153`) | Future block-level REST clients (not wired to FE today) | raw body, one block | **no** (`io.ReadAll(LimitReader(...))` at line 189) | n/a (one block per call) | n/a | n/a | **no** (caller decides) |

### What's actually duplicated

Every endpoint above re-implements some subset of the same five steps. None share a helper.

```
                 (1) chunked    (2) non-chunked   (3) UploadFile   (4) PutBlock   (5) UploadBlock
body → bytes        stream         ReadAll          ReadAll          ReadAll         LimitReader
SHA-1 of plaintext  per-block ✓    full file ✓      full file ✓      —               —
encrypt             per-block ✓    full file ✓      full file ✓      client-side     n/a
SHA-256 of stored   per-block ✓    full file ✓      full file ✓      ✓               ✓
PutBlockAuto / Put  per-block ✓    once             once             once            once
INSERT mappings     per-block      once             once             SHA-1 only      no
INSERT block meta   per-block      once             once             ✓               ✓ (dedup-aware)
fs_object + commit  multi-block    single-block     single-block     no              no
```

Five copies of "compute hashes and store". Three copies of "compute hashes, encrypt, store, write mappings". Two copies of "and then build a single-block fs_object + commit". One copy of "and then build a multi-block fs_object + commit". Each copy has its own bugs (the legacy paths still have whole-file `ReadAll`, the legacy paths silently store as one giant block defeating dedup, the sync path skips server-side encryption, etc.).

### Behavioural contradictions worth calling out

- **(2) and (3) defeat block-level deduplication entirely.** Identical 100 MB files uploaded via these endpoints produce two distinct 100 MB S3 objects keyed on the SHA-256 of the **whole file**, not on per-block SHA-256. A user re-uploading after editing one byte stores another 100 MB.
- **(2) is reachable from the web UI today.** A misbehaving or older client that POSTs to `/seafhttp/upload-api/:token` without `Content-Range` silently falls into the slow, single-block path. There is no logging that this happened.
- **(1) writes encryption metadata that (3) cannot decrypt.** They produce different fs_object shapes (`block_ids: [N items]` vs `block_ids: [1 item]`). Files uploaded via path (3) cannot be retroactively re-blocked.
- **(4) and (5) trust the client about hashing semantics.** That's by design (sync protocol contract), but it means three different invariants live in three different files — and there is no single place where "this is what a valid block looks like" is enforced.

### Convergence roadmap

The goal: every upload endpoint becomes a thin adapter over **one** internal pipeline. That pipeline has the chunked-finalize behaviour (Option A) for free, and stays backend-agnostic — no S3-only features anywhere. We need to get there in stages because the four clients have different release cadences and we cannot break sync clients in the field.

### Status after 2026-05-12 closeout

The current branch did **not** ship the Phase 1 `blocksink` extraction described
below. The safe slice that actually shipped is narrower:

- async preflush for contiguous chunked blocks on the existing SeafHTTP path
- finalize waiting/reuse semantics for preflushed blocks
- reliability/security hardening around encrypted-library detection and upload
    temp-file cleanup
- focused unit and integration coverage for byte-equal chunked upload behavior

The roadmap below remains the forward-looking convergence plan after that safe
slice. Read it as future work, not as a description of what already landed.

#### Phase 0 — Lock in current behaviour with characterization tests *(week of launch, parallel with Option A)*

We cannot refactor what we cannot regression-test. Before any consolidation, every endpoint above gets a black-box test in `internal/integration/` that asserts:

- successful upload of a small (1 KB) and a medium (64 MB) file,
- byte-equal download round-trip,
- correct ref-count on duplicate upload (per-endpoint dedup behaviour, including the broken cases — we *want* them recorded so the fix is visible),
- 403/401 on missing auth, 413 on oversize.

Tests live next to `upload_download_test.go`, named `TestEndpoint{1..5}_*`. No production change in this phase.

**Exit criteria:** all four endpoints have a green characterization suite. Option A from above is shipped on endpoint #1.

#### Phase 1 — Extract the block-storage primitive *(post-launch, ~1 sprint)*

Add a single internal function. Critically, the primitive takes a **`BlockSplitter`** rather than a fixed `BlockSize` int — that's the one shape change we make so the wire-vs-storage distinction is structurally honoured from day one.

```go
// internal/storage/blocksink/sink.go (new package)
type BlockResult struct {
    SHA1IDs    []string  // SHA-1 of plaintext, one per block (Seafile-compat)
    SHA256IDs  []string  // SHA-256 of stored bytes, one per block
    Sizes      []int     // stored size per block (post-encryption)
    TotalBytes int64     // plaintext total
}

// BlockSplitter decides where a block ends. Implementations must be
// content-deterministic: the same input bytes must always produce the
// same boundaries (otherwise SHA-256 dedup breaks across uploads).
type BlockSplitter interface {
    NextBoundary(buf []byte, isFinal bool) int
}

type Options struct {
    Splitter       BlockSplitter        // FixedSize{8 MB} today; FastCDC{min,avg,max} later
    Concurrency    int                  // typically 8
    EncryptKey, IV []byte               // nil = no encryption
    Store          storage.BlockStore   // backend
    OnBlock        func(BlockResult, blockIdx int) error  // hook for mapping/metadata
}

func StreamFile(ctx context.Context, r io.Reader, opt Options) (BlockResult, error)
```

Phase 1 ships only the `FixedSizeSplitter{8 MB}` implementation (matching today's behaviour). The interface is in place so Phase 1.5 can swap it without touching call sites.

**Exit criteria:** endpoint #1 (chunked branch) calls `blocksink.StreamFile` with `FixedSizeSplitter`. Characterization tests still green. Code in `seafhttp.go:1242-1397` is replaced by the call. **No** speed-probe coupling at this layer.

#### Phase 1.5 — FastCDC-at-fixed-parameters as the storage splitter *(optional, ~½ sprint)*

Add `FastCDCSplitter{min, avg, max}` as a second `BlockSplitter` implementation. The parameters are **per-repository constants** — read once when the repo is created (or migrated) and stored in `libraries`, never varied by upload speed. Switching a repo from fixed-size to FastCDC is a one-way migration; existing blocks stay valid because they're keyed by SHA-256.

This is the **only place** content-defined chunking is allowed to influence storage. It buys better dedup across small edits to large files (the canonical FastCDC win) without breaking dedup across uploads of the same file from different connections.

**Exit criteria:** a feature-flagged repo can use FastCDC blocks; old repos continue using fixed-size; both go through the same primitive. `chunker.NewFastCDCFromSpeed` is **deleted** (it conflates the two axes). Adaptive-config defaults stay backward-compatible.

> Phase 1.5 is optional for the convergence story — Phase 1 alone gives us the unified pipeline. We list it here to make sure the right `BlockSplitter` interface ships in Phase 1 so we don't have to refactor again to add it.

#### Phase 2 — Extract the metadata-commit primitive *(~1 sprint)*

```go
// internal/api/v2/uploadcommit/commit.go (new file)
type Args struct {
    OrgID, RepoID, UserID string
    ParentDir, Filename   string
    BlockSHA1IDs          []string  // for the fs_object
    BlockSHA256IDs        []string  // for ref counting
    BlockSizes            []int
    TotalSize             int64
    Replace               bool
}

func Commit(ctx context.Context, db *db.DB, args Args) (commitID, fileID, actualName string, err error)
```

This wraps the existing `commitUploadedFileMultiBlock`, mapping inserts, and ref-count writes — and finally batches the per-block Cassandra round-trips into one or two batched statements (the optimization noted earlier). Endpoint #1 starts using it; the per-block synchronous INSERTs in `seafhttp.go:1351-1370` move into the commit step where they can batch.

**Exit criteria:** endpoint #1 routes all metadata writes through `uploadcommit.Commit`. Per-block `INSERT block_id_mappings` is gone from the worker goroutine.

#### Phase 3 — Migrate endpoints #2 and #3 onto the same pipeline *(~1 sprint)*

`UploadFile` (#3) and the non-chunked branch of `HandleUpload` (#2) are rewritten as:

```go
result, err := blocksink.StreamFile(ctx, file, opts)  // file = multipart body, streamed
if err != nil { ... }
_, fileID, _, err := uploadcommit.Commit(ctx, db, uploadcommit.Args{...})
```

That single change fixes the "1 file = 1 block" anti-pattern on both endpoints, recovers dedup, and removes the whole-file `io.ReadAll`. The function bodies shrink to ~30 lines each; the rest is permission/quota plumbing that already lives in middleware.

**Exit criteria:** characterization tests for #2 and #3 still green, **plus** new dedup tests show ref-count incrementing on identical uploads via these endpoints.

#### Phase 4 — Migrate endpoints #4 and #5 *(~½ sprint each)*

`PutBlock` (#4) and `UploadBlock` (#5) already operate on a single block. They migrate to a **single-block adapter**: `blocksink.StoreOne(ctx, reader, expectedSHA256, opts) (BlockResult, error)`. Same code path as `StreamFile`, with `BlockSize = ContentLength`, `Concurrency = 1`. The mapping/metadata logic moves to a shared helper so the SHA-1↔SHA-256 dual write lives in exactly one place.

After this phase, all five paths read bytes through the same primitive and store them through the same primitive.

**Exit criteria:** sync client integration tests (existing) still green. The dual-write SHA-1↔SHA-256 logic lives in a single file, called from one place per endpoint.

#### Phase 5 — Sunset legacy paths *(when traffic on them is < 1 %)*

Once telemetry shows the bulk of upload bytes flowing through the chunked + pipelined path, the legacy endpoints can be deprecated:

- Endpoint **#2** (non-chunked branch of `HandleUpload`) is deleted outright; its code path is redundant once every client is sending `Content-Range`. A small Gin middleware can short-circuit any `Content-Range`-less request with a 400 and a clear error.
- Endpoint **#3** (`UploadFile`) is kept only as an automation/legacy escape hatch behind a feature flag, with a `Deprecation` response header. Its body now goes through `blocksink.StreamFile` (from Phase 3), so it is no longer a performance liability — it is just an extra surface area to maintain.
- Endpoints **#4** and **#5** stay — they are different by design (sync protocol contract; raw block REST), but they share implementation through `blocksink`.

### Roadmap-at-a-glance

| Phase | What changes | Endpoints touched | Risk | Ship before launch? |
|---|---|---|---|---|
| 0 | Add characterization tests | all 5 (tests only) | low | **yes** |
|   | Ship Option A (pipelined finalize) | #1 | medium | **yes** |
| 1 | Extract `blocksink.StreamFile` with `BlockSplitter` interface | #1 (refactor) | low | no |
| 1.5 | Add `FastCDCSplitter` (fixed params, per-repo) — optional | #1 (opt-in feature flag) | low | no |
| 2 | Extract `uploadcommit.Commit`; batch Cassandra | #1 (refactor) | low | no |
| 3 | Migrate #2 and #3 onto the pipeline | #2, #3 | medium (dedup behaviour changes) | no |
| 4 | Migrate #4 and #5 onto a single-block adapter | #4, #5 | medium (sync clients in field) | no |
| 5 | Delete dead branches; deprecate `UploadFile` | #2, #3 | low | no |

The end state: one `blocksink` primitive (with a pluggable `BlockSplitter`), one `uploadcommit` primitive, four thin handlers (#2 disappears in Phase 5) that each do auth/quota/permission plumbing and then call into the shared pipeline. Wire chunk size and storage block size are aligned at one number — no probes, no adaptive resize, no extra endpoints. If Phase 1.5 (FastCDC) ever ships, the wire-vs-storage distinction returns and the dormant `AdaptiveChunker` code can be reconsidered. The pipeline stays backend-agnostic — it depends only on the `BlockStore` interface, which any object store (S3, GCS, Azure Blob, MinIO, on-prem) can implement. Performance characteristics of the chunked-finalize fix (Option A) become a property of every upload endpoint, not just the web UI's.
