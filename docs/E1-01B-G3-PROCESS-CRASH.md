# E1-01B — G3 process-crash evidence

Frozen plan: 2026-10-13, base `main@f8e08008c90fa70790651f21bf98d6cc9d22d40b` (#281).
Only C4/C5 of [E1-01](./E1-01-CRASH-EVIDENCE-PLAN.md); test-only, E1-01/X1 OPEN.
Current G3 CrashBefore/AfterFinalize tests simulate replay with MockStore; real G4 calls store
steps directly. These are useful existing controls, not OS death of the productive worker here.

- C4 `before-retirement`: after real Promote confirms COMMITTED, before Finalize.
  Exact blocks(P,D) still exists; root/orphan COMMITTED and published lifecycle are durable; K1 exists.
- C5 `after-retirement`: real Finalize returns Finalized, before candidate/queue settlement.
  Exact canonical P1 is absent; the same root/orphan/lifecycle and candidate/queue survive; K1 exists.
- Each boundary has SIGKILL + same-boundary resume controls. Parent confirms the cut before killing.
- A fresh productive `ProcessOrgOnce` settles retained candidate/queue, then another process replay
  proves idempotence. Parent verifies unchanged exact continuation authority and K1 before physical recovery.
- Separate fresh-process root recovery and replay reach exact TERMINAL with K1 absent, unchanged HEAD,
  zero references/repairs and no owned candidate, queue, pending or failed row.

Reuse #281's fixture, subprocess, completion markers, snapshots and preservation of failed fixtures.
Extend its small matrix/forwarding helpers to accept the two cut names and boundary/replay oracles;
keep C1/C2 controls and their failure negative unchanged in meaning. Wrapper mutations delegate the
actual store; pause only successful Promote/Finalize returns, for the owned org/L/P/D.
No runtime/schema/writer/scheduler/activation change or additional publication funnel belongs here.
Existing G4 P1/P2 coexistence remains a separate unchanged regression.

Gate requires all four named G3 legs in TestMain and both standard Docker runners. Scoped omission,
backend unavailable and recovery-child SKIP must not pass. A C5 failure negative proves the retired
row does not let teardown erase D, queue/candidate or K1; incomplete snapshots retain UNKNOWN/fixture.

Docker: directed A+B matrices and failure controls, race repeat, omission/unavailable/SKIP negatives,
G1–G5/guards, normal/integration vet, complete short Go tree and standard integration/API/OIDC.
Freeze source/image identities. Run shared fixtures sequentially. Real product RED gets a separate
small fix PR per root cause; harness failure does not count as physical RED or positive evidence.

## Evidence and final audit

Pending execution; no new result is claimed by this frozen plan.
