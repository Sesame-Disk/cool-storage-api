# X1 — Physical-life handoff architecture (D0)

**Status:** accepted architecture freeze. Documentation only. X1 remains OPEN.
**Parent:** `17f487c5d` (`main` containing #200)
**Branch:** `docs/x1-physical-life-handoff`
**Scope:** reconstruct why the current design exists, separate mixed
responsibilities, and freeze the productive PR series. This file is the source
of record for the accepted X1 closure architecture.
**Not this PR:** runtime code, CQL, effective migrations, worker, storage,
references, or configuration.
**Production/fleet:** `GC_ENABLED=false` remains mandatory fleet-wide.
Activation (`GC_ENABLED=true`) is a separate PR after X1 CLOSED. X1 CLOSED is
not permission to enable deletion.

Verified against `main` at `17f487c5d` on 2026-09-02. Claims labelled
`CURRENT / TRANSITIONAL` are observations of that tree. Claims labelled
`DECIDED` are the accepted target and are **not** current production behavior.

---

## How to read this document

Every architectural sentence is one of:

| Label | Meaning |
|-------|---------|
| `PROVEN` | Measured or implemented with a concrete citation |
| `CURRENT / TRANSITIONAL` | What production code does today |
| `DECIDED` | Accepted target; not yet production |
| `OPEN` | Unresolved; must not be treated as closed |
| `PENDING RE-EVALUATION` | Old requirement that may not survive the new model |
| `SUPERSEDED` | A previous candidate that is no longer the active roadmap |

Do not quote a `DECIDED` step as if `processBlock` already did it.
Do not quote a `#199` candidate harness step as production.

`P` is never a synonym of `storage_key`. `K` is the key. `P = (storage_class, storage_key)`.

---

## Why this supersedes the previous closure candidate

#199 (`docs/GC-X1-STRICT-NONOVERLAP-CHARACTERIZATION.md`) asked whether
`blocks(L)=P1/deleting/handoff=true` must remain the only destructive authority
until `DeleteExact(K1)` finishes, and only then may Finalize remove the
canonical row.

#199 already reasoned with independent physical lives. #185 (minted
single-use incarnations; `P1`/`P2`, `K1`/`K2`) had merged before that
characterization. The candidate was a **deliberately conservative strategy
even with those independent lives**: keep `blocks(P1)` canonical until
physical retirement.

#201 supersedes that strategy because a durable exact handoff
(`orphan COMMITTED(P1,D1)`) keeps authority over P1 without leaving
`blocks(P1)` as the canonical row through cleanup.

#199's candidate harness used:

```text
Commit D on blocks
→ DeleteBlockByStorageKey(K1)
→ FinalizeBlockDelete
```

and treated `gc_s3_orphans` as not required for the first Finalize.

**That candidate order is SUPERSEDED as the closure architecture.** The
measurements remain valid observations. The accepted target does **not** require
strict physical non-overlap and does **not** require `Delete K1` before
`Finalize P1`.

What it requires:

> before retiring `blocks(P1)`, a durable exact recovery authority must exist
> that lets GC finish P1 without consulting L's future canonical life.

That authority is `gc_s3_orphans` after a confirmed handoff: not a mutex on L,
and not mere discovery.

---

## 1. The observation that changes the design

Historically logical identity and physical identity were almost the same:

```text
L = logical hash
K ≈ blocks/<org>/<sha256>
```

A new materialization of L could land on the same key. Then:

```text
GC authorized on L
→ metadata disappears
→ writer rematerializes L
→ same K exists again
→ GC continues
→ DELETE K
```

could destroy newly live bytes. That justified conservative defenses:

```text
orphan(L) exists → writer blocked
```

and forced recovery to re-consult logical references.

That assumption no longer describes the architecture.

Since P2/#185:

```text
P = (storage_class, storage_key)
physical(P1) != physical(P2)
P1 != P2
K1 != K2
```

Fresh minted lives have unequivocal physical separation. Do not hedge this as
`normally K1 != K2`. A new physical life does not revive the previous one's
identity. That changes the roles of `blocks`, `block_references`, and
`gc_s3_orphans`.

---

## 2. Vocabulary

```text
L = logical content identity (canonical block id / hash)

P = one concrete physical life
  = (storage_class, storage_key)

K = the storage_key half of P

D = one concrete destructive authority
  bound to one P and one lifecycle/claim identity
```

Example:

```text
L
P1 = (hot-s3-na, K1)
D1 = delete authority of P1

later:
P2 = (hot-s3-na, K2)

P1 != P2
K1 != K2
```

`claim` is reversible. `D` is the irreversible committed authority. They may
share a `claim_id`, but they are not the same privilege. After
`CommitBlockDeleteOrphanHandoff`, that exact `(P, claim_id, claimed_at)` is D
and cannot be released or taken over.

---

## 3. Definitive roles

### `block_references` — logical liveness

`up:`, `pub:`, `fs:` answer:

> is there currently a logical operation or durable object that needs content L?

They are not physical GC authority and not physical generations. Before the
destructive cut they protect the currently canonical P.

### `blocks` — canonicity and active authority

Answers:

> which physical life P currently represents L?

and, while GC is still in the handoff:

> who holds the claim / destructive authority on that P?

```text
blocks(L) = P1
```

or:

```text
blocks(L) = P1 + deleting + D1
```

While `blocks` names P1, P2 cannot be the canonical life. The install remains
single-use `INSERT IF NOT EXISTS` (`PROVEN`, #185).

### `gc_s3_orphans` — destructive continuation / crash recovery

Final role (`DECIDED`):

> durably keep the authorization GC already won to retire one exact physical
> life P1, so work can continue after `blocks(P1)` is gone and after the
> original worker died.

Orphan **can** be DELETE authority, stated precisely:

| State | DELETE authority? |
|-------|-------------------|
| before handoff / `PREPARED` | **no** |
| after confirmed handoff / `COMMITTED(P1,D1)` | **yes**, continuation only for `DeleteExact(K1)` |

It does not create the original authorization. It receives it from the
authority that was in `blocks`.

`CURRENT / TRANSITIONAL`: the table is still also a writer fence. Mere
existence of a `gc_s3_orphans` row for `(org, L)` makes
`ProbeBlockReuse` return `BlockedByGC`, `BlockDeleteFenceActive` return true,
and `ValidateBlockRepairAuthority` return `Blocked`. That is why orphan was
used as a mutex on L. G4 removes that use, and only after W1+W2+G3. Until
G4, Finalize may vacate `blocks(L)` while writers remain fenced by the
orphan.

---

## 4. Authority is not discovery

```text
gc_s3_orphans
  = canonical recovery authority   (DECIDED; CURRENT: mixed fence + recovery row)

gc_s3_orphans_by_day
  = discovery / scheduling index   (CURRENT and DECIDED)
```

The projection finds work. The canonical row says which exact lifecycle owns
authority.

Equally, `candidate`, `queue`, `pending`, and DLQ are selection, scheduling,
and work-item lifecycle. They cannot invent a destructive target.
`PROVEN` by #199 F0b1/F0b2: queue loss and a behind-cursor candidate are not a
recovery root.

---

## 5. PROVEN facts

### Exact locator — #181

Canonical destructive paths consume the persisted locator.
GC must not invent the key from L in order to destroy bytes.
`blocks.storage_key` and `gc_s3_orphans.storage_key` are required; empty fails
closed. `ValidatePhysicalLocator` binds the persisted key before DELETE.

### Independent physical lives — #184 then #185

Fresh installs mint a new physical identity:

```text
physical(P1) != physical(P2)
P1 != P2
K1 != K2
```

This is required for minted lives, not typical. Canonical install is
single-use. #184 is the structural exact-key store prerequisite. #185 is the
mint/install closure (P2/R9/R24).

### Repair does not freely recreate a condemned P — P3/#187

Repair is tuple-aware and non-creating. Mutations bind to exact P.
A residual late PUT remains; that is H, classified separately (`OPEN`).

### Exact destructive authority — #189–#194

```text
candidate → P
claim → P + attempt identity
release / takeover → exact authority
handoff → exact P/D
Finalize → exact P/D
```

#189 is P4a (claim). #194 is P4b-2 (handoff/orphan/finalize bound to stored D).
`CommitBlockDeleteOrphanHandoff` is the irreversible `blocks.gc_orphan_handoff`
commit (migration 019). Migration 020 is the never-deleted D tombstone.

### Work-item identity — #190

Candidate / queue / pending / DLQ distinguish P1 from P2.
The orphan half remains open: `P4c-orphan` (`CURRENT / TRANSITIONAL` and
`DECIDED / OPEN` implementation). `gc_s3_orphans` PRIMARY KEY is still
`((org_id, block_id))`. `storage_class`, `storage_key`, `gc_claim_id`, and
`gc_claimed_at` are payload, not identity. Two lives of one L still collide.

### #199 — characterization, not protocol

Conservative protocol characterization **after** independent physical lives
already existed. It was not forced by reusable `K`.

Demonstrated:

```text
P2 does not install while P1 occupies blocks
after P1 is retired, P2 can install with K2 != K1
a stale D1 does not acquire authority over K2
```

Also reproduced:

```text
H: DELETE K1 → late writer PUT K1
F0b / F2 rediscovery and settlement holes
```

Current-protocol contrast in that PR: production Finalize-then-DELETE can
remove `blocks` while K1 still exists. That is `CURRENT / TRANSITIONAL`, not a
defect against the accepted target.

### #200 — BorrowedFS publication through HEAD

Demonstrated the CreateFileFromBlocks TOCTOU through HEAD, and the handshake:

```text
own up: before the global zero-proof
→ GC sees liveness
→ GC releases

zero-proof first
→ late up:/pub:
→ cannot undo that proof retroactively
→ a fence can make the writer lose
```

This is experimental evidence, **not production protocol**. Productive hooks
are nops (`//go:build !integration`). W1 productizes the handshake in
`CreateFileFromBlocks` (own `up:` before claim; exact-placement authority
re-validation, not a bare fence, immediately before HEAD -- see W1 fix 1/2
below). The hooks remain test-only.

---

## 6. Central architectural decision

Abandon as the end-state:

```text
P1 must stay canonical until DeleteExact(K1) finishes
```

That is strict physical non-overlap. It wastes `P1 != P2`.

Accepted model (`DECIDED`):

```text
P1 canonical
        ↓
GC proves it may condemn P1
        ↓
durable handoff P1 → orphan(P1,D1)
        ↓
blocks(P1) may be retired          (G3; writers still fenced by orphan(P1))
        ↓
        ├──────── GC continues DeleteExact(K1)
        │
        └──────── G4: writers may create P2/K2
```

Therefore physical cleanup of P1 and canonical life P2 **may overlap** after
G4 removes the orphan-as-L writer fence. G3 only vacates `blocks`. That
overlap is safe only because `P1 != P2` and every old destructive operation
remains bound exactly to P1.

---

## 7. The critical point of X1 is the handoff

The most important property is no longer "Delete before Finalize".

It is:

> before retiring `blocks(P1)`, a durable exact authority must exist that lets
> GC finish P1 without depending on L or on the future canonical P.

Gapless transfer:

```text
blocks authority(P1,D1)
        ↓
orphan authority(P1,D1)
```

Never:

```text
blocks disappears
+
no durable recovery authority
```

Never:

```text
orphan(P1) later adopts P2
```

---

## 8. Cassandra cannot CAS across these tables

`blocks` and `gc_s3_orphans` are separate Paxos partitions. Migration 019
states this explicitly. Do not design as:

```text
atomic {
    write orphan
    commit blocks
}
```

The protocol must tolerate crashes between steps with monotonic states.
The chosen shape is **prepare → commit → promote** (`DECIDED`).

---

## 9. CURRENT production protocol (transitional)

`processBlock` today (`internal/gc/worker.go`, verified 2026-09-02):

```text
Claim(P1, D_attempt)                         # reversible
→ BlockHasReferencesGlobal(L) @ EACH_QUORUM  # authorizing zero-proof
→ CommitBlockDeleteOrphanHandoff(P1,D1)      # irreversible D on blocks
→ StartBlockDeleteOrphan                     # INSERT gc_s3_orphans + 020 published
→ FinalizeBlockDelete                        # DELETE blocks row
→ DeleteBlockByStorageKey(K1)                # physical
```

Crash notes that are true of **current** code:

- After D commit and before orphan INSERT, `blocks` still exists.
  Resume is `CommittedOwner` of stored D. No PREPARED row exists.
- After orphan INSERT and before Finalize, both `blocks` and orphan exist.
  Writer fence is true on either.
- After Finalize and before S3, orphan is the remaining recovery row.
  Writer fence is true on mere orphan existence for `(org, L)`.
- `CommittedOwner` re-reads `BlockHasReferencesGlobal` as a **contradiction
  detector** after handoff (`CURRENT / TRANSITIONAL`). Refs>0 →
  `committed_pending`, no delete. This does not authorize a new delete.
- `RecoverS3Orphans` independently re-reads `BlockHasReferencesGlobal` before
  S3 (`CURRENT / TRANSITIONAL`). Refs>0 refuse the delete. That branch does
  **not** set `phaseErr`; the day cursor can advance, the row can leave the
  working set, and the 90-day TTL can then destroy the durable record
  (`ISSUE-GC-REFERENCED-ORPHAN-LIFECYCLE-01`). Storage leak, not live-data
  delete. This is why pending recovery authority must not TTL out.
- Recovery does **not** scan canonical `gc_s3_orphans`. It walks
  `gc_s3_orphans_by_day` from a persisted UTC-day cursor (`RecoverS3Orphans`).
  A canonical row whose `_by_day` publish never happened is invisible to
  restart. `StartBlockDeleteOrphanProjectionUnconfirmed` already names that
  window. CURRENT still has TTL, so the writer fence eventually lifts. G2
  PREPARED without TTL must not inherit that hole.

`PREPARED` is **not** current production protocol.

---

## 10. DECIDED handoff protocol

Not current `processBlock`. Implementation is G2/G3.

### A — Claim

```text
Claim(P1,D1)
```

Still reversible.

### B — Global zero-proof

```text
BlockHasReferencesGlobal(L) @ EACH_QUORUM
```

`refs > 0` → release the claim.
`refs = 0` → this attempt may start the handoff.

This is the liveness proof that authorizes **starting** the handoff. It is
not irreversible condemnation. PREPARED still has no S3 authority. D is
committed only by the later CAS on `blocks`. A later abort before that CAS
must leave P1 canonical and usable.

### C — Write recovery PREPARED

Before the irreversible point:

```text
gc_s3_orphans(P1,D1, PREPARED)
```

must be durable and discoverable.

`PREPARED` does **not** authorize S3 DELETE. It means only: this lifecycle was
preparing a destructive handoff on P1.

If the process dies here:

```text
orphan PREPARED
blocks not yet committed
```

recovery may abort PREPARED only by first **revoking that exact D's ability
to commit** on the same `blocks` partition that `CommitBlockDeleteHandoff`
writes. A SERIAL observation that D is not yet committed does **not**
suffice: it does not stop the owner from committing afterwards.

```text
PREPARED exists

unsafe (forbidden):
  SERIAL observe D not committed
  → worker Commit D(P1,D1) applies
  → recovery DELETE PREPARED
  ⇒ blocks = D1 committed, orphan PREPARED gone

required:
  PREPARED(D1)

  D1 still exact owner AND handoff = null
    → Abort/Release exact P1,D1
      IF exact owner AND handoff = null
      (competes with Commit D; only one wins)

      released
        → D1 can no longer commit
        → then remove PREPARED(D1)
        → P1 remains canonical; writers may use it

      committed (handoff true for exact D1)
        → keep PREPARED → promote COMMITTED

      ambiguous
        → keep PREPARED → fail closed

      not-owner
        → do not treat this as a single outcome
        → SERIAL-classify exact D1 on blocks
          (CURRENT ReleaseBlockClaim collapses every
          not-applied CAS to NotOwner; G2 must not)

            D1 committed (exact owner + handoff true)
              → promote PREPARED(D1) → COMMITTED

            D1 irrevocably lost commit capability
            (already released / no claim, or superseded by D2)
              → settle/remove PREPARED(D1) only
              → do not destroy a later D2 row

            ambiguous/unavailable
              → keep PREPARED → fail closed
```

`not-owner` covers at least two valid recoveries. Both must converge;
pending PREPARED must not rely on TTL, and until G4 it still fences L.

```text
Case A — abort won, crash before deleting PREPARED
  Abort/Release D1 APPLIED
  → crash
  → retry Abort D1 → NotOwner
  → D1 not owner, handoff still null
  → settle/remove PREPARED(D1)

Case B — stale takeover
  PREPARED(D1), D1 dies
  → D2 takes ownership (handoff still null)
  → Abort D1 → NotOwner
  → D1 superseded, cannot commit
  → settle/remove PREPARED(D1); leave D2
```

Do not substitute “PREPARED prevents stale takeover”. Even then Case A
remains. Takeover while PREPARED exists is classified, not forbidden, in
D0; G2 may narrow it only without losing Case A.

SERIAL observation that D is not yet committed, **while D1 is still exact
owner**, must not remove PREPARED. That is the Commit race. SERIAL
classify after Abort returned not-owner is how crash recovery converges.

### D — Commit D on `blocks`

After PREPARED is confirmed:

```text
CommitBlockDeleteHandoff(P1,D1)
```

The lifecycle becomes irreversible: no release, no takeover, no late ref that
retroactively cancels D1. `blocks(P1)` still exists.

### E — Promote orphan to COMMITTED

```text
PREPARED → COMMITTED
```

only after proving exactly:

```text
blocks(L)=P1, D1, handoff=true
```

in the same SERIAL exact domain as the `blocks` row. An ambiguous or
unavailable observation keeps PREPARED and fails closed. Do not invent
COMMITTED, and do not abort, from that observation. Abort remains the CAS
in C, not a SERIAL read.

Crash:

```text
D committed
+
orphan still PREPARED
```

`blocks` still exists, so recovery can re-observe exact D1 and finish the
promote.

### F — Retire `blocks(P1)`

Only when `orphan(P1,D1)=COMMITTED` is confirmed.

Then `FinalizeBlockDelete(P1,D1)` may retire P1's canonicity.

From that point `orphan(P1,D1)` is the complete durable authority to finish
the physical delete. `blocks` is no longer required for P1.

---

## 11. Finalize frees `blocks(L)`; writers stay fenced until G4

G3 after `orphan COMMITTED(P1,D1)`:

```text
FinalizeBlockDelete(P1,D1)
```

From that point `blocks(L)` is no longer required for P1. The canonical slot
is vacant. That is **not** writer permission to mint and install P2.

`CURRENT / TRANSITIONAL`: `InstallBlockMetadata` is still a plain
`INSERT IF NOT EXISTS` and does not consult `gc_s3_orphans`. The writer fence
is the **read** (`ProbeBlockReuse` / `BlockDeleteFenceActive` /
`ValidateBlockRepairAuthority`). Any `gc_s3_orphans` row for `(org, L)` still
blocks those paths. While that fence remains, a well-formed writer cannot
productively install P2.

The accepted productive state

```text
blocks(L)=P2/K2
gc_s3_orphans contains P1/K1/D1
```

is `DECIDED`, and it is **G4's** demonstration, not G3's. G4 is also what
removes the orphan-as-L mutex. W2 is a hard gate for that overlap (late
publication must not recreate dependence on condemned P1). W2 is **not** a
gate for G3 Finalize: until G4, `orphan(Pold)` still fences writers.

---

## 12. Recovery of P1 after handoff (`DECIDED`)

Once COMMITTED, `orphan(P1,D1)` must not ask:

```text
does blocks now say P2?
```

to retarget, and must not ask:

```text
did new refs of L appear?
```

to revoke D1.

Its job is `DeleteExact(K1)` using the persisted `storage_class` and
`storage_key` of P1.

`blocks(L)=P2` is expected, not a contradiction. Recovery never touches K2.

`CURRENT / TRANSITIONAL` still re-checks refs and still fences writers on
orphan(L). That is the protocol we are replacing, not the target.

---

## 13. Late refs

Before the zero-proof, `up:` / `pub:` / `fs:` can save P1.

After irreversible D, late `up:` / `pub:` must not make P1 live again.
That is only safe once the full writer side is closed (W2).

The GC protocol must not change this semantic until the writer PRs are merged.
Until then, post-D refs remain a contradiction/veto (`CURRENT / TRANSITIONAL`).

---

## 14. Writer protocol (`DECIDED`)

Not only BorrowedFS. Any writer that will depend on the currently canonical P
must have own liveness before depending on it irreversibly.

```text
own up:
   ↓
fence / canonical validation
   ↓
work
   ↓
pub:
   ↓
HEAD
   ↓
fs:
```

Liveness must be continuous:

```text
up:  -------------------------+
                            pub: --------------------+
                                                  fs:
```

Do not accept:

```text
up expires
      [zero liveness]
                     pub
```

TTL alone is not a proof. There must be renewal or protocol overlap.

---

## 15. BorrowedFS — W1

The next writer PR productizes #200:

```text
BorrowedFS → own up: → fence → publication
```

Writer wins: `up` visible → GC EACH_QUORUM observes ref → release.
GC wins: fence visible (claim or committed D) → writer aborts its P1 work.

A late writer retries. A new materialization uses the then-canonical life or
mints P2.

Merge criteria: writer-first → GC releases; GC-first → no HEAD; no liveness
gap; no accidental hot-path WAN/Paxos explosion. Do not touch GC/orphan schema
in W1.

---

## 16. R31 remains a blocker (`OPEN`)

Do not declare the writer side closed after BorrowedFS.

Still:

```text
HEAD may have applied
→ fs not yet durable
→ temporary pub:
→ process dies
```

If `pub:` expires and nobody reconciles HEAD, GC could later see zero refs
incorrectly.

W2 must close `up → pub → fs`, including ambiguous HEAD / publication
settlement. R31 closes only with durable reconciliation or an equivalent proof
that a possibly-accepted publication never loses its definitive liveness.

---

## 17. Orphan/recovery identity — G1 / P4c-orphan

`DECIDED / OPEN` implementation.

Canonical recovery identity must distinguish:

```text
orphan(P1,D1) != orphan(P2,D2)
```

`(org, L)` is not enough. A mutation of P1/D1 must not name P2/D2's row.

Do not freeze an exact PRIMARY KEY here without reviewing access patterns.
Prefer reusing already-persisted authority:

```text
storage_class
storage_key
claim / delete identity
```

before inventing another global generation.

Migration 020 already clusters by `claim_id` on `gc_block_delete_lifecycles`:
D1 and D2 of the same L occupy different tombstone rows. That does **not**
close P4c-orphan; the recovery table GC actually resumes from is still
`(org, L)`. G1 also includes the minimum durable discovery root required
before G2 may create PREPARED (see §18 and G1 in §25). The identity
schema alone is not that root.

---

## 18. The `_by_day` projection must carry enough identity

`CURRENT`: after migration 014, `gc_s3_orphans_by_day` is identity-only:

```text
(first_seen_day, bucket, first_seen_at, org_id, block_id)
```

No P, no D. Discovery of `(org, L, first_seen)` cannot distinguish independent
physical lifecycles.

`DECIDED`: discovery may be non-authoritative, but it must not be ambiguous.
The projection, or its replacement, must locate exact `(P, D)` of the
lifecycle it enumerated. It must also be a restart-findable root: a
canonical PREPARED/COMMITTED whose discovery publish never happened must
still be found (G1, before G2). Queue/pending/cursor are not that root.

---

## 19. TTL — mandatory change (`DECIDED`)

If after retiring `blocks(P1)` the orphan row is the only durable authority
left for `DeleteExact(K1)`, that row **must not disappear by TTL while still pending**.

`CURRENT / TRANSITIONAL`: both `gc_s3_orphans` and `gc_s3_orphans_by_day` still
have `default_time_to_live = 7776000` (90 days). R28 records that this is a
per-value TTL, not a row-lifetime ceiling, and that expiry can destroy a
pending durable record. `ISSUE-GC-REFERENCED-ORPHAN-LIFECYCLE-01` is the
refs-deferred leak of that same record.

Decision: pending canonical orphan/recovery **does not expire automatically**.
Deletion is explicit after settlement. Discovery needed to find it must not
depend on a TTL lasting “long enough”.

Retention/TTL may exist for already-terminal, metrics, or historical audit
rows — not for pending authority/recovery. This absorbs the dangerous half of
R28.

---

## 20. Conceptual orphan states (`DECIDED`)

Do not freeze production names yet. Freeze equivalents of:

```text
PREPARED
COMMITTED            (includes DELETE_UNCERTAIN: still COMMITTED)
PHYSICAL_COMPLETE
SETTLED
```

| State | Meaning |
|-------|---------|
| `PREPARED` | no S3 authority; abort by CAS against exact D, then remove. `not-owner` is classified: committed → promote; lost commit capability / superseded → settle that exact PREPARED; ambiguous → keep and fail closed |
| `COMMITTED` | D transferred; authorizes only `DeleteExact(P)` of that exact P. Ambiguous or unknown DELETE stays here (`DELETE_UNCERTAIN`) and retries `DeleteExact`. |
| `PHYSICAL_COMPLETE` | physical DELETE confirmed (success or known-absent). Never DELETE again. Only settlement to `SETTLED`. |
| `SETTLED` | lifecycle finished; canonical recovery may be deleted explicitly |

```text
COMMITTED
  → DELETE ambiguous/unknown → remain COMMITTED / DELETE_UNCERTAIN
  → success or known-absent  → PHYSICAL_COMPLETE

PHYSICAL_COMPLETE
  → never more DELETE
  → only settlement
```

`DELETE_UNCERTAIN` is not a fifth production name. It is COMMITTED still
authorized to `DeleteExact`. Promoting to `PHYSICAL_COMPLETE` and then
issuing another destructive op for a previously ambiguous response is
forbidden: if the DELETE was not known, the lifecycle was never
`PHYSICAL_COMPLETE`.

`CURRENT` phases (`pending_s3`, `pending_mapping_cleanup`) are transitional
names and must not be copied into the new protocol as if they were PREPARED /
COMMITTED.

---

## 21. H — late PUT (`OPEN`, must not reclassify yet)

#199 showed `DELETE K1 → late PUT K1`. Do not assume it disappears.

Under the accepted architecture the X1 question is not “did bytes K1
reappear?”. It is:

> can that stale writer make P1 canonical again, or make a live reference
> point at P1?

Required matrix (E1, not D0):

```text
orphan COMMITTED P1
→ blocks P1 Finalized
→ P2/K2 installs
→ orphan Delete K1
→ stale writer PUT K1
→ stale writer attempts metadata/publication
```

Must demonstrate:

```text
P1 does not return to blocks
P1 is not referenced canonically again
D1 does not touch K2
P2 remains intact
```

If that is green, late K1 is X3 (orphan bytes / leak), not X1 data loss, and H
leaves X1. If it can regain canonicity, H still blocks X1.

D0 does **not** reclassify H as X3.

---

## 22. R18 / R27 — `PENDING RE-EVALUATION`

Do not implement the old protocol before this transition.

The old reasoning:

```text
orphan → late ref → postpone delete → reproject/retry until it vanishes
```

depended on a late logical reference being able to revive the physical life
the orphan was retiring. In the final model that is false. After handoff,
`D1(P1)` is irreversible.

R18/R27 stay `PENDING RE-EVALUATION` until writer continuity and handoff are
implemented.

What remains mandatory: every outstanding PREPARED or COMMITTED orphan must
be rediscoverable and retryable until it converges. The **minimum recovery
root** is G1 (before G2 may create PREPARED). G5 hardens scheduling after
that root exists. Queue/pending/behind-cursor candidate are not this root
(`PROVEN` #199 F0b1/F0b2).

---

## 23. Lifecycle 020 — keep (`OPEN` whether it stays forever)

Keep it. Do not declare it redundant.

It still provides settlement / tombstone classification during the transition
(`CURRENT`: `AlreadyFinalized` requires an exact published `(P,D)` certificate;
terminal D cannot republish an authorizing orphan).

After exact P/D orphan identity, PREPARED/COMMITTED, explicit physical
complete, and exact settlement exist, audit whether 020 is `KEEP`,
`SIMPLIFY`, or `REMOVE`. Do not decide that in D0.

---

## 24. E6 — keep separate (`OPEN`)

Queue arbitration can duplicate work or lose liveness
(`DequeueBatch` still takes no lease).

Under the new model a defective queue must not be able to duplicate
destructive authority, because orphan/authority will be exact and write-once.
Do not mark E6 closed until that is demonstrated. Reclassify in E1.

---

## 25. Productive roadmap

### D0 — this document

Docs freeze. No runtime.

### W1 — BorrowedFS own-liveness

Productize #200. No GC/orphan schema.

**Current status (2026-09-02):** implemented in this PR. `CreateFileFromBlocks`
establishes `up:<session>` for each distinct BorrowedFS block before session
claim and re-validates the exact observed physical placement immediately
before HEAD (after `pub:` may already be staged — the #200 handshake, not the
§14 full-writer ideal). Fence abort must drop that `pub:`. R31/W2 and X1
remain open; keep `GC_ENABLED=false` fleet-wide.

**W1 fix 1 (2026-09-02):** a bare `BlockDeleteFenceActive` check cannot see the
terminal state where GC has already fully retired a block (Finalize + settled
orphan): no `gc_state='deleting'` row and no orphan row are left to report a
fence, yet the placement this commit observed is gone. The pre-HEAD check now
re-validates the exact observed `(storage_class, storage_key)`, whose
`Blocked`/`Changed` outcomes are a strict superset of the old fence check.
Proven red without the fix and green with it on real Cassandra+MinIO by the
`gcFullyRetiredBeforeLateOwnPin` leg.

**W1 fix 2 (2026-09-02):** fix 1's first cut reused `ValidateBlockRepairAuthority`,
which reads at `BlockAuthorityStrong` (SERIAL) -- reserved by
`docs/UPLOAD-PAXOS-HOT-PATH-X1-CHARACTERIZATION.md` for the cold pre-PUT
repair boundary and kept off the dedup path
(`TestP3SerialReadsStayOffTheDedupPath`). Calling it once per BorrowedFS block
would pay one global Paxos round trip per block on this dedup hot path,
exactly the "accidental hot-path WAN/Paxos explosion" the W1 merge criteria
rule out. The pre-HEAD check now calls the new
`db.ValidateBorrowedFSPublicationAuthority`, which shares the same
exact-placement classification but reads at `BlockAuthorityAdvisory`
(LOCAL_QUORUM). Safety here comes from ordering, not a downstream CAS: the
caller has already durably written its own `up:<session>` pin before this
runs. That function's own doc comment is the source of record for the full
four-case interleaving proof (writer-pin-first; GC claim landed but D not yet
committed; GC fully retired P1 before the pin, where replication lag can only
bias the read toward the conservative `Blocked` answer, never fabricate
`Authorized`; and a released/superseded GC claim) -- summarized here only to
avoid duplicating and drifting from it. Re-proven green with the same leg;
guarded by `TestValidateCommitBlockPublicationFencesStaysOffSerialAuthority`
(caller picks the right function; renamed from
`TestValidateBorrowedFSFencesStaysOffSerialAuthority` when the check below was
generalized) and `TestValidateBorrowedFSPublicationAuthorityUsesAdvisoryReads`
(that function issues Advisory, not Strong, reads).

**W2 first slice -- SessionUpload parity in `CreateFileFromBlocks`
(2026-09-02):** `ensureCommitBlockOwnLiveness` (renamed from
`ensureBorrowedFSOwnLiveness`) and `validateCommitBlockPublicationFences`
(renamed from `validateBorrowedFSFences`) now run over every distinct ready
block in a `CreateFileFromBlocks` commit, not just BorrowedFS ones. A
SessionUpload block already carries its own `up:<session>` reference from
`/blocks/upload` time; this step upserts that same reference before session
claim via `db.AddProvisionalBlockReferenceWithExpiry` -- an idempotent
overwrite of one Cassandra key either way, but concretely it *renews* the
deadline when the reference is still present and *recreates* it when the
original has already lapsed (slow client past TTL, or GC having released it
after an earlier zero-proof). This narrows, but does not close, the "TTL
alone is not a proof" gap §14 names: recreating the pin after a lapse does
not retroactively prove continuous liveness held the whole time, and does not
by itself revoke any delete GC already committed against the block's prior
placement. What actually makes this safe through HEAD is the SAME thing that
made W1 safe -- `validateCommitBlockPublicationFences` re-validating the
exact observed placement immediately before HEAD, so a post-D placement is
rejected instead of published, gap or no gap. That is the #200 handshake, not
the §14 full-writer ideal (line 1004 above): continuous `up: -> pub:` overlap
is not proven and remains an R31 property.
The pre-HEAD check's position is unchanged (immediately before
`UpdateLibraryHeadFromSnapshot`, after `pub:`/the commit row already exist):
moving it earlier, on the theory that `pub:` then makes it redundant, was
considered and rejected -- there was only ever one check, not two, and moving
it earlier while deleting it here would reopen the exact window
(`stagePendingPublishedFiles` -> `queuePendingPublishedFileRepairs` ->
`insertCommit`) W1 closed. A pre-`pub:` check is not unsound in principle --
it would need up: to structurally guarantee protection all the way through to
pub: being durable, and a finite TTL refresh alone does not prove that
handoff; the late exact-placement check establishes the characterized pre-HEAD
safety cut. It does not close full R31 continuity. New evidence:
`internal/integration/sessionupload_own_liveness_test.go` (six named legs,
`SESAMEFS_REQUIRE_SESSIONUPLOAD_OWN_LIVENESS_EVIDENCE=1`, kept as a separate
gate from W1's eight-leg one since that struct's field list is pinned and
W1/BorrowedFS-scoped). Separately, new test-only seams on `UpdateLibraryHead`
(`SetLibraryHeadAmbiguousCASForTest` / `SetLibraryHeadConfirmVisibleForTest`,
zero production behavior change) drive the ambiguous-HEAD-CAS branch inside a
real `CreateFileFromBlocks` call and confirm the PRE-EXISTING
`published_block_reference_repairs` repair/sweep mechanism resolves both
outcomes for this funnel
(`internal/integration/createfilefromblocks_ambiguous_head_test.go`).
**Update (2026-09-04, post-freeze):** "converges both directions" described
the sweep's shape at this doc's `17f487c5d` snapshot -- a binary
reachable/unreachable classification. Later commits on this branch
(`71ecfcefc`, `8c69a2347`, `0832caeb2`) replaced that with three verdicts:
reachable → promote; confirmed terminal via the durable
`library_publication_revocations` witness → clean up; everything else
(live-unreachable, soft-deleted, unknown, or a read failure) → retain `pub:`
and the repair row indefinitely, not clean up. "Converges" is no longer
accurate for that third outcome -- it deliberately does not converge to a
terminal action until the library is confirmed reachable again or genuinely,
durably terminal. See `docs/CHANGELOG.md` "W2a" for the authoritative current
model. This is
a **slice** of W2, not its closure: every other `CONDITIONAL`/`UNKNOWN` funnel
below remains open, `GC_ENABLED=false`, X1 remains OPEN, and the
`resolveLibraryHeadUpdateError` confirmed-lost/`ErrLibraryHeadConflict`
asymmetry (§16) is documented but deliberately not fixed here -- it is shared
infrastructure also used by SeafHTTP/OnlyOffice/batch_operations/trash, which
this PR has no dedicated evidence for.

### W2 — Full publication continuity / R31

Audit every `CONDITIONAL` / `UNKNOWN` funnel in
`docs/R3-LIVENESS-CONTINUITY.md`. Close `up → pub → HEAD → fs`, ambiguous
HEAD, and crash after a possibly-applied HEAD.

Absolute merge criterion:

> once D(P1) is committed, no legitimate writer may later publish durable
> liveness that depends on P1.

Zero-proof only authorizes starting the handoff. This sequence must remain
valid:

```text
zero-proof
→ PREPARED
→ abort before D
→ claim released
→ P1 still canonical
→ a writer later uses P1 legitimately
```

Do not treat zero-proof itself as irreversible condemnation.

Only after W2 may post-D refs stop being a contradiction/veto.

### G1 — P4c-orphan exact identity + minimum durable discovery

GC/schema only. Do not change writer policy or delete order yet.

Must ship together, **before G2 creates PREPARED in production**:

```text
exact (P,D) on canonical orphan
exact (P,D) on discovery
no TTL for pending authority / pending discovery
canonical PREPARED/COMMITTED cannot exist without a durable
way to find it after crash
```

`CURRENT`: `RecoverS3Orphans` finds work only by walking
`gc_s3_orphans_by_day`. Canonical `gc_s3_orphans` is not scanned. Crash
between canonical INSERT and `_by_day` publish:

```text
gc_s3_orphans:        PREPARED(P1,D1) exists
gc_s3_orphans_by_day: empty
restart:              recovery never sees it
```

Until G4 that orphan still fences writers on L. The new design also
forbids TTL as cleanup. Queue, pending, and behind-cursor candidates are
not a substitute (`PROVEN` #199 F0b1/F0b2).

Frozen property (mechanism is G1's to choose: atomic-ish publish, repair
scanner, canonical partition, second index, …):

> once PREPARED exists, restart can eventually find it.

Real Cassandra tests:

```text
P1/D1 + P2/D2 coexist
settle P1 leaves P2 untouched
stale projection P1 cannot clear P2
same L, same timestamps, distinct P/D still distinct rows
canonical PREPARED without _by_day is still found after restart
```

### G2 — PREPARED → COMMITTED handoff

**Requires G1.** G2 must not land durable PREPARED without G1's recovery
root. Write-ahead orphan. Do not retire `blocks` before COMMITTED.
Implements the frozen PREPARED abort and **not-owner classifier** in C.
Abort CAS and Commit D compete on the same `blocks` partition. SERIAL
observation while D1 is still owner must not remove PREPARED. After Abort
returns not-owner, SERIAL-classify exact D1 and converge (promote, or
settle that PREPARED).

Crash matrix: after PREPARED; abort-vs-commit race; abort applied then crash
before PREPARED delete (Case A); stale takeover while PREPARED(D1) (Case B);
during D; after D before COMMITTED; ambiguous COMMITTED observation;
canonical PREPARED whose discovery publish has not happened (must still be
found — G1). Each state recoverable without inventing destructive
authority. Ambiguous abort CAS never deletes PREPARED. A stranded
PREPARED(D1) whose D1 cannot commit must not depend on TTL to disappear.

### G3 — Canonical retirement after committed handoff

`processBlock`:

```text
orphan COMMITTED(P1,D1) → Finalize blocks(P1,D1)
```

After Finalize, `blocks(L)` is architecturally free: P1 is no longer
canonical, and `orphan COMMITTED(P1,D1)` is the complete durable authority
to finish P1. Writers are still blocked if `orphan(P1)` remains an L-fence.

Do **not** treat `blocks=P2` + `orphan=P1` as a G3 result. That coexistence
is not productive until G4 removes the writer fence.

Physical delete of P1 is authorized by orphan. It may still be attempted
inline, but semantically it belongs to recovery:

```text
DeleteExact(K1)
  ambiguous/unknown → remain COMMITTED / DELETE_UNCERTAIN; retry DeleteExact
  success or known-absent → PHYSICAL_COMPLETE → SETTLED
```

A crash after Finalize is not dangerous because orphan COMMITTED already
exists.

### G4 — Remove orphan from writer fencing; detach post-D refs

Only after W1+W2+G3.

Change `ProbeBlockReuse`, `BlockDeleteFenceActive`, and
`ValidateBlockRepairAuthority` so mere `orphan(Pold)` does not block the
current/new P. The writer consults canonical `blocks(L)` and its state.

Then a writer can productively install P2 while P1 cleanup continues.
This PR must demonstrate `blocks=P2` + `orphan=P1` is a valid state.

COMMITTED recovery: late `refs(L)` no longer cancel or postpone D1. Recovery
of P1 does not re-check global refs for DELETE permission. Its authority is
the exact certificate received at handoff.

W2 is required before that overlap is safe against late publication. G3
Finalize does not wait for this fence removal; the orphan fence covers
writers until this PR.

### G5 — Recovery scheduling hardening

Not the first time restart can find PREPARED or COMMITTED. That property is
G1, required before G2.

G5 hardens scheduling and operational convergence on top of that root:

```text
cursor repair
projection repair
lost scheduling
scanner starvation
pending/queue independence
operational convergence
```

`orphan COMMITTED` must still not depend on `gc_queue`, `gc_pending_items`,
the scanner cursor, or unbounded TTL to *survive*. G5 may still absorb
remaining F0b/F2 operational liveness after the G1 root exists.

### E1 — X1 final evidence / reclassification

No new architecture unless a test shows a defect. Run the matrix in the
planning note (writer-first, GC-first, BorrowedFS, R3 funnels, R31, P1→orphan→P2
coexistence, stale D1 vs K2, H late K1 PUT, crash at every handoff phase,
queue/pending/cursor/discovery loss, 3-DC, LWT ambiguity, S3 ambiguity, stale
orphan mutation).

Reclassify H, R18, R27, R28, E6, 020, R30/R31 into
`CLOSED` / `SUPERSEDED` / `X3` / `LIVENESS` / `TECH DEBT` / `STILL X1`.

**Only E1 may declare `X1 CLOSED`.**

### A1 — GC activation

Separate PR. X1 CLOSED is not GC enabled.

Migration/schema final, operational preflight, dry run, metrics/alerts, staged
rollout, rollback, then `GC_ENABLED=true`.

---

## 26. Target end-state

```text
L
│
├── references(L)              = logical liveness
│
├── blocks(L)=P2               = current canonical physical life
│
└── gc_s3_orphans(P1,D1)       = GC finishing an older physical life
```

Valid:

```text
P1 cleanup still running
P2 currently live
```

because `P1 != P2`. Old GC knows only P1/K1. New writer depends only on P2/K2.
This coexistence is the G4 end-state, not a G3 result.

There is no **cross-life** destructive or canonical ABA between P1 and P2:
`D1` cannot name `K2`, and `P2` cannot occupy `P1`'s identity. Same-key
resurrection of `K1` after DELETE (`DELETE K1 → late PUT K1`) is H, which
remains `OPEN`. D0 does not claim that physical reappearance is impossible.

---

## 27. Review questions for every future X1 change

```text
Which L?
Which exact P?
Who currently owns authority on that P?
Is this liveness, canonical authority, recovery authority, or discovery?
Can this operation accidentally act on a different P?
```

A function that takes only `(org, L)` but makes a physical decision about a
historical life is suspect.

---

## 28. Status ledger

| Claim | Status |
|-------|--------|
| `P=(storage_class, storage_key)` | `PROVEN` |
| minted `P1 != P2` / `K1 != K2` | `PROVEN` (#185) |
| reversible claim ≠ committed D | `PROVEN` (#189–#194) |
| exact P/D destructive authority | `PROVEN` (#189–#194) |
| work-item identity includes P | `PROVEN` (#190) |
| orphan PK still `(org, L)` | `CURRENT / TRANSITIONAL` (P4c-orphan) |
| current orphan is a writer fence | `CURRENT / TRANSITIONAL` |
| current Finalize → physical DELETE | `CURRENT / TRANSITIONAL` |
| current recovery re-checks refs | `CURRENT / TRANSITIONAL` |
| current 90-day orphan TTL | `CURRENT / TRANSITIONAL`; **not** acceptable for post-handoff pending authority |
| lifecycle 020 present | `CURRENT`; keep; redundancy `OPEN` |
| R31 | `OPEN` |
| H = X3 | `OPEN` — must prove; do not reclassify in D0 |
| R18/R27 | `PENDING RE-EVALUATION` |
| orphan as post-handoff DELETE authority | `DECIDED` |
| exact-P/D orphan identity | `DECIDED / OPEN` implementation (G1) |
| durable PREPARED discovery | `DECIDED` / G1; required before G2 |
| PREPARED → COMMITTED handoff | `DECIDED / OPEN` implementation (G2; requires G1) |
| recovery scheduling hardening | `DECIDED / OPEN` implementation (G5; not the first recovery root) |
| P1 cleanup may overlap P2 life | `DECIDED` / G4 |
| late refs cannot revoke committed D | `DECIDED` / depends W2 |
| orphan removed from writer path | `DECIDED` / G4 after W1+W2+G3 |
| X1 | `OPEN` |
| GC activation | **out of X1 closure** (A1) |

---

## 29. Related documents

| Document | Role after D0 |
|----------|----------------|
| this file | source of record for the accepted architecture |
| `docs/GC-X1-CLOSURE-OPTIONS.md` | historical option comparison; **not** the active roadmap |
| `docs/GC-X1-STRICT-NONOVERLAP-CHARACTERIZATION.md` | #199 evidence; candidate order SUPERSEDED as closure architecture |
| `docs/R3-BORROWEDFS-HEAD-CHARACTERIZATION.md` | #200 evidence; not production protocol; W1 consumes it |
| `docs/R3-LIVENESS-CONTINUITY.md` | R3 funnel inventory; W2 consumes it |
| `docs/UPLOAD-FENCE-FINDINGS-REGISTRY.md` | finding ids (X1, R-rows) unchanged |

---

## 30. D0 merge criteria

Do not merge this docs PR unless:

1. every `PROVEN` row cites concrete evidence (PR and/or code path);
2. no future decision is written as current behavior;
3. `P` is distinguished from `K`;
4. current orphan still participates in writer fencing;
5. current recovery still re-checks refs;
6. current worker still Finalizes before physical delete;
7. P4c-orphan is still open;
8. current TTL is not presented as acceptable for post-handoff pending authority;
9. R31 stays open;
10. H is not reclassified;
11. R18/R27 are pending re-evaluation;
12. 020 remains;
13. this PR has no runtime code, schema, or config;
14. `GC_ENABLED=false`;
15. activation is explicitly outside X1 closure;
16. G3 does not claim productive P2 install; G4 owns `blocks=P2` + `orphan=P1`;
17. `PHYSICAL_COMPLETE` never authorizes another DELETE;
18. PREPARED abort CAS-revokes exact D's commit capability before removing PREPARED; SERIAL-only observe-then-delete while D1 is still owner is forbidden;
19. W2's irreversible frontier is `D committed`, not zero-proof;
20. D0 does not claim “no physical ABA”; H stays OPEN;
21. minted lives are `K1 != K2`, not `normally K1 != K2`;
22. PREPARED `not-owner` is classified (promote vs settle exact D1 vs fail closed); it is not “keep forever”;
23. G2 requires G1 durable PREPARED discovery; G5 is scheduling hardening, not that first recovery root.

The contract test `TestX1PhysicalLifeHandoffPlanIsDocumented` pins these
invariants against this file and against current `processBlock` /
`RecoverS3Orphans` / schema. `TestX1PhysicalLifeHandoffCurrentWriterStillFencesOnOrphan`
pins each named writer path to its `gc_s3_orphans` helper.

---

## Decision in one paragraph

The accepted architecture does not try to stop P1 and P2 from existing
physically at the same time. It stops them from **sharing authority**.

Handoff:

```text
blocks owns P1
       ↓
GC proves zero liveness
       ↓
orphan PREPARED
       ↓
D committed
       ↓
orphan COMMITTED owns retirement of P1
       ↓
blocks P1 removed   (G3; writers still fenced by orphan(P1))
       ↓
G4 removes orphan from the writer fence
       ↓
P2 may become canonical while orphan(P1) continues DeleteExact(K1)
```

From there `orphan P1 → DeleteExact(K1)` and `blocks P2 → K2` proceed
independently. That is the real benefit of unique physical lives: stop using
an old orphan as a mutex on the logical block, and make it what it should have
been — GC's durable authority over one exact historical physical life.
