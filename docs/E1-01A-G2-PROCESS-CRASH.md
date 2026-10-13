# E1-01A — G2 process-crash evidence

Frozen plan: 2026-10-13, base `main@b0d3a5361a6914ac9063fbb8298a704e12ddbe42` (#280).
Implements only C1/C2 of [the bounded E1-01 plan](./E1-01-CRASH-EVIDENCE-PLAN.md).
Test-only evidence; no runtime/schema/GC activation change. X1 and E1-01 remain OPEN.

- C1: real worker PREPARED return, before handoff commit. SIGKILL; independent root recovery aborts D1,
  preserves canonical P1/K1, and a second process replay remains harmless.
- C2: real blocks handoff committed, before Promote. SIGKILL; independent root recovery preserves exact D1,
  promotes/retire/deletes only K1 and reaches TERMINAL; a second process replay remains harmless.
- A no-kill control at each same boundary resumes the actual worker and completes physical recovery.
- Existing G4 P1/P2 coexistence is a separate unchanged regression; no extra crash cut or publication funnel.

Use current web upload to obtain P1; release only its owned temporary reference and enqueue its exact candidate.
The child calls `ProcessOrgOnce(ctx, orgID)` with a real-store wrapper scoped to that queue identity.
Prepare/Commit delegate real mutations and pause only confirmed returns. Parent verifies durable P/D/root/orphan,
bytes and handoff flag before killing; no fixture-fabricated lifecycle state. Recovery runs in another OS process
with new DB/storage sessions through current root enumeration. Parent verifies state before teardown.
Only complete/aborted authority may be removed during GC fixture cleanup; no lifecycle deletion ahead of recovery.

Require all four named legs, reject filtered/unavailable runs, preserve subtest filters through isolated forwarding.
Use the existing GC-OFF e19 keyspace/backend; fail if background GC is enabled. Keep production topology checks.
Wire the required gate into TestMain and both standard Compose Go runners. Scope gates in directed runs.
Validation: directed four-leg Docker matrix, independent replay and teardown; omission and unavailable negatives;
race repeat; G1–G5 regression/guards; normal/integration vet; standard Go unit/integration/API/OIDC Docker runner.
A reproduced product defect is a separate small fix PR. A harness failure is not physical RED or PASS.

## Evidence and final audit

Pending implementation and execution. No crash result is claimed by this frozen plan.
