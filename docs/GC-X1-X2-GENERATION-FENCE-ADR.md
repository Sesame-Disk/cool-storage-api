# ADR: X1/X2 Generational GC Fence

**Status:** Proposed design; implementation not started

**Date:** 2026-08-07 · **Last updated:** 2026-08-10

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
   and the rematerialization CAS using `EACH_QUORUM`.
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

GC: candidate -> RETIRING -> drain uses -> global refs check
    -> ACTIVE or RETIRED -> DELETING -> persist recovery -> DELETE exact K
```

The following statements are part of the contract:

- A pin is not a permanent liveness reference.
- Finding a pin with live authority during `RETIRING` does not justify
  `RETIRING -> ACTIVE`; it justifies waiting. A generation whose only remaining uses
  have all expired their authority does return to `ACTIVE`, to release the writer
  fence rather than to assert liveness.
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
- Every generation that can be used by an in-flight operation has a corresponding
  use row. There is no writer, including the materializer, that is invisible to the
  GC drain.
- `DELETING` is recoverable from the generation record even if the orphan projection was not written.
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
- Each configured DC has the expected replication factor.
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
- Rematerialization of a `RETIRED` logical block performs one activation CAS
  attempt per materializing request. It may run inline in the request that
  materialized the new generation.

The Cassandra 5.0.6 source used to resolve the read-level question is:

`https://raw.githubusercontent.com/apache/cassandra/cassandra-5.0.6/src/java/org/apache/cassandra/db/ConsistencyLevel.java`

Older Cassandra 3.0 documentation says `EACH_QUORUM` is not supported for reads.
That documentation is not the target contract for this repository; the engine
version must still be asserted by integration tests.

## Consistency Contract

| Operation | Required consistency |
|---|---|
| Initial writer generation read | `LOCAL_QUORUM` |
| `MATERIALIZING` intent/use insert | `LOCAL_QUORUM` |
| Materializer use release | `LOCAL_QUORUM` |
| Pin insert | `LOCAL_QUORUM` |
| Pin confirmation | `LOCAL_QUORUM` |
| Generation revalidation | `LOCAL_QUORUM` |
| Reuse, dedup, and normal metadata reads | `LOCAL_QUORUM` |
| Provisional/permanent reference insert/delete | `LOCAL_QUORUM` |
| GC discovery and candidate reads | `LOCAL_QUORUM` |
| Existing terminal first-writer metadata LWT (initial pointer only) | Unchanged from today: `SERIAL` phase with the existing writer commit level. The fence adds no second LWT here |
| Rematerialization `G1 RETIRED -> G2 ACTIVE` CAS | LWT serial phase `SERIAL`, regular commit `EACH_QUORUM` on `blocks`; may run inline in the materializing request |
| `ACTIVE -> RETIRING` (active pointer) | LWT serial phase `SERIAL`, regular commit `EACH_QUORUM` on `blocks` |
| Use-row drain, including `PENDING`, `AUTHORIZED`, and materializer uses | `EACH_QUORUM` |
| Final generation-reference check | `EACH_QUORUM` |
| `RETIRING -> ACTIVE` (active pointer) | LWT serial phase `SERIAL`, regular commit `EACH_QUORUM` on `blocks` |
| Retirement evidence append (one row per `(generation, claim_epoch)`) | `EACH_QUORUM`; append-only, never overwritten, ordered before the pointer CAS |
| `RETIRING -> RETIRED` (active pointer) | LWT serial phase `SERIAL`, regular commit `EACH_QUORUM` on `blocks` |
| `RETIRED -> DELETING` (generation lifecycle) | LWT serial phase `SERIAL`, regular commit `EACH_QUORUM` on the G1 generation row |
| `DELETING -> DELETED` (generation lifecycle) | LWT serial phase `SERIAL`, regular commit `EACH_QUORUM` on the G1 generation row |

The session default remains `LOCAL_QUORUM`. New critical background LWT queries
must explicitly set both levels rather than relying only on the driver default:

```go
query.Consistency(gocql.EachQuorum).
    SerialConsistency(gocql.Serial)
```

The current `internal/db/db.go:94-95` does explicitly assign
`cluster.SerialConsistency`. The current configuration and tests still include
`LOCAL_SERIAL` in the multi-region profiles, so the final X1/X2 test profile must
use `SERIAL`. The global session configuration must not be changed to
`EACH_QUORUM`.

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
- **Rematerialization** performs one activation CAS attempt when a `RETIRED` logical
  block comes back to life. It may run inline in the materializing request.

Retirement and delete LWTs belong to the GC worker and never to a request handler.

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
activation CAS attempt a materializing request makes when it rematerializes a
retired block.

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
The implementation must:

- verify the keyspace replication strategy at startup, and refuse to boot when
  `gc.enabled=true` and the strategy is not `NetworkTopologyStrategy`;
- treat that check as part of the destructive-GC activation gate, not as an
  operational note.

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
                              |
                              |   evaluated in this order:
                              |
                              +-- error/DC unavailable      -> RETIRING, retry
                              |
                              +-- refs > 0                  -> ACTIVE
                              |
                              +-- refs = 0, a use holds live
                              |   authority                 -> RETIRING, drain
                              |
                              +-- refs = 0, all remaining
                              |   uses expired authority    -> ACTIVE
                              |
                              +-- refs = 0 and uses = 0     -> RETIRED
                                                                  |
                                                                  v
                                                               DELETING
                                                                  |
                                                                  v
                                                               DELETED
```

A reference is proof of liveness; a use is only proof of pending work. That is why
references are evaluated first.

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

The materialization intent is also a generation-use row. Creating it writes a use
row for `(G, use_id)` in the `AUTHORIZED` state, because the materializer owns `G`
by construction: no other operation can be using a generation that does not exist
yet. That use row is what makes the materializer visible to the GC drain, and it is
held across the PUT, the activation CAS, and the reference publication.

Without it there is a live-data hole. A materializer that wins activation creates a
generation that is `ACTIVE` with zero references, which is exactly the shape of a GC
candidate. If the materializer had no use row, the GC could retire, drain nothing,
find no references, and delete `K` while a successful upload was still preparing to
publish its reference.

### `ACTIVE`

`ACTIVE` means the authoritative active pointer selects `G`, and the exact object
`K` has been PUT and verified. Readers may resolve the pointer to `K`.

### `RETIRING`

`RETIRING` is a temporary GC fence. New writers cannot use G1 and cannot create G2.
An existing writer whose pin was already authorized for G1 may finish and publish
while G1 is `RETIRING`; the GC sees the pin and waits. A writer with a `PENDING`
pin cannot publish until it revalidates while G1 is `ACTIVE`.

A generation-bound `up:`, `pub:`, or `fs:` row found by the final global check
returns G1 to `ACTIVE`. This is safe even when a provisional row is stale: the
result is temporary retention, not deletion. The owner/expiry path can later
remove the row and create a fresh candidate.

`RETIRING` is a fence on writers, so it must not become a parking state. It ends on
any of three outcomes: a reference is found, every remaining use has expired its
authority, or the drain reaches zero. Never on "some row still exists". See
Escaping `RETIRING` On Abandoned Uses.

### Retirement Evidence

Retirement evidence is not a state. It is an append-only row meaning "a global zero
check for this generation passed under this claim epoch", written before the pointer
CAS and never overwritten. It is not visible on the active pointer and never
authorizes deletion by itself; the authoritative pointer must also have left
`RETIRING`. See Retirement Handoff.

### `RETIRED`

`RETIRED` means the GC has globally confirmed, at `EACH_QUORUM`, zero
generation-bound references and zero use rows of any kind, including writer pins in
both `PENDING` and `AUTHORIZED` state and materializer uses. G2 may now be
materialized. G1 remains addressable by its own identity until its delete is
complete.

The guarantee is about authority, not about row absence. The protocol cannot
prevent a use row from appearing after the final read: a writer may have observed
`ACTIVE` before the fence and insert its `PENDING` row afterwards. That is safe and
does not invalidate `RETIRED`, because the writer's revalidation is ordered after
its own insert and will observe `RETIRING` or later, so the row can never be
promoted to `AUTHORIZED`.

The correct statement is therefore:

```text
RETIRED means no use row observed at the final global read could ever
acquire authority, and no reference existed.
```

An implementation must not read `RETIRED` as "no use row can exist from now on" and
must not build a drain loop that waits for that property. It is unachievable
without an admission LWT, which this design deliberately does not add.

The same distinction, applied to publication rather than to rows, is the
publication frontier: the pair of global reads proves that no operation can still
acquire the authority needed to publish, which is why no further reference check is
required between the retirement persist and the pointer CAS.

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

That is a testable condition, not a convention, so the GC-owned pointer transitions
carry it in the `IF` clause rather than in a preceding read:

```text
ACTIVE -> RETIRING
    IF active_generation_id = G1
    AND active_state       = ACTIVE
    AND active_epoch       = E1
    -- and installs retire_claim_id / retire_claim_epoch / retire_claim_deadline

RETIRING -> ACTIVE                RETIRING -> RETIRED
    IF active_generation_id = G1      IF active_generation_id = G1
    AND active_state    = RETIRING    AND active_state       = RETIRING
    AND active_epoch    = E1          AND active_epoch       = E1
    AND retire_claim_id    = C        AND retire_claim_id    = C
    AND retire_claim_epoch = N        AND retire_claim_epoch = N
```

A worker whose claim was taken over fails these conditions and cannot complete a
transition it no longer owns. The deadline itself is not in the `IF` clause —
Cassandra cannot compare against "now" — so it is enforced by the takeover LWT that
bumps `retire_claim_epoch`, which is what invalidates the stale worker's condition.

The activation CAS deliberately does **not** carry the claim, because it is executed
by a materializing request that holds no GC claim. Its safety comes from
`active_state = RETIRED`, which only a claim-holding worker could have installed.

The same ownership rule applies to `DELETING`. The physical delete must be tied to
the generation and claim that authorized it, not merely to a logical block ID.
The claim epoch/lease can prevent stale Cassandra transitions; it cannot revoke an
S3 `DELETE K` request that has already left the process. That is intentional and is
safe only because K is never reused: a late delete of K1 cannot affect K2.

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
4. Re-read the active pointer                     LOCAL_QUORUM
5. Authorize only if G/epoch is still ACTIVE
   AND this pin's own authority_deadline
   has not passed
6. Re-check the authority deadline, then
   reuse K or PUT/repair K
7. Validate AUTHORIZED pin, deadline, and epoch
8. Publish the generation-bound reference         LOCAL_QUORUM
9. Confirm an ambiguous reference result
10. Remove pin(G, use_id)                          LOCAL_QUORUM
```

The pin becomes `AUTHORIZED` only after step 5. A writer that observes
`RETIRING`, `RETIRED`, or a different generation at revalidation has no authority
and must not perform a physical operation. (`DELETING` and `DELETED` are not
pointer states; see Which States Live Where.)

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
3. Persist MATERIALIZING intent + AUTHORIZED use     LOCAL_QUORUM
4. PUT K
5. Verify K exists and has the expected metadata
6. Activate: first life via the existing first-writer LWT,
   rematerialization via the activation CAS below
7. If activation wins, mark/complete ACTIVE
8. If another writer won, do not publish a reference
9. Publish the generation-bound reference            LOCAL_QUORUM
10. Release the materializer use                     LOCAL_QUORUM
11. Preserve losing K as an orphan for exact cleanup
```

The use row from step 3 is held until step 10. It survives activation: the window
between winning at step 6 and publishing at step 9 is precisely when the generation
is `ACTIVE` with zero references, so the GC must be able to see that the generation
is in use. A materializer that loses at step 8 releases its use and lets exact
orphan cleanup remove `K`.

For the first logical life, the existing terminal `INSERT ... IF NOT EXISTS` sets
the initial active pointer in that same operation, so no additional activation
Paxos is introduced. For rematerialization, the logical row already exists and
activation is an update conditional on the old generation:

```text
UPDATE blocks
SET active_generation_id = G2,
    active_storage_key = K2,
    active_storage_class = C2,
    active_state = ACTIVE,
    active_epoch = E2
WHERE org_id = O
AND block_id = L
IF active_generation_id = G1
AND active_state = RETIRED
AND active_epoch = E1
```

This CAS may run inline in the request that materialized G2. Each materializing
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

A retry that reuses the same generation and epoch is idempotent — the condition
`IF active_generation_id = G1 AND active_state = RETIRED AND active_epoch = E1` can
only be satisfied once — so retrying cannot produce a second generation. Concurrent
rematerializers of the same retired block each run one logical operation, so the
cost is bounded by concurrent writers of that one block, not by request volume. It
is not a new LWT per reference, reuse, or dedup hit.

Inline activation is preferred over deferring to a background worker. Deferring
would leave a request that has already completed a correct PUT unable to finish:
it would return a retryable error and wait for a periodic worker, and a client
retry in the meantime would materialize further losing generations and objects.
Deferring does not remove the WAN dependency either — during a DC outage the upload
cannot complete under either design, because it cannot publish a reference to a
generation that is not active. It only moves where the failure surfaces and adds a
worker interval to it.

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
crashed between a verified PUT and activation. That is recovery work, not the
normal path.

### Ambiguous Activation Outcome

A timeout or driver-level error on the activation CAS does not mean the CAS was
not applied. The request must not assume it won or lost. It retains `G2`, `K2`, and
its materializer use, then reconciles against the authoritative `blocks` row:

| Observed authoritative state | Action |
|---|---|
| `active_generation_id = G2` and `active_epoch = E2` | The CAS applied; publish the reference and release the use |
| A different generation is active | Lost the race; make `K2` an exact orphan and release the use |
| Still `G1` / `RETIRED` / `E1` | Not applied; the same attempt may be retried idempotently |
| Read uncertain or a DC is unavailable | Retain `G2`, `K2`, and the use; retry later. Never orphan and never delete on an uncertain read |

This generalizes to every destructive or lifecycle LWT in this design: an ambiguous
result is reconciled against authoritative state, never guessed. Treating a timeout
as "not applied" is the failure mode that produces both double activation and
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

**Ambiguous materializer use.** The materializer's intent and its `AUTHORIZED` use
are persisted together before the PUT, in step 3 of First Materialization Or
Rematerialization — not the step 3 of the writer sequence. If that write is ambiguous the
materializer has no authority and must confirm the use with `LOCAL_QUORUM` before
proceeding. It must not PUT, and must not activate, on the assumption that the use
landed. A materializer that PUTs and activates without a confirmed use recreates
the exact hole this design closes: a generation that is `ACTIVE` with no reference
and no use is indistinguishable from garbage, and the GC will delete `K`.

In every case: abort if confirmation still fails, and never perform an S3 operation
based on an assumption that a use row exists.

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
- the pointer says the pinned generation is `RETIRED`, or the pinned generation's
  own row says `DELETING` or `DELETED` — the last two are read from
  `block_generations`, not from `active_state` (see Which States Live Where);
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
  when every remaining use has expired its authority, so the wait is bounded by the
  authority deadline rather than by `retention_expires_at`.

`RETIRED` does not stall: the writer proceeds immediately to materialize and
activate a new generation rather than waiting for a state transition.

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
  the materializer's own generation, rather than treating the stub as a winner;
- keep the stub-repair contention signal retryable, since a contended repair is a
  race with another materializer, not a fenced generation;
- assert, as a schema invariant, that no row may carry `active_state` without
  `active_generation_id` and `active_storage_key`.

PR-1 must decide whether generation-aware claims can avoid creating stubs at all —
a conditional update whose `IF` clause cannot be satisfied by a null row is
preferable to repairing the damage afterwards — and PR-3 must not assume the
question was settled.

`block_generations` stores immutable physical identity, historical lifecycle,
claim/recovery data, and mirror state. It is not a second authority for deciding
which generation is active. `block_generations.state=MATERIALIZING` is never
sufficient to authorize deletion. Recovery always consults the `blocks` pointer
first and repairs the mirror if the pointer already selects G.

### Which States Live Where

The document uses one vocabulary for two different state machines, and an
implementation that conflates them will look for values where they cannot appear.
The split is:

| State | Recorded on | Observable by a writer reading the pointer |
|---|---|---|
| `ACTIVE` | `blocks.active_state` | Yes |
| `RETIRING` | `blocks.active_state` | Yes |
| `RETIRED` | `blocks.active_state` and the generation row | Yes |
| `MATERIALIZING` | generation row only | **No** |
| `QUARANTINED` | generation row only | No |
| `DELETING`, `DELETED` | generation row only | **No** |

The pointer never holds `MATERIALIZING`. A first life creates the row already
`ACTIVE` through the first-writer LWT; a rematerialization moves it directly from
`RETIRED` (G1) to `ACTIVE` (G2). Two concurrent first materializers of the same new
content therefore both PUT and both attempt the LWT, one loses and orphans its key.
That is the current behaviour and it is safe; there is no pointer state a writer
could poll to avoid it.

Likewise a writer never observes `DELETING` or `DELETED` on the pointer. Those are
terminal generation states, reached after the pointer has already stopped selecting
G1. A writer arriving then reads `RETIRED` (or a different active generation) and
takes the rematerialize branch, which is correct: the new generation gets a new UUID
and a new key, so whatever the GC is doing to `K1` is irrelevant to it.

Where the tables below list writer behaviour or publish-helper rejections for
`MATERIALIZING`, `DELETING`, or `DELETED`, the check is against the **generation
row** reached through the writer's own pinned generation ID, not against
`active_state`. For the common case the pointer check "the active generation no
longer matches my authorized pin" already covers it.

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

### Retirement Handoff

The `blocks` row is the linearization point only while G is the active generation.
Once G2 overwrites `active_generation_id`, that row no longer records anything
about G1 — including the fact that G1 ever reached `RETIRED`. A delete of `K1` is
authorized by that fact, so the evidence must outlive the pointer.

The required order is therefore:

```text
1. global zero check for G1                       EACH_QUORUM
   (uses first, then refs — see Read Order Is Not Decision Order)
2. append retirement evidence for (G1, claim_epoch)
   in the append-only evidence table              EACH_QUORUM
3. CAS blocks RETIRING -> RETIRED                 SERIAL + EACH_QUORUM
   <-- G2 may activate from this point on
4. finalize the mirror: generation G1 -> RETIRED   EACH_QUORUM, best effort
```

**Step 3 is the gate, not step 4.** The G2 activation CAS conditions on
`blocks` alone, so the instant the pointer reads `G1 / RETIRED / E1` a
rematerializing request can win it. Nothing can hold G2 back until a later write
to a different table lands, and an implementer who tries to enforce that will reach
for a cross-table condition Cassandra cannot express — which this ADR forbids
elsewhere as an acceptance criterion.

Step 4 is therefore a best-effort mirror finalize, not a precondition. The durable
proof of retirement is:

> a matching-epoch evidence row for G1, **plus** the authoritative pointer having
> left `RETIRING` — either because it reads `G1 / RETIRED`, or because it has
> already been replaced by G2, which could only have happened through a CAS that
> required `G1 / RETIRED`.

The reconciliation table below already encodes exactly this, including the row for
`pointer = G2` with the mirror not yet finalized. The step list previously
contradicted it by asserting an ordering the system cannot impose.

Without step 2 before step 3 there is a losing interleaving: the global check
passes, `blocks` says `G1 RETIRED`, the worker crashes, G2 activates and
overwrites the pointer, and `block_generations(G1)` still says `RETIRING`. The
only durable proof that G1 was legitimately retired is gone, and no worker can
safely authorize deleting `K1` afterwards.

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
    retire_claim_epoch      -- clustering key; one row per retire cycle
    retire_claim_id
    checked_at
    uses_read_at
    refs_read_at
    PRIMARY KEY ((org_id, block_id, generation_id), retire_claim_epoch)
```

Each worker writes its own row and can never overwrite another's. No CAS is needed,
so the cold path gains a durable ordering guarantee without a third global LWT.
Consumers read the row whose `retire_claim_epoch` equals the live claim on the
`blocks` row; any other row is history.

This also removes the need for any intermediate "retire prepared" generation state.
The generation row keeps `RETIRING` until the pointer crosses, then finalizes to
`RETIRED`. The two tables never simultaneously assert different things, because
only one of them makes a claim about retirement at all.

This needs no cross-table transaction. It needs an ordering rule, an epoch rule,
and a reconciliation rule:

| `blocks` view of G1 | Evidence row for the live claim epoch | Recovery action |
|---|---|---|
| Pointer selects G1, state `RETIRING` | absent | Normal in-progress retirement; continue the drain from step 1 |
| Pointer selects G1, state `RETIRING` | present | Crash between steps 2 and 3. **Never `DELETING`, never G2.** Revalidate the claim, re-run the global zero check, then complete step 3 — or return to `ACTIVE` if a reference has appeared |
| Pointer selects G1, state `RETIRING` | only rows for older epochs | History from earlier retire cycles; ignore them and continue the drain from step 1 |
| Pointer selects G1, state `RETIRED` | present | Consistent; finalize the mirror if needed, then proceed to `DELETING` |
| Pointer selects G1, state `RETIRED` | absent | Step 2 was lost; re-run the global zero check and append evidence before proceeding |
| Pointer selects G2 | present for the epoch that retired G1 | The zero check passed and G2 could only activate through a CAS requiring `G1 / RETIRED`; finalize the mirror and proceed to `DELETING` |
| Pointer selects G2 | no evidence row for any epoch | Fail closed. Never delete `K1`; the retirement is unprovable and G1 must be quarantined |
| Either read uncertain | any | Preserve and retry |

Note that the `pointer selects G2` rows do not require the mirror finalize to have
happened. That is the point of moving the gate to step 3: G2 legitimately activates
in that window, and the evidence row plus the pointer having moved is the complete
proof.

Three invariants govern the table, and all must be asserted in code rather than
inferred from it:

> `block_generations(G1).state` alone never authorizes `RETIRED -> DELETING`. The
> worker must additionally hold an evidence row for the live claim epoch, and the
> authoritative pointer must either read `G1 / RETIRED` or already select G2.

> Retirement evidence is valid only under the claim epoch that produced it. A
> worker that cannot match the epoch treats the evidence as absent.

> No retirement evidence is ever overwritten or deleted by a worker. Rows age out
> by TTL only, well after the generation itself is gone.

The fail-closed row is the point of the rule: a generation whose retirement cannot
be proven is retained forever rather than deleted on inference.

Crash rules:

| Situation | Recovery action |
|---|---|
| Intent before PUT | Expire intent after its deadline; no object exists to delete |
| Use write ambiguous before PUT | No authority; confirm the use before any S3 operation |
| PUT before activation CAS | Consult active pointer; if not selected, clean exact K as orphan only after confirming no references and no live uses |
| Activation CAS ambiguous | Reconcile per Ambiguous Activation Outcome; never orphan or delete on an uncertain read |
| Active pointer selects G but generation mirror says MATERIALIZING | Complete/repair `ACTIVE`; never delete K |
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
     refs == 0 and some use still holds unexpired authority
         -> keep RETIRING, drain
     refs == 0 and every remaining use has expired authority
         -> RETIRING -> ACTIVE                     SERIAL + EACH_QUORUM
     refs == 0 and uses == 0
         -> G1 -> RETIRED                          SERIAL + EACH_QUORUM
6. Allow G2 only after G1 is RETIRED
7. G1 RETIRED -> DELETING                        SERIAL + EACH_QUORUM
8. Persist/reconstruct recovery for G1 + K
9. DELETE exact K
10. Mark G1 DELETED and clean only G1 metadata
```

The decision order matters. References are evaluated before uses, because a
reference is proof of liveness while a use is only proof of pending work. Testing
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
| At least one use whose `authority_deadline` has not passed | Keep `RETIRING` and drain; that writer may still legitimately publish |
| Every remaining use has an expired `authority_deadline` but unexpired `retention_expires_at` | `RETIRING -> ACTIVE` |

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

Delegating this to the Recovery Protocol's `ACTIVE` with no reference and no live
use branch would be wrong on Cassandra: that branch is a scan over
`block_generations`, which is a crash-recovery sweep, not a scheduler. Using it for
ordinary scheduling turns a routine outcome into a full-table walk whose cost grows
with total block count rather than with GC work. The scan stays as the safety net
for generations whose enqueue itself was lost.

## Recovery Protocol

`gc_s3_orphans` is a retry/discovery projection, not the only source of truth.

Recovery must scan `block_generations` for:

- expired `MATERIALIZING` intents;
- generations whose materializer use expired without a published reference;
- generations that are `ACTIVE` with no reference and no live use. This covers both
  a materializer that died after activation and a generation reactivated out of
  `RETIRING` on expired-authority uses; neither may be assumed garbage, and this is
  the only path that re-examines a generation whose candidate will never be
  re-created by a reference-removal event;
- generations holding an evidence row for the live claim epoch while the pointer
  still says `RETIRING` — the crash between handoff steps 2 and 3;
- `RETIRED` in `blocks` with no evidence row for the live claim epoch, per the
  retirement handoff reconciliation table;
- generations whose mirror finalize (handoff step 4) never ran, which is expected
  whenever G2 activated promptly and is not an error;
- `DELETING` generations with no orphan row;
- `DELETING` generations with an orphan row in `pending_s3`;
- generations whose S3 delete completed but metadata cleanup did not.

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

### Generation History

```text
block_generations
    org_id
    block_id
    generation_id
    state                 -- mirror/history; not the active-pointer authority
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
    quarantined_at
    quarantine_reason
    created_at
    updated_at
```

`state` here ranges over `MATERIALIZING`, `ACTIVE`, `RETIRING`, `RETIRED`,
`DELETING`, `DELETED`, `QUARANTINED`. Retirement evidence does **not** live on this
row; see Retirement Evidence Is Append-Only And Epoch-Keyed.

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
generation stuck in an ordinary `RETIRING`, which is precisely the inference the
fail-closed rule exists to forbid.

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
```

A materializer use is created in `AUTHORIZED` state together with the
`MATERIALIZING` intent, before the PUT. A writer use is created `PENDING` and is
promoted to `AUTHORIZED` only after the active pointer is revalidated. Both kinds
are drained identically by the GC; the distinction exists for recovery, which must
know whether an abandoned use also implies an abandoned materialization.

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
    expires_at            -- null for permanent references
    library_id
    created_at
```

The primary key must partition by logical block and generation so the destructive
check can read one generation globally without mixing G1 and G2. The existing
`block_references` primary key cannot be altered by migration; the new table is the
generation-aware source for the greenfield implementation, with a reverse
projection by referrer for cleanup.

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
                 generation_id, referrer)
```

This mirrors `gc_block_candidates_by_day`, `gc_s3_orphans_by_day`, and
`gc_failed_items_by_expiry` (`internal/db/migrations/001_initial_schema.cql:317-329,1180-1190,1254-1263`),
which all partition by `(day, bucket)` so the scanner can walk a day in parallel.
Partitioning by `org_id` would concentrate every expiring reference of the largest
tenant into one partition. The projection must cover `pub:` as well as `up:`; its
scanner confirms the exact reference row's expiry before cleanup and candidate
creation. A writer-owner callback is not a correctness requirement.

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
- `internal/db/migrations/001_initial_schema.cql:1239-1263`,
  `internal/db/migrations/007_gc_s3_orphan_mapping_recovery.cql`, and
  `internal/db/migrations/009_block_representation_mappings.cql` define the current
  orphan shape. Later migrations add `external_sha1`, `recovery_phase`, and
  `representation_id`; none adds `generation_id` or an exact `storage_key`.
- `docker-compose.mr-cluster.yaml:245-248,285-288` currently sets `LOCAL_SERIAL`
  and therefore is not final evidence for global LWT behavior.
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
  references, reuse, repair authorization, or dedup, and adds at most one
  activation CAS attempt per materializing request. Retirement and delete LWTs run
  in the GC worker.
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

### PR-1: Final Greenfield Schema And Models

Add generation, materialization, pin, reference binding, candidate, queue, orphan,
claim, and recovery fields/tables. Add Go models and migration tests.

Includes the append-only retirement-evidence table, the `QUARANTINED` state, the
`(expiry_day, bucket)` partitioning for the provisional-expiry projection, and a
decision on whether generation-aware claims can be written so they cannot create
stub rows at all rather than repairing them afterwards.

### PR-2: Explicit Consistency Helpers

Add local writer operations and explicit global destructive operations. Add query
context propagation and fail-closed behavior for all global reads/LWTs.

Includes the startup assertion that the keyspace uses `NetworkTopologyStrategy`
whenever `gc.enabled=true`, because an `EACH_QUORUM` read degrades silently
without it.

### PR-3: Generation Allocation And Materialization

Generate UUID keys before PUT, persist the durable `MATERIALIZING` intent together
with its `AUTHORIZED` materializer use, verify the object, reuse the existing
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
`DELETING`, `QUARANTINED`, and exact-key deletion.

The drain reads uses before references — see Read Order Is Not Decision Order — and
implements the `RETIRING -> ACTIVE` escape on expired-authority uses. The retirement
handoff writes epoch-stamped evidence before the pointer CAS and never authorizes
`DELETING` from the generation row alone.

### PR-7: Recovery And Readers

Make canonical reads use persisted keys, scan materializing/deleting generations,
reconcile retirement evidence against the live claim epoch, rebuild missing orphan
projections, and clean only exact generations. Quarantined generations are skipped
by every automated path.

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
- a generation whose only remaining uses have all expired their authority is
  reactivated rather than parked, so an abandoned pin cannot make a block
  unwritable for its full `retention_expires_at` window;
- a reactivation on expired-authority uses is followed by the recovery scan that
  re-examines `ACTIVE` generations with no reference and no live use;
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
- `RETIRING -> ACTIVE` on abandoned uses writes a delayed candidate and its
  discovery projection row, and the generation is re-examined without any
  `block_generations` scan;
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
- a lost step-2 retirement persist is detected and the global zero check re-runs;
- `pointer=RETIRING` plus a matching-epoch evidence row **never reaches
  `DELETING`**: assert no `DELETING` transition, no `DeleteObject`, no G2
  activation, and that the global zero check re-runs before the pointer CAS
  completes;
- an evidence row from an older claim epoch is ignored rather than used to complete
  a retirement it did not earn;
- a generation that travels `RETIRING -> ACTIVE -> RETIRING` cannot have its second
  cycle satisfied by first-cycle evidence;
- a stale worker that appends evidence under an old claim epoch cannot destroy or
  regress a newer worker's evidence, and cannot regress the generation row out of
  `RETIRED` or `DELETING`;
- G2 activation is accepted as soon as the pointer CAS commits, without waiting for
  the mirror finalize; the resulting `pointer=G2` with an un-finalized mirror is
  reconciled, not quarantined;
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
  references, reuse, repair authorization, or deduplication, and at most one
  activation CAS attempt per materializing request;
- two concurrent rematerializers issue two activation CAS attempts and exactly one
  succeeds; the acceptance criterion is not "one CAS per rematerialization";
- a storage key carrying a generation suffix round-trips through every parser in
  the physical key inventory;
- all use rows, including rows whose authority expired but whose retention has not,
  are visible to the drain and are classified by authority rather than ignored;
- no unconditional statement writes any active-pointer or `retire_claim_*` column;
- the provisional reference TTL is greater than the measured upload-to-commit
  window, and a commit whose provisional reference expired fails closed with a
  documented error rather than publishing a reference to a dead generation;
- with `gc.enabled=true` and a non-`NetworkTopologyStrategy` keyspace, startup
  fails; the process must not run destructive GC where `EACH_QUORUM` reads degrade
  to an ordinary quorum.

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
  `pointer=RETIRING` with a matching-epoch evidence row never produces a
  `DELETING` transition, an S3 delete, or a G2 activation;
- a crash between the pointer CAS and the retirement persist, followed by G2
  activation, quarantines G1 instead of deleting `K1`;
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

Expected total: **40-65 engineer-days**, with the actual range refined after the
writer inventory and first schema prototype.

The complete successful writer protocol has approximately five to six local
Cassandra interactions per block when pin confirmation, active-pointer
revalidation, authority validation, reference publication, and pin removal are
counted. Existing probe/reference work may be reused or coalesced where the same
query already supplies the observation, so the incremental cost must be measured
against the current path; it must not be budgeted as only three round trips.

The fence adds no Paxos to those per-block operations. It adds at most one
activation CAS attempt per materializing request, which executes only when a
previously retired SHA comes back to life and therefore does not scale with request
volume. Concurrent rematerializers of the same block each attempt once, so the
attempt count is bounded by concurrent writers of that one block. The pre-existing
upload LWTs listed in Current Code Evidence are unchanged.

Cold-path cost is several global reads/LWTs per candidate and intentional dependence
on the slowest participating DC. A DC outage causes retention rather than deletion.

Temporary G1/G2 coexistence consumes additional storage only after G1 reaches
`RETIRED`; G2 is forbidden during `RETIRING`.

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
- the keyspace uses `NetworkTopologyStrategy` and the process refuses to start
  destructive GC otherwise, since `EACH_QUORUM` reads degrade silently without it;
- the global zero check reads uses before references, and the ordering is asserted
  by a test rather than left to reviewer discipline;
- the publication frontier is stated as an invariant and no second global reference
  check is inserted between the retirement persist and the pointer CAS;
- G1 retirement evidence is durably appended before the pointer CAS that lets G2
  activate, it is keyed by claim epoch and never overwritten, evidence alone never
  authorizes `DELETING`, and an unprovable retirement quarantines rather than
  deletes into a durable `QUARANTINED` state that survives restart;
- the gate for G2 is the pointer CAS, not a later mirror write, and no rule
  requires a condition spanning two Cassandra tables;
- pin authority is enforced at authorization and again immediately before any
  physical operation, not only at publish;
- `RETIRING` is never a parking state: it ends on a reference, on all remaining
  uses having expired their authority, or on a zero drain, and an abandoned pin
  cannot make a block unwritable for its full retention window;
- every recovery cleanup branch confirms the absence of use rows, not only of
  references;
- expired authority cannot publish;
- pins and ordinary references remain `LOCAL_QUORUM`;
- retire, drain, final refs, and deleting use the required global levels;
- the GC decision order evaluates errors, then references, then uses;
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
- `DELETING` recovery works without an orphan projection;
- active-pointer transitions use one `blocks` row, while terminal generation
  transitions use the generation row; no cross-table CAS is assumed;
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
   generation whose remaining uses have *all* expired their authority does return
   to `ACTIVE`, to release the writer fence rather than to assert liveness.
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
21. G2 activation may run inline in the materializing request. Forcing it into a
    background allocator was a rejected over-correction: it would leave a request
    that completed a correct PUT unable to finish, returning a retryable error
    while waiting on a periodic worker and inviting duplicate losing generations.
    One CAS on a cold path is the cheaper trade.

    This has been re-raised in review as "inline Paxos in the request path
    violates the rule that only the pre-existing upload Paxos may be in a
    request". No such rule exists in this document, and the opposite is recorded
    in the Executive Decision and in correction 5: the upload path already carries
    several LWTs today, and the fence's constraint is that it adds none to pins,
    revalidation, references, reuse, repair authorization, or deduplication — the
    operations whose cost multiplies with request volume. Rematerialization is not
    one of them. Deferring it does not remove the WAN dependency either, since the
    upload cannot publish a reference to a generation that is not active; it only
    moves where the failure surfaces and adds a worker interval to it.
22. The materialization intent is also an `AUTHORIZED` use, held from before the
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
28. G1 retirement must be durably persisted in `block_generations` before G2 may
    overwrite the active pointer. Once G2 owns the pointer, `blocks` no longer
    holds any evidence that G1 was retired, and a delete of `K1` rests on that
    evidence. An unprovable retirement quarantines; it never deletes.
29. `RETIRED` is a statement about authority, not about row absence. A late
    `PENDING` use may appear after the final global read; it is harmless because
    its own revalidation is ordered after its insert and will observe the fence.
    Do not write a drain loop that waits for "no use row can exist".
30. Ambiguous `PENDING` insert and ambiguous authorization have different
    confirmation contracts. Requiring `state=AUTHORIZED` to confirm a `PENDING`
    insert would abort a pin that landed correctly.
31. Recovery cleanup must confirm the absence of use rows, not only references. A
    materializer holds a use and no reference for its whole dangerous window.
32. Activation is "at most one attempt per materializing request, exactly one
    winner", not "exactly one CAS per rematerialization"; concurrent
    rematerializers each attempt once.
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
    generation whose remaining uses have all expired their authority returns to
    `ACTIVE`; this is retention-safe and is not the rejected `pin -> ACTIVE` rule,
    which concerns uses that still hold live authority.
38. Retirement evidence is epoch-scoped **and append-only**. A generation can travel
    `RETIRING -> ACTIVE -> RETIRING`, so evidence that does not name its cycle is
    meaningless. A mutable state on the generation row fails twice: it is not
    self-dating, and a stalled worker holding an old claim can overwrite a newer
    worker's evidence — potentially regressing the row out of `RETIRED` or
    `DELETING` and destroying the delete authorization this ADR designates a
    recovery source. One immutable row per `(generation, claim_epoch)` removes both
    failures with no additional Paxos.
39. `pointer=RETIRING` with a matching-epoch evidence row is a normal, expected
    crash state and was missing from the reconciliation table. It authorizes
    nothing: no `DELETING`, no G2, no S3 delete. Evidence alone never authorizes
    deletion; the authoritative pointer must also have left `RETIRING`.
40. The gate for G2 is the **pointer CAS**, not the mirror finalize. The activation
    CAS conditions on `blocks` alone, so G2 can win the instant the pointer reads
    `G1 / RETIRED`; no later write to another table can hold it back. An earlier
    revision of this ADR ordered the mirror finalize before "G2 may activate",
    which is unenforceable and invites the cross-table condition this design
    forbids. Durable proof of retirement is the evidence row plus the pointer
    having left `RETIRING` — including the case where it already selects G2.
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
44. Confirming the pin is not redundant ambiguity handling. It orders the insert
    before the revalidation read, which is what makes both the `RETIRED` guarantee
    and the publication frontier true.
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
52. `QUARANTINED` is a durable generation state with a reason, not an operator
    note. The fail-closed branch is reachable after a crash, so without a
    persistent marker a restarted worker cannot distinguish a quarantined
    generation from an ordinary `RETIRING` — which is exactly the inference the
    fail-closed rule forbids.

## Related Documents

- [Known issues](./KNOWN_ISSUES.md), X1 and X2 remain open.
- [Open work index](./OPEN-WORK-INDEX.md), production activation gate.
- [Upload-fence findings registry](./UPLOAD-FENCE-FINDINGS-REGISTRY.md), audit evidence.
- [Historical upload-fence PR plan](./GC-UPLOAD-FENCE-PR-PLAN.md), PR-1 through PR-10 history.
- [Architecture](./ARCHITECTURE.md), current GC behavior and known limitations.
- [Database guide](./DATABASE-GUIDE.md), current schema and consistency inventory.
- [Multi-region testing](./MULTIREGION-TESTING.md), existing regional test guidance.
- [Session checklist](./SESSION_CHECKLIST.md), required documentation verification.
