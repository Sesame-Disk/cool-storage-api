# E1-01D — terminal-before-cleanup process-crash evidence

Frozen plan: 2026-10-14, base `main@b4a2eaf3cfb508fd5bc75dfb543d340ff6d5c63d` (#287).
Only the first C8 boundary of [E1-01](./E1-01-CRASH-EVIDENCE-PLAN.md); E1-01/X1 OPEN.
Production GC remains OFF. Missing process-death evidence is not a reproduced runtime bug.

The current worker persists TERMINAL then calls DeleteS3Orphan. The concrete Cassandra
store removes the exact orphan, by-day discovery and recovery root within that one call.
Current A/B/C process cuts stop before TERMINAL; G1's terminal replay controls are useful
real-state evidence but do not kill the productive recovery actor at this boundary.

- One cut, `terminal-before-cleanup`: a test-only GCStore wrapper pauses at entry to
  DeleteS3Orphan for the exact owned org/L/P/D, before delegating the real call.
- Reuse C's real upload, separate ProcessOrgOnce preparation and productive recovery process.
  No fabricated lifecycle/deleting state, direct setup CQL mutation or productive hook.
- Parent verifies exact TERMINAL by SERIAL lifecycle observation, canonical/K1 absence,
  retained COMMITTED/pending_mapping_cleanup orphan, exact by-day discovery and durable root,
  settled candidate/queue/pending/DLQ, unchanged HEAD and zero references/repairs before death.
- SIGKILL and same-boundary resume controls. Fresh-process recovery and another independent
  replay must remove exact orphan/discovery/root and work without changing TERMINAL or HEAD
  or recreating K1. Include the by-day coordinate in snapshots, even after root removal.
- A deliberately failed post-kill leg retains orphan/discovery/root and the full snapshot
  through all cleanups. Another session verifies unchanged live state before explicit disposal.
- A required two-leg gate in TestMain and both standard Docker runners rejects omission,
  unavailable backend and recovery-child SKIP. Preserve A/B/C meanings and gates.

Scope budget: one small integration file, minimal shared helper/C wiring, gate/runner and
one ledger; two brief active/history index updates. Roughly 300 added handwritten lines.
No runtime/schema/writer/scheduler/config-default/activation/dependency changes.
Internal cuts between orphan/discovery/root deletes, C0/C3 inventory, P2 publication,
3-DC residuals and global scheduling/checkpoint obligations remain separate. This does not
close all C8 or E1-01. A real product failure gets a separately reproduced, bounded fix PR.

Docker validation: directed A/B/C/D and preservation controls, final-source race repeat,
new-gate omission/backend-unavailable/child-SKIP negatives, complete short Go tree and
normal/integration vet, standard required integration/API/OIDC with G1/G4/G5 regressions.
Record source/image/topology identity, command outcomes, before/after exact state and cleanup.
Persist outer runner exit separately so a resumed client cannot lose that evidence.
Final audit checks real-call ordering, no simulated authority, projection coordinates,
fresh sessions/markers, preservation, scope, compatibility, links and clean diff.

Evidence: implementation and Docker verification pending; no new result or closure claimed.
