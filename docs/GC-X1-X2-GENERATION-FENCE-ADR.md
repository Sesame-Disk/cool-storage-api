# ADR: X1/X2 Generational GC Fence

**Status:** Proposed design; implementation not started

**Date:** 2026-08-07

**Audit baseline:** `main` at `186d7800d`

**Target engine:** Cassandra 5.0.x, including the `cassandra:5.0` Compose images

**Deployment model:** greenfield; no legacy block rows, objects, or production data

**Scope:** close `ISSUE-GC-UPLOAD-FENCE-REMATERIALIZATION-01` (X1) and
`ISSUE-GC-CROSS-DC-REFERENCE-VISIBILITY-01` (X2) without adding Paxos to ordinary
references or normal deduplication.

**Operational gate:** keep `GC_ENABLED=false` on every replica in every DC until the
acceptance criteria in this document are implemented and verified.

This document is the normative design record for the proposed X1/X2 work. It records
the reasoning, rejected alternatives, current code evidence, state transitions,
crash recovery rules, implementation phases, tests, rollout constraints, and all
corrections made during the audit. It does not claim that any blocker is closed.

## Document Roles

The repository has several documents with different responsibilities:

- `docs/KNOWN_ISSUES.md` owns live issue status and activation gates.
- `docs/OPEN-WORK-INDEX.md` owns the compact work index.
- `docs/UPLOAD-FENCE-FINDINGS-REGISTRY.md` owns the dated audit evidence and finding history.
- `docs/GC-UPLOAD-FENCE-PR-PLAN.md` records the historical PR-1 through PR-10 series and its research history.
- This ADR owns the proposed X1/X2 protocol and its implementation contract.

None of the existing status documents should mark X1, X2, or destructive GC as
closed until code and verification satisfy this ADR.

## Executive Decision

Use two independent safety mechanisms:

1. **X2:** keep writers regional and fast with `LOCAL_QUORUM`; move global
   coordination into the cold destructive-GC path using `EACH_QUORUM`.
2. **X1:** give every new physical lifecycle a never-reused UUID generation and
   immutable physical `storage_key`, so a delayed delete can target only the old
   generation.

The core protocol is:

```text
writer: read G -> pin G -> confirm/revalidate G -> use K -> publish ref -> remove pin

GC: candidate -> RETIRING -> drain pins -> global refs check
    -> ACTIVE or RETIRED -> DELETING -> persist recovery -> DELETE exact K
```

The following statements are part of the contract:

- A pin is not a permanent liveness reference.
- Finding a pin during `RETIRING` does not justify `RETIRING -> ACTIVE`.
- Only a confirmed permanent reference justifies returning to `ACTIVE`.
- A provisional `up:` or `pub:` reference prevents deletion but does not by itself
  justify `RETIRING -> ACTIVE`; the generation remains fenced until the owning
  operation resolves or expires safely.
- A writer with an ambiguous pin write has no authority to use the generation.
- A writer whose authority deadline has passed cannot publish a reference.
- A generation cannot become `ACTIVE` until its physical object has been stored and verified.
- `DELETING` is recoverable from the generation record even if the orphan projection was not written.
- G2 is forbidden while G1 is `RETIRING` and is allowed only after G1 is `RETIRED`.

## Problem Statements

### X1: Physical-delete ABA

The current physical key is derived from the logical content hash. A GC worker can
authorize and start deleting `blocks/<org>/<hash>`, then a writer can rematerialize
byte-identical content at the same key. The old delete can complete after the new
write and remove live bytes.

Cassandra claim state cannot revoke an S3 delete already in flight. ETag comparison
does not distinguish the old and new lifecycle when the bytes are identical.

X1 closes only when new lifecycles use keys that are never reused:

```text
G1 -> blocks/<org>/<hash>/<generation-1>
G2 -> blocks/<org>/<hash>/<generation-2>
```

### X2: Cross-DC reference visibility

The current `block_references` writes and GC liveness reads inherit the session's
`LOCAL_QUORUM`. With RF 1 per DC, a write acknowledged in EU and a read performed
with a local quorum in USA need not intersect. `SERIAL` on an unrelated conditional
`blocks` update does not make ordinary reference rows globally visible.

The one-hour grace period is mitigation, not a correctness proof.

## Assumptions

- Cassandra uses `NetworkTopologyStrategy`.
- Every writer connects to its local DC through a DC-aware driver policy.
- Each configured DC has the expected replication factor.
- The target engine is Cassandra 5.0.x. Cassandra 5.0.6 contains
  `eachQuorumForRead()` and permits `EACH_QUORUM` reads.
- The current multi-region Compose cluster has two DCs, `usa` and `eu`, with RF 1
  per DC. Production reasoning must also work with higher RF per DC.
- The deployment is greenfield. New objects do not need legacy deterministic-key
  compatibility or backfill.
- Only designated replicas in one DC will run destructive GC after X1/X2 close.
- The existing first-writer metadata LWT remains available for first materialization.

The Cassandra 5.0.6 source used to resolve the read-level question is:

`https://raw.githubusercontent.com/apache/cassandra/cassandra-5.0.6/src/java/org/apache/cassandra/db/ConsistencyLevel.java`

Older Cassandra 3.0 documentation says `EACH_QUORUM` is not supported for reads.
That documentation is not the target contract for this repository; the engine
version must still be asserted by integration tests.

## Consistency Contract

| Operation | Required consistency |
|---|---|
| Initial writer generation read | `LOCAL_QUORUM` |
| Pin insert | `LOCAL_QUORUM` |
| Pin confirmation | `LOCAL_QUORUM` |
| Generation revalidation | `LOCAL_QUORUM` |
| Reuse, dedup, and normal metadata reads | `LOCAL_QUORUM` |
| Provisional/permanent reference insert/delete | `LOCAL_QUORUM` |
| GC discovery and candidate reads | `LOCAL_QUORUM` |
| `ACTIVE -> RETIRING` | LWT serial phase `SERIAL`, regular commit `EACH_QUORUM` |
| Pin drain | `EACH_QUORUM` |
| Final generation-reference check | `EACH_QUORUM` |
| `RETIRING -> ACTIVE` | LWT serial phase `SERIAL`, regular commit `EACH_QUORUM` |
| `RETIRING -> RETIRED` | LWT serial phase `SERIAL`, regular commit `EACH_QUORUM` |
| `RETIRED -> DELETING` | LWT serial phase `SERIAL`, regular commit `EACH_QUORUM` |
| `DELETING -> DELETED` | LWT serial phase `SERIAL`, regular commit `EACH_QUORUM` |

The session default remains `LOCAL_QUORUM`. Critical LWT queries must explicitly
set both levels rather than relying only on the driver default:

```go
query.Consistency(gocql.EachQuorum).
    SerialConsistency(gocql.Serial)
```

The current `internal/db/db.go:94-95` does explicitly assign
`cluster.SerialConsistency`. The current configuration and tests still include
`LOCAL_SERIAL` in the multi-region profiles, so the final X1/X2 test profile must
use `SERIAL`. The global session configuration must not be changed to
`EACH_QUORUM`.

### Why Local Writers And Global GC Are Safe

Suppose a writer in EU confirms a pin or reference with `LOCAL_QUORUM`:

```text
RF=3 in EU
writer W = 2 EU replicas
```

The GC later performs `EACH_QUORUM` and must read a quorum in EU:

```text
GC R = 2 EU replicas
```

Since `W + R > RF`, the sets intersect in EU. With RF 1 per DC, both values are
one and the intersection is the single EU replica. The global destructive read
therefore sees a locally confirmed pin/reference from every DC that accepted one.

This is why the protocol does not put WAN traffic or Paxos into each upload.

### `EACH_QUORUM` Versus `ALL`

`EACH_QUORUM` is preferred over `ALL` for the destructive path. With RF 1 per DC
they are equivalent for the current test topology. With RF 3 per DC,
`EACH_QUORUM` requires a quorum in every DC rather than every replica in every DC,
so it preserves the per-DC safety property while tolerating a replica failure in a
DC. A plain global `QUORUM` is not an equivalent substitute because it can satisfy
the total quorum without reading from one particular DC.

## Identity Model

```text
L = logical block identity, normally SHA-256
G = physical generation identity, UUID/timeuuid
K = immutable physical storage key belonging to G
```

The following are distinct:

```text
Liveness reference:  keeps a generation logically alive
Use pin:             gives a bounded writer authority window
Active pointer:      identifies the generation new work may use
Generation record:  records immutable lifecycle and physical identity
Recovery record:     permits exact retry after a crash
```

The `fs_object` content identity may continue to contain logical SHA values. A
separate immutable binding must record which generation was actually referenced.

## Generation State Machine

```text
MATERIALIZING -> ACTIVE -> RETIRING
                              | \
                              |  \
                              |   +-- permanent references found -> ACTIVE
                              |
                              +------ provisional references found -> RETIRING, resolve
                              |
                              +------ valid pins found -> RETIRING, drain
                              |
                              +------ error/DC unavailable -> RETIRING, retry
                              |
                              +------ pins=0 and refs=0 globally -> RETIRED
                                                                       |
                                                                       v
                                                                    DELETING
                                                                       |
                                                                       v
                                                                    DELETED
```

### `MATERIALIZING`

`MATERIALIZING` means an intent exists for `G` and `K`, but the generation is not
yet active. Readers and normal deduplication must not use it as the active pointer.

Required fields include:

- logical block ID;
- generation ID;
- exact storage key;
- storage class;
- operation/owner ID;
- materialization deadline;
- state and timestamps.

### `ACTIVE`

`ACTIVE` means the authoritative active pointer selects `G`, and the exact object
`K` has been PUT and verified. Readers may resolve the pointer to `K`.

### `RETIRING`

`RETIRING` is a temporary GC fence. New writers cannot use G1 and cannot create G2.
Existing writers with valid pins are allowed to finish or lose authority, but they
must not be treated as proof that G1 is permanently live. A provisional `up:` or
`pub:` row similarly blocks deletion, but the GC waits for its owner/expiry path
instead of treating it as a permanent reference that reactivates G1.

### `RETIRED`

`RETIRED` means the GC has globally confirmed zero pins and zero permanent
references. G2 may now be materialized. G1 remains addressable by its own identity
until its delete is complete.

### `DELETING`

`DELETING` is an irreversible physical-delete authorization for one exact
generation and key. The generation record itself is a recovery source and must
contain the claim, epoch/deadline, storage class, and exact key.

## Retire Ownership

`retire_claim_id` alone is insufficient. The generation also needs:

```text
retire_claim_id
retire_claim_epoch
retire_claim_deadline
```

Every transition after claim acquisition must match the current claim ID and
epoch and must be within the valid ownership deadline. A recovery worker may take
over after the deadline using an LWT. A stale worker that resumes later must fail
its conditional transition.

The same ownership rule applies to `DELETING`. The physical delete must be tied to
the generation and claim that authorized it, not merely to a logical block ID.

## Writer Protocol

### Writer Inventory

The implementation audit must cover every path before any path is allowed to claim
the protocol complete:

- `internal/api/sync.go`;
- `internal/api/seafhttp.go`;
- `internal/api/v2/blocks.go`;
- `internal/api/v2/files.go`;
- `internal/api/v2/file_from_blocks.go`;
- `internal/api/v2/onlyoffice.go`;
- `internal/api/v2/upload_reuse.go`;
- `internal/api/v2/fs_helpers.go`;
- `internal/api/v2/publish_repair.go`;
- `internal/api/v2/batch_operations.go`.

The inventory must locate `ProbeBlockReuse`, `EnsureReusableBlockPresent`, every
direct PUT, metadata materialization, provisional reference, permanent reference,
publish repair, and recovery callback. The Sync no-DB direct-storage fallback must
be removed or fail closed because it cannot participate in the pin/generation
protocol. SeafHTTP finalization must propagate a bounded operation context rather
than hiding the writer lifetime behind `context.Background()`.

### Existing Active Generation

```text
1. Read active G                                  LOCAL_QUORUM
2. Insert pin(G)                                  LOCAL_QUORUM
3. Confirm pin(G) using the same use_id             LOCAL_QUORUM
4. Re-read G and its state                         LOCAL_QUORUM
5. Require state=ACTIVE and a valid authority deadline
6. Reuse K or PUT/repair K
7. Revalidate pin, deadline, state, and exact G
8. Publish the generation-bound reference        LOCAL_QUORUM
9. Confirm an ambiguous reference result
10. Remove pin(G)                                 LOCAL_QUORUM
```

No physical operation is permitted before step 5.

### First Materialization Or Rematerialization

```text
1. Observe no usable active generation
2. Generate UUID G and exact K
3. Persist MATERIALIZING intent/pin                LOCAL_QUORUM
4. PUT K
5. Verify K exists and has the expected metadata
6. Activate through the first-writer/activation CAS
7. If the CAS wins, mark/complete ACTIVE
8. If the CAS loses, do not publish a reference
9. Preserve losing K as an orphan for exact cleanup
```

For the first logical life, the activation is equivalent to the existing
`INSERT ... IF NOT EXISTS`. For rematerialization, the row already exists and the
activation is an update conditional on the old generation:

```text
UPDATE active_generation = G2
WHERE logical_block_id = L
IF active_generation = G1
AND generation_state = RETIRED
```

This remains one activation LWT, not a new LWT per reference or dedup hit.

### Ambiguous Pin Creation

If pin creation returns a timeout or ambiguous Cassandra error:

- the writer has no authority;
- it must confirm the exact `use_id` with `LOCAL_QUORUM`;
- it may retry the idempotent insert with the same identity;
- it must abort if confirmation still fails;
- it must not perform an S3 operation based on an assumption that the pin exists.

### Pin Authority

Every pin stores:

```text
authority_deadline
retention_expires_at
```

The retention expiry must be strictly later than the authority deadline plus:

- maximum clock skew;
- maximum Cassandra write/query time;
- cancellation and retry margin;
- an operational safety margin.

The central publish helper must reject a writer when:

- the pin is absent or belongs to another operation;
- the generation ID does not match;
- the authority deadline has passed;
- the generation is no longer `ACTIVE`;
- the request context has expired;
- the reference result cannot be safely classified.

The helper may remove a pin only after successful reference confirmation. An
ambiguous reference result leaves the pin for retry/recovery.

## First Materialization And Active-Pointer Authority

The design must not have two independent sources that can disagree about which
generation won.

Recommended authority split:

- The logical `blocks` active pointer is authoritative for which generation is
  currently active.
- `block_generations` is authoritative for immutable generation identity, exact
  key, lifecycle state, and recovery data.
- `block_generations.state=MATERIALIZING` is never sufficient to authorize deletion.
- Recovery must always consult the active pointer before cleaning a materializing
  generation.

Crash rules:

| Situation | Recovery action |
|---|---|
| Intent before PUT | Expire intent after its deadline; no object exists to delete |
| PUT before activation CAS | Consult active pointer; if not selected, clean exact K as orphan |
| Active pointer selects G but generation mirror says MATERIALIZING | Complete/repair `ACTIVE`; never delete K |
| Active pointer selects another G | G lost the activation race; clean K only after confirming no references/pins |
| No active pointer and intent expired | Clean exact K after fail-closed confirmation |
| Any uncertain read | Preserve G/K and retry |

The physical PUT must be verifiable before activation. A reader must never infer
that a generation is active merely because a materialization intent exists.

## Reference Identity

`fs_objects.block_ids` may remain logical SHA values. The lifecycle needs a separate
binding such as:

```text
publication or fs_object
logical_block_id
generation_id
referrer
created_at
```

The reference primary key must permit the same logical referrer to exist for
different generations without overwriting another lifecycle. A reverse projection
by referrer is required so cleanup can remove the exact generation binding without
resolving `L` to the current active generation.

Generation identity must flow through:

- provisional `up:` references;
- publish-attempt `pub:` references;
- permanent `fs:` references;
- publish repair rows;
- pending OnlyOffice materialization rows;
- GC candidates and expiry projections;
- queue and DLQ records;
- S3 orphan recovery.

## GC Protocol

```text
1. Discover candidate                              LOCAL_QUORUM
2. ACTIVE G1 -> RETIRING                           SERIAL + EACH_QUORUM
3. Drain valid pins for G1                        EACH_QUORUM
4. Read all refs bound to G1                      EACH_QUORUM
5. If pins remain, keep RETIRING and retry
6. If permanent refs exist, RETIRING -> ACTIVE   SERIAL + EACH_QUORUM
7. If provisional refs exist, remain RETIRING and resolve/await expiry
8. If any read/error/DC failure, keep RETIRING and retry
9. If pins=0 and refs=0 globally, G1 -> RETIRED  SERIAL + EACH_QUORUM
10. Allow G2 only after G1 is RETIRED
11. G1 RETIRED -> DELETING                       SERIAL + EACH_QUORUM
12. Persist/reconstruct recovery for G1 + K
13. DELETE exact K
14. Mark G1 DELETED and clean only G1 metadata
```

The GC must not reactivate a generation merely because a pin exists. A pin can be
an operation that was fenced during revalidation and will never publish.

## Recovery Protocol

`gc_s3_orphans` is a retry/discovery projection, not the only source of truth.

Recovery must scan `block_generations` for:

- expired `MATERIALIZING` intents;
- `DELETING` generations with no orphan row;
- `DELETING` generations with an orphan row in `pending_s3`;
- generations whose S3 delete completed but metadata cleanup did not.

The exact recovery identity is always:

```text
logical_block_id + generation_id + storage_key + storage_class
```

Recovery must never do:

```text
SHA -> current generation -> DELETE
```

It must do:

```text
orphan(G1, key-A) -> DELETE key-A
```

A physical S3 delete is idempotent. If the process dies after the physical delete
but before `DELETED`, retrying `DELETE key-A` and completing the generation state is
safe. If it dies before the orphan projection is written, the `DELETING` generation
record supplies the recovery evidence.

## Proposed Schema

Because the deployment is greenfield, the final generation-aware shape can be the
baseline schema without legacy dual-read code.

### Logical Active Pointer

The existing `blocks` logical row should carry the active generation and exact
active key, for example:

```text
org_id
block_id
active_generation_id
active_storage_key
active_storage_class
active_state
```

### Generation History

```text
block_generations
    org_id
    block_id
    generation_id
    state
    storage_key
    storage_class
    size_bytes
    sha1
    representation_id
    materialization_owner
    materialization_deadline
    retire_claim_id
    retire_claim_epoch
    retire_claim_deadline
    created_at
    updated_at
```

### Uses And References

```text
block_generation_uses
    org_id
    block_id
    generation_id
    use_id
    authority_deadline
    retention_expires_at
    operation_id
```

```text
block_references
    org_id
    block_id
    generation_id
    referrer
    library_id
    created_at
```

The primary key must partition by logical block and generation so the destructive
check can read one generation globally without mixing G1 and G2.

### GC State

Candidates, provisional-expiry rows, queue items, failed items, and orphan rows all
need `generation_id`. Orphans additionally need the immutable `storage_key`.

The baseline schema must ensure that G1 and G2 cannot collide on a candidate,
queue, orphan, or reference row.

## Current Code Evidence

The following current paths must be addressed by implementation PRs:

- `internal/db/block_references.go:814-935` implements `ProbeBlockReuse` without
  generation state and currently validates the content-addressed lifecycle.
- `internal/db/block_references.go:941-977` implements ordinary reference writes and
  local inherited liveness reads.
- `internal/api/v2/upload_reuse.go:115-190` derives keys from hashes and performs
  physical PUT/repair operations.
- `internal/api/v2/upload_reuse.go:198-298` wraps store/materialize/confirm retries,
  making it a major writer integration point.
- `internal/api/v2/fs_helpers.go:985-1019` creates the provisional reference and
  metadata after earlier physical work in several funnels.
- `internal/api/v2/fs_helpers.go:1022-1058` creates permanent references without a
  generation binding today.
- `internal/db/block_references.go:114-128` makes the permanent `fs:` referrer from
  `libraryID` and content-addressed `fsID`; the same referrer identity can therefore
  occur across lifecycle generations unless a separate binding is persisted.
- `internal/api/sync.go:1992-2018` contains a direct-storage fallback with no DB
  metadata or liveness record and is a protocol bypass that must be fail-closed.
- `internal/api/seafhttp.go` has a chunked-finalization path whose context lifetime
  must be included in the authority-deadline audit.
- `internal/gc/worker.go:412-465` performs the current pre-claim and post-claim
  reference checks, both through the local inherited consistency.
- `internal/storage/blocks.go:75-171` writes by derived hash keys, although explicit
  key methods already exist for some operations.
- `internal/streaming/canonical_block_reader.go:29-54,180-247` reads persisted
  canonical locations but must stop assuming the key is always hash-derived.
- `internal/gc/worker.go:403-606` implements the current local claim-then-verify
  delete flow.
- `internal/gc/worker.go:547-594` persists current orphan state before finalizing the
  logical block row, but future recovery must additionally scan `DELETING` generations.
- `internal/gc/worker.go:649-810` recovers by logical `BlockID` today and must become
  generation/key exact.
- `internal/gc/store.go:590-600` exposes physical deletion by logical block ID and
  must expose exact-key deletion.
- `internal/gc/store_cassandra.go:2057-2135` contains current conditional claim,
  release, and finalize operations that must become generation/epoch-aware.
- `internal/db/migrations/001_initial_schema.cql:272-340` has logical block,
  reference, candidate, and provisional tables whose keys omit generation.
- `internal/db/migrations/001_initial_schema.cql:1113-1207` defines queue, DLQ, and
  pending-work identities that must distinguish G1 from G2.
- `internal/db/migrations/001_initial_schema.cql:1239-1263` defines orphan tables
  that currently do not preserve generation or exact storage key.
- `docker-compose.mr-cluster.yaml:245-248,285-288` currently sets `LOCAL_SERIAL`
  and therefore is not final evidence for global LWT behavior.

Adjacent findings remain separate unless implementation deliberately closes them:

- X3 (`ISSUE-UPLOAD-PUT-BEFORE-INTENT-01`) is a storage-leak/discovery issue. The
  durable `MATERIALIZING` intent proposed here can close it for covered writers,
  but the ADR does not claim X3 closed until every writer is verified.
- X4 (per-block first-writer Paxos cost) remains a measurement/performance concern.
  This design retains the existing first-writer LWT and does not add reference LWTs.
- X5, X6, X9, X10, and X11 remain separate audit work and are not activation proof
  for X1/X2.

The current `internal/db/db.go:94-95` explicitly configures the serial consistency,
so the earlier claim that it did not do so is incorrect for the audited checkout.

## Implementation Split

The following is the recommended PR sequence. Each PR must leave the repository safe
with destructive GC disabled.

### PR-0: ADR And Evidence

Documentation only. This document records the design and current evidence. It must
not claim any runtime behavior that has not been implemented.

Before implementation, PR-0/Fase 0 must measure the maximum real writer lifetime
for every funnel, including S3 upload, metadata, reference publication, retry,
publish repair, and client cancellation. The authority deadline and all safety
margins are parameters derived from that inventory, not guessed constants.

### PR-1: Final Greenfield Schema And Models

Add generation, materialization, pin, reference binding, candidate, queue, orphan,
claim, and recovery fields/tables. Add Go models and migration tests.

### PR-2: Explicit Consistency Helpers

Add local writer operations and explicit global destructive operations. Add query
context propagation and fail-closed behavior for all global reads/LWTs.

### PR-3: Generation Allocation And Materialization

Generate UUID keys before PUT, persist durable `MATERIALIZING` intent, verify the
object, activate through first-writer/activation CAS, and record losing objects for
exact orphan cleanup.

### PR-4: Writer Pin Integration

Integrate pin creation, confirmation, revalidation, deadline enforcement, ambiguous
outcome handling, and pin cleanup into every upload/reuse/publish funnel.

### PR-5: Generation-Bound References

Make provisional, publish, permanent, repair, and pending-OnlyOffice references
generation-aware without changing the logical `fs_object` content identity.

### PR-6: Retiring GC And Claim Leases

Implement `RETIRING`, pin drain, global reference checks, claim epoch/deadline
takeover, `RETIRED`, `DELETING`, and exact-key deletion.

### PR-7: Recovery And Readers

Make canonical reads use persisted keys, scan materializing/deleting generations,
rebuild missing orphan projections, and clean only exact generations.

### PR-8: Verification And Activation Gate

Add unit, race, multi-DC, DC outage, crash, delayed-delete, and greenfield rollout
tests. Keep `GC_ENABLED=false` until all acceptance checks pass.

## Verification Plan

### Unit And Race Tests

Required cases include:

- ambiguous `BeginPin` never permits S3 use;
- pin confirmation and revalidation race with `RETIRING`;
- valid pin keeps a generation in `RETIRING` but does not reactivate it;
- permanent reference reactivates a generation;
- deadline expiry rejects publication;
- materializing generation whose active pointer is itself is completed, never deleted;
- materializing generation that lost the CAS becomes an exact orphan;
- `DELETING` with no orphan row is recovered from the generation record;
- stale retire worker cannot transition after claim takeover;
- G1 cleanup cannot remove G2 references, mappings, or objects;
- no physical delete path accepts only logical `blockID`;
- any global read error prevents deletion.

### Cassandra Multi-DC Tests

Use Cassandra 5.0 with `NetworkTopologyStrategy`, RF 1 per DC initially, and a
test profile configured with `SERIAL`, not `LOCAL_SERIAL`.

Required scenarios:

- reference written in USA with `LOCAL_QUORUM`, final GC read from the other DC with
  `EACH_QUORUM`;
- same test in the reverse direction;
- pin written in one DC and drained globally;
- `RETIRE` visibility through a later local writer read;
- DC outage during retire, drain, final reference check, and deleting claim;
- no physical delete after any global operation fails;
- claim takeover after `retire_claim_deadline`;
- G1 delayed delete while G2 is active;
- crash before activation, before orphan projection, after orphan projection, and
  after physical delete.

The existing `scripts/run-mr-cluster.sh` proves Cassandra/MinIO replication and
application replication, but does not yet prove these GC safety properties. A
dedicated integration harness is required.

### Documentation Verification

For this docs-only branch:

```text
git diff --check
```

For the later implementation and integration phases, the repository's Docker-first
verification includes commands equivalent to:

```text
docker compose --profile test run --rm gotest go test ./internal/db ./internal/gc ./internal/api/v2 ./internal/streaming ./internal/storage -short
docker compose --profile test run --rm gotest go test -race ./internal/db ./internal/gc ./internal/api/v2 ./internal/streaming ./internal/storage -short
./scripts/run-mr-cluster.sh up
./scripts/run-mr-cluster.sh status
```

The multi-DC harness must run with the generation-fence test profile and
`CASSANDRA_SERIAL_CONSISTENCY=SERIAL`; the existing replication script alone is
not sufficient evidence.

Also verify that:

- every internal link resolves;
- no document says X1 or X2 is closed;
- `GC_ENABLED=false` remains the operational rule;
- `LOCAL_SERIAL` is not described as final X2 evidence;
- the ADR does not describe unimplemented code as current behavior;
- all proposed table/state names are labeled design unless implemented.

## Rollout And Activation

The greenfield rollout is:

1. Bootstrap the final generation-aware schema.
2. Deploy readers and writers that understand UUID generation keys.
3. Verify all upload funnels use the pin/materialization protocol.
4. Run dry-run GC with no physical deletion.
5. Run crash and multi-DC outage tests.
6. Verify no old deterministic-key writer is present in the deployment.
7. Activate GC only on designated replicas in one DC after X1 and X2 close.
8. Keep all other replicas/DCs at `GC_ENABLED=false`.

There is no rollback to a pre-generation writer after UUID keys are in use. Rollback
must be forward-only: disable destructive GC, deploy a compatible writer, and
preserve generation records and exact keys.

## Cost And Operational Impact

The greenfield scope removes legacy backfill and dual-read work. The planning range
is approximately:

| Work area | Estimate |
|---|---:|
| ADR and complete writer audit | 2-3 engineer-days |
| Schema, models, and consistency helpers | 4-7 engineer-days |
| Materialization and first-writer/activation CAS | 5-8 engineer-days |
| Pins, deadlines, and all writer funnels | 6-9 engineer-days |
| Generation references and publish repair | 4-7 engineer-days |
| Retiring GC, claims, and exact deletes | 6-9 engineer-days |
| Recovery and readers | 4-7 engineer-days |
| Tests, multi-DC drills, and rollout | 7-10 engineer-days |

Expected total: **38-60 engineer-days**, with the actual range refined after the
writer inventory and first schema prototype.

Hot-path cost is expected to be one local pin write, one local confirmation/read,
and one local pin removal per block, with no new WAN round trip and no new Paxos per
reference or ordinary dedup hit.

Cold-path cost is several global reads/LWTs per candidate and intentional dependence
on the slowest participating DC. A DC outage causes retention rather than deletion.

Temporary G1/G2 coexistence consumes additional storage only after G1 reaches
`RETIRED`; G2 is forbidden during `RETIRING`.

## Acceptance Criteria

### X2 Closure

X2 is closed only when:

- every writer pins and revalidates before using a generation;
- ambiguous pin writes grant no authority;
- expired authority cannot publish;
- pins and ordinary references remain `LOCAL_QUORUM`;
- retire, drain, final refs, and deleting use the required global levels;
- pins do not reactivate a generation;
- permanent references can reactivate a generation;
- failed global operations retain `RETIRING` and do not delete;
- multi-DC and DC-outage tests pass;
- no new reference/dedup Paxos exists.

### X1 Closure

X1 is closed only when:

- every new physical lifecycle uses a UUID generation key;
- no key is reused;
- G2 cannot exist before G1 is `RETIRED`;
- G1 and G2 can coexist safely after that point;
- readers use the persisted exact key;
- recovery uses generation and key, never logical hash resolution;
- `DELETING` recovery works without an orphan projection;
- a delayed G1 delete cannot affect G2.

Until both lists pass, destructive GC remains disabled.

## Audit Correction Log

The following corrections are intentionally preserved so future work does not
reintroduce rejected designs:

1. Cassandra 5.0.6 supports `EACH_QUORUM` reads; old Cassandra 3.0 documentation
   is not the target engine contract.
2. `EACH_QUORUM` belongs in the destructive GC path, not pins, references, or normal
   writer reads.
3. `SERIAL` is the Paxos phase; `EACH_QUORUM` is the regular LWT commit/read level.
4. `internal/db/db.go` explicitly configures `cluster.SerialConsistency` in the
   audited branch; critical LWTs should still set it per query.
5. The correct performance requirement is no new Paxos per reference, permanent
   reference, or normal dedup hit, not zero Paxos in all uploads.
6. A TTL is retention, not writer authority.
7. `pin -> ACTIVE` is invalid; only a permanent reference can reactivate during
   `RETIRING`.
8. G2 is forbidden during `RETIRING` and begins only after `RETIRED`.
9. New greenfield objects use UUID keys from their first materialization; no G0
   deterministic-key compatibility path is required.
10. A generation cannot become active before its physical key is PUT and verified.
11. `MATERIALIZING` recovery must consult the authoritative active pointer before
    deleting anything.
12. G2 activation is a CAS conditional on the still-retired G1, not another
    unconditional `INSERT IF NOT EXISTS`.
13. `gc_s3_orphans` is a retry projection, not the sole recovery source.
14. `DELETING` generations must be recoverable after a crash before orphan creation.
15. Retire ownership requires ID plus epoch/deadline so stale workers cannot finish
    after takeover.

## Related Documents

- [Known issues](./KNOWN_ISSUES.md), X1 and X2 remain open.
- [Open work index](./OPEN-WORK-INDEX.md), production activation gate.
- [Upload-fence findings registry](./UPLOAD-FENCE-FINDINGS-REGISTRY.md), audit evidence.
- [Historical upload-fence PR plan](./GC-UPLOAD-FENCE-PR-PLAN.md), PR-1 through PR-10 history.
- [Architecture](./ARCHITECTURE.md), current GC behavior and known limitations.
- [Database guide](./DATABASE-GUIDE.md), current schema and consistency inventory.
- [Multi-region testing](./MULTIREGION-TESTING.md), existing regional test guidance.
- [Session checklist](./SESSION_CHECKLIST.md), required documentation verification.
