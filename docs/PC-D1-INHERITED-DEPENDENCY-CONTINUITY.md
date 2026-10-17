# PC-D1 - Inherited dependency continuity decision

> **Frozen (2026-09-29, [X1-CRITICAL-PATH.md](./X1-CRITICAL-PATH.md) §5):**
> this line is not on the X1 critical path. A productive consumer and PC-2
> are not X1 prerequisites; the certification-window fence (PC-D1B.5) is
> PRE-GC only; E1 re-evaluates whether the certified baseline is needed once
> W2 is closed. Statements below that order PC-D1B work before PC-2 remain
> the contract for a future consumer, not the X1 roadmap.

**Status:** DECIDED architecture freeze; PC-D1A authority foundation
implemented; PR #228 implements the fail-closed certifier gate; PC-D1B.3
mapping authority/cold-path promotion is implemented in PR #233 (merged).
The productive consumer remains open. Historical backfill is a greenfield
non-goal (2026-09-20; see the note in section 5).
**PC-D1A implementation:** canonical witness schema and HEAD-fenced/global-SERIAL
authority primitives landed 2026-09-19. PR #228 implements the read-only certifier
gate; PC-D1B.3 mapping promotion is implemented in PR #233 (merged), and
a productive consumer remains open.
**Issue:** `ISSUE-PC0-INHERITED-DEPENDENCY-CONTINUITY-01`
**Branch:** `docs/pc-d1-inherited-dependency-continuity`
**Decision development baseline:** `main@2936c1179` (PC-1 merged)
**PR merge baseline:** `main@33a41f822` (#217 merged)

## PC-D1B.1 implementation status (2026-09-19)

PC-D1B.1 implements the cold-path one-library / one-HEAD certifier. It walks
the complete reachable tree, resolves the exact minted physical incarnation,
establishes permanent current-library liveness with EACH_QUORUM visibility,
revalidates physical and GC authority, and settles an ambiguous witness CAS
through an authoritative serial read before reporting certification.

This slice does not add historical backfill, scheduling, lifecycle
serialization, a productive consumer, PC-2, or GC activation.
This document is the source of record for the inherited-dependency decision
required before PC-2. The architecture decision adds no importer, funnel
migration, GC behavior, or configuration change; PC-D1A implements the
canonical witness authority, and PC-D1B.1 implements the cold-path certifier.
`GC_ENABLED=false` remains mandatory.

## 1. The gap is real

R3 defines the logical positive delta as:

```text
LogicalPositiveBlockDelta =
  UniqueCanonicalSHA256(ReachableBlocks(new HEAD))
  - UniqueCanonicalSHA256(ReachableBlocks(old HEAD))
```

For:

```text
H1: A B C
H2: A B C D
```

the delta is `{D}`. `A`, `B`, and `C` are inherited and are therefore omitted,
even if `B` was first published under `UNKNOWN` and `C` under `CONDITIONAL`
continuity. No later incremental publication revisits those proofs.

The executable test
`TestPCD1LogicalPositiveDeltaOmitsUncertifiedInheritedDependencies` uses this
exact vector and fails if an implementation silently treats the logical delta
as a complete work set.

This is a positive-continuity gap, not a claim that GC is already safe. The
historical Phase 5 characterization showed a dangling expired commit deleting
`fs_objects` still reachable from HEAD. [The scoped Phase 5 fix](./GC-PHASE5-SHARED-FSOBJECTS.md)
now retires unguarded commits without cascading into their trees, with the
preservation regression `TestPhase5CommitRetirementPreservesFSObjectsSharedWithHEAD`.
Phase 6/concurrent metadata destruction and reclamation remain PRE-GC; this fix
does not certify general inherited continuity or change the frozen PC-D1 scope.

## 2. Architecture comparison

| Dimension | A - coordinator-expanded | B - GC-owned | C - baseline certification |
|---|---|---|---|
| Hot path | O(reachable tree/dependencies) for every publish unless it grows durable per-dependency state (which becomes C) | No publication cost; destructive work pays the scan cost | O(reachable tree) only when the witness is absent/stale; steady state is O(newly-live) plus one witness predicate |
| Tree reads | Every publish, or every dependency-status lookup | Every destructive scan and every stale-item revalidation | One complete walk per certification; no inherited walk while the witness matches HEAD |
| Multi-DC | Every ordinary publish depends on all proof reads being available | Destructive GC must fail closed if a DC cannot answer reachability | Certification and witness writes require the configured global `SERIAL`/LWT quorum. Loss of enough replicas to satisfy that domain blocks certification. A valid witness removes inherited-tree reads, but incremental publication still retains the availability requirements of newly-live proofs, readiness/fences, and the HEAD+witness CAS |
| Crash/restart | Requires durable per-dependency progress or repeats a full walk | Existing queue is durable, but a queued keep-set can become stale | Partial work grants no authority; no witness means retry. Existing durable pins/repairs remain the recovery state |
| HEAD advances during work | A stale full walk must be discarded and repeated | A stale destructive snapshot must be fenced before delete | A certification for H is accepted only with `IF head_commit_id = H`; a later legacy H->H' makes the witness invalid by equality |
| Existing libraries | Only the next mutation discovers the gap; inactive libraries remain unexamined | Cannot prove or repair historical publication continuity | Explicit backfill or lazy certification; an un-certifiable library is fail-closed |
| Libraries created after cutover | Full proof remains the default cost | Still depends on correct publication and GC | Empty initial HEAD may be certified atomically once the initializer is V-aware; otherwise first coordinated use certifies it |
| Content resurrection | Newly-live and still adapter-owned | Does not protect the resurrection publication race | Historical content is newly-live again and still requires BORROWED own pin and `ExpectedP` |
| Durable state | Per-dependency proof, or a baseline/frontier equivalent to C | Queue identity, reachability snapshot, and delete fences | Canonical witness `(library, certified HEAD, contract version)`; a cursor is optional optimization, never authority |

### Why A is not selected

Without a durable frontier, A converts a one-time historical problem into a
tree-sized hot-path tax. With a frontier, the coordinator is simply redoing the
certification state machine selected by C. A also leaves inactive libraries
without any new protection and does not repair a missing physical object.

### Why B is not the continuity owner

GC is a destructive authority. A sharing-aware GC is required to preserve every
object reachable from the current HEAD, but a negative answer from GC cannot
prove that an old publication had valid provenance, cannot reconstruct bytes
that are already missing, and cannot certify a library that was never checked.
Making GC the continuity owner would also couple publication availability to
the destructive worker and create an ambiguous coordinator/GC responsibility
boundary. B is therefore a mandatory PRE-GC safety repair, not the answer to the
PC-D1 positive-continuity question.

## 3. Decision: certified baseline frontier

```text
INHERITED CONTINUITY OWNER = CERTIFIED BASELINE FRONTIER
```

The frontier is a library-level continuity protocol:

1. Observe the canonical HEAD `H` for library `X`.
2. Walk the complete tree of `H`, canonicalize and deduplicate every block, and
   run the following **GC-aware baseline handshake for every physical
   dependency** before counting that dependency as certified:

   ```text
   resolve/capture exact physical incarnation P
     → establish durable library-owned liveness
     → revalidate exact P + GC authority
   ```

   Here `P` is the exact physical incarnation and canonical placement tuple
   `(storage_class, storage_key)`, matching the GC/R26 vocabulary. PC-D1 does
   not invent a second incarnation field: a minted locator's UUID suffix is
   part of `storage_key` and therefore part of P. A legacy deterministic
   locator is not evidence of a fresh physical generation. Before such a
   dependency can enter a witness, the implementation must safely
   rematerialize/migrate it to a minted, never-reused P; until that transition
   is implemented and validated, a legacy deterministic dependency is
   un-certifiable and baseline certification fails closed. Any future
   `physical_generation` token would be a separate pre-PC-2 decision, not an
   implicit PC-D1 field. “Durable” means persisted in the canonical Cassandra
   domain and visible to the GC authority, not a process-local flag or an
   eventual write. A bounded-TTL `up:`/`pub:` pin may bridge the scan and
   handshake, but it can never by itself justify the witness. Before writing
   the witness, every dependency must have non-expiring current-library
   liveness (normally finalized `fs:` authority retained while the certified
   HEAD reaches it), or a confirmed idempotent repair that established that
   permanent authority. The lifetime must cover the witness/reachability
   frontier; a renewal failure fails certification. The final revalidation is
   a fresh authority check **after** liveness is established; it is not
   satisfied by the initial placement read or by a bare fence read.

   If any step is missing, ambiguous, unavailable, observes a changed `P`, or
   finds that GC already owns destructive authority, the
   dependency and the whole baseline certification fail closed. A late
   liveness write cannot revoke a zero-proof/GC authority already won, which is
   why the post-liveness revalidation is mandatory.
3. Commit a durable witness only if the canonical HEAD is still exactly `H`.
4. For a later coordinated publication `H -> H'`, prove only the
   `LogicalPositiveBlockDelta` and atomically advance both HEAD and the witness
   to `H'` under the same contract version.

This gives an induction proof: the baseline certifies all dependencies in H;
each subsequent coordinated publication certifies its positive delta; therefore
all dependencies in the current certified HEAD have a continuous chain of
proofs. If a witness is missing, stale, or from an unsupported version, the
incremental path is not admissible: certify the current HEAD first or fail
closed.

PC-D1B's future coordinator is the only intended productive consumer and
advancer of this frontier. PC-D1A adds no productive consumer: GC must
preserve current-HEAD reachability, but it does not create, interpret, or
replace the witness. There is no double ownership.

## 4. Witness contract

The exact semantic statement is:

```text
library X
certified through HEAD H
under continuity contract V
```

The canonical representation is two fields on the existing `libraries`
row (not a process-local map and not a second authority table):

```text
continuity_certified_head_commit_id = H
continuity_contract_version         = V
```

The witness is valid only when all of the following hold:

- the live canonical row is the same `(org_id, library_id)`;
- `deleted_at == null`; a canonical row whose deletion is already visible as
  non-null is never a valid continuity authority;
- `head_commit_id == continuity_certified_head_commit_id == H`;
- `V` is the currently accepted contract version;
- the certificate's complete tree walk found every reachable `fs_object`;
- every canonical block completed the baseline handshake above: the exact
  physical incarnation P = `(storage_class, storage_key)` was captured,
  non-expiring current-library liveness (or an idempotent repair that
  establishes that permanent authority) was persisted and visible to GC, and
  a fresh exact-P/GC-authority revalidation accepted that same P afterwards;
  a bounded-TTL pin is only a certification bridge and is never sufficient by
  itself;
- any read error, unavailable DC, incomplete tree, missing object, ambiguous
  CAS, or unsupported version fails closed.

The certification write is conceptually:

```text
UPDATE libraries
SET continuity_certified_head_commit_id = H,
    continuity_contract_version = V
WHERE org_id = X.org AND library_id = X.id
IF head_commit_id = H
   AND deleted_at = null
```

The coordinated advance is conceptually:

```text
UPDATE libraries
SET head_commit_id = H',
    continuity_certified_head_commit_id = H',
    continuity_contract_version = V
WHERE org_id = X.org AND library_id = X.id
IF head_commit_id = H
   AND continuity_certified_head_commit_id = H
   AND continuity_contract_version = V
   AND deleted_at = null
```

PC-D1A adds exactly these two columns and executes these two DB primitives.
PC-D1B.1 adds a cold-path certifier that consumes only the baseline witness
primitive; no legacy HEAD writer, derived projection, or productive funnel
consumes or advances the witness in this PR. The
`deleted_at` predicates are fail-closed guards for an already-visible
deleted state; they do not serialize the existing production
soft-delete/restore/hard-delete lifecycle with the global HEAD Paxos domain.
`ISSUE-LIB-DELETED-FENCE-01` remains open and is a prerequisite before a
productive PC-D1B consumer relies on the frontier across concurrent lifecycle
activity. PC-D1B.4
([PC-D1B-CERTIFICATION-WINDOW-FENCE.md](PC-D1B-CERTIFICATION-WINDOW-FENCE.md))
shows that soft-delete, restore and hard delete do not change what a witness
asserts; the witness-side fence is a per-library destruction epoch plus pending
destruction intents predicated by both PC-D1A primitives.

### Canonical SERIAL domain prerequisite

Before frontier activation or PC-2, **all** canonical `HEAD` writers that
coexist with the frontier (legacy advances, initializers, rollback guards), the
baseline-certification LWT, and the combined HEAD+witness advance must use one
compatible global `SERIAL` Paxos domain.

`ISSUE-LIBRARY-HEAD-SERIAL-DOMAIN-01` is closed: every current competing
`libraries.head_commit_id` LWT pins `SerialConsistency(db.LibraryHeadSerialConsistency)`
(`gocql.Serial`) and does not inherit `database.serial_consistency`. A
supported multi-DC deployment may still set `LOCAL_SERIAL` for other LWTs; that
default is not a substitute for the HEAD pin, and a warning is not the
invariant. **Global SERIAL prerequisite: satisfied.** Certified baseline
implementation, PC-2, W2/R31, G4/G5, and X1 remain OPEN. `GC_ENABLED=false`.

### PC-D1A executable evidence

The authority foundation is covered by Docker-only unit and mutation evidence:

```bash
docker run --rm -v ${PWD}:/build -w /build sesamefs-pcd1a-gotest \
  go test ./internal/db ./internal/publication
bash scripts/pc-d1a-certified-frontier-mutation-validation.sh
```

The real three-DC evidence uses an isolated Cassandra project and the
`sesamefs-pcd1a-*` resource prefix; it leaves the existing application
stacks untouched:

```bash
bash scripts/pc-d1a-certified-frontier-multidc-validation.sh
```

The 3-DC gate runs with session `LOCAL_SERIAL` while both PC-D1A primitives
pin global `SERIAL`, proves competing baseline witnesses converge, rejects a
stale certificate after a legacy HEAD move, and proves atomic
`(H,H,V) -> (H',H',V)` convergence. `GC_ENABLED=false` remains explicit.

### Moving-HEAD proof

```text
t0  certifier observes H
t1  another writer advances HEAD H -> H'
t2  stale certification attempts witness(H,V) with IF head_commit_id = H
    -> applied=false; no witness for H' is written
t3  a fresh certification must observe and prove H'
```

If the stale certification wins at `t1` and a legacy writer advances HEAD at
`t2`, the row contains `head=H'` and `certified_head=H`; equality fails, so the
witness is unusable. A crash before the final LWT also leaves no certification
authority. PC-D1A returns `UNKNOWN` plus the Cassandra error for an ambiguous
final LWT; it performs no read-back and never infers `APPLIED`. The PC-D1B.1
certifier settles that result through an authoritative serial read; any future
productive caller must use the same settlement discipline and read HEAD and
both witness fields in the canonical serial domain before classifying the
result. This
invalidation guarantee depends on the global `SERIAL` domain prerequisite
above; `LOCAL_SERIAL` cannot provide one global frontier.

**Productive witness reads (frozen by PC-D1B.4).** The witness shape stays
`(H, V)` and its validity predicate is unchanged, but every productive decision
that treats the witness as authority must obtain `(head_commit_id,
continuity_certified_head_commit_id, continuity_contract_version, deleted_at)`
with a global `SERIAL` read, or evaluate it inside an LWT of the same domain
(as the frontier advance does by predicating it). A `LOCAL_QUORUM` observation
is never authority: once destruction intents exist, it can be a stale copy of a
witness that an intent in another DC already cleared
([PC-D1B-CERTIFICATION-WINDOW-FENCE.md](PC-D1B-CERTIFICATION-WINDOW-FENCE.md)
§10.7). The first productive consumer owns the matching
`SERIAL → LOCAL_QUORUM` mutation (CW-M17) and its 3-DC evidence.

### GC-authority interleaving (mandatory baseline rule)

The HEAD conditional is necessary but not sufficient. The baseline must not
use this unsafe order:

```text
resolve P → observe GC zero-proof → write library liveness → certify
```

The zero-proof can already have granted destructive authority before the late
liveness appears, and that write does not retroactively revoke the authority.
The only admissible order for each dependency is:

```text
resolve/capture exact physical incarnation P
  → establish durable library-owned liveness
  → revalidate exact P + GC authority
  → include in the baseline certificate
```

The revalidation must use the same canonical authority domain as the relevant
GC fence/claim (and fail closed on an unavailable or ambiguous observation).
Only after **all** dependencies pass this handshake may the certifier attempt
the final `IF head_commit_id = H AND deleted_at = null` witness write. The HEAD CAS protects the
logical frontier; the per-dependency handshake protects the physical
incarnation against GC.

## 5. Lifecycle and boundaries

### Existing libraries

Use an explicit backfill or lazy first-use certification. Backfill may be
parallelized, but only the final exact-HEAD LWT is authority. Backfill may
certify minted physical locators; a legacy deterministic locator must first be
safely rematerialized/migrated to a minted, never-reused P. A library whose
tree, physical bytes, or required permanent liveness cannot be proven remains
uncertified and cannot take the incremental publication path. Certification
does not claim that an earlier UNKNOWN/CONDITIONAL publication was historically
safe; it establishes a new safe baseline from the bytes that exist now.

Certification also presumes that the metadata identities it traverses are
authoritative, which is a separate prerequisite from the physical
exact-P/liveness handshake above. It is decided in
[PC-D1B-METADATA-IDENTITY-AUTHORITY.md](./PC-D1B-METADATA-IDENTITY-AUTHORITY.md)
and tracked as `ISSUE-PCD1B-METADATA-IDENTITY-AUTHORITY-01`. Its legacy cutover
bounds how many existing libraries can ever be certified; it does not change
the fail-closed default for a library that cannot be proven.

**Note (2026-09-20).** This document's backfill, cutover and "legacy writers"
language, here in section 5 and in the section 6 PC-2 contract, predates the
explicit greenfield deployment contract recorded in
[PC-D1B-METADATA-IDENTITY-AUTHORITY.md](./PC-D1B-METADATA-IDENTITY-AUTHORITY.md).
Under that contract there are no pre-authority production rows, so historical
backfill and the legacy cutover are non-goals rather than planned work, and
that text stands as rationale and as what a brownfield deployment would need.
What survives as live work is coverage for identities the authority-aware
system itself creates unproven — chiefly the SHA-1-only file identities
`storeSyncFSObject` writes — through cold-path mapping promotion.

"Legacy writers during rollout" below is **not** covered by that non-goal, and
an earlier draft of this note wrongly implied it was. The two are different
writers:

- **Non-goal:** coexistence with a *pre-authority* production binary. The
  greenfield contract puts the authority-aware release before first production
  traffic, so that case does not arise.
- **Still live:** coexistence with a *supported, authority-aware* HEAD writer
  that has not yet been migrated to the PC-2 frontier protocol. That is exactly
  what "an old writer may advance HEAD without updating the witness" describes,
  it is expected during PC-2 funnel migration in a greenfield deployment, and
  the `head != certified_head` staleness rule that handles it remains a live
  property of this decision.

This note does not retro-edit the decision; the current status lives in
`ISSUE-PCD1-CERTIFIED-BASELINE-IMPLEMENTATION-01` and
`ISSUE-PCD1B-METADATA-IDENTITY-AUTHORITY-01`.

### Libraries created after cutover

The empty initial HEAD can receive a V-aware witness in the same initialization
domain. Until the initializer is migrated, a newly-created library has no
implicit certificate and the first coordinated publication must perform the
baseline gate.

### Legacy writers during rollout

An old writer may advance HEAD without updating the witness. That is safe for
the decision: `head != certified_head` makes the witness stale. The next
coordinated writer must certify the new current HEAD; it may not infer a chain
from the old witness across the legacy write.

### Content resurrection

If a resurrected historical object is absent from the old HEAD, it is
newly-live and remains the responsibility of the BORROWED adapter: own pin,
exact `ExpectedP`, publication staging, repair, and fence. A baseline witness
must never bless it merely because the object appeared in an older commit.
The four current resurrection routes remain open W2 work.

### GC and activation

Phase 5 still needs a sharing-aware keep-set or a metadata-only retirement
change before any destructive activation. A valid witness is not a GC delete
authorization, and `GC_ENABLED=false` remains required for this PR and for the
current fleet.

## 6. PC-2 contract

```text
PC-2 may assume:
  - incremental newly-live evidence is sufficient only when the predecessor
    HEAD has a valid durable witness (X, H, V) backed by non-expiring
    current-library liveness for every inherited dependency;
  - the witness is consumed and advanced atomically with the HEAD CAS;
  - every coexisting canonical HEAD writer and frontier LWT participates in
    the same compatible global `SERIAL` Paxos domain;
  - inherited dependencies need not be rescanned while that equality holds.

PC-2 may NOT assume:
  - every existing or newly-created library is already certified;
  - a local read, in-memory flag, partial scan, or GC reachability observation
    is a witness;
  - current Phase 5 protects inherited dependencies;
  - content resurrection, W2, R31, G4, G5, or X1 is closed;
  - an unavailable or unprovable HEAD can be certified by inference.
```

Before the first productive funnel migration, PC-D1B must add the complete
baseline certification gate, exact-P and GC-authority handshake, settlement
read-back policy, and productive consumer integration. (2026-09-20: the
original wording said "certification/backfill gate"; historical backfill is a
greenfield non-goal, see the note in section 5.) PC-D1A already provides
the canonical witness state, fail-closed validity, and atomic HEAD+witness
authority primitives; those primitives remain unused until PC-D1B proves the
preconditions.

## 7. Evidence and merge criteria

The PC-D1 decision, PC-D1A authority foundation, and PC-D1B.1 certifier evidence include:
- PC-D1B.1 source contracts for complete reachable-tree traversal, bounded
  fail-closed behavior, minted-P validation, and physical-byte existence at the
  exact captured storage class/key (`NOT_CERTIFIED/physical_bytes_missing`;
  unavailable storage is `UNKNOWN/physical_storage_unavailable`), permanent
  EACH_QUORUM liveness before and after writes and before the witness,
  exact-P/GC-authority revalidation, context-aware final witness CAS, and
  ambiguous-CAS settlement;
- directed M1-M15 source mutations that independently make each critical
  certifier condition RED, including M11 exact physical-byte proof, M12
  context-aware witness-CAS propagation, M13 incomplete-file rejection,
  M14a reachable fs_object and M14b commit H-to-R identity verification,
  M15a SHA-1-only `identity_unproven`, and M15b paired-mapping disagreement
  with the claim-bound canonical dependency list (17 execution legs total);
- Docker 3-DC certifier evidence under LOCAL_SERIAL sessions proving complete
  tree certification against bytes at captured P in MinIO, permanent
  EACH_QUORUM-visible liveness, physical-byte-missing and storage-unavailable
  fail-closed outcomes, exact-P/GC rejection, a certifier-level P-change race,
  retry idempotence, stale-HEAD rejection, both applied and non-applied
  ambiguous-CAS SERIAL settlements, and missing/deleted/legacy negative cases;
- Docker 3-DC partial-row evidence proving that dc-eu retains complete reachable
  F, its permanent `fs:` reference to B, and bytes at exact P while dc-na has a
  partial F; after the remote DCs recover, certification returns
  `NOT_CERTIFIED/incomplete_fs_object` without new liveness work or a witness;
  this rejects partial authority but does not repair or converge the tree;
- isolated 3-DC metadata-identity evidence proving complete fs_object A/B
  divergence and commit H-to-R1/R2 divergence fail before physical or
  liveness work and create no witness; unavailable global SERIAL identity
  authority returns UNKNOWN without a witness;
- real-Cassandra edge evidence, also run in the 3-DC main leg, proving that an
  authority-verified Seafile `EMPTY_SHA1` root certifies as an empty tree
  (one commit, zero fs_objects/blocks/liveness/physical work) and that
  `EMPTY_SHA1` file/directory entries need no fs_objects row, while an
  `EMPTY_SHA1` root with a missing or conflicting commit claim fails closed
  without a witness;
- PC-D1B.3 Mapping Authority evidence
  ([PC-D1B-METADATA-IDENTITY-AUTHORITY.md](./PC-D1B-METADATA-IDENTITY-AUTHORITY.md#pc-d1b3-mapping-authority-implementation-2026-09-23)).
  A SHA-1-only dependency certifies only through a write-once global-`SERIAL`
  mapping claim. Its value was proved from representation-bound stored bytes,
  and its ordinary `block_id_mappings` row was frozen at a dominant write
  timestamp so that stale ordinary writes are inert. A diverged row fails
  closed during the walk and before the witness, and a mapping without
  provenance stays `identity_unproven`. The evidence is M18a-g, M19a-b,
  R1-R2, E1, S1-S2, H1-H2 and I1 mutations, the real-Cassandra behavioral leg
  T1, and isolated 3-DC legs MAPPING-3DC-1/1b/2/3/4/4b/5;
- a real EACH_QUORUM outage leg that stops only the fixture's `dc-asia` node,
  confirms it is `DN` from `dc-na`, and verifies liveness-read failure returns
  `UNKNOWN` without a witness before restoring the node.

- the inherited-delta counterexample and moving-HEAD witness model tests;
- a test-only GC interleaving model proving that late library liveness does not
  revoke already-won destructive authority and that the required
  `resolve/capture exact physical incarnation P → durable liveness → exact-P +
  GC-authority`
  order is enforced;
- a test-only inductive frontier model proving only `(H,H,V) → (H',H',V)` and
  rejecting a missing/stale predecessor witness, a wrong contract version, or
  a mismatched predecessor HEAD; mutations must make each predicate and both
  witness updates fail closed;
- a source-contract test that pins the single owner, the conditional meaning of
  `newly-live`, the exact witness rule, the non-expiring liveness requirement,
  the legacy deterministic-locator policy, the global `SERIAL` prerequisite,
  and the open issues;
- a Docker 3-DC PC-D1A run against the canonical migrated `libraries` table
  that proves competing baseline witnesses converge, stale certification is
  rejected after a HEAD move, a globally-visible/established deleted state
  rejects both authority LWTs,
  and `(H,H,V) -> (H',H',V)` converges under session `LOCAL_SERIAL` with
  explicit global `SERIAL` primitives;
- directed mutations proving that removing each HEAD/witness/version/lifecycle
  predicate or either atomic witness SET makes tests RED;
- Docker unit/full-suite, `go vet`, mutation, 3-DC evidence, and
  `git diff --check` validation.

The issue is marked **decision resolved / PC-D1A authority foundation and
PC-D1B.1 certifier gate implemented / PC-D1B.3 mapping coverage implemented in
PR #233 (merged) / lifecycle fence and productive-consumer work open**.
W2, R31, X1, content resurrection, G4/G5, and
`ISSUE-GC-PHASE5-CASCADE-SHARED-FSOBJECTS-01` remain OPEN. No funnel is migrated,
no publication runtime changes, and no GC activation is permitted.
