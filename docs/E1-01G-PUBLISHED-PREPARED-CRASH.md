# E1-01G — PUBLISHED lifecycle with PREPARED orphan process crash

Frozen plan: 2026-10-14, base `main@6f8fc2630` (#291 merged).
Advances #201 E1-01, exactly C3 inside productive PromoteBlockDeleteOrphan.
E1-01/X1 stay OPEN; ordinary/production GC OFF. Missing crash evidence is not
itself a reproduced product defect; no gate closes in this slice.

- Run the real Worker.ProcessOrgOnce for the fixture's organization, preserving
  claim, zero-liveness, Prepare and committed G2 handoff. No global ProcessOnce.
- Reuse NewWithObservers in that worker process. Capture exact P/D and original
  first_seen from the delegated promotion caller and real PREPARED row; pause
  only the successful eight-argument owned lifecycle INSERT before orphan CAS.
  The diagnostic callback is not the durable oracle; add no production hook.
- Parent independently proves exact blocks deleting/committed D1/P1, PUBLISHED
  lifecycle, PREPARED/pending_s3 orphan, root/original token/discovery present,
  candidate/queue retained, original K1 bytes intact, unchanged HEAD, zero refs
  and no repairs. A C2 pre-Promote or C4 post-Promote state must fail this oracle.
- Require OS SIGKILL and same-boundary no-kill continuation. Independent orphan
  recovery must finish promotion/retirement to exact TERMINAL without revoking
  D1 or reinstalling P1. Then separately settle/replay the owned queue through
  ProcessOrgOnce; fresh-process orphan replay must remain idempotent. Preserve
  original discovery coordinates for post-root-removal checks.
- Preserve full snapshots on failure. Another process/session compares live state
  after failed teardown BEFORE explicit owned recovery/disposal. Failed legs must
  not retry recovery or erase K1/authority under inherited cleanup.
- Separate mandatory two-leg gate, reset per invocation, parent cleanup registered
  before availability checks, credit after teardown, TestMain and both Compose
  runners. Check omitted resume, unavailable backend, early SKIP in both repetition
  orders, unavailable recovery child and incomplete no-kill child completion.

Scope: one new test-only cut, minimal shared harness/gate wiring and evidence docs.
No runtime/schema/API/writer/scheduler/GC-default/dependency change. A reproduced
product defect needs a separate bounded regression/fix PR. C0, three-DC residuals
and final closure inventory remain separate; no automatic C8/E1-01/X1 closure.

Docker validation: directed G/preservation and static guards; G race repetitions;
negative gate/recovery probes with verified disposal; complete short Go tree and
normal/integration vet; standard required integration/API/OIDC including A–G and
all preservation controls. Record actual exits, immutable image/source hashes,
real topology, pre-kill state, recovery/replay and live preservation. Audit final
scope, links, command, gates, compatibility and current main before publication.
Backward compatible: yes, integration evidence and tracking only.
