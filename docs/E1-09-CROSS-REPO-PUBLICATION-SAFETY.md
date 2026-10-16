# E1-09 / W2-9 cross-repository publication safety

Date: 2026-10-05. Base: main@975b3057db3e0aefbc09e41e7beeaf0ce68193e7.

## Frozen plan

Characterize current runtime before changing it. One plaintext file, one block,
same organization and block representation, both real AsyncBatchCopy and
AsyncBatchMove -> processSingleItem paths. Copy reads source metadata into a
pending in-memory object; staging persists destination metadata before pub:.

Controls: normal copy/move, retained source fs: during GC, and destination
repair-first protection. Investigate productive source library deletion/purge
and its real worker cascade as an alternative to Phase 5/6. Never delete source
fs: by CQL to manufacture a race. Temporary upload expiry-state controls and
fixture-scoped candidate discovery are explicit controls, not elapsed TTL proof.

Pause after actual source metadata read and before destination pub:. If productive
source cleanup can release all source liveness, drive actual block GC to exact
COMMITTED and separately TERMINAL, then resume. Assert destination HEAD/tree,
exact source/destination fs:/pub:, durable repair, canonical P, lifecycle/root,
K1/bytes, task completion and move source ordering. The RED criterion is durable
reachable destination dependence on retired P1, not endpoint acknowledgement.

GREEN: document only the measured contract. RED: freeze reproducible unchanged
runtime evidence, implement minimal existing exact-P/liveness adapter after
repair and before HEAD, and demonstrate RED -> GREEN and omission sensitivity.
Retries must capture authority per attempt; unknown/error fails closed.

No directories, multi-block/DC, encrypted cross-representation, W2-10,
R31/W2-11..14, Phase 5/6 fixes, coordinator, PRE-GC/A1 or GC activation.

Final validation in Docker: directed characterization, meaningful mutation
control if runtime changes, race repetitions, Go regression/vet and appropriate
standard suite. Audit all changes and claim limits, commit/push and create PR.
## Frozen unchanged-runtime RED

Directed Docker run on production source main@975b3057: integration FAIL
(16.328s). Normal copy/move PASS. Both copy and move publish destination HEAD
and permanent destination fs: after productive source library purge and exact
P1 COMMITTED, and separately TERMINAL/K1 absent. Copy reports task success;
move reports failure later because source library is already gone, but its
destination HEAD/fs: are already published. Task failure is not rollback.

Source deletion used actual owner HTTP soft-delete and permanent-delete routes;
real source-library queue/cascade/processFSObject removed the permanent fs:.
Only source-owned temporary up: was explicitly lapsed. No source fs: CQL delete,
Phase 5/6 scan, synthetic metadata/P/ref/claim or invented GC answer was used.
Completed real source SELECT response was paused before caller continuation.
Real EACH_QUORUM two-zero reads/full repair scan and exact COMMITTED/root,
TERMINAL/K1 deletion checks reuse the prior verified worker helpers.

Raw log: external $TEMP/sesamefs-e19-red.log. The safety assertions fail on this
revision; the next commit must make them pass. Existing HTTP normal copy/delete
regression already exists; the report's "never measured" claim is narrowed to
this previously untested pre-pub purge/retirement schedule.

## Fix and measured contract

Plan commit e3aa03c3b; unchanged-runtime RED frozen in d8153a52c. Cross-repo
now captures every distinct canonical source placement per destination attempt
with bounded LOCAL_QUORUM reads, before staging. It verifies that the destination
staged block set equals the captured set, then uses the existing final exact-P
primitive after durable repair and immediately before destination HEAD. Missing,
changed, blocked or malformed placement, query failure and unknown authority
cannot authorize HEAD. A failed final gate cleans its own pub:/pending rows and
repair. HEAD-conflict retry re-reads source and captures authority again.

No new up: is required for this measured fix: source fs: protects the borrowed
read until it disappears, and destination pub: plus non-expiring durable repair
protects the final check -> HEAD interval through the existing W2-0 protocol.
The early gap is permitted to lose to GC; original exact-P then rejects. The
repair-first control genuinely expires the actual pub: after source purge,
executes the productive GC candidate/guard, preserves P1/K1 and the candidate,
and continues publication. This is not a claim that a TTL-bound pub: alone is
continuous protection, nor a new repair/renewal design.

A race detector additionally reproduced task progress fields read after the
TaskStore read lock was released. GetTaskProgress now takes a value snapshot
under that lock; a concurrent-update unit regression validates consistent
responses under -race. This is the same asynchronous funnel's progress surface.

Fourteen required names (copy and move for each row):

| Schedule | Required result |
|---|---|
| normal | Source fs: retained; destination HEAD/paired-canonical metadata/permanent fs:, exact P1/bytes including real destination HTTP download, repair-write -> fs-write -> repair-delete. Explicit skip replay preserves destination HEAD/P. |
| committed | Pause actual source SELECT; owner HTTP soft-delete and permanent-delete, real library/fs_object cascade removes source fs:; actual block worker zero-proof/repair scan reaches COMMITTED and retires canonical P1. Continuation rejects before destination HEAD. |
| terminal | Same schedule plus exact real recovery to TERMINAL, absent K1/orphan/root before continuation; destination HEAD unchanged. |
| captured-committed | Pause completed actual physical placement SELECT: retain its P1 response while productive source purge and GC commit D. Late pub:/repair acquisition does not defeat final exact-P; reject and settle attempt. |
| captured-terminal | Same after exact physical retirement; canonical absence cannot be mistaken for fence clearance. Reject without destination reachability. |
| repair-first | Pause completed durable repair INSERT, purge source productively, shorten existing pub: TTL via reference API and observe real expiration. Productive GC guard preserves candidate/P1/K1 with no D; resume safe destination settlement. Copy succeeds; move reports later source absence while safe destination persists. |
| head-conflict | Actual competing empty-file CreateFile wins destination HEAD while copy is paused. First destination CAS loses; second attempt re-captures P, preserves competing tree content and settles. Source move HEAD occurs after observed destination fs: settlement. |

The handlers use actual owner Gin contexts, actual permission middleware and
real Cassandra/SILO IO; library purge uses the existing external HTTP owner
routes. The async task and productive helpers are used, not direct simulation
of processSingleItem outcomes. This does not certify token/auth middleware or
external routing for the in-process batch requests. Query observers pause
completed real responses; their rows/results are never replaced. Scoped worker
queue adapters filter source metadata or exact block candidates, not authority.
Source upload up: removal is an explicit expiry-state control, not elapsed TTL
proof; repair-first pub: expiry is actual Cassandra TTL observation. Worker
fixture grace/discovery controls do not certify production discovery timing.

After rejected COMMITTED legs, exact recovery deletes K1 during teardown;
TERMINAL replay remains idempotent. Successful fixtures remove only owned refs,
expiry projections, repairs and exact physical objects. No production fs: is
manually erased to reach zero; reference deletion in fixture teardown occurs
only after outcome assertions/task completion.

Disposition: **CLOSED-FIX for measured single-file/single-block plaintext,
same-org/same-representation cross-repo copy/move pre-HEAD publication**.
E1-09 is PARTIAL; W2-9 overall stays OPEN for unmeasured directories, multi-block,
multi-DC, encrypted cases, other concurrent publication boundaries and shared
post-HEAD/R31. W2-5 overall, W2-10, W2-11..14, E1/X1, Phase 5/6, coordinator and
PRE-GC/A1 are unchanged. Production GC remains OFF. Move atomicity/rollback
when the source disappears after safe destination publication is not changed.

## Validation

- Unchanged runtime: normal copy/move PASS; four genuine unsafe reachable
  COMMITTED/TERMINAL continuations FAIL (16.328s integration).
- First fix: all twelve initial directed names PASS (26.408s).
- Initial -race: safety checks pass, but exposes existing async progress races;
  fixed with the locked value snapshot, not by suppressing the detector.
- Expanded required fourteen legs and gate-wiring contracts PASS (26.418s),
  including productive HEAD-conflict retries and explicit skip replay.
- Go short/coverage, ten concurrent progress/authority unit race repetitions,
  normal vet and integration vet PASS. An initial test-only enum formatting
  compile/vet error was corrected before these checks passed.
- Isolated runner omission of only validateCopiedBlockPublication restores all
  four captured-P COMMITTED/TERMINAL REDs (10.259s). Source capture, staging,
  repair and HEAD remain productive. Runtime restored by trap; host/runner
  batch_operations.go SHA-256 match. The first omission script failed to compile
  due to an unused local; only the corrected, compiled counterexamples count.
- Ten required -race repetitions: PASS (289.247s), all 140 named legs and
  completeness/gate contracts. After adding real destination HTTP byte-download
  assertions, final Go source passes three more -race repetitions (93.393s),
  all 42 named legs; no reported data races.
- Required copy/normal-only filter: expected FAIL, thirteen other names missing.
  Required unavailable API/Cassandra: expected bootstrap FAIL before tests run.
- Standard go-all-test command on final Go source: PASS, integration 502.758s,
  all 20 API suites and 25/25 OIDC checks. The three dev nodes were rebuilt and
  redeployed with this production source before external HTTP validation.
- Independent owned-artifact check: PASS. All 196 keys from the final race,
  byte-download race and standard integration runs were already physically
  absent, with canonical rows, refs and owned expiry projections absent. Across
  245 exact logged fixture keys, 11 earlier RED/harness objects were cleaned
  only after verifying canonical/refs absence. No unrelated key was enumerated
  for deletion. Temporary maintenance test removed from runner; it is not in Git.
- Final normal and integration vet, gofmt and diff whitespace checks PASS.
  Final diff audit: no unresolved introduced P0/P1/P2. Wider open ledger rows
  and move's partial-operation semantics are not closed by that statement.

Logs are outside Git: $TEMP/sesamefs-e19-{red,green,race-initial,
final-directed,unit-vet-final,omission-final,race-final,download-race,filtered,
unavailable,full-suite,owned-artifacts,vet-final,final-images}.log.


Historical validation at PR HEAD 8d5596f9b (before the cross-audit corrections):
Host, validation runner and test image agreed on these Go SHA-256s:

| Source | SHA-256 |
|---|---|
| internal/api/v2/batch_operations.go | 14006009ee2c55f810628a232a0d1e086ebd2165bb0ad6f5723a9ac3578418d4 |
| internal/api/v2/cross_repo_publication.go | 8395ca6009ecde805979a572dd39ff160e10c72ced9fe2413139ee2bddcf5161 |
| internal/api/v2/cross_repo_publication_test.go | 3a846b9770f4ea4e4d0dae1f2a1be0e24397fe4c8f57c98187de6ab29409f018 |
| internal/integration/e19_cross_repo_test.go | 194e470119f320505d640fee65eeac42f6b46af51e5cc96aba4bfd814caa130f |
| internal/integration/integration_test.go | 6b8fa8dc4f4123716a7829ae4e92f9e02952e2399017d77e2a9449ff91739386 |

Test image: sha256:7ee5fadc31d612a931d8192dacc2b274d4920d72948541d165643d28e2827eb0.
Backend image: sha256:24561767c85e1895ae868fb6495bb7dc05f52018f08eb943e4ad497e6d793db5.
All three deployed dev nodes' /app/sesamefs SHA-256 matches
c01c149f6387ef0e43107536206cd155e5b6ed73f7cff716192b8f6104195144.
Cassandra/SILO evidence is single-DC; no optional 3DC phase is certified. Local
GC uses the existing dev configuration, including its background participant;
production activation/configuration and DB/GC runtime are unchanged.


## Cross-audit corrections (2026-10-05)

The external cross-audit of 8d5596f9b identified the real worker fan-out P2. Its proposed direct MoveFile P2 was subsequently retracted: MoveFile returns 501 for cross-repo before calling processSingleItem. The 409 mapper change is retained as correct hardening, not a current THIS-PR runtime fix. The earlier “no unresolved introduced P0/P1/P2” statement above describes that audit's conclusion and was incomplete: active-query semaphores did not bound goroutine creation. The mapper helper lacked a retirement-sentinel branch, but its direct cross-repo caller could not reach that sentinel.

Both capture and the shared final exact-P validator now use `runBoundedPublicationChecks`, with errgroup.SetLimit(20) before goroutine creation. Canceled queued jobs do not perform authority reads; scheduling stops after the first observed error, and the original worker error is returned. The shared validator's authority primitive, outcomes, captured P and ordering are unchanged. The direct MoveFile error mapper now handles wrapped and unwrapped ErrBlockDeleteInProgress like HEAD conflict (409/retry).

Regression checks hold the first 20 workers of a 131,072-item input and measure the actual goroutine population, then verify all checks complete; both the common capture scheduler and final validator are exercised. Additional checks cover fail-closed cancellation and wrapped/unwrapped direct MoveFile error responses. This resource test does not certify multiblock W2-9 physical-life publication.

The pre-existing P1 source identity race is registered as ISSUE-CROSSREPO-MOVE-SOURCE-IDENTITY-RACE-01 in KNOWN_ISSUES.md, confirmed by source inspection and left for a separate PR. Broader W2-9/E1/X1 and production GC disposition remain unchanged. Local dev GC configuration is not claimed OFF.

Correction validation (Docker):

- New resource/authority/MoveFile regressions: three -race repetitions PASS (8.369s). Both scheduler and final validator finish all 131,072 checks with bounded goroutine population; first-error cancellation preserves the original error.
- Entire Go short/coverage suite, normal vet and integration vet PASS on corrected source.
- Required E1-09 matrix: 14/14 named copy/move legs plus completeness contract PASS under -race (37.474s). Initial runner without Compose credentials/config failed before executing legs; this was not counted as evidence. The first correctly configured run passed 13/14 but the copy/committed checkpoint observed D1 already TERMINAL with the existing background GC active (59.423s FAIL). Its async session-close errors occurred after test failure/teardown, not as evidence of writer safety. The complete fresh-fixture rerun passed without source or dev GC configuration changes.
- Isolated /tmp source copy, omission of SetLimit and direct MoveFile sentinel mapping: expected compiled FAIL (0.144s), roughly 24,600 queued goroutines at the held-wave observation and both raw/wrapped sentinel responses 500 instead of 409. The real /build source and host were not mutated by this control.
- External HTTP backend images were not rebuilt for this correction; E1-09 invokes the corrected productive batch handlers in-process against real Cassandra/SILO, and external purge/download helpers use the existing dev nodes. Direct MoveFile status mapping is covered by handler error-response tests. The previous complete API/OIDC run and image hashes above remain historical evidence at 8d5596f9b, not a claim of rerunning those suites on this correction.

Logs outside Git: $TEMP/sesamefs-e19-cross-audit-{unit,short-vet,integration,integration-final,integration-rerun,omission}.log. Historical correction audit at cc5a9cc3d missed the remaining COMMITTED schedule evidence P2. The new isolation correction below supersedes that evidence verdict. The direct MoveFile mapper RED is a helper hardening control, not proof of a currently reachable cross-repo HTTP 500. The separately registered source-identity P1 remains OPEN.

Corrected source hashes (host and Docker validation runner):

| Source | SHA-256 |
|---|---|

| internal/api/v2/cross_repo_publication.go | ca1456a57a5f9dad0942d6041bd1bd88077e9f7c1f38fcf3ec553f3a6591cadd |
| internal/api/v2/cross_repo_publication_test.go | 533baf2ee18cd416986d146bcef88d2207e0d288c7ea576fcd5288565f464a77 |
| internal/api/v2/file_from_blocks.go | b36b8429043f23f92a5e02bd7804a059c6ecbd8e680d7b5f4770b1d5fb1dd162 |
| internal/api/v2/files.go | ddc4739c36c41fe949b7e6c1b2d57ccbbebbf5ccc0dc0383e9b3ed05bc0454bb |
| internal/api/v2/files_batch_test.go | aea40ef08602c1a9e1a116eb9e642800997e9779736eda1982ed15af9cf3803f |


## COMMITTED isolation correction (2026-10-05, historical ccdebcb72)

**Superseded below:** globally disabling the development daemon lost existing
coverage and is a P2 THIS-PR / TEST-INFRA blocker. The 14 skips and 555.283s PASS
do not qualify as the final standard-suite validation.

The crossed audit of cc5a9cc3d correctly identified P2 / THIS-PR TEST-EVIDENCE: an enabled external development recovery worker could move exact D1 from COMMITTED to TERMINAL between the certificate and the resumed writer. A later green rerun did not certify the claimed COMMITTED continuation schedule. Earlier active-background-GC COMMITTED legs are historical evidence and are superseded for that distinction.

The standard development/test Compose fleet now explicitly disables daemon GC on its primary as well as nodes 2/3. The primary can opt into separate development/admin-GC exercises with SESAMEFS_DEV_BACKGROUND_GC_ENABLED=true, but mandatory E1-09 then fails closed. No production configuration, GC/runtime primitive, schema or block authority is changed. Tests still execute real scoped workers and real recovery; productive source purge still queues metadata cascades. Existing enabled-daemon/admin-trigger tests use their existing disabled-GC skip behavior in the isolated standard integration run; this run does not certify their enabled-daemon behavior.

E1-09 checks authenticated GC status on every node in the controlled three-node Docker fleet before fixtures and again after COMMITTED writer assertions. Missing status, enabled=true, invalid payload or unavailable nodes fail the required package gate. For committed/captured-committed, the test strictly re-certifies the exact claim/P/time, published lifecycle, COMMITTED orphan, recovery root, absent canonical P1 and extant K1 immediately before resume and after task/HEAD/ref/repair assertions, before its own recovery. It never accepts TERMINAL as COMMITTED. Terminal legs continue running real recovery before resume. This certificate is scoped to the controlled Compose fleet; no unknown external worker or parallel admin-GC job may share its fixture datastore.

Retraction: direct MoveFile returns 501 before the cross-repo publication path. Its 409 mapping remains hardening. A direct-handler regression verifies 501 with no database, proving the early branch remains. The P1 source identity race remains separate and OPEN; the pre-existing two FileFromBlocks worker fan-outs are now registered as ISSUE-FILEFROMBLOCKS-WORKER-FANOUT-01, P2 GENERAL / OPEN.

Isolation validation (Docker, same existing Cassandra/SILO volumes):

- Original enabled fleet: expected required-evidence FAIL before fixtures (2.569s); status explicitly reported enabled=true. False/missing/malformed/unavailable status controls PASS.
- Five consecutive required -race matrices PASS (260.270s), independently counted 5 complete matrices / 70 named legs. Each COMMITTED leg re-certified its exact live D1 through task, HEAD/tree, reference and repair assertions before owned recovery. No data races.
- Isolated source-copy control inserts real e17Recover after writer assertions but before the new post-writer certificate: expected compiled FAIL (9.508s), with the same claim/P/time now phase=terminal. The actual /build source was not changed by this control.
- Direct MoveFile early cross-repo 501 plus mapper controls: three -race repetitions PASS (1.287s); integration vet PASS.
- Initial standard Compose run FAIL (494.952s): detected missing SESAMEFS_TEST_IN_CONTAINER declaration in go-integration-test and an existing share-link scanner test's undeclared daemon prerequisite. After the marker fix, second standard run passed E1-09 but still FAILed that scanner test (497.761s). These are retained as failures, not counted as final evidence.
- The scanner test now declares requireGCEnabled before creating a fixture, like the existing daemon-dependent tests. Its complete unchanged projection assertions PASS separately with the real development daemon enabled (19.071s; test itself 10.34s). The primary was then recreated with background GC disabled; all three actual container configs report GC_ENABLED=false. Production was never enabled or changed.
- Final exact go-integration-test command and complete resolved Compose environment, executed in the persistent Docker validation runner: PASS (555.283s), including 14/14 required E1-09 legs and all other mandatory package evidence gates. The marker declaration is included. The isolated run reports 14 explicit disabled-daemon skips, including the declared share-link prerequisite; its enabled branch was independently verified above. Optional multi-DC tests remain unconfigured/not certified. API/OIDC suites were not rerun for this test-only correction.
- Final integration vet, gofmt and whitespace checks PASS. No production API, DB, GC or production configuration changes in this correction. Final scoped audit found no unresolved introduced P0/P1/P2; the two registered GENERAL follow-ups remain OPEN.

Logs outside Git: $TEMP/sesamefs-e19-isolation-{enabled-red,race-five,unit-vet,premature-red,standard-integration,standard-final,standard-complete,vet-final}.log and $TEMP/sesamefs-e19-share-daemon-{fleet,green}.log. The first two standard logs contain their respective failures; standard-complete is the final PASS.

Validated Go source hashes (host and final Docker runner):

| Source | SHA-256 |
|---|---|

| internal/integration/e19_cross_repo_test.go | c69020470040b0d71a3c115ea3550ec175a6521fb960fedb614ba9cb9ca7b5d5 |
| internal/integration/share_projection_regression_test.go | 979bf8a5707ddf377e9221bd0d2a53e2434220a2fe17ee1f13e994bede273f7f |
| internal/api/v2/files_batch_test.go | 1eec81af656551309620f7e260a5d9a8ac855d83c6ffaca7c99d8dc9eef6dbac |

## Scoped E1-09 isolation and normal GC restoration (2026-10-05)

The general Compose primary again inherits GC_ENABLED from the selected env
file, exactly as before #258. Nodes 2/3 retain their existing GC=false setting.
The share-link scanner test's added requireGCEnabled skip is removed. Production
GC remains OFF; no production config/runtime/schema change is included here.

Only the test profile adds sesamefs-e19, with GC=false and CASSANDRA_KEYSPACE=
sesamefs_e19, no host port, and the ordinary real Cassandra/SILO services. Its
bootstrap creates/grants this separate keyspace using the existing app role,
after the ordinary bootstrap; it does not rotate shared passwords. The backend
applies the same normal migrations. This is the measured single-DC harness,
not a new replication/protocol claim. No background worker uses this keyspace.

Both go-integration-test and go-all-test depend on this backend and supply
SESAMEFS_E19_ISOLATED_URL. TestE19CrossRepoPublication launches the same test
binary (preserving -race instrumentation) with a 3-minute test timeout, the
isolated keyspace/URLs, and only the E1-09 required evidence gate. All other
mandatory gates still run in the parent on the normal stack. Child TestMain
must pass all fourteen named legs; nonzero exit/unavailable endpoint/unknown or
enabled GC fail the parent. Only successful complete child evidence is recorded
by the parent. Direct controlled E1-09 execution retains its existing guard.
The endpoint check and strict exact COMMITTED certificates before/after the
writer remain; TERMINAL is still a separate leg. This isolation is scoped to
this controlled service/keyspace, not unknown external workers/admin jobs.

Final validation pending: repeated required E1-09 under -race while the general
daemon is active, and the actual full go-all-test service with daemon-dependent
coverage restored. Historical blanket-GC-OFF validation above is not substituted.

The first actual go-all-test after restoring GC exposed a pre-existing MaxRetry
harness race: it failed at the unrelated LastWorkerRun barrier (47.36s), before
checking its own DLQ row. Cassandra later confirmed that exact synthetic fixture
in gc_failed_items with retry_count=5 at 21:22:54 UTC, about 64s after creation.
The trigger is asynchronous; the global timestamp is not fixture completion.
The focused correction removes only that timestamp barrier and spends the same
combined 45s+45s budget polling the exact DLQ row, live-queue absence and reconciled
snapshot. All original field/counter assertions remain. Cleanup is registered
before insertion so a failed wait no longer leaks the synthetic fixture. No daemon
skip, runtime timeout or GC implementation change is added. This first failed
run is retained; it does not count as final go-all-test evidence.

The second actual go-all-test retained normal GC coverage: E1-09 14/14 and all
14 daemon-dependent tests PASS, zero disabled-GC skips. It nevertheless failed
integration (886.716s) at pre-existing W2NativeHEADAmbiguity/SyncDirect/
requestLostUnconfirmed: a peer postponed the discovery row after the manual
worker's cutoff, so its scoped SELECT found zero. Candidate/P were not retired.
MaxRetry PASSed in that complete run (3.49s); API/OIDC were not reached.

Correct only w2AssertGCBlocked's discovery observation: enqueue once, then allow
at most three real dequeues with fresh cutoffs when zero rows were observed
before its own liveness probe. Every attempt still checks the exact candidate,
canonical P/key and bytes; success still requires its own productive read and
n=0/error=nil. A peer's protection is not substituted as evidence. Actual D,
candidate loss, missing bytes, different errors or absent own probe still fail.
No daemon toggle, fabricated rows, authority answers or runtime change is used.
Five focused native-leg -race repeats PASS (35.781s) before final cutoff-only
adjustment. A scratch-source control inserting a real peer before cutoff did
not exercise the race and is not counted as branch evidence. Moving the real
peer between cutoff capture and SELECT reproduced the window, and also exposed
that a first re-enqueue approach could produce two rows (expected FAIL,
10.110s). The final read-only retry control then PASSed (10.164s), logged the
zero-row first attempt and obtained its own actual blocked probe on retry.
All controls used real Cassandra/SILO/production workers in a /tmp source copy;
no control injection is included in the final source.

Final audited validation (actual Compose service, not a filtered substitute):

- docker compose --profile test run --rm go-all-test: PASS, exit 0.
- Go short/coverage PASS; mandatory full integration PASS (845.139s), including
  E1-09 14/14 and every other configured package evidence gate.
- All 14 standard daemon-dependent tests PASS, zero disabled-GC skip messages.
  MaxRetry PASS (5.88s), share projection scanner PASS (4.77s); the whole native
  HEAD ambiguity matrix PASS (104.73s). Its original own-read/P/candidate/bytes
  assertions remain. API 20/20 suites PASS; OIDC 25/25 checks PASS, zero OIDC skips.
- Authenticated status confirms normal primary GC=true, E1-09 backend GC=false.
  Standard nodes 2/3 and production configurations are unchanged. Optional
  multi-DC/cgroup scenarios remain unconfigured; gcsoak is a separate unselected
  tag and is not certified by the standard integration invocation.
- Final integration vet, gofmt and whitespace checks PASS. Scoped final audit:
  no unresolved introduced P0/P1/P2. Registered GENERAL P1 source identity and P2
  FileFromBlocks fan-outs remain OPEN; W2-9/E1/X1/R31/prod-GC scope unchanged.

Logs outside Git: $TEMP/sesamefs-e19-scoped-{race,enabled-red,go-all-final,
go-all-complete,go-all-audited}.log; $TEMP/sesamefs-e19-maxretry-{observed,final}.log;
$TEMP/sesamefs-e19-native-{discovery-final,peer-control,peer-window-control,
peer-window-final}.log. go-all-final and go-all-complete are the retained full
failures, not PASS evidence; go-all-audited is the actual final complete PASS.
peer-control did not hit the intended window; peer-window-control records the
rejected duplicate-enqueue approach. peer-window-final validates the precise
window with the final read-only retry. Scratch controls are outside Git/source.

Final Go source SHA-256 (host/current Docker test image):

| Source | SHA-256 |
|---|---|
| internal/integration/e19_cross_repo_test.go | 977677c72963680d015df6258e11ce35398d7d04058161cda70b7a5654fec274 |
| internal/integration/gc_integration_test.go | ed7b8bf4610d0281c29328b6e130ea4100fddadcf3eeb68719e47a0c169eb49d |
| internal/integration/share_projection_regression_test.go | 746994e959a1f5a41d07f7a04ecad89df9d1927097c51f37ec07db34f7c38f0c |
| internal/integration/w2_wire_crash_closure_test.go | eeb31e4f75e22a567f01851921baa5856d5084323614c47435420f09bc4f0651 |

## Scoped current-main three-DC plan (2026-10-15)

Base main@8c7bd3d48 (merged #300). Freeze before execution. Scope: existing
TestE19CrossRepoPublication, copy/move across seven phases (14 named legs).

1. Execute unchanged source on the private Cassandra 5.0.9/SILO fixture with
   actual NTS RF1 per dc-na/dc-eu/dc-asia, migrated sesamefs_e19 and GC-OFF API.
   Require the existing completeness gate and exact named outcomes. Do not
   assume #300 resolves every historical failure.
2. Repeat race and EU coordinator; verify actual parent/child isolation plus
   required filtered/unavailable/enabled-GC rejection and existing meaningful
   source/publication omission controls. Mutations stay in disposable containers;
   restore and compare before final original-source execution.
3. Verify owned block/ref/mapping/repair/expiry/lifecycle/root/discovery/object
   cleanup independently and classify retained auxiliary probes. Existing
   E1-10e/E1-17 COMMITTED teardown debt stays separate; do not erase older data.
4. If no defect reproduces, change only this evidence ledger and concise tracking.
   Otherwise preserve RED before a minimal correction and scope assessment.
   Audit links, source/image identity, exact exits, complete diff and current
   main; stop private services retaining volumes, then open a focused PR.

No full-suite/client/auth/all-order/partition/TTL or gate-closure claim. Previous
one-DC compatibility is historical evidence, not a substitute for this matrix.
P2 umbrella, E1-01/X1/PRE-GC/A1 stay OPEN; ordinary/production GC OFF.

## Three-DC validation on current main (2026-10-15)

### Scope and disposition

Frozen plan `1e80a0630`, base main@8c7bd3d4878ec33a472684f936675a91b8c5fb18
(merged #300). No current-code failure reproduces in the unchanged 14-leg
matrix: copy/move across normal, committed, terminal, captured-committed,
captured-terminal, repair-first and head-conflict all PASS. e17CommitGC already
inherits #300's validated fixture declaration on its observed native session.
No new Go/runtime/harness fix is justified or included; this slice changes docs.
This is current scoped positive evidence, not a causal replay of every historical
three-DC failure. Earlier results and rollout notes retain their dated context;
current gate decisions follow X1 and ordinary/production GC remains OFF.

### Frozen source and environment

Image sesamefs-e19-3dc-current, built at the plan-only revision:
`sha256:f7df7a2cc9a4a94a23744561a50b07117407dc06cde773e48f61260ad31ab246`.
Host/image/restored runner agree on LF-normalized source SHA256:

| Source | SHA256 |
|---|---|
| e19_cross_repo_test.go | 977677c72963680d015df6258e11ce35398d7d04058161cda70b7a5654fec274 |
| e17_sync_recvfs_test.go | a064846b55a622e01a5119a3afe5e72bbce40bac546918adcfef3c5f989dd5f8 |
| w2_native_wire_test.go | d22a369ca0b7150463589480823ca84805b7c535345edaa8fe4fcf58e556a842 |
| batch_operations.go | 14006009ee2c55f810628a232a0d1e086ebd2165bb0ad6f5723a9ac3578418d4 |
| cross_repo_publication.go | ca1456a57a5f9dad0942d6041bd1bd88077e9f7c1f38fcf3ec553f3a6591cadd |

The private sesamefs-e111-3dc stack reuses owned volumes, migrated sesamefs_e19,
Cassandra 5.0.9 and actual NTS RF1 in dc-na/dc-eu/dc-asia, checked before/after;
all three nodes are UN/healthy. SILO RELEASE.2026-09-16T00-00-00Z image
`sha256:635197cb9f36d01bee221d34d1c7d7960f6a95c48b0b6c01d99cd13bdae51a46`.
API/seed uses dc-na; native/verification sessions use dc-na, then dc-eu in the
separate EU run. This is healthy topology, not an unavailable/divergent replica
schedule. Backend built from unchanged current source, binary SHA256
`f95399fbbcf07e5f853c04067839247222280b1bf02511e0142bfb95177cc6ab`.
Authenticated admin status reports enabled=false, worker never run. Every
supplied API URL points to this controlled backend. Real-service runs are
sequential; no ordinary/shared deployment is enabled or redeployed.

### Results and controls

Ignored logs and explicit exits under tmp/:

| Log | Measured result |
|---|---|
| e19-3dc-current.log | Unchanged 14/14 legs, completeness, isolation-status controls, evidence wiring and no-global-fanout guards PASS; exit 0. |
| e19-3dc-isolated-race.log | Actual parent/child path, -race -count=2: both fresh children prove all 14 names (28 total), parents PASS, exit 0. Existing child 3m timeout unchanged. |
| e19-3dc-eu.log | Full 14-leg matrix plus existing E19 contracts through dc-eu PASS, exit 0. |
| e19-3dc-negative-filtered.log | Direct copy/normal PASS; required gate reports the other 13 names, expected exit 1. |
| e19-3dc-negative-unavailable.log | Required parent bootstrap rejects unreachable API before tests, expected exit 1. |
| e19-3dc-negative-child_unavailable.log | Healthy parent rejects failed isolated child bootstrap; no child evidence credited, expected exit 1. |
| e19-3dc-negative-omission.log | Omit only final validateCopiedBlockPublication call in container: all four copy/move captured COMMITTED/TERMINAL cases fail their real reachable-destination oracle, expected exit 1; four cleanups verified. |
| e19-3dc-negatives.log | All expected exits/markers, four behavior failures/cleanups and restored-source comparisons PASS; wrapper exit 0. |
| e19-3dc-cleanup-audit.log | Restored runtime, 14/14 legs plus GC-OFF check PASS; 14 read-only post-teardown verifications. Wrapper exit 0. |
| e19-3dc-restored-race.log | After runtime/test restoration and byte comparisons: original-source -race -count=1, 14/14 plus E19 contracts PASS; exit 0. |

All positive directed matrices ran with the existing required E19 gate, with
exact named PASS counts, no SKIPs or race reports. The status unit control uses
existing httptest responses: enabled=true, unknown/malformed state and bad HTTP
are rejected. It does not start a GC-enabled real backend. TestMain cleanup still
logs its pre-existing partial-topology declaration; the destructive worker uses
the corrected full declaration. No general shared-helper fix is claimed.

The omission does not bypass source capture, staging, repair or HEAD. Copy
reports success with reachable retired-P content; move may report source failure
but still exposes the unsafe destination. Four failures specifically contain
`RED: destination reachable after exact P1 retirement`; compilation/setup or
completeness failures alone are not counted. Container-only instrumentation
records that original behavior failure before extra exact recovery disposes D1,
then fails the test. No mutation reaches host source or this PR.

Extra container-only audit callbacks run AFTER the existing teardown and add no
cleanup writes. They check exact owned block/ref/lifecycle/orphan/mapping rows,
source AND destination repair partitions, canonical expiry trackers/by-day rows,
recovery roots/discovery and physical K. All 14 positive fixtures and four
mutation fixtures verify absence. Old COMMITTED authority is recovered through
the productive exact path; soft-deleted library history is not claimed purged.
Audit instrumentation is removed, both modified container files byte-compared
with originals, and the final race executes original source/oracles.

Independent EACH_QUORUM aggregate roots/orphans/publication-repair counts are
zero before/after; orphan discovery is also zero after. The bucket retains 41
auxiliary storage-class probes (33 prior + 8 new); every remaining object payload
matches gc-test-discover.txt-*. No E19 fixture K remains and no empty-bucket
claim is made. The separate E1-10e/E1-17 COMMITTED teardown follow-up remains
OPEN; this run does not touch its older one-DC records. The private three-DC
stack is stopped after verification, volumes retained.

### Reproduction and audit limits

In the built Docker image, with isolated GC-OFF API/SILO, migrated keyspace,
complete explicit CASSANDRA_HOSTS/LOCAL_DC/REPLICATION_DCS and SESAMEFS_URL:

```bash
for v in ${!SESAMEFS_REQUIRE_@}; do unset "$v"; done
export SESAMEFS_REQUIRE_E19_CROSS_REPO_EVIDENCE=1
go test -tags integration -v -count=1 -timeout 8m -run '^TestE19' ./internal/integration
SESAMEFS_E19_ISOLATED_URL="$SESAMEFS_URL" go test -tags integration -race -v -count=2 -timeout 10m -run '^TestE19CrossRepoPublication$' ./internal/integration
```

Repeat the direct matrix with dc-eu host/local DC; keep the complete declared
map. Existing per-leg barriers, task waits and isolated child timeout remain
unchanged. Productive source HTTP purge, real cascade/zero-proof/retirement,
final exact-P and actual destination HTTP download remain the named oracles.
Source up: lapse is an explicit expiry-state control; repair-first observes
its shortened real pub: TTL, not production TTL-policy/scanner coverage.

Not rerun: full standard integration/API/OIDC, short Go/vet, one-DC compatibility,
standalone Sync/frontend or other three-DC matrices. Their dated results above
and #300's one-DC 14/14 are historical evidence, not new executions here.
This slice changes documentation only; Go source, schema, defaults,
dependencies and runners match the frozen base.
No external batch auth/routing, client, directory/multi-block/encrypted case,
all-order, partition or global-progress claim. E1-10e and full-suite obligations
remain pending; umbrella P2, E1-01 (including PREPARED before discovery), X1 and
PRE-GC/A1 stay OPEN. Backward compatible: yes, documentation/evidence only.

Final scoped audit: no introduced blocker identified. All tracked changes are
five Markdown files; Go, scripts, Compose, configuration and dependencies match
the frozen base. Source/image hashes, original restored oracles, exact named
positive/negative exits, 266 relative file links, new evidence anchor and
whitespace checks pass. Main remains the tested base, with no rebase/rewrite
needed. Retained probes and soft-deleted library history do not imply an empty
bucket/database or global queue convergence; only the stated owned surfaces
are certified. No issue/gate is closed by this evidence slice.
