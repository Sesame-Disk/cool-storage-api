# ADR: X1/X2 Generational GC Fence

**Status:** Design accepted for review; r3 not frozen for implementation

**Date:** 2026-08-07 · **Last updated:** 2026-08-12

**Protocol revision:** r3 (2026-08-12). r1 (2026-08-11) was the first freeze. r2
(2026-08-12) added the `retire_claim_kind` column with the `RETIRING/QUARANTINE` and
`RETIRED/QUARANTINE` pointer states and their `RESOLVING` resolution workflow, raised
the quarantine transition and the first-writer `blocks` commit to `SERIAL + ALL` and
`QUORUM` respectively, moved the target engine to 5.0.9, made Cassandra topology
changes a gated maintenance operation, and bound quarantine resolution to the
terminality of `RETIRED` and the delayed-candidate ordering rule.

r3 alters the contract again, and the whole of the change is in the quarantine
resolution's **abort**. r2 let an operator abandon a `RESOLVING` resolution by reading
canonical state and writing `REJECTED`. That is not enough three times over, and the
three failures compose into one wedge:

- it classified from an ordinary read, which cannot see a live resolver or a pointer
  LWT accepted but not learned, so the rollback could be overtaken by the very CAS it
  was undoing (correction 168);
- it fenced at most the pointer partition, while the `QUARANTINED -> null` clear names
  no pointer column and by correction 68 may not, so a resolver stalled before its
  clear survived the fence (correction 174);
- it left no durable record of the abort itself, so a crash after the fences was
  indistinguishable from an ordinary claim takeover and a scanner resumed the very
  resolution being abandoned (correction 173).

An abort therefore now **linearizes on `blocks` first**: a `SERIAL + ALL` claim
takeover installs `retire_claim_kind=QUARANTINE_ABORT` with a fresh `(Cf, Nf)` and
`retire_abort_id`. That single LWT is durable abort authority and the revocation of
every resolution pointer CAS naming the old quarantine claim (correction 181). Only
after it applies does the work move `RESOLVING -> ABORTING`, then the generation's
monotonic `resolution_epoch` is fenced with the same serial-settlement and exact-retry
rules the pointer partition already has (corrections 177 and 178). It then rolls back
against all four reachable values of `gc_state`, since an authorized delete that
advanced to `DELETING` during the resolution forbids a `gc_state` rollback rather than
permitting one (correction 176, clarified by 179). Every post-`RESOLVING` resolver
mutation of `block_generations` — the `MATERIALIZING -> VERIFIED` repair as well as
`QUARANTINED -> null` — names that same `resolution_epoch` (correction 180). The fence
counter is initialized by the `MATERIALIZING` intent and never by a quarantine
transition, or it would be null on the first cycle and reset on the second
(correction 175). The human exit after `DELETE_ALREADY_ADVANCED` is a **new** work
identity with immutable `work_kind=SUCCESSOR_AFTER_DELETE` and, once authorized,
`decision=ALLOW_SUCCESSOR_AFTER_DELETE` — never a reopen of `REJECTED`, and never an
`OPEN` row that a scanner could mistake for "complete the quarantine" (corrections
182 and 185). After the quarantine handoff is confirmed, every post-handoff
`LOCAL_QUORUM` work-state read and mutation runs in the designated GC DC; operator
requests that arrive elsewhere are routed there (correction 184). The consistency
table no longer invents an `ABORTING -> RESOLVED` exit: once abort authority has
linearized, the terminal work state is `REJECTED`; the ordinary lost-race finishes
`RESOLVING -> RESOLVED` before abort authority exists (correction 186). A durable
**abort intent** is confirmed on the work row *before* the pointer fence, but it
grants **no** abort authority — only `QUARANTINE_ABORT` on `blocks` linearizes
revocation — so a crash between those writes still recovers exact actor/reason/
`(Cf,Nf,Df)` provenance (corrections 187 and 189). An abandoned `OPEN` +
`SUCCESSOR_AFTER_DELETE` row terminates as `REJECTED` without completing quarantine
or touching the pointer (correction 188); once that workflow has reached
`RESOLVING`, cancellation is a **successor-cancel abort** of the live
`QUARANTINE_ABORT` claim rather than `OPEN -> REJECTED` (correction 189), with
`abort_scope=POINTER_ONLY` so it must not run the generation `resolution_epoch`
fence (correction 190). The abort-intent LWT is single-assignment and SERIAL-settled;
a logical abort identity `A` is stable while pointer-fence attempts may be revised
only after a proven claim supersession (correction 189). Each fence attempt persists
the **full source authority tuple** — claim, epoch, kind, and `retire_abort_id` when
the source is already `QUARANTINE_ABORT` — so post-linearization ordinary takeover
is not misread as "F never linearized" (correction 190). r3 also removes a
verification and X2-closure requirement that contradicted the abort contract and made
the normative suite unsatisfiable (correction 169), gives the post-commit branch the
right quarantine path for a recertified or superseded pointer (correction 170), allows
a quarantined `MATERIALIZING` generation to be resolved at all (correction 171), and
freezes the delete-escalation columns the r2 schema named only in prose (correction
172). An implementation written against r1 or r2 is not compliant with r3. Nothing
here is implemented; the change is to the specification, not to running code.

**Audit baseline:** `main` at `186d7800d`

**Target engine:** Cassandra 5.0.9. The ADR's Cassandra semantics and acceptance
tests must be revalidated against 5.0.9; the repository Compose files use the exact
5.0.9 tag, while Phase 0 must still pin and verify the image digest. The official
image publishes `5.0.9`, `5.0.9-bookworm` and `5.0.9-trixie` (verified 2026-08-12); a
version tag is nevertheless mutable, which is why the digest requirement stands.

**Paxos variant policy:** Cassandra 5.0.9's semantically acceptable variants for
this ADR are `{v1, v2}`. That set is not a deployment policy: every
generation-fence writer deployment selects exactly one
`generation_fence_paxos_variant` from that set, and every generation-fence
participant node must report the selected target.

**Deployment model:** greenfield; no legacy block rows, objects, or production data

**Scope:** close `ISSUE-GC-UPLOAD-FENCE-REMATERIALIZATION-01` (X1) and
`ISSUE-GC-CROSS-DC-REFERENCE-VISIBILITY-01` (X2) without adding a new Paxos
operation to the writer hot path. The generation fence adds no LWT to pins, revalidation,
references, reuse, repair authorization, or deduplication hits. It adds one logical
activation CAS per materializing request, on the cold path where a retired logical
block comes back to life, with exactly one successful activation per
rematerialization.

SesameFS already contains unrelated coordination LWTs in and around upload
funnels — block metadata first-writer registration, released-stub repair claims,
`sha1`/`representation_id` backfills, block-upload session slots, head-commit
promotion, and file locks. Those are the existing baseline and are outside this
ADR's scope as operation counts. This document also strengthens every existing LWT
that can touch a generation-managed `blocks` partition to global `SERIAL` with regular
commit at least `QUORUM`; it adds no second first-writer LWT.

**Operational gate:** keep `GC_ENABLED=false` on every replica in every DC until the
acceptance criteria in this document are implemented and verified.

This document is the normative design record for the accepted X1/X2 work. It records
the reasoning, rejected alternatives, current code evidence, state transitions,
crash recovery rules, implementation phases, tests, rollout constraints, and all
corrections made during the audit. It does not claim that any blocker is closed.

## Document Roles

The repository has several documents with different responsibilities:

- `docs/KNOWN_ISSUES.md` owns live issue status and activation gates.
- `docs/OPEN-WORK-INDEX.md` owns the compact work index.
- `docs/UPLOAD-FENCE-FINDINGS-REGISTRY.md` owns the dated audit evidence and finding history.
- `docs/GC-UPLOAD-FENCE-PR-PLAN.md` records the historical PR-1 through PR-10 series and its research history.
- This ADR owns the accepted X1/X2 protocol and its implementation contract.

None of the existing status documents should mark X1, X2, or destructive GC as
closed until code and verification satisfy this ADR.

## Executive Decision

Use two independent safety mechanisms:

1. **X2:** keep fence-added ordinary writer operations — pins, references,
   revalidation, reuse, repair authorization, and deduplication — at
   `LOCAL_QUORUM` and add no LWT to them. Generation-fence writer mode nevertheless
   requires every existing `blocks`-partition LWT to use the global `SERIAL` domain,
   which can promote an existing multi-DC LWT from `LOCAL_SERIAL`. New global
   operations introduced by the fence are confined to lifecycle/ambiguity recovery,
   the rare rematerialization activation path, and the destructive-GC cold path. The
   rematerialization path performs the retirement-evidence `EACH_QUORUM` read
   followed by the `SERIAL + EACH_QUORUM` activation CAS.
2. **X1:** give every new physical lifecycle a never-reused UUID generation and
   immutable physical `storage_key`, so a delayed delete can target only the old
   generation.

This is not a claim that every upload is purely regional. A first upload of new
content still pays the existing block-metadata first-writer LWT. The target production
profile already selects global `SERIAL`, but this ADR also raises that LWT's regular
commit from the ordinary session `LOCAL_QUORUM` to `QUORUM` so v2 safety does not
depend on `paxos_state_purging=repaired`. X4 is the existing operation; its final
three-DC latency is a Phase-0 go/no-go input, not an unchanged-cost assumption.

The core protocol is:

```text
writer: read G -> pin G -> confirm/revalidate G -> use K -> publish ref -> remove pin
        (generation fence adds no LWT on this path)

GC: candidate -> blocks RETIRING -> drain uses -> global refs check
    -> blocks ACTIVE          (a reference, or uses > 0 and ALL of them expired;
                               the empty set is not "all expired" -- correction 55)
    OR
    -> append retirement evidence (G1, claim_epoch)
    -> publish handoff work
    -> blocks RETIRED
    -> persist delete-recovery discovery work
    -> generation gc_state=DELETING -> DELETE exact K
    -> generation gc_state=DELETED -> retire discovery work
 OR quarantine contradiction:
     -> publish + confirm quarantine work
     -> pointer RETIRING/QUARANTINE, or retained RETIRED/QUARANTINE
     -> drain uses -> generation gc_state=QUARANTINED
     -> no retirement evidence, G2 activation, delete, or automatic pointer exit
     -> operator only: work OPEN -> RESOLVING, then clear gc_state, then
        RETIRING/QUARANTINE -> ACTIVE, or RETIRED/QUARANTINE -> RETIRED/GC_RETIRE.
        A RETIRED pointer never returns to ACTIVE on the same generation.
```

Discovery always precedes the irreversible step, never follows it; see
Discoverability Before Irreversibility. The evidence and handoff steps are part of
the summary rather than a detail of the full sequence: the path to `RETIRED` cannot
be written without them, because both are ordered before the pointer CAS and
neither can be reconstructed afterwards.

The following statements are part of the contract:

- A pin is not a permanent liveness reference.
- Finding a pin with live authority during `RETIRING/GC_RETIRE` does not justify
  `RETIRING -> ACTIVE`; it justifies waiting. A generation with one or more
  remaining uses, all of which have expired their authority, does return to `ACTIVE`
  only for `GC_RETIRE`, to release the writer fence rather than to assert liveness.
- Any confirmed generation-bound reference, permanent or provisional, justifies
  returning a `GC_RETIRE` claim to `ACTIVE`; a pin holding live authority does not.
  A `QUARANTINE` claim never reactivates automatically.
- A provisional `up:` or `pub:` reference prevents deletion but does not by itself
  represent writer authority; it is nevertheless sufficient to return a
  `GC_RETIRE` claim to `ACTIVE` because reactivation is retention-safe and prevents a
  dead publish attempt from parking a hot logical block for its full TTL. It cannot
  clear a `QUARANTINE` claim.
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
- G2 is forbidden while G1 is `RETIRING` and is allowed only after G1 is
  `RETIRED/GC_RETIRE`; a `RETIRED/QUARANTINE` pointer remains fenced until operator
  resolution.
- The generation fence adds no LWT to pins, revalidation, references, reuse, repair
  authorization, or deduplication. It adds one logical activation CAS per
  materializing request; ambiguity retries reuse the same generation and epoch and
  cannot create a second generation.
- A global check that observes zero uses and then zero references closes the
  publication frontier for that generation. The read order is part of the proof.
- A quarantine that can make a pointer-selected generation unusable is writer-visible
  only after an acknowledged `SERIAL + ALL` transition. Serial settlement alone, or
  a Paxos v2 aggregate `EACH_QUORUM` commit, is not a per-DC quarantine fence.

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

Two tempting X1 alternatives are rejected:

- **S3 version IDs.** Exact version-aware PUT/HEAD/GET/DELETE could separate two
  lifecycles only if every supported object backend enabled compatible versioning,
  returned and persisted the version ID through every metadata/recovery path, and
  never fell back to key-only delete semantics. The current storage interface carries
  only a key, and provider version/delete-marker behavior is not uniform. UUID keys
  provide backend-independent physical identity instead.
- **A longer temporal fence or grace period.** No finite delay bounds execution of an
  already accepted object-store delete, coordinator timeout, retry, outage, or
  cross-DC propagation lag. More time reduces probability but cannot prove that an old
  delete finished before a byte-identical key is reused. It remains mitigation, never
  an X1 closure mechanism.

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
- The target engine is Cassandra 5.0.9. Cassandra 5.0.9 contains
  `eachQuorumForRead()` and permits `EACH_QUORUM` reads.
- The current multi-region Compose cluster has two DCs, `usa` and `eu`, with RF 1
  per DC. It is only regression coverage; acceptance requires the three-DC
  `dc-na`/`dc-eu`/`dc-asia` RF1/RF3 harness.
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
- Rematerialization of a `RETIRED/GC_RETIRE` logical block performs one logical activation
  operation per materializing request. It runs inline in the request that
  materialized the new generation; ambiguity retries reuse its same generation and
  epoch.

The Cassandra 5.0.9 source used to resolve the read-level question is:

`https://raw.githubusercontent.com/apache/cassandra/cassandra-5.0.9/src/java/org/apache/cassandra/db/ConsistencyLevel.java`

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
| Pin authorization (`PENDING -> AUTHORIZED`) | `LOCAL_QUORUM`; preserves the original authority deadline and remaining TTL, and an ambiguous result is confirmed by the full authority tuple |
| Generation revalidation | `LOCAL_QUORUM` |
| Generation lifecycle-state inspection after a known result, including `QUARANTINED` | `LOCAL_QUORUM` |
| Reuse, dedup, and normal metadata reads | `LOCAL_QUORUM` |
| Provisional/permanent reference insert/delete | `LOCAL_QUORUM` |
| Writer-originated recovery/expiry projection scans | `EACH_QUORUM`; projection writes remain `LOCAL_QUORUM`, so the scan must intersect the writer DC. A failed or unavailable DC holds the cursor and performs no cleanup |
| GC-owned candidate, handoff, and delete-work reads in the designated GC DC | `LOCAL_QUORUM`; these rows are produced and consumed under the single-DC GC ownership contract |
| Existing terminal first-writer metadata LWT (initial pointer only) | Existing LWT operation, with serial phase `SERIAL` and regular commit `QUORUM` when it can touch a generation-managed `blocks` partition. The fence adds no second LWT here; `QUORUM` avoids making `paxos_state_purging=repaired` a prerequisite for a v2 `LOCAL_QUORUM` commit |
| Rematerialization `G1 RETIRED/GC_RETIRE -> G2 ACTIVE` activation operation | LWT serial phase `SERIAL`, regular commit `EACH_QUORUM` on `blocks`; runs inline in the materializing request; physical CAS executions may retry the same logical operation |
| Quarantine discovery work | `LOCAL_QUORUM` in the originating writer/request path, then handed off and confirmed in the designated GC DC before any pointer/generation mutation; exact operation and contradiction identity. After that handoff is confirmed, the row is **owned by exactly one designated GC DC** (correction 184) |
| `ACTIVE -> RETIRING` (active pointer, `GC_RETIRE` or `QUARANTINE` claim acquisition) | LWT serial phase `SERIAL`, regular commit `ALL` on `blocks`; the `IF` clause must also match the observed `retire_claim_epoch` so the counter stays strictly monotonic, and the new claim kind is immutable for that epoch. `ALL` is the per-DC writer-visibility fence for both Paxos v1 and v2 |
| `RETIRED/GC_RETIRE -> RETIRED/QUARANTINE` (retained pointer fence) | LWT serial phase `SERIAL`, regular commit `ALL` on `blocks`; matches the exact retained `G1/E1/C1/N1/GC_RETIRE` claim, increments `retire_claim_epoch` to a new quarantine epoch, and changes only claim owner/deadline/kind plus that monotonic epoch, so no later G2 activation can satisfy its `GC_RETIRE` predecessor condition |
| Ambiguous `ACTIVE -> RETIRING` visibility reaffirmation | After `SERIAL` settlement identifies the exact `RETIRING/G/E/C/N/KIND` tuple, a second idempotent conditional write reasserts that tuple with serial phase `SERIAL` and regular commit `ALL`. No kind-specific drain starts until it applies and is acknowledged unambiguously |
| Claim takeover after `retire_claim_deadline` | LWT serial phase `SERIAL`, regular commit `EACH_QUORUM` on `blocks`; same epoch-matching shape on `RETIRING` or `RETIRED`; replaces owner/deadline/epoch without changing `active_state` or `retire_claim_kind`. When `kind=QUARANTINE_ABORT`, the `IF` matches the observed `retire_abort_id` and the `SET` MUST NOT write that column — ordinary takeover preserves abort-authority identity exactly. Only an authorized abort fence may change `retire_abort_id` (correction 190) |
| Use-row drain, including `PENDING`, `AUTHORIZED`, and materializer uses | `EACH_QUORUM` |
| Quarantine use drain | `EACH_QUORUM` until zero after an acknowledged `RETIRING/QUARANTINE` pointer fence; no retirement evidence, reactivation, or delete |
| Quarantine work post-handoff reads and state mutations | After handoff confirmation — or from creation for `work_kind=SUCCESSOR_AFTER_DELETE` — every `LOCAL_QUORUM` read or conditional write on `gc_generation_quarantines_by_day` that classifies or advances `OPEN`/`RESOLVING`/`ABORTING`/`RESOLVED`/`REJECTED` MUST execute in the designated GC owner DC. Operator/API requests that arrive in any other DC MUST be routed to that owner; they MUST NOT perform the mutation in the caller DC. If the owner DC is unavailable, the mutation fails closed / retries — it MUST NOT fall back to caller-DC `LOCAL_QUORUM` or auto-assume ownership in another DC. Successor rows are created in the owner DC; they do not use the writer-DC discovery exception. A scanner in the owner DC must never act on stale `OPEN` after an authorization that already returned (correction 184) |
| Quarantine work `OPEN -> RESOLVING` | LWT serial phase `SERIAL`, regular commit `LOCAL_QUORUM` in the owner DC; matches the full quarantine operation/evidence identity and immutable `work_kind`, fixes the prospective `(Cr, Nr)` when applicable, and is confirmed **before** any `gc_state` or pointer mutation. For `work_kind=QUARANTINE`, `decision` is `FALSE_POSITIVE`. For `work_kind=SUCCESSOR_AFTER_DELETE`, `decision` is `ALLOW_SUCCESSOR_AFTER_DELETE`. `work_kind` is immutable: no mutation may change `QUARANTINE` ↔ `SUCCESSOR_AFTER_DELETE`. This is not a `blocks` partition, so a later inventoried optimization may localize its serial phase; the one-serial-domain rule does not bind it |
| Quarantine work `OPEN -> REJECTED` | LWT serial phase `SERIAL`, regular commit `LOCAL_QUORUM` in the owner DC. For `work_kind=QUARANTINE`: ordinary contradiction rejection; quarantine retained. For `work_kind=SUCCESSOR_AFTER_DELETE`: successor authorization declined/cancelled **before** `RESOLVING`; pointer remains `QUARANTINE_ABORT`, generation untouched — this is **not** "complete quarantine" (correction 188). After `RESOLVING`, successor cancel is a successor-cancel abort (`abort_scope=POINTER_ONLY`, source kind `QUARANTINE_ABORT`) — not this transition (corrections 189 and 190) |
| Abort intent (durable, non-authoritative; correction 187/189/190) | LWT serial phase `SERIAL`, regular commit `LOCAL_QUORUM` in the owner DC on the `RESOLVING` work row. **First assignment** matches `resolution_state=RESOLVING`, `resolution_id=R`, and `pending_abort_id = null`, then installs immutable logical abort identity `pending_abort_id=A` plus actor/reason/`pending_abort_started_at` and the first pointer-fence attempt `F1` with **full source authority** `(source_claim_id, source_epoch, source_kind, source_abort_id)` and target `(Cf,Nf,Df)`. `source_abort_id` is null iff `source_kind=QUARANTINE`; required iff `source_kind=QUARANTINE_ABORT`. Remains `RESOLVING`. Grants **no** abort authority. Concurrent writers cannot overwrite `A` with `A2`; a loser observes canonical `A'` and returns already-in-progress/conflict — it does not adopt or rewrite actor/reason. Ambiguous results SERIAL-settle the work partition and exact-retry the same payload `(A,F,source_kind,source_claim_id,source_epoch,source_abort_id,Cf,Nf,Df)` — never invent a second logical abort. A **proven pre-linearization claim supersession** may conditionally install a new fence attempt `F2` under the same `A`. Linearization proof is `retire_abort_id=A`, not "kind still matches and epoch advanced". Intent alone must never cause `ABORTING`, generation fence, rollback, or a terminal work transition |
| Abort pointer fence (durable abort authority + linearization point) | LWT serial phase `SERIAL`, regular commit `ALL` on `blocks`; the **first** authoritative abort act. Matches the current attempt's full source tuple (`retire_claim_id/epoch/kind` and, when `source_kind=QUARANTINE_ABORT`, `retire_abort_id=source_abort_id`) and installs that attempt's `(Cf, Nf, Df)` with `retire_claim_kind=QUARANTINE_ABORT` and `retire_abort_id=A`, preserving `active_state`. Exact retry uses the full attempt payload. An ambiguous result leaves work still `RESOLVING` with the fence **unresolved** — SERIAL-settle / exact-retry; do not write `ABORTING`. Settlement classifies: live `QUARANTINE_ABORT` with `retire_abort_id=A` means this abort linearized (ordinary takeover may have advanced claim id/epoch afterwards); live source kind still matching **and** `retire_abort_id` still equal to `source_abort_id` (null for `QUARANTINE`) with epoch advanced means `F` never linearized — prepare `F'` under the same `A`; if the resolution/successor pointer step already committed, abort never linearized: finish `RESOLVING -> RESOLVED` and raise a new quarantine |
| Quarantine work `RESOLVING -> ABORTING` | LWT serial phase `SERIAL`, regular commit `LOCAL_QUORUM` in the owner DC; permitted only after the abort pointer fence has applied (or SERIAL settlement proves `retire_claim_kind=QUARANTINE_ABORT` **and** `retire_abort_id=A` for this work's logical abort). Copies provenance from the durable logical abort matching the pointer. For `work_kind=SUCCESSOR_AFTER_DELETE`, the pointer already reads `QUARANTINE_ABORT` under the **previous** abort `A0` while this work is still a live successor; that is not this cancel. Adopt `ABORTING` only when `retire_abort_id` equals this row's `pending_abort_id`. A crash after the pointer fence but before this write leaves work at `RESOLVING` with pointer already `QUARANTINE_ABORT/A` and intent still present; the scanner must adopt that exact logical abort into `ABORTING` and must **never** continue the abandoned resolution or successor |
| Abort generation fence (`resolution_epoch` bump after abort authority) | LWT serial phase `SERIAL`, regular commit `ALL` on the generation row; matches `(G, storage_key, quarantine_operation_id, resolution_epoch = Rr)` and installs `Rr + 1`. Runs only for `abort_scope=POINTER_AND_GENERATION` (`work_kind=QUARANTINE`), after abort authority is durable on `blocks` and work is `ABORTING` — including "nothing to undo" and the `DELETING`/`DELETED` branch. **MUST NOT run** for `abort_scope=POINTER_ONLY` (`work_kind=SUCCESSOR_AFTER_DELETE`): that work's `quarantine_operation_id` is a fresh `Q1` that was never installed on the generation row (`Q0` remains), so the `IF` cannot apply and would wedge `ABORTING` (correction 190). An ambiguous `POINTER_AND_GENERATION` result must `SELECT ... CONSISTENCY SERIAL` the generation partition and classify: settled `Rr + 1` with matching identity means this abort's fence already applied; settled `Rr` with matching identity means retry the exact same fence; any other settled identity or an unsettleable partition leaves the work `ABORTING` with no rollback |
| Quarantine work `ABORTING -> REJECTED` | LWT serial phase `SERIAL`, regular commit `LOCAL_QUORUM` in the owner DC. For `abort_scope=POINTER_AND_GENERATION`: permitted only after both fences have applied — restoring `gc_state = QUARANTINED` at `SERIAL + ALL` first when the generation step already ran. When `gc_state` has already reached `DELETING` or `DELETED`, perform no rollback of `gc_state`, materialization, storage identity, or delete lifecycle metadata; the abort's monotonic `resolution_epoch` fence may already have advanced and is retained. For `abort_scope=POINTER_ONLY`: permitted after the pointer fence alone; **MUST NOT** issue a generation LWT or `gc_state` rollback; records `abort_outcome=SUCCESSOR_CANCELLED`; generation row and `resolution_epoch` remain untouched. An ordinary read of the pointer never authorizes the rollback. Once abort authority has linearized, this is the only terminal work transition from `ABORTING` |
| Quarantine work `RESOLVING -> RESOLVED` (abort lost the pointer race) | LWT serial phase `SERIAL`, regular commit `LOCAL_QUORUM` in the owner DC; used when settlement proves the resolution pointer step committed before abort authority linearized. The abort never became durable; raise a new quarantine through the canonical path for the pointer's current state. There is **no** `ABORTING -> RESOLVED` transition |
| Operator `MATERIALIZING -> VERIFIED` repair (false-positive resolution) | LWT serial phase `SERIAL`, regular commit `ALL` on the generation row; matches the exact generation/key and quarantine operation/evidence identity **and `resolution_epoch = Rr`**. Required before `QUARANTINED -> null` when the object proves out. An abort that has already bumped past `Rr` makes this repair fail closed |
| Operator `gc_state QUARANTINED -> null` (false-positive resolution) | LWT serial phase `SERIAL`, regular commit `ALL` on the generation row; matches the exact `(G, storage_key, quarantine_operation_id, quarantine_evidence_digest)` identity **and `resolution_epoch = Rr`**, the value the `RESOLVING` record fixed — never null, because the `MATERIALIZING` intent initializes it to `0` at row creation and no quarantine transition writes it — after fresh exact-object verification. The epoch is what an abort bumps to fence this statement; the pointer takeover cannot, because correction 68 forbids a generation-row condition on `retire_claim_*`. It does **not** condition on `materialization_state`: a `MATERIALIZING` generation is quarantinable, so requiring `VERIFIED` here would make that class unresolvable. Where the object proves out, the marker is repaired to `VERIFIED` first under the same `resolution_epoch = Rr` condition |
| `RETIRING/QUARANTINE -> ACTIVE` (operator resolution) | LWT serial phase `SERIAL`, regular commit `ALL` on `blocks`; matches the retained quarantine claim; requires a confirmed delayed candidate and its discovery projection first |
| `RETIRED/QUARANTINE -> RETIRED/GC_RETIRE` (operator recertification) | LWT serial phase `SERIAL`, regular commit `ALL` on `blocks`; matches the retained quarantine claim, installs the next `retire_claim_epoch`, and requires a fresh global zero check plus confirmed fresh evidence for that epoch first. There is no `RETIRED -> ACTIVE` form |
| Final generation-reference check | `EACH_QUORUM` |
| `RETIRING/GC_RETIRE -> ACTIVE` (active pointer) | LWT serial phase `SERIAL`, regular commit `EACH_QUORUM` on `blocks` |
| Retirement evidence append (one row per `(generation, claim_epoch)`) | LWT serial phase `SERIAL`, regular commit `EACH_QUORUM`; `INSERT ... IF NOT EXISTS`, immutable and ordered before the pointer CAS |
| `RETIRING/GC_RETIRE -> RETIRED` (active pointer) | LWT serial phase `SERIAL`, regular commit `EACH_QUORUM` on `blocks` |
| Crashed-activation settlement read | `SELECT` on the `blocks` partition at read consistency `SERIAL`. This is the single normative form; an ordinary read at any level does not settle a pending proposal |
| `gc_state = null -> DELETING` (generation lifecycle) | LWT serial phase `SERIAL`, regular commit `EACH_QUORUM` on the G1 generation row; `IF gc_state = null AND materialization_state = VERIFIED AND storage_key = K1`, issued after the authoritative pointer/evidence proof and recording the authorizing claim |
| `DELETING -> DELETED` (generation lifecycle) | LWT serial phase `SERIAL`, regular commit `EACH_QUORUM` on the G1 generation row; `IF gc_state = DELETING` |
| Transition to `gc_state = QUARANTINED` (generation lifecycle) | LWT serial phase `SERIAL`, regular commit `ALL` on the generation row; `IF gc_state = null AND storage_key = K`, plus the `materialization_state`/operation-ID identity in the `MATERIALIZING` form. It records the operation ID and evidence digest. `ALL` makes the unusable state visible to every later local writer read |
| Ambiguous quarantine visibility reaffirmation | `SELECT` the exact generation partition at read consistency `SERIAL`. If the settled row is the exact `QUARANTINED/G/K/R/T/D` tuple, conditionally reassert that same tuple at `SERIAL + ALL`; if it remains the expected unquarantined tuple, retry the original quarantine at `SERIAL + ALL`. Uncertain, unavailable, non-applied, or different-terminal outcomes preserve data and grant no globally established quarantine conclusion |
| Ambiguous non-quarantine generation-lifecycle LWT reconciliation | Settle the exact generation row with `SELECT ... CONSISTENCY SERIAL`; a matching applied `DELETING`/`DELETED` identity is accepted only with its recorded generation/key and delete-claim identity, while a conflicting terminal state fails closed. A matching `DELETED` row retires work only after exact-key absence is confirmed. An ordinary read is not sufficient to settle a pending proposal |

The ordinary-query session default remains `LOCAL_QUORUM`. Every generation-fence LWT
sets both levels explicitly rather than inheriting that regular default. The
non-quarantine global lifecycle form is:

```go
query.Consistency(gocql.EachQuorum).
    SerialConsistency(gocql.Serial)
```

The first-writer and other otherwise-local `blocks` LWTs use regular `QUORUM`; the
retiring and quarantine visibility fences use regular `ALL`. No generation-fence LWT
uses regular `LOCAL_QUORUM` or `ANY`. Ordinary non-LWT pins, references, and writer
reads/writes remain `LOCAL_QUORUM`.

The current `internal/db/db.go:94-95` does explicitly assign
`cluster.SerialConsistency`. The current configuration and tests still include
`LOCAL_SERIAL` in the multi-region profiles, so the final X1/X2 profile must use
`SERIAL` for every LWT that can touch `blocks`; no such statement may run with an
effective serial level of `LOCAL_SERIAL` under the generation-fence writer-mode gate.
Read that as a rule about statements rather than about the session default — the
split between what startup can check and what only a helper and a test can is in
Which Serial Level Is The Session Default. The global session
configuration must not be changed to `EACH_QUORUM`; regular global operations set
that level per query.

### Which Serial Level Is The Session Default

"Unrelated LWTs may keep their existing consistency" and "the gate rejects
`LOCAL_SERIAL`" cannot both be satisfied by accident, because **no per-query serial
level exists anywhere in this repository today**. `internal/db/db.go:95` assigns
`cluster.SerialConsistency` from one config value and not a single statement
overrides it. This ADR chooses option A:

| Option | Session default | What must be written per query | Consequence |
|---|---|---|---|
| **A — selected** | `SERIAL` | none for the initial implementation; a later, separately inventoried optimization may set explicit `LOCAL_SERIAL` only on unrelated partitions | a forgotten `blocks` LWT is still correct; unrelated LWTs are globally serialized unless deliberately localized |
| **B** | `LOCAL_SERIAL` | an explicit `SERIAL` on every LWT in the `blocks`-partition inventory | unrelated LWTs stay regional; a forgotten `blocks` LWT is a **correctness defect**, silently in the wrong Paxos domain |

The asymmetry is the whole decision: A fails safe and costs latency, B fails unsafe
and costs nothing. Safety wins. Generation-fence writer mode therefore requires a
session default of `SERIAL`; PR-2 does not downgrade unrelated statements in the
initial implementation. Any later localization is a measured optimization with its
own complete non-`blocks` inventory and regression tests.

Which profiles actually change is not obvious from `config.prod.yaml` alone, and
getting it backwards misprices A:

```text
already SERIAL   config.prod.yaml:52, config-usa.yaml:23, config-eu.yaml:23,
                 config.docker.yaml:21, config.example.yaml:26
                 -- but every one of these declares a SINGLE DC, where SERIAL
                    and LOCAL_SERIAL coincide and A costs nothing

still LOCAL_SERIAL   config-usa.cluster.yaml:27, config-eu.cluster.yaml:27,
                     docker-compose.mr-cluster.yaml:245-248,285-288
                     -- the genuinely multi-DC profiles, which is exactly where
                        X1/X2 lives and where A is a real cross-DC cost
```

So option A does not "promote" anything in the single-DC profiles; it changes
nothing there. It promotes head-commit promotion, file locks, block-upload session
slots and the `gc_leases` finalize lease to cross-DC Paxos **in the multi-DC
profiles**, three of them on the upload path. PR-0 measures those four under `SERIAL`
in a multi-DC topology to quantify the accepted cost and to inform any later,
separately reviewed localization — the same discipline this document already applies
to the activation CAS.

The one-domain rule is a **statement-level** invariant, and a startup gate cannot
check it: nothing at boot can enumerate the consistency level of every LWT written in
Go. Splitting the enforcement is therefore part of the decision, not an
implementation detail:

| Enforced by | What it covers |
|---|---|
| Startup/eligibility gate | topology, DC set and RF, every generation-fence participant node's selected `generation_fence_paxos_variant`, and session serial level `SERIAL` |
| A single helper | every LWT on the `blocks` partition goes through one constructor that sets the serial level; no `blocks` LWT is issued through a raw `Session().Query` |
| Tests and writer-path tracing | the inventory in Current Code Evidence is enumerated by test, and each statement's effective level asserted |

This is the technique the org-scoped-key series already used: make the compiler and
the helper carry the invariant so a reviewer does not have to. An assertion phrased
as "the gate asserts the effective per-statement level" is not implementable at
startup and would have been satisfied by a config-string check that proves only the
selected safe default, not that a future explicit per-query override stayed outside
the `blocks` inventory.

The existing terminal first-writer LWT retains its logical operation; when it can
touch a generation-managed `blocks` partition, its regular commit is strengthened to
`QUORUM`. The generation fence must not add a second LWT around it. Its **serial**
phase is not exempt: it competes on the `blocks` partition and is named in the
inventory above.

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
| **Existing hot path** | Block-metadata first-writer `INSERT ... IF NOT EXISTS` (X4), released-stub repair claims, `sha1`/`representation_id` backfills, block-upload session slots, `gc_leases` finalize lease, head-commit promotion, file locks | Per block or per request | Existing operations; generation-managed `blocks` LWT consistency is strengthened, while X4 names only the first-writer cost |
| **New hot path** | **None** | — | — |
| **Rare rematerialization** | `G1 RETIRED/GC_RETIRE -> G2 ACTIVE` activation | Only when a retired SHA comes back to life | Materializing request, inline |
| **GC cold path** | `ACTIVE -> RETIRING`, retirement evidence `INSERT ... IF NOT EXISTS`, `RETIRING/GC_RETIRE -> ACTIVE`, `RETIRING/GC_RETIRE -> RETIRED`, `gc_state = null -> DELETING`, `DELETING -> DELETED`, quarantine | Per collected generation | GC worker |

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
`materialize()`. Therefore, for funnels and retries that reach this sequence:

> Every block invocation that reaches `RegisterUploadedBlock` pays one first-writer
> Paxos round, including a reusable-row outcome where the LWT cannot apply. Browser
> and sync preflight may reject a fully deduplicated block before this sequence, so
> “every block of every upload” is too broad.

It is global in the target production profile because `configs/config.prod.yaml:52`
and `.env.prod.example` select `SERIAL`, and this ADR's option A requires that domain
from the first generation-aware write. Deployments currently on `LOCAL_SERIAL` are
promoted; already-`SERIAL` deployments do not acquire a new round. This is finding X4,
and it is the only Paxos in the system whose count scales with metadata-materializing
block volume.

Three measurements belong in Phase 0 before any latency decision is made:

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
- **Foreground first-writer path**, including p50/p95/p99/error rate from each region,
  a representative new-content 1 GiB upload at each funnel's real block concurrency,
  unrelated option-A LWTs, and Cassandra QPS/tombstone pressure before and after the
  proposed use/reference protocol. Phase 0b is NO-GO if predeclared endpoint SLOs or
  capacity headroom do not hold.

A probe fast path is the obvious lever and is compatible with this design: the
probe has already read the row, so a `Reusable` outcome with complete metadata can
skip the LWT entirely. The asymmetry makes it safe — a false negative (a
`LOCAL_QUORUM` probe not seeing a row written in another DC) falls through to the
LWT and is correct, while a false positive is impossible because a row that was
never written cannot be read. Placement columns are first-writer-wins and
immutable, so a complete row has no race left to resolve.

That optimization does not help first upload of new content, a first-writer race, or
rematerialization; each still needs its own linearization decision. It is not the
viability answer for the expensive case it cannot cover.

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

This is why the generation fence adds no new LWT or global coordination to pins,
references, reuse, or deduplication. It does **not** mean every existing
`blocks`-partition LWT remains regional: writer mode requires those statements to
use the global `SERIAL` domain. New global operations introduced by the fence are
confined to lifecycle/ambiguity recovery, the activation CAS a materializing request
makes when it rematerializes a retired block, and the GC cold path.

### `NetworkTopologyStrategy` Is Load-Bearing

The entire X2 argument rests on `EACH_QUORUM` contacting a quorum in every DC. In
Cassandra 5.0.9 that behaviour is conditional on the keyspace using
`NetworkTopologyStrategy`, and the two failure modes are not symmetric:

| Path | Behaviour under a non-NTS keyspace |
|---|---|
| `EACH_QUORUM` **read** | `ReplicaPlans.contactForRead()` only routes to `contactForEachQuorumRead()` for `NetworkTopologyStrategy`. Otherwise it falls back to the ordinary `blockFor` selection, which is a plain global quorum. **The read succeeds and silently loses the per-DC intersection property.** |
| `EACH_QUORUM` **LWT commit** | `ConsistencyLevel.validateForCasCommit()` calls `requireNetworkTopologyStrategy()` and throws `InvalidRequestException`. Loud, fail-closed. |

The read case is the dangerous one: a deployment on `SimpleStrategy` would pass a
naive smoke test while providing exactly the guarantee X2 says is insufficient — a
global quorum that need not read from any particular DC.

This repository still supports `SimpleStrategy` as a legacy fallback
(`docker-compose.prod.yml:176,184`), so the assumption cannot be left implicit.
### Two Gates, Not One

The startup assertions in this document belong to **two different gates**, and
conflating them leaves the earlier one unenforced during the window it exists for:

```text
generation-fence writer mode enabled       (rollout step 5: verified admission barrier)
    startup: NetworkTopologyStrategy verified
    startup: DC set and per-DC RF verified
    startup: session serial default is SERIAL
    startup/eligibility: every generation-fence participant node reports the
                         selected generation_fence_paxos_variant target
    startup/runtime: every app/Cassandra clock attestation is fresh and in bounds
    startup/runtime: every app region reaches each authoritative storage target
    deployment gate: every writer is generation-capable; no legacy writer remains
    code + tests: every blocks LWT has effective SERIAL

destructive GC enabled                     (rollout step 8)
    requires the writer gate above, plus
    all X1/X2 acceptance criteria verified
```

### Generation-Fence Participant Eligibility Is Continuous

A **generation-fence participant node** is every joined Cassandra node in the
configured participating DC set that either can coordinate a generation-fence
statement or can own a replica for the generation-fence keyspace. Coordinator
selection does not remove a node from this set: a node that is never chosen as the
initial CQL coordinator can still receive Paxos work as a replica.

The writer-mode gate is not only a startup photograph. Every participant node must be
verified for all of the following:

- the pinned release and image/artifact identity;
- the selected `generation_fence_paxos_variant` target;
- the expected DC and topology membership.

A rejoined node starts **unverified** and cannot restore writer eligibility until its
local facts are checked. A genuinely new or replacement node is different: Cassandra
may assign pending ranges and replica ownership during bootstrap before SesameFS can
verify it, so adding it while writer mode is live is forbidden by the maintenance
procedure below. An unavailable or unverifiable participant makes generation-fence
writer mode unavailable until that node is verified or removed from the ring/replica
topology by the corresponding Cassandra operational procedure. It is not sufficient
to exclude that node from application host selection. A participant whose local
configuration or membership no longer matches loses eligibility immediately. The
implementation may choose the orchestration and inspection API, but it must not
silently treat a previously verified node as verified forever.

### Cassandra Topology Changes Are Maintenance Operations

The local-write/global-read intersection is proven against one stable replica layout.
SesameFS does not attempt to prove it through Cassandra's intermediate pending-range,
streaming, or RF-transition states. The following operations are forbidden while
generation-fence writer mode or destructive GC is live: add/bootstrap, replace,
remove/decommission or move a node; change tokens, DC, rack, snitch placement,
replication strategy, per-DC RF, or the participating DC set.

Every such operation uses this maintenance sequence:

1. Acquire the durable cluster-wide topology-maintenance lease/marker before any
   Cassandra topology or `ALTER KEYSPACE` command. The marker is owned by the
   maintenance controller, has an owner ID and expiry, is confirmed at
   `EACH_QUORUM`, and makes writer mode and destructive GC ineligible; stale-marker
   takeover is allowed only after an audited health/ownership check.
2. Quiesce block-write admission, drain or cancel in-flight generation-aware work,
   pause generation-fence writer mode, and pause destructive GC. No operator may
   begin the Cassandra mutation until this marker and drain state are confirmed.
3. Record the expected post-change node IDs, tokens, DC/rack placement, replication
   strategy, participating DC set, and per-DC RF.
4. Run the Cassandra-supported topology procedure. Complete bootstrap/streaming or
   decommission/removal, then run every full repair required to populate the affected
   generation-fence ranges after an RF, DC, or ownership change. This operation is
    **not permitted while the selected Paxos target is v1**: Cassandra 5.0.9's
   topology-change linearizability guarantee is v2-only, and a manual v1
   `--paxos-only` repair is not accepted as a substitute safety proof. Migrate to a
   uniform v2 target using the repair-first transition procedure before changing
   ownership, RF, DC, rack, strategy, or tokens. Verify `paxos_repair_enabled`,
   `skip_paxos_repair_on_topology_change`, per-keyspace skip lists, and the selected
   topology-repair quorum settings before proceeding.
5. Prove there are no pending ranges, joining/leaving/moving nodes, incomplete streams,
   or required repairs outstanding.
6. Reverify the complete participant set: release and artifact identity, node IDs and
   tokens, DC/rack placement, keyspace strategy and RF, selected `paxos_variant`, clock
   health, and authoritative storage eligibility.
7. Restore writer mode only after every eligible writer and Cassandra participant
   passes. Run dry-run reconciliation before separately restoring destructive GC.
8. Release the maintenance marker only after the post-change gates pass; a failed
   operation leaves the marker held and writer mode unavailable until recovery is
   audited.

A config rollout before or after `ALTER KEYSPACE` is not this proof. The gates stay
closed for the entire interval in which configured and actual replica layouts differ.

The selected Paxos target is fixed before the first generation-aware write. Greenfield
writer mode may select either semantic target for steady-state lifecycle operations,
but any topology/RF/DC/rack/strategy/token mutation requires a uniform v2 target;
v1 is a stable-layout-only target. Changing from `v1` to `v2`, or from `v2` to `v1`,
requires this explicit procedure:

1. Pause generation-fence writer mode and fail closed for generation-aware writes.
2. Execute the SesameFS generation-fence variant-transition procedure. For
   a greenfield cluster, set the selected target uniformly before its first LWT. If
   any LWT has run under v1, `v1 -> v2` follows the later, stricter Apache procedure:
   all nodes on one supported Cassandra version, then successful
   `nodetool repair --full -pr` on **every joined Cassandra cluster node**, including
   nodes outside the generation-fence keyspace's participating DC set, before the
   variant change and rolling restart. Cassandra 5.0.9's bundled `NEWS.txt` says v2
   may be enabled at any time and omits that one-time repair, while CASSANDRA-21316
   added the stricter instruction to 5.0.9/current 5.0 documentation. SesameFS resolves
   the upstream conflict conservatively; it does not misstate either source as the
   unqualified 5.0.9 binary contract. For `v2 -> v1`, the later procedure permits a
   rolling rollback without that repair; writer mode still remains paused until every
   participant is uniform and re-verified.
3. Bring every joined cluster node to the new selected target; a mixed target state is
   not an accepted writer-mode deployment.
4. Re-verify build identity and selected variant for every joined cluster node, plus
   DC/topology membership for the complete generation-fence participant set.
5. Re-enable generation-fence writer mode only after the complete verification passes.

Generation-fence LWTs never use commit consistency `ANY` or `LOCAL_QUORUM`; the
consistency helper and inventory test reject both, using `QUORUM`, `EACH_QUORUM`, or
`ALL` as specified. `paxos_state_purging=repaired` is therefore not a prerequisite for
this ADR: upstream ties it to recurring Paxos repair and to permitting optimized
`ANY`/`LOCAL_QUORUM` commit behavior, not to selecting v2 itself. Phase 0 records the
value on every joined Cassandra node. Once a cluster moves from `legacy` to either
`gc_grace` or `repaired`, returning to `legacy` is forbidden. If operators choose
`repaired`, recurring Paxos repair is mandatory even after a `v2 -> v1` rollback;
use the upstream `gc_grace` fallback instead if repaired purging must be disabled.

Every assertion in the first block binds from the **first generation-aware write**,
not from `gc.enabled=true`. The reason is concrete: a materializer can crash between
its PUT and its activation LWT on day one of the writer rollout, and the recovery
that follows needs the serial round and the correct topology — multiple rollout steps
before destructive GC is switched on. Wherever this document says "when
`gc.enabled=true`" for one of these writer-mode checks, it means the writer gate.

The implementation must:

- verify the keyspace replication strategy at startup, and refuse to boot when
  generation-fence writer mode is enabled and the strategy is not
  `NetworkTopologyStrategy`;
- verify that the keyspace DC set exactly matches the configured participating DC
  set and that every participating DC has exactly its configured RF, with RF > 0;
- verify that the configured session serial level is `SERIAL`, as selected in Which
  Serial Level Is The Session Default. Startup can check the
  configuration; it cannot enumerate the serial level of every LWT written in Go, so
  "every conditional write that can touch the `blocks` pointer partition runs at
  `SERIAL`" is carried by one helper plus a test over the inventory, not by this
  gate. `LOCAL_SERIAL`
  may remain available for unrelated partitions, but mixing serial domains on the
  same pointer partition is not accepted. **This requirement binds from the first
  generation-aware write, not from `gc.enabled=true`**: a deployment that writes
  generations under `LOCAL_SERIAL` and only switches to `SERIAL` when GC is enabled
  leaves each `blocks` partition with Paxos history produced under two different
  participant sets, which is the very condition the gate exists to prevent. The
  rollout sequence below deliberately activates GC several steps after writers go
  live, so tying the gate to `gc.enabled` would leave exactly that window open;
- require the durable cluster-wide topology-maintenance marker to be absent before
  writer mode is eligible, and require its acquisition before any topology or RF
  mutation. The marker owner, expiry, takeover, and release are audited by the
  maintenance controller; polling Cassandra pending ranges alone is not a start
  interlock;
- reject commit consistency `ANY` and `LOCAL_QUORUM` in the generation-fence LWT
  helper and inventory tests. Record `paxos_state_purging` on every joined cluster
  node; once any node moves from `legacy` to `gc_grace` or `repaired`, prohibit a
  return to `legacy`. If any node uses `repaired`, require and monitor recurring Paxos
  repair, including under v1, but do not require `repaired` for LWTs that commit at
  `QUORUM` or stronger;
- verify that **every generation-fence participant node** reports the selected
  `generation_fence_paxos_variant` target — not merely that one observed coordinator
  avoids `*_without_linearizable_reads*`. A single-node observation is not sufficient
  evidence for this gate: a non-coordinator can still own a replica for the keyspace.
  For Cassandra 5.0.9, `{v1, v2}` is the semantic allowlist, but PR-0 must select
  exactly one target and the gate must reject any participant that reports the other
  value, an unrecognised value, or a value without linearizable reads. A topology
  maintenance gate additionally rejects v1, because v1 is stable-layout-only. A deny-list
  admits every variant the engine has not shipped yet, which is the wrong default for
  a property that fails silently: an unrecognised value must fail closed and force an
  explicit review, exactly as generation validity is a positive predicate rather than
  "not `QUARANTINED`" (correction 72). This is the fourth check, and it is listed
  here rather than only in How To Issue The Settlement Read because leaving it out of
  this enumeration is how it ends up implemented as a GC-time assertion — the failure
  it prevents is a crashed activation on day one of the writer rollout;
- verify fresh clock-health attestations for every application and Cassandra
  participant against `generation_fence_max_clock_skew`; a missing, stale, or
  out-of-bound attestation fails writer mode;
- verify from every application region that every configured persisted storage class
  resolves to and can access the same authoritative target; an asynchronous local
  replica without authoritative fallback is not eligible;
- verify through deployment inventory that every process capable of writing blocks
  is generation-capable and that no deterministic-key or legacy-reference writer can
  receive traffic. This is an orchestration assertion, not something Cassandra
  startup can infer;
- treat this complete set of checks as the generation-fence writer-mode gate, which is a
  prerequisite of the destructive-GC activation gate — not as an operational note.

### `EACH_QUORUM` Versus `ALL`

`EACH_QUORUM` is preferred for destructive **reads** and for cold-path LWTs that do
not establish writer visibility. With RF 1 per DC it is equivalent to `ALL`. With
RF 3 per DC, an ordinary `EACH_QUORUM` read requires a quorum in every DC rather than
every replica in every DC, so it preserves the per-DC intersection property while
tolerating one replica failure per DC. A plain global `QUORUM` is not an equivalent
substitute because it can satisfy the total quorum without reading from one
particular DC.

`ACTIVE -> RETIRING` is the first deliberate exception and commits at `ALL`. The
publication-frontier proof needs the pointer fence visible to every later
`LOCAL_QUORUM` writer revalidation, and Cassandra 5.0.9 Paxos v2 does not retain
ordinary `EACH_QUORUM`'s per-DC response accounting in its LWT commit callback at RF
greater than one. `ALL` is cold-path cost paid once per candidate; it is not applied
to pins, references, or normal writer operations.

`gc_state = null -> QUARANTINED` is the second exception. It may invalidate the
generation still selected by an `ACTIVE` pointer, while writers inspect that
generation row at `LOCAL_QUORUM`. A Paxos v2 aggregate commit that omits one DC would
let that DC continue to authorize a physically or logically contradicted generation.
The quarantine transition and its ambiguity-only identity reaffirmation therefore use
`ALL`. `DELETING` and `DELETED` remain at `EACH_QUORUM`: they are reached only after
the pointer's acknowledged `ALL` retirement fence has already made the generation
unacquirable, so stale state there is restrictive rather than permissive.

This is an implementation fact, not an inference from the public consistency-level
description. In Cassandra 5.0.9, `Paxos.Participants.requiredFor()` converts the
requested regular level to one `blockForWrite` integer, and `PaxosCommit` completes
from one aggregate response counter; the class explicitly notes that it does not
support `EACH_QUORUM` distribution because there is no `EACH_SERIAL`. The ordinary
write path instead uses `DatacenterSyncWriteResponseHandler`, which tracks one count
per DC. PR-0 pins and regression-tests that exact source/build behavior rather than
assuming the ordinary-write documentation also describes the Paxos v2 callback.

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
                 |           +-- GC_RETIRE -> G2 may activate through the pointer CAS
                 |           +-- GC_RETIRE -> QUARANTINE claim (retained pointer)
                 |           +-- QUARANTINE -> GC_RETIRE (operator recertification:
                 |                             new epoch + fresh evidence)
                 |
                 |           RETIRED never returns to ACTIVE on the same generation.
                 |
                 +-- error, reference, or expired uses -> ACTIVE (GC_RETIRE only)
                 +-- QUARANTINE -> generation QUARANTINED; no automatic pointer exit
                 +-- QUARANTINE -> ACTIVE (operator resolution; RETIRING only,
                                   never reached RETIRED, delayed candidate first)

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
- operation/owner ID, recorded as `materialization_owner` and equal to the
  `operation_id` on the materializer's use row;
- materialization deadline;
- `materialization_state` and timestamps;
- `resolution_epoch = 0`, the generation partition's abort fence counter. It is
  initialized here, at row creation, and not by the quarantine transition — see
  correction 175;
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

#### Authoritative Storage Is Globally Addressable

`ACTIVE` is not a promise that asynchronous bucket replication has completed. The
production contract is instead that the persisted `active_storage_class` resolves,
from every application region, to the **same authoritative storage target** that
accepted and verified `K`. A region may use a local cache or replica as an
optimization, but absence there must fall back to the authoritative target; it may
not turn a globally active generation into a regional 404.

This matches the current production architecture: a block placed in
`hot-s3-na` remains addressed through that class and remote regions read the NA
bucket rather than silently substituting their own regional class. The materializer
verifies the exact authoritative `(storage_class, storage_key)` before activation.
Writer-mode eligibility separately verifies that every application region can resolve
and access every configured authoritative class.

The existing MinIO cluster harness maps one class name to region-local asynchronously
replicated buckets, so it does **not** satisfy this contract by construction. The
generation-fence harness must either route all regions to one authoritative endpoint
per class or implement and test authoritative fallback. Waiting for asynchronous
replication in the activation request is explicitly rejected: it couples Cassandra
availability to an external replication SLA and still does not prove future regional
readability.

### `RETIRING`

`RETIRING` is a temporary writer fence with an immutable claim kind. New writers
cannot use G1 and cannot create G2. Under `retire_claim_kind=GC_RETIRE`, an existing
writer whose pin was already authorized for G1 may finish and publish while G1 is
`RETIRING`; the GC sees the pin and waits. Under `retire_claim_kind=QUARANTINE`, no
new physical operation or publication is permitted after observing the fence and the
worker drains all pre-fence uses before establishing quarantine. A writer with a `PENDING`
pin cannot publish until it revalidates while G1 is `ACTIVE`, but an unexpired
`PENDING` row still blocks retirement because it may have been revalidated before
the committed fence.

A generation-bound `up:`, `pub:`, or `fs:` row found by the final global check
returns G1 to `ACTIVE` only for a `GC_RETIRE` claim. This is safe even when a
provisional row is stale: the result is temporary retention, not deletion. The
owner/expiry path can later remove the row and create a fresh candidate. A
`QUARANTINE` claim never reactivates automatically, regardless of references or
expired uses.

`RETIRING` is a fence on writers, so a `GC_RETIRE` claim must not become a parking
state. It ends on
any of four outcomes: a reference is found; at least one use exists and every
remaining use has expired its authority; the drain reaches zero; or a liveness read
fails and, after the delayed candidate is durable, the worker conservatively returns
the pointer to `ACTIVE`. The error branch appends no evidence and deletes nothing.
Never decide from "some row still exists". See Escaping `RETIRING` On Abandoned Uses.

### Retirement Evidence

Retirement evidence is not a state. It is an append-only row meaning "a global zero
check for this generation passed under this claim epoch", written before the pointer
CAS and never overwritten. It is not visible on the active pointer and never
authorizes deletion by itself; the authoritative pointer must also have left
`RETIRING`. See Retirement Handoff.

### `RETIRED`

`RETIRED` is an authoritative `blocks.active_state` value. For a
`RETIRED/GC_RETIRE` pointer, it means the GC has established the publication frontier
at `EACH_QUORUM`:
it observed zero use rows first, then zero generation-bound references, so no
operation capable of legally publishing a reference remains. G2 may now be
materialized. A `RETIRED/QUARANTINE` pointer is the exception: it is writer-fenced
and cannot begin G2 materialization until the explicit operator resolution workflow
recertifies it to `RETIRED/GC_RETIRE`. The conversion from `RETIRED/GC_RETIRE`
increments `retire_claim_epoch`, so an activation CAS carrying the old `GC_RETIRE`
predecessor tuple fails.

`RETIRED` is terminal for its generation under both claim kinds. Neither the
quarantine conversion nor its resolution ever returns the pointer to `ACTIVE` on G1;
the only exit from `RETIRED` is a successor generation. That terminality is what makes
a `DELETING` authorization irrevocable once obtained, so it is a safety invariant
rather than a stylistic choice — see Authorizing `DELETING`.

G1 remains addressable by its own identity until its delete is complete;
after physical deletion, the logical pointer may still retain G1 as a historical
retired predecessor until G2 replaces it.

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
retire_claim_kind       -- GC_RETIRE, QUARANTINE, or QUARANTINE_ABORT;
                           immutable for one claim epoch
retire_abort_id         -- set only while kind = QUARANTINE_ABORT; the durable
                           abort identity that linearized on this partition
```

Every worker-owned pointer transition must match the current claim ID, epoch, and
kind. `RETIRING -> ACTIVE` and `RETIRING -> RETIRED` additionally require
`retire_claim_kind=GC_RETIRE`; no automated pointer exit exists for a quarantine-kind
or quarantine-abort-kind claim. Writers treat `QUARANTINE` and `QUARANTINE_ABORT` as
the same fence: neither kind authorizes pin, authorize, publish, or physical delete.
Generation-row transitions do not condition across tables; see Conditional
Generation-Lifecycle Transitions.

The deadline is **not** a fourth condition on those transitions. Cassandra cannot
compare against "now", so `retire_claim_deadline` is not a hard expiry that
invalidates a worker at the instant it passes: it is the point at which the claim
becomes **eligible for takeover**, and what actually invalidates the stale worker is
the takeover LWT bumping `retire_claim_epoch`. An expired-but-uncontested claim is
still a valid claim, and its owner's transitions still apply. Stating it as "must be
within the deadline" reads as a check an implementer would then go looking for.

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
acquisition, conditioned on the epoch it observed. The same shape applies while
`active_state` is `RETIRING` or `RETIRED`; the statement preserves that state.
Ordinary takeover replaces owner, epoch, and deadline only:

```text
takeover
    IF active_generation_id = G1
    AND active_state       = RETIRING or RETIRED     -- preserved, not rewritten
    AND retire_claim_id    = Cold
    AND retire_claim_epoch = Nobserved
    AND retire_claim_kind  = existing kind
    AND (kind != QUARANTINE_ABORT OR retire_abort_id = Aobserved)
    SET retire_claim_id       = Cnew
        retire_claim_epoch    = Nobserved + 1
        retire_claim_deadline = Dnew
        retire_claim_kind     = existing kind
    -- MUST NOT SET retire_abort_id. When kind = QUARANTINE_ABORT, abort-authority
    -- identity is preserved exactly. Only an authorized abort fence may change it.
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
        retire_claim_kind     = GC_RETIRE or QUARANTINE

RETIRING -> ACTIVE                RETIRING -> RETIRED
    IF active_generation_id = G1      IF active_generation_id = G1
    AND active_state    = RETIRING    AND active_state       = RETIRING
    AND active_epoch    = E1          AND active_epoch       = E1
    AND retire_claim_id    = C        AND retire_claim_id    = C
    AND retire_claim_epoch = N        AND retire_claim_epoch = N
    AND retire_claim_kind = GC_RETIRE       AND retire_claim_kind = GC_RETIRE
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
retire_claim_kind     = null
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
`G1 / E1 / RETIRED/GC_RETIRE / C1 / N1` condition, which only the claim-holding retire worker
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
or references. Settlement establishes which value won Paxos; it does **not** prove
that an ordinary local read in every DC can already observe that value. That
distinction is load-bearing in three DCs: `SERIAL` can settle through a global
majority that omits one DC, while writers in that DC revalidate the pointer at
`LOCAL_QUORUM`.

The ambiguity path therefore has two separate gates:

```text
ambiguous ACTIVE -> RETIRING result
    -> SERIAL SELECT settles the blocks partition
    -> matching RETIRING/G/E/C/N/KIND:
         conditionally reassert that exact tuple at SERIAL + ALL
         preserve the original claim deadline and kind; do not extend/change either
         acknowledged applied result: visibility fence complete; begin the drain
         ambiguous/not-applied/unavailable: do not drain; reconcile and retry
    -> ACTIVE: re-issue the same claim LWT at SERIAL + ALL; do not drain yet
    -> different claim/epoch or terminal state: stale worker; stop
    -> uncertain serial result or unavailable DC: retain ACTIVE/RETIRING and retry
```

The reaffirmation is an identity write, not a new claim and not a lease renewal. Its
`IF` clause matches `active_generation_id`, `active_epoch`, `active_state`,
`retire_claim_id`, `retire_claim_epoch`, and `retire_claim_kind`; its `SET` clause
writes those same values and the same `retire_claim_deadline`. A non-applied result
does not provide a visibility barrier, even if a following ordinary read happens to
return `RETIRING`.

`ALL` is deliberate. In Cassandra 5.0.9 Paxos v2, the LWT commit path reduces
`EACH_QUORUM.blockForWrite()` to a total response count and does not retain the
ordinary write path's per-DC response accounting. At RF 1 in each of the three
production DCs, `ALL` and `EACH_QUORUM` both require all three replicas. At higher
RF, only `ALL` makes the statement-level guarantee this proof needs independent of
the selected `{v1, v2}` target: every replica that can satisfy a later local writer
read has learned the fence. Ordinary `EACH_QUORUM` **reads** retain their per-DC
semantics and remain the correct shape for the use/reference drain.

No use/reference read, retirement evidence append, G2 activation, or physical delete
is allowed until the transition has a committed claim **and** the writer-visibility
fence has completed. The extra reaffirmation is ambiguity-only; the normal
`ACTIVE -> RETIRING` LWT itself commits at `ALL`, so it needs no second statement and
no writer-path Paxos is added.

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
5. Validate that G/epoch is still ACTIVE,
   the generation row satisfies the positive predicate
   (gc_state = null AND materialization_state = VERIFIED),
   AND this pin's own authority_deadline
   has not passed
6. Promote PENDING -> AUTHORIZED                   LOCAL_QUORUM
   preserving the original deadline and remaining TTL;
   if ambiguous, confirm the full authority tuple
7. Immediately before physical work, perform one fresh authoritative read of the
   pointer, the pinned generation row, and the pin row, and require the complete
   predicate: `active_generation_id=G`, `active_epoch=E`, pin identity/state
   `AUTHORIZED`, pointer state `ACTIVE` with null claim kind or
   `RETIRING/GC_RETIRE`, generation `gc_state=null AND materialization_state=VERIFIED`,
   and a deadline/margin that covers the operation. A `RETIRING/QUARANTINE`
   observation revokes this operation and permits no PUT, repair, reuse, or
   publication. Then reuse K or PUT/repair K only for the allowed pointer states.
8. Validate AUTHORIZED pin, deadline, and epoch
9. Publish the generation-bound reference         LOCAL_QUORUM
10. Confirm an ambiguous reference result
11. Remove pin(G, use_id)                         LOCAL_QUORUM
```

The pin becomes `AUTHORIZED` only after step 6. A writer that observes `RETIRING`,
`RETIRED` (including `RETIRED/QUARANTINE`), or a different generation on the pointer, or whose generation row fails
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
GC drains, finds only expired-authority uses -> RETIRING/GC_RETIRE -> ACTIVE
W finally revalidates: G/E1/ACTIVE, exactly what it first observed
```

The `RETIRING/GC_RETIRE -> ACTIVE` escape makes this reachable by design, and the epoch does
not distinguish it because `active_epoch` deliberately does not change across GC
cycles. Bumping the epoch on reactivation would close this case but break a
different guarantee — an already-`AUTHORIZED` writer is entitled to finish
publishing across `ACTIVE -> RETIRING`, and an epoch bump would reject it at the
publish helper. So the fix belongs where the defect is: authorization must check
the deadline.

Step 7 re-checks it immediately before the physical operation — step 6 is the
`PENDING -> AUTHORIZED` promotion itself. Without that
re-check a writer whose deadline expires between authorization and PUT can write
bytes to a key whose generation has since been retired and deleted, recreating an
untracked object at `K1` that no reference and no orphan row accounts for. Nothing
is lost, but a leak that the recovery scan cannot attribute is exactly the class of
state this design is trying to eliminate.

After authorization, the writer may finish while a normal `GC_RETIRE` pointer fence
changes from `ACTIVE` to `RETIRING`. The publish helper must therefore validate the
authorized pin's generation and epoch, deadline, and existence; it must not reject
solely because the current pointer state is now `RETIRING/GC_RETIRE`. It must reject
`RETIRING/QUARANTINE`, `RETIRED`, `DELETING`, `DELETED`, a different active
generation, or a missing/expired pin. The `retire_claim_kind` comparison is a state
allowlist (`ACTIVE/null` or `RETIRING/GC_RETIRE`), not equality with a kind stored on
the earlier pin.

No physical operation is permitted before pin authorization.

Reuse or repair of K is permitted only while its generation is `ACTIVE` or
`RETIRING/GC_RETIRE` and the writer holds an authorized pin. A generation that has reached
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
    retire_claim_kind = null,
    retire_claim_epoch = N1
WHERE org_id = O
AND block_id = L
IF active_generation_id = G1
AND active_state = RETIRED
AND active_epoch = E1
AND retire_claim_id = C1
AND retire_claim_epoch = N1
AND retire_claim_kind = GC_RETIRE
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
domain. If the retry remains ambiguous, it performs the normative `SELECT` at read
consistency `SERIAL`. **Only that serially settled pointer may classify the
outcome.** An ordinary read at `EACH_QUORUM` may inspect generation/evidence rows
afterwards, but it can never authorize orphaning, use release, or a conclusion that
the activation did not apply:

| Observed authoritative state | Action |
|---|---|
| `active_generation_id = G2` and `active_epoch = E2`, G2's predecessor tuple matches `(G1, E1, C1, N1)` with retirement evidence for `(G1, N1)`, G2 is usable (`gc_state = null AND materialization_state = VERIFIED`), and the materializer use still has valid authority and retention margin | The CAS applied; publish the reference and release the use |
| The same valid G2 lineage is selected, but the materializer authority deadline, materialization deadline, retention margin, or request context is no longer valid | Do not publish. Retain G2 and its exact use/key for recovery; release only through the expired-use recovery rule and re-enqueue the active zero-reference generation. An applied CAS does not restore expired authority |
| `active_generation_id = G2` and `active_epoch = E2`, but G2's predecessor tuple/evidence is absent or mismatched, or G2's `gc_state` is `QUARANTINED`, `DELETING`, or `DELETED` | Preserve G1/K1 and G2/K2; quarantine the unprovable generation state, never publish or delete on inference |
| The same G2 lineage is valid but G2's `materialization_state` still reads `MATERIALIZING` | Not a contradiction. Confirm the materializer's own `VERIFIED` write, or re-verify `K2`, and repair the marker; do not quarantine on marker lag alone. See `VERIFIED` Lag Is Not A Contradiction |
| A different generation is active and an exact `EACH_QUORUM` lineage/evidence walk from the settled pointer is complete | If the chain contains G2, G2 was a historical winner that was later retired and superseded: never orphan K2; hand it to its existing `RETIRED`/`DELETING`/`DELETED` lifecycle. Only if the complete chain does not contain G2 did this materializer lose; then make K2 an exact orphan and release the use |
| A different generation is active but the lineage/evidence walk is missing, mismatched, or uncertain | Preserve G2/K2 and the use; retry or quarantine the unprovable lineage. Never turn an incomplete chain into a losing-orphan decision |
| Still `G1` / `RETIRED/GC_RETIRE` / `E1` with matching claim tuple | Not applied; the same logical operation may be retried idempotently |
| Read uncertain or a DC is unavailable | Retain `G2`, `K2`, and the use; retry later. Never orphan and never delete on an uncertain read |

#### Recovering A Crashed Activation

The table above is the *request's* reconciliation, where the process that issued the
CAS is still alive. A recovery worker arriving later has a strictly harder problem,
and the naive branch is unsafe:

```text
G2 VERIFIED, K2 exists, materializer use AUTHORIZED
activation CAS proposal accepted; coordinator dies before the commit is learned
authority later expires

recovery: ordinary EACH_QUORUM read of blocks -> "G1 / RETIRED/GC_RETIRE"
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
3. settled pointer = G1/RETIRED/GC_RETIRE with no proposal outstanding
                              activation did not happen. If authority has
                              expired, do NOT activate; K2 becomes an exact
                              orphan
4. settled pointer = another G   walk the complete lineage from that pointer
                                 with exact EACH_QUORUM reads:
                                 G2 in chain -> historical winner; continue G2's
                                                normal retired/delete lifecycle
                                 complete chain excludes G2 -> true loser; exact orphan
                                 broken/uncertain chain -> retain; never infer loss
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
| selects a different generation, and the complete lineage/evidence chain excludes this generation | This materializer lost the first-writer race; `K` becomes an exact orphan |
| selects a different generation, and the complete lineage/evidence chain contains this generation | This generation won historically and was later superseded; never orphan `K`. Continue its normal retired/delete lifecycle |
| selects a different generation, but the lineage/evidence chain is incomplete or uncertain | Retain the generation and `K`; retry or quarantine the unprovable lineage, never infer that it lost |
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
// The deprecated SerialConsistency *type* aliases Consistency in v2.
// Query.SerialConsistency(...) is a different API and is NOT deprecated:
// it sets only the serial phase of a conditional statement and must not
// be used here.
session.Query(`SELECT ... FROM blocks WHERE org_id = ? AND block_id = ?`, o, b).
    Consistency(gocql.Serial)
```

Keeping the type and the method apart matters, because only one of them is
deprecated and the two are one keystroke apart:

| Symbol | Status in v2 | Role |
|---|---|---|
| `type SerialConsistency = Consistency` | deprecated alias | historical type, kept for source compatibility |
| `Query.SerialConsistency(cons)` | current API, not deprecated | serial phase of a **conditional** statement only |
| `Query.Consistency(cons)` | current API | the statement's consistency, and the only way to ask for a serial read |

The distinction matters because it is the opposite of the older `gocql/gocql` v1
driver, where `SerialConsistency` was a *separate type* and could only be attached
to a conditional statement's serial phase, so a serial `SELECT` was not expressible
through the normal API. Under v2, `Query.Consistency` accepts `Serial` without
validation and the level is sent as the statement's read consistency. Do **not**
write `.SerialConsistency(gocql.Serial)` on a plain `SELECT`: that setter is for the
conditional phase of an LWT and is ignored by a non-conditional statement — it is
exactly the silent no-op this section exists to prevent.

One implementation note that belongs with the serial-domain decision above:
`Query.SerialConsistency` **panics** when handed anything other than `SERIAL` or
`LOCAL_SERIAL`. A per-query serial level driven by configuration must therefore be
validated before it reaches the setter, not at the call site.

**The serial `SELECT` is the normative mechanism.** There is exactly one settlement
form in this design, and the startup gate is unconditional: every generation-fence
participant node must report the selected `generation_fence_paxos_variant` target.
For the pinned Cassandra 5.0.9 build, `{v1, v2}` is the semantic allowlist, but PR-0
selects exactly one target and PR-2's gate rejects any participant outside it — an
unrecognised variant, a disallowed variant, or a single node on the other allowlisted
variant fails closed rather than being assumed safe. Inspecting one coordinator is
not sufficient because a non-coordinator can still own a replica.
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

#### Clock Health Is An Eligibility Gate

`maximum clock skew` is a measured and continuously enforced deployment property,
not a guessed constant. Phase 0 defines one authoritative time service, records the
maximum permitted absolute offset and drift rate as
`generation_fence_max_clock_skew`, and measures every application and Cassandra
participant against it. The configured deadline/TTL margin must be greater than that
bound plus the query, retry, cancellation, and physical-operation margins above.

Generation-fence writer mode fails closed at startup when any participant lacks a
fresh clock-health attestation or exceeds the bound. Runtime eligibility rechecks the
same fact continuously; an out-of-bound or stale participant pauses new
generation-aware writes and destructive GC until clocks are healthy and the complete
participant set is reverified. Existing authorized work may only finish while its
remaining budget still covers the configured bound. Monitoring exposes offset,
attestation age, and gate state without node identity in unbounded metric labels.

The fault contract is asymmetric and tested on both sides of the bound: within the
bound, a use row outlives every legal publication; beyond the bound, no process may
classify authority expiry or begin physical work. Merely documenting "NTP required"
does not satisfy this gate.

The central publish helper must reject a writer when:

- the pin is absent or belongs to another operation;
- the generation ID does not match;
- the authority deadline has passed;
- the pointer says the pinned generation is `RETIRED`;
- the pointer says the pinned generation is `RETIRING` under
   `retire_claim_kind=QUARANTINE`;
- the pinned generation's own row fails the positive usability predicate
  `gc_state = null AND materialization_state = VERIFIED`, read from
  `block_generations` and not from `active_state` (see Which States Live Where).
  Stating it positively is deliberate: an enumeration of `DELETING`, `DELETED`, and
  `QUARANTINED` silently accepts a generation that is merely not yet `VERIFIED`, and
  a funnel that reaches publication only through this helper would then publish a
  reference on a regional `MATERIALIZING` lag. Quarantine remains fail-closed even
  if a stale pointer still names that generation; it is simply not the whole test;
- the active generation/epoch or claim kind no longer matches the authorized pin;
- the immediately-before-operation pointer tuple does not equal
   `active_generation_id=G` and `active_epoch=E`, or its state is not `ACTIVE` or
   `RETIRING/GC_RETIRE`; the `ACTIVE` form requires a null claim kind, while the
   `RETIRING` form requires the live pointer claim to be `GC_RETIRE` and does not
   compare that newly-created claim to the pin created under `ACTIVE`;
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
| `RETIRING/GC_RETIRE` | pointer | bounded poll/backoff; no G2 allocation; retryable failure with `Retry-After` when the budget ends. The wait is bounded by live-authority drain, not by any retention TTL — see Escaping `RETIRING` On Abandoned Uses |
| `RETIRING/QUARANTINE` | pointer | fail closed immediately; this observation **revokes** an already-authorized operation. No PUT, repair, reuse, or publication, no G2 allocation, and no bounded poll — the exit is the operator resolution workflow, not a drain the writer can wait out |
| `RETIRED/GC_RETIRE` | pointer | allocate a new UUID generation, materialize it, and complete the activation CAS inline; on CAS loss, orphan the losing key and reuse the winner |
| `RETIRED/QUARANTINE` | pointer | fail closed; allocate no G2, perform no physical operation, and require the explicit quarantine resolution workflow. Resolution recertifies the pointer to `RETIRED/GC_RETIRE`; it never returns G1 to `ACTIVE` |
| `MATERIALIZING` owned by another operation | generation row, reached through a pinned generation ID | bounded poll/backoff, then retryable failure. Not reachable from the pointer, so it is not a state a first materializer can poll to avoid racing |
| `QUARANTINED` | generation row, including the row selected by a pointer under investigation | fail closed; no new use, physical operation, candidate, or delete; require operator reconciliation |
| `DELETING` | generation row | fail/retry; never repair or reuse that generation |
| `DELETED` | generation row | re-probe the logical block and allocate a new generation if needed |
| Cassandra state uncertainty | either | fail closed with a bounded retry budget |
| A DC is unavailable | either | all destructive reads and the `ACTIVE -> RETIRING` `ALL` fence fail closed. First materialization, deduplication, and rematerialization availability is topology- and Paxos-target-dependent; no request may infer per-DC visibility from a Paxos v2 aggregate commit. See below |

The exact HTTP/status mapping is an implementation decision for PR-4, but every
funnel must expose the same retryable error contract.

#### A DC Outage Is Not Confined To Rematerialization

An earlier revision limited the DC-outage row to rematerialization and stated that
deduplication "is unaffected and stays regional". That is false against the audited
checkout, and it contradicts this document's own X4 analysis:

```text
retryUploadedBlockMaterialization:  store() -> materialize() -> store()
materialize() -> RegisterUploadedBlock  (internal/api/v2/fs_helpers.go:985)
                 -> UpsertBlockMetadataWithSHA1 -> INSERT ... IF NOT EXISTS
```

Within this metadata-materialization funnel, `materialize()` runs on **both** probe
outcomes, so a reusable-row deduplication result still pays the first-writer LWT.
Preflight that rejects a fully deduplicated block before this funnel pays none. With
`configs/config.prod.yaml:52` at
`serial_consistency: SERIAL` that LWT is global Paxos, and at RF 1 per DC across two
DCs a global Paxos quorum is both replicas. Losing one DC therefore fails every
invocation reaching that funnel — reusable-row result, first materialization, and
rematerialization alike — not only the rematerializing case.

Two things must be kept apart here, and the first draft ran them together. The
**existing baseline** is that every invocation reaching this materialization helper
reaches the LWT; preflight may bypass the helper entirely. That is unchanged by the
fence. Whether that LWT's Paxos domain is global or
local is a property of the **deployed serial-consistency profile**, and today the
multi-DC profiles are on `LOCAL_SERIAL`. Generation-fence writer mode is what makes
the global domain mandatory for `blocks` rather than configurable — so the outage
exposure described here is a consequence of enabling the fence on the current
topology, not a description of what those profiles do now.

The generalization stops there, though, and the reason is that `SERIAL` and
`EACH_QUORUM` do not have the same availability envelope. Conflating them produces a
statement that happens to be true for the test topology and false for a production
one:

| Operation | What it needs | Survives a whole-DC outage? |
|---|---|---|
| Rematerialization activation CAS | `SERIAL` phase plus an `EACH_QUORUM` regular commit | **Variant-dependent at RF > 1.** v1 uses the ordinary per-DC commit handler; Cassandra 5.0.9 v2 uses an aggregate Paxos commit count. At RF1, both still require every replica. The predecessor must be `RETIRED/GC_RETIRE`; `RETIRED/QUARANTINE` is not activatable |
| First-writer LWT (first materialization, and reusable-row results that reach metadata materialization) | `SERIAL` phase plus `QUORUM` regular commit | **Topology-dependent.** Both require a global majority, not one quorum per DC |
| `ACTIVE -> RETIRING` writer fence | `SERIAL` phase plus `ALL` regular commit | **Never.** This is intentionally stricter because the publication frontier requires writer visibility, not only consensus |
| Destructive drain and final refs check | `EACH_QUORUM` reads | **Never**, by construction — this is the X2 property |

Worked through:

```text
2 DCs x RF1   global replicas = 2, Paxos majority = 2
              losing one DC blocks the first-writer LWT too
               -> metadata-materializing dedup, first materialization and rematerialization fail

2 DCs x RF3   global replicas = 6, majority = 4, one DC holds 3
              losing one DC still blocks it

3 DCs x RF1   global replicas = 3, majority = 2
              losing one DC leaves 2 -> the first-writer LWT still commits,
              while activation and the retiring fence still require all 3

3 DCs x RF3   global replicas = 9, majority = 5
               Cassandra 5.0.9 Paxos v2 can satisfy its aggregate activation
              commit count from the 6 surviving replicas; v1's per-DC handler cannot
              -> no X2 proof may depend on activation being visible in the lost DC
              -> ACTIVE -> RETIRING still fails because it uses ALL
```

So the correct statements are split by mechanism:

- rematerialization at RF1 requires every replica; at higher RF its outage envelope
  depends on the selected Paxos implementation, and the acceptance harness records
  the actual target behavior rather than importing ordinary-write semantics;
- destructive reads and the `ACTIVE -> RETIRING` writer fence always require every
  participating DC, with the fence stricter still at `ALL`;
- the availability of the **first-writer LWT** — and therefore of first
  materialization and dedup that reaches metadata materialization — follows the global
  Paxos majority over the configured replica
  set. On the current two-DC RF 1 topology, **once generation-fence writer mode
  forces the `blocks` LWT to `SERIAL`**, a whole-DC outage blocks them; with three or
  more DCs it need not. It must not be written as "every DC" in general, and the
  qualifier about the profile is not optional either — those profiles run
  `LOCAL_SERIAL` today.

Two consequences belong in PR-0 rather than in an incident:

- the DC-outage error contract is measured against the **deployed** topology, not
  assumed from the two-DC test profile;
- the X4 probe fast path removes the LWT only from reusable-row outcomes that already
  reached metadata materialization. Browser/sync preflight can bypass that helper
  today, while first content and rematerialization still need coordination. No path
  that actually runs the generation-managed first-writer LWT is regional.

Only `RETIRING` requires a writer to wait, and two separate rules keep that wait
short. Neither is optional, because each closes a stall the other does not:

- A provisional reference must not create a long writer stall: the GC reactivates a
  `GC_RETIRE` generation when it sees any generation-bound reference, so a long
  provisional TTL costs retention rather than availability. A `QUARANTINE` claim
  remains fenced.
- An abandoned pin must not create a long writer stall either: the GC reactivates a
  `GC_RETIRE` claim when one or more uses remain and every such use has expired its
  authority, so the wait is bounded by the authority deadline rather than by
  `retention_expires_at`. A `QUARANTINE` claim remains fenced.

`RETIRED/GC_RETIRE` does not stall on a state transition: the writer proceeds
immediately to materialize and activate a new generation. `RETIRED/QUARANTINE` is
the deliberate exception and fails closed until operator resolution.

"Does not stall" is a statement about waiting, not about availability. A
rematerializing writer still depends on the activation CAS reaching global
agreement, so during a DC outage it fails fast with a retryable error instead of
waiting — the row above says exactly this, and A DC Outage Is Not Confined To
Rematerialization says that first materialization and deduplication fail the same
way for a different LWT. The two statements describe different things: `RETIRING`
makes a writer wait for someone else's work to finish; `RETIRED` never does, though
its own work can fail.

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
retire_claim_kind
```

All conditional active-pointer transitions occur through LWTs on that same row:

```text
ACTIVE -> RETIRING              GC worker
RETIRING/GC_RETIRE -> ACTIVE    GC worker
RETIRING -> RETIRED             GC worker
RETIRED/GC_RETIRE G1 -> ACTIVE G2       materializing request (inline) or recovery
RETIRING/QUARANTINE -> ACTIVE           operator resolution only
RETIRED/QUARANTINE -> RETIRED/GC_RETIRE operator resolution only

No transition returns a RETIRED pointer to ACTIVE on the same generation.
The only exit from RETIRED is a successor generation.
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

Generation-aware claims must not create stubs. Every pointer mutation uses positive
identity/state equality predicates that cannot apply to a missing row; no
generation-aware equivalent of `IF gc_state != 'deleting'` is permitted. The guarded
stub-repair path above remains only defensive handling for a partial row observed at
cutover, not a normal creator/consumer pair. A stub produced after writer mode is
enabled is a protocol violation that fails closed and pages the operator.

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
`RETIRED/GC_RETIRE` (G1) to `ACTIVE` (G2). A `RETIRED/QUARANTINE` pointer cannot
rematerialize while it is fenced, and it never returns to `ACTIVE` on G1: the operator
workflow recertifies it forward to `RETIRED/GC_RETIRE` under a new claim epoch with
fresh evidence, after which the ordinary successor path applies. A resolution that
instead confirms the contradiction marks the work `REJECTED` and leaves the pointer
fenced permanently; that block accepts no further writes until a new audited
resolution. Two concurrent
first materializers of the same new content therefore both PUT and both attempt the
LWT, one loses and orphans its key.
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
that either reads `G1 / RETIRED/GC_RETIRE` or selects a successor reachable from G1 by an
unbroken lineage chain.

Which claim the evidence must match depends on where the pointer is, and conflating
the two cases is a live trap:

```text
pointer = G1 / RETIRED/GC_RETIRE
                            evidence(G1, N1) must match the LIVE pointer claim,
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
be re-asserted in the `IF` clause. A `RETIRED/QUARANTINE` pointer is not an ordinary
retirement proof: it is a fail-closed retained pointer that cannot authorize
`DELETING` or G2 activation until its explicit resolution workflow completes:

```text
RETIRED is terminal for the generation: no transition of any kind, automated or
administrative, returns a pointer from RETIRED to ACTIVE on the same generation
    -> once G1 reads RETIRED, no writer can ever acquire authority on G1 again
    -> the pointer can only move forward to a successor, and the first such
       successor's activation CAS required the exact (G1, E1, C1, N1) tuple that
       is this worker's own proof
    -> any later generation is reachable from that one by the same rule, so the
       proof survives arbitrarily many rematerializations
```

**This invariant is load-bearing for the `DELETING` CAS and must be stated over
`RETIRED`, not over `RETIRED/GC_RETIRE`.** The `DELETING` statement deliberately does
not re-read the pointer, so its safety rests entirely on the claim that authority can
never be reacquired on G1. Scoping the invariant to one claim kind leaves a two-hop
escape — `RETIRED/GC_RETIRE -> RETIRED/QUARANTINE -> ACTIVE` — that no single
transition violates and that reintroduces deletion of live bytes:

```text
D reads pointer G1/RETIRED/GC_RETIRE + evidence(G1, C1, N1); proof obtained
D stalls before its DELETING CAS
Q converts the pointer to RETIRED/QUARANTINE
operator resolves the quarantine as a false positive and returns G1 to ACTIVE
a writer pins G1, authorizes, uses K1, and publishes ref(G1)
D wakes: IF gc_state = null AND materialization_state = VERIFIED
         AND storage_key = K1   -- all three still hold
D sets DELETING and deletes K1 under a live reference
```

Adding a pointer re-read before the `DELETING` CAS does not close it: the two
statements are on different tables, so a reactivation can land between them. The
invariant itself is the mechanism, which is why quarantine resolution is defined to
preserve it. A `RETIRING/QUARANTINE` pointer never reached `RETIRED`, so no delete
authorization can exist for it and returning it to `ACTIVE` is safe; a
`RETIRED/QUARANTINE` pointer resolves forward to `RETIRED/GC_RETIRE` and never back to
`ACTIVE`. See Resolving A Quarantine Without Breaking Irrevocability.

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
If either lifecycle LWT is ambiguous, settlement must classify the exact generation
identity rather than treating a non-applied result as failure: matching `DELETING`
must retain the recorded generation/key and delete-claim ID/epoch; matching
`DELETED` may retire delete work only after exact-key absence is reverified; a
conflicting terminal state, missing identity, or uncertain read retains the work and
fails closed.

#### When The Physical Delete Keeps Failing

`gc_state` has no path out of `DELETING` other than `DELETED`. Quarantine conditions
on `gc_state = null` and therefore cannot be reached from `DELETING`, and that is
correct rather than an oversight: the authorization is irrevocable, the object may
already be partially removed, and there is no state that means "we changed our mind
about a delete that may have happened". But a delete can still fail forever —
credentials, a bucket policy, a decommissioned storage class — and the document
otherwise stops at "retry".

The rule for a permanently failing `DELETE K`:

```text
delete fails           -> bounded backoff, retry; the delete work row is NEVER
                          retired, and gc_state stays DELETING
budget exhausted       -> ADD a DLQ record, a metric, and an audit event
                       -> stamp escalated_at and push next_attempt_at forward
                       -> gc_state stays DELETING, the delete work row stays
                          durable, the generation stays out of every other path
after escalation       -> continue low-frequency, idempotent automatic retries
                          and alert the operator; operator remediation may make
                          a retry succeed sooner
verified exact absence -> the only state exit; end at DELETED after re-verification
```

`next_attempt_at` and `escalated_at` are not tidiness. Without them the work row is
immediately eligible again, so the scanner re-reads it every tick, re-fails, and
re-emits the DLQ record, the metric and the audit event — a hot loop that buries the
one signal it exists to raise. The DLQ identity must also be idempotent per
`(generation, escalation)` so a repeated escalation updates one record instead of
appending forever.

The escalation is **additive**, and the word is chosen against the usual meaning of
"move it to the DLQ". A DLQ record here is a signal to an operator, not a change of
custody: `gc_generation_deletes_by_day` still names the generation, because the
handoff rule of correction 100 has no exception for an item that is failing. An
implementation that dequeues the work into a DLQ has retired the only row naming an
authorized, incomplete delete — the exact seam that rule exists to close.

Retrying indefinitely is always safe here for the same reason recovery may retry the
delete after a crash: `DELETE K` is idempotent and `K` is never reused, so a late
success cannot touch a live object. What is **not** safe is any of the three
shortcuts an implementer reaches for when a queue will not drain — retiring the work
row, regressing `gc_state`, or quarantining the generation. Each one strands an
authorized delete with no owner, which is the ownerless-fence failure of correction
100 in its physical form.

Escalation does not suspend recovery. `next_attempt_at` schedules low-frequency
automatic retries while the additive DLQ record alerts the operator. Operator
remediation may change the outcome, but neither remediation nor the DLQ record is a
state transition: only a retry that confirms exact-key absence can advance
`DELETING -> DELETED`.

What is **not** known in this state is whether `K1` is still there. A `DELETE` that
returned a timeout or a transport error may well have reached the object store, so
"the object is retained" is exactly the kind of inference this document forbids
elsewhere:

```text
gc_state = DELETING, delete unresolved
    -> the physical presence of K1 is UNKNOWN until re-verified
    -> it is not "retained", and it is not "gone"
```

The remedy is unchanged, which is why the distinction is cheap to honour: keep
`DELETING`, keep the work row, retry the exact key, escalate additively. Retrying is
safe under either truth because `DELETE K` is idempotent and `K` is never reused, and
the terminal `DELETED` transition is earned by re-verifying absence rather than by
assuming the last attempt failed. What the operator is being told is not "an object is
leaking" but "an authorized delete has not been proven complete" — and the only thing
that can go wrong from here is nobody reading it.

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

Quarantine is a durable workflow, not an unconditional diagnostic write and not a
cross-table CAS. Every proven contradiction first publishes and confirms
`gc_generation_quarantines_by_day` with the exact generation/key, contradiction
identity/evidence digest, reason, timestamp, and operation ID. An unconfirmed work row
means no pointer or generation mutation.

If the pointer currently selects G as `ACTIVE`, the worker must first acquire the same
writer-visible pointer fence at `SERIAL + ALL`, but with
`retire_claim_kind=QUARANTINE`. Ambiguity uses the normal serial settlement plus exact
`SERIAL + ALL` pointer reaffirmation. The kind is immutable for the claim epoch,
takeover preserves it, normal GC retirement/evidence/reactivation branches require
`GC_RETIRE`, and a quarantine claim can never append retirement evidence or authorize
delete. Publish/revalidation treats `RETIRING/QUARANTINE` as revocation: unlike a
normal GC drain, an already-authorized writer may not start another physical operation
or publish after observing it.

After the fence is acknowledged, the quarantine worker reads the complete use
partition at `EACH_QUORUM` until no use remains. A physical or reference write already
issued before the fence cannot be revoked; its use stays durable until the exact
reference is confirmed or the operation is abandoned. The worker preserves any such
key/reference, performs no delete, and does not quarantine the generation until this
use drain reaches zero. The use-before-reference publication ordering therefore
closes the same late-writer seam without claiming cancellation of an external request.

If G is not the pointer-selected `ACTIVE` generation, the worker must first settle
the pointer at `SERIAL` and classify any pending activation or existing claim. If G
could still be selected by a legal activation, the worker must first acquire a
`RETIRING/QUARANTINE` fence on the currently selected `ACTIVE` generation (or
 coalesce with its existing quarantine claim). If the pointer is `RETIRED/GC_RETIRE`,
it must instead convert the retained pointer claim from `GC_RETIRE` to `QUARANTINE`
with an exact `SERIAL + ALL` conditional write that leaves `active_state=RETIRED`
and increments `retire_claim_epoch`; this blocks the activation CAS, whose
predecessor condition requires the old `retire_claim_kind=GC_RETIRE` and epoch. A
`RETIRED/QUARANTINE` pointer is already fenced and is coalesced by exact
operation/evidence identity. Drain the relevant pointer/materializer uses and
retain the quarantine work until the activation is serially settled. This broader
fence is intentional: a generation not currently selected has no row-local lock that
can guard a future pointer CAS. The activation path must re-confirm an unexpired
materializer use immediately before its CAS and cannot issue that CAS after the use
is terminally released. An existing `RETIRING/GC_RETIRE` claim is not overwritten by
quarantine until its claim owner is safely settled. The quarantine worker does not
write `QUARANTINED` until these checks and the pointer fence prove that no legal
activation remains. Durable quarantine work still precedes the generation-row
transition. In both cases, the terminal generation mutation is:

```text
UPDATE block_generations
SET gc_state = QUARANTINED,
    quarantined_at = now,
    quarantine_reason = R,
    quarantine_operation_id = T,
    quarantine_evidence_digest = D
WHERE org_id = O
  AND block_id = L
  AND generation_id = G
IF gc_state = null
AND storage_key = K
```

The statement uses `SERIAL` for the conditional phase and `ALL` for the regular
commit. Note what it does **not** write: `resolution_epoch` is absent from the `SET`
clause deliberately. That counter is initialized to `0` by the `MATERIALIZING` intent
that creates the row and is bumped only by an abort, so quarantine preserves whatever
value the previous cycle left. Initializing it here instead would be wrong twice over:
`RESOLVING` could capture `Rr = null` on the first cycle, leaving `Rr + 1` undefined
for the abort; and a second quarantine of the same generation after a resolved first
one would reset the counter, so a stale first-cycle clear naming `Rr = 0` would find
`0` again and apply — an ABA on the fence itself. See correction 175.

The pointer state is supplied by the worker's prior authoritative
read, but is not copied into the generation row. `storage_key` is the non-null
identity guard required above; quarantine cannot use `materialization_state` for
that role because a `MATERIALIZING` generation is one of the things worth
quarantining. The implementation must use fixed identity-specific statements and
never accept an arbitrary state value: the `MATERIALIZING` form additionally matches
its operation ID and `materialization_state`. There is no automated form for
`DELETING` or `DELETED`; `IF gc_state = null` is what prevents overwriting either
terminal state.

Quarantine needs no claim condition for a second, independent reason: it is
fail-closed in the safe direction once globally visible. A stale worker that
quarantines a healthy generation costs retention and an operator ticket, never data.
Two concurrent quarantiners converge, because the second simply does not apply. That
does not make commit visibility optional: a writer in a DC omitted by a Paxos v2
aggregate commit could otherwise continue to read `gc_state=null` and use the
contradicted generation.

An ambiguous quarantine result has separate decision and visibility gates. First,
`SELECT ... CONSISTENCY SERIAL` on the exact generation partition settles any pending
proposal. If it returns the exact `QUARANTINED/G/K/R/T/D` identity, the worker
conditionally reasserts those same values at `SERIAL + ALL`; it does not replace the
reason or timestamp. If it returns the expected `gc_state=null` identity, the worker
retries the original quarantine at `SERIAL + ALL`. `DELETING`/`DELETED` is preserved.
An uncertain serial read, unavailable replica, non-applied reaffirmation, or ambiguous
result retains the generation, physical key, pointer fence, and quarantine work row.
The scanner resumes the same operation after worker/coordinator death; no generation
mutation is attempted before an active pointer is durably fenced. No writer-facing
path may rely on quarantine until the `ALL` barrier is acknowledged. A quarantined
generation is then excluded from ordinary discovery, re-enqueue, and every delete
path; only its quarantine workflow remains until the pointer/generation state is
confirmed. The work row remains as a terminal-resolution tombstone until operator
resolution completes; it is never retired merely because `QUARANTINED` is visible.
It can leave quarantine only through the
explicit operator workflow above, with its own audited conditional transitions and
no delete authority.

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
- If quarantine wins after a materializer's pre-CAS read but before its activation
  CAS, the materializer's CAS is rejected by the settled quarantine workflow before
  pointer installation; if the CAS nevertheless selects G2 first, reconciliation
  immediately installs durable quarantine work/fencing for the selected generation,
  prevents publication, and does not leave G2 as an accepted active generation.
- A quarantine request never concludes from an ordinary `ACTIVE` read alone. If the
  settled pointer names another generation but its serial settlement leaves a
  possible `G` activation proposal, the request retains durable work and retries;
  it does not mutate G or retire the work row. If an existing pointer claim is
  `RETIRING/QUARANTINE`, the request coalesces by operation/evidence identity and
  waits for that claim's drain; if it is `RETIRING/GC_RETIRE`, the request does not
  overwrite the live claim and waits for explicit claim settlement.

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

The successful `RETIRING/GC_RETIRE -> RETIRED` pointer transition retains the live
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
    retire_claim_kind     = null
    retire_abort_id       = null
    retire_claim_epoch    = last allocated value        <-- retained, never cleared

ACTIVE -> RETIRING
    retire_claim_epoch    = previous + 1
    retire_claim_id       = C
    retire_claim_deadline = new deadline
    retire_claim_kind     = GC_RETIRE or QUARANTINE

RETIRING/GC_RETIRE -> ACTIVE          (reference found, or all uses expired)
    retire_claim_id       = null
    retire_claim_deadline = null
    retire_claim_kind     = null
    retire_claim_epoch    = retained

RETIRING/GC_RETIRE -> RETIRED
    retire_claim_id / epoch / deadline / kind all retained
    (this is the predecessor proof the activation CAS matches)

RETIRED/GC_RETIRE -> RETIRED/QUARANTINE
    IF active_generation_id = G1
    AND active_state       = RETIRED
    AND active_epoch       = E1
    AND retire_claim_id    = C1
    AND retire_claim_epoch = N1
    AND retire_claim_kind  = GC_RETIRE
    SET retire_claim_id       = Cq
        retire_claim_epoch    = N1 + 1
        retire_claim_deadline = Dq
        retire_claim_kind     = QUARANTINE

RETIRING/QUARANTINE -> generation QUARANTINED
    pointer claim id / epoch / deadline / kind retained
    quarantine operation/evidence identity retained
    no automated pointer transition; operator workflow owns resolution

RETIRING/QUARANTINE -> ACTIVE      (operator FALSE_POSITIVE resolution only)
    work RESOLVING confirmed first; decision = FALSE_POSITIVE
    generation gc_state QUARANTINED -> null confirmed next
    delayed candidate + discovery projection confirmed BEFORE this CAS
    IF active_generation_id = G1
    AND active_state       = RETIRING
    AND active_epoch       = E1
    AND retire_claim_kind  = QUARANTINE
    AND retire_claim_id / epoch match the retained quarantine claim
    SET retire_claim_id       = null
        retire_claim_deadline = null
        retire_claim_kind     = null
        retire_abort_id       = null
        retire_claim_epoch    = retained quarantine epoch
        active_state          = ACTIVE
    -- An abort that linearized first installs QUARANTINE_ABORT; this CAS then
    -- fails closed. There is no RETIRING/QUARANTINE_ABORT -> ACTIVE path.

RETIRING/QUARANTINE -> RETIRING/QUARANTINE_ABORT   (abort authority on blocks)
    IF ... claim matches (Cq, Nq, QUARANTINE)
    SET retire_claim_id/epoch/deadline/kind = Cf/Nf/Df/QUARANTINE_ABORT
        retire_abort_id = A
    -- first authoritative abort act; see Aborting a RESOLVING Resolution

RETIRED/QUARANTINE -> RETIRED/QUARANTINE_ABORT     (abort authority on blocks)
    same shape; active_state remains RETIRED

RETIRED/QUARANTINE_ABORT -> RETIRED/QUARANTINE_ABORT
    (successor-cancel abort fence only; work_kind=SUCCESSOR_AFTER_DELETE)
    IF ... claim matches (C0, N0, QUARANTINE_ABORT)
    AND retire_abort_id = A0          -- the abort that left the live fence
    SET retire_claim_id/epoch/deadline/kind = Cf/Nf/Df/QUARANTINE_ABORT
        retire_abort_id = A1          -- this cancel's logical abort
    -- ordinary deadline takeover of QUARANTINE_ABORT MUST NOT take this path:
    -- it preserves retire_abort_id exactly and only bumps claim id/epoch/deadline

ordinary claim takeover (RETIRING or RETIRED)
    GC_RETIRE:         preserve kind
    QUARANTINE:        preserve kind
    QUARANTINE_ABORT:  preserve kind AND retire_abort_id EXACTLY
    -- never a vehicle for A0 -> A1; that is only the abort fence above

RETIRED/QUARANTINE -> RETIRED/GC_RETIRE   (operator FALSE_POSITIVE resolution only)
    work RESOLVING confirmed first; it fixes the PROSPECTIVE claim (Cr, Nr = Nq + 1)
    generation gc_state QUARANTINED -> null confirmed next
    global zero check (uses then refs, EACH_QUORUM) while the pointer still reads
        G1 / RETIRED / Cq / Nq / QUARANTINE -- the OLD fence protects this check;
        Cr/Nr are not installed yet and protect nothing
    prospective evidence(G1, Nr) with claim_id = Cr appended + confirmed BEFORE this CAS
    IF active_generation_id = G1
    AND active_state       = RETIRED
    AND active_epoch       = E1
    AND retire_claim_kind  = QUARANTINE
    AND retire_claim_id    = Cq
    AND retire_claim_epoch = Nq
    SET retire_claim_id       = Cr
        retire_claim_deadline = Dr
        retire_claim_kind     = GC_RETIRE
        retire_abort_id       = null
        retire_claim_epoch    = Nr
    -- this CAS is what makes evidence(G1, Nr, Cr) authoritative
    -- G1 stays RETIRED. There is no path from RETIRED back to ACTIVE.
    -- An abort that linearized first makes this CAS fail; use the
    -- ALLOW_SUCCESSOR_AFTER_DELETE path below only after DELETE_ALREADY_ADVANCED.

RETIRED/QUARANTINE_ABORT -> RETIRED/GC_RETIRE
    (new work identity; work_kind=SUCCESSOR_AFTER_DELETE only)
    work RESOLVING confirmed first under a fresh OPEN row with that work_kind;
    never reopen REJECTED; decision = ALLOW_SUCCESSOR_AFTER_DELETE at RESOLVING
    fresh EACH_QUORUM zero check under the live QUARANTINE_ABORT fence
    prospective evidence(G1, Nr, Cr) + handoff confirmed BEFORE this CAS
    IF active_generation_id = G1
    AND active_state       = RETIRED
    AND retire_claim_kind  = QUARANTINE_ABORT
    AND retire_claim_id / epoch match the abort claim
    AND retire_abort_id    = A
    SET retire_claim_id/epoch/deadline/kind = Cr/Nr/Dr/GC_RETIRE
        retire_abort_id = null
    -- does not touch G1's DELETING/DELETED lifecycle

G1 RETIRED -> G2 ACTIVE     (activation CAS)
    G2.predecessor_* records C1 / N1 durably first
    retire_claim_id       = null
    retire_claim_deadline = null
    retire_claim_kind     = null
    retire_claim_epoch    = N1 retained as the counter
```

The single property that makes this safe is that `retire_claim_epoch` is a
**monotonic counter that is never cleared**, while `retire_claim_id` and
`retire_claim_deadline`/`retire_claim_kind`/`retire_abort_id` are live only during
`RETIRING` and `RETIRED`. Clearing the
epoch on reactivation or on activation would let a stale worker from cycle N match a
future cycle — the hole correction 53 exists to close — and clearing the id or
deadline while `RETIRED` would destroy the predecessor condition. `retire_abort_id`
is null except while `retire_claim_kind=QUARANTINE_ABORT`.

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
4. CAS blocks RETIRING -> RETIRED/GC_RETIRE        SERIAL + EACH_QUORUM
    <-- G2 may activate from this point on, unless a quarantine claim is installed
   <-- the candidate/queue work may now retire
5. publish + confirm gc_generation_deletes_by_day LOCAL_QUORUM
   <-- the handoff work may now retire
```

Steps 3 and 5 are the work-continuity handoff, not bookkeeping. Without step 3 the
seam after the pointer CAS has no owner: `blocks` reads `G1 / RETIRED/GC_RETIRE` with valid
evidence and `K1` present, while the candidate that discovered G1 is gone and the
delete work does not exist yet. See Discoverability Before Irreversibility.

**Step 4 is the gate.** The G2 activation CAS conditions on the
`blocks` row alone, so a rematerializing request can win it once the pointer reads
`G1 / RETIRED/GC_RETIRE / E1` with the matching claim tuple. Nothing can hold G2 back until a
later write to a different table lands, and an implementer who tries to enforce
that will reach for a cross-table condition Cassandra cannot express — which this
ADR forbids elsewhere as an acceptance criterion.

There is no mutable generation-row mirror finalize. The durable proof of retirement is:

> an evidence row for G1 matching the **retirement-authorizing claim's epoch and
> ID**, **plus** the
> authoritative pointer having left `RETIRING` — either because it reads
> `G1 / RETIRED/GC_RETIRE`, or because it has already been replaced by a successor whose
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
    if the pointer reads G / RETIRED/GC_RETIRE -> the claim on the blocks row
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
| Pointer selects G1, state `RETIRED/GC_RETIRE` | present | Consistent; conditionally set G1 `gc_state=DELETING`, then proceed with exact-key cleanup |
| Pointer selects G1, state `RETIRED/QUARANTINE` | any | Quarantine fence remains authoritative; no `DELETING`, no G2, and no physical delete until explicit operator resolution |
| Pointer selects G1, state `RETIRED/GC_RETIRE` | absent at the authorizing claim | **Protocol violation. Fail closed.** Quarantine G1; no `DELETING`, no G2, no S3 delete, and above all **no reconstruction of the missing evidence**. See Evidence Cannot Be Reconstructed After The Fact |
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

Operator recertification is the one place where the second sentence does not hold: its
evidence is appended for a *prospective* claim, before the CAS that installs it. The
first sentence is unaffected — a recertified generation reached `RETIRED` through the
ordinary path, so it did hold the pointer. The lineage walk is unaffected for a
different reason: a prospective row is discoverable through its handoff projection and
its `RESOLVING` work row, but it becomes retirement authority only once the pointer or
a successor lineage proves that exact claim was installed. See Recertification Evidence
Is Prospective.

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

pointer = RETIRED/GC_RETIRE, evidence absent -> the ordering was violated
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
> pointer still reads `G1 / RETIRED/GC_RETIRE`, otherwise the claim recorded in the lineage
> link that supersedes it — and the
> authoritative pointer must either read `G1 / RETIRED/GC_RETIRE`, or select a later
> generation reachable from G1 by an unbroken lineage chain — the immediate
> successor G2 being only its shortest case. See The Lineage Chain Is Transitive;
> an implementation that hard-codes "G1 or G2" strands every generation collected
> after two rematerializations.

> Retirement evidence is valid only when **both** `retire_claim_epoch` and
> `retire_claim_id` match the retirement-authorizing claim for that generation — the
> claim on the `blocks` row when the pointer still reads `G / RETIRED/GC_RETIRE`, otherwise the
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
| Active pointer selects another G | After serial settlement, walk the exact lineage/evidence chain from the selected generation. If it contains G, G was a historical winner and continues its normal retired/delete lifecycle. Only a complete chain that excludes G proves a losing activation; a broken or uncertain chain retains K and never authorizes orphan cleanup |
| No active pointer and intent expired | **Settle the pointer partition serially first** — "no row" and "a released stub" are both compatible with an accepted-but-unlearned first-writer proposal. Only then clean exact K, after fail-closed confirmation including use absence. See First Materialization Has The Same Hazard |
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

The commit itself has an interior window that must not be left to inference: the
`fs_object` row is persisted **before** its permanent `fs:` references exist
(`internal/api/v2/fs_helpers.go:1022-1031`), so for that interval an fs_object is
visible while nothing permanent holds its blocks alive. The current code already
names the answer — liveness is carried by the provisional publish-attempt `pub:`
references staged before the promote step — and the generation-aware design must keep
that ordering explicit:

```text
stage + CONFIRM pub: references (generation-bound)
    ->  persist fs_object
    ->  write + CONFIRM fs: references (generation-bound)
    ->  release pub:
```

`pub:` rows are therefore not a convenience: they are the liveness holder for the
publish race, which is why Reference Identity requires generation identity to flow
through them and why the expiry projection must cover them as well as `up:`. No
liveness holder is released after an ambiguous publication write: an uncertain
`pub:` stage preserves the publish attempt for retry, and an uncertain `fs:` promotion
preserves `pub:` until the exact generation-bound reference is confirmed.

The provisional reference TTL is therefore a correctness parameter of the same kind
as the authority deadline, not a cleanup convenience:

```text
provisional_reference_ttl
    > enforced upload-session + upload-to-commit + retry/resume bound
    + maximum clock/operation skew
    + operational safety margin
```

Phase 0 must define and fault-test this hard window explicitly, separately from the
single-operation writer bound that sets the authority deadline. Measurements validate
normal SLO/headroom but do not establish the bound. A
provisional TTL sized only for a single request will produce commit failures that
look like GC bugs.

Sizing it upward is cheap here precisely because a provisional reference no longer
parks a `GC_RETIRE` generation: the GC reactivates on any generation-bound reference
under that claim kind, so a long provisional TTL costs retention, not availability.
A `QUARANTINE` claim remains fenced regardless of references.

## GC Protocol

```text
1. Discover candidate                              LOCAL_QUORUM
2. ACTIVE G1 -> RETIRING / kind=GC_RETIRE          SERIAL + ALL
3. Read all use rows for G1                       EACH_QUORUM
4. Read all refs bound to G1                      EACH_QUORUM
5. Decide, in this order:
     any read error / DC unavailable (GC_RETIRE only)
         -> publish + confirm a delayed candidate
            and its discovery projection            LOCAL_QUORUM
          -> attempt RETIRING/GC_RETIRE -> ACTIVE    SERIAL + EACH_QUORUM
         -> if the global CAS is unavailable/ambiguous, keep RETIRING
            only until it can be reconciled; never append evidence or delete
      refs > 0 (GC_RETIRE only)
          -> RETIRING/GC_RETIRE -> ACTIVE            SERIAL + EACH_QUORUM
      refs == 0 and any use (PENDING or AUTHORIZED) has an
      unexpired authority_deadline
          -> keep RETIRING, drain
       refs == 0 and uses > 0 and every remaining use has expired authority
       (GC_RETIRE only)
          -> publish + confirm the delayed candidate
             and its discovery projection            LOCAL_QUORUM
           -> RETIRING/GC_RETIRE -> ACTIVE            SERIAL + EACH_QUORUM
       refs == 0 and uses == 0 and retire_claim_kind = GC_RETIRE
           -> append + confirm retirement evidence   SERIAL + EACH_QUORUM
           -> publish + confirm handoff work         LOCAL_QUORUM
           -> G1 -> RETIRED                          SERIAL + EACH_QUORUM
           -> the candidate/queue work may now retire
       refs == 0 and uses == 0 and retire_claim_kind = QUARANTINE
           -> retain the quarantine work and pointer fence; no evidence, RETIRED,
              delete, or automatic pointer exit
6. Allow G2 only after G1 is RETIRED/GC_RETIRE
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

The error branch for a `GC_RETIRE` claim is a fail-closed **writer-fence escape**,
not delete progress.
Unknown liveness can never produce retirement evidence, but retaining `K1` and
returning the pointer to `ACTIVE` is safe under every possible read result. The
delayed candidate is confirmed before the CAS exactly as in the abandoned-use branch.
If a whole DC is unavailable, the reactivation CAS may also be unavailable; the
pointer then remains `RETIRING` only until global coordination returns, not forever
because the same tombstone-heavy liveness read is retried as the only exit.

At RF3 under Paxos v2, requested `EACH_QUORUM` may instead acknowledge
`RETIRING -> ACTIVE` from six aggregate responses while one DC learns nothing. That
is safe and intentionally not promoted to `ALL`: the omitted DC can only remain stale
at restrictive `RETIRING`, so it rejects writer authorization until hinted delivery,
repair, or a later pointer LWT converges the row. Each request fails within its bounded
retry budget, but the regional stale interval itself has no protocol-intrinsic bound.
Phase 0 sets a convergence/repair SLO and operational trigger. Acceptance tests cover
reference-found, expired-use, and read-error reactivation through both natural
convergence and forced repair.

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

The proof composes five rules already required elsewhere in this document, and each
is load-bearing:

1. Only an operation holding an `AUTHORIZED` use may publish a generation-bound
   reference. Pin authorization requires revalidating the active pointer while it
   still selects G1 in `ACTIVE`; a materializer's use is `AUTHORIZED` from before
   its PUT.
2. A writer publishes its reference **before** removing its use, and the publish
   helper may remove a use only after confirming the reference.
3. An ambiguous reference result leaves the use in place. A writer never releases a
   use on an unconfirmed publish.
4. Before the use read, `ACTIVE -> RETIRING` has either returned an acknowledged
   `SERIAL + ALL` result or an ambiguous transition has completed the exact
   `SERIAL + ALL` visibility reaffirmation. A serial settlement alone is not this
   fence: it may omit the DC of a later local writer read.
5. A use row inserted after the frontier reads can never reach `AUTHORIZED`,
   because its own revalidation is ordered after its insert and will observe
   `RETIRING` or later.

Rules 1-3 mean every operation capable of publishing was visible at the use read;
rules 4-5 mean no new such operation can be created afterwards. Together they are why
the sequence "global zero check -> persist retirement evidence -> pointer CAS"
needs **no** second global reference check between its steps. Without this stated
as an invariant, a PR-6 implementer will either insert a redundant global check
between the persist and the CAS, or — much worse — relax one of rules 2-5 while
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
| Uses > 0 and every remaining use has an expired `authority_deadline` but unexpired `retention_expires_at` | `RETIRING/GC_RETIRE -> ACTIVE`; a `QUARANTINE` claim remains fenced |

Reactivating on expired-authority uses is retention-safe by construction: `K1` was
never deleted, and the publish helper independently rejects a writer whose authority
deadline has passed, so the abandoned owner cannot benefit from the reactivation. It
converts an availability stall into retention, which is the trade this ADR makes
everywhere else.

This does not reintroduce `pin -> ACTIVE`. A use with live authority still never
justifies reactivation; it justifies waiting. Only a use that can no longer produce
a reference — and therefore can no longer be drained into a decision — releases a
`GC_RETIRE` fence. A `QUARANTINE` fence never uses this escape.

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

### Resolving A Quarantine Without Breaking Irrevocability

Quarantine resolution returns a fenced pointer to service, so it has to satisfy two
rules that the rest of the protocol already relies on. The two quarantine pointer
states satisfy them differently, and collapsing them into one "return it to `ACTIVE`"
step is what reopens deletion of live data.

**A `RETIRED` generation never returns to `ACTIVE`.** The `DELETING` CAS does not
re-read the pointer; its entire safety argument is that authority on G1 can never be
reacquired once G1 reads `RETIRED`. A resolution that reactivates the same generation
falsifies that, and a stale delete worker holding a valid earlier proof will then
delete bytes under a live reference — see Authorizing `DELETING`. So the two states
resolve to different targets:

```text
RETIRING/QUARANTINE   never reached RETIRED
                      no delete authorization can exist for it
                      -> resolves to ACTIVE on the same generation

RETIRED/QUARANTINE    a delete authorization may already be held
                      -> resolves forward to RETIRED/GC_RETIRE
                      -> never to ACTIVE
```

A `RETIRED/QUARANTINE` resolution is therefore a *recertification*, not a revival. It
allocates the next `retire_claim_epoch`, re-runs the global zero check, and appends
fresh evidence before the pointer CAS. The re-check is cheap and provable: no writer
can acquire authority while the pointer is `RETIRED` or `RETIRING`, so the frontier
cannot have reopened, and the fresh row is a genuine attestation of a check that just
happened rather than the synthetic proof correction 109 forbids. Reusing the
pre-quarantine evidence instead would force the evidence-matching rule to accept a
superseded cycle, weakening a rule that exists to stop exactly that.

#### Recertification Evidence Is Prospective

This is the **single exception** in this design to the rule that evidence is appended
by the worker that already holds the claim it names, and it has to be stated as one or
it will be "simplified" back into a defect.

Everywhere else, a claim is acquired first and its holder then earns the evidence:
`ACTIVE -> RETIRING` installs `(C, N)` and the append is ordered after it, which is
what makes "evidence for `(Gn, Nn)` can only exist if Gn held the pointer" true.
Recertification inverts that order, because the pointer transition it protects is the
very one that installs the claim:

```text
live claim while the check and the append run:   Cq / Nq / QUARANTINE
claim the evidence names:                        Cr / Nr   (not installed yet)
```

The safety argument is therefore about which fence protects the check, not about who
holds the claim:

> The zero check is protected by the **existing** `RETIRED/QUARANTINE` fence. `Cr/Nr`
> is a *prospective* claim that protects nothing while the check runs, and it is fixed
> by the `RESOLVING` record before any of this begins. The prospective evidence becomes
> authoritative if and only if the subsequent CAS atomically installs exactly that
> `(Cr, Nr)` as `RETIRED/GC_RETIRE`.

**Discoverable is not authoritative.** Prospective evidence is *not* unreachable, and
saying so would be wrong in a way that matters: the recertification publishes its
handoff projection before the CAS, and the `RESOLVING` work row carries
`prospective_claim_epoch` too, so a scanner has two ways to find
`evidence(G1, Nr, Cr)` while the pointer still reads `G1 / RETIRED / Cq / Nq /
QUARANTINE`. That discoverability is required rather than incidental — it is what
recovers a crash between the append and the CAS.

What the row lacks is authority, and the two properties must be kept apart:

```text
discovered before the CAS
    -> may ONLY settle or retry the exact (Cq, Nq) -> (Cr, Nr) recertification
    -> may NOT authorize DELETING, publish delete work, or permit G2 activation

CAS proven applied (live pointer, or transitive successor lineage naming Cr/Nr)
    -> evidence(G1, Nr, Cr) is the retirement-authorizing evidence

CAS proven never applied and no longer applicable
    -> stale historical/recovery state; authorizes nothing, ever
```

No delete or activation path may treat `(Cr, Nr)` as retirement authority until the
live pointer or its transitive successor lineage proves that exact prospective claim
was installed. This is the standing rule that evidence alone never authorizes
deletion, applied to a row whose claim may not exist yet — the authoritative pointer
must also have installed it.

**The order cannot be inverted.** Doing the CAS first and the check and append
afterwards looks tidier and is wrong: from the instant the pointer reads
`RETIRED/GC_RETIRE` with `(Cr, Nr)`, a materializer may attempt G2 activation and a
delete worker may seek authorization, and both look for `evidence(G1, Nr)`. Finding it
absent, the delete path takes the `RETIRED` pointer with no authorizing evidence
branch — a protocol violation that quarantines G1 permanently, with the synthetic
reconstruction correction 109 forbids as the only other way out. Evidence before the
pointer transition that enables activation is the same ordering the normal retirement
path uses; recertification changes who holds the claim at that moment, not the order.

**`(Cr, Nr)` must be deterministic across retries.** The append is
`INSERT ... IF NOT EXISTS` keyed by `(org, block, generation, retire_claim_epoch)`, and
a row with the same key but a different immutable payload is a protocol violation. If a
retry after an ambiguous append allocated a fresh `Cr'`, it would collide with its own
first attempt at `Nr` and fail closed permanently. The `RESOLVING` record therefore
fixes `(Cr, Nr)` before the first append, and every retry of that resolution reuses
them — the same idempotence rule the activation CAS already has for `(G2, K2, E2)`.

A recertification does **not** invalidate a delete authorization obtained under an
earlier claim. A worker holding a proof from `(C1, N1)` may still complete its
`DELETING` CAS afterwards, because G1 never returned to `ACTIVE` and its bytes are
still dead. Requiring such a worker to re-match the live claim would reintroduce
exactly the revocability that correction 162 removed.

After recertification the block is in the ordinary retired state: `K1` may be deleted
under the new claim, and a future G2 activates against `(G1, E1, Cr, Nq + 1)`. What
never happens is G1 becoming usable again. If the operator wants the *content* back,
that is a rematerialization into a new generation, which is the normal path.

**A pointer returned to `ACTIVE` needs a delayed candidate.** This is correction 97
applied to the one resolution that does reach `ACTIVE`. A generation can be
quarantined after its references already reached zero, so the reactivated generation
may be `ACTIVE` with `refs == 0` and no pending reference-removal event to ever
re-create its candidate:

```text
operator CAS RETIRING/QUARANTINE -> ACTIVE
    => ACTIVE, refs possibly 0, no candidate, no work row naming the generation
    => invisible to every automated path, retained forever
```

The requirement is identical to the abandoned-use escape, and deliberately
unconditional rather than predicated on observing `refs == 0`:

```text
1. write + confirm the delayed candidate (candidate_at = now + drain_grace)
   and its (day, bucket) discovery projection row
2. CAS the pointer back to ACTIVE
```

An unconfirmed enqueue forbids the CAS. Making it unconditional costs one wasted
revalidation when references do exist — the scanner revalidates canonical state and
deletes a candidate that no longer applies — and buys the guarantee in the case that
matters.

Neither form adds a table or a new work identity: the `ACTIVE` form reuses
`gc_block_candidate` and its discovery projection, and the recertification form reuses
the retirement-evidence append and the handoff projection the normal cycle already
writes.

### Resolution Needs Its Own Durable State

Both resolution forms mutate `gc_state` before they mutate the pointer, and both take
several confirmed steps. A crash in the middle must be classifiable, and `OPEN` plus
the observed rows cannot classify it.

The ambiguity is exact rather than theoretical. An authorized false-positive
resolution that dies after clearing `gc_state` leaves:

```text
work            = OPEN
generation      = gc_state null
pointer         = .../QUARANTINE
```

which is byte-for-byte the state of an ordinary quarantine that has fenced the pointer
and not yet written `gc_state = QUARANTINED`. One demands "finish the authorized
resolution", the other demands "finish quarantining the generation", and they are
opposite actions. A scanner that infers authorization from `gc_state = null` would
promote an unexplained mutation into an administrative decision — the inference the
fail-closed rule exists to forbid.

So authorization is itself durable, and it is written first:

```text
OPEN -> RESOLVING            conditional; records resolution_id, decision, actor,
                             verification_digest, started_at, the prospective
                             (Cr, Nr) when applicable, and the generation's
                             observed resolution_epoch Rr.
                             Matches immutable work_kind:
                               QUARANTINE              -> decision = FALSE_POSITIVE
                               SUCCESSOR_AFTER_DELETE  -> decision =
                                                          ALLOW_SUCCESSOR_AFTER_DELETE
     -> RESOLVED             only after the pointer step is confirmed; also the
                             abort-lost-race terminal when abort authority never
                             linearized (from RESOLVING, never from ABORTING)

OPEN -> REJECTED             work_kind = QUARANTINE: contradiction confirmed;
                             quarantine retained
                             work_kind = SUCCESSOR_AFTER_DELETE: successor
                             authorization declined/cancelled before RESOLVING;
                             pointer remains QUARANTINE_ABORT; generation untouched

RESOLVING (abort intent)     conditional while still RESOLVING; first assignment
                             is single-assignment under IF pending_abort_id=null:
                             immutable logical A + actor/reason/started_at and
                             fence attempt F with full source authority
                             (claim, epoch, kind, source_abort_id) + (Cf,Nf,Df).
                             A later proven pre-linearization claim supersession
                             may revise only the fence attempt under the same A.
                             Grants NO authority. See corrections 187, 189, 190.

RESOLVING -> ABORTING        conditional; only after the blocks abort fence has
                             applied (or SERIAL settlement proves
                             QUARANTINE_ABORT / retire_abort_id = this work's A).
                             Copies abort_id/actor/reason/started_at from the
                             durable logical abort matching the pointer
          -> REJECTED        the abort completed; the only terminal exit from
                             ABORTING. POINTER_AND_GENERATION requires both
                             fences plus classified rollback.
                             POINTER_ONLY requires the pointer fence only;
                             abort_outcome = SUCCESSOR_CANCELLED
```

The transition to `RESOLVING` is confirmed **before** any `gc_state` or pointer
mutation. Abort **intent** is confirmed **before** the pointer fence but is not
authority. `ABORTING` is confirmed **only after** durable abort authority exists on
`blocks`:

```text
OPEN + work_kind=QUARANTINE
             -> no resolution was authorized: continue/complete the quarantine
OPEN + work_kind=SUCCESSOR_AFTER_DELETE
             -> await/drive the successor workflow; NEVER "complete quarantine"
                may terminate OPEN -> REJECTED if the human declines
RESOLVING    -> continue the recorded resolution or successor, UNLESS the pointer
                already reads QUARANTINE_ABORT with retire_abort_id equal to THIS
                work row's pending_abort_id: then adopt that intent into ABORTING
                and resume ONLY the abort. A SUCCESSOR_AFTER_DELETE row starts
                with the pointer already QUARANTINE_ABORT under the previous abort
                A0; that is the live successor fence, not this cancel. An abort
                intent alone (retire_abort_id still not equal to pending_abort_id)
                MUST NOT cause ABORTING, generation fence, or rollback
ABORTING     -> resume ONLY the abort for this work_kind's abort_scope:
                POINTER_AND_GENERATION: generation fence, restore, REJECTED
                POINTER_ONLY: no generation fence, no generation rollback,
                ABORTING -> REJECTED with SUCCESSOR_CANCELLED
                Never continue the resolution. Never ABORTING -> RESOLVED
RESOLVED     -> terminal; never reopen
REJECTED     -> terminal; the pointer stays fenced
```

`ABORTING` exists so a crash after the pointer abort fence — when work is still
`RESOLVING` — is not mistaken for an ordinary live resolution. Without it:

```text
work       = RESOLVING with decision = FALSE_POSITIVE
pointer    = QUARANTINE_ABORT under (Cf, Nf, A)
generation = gc_state QUARANTINED or null

    an abort that linearized on blocks and died before writing ABORTING, or an
    authorized resolution whose claim was taken over after retire_claim_deadline
```

A scanner that ignored `QUARANTINE_ABORT` would continue the resolution whose pointer
CAS the abort fence just revoked. Recording `ABORTING` *before* the pointer fence is
what correction 181 forbids: that write does not revoke the resolver. See also
correction 173, whose durability requirement survives with the linearization moved
onto `blocks`.

**A bare `RESOLVING -> REJECTED` is forbidden**, but not because `RESOLVING` implies
`gc_state` was already cleared — it does not. `RESOLVING` is confirmed *before* any
mutation, so it spans three distinct situations:

```text
RESOLVING, gc_state = QUARANTINED, pointer fenced    nothing has been undone
RESOLVING, gc_state = null,        pointer fenced    generation step done
RESOLVING, pointer step committed                    resolution effectively complete
```

That is precisely why the state exists, and it is why a bare rejection cannot define
rollback: the same work state means three different amounts of undo.

**Aborting therefore persists a non-authoritative abort intent on the work row,
settles `blocks`, installs durable abort authority on that same partition
(linearization + revocation), only then records `ABORTING` on the work row from
that intent, and then follows `abort_scope`:** `POINTER_AND_GENERATION` fences the
generation and rolls back; `POINTER_ONLY` does neither and terminals as
`REJECTED` with `abort_outcome=SUCCESSOR_CANCELLED`.

Reading the canonical state is *not* sufficient, and an earlier revision of this
section that began "aborting reads the canonical state" was wrong for a reason worth
stating: "the pointer is still fenced" is a statement about the past. Two things can
still move the pointer after that read, and neither is visible to it:

```text
live resolver     a resolver process that already observed RESOLVING is alive and
                  about to issue its pointer CAS. Its IF clause names only the
                  (G, E, active_state, Cq, Nq, QUARANTINE) tuple, none of which a
                  work-row ABORTING write changes -- so it applies after ABORTING
                  if the pointer claim has not yet been taken over

replayed proposal a dead resolver's pointer LWT was accepted by part of the Paxos
                  cohort and never learned. An ordinary read still returns the
                  fenced state; a later LWT on the partition replays it
```

Both land in the same end state, and it is precisely the one this workflow exists to
make unreachable:

```text
pointer    = ACTIVE (or RETIRED/GC_RETIRE after a recertification)
generation = gc_state null                  <-- clear already happened
work       = ABORTING or REJECTED           <-- abort "authorized" on the wrong table
```

A writer can then pin, authorize, and publish against a generation the operator has
just decided is still suspect — the fail-open window corrections 166 and 168 exist to
forbid — before step 6 re-quarantines. Writing `ABORTING` first does not close it,
because the resolution pointer CAS never reads the work row. See correction 181.

A resolution touches **two** partitions, so an abort needs **two** fences after its
authority is durable. Cassandra has no cross-table CAS — the constraint this document
works around everywhere else — and the pointer takeover invalidates only the
statements that name the pointer claim. The generation-row clear names none of them,
and correction 68 forbids it from doing so, which leaves this open if only the pointer
is fenced:

```text
resolver stalls just before its QUARANTINED -> null clear
abort takes over the pointer claim as QUARANTINE_ABORT (Cf, Nf, A)
abort sees gc_state still QUARANTINED -- "nothing to undo" -- and writes REJECTED
resolver wakes and executes QUARANTINED -> null

    the clear matches generation, storage_key and the original quarantine
    operation/evidence identity. The pointer takeover changed none of them

=> pointer fenced, gc_state = null, work terminal REJECTED
```

No writer can use the generation — the pointer is still fenced — but the durable record
of the contradiction is gone from the generation row, the work is terminal, and nothing
drives the state anywhere. It is the same wedge as correction 168, reached through the
partition that correction 168 did not fence. See correction 174.

The generation row therefore carries its own monotonic `resolution_epoch`, and the
clear is conditioned on it. This is **not** the unsatisfiable-`IF` defect of correction
68: that rule forbids conditioning on `retire_claim_*`, columns the pointer owns and
nothing writes to `block_generations`. `resolution_epoch` is written by this table's
own transitions, so the condition is satisfiable by construction.

**Abort scope is derived from immutable `work_kind`; it is not a stored column
(correction 190):**

```text
work_kind = QUARANTINE              -> abort_scope = POINTER_AND_GENERATION
work_kind = SUCCESSOR_AFTER_DELETE  -> abort_scope = POINTER_ONLY
```

Shared machinery: single-assignment logical `A`, revisable fence attempt `F` with
full source identity, SERIAL settlement, exact retry, pointer fence, then
`RESOLVING -> ABORTING`, then lost-race step 6. The scopes diverge after
`ABORTING`: only `POINTER_AND_GENERATION` fences `block_generations` and classifies
`gc_state` rollback. `POINTER_ONLY` must not, because a successor work identity `Q1`
never wrote the generation row (it still carries original `Q0`).

**The abort sequence is therefore:**

```text
0. SERIAL-settle the blocks partition: SELECT ... CONSISTENCY SERIAL.
   For a recertification, also walk the predecessor/evidence lineage, because a
   committed CAS may already have been superseded by G2 or G3 -- the same rule as
   A RESOLVING Recertification Is Settled By Lineage.
   Settlement classifies; it does not yet revoke.

   Exact fence-attempt payload, used by every SERIAL settle / exact retry below:

       (A, F, source_kind, source_claim_id, source_epoch, source_abort_id, Cf, Nf, Df)

   source_abort_id is null iff source_kind = QUARANTINE; required iff
   source_kind = QUARANTINE_ABORT (the previous retire_abort_id A0).

1. Persist abort INTENT on the work row while still RESOLVING (owner DC,
   SERIAL + LOCAL_QUORUM). This step has two shapes (correction 189).

   1a. First assignment of the LOGICAL abort — single-assignment:

       Allocate logical abort_id A, fence attempt F1, and (Cf, Nf, Df) for F1.
       Nf must be strictly greater than both the observed live claim epoch and any
       prospective epoch recorded by the RESOLVING row. Observed source is the
       full authority tuple (Cq, Nq, source_kind, source_abort_id). Issue:

           IF resolution_state   = RESOLVING
           AND resolution_id     = R
           AND pending_abort_id  = null
           SET pending_abort_id                 = A
               pending_abort_actor
               pending_abort_reason
               pending_abort_started_at
               pending_fence_attempt_id         = F1
               pending_fence_source_claim_id    = Cq
               pending_fence_source_epoch       = Nq
               pending_fence_source_kind        = source_kind
               pending_fence_source_abort_id    = source_abort_id
               pending_fence_claim_id           = Cf
               pending_fence_epoch              = Nf
               pending_fence_deadline           = Df

       applied      logical abort A and attempt F1 are durable. Go to step 2.
       ambiguous    SELECT the work partition at CONSISTENCY SERIAL.
                      pending_abort_id/actor/reason/started_at and
                      pending_fence_* == exact payload
                          -> intent applied; continue to step 2
                      pending_abort_id is null and work still exact RESOLVING/R
                          -> retry EXACT SAME payload
                      pending_abort_id is set to a different A'
                          -> A' is the canonical logical abort for this
                             resolution. This request creates no authority and
                             MUST NOT overwrite actor/reason/started_at.
                             API returns already-in-progress/conflict and MAY
                             report A'. Recovery always continues A'.
                      work is no longer RESOLVING
                          -> intent did not grant authority; classify the current
                             workflow; never infer abort authority
       not applied  classify with the same rules; never assume failure from timeout

       Once A is written it is IMMUTABLE for this resolution identity. No path may
       replace A with A2 on the same work row.

   1b. New fence ATTEMPT after a proven PRE-LINEARIZATION claim supersession
       (same logical A):

       Only when step 2 settlement proves F never linearized: live kind still
       equals source_kind, live retire_abort_id still equals source_abort_id
       (null for QUARANTINE), and the live claim is no longer the attempt's
       source — typically an ordinary retire_claim_deadline takeover. Then:

           IF resolution_state          = RESOLVING
           AND resolution_id            = R
           AND pending_abort_id         = A
           AND pending_fence_attempt_id = F
           SET pending_fence_attempt_id         = F'
               pending_fence_source_claim_id    = C2
               pending_fence_source_epoch       = N2
               pending_fence_source_kind        = source_kind
               pending_fence_source_abort_id    = live retire_abort_id
               pending_fence_claim_id           = Cf'
               pending_fence_epoch              = Nf'   -- Nf' > max(N2, prospective)
               pending_fence_deadline           = Df'
           -- A / actor / reason / started_at retained unchanged

       The superseded attempt F can never apply: its source epoch is gone forever.
       Ambiguity of 1b uses the same SERIAL settlement / exact-retry rules against
       the exact F' payload.

       Do NOT take 1b when live retire_abort_id already equals A: that means F
       linearized and a later ordinary takeover only changed claim owner.

   IMPORTANT: intent (1a or 1b) revokes NOTHING, fences NOTHING, and MUST NOT alone
   cause ABORTING, a generation fence, rollback, or a terminal work transition.
   Work remains RESOLVING. See corrections 187, 189, and 190.

2. Fence the POINTER partition — durable abort authority AND linearization point.
   Issue at SERIAL + ALL using the CURRENT fence attempt's exact payload:

       IF active_generation_id / active_epoch / active_state match the settled row
       AND retire_claim_id    = pending_fence_source_claim_id
       AND retire_claim_epoch = pending_fence_source_epoch
       AND retire_claim_kind  = pending_fence_source_kind
       AND (source_kind != QUARANTINE_ABORT
            OR retire_abort_id = pending_fence_source_abort_id)
       SET retire_claim_id       = Cf
           retire_claim_epoch    = Nf
           retire_claim_deadline = Df
           retire_claim_kind     = QUARANTINE_ABORT
           retire_abort_id       = A
       -- active_state preserved (RETIRING or RETIRED)

       applied      every CAS naming the attempt's source claim now fails.
                    Abort authority is durable. Go to step 3.
       not applied  classify the returned/settled row rather than assuming failure:
                      live kind = QUARANTINE_ABORT AND live retire_abort_id = A
                          -> this abort linearized. Ordinary takeover MAY have
                             advanced claim id/epoch afterwards; (Cf, Nf) need
                             not still match. Go to step 3.
                      pointer/successor step committed (directly, or proven by
                          the lineage walk) -> abort NEVER linearized; go to step 6
                      live kind still = source_kind
                      AND live retire_abort_id still = source_abort_id
                      AND live epoch advanced past this attempt's source
                          -> F never linearized; source superseded; go to step 1b.
                             DO NOT exact-retry the old Nf.
                      anything else -> remain RESOLVING with intent; fail closed;
                          no ABORTING, no rollback
       ambiguous    fence outcome UNRESOLVED. Remain RESOLVING with intent.
                    SERIAL-settle / exact-retry the same exact payload.
                    Do not say the pointer is fenced, do not write ABORTING,
                    do not fence the generation, do not roll back, do not
                    terminalize work.

   Reusing a prospective Nr as Nf would leave evidence(G1, Nr) carrying Cr while
   the pointer carries Cf at that same epoch -- correction 89. The commit level is
   ALL for uniformity with every other quarantine-kind pointer statement.

   Putting ABORTING on the work row before this LWT is wrong for a different
   reason than the generation-first order: ABORTING can be confirmed while the
   old resolver's pointer CAS still names (Cq, Nq, QUARANTINE) and still applies
   afterwards. Durable abort authority and pointer revocation must be the same
   blocks event. See correction 181. Intent before the LWT does not reopen that
   hole because intent is explicitly non-authoritative.

3. RESOLVING -> ABORTING, confirmed, copying provenance from the durable abort
   intent that matches the pointer (abort_id = A, actor, reason, started_at).
   Only after step 2 applied (or SERIAL settlement proved retire_abort_id = A).
   A crash between steps 2 and 3 leaves work=RESOLVING with pointer already
   QUARANTINE_ABORT/A and pending_abort_* still present: the scanner MUST load that
   intent, adopt it into ABORTING, and MUST NOT continue the abandoned resolution
   or successor. ABORTING exists so that crash is decidable; it is not itself the
   revocation. Adopt ABORTING only when retire_abort_id equals THIS work's
   pending_abort_id — a SUCCESSOR_AFTER_DELETE row starts with QUARANTINE_ABORT/A0.

3a. Branch on abort_scope (derived from work_kind; correction 190):

       POINTER_AND_GENERATION  -> go to step 4
       POINTER_ONLY            -> go to step 5b
                                 MUST NOT issue a generation-row LWT

4. Fence the GENERATION partition. POINTER_AND_GENERATION only.
   Conditionally bump resolution_epoch to Rr + 1 at SERIAL + ALL, matching the
   exact (G, storage_key, quarantine_operation_id, resolution_epoch = Rr) the
   RESOLVING row recorded. A stalled resolver's QUARANTINED -> null and its
   MATERIALIZING -> VERIFIED repair then fail, because both statements name
   resolution_epoch = Rr.

   This bump runs in EVERY POINTER_AND_GENERATION abort that still owns the
   generation after step 2, including "gc_state still QUARANTINED, nothing to
   undo" and the DELETING/DELETED branch -- skipping it there reopens the
   stalled-clear interleaving, and the epoch bump is not a gc_state rollback.

   The generation fence is itself a LWT and needs the same settlement contract
   the pointer fence already has. "Only this abort produces exactly Rr + 1 from
   the Rr frozen by its work row" is the load-bearing property; it must be
   written and tested, not inferred:

       applied      generation fence proven; go to step 5
       ambiguous    SELECT ... CONSISTENCY SERIAL the generation partition.
                    Do not advance on an ordinary read.
                      settled identity matches and resolution_epoch == Rr + 1
                          -> this abort's fence already applied; continue
                      settled identity matches and resolution_epoch == Rr
                          -> fence did not apply; retry the exact same
                             Rr -> Rr + 1 statement
                      settled resolution_epoch > Rr + 1, or identity mismatch
                          -> protocol conflict; remain ABORTING, fail closed
                      unable to settle
                          -> remain ABORTING; no rollback, no REJECTED
       not applied  classify from the returned/settled row with the same
                    rules as the ambiguous case above; never assume failure
                    from a timeout alone

5. Roll back and reject (POINTER_AND_GENERATION), now that the pointer cannot
   move under the abandoned resolution and every abandoned resolution mutation
   of block_generations names a revoked epoch. `gc_state` has four reachable
   values here, not two:

       gc_state still QUARANTINED -> ABORTING -> REJECTED directly; nothing to undo
       gc_state already null      -> conditionally restore gc_state = QUARANTINED at
                                     SERIAL + ALL, matching the same generation/key
                                     identity and retaining the bumped
                                     resolution_epoch, then ABORTING -> REJECTED
       gc_state DELETING or DELETED
                                  -> no rollback of gc_state, materialization,
                                     storage identity, or delete lifecycle metadata.
                                     The resolution_epoch fence from step 4 may
                                     already have advanced and is retained.
                                     See Aborting After The Delete Already Advanced

5b. POINTER_ONLY terminal (successor-cancel abort). After step 3:

       MUST NOT bump resolution_epoch
       MUST NOT restore or otherwise write gc_state
       MUST NOT match generation.quarantine_operation_id against this work's Q1
       generation row unchanged (Q0, resolution_epoch, gc_state, delete lifecycle)
       pointer remains RETIRED/QUARANTINE_ABORT under A
       ABORTING -> REJECTED with abort_outcome = SUCCESSOR_CANCELLED

6. Abort lost the pointer race: settlement proved the resolution or successor
   pointer step committed before step 2 could apply. Abort authority never became
   durable. Finish RESOLVING -> RESOLVED (not via ABORTING), then raise a NEW
   quarantine through the canonical path for the generation's CURRENT
   authoritative state. A leftover abort intent without retire_abort_id = A
   MUST be ignored as authority.
```

#### Aborting After The Delete Already Advanced

The `DELETING`/`DELETED` branch is not defensive padding. The protocol deliberately
creates it: a delete authorization obtained under a pre-quarantine claim survives both
quarantine and recertification, because `RETIRED` is terminal and the bytes are dead
regardless of how the quarantine resolves. So this ordering is legal end to end:

```text
D obtains its pointer/evidence proof for G1 / RETIRED/GC_RETIRE and stalls
Q converts the pointer to RETIRED/QUARANTINE; gc_state = QUARANTINED
   -> D's DELETING CAS now fails on IF gc_state = null
operator authorizes a false-positive resolution: RESOLVING, then
   QUARANTINED -> null
   -> D wakes and its CAS applies: gc_state = DELETING
operator changes their mind and requests an abort
```

Only the recertification form can reach it, since a `RETIRING/QUARANTINE` pointer never
held a delete authorization at all. Both remaining branches are wrong here, and one is
dangerous:

```text
restore gc_state = QUARANTINED   forbidden. It regresses an authorized, irrevocable
                                 delete -- one of the three shortcuts When The
                                 Physical Delete Keeps Failing exists to forbid, and
                                 the object may already be partly removed

"nothing to undo" -> REJECTED    wrong for a different reason: it reports a retained
                                 quarantine that does not exist, and leaves an
                                 operator believing the generation is fenced when its
                                 bytes are being deleted
```

So the abort terminates without a `gc_state` rollback, and says so:

```text
-> perform no rollback of gc_state, materialization_state, storage identity, or
   delete lifecycle metadata; DELETING and DELETED are terminal-directed and the
   delete work row keeps its own retry/escalation lifecycle untouched
-> the abort's monotonic resolution_epoch fence from step 4 may already have
   advanced and is retained; that bump is a fence, not a lifecycle rollback
-> ABORTING -> REJECTED, recording abort_outcome = DELETE_ALREADY_ADVANCED and
   abort_observed_gc_state = the settled DELETING or DELETED value
-> leave the pointer fenced at RETIRED/QUARANTINE_ABORT and raise a distinct alert
   and audit event
```

Leaving the pointer fenced is the fail-closed choice and it is deliberate: the operator
has just confirmed a contradiction they can no longer record as `QUARANTINED` on the
generation row, so whether this logical block may ever have a G2 is exactly the
judgement the quarantine workflow exists to route to a human.

#### Pointer-Only Successor After `DELETE_ALREADY_ADVANCED`

`REJECTED` is terminal and must never reopen. The later human path is therefore a
**new** work identity, not a mutation of the rejected row. That identity is
distinguished at creation by an immutable `work_kind`, because `decision` is only
set with `RESOLVING` and an undifferentiated `OPEN` would again mean two opposite
recoveries (correction 185):

```text
new OPEN row   -- created in the designated GC owner DC (routed there; never via
               -- the writer-DC discovery exception)
    quarantine_operation_id = fresh identity
    work_kind               = SUCCESSOR_AFTER_DELETE   -- immutable at creation
    -- NOT the same OPEN as work_kind=QUARANTINE ("complete the quarantine")
    decision                = null until RESOLVING
     -> RESOLVING
          decision = ALLOW_SUCCESSOR_AFTER_DELETE
          -- not FALSE_POSITIVE: the contradiction was confirmed; the human is
          -- authorizing a successor generation while G1's delete lifecycle continues
          fixes prospective (Cr, Nr) against the live QUARANTINE_ABORT claim
     -> fresh EACH_QUORUM zero check under the existing RETIRED/QUARANTINE_ABORT fence
     -> prospective evidence(G1, Nr, Cr) + handoff, confirmed before the pointer CAS
     -> RETIRED/QUARANTINE_ABORT -> RETIRED/GC_RETIRE
          IF retire_claim_kind = QUARANTINE_ABORT
          AND retire_claim_id / epoch match the abort claim
          AND retire_abort_id matches the authorizing abort
          SET retire_claim_id/epoch/deadline/kind = Cr/Nr/Dr/GC_RETIRE
              retire_abort_id = null
     -> RESOLVED

OR, if the human declines before RESOLVING:

     OPEN + SUCCESSOR_AFTER_DELETE -> REJECTED
          -- successor authorization cancelled; NOT "complete quarantine"
          -- pointer remains QUARANTINE_ABORT; generation untouched

OR, if the human cancels AFTER RESOLVING (work_kind=SUCCESSOR_AFTER_DELETE):

     -- OPEN -> REJECTED no longer applies; the row already authorized a pointer CAS
     -- that names the live QUARANTINE_ABORT claim. This is a successor-cancel abort
     -- (abort_scope=POINTER_ONLY). It shares intent/attempt/pointer-fence machinery
     -- with the false-positive abort but MUST NOT run the generation fence
     -- (corrections 189 and 190). Source kind is QUARANTINE_ABORT, not QUARANTINE.

     1. Persist abort INTENT on this SUCCESSOR RESOLVING row (single-assignment A,
        fence attempt F with full source (C0, N0, QUARANTINE_ABORT, A0) and target
        (Cf', Nf', Df') with Nf' > live epoch).
     2. Fence the POINTER at SERIAL + ALL using that attempt:
            IF ... AND retire_claim_kind = QUARANTINE_ABORT
            AND retire_claim_id/epoch match the attempt's source
            AND retire_abort_id = A0
            SET retire_claim_id/epoch/deadline/kind = Cf'/Nf'/Df'/QUARANTINE_ABORT
                retire_abort_id = A
        -- still RETIRED; successor CAS naming the old claim fails
     3. RESOLVING -> ABORTING (copy provenance) only when retire_abort_id = A.
        Then step 5b: NO generation fence, NO gc_state rollback.
        ABORTING -> REJECTED with abort_outcome = SUCCESSOR_CANCELLED.
        generation.quarantine_operation_id remains Q0; resolution_epoch unchanged.
     6. If settlement proves the successor pointer step already committed
        (RETIRED/GC_RETIRE under Cr): abort never linearized. Finish
        RESOLVING -> RESOLVED, then raise a NEW quarantine through the form matching
        the pointer's CURRENT state — never ABORTING -> RESOLVED.

     Classification after a later ordinary takeover of QUARANTINE_ABORT:
        live retire_abort_id = A     -> this cancel linearized; do not prepare F2
        live retire_abort_id = A0    -> F never linearized; prepare F' under A
     Exact retry uses the full attempt payload including source_kind and A0.
```

A scanner that wakes on `OPEN` + `SUCCESSOR_AFTER_DELETE` must not run the quarantine
completion path. No automated path may take the successor step on the operator's
behalf, and none may infer from `DELETING` that the contradiction was resolved.
Nothing here is unsafe: G1 reached `RETIRED`, no writer can reacquire authority on
it, and deleting `K1` was correct independently of how the quarantine ended. See
corrections 176, 179, 182, 185, 188, 189, and 190.

Step 6's path depends on where the pointer actually is, and naming only the `ACTIVE`
form — as an earlier revision did — leaves a recertified generation unfenced while
delete authorization and G2 activation stay enabled:

```text
resolved to ACTIVE (the pointer was RETIRING/QUARANTINE)
    -> ACTIVE -> RETIRING/QUARANTINE, the ordinary pointer-selected path

recertified, and G1 still holds the pointer as RETIRED/GC_RETIRE
    -> RETIRED/GC_RETIRE -> RETIRED/QUARANTINE, the retained-pointer conversion.
       G1 is RETIRED, not ACTIVE; the ordinary ACTIVE form cannot apply here

a successor already owns the pointer
    -> G1 can no longer be selected by any activation, so no pointer fence is
       required or possible for it. Use the generation-row quarantine form for a
       generation that is not pointer-selected; its IF gc_state = null also
       correctly refuses a G1 that already reached DELETING or DELETED.
       Never write a transition that presumes G1 is ACTIVE
```

An earlier revision of this section said instead that an operator who changes their
mind should "complete the authorized resolution and then raise a new quarantine". As a
general rule that is a fail-closed violation: for the first two situations it would
clear `gc_state` and return a generation whose contradiction has *just been confirmed*
to `ACTIVE`, opening a window in which a writer can pin it, authorize, and publish
before the second quarantine lands. It is correct only in the third situation, where
the pointer step has already committed and there is genuinely nothing to undo — which
is step 6 of the sequence above, not a fourth situation.

If new evidence appears mid-resolution and the abort cannot complete — an unavailable
DC, an unsettleable partition, a non-applied fence, an ambiguous restore — remain
fail-closed on the furthest durable abort state already proven:

```text
pointer fence unresolved
    -> remain RESOLVING; fence outcome unresolved; SERIAL-settle / retry
    -> no ABORTING, no generation fence, no rollback, no terminal work transition

pointer fence applied, abort_scope = POINTER_ONLY
    -> ABORTING -> REJECTED with abort_outcome = SUCCESSOR_CANCELLED
    -> generation row MUST remain untouched; no resolution_epoch LWT

pointer fence applied, abort_scope = POINTER_AND_GENERATION,
generation fence unresolved or restore incomplete
    -> remain ABORTING; pointer already QUARANTINE_ABORT
    -> SERIAL-settle / retry the exact unfinished operation
    -> no terminal REJECTED until both fences and the classified rollback complete
```

Fail-closed indefinitely is the correct outcome; advancing a resolution known to be
wrong is not, and neither is rolling one back without first proving nothing can
overtake the rollback, on either partition.

#### A `RESOLVING` Recertification Is Settled By Lineage, Not By The Live Pointer

The last crash point needs the transitive walk, and reading it naively inverts the
answer. Once the recertification CAS commits, the pointer reads
`G1 / RETIRED/GC_RETIRE / Cr / Nr` and a rematerializer may activate G2 **immediately**.
If the coordinator then dies before marking the work `RESOLVED`, recovery wakes to:

```text
work    = RESOLVING
pointer = G2 ACTIVE       (or G3, after another cycle)
```

"G1 is no longer on the pointer" must not be read as "the recertification failed".
That is the same mistake correction 140 closes for a crashed materializer, in the
vocabulary of an operator resolution:

```text
pointer = G1 / RETIRED/GC_RETIRE with exactly Cr / Nr
    -> the CAS committed; mark the work RESOLVED

pointer = a successor
    -> walk the predecessor/evidence lineage backwards
    -> a link naming exactly (G1, E1, Cr, Nr) proves the CAS committed
    -> mark the work RESOLVED

pointer = G1 / RETIRED/QUARANTINE with the old Cq / Nq
    -> the CAS did not commit; retry the same recertification with the same
       prospective (Cr, Nr)

lineage broken, or any read uncertain
    -> retain RESOLVING and retry; never infer failure, and never re-allocate a
       second prospective claim
```

The prospective evidence row is what makes the successor case decidable: it was
appended before the CAS, it is never deleted, and only a committed CAS can put `(Cr,
Nr)` into a lineage link. This is the same property that makes the ordinary lineage
walk sound, so the recertification adds no new proof obligation — only the requirement
that its recovery use the walk instead of the live pointer alone.

This is Discoverability Before Irreversibility applied to an administrative decision:
the record that a decision was made outlives the process that made it, and the scanner
never has to reconstruct intent from state it did not write.

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

Projection publication and discovery deliberately use asymmetric consistency. A
writer in any DC publishes its materialization or provisional-expiry projection at
`LOCAL_QUORUM`; the designated recovery scanner reads every partition in those
writer-originated projection families at `EACH_QUORUM`. The scan therefore intersects
the writer quorum in the originating DC without adding WAN latency to normal
materialization. A missing or unavailable DC fails the scan, holds the day cursor,
and retires no projection.

This rule applies to `gc_generation_intents_by_day` and
`gc_provisional_generation_refs_by_day`. The intent scanner may run in the designated
GC DC, but its input is still writer-originated because step 3c is published by a
materializer in any application DC. `gc_generation_zero_ref_by_day` is different: its
sole publisher is that designated scanner when it hands off an intent that won
activation but published no reference. It is therefore GC-owned, along with
candidate, retirement-handoff, and delete-work projections; those families are
created and consumed in the designated GC DC and remain `LOCAL_QUORUM`. Quarantine
work is the exception at discovery: any application/request path may discover it, so
the durable row is handed off to and confirmed in the designated GC DC before pointer
or generation mutation. **After that handoff is confirmed, the row is owned by exactly
one designated GC DC** (correction 184):

```text
After the quarantine handoff is confirmed — or, for work_kind=SUCCESSOR_AFTER_DELETE,
from the moment the row is created —
gc_generation_quarantines_by_day is owned by exactly one designated GC DC.

Every LOCAL_QUORUM work-state read and mutation MUST execute in that owner DC.

Operator/API requests arriving in any other DC MUST be routed to the owner;
they MUST NOT perform the LOCAL_QUORUM mutation in their caller DC.

If the owner GC DC is unavailable, the mutation fails closed / retries.
It MUST NOT fall back to caller-DC LOCAL_QUORUM, and no alternate DC may
auto-assume ownership. Changing the designated owner requires the topology /
drained-cutover maintenance procedure, not a request-time fallback.

SUCCESSOR_AFTER_DELETE rows are created in the owner DC under that routing rule;
they do not use the writer-DC discovery exception that ordinary QUARANTINE rows use
before handoff.
```

The scanner then owns the row locally under that contract. A projection family may
not be moved from one ownership class to the other without changing its read
consistency and acceptance tests.

The required projections and their branches are:

| Projection | Branch it recovers |
|---|---|
| expired materialization intents | two branches, not one: `materialization_state=MATERIALIZING` past its deadline, **and** a materializer use that expired with no published reference. Both are reached from the same row, because the intent owns the generation until a reference, zero-ref work, or orphan work takes over |
| zero-reference active generations | the pointer selects G, no reference and no live use exist, because a materializer died after activation. Its only publisher is the worker that retires the materialization intent without finding a reference. It does **not** cover the `RETIRING -> ACTIVE` escape — see below |
| retirement handoff | evidence exists for the authorizing claim while `blocks.active_state` is still `RETIRING` — the crash between the evidence append and the pointer CAS. A `RETIRED/GC_RETIRE` pointer with no such evidence is **not** a recovery branch: it is a protocol violation routed to quarantine |
| pending physical delete | `gc_state=DELETING` with no orphan row, or with an orphan row in `pending_s3`, or whose S3 delete completed but metadata cleanup did not |
| pending quarantine | a proven contradiction has durable work but its active-pointer quarantine fence, use drain, generation-row `SERIAL + ALL` transition, or exact visibility reaffirmation has not completed |

The table has **five** rows because the schema below defines five recovery tables. Two
further named projection families exist outside that table and must be counted with it:
`gc_provisional_generation_refs_by_day`, the inherited provisional/publish reference
expiry projection whose loss reopens F10, and
`block_generation_references_by_referrer`, the reverse-reference cleanup projection.
That is **seven** named projection families overall. The fifth recovery table is now
quarantine work, not the removed materializer-use projection. An earlier revision
listed "expired materializer uses" as a fifth projection; correction 109 removed it as
redundant because its branch is recovered through the intent row, and a later revision
said "six overall" by counting only the reverse-reference table alongside the five —
dropping the provisional family that correction 112 had just restored.

An earlier revision also listed the `RETIRING -> ACTIVE` escape under the
zero-reference projection, calling it a backstop behind the delayed candidate. It
was not one. That projection has exactly one publisher — the worker that retires the
materialization intent without finding a reference — and that worker never runs on
the escape path, so the "backstop" was an empty table. Naming a second owner that
does not exist is worse than naming none, because it invites an implementer to treat
the delayed candidate as best-effort.

The escape needs no backstop, and this is a property of the contract rather than an
accident: correction 97 orders the delayed candidate and its projection **before**
the reactivation CAS and makes an unconfirmed enqueue forbid the CAS entirely. A
committed reactivation therefore always has its discovery row, so the branch is
owned outright, once, by the candidate. This is correction 110 applied to a table
instead of to prose — where the ordering makes a crash state unreachable, nothing
should advertise a repair for it.

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
    gc_generation_quarantines_by_day    -- proven contradiction awaiting handoff,
                                        writer fence, use drain, or globally visible quarantine

base key, for the four whose subject is the generation itself:
    PRIMARY KEY ((due_day, bucket), due_at, org_id, block_id, generation_id)
    WITH CLUSTERING ORDER BY (due_at ASC, org_id ASC, block_id ASC,
                              generation_id ASC)

gc_generation_handoff_by_day adds retire_claim_epoch:
    PRIMARY KEY ((due_day, bucket), due_at, org_id, block_id, generation_id,
                 retire_claim_epoch)

gc_generation_deletes_by_day additionally carries the retry/escalation state that
When The Physical Delete Keeps Failing depends on:
    attempt_count
    next_attempt_at        -- re-eligibility time; without it the row is eligible
                              every tick and the scanner re-emits the alert forever
    escalated_at           -- null until the attempt budget is exhausted
    last_error             -- diagnostic only; never a state
    dlq_escalation_id      -- idempotent per (org, block, generation, escalation),
                              so a repeated escalation updates one record instead
                              of appending one per scanner tick

gc_generation_quarantines_by_day adds quarantine_operation_id and carries
resolution_state/resolution identity:
    PRIMARY KEY ((due_day, bucket), due_at, org_id, block_id, generation_id,
                 quarantine_operation_id)
    resolution_state       -- OPEN, RESOLVING, ABORTING, RESOLVED, or REJECTED;
                              never implicit
    work_kind              -- immutable at row creation: QUARANTINE or
                              SUCCESSOR_AFTER_DELETE. Distinguishes OPEN recovery
                              before decision exists (correction 185)
    resolution_id          -- set with RESOLVING; identifies one authorized attempt
    decision               -- set with RESOLVING only:
                              FALSE_POSITIVE when work_kind=QUARANTINE;
                              ALLOW_SUCCESSOR_AFTER_DELETE when
                              work_kind=SUCCESSOR_AFTER_DELETE.
                              REJECTED rows are terminal and never reopen;
                              SUCCESSOR_AFTER_DELETE always uses a fresh identity
                              after DELETE_ALREADY_ADVANCED
    resolution_actor       -- the authorizing operator
    verification_digest    -- the evidence the operator verified
    resolution_started_at
    observed_resolution_epoch -- Rr, the generation's resolution_epoch at RESOLVING;
                                 the clear names it and an abort bumps past it
    prospective_claim_id    -- Cr, fixed at RESOLVING; null unless the pointer is
    prospective_claim_epoch -- Nr, fixed at RESOLVING; RETIRED/QUARANTINE or
                                 RETIRED/QUARANTINE_ABORT
    pending_abort_id        -- LOGICAL abort identity; first assignment while still
    pending_abort_actor        RESOLVING, BEFORE the pointer fence, under
    pending_abort_reason       IF pending_abort_id = null. Immutable once set.
    pending_abort_started_at   Grants NO authority (corrections 187, 189, 190)
    pending_fence_attempt_id -- current pointer-fence attempt F under A; revisable
                               only after proven PRE-LINEARIZATION claim supersession
    pending_fence_source_claim_id
    pending_fence_source_epoch
    pending_fence_source_kind      -- QUARANTINE or QUARANTINE_ABORT
    pending_fence_source_abort_id  -- null iff source_kind=QUARANTINE;
                                   -- required iff source_kind=QUARANTINE_ABORT (A0)
    pending_fence_claim_id  -- Cf intended for the CURRENT fence attempt
    pending_fence_epoch     -- Nf; must exceed live and prospective epochs
    pending_fence_deadline  -- Df; exact retry includes the full attempt payload
    abort_id               -- set with ABORTING; must match blocks.retire_abort_id
                              and pending_abort_id
    abort_actor            -- copied from pending_abort_* at ABORTING
    abort_reason
    abort_started_at
    abort_outcome          -- set with REJECTED from ABORTING; e.g.
                              NOTHING_TO_UNDO, RESTORED_QUARANTINED,
                              DELETE_ALREADY_ADVANCED,
                              SUCCESSOR_CANCELLED
                              (lost-race finishes RESOLVING -> RESOLVED without
                              ABORTING when abort authority never linearized;
                              there is no ABORTING -> RESOLVED.
                              OPEN + SUCCESSOR_AFTER_DELETE -> REJECTED is a
                              separate terminal path and does not use abort_outcome;
                              post-RESOLVING successor cancel uses SUCCESSOR_CANCELLED
                              and abort_scope=POINTER_ONLY)
    abort_observed_gc_state -- set with REJECTED when DELETE_ALREADY_ADVANCED;
                              the settled DELETING or DELETED value
    resolved_by
    resolved_at
```

The extra clustering column is not decoration. A generation can travel
`RETIRING -> ACTIVE -> RETIRING` many times, so without `retire_claim_epoch` two
retire cycles sharing a `due_at` collide on one row and one is silently lost — a work
item dropped in a design that has just forbidden the scan that used to catch it.

`resolution_state` is likewise not bookkeeping: it is the column that tells the
scanner which of three opposite actions to take from otherwise identical
`gc_state`/pointer rows.
An `OPEN` row means no resolution was authorized, so the scanner completes the
**quarantine**; a `RESOLVING` row means the scanner resumes exactly the recorded
authorized resolution, reusing `resolution_id` and — for a `RETIRED` recertification —
the `prospective_claim_id`/`prospective_claim_epoch` fixed at that moment; an
`ABORTING` row means the scanner resumes **only** the abort for that row's
`abort_scope` — settle, confirm the pointer fence, then either the generation fence
and classified restore (`POINTER_AND_GENERATION`) or `ABORTING -> REJECTED` with
`SUCCESSOR_CANCELLED` and no generation LWT (`POINTER_ONLY`) — and never continues
the resolution. A DDL that
omits any of the three, or their identities, cannot recover an interrupted resolution
unambiguously, whatever the prose says. `ABORTING` in particular is what stops a
resumed scanner from re-reading the live pointer claim and treating an abort's own
fence as the claim it should match. See Resolution Needs Its Own Durable State and
Recertification Evidence Is Prospective.

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

Quarantine work additionally carries the immutable contradiction identity and
evidence digest, `quarantine_reason`, observation timestamp, and operation ID. It is
published and confirmed before any pointer or generation mutation. Its scanner
settles/retries the same logical operation after coordinator death. The row is not
physically retired merely because the generation is globally `QUARANTINED` and any
pointer that selected it is durably `RETIRING` under a quarantine-kind claim; it
remains as a terminal-resolution tombstone until an operator marks it `RESOLVED` or
`REJECTED` and any pointer cleanup is complete. An operator resolution first
sets `resolution_state` conditionally against the full operation/evidence identity;
the scanner observes `RESOLVED` or `REJECTED` and never reopens that row.

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
materialization        confirm intent -> confirm use -> confirm discovery -> PUT
RETIRING -> ACTIVE     confirm delayed candidate + projection -> CAS
retirement handoff     confirm handoff projection -> pointer CAS
physical delete        confirm delete projection -> DELETING CAS
provisional reference  confirm expiry projection -> confirm reference row
```

A scanner that finds a projection row whose canonical state no longer matches deletes
only the projection row. That is what makes "publish early, retire late" free: the
cost of a stale row is one wasted revalidation, and the cost of a missing row is the
recovery guarantee.

### The Reference Family Is Not Optional, And Was Closed Once Already

The provisional-reference row is the inherited reference family, and it is the one
that had to be recovered rather than derived: replacing the universal "same logged
batch as the canonical row" phrasing with a per-family list dropped it, because the
recovery families that got written down were only those the generation fence
introduced. It is already a **closed finding** on `main`:

> F10 — "Provisional reference and its expiry were written separately, so a failure
> between them left an undiscoverable reference." Closed by PR-8
> ([#144](https://github.com/Sesame-Disk/sesamefs/pull/144)), which writes the
> TTL-bound reference, the longer-lived tracker and the durable by-day projection in
> one logged batch.

The failure it closed is exactly the one the generation design would reopen, and it
is worth stating in the new vocabulary because the mechanism is not obvious:

```text
write reference row (TTL from expires_at)
    -> CRASH
    -> expiry projection row never written

the reference row TTL-expires on its own
    -> references reach zero with no event and no scanner row
    -> nothing ever creates a candidate for that generation
    => ACTIVE, zero references, invisible to every automated path
```

Nothing is lost — this is retention, not deletion — but the generation is
uncollectable and the scan that used to catch it is gone. The current code's own
comment says it plainly: an unprojected reference "pins the block forever with
nothing able to find it"
(`internal/db/provisional_block_ref_expiry.go:56-61`).

Two shapes can satisfy the invariant in principle, but this contract selects the
existing logged-batch shape. The rejected ordering-only alternative has a race the
other GC/recovery families do not have. Their projections are
consumed by a scanner that revalidates canonical state and deletes a projection whose
branch no longer matches — which is exactly the wrong behaviour here:

```text
W: confirms the expiry projection
S: reads the projection, finds no reference row yet,
   concludes the branch is gone and deletes the projection      <-- F10 again
W: writes the reference row
```

What saves the ordering variant is that this projection is **scheduled**, not
immediate: it is keyed by `expires_at`, which a fresh admission sets a full
provisional TTL into the future, and the scanner skips a row that is not yet due
(`internal/gc/scanner.go:265-268`, `if expiry.ExpiresAt.After(now) { continue }`).
The current implementation relies on exactly this and says so — a projection just
written "points ~48h into the future, so nothing acts on it until long after every
statement has landed" (`internal/db/provisional_block_ref_expiry.go:68-71`).

That is a precondition, not a property of ordering, so it has to be written down as
one:

```text
the scanner processes a projection row only when expires_at <= now
and
a writer publishes the reference only while a full TTL margin remains
```

The current scanner adds a second defence worth carrying forward: it re-checks the
exact reference before classifying either outcome, precisely because "a logged batch
is atomic but not isolated, and sweeping a projection while its live reference is
visible can erase the upload's only durable retry anchor"
(`internal/gc/scanner.go:279-284`). The generation-aware scanner needs the same
re-check against `(generation_id, referrer, reference_instance_id)`.

The frozen PR-5 choice is:

- **one logged batch**, which is what the current implementation does
  (`internal/api/v2/fs_helpers.go:989-994`) and what
  `TestRegisterUploadedBlock_WritesReferenceAndExpiryAtomically` asserts. It prevents
  a crash from durably applying only one member, adds no Paxos, and has the smallest
  writer surface;
- the ordering-only alternative — confirm
  `gc_provisional_generation_refs_by_day`, then write the reference row — remains
  recorded above only as evaluated and rejected. It is not an implementation option
  in this ADR.

The logged batch remains an implementation convenience rather than an isolation
barrier. It does **not** remove either scanner precondition above: a concurrent reader
may observe batch members at different times, so the scanner still skips
`expires_at > now` and re-checks the exact reference before deleting or classifying
the projection. What is not optional is that the projection may never lag the
reference, for the generation-bound reference exactly as for the logical one it
replaces. The same rule covers `pub:` rows, which the projection must also carry.

`block_generation_references_by_referrer` is deliberately **not** in this list. It is
a cleanup convenience, not a discovery record: losing it costs an un-removable
reference row, which is retention, and no path infers liveness from its absence.

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
| `RETIRING/QUARANTINE -> ACTIVE` (operator resolution) | the quarantine work at `RESOLVING`, **plus** a delayed candidate and its discovery projection published **before** the pointer CAS | the work is retired only after the pointer CAS is confirmed and it is marked `RESOLVED` |
| `RETIRED/QUARANTINE -> RETIRED/GC_RETIRE` (operator recertification) | the quarantine work at `RESOLVING`, **plus** fresh retirement evidence for the new claim epoch and the handoff projection, all published **before** the pointer CAS | the work is retired only after the pointer CAS is confirmed and it is marked `RESOLVED` |

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
`blocks` reads `G1 / RETIRED/GC_RETIRE` with valid evidence and no delete work yet is covered
by a row that already exists. Its scanner revalidates canonical state, so publishing
it for a retirement that then reactivates instead costs one wasted revalidation.

This needs **no additional Paxos**. PR-1 implements the dedicated projections named
above, including `gc_generation_zero_ref_by_day` and
`gc_generation_handoff_by_day`; it does not overload the legacy candidate row with
either identity. Neither the intra-step ordering nor the handoff is optional.

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
retire_claim_kind
retire_abort_id
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
    materialization_owner -- the operation_id of the materializing request; the
                             same value its materializer use row carries, so the
                             two rows are joinable without a second identifier
    materialization_deadline
    delete_claim_id       -- recorded by the DELETING transition; never a precondition
    delete_claim_epoch    -- recorded by the DELETING transition; never a precondition
    delete_authorized_at
    quarantined_at
    quarantine_reason
    quarantine_operation_id
    quarantine_evidence_digest
    resolution_epoch      -- monotonic; initialized to 0 by the MATERIALIZING
                             intent that creates the row, and bumped ONLY by an
                             abort. No quarantine transition writes it, so it is
                             preserved across every quarantine cycle and never
                             resets. The false-positive QUARANTINED -> null clear
                             conditions on it, which is how an abort fences this
                             partition. Written only by this table's own
                             transitions, so unlike a retire_claim_* column the
                             condition is satisfiable (correction 68 versus
                             correction 174). See correction 175 for why it is
                             initialized at row creation rather than at quarantine
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
- keeps `quarantine_operation_id` and `quarantine_evidence_digest` so operator
  resolution is tied to the exact contradiction that caused the terminal state;
- its quarantine work row has a `resolution_state` of `OPEN`, `RESOLVING`, `ABORTING`,
  `RESOLVED`, or `REJECTED`, with the resolving operation, actor, and timestamp.
  `RESOLVING` additionally carries `resolution_id`, `decision`, `verification_digest`,
  `started_at`, and the `observed_resolution_epoch`; `ABORTING` carries `abort_id`,
  actor, reason, and `abort_started_at`. Terminal abort outcomes additionally carry
  `abort_outcome` and, when the delete already advanced, `abort_observed_gc_state`.
  Scanners must confirm this state before
  retrying, must resume an `ABORTING` row only as an abort, must treat a
  `RESOLVING` row whose pointer already reads `QUARANTINE_ABORT` as an abort to
  adopt (never as a continuing false-positive resolution), and must never reopen
  `RESOLVED` or `REJECTED` work;
- is never transitioned out of `QUARANTINED` by any automated path, only by explicit
  operator action. Resolution is an audited workflow whose **first durable step is the
  authorization itself**. The operator conditionally moves the quarantine work from
  `OPEN` to `RESOLVING`, recording `resolution_id`, `decision` matching the row's
  immutable `work_kind` (`FALSE_POSITIVE` or `ALLOW_SUCCESSOR_AFTER_DELETE`), the
  actor, the verification digest, and `started_at`, and confirms it **before touching
  `gc_state` or the pointer**. Without that record the two mutations are
  indistinguishable from an in-progress quarantine after a crash; see Resolution Needs
  Its Own Durable State. `work_kind` is fixed at row creation so an `OPEN`
  `SUCCESSOR_AFTER_DELETE` row is never recovered as "complete quarantine". Then, for
  a false positive: clear `gc_state` with a
  conditional `QUARANTINED -> null` update at `SERIAL + ALL` that matches the exact
  generation, `storage_key`, quarantine operation/evidence identity, and the
  `resolution_epoch = Rr` recorded by the `RESOLVING` row, after fresh
  exact-object verification. The epoch is the generation partition's own fence: it is
  what an abort bumps to invalidate a stalled resolver's clear, and the pointer
  takeover cannot serve that role because correction 68 forbids a generation-row
  condition on `retire_claim_*`. It must **not** condition on
  `materialization_state = VERIFIED`. A `MATERIALIZING` generation is one of the things
  worth quarantining — that is why the quarantine statement itself uses `storage_key`
  rather than `materialization_state` as its identity guard — so requiring `VERIFIED`
  to clear would leave every quarantined `MATERIALIZING` generation permanently
  unresolvable, an `IF` clause that can never apply on exactly the class the workflow
  exists for. Where the re-verified object proves the generation out, repair
  `materialization_state = VERIFIED` in its own write **before** clearing `gc_state`,
  with the same `resolution_epoch = Rr` condition the clear uses — every
  post-`RESOLVING` resolver mutation of `block_generations` must name that epoch, or
  an abort's bump would revoke only the last statement and leave a stalled repair
  able to fire after `REJECTED`. Exactly as `VERIFIED` Lag Is Not A Contradiction
  already prescribes for recovery;
  where it does not, the contradiction is confirmed and the work is rejected rather
  than resolved. Then complete
  the pointer step for the state the pointer is actually in. If the pointer carries a
  matching `RETIRING/QUARANTINE` claim, publish and confirm a delayed candidate and its
  discovery projection, then clear the claim columns and return it to `ACTIVE` with a
  conditional `SERIAL + ALL` LWT. If the pointer is `RETIRED/QUARANTINE`, it is
  **recertified rather than reactivated**: using the prospective `(Cr, Nr)` fixed by the
  `RESOLVING` record, re-run the global zero check **while the pointer still reads
  `RETIRED/QUARANTINE`** — that existing fence is what protects the check, since
  `(Cr, Nr)` is not installed yet — append and confirm the prospective evidence for
  `Nr`, then move the pointer to `RETIRED/GC_RETIRE` with a conditional `SERIAL + ALL`
  LWT that installs exactly that pair. **No resolution returns a `RETIRED` pointer to
  `ACTIVE`**, and an unconfirmed candidate or evidence append forbids its CAS; see
  Resolving A Quarantine Without Breaking Irrevocability and Recertification Evidence
  Is Prospective. The generation-row step precedes the pointer step,
  so no writer can observe an active pointer to a still-quarantined generation. Only
  after the required pointer step is confirmed does the operator mark the work
  `RESOLVED` with a conditional update matching the full identity. If a coordinator
  dies before that final marker, the work remains `RESOLVING` and the scanner resumes
  that exact authorized resolution; it must not retire the work while pointer cleanup
  is pending. If the contradiction is confirmed **while the work is still `OPEN`**,
  transition `OPEN -> REJECTED` and retain `QUARANTINED`; if it is confirmed after the
  work reached `RESOLVING`, follow the state-aware abort contract in Resolution Needs
  Its Own Durable State rather than completing the false-positive resolution: durable
  abort authority linearizes on `blocks` first as `QUARANTINE_ABORT` /
  `retire_abort_id`, then work adopts `ABORTING`, then the generation fence, then
  rollback. If any
  identity or condition does not match, retain quarantine and
  require a new audited resolution; no operator action may authorize deletion
  implicitly. The scanner must see a terminal work state before retiring the work
  record or accepting any retry.
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

That read shape gives `block_generation_references` a tombstone problem the use
table does not have, and PR-1 must specify its compaction strategy,
`gc_grace_seconds`, and repair SLA for a reason of its own. A generation becomes a
candidate precisely when its references reach zero, so the destructive proof reads
that partition at the moment its live rows are gone and its removals are not: the
partition can hold **a tombstone for every reference removal not yet purged**, which
is `O(reference churn between successful purges)` rather than a fixed count —
compaction may already have reclaimed earlier ones, and `gc_grace_seconds` bounds
how soon it may. The read must take the partition whole; the single-partition rule
above forbids every workaround. A block whose referrers churned heavily therefore
arrives at the drain with a tombstone-dominated partition, and crossing
`tombstone_failure_threshold` turns the read into an error rather than a slow answer.

Without an explicit escape, the consequence would be worse than a stalled collection:

```text
step 5, first branch:  any read error / DC unavailable (GC_RETIRE)
    -> no evidence, no delete
    -> confirm delayed candidate
    -> RETIRING/GC_RETIRE -> ACTIVE when the pointer LWT is available
```

The error branch deliberately does not need to know whether references or uses exist:
reactivation retains bytes and permits all of them to remain valid. If the pointer
CAS itself is unavailable, writers remain fenced until global coordination recovers;
once it does, a permanently unreadable reference partition no longer makes the fence
permanent. Collection can remain blocked until compaction/repair restores the global
read, which is retention rather than block-wide write unavailability.

The parameters that keep the partition readable are still capacity requirements, and
PR-6 should surface a tombstone-count metric on this read rather than discovering the
threshold in an incident. The safe reactivation escape removes unreadability from the
data-safety and permanent-writer-fence proofs; it does not make an uncollectable hot
partition operationally acceptable.

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

This is the generation-aware successor of the existing
`gc_provisional_block_refs_by_day` (`internal/db/migrations/001_initial_schema.cql:342-354`)
and mirrors `gc_block_candidates_by_day`, `gc_failed_items_by_expiry`, and
`gc_s3_orphans_by_day` (`internal/db/migrations/001_initial_schema.cql:317-329,1180-1190,1254-1263`),
which all partition by `(day, bucket)` so the scanner can walk a day in parallel.
Partitioning by `org_id` would concentrate every expiring reference of the largest
tenant into one partition. The projection must cover `pub:` as well as `up:`; its
scanner confirms the exact reference row's expiry before cleanup and candidate
creation.

It is published under the ordering rule of The Reference Family Is Not Optional, And
Was Closed Once Already: never after the reference row it describes. It also keeps
the property its predecessor has for a stated reason — **no TTL of its own**
(`internal/db/provisional_block_ref_expiry.go:46-48`) — because it is the recovery
anchor that has to outlive the canonical row's self-expiry, including when scanning
is delayed past the tracker margin. It is retired by its scanner after the branch is
resolved, never by expiry.

The expiry scanner never deletes a reference based only on a stale
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
- `docker-compose.prod.yml:176,184` still supports `SimpleStrategy` as a legacy
  fallback. Under a non-`NetworkTopologyStrategy` keyspace an `EACH_QUORUM` read
  silently degrades to an ordinary quorum, which is the exact guarantee X2 rejects.
  The activation gate needs a startup assertion, not an assumption. (These are the
  line numbers in this branch. The audit baseline `186d7800d` has them at `174,182`;
  the two-line `GC_ENABLED=false` block this branch adds to the same file shifted
  them.)
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
  reuses that LWT rather than adding a parallel one, adds no **new** Paxos operation
  to pins, references, reuse, repair authorization, or dedup, and adds one logical
  rematerialization activation operation. Writer mode still moves every existing
  `blocks`-partition LWT into the selected global `SERIAL` domain. Retirement and
  delete LWTs run in the GC worker.
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

**Two milestones, not one.** Freezing this ADR and completing the Phase-0
measurements are separate, and merging the document does not close the second:

```text
ADR under review  r3 protocol accepted for review; not frozen for implementation
                  until the human explicitly freezes it
Phase 0 complete  0a measurements complete; 0b records an explicit GO decision for
                  one selected paxos_variant target, every eligible node's
                  build/variant identity, foreground SLOs, writer lifetime,
                  upload-to-commit window, tombstone envelope and GC throughput
```

Every safety deadline and TTL in this document is derived from **enforced protocol
bounds**, not an observed maximum: request/server operation deadlines, S3/Cassandra
timeouts, retry-count and cumulative-backoff ceilings, upload-session expiry,
publish/repair deadlines, cancellation behavior, clock skew, and an explicit margin.
Measurements validate SLOs and choose headroom inside those hard bounds; a load test
cannot prove a maximum. A merge that freezes the design must say so explicitly rather than
letting "PR-0 merged" be read as "Phase 0 done". PR-1 may prototype against the
reviewed schema decisions, but it must not merge until Phase 0b records GO; no numeric
parameter is fixed until the measurements exist.

Phase 0 has two explicit products:

- **0a, prototype and measurements:** a disposable generation schema/query/load
  harness, exact engine semantics and identity, per-funnel writer/LWT inventory,
  three-DC foreground latency and throughput, validation of every enforced lifetime/
  TTL bound, storage and clock gates, use/reference churn, tombstone behavior, and GC
  capacity. The disposable prototype is required to measure the proposed “after”
  query plan before PR-1 merges; it is not a production migration;
- **0b, go/no-go:** compare those measurements to predeclared endpoint SLOs, timeout
  headroom, Cassandra QPS/capacity, repair/compaction limits, and the delivery budget.
  A semantic mismatch, unbounded writer funnel, unreadable qualification-envelope
  partition, or
  foreground SLO failure is NO-GO for merging PR-1. The response is to change the
  deployment target, capacity/concurrency plan, or schedule and remeasure, never to
  weaken `SERIAL`, `ALL`, `EACH_QUORUM`, or the positive usability predicate.

Before implementation, PR-0/Phase 0 must define, enforce in the disposable prototype,
and fault-test two different bounds; it must not infer either from the largest sample
or collapse them into one number:

- the enforced **single-operation writer bound** for every funnel, including S3
  upload, metadata, reference publication, retry/backoff, server-side cancellation,
  and any separately authorized publish repair. This sets the authority deadline and,
  through it, `retention_expires_at`;
- the enforced **upload-to-commit bound**, including upload-session expiry and every
  permitted client/server commit retry. This sets the provisional reference TTL. See
  Provisional Reference Lifetime Bounds The Upload-To-Commit Window.

Fault injection must prove operations cannot continue after those bounds. All safety
margins are computed from that enforced inventory plus clock/operation headroom, not
guessed constants or observed maxima.

PR-0 must also measure the deployed `paxos_variant`, the complete activation critical
path (retirement-evidence `EACH_QUORUM` read plus activation `SERIAL + EACH_QUORUM`
CAS), and the p50/p95/p99 between every pair of participating DCs. It must also
measure GC candidate throughput under bounded concurrency and the queue age target.
Those numbers justify the inline decision and the worker capacity; without them the
inline-versus-background and serial-versus-concurrency questions can only be
asserted.

The foreground side is equally mandatory. From every application region, Phase 0
measures the existing first-writer `blocks` LWT and every unrelated option-A LWT at
p50/p95/p99/error rate under forecast concurrency. It runs representative new-content
and deduplicated uploads, including a 1 GiB payload at the block size and concurrency
used by each supported funnel. The result records two different costs:

- CQL statement amplification per block before/after, and aggregate Cassandra QPS at
  each declared workload/concurrency point, including use-row
  insert/authorization/delete and projection/reference work;
- serial critical-path stages after safe parallelism, read reuse, and coalescing.

The probe fast path can remove a non-applying metadata LWT only when a complete
existing row is observed. It cannot remove consensus for first upload of new content,
a first-writer race, or rematerialization. Existing browser, sync, and resumable
concurrency is measured and bounded first; PR-3 adds a shared materialization admission
budget only if traces show driver saturation, timeout amplification, or SLO failure
that existing per-funnel limits cannot control. It must never serialize unrelated
block partitions fleet-wide merely to hide WAN latency.

Phase 0 also declares a finite qualification envelope before testing: maximum live
references and renewals per generation, use/reference write rate, partition age and
size, maximum enforced repair outage, compaction backlog, and safety headroom. It then
load-tests that envelope through the selected compaction, `gc_grace_seconds`, repair
cadence, and whole-partition `EACH_QUORUM` reads. If production cannot enforce or
capacity-plan that envelope, the schema must gain bounded rollover/another proof
before GO. The criterion is not merely that errors retain data: reads stay below
declared tombstone warning/failure thresholds with repair and capacity headroom.

Phase 0 also records `paxos_state_purging` on every joined cluster node, confirms that
no generation-fence LWT uses commit consistency `ANY` or `LOCAL_QUORUM`, and records
any recurring Paxos repair obligation and irreversible non-legacy transition. It
records the authoritative time service, maximum observed application and
Cassandra clock offset/drift, the selected `generation_fence_max_clock_skew`, and the
attestation freshness limit. It verifies authoritative storage-class resolution and
connectivity from `dc-na`, `dc-eu`, and `dc-asia`. Finally, it pins source-level
  evidence for Cassandra 5.0.9 Paxos v2's aggregate LWT commit callback and measures the
`SERIAL + ALL` retirement fence separately from the `SERIAL + EACH_QUORUM`
activation path.

  PR-0 must select one `generation_fence_paxos_variant` target from the Cassandra 5.0.9
semantic set `{v1, v2}`, record the image digest or artifact checksum for the exact
audited build, and verify that every generation-fence participant node reports both
that build identity and that selected target. A release-version check alone is not
enough, and a mixed `{v1, v2}` deployment is not an accepted writer-mode state.
Phase 0b also replaces the uncalibrated delivery reserve below with a line-item
estimate for remaining schema, implementation, integration, operations, rollout,
review/rework, and contingency. No implementation schedule is approved before that
rebaseline.

### PR-1: Final Greenfield Schema And Models

Add generation, materialization, pin, reference binding, candidate, queue, orphan,
claim, and recovery fields/tables. Freeze the exact single-partition primary keys
for uses and references, the use TTL/retention policy, the retained logical
`blocks` pointer after physical deletion, and the non-mutable generation markers.
Add Go models and migration tests.

Includes the append-only retirement-evidence table, the `QUARANTINED` state, the
`(expiry_day, bucket)` partitioning for the provisional-expiry projection, and
positive-predicate generation-aware claims that cannot create stub rows. The
migration must include
bounded recovery projections for `MATERIALIZING`, pending quarantine, and
`gc_state=DELETING`, the immutable pointer `retire_claim_kind`, and an
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
  reintroduced as a TTL;
- `block_generation_references` gets its own compaction strategy, `gc_grace_seconds`,
  and repair SLA, sized for a whole-partition `EACH_QUORUM` read taken when the live
  references are gone while tombstones for removals not yet reclaimed by compaction
  remain — worst-case pressure proportional to churn between successful purges, not
  a fixed count. This is a separate exercise from the use table's policy and has a
  different failure mode: a read that trips `tombstone_failure_threshold` blocks
  collection, publishes no evidence, and takes the delayed-candidate
  `RETIRING/GC_RETIRE -> ACTIVE` escape. Compaction remains required for reclamation capacity,
  but unreadability cannot be the only exit from the writer fence;
- `gc_provisional_generation_refs_by_day` carries **no TTL** and is retired by its
  scanner, matching the predecessor it replaces;
- `gc_generation_deletes_by_day` carries `attempt_count`, `next_attempt_at`,
  `escalated_at`, `last_error`, and an idempotent `dlq_escalation_id` keyed per
  `(org, block, generation, escalation)`. These are the columns the terminal
  delete-failure contract runs on, and a verification case already asserts their
  behaviour; leaving them in prose would let PR-6 invent a DLQ that dequeues the work
  row. See When The Physical Delete Keeps Failing and correction 172;
- the operator `QUARANTINED -> null` clear matches generation/key plus quarantine
  operation/evidence identity **and `resolution_epoch = Rr`**, and **must not**
  condition on `materialization_state`, since a `MATERIALIZING` generation is
  quarantinable. The preceding `MATERIALIZING -> VERIFIED` repair, when required,
  names the same `resolution_epoch = Rr`. See corrections 171 and 180;
- `block_generations` carries a monotonic `resolution_epoch`, initialized to 0 by the
  `MATERIALIZING` intent that creates the row — **not** by the quarantine transition,
  which must not write it at all — named by every post-`RESOLVING` resolver mutation
  of the generation row, and bumped only by an abort after the pointer fence has
  linearized. Without it an abort can only fence the pointer, and a stalled resolver
  clears the quarantine afterwards. Initializing it at quarantine instead would leave
  `Rr` null on the first cycle and reset the counter on a second, reintroducing an ABA
  on the fence. An ambiguous abort bump settles the generation partition serially and
  classifies `Rr` versus `Rr + 1`. See corrections 174, 175, 177, and 178;
- `gc_generation_quarantines_by_day` carries `ABORTING` in `resolution_state`, plus
  `abort_id`/`abort_actor`/`abort_reason`/`abort_started_at`,
  `abort_outcome`/`abort_observed_gc_state`, and the `observed_resolution_epoch` the
  clear, the repair, and the abort fence all name. See corrections 173 and 179.

### PR-2: Explicit Consistency Helpers

Add local writer operations and explicit global destructive operations. Add query
context propagation and fail-closed behavior for all global reads/LWTs. Add serial
domain selection for every LWT that can touch a generation-managed `blocks` row;
ordinary writer operations remain `LOCAL_QUORUM`.

The enforcement is split across three mechanisms, and the split is normative because
one of them cannot do the others' job:

```text
Startup assertion (configuration and cluster state):
    NetworkTopologyStrategy
    exact configured DC set, and positive RF per DC
    no pending Cassandra topology/range/RF mutation
    every generation-fence participant node reports the selected
    generation_fence_paxos_variant target (chosen from `{v1, v2}` for the pinned
    5.0.9 build)
    session serial default is SERIAL
    fresh in-bound clock-health attestations
    authoritative storage targets reachable from every application region

Deployment assertion (orchestration state):
    every block writer is generation-capable
    no deterministic-key or legacy-reference writer can receive traffic
    no in-flight or durable legacy operation can resume an old-schema write

Code invariant (one helper, no raw statements):
    every blocks-partition LWT is issued through one constructor
    that sets the serial level explicitly
    ACTIVE -> RETIRING and its ambiguity reaffirmation commit at ALL
    null -> QUARANTINED and its ambiguity reaffirmation commit at ALL
    no generation-fence LWT commit uses ANY or LOCAL_QUORUM

Test and inventory (what startup cannot see):
    every LWT in the Current Code Evidence inventory has an effective
    serial level of SERIAL
    a blocks-partition LWT issued through a raw Session().Query fails
    the inventory test
```

`EACH_QUORUM` reads degrade silently without NTS; mixing `LOCAL_SERIAL` and `SERIAL`
on the same pointer partition is not an accepted generation-fence profile; and a
variant without linearizable reads removes the serial read that Recovering A Crashed
Activation depends on. These properties fail silently, so none is left as an
operational note. Topology/DC/RF, the selected `paxos_variant` on every current
generation-fence participant, and the configured session default are checkable at
startup and through continuous participant eligibility. The effective serial level of
every Go statement is not; that statement-level invariant is carried by the helper
and inventory tests. See correction 122 and Generation-Fence Participant Eligibility
Is Continuous.

PR-2 implements the option-A decision in Which Serial Level Is The Session Default.
There is no per-query serial level in the repository today, so the initial
implementation keeps the session default at `SERIAL` and accepts that head-commit
promotion, file locks, upload-session slots, and the `gc_leases` finalize lease use
cross-DC Paxos in multi-DC profiles. PR-0 measures that cost; localizing any unrelated
statement later requires a separate inventory-backed change and may never include a
generation-managed `blocks` statement.

PR-2 additionally owns the split projection helpers: writer-originated recovery and
expiry scans are explicit `EACH_QUORUM` reads, while GC-owned projections remain
local to the designated GC DC. Quarantine work is handed off to that DC and
confirmed there before mutation, because its discovery may originate in any writer
DC. After handoff confirmation, every post-handoff `LOCAL_QUORUM` work-state read or
mutation runs only in that owner DC; operator/API requests that arrive elsewhere are
routed to the owner and must not mutate in the caller DC (correction 184). A generic
projection reader that silently inherits the session's `LOCAL_QUORUM` is not
acceptable for the writer-originated families.

PR-2 also implements the maintenance interlock: a durable cluster-wide
topology-maintenance marker is acquired before any Cassandra topology mutation and
confirmed before admission is drained. Pending ranges, joining/leaving/moving nodes,
configured-versus-actual token/DC/rack/RF drift, or that active marker make writer
mode and destructive GC ineligible. The operational runbook owns the Cassandra
command sequence; application code owns refusing to serve through the transition.

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
outcome handling, the explicit `PENDING -> AUTHORIZED` write, the full
authority-tuple confirmation, the bounded non-active state retry contract, and pin
cleanup into every upload/reuse/publish funnel.

### PR-5: Generation-Bound References

Make provisional, publish, permanent, repair, and pending-OnlyOffice references
generation-aware without changing the logical `fs_object` content identity.

The same change must remove every read and write of `block_references`, so the
compiler prevents two liveness sources from coexisting.

It also carries the provisional-reference publication rule. F10 is a closed finding
on `main`, and the generation-aware rewrite is the one place it can be reopened: the
expiry projection is confirmed before the reference row, or both go in one logged
batch as they do today. PR-5 keeps that selected shape and the existing atomicity
test's intent, generation-bound.

### PR-6: Retiring GC And Claim Leases

Implement `RETIRING`, use drain, global reference checks in the specified decision
order, claim epoch/deadline takeover, append-only retirement evidence, `RETIRED`,
generation `gc_state=DELETING/DELETED`, `QUARANTINED`, and exact-key deletion.

The drain reads uses before references — see Read Order Is Not Decision Order — and
implements the `RETIRING/GC_RETIRE -> ACTIVE` escape on expired-authority uses.
Unexpired `PENDING` uses keep the fence. A `RETIRING/QUARANTINE` claim has no
reactivation escape. Ambiguous pointer LWTs are settled in the serial
Paxos domain and then complete the exact `SERIAL + ALL` visibility reaffirmation
before the drain. A normal claim also commits at `ALL`. Read errors publish a delayed
candidate and attempt the retention-safe `RETIRING/GC_RETIRE -> ACTIVE` escape; they never
append evidence and never remain the only path out of the writer fence. The
retirement handoff writes epoch-stamped evidence before the pointer CAS and never
authorizes `DELETING` from the generation row alone.
Every quarantine first publishes its durable work with immutable
`work_kind=QUARANTINE`. If the generation is
pointer-selected `ACTIVE`, PR-6 acquires `RETIRING/QUARANTINE` at `SERIAL + ALL`,
drains uses at `EACH_QUORUM` without permitting publication or delete, and only then
transitions the generation at `SERIAL + ALL`. An ambiguous quarantine settles the
exact generation partition serially and then completes its exact identity
reaffirmation at `SERIAL + ALL`; the durable work and pointer fence make every crash
window recoverable.
PR-6 also owns the operator resolution workflow and its abort. An abort first
persists a durable **abort intent** on the still-`RESOLVING` work row under
single-assignment (`IF pending_abort_id = null`): immutable logical `A` plus
actor/reason/`started_at` and the current fence attempt `F` with the **full source
authority tuple** and target `(Cf, Nf, Df)`. That intent grants **no** authority;
concurrent writers must not overwrite `A` with `A2` — a loser observes canonical
`A'` and returns already-in-progress/conflict. Ambiguous intent results SERIAL-settle
and exact-retry the same full attempt payload. The **first** authoritative act is
then a `SERIAL + ALL` claim takeover on `blocks` matching that source tuple
(including `retire_abort_id=source_abort_id` when source kind is `QUARANTINE_ABORT`)
and installing `retire_claim_kind=QUARANTINE_ABORT` with that attempt's `(Cf, Nf)`
and `retire_abort_id=A`. Linearization proof is `retire_abort_id=A`, not matching
`(Cf, Nf)` after a later ordinary takeover. Only after it applies (or SERIAL
settlement proves `retire_abort_id=A`) does the work move `RESOLVING -> ABORTING`.
An intent alone must never cause `ABORTING`, generation fence, or rollback.
If settlement proves a **pre-linearization** claim supersession (`retire_abort_id`
still equals `source_abort_id`), revise the fence attempt under the same `A`.
`abort_scope` is derived from `work_kind`: `POINTER_AND_GENERATION` then bumps
`resolution_epoch` at `SERIAL + ALL` and classifies rollback; `POINTER_ONLY`
(`SUCCESSOR_AFTER_DELETE`) MUST NOT issue a generation LWT and terminals as
`REJECTED` with `abort_outcome=SUCCESSOR_CANCELLED`. An ambiguous **pointer** fence
leaves work still `RESOLVING` with the fence **unresolved**. An ambiguous
**generation** fence (POINTER_AND_GENERATION only) settles the generation partition
serially. Ordinary `QUARANTINE_ABORT` takeover preserves `retire_abort_id` exactly.
Where settlement proves the resolution or successor pointer step already committed
before abort authority could linearize, finish `RESOLVING -> RESOLVED` (never via
`ABORTING`) and raise a new quarantine through the form matching the pointer's
current state — `ACTIVE -> RETIRING/QUARANTINE`, `RETIRED/GC_RETIRE ->
RETIRED/QUARANTINE`, or the generation-row form when a successor already owns the
pointer. After `DELETE_ALREADY_ADVANCED`, the later human exit is a **new** work
identity with immutable `work_kind=SUCCESSOR_AFTER_DELETE` and, at `RESOLVING`,
`decision=ALLOW_SUCCESSOR_AFTER_DELETE` — never a reopen of terminal `REJECTED`, and
never an `OPEN` that means "complete quarantine". An abandoned successor `OPEN` may
terminate as `REJECTED` (declined). There is no `ABORTING -> RESOLVED`
transition. See corrections 168, 170, 177, 178, 181, 182, 184, 185, 186, 187, 188,
189, and 190.
PR-6 must also implement bounded worker concurrency and a measured queue-throughput
target; a serial queue is not an adequate capacity plan for the global cold path.

It owns the terminal failure path too. `DELETING` has no exit but `DELETED`, so a
permanently failing physical delete **adds** a DLQ record, a metric and an audit
event while keeping its delete work row; it never dequeues the row into the DLQ,
regresses `gc_state`, or quarantines. The row remains eligible for low-frequency,
idempotent automatic retries through `next_attempt_at` while the DLQ alerts the
operator; only confirmed exact-key absence permits `DELETED`. See When The Physical
Delete Keeps Failing.

### PR-7: Recovery And Readers

Make canonical reads use persisted keys, reach materializing and deleting generations
through bounded recovery projections, reconcile retirement evidence against the
retirement-authorizing claim, rebuild missing orphan projections, preserve the
logical pointer row, and clean only exact generations. Quarantined generations are
skipped by every automated path.

Every activation outcome is first settled through `SELECT ... CONSISTENCY SERIAL`.
When the settled pointer has advanced, PR-7 walks exact predecessor/evidence links
backwards: membership proves a historical winner and routes it through normal
retirement/delete recovery; only a complete chain that excludes the candidate proves
a losing orphan. Ordinary `EACH_QUORUM` pointer reads never classify activation.

### PR-8: Verification And Activation Gate

Add unit, race, exact three-DC RF1/RF3, DC outage, clock fault, storage readability,
quarantine visibility, topology-maintenance interlock, legacy-work drain, crash,
delayed-delete, and coordinated greenfield rollout tests. Keep
`GC_ENABLED=false` until all acceptance checks pass.

## Verification Plan

### Unit And Race Tests

Required cases include:

- ambiguous `BeginPin` never permits S3 use;
- `PENDING` pin confirmation and revalidation race with `RETIRING`;
- an `AUTHORIZED` pin can finish and publish across a normal `GC_RETIRE` pointer
  fence, while a `QUARANTINE` fence revokes publication and drains the use;
- a `PENDING` pin cannot publish after observing `RETIRING`;
- any generation-bound reference reactivates a `GC_RETIRE` generation;
  a `QUARANTINE` claim never reactivates automatically;
- a use holding live authority never reactivates a `GC_RETIRE` generation; it extends
  the drain, and neither live nor expired uses reactivate a `QUARANTINE` claim;
- a `GC_RETIRE` generation with one or more remaining uses, all of which have expired
  their authority, is reactivated rather than parked, so an abandoned pin cannot make
  a block unwritable for its full `retention_expires_at` window;
- zero uses follows the retirement branch rather than the expired-use reactivation
  branch; the "every use expired" predicate must not match an empty set;
- a reactivation on expired-authority uses writes and confirms its delayed candidate
  and discovery projection **before** the CAS; an unconfirmed enqueue keeps the
  generation `RETIRING` and performs no CAS at all;
- a use/reference read error, including `tombstone_failure_threshold`, writes and
  confirms the same delayed candidate before attempting `RETIRING/GC_RETIRE -> ACTIVE`;
  it creates no retirement evidence, and once pointer LWTs are available the block is
  writable even if the reference partition remains unreadable; a quarantine claim
  remains fenced;
- the GC reads uses **before** references, and a test asserts the unsafe ordering
  fails: with refs read first, a writer holding an `AUTHORIZED` use that publishes
  and releases between the two reads must be caught, not silently accepted;
- provisional references do not create a full-retention-TTL availability stall;
- with both a reference and a live use present, a `GC_RETIRE` generation reactivates
  rather than parking in `RETIRING`; a quarantine claim remains fenced;
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
- under RF3/Paxos v2, `GC_RETIRE` reference-found, expired-use, and read-error
  `RETIRING -> ACTIVE` transitions may acknowledge from six aggregate responses while
  one DC remains stale at `RETIRING`; that DC rejects authorization until hints,
  repair, or a later pointer LWT converges it, and no branch treats the stale state as
  delete authority;
- writers observing `RETIRING` stop after a bounded retry budget and return a
  documented retryable result;
- ambiguous authorization confirmed only by `use_id` existence is rejected; the
  full tuple including `state=AUTHORIZED` and epoch is required;
- an ambiguous `PENDING` insert is confirmed by identity and is *not* rejected for
  lacking `AUTHORIZED`;
- an acknowledged `PENDING` insert proceeds straight to revalidation with no
  read-back, and the writer-path trace shows the round trip is absent on the
  acknowledged path and present on the ambiguous one;
- the main writer sequence performs an explicit `PENDING -> AUTHORIZED` write at
  `LOCAL_QUORUM`, preserving the original deadline and remaining TTL;
- an ambiguous materializer use aborts before PUT rather than proceeding;
- an ambiguous activation CAS reconciles against the authoritative `blocks` row and
  never orphans or deletes on an uncertain read; an ordinary `EACH_QUORUM` pointer
  result is injected after the ambiguous retry and is rejected as a classifier until
  `SELECT ... CONSISTENCY SERIAL` settles the partition;
- an ambiguous `ACTIVE -> RETIRING` LWT is settled in the `SERIAL` Paxos domain
  and then reasserts the exact `RETIRING/G/E/C/N/KIND` tuple at `SERIAL + ALL` before any
  use/reference drain; an ordinary `EACH_QUORUM` read returning `ACTIVE`, or an
  ambiguous/non-applied reaffirmation, never opens the drain;
- a normal acknowledged `ACTIVE -> RETIRING` also commits at `ALL`; under Paxos v2
  and RF3 the test withholds one DC and proves an aggregate `EACH_QUORUM` response
  would be insufficient while `ALL` fails closed;
- a pointer-selected `ACTIVE` generation cannot become globally established
  `QUARANTINED` at requested `EACH_QUORUM`: under Paxos v2/RF3 the test withholds one
  DC, proves six aggregate acknowledgements would leave its `LOCAL_QUORUM` writer
  permissive, and verifies the durable quarantine work remains while the required
  `RETIRING/QUARANTINE` pointer fence at `SERIAL + ALL` fails closed;
- an ambiguous quarantine is classified only by `SELECT ... CONSISTENCY SERIAL` on
  the exact generation partition and then reaffirms the exact `QUARANTINED/G/K/R/T/D`
  tuple at `SERIAL + ALL`; unavailable, non-applied, or ambiguous reaffirmation keeps
  the pointer fence and durable work for retry;
- an ambiguous `DELETING` result settles to the exact generation/key and recorded
  delete claim before retry or work retirement; a matching `DELETED` result requires
  exact-key absence, and a conflicting terminal state never advances;
- coordinator death after quarantine-work publication but before the pointer fence,
  after the fence but during the use drain, and after the generation mutation but
  before work retirement all resume the same operation without scan or delete;
- a second quarantine request encountering an existing `RETIRING/QUARANTINE` claim
  coalesces on the exact operation/evidence identity and does not install a second
  claim; a request encountering `RETIRING/GC_RETIRE` settles that claim and never
  overwrites it while its owner is live;
- a pending activation that could select the generation under quarantine is settled
  serially before the generation mutation; the materializer use is re-confirmed at
  the activation CAS boundary, the currently selected `ACTIVE` pointer is fenced or
  a retained `RETIRED/GC_RETIRE` pointer is converted to a matching `QUARANTINE` claim, and an
  uncertain or non-applied settlement retains the quarantine work and performs no
  mutation;
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
- a lost step-2 retirement persist **under a `RETIRING` pointer** is detected and the
  global zero check re-runs; the scope is load-bearing, because the same absence
  under a `RETIRED/GC_RETIRE` pointer must never re-run it — see the quarantine cases below;
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
- a crashed G2 activation that won, later retired, and was superseded by G3 is
  classified as a historical winner by both request reconciliation and recovery;
  neither first-life nor rematerialization cleanup may orphan K2 merely because the
  settled pointer now selects G3;
- retirement evidence survives a successor taking the pointer, and a test asserts
  that ageing it at that moment would strand `K1` in fail-closed;
- the claim-column lifecycle holds across `RETIRING -> ACTIVE` and the activation
  CAS: `retire_claim_epoch` never decreases or resets, while `retire_claim_id` and
  `retire_claim_deadline`/`retire_claim_kind` are null whenever the pointer reads
  `ACTIVE`; takeover preserves the existing kind and, when `kind=QUARANTINE_ABORT`,
  preserves `retire_abort_id` exactly; normal reactivation/retirement
  requires `GC_RETIRE`; quarantine retains its operation/evidence identity;
- a materializer whose intent write is ambiguous confirms the intent before writing
  its use, and never PUTs or activates with an unconfirmed intent;
- no recovery branch queries a pointer state on `block_generations`, and every
  recovery branch is reachable through a bounded discovery projection rather than a
  table enumeration;
- a writer-originated projection confirmed at `LOCAL_QUORUM` in each of `dc-na`,
  `dc-eu`, and `dc-asia` is discovered by the designated scanner at `EACH_QUORUM`;
  forcing that scanner to inherit `LOCAL_QUORUM` fails the test, and an unavailable
  source DC holds the cursor and retires no row;
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
  even when the settled pointer still reads `G1 / RETIRED/GC_RETIRE`;
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
- two retire cycles of one generation sharing a `due_at` produce two distinct
  `gc_generation_handoff_by_day` rows, because `retire_claim_epoch` is part of the
  clustering key;
- the topology, DC/RF, cluster-wide `paxos_variant`, and chosen session-default
  assertions all fail startup in generation-fence writer mode, with `gc.enabled=false`;
  a separate inventory test rejects any `blocks`-partition statement whose effective
  serial level is not `SERIAL`;
- writer mode also fails when any application/Cassandra clock attestation is stale or
  exceeds `generation_fence_max_clock_skew`, when any application region cannot read
  every authoritative storage class, or when deployment inventory still contains a
  deterministic-key/legacy-reference writer;
- an unconfirmed step-3c discovery write aborts before the PUT, exactly as an
  unconfirmed intent or use does;
- a materializer that wins activation and dies before publishing its reference is
  still discovered: the intent work row is not retired until either a
  generation-bound reference exists or `gc_generation_zero_ref_by_day` is durable,
  and a test injects the crash in that exact window;
- `gc_generation_intents_by_day` stays durable after the canonical materializer use
  row's TTL expires, so an abandoned materialization is still discoverable with no
  separate use projection;
- a generation that retires, reactivates, and retires again writes its evidence rows
  to distinct partitions, and every consumer reaches them by point lookup on the
  authorizing `(G, N)` rather than by enumerating a generation's cycles;
- a `RETIRED/GC_RETIRE` pointer whose authorizing evidence is absent is quarantined, and a test
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
- a pointer-selected generation reaches `QUARANTINED` only after durable work, an
  acknowledged `RETIRING/QUARANTINE` pointer fence, and a zero-use global drain; new
  uses and physical operations are rejected by both pointer kind and generation state;
  quarantine work is handed off and confirmed in the designated GC DC before either
  mutation; after that handoff, every post-handoff `LOCAL_QUORUM` work-state read or
  mutation executes only in that owner DC, and operator requests from other DCs are
  routed there rather than mutating in the caller DC;
- **post-handoff quarantine-work ownership is single-DC.** A test sets owner=`dc-na`,
  originates an operator `OPEN -> RESOLVING` request in `dc-eu`, and asserts the
  caller DC does not execute the `LOCAL_QUORUM` mutation locally: the mutation is
  routed/coordinated in `dc-na`, and a `dc-na` scanner never acts on stale `OPEN`
  after authorization has already returned to the operator. A second test creates a
  `SUCCESSOR_AFTER_DELETE` `OPEN` row from `dc-eu` and asserts creation itself is
  routed to `dc-na`, not published through the writer-DC discovery exception. A
  third test makes the owner DC unavailable and asserts the mutation fails
  closed / retries with **no** caller-DC `LOCAL_QUORUM` fallback and no automatic
  ownership transfer;
- **`work_kind` is immutable and creation is idempotent.** A test attempts
  `UPDATE work_kind` from `QUARANTINE` to `SUCCESSOR_AFTER_DELETE` (and the reverse)
  and asserts it is rejected. A second test times out an ambiguous
  `SUCCESSOR_AFTER_DELETE` insert, retries the same identity, and asserts
  idempotent success with the same `work_kind`; the same operation id with a
  different `work_kind` fails closed;
- a generation quarantined between pin insertion and authorization is rejected
  before authorization. If the quarantine pointer fence lands after a prior
  revalidation, the immediately-before-operation checkpoint detects
  `RETIRING/QUARANTINE` and starts no PUT/repair; an operation already issued before
  that checkpoint may finish but cannot be published after the fence and its use is
  drained. If an exact reference write was already issued after its publish check,
  the worker preserves that reference/key and performs no delete before globally
  establishing quarantine;
- a quarantined generation survives a process restart as `QUARANTINED`, is skipped
  by candidate discovery and every delete path, and is never automatically
  transitioned out; its resolution matches the persisted quarantine operation ID and
  evidence digest through an audited operator transition;
- an operator cannot clear `QUARANTINED` with a reason-only update: the full
  generation/key/operation/evidence identity is required, the generation-row
  conditional clears before any matching pointer claim is reactivated, and a
  confirmed contradiction has no automated or operator shortcut to deletion;
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
- a materializing generation proven to have lost the CAS by a complete settled
  lineage exclusion becomes an exact orphan and releases its use; a historical winner
  or an incomplete chain never takes this branch;
- `DELETING` with no orphan row is recovered from the generation record;
- stale retire worker cannot transition after claim takeover, because
  `retire_claim_epoch` is in the `IF` clause of every GC-owned pointer transition;
- G1 cleanup cannot remove G2 references, mappings, or objects;
- no physical delete path accepts only logical `blockID`;
- any global read error prevents deletion and, after publishing a delayed candidate,
  attempts the retention-safe `GC_RETIRE` reactivation escape; a quarantine claim
  remains fenced;
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
- the provisional reference TTL is greater than the enforced upload-to-commit
  window, and a commit whose provisional reference expired fails closed with a
  documented error rather than publishing a reference to a dead generation;
- the publish sequence writes and confirms generation-bound `pub:` references before
  persisting the visible `fs_object`, then writes and confirms every generation-bound
  `fs:` reference before releasing `pub:`; an ambiguous `pub:` or `fs:` result keeps
  the liveness holder instead of releasing it;
- a provisional reference is never durable before its expiry projection: a test
  writes the reference with the projection suppressed and asserts the funnel fails
  closed rather than publishing, then asserts the F10 shape — reference TTL-expires,
  references reach zero, no candidate is ever created — is unreachable;
- PR-5 preserves the logged batch for the provisional reference, tracker, and expiry
  projection; separate regression tests reject an ordering-only implementation,
  prove the scanner never deletes a not-yet-due row, and inject partial concurrent
  batch visibility so the exact-reference recheck preserves the projection;
- **no transition returns a `RETIRED` pointer to `ACTIVE` on the same generation.** The
  decisive test is the stale-delete interleaving: a delete worker obtains its
  pointer/evidence proof for `G1 / RETIRED/GC_RETIRE` and stalls; the pointer is
  quarantined and then resolved as a false positive; a writer then pins, authorizes and
  publishes against G1; the stalled worker issues its `DELETING` CAS. The test asserts
  that `K1` is never deleted under a live reference, and that an implementation which
  resolves `RETIRED/QUARANTINE` to `ACTIVE` fails it. A pointer re-read added before
  the `DELETING` CAS must not make the test pass, since the two statements are on
  different tables;
- a `RETIRED/QUARANTINE` resolution recertifies to `RETIRED/GC_RETIRE`: it allocates
  the next `retire_claim_epoch`, re-runs the global zero check, and appends and
  confirms fresh evidence for that epoch before the pointer CAS. A test asserts the
  pre-quarantine evidence row is not reused, and that a later G2 activates against the
  recertified claim tuple;
- the recertification zero check and evidence append run while the pointer still reads
  `RETIRED/QUARANTINE`, and a test asserts the inverted order fails: with the pointer
  moved to `RETIRED/GC_RETIRE` first, a G2 activation or delete authorization attempted
  before the append finds no `evidence(G1, Nr)` and drives G1 into permanent
  fail-closed quarantine;
- a crash after the prospective evidence and handoff but **before** the recertification
  CAS is recovered through the handoff: a test asserts the scanner discovers
  `evidence(G1, Nr, Cr)`, may retry the exact same `(Cq, Nq) -> (Cr, Nr)` CAS, and may
  **not** publish delete work, authorize `DELETING`, or permit G2 activation while the
  pointer still reads `RETIRED/QUARANTINE`. Discoverability is required; authority is
  not granted until the CAS is proven applied;
- a recertification whose pointer CAS is proven never to have applied leaves
  `evidence(G1, Nr, Cr)` as stale historical state: a test asserts it authorizes no
  deletion and no activation, ever;
- a recertification CAS that commits and is followed immediately by a G2 activation, with
  the coordinator dying before `RESOLVED`, is settled through the predecessor/evidence
  lineage: a test asserts recovery finds the link naming exactly `(G1, E1, Cr, Nr)`,
  marks the work `RESOLVED`, and does **not** read "G1 is no longer on the pointer" as a
  failed recertification or allocate a second prospective claim;
- aborting a resolution is state-aware: a test covers all three situations —
  nothing undone (`ABORTING -> REJECTED` directly), generation step done (restore
  `gc_state = QUARANTINED` at `SERIAL + ALL`, then `REJECTED`), pointer step committed
  before abort authority linearized (no abort; finish `RESOLVING -> RESOLVED` and
  raise a new quarantine). A test asserts that the general
  "complete the resolution then re-quarantine" shortcut is rejected for the first two,
  because it returns a known-suspect generation to `ACTIVE` and a writer can pin,
  authorize and publish in that window;
- **an abort never classifies from an ordinary read.** A test issues the operator
  pointer CAS, makes its result ambiguous, and then attempts the abort; it asserts that
  no `gc_state` restore and no `ABORTING -> REJECTED` occurs until
  `SELECT ... CONSISTENCY SERIAL` has settled the `blocks` partition and the
  fences required by `abort_scope` have applied (`POINTER_AND_GENERATION`: both;
  `POINTER_ONLY`: pointer fence only). The inverse case is also asserted: a CAS that actually committed,
  where recovery initially observes the old state, is discovered by serial settlement
  (and, for a recertification, by the predecessor/evidence lineage walk) and ends
  `RESOLVED`, never `REJECTED`;
- **an abort's durable authority is the pointer fence, and it runs before
  `ABORTING`.** A test confirms `ABORTING` on the work row while leaving the pointer
  still at `QUARANTINE`, then releases a stalled resolver whose CAS still names
  `(Cq, Nq, QUARANTINE)`; the CAS must still be able to apply under that broken order,
  proving why `ABORTING`-before-fence is forbidden. The positive test installs
  `QUARANTINE_ABORT` / `retire_abort_id` first, then writes `ABORTING`, and asserts the
  same resolver CAS cannot apply afterwards even though work was still `RESOLVING`
  when the fence committed;
- **an abort fences the resolver before it rolls anything back, pointer first.**
  A test keeps a live resolver paused between the `gc_state` clear and its pointer CAS,
  runs the complete abort, then releases the resolver; it asserts the resolver's CAS
  does not apply because the takeover installed `(Cf, Nf, QUARANTINE_ABORT)`, and that
  the end state
  `pointer = ACTIVE` with `gc_state = QUARANTINED` and terminal `REJECTED` work is
  therefore unreachable. A **second** test pauses the resolver one step earlier, before
  its `QUARANTINED -> null` clear, and asserts the clear does not apply either, because
  the abort bumped `resolution_epoch` past the `Rr` that statement names — the pointer
  takeover alone must fail this test, since the clear conditions on no pointer column.
  It asserts the end state `pointer` fenced with `gc_state = null` and terminal
  `REJECTED` work is unreachable. A third test asserts the generation fence runs in the
  "gc_state still `QUARANTINED`, nothing to undo" branch too, and a fourth that `Nf`
  is strictly greater than any prospective epoch fixed by the `RESOLVING` row, so no
  evidence row ever pairs a matching epoch with a different claim ID;
- **pointer fence is the abort's linearization point and runs before the generation
  fence and before `ABORTING`.** A test clears `gc_state` under the live resolver, lets
  the abort settle `blocks` and apply the pointer takeover, then releases the resolver
  before `ABORTING` is written; it asserts the resolver's pointer CAS does not apply
  and the pointer never becomes `ACTIVE`. A negative test that writes `ABORTING` first
  and then releases the resolver before the pointer takeover must fail: that order
  permits a known-suspect generation to return to service after abort was "authorized"
  only on the work partition;
- **the generation fence has the same settlement/idempotence contract as the pointer
  fence.** A test applies `Rr -> Rr + 1`, drops the response, crashes, and resumes
  `ABORTING`; recovery must SERIAL-settle the generation partition, treat settled
  `Rr + 1` with matching identity as already applied, and continue without retrying a
  conflicting payload. A second test leaves the generation fence accepted but
  unlearned, asserts that an ordinary read still seeing `Rr` does **not** authorize
  advancing to rollback, and that only serial settlement plus the exact retry or the
  settled `Rr + 1` proof does. A third test asserts that only this abort may produce
  exactly `Rr + 1` from the `Rr` frozen by its work row. A fourth test makes the
  **pointer** fence ambiguous and asserts the work remains `RESOLVING` with the fence
  **unresolved** — no `ABORTING`, no generation fence, no rollback — until SERIAL
  settlement classifies the outcome;
- **`resolution_epoch` is never null and never resets.** A test asserts the
  `MATERIALIZING` intent writes `resolution_epoch = 0`, that the `null -> QUARANTINED`
  statement does not write the column at all, and that a generation quarantined,
  resolved, and quarantined again carries the value the first cycle left rather than
  `0`. A negative test initializes the counter at quarantine instead and asserts both
  failures appear: `RESOLVING` captures `Rr = null` so the abort cannot compute
  `Rr + 1`, and a stale first-cycle `QUARANTINED -> null` naming `Rr = 0` applies
  against the reset counter;
- **an abort whose delete already advanced performs no `gc_state` rollback.** A
  test drives the legal ordering — delete proof obtained, pointer converted to
  `RETIRED/QUARANTINE`, false-positive resolution clears `gc_state`, the stalled delete
  worker's CAS applies as `DELETING` — and then requests the abort. It asserts the
  abort reaches `ABORTING -> REJECTED` without restoring `QUARANTINED` and without any
  other `gc_state`, materialization, storage-identity, or delete-lifecycle write, that
  the abort's `resolution_epoch` fence may still have advanced and is retained, that
  `abort_outcome = DELETE_ALREADY_ADVANCED` and `abort_observed_gc_state` record the
  settled value, that the delete work row and its retry/escalation state are
  untouched, that the pointer stays fenced at `RETIRED/QUARANTINE_ABORT`, and that a
  distinct alert and audit event are raised. A test that leaves recovery stuck in
  `ABORTING` with no matching branch, or that regresses `gc_state` out of `DELETING`,
  must fail. The same assertions run with `gc_state = DELETED`. A further test asserts
  the later human recovery path creates a **new** row with immutable
  `work_kind=SUCCESSOR_AFTER_DELETE` and, at `RESOLVING`,
  `decision=ALLOW_SUCCESSOR_AFTER_DELETE`, performs a pointer-only audited
  recertification (`RETIRED/QUARANTINE_ABORT -> RETIRED/GC_RETIRE`) under that fresh
  authorization, never reopens terminal `REJECTED`, and does not require
  `QUARANTINED -> null`. A negative test creates that successor row as an
  undifferentiated `OPEN` without `work_kind` and asserts recovery cannot tell
  "complete quarantine" from "await successor authorization";
- **`OPEN` recovery is keyed by immutable `work_kind`.** A test crashes after creating
  an `OPEN` + `SUCCESSOR_AFTER_DELETE` row and before `RESOLVING`; the scanner must
  not complete a quarantine. The same crash with `work_kind=QUARANTINE` must continue
  or complete the quarantine. A schema without `work_kind` must fail this pair;
- **durable abort intent precedes abort authority and is not authority.** A test
  persists single-assignment intent for logical `A` and attempt `F` with the full
  source tuple plus `(Cf,Nf,Df)`, commits `QUARANTINE_ABORT/A`, crashes before
  `RESOLVING -> ABORTING`, and asserts recovery loads the original
  actor/reason/time and exact attempt payload from the intent, adopts exact `A` into
  `ABORTING`, and never resumes the false-positive resolution. A negative test
  leaves the intent confirmed while the pointer remains `QUARANTINE` and asserts
  the intent alone causes no `ABORTING`, no generation fence, no rollback, and no
  terminal work transition. A third test makes the pointer fence ambiguous after
  the intent and asserts SERIAL settlement / exact retry of the same full payload
  — including `Df`, `source_kind`, and `source_abort_id`;
- **abort intent is single-assignment and SERIAL-settled.** A test races two abort
  writers `A1` and `A2` against the same `RESOLVING` row and asserts at most one
  logical `pending_abort_id` wins under `IF pending_abort_id = null`; the loser
  must not overwrite actor/reason/`started_at` and the API returns
  already-in-progress/conflict reporting canonical `A'`. A second test drops the
  first-assignment response, asserts an ordinary read is insufficient, and that
  SERIAL settlement plus exact retry of the same full payload is required. A
  negative test that reallocates `A2` on ambiguity, or that "adopts" `A'` by
  rewriting provenance, must fail;
- **fence attempts revise only after proven pre-linearization claim supersession.**
  A test installs intent under source `(Cq,Nq,QUARANTINE,null)` / attempt `F1`, then
  lets an ordinary still-`QUARANTINE` claim takeover advance the live epoch before
  the abort fence; exact retry of `F1`'s `Nf` must not apply. Recovery must install
  `F2` under the same logical `A` with `Nf2` strictly greater than the new live
  epoch, then fence. A second test lets `F1` linearize as `QUARANTINE_ABORT/A`, then
  an ordinary takeover advances claim id/epoch while preserving `retire_abort_id=A`;
  recovery MUST treat that as already linearized and MUST NOT prepare `F2`. A
  negative test that exact-retries the dead `(Cf,Nf)` after pre-linearization
  takeover, or that replaces `A` when revising the attempt, must fail;
- **an abandoned successor `OPEN` can terminate without completing quarantine.** A
  test drives `OPEN + SUCCESSOR_AFTER_DELETE -> REJECTED`, asserts the pointer stays
  `QUARANTINE_ABORT`, the generation is untouched, and no quarantine-completion
  path ran;
- **a successor cancelled after `RESOLVING` is `abort_scope=POINTER_ONLY`, not
  `OPEN -> REJECTED` and not the two-partition abort.** Original quarantine
  operation is `Q0`; successor work is fresh `Q1`. A test authorizes
  `SUCCESSOR_AFTER_DELETE` into `RESOLVING`, then cancels: it asserts abort intent
  with `source_kind=QUARANTINE_ABORT` and `source_abort_id=A0`, a pointer fence
  matching that source, `ABORTING -> REJECTED` with `abort_outcome=SUCCESSOR_CANCELLED`,
  the successor's `RETIRED/QUARANTINE_ABORT -> RETIRED/GC_RETIRE` CAS failing, **no**
  generation LWT, `generation.quarantine_operation_id` still `Q0`, and
  `resolution_epoch` unchanged. A negative test that issues the generation fence
  matching `Q1` must fail closed / not apply, and must not leave work stuck in
  `ABORTING`. A second test lets the successor pointer step commit first and asserts
  abort never linearized: `RESOLVING -> RESOLVED` plus a new quarantine — never
  `ABORTING -> RESOLVED`. A third test linearizes cancel `A1`, drops the response,
  then ordinary-takeovers `QUARANTINE_ABORT` preserving `retire_abort_id=A1`; recovery
  must not prepare `F2`. A fourth test takeovers **before** `F1` so live
  `retire_abort_id` remains `A0`; recovery must prepare `F2` under `A1`. A fifth
  test leaves `SUCCESSOR_AFTER_DELETE` at `RESOLVING` with pointer
  `QUARANTINE_ABORT/A0` and `pending_abort_id` null, and asserts the scanner
  continues the successor — it must not adopt `ABORTING` from the previous abort;
- **ordinary `QUARANTINE_ABORT` takeover preserves `retire_abort_id`.** A test
  takeovers a `QUARANTINE_ABORT/A` claim after deadline and asserts kind and
  `retire_abort_id` are unchanged while claim id/epoch/deadline advance. A negative
  test that nulls or replaces `retire_abort_id` on ordinary takeover must fail;
- **every post-`RESOLVING` resolver mutation of `block_generations` names
  `resolution_epoch = Rr`.** A test pauses a resolver before its
  `MATERIALIZING -> VERIFIED` repair, completes the abort, then releases the resolver;
  the repair must not apply. A negative test that conditions only the clear and not
  the repair must fail: the generation partition would not be fully fenced;
- **durable abort authority lives on `blocks`; `ABORTING` adopts the intent.** A test
  asserts no generation fence and no rollback occur while the pointer still reads
  `QUARANTINE` even if `pending_abort_*` is present, and injects a crash after the
  pointer abort fence but before `ABORTING`: recovery must observe `RESOLVING` plus
  `QUARANTINE_ABORT` / `retire_abort_id` **and** matching `pending_abort_*`, adopt
  that exact provenance into `ABORTING`, and resume **only** the abort — never the
  false-positive resolution. A further test performs an ordinary
  `retire_claim_deadline` takeover that preserves `kind=QUARANTINE` on a genuinely
  live resolution and asserts the scanner still continues it — that takeover alone
  must not be readable as an abort;
- **there is no `ABORTING -> RESOLVED` transition.** A test that attempts it after
  abort authority has linearized must not apply; lost-race termination is only
  `RESOLVING -> RESOLVED` when the resolution pointer step committed first;
- a retry of an ambiguous recertification evidence append reuses the `(Cr, Nr)` fixed by
  the `RESOLVING` record; a test allocates a fresh claim ID on retry and asserts the
  conditional insert fails closed on the conflicting payload, proving the determinism
  requirement is load-bearing;
- a delete worker holding a proof from a pre-quarantine claim still completes its
  `DELETING` CAS after a recertification, since G1 never returned to `ACTIVE`; a test
  asserts recertification does not revoke an authorization already obtained;
- `REJECTED` is reachable from `OPEN` directly, and otherwise **only** from `ABORTING`
  through the settled, state-aware abort above (`POINTER_AND_GENERATION` is
  doubly-fenced; `POINTER_ONLY` is pointer-fenced only). A test asserts that a
  bare `RESOLVING -> REJECTED` — one that skips the `ABORTING` record, the serial
  settlement, the fences its `abort_scope` requires, or the `gc_state` restore a
  `POINTER_AND_GENERATION` situation requires — does not
  apply, so no path leaves an unquarantined generation under a fenced pointer and no
  path leaves a live pointer over a quarantined generation. This criterion previously
  read "reachable only from `OPEN`", which contradicted the abort contract it sits
  beside and made the normative suite unsatisfiable; see correction 169;
- the quarantine projection DDL carries `ABORTING`, the abort identity,
  `abort_outcome`/`abort_observed_gc_state`, `observed_resolution_epoch`,
  immutable `work_kind`, `pending_abort_*` logical-abort columns, and
  `pending_fence_*` attempt columns including `source_kind` and `source_abort_id`, and
  `block_generations` carries `resolution_epoch`; a
  test drives an interrupted abort or successor-OPEN through a schema without them
  and asserts recovery is ambiguous, so neither column set can be dropped as cosmetic;
- the quarantine projection DDL carries `RESOLVING` and the full resolution identity;
  a test drives an interrupted resolution through a schema without them and asserts the
  recovery is ambiguous, so the columns cannot be dropped as cosmetic;
- operator quarantine resolution clears `gc_state` **before** any pointer step, and a
  test asserts that the reverse order — an `ACTIVE` pointer selecting a
  still-`QUARANTINED` generation — is never produced by the workflow;
- the `RETIRING/QUARANTINE -> ACTIVE` resolution publishes and confirms a delayed
  candidate and its discovery projection **before** the CAS; a test suppresses that
  enqueue and asserts no CAS occurs, then asserts the reactivated generation is always
  re-examined without any `block_generations` enumeration. The F10-shaped end state —
  `ACTIVE`, zero references, no candidate, invisible forever — must be unreachable
  through the operator path exactly as it is through the abandoned-use escape;
- resolution authorization is durable before it acts: the work moves `OPEN ->
  RESOLVING` with its `resolution_id`, decision, actor and verification digest, and a
  test asserts no `gc_state` or pointer mutation occurs while the work still reads
  `OPEN`;
- a crash injected between the operator's `gc_state` clear and its pointer step leaves
  the work at `RESOLVING`, and the scanner resumes that exact authorized resolution. A
  test injects the same crash with the work left at `OPEN` and asserts the scanner
  completes the **quarantine** instead — the two states must drive opposite actions
  from identical `gc_state`/pointer rows;
- a `REJECTED` resolution retains `QUARANTINED` and leaves the pointer fenced; a test
  asserts no automated path later returns that pointer to `ACTIVE` or to
  `RETIRED/GC_RETIRE`;
- a writer that observes `RETIRING/QUARANTINE` or `RETIRING/QUARANTINE_ABORT` after
  its own authorization performs no PUT, repair, reuse, or publication, and does not
  enter a bounded poll; the same refusal applies to `RETIRED/QUARANTINE` and
  `RETIRED/QUARANTINE_ABORT`;
- the `RETIRING/GC_RETIRE -> ACTIVE` escape is recovered by its delayed candidate alone; a test
  asserts that no path publishes `gc_generation_zero_ref_by_day` for that branch and
  that the branch is still recovered without one;
- a `DELETE K` that fails permanently never retires its delete work row, never
  regresses `gc_state` out of `DELETING`, never quarantines the generation, and adds
  a DLQ record, a metric and an audit event once its attempt budget is exhausted; a
  test asserts all four, including that the DLQ record is **additive** and
  `gc_generation_deletes_by_day` still names the generation afterwards;
- an escalated delete does not hot-loop: a test asserts `next_attempt_at` moves
  forward and that repeated escalation updates one idempotent DLQ record rather than
  appending one per scanner tick;
- `DELETED` is reached only after re-verifying that the exact object is absent; a
  test injects an ambiguous `DELETE` whose request actually landed and asserts the
  worker re-verifies rather than assuming either outcome;
- the serial-domain invariant is enforced where it can be: startup rejects a session
  serial level other than `SERIAL`, and a **separate test** walks
  the `blocks`-partition inventory and asserts every statement's effective serial
  level is `SERIAL`. A test adds an LWT on `blocks` issued through a raw
  `Session().Query` and asserts the inventory test fails — the invariant must not be
  expressible as a startup check alone;
- the two-DC RF1 regression harness still proves that a whole-DC outage fails a
  reusable-row result that reaches metadata materialization, first materialization,
  and rematerialization because the Paxos majority is both replicas. The production
  acceptance matrix does not generalize
  that result: at three-DC RF1, losing one DC leaves the global majority for the
  first-writer LWT but not the activation commit; at RF3, Paxos v2 may satisfy its
  aggregate activation commit count from the surviving DCs while v1 cannot. The
  `ACTIVE -> RETIRING` `ALL` fence and destructive `EACH_QUORUM` reads fail in every
  whole-DC outage case;
- a `block_generation_references` partition carrying one tombstone per removed
  reference is still readable as one `EACH_QUORUM` partition under the configured
  compaction and `gc_grace_seconds`; a test asserts the destructive read is never
  degraded to `ALLOW FILTERING`, a partial read, or a per-referrer read to work
  around it;
- forcing that read over `tombstone_failure_threshold` creates no evidence and no
  delete, and successfully returns a `GC_RETIRE` pointer to `ACTIVE` once pointer
  LWTs are available, even while the reference partition continues to fail; a
  `QUARANTINE` pointer remains fenced;
- under generation-fence writer mode and a non-`NetworkTopologyStrategy` keyspace, startup
  fails; the process must not run destructive GC where `EACH_QUORUM` reads degrade
  to an ordinary quorum.
- startup also fails when the keyspace DC set differs from the configured
  participating DC set or any expected DC has an RF different from its configured
  positive value.
- the writer-mode gate inspects every generation-fence participant node and fails if
  any reports a `paxos_variant` different from the selected
  `generation_fence_paxos_variant` target; a target-matching bootstrap coordinator
  cannot mask a disallowed replica participant;
- the LWT inventory rejects commit consistency `ANY` and `LOCAL_QUORUM`;
  `legacy`/`gc_grace` purging with the specified `QUORUM`-or-stronger commit levels
  remains eligible, while `repaired` with a
  missing or stale recurring Paxos-repair attestation fails its separate operational
  obligation. A test rejects return to `legacy` after either non-legacy mode;
- a rejoined node remains unverified until that exact node's release/build identity,
  selected `paxos_variant`, and DC/topology membership are verified locally; a new or
  replacement node cannot bootstrap while writer mode is live and follows the full
  topology-maintenance procedure instead;
- changing the selected target from `v1` to `v2`, or from `v2` to `v1`, pauses writer
  mode for the recorded SesameFS transition procedure and re-enables it only after
  every joined Cassandra cluster node matches the new target; the test/runbook records the
   conflict between the pinned release notes and CASSANDRA-21316/current 5.0 guidance and uses
   the later full-repair-first procedure whenever v1 LWT history exists. The reverse
   direction does not require that repair, but topology changes remain prohibited until
   the selected target is uniform v2.

### Cassandra Multi-DC Tests

Use the pinned Cassandra `5.0.9` image with `NetworkTopologyStrategy` and **exactly
three DCs** named `dc-na`, `dc-eu`, and `dc-asia`. Run the safety matrix first at RF 1
per DC and then at RF 3 per DC, with `SERIAL`, not `LOCAL_SERIAL`. Run the protocol
matrix under both semantically accepted Paxos targets, one uniform target per run;
production still selects exactly one. A two-DC harness remains useful regression
coverage but cannot satisfy X2 acceptance because its Paxos majority accidentally
includes both RF1 replicas.

The whole-DC outage matrix is normative, not an instruction to run one representative
case. For every row, run with each of `{dc-na, dc-eu, dc-asia}` withheld in turn and
coordinate from each surviving DC. Also run the no-outage success baseline from all
three coordinator DCs:

| RF per DC | Uniform Paxos target | `SERIAL` phase with one DC withheld | Existing first-writer LWT at `QUORUM` regular commit | Activation requested at `EACH_QUORUM` | Retiring fence requested at `ALL` | Destructive `EACH_QUORUM` reads |
|---:|---|---|---|---|---|---|
| 1 | v1 | succeeds: 2/3 is a majority | succeeds when coordinated in a surviving DC | fails: commit needs all 3 replicas | fails: commit needs all 3 replicas | fail: one DC quorum is absent |
| 1 | v2 | succeeds: 2/3 is a majority | succeeds when coordinated in a surviving DC | fails: aggregate commit count is 3 | fails: aggregate commit count is 3 | fail: one DC quorum is absent |
| 3 | v1 | succeeds: 6/9 exceeds the majority of 5 | succeeds when coordinated in a surviving DC | fails: v1 commit handler needs 2 responses in every DC | fails: all 9 responses are required | fail: one DC quorum is absent |
| 3 | v2 | succeeds: 6/9 exceeds the majority of 5 | succeeds when coordinated in a surviving DC | **succeeds:** the aggregate commit count is 6 and all 6 survivors respond; this grants no per-DC visibility proof | fails: all 9 responses are required | fail: one DC quorum is absent |

Each cell asserts the requested consistency, actual response distribution, returned
result, settled pointer, and whether a writer in the restored DC can observe the new
state at `LOCAL_QUORUM`. The RF3/v2 activation-success cell must not be rewritten as a
guaranteed failure merely because ordinary `EACH_QUORUM` writes are per-DC.

The harness must query `SELECT release_version FROM system.local` on every Cassandra
participant and fail unless `release_version=5.0.9`; it must also verify that every
image digest or Cassandra artifact checksum matches the Phase-0 pinned build identity.
Release version alone is not evidence of the exact audited build.

Required scenarios:

- a reference written at `LOCAL_QUORUM` independently in each of `dc-na`, `dc-eu`,
  and `dc-asia`, with the final GC read coordinated from each DC at `EACH_QUORUM`;
- a pin written in each DC and drained globally;
- `PENDING`, `AUTHORIZED`, and materializer uses are all included in the global drain;
- for each DC in turn, force `ACTIVE -> RETIRING` to be accepted by a Paxos majority
  that excludes that DC, lose the original result, and settle with `SERIAL`; prove
  that no drain begins while a `LOCAL_QUORUM` writer in the omitted DC can still read
  `ACTIVE`, then restore the DC, complete the exact `SERIAL + ALL` reaffirmation, and
  prove that the same writer cannot authorize;
- at RF3 under Paxos v2, withhold all responses from one DC and prove that the
  `SERIAL + ALL` fence cannot succeed even though an aggregate threshold shaped like
  `EACH_QUORUM.blockForWrite()` could be met by the other replicas;
- repeat the omitted-DC shape for a pointer-selected generation's quarantine: an
  aggregate `EACH_QUORUM` commit is demonstrated insufficient, quarantine work stays
  durable while the `RETIRING/QUARANTINE` `SERIAL + ALL` pointer fence fails with the
  DC absent, and restoration allows the fence, use drain, and exact quarantine
  reaffirmation to complete without delete;
- inline G2 activation in each DC is visible to GC, and the exact authoritative
  object is immediately readable from all three application regions without waiting
  for asynchronous bucket replication;
- two concurrent rematerializers in different DCs produce exactly one winner and
  one exact orphan;
- an activation CAS that times out but applied is reconciled as a win, not retried
  into a second generation;
- an activation CAS that remains ambiguous cannot be classified by an ordinary
  `EACH_QUORUM` pointer read; only the subsequent serial settlement can authorize a
  winner/loser branch;
- G2 wins activation, dies before publish, is safely retired, and G3 activates before
  G2 recovery resumes; the lineage walk classifies G2 as a historical winner and
  never as a losing orphan, for both first-life and rematerialization recovery;
- `gc_generation_intents_by_day` and
  `gc_provisional_generation_refs_by_day` written at `LOCAL_QUORUM` in each DC are
  found by the designated scanner at `EACH_QUORUM`; an unavailable source DC holds
  the cursor and performs no cleanup. Separately, the designated intent scanner
  publishes and consumes `gc_generation_zero_ref_by_day` at `LOCAL_QUORUM` in the GC
  DC before retiring the inbound intent row;
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
- an authorized writer publishes after a normal `ACTIVE -> RETIRING/GC_RETIRE`
  transition without requiring an atomic state/reference transaction; the same test
  proves `RETIRING/QUARANTINE` rejects publication;
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
- clock offsets immediately inside and outside `generation_fence_max_clock_skew`
  respectively permit and pause writer mode; stale attestations fail the same way;
- a clock attestation becomes stale after pin authorization but before PUT and the
  writer performs no physical operation; separately, clock health fails after the
  acknowledged retiring fence but before deadline classification and the worker
  appends no evidence and performs no delete until health is restored and the full
  liveness decision is restarted;
- a region whose persisted class resolves to a local asynchronous replica without
  authoritative fallback is rejected by writer-mode eligibility;
- a mixed deployment containing one deterministic-key/legacy-reference writer cannot
  enable writer mode or receive block-write traffic;
- a cutover with an admitted legacy request or a durable legacy publish/repair job
  cannot enable writer mode; after those operations are drained, migrated, or
  cancelled, every eligible node is enabled and verified before traffic reopens;
- add/bootstrap, replace, move, decommission, RF/DC/rack, and token-layout changes are
  rejected while writer mode or destructive GC is live; the maintenance rehearsal
  resumes only after pending ranges/streams clear, required repair completes, and the
  full post-change participant inventory is reverified;
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

This branch changes no Go code, but it is not docs-only: it pins the Cassandra image
across the Compose files and sets `GC_ENABLED=false` in the production Compose. For
the documentation half:

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
`CASSANDRA_SERIAL_CONSISTENCY=SERIAL`, pinned to Cassandra `5.0.9`; the existing
replication script alone is not sufficient evidence.

Also verify that:

- every internal link resolves;
- no document says X1 or X2 is closed;
- `GC_ENABLED=false` remains the operational rule;
- `LOCAL_SERIAL` is not described as final X2 evidence;
- the ADR does not describe unimplemented code as current behavior;
- all proposed table/state names are labeled design unless implemented.

## Rollout And Activation

The greenfield rollout is a coordinated cutover, not a mixed-version rolling write:

1. Bootstrap the final generation-aware schema on a `NetworkTopologyStrategy`
   keyspace. `SimpleStrategy` is not a supported topology for writer mode or
   destructive GC.
2. Close external block-write admission. Drain to completion or cancel every in-flight
   deterministic-key block PUT, materialization, publish, reuse, and repair operation.
   Drain, migrate, or explicitly cancel every durable legacy repair, owner, pending,
   resumable-upload, and publish-work family that could replay a deterministic-key or
   `block_references` write after restart. Keep traffic quiesced throughout cutover.
3. Deploy generation-capable readers and writers everywhere with generation-fence
   writer mode **off**; they may read the empty greenfield schema but may not create
   either deterministic or generation-aware blocks yet.
4. Verify every application writer node, upload funnel, build identity, Cassandra
   participant, authoritative storage target, clock-health attestation, serial domain,
   and selected Paxos target. Prove that no deterministic-key or legacy-reference
   writer remains and that no legacy durable operation can resume. For the greenfield
   deployment, assert zero deterministic physical block keys, zero legacy liveness
   rows, and zero replayable legacy upload/materialization jobs.
5. While writes remain quiesced, enable generation-fence writer mode on every eligible
   writer and verify that each reports enabled. Remove any missing or failed node from
   write routing. This is an admission barrier, not an atomic distributed flag
   transaction. Reopen external writes only after the complete eligible writer set is
   verified.
6. Run dry-run GC with no physical deletion and verify all writer-originated
   projections through their `EACH_QUORUM` scanner path.
7. Run crash, three-DC visibility, RF 1/RF 3, clock-fault, storage-readability, and
   multi-DC outage tests.
8. Close X1/X2 only after every acceptance criterion passes, then activate GC only on
   designated replicas in one DC.
9. Keep all other replicas/DCs at `GC_ENABLED=false`.

There is no rollback to a pre-generation writer after UUID keys are in use. Rollback
must be forward-only: disable destructive GC, deploy a compatible writer, and
preserve generation records and exact keys.

## Cost And Operational Impact

The greenfield scope removes legacy backfill and dual-read work. The table below is
the **original pre-audit rough breakdown**, retained only for provenance:

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

The previously quoted **40-65 engineer-day** rough range (the table's literal line-item
sum is 39-62) and the later **55-75 day** reserve are not accepted
remaining-delivery estimates. They omit separate Phase-0 environment/evidence work,
continuous participant/topology tooling, writer-load and tombstone qualification,
the complete RF1/RF3 fault matrix, operational dashboards/runbooks, rollout rehearsal,
review/rework, and contingency; they also include ADR work already completed. Phase
0b replaces them with a line-item estimate before PR-1 may merge. Until then there is
no approved schedule, and neither historical number may be used for staffing or a
delivery commitment.

“Five to six interactions” is also not a defensible total. The existing-active
sequence alone names at least an initial pointer read, `PENDING` insert, pointer and
generation revalidation, authorization update, positive-predicate/authority check,
generation-bound reference publication, and use release; first materialization adds
three separately confirmed pre-PUT writes, verification, and the first-writer LWT.
Some observations may be reused or safely parallelized, but they remain Cassandra
requests. Phase 0 separately records statement amplification per block, aggregate QPS
at a specified workload/concurrency, and serial critical-path stages per funnel; PR-3
through PR-5 freeze the final query plan.

The fence adds no **new Paxos operation** to those per-block request operations. It
creates one logical activation operation per materializing request only when a
previously retired SHA comes back to life. Concurrent operations for the same block
may retry physical CAS executions when results are ambiguous, but every retry uses
the same generation, epoch, and predecessor tuple. The pre-existing upload LWTs
remain the same operations, but writer mode changes the effective serial domain of
those that touch `blocks` to the selected global `SERIAL` target and requires regular
commit at least `QUORUM`; this changes their multi-DC coordination cost without adding
another LWT.

Cold-path cost is several global reads/LWTs per candidate and intentional dependence
on the slowest participating DC. A DC outage causes retention rather than deletion.
The worker must provision bounded concurrency against a measured target; serial
processing is not an acceptable implicit capacity plan once the per-generation WAN
rounds are introduced.

That statement needs an order of magnitude, or PR-6 will satisfy it with an arbitrary
small number. A classic Cassandra LWT is **four** round trips — prepare/promise, read
of current values, propose/accept, commit — which is exactly the count Paxos v2
halves. `paxos_variant` defaults to v1, and nothing in this repository sets it. A
single WAN RTT cannot model the path: `SERIAL` waits for the required global response
order statistic, ordinary `EACH_QUORUM` waits for a local quorum in every DC, and
`ALL` waits for the slowest replica. The following deliberately applies one scalar to
every phase only as two synthetic sensitivity scenarios for the normal successful
retirement/delete branch (five LWTs and two reads), not as lower/upper bounds:

```text
90 ms sensitivity:
    v1: 5 LWTs x (4 x 90 ms) + 2 reads x 90 ms ~= 2.0 s/generation
    v2: 5 LWTs x (2 x 90 ms) + 2 reads x 90 ms ~= 1.1 s/generation

200-250 ms all-phases synthetic sensitivity for the same simple model:
    v1 ~= 4.4-5.5 s/generation
    actual SERIAL phases may complete from a nearer majority, but ALL and ordinary
    EACH_QUORUM stages still include the slowest required DC/replica

today: gc.batch_size = 100, gc.worker_interval = 30s, and the queue loop in
       internal/gc/worker.go:262 is strictly serial

      100 x 2.0 s = ~200 s per synthetic batch
      100 x 4.4-5.5 s = ~440-550 s per synthetic batch against a 30 s tick
```

Real Cassandra traffic does not reduce perfectly to RTT x rounds, so these are
illustrative scenarios rather than predictions or bounds — which is precisely why Phase 0 measures
`paxos_variant` and the real per-DC latencies instead of reasoning from this table.

So the current worker shape cannot be assumed to drain its own batch. Purging a 1 TB
library at 4 MB blocks is ~262k generations, but no completion time or concurrency of
32 is accepted from this scalar model. Phase 0 measures the real service rate and
selects a supported concurrency while foreground SLOs hold; PR-8 asserts that target.
The arithmetic exists so capacity cannot be treated as a nicety. Paxos variant is one
lever, alongside coordinator placement, bounded concurrency, replica health, and
topology latency.

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

At RF 3 in three DCs that is a few tens of GB over several years — not alarming, but
monotonic and with no reclamation path in this design.

**For X1/X2 the decision is to accept it.** Unbounded logical-history retention is
unconditionally safe, and the alternative is not a cleanup routine — it is another
distributed protocol. A horizon-based collection of the pointer row, its generations
and their evidence "as one unit once every generation is `DELETED` and no reference
or use remains" carries the same admission race this ADR exists to close:

```text
cleanup: global check sees zero uses
                           writer: reads pointer = G1 / RETIRED/GC_RETIRE
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
`RETIRED/GC_RETIRE`; G2 is forbidden during `RETIRING` and while the retained pointer
claim is `QUARANTINE`.

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
  domain and then completes an acknowledged exact-tuple `SERIAL + ALL` visibility
  reaffirmation before any use/reference drain; an ordinary `EACH_QUORUM` read, a
  non-applied reaffirmation, or another ambiguous result never opens the drain;
- every normal `ACTIVE -> RETIRING` claim also commits at `ALL`, so the publication
  frontier does not depend on Paxos v2 preserving per-DC `EACH_QUORUM` response
  distribution at RF greater than one;
- every quarantine has durable discovery before mutation. If G is pointer-selected
  `ACTIVE`, an acknowledged `RETIRING/QUARANTINE` `SERIAL + ALL` fence and zero-use
  `EACH_QUORUM` drain precede `gc_state=null -> QUARANTINED` at `SERIAL + ALL`; an
  ambiguous generation result settles serially and completes an exact-identity
  `SERIAL + ALL` reaffirmation. Every crash resumes from the work/pointer state and
  no quarantine branch writes retirement evidence or deletes;
- the keyspace uses `NetworkTopologyStrategy` and the process refuses to start **in
  generation-fence writer mode** otherwise — not merely to start destructive GC —
  since `EACH_QUORUM` reads degrade silently without it and crashed-activation
  recovery needs the topology from the first generation-aware write. Topology, DC/RF,
  `SERIAL` and `paxos_variant` are one gate and it binds at the same moment; see Two
  Gates, Not One;
- the keyspace DC set exactly matches the configured participating DC set and every
  expected DC has exactly its configured positive RF;
- no node/token/DC/rack/strategy/RF mutation occurs while writer mode or destructive
  GC is live; maintenance drains operations, completes Cassandra streaming and
  required repair, proves no pending ranges, and reverifies the complete post-change
  participant layout before either gate returns;
- topology/RF/DC/rack/strategy/token mutation is permitted only under a uniform v2
  Paxos target; v1 steady-state writer mode is stable-layout-only because Cassandra's
  5.0.9 topology-change linearizability guarantee is v2-only;
- every LWT that can touch a generation-managed `blocks` partition has an **effective
  serial level** of `SERIAL`. The session default is also `SERIAL`, but the criterion
  remains statement-level so a later explicit override cannot silently move one
  `blocks` statement to `LOCAL_SERIAL`. It is enforced by routing those statements
  through one helper and asserted over the inventory by test, because startup cannot
  enumerate them;
- every generation-fence participant node reports the selected
  `generation_fence_paxos_variant` target. For the pinned Cassandra 5.0.9 build the
  semantic candidates are `{v1, v2}`, but PR-0 selects exactly one target; startup
  and continuous participant eligibility reject any node on the other candidate or
  on an unrecognised/disallowed value, since crashed-activation settlement depends on
  the serial `SELECT`. A participant that cannot be verified must be removed from the
  ring/replica topology or keep generation-fence writer mode unavailable;
- no generation-fence LWT commits at `ANY` or `LOCAL_QUORUM`; first-writer and
  otherwise-local `blocks` LWTs commit at least at `QUORUM`. Phase 0 records
  `paxos_state_purging`; selecting either non-legacy mode irreversibly forbids return
  to `legacy`, and `repaired` creates a monitored recurring-Paxos-repair obligation
  even under v1. `repaired` is not itself required by this protocol;
- every application and Cassandra participant has a fresh clock-health attestation
  within `generation_fence_max_clock_skew`; exceeding the bound or losing attestation
  pauses writer mode and destructive GC;
- every application region resolves each persisted storage class to the same
  authoritative target and can read it; a region-local asynchronous replica without
  authoritative fallback is not an accepted `ACTIVE` read path;
- deployment inventory proves that every block writer is generation-capable and no
  deterministic-key or legacy-reference writer can receive traffic before writer mode
  is enabled; every admitted legacy request and durable legacy repair/publish job is
  drained, migrated, or cancelled before the verified admission barrier reopens;
- writer-originated recovery/expiry projections are written at `LOCAL_QUORUM` and
  scanned at `EACH_QUORUM`, so the designated scanner intersects the writer DC; an
  unavailable DC holds the cursor and retires no work;
- the global zero check reads uses before references, and the ordering is asserted
  by a test rather than left to reviewer discipline;
- the publication frontier is stated as an invariant and no second global reference
  check is inserted between the retirement persist and the pointer CAS; its proof
  explicitly depends on the acknowledged `ALL` fence or reaffirmation occurring
  before the use read;
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
  is an explicit `LOCAL_QUORUM` write that preserves the original authority deadline
  and retention boundary;
- pin authority is enforced at authorization and again immediately before any
  physical operation, not only at publish;
- `RETIRING/GC_RETIRE` is never a parking state: it ends on a reference, on one or
  more remaining uses all having expired their authority, on a zero drain, or through
  the retention-safe delayed-candidate reactivation after any liveness-read error
  for `GC_RETIRE`; a `RETIRING/QUARANTINE` claim never uses these exits and remains
  fenced;
- `RETIRED` is terminal for its generation: no transition, automated or
  administrative, returns a `RETIRED` pointer to `ACTIVE` on the same generation, so
  the `DELETING` authorization stays irrevocable once obtained. A `RETIRED/QUARANTINE`
  pointer resolves forward to `RETIRED/GC_RETIRE` under a new claim epoch with fresh
  evidence, never back to `ACTIVE`;
- quarantine resolution records its authorization durably as `RESOLVING` before
  mutating `gc_state` or the pointer, so a crash is classifiable and an unexplained
  `gc_state = null` is never read as an administrative decision. `RESOLVING` also fixes
  the prospective `(Cr, Nr)` so recertification retries are idempotent. `REJECTED` is
  reachable from `OPEN` directly, and from `RESOLVING` only through the state-aware
  abort;
- aborting a resolution persists a durable **abort intent** on the work row first
  under single-assignment (`IF pending_abort_id = null`): immutable logical `A`
  plus a revisable fence attempt carrying the full source authority tuple and
  `(Cf, Nf, Df)`. It then **linearizes on `blocks`**: a `SERIAL + ALL` claim
  takeover installs `QUARANTINE_ABORT` with `retire_abort_id=A` — durable abort
  authority and the revocation of every CAS naming the attempt's source. Only then
  does work move `RESOLVING -> ABORTING`. `abort_scope=POINTER_AND_GENERATION` then
  fences `resolution_epoch` and classifies rollback; `abort_scope=POINTER_ONLY`
  does neither. The intent alone is not authority. Linearization proof is
  `retire_abort_id=A`, not "kind still matches and epoch advanced". An ambiguous
  pointer fence leaves work still `RESOLVING` with the fence **unresolved**. A
  proven pre-linearization claim supersession revises the fence attempt under the
  same `A`. Ordinary `QUARANTINE_ABORT` takeover preserves `retire_abort_id`. An
  ambiguous or unsettleable generation fence leaves `POINTER_AND_GENERATION` work
  `ABORTING` with no rollback. For false-positive abort, one fence is not enough:
  the pointer takeover cannot invalidate the `QUARANTINED -> null` clear or the
  `MATERIALIZING -> VERIFIED` repair, which condition on no pointer column and, by
  correction 68, may not. No path produces a live pointer over a `QUARANTINED`
  generation with terminal work, and none produces a fenced pointer over a cleared
  generation with terminal work;
- the abort classifies **all four** reachable values of `gc_state`, not two. Where an
  authorized delete has already advanced the generation to `DELETING` or `DELETED` —
  a legal ordering, since a pre-quarantine delete authorization survives quarantine
  and recertification — the abort performs no rollback of `gc_state`, materialization,
  storage identity, or delete lifecycle metadata; the abort's monotonic
  `resolution_epoch` fence may already have advanced and is retained; the work
  terminates as `REJECTED` recording `abort_outcome` /
  `abort_observed_gc_state`, leaves the delete work row and the pointer fence
  (`RETIRED/QUARANTINE_ABORT`) untouched, and raises a distinct alert. The later human
  exit is a **new** work identity with immutable `work_kind=SUCCESSOR_AFTER_DELETE`
  and `decision=ALLOW_SUCCESSOR_AFTER_DELETE` at `RESOLVING`, never a reopen of
  terminal `REJECTED`, and never an `OPEN` recovered as "complete quarantine". An
  abandoned successor `OPEN` may terminate as `REJECTED` (declined). A successor
  cancelled after `RESOLVING` is `abort_scope=POINTER_ONLY`: pointer fence under
  source `QUARANTINE_ABORT/A0`, then `ABORTING -> REJECTED` with
  `SUCCESSOR_CANCELLED`, generation untouched — not `OPEN -> REJECTED` and not the
  two-partition abort. Recovery is
  never left in `ABORTING` with no matching branch. Once abort authority has
  linearized, `ABORTING` terminates only as `REJECTED` — there is no
  `ABORTING -> RESOLVED`;
- after quarantine handoff confirmation, `gc_generation_quarantines_by_day` is owned by
  exactly one designated GC DC: every post-handoff `LOCAL_QUORUM` work-state read or
  mutation executes there, and operator requests from other DCs are routed to that
  owner rather than mutating in the caller DC; owner unavailability fails closed with
  no caller-DC fallback;
- `resolution_epoch` is initialized by the `MATERIALIZING` intent, is never written by
  a quarantine transition, and never resets across quarantine cycles, so `Rr` is never
  null and no stale clear can match a reset counter;
- recertification evidence is prepared under the existing `RETIRED/QUARANTINE` fence
  and becomes authoritative only if the pointer CAS installs exactly its prospective
  claim. It is discoverable before then — through its handoff projection and the
  `RESOLVING` work row — but discovery may only settle or retry that exact
  recertification, never authorize a delete or an activation. The check and append are
  never reordered after the CAS, and a `RESOLVING` recertification is settled through
  the predecessor/evidence lineage rather than the live pointer alone;
- aborting a resolution is state-aware and never returns a known-suspect generation to
  `ACTIVE`: rejection is direct when nothing has been undone, restores
  `gc_state = QUARANTINED` first when the generation step already ran, and is not
  available once the pointer step committed;
- every transition that returns a pointer to `ACTIVE` — the abandoned-use escape, the
  liveness-read-error escape, and the `RETIRING/QUARANTINE` resolution — publishes and
  confirms its delayed candidate and discovery projection **before** the CAS, and an
  unconfirmed enqueue forbids the CAS. No path produces an `ACTIVE` generation with
  zero references that no durable work row names;
- an RF3/Paxos-v2 `RETIRING/GC_RETIRE -> ACTIVE` commit may leave an omitted DC safely stale at
  `RETIRING`; writers there return the bounded retryable response until hints, repair,
  or a later LWT converges it, and the stale state never authorizes deletion;
- every recovery cleanup branch confirms the absence of use rows, not only of
  references;
- expired authority cannot publish;
- pins and ordinary references remain `LOCAL_QUORUM`;
- retire acquisition uses `SERIAL + ALL`; drain, final refs, and the remaining
  lifecycle transitions use their specified global levels;
- the GC decision order evaluates errors, then references, then uses;
- an unexpired `PENDING` use keeps `RETIRING` just like an unexpired `AUTHORIZED`
  use for drain purposes, while it still cannot perform physical work;
- a use holding live authority does not reactivate a `GC_RETIRE` generation and never
  clears a `QUARANTINE` claim;
- any generation-bound reference can reactivate a `GC_RETIRE` generation, never a
  `QUARANTINE` claim;
- exactly one liveness source exists: `block_references` is neither read nor
  written after PR-5;
- no unconditional write touches an active-pointer or `retire_claim_*` column;
- the provisional reference TTL exceeds the enforced upload-to-commit bound, and a
  commit that lost its provisional reference fails closed;
- failed global liveness reads never delete or append evidence and attempt the
  delayed-candidate reactivation escape only for `GC_RETIRE`; if the pointer CAS is
  unavailable they retain `RETIRING` only until that CAS can be reconciled, while a
  `QUARANTINE` claim remains fenced;
- a provisional reference cannot park a hot generation in `RETIRING` for its full
  retention TTL;
- writers observing `RETIRING` have a bounded retry/backoff and never hang
  indefinitely, `RETIRED/GC_RETIRE` does not stall a writer, and
  `RETIRED/QUARANTINE` fails closed without allocating G2;
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
- the integration harness asserts Cassandra `release_version=5.0.9` and the image
  digest or artifact checksum matches the Phase-0 pinned build identity on every
  participant;
- exact `dc-na`/`dc-eu`/`dc-asia` RF1 and RF3 tests pass under each semantically
  accepted uniform Paxos target, including a settled majority that omitted one DC,
  visibility reaffirmation, clock faults, authoritative storage reads, projection
  discovery, and each whole-DC outage.

### X1 Closure

X1 is closed only when:

- every new physical lifecycle uses a UUID generation key;
- no key is reused;
- G2 cannot exist before G1 is `RETIRED/GC_RETIRE`; a `RETIRED/QUARANTINE` pointer
  remains fenced until explicit operator resolution;
- G1 and G2 can coexist safely after that point;
- readers use the persisted exact key;
- recovery uses generation and key, never logical hash resolution;
- every ambiguous first-life or rematerialization activation is classified only after
  serial settlement; when the pointer has advanced, an exact lineage/evidence walk
  distinguishes a historical winner from a true loser, and an ordinary
  `EACH_QUORUM` pointer read or incomplete chain never authorizes orphan deletion;
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

1. Cassandra 5.0.9 supports `EACH_QUORUM` reads; old Cassandra 3.0 documentation
   is not the target engine contract.
2. `EACH_QUORUM` belongs in the destructive GC path, not pins, references, or normal
   writer reads.
3. `SERIAL` is the Paxos phase; ordinary `EACH_QUORUM` reads are the per-DC
   destructive-check level. Most lifecycle LWTs use `EACH_QUORUM` as their requested
   regular commit, while first-writer generation-managed LWTs use at least `QUORUM`.
   `ACTIVE -> RETIRING` and pointer-selected quarantine use `ALL` because their
   writer-visibility guarantees must hold under both Paxos v1 and v2.
4. `internal/db/db.go` explicitly configures `cluster.SerialConsistency` in the
   audited branch; critical LWTs should still set it per query.
5. The constraint is scoped to the fence, not to all of SesameFS. The upload path
   already contains several unrelated LWTs (stub repair, identity backfills,
   session slots, head promotion, file locks), so "exactly one upload-path LWT" is
   false and must never be restated. The fence adds no Paxos to pins, references,
   reuse, repair authorization, or ordinary deduplication.
6. A TTL is retention, not writer authority.
7. `pin -> ACTIVE` is invalid for a pin that still holds live authority; any
   generation-bound reference can reactivate during `RETIRING/GC_RETIRE`, while an
   authorized pin can finish publishing without reactivation. A quarantine-kind
   claim is the exception and revokes publication. Refined by correction 37: a
   generation with one or more remaining uses, all of which have expired their
   authority, does return to `ACTIVE`, to release the writer fence rather than to
   assert liveness.
8. G2 is forbidden during `RETIRING` and begins only after `RETIRED/GC_RETIRE`.
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
16. An authorized pin may finish publishing while the pointer is
    `RETIRING/GC_RETIRE`; a `RETIRING/QUARANTINE` claim revokes publication and
    drains the use. A `PENDING` pin cannot publish under either kind.
17. Provisional references reactivate a `GC_RETIRE` generation for availability-safe
    retention; a pin with live authority does not. A `QUARANTINE` claim never
    reactivates automatically. See correction 37 for the expired-authority case.
18. `001_initial_schema.cql` remains immutable; greenfield removes backfill, not
    migration checksum/history requirements.
19. The real physical key preserves the existing two-level hash sharding before
    adding the generation suffix.
20. The acceptance harness must use a pinned Cassandra `5.0.9` image whose digest or
    artifact checksum matches the Phase-0 pinned build identity, and must not treat
    `-short` tests as integration evidence. `release_version=5.0.9` alone is not an
    exact-build proof.
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
`blocks` row alone, including `G1 / RETIRED/GC_RETIRE / E1` and its retire claim ID/epoch,
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
    writer entitled to finish publishing across a normal
    `ACTIVE -> RETIRING/GC_RETIRE`, so the check
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
    round whether or not it applies — so every block invocation that reaches metadata
    materialization pays one round, including a reusable-row outcome. Browser/sync
    preflight can bypass the sequence entirely, and the probe fast path helps only an
    existing complete row, never first content. That is X4, the Paxos whose count
    scales with metadata-materializing block volume. Optimizing the fence's cold path
    while leaving its foreground cost unmeasured is the mistake an earlier revision
    made.
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
63. **An ordinary read does not settle a Paxos proposal, and settlement is not
    visibility.** A serial ballot can be
    accepted without its commit being learned, so an `EACH_QUORUM` read after an
    ambiguous `ACTIVE -> RETIRING` can still return `ACTIVE` while a later LWT
    replays the pending proposal. Draining from that read lets a writer observe
    `ACTIVE` and acquire authority after the fence was actually decided. Ambiguous
    pointer LWTs are settled by `SELECT ... CONSISTENCY SERIAL`; when the settled
    result is the exact `RETIRING` claim, an acknowledged `SERIAL + ALL` identity
    reaffirmation must follow before any use or reference read. This is the one place
    where the generic "reconcile against authoritative state" rule is not strong
    enough: Paxos decision and per-DC base-table visibility are distinct properties.
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
    because the evidence row already is one. The same walk classifies an old
    materializer: if its generation appears in the chain it was a historical winner,
    not a losing orphan merely because the pointer now selects G3 or later.
74. **Evidence is not released by a successor taking the pointer.** An earlier rule
    said it could be, while the reconciliation table simultaneously required that
    same evidence to authorize deleting the predecessor's key. Evidence and
    predecessor tuples are released only together with the generations they
    describe. **Superseded by correction 99**: X1/X2 releases them at no point
    whatsoever, and "once every one of those is `DELETED`" is not a release
    condition either — logical-history compaction is `PURGING_LOGICAL` work.
75. **The claim-column lifecycle is specified for every transition**, not only for
    `RETIRING -> RETIRED`. `retire_claim_epoch` is a monotonic counter that is never
    cleared; `retire_claim_id`, `retire_claim_deadline`, and `retire_claim_kind` are
    live only during `RETIRING` and `RETIRED` and are cleared by the same statement
    that reactivates or replaces the pointer. Clearing the epoch would let a stale
    cycle-N worker match a future cycle.
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
    exact keys. `gc_generation_zero_ref_by_day` is the intent scanner's handoff for a
    materializer that activated but published no reference; it is **not** a backstop
    for `RETIRING -> ACTIVE`, which is owned solely by its delayed candidate made
    durable before the CAS. A full enumeration of `block_generations` is an offline
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
    90 ms sensitivity is ~2.0 s per generation at v1 and ~1.1 s at v2, but that is
    not a topology-independent prediction: `SERIAL` waits for a global response order
    statistic, `EACH_QUORUM` for every DC quorum, and `ALL` for every replica. A
    200-250 ms slow-DC sensitivity raises the simple v1 model to ~4.4-5.5 s, which
    strengthens rather than weakens the concurrency and measurement requirement.
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
     its candidate before writing the delete work leaves `blocks` at `G1 / RETIRED/GC_RETIRE`
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
     the pointer still reads `G1 / RETIRED/GC_RETIRE` the live pointer claim *is* `C1 / N1`, so
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
    first four to `gc.enabled=true` leaves them unenforced for the multiple rollout steps
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
     check and append evidence". For a `RETIRED/GC_RETIRE` pointer that is a safety
     hole: the
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
111. **The Executive Decision sketch is normative too.** It still summarised the path
    to `RETIRED` as "global refs check -> blocks ACTIVE or RETIRED", skipping the
    retirement-evidence append that correction 28 orders before the pointer CAS and
    the handoff publication that correction 100 adds ahead of it. This is correction
    88 recurring, in the one place a hurried reader stops. Neither step is a detail of
    the full sequence: both are ordered before the CAS and neither can be
    reconstructed afterwards, so a summary without them describes a protocol that
    cannot delete safely.
112. **The provisional reference is the fifth projection family, and dropping it
    reopens a closed finding.** Correction 101 replaced the universal "same logged
    batch as the canonical row" rule with a per-family list — and the list contained
    only the then-four recovery families this fence invented, so the inherited one fell out
    silently. Its failure is F10, closed on `main` by PR-8
    ([#144](https://github.com/Sesame-Disk/sesamefs/pull/144)): a reference written
    without its expiry projection TTL-expires on its own, references reach zero with
    no event and no scanner row, and nothing ever creates a candidate. The generation
    is `ACTIVE`, unreferenced and invisible — and the scan that used to catch it is
    exactly what this design removed. The logged batch is PR-5's selected contract
    because it closes partial durable application; correction 121 explains why its
    lack of isolation still requires the due-time guard and exact-reference recheck.
    The ordering-only variant is evaluated but not selected.
113. **A backstop with no publisher is worse than no backstop.**
    `gc_generation_zero_ref_by_day` was listed as covering both a materializer that
    died after activation *and* a generation reactivated out of `RETIRING` on expired
    authority, while the ownership rules gave it exactly one publisher — the worker
    that retires the materialization intent — which never runs on the escape path.
    The escape needs no backstop: correction 97 orders its delayed candidate before
    the CAS and forbids the CAS without it, so the branch is owned outright. Naming a
    second owner that does not exist invites an implementer to treat the first one as
    best-effort.
114. **`DELETING` needs a terminal failure path, not just a retry.** There is no exit
    from `DELETING` except `DELETED` — quarantine conditions on `gc_state = null` and
    cannot be reached — and that is correct, because the authorization is irrevocable
    and the object may already be partly removed. But the document stopped at "retry",
    leaving PR-6 to invent something for a delete that fails forever. The rule is
    bounded backoff, an **additive** DLQ record with a metric and an audit event, the
    delete work row kept and `gc_state` kept at `DELETING`; low-frequency automatic
    retries continue while the operator is alerted. Retrying is always safe because
    `DELETE K` is idempotent and `K` is never reused; dequeuing the work into the DLQ,
    regressing the state, or quarantining are the three shortcuts that strand an
    authorized delete with no owner. "Escalate to the DLQ" normally means "move it",
    which is why the additive part is stated rather than implied. The only state exit
    remains `DELETED`, earned by confirmed exact-key absence; operator remediation can
    make that retry succeed but is not itself a state transition. Two details were
    wrong in the first draft of this rule: `K1` is not
    "retained" while the delete is unresolved — an ambiguous `DELETE` may well have
    reached the object store, so its presence is **unknown until re-verified**, and
    `DELETED` is earned by confirming absence rather than by assuming the last attempt
    failed. And the escalation must stamp `next_attempt_at`/`escalated_at` with an
    idempotent DLQ identity, or the row stays immediately eligible and the scanner
    re-emits the alert every tick, burying the signal it exists to raise.
115. **"Unrelated LWTs keep their consistency" is not a state the code can be in.**
    No per-query serial level exists anywhere in this repository:
    `internal/db/db.go:95` sets `cluster.SerialConsistency` from one config value and
    nothing overrides it. So that allowance and the gate that "rejects `LOCAL_SERIAL`"
    could not both hold. This ADR selects A: a `SERIAL` session default with no initial
    per-query downgrades. The options are asymmetric: A fails safe and costs latency,
    B fails unsafe and costs nothing. Which profiles A actually changes
    must be read from the configs and not assumed: every single-DC profile is already
    `SERIAL`, where `SERIAL` and `LOCAL_SERIAL` coincide and A costs nothing, while
    the genuinely multi-DC `*.cluster.yaml` profiles are on `LOCAL_SERIAL` — those are
    the ones A promotes to cross-DC Paxos, and they are the topology X1/X2 lives in.
    Describing A as "promoting production" gets that backwards.
116. **A DC outage is not confined to rematerialization.** The writer table said
    deduplication "is unaffected and stays regional", contradicting this document's
    own X4 section: `retryUploadedBlockMaterialization` calls `materialize()` on both
    probe outcomes, so a reusable-row result that reaches that helper still reaches
    the first-writer LWT, which the target profile runs at `SERIAL`; preflight may
    bypass the helper entirely. At RF 1 per DC across two DCs a global Paxos quorum is
    both replicas, so losing one fails those metadata-materializing results, first
    materializations and rematerializations alike. But the first draft of this
    correction then over-reached
    into "the availability envelope of the whole upload path is every participating DC
    reachable", which is only true for the two-DC topology it was derived from. See
    correction 120: `SERIAL` and `EACH_QUORUM` do not fail together.
117. **The reference partition needs a tombstone policy, for a different reason than
    the use table.** PR-1 was told to specify compaction and `gc_grace_seconds` for
    `block_generation_uses` and nothing for `block_generation_references`, although
    the destructive proof reads the reference partition **whole** at `EACH_QUORUM`,
    and does so precisely when its live rows are gone and its removals are not. The
    count is `O(reference churn between successful purges)`, not "one per reference
    the generation ever had" — compaction may already have reclaimed earlier ones and
    `gc_grace_seconds` bounds how soon it may; the first draft over-claimed. A read
    that trips `tombstone_failure_threshold` is not slow, it is an error. The corrected
    fail-closed rule writes a delayed candidate and attempts `RETIRING -> ACTIVE`:
    uncertainty retains bytes and prevents evidence, but no longer makes the failing
    reference read the only exit from the writer fence. Compaction/repair remains
    required to reclaim the generation; the safe escape converts permanent write
    unavailability into retention.
118. **The deprecated symbol is the type, not the method.** In
    `apache/cassandra-gocql-driver/v2` the deprecation applies to
    `type SerialConsistency = Consistency`; `Query.SerialConsistency(...)` is current
    API that configures only a conditional statement's serial phase. The earlier
    wording was literally correct and one word away from being read as "the setter is
    obsolete", in a section whose whole purpose is to stop someone from using that
    setter on a `SELECT`. It also panics on a non-serial value, which matters once a
    serial level comes from configuration.
119. **The acceptance criteria must not re-narrow a gate the design already widened.**
    The X2 list still tied `NetworkTopologyStrategy` to "refuses to start destructive
    GC", multiple rollout steps after correction 105 established that the four
    topology/serial assertions bind from the first generation-aware write. A criteria
    list is what an implementer checks against, so a gate stated narrowly there
    survives however carefully the body states it broadly. `paxos_variant` was missing
    from the enumerated "implementation must" list for the same reason. Correction 109
    left the same kind of residue in three places: two verification cases and a row in
    the recovery-projection table still named the materializer-use projection it had
    removed, so the table then promised five projections where the schema defined
    four. The later fifth `gc_generation_quarantines_by_day` is a different, durable
    workflow required before pointer/generation quarantine mutations. A removal or
    addition is not complete until every enumeration is recounted.
120. **`SERIAL`, ordinary `EACH_QUORUM`, and Paxos v2's requested commit level do not
    have one availability envelope.** An ordinary `EACH_QUORUM` read requires a
    quorum in every DC. Cassandra 5.0.9 Paxos v1 uses the ordinary per-DC commit
     handler, but Paxos v2 reduces the requested commit level to an aggregate response
    count, so an RF3 activation may succeed with one DC absent. `SERIAL` itself
    requires a majority of **all** replicas, which one lost DC may
    or may not break: at 2 DCs it always does, at 3 DCs × RF1 it does not. So the
    first-writer LWT — and therefore first materialization and today's dedup — has a
    topology-dependent envelope that must not be stated as "every DC". A property
    derived from the two-DC test profile and frozen as a general one is exactly the
    class of error this document keeps catching in the other direction; the assumptions
    already say production reasoning must work at higher RF, and it must work at more
    DCs too. No safety proof relies on the activation commit having per-DC
    distribution; the one pointer transition that needs that property,
    `ACTIVE -> RETIRING`, commits at `ALL`.
121. **Ordering does not substitute for a logged batch unless the consumer cannot yet
    act.** "Confirm the projection, then write the canonical row" reads like a
    universal replacement for atomicity, and for the GC-owned workflow families it is, because
    nothing consumes those projections until the branch is due. The provisional
    reference is different: its scanner deletes a projection whose canonical row is
    absent, so a scanner that could run between the two writes would delete the
    projection and reopen F10. What saves it is that the row is scheduled a full TTL
    ahead, the scanner skips rows that are not due, and it re-checks the exact
    reference before deleting a due projection
    (`internal/gc/scanner.go:265-268`) — a precondition, not a property of ordering.
    Any future family that adopts the ordering shape has to state the same thing or
    use a batch. PR-5 does not take the partial-application risk: this ADR freezes the
    existing logged batch for the provisional-reference family, while retaining both
    consumer guards because Cassandra batches are atomic but not isolated.
122. **A statement-level invariant cannot be a startup assertion.** "The gate asserts
    the effective per-statement serial level for the inventory" is not implementable:
    nothing at boot can enumerate the consistency level of every LWT written in Go, so
    the phrasing would have been satisfied by the config-string check it was written
    to replace. Split it — startup checks configuration and topology, one helper
    carries the level for every `blocks`-partition LWT, and tests plus writer-path
    tracing assert the inventory. This is the technique the org-scoped-key series used
    when it deleted the global block APIs: make the compiler and the helper hold the
    invariant so a reviewer does not have to.
123. **The Implementation Split is read instead of the body, so a rule fixed in the
    body is not fixed.** PR-2 still demanded the impossible startup check that
    correction 122 had just removed, and PR-1 still carried the "one tombstone per
    reference the generation ever had" wording that correction 117 had just replaced.
    Both were written before the corrections and neither was revisited, which is
    correction 88's rule applied to a section that had not been treated as normative
    prose. It is: an implementer opens the PR list first and rarely reads back up.
124. **A profile is not a topology.** "In the current two-DC profiles a whole-DC
    outage blocks the first-writer LWT" was false as written, because those profiles
    run `LOCAL_SERIAL` today — the exposure appears only once generation-fence writer
    mode forces `SERIAL` on the `blocks` partition. Availability claims have to name
    both the replica topology **and** the consistency profile; naming one and implying
    the other is how the previous over-generalization got in.
125. **A gate against a silent failure is an allowlist, not a deny-list.** The
    `paxos_variant` check was written as "reject `*_without_linearizable_reads*`",
    which admits every variant the engine has not shipped yet — and the property it
    guards fails silently, so an unrecognised value would be assumed safe. It becomes
    a positive allowlist recorded by PR-0, for the same reason generation validity is
    a positive predicate rather than "not `QUARANTINED`" (correction 72).
126. **The `pub:` reference is the liveness holder for the publish race, not
    bookkeeping.** The commit persists the `fs_object` row before its permanent `fs:`
    references exist (`internal/api/v2/fs_helpers.go:1022-1031`), so for that interval
    an fs_object is visible while nothing permanent holds its blocks. The current code
    already answers this with staged publish-attempt references; the ADR named `pub:`
    in the identity list without stating the ordering that makes it load-bearing.
    Stage `pub:` → persist `fs_object` → promote `fs:` → release `pub:`.
127. **`retire_claim_deadline` is takeover eligibility, not a hard expiry.** "Every
     GC-owned transition must be within the valid ownership deadline" reads as a fourth
     `IF` condition, which Cassandra cannot express and which correction 93 already
     resolved for the activation CAS. An expired-but-uncontested claim is still valid
     and its owner's transitions still apply; what invalidates a stale worker is the
     takeover LWT bumping `retire_claim_epoch`. The rule survived in the body of Retire
     Ownership even after its own section explained why it could not exist.
128. **Startup can check the gate's cluster facts, but not every Go statement.** The
     Implementation Split said "only the first is checkable at boot" after listing
     topology, DC/RF, `paxos_variant`, and the session default. The first three
     cluster facts plus the configured default are startup-checkable; only the
     effective serial level of every `blocks`-partition statement requires the helper
     and inventory tests. The distinction must remain explicit in the normative split.
129. **`paxos_variant` is node-local, so its target is participant-wide.** A positive
     allowlist is not enough if startup inspects only its bootstrap coordinator. Every
     Cassandra node that can coordinate a generation-fence statement or own a replica
     for the generation-fence keyspace must report the one selected target. For the
     pinned 5.0.9 build, PR-0 selects from `{v1, v2}`; one disallowed participant fails
     the writer-mode gate.
130. **A liveness handoff needs confirmation on both sides.** The `pub:` ordering
     closes the visible `fs_object` window, but ambiguous writes must not be treated as
     success: confirm `pub:` before persisting `fs_object`, confirm generation-bound
     `fs:` before releasing `pub:`, and preserve the holder on either uncertainty.
131. **The rematerialization summary must describe operations, not Paxos round count.**
     The writer-side path performs the retirement-evidence `EACH_QUORUM` read followed
     by the `SERIAL + EACH_QUORUM` activation CAS. Calling this "one global round"
     obscures both the read and the implementation-dependent Paxos round trips.
132. **An additive DLQ does not make the operator the only recovery actor.** After
     escalation, `next_attempt_at` continues low-frequency idempotent retries while
     the operator is alerted. Operator remediation may help, but only confirmed
     exact-key absence permits `DELETING -> DELETED`; neither the DLQ nor remediation
     changes ownership or state by itself.
133. **A semantic allowlist is not a mixed-deployment policy.** `{v1, v2}` identifies
     the Cassandra 5.0.9 variants that retain the required read semantics, but a
     generation-fence deployment selects exactly one
     `generation_fence_paxos_variant`; every participant node — coordinator or
     replica — must match it. Changing that target is an explicit rolling operational
     procedure, not an implicit consequence of two values being semantically
     acceptable, and writer mode is paused during the mixed state.
134. **Node verification is continuous, not a T0 snapshot.** A rejoined node is
     unverified until its local release/build identity, selected Paxos variant, and
     DC/topology membership are verified. A genuinely new/replacement node cannot be
     made safe by checking it after Cassandra assigns pending ranges; joining is a
     drained topology-maintenance operation. If a node can own a generation-fence
     replica, excluding it only from coordinator selection is insufficient.
135. **The Executive Decision must distinguish new from repurposed coordination.**
     The fence adds no new LWT to ordinary writer operations, but its writer-mode gate
     intentionally moves every existing `blocks`-partition LWT into the `SERIAL`
     domain. New global operations are limited to lifecycle/ambiguity recovery,
     rematerialization, and the GC cold path.
136. **Release version is not exact-build identity.** The Cassandra 5.0.9 acceptance
      harness must assert both `release_version=5.0.9` and a pinned image digest or
     artifact checksum. The version query alone cannot prove the audited build.
137. **The publication wording names one holder, not two releases.** The `pub:` to
     `fs:` handoff says no liveness holder is released after an ambiguous publication
     write; the holder remains until the exact generation-bound reference is confirmed.
138. **Serial settlement does not establish a writer-visible fence in every DC.** In
     three DCs, `SERIAL` can resolve an accepted `RETIRING` proposal through a global
     majority while the omitted DC still serves `ACTIVE` at `LOCAL_QUORUM`. Draining
     from that state destroys publication-frontier rule 4. A normal claim commits at
     `ALL`; an ambiguous claim first settles serially and then reasserts the exact
     tuple at `SERIAL + ALL`. No acknowledged barrier means no drain.
139. **Only a serially settled activation may be classified.** Retrying an ambiguous
     activation CAS and then reading the pointer at ordinary `EACH_QUORUM` still does
     not prove that no proposal remains pending. The request and recovery paths use the
     same rule: `SELECT ... CONSISTENCY SERIAL` first; ordinary global reads may only
     inspect lineage/evidence afterwards.
140. **Another active generation is not proof that this materializer lost.** G2 may
     have won, retired, and been superseded by G3 before old recovery wakes. A complete
     evidence/predecessor walk containing G2 proves a historical winner and routes it
     through normal lifecycle cleanup. Only a complete chain that excludes G2 proves a
     losing orphan; a broken chain fails closed.
141. **Local projection publication needs a global-intersection reader.** Writer hot
     paths keep materialization and provisional-expiry projection writes at
     `LOCAL_QUORUM`, but the designated scanner reads those families at
     `EACH_QUORUM`, holds its cursor on any unavailable DC, and retires nothing on an
     uncertain scan. GC-owned projections remain local to the designated GC DC.
142. **Pin authorization is a write, not prose between two steps.** The main writer
     sequence and consistency table explicitly include `PENDING -> AUTHORIZED` at
     `LOCAL_QUORUM`, preserving the original deadline and remaining TTL and confirming
     an ambiguous result by the full authority tuple.
143. **Variant-transition policy is version-pinned because upstream guidance
     changed.** Cassandra 5.0.9 `NEWS.txt` says v2 may be enabled at any time and its
     sequence omits a one-time repair. CASSANDRA-21316 added a full primary-range
     repair before `v1 -> v2` to 5.0.9/current 5.0 configuration guidance. SesameFS
     follows the later stricter sequence whenever v1 has carried LWT history, while a
     greenfield cluster selects its target before the first LWT. The reverse rolling
     change does not require the same repair.
144. **Clock skew is an eligibility fact.** Deadline and TTL proofs are valid only
     while every application/Cassandra participant has a fresh attestation inside the
     enforced maximum offset/drift selected and validated by Phase 0. Missing or out-of-bound evidence pauses writer mode
     and destructive GC; “NTP required” without enforcement is not a contract.
145. **`ACTIVE` names an authoritative globally addressable target, not completion of
     asynchronous S3 replication.** Every application region resolves the persisted
     class to the same authoritative bucket or has mandatory fallback to it. The
     generation-fence harness must test immediate cross-region readability rather than
     wait for a local replica and call that activation safety.
146. **Greenfield does not make mixed writers safe.** All code is deployed with writer
     mode off under quiesced writes, legacy writers are excluded, and only then is
     generation mode enabled fleet-wide. Verifying old-writer absence after new writes
     begin is too late because the two liveness schemas are intentionally not co-live.
147. **Two DCs cannot prove the three-DC fence.** At RF1 a two-DC Paxos majority
     includes both replicas and hides the omitted-DC settlement bug. Acceptance uses
     exactly `dc-na`, `dc-eu`, and `dc-asia` at RF1 and RF3 and forces a majority that
     excludes one region.
148. **Paxos v2 requested `EACH_QUORUM` is not the ordinary per-DC write handler.** In
     Cassandra 5.0.9, `PaxosCommit` uses one aggregate required/accepted count, unlike
     `DatacenterSyncWriteResponseHandler`. RF1 masks the difference because the summed
     threshold equals `ALL`; RF3 does not. The cold writer fence therefore uses `ALL`
     while ordinary destructive reads remain `EACH_QUORUM`.
149. **A liveness-read error retains bytes, not a permanent writer fence.** The worker
     publishes a delayed candidate and attempts `RETIRING -> ACTIVE`; it writes no
     evidence and performs no delete. If the pointer CAS is unavailable the fence
     remains only until global coordination returns, not until an unreadable reference
     partition becomes readable.
150. **A frozen ADR cannot delegate safety-affecting choices to implementation PRs.**
     The session default is `SERIAL` (option A), generation-aware claims cannot create
     stubs, the named dedicated recovery projections are mandatory, and PR-5 preserves
     the logged batch for provisional reference plus expiry work. Measurements tune
     parameters and capacity; they do not select among protocol shapes.
151. **Zero-reference work is GC-owned even though the intent feeding it is not.** A
     materializer in any DC publishes `gc_generation_intents_by_day`, so that scanner
     reads at `EACH_QUORUM`. Only the designated intent scanner publishes
     `gc_generation_zero_ref_by_day`; that outbound family is created and consumed at
     `LOCAL_QUORUM` in the GC DC.
152. **Neither side of conflicting upstream Paxos guidance may be stated as
     universal.** The pinned 5.0.9 release notes permit enabling v2 at any time;
     5.0.9/current 5.0 docs prescribe full repair first. The production contract takes
     the conservative intersection: if v1 LWT history exists, repair every primary
     range on every node before v2; if the cluster is greenfield, select the uniform
     target before its first LWT. `paxos_state_purging=repaired` is a separate choice
     tied to recurring Paxos repair and weaker commit optimization, not a prerequisite
     for this `QUORUM`-or-stronger LWT protocol.
153. **Acceptance is a four-cell matrix, not the phrase “under both variants.”** RF1
     and RF3 run under uniform v1 and v2, every DC is omitted in turn, and every
     surviving DC coordinates. RF3/v2 must demonstrate that activation at requested
     `EACH_QUORUM` succeeds from six aggregate responses while the `ALL` retiring
     fence and ordinary destructive `EACH_QUORUM` reads fail.
154. **Quarantine is a writer fence when the pointer still selects the generation.**
     Paxos v2 can acknowledge requested `EACH_QUORUM` from two RF3 DCs while writers
     in the third still read `gc_state=null`. `null -> QUARANTINED` therefore commits
     at `SERIAL + ALL`; ambiguity settles the generation partition serially and then
     reaffirms the exact quarantine identity at `SERIAL + ALL`. `DELETING/DELETED`
     remain `EACH_QUORUM` because the pointer's earlier `ALL` fence already made them
     unacquirable.
155. **Replica topology is stable protocol input, not live background churn.** Adding,
     replacing, moving, or removing nodes and changing tokens, DC/rack, strategy, or
     RF are maintenance operations. Writers and GC pause and drain before Cassandra
     enters pending-range/streaming states; required repair and complete post-change
     participant verification finish before either gate returns.
156. **Quiescing new traffic does not drain old work.** Cutover separately waits for
     or cancels admitted legacy requests and drains, migrates, or cancels every durable
     legacy repair/publish/upload job that could replay old-schema writes. Writer mode
     is enabled and verified on all eligible nodes while traffic remains closed; no
     atomic distributed flag transaction is assumed.
157. **A stale reactivation is restrictive.** At RF3/Paxos v2,
     `RETIRING -> ACTIVE` may commit from six aggregate responses while one DC still
     reads `RETIRING`. That DC rejects writes until hints, repair, or another LWT
     converges it. This is documented availability loss, not a reason to put `ALL` on
     a transition whose stale direction is safe.
158. **A correct protocol still needs a feasibility decision.** Phase 0 is split into
     measurements and an explicit go/no-go before PR-1 merges. It budgets foreground
     first-writer/global-`SERIAL` latency, per-funnel CQL QPS and concurrency,
     tombstones, GC service rate, and a new delivery estimate. The old 40-65 and 55-75
     engineer-day figures are provenance, not approved plans.
159. **S3 version IDs and longer waiting are not portable identity proofs.** Version
     IDs would require uniform backend versioning plus persisted version-aware
     operations everywhere; no finite delay bounds an already accepted delete or
     outage. Never-reused UUID keys close X1 independently of both assumptions.
160. **Operator reactivation is a pointer return, so correction 97 binds it too.**
     Adding the quarantine claim kind created a new path back to `ACTIVE` that did not
     carry the delayed-candidate rule. A generation can be quarantined after its
     references already reached zero, so the resolution returns an `ACTIVE` generation
     with `refs == 0` and no pending reference-removal event to ever re-create its
     candidate: `ACTIVE`, unreferenced, named by no work row, invisible to a recovery
     protocol that has forbidden scans — the exact state correction 97 exists to make
     unreachable, reached through a path correction 97 did not name. The
     `RETIRING/QUARANTINE -> ACTIVE` resolution now publishes and confirms the delayed
     candidate and its projection before the CAS, unconditionally rather than after
     reading a reference count. This is correction 88's rule applied to a state machine
     change: when a transition is added, every ordering invariant stated over the
     transitions it resembles is part of that change.
161. **A frozen ADR that changes protocol needs a revision line.** The claim kind, the
     two quarantine pointer states, and the `QUORUM` first-writer commit are protocol
     changes made after the document was first labelled frozen. "Last updated" does not
     tell an implementer which contract they read. Freeze is a statement about the
     protocol, so a protocol change re-dates it explicitly.
162. **`RETIRED` is terminal, and the invariant must be stated over `RETIRED`, not over
     `RETIRED/GC_RETIRE`.** The first draft of the quarantine claim kind added
     `RETIRED/QUARANTINE -> ACTIVE` as an administrative escape, which falsified the
     one property the `DELETING` CAS depends on. That CAS deliberately does not re-read
     the pointer; its whole safety argument is that authority can never be reacquired
     on a generation that reached `RETIRED`. The escape was two hops —
     `RETIRED/GC_RETIRE -> RETIRED/QUARANTINE -> ACTIVE` — so no single transition
     looked wrong, and the irrevocability paragraph, scoped to one claim kind, still
     read as true. A delete worker that obtained a valid proof and stalled would then
     delete `K1` under a live reference published after the reactivation: live-data
     loss, and the exact failure class X1 exists to close, reintroduced by an
     administrative convenience. Adding a pointer re-read before the `DELETING` CAS
     does not fix it, because the pointer and the generation row are different tables
     and the reactivation can land between the two statements. The invariant is the
     mechanism. `RETIRED/QUARANTINE` therefore resolves *forward* to
     `RETIRED/GC_RETIRE` — a recertification with a new claim epoch, a fresh global
     zero check, and fresh evidence — and never back to `ACTIVE`. Only
     `RETIRING/QUARANTINE`, which never reached `RETIRED` and for which no delete
     authorization can exist, resolves to `ACTIVE`. When an escape hatch is added to a
     terminal state, the proofs that depend on its terminality are part of the change.
163. **An authorized resolution must be durable before it acts.** With work states
     `OPEN | RESOLVED | REJECTED`, a false-positive resolution that cleared `gc_state`
     and then crashed left `work=OPEN`, `gc_state=null`, `pointer=.../QUARANTINE` —
     byte-for-byte identical to an ordinary quarantine that has fenced the pointer and
     not yet written `QUARANTINED`. The two demand opposite actions, and no scanner can
     tell them apart without inferring administrative authorization from an unexplained
     mutation, which the fail-closed rule forbids. `OPEN -> RESOLVING` is confirmed
     before any `gc_state` or pointer mutation and records `resolution_id`, decision,
     actor, verification digest and `started_at`, making every crash point decidable.
     This is Discoverability Before Irreversibility applied to a decision rather than
     to a row: the record that a decision was made outlives the process that made it.
164. **A published tag is checkable; assert it rather than inferring from a release
     page.** A review of r2 reported that `cassandra:5.0.9` did not exist and that the
     Compose pin was broken. The official image publishes `5.0.9`, `5.0.9-bookworm` and
     `5.0.9-trixie`, verified 2026-08-12. Recording the verification date here keeps the
     next reviewer from repeating the check against stale mirror data — and does not
     weaken the standing requirement that Phase 0 pin and verify a digest, since a
     version tag is still mutable.
165. **Recertification evidence is prospective, and that is an exception that must be
     named.** Correction 162 introduced `RETIRED/QUARANTINE -> RETIRED/GC_RETIRE` with
     its zero check and evidence append ordered before the CAS. That inverts the rule
     holding everywhere else — claim first, then the holder earns its evidence — because
     here the CAS *is* what installs the claim. Describing the check as running "under
     the new claim" was simply false: the live claim is still the quarantine one, and
     `(Cr, Nr)` protects nothing until the CAS applies. The order cannot be fixed by
     inverting it: once the pointer reads `RETIRED/GC_RETIRE`, activation and delete
     authorization both look for `evidence(G1, Nr)`, and its absence drives G1 into the
     permanent fail-closed branch whose only other exit is the synthetic evidence
     correction 109 forbids. So the semantics change instead of the order: the **old**
     `RETIRED/QUARANTINE` fence protects the check, `(Cr, Nr)` is prospective and fixed
     by the `RESOLVING` record, and the evidence becomes authoritative only if the CAS
     installs exactly that pair. A prospective row is **discoverable but not
     authoritative**: the recertification publishes its handoff projection before the
     CAS and the `RESOLVING` work row carries the prospective epoch, so a scanner can and
     must be able to find it — that is what recovers a crash between the append and the
     CAS. Before the CAS is proven applied, that discovery may only settle or retry the
     exact recertification; it may never authorize `DELETING`, publish delete work, or
     permit activation. Calling the row "inert" or "unreachable" was wrong and would have
     sent an implementer looking for a discovery path that must exist. `(Cr, Nr)` must
     also be deterministic across
     retries: the append is `INSERT ... IF NOT EXISTS` on `(…, retire_claim_epoch)`, so a
     retry that allocated a fresh claim ID would collide with its own first attempt and
     fail closed forever. Finally, recertification does not revoke a delete
     authorization already obtained under an earlier claim — requiring a re-match would
     reintroduce the revocability correction 162 removed.
166. **Aborting a resolution is state-aware, and "finish it then re-quarantine" is a
     fail-closed violation.** The first draft of this rule justified forbidding
     `RESOLVING -> REJECTED` by claiming `RESOLVING` implies `gc_state` was already
     cleared. That contradicts the definition one paragraph earlier: `RESOLVING` is
     confirmed *before* any mutation, and it spans three situations — nothing undone,
     generation step done, pointer step committed. The conclusion was also wrong. Telling
     an operator who has just confirmed the contradiction to "complete the authorized
     resolution and then raise a new quarantine" would clear `gc_state` and return a
     known-suspect generation to `ACTIVE`, opening a window for a writer to pin,
     authorize and publish before the second quarantine lands — fail-open in the one
     workflow that exists because the protocol could not prove something safe. The abort
     therefore reads canonical state: reject directly when nothing has been undone,
     restore `gc_state = QUARANTINED` at `SERIAL + ALL` first when the generation step
     already ran, and only when the pointer step has already committed is there nothing
     left to abort — then the resolution finishes and a **new** quarantine is raised
     through the ordinary fenced path. If the abort cannot complete, the work stays
     `RESOLVING` and fenced. **Superseded in mechanism by corrections 168, 170, 173 and
     174**: "reads canonical state" is not enough, because an ordinary read cannot see a
     live resolver or an accepted-but-unlearned pointer proposal; one fence is not
     enough, because a resolution writes two partitions; the abort needs its own durable
     `ABORTING` record, because otherwise its intermediate state is indistinguishable
     from an ordinary claim takeover; and "the ordinary fenced path" is not one path.
     The three state-aware branches survive unchanged — what changed is everything that
     must happen before them.
167. **A `RESOLVING` recertification is settled by lineage, not by the live pointer.**
     G2 may activate the instant the recertification CAS commits, so a coordinator that
     dies before marking `RESOLVED` leaves recovery looking at `G2` or `G3` rather than
     `G1 / RETIRED/GC_RETIRE`. Reading "G1 is no longer on the pointer" as "the
     recertification failed" would retry a CAS that already applied and, worse, invite a
     second prospective claim. The walk decides it: a predecessor/evidence link naming
     exactly `(G1, E1, Cr, Nr)` proves the CAS committed, because only a committed CAS
     can put that pair into a lineage link. This is correction 140 in operator-resolution
     vocabulary, and it adds no new proof obligation — only the requirement to use the
     walk.

168. **A rollback must fence what it is rolling back.** The r2 abort classified from an
     ordinary read — "`gc_state` already null, pointer still fenced" — and then restored
     `gc_state` and marked the work `REJECTED`. Nothing in that sequence invalidates the
     resolver whose work is being undone, and there are two of them. A **live** resolver
     that already observed `RESOLVING` issues a pointer CAS whose `IF` clause names only
     `(G, active_epoch, active_state, Cq, Nq, QUARANTINE)`, none of which the rollback
     touches, so it applies afterwards. A **dead** resolver's pointer LWT may have been
     accepted by part of the Paxos cohort and never learned, in which case the abort's
     ordinary read returns the fenced state and a later LWT replays the proposal. Both
     produce a live pointer over a `QUARANTINED` generation with terminal `REJECTED`
     work — the exact state this workflow says it never produces, now with no `RESOLVING`
     record left to classify it, so the block is wedged with no automated exit. This is
     correction 63 and correction 139 applied to an administrative decision: an ordinary
     read never settles a Paxos proposal, and settlement is not the same as revocation.
     The abort therefore settles serially, then takes over the quarantine claim at
     `SERIAL + ALL` so every CAS naming `(Cq, Nq)` fails, and only then rolls back. The
     takeover epoch must exceed any prospective epoch fixed by the `RESOLVING` row, or
     the fence itself would create the epoch-match/claim-ID-mismatch that correction 89
     defines as a protocol violation.
169. **A criteria list that contradicts the body makes the suite unsatisfiable.** After
     correction 166 introduced the state-aware abort, the verification plan still carried
     "`REJECTED` is reachable only from `OPEN`; a test asserts a `RESOLVING -> REJECTED`
     attempt does not apply", and the X2 closure list repeated it. Two normative tests
     sat eleven lines apart, one asserting the transition applies and one asserting it
     does not. This is correction 119 and correction 123 recurring in the section they
     were written about: an implementer checks against the criteria, so a rule fixed only
     in the body is not fixed. The rule is that `REJECTED` is reachable from `OPEN`
     directly and from `RESOLVING` only through the complete settled-and-fenced abort;
     what the test must reject is a *bare* transition that skips settlement, the fence,
     or the restore its situation requires.
170. **"Raise a new quarantine" names three different transitions.** The post-commit
     abort branch said "through the ordinary `ACTIVE -> RETIRING/QUARANTINE` path", which
     is only correct when the committed resolution was a `RETIRING/QUARANTINE -> ACTIVE`
     reactivation. After a **recertification** the pointer reads `RETIRED/GC_RETIRE` and
     G1 is not `ACTIVE` at all, so that form cannot apply — leaving the generation
     unfenced while delete authorization and G2 activation stay enabled, which is the
     opposite of what an abort is for. And once a **successor** owns the pointer, G1 can
     no longer be selected by any activation, so it needs no pointer fence and has none
     available; the generation-row quarantine form is the whole mechanism. The branch now
     enumerates all three and points at the existing forms rather than inventing one.
171. **A quarantine that cannot be resolved is not fail-closed, it is a wedge.** The
     false-positive `QUARANTINED -> null` clear conditioned on
     `materialization_state = VERIFIED`, while the quarantine statement that creates the
     state deliberately uses `storage_key` as its identity guard *because* a
     `MATERIALIZING` generation is one of the things worth quarantining. So the one class
     the operator workflow most needs — a materialization quarantined on an object that
     later proves out — had an `IF` clause that can never apply. This is correction 68's
     unsatisfiable-condition defect on a different statement. The clear matches the
     generation/key and quarantine operation/evidence identity instead; where
     re-verification proves the object, the marker is repaired to `VERIFIED` in its own
     write first, exactly as `VERIFIED` Lag Is Not A Contradiction already prescribes.
172. **Columns named only in prose are not schema.** `next_attempt_at` and
     `escalated_at` carry the entire anti-hot-loop argument of When The Physical Delete
     Keeps Failing, and a verification case asserts `next_attempt_at` moves forward and
     that repeated escalation updates one idempotent DLQ record — but neither column, nor
     any DLQ identity, appeared in the `gc_generation_deletes_by_day` shape or in PR-1's
     freeze list. Correction 92 established that recovery projections are schema rather
     than named concepts, and correction 150 that a frozen ADR does not delegate
     safety-affecting shape to implementation PRs; both apply here. The delete projection
     carries its retry/escalation columns explicitly, and the DLQ record is keyed
     `(org, block, generation, escalation)` so a repeated escalation updates one row.

173. **The decision to stop is a decision, and it must outlive the process that made
     it.** Correction 168 gave the abort a settlement and a fence but no durable record
     of its own. After the fences the work still read `RESOLVING` with
     `decision = FALSE_POSITIVE`, a fenced pointer, and a changed claim — which is
     *also* what an ordinary `retire_claim_deadline` takeover of a genuinely live
     resolution looks like, since takeover preserves the kind and is expected to be
     followed by the resolution continuing. A scanner resuming that row does what
     `RESOLVING` tells it to: it continues the resolution the operator just abandoned,
     re-reading the live claim so the abort's own fence becomes the claim it matches.
     The durability requirement stands. Correction 181 moves the *first* authoritative
     abort act onto `blocks` as `QUARANTINE_ABORT` / `retire_abort_id`; `RESOLVING ->
     ABORTING` then adopts that identity so a crash after the pointer fence is not
     mistaken for a continuing resolution. An `ABORTING` row is resumed only as an
     abort. This is correction 163 applied one level down: the authorization to undo
     needs the same durability as the authorization to act.
174. **A resolution touches two partitions, so an abort needs two fences.** Correction
     168 fenced the pointer, which invalidates every statement naming the quarantine
     claim — but the `QUARANTINED -> null` clear names none of them, and correction 68
     forbids it from naming them, because nothing writes `retire_claim_*` to
     `block_generations`. A resolver stalled just before its clear therefore survived
     the fence: the abort saw `gc_state` still `QUARANTINED`, took the "nothing to
     undo" branch, wrote `REJECTED`, and the resolver then cleared the quarantine into
     a terminal work row. Fenced pointer, `gc_state = null`, no contradiction recorded,
     nothing driving the state — correction 168's wedge reached through the other
     partition. The generation row gets its own monotonic `resolution_epoch`, fixed by
     the `RESOLVING` record, named by the clear, and bumped by the abort in **both**
     rollback branches. It is not a correction-68 violation for the reason correction
     68 states: the column is written by this table's own transitions, so the condition
     is satisfiable. Whenever a fence is added, count the partitions the fenced
     operation writes to.

175. **A counter the prose initializes is not initialized.** Correction 174 introduced
     `resolution_epoch` and said the `null -> QUARANTINED` transition sets it to 0 —
     but the canonical CQL for that transition does not write the column, so
     `RESOLVING` could capture `Rr = null` and the abort had no defined `Rr + 1`. This
     is correction 92's rule about recovery projections applied to a column: a value
     named only in prose is not written by anything. Fixing it by adding the column to
     the quarantine `SET` clause would have been the *wrong* repair, and the reason is
     the same one correction 75 gives for `retire_claim_epoch`: a generation can be
     quarantined, resolved, and quarantined again, so a counter initialized per
     quarantine **resets**, and a stale first-cycle `QUARANTINED -> null` naming
     `Rr = 0` would find `0` again and apply — an ABA on the fence built to stop an
     ABA. The counter is therefore initialized once by the `MATERIALIZING` intent that
     creates the row, written by nothing but an abort, and monotonic for the life of
     the generation.
176. **The abort must classify every reachable `gc_state`, and one of them forbids
     `gc_state` rollback.** The abort enumerated `QUARANTINED` and `null` only, while
     this design deliberately allows a delete authorization obtained under a
     pre-quarantine claim to survive both quarantine and recertification — so
     `QUARANTINED -> null` followed by a stalled worker's `DELETING` CAS is a legal
     ordering, and an abort requested afterwards fell through every branch and
     stranded recovery in `ABORTING` forever. Neither existing branch is usable:
     restoring `QUARANTINED` would regress an authorized, irrevocable delete against a
     possibly half-removed object, which is one of the three shortcuts When The
     Physical Delete Keeps Failing forbids; and taking the "nothing to undo" branch
     would report a retained quarantine that does not exist. The abort therefore
     performs no rollback of `gc_state`, materialization, storage identity, or delete
     lifecycle metadata, terminates as `REJECTED` recording `abort_outcome` /
     `abort_observed_gc_state`, leaves the pointer fenced and the delete work running,
     and alerts. The monotonic `resolution_epoch` fence may already have advanced and
     is retained — that bump is a fence, not a lifecycle rollback (see correction 179).
     Nothing unsafe has happened — G1 reached `RETIRED`, authority can never be
     reacquired, and the delete was correct independently of the resolution — but
     whether the logical block may have a G2 is now a fresh human decision, which is
     what the quarantine workflow exists to route. Only the recertification form can
     reach this state; a `RETIRING/QUARANTINE` pointer never held a delete
     authorization. The later human recovery path is a **pointer-only** audited
     recertification that does not require `QUARANTINED -> null`.
177. **The generation fence is a LWT; treat it like one.** The abort sequence issued
     `Rr -> Rr + 1` and then continued as if "applied" were the only outcome. A lost
     response after Cassandra applied the LWT leaves recovery looking at
     `work=ABORTING` and `resolution_epoch=Rr+1`, and a blind retry of
     `IF resolution_epoch = Rr` returns `not applied` with no contract for reading that
     as success. Worse, an accepted-but-unlearned fence can still leave an ordinary
     read at `Rr`, so advancing to rollback would leave a stalled
     `QUARANTINED -> null` able to fire. The generation partition therefore gets the
     same settlement rule `blocks` already has: ambiguous or uncertain results
     `SELECT ... CONSISTENCY SERIAL`; settled `Rr + 1` with matching identity means
     this abort's fence already applied; settled `Rr` retries the exact same fence;
     any other settled identity or an unsettleable partition remains `ABORTING` with
     no rollback. The load-bearing property — only this abort produces exactly
     `Rr + 1` from the `Rr` frozen by its work row — is stated and tested, not inferred.
178. **Fence the pointer before the generation.** The first r3 abort ordered the
     generation bump first. That widens the race unnecessarily: a resolver that has
     already cleared `QUARANTINED -> null` can still win its pointer CAS between the
     two fences and return a known-suspect generation to `ACTIVE` before the abort
     takes over the claim — precisely the fail-open window the abort exists to forbid.
     A clear that sneaks in *after* the pointer takeover is reversible under a still-
     fenced pointer; an `ACTIVE` window is not. The pointer takeover is therefore the
     abort's linearization point and runs first; the generation fence follows.
179. **"`DELETING`/`DELETED` = no generation-row mutation" contradicted the fence.**
     The same section that required `resolution_epoch Rr -> Rr + 1` on every abort also
     said the delete-advanced branch must perform no generation-row mutation at all.
     Both cannot be literal. The corrected rule forbids rollback of `gc_state`,
     materialization, storage identity, and delete lifecycle metadata; the monotonic
     fence counter may already have advanced and is retained.
180. **Fence the whole abandoned resolution, not only its last statement.** The
     false-positive path repairs `MATERIALIZING -> VERIFIED` in its own write before
     `QUARANTINED -> null`, but only the clear named `resolution_epoch = Rr`. A
     resolver stalled before the repair could therefore still flip the marker after
     `REJECTED`. That does not reopen writer usability while `gc_state` remains
     `QUARANTINED`, but it falsifies the claim that the generation partition is fenced
     against the abandoned resolution. Every post-`RESOLVING` resolver mutation of
     `block_generations` therefore conditions on `resolution_epoch = Rr`.

181. **Durable abort authority and pointer revocation must be the same `blocks`
     event.** After corrections 177–180 the abort still wrote `RESOLVING -> ABORTING`
     before the pointer fence. That work-row write does not revoke a stale resolver:
     the resolution pointer CAS names only `(G, E, active_state, Cq, Nq, QUARANTINE)`
     and never reads the work partition. The strong interleaving is therefore not a
     simultaneous CAS race but a real-time order — `ABORTING` commits, time passes,
     then the old resolver issues its pointer CAS and returns a known-suspect
     generation to `ACTIVE` under `work=ABORTING`. The abort later SERIAL-settles,
     discovers the pointer step committed, and raises a new quarantine — exactly the
     window corrections 166/168 forbade. The first authoritative abort act is therefore
     a `SERIAL + ALL` claim takeover that installs `retire_claim_kind=QUARANTINE_ABORT`
     with fresh `(Cf, Nf)` and `retire_abort_id`. That single LWT is durable abort
     authority and the revocation of every resolution pointer CAS naming the old claim.
     Only afterwards may work move to `ABORTING`. A crash between those writes leaves
     `RESOLVING` plus `QUARANTINE_ABORT`; the scanner adopts the abort and must never
     continue the false-positive resolution. Ambiguous pointer-fence outcomes leave
     work still `RESOLVING` with the fence **unresolved** — they are not "`ABORTING`
     and fenced".

182. **`REJECTED` is terminal; pointer-only successor recovery needs a new work
     identity.** After `DELETE_ALREADY_ADVANCED` the ADR correctly keeps the pointer
     fenced and forbids `gc_state` rollback, then offered "a new pointer-only audited
     recertification under a fresh `RESOLVING` authorization". That sentence did not
     say where the new `RESOLVING` comes from: the rejected row cannot reopen
     (`REJECTED -> RESOLVING` is forbidden), and `FALSE_POSITIVE` is the wrong decision
     because the contradiction was confirmed. The exit is therefore a **new** `OPEN`
     work identity — later distinguished at creation by `work_kind` (correction 185) —
     with `decision = ALLOW_SUCCESSOR_AFTER_DELETE` at `RESOLVING`, fixing a prospective
     `(Cr, Nr)` against the live `QUARANTINE_ABORT` claim, then a fresh zero check /
     evidence / handoff, then `RETIRED/QUARANTINE_ABORT -> RETIRED/GC_RETIRE`. G1's
     delete lifecycle stays untouched.

183. **An ambiguous fence is unresolved, not "fenced".** Saying an ambiguous pointer
     result leaves the work "`ABORTING` and fenced" asserts knowledge the serial phase
     has not yet produced. Until SERIAL settlement classifies the Paxos/conditional-
     write history, the fence outcome is unresolved: remain at the furthest proven
     state, retry the exact operation, and do not advance to generation fence,
     rollback, or a terminal work transition on the strength of an ordinary read.

184. **Post-handoff quarantine work is single-owner under `LOCAL_QUORUM`.** Discovery
     may originate in any writer DC, and the ADR already required handoff confirmation
     in the designated GC DC before pointer/generation mutation. It did not freeze the
     same ownership for later work-state classification. `OPEN -> RESOLVING` and its
     siblings commit at `LOCAL_QUORUM`, so an operator mutation in `dc-eu` while the
     owner scanner reads in `dc-na` can leave the scanner on stale `OPEN` after
     authorization returned — and `OPEN` versus `RESOLVING` drive opposite recoveries.
     After handoff confirmation — and from creation for
     `work_kind=SUCCESSOR_AFTER_DELETE`, which never uses the writer-DC discovery
     exception — every `LOCAL_QUORUM` work-state read and mutation executes in the
     designated GC owner DC; operator/API requests that arrive elsewhere are routed
     there and must not mutate in the caller DC. If the owner DC is unavailable, the
     mutation fails closed / retries — never caller-DC `LOCAL_QUORUM` fallback, and no
     automatic ownership transfer outside the topology / drained-cutover procedure.

185. **`OPEN` needs an immutable `work_kind` once successor recovery exists.**
     Correction 182 introduced a new `OPEN` identity for
     `ALLOW_SUCCESSOR_AFTER_DELETE`, while the global scanner rule still said
     `OPEN -> complete the quarantine`. `decision` is set only with `RESOLVING`, so an
     interrupted successor row at `OPEN` was indistinguishable from ordinary quarantine
     work — the same class of ambiguity that motivated `RESOLVING` itself. Rows now
     carry immutable `work_kind` at creation: `QUARANTINE` or `SUCCESSOR_AFTER_DELETE`.
     `OPEN + QUARANTINE` completes quarantine; `OPEN + SUCCESSOR_AFTER_DELETE` awaits
     or drives that administrative workflow and never runs quarantine completion.
     No mutation may change `work_kind` after creation.

186. **Do not reserve a terminal transition without an enumerated interleaving.** The
     consistency table kept `ABORTING -> RESOLVED` as a "rare case" after correction
     181 moved the ordinary lost-race to `RESOLVING -> RESOLVED` before abort authority
     exists. No normative interleaving remained in which abort authority had linearized
     on `blocks` as `QUARANTINE_ABORT` and the work still needed terminal `RESOLVED`
     rather than `REJECTED`. A frozen ADR cannot carry an unspecified terminal exit.
     Once abort authority has linearized, `ABORTING` terminates only as `REJECTED`.

187. **Durable abort intent precedes abort authority, but is not authority.** After
     correction 181, authority correctly linearizes on `blocks` as `QUARANTINE_ABORT`,
     and `ABORTING` is written afterwards. A crash between those writes left recovery
     able to see `retire_abort_id=A` on the pointer but without durable
     `abort_actor`/`reason`/`started_at` or the exact intended fence attempt for
     exact retry — while the ADR still required those fields on `ABORTING`. Safety
     was intact (the pointer was fenced), but audited recovery was not. The abort
     therefore confirms a non-authoritative `pending_abort_*` / `pending_fence_*`
     intent on the still-`RESOLVING` work row *before* the pointer fence. Intent
     alone must never cause `ABORTING`, generation fence, rollback, or a terminal
     transition. Only `QUARANTINE_ABORT` linearizes authority; recovery then copies
     the intent into `ABORTING`. Correction 189 freezes the single-assignment /
     SERIAL-settlement / revisable-attempt contract for that intent.

188. **An abandoned successor `OPEN` needs a terminal exit that is not quarantine
     completion.** Correction 185 forbade treating `OPEN + SUCCESSOR_AFTER_DELETE` as
     "complete quarantine", and correction 186/182 kept `REJECTED` terminal, but left
     no defined exit if the human declined before `RESOLVING`. That row may now
     terminate `OPEN -> REJECTED` meaning successor authorization declined: pointer
     remains `QUARANTINE_ABORT`, generation untouched. It is not the quarantine-
     completion path.

189. **Abort intent is single-assignment; fence attempts are revisable; exact retry
     includes `Df`; successor cancel after `RESOLVING` is not `OPEN -> REJECTED`.**
     Correction 187 required durable intent before the pointer fence but under-
     specified the LWT: without `IF pending_abort_id = null`, SERIAL settlement, and
     exact-retry of one payload, concurrent abort writers could overwrite logical
     `A1` with `A2` while the pointer might already have linearized `A1` — provenance
     mismatch. Separately, saying both "exact-retry fixed `(Cf,Nf)`" and "retry against
     the new tuple after ordinary `QUARANTINE` takeover" contradicted epoch monotonicity:
     a superseded attempt's `Nf` can never apply once the live claim advanced. The
     contract is therefore: (1) first intent assignment is single-assignment under
     `pending_abort_id = null`, installing immutable logical `A` plus actor/reason/
     `started_at` and fence attempt `F1` with source and target `(Cf,Nf,Df)`;
     ambiguous results SERIAL-settle and exact-retry the same `(A,F,Cf,Nf,Df)` —
     never invent `A2`; (2) only a **proven** still-`QUARANTINE` (or, for successor
     cancel, still-`QUARANTINE_ABORT`) claim supersession may revise the fence attempt
     under the same `A` with a strictly higher `Nf'`; (3) exact retry always includes
     `Df`; (4) once a `SUCCESSOR_AFTER_DELETE` row has reached `RESOLVING`, cancellation
     is a successor-cancel abort re-taking `QUARANTINE_ABORT` under a fresh epoch —
     not `OPEN -> REJECTED`. If the successor pointer step already committed, abort
     never linearized: finish `RESOLVING -> RESOLVED` and raise a new quarantine.
     Correction 190 specializes that cancel's `abort_scope` and the attempt's full
     source identity; it does not reopen this single-assignment / revisable-attempt
     contract.

190. **A fence attempt stores the full source authority tuple; successor cancel is
     `POINTER_ONLY`; ordinary `QUARANTINE_ABORT` takeover preserves `retire_abort_id`.**
     Correction 189 reused the two-partition abort for `SUCCESSOR_AFTER_DELETE`
     cancel after `RESOLVING`. That work identity is a fresh `Q1` while the
     generation row still carries original `Q0`, so a generation fence matching `Q1`
     cannot apply and would wedge `ABORTING`. Successor authorization never mutates
     `block_generations`, so there is nothing on that partition to revoke.
     `abort_scope` is therefore derived from `work_kind`: `QUARANTINE` is
     `POINTER_AND_GENERATION`; `SUCCESSOR_AFTER_DELETE` is `POINTER_ONLY` (pointer
     fence, `ABORTING -> REJECTED` with `abort_outcome=SUCCESSOR_CANCELLED`, no
     generation LWT). Separately, once source and target can both be
     `QUARANTINE_ABORT`, kind+epoch cannot classify pre- vs post-linearization
     takeover: live `retire_abort_id=A` means this abort linearized (ordinary
     takeover may have advanced the claim afterwards); live `retire_abort_id` still
     equal to `source_abort_id` with epoch advanced means `F` never linearized.
     Each attempt therefore persists `source_kind` and `source_abort_id` (null iff
     `QUARANTINE`). The generic pointer CAS matches that full source tuple rather
     than hardcoding `retire_claim_kind=QUARANTINE`. Ordinary claim takeover of
     `QUARANTINE_ABORT` preserves `retire_abort_id` exactly; only an authorized abort
     fence may change it. A concurrent intent loser observes canonical `A'` and
     returns already-in-progress/conflict — it does not rewrite provenance. Scanner
     adoption of `ABORTING` requires `retire_abort_id` equal to **this** work's
     `pending_abort_id`, because a live successor row starts with `QUARANTINE_ABORT/A0`.

## Related Documents

- [Known issues](./KNOWN_ISSUES.md), X1 and X2 remain open.
- [Open work index](./OPEN-WORK-INDEX.md), production activation gate.
- [Upload-fence findings registry](./UPLOAD-FENCE-FINDINGS-REGISTRY.md), audit evidence.
- [Historical upload-fence PR plan](./GC-UPLOAD-FENCE-PR-PLAN.md), PR-1 through PR-10 history.
- [Architecture](./ARCHITECTURE.md), current GC behavior and known limitations.
- [Database guide](./DATABASE-GUIDE.md), current schema and consistency inventory.
- [Multi-region testing](./MULTIREGION-TESTING.md), existing regional test guidance.
- [Session checklist](./SESSION_CHECKLIST.md), required documentation verification.
