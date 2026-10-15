# PR #293 — withdrawn harness implementation and evidence

Archived: 2026-10-15. This is a historical record, not the final implementation.
Source: PR #293 HEAD `86b67c3dfc4c5793f9e48f5dce536ea15b0d1217`;

- docs/CHANGELOG.md, lines 11–62.
- docs/TECHNICAL-DEBT.md, lines 2371–2410.

Reason: the helper's omitted replication map was real, but no current caller was
shown to fail because of it. The seven E1 legs already use the topology-aware
shareProjectionDBForTest. Switching the other helper to full config validation
introduced auth requirements in independent test runners. Four corrected runners
and H1 passed, but a subsequent clean-environment probe of the existing hard-delete
lease runner still reproduced the HMAC validation failure. That was not an
exhaustive audit of every runner or a production defect.

The maintainer chose to withdraw the helper, runner edits and added regression
from the final diff, rather than expand a maintenance change without a demonstrated
X1 fix. Both source sections below are preserved as recorded; statements that
code "now" does something describe the withdrawn proposal, not current main.
Its H1 result remains evidence for its frozen tested version, not an X1 closure
or certification of the final documentation-only PR.

Implementation references: topology version `4d374fe39c154e6f2d5b1a7e1702437737109dc7`,
runner fix `d71ff7e1c7cdfc29dd487239e91bba358d006171`. Original implementation
and subsequent withdrawal remain recoverable in PR history.

Current diagnosis and open work: [technical debt](../TECHNICAL-DEBT.md),
[current work](../../CURRENT_WORK.md), [X1 order and closure](../X1-CRITICAL-PATH.md).
The seven E1 three-DC progress failures remain open with their cause unestablished;
archiving this record closes no gate or activation requirement.

## Preserved changelog record

## 2026-10-15 — Shared projection DB helpers declare the fixture topology

Test-harness only; no runtime, schema or gate change.
`openIntegrationProjectionDB` now loads the server config the way
`shareProjectionDBForTest` has since `fa73da717`. Both shared helpers supply
`CASSANDRA_REPLICATION_CLASS`/`CASSANDRA_REPLICATION_DCS` to the destructive
GC gate's declared-vs-live comparison. Dedicated test connections remain outside
this change; no suite-wide topology claim is made.

Verified 2026-10-15 on the isolated single-DC stack: with the dev map declared
(`datacenter1:1`) the gate accepts the harness config; with a synthetic
`dc-na:1,dc-eu:1,dc-asia:1` declaration it refuses with the exact-mismatch error
naming those datacenters, a refusal the hand-built config could not produce because
it ignored the variable. H1 initial-head tests pass unchanged.

This **corrects** the three-DC entry in `docs/TECHNICAL-DEBT.md` rather than closing
it: the entry's mechanism attribution — that this helper blocks seven named E1 legs —
is unsupported, because those legs use the topology-aware sibling helper, so the
recorded "GC retirement n=0" symptom has some other, still unidentified cause. X1
remains OPEN and `GC_ENABLED=false` remains mandatory.

Post-rebase validation on `4d374fe39`: topology accept/reject discriminator,
all five H1 tests, provenance-cost characterization, H1 race repetitions, short
Go tree and normal/integration vet PASS. Standard required integration (A–G and
seven preservation controls), API 20/20 suites and OIDC 25/25 tests PASS; container
and outer exits 0. Subsequent precision edits change only docs and a Go comment.
Those earlier runs used single-DC RF1; the synthetic map is a topology negative.

Standalone runner follow-up, implementation `d71ff7e1c`: full config validation
introduced an auth dependency in four existing runner_env functions. Explicit
AUTH_DEV_MODE=true restores their development fixture contract without relaxing
production validation. New TestMultidcRunnerConfiguration runs their actual shell
functions with inherited application config removed; all four fail before the
fix and pass after. Child completion plus exact declared topology are mandatory.

Docker evidence (ignored tmp/pr293-* logs): runner-config-red exit 1 (four HMAC
errors); runner-config-green exit 0 (0.068s); runner-config-race count=2 plus gate/
fanout guards exit 0 (9.338s); runner-fix-unit-vet exit 0 (short Go and both vets).
Full h1-3dc exit 0: seed, publish and blind-EU initializer pass for both Sync and
v2, including TestMain's final verifier. Real Cassandra 5.0.9, dc-na/dc-eu/dc-asia
RF1, three UN nodes and matching declared/live maps. Backend GC stayed OFF.
The LF-normalized full script ran unchanged apart from its path in ignored tmp/;
fixtures were new/owned and three-DC teardown completed. No other project touched.

Frozen validation image sesamefs-pr293-runner-fix:
`sha256:5f3959667cad79695c62170ecce9b134ebd01ac06b67d12751693f60ff2f1da7`;
full H1 image sesamefs-h1-initial-head-3dc:
`sha256:a0e03c102a07a13ed66ee27b04f45d8ae9244e2c048174401af21eabe2c52311`.
Other three runners' configuration paths, not full scenarios, were rerun. Standard
integration/API/OIDC remain the earlier 4d374fe39 results, not new full-suite runs.
The seven E1 three-DC residuals remain unproved/open; no X1 closure or activation.


## Preserved technical-debt proposal and audit record

**E1 in-process-GC legs cannot run on a three-DC fixture (P2).** `openIntegrationProjectionDB` builds its `DatabaseConfig` without replication, so it declares only the local DC. On a three-DC keyspace the in-process GC's destructive topology gate correctly refuses the G2 handoff. As a result, E1-3, E1-7, E1-09, E1-10e, E1-11, E1-12 and E1-13 fail on fixture progress ("GC retirement n=0", "worker did not visit owned real candidate"), not on safety. These legs are single-DC evidence by design. If they are wanted on three DCs, the harness must declare the fixture topology, for example from `CASSANDRA_REPLICATION_DCS`.

**2026-10-15 (harness aligned; the mechanism above does not hold).**
`openIntegrationProjectionDB` now loads the server config the way
`shareProjectionDBForTest` has since `fa73da717` (2026-10-02). The two shared
projection DB helpers now use the same topology declaration. Dedicated test
connections, including the upload-mapping observer session, are outside this
change; not every connection in the suite has been aligned. Two corrections to
the entry above:

- **The attribution is unsupported.** E1-3, E1-7, E1-09, E1-10e, E1-11, E1-12 and
  E1-13 route through `shareProjectionDBForTest`, not through
  `openIntegrationProjectionDB`, and that helper already loaded the server config
  two days before this entry was written. Their recorded symptom therefore has
  some other cause, which is **not identified here and stays open**.
- **The named helper's reach is small.** Its callers are the H1 initial-head tests,
  the single-DC `TestW2SyncXDCProvenanceCostCharacterization` and `TestMain`'s
  cleanup; none runs destructive GC, so none was refused by the gate for this
  reason. Aligning the two shared helpers does not resolve or close the recorded
  three-DC fixture-progress failures.

Verified 2026-10-15 on the isolated single-DC stack: with the dev map declared
(`datacenter1:1`) the gate accepts the harness config; with a synthetic
`dc-na:1,dc-eu:1,dc-asia:1` declaration it refuses with the exact-mismatch error
naming those datacenters, which the hand-built config could not produce because it
ignored `CASSANDRA_REPLICATION_DCS`. H1 initial-head tests pass unchanged. The
seven legs were not re-run on a three-DC fixture.

**2026-10-15 (standalone runner compatibility).** Loading the full config exposed
a separate P2 in this PR: the four standalone Go runner_env functions did not
supply auth development configuration. config.Load rejected them, and TestMain's
final projection verifier failed before connecting to Cassandra. The migration
CLI's process-local .env loading does not configure later go test processes.
The runners now declare AUTH_DEV_MODE=true explicitly. A regression executes each
real runner_env in a clean subprocess, checks full config.Load and the three-DC
map, and fails for all four before the fix. After the fix it passes, including
race repetitions. The complete H1 script passes both blind initializer cycles
against three real DCs, including final cleanup and exit 0. Other three scripts'
configuration paths are verified, not their full scenarios. This restores runner
compatibility; it does not resolve the seven E1 failures described above or close X1.