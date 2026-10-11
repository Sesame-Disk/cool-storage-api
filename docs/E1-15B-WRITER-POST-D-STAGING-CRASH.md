# E1-15B — writer publication after D, crash before the final fence

Frozen base: main@4290a2bb394b8748973b2a4b7e9b8340d122a2af (#271 merged).

> **Scope update (2026-10-10, [E1-16](./E1-16-POST-D-CONTRACT-ALIGNMENT.md)):**
> the X1 invariant is aligned with G4 contract 5. The post-D `pub:` and
> repair are logical references of L. E1-15C measured no HEAD advanced and no
> P1 reinstall, so the class moves from P1 PRE-X1 to P1 PRE-GC. Evidence and
> severity below are unchanged.

## Question

Can a current Office/CreateFile writer, resumed after exact COMMITTED D(P1),
durably write its original `pub:` (and its durable repair) and then lose the
process before `validateCommitBlockPublicationFences`, which would have
rejected the publication? What does the durable state look like afterwards,
and does the shared repair worker keep that post-D liveness alive?

## Static analysis (main@4290a2bb3)

CreateFile order: materialize P1 + `up:` → `createFileAfterMaterializedBarrier`
→ `prepareFileFSObjectForPublish` → `stagePendingPublishedFiles` (resolve
SHA-1→SHA-256 through `block_id_mappings`, persist the pending fs owner,
`AddPublishAttemptReferences` → `pub:<commit>`) → `fileFromBlocksAfterStagedBarrier`
→ `queuePendingPublishedFileRepairs` (durable, non-expiring R) → `insertCommit`
→ `fileFromBlocksBeforeHeadBarrier` → `validateCommitBlockPublicationFences` →
HEAD CAS. Nothing between materialization and staging checks claim or D.
Mappings survive physical retirement (R11a), so staging should succeed after D.

Expected by code reading, not yet measured:
- crash after staging: a durable `pub:<commit>` after D, TTL 35d, no repair;
- crash after queueing: `pub:` plus a durable R for an attempt that will
  never reach HEAD. The sweep should classify it UNKNOWN and renew
  `pub:<repo:commit:fs>` on every visit, and the #271 post-check would see R
  present and keep the pin. That is post-D liveness renewed indefinitely
  (intersection with `ISSUE-PUBLISH-REPAIR-DEAD-ROW-RETENTION-01`).

## Classification (frozen before runtime, same rule as E1-15A)

RED: any durable `block_references` row for the block written by a current
operation after exact COMMITTED D(P1) and present at the inspection point.
Harm is recorded separately: a TTL-bounded dead pin, or an indefinitely
renewed pin; none of these legs advances HEAD. This PR characterizes and does
not fix. A confirmed RED is registered as one P1 PRE-X1 class ("liveness
written post-D before validation"), linked to the #271 residual and to
DEAD-ROW-RETENTION. The shared fix belongs to a separate design PR.

## Plan

1. Office/CreateFile, real Cassandra/SILO, existing e19 GC-OFF isolation; each
   leg owns an org/library. No production change. No CQL insert/delete of
   references, no harness candidate or repair, no fabricated HEAD/P/D.
2. Legs:
   - `no-gc-control`: in-process writer publishes; fs: and settled repair.
   - `d-before-stage-no-crash`: writer subprocess paused after
     materialization; natural D; resume; the final fence rejects; the
     request fails; pub:/repair cleaned; HEAD unchanged.
   - `d-before-stage-crash-before-queue`: same, then pause at afterStaged and
     SIGKILL (OS signal verified); inspect at EACH_QUORUM from the parent.
   - `d-before-stage-crash-after-queue`: same, pause at beforeHead (after
     queue and insertCommit) and SIGKILL. Inspect pub:, R, commit and HEAD.
     Then run the productive sweep twice (eligibility +2h, then +6h past the
     retry hint; real lease kept ahead of the shared daemon), recording the
     native classification and every reference write.
3. Natural D: the writer's real `up:` is moved by the productive renewal API
   to a seconds-scale deadline and retired by Cassandra TTL; owned-scope Phase
   0 creates the candidate at exact P1; owned-scope Phase 1 and the productive
   worker reach exact COMMITTED (lifecycle PUBLISHED, orphan COMMITTED, root,
   canonical retired). Reuses the E1-15A helper.
4. Teardown: owned root completed to TERMINAL; E1-11/12/13/14/15A verifiers.
   Completeness gate, gate negatives, race repeats, vets, standard
   go-all-test, cleanup/quota checks, scoped audit, PR.

## Out of scope

Repair worker changes, fences, tables/migrations, TTL, Phase 0–6 changes,
Sync/SeafHTTP/OnlyOffice/cross-repo, read-repair/3-DC, scheduler, GC
activation, and any fix of a confirmed RED.

## Validation

Accepted results are recorded below. Nothing is inferred from this plan.

## Plan deviation: one new integration-only seam

The plan assumed the existing `fileFromBlocksBeforeHeadBarrier` covers the
"after queueing" crash. It does not: that seam exists only in UploadFile.
CreateFile had no observation point between `insertCommit` and the final
fence. In the first run the writer reached the fence, got 409, and the leg
correctly failed for lack of a boundary. `createFileBeforeFinalFenceBarrier`
was added right before `validateCommitBlockPublicationFences` (integration
build only; normal builds compile a no-op, same pattern as E1-13/E1-15A). No
production behaviour changes.

## Harness (test code 60e435321)

`TestWriterPostDStagingCrash` runs in the existing e19 isolated child.

- Writer: a real CreateFile in a subprocess (`TestE115BProcessChild`, E1-12
  process pattern). It holds at `createFileAfterMaterializedBarrier` with P1/K1
  and its real `up:` only. The parent requires exactly one `up:`, no repair and
  HEAD unchanged at that point.
- Natural D (E1-15A helper): the real `up:` is moved by the productive
  renewal API and retired by Cassandra TTL; owned-scope Phase 0 creates the
  candidate at exact P1; owned-scope Phase 1 and the productive worker reach
  exact COMMITTED (lifecycle PUBLISHED, orphan COMMITTED, root, canonical
  retired). The harness never creates the candidate.
- The writer then resumes. Depending on the leg it completes, or holds at
  `fileFromBlocksAfterStagedBarrier` / `createFileBeforeFinalFenceBarrier`,
  where the parent SIGKILLs it and verifies the OS signal.
- Inspection is done by the parent at EACH_QUORUM: canonical retired,
  lifecycle still PUBLISHED, HEAD unchanged, no fs:, exact references,
  reference TTLs, repair rows and repair TTL, commit row.
- The after-queue leg ages the real repair (created_at −1h, real lease +1h)
  and runs the productive sweep twice, at +2h and +6h eligibility (past the
  row's retry hint), on an observed session. It records the native
  classification and every reference write.
- The owned COMMITTED root is completed to TERMINAL by a `defer` in every D
  leg. It is not a `t.Cleanup`, because `t.Context()` is already canceled
  there. Teardown: E1-11/12/13/14/15A verifiers.

## Results (accepted on 60e435321)

- `no-gc-control`: the writer publishes; fs: present; repair settled.
- `d-before-stage-no-crash`: resumed after D, it stages, then the final fence
  rejects it with 409; pub:/repair cleaned; HEAD unchanged. The staging
  INSERT itself still ran post-D (transient).
- `d-before-stage-crash-before-queue` — **RED (characterized)**: SIGKILL after
  staging leaves a durable `pub:<commit>` (TTL 3024000s) written after exact
  COMMITTED D(P1); no repair; HEAD unchanged. Harm: a TTL-bounded dead pin.
- `d-before-stage-crash-after-queue` — **RED (characterized)**: SIGKILL after
  queueing and insertCommit, before the final fence, leaves a durable
  `pub:<commit>` plus a non-expiring repair, both written after D. Each of the
  two productive sweeps classifies native UNKNOWN and renews
  `pub:<repo:commit:fs>` (TTL reset to 3024000s) after D, and the repair is
  retained. The #271 post-check keeps the pin because the row is present.
  Harm: post-D liveness renewed on every visit, with no bound, by a repair
  for an attempt that can never reach HEAD
  (`ISSUE-PUBLISH-REPAIR-DEAD-ROW-RETENTION-01`).

Accepted checks:
- Container SHA-256: test aaa1c8243683df25f2588a20d577c72ccc0e7821ea4b55f634cf545c009fe2bb,
  files.go dde0a3b53cc06e9ba034716555024d44fbe8833e1cc188a298be7643fad5852b.
- Matrix 4/4 PASS, 20 teardown verifications.
- Race `-race -count=3`: 12/12 legs, 12 RED characterizations reproduced,
  60 teardown verifications, 0 data races; 129.653s package, 209s wall.
- Ordinary/integration vets and `go test ./internal/api/v2/` PASS; three gate
  negatives PASS (filtered, unavailable, filtered child).

Development runs not accepted as evidence:
1. The missing CreateFile seam (above). That leg failed before its
   continuation, and teardown then removed the lifecycle row.
2. A `t.Cleanup` continuation failed with "context canceled" in three legs.

In both cases the COMMITTED root, orphan and K1 leaked in e19. Productive
recovery correctly refused to continue without the lifecycle row. The four
leaked identities (orgs bab06c8b, 9b7172bc, 2d6c3cca, 5dd23fa0) were removed
by a one-off, uncommitted test at their exact logged coordinates. It required
canonical absent, one orphan whose class/key match the fixture pattern, and
the exact root; it used `DeleteS3OrphanRecoveryRoot`/`DeleteS3Orphan`, then
deleted the exact K1 and verified root/orphan/K1 absent.

## Review corrections (two P2 evidence-contract findings, both confirmed)

1. The no-crash control accepted any non-201 response. A writer failing
   before `validateCommitBlockPublicationFences` (a 400/404/500 after cleanup)
   would leave HEAD and references clean and pass without proving the fence
   rejected it. Now the subprocess records, without holding, that it reached
   `createFileBeforeFinalFenceBarrier`, and the parent requires that marker
   **and** HTTP 409. The HEAD/pub:/repair checks are kept.
2. The root finalizer was registered after `e115aCommitD`. A failure inside
   that function after exact COMMITTED (orphan/root/lifecycle verification)
   would have left the root uncompleted. `e115bFinalizeCommitted` is now
   deferred **before** GC runs, and acts only when durable exact COMMITTED
   authority for this P1 exists: lifecycle PUBLISHED at P1 and its orphan
   COMMITTED. It never creates D or deletes without that authority. Focused
   negative control `scripts/e115b-finalizer-negative.sh` (disposable
   container): a `t.Fatalf` is injected right after the COMMITTED check inside
   `e115aCommitD`. The leg fails with that marker, the finalizer completes the
   root, and all E1-11/12/13/14/15A teardown verifiers pass (PASS). The
   script unsets every unrelated mandatory gate.

No production change. Re-run on the corrected source (test SHA-256
60408675f2a0a8b89ce5d3ead4a187d3d79ada214fa2c694b00dfc5ce5751d16):
race `-count=3` 12/12, 12 RED characterizations, 9 finalizer completions
(three D legs × 3), 60 teardown verifications, 0 data races (143.325s package,
219s wall); vets and three gate negatives PASS. The E1-15A test shares the
older end-of-leg continuation (merged in #271), a test-robustness follow-up
not changed here.

Standard regression on the corrected source:
- With the 22m budget: exit 1, **timeout only** (1320.804s). There was no
  `--- FAIL`; the timeout hit while `TestWebBlockUploadRejectsUncommittableBlocks`
  was starting. Host load slowed the suite (each E1 matrix 10–20s slower than
  the 1196s accepted run), and 22m left about 2 minutes of margin. Not
  accepted.
- Budget raised again, 22m → **30m**, in both Compose runners (about 10
  minutes of margin). Time budget only; no gate or assertion relaxed.
- **Accepted** go-all-test with 30m: exit 0, 2026-10-08 13:09–13:35 local.
  Integration 1223.464s (E1-15B child PASS with the 409 + fence-marker control
  and 4 RED characterizations); API 20/20; OIDC 25/25; the same 75 SKIPs as
  before.
- Afterwards both backends report `CLEANUP_STATUS: clean`, with quota_usage 0,
  storage_quota 2000000000 and policy hard unchanged. The earlier 22m-budget
  acceptance (1196.541s) is historical; this run is the accepted evidence for
  the final source.

## Disposition

- Confirmed **P1 PRE-X1 class**: a current writer's liveness can be written
  after exact COMMITTED D(P1), before the final validation that would reject
  it. A crash in that window leaves it durable. The pre-HEAD guarantee holds:
  no leg advanced HEAD, and the no-crash leg is rejected. The strict post-D
  no-reference guarantee does not hold. Registered as
  `ISSUE-PUBLICATION-POST-D-LIVENESS-BEFORE-VALIDATION-01`, linked to the
  #271 residual (`ISSUE-PUBLISH-REPAIR-STALE-RENEWAL-AFTER-CLEAR-01`) and to
  `ISSUE-PUBLISH-REPAIR-DEAD-ROW-RETENTION-01`.
- The after-queue result means a write fence alone would not suffice for a
  shared fix. A durable repair for an attempt that never reached HEAD keeps
  renewing post-D liveness, so the shared design must also address that
  repair's settlement.
- Not fixed here, by plan. W2-6a/CreateFile pre-HEAD rows are not
  reclassified; only the strict post-D reading is recorded as open. E1-15
  overall, other funnels (unmeasured, same pattern expected), E1/X1 and GC
  activation remain OPEN.

## Standard regression and final audit

- First standard go-all-test on this source: exit 1, **timeout only**. The
  integration package hit its 18m budget (1080.591s) while
  `TestW2ProcessKillContinuity/BorrowedFS/afterAuthority` was 3s into a
  normal run. There was no `--- FAIL`, and the E1-15A/E1-15B children had
  passed. The #271 run already needed 1009.387s, and the two new mandatory
  child matrices exceed 18m. Not accepted as evidence.
- Budget adjustment: the integration `-timeout` in both Compose runners
  (`go-integration-test`, `go-all-test`) goes from 18m to 22m. No gate,
  assertion, GC configuration or test was relaxed or removed; E1-11 set the
  earlier 18m budget the same way.
- **Accepted** go-all-test with the 22m budget: exit 0, 2026-10-08
  11:01–11:26 local. `go test ./... -short` PASS; integration 1196.541s
  (E1-15B isolated child PASS, 4 RED characterizations); API 20/20 suites;
  OIDC 25/25. There are 75 SKIPs: the 74 existing optional 3-DC/topology ones
  plus the parent-only `TestE115BProcessChild` helper. Their execution is not
  claimed.
- Afterwards both backends report `CLEANUP_STATUS: clean`, with quota_usage 0,
  storage_quota 2000000000 and policy hard unchanged.
- Scoped audit. Production-file diff: one integration-only seam call in
  CreateFile (no-op in normal builds); no schema, migration, GC, scanner, TTL,
  repair or config change. Plan items executed except merge/activation.
  Prohibitions held: no CQL insert/delete of references, no harness
  candidate or repair, no fabricated HEAD/P/D. Deviations: the missing
  CreateFile seam, the continuation moved to `defer`, the integration budget,
  and the exact-identity removal of four leaked development roots (all above).
  No unresolved introduced P0/P1/P2. The characterized P1 PRE-X1 is
  pre-existing runtime behaviour, registered and not fixed by design.
