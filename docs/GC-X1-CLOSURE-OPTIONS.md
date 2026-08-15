# X1 — Closure Options for the Physical-Delete ABA

**Status:** Analysis and option comparison. **No option is accepted. Nothing is
implemented. This is not an ADR.**

Even a future verified X1 implementation would not enable destructive GC by itself;
removing the fleet-wide `GC_ENABLED=false` gate is a separate activation change after
the closure evidence and operational rollout checks pass.

**Date:** 2026-08-14 · Supersedes `GC-X1-X2-ALTERNATIVES.md` (2026-08-13), whose X2
half is now history and whose X1 half is carried forward here, corrected and extended.

**Scope:** `ISSUE-GC-UPLOAD-FENCE-REMATERIALIZATION-01` (X1), the sole remaining
runtime blocker for destructive GC.

**Related:**

- [KNOWN_ISSUES.md](./KNOWN_ISSUES.md) — status of record
- [UPLOAD-FENCE-FINDINGS-REGISTRY.md](./UPLOAD-FENCE-FINDINGS-REGISTRY.md) — X1/X2 wording, the closed F-series, and the X list
- [GC-X2-MULTIDC-VALIDATION.md](./GC-X2-MULTIDC-VALIDATION.md) — X2's runbook and closure findings
- [GC-SERVICE-ANALYSIS.md](./GC-SERVICE-ANALYSIS.md) — current GC behaviour

> **Destructive GC stays disabled.** `GC_ENABLED=false` on **every replica in every
> DC**, the wording `DEPLOY.md` and `config.prod.yaml` use, until X1 closes. The
> single-node local Compose stack deliberately does not pin the variable; that
> exemption is documented in `docker-compose.yaml` and is not a policy gap.

---

## What changed on 2026-08-14, and why this document exists

Two things settled at once.

**X2 closed, without generations.** `ISSUE-GC-CROSS-DC-REFERENCE-VISIBILITY-01` was
fixed by pinning the read that *authorizes destruction* to `EACH_QUORUM`
(`BlockHasReferencesGlobal`, `internal/gc/store_cassandra.go:1852`) behind a topology
gate (`ValidateDestructiveGCTopology`, in the `GCStore` interface at
`internal/gc/store.go:115`), with reference writers left at `LOCAL_QUORUM` and pinned
per statement. Evidence: five legs green on a real three-DC cluster, both mutations red.
See the registry's X2 row.

**The generational-fence protocol (r3) was abandoned.** It was drafted on
`docs/gc-x1-x2-generation-fence-final` and proposed in PR #166, which closed unmerged.
That branch is kept as investigative reference only; **no part of it is a decision of
record**, and neither the ADR nor its PR-1…PR-8 plan should be cited as accepted. Its
material contribution — that the physical-delete ABA component closes only when new
physical incarnations use never-reused physical keys — is preserved here without the
surrounding lifecycle protocol.

X2's closure separated the cross-DC liveness problem from the publication TOCTOU. r3 kept
ordinary reference writes at `LOCAL_QUORUM` and therefore proposed a publication frontier
to prove that no new reference could appear against a generation being retired. That
frontier remains one possible X1 solution; X2 did not make it unnecessary. What X2 removes
is the need to use a generation lifecycle to solve *reference visibility*: the shipped
`EACH_QUORUM` GC-side read handles that half without a writer hot-path round trip.

The smaller option below tries to replace the publication frontier with a narrower
combination: globally visible claims, validation of the canonical physical incarnation
after reference writes, and never-reused physical keys. Whether that combination is
enough is the X1 question; it is not settled by X2.

This document therefore asks one question: **what is the smallest correct way to give
each physical incarnation of a block an identity that a stale DELETE cannot name?**

---

## X1 — verified problem statement

The physical object key is derived from the logical content hash:

```text
blocks/<org_id>/<hash[0:2]>/<hash[2:4]>/<hash>
```

`BlockStore.hashToKey`, `internal/storage/blocks.go:309-317`. GC deletes by re-deriving
that key: `processBlock` → `deleteS3WithRetry` → `BlockStore.DeleteBlock(blockID)`
(`internal/storage/blocks.go:289`) → `hashToKey` again. `S3Store.Delete` issues a
key-only `DeleteObject` with no `VersionId`.

```text
GC determines refs = 0
        ↓
authorizes DELETE of blocks/.../ABC123
        ↓
DELETE is slow / retrying / already on the wire

                   writer rematerializes ABC123
                   PUT the same key
                   new logical reference
                         ↓
                   old DELETE lands
                         ↓
                   live bytes are gone
```

Cassandra claim state cannot revoke an S3 request already accepted by the object store.
Because blocks are content-addressed the new bytes are byte-identical to the old ones,
so ETag, `If-Match` or HEAD cannot tell the two lives apart.

`blocks.storage_key` already exists as a column
(`internal/db/migrations/001_initial_schema.cql:277`) but it is **not** the locator.
Three production sites derive the key and **reject** a persisted key that differs:

| Site | File |
|---|---|
| Canonical read path | `internal/streaming/canonical_block_reader.go:238` |
| `ResolveNeedsPutBlockStore` | `internal/api/v2/upload_reuse.go:142` |
| `Reusable` branch of `StoreUploadedBlockForProbe` | `internal/api/v2/upload_reuse.go:159` |

That third one is a second X1 exposure distinct from rematerialization: when the probe
says `Reusable` but the object is missing, it issues a **repair PUT at the derived key**
(`repairCanonicalBlockDirectFn`, `internal/api/v2/upload_reuse.go:172`) with no fence
re-check. Tracked below as **R10**.

Note what those three sites mean for any minted-key design: today "persisted key ≠
derived key" is a **fail-closed corruption signal**. Minting keys inverts it. That is
not a locator refactor; it is replacing an invariant with a different one (validate org,
hash prefix and format instead of exact equality), and it must be done deliberately.

**Minimum property:**

> A DELETE authorized for the old physical life of a block must never be able to delete
> the new physical life.

That requires distinct physical identities. Waiting, locking, or re-reading Cassandra
after the DELETE has been sent does not establish it.

### The premise that makes every option below viable

`L = sha256.Sum256(storedContent)` where `storedContent` is the byte sequence actually
written to the object store — **after** encryption for encrypted libraries
(`internal/api/seafhttp.go:2468-2482`; the same shape at `sync.go:1894`,
`v2/blocks.go:927`, `v2/files.go:3428`, `v2/onlyoffice.go:1181`).

Therefore **any two objects with the same `L` are byte-identical by construction.** Two
physical incarnations of one logical block are interchangeable to every reader. This is
what makes it unnecessary to bind references to a physical life: a reference means
"content `L` is live", and any live incarnation of `L` satisfies it.

This premise is load-bearing for the recommended safety baseline. If block IDs ever stop being a
hash of the stored bytes, the option below has to be re-derived.

---

## What X2's closure settled, and what it did not

Settled, and reusable by any X1 design:

- **A global liveness read exists and is gated.** `BlockHasReferencesGlobal` at
  `EACH_QUORUM`, used by `processBlock`'s claim-then-verify and by `RecoverS3Orphans`,
  refusing to run at all unless `ValidateDestructiveGCTopology` passes.
- **Reference writers are pinned.** `BlockReferenceWriteConsistency = gocql.LocalQuorum`
  (`internal/db/block_references.go:972`), set per statement by both producers, with an
  AST scan (`TestBlockReferenceProducersPinWriteConsistency`) that fails on a third
  producer that forgets. The level did not change; the pin did.
- **The zero-check is asymmetric.** A locally visible reference is proof the block is
  alive; a local zero authorizes nothing. So the pre-claim check, the scanner and
  `enqueueZeroRefBlocks` legitimately stay at session consistency, and only the
  authorizing read pays WAN.

Not settled by X2, and squarely X1's problem:

- **Physical identity.** Nothing about `EACH_QUORUM` stops a stale DELETE from naming
  the key a new upload just wrote.
- **The publication TOCTOU.** GC reads zero, a writer publishes afterwards. Globalizing
  the GC read does not close it. It is not X2 and must not be smuggled into it.

### `QUORUM` is not enough, and this is now empirical

Production is three DCs — NA, EU, Asia — at RF 1 each
(`.env.prod.example`: `CASSANDRA_REPLICATION_DCS=dc-na:1,dc-eu:1,dc-asia:1`). A writer's
`LOCAL_QUORUM` in EU puts the row on the EU replica only; a non-local `QUORUM` read needs
2 of 3 and can be satisfied by NA+Asia. The three-DC harness confirms it: mutating
`EACH_QUORUM→QUORUM` turns leg 2b red.

> **Do not read the DC count out of `configs/`.** The versioned cluster profiles declare
> `replication_dcs: {usa: 1, eu: 1}` — two DCs, no Asia — and they are correct as they
> stand: they belong to the `docker-compose.mr-cluster.yaml` harness. Anyone sizing a
> quorum argument from `configs/` gets 2 of 2, where `QUORUM` looks sufficient, instead
> of the real 2 of 3, where it is not.

### `EACH_QUORUM` vs `ALL`

On RF 1 per DC they are equivalent — a quorum of 1 in each of three DCs is every replica.
Prefer `EACH_QUORUM` for a cross-DC writer-visible fence: it reaches a quorum in every DC
without requiring every replica when RF>1. The serial phase of the LWT is separate and
should be `SERIAL`; the regular commit can be `EACH_QUORUM`, while writer fence reads use
an explicit `LOCAL_QUORUM`. `ALL` remains a stricter policy choice, not a requirement for
intersection with a `LOCAL_QUORUM` writer read.

---

## What upload-fence already gives

PR-1…PR-10 closed F1–F14. X1 was explicitly out of scope. The series is reusable
substrate, not a close.

| Mechanism | Where | What it gives X1 | What it does not |
|---|---|---|---|
| Provisional `up:` written **before** metadata | `RegisterUploadedBlock`, `internal/api/v2/fs_helpers.go:989` | GC's claim-then-verify can see an in-flight upload | PUT still happens before `up:` (X3 leak) |
| Write-ref-then-check-fence | same helper, `:996-1003` | A writer that sees the fence rematerializes instead of reporting success | Uses the **same derived key** |
| Claim-then-verify | `processBlock`, `internal/gc/worker.go:1039` | A ref that landed before verify aborts the delete | Nothing about the physical key |
| Outer store→materialize→confirm | `retryUploadedBlockMaterialization`, `internal/api/v2/upload_reuse.go:230` | Re-PUT after a fence that cleared mid-cycle | Re-PUT uses the same derived key |
| `BlockDeleteFenceActive` | `gc_state='deleting'` **or** a `gc_s3_orphans` row | Current code makes any orphan a logical fence; B must make the fence key-aware: block while the canonical row still names the orphaned key, but allow `K2` after the row is gone | Orphan row is keyed by logical `block_id`; recovery deletes by derived hash |
| `pub:` then `fs:` | `StagePublishAttemptReferences` / `PromotePublishAttemptReferences` | Publish-attempt liveness across the HEAD CAS | **No** post-insert fence check on the `pub:` stage (unlike `RegisterUploadedBlock`) — tracked as **R3** |

`ProbeBlockReuse` (`internal/db/block_references.go:864`) classifies
`Reusable` / `NeedsPut` / `BlockedByGC` / `RepairableStub` / `UnknownError` from
`LOCAL_QUORUM` reads of `blocks`, references and orphans. Note its precedence on the
row-exists path, which matters for every option below:

```go
if activeClaim || repairClaim { return BlockedByGC }   // :913
...
if hasOrphan     { return BlockedByGC }                // :927  ← wins over Reusable
if hasReferences { return Reusable    }                // :931
return NeedsPut                                        // :934
```

Two observations that are easy to get backwards:

- **Zero references does not mean "mint a new incarnation".** A row with no references
  is a GC candidate, so the probe answers `NeedsPut` and the writer may need to
  materialize the bytes. If a healthy canonical row still exists, that materialization is
  a repair/reuse of its current `storage_key`; minting a new key while `blocks(L)` still
  exists would lose the `INSERT ... IF NOT EXISTS` race and leave the new object orphaned.
  A fresh key is minted only when a new physical incarnation is actually born, normally
  after the canonical row is absent.
- **`hasOrphan` outranks `hasReferences`.** This is the only thing that stops a writer
  from reusing an incarnation whose bytes are already condemned but whose `blocks` row
  outlived the authorization. Any design that stops treating an orphan as a fence must
  replace this, not simply delete it. See **R13**.

---

## The measured cost of waiting — the number that decides the design

The current protocol makes the writer wait behind the physical delete. Both sides of that
wait are constants in the repo, so this needs no measurement campaign.

| Quantity | Value | Source |
|---|---|---|
| Writer retry budget | 8 attempts, 50 ms doubling to a 400 ms ceiling ⇒ **≈1.95 s** of total backoff | `internal/api/v2/fs_helpers.go:680-684` |
| GC inline S3 delete | 4 attempts, 100 ms / 500 ms / 2 s ⇒ **≈2.6 s** of backoff plus S3 latency | `internal/gc/worker.go:352-356` |
| Orphan recovery cadence | Scanner **Phase 16**, on the scanner ticker: **24 h** in production, plus once at startup and on manual trigger | `internal/gc/scanner.go:138,1437`; `internal/gc/gc.go:690-706`; `configs/config.prod.yaml:386` |
| Orphan row ceiling | `default_time_to_live = 7776000` (90 days) | `001_initial_schema.cql:1248,1263` |
| Stale claim release | 15 minutes, and only if a later candidate pass revisits the block | `internal/gc/worker.go:522` |

Read together:

- **Happy path.** The orphan row lives as long as the S3 DELETE takes — milliseconds. The
  writer's retries cover it comfortably, and each retry re-runs the probe, so the upload
  proceeds on the first attempt after GC clears the row. The fence is invisible.
- **S3 delete fails.** The row survives with `recovery_phase = pending_s3`. The next
  thing that can clear it is Phase 16 — **up to 24 hours later**, or 90 days if recovery
  keeps failing. Every upload of that content in the meantime spends ~2 s and then fails
  with `ErrBlockDeleteInProgress`.

The escape hatch is inert. `retryUploadedBlockMaterialization` accepts a `resolveFence`
hook that could end the wait early. The two SeafHTTP paths wire it
(`internal/api/seafhttp.go:2541,3046`) to `clearSeafHTTPS3OrphanFence`, which reads the
orphan row, logs that "the writer will back off and leave S3 cleanup to GC recovery", and
returns `(false, nil)` on every path (`internal/api/seafhttp.go:2664-2681`). The three
v2 paths (`v2/blocks.go:1005`, `v2/files.go:3472`, `v2/onlyoffice.go`) pass `nil`. **No
production path can shorten the wait.**

**This is the strongest availability argument for B, not the safety-baseline argument.** A design in which
the writer never waits behind a *physical* delete removes a 24-hour worst case, not a
theoretical one.

### But the 24 hours is a scheduling artifact, not a property of A+

This is worth separating before the figure is used to choose a protocol. Orphan recovery
is **Phase 16 of the scanner**, so it inherits `scan_interval`. Nothing about the work
itself requires that cadence:

- The worker already runs its own loop on `gc.worker_interval` (30 s) with its own
  trigger channel (`internal/gc/gc.go:501-519`), and `RecoverS3Orphans` is a method on
  the **worker**, not the scanner (`internal/gc/worker.go:1332`) — the scanner only calls
  it (`internal/gc/scanner.go:1437-1444`).
- A manual trigger already exists and is wired to the API layer
  (`Service.TriggerScanner`, `internal/gc/gc.go:303-310`, called from
  `internal/api/server.go:2392`), so 24 h is the *automatic* bound, not a hard one.
- The inert `resolveFence` hook is the writer-side version of the same idea: a writer
  that hits a fence could drive bounded recovery of that exact key instead of waiting for
  a sweep.

So A+'s worst case can plausibly be cut from ~24 h to the worker tick by **where the
phase is scheduled**, with no protocol change and none of B's key-aware semantics. The
residual after that is the case where S3 is persistently unavailable — where failing the
upload closed is arguably the correct answer anyway.

**Price this before treating the 24 h figure as the reason to adopt B.** If a scheduling
change gets the stall to tens of seconds, B's remaining advantage is narrow enough that
the larger proof obligation may not be worth taking on in the same change that closes the
blocker. Two caveats keep it from being free: recovery on the worker tick pays its
`EACH_QUORUM` verify and its bucket walk far more often, which lands squarely on the GC
drain-capacity question below; and the cursor/`phaseErr` semantics were written for a
daily sweep and would need re-examining at 30-second cadence.

---

## Options

| Option | Physical-delete ABA | Publication TOCTOU | Invasiveness | Verdict |
|---|---|---|---|---|
| **A+** — sequential lives plus the complete X1 closure package | Closed | Needs the strong `pub:` post-check | Medium | **Recommended safety baseline; inherits the current ~24 h automatic recovery cadence** |
| **B** — overlapped lives ("d-lite"): fresh key for each new life, next life installs as soon as the metadata row is free | Closed | Needs the strong `pub:` post-check, in a stronger form | Medium-high, still far below r3 | **Post-A+ availability optimization** |
| **C** — writer references at `EACH_QUORUM` | **Open** | Open | Low | Not an X1 mechanism |
| **D** — S3 object versioning | Closed only if *every* delete is version-id-specific | Unchanged | Backend + interface | Discussable, not preferred |
| **E** — reduced fence, keep hash-derived keys | **Open** | Closable | Medium | Cannot close X1 |
| **0** — the abandoned r3 generational fence | Closed | Closed | Very high | Abandoned 2026-08-14; reference only |

Options A+ and B share the physical-identity and claim package; they differ only in
whether the old physical delete must finish before a new canonical life can exist.

In the table, **Closed** under *Physical-delete ABA* means that the option can close that
component only. It does not mean that X1 as a workstream is closed; the publication,
claim-ownership and recovery criteria remain open until implemented and evidenced.

---

## The shared core of A+ and B — never-reused physical keys

Keep the logical model exactly as it is:

```text
reference ──▶ L (SHA-256)          references stay logical, LOCAL_QUORUM, unchanged
                │
                 └── blocks row ──▶ storage_key      which incarnation currently holds L
```

For the physical protocol, define `P = (storage_class, storage_key)`. The key may be a
valid CAS token within one storage class, but storage I/O also needs the class to select
the backend. Every candidate, claim, orphan, finalize, recovery and exact delete carries
`P`; prose below may use `K1` as shorthand only when `C1 = storage_class(K1)` is already
fixed.

Every **new physical incarnation** of `L` writes to a freshly minted key that is never
reused. A repair of an incarnation that is still canonical and not condemned keeps its
existing key:

```text
L  = sha256:abc…

life 1:  K1 = blocks/<org>/ab/c…/abc….<uuid-1>
life 2:  K2 = blocks/<org>/ab/c…/abc….<uuid-2>
```

GC records the exact key it was authorized to destroy and deletes **that key only**. A
delayed DELETE of `K1` cannot name `K2`. That closes the stale physical-delete ABA
component of X1; publication validation, claim ownership and recovery liveness remain
separate X1 criteria.

**`storage_key` is sufficient as the incarnation/CAS identity within a storage class.**
An earlier draft proposed a separate `physical_id` column and a `delete_id`; neither is
needed. If the physical tuple `P = (storage_class, storage_key)` is globally fresh and
never reused, it *is* the identity. The exact storage I/O locator is the same tuple,
because the storage class selects the backend:

```sql
INSERT INTO gc_s3_orphans (…, storage_class, storage_key) VALUES (…, C1, K1) IF NOT EXISTS
DELETE FROM gc_s3_orphans WHERE org_id = ? AND block_id = ?
  IF storage_class = C1 AND storage_key = K1
```

**References do not become generation-aware.** They keep meaning "content `L` is live",
which any incarnation satisfies, because of the byte-identity premise above.

### What both options must change, regardless of the A/B decision

1. **A delete-by-exact-key surface.** `BlockStore` has exact-key `Put`
   (`PutObjectAutoDirect`), `Exists` (`ObjectExists`) and `Get`
   (`GetBlockByStorageKey`), but **no exact-key delete** — only
   `DeleteBlock(ctx, hash)`. Both destructive callers pass a logical block ID today:
   `deleteS3WithRetry` (`worker.go:1234`) and `RecoverS3Orphans`
   (`worker.go:1557`). The replacement must validate `(org, L, P)` and the minted-key
   format before selecting `backend(P).DeleteExact(storage_key)`; malformed persisted
   locators fail closed.
2. **`gc_s3_orphans` carries the exact physical tuple**, on the canonical table and on
   the `_by_day` projection, and recovery stops deriving. Both rows must be confirmed
   durable before `FinalizeBlockDelete` removes `blocks(L)`; a canonical orphan without
   its discovery projection can strand recovery behind the cursor.
3. **The claim must name the physical locator.** `ClaimBlockDelete` is
   `UPDATE blocks … IF gc_state != 'deleting'` (`store_cassandra.go:2183-2187`) — it
   does not mention the physical locator. It must become `IF gc_state != 'deleting' AND
   storage_class = C1 AND storage_key = K1`, and the candidate/discovery identity must
   preserve `P1` until claim, finalize and cleanup. Otherwise a candidate enqueued for one
   life can claim or clear another. The `claimID` must also be a fresh UUID per worker
   attempt, not a deterministic value derived from `candidateAt`; stale takeover and
   every release or finalize must compare the previous attempt's `(P1, claimID,
   claimed_at)`. The current `bool` result is not enough by itself: `!applied` must not
   mean "complete the candidate" when another fresh attempt owns the same `P1`.
4. **`FinalizeBlockDelete` binds the life.** Its existing
   `IF gc_state = ? AND gc_claim_id = ?` (`store_cassandra.go:2227-2229`) grows
   `AND storage_class = C1 AND storage_key = K1`.
5. **One serial domain** — see R12, and note the inventory below is larger than
   previously recorded.
6. **The `pub:` stage grows a post-insert check** — see R3.
7. **No repair-PUT onto a condemned key** — see R10.
8. **The orphan TTL package** — see the dedicated section.
9. **Physical reconciliation** — see R9.
10. **Readers and the dedup oracle stop deriving keys** — see the call-site inventory.
11. **Validate the physical locator before destruction.** `ValidatePhysicalLocator(org,
    L, storage_class, storage_key)` must validate org scope, logical hash binding and the
    minted-key format before any exact-key DELETE. A mismatch fails closed and emits an
    alert; persisted `storage_key` becoming authoritative must not turn corrupt metadata
    into permission to delete an arbitrary object. Recovery applies the same validation.

**Minted keys are necessary but not sufficient, and X1 is the whole workstream.** The
registry's X1 row carries four closure criteria, and the physical-identity change is only
one of them. In particular the claim id is derived from the candidate timestamp
(`blockDeleteClaimID(candidateAt)`, `internal/gc/worker.go:1276`) and is therefore
*shared* by every attempt on the same logical candidate: one worker can drop the
publication fence while another is still deleting under it, and the reuse probe can then
hand a writer back the very incarnation being destroyed. Physical-key uniqueness cannot
prevent that, because no new incarnation is created. Neither option below closes it on its own;
read the criteria in Registry X1 alongside this document.

---

### Option A+ — sequential lives, hardened safety baseline

GC drops the `blocks` row first, as today, and the writer keeps waiting until the whole
destructive window is over:

```text
orphan(L,P1) durable → drop blocks row → DELETE exact P1 → clear orphan(P1)
                                                          ↓
                                    writer wakes, mints P2, INSERT IF NOT EXISTS
```

`P1` and `P2` never coexist **as canonical lives**. State the guarantee that way rather
than "only one object can exist", which R9 disproves: two writers leaving the wait
together can both PUT before the install CAS picks a winner, so two *objects* for one `L`
are reachable. What A+ guarantees is that no canonical successor exists while the
predecessor's destructive lifecycle is still open. Because the canonical lives are
serialized, most of the machinery people expect is structurally unnecessary:
`ProbeBlockReuse` keeps its current meaning (an orphan blocks the logical block, because
there is no live successor to distinguish it from), no "one outstanding delete" invariant
has to be stated, and R11 cannot fire.

A+ is not merely "add a UUID". It combines sequential canonical lives with the complete
X1 closure package: physical identity `P` never reused; every destructive action bound to
`(L, P, claimID)`; a fresh claim UUID per worker attempt with CAS-protected takeover;
durable orphan creation before row removal; exact-key recovery and conditional orphan
clear; a strong post-write publication check; repair strictly separated from install
(R17); and ambiguous LWT outcomes settled in the serial domain (R20). The existing X2
global liveness verification remains in orphan recovery under A+ — **subject to R18,
which is the one place that argument does not carry over unexamined.** A stale takeover
must itself compare the prior `(P1, claimID, claimed_at)` — storage class included, since
`P` and not `storage_key` is the identity; age alone is not authority to release a claim
owned by another attempt.

For A+, any `gc_s3_orphans(L)` row remains a logical writer fence: after staging `up:` or
`pub:`, publication succeeds only if `blocks(L)` exists, has a non-empty canonical
`storage_key`, has no destructive or repair claim, and no orphan row exists for `L`.
The check is stronger than merely asking whether a generic fence was observed earlier.

**Cost.** The writer is blocked for as long as the orphan row lives. A+ inherits the
current **approximately 24-hour automatic recovery cadence** unless recovery scheduling
is changed; this is an operational scheduling choice, not a protocol bound. **But that is
no longer the worst case once R18 is accounted for:** if the conservative resolution (a)
is chosen — keep the reference re-check and stop dropping referenced orphans out of the
working set — the fence also has to outlast any attempt reference written after the
authorizing read, which is up to **48 h** for `up:` and up to **35 days** for `pub:`.
Quote the cost as "the recovery cadence, plus the attempt-reference TTL under resolution
(a)", not as a flat 24 hours. A+ can close X1 only
when the complete package above is implemented and evidenced; never-reused keys alone
close only the stale physical-delete ABA component.

The existing `resolveFence` hook can later become a writer-assisted exact-key recovery
optimization: attempt bounded recovery of the same `K1`, clear it conditionally only
after success, then re-probe and mint `K2`. If S3 remains unavailable, the writer still
fails closed and no successor life is created. That optimization is separate from the
initial A+ safety closure.

---

### Option B — overlapped lives ("d-lite") — **post-A+ optimization**

Same drop-row-first ordering, but the writer stops waiting on the *physical* delete. The
orphan row stops meaning "logical block `L` is blocked" and starts meaning "physical
incarnation `K1` is being deleted".

```text
t0  GC claims the row (gc_state='deleting', P1=(C1,K1))        ← writer blocked here
t1  BlockHasReferencesGlobal @ EACH_QUORUM == 0
t2  INSERT orphan(L, P1) IF NOT EXISTS                        ← durable authorization record
t3  DELETE blocks row IF claim AND P1                         ← writer released here
t4  DELETE K1  (slow, may fail, may be retried for days)      ← blocks nobody
        ║
        ╚═ concurrently: writer mints P2, PUT P2, INSERT blocks(L→P2) IF NOT EXISTS
```

**What the writer's wait becomes.** Not zero — that claim must not be made. `blocks` is
one row per logical block (`PRIMARY KEY ((org_id, block_id))`) and the install is
`INSERT … IF NOT EXISTS`, so while any row for `L` exists the writer cannot install `K2`.
The wait shrinks from *"until the physical delete finishes"* to *"until GC releases the
metadata row"* — one `EACH_QUORUM` read plus two or three LWTs. **The physical delete
stops blocking anyone.** That is the entire value proposition, and it is worth stating
in exactly those terms.

Eliminating the residual wait as well would require a conditional `K1 → K2` update on the
claimed row. That is a genuinely different design with a new CAS shape and is **not**
recommended: the residual is bounded by GC's own metadata machinery, and the CAS is the
first step back toward a generation lifecycle.

#### B.1 — Authorization must stop being asked about `L`

**This is the deepest change in the option, and the one most easily missed.**

Every authorization in the destructive path is keyed by the *logical* ID. Under
overlapped lives, `K1` pending and `K2` live share the same `block_id`, so those
questions stop answering the question that matters:

| Check | Where | What it does today | What it must do once `K2` exists |
|---|---|---|---|
| `BlockExists(L)` | `worker.go:1441-1452` | If the canonical row exists, defer recovery **and set `phaseErr`** | Replace existence with a canonical-locator read: if the row still points at `K1`, defer and let GC finish metadata finalization; if absent or pointing at `K2`, recovery of `K1` may continue. A logical existence check alone freezes the cursor permanently. |
| `BlockHasReferencesGlobal(L)` | `worker.go:1459` | If references exist, refuse to delete the bytes and log for an operator | Do not use `refs(L)` to re-authorize the physical delete: references to `K2` are irrelevant to `K1`. The orphan's exact key and the canonical-locator check carry the prior authorization. Legacy or keyless orphan rows cannot use this shortcut and must remain fail-closed. |
| `StartBlockDeleteOrphan` | `store_cassandra.go:1571-1600` | *"always resets the phase to `pending_s3`, even when a stale row from an older delete already exists for the same `block_id`"* | Overwrites `K1`'s record with `K2`'s ⇒ **`K1`'s only durable memory is destroyed** |

The resolution is a single principle:

> **`EACH_QUORUM == 0` authorizes the retirement of `K1` once, before the orphan row is
> written. The orphan row *is* the durable record of that authorization. Recovery
> finishes a previously authorized physical delete; it does not re-authorize it.**

Under never-reused keys that argument is sound in a way it is not today: *"`K1` was
authorized for retirement at time T"* cannot authorize a future life, because no future
life can ever be called `K1`. Recovery still must confirm that the canonical row no longer
points at `K1` before issuing the S3 DELETE. The re-verification of `refs(L)` is what
becomes redundant; the physical-locator check does not.

**This is an explicit amendment to X2's closure argument and must be recorded as one.**
The registry's X2 row currently states the opposite: that `RecoverS3Orphans` *"could
inherit authorization transitively from an orphan row that cannot exist unless that
verify passed, but that implication only runs forward in time and rested on a greenfield
precondition code cannot enforce and that fails silently."* Under minted keys the
precondition **is** enforceable by code — the orphan row names the exact bytes. Whoever
implements B rewrites that paragraph with the new argument. Deleting the re-verify
without rewriting the argument is how X2 gets silently reopened.

Concretely:

- `StartBlockDeleteOrphan` becomes a real mutual exclusion: `INSERT … IF NOT EXISTS`,
  and on conflict **do not overwrite** — release the claim on `K2` and *postpone* its
  candidate (a postpone that does not spend the queue item's retry budget; the walk
  already has `blockClaimNotYetStaleError` for exactly this shape). That is where the
  **single outstanding physical delete per logical block** invariant is actually born,
  and it lets the orphan table keep its one-row-per-`(org, L)` primary key.
- `RecoverS3Orphans` compares the current canonical locator with the orphan's exact key,
  deletes only after the row is absent or points at a different key, and clears the row
  with `IF storage_class = C1 AND storage_key = K1`. Note that `DeleteBlockS3Orphan` is a plain unconditional
  `DELETE` today (`internal/db/block_references.go:1199`) — the fence clear must become
  conditional, or a delayed clear from `K1`'s lifecycle can lift a fence belonging to a
  later one.
- The accepted price: while `K1` cannot be deleted, `K2` is not GC-eligible. Storage is
  not reclaimed until the earlier delete completes. That is fail-closed and correct, but
  it is an operational behaviour that should be named before it surprises someone.

#### B.1a — Canonical incarnation states

The logical row and the orphan row together define which physical incarnation is usable.
The minimum state machine is:

| Canonical `blocks(L)` | Orphan physical tuple | Writer outcome |
|---|---|---|
| absent | `Pold` | Allow a new incarnation; mint `Pnew`. |
| `P1`, active | absent or a different tuple | Reuse or repair `P1`; do not mint. |
| `P1`, deleting/repair-claimed | any | Block and retry. |
| `P1` | `P1` | Block and retry; `P1` remains the condemned canonical locator until the row is removed. |
| `P2`, active | `P1` | **B only:** `P2` is usable; recover `P1` independently. **A+:** this state is not allowed. |

This is the distinction the existing `NeedsPut` name hides. `NeedsPut` can mean that the
current canonical object needs repair; it does not, by itself, authorize a new physical
key. A new key is minted only for a new incarnation after the old canonical row is no
longer the row being installed into. `RegisterUploadedBlock` therefore becomes
key-aware: a repair preserves the current key, while a post-delete materialization uses a
new key and an insert that wins the canonical row.

#### Orphan creation outcomes — A+ and B

`StartBlockDeleteOrphan` must distinguish an applied insert, an idempotent retry, a
different lifecycle and an ambiguous database outcome. Treating all non-success results
as conflicts is unsafe:

| Result | Required action |
|---|---|
| `applied=true`, stored tuple `P1=(C1,K1)` | Proceed with `P1`; durably ensure the canonical orphan and its discovery projection before continuing. |
| `applied=false`, existing tuple `P1` | Treat as an idempotent resume of the same lifecycle; repair/ensure the projection and **do not release the claim**. |
| `applied=false`, existing tuple differs from `P1` | Another physical delete is pending for this logical block; do not overwrite or reset it. Release the `P1` claim through its own CAS and postpone without spending the retry budget. |
| Timeout, transport error or otherwise ambiguous CAS result | Do not release the claim. **Settle the outcome in the serial domain** — preferably an idempotent no-op retry of the same LWT; never an ordinary read used as negative authority (R20). An ambiguous result may mean `orphan(L,P1)` already exists. If settlement cannot be established, the state stays ambiguous: retain the claim and the candidate and stop. |

The storage interface may expose these outcomes directly or return enough existing-row
identity for the worker to classify them. A retry that finds its own `P1` must never be
treated as a competing lifecycle, while an ambiguous result must never reopen the claim
window between `blocks(L)` and `orphan(L,P1)` — and per **R20** it is settled in the
serial domain, never by an ordinary read used as negative authority.

**"Durable" needs an operational definition, not just the word.** The canonical
`blocks(L)` row must not be removed until both the canonical orphan row and its `_by_day`
recovery projection are durable, which means:

| Write | Requirement |
|---|---|
| Canonical `gc_s3_orphans` row | LWT, serial phase `SERIAL`, regular commit `EACH_QUORUM` — same global visibility as the claim |
| `gc_s3_orphans_by_day` projection | Ordinary write — no Paxos needed — but at **`EACH_QUORUM`**, acknowledged before `FinalizeBlockDelete` |

"Explicit consistency" is too weak a rule to state here, because it fixes no property:
`upsertS3OrphanProjection` is an ordinary insert inheriting the session, which production
sets to `LOCAL_QUORUM`. Pick `EACH_QUORUM` concretely. It costs nothing this path does not
already pay — the destructive walk is gated on every DC being reachable anyway — and it
stops a GC failover to another DC from depending on hinted or eventual delivery of the
index. The alternative, if that cost is ever unacceptable, is a durable way to rebuild
missing projection rows from the canonical ones; what is not acceptable is leaving the
level implicit.

The projection is load-bearing for recovery *liveness*, not for authorization: losing it
after the canonical row is removed is safe against data loss but leaves a fence that the
day-cursor sweep never rediscovers. **And "never a source of authorization" has to be
enforced, not merely asserted** — today recovery destroys from projection-sourced fields
without reloading the canonical row (R22). `upsertS3OrphanProjection` already returns its error
to `StartBlockDeleteOrphan`, and `processBlock` already fails closed before
`FinalizeBlockDelete` (`store_cassandra.go:1606-1608`), so **the ordering is correct
today** — this rule exists so a refactor cannot lose it, and so the projection's
consistency stops being implicit.

#### B.2 — The publication post-check must name the incarnation

`StagePublishAttemptReferences` (`internal/db/block_references.go:495-513`) inserts the
`pub:` rows and returns; there is no post-insert fence check, unlike
`RegisterUploadedBlock`. Under B the check cannot simply ask "is there an orphan?",
because an old orphan deliberately no longer blocks the writer. The correct invariant is:

> No newly written reference may become a successful publication unless, after the write,
> there exists a canonical incarnation of `L` that is active, has no destructive or
> repair claim, and whose `storage_key` is not the one recorded in an orphan row.

All conditions are load-bearing. Requiring only "a canonical row exists and is not
deleting" is satisfied by a stale row still pointing at a condemned `K1` — see R13 — and
requiring only "the key is not orphaned" misses the window between GC's claim and its
orphan insert. The post-check must reject both a condemned locator and an active claim.

The race is concrete: GC can claim `K1` and obtain `EACH_QUORUM == 0`, then a writer can
insert `pub:` before GC has inserted `orphan(L,P1)`. A check that sees only "row exists"
and "no orphan yet" would accept a publication that GC is already authorized to remove.
The post-check therefore requires `blocks(L)` to exist, `gc_state` and repair-claim state
to be clear, and the canonical `storage_key` not to match a pending orphan. If any part
is inconclusive, the call must remove the `pub:` rows staged by that call and retry rather
than succeeding. The rollback must be scoped to this attempt; if it fails, return an
error/recovery signal rather than leaving a rejected publication live. The existing
`pub:` TTL (35 days) is a leak bound, not a substitute for this rollback.

`RegisterFSObjectBlockReferences` needs no check of its own: it runs only inside
`PromotePublishAttemptReferences` (all three call sites — `seafhttp.go`, `sync.go`,
`fs_helpers.go`), i.e. after the HEAD CAS and while this attempt's `pub:` rows are still
live, so `fs:` inherits liveness from a `pub:` row that was itself checked. **The gap is
one function, not two.**

Availability note: that post-check reads `blocks` at `LOCAL_QUORUM` while `K2` may have
been installed by an LWT in another DC. It can miss it and retry. That costs latency, not
safety.

#### B.3 — The SHA-1 mapping belongs to `L`, not to a key

`cleanupBlockMapping` (`internal/gc/worker.go:707-726`) deletes from
`block_id_mappings`, which maps an **external SHA-1 → internal SHA-256**. That row
belongs to the logical block, not to any incarnation, so "check the mapping still belongs
to `K1`" is not expressible without making the mapping generation-aware — exactly what
this option avoids.

The better move is to **decouple the mapping's lifecycle from the physical object's**:
physical recovery of `K1` should not clean the logical mapping at all. Two facts support
it:

- The code already classifies a leftover mapping as benign — *"a leftover forward row is
  a harmless dangling pointer (a desktop bare-SHA-1 block GET 404s; it self-heals if the
  identical block is re-uploaded)"* (`worker.go:710-713`).
- The conflict that table protects against is *"an external SHA-1 already maps to a
  **DIFFERENT** internal SHA-256"* (`internal/db/block_references.go:50`). `K1` and `K2`
  share the same `L`, so no incarnation change can ever trigger it.

The price is that nothing reaps the mapping when `L` genuinely dies, so the table grows
by one small row per ever-deleted block with an external SHA-1. Decide explicitly: accept
the growth, or give the mapping its own logical-death reaper. Do not leave it implicit.

Until that decoupling lands, `processBlock` needs the resurrection guard that
`RecoverS3Orphans` already has (`BlockExists` before cleanup, `worker.go:1404`) and
`processBlock` lacks (`worker.go:1256`). See R11.

#### B.4 — What B deliberately does not add

`block_generations`, `block_generation_uses`, generation-bound references, retirement
evidence, a publication-frontier protocol, quarantine, abort, successor states, pointer
epochs. None of it is reachable from the invariants above.

---

### Option C — writer references at `EACH_QUORUM`

Recorded for completeness and to keep it out of X1's scope. Making
`AddBlockReference` / provisional / publish-attempt / removals write at `EACH_QUORUM`
would close cross-DC visibility from the writer side, which X2 already closed more
cheaply from the reader side. It does **not** close X1 and does **not** close the
publication TOCTOU. If it is ever revisited it is a latency experiment, not a design:
do not model it as RTT × rounds, because `SERIAL` waits on a global response order
statistic, ordinary `EACH_QUORUM` waits for a local quorum in every DC, and `ALL` waits
for the slowest replica.

---

### Option D — S3 object versioning

Version-aware PUT/GET/DELETE can separate two lives on the **same** key:

```text
PUT ABC → versionId=V1 ; GC DELETE ABC?versionId=V1 ; new PUT → V2 ; delayed DELETE V1 → V2 survives
```

Same *shape* as `K1`/`K2`, sourced from the backend instead of from us.

**Why it is not preferred.** Product direction is that versioning, if any, lives in
Cassandra so SesameFS does not depend on a specific object backend. The runtime `Store`
interface is key-only (`Put`/`Get`/`Delete(storageKey)`), the only implemented backend is
`S3Store`, and MinIO/AWS behaviour is not identical.

**The delete-marker caveat is why "just enable versioning" is unsafe.** On a versioned
bucket a **key-only** `DeleteObject` does not remove old versions — it writes a delete
marker and makes an unversioned GET see a 404. A delayed key-only DELETE after a new PUT
still hides the live current version. Versioning closes X1 **only if every delete path is
version-id-specific**, orphan recovery included. Falling back to key-only delete reopens
X1 from the reader's point of view.

---

### Option E — reduced fence, keep hash-derived keys

Fence at `SERIAL+ALL`, keep `blocks/<org>/…/<hash>`. **X1 stays open**: the stale DELETE
still names the only key the new upload will use. Useful only as the publication half of
A or B. Discarded as an X1 close.

---

### Explicitly discarded

None of the following close X1 while the old DELETE and the new PUT share one physical
identity:

- **Longer grace** (1 h / 24 h / 7 d). Reduces probability; does not bound an in-flight
  DELETE, a retry, or an outage.
- **ETag / `If-Match` / HEAD-before-DELETE.** Byte-identical rematerialization makes the
  values equal — that is the whole point of content addressing.
- **Cassandra TTL lock, GC lease, extra `ref_count`, re-read `blocks` immediately before
  DELETE.** Cassandra cannot retract a request the object store already accepted.
- **Raise RF and keep LQ/LQ.** `LOCAL_QUORUM` is per-DC; an EU write and an NA read still
  need not intersect.
- **Quarantine-prefix Copy+Delete.** The `Delete` of the live key has the same ABA. It
  remains useful as a *recovery affordance* — the bytes survive — which is a different
  claim from closing X1.
- **Two-phase "wait then HEAD".** Grace plus ETag, and fails for both reasons.

X3 (PUT before durable discoverable intent) is a storage leak, not live-data deletion,
and is out of scope here.

---

## Race matrix

Assume: references stay `LOCAL_QUORUM`; GC's authorizing read is `EACH_QUORUM`; the claim
fence is globally visible; every **new incarnation** mints a fresh key, while repairs of
an active canonical incarnation preserve its key.

| # | Sequence | Required outcome |
|---|---|---|
| R1 | EU `up:` acked, then NA GC reads at `EACH_QUORUM` | GC sees the reference; no DELETE. Closed by X2. |
| R2 | NA GC sees 0, then EU `up:` acked, then the writer's fence check | Writer sees the fence and returns a fence error; the wrapper retries. GC's DELETE names `K1` only, and the `blocks` cleanup is bound to `K1`. |
| R3 | Writer stalled after `Probe=Reusable` (LQ, no fence yet); GC fences, sees 0, deletes `K1`; the writer then stages `pub:` | **Must not succeed as "pin `K1`".** The `pub:` stage needs the post-insert check of B.2. `fs:` needs no separate check — it runs inside the promote, under a still-live checked `pub:` row. |
| R4 | GC sees 0, claims, verify still 0, row dropped, delayed S3 DELETE, writer PUTs | **A+:** the writer remains blocked by orphan `P1`; it cannot create `P2` until exact `P1` deletion and orphan clear complete. **B:** `P2` may exist after the canonical row is removed, and the delayed DELETE must still target only validated exact `P1`, never `hashToKey(L)`. |
| R5 | Writer's probe misses a remote claim because the claim commits at `LOCAL_QUORUM` | **Fails both options.** The claim must commit with global visibility (`EACH_QUORUM`, or stricter `ALL`). **A+:** any orphan for `L` remains a logical writer fence. **B:** after `blocks(L)` is gone, orphan `P1` is recovery/exclusion state for that physical locator and does not block installation of `P2`. |
| R6 | A non-local `QUORUM` GC read (2 of 3), writer LQ in the DC not contacted | **Forbidden.** Empirically red on the three-DC harness. |
| R7 | One DC down during the authorizing read | Fail closed; no DELETE. Already the shipped behaviour. |
| R8 | Who installs the next life, and with what CAS | `blocks` is one row per logical block and the install is `INSERT … IF NOT EXISTS`. **A+:** the writer waits until both the row and orphan are gone, then a plain insert creates `P2`. **B:** the writer waits only until GC drops the row, then may install `P2` while orphan `P1` remains. If the row still exists and is healthy, `NeedsPut` repairs the current `P` instead; it must not mint a losing second object. Neither needs a `P1→P2` successor CAS or a second generation table. |
| R9 | Writers in two DCs both leave the wait and both mint a key | Exactly one incarnation becomes **canonical**. `UpsertBlockMetadata` sets no serial level, so it inherits the session's — `LOCAL_SERIAL` in the shipped cluster profiles (`configs/config-eu.cluster.yaml:27`, `configs/config-usa.cluster.yaml:27`) — and two local Paxos rounds can both apply. Harmless while keys are derived (both write the same key); **not** harmless once each DC mints its own. The installing statement needs a `SERIAL` serial phase. `SERIAL` picks a canonical winner; it does **not** prevent the loser's PUT. The losing writer still knows its exact key and should best-effort delete it, or persist that exact key for cleanup. A crash before any durable intent remains X3, not an X1 bucket-inventory requirement. |
| R10 | Writer stalls after `Probe=Reusable(K1)`; GC fences, sees 0, authorizes `DELETE K1`; the writer resumes, finds the object missing, and repair-PUTs | Must not re-PUT a condemned key. Confirmed live: the `Reusable` branch of `StoreUploadedBlockForProbe` (`upload_reuse.go:152-174`) does `ObjectExists` → repair-PUT with **no** fence re-check, and `EnsureReusableBlockPresent` passes `beforePut = nil` (`:205`). The one caller that supplies `beforePut` (`v2/blocks.go:996`) uses it for the staging cap, not for the fence. Under minted keys the clean rule is **repair an active current incarnation with its current key; never repair a condemned incarnation — wait and mint a new key after the row is free**. |
| R11 | `K1`'s delete completes, `K2` is created and live, then `K1`'s `cleanupBlockMapping` runs | The SHA-1→SHA-256 mapping now belongs to `L` and must survive. `RecoverS3Orphans` guards with `BlockExists` (`worker.go:1404`); `processBlock` has no equivalent (`worker.go:1256`), safe today only because the orphan fence blocks resurrection outright — which is exactly what B gives up. **Load-bearing under B**; preventive under A. B.3 proposes removing the coupling entirely. |
| R12 | Any conditional statement on the `blocks` partition still runs at `LOCAL_SERIAL` after the others are raised | **Fails the whole fence.** The two levels are different quorum domains, so a `LOCAL_SERIAL` round can miss an in-flight `SERIAL` proposal and one straggler invalidates every other statement's guarantee. See the inventory below — it is **eleven** statements, not six. |
| R13 | `INSERT orphan(L,P1)` succeeds, `DELETE blocks` row fails persistently, and a later candidate pass may release the claim once it is at least 15 minutes old | **New, and it is a data-loss path under B.** The row is now live, unclaimed, and pointing at a physical tuple already authorized for retirement. Today `ProbeBlockReuse` refuses it because `hasOrphan` outranks everything (`block_references.go:927`); B must replace that logical fence with a tuple-aware one. The corrected outcome is not to mint `P2` while `blocks(L) -> P1` still exists: both repair and install paths must block because the canonical tuple is condemned. Once the row is removed, `P2` may be minted. This makes step 6 of the naive protocol ("`P1` is irrevocably retired once the orphan is written") false: retirement completes when the canonical row stops naming `P1`. |
| R14 | A candidate enqueued for `P1=(C1,K1)` is processed after `P1` died and `P2` was installed | The claim CAS must bind the physical tuple (`IF … AND storage_class = C1 AND storage_key = K1`), or GC claims a life it never verified. The candidate/discovery work item must carry `P1` far enough for claim, finalize and candidate cleanup to reject stale work instead of touching `P2`. `processBlock` re-reads `GetBlockInfo` after the claim, which limits the damage, but the CAS should still name the life. |
| R15 | `StartBlockDeleteOrphan` returns a conflict or ambiguous error after the orphan insert may have applied | Same-identity conflict is an idempotent resume and must retain the claim; a different identity is a confirmed competing lifecycle and may release/postpone by CAS; a timeout or error must not release the claim until the outcome has been **settled in the serial domain** (R20) — never by an ordinary read. Treating every non-applied result as a conflict reopens the claim/orphan gap. |
| R16 | A fresh attempt `D2` calls `ClaimBlockDelete(P1,D2)` while `P1` is already claimed by fresh `D1` | `!applied` is not completion. Classify row absent, same `P1` with fresh `D1` (postpone and preserve the candidate), same `P1` with stale `D1` (take over with CAS), different `P` (stale candidate; never touch it), and ambiguous timeout (settle serially per R20; no candidate cleanup). A fresh claim UUID invalidates the current `!applied → DeleteBlockGCCandidate` shortcut. |
| R17 | An **existing-incarnation operation** that read `P1` from a live canonical row completes its metadata write after `P1`'s whole destructive lifecycle finished | **Reopens X1, and revalidating before the repair PUT does not close it.** The dangerous step is the metadata install, not the PUT. `RegisterUploadedBlock` ends at `UpsertBlockMetadata`, whose first statement is `INSERT … IF NOT EXISTS` (`block_references.go:167-171`), and the `storageKey` it inserts is the one captured during the store phase (`StoreUploadedBlockForProbe` → `RegisterUploadedBlockAndMapping` → `RegisterUploadedBlock`). Sequence: writer revalidates `P1` and repair-PUTs it, then stalls; GC claims, verifies zero, orphans, drops the row, and its first exact DELETE times out ambiguously while a retry reports success; GC clears the orphan; the writer resumes, sees **no** fence (row and orphan are both gone), and its `INSERT … IF NOT EXISTS` **applies**, re-installing `blocks(L) → P1`; the confirm phase re-PUTs the object; the ambiguous first DELETE lands and the live bytes vanish. **Required outcome: repair and install must be different operations.** `REPAIR(P1)` may only *update* a canonical row that still names `P1` and must never create an absent row — if `blocks(L)` disappeared or changed, it returns retryable and the wrapper re-probes from scratch, minting `P2` if a new incarnation is warranted. Only `INSTALL(P2)`, on a key minted in this attempt, may `INSERT … IF NOT EXISTS`. **Do not scope this to the branch literally named "repair".** It covers *every* path that takes `P` from an existing canonical row and hands it to a create-capable primitive — the `Reusable` repair, and equally `NeedsPut` on an existing row, which resolves its key from `probe.StorageClass`/`probe.StorageKey` in `ResolveNeedsPutBlockStore`. Cleanest form: an existing-incarnation operation calls no create-capable metadata primitive at all, and updates — if it needs any — are `IF storage_class = C1 AND storage_key = K1`, with an absent row meaning start over. |
| R18 | An **attempt reference written after the authorizing read** — rejected or abandoned — vetoes recovery of the very key it was rejected for | **A+-specific, because A+ keeps `BlockHasReferencesGlobal(L)` in recovery. Fail-closed: the consequence is a stuck fence, not deleted live data.** Both attempt-reference kinds do it. `up:`: `RegisterUploadedBlock` writes the provisional reference **before** checking the fence and deliberately does **not** roll it back when the fence is active (`fs_helpers.go:989-1003`), TTL **48 h**. `pub:`: `StagePublishAttemptReferences` cleans up a *partial* stage, but a stage that completes and then loses its process — before the new post-check or its rollback — leaves the rows live, and their TTL is **35 days** (`PublishAttemptReferenceTTLSeconds`), 17× worse. Recovery then reads `refs(L) > 0` and refuses to delete `P1` — and that branch deliberately sets no `phaseErr`, so the cursor advances and the row **leaves the working set permanently** (`worker.go:1502-1530`, filed as `ISSUE-GC-REFERENCED-ORPHAN-LIFECYCLE-01`). Rolling back is not a sufficient answer: the writer can die between the insert and the fence observation. Three ways out, cheapest first: **(a)** keep the re-check but stop dropping a referenced orphan out of the working set — re-project and retry until the attempt TTLs expire, which is correct and costs only availability; **(b)** distinguish durably-published `fs:` from rejected/abandoned `up:`/`pub:`; **(c)** adopt B's historical-authorization argument, which needs **R21** first. **(a) is the conservative choice for a first closure; do not reach for (c) merely to optimize this.** |
| R19 | A non-creating orphan mutation resurrects a cleared orphan row | `UpdateS3OrphanAttempt` is a plain `UPDATE … WHERE org_id = ? AND block_id = ?` with no `IF` (`store_cassandra.go:1742-1759`), and in Cassandra that is an upsert. A recoverer whose S3 delete failed can write it after another path already cleared the row, recreating a **partial** row with `last_attempt_at`/`retry_count`/`last_error` and no `storage_class`, no `recovery_phase`, no `first_seen_at` — and no `_by_day` projection row, because that mutation does not touch the projection. The result is worse than a stale row: under A+ it is a **writer fence** (`ProbeBlockReuse` answers `BlockedByGC` on any orphan) that recovery can never enumerate, and if the TTL is removed as the TTL package proposes, it never expires either. **Rule: once `P` exists, `StartBlockDeleteOrphan` is the only mutation allowed to create orphan state.** Every update/phase/clear is conditional on the expected `P` and fails when the row is gone. The `_by_day` table is a discovery index and never a source of authorization. |
| R20 | An LWT returns a timeout or otherwise ambiguous result and the caller resolves it with an ordinary read | **An ordinary read is never authority to conclude that a claim or orphan does not exist.** Cassandra can accept a Paxos proposal that the client never learns, and an ordinary read need not materialize it — this is exactly the defect already filed as `ISSUE-GC-STALE-CLAIM-READ-CONSISTENCY-01` and documented at `store_cassandra.go:1880-1910`, where the comment defers the fix to "the serial-domain decision X1 has to make anyway". Every "read back and reconcile" in R15 and R16 means **settle in the serial domain**, and the mechanism has to be named rather than left to the implementer: **prefer an idempotent, no-op retry of the same LWT in the same serial domain**, whose `applied`/existing-row result is authoritative. A read at `SERIAL` level is acceptable *only* if it runs in the same serial domain as the write (R12 — `LOCAL_SERIAL` and `SERIAL` are different domains, so a mismatched level settles nothing) and only once its proposal-materializing behaviour is verified on the deployed engine. It must never be implemented as an ordinary `SELECT` that merely carries a consistency argument. If neither settles (timeout, DC unavailable, serial quorum unreachable), the state **stays ambiguous** and the response is: retain the claim, retain the candidate, do not finalize, do not release, do not clear the orphan. Fail closed. |

| R21 | A second API can create an orphan row, so the orphan cannot be trusted as an authorization certificate | **Blocks any design in which `orphan(L,P1)` *is* the durable proof that `EACH_QUORUM == 0` happened** — which is B's whole recovery argument and one of the two ways out of R18 for A+. R19's rule ("only `StartBlockDeleteOrphan` may create orphan state") is **not true of the code today**: `RecordS3Orphan` runs its own `INSERT INTO gc_s3_orphans … IF NOT EXISTS` (`store_cassandra.go:1618-1630`) and sits in the `GCStore` interface (`store.go:196`). It has **no production caller** — only `s3_orphan_recovery_test.go` and the mock — and its doc comment ("Called both for the recovery scanner and for actual S3 delete failures") is stale, since the scanner path uses `StartBlockDeleteOrphan` and failure updates use `UpdateS3OrphanAttempt`. That makes it cheap to fix *now*: drop it from the interface, or narrow it to `RepairExistingS3Orphan` with `IF EXISTS` and no ability to create. Left alone, it is a forgery vector for a certificate the protocol is about to start trusting. |
| R22 | Recovery destroys bytes using data read from the discovery projection, without re-reading the canonical orphan row | The document says `_by_day` is "a discovery index and never a source of authorization", but the code does not honour that: `RecoverS3Orphans` takes its `S3OrphanInfo` straight from `ListS3OrphansByDay` and then uses those fields — `StorageClass` included — to resolve the backend and issue the delete (`worker.go:1376-1560`), never reloading `gc_s3_orphans`. So a stale or diverged projection row can select the wrong backend for a physical delete. **Required flow: `by_day` → load the canonical orphan → require an exact `P` match → only then destroy.** If the canonical row is missing, or `P_projection ≠ P_canonical`, the sweep may repair or drop the projection entry and must **never** issue a physical DELETE. |
| R23 | `storage_class` is rebound to a different bucket, account or backend namespace | **`P` is only an eternal physical identity if the class name is.** `storage_class` is a logical label resolved through `m.backends[className]` (`internal/storage/storage.go:493`), and bucket/endpoint for a class come from configuration (`internal/config/config.go:3526,3532`). Rebind `hot-s3-na` from bucket A to bucket B and every persisted `P` silently renames the object it addresses — a months-old orphan would then issue an exact DELETE into a namespace it never verified. Either declare and operationally enforce that a storage-class name is **append-only and never rebound to a different physical namespace**, or persist an immutable `backend_id` and define `P = (backend_id, storage_key)`. Credentials and endpoints may change as long as they keep naming the same namespace. **Removing the orphan TTL makes this contract permanent rather than 90-day-bounded.** |
| R24 | A minted key that lost its install, or whose install outcome is unsettleable, is reused by a later attempt | **The same ABA as X1, produced by the writer's own cleanup instead of by GC.** R9 has the losing writer best-effort delete its key; R17 forbids a stale *repair* from installing. Neither covers a stale **install**: `W2` loses the CAS to `W1`, schedules cleanup of `P2`, and that DELETE is slow; later GC removes `P1` and `blocks(L)` goes absent; a lingering retry of `W2` re-runs `INSERT … IF NOT EXISTS` with `P2`, which now applies — and the old cleanup DELETE lands on live bytes. **Rule: a minted `P` is a single-use installation identity, canonical or not.** Once its install is known to have lost, its outcome cannot be settled safely, or any cleanup of it is authorized, that `P` is permanently ineligible for canonical installation; a retry that still needs an incarnation mints a fresh one. This also bounds R20: "idempotent retry of the same CAS" is safe for claim and orphan statements, but **not** for an install whose history is already uncertain. |

R8, R13 and R15 decide whether Option B is viable. R16, R17, R19, R20, R22, R23, R24, R3,
R9, R10, R11 and R12 are common closure criteria — R16 and R20 become newly load-bearing
when claim IDs move from candidate identity to per-attempt UUIDs, and R17 is the one that
shows why "revalidate immediately before the repair PUT" was never sufficient on its own.
**R18 is A+-specific and is the one open question A+ does not inherit from X2 for free.
R21 gates whichever escape from R18 is chosen**, because both B's argument and A+'s
historical-authorization option depend on the orphan row being unforgeable.

**The invariant these add up to.** `P` has a monotonic authority lifecycle, and no
transition ever runs backwards:

```text
fresh / never-canonical  →  canonical  →  condemned (delete authorized)  →  burned forever
                    └──────── lost or unsettleable install ────────────────────┘
```

Never `condemned → canonical`, and never `burned → canonical`. R17 forbids the first
through a stale existing-incarnation path; R24 forbids the second through a stale install.

---

## R12 — the one-serial-domain inventory, corrected

An earlier count named six conditional statements on the `blocks` partition. **There are
eleven.** R12's own rule is that one statement left behind invalidates the guarantee for
all the others, so the count is not a detail.

| # | Statement | Location |
|---|---|---|
| 1 | `upsertBlockMetadataInsertWithRepresentationFn` — first-writer `INSERT … IF NOT EXISTS` | `internal/db/block_references.go:169` |
| 2 | `claimReleasedBlockStubForRepairFn` | `internal/db/block_references.go:204` |
| 3 | `deleteRepairClaimedBlockStubFn` | `internal/db/block_references.go:217` |
| 4 | `deleteClaimedBlockStubFn` | `internal/db/block_references.go:237` |
| 5 | `backfillBlockSHA1Fn` | `internal/db/block_references.go:253` |
| 6 | `backfillBlockRepresentationIDFn` | `internal/db/block_references.go:262` |
| 7 | `ReleaseBlockDeleteClaim` | `internal/db/block_references.go:1180` |
| 8 | `ReleaseStaleBlockClaim` | `internal/gc/store_cassandra.go:1931` |
| 9 | `ClaimBlockDelete` | `internal/gc/store_cassandra.go:2183` |
| 10 | `ReleaseBlockClaim` | `internal/gc/store_cassandra.go:2207` |
| 11 | `FinalizeBlockDelete` | `internal/gc/store_cassandra.go:2227` |

Statements 4, 5, 6, 7 and 8 were absent from the earlier list.

**And the rule does not stop at `blocks`.** Under any drop-row-first design,
`gc_s3_orphans` carries the durable physical-delete authorization after the canonical row
is dropped, so its conditional operations need the same **global `SERIAL` discipline**.
The two partitions do not share a Paxos log and `SERIAL` does not make orphan insertion
and canonical-row deletion atomic; R13 remains a real two-step crash window. The protocol
must provide the ordering and recovery checks explicitly.

The orphan partition carries five more conditional statements
(`internal/gc/store_cassandra.go:1577, 1598, 1630, 1685, 1717`) plus one
**unconditional** `DELETE` that clears the record (`internal/db/block_references.go:1199`),
which B.1 requires to become conditional. Every relevant LWT in both partitions uses
serial phase `SERIAL`, with `EACH_QUORUM` as the default regular commit for global
claim/orphan visibility (`ALL` is stricter but not required for intersection with an
explicit `LOCAL_QUORUM` writer read), and `QUORUM` or higher for installation. This is a
consistency discipline, not cross-table atomicity.

There is a related defect already filed against the same decision:
`ISSUE-GC-STALE-CLAIM-READ-CONSISTENCY-01`, documented at length in
`internal/gc/store_cassandra.go:1880-1910` — `ReleaseStaleBlockClaim` reads claim state
with an ordinary read that can miss a cross-DC claim or an accepted-but-uncommitted
Paxos value. The comment there explicitly defers the fix to "the serial-domain decision
X1 has to make anyway". This is that decision.

---

## The orphan TTL package — four changes, not one

`gc_s3_orphans` carries `default_time_to_live = 7776000` (90 days) on the canonical table
and on the `_by_day` projection. A persistently failing delete therefore has its recovery
record expire silently.

The physical-delete ABA component would remain closed under B even then — with no `blocks` row the
writer probes `NeedsPut`, mints a fresh key, and a stale DELETE of the old key can never
reach it. What expiry destroys is **the durable record that the old key still has to be
deleted**: the bytes leak with nothing pointing at them, and under B the
single-outstanding-delete invariant silently becomes untrue. X1 as a whole is not closed
by that key separation alone.

The cold-start recovery horizon is deliberately pinned to the TTL:
`gcS3OrphanInitialScanLookbackDays = 90` (`internal/gc/worker.go:1644`), whose comment
says to match it "so the first pass can still see every live orphan row". Drop the TTL
and leave the horizon alone and any orphan older than 90 days becomes permanently
invisible after a cursor loss — precisely the case where the orphan matters most.

The package is: remove the canonical TTL; remove the `_by_day` projection TTL; redefine
the cold-start horizon and cursor semantics; and guarantee discovery of an arbitrarily
old orphan. Expiry, if kept at all, must raise an alert instead of deleting the row.

Note the cursor's current shape is itself part of this: the sweep advances the day cursor
only when the pass had no `phaseErr` (`worker.go:1602`), and the "still referenced"
branch deliberately does not set one — so that row falls out of the working set and is
never revisited. Under B that branch changes meaning entirely (B.1).

---

## Call-site inventory

A conceptual diff, not an implementation list.

| Surface | Today | What minted keys need |
|---|---|---|
| `hashToKey` / `StorageKeyForHash` | Deterministic locator | Prefix/format helper only; never the authoritative live locator. The live physical tuple is persisted `(storage_class, storage_key)` |
| `DeleteBlock(hash)` / `BlockStoreDeleter` | Derives the key from the hash | Delete the exact key captured in the GC work. **No exact-key delete exists today** |
| `GetBlock` / `BlockExists` / `PutBlock` / `PutBlockData` / `PutBlockAuto` / `PutBlockAutoDirect` | All derive via `hashToKey` | Take an exact key, or resolve one first. Exact-key `Put`/`Exists`/`Get` variants already exist and are the model |
| `CheckBlocksExist` (canonical reader, primary `/check-blocks`) and `CheckBlocks` / `CheckBlocksParallel` (legacy fallback) | Resolve the canonical location, then HEAD a key derived from the hash | Keep the physical existence check: `L → DB resolves (storage_class, storage_key) → HEAD exact storage_key → exists/missing`. This is a derived-HEAD to DB-resolved exact-key change, not a database-only dedup answer. |
| `S3Store.Put(ctx, blockID, …)` | Second derivation layer: `s.key(blockID)` on what callers already pass as a key | Make the key parameter mean a key |
| `canonical_block_reader.go:238` | Rejects persisted ≠ derived | Use the persisted key; validate org/hash/format instead |
| `upload_reuse.go` — `ResolveNeedsPutBlockStore` **and** the `Reusable` branch of `StoreUploadedBlockForProbe` | Two reject sites; the second also repair-PUTs at the derived key | Immediately before repair PUT, re-read the canonical row and require the same `storage_key`, no destructive/repair claim and no orphan. Repair/reuse that active key; mint only when a new incarnation is allowed; never repair a condemned incarnation (R10) |
| `UpsertBlockMetadata` | `INSERT … IF NOT EXISTS` inheriting the session serial level | Store the exact key; raise the serial phase to `SERIAL` so one incarnation wins globally (R9) |
| `ClaimBlockDelete` / `FinalizeBlockDelete` | Conditional on `gc_state` / `gc_claim_id` only | Bind the life: `AND storage_class = C1 AND storage_key = K1`, with fresh per-attempt claim identity (R14/R16) |
| `ReleaseBlockClaim`, `ReleaseStaleBlockClaim`, `ReleaseBlockDeleteClaim`, the stub-repair pair, both backfills | Conditional statements on `blocks`, inheriting the session serial level | Serial phase `SERIAL` — the one-serial-domain rule admits no exceptions on this partition (R12) |
| `gc_s3_orphans` (+ `gc_s3_orphans_by_day`) | PK `((org_id, block_id))`; grew `external_sha1`/`recovery_phase` (migration 007) and `representation_id` (009) | Add exact `(storage_class, storage_key)` to both; recovery and `ListS3OrphansByDay` must not `hashToKey`; the clear becomes conditional on both tuple fields |
| `StartBlockDeleteOrphan` | `INSERT … IF NOT EXISTS`, then **resets** an existing row to `pending_s3` | Return/classify applied, same-key idempotent, different-key conflict and ambiguous error; never overwrite/reset, never release on an ambiguous result, and postpone only a confirmed different lifecycle (B.1) |
| `gcS3OrphanInitialScanLookbackDays = 90` | Cold-start horizon, matched to the TTL | Redefine together with the TTL removal |
| `RecoverS3Orphans` | Re-verifies `BlockExists(L)` and `BlockHasReferencesGlobal(L)` | **A+:** retain `BlockHasReferencesGlobal(L)` and the canonical-row check, then delete exact validated `P1`. **B:** the orphan may carry the historical authorization for `P1`, but recovery still validates the canonical locator and legacy/keyless rows fail closed. |
| `cleanupBlockMapping` | Deletes the SHA-1 mapping unconditionally from `processBlock`; `RecoverS3Orphans` guards with `BlockExists` | Decouple from the physical lifecycle (B.3), or add the same guard to `processBlock` (R11) |
| `StagePublishAttemptReferences` | Insert only | Post-check the active canonical incarnation. **A+:** any orphan for `L` blocks success. **B:** the canonical `P` must differ from the pending orphan `P1`; both also require no destructive/repair claim. Roll back this call's `pub:` rows on failure. |
| `RegisterUploadedBlock` | Write `up:` then check the logical fence, then `UpsertBlockMetadata`; the provisional ref is **not** rolled back when the fence is found active (`fs_helpers.go:989-1003`) | Split repair from install (**R17**): a repair may only update a row that still names the same `P`, never create an absent one. Make the path tuple-aware: reuse/repair the active canonical `P`, block a condemned/deleting one, mint only for a genuine new incarnation. And settle what the surviving `up:` row does to recovery (**R18**) |
| `UpdateS3OrphanAttempt` | Plain `UPDATE` with no `IF` (`store_cassandra.go:1742-1759`) — an upsert that can recreate a cleared orphan as a partial row with no `storage_class` and no `_by_day` projection | Condition on the expected `P` and fail when the row is gone (**R19**) |
| `RecordS3Orphan` | A **second** `INSERT … IF NOT EXISTS` creator of `gc_s3_orphans` (`store_cassandra.go:1618-1630`), exposed in the `GCStore` interface (`store.go:196`) with **no production caller** — tests and the mock only — and a stale doc comment | Remove from the interface, or narrow to `RepairExistingS3Orphan` with `IF EXISTS` and no ability to create. `StartBlockDeleteOrphan` must be the sole creator before any design treats the orphan as an authorization certificate (**R21**) |
| `RecoverS3Orphans` discovery | Takes `S3OrphanInfo` — `StorageClass` included — straight from `ListS3OrphansByDay` and destroys on it, never reloading the canonical row | `by_day` → load canonical orphan → require exact `P` match → destroy. Mismatch or missing canonical row repairs/drops the index entry and never deletes (**R22**) |
| `storage_class` → backend | A logical name resolved through `m.backends[className]` (`internal/storage/storage.go:493`); bucket and endpoint come from config (`internal/config/config.go:3526,3532`) | Either contractually append-only and never rebound to another namespace, or persist an immutable `backend_id` and define `P = (backend_id, storage_key)` (**R23**) |

---

## Open questions to settle before anything is accepted

Item 0 comes first because it reopens X1 outright; the rest are ordered so the cheapest
decisions come first.

0. **R17 — repair must never become install.** The highest-severity item on this list and
   the only one that reopens X1 outright. Split the metadata path in two: `REPAIR(P1)`
   updates a canonical row that still names `P1` and **may not create an absent row**;
   only `INSTALL(P2)`, on a key minted in this attempt, may `INSERT … IF NOT EXISTS`.
   Settle this before anything else, because "revalidate before the repair PUT" reads
   like a fix and is not one.
1. **A+ claim identity.** Replace the candidate-derived claim with a fresh UUID per
   worker attempt; carry `(P, claimID, claimed_at)` through claim, release, finalize,
   stale takeover and candidate cleanup.
2. **A+ CAS outcomes (R15/R16/R20).** Distinguish applied, same-physical-identity
   idempotent, different-identity conflict, existing fresh owner, stale owner and
   ambiguous results. Settle ambiguity in the serial domain; never release, finalize or
   complete a candidate on an ordinary read used as negative authority. This subsumes
   `ISSUE-GC-STALE-CLAIM-READ-CONSISTENCY-01`, which was explicitly deferred to this
   decision.
3. **R21 — orphan authorization provenance.** Cheap today and it gates everything below
   it: `RecordS3Orphan` can create an orphan row and is in the `GCStore` interface with
   **no production caller**. Remove it from the interface or narrow it to an `IF EXISTS`
   repair. Until that holds, the orphan cannot be trusted as an authorization certificate,
   which is what both B and R18's option (c) require.
4. **R18 — post-authorization attempt-reference poisoning.** Decide whether A+ keeps
   `BlockHasReferencesGlobal(L)` in recovery. As written, a rejected or abandoned `up:`
   (48 h) or `pub:` (35 days) row can veto recovery of the key it was rejected for, and
   the refusal branch drops the orphan out of the working set. **Take the conservative
   option (a) for a first closure** — keep the re-check, stop dropping referenced orphans,
   re-project and retry — and treat the availability cost as the price. This is the one
   A+ question X2's closure does not answer.
5. **R22 — canonical orphan revalidation.** Recovery must reload the canonical orphan and
   match `P` exactly before any physical delete; a projection mismatch repairs the index
   and never destroys.
6. **R23 — immutable backend namespace.** Decide and write down whether a storage-class
   name is contractually append-only, or persist a `backend_id` and redefine `P` on it.
   Removing the orphan TTL makes this contract permanent.
7. **R24 — install identity is single-use.** A minted `P` that lost, became unsettleable,
   or had cleanup authorized is burned; retries mint fresh. Also bounds R20's "idempotent
   retry of the same CAS", which is safe for claim and orphan but not for install.
8. **R3 — the `pub:` post-check.** Require an active canonical row, no destructive or
   repair claim, and a usable non-orphaned key. A failed post-check must roll back the
   `pub:` rows staged by that call, with rollback failure treated as recovery/error rather
   than publication success.
9. **R10 — never repair a condemned incarnation.** Revalidate the exact canonical key,
   claim state and orphan state immediately before repair PUT. Necessary but not
   sufficient on its own — see R17.
10. **R19 — orphan mutation resurrection.** After `P` exists, `StartBlockDeleteOrphan` is
   the only mutation permitted to create orphan state; every update/phase/clear is
   conditional on the expected `P` and fails when the row is gone.
11. **R12 — one serial domain**, across eleven `blocks` statements and the
   `gc_s3_orphans` partition; use `SERIAL` for the serial phase and `EACH_QUORUM` as the
   default regular visibility level for the global fence.
12. **B-specific R13/B.1.** If B is pursued, settle the tuple-aware probe/install rules and
   rewrite the recovery argument so `P1` authorization is not confused with `L` liveness.
13. **R9 — the losing PUT.** `SERIAL` picks a canonical winner, but the loser's minted key
   is not in `blocks` or an orphan row. The losing writer still knows that exact key and
   should best-effort delete it or persist it for cleanup. A crash after PUT but before
   any durable intent remains the existing X3 leak; do not make bucket inventory a
   prerequisite for X1.
14. **B.3 — the SHA-1 mapping.** Decouple and accept the growth, or build the reaper.
15. **The TTL package**, all four changes together — and note R19 first: removing the TTL
    without closing the resurrection path turns a partial resurrected orphan into a
    permanent, undiscoverable writer fence.
16. **GC drain capacity.** The queue loop in `internal/gc/worker.go` is strictly serial at
    `gc.batch_size = 100` on a `gc.worker_interval = 30s` tick. Any design that fences
    globally adds per-block WAN LWTs to that loop. This is a throughput question, not a
    latency one, and "GC is not the hot path" does not answer it. Independent of which
    option wins.
17. **Fail-closed observability.** `EACH_QUORUM` couples GC availability to every DC, and
    at RF 1 the tolerance is zero. Correct under "when in doubt, do not delete", but a
    silent stall defers the user/library/org purge SLAs and leaves space uncollected.

**When this option stops being small.** If R13's predicate cannot be expressed without a
second physical-identity table, or R8 turns out to need a conditional `K1 → K2` update, or
the `pub:` post-check cannot be made to hold, then the missing piece is a publication
frontier — and that is what the abandoned r3 document already contains. In that case
retrieve it from the reference branch rather than inventing a third protocol.

---

## Status of the abandoned design

`docs/gc-x1-x2-generation-fence-final` holds the r3 generational-fence ADR (~8.9k lines
of protocol specification) and PR #166, closed unmerged on 2026-08-14. It is retained as
**investigative reference only**. Nothing in it is accepted, frozen, or scheduled, and no
document should cite it as a design of record. Its lasting contributions are recorded
above: never-reused physical keys as the thing that closes the physical-delete ABA
component (not X1 by itself), the one-serial-domain rule, and the exact-key recovery
requirement.
