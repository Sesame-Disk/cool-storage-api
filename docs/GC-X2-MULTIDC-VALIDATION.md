# X2 — multi-DC validation runbook and closure findings

**Scope:** `ISSUE-GC-CROSS-DC-REFERENCE-VISIBILITY-01`. Status of record lives in
`KNOWN_ISSUES.md`; this document holds the reproduction, the runbook, and the
findings turned up while implementing the fix.

**Related:** `GC-X1-X2-ALTERNATIVES.md` (why X2 is separable from X1),
`UPLOAD-FENCE-FINDINGS-REGISTRY.md` X2, `GC-X1-X2-GENERATION-FENCE-ADR.md` (r3,
still the accepted-for-review design for **X1**, untouched by this fix).

---

## The property under test

> Every physical delete is authorized by a liveness read that intersects every DC
> able to acknowledge a `LOCAL_QUORUM` reference write.

`EACH_QUORUM` must obtain a quorum in every datacenter, so it intersects the quorum
that acknowledged the write in whichever DC accepted it. Writers stay `LOCAL_QUORUM`;
no WAN is added to the upload path.

## Why two datacenters cannot prove it

This is the single most important thing to know before trusting a green run.

At **two** DCs with RF 1 there are two replicas total, so a non-local `QUORUM` is 2
of 2 — it contacts every replica and intersects everything **by accident**. A test
suite on a two-DC cluster passes whether or not the fix is present, and would also
pass with plain `QUORUM`, which is *not* a valid closure.

At **three** DCs with RF 1, `QUORUM` is 2 of 3 and can be satisfied by the two DCs
that do not hold the reference. Only here do the correct and incorrect designs
separate. Production is three DCs (`.env.prod.example`:
`CASSANDRA_REPLICATION_DCS=dc-na:1,dc-eu:1,dc-asia:1`).

`docker-compose.mr-cluster.yaml` is pinned to two DCs (`{usa:1, eu:1}`) because it
exists to prove regional *application* behaviour. It is the wrong instrument here,
which is why `docker-compose.cassandra-3dc.yaml` was added: same cluster shape,
three DCs, nothing but Cassandra.

## The naive test proves nothing

Before the runbook, the trap it exists to avoid.

"Write `LOCAL_QUORUM` in dc-eu, read `EACH_QUORUM` from dc-na, assert visible" is
**not** a test of this fix. Cassandra sends every mutation to **all** replicas
regardless of consistency level; the level only decides how many acknowledgements
the coordinator waits for. So dc-na's replica has usually already received the row,
and that assertion passes whether the read is `EACH_QUORUM` or `LOCAL_QUORUM` — it
passes on the *unfixed* code too.

The regression has to assert **both halves against the same divergent state**:

```text
LOCAL_QUORUM from dc-na  ==  false   ← the defect: the local view is blind
EACH_QUORUM  from dc-na  ==  true    ← the fix: the global view intersects dc-eu
```

The first assertion is what makes the second mean anything. `TestX2_Divergent...`
fails loudly if dc-na can already see the reference locally, precisely so a
mis-built state cannot be mistaken for a passing fix.

## Runbook

```bash
# 1. Stand up three real datacenters. Nodes join one at a time, so first formation
#    takes a few minutes; the bootstrap waits for all three in gossip before
#    creating the keyspace (a CREATE KEYSPACE naming an unseen DC is rejected).
docker compose -f docker-compose.cassandra-3dc.yaml up -d
docker compose -f docker-compose.cassandra-3dc.yaml logs -f cassandra-3dc-bootstrap

# 2. Apply the schema through the local DC.
CASSANDRA_HOSTS=127.0.0.1:9242 CASSANDRA_LOCAL_DC=dc-na go run ./cmd/sesamefs migrate

export X2_DC_HOSTS="dc-na=127.0.0.1:9242,dc-eu=127.0.0.1:9243,dc-asia=127.0.0.1:9244"

# 3. Disable hinted handoff on every node. Without this the stopped DCs receive
#    the write as a replayed hint when they come back, the divergence evaporates,
#    and the visibility test goes back to proving nothing.
for n in na eu asia; do
  docker exec sesamefs-cassandra-$n nodetool disablehandoff
done

# 4. Build the divergent state: stop the other two DCs, write the reference in
#    dc-eu (LOCAL_QUORUM still succeeds — dc-eu has its own replica), bring them
#    back. Note the ids the helper prints.
docker compose -f docker-compose.cassandra-3dc.yaml stop cassandra-na cassandra-asia
X2_WRITE_DIVERGENT=1 go test -tags integration ./internal/integration/ \
  -run TestX2_WriteReferenceForDivergence -v
docker compose -f docker-compose.cassandra-3dc.yaml start cassandra-na cassandra-asia

# 5. Visibility leg — THE regression. Both halves must hold.
X2_DIVERGENT_ORG=<printed> X2_DIVERGENT_BLOCK=<printed> \
  go test -tags integration ./internal/integration/ -run TestX2_Divergent -v

#    Mutation check, strongly recommended: point BlockHasReferencesGlobal at
#    LOCAL_QUORUM instead and confirm this test FAILS. A regression that cannot
#    fail is not evidence.

# 6. Fail-closed leg: with a DC down the destructive read must ERROR, not return
#    zero. A false zero here is data loss.
docker compose -f docker-compose.cassandra-3dc.yaml stop cassandra-asia
X2_EXPECT_DC_DOWN=1 go test -tags integration ./internal/integration/ \
  -run TestX2_EachQuorumFailsClosed -v
docker compose -f docker-compose.cassandra-3dc.yaml start cassandra-asia

# 7. Topology gate accepts a valid 3-DC keyspace.
go test -tags integration ./internal/integration/ -run TestX2_TopologyGate -v

docker compose -f docker-compose.cassandra-3dc.yaml down -v
```

The tests **skip** without `X2_DC_HOSTS`, and skip if it names fewer than three
DCs, so a two-DC environment cannot report a false pass.

**RF 1 only.** Under `NetworkTopologyStrategy` the replication factor is *per
datacenter*, so RF 3 means three replicas in **every** DC — nine nodes, not three.
Setting RF 3 on this three-node fixture yields a keyspace whose `EACH_QUORUM` reads
can never be satisfied, which reads as a failing fix rather than a misconfigured
harness. RF 1 is the production shape and what X2 closure requires; an RF 3 pass
would need its own 9-node stack and is hardening, not closure.

## What the unit suite does and does not cover

`internal/gc/x2_cross_dc_liveness_test.go` runs in the normal suite and pins what a
single process can observe: which read authorizes a delete, that an erroring read
fails closed, that the topology gate guards both destructive paths, and that a
remote-only reference aborts the delete. The authorization canary is
mutation-verified — reverting that one call makes the suite delete a live block
under an unavailable DC.

It cannot observe a consistency level. Everything about per-DC quorum intersection
is the integration suite's job.

---

## Findings from the implementation

Recorded because they shaped the fix and because several outlive it.

### 1. Four liveness readers, one authorizer

`BlockHasReferences` has four production callers, and only one may authorize
destruction:

| Caller | Role | Level |
|---|---|---|
| `processBlock` pre-claim | Short-circuit | `LOCAL_QUORUM` |
| `processBlock` claim-then-verify | **Authorizes the delete** | **`EACH_QUORUM`** |
| `scanner.go` | Candidate discovery | `LOCAL_QUORUM` |
| `enqueueZeroRefBlocks` | Candidate discovery | `LOCAL_QUORUM` |

The zero-check is **asymmetric**: proving "a reference exists" takes one positive
answer from any replica, while proving "zero references" takes an answer from all of
them. A locally visible row is proof the block is alive, so aborting early is always
correct; a local zero proves nothing and authorizes nothing. Raising the discovery
reads would be correct but would pay WAN to learn something the verify has to
re-establish anyway.

### 2. Two delete paths, one of them authorized transitively

`processBlock` deletes under its own verify. `RecoverS3Orphans` deletes bytes
**without reading references at all** — it is authorized by the existence of a
`gc_s3_orphans` row, which cannot exist unless `processBlock` already passed its
verify (`StartBlockDeleteOrphan` runs strictly after it).

That makes the closure an *invariant to enforce*, not an edit to make. A future
destructive path that does not descend from that verify reopens X2 with no failing
test. The invariant is stated at both sites for that reason.

**The transitive link depends on a deployment precondition, and it is worth naming:**

> **Greenfield invariant.** On first production activation, `blocks`,
> `gc_s3_orphans` and the GC work tables contain no rows written by a pre-X2
> protocol.

"An orphan row implies an `EACH_QUORUM` verify" is true *going forward*, not
*backwards*. A row written by the previous code was authorized by a `LOCAL_QUORUM`
verify, and `RecoverS3Orphans` would happily finish that delete after an upgrade —
without any global read ever having happened. SesameFS deploys clean, with no legacy
state, so the chain holds from day one and no extra `EACH_QUORUM` is needed inside
recovery.

This is an operational precondition rather than a code check, and it is only sound
while the greenfield assumption holds. Were SesameFS ever to be upgraded in place
over a cluster where destructive GC had run — including a local stack, where
`.env` ships `GC_ENABLED=true` — the honest options are to drain `gc_s3_orphans`
before activation, or to give `RecoverS3Orphans` its own `BlockHasReferencesGlobal`
check. It is cheap: recovery is already the cold path.

### 3. The topology argument had nothing enforcing it

The per-DC quorum argument presumes `NetworkTopologyStrategy` with every
replica-holding DC in the map. Under `SimpleStrategy`, `EACH_QUORUM` has no per-DC
meaning and the closure does not hold.

Before this fix, `logCassandraRuntimeConfig` only **warned** — on replication
mismatch, and on `LOCAL_SERIAL` under multi-region NTS. Nothing gated. The new
`ValidateDestructiveGCTopology` reads **live keyspace metadata** (not config,
because the real map comes from `CASSANDRA_REPLICATION_DCS` and the checked-in
profiles are the local harness) and is re-evaluated per destructive attempt, since
replication can be altered at runtime.

### 4. Fail-closed couples GC availability to every DC

`EACH_QUORUM` fails if any DC is unreachable, and at RF 1 the tolerance is **zero** —
one unreachable node stops collection fleet-wide. That is correct under the ADR's
standing "when in doubt, do not delete", but it is not free: a silent stall defers
the user (7d), library trash (30d) and org (30d) purge SLAs and leaves reclaimed
space uncollected. Hence
`GCErrorsTotal{reason="liveness_verify_unavailable"|"destructive_topology_gate"}`
and `GCAuditEventsTotal{event="gc_block_delete_failed_closed"}`. **Alert on a
stalled GC, not only on a slow one.**

### 5. Tombstones bias safely

`block_references` does not set `gc_grace_seconds`, so it inherits the 10-day
default. A tombstone purged in one DC while the row survives in another can make an
`EACH_QUORUM` read report a reference that was already deleted. That is conservative
— it delays collection, never authorizes a delete — but it is worth knowing when
diagnosing blocks that refuse to collect.

### 6. `configs/` is not the fleet topology

`configs/*.cluster.yaml` declare `{usa:1, eu:1}` and are **correct as they stand**:
they belong to `docker-compose.mr-cluster.yaml`. Production is
`docker-compose.prod.yml`, which pins no DC map and takes it from the environment.
Sizing a quorum argument from `configs/` yields 2 of 2, where non-local `QUORUM`
looks sufficient, instead of the real 2 of 3, where it is not. Do not read the DC
count out of `configs/`.

### 7. Carried into the X1 work, not fixed here

- **The `resolveFence` fast-clear is inert in production.** The only implementation,
  `clearSeafHTTPS3OrphanFence`, returns `(false, nil)` on every path. Writers cannot
  shorten a fence wait; they re-probe each retry and proceed on the first attempt
  after GC clears the orphan.
- **Six conditional statements touch the `blocks` partition** — `ClaimBlockDelete`,
  `ReleaseBlockClaim`, `FinalizeBlockDelete`, `UpsertBlockMetadata`, the stub repair
  claim and its conditional delete — and all six inherit the session serial level,
  which the cluster profiles set to `LOCAL_SERIAL`. Mixing `LOCAL_SERIAL` and
  `SERIAL` on one partition breaks linearizability: they are different quorum
  domains, and one straggler invalidates every other statement's guarantee. r3 calls
  this the **one-serial-domain rule**. X2 does not depend on it — this fix changes no
  LWT — but any X1 design does. Tracked as R12 in `GC-X1-X2-ALTERNATIVES.md`.
- **`gcS3OrphanInitialScanLookbackDays = 90` is pinned to the orphan TTL** by design;
  its comment says so. Any X1 design that removes that TTL must redefine the
  cold-start horizon in the same change, or orphans older than 90 days become
  undiscoverable after a cursor loss.

---

## Closing checklist

X2 moves to Closed in `KNOWN_ISSUES.md` when:

- [x] Destructive verify reads at explicit per-query `EACH_QUORUM`
- [x] Authorization invariant stated and enforced across both delete paths
- [x] Topology gate on the destructive path, from live keyspace metadata
- [x] Fail-closed is observable
- [x] Unit regressions, authorization canary mutation-verified
- [x] A failed verify does not wedge the block (claim released on both paths,
      mutation-verified)
- [ ] **Divergent-state visibility leg green** (runbook step 5) — and confirmed to
      FAIL when the destructive read is downgraded to `LOCAL_QUORUM`. A green run on
      a non-divergent cluster does not count.
- [ ] **Fail-closed leg green** (runbook step 6)
- [ ] **Topology gate leg green** (runbook step 7)

RF 3 is not on this list: it needs a 9-node stack and is hardening, not closure.

Closing X2 does **not** enable destructive GC. `GC_ENABLED=false` remains mandatory
on every replica in every DC, resting on `ISSUE-GC-UPLOAD-FENCE-REMATERIALIZATION-01`
(X1) alone.
