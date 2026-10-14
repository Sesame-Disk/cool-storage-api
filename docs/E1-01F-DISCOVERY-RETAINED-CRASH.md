# E1-01F — orphan-absent/discovery-present TERMINAL process crash

Frozen plan: 2026-10-14, base `main@2b282a07a` (#290 merged).
Advances #201 E1-01, one internal C8 cut in the current DeleteS3Orphan ordering.
E1-01/X1 remain OPEN; ordinary/production GC OFF. No gate closes here.

- Reuse productive upload, owned reference lapse and separate ProcessOrgOnce
  preparation with real claim, zero-liveness read, G2 handoff and retirement.
- Use existing NewWithObservers in the physical recovery child. Match successful
  DELETE FROM gc_s3_orphans on the exact org/L/P/D six-argument tuple; pause its
  real caller before discovery DELETE. No runtime hook or direct state fabrication.
- Parent independently proves exact TERMINAL, orphan/canonical/K1 absent,
  discovery present at original day/bucket/first_seen/P/D, retained exact root
  and token, settled bookkeeping, unchanged HEAD and zero refs/repairs.
- Require OS SIGKILL and a same-boundary no-kill control. Fresh-process recovery
  and another independent replay must remove discovery/root without restoring
  K1, canonical placement or changing TERMINAL/HEAD. Retain discovery coordinates
  for post-root-removal checks; do not accept completion markers alone.
- Preserve the full boundary snapshot on failure. A fresh process/session must
  compare live state after failed teardown before explicit owned disposal.
- Separate mandatory two-leg gate: reset per invocation; parent cleanup registered
  before early SKIP; credit after fixture teardown; TestMain and both Compose
  runners fail closed. Verify omitted leg, unavailable backend, early SKIP in both
  repetition orders and unavailable recovery child on disposable source copies.

Scope: one test-only cut, minimal shared harness/gate wiring and evidence docs.
No runtime/schema/API/dependency/default change. A reproduced product defect
requires a separate bounded regression/fix PR. C0/C3 and three-DC residuals remain
separate; no all-C8 or X1 closure is inferred from this one observation.

Validation in Docker: directed F/preservation plus static guards; race repetitions;
negative gate/recovery probes and post-failure disposal; short Go and normal plus
integration vet; standard required integration/API/OIDC including A/B/C/D/E/F.
Record actual exits, immutable image/source hashes, topology, durable pre-kill
state and recovery/replay. Audit final diff, links, gates, compatibility and main
before publication. Backward compatible: yes, test/evidence only.
