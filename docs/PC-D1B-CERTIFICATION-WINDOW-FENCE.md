# PC-D1B.4 — Certification-window lifecycle fence: decision and characterization

> **Superseding scope decision (2026-09-29, [X1-CRITICAL-PATH.md](./X1-CRITICAL-PATH.md)):**
> PC-D1B.5 is **PRE-GC only**. The destroyers that can falsify a witness
> (D1–D3, §5) run only inside GC, so with `GC_ENABLED=false` no active path
> can invalidate a witness; the fence no longer gates a productive consumer
> (a consumer still owns CW-M17). Before implementing, §18 is re-scoped to
> what D1–D3 need; CW-M1..M34 are not automatically required. The
> investigation, model and characterization below stand unchanged. WIP is
> parked on `feat/pc-d1b5-certification-window-fence-runtime`.

**Status:** architecture decision + executable characterization. **Decision
CLOSED / MERGEABLE (2026-09-24)** after both CW-M33 accepted-Paxos races and
G21/G22 mutations passed on Cassandra 5.0.9. CW-M34's in-flight writer ordering
property also has model, mutation and real-Cassandra 3-DC evidence. Neither
finding adds production runtime permits or recovery; PC-D1B.5 remains mandatory
before destructive GC activation or a productive consumer. No productive
runtime, schema, certifier, writer, GC, mapping-authority or consumer change.
**Base:** `main@62a2c0e0` (PR #228 merged). **Runtime follow-up:** PC-D1B.5.
**Tracking:** `ISSUE-PCD1B4-CERTIFICATION-WINDOW-FENCE-01`.
`GC_ENABLED=false` remains mandatory. File:line references are to
`main@62a2c0e0`; symbol names are authoritative when lines drift.

## 0. Decision in one page

```text
DECISION

Certification lifecycle authority = the canonical libraries row, in the global
SERIAL HEAD Paxos domain, extended with a per-library DESTRUCTION FENCE:

    continuity_destruction_epoch       timeuuid             (E)
    continuity_destruction_pending     map<uuid, timeuuid>  (P: token -> generation)
    continuity_destruction_superseded  timeuuid             (S: highest generation
                                                             replaced before completing)

Witness shape (unchanged):  (H, V)
Witness validity (unchanged):
    HEAD == H  AND  certified == H  AND  contract == V  AND  deleted_at == null

Certification:
    ... walk, exact-P, liveness, GC authority (unchanged) ...
    capture (E0, P0, S0) with a SERIAL read BEFORE the final revalidation pass
    P0 != {}  -> NOT_CERTIFIED (identity_destruction_pending), no witness
    final revalidation pass, plus: when S0 is non-null, every covered row is
      reaffirmed through EACH_QUORUM with its identical claimed content at
      USING TIMESTAMP ts(S0)+1, regardless of local WRITETIME, and re-read
    witness CAS: IF head_commit_id = H AND deleted_at = null
                    AND continuity_destruction_epoch = E0        <- new

Destruction of witness-covered state while the canonical row exists
(commit / fs_object source delete, permanent fs: reference removal):
    intent LWT     g = fresh timeuuid with ts(g) > ts(E)
                   SET E = g, P[t] = g, certified = null, contract = null,
                       S = max(S, P[t]_old)     (only if t had another owner)
                   IF E = E_read AND P[t] = P[t]_old AND S = S_read
                       AND created_at = created_at_read
                                                     (global SERIAL)
    destroy        only after the intent APPLIED, every destructive write
                   USING TIMESTAMP ts(g); acknowledged at a CL the
                   certifier's final reads intersect
    completion LWT DELETE P[t]  IF P[t] = g         (global SERIAL)
                   only after every destructive write is acknowledged, and
                   only while generation g still owns token t

    t = durable logical destruction identity: one per durable GC QueueItem
    g = execution ownership (one per intent)       t != ownership

    A superseded generation may be a paused process that resumes and writes.
    Its tombstones carry ts <= ts(S), and every certified cell carries a
    write time > ts(S), so it can never make a destruction effective against
    certified state (§10.3).

Proven-uncovered cleanups (D4/D5: commits proven never to be HEAD) take a
ProvenUncoveredCleanup capability instead and write no fence state. A
canonical-absence bypass uses `GlobalCanonicalAbsenceProof` only when a
global-SERIAL HEAD Paxos settlement/read both settles the Paxos history up to
its own linearization point and itself returns canonical ABSENT, followed by a
subsequent `EACH_QUORUM` canonical absence read. A present SERIAL result,
error or timeout never mints proof; a local `CanonicalLibraryExists == false`
never authorizes destruction. Cassandra 5.0.9 freezes the SERIAL-ABSENT then
EACH_QUORUM-ABSENT order. The isolated fixture pauses an accepted HEAD proposal
after `StorageProxy.doPaxos` accepts it and before commit; the hard delete uses
`ballot-1` so Cassandra's LWW reconciliation can expose the late proposal. G21
removes settlement; G22 ignores a present SERIAL result; both turn RED.

Productive witness authority: read only at global SERIAL (or inside an LWT of
the same domain). A LOCAL_QUORUM witness observation never authorizes work.

Soft-delete / restore / hard delete:  NO CHANGE. They are not witness
lifecycle events: they do not change the certified dependency set.

Identity claims: immutable, never touched by the fence.
```

The fence is **per library** because every piece of witness-covered state is
library-scoped (§6). It adds **no** Paxos to the upload/HEAD hot path, **no**
per-identity state, **no** fan-out and **no** tree-wide lock. It is the
smallest mechanism that the exhaustive model (§11) found safe **without
assuming that a superseded process stops writing**; every weaker variant has a
concrete counterexample.

**Scope of "only destruction falsifies a witness".** This statement describes
the #228 baseline (`main@62a2c0e0`), where no SHA-1-only file can be certified.
The mapping-projection scenario recorded below as F4 was a real follow-up at
that point: after #233, a witness could rest on authority A while a mutable
mapping row later made ordinary readers resolve B. **PR #233 closes F4** by
freezing the ordinary projection to the durable authority at the dominant
timestamp, verifying its write time at EACH_QUORUM, pinning productive readers
to LOCAL_QUORUM, and inventorying production mapping mutations and readers.
The former PRE-CONSUMER issue `ISSUE-PCD1B-MAPPING-PROJECTION-STABILITY-01` is
therefore CLOSED by #233; PC-D1B.5 does not own or absorb it.

## 1. The question this PR answers

> After the last revalidation performed by #228, which event can make the
> certification false before or after the witness exists, and which operation
> must be serialized with which to prevent it?

Answer, proven against the current runtime (§8, §11):

- **Only the destruction of witness-covered state** can make a witness false:
  deleting the `commits` row of `H`, deleting a reachable `fs_objects` row, or
  removing a permanent `fs:<library>:<fs_id>` reference (after which GC may
  delete the bytes). Semantic replacement at the same key is already
  impossible because identity claims are immutable (#230/#231).
- HEAD movement already invalidates a witness and is already fenced (the
  witness CAS predicates HEAD; HEAD writers are global-SERIAL LWTs).
- Library soft-delete, restore and hard delete do **not** change what the
  witness asserts. Today they matter only because they are a window in which
  a destroyer can act unobserved (R3b, R10).
- The destroyers must be serialized with the witness CAS, and the certifier
  must refuse to certify while a destruction is in flight.

## 2. Baseline that is not reopened

#228 guarantees, in order: authoritative commit identity → authoritative
reachable fs_object identities → exact tree → exact minted P → physical bytes
→ permanent `EACH_QUORUM` liveness → GC authority → final source/claim/physical
revalidation → HEAD-fenced global-SERIAL witness CAS with SERIAL settlement.
This PR does not revisit any of it. The problem starts after the final
revalidation:

```text
final source/claim revalidation        library_continuity_certifier.go:365-474
            ↓   WINDOW
beforeWitnessCAS hook                   :476-478 (integration builds only)
witness CAS                             :479   -> library_continuity.go:155-191
UNKNOWN settlement (SERIAL read)        :483-507, :1032-1062
```

## 3. Two separate problems

| | Question | Current answer | Fence answer |
|---|---|---|---|
| **A — certification window** | Can a witness be born already stale? | **Yes** (R4, R5, R3b, R11b) | No: an in-flight destroyer either changed `E` (CAS fails) or is pending at capture (refused) |
| **B — stored witness lifecycle** | When does a correct witness stop being valid? | HEAD change or `deleted_at`; **destruction does not invalidate it** (R10, R10b) | Additionally at the first destruction intent, which clears it in the same Paxos write |

## 4. Current runtime

### 4.1 Final frontier and settlement

- The final pass re-reads library state at LOCAL_QUORUM, rechecks every block's
  exact P, GC authority, bytes and permanent liveness (`EACH_QUORUM`), then the
  commit projection and every fs_object projection against their SERIAL claims.
- The witness CAS is
  `UPDATE libraries SET continuity_certified_head_commit_id = H, continuity_contract_version = V IF head_commit_id = H AND deleted_at = null`,
  pinned to `LibraryHeadSerialConsistency` (global SERIAL).
- On UNKNOWN the certifier SERIAL-reads `(head, certified, contract, deleted_at)`
  and reports CERTIFIED iff `ContinuityWitnessValidFor(V) && head == H`.
- Nothing productive calls the certifier or reads the witness
  (`ContinuityWitnessValidFor` has no production caller besides settlement;
  `AdvanceLibraryCertifiedFrontier` and `CommitLibraryContinuityWitness` have
  no production caller). I9 holds today.

### 4.2 SERIAL domains on the `libraries` partition

| Writer | Statement shape | Serial domain |
|---|---|---|
| Witness CAS (×2), frontier advance | LWT `IF head_commit_id = ? … AND deleted_at = null` | global SERIAL (pinned) |
| Sync `updateLibraryHeadWithStats`, v2 `UpdateLibraryHead`, `InitializeLibraryHeadIfUnset` | LWT `IF head_commit_id = ?` / `= null` | global SERIAL (pinned) |
| Unpublished rollback `deleteUnpublishedLibraryRow` | LWT `DELETE … IF head_commit_id = null` | global SERIAL (pinned) |
| Soft-delete, restore, hard delete | plain writes in a LoggedBatch | **none** (client timestamps) |
| Hard-delete lease `gc_library_hard_delete_locks` | LWT on another table | **inherits `database.serial_consistency`** (may be LOCAL_SERIAL) — side finding F2 |

## 5. Lifecycle mutation inventory

Derived from the source and frozen by
`internal/db/pcd1b4_lifecycle_mutation_inventory_test.go`
(lifecycle statements and destroyer call sites) together with the existing
`TestIdentityWritersAreInventoried` (every commits/fs_objects writer/deleter is
confined to the identity gateway). The guards mechanically freeze the
currently recognized production sites: a new soft-delete/restore/row-delete or
witness statement, a new direct call of a destroyer primitive (including a new
wrapper), an alias of a primitive, and a raw `DELETE FROM block_references`
turn them red (guard mutations G1–G5, G8–G10). They do not trace new callers of
an already-inventoried wrapper (e.g. the GC store's `DeleteFSObject`); that is
why PC-D1B.5 makes the capability a parameter of the destructive primitives,
which is the structural boundary. The inventory is defense in depth.

| # | Operation | Source | Tables | CL | LWT / serial | Conditional | Races certification | Races stored witness | Modifies authority | Invalidates identity |
|---|---|---|---|---|---|---|---|---|---|---|
| L1 | User/admin soft-delete | `api/v2/write_helpers.go:954` `softDeleteLibrary` | libraries.deleted_at, deleted_libraries, aggregates, admin read models | session (LOCAL_QUORUM) | no | no | yes (R1, R12) | yes (R8) | no | no |
| L2 | GC user/org cascade soft-delete | `gc/store_cassandra.go:5730` `SoftDeleteLibrary` | same as L1 | session | no | no | yes | yes | no | no |
| L3 | Restore from trash | `api/v2/write_helpers.go:1000` `restoreDeletedLibrary` | libraries (updated_at, DELETE deleted_at), deleted_libraries | session | no (hard-delete lease LWT on another table) | reads canonical row under lease | yes (R3) | yes (R10) | no | no |
| L4 | Permanent delete (API) | `api/v2/library_delete_helpers.go:52` `hardDeleteLibraryRowsFn` | DELETE libraries row, libraries_by_id, read models; marker | session | no | no | yes (R9, R9g) | yes | no | no (row + witness removed) |
| L5 | GC cascade hard delete | `gc/store_cassandra.go` `PrepareLibraryHardDelete` (build; execution under final exclusion) | same as L4 + policy rows | session | no | no | yes | yes | no | no |
| L6 | Unpublished-library rollback | `api/v2/write_helpers.go:896` + `library_rollback.go` batch | libraries row (LWT), then whole commits/fs_objects partitions | LWT + session | LWT, global SERIAL | `IF head_commit_id = null` | no (no HEAD ⇒ no witness) | no | no | only of a never-published library |
| D1 | GC commit delete | `gc/worker.go:3135` → `store_cassandra.go` `DeleteCommit` → `DeleteCommitIdentity` | commits row | EACH_QUORUM | claim SERIAL read | guard modes | never the HEAD commit (Phase 5 keeps the HEAD chain; Phase 3/cascade need canonical absence), but its cascade feeds D2/D3 | yes | no (claim survives) | **yes** |
| D2 | GC fs_object delete | `gc/worker.go:3201` → `DeleteFSObject` → `DeleteFSObjectIdentity` | fs_objects row | EACH_QUORUM | claim SERIAL read | guard modes | **yes**: Phase 5 cascade, Phase 6 execute-time TOCTOU | yes | no | **yes** |
| D3 | GC permanent reference removal | `gc/worker.go:3875` → `RemoveBlockReference` | block_references `fs:` rows | session | no | lease fence | **yes** (with D2) | yes | no | **yes** (liveness) |
| D4 | Failed-publish cleanup | `api/v2/publish_repair.go` `cleanupFailedPublishDeleteCommitFn` (fs variant has no production caller) | commits row | EACH_QUORUM | claim SERIAL read | no | no (attempt commit ≠ HEAD) | no | no | of a non-HEAD commit |
| D5 | v2 initial-commit discards | `api/v2/fs_helpers.go` `InitializeLibraryHeadIfUnset`, `DiscardLosingInitialCommit` | commits row | EACH_QUORUM | claim SERIAL read | refuses the winning HEAD | no | no | no | of a non-HEAD commit |
| D6 | Publish-attempt reference cleanup | `db/block_references.go` `removePublishAttemptReferenceFn` | block_references `pub:` rows | session | no | no | no (`pub:` is not a certified dependency) | no | no | no |
| H1 | HEAD advance (Sync, v2, initializer) | §4.2 | libraries.head_commit_id | LWT | global SERIAL | `IF head_commit_id = ?` | fenced (CAS predicates HEAD) | invalidates (HEAD never repeats, §7.4) | no | no |
| C1 | Library creation (6 production `INSERT INTO libraries` statements, all binding `library_id` to a fresh UUID) | `INSERT INTO libraries` with `uuid.New()` | libraries | session | no | no | no | no | no | no |

Not present anywhere: a production deleter of `block_id_mappings` (insert-only),
or of `identity_authority_claims`.

## 6. Identity authority interaction

- **Claims survive source deletion** (#230 rule 1; `DeleteCommitIdentity` /
  `DeleteFSObjectIdentity` delete the source row only, after verifying the
  claim with a global SERIAL read).
- **Re-creating a deleted key** is a write under the existing claim: the same
  digest is idempotent, a different digest is `IdentityAuthorityConflict`
  (R6, R7 on real Cassandra). So semantic ABA (lifecycle A → lifecycle B at
  the same key) is already impossible for commits and fs_objects; **only
  presence** (live ↔ absent) remains unfenced. Re-creating the claimed digest
  restores truth, so writers are never destroyers and never need the fence.
- **Blind deletion is refused** unless the claim can be read at global SERIAL:
  in the 3-DC leg a gateway delete issued from an isolated DC returns
  `IdentityAuthorityUnavailable` and removes nothing.
- **Sharing.** `commits` and `fs_objects` are keyed `((library_id), …)`;
  permanent references use referrer `fs:<library>:<fs_id>`. Within one library
  an fs_id is shared by many commits (content addressing — the root of the
  Phase 5 bug), but **no witness-covered row is shared across libraries**. A
  destroyer therefore always knows the one library row to fence: fan-out is 1
  and no reverse index is needed. Physical blocks are org-shared but are
  protected by the per-library referrers. `block_id_mappings` is org-scoped
  (shared across libraries); it has no deleter today, and mapping authority
  must stay non-destructible (§15).

## 7. Semantics frozen by this decision

### 7.1 Soft-delete
Not a witness lifecycle event. Validity is already false while `deleted_at` is
set; the witness columns persist. A plain soft-delete can be invisible to the
witness CAS's Paxos read (cross-DC, R12), producing a witness on a library the
user already deleted; that witness is **true** (nothing was destroyed) and is
invalid once `deleted_at` converges. Serializing soft-delete with HEAD writers
is `ISSUE-LIB-DELETED-FENCE-01` and a **consumer** prerequisite (a consumer must
not publish into a deleted library), not a witness prerequisite.

### 7.2 Restore
Resumes the same lifecycle. A witness may survive a trash round trip **iff no
destroyer ran during trash**, because every destroyer clears it through its
intent. No restore epoch: the model proves lifecycle writes stay safe without
one (`TestPCD1B4ModelLifecycleNeedsNoRestoreEpoch`), so adding one would not be
minimal. The unsafe revival observed today (R10) is closed by destroyer
participation, not by changing restore.

### 7.3 Hard delete
The canonical row and its witness disappear together; library ids are never
reused (every creator mints `uuid.New()`, frozen by
`TestPCD1B4LibraryCreatorsMintFreshIDs`). A witness CAS whose Paxos ballot is
later than a concurrent plain row delete leaves a **ghost row** with only
witness cells and a null HEAD (R9g): never a valid witness, but it makes GC's
canonical-existence checks see a library — side finding F1 (GC domain).

### 7.4 HEAD
HEAD values never repeat on main: Sync promotion requires
`parent(target) == current HEAD` with the parent bound by an immutable commit
claim, and v2/auto-merge mint fresh ids. The selected fence does **not** rely
on this: the model's `head/moves-and-repeats` scenario is safe even when HEAD
returns to `H`, because a destroyer that acted while HEAD was elsewhere cleared
the witness.

### 7.5 Commit / fs_object delete and re-create
Presence changes are fenced by destruction intents; content changes are
impossible (claims). A delete + same-digest re-create inside the window yields
a correct certification (R6).

## 8. Race matrix

`UNSAFE` = a reader can treat a witness as valid while covered state is gone.
Evidence: **M** = model (`internal/db/pcd1b4_certification_window_model_test.go`),
**C** = single-node real Cassandra
(`internal/integration/pcd1b4_certification_window_characterization_test.go`),
**3DC** = isolated three-datacenter fixture
(`scripts/pc-d1b4-certification-window-multidc-characterization.sh`).

| Race | Interleaving | CURRENT behavior (main) | Verdict | Target invariant | Fence behavior (PC-D1B.5) | Evidence |
|---|---|---|---|---|---|---|
| R1 | final reval → soft-delete → CAS (same DC) | NOT_CERTIFIED / witness_not_applied, no witness | SAFE | I1 | unchanged | C |
| R2 | soft-delete → final reval → CAS | final reval reads `deleted_at` → NOT_CERTIFIED / library_deleted | SAFE | I1 | unchanged | code (`:376`) |
| R3 | final reval → soft-delete → restore → CAS | CERTIFIED; tree intact | SAFE (true by accident) | I1 | unchanged (no destroyer ⇒ truth holds) | C, M |
| R3b | R3 with a destroy during trash | CERTIFIED over a destroyed fs_object | **UNSAFE** | I1 | intent bumps E → CAS NOT_APPLIED | C, M |
| R4 | final reval → delete commit H → CAS | CERTIFIED; commit row gone | **UNSAFE** (no product path today) | I1 | intent → CAS NOT_APPLIED | C, M |
| R5 | final reval → delete reachable fs_object → CAS | CERTIFIED; recertification says missing_fs_object but the stale witness stays | **UNSAFE** (product path: GC Phase 5/6 only) | I1 | intent → CAS NOT_APPLIED; pending at capture → refused | C, M |
| R6 | final reval → delete + re-create same key → CAS | same digest re-created and CERTIFIED; divergent digest refused | SAFE | I3 | unchanged (destroy half participates) | C |
| R7 | final reval → divergent authorization attempt → CAS | IdentityAuthorityConflict; CERTIFIED over unchanged identity | SAFE | I3, I6 | unchanged | C |
| R8 | witness → soft-delete | witness persists, validity false while deleted | SAFE | I2 | unchanged | C |
| R9 | witness → hard delete | row and witness gone | SAFE | I2 | unchanged | C |
| R9g | CAS commit timestamp after a concurrent plain hard delete | ghost row: witness cells, HEAD null; validity false; GC sees a library | SAFE for validity; GC finding F1 | I2 | unchanged; F1 tracked separately | C |
| R9i | any LWT on the row (the future intent LWT included) ordered after a concurrent plain hard delete | HEAD-less ghost row with the LWT's live cells; validity false; GC sees a library | SAFE for validity; PRE-GC `ISSUE-PCD1B-CONTINUITY-LWT-GHOST-ROW-01` | I2 | unchanged; PRE-GC cleanup/absence semantics | C |
| R10 | witness → soft-delete → destroy → restore | witness revives over a destroyed fs_object | **UNSAFE** | I2 | intent clears the witness | C, M |
| R10b | witness → destroy (live library) | witness stays valid | **UNSAFE** | I2 | intent clears the witness | C, M |
| R11a | CAS applied → UNKNOWN → soft-delete → settle | NOT_CERTIFIED / library_deleted; witness persists and revives on restore | SAFE (witness true; conservative report) | I4 | unchanged | C |
| R11b | CAS applied → UNKNOWN → destroy → settle | CERTIFIED / witness_settled over a destroyed fs_object | **UNSAFE** | I4 | intent cleared the witness → settlement reads NOT_CERTIFIED | C, M (CW-M10) |
| R12 | soft-delete acknowledged in dc-eu → dc-na certifies (global SERIAL, dc-eu down) → dc-asia LOCAL_QUORUM reader | CERTIFIED; dc-asia sees a valid witness on a live library; after convergence the row is deleted with witness H; restore revives it | SAFE for witness truth; lifecycle serialization belongs to ISSUE-LIB-DELETED-FENCE-01 | I5 | fence writes and CAS stay global SERIAL; consumer must read SERIAL | 3DC, M |
| R12b | destroyer isolated in one DC | gateway delete refused: claim read needs global SERIAL | SAFE | I5 | intent is also global SERIAL | 3DC |

"No product path today" / "GC only": with `GC_ENABLED=false` no production path
can destroy an identity reachable from the current HEAD (D4–D6 never touch HEAD,
L6 has no HEAD). The unsafe rows are reachable through the gateway primitives,
which consult no witness, and through GC once enabled.

## 9. Candidate designs

| Option | Mechanism | Verdict | Why |
|---|---|---|---|
| **A** Lifecycle predicate in the witness CAS | add more `IF` columns of the libraries row | rejected | No column on the libraries row observes the presence of an arbitrary reachable row; the CAS is single-partition. The current runtime is option A and the model finds R5 immediately. |
| **B** Library/authority epoch alone | mutators bump `E`; CAS predicates `E0` | rejected | Bump before destroy: a certifier that captures after the bump and reads before the delete certifies it (model trace). Bump after: born-false witness until the bump, forever on a crash. Bump before *and* after: crash between delete and second bump. |
| **C** Stored dependency digest `D` | witness `(H, D, V)` | rejected as a fence | With immutable claims the reachable semantic tree is a function of `H`; `D` adds no information and cannot see presence without re-reading the world; a deleter cannot know which `D` it breaks. May be added later as audit evidence only. |
| **D** Lifecycle claims in the same Paxos domain | per-identity delete capability / tree reservation | rejected in general form | Would need a capability per reachable identity (distributed lock over the tree, I7). Its per-library reduction is the selected design, possible only because fan-out is 1 (§6). |
| **D-lite** Pending intents without an epoch | certifier refuses a busy library | rejected | Emptiness ABA: capture idle → intent → delete → completion → CAS sees idle again (model trace). |
| **E** Invalidate witness on mutation | clear the witness when destroying | rejected alone | After the destroy = transient/crash window (as B-after). Before the destroy = B-before. |
| Token set without generation | `P` as `set<uuid>`; completion removes `t` | rejected | A paused attempt of unit `t` completes after its retry re-established `t` and strips the retry's protection while it still destroys (CW-M16, `TestPCD1B4ModelStaleCompletionCannotClearRetry`). |
| Completion predicated on the global epoch | `DELETE P[t] IF E = my_epoch` | rejected | Safe but not live: any unrelated intent advances `E` and leaves `t` stuck forever, blocking certification. |
| Lease re-check before each destructive write | owner fencing by checking the lease/`P[t]` right before writing | rejected | Check and write are not atomic: a pause between them lets a superseded generation write after takeover (audit trace). Phase 5/6 items even run with `LibraryGuardNone`. |
| Target-side fencing columns | per-row/partition ownership columns checked by an LWT delete on the target | rejected | Adds per-identity state and LWTs on hot write partitions; a row delete loses the fence, so a resumed generation can re-stamp a re-materialized row. |
| Generation-timestamped tombstones alone | no reaffirmation | rejected | Safe for re-materialized state, but a successor that keeps F leaves the original version older than the stale tombstone (CW-M20). |
| Intents for D4/D5 | best-effort commit discards take intents | rejected | No durable re-drive owner: a crash leaves `P` non-empty forever. A commit proven never to be HEAD is never covered, so a proof-backed capability suffices (§10.6). |
| Restore epoch / soft-delete as LWT | new lifecycle generation for library lifecycle | not needed | Library lifecycle does not change dependencies; the model is safe without it. |
| **Selected** | epoch + pending intents + witness clear, all global SERIAL, capture before final revalidation | adopted | Only variant with no counterexample in any scenario; every simplification of it is RED (§12). |

## 10. Selected minimum design (contract for PC-D1B.5)

### 10.1 State
`libraries.continuity_destruction_epoch timeuuid` and
`libraries.continuity_destruction_pending map<uuid, timeuuid>`. Written **only**
by global-SERIAL LWTs (`LibraryHeadSerialConsistency`); never by a plain write
(mixing client and ballot timestamps on the same cells is unsafe).

The map separates two identities that a set conflates:

- **token `t`** (`uuid`, e.g. `uuid.NewMD5` of the GC item identity) — the
  durable logical destruction unit, stable across retries;
- **generation `g`** (`timeuuid`, the epoch value written by that intent) — the
  execution that currently owns the unit.

`continuity_destruction_superseded timeuuid` (S) is the highest generation
that lost ownership of its token to a newer intent before completing. It only
grows, and only an intent that takes over a token raises it, in the same LWT.

A plain set of tokens is unsafe: attempt G1 of unit `t` is presumed dead, retry
G2 re-establishes `t`, G1 resumes and completes, and removing `t` strips G2's
protection while G2 is still destroying (model
`TestPCD1B4ModelStaleCompletionCannotClearRetry`, CW-M16). Completion must not
be predicated on the global epoch either (`IF E = my_epoch`): an unrelated
destroyer legitimately advances `E`, which would leave the first destroyer's
token stuck forever.

### 10.2 Destruction intent (begin)
```sql
UPDATE libraries
SET continuity_destruction_epoch = :g,           -- fresh timeuuid, ts(g) > ts(E_read)
    continuity_destruction_pending[:t] = :g,     -- this execution owns t
    continuity_destruction_superseded = :s,      -- max(S_read, P[t]_old) on takeover, else S_read
    continuity_certified_head_commit_id = null,
    continuity_contract_version = null
WHERE org_id = ? AND library_id = ?
IF continuity_destruction_epoch = :e_read
   AND continuity_destruction_pending[:t] = :p_old   -- null when t has no owner
   AND continuity_destruction_superseded = :s_read
   AND created_at = :created_at_read                 -- non-null canonical-row sentinel
```
- `created_at_read` is the exact non-null immutable canonical creation value
  read with E/P/S. A completely absent row, or a legacy row lacking that
  sentinel, must return NOT_APPLIED and MUST NOT be materialized by
  `BeginDestructionIntent`; fail closed for operator repair if the sentinel is
  missing. Do not specify a literal `IF EXISTS` as a substitute for the
  observed E/P/S predicates. The contract is one conditional LWT over E, P,
  S **and** canonical existence. `TestPCD1B4IntentDoesNotCreateAbsentLibrary`
  verifies complete absence against real Cassandra.
- `E_read`, `P[t]_old`, `S_read` come from a SERIAL read or from the current
  values a NOT_APPLIED result returns; retry until APPLIED or the row is
  absent. The `IF` makes the epoch strictly monotonic in time and makes S
  record every takeover atomically with the takeover. The `IF` does **not**
  prevent a ghost row: an intent whose Paxos read precedes a concurrent plain
  hard delete, with a ballot later than the delete's timestamp, leaves its live
  cells (E, P, S) on a HEAD-less row (characterized for any LWT on the row by
  R9i, like the witness CAS in R9g). Such a row is never a valid witness (HEAD
  is null), but GC canonical-existence checks see it. Tracked PRE-GC as
  `ISSUE-PCD1B-CONTINUITY-LWT-GHOST-ROW-01`: before GC activation a HEAD-less
  `libraries` row must count as canonically absent (or be cleaned) for every
  GC and restore decision.
- APPLIED → the destroyer may write. Row absent from a local
  `CanonicalLibraryExists` read is **not** authority to bypass the fence. An
  absent-row bypass requires `GlobalCanonicalAbsenceProof` minted only when a
  global-SERIAL read/barrier both settles prior HEAD Paxos and itself observes
  canonical absence, followed by an explicit `EACH_QUORUM` absence read of
  `libraries` (§10.6). A present SERIAL result permanently refuses that proof
  attempt, even if the subsequent EACH_QUORUM read is absent (CW-M33/G22); a
  local miss can coexist with a remote row (CW-M31).
- UNKNOWN → do not destroy; retry with a new generation. An UNKNOWN intent
  cannot land after a later one: the next Paxos round on the partition
  finishes an in-flight proposal before its own.
- **Token tuple (frozen).** UUIDv5 namespace is the exact UUID
  `6ba7b811-9dad-11d1-80b4-00c04fd430c8` (the RFC URL namespace). UUIDv5 uses
  SHA-1 over `namespace_bytes || name_bytes` and sets the version/variant bits
  per RFC 4122. `name_bytes` are `EncodeLengthDelimitedV1` over, in order:
  `"sesamefs/pcd1b4/destruction-token/v1"`, the 16 canonical bytes of `org_id`,
  the 16 canonical bytes of `library_id`, exact UTF-8 `item_type`, exact UTF-8
  `item_id`, signed big-endian 64-bit `identity_at.UnixMilli()`, and
  `block_candidate`. `EncodeLengthDelimitedV1` prefixes each field with its
  4-byte unsigned big-endian byte length, then the field bytes. For non-block
  items, `block_candidate` is the zero-length byte field; for a block item it
  is a nested `EncodeLengthDelimitedV1` of exact UTF-8 `storage_class`, exact
  UTF-8 `storage_key`, and signed big-endian 64-bit
  `candidate_at.UnixMilli()`. Timestamps are the millisecond values read from
  the persisted Cassandra `TIMESTAMP` columns, normalized to UTC before
  `UnixMilli()`. The namespace, domain tag, field order, encodings and timestamp
  precision are persistent identity and may not vary by implementation.

  Known vectors (same organization, library and persisted timestamp):

  ```text
  namespace       = 6ba7b811-9dad-11d1-80b4-00c04fd430c8
  org_id          = 00000000-0000-0000-0000-000000000001
  library_id      = 00000000-0000-0000-0000-000000000002
  item_type       = "block"
  item_id         = "block-42"
  identity_at     = 2024-01-02T03:04:05.006Z
  storage_class   = "hot-s3-na"
  storage_key     = "org/blocks/abc123"
  candidate_at    = 2024-01-02T03:04:05.006Z
  token           = 6af2fa07-6d4b-565e-b9f7-29a17ebd5476

  ---
  item_type       = "commit"
  item_id         = "commit-42"
  block_candidate = <zero-length field>
  token           = e69245e7-9a55-5765-be8c-46c519d4f359

  item_type       = "fs_object"
  item_id         = "fs-42"
  block_candidate = <zero-length field>
  token           = dc3ef498-f802-53a1-a05b-178c0a7d36d0
  ```

  `TestPCD1B4DestructionTokenV1KnownVector` freezes all three vectors and retry
  stability (CW-M30).

  `identity_at` is read from the persisted durable queue row. `QueueItem.Identity()`
  is **not** the token: it carries only `IdentityAt` and the block candidate,
  so distinct units of one cascade collide. Producers may pass a zero in-memory
  `IdentityAt`: Cassandra enqueue persists the effective durable value
  (`identity_at = queued_at` when omitted), and `RequeueItem` preserves that
  stored value. PC-D1B.5 derives the token only after hydrating the persisted
  queue row; it must not derive it from a pre-persistence `QueueItem` or expand
  producer scope absent a demonstrated persisted zero/NULL path. Characterize
  same persisted item across retries and different durable items: two commits
  of one cascade instant, commit vs fs_object with the same id, two fs_objects,
  the same id in different libraries, and one persisted item across retries
  (CW-M24).
- Every retry of that item reuses `t` and takes ownership with its own
  generation. There is no process-local batch token: `DequeueBatch` is a plain
  read with no durable batch identity or membership.

### 10.3 Destroy: generation-fenced writes
**Frozen property.** *A destruction generation that has lost ownership of its
token must not be able to make any destructive mutation effective against
certified state — neither against state materialized after it lost ownership
nor against state that existed before and was kept by its successor.* A lease
check before the write cannot provide this: a process can pause between check
and write, and Phase 5/6 items run with `LibraryGuardNone`.

**Frozen mechanism: timestamp fencing on Cassandra's own last-write-wins.**

1. Every destructive write of generation `g` (source row delete, `fs:`
   reference delete) is issued `USING TIMESTAMP ts(g)`, the microsecond time of
   its generation, never "now".
2. When an intent takes over a token from an older, uncompleted generation, the
   same LWT raises S to that generation. A generation that completed itself
   issues no later writes: it completes only after every destructive write is
   acknowledged.
3. If captured S0 is non-null, during final revalidation the certifier
   **always reaffirms every covered row** (commit row of H, reachable
   fs_objects rows, permanent `fs:` references) through `EACH_QUORUM`, using
   its identical authoritative content `USING TIMESTAMP ts(S0)+1`, then
   re-reads it (§10.3.1). This is required even when the certifier's local
   `WRITETIME` is greater than S0: local visibility does not prove that the
   version is present in every DC. A successful `EACH_QUORUM` operation is the
   proof; a local timestamp is not. If S0 is null, no superseded generation
   exists and reaffirmation is unnecessary.

Every tombstone a superseded generation can still issue has
`ts <= ts(g_old) <= ts(S0)`. The EACH_QUORUM reaffirmation makes the identical
covered projection at a timestamp above S0 durable across the participating DCs,
even when a local replica already has a still-newer version. The stale
tombstone therefore cannot shadow certified state wherever it arrives.
Generations newer than the capture either change E (the CAS fails) or begin
after the CAS and clear the witness. No pause assumption is involved.
Reaffirmation writes only the claimed digest, so it cannot change an identity,
and a physical object delete stays fenced by the existing GC delete authority,
which the certifier already revalidates.

#### 10.3.1 Reaffirmation surface (frozen)

Reaffirmation is a semantic write to `commits` / `fs_objects`, so it must stay
inside the #231 no-bypass boundary. It is a gateway operation, not certifier
CQL:

- **Capability.** `VerifiedReaffirmationCapability` is minted by the identity
  gateway only after it has read the strict presence-aware source projection
  and verified it, with a global-SERIAL claim read, against an **already
  existing** durable claim (the same verification the certifier performs).
  Minting it never creates a claim, never promotes an authority (commit,
  fs_object or mapping), and is impossible for an unproven, conflicting or
  unavailable identity.
- **Gateway writers.** `ReaffirmAuthorizedCommitAt(capability, ts)` and
  `ReaffirmAuthorizedFSObjectAt(capability, ts)` (or equivalents) rewrite
  exactly the verified projection `USING TIMESTAMP ts`. They are the only
  statements besides the existing authorized materializers allowed to write
  semantic `commits`/`fs_objects` columns, and they join
  `TestIdentityWritersAreInventoried` and the gateway-caller inventory.
  Reaffirmation CQL outside the gateway, a reaffirmation that creates or
  changes a claim, or one that writes values other than the verified
  projection is RED (CW-M25).
- **Full projection.** A stale row tombstone at `ts <= ts(S0)` shadows the row
  marker and every cell at or below it, so the rewrite is an `INSERT` of the
  whole identity projection, never a marker or a single column. Commit: the
  complete verified `CommitProjection` (parent, root, creator, description,
  `created_at`). fs_object: `obj_type` and, for a directory, `dir_entries`; for
  a file, `size_bytes`, `block_ids` and `seafile_block_ids_sha1` with the
  presence semantics #231 reads (a column that is NULL in the verified source
  stays unwritten, a present list is written whole; the zero-block file keeps
  both lists NULL). Canonical identifiers (`library_id`, `commit_id`, `fs_id`)
  are the verified keys byte for byte. Display-only columns (`obj_name`,
  `full_path`, `mtime`) are outside the identity projection and are not
  reaffirmed (their loss to a stale tombstone is PRE-GC follow-up
  `ISSUE-PCD1B-STALE-TOMBSTONE-DISPLAY-METADATA-01`).
- **Permanent `fs:` references.** An equivalent authorized primitive rewrites
  the complete permanent reference row `(org_id, block_id,
  referrer = fs:<library>:<fs_id>, library_id, created_at)` with the same
  referrer, library and original `created_at`, **no TTL**, `USING TIMESTAMP ts`,
  only for a referrer the certifier just proved permanent.
- **Consistency (frozen).** All three reaffirmations are written at
  `EACH_QUORUM`. A LOCAL_QUORUM reaffirmation can be visible only in the
  certifier's DC; a superseded tombstone delivered with the destroyers'
  `EACH_QUORUM` then removes the certified row everywhere else. This is
  demonstrated on the isolated 3-DC fixture
  (`TestPCD1B4ReaffirmationConsistency3DC`: the LOCAL_QUORUM-reaffirmed row
  survives only in dc-na; the EACH_QUORUM one survives in every DC; an
  EACH_QUORUM reaffirmation with two DCs down is refused). Weakening it is
  CW-M23.
- **Fail closed.** Any reaffirmation error, timeout or UNKNOWN makes the
  certification `UNKNOWN` with no witness; there is no best-effort
  reaffirmation. An ambiguous attempt establishes no global proof. A retry
  must repeat the `EACH_QUORUM` reaffirmation for every covered row whenever
  its captured S0 is non-null, even if the retrying DC locally observes a
  `WRITETIME > ts(S0)`. The re-read after a successful global write must see a
  version newer than S0 or certification is `UNKNOWN`.

#### 10.3.2 Progress when a destroy loses to a newer write time

A tombstone at `ts(g)` cannot remove a version whose write time `W` is above
`ts(g)` (a re-materialization, or a writer whose clock runs ahead). That is
safe (the destroyer re-reads, sees the target, does not complete as
destroyed), but repeated generations chosen only as `ts(g) > ts(E)` could keep
losing to the same `W`. Frozen progress rule:

- `W` is the maximum write time of **every live regular cell that the actual
  destructive mutation must dominate**, not only the identity projection. For
  a whole-row `DELETE FROM fs_objects`, include `obj_type`, all file/directory
  identity columns, and display-only `obj_name`, `full_path`, and `mtime` (plus
  every later-added live regular column covered by that row tombstone). For a
  whole-row commit delete, include every live regular commit cell. Derive `W`
  from the complete target row. For a whole-row permanent `fs:` reference
  delete, include every live regular reference cell too (including
  `library_id` and `created_at`). A higher display/metadata-cell timestamp can
  otherwise survive the row tombstone while the destroyer believes it
  completed.
- **CW-M28 — no same-clock future timestamp.** A destructive timestamp must
  never be ahead of the GC process's own wall clock. If `W` or E prevents
  minting a fresh `g > max(ts(E), W)` at or before that local clock, postpone
  destruction with durable retry/backoff; do not jump forward by a skew
  allowance. This closes same-clock future-tombstone poisoning, but by itself
  does not close cross-node skew (CW-M29).
- **CW-M29 — cross-node timestamp safety.** `Δ` is a finite, centrally
  configured maximum pairwise skew bound for every source that can assign
  Cassandra write timestamps for supported materializers or destructive
  writes: GC workers, supported clients that send explicit timestamps, and
  Cassandra coordinators that assign default write timestamps. It may not be
  independently configured per process. A trusted fleet-wide
  `ClockSafetyProvider` maintains an authoritative inventory of those sources
  and issues a short-lived `ClockSafetyLease` only when it can prove that the
  maximum pairwise offset is within `Δ`, each source has fresh clock-health
  measurements, and no wall-clock regression/step has invalidated monotonic
  timestamp behavior. The proof includes measurement uncertainty and the
  worst-case drift through the lease's validity window. Every source that can
  timestamp a supported materialization must obey the same lease: a Cassandra
  coordinator or client with expired/unknown health is fenced from accepting
  timestamped writes, including after GC has issued a tombstone. This ongoing
  gate makes monotonicity a protocol premise rather than a one-time sample.
  NTP/chrony being enabled without an enforced, observable fleet bound is
  insufficient. An unregistered source, stale measurement, unknown health,
  regression or bound violation means no lease and fail-closed behavior.

  With a valid lease, let `q = 1 microsecond` (Cassandra write-timestamp
  resolution and strict LWW tie guard) and
  `safe_now = gc_local_now - Δ - q`. A fresh generation may be minted only
  when `max(ts(E), W) < safe_now`, and must satisfy
  `max(ts(E), W) < ts(g) <= safe_now`. The timeuuid allocator must construct
  `g` inside that interval and preserve strict ordering above persisted E; it
  may not substitute an unbounded local `now`. The lease must still be valid
  immediately before the destructive write. If there is no such timeuuid, the
  lease expires, or the monotonic/health premise becomes unknown, issue no
  DELETE; keep any existing pending ownership and retry with durable backoff.
  This leaves the destructive timestamp strictly below the slowest supported
  writer's timestamp at the same real time, so later supported materialization
  cannot be hidden by the GC tombstone. A fast-GC/slow-writer model and a
  mutation removing Δ's safety margin must turn RED (CW-M29).

- **CW-M34 — in-flight materialization ordering.** Clock admission covers new
  requests only; a healthy writer may already have assigned `w` and remain
  capable of applying it after a destroyer chooses `g > w`. Before committing
  `g`, no previously admitted covered materialization with timestamp `<= g`
  may remain capable of settling later without recovery. A candidate solution
  may drain active permits through definite settlement and then revalidate
  target liveness, or use another proven ordering. It may instead reconcile a
  late successful/ambiguous write by retrying above the destructive floor and
  verifying visibility before success. A timeout/UNKNOWN must retain protection
  while the mutation can still become effective. A one-shot health check before
  CQL is insufficient. The exhaustive model covers both drain+revalidation and
  post-delete recovery; Cassandra 5.0.9 3-DC evidence holds the admitted request
  before server receipt, commits `g`, then resumes the write and observes
  successful-but-hidden settlement. The runtime mechanism is deliberately not
  selected here.

Consequences PC-D1B.5 must handle:
- A destroyer's tombstone at `ts(g)` does not shadow a version written later
  than `ts(g)` (a re-materialization, or a writer clock ahead). After its
  writes the destroyer re-reads the target (a CL intersecting the certifier's
  reads). If the target is still visible, the unit is not destroyed and is
  re-evaluated; this costs liveness, never safety.
- Destructive writes stay visible to the certifier's final reads once
  acknowledged: source deletes `EACH_QUORUM` against LOCAL_QUORUM source reads,
  `fs:` reference removals at LOCAL_QUORUM against `EACH_QUORUM` liveness reads.
  A failed or UNKNOWN destructive write may be partially applied: keep the
  entry.
- When captured S0 is non-null, every certification attempt globally
  reaffirms every covered row. A successful reaffirmation need not be repeated
  within that completed attempt, but an ambiguous `EACH_QUORUM` attempt is not
  proof of completion: a retry may legitimately repeat it. No skip may be
  inferred from local `WRITETIME`; only a future durable per-identity global
  proof could justify such an optimization.

### 10.4 Completion (end) and recovery
```sql
DELETE continuity_destruction_pending[:t] FROM libraries
WHERE org_id = ? AND library_id = ?
IF continuity_destruction_pending[:t] = :g
```
Only after every destructive write of the unit is acknowledged, and only by the
generation that owns `t`. NOT_APPLIED means a newer generation owns the unit
(or it is already complete): the stale attempt stops. A crash leaves the
entry: certification is refused (liveness cost, fail closed) until the unit is
resolved.

**Recovery lifecycle (frozen).** An entry `P[t]` must always have a way to be
resolved; it must never lose its only resolver:

- The GC `QueueItem` owning `t` is retried under new generations (takeover,
  S raised).
- Before that item can leave the queue in any way that ends re-driving — retry
  exhaustion into the DLQ, DLQ expiry (`scanExpiredFailedItems`), operator
  delete — the path must first resolve `t` by **abandon-by-takeover**: begin a
  new generation for `t` (raising S), destroy nothing, complete. The DLQ row may
  expire only after that completion is APPLIED; otherwise the row stays.
- Nobody may delete another generation's entry directly (unfenced janitor,
  CW-M14). Abandon-by-takeover is safe because of §10.3: the abandoned
  generation's late writes are older than every reaffirmed certified cell.
- Only destroyers with a durable queue item may create entries (D1–D3); see
  §10.6.

**Cardinality and backpressure.** P is bounded by the library's outstanding
unresolved destruction units (in-flight, crashed, retrying or DLQ'd items that
still hold a token), not by concurrent workers. Each entry costs a
`(uuid, timeuuid)` pair in the libraries row and in every Paxos payload on that
partition. PC-D1B.5 must bound it: a GC worker must not begin a new unit for a
library whose P already holds `N` entries (configurable), and must re-drive or
abandon-by-takeover existing entries first.

### 10.5 Certifier
1. Everything #228 does, unchanged, up to the final pass.
2. **Capture** `(head, deleted_at, E0, P0, S0)` with a SERIAL read immediately
   before the final revalidation pass (replacing the LOCAL_QUORUM
   `finalState` read at `library_continuity_certifier.go:367`). `P0` non-empty →
   `NOT_CERTIFIED / identity_destruction_pending`, no witness. A read error →
   UNKNOWN. (A LOCAL_QUORUM capture is also safe — the model proves a stale
   capture costs liveness only — but SERIAL avoids spurious refusals.) An
   additional early capture is allowed; the one that matters precedes the
   final pass.
3. Final revalidation pass (unchanged), plus an `EACH_QUORUM` reaffirmation of
   every covered row above `ts(S0)` whenever S0 is non-null (§10.3), regardless
   of local write times. With a stale LOCAL_QUORUM capture, an older S implies
   an older E, so the CAS fails.
4. Witness CAS adds `AND continuity_destruction_epoch = E0` (CQL `= null` when
   no destruction ever ran).
5. Settlement predicate unchanged: an intent that landed after an applied CAS
   cleared the witness, so "applied but already invalidated" reads as not
   certified; "not applied" reads as not certified; "applied, same lifecycle"
   reads as certified.

`AdvanceLibraryCertifiedFrontier` gains the same epoch predicate and its caller
the same capture rule. `CommitLibraryContinuityWitness` (no production caller)
either gains the predicate or is removed.

### 10.6 Participants and capabilities
The destructive primitives (`DeleteCommitIdentity`, `DeleteFSObjectIdentity`,
the `fs:` reference removal) accept exactly one of three typed capabilities,
like the existing authorized projection tokens:

| Capability | Minted only from | Who | Fence state |
|---|---|---|---|
| `DestructionIntentCapability` | an APPLIED intent for `(t, g)` | D1–D3 (GC commit/fs_object delete, `fs:` removal): durable queue items that are retried until done, so a crashed intent is always re-driven | epoch + `P[t] = g` |
| `GlobalCanonicalAbsenceProof` | a global-SERIAL HEAD Paxos read/barrier settles history up to its own linearization point **and itself returns canonical ABSENT**; only then an explicit `Consistency(EACH_QUORUM)` read of canonical `libraries` may confirm absence in every DC. Any present SERIAL row/result, error/timeout, non-ABSENT EACH_QUORUM result, or local `CanonicalLibraryExists == false` refuses the proof | library cascade children, Phase 3/4 orphans using `LibraryGuardCanonicalMustBeAbsent` as work classification only | none after stable global absence is proved (no row ⇒ no witness) |
| `ProvenUncoveredCleanupCapability` | a proof that the target commit is not and can never become the canonical HEAD: its attempt-unique, server-minted, never-exposed id was never proposed, or its HEAD CAS definitively did not apply (`ErrLibraryHeadConflict`, `ErrLibraryHeadNotFound`, `ErrLibraryHeadUninitializable`), or it is an initial commit (empty parent, unpromotable by Sync) that lost to a different winning HEAD | D4 `cleanupFailedPublishDeleteCommitFn`, D5 `InitializeLibraryHeadIfUnset` discard and `DiscardLosingInitialCommit` | none |

D4/D5 must **not** take intents. They are deliberately best-effort, with no
durable owner that would re-drive a completion; an intent followed by a crash
would leave `P` non-empty forever and turn a tolerated dangling-commit leak into
a permanent certification block. They also do not need one: a witness covers
only the commit row of its HEAD, so a commit that can never be HEAD is never
covered. The capability applies to commit rows only; the fs_object variant
(`cleanupFailedPublishDeleteFSObjectFn`, no production caller today) stays a
participant because fs_objects are shared with HEAD by content addressing.
`ProvenUncoveredCleanupCapability` must be impossible to mint from anything but
those definitive outcomes (CW-M18).

Not destroyers: L6 rollback (keeps its `IF head_commit_id = null` authority),
D6 `pub:` cleanup (not covered), L1–L5 lifecycle writes, writers that
re-materialize a claimed digest.

### 10.7 Productive witness reads
The witness shape and validity predicate are unchanged, but its **consumption**
is constrained: every productive decision that treats the witness as authority
must obtain it with a global-SERIAL read or inside an LWT of the same domain
(as `AdvanceLibraryCertifiedFrontier` does by predicating it). A LOCAL_QUORUM
observation of `(HEAD, certified, contract)` can be a stale copy from before an
intent cleared the witness in another DC, and must never authorize productive
work. Frozen in the canonical PC-D1 contract; the consumer PR owns CW-M17 and
its 3-DC evidence.

## 11. Executable model

`internal/db/pcd1b4_certification_window_model_test.go` exhaustively explores
every interleaving of a certifier, up to two destroyers, a
supported writer, library lifecycle (soft-delete visible or invisible to
Paxos, restore, hard delete racing the CAS commit), HEAD movement (including a
hypothetical repeat), and UNKNOWN witness results. There is **no owner-fencing
assumption**: a crashed destroyer is a paused process that may later resume,
in program order, and issue its destructive write and then its completion; a
same-token retry may take over while it is paused, and may itself decide not
to destroy. Presence follows Cassandra last-write-wins (a tombstone wins a
timestamp tie), and a writer re-materializing F may use a current or a skewed
past timestamp. CW-M29 also models distinct GC and slow-writer local clocks,
the pairwise skew lease, the timestamp-resolution tie margin and unknown or
regressed clock health. LWT steps on the libraries row are linearized; plain
writes may land between a CAS's read and commit.
Invariant: in every reachable state, a witness a reader would treat as valid
(merged view or stale Paxos view) implies the covered state is present with
its certified content; every CERTIFIED result is backed by the stored witness.

| Test | Result |
|---|---|
| `TestPCD1B4ModelCurrentRuntimeAdmitsFalseWitness` | main admits a false witness (R5 trace) |
| `TestPCD1B4ModelRejectsWeakerFences` | A/B-lite, E, B, D-lite each have a counterexample |
| `TestPCD1B4ModelSelectedFenceHoldsInvariants` | selected: 0 violations in all 6 scenarios (each explored exhaustively; state counts are logged); certifies in some executions; busy refusals reachable; a LOCAL_QUORUM capture is still safe |
| `TestPCD1B4ModelLifecycleNeedsNoRestoreEpoch` | plain soft-delete/restore/hard delete safe under the selected fence |
| `TestPCD1B4ModelStaleCompletionCannotClearRetry` | replays the audit trace `begin(t,G1) → delete → re-materialize → begin(t,G2) → stale complete(t,G1) → second delete → certify`: GREEN with generation ownership (capture refuses the busy library), a false witness with a plain token set; the exhaustive search finds an even shorter token-set counterexample |
| `TestPCD1B4ModelStaleGenerationCannotDestroyLate` | replays the audit trace (G1 passes its fence and pauses; G2 takes over, destroys and completes; F re-materialized; certifier revalidates; G1's old delete lands before the CAS) and the stronger trace (G2 keeps F; G1 deletes the original version): both safe with the selected fence, RED under CW-M19 and CW-M20 respectively |
| `TestPCD1B4ModelMutationContract` | CW-M1..M8, M10, M14, M15, M16, M19, M20, M21 each RED |
| `TestPCD1B4ModelCrossNodeClockSkewCannotPoisonWriter` | fast-GC/slow-writer clocks cannot mint a future tombstone; absent, stale or regressed clock health fails closed (CW-M29) |
| `TestPCD1B4ModelClockLeaseGatesEveryWriterAuthority` | commit/fs_object/permanent-reference materializers and Cassandra coordinators refuse timestamps after health expires (CW-M32) |
| `TestPCD1B4ModelCanonicalAbsenceRequiresGlobalRead` | local absence with a remote-present row cannot mint `GlobalCanonicalAbsenceProof` (CW-M31) |
| `TestPCD1B4ModelStableCanonicalAbsenceRequiresPaxosSettlement` | EACH_QUORUM absence alone permits a paused old HEAD CAS to resurrect the row; the model requires settlement before the absence read (CW-M33) |
| `TestPCD1B4StableAbsencePaxosRace3DC` | Cassandra 5.0.9 latch pauses an accepted HEAD proposal before commit; SERIAL settlement makes the following absence proof refuse, while G21's EACH_QUORUM-only mutation resurrects H1 after proof (CW-M33) |
| `TestPCD1B4ModelSerialProofReadMustObserveAbsence` | a present result from the SERIAL proof-read vetoes a later EACH_QUORUM miss (CW-M33 issuance-window extension); G22 removing the predicate turns RED |
| `TestPCD1B4SerialProofReadPresenceRace3DC` | SERIAL observes H0; a new accepted CAS starts afterward; after hard delete and EACH_QUORUM absence, the saved SERIAL-present result still refuses proof; G22 ignores it and sees H1 after resume (CW-M33) |
| `TestPCD1B4SerialProofReadPresenceMutation3DC` | with G22's SERIAL-result predicate removed, the same Cassandra race turns RED on proof plus the resumed H1 (CW-M33) |
| `TestPCD1B4ModelInFlightMaterializationNeedsBarrierOrRecovery` | a healthy, timestamped writer paused in flight must be drained/revalidated or recovered above `g`; UNKNOWN cannot release active protection (CW-M34) |
| `TestPCD1B4DestructionTokenV1KnownVector` | exact UUIDv5 namespace/encoding maps fixed block, commit and fs_object durable QueueItems to their tokens; retry-only fields do not change the token (CW-M30) |

## 12. Mutation contract for PC-D1B.5

Each mutation must turn a targeted test RED **for its specific reason**
(outcome/reason assertion, not a compile error). "Model" means the mutation is
already proven meaningful by §11; PC-D1B.5 must reproduce it against real code.

| ID | Mutation | Required RED | Model |
|---|---|---|---|
| CW-M1 | witness CAS omits the epoch predicate | R5 in-window destroy yields CERTIFIED | ✅ |
| CW-M2 | intent does not set a fresh epoch | intent+destroy+completion inside the window yields CERTIFIED | ✅ |
| CW-M3 | intent does not clear the witness | R10b: witness valid after destroy | ✅ |
| CW-M4 | capture ignores pending tokens | capture after intent, destroy before CAS yields CERTIFIED | ✅ |
| CW-M5 | capture placed after the final revalidation | intent/destroy/completion before capture yields CERTIFIED | ✅ |
| CW-M6 | completion issued before the destroy is acknowledged (or on destroy UNKNOWN) | CERTIFIED over a destroyed identity | ✅ |
| CW-M7 | a destroyer (any participant in §10.6) bypasses the intent | inventory guard RED + integration CERTIFIED | ✅ |
| CW-M8 | intent, completion or witness CAS uses LOCAL_SERIAL / inherits `serial_consistency` | 3-DC: intent in dc-eu not seen by a dc-na CAS | ✅ |
| CW-M9 | a fence column written by a non-LWT statement | source guard RED | — |
| CW-M10 | UNKNOWN settled from the CAS applied flag or a LOCAL_QUORUM read | R11b yields CERTIFIED | ✅ |
| CW-M11 | intent omits the observed non-null canonical `created_at` existence predicate, or creates on canonical absence | a completely absent library row becomes a partial `libraries` row; real Cassandra must return NOT_APPLIED and remain absent | single-node Cassandra |
| CW-M12 | `AdvanceLibraryCertifiedFrontier` omits the epoch predicate | advance over an intervening destruction applies | — |
| CW-M13 | destroyer CL weakened so the certifier's final read does not intersect it | 3-DC: delete acknowledged in one DC, certifier in another certifies | — |
| CW-M14 | a pending token dropped by a non-owner without fencing the owner | zombie destroyer deletes after certification | ✅ |
| CW-M15 | identity claims become mutable, or the fence rewrites a claim | re-created divergent content under a valid witness | ✅ |
| CW-M16 | completion removes the token without checking its generation (plain set), or is predicated on the global epoch instead | stale completion of G1 clears G2's protection → CERTIFIED over a destroyed identity (or: a token stuck after an unrelated intent) | ✅ |
| CW-M19 | a destructive write uses "now" (or any timestamp above its generation) | stale generation's late delete removes a re-materialized, certified identity | ✅ |
| CW-M20 | the certifier does not reaffirm covered state above `ts(S0)` when S0 is non-null | stale generation's late delete removes the original version its successor kept | ✅ |
| CW-M21 | a takeover does not raise S (or S is written outside the intent LWT) | reaffirmation misses the superseded generation; its late delete lands on certified state | ✅ |
| CW-M22 | an item holding `P[t]` leaves the queue (DLQ expiry, operator delete) without abandon-by-takeover | integration: entry stuck forever (liveness) / unfenced drop (safety, as CW-M14) | — |
| CW-M23 | reaffirmation written below `EACH_QUORUM` (e.g. LOCAL_QUORUM), or an error/UNKNOWN reaffirmation treated as best effort | 3-DC: a stale tombstone removes the certified row outside the certifier's DC while the witness stays valid (reproduced at the Cassandra level by `TestPCD1B4ReaffirmationConsistency3DC`) | 3-DC ✅ |
| CW-M24 | the destruction token omits a distinguishing field of the durable item or includes a retry-variant one (`QueuedAt`, `RetryCount`, attempt, worker) | collision test: two units share a token / one item gets two tokens across a retry | — |
| CW-M25 | reaffirmation outside the identity gateway, without `VerifiedReaffirmationCapability`, creating or changing a claim, or writing a partial projection (marker or subset of columns) | no-bypass inventory RED; a stale row tombstone removes an un-reaffirmed identity column | — |
| CW-M26 | whole-row `W` omits a live regular cell, a blocked destroy completes, or a generation/tombstone is minted ahead of the GC clock to beat W | display cell survives a whole-row delete / same-clock future tombstone hides a successful normal writer / blocked item reports destroyed | real Cassandra + model |
| CW-M17 | a productive witness read at LOCAL_QUORUM (consumer PR) | 3-DC: stale copy of a cleared witness authorizes work | — (consumer) |
| CW-M18 | `ProvenUncoveredCleanupCapability` minted without a definitive never-HEAD proof, or accepted for an fs_object / `fs:` destroy | integration: a covered identity destroyed without an intent yields CERTIFIED | — |
| CW-M27 | a retry skips global reaffirmation because its local `WRITETIME > S0` after a partial EACH_QUORUM attempt returned UNKNOWN | 3-DC: stale tombstone leaves the certified row absent in another DC | exhaustive model + isolated 3-DC |
| CW-M28 | a destroyer mints `g > W` while `g` is ahead of wall clock instead of postponing | real Cassandra: a normal successful rematerialization remains hidden by the future row tombstone | single-node Cassandra + model |
| CW-M29 | omit/understate fleet-wide `Δ`, accept unknown/stale clock health, or exclude a timestamp source | a fast GC clock legally emits `g` under local now but ahead of a slow writer; the later successful materialization remains hidden | two-clock model |
| CW-M30 | change UUIDv5 namespace, domain tag, field order, length encoding, millisecond precision, or block/non-block candidate encoding | one of the frozen block/commit/fs_object QueueItem vectors changes | three exact known vectors |
| CW-M31 | mint `GlobalCanonicalAbsenceProof` from a local `CanonicalLibraryExists == false` instead of an explicit EACH_QUORUM absence read | 3-DC: dc-na sees absent while dc-eu retains the library; local proof would authorize a fence-bypass destroy | model + isolated 3-DC |
| CW-M32 | a materializer/reference writer or Cassandra timestamp coordinator bypasses its expired/unknown `ClockSafetyLease` | a successful client/coordinator write with a regressed clock can remain hidden by the earlier GC tombstone | writer-authority model |
| CW-M33 | omit settlement of pre-existing proposals (G21), or ignore a SERIAL proof-read that observed canonical presence (G22) | accepted old HEAD CAS can land after proof; or a new post-barrier CAS starts after SERIAL observes H0, then hard delete + EACH_QUORUM miss must still be refused | models + isolated Cassandra 5.0.9 3-DC accepted-Paxos races; G21/G22 RED |
| CW-M34 | release an admitted writer permit before definite settlement, or remove post-delete recovery/revalidation | valid-lease write at `w` settles successfully after GC tombstone `g>w` and remains invisible | model mutation RED + isolated Cassandra 5.0.9 3-DC transport-gated write |

## 13. Witness shape

| Field | Needed for safety? | Why |
|---|---|---|
| H | **yes** | the certified frontier; HEAD equality is the validity anchor |
| R | no | `commit(H) → root_fs_id` is bound by the immutable commit claim; storing R adds nothing |
| D | no | the reachable semantic tree is a function of H under immutable claims; D cannot detect presence changes. Optional future audit evidence, never a CAS field |
| A (epoch E) | predicated, **not stored** | every epoch change clears the witness in the same Paxos write, so a stored witness was necessarily written at the current epoch. Storing E would force every consumer's validity predicate to change for no extra safety |
| V | **yes** | certification contract version |

No speculative field becomes mandatory. The stored witness stays `(H, V)`.

## 14. Required invariants

| # | Invariant | Satisfied by |
|---|---|---|
| I1 | A witness cannot be born valid for an identity lifecycle that ceased being live before settlement | capture before final pass + busy refusal + epoch predicate + intent before destroy (CW-M1/2/4/5/6) |
| I2 | A lifecycle mutation that invalidates a certified dependency cannot leave a witness productively valid | intent clears the witness in the same global-SERIAL write (CW-M3); lifecycle writes do not invalidate dependencies (§7) |
| I3 | Delete/recreate at the same key cannot make a witness for lifecycle A authorize B | immutable claims (#230/#231, R6/R7) + presence fenced by intents (CW-M15) |
| I4 | Ambiguous settlement cannot invent validity | settlement reads the stored witness at SERIAL; intents clear it (CW-M10) |
| I5 | Cross-DC stale reads cannot make a stale witness valid | all fence writes and the CAS in global SERIAL (CW-M8); productive witness reads at global SERIAL only (§10.7, CW-M17); R12 evidence |
| I6 | Authority identity immutability is preserved | the fence never writes `identity_authority_claims`; existing immutability guards |
| I7 | No distributed lock over the reachable tree | per-library row state; an intent spans one destruction unit |
| I8 | No writer-side Paxos for baseline certification | supported writers add local ClockSafety/lifetime admission; only destroyers pay the global-SERIAL fence Paxos cost (§16) |
| I9 | No productive consumer trusts the witness until this protocol is implemented | no production caller of the certifier, the advance primitive or witness validity (§4.1) |
| I10 | A released intent's destruction is visible to the certifier's final reads | CL pairing in §10.3 (CW-M13) |
| I11 | A superseded generation cannot make a destructive mutation effective against certified state | generation-timestamped tombstones + S raised on takeover + certifier reaffirmation above S (§10.3; CW-M19/M20/M21) |
| I12 | A pending entry never loses its only resolver | durable queue item re-drives; DLQ/expiry/operator paths abandon-by-takeover first (§10.4; CW-M22) |
| I13 | Reaffirmation cannot bypass identity authority and is visible in every DC | gateway-only `VerifiedReaffirmationCapability`, full projection, `EACH_QUORUM`, fail closed (§10.3.1; CW-M23, CW-M25) |

## 15. Relationships

- **Phase 5 / Phase 6 (PRE-GC).** The fence does **not** protect HEAD from
  `ISSUE-GC-PHASE5-CASCADE-SHARED-FSOBJECTS-01` or from Phase 6's execute-time
  TOCTOU (keep-set computed at scan; items carry no guard and no reachability
  recheck; HEAD can re-reference an fs_id outside the keep set, cf.
  `ISSUE-PC0-CONTENT-RESURRECTION-PUBLICATION-01`). Those destroy
  HEAD-reachable content — data loss independent of any witness. With the
  fence, such a destruction clears the witness and recertification fails
  closed (`missing_fs_object`): the witness stays honest while HEAD is broken.
  Phase 5 and Phase 6 must be fixed before GC activation on their own; this
  PR does not absorb them.
- **Mapping authority (PR A).** The fence treats authority types uniformly:
  claims are immutable and never written by the fence. Mapping rows are
  org-scoped (shared across libraries), so a mapping deleter could not use a
  per-library fence without fan-out; PR A keeps mapping authority
  non-destructible. No deleter exists today. F4 below records the former
  mutable-projection risk from the #228 review; **PR #233 closes it** with the
  dominant-timestamp projection freeze, EACH_QUORUM write-time verification,
  LOCAL_QUORUM productive reads, and repository-wide mapping mutation/reader
  guards. It is not a PC-D1B.5 responsibility.
- **SYNC-ID-1.** Untouched. The fence assumes identities are canonical when
  they enter the system; #228 already compares fs_id keys byte-exactly.
- **ISSUE-LIB-DELETED-FENCE-01.** Soft-delete serialization with HEAD writers
  is a consumer prerequisite (§7.1), not part of this fence.

## 16. Performance (PC-D1B.5)

| Dimension | Cost |
|---|---|
| Certifier | +0 LWT; the final state read becomes a SERIAL read (+1 Paxos read round, cold path); one extra CAS predicate column; write-time reads of every covered row in the final pass; when S0 is non-null, an `EACH_QUORUM` reaffirmation write plus re-read for every covered row on each attempt |
| Destroyers | For D1–D3, +2 global-SERIAL LWTs per destructive GC `QueueItem` on the library partition (intent, completion), plus a SERIAL read or CAS retry when the fence values moved, one verification read per target, and a fresh `ClockSafetyLease` check before mint/write. `GlobalCanonicalAbsenceProof` costs one global-SERIAL Paxos read/barrier plus a subsequent EACH_QUORUM canonical-absence read. No batch amortization in v1: `DequeueBatch` has no durable batch identity, and a durable batch/membership record is a later optimization if metrics require it. D4/D5: 0 (capability from an existing proof) |
| Certifier reaffirmation | one `EACH_QUORUM` write per covered row per certification attempt while S0 is non-null; ambiguous attempts do not establish proof and retries may repeat the writes; 0 when S0 is null |
| Normal writer path (uploads, RecvFS/PutCommit, HEAD CAS) | 0 new writer-side Paxos and 0 network round trips in the no-contention path; local `ClockSafetyLease` admission plus lifetime accounting through definite settlement for each timestamped covered write |
| CW-M34 exceptional path | A destroyer may wait for admitted writes to settle and revalidate liveness, or a late successful/UNKNOWN write may require authorized recovery above the destructive floor and a visibility check. Additional I/O depends on the PC-D1B.5 mechanism; it is not hidden in the normal-path cost |
| Read amplification | certifier: the SERIAL capture and write times of covered rows (folded into the existing final-pass reads), plus a re-read of each reaffirmed row; destroyers: one verification read per target (with its write time) |
| Write amplification | destroyers: the 2 LWTs per item; per-destruction witness clear. Certifier: one `EACH_QUORUM` reaffirmation per covered row per attempt while S0 is non-null; an ambiguous retry may repeat it |
| Fan-out | 1 (library-scoped keys, §6) |
| Per-library state | 3 columns; the pending map is bounded by outstanding unresolved destruction units, capped by the `N` backpressure limit of §10.4 |
| Per-identity state | 0 |
| Multi-DC | intents are global Paxos (same cost class as a HEAD CAS); they contend with HEAD writers on the same partition. Clock safety also requires a fresh fleet-wide proof covering every timestamp source; missing/unknown health blocks destructive writes. Contention is controlled by destructive QueueItem rate and per-library backpressure; v1 assumes no batch amortization |
| Liveness | every destruction clears the witness → recertification (cold path). Preserving a witness across provably-unreachable destructions needs a GC reachability proof in the HEAD domain (X1/G4) and is deferred |

## 17. Executable characterization (this PR)

```bash
# model + inventory guards (inside Docker; no Cassandra)
docker run --rm -v "$PWD":/build -w /build <gotest-image> go test ./internal/db -run PCD1B4 -v
# persisted IdentityAt characterization (inside Docker; no Cassandra)
docker run --rm -v "$PWD":/build -w /build <gotest-image> go test ./internal/gc -run PCD1B4 -v
# single-node real-Cassandra characterization (Docker Compose test stack)
docker compose --profile test run --rm --build go-integration-test
# isolated 3-DC (R12, CW-M23, CW-M27 retry, CW-M31 global-absence visibility, CW-M33 accepted-Paxos races/G21/G22, CW-M34 in-flight materialization); owns sesamefs-pcd1b4-* resources only
bash scripts/pc-d1b4-certification-window-multidc-characterization.sh
# the guards bite: G1-G15, G17-G20 source/model mutations, G21/G22 in the 3-DC script, plus C1 on real Cassandra
bash scripts/pc-d1b4-certification-window-guard-mutation-validation.sh [--with-cassandra]
```

The guard runner proves the evidence is not vacuous: an unlisted soft-delete
(G1), an unlisted destroyer call (G2), a vanished inventoried restore (G3), a
premature fence-column write (G4), a reused library id (G5), the selected
fence without its epoch predicate (G6), a model whose current runtime is
silently fenced (G7), an aliased destroyer primitive (G8), a new destroyer
wrapper (G9), a raw `DELETE FROM block_references` (G10), a
generation-blind completion (G11), destructive writes at a current timestamp
(G12), removal of the cross-node skew margin (G13), UUIDv5 namespace drift
(G14), local-only canonical-absence proof (G15), writer clock-health bypass
(G17), non-block token-encoding drift (G18), omission of Paxos settlement from
the absence proof (G19), and removal of the admitted-writer drain (G20) each
turn their guard RED for the stated reason. The isolated 3-DC script
also mutates the global EACH_QUORUM proof read to LOCAL_QUORUM (G16) and requires its
absent-local/present-remote characterization to turn RED. With
`--with-cassandra`, dropping `deleted_at = null` from the witness CAS turns the
R1 characterization RED on real Cassandra (C1). Its CW-M34 leg holds a
timestamped client request in transport, commits a later EACH_QUORUM tombstone,
then resumes the write and confirms successful-but-hidden materialization.
CW-M33 pauses the accepted HEAD proposal before commit; G21 omits its SERIAL
settlement and the resumed CAS produces proof-plus-resurrection RED. A second
race lets a fresh HEAD CAS start after a SERIAL read observed H0, then deletes
the row before EACH_QUORUM absence; the saved SERIAL-present result refuses
the proof, and G22 mutating away that predicate reproduces proof-plus-H1 RED.
Both run on the same isolated Cassandra 5.0.9 fixture.

The isolated suite builds a test-only Cassandra 5.0.9 image from the pinned
binary digest and exact source commit
`b5f2a54210d541339c2e7c17a794195cac0e67c2`; Docker validates the source SHA-256
for both `Paxos.java` and `StorageProxy.java` before compiling the latch. The
one-shot latch pauses at the accepted-proposal/pre-commit seam in both the v1
`StorageProxy.doPaxos` and v2 `Paxos.cas` paths; it does not change the stock
image or production runtime. The hard-delete timestamp is set to one microsecond
below the accepted ballot so Cassandra's LWW merge permits the late proposal to
become visible without settlement. G21 omits the barrier for a pre-existing
proposal; G22 ignores a prior SERIAL-present result. Both require
proof-plus-resurrection RED on their targeted Cassandra race.

The characterization tests assert CURRENT behavior and pass on main. PC-D1B.5
must invert the rows marked UNSAFE in §8 (R3b, R4, R5, R10, R10b, R11b) and
keep the SAFE rows unchanged.

## 18. Next PR contract — PC-D1B.5 runtime

PC-D1B.4 freezes the decision and is **mergeable** after both CW-M33
accepted-Paxos/issuance races and G21/G22 SERIAL-barrier/absence-result
mutations passed on Cassandra 5.0.9. PC-D1B.5
implements the frozen contract afterward; it is required before destructive GC
activation or a productive consumer.

**Scope (exact):**
1. Next available migration (`028` if #233's `027_block_mapping_authority_claims.cql` lands first): the three fence columns.
2. `internal/db`: intent/completion/capture primitives (global SERIAL,
    observed E/P/S plus the canonical non-null `created_at` existence
    predicate, tri-state outcomes); typed intent capability; mint
    `GlobalCanonicalAbsenceProof` only when a global-SERIAL HEAD Paxos
    settlement/read itself returns canonical ABSENT; present/error/timeout
    refuses the proof attempt. A subsequent canonical-row `EACH_QUORUM` read
    must also return ABSENT in every DC. Cassandra 5.0.9 freezes the order and
    G21/G22 characterize both race boundaries.
3. Identity gateway: destructive deletes and `fs:` reference removal require
   one of the three capabilities of §10.6 as a parameter; new
   `VerifiedReaffirmationCapability` and the reaffirmation writers of §10.3.1
   (commit, fs_object, permanent `fs:` reference), `EACH_QUORUM`, explicit
    timestamp, full projection, inventoried. All supported commit/fs_object and
    permanent `fs:` timestamped materializers also require a local cached
    `ClockSafetyLease` admission before CQL; an expired/unknown lease refuses
    the write. Cassandra coordinators that assign default timestamps must be
    fenced by the same fleet clock-health provider; pin the timestamp authority
    used by every relevant statement rather than relying on driver defaults.
    CW-M34 additionally requires a lifetime barrier or verified recovery for
    already-admitted writes: drain permits through definite settlement and
    revalidate liveness, or reconcile late/UNKNOWN outcomes above the destructive
    floor and verify visibility. A one-shot admission check cannot authorize
    deletion while such a write can still land.
4. GC: derive tokens from each persisted durable queue tuple (do not expand
   producers merely to stamp in-memory `IdentityAt`); D1–D3 acquire a fresh,
   valid `ClockSafetyLease` before an intent/generation, then mint `g` above E
   and complete target-row W but at or below `safe_now` (§10.3.2/CW-M29). If
   clock health, the lease, or the safe interval is absent, issue no destructive
   write and retain any existing P entry for durable retry/backoff. Write
   tombstones `USING TIMESTAMP ts(g)`,
   re-read targets after writing, complete `IF P[t] = g` after
   acknowledged writes, keep the entry on any failure; a stale generation stops
   on NOT_APPLIED; DLQ exhaustion, DLQ expiry and operator paths
   abandon-by-takeover before an item leaves the queue; backpressure caps P.
5. D4/D5 take `ProvenUncoveredCleanupCapability` minted only from their
   definitive never-HEAD outcomes; they write no fence state. Library-cascade
   children and Phase 3/4 orphan bypasses require `GlobalCanonicalAbsenceProof`;
   local `CanonicalLibraryExists` results remain non-authoritative.
6. Certifier: SERIAL capture of (E, P, S) before the final pass, busy refusal
   with a new reason, gateway `EACH_QUORUM` reaffirmation of every covered row
   whenever S0 is non-null (regardless of local WRITETIME; fail closed and
   repeat globally after UNKNOWN), epoch predicate in the witness CAS;
   settlement unchanged.
7. `AdvanceLibraryCertifiedFrontier` epoch predicate.
8. Invert the UNSAFE characterization rows; extend the lifecycle inventory
   guard with the fence roles (CW-M7, CW-M9); a mutation runner covering
   CW-M1..M16 and CW-M18..M34 (CW-M17 belongs to the consumer PR); the 3-DC
   script covers CW-M8, CW-M13, CW-M23, CW-M27, CW-M31, CW-M33 and CW-M34.
   CW-M28 must invert
   the real-Cassandra same-clock future-tombstone poisoning characterization;
   CW-M29 must require the fleet skew lease and margin; CW-M30 must preserve
   all known UUIDv5 vectors; CW-M31 must reject local-absence authority; CW-M32
   must fence each writer-side timestamp authority on invalid health; CW-M34
   must protect a valid writer already in flight and keep UNKNOWN protected.

**Acceptance criteria:** every §8 UNSAFE row inverted on real Cassandra; SAFE
rows unchanged; CW-M1..M16 and CW-M18..CW-M34 RED for their stated reason; real
Cassandra must cover both CW-M33 interleavings: accepted pre-barrier CAS → hard
delete → SERIAL settlement → EACH_QUORUM absence, and SERIAL observes H0 → new
CAS accepted/paused → hard delete → EACH_QUORUM absence. Neither may mint proof
before an effective later HEAD. G21 removes pre-existing-proposal settlement;
G22 removes the SERIAL-absent requirement; each must reproduce
proof-plus-resurrection RED.
Also retain a real-Cassandra leg in which a paused
generation's late delete lands after a takeover and after certification without
breaking the witness; isolated 3-DC green;
full short suite, race, vet and Compose integration green; no hot-path
statement changed (inventory guards); `GC_ENABLED=false`.

**Not in PC-D1B.5:** productive consumer, PC-2, Phase 5/6 fixes, mapping
authority, SYNC-ID-1, soft-delete serialization, GC activation.

## 19. Side findings

| ID | Finding | Severity | Domain |
|---|---|---|---|
| F1 `ISSUE-PCD1B-CONTINUITY-LWT-GHOST-ROW-01` | Any continuity LWT on the `libraries` row (witness CAS R9g, the future intent/completion R9i) racing a plain hard delete can leave a HEAD-less ghost row with the LWT's cells. Never a valid witness; GC canonical-existence checks then see a library, so guarded cascade children postpone and the storage is not reclaimed | Low (GC liveness) | GC, PRE-GC |
| F2 `ISSUE-GC-HARD-DELETE-LEASE-SERIAL-DOMAIN-01` | `acquireHardDeleteLock`/renew/release are LWTs without `SerialConsistency`; with `serial_consistency: LOCAL_SERIAL` a restore in one DC and a cascade in another can both own the library lease | Medium (multi-DC lifecycle) — ✅ closed by PR #234 (global `SERIAL` pin) | GC, PRE-GC |
| F3 | Phase 6 items carry no library guard and no execute-time reachability recheck (scan-time keep set) | recorded under `ISSUE-PC0-CONTENT-RESURRECTION-PUBLICATION-01` | GC, PRE-GC |
| F5 `ISSUE-PCD1B-STALE-TOMBSTONE-DISPLAY-METADATA-01` | A superseded generation's row tombstone at `ts(g)` also shadows display-only fs_object cells (`obj_name`, `full_path`, `mtime`) written at or before it. Reaffirmation restores the identity projection, not those cells; witness truth holds but display metadata can be lost | Medium — PRE-GC | GC / fs_objects metadata |
| F4 `ISSUE-PCD1B-MAPPING-PROJECTION-STABILITY-01` — **CLOSED by PR #233** | Historical #228 concern: a witness could rest on mapping authority A while ordinary readers later resolved mutable row B. #233 freezes the projection at the dominant timestamp, verifies it at EACH_QUORUM, pins productive readers to LOCAL_QUORUM, and guards the complete production mutation/reader inventory. PC-D1B.5 is not the owner. | Closed | PR #233 |

## 20. Out of scope

Mapping-authority representation and M18/M19 promotion; SYNC-ID-1
canonicalization; the productive consumer; PC-2; the Phase 5/6 fixes; G4/G5;
soft-delete serialization (`ISSUE-LIB-DELETED-FENCE-01`); GC activation.
`GC_ENABLED=false`.
