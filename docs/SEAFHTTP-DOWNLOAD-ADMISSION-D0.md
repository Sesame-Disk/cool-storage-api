# B4 Subcontract D — Download Admission Contract

**Date:** 2026-08-01 (original contract freeze)  
**Last updated:** 2026-08-06
**Branch:** `feat/b4-subcontract-d6-closure`
**Status:** **D0-D6 are complete.** D1 supplied the coordinator, configuration
and metrics; D2 the stable public download-token `SourceID`; D3 the writer
lifetime and gzip/proxy reachability; D4 the non-block producers; D5 block GET
streaming through `CanonicalBlockReader`. D6 measured the per-admission cost,
selected and shipped the operating values, turned the section on, and produced
the client, proxy and saturation evidence the closure criteria require. See
**D6 Evidence** below for the measured figures and where each criterion is
demonstrated.

Two findings remain open and are explicitly **not** closed by D:
`ISSUE-OBJECT-STORAGE-ANONYMOUS-DOWNLOAD-01` (§13) and
`ISSUE-DOWNLOAD-BYTE-RATE-SHAPING-01`, now quantified rather than deferred.

This document freezes the contract and inventory for subcontract D of
`ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01`. It is the design record for the D1-D6
implementation series. Live status remains in
[`KNOWN_ISSUES.md`](./KNOWN_ISSUES.md); this document does not create a second
status tracker.

## Source Of Record

- [`KNOWN_ISSUES.md`](./KNOWN_ISSUES.md), `ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01`
- [`KNOWN_ISSUES.md`](./KNOWN_ISSUES.md),
  `ISSUE-OBJECT-STORAGE-ANONYMOUS-DOWNLOAD-01`
- [`KNOWN_ISSUES.md`](./KNOWN_ISSUES.md),
  `ISSUE-DOWNLOAD-BYTE-RATE-SHAPING-01` residual
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

## D1 Implementation Boundary

D1 supplies the process-local coordinator, structured identity constructors,
bounded waiter/identity state, configuration validation, fixed-label metrics and
unit/race-test coverage. It intentionally does not call the coordinator from a
download producer, mint or propagate public-link source IDs, install response
deadlines, or select positive capacity values. Those are D2-D6 deliverables.

The shipped YAML and `.env` examples are clean-deployment baselines: the
measured values are enabled in every shipped configuration and also live in
`DefaultConfig()`. A file that omits the section therefore inherits protection;
an explicit `download_admission.enabled: false` remains the only opt-out.

`Load()` overlays YAML on `DefaultConfig()` field by field, including when the
section is present. Omitted keys inside `download_admission` therefore retain
the D6 defaults. A present disabled section is checked for positive structural
values, including its configured memory budget, but its combined memory design
is evaluated only when enabled because it includes sync and preview settings
owned by other subsystems.

The refusal keys on **completeness**, not on a fixed shape: `enabled: false` is
supported when the effective values pass the section's structural rules if the
guard were switched on. An exact-shape test would stop matching after one
`DOWNLOAD_ADMISSION_*` override, and completeness is the rule the refusal
message states.
`server.write_timeout` is excluded, since it conflicts only with an active guard.
The check runs after environment overrides, so a deployment already repaired
through `DOWNLOAD_ADMISSION_*` is unaffected.

### D coordinator ownership

Bootstrap creates exactly one `Coordinator` and shares that pointer with every
protected producer, including `internal/api`, `internal/api/v2`,
`SyncHandler.GetBlock` and the share-link handlers. `New` deliberately does not enforce a process-global
singleton in D1 so unit tests can create isolated coordinators; multiple enabled
instances in production would multiply the process-local node cap and make the
global metrics meaningless.

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
| `SeafHTTPHandler.HandleZipDownload` | `GET /seafhttp/zip/:token` | Token-authorized generated ZIP stream, behind an optional request-start `zipRL` (`rate.Every(15s)`, burst 3) that is not an active-transfer bound | `zip` |
| `SyncHandler.GetBlock` | `GET /seafhttp/repo/:repo_id/block/:block_id` | Authenticated block GET; streams through the canonical reader since D5 (it buffered `[]byte` when this inventory was frozen) | `block` |
| `FileViewHandler.ServeRawFile` | `GET /repo/:repo_id/raw/*filepath` | Authenticated raw stream; AV media uses `http.ServeContent` and Range; the `preview=1` iWork branch fully buffers the source file (see §6) | `raw` |
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

`/history/view` is already resolved as a non-producer: `ViewHistoricFile` only
issues redirects — to `/repo/:repo_id/history/download` or to the frontend
preview URL — and never opens a storage reader. It needs a D4 regression that
keeps it that way, not a D4 investigation. Other redirect/bootstrap endpoints
must still be verified during D4. Redirects and control-plane JSON do not
reserve a long-lived transfer slot.

Two unused helpers would become bypasses if wired: `getFileFromBlocks`, which
loads every block of a file into one buffer, and `GetPresignedDownloadURL` in
`internal/storage`, which would hand a client a direct object-storage URL that
never reaches the application at all. Neither has callers, so neither is a
current bypass, and D does not inherit their cleanup. The frozen rule is
narrow:

> D4 must not introduce a call to either helper, and must leave a call-site
> regression or an explicit deprecation warning that says why.

Deleting them may land as separate work. `GetPresignedUploadURL` is not a
download producer and is out of D's scope entirely; if the presigned pair is
ever revisited it belongs with `ISSUE-OBJECT-STORAGE-ANONYMOUS-DOWNLOAD-01`,
which already owns direct object access.

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

`CreateLinkDownloadToken` now accepts `sourceID`; migration 013 and the
upload-token path already support `access_tokens.source_id`. D2 adds the
download-token wiring.

Admission keys are structured and namespaced; they are not untyped concatenated
strings:

```text
auth-user   = namespace + orgID + userID
link-source = namespace + SourceID
client-link = namespace + normalizedClientIP + SourceID
```

The coordinator should represent these as typed dimensions, for example
`DimensionKey{Kind, ID}`, so a user ID, source ID and IP with equal textual
representations cannot collide. `orgID` is mandatory in `auth-user`; two users
with the same user identifier in different organizations never share a gate.
`normalizedClientIP` must come from `c.ClientIP()` on an engine already
configured through `configureTrustedProxies` / `SetTrustedProxies`, never from a
directly parsed `X-Forwarded-For` value. There is no bespoke resolver to call:
this is exactly how the existing abuse controls attribute a client, and D1 must
use the same trusted-proxy configuration and attribution rules rather than
introducing a second one.

For `Source == "link"`:

The list below is the D2 contract and implementation result. Upload and public
download-token paths carry a source identity; admission itself remains deferred
to D4.

- New tokens require a non-empty, non-whitespace `SourceID`.
- All three public mint flows pass
  `publicLinkSourceID("share-link", sl.token)`:
  normal download, public OnlyOffice and public ZIP token creation.
- A token remint retains the same source identity while changing the bearer.
- A link token with a blank source identity fails closed before protected bytes
  are produced by the download consumers. Inline text does not necessarily use
  a download token: for
  `readFileContentAsText`, derive the admission source directly from the
  resolved share link with `publicLinkSourceID("share-link", sl.token)`.
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
node + auth-user (+ fixed profile dimension where configured)
```

Public-link transfer:

```text
node + link-source + client-link (+ fixed profile dimension where configured)
```

The exact operation is all-or-none over the request's **complete** dimension
set, not over the public-link subset alone:

```text
grant iff, for every d in dimensions(request): d.active < d.max

dimensions(authenticated) = { node, auth-user }    (+ profile where configured)
dimensions(public link)   = { node, link-source,
                              client-link }        (+ profile where configured)
```

An implementation that checks only the link dimensions silently drops
authenticated-user fairness, which is half of closure criterion 4.

No dimension is reserved while the request waits for another dimension. A
single bounded waiter entry represents the request. Cancellation, timeout and
release remove or release every participating dimension idempotently.

The coordinator must not hold its mutex while waiting, accessing Cassandra/S3
or writing a response. A waiter that cannot currently satisfy all dimensions
must not consume active capacity. Scheduling must avoid an exhausted identity
head-of-line blocking unrelated identities.

D1 uses a close-and-replace broadcast notification for parked waiters. This is
intentional: the bounded waiter set is rechecked as a whole so an exhausted
identity cannot block unrelated identities. It is not FIFO and new arrivals may
barge when capacity opens; the configured waiter ceiling bounds the resulting
thundering-herd work. D1 includes a contended acquire/release test; D6 should
benchmark the wake-up cost at the measured queue ceiling before selecting a
positive queue size.

The shared node capacity is one invariant, not one independent node cap per
profile. A public transfer occupies both source dimensions for fairness but is
counted once in `node_active`.

### 5. Bounded Internal State

The coordinator is bounded by construction:

- Global maxima on active transfers and on parked requests, checked before
  identity state is created. These two are the whole global envelope: because
  admission is atomic, D needs no separate pre-gate entry ring of its own.
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

- `block`: authenticated desktop block GET. It had no admission when this
  contract was frozen, so there was no behavior to preserve: D5 *adopts* the
  desktop-compatible
  `503 + Retry-After` contract already proven against real `seaf-cli` by
  subcontract B on the PUT side of the same route.
- `file`: full-file and OnlyOffice file streams.
- `raw`: current authenticated raw streams and Range operations.
- `history`: historic file streams.
- `link_raw`: public link raw streams.
- `zip`: generated ZIP streams, with a separately measured route cap.
- `link_inline`: short public inline-content reads. "Short work" here means the
  1 MB inline-content limit plus a low `max_active_link_inline`, not a deadline
  of its own — it shares the global `preparation_deadline`.

Two profiles have an admission lifetime that is easy to get wrong, so it is
stated rather than left to criterion 6.

`link_inline` is acquired in the outer bootstrap handler and held until the JSON
response carrying the inline content has been written or has failed. It is
**not** acquired and released inside `readFileContentAsText`. Releasing when the
storage read returns would free the slot while the content is still buffered in
memory, still unserialised and still unwritten — so unbounded inline responses
could accumulate after admission was given back, which is the opposite of what
putting this producer in scope was for. The preparation deadline covers the
storage read; the idle-write deadline covers the response write; both run under
one admission.

A **single global `preparation_deadline` is frozen here**, and the configuration
schema in §12 has no per-profile deadline keys. The residual tension is
acknowledged rather than left implicit: a profile cap bounds how many requests
of a kind run concurrently, not how long each may sit in preparation, so a
deadline sized for a large ZIP's traversal is inherited unchanged by a 1 MB
inline text read.

The verification belongs to **D4 and D6, not D1**. D1 ships the coordinator
without connecting it to any producer, so it has no ZIP traversal or inline read
to time; a measurement requirement placed there could not be met. D4 wires the
producers and D6 measures whether one finite deadline genuinely serves both ends
of the range. If it does not, the contract and the configuration schema change
before D closes — adding per-profile deadline keys is a contract amendment with
evidence, not an implementation detail D1 may improvise.

`zip` is held until `zipWriter.Close()` returns, because that close is what
flushes the central directory: releasing earlier returns the slot while the
response is still being written. Acquiring before `zip.NewWriter` is necessary
but not sufficient — what matters is the `defer` registration order, which is
what actually decides the unwind. The frozen shape is one cleanup that owns
both, so the order cannot be broken by a later edit:

```go
lease, ok := admission.Acquire(...)   // before zip.NewWriter
if !ok {
    return
}
zipWriter := zip.NewWriter(c.Writer)
defer func() {
    defer lease.Release()             // runs even if Close panics

    if err := zipWriter.Close(); err != nil {
        // record the close/response failure — see below
    }
}()
```

The inner `defer` is not a stylistic choice. A release that runs as a plain
statement after `zipWriter.Close()` is skipped entirely if that close panics,
which would strand a node slot for the life of the process — and D lists `panic`
as a release `cause`, so the frozen pattern has to survive one. Two separate
defers are equally acceptable, provided `lease.Release()` is registered
immediately after the acquire and the close defer immediately after
`zip.NewWriter`: LIFO then closes before releasing, and a panic in the close
still unwinds into the earlier release. Either shape must also let the release
path attribute a panic unwind, otherwise `cause="panic"` is a label that can
never be emitted.

The current handler uses a bare `defer zipWriter.Close()`, which discards the
error, so a failed central-directory flush is invisible today. D4 must record
it: a ZIP that fails to close is a truncated archive the client may accept as
complete. The regression must prove both the ordering and that the close error
is recorded.

`raw` contains a third case, and it is the worst memory profile in D. The
`preview=1` iWork branch of `ServeRawFile` is not a stream: it opens its own
canonical reader, copies **every block of the source file** into a single
`bytes.Buffer` — reading each block fully into memory first when the library is
encrypted, so decryption can run — and only then parses the assembled document
as a ZIP to extract a preview. Admission must therefore be taken before the
first reader is opened and held until the extracted PDF/JPEG/PNG has been
written, exactly as for the streaming branches.

The source size **is** already gated. `ServeRawFile` rejects with `413` when
`fileSize > getMaxFileSizeForPreview(ext)`, and that check runs before the block
store is resolved and before the buffering loop. The problem is the value, not
its absence: an iWork file is neither video nor text, so it falls through to the
general `FileView.MaxPreviewBytes`, which is **1 GiB** in every shipped config —
a general preview limit, not an iWork-specific in-memory budget, and never
validated as one for a path that materialises the whole file.
`MaxIWorkPreviewBytes` (50 MB) caps only the *extracted* preview, twenty times
lower, and is consulted after the source is already in memory.

So `raw` is bounded, but at a value that makes a small `max_active_raw` the only
thing standing between the node and multiple gigabytes of buffered previews. The
per-request peak is more than the source file:

```text
peak_iwork_request ≈ source buffer capacity        (may exceed fileSize;
                                                    bytes.Buffer grows in steps)
                   + current encrypted block
                   + current decrypted block
                   + extracted preview             (≤ MaxIWorkPreviewBytes)
                   + ZIP parsing and runtime overhead
```

iWork runs under the `raw` profile, so while that holds the node budget is:

```text
iWork node memory budget ≈ max_active_raw × measured_peak_iwork_request
```

D6 must measure that peak rather than deriving it from `fileSize`, and the
shipped `max_active_raw` and `MaxPreviewBytes` defaults must together satisfy a
stated memory budget. If 1 GiB stays, an iWork-specific source cap under
`FileView` — well below the general preview limit — is the cheaper lever than
shrinking `max_active_raw` for every raw stream. That is a `FileView` key and
does not touch the `download_admission` schema frozen in §12.

Whether `raw` should keep carrying both is a real question, because it mixes
O(one block) streams with a fully buffered producer, and one cap sized for iWork
would throttle ordinary raw streaming far below what it needs. But splitting is
**not** an implementation choice available to D4. §12 freezes a closed profile
enum, flat `max_active_<profile>` keys, one environment variable per key, and
the `profile` metric label set; there is no `iwork_preview` in any of them, and
adding one silently would break the sets this document exists to fix. So:

> If D4/D6 evidence shows the shared `raw` cap either leaves iWork memory unsafe
> or needlessly throttles ordinary raw streams, D4 must amend this contract, the
> configuration schema, the environment variables, the profile enum and the
> metric label set **before** introducing an `iwork_preview` profile or sub-cap.

Freezing `iwork_preview` now would be premature in the other direction: there
are no measurements yet to size it. It stays in `raw`, and the split is an
explicit amendment if evidence calls for it.

Placement is part of this. The order D4 must implement and regress is:

```text
authenticate and authorize
→ resolve fileSize (bounded metadata lookups)
→ apply the 413 preview-size gate
→ acquire D
→ resolve block store and canonical reader
→ buffer / stream
→ write the response
→ release D
```

The gate preceding admission is not licence to do more before acquiring D: the
metadata reads above are the bounded lookups needed to decide the `413` itself,
and D4 must not add traversals, storage reads or other expensive work ahead of
the acquire. An ordering regression that moved the `413` after the buffering
loop would remove the only source bound there is.

All profiles consume the shared node ceiling. Profile caps must not create
independent escape hatches that allow the aggregate node limit to be exceeded.
Initial capacities and wait policies are measurement outputs, not copied values
from B/C. The evidence must include transfer duration, memory, S3 readers,
connection occupancy, throughput, client behavior and slot drain.

For long streams, waiting is intentionally small or disabled unless real client
testing demonstrates that a queue is useful. A ten-second wait copied from B/C
is not automatically meaningful when a legitimate transfer may hold capacity
for minutes. The primary recovery control is write-progress timeout and context
cancellation, not a short absolute transfer lifetime. D6 must measure byte
throughput against the node's egress budget; concurrency alone is not a byte-rate
shaper. Residual shaping work is tracked separately as
`ISSUE-DOWNLOAD-BYTE-RATE-SHAPING-01`.

A refused request answers `503` with `Retry-After` on **every** profile, not
only `block`. The desktop client classifies 502/503/504 as retryable network
errors and has no 429 handling, and a single coordinator that answered
differently per surface would make the refusal contract depend on which URL a
client happened to use. 429 remains reserved for the existing non-blocking
upload-link limiter, which serves browser traffic under a different contract. D
does not claim that browsers or OnlyOffice retry automatically on 503: the
status communicates transient unavailability, and real client behavior is
closure criterion 11 evidence rather than an assumption.

### 7. Stable Public-Link Source Wiring

Migration 013 already adds nullable `source_id` to `access_tokens`, and the
database model, Cassandra scan and upload-token path already carry it. No new
migration is planned for D2.

D2 updates the download-token contract across:

- `TokenStore` and `TokenManager` interfaces/implementations;
- `CassandraTokenAdapter`;
- `v2` token-creator interfaces;
- normal download, public OnlyOffice and public ZIP callers;
- all affected mocks and tests.

The existing nullable scan behavior may remain as defensive schema handling, but
new link-token writers and consumers must be strict. No backfill or legacy
fallback is required for the clean deployment. D2 is not a mixed-version rolling
deployment contract: the clean release deploys all token writers and consumers
as one coordinated version, and disposable pre-release tokens may be invalidated.
No production data or token continuity is being preserved.

### 8. Gzip And Writer Reachability

The current gzip exclusion already covers the sync block path through:

```text
/seafhttp/repo/.*/block/.*
```

That protects both block GET and PUT. It also covers `/seafhttp/files` and
`/seafhttp/zip`.

The old `/api/v2.1/.*raw/.*` and `/api/v2.1/.*history/.*` patterns were stale for
the registered routes. D3 replaces them with exclusions for the actual `/repo`
raw/history paths, the blanket `/d/...` public stream prefix, and both public
inline bootstrap routes:

```text
^/repo/[^/]+/raw(?:/.*)?$
^/repo/[^/]+/history/(?:download|raw)/?$
^/d/.*$
^/api/v2\.1/share-links/[^/]+/(?:files/)?bootstrap/?$
```

The `/d/...` exclusion is safe because the route serves only redirects, short
errors and the public raw stream. The bootstrap exclusion is explicit because
those responses can contain compressible inline content; D6 must measure the
egress tradeoff before selecting production capacities. The Go writer tests
also assert that the old patterns cannot silently return.

**A blanket `/d/...` exclusion is an acceptable final design, not a temporary
compromise.** Under the SPA-API-only architecture those two routes serve almost
nothing else: `ServeShareLinkPage` and `ServeShareLinkFilePage` answer only
`dl=1` (mint plus redirect) and `raw=1` (the protected byte stream), and every
other query returns a short `404` JSON. There is no large compressible response
under `/d/...` to protect, so excluding the prefix costs approximately nothing
and removes the need for query-aware gzip logic on the share-raw path. D3 uses
this blanket exclusion; D6 still measures the effective egress before selecting
production capacities.

The compressible public bootstrap responses live on **different routes**:

```text
/api/v2.1/share-links/:token/bootstrap
/api/v2.1/share-links/:token/files/bootstrap
```

These are where the `link_inline` producer's response is written. D3 excludes
both routes from the current Go gzip wrapper and adds matching
`proxy_buffering off` and `gzip off` locations in the supported frontend nginx
topology. This removes the writer boundary that made subcontract C's admitted
lifetime unenforceable:

```text
admission acquired
→ storage read completes
→ response write begins
→ idle-write deadline cannot reach the socket
→ a slow client holds the slot indefinitely
```

D3 uses outright exclusion for these two routes. The supported frontend nginx
configuration also disables both proxy buffering and gzip for every D transfer
location: raw/history, share raw, public bootstrap and `/seafhttp/`. Browser
backpressure is therefore not hidden by buffering or response compression in
that proxy. These responses can carry up to the full inline-content limit of
highly compressible text, so the egress cost remains a D6 measurement, not a
claim that compression is free.

Every writer that needs an idle-write deadline must make the underlying network
connection reachable through `http.ResponseController` or an equivalent
mechanism. Required writer capabilities include `Unwrap` and `Flush`. The D
wrapper must **not** expose `ReaderFrom` unless it implements its own chunked
`ReadFrom` that renews deadlines, counts bytes and propagates cancellation; a
transparent delegation to an underlying `ReaderFrom` is forbidden. The same
rule applies to a source `WriterTo` fast path: `io.Copy` must not bypass the D
wrapper's accounting and lifetime hooks. HTTP/1.1 is the backend protocol in the
supported nginx topology. Public HTTP/2 terminates at the external proxy and
requires end-to-end deployment validation rather than a claim that the Go
listener directly supports HTTP/2.

The proxy is part of the lifetime contract. D3/D6 must verify the effective
configuration for every protected route, including `proxy_buffering`, gzip or
another response transformation, buffering-to-disk behavior,
`proxy_read_timeout` and `send_timeout`.

**The proxy's timers must strictly dominate every deadline the application will
accept.** Disabling buffering is necessary but not sufficient: if nginx's own
timers fire first, the transfer dies on nginx's clock, an operator's configured
tolerance silently does nothing, and the release is misattributed to a client
disconnect instead of the idle-write timeout. Two floors, because the phases are
silent in opposite directions:

```text
proxy_read_timeout > max preparation_deadline + max idle_write_timeout
send_timeout       > 2 x max idle_write_timeout
```

The read floor spans both silent phases, not just preparation: the first storage
read sits *after* `StartStreaming` and is bounded by the idle-write deadline, so
nothing reaches nginx during it either. Summing only preparation would leave the
phase D6 added in the pre-first-write fix uncovered.

The idle floor is doubled because progress restarts the interval, so the span
from the streaming phase change to the first deadline can approach twice the
configured value.

Both apply **per protected location**, not at server level. Raising every route's
connection-retention window would trade one abuse vector for another in the PR
that closes B4, so non-D routes keep a short `send_timeout` and the D locations
opt in. `config.MinNginxProxyReadTimeout` and `config.MinNginxSendTimeout`
derive both from the validation ceilings, and
`TestSupportedNginxTimeoutsNeverPreemptDownloadAdmission` asserts the
relationship rather than a literal — a literal is what previously froze a
mismatch in place, with nginx cutting a stalled client at 120s while validation
accepted `idle_write_timeout` up to 15m.

Note that `idle_write_timeout` bounds the interval **without progress**, not the
transfer. A multi-hour download of a very large file is unaffected by a 60s
setting as long as bytes keep flowing; what the setting tolerates is a stalled
peer or a stalled object-store fetch between blocks. The supported transfer path must use
backpressure-compatible settings (currently `proxy_buffering off` and
`gzip off` in the supported frontend transfer locations) or document that D
protects only the Go-to-proxy hop. D3's configuration regression and nginx
syntax check protect that setting; D6 must run a slow-client test through the
real nginx topology as well as directly against Go. A backend TCP test alone
does not prove that the browser/client is controlling admission lifetime.

### 9. Write-Progress Lifetime

The deadline contract is:

> The idle interval opens when the request **enters streaming**, not when it
> first produces output: `StartStreaming` installs a deadline at
> `now + idle_write_timeout`. Each successful output progress sets the next
> absolute deadline at `progress time + idle_write_timeout`; a later write or
> flush installs that same deadline rather than extending it.

Opening the interval at the phase change rather than at the first byte is a
correction to the original D3 wording, made after `SyncHandler.GetBlock` showed
what "before the first actual write" left uncovered: preparation is already over
at that point, so a first storage read that never returns had no deadline of any
kind. See `ISSUE-DOWNLOAD-ADMISSION-PRE-FIRST-WRITE-GAP-01`.

Two consequences follow, and both are contract, not implementation detail:

- **A deferred header preserves the interval.** Gin records a status without
  committing it, so `c.Status(200)` reaches the writer with nothing written. That
  is not progress and must not extend the interval, but it must not clear it
  either — clearing is what would reopen the gap immediately after arming. Before
  the interval is opened, a deferred header still leaves no timer.
- **The total span may approach `2 × idle_write_timeout`** when the first byte
  arrives just before the initial interval expires, because progress legitimately
  restarts the interval from the progress instant. What is forbidden is the
  initial callback surviving that progress, two intervals stacking, or a deferred
  status resetting the clock.

Beginning a later write must not grant a second full idle period, and a deferred
status assignment that has not committed headers must not start the idle timer.
After successful output, clear the socket deadline while the progress timer owns
the interval so a keep-alive connection does not inherit it. On timeout or write
failure:

- cancel the request/stream context;
- stop prefetch and close storage readers;
- release all D dimensions exactly once;
- do not append JSON after headers are committed.

Three deadlines are independent:

1. **Admission wait:** time allowed to obtain all dimensions.
2. **Preparation deadline:** time allowed after admission for metadata, mappings,
   canonical-reader construction and initial storage work, including inline
   text. Its context reaches Cassandra and S3.
3. **Idle-write timeout:** maximum interval without successful response
   progress, opened when the request enters streaming rather than when output
   starts, so the first storage read is covered too. After the first progress the
   maximum is measured from the last successful progress, not from the start of a
   subsequent write. A transfer that dies before committing any output answers
   `503` with `Retry-After` instead of Gin's default `200`.

The preparation context is cancelled or replaced when streaming starts; it must
not accidentally become a total download timeout. `readFileContentAsText` keeps
the preparation deadline because it may never write a response while waiting for
storage.

`server.write_timeout` is a process-wide `http.Server` setting, not a per-route
one, and all seven shipped configurations already set it to `0s` — two of them
explaining it as "No write timeout — large downloads/zips can take minutes",
`config.prod.yaml` with a similar note, and the four regional files without a
comment. D therefore depends on an existing deployment property rather than
introducing a new constraint:
with it at zero, the application-controlled idle deadline is the only write
deadline on the connection and is authoritative by construction.

`http.Server` does not expose the deadline it installed, so D cannot refresh a
non-zero `write_timeout` without risking extending it. A non-zero global
`server.write_timeout` is therefore unsupported while D is enabled, unless a
future implementation derives the absolute deadline from configuration plus
request start and always applies `min(absoluteDeadline, now + idleTimeout)` —
computable, but deliberately outside this contract. Since D declares a non-zero
value unsupported, D3 makes startup **fail** when `download_admission.enabled`
is true and `server.write_timeout != 0`. A warning or a regression test does not
prevent an invalid production configuration from running; a refusal to start
does.

If the D writer cannot install the required deadline before headers, the contract
is fail closed rather than a metric-only degradation. D3 now supplies
`httputil.IdleWriteWriter`, which probes reachability before headers, requires a
non-nil cancellation callback, uses the last successful progress's absolute
deadline before subsequent writes and flushes, rejects short writes, invalidates
stale timer callbacks, cancels after an idle-progress timeout, and clears the
deadline in `Finish()`. A plain
`httptest.ResponseRecorder` cannot expose the connection deadline and is
therefore intentionally rejected; D4 producer tests must use a connection-
capable writer or a real server. D4 supplies the admission lease callbacks and
installs this writer around each protected producer.

### 10. Block GET Refactor Contract

`CanonicalBlockReader.GetBlockSize` and `GetBlockReader` already existed. D5
wired them into `SyncHandler.GetBlock` instead of inventing a new storage reader.

The refactor must:

- acquire D before opening the canonical/S3 reader;
- obtain authoritative opaque block size before committing headers;
- use that size for quota projection and `Content-Length`;
- bind the reader to `c.Request.Context()`;
- stream bytes without decrypting, reinterpreting or reserializing them;
- preserve existing auth, permission and response states;
- preserve the current `last_accessed` placement and semantics during D5. The
  current handler writes it after the quota pre-check and before the response
  body is sent. Redefining it as post-complete-delivery is outside D5; if that
  semantic change is wanted later, it needs its own issue and evidence. No
  current reader uses `blocks.last_accessed` for retention or deletion, but any
  future GC or cold-storage consumer must still treat its current meaning as
  part of the contract;
- preserve exact block-GET traffic accounting: `GetBlockSize` may drive the
  preflight quota decision and `Content-Length`, but the recorded transfer must
  use bytes successfully written. D5 must not regress the current exact
  `len(data)` accounting to nominal-size overbilling after replacing the buffer
  with a reader;
- record partial outcomes when a post-header reader/write fails. A reader that
  reaches EOF **early** is one of those outcomes even though `io.Copy` reports no
  error: D5 compares the bytes copied against the authoritative size and releases
  a short block as `storage_error` rather than `completed`. Content addressing
  makes that divergence extraordinary, which is the reason to assert it rather
  than assume it — an unasserted structural expectation is how a metadata/object
  mismatch would reach a client as a silently truncated block;
- release admission after the response operation ends, not after reader creation.

The separate `ISSUE-BLOCK-CROSS-LIBRARY-READ-01` cross-library block-read
authorization finding remains separate. D must not claim that streaming or
admission fixes BOLA.

#### D5 depends on sync authentication, and does not fix it

D5 builds its admission identity with `NewAuthenticatedRequest(ProfileBlock,
orgID, userID)` because the sync surface has no legitimate public flow: every
byte producer reachable under `/seafhttp/repo/:repo_id/*` is authenticated by
design. That is a **dependency on `syncAuthMiddleware`, not a property D5
proves**.

`ISSUE-SYNC-LINK-TOKEN-AUTH-01` is open: the middleware accepts any valid
`TokenTypeDownload` without rejecting `Source == "link"`, without requiring the
repository-root token shape, and without binding the token's `RepoID` to the
route's `:repo_id`. While it is open, a public share-link bearer that reaches
this route is admitted as the link creator's authenticated identity, so criterion
4's fairness invariant is **not** end-to-end proven for block GET.

This is not a D5 regression and does not gate D5, because the same middleware
already supplies the identities that subcontract B (block PUT) and subcontract C
(check-blocks) key their admissions on, and both were closed on that same basis.
Gating D5 alone on it would be inconsistent; the fix belongs in the middleware,
where it repairs all three at once. What D must not do is present the
authenticated-identity classification as verified end to end while that issue is
open.

### 11. Traffic Accounting Boundary

D separates admission identity from traffic subject. It does not silently change
the product decision about whether anonymous link traffic consumes owner-level
quota or organization-level quota.

Existing paths differ in byte accounting: some record declared file size, some
record writer deltas, and some omit partial failures. D6 must document these
residuals and ensure tests do not claim exact delivered-byte accounting merely
because admission was added.

**D6 residual survey.** Every streaming producer now records a writer delta
rather than a declared size, so a partial transfer bills what reached the client:
`streamFileFromBlocks` and the ZIP path through `recordSeafHTTPDownloadTrafficFn`,
`SyncHandler.GetBlock` through `recordSyncBlockDownloadTrafficFn`, and the
`FileView` raw/historic and share-raw paths through their own `sent` counters.
One residual remains and is **not** closed by D:

> `link_inline` is admitted but not billed. `readFileContentAsText` and the
> bootstrap responses that carry its output do not call
> `traffic.RecordCheckedTransfer`, so inline content — up to the 1 MB limit per
> request — passes through admission without appearing in traffic accounting.

That is a product decision about whether anonymous inline reads consume owner or
organization quota, not a D correctness gap, and §11 reserves it: changing it
requires its own explicit product and issue decision. It is recorded here so no
one later reads "every D producer is admitted" as "every D producer is billed".
The block GET path is explicitly protected from regressions: D5 must not change
its exact completed-block accounting to nominal-size accounting. The related
`StreamBlocks` false-success and over-counting issue was fixed on 2026-08-03.
A future traffic-accounting change beyond that D5 non-regression requires its
own explicit product and issue decision.

### 12. Configuration And Metrics

D1 ships configuration and metrics. This section is what they must satisfy, so
the shape is decided here rather than improvised in the implementation PR.

**Configuration.** The coordinator spans `internal/api`, `internal/api/v2` and
the share-link handlers, so its keys belong in their own top-level section
rather than under `seafhttp`, which is route-scoped. The section is
`download_admission`, the environment prefix is `DOWNLOAD_ADMISSION_`, and the
shape is frozen here so `applyEnvOverrides()` has something to implement:

```yaml
download_admission:
  # Auto-sized D6 defaults. The budget uses memory_budget_percent (25) of the cgroup limit when exposed;
  # the 2 GiB fallback is used when no container limit is available.
  # Set enabled: false explicitly only when admission is intentionally disabled.
  enabled: true
  capacity_mode: auto
  memory_budget_percent: 25
  raw_capacity_percent: 33
  safety_margin_percent: 20
  # memory_budget_bytes: 2147483648 # optional fallback/override
  max_active_per_node: 16 # derived; 4 raw + 12 stream slots at the 2 GiB fallback
  max_active_per_auth_user: 6 # derived fairness cap
  max_active_per_link_source: 6 # derived fairness cap
  max_active_per_client_link: 3 # derived fairness cap
  max_waiters_per_identity: 4
  max_waiters_per_node: 24
  admission_wait: 2s
  preparation_deadline: 60s
  idle_write_timeout: 60s
  retry_after: 10s
  # Per-profile caps are flat, explicit keys — not a YAML map.
  max_active_block: 12
  max_active_file: 12
  max_active_raw: 4
  max_active_history: 6
  max_active_link_raw: 12
  max_active_zip: 4
  max_active_link_inline: 8
```

Values above are the measured D6 values for the clean 8 GiB-container baseline.
The budget and caps must be resized together for smaller containers. The
per-profile caps are **flat keys
rather than a map** on purpose: the profile set is a fixed,
closed enum, and a map cannot be overridden per entry by an environment variable
without inventing JSON-in-env. Each maps to
`DOWNLOAD_ADMISSION_MAX_ACTIVE_ZIP` and so on, one variable per key.

Zero does not mean the same thing everywhere, and a uniform "0 disables it"
rule would let a configuration claim D while disabling the ceiling D exists to
enforce. When `enabled: true`, these must be greater than zero and startup fails
otherwise:

```text
max_active_per_node
max_active_per_auth_user
max_active_per_link_source
max_active_per_client_link
preparation_deadline
idle_write_timeout
retry_after
```

These may legitimately be zero:

```text
admission_wait      -> refuse immediately instead of queueing
max_waiters_*       -> no queue at all
max_active_<profile> -> no additional cap for that profile
```

Remaining validation, in the shape B and C already use, enforced rather than
commented:

- no identity cap and no profile cap may exceed `max_active_per_node`, so a
  profile cannot become an escape hatch around the aggregate bound;
- `max_active_per_client_link` may not exceed `max_active_per_link_source`;
- `max_waiters_per_identity` may not exceed `max_waiters_per_node`. The global
  bound would dominate anyway, so this is not a safety hole — it rejects a
  configuration whose per-identity queue can never be reached, and keeps the
  waiter keys under the same rigor as the active caps;
- a validated ceiling exists for every cap and every deadline;
- the coordinator is process-local, and every default is documented as such:
  fleet capacity scales with node count.

`retry_after` is a key of its own rather than being derived from
`admission_wait` the way B and C derive it. Their derivation works because a
refused `PutBlock` slot frees on the timescale of the wait; a download slot does
not. With a short or disabled queue — which §6 expects for long streams —
`ceil(admission_wait)` would tell every refused client to come back in one
second while transfers hold capacity for minutes, turning the refusal into a
retry storm. The floor is one second, since `Retry-After` has second
granularity and zero means "retry now". The header is serialised as
`ceil(retry_after / 1s)`, minimum `1`, so a sub-second or fractional value such
as `1500ms` becomes `2` rather than being truncated to `1`.

Every `configs/*.yaml`, `.env.example`, `.env.prod.example` and
`applyEnvOverrides()` must carry the new keys. A key that exists in the struct
but in only some config files is a defect B and C both had to fix.

**Metrics.** Naming follows the existing admission series
(`sync_check_blocks_*`), with `download_admission_` as the prefix:

| Series | Type | Labels |
|---|---|---|
| `download_admission_active_current` | Gauge | none — the node total |
| `download_admission_active_by_profile` | Gauge | `profile`, fixed exclusive enum |
| `download_admission_entries_current` | Gauge | none — requests inside admission: active plus parked |
| `download_admission_waiters_current` | Gauge | none — **unique** requests currently parked |
| `download_admission_waiters_by_gate` | Gauge | `gate` — which gates those requests were blocked on at their last coordinator reevaluation |
| `download_admission_tracked_identities` | Gauge | `dimension` — identity gates currently materialised |
| `download_admission_rejected_total` | Counter | `reason`, fixed set |
| `download_admission_rejected_by_profile_total` | Counter | `profile`, `reason`, both fixed sets; attribution companion for drills and alerts |
| `download_admission_released_total` | Counter | `cause`, fixed set |
| `download_admission_deadline_expired_total` | Counter | `phase` = `preparation` or `idle_write` |
| `download_admission_writer_unreachable_total` | Counter | none — deadline could not be installed |
| `download_admission_wait_seconds` | Histogram | `outcome` |
| `download_admission_occupancy` | Histogram | `dimension` |

Waiters need two series for the same reason active does. A parked public request
blocks on `link_source` and `client_link` at once, so a per-gate gauge cannot
answer "how many requests are queued" — which is precisely what
`max_waiters_per_node` bounds. `waiters_current` is that unlabelled count;
`waiters_by_gate` shows which gates they were blocked behind at the latest
reevaluation, and its label set covers `node` and `profile` too, because a
request can be parked on the node ceiling or a profile cap with every identity
gate free. The series is intentionally an observation of the coordinator's last
reevaluation rather than a scrape-time scan of every waiter; this keeps metrics
out of the O(waiters) admission hot path. Summing
`waiters_by_gate` against `waiters_current` is the same double-count error as
summing identity dimensions against the node total: one parked request can be
blocked on several gates at once.

"Fixed set" means enumerated here, not decided in D1, because these values end
up in dashboards and alert rules:

```text
dimension: auth_user | link_source | client_link
gate:      node | profile | auth_user | link_source | client_link
profile:   block | file | raw | history | link_raw | zip | link_inline
phase:     preparation | idle_write
outcome:   admitted | refused | timeout | cancelled

reason:    node_full | profile_full
           | auth_user_full | link_source_full | client_link_full
           | node_queue_full
           | auth_user_queue_full | link_source_queue_full
           | client_link_queue_full
           | admission_timeout | client_gone

cause:     completed | client_disconnect | preparation_timeout
           | idle_write_timeout | storage_error | response_error | panic
```

An impossible package-level `AdmissionRequest` returns `invalid_request` to its
caller but is outside this capacity-reason metric set and does not increment
`download_admission_rejected_total`. The public constructors reject those
inputs before a producer can call the coordinator; this path exists only as a
defensive package boundary.

`reason` names the identity dimension that refused the request rather than
collapsing all three into one `identity_full`. `waiters_by_gate` already
distinguishes `auth_user`, `link_source` and `client_link`, so a collapsed
reject reason would let an operator see which gate requests are parked behind
but not which one is turning them away — and those are different questions with
different responses, since a saturated `client_link` suggests one abusive
client while a saturated `link_source` suggests one hot link.

There is deliberately **no** `entry_queue_full`, and no `max_entries_per_node`
to go with it. B/C carry a global entry ring because they acquire gates
sequentially: a request can hold its per-user slot while it has not yet reached
the node gate, a transitional state that is neither active nor parked, so
without the ring it would be unaccounted. Its capacity there is exactly
`maxPerNode + maxWaitersPerNode` — not an independent third budget. D admits
atomically and reserves nothing while waiting (§4), so that transitional state
does not exist and a third ring would be mechanism copied from B/C into a design
that removed the need for it. When there is no room, the reason is a full node
or a full queue. The identity is therefore frozen:

```text
download_admission_entries_current == active_current + waiters_current
```

with the same snapshot caveat as the profile invariant below.

`cause` must account for every way a slot disappears, so a release that is not
`completed` is visible rather than looking like ordinary traffic. `client_gone`
stays inside `rejected_total` rather than becoming a separate series, keeping
B/C's vocabulary, but it is explicitly **not** a capacity signal: counting
abandoned requests as capacity rejections would make the metric read as overload
during ordinary client churn, so any saturation alert must exclude it.

The occupancy invariant is:

```text
download_admission_active_current == sum(download_admission_active_by_profile)
```

It must **not** be written as a sum over identity dimensions. A public transfer
occupies `link_source` and `client_link` simultaneously, so summing those
against the node gauge double-counts every public byte and would make a healthy
node look over-subscribed. That is the whole reason `active_by_profile` exists
as a separate, mutually exclusive series.

The invariant holds over internal coordinator state and over stable test
snapshots. It is **not** promised for every concurrent Prometheus scrape:
independent gauges are gathered one at a time, so an admission or release
between two reads can make a single scrape disagree by a small amount even when
the coordinator updates both under one lock. D1 either documents that and no
alert rule is written on strict equality, or it registers a custom collector
that captures the node total and the per-profile values in one snapshot. Either
is acceptable; leaving it unstated is not, because the obvious alert rule would
then fire on healthy nodes.

No label value may carry a bearer token, client IP, user, organization,
repository or source identity. `tracked_identities` is how identity growth is
observed — a bounded count per dimension, never one series per identity — and it
is the metric that makes the criterion 5 churn evidence readable in production
rather than only in tests.

### 13. Direct Object Storage Is Separate

The Compose files currently execute `mc anonymous set download` for storage
buckets. A caller who knows a bucket/key can bypass application authentication,
quota checks, traffic recording and D admission.

This is `ISSUE-OBJECT-STORAGE-ANONYMOUS-DOWNLOAD-01`, a separate object-storage
exposure finding, not an undocumented part of D implementation. Once D closes,
`ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01` and its B4 umbrella may be marked closed;
the object-storage issue remains independently open. The overall production
verdict stays no-go while the object-storage issue is open, and production
readiness must not be described as enabled while that bypass is unresolved.

## D6 Evidence

### Measured per-admission cost

Auto mode divides the effective process-local design budget by measured cost, not
by estimate. The default 2 GiB fallback applies a 20% safety margin and derives
4 raw slots plus 12 other stream slots:

```text
4 x 192 MiB + 12 x 72 MiB = 1632 MiB (~1.59 GiB)
```

Both component benchmarks are in the tree so the figures can be re-derived.
`Load()` derives the budget from `memory_budget_percent` of an exposed cgroup
limit — 25% by default, capped at 50% — uses the 2 GiB fallback otherwise, and
rejects an explicit budget above that same share, so the guard and the setting
cannot disagree.
The budget is a design validator, not an OS memory reservation.

| Producer shape | Measured peak per admission | Benchmark |
|---|---|---|
| Plaintext streaming (`file`, `block`, `raw`, `history`, `link_raw`) | **4.0 MiB**, independent of file size | `BenchmarkDownloadStreamMemory/block=8MiB/plaintext` and `block=16MiB/plaintext` |
| Encrypted streaming | **36.0 MiB** at 8 MiB; **~68 MiB measured / 72 MiB design** at 16 MiB | `BenchmarkDownloadStreamMemory/block=8MiB/encrypted` and `block=16MiB/encrypted` |
| `raw` iWork preview, 32 MiB source cap | **~184.5 MiB** encrypted, ~120 MiB plaintext; 192 MiB design cost | `BenchmarkIWorkPreviewMemory/source=32MiB/*` |

Two results drove decisions rather than merely being recorded.

Encrypted streaming does not stream inside the prefetch: `PrefetchBlock` calls
`GetBlock` and `DecryptLibraryBlock`, so an admitted transfer holds the decrypted
current block, the decrypted next block and their encrypted sources at once. At
roughly 4.5x the block size it is what `max_active_per_node` actually divides
into. The 16 MiB accepted sync block is therefore the sizing input, not only the
8 MiB typical client block.

The iWork preview is not a stream at all, and its peak is ~4× the source
plaintext and ~6× encrypted — a 256 MiB document measured at 1.5 GiB. Against the
general 1 GiB `max_preview_bytes`, one request could touch six. §6 anticipated
this and named the cheaper lever, which is what was taken: a `FileView` iWork
**source** cap of 32 MiB, which touches neither the frozen profile enum, the §12
schema nor the metric label set. Sizing `max_active_raw` for iWork instead would
have throttled ordinary raw streams — 4 MiB each — by two orders of magnitude.

Worst case under the shipped auto caps: `4 x 192 MiB + 12 x 72 MiB = 1632 MiB`
(`~1.59 GiB`), below the safety-adjusted 1.6 GiB design budget. The budget is
configurable per deployment through `memory_budget_bytes`,
`memory_budget_percent` or `DOWNLOAD_ADMISSION_MEMORY_BUDGET_BYTES`; the 2 GiB
value is a fallback reference, not a universal machine capacity.
This is a hard startup invariant enforced by the configuration validator, not an
operator advisory. Raising the node/profile/source/block values or lowering
the deployment budget can therefore make an enabled deployment refuse to boot.
A zero `max_active_raw` removes the
extra raw profile sub-cap; it does not remove the raw/iWork term, so validation
charges all node slots at that worst-case cost. `max_active_block` remains a
profile sub-cap and cannot exceed the node cap. Overrides use the maximum of the
ordinary plaintext/encrypted stream cost, the measured iWork source cost and
the bounded preview-output cost (`stream + source + extracted preview`) for
every raw slot, so reducing the source cap or sync block size cannot undercharge
a raw stream.

### Where each criterion is demonstrated

| # | Evidence |
|---|---|
| 1 | Producer inventory in §1; non-producers regressed in D4; `link_inline` billing residual recorded in §11 |
| 2 | `internal/downloadadmission` atomicity tests (D1) |
| 3 | `TestDownloadAdmissionSaturationHoldsOneNodeCeiling` — three identities fill the real node cap while `raw` and `file` live simultaneously inside one aggregate |
| 4 | `TestDownloadAdmissionFairnessIsolatesLinkAndOwner` — both directions, each gated on the other side genuinely refusing first |
| 5 | Same saturation drill drains to zero; `TestIdentityEntriesDrainAfterSequentialChurn` covers 20,000 distinct identities and the live probe covers 200 repeated transfers |
| 6 | D4/D5 placement regressions, including ZIP held through `Close()` |
| 7 | D3/D5 lifetime tests plus `ISSUE-DOWNLOAD-ADMISSION-PRE-FIRST-WRITE-GAP-01`, which closed the last unbounded phase |
| 8 | Saturation drill asserts `503` with a valid `Retry-After`; byte integrity in `TestDownloadAdmissionThroughProxyDeliversCompleteBytes` |
| 9 | D5 canonical-reader streaming with authoritative size |
| 10 | `TestDownloadAdmissionThroughProxyReleasesStalledClient` — a stalled client through the **real frontend nginx** holds its slot, is classified as `idle_write_timeout`, and is not released by client cancellation |
| 11 | `scripts/fault-inject-download-admission.sh` — the follow-up drill records 33 retryable `profile="block"` refusals, summing the six reasons a saturated node can answer with (`admission_timeout`, `auth_user_full`, `node_full`, `profile_full`, `auth_user_queue_full`, `node_queue_full`) and excluding `client_gone`; it proves HTTP 503 with `Retry-After: 10`, and real `seaf-cli` reaches `synchronized`, pulls every file and drains every slot |
| 12 | Measurements above; egress measured at 356 MiB/s single and 1699 MiB/s aggregate six-way (**4.9×**), re-measured on a rebuilt stack at 350 and 1788 MiB/s (**5.1×**). The residual is the near-linear relationship, which reproduced; the absolute rates are per-run |
| 13 | `ISSUE-OBJECT-STORAGE-ANONYMOUS-DOWNLOAD-01` remains open and is not treated as closed by D |
| 14 | `TestShippedConfigs*` load every shipped configuration through the real validator, including auto-derived capacities and the safety-adjusted memory invariant; `TestDownloadAdmissionDerivesCapacityFromTheCgroupLimit` covers the auto path against a real container limit via the `sesamefs-cgroup-probe` service — the only node in the stack that declares one, since the rest report `max` and always take the reference fallback — asserting the budget *source* as well as the numbers, because a derived budget and an explicit one of the same size are otherwise indistinguishable; `TestDownloadAdmissionMemoryUnderSaturation` is an opt-in real-node RSS/heap/cgroup probe, which sizes itself from the node's published capacity and safety-adjusted budget. Measured at the 16-slot baseline: **+52 MiB cgroup delta against a 1.64 GiB safety-adjusted budget**, with RSS and heap deltas at zero — the pages were already resident. It holds ordinary raw and file streams rather than buffered iWork previews, so it does **not** reproduce the modelled worst case; the per-admission benchmarks remain the source for that |

### What the measurements are not

They come from a container stack on developer hardware. The component **memory**
figures are properties of the code — buffer sizes, block sizes and copy counts —
so they transfer; the aggregate probe additionally measures the running process
with real storage and middleware. The **throughput** figures do not: ~350 MiB/s single-transfer is a
loopback number, and production egress must be re-measured on the real network
before the concurrency cap is read as an egress ceiling. Two runs on the same
hardware differed by 5% on the single rate and moved the multiple from 4.9x to
5.1x, which is the accuracy this measurement has. The relationship the residual
depends on — aggregate scaling with concurrency because nothing shapes byte rate
— is what reproduced across both.

Capacities are also **process-local**. Fleet capacity is these numbers times the
node count; none of this is a cluster-global quota.

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
| 6 | Correct placement | Rejection happens before expensive metadata/S3 work; admission is held through the protected response lifetime, explicitly including until `zipWriter.Close()` returns and until the inline-content JSON response is written |
| 7 | Recoverable lifetime | Context cancellation, idle-write timeout, writer reachability, storage cancellation and idempotent release are proven; release also survives a panic raised inside the response cleanup, and that unwind is attributable as `cause="panic"` |
| 8 | Correct HTTP behavior | 503/Retry-After contract, Range behavior, post-header failure, 304/416/redirect handling and byte integrity are preserved |
| 9 | Block GET safety | Block GET streams through the canonical reader with authoritative size and no full-block materialization |
| 10 | Middleware and proxy wiring | Actual gzip stack, real TCP tests and the supported nginx topology cover block, files, ZIP, raw, history, `/d/...` **and the two `/api/v2.1/share-links/:token[/files]/bootstrap` routes that carry the `link_inline` response**; buffering, timeout and H2 behavior are verified |
| 11 | Client recovery | `seaf-cli`, browser/download behavior and OnlyOffice retrieval recover or fail clearly under saturation |
| 12 | Configuration evidence | Defaults, ceilings and long-transfer capacities are measured, validated and documented as process-local; egress throughput is measured and the byte-rate residual is explicit |
| 13 | No false storage claim | MinIO/direct-object exposure remains separately tracked and is not silently treated as closed by D |
| 14 | Observability and configuration contract | Every key ships in all `configs/*.yaml`, both `.env` examples and `applyEnvOverrides()` with enforced ceilings and the per-key zero policy; startup refuses an enabled-but-unbounded configuration and a non-zero `server.write_timeout`; the metric series and their fixed label sets exist as specified; `active_current == sum(active_by_profile)` and `entries_current == active_current + waiters_current` hold under mixed authenticated and public load; `Retry-After` is `ceil(retry_after / 1s)` with a floor of 1; no identity value appears in any label |

## D0-D6 PR Sequence

Each PR is independently reviewable and safe to stop during development; none
may claim a later PR's behavior. Runtime rollout constraints are explicit: D2's
strict token contract is deployed as one coordinated greenfield version across
all writers and consumers. This series does not promise mixed-version rolling
deployment for token issuance.

| PR | Purpose | Runtime behavior |
|---|---|---|
| D0 | Contract, inventory, identity and evidence record | None; docs only |
| D1 | Neutral D coordinator, atomic dimensions, bounded state, config and metrics | Coordinator not yet connected to producers |
| D2 | Stable `SourceID` for all public download-token mint paths | Merged in `main`; new link tokens are strict; no legacy compatibility; coordinated greenfield rollout |
| D3 | Writer lifetime, idle-write deadline and gzip/writer reachability strategy | Merged in `main`; lifecycle and frontend-config regressions exercise writer safety before broad admission activation. D6 owns the real nginx slow-client drill. |
| D4 | Integrate file, ZIP, raw, history, share raw and inline text producers | Complete: one bootstrapped coordinator covers all listed non-block producers through preparation and response lifetime |
| D5 | Stream sync block GET through existing canonical reader APIs | Complete: `SyncHandler.GetBlock` uses `GetBlockSize`/`GetBlockReader` under `ProfileBlock` on the shared coordinator; neither the canonical nor the legacy no-metadata path materializes the whole block; traffic uses bytes written |
| D6 | Fault evidence, client recovery, measurements and final closure docs | **Complete (follow-up verified 2026-08-04).** Per-admission cost measured, auto capacities derived from the effective budget with 20% safety headroom, shipped configs enabled, and the reason-filtered client/proxy/saturation evidence produced. An opt-in probe measures the real process RSS/heap/cgroup delta under saturation; it exercises ordinary raw streams, not the iWork worst case, and its output is not yet recorded as a closure figure. See **D6 Evidence** above |

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
docker compose --profile test run --rm --build --entrypoint sh go-integration-test \
  -c 'export PATH=$PATH:/usr/local/go/bin && SESAMEFS_DOWNLOAD_PROBE=1 SESAMEFS_DOWNLOAD_MEMORY_PROBE=1 \
      go test -tags integration -run TestDownloadAdmissionMemoryUnderSaturation -v -count=1 ./internal/integration/'
docker compose --profile test run --rm --build sync-test
docker compose --profile test run --rm --build go-all-test
```

D1-D5 must add focused tests for their own invariants. D6 must additionally
prove real client recovery, cross-route saturation, public-link remint
fairness, identity churn, memory/goroutine/slot drain and byte-for-byte
integrity. A green unit suite without real middleware and storage evidence is
not sufficient to close D.
