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

## Why the fixture needs three datacenters

This is the single most important thing to know before trusting a green run — and it
is a narrower claim than "two DCs prove nothing", which is what an earlier draft of
this document said and is not true.

**Two DCs are enough to reproduce the original defect.** With `{dc-na:1, dc-eu:1}`
and a reference acknowledged only in dc-eu, a `LOCAL_QUORUM` read from dc-na does not
see it while an `EACH_QUORUM` read from dc-na must obtain a quorum in *each* DC and
therefore does. The bug and its fix are distinguishable at two DCs.

**What two DCs cannot do is rule out the wrong fix.** With two replicas total, a
non-local `QUORUM` is 2 of 2: it contacts every replica and intersects everything
**by accident**. So a two-DC suite passes with `EACH_QUORUM` and passes just as
happily with plain `QUORUM` — and plain `QUORUM` is *not* a valid closure.

At **three** DCs with RF 1, `QUORUM` is 2 of 3 and can be satisfied by the two DCs
that do not hold the reference. Only here do `EACH_QUORUM` and `QUORUM` separate, and
only here does the fixture match production (`.env.prod.example`:
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

Automated as [`scripts/x2-multidc-validation.sh`](../scripts/x2-multidc-validation.sh)
— `--keep` leaves the stack up, `--no-up` reuses a running one, `--mutate` re-proves
that leg 1 can fail. Prefer it: a multi-step manual procedure that cannot be run in
one command is a procedure that quietly stops being run. The steps below are what it
does, and why each one is load-bearing.

```bash
# 1. Stand up three real datacenters. Nodes join one at a time, so first formation
#    takes a few minutes; the bootstrap waits for all three in gossip before
#    creating the keyspace (a CREATE KEYSPACE naming an unseen DC is rejected).
docker compose -f docker-compose.cassandra-3dc.yaml up -d
docker compose -f docker-compose.cassandra-3dc.yaml logs -f cassandra-3dc-bootstrap

# 1b. WAIT for that container to exit 0 before continuing. Three healthy nodes do not
#     mean the keyspace exists — the bootstrap starts only once they are healthy and
#     then polls gossip. Skipping this races it against step 2.
docker compose -f docker-compose.cassandra-3dc.yaml wait cassandra-3dc-bootstrap

# 2. Apply the schema through the local DC.
#    The replication map is stated explicitly and is NOT optional: migrate creates the
#    keyspace itself when it is missing, and with only CASSANDRA_LOCAL_DC set it falls
#    back to {dc-na: 1} — the under-declared map leg 3b asserts the gate must refuse.
#    Stating it makes both possible creators produce the same keyspace.
CASSANDRA_HOSTS=127.0.0.1:9242 CASSANDRA_LOCAL_DC=dc-na \
  CASSANDRA_REPLICATION_CLASS=NetworkTopologyStrategy \
  CASSANDRA_REPLICATION_DCS=dc-na:1,dc-eu:1,dc-asia:1 \
  go run ./cmd/sesamefs migrate

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

# 7. Topology gate: accepts the declared 3-DC keyspace, and refuses a session whose
#    declared map is smaller than the live one (the shape a shrunk ALTER leaves).
go test -tags integration ./internal/integration/ -run TestX2_TopologyGate -v

# 8. Tear down. `down -v` discards the nodes, so the handoff setting goes with them.
#    If you abort the run and keep the stack, re-enable handoff first — otherwise the
#    fixture silently stays in a state where any later test builds divergence it did
#    not ask for.
for n in na eu asia; do
  docker exec sesamefs-cassandra-$n nodetool enablehandoff
done
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
fails closed on both destructive paths, that recovery refuses a referenced block, that
the topology gate is armed without explicit wiring, that a stale claim is released but
a live one is not, that a failed release does not consume the candidate, that failing
closed does not consume the retry budget, and that a remote-only reference aborts the
delete.

One of those deserves calling out because the obvious version of the test misses the
bug entirely. "A fresh claim is left alone" is only half the requirement: declining to
release must also **keep the candidate**, and must postpone rather than retry. A
candidate settled while a claim is still young leaves `gc_state='deleting'` with no
work item left to clear it once the claim ages out — the block is fenced against every
future upload of its content, permanently — and an item that burns retries while
waiting reaches the DLQ, where block items never auto-recover, arriving at the same
wedge from the other side. Both are asserted, and both mutations were confirmed to go
red.

Every one of those is mutation-verified — each assertion was confirmed to fail against
a deliberately reverted implementation. An assertion that cannot fail is not evidence.
`internal/db/destructive_gc_topology_test.go` does the same for the gate's decision
logic against synthetic replication maps, including the case that motivates comparing
maps at all: a shrunk map that passes every structural check.

The unit suite cannot observe a consistency level. Everything about per-DC quorum
intersection is the integration suite's job.

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

### 2. Two delete paths, and the second one now authorizes itself

`processBlock` deletes under its own verify. `RecoverS3Orphans` deletes bytes
**without reading references at all** — it was authorized by the existence of a
`gc_s3_orphans` row, which cannot exist unless `processBlock` already passed its
verify (`StartBlockDeleteOrphan` runs strictly after it).

That makes the closure an *invariant to enforce*, not an edit to make. A future
destructive path that does not descend from that verify reopens X2 with no failing
test. The invariant is stated at both sites for that reason.

**The transitive link depended on a deployment precondition, and naming it is what
killed it:**

> **Greenfield invariant.** On first production activation, `blocks`,
> `gc_s3_orphans` and the GC work tables contain no rows written by a pre-X2
> protocol.

"An orphan row implies an `EACH_QUORUM` verify" is true *going forward*, not
*backwards*. A row written by the previous code was authorized by a `LOCAL_QUORUM`
verify, and `RecoverS3Orphans` would happily finish that delete after an upgrade —
without any global read ever having happened. SesameFS deploys clean, so the chain
does hold from day one.

It was still the wrong thing to rest a data-loss guarantee on: an operational
precondition that is true today, unenforceable in code, and — this is the part that
decided it — **silent** at the moment it stops being true. Nothing would fail. So
recovery now performs its own `BlockHasReferencesGlobal` before destroying bytes, and
refuses when the block still has references or when the read cannot be established.
The greenfield assumption remains true; nothing depends on it any more. Recovery is
the cold path, so the extra WAN read costs nothing that matters.

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

**Structural validity is not enough, and this is subtle.** The quorum-intersection
proof is about the replica set that *accepted* each reference write, not the one in
effect when GC reads. Consider:

```text
t0   NTS {dc-na:1, dc-eu:1, dc-asia:1}
     dc-eu writer acknowledges reference ABC at LOCAL_QUORUM
t1   ALTER KEYSPACE ... NTS {dc-na:1}
t2   NetworkTopologyStrategy ✅   positive RF ✅   local DC present ✅
     EACH_QUORUM now means "a quorum of dc-na" and never contacts dc-eu
```

Every structural check passes while the guarantee is gone, and Cassandra does not
relocate historical data on `ALTER` — reference ABC is simply unreachable by the read
that authorizes deleting its block. So the gate additionally requires the **live map
to equal the declared one**, which *detects drift between the live topology and the
topology the running process was configured with*. It does not make topology
immutable — nothing here can block a concurrent `ALTER`, and the comparison is
against today's config rather than against the map that accepted the historical
references (see the fingerprint discussion below). Changing topology is therefore an
explicit procedure, and an operational precondition rather than an enforced
invariant:

```text
GC_ENABLED=false everywhere
ALTER KEYSPACE
repair / reconcile the new replica set
update CASSANDRA_REPLICATION_DCS
re-enable GC
```

The gate is part of `GCStore` rather than an optional capability discovered by type
assertion, so a store that does not carry it fails to compile. A data-loss guard that
can be disarmed by wrapping a struct is not a guard.

**What the gate proves, exactly — it is narrower than it first reads.** The comparison
is *today's live topology vs. today's declared config*. That catches the realistic
accident in both directions: the keyspace altered without the deployment config, or the
config changed without the keyspace. It is **not** proof that the map is unchanged
since the references were written. An operator who changes both together — `ALTER
KEYSPACE`, then `CASSANDRA_REPLICATION_DCS`, then restart — passes the gate cleanly
while historical references still live in the datacenters that were dropped.

So the honest statement is: **"topology does not change while destructive GC is
enabled" is an operational precondition, not an invariant this code enforces.** The
gate is a strong detector of accidental drift and nothing more.

> **Follow-up (not built): certified topology fingerprint.** Persist the replication
> map at first destructive activation, compare every subsequent destructive attempt
> against that persisted value, and require an explicit recertification step after a
> topology change and its repair. That upgrades the check from
> `today == today's config` to `today == the certified topology`, which is the property
> the proof actually wants. Deliberately deferred: it is new persisted protocol with a
> trust-on-first-use step of its own, and the gap it closes is reachable only through a
> deliberate multi-step administrative change — precisely what the documented procedure
> covers. Revisit if destructive GC is ever activated on a fleet whose topology is
> expected to evolve.

### 3b. One claim id, two workers — carried to X1

`blockDeleteClaimID` derives from the candidate timestamp so retries of one logical
candidate stay its owner. The side effect is that *concurrent* attempts share it too,
and `ClaimBlockDelete` answers `applied=true` to both. "Our claim" is therefore not
exclusive.

The pre-check release is stale-only for that reason (see 4b). The three post-claim
releases — failed global verify, re-referenced after claim, malformed metadata — are
deliberately **not**: worker A releasing after its own verify failed can drop the fence
while worker B deletes under the same claim id. B's delete stays authorized by its own
`EACH_QUORUM` verify, so this is not an X2 hole; it narrows the upload fence inside a
window X1 already owns, and only if A's release lands between B's verify and B's
`StartBlockDeleteOrphan`.

Making them stale-only would be worse: a failed global verify is *systematic*, so an
unreachable DC would fence every in-flight block for the full staleness threshold on
every outage. The real fix is per-attempt claim identity with staleness-based takeover
— a redesign of the claim protocol, which belongs to X1's r3 rather than here. Recorded
so that design inherits it instead of rediscovering it.

### 4. Fail-closed couples GC availability to every DC

`EACH_QUORUM` fails if any DC is unreachable, and at RF 1 the tolerance is **zero** —
one unreachable node stops collection fleet-wide. That is correct under the ADR's
standing "when in doubt, do not delete", but it is not free: a silent stall defers
the user (7d), library trash (30d) and org (30d) purge SLAs and leaves reclaimed
space uncollected. Hence
`GCErrorsTotal{reason="liveness_verify_unavailable"|"destructive_topology_gate"}`
and `GCAuditEventsTotal{event="gc_block_delete_failed_closed"}`. **Alert on a
stalled GC, not only on a slow one.**

**A stall was worse than a stall, and this needed a code change.** The failure is
systematic, not per-item: every block in flight fails on the same tick for the same
reason. Under the ordinary error path each would burn one of five retries per pass and
land in the DLQ within a few minutes of an outage — and from there:

- the DLQ auto-recovery classifier only rescues `commit`/`fs_object` rows blocked on a
  library hard delete, so `ItemBlock` never returns on its own;
- DLQ rows expire on their own schedule;
- the scanner's block-candidate day cursor advances to `today-1` after a clean
  enumeration pass regardless of what the worker later did with those items, so the
  surviving `gc_block_candidates_by_day` rows sit in a bucket that is never revisited.

A few minutes of DC unavailability would therefore convert deferred collection into
**permanently uncollectable storage**, silently. Fail-closed errors now carry
`GCFailureCodeDestructiveFailClosed` and take the existing postpone path — the same
one lock contention uses — so they cost latency instead of the work item. Pinned by
`TestX2_FailClosedDoesNotBurnTheRetryBudget`, which rides out more passes than the
retry budget would have survived.

### 4b. One claim id, possibly two workers

`DequeueBatch` takes no lease, so two GC workers hand themselves the same queue row,
and `blockDeleteClaimID` derives from the candidate timestamp — deliberately, so
retries of one logical candidate stay its owner. The consequence is that *concurrent*
attempts share a claim id too, and `ClaimBlockDelete` reports `applied=true` to both.

That makes an unconditional "release the claim" call dangerous in the one path that
does not own a delete: the pre-check branch that settles a candidate on a live
reference. Worker B observing a reference could hand back the claim that worker A is
mid-delete under, dropping the upload fence inside the window between A's verify and
its orphan row — the exact window the fence exists to cover. `ReleaseStaleBlockClaim`
therefore releases only a claim older than `blockDeleteClaimStaleAfter` (15 minutes,
far beyond any single walk), leaves anything younger untouched, and reports "nothing
to release" as success rather than as a failed CAS. And when the release genuinely
fails, the candidate is **not** consumed: it is the only work item that can retry
lifting that fence.

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
- [x] Authorization invariant enforced on both delete paths — recovery performs its
      own global verify rather than inheriting one from an orphan row
- [x] Topology gate on the destructive path, from live keyspace metadata, requiring
      the live map to equal the declared one
- [x] Topology gate is part of `GCStore`, so it cannot be dropped without a compile
      error
- [x] Fail-closed is observable, and does not consume the item's retry budget
- [x] A failed verify does not wedge the block (claim released on both paths); a
      stale claim is released, a live one is not, and a failed release does not
      consume the candidate
- [x] Every unit regression mutation-verified
- [x] **Divergent-state visibility leg green** (runbook step 5) — and confirmed to
      FAIL when the destructive read is downgraded to `LOCAL_QUORUM`. A green run on
      a non-divergent cluster does not count.
- [x] **Fail-closed leg green** (runbook step 6)
- [x] **Topology gate leg green, both halves** (runbook step 7): accepts the declared
      3-DC map, refuses an under-declared one

**All legs ran green on 2026-08-13** against `docker-compose.cassandra-3dc.yaml`
(Cassandra 5.0.9, three DCs, RF 1 each), via
[`scripts/x2-multidc-validation.sh`](../scripts/x2-multidc-validation.sh):

```text
LEG 1  divergent state confirmed: LOCAL_QUORUM from dc-na is blind,
       EACH_QUORUM sees the dc-eu reference
LEG 2  EACH_QUORUM correctly failed closed with a datacenter down:
       Cannot achieve consistency level EACH_QUORUM in DC dc-asia
LEG 3a gate accepted the declared 3-DC map
LEG 3b gate refused a session declaring only dc-na against the 3-DC keyspace

MUTATION (scripts/x2-multidc-validation.sh --mutate)
       with .Consistency(gocql.EachQuorum) → LocalQuorum, against a FRESH
       divergent state, leg 1 goes red:
       "X2 REGRESSION: reference acknowledged at LOCAL_QUORUM in dc-eu is
        invisible to the EACH_QUORUM read from dc-na; GC would authorize
        deleting a live block"
```

**Re-run green on 2026-08-13 after the harness itself changed** (per-node healthcheck,
explicit `wait_bootstrap`, explicit `CASSANDRA_REPLICATION_DCS` on the migrate step,
exit-code check in `build_divergence`). Those are the parts that produce the evidence,
so changing them invalidates a green run taken before them; the five tests above went
green again unchanged, and the keyspace was confirmed to carry all three datacenters:

```text
sesamefs | {'class': 'NetworkTopologyStrategy', 'dc-asia': '1', 'dc-eu': '1', 'dc-na': '1'}
```

The mutation leg was not repeated — nothing in that change touches the destructive read,
its consistency level, or the gate.

The mutation is the load-bearing half. Leg 1 passing means nothing on its own — it
had to be shown to go red against the very defect it exists to catch, on a cluster
divergent enough for the two consistency levels to disagree.

RF 3 is not on this list: it needs a 9-node stack and is hardening, not closure.

Closing X2 does **not** enable destructive GC. `GC_ENABLED=false` remains mandatory
on every replica in every DC, resting on `ISSUE-GC-UPLOAD-FENCE-REMATERIALIZATION-01`
(X1) alone.
