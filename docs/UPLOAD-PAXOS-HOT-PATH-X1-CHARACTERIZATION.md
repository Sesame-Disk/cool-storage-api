# X1 / X4 - Upload Hot-Path Paxos Characterization

**Date:** 2026-08-20
**Status:** Confirmed analysis and design constraints. **Not an ADR. No runtime
change is proposed by this document.**
**Base:** `main` after R23b merge `5cf4f0345`.

This document preserves the investigation that connects the upload performance
finding X4 / P-4 / UP-2 with the physical-delete ABA work in X1. It incorporates
the two successive reviews of the original analysis. The first review is right
that the hot path is expensive; it is wrong if read as saying that P0 would
introduce new `SERIAL` latency to deployments already using the shipped setting.
The second review correctly moves the
decision from "add more SERIAL" to "prove whether ordinary installation can
have one deterministic physical identity without per-block Paxos".

## Executive Verdict

The following facts are confirmed against the current tree:

| Claim | Verdict | Required qualification |
|---|---|---|
| 1000 newly registered blocks can cause about 1000 metadata LWT attempts | Confirmed | This is for blocks that reach metadata registration. Deduplication preflight can bypass complete existing blocks; retries can add attempts. |
| Chunked SeafHTTP finalization runs S3 work concurrently but metadata materialization at concurrency 1 per process | Confirmed with scope | This applies to `finalizeUploadStreaming` and its chunked finalizations. Non-chunked `HandleUpload` registers uploads without this permit, so process-wide metadata materialization can exceed one. The permit covers provisional-reference and metadata work, not only the LWT. |
| The shipped production YAML/example uses `SERIAL` | Confirmed | `configs/config.prod.yaml` and `.env.prod.example` select `SERIAL`, but `CASSANDRA_SERIAL_CONSISTENCY` can override it and `Validate()` accepts `LOCAL_SERIAL`. Whether the transaction crosses DCs depends on the effective replication topology; the same shipped config also supports single-region deployments. |
| P0 introduces 1000 `SERIAL` LWT/Paxos transactions into deployments using the shipped setting | Incorrect | Those deployments already pay the LWT/Paxos transaction today. P0 would make the serial domain explicit and could change deployments whose environment overrides the setting to `LOCAL_SERIAL`. |
| The two-minute finalize context limits the 1000 block materializations | Incorrect | `eg.Wait()` completes first. The two-minute context starts afterward for final file metadata/lease work. |
| Generation-based deterministic keys are a promising direction | Candidate only | They can remove the need to choose between random keys for the same incarnation; this does not revive the abandoned r3 generational GC fence. |
| Generation alone closes X1 | Incorrect | A stale writer can still attempt to install an old generation unless install is generation-aware or the schema makes old generations unable to become canonical. |
| `storage_class` can differ for the same `(org_id, block_id)` | Confirmed | A mutable library preference, empty request-region routing and org-wide dedupe let concurrent/new materializations propose different classes; an existing canonical row retains the class won by the first writer. |
| Switching to `LOCAL_SERIAL` is a safe performance fix | Rejected | Two DCs can each accept a local Paxos decision. That is not a global winner and can diverge placement/lifecycle state. |

The resulting characterization boundary is:

> Do not start P0/R12 merely as an X4 performance fix. Keep the shipped
> `SERIAL` default, do not switch a multi-DC deployment to `LOCAL_SERIAL` as a
> performance shortcut, and characterize placement and incarnation before
> removing the metadata LWT. The sequencing of explicit P0/R12 correctness
> hardening remains open.

This does **not** discard R12. R12 remains a safety requirement for any
conditional operations whose correctness depends on one global serial domain.
It is not, by itself, a justification for adding new latency to deployments
that already run the shipped `SERIAL` setting. A deployment whose environment
overrides the setting to `LOCAL_SERIAL` would have different current behavior.

## Confirmed Current Path

### Metadata LWT

The first-writer metadata function issues:

```sql
INSERT INTO blocks (
    org_id, block_id, representation_id, sha1, size_bytes,
    storage_class, storage_key, created_at, last_accessed
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
IF NOT EXISTS
```

The query is in
[`internal/db/block_references.go`](../internal/db/block_references.go), in
`upsertBlockMetadataInsertWithRepresentationFn`.
`UpsertBlockMetadataWithRepresentationAndSHA1` calls it for every materialized
block and retries only for the specific released-stub repair path in the same
file.

For a new block, the normal materialization cost is one metadata LWT attempt.
The surrounding operations are not all LWTs:

| Stage | Current operation | Paxos? |
|---|---|---:|
| Reuse classification | Metadata, reference and orphan-fence reads | No |
| Upload liveness | Provisional reference plus expiry/projection logged batch | No |
| Fence authorization | `BlockDeleteFenceActive` reads | No |
| Canonical metadata | `INSERT ... IF NOT EXISTS` on `blocks` | Yes, one `SERIAL` LWT/Paxos transaction per registration |
| Web SHA-1 mapping | Read-before-write checked mapping | No, by design |

The "global" part, when the effective setting is `SERIAL` and the replica set
spans DCs, is the serial consistency domain. The LWTs for different
`(org_id, block_id)` partitions do not share one Paxos log and do not contend
on one Cassandra partition; each registering block still pays its own
`SERIAL` LWT/Paxos transaction.

One LWT/Paxos transaction is not necessarily one network round-trip. Cassandra
5 documents the Paxos variant as a material benchmark variable: compatible
Paxos v1 expects about four round-trips for a write, while optimized v2 expects
about two. The repository's Cassandra 5.0.9 Compose definitions do not pin
`paxos_variant` or `paxos_state_purging`; the benchmark must record the actual
server settings rather than infer them from the image tag. See the [Cassandra
5.0 configuration documentation](https://cassandra.apache.org/doc/5.0.9/cassandra/managing/configuration/cass_yaml_file.html).

The provisional batch is documented in
[`internal/db/provisional_block_ref_expiry.go`](../internal/db/provisional_block_ref_expiry.go),
in `AddProvisionalBlockReferenceWithExpiry`.
The web mapping explicitly avoids a per-block LWT because of multi-DC latency
and contention in `GetBlockIDMapping`/the mapping write path in
[`internal/db/block_references.go`](../internal/db/block_references.go).

Therefore, for 1000 new blocks that all reach registration, approximately 1000
metadata LWT attempts is correct. At the current 8 MiB block size, a GiB of
new/full content is approximately 128 such blocks and therefore approximately
128 cross-DC `SERIAL` LWT/Paxos transactions when the effective replica set
spans DCs. It is not correct to call either number a cost for
every file unconditionally: fully deduplicated blocks can be classified before
registration, while retries and GC races can make the actual count higher.

### Web block upload

The session web path stores the block and then calls `materializeUploadedBlock`
in:

- [`internal/api/v2/blocks.go`](../internal/api/v2/blocks.go)
- [`internal/api/v2/fs_helpers.go`](../internal/api/v2/fs_helpers.go), through
  `RegisterUploadedBlock`

The per-user request limiter defaults to eight concurrent block uploads. This
allows independent block partitions to overlap, but it does not reduce the
number of LWT/Paxos transactions or the Cassandra capacity consumed by them.

### SeafHTTP large-file upload

`finalizeUploadStreaming` reads 8 MiB blocks and submits per-block work to a
bounded worker pool:

- S3/block work: `finalizeUploadConcurrency = 8`
- Cassandra materialization permit:
  `finalizeUploadBlockMetadataConcurrency = 1`

The constants and their stated purpose are in
[`internal/api/seafhttp.go`](../internal/api/seafhttp.go).
The metadata permit is acquired after the S3 work and covers the registration
callback at
`acquireFinalizeUploadBlockMetadataPermit`.
The non-chunked `HandleUpload` path registers at
`HandleUpload` without acquiring this permit.

Within a chunked finalization, the eight-slot worker pool can initially have up
to eight S3 operations in flight. Once a worker reaches the process-local
metadata permit, it serializes the metadata/ref/mapping callback one block at a
time while retaining its worker slot. The permit is a pressure valve against a
Cassandra LWT stampede; it is not a process-wide limit on every SeafHTTP upload:
the non-chunked `HandleUpload` path calls registration without acquiring it.
The eight-slot worker semaphore is acquired before the goroutine starts S3 work
and released only after the full callback returns. A worker waiting for the
metadata permit therefore still occupies one of the eight slots; this is not an
independent pipeline with eight S3 workers plus an unbounded metadata queue.
Non-chunked requests can issue metadata work concurrently with a covered
chunked-finalization callback, while concurrent chunked callbacks queue behind
the permit. Multiple processes can also issue metadata work concurrently.

Two shapes are worth writing down, but only as bounds with an explicit premise —
this document exists to argue for measurement, so it must not present an unmeasured
formula as the wall-clock model:

```text
SeafHTTP, one process, IF serialized metadata dominates:
    approximately N × (metadata/ref path latency)

Web session path, idealized:
    approximately ceil(N / per-user-concurrency) × LWT latency
    plus queueing and Cassandra contention
```

The SeafHTTP shape holds only when the serialized metadata stage is the dominant
bottleneck. The real path is a pipeline: up to eight S3 operations overlap while a
single metadata permit serializes the callbacks, so throughput is governed by
whichever of those two stages is slower, plus fill/drain at the edges and Cassandra
contention. If S3 is the slower stage, `N × metadata latency` understates the
total; if metadata is slower, it approaches it from below. Which one dominates is
exactly what the benchmark in the next section has to establish.

Neither shape removes the total work of N LWT/Paxos transactions. The current code
does not provide a production per-statement latency metric, so no numeric latency
claim should be treated as measured.

### The two-minute context correction

The two-minute context is created only after all per-block workers complete:

```go
if err := eg.Wait(); err != nil {
    return "", "", 0, 0, err
}

finalizeCtx, cancelFinalize := newSeafHTTPUploadMetadataFinalizeContext()
```

This ordering is in `finalizeUploadStreaming` in
[`internal/api/seafhttp.go`](../internal/api/seafhttp.go).
The two-minute timeout governs final file metadata/lease publication, not the
preceding chain of block materializations. A large upload can still be very
slow, but this specific timeout does not abort it at 120 seconds.

## Serial Consistency and P0

The shipped application configuration is:

```yaml
consistency: LOCAL_QUORUM
serial_consistency: SERIAL
```

at [`configs/config.prod.yaml`](../configs/config.prod.yaml), and
`.env.prod.example` also sets `CASSANDRA_SERIAL_CONSISTENCY=SERIAL`.
`CASSANDRA_SERIAL_CONSISTENCY` overrides the YAML value before validation;
`Validate()` accepts both `SERIAL` and `LOCAL_SERIAL`. `newCluster` places the
effective value on the gocql session in [`internal/db/db.go`](../internal/db/db.go).
The metadata query does not call `.SerialConsistency(...)`, so it inherits the
session-level value.

The current effective behavior is therefore:

```text
metadata INSERT IF NOT EXISTS
    -> inherited effective session serial consistency
    -> SERIAL only when the runtime setting remains SERIAL
```

The USA/EU cluster profiles intentionally use `LOCAL_SERIAL` for their test
harnesses. The database code warns, but does not reject, local serial when the
effective replication is multi-region. `SERIAL` is a global serial domain only
relative to the configured replica set; a single-region deployment does not
turn it into WAN latency.

P0/R12 would change implicit/configurable behavior into explicit statement
behavior. It would:

- prevent a weaker session configuration from silently changing a safety
  property;
- make the relevant `blocks` LWT inventory auditable;
- align test profiles with the intended global serial contract if they are
  meant to exercise that contract;
- add no new `SERIAL` latency to deployments currently using the shipped
  `SERIAL` setting; it can change behavior and latency for an override using
  `LOCAL_SERIAL`.

P0/R12 is not required as a performance fix and should not be started merely
to address X4. Its sequencing as correctness hardening remains open while the
install design is evaluated. If the final design removes the
ordinary metadata LWT, the P0 inventory must be split between hot-path install
operations and background/destructive lifecycle operations. GC claims,
conditional orphan transitions and ambiguous-outcome settlement cannot be
made local merely because normal installation is redesigned.

## Why `storage_class` Is the First Design Decision

The current metadata comments explicitly state that `storage_class` and
`storage_key` are not globally fixed by the block hash. First-writer-wins pins
one physical location because writers can arrive with different classes:

[`internal/db/block_references.go`](../internal/db/block_references.go), in the
`UpsertBlockMetadata` placement contract comments.

The resolver has three materially different cases:

1. A non-empty library class is the preferred class for a new materialization and
   is resolved exactly before health-aware selection. The actual selected class
   is persisted on the canonical block row.
2. An empty library class falls through to hostname/region/default routing.
3. An existing canonical block bypasses this preference and resolves from its
   persisted `blocks.storage_class` for reads, reuse and repair.

That behavior is implemented by `ResolveStorageClass` in
[`internal/storage/storage.go`](../internal/storage/storage.go) and the
library lookup in [`internal/api/v2/storage_resolution.go`](../internal/api/v2/storage_resolution.go).

The residency policy is enforced separately by
`resolveCreateStorageClassForOrg` during library creation. `strict` constrains
the requested/new-library class to the organization's allowed region, but the
later `GetHealthyBlockStoreForOrg` selection does not receive that policy. If a
preferred class is already marked `Unhealthy` or `Failed`, its configured
`failover_class` can therefore supply the actual class for a new block, which is
then persisted on `blocks`. `Unknown`, `Healthy` and `Degraded` use the
preferred class. No periodic production health checker was found, so failover
depends on the health map already having been updated. It is not merely
unscheduled: `UpdateHealth` has one caller, `CheckHealth`, and neither
`CheckHealth` nor `CheckAllHealth` has any non-test caller in the tree. Ordinary
PUT/GET errors do not update the map either, so in a running server every class
stays `Unknown` and this failover branch cannot be taken. The residency
consequence above is therefore latent today and becomes reachable as soon as a
health checker is added.

The current logical block key is only `(org_id, block_id)`:

```sql
PRIMARY KEY ((org_id, block_id))
```

at [`internal/db/migrations/001_initial_schema.cql`](../internal/db/migrations/001_initial_schema.cql).

This creates the unresolved placement case:

```text
same org + same content hash
    writer NA -> B1
    writer EU -> B2
```

If the dedupe domain stays org-global, a deterministic `K` alone still leaves
two different physical tuples:

```text
P1 = (B1, K)
P2 = (B2, K)
```

The current `ChangeStorageClass` endpoint changes the library preference but
does not rewrite `storage_class` on existing `blocks` rows or migrate their
objects; the migration work remains a TODO in
[`internal/api/v2/libraries.go`](../internal/api/v2/libraries.go). A library
can therefore reference blocks materialized under different classes over time,
and a new preference can compete with an org-global canonical block when a
logical block is not already present. This is a placement-policy problem, not
a rebinding of the old class name or a silent rewrite of old block rows.

## Candidate Placement Domains

These are candidates for investigation, not accepted designs.

The detailed impact analysis is in
[STORAGE-CLASS-PLACEMENT-OPTIONS.md](./STORAGE-CLASS-PLACEMENT-OPTIONS.md). It
covers the current schema, references, upload/read paths, GC, failover,
storage cost, greenfield initial-schema requirements and the conditions for any
future LWT removal.

The first design decision is `B = storage_class`, not the deterministic key
`K`. With the current request-dependent placement, two writers can still
propose different physical tuples even when they derive the same key:

```text
Writer NA                     Writer EU
   |                              |
   v                              v
B = hot-na                    B = hot-eu
K = hash                      K = hash

P1 = (hot-na, K)              P2 = (hot-eu, K)
```

If placement instead becomes deterministic for the logical block,
`B = Placement(org_id, block_id)`, every writer proposes the same physical
class before installation:

```text
Writer NA ----+
Writer EU ----+--> same block_id --> same B
Writer AS ----+
```

That removes the placement election from the install race. It does not by
itself prove that the remaining install, repair, or incarnation transitions
are safe without a conditional operation.

Any option also requires `storage_class` to remain an append-only physical
namespace identity: a class must not be rebound to another backend and must
not be reused for a different namespace. A library class change therefore
needs an explicit policy for future placement and any physical migration; it
does not by itself change the meaning of existing block rows.

### A-prime. Mutable library preference with org-global canonical identity

The library carries a mutable preference for future placement:

```text
library.storage_class = hot-na
```

Existing canonical blocks are not moved or reinterpreted when this preference
changes. New block materializations from libraries that have different
preferences can still compete while the same `(org, block_id)` is deduplicated
across the organization:

```text
library A -> hot-na
library B -> hot-eu
same org + same SHA-256
```

Because `blocks` is currently keyed by `(org, block_id)`, those libraries compete
to establish one canonical physical class and the first-writer LWT remains the
arbitration mechanism. `ChangeStorageClass` therefore changes future preference
only; a separate migration operation would be required to copy existing
referenced bytes.

### B. Class included in logical identity

Change the conceptual logical domain from:

```text
L = (org_id, content_hash)
```

to:

```text
L = (org_id, storage_class, content_hash)
```

Then the deterministic physical identity can be defined as:

```text
(org_id, B, hash) -> K
```

so `hot-na/hashX` and `hot-eu/hashX` are different logical blocks. Two writers
of the same logical identity necessarily propose the same `P`; the placement
fight disappears by construction.

The product/storage cost is losing dedupe between classes: identical content
placed in NA and EU occupies two objects. That may be the correct semantics if
each storage class is a genuinely different physical namespace and materialized
life. It requires a broad audit of block references, mappings, file manifests,
readers and GC, and it is not yet a schema decision.

This is a candidate worth characterizing because it matches R23b's meaning of a
storage class as a permanent physical namespace. It is not yet a schema
decision or the current product recommendation.

### C. Home class derived from `(org_id, content_hash)`

For example:

```text
home = H(org_id, block_id) % availableClasses
```

This preserves org-global dedupe: every library and writer for one org/hash
lands in the same home class, regardless of the DC from which the upload
arrives. It changes placement authority from request/library policy to a
stable hash assignment, so it needs a versioned placement function, stable
class membership, explicit hot/cold semantics, failover rules and a migration
story when the class set changes. Changing the class set can otherwise remap
existing hashes, and requested library placement no longer has authority.

## Generation and Exact-`P` Requirements

The generation terms below describe a candidate physical-identity and install
protocol only. They do **not** revive the abandoned r3 generational GC fence:
that decision remains recorded in [`DECISIONS.md`](./DECISIONS.md#abandon-the-generational-gc-fence-r3-for-x1),
and X1 still has no accepted design.

Once the class domain is fixed, a deterministic incarnation key remains a
possible way to remove random-writer arbitration:

```text
life G0 -> K0 = f(logical_id, G0)
life G1 -> K1 = f(logical_id, G1)
```

The following requirements are non-negotiable before removing the install LWT:

1. The generation is durable, monotonic and cannot disappear with an orphan
   TTL.
2. Existing/legacy rows are assigned a non-reusable generation before new
   generations are enabled.
3. A writer that read `G0` cannot install `G0` after GC has advanced the logical
   block to `G1`.
4. `INSTALL(G)` and `REPAIR(P)` are different operations. Repairing an existing
   incarnation must never recreate an absent canonical row.
5. The canonical record carries the exact current physical tuple
   `P = (storage_class, storage_key)`. If a future design adds generation to
   the install identity, it must bind that generation to `P` without allowing
   an old writer to re-authorize the tuple.
6. Readers, HEAD/existence checks, repair and GC use the persisted locator
   instead of deriving the old hash-only key.
7. Orphans, candidates and discovery projections cannot clear or delete a
   different generation's tuple.
8. Ambiguous CAS/install outcomes remain install-uncertain until settled; an
   uncertain key cannot be reused or cleanup-authorized speculatively.

The stale-writer race remains even with a global generation publication:

```text
W1 reads G0 and pauses
GC retires G0 and publishes G1
GC releases the fence
W1 resumes with G0
W1 must not make K0 canonical again
```

`EACH_QUORUM` can disseminate a generation after an already-owned GC
transition, but it does not itself provide the compare-and-set that prevents
the stale install. This is the core relationship between the hot-path redesign
and X1's R17/R24 requirements.

The current physical locator is still derived by `hashToKey` in
[`internal/storage/blocks.go`](../internal/storage/blocks.go).
The locator-authority phase must therefore precede any change to the key
formula, as required by the X1 sequencing note.

## X1 Relationship

This investigation does not close X1. It narrows the safe design space:

- X1's physical-delete ABA still requires never-reused exact physical tuples.
- R9 still requires one canonical identity for concurrent installation.
- R12 still governs conditional operations that rely on one global serial
  domain; it is not a reason to add another hot-path consensus transaction for a
  deployment already using the shipped `SERIAL` setting.
- R17 still requires condemned-incarnation writer safety.
- R24 still requires single-use install identities and settlement of ambiguous
  outcomes.
- R13, R20, R26 and R27 remain relevant to orphan and projection lifecycle.

The authoritative X1 option/race ledger is
[`GC-X1-CLOSURE-OPTIONS.md`](./GC-X1-CLOSURE-OPTIONS.md). Its current sequencing
note is a hypothesis for safety ordering, not approval to implement P0-P4 as a
single roadmap. The existing PR-11 entry remains deferred and is linked from
[`GC-UPLOAD-FENCE-PR-PLAN.md`](./GC-UPLOAD-FENCE-PR-PLAN.md).

## Next Characterization PR

The next PR should change neither identity semantics nor consistency levels. It
should answer these questions with instrumentation, tests and a real
multi-DC-compatible benchmark:

- How many metadata registrations and LWT attempts occur for 1, 100 and 1000
  new blocks?
- What are LWT latency, `applied=true/false`, retries and timeout distributions?
- How much time is spent waiting for `finalizeUploadBlockMetadataConcurrency`?
- What happens when `finalizeUploadConcurrency` is 1, 2, 4 and 8 with the
  metadata permit fixed at 1, and when the permit is varied independently?
- How much S3 launch delay is caused by worker slots held while waiting for the
  metadata permit?
- What are the actual Cassandra version, `paxos_variant`, Paxos state-purging
  mode, effective serial consistency and replication topology/RF at benchmark
  time? The repository pins the Cassandra 5.0.9 image but does not pin those
  server-side Paxos settings.
- Which upload paths pass `storage_key=""` or derive it again?
- What `storage_class` can two concurrent writers propose for the same logical
  content across libraries and DCs?
- What is the dedupe/storage cost of `(org, storage_class, content_hash)`?

The candidate-schema stale-generation race, `INSTALL(G)`/`REPAIR(P)` split and
old-writer rejection belong to the subsequent protocol prototype. This
characterization PR should inventory the current identities and measure the
current paths; it must not imply that a candidate schema already exists.

The performance objective for the eventual design is conditional, and the
condition is not negotiable:

```text
ordinary new-block installation:
    minimize or remove the per-block `SERIAL` LWT/Paxos transaction ONLY IF
    equivalent global first-writer arbitration is provided without changing
    A-prime semantics

retirement/reincarnation:
    global coordination only on exceptional GC transitions
```

Stated unconditionally, that first line would contradict this document's own
finding. Under A-prime the LWT is not overhead: it is the mechanism that lets two
concurrent first materializations proposing different classes for the same
`(org_id, block_id)` settle on one canonical physical placement while preserving
org-global deduplication.

Removing it naively does not shrink the deduplication domain — one row per
`(org_id, block_id)` remains — it breaks the locator: a plain last-writer-wins
INSERT can leave the canonical row naming a class where the winning writer never
put the object, which breaks reads and points GC at the wrong physical copy. The
alternatives that would remove the arbitration safely are the ones catalogued in
[STORAGE-CLASS-PLACEMENT-OPTIONS.md](./STORAGE-CLASS-PLACEMENT-OPTIONS.md), and
each pays elsewhere: Option B changes the deduplication domain, Option C removes
library placement authority. There is no version of this that is only a latency
change.

There is therefore no architectural obligation to eliminate it. If measurement
shows the cost is acceptable — or that Cassandra's Paxos v2 makes it acceptable —
keeping it is a legitimate outcome. "Measure, then decide" is the position; "remove
the LWT" is not a goal this document endorses.

Until those invariants and measurements exist, keep the current metadata LWT,
keep the shipped `SERIAL` default, do not use `LOCAL_SERIAL` as a multi-DC
performance shortcut, and keep destructive GC disabled under X1.

## References

- [X1 closure options and race ledger](./GC-X1-CLOSURE-OPTIONS.md)
- [Upload-fence PR split plan](./GC-UPLOAD-FENCE-PR-PLAN.md)
- [Upload performance/security analysis, P-4](./UPLOAD-PERFORMANCE-SECURITY-2026-06.md)
- [Open work index, X4](./OPEN-WORK-INDEX.md)
- [Known issue: per-block Paxos](./KNOWN_ISSUES.md#issue-upload-per-block-paxos-01-one-serial-lwtpaxos-transaction-per-block-on-upload)
- [X1/X2 findings registry](./UPLOAD-FENCE-FINDINGS-REGISTRY.md)
