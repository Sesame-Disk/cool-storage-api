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

Pending; no new PASS or gate closure claimed until execution and final audit.
