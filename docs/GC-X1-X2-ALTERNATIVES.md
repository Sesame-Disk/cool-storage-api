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

GC recovery is keyed by logical `block_id`: `gc_s3_orphans` primary key is `((org_id, block_id))`, and `BlockStoreDeleter` is `DeleteBlock(ctx, blockID)`. After `FinalizeBlockDelete` drops the `blocks` row, the remaining writer fence is that orphan row.

**The fence makes writers wait, and it clears on confirmation.** While the row exists `ProbeBlockReuse` answers `BlockedByGC`, but that is a *retryable* signal, not a failure: `retryUploadedBlockMaterialization` treats `ErrBlockDeleteInProgress` as retryable and retries up to `libraryHeadMutationRetryAttempts` (8) with backoff. Only an upload that still sees the fence after all eight attempts returns 409. The wrapper also accepts a `resolveFence` hook that could end the wait early, but it is inert in production: the only implementation, `clearSeafHTTPS3OrphanFence`, returns `(false, nil)` on every path and logs that the writer will back off and leave S3 cleanup to GC recovery. So the writer cannot *shorten* the wait — but it does not sit out the whole budget either: each retry re-runs the probe, and the upload proceeds on the first attempt that happens after GC cleared the orphan.

On the GC side the row is deleted as soon as the S3 delete is confirmed (`DeleteS3Orphan`, from both `processBlock` and `RecoverS3Orphans`); the 7776000 s (90 day) `default_time_to_live` is the worst-case ceiling for a delete that never succeeds, not the duration of the fence.

**That waiting narrows the X1 window but does not close it.** `deleteS3WithRetry` is strictly sequential, so there is no overlap between its own attempts. The residual case is the object store's, not the loop's: an attempt that **timed out client-side** can still be applied server-side, after a later attempt reported success and the orphan was cleared. The writer then resumes against a key that an already-accepted request will still delete. This is exactly the case the registry ruled on: waiting, locking, or re-reading Cassandra after the DELETE has been sent does not establish the property.

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

**This is worth stating precisely, because it is the load-bearing similarity between r3 and Option 1: r3 does not close X2 by making reference writes global.** It closes X2 with a global (`EACH_QUORUM`) liveness read on the GC side, writers left local — the same mechanism Option 1 uses, and the same one Options 3 and 4 would need. What r3 buys with its extra machinery is not X2; it is the publication frontier, which is a different property (see *What is not X2*).

Option 2 is the exception and the reason not to write "every smaller option": it closes X2 from the writer side instead, by making reference writes themselves `EACH_QUORUM`, at which point a `LOCAL_QUORUM` GC read would already intersect. That is a genuinely different mechanism with a different cost profile, evaluated on its own below.

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
- If a DC is unavailable, the read fails. GC must fail closed (no DELETE) — the standing policy the ADR states as *"when in doubt, do not delete"* and *"never orphan and never delete on an uncertain read"*. That is acceptable on **latency** grounds — GC is not the hot path. It is not automatically acceptable on **throughput** grounds; see the drain-capacity point under Option 1.

### The zero-check is asymmetric, and only one direction needs WAN

Proving **"at least one reference exists"** takes a single positive answer from any replica. Proving **"zero references exist"** takes an answer from all of them. That asymmetry is a direct consequence of the fail-closed policy, and it means the two `BlockHasReferences` calls in `processBlock` do not need the same level:

- **Pre-claim check:** `LOCAL_QUORUM` suffices. A row seen locally is a real reference, so aborting is correct and no delete is authorized. A local zero proves nothing and simply does not stop the walk.
- **Claim-then-verify:** must be `EACH_QUORUM`. This is the read that authorizes destruction, and it is the one X2 is about.

This never weakens fail-closed — a local zero authorizes nothing at either step. It has two practical effects: it halves the WAN reads for candidates that turn out to be re-referenced (a case `processBlock` handles explicitly), and it lets the worker keep classifying and deferring work while a DC is unreachable instead of stalling on the first read. Whether that is worth specifying depends on the re-reference rate, which is unmeasured.

```text
EU writer: INSERT ref @ LOCAL_QUORUM     (durable on EU)
NA GC:     SELECT refs @ EACH_QUORUM     (must contact NA, EU, Asia)
           EU replica returns the row
           GC cannot authorize delete
```

This is the X2 mechanism of choice for a small design. It does **not** put WAN on `up:` / `pub:` / `fs:` inserts.

### `QUORUM` (non-LOCAL) is not enough

The deployment topology is **three** DCs — NA, EU and Asia — at RF=1 each. Writer `LOCAL_QUORUM` in EU: only the EU replica holds the row. A `QUORUM` read needs 2 of 3 replicas and may be satisfied by NA+Asia. X2 remains open. **GC liveness must be `EACH_QUORUM` or `ALL`, not `QUORUM`.**

> **Do not read the DC count out of `configs/`.** The versioned cluster profiles declare `replication_dcs: {usa: 1, eu: 1}` — two DCs, no Asia — and they are **correct as they stand**: they belong to the `docker-compose.mr-cluster.yaml` harness, which pins `CASSANDRA_REPLICATION_DCS=usa:1,eu:1` for both nodes. Rewriting them to the production map would break that harness. Production is `docker-compose.prod.yml`, which pins no DC map at all and takes it from the environment; `.env.prod.example` sets `CASSANDRA_REPLICATION_DCS=dc-na:1,dc-eu:1,dc-asia:1`. What the profiles lack is only a pointer saying production lives elsewhere. The operative warning is the one that matters for X2: anyone sizing a quorum argument from `configs/` gets 2 of 2, where `QUORUM` looks sufficient, instead of the real 2 of 3, where it is not.

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
| 1. DB physical incarnation + GC-only global liveness + reuse upload-fence | Closed if keys never reused and GC deletes the exact key | Closed if the claim-then-verify read is `EACH_QUORUM`/`ALL` | Closed only if the fence is globally visible and the `pub:` stage grows a post-insert fence check | Medium under R8 (c), still well below r3 under (d), higher under (a)/(b) | **Recommended investigation** |
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

- `BlockHasReferences` at `EACH_QUORUM` (or `ALL`) on the **claim-then-verify** read, which is the one that authorizes destruction. The pre-claim check may stay `LOCAL_QUORUM` (see the asymmetry above); raising both is simpler and also correct.
- Fail closed if any DC is unavailable.
- Session default stays `LOCAL_QUORUM`. Set the level per query, the same rule r3 already states for global operations.

**Publication TOCTOU.** EQ reads do not close “writer publishes after GC observed 0.” Reuse upload-fence ordering, and make the **fence** visible to every later LQ writer read:

1. Claim at serial phase `SERIAL`, regular commit `ALL` (so `gc_state='deleting'` is on every replica). Today `ClaimBlockDelete` sets neither level: it inherits the session regular level (LQ) *and* the session serial level, which the shipped multi-DC profiles set to `LOCAL_SERIAL`. Both have to be set per statement — and not only for this one statement, see the serial-domain rule below (**R12**).
2. Either do not drop the `blocks` row before the exact-key DELETE succeeds, **or** keep today's drop-first ordering and make the orphan/fence globally visible and keyed by `physical_id` + exact key. Today `FinalizeBlockDelete` removes the row first; `gc_s3_orphans` is LQ and logical. **These branches are not interchangeable and they are what the P1→P2 handover below has to choose between.**
3. Writers that observe the fence **must not pin doomed P1**. What they do instead is branch-dependent and is the single sharpest difference between (c) and (d): under **(c)** the writer waits, does no PUT and mints nothing while the fence stands, and only mints P2 once the fence is gone; under **(d)** it mints P2 and PUTs K2 immediately. Reading "mint P2 on seeing the fence" as unconditional is reading (d) into (c).
4. The `pub:` stage follows write-then-check-fence, as `RegisterUploadedBlock` already does. `RegisterFSObjectBlockReferences` does **not** need its own check: it only ever runs inside `PromotePublishAttemptReferences` (all three call sites — `seafhttp.go`, `sync.go`, `fs_helpers.go`), i.e. after the HEAD CAS and while this attempt's `pub:` rows are still live, so `fs:` inherits liveness from a `pub:` row that was itself fence-checked. The gap is one function, not two.

**The open question: who moves `blocks` from P1 to P2, and with what CAS.**

This is the part that decides whether Option 1 is actually smaller than r3.

- `blocks` has primary key `((org_id, block_id))` — **one row per logical block**. P1 and P2 cannot coexist in it.
- `UpsertBlockMetadata` is `INSERT ... IF NOT EXISTS`, deliberately first-writer-wins.
- Today a writer can re-insert *only because* `FinalizeBlockDelete` deleted the row first.
- Step 2's first branch proposes not deleting the row until the exact-key DELETE succeeds — which forbids exactly the ordering the current re-insert depends on.

Three resolutions, not two:

**(a) GC keeps the P1 row.** The writer minting P2 needs a *conditional update* `P1 → P2` on a row GC has claimed, gated on the delete's authority. That is a new CAS on `blocks` with a predecessor condition — structurally the same object as r3's `RETIRED/GC_RETIRE → ACTIVE` activation.

**(b) GC drops the row first, and the fence becomes a general multi-generation record.** If arbitrarily many physical lives of one logical block may be pending delete at once, the durable exact-key fence needs its own table keyed by `(block_id, physical_id)`. That **is `block_generations` under another name**.

**(c) Sequential lives: GC drops the row first, and the writer keeps waiting.** This is today's protocol plus an identity, and it is the smallest branch:

```text
orphan(P1, K1) durable  →  drop blocks row  →  DELETE K1  →  clear orphan
                                                                  ↓
                                            writer wakes, mints P2, INSERT IF NOT EXISTS
```

P1 and P2 **never coexist**. The writer already waits on the fence (see X1 above), and by the time it wakes there is no `blocks` row and no orphan row, so a plain first-writer-wins `INSERT` installs P2 with no conditional transition and no second table. `gc_s3_orphans` grows `physical_id` and the exact key, which the inventory below already lists.

Because the lives are serialized, most of the machinery people expect here is simply not needed: `ProbeBlockReuse` keeps its current meaning (an orphan blocks the logical block, because there is no live successor to distinguish it from), no "one outstanding delete" invariant has to be stated (it is structural), and R11 cannot fire, since P1's mapping cleanup runs while the writer is still fenced.

**(d) Overlapped lives: the writer installs P2 while P1 is still being deleted.** Same drop-first ordering, but the writer stops waiting. This buys upload availability — no user-visible stall behind a slow S3 delete — and it is a genuinely different design with a real price:

- **The orphan row changes meaning**, from "logical block ABC is blocked" to "physical incarnation P1 is being deleted". `ProbeBlockReuse` must compare `physical_id` and stop answering `BlockedByGC` for a live P2.
- **"At most one outstanding physical delete per logical block" becomes a stated invariant**, not a structural fact, because `gc_s3_orphans` is still one row per logical block. While P1 pends, P2 is live and usable but **not GC-eligible**.
- **R11 becomes load-bearing.** P1's `cleanupBlockMapping` can now run while P2 owns the logical mapping, and `processBlock` has no resurrection guard.

(d) is still far short of r3 — one previous life, not a lineage — but it is no longer "the current protocol plus a column".

**What both branches share:**

- **The orphan TTL has to go, and the reason is not X1.** `default_time_to_live = 7776000` lets a persistently failing delete have its fence expire silently after 90 days. X1 stays closed even then — with no `blocks` row the writer probes `NeedsPut`, mints a fresh `physical_id` and PUTs K2, and a stale DELETE of K1 can never reach it. What expiry destroys is the **durable record that K1 still has to be deleted**: the bytes leak with nothing pointing at them, and under (c) the "lives are sequential" property silently becomes untrue, which is what made R11 unreachable in the first place. So the row must not expire on its own: no TTL, or expiry that raises an alert instead of deleting the row.

- **Removing the TTL is four changes, not one.** The cold-start recovery horizon is deliberately pinned to it: `gcS3OrphanInitialScanLookbackDays = 90`, whose comment says to "match the `gc_s3_orphans` / `gc_s3_orphans_by_day` TTL so the first pass can still see every live orphan row". Drop the TTL and leave the horizon alone and any orphan older than 90 days becomes permanently invisible after a cursor loss — precisely the case where the orphan matters most. The package is: remove the canonical TTL, remove the `_by_day` projection TTL, redefine the cold-start horizon and cursor semantics, and guarantee discovery of an arbitrarily old orphan.
- **Neither removes the need for never-reused keys.** They settle the handover question, not X1. A DELETE that timed out client-side can still complete on the object store after a later attempt reported success and the orphan was cleared, which is why P2 needs its own key.

So R8 is an open question, **not** a demonstration that the option has converged on r3: (c) keeps it Medium, (d) keeps it well below r3, and only (a)/(b) push it up. **R8 in the race matrix is the test that settles it, and (c) is the branch to price first.**

**Whichever branch wins, P2 selection needs a global serial domain.** `UpsertBlockMetadata` sets no serial level, so like `ClaimBlockDelete` it inherits `LOCAL_SERIAL` on the shipped multi-DC profiles. Today that is harmless *only because the key is derived*: two DCs that each win their own local Paxos round write the identical key. Mint a UUID `physical_id` and that stops being true — EU installs `ABC.uuid-eu`, NA installs `ABC.uuid-na`, and one of them wins the row. This is **R9**, and it is why r3 raises the existing first-writer LWT to `SERIAL` + regular `QUORUM` "when it can touch a generation-managed `blocks` partition".

Note what `SERIAL` does and does not buy here. It makes exactly one incarnation **canonical**. It does not prevent the losing writer from having already PUT its object, because the PUT happens before the LWT — so R9's required outcome is "one canonical incarnation", not "no stray bytes". The stray object is an X3-class leak, but a worse one than today's: with derived keys an orphaned PUT can be reconciled with a single HEAD, because the key is recomputable from the `block_id`. A minted key that lost the LWT is recorded **nowhere** — no `blocks` row, no orphan row — so reconciling it needs a full bucket inventory, which does not exist today. Key minting therefore turns physical reconciliation from a point lookup into a scan, and that belongs in Option 1's price rather than in the out-of-scope pile.

**And the serial domain cannot be raised statement by statement (R12).** Mixing `LOCAL_SERIAL` and `SERIAL` on the same partition does not merely look inconsistent — it breaks linearizability, because they are different quorum domains and a `LOCAL_SERIAL` round can fail to observe an in-flight `SERIAL` proposal. One statement left behind invalidates the guarantee for all the others. There are **six** conditional statements on the `blocks` partition today, and all six inherit the session serial level:

| LWT | Where |
|---|---|
| `ClaimBlockDelete` | `store_cassandra.go` |
| `ReleaseBlockClaim` | `store_cassandra.go` |
| `FinalizeBlockDelete` | `store_cassandra.go` |
| `UpsertBlockMetadata` (first-writer) | `block_references.go` |
| Stub repair claim | `block_references.go` |
| `deleteRepairClaimedBlockStubFn` | `block_references.go` |

The rule is r3's, and it already has a name there — the ADR calls it the **one-serial-domain rule** and scopes it to `blocks`. Any smaller design keeps it: *every* conditional statement that can touch `blocks` uses serial phase `SERIAL`, with the regular commit level chosen per statement (`ALL` for the claim fence, `QUORUM` or higher for the first-writer/P2 install, decided per statement for release and finalize). This is one of the disciplines worth keeping even if the rest of r3 is discarded.

**What this deliberately does not add** (holds under (c) and (d); (a) adds one CAS, (b) forfeits the claim). `block_generations`, `block_generation_uses`, generation-bound refs, retirement evidence, publication-frontier protocol, quarantine/abort workflows.

**Pros.**

- X1 by construction (old DELETE cannot name new key).
- X2 for completed refs without hot-path WAN.
- Reuses `up:` / `pub:` / `fs:`, claim-then-verify, rematerialize wrapper.
- Bounded schema under (c) or (d): `physical_id` + exact key on `blocks`, `gc_s3_orphans` and its by-day projection — no new table, no new CAS. `gc_block_candidates` probably does not need it either, since `processBlock` reads `GetBlockInfo` at claim time, after the candidate was enqueued.
- Canonical reader change is real but local: persisted key becomes authoritative, with validation (org, hash, format) instead of “derived key or reject.”

**Cons / residual holes (these decide whether the option stays small).**

- **The P1→P2 handover is unresolved** (above). This is the largest one; everything below is secondary to it. Branch (c) is the one that keeps the option small, and it carries its own conditions — reinterpreted orphan semantics, the single-outstanding-delete invariant, and removing the orphan TTL.
- **P2 selection must linearize globally, and so must every other `blocks` LWT** (R9, R12). Inheriting `LOCAL_SERIAL` on `UpsertBlockMetadata` is survivable only while keys are derived; with a minted `physical_id` it lets two DCs install different incarnations. And raising only the statements this document names is not enough: six conditional statements touch that partition, and one left at `LOCAL_SERIAL` breaks linearizability for all of them.
- **Minted keys make physical reconciliation a scan** rather than a point lookup, because a key that lost the install LWT is recorded nowhere. That is an X3-class leak, but it is strictly worse than today's and belongs in this option's price.
- **The `Reusable` repair PUT is a second X1 surface** (R10), not just the rematerialization path. A writer stalled after a `Reusable` probe can resume into `EnsureReusableBlockPresent` and repair-PUT the old key under an authorized delete.
- **Logical mapping cleanup can outlive the incarnation that owned it** (R11). `cleanupBlockMapping` is keyed by the logical SHA-1, so a P1 cleanup running after P2 exists deletes a mapping P2 now owns. `RecoverS3Orphans` already guards this with a `BlockExists` check before cleanup; `processBlock` does not — safe today only because the orphan fence prevents resurrection outright, which is what branch (d) gives up. Under (c) the guard is worth adding defensively, not because the race is reachable.
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
| `CheckBlocksExist` (canonical reader, primary `/check-blocks` path) and `CheckBlocks` / `CheckBlocksParallel` (legacy fallback) | Both HEAD a key derived from the hash | Dedup oracle must resolve the live incarnation from the DB, not from a derived key |
| `S3Store.Put(ctx, blockID, …)` | Second derivation layer: `s.key(blockID)` on what callers already pass as a key | Make the key parameter mean a key |
| `canonical_block_reader.go` | Rejects persisted ≠ derived | Use persisted key; validate org/hash/format |
| `upload_reuse.go` — `ResolveNeedsPutBlockStore` **and** the `Reusable` branch of `StoreUploadedBlockForProbe` | Two reject sites; the second also repair-PUTs at the derived key | Mint `physical_id` on `NeedsPut` / `RepairableStub` / fence; PUT that key |
| `UpsertBlockMetadata` | `INSERT … IF NOT EXISTS` inheriting session serial (`LOCAL_SERIAL` in the cluster profiles) | Store `physical_id` + exact key; raise the serial phase to `SERIAL` so one incarnation wins globally (R9); and see the P1→P2 handover — first-writer-wins cannot install P2 over a live P1 row |
| `cleanupBlockMapping` | Deletes the logical SHA-1 mapping unconditionally; only `RecoverS3Orphans` guards with `BlockExists` | `processBlock` needs the same resurrection guard once an orphan no longer blocks the whole logical block (R11) |
| `EnsureReusableBlockPresent` | Repair-PUTs the derived key when a `Reusable` object is missing | Revalidate the fence immediately before the repair, or mint P2 instead of repairing P1 (R10) |
| `ClaimBlockDelete` | LWT inheriting session regular (LQ) and session serial (`LOCAL_SERIAL` in the cluster profiles) | Set both per statement: `SERIAL` + regular `ALL` |
| `ReleaseBlockClaim`, stub repair claim, `deleteRepairClaimedBlockStubFn` | Conditional statements on `blocks`, inheriting session serial | Serial phase `SERIAL` too — the one-serial-domain rule admits no exceptions on this partition (R12) |
| `gcS3OrphanInitialScanLookbackDays = 90` | Cold-start recovery horizon, deliberately matched to the orphan TTL | Redefine together with the TTL removal, or old orphans become undiscoverable after a cursor loss |
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
| R2 | NA GC EQ sees 0, then EU `up:` acked, then writer fence check | Writer sees the ALL fence and returns a fence error. Under (c) the wrapper retries and mints P2 only after the fence clears; under (d) it mints P2 at once. Either way GC's DELETE names P1 only, and `blocks` cleanup is `IF physical_id=P1` |
| R3 | Writer stalled after Probe=`Reusable` (LQ, no fence yet), GC ALL-fences, EQ sees 0, DELETE P1, writer then stages `pub:` | Must not succeed as “pin P1.” The post-insert fence check on the `pub:` stage must fail closed — retrying to a later P2 under (c), rematerializing to P2 under (d) — or the insert must be refused. `fs:` needs no separate check: it runs inside the promote, under a still-live fence-checked `pub:` row |
| R4 | GC EQ sees 0, claims, verify still 0, row dropped, delayed S3 DELETE, writer PUTs | P2 key ≠ P1 key; delayed DELETE cannot hit P2; orphan recovery must DELETE stored P1 key, never `hashToKey(block_id)`. **Note this row assumes the drop-row-first ordering — R8 branches (b), (c) or (d), not (a)** |
| R5 | Writer Probe misses a remote claim because claim still commits at LQ | **Fails Option 1.** Claim commit must be `ALL` (or equivalent global visibility) |
| R6 | `QUORUM` GC read (2 of 3), writer LQ in the DC not contacted | **Fails X2.** Forbidden |
| R7 | One DC down during GC EQ read | Fail closed; no DELETE |
| R8 | **P1→P2 handover: who installs P2, and with what CAS** | `blocks` is one row per logical block and `UpsertBlockMetadata` is `INSERT … IF NOT EXISTS`, so first-writer-wins cannot install P2 over a live P1 row. Pick a branch: **(a)** keep the P1 row → new conditional `P1 → P2` CAS; **(b)** drop-first with many concurrent pending deletes → a `(block_id, physical_id)` table, i.e. `block_generations`; **(c)** drop-first, writer keeps waiting → lives are sequential, plain `INSERT` after the wait, no new CAS and no new table; **(d)** drop-first, writer installs P2 while P1 is deleting → buys upload availability, costs the orphan reinterpretation, the single-outstanding-delete invariant and R11. Price (c) first |
| R9 | Writers in EU and NA both leave the fence wait and both mint a `physical_id` for the same logical block | Exactly one incarnation becomes **canonical**. `UpsertBlockMetadata` inherits `LOCAL_SERIAL`, so both local Paxos rounds can apply — harmless while keys are derived, **not** harmless once each DC mints its own key. The installing statement must use a `SERIAL` serial phase. The loser's already-PUT object is an explicit leak/recovery concern (its minted key is recorded nowhere, so reconciliation needs a bucket inventory) and must never become authoritative — but `SERIAL` does not prevent it, and this row must not pretend otherwise |
| R10 | Writer stalls after Probe=`Reusable(P1)`; GC fences, EQ sees 0, authorizes DELETE K1; writer resumes, finds the object missing, and repair-PUTs | Must not re-PUT K1 under an authorized delete — that is X1 again through `EnsureReusableBlockPresent` rather than through rematerialization. Either revalidate the fence immediately before the repair PUT and refuse when it stands (under (c) the writer then waits for a later life), or never repair a fenced incarnation and mint P2 instead (under (d)) |
| R11 | P1 delete completes, P2 is created and live, then P1's `cleanupBlockMapping` runs | The logical SHA-1→SHA-256 mapping now belongs to P2 and must survive. `RecoverS3Orphans` already guards this with `BlockExists` before cleanup; `processBlock` has no equivalent check and is safe today only because the orphan fence blocks resurrection entirely. **Load-bearing under R8 branch (d), which removes that protection; preventive under (c), where the lives never overlap** |

| R12 | Any conditional statement on the `blocks` partition still runs at `LOCAL_SERIAL` after the others are raised to `SERIAL` | **Fails the whole fence.** The two levels are different quorum domains, so a `LOCAL_SERIAL` round can miss an in-flight `SERIAL` proposal and one straggler invalidates every other statement's guarantee. All six of today's `blocks` LWTs — `ClaimBlockDelete`, `ReleaseBlockClaim`, `FinalizeBlockDelete`, `UpsertBlockMetadata`, the stub repair claim and its conditional delete — must use serial phase `SERIAL`, with regular commit chosen per statement. This is r3's own one-serial-domain rule and survives any subsetting |

R3 and R8 decide whether the option is viable; R9, R10, R11 and R12 decide whether it is correct once it is. If R3 cannot be made true for `StagePublishAttemptReferences` without inventing use-rows, or R8 falls to branch (a) or (b), Option 1 is no longer meaningfully smaller than a documented r3 subset.

---

## Recommended next step

Do **not** implement r3 yet.

Treat **Option 1** as the design to stress-test:

1. X1 = DB-owned never-reuse physical incarnation (exact key on GC work).
2. X2 = GC-only `EACH_QUORUM`/`ALL` liveness reads; writers stay `LOCAL_QUORUM`. This is the same X2 mechanism r3 uses; it is not where the two designs differ.
3. Publication = existing write-ref-then-check-fence, with a globally visible claim/orphan fence and P2 rematerialization, extended to the `pub:` stage.

Gates, in the order that makes the cheapest ones decide first:

1. **R8 — P1→P2 handover.** Price branch (c) first: drop-row-first as today, exact key on the orphan, writer keeps waiting, lives stay sequential. It needs no new CAS and no new table. Its price is the TTL package below. Take (d) — overlapped lives — only if the upload stall behind a slow delete proves unacceptable, and price the orphan reinterpretation, the single-outstanding-delete invariant and R11 with it.
2. **R12 — one serial domain on `blocks`.** Cheapest to settle and it gates the rest: all six conditional statements move to `SERIAL`, or none of the fencing holds.
3. **R9 — cross-DC P2 install.** One incarnation becomes canonical; the losing PUT is a leak to be classified, not something `SERIAL` prevents.
4. **R10 — stalled `Reusable` repair PUT.** Never re-PUT a fenced key.
5. **R11 — mapping cleanup after resurrection.** P1 cleanup must not destroy P2's logical mapping.
6. **R3 — `pub:` write-then-check-fence.** A commit must not pin doomed bytes.
7. **The TTL package.** Canonical TTL, `_by_day` TTL, cold-start horizon and cursor semantics move together.
8. **Physical reconciliation.** Minted keys need a bucket-inventory story that derived keys never did.
9. Then measure GC drain (below).

If R8 lands on (c) and R3 holds, the implementation is a handful of schema/locator/GC/writer PRs on the current model, not a new distributed lifecycle. If R8 falls to (a) or (b), the missing proof is the publication frontier, and r3 is the document that already contains it — do not invent a third protocol.

Two things are true regardless of which option wins, and can be scheduled independently:

- **GC drain capacity.** The serial worker loop cannot absorb per-block WAN LWTs at `batch_size=100` on a 30 s tick. Every option that fences globally needs this.
- **Measuring writer-side `EACH_QUORUM`** (Option 2), as a performance question. It is not required to close X2 and does not close X1.
