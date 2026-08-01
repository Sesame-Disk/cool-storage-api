# B4 Subcontract D — Download Admission Contract

**Date:** 2026-08-01  
**Branch:** `docs/b4-subcontract-d0-contract`  
**Status:** D0 documentation only. No production code or runtime behavior is
changed by this document.

This document freezes the contract and inventory for subcontract D of
`ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01`. It is the design record for the D1-D6
implementation series. Live status remains in
[`KNOWN_ISSUES.md`](./KNOWN_ISSUES.md); this document does not create a second
status tracker.

## Source Of Record

- [`KNOWN_ISSUES.md`](./KNOWN_ISSUES.md), `ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01`
- [`KNOWN_ISSUES.md`](./KNOWN_ISSUES.md),
  `ISSUE-OBJECT-STORAGE-ANONYMOUS-DOWNLOAD-01`
- [`OPEN-WORK-INDEX.md`](./OPEN-WORK-INDEX.md)
- [`PROD-SECURITY-READINESS-20260724.md`](./PROD-SECURITY-READINESS-20260724.md)
- [`UPLOAD-FENCE-FINDINGS-REGISTRY.md`](./UPLOAD-FENCE-FINDINGS-REGISTRY.md),
  X10/X11 relationship

The D0 contract is deliberately more precise than the original one-line D row.
The original row remains the canonical finding:

> Add per-IP or per-token rate/concurrency on read paths, distinct from write
> limits, to address bandwidth/resource exhaustion.

The implementation must not satisfy that sentence only by protecting two URLs
while leaving equivalent storage-read paths unbounded.

## Why D Exists

Subcontracts A1/A2, B and C now protect their respective write, block-PUT and
check-blocks resources. They do not protect download resources, and their
capacities must remain independent.

The remaining risk is accepted read work that can keep Cassandra, object-store
readers, response writers, buffers, compression/ZIP state and egress occupied.
A per-request memory bound or a request-start rate limiter is not an aggregate
bound when many accepted transfers remain active for minutes.

The D closure claim is therefore:

> Every application path that reads user content from block/object storage and
> writes that content, or an inline representation of it, into an HTTP response
> passes one process-local, bounded download-admission coordinator for the
> lifetime of the protected read.

This is a process-local node-capacity guard, not a cluster-global quota. Fleet
capacity scales with the number of application nodes. Exact cluster-global
shaping would be a separate distributed or ingress design.

## D0 Decisions

### 1. Scope Is By Byte-Producing Flow

The definitive scope is the code path, not only the route string. A path is in D
when it opens a block/object-storage reader and produces user file bytes or an
inline file-content representation in the response.

The following producers are in scope:

| Producer | Route or entry point | Current behavior | D profile |
|---|---|---|---|
| `SeafHTTPHandler.HandleDownload` / `streamFileFromBlocks` | `GET /seafhttp/files/:token/*filepath` | Token-authorized full-file stream | `file` |
| `SeafHTTPHandler.HandleZipDownload` | `GET /seafhttp/zip/:token` | Token-authorized generated ZIP stream | `zip` |
| `SyncHandler.GetBlock` | `GET /seafhttp/repo/:repo_id/block/:block_id` | Authenticated block GET currently buffers `[]byte` | `block` |
| `FileViewHandler.ServeRawFile` | `GET /repo/:repo_id/raw/*filepath` | Authenticated raw stream; AV media uses `http.ServeContent` and Range | `raw` |
| `FileViewHandler.DownloadHistoricFile` | `GET /repo/:repo_id/history/download` | Authenticated historic full-file stream | `history` |
| `FileViewHandler.ServeHistoricFileRaw` | `GET /repo/:repo_id/history/raw` | Authenticated historic raw stream | `history` |
| `ShareLinkViewHandler.handleShareLinkRaw` | `/d/:token` or `/d/:token/files[/]` with `raw=1` | Public share raw stream; AV media may use Range | `link_raw` |
| `ShareLinkViewHandler.readFileContentAsText` | Share-file bootstrap for eligible text/Markdown files | Public storage read, up to the inline-content limit, currently with `context.Background()` | `link_inline` |

The share-link `dl=1` flow is not itself a byte producer. It mints a token and
redirects; the resulting `/seafhttp/files` request consumes the `file` profile.
The public ZIP-token flow similarly mints a token; `/seafhttp/zip` consumes the
`zip` profile.

OnlyOffice document retrieval ultimately uses
`GET /seafhttp/files/:token/*filepath`, so that final endpoint is the D
enforcement point. Editor HTML, configuration JSON and callbacks are not D
document-byte transfers.

`/history/view` and other redirect/bootstrap endpoints must be verified during
D4 to ensure they do not independently open a storage reader. Redirects and
control-plane JSON do not reserve a long-lived transfer slot.

Generated exports that do not read block/object storage are outside this D0
scope. They require a separate inventory before being described as protected by
D.

### 2. Non-Producers And HTTP Semantics

- No explicit `HEAD` route currently exists for these producers. D does not
  silently claim HEAD support. If HEAD is added later, it must authenticate and
  authorize normally, avoid payload reads, and charge zero payload bytes.
- `304`, `416`, invalid requests, redirects and permission failures must avoid
  opening a storage reader whenever the result can be decided before the read.
- Range support currently exists in the AV branches using `http.ServeContent`:
  `ServeRawFile` and `handleShareLinkRaw`. The admission must span the complete
  `ServeContent` operation, including Range/multi-range response writes.
- A response error after headers have been committed cannot be rewritten as a
  JSON 503. The stream must stop, cancel its readers and record the partial
  outcome.

### 3. Identities Are Separate Concepts

The implementation must never use one field for all three meanings:

| Concept | Meaning | D use |
|---|---|---|
| Principal user | `AccessToken.UserID` or authenticated sync user | Authorization, permission checks and encrypted-library session lookup | 
| Admission source | Stable public-link `SourceID` | Per-link capacity and remint-resistant fairness | 
| Traffic subject | Authenticated user or anonymous link traffic | Existing traffic/quota policy; not silently changed by D | 

`CreateLinkDownloadToken` currently accepts no `sourceID`, although migration
013 and the upload-token path already support `access_tokens.source_id`. D2
must add the missing download-token wiring.

For `Source == "link"`:

- New tokens require a non-empty, non-whitespace `SourceID`.
- All three public mint flows use
  `publicLinkSourceID("share-link", sl.token)`:
  normal download, public OnlyOffice and public ZIP token creation.
- A token remint retains the same source identity while changing the bearer.
- A link token with a blank source identity fails closed before protected bytes
  or inline content are produced.
- No fallback uses the temporary bearer, owner user ID, repo/path or a shared
  `unknown` key. Production is being deployed greenfield; no historical token
  compatibility path is required.
- `AccessToken.UserID` remains the share-link creator where existing
  authorization/decryption contracts require it. It is not replaced by a link
  hash or anonymous UUID.

The critical fairness invariant is:

> Public-link traffic never consumes the authenticated-user admission budget of
> the link owner.

D4 must saturate a public link and prove that an authenticated download by the
owner still reaches its own user admission, and the reverse isolation must also
hold.

### 4. Atomic Multidimensional Admission

D uses one coordinator for the shared node ceiling. Every request constructs all
applicable dimensions before waiting:

Authenticated transfer:

```text
node + authenticated-user (+ fixed profile dimension where configured)
```

Public-link transfer:

```text
node + link-source + client-link (+ fixed profile dimension where configured)
```

The exact operation is all-or-none:

```text
node.active < nodeMax
AND source.active < sourceMax
AND clientSource.active < clientSourceMax
```

No dimension is reserved while the request waits for another dimension. A
single bounded waiter entry represents the request. Cancellation, timeout and
release remove or release every participating dimension idempotently.

The coordinator must not hold its mutex while waiting, accessing Cassandra/S3
or writing a response. A waiter that cannot currently satisfy all dimensions
must not consume active capacity. Scheduling must avoid an exhausted identity
head-of-line blocking unrelated identities.

The shared node capacity is one invariant, not one independent node cap per
profile. A public transfer occupies both source dimensions for fairness but is
counted once in `node_active`.

### 5. Bounded Internal State

The coordinator is bounded by construction:

- Global maximum for active entries and waiters before creating identity state.
- Per-identity active and waiter limits.
- Entry removal when `active == 0 && waiters == 0` and no reservation remains.
- No timer or goroutine permanently allocated per identity.
- Cancelled waiters removed without a global cleanup scan.
- No raw bearer, client IP or source identity in Prometheus labels.
- Sustained sequential churn of tens of thousands of identities returns entries,
  waiters, timers and active counts to zero.

The D1 tests must cover both simultaneous cardinality and sequential churn. A
single 1,000-identity test is not sufficient evidence of a long-term state
bound.

### 6. Separate Profiles And Long-Lived Transfers

D does not reuse B/C defaults or their admitted-lifetime assumptions. PUT and
check-blocks are short-work paths with different scarce resources. Downloads
can occupy slots for minutes, and ZIP has additional traversal, compression and
metadata cost.

The profiles are separate for measurement and fairness:

- `block`: authenticated desktop block GET; must preserve desktop-compatible
  `503 + Retry-After` behavior.
- `file`: full-file and OnlyOffice file streams.
- `raw`: current authenticated raw streams and Range operations.
- `history`: historic file streams.
- `link_raw`: public link raw streams.
- `zip`: generated ZIP streams, with a separately measured route cap.
- `link_inline`: short public inline-content reads, with a short-work bound.

All profiles consume the shared node ceiling. Profile caps must not create
independent escape hatches that allow the aggregate node limit to be exceeded.
Initial capacities and wait policies are measurement outputs, not copied values
from B/C. The evidence must include transfer duration, memory, S3 readers,
connection occupancy, throughput, client behavior and slot drain.

For long streams, waiting is intentionally small or disabled unless real client
testing demonstrates that a queue is useful. A ten-second wait copied from B/C
is not automatically meaningful when a legitimate transfer may hold capacity
for minutes. The primary recovery control is write-progress timeout and context
cancellation, not a short absolute transfer lifetime.

### 7. Stable Public-Link Source Wiring

Migration 013 already adds nullable `source_id` to `access_tokens`, and the
database model, Cassandra scan and upload-token path already carry it. No new
migration is planned for D2.

D2 must update the download-token contract across:

- `TokenStore` and `TokenManager` interfaces/implementations;
- `CassandraTokenAdapter`;
- `v2` token-creator interfaces;
- normal download, public OnlyOffice and public ZIP callers;
- all affected mocks and tests.

The existing nullable scan behavior may remain as defensive schema handling, but
new link-token writers and consumers must be strict. No backfill or legacy
fallback is required for the clean deployment.

### 8. Gzip And Writer Reachability

The current gzip exclusion already covers the sync block path through:

```text
/seafhttp/repo/.*/block/.*
```

That protects both block GET and PUT. It also covers `/seafhttp/files` and
`/seafhttp/zip`.

The following current patterns are stale for the registered routes:

```text
/api/v2.1/.*raw/.*
/api/v2.1/.*history/.*
```

They do not cover `/repo/:repo_id/raw`, `/repo/:repo_id/history/download`, or
`/repo/:repo_id/history/raw`. The public raw response under `/d/:token` is also
not distinguishable by query through the path-regex API.

D3 will exclude the compatible `/repo/...` and `/d/...` byte-serving families.
Excluding all `/d/...` responses is intentional: redirects and small error/page
responses lose negligible compression, while query-based exclusion cannot
protect `raw=1` selectively.

Every writer that needs an idle-write deadline must make the underlying network
connection reachable through `http.ResponseController` or an equivalent
mechanism. Required writer capabilities include `Unwrap`, `Flush`, and any
`ReaderFrom`/server interfaces used by the streaming path. HTTP/1.1 is the
backend protocol in the supported nginx topology. Public HTTP/2 terminates at
the external proxy and requires end-to-end deployment validation rather than a
claim that the Go listener directly supports HTTP/2.

### 9. Write-Progress Lifetime

The deadline contract is:

> Install or refresh the idle-write deadline immediately before each write or
> flush toward the underlying writer.

It must not be refreshed after a write that may already be blocked. On success,
clear the deadline so a keep-alive connection does not inherit it. On timeout or
write failure:

- cancel the request/stream context;
- stop prefetch and close storage readers;
- release all D dimensions exactly once;
- do not append JSON after headers are committed.

The preparation wait deadline is cancelled or replaced once streaming starts; it
must not accidentally become a total download timeout. A configured stricter
server write timeout is not lengthened. If the D writer cannot install the
required deadline before headers, the contract is fail closed rather than a
metric-only degradation.

### 10. Block GET Refactor Contract

`CanonicalBlockReader.GetBlockSize` and `GetBlockReader` already exist. D5 will
wire them into `SyncHandler.GetBlock` instead of inventing a new storage reader.

The refactor must:

- acquire D before opening the canonical/S3 reader;
- obtain authoritative opaque block size before committing headers;
- use that size for quota projection and `Content-Length`;
- bind the reader to `c.Request.Context()`;
- stream bytes without decrypting, reinterpreting or reserializing them;
- preserve existing auth, permission and response states;
- deliberately move `last_accessed` from its current pre-body update to a
  post-body update after a complete successful block stream. This is a behavior
  change, not preservation: current code writes it before the response body is
  sent. It is safe for the clean deployment because no current reader uses
  `blocks.last_accessed` for retention or deletion; any future GC or cold-storage
  consumer must revisit the contract;
- preserve exact block-GET traffic accounting: `GetBlockSize` may drive the
  preflight quota decision and `Content-Length`, but the recorded transfer must
  use bytes successfully written. D5 must not regress the current exact
  `len(data)` accounting to nominal-size overbilling after replacing the buffer
  with a reader;
- record partial outcomes when a post-header reader/write fails;
- release admission after the response operation ends, not after reader creation.

The separate `ISSUE-BLOCK-CROSS-LIBRARY-READ-01` cross-library block-read
authorization finding remains separate. D must not claim that streaming or
admission fixes BOLA.

### 11. Traffic Accounting Boundary

D separates admission identity from traffic subject. It does not silently change
the product decision about whether anonymous link traffic consumes owner-level
quota or organization-level quota.

Existing paths differ in byte accounting: some record declared file size, some
record writer deltas, and some omit partial failures. D6 must document these
residuals and ensure tests do not claim exact delivered-byte accounting merely
because admission was added. The exception is the block GET path: D5 must not
introduce a regression from its current exact completed-block accounting to
nominal-size accounting. The broader `StreamBlocks` false-success and
over-counting issue is already tracked as `ISSUE-STREAMBLOCKS-VOID-01`.
A future traffic-accounting change beyond that D5 non-regression requires its
own explicit product and issue decision.

### 12. Direct Object Storage Is Separate

The Compose files currently execute `mc anonymous set download` for storage
buckets. A caller who knows a bucket/key can bypass application authentication,
quota checks, traffic recording and D admission.

This is `ISSUE-OBJECT-STORAGE-ANONYMOUS-DOWNLOAD-01`, a separate object-storage
exposure finding, not an undocumented part of D implementation. It remains a
production-posture blocker until clean deploy verification proves production
buckets are private and no direct signed/public download path bypasses the
application. D may be technically closed while this separate production
condition remains open; B4 production readiness must not be described as
enabled while that bypass is unresolved.

## Closure Criteria

D is not closed until all criteria below have evidence in code, focused tests
and the relevant integration/fault drill:

| # | Criterion | Required evidence |
|---|---|---|
| 1 | Complete producer inventory | Every storage-backed byte/inline-content producer above is covered; redirects and non-producers are explicitly tested or documented |
| 2 | Atomic admission | All applicable dimensions commit together or not at all; no partial slot reservation or identity contamination |
| 3 | One node ceiling | Files, raw, history, share raw, ZIP, inline text and block GET share one aggregate node bound |
| 4 | Fair identities | Authenticated user, link source and client-link limits are isolated; public link traffic cannot consume owner-user capacity |
| 5 | Bounded state | Active entries, waiters, identity maps, timers and goroutines remain bounded and drain to zero after sustained churn |
| 6 | Correct placement | Rejection happens before expensive metadata/S3 work; admission remains held through the protected response lifetime |
| 7 | Recoverable lifetime | Context cancellation, idle-write timeout, writer reachability, storage cancellation and idempotent release are proven |
| 8 | Correct HTTP behavior | 503/Retry-After contract, Range behavior, post-header failure, 304/416/redirect handling and byte integrity are preserved |
| 9 | Block GET safety | Block GET streams through the canonical reader with authoritative size and no full-block materialization |
| 10 | Middleware wiring | Actual gzip stack and real TCP tests cover block, files, ZIP, raw, history and `/d/...`; H2 is validated through supported proxy topology |
| 11 | Client recovery | `seaf-cli`, browser/download behavior and OnlyOffice retrieval recover or fail clearly under saturation |
| 12 | Configuration evidence | Defaults, ceilings and long-transfer capacities are measured, validated and documented as process-local |
| 13 | No false storage claim | MinIO/direct-object exposure remains separately tracked and is not silently treated as closed by D |

## D0-D6 PR Sequence

Each PR is independently reviewable, safe to stop after review and must not
claim a later PR's behavior.

| PR | Purpose | Runtime behavior |
|---|---|---|
| D0 | Contract, inventory, identity and evidence record | None; docs only |
| D1 | Neutral D coordinator, atomic dimensions, bounded state, config and metrics | Coordinator not yet connected to producers |
| D2 | Stable `SourceID` for all public download-token mint paths | New link tokens become strict; no legacy compatibility |
| D3 | Writer lifetime, idle-write deadline and actual gzip exclusions | Writer safety exercised before broad admission activation |
| D4 | Integrate file, ZIP, raw, history, share raw and inline text producers | All listed storage-backed producers use D |
| D5 | Stream sync block GET through existing canonical reader APIs | Block GET no longer materializes the block |
| D6 | Fault evidence, client recovery, measurements and final closure docs | Closure only after all criteria pass |

The B/C `syncAdmissionLimiter` remains in `internal/api` during this series.
Its white-box tests inspect unexported state, so extracting it to
`internal/admission` while claiming those tests remain byte-identical is not a
safe D change. A generic admission refactor may be proposed later as a
standalone change that moves the white-box tests with the implementation.

## Verification Plan

Focused verification belongs to the PR that makes each claim true. The final
series must run the relevant commands inside Compose, not directly on the
Windows host:

```bash
docker compose --profile test run --rm --build gotest
docker compose --profile test run --rm --build \
  gotest go test -race -count=1 \
  ./internal/api/... ./internal/api/v2/... ./internal/streaming/... ./internal/config/...
docker compose --profile test run --rm --build go-integration-test
docker compose --profile test run --rm --build sync-test
docker compose --profile test run --rm --build go-all-test
```

D1-D5 must add focused tests for their own invariants. D6 must additionally
prove real client recovery, cross-route saturation, public-link remint
fairness, identity churn, memory/goroutine/slot drain and byte-for-byte
integrity. A green unit suite without real middleware and storage evidence is
not sufficient to close D.
