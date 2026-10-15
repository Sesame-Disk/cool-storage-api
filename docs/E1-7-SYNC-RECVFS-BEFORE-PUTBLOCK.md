# E1-7 / W2-5: Sync RecvFS-before-PutBlock publication continuity

## Frozen plan

Base: main@42dc3c38dc995fb80f74548813648b06395fb7b3, merged #256.

Characterize production before changing it. Split real Sync requests into
PutCommit object, RecvFS, PutBlock and HEAD phases; inspect Cassandra between
completed requests rather than adding runtime barriers for sequential calls.
Use real Cassandra/SILO and the current productive GC worker/authority.

Required named legs: fresh; fresh-head-before-put; reused; reused-committed;
reused-terminal; post-put-gc. Fresh content must show exact SHA-1 metadata,
absent mapping/canonical/P/references before PutBlock, unchanged HEAD, and
safe GC observation without creating missing mappings/blocks/placements.
Reused content is seeded by a productive non-Sync upload; no permanent fs:
is removed to manufacture zero. Observe original exact P and real mapping,
RecvFS replay, no implicit Sync pin, and the actual liveness boundary.
For retirement controls lapse only fixture-owned temporary refs explicitly,
use productive scoped GC to reach exact COMMITTED/TERMINAL, then continue the
real protocol. Assert HEAD/tree/commit, fs:/pub:/up:, repair, canonical and
mapping, lifecycle/root, original K1 and successful replacement P2 if any.
After PutBlock, attempt scoped GC and show convergence to existing readiness,
repair/final exact-P and settlement, retaining the original placement.

If a supported schedule reproduces unsafe retired-P publication, freeze RED
before implementing only the minimum funnel fix and mutation control. Otherwise
make no runtime change. Record CLOSED-EVIDENCE only for the measured ordered
contract; leave wider W2-5/E1-05 obligations explicitly OPEN unless supported
by the complete evidence. PutCommit object persistence is not HEAD publication.
Do not redesign SHA-1 layout, W2-9/10, R31/W2-11..14, Phase 5/6, coordinator,
PRE-GC/A1 or activation. Production GC remains OFF.

Require all named legs through TestMain and both standard Docker commands;
prove required filtered/unavailable runs fail. Execute directed/race evidence,
Docker regression and vet, inspect the complete diff and document precise
limits, then commit, push and create the PR.

## Measured outcome and disposition

Frozen plan commit: 07848d392. The six directed legs are GREEN on unchanged
production runtime. No unsafe retired-P publication was reproduced, so no
runtime fence or SHA-1 layout change is added.

| Named leg | Actual observation |
|---|---|
| fresh | PutCommit stores the commit without moving HEAD; RecvFS stores SHA-1 file metadata and one-entry tree; no forward mapping, canonical P, refs or repair exist. A real queue scan finds no owned block candidate; no delete target/claim is manufactured. PutBlock creates P and up:sync, then HEAD settles fs: and repair. |
| fresh-head-before-put | HEAD before PutBlock returns 503 with HEAD intact and no refs/repair. Real PutBlock and retry of the same commit subsequently publish normally. |
| reused | A productive web block upload seeds P1/mapping and only its source-owned up:. RecvFS and its replay preserve P1 without establishing a Sync pin. Controlled discovery plus the real GC worker observes that temporary ref and settles the candidate before claim/global proof. PutBlock reuses exact P1. |
| reused-committed | Explicitly lapse only the source-owned temporary pin. Real worker reads zero references, scans the entire repair domain, reads references again, commits D1, creates the exact COMMITTED orphan/root and retires canonical P1. K1 remains. HEAD before PutBlock rejects; metadata replay does not restore P1. PutBlock rematerializes P2, HEAD settles; old-D recovery deletes only K1 and leaves reachable P2 intact. |
| reused-terminal | Same productive D1 path, then the actual recovery worker deletes K1 and settles exact TERMINAL state/root cleanup before protocol continuation. HEAD rejects until real PutBlock creates P2; the original metadata/commit then publish successfully. |
| post-put-gc | Fresh RecvFS-first converges to canonical P and own up:sync after PutBlock. Productive GC sees the real positive pin and skips before claim/global proof; original P survives publication. |

Every leg also checks exact SHA-1 file layout and tree before/after publication,
forward mapping, exact refs, permanent fs: TTL, renewed Sync up: TTL, repair
absence after settlement, actual repair-write -> fs-write -> repair-delete order,
PutBlock/HEAD replay identity, and bytes served by the real Sync GetBlock.
Retirement legs certify exact lifecycle class/key, COMMITTED recovery authority
and root, TERMINAL physical deletion and cleanup, plus distinct replacement P2.
No fs: is erased to create zero. Temporary source-pin removal is an explicit
expiry-state control, not evidence that a wall-clock TTL was observed to expire.

The six phases call current productive Sync handlers in-process with the actual
library owner context and real Cassandra/SILO IO. The existing w24Handler
helper passes a nil permission middleware; these trusted-context tests do not
execute endpoint permission checks. Productive web
block-upload seeding uses HTTP. These tests do not certify external Sync auth,
permission middleware or routing; the existing external HTTP RecvFS-first regression remains
an independent control. The observed file rows are newly received SHA-1 rows
and exact replays, not replay of a paired-canonical fs_object created elsewhere.

Candidate/queue discovery is fixture-scoped using existing helpers. All claim,
zero-proof, handoff, canonical retirement and physical recovery decisions use
the actual store/worker. Positive temporary refs trigger the early LOCAL_QUORUM
skip; this is not a repair-guard claim-release observation. Fresh content has no
owned candidate/P to retire; an empty scoped real queue scan is not destructive
zero-proof evidence. COMMITTED includes productive G3 canonical retirement,
with exact orphan/lifecycle authority surviving for later physical recovery.

**CLOSED-EVIDENCE applies only to the measured direct ordered handler contract**
PutCommit object -> RecvFS -> PutBlock -> HEAD, including these reuse/retirement
continuations and early-HEAD negative controls. **W2-5 / E1-05 overall remain
OPEN/PARTIAL**: this does not certify auto-merge, every legal order, multi-block
or multi-DC, paired-canonical replay, concurrent publication boundaries,
post-HEAD repair/renewal, or the wider R31 matrix. It supplements existing W2-3
and W2-4 evidence without closing their shared post-HEAD residuals.
W2-10, W2-9, W2-11..14/R31, Phase 5/6, coordinator, PRE-GC/A1 and activation
are unchanged. Production GC remains OFF.

## Validation and final audit

All runtime checks ran in Docker with real Cassandra/SILO. Required gate
SESAMEFS_REQUIRE_E17_SYNC_RECVFS_CHARACTERIZATION is in TestMain's bootstrap
and final named-leg check, and in both standard Docker integration commands.

- Initial directed six-leg characterization: PASS (31.865s).
- Ten initial-source -race repetitions plus gate-wiring contract: PASS (299.897s),
  60 named legs. No data races reported.
- Full Go short/coverage regression and both normal/integration go vet: PASS.
- Required fresh-only filter: expected failure, the other five names missing.
- Required unavailable backend: expected failure before tests run.
- Isolated runner omission of only RecvFS fs-object persistence: expected failure
  at missing SHA-1 metadata despite successful RecvFS acknowledgement. This is
  harness sensitivity, not a retired-P counterexample or a new-gate mutation.
  Runtime restored by trap and its original SHA-256 verified.
- First standard suite: FAIL (658.702s integration). Two existing controls
  failed: aggregate storage snapshots did not return exactly to baseline within
  the soft-delete test's deadline; W2-4 direct/gcBeforeRepair found canonical P
  already absent at a verifier expecting the COMMITTED blocks row to remain.
  E1-7 passed all six names in that same run. API/OIDC were not reached by &&.
- The two controls, unchanged in an image built from main@42dc3c38, each PASS
  three isolated repetitions (11.063s combined). This contrast does not prove
  all shared-stack schedules are stable or erase the first failed run.
- Current-source combined E1-7, all ten W2-4 legs, and the counter regression:
  PASS (54.125s), with both E1-7/W2-4 completeness gates required.
- Standard suite rerun: PASS (669.730s integration), all 20 API suites and
  25/25 OIDC checks. This run preceded the final test-only teardown and peer
  observation hardening; it is not a full-suite run of that later revision.
- Final audit corrected cleanup ordering so exact physical objects and owned
  Sync expiry projections are cleaned before canonical fixture rows disappear.
  Earlier owned fixture leftovers were identified from exact evidence-log keys
  and removed only after canonical rows and references were verified absent.
- The first post-cleanup stress run exposed two harness failures with background
  GC peers: a peer consumed discovery before our positive proof, and a peer
  finished exact recovery before our visit. No unsafe HEAD publication occurred.
  Positive proof now requires our actual reference read after at most three
  rediscoveries; recovery accepts a peer only with SERIAL exact claim/time/P,
  TERMINAL, absent K1 and absent orphan/root. Neither absence alone passes.
- Final-source -race repetitions plus completeness/gate contracts: PASS
  (295.896s), all 60 named legs, no reported data races. Full Go short/coverage
  and normal/integration vet also PASS on this final Go source.
- Independent final teardown check: PASS; all 80 exact physical keys and 60
  canonical blocks, refs and Sync expiry projections already absent. No cleanup
  mutation was needed by this check.

Final diff audit found no unresolved introduced P0/P1/P2. Existing shared-stack
control instability is recorded above; this does not certify the remaining
W2-5/E1/R31 contracts.

No failing assertion/timeouts or existing runtime were changed to obtain the
contrast. Logs are outside Git under $TEMP/sesamefs-e17-{directed,race,
unit-vet,filtered,unavailable,omission,full-suite,baseline-controls,
paired-controls,full-suite-rerun,final-build,final-race,peer-safe-race,
peer-safe-unit-vet,peer-safe-image,final-residue}.log.

Host, final image and validation runner agree on these Go SHA-256 hashes:

| Source | SHA-256 |
|---|---|
| internal/integration/e17_sync_recvfs_test.go | ab581ec6cd0863e2217b7f83442e38cf2df70c3b7bbbf5438f636c50f10e5f88 |
| internal/integration/integration_test.go | 26d452109dd43f215a276b28386fedd8446244b51bb4728bbee3f1a2df390f44 |
| internal/api/sync.go (unchanged) | 0f78a4ac1776ffd291ec389977266876b5add4b0d9e7b3b731daf52ddf3a6fb6 |

Final test image: sha256:de37db3608deb51bfb8099f16b950e7c7823e6241955437ccdfd192d1c061ddc.
No backend image was redeployed. In-process handler evidence compiles current
source; external HTTP regression uses the existing dev stack. No optional 3DC
phase is certified. Production config still has GC_ENABLED=false; local manual
worker/discovery controls do not certify production candidate discovery/grace.

## Scoped three-DC verification plan (2026-10-15)

Base main@7a8bf5b86 (merged #299). Freeze before Go changes. Scope: the six
existing E1-7 ordered Sync legs, not the wider Sync protocol or E1-17 handlers.

1. Run unchanged TestE17SyncRecvFSBeforePutBlock on isolated Cassandra 5.0.9 /
   SILO, actual NTS RF1 in dc-na/dc-eu/dc-asia, GC-OFF backend. Require its named
   evidence gate; record failures, actual topology and owned cleanup.
2. Diagnose before fixing. If a local observed-worker wrapper omits validated
   fixture replication, correct only that adapter, preserving the native session,
   real gate, proof reads, original assertions and bounded peer handling.
3. Repeat all six legs, race, EU coordinator and one-DC compatibility. Preserve
   required filtered/unavailable negatives and the original RecvFS-persistence
   omission control, exclusively in disposable containers; restore/compare source.
4. Verify owned metadata/K1/expiry cleanup and classify retained auxiliary probes;
   run relevant short Go/vet and static guards in Docker. Audit source/diff/docs
   and current main, then open a focused PR with exact evidence and limits.

No runtime/schema/default/shared-helper refactor without a reproduced need.
No full-suite, external-client/auth, TTL, partition or all-order claim follows
from directed tests. P2 umbrella, E1-01/X1/PRE-GC/A1 stay OPEN; ordinary GC OFF.
