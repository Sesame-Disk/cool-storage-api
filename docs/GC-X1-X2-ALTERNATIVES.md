# X1/X2 Alternatives Analysis

**Status:** Analysis. Not an ADR. Not an implementation. Does not supersede r3.

**Date:** 2026-08-13

**Related:**

- [GC-X1-X2-GENERATION-FENCE-ADR.md](./GC-X1-X2-GENERATION-FENCE-ADR.md) — accepted-for-review closure design (r3, not frozen, not implemented)
- [UPLOAD-FENCE-FINDINGS-REGISTRY.md](./UPLOAD-FENCE-FINDINGS-REGISTRY.md) — X1/X2 wording and the closed F-series
- [KNOWN_ISSUES.md](./KNOWN_ISSUES.md) — `ISSUE-GC-UPLOAD-FENCE-REMATERIALIZATION-01`, `ISSUE-GC-CROSS-DC-REFERENCE-VISIBILITY-01`
- [DECISIONS.md](./DECISIONS.md) — Generational GC Fence entry

Destructive GC stays disabled — `GC_ENABLED=false` on **every replica in every DC**, the wording `DEPLOY.md` and `config.prod.yaml` use — until X1 and X2 close. (The single-node local Compose stack deliberately does not pin the variable; that exemption is documented in `docker-compose.yaml` and is not a policy gap.) This document asks whether X1 and X2 can close with a much smaller change than r3.

---

## Why this document exists

ADR r3 is the accepted-for-review design for X1 and X2. It is large because it tries to keep several properties at once: local writer references, a generic S3 backend, concurrent rematerialization, multi-DC GC, crash recovery, and almost no new coordination on the writer hot path. Each kept property pushes coordination somewhere else.

If we are willing to spend WAN on **GC only**, and to own physical identity **in Cassandra**, X1 and X2 can be attacked without making every reference generation-aware.

This is a code-backed comparison, not a decision to implement. The recommended next step is to stress-test **Option 1** against the race matrix below, starting with R8. Implementation is a later decision.

---

## Verified problem statements

### X1 — physical-delete ABA

The physical object key is derived from the logical content hash:

```text
blocks/<org_id>/<hash[0:2]>/<hash[2:4]>/<hash>
```

See `BlockStore.hashToKey` in `internal/storage/blocks.go`. GC then deletes by re-deriving that key: `processBlock` → `deleteS3WithRetry` → `BlockStore.DeleteBlock(blockID)` → `hashToKey` again. `S3Store.Delete` issues a key-only `DeleteObject` (no `VersionId`).

```text
GC determines refs=0
        ↓
authorizes DELETE of blocks/.../ABC123
        ↓
DELETE is slow / retries / already on the wire

                   writer rematerializes ABC123
                   PUT the same key
                   new logical reference
                         ↓
                   old DELETE lands
                         ↓
                   live bytes are gone
```

Cassandra claim state cannot revoke an S3 request already in flight. Because blocks are content-addressed, the new bytes are identical to the old ones, so ETag / `If-Match` / HEAD cannot tell the two lives apart.

`blocks.storage_key` already exists, but it is **not** the locator. Three production sites derive the key and **reject** a persisted key that differs: `canonical_block_reader.go`, and — in `upload_reuse.go` — both `ResolveNeedsPutBlockStore` and the `Reusable` branch of `StoreUploadedBlockForProbe`. That last one matters on its own: when the probe says `Reusable` but the object is missing, it issues a **repair PUT at the derived key**, which is a second X1 exposure distinct from rematerialization.

GC recovery is keyed by logical `block_id`: `gc_s3_orphans` primary key is `((org_id, block_id))`, and `BlockStoreDeleter` is `DeleteBlock(ctx, blockID)`. After `FinalizeBlockDelete` drops the `blocks` row, the remaining writer fence is that orphan row, whose `default_time_to_live` is 7776000 s (90 days). While it exists, `ProbeBlockReuse` answers `BlockedByGC` — the upload **fails**, it does not wait. When the orphan clears, a rematerialized PUT at the same derived key can still lose to a delayed DELETE.

**Minimum property:**

> A DELETE authorized for the old physical life of a block must never be able to delete the new physical life.

That requires distinct physical identities (different keys, or different backend version IDs). Waiting, locking, or re-reading Cassandra after the DELETE has been sent does not establish it.

### X2 — cross-DC reference visibility

`block_references` writes and GC liveness reads inherit the session default. `internal/db/db.go` sets `cluster.Consistency` from config (shipped profiles: `LOCAL_QUORUM`). No production `block_references` query sets a per-statement consistency. `EACH_QUORUM` and `ALL` are parsed in `parseConsistency` and **never applied to a query under `internal/`**. The only production `Consistency(...)` overrides are `One` (system/keyspace introspect) and `Serial` (one HEAD read).

GC liveness is `BlockHasReferences` (`SELECT referrer ... LIMIT 1`). `processBlock` calls it twice: before the claim LWT and after (`claim-then-verify`).

With NetworkTopologyStrategy and RF=1 per DC, a `LOCAL_QUORUM` write in EU and a `LOCAL_QUORUM` read in NA need not intersect:

```text
EU writer: INSERT block_reference @ LOCAL_QUORUM  →  ack
NA GC:     SELECT block_references @ LOCAL_QUORUM  →  0 rows
```

The serial phase of the unrelated `blocks` claim LWT does not make ordinary reference rows globally visible either — and it is weaker than "SERIAL" anyway: `ClaimBlockDelete` sets no serial level, so it inherits `cluster.SerialConsistency`, which the shipped multi-DC profiles (`config-eu.cluster.yaml`, `config-usa.cluster.yaml`) set to **`LOCAL_SERIAL`**. The default 1h `grace_period` delays processing; it is not a cross-DC visibility bound.

**Minimum property:**

> Before authorizing a physical DELETE, GC must be unable to conclude “0 references” if any DC has already accepted a valid reference.

The 1h grace, a longer grace, or “GC runs in one DC” do not prove that.

### What is not X2

```text
GC reads refs = 0
        ↓
writer publishes a reference afterwards
        ↓
GC deletes
```

That is a **publication / writer-GC TOCTOU**. Upload-fence already addresses it inside one DC. Globalizing the GC **read** does not close it. It is tracked separately below so it is not smuggled into X2.

---

## What upload-fence already gives

PR-1..PR-10 closed F1–F14. X1 and X2 were explicitly out of scope. The series is reusable substrate, not a close.

| Mechanism | Where | What it closes | What it does not |
|---|---|---|---|
| Provisional `up:` written **before** metadata | `RegisterUploadedBlock` | Same-DC GC claim-then-verify can see an in-flight upload | Cross-DC LQ miss (X2); PUT still happens before `up:` (X3 leak) |
| Write-ref-then-check-fence | `RegisterUploadedBlock`: `AddProvisionalBlockReferenceWithExpiry` then `BlockDeleteFenceActive` | If the writer sees the fence, it rematerializes instead of reporting success (F1) | Fence itself is a LQ read of `blocks.gc_state` / `gc_s3_orphans`; a remote claim committed at LQ can be invisible |
| Claim-then-verify | `processBlock` | Same-DC: a ref that landed before verify aborts the delete | Verify is LQ; a remote completed ref can be missed (X2) |
| Outer store→materialize→confirm | `RetryUploadedBlockMaterialization` | Re-PUT after a fence that cleared mid-cycle (F1) | Re-PUT uses the **same derived key** (X1) |
| `BlockDeleteFenceActive` | `gc_state='deleting'` **or** `gc_s3_orphans` row | Writers back off after the `blocks` row is gone | Orphan row is LQ and keyed by logical `block_id`; recovery deletes by derived hash |
| `pub:` then `fs:` | `StagePublishAttemptReferences` / `PromotePublishAttemptReferences` | Publish-attempt liveness across HEAD CAS | **No** post-insert fence check on the `pub:` stage (unlike `RegisterUploadedBlock`) |

`ProbeBlockReuse` classifies `Reusable` / `NeedsPut` / `BlockedByGC` / `RepairableStub` / `UnknownError` from LQ reads of `blocks`, refs, and orphans. Two of those states reach the derived key while GC may be deleting the only physical copy: `Reusable` dedup that skips the PUT and only adds a logical ref (and, via `EnsureReusableBlockPresent`, repair-PUTs at that same key when the object is missing), and `RepairableStub`, which `PrepareUploadedBlockProbe` repairs into `NeedsPut` so the upload continues down the ordinary derived-key PUT path.

B4 (`ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01`) is a closed rate-limit umbrella. It is not a GC correctness mechanism.

---

## Why r3 became so large

r3 keeps writer pins, uses, and ordinary references at `LOCAL_QUORUM` — its consistency table is explicit that "provisional/permanent reference insert/delete" stays `LOCAL_QUORUM` and that "ordinary non-LWT pins, references, and writer reads/writes remain `LOCAL_QUORUM`". It therefore has to **prove** that after GC’s global zero-check no new reference can appear against the generation being retired. That proof is the publication frontier: `SERIAL+ALL` `ACTIVE→RETIRING`, `EACH_QUORUM` use drain, `EACH_QUORUM` retirement evidence, then the `EACH_QUORUM` final generation-reference check. It also refuses backend versioning, so it invents UUID generations and immutable keys. Crash recovery, ambiguous Paxos, quarantine, and abort each added tables and CAS shapes.

**This is worth stating precisely, because it is the load-bearing similarity between r3 and the options below: r3 does not close X2 by making reference writes global.** It closes X2 the same way any smaller design would — a global (`EACH_QUORUM`) liveness read on the GC side, with writers left local. What r3 buys with its extra machinery is not X2; it is the publication frontier, which is a different property (see *What is not X2*).

Two load-bearing choices drive most of the page count:

1. **Keep reference writes local** → need a publication frontier *in addition to* the global GC read, not instead of it.
2. **Do not depend on object-store versioning** → need an application physical identity.

If GC (cold path) may use `EACH_QUORUM`/`ALL`, (1) shrinks. If physical identity lives in Cassandra, (2) is a schema/locator change rather than a new lifecycle. The rest of r3 (quarantine/abort/uses) is only required if we still need to prove “no new ref to this physical life can appear after the frontier” without a globally visible fence plus new keys.

r3 remains a correct design **if implemented as specified**. This document asks whether a smaller design is also correct.

---

## Consistency facts that any smaller design must use

### GC can pay WAN; writers need not

X2 is a statement about **completed** reference writes. Cassandra visibility for those writes does not require the writer to use `EACH_QUORUM`.

With writers remaining `LOCAL_QUORUM`:

- A completed EU write at RF=1 is on the EU replica.
- A later GC read at `EACH_QUORUM` or `ALL` must obtain a quorum in **every** DC, including EU.
- The replica sets intersect ⇒ GC cannot conclude `refs=0` if any DC has already accepted the write.
- If a DC is unavailable, the read fails. GC must fail closed (no DELETE). That is acceptable on **latency** grounds — GC is not the hot path. It is not automatically acceptable on **throughput** grounds; see the drain-capacity point under Option 1.

```text
EU writer: INSERT ref @ LOCAL_QUORUM     (durable on EU)
NA GC:     SELECT refs @ EACH_QUORUM     (must contact NA, EU, Asia)
           EU replica returns the row
           GC cannot authorize delete
```

This is the X2 mechanism of choice for a small design. It does **not** put WAN on `up:` / `pub:` / `fs:` inserts.

### `QUORUM` (non-LOCAL) is not enough

The deployment topology is **three** DCs — NA, EU and Asia — at RF=1 each. Writer `LOCAL_QUORUM` in EU: only the EU replica holds the row. A `QUORUM` read needs 2 of 3 replicas and may be satisfied by NA+Asia. X2 remains open. **GC liveness must be `EACH_QUORUM` or `ALL`, not `QUORUM`.**

> **Do not read the DC count out of `configs/`.** The versioned cluster profiles declare `replication_dcs: {usa: 1, eu: 1}` — two DCs, no Asia. That is not the deployed topology: `replication_dcs` is overridable at deploy time via `CASSANDRA_REPLICATION_DCS`, so the checked-in profiles are a starting point, not the source of truth. Anyone sizing a quorum argument from those files will get 2 of 2 (where `QUORUM` looks sufficient) instead of 2 of 3 (where it is not). Worth reconciling separately: either the profiles should carry the real three-DC map, or they should say in a comment that they do not.

Raising RF while keeping LQ writes and LQ reads also fails: `LOCAL_QUORUM` is still per-DC. RF=3 in NA does not make an EU write visible to an NA read.

### `EACH_QUORUM` vs `ALL`

On the current RF=1-per-DC topology they are equivalent — a quorum of 1 in each of the three DCs is every replica. Prefer `EACH_QUORUM` in the design so a later RF>1 does not require every replica in every DC. `ALL` is the stricter availability choice, and it is what a writer-visibility **fence** needs (see Option 1, step 1) as opposed to a liveness read.

### Writer-side `EACH_QUORUM` is optional and not sufficient

Making every `block_references` insert `EACH_QUORUM` would also close X2 for completed writes (the write is then present in every DC, so even a LQ GC read in NA would see it). Costs:

- WAN on the **hot path** (every upload provisional ref, every publish-attempt pin, every `fs:` promote, every removal).
- Does **not** close X1.
- Does **not** close the publication TOCTOU: GC can still observe 0 and the writer can still insert afterwards.

Measuring that latency is reasonable. It should not be the leading X2 design.

---

## Alternatives

| Option | X1 | X2 (completed refs) | Publication TOCTOU | Invasiveness | Verdict |
|---|---|---|---|---|---|
| 0. ADR r3 complete | Closed (generational keys) | Closed (`EACH_QUORUM` final generation-reference check; refs themselves stay `LOCAL_QUORUM`) | Closed (uses + frontier) | Very high | Correct, expensive |
| 1. DB physical incarnation + GC-only global liveness + reuse upload-fence | Closed if keys never reused and GC deletes the exact key | Closed if both claim-then-verify reads are `EACH_QUORUM`/`ALL` | Closed only if the fence is globally visible and the `pub:` stage grows a post-insert fence check | Medium **if** the P1→P2 handover needs no new CAS — unresolved, see R8 | **Recommended investigation** |
| 2. Writer refs also `EACH_QUORUM` | Unchanged | Closed for completed writes | Open | Low for X2, WAN on hot path | Optional extra, not the X2 mechanism |
| 3. S3 object versioning | Closed only if every delete is version-id-specific | Unchanged | Unchanged | Backend + interface | Discussable; not preferred |
| 4. Reduced fence, keep hash keys | **Open** | Closable with EQ reads + ALL fence | Closable with ALL fence | Medium | X2/publication half of Option 1; cannot close X1 |
| Discarded (grace, ETag, HEAD, locks, RF+LQ/LQ, …) | Open | Open or mitigation only | Open | Low | Do not invest |

---

### Option 0 — ADR r3 complete

**What it is.** UUID generations, immutable `storage_key` `…/<hash>.<generation>`, `block_generations` / uses / retirement evidence / quarantine / abort, writer refs still `LOCAL_QUORUM`, destructive liveness `EACH_QUORUM`, `ACTIVE→RETIRING` and the quarantine fences at `SERIAL+ALL`, the remaining generation-lifecycle LWTs at `SERIAL+EACH_QUORUM`, and the existing first-writer LWT at `SERIAL+QUORUM`.

**Pros.** Specified end-to-end, including crash, ambiguous Paxos, false-positive quarantine, concurrent rematerialization, topology gates. Keeps WAN off ordinary reference writes.

**Cons.** ~8881-line protocol, new schema, every writer path becomes generation-aware, large bug surface. Nothing of it is implemented. r3 is not frozen.

**Residual.** Implementation and Phase-0 measurement risk, not a known hole in the spec’s own invariants.

**Reuse.** Upload-fence ordering ideas; not the current tables as-is.

---

### Option 1 — DB physical incarnation + GC-only global liveness (recommended investigation)

Keep the current logical model:

```text
logical SHA ── block_references     (writer writes stay LOCAL_QUORUM)
     │
     └── blocks                     (physical_id + exact storage_key)
```

**X1.** Every new physical life gets a never-reused UUID `physical_id` and an immutable key, for example:

```text
P1 → blocks/<org>/<aa>/<bb>/<hash>.<physical_id-1>
P2 → blocks/<org>/<aa>/<bb>/<hash>.<physical_id-2>
```

GC work records `(block_id, physical_id, exact_storage_key)` and issues `DELETE` of that key only. A delayed DELETE of P1 cannot name P2.

Identity is **ours in Cassandra**, not an object-store version id. That matches the product constraint: versioning, if any, lives in the database so SesameFS does not depend on a specific backend.

References can stay logical (`ABC → fs:…`). They mean “content ABC is live.” `blocks` is what resolves which physical incarnation currently holds that content. GC must not `DELETE FROM blocks WHERE block_id=ABC` after P2 exists; cleanup is `IF physical_id = P1` (or leave the row if it already points at P2).

**X2.** Do not change writer reference consistency. Change **only** GC liveness:

- `BlockHasReferences` used by `processBlock` (both pre-claim and claim-then-verify) at `EACH_QUORUM` (or `ALL`).
- Fail closed if any DC is unavailable.
- Session default stays `LOCAL_QUORUM`. Set the level per query, the same rule r3 already states for global operations.

**Publication TOCTOU.** EQ reads do not close “writer publishes after GC observed 0.” Reuse upload-fence ordering, and make the **fence** visible to every later LQ writer read:

1. Claim at serial phase `SERIAL`, regular commit `ALL` (so `gc_state='deleting'` is on every replica). Today `ClaimBlockDelete` sets neither level: it inherits the session regular level (LQ) *and* the session serial level, which the shipped multi-DC profiles set to `LOCAL_SERIAL`. Both have to be set per statement.
2. Do not drop the `blocks` row before the exact-key DELETE succeeds, **or** persist the orphan/fence at a global level and key it by `physical_id` + exact key. Today `FinalizeBlockDelete` removes the row first; `gc_s3_orphans` is LQ and logical. **These two branches are not interchangeable — see the P1→P2 handover below.**
3. Writers that observe the fence **mint P2** and PUT P2; they must not add a logical ref that pins doomed P1 without a new incarnation.
4. The `pub:` stage follows write-then-check-fence, as `RegisterUploadedBlock` already does. `RegisterFSObjectBlockReferences` does **not** need its own check: it only ever runs inside `PromotePublishAttemptReferences` (all three call sites — `seafhttp.go`, `sync.go`, `fs_helpers.go`), i.e. after the HEAD CAS and while this attempt's `pub:` rows are still live, so `fs:` inherits liveness from a `pub:` row that was itself fence-checked. The gap is one function, not two.

**The unresolved question: who moves `blocks` from P1 to P2, and with what CAS.**

This is the part that decides whether Option 1 is actually smaller than r3, and it is not yet answered.

- `blocks` has primary key `((org_id, block_id))` — **one row per logical block**. P1 and P2 cannot coexist in it.
- `UpsertBlockMetadata` is `INSERT ... IF NOT EXISTS`, deliberately first-writer-wins.
- Today a writer can re-insert *only because* `FinalizeBlockDelete` deleted the row first.
- Step 2 above proposes not deleting the row until the exact-key DELETE succeeds — which forbids exactly the ordering the current re-insert depends on.

So one of two things must be true, and the option has to pick:

**(a) GC keeps the P1 row.** Then the writer minting P2 needs a *conditional update* `P1 → P2` on a row GC has claimed, gated on the delete's authority. That is a new CAS on `blocks` with a predecessor condition — structurally the same object as r3's `RETIRED/GC_RETIRE → ACTIVE` activation.

**(b) GC drops the row first** (today's ordering). Then the durable exact-key fence has to live somewhere else, globally visible, keyed by `(block_id, physical_id)` and holding the exact key. A table with that key and that content **is `block_generations` under another name**, which contradicts the "deliberately does not add" list below.

The phrase "(or equivalent durable exact-key fence)" in the inventory below is carrying this entire question. Until it is answered, "Medium invasiveness" is an estimate, not a finding. **R8 in the race matrix is the test that settles it.**

**What this deliberately does not add** (subject to the P1→P2 answer above). `block_generations`, `block_generation_uses`, generation-bound refs, retirement evidence, publication-frontier protocol, quarantine/abort workflows.

**Pros.**

- X1 by construction (old DELETE cannot name new key).
- X2 for completed refs without hot-path WAN.
- Reuses `up:` / `pub:` / `fs:`, claim-then-verify, rematerialize wrapper.
- Bounded schema **if** the P1→P2 handover resolves as branch (a): `physical_id` + exact key on `blocks`, `gc_s3_orphans` (and its by-day projection). `gc_block_candidates` probably does not need it — `processBlock` reads `GetBlockInfo` at claim time, after the candidate was enqueued.
- Canonical reader change is real but local: persisted key becomes authoritative, with validation (org, hash, format) instead of “derived key or reject.”

**Cons / residual holes (these decide whether the option stays small).**

- **The P1→P2 handover is unresolved** (above). This is the largest one; everything below is secondary to it.
- If the `pub:` stage cannot grow a post-insert fence check, a commit can pin a logical SHA whose only bytes GC is deleting. That is data loss, and fixing it starts to look like generation-bound refs.
- `SERIAL+ALL` on every block claim is a real multi-DC cost **on GC**, and a topology availability constraint (cannot fence if any replica is down). Fail closed is correct; GC stalls until the cluster is whole.
- **“GC is not the hot path” is a latency argument, not a throughput one.** The ADR already worked this out for the same class of cost: `gc.batch_size = 100`, `gc.worker_interval = 30s`, and the queue loop in `internal/gc/worker.go` is strictly serial, so its synthetic sensitivity scenarios land at ~200 s and ~440–550 s per batch against a 30 s tick. Option 1 adds a `SERIAL+ALL` claim plus two `EACH_QUORUM` reads per block, so it inherits that drain problem in full. Whatever GC concurrency work r3 implies, Option 1 implies it too. (Those ADR figures are explicitly illustrative, not bounds — the point is the shape, and that Phase 0 has to measure it either way.)
- Probe/reuse at LQ must see the ALL fence. If we keep LQ commit on the claim, Option 1 does **not** close the remote missed-fence case.
- X3 (PUT before durable intent) remains a leak, not live-delete. Out of scope here, as in r3.
- Readers, dedup, and GC must stop deriving keys from hash alone. The touch set is wider than the read path: `GetBlock`, `BlockExists`, `DeleteBlock`, the whole `PutBlock*` family, `CheckBlocks` / `CheckBlocksParallel`, and `canonical_block_reader`. `CheckBlocks` is the sharpest of these — it backs the `check-blocks` dedup oracle, and under Option 1 “does this content exist?” stops being answerable by a hash-derived HEAD and becomes a database question.

**Call-site inventory (conceptual diff, not an implementation list).**

| Surface | Today | Option 1 needs |
|---|---|---|
| `hashToKey` / `StorageKeyForHash` | Deterministic locator | First-life helper only; live locator is persisted `storage_key` |
| `DeleteBlock(hash)` / `BlockStoreDeleter` | Derive key from hash | Delete exact key captured in GC work |
| `GetBlock` / `BlockExists` / `PutBlock` / `PutBlockData` / `PutBlockAuto` / `PutBlockAutoDirect` | All derive via `hashToKey` | Take an exact key, or resolve one first |
| `CheckBlocks` / `CheckBlocksParallel` | Per-hash derived HEAD | Dedup oracle must resolve the live incarnation from the DB, not from a derived key |
| `S3Store.Put(ctx, blockID, …)` | Second derivation layer: `s.key(blockID)` on what callers already pass as a key | Make the key parameter mean a key |
| `canonical_block_reader.go` | Rejects persisted ≠ derived | Use persisted key; validate org/hash/format |
| `upload_reuse.go` — `ResolveNeedsPutBlockStore` **and** the `Reusable` branch of `StoreUploadedBlockForProbe` | Two reject sites; the second also repair-PUTs at the derived key | Mint `physical_id` on `NeedsPut` / `RepairableStub` / fence; PUT that key |
| `UpsertBlockMetadata` | `INSERT … IF NOT EXISTS`, stores `storage_key` that must match derived | Store `physical_id` + exact key — and see the P1→P2 handover: first-writer-wins cannot install P2 over a live P1 row |
| `ClaimBlockDelete` | LWT inheriting session regular (LQ) and session serial (`LOCAL_SERIAL` in the cluster profiles) | Set both per statement: `SERIAL` + regular `ALL` |
| `BlockHasReferences` (GC) | Session LQ | Per-query `EACH_QUORUM` |
| `FinalizeBlockDelete` | **Already** a conditional LWT: `DELETE … IF gc_state = ? AND gc_claim_id = ?` | Add `physical_id = P1` to the existing `IF`; and either order it after the exact-key DELETE or provide the equivalent durable exact-key fence (unresolved) |
| `gc_s3_orphans` | PK `((org_id, block_id))`; already grew `external_sha1`/`recovery_phase` (migration 007) and `representation_id` (009), mirrored on `gc_s3_orphans_by_day` | Add exact `storage_key` / `physical_id` to both; recovery and `ListS3OrphansByDay` must not `hashToKey` |
| `RegisterUploadedBlock` | Write `up:` then fence check | Keep; rematerialize must mint P2 |
| `StagePublishAttemptReferences` | Insert only | Write then fence check; fence → rematerialize, do not treat as reusable |

**When this option stops being small.** If the P1→P2 handover needs a new conditional transition or a second physical-identity table, or the `pub:` audit fails, or the ALL-fence is operationally unacceptable, the missing piece is exactly what r3’s generations/uses/frontier provide. At that point, do not invent a third protocol; return to r3 or a documented subset.

---

### Option 2 — writer references at `EACH_QUORUM`

**What it is.** `AddBlockReference` / provisional / publish-attempt / removals set `EACH_QUORUM` per query. GC could even stay LQ in a DC that received the write.

**X1.** Unchanged.

**X2.** Closed for completed writes (stronger than needed).

**Publication TOCTOU.** Open. A writer can still insert after GC’s zero check.

**Pros.** No schema change for X2. Easy to A/B measure — and measurement is the only honest way to state the cost here. Do **not** reason about it as RTT × rounds: the ADR rejects that model explicitly, because `SERIAL` waits for a global response order statistic, ordinary `EACH_QUORUM` waits for a local quorum in *every* DC, and `ALL` waits for the slowest replica. Whether the added latency disappears next to the S3 PUT and the first-writer LWT is a Phase-0 question, not a desk estimate.

**Cons.** WAN on every reference mutate, including churny `up:`/`pub:` rows. Does not remove the need for Option 1’s physical keys. Must not be confused with “then we can skip EQ on GC”: if any writer path is forgotten and stays LQ, X2 reopens unless GC still reads `EACH_QUORUM`.

**Verdict.** Optional measurement, not the X2 design.

---

### Option 3 — S3 object versioning (discussable)

Exact version-aware PUT/GET/DELETE can separate two lives on the **same** key:

```text
PUT ABC → versionId=V1
GC DELETE ABC?versionId=V1
new PUT → versionId=V2
delayed DELETE V1  →  V2 remains
```

That is the same *shape* as `ABC.P1` / `ABC.P2`.

**Why it is not the preferred path.**

Product: if there is versioning, it should be **ours in Cassandra**, so SesameFS does not depend on a specific object backend. The runtime `Store` interface today is key-only (`Put`/`Get`/`Delete(storageKey)`). The only implemented backend is `S3Store`; config mentions `filesystem` but no filesystem `Store` exists. Even so, production intent is S3-compatible generality (AWS, MinIO, and whatever else is wired as `type: s3`).

**Delete-marker caveat (this is why “just enable versioning” is unsafe).** On a versioned bucket, a **key-only** `DeleteObject` does not remove old versions; it writes a delete marker and makes unversioned GET see a 404. A delayed key-only DELETE after a new PUT still hides the live current version. Versioning closes X1 **only if every delete path is version-id-specific**, including orphan recovery. Falling back to key-only delete reopens X1 from the reader’s point of view.

Other costs: persist `versionId` on `blocks` and all GC work; `Get` must use the stored id or the current version can be a marker; bucket versioning must be enabled and lifecycle-managed on every production backend (unbounded old versions otherwise); MinIO and AWS behavior is not identical.

**Pros.** Smallest *schema* story if we were willing to require versioning. No hash-key format change.

**Cons.** Backend-dependent; contradicts DB-owned identity; current GC/storage/recovery are all key-only; easy to implement unsafely (delete marker).

**Verdict.** Recorded so it is not “forgotten.” Not the primary close. If it were ever revived, it would still need Option 1’s X2 (GC `EACH_QUORUM`) and the publication-fence work; versioning is an X1 mechanism only.

---

### Option 4 — reduced fence, keep hash-derived keys

Fence at `SERIAL+ALL`, GC refs at `EACH_QUORUM`, keep `blocks/<org>/…/<hash>`.

**X2 + publication.** Potentially closable (same fence/EQ story as Option 1).

**X1.** **Open.** The stale DELETE still names the only key the new upload will use. This is the case the registry already rejected: “Cassandra authorization/claim generations alone cannot revoke a DELETE already in flight.”

**Verdict.** Useful as the X2/publication *half* of Option 1. Discarded as an X1 close.

---

### Explicitly discarded

None of the following close X1 while old DELETE and new PUT share one physical identity:

- Longer grace (1h / 24h / 7d). Reduces probability; does not bound an in-flight DELETE, retry, or outage.
- ETag / `If-Match` / HEAD-before-DELETE. Byte-identical rematerialization makes the values equal.
- Cassandra TTL lock, GC lease, extra `ref_count`, re-read `blocks` immediately before DELETE. Cassandra cannot retract a request already accepted by the object store.
- Raise RF and keep LQ/LQ. `LOCAL_QUORUM` is per-DC; EU write and NA read still need not intersect.
- Quarantine-prefix Copy+Delete. The `Delete` of the live key has the same ABA (registry already corrected this). Note the registry still keeps quarantine as a *recovery affordance* — the bytes survive — which is a different claim from closing X1.
- Two-phase “wait then HEAD.” Same as grace plus ETag.

X3 (PUT before durable discoverable intent) is a storage leak, not live-data deletion. It is out of scope for this close.

---

## Publication race matrix (Option 1 must survive these)

Assume writers stay `LOCAL_QUORUM`, GC reads refs at `EACH_QUORUM`, claim commit is `SERIAL+ALL`, rematerialization mints a new `physical_id`.

| # | Sequence | Required outcome |
|---|---|---|
| R1 | EU `up:` acked, then NA GC EQ read | GC sees the ref; no DELETE |
| R2 | NA GC EQ sees 0, then EU `up:` acked, then writer fence check | Writer sees ALL fence, returns fence error, outer wrapper mints P2; GC DELETE names P1 only; `blocks` cleanup `IF physical_id=P1` |
| R3 | Writer stalled after Probe=`Reusable` (LQ, no fence yet), GC ALL-fences, EQ sees 0, DELETE P1, writer then stages `pub:` | Must not succeed as “pin P1.” The post-insert fence check on the `pub:` stage must fail closed and rematerialize P2, or the insert must be refused. `fs:` needs no separate check — it runs inside the promote, under a still-live fence-checked `pub:` row |
| R4 | GC EQ sees 0, claims, verify still 0, row dropped, delayed S3 DELETE, writer PUTs | P2 key ≠ P1 key; delayed DELETE cannot hit P2; orphan recovery must DELETE stored P1 key, never `hashToKey(block_id)`. **Note this row assumes the drop-row-first ordering — branch (b) of R8, not step 2's branch (a)** |
| R5 | Writer Probe misses a remote claim because claim still commits at LQ | **Fails Option 1.** Claim commit must be `ALL` (or equivalent global visibility) |
| R6 | `QUORUM` GC read (2 of 3), writer LQ in the DC not contacted | **Fails X2.** Forbidden |
| R7 | One DC down during GC EQ read | Fail closed; no DELETE |
| R8 | **GC keeps the P1 `blocks` row until the exact-key DELETE succeeds (step 2), and a concurrent writer must install P2** | `blocks` is one row per logical block and `UpsertBlockMetadata` is `INSERT … IF NOT EXISTS`, so first-writer-wins cannot install P2 over the live P1 row. Either specify the conditional `P1 → P2` update and what authorizes it, or move the exact-key fence out of `blocks`. **This is the decisive test:** answer (a) means a new `blocks` CAS with a predecessor condition; answer (b) means a table keyed by `(block_id, physical_id)`. Both are r3 components under other names, and either one moves this option out of “Medium” |

R3 and R8 are what decide the option. If R3 cannot be made true for `StagePublishAttemptReferences` without inventing use-rows, or R8 resolves into a new CAS or a second identity table, Option 1 is no longer smaller than a documented r3 subset.

---

## Recommended next step

Do **not** implement r3 yet.

Treat **Option 1** as the design to stress-test:

1. X1 = DB-owned never-reuse physical incarnation (exact key on GC work).
2. X2 = GC-only `EACH_QUORUM`/`ALL` liveness reads; writers stay `LOCAL_QUORUM`. This is the same X2 mechanism r3 uses; it is not where the two designs differ.
3. Publication = existing write-ref-then-check-fence, with a globally visible claim/orphan fence and P2 rematerialization, extended to the `pub:` stage.

**Answer R8 first.** It is cheap to answer on paper, it gates the invasiveness estimate, and if it resolves into a new `blocks` CAS or a `(block_id, physical_id)` table then the rest of the audit is moot — the option has already converged on r3's components and the real question becomes which documented r3 subset to take.

If R8 and R3 both hold, the implementation is a handful of schema/locator/GC/writer PRs on the current model, not a new distributed lifecycle. If they do not, the missing proof is the publication frontier, and r3 is the document that already contains it.

Two things are true regardless of which option wins, and can be scheduled independently:

- **GC drain capacity.** The serial worker loop cannot absorb per-block WAN LWTs at `batch_size=100` on a 30 s tick. Every option that fences globally needs this.
- **Measuring writer-side `EACH_QUORUM`** (Option 2), as a performance question. It is not required to close X2 and does not close X1.
