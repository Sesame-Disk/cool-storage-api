# E1-01H — root persisted before PREPARED process crash

Frozen plan: 2026-10-15, base `main@a71eec36a` (#292 merged).
Advances #201 E1-01, exactly C0 inside current PrepareBlockDeleteOrphan.
E1-01/X1 stay OPEN; ordinary/production GC OFF. Safe retention is a valid
result, not a reproduced defect or an obligation to reclaim an unsettled root.

- Use productive upload, controlled owned-reference lapse and real
  Worker.ProcessOrgOnce for the fixture organization: claim and zero-liveness
  remain real. Capture exact P/D from its delegated Prepare call.
- Existing NewWithObservers pauses only the successful owned nine-argument
  recovery-root INSERT, before the producer can submit its PREPARED LWT. No
  productive hook or direct setup CQL fabricates root, orphan or authority.
- Parent independently proves exact deleting/uncommitted blocks claim, root and
  original token present, orphan/discovery/lifecycle absent, candidate/queue
  retained, original K1 bytes intact, zero refs/repairs and unchanged HEAD.
- Before either action, an independent real recovery process must report zero
  completed orphan recoveries, prove the exact missing-orphan/unpublished-lifecycle
  executor visits, and leave the full C0 snapshot unchanged while the
  producer is paused. Resume must still let that producer finish normal G2/G3.
- SIGKILL leg: verify OS death, then two fresh recovery/replay processes must
  retain exactly C0, including K1 and the unresolved root/claim. No bounded
  reclamation or convergence is inferred. Resume leg: require productive child
  completion, independent physical recovery/replay and exact TERMINAL settlement.
- Keep original discovery coordinates for all after-root-removal checks. Only
  AFTER positive retention proof and producer death may explicit owned fixture
  disposal revoke the exact uncommitted claim and use the existing serial
  DeletePreparedBlockDeleteOrphan cleanup. That is test disposal, not automatic
  root recovery. Never run it as failed-leg teardown.
- Failed legs preserve full snapshots/live state. A new session compares that
  state after teardown BEFORE separately authorized fixture disposal. Run the
  failed-fixture child directly on E19 to retain its liveRepoIDs.
- Separate mandatory two-leg gate: reset per invocation, register parent cleanup
  before availability checks, credit after successful teardown, TestMain and both
  standard Compose runners. Negatives: omitted resume, unavailable backend,
  early SKIP in both repetition orders, unavailable retention child after SIGKILL,
  and missing no-kill completion; verify preserved live state before disposal.

Scope: one C0 test-only cut, minimal shared harness/gate wiring and lean tracking
and evidence docs. No runtime/schema/API/writer/scheduler/GC-default/dependency
change. No automatic root removal, lease expiry rule or new recovery deadline.
A reproduced product defect requires its own bounded regression/fix PR. C0–C8
coverage reconciliation, three-DC residuals and gate closure remain separate.

Docker validation: directed H/preservation plus static guards; H race repetitions;
negative probes and fresh verified disposal; complete short Go tree and normal/
integration vet; standard required integration/API/OIDC including A–H and all
preservation controls. Record image/source hashes, real topology, actual exits,
pre-action and post-kill retention, resume recovery/replay, and disposal separately.
Audit final scope, links, command, gate semantics, compatibility and current main.
Backward compatible: yes, integration evidence and tracking only.

The prior SesameFS Docker project was absent. Validation uses a separate
`sesamefs-e101h` project with fresh private volumes/network and no published
host ports. Database/storage endpoints are checked to resolve only inside that
project. Existing local test GC settings are preserved: only its owned standard-
runner namespace has the development scheduler; E19/node2/node3 GC stay OFF.
Other projects and ordinary/production deployments are untouched.
