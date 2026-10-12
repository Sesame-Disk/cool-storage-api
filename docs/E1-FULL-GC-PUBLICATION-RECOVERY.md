# E1 — Full-GC publication and recovery evaluation

Status: ACTIVE; E1-16 (post-D contract alignment with G4, docs only, base main@5c3163871803 with #276 and #261 merged): the X1 invariant is physical; W2-1..9 and W2-12..14 close for X1; W2-10, E1-01, the three-DC residuals (suite re-measured 2026-10-10), the D0 §25 list and one PRE-X1 audit debt remain ([E1-16](./E1-16-POST-D-CONTRACT-ALIGNMENT.md)). The section "E1-16 — post-D contract alignment" below supersedes the PRE-X1 labels and strict post-D reading in the older status text that follows. Before it, E1-15F (#276): 3-DC evidence for SUPERSEDED plus a parent-read liveness fix. Before it, E1-15E (#275): the repair worker settles superseded abandoned repairs (option A of E1-15D §3), narrowing the PRE-GC retention. Before it, E1-15D (#274). E1-15D shows abandoned commits stay publishable via Sync auto-merge, and proposes the contract split and cancellation authority for approval. Before it, E1-15C on #273, E1-15C measures no physical (D0 §13) RED from post-D refs on Office; it shows that dead repairs veto later lives of L; a contract reconciliation is pending a decision. E1-15B confirms post-D writer staging liveness on crash (P1 PRE-X1 OPEN, characterized). E1-11 and the measured E1-12/E1-13 subsets have pre-D evidence; the measured E1-14 subset shows `pub:` expiry alone reaches no candidate; E1-15A found a stale-renewal post-D RED and compensated it (PARTIAL-FIX / CLOSED-EVIDENCE only when the global check and withdrawal succeed; strict guarantee OPEN P1 PRE-X1); W2-12/13/14, E1-15 overall and E1/X1 remain OPEN.
Matrix frozen before E1 runtime edits: 2026-10-03. Base main@eabd93bee includes merged G5 PR #248 and the pinned SILO backend from PR #249. Scope: supported greenfield deployment with one compatible release. Destructive GC remains OFF.

Tested storage backend: `docker.io/pgsty/silo:RELEASE.2026-09-16T00-00-00Z`, a MinIO-compatible S3 backend. The Compose service remains named `minio`; this ledger does not claim tests against the archived MinIO server image.

## Contract and disposition rules

Evaluate current main's full G1→G5 executor and every current writer/repair funnel able to publish a block reference. X1 invariant (aligned with G4 contract 3–5 by E1-16, 2026-10-10): once exact physical life P1 has committed D1 (and separately after terminal retirement), no legitimate current-version operation may reinstall P1 or make it canonical again, revoke or postpone D1, or publish HEAD/commit/tree content depending on retired P1; a possibly accepted publication keeps D-blocking liveness until its permanent `fs:` exists. References are logical liveness of L (D0 §3): an `up:`/`pub:`/`fs:` row of L written after D1 is not a P1 dependency, and its retention is PRE-GC convergence. (Replaced wording: "may durably add a P1 reference or publish HEAD with reachable content depending on P1".)

A surviving S3 byte alone is not a logical reference. Inspect exact canonical P/D lifecycle and durable root, (storage_class, storage_key), block_references, commit/HEAD/tree ancestry, bytes, repair rows. Read with production EACH_QUORUM/SERIAL where used. Settle ambiguous writes as UNKNOWN; timeout alone proves neither result.

Run this matrix before runtime edits. A counterexample must traverse existing handler/worker/retry/repair/upload code and leave a durable post-D P1 dependency. Record source SHA, exact P/D/referrer, before/after rows, request result, consistency, actor DC, restart, and object state. Missing refs, leaked bytes or retained repair alone do not falsify X1. Non-reproduction alone does not close a W2 row: require positive source exclusion proof or an existing tested fail-closed gate. On RED make the smallest fix, preserve RED evidence, prove GREEN, then run relevant crash/restart and consistency controls.

Use X1 §4 states: CLOSED-EVIDENCE requires positive exclusion under the supported contract; CLOSED-FIX requires a fix plus evidence; CLOSED-GATED requires a named tested fail-closed mechanism. Else OPEN. Do not declare X1 CLOSED while any applicable §4 W2 row is OPEN/UNKNOWN. X1 CLOSED does not activate GC.

## Frozen matrix

Common real-service harness: install exact P1; create/remove/move its legitimate reference through the named current funnel; let real scanner/queue/claim go through PREPARED→COMMITTED; preserve G5 root. Pause/release the writer at the actual boundary. Restart when specified. Each leg includes a no-GC success control. All rows start UNRUN. Existing pre-HEAD PASS does not close post-D/R31 rows.

| ID | Funnel | Counterexample to attempt | RED only if |
|---|---|---|---|
| E1-01 | GC/recovery baseline: candidate→claim→PREPARED→COMMITTED→retire→G5 recovery | Exercise the production worker and SILO; kill/restart after root, COMMITTED, canonical retirement, S3 delete and mapping/root terminal cleanup. | Recovery creates/reopens P1 liveness or D commits with an authoritative live dependency. Record K1/K2, exact idempotence. G4/G5 legs are controls. |
| E1-02 | Master late-publication race | Hold a valid writer/repair; let prior fs:/pub:/up: lapse; run full GC to COMMITTED D1(P1); resume at its real publication step. Repeat once D is TERMINAL. | Durable P1 reference or reachable HEAD/commit/tree after D, including accepted success leaving that state. Priority one. |
| E1-03 | W2-1/2 CreateFileFromBlocks, SessionUpload, BorrowedFS/dedup | Pause around ensureCommitBlockOwnLiveness, final validateCommitBlockPublicationFences, pub: stage, HEAD CAS and settlement. Cover owned up: and foreign fs:; expire only real TTL. | Post-D P1 dependency. Record 409/unchanged HEAD controls. Separate pre-HEAD proof from R31. |
| E1-04 | W2-3 Sync PutBlock, auto-merge, retry on another pod | Delay across up: expiry or cross-DC visibility, commit D1, then resume readiness/HEAD/repair from relevant DCs. | Reachable P1 after D. Fail-closed timeout/unchanged HEAD is safe but does not close availability. |
| E1-05 | W2-4 post-HEAD R31; W2-5 RecvFS before PutBlock | Run no-PutBlock repair chain after its pre-HEAD fix; separately trace actual ReceiveFS→commit→later PutBlock. Interleave D at each existing durable boundary and retry. | Late repair creates P1 after D or legal Sync publishes reachable retired P1. Pre-HEAD W2-4 is not R31 evidence. |
| E1-06 | W2-6/6a v2 UploadFile and Office-template CreateFile | Keep exact-placement checks as controls. Pause after HEAD at pending repair settlement, cleanup, renewal; expire pub: by TTL; commit D; restart/resume. | Post-D P1 ref/reachable HEAD. Distinguish UNKNOWN from proven loser. |
| E1-07 | W2-7 SeafHTTP normal/streaming | Use HandleUpload/finalizeUploadStreaming; pause from materialization/up: through stageSeafHTTPPublishAttemptReferences, HEAD, finalizeSeafHTTPPublishedBlockReferences, retry/cleanup. | Post-D P1 ref/HEAD. Record staged/promoted identity and response. |
| E1-08 | W2-8 OnlyOffice callback | Use saveOnlyOfficePendingBlock and actual callback. Pause around stagePendingPublishedFiles, HEAD, repair, cleanup, replay and D. | Successful callback leaves a durable post-D P1 dependency. |
| E1-09 | W2-9 cross-repository copy/move | Use copyFSObjectToLibraryForPublish/processSingleItem. Read source P1; stage destination pub:; race source GC; publish destination; resume promotion and source cleanup. Exercise copy and move. | Destination HEAD/fs_object durably depends on retired P1. Pre-HEAD failure or bytes alone is not RED. |
| E1-10 | W2-10 RevertFile, RevertDirectory, RestoreTrashItem, RevertDirents | Pause actual historical tree path after old fs_object/block read but before new HEAD/ref; commit D1; resume. Retain an extant tree; keep Phase 6 deletion outside this leg. | Handler publishes reachable P1 post-D. If only reachable through Phase 6 or invented state, document and leave W2 OPEN. |
| E1-11 | W2-11 repair pub: renewal after classifier | Pause repairPublishedBlockReferenceRepairVisit between ancestry classification and renewPublishedBlockReferenceRepairLivenessIfPending; let liveness expire, commit D, resume. | Renewal/promotion creates durable P1 after D; preserve ancestry proof. |
| E1-12 | W2-12 known-loser durability | Force real applied=false HEAD CAS; kill request before local cleanup; discover/restart repair around D. Never relabel UNKNOWN as loser. | Unreachable loser is promoted as P1 after D. |
| E1-13 | W2-13 repair discovery scale | Put an actually reachable repair behind controlled current backlog; expire pub: through TTL, advance G5, restart/visit repair. | Reachable P1 is referenced after D. Slowness/retention alone is not RED. |
| E1-14 | W2-14 pub: expiry/zero-ref transition | Expire final pub: with pending repair; run scanner/G5 to D; resume persisted repair. Include no-repair control; never manufacture gap via direct CQL. | Reachable P1 promoted post-D. If durable guard prevents D, capture positive source proof and tested gate. |
| E1-15 | Shared Sync/SeafHTTP/OnlyOffice/cross-repo repair cleanup, cancel, retry | Interleave owner cleanup, dead/unreachable repair, cancellation, renewal, progress-bucket contention, restart and retry at actual boundaries. | An actual HEAD-reachable repair adds P1 after D. Ownerless pub over-retention or irrelevant residue remains a follow-up. |

## Delayed PUT (separate classification)

E1-PUT-01: obtain a real pre-D authorized upload/session for P1/K1; run GC through COMMITTED and full retirement/K1 deletion; deliver held PutBlock/storage write and its real metadata-install, mapping and reference steps. Repeat at worker restart and only a material DC boundary. Inspect exact bytes, canonical P/D, mapping, block_references, reachable HEAD at production consistency.

- K1 bytes alone return: storage over-retention/leak follow-up, not by itself X1 RED.
- Metadata/ref/HEAD reinstalls P1 after D: E1/X1 RED; fix only if reachable through supported current code.
- Upload/install fails closed with no P1 ref: safe; record availability/retry.

Use real authorization/upload/install/mapping; a test-only writer is not a product counterexample.

## Execution ledger — 2026-10-03 (partial; X1 remains OPEN)

The E1 integration additions are test-only and build-tagged `integration`; no
production runtime behavior changed. The final Docker command
`docker compose --profile test run --rm --build go-all-test` exited 0 after
these additions, including Go unit/integration, API and OIDC suites. The normal
runner reports isolated 3DC legs as skipped when they are not enabled; no E1
cross-DC leg is claimed as passed. The full-suite result does not substitute
for the post-D counterexample legs below. The directed E1 cases also pass under
`go test -race`; `go vet ./...` and `go vet -tags integration ./...` pass in the
same sequential Docker runner. An initial race repeat exposed a fixture
assumption: the application G5 recovery worker can finish the same durable root
before the test-scoped visitor. Its verifier now accepts that interleaving only
after confirming the exact terminal class/key certificate, absent exact root
and orphan, absent canonical row, and deleted K1; the race rerun passes.

| Case | Result | Evidence and limit |
|---|---|---|
| E1-PUT-01 delayed physical PUT | PASS for the exercised Sync `PutBlock` path | `TestE1DelayedPutCannotRestoreRetiredPhysicalLife` uses real Cassandra, SILO, the production handler and G5 worker. It pauses the physical write after authorization of exact P1, drives P1 through terminal recovery and K1 deletion, then releases the actual storage PUT. The request returns 200; K1 bytes reappear, but P1 stays absent, the same logical block is rematerialized at a different P2 key, and references contain the upload `up:` row without a P1 `fs:` row. The no-GC control also passes. This is a K1 orphan/over-retention result (the existing [G4 delayed-PUT follow-up](./X1-CRITICAL-PATH.md#confirmed-e1--pre-gc-dependency-from-g4-cross-audit-2026-10-02)), not X1 RED and not closure of W2-3 or the stale-delete ABA issue. |
| E1-02 losing-target repair replay | PARTIAL; row remains OPEN | `TestW2WorkerRepairLifecycle/lateRepairDoesNotStallCommittedDelete` replays the production repair visitor after terminal D for a commit that lost HEAD. The production resumable classifier returns `unknown` without error, and the actual visitor returns the typed `retained` outcome (operational/renewal failures are rejected); the row and its repair-owned `pub:` liveness remain, HEAD is unchanged, and neither P1 nor K1 returns. This does not exercise a repair whose commit is already reachable from HEAD after D, so it is not evidence closing the master late-publication race or W2-11/12. |
| E1-01 and E1-03 | UNRUN in this evaluation | Existing G4/G5 tests are controls only; this branch did not repeat the full crash-point or upload/dedup post-D matrix. |
| E1-04 | PARTIAL; row remains OPEN | The delayed-PUT case above covers only the held physical-write continuation. Sync HEAD, auto-merge and cross-pod retry after D are not evaluated here. |
| E1-05 through E1-15 | Historical initial UNRUN snapshot; subsequent scoped evidence below supersedes it; broad rows remain OPEN | E1-7 adds measured direct RecvFS-first fresh/reuse and retirement continuations for E1-05; no full matrix closure. |

Source trace with subsequent scoped dispositions:

- SeafHTTP single-shot E1-5a reproduces HEAD/fs:P1 after COMMITTED/TERMINAL
  retirement and fixes the measured pre-HEAD contract by carrying original P
  through retries to exact-P validation after repair. Real HEAD-conflict and
  P2 replay controls are included. E1-5b also fixes measured streaming pre-HEAD
  with original placements and selective same-tracker recovery. E1-07 is PARTIAL:
  post-HEAD/R31 and broader W2-7 remain OPEN. See [evidence](./E1-5A-SEAFHTTP-SINGLE-PUBLICATION-SAFETY.md).
- Sync `RecvFS` stores the authorized fs-object projection but does not itself
  publish HEAD or create block liveness. HEAD publication is a separate Sync
  step; the existing no-GC `RecvFS-before-PutBlock` integration case is not a
  post-D interleaving. E1-7 now supplies the separately scoped direct ordered
  evidence below; W2-5 overall stays OPEN.
- `CreateFileFromBlocks`, v2 `UploadFile` and Office-template `CreateFile` use
  staged `pub:`/repair state and exact-placement checks before HEAD in their
  covered paths. Existing pre-HEAD controls are not post-D E1 results, and
  their applicable R31 rows stay OPEN.
- OnlyOffice callback originally had no final exact-P check. E1-4 reproduces
  HEAD/fs: after COMMITTED and TERMINAL P1 retirement and adds the minimal
  exact-P validation after repair, before HEAD. Measured pre-HEAD safety and
  terminal P2 replay are CLOSED-FIX; post-HEAD/R31 W2-8 remains OPEN.
- Cross-repository copy/move in `BatchOperationHandler.processSingleItem`
  copies source fs objects, stages destination `pub:` references and durable
  repair before HEAD, then promotes after HEAD. E1-09 now adds source exact-P
  capture per attempt and final validation after repair: the measured productive
  source-purge retirement schedule is CLOSED-FIX; W2-9 overall stays OPEN.
- Content-resurrection handlers rebuild a tree from historical fs objects without
  own up:/pub:/repair. Retained-history subsets block GC before D. E1-10e runs
  a different real schedule: unpublished PutCommit/RecvFS metadata persists while
  owned temporary liveness lapses, GC retires exact P1 at COMMITTED/TERMINAL,
  and the original RevertDirents publishes HEAD depending on that retired P1.
  The measured non-directory file source-admission subset is now CLOSED-FIX:
  settled historical fs: via LQ→EQ fallback, original exact-P capture and recheck
  immediately before every HEAD CAS. This is not continuous own liveness through
  HEAD; directories/batches, other resurrection handlers, retention/Phase5/6,
  R31 and W2-10 overall remain OPEN. [E1-10e](./E1-10E-W210-RESIDUAL-DISPOSITION.md).
- The Sync idempotent-repair path can enqueue a repair for an already-current
  HEAD and intentionally has no retroactive exact-P rejection. This branch did
  not construct that state through a supported current flow after D; W2-3 and
  the applicable R31 rows stay OPEN.

No E1 row is marked CLOSED by non-reproduction. In particular, no reachable
post-D repair was exercised, the restart matrix is incomplete, W2-11..14 remain
open, X1 is not closed, and destructive GC remains OFF. These results authorize
no PRE-GC or startup-gate transition.

## Cross-audit hardening — 2026-10-03

The three test/evidence findings are addressed without production runtime edits:

- Both delayed-PUT and no-GC fixtures clean the current canonical storage key
  before metadata teardown, including rematerialized K2, and their exact Sync
  `up:` referrer. Independent teardown reads verify the physical object,
  canonical expiry tracker and durable by-day expiry projection are absent.
- E1-02 requires the real resumable classifier to return `unknown` with no
  operational error, then requires the actual visitor's typed `retained`
  outcome. Retention alone does not certify UNKNOWN. The test also verifies
  the durable repair identity and its exact repair-owned `pub:` referrer.
- The tested backend is pinned SILO, as recorded above. Historical MinIO
  evidence outside this E1 slice is unchanged.

The additional verifier correction covers a competing G5 worker completing
between recovery-root discovery and the exact orphan read. A clean missing
orphan is accepted only after the existing terminal certificate/object/root
checks pass. `TestW2RecoveryCompletesBetweenRootListAndRead` drives that
interleaving through the real worker; it is a harness regression control,
not closure of E1-01's crash/restart matrix.

Audit validation: PASS, all sequential in a single Docker runner built from
`e0358bd4b` plus this audit-hardening diff. The standard `go-all-test` command
(Go unit/integration, API and OIDC) and normal/integration `go vet` pass; the
integration suite completed in 457.946s. Ten `-race -count=10` repetitions of
`TestE1DelayedPutCannotRestoreRetiredPhysicalLife`,
`TestW2WorkerRepairLifecycle` and
`TestW2RecoveryCompletesBetweenRootListAndRead` pass (56.212s).

Three container-only mutations are RED for their own assertion: removing
cleanup leaves the physical object, canonical expiry tracker and by-day
projection; substituting
an arbitrary DB error yields a `failed` visit instead of `retained`; removing
the new discovery/read fallback loses the committed continuation after the
competing real worker finishes. Source was restored before the final race run;
mutation-only teardown also removes its exact artifacts. No host source was
mutated by these experiments. Optional 3DC/proxy/saturation legs are not claimed
as passed, and no E1/X1 disposition or production GC activation gate changed.

## E1-2 reachable-HEAD gate evidence — 2026-10-03

On main@c1f31f7ec plus the E1-2 test, a real Office CreateFile publication wins
HEAD and stops before permanent-reference promotion. The test verifies the
HEAD/commit/tree/block chain and lets its actual temporary references expire in
Cassandra using a two-second TTL time control through the reference API.
The surviving durable repair yields REPAIR_GUARD_ONLY; the real worker
postpones before destructive handoff. A fresh-session productive visitor then
classifies REACHABLE and settles to the exact fs: without changing HEAD/P1/K1.

This is positive evidence of the existing pre-D gate for the covered funnel,
not a post-D reachable-repair execution. No COMMITTED or TERMINAL D is forced.
E1-02, W2-11/14 and X1 remain OPEN; concurrent renewal/cleanup and other funnels
need separate disposition. The no-GC control, experiment, source exclusion
argument and validation are in [the E1-2 ledger](./E1-2-REACHABLE-HEAD-LATE-REPAIR.md).

## E1-3 late publication / negative repair scan — 2026-10-03

The [E1-3 plan and evidence](./E1-3-LATE-PUBLICATION-PRE-D-PROOF.md) records
two directed PASS orderings for real Office CreateFile/shared repair:
late repair acquisition under an already-held claim is rejected at final
exact-P, leaving HEAD unchanged; reachable settlement during the proof writes
fs: before deleting repair, so a subsequent negative scan is protected by the
second EACH_QUORUM refs read. Its original HEAD/tree/P1/K1 survive without D.
A no-GC writer control succeeds. Driver observation leaves actual query
responses unchanged. This is not closure of every pre-D schedule, W2-11/14,
other funnels or X1, and grants no activation transition.

## E1-5a SeafHTTP single-shot pre-HEAD — 2026-10-04

The [E1-5a frozen plan and evidence](./E1-5A-SEAFHTTP-SINGLE-PUBLICATION-SAFETY.md)
records productive HandleUpload baseline RED after exact COMMITTED/TERMINAL P1,
fixed 409 rejection with unchanged HEAD and owned cleanup, normal success/P2
replay, and a real HEAD-conflict retry validating the same original P again.
Mandatory evidence requires all four named legs. This closes only measured
single-shot pre-HEAD safety: E1-07 remains PARTIAL, streaming and post-HEAD/R31
W2-7 remain OPEN, as do W2-11..14 and X1. No activation transition is granted.
## Topology, exclusions and exit

One stack at a time. Start with single-DC Cassandra plus real SILO for decisive writer/recovery races. Add isolated 3DC only when actors differ by DC or SERIAL/EACH_QUORUM visibility is material; name the assertion it proves. Use healthy Cassandra 5.0.9, RF1/DC and dedicated SILO. Record exact crash point/DC/read levels. G4/G5 are controls; repeat their shared executor only if modified.

No runtime edit before this matrix is frozen in a base-referenced commit. If GREEN, close only rows supported by positive §4 evidence or an extant tested gate; else OPEN and X1 stays open. On RED fix minimum cause and rerun exact leg, regression, real Cassandra/SILO, crash/restart and required DC leg. Preserve RED/UNKNOWN/incomplete results.

Out of scope: G5 clock-health/scheduler, stale-claim settlement, Phase 5 shared-fs_object cascade, Phase 6 execute-time TOCTOU, full PC-D1B.5, A1 startup gate, GC activation, future funnels, speculative coordinator/scheduler redesign. Track separately unless evidence proves the exact post-D reachable-P1 violation on supported code.

Final audit: full Go, E1-required integration, race for changed packages, normal/integration vet, decisive single-DC legs and required 3DC, all sequential in Docker. Re-audit complete diff. Update this matrix, X1/KNOWN_ISSUES, CURRENT_WORK and evidence docs. E1 may recommend X1 CLOSED only if every applicable §4 row has valid disposition and no current supported funnel publishes P1 after D. GC stays OFF; PRE-GC/A1 remains distinct.

## E1-4 / W2-8 OnlyOffice callback

[E1-4 plan and evidence](./E1-4-ONLYOFFICE-PUBLICATION-SAFETY.md) records
current-behavior RED for a real callback resuming after exact P1 COMMITTED and
TERMINAL retirement. The fix retains the original materialized placement
through durable repair and validates it immediately before HEAD. Normal save,
rejection/cleanup and terminal replay at P2 are covered; expiry is an explicit
state control. Only this pre-HEAD contract is CLOSED-FIX. E1-08 post-HEAD repair,
concurrent cleanup/recovery and the shared R31 matrix remain OPEN.

## E1-5b SeafHTTP streaming pre-HEAD — 2026-10-04

The [E1-5b frozen plan and evidence](./E1-5B-SEAFHTTP-STREAMING-PUBLICATION-SAFETY.md)
reproduces COMMITTED/TERMINAL victim HEAD/fs: violations through productive
chunked HandleUpload before the fix. The fix retains each original exact P
through tracker and metadata retries, validates after durable repair before
HEAD, and selectively invalidates rejected digests for productive
same-tracker rematerialization. Real HEAD conflict proves repeated reads for
all original blocks; omission restores RED. Unit controls cover state/authority
failures and coherent/contradictory duplicates. Only measured streaming pre-HEAD
and selective same-process retry are CLOSED-FIX. This supersedes the streaming
OPEN snapshot in the earlier E1-5a section, not its scope. E1-07 remains PARTIAL;
post-HEAD/R31, W2-11..14 and X1 remain OPEN. Production GC stays OFF.

## E1-6 / W2-10a RevertFile retained-history — 2026-10-04

The [E1-6 frozen plan and evidence](./E1-6-REVERTFILE-PUBLICATION-SAFETY.md)
uses productive historical upload, DeleteFile and RevertFile. Leaving current
HEAD retains historical fs:; after explicit lapse of only the upload's up:,
the real block worker sees a positive local reference, settles the controlled
candidate and stops before claim/global zero-proof. An independent global read
also observes real liveness. One/two-block, actual competing HEAD retry,
same-content and skip controls preserve original historical layout/P/K.
Current RevertFile still creates no own pin/pub:/repair/exact-P sequence.
No COMMITTED/TERMINAL leg, retirement RED or runtime fix is claimed. This
measures only the retained-history schedule; removal of historical metadata
or references, Phase 5/6, multi-DC visibility and post-HEAD recovery are not
certified. E1-10 and W2-10 remain OPEN, including all other resurrection funnels.
The E1-10 hypothesis/exit criteria above are unchanged. X1 remains OPEN.

## E1-7 / W2-5 direct RecvFS-first continuity — 2026-10-04

[Plan and evidence](./E1-7-SYNC-RECVFS-BEFORE-PUTBLOCK.md): six named current
productive Sync/Cassandra/SILO legs cover fresh metadata with no P/mapping,
pre-existing web-upload P1, productive COMMITTED/TERMINAL retirement after
explicit lapse of its temporary pin, early-HEAD rejection and productive
PutBlock rematerialization to P2. After PutBlock, controlled GC observes real
up:sync and skips early; HEAD uses existing repair/exact-P machinery and settles
permanent fs: before deleting repair. Bytes and metadata/replays are verified.
No runtime fix. Only the measured direct ordered contract is CLOSED-EVIDENCE;
E1-05 is PARTIAL and W2-5 overall OPEN. Auto-merge, concurrent durable boundaries,
other orders, multi-block/DC and post-HEAD/R31 are not certified by this slice.
Production GC remains OFF; E1/X1 are not closed.


## E1-09 / W2-9 cross-repo source-purge retirement — 2026-10-05

[Plan/RED/fix/evidence](./E1-09-CROSS-REPO-PUBLICATION-SAFETY.md): productive
source library purge (not Phase 5/6 and not manual fs: deletion) allows actual
GC COMMITTED/TERMINAL after source metadata read. Both copy and move originally
publish unsafe destination HEAD/fs:; move's eventual source failure is not
rollback. Exact source P capture per retry and final validation after repair
reject retirement, while repair-first protects P1 even after actual pub: expiry.
Fourteen named legs include normal controls, captured-P windows, HEAD-conflict
retry and source move ordering. Only measured single-file/block plaintext,
same-org/representation pre-HEAD is CLOSED-FIX. E1-09 PARTIAL, W2-9 overall and
post-HEAD/R31 OPEN; E1/X1 and production GC OFF unchanged.


E1-09 cross-audit stabilization (2026-10-05): exact COMMITTED continuation is
isolated in the profile-test sesamefs-e19 backend/keyspace. The shared primary
again inherits GC_ENABLED and retains daemon-dependent coverage. The preceding
blanket-GC-OFF solution at ccdebcb72 is superseded as P2 TEST-INFRA, not accepted
as final standard-suite evidence. Both standard runners require the complete
14-leg isolated child test process. Exact D/P/time, COMMITTED orphan/root,
canonical absence and extant K1 remain certified before/after writer assertions,
before explicit recovery; TERMINAL is never accepted in those legs. Unknown
workers sharing the isolated keyspace remain outside the harness certificate.

Final restored-daemon go-all-test PASS: integration 845.139s, API 20/20, OIDC
25/25; E1-09 14/14 and standard daemon-dependent cases 14/14, zero disabled-GC
skips. Two pre-existing harness races revealed by restoration were corrected
without changing runtime or accepting peer activity as the own worker proof.
The exact native peer-window scratch control and full native matrix PASS.

## E1-10 / RestoreTrashItem retained-history — 2026-10-05

The [frozen plan and evidence](./E1-10-RESTORETRASH-RETAINED-HISTORY.md) measures
only single-file/one-block/plaintext restore in one repo/org with retained
history. Real upload/DeleteFile retain the sole permanent fs:. At the actual
oldEntry read barrier, the real worker observes positive local liveness, settles
the candidate and stops before claim/global proof/D. Restore reaches the original
fs_id and retains P/K/bytes without new repair or fs: settlement. Real competing
HEAD/CAS retry preserves both restored and concurrent entries.
CLOSED-EVIDENCE applies only to this measured retained-history schedule.
COMMITTED/TERMINAL legs remain UNEXECUTED because that schedule prevents their
prerequisite; no refs/D are fabricated. E1-10/W2-10 overall, other resurrection
funnels, retention/Phase 5/6, R31, E1 and X1 remain OPEN. No runtime fix or
activation change; production GC OFF. RevertFile's earlier scope is unchanged.

## E1-10b / RestoreTrashItem directory retained-history — 2026-10-05

[Directory evidence](./E1-10B-RESTORETRASH-DIRECTORY-HISTORY.md) uses productive
CreateDirectory, parent-bound HandleUpload, DeleteDirectory and actual restore.
A single directory/child/plaintext block retains sole child fs: while absent from
current HEAD. Real worker settles the exact candidate before claim/global proof/D.
Restore publishes the named full subtree with original exact P/K/bytes; a real
competing HEAD forces retry and survives. No repair/fs: settlement or runtime fix.
Own async deletion housekeeping is synchronized through real observed queries.
Separate three-leg gate/selector/child preserve #259's evidence contract and normal
dev GC availability. CLOSED-EVIDENCE only for the measured retained-history slice;
COMMITTED/TERMINAL unexecuted. W2-10/E1-10 overall, other funnels/broader shapes,
retention/Phase5/6, R31, E1/X1 remain OPEN. Production GC OFF. RevertFile prior
evidence is not reclassified, and no exclusively Phase5/6 residual is inferred.

## E1-10c / RevertDirectory retained-history — 2026-10-06

[Directory revert evidence](./E1-10C-REVERTDIRECTORY-RETAINED-HISTORY.md)
measures one absent historical directory/child/plaintext block in one DC.
Actual handler captures DIR1 outside retry; inherited child fs: blocks real GC
before claim/D, settling candidate. Revert publishes full named subtree with
original exact P/K/bytes and no new publication liveness. Real competitor forces
CAS retry (historical=1, HEAD=2), preserves both and becomes winning parent.
Separate integration hooks, gate/selector/child leave #259/#260 contracts and
standard GC intact. CLOSED-EVIDENCE only for this retained-history subset;
COMMITTED/TERMINAL unexecuted. W2-10/E1-10 overall, RevertDirents, broader cases,
retention/Phase5/6, R31 and E1/X1 remain OPEN. Production GC OFF. No speculative
fence or retroactive RevertFile reclassification; residual risk is not declared
exclusively Phase5/6.

## E1-10d / RevertDirents single-file retained-history — 2026-10-06

[Dirent evidence](./E1-10D-REVERTDIRENTS-RETAINED-HISTORY.md) measures a single
root file/path/plaintext block, absent current target, retained historical
commit in one DC. Actual form handler captures oldEntry outside per-item retry.
Inherited sole fs: blocks real worker before claim/global proof/D and settles
controlled exact candidate. HEAD reaches original named FILE_FS1/P/K/bytes;
real competitor causes two CAS attempts, capture once, winning parent equals
competing HEAD and both files survive. No new pub:/repair/fs: promotion.
Independent hooks/gate/selector/child preserve old contracts and standard GC.
CLOSED-EVIDENCE only for this measured schedule; COMMITTED/TERMINAL unexecuted.
W2-10/E1-10 overall, broader RevertDirents batch/shapes, retention/Phase5/6,
R31 and E1/X1 remain OPEN. RevertFile prior disposition unchanged. Reevaluate
remaining publication risks separately before any PRE-GC reclassification;
no inference that all remaining risks belong to Phase5/6. Production GC OFF.


### E1-10e: unpublished source counterexample and scoped fix (2026-10-06)

Base main@5071d1701624b291a95db9ade8b5ff6abdbd63d7. Productive web upload
followed by Sync PutCommit/RecvFS persists an unpublished commit/tree/file with
SHA-1 block IDs and a canonical mapping, but no permanent fs: or repair.
Only the owned temporary upload reference exists. After its controlled lapse,
real GC reaches COMMITTED or TERMINAL for exact P1 without removing history.
The original RevertDirents handler then successfully publishes HEAD depending
on that retired P1. This is a source-admission counterexample outside Phase5/6;
metadata existence does not prove retained permanent liveness.

RevertDirents non-directory items now require their own settled
fs:<repo>:<historical-file> for every canonical block, capture original exact P,
and recheck those references plus existing exact-P authority immediately before
each HEAD CAS. Missing/malformed/unknown evidence fails the item with HEAD intact.
Live unpublished metadata is deliberately rejected too; normal Sync publication
followed by deletion and restoration remains supported. No pin/pub:/repair is
acquired, no bytes are rematerialized and commit ancestry is not certified.

Measured one root file/path/plaintext block, same repo/org and one DC:
RevertDirents source-admission subset = CLOSED-FIX. Directories, other handlers,
broader batches/layouts, concurrent retention/cleanup, Phase5/6 and R31 remain
OPEN. Foreign settled fs: is not a continuous own-pin proof through HEAD.
W2-10/E1/X1 remain OPEN; production GC OFF, shared development GC unchanged.
See [frozen plan and evidence](./E1-10E-W210-RESIDUAL-DISPOSITION.md).


### E1-11: current-version repair liveness disposition

Six named real Office/repair/GC legs cover actual up:/pub: expiry during and
after classification, clean UNKNOWN retention, applied-HEAD classifier loss
and destructive pending-scan loss. The non-expiring pending repair prevents
new D at zero real refs; REACHABLE acknowledges fs: before deleting repair.
Omitting only the existing guard produces exact COMMITTED D(P1), followed by
productive terminal recovery and verified cleanup. See [source-pinned evidence](./E1-11-CURRENT-RUNTIME-REPAIR-LIVENESS.md).

E1-11 / W2-11 = CLOSED-EVIDENCE only for covered current-version pre-D safety
with repair already acquired by an adopted writer. This supersedes the old
UNRUN/renewal-gap diagnosis for that contract, not the frozen attack matrix.
No post-COMMITTED revocation, discovery/completion bound, concurrent cleanup,
W2-12..14, other funnels, Phase5/6 or broader E1/X1 closure is claimed.
Production GC remains OFF; the shared dev daemon remains unchanged.

## E1-12 / W2-12 — definitive-loser crash pre-D evidence

[E1-12](./E1-12-KNOWN-LOSER-CRASH-SAFETY.md) measures one Office/CreateFile
plaintext block after actual HEAD CAS applied=false and Linux SIGKILL before
cleanup. Productive GC cannot create D while its non-expiring repair remains;
fresh-process productive sweeps retain UNKNOWN and renew exact repair-owned pub:
without promotion. Normal cleanup/retry and six named legs are covered; the guard
omission reaches COMMITTED and finishes owned TERMINAL recovery/cleanup.
W2-12 overall remains OPEN for durable loser authority and cleanup/convergence.
W2-13/14, concurrent cleanup, broader funnels, E1/X1 and activation remain OPEN.
Full standard regression and final audit status belong to the linked evidence.

## E1-13 / W2-13 — repair discovery delay safety

[E1-13](./E1-13-REPAIR-DISCOVERY-DELAY-SAFETY.md) measures real Office repairs
behind naturally earlier buckets, with target unvisited and zero refs. Productive
GC's direct EQ repair guard vetoes new D independently of sweep discovery; later
native UNKNOWN visits retain repair and renew exact pub without HEAD/fs promotion.
A fresh-process restart rediscovers backlog. Guard-only omission reaches exact
COMMITTED and completes owned TERMINAL recovery. Every fixture requires independent
metadata/bytes/expiry/pending-owner canonical+projection cleanup.
Only the measured Office delayed-discovery pre-D subset is CLOSED-EVIDENCE.
The frozen E1-13 attack row above asks for a reachable repair under backlog and
post-D continuation. This slice uses an unpublished/native UNKNOWN target and
measures a pre-D veto; it does not execute or close that broader reachable/post-D
row. E1-11's reachable evidence is separate, not a substituted backlog schedule.
W2-13 overall discovery/convergence/scale remains OPEN; no arbitrary delay, SLA,
fairness, completion or same-org GC scan scale bound. W2-12/14, concurrent cleanup,
broader E1/X1 and activation remain OPEN. Final validation belongs to linked evidence.

## E1-14 / W2-14 — pub: expiry zero-ref transition

[E1-14](./E1-14-PUB-ZERO-REF-TRANSITION.md) uses real Office/CreateFile `pub:`
references. Without a repair, the writer stops after `pub:` staging and before
repair queueing; with one, the E1-13 durable UNKNOWN shape is used. `up:` is
moved through the productive renewal API, then retired by Cassandra TTL, and
Phase 0 resolves it while `pub:` keeps the block live. The final `pub:` is
retired by actual TTL to global EQ zero refs. Owned-scope Phase 0, Phase 1 and
the productive worker then create no candidate, queue row, D or recovery root,
and P1/K1 stay retained. In the repair schedule the candidate is absent before
any visit; the later native UNKNOWN sweep renews the exact repair-owned `pub:`
without fs:/HEAD/P change. A disposable mutation that gives `pub:` the `up:`
expiry projection makes the same leg reach a productive candidate.

Measured safety subset: CLOSED-EVIDENCE / NOT-X1-RED (`pub:` expiry alone cannot
reach D). W2-14 transition/convergence stays OPEN as the confirmed P2 retention
gap `ISSUE-GC-PUB-REF-ZERO-REF-01`. The frozen row's "scanner to D" step is not
reachable through this transition in current runtime and was not executed. A
future `pub:` projection must re-run this matrix with the guard-omission
control. Final validation belongs to the linked evidence.

## E1-15A — stale repair visitor vs concurrent durable cancellation

[E1-15A](./E1-15A-STALE-REPAIR-VISITOR-CANCELLATION.md) holds a real Office
writer before HEAD with durable R. The productive sweep classifies R natively
UNKNOWN and pauses. Every CreateFile retry then meets a real competitor HEAD
and runs real known-loser cleanup, clearing R. Real `up:` TTL expiry gives a
natural candidate, and the productive worker reaches exact COMMITTED D(P1)
before the visitor resumes. Paused before its durable re-checks, the visitor
does not renew. Paused after the last re-check, current runtime was **RED**:
it wrote `pub:<repo:commit:fs>` after D, and the post-renewal local gone
observation kept it.

Fix: post-renewal local absence escalates to EACH_QUORUM; confirmed global
absence withdraws exactly that pin, and unavailable authority retains. Measured
Office single-block subset: PARTIAL-FIX / CLOSED-EVIDENCE only when the global
check and withdrawal succeed; crash, check/withdraw errors and ambiguous renewal
still leave the pin.
This is compensation after the write, not a fence; the strict post-D
no-reference guarantee stays OPEN (P1 PRE-X1). Two causal mutations (revert fix;
bypass re-check) reproduce the post-D pin. Crash/withdraw-failure/unavailable
residue (TTL-bounded), funnel-specific E1-15 rows, multi-DC measurement,
E1/X1 and activation stay OPEN. Final validation belongs to the linked
evidence.

## E1-15B — writer publication after D, crash before the final fence

[E1-15B](./E1-15B-WRITER-POST-D-STAGING-CRASH.md) holds a real CreateFile
writer subprocess after materialization. Its real `up:` is retired by TTL and
the productive GC reaches a natural exact COMMITTED D(P1). Resumed, the writer
stages `pub:` post-D. Without a crash the final fence rejects it (409) and
cleans up. SIGKILL after staging leaves a durable `pub:` (35d). SIGKILL after
repair queueing and insertCommit leaves `pub:` plus a non-expiring repair, and
two productive sweeps classify UNKNOWN and renew `pub:<repo:commit:fs>` after D.
Characterized P1 PRE-X1 class, not fixed; the pre-HEAD guarantee holds. A
shared fix must also settle repairs for attempts that never reach HEAD. Other
funnels (unmeasured), E1/X1 and activation stay OPEN.

## E1-15C — post-D references, terminal retirement and P2

[E1-15C](./E1-15C-POST-D-PHYSICAL-LIFE-DISPOSITION.md) reuses the E1-15B
schedules. The productive continuation completes D1 to TERMINAL with the
post-D `pub:` and the dead repair present: K1/orphan/root gone, canonical not
reinstalled, HEAD unchanged. A real CreateFile then rematerializes the same
content as P2 (new key) and publishes HEAD on P2 only; the new file shares R's
content-addressed fs_id. The dead repair stays UNKNOWN and re-pins L. Once
every real reference of an unreferenced P2 has expired, the productive worker
vetoes D(P2) only because of the dead repair. No physical RED on Office; the
strict E1 reading stays RED; a contract reconciliation and dead-repair
settlement as the next step are proposed for explicit decision.

## E1-15F — SUPERSEDED across datacenters

[E1-15F](./E1-15F-SUPERSEDED-CROSS-DC.md) runs the production sweep on real
three-DC Cassandra. Commits, HEAD, repairs and refs go through production
functions. Only library identity and fs_objects content rows are CQL
fixtures. Results:

- HEAD = parent: UNKNOWN, retained.
- One DC down: UNKNOWN on the witness read, retained.
- After recovery, a sweep from another DC settles SUPERSEDED, verified from
  two DCs at EACH_QUORUM. Only R's row and its own `pub:` go.
- REACHABLE still classifies and promotes.

It also fixed a #275 liveness defect: a failed parent read no longer lets
the walk pass the parent without the witness.

## E1-15E — superseded abandoned repair settlement

[E1-15E](./E1-15E-ABANDONED-REPAIR-SETTLEMENT.md) adds the one negative
outcome with cleanup authority: SUPERSEDED, when the resumable walk from the
SERIAL HEAD anchor reaches the repair commit's immutable parent before the
commit and the anchor is not that parent. Monotonic HEAD makes it permanent.
Settlement removes only the repair-owned `pub:` and the row. Measured on real
writers, sweeps and Sync: HEAD = parent still retains; a superseded repair
settles and a natural D(P1) then reaches COMMITTED; Sync after the settlement,
and Sync paused before its repair or before its CAS, publish with their own
references while Phase 0 makes no candidate; a stale renewal is withdrawn.
E1-15D, E1-15C C1 and E1-12 crash-restart were amended because their repair
now settles. Still retained: HEAD = parent, a missing commit row.

## E1-15D — contract decision and repair cancellation authority

[E1-15D](./E1-15D-POST-D-CONTRACT-DECISION.md) audits every HEAD writer: all
are CAS moves to a child of the current HEAD, so HEAD is monotonic. It also
lists the publication paths for an existing commit. Evidence: after a real
writer crash leaves R and commit c1, real Sync `UpdateBranch?head=c1`
auto-merges c1's content into HEAD (200), so R protected a still-publishable
commit. After P1 is TERMINAL, the same promotion is rejected by the Sync
publication readiness gate (attributed via the handler log). Proposed for
approval: X1 = physical invariant, post-D references and abandoned repairs =
PRE-GC convergence. A cancellation-authority matrix records that no
authority exists today. A post-merge productive sweep still classifies R
natively UNKNOWN and retains it. The next PR evaluates the minimal
settlement first (durable authority over the original attempt plus a
concurrency analysis against Sync promotion, which takes its own
protection); global commit cancellation is a fallback, not a requirement.
A settlement never removes `fs:<repo:fs_id>`. No severity changed.

## E1-16 — post-D contract alignment

[E1-16](./E1-16-POST-D-CONTRACT-ALIGNMENT.md) records the owner's approval of
E1-15D §1 on 2026-10-10 as alignment with G4 contract 3–5 (#247): D0 §3 already
defines references as logical liveness of L, and the COMMITTED recovery path
never consults L's new references (`internal/gc/worker.go`). The invariant
paragraph above now states the physical reading. The frozen matrix RED
conditions were already physical and are unchanged.

Against code read at main@5c3163871803, the R31 safety rests on a
funnel-independent guard: repair rows have no TTL, and the destructive
EACH_QUORUM scan matches `staged_block_ids` regardless of classification or
writer. Every repair-adopting funnel queues before HEAD. Sync validates every
added block after repair regardless of provenance, `RecvFS` publishes nothing,
and cross-repo copy captures and re-validates the full recursive block set.
X1 dispositions: W2-1..9 and W2-12..14 CLOSED for X1; W2-10 OPEN.
E1-15A/B and the R31 convergence residuals move from PRE-X1 to PRE-GC with
severity unchanged. The 2026-10-02 G4 cross-audit dependency is CLOSED-EVIDENCE
for repair-adopting funnels (E1-2/E1-11).

Still before X1 CLOSED: W2-10 (E1-17); E1-01, first inventoried against the
existing G1–G5 crash evidence; the three-DC residuals (28 of 30 G5 failures now pass); the D0 §25
reclassification list; and the identity claim-table PRE-X1 audit debt.
No runtime, test or guard changed. GC stays OFF.
