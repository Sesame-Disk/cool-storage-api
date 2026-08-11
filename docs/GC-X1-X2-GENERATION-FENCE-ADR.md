# ADR: X1/X2 Generational GC Fence

**Status:** Proposed design; implementation not started

**Date:** 2026-08-07 · **Last updated:** 2026-08-11

**Audit baseline:** `main` at `186d7800d`

**Target engine:** Cassandra 5.0.6. A floating `cassandra:5.0` image is not
sufficient evidence for the acceptance test.

**Deployment model:** greenfield; no legacy block rows, objects, or production data

**Scope:** close `ISSUE-GC-UPLOAD-FENCE-REMATERIALIZATION-01` (X1) and
`ISSUE-GC-CROSS-DC-REFERENCE-VISIBILITY-01` (X2) without adding Paxos to the
writer hot path. The generation fence adds no LWT to pins, revalidation,
references, reuse, repair authorization, or deduplication hits. It adds one logical
activation CAS per materializing request, on the cold path where a retired logical
block comes back to life, with exactly one successful activation per
rematerialization.

SesameFS already contains unrelated coordination LWTs in and around upload
funnels — block metadata first-writer registration, released-stub repair claims,
`sha1`/`representation_id` backfills, block-upload session slots, head-commit
promotion, and file locks. Those are the existing baseline and are outside this
ADR's scope. This document constrains only the Paxos that the generation fence
itself introduces.

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

1. **X2:** keep the normal generation-fence writer path regional with
   `LOCAL_QUORUM`, and confine new global coordination to the destructive-GC path
   and the rematerialization activation CAS using `EACH_QUORUM`.
2. **X1:** give every new physical lifecycle a never-reused UUID generation and
   immutable physical `storage_key`, so a delayed delete can target only the old
   generation.

This is not a claim that every upload is purely regional. A first upload of new
content still pays the existing block-metadata first-writer LWT, and
`configs/config.prod.yaml` sets `serial_consistency: SERIAL`, so that LWT is
already global Paxos in production. That cost is the current baseline (finding
X4) and is unchanged by this ADR.

The core protocol is:

```text
writer: read G -> pin G -> confirm/revalidate G -> use K -> publish ref -> remove pin
        (generation fence adds no LWT on this path)

GC: candidate -> blocks RETIRING -> drain uses -> global refs check
    -> blocks ACTIVE or RETIRED
    -> persist delete-recovery discovery work
    -> generation gc_state=DELETING -> DELETE exact K
    -> generation gc_state=DELETED -> retire discovery work
```

Discovery always precedes the irreversible step, never follows it; see
Discoverability Before Irreversibility.

The following statements are part of the contract:

- A pin is not a permanent liveness reference.
- Finding a pin with live authority during `RETIRING` does not justify
  `RETIRING -> ACTIVE`; it justifies waiting. A generation with one or more
  remaining uses, all of which have expired their authority, does return to `ACTIVE`,
  to release the writer fence rather than to assert liveness.
- Any confirmed generation-bound reference, permanent or provisional, justifies
  returning to `ACTIVE`; a pin holding live authority does not.
- A provisional `up:` or `pub:` reference prevents deletion but does not by itself
  represent writer authority; it is nevertheless sufficient to return the
  generation to `ACTIVE` because reactivation is retention-safe and prevents a
  dead publish attempt from parking a hot logical block for its full TTL.
- A writer with an ambiguous pin write has no authority to use the generation.
- A writer whose authority deadline has passed cannot publish a reference.
- A generation cannot become `ACTIVE` until its physical object has been stored and verified.
- A materializer holds generation-use authority from before its PUT until after it
  publishes or abandons its reference. Winning the activation CAS does not end that
  authority; only release, expiry, or abandonment does.
- A `PENDING` writer use with an unexpired authority deadline is potential authority
  during the drain. It must keep `RETIRING` until it is authorized or expires;
  `PENDING` is not permission to perform physical work, but it may have been
  revalidated before the fence became committed.
- Every generation that can be used by an in-flight operation has a corresponding
  use row. There is no writer, including the materializer, that is invisible to the
  GC drain.
- `DELETING` is discoverable from a durable work row written before the transition, and the generation record supplies the exact key once found, even if the orphan projection was never written.
- G2 is forbidden while G1 is `RETIRING` and is allowed only after G1 is `RETIRED`.
- The generation fence adds no LWT to pins, revalidation, references, reuse, repair
  authorization, or deduplication. It adds one logical activation CAS per
  materializing request; ambiguity retries reuse the same generation and epoch and
  cannot create a second generation.
- A global check that observes zero uses and then zero references closes the
  publication frontier for that generation. The read order is part of the proof.

## Problem Statements

### X1: Physical-delete ABA

The current physical key is derived from the logical content hash. A GC worker can
authorize and start deleting `blocks/<org_id>/<h0:2>/<h2:4>/<hash>`, then a writer
can rematerialize byte-identical content at the same key. The old delete can
complete after the new write and remove live bytes.

Cassandra claim state cannot revoke an S3 delete already in flight. ETag comparison
does not distinguish the old and new lifecycle when the bytes are identical.

The current layout is sharded by the first four hash characters:

```text
blocks/<org_id>/<h0:2>/<h2:4>/<hash>
```

X1 closes only when new lifecycles preserve that layout and add a never-reused
generation suffix:

```text
G1 -> blocks/<org_id>/<h0:2>/<h2:4>/<hash>.<generation-1>
G2 -> blocks/<org_id>/<h0:2>/<h2:4>/<hash>.<generation-2>
```

### X2: Cross-DC reference visibility

The current `block_references` writes and GC liveness reads inherit the session's
`LOCAL_QUORUM`. With RF 1 per DC, a write acknowledged in EU and a read performed
with a local quorum in USA need not intersect. `SERIAL` on an unrelated conditional
`blocks` update does not make ordinary reference rows globally visible.

The one-hour grace period is mitigation, not a correctness proof.

## Assumptions

- Cassandra uses `NetworkTopologyStrategy`. This is a hard requirement, not a
  preference; see "`NetworkTopologyStrategy` Is Load-Bearing" below.
- Every writer connects to its local DC through a DC-aware driver policy.
- The expected DC set is explicitly configured, exactly matches the keyspace DC set,
  and every participating DC has exactly its configured RF, which must be greater
  than zero.
- The target engine is Cassandra 5.0.6. Cassandra 5.0.6 contains
  `eachQuorumForRead()` and permits `EACH_QUORUM` reads.
- The current multi-region Compose cluster has two DCs, `usa` and `eu`, with RF 1
  per DC. Production reasoning must also work with higher RF per DC.
- The deployment is greenfield. New objects do not need legacy deterministic-key
  compatibility or backfill.
- Only designated replicas in one DC will run destructive GC after X1/X2 close.
- The existing terminal first-writer metadata LWT remains available for first
  materialization and may initialize the first active pointer in that same LWT,
  so a first physical lifecycle needs no additional activation Paxos.
- Once the generation fence is enabled, every conditional write that can touch the
  logical `blocks` pointer partition uses the global `SERIAL` Paxos domain. This
  includes the existing first-writer/materialization LWT and the new pointer CASs;
  `LOCAL_SERIAL` is not a valid setting for that partition in the generation-fence
  profile. Unrelated LWTs on other partitions may keep their existing consistency.
- Rematerialization of a `RETIRED` logical block performs one logical activation
  operation per materializing request. It runs inline in the request that
  materialized the new generation; ambiguity retries reuse its same generation and
  epoch.

The Cassandra 5.0.6 source used to resolve the read-level question is:

`https://raw.githubusercontent.com/apache/cassandra/cassandra-5.0.6/src/java/org/apache/cassandra/db/ConsistencyLevel.java`

Older Cassandra 3.0 documentation says `EACH_QUORUM` is not supported for reads.
That documentation is not the target contract for this repository; the engine
version must still be asserted by integration tests.

## Consistency Contract

| Operation | Required consistency |
|---|---|
| Initial writer generation read | `LOCAL_QUORUM` |
| `MATERIALIZING` intent insert (step 3a) | `LOCAL_QUORUM`, confirmed on its own before the use |
| `AUTHORIZED` materializer use insert (step 3b) | `LOCAL_QUORUM`, confirmed on its own before the discovery row |
| Materialization discovery projection (step 3c) | `LOCAL_QUORUM`, confirmed before the PUT; an unconfirmed write means no PUT |
| Materializer use release | `LOCAL_QUORUM` |
| Pin insert | `LOCAL_QUORUM` |
| Pin confirmation | `LOCAL_QUORUM` |
| Generation revalidation | `LOCAL_QUORUM` |
| Generation lifecycle-state inspection after a known result, including `QUARANTINED` | `LOCAL_QUORUM` |
| Reuse, dedup, and normal metadata reads | `LOCAL_QUORUM` |
| Provisional/permanent reference insert/delete | `LOCAL_QUORUM` |
| GC discovery and candidate reads | `LOCAL_QUORUM` |
| Existing terminal first-writer metadata LWT (initial pointer only) | Existing LWT, but `SERIAL` phase is mandatory when it can touch a generation-managed `blocks` partition; its regular commit level remains the existing writer level. The fence adds no second LWT here |
| Rematerialization `G1 RETIRED -> G2 ACTIVE` activation operation | LWT serial phase `SERIAL`, regular commit `EACH_QUORUM` on `blocks`; runs inline in the materializing request; physical CAS executions may retry the same logical operation |
| `ACTIVE -> RETIRING` (active pointer, claim acquisition) | LWT serial phase `SERIAL`, regular commit `EACH_QUORUM` on `blocks`; the `IF` clause must also match the observed `retire_claim_epoch` so the counter stays strictly monotonic |
| Claim takeover after `retire_claim_deadline` | LWT serial phase `SERIAL`, regular commit `EACH_QUORUM` on `blocks`; same epoch-matching shape, replaces the owner without changing `active_state` |
| Use-row drain, including `PENDING`, `AUTHORIZED`, and materializer uses | `EACH_QUORUM` |
| Final generation-reference check | `EACH_QUORUM` |
| `RETIRING -> ACTIVE` (active pointer) | LWT serial phase `SERIAL`, regular commit `EACH_QUORUM` on `blocks` |
| Retirement evidence append (one row per `(generation, claim_epoch)`) | LWT serial phase `SERIAL`, regular commit `EACH_QUORUM`; `INSERT ... IF NOT EXISTS`, immutable and ordered before the pointer CAS |
| `RETIRING -> RETIRED` (active pointer) | LWT serial phase `SERIAL`, regular commit `EACH_QUORUM` on `blocks` |
| Crashed-activation settlement read | `SELECT` on the `blocks` partition at read consistency `SERIAL`. This is the single normative form; an ordinary read at any level does not settle a pending proposal |
| `gc_state = null -> DELETING` (generation lifecycle) | LWT serial phase `SERIAL`, regular commit `EACH_QUORUM` on the G1 generation row; `IF gc_state = null AND materialization_state = VERIFIED AND storage_key = K1`, issued after the authoritative pointer/evidence proof and recording the authorizing claim |
| `DELETING -> DELETED` (generation lifecycle) | LWT serial phase `SERIAL`, regular commit `EACH_QUORUM` on the G1 generation row; `IF gc_state = DELETING` |
| Transition to `gc_state = QUARANTINED` (generation lifecycle) | LWT serial phase `SERIAL`, regular commit `EACH_QUORUM` on the generation row; `IF gc_state = null AND storage_key = K`, plus the `materialization_state`/operation-ID identity in the `MATERIALIZING` form |
| Ambiguous generation-lifecycle LWT reconciliation | Re-issue the same idempotent LWT with serial phase `SERIAL` and regular commit `EACH_QUORUM`; an ordinary read is not sufficient to settle a pending proposal |

The session default remains `LOCAL_QUORUM`. New critical lifecycle LWT queries
must explicitly set both levels rather than relying only on the driver default:

```go
query.Consistency(gocql.EachQuorum).
    SerialConsistency(gocql.Serial)
```

The current `internal/db/db.go:94-95` does explicitly assign
`cluster.SerialConsistency`. The current configuration and tests still include
`LOCAL_SERIAL` in the multi-region profiles, so the final X1/X2 profile must use
`SERIAL` for every LWT that can touch `blocks`; a startup gate must reject
`LOCAL_SERIAL` under the generation-fence writer-mode gate. The global session
configuration must not be changed to `EACH_QUORUM`; regular global operations set
that level per query.

The existing terminal first-writer LWT retains its current writer-path consistency;
the generation fence must not add a second LWT around it.

The hot writer path must not add an `IF` mutation for pins, revalidation,
references, reuse, repair authorization, or deduplication. Those are the
per-request, per-block operations whose cost multiplies, and the fence keeps all of
them at ordinary `LOCAL_QUORUM` writes and reads.

Two lifecycle transitions are the deliberate exception, and neither scales with
request volume:

- **First materialization** reuses the existing block-metadata first-writer LWT and
  initializes the active pointer in that same operation.
- **Rematerialization** performs one logical activation operation when a `RETIRED`
  logical block comes back to life. It runs inline in the materializing request;
  ambiguity retries reuse the same generation and epoch.

Retirement and delete LWTs belong to the GC worker and never to a request handler.

### LWT Taxonomy

Every LWT in this system belongs to exactly one of four groups. An implementer must
be able to name the group before adding one, because "the generation fence uses
Paxos" is not licence to write `pin IF ...`, `reference IF ...`, or `reuse IF ...`:

| Group | Members | Frequency | Owner |
|---|---|---|---|
| **Existing hot path** | Block-metadata first-writer `INSERT ... IF NOT EXISTS`, released-stub repair claims, `sha1`/`representation_id` backfills, block-upload session slots, `gc_leases` finalize lease, head-commit promotion, file locks | Per block or per request | Unchanged baseline (X4) |
| **New hot path** | **None** | — | — |
| **Rare rematerialization** | `G1 RETIRED -> G2 ACTIVE` activation | Only when a retired SHA comes back to life | Materializing request, inline |
| **GC cold path** | `ACTIVE -> RETIRING`, retirement evidence `INSERT ... IF NOT EXISTS`, `RETIRING -> ACTIVE`, `RETIRING -> RETIRED`, `gc_state = null -> DELETING`, `DELETING -> DELETED`, quarantine | Per collected generation | GC worker |

The rule this taxonomy exists to protect:

> Zero new Paxos for any operation that happens once per normal block. One global
> Paxos only when a new generation is born after `RETIRED`.

The GC cold path can reach five or six LWTs per collected generation. That is
accepted deliberately: it is off the upload path, its cost buys "when in doubt, do
not delete", and GC must use bounded, measured concurrency so those rounds make
progress without overwhelming Cassandra alongside user traffic. A strictly serial
worker is not sufficient once each generation pays several WAN-sensitive LWTs; PR-6
must define a concurrency limit and a measured queue-throughput target. A DC outage
stalls collection rather than weakening deletion correctness.

Note that the existing hot-path group is not zero-cost today, and this ADR does not
make it so. See The Existing Per-Block Paxos Is The Real Latency Cost.

### The Existing Per-Block Paxos Is The Real Latency Cost

It is tempting to read the taxonomy as "the normal upload path is Paxos-free". It
is not, and the difference matters when choosing where to spend effort on latency.

`UpsertBlockMetadataWithSHA1` (`internal/db/block_references.go:569-600`) runs the
first-writer LWT unconditionally, with no pre-read:

```go
for attempt := 0; attempt < 2; attempt++ {
    applied, err := insert()   // INSERT ... IF NOT EXISTS
```

An `INSERT ... IF NOT EXISTS` pays the full Paxos round **whether or not it
applies**. The upload cycle in `retryUploadedBlockMaterialization` is
`store() -> materialize() -> store()`, where `store()` runs `ProbeBlockReuse` (an
ordinary `SELECT` on `blocks`) and `materialize()` runs `RegisterUploadedBlock`,
which calls the LWT. Both the `Reusable` and `NeedsPut` probe outcomes go through
`materialize()`. Therefore:

> Every block of every upload pays one global Paxos round in production today,
> including pure deduplication hits where the row already exists and the LWT
> provably cannot apply.

It is global because `configs/config.prod.yaml:52` sets
`serial_consistency: SERIAL`. This is finding X4, and it is the only Paxos in the
system whose count scales with block volume.

Two measurements belong in PR-0 before any latency decision is made:

- **`paxos_variant`.** Nothing in this repository sets it, so the engine default
  applies. The variant materially changes the round-trip count of every LWT above,
  and therefore changes the WAN cost of both the existing hot path and the GC cold
  path. Measure the deployed value and the effect of changing it, rather than
  assuming either.
- **Activation critical path**, including the retirement-evidence `EACH_QUORUM`
  read plus the `SERIAL + EACH_QUORUM` activation CAS, with p50/p95/p99 from each
  participating DC to each other. The inline-versus-background question is a
  question about this complete path, not the CAS alone, and it must not be answered
  by assumption.

A probe fast path is the obvious lever and is compatible with this design: the
probe has already read the row, so a `Reusable` outcome with complete metadata can
skip the LWT entirely. The asymmetry makes it safe — a false negative (a
`LOCAL_QUORUM` probe not seeing a row written in another DC) falls through to the
LWT and is correct, while a false positive is impossible because a row that was
never written cannot be read. Placement columns are first-writer-wins and
immutable, so a complete row has no race left to resolve.

That work is X4, not this ADR. It is recorded here because a reader who concludes
from the taxonomy that per-block Paxos is already solved will optimize the wrong
path, exactly as an earlier revision of this document did.

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

This intersection holds for RF greater than one per DC because a Cassandra
`EACH_QUORUM` read contacts a quorum in every datacenter rather than the nearest
`blockFor` replicas: `ReplicaPlans.contactForEachQuorumRead()` filters candidates
against a per-DC requirement map. A plain global `QUORUM` would not give this
property.

This is why the protocol adds no WAN traffic or Paxos to pins, references, reuse,
or deduplication. New global Paxos is confined to GC lifecycle transitions and the
activation CAS a materializing request makes when it rematerializes a retired
block.

### `NetworkTopologyStrategy` Is Load-Bearing

The entire X2 argument rests on `EACH_QUORUM` contacting a quorum in every DC. In
Cassandra 5.0.6 that behaviour is conditional on the keyspace using
`NetworkTopologyStrategy`, and the two failure modes are not symmetric:

| Path | Behaviour under a non-NTS keyspace |
|---|---|
| `EACH_QUORUM` **read** | `ReplicaPlans.contactForRead()` only routes to `contactForEachQuorumRead()` for `NetworkTopologyStrategy`. Otherwise it falls back to the ordinary `blockFor` selection, which is a plain global quorum. **The read succeeds and silently loses the per-DC intersection property.** |
| `EACH_QUORUM` **LWT commit** | `ConsistencyLevel.validateForCasCommit()` calls `requireNetworkTopologyStrategy()` and throws `InvalidRequestException`. Loud, fail-closed. |

The read case is the dangerous one: a deployment on `SimpleStrategy` would pass a
naive smoke test while providing exactly the guarantee X2 says is insufficient — a
global quorum that need not read from any particular DC.

This repository still supports `SimpleStrategy` as a legacy fallback
(`docker-compose.prod.yml:174,182`), so the assumption cannot be left implicit.
### Two Gates, Not One

The startup assertions in this document belong to **two different gates**, and
conflating them leaves the earlier one unenforced during the window it exists for:

```text
generation-fence writer mode enabled       (rollout step 2: writers deployed)
    NetworkTopologyStrategy verified
    DC set and per-DC RF verified
    SERIAL domain verified for every LWT touching blocks
    paxos_variant retains linearizable reads

destructive GC enabled                     (rollout step 7)
    requires the writer gate above, plus
    all X1/X2 acceptance criteria verified
```

Every assertion in the first block binds from the **first generation-aware write**,
not from `gc.enabled=true`. The reason is concrete: a materializer can crash between
its PUT and its activation LWT on day one of the writer rollout, and the recovery
that follows needs the serial round and the correct topology — five rollout steps
before destructive GC is switched on. Wherever this document says "when
`gc.enabled=true`" for one of those four checks, it means the writer gate.

The implementation must:

- verify the keyspace replication strategy at startup, and refuse to boot when
  generation-fence writer mode is enabled and the strategy is not
  `NetworkTopologyStrategy`;
- verify that the keyspace DC set exactly matches the configured participating DC
  set and that every participating DC has exactly its configured RF, with RF > 0;
- verify that the generation-fence consistency profile uses `SERIAL` for every
  conditional write that can touch the `blocks` pointer partition. `LOCAL_SERIAL`
  may remain available for unrelated partitions, but mixing serial domains on the
  same pointer partition is not accepted. **This gate binds from the first
  generation-aware write, not from `gc.enabled=true`**: a deployment that writes
  generations under `LOCAL_SERIAL` and only switches to `SERIAL` when GC is enabled
  leaves each `blocks` partition with Paxos history produced under two different
  participant sets, which is the very condition the gate exists to prevent. The
  rollout sequence below deliberately activates GC several steps after writers go
  live, so tying the gate to `gc.enabled` would leave exactly that window open;
- treat those checks as the generation-fence writer-mode gate, which is a
  prerequisite of the destructive-GC activation gate — not as an operational note.

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
Generation record:  records immutable physical identity and monotonic GC markers
Recovery record:     permits exact retry after a crash
```

The `fs_object` content identity may continue to contain logical SHA values. A
separate immutable binding must record which generation was actually referenced.

## Generation State Machines

```text
blocks pointer:
    ACTIVE -> RETIRING -> RETIRED
                 |           |
                 |           +-- G2 may activate through the pointer CAS
                 |
                 +-- error, reference, or expired uses -> ACTIVE

generation row:
    materialization_state: MATERIALIZING -> VERIFIED
    gc_state:              null -> DELETING -> DELETED
                              \-> QUARANTINED
```

A reference is proof of liveness; a use is proof of pending or authorized work. That
is why references are evaluated first in the GC decision, even though uses are read
first to establish the publication frontier. The `blocks` pointer is the only
authority for `ACTIVE`, `RETIRING`, and `RETIRED`; the generation row never mirrors
those states through a second mutable state machine.

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
- `materialization_state` and timestamps;
- `gc_state` and claim fields when physical cleanup begins.

The materialization intent is **accompanied by** a generation-use row, not identical
to it. Step 3b writes a separate row for `(G, use_id)` in the `AUTHORIZED` state —
`AUTHORIZED` from creation because the materializer owns `G` by construction: no
other operation can be using a generation that does not exist yet. The two live in
different tables and different partitions and are written and confirmed
independently; see First Materialization Or Rematerialization. That use row is what
makes the materializer visible to the GC drain, and it is held across the PUT, the
activation CAS, and the reference publication.

Without it there is a live-data hole. A materializer that wins activation creates a
generation that is `ACTIVE` with zero references, which is exactly the shape of a GC
candidate. If the materializer had no use row, the GC could retire, drain nothing,
find no references, and delete `K` while a successful upload was still preparing to
publish its reference.

### `VERIFIED`

`VERIFIED` is a monotonic generation-row marker meaning that the exact object `K`
was PUT and verified. It is not an active-pointer state and cannot authorize reads,
publication, or deletion. The pointer CAS is the only operation that selects a
generation for new work.

### `ACTIVE`

`ACTIVE` means the authoritative active pointer selects `G`, and the exact object
`K` has been PUT and verified. Readers may resolve the pointer to `K`.

### `RETIRING`

`RETIRING` is a temporary GC fence. New writers cannot use G1 and cannot create G2.
An existing writer whose pin was already authorized for G1 may finish and publish
while G1 is `RETIRING`; the GC sees the pin and waits. A writer with a `PENDING`
pin cannot publish until it revalidates while G1 is `ACTIVE`, but an unexpired
`PENDING` row still blocks retirement because it may have been revalidated before
the committed fence.

A generation-bound `up:`, `pub:`, or `fs:` row found by the final global check
returns G1 to `ACTIVE`. This is safe even when a provisional row is stale: the
result is temporary retention, not deletion. The owner/expiry path can later
remove the row and create a fresh candidate.

`RETIRING` is a fence on writers, so it must not become a parking state. It ends on
any of three outcomes: a reference is found, at least one use exists and every
remaining use has expired its authority, or the drain reaches zero. Never on "some
row still exists". See Escaping `RETIRING` On Abandoned Uses.

### Retirement Evidence

Retirement evidence is not a state. It is an append-only row meaning "a global zero
check for this generation passed under this claim epoch", written before the pointer
CAS and never overwritten. It is not visible on the active pointer and never
authorizes deletion by itself; the authoritative pointer must also have left
`RETIRING`. See Retirement Handoff.

### `RETIRED`

`RETIRED` is an authoritative `blocks.active_state` value. It means the GC has
established the publication frontier at `EACH_QUORUM`:
it observed zero use rows first, then zero generation-bound references, so no
operation capable of legally publishing a reference remains. G2 may now be
materialized. G1 remains addressable by its own identity until its delete is
complete; after physical deletion, the logical pointer may still retain G1 as a
historical retired predecessor until G2 replaces it.

The guarantee is about authority, not about row absence. The protocol cannot
prevent a use row from appearing after the final read: a writer may have observed
`ACTIVE` before the fence and insert its `PENDING` row afterwards. That is safe and
does not invalidate `RETIRED`, because the writer's revalidation is ordered after
its own insert and will observe `RETIRING` or later, so the row can never be
promoted to `AUTHORIZED`.

The normative authority statement is therefore:

```text
RETIRED means no use row observed at the first global read could acquire
authority, and no reference existed at the second global read.
```

An implementation must not read `RETIRED` as "no use row can exist from now on" and
must not build a drain loop that waits for that property. It is unachievable
without an admission LWT, which this design deliberately does not add.

The same distinction, applied to publication rather than to rows, is the
publication frontier: the pair of global reads proves that no operation can still
acquire the authority needed to publish, which is why no further reference check is
required between the retirement persist and the pointer CAS.

### `DELETING`

`gc_state=DELETING` is an irreversible physical-delete authorization for one exact
generation and key. The generation record itself is a recovery source and must
record the authorizing claim ID and epoch, the storage class, and the exact key.
Those claim columns are written by the `DELETING` transition and are recovery
evidence, not a precondition; see Conditional Generation-Lifecycle Transitions.
Physical cleanup must never delete the logical `blocks` pointer row.

## Retire Ownership

`retire_claim_id` alone is insufficient. The logical `blocks` pointer row — the only
place a live retirement claim is ever stored — also needs:

```text
retire_claim_id
retire_claim_epoch
retire_claim_deadline
```

Every **GC-owned reversible** pointer transition — `ACTIVE -> RETIRING`,
`RETIRING -> ACTIVE`, `RETIRING -> RETIRED` — must match the current claim ID and
epoch and must be within the valid ownership deadline. Generation-row transitions do
not, and cannot; see Conditional Generation-Lifecycle Transitions.

The activation CAS is deliberately **not** subject to the deadline rule. It is
issued by a materializing request that holds no GC claim at all, and its safety comes
from a different property: `RETIRED` is terminal for G1, so an expired-but-uncontested
claim still describes a generation no writer can use. If a takeover did occur, it
bumped `retire_claim_epoch`, and the materializer's CAS on `(C1, N1)` simply fails —
which is the correct outcome and needs no clock comparison. Applying the deadline
rule to activation would be unimplementable anyway, since Cassandra cannot compare
against "now"; stating it broadly invited an implementer to look for a check that
cannot exist.

A recovery worker may take over after the deadline using an LWT of the same shape as
acquisition, conditioned on the epoch it observed:

```text
takeover
    IF active_generation_id = G1
    AND active_state       = RETIRING
    AND retire_claim_epoch = Nobserved
    SET retire_claim_id       = Cnew
        retire_claim_epoch    = Nobserved + 1
        retire_claim_deadline = Dnew
```

It does not change `active_state`; it only replaces the owner. A stale worker that
resumes later fails its conditional transition, because every transition it could
still attempt names `retire_claim_epoch = Nobserved`.

That is a testable condition, not a convention, so the GC-owned pointer transitions
carry it in the `IF` clause rather than in a preceding read:

```text
ACTIVE -> RETIRING
    IF active_generation_id = G1
    AND active_state       = ACTIVE
    AND active_epoch       = E1
    AND retire_claim_epoch = Nprev        <-- the value this worker observed
    SET active_state       = RETIRING
        retire_claim_id       = Cnew
        retire_claim_epoch    = Nprev + 1
        retire_claim_deadline = Dnew

RETIRING -> ACTIVE                RETIRING -> RETIRED
    IF active_generation_id = G1      IF active_generation_id = G1
    AND active_state    = RETIRING    AND active_state       = RETIRING
    AND active_epoch    = E1          AND active_epoch       = E1
    AND retire_claim_id    = C        AND retire_claim_id    = C
    AND retire_claim_epoch = N        AND retire_claim_epoch = N
```

**Claim acquisition must condition on the previous `retire_claim_epoch`.** Without
it the acquisition is the one GC-owned transition a stale worker can still win,
because `active_epoch` deliberately does not change across GC cycles:

```text
A observes G1 / E1 / ACTIVE / retire_claim_epoch = 0, then stalls
B:  ACTIVE -> RETIRING (epoch 1) -> ... -> RETIRING -> ACTIVE
C:  ACTIVE -> RETIRING (epoch 2) -> ... -> RETIRING -> ACTIVE
A wakes. G1 ✓ ACTIVE ✓ E1 ✓  -> A's CAS applies and installs epoch 1 again
```

The damage is to epoch uniqueness, which the evidence table depends on. `A` now
holds claim `C_A` at epoch 1 while B's completed cycle already wrote
`evidence(G1, 1)` under claim `C_B`. Two things then go wrong at once: A's own
evidence append for `(G1, 1)` is an `INSERT ... IF NOT EXISTS` that will not apply
and whose payload conflicts, so A fails closed permanently; and A's next cycle would
collide with C's `evidence(G1, 2)`. The block becomes unretirable.

What prevents A from *deleting* on B's evidence is a separate rule — evidence is
valid only when the claim ID matches too, so A reads `C_B` where it expects `C_A`
and treats the row as a protocol violation. Both defences are required and neither
substitutes for the other: the epoch CAS keeps the counter a total monotonic
sequence, and the claim-ID match binds each evidence row to the worker that earned
it.

The first-writer LWT must therefore initialize the claim columns explicitly
alongside the initial pointer:

```text
retire_claim_epoch    = 0
retire_claim_id       = null
retire_claim_deadline = null
```

This is not the missing-row hazard of correction 71 — the same `IF` clause also
matches `active_generation_id`, `active_state`, and `active_epoch`, none of which
can hold against an absent partition. The reasons are simpler: the first
acquisition needs no special case, `Nprev + 1` works from the first cycle, the
counter is total rather than sparse, and `null` never has to mean both "not
initialized" and "no retirement yet" at the same time.

A worker whose claim was taken over fails these conditions and cannot complete a
transition it no longer owns. The deadline itself is not in the `IF` clause —
Cassandra cannot compare against "now" — so it is enforced by the takeover LWT that
bumps `retire_claim_epoch`, which is what invalidates the stale worker's condition.

The activation CAS carries the predecessor claim ID and epoch even though the
materializing request holds no GC claim. Its safety comes from the complete
`G1 / E1 / RETIRED / C1 / N1` condition, which only the claim-holding retire worker
could have installed, plus the immutable predecessor record and matching evidence
check. Conditioning on the claim tuple rather than on `active_state = RETIRED`
alone matters because `active_epoch` deliberately does not change across GC cycles:
without it, a materializer that observed the pointer during retire cycle N1 could
activate during cycle N2.

The same ownership rule applies to `DELETING`, with one structural difference. The
physical delete must be tied to the generation and the claim that authorized it,
not merely to a logical block ID — but on the generation row that binding is
*recorded*, because the pointer proof that precedes it is already irrevocable. Only
the reversible pointer transitions match a claim tuple in an `IF` clause. See
Conditional Generation-Lifecycle Transitions.

The claim epoch/lease can prevent stale Cassandra transitions; it cannot revoke an
S3 `DELETE K` request that has already left the process. That is intentional and is
safe only because K is never reused: a late delete of K1 cannot affect K2.

### Ambiguous Active-Pointer Transition

An ambiguous `ACTIVE -> RETIRING` LWT is not settled by an ordinary
`EACH_QUORUM` read. A Cassandra serial proposal may have been accepted by part of
the Paxos cohort without its commit being learned; a normal read can still return
`ACTIVE`, and a later LWT may replay the pending proposal. Starting the drain from
that ordinary read would let a writer observe `ACTIVE` and acquire authority before
the fence becomes committed.

The worker must therefore settle an ambiguous pointer transition before reading uses
or references:

```text
ambiguous ACTIVE -> RETIRING result
    -> serializable reconciliation of the blocks row, or re-issue the same LWT
    -> matching RETIRING claim/epoch: transition is committed; begin the drain
    -> ACTIVE: re-issue the same claim LWT; do not drain yet
    -> different claim/epoch or terminal state: stale worker; stop
    -> uncertain serial result or unavailable DC: retain ACTIVE/RETIRING and retry
```

The reconciliation must use the `SERIAL` Paxos domain for this `blocks` partition;
`EACH_QUORUM` remains the regular commit/read level for the LWT and for the later
destructive checks. No use/reference read, retirement evidence append, G2
activation, or physical delete is allowed until the transition has a committed
claim. This is an ambiguity-only extra execution and does not add Paxos to the
successful normal writer path.

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

### Physical Key Parsing Inventory

The key changes from `blocks/<org_id>/<h0:2>/<h2:4>/<hash>` to
`blocks/<org_id>/<h0:2>/<h2:4>/<hash>.<generation>`. Replacing the key derivation
helper is not sufficient; every consumer that infers meaning from the key string
must be found and audited. The inventory must locate code that:

- takes the basename of a storage key and assumes it equals the SHA;
- splits or slices a storage key to recover org, shard, or hash components;
- lists an S3 prefix to enumerate or discover blocks;
- reconstructs a logical block ID from a physical object name;
- validates the length or character set of the final path segment;
- recovers orphans or block mappings from the physical name.

Wherever a persisted `storage_key` is available, consumers must use it directly
instead of parsing or re-deriving. Parsing is permitted only where the physical
object is the sole surviving evidence.

No such consumer exists today: `internal/storage/s3.go:703` exposes a generic
`List`, but it has no caller anywhere in `internal/` or `cmd/`. The "lists an S3
prefix" and "recovers orphans from the physical name" categories of this inventory
are expected to come back empty against the audited checkout, and that is the
correct result — it means the only work is the derivation helper and the reader
validation. The categories stay in the inventory because a future bucket-scan
recovery tool is the one place where key parsing would be legitimate, and it must
be written against the generation-suffixed layout from the start.

Required test:

```text
key = blocks/<org>/ab/cd/<64-char-sha>.<uuid>

parser yields exactly:
    org_id
    logical hash
    generation_id
```

### Existing Active Generation

```text
1. Read active G and epoch                         LOCAL_QUORUM
2. Insert pin(G, epoch, PENDING)                  LOCAL_QUORUM
3. If step 2 was ambiguous, confirm pin(G)         LOCAL_QUORUM
   using the same use_id; on success, skip
4. Re-read the active pointer and generation row  LOCAL_QUORUM
5. Authorize only if G/epoch is still ACTIVE,
   the generation row satisfies the positive predicate
   (gc_state = null AND materialization_state = VERIFIED),
   AND this pin's own authority_deadline
   has not passed
6. Re-check the positive predicate and authority
   deadline, then reuse K or PUT/repair K
7. Validate AUTHORIZED pin, deadline, and epoch
8. Publish the generation-bound reference         LOCAL_QUORUM
9. Confirm an ambiguous reference result
10. Remove pin(G, use_id)                          LOCAL_QUORUM
```

The pin becomes `AUTHORIZED` only after step 5. A writer that observes `RETIRING`,
`RETIRED`, or a different generation on the pointer, or whose generation row fails
the positive predicate for any reason — `QUARANTINED`, `DELETING`, `DELETED`, or not
yet `VERIFIED` — has no authority and must not perform a physical operation.
(`DELETING` and `DELETED` are not pointer states; see Which States Live Where.)

For a writer, a generation that is not `VERIFIED` is simply not usable yet: it fails
closed and retries. It does **not** quarantine on that observation; see
`VERIFIED` Lag Is Not A Contradiction.

`authority_deadline` is allocated once when the `PENDING` use is created. Promoting
`PENDING -> AUTHORIZED` must carry the same deadline and may not silently extend
the window that the GC classified during its drain. A renewal is a new bounded use
operation with a new `use_id`, not an in-place deadline extension.

#### Why Step 3 Is Conditional

What the protocol needs is that the pin insert is **durably established before the
revalidation read**, because that ordering is what makes the `RETIRED` guarantee and
the publication frontier true:

```text
insert established before the GC's global use read
    -> quorum intersection means the GC sees the pin

insert established after the GC's global use read
    -> the revalidation read is ordered after it, hence after the
       ACTIVE -> RETIRING commit, and observes the fence
```

A successful `LOCAL_QUORUM` write already provides that establishment: the
acknowledgement *is* the durability guarantee at the requested consistency level,
and program order puts it before the revalidation read. On the acknowledged path
the read-back proves nothing further and is a round trip removed from the hot path.

The read-back is required exactly when the write result is ambiguous — a timeout or
driver error — because then the writer does not know whether the row was
established, and neither branch above can be claimed. That case is specified under
Ambiguous Pin Creation.

What must **not** happen is skipping both: proceeding to revalidation after an
ambiguous insert leaves a writer authorizing itself on an ordering it cannot
demonstrate.

#### Authority Is Checked At Authorization, Not Only At Publish

Step 5 validates the pin's own `authority_deadline` in addition to the pointer
state. This is not redundant with the publish helper, because a writer can reach
revalidation *after* its own deadline has passed and still find the pointer
`ACTIVE`:

```text
W reads G/E1/ACTIVE, inserts PENDING
GC retires: G/E1/RETIRING
W stalls; its authority_deadline passes
GC drains, finds only expired-authority uses -> RETIRING -> ACTIVE
W finally revalidates: G/E1/ACTIVE, exactly what it first observed
```

The `RETIRING -> ACTIVE` escape makes this reachable by design, and the epoch does
not distinguish it because `active_epoch` deliberately does not change across GC
cycles. Bumping the epoch on reactivation would close this case but break a
different guarantee — an already-`AUTHORIZED` writer is entitled to finish
publishing across `ACTIVE -> RETIRING`, and an epoch bump would reject it at the
publish helper. So the fix belongs where the defect is: authorization must check
the deadline.

Step 6 re-checks it immediately before the physical operation. Without that
re-check a writer whose deadline expires between authorization and PUT can write
bytes to a key whose generation has since been retired and deleted, recreating an
untracked object at `K1` that no reference and no orphan row accounts for. Nothing
is lost, but a leak that the recovery scan cannot attribute is exactly the class of
state this design is trying to eliminate.

After authorization, the writer may finish while the pointer state changes from
`ACTIVE` to `RETIRING`. The publish helper must therefore validate the authorized
pin's generation and epoch, deadline, and existence; it must not reject solely
because the current pointer state is now `RETIRING`. It must reject `RETIRED`,
`DELETING`, `DELETED`, a different active generation, or a missing/expired pin.

No physical operation is permitted before pin authorization.

Reuse or repair of K is permitted only while its generation is `ACTIVE` or
`RETIRING` and the writer holds an authorized pin. A generation that has reached
`RETIRED` or `DELETING` is never repaired or reused; any new materialization gets a
new UUID and a new key.

### First Materialization Or Rematerialization

```text
1. Observe no usable active generation
2. Generate UUID G and exact K
3a. Persist + confirm MATERIALIZING intent            LOCAL_QUORUM
3b. Persist + confirm AUTHORIZED materializer use     LOCAL_QUORUM
3c. Persist + confirm recovery discovery work for G/K LOCAL_QUORUM
4. Re-check materialization deadline/margin; PUT K
5. Verify K exists; set materialization_state=VERIFIED
6. Activate: first life via the existing first-writer LWT,
   rematerialization via the activation CAS below
7. If activation wins, the pointer is now ACTIVE; the only
   generation-row repair is materialization_state=VERIFIED
8. If another generation won, do not publish a reference
9. Publish the generation-bound reference            LOCAL_QUORUM
10. Release the materializer use                     LOCAL_QUORUM
11. Preserve losing K as an orphan for exact cleanup
```

Steps 3a, 3b and 3c are three writes to three different tables, therefore three
different partitions, therefore **not atomic**. The earlier single-line "intent +
use" phrasing hid that, and the ambiguity contract that followed from it named only
the use. All three must be durable before the PUT, and each is confirmed on its own
terms:

```text
3a  MATERIALIZING intent durable:  G + K + storage class + predecessor tuple
                                   + operation identity + deadlines
3b  AUTHORIZED materializer use durable:  G + use_id + authority + deadlines
3c  recovery discovery work durable:  (org, block, generation) reachable from a
                                      bounded projection
only then:  PUT
```

Step 3c is the materialization side of Discoverability Before Irreversibility: after
the PUT a physical object exists, and if nothing already schedules its inspection,
a crash leaves bytes that no projection references and no scan is permitted to find.

The order is not interchangeable. If only the use were confirmed and the intent
write was silently lost, the materializer would PUT and activate with no durable
record of `K`, its storage class, or its lineage: the pointer would name a
generation that has no row, which breaks recovery, quarantine, the `gc_state`
machine, and the predecessor chain for every later generation. Confirming the intent
first also matches the rule that G2's predecessor tuple must be durable before
activation is attempted.

A logged batch across the three partitions would also be correct, but it is more
expensive and harder to reason about than three ordered confirmations, and this design
prefers the sequential form. Neither variant needs Paxos.

The use row from step 3b is held until step 10. It survives activation: the window
between winning at step 7 and publishing at step 9 is precisely when the generation
is `ACTIVE` with zero references, so the GC must be able to see that the generation
is in use. A materializer that loses at step 8 releases its use and lets exact
orphan cleanup remove `K`.

Steps 4 and 5 are ordered before step 6 for a reason that survives every crash
variant: **no path may activate a generation whose object has not been verified.**
The request satisfies this by construction, because the same execution performs the
PUT, the verification, and the CAS. A recovery worker repairing a materializer that
died between step 3 and step 6 does not have that guarantee and must therefore
re-verify `K` — exact key, expected size, expected storage metadata — immediately
before any activation attempt. A missing or uncertain object means no activation:
preserve the intent, the use, and the key, then retry or expire.

This is the property that makes deferring activation expensive. Once the CAS runs
in a different process from the PUT, every activation crosses that trust boundary
and every activation has to re-establish it.

For the first logical life, the existing terminal `INSERT ... IF NOT EXISTS` sets
the initial active pointer in that same operation, so no additional activation
Paxos is introduced. For rematerialization, the logical row already exists. The
materializer first persists G2's exact predecessor tuple from the retired pointer:

```text
G2.predecessor_generation_id       = G1
G2.predecessor_active_epoch        = E1
G2.predecessor_retire_claim_id     = C1
G2.predecessor_retire_claim_epoch  = N1
```

That record must be durable before the activation is attempted. Before issuing the
CAS, the materializer must also read the exact retirement-evidence row for
`(G1, N1)` at `EACH_QUORUM` and confirm that G1's generation row is not
`QUARANTINED`. A missing or mismatched row is a fail-closed case: do not activate
G2, retain its use and key, and quarantine the unprovable generation state with the
conditional transition below.

The materializer must additionally hold more than the configured activation and
clock-skew margin before all three of its own deadlines — its use's
`authority_deadline`, its `materialization_deadline`, and the use's retention
boundary — otherwise it abandons activation and lets exact-key recovery handle the
intent. Activating with expired authority would produce a generation that is
`ACTIVE` while nobody holds the authority to publish a reference to it.

The activation is an update conditional on the complete old-generation identity:

```text
UPDATE blocks
SET active_generation_id = G2,
    active_storage_key = K2,
    active_storage_class = C2,
    active_state = ACTIVE,
    active_epoch = E2,
    retire_claim_id = null,
    retire_claim_deadline = null,
    retire_claim_epoch = N1
WHERE org_id = O
AND block_id = L
IF active_generation_id = G1
AND active_state = RETIRED
AND active_epoch = E1
AND retire_claim_id = C1
AND retire_claim_epoch = N1
```

The `SET` clause is part of the contract, not illustrative shorthand. G2's activation
ends G1's retirement claim, so the same statement clears `retire_claim_id` and
`retire_claim_deadline` while **retaining** `retire_claim_epoch` as the monotonic
counter. Leaving the id or deadline behind would make the pointer read `ACTIVE` while
still advertising a live claim; clearing the epoch would reset the counter that
Claim-Column Lifecycle depends on.

This CAS runs inline in the request that materialized G2. Each materializing
request performs **one logical activation operation**, and exactly one such
operation across all concurrent rematerializers can succeed.

"One logical operation" is not "one CAS execution", and the acceptance criteria
must not be written as if it were. An ambiguous result is retried against the same
`(G2, K2, E2)` per Ambiguous Activation Outcome, so a single logical activation may
issue several physical CAS executions. What is bounded is what matters:

```text
bounded:     generations created per materializing request        = 1
bounded:     successful activations per rematerialization         = 1
NOT bounded: CAS executions, when results are ambiguous
```

A retry that reuses the same generation, predecessor tuple, and epoch is idempotent
— the complete condition can only be satisfied once — so retrying cannot produce a
second generation. Concurrent rematerializers of the same retired block each run one
logical operation, so the cost is bounded by concurrent materializers of that one
block, not by request volume. It is not a new LWT per reference, reuse, or dedup hit.

Inline activation is preferred over deferring to a background worker. Deferring
would leave a request that has already completed a correct PUT unable to finish: it
would return a retryable error and wait for a periodic worker, and a client retry in
the meantime would materialize further losing generations and objects unless a whole
operation-identity layer is added to prevent it. Deferring does not remove the WAN
dependency either — the same `SERIAL + EACH_QUORUM` round still runs, just later and
by someone else — so it reduces first-response latency while increasing end-to-end
latency and total work. See Background Activation, Evaluated And Rejected.

#### Background Activation, Evaluated And Rejected

A revision of this ADR moved the activation CAS out of the request handler into a
background materialization worker, with a durable operation projection so a retry
could reuse the same generation instead of allocating another. It is recorded here
because the reasoning is worth keeping, not because the design is available.

It was rejected on two grounds.

**It does not do what it was adopted for.** The stated motivation was WAN latency.
Moving a CAS to a worker does not remove a Paxos round; it relocates it. The same
`SERIAL + EACH_QUORUM` operation still runs, so first-response latency drops while
end-to-end latency and total work rise:

```text
inline      T = T_local + T_S3 + T_paxos + T_publish

background  T_first_response = T_local + T_S3 + T_handoff
            T_complete       = T_local + T_S3 + T_handoff
                               + T_queue + T_preflight + T_paxos
                               + T_publish/retry
```

And it optimizes a rare path. Rematerialization happens only when a SHA whose
generation was fully collected comes back; the Paxos that scales with block volume
is the existing first-writer LWT, which the move leaves untouched.

**The trust boundary it creates costs more than the CAS.** Once the CAS runs in a
different process from the PUT, six problems appear that inline does not have:

| Problem | Why inline does not have it |
|---|---|
| The worker can activate a generation whose object was never PUT | The same execution does the PUT, the verification, and the CAS |
| Concurrent retries of one operation allocate different generations; a fingerprinted projection detects the conflict but nothing makes them converge | No projection, no handoff |
| Operation IDs must be both stable per attempt and never reused across attempts; `sync:<repo>:<block>` is stable but repeats across physical lifecycles | No operation identity is needed |
| The materializer use is written `LOCAL_QUORUM` in one DC and read by a worker in another — X2's own failure shape, reappearing inside the new layer | Same process, same DC |
| A client that never retries leaves `ACTIVE` with zero references, making the crash-recovery scan a scheduler for a normal outcome | The request publishes its own reference |
| The materializer use stops being request-scoped and becomes an operation-scoped capability, so its deadlines must be redefined | One request, one lifetime |

Every one of those is solvable. Together they are a distributed protocol added to
avoid one cold-path Paxos round.

The decision is therefore conditional, not permanent: if measured
`SERIAL + EACH_QUORUM` activation latency turns out to violate a real request SLA,
revisit it — with the six problems above as the known cost, and after checking
whether the engine's Paxos variant and a probe fast path on the existing
first-writer LWT close the gap more cheaply.

Losing the CAS is an ordinary outcome and needs no coordination:

```text
writer A: G2/key-A ─┐
                    ├→ activation CAS
writer B: G3/key-B ─┘

B wins

A: key-A becomes an exact orphan
   release materializer use
   re-probe -> observe G3
   pin and reuse G3
```

A background allocator may still exist to repair generations whose materializer
crashed between a verified PUT and activation. That is recovery work, not the normal
path, and it must re-verify the exact object before activating because it did not
perform the PUT itself.

### Ambiguous Activation Outcome

A timeout or driver-level error on the activation CAS does not mean the CAS was
not applied. The request must not assume it won or lost. It retains `G2`, `K2`, and
its materializer use, then re-issues the same activation CAS in the `SERIAL` Paxos
domain. If the retry remains ambiguous, it inspects the authoritative `blocks` row
at `EACH_QUORUM`; that ordinary read is not itself a settlement of a pending
proposal:

| Observed authoritative state | Action |
|---|---|
| `active_generation_id = G2` and `active_epoch = E2`, G2's predecessor tuple matches `(G1, E1, C1, N1)` with retirement evidence for `(G1, N1)`, G2 is usable (`gc_state = null AND materialization_state = VERIFIED`), and the materializer use still has valid authority and retention margin | The CAS applied; publish the reference and release the use |
| The same valid G2 lineage is selected, but the materializer authority deadline, materialization deadline, retention margin, or request context is no longer valid | Do not publish. Retain G2 and its exact use/key for recovery; release only through the expired-use recovery rule and re-enqueue the active zero-reference generation. An applied CAS does not restore expired authority |
| `active_generation_id = G2` and `active_epoch = E2`, but G2's predecessor tuple/evidence is absent or mismatched, or G2's `gc_state` is `QUARANTINED`, `DELETING`, or `DELETED` | Preserve G1/K1 and G2/K2; quarantine the unprovable generation state, never publish or delete on inference |
| The same G2 lineage is valid but G2's `materialization_state` still reads `MATERIALIZING` | Not a contradiction. Confirm the materializer's own `VERIFIED` write, or re-verify `K2`, and repair the marker; do not quarantine on marker lag alone. See `VERIFIED` Lag Is Not A Contradiction |
| A different generation is active | Lost the race; make `K2` an exact orphan and release the use |
| Still `G1` / `RETIRED` / `E1` with matching claim tuple | Not applied; the same logical operation may be retried idempotently |
| Read uncertain or a DC is unavailable | Retain `G2`, `K2`, and the use; retry later. Never orphan and never delete on an uncertain read |

#### Recovering A Crashed Activation

The table above is the *request's* reconciliation, where the process that issued the
CAS is still alive. A recovery worker arriving later has a strictly harder problem,
and the naive branch is unsafe:

```text
G2 VERIFIED, K2 exists, materializer use AUTHORIZED
activation CAS proposal accepted; coordinator dies before the commit is learned
authority later expires

recovery: ordinary EACH_QUORUM read of blocks -> "G1 / RETIRED"
recovery: "not selected, authority expired" -> orphan and delete K2
later:    a subsequent LWT replays the pending proposal -> pointer becomes G2
```

The pointer now names a generation whose object recovery already deleted. An
ordinary global read does not prove that a proposal was never accepted — the same
property this document already establishes for `ACTIVE -> RETIRING`.

Settlement and activation are also two different acts, and recovery must not
conflate them. Re-issuing the activation CAS when the materializer's authority has
already expired would *create* an `ACTIVE` G2 that nobody is entitled to publish a
reference to, contradicting the authority contract. Recovery settles; it does not
activate on an expired authority.

```text
expired or crashed VERIFIED materialization

1. settle the blocks partition in the SERIAL Paxos domain
2. settled pointer = G2       activation did happen.
                              Never orphan K2. Do not publish with expired
                              authority. Hand off to the zero-reference
                              ACTIVE recovery branch
3. settled pointer = G1/RETIRED with no proposal outstanding
                              activation did not happen. If authority has
                              expired, do NOT activate; K2 becomes an exact
                              orphan
4. settled pointer = another G   G2 lost; exact orphan
5. anything uncertain         retain G2 and K2; retry later
```

##### First Materialization Has The Same Hazard

The sequence above is written in rematerialization vocabulary, but **the hazard is
not specific to rematerialization**. The first-writer `INSERT ... IF NOT EXISTS` is
also a Paxos operation on the same `blocks` partition, in the same `SERIAL` domain,
and it fails the same way:

```text
materializer PUTs K1
first-writer LWT proposal accepted; coordinator dies before the commit is learned
recovery: ordinary read of blocks -> no row (or a released stub)
recovery: "never activated, authority expired" -> orphan and DELETE K1
later:    any LWT on that partition replays the pending proposal
          -> blocks now points at K1, which no longer exists
```

That is live-data loss on the *first* upload of new content, reachable from day one
of the writer rollout and long before any rematerialization exists. An implementation
that applies the settlement rule only where this document says "G2" leaves it open.

The rule is therefore stated over materializations, not over successors:

> Any materializer whose activation LWT may have been issued — first life or
> rematerialization — must settle the `blocks` partition serially before any orphan
> or delete decision about its own key.

For a first life the settled outcomes are the same shape:

| Settled pointer | Action |
|---|---|
| selects this generation | The LWT applied. Never orphan `K`. Do not publish with expired authority; hand off to the zero-reference `ACTIVE` branch |
| selects a different generation | This materializer lost the first-writer race; `K` becomes an exact orphan |
| absent, or a released stub, with no proposal outstanding | The LWT did not apply. If authority has expired, do **not** activate; `K` becomes an exact orphan |
| uncertain | Retain the generation and `K`; retry later |

The stub row deserves its own mention: "no `active_generation_id`" is not "no
activation was ever proposed". Only the settled read distinguishes them, which is
why Released Stubs Break "Row Exists Implies Active Pointer" and this section have to
be read together.

##### How To Issue The Settlement Read

"Settle serially" is a driver-level question, and getting it wrong produces a
statement that compiles, runs, and proves nothing. The repository uses
`github.com/apache/cassandra-gocql-driver/v2 v2.0.0`, where this is directly
expressible:

```go
// Serial is a first-class Consistency value (0x08) in this driver.
// `SerialConsistency` is a deprecated alias of `Consistency`.
session.Query(`SELECT ... FROM blocks WHERE org_id = ? AND block_id = ?`, o, b).
    Consistency(gocql.Serial)
```

The distinction matters because it is the opposite of the older `gocql/gocql` v1
driver, where `SerialConsistency` was a *separate type* and could only be attached
to a conditional statement's serial phase, so a serial `SELECT` was not expressible
through the normal API. Under v2, `Query.Consistency` accepts `Serial` without
validation and the level is sent as the statement's read consistency. Do **not**
write `.SerialConsistency(gocql.Serial)` on a plain `SELECT`: that setter is for the
conditional phase of an LWT and is ignored by a non-conditional statement — it is
exactly the silent no-op this section exists to prevent.

**The serial `SELECT` is the normative mechanism.** There is exactly one settlement
form in this design, and the startup gate is unconditional: `paxos_variant` must
retain linearizable reads. PR-0 records the exact accepted set for the deployed
engine, and PR-2's startup gate rejects any `*_without_linearizable_reads*` variant.
This is a third axis alongside `NetworkTopologyStrategy` and `SERIAL`, and it fails
the same way: silently.

Offering two mechanisms would not have been free. A conditional gate is a gate that
is wrong in one of its branches whenever the branches drift, and the alternative
below also needs a schema column that Proposed Schema does not define. One
mechanism, one gate, one column set.

The alternative is recorded only so it is not rediscovered as new: a **harmless
settlement LWT** on the same `blocks` partition — for example an
`UPDATE ... SET <token column> ... IF EXISTS` at `SERIAL` + `EACH_QUORUM` — would
also settle a pending proposal, because any LWT runs a prepare phase that finishes
in-progress rounds before proceeding, and a statement touching only a dedicated
column cannot install a pointer. It needs no linearizable-read guarantee. It is
**not** part of this contract: it costs a full LWT instead of a serial read, and
adopting it would require PR-1 to add the token column and PR-2 to make the
`paxos_variant` gate conditional. Revisit it only if a future driver or engine
removes the serial read.

**This gate binds from the first generation-aware write, not from `gc.enabled`**,
for the same reason the `SERIAL` gate does — and here the reason is sharper. A
materializer can crash between its PUT and its activation CAS on the very first day
generation-aware writers are deployed, long before destructive GC is switched on.
The crashed-activation recovery that follows is exactly the path that needs the
serial round. Tying this assertion to `gc.enabled` would leave it unenforced during
precisely the window in which the first crashed activations occur.

This generalizes to every destructive or lifecycle LWT in this design: an ambiguous
result is reconciled against authoritative state, never guessed. Pointer LWTs have
the stronger rule in Ambiguous Active-Pointer Transition: an ordinary global read
does not settle an accepted-but-uncommitted Paxos proposal. Treating a timeout as
"not applied" is the failure mode that produces both double activation and
premature orphan cleanup.

### Ambiguous Pin Creation

Pin creation and pin authorization are two different writes with two different
ambiguity contracts. Conflating them is itself a bug: requiring `AUTHORIZED` to
confirm an ambiguous `PENDING` insert would abort a pin that landed correctly.

This section applies **only** to ambiguous results. An acknowledged write needs no
confirmation; see Why Step 3 Is Conditional.

**Ambiguous `PENDING` insert.** The writer confirms that the row exists with the
expected identity:

```text
use_id + generation_id + epoch + state in {PENDING, AUTHORIZED}
```

It may retry the idempotent insert with the same `use_id`. Success here grants no
authority; the writer still has to revalidate and authorize. It must not perform
any S3 operation yet.

**Ambiguous authorization.** The writer confirms the full authority tuple:

```text
use_id + generation_id + epoch + state=AUTHORIZED + authority_deadline
```

A row that exists in `PENDING` is not authority. Treating "the `use_id` row is
present" as success would let an ambiguous authorization be read as a granted one,
which is exactly the failure the `PENDING`/`AUTHORIZED` split exists to prevent.

**Ambiguous materialization intent, use, or discovery.** These are three separate
writes to three separate partitions, in steps 3a, 3b and 3c of First Materialization
Or Rematerialization — not the step 3 of the writer sequence — and each has its own
confirmation:

- an ambiguous **intent** write is confirmed by reading back the exact
  `(org, block, generation)` row and matching `storage_key`, storage class,
  predecessor tuple, and operation identity. Without it there is no durable record
  of `K` and no lineage, so the materializer must not proceed to the use, the
  discovery row, the PUT, or the activation;
- an ambiguous **use** write is confirmed by reading back
  `use_id + generation_id + kind=MATERIALIZER + state=AUTHORIZED + deadlines`;
- an ambiguous **discovery** write is confirmed by reading back the exact
  `gc_generation_intents_by_day` row for `(due_day, bucket, due_at, org, block,
  generation)`. **If it cannot be confirmed, there is no PUT.** Step 3c is
  safety-critical in the same way as the other two: after the PUT an object exists
  physically, and if no work row is durable, nothing schedules its inspection and no
  scan is permitted to find it.

If either confirmation fails the materializer has no authority. It must not PUT and
must not activate on the assumption that either row landed. A materializer that PUTs
and activates without a confirmed use recreates the exact hole this design closes: a
generation that is `ACTIVE` with no reference and no use is indistinguishable from
garbage, and the GC will delete `K`. A materializer that activates without a
confirmed intent produces the mirror-image hole: a pointer naming a generation that
has no row at all.

In every case: abort if confirmation still fails, and never perform an S3 operation
based on an assumption that a use or intent row exists.

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

Authorization and the final pre-operation check must also require a remaining
authority budget greater than the configured clock-skew, Cassandra latency, retry,
cancellation, and physical-operation margins. A deadline that is technically in
the future but cannot cover that bound is treated as expired for starting new
physical work. This prevents a writer from passing a wall-clock comparison and
then crossing the authority boundary before its PUT or repair begins.

The central publish helper must reject a writer when:

- the pin is absent or belongs to another operation;
- the generation ID does not match;
- the authority deadline has passed;
- the pointer says the pinned generation is `RETIRED`;
- the pinned generation's own row fails the positive usability predicate
  `gc_state = null AND materialization_state = VERIFIED`, read from
  `block_generations` and not from `active_state` (see Which States Live Where).
  Stating it positively is deliberate: an enumeration of `DELETING`, `DELETED`, and
  `QUARANTINED` silently accepts a generation that is merely not yet `VERIFIED`, and
  a funnel that reaches publication only through this helper would then publish a
  reference on a regional `MATERIALIZING` lag. Quarantine remains fail-closed even
  if a stale pointer still names that generation; it is simply not the whole test;
- the active generation/epoch no longer matches the authorized pin;
- the pin is not `AUTHORIZED`;
- the request context has expired;
- the reference result cannot be safely classified.

The helper may remove a pin only after successful reference confirmation. An
ambiguous reference result leaves the pin for retry/recovery.

### Writer Behavior For Non-Active States

Every writer path needs a bounded retry contract; it must never wait indefinitely
for a generation transition:

| Observed state | Where observed | Writer behavior |
|---|---|---|
| `RETIRING` | pointer | bounded poll/backoff; no G2 allocation; retryable failure with `Retry-After` when the budget ends. The wait is bounded by live-authority drain, not by any retention TTL — see Escaping `RETIRING` On Abandoned Uses |
| `RETIRED` | pointer | allocate a new UUID generation, materialize it, and complete the activation CAS inline; on CAS loss, orphan the losing key and reuse the winner |
| `MATERIALIZING` owned by another operation | generation row, reached through a pinned generation ID | bounded poll/backoff, then retryable failure. Not reachable from the pointer, so it is not a state a first materializer can poll to avoid racing |
| `QUARANTINED` | generation row, including the row selected by a pointer under investigation | fail closed; no new use, physical operation, candidate, or delete; require operator reconciliation |
| `DELETING` | generation row | fail/retry; never repair or reuse that generation |
| `DELETED` | generation row | re-probe the logical block and allocate a new generation if needed |
| Cassandra state uncertainty | either | fail closed with a bounded retry budget |
| A DC is unavailable and the block needs rematerialization | either | retryable failure; the activation CAS cannot reach global agreement, so the upload cannot complete. Deduplication against an already-`ACTIVE` generation is unaffected and stays regional |

The exact HTTP/status mapping is an implementation decision for PR-4, but every
funnel must expose the same retryable error contract.

Only `RETIRING` requires a writer to wait, and two separate rules keep that wait
short. Neither is optional, because each closes a stall the other does not:

- A provisional reference must not create a long writer stall: the GC reactivates
  the generation when it sees any generation-bound reference, so a long
  provisional TTL costs retention rather than availability.
- An abandoned pin must not create a long writer stall either: the GC reactivates
  when one or more uses remain and every such use has expired its authority, so the
  wait is bounded by the authority deadline rather than by `retention_expires_at`.

`RETIRED` does not stall on a state transition: the writer proceeds immediately to
materialize and activate a new generation.

"Does not stall" is a statement about waiting, not about availability. A
rematerializing writer still depends on the activation CAS reaching global
agreement, so during a DC outage it fails fast with a retryable error instead of
waiting — the row above says exactly this. The two statements describe different
things: `RETIRING` makes a writer wait for someone else's work to finish;
`RETIRED` never does, though its own work can fail.

## First Materialization And Active-Pointer Authority

The design must not have two independent sources that can disagree about which
generation won. Cassandra cannot perform a conditional update atomically across
`blocks` and `block_generations`.

The single linearization point is the logical `blocks` row. It contains:

```text
active_generation_id
active_storage_key
active_storage_class
active_state
active_epoch
retire_claim_id
retire_claim_epoch
retire_claim_deadline
```

All conditional active-pointer transitions occur through LWTs on that same row:

```text
ACTIVE -> RETIRING        GC worker
RETIRING -> ACTIVE        GC worker
RETIRING -> RETIRED       GC worker
RETIRED G1 -> ACTIVE G2   materializing request (inline) or recovery worker
```

Because those columns are Paxos-managed, the following must hold and must be
enforced by review rather than assumed:

> No unconditional write may touch `active_generation_id`, `active_storage_key`,
> `active_storage_class`, `active_state`, `active_epoch`, or any `retire_claim_*`
> column.

The `blocks` partition already receives unconditional traffic — `internal/api/sync.go:436`
does a plain `UPDATE blocks SET last_accessed = ?` — and mixing conditional and
unconditional writes on one partition is a documented Cassandra sharp edge. It is
benign today only because the column sets are disjoint and Cassandra timestamps
cells individually. That disjointness is the invariant; nothing currently states it,
so nothing currently protects it.

Initial pointer creation is folded into the existing terminal first-writer
metadata LWT in the same operation that registers the new block, so a first
physical lifecycle adds no activation Paxos of its own.

### Released Stubs Break "Row Exists Implies Active Pointer"

Folding pointer creation into `INSERT ... IF NOT EXISTS` assumes that a `blocks`
row existing means a generation was activated. **That assumption is false in the
current schema and the implementation must handle the exception explicitly.**

Cassandra applies `UPDATE ... IF <col> != <value>` against a missing row, because
the condition holds against `null`. `internal/gc/store_cassandra.go:2077-2087`
(`ClaimBlockDelete`, `IF gc_state != 'deleting'`) can therefore create a partial
`blocks` row that has `gc_state` set and `created_at = null`. The codebase already
names this state and repairs it: `internal/db/block_references.go:203-226` claims
and deletes such rows under `BlockGCStateRepairingStub` with
`IF created_at = null AND storage_class = null`, and
`internal/api/v2/fs_helpers.go:1006-1010` surfaces `db.ErrBlockStubRepairContended`
back to the upload retry loop.

Against a stub row the first-writer `INSERT ... IF NOT EXISTS` does **not** apply.
A materializer that reads that outcome as "another writer won the activation race"
would orphan its key, re-probe, find no active generation, materialize again, and
loop — burning a generation and an object per iteration while never converging.

The generation-aware implementation must therefore:

- treat a non-applied first-writer LWT as *ambiguous* until the returned row is
  inspected, exactly as the Ambiguous Activation Outcome rule requires elsewhere. A
  row carrying an `active_generation_id` means a real loss; a stub row does not;
- reuse the existing stub-repair claim to convert a stub into a full activation for
  the materializer's own generation, rather than treating the stub as a winner.
  **This path establishes a first active pointer and must initialize the pointer
  exactly as the first-writer LWT does**, in the same conditional operation:
  `active_epoch = 1`, `retire_claim_epoch = 0`, and null claim ID and deadline.
  Otherwise a repaired stub yields a live pointer whose `retire_claim_epoch` is
  null, and the first `ACTIVE -> RETIRING` — which conditions on
  `retire_claim_epoch = Nprev` — would need a null special case, reintroducing
  exactly the exception correction 90 removed;
- keep the stub-repair contention signal retryable, since a contended repair is a
  race with another materializer, not a fenced generation;
- assert, as a schema invariant, that no row may carry `active_state` without
  `active_generation_id` and `active_storage_key`.

PR-1 must decide whether generation-aware claims can avoid creating stubs at all —
a conditional update whose `IF` clause cannot be satisfied by a null row is
preferable to repairing the damage afterwards — and PR-3 must not assume the
question was settled.

`block_generations` stores immutable physical identity, claim/recovery data, and
monotonic physical lifecycle markers. It is not a second authority for deciding
which generation is active. `materialization_state=MATERIALIZING` is never
sufficient to authorize deletion. Recovery always consults the `blocks` pointer
first; it may repair the monotonic verification marker, but it never reconstructs
an active/retiring/retired pointer state from the generation row.

### Which States Live Where

The document uses one vocabulary for two different state machines, and an
implementation that conflates them will look for values where they cannot appear.
The split is:

| State | Recorded on | Observable by a writer reading the pointer |
|---|---|---|
| `ACTIVE` | `blocks.active_state` | Yes |
| `RETIRING` | `blocks.active_state` | Yes |
| `RETIRED` | `blocks.active_state` | Yes |
| `MATERIALIZING`, `VERIFIED` | `block_generations.materialization_state` | **No** |
| `QUARANTINED` | `block_generations.gc_state` | No |
| `DELETING`, `DELETED` | `block_generations.gc_state` | **No** |

The pointer never holds `MATERIALIZING`. A first life creates the row already
`ACTIVE` through the first-writer LWT; a rematerialization moves it directly from
`RETIRED` (G1) to `ACTIVE` (G2). Two concurrent first materializers of the same new
content therefore both PUT and both attempt the LWT, one loses and orphans its key.
That is the current behaviour and it is safe; there is no pointer state a writer
could poll to avoid it.

Likewise a writer never observes `DELETING` or `DELETED` on the pointer. Those are
terminal generation states, reached after the pointer has already stopped selecting
G1 as a usable generation. The logical `blocks` row is retained; it may continue to
show `RETIRED` with G1 as the historical predecessor while G1's `gc_state` is
`DELETED`. A writer arriving then reads the generation row, allocates a new UUID
and key, and uses the retained pointer/evidence lineage for the activation CAS. This
preserves `active_epoch` monotonicity and avoids deleting the logical pointer row.

Where the tables below list writer behaviour or publish-helper rejections for
`MATERIALIZING`, `DELETING`, or `DELETED`, the check is against the **generation
row** reached through the writer's own pinned generation ID, not against
`active_state`. For the common case the pointer check "the active generation no
longer matches my authorized pin" already covers it.

### Conditional Generation-Lifecycle Transitions

`gc_state` is the generation row's terminal physical lifecycle. Every transition
into it is a conditional LWT on the generation row, and **none of them may condition
on a retirement claim**, because the generation row does not carry one.

That is a deliberate consequence of removing the mutable mirror, and it must be
stated rather than left to be rediscovered. Nothing writes a live `retire_claim_*`
value onto `block_generations`: the claim tuple is installed by the
`ACTIVE -> RETIRING` CAS on the `blocks` row, and this document forbids any
unconditional write to a `retire_claim_*` column. A generation-row condition on
those columns could therefore never be satisfied. The claim proof belongs where the
worker actually obtains it — the authoritative pointer and evidence read that
precedes the transition — and the claim identity is *recorded* by the transition,
never *matched* by it.

#### No Conditional May Be Satisfied By A Missing Row

Before any statement below, one invariant governs all of them:

> No generation-row conditional may consist only of null comparisons. Every form
> must additionally match at least one **non-null immutable identity column**.

This is correction 41 applied to the new table. Cassandra evaluates
`IF col = null` against a *missing* partition and finds it true, so a bare
`IF gc_state = null` **applies against a `block_generations` row that does not
exist** and creates a partial row carrying `gc_state` and nothing else — no
`storage_key`, no `storage_class`, no `materialization_state`. That row is exactly
the shape this design calls unrecoverable: a `DELETING` generation whose exact key
is unknown. It is the same defect `internal/gc/store_cassandra.go:2077-2087` already
has on `blocks`, and this ADR must not reproduce it on the table built to replace
that behaviour.

The identity column is chosen per form, and every form has one available because
the materialization intent writes the whole row before anything else can reference
it.

#### Authorizing `DELETING`

```text
UPDATE block_generations
SET gc_state = DELETING,
    delete_claim_id = C1,
    delete_claim_epoch = N1,
    delete_authorized_at = now
WHERE org_id = O
  AND block_id = L
  AND generation_id = G1
IF gc_state = null
AND materialization_state = VERIFIED
AND storage_key = K1
```

`generation_id` is in the primary key and is never reused, so the row identifies one
physical lifecycle exactly; there is no newer same-identity lifecycle for a stale
worker to damage. `IF gc_state = null` supplies monotonicity: the transition applies
at most once and can never regress `DELETED` or `QUARANTINED`.

`materialization_state = VERIFIED` and `storage_key = K1` are the non-null identity
guard. They cannot hold against a missing row, so the statement can never fabricate
a partial generation, and they make the worker prove it is deleting the object it
actually resolved rather than whatever the row now says. A generation that never
reached `VERIFIED` has no confirmed object to delete and belongs to the
materialization-expiry path, not to this one.

The authorization comes from the proof required *before* the statement is issued: an
evidence row for G1 whose epoch **and** claim ID match, plus an authoritative pointer
that either reads `G1 / RETIRED` or selects a successor reachable from G1 by an
unbroken lineage chain.

Which claim the evidence must match depends on where the pointer is, and conflating
the two cases is a live trap:

```text
pointer = G1 / RETIRED     evidence(G1, N1) must match the LIVE pointer claim,
                           because the live claim is C1 / N1

pointer = G2 or later      the live pointer claim belongs to a later cycle, or is
                           null. evidence(G1, N1) must match the claim recorded in
                           the lineage link -- G2's predecessor tuple (G1, E1, C1,
                           N1) -- NOT the pointer's current claim
```

Reading "the live claim" as the pointer's current claim in the successor case would
fail every legitimate delete of a superseded generation. The claim to match is always
the one that authorized *that* retirement, and the lineage link is what records it.

That proof is irrevocable once obtained, which is precisely why it does not need to
be re-asserted in the `IF` clause:

```text
the pointer has no RETIRED -> ACTIVE transition
    -> once G1 reads RETIRED, no writer can ever acquire authority on G1 again
    -> the pointer can only move forward to a successor, and the first such
       successor's activation CAS required the exact (G1, E1, C1, N1) tuple that
       is this worker's own proof
    -> any later generation is reachable from that one by the same rule, so the
       proof survives arbitrarily many rematerializations
```

The successor is written as "the next generation" rather than "G2" on purpose. By
the time a delayed worker runs its statement the pointer may already read G3 or
later; that changes which chain it must walk, not whether its proof still holds.
See The Lineage Chain Is Transitive.

So a worker that stalls between the proof and the statement cannot wake into a world
where deleting `K1` has become unsafe. Contrast `ACTIVE -> RETIRING`,
`RETIRING -> ACTIVE`, and `RETIRING -> RETIRED`: those are contended and reversible,
so they do carry the full claim tuple in their `IF` clauses — on the `blocks` row,
where the tuple actually lives.

The recorded `delete_claim_id`/`delete_claim_epoch` satisfy the separate requirement
that the generation record be a recovery source able to say which claim authorized
the physical delete. They are written by this conditional statement, so no
unconditional write is introduced, and they are never read back as a precondition.

`DELETING -> DELETED` is the same shape with `IF gc_state = DELETING`, which is
itself a non-null comparison and therefore already satisfies the missing-row rule.

#### Generation Validity Is A Positive Predicate

Several paths need to ask "may I use, publish to, or trust this generation?". That
question must always be answered with a **positive** predicate:

```text
usable  ==  gc_state = null AND materialization_state = VERIFIED
```

It must never be expressed as "the generation is not `QUARANTINED`". That phrasing
is true for a generation whose `gc_state` is `DELETING` or `DELETED`, and it is true
for a partial row that never carried a `materialization_state` at all. A
materializer that reconciles an activation with "G2 is not `QUARANTINED`" can
therefore publish a reference to a generation whose object is being deleted or is
already gone.

The rule applies wherever the document names quarantine as a validity condition,
including the post-CAS reconciliation in Conditional Quarantine and the
activation-outcome table. `QUARANTINED` remains fail-closed; it is simply never the
whole test.

#### `VERIFIED` Lag Is Not A Contradiction

Failing the positive predicate and *proving a contradiction* are different
conclusions, and only the second may quarantine. `materialization_state=VERIFIED` is
written at `LOCAL_QUORUM` while the activation CAS commits at `EACH_QUORUM`, so this
is an ordinary, expected multi-DC observation:

```text
DC-A: materializer writes VERIFIED (LOCAL_QUORUM), then activation CAS (EACH_QUORUM)
DC-B: pointer already reads G2 / ACTIVE
      local generation read still reads MATERIALIZING
```

Nothing is wrong there; regional propagation is simply pending. Quarantining on it
would make a routine cross-DC lag produce a permanent, operator-only terminal state
on a healthy generation — the exact over-reaction the fail-closed rule is meant to
avoid, and one that a single slow DC could inflict at scale.

The split is therefore by role:

| Role | Observation | Action |
|---|---|---|
| Writer or dedup | generation not `VERIFIED` | Fail closed, retry. Never quarantine |
| Materializer, own generation | its `VERIFIED` write was ambiguous | Confirm the write before attempting activation |
| Recovery / quarantine decision | pointer selects G, generation appears `MATERIALIZING` | Reconcile authoritatively, re-verify the exact object `K`, repair `materialization_state=VERIFIED` if the object proves out. Quarantine **only** on an actual contradiction, such as a missing or mismatched object |

So the verification case "a `pointer=G2` whose marker lags is repaired, not
quarantined" and the rule "a generation that is not `VERIFIED` is not usable" are
both true: the first is about the recovery worker's conclusion, the second about a
writer's permission.

#### Quarantine

Quarantine is a generation-row transition, not an unconditional diagnostic write
and not a cross-table CAS:

```text
UPDATE block_generations
SET gc_state = QUARANTINED,
    quarantined_at = now,
    quarantine_reason = R
WHERE org_id = O
  AND block_id = L
  AND generation_id = G
IF gc_state = null
AND storage_key = K
```

The statement uses `SERIAL` for the conditional phase and `EACH_QUORUM` for the
regular commit. The pointer state is supplied by the worker's prior authoritative
read, but is not copied into the generation row. `storage_key` is the non-null
identity guard required above; quarantine cannot use `materialization_state` for
that role because a `MATERIALIZING` generation is one of the things worth
quarantining. The implementation must use fixed identity-specific statements and
never accept an arbitrary state value: the `MATERIALIZING` form additionally matches
its operation ID and `materialization_state`. There is no automated form for
`DELETING` or `DELETED`; `IF gc_state = null` is what prevents overwriting either
terminal state.

Quarantine needs no claim condition for a second, independent reason: it is
fail-closed in the safe direction. A stale worker that quarantines a healthy
generation costs retention and an operator ticket, never data. Two concurrent
quarantiners converge, because the second simply does not apply.

An ambiguous quarantine result is reconciled by re-issuing the same conditional LWT
in the `SERIAL` Paxos domain, followed by an `EACH_QUORUM` inspection if needed: an
observed `gc_state=QUARANTINED` is success, the same expected unquarantined state is
retryable, `DELETING`/`DELETED` is preserved, and an uncertain inspection retains the
generation and physical key. A quarantined generation is excluded from discovery,
queues, re-enqueue, and every delete path. It can leave quarantine only through an
explicit operator workflow with its own audited conditional transition.

The generation row and the active-pointer row cannot be guarded by one Cassandra
condition, so quarantine is a fail-closed marker, not an atomic activation lock. The
materializer performs a final authoritative reconciliation after its pointer CAS:

- If G2 is selected, its predecessor tuple and exact evidence match, and G2's own
  row satisfies the positive usability predicate
  (`gc_state = null AND materialization_state = VERIFIED`), the activation is valid.
  A stale quarantine marker on proven G1 is retained for operator review and never
  authorizes deleting K1.
- If G2's `gc_state` is `QUARANTINED`, `DELETING`, or `DELETED`, or its
  lineage/evidence is missing or mismatched, the worker does not publish the
  reference and quarantines/retains the affected generation rows. A row that is
  merely not yet `VERIFIED` is **not** in this list: that is marker lag, resolved by
  confirming the write or re-verifying `K2` and repairing the marker. See `VERIFIED`
  Lag Is Not A Contradiction.
- If quarantine wins before activation, the pre-CAS read aborts. If the two rows
  race after that read, the same post-CAS reconciliation decides; no cross-table
  inference authorizes a publish or delete.

### Epoch Allocation

`active_epoch` and `retire_claim_epoch` appear in CAS conditions, in use rows, and
in the authority tuple. Their allocation rules are:

- `active_epoch` is a monotonically increasing integer scoped to one logical block.
  It is set once per activation and never decreases.
- The first-writer LWT initializes `active_epoch = 1` together with the initial
  pointer.
- A rematerialization proposes `E2 = E1 + 1` and installs it in the same activation
  CAS that installs G2. Two concurrent rematerializers may propose the same value;
  this is harmless because their use rows live in different generation partitions
  and only one CAS can win.
- `ACTIVE -> RETIRING`, `RETIRING -> ACTIVE`, and `RETIRING -> RETIRED` do **not**
  change `active_epoch`. They change `active_state`. The epoch identifies a
  physical activation, not a GC cycle.
- `retire_claim_epoch` is separate and increments on every claim acquisition or
  takeover for the block, including a takeover after `retire_claim_deadline`. It is
  what makes a stale worker's conditional transition fail, and what dates the
  retirement evidence described under Retirement Handoff.

A `WRITER` use records the `active_epoch` it observed; a `MATERIALIZER` use records
the `active_epoch` it proposes. See Uses And References for why the two must not be
validated the same way.

The successful `RETIRING -> RETIRED` pointer transition retains the live
`retire_claim_id` and `retire_claim_epoch` until G2 activation replaces the pointer.
Clearing or changing those columns before a later activation would destroy
the exact predecessor condition and is forbidden.

#### Claim-Column Lifecycle

Two transitions previously left the claim columns unspecified, which is enough for
two implementations to disagree about whether a claim is live. The complete rule,
which must be frozen in PR-1:

```text
ACTIVE (steady state)
    retire_claim_id       = null
    retire_claim_deadline = null
    retire_claim_epoch    = last allocated value        <-- retained, never cleared

ACTIVE -> RETIRING
    retire_claim_epoch    = previous + 1
    retire_claim_id       = C
    retire_claim_deadline = new deadline

RETIRING -> ACTIVE          (reference found, or all uses expired)
    retire_claim_id       = null
    retire_claim_deadline = null
    retire_claim_epoch    = retained

RETIRING -> RETIRED
    retire_claim_id / epoch / deadline all retained
    (this is the predecessor proof the activation CAS matches)

G1 RETIRED -> G2 ACTIVE     (activation CAS)
    G2.predecessor_* records C1 / N1 durably first
    retire_claim_id       = null
    retire_claim_deadline = null
    retire_claim_epoch    = N1 retained as the counter
```

The single property that makes this safe is that `retire_claim_epoch` is a
**monotonic counter that is never cleared**, while `retire_claim_id` and
`retire_claim_deadline` are live only during `RETIRING` and `RETIRED`. Clearing the
epoch on reactivation or on activation would let a stale worker from cycle N match a
future cycle — the hole correction 53 exists to close — and clearing the id or
deadline while `RETIRED` would destroy the predecessor condition.

The activation CAS clears `retire_claim_id`/`retire_claim_deadline` in the same
statement that installs G2, so there is no window in which the pointer reads `ACTIVE`
while still advertising a live claim, and no unconditional write is introduced.

### Retirement Handoff

The `blocks` row is the linearization point only while G is the active generation.
Once G2 overwrites `active_generation_id`, that row no longer records anything
about G1 — including the fact that G1 ever reached `RETIRED`. A delete of `K1` is
authorized by that fact, so the evidence must outlive the pointer.

The required order is therefore:

```text
1. global zero check for G1                       EACH_QUORUM
   (uses first, then refs — see Read Order Is Not Decision Order)
2. append + confirm retirement evidence for
   (G1, claim_epoch)                              SERIAL + EACH_QUORUM
3. publish + confirm gc_generation_handoff_by_day LOCAL_QUORUM
4. CAS blocks RETIRING -> RETIRED                 SERIAL + EACH_QUORUM
   <-- G2 may activate from this point on
   <-- the candidate/queue work may now retire
5. publish + confirm gc_generation_deletes_by_day LOCAL_QUORUM
   <-- the handoff work may now retire
```

Steps 3 and 5 are the work-continuity handoff, not bookkeeping. Without step 3 the
seam after the pointer CAS has no owner: `blocks` reads `G1 / RETIRED` with valid
evidence and `K1` present, while the candidate that discovered G1 is gone and the
delete work does not exist yet. See Discoverability Before Irreversibility.

**Step 4 is the gate.** The G2 activation CAS conditions on the
`blocks` row alone, so a rematerializing request can win it once the pointer reads
`G1 / RETIRED / E1` with the matching claim tuple. Nothing can hold G2 back until a
later write to a different table lands, and an implementer who tries to enforce
that will reach for a cross-table condition Cassandra cannot express — which this
ADR forbids elsewhere as an acceptance criterion.

There is no mutable generation-row mirror finalize. The durable proof of retirement is:

> an evidence row for G1 matching the **retirement-authorizing claim's epoch and
> ID**, **plus** the
> authoritative pointer having left `RETIRING` — either because it reads
> `G1 / RETIRED`, or because it has already been replaced by a successor whose
> durable predecessor tuple and activation CAS required the exact
> `G1 / E1 / C1 / N1` retirement claim. Where that successor is not the current
> pointer, the same proof is reached by walking the lineage chain.

Once that proof exists, the worker may conditionally set G1's `gc_state` to
`DELETING` on the generation row. This is physical cleanup, not a second active
pointer state, and it does not require a cross-table CAS.

Without step 2 before step 4 there is a losing interleaving: the global check
passes, `blocks` says `G1 RETIRED`, the worker crashes, and G2 activates and
overwrites the pointer. G1's generation row cannot help, because it never held a
retirement state at all — it carries only `materialization_state=VERIFIED` and
`gc_state=null`. Once the pointer names G2, the only durable proof that G1 was
legitimately retired is gone, and no worker can safely authorize deleting `K1`
afterwards.

#### Retirement Evidence Is Append-Only And Epoch-Keyed

A generation can be retired more than once. `RETIRING -> ACTIVE` is a legitimate
transition, so G1 may travel `RETIRING -> ACTIVE -> RETIRING` repeatedly, each cycle
under a different `retire_claim_epoch`. Evidence that a zero check passed is
therefore meaningless unless it says *which cycle* it belongs to.

Recording it as a mutable state on the generation row does not work, for two
separate reasons.

**It is not self-dating.** Worker A passes the zero check, records evidence, and
crashes before its pointer CAS. Its claim expires. Worker B takes over, finds a
reference, and reactivates G1 to `ACTIVE`. Much later a third cycle retires G1
again, and a recovery worker cannot tell whether the recorded evidence is its own
or two cycles stale.

**A stale worker can destroy a newer worker's evidence.** An unconditional write to
one mutable row is last-write-wins by wall clock:

```text
worker A   claim epoch 10   zero check OK, then stalls
worker B   takeover epoch 11, zero check OK, records evidence(11)
worker B   CAS pointer RETIRING -> RETIRED
worker A   wakes, records evidence(10)      <-- overwrites B's evidence
```

A's write is not merely useless. It can regress the row past `RETIRED` or
`DELETING`, discarding the delete authorization that this document elsewhere
designates a recovery source.

So the evidence is **append-only and keyed by claim epoch**, not a mutable state:

```text
block_generation_retire_evidence
    org_id
    block_id
    generation_id
    retire_claim_epoch      -- part of the partition key; one row per retire cycle
    retire_claim_id
    checked_at
    uses_read_at
    refs_read_at
    PRIMARY KEY ((org_id, block_id, generation_id, retire_claim_epoch))
```

**One evidence event, one partition.** Folding `retire_claim_epoch` into the
partition key rather than clustering under the generation matters for the same reason
it did for `block_generations`: a generation can travel
`RETIRING -> ACTIVE -> RETIRING` without limit, and this design retains evidence
forever, so the clustered form would accumulate every retire cycle of a hot
generation in one partition. Nothing needs the co-location — every consumer looks up
an exact `(G, N)` decided by the retirement-authorizing claim, never a range — and the
split also stops concurrent cycles of one generation contending on a single
partition's Paxos state.

Uses and references are the deliberate contrast: those *are* clustered under the
generation, because the destructive proof reads each of them as one complete
partition. Evidence is never read that way.

The write is the equivalent of:

```text
INSERT INTO block_generation_retire_evidence (...) VALUES (...)
IF NOT EXISTS
```

The retry must use the same claim ID, read timestamps, and generation identity. A
matching existing row is success; a row with the same key but different immutable
payload is a protocol violation and fails closed.

Each worker writes its own row with an immutable `INSERT ... IF NOT EXISTS` and can
never overwrite another's payload. This conditional write is intentionally on the
GC cold path, not the writer hot path. Consumers read the row for the
**retirement-authorizing claim of the generation they are deciding about**, which is
not always the pointer's current claim:

```text
retirement-authorizing claim for G:
    if the pointer reads G / RETIRED   -> the claim on the blocks row
    otherwise                          -> the claim recorded in the successor
                                          lineage link for G
```

Any other row is history. Reading "the live claim on the `blocks` row"
unconditionally is correct only in the first case and fails every delete of a
superseded generation; see correction 103.

An ambiguous evidence append is reconciled by re-issuing the same immutable
`INSERT ... IF NOT EXISTS` in the `SERIAL` Paxos domain. An exact-row
`(G1, retire_claim_epoch)` `EACH_QUORUM` read may inspect the settled result. A
matching immutable row means the append succeeded and the pointer CAS may proceed;
an absent row after the settled retry is retryable and the pointer must remain
`RETIRING`; a conflicting payload is a protocol violation. An uncertain result
preserves the claim and blocks every retirement, G2 activation, and delete decision.
The worker must never infer append failure from a timeout.

This needs no cross-table transaction. It needs an ordering rule, an epoch rule,
and a reconciliation rule:

| `blocks` view of G1/G2 | Evidence row and durable predecessor lineage | Recovery action |
|---|---|---|
| Pointer selects G1, state `RETIRING` | absent | Normal in-progress retirement; continue the drain from step 1 |
| Pointer selects G1, state `RETIRING` | present | Crash between the evidence append and the pointer CAS. **Never `DELETING`, never G2.** Revalidate the claim, re-run the global zero check, then complete steps 3 and 4 — or return to `ACTIVE` if a reference has appeared |
| Pointer selects G1, state `RETIRING` | absent at the authorizing epoch, though earlier cycles left rows | Identical to the row above: the lookup is a point read at the exact `(G1, N)`, so earlier cycles are simply not found. They are history and are never enumerated |
| Pointer selects G1, state `RETIRED` | present | Consistent; conditionally set G1 `gc_state=DELETING`, then proceed with exact-key cleanup |
| Pointer selects G1, state `RETIRED` | absent at the authorizing claim | **Protocol violation. Fail closed.** Quarantine G1; no `DELETING`, no G2, no S3 delete, and above all **no reconstruction of the missing evidence**. See Evidence Cannot Be Reconstructed After The Fact |
| Pointer selects G2 | evidence for the exact `N1`, and G2 predecessor is `(G1, E1, C1, N1)` | The zero check passed and G2 could only activate through a CAS requiring the matching pointer tuple; proceed to `DELETING` for G1 |
| Pointer selects G2 | missing/mismatched evidence or G2 predecessor lineage | Fail closed. Never delete `K1`; quarantine G1, and quarantine G2 as well when its own lineage is mismatched, using conditional transitions |
| Pointer selects G3 or later | an unbroken evidence/predecessor chain back to G1 | The chain proves G1 was superseded; proceed to `DELETING` for G1. See The Lineage Chain Is Transitive |
| Pointer selects G3 or later | the chain is broken at any link | Fail closed; quarantine G1 rather than deleting on inference |
| Either read uncertain | any | Preserve and retry |

#### The Lineage Chain Is Transitive

The rows above must not be written as if the pointer always names G1 or its
immediate successor. A delayed physical delete plus two quick rematerializations
produces this, which is an ordinary outcome rather than a corruption:

```text
G1 RETIRED -> G2 ACTIVE -> G2 RETIRED -> G3 ACTIVE
                                            ^ pointer, while K1 is still undeleted
```

A worker that only knows the `pointer = G1` and `pointer = G2` rows falls through to
fail-closed and retains or quarantines G1 **forever**. That is not a data-loss bug,
but it defeats the purpose of the physical delete, and a backlogged GC queue makes it
*more* likely rather than less — see Cost And Operational Impact.

Crucially, G2's own record is **not** sufficient proof on its own. A materializer
persists its predecessor tuple *before* the activation CAS, so a generation that
**lost** the race also carries `predecessor = (G1, E1, C1, N1)` and may even be
`VERIFIED`. "A row exists claiming G1 as predecessor" proves nothing.

The proof that does exist is retirement evidence, because of where it can be written:

> Retirement evidence for `(Gn, Nn)` can only exist if Gn was the pointer's active
> generation. The `ACTIVE -> RETIRING` CAS conditions on
> `active_generation_id = Gn`, and the evidence append is ordered after it.

So a losing generation can never accumulate evidence of its own, and the chain is
decidable by walking backwards from the live pointer:

```text
evidence(G1, N1)                        G1 passed its zero check under N1
    ^
    | G2.predecessor = (G1, E1, C1, N1)
    |
evidence(G2, N2)                        G2 held the pointer and was retired
    ^
    | G3.predecessor = (G2, E2, C2, N2)
    |
pointer = G3
```

Each link pairs one predecessor tuple with the matching claim evidence row for that
predecessor. Every link must match exactly; a single missing or mismatched link
fails the whole chain closed. This adds **no new table, no new write, and no new
Paxos** — every row involved is already required elsewhere in this document. What
PR-6 and PR-7 must implement is the walk itself, and PR-1 must guarantee the rows
survive long enough to be walked; see the evidence retention rule below.

An immutable "supersession receipt" was considered and is unnecessary: the
retirement evidence row already is one.

#### Evidence Cannot Be Reconstructed After The Fact

`RETIRING` with no evidence and `RETIRED` with no evidence are opposite situations,
and an earlier revision treated them the same way — "step 2 was lost, re-run the zero
check and append evidence". That is safe for the first and is a safety hole in the
second.

The protocol guarantees the ordering: evidence is appended **and confirmed** before
the pointer CAS, is never deleted, never expires, and is read by exact `(G, N)`. So:

```text
pointer = RETIRING, evidence absent   -> normal, incomplete retirement
                                         re-run the zero check, append evidence

pointer = RETIRED,  evidence absent   -> the ordering was violated
                                         fail closed, quarantine
                                         no DELETING, no G2, no delete
                                         and no synthetic evidence
```

Re-running the zero check *now* and appending evidence would manufacture a row
indistinguishable from a legitimate one, and that row would then authorize
`DELETING`. The whole value of evidence is that it attests a global zero check
happened **before** the pointer crossed; a row written afterwards attests nothing
and launders a protocol violation into proof.

There is also no way to know *why* the evidence is missing. The system is in a state
this protocol says is unreachable, which is precisely the condition under which the
fail-closed rule forbids inference. Retention costs one object; a fabricated proof
costs the guarantee.

Four invariants govern the table, and all must be asserted in code rather than
inferred from it:

> `block_generations(G1).gc_state` alone never authorizes `DELETING`. The worker
> must additionally hold an evidence row whose epoch and claim ID match the claim
> that authorized *that* generation's retirement — the live pointer claim when the
> pointer still reads `G1 / RETIRED`, otherwise the claim recorded in the lineage
> link that supersedes it — and the
> authoritative pointer must either read `G1 / RETIRED`, or select a later
> generation reachable from G1 by an unbroken lineage chain — the immediate
> successor G2 being only its shortest case. See The Lineage Chain Is Transitive;
> an implementation that hard-codes "G1 or G2" strands every generation collected
> after two rematerializations.

> Retirement evidence is valid only when **both** `retire_claim_epoch` and
> `retire_claim_id` match the retirement-authorizing claim for that generation — the
> claim on the `blocks` row when the pointer still reads `G / RETIRED`, otherwise the
> claim recorded in the successor lineage link. The epoch identifies the row; the
> claim ID is part of the immutable proof
> payload. A worker that cannot match the epoch treats the evidence as absent; a row
> whose epoch matches but whose claim ID does not is a **protocol violation** and
> fails closed, never "close enough".

> No retirement evidence is ever overwritten, deleted, or expired by any path in
> this design. Under X1/X2 it is retained unconditionally; ageing it out at all
> belongs to the out-of-scope `PURGING_LOGICAL` protocol. In particular "a successor
> pointer exists" is **not** a release condition: the `pointer selects G2` and
> `pointer selects G3 or later` rows both consume that evidence, so ageing it on
> successor activation would send the cleanup worker straight into fail-closed and
> strand `K1` permanently.

> A G2 record is valid only when its immutable predecessor tuple matches the
> evidence row and the old pointer claim used by the activation CAS. The latest
> evidence row for G1 is not a substitute for that exact tuple.

The fail-closed row is the point of the rule: a generation whose retirement cannot
be proven is retained forever rather than deleted on inference.

Crash rules:

| Situation | Recovery action |
|---|---|
| Intent before PUT | Expire intent after its deadline; no object exists to delete |
| Use write ambiguous before PUT | No authority; confirm the use before any S3 operation |
| PUT before activation CAS | **Settle the pointer partition serially first** — an ordinary read cannot prove the activation was never accepted. Then apply Recovering A Crashed Activation |
| Activation CAS ambiguous | Reconcile per Ambiguous Activation Outcome; never orphan or delete on an uncertain read |
| Active pointer selects G but generation row remains `MATERIALIZING` | Complete/repair `materialization_state=VERIFIED`; never delete K |
| Active pointer selects another G | G lost the activation race; clean K only after confirming no references and no live uses |
| No active pointer and intent expired | Clean exact K after fail-closed confirmation, including use absence |
| Any uncertain read | Preserve G/K and retry |

Every cleanup branch requires confirming the absence of **use rows**, not only of
references. A materializer holds a use and no reference for its entire dangerous
window, so a recovery worker that checks references alone will delete a live key.

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

### Provisional Reference Lifetime Bounds The Upload-To-Commit Window

The pin protocol protects a single request. It does not protect the gap between two
requests, and the upload funnels have one: blocks are uploaded and pinned by
`up:<operation_id>` in one request, and the permanent `fs:` references are created
much later by the commit that publishes the fs_object
(`internal/api/v2/fs_helpers.go:1043-1058`).

The commit publisher is a writer under this protocol and must pin and revalidate
like any other. But unlike an upload it **cannot rematerialize**: it holds block
IDs, not block bytes. If the provisional reference expires before the commit runs,
the generation legitimately retires and the commit has no recovery path — it can
only fail and force the client to re-upload.

The provisional reference TTL is therefore a correctness parameter of the same kind
as the authority deadline, not a cleanup convenience:

```text
provisional_reference_ttl
    > maximum observed upload-to-commit window
    + client retry and resume budget
    + operational safety margin
```

PR-0's writer-lifetime inventory must measure this window explicitly, separately
from the single-request writer lifetime that sets the authority deadline. A
provisional TTL sized only for a single request will produce commit failures that
look like GC bugs.

Sizing it upward is cheap here precisely because a provisional reference no longer
parks a generation: the GC reactivates on any generation-bound reference, so a long
provisional TTL costs retention, not availability.

## GC Protocol

```text
1. Discover candidate                              LOCAL_QUORUM
2. ACTIVE G1 -> RETIRING                           SERIAL + EACH_QUORUM
3. Read all use rows for G1                       EACH_QUORUM
4. Read all refs bound to G1                      EACH_QUORUM
5. Decide, in this order:
     any read error / DC unavailable
         -> keep RETIRING, retry
     refs > 0
         -> RETIRING -> ACTIVE                     SERIAL + EACH_QUORUM
      refs == 0 and any use (PENDING or AUTHORIZED) has an
      unexpired authority_deadline
          -> keep RETIRING, drain
      refs == 0 and uses > 0 and every remaining use has expired authority
          -> publish + confirm the delayed candidate
             and its discovery projection            LOCAL_QUORUM
          -> RETIRING -> ACTIVE                     SERIAL + EACH_QUORUM
     refs == 0 and uses == 0
         -> append + confirm retirement evidence   SERIAL + EACH_QUORUM
         -> publish + confirm handoff work         LOCAL_QUORUM
         -> G1 -> RETIRED                          SERIAL + EACH_QUORUM
         -> the candidate/queue work may now retire
6. Allow G2 only after G1 is RETIRED
7. Persist + confirm delete-recovery discovery work
    for the exact (G1, K1)                         LOCAL_QUORUM
    <-- the handoff work may now retire
8. G1 generation `gc_state=null -> DELETING`      SERIAL + EACH_QUORUM
9. Persist/reconstruct the orphan projection for G1 + K1
10. DELETE exact K1
11. Mark G1 `gc_state=DELETED`; clean only G1-scoped
    physical/recovery metadata and retain the logical `blocks` pointer
12. Retire the discovery work row
```

Step 7 precedes step 8 deliberately. The orphan projection at step 9 is a retry
aid, not the discovery record: a crash between 8 and 9 would otherwise leave a
`DELETING` generation that no projection references and no scan may find.

The drain classifies a use by its `authority_deadline`, never by `PENDING` versus
`AUTHORIZED`. An unexpired `PENDING` row counts exactly like an unexpired
`AUTHORIZED` one, because it may belong to a writer that already revalidated against
`ACTIVE` and is about to promote itself; see correction 65. The `kind` and `state`
columns exist for recovery, not for the drain predicate.

The decision order matters. References are evaluated before uses, because a
reference is proof of liveness while a use is proof of pending or authorized work
that has not yet produced one. Testing
uses first would leave a generation that has both a reference and an in-flight
writer parked in `RETIRING` instead of reactivating it, which contradicts the
contract even though it is not itself unsafe.

The GC must not reactivate a generation merely because a use with live authority
exists. Such a use can be an operation that was fenced during revalidation and will
never publish; it earns a wait, not a reactivation.

Every use row must be *seen* and classified. An implementation must never treat a
use row as absent because its deadline looks expired and then proceed to `RETIRED`
— that is deletion on a local clock reading. The retention contract exists so the
row remains visible until the writer's authority window and write margin have
ended, which is precisely the window in which the row is still evidence. Expired
authority changes the decision from "wait" to "reactivate"; it never changes it to
"delete".

### Read Order Is Not Decision Order

Step 3 reads uses and step 4 reads references. That order is load-bearing and is
deliberately the reverse of the decision order in step 5. The two must not be
aligned:

```text
uses then refs (required)
    T3: uses = 0     -> no operation holds authority for G1
    T4: refs = 0     -> and no reference exists
    between T3 and T4 no reference can appear, because publishing
    requires an AUTHORIZED use and none existed at T3

refs then uses (unsafe)
    T3: refs = 0
    T3.5: a writer holding an AUTHORIZED use publishes ref(G1),
          then removes its use
    T4: uses = 0
    both reads returned zero and a live reference exists
```

An implementer who reads "references are evaluated before uses" in step 5 and
reorders the reads to match introduces exactly the deletion of live data this ADR
exists to prevent, and no single-DC test will catch it. The decision order is about
which answer wins; the read order is about what the answers prove.

### The Publication Frontier

The property the retirement path actually depends on, stated normatively:

> A global `EACH_QUORUM` observation of zero use rows followed by a global
> `EACH_QUORUM` observation of zero generation-bound references closes the
> publication frontier for G1: after that pair of reads, no new generation-bound
> reference to G1 can legally appear.

The proof composes four rules already required elsewhere in this document, and each
is load-bearing:

1. Only an operation holding an `AUTHORIZED` use may publish a generation-bound
   reference. Pin authorization requires revalidating the active pointer while it
   still selects G1 in `ACTIVE`; a materializer's use is `AUTHORIZED` from before
   its PUT.
2. A writer publishes its reference **before** removing its use, and the publish
   helper may remove a use only after confirming the reference.
3. An ambiguous reference result leaves the use in place. A writer never releases a
   use on an unconfirmed publish.
4. A use row inserted after the frontier reads can never reach `AUTHORIZED`,
   because its own revalidation is ordered after its insert and will observe
   `RETIRING` or later.

Rules 1-3 mean every operation capable of publishing was visible at the use read;
rule 4 means no new such operation can be created afterwards. Together they are why
the sequence "global zero check -> persist retirement evidence -> pointer CAS"
needs **no** second global reference check between its steps. Without this stated
as an invariant, a PR-6 implementer will either insert a redundant global check
between the persist and the CAS, or — much worse — relax one of rules 2-4 while
believing the frontier is maintained by the reads alone.

The frontier is a statement about the *ability to publish*, not about row absence.
See `RETIRED` above for the same distinction applied to use rows.

### Escaping `RETIRING` On Abandoned Uses

`RETIRING` blocks every writer of that logical block. If the GC parked in
`RETIRING` for as long as any use row remains visible, a single writer that crashed
holding an `AUTHORIZED` use would make the block unwritable for the whole
`retention_expires_at` window — the same availability failure this document already
rejects for long-lived provisional references, and for the same reason.

So the drain distinguishes two cases that the retention contract keeps separate:

| Remaining use rows | Action |
|---|---|
| At least one `AUTHORIZED` use whose `authority_deadline` has not passed | Keep `RETIRING` and drain; that writer may still legitimately publish |
| At least one `PENDING` use whose `authority_deadline` has not passed | Keep `RETIRING` and drain; it may have revalidated before the committed fence, but it cannot perform physical work until authorization |
| Uses > 0 and every remaining use has an expired `authority_deadline` but unexpired `retention_expires_at` | `RETIRING -> ACTIVE` |

Reactivating on expired-authority uses is retention-safe by construction: `K1` was
never deleted, and the publish helper independently rejects a writer whose authority
deadline has passed, so the abandoned owner cannot benefit from the reactivation. It
converts an availability stall into retention, which is the trade this ADR makes
everywhere else.

This does not reintroduce `pin -> ACTIVE`. A use with live authority still never
justifies reactivation; it justifies waiting. Only a use that can no longer produce
a reference — and therefore can no longer be drained into a decision — releases the
fence.

After such a reactivation the generation is `ACTIVE` with zero references, and no
reference-removal event will ever re-create its candidate — the references were
already gone when it became a candidate the first time. Something has to schedule
the re-examination.

That something must be an explicit re-enqueue, not the recovery scan. The
reactivating worker writes a fresh candidate with a delay:

```text
RETIRING -> ACTIVE (abandoned uses)
    -> write gc_block_candidate(org, block, generation,
                                candidate_at = now + drain_grace)
    -> and its (day, bucket) discovery projection row
```

`drain_grace` should exceed the longest remaining `retention_expires_at` among the
uses that caused the escape, so the next cycle finds the rows gone rather than
repeating the same reactivation.

The pointer reactivation and delayed-candidate enqueue are deliberately not one
transaction, so the ordering between them matters. **The candidate must be written
before the CAS, not after it.**

An earlier revision wrote it afterwards and justified the crash window by saying
recovery scans `ACTIVE` generations with no reference and no live use as the
backstop. That backstop no longer exists: recovery is projection-driven, and
`gc_generation_zero_ref_by_day` only helps if a row was already published. A crash
between the CAS and the candidate write would leave a generation that is `ACTIVE`
with zero references, invisible to every automated path, retained forever.

This is Discoverability Before Irreversibility applied to a transition that is not
destructive but is still state-changing:

```text
1. write the delayed candidate (candidate_at = now + drain_grace)
   and its discovery projection row
2. CAS RETIRING -> ACTIVE
```

Writing it first is safe in the direction that matters. If the CAS then fails —
because a reference appeared, or another worker got there — the candidate is merely
stale, and the scanner revalidates canonical state before acting and deletes the row
if it no longer applies. A stale candidate costs one wasted revalidation. An
invisible `ACTIVE` generation costs the guarantee.

The candidate write must be retryable and fail closed, and a failure to publish it
is a reason not to proceed to the CAS — never a reason to roll back or weaken the
pointer transition once the CAS has committed.

Delegating this to the Recovery Protocol would be wrong on Cassandra: recovery is a
crash sweep, not a scheduler. Using it for ordinary scheduling turns a routine
outcome into a walk whose cost grows with total block count rather than with GC
work. Recovery stays as the safety net for stale or inconsistent work rows — not for
a missing post-CAS enqueue, which correction 97 makes unreachable: an unconfirmed
enqueue forbids the CAS, so a committed reactivation always has its discovery row.

## Recovery Protocol

`gc_s3_orphans` is a retry/discovery projection, not the only source of truth.

Recovery is **projection-driven, not scan-driven**, and it must be written against
the post-mirror model: `blocks` is the only authority for `ACTIVE`, `RETIRING`, and
`RETIRED`, while `block_generations` carries only `materialization_state` and
`gc_state`. No recovery branch may look for a pointer state on the generation row —
that column does not exist, and a branch phrased that way will silently never match.

Each branch is discovered through a bounded `(day, bucket)` projection in the
established pattern, and the projection key is then used to read the exact rows that
decide the case:

```text
discovery projection  ->  exact keys  ->  blocks pointer
                                          block_generations row
                                          block_generation_uses
                                          block_generation_references
                                          retirement evidence
```

The required projections and their branches are:

| Projection | Branch it recovers |
|---|---|
| expired materialization intents | `materialization_state=MATERIALIZING` past its deadline |
| expired materializer uses | a materializer use that expired with no published reference |
| zero-reference active generations | the pointer selects G, no reference and no live use exist — a materializer that died after activation, or a generation reactivated out of `RETIRING` on expired-authority uses. This is a **backstop**; the `RETIRING -> ACTIVE` escape schedules its own delayed candidate and that remains the normal path |
| retirement handoff | evidence exists for the authorizing claim while `blocks.active_state` is still `RETIRING` — the crash between the evidence append and the pointer CAS. A `RETIRED` pointer with no such evidence is **not** a recovery branch: it is a protocol violation routed to quarantine |
| pending physical delete | `gc_state=DELETING` with no orphan row, or with an orphan row in `pending_s3`, or whose S3 delete completed but metadata cleanup did not |

A full enumeration of `block_generations` remains available as an exceptional
offline reconciliation tool. It is not the recovery protocol, and no automated path
may depend on it.

A projection named only in prose is not implementable, and the branches above are
the ones a worker will otherwise try to satisfy with a scan. They therefore get
concrete shapes here, all in the `(day, bucket)` pattern established by
`gc_block_candidates_by_day` and its siblings, where
`bucket = GCDiscoveryBucket(org_id, block_id)`:

```text
gc_generation_intents_by_day        -- materialization in flight, from before the
                                       PUT until reference/zero-ref/orphan handoff
gc_generation_zero_ref_by_day       -- ACTIVE with no reference and no live use
gc_generation_handoff_by_day        -- retirement handoff in progress
gc_generation_deletes_by_day        -- pending physical delete work

base key, for the three whose subject is the generation itself:
    PRIMARY KEY ((due_day, bucket), due_at, org_id, block_id, generation_id)
    WITH CLUSTERING ORDER BY (due_at ASC, org_id ASC, block_id ASC,
                              generation_id ASC)

gc_generation_handoff_by_day adds retire_claim_epoch:
    PRIMARY KEY ((due_day, bucket), due_at, org_id, block_id, generation_id,
                 retire_claim_epoch)
```

The extra clustering column is not decoration. A generation can travel
`RETIRING -> ACTIVE -> RETIRING` many times, so without `retire_claim_epoch` two
retire cycles sharing a `due_at` collide on one row and one is silently lost — a work
item dropped in a design that has just forbidden the scan that used to catch it.

There is deliberately **no** separate materializer-use projection. An earlier revision
added one and left it half-normative: a fourth write outside the 3a/3b/3c sequence,
with no ambiguity contract and no place in the confirm order.
`gc_generation_intents_by_day` already owns the generation from before the PUT until
the reference, zero-ref work, or orphan work takes over, and it carries `(org, block,
generation)`, which is everything a recovery worker needs to read the exact use rows.
The canonical use row's TTL is then free to expire without hiding anything.

`due_at` is the time the branch becomes eligible, which lets a forward-dated row
land in a future day partition exactly as the delayed candidate does. Each row
carries the exact `storage_key` and `storage_class` so the worker can act without
re-deriving anything from the logical ID.

`gc_generation_deletes_by_day` is also the durable discovery record required by
Discoverability Before Irreversibility: it is written and confirmed before the
`DELETING` CAS and retired after `DELETED`. The two roles are deliberately the same
table, because a second table that must be written in the same window would only
add another crash gap.

The reverse reference projection needed for cleanup is:

```text
block_generation_references_by_referrer
    PRIMARY KEY ((org_id, referrer), block_id, generation_id,
                 reference_instance_id)
```

It partitions by referrer so a commit or publish-attempt cleanup can remove exactly
its own rows without resolving any logical block to its current generation. The
instance ID is in the key for the same reason it is in the forward table.

The publication rule is **not** "same logged batch as the canonical row". That
phrasing would contradict the sequential 3a/3b/3c protocol, and for retirement
evidence — a conditional `INSERT ... IF NOT EXISTS` whose projection lives in another
partition — it would demand a conditional cross-partition batch, which Cassandra
cannot express. A logged batch may be used where it happens to fit; it is never part
of the safety proof.

The invariant is one-directional and stated per family:

> A projection may transiently outlive its branch. It may never lag behind it.

```text
materialization      confirm intent -> confirm use -> confirm discovery -> PUT
RETIRING -> ACTIVE   confirm delayed candidate + projection -> CAS
retirement handoff   confirm handoff projection -> pointer CAS
physical delete      confirm delete projection -> DELETING CAS
```

A scanner that finds a projection row whose canonical state no longer matches deletes
only the projection row. That is what makes "publish early, retire late" free: the
cost of a stale row is one wasted revalidation, and the cost of a missing row is the
recovery guarantee.

### Discoverability Before Irreversibility

Removing the scan removes the backstop that used to catch a row nobody else knew
about. A projection-driven recovery therefore needs an ordering rule that the
scan-driven design did not:

> Before any irreversible or externally-visible step, a durable discovery record
> for that exact generation must already exist.

And, because a generation passes through several owners, the stronger form that
actually closes the protocol:

> **There must never be a window in which neither of two durable work identities
> exists.** Ownership is handed over, never dropped and re-acquired.

The first rule alone is not enough. It makes each step discoverable at the moment it
runs, but says nothing about the seam between one work record being retired and the
next being published — and with scans forbidden, a crash in that seam loses the
generation exactly as thoroughly as never publishing at all.

Three windows need the first rule, and all are otherwise unreachable:

```text
gc_state = null
    -> CAS gc_state = DELETING
    -> CRASH                        <-- generation is DELETING
    -> write orphan/recovery row        no projection, no orphan row,
                                        and scans are forbidden: nobody finds it
```

```text
persist MATERIALIZING intent
    -> PUT K                        <-- object exists physically
    -> CRASH                            with nothing scheduling its cleanup
```

```text
GC completes RETIRING -> RETIRED
    -> retire the candidate/queue work that discovered G1
    -> CRASH                        <-- blocks = G1/RETIRED, evidence valid,
    -> write gc_generation_deletes_by_day   K1 still present, and no work row
                                            names G1 any more
```

None of the three loses data — `K` is retained in all of them — but all three
contradict the recovery guarantee, and the first directly contradicts "`DELETING` is
recoverable from the generation record even if the orphan projection was not
written", which silently assumed a scan.

The third is the one the handoff rule exists for, and it has a worse sibling. If the
candidate/queue work is retired at `ACTIVE -> RETIRING` instead, a crash leaves the
block **permanently `RETIRING`** — a writer fence with no owner, which is an
availability failure rather than mere retention.

The required order within one step is:

```text
1. durable recovery/queue work row for (org, block, generation)
2. the irreversible step        (DELETING CAS, or the PUT)
3. ... the rest of the operation
4. only then retire the work row
```

And the required order **across** steps — the handoff rule — is that every GC-owned
transition names the work identity that covers it, with the successor published
before the predecessor is retired:

| Transition | Work identity that must be durable | Retired only after |
|---|---|---|
| candidate discovered → `ACTIVE -> RETIRING` | the candidate/queue row | never during `RETIRING`; it owns the fence |
| entire `RETIRING` drain | the same candidate/queue row | see below |
| `RETIRING -> ACTIVE` (escape) | the delayed candidate, published **before** the CAS | after the CAS commits |
| `RETIRING -> RETIRED` | `gc_generation_handoff_by_day`, published **before** the pointer CAS | after `gc_generation_deletes_by_day` is confirmed |
| `gc_state = null -> DELETING` | `gc_generation_deletes_by_day`, published **before** the CAS | after `DELETED` |

The materializer side needs the same table, and it was the half that stayed
implicit:

| Stage | Work identity that must be durable | Retired only after |
|---|---|---|
| step 3c → PUT → activation → publication | `gc_generation_intents_by_day`, published and confirmed at 3c | one of: a generation-bound reference exists; `gc_generation_zero_ref_by_day` is durable; or delete/orphan work is durable for a losing generation |
| materializer use held across PUT and activation | covered by the same `gc_generation_intents_by_day` row; there is no separate use projection | — |

The seam that closes is the one where a materializer wins activation and then dies:

```text
intent work exists -> PUT -> VERIFIED -> activation wins
    -> intent scanner sees "no longer MATERIALIZING" and retires its row   <-- FORBIDDEN
    -> request dies before publishing the reference
    -> canonical materializer use TTL-expires
    => ACTIVE with refs = 0 and no work row naming it
```

The scanner may not retire on "no longer `MATERIALIZING`". It must first observe a
generation-bound reference, or publish `gc_generation_zero_ref_by_day` itself. That
projection has exactly one publisher — whichever worker retires the intent work
without finding a reference — and it is published before that retirement, never
after.

The rule that ties them together, and the one an implementer must not optimize away:

> A worker retires its inbound work row only after the outbound work row for the
> next stage is durable. At every instant, at least one row names the generation.

`gc_generation_handoff_by_day` is what makes the `RETIRING -> RETIRED` seam
survivable: it is published before the pointer CAS, so the crash window in which
`blocks` reads `G1 / RETIRED` with valid evidence and no delete work yet is covered
by a row that already exists. Its scanner revalidates canonical state, so publishing
it for a retirement that then reactivates instead costs one wasted revalidation.

This needs **no additional Paxos**. The existing candidate/queue row carries the role
for the first two stages; PR-1 decides whether to extend it or add a dedicated
projection, but neither the intra-step ordering nor the handoff is optional.

No recovery branch may release a use row and clean its key in the same pass
without first confirming, at `EACH_QUORUM`, that the use is genuinely expired and
that no reference was published. Releasing a use and deleting its key on the basis
of a single local read is the recovery-side form of the materializer hole.

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

Greenfield means there is no data backfill or legacy dual-read requirement. It does
not mean migration history can be rewritten. `internal/db/migrator.go:20-40,303-315`
records checksums and refuses to boot when an applied migration changes.

Therefore `001_initial_schema.cql` must remain immutable. The final shape must be
introduced through one or more new numbered migrations after the current `013_`
series, for example `014_gc_generation_fence.cql`. Incompatible primary keys must
use new generation-aware tables; an `ALTER TABLE` cannot change an existing
Cassandra primary key. A clean deployment runs all migrations from `001` through
the new version, but no data backfill is required.

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
active_epoch
retire_claim_id
retire_claim_epoch
retire_claim_deadline
```

The logical `blocks` row is not deleted when one physical generation is collected.
Generation cleanup deletes only the exact physical key and generation-scoped
recovery/liveness work. If no successor exists, the pointer may remain
`active_state=RETIRED` while the predecessor generation has `gc_state=DELETED`; that
combination means "no usable active bytes, but a durable logical predecessor exists"
and is the input to the next rematerialization. A new generation then replaces the
pointer through the normal predecessor CAS. This keeps `active_epoch` monotonic and
avoids a second empty-pointer state or a reset caused by deleting the logical row.

The predecessor tuple and its matching retirement evidence must remain durable while
the retained pointer can name that predecessor, **and** while any lineage chain still
needs them as a link. A successor taking the pointer does not release them: the
cleanup worker consumes exactly those rows to prove that a delayed `K1` may be
deleted, and The Lineage Chain Is Transitive walks them across arbitrarily many
generations. Under X1/X2 they are never released at all: no path in this design
deletes or expires a `blocks` row, a generation row, a predecessor tuple, or a
retirement evidence row as logical-history compaction. That is the whole content of
the retention decision recorded in Cost And Operational Impact, and it is what makes
the lineage walk safe at any depth and any age.

### Generation History

```text
block_generations
    org_id
    block_id
    generation_id
    predecessor_generation_id
    predecessor_active_epoch
    predecessor_retire_claim_id
    predecessor_retire_claim_epoch
    materialization_state -- MATERIALIZING or VERIFIED; not pointer authority
    gc_state              -- null, DELETING, DELETED, or QUARANTINED
    active_epoch
    storage_key
    storage_class
    size_bytes
    sha1
    representation_id
    materialization_owner
    materialization_deadline
    delete_claim_id       -- recorded by the DELETING transition; never a precondition
    delete_claim_epoch    -- recorded by the DELETING transition; never a precondition
    delete_authorized_at
    quarantined_at
    quarantine_reason
    created_at
    updated_at
    PRIMARY KEY ((org_id, block_id, generation_id))
```

The primary key is not optional detail. Every generation-row LWT in this document
addresses exactly one `generation_id`, and the argument that a stale worker cannot
reach a different lifecycle rests on that key plus the never-reused generation UUID.

**One generation is one partition.** Folding `generation_id` into the partition key
rather than clustering under `(org_id, block_id)` matters because this design also
decided to retain logical history indefinitely. Under the clustered form, every
generation a hot SHA ever had would accumulate in a single unbounded partition —
exactly the wide-partition shape Cassandra's own guidance and size guardrails warn
against, and one an adversarial or merely popular workload drives without limit.

Nothing needs the co-location. Recovery is projection-driven, and the lineage walk
always knows `(org_id, block_id, generation_id)` exactly, because each predecessor
tuple names the next generation to read. Every access in this document is a point
read or a point LWT.

The separation also removes Paxos contention between lifecycles: LWT ballots are
per-partition, so under the clustered form G1's `DELETING` transition would
serialize against a concurrent quarantine or delete of G500 on the same SHA. It
changes no Paxos count, only which partition each round contends on.

This row deliberately carries **no live `retire_claim_*` columns**. The retirement
claim lives on the `blocks` pointer, which is where it is installed and matched. The
`delete_claim_*` columns record which claim authorized the physical delete, so the
generation record remains a self-contained recovery source, and they are written by
the conditional `DELETING` transition itself. Naming them separately is intentional:
a column called `retire_claim_id` on this row would invite exactly the unsatisfiable
`IF` clause that Conditional Generation-Lifecycle Transitions exists to forbid.

`materialization_state` is a monotonic marker: it starts as `MATERIALIZING` and
becomes `VERIFIED` only after the exact object has been PUT and verified. It is not
an active-pointer mirror and is never used to authorize deletion. `gc_state` is the
generation's terminal physical lifecycle and may advance only through the
generation/claim rules below. `ACTIVE`, `RETIRING`, and `RETIRED` exist only on the
authoritative `blocks` pointer. Retirement evidence does **not** live on this row;
see Retirement Evidence Is Append-Only And Epoch-Keyed.

The predecessor fields are null only for the first generation. For every
rematerialized generation they are immutable and must identify the exact retired
predecessor and retirement claim that authorized its activation; they are not
reconstructed from the latest evidence row.

`QUARANTINED` is a durable terminal state, not an operator note. The fail-closed
branch of the reconciliation table is reachable after any crash, so "quarantine G1"
has to survive a restart or the next worker simply re-derives the same ambiguous
state and either loops or, worse, resolves it differently. A quarantined generation:

- is excluded from candidate discovery, from the queue, and from every delete path;
- keeps its `storage_key` and `storage_class` so an operator can inspect the object;
- is never transitioned out of `QUARANTINED` by any automated path, only by explicit
  operator action;
- is surfaced by a metric and an audit event, because a quarantine means the
  protocol hit a state it could not prove safe.

Without this state a restarted worker cannot distinguish a quarantined G1 from a
generation whose `gc_state` is simply still `null` — an ordinary, healthy lifecycle
awaiting collection. That inference is precisely what the fail-closed rule exists to
forbid, and the generation row offers no other signal, since it no longer mirrors
any pointer state.

### Uses And References

```text
block_generation_uses
    org_id
    block_id
    generation_id
    use_id
    state                 -- PENDING or AUTHORIZED
    kind                  -- WRITER or MATERIALIZER
    epoch                 -- observed epoch (WRITER) or proposed epoch (MATERIALIZER)
    authority_deadline
    retention_expires_at
    operation_id
    PRIMARY KEY ((org_id, block_id, generation_id), use_id)
```

A materializer use is created in `AUTHORIZED` state as its own confirmed write in
step 3b, after the `MATERIALIZING` intent of step 3a and before the PUT — the two
are separate partitions and are never written as one operation. A writer use is
created `PENDING` and is
promoted to `AUTHORIZED` only after the active pointer is revalidated. Both kinds
are drained identically by the GC; the distinction exists for recovery, which must
know whether an abandoned use also implies an abandoned materialization.

Every use row is written with a TTL derived from its fixed `retention_expires_at`,
long enough to cover the authority deadline, clock skew, query/write margin, and
recovery margin. The `PENDING -> AUTHORIZED` update uses only the remaining TTL; it
must not reset the row's retention window. TTL expiration is a recovery signal, not
the authority check. PR-1 must also specify compaction and tombstone policy for
this high-churn table, including the allowed `gc_grace_seconds` and repair SLA.
An accidentally resurrected use is fail-safe retention rather than data loss, but a
short grace period is not free: it is permitted only when the repair/anti-entropy
operational guarantee is documented and tested.

The `epoch` column carries different meanings by kind, and an implementation must
not validate them the same way. A writer use records the epoch it *observed* on the
active pointer, so validation compares it against the current pointer. A
materializer use is `AUTHORIZED` before the pointer carries its epoch at all, so it
records the epoch it *proposes* to install; it becomes comparable to the pointer
only after its activation CAS wins. Validating a materializer use against the
current pointer epoch before activation would reject every materializer.

```text
block_generation_references
    org_id
    block_id
    generation_id
    referrer
    reference_instance_id  -- stable for one attempt; never reused
    expires_at            -- immutable for this instance; null for permanent refs
    library_id
    created_at
    PRIMARY KEY ((org_id, block_id, generation_id), referrer, reference_instance_id)
```

The primary key must partition by logical block and generation so the destructive
check can read one generation globally without mixing G1 and G2. A retry of one
logical reference admission reuses its `reference_instance_id`; a distinct attempt
must receive a new value. This closes the provisional-reference ABA where a stale
expiry worker could delete a replacement row for the same `referrer`.

**`expires_at` is immutable for the lifetime of a `reference_instance_id`.** The
instance ID alone does not close the ABA if the same instance can be renewed in
place, because the stale row and the renewed row are then the *same* row:

```text
scanner:  reads instance I, expires = T1
writer:   reuses I, extends expires to T2
scanner:  DELETE exact PK (..., I)     <-- deletes the renewed reference
```

So the contract mirrors the one already frozen for `authority_deadline`:

```text
reference_instance_id   identifies one immutable reference admission
expires_at              fixed for the life of that instance
idempotent retry        same instance_id, same expires_at
renewal or extension    NEW instance_id, NEW expires_at
```

With that rule the scanner deletes `I1` while the live reference is `I2`, and the
exact-key delete provably cannot reach `I2`. This is what allows the delete to stay
a plain `LOCAL_QUORUM` statement with no LWT. A renewal that reused the instance ID
would silently require Paxos to be safe, which the taxonomy forbids on this path.

The existing
`block_references` primary key cannot be altered by migration; the new table is the
generation-aware source for the greenfield implementation, with a reverse
projection by referrer for cleanup.

The use and reference reads in the destructive proof must be single-partition
queries at `EACH_QUORUM`. They must not use a secondary index, `ALLOW FILTERING`, a
table scan, or a cross-generation partition. The exact primary keys are part of the
X2 proof, not an implementation detail.

`block_references` must become **dead**, not co-live. Greenfield means no backfill,
but it does not mean the old table can be left written or read: two liveness sources
for the same block is precisely the condition under which a GC worker concludes
"zero references" against the wrong table. PR-5 must remove every write and read of
`block_references` in the same change that introduces the generation-aware table, so
the compiler rather than a reviewer enforces the cutover — the same technique the
org-scoped-key series used when it deleted the global block APIs.

Every expiring `up:` and `pub:` reference also needs a durable expiry projection.
Its key must follow the established discovery-projection pattern in this schema
rather than partitioning by org:

```text
gc_provisional_generation_refs_by_day
    PRIMARY KEY ((expiry_day, bucket), expires_at, org_id, block_id,
                 generation_id, referrer, reference_instance_id)
```

This mirrors `gc_block_candidates_by_day`, `gc_s3_orphans_by_day`, and
`gc_failed_items_by_expiry` (`internal/db/migrations/001_initial_schema.cql:317-329,1180-1190,1254-1263`),
which all partition by `(day, bucket)` so the scanner can walk a day in parallel.
Partitioning by `org_id` would concentrate every expiring reference of the largest
tenant into one partition. The projection must cover `pub:` as well as `up:`; its
scanner confirms the exact reference row's expiry before cleanup and candidate
creation. The expiry scanner never deletes a reference based only on a stale
`(generation_id, referrer, reference_instance_id)` observation: canonical expiry
is by TTL, and only the matching `(expiry_day, bucket, expires_at, generation_id,
referrer, reference_instance_id)` projection row may be removed. Any explicit
reference delete targets the exact primary key, `reference_instance_id` included:

```text
DELETE FROM block_generation_references
WHERE org_id = O AND block_id = L AND generation_id = G
  AND referrer = R AND reference_instance_id = I
```

This is a plain `LOCAL_QUORUM` delete and **must not be an LWT**. The instance ID is
what makes the CAS unnecessary: a replacement admission carries a different ID, so an
exact-key delete provably cannot remove it. Read "condition on the instance ID" as
"address the exact row", never as a CQL `IF` — a reference cleanup path is exactly
the per-block operation whose cost multiplies, and the taxonomy forbids Paxos there.
A writer-owner callback is not a correctness requirement.

### GC State

Candidates, provisional-expiry rows, queue items, failed items, and orphan rows all
need `generation_id`. Orphans additionally need the immutable `storage_key`.

Candidates must also support a **future** `candidate_at`. Today a candidate is
always created at the moment references reach zero, so `candidate_at` doubles as a
creation timestamp; the `RETIRING -> ACTIVE` escape needs a candidate that becomes
eligible only after `now + drain_grace`. The discovery projection already
partitions by `(candidate_day, bucket)` and clusters on `candidate_at`, so a
forward-dated row lands in a future day partition naturally — but the scanner must
be confirmed to walk *today's* day partition rather than assuming every candidate
it can see is due.

Generations must carry `QUARANTINED` and a quarantine reason, and every discovery,
queue, and delete path must exclude that state.

The `014+` schema must ensure that G1 and G2 cannot collide on a candidate, queue,
orphan, evidence, or reference row.

`block_id_mappings` needs no generation column and no successor-aware cleanup logic,
because **generation collection must not delete these rows at all**. They are part of
the logical identity of the block, and this design retains the logical `blocks` row
indefinitely; a mapping that outlives its generations is exactly as correct as the
pointer that outlives them. Removing them belongs to the future `PURGING_LOGICAL`
work, which is out of scope.

That is strictly simpler than the successor-aware rule an earlier revision proposed,
and it removes a class of bug rather than specifying how to avoid it: there is no
"is a successor still using this mapping?" question to get wrong. Concretely, the
current `cleanupBlockMapping` call in the block-delete path must be dropped, not
made generation-aware.

## Current Code Evidence

The following current paths must be addressed by implementation PRs:

- `internal/db/block_references.go:159-171,546-560` contains the existing
  terminal first-writer `INSERT ... IF NOT EXISTS`. It is the LWT this ADR reuses
  for initial pointer creation and must not be duplicated by the generation fence.
- The upload path is not free of other Paxos today, so no acceptance criterion may
  be written as "exactly one LWT per upload". The existing baseline includes
  `internal/db/block_references.go:203-231,235-245` (released-stub repair claim and
  deletes), `:252-268` (`sha1` and `representation_id` backfill CAS),
  `:1082-1088` (`ReleaseBlockDeleteClaim`, a GC claim release),
  `internal/db/block_upload_sessions.go:157,180,243` (session slot
  acquire/release/commit), `internal/api/seafhttp.go:1890-1910` (three `gc_leases`
  LWTs acquiring, renewing, and releasing the upload-metadata finalize lease, which
  is squarely on the upload path), `internal/api/sync.go:3346` and
  `internal/api/v2/fs_helpers.go:647` (head-commit promotion), and
  `internal/db/file_locks.go`. These are outside this ADR's scope; the fence must
  simply not add to them on the per-block hot path.
- `internal/gc/store_cassandra.go:2077-2087` (`ClaimBlockDelete`,
  `IF gc_state != 'deleting'`) can create a partial `blocks` row against a missing
  partition, because Cassandra evaluates the condition against `null`.
  `internal/db/block_references.go:203-226` and
  `internal/api/v2/fs_helpers.go:1006-1010` are the existing stub-repair machinery
  for that state. This breaks "a `blocks` row exists implies a generation was
  activated" and must be handled explicitly; see Released Stubs Break "Row Exists
  Implies Active Pointer".
- `internal/api/sync.go:436` writes `UPDATE blocks SET last_accessed = ?`
  unconditionally into the partition that will hold the Paxos-managed active
  pointer. It is benign only because the column sets are disjoint; that
  disjointness must become a stated invariant.
- `docker-compose.prod.yml:174,182` still supports `SimpleStrategy` as a legacy
  fallback. Under a non-`NetworkTopologyStrategy` keyspace an `EACH_QUORUM` read
  silently degrades to an ordinary quorum, which is the exact guarantee X2 rejects.
  The activation gate needs a startup assertion, not an assumption.
- `internal/storage/s3.go:703` exposes `List` but has no caller in `internal/` or
  `cmd/`. There is no S3-side prefix enumeration to migrate today.
- Operation IDs are not uniformly stable across client retries.
  `internal/api/sync.go:3758-3760` derives one deterministically from repo and
  block; `internal/api/seafhttp.go:1869-1876` embeds a fresh `uuid.NewString()` and
  memoizes it only for the lifetime of one `ChunkUpload` (`:1167-1171`). Inline
  activation does not depend on either property, but any future request-to-worker
  handoff would need both; see correction 58.
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
- `internal/streaming/canonical_block_reader.go:29-54,180-247` resolves persisted
  canonical locations, while the current validation still rejects a persisted key
  that differs from the hash-derived key; generation keys require replacing that
  invariant with exact-key validation.
- `internal/gc/worker.go:403-606` implements the current local claim-then-verify
  delete flow.
- `internal/gc/worker.go:547-594` persists current orphan state before finalizing the
  logical block row, but future recovery must additionally reach `DELETING`
  generations through `gc_generation_deletes_by_day`, published before the
  transition rather than discovered by a scan.
- `internal/gc/worker.go:233` (`cleanupBlockMapping`) and
  `internal/gc/store_cassandra.go:2137` (`DeleteBlockMappingExact`) remove
  `block_id_mappings` rows keyed by logical `(representation_id, external_id)`, not
  by a physical generation, and today they run right after the logical `blocks` row
  is finalized. Under this design the generation collector **stops calling them**:
  mappings are logical identity and survive with the retained pointer row. The
  functions themselves stay for the future `PURGING_LOGICAL` work and for operator
  tooling.
- `internal/db/block_references.go:1082-1088` (`ReleaseBlockDeleteClaim`) is one of
  several existing conditional statements on the `blocks` partition —
  alongside `:203-226`, `:235-245`, `:252-268`, and
  `internal/gc/store_cassandra.go:2077-2087` (`ClaimBlockDelete`), `:2102-2115`
  (`ReleaseBlockClaim`), and `:2122-2135` (`FinalizeBlockDelete`). PR-2's serial
  domain gate must cover this whole inventory, not only the first-writer LWT: any
  one of them left on `LOCAL_SERIAL` reintroduces the split Paxos domain on the
  pointer partition.
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
- `internal/db/migrations/001_initial_schema.cql:1239-1263`,
  `internal/db/migrations/007_gc_s3_orphan_mapping_recovery.cql`, and
  `internal/db/migrations/009_block_representation_mappings.cql` define the current
  orphan shape. Later migrations add `external_sha1`, `recovery_phase`, and
  `representation_id`; none adds `generation_id` or an exact `storage_key`.
- `docker-compose.mr-cluster.yaml:245-248,285-288` currently sets `LOCAL_SERIAL`
  and therefore is not final evidence for global LWT behavior. The generation-fence
  profile must use `SERIAL` for every LWT touching `blocks`, including the existing
  first-writer path that competes on that partition.
- `internal/db/migrator.go:20-40,303-315` makes migration files append-only after
  application; the implementation must add `014+` migrations rather than editing
  `001_initial_schema.cql`.

Adjacent findings remain separate unless implementation deliberately closes them:

- X3 (`ISSUE-UPLOAD-PUT-BEFORE-INTENT-01`) is a storage-leak/discovery issue. The
  durable `MATERIALIZING` intent proposed here can close it for covered writers,
  but the ADR does not claim X3 closed until every writer is verified.
- X4 (the existing terminal first-writer Paxos cost) remains a deferred finding
  outside this ADR's scope and is tolerated as the current baseline. This design
  reuses that LWT rather than adding a parallel one, adds no Paxos to pins,
  references, reuse, repair authorization, or dedup, and adds one logical
  rematerialization activation operation. Retirement and delete LWTs run in the GC
  worker.
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

Before implementation, PR-0/Fase 0 must measure two different windows and must not
collapse them into one number:

- the maximum real **single-request** writer lifetime for every funnel, including S3
  upload, metadata, reference publication, retry, publish repair, and client
  cancellation. This sets the authority deadline and, through it,
  `retention_expires_at`;
- the maximum real **upload-to-commit** window, which sets the provisional
  reference TTL. See Provisional Reference Lifetime Bounds The Upload-To-Commit
  Window.

All safety margins are parameters derived from that inventory, not guessed
constants.

PR-0 must also measure the deployed `paxos_variant`, the complete activation critical
path (retirement-evidence `EACH_QUORUM` read plus activation `SERIAL + EACH_QUORUM`
CAS), and the p50/p95/p99 between every pair of participating DCs. It must also
measure GC candidate throughput under bounded concurrency and the queue age target.
Those numbers justify the inline decision and the worker capacity; without them the
inline-versus-background and serial-versus-concurrency questions can only be
asserted.

### PR-1: Final Greenfield Schema And Models

Add generation, materialization, pin, reference binding, candidate, queue, orphan,
claim, and recovery fields/tables. Freeze the exact single-partition primary keys
for uses and references, the use TTL/retention policy, the retained logical
`blocks` pointer after physical deletion, and the non-mutable generation markers.
Add Go models and migration tests.

Includes the append-only retirement-evidence table, the `QUARANTINED` state, the
`(expiry_day, bucket)` partitioning for the provisional-expiry projection, and a
decision on whether generation-aware claims can be written so they cannot create
stub rows at all rather than repairing them afterwards. The migration must include
bounded recovery projections for `MATERIALIZING` and `gc_state=DELETING`, and an
explicit tombstone/compaction policy for the high-churn use table.

Several schema decisions must be settled here rather than deferred, because each has
already been got wrong once:

- the generation row carries **no live `retire_claim_*` columns**, only the
  `delete_claim_*` values recorded by its own `DELETING` transition. A column named
  like the pointer's claim invites an `IF` clause that can never apply;
- `block_generations` is keyed `((org_id, block_id, generation_id))` — one
  generation, one partition — because logical history is retained indefinitely and
  the clustered form would grow without bound for a hot SHA;
- `expires_at` is immutable per `reference_instance_id`, and renewal allocates a new
  instance. Without that the instance ID does not close the expiry ABA;
- the first-writer LWT initializes `retire_claim_epoch = 0` with a null claim ID and
  deadline, so the first acquisition's `IF` clause is not a null comparison;
- every recovery discovery projection gets a concrete name, primary key, and
  publication rule, in the `(day, bucket)` shape used by the existing projections;
- the durable discovery record required by Discoverability Before Irreversibility is
  either the existing candidate/queue row or a dedicated projection — but the
  ordering it enforces is not optional;
- retention of the pointer/generation/evidence triple is accepted as unbounded, with
  a capacity note. Logical-history compaction is out of scope and must not be
  reintroduced as a TTL.

### PR-2: Explicit Consistency Helpers

Add local writer operations and explicit global destructive operations. Add query
context propagation and fail-closed behavior for all global reads/LWTs. Add serial
domain selection for every LWT that can touch a generation-managed `blocks` row;
ordinary writer operations remain `LOCAL_QUORUM`.

Includes startup assertions for `NetworkTopologyStrategy`, the exact configured DC
set and positive RF per DC, `SERIAL` for all LWTs that can touch a
generation-managed `blocks` partition, and a `paxos_variant` that retains
linearizable reads. `EACH_QUORUM` reads degrade silently without NTS; mixing
`LOCAL_SERIAL` and `SERIAL` on the same pointer partition is not an accepted
generation-fence profile; and a `*_without_linearizable_reads*` variant removes the
serial read that Recovering A Crashed Activation depends on. All three fail
silently, which is why all three are startup assertions rather than operational
notes.

### PR-3: Generation Allocation And Materialization

Generate UUID keys before PUT, persist and confirm the durable `MATERIALIZING`
intent, then its `AUTHORIZED` materializer use, then its recovery discovery work —
three separate partitions in that order, never as one conflated step — verify the
object, reuse the existing
terminal first-writer LWT for initial pointer creation, perform the inline
rematerialization activation CAS, hold the materializer use until the reference is
published, and record losing objects for exact orphan cleanup.

Also replace every hash-derived key assumption per the physical key parsing
inventory, including the `canonical_block_reader` validation that currently
rejects a persisted key differing from the derived key.

A non-applied first-writer LWT must be classified, not assumed: a returned row
carrying an `active_generation_id` is a real activation loss; a stub row is not,
and treating it as one loops the materializer.

### PR-4: Writer Pin Integration

Integrate pin creation, confirmation, revalidation, deadline enforcement, ambiguous
outcome handling, the full authority-tuple confirmation, the bounded non-active
state retry contract, and pin cleanup into every upload/reuse/publish funnel.

### PR-5: Generation-Bound References

Make provisional, publish, permanent, repair, and pending-OnlyOffice references
generation-aware without changing the logical `fs_object` content identity.

The same change must remove every read and write of `block_references`, so the
compiler prevents two liveness sources from coexisting.

### PR-6: Retiring GC And Claim Leases

Implement `RETIRING`, use drain, global reference checks in the specified decision
order, claim epoch/deadline takeover, append-only retirement evidence, `RETIRED`,
generation `gc_state=DELETING/DELETED`, `QUARANTINED`, and exact-key deletion.

The drain reads uses before references — see Read Order Is Not Decision Order — and
implements the `RETIRING -> ACTIVE` escape on expired-authority uses. Unexpired
`PENDING` uses keep the fence. Ambiguous pointer LWTs are settled in the serial
Paxos domain before the drain. The retirement handoff writes epoch-stamped evidence
before the pointer CAS and never authorizes `DELETING` from the generation row alone.
PR-6 must also implement bounded worker concurrency and a measured queue-throughput
target; a serial queue is not an adequate capacity plan for the global cold path.

### PR-7: Recovery And Readers

Make canonical reads use persisted keys, reach materializing and deleting generations
through bounded recovery projections, reconcile retirement evidence against the
retirement-authorizing claim, rebuild missing orphan projections, preserve the
logical pointer row, and clean only exact generations. Quarantined generations are
skipped by every automated path.

### PR-8: Verification And Activation Gate

Add unit, race, multi-DC, DC outage, crash, delayed-delete, and greenfield rollout
tests. Keep `GC_ENABLED=false` until all acceptance checks pass.

## Verification Plan

### Unit And Race Tests

Required cases include:

- ambiguous `BeginPin` never permits S3 use;
- `PENDING` pin confirmation and revalidation race with `RETIRING`;
- an `AUTHORIZED` pin can finish and publish while the pointer is `RETIRING`;
- a `PENDING` pin cannot publish after observing `RETIRING`;
- any generation-bound reference reactivates a generation;
- a use holding live authority never reactivates a generation; it extends the drain;
- a generation with one or more remaining uses, all of which have expired their
  authority, is reactivated rather than parked, so an abandoned pin cannot make a
  block unwritable for its full `retention_expires_at` window;
- zero uses follows the retirement branch rather than the expired-use reactivation
  branch; the "every use expired" predicate must not match an empty set;
- a reactivation on expired-authority uses writes and confirms its delayed candidate
  and discovery projection **before** the CAS; an unconfirmed enqueue keeps the
  generation `RETIRING` and performs no CAS at all;
- the GC reads uses **before** references, and a test asserts the unsafe ordering
  fails: with refs read first, a writer holding an `AUTHORIZED` use that publishes
  and releases between the two reads must be caught, not silently accepted;
- provisional references do not create a full-retention-TTL availability stall;
- with both a reference and a live use present, the generation reactivates rather
  than parking in `RETIRING`;
- deadline expiry rejects publication;
- a writer whose `authority_deadline` passed while it was stalled cannot authorize
  its `PENDING` pin, even when the pointer reads `ACTIVE` with the same generation
  and epoch it originally observed — the case the `RETIRING -> ACTIVE` escape makes
  reachable;
- a writer whose deadline expires between authorization and the physical operation
  performs no PUT or repair, so no untracked object is created at a key whose
  generation has moved on;
- a writer whose deadline has less than the configured clock-skew and operation
  margin is rejected before starting physical work, even when the deadline has not
  literally passed;
- a materializer whose operation deadline lacks the configured margin performs no
  PUT and leaves only an exact recovery record;
- `RETIRING -> ACTIVE` on abandoned uses writes a delayed candidate and its
  discovery projection row, and the generation is re-examined without any
  `block_generations` scan;
- once the reactivation CAS commits, the discovery row exists **by construction**, so
  a crash immediately after it leaves at most a stale candidate that the scanner
  revalidates and discards; a future-dated candidate is not processed before
  `candidate_at`;
- writers observing `RETIRING` stop after a bounded retry budget and return a
  documented retryable result;
- ambiguous authorization confirmed only by `use_id` existence is rejected; the
  full tuple including `state=AUTHORIZED` and epoch is required;
- an ambiguous `PENDING` insert is confirmed by identity and is *not* rejected for
  lacking `AUTHORIZED`;
- an acknowledged `PENDING` insert proceeds straight to revalidation with no
  read-back, and the writer-path trace shows the round trip is absent on the
  acknowledged path and present on the ambiguous one;
- an ambiguous materializer use aborts before PUT rather than proceeding;
- an ambiguous activation CAS reconciles against the authoritative `blocks` row and
  never orphans or deletes on an uncertain read;
- an ambiguous `ACTIVE -> RETIRING` LWT is settled in the `SERIAL` Paxos domain
  before any use/reference drain; an ordinary `EACH_QUORUM` read returning `ACTIVE`
  is not treated as proof that an accepted proposal was never committed;
- a rematerialization persists G2's predecessor tuple before activation, and the
  activation CAS requires `G1`, `E1`, the retire claim ID, and the retire claim
  epoch from that tuple;
- a missing retirement-evidence row or quarantined G1 prevents the CAS from
  activating G2;
- a recovery worker repairing a crashed materializer re-verifies the exact object
  before activating, and refuses to activate a generation whose key is missing or
  whose existence is uncertain;
- a crash after the MATERIALIZING intent but before the PUT never results in an
  activation: the recovery worker verifies the exact object first and refuses;
- an expired materialization deadline cleans only the exact key after the
  use/retention checks, and never allocates a replacement generation;
- a materializer refuses activation when its use `authority_deadline`, its
  `materialization_deadline`, or the use retention boundary lacks the configured
  margin, and an already-applied activation discovered after expiry cannot publish
  a reference;
- a G2 whose predecessor tuple does not match the retirement evidence is quarantined
  and never publishes or deletes either physical key;
- a quarantine/activation race is reconciled after the pointer CAS; a proven G2 may
  proceed without deleting quarantined G1, while a quarantined or unprovable G2
  cannot publish;
- a lost step-2 retirement persist is detected and the global zero check re-runs;
- `pointer=RETIRING` plus a matching claim evidence row **never reaches
  `DELETING`**: assert no `DELETING` transition, no `DeleteObject`, no G2
  activation, and that the global zero check re-runs before the pointer CAS
  completes;
- an evidence row from an older claim epoch is ignored rather than used to complete
  a retirement it did not earn;
- a generation that travels `RETIRING -> ACTIVE -> RETIRING` cannot have its second
  cycle satisfied by first-cycle evidence;
- a stale worker that appends evidence under an old claim epoch cannot destroy or
  regress a newer worker's evidence, and cannot overwrite the generation row's
  `gc_state` or the authoritative pointer;
- two writes for the same `(generation, claim_epoch)` cannot change the first
  evidence payload; the conditional insert treats an identical retry as success
  and a conflicting payload as a protocol violation;
- the **pointer transition** is valid as soon as the activation CAS commits, and a
  generation row whose `materialization_state` still reads `MATERIALIZING` is
  repaired rather than quarantined. This is a statement about the recovery worker's
  conclusion, not a permission: **publishing** a reference and any writer's use of
  the generation still require the positive predicate, so a lagging marker means
  fail-closed-and-retry for a writer and reverify-then-repair for recovery;
- a stale quarantine worker cannot overwrite `DELETING` or `DELETED`, because the
  transition is conditional on `gc_state = null` and the expected generation identity;
- a stale quarantine worker with an old operation ID cannot quarantine a newer
  `MATERIALIZING` lifecycle; and because `generation_id` is never reused, no stale
  worker can reach a different lifecycle on the same generation row at all;
- no generation-row LWT has a live `retire_claim_*` column in its `IF` clause. A
  test asserts that such a statement would never apply, because nothing populates
  those columns on `block_generations`, and that the `DELETING` transition instead
  *records* `delete_claim_id`/`delete_claim_epoch` for recovery;
- no generation-row LWT applies against a missing partition: a test issues each
  conditional form against a `block_generations` row that does not exist and asserts
  it does **not** apply and creates no partial row;
- every generation-validity check is the positive predicate
  `gc_state = null AND materialization_state = VERIFIED`; a test asserts that a
  `DELETING` or `DELETED` generation is rejected by every path that previously
  tested only for `QUARANTINED`;
- a `pointer = G3` cleanup for G1 succeeds through the transitive lineage walk, and
  a chain broken at any single link fails closed; a losing generation carrying the
  same predecessor tuple as the winner never satisfies the walk, because it holds no
  retirement evidence of its own;
- retirement evidence survives a successor taking the pointer, and a test asserts
  that ageing it at that moment would strand `K1` in fail-closed;
- the claim-column lifecycle holds across `RETIRING -> ACTIVE` and the activation
  CAS: `retire_claim_epoch` never decreases or resets, while `retire_claim_id` and
  `retire_claim_deadline` are null whenever the pointer reads `ACTIVE`;
- a materializer whose intent write is ambiguous confirms the intent before writing
  its use, and never PUTs or activates with an unconfirmed intent;
- no recovery branch queries a pointer state on `block_generations`, and every
  recovery branch is reachable through a bounded discovery projection rather than a
  table enumeration;
- a reference delete is a plain exact-key `DELETE` including
  `reference_instance_id`, and writer-path tracing shows no LWT on that path;
- a stale worker that observed `ACTIVE` before two completed retire cycles cannot
  acquire the claim afterwards; `retire_claim_epoch` never decreases, and a test
  asserts the stale acquisition would otherwise inherit a previous cycle's evidence
  row;
- an expiry scanner that read `(I, T1)` cannot delete a renewed reference, because a
  renewal allocates a new `reference_instance_id`; a test covers expiry racing
  renewal;
- a crashed activation whose proposal was accepted but not learned is settled
  serially before any orphan decision, and a test asserts `K2` is not deleted when
  the replayed proposal later makes G2 the pointer;
- recovery never activates a generation whose materializer authority has expired,
  even when the settled pointer still reads `G1 / RETIRED`;
- startup fails when `paxos_variant` drops linearizable reads under generation-fence
  writer mode, including with `gc.enabled=false`;
- a crash between the `DELETING` CAS and the orphan projection, and a crash between
  the intent and the PUT, are both discovered without any table enumeration;
- every normative sequence orders the discovery write before the irreversible step;
  a test asserts no `DELETING` CAS and no PUT occurs while the discovery record for
  that exact generation is absent;
- an evidence row whose `retire_claim_epoch` matches the live pointer claim but
  whose `retire_claim_id` does not is rejected as a protocol violation, not consumed
  as proof;
- the settlement read is issued as `Consistency(Serial)` on a `SELECT`, and a test
  asserts that the `SerialConsistency` setter on a non-conditional statement does
  not produce a serial round — the silent no-op form must fail the test;
- a **first** materialization whose first-writer LWT proposal was accepted but not
  learned is settled serially before any orphan decision; a test crashes the
  coordinator in that window and asserts `K1` is not deleted when a later LWT
  replays the proposal and the pointer becomes that generation;
- a released stub observed by a crashed first materializer is not treated as proof
  that no activation was proposed;
- a crash between the delayed-candidate write and the `RETIRING -> ACTIVE` CAS
  leaves only a stale candidate, which the scanner revalidates and discards; a crash
  in the reverse order — which the contract forbids — is asserted to be
  unreachable, since no `ACTIVE` generation may exist without a published
  discovery row;
- no code path deletes or expires a `blocks` row, a generation row, a predecessor
  tuple, retirement evidence, or a `block_id_mappings` row; a test asserts the
  absence of any TTL on those tables;
- at every instant of a GC cycle at least one durable work row names the generation:
  a crash injected immediately after `RETIRING -> RETIRED` and before the delete
  work is published is still recovered, and a crash after `ACTIVE -> RETIRING`
  never leaves an ownerless `RETIRING` fence;
- a repaired released stub yields `active_epoch = 1`, `retire_claim_epoch = 0`, and
  null claim ID/deadline, so the first `ACTIVE -> RETIRING` needs no null special
  case;
- the central publish helper rejects a generation that is merely not `VERIFIED`, not
  only one that is `DELETING`, `DELETED`, or `QUARANTINED`;
- with `pointer = G3`, G1's delete is authorized by evidence matching the claim in
  the lineage link, and a worker that compares it against the pointer's current
  claim fails the test rather than the delete;
- two expired uses of one generation sharing a `due_at`, and two retire cycles of
  one generation sharing a `due_at`, each produce two distinct projection rows;
- the topology, DC/RF, `SERIAL`, and `paxos_variant` assertions all fail startup in
  generation-fence writer mode, with `gc.enabled=false`;
- an unconfirmed step-3c discovery write aborts before the PUT, exactly as an
  unconfirmed intent or use does;
- a materializer that wins activation and dies before publishing its reference is
  still discovered: the intent work row is not retired until either a
  generation-bound reference exists or `gc_generation_zero_ref_by_day` is durable,
  and a test injects the crash in that exact window;
- a materializer use projection outlives the canonical use row's TTL, so an
  abandoned materializer is discoverable after the canonical row expires;
- a generation that retires, reactivates, and retires again writes its evidence rows
  to distinct partitions, and every consumer reaches them by point lookup on the
  authorizing `(G, N)` rather than by enumerating a generation's cycles;
- a `RETIRED` pointer whose authorizing evidence is absent is quarantined, and a test
  asserts that no path re-runs the zero check and appends evidence for it: the
  synthetic-proof route must fail the test, not merely be undocumented;
- the same absence under a `RETIRING` pointer is the ordinary in-progress case and
  does re-run the zero check, so the two branches are asserted to diverge;
- the retirement handoff publishes and confirms `gc_generation_handoff_by_day` before
  the pointer CAS and retires the candidate only afterwards; a crash injected between
  the CAS and the delete-work publish is still recovered through the handoff row;
- a generation whose `materialization_state` lags behind an activation committed in
  another DC is repaired rather than quarantined, while a writer observing the same
  lag fails closed and retries;
- generation collection leaves `block_id_mappings` intact, and a test asserts a
  later rematerialization of the same external ID still resolves;
- a worker that stalls between its pointer/evidence proof and its `DELETING`
  statement still deletes correctly, because `RETIRED` has no transition back to
  `ACTIVE` and the pointer can only have moved to a G2 whose activation required
  that worker's own predecessor tuple;
- a pointer-selected generation whose `gc_state` is `QUARANTINED` rejects new uses
  and physical operations rather than relying on the pointer state alone;
- a generation quarantined between pin insertion and authorization is rejected
  before authorization, and one quarantined between authorization and the physical
  operation is rejected by the final lifecycle-state check;
- a quarantined generation survives a process restart as `QUARANTINED`, is skipped
  by candidate discovery and every delete path, and is never automatically
  transitioned out;
- a generation whose retirement evidence is unreconstructable is quarantined, never
  deleted;
- the retirement handoff performs no second global reference check between the
  evidence persist and the pointer CAS, and a test asserts the publication frontier
  is what makes that safe: no `AUTHORIZED` use can be created in that window;
- a stub `blocks` row does not make a first materializer conclude it lost the
  activation race, and repeated materialization against a stub converges instead of
  allocating a new generation per attempt;
- a first-writer LWT that does not apply is classified by inspecting the returned
  row, never assumed to be a loss;
- a late `PENDING` use inserted after the final global read can never reach
  `AUTHORIZED`;
- an unexpired `PENDING` use observed by the drain keeps `RETIRING`, while a
  `PENDING -> AUTHORIZED` promotion preserves the original authority deadline;
- a materializer use is not rejected for carrying a proposed epoch the active
  pointer does not yet hold;
- a materializer holds an `AUTHORIZED` use across PUT, activation, and publication;
- a generation that is `ACTIVE` with zero references but a live materializer use is
  never retired to deletion;
- materializing generation whose active pointer is itself is completed, never deleted;
- materializing generation that lost the CAS becomes an exact orphan and its use is
  released;
- `DELETING` with no orphan row is recovered from the generation record;
- stale retire worker cannot transition after claim takeover, because
  `retire_claim_epoch` is in the `IF` clause of every GC-owned pointer transition;
- G1 cleanup cannot remove G2 references, mappings, or objects;
- no physical delete path accepts only logical `blockID`;
- any global read error prevents deletion;
- activation CAS cannot be satisfied by a condition spanning two Cassandra tables;
- writer-path tracing shows no fence-added Paxos for pins, revalidation,
  references, reuse, repair authorization, or normal deduplication; the activation
  CAS is absent from ACTIVE/reuse/dedup paths and appears only in the `RETIRED`
  rematerialization branch of the request handler;
- two concurrent rematerializers perform two logical activation operations
  and exactly one succeeds; ambiguous retries reuse the same generation and do not
  allocate another one;
- a storage key carrying a generation suffix round-trips through every parser in
  the physical key inventory;
- all use rows, including rows whose authority expired but whose retention has not,
  are visible to the drain and are classified by authority rather than ignored;
- no unconditional statement writes any active-pointer or `retire_claim_*` column;
- the provisional reference TTL is greater than the measured upload-to-commit
  window, and a commit whose provisional reference expired fails closed with a
  documented error rather than publishing a reference to a dead generation;
- under generation-fence writer mode and a non-`NetworkTopologyStrategy` keyspace, startup
  fails; the process must not run destructive GC where `EACH_QUORUM` reads degrade
  to an ordinary quorum.
- startup also fails when the keyspace DC set differs from the configured
  participating DC set or any expected DC has an RF different from its configured
  positive value.

### Cassandra Multi-DC Tests

Use the pinned Cassandra `5.0.6` image with `NetworkTopologyStrategy`, RF 1 per DC
initially, and a test profile configured with `SERIAL`, not `LOCAL_SERIAL`. The
harness must query `SELECT release_version FROM system.local` and fail unless the
expected engine version is present.

Required scenarios:

- reference written in USA with `LOCAL_QUORUM`, final GC read from the other DC with
  `EACH_QUORUM`;
- same test in the reverse direction;
- pin written in one DC and drained globally;
- `PENDING`, `AUTHORIZED`, and materializer uses are all included in the global drain;
- `RETIRE` visibility through a later local writer read;
- inline G2 activation in one DC is visible to a GC worker in the other DC before
  it can retire G2;
- two concurrent rematerializers in different DCs produce exactly one winner and
  one exact orphan;
- an activation CAS that times out but applied is reconciled as a win, not retried
  into a second generation;
- a crash between the G1 retirement persist and the pointer CAS is recovered by
  re-running the global zero check, and the intermediate state
  `pointer=RETIRING` with a matching claim evidence row never produces a
  `DELETING` transition, an S3 delete, or a G2 activation;
- an injected inconsistent state that looks like a pointer CAS occurred without
  matching retirement evidence refuses G2 activation and quarantines/retains G1
  instead of deleting `K1`; the normal protocol cannot produce this order because
  evidence is appended before the pointer CAS;
- retirement evidence from a cycle that was subsequently reactivated is rejected on
  epoch mismatch in a later cycle;
- an authorized writer publishes after `ACTIVE -> RETIRING` without requiring an
  atomic state/reference transaction;
- a provisional `up:`/`pub:` reference causes `RETIRING -> ACTIVE` rather than a
  retention-length availability stall;
- a writer that crashes holding an `AUTHORIZED` pin causes `RETIRING -> ACTIVE`
  once its authority deadline passes, and uploads of that block succeed again
  without waiting for `retention_expires_at`;
- the same harness run against a `SimpleStrategy` keyspace demonstrates that the
  `EACH_QUORUM` reference read no longer intersects per DC, so the startup
  assertion is proven necessary rather than assumed;
- a `NetworkTopologyStrategy` keyspace with a positive but incorrect RF or an
  unexpected DC is rejected by the same startup gate;
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

For the later implementation and integration phases, unit and integration tests
must not be conflated. `-short` is for the fast unit path; it is not evidence for
the real Cassandra tests. The repository's Docker-first verification includes:

```text
./scripts/test.sh go
./scripts/test.sh go-integration
docker compose --profile test run --rm gotest go test ./internal/db ./internal/gc ./internal/api/v2 ./internal/streaming ./internal/storage -short
docker compose --profile test run --rm gotest go test -race ./internal/db ./internal/gc ./internal/api/v2 ./internal/streaming ./internal/storage -short
docker compose --profile test run --rm go-integration-test go test -tags integration -v -count=1 -timeout 10m ./internal/integration/...
./scripts/run-mr-cluster.sh up
./scripts/run-mr-cluster.sh status
```

The integration timeout is not uniform today. `Dockerfile.gotest` sets no timeout
at all (`CMD ["go", "test", "./..."]`); the `go-integration-test` service in
`docker-compose.yaml` already passes `-timeout 10m`; and `scripts/test.sh:802,821`
uses `-timeout 5m` on its local path. Use drains, claim-deadline takeover, and
DC-outage scenarios are wall-clock bound and do not fit a 5-minute budget, so PR-8
must raise the `scripts/test.sh` path to match the compose service rather than let
the same suite time out differently depending on how it is invoked.

The multi-DC harness must run with the generation-fence test profile and
`CASSANDRA_SERIAL_CONSISTENCY=SERIAL`, pinned to Cassandra `5.0.6`; the existing
replication script alone is not sufficient evidence.

Also verify that:

- every internal link resolves;
- no document says X1 or X2 is closed;
- `GC_ENABLED=false` remains the operational rule;
- `LOCAL_SERIAL` is not described as final X2 evidence;
- the ADR does not describe unimplemented code as current behavior;
- all proposed table/state names are labeled design unless implemented.

## Rollout And Activation

The greenfield rollout is:

1. Bootstrap the final generation-aware schema on a `NetworkTopologyStrategy`
   keyspace. `SimpleStrategy` is not a supported topology for destructive GC.
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
| Materialization, existing first-writer integration, and inline activation CAS | 5-8 engineer-days |
| Pins, deadlines, and all writer funnels | 6-9 engineer-days |
| Generation references and publish repair | 4-7 engineer-days |
| Retiring GC, claims, and exact deletes | 6-9 engineer-days |
| Recovery and readers | 4-7 engineer-days |
| Tests, multi-DC drills, and rollout | 8-12 engineer-days |

Expected total in the original split: **40-65 engineer-days**. With the now-required
serial-domain gate, ambiguity reconciliation, exact recovery keys, tombstone policy,
concrete recovery projections, and measured GC concurrency, use **55-75
engineer-days as the planning reserve** until PR-1 and the first multi-DC prototype
refine it. This is a planning range, not a measured delivery commitment.

The low end of the total remains optimistic. It assumes the writer inventory finds
no funnel that resists the pin protocol, which the Sync no-DB fallback already
contradicts.

The complete successful writer protocol has approximately five to six local
Cassandra interactions per block when pin confirmation, active-pointer
revalidation, authority validation, reference publication, and pin removal are
counted. Existing probe/reference work may be reused or coalesced where the same
query already supplies the observation, so the incremental cost must be measured
against the current path; it must not be budgeted as only three round trips.

The fence adds no Paxos to those per-block request operations. It creates one
logical activation operation per materializing request only when a previously
retired SHA comes back to life. Concurrent operations for the same block
may retry physical CAS executions when results are ambiguous, but every retry uses
the same generation, epoch, and predecessor tuple. The pre-existing upload LWTs
listed in Current Code Evidence are unchanged.

Cold-path cost is several global reads/LWTs per candidate and intentional dependence
on the slowest participating DC. A DC outage causes retention rather than deletion.
The worker must provision bounded concurrency against a measured target; serial
processing is not an acceptable implicit capacity plan once the per-generation WAN
rounds are introduced.

That statement needs an order of magnitude, or PR-6 will satisfy it with an
arbitrary small number. A classic Cassandra LWT is **four** round trips —
prepare/promise, read of current values, propose/accept, commit — which is exactly
the count Paxos v2 halves. `paxos_variant` defaults to v1, and nothing in this
repository sets it. At a 90 ms inter-DC RTT:

```text
v1: 5 LWTs x (4 x 90 ms) + 2 EACH_QUORUM reads x 90 ms ~= 2.0 s per generation
v2: 5 LWTs x (2 x 90 ms) + 2 EACH_QUORUM reads x 90 ms ~= 1.1 s per generation

today: gc.batch_size = 100, gc.worker_interval = 30s, and the queue loop in
       internal/gc/worker.go:262 is strictly serial

      100 x 2.0 s = ~200 s per batch against a 30 s tick
```

Real Cassandra traffic does not reduce perfectly to RTT x rounds, so these are
illustrative bounds rather than predictions — which is precisely why PR-0 measures
`paxos_variant` and the real per-DC latencies instead of reasoning from this table.

So the current worker shape cannot drain its own batch, and the backlog grows
monotonically rather than degrading gracefully. Sustained throughput falls from the
present ~3.3 items/s ceiling to under 1 item/s. Purging a 1 TB library at 4 MB
blocks is ~262k generations: days at serial, hours at 32-way concurrency. PR-0
measures the real numbers and PR-8 asserts the target; the arithmetic here exists so
the requirement cannot be read as a nicety. `paxos_variant` is the single lever that
moves both this figure and the X4 per-block cost, which is why PR-0 measures it
first.

Metadata retention is the other unpriced consequence of retaining the logical
pointer row. A fully collected block permanently keeps a `blocks` row, a
`block_generations` row, and at least one retirement-evidence row — roughly 750
bytes before replication — and the evidence rule forbids ageing that row out while
it is the only proof authorizing a future rematerialization. This is a deliberate
trade for `active_epoch` monotonicity and lineage, but it is a **regression against
current behaviour**, where `FinalizeBlockDelete` reclaims the row outright:

```text
1 TB/month churn at 4 MB blocks ~= 262k generations/month
                                ~= ~200 MB/month of permanent metadata
                                   for content that no longer exists, x RF x DCs
```

At RF 3 in two DCs that is a few tens of GB over several years — not alarming, but
monotonic and with no reclamation path in this design.

**For X1/X2 the decision is to accept it.** Unbounded logical-history retention is
unconditionally safe, and the alternative is not a cleanup routine — it is another
distributed protocol. A horizon-based collection of the pointer row, its generations
and their evidence "as one unit once every generation is `DELETED` and no reference
or use remains" carries the same admission race this ADR exists to close:

```text
cleanup: global check sees zero uses
                          writer: reads pointer = G1 / RETIRED
cleanup: global check sees zero refs
                          writer: persists G2 intent, PUTs K2
cleanup: deletes blocks + generations + evidence
                          writer: activation CAS finds no row
```

Closing that needs an admission fence of its own — something like a
`RETIRED -> PURGING_LOGICAL` pointer CAS that forbids new generation creation,
followed by a global drain, followed by the unit delete — which is another cold-path
LWT, another state, and another set of crash rules. The metadata figures above do not
justify it.

So: logical-history compaction is **out of scope for X1/X2** and is recorded here as
separate future work. If it is ever taken up, two rules from this analysis carry
forward: it requires its own admission fence, and the three row families are
collected as one unit or not at all. Collecting any of them alone — in particular
ageing evidence out while a pointer or a lineage chain can still name it — is unsafe
and must never be offered as a partial optimization.

Temporary G1/G2 coexistence consumes additional storage only after G1 reaches
`RETIRED`; G2 is forbidden during `RETIRING`.

Rematerialization cannot reuse K1 after retirement: it must PUT the complete payload
under K2, which is an intentional X1 trade but a real S3 bandwidth and active-active
replication cost. If a backing bucket uses versioning, `DeleteObject` may leave
non-current versions and delete markers; lifecycle/version cleanup must be included
in storage-cost and capacity planning rather than treating logical deletion as
immediate physical capacity reclamation.

## Acceptance Criteria

### X2 Closure

X2 is closed only when:

- every writer using an existing generation pins and revalidates before using it;
- every materializer persists a durable materialization lease and an `AUTHORIZED`
  materializer use before PUT, and holds it until its reference is published;
- no generation can be used by any in-flight operation without a visible use row;
- ambiguous pin writes grant no authority, and confirmation checks the contract
  appropriate to the write: identity for a `PENDING` insert, the full authority
  tuple for an authorization, and confirmed existence for a materializer use;
- every ambiguous lifecycle LWT reconciles against authoritative state rather than
  assuming it was not applied;
- an ambiguous `ACTIVE -> RETIRING` pointer LWT is settled in the `SERIAL` Paxos
  domain before any use/reference drain; an ordinary `EACH_QUORUM` read returning
  `ACTIVE` is not treated as proof that an accepted proposal was never committed;
- the keyspace uses `NetworkTopologyStrategy` and the process refuses to start
  destructive GC otherwise, since `EACH_QUORUM` reads degrade silently without it;
- the keyspace DC set exactly matches the configured participating DC set and every
  expected DC has exactly its configured positive RF;
- all LWTs that can touch a generation-managed `blocks` partition use `SERIAL`, and
  the activation gate rejects `LOCAL_SERIAL` for that profile;
- the global zero check reads uses before references, and the ordering is asserted
  by a test rather than left to reviewer discipline;
- the publication frontier is stated as an invariant and no second global reference
  check is inserted between the retirement persist and the pointer CAS;
- G1 retirement evidence is durably appended before the pointer CAS that lets G2
  activate, it is keyed by claim epoch and never overwritten, evidence alone never
  authorizes `DELETING`, and an unprovable retirement quarantines rather than
  deletes into a durable `QUARANTINED` state that survives restart;
- the gate for G2 is the pointer CAS, not a later generation-row mirror write, and
  no rule requires a condition spanning two Cassandra tables;
- `block_generation_uses` and `block_generation_references` are each read as one
  exact generation partition at `EACH_QUORUM`, with no index, scan, or
  `ALLOW FILTERING` fallback;
- no generation-row LWT conditions on a live `retire_claim_*` column; generation
  transitions condition on `gc_state` plus the never-reused `generation_id`, and the
  `DELETING` transition records `delete_claim_id`/`delete_claim_epoch` as recovery
  evidence in that same conditional statement;
- use rows have a fixed `retention_expires_at` and TTL; `PENDING -> AUTHORIZED`
  preserves the original authority deadline and retention boundary;
- pin authority is enforced at authorization and again immediately before any
  physical operation, not only at publish;
- `RETIRING` is never a parking state: it ends on a reference, on one or more
  remaining uses all having expired their authority, or on a zero drain, and an
  abandoned pin cannot make a block unwritable for its full retention window;
- every recovery cleanup branch confirms the absence of use rows, not only of
  references;
- expired authority cannot publish;
- pins and ordinary references remain `LOCAL_QUORUM`;
- retire, drain, final refs, and deleting use the required global levels;
- the GC decision order evaluates errors, then references, then uses;
- an unexpired `PENDING` use keeps `RETIRING` just like an unexpired `AUTHORIZED`
  use for drain purposes, while it still cannot perform physical work;
- a use holding live authority does not reactivate a generation;
- any generation-bound reference can reactivate a generation;
- exactly one liveness source exists: `block_references` is neither read nor
  written after PR-5;
- no unconditional write touches an active-pointer or `retire_claim_*` column;
- the provisional reference TTL exceeds the measured upload-to-commit window, and a
  commit that lost its provisional reference fails closed;
- failed global operations retain `RETIRING` and do not delete;
- a provisional reference cannot park a hot generation in `RETIRING` for its full
  retention TTL;
- writers observing `RETIRING` have a bounded retry/backoff and never hang
  indefinitely, and `RETIRED` does not stall a writer at all;
- the fence adds no Paxos to pins, revalidation, references, reuse, repair
  authorization, or deduplication, measured by writer-path tracing against the
  pre-existing LWT baseline;
- rematerialization performs one logical activation operation per materializing
  request, creating at most one generation regardless of how many physical CAS
  executions its ambiguity retries require, with exactly one successful activation
  per rematerialization, and retirement/delete LWTs run only in the GC worker;
- GC worker concurrency is bounded and has a measured throughput/queue-age target;
- the generation row uses monotonic materialization/physical markers rather than a
  second mutable `ACTIVE/RETIRING/RETIRED` authority;
- physical generation cleanup never deletes the logical `blocks` row, never resets
  `active_epoch`, and never removes a `block_id_mappings` row at all — mappings are
  logical identity and are not part of generation collection under any condition;
- the integration harness asserts Cassandra `release_version=5.0.6`;
- multi-DC and DC-outage tests pass.

### X1 Closure

X1 is closed only when:

- every new physical lifecycle uses a UUID generation key;
- no key is reused;
- G2 cannot exist before G1 is `RETIRED`;
- G1 and G2 can coexist safely after that point;
- readers use the persisted exact key;
- recovery uses generation and key, never logical hash resolution;
- `DELETING` recovery works without an orphan projection, through the work row
  written before the transition rather than through any table enumeration;
- active-pointer transitions use one `blocks` row, while terminal generation
  transitions use the generation row; no cross-table CAS is assumed;
- the logical `blocks` row remains available as a retired predecessor after G1's
  physical `gc_state` reaches `DELETED`, with the required evidence/lineage retained
  for a future G2 activation;
- the retained pointer/generation/evidence triple is kept **unconditionally**, with
  an explicit capacity note. No X1/X2 path collects, expires, TTLs, or horizons any
  of the three, together or alone; logical-history compaction is `PURGING_LOGICAL`
  work and is out of scope;
- a `blocks` row that exists without an `active_generation_id` is recognised as a
  released stub and never mistaken for a lost activation race;
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
5. The constraint is scoped to the fence, not to all of SesameFS. The upload path
   already contains several unrelated LWTs (stub repair, identity backfills,
   session slots, head promotion, file locks), so "exactly one upload-path LWT" is
   false and must never be restated. The fence adds no Paxos to pins, references,
   reuse, repair authorization, or ordinary deduplication.
6. A TTL is retention, not writer authority.
7. `pin -> ACTIVE` is invalid for a pin that still holds live authority; any
   generation-bound reference can reactivate during `RETIRING`, while an authorized
   pin can finish publishing without reactivation. Refined by correction 37: a
   generation with one or more remaining uses, all of which have expired their
   authority, does return to `ACTIVE`, to release the writer fence rather than to
   assert liveness.
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
16. An authorized pin may finish publishing while the pointer is `RETIRING`; a
    `PENDING` pin cannot.
17. Provisional references reactivate a retiring generation for availability-safe
    retention; a pin with live authority does not. See correction 37 for the
    expired-authority case.
18. `001_initial_schema.cql` remains immutable; greenfield removes backfill, not
    migration checksum/history requirements.
19. The real physical key preserves the existing two-level hash sharding before
    adding the generation suffix.
20. The acceptance harness must use a pinned Cassandra `5.0.6` image and must not
    treat `-short` tests as integration evidence.
21. G2 activation runs inline in the materializing request. Deferring it to a
    background allocator has now been proposed, adopted, and reverted once. The
    reversion reasons are recorded under Background Activation, Evaluated And
    Rejected: relocating a CAS does not remove a Paxos round, the round it relocates
    is on a rare path while the per-block Paxos is elsewhere, and the request-to-
    worker trust boundary introduces six distributed-protocol problems that inline
    does not have. Revisit only against measured activation latency, never on the
    assumption that "background" means "cheaper".
22. The materialization intent is accompanied by an `AUTHORIZED` use, held from before the
    PUT until the reference is published. Without it a freshly activated
    generation is `ACTIVE` with zero references and zero uses, which is exactly a
    GC candidate, and the GC could delete `K` under a successful upload.
23. The GC decision order is errors, then references, then uses. Testing uses
    first would strand a generation that has both a reference and a live writer.
24. Ambiguous authorization must be confirmed by the full tuple
    `use_id + generation_id + epoch + state=AUTHORIZED + authority_deadline`.
    Row existence alone would read a `PENDING` use as granted authority.
25. Changing the physical key requires auditing every consumer that parses, splits,
    lists, or reconstructs identity from a storage key, not only the derivation
    helper.
26. The headline "writers are regional" applies to the operations this fence adds.
    A first upload of new content already pays global Paxos because production sets
    `serial_consistency: SERIAL`.
27. An ambiguous LWT outcome is reconciled against authoritative state, never
    guessed. A timeout does not mean "not applied". This applies to the activation
    CAS and to every lifecycle transition.
28. G1 retirement must be durably appended in the epoch-keyed retirement-evidence
    table before G2 may overwrite the active pointer. `block_generations` carries
    physical-generation lifecycle only; once G2 owns the pointer, `blocks` no longer
    holds any evidence that G1 was retired. G2 must also persist the exact predecessor tuple
    `(G1, E1, C1, N1)` and its activation CAS must match the old pointer claim. An
    unprovable retirement quarantines; it never deletes.
29. `RETIRED` is a statement about authority, not about row absence. A late
    `PENDING` use may appear after the final global read; it is harmless because
    its own revalidation is ordered after its insert and will observe the fence.
    Do not write a drain loop that waits for "no use row can exist".
30. Ambiguous `PENDING` insert and ambiguous authorization have different
    confirmation contracts. Requiring `state=AUTHORIZED` to confirm a `PENDING`
    insert would abort a pin that landed correctly.
31. Recovery cleanup must confirm the absence of use rows, not only references. A
    materializer holds a use and no reference for its whole dangerous window.
32. Activation is one logical operation per materializing request, not
    "exactly one CAS per rematerialization". Ambiguous outcomes may cause repeated
    physical CAS executions against the same `(G, K, E)` and predecessor tuple;
    exactly one operation wins and no retry allocates another generation.
33. A `MATERIALIZER` use carries a *proposed* epoch, not an observed one, because
    it is `AUTHORIZED` before the pointer holds that epoch. Validating it like a
    `WRITER` use would reject every materializer.
34. `EACH_QUORUM` is only per-DC when the keyspace uses
    `NetworkTopologyStrategy`. Under any other strategy the **read** degrades
    silently to an ordinary quorum — the exact guarantee X2 rejects — while the
    LWT commit fails loudly. The strategy is a verified startup requirement, not
    an assumption, because the dangerous half of that pair is the silent one.
35. The global zero check must read uses **before** references. The decision order
    (references first) is deliberately the opposite and must not be used to justify
    reordering the reads: with references read first, a writer holding an
    `AUTHORIZED` use can publish and release between the two reads and both come
    back zero.
36. The publication frontier is a named invariant: zero uses followed by zero
    references proves no operation can still acquire the authority to publish. It
    is why no second global reference check is needed between the retirement
    persist and the pointer CAS. Left unstated, an implementer either adds a
    redundant check or relaxes the publish/release ordering that makes it true.
37. `RETIRING` must not be a parking state. A single abandoned pin would otherwise
    make a block unwritable for its whole `retention_expires_at` window — the same
    availability failure the ADR rejects for long-lived provisional references. A
    generation with one or more remaining uses, all of which have expired their
    authority, returns to `ACTIVE`; this is retention-safe and is not the rejected
    `pin -> ACTIVE` rule, which concerns uses that still hold live authority.
38. Retirement evidence is epoch-scoped **and append-only**. A generation can travel
    `RETIRING -> ACTIVE -> RETIRING`, so evidence that does not name its cycle is
    meaningless. A mutable state on the generation row fails twice: it is not
    self-dating, and a stalled worker holding an old claim can overwrite a newer
    worker's evidence — potentially regressing the row out of `RETIRED` or
    `DELETING` and destroying the delete authorization this ADR designates a
    recovery source. One immutable row per `(generation, claim_epoch)` removes both
    failures; its `INSERT ... IF NOT EXISTS` is an intentional cold-path Paxos
    operation and never runs in a writer request.
39. `pointer=RETIRING` with a matching claim evidence row is a normal, expected
    crash state and was missing from the reconciliation table. It authorizes
    nothing: no `DELETING`, no G2, no S3 delete. Evidence alone never authorizes
    deletion; the authoritative pointer must also have left `RETIRING`.
40. The gate for G2 is the **pointer CAS**. The activation CAS conditions on the
    `blocks` row alone, including `G1 / RETIRED / E1` and its retire claim ID/epoch,
    so a rematerializing request can win once that exact tuple is present; no later
    write to another table can hold it back. The generation row has no mutable
    ACTIVE/RETIRING/RETIRED mirror. Durable proof of retirement is the evidence row,
    the matching G2 predecessor tuple, and the pointer having left `RETIRING` —
    including the case where it already selects G2.
41. A `blocks` row can exist without ever having been activated. Cassandra applies
    `UPDATE ... IF col != v` to a missing row, so the existing GC claim can create a
    released stub; the codebase already repairs that state. "Row exists implies
    active pointer" is false, and a materializer that reads a non-applied
    first-writer LWT as a lost race will loop, allocating a generation and an object
    per attempt.
42. Epoch allocation is specified, not implied: `active_epoch` is monotonic per
    logical block, set once per activation, and unchanged by GC state transitions;
    `retire_claim_epoch` increments on every claim acquisition or takeover.
43. The pointer never holds `MATERIALIZING`, `DELETING`, or `DELETED`. Writer
    behaviour and publish-helper checks for those states read the generation row,
    not `active_state`. The state machine describes two machines and they must be
    kept distinct.
44. Pin confirmation is required only when the insert result is ambiguous. An
    acknowledged `LOCAL_QUORUM` insert already establishes the row and program
    order places it before revalidation; an ambiguous insert needs an identity
    read-back before proceeding. Confirmation must not be made unconditional on the
    writer hot path.
45. The provisional reference TTL is a correctness parameter bounding the
    upload-to-commit window, measured separately from the single-request writer
    lifetime. A commit publisher holds block IDs, not bytes, and cannot
    rematerialize.
46. `block_references` must be removed, not left co-live beside the
    generation-aware table. Two liveness sources is how a worker concludes "zero
    references" against the wrong one.
47. The provisional-expiry projection partitions by `(expiry_day, bucket)` like
    every other discovery projection in this schema. Partitioning by `org_id` would
    concentrate the largest tenant into one partition.
48. Active-pointer columns are Paxos-managed and no unconditional write may touch
    them. The `blocks` partition already receives an unconditional
    `last_accessed` update, so the disjointness of the column sets is the
    invariant that keeps this safe.
49. Pin authority is validated at **authorization**, not only at publish. The
    `RETIRING -> ACTIVE` escape makes it reachable for a stalled writer to
    revalidate after its own deadline passed and still find the pointer `ACTIVE`
    with the generation and epoch it first observed. Bumping `active_epoch` on
    reactivation would close that case but would reject an already-`AUTHORIZED`
    writer entitled to finish publishing across `ACTIVE -> RETIRING`, so the check
    belongs on the deadline. It is re-checked immediately before the physical
    operation, so an expired writer cannot recreate an untracked object at a key
    whose generation has moved on.
50. Confirming the pin is required only when the insert result is **ambiguous**. A
    successful `LOCAL_QUORUM` write already establishes the row at the requested
    consistency level, and program order puts it before the revalidation read, so
    both branches of the ordering argument hold without a read-back. An earlier
    revision of this ADR made the read-back unconditional and justified it with
    that same ordering argument; that was an unnecessary round trip on the hot
    path.
51. `RETIRING -> ACTIVE` on abandoned uses must write a delayed candidate rather
    than delegate re-examination to the recovery scan. No reference-removal event
    will re-create the candidate, and the scan over `block_generations` is a
    crash-recovery sweep whose cost grows with total block count; using it as a
    scheduler makes a routine outcome a full-table walk.
52. `QUARANTINED` is a durable generation `gc_state` with a reason, not an operator
    note. The fail-closed branch is reachable after a crash, so without a
    persistent marker a restarted worker cannot distinguish a quarantined
    generation from an ordinary unquarantined lifecycle — which is exactly the
    inference the fail-closed rule forbids.
53. G2 must carry the exact predecessor tuple `(G1, E1, retire_claim_id,
    retire_claim_epoch)` that authorized its activation. The activation CAS checks
    that tuple on the authoritative `blocks` row; the latest evidence row alone is
    not durable lineage. It also closes a hole `active_epoch` cannot: the epoch does
    not change across GC cycles, so without the claim tuple a materializer that read
    the pointer during retire cycle N1 could activate during cycle N2.
54. Quarantine transitions are conditional global LWTs on the generation row. They
    require the expected identity and `gc_state = null` and cannot overwrite
    `DELETING` or `DELETED`;
    ambiguous results are reconciled before any delete or publish action.
55. The expired-use reactivation predicate requires `uses > 0`; universal
    quantification over an empty use set must not prevent `RETIRING -> RETIRED`.
56. There is no late mirror finalize. The pointer is authoritative for
    `ACTIVE/RETIRING/RETIRED`, while generation cleanup is guarded separately by
    `gc_state` and the live retirement evidence/claim.
57. The topology gate verifies the configured DC set and exactly the configured
    positive RF for every participating DC, not only the replication strategy name.
    Authority checks also reserve clock-skew and physical-operation margin before
    starting work.
58. Any design that hands work from a request to another actor needs an operation
    identity with two properties, not one: identical across every retry of one
    logical attempt, **and** never reused between distinct attempts. Today's IDs
    have at most one each — `internal/api/sync.go:3758-3760` is deterministic and
    therefore repeats across physical lifecycles, while
    `internal/api/seafhttp.go:1869-1876` embeds a fresh UUID and therefore does not
    survive a retry. Inline activation needs neither property, which is part of why
    it is cheaper. Record this before anyone proposes a handoff again.
59. Every LWT belongs to one of four groups — existing hot path, new hot path
    (empty), rare rematerialization, GC cold path. State the group before adding
    one. "The generation fence uses Paxos" is not licence to write `pin IF ...`,
    `reference IF ...`, or `reuse IF ...`; those are the per-block operations whose
    cost multiplies.
60. The normal upload path is **not** Paxos-free today, and the taxonomy must not be
    read as saying so. `UpsertBlockMetadataWithSHA1` runs `INSERT ... IF NOT EXISTS`
    unconditionally with no pre-read, and an `IF NOT EXISTS` pays the full Paxos
    round whether or not it applies — so every block of every upload, including pure
    dedup hits, pays one global round in production. That is X4, it is the only
    Paxos whose count scales with block volume, and a probe fast path is the cheap
    lever because the probe has already read the row. Optimizing the fence's cold
    path while leaving this untouched is the mistake an earlier revision made.
61. The generation row has **no mutable `ACTIVE`/`RETIRING`/`RETIRED` mirror**. An
    earlier revision kept one "for history" and then had to defend it with a
    best-effort finalize, an ordering rule the system could not impose, and a
    monotonicity argument about regressing terminal states. Splitting the row into a
    monotonic `materialization_state` (`MATERIALIZING -> VERIFIED`) and a monotonic
    `gc_state` (`null -> DELETING -> DELETED`, or `QUARANTINED`) removes the whole
    class: the `blocks` pointer is the only authority for pointer states, and one
    LWT disappears from the cold path. Do not re-add a second mutable assertion of
    the same fact so recovery can read one table instead of two.
62. Physical cleanup **never deletes the logical `blocks` row**. Today
    `internal/gc/worker.go:554` does exactly that through `FinalizeBlockDelete`, and
    carrying that behaviour forward would reset `active_epoch`, orphan the
    retirement evidence, and create a "pointer row absent" outcome the activation
    ambiguity table does not cover. `active_state=RETIRED` with the predecessor's
    `gc_state=DELETED` is a valid, expected resting state and is the input to the
    next rematerialization. The cost of retaining it is priced in Cost And
    Operational Impact; the row may only ever be collected as one unit with its
    generations and evidence.
63. **An ordinary read does not settle a Paxos proposal.** A serial ballot can be
    accepted without its commit being learned, so an `EACH_QUORUM` read after an
    ambiguous `ACTIVE -> RETIRING` can still return `ACTIVE` while a later LWT
    replays the pending proposal. Draining from that read lets a writer observe
    `ACTIVE` and acquire authority after the fence was actually decided. Ambiguous
    pointer LWTs are settled by re-issuing the same claim LWT in the `SERIAL` domain
    before any use or reference read. This is the one place where the generic
    "reconcile against authoritative state" rule is not strong enough.
64. **One serial domain per partition.** Every conditional write that can touch a
    generation-managed `blocks` partition uses `SERIAL`. Mixing `SERIAL` and
    `LOCAL_SERIAL` on one partition creates two Paxos domains that do not order
    against each other; with RF 1 per DC the quorums coincide by accident, but at
    RF 3 per DC a local quorum and a global quorum need not intersect. The gate must
    cover the **whole** inventory of existing `blocks`-partition LWTs, not just the
    first-writer one, because the profiles ship `LOCAL_SERIAL` today.
65. **An unexpired `PENDING` use keeps `RETIRING`.** `PENDING` is not permission to
    do physical work, but the row may belong to a writer that already revalidated
    against `ACTIVE` and is about to promote itself. Treating `PENDING` as ignorable
    during the drain would retire underneath it. The drain classifies by authority
    deadline, never by `PENDING` versus `AUTHORIZED`.
66. **`authority_deadline` is immutable for the life of a use.** It is allocated
    when the `PENDING` row is created; the `AUTHORIZED` promotion carries the same
    value and consumes only the remaining TTL. A renewal is a new bounded use with a
    new `use_id`. An in-place extension would let a writer outlive the classification
    the drain already made and would break the publish helper's independent
    rejection of expired authority.
67. **Provisional references need a non-reused `reference_instance_id`.** The
    referrer alone is not a unique row identity across attempts:
    `internal/api/sync.go:3758-3760` derives `up:sync:<repo>:<block>`
    deterministically, so the same referrer genuinely recurs. Without the instance
    ID a stale expiry worker deletes the replacement row for a live upload — a
    reference-level ABA in a design whose whole subject is ABA.
68. **No generation-row LWT may condition on a retirement claim.** Removing the
    mirror left the quarantine CAS matching `retire_claim_id`/`retire_claim_epoch` on
    `block_generations`, columns that nothing populates and that no unconditional
    write is permitted to populate — an `IF` clause that could never apply. The
    claim proof is the authoritative pointer/evidence read that precedes the
    statement, and it is irrevocable because the pointer has no `RETIRED -> ACTIVE`
    transition. Generation-row transitions therefore condition on `gc_state` and the
    never-reused `generation_id`, and *record* the authorizing claim as
    `delete_claim_*` for recovery. The columns are named differently on purpose.
69. **The destructive proof is a single-partition read.** Uses and references are
    each read as one exact `(org, block, generation)` partition at `EACH_QUORUM`,
    with no secondary index, `ALLOW FILTERING`, or cross-generation scan. The exact
    primary keys are part of the X2 argument, not an implementation detail. Use rows
    additionally carry a TTL derived from their fixed `retention_expires_at`;
    without it an abandoned pin is immortal and the generation flaps between
    `RETIRING` and `ACTIVE` forever, burning global LWTs and never being collected.
70. **A serial GC queue is not a capacity plan.** Once every collected generation
    pays several WAN-sensitive LWTs, the existing serial loop cannot drain its own
    batch within its own tick. Bounded, measured concurrency and a queue-age target
    are requirements, not tuning. The arithmetic is recorded in Cost And Operational
    Impact so the requirement cannot be satisfied by an arbitrary small number.
71. **No generation-row conditional may be satisfiable by a missing row.** A bare
    `IF gc_state = null` applies against a `block_generations` partition that does
    not exist, creating a partial row with a `gc_state` and no `storage_key` — a
    `DELETING` generation whose exact key is unknown, which is unrecoverable. This is
    correction 41 reappearing on the new table, and it was introduced by the very
    commit that removed the mirror. Every form must match at least one non-null
    immutable identity column: `materialization_state = VERIFIED AND storage_key = K`
    for `DELETING`, `storage_key = K` for quarantine, `gc_state = DELETING` for
    `DELETED`.
72. **Generation validity is a positive predicate**,
    `gc_state = null AND materialization_state = VERIFIED`, never "not
    `QUARANTINED`". The negative phrasing is true for `DELETING`, for `DELETED`, and
    for a partial row, so an activation reconciled with it can publish a reference to
    a generation whose object is being deleted or is already gone.
73. **The lineage chain is transitive and must be walked.** The reconciliation table
    originally handled only `pointer = G1` and `pointer = G2`; a delayed delete plus
    two rematerializations produces `pointer = G3` and strands `K1` in fail-closed
    forever. A generation's predecessor tuple alone proves nothing, because a
    materializer persists it *before* the CAS and losers carry it too. Retirement
    evidence is the proof, since `ACTIVE -> RETIRING` conditions on
    `active_generation_id`, so only a generation that actually held the pointer can
    have any. Walk `evidence(Gn, Nn)` paired with `G(n+1).predecessor` backwards from
    the live pointer. No new table, write, or Paxos — and no "supersession receipt",
    because the evidence row already is one.
74. **Evidence is not released by a successor taking the pointer.** An earlier rule
    said it could be, while the reconciliation table simultaneously required that
    same evidence to authorize deleting the predecessor's key. Evidence and
    predecessor tuples are released only together with the generations they
    describe. **Superseded by correction 99**: X1/X2 releases them at no point
    whatsoever, and "once every one of those is `DELETED`" is not a release
    condition either — logical-history compaction is `PURGING_LOGICAL` work.
75. **The claim-column lifecycle is specified for every transition**, not only for
    `RETIRING -> RETIRED`. `retire_claim_epoch` is a monotonic counter that is never
    cleared; `retire_claim_id` and `retire_claim_deadline` are live only during
    `RETIRING` and `RETIRED` and are cleared by the same statement that reactivates
    or replaces the pointer. Clearing the epoch would let a stale cycle-N worker
    match a future cycle.
76. **The materialization intent and the materializer use are separate writes to
    separate partitions.** "Persist intent + use" as one line hid that they are not
    atomic and produced an ambiguity contract naming only the use. Confirm the intent
    first, then the use, then PUT. An activation with an unconfirmed intent leaves the
    pointer naming a generation that has no row at all. **Superseded in count by
    correction 88**: a third write, the recovery discovery record, was later inserted
    before the PUT, so the sequence is 3a/3b/3c and any text saying "two" is stale.
77. **Recovery is projection-driven, not scan-driven, and carries no pointer states.**
    The branch "scan `block_generations` for generations that are `ACTIVE`" survived
    the mirror removal and can never match, because `ACTIVE` exists only on `blocks`.
    Recovery discovers through bounded `(day, bucket)` projections and then reads
    exact keys; the zero-reference branch is a backstop behind the delayed candidate,
    never "the only path". A full enumeration of `block_generations` is an offline
    tool, not a protocol.
78. **Logical-history compaction is out of scope for X1/X2.** Collecting the retained
    pointer, generations and evidence has the same admission race as X2 — a writer
    can read `RETIRED` and begin materializing between the cleanup's zero check and
    its delete — so it needs its own `PURGING_LOGICAL` fence. Unbounded retention is
    accepted instead; the metadata figures do not justify another distributed
    protocol. Do not reintroduce it as a "simple TTL".
79. **A reference delete is a plain exact-key `DELETE`, never an LWT.** The
    non-reused `reference_instance_id` in the primary key is what removes the need
    for a CAS: a replacement admission has a different ID, so an exact-key delete
    cannot remove it. "Condition on the instance ID" means "address the exact row";
    read as a CQL `IF` it would put Paxos on a per-block cleanup path.
80. **A classic Cassandra LWT is four round trips**, not three — prepare/promise,
    read, propose/accept, commit — which is what Paxos v2 halves. An earlier revision
    of the cost model used three and understated the GC cold path by 25%. The
    corrected figures are ~2.0 s per generation at v1 and ~1.1 s at v2 for a 90 ms
    inter-DC RTT, which strengthens rather than weakens the concurrency requirement.
81. **Claim acquisition conditions on the previous `retire_claim_epoch`.**
    `ACTIVE -> RETIRING` was the one GC-owned transition a stale worker could still
    win, because `active_epoch` deliberately does not change across GC cycles: a
    worker that observed `ACTIVE` before two completed retire cycles could wake and
    reinstall an old epoch. The damage is to **epoch uniqueness**, which the evidence
    table depends on: the stale worker's own evidence append for that epoch is an
    `INSERT ... IF NOT EXISTS` that will not apply and whose payload conflicts, so it
    fails closed permanently and the block becomes unretirable. What separately stops
    it from *deleting* on the earlier cycle's evidence is correction 89's claim-ID
    match, not this CAS; both defences are required and neither substitutes for the
    other. The first-writer LWT initializes the epoch to 0, for the reasons in
    correction 90 — not because of any missing-row hazard.
82. **`expires_at` is immutable per `reference_instance_id`.** The instance ID does
    not close the expiry ABA on its own: if a retry may renew the same instance in
    place, the stale row the scanner read and the renewed row it deletes are the same
    row. Renewal allocates a new instance with a new expiry, which is what keeps the
    reference delete a plain exact-key statement instead of requiring a CAS. This is
    correction 66 applied to references instead of uses.
83. **Recovery settles a crashed activation; it does not activate one.** An ordinary
    global read cannot prove an activation proposal was never accepted, so
    "pointer reads G1, authority expired, therefore orphan `K2`" can delete the
    object a replayed proposal is about to make live. Settle the partition serially
    first. And settlement is not activation: re-issuing the CAS after the
    materializer's authority expired would create an `ACTIVE` generation nobody may
    publish to. This makes a linearizable serial read load-bearing, so
    `paxos_variant` values that drop linearizable reads join NTS and `SERIAL` on the
    startup gate — a third silent failure mode.
84. **Discoverability before irreversibility.** Removing the recovery scan removed
    the backstop that caught rows nobody had scheduled. A durable discovery record
    for the exact generation must exist *before* the `DELETING` CAS and before the
    PUT, or a crash in either window leaves a `DELETING` generation or a physical
    object that no projection references and no scan is permitted to find. Neither
    loses data, but both break the recovery guarantee — and the first contradicts
    "`DELETING` is recoverable from the generation record", which silently assumed a
    scan. No extra Paxos is needed; the existing queue row can carry the role.
85. **One generation is one Cassandra partition.** `block_generations` is keyed
    `((org_id, block_id, generation_id))`, not clustered under `(org_id, block_id)`.
    With logical history retained indefinitely, the clustered form lets a hot SHA's
    generation history grow into a single unbounded partition. Nothing needs the
    co-location — the lineage walk always knows the exact generation ID — and the
    split also stops old and new lifecycles of the same SHA contending on one
    partition's Paxos state.
86. **`VERIFIED` lag is not a contradiction.** `materialization_state=VERIFIED` is a
    `LOCAL_QUORUM` write while the activation CAS commits at `EACH_QUORUM`, so
    another DC can legitimately see `pointer = G2 / ACTIVE` with a locally stale
    `MATERIALIZING`. Quarantining on that turns routine regional propagation into a
    permanent operator-only state on a healthy generation. A writer fails closed and
    retries; only a recovery worker that re-verifies the exact object and finds an
    actual contradiction may quarantine.
87. **Generation collection does not delete `block_id_mappings`.** They are logical
    identity and survive with the retained `blocks` pointer. An earlier revision
    required "successor-aware" cleanup; not deleting them at all is simpler and
    removes the class of bug instead of specifying how to avoid it. Their removal
    belongs to the out-of-scope `PURGING_LOGICAL` work.
88. **Discovery precedes the irreversible step in every sequence, not just in the
    section that states the rule.** When Discoverability Before Irreversibility was
    added, three normative sequences still showed the opposite order — the protocol
    sketch, the GC protocol, and the materialization steps — and PR-3 still called
    the intent and the use one write. A principle stated in one section and
    contradicted in three is worse than no principle, because two implementers can
    each cite the document. Whenever an ordering rule is introduced, every sequence
    that expresses that ordering is part of the change.
89. **Retirement evidence is bound by claim ID *and* claim epoch.** The epoch is the
    partition key that finds the row (see correction 107); the claim ID is immutable
    payload that proves
    which worker earned it. Validating on epoch alone would let a worker that
    somehow acquired a reused epoch consume another worker's proof. An epoch match
    with a claim-ID mismatch is a protocol violation, not a near miss. This is a
    separate defence from correction 81's monotonic acquisition CAS, and neither
    substitutes for the other.
90. **Initializing `retire_claim_epoch = 0` is not about missing rows.** An earlier
    revision justified it by correction 71, which does not apply: the acquisition's
    `IF` clause also matches `active_generation_id`, `active_state`, and
    `active_epoch`, and none of those holds against an absent partition. The real
    reasons are that the first acquisition needs no special case, `Nprev + 1` works
    from the first cycle, the counter stays total rather than sparse, and `null`
    never means both "not initialized" and "no retirement yet".
91. **The serial settlement read is driver-specific and must be written the right
    way.** Under `apache/cassandra-gocql-driver/v2`, `Serial` is a first-class
    `Consistency` value and `SerialConsistency` is a deprecated alias, so
    `.Consistency(gocql.Serial)` on a `SELECT` is the correct form. Under the older
    `gocql/gocql` v1 it was a separate type usable only on a conditional statement's
    serial phase, which is why this looks unavailable to anyone reasoning from that
    driver. Calling `.SerialConsistency()` on a plain `SELECT` is a silent no-op and
    proves nothing. **Amended by correction 98**: the harmless settlement LWT is a
    *rejected alternative*, not a supported fallback — the serial `SELECT` is the
    single normative mechanism and the `paxos_variant` gate is unconditional.
92. **The recovery projections are schema, not prose.** Every other table in this
    design has its primary key frozen here; leaving the recovery branches as named
    concepts is what pushes an implementer back toward the enumeration the protocol
    forbids. **Refined by corrections 104 and 109**: three projections carry the base
    key `((due_day, bucket), due_at, org_id, block_id, generation_id)` and
    `gc_generation_handoff_by_day` adds `retire_claim_epoch`; the fifth,
    `gc_generation_uses_by_day`, was later removed as redundant. Also
    `gc_generation_deletes_by_day` doubles as the discovery record required before
    the `DELETING` CAS — deliberately the same table, because a second row that must
    be written in the same window would only add another crash gap. The reverse
    reference projection partitions by `(org_id, referrer)`.
93. **The claim deadline binds only GC-owned reversible pointer transitions.** The
    rule was stated for "every transition after claim acquisition", which reads as
    covering the activation CAS — a statement issued by a materializer that holds no
    claim, and one Cassandra could not express anyway since it cannot compare
    against "now". Activation is safe for a different reason: `RETIRED` is terminal
    for G1, and a takeover would have bumped the epoch and failed the CAS on its own.
    Stating the rule broadly sent an implementer looking for a check that cannot
    exist.
94. **Irrevocability is proven against "the next successor", never against "G2".**
    The authorization paragraph kept saying the pointer can only move to G2 after the
    lineage table had already been generalized. The stall-after-proof argument holds
    at any depth; hard-coding the immediate successor reintroduces the
    two-rematerialization stall that correction 73 exists to prevent.
95. **The gates bind from the first generation-aware write, all of them.** `SERIAL`
    was scoped that way in an earlier revision, but the `paxos_variant` assertion was
    left tied to `gc.enabled`. A materializer can crash between its PUT and its
    activation CAS on day one of the writer rollout, and the recovery that follows is
    exactly the path that needs the serial round — so the assertion would have been
    unenforced during the window in which the first crashed activations occur.
96. **The accepted-but-not-learned hazard is not specific to rematerialization.** The
    first-writer `INSERT ... IF NOT EXISTS` is a Paxos operation on the same `blocks`
    partition in the same `SERIAL` domain, so a first materialization can PUT `K1`,
    have its proposal accepted, lose its coordinator, and then be read by recovery as
    "no row, never activated" — after which orphaning `K1` loses live data the moment
    a later LWT replays the proposal. This is reachable on the first upload of new
    content, before any rematerialization exists. The settlement rule is stated over
    *materializations*, not over successors, and a released stub is not proof that no
    activation was proposed.
97. **`RETIRING -> ACTIVE` writes its delayed candidate before the CAS, not after.**
    The original order was justified by a recovery scan over `ACTIVE` generations,
    and that scan no longer exists. A crash in the window would leave a generation
    that is `ACTIVE` with zero references and no projection row — invisible to every
    automated path. A stale candidate costs one wasted revalidation; an invisible
    `ACTIVE` generation costs the guarantee. This is Discoverability Before
    Irreversibility applied to a transition that is state-changing without being
    destructive.
98. **One settlement mechanism, one gate.** An earlier revision made the serial
    `SELECT` and a harmless settlement LWT both normative while keeping the
    `paxos_variant` gate unconditional — two policies that cannot both hold, and the
    LWT form additionally used a column Proposed Schema never defined. The serial
    `SELECT` is the contract; the LWT is recorded as a rejected alternative with its
    costs, so it is not rediscovered as new. A conditional gate is a gate that is
    wrong in one branch as soon as the branches drift.
99. **X1/X2 never deletes logical history — no exceptions, no horizons.** The
    retention decision was recorded in the cost section while three other places
    still described conditions under which evidence, predecessor tuples, or the
    triple as a whole "may age out" or be "bounded by a horizon". Any of those is an
    implementation licence for exactly the purge race declared out of scope.
    `blocks` rows, generation rows, predecessor lineage, retirement evidence, and
    `block_id_mappings` are retained unconditionally; releasing them is
    `PURGING_LOGICAL` work with its own fence.

100. **Durable GC work is handed over, never dropped and re-acquired.**
    Discoverability Before Irreversibility made each step discoverable but said
    nothing about the seam between one work record retiring and the next publishing.
    With scans forbidden, a crash in that seam loses the generation as thoroughly as
    never publishing at all: a worker that finishes `RETIRING -> RETIRED` and retires
    its candidate before writing the delete work leaves `blocks` at `G1 / RETIRED`
    with valid evidence, `K1` present, and nothing naming G1. Retiring the candidate
    at `ACTIVE -> RETIRING` is worse — the block stays `RETIRING` forever, an
    ownerless writer fence. The rule is that the outbound work row is durable before
    the inbound one is retired, and `gc_generation_handoff_by_day` is published
    before the pointer CAS so the `RETIRED` seam is covered.
101. **Projection publication is per family, never "same logged batch as the
    canonical row".** That universal phrasing contradicted the deliberately
    sequential 3a/3b/3c protocol, and for retirement evidence — a conditional insert
    whose projection lives in another partition — it demanded a conditional
    cross-partition batch Cassandra cannot express. The invariant is one-directional:
    a projection may transiently outlive its branch, never lag behind it. Logged
    batches are an implementation convenience, not part of the safety proof.
102. **Released-stub repair establishes a first active pointer and must initialize
    the claim counter.** It is the second path that can create one, and it was left
    out when the first-writer LWT was given `active_epoch = 1`,
    `retire_claim_epoch = 0`, and null claim ID and deadline. Without it a repaired
    stub yields a pointer whose `retire_claim_epoch` is null, and the first
    `ACTIVE -> RETIRING` — conditioned on `retire_claim_epoch = Nprev` — needs a null
    special case, reintroducing exactly the exception correction 90 removed.
103. **Which claim the evidence must match depends on where the pointer is.** When
    the pointer still reads `G1 / RETIRED` the live pointer claim *is* `C1 / N1`, so
    "match the live claim" is right. Once a successor owns the pointer the live claim
    belongs to a later cycle or is null, and the evidence must match the claim
    recorded in the lineage link instead. Reading "live claim" literally in the
    successor case fails every legitimate delete of a superseded generation — the
    transitive-lineage rule and the evidence rule have to be read together.
104. **Recovery projection keys must distinguish every event they schedule.** A
    generation can travel `RETIRING -> ACTIVE -> RETIRING` repeatedly, so
    `gc_generation_handoff_by_day` needs `retire_claim_epoch` in the clustering key.
    Without it two retire cycles sharing a `due_at` collide on one row and one is
    silently dropped — a lost work item in a design that just removed the scan that
    used to catch it. **Amended by correction 109**: the same argument originally
    covered `use_id` on a materializer-use projection that has since been removed.
105. **The startup assertions form two gates, not one.** Topology, DC/RF, `SERIAL`
    domain, and `paxos_variant` bind from the first generation-aware write;
    destructive GC requires that gate plus the full acceptance criteria. Tying the
    first four to `gc.enabled=true` leaves them unenforced for the five rollout steps
    between deploying generation-aware writers and switching GC on — precisely the
    window in which the first crashed activations occur.

106. **Work continuity applies to the materializer, not only to GC.** The handoff rule
    was frozen for the GC cycle while the materialization side kept three
    incompatible models: `MATERIALIZING` still called the intent "also a
    generation-use row", the ambiguity contract covered 3a and 3b but not 3c, and no
    text said who publishes the materialization-side projections or when ownership
    passes between them. The seam that leaks is a materializer that wins activation
    and then dies: an intent scanner that retires its row on "no longer
    `MATERIALIZING`" leaves an `ACTIVE` generation with zero references and no work
    row. It must observe a reference or publish `gc_generation_zero_ref_by_day`
    first. Step 3c also needs its own ambiguity contract — unconfirmed means no PUT.
107. **One evidence event, one partition.** `block_generation_retire_evidence` keyed
    `((org, block, generation), retire_claim_epoch)` accumulates every retire cycle
    of a generation in one partition, and this design retains evidence forever while
    allowing `RETIRING -> ACTIVE -> RETIRING` without limit. Every consumer looks up
    an exact `(G, N)`, never a range, so the epoch belongs in the partition key. Uses
    and references are the deliberate contrast: the destructive proof reads each of
    those as one complete partition, so they stay clustered under the generation.
108. **Every normative pseudocode carries the ordering it depends on.** The
    `RETIRING -> ACTIVE` escape was corrected in its own section while the main GC
    protocol still showed the bare CAS, and two verification cases still described
    repairing a lost enqueue — which the contract now makes impossible, since an
    unconfirmed enqueue means no CAS. This is correction 88 recurring; the practical
    remedy is to grep for the old formulation before committing a new rule.
109. **Retirement evidence is never reconstructed after the fact.** `RETIRING` with
    no evidence and `RETIRED` with no evidence are opposite situations, and an
    earlier revision gave both the same remedy — "step 2 was lost, re-run the zero
    check and append evidence". For a `RETIRED` pointer that is a safety hole: the
    protocol confirms evidence *before* the pointer CAS and never deletes or expires
    it, so its absence means the ordering was violated. Re-running the check now and
    appending would manufacture a row indistinguishable from a legitimate one, which
    would then authorize `DELETING` — laundering a protocol violation into proof of
    something that never happened. `RETIRED` with no evidence at the authorizing
    claim is quarantine, full stop. The same revision also removed the redundant
    materializer-use projection, which had become a fourth write outside the 3a/3b/3c
    sequence with no ambiguity contract of its own.
110. **A blocked step is not the same as a lost step.** Recovery exists for stale and
    inconsistent work rows, not for outcomes the contract makes unreachable. After
    correction 97 an unconfirmed enqueue forbids the reactivation CAS, so a committed
    reactivation always has its discovery row; describing recovery as the backstop
    for "generations whose enqueue itself was lost" reopens by narration the window
    the ordering closed. Where a rule makes a crash state impossible, the prose must
    stop offering to repair it.
## Related Documents

- [Known issues](./KNOWN_ISSUES.md), X1 and X2 remain open.
- [Open work index](./OPEN-WORK-INDEX.md), production activation gate.
- [Upload-fence findings registry](./UPLOAD-FENCE-FINDINGS-REGISTRY.md), audit evidence.
- [Historical upload-fence PR plan](./GC-UPLOAD-FENCE-PR-PLAN.md), PR-1 through PR-10 history.
- [Architecture](./ARCHITECTURE.md), current GC behavior and known limitations.
- [Database guide](./DATABASE-GUIDE.md), current schema and consistency inventory.
- [Multi-region testing](./MULTIREGION-TESTING.md), existing regional test guidance.
- [Session checklist](./SESSION_CHECKLIST.md), required documentation verification.
