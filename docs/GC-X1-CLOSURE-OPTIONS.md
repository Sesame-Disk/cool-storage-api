# X1 — Closure Options for the Physical-Delete ABA

**Status:** Analysis and option comparison. **No option is accepted. Nothing is
implemented. This is not an ADR.**

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
material contribution — that X1 closes only when new bytes use never-reused physical
keys — is preserved here without the surrounding lifecycle protocol.

X2's closure removed most of r3's justification. r3 kept ordinary reference writes at
`LOCAL_QUORUM` and therefore had to *prove* that no new reference could appear against a
generation being retired: that proof is the publication frontier, with `SERIAL+ALL`
`ACTIVE→RETIRING`, `EACH_QUORUM` use drain, retirement evidence, quarantine and abort.
Once a global GC-side liveness read is shipped and sufficient, what remains to buy is
physical identity — which is a schema and locator change, not a distributed lifecycle.

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

This premise is load-bearing for the recommended option. If block IDs ever stop being a
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
Prefer `EACH_QUORUM` so a later RF>1 does not require every replica. `ALL` is the
stricter availability choice and is what a writer-visibility **fence** needs, as opposed
to a liveness read.

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
| `BlockDeleteFenceActive` | `gc_state='deleting'` **or** a `gc_s3_orphans` row | Writers back off after the `blocks` row is gone | Orphan row is keyed by logical `block_id`; recovery deletes by derived hash |
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

- **Zero references already means "do not trust this incarnation".** A row with no
  references is a GC candidate, so the probe answers `NeedsPut` and the writer stores its
  own copy. No option below invents that check; they only change which key the resulting
  PUT targets.
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

**This is the single strongest argument for the recommended option.** A design in which
the writer never waits behind a *physical* delete removes a 24-hour worst case, not a
theoretical one.

---

## Options

| Option | X1 | Publication TOCTOU | Invasiveness | Verdict |
|---|---|---|---|---|
| **A** — sequential lives: minted keys, writer waits for the whole destructive window | Closed | Needs the `pub:` post-check | Medium | Correct but keeps the 24 h worst case |
| **B** — overlapped lives ("d-lite"): minted keys, writer installs the next life as soon as the metadata row is free | Closed | Needs the `pub:` post-check, in a stronger form | Medium-high, still far below r3 | **Recommended** |
| **C** — writer references at `EACH_QUORUM` | **Open** | Open | Low | Not an X1 mechanism |
| **D** — S3 object versioning | Closed only if *every* delete is version-id-specific | Unchanged | Backend + interface | Discussable, not preferred |
| **E** — reduced fence, keep hash-derived keys | **Open** | Closable | Medium | Cannot close X1 |
| **0** — the abandoned r3 generational fence | Closed | Closed | Very high | Abandoned 2026-08-14; reference only |

Options A and B share everything except one decision, so they are described together.

---

## The shared core of A and B — never-reused physical keys

Keep the logical model exactly as it is:

```text
reference ──▶ L (SHA-256)          references stay logical, LOCAL_QUORUM, unchanged
                │
                └── blocks row ──▶ storage_key      which incarnation currently holds L
```

Every materialization of `L` writes to a **freshly minted key that is never reused**:

```text
L  = sha256:abc…

life 1:  K1 = blocks/<org>/ab/c…/abc….<uuid-1>
life 2:  K2 = blocks/<org>/ab/c…/abc….<uuid-2>
```

GC records the exact key it was authorized to destroy and deletes **that key only**. A
delayed DELETE of `K1` cannot name `K2`. That is the whole of X1.

**`storage_key` is sufficient as the physical identity.** An earlier draft proposed a
separate `physical_id` column and a `delete_id`; neither is needed. If the key is
globally fresh and never reused, it *is* the identity, and it works as a CAS token:

```sql
INSERT INTO gc_s3_orphans (…, storage_key) VALUES (…, K1) IF NOT EXISTS
DELETE FROM gc_s3_orphans WHERE org_id = ? AND block_id = ? IF storage_key = K1
```

**References do not become generation-aware.** They keep meaning "content `L` is live",
which any incarnation satisfies, because of the byte-identity premise above.

### What both options must change, regardless of the A/B decision

1. **A delete-by-exact-key surface.** `BlockStore` has exact-key `Put`
   (`PutObjectAutoDirect`), `Exists` (`ObjectExists`) and `Get`
   (`GetBlockByStorageKey`), but **no exact-key delete** — only
   `DeleteBlock(ctx, hash)`. Both destructive callers pass a logical block ID today:
   `deleteS3WithRetry` (`worker.go:1234`) and `RecoverS3Orphans`
   (`worker.go:1557`).
2. **`gc_s3_orphans` carries the exact key**, on the canonical table and on the
   `_by_day` projection, and recovery stops deriving.
3. **The claim must name the key.** `ClaimBlockDelete` is
   `UPDATE blocks … IF gc_state != 'deleting'` (`store_cassandra.go:2183-2187`) — it
   does not mention `storage_key`. It must become `IF gc_state != 'deleting' AND
   storage_key = K1`, or a candidate enqueued for one life can claim another.
4. **`FinalizeBlockDelete` binds the life.** Its existing
   `IF gc_state = ? AND gc_claim_id = ?` (`store_cassandra.go:2227-2229`) grows
   `AND storage_key = K1`.
5. **One serial domain** — see R12, and note the inventory below is larger than
   previously recorded.
6. **The `pub:` stage grows a post-insert check** — see R3.
7. **No repair-PUT onto a condemned key** — see R10.
8. **The orphan TTL package** — see the dedicated section.
9. **Physical reconciliation** — see R9.
10. **Readers and the dedup oracle stop deriving keys** — see the call-site inventory.

**Minted keys are necessary but not sufficient, and X1 is the whole workstream.** The
registry's X1 row carries four closure criteria, and the physical-identity change is only
one of them. In particular the claim id is derived from the candidate timestamp
(`blockDeleteClaimID(candidateAt)`, `internal/gc/worker.go:1276`) and is therefore
*shared* by every attempt on the same logical candidate: one worker can drop the
publication fence while another is still deleting under it, and the reuse probe can then
hand a writer back the very incarnation being destroyed. Generational keys cannot prevent
that, because no new incarnation is created. Neither option below closes it on its own;
read the criteria in Registry X1 alongside this document.

---

### Option A — sequential lives

GC drops the `blocks` row first, as today, and the writer keeps waiting until the whole
destructive window is over:

```text
orphan(L,K1) durable → drop blocks row → DELETE K1 → clear orphan
                                                          ↓
                                    writer wakes, mints K2, INSERT IF NOT EXISTS
```

`K1` and `K2` never coexist. Because the lives are serialized, most of the machinery
people expect is structurally unnecessary: `ProbeBlockReuse` keeps its current meaning
(an orphan blocks the logical block, because there is no live successor to distinguish it
from), no "one outstanding delete" invariant has to be stated, and R11 cannot fire.

**Why it is not recommended.** It is exactly today's protocol plus an identity, and it
therefore inherits today's worst case in full: the writer is blocked for as long as the
orphan row lives, which the measured section above puts at **up to 24 hours** when the
S3 delete fails. Option A closes X1 and leaves that untouched.

Keep A on file as the fallback if B's corrections prove harder than they look. It is
strictly smaller.

---

### Option B — overlapped lives ("d-lite") — **recommended**

Same drop-row-first ordering, but the writer stops waiting on the *physical* delete. The
orphan row stops meaning "logical block `L` is blocked" and starts meaning "physical
incarnation `K1` is being deleted".

```text
t0  GC claims the row (gc_state='deleting', storage_key=K1)   ← writer blocked here
t1  BlockHasReferencesGlobal @ EACH_QUORUM == 0
t2  INSERT orphan(L, K1) IF NOT EXISTS                        ← durable authorization record
t3  DELETE blocks row IF claim AND storage_key = K1           ← writer released here
t4  DELETE K1  (slow, may fail, may be retried for days)      ← blocks nobody
        ║
        ╚═ concurrently: writer mints K2, PUT K2, INSERT blocks(L→K2) IF NOT EXISTS
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

| Check | Where | What it does today | What it does once `K2` exists |
|---|---|---|---|
| `BlockExists(L)` | `worker.go:1441-1452` | If the canonical row exists, defer recovery **and set `phaseErr`** | Always true ⇒ recovery of `K1` defers forever, and because the cursor advances only `if phaseErr == nil` (`worker.go:1602`) **the orphan cursor freezes permanently** |
| `BlockHasReferencesGlobal(L)` | `worker.go:1459` | If references exist, refuse to delete the bytes and log for an operator | Always true (they are `K2`'s references) ⇒ `K1` is never authorized. This branch deliberately does **not** set `phaseErr`, so the cursor advances, the row falls out of the working set, and `K1` leaks until the 90-day TTL — at which point the alert goes quiet too. Already filed as `ISSUE-GC-REFERENCED-ORPHAN-LIFECYCLE-01` |
| `StartBlockDeleteOrphan` | `store_cassandra.go:1571-1600` | *"always resets the phase to `pending_s3`, even when a stale row from an older delete already exists for the same `block_id`"* | Overwrites `K1`'s record with `K2`'s ⇒ **`K1`'s only durable memory is destroyed** |

The resolution is a single principle:

> **`EACH_QUORUM == 0` authorizes the retirement of `K1` once, before the orphan row is
> written. The orphan row *is* the durable record of that authorization. Recovery
> finishes a previously authorized physical delete; it does not re-authorize it.**

Under never-reused keys that argument is sound in a way it is not today: *"`K1` was
authorized dead at time T"* cannot expire, because no future life can ever be called
`K1`. The re-verification exists today precisely **because** the key is shared with
future lives — minting keys converts it from necessary to redundant.

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
- `RecoverS3Orphans` deletes the exact stored key, and clears the row with
  `IF storage_key = K1`. Note that `DeleteBlockS3Orphan` is a plain unconditional
  `DELETE` today (`internal/db/block_references.go:1199`) — the fence clear must become
  conditional, or a delayed clear from `K1`'s lifecycle can lift a fence belonging to a
  later one.
- The accepted price: while `K1` cannot be deleted, `K2` is not GC-eligible. Storage is
  not reclaimed until the earlier delete completes. That is fail-closed and correct, but
  it is an operational behaviour that should be named before it surprises someone.

#### B.2 — The publication post-check must name the incarnation

`StagePublishAttemptReferences` (`internal/db/block_references.go:495-513`) inserts the
`pub:` rows and returns; there is no post-insert fence check, unlike
`RegisterUploadedBlock`. Under B the check cannot simply ask "is there an orphan?",
because an old orphan deliberately no longer blocks the writer. The correct invariant is:

> No newly written reference may become a successful publication unless, after the write,
> there exists a canonical incarnation of `L` **whose `storage_key` is not the one
> recorded in an orphan row**.

Both halves are load-bearing. Requiring only "a canonical row exists and is not
deleting" is satisfied by a stale row still pointing at a condemned `K1` — see R13 — and
would publish references over bytes that recovery is about to delete.

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
fence is globally visible; every materialization mints a fresh key.

| # | Sequence | Required outcome |
|---|---|---|
| R1 | EU `up:` acked, then NA GC reads at `EACH_QUORUM` | GC sees the reference; no DELETE. Closed by X2. |
| R2 | NA GC sees 0, then EU `up:` acked, then the writer's fence check | Writer sees the fence and returns a fence error; the wrapper retries. GC's DELETE names `K1` only, and the `blocks` cleanup is bound to `K1`. |
| R3 | Writer stalled after `Probe=Reusable` (LQ, no fence yet); GC fences, sees 0, deletes `K1`; the writer then stages `pub:` | **Must not succeed as "pin `K1`".** The `pub:` stage needs the post-insert check of B.2. `fs:` needs no separate check — it runs inside the promote, under a still-live checked `pub:` row. |
| R4 | GC sees 0, claims, verify still 0, row dropped, delayed S3 DELETE, writer PUTs | `K2` ≠ `K1`, so the delayed DELETE cannot hit `K2`. Orphan recovery must DELETE the **stored** key, never `hashToKey(block_id)`. |
| R5 | Writer's probe misses a remote claim because the claim commits at `LOCAL_QUORUM` | **Fails the option.** The claim must commit with global visibility (`ALL` or equivalent). Note the same requirement extends to the **orphan** row, which is the fence for most of the destructive window once the `blocks` row is gone. |
| R6 | A non-local `QUORUM` GC read (2 of 3), writer LQ in the DC not contacted | **Forbidden.** Empirically red on the three-DC harness. |
| R7 | One DC down during the authorizing read | Fail closed; no DELETE. Already the shipped behaviour. |
| R8 | Who installs the next life, and with what CAS | `blocks` is one row per logical block and the install is `INSERT … IF NOT EXISTS`. **A:** the writer waits until the row is gone, so a plain insert suffices. **B:** the writer waits only until GC drops the row — same plain insert, but the orphan and the authorization must be re-scoped per B.1. Neither needs a new CAS or a second table. |
| R9 | Writers in two DCs both leave the wait and both mint a key | Exactly one incarnation becomes **canonical**. `UpsertBlockMetadata` sets no serial level, so it inherits the session's — `LOCAL_SERIAL` in the shipped cluster profiles (`configs/config-eu.cluster.yaml:27`, `configs/config-usa.cluster.yaml:27`) — and two local Paxos rounds can both apply. Harmless while keys are derived (both write the same key); **not** harmless once each DC mints its own. The installing statement needs a `SERIAL` serial phase. `SERIAL` picks a canonical winner; it does **not** prevent the loser's PUT. |
| R10 | Writer stalls after `Probe=Reusable(K1)`; GC fences, sees 0, authorizes `DELETE K1`; the writer resumes, finds the object missing, and repair-PUTs | Must not re-PUT a condemned key. Confirmed live: the `Reusable` branch of `StoreUploadedBlockForProbe` (`upload_reuse.go:152-174`) does `ObjectExists` → repair-PUT with **no** fence re-check, and `EnsureReusableBlockPresent` passes `beforePut = nil` (`:205`). The one caller that supplies `beforePut` (`v2/blocks.go:996`) uses it for the staging cap, not for the fence. Under minted keys the clean rule is **never repair a condemned incarnation — mint a new one**. |
| R11 | `K1`'s delete completes, `K2` is created and live, then `K1`'s `cleanupBlockMapping` runs | The SHA-1→SHA-256 mapping now belongs to `L` and must survive. `RecoverS3Orphans` guards with `BlockExists` (`worker.go:1404`); `processBlock` has no equivalent (`worker.go:1256`), safe today only because the orphan fence blocks resurrection outright — which is exactly what B gives up. **Load-bearing under B**; preventive under A. B.3 proposes removing the coupling entirely. |
| R12 | Any conditional statement on the `blocks` partition still runs at `LOCAL_SERIAL` after the others are raised | **Fails the whole fence.** The two levels are different quorum domains, so a `LOCAL_SERIAL` round can miss an in-flight `SERIAL` proposal and one straggler invalidates every other statement's guarantee. See the inventory below — it is **eleven** statements, not six. |
| R13 | `INSERT orphan(L,K1)` succeeds, `DELETE blocks` row fails persistently, the claim is released as stale after 15 min | **New, and it is a data-loss path under B.** The row is now live, unclaimed, and pointing at a key already authorized dead. Today `ProbeBlockReuse` refuses it because `hasOrphan` outranks everything (`block_references.go:927`); B removes that. Trace it through: probe returns `NeedsPut` (refs are 0), the writer mints `K2` and PUTs it, then `UpsertBlockMetadata`'s `INSERT … IF NOT EXISTS` does not apply, `readBlockIdentityForRepair` finds a healthy row, and `ensureBlockIdentityRow` (`block_references.go:639`) **returns nil** — the upload reports success while the canonical row still points at `K1`, which recovery then deletes. **Required outcome:** the orphan fences the *key*, not the logical block — the probe and the install must both refuse when `blocks.storage_key` equals an orphaned key. This is what makes step 6 of the naive protocol ("`K1` is irrevocably retired once the orphan is written") false: retirement completes when the canonical row stops naming `K1`. |
| R14 | A candidate enqueued for `K1` is processed after `K1` died and `K2` was installed | The claim CAS must bind the key (`IF … AND storage_key = K1`), or GC claims a life it never verified. `processBlock` re-reads `GetBlockInfo` after the claim, which limits the damage, but the CAS should still name the life. |

R8 and R13 decide whether Option B is viable. R3, R9, R10, R11 and R12 decide whether it
is correct once it is.

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

**And the rule does not stop at `blocks`.** Under any drop-row-first design the fence
lives in `gc_s3_orphans` for most of the destructive window — from the moment the
canonical row is dropped until the physical delete is confirmed — so that partition
enters the same serial domain. It carries five more conditional statements
(`internal/gc/store_cassandra.go:1577, 1598, 1630, 1685, 1717`) plus one **unconditional**
`DELETE` that clears the fence (`internal/db/block_references.go:1199`), which B.1
requires to become conditional.

The rule, then: *every* conditional statement that can touch `blocks` **or**
`gc_s3_orphans` uses serial phase `SERIAL`, with the regular commit level chosen per
statement (`ALL` for the claim and orphan fences, `QUORUM` or higher for the install).
This discipline is worth keeping whichever option wins.

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

X1 stays closed even then — with no `blocks` row the writer probes `NeedsPut`, mints a
fresh key, and a stale DELETE of the old key can never reach it. What expiry destroys is
**the durable record that the old key still has to be deleted**: the bytes leak with
nothing pointing at them, and under B the single-outstanding-delete invariant silently
becomes untrue.

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
| `hashToKey` / `StorageKeyForHash` | Deterministic locator | First-life helper only; the live locator is the persisted `storage_key` |
| `DeleteBlock(hash)` / `BlockStoreDeleter` | Derives the key from the hash | Delete the exact key captured in the GC work. **No exact-key delete exists today** |
| `GetBlock` / `BlockExists` / `PutBlock` / `PutBlockData` / `PutBlockAuto` / `PutBlockAutoDirect` | All derive via `hashToKey` | Take an exact key, or resolve one first. Exact-key `Put`/`Exists`/`Get` variants already exist and are the model |
| `CheckBlocksExist` (canonical reader, primary `/check-blocks`) and `CheckBlocks` / `CheckBlocksParallel` (legacy fallback) | Both HEAD a key derived from the hash | **The dedup oracle stops being answerable by a derived HEAD and becomes a database question.** This is the widest change in the whole option and should not be filed under "locator" |
| `S3Store.Put(ctx, blockID, …)` | Second derivation layer: `s.key(blockID)` on what callers already pass as a key | Make the key parameter mean a key |
| `canonical_block_reader.go:238` | Rejects persisted ≠ derived | Use the persisted key; validate org/hash/format instead |
| `upload_reuse.go` — `ResolveNeedsPutBlockStore` **and** the `Reusable` branch of `StoreUploadedBlockForProbe` | Two reject sites; the second also repair-PUTs at the derived key | Mint on `NeedsPut` / `RepairableStub`; never repair a condemned incarnation (R10) |
| `UpsertBlockMetadata` | `INSERT … IF NOT EXISTS` inheriting the session serial level | Store the exact key; raise the serial phase to `SERIAL` so one incarnation wins globally (R9) |
| `ClaimBlockDelete` / `FinalizeBlockDelete` | Conditional on `gc_state` / `gc_claim_id` only | Bind the life: `AND storage_key = K1` (R14) |
| `ReleaseBlockClaim`, `ReleaseStaleBlockClaim`, `ReleaseBlockDeleteClaim`, the stub-repair pair, both backfills | Conditional statements on `blocks`, inheriting the session serial level | Serial phase `SERIAL` — the one-serial-domain rule admits no exceptions on this partition (R12) |
| `gc_s3_orphans` (+ `gc_s3_orphans_by_day`) | PK `((org_id, block_id))`; grew `external_sha1`/`recovery_phase` (migration 007) and `representation_id` (009) | Add the exact `storage_key` to both; recovery and `ListS3OrphansByDay` must not `hashToKey`; the clear becomes conditional on the key |
| `StartBlockDeleteOrphan` | `INSERT … IF NOT EXISTS`, then **resets** an existing row to `pending_s3` | Real mutual exclusion: on conflict, release the claim and postpone (B.1) |
| `gcS3OrphanInitialScanLookbackDays = 90` | Cold-start horizon, matched to the TTL | Redefine together with the TTL removal |
| `RecoverS3Orphans` | Re-verifies `BlockExists(L)` and `BlockHasReferencesGlobal(L)` | Inherit authorization from the orphan row; delete the exact key (B.1) |
| `cleanupBlockMapping` | Deletes the SHA-1 mapping unconditionally from `processBlock`; `RecoverS3Orphans` guards with `BlockExists` | Decouple from the physical lifecycle (B.3), or add the same guard to `processBlock` (R11) |
| `StagePublishAttemptReferences` | Insert only | Insert, then check for an unorphaned canonical incarnation (B.2) |
| `RegisterUploadedBlock` | Write `up:` then check the fence | Keep as-is; rematerialization mints |

---

## Open questions to settle before anything is accepted

Ordered so the cheapest decisions come first.

1. **R13 — the orphan must fence the key, not the logical block.** Settle the probe and
   install predicates. Nothing else in B is safe until this is decided.
2. **R12 — one serial domain**, across eleven `blocks` statements and the
   `gc_s3_orphans` partition.
3. **B.1 — the authorization amendment.** Write the new argument for
   `RecoverS3Orphans` and rewrite the registry's X2 paragraph to match.
4. **R3 — the `pub:` post-check**, in the B.2 form.
5. **R10 — never repair a condemned incarnation.**
6. **R9 — the losing PUT.** `SERIAL` picks a canonical winner, but the loser's minted key
   is recorded nowhere: no `blocks` row, no orphan row. With derived keys a stray object
   was reconcilable with a single HEAD; with minted keys it needs a bucket inventory,
   which does not exist. Classify it as an X3-class leak and decide whether an inventory
   story is in scope.
7. **B.3 — the SHA-1 mapping.** Decouple and accept the growth, or build the reaper.
8. **The TTL package**, all four changes together.
9. **GC drain capacity.** The queue loop in `internal/gc/worker.go` is strictly serial at
   `gc.batch_size = 100` on a `gc.worker_interval = 30s` tick. Any design that fences
   globally adds per-block WAN LWTs to that loop. This is a throughput question, not a
   latency one, and "GC is not the hot path" does not answer it. Independent of which
   option wins.
10. **Fail-closed observability.** `EACH_QUORUM` couples GC availability to every DC, and
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
above: never-reused physical keys as the only thing that closes X1, the one-serial-domain
rule, and the exact-key recovery requirement.
