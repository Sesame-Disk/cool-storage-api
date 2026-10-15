# Current Work

## Status snapshot (2026-10-12)

The source of record for order and closure states is
[docs/X1-CRITICAL-PATH.md](docs/X1-CRITICAL-PATH.md). This is a summary.

- **GC:** `GC_ENABLED=false` is mandatory fleet-wide.
- **Path:** W1 → G1–G5 (G1–G3 #207/#209/#212, G4 #247, G5 #248) → **E1 (in
  progress)** → X1 CLOSED → PRE-GC → A1 → GC ON. X1 is OPEN.
- **X1 contract:** a physical invariant (E1-16, #277). After COMMITTED D(P1),
  nothing may reinstall P1, revoke or postpone D1, or publish HEAD content
  that depends on retired P1. Logical references of L after D1 are retention
  (PRE-GC), not X1.
- **W2 checklist:** every row, W2-0..14, is closed for X1. W2-10 (content
  resurrection) was closed by E1-17 (CLOSED-FIX).
- **Remaining before X1 CLOSED:**
  1. E1-01, after inventorying the G1–G5 crash evidence (next);
  2. the three-DC residuals (28 of the 30 G5 failures now pass);
  3. the D0 §25 reclassification list;
  4. the identity claim-table PRE-X1 audit debt.
- **PRE-GC / A1 gate:** every open `PRE-GC`/`PRE-ACTIVATION` tag plus the X1 §6
  table. Highlights:
  - Phase 5 cascade (P0 latent) and the Phase 6 TOCTOU;
  - the stale-claim settle race and the clock assumption;
  - the certification-window fence (to be re-scoped);
  - the HEAD-less ghost row;
  - hard-delete `NONFENCING-01`: P1, also current-runtime; #243 was closed
    without merge;
  - the E1-16 convergence moves (E1-15A/B, W2-12..14 residuals);
  - **abandoned repair retention:** P2, measure first, since 2026-10-12.
- **Open follow-ups from the E1-16 runs:**
  - `ISSUE-SEAFHTTP-UPLOAD-FINALIZE-LEASE-ERROR-500-01` (P2, multi-DC
    availability);
  - TECHNICAL-DEBT: the integration suite is at its 40m budget, and the E1
    legs do not run on three DCs.

## Active slice: harness topology declaration (test-only)

Branch `test/harness-fixture-topology`, base main@a71eec36a (#292). `openIntegrationProjectionDB`
now loads the server config — topology included — the way `shareProjectionDBForTest`
has since `fa73da717`. Both shared projection DB helpers now carry
`CASSANDRA_REPLICATION_CLASS`/`CASSANDRA_REPLICATION_DCS` into the destructive
GC gate's declared-vs-live comparison; dedicated test connections are outside
this change. Verified on the isolated
single-DC stack: the gate accepts the declared dev map and refuses a synthetic
three-DC declaration naming `dc-na`/`dc-eu`/`dc-asia`; H1 initial-head tests pass.
`docs/TECHNICAL-DEBT.md`'s three-DC entry is corrected, not closed: its attribution
to this helper for seven E1 legs is unsupported — those legs use the topology-aware
sibling — so that symptom's cause remains open and the legs were not re-run on three
DC. See [docs/CHANGELOG.md](docs/CHANGELOG.md) (2026-10-15).

## Previous merged slice: E1-01G PUBLISHED/PREPARED process crash (#292)

Branch `codex/e101-published-prepared-crash`, base main@6f8fc2630 (#291).
[E1-01G](docs/E1-01G-PUBLISHED-PREPARED-CRASH.md) is bounded to C3: successful
exact PUBLISHED lifecycle INSERT before PREPARED-to-COMMITTED orphan CAS, with
committed handoff and original canonical/K1 still intact. Productive org-scoped
worker, existing observer, independent durable oracle, SIGKILL/resume, orphan
recovery before separate queue settlement/replay, preservation and required gate.
Directed/race, negative gate/recovery/no-kill controls, preservation, short Go/vet
and full required integration/API/OIDC PASS; container and outer exits 0. All A–G
and seven preservation controls remain green; scoped audit found no blockers.
Initial preservation HTTP 404 corrected by direct E19 execution, as in E/F;
ledger records the failure, frozen sources and single-DC scope. No runtime change;
E1-01/X1 OPEN, ordinary/production GC OFF.

## Previous merged slice: E1-01F discovery-retained TERMINAL process crash (#291)

Branch `codex/e101-discovery-retained-crash`, base main@2b282a07a (#290).
[E1-01F](docs/E1-01F-DISCOVERY-RETAINED-CRASH.md) is limited to successful exact
orphan DELETE before discovery DELETE: TERMINAL, orphan absent, discovery/root
present. Reuse productive preparation, observer, independent durable oracle,
SIGKILL/resume, fresh-process recovery/replay, preservation and a separate gate.
Directed Docker, race repetitions, gate/recovery-child negatives and preservation
PASS. Short Go/vet and full required integration/API/OIDC PASS; container and outer
runner exits are 0. A/B/C/D/E controls remain green; scoped audit found no blockers.
Frozen source/image and one-DC scope in ledger. No runtime change; E1-01/X1 OPEN.

## Previous merged slice: E1-01E root-only TERMINAL process-crash evidence (#290)

Branch `codex/e101-root-only-terminal-crash`, base main@1b16f092f (#289).
[E1-01E](docs/E1-01E-ROOT-ONLY-TERMINAL-CRASH.md) implements one internal C8 cut:
after real orphan/discovery deletion, before recovery-root deletion. The existing
query observer holds the productive call; the parent proves exact root-only
TERMINAL independently. SIGKILL/resume, independent recovery/replay and failed
fixture preservation are required, with a two-leg fail-closed runner gate.
Directed Docker, race repetitions, gate/recovery-child negatives, short Go/vet
and full required integration/API/OIDC PASS; both full-runner exits are 0.
A/B/C/D controls remain green. See ledger for frozen sources and single-DC scope.
No runtime change; E1-01/X1 OPEN, production GC OFF.

## Previous merged slice: E1-01 remaining-cut inventory (#289)

Branch `codex/e101-remaining-cut-inventory`, base main@bd8cab852 (#286 merged).
[E1-01 section 0](docs/E1-01-CRASH-EVIDENCE-PLAN.md#0-current-reconciliation-and-next-bounded-execution-2026-10-14)
reconciles merged A/B/C/D K evidence with actual C0/C3 and internal C8 call order.
C0's unsettled root is safe retention; C3's PUBLISHED/PREPARED intermediate state
is not proved by C2 or C4. Neither is closed. The next execution is bounded to C8
after exact orphan/discovery removal, before root removal, with TERMINAL retained.
This PR supplies the inventory and plan, no new process-death result or runtime fix.
E1-01/X1 remain OPEN; the E1-16 test-gate P2 remains a separate follow-up.

## Previous merged slice: E1-17 pre-HEAD recheck witness (#286)

PR #286, branch `test/e117-prehead-recheck`, rebased onto main@25b74624ed5f.
Adds
`TestE117PreHEADRecheckRejectsLostAuthority`
(`internal/integration/e117b_prehead_recheck_test.go`, gate
`SESAMEFS_REQUIRE_E117_REHEAD_RECHECK_EVIDENCE`, wired into `TestMain` and both
compose runners). The 15 residual legs assert `headVisits == 0`, so
they do not exercise the recheck. E1-16 already covers successful rechecks; this
leg adds rejection on lost permanent authority. It runs the
supported restore path on settled history, removes the permanent `fs:` the
recheck reads at the existing pre-HEAD barrier, and requires rejection with HEAD
unchanged. Independent audit corrected a reproduced stale-evidence/SKIP false PASS
in the new gate; evidence now resets per invocation, is recorded after cleanups,
and is checked even after an early parent SKIP.
Initial audit: Docker short Go, vet, race, full required integration, API (20/20 suites)
and OIDC (25/25 tests) PASS, with both full-runner exits 0. After the crossed-audit
gate correction, focused race repetitions/integration vet PASS; both orders of
early SKIP, rejection-leg SKIP and cleanup SKIP reject. The full runner was not
repeated for this harness-only correction. Scope is the shared permanent-reference recheck
through single-file RevertFile on one DC; X1/E1 remain OPEN. See
[docs/E1-17-W2-10-RESIDUAL-HANDLERS.md](docs/E1-17-W2-10-RESIDUAL-HANDLERS.md)
§Pre-HEAD recheck witness and [docs/CHANGELOG.md](docs/CHANGELOG.md) (2026-10-14).
The pre-existing E1-16 gate defect is tracked separately as
[ISSUE-E16-REPEATED-EVIDENCE-GATE-01](docs/KNOWN_ISSUES.md#issue-e16-repeated-evidence-gate-01).

## Previous merged slice: E1-01D terminal-before-cleanup evidence (#288)

[E1-01D](docs/E1-01D-TERMINAL-CLEANUP-CRASH.md) covers only the first C8 boundary:
TERMINAL persisted, before the real aggregate orphan/discovery/root cleanup call.
The kill/resume controls, independent recovery/replay, failure preservation, race repeat and
standard Docker integration/API/OIDC PASS; both recorded runner exits are 0. Internal
cleanup substeps and C0/C3 inventory remain separate; E1-01/X1 remain OPEN, production GC OFF.

## Previous merged slice: E1-01C physical recovery crash evidence (#287)

[E1-01C](docs/E1-01C-PHYSICAL-RECOVERY-CRASH.md) is limited to C6 (after real S3
DELETE, before phase persistence) and C7 (phase persisted, before TERMINAL).
[E1-01A](docs/E1-01A-G2-PROCESS-CRASH.md) (#281) and
[E1-01B](docs/E1-01B-G3-PROCESS-CRASH.md) (#283) are merged single-DC process-death
evidence for C1/C2 and C4/C5. C8 and the C0/C3 inventory remain separate;
C6/C7 kill/control and independent recovery/replay, preservation negatives, race repeat and the
standard Docker integration/API/OIDC stages PASS on the frozen source (see ledger).
E1-01/X1 remain OPEN and production GC remains OFF.

## Previous merged slice: verification sweep corrections (PR #285)

Branch `docs/verify-sweep-corrections`, base main@f580787ed. Independent re-read
of the seven PRs merged after the 2026-10-09 progress report against the code,
the tests and the sources of record. Corrections: the abandoned-repair
`retained` definition in [PUBLISH-REPAIR-OBSERVABILITY](docs/PUBLISH-REPAIR-OBSERVABILITY.md),
a dated W2-10 note in [E1-16](docs/E1-16-POST-D-CONTRACT-ALIGNMENT.md), and the
dangling PR #231 pointer in [OPEN-WORK-INDEX](docs/OPEN-WORK-INDEX.md). Open
findings reported with the sweep: E1-17's pre-HEAD recheck
was overreported as wholly uncovered: E1-16 already covers successful rechecks,
while the 15 residual legs reject at capture. PR #286 witnesses lost-reference
rejection; and `docs/FILE-LOCKING-DESIGN.md` is now referenced from
nothing. See [docs/CHANGELOG.md](docs/CHANGELOG.md) (2026-10-14).

## Previous merged slice: sandbox-gated dev multi-region stack (tooling, PR #284)

Branch `feat/sandbox-gated-multiregion`, base main@acc6c6f45. The dev
multi-region stack comes up on restricted hosts through
`./scripts/bootstrap.sh multiregion --sandbox`, which layers
[docker-compose.multiregion.sandbox.yaml](docker-compose.multiregion.sandbox.yaml):
region/nginx configs baked into the images, LB on 8081, USA on 3001, nginx
healthcheck on 127.0.0.1. Off by default — without the flag the compose
selection stays `-f docker-compose-multiregion.yaml` — and `--status` now routes
with a `Host` header instead of `/etc/hosts`. Host prerequisite, not in the repo:
the `buildx` CLI plugin (`~/.docker/cli-plugins/docker-buildx`); the server
Dockerfiles build `FROM --platform=$BUILDPLATFORM`, and without buildx the build
fails as "failed to parse platform". Sandbox mode checks for it before building.
See [docs/MULTIREGION-TESTING.md](docs/MULTIREGION-TESTING.md) §Sandbox and
[docs/CHANGELOG.md](docs/CHANGELOG.md) (2026-10-14).

## Active slice: E1-17 W2-10 residual content-resurrection handlers

Base main@747408b39 (#277 and #278 merged). Branch
fix/e1-17-w2-10-residual-handlers.

- **RED:** `RevertFile`, `RestoreTrashItem` (file and directory),
  `RevertDirectory` and `RevertDirents` directory items published HEAD on
  COMMITTED or retired P1 from an unpublished Sync commit (15 legs, all
  shapes).
- **Fix:** the E1-10e source admission now covers whole entries:
  - a strict recursive directory read;
  - each block-bearing file's own permanent `fs:`;
  - exact P, rechecked before every HEAD CAS.
- **Evidence:** GREEN 15/15; retained-history controls pass; an omission
  mutation brings RED back; race 4/4.
- **Result:** W2-10 CLOSED-FIX. The integration timeout is raised
  explicitly from 40m to 50m.

[E1-17](docs/E1-17-W2-10-RESIDUAL-HANDLERS.md).

## Previous merged slice: abandoned repair retention to PRE-GC (docs only, PR #278)

Base main@cf03f8415 (#277 merged). Branch docs/dead-row-retention-pre-gc.
`ISSUE-PUBLISH-REPAIR-DEAD-ROW-RETENTION-01` moves from FOLLOW-UP / GENERAL to
P2 PRE-GC with a measure-first plan:
1. use the existing repair metrics (new runbook section);
2. add a retain reason and a pinned-bytes estimate only if the retention is
   real;
3. disposition it before A1 from the data.

No TTL on the repair row, because the row may protect a publication already in
HEAD and the HEAD CAS has no deadline. No code, test or schema change.

## Previous merged slice: E1-16 post-D contract alignment with G4 (PR #277)

Base main@5c3163871803 (#276 and #261 merged). Branch
docs/e1-16-post-d-contract-alignment.

- **Decision:** the owner approved E1-15D §1 as alignment with G4 contract 5.
  X1 is physical: nothing may reinstall P1, revoke or postpone D1, or publish
  HEAD content depending on retired P1. A logical reference of L written after
  D1 is liveness of L. Its retention is PRE-GC convergence.
- **Verified against code:** the R31 safety guard is funnel-independent (repair
  rows have no TTL; the EACH_QUORUM scan matches `staged_block_ids`). Sync
  validates every added block regardless of provenance. `RecvFS` publishes
  nothing. Cross-repo copy captures the full recursive block set.
- **X1 §4:** W2-1..9 and W2-12..14 are CLOSED for X1; W2-10 stays OPEN.
  E1-15A/B and the R31 convergence residuals move from PRE-X1 to PRE-GC with
  severity unchanged.
- **Remaining before X1 CLOSED:** W2-10 (E1-17); E1-01, first inventoried
  against the G1–G5 crash evidence; the three-DC residuals (suite re-measured, 28 of 30 G5 failures pass); the D0 §25 list;
  and the identity claim-table PRE-X1 audit debt.

No runtime, test or guard change. [E1-16](docs/E1-16-POST-D-CONTRACT-ALIGNMENT.md).

## Previous merged slice: E1-15F SUPERSEDED across datacenters (PR #276)

Base main@e7a83ea789e8 (#275 merged). Branch claude/e1-15f-superseded-cross-dc;
frozen plan 7bbce0eec.

- **Fix:** a #275 liveness defect. A failed EACH_QUORUM read of the commit's
  parent let the walk pass the parent without the witness, which left the
  repair UNKNOWN until HEAD moved. The visit now stays UNKNOWN without
  walking. Unit RED → GREEN; 5/5 mutations.
- **Real 3-DC evidence, two runs, 9/9 phases each:**
  - HEAD = parent retains;
  - with dc-eu down, the visit stops at the witness read and retains;
  - after recovery, a dc-asia sweep settles SUPERSEDED, verified from
    dc-na and dc-eu at EACH_QUORUM;
  - REACHABLE promotes.
- **Regression:** go-all-test exit 0 (integration 1827.132s; API 20/20;
  OIDC 25/25); backends clean.

[Plan/evidence](docs/E1-15F-SUPERSEDED-CROSS-DC.md).

## Previous merged slice: E1-15E superseded abandoned repair settlement (PR #275)

Base main@1b39d6a0b2f9 (#274 merged). Branch
claude/e1-15e-abandoned-repair-settlement; frozen plan ab294c10c. Option A of
E1-15D §3, implemented. The resumable classifier emits SUPERSEDED when the
walk from its SERIAL HEAD anchor reaches the repair commit's immutable parent
(EACH_QUORUM) before the commit, and the anchor is not that parent. HEAD
monotonicity makes this permanent. Settlement removes only the repair-owned
`pub:` and the row.

- RED on main: 5/6 legs.
- GREEN: E1-15E 6/6.
- Amended suites, because their repair now settles or their UNKNOWN fixture
  had to keep HEAD at the parent: E1-15D, E1-15C C1, E1-12 crash-restart,
  E1-11, E1-13.
- Race 18/18, 0 data races. Unit mutations 4/4. go-all-test exit 0
  (integration 1587.426s; API 20/20; OIDC 25/25). Both backends clean.

Still UNKNOWN: HEAD = parent, a missing commit row. Retention is narrowed,
not closed. [Plan/evidence](docs/E1-15E-ABANDONED-REPAIR-SETTLEMENT.md).

## Previous merged slice: E1-15D post-D contract decision and repair cancellation authority (PR #274)

Base main@24372ba95fad (#273 merged). Branch claude/e1-15d-post-d-contract-decision;
frozen plan e49a8362f. Decision proposal plus one evidence pair, with no
production change. HEAD-writer audit: all writers are CAS moves to a child of
the current HEAD, so HEAD is monotonic. A real writer crash leaves R and
commit c1; real Sync `UpdateBranch?head=c1` auto-merges c1 into HEAD (200),
so R protected a still-publishable commit. After TERMINAL, Sync's publication
readiness gate rejects it. Race 6/6, 0 data races, gates PASS. Integration
budget raised 30m→40m. Proposed for approval: X1 = physical invariant;
convergence is a PRE-GC requirement; no cancellation authority exists today;
the next PR evaluates the minimal settlement first, global commit
cancellation only as a fallback. E1-15C wording amended ("abandoned", not
"dead"). No severity changed. Cross-audit follow-up: post-merge sweep
classifies R natively UNKNOWN and retains it (race 6/6 again). Fixed an
E1-12 marker-read race found by the first standard run. go-all-test exit 0
(integration 1736.896s, 72% of 40m; API 20/20, OIDC 25/25); both backends
clean. Final scoped audit: no unresolved introduced P0/P1/P2.
[Plan/evidence](docs/E1-15D-POST-D-CONTRACT-DECISION.md).

## Previous merged slice: E1-15C post-D references, terminal retirement and P2 rematerialization (PR #273)

Base main@304f6da8f7c30e4ad816d17d061dcde6816c0854 (#272 merged).
Branch claude/e1-15c-post-d-physical-life; frozen plan 11cbdb6b5. Evidence only,
no production change. On Office, post-D `pub:` and a dead repair do not stop
TERMINAL retirement of P1 or reinstall it. A real rematerialization publishes
P2 (new key, same content-addressed fs_id as the dead repair). The dead repair
re-pins L, and with every real reference expired the worker vetoes D of the
unreferenced P2 only because of it. Race 12/12, 0 data races, gates PASS. No
physical (D0 §13) RED measured; strict E1 reading stays RED. Proposed for
explicit decision: X1 safety = physical invariant, post-D refs = convergence
gate, next PR = dead-repair settlement (never removing `fs:<repo:fs_id>`). No
severity lowered. Review fixes: C1 requires the shared fs_id; C2 requires the
worker's own RepairGuardOnly answer. go-all-test on d3fcef19f exit 0
(integration 1562.886s, 87% of the 30m budget; API 20/20, OIDC 25/25); both backends clean, quota_usage 0, hard limits
unchanged. Final scoped audit: no unresolved introduced P0/P1/P2.
[Plan/evidence](docs/E1-15C-POST-D-PHYSICAL-LIFE-DISPOSITION.md).

## Previous merged slice: E1-15B writer publication after D, crash before the final fence (PR #272)

Base main@4290a2bb394b8748973b2a4b7e9b8340d122a2af (#271 merged).
Branch claude/e1-15b-writer-post-d-staging-crash; frozen plan d25a44350.
Characterization; the only production-file change is an integration-only
CreateFile seam before the final fence. A real CreateFile writer subprocess
resumed after natural exact COMMITTED D(P1) stages `pub:` post-D. No crash:
the fence rejects (409) and cleans up. SIGKILL after staging: durable `pub:`
(35d). SIGKILL after queue: `pub:` plus a non-expiring repair, renewed post-D
by every sweep (measured twice). Race 12/12, 0 data races, gates PASS.
Confirmed P1 PRE-X1 class ISSUE-PUBLICATION-POST-D-LIVENESS-BEFORE-VALIDATION-01,
not fixed. Pre-HEAD holds; strict post-D X1, E1-15, E1/X1 and activation
remain OPEN. Review fixes: the no-crash control requires 409 plus the
fence marker; the root finalizer is deferred before GC (negative control
PASS). go-all-test exit 0 with the integration budget raised 18m→30m
(integration 1223.464s; the 18m and 22m runs timed out without failures);
API 20/20, OIDC 25/25; both backends clean, quota_usage 0, hard limits unchanged. Final
scoped audit: no unresolved introduced P0/P1/P2.
[Plan/evidence](docs/E1-15B-WRITER-POST-D-STAGING-CRASH.md).

## Previous merged slice: E1-15A stale repair visitor vs concurrent durable cancellation (PR #271)

Base main@3583d989d39fc6c0e2706b426dde1131c3dcb3a7 (#270 merged).
Branch claude/e1-15a-stale-repair-visitor-cancellation; frozen plan cd12f83b9.
A real Office writer is held before HEAD with durable R; the productive sweep
classifies R natively UNKNOWN and pauses. Every CreateFile retry meets a real
competitor HEAD and runs real loser cleanup (8/8), clearing R. Real `up:` TTL
gives a natural candidate; the productive worker reaches exact COMMITTED D(P1).
Paused before the re-checks: no renewal. Paused after the last re-check:
**RED** on current runtime, a durable `pub:` after D (2700790c9).
Fix 7c59cc63a: post-renewal local absence escalates to EACH_QUORUM; confirmed
global absence withdraws exactly that pin; unavailable authority retains.
Race 12/12, 0 RED, 0 data races; unit tests, vets, gate negatives, two
causal mutations and the E1-11..14 matrices pass. Review reclassified it:
PARTIAL-FIX / CLOSED-EVIDENCE only when the global check and withdrawal
succeed. The write is not fenced, so crash, check/DELETE errors and ambiguous
renewal keep strict post-D X1 OPEN (P1 PRE-X1). Multi-DC and the shared writer write-before-validate class are
unmeasured. E1-15 overall, E1/X1 and activation remain OPEN. go-all-test exit 0 (integration 1009.387s, API 20/20, OIDC
25/25); both backends clean, quota_usage 0, hard limits unchanged. Final
scoped audit: no unresolved introduced P0/P1/P2.
[Plan/evidence](docs/E1-15A-STALE-REPAIR-VISITOR-CANCELLATION.md).

## Previous merged slice: E1-14 / W2-14 pub: expiry zero-ref transition (PR #270)

Base main@718cf85f726f8ed4137c942f57d23e17bf5acd90 (#269 merged).
Branch claude/e1-14-pub-zero-ref-transition; frozen plan dceb7df19.
Characterization only, with no production code change. Five legs on real
Office/CreateFile `pub:`: no-repair (writer stopped after `pub:` staging,
before repair queueing) and durable-UNKNOWN repair. `up:` is moved through the
productive renewal API and retired by TTL; Phase 0 resolves it and `pub:`
keeps the block live. The final `pub:` (no tracker/projection) is retired by
actual TTL to global EQ zero refs. Owned-scope Phase 0/1 and the productive
worker create no candidate/queue/D/root; P1/K1 retained. The repair sweep later
renews the exact repair-owned `pub:` (native UNKNOWN, no fs:/HEAD/P change).
Causal mutation: an `up:`-style projection on `pub:` yields a productive
candidate. Docker race 15/15 PASS, vets and three own negatives PASS.
Measured safety subset CLOSED-EVIDENCE / NOT-X1-RED. W2-14 transition/
convergence OPEN; ISSUE-GC-PUB-REF-ZERO-REF-01 confirmed P2 FOLLOW-UP
(retention). No projection/migration/scanner change. W2-12/13, E1/X1 and
activation remain OPEN. Final go-all-test on ce77a70c1 exit 0 (integration
828.637s, API 20/20, OIDC 25/25); both backends clean, quota_usage 0, hard
limits unchanged. Final scoped audit: no unresolved introduced P0/P1/P2.
[Plan/evidence](docs/E1-14-PUB-ZERO-REF-TRANSITION.md).

## Previous merged slice: E1-13 / W2-13 pending repair safety during discovery delay (PR #269)

Base main@fc50e168db91212cdb6d74b78f3f7db8756c6310 (#268 merged).
Branch codex/e1-13-repair-discovery-delay-safety; frozen plan cf2cf6c36.
Five productive Office/Cassandra/SILO legs with at least three actual earlier-
bucket repairs and fresh-process observers. Audit corrected pending-fs owner
cleanup/projections, a weak backlog-count check and shared-daemon schedule
contamination. Real future leases isolate the target; only sweep eligibility time
is controlled, with the same productive body and native classifier/GC clocks.
Final Docker race 15/15 PASS (160.321s), vets and own/W2 negatives PASS.
Guard omission reaches exact COMMITTED and own TERMINAL with teardown (16.053s).
Standard go-all-test retry exit 0: integration 705.954s, API 20/20, OIDC 25/25;
all 13 daemon controls PASS. First final run interrupted by Docker restart,
exit 255, not accepted. Both backends CLEAN, quota_usage 0, hard limits unchanged.
Every fixture verifies metadata/repair/bytes/expiry/owner cleanup. Existing e19 only.
Final audit: no unresolved introduced P0/P1/P2 in measured scope; ready for review.
W2-13 overall OPEN for discovery/convergence/scale; only measured pre-D safety
may close. W2-12/14, E1/X1 and activation remain OPEN. Standard dev GC unchanged.
[Plan/evidence](docs/E1-13-REPAIR-DISCOVERY-DELAY-SAFETY.md).

## Previous merged slice: E1-12 / W2-12 definitive-loser crash safety (PR #268)

Base main@2a6eb32fd1a5a6a0e71ddd18ddc0a2fedfaad0eb (#266 merged).
Frozen plan fb9911edd. Scope: one Office/CreateFile block, actual negative HEAD
CAS, Linux SIGKILL before request-local cleanup, fresh-process productive repair
sweep, real temporary-reference TTL expiry and owned-candidate GC on Cassandra/SILO.
Six named legs and independent fixture cleanup; existing e19 isolation only.
Final audit corrected one introduced P2: child upload expiry trackers/projections
were omitted by parent-local ref tracking. Corrected source d35f40b03 passes the
race matrix 18/18 (79.870s), both vets and own/W2 negative controls. Guard omission
reaches exact COMMITTED, then own TERMINAL and verified metadata/K1/expiry cleanup
(9.147s). Final standard Docker go-all-test completed PASS: integration 610.866s,
API 20/20 suites, OIDC 25/25; all 13 standard daemon controls PASS. Both backends
CLEAN, quota_usage 0, hard policy/limits unchanged. No unresolved introduced
P0/P1/P2 identified in the audited scope; ready for PR review.
W2-11 covered pre-D CLOSED-EVIDENCE. W2-12 overall remains OPEN for durable
loser authority/cleanup/convergence; W2-13/14, broader E1/X1 and activation OPEN.
Production GC OFF; standard development daemon configuration unchanged.
[Plan/evidence](docs/E1-12-KNOWN-LOSER-CRASH-SAFETY.md).

## Previous merged slice: E1-11 / W2-11 current-runtime repair liveness (PR #266)

Merged #266 in main@2a6eb32fd1a5a6a0e71ddd18ddc0a2fedfaad0eb; PR base was main@308d77df68afc2090d1a090528642950b028f4e3 (#267 merged).
Original frozen-plan base main@4f7c29923fb6022c4b7e48996b50918011781679 (#264).
Frozen plan 3dc849fbb. Real Office writer/repair visitor, isolated e19 keyspace,
actual up:/pub: TTL expiry, productive scoped GC and native Cassandra wire loss.
Six legs cover normal, during-classifier, post-REACHABLE, clean UNKNOWN from a
real interrupted loser, post-HEAD classifier failure and destructive guard failure.
No classification substitution or fabricated repair/P/HEAD/D. Production callback
is a no-op; only test-tagged session/library observation pauses the visitor.

Final matrix race 30/30 PASS (140.593s); after the G5 harness correction,
parent/child race repeats 18/18 E1-11 and 3/3 original G5 PASS (280.932s).
Exact COMMITTED guard-omission RED observed; productive recovery completes
TERMINAL before independently verified teardown (final script PASS, 26.231s).
Five filtered/unavailable child-gate controls and existing W2 negatives PASS;
ordinary/integration vets PASS. Test code/harness pinned to 73d083425.

Final standard Docker go-all-test exit 0: integration 956.708s, API 20/20 suites,
OIDC 25/25 tests; all 13 daemon controls PASS without GC-disabled skips.
Both backends CLEAN, storage quota_usage 0 and hard policy/limits unchanged.
Real traffic counters increase normally. A pre-existing manual G5 recovery race
is isolated to the existing e19 child; original assertions and dev daemon remain.
Standard integration budget 18m accommodates mandatory matrix/child overhead.
The first contaminated/timeout run is recorded, not accepted as GREEN.

Cross-audit of 1656de975 confirmed one P2 documentation/status contradiction.
Corrected historical OPEN/P1 labels and separated discovery/cleanup residuals;
Markdown only, runtime/test/Compose snapshot unchanged. Prior full Docker PASS
is retained as prior execution; this correction does not claim a new test run.

Scope reaudit: G5 harness isolation and its own gate controls are extracted
to prerequisite PR #267, now merged into main. #266 targets main. Effective
combined executable Go/Compose match c9a4eb23b; metrics comments are reconciled
in the latest audit. Three E1-11 controls stay in this PR;
two G5 controls belong to merged #267. Published #266 history is preserved.
Post-extraction Docker E1-11 6/6 race (49.586s) and three own negatives PASS;
standalone G5 9/9 race and two negatives/vets PASS. Both backends CLEAN,
storage quota_usage 0, hard policy/limits unchanged. After user merge of #267,
#266 is aligned with main; earlier full go-all-test is not claimed as rerun.

Latest audit at a1df08de1 confirmed one P2 live discovery/observability contract
contradiction. Reconciled W2-13, the runbook and metrics comments: discovery
remains OPEN for convergence/scale; historical health-gate proposal is superseded
for covered pre-D safety. Docker parsing/token-equivalence and document checks
PASS; metrics declarations/literals and all other Go/test/Compose are unchanged.
Prior full Docker results are retained, not claimed as a new run.

Updated scoped audit: no unresolved introduced P0/P1/P2. W2-11 CLOSED-EVIDENCE
for covered current-version pre-D safety only. W2-12..14, W2-10 overall,
cleanup/discovery/Paxos residuals, E1/X1, Phase5/6 and activation remain OPEN.
Production GC OFF; standard dev daemon configuration unchanged. No new TTL
policy, renewal mechanism, production queries/writes or health gate.
[Plan/evidence](docs/E1-11-CURRENT-RUNTIME-REPAIR-LIVENESS.md).

## Previous merged slice: E1-10e / PR #264

PR #264 targets main@79c5e2d2d (#265 merged); frozen plan c167aa947.
New cross-audit confirms three P2: serving-DC false misses across the new
admission chain, no productive rerun after d2d680f32, and stale live validation.
Scoped correction uses LQ→EQ only on clean absence for historical layout,
representation, SHA1 mapping, settled fs: and captured P; global readers stay
unchanged. Local hits avoid EQ, transport/contradiction errors fail closed.
The same original P is validated before each HEAD via the existing primitive.
Own up:/pub:/repair and continuous liveness through HEAD remain absent/OPEN.

FINAL updated-runtime validation: six fallback unit cases and 25 actual-query
consistency mutations PASS; mapping/writer/identity inventories and both vets
PASS. Productive matrix race 20/20 (212.576s), final standard Docker go-all-test
exit 0: integration 797.368s, API 20/20 suites, OIDC 25/25 tests, all 13 daemon
controls PASS with no GC-disabled skips. Both backends CLEAN, quotas free/hard
and limits unchanged. Earlier b18d3c980/d2d680f32 results are historical snapshots.
Scoped audit: the three supported P2 are corrected; no unresolved introduced
P0/P1/P2 found. W2-10 overall/E1/X1/R31/Phase5/6 remain OPEN; production GC OFF.
Standard dev daemon behavior remains unchanged.
[Plan/evidence](docs/E1-10E-W210-RESIDUAL-DISPOSITION.md).

## Previous merged slice: E1-10d / RevertDirents retained-history

Base main@f465d1405d147dc34e69c894c08c21b435df5c1d (#262 merged).
Plan frozen in 1339cb4c7. Single root file/path/plaintext block, retained
history, absent target. Actual form handler plus Cassandra/SILO; independent
item-path hooks/gate/selector, normal-build no-ops. No new publication protocol.
Productive GC positive-reference early skip; real per-item HEAD conflict/retry.
Cleanup registered early, productive DeleteFile async completion observed.
Narrow measured subset only; W2-10/E1-10/E1/X1/R31 remain OPEN. No automatic
RevertFile reclassification or Phase5/6 residual inference. Final go-all-test
exit 0: integration 592.182s, API 20/20 suites, OIDC 25/25 tests. New matrix
3/3, five race repetitions 15/15; prior selectors intact, all 13 standard daemon
controls PASS with no GC-disabled skips. Final owned test repos absent on both
backends, quota unchanged. Scoped audit: no unresolved introduced P0/P1/P2.
Production GC OFF, standard dev daemon unchanged.
[Plan and evidence](docs/E1-10D-REVERTDIRENTS-RETAINED-HISTORY.md).

## Previous merged slice: E1-10c / PR #262

Base main@a7548d2c0a75a89ad273ed714bfc86a93ad0266b (#260 merged).
Plan frozen in 76faebb54. Real directory create/upload/delete/revert measures
full named historical/winning HEAD/root/DIR1/FILE_FS1/P/K/bytes. Three initial
legs PASS: normal, positive retained-fs GC skip and real HEAD conflict/retry.
Own hooks are integration-only with normal no-ops; no new publication protocol.
Independent gate/selector/child preserve #259/#260 contracts and dev daemon.
No fabricated D; narrow retained-history evidence, W2-10/E1-10/E1/X1 OPEN.
Production GC OFF. Final Docker go-all-test exit 0: integration 814.242s,
API 20/20 suites, OIDC 25/25. New matrix 3/3, race repetitions 15/15; all 13
standard daemon controls PASS with no GC-disabled skips (gcsoak is separately
tagged). Interrupted cross-lib API fixtures are recognized by stale cleanup;
quota unchanged. Final owned test repos absent on both backends. Scoped audit:
no unresolved introduced P0/P1/P2.
[Plan and evidence](docs/E1-10C-REVERTDIRECTORY-RETAINED-HISTORY.md).

## Previous merged slice: E1-10b / PR #260

Base main@2f5635621f1a0dafafd24e0916fcdf88e8d0f149 (#259 merged).
Plan frozen in 265268ce3. Productive creation, parent-bound upload, directory
DeleteDirectory and actual restore preserve full HEAD/root/DIR1/FILE_FS1/P/K/bytes.
Normal/positive-GC/real HEAD-conflict controls PASS. Sole child fs: blocks
real worker before claim/D; inherited refs, no repair/settlement. Async deletion
housekeeping is synchronized by observed final real tag/counter queries.
No production changes. Independent directory gate/selector/child reuse existing
manual-GC keyspace; #259's contract unchanged, normal dev GC remains active.
Narrow retained-history evidence only; W2-10/E1-10 overall, E1/X1/R31 and
retention/Phase5/6 remain OPEN. Production GC OFF. Final go-all-test exit 0: integration 857.566s, API 20/20,
OIDC 25/25. Directory 3/3 and 15/15 race legs; 14/14 daemon controls with no
GC-disabled skips. Scoped audit: no introduced P0/P1/P2.
[Plan and evidence](docs/E1-10B-RESTORETRASH-DIRECTORY-HISTORY.md).

## Previous merged slice: E1-10 / PR #259

Base main@53298f7af4a1efc51039291146adc5f6e1204c43 (#258 merged).
Plan frozen in b823f11de. Actual single-file/one-block/plaintext upload/delete/
restore retains historical fs:. Directed normal, productive GC attempt and real
HEAD-conflict retry PASS. Worker settles the candidate before claim/global proof;
restore preserves original fs_id/P/K/bytes without repair or fs: settlement.
No production fix: only integration scheduling hooks and mandatory three-leg
gate in both standard Docker suites. Narrow retained-history CLOSED-EVIDENCE;
W2-10/E1-10 overall, R31, E1/X1 remain OPEN. Production GC OFF; shared dev GC and
E1-09 namespace isolation unchanged; E1-10 reuses that backend/keyspace for
owned-worker proof. An initial shared-daemon race run failed when its candidate
was consumed externally; no false PASS or global GC change. Final isolated
-race 15/15 legs PASS (83.639s); rejection/omission controls exercised.
Actual final Docker go-all-test PASS (exit 0): Go short/coverage, mandatory
integration 862.293s, API 20/20, OIDC 25/25 (zero OIDC skips). E1-10 3/3, E1-09
14/14 and daemon controls 14/14 PASS, zero disabled-GC skips. Final vet/style and
host/image/runner Go hashes agree. Scoped final audit found no unresolved
introduced P0/P1/P2; the existing W2-10 P1 remains OPEN.
[Plan and evidence](docs/E1-10-RESTORETRASH-RETAINED-HISTORY.md).

## Previous merged slice: E1-09 / PR #258

E1-09 / W2-9 (2026-10-05), base main@975b3057 with merged #257.
Plan e3aa03c3b, unchanged-runtime RED d8153a52c: productive source library purge
and real GC COMMITTED/TERMINAL allow unsafe destination HEAD/fs: in copy and
move. Move's later task failure does not undo the already published destination.
Fix captures exact source P per retry, checks staged identities and validates
exact-P after durable repair before HEAD. No new schema/lease/coordinator.
Fourteen real Cassandra/SILO copy/move legs include late captured-P retirement,
repair-first with genuine pub: expiration, actual HEAD conflict and skip replay.
The same task's progress data race is fixed by a locked value snapshot.
Only measured single-file/single-block plaintext same-org/representation
pre-HEAD is CLOSED-FIX; W2-9 overall, R31, E1/X1 remain OPEN and production GC OFF.
Validation: standard Docker PASS (Go integration 502.758s, 20 API suites,
25/25 OIDC checks), 140 named -race legs plus 42 final-source legs with HTTP
byte downloads, short/coverage and both vet modes PASS at 8d5596f9b.
Cross-audit confirmed the worker-creation P2, fixed by the shared SetLimit
scheduler. Direct MoveFile cross-repo returns 501 before publication; its 409
mapper change is hardening, and the earlier THIS-PR classification is retracted.
The cc5a9cc3d COMMITTED schedule needed isolation from external recovery.
The ccdebcb72 attempt disabled the shared daemon and caused 14 daemon-dependent
skips. That solution is rejected as P2 THIS-PR / TEST-INFRA and superseded.
Normal Compose again inherits primary GC_ENABLED from the env file; nodes 2/3
remain disabled. The added share-link prerequisite/skip is removed.
Only E1-09 runs in sesamefs_e19, served by a profile-test backend with GC OFF.
Both standard runners invoke the same test binary in an isolated child process;
its own TestMain requires all 14 legs before the parent can record evidence.
The normal daemon cannot discover that keyspace's recovery roots. Strict exact
COMMITTED certificates and authenticated status checks remain before/after writer.
Repeated active-daemon E1-09 -race validation: 42/42 legs PASS (139.748s).
Wrong active-daemon endpoint fails the mandatory child/parent gate (6.812s).
The first actual go-all-test exposed MaxRetry's pre-existing global-timestamp
wait race; its exact DLQ row appeared after about 64s. That test now polls its
own outcome with the original combined 90s budget and registers cleanup early.
The second complete run restored 14/14 daemon tests and E1-09, with zero
GC-disabled skips, but exposed a pre-existing native-proof cutoff/discovery race.
Its helper now retries only the actual dequeue (three attempts), never re-enqueues
or accepts a peer as proof. Exact candidate/P/bytes and own read remain mandatory.
A real peer in the precise window PASSes the scratch-source control (10.164s).
Both full failures are retained. Actual final go-all-test PASS (exit 0): Go
short/coverage, mandatory integration 845.139s, API 20/20, OIDC 25/25. E1-09 and
standard daemon cases both 14/14; zero disabled-GC skips. Native ambiguity full
matrix PASS (104.73s); final integration vet/style checks PASS. Source hashes
match the current Docker image. No unresolved introduced P0/P1/P2 in final audit.
The pre-existing P1 source identity and P2 FileFromBlocks fan-outs remain OPEN.
At 8d5596f9b, the 196 final-run physical keys/blocks, refs and owned expiry
projections were already absent; earlier owned RED artifacts cleaned.
[Plan, RED and evidence](docs/E1-09-CROSS-REPO-PUBLICATION-SAFETY.md).

## Previous merged slice: E1-7 / PR #257

E1-7 / W2-5 (2026-10-04), base main@42dc3c38 with merged #256.
Plan frozen in 07848d392. Six named real-Cassandra/SILO legs distinguish fresh
SHA-1 metadata without P/mapping from pre-existing web-upload P1. RecvFS creates
no Sync pin; real positive refs block GC early. After explicit source-owned
pin lapse, real GC commits D and retires P1; HEAD rejects until productive PutBlock
rematerializes P2. COMMITTED/TERMINAL controls, exact metadata/refs/mapping,
ordered repair/fs settlement, protocol replays and bytes are included.
No production runtime change. CLOSED-EVIDENCE only for the measured direct
PutCommit-object -> RecvFS -> PutBlock -> HEAD contract. W2-5/E1-05 overall,
W2-10, R31, E1 and X1 remain OPEN; production GC remains OFF.
Final-source validation: 60 named legs under -race, Go short/coverage and both
vet modes PASS; independent teardown verifies 80 physical keys/60 blocks and
owned expiry projections already absent. The full Docker suite rerun passed
before final harness hardening (integration, 20 API suites, 25 OIDC checks).
Earlier shared-stack control failures and harness corrections are documented.
[Plan and evidence](docs/E1-7-SYNC-RECVFS-BEFORE-PUTBLOCK.md).

## Previous merged slice: E1-6 / PR #256

E1-6 / W2-10a (2026-10-04), base main@f2837397 with merged #255.
Plan frozen in cef35d669 before instrumentation. Productive historical upload,
DeleteFile and RevertFile with real Cassandra/SILO show that leaving current
HEAD retains the historical fs: reference. With temporary up: explicitly lapsed,
the real block worker observes a positive LOCAL_QUORUM reference, settles its
candidate and skips before claim/global proof. Independent global observation
also reports a real reference. One/two-block, real HEAD conflict, same-content
and skip controls are included. Original layout/P/K survive; current RevertFile
still installs no new pin/pub:/repair/exact-P protocol. No runtime fix or
CLOSED-FIX claim: COMMITTED/TERMINAL with extant historical dependencies has
not been reproduced. W2-10/E1-10 and X1 remain OPEN; Phase 5/6 and GC activation
are outside this slice. Production GC remains OFF.
Full Docker Go/integration/API/OIDC and both vet modes PASS. Ten -race
repetitions (60 named legs) PASS; partial/unavailable and scheduling-omission
controls reject false evidence. Host/image/runner Go hashes agree. Final scoped
audit found no unresolved introduced P0/P1/P2; existing W2-10 P1 remains OPEN.
[Plan and evidence](docs/E1-6-REVERTFILE-PUBLICATION-SAFETY.md).

## Previous merged slice: E1-5b / PR #255

E1-5b / W2-7b (2026-10-04), base main@8ce5fd8c with merged #254.
Plan frozen in 818e0084b before runtime changes. Real chunked HandleUpload,
Cassandra and SILO reproduced 200 plus HEAD/fs: after victim COMMITTED and
TERMINAL retirement. Retain each confirmed exact P atomically with accounting;
validate every distinct P after durable repair before every HEAD attempt.
Blocked/Changed returns 409, cleans owned attempt state and invalidates only
positions of rejected digests. Same ChunkUpload retry rematerializes the victim
to a new P while retaining the healthy original P and upload operation.
Real HEAD-conflict revalidates both original placements. Omission restores both
retirement REDs with normal GREEN; filtered required-evidence run fails.
Unit controls cover missing/malformed placements, Unknown, contradictory
and coherent duplicates, selective recovery and bounded eight-read fan-out.
Full Docker Go/integration/API/OIDC and both vet modes PASS. Ten productive
-race repetitions (40 named subtests plus ten terminal recoveries) and ten
unit-control race repetitions PASS. Final scoped audit: no pending P0/P1/P2.
Measured single-shot and streaming pre-HEAD are CLOSED-FIX; post-HEAD/R31,
W2-11..14, E1-07 as a whole and X1 remain OPEN. Production GC remains OFF.
[Plan and evidence](docs/E1-5B-SEAFHTTP-STREAMING-PUBLICATION-SAFETY.md).

## Previous merged slice: E1-5a / PR #254

E1-5a / W2-7a (2026-10-04), base main@8319d077 with merged #253: plan frozen
in 13e7e6e69 before characterization. Productive single-shot SeafHTTP uploads
reproduced HEAD/fs:P1 after COMMITTED and TERMINAL retirement. Retain original
materialized SHA-256/class/key through every metadata retry; validate after
durable repair immediately before HEAD. Fixed rejection returns 409 and cleans
owned commit/pub:/repair/pending owner. Real HEAD-conflict control validates the
same P again; terminal replay creates P2. Omission restores both baseline REDs.
Measured single-shot pre-HEAD is CLOSED-FIX; streaming and post-HEAD/R31, broader
W2-7/E1-07 and X1 remain OPEN. Docker Go/API/OIDC, final full integration, 70 race cases, omission/filter controls and both vet modes PASS.
[Plan and evidence](docs/E1-5A-SEAFHTTP-SINGLE-PUBLICATION-SAFETY.md).

## Previous merged slice: E1-4 / PR #253

E1-4 / W2-8 (2026-10-03), base main@d2cb034dd with merged #252: plan frozen
in ad151bdf4 before characterization. Real signed OnlyOffice callbacks reproduced
HEAD plus fs: publication after exact P1 COMMITTED and TERMINAL retirement.
The fix retains the original materialized placement and validates it through
the existing exact-P authority primitive after durable repair and before HEAD.
Rejected attempts clean their owned publication state; terminal replay creates
P2 without reviving K1. Only the measured pre-HEAD contract is CLOSED-FIX;
post-HEAD W2-8/E1-08, R31 and X1 remain OPEN. Production GC remains OFF.
[Plan and evidence](docs/E1-4-ONLYOFFICE-PUBLICATION-SAFETY.md).
Final Docker Go/API/OIDC, both vet modes and ten race repetitions pass. The
final exact-P omission is RED in both retirement legs; the named-leg gate
rejects partial evidence. Replay success requires exact TERMINAL D1 plus new P2.

Prior slice:

E1-3 (2026-10-03), base main@abb558358 with merged #251: the plan was frozen
before implementation. Directed real-Cassandra/SILO Office legs show that a
late repair acquisition cannot publish HEAD under the held GC claim, and that
reachable settlement writes fs: before clearing repair so the second global
refs read vetoes D after the scan becomes negative. The observer sees actual
EACH_QUORUM results; no liveness responses or D rows are manufactured.
No runtime change; W2-11/14 and X1 remain OPEN. See
[E1-3 plan and evidence](docs/E1-3-LATE-PUBLICATION-PRE-D-PROOF.md).
Full Docker Go/API/OIDC, both vet modes and ten race repetitions pass. Omitting
final exact-P or the second global refs read is RED at its behavior assertion.

Prior slice: E1-2 (2026-10-03), base main@c1f31f7ec with merged #250: a real Office
CreateFile wins HEAD and stops before fs: promotion. After its actual up:/pub:
rows expire in Cassandra under a shortened-TTL time control, the durable repair
vetoes a new D at the productive worker's pre-handoff proof. A fresh-session
repair then classifies REACHABLE, promotes the exact fs:, settles its row and
preserves HEAD/tree/P1/K1. COMMITTED/TERMINAL cannot be reached in this measured
leg without bypassing the gate; neither is claimed as exercised. This is narrow
gate evidence, not closure of E1-02, W2-11/14 or X1. See
[E1-2 evidence and limits](docs/E1-2-REACHABLE-HEAD-LATE-REPAIR.md).
The full Docker Go/API/OIDC suite, both vet modes and ten race repetitions pass;
bypassing the worker's repair veto in a container-only mutation is RED.

Prior E1 slice: base main@eabd93bee includes merged G5 PR #248.
G5 durable-root execution is
complete for its covered contract; destructive GC remains OFF.
E1 is ACTIVE. Its publication/recovery counterexample matrix is frozen before
runtime edits: [E1 plan](docs/E1-FULL-GC-PUBLICATION-RECOVERY.md).
Highest priority is a real post-D late publication/repair; a delayed authorized
K1 PUT is separately classified as orphan bytes or a durable P1 dependency.
Re-evaluate each applicable W2/R31 row against G1→G5. A non-reproduction alone
does not close X1. Scope excludes Phase 5/6, G5 clock-health, stale-claim,
full PC-D1B.5, A1 and GC activation unless evidence proves the E1 invariant.
Work order: G5 -> E1 -> X1 CLOSED -> PRE-GC -> A1 -> GC ON.

E1 status (2026-10-03): real Cassandra plus pinned SILO
`RELEASE.2026-09-16T00-00-00Z` (MinIO-compatible S3) delayed Sync PutBlock after
terminal D left K1 bytes but did not restore P1; the handler materialized the
same block at a new P2 key. A post-terminal replay for an unreachable repair
retained UNKNOWN plus `pub:` only and left HEAD/P1 unchanged. Both are narrow
results; the reachable-HEAD late-repair leg and the remaining E1 matrix are
still OPEN. The Docker `go-all-test` suite passed after the integration test
changes; the two E1 cases pass under `-race`, and normal/integration `go vet`
pass in a single sequential Docker runner. No production runtime code changed;
GC stays OFF and X1 stays OPEN.

E1 cross-audit hardening (2026-10-03): fixed K2 plus exact Sync up:/expiry
teardown, checked UNKNOWN classification and clean typed retained visitation,
and corrected backend provenance to pinned SILO. The verifier also covers a
worker finishing between root discovery and the exact orphan read, with a real
worker regression. Full Go/API/OIDC, both vet modes, three directed RED
mutations and ten GREEN race repetitions pass sequentially in Docker. These
are test/evidence corrections; remaining E1/W2 rows and X1 stay OPEN. See the
[E1 audit ledger](docs/E1-FULL-GC-PUBLICATION-RECOVERY.md#cross-audit-hardening--2026-10-03).

## Gates

### ⚠️ Read this distinction before quoting any blocker count

Two different gates get confused constantly, and conflating them is how "X1 is
the only blocker" turns into "X1 closed → ship it":

| Gate | Blocked by | Status |
|---|---|---|
| **Activating destructive GC** (`GC_ENABLED=true`) | **X1, then the PRE-GC / A1 gate** ([docs/X1-CRITICAL-PATH.md](docs/X1-CRITICAL-PATH.md) §6). X2 closed 2026-08-14. | X1 OPEN; D0 architecture implemented through G1–G5 (#207, #209, #212, #247, #248); E1 in progress, with the exit items listed in [E1-16](docs/E1-16-POST-D-CONTRACT-ALIGNMENT.md); PRE-GC items open |
| **Putting SesameFS in production at all** | **Independent security / resource findings that have nothing to do with GC** | See the canonical non-GC readiness links below |

X1 is the critical-path blocker for the *first* row **only**, and even there
X1 CLOSED is not activation: the PRE-GC / A1 gate must also close. X1 is not
a production blocker for the second row, and no status document should say
that it is.

**Non-GC readiness:** consult [docs/OPEN-WORK-INDEX.md](docs/OPEN-WORK-INDEX.md) for
the current deployment blockers and [docs/KNOWN_ISSUES.md](docs/KNOWN_ISSUES.md) for
status per issue. Those findings remain independent of the destructive-GC gate.

---

Older slices, session summaries, the February priority list and the retired "frozen components" list are archived in [docs/legacy/CURRENT_WORK-HISTORY-2026-10-12.md](docs/legacy/CURRENT_WORK-HISTORY-2026-10-12.md). Each merged slice is also in [docs/CHANGELOG.md](docs/CHANGELOG.md).
