# E1-01E — root-only TERMINAL process-crash evidence

Frozen plan: 2026-10-14, base `main@1b16f092f3e5c2abd9e5f607c3b60ebd2628595c` (#289).
Implements only [E1-01 section 0.3](./E1-01-CRASH-EVIDENCE-PLAN.md#03-selected-next-drill-one-internal-c8-root-only-terminal-cut):
after real orphan/discovery DELETE, before recovery-root DELETE. E1-01/X1 OPEN;
production GC OFF. Missing evidence is not a reproduced product defect.

- Reuse A/B/C/D's productive upload, owned reference lapse and separate
  ProcessOrgOnce preparation; no direct CQL setup of retired/terminal authority.
- The physical-recovery process uses existing NewWithObservers. Match only the
  successful exact discovery DELETE's full day/bucket/first_seen/org/L/P/D tuple;
  pause its real caller before it can issue root DELETE. No productive hook.
- Parent independently observes exact TERMINAL, retained root/first_seen, absent
  orphan/discovery/canonical/K1, unchanged HEAD, zero refs/repairs and settled work.
  A callback/marker alone is never the durable oracle.
- One SIGKILL leg and one same-boundary no-kill control; fresh-process recovery
  and another independent replay settle only the exact identity. Preserve the
  root/full snapshot on failure; another fresh session verifies live state before
  explicit owned disposal. Keep the discovery coordinate after root removal.
- Mandatory two-leg gate in TestMain and both Compose runners. Reset per invocation,
  check parent cleanup before early SKIP, credit after fixture teardown; test
  omitted legs, unavailable backend/recovery child and both SKIP repetition orders.

Scope: one cut, reused harness, no runtime/schema/writer/scheduler/config-default
or dependency change. D's intact-orphan cut and A/B/C behavior remain unchanged.
Earlier internal orphan-only deletion, C0/C3, P2 publication, global scheduling
and three-DC residuals stay separate. A reproduced product defect requires its
own bounded fix PR, not an invented root reclamation obligation.

Docker validation: directed E and its preservation control; standard suite retains
A/B/C/D and their preservation controls; final-source E
race repetitions; omitted/backend-unavailable/child-SKIP and repetition negatives;
short Go tree and normal/integration vet; standard required integration/API/OIDC
with existing G1/G4/G5 and no-global-fanout guard. Record image/source/topology,
actual exits, pre-kill oracles, recovery/replay and preservation before disposal.

## Validation and audit ledger

Frozen implementation: `5784f63c8a14c119aca65187cb377574598994d0`.
Final runner image (directed/race/negatives/standard):
`sha256:7757a2c7f90a59a6895d77061c31c73738c5de0e1e3f101fdce62a38e82d6775`.
SHA-256 inside that image:
- E cut/observer: `7ef71c697aa19dd5e4d82edcd1134558b83f85cfb4da695f3f6b44465b4a37e8`.
- Shared harness: `5274dd96f0e764a25bef38366d8913ff0b052ff034bfa8651bcb72b2a3b7ea86`.
- Physical actor/preparation: `7f770737a16636739b30c3b7fdda3d6bc12ff9603154b978e19cff39df9ecddd`.
- Terminal classification: `25160174467fb47315bd339030da430307dafd8b5d3f9e40ebb76b6e2d937efe`.
- TestMain: `916d6120c217a009cf667913bb84f8f2f3239fa77f58ad4195193f5c18556be5`.

Linux Docker, real Cassandra 5.0.9, actual datacenter1/RF1 NetworkTopologyStrategy,
LOCAL_QUORUM/SERIAL configuration, explicit SERIAL lifecycle/canonical snapshot and
EACH_QUORUM scoped metadata reads. SILO `RELEASE.2026-09-16T00-00-00Z`.
E19 backend image `sha256:a53db0b97cb82304f58973aae8d848ad67fa0789489c891b760df4f83223be4e`,
binary SHA-256 `e6facb5f64f3b3104793242cf57f4dbd1a1279d6bb377e5d6d11ec5596aec1e3`.
Productive sources are unchanged. E19/node2/node3 GC OFF; previously authorized
main development GC is outside this isolated keyspace. No GC setting was changed.

Both positive legs independently observed the exact root-only precondition before
kill/resume: TERMINAL, original root token retained, orphan and its exact day/bucket
projection absent, canonical/K1 absent, zero refs/repairs, settled owned work and
unchanged HEAD. Original K1 bytes were checked before separate productive retirement.
Actual SIGKILL is asserted. Fresh-process recovery and another replay remove the
root without restoring K1 or changing TERMINAL/HEAD; the saved discovery coordinate
is checked even when the root no longer exists.

The first directed run failed its preservation control with HTTP 404 on final
library disposal (`e101e-directed-before-fix.log`, exit 1, 97.129s). The snapshot
and root survived; an extra forwarding TestMain lacked the fixture-owning process's
liveRepoIDs and swept its library. E's intentional-failure child now starts directly
on e19, retaining its own liveRepoIDs through the sweep. A/B/C/D paths and block
cleanup logic are unchanged. This was a harness failure, not a runtime counterexample.

Local ignored `tmp/` logs and explicit `.exit` records:
- `e101e-directed.log`: kill/resume, independent recovery/replay, failure preservation,
  static gate wiring and no-global-fanout guard PASS (82.063s, exit 0).
- `e101e-race.log`: same E cases repeated twice with race PASS (120.748s, exit 0).
  Four verified SIGKILLs: one positive and one preservation negative per repetition.
- `e101e-unit-vet.log`: complete short Go tree and normal/integration vet PASS, exit 0,
  rerun on the final frozen image after the preservation correction.
- `e101e-gate-negatives.log`: omitted resume and unavailable backend each exit 1;
  required gate is enabled alone, with explicit missing-leg/backend output.
- `e101e-skip-first.log` / `e101e-skip-second.log`: injected early test SKIP in either
  repetition order is rejected, test exit 1 (31.089s / 31.243s). Each log also proves
  both kill/resume legs PASS in its healthy repetition; no product failure inferred.
- `e101e-s3-negative.log`: only the recovery child's S3 endpoint is faulted in a
  disposable source copy. Real helper SKIP/exit 0 is rejected by the parent (test
  exit 1, 30.828s). Root-only TERMINAL and full snapshot survive all failed cleanups;
  the JSON is persisted outside the --rm container.
- `e101e-s3-negative-disposal.log`: a new process/session compares the unchanged live
  snapshot BEFORE explicit owned recovery/disposal; PASS (1.284s, exit 0), external
  JSON byte-identical. Negative-probe wrapper exits 0 only after semantic assertions;
  these are expected failing test runs, not positive PASS evidence.
- `e101e-go-all.log`: full standard runner PASS; short Go coverage, required integration
  (1491.719s), API 20/20 suites (93s), OIDC 25/25 tests (one suite, 1s).
  A/B/C/D/E matrices and all five preservation controls PASS, alongside G1/G4/G5
  and the no-global-fanout guard. Container and outer process exits both record 0.
  Conditional three-DC/topology/load/helper SKIPs are not counted as positive evidence.
  The exact Compose command is enveloped only to persist actual exit atomically
  before --rm; it does not alter the required gate list or test command.

Scope remains one internal C8 boundary, one DC, fixture-local enumeration inherited
from A/B/C/D. No earlier orphan-only deletion cut, C0/C3, P2 publication or global
scheduling/checkpoint/fairness proof is inferred. E1-01/X1 remain OPEN; this evidence
authorizes no ordinary or production GC activation.

Final audit: no blockers identified in the scoped diff. The observer delegates all
real calls and matches the full owned query tuple; independent durable reads, not
query success, certify the cut. Actual SIGKILL, separate process/session completion
markers, before/after full snapshots and post-teardown live comparisons are checked.
E's omitted/SKIP/unavailable evidence cannot PASS; A/B/C/D gate semantics remain
unchanged. Original discovery coordinates survive root removal. No runtime/schema,
GC default, dependency or writer changes; no new product violation reproduced.
All five modified Go source hashes match the frozen tested image; links/anchor,
Compose configuration and `git diff --check` PASS. Main remained at 1b16f092f.
Later commits only update evidence/index/history documentation. No additional Sync
or frontend suite was run for this test-only change; no three-DC K claim is made.
