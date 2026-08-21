# X1 / X4 - Upload Hot-Path Paxos Characterization

**Date:** 2026-08-20
**Status:** Confirmed analysis and design constraints. **Not an ADR. No runtime
change is proposed by this document.**
**Base:** `main` after R23b merge `5cf4f0345`.

This document preserves the investigation that connects the upload performance
finding X4 / P-4 / UP-2 with the physical-delete ABA work in X1. It incorporates
the two successive reviews of the original analysis. The first review is right
that the hot path is expensive; it is wrong if read as saying that P0 would
introduce production `SERIAL` latency. The second review correctly moves the
decision from "add more SERIAL" to "prove whether ordinary installation can
have one deterministic physical identity without per-block Paxos".

## Executive Verdict

The following facts are confirmed against the current tree:

| Claim | Verdict | Required qualification |
|---|---|---|
| 1000 newly registered blocks can cause about 1000 metadata LWT attempts | Confirmed | This is for blocks that reach metadata registration. Deduplication preflight can bypass complete existing blocks; retries can add attempts. |
| Chunked SeafHTTP finalization runs S3 work concurrently but metadata materialization at concurrency 1 per process | Confirmed with scope | This applies to `finalizeUploadStreaming` and its chunked finalizations. Non-chunked `HandleUpload` registers uploads without this permit, so process-wide metadata materialization can exceed one. The permit covers provisional-reference and metadata work, not only the LWT. |
| Production `SERIAL` is cross-DC/global | Confirmed | `configs/config.prod.yaml` already selects `SERIAL`; the metadata LWT inherits the session setting. |
| P0 introduces 1000 global Paxos rounds into production | Incorrect | Production already pays them today. P0 would make the serial domain explicit and protect against a weaker session configuration, including `LOCAL_SERIAL` test profiles. |
| The two-minute finalize context limits the 1000 block materializations | Incorrect | `eg.Wait()` completes first. The two-minute context starts afterward for final file metadata/lease work. |
| Generation-based deterministic keys are a promising direction | Candidate only | They can remove the need to choose between random keys for the same incarnation; this does not revive the abandoned r3 generational GC fence. |
| Generation alone closes X1 | Incorrect | A stale writer can still attempt to install an old generation unless install is generation-aware or the schema makes old generations unable to become canonical. |
| `storage_class` can differ for the same `(org_id, block_id)` | Confirmed | Explicit library placement is stable only while the library value is present and unchanged. Empty library placement is request-region dependent, and org-wide dedupe allows cross-library conflicts. |
| Switching to `LOCAL_SERIAL` is a safe performance fix | Rejected | Two DCs can each accept a local Paxos decision. That is not a global winner and can diverge placement/lifecycle state. |

The resulting decision is:

> Do not implement P0/R12 or remove the metadata LWT until the placement and
> incarnation protocol is characterized. Do not weaken production to
> `LOCAL_SERIAL`. The next change should measure and specify the hot path, not
> change its safety semantics.

This does **not** discard R12. R12 remains a safety requirement for any
conditional operations whose correctness depends on one global serial domain.
It is not, by itself, a justification for adding new production latency: the
current production metadata LWT already inherits `SERIAL`.

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
[`internal/db/block_references.go:168-173`](../internal/db/block_references.go#L168-L173).
`UpsertBlockMetadataWithRepresentationAndSHA1` calls it for every materialized
block and retries only for the specific released-stub repair path
([`internal/db/block_references.go:574-634`](../internal/db/block_references.go#L574-L634)).

For a new block, the normal materialization cost is one metadata LWT attempt.
The surrounding operations are not all LWTs:

| Stage | Current operation | Paxos? |
|---|---|---:|
| Reuse classification | Metadata, reference and orphan-fence reads | No |
| Upload liveness | Provisional reference plus expiry/projection logged batch | No |
| Fence authorization | `BlockDeleteFenceActive` reads | No |
| Canonical metadata | `INSERT ... IF NOT EXISTS` on `blocks` | Yes, one per registration |
| Web SHA-1 mapping | Read-before-write checked mapping | No, by design |

The "global" part of this Paxos cost is the `SERIAL` consistency domain. The
LWTs for different `(org_id, block_id)` partitions do not share one Paxos log
and do not contend on one Cassandra partition; each registering block still
pays its own serial round.

The provisional batch is documented at
[`internal/db/provisional_block_ref_expiry.go:57-123`](../internal/db/provisional_block_ref_expiry.go#L57-L123).
The web mapping explicitly avoids a per-block LWT because of multi-DC latency
and contention at
[`internal/db/block_references.go:403-416`](../internal/db/block_references.go#L403-L416).

Therefore, for 1000 new blocks that all reach registration, approximately 1000
metadata LWT attempts is correct. At the current 8 MiB block size, a GiB of
new/full content is approximately 128 such blocks and therefore approximately
128 cross-DC serial rounds. It is not correct to call either number a cost for
every file unconditionally: fully deduplicated blocks can be classified before
registration, while retries and GC races can make the actual count higher.

### Web block upload

The session web path stores the block and then calls
`materializeUploadedBlock`:

- [`internal/api/v2/blocks.go:959-1021`](../internal/api/v2/blocks.go#L959-L1021)
- [`internal/api/v2/fs_helpers.go:986-1023`](../internal/api/v2/fs_helpers.go#L986-L1023)

The per-user request limiter defaults to eight concurrent block uploads. This
allows independent block partitions to overlap, but it does not reduce the
number of Paxos rounds or the Cassandra capacity consumed by them.

### SeafHTTP large-file upload

`finalizeUploadStreaming` reads 8 MiB blocks and submits per-block work to a
bounded worker pool:

- S3/block work: `finalizeUploadConcurrency = 8`
- Cassandra materialization permit:
  `finalizeUploadBlockMetadataConcurrency = 1`

The constants and their stated purpose are at
[`internal/api/seafhttp.go:1812-1830`](../internal/api/seafhttp.go#L1812-L1830).
The metadata permit is acquired after the S3 work and covers the registration
callback at
[`internal/api/seafhttp.go:3045-3054`](../internal/api/seafhttp.go#L3045-L3054).
The non-chunked `HandleUpload` path registers at
[`internal/api/seafhttp.go:2543-2549`](../internal/api/seafhttp.go#L2543-L2549)
without acquiring this permit.

Within a chunked finalization, one SeafHTTP process can have eight S3 operations
in flight while the process-local permit serializes the metadata/ref/mapping
callback one block at a time. The permit is a pressure valve against a
Cassandra LWT stampede; it is not a process-wide limit on every SeafHTTP upload:
the non-chunked `HandleUpload` path calls registration without acquiring it.
Non-chunked requests can therefore issue metadata work concurrently with a
covered chunked-finalization callback, while concurrent chunked callbacks queue
behind the permit. Multiple processes can also issue metadata work concurrently.

The practical wall-clock model is therefore:

```text
SeafHTTP, one process:
    approximately N × (metadata/ref path latency)

Web session path, idealized:
    approximately ceil(N / per-user-concurrency) × LWT latency
    plus queueing and Cassandra contention
```

Neither model removes the total work of N Paxos rounds. The current code does
not provide a production per-statement latency metric, so no numeric latency
claim should be treated as measured.

### The two-minute context correction

The two-minute context is created only after all per-block workers complete:

```go
if err := eg.Wait(); err != nil {
    return "", "", 0, 0, err
}

finalizeCtx, cancelFinalize := newSeafHTTPUploadMetadataFinalizeContext()
```

This ordering is at
[`internal/api/seafhttp.go:3071-3087`](../internal/api/seafhttp.go#L3071-L3087).
The two-minute timeout governs final file metadata/lease publication, not the
preceding chain of block materializations. A large upload can still be very
slow, but this specific timeout does not abort it at 120 seconds.

## Serial Consistency and P0

Production configuration is:

```yaml
consistency: LOCAL_QUORUM
serial_consistency: SERIAL
```

at [`configs/config.prod.yaml:47-59`](../configs/config.prod.yaml#L47-L59).
`newCluster` places that value on the gocql session at
[`internal/db/db.go:91-119`](../internal/db/db.go#L91-L119). The metadata query
does not call `.SerialConsistency(...)`, so it inherits the session-level value.

The current effective behavior is therefore:

```text
metadata INSERT IF NOT EXISTS
    -> inherited session serial consistency
    -> SERIAL under the production profile
```

The USA/EU cluster profiles intentionally use `LOCAL_SERIAL` for their test
harnesses. The database code warns when local serial is used with multi-region
replication at [`internal/db/db.go:164-171`](../internal/db/db.go#L164-L171).
Those profiles do not model production global Paxos.

P0/R12 would change implicit/configurable behavior into explicit statement
behavior. It would:

- prevent a weaker session configuration from silently changing a safety
  property;
- make the relevant `blocks` LWT inventory auditable;
- align test profiles with the intended global serial contract if they are
  meant to exercise that contract;
- add no new `SERIAL` latency to the current production default.

P0 is still not ready to implement blindly. If the final design removes the
ordinary metadata LWT, the P0 inventory must be split between hot-path install
operations and background/destructive lifecycle operations. GC claims,
conditional orphan transitions and ambiguous-outcome settlement cannot be
made local merely because normal installation is redesigned.

## Why `storage_class` Is the First Design Decision

The current metadata comments explicitly state that `storage_class` and
`storage_key` are not globally fixed by the block hash. First-writer-wins pins
one physical location because writers can arrive with different classes:

[`internal/db/block_references.go:547-561`](../internal/db/block_references.go#L547-L561)

The resolver has two materially different modes:

1. A non-empty library class is authoritative and is resolved exactly.
2. An empty library class falls through to hostname/region/default routing.

That behavior is implemented at
[`internal/storage/storage.go:302-350`](../internal/storage/storage.go#L302-L350)
[`internal/api/v2/storage_resolution.go:39-64`](../internal/api/v2/storage_resolution.go#L39-L64).

The current logical block key is only `(org_id, block_id)`:

```sql
PRIMARY KEY ((org_id, block_id))
```

at [`internal/db/migrations/001_initial_schema.cql:272-284`](../internal/db/migrations/001_initial_schema.cql#L272-L284).

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

The current `ChangeStorageClass` endpoint also updates a library class without
migrating existing blocks; its migration work is still a TODO at
[`internal/api/v2/libraries.go:1116-1158`](../internal/api/v2/libraries.go#L1116-L1158).
That means library-fixed placement is not yet an immutable contract.

## Candidate Placement Domains

These are candidates for investigation, not accepted designs.

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
needs an explicit migration/identity policy rather than silently changing the
meaning of existing block rows.

### A. Class fixed by library

The library carries the complete placement authority:

```text
library.storage_class = hot-na
```

and block writes from that library never derive the class from the request
hostname. This is simple for one library, but insufficient while the same
`(org, block_id)` is deduplicated across libraries that can request different
classes:

```text
library A -> hot-na
library B -> hot-eu
same org + same SHA-256
```

Because `blocks` is currently keyed by `(org, block_id)`, those libraries still
compete to establish one canonical physical class. Library-fixed placement by
itself therefore does not remove the LWT while dedupe remains org-global. The
existing `ChangeStorageClass` endpoint also needs a real migration or an
immutable-placement rule before this can be an authority.

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

This is the first candidate worth characterizing because it matches R23b's
meaning of a storage class as a permanent physical namespace. It is not yet a
schema decision.

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

The current physical locator is still derived by `hashToKey` at
[`internal/storage/blocks.go:309-319`](../internal/storage/blocks.go#L309-L319).
The locator-authority phase must therefore precede any change to the key
formula, as required by the X1 sequencing note.

## X1 Relationship

This investigation does not close X1. It narrows the safe design space:

- X1's physical-delete ABA still requires never-reused exact physical tuples.
- R9 still requires one canonical identity for concurrent installation.
- R12 still governs conditional operations that rely on one global serial
  domain; it is not a reason to add another hot-path consensus round when the
  current production session already uses `SERIAL`.
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
- What happens at metadata concurrency 1, 2, 4 and 8?
- Which upload paths pass `storage_key=""` or derive it again?
- What `storage_class` can two concurrent writers propose for the same logical
  content across libraries and DCs?
- What is the dedupe/storage cost of `(org, storage_class, content_hash)`?
- Can a real race prove that an old writer cannot reinstall a retired
  generation under the candidate schema?

The performance objective for the eventual design is:

```text
ordinary new-block installation: no cross-DC Paxos per block
retirement/reincarnation: global coordination only on exceptional GC transitions
```

Until those invariants and measurements exist, keep the current metadata LWT,
keep production `SERIAL`, do not use `LOCAL_SERIAL` as a production shortcut,
and keep destructive GC disabled under X1.

## References

- [X1 closure options and race ledger](./GC-X1-CLOSURE-OPTIONS.md)
- [Upload-fence PR split plan](./GC-UPLOAD-FENCE-PR-PLAN.md)
- [Upload performance/security analysis, P-4](./UPLOAD-PERFORMANCE-SECURITY-2026-06.md)
- [Open work index, X4](./OPEN-WORK-INDEX.md)
- [Known issue: per-block Paxos](./KNOWN_ISSUES.md#issue-upload-per-block-paxos-01-one-global-paxos-lwt-per-block-on-upload)
- [X1/X2 findings registry](./UPLOAD-FENCE-FINDINGS-REGISTRY.md)
