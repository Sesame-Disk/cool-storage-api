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

Completed and pending validation are recorded below; no E1-01/X1 closure claimed.

## Evidence and final audit ledger

Frozen implementation: `f8c75e72691a2f8fb93f8ef13bc1422b758e7d77`.
SHA-256 read inside the final tested image:
- Shared A/B/C/D harness: `dfe9b2dc685b7a576e8ac0f3f3e2f6ddbffa2b358895052f547c6f55b1b28194`.
- Reused physical preparation/actor: `bb969f34ab876e1215da884044805f9b2470fd9f3dfcb4d886e1771beee46552`.
- Terminal cut: `415db85eb344cf7acc2a1c4b8bfb873557fec846284da920da253cc7f0efe76e`.
Runner `go-integration-test`: `sha256:bd879c3d5516946b5224b80603b64cfb8ef9356aded101fb9ccc74943381f39e`.
Runner `go-all-test`: `sha256:358bc69cb757e77b512ea80fe7ba750a59643409bdaa8ee6b1b82633e7c71604`.
E19 backend: `sha256:a53db0b97cb82304f58973aae8d848ad67fa0789489c891b760df4f83223be4e`;
binary SHA-256 `e6facb5f64f3b3104793242cf57f4dbd1a1279d6bb377e5d6d11ec5596aec1e3`.
Productive GC/API/DB/storage/config/dependency sources are unchanged since merged #281.

Linux Docker, Cassandra 5.0.9, actual datacenter1/RF1 NetworkTopologyStrategy,
LOCAL_QUORUM/SERIAL configuration; explicit SERIAL canonical/lifecycle and EACH_QUORUM
exact snapshot reads where required. SILO `RELEASE.2026-09-16T00-00-00Z`.
All APIs/Cassandra/SILO healthy. E19/node2/node3 GC OFF; existing explicitly authorized
main development GC remains outside the isolated e19 keyspace. No configuration was changed.

Both kill and resume reached the same durable boundary: exact lifecycle TERMINAL,
COMMITTED/pending_mapping_cleanup orphan, exact by-day discovery and recovery root present,
canonical/K1 absent, owned candidate/queue/pending/DLQ absent, HEAD unchanged, zero refs/repairs.
Original K1 bytes are verified before physical recovery. First recovery and another replay,
each in a separate process/session, remove orphan/discovery/root without recreating K1 or
changing TERMINAL/HEAD. The original root's first_seen_at stays in process data so the parent
can verify the exact discovery row after the root itself disappears.

Local ignored `tmp/` logs and explicit `.exit` records:
- `e101d-directed.log`: A/B/C/D 14/14 legs, all four preservation controls and gate PASS
  (195.754s, outer exit 0).
- `e101d-race.log`: race/count=2, A/B/C/D 28/28 legs and four preservation controls twice
  PASS (484.829s, outer exit 0). 22 verified SIGKILLs: 14 positive and eight preservation negatives.
- `e101d-unit-vet.log`: complete short Go tree and normal/integration vet PASS, exit 0.
- `e101d-unavailable.log`: terminal gate alone rejects missing backend (test exit 1).
- `e101d-omission.log`: terminal/kill passes, but omitted resume is rejected (test exit 1).
- `e101d-s3-negative.log`: disposable source copy faults only the recovery child's S3 endpoint.
  Real helper SKIP/exit 0 is rejected by the parent (test exit 1); TERMINAL, orphan, discovery,
  root and full snapshot survive all failed-child cleanups, with K1/canonical/work absent.
  `e101d-s3-negative-disposal.log` verifies unchanged live state from a new session BEFORE
  explicit owned disposal, PASS/exit 0; the external JSON remains byte-identical.
- `e101d-go-all.log`: complete standard runner PASS; short Go tree, required integration
  (1149.101s, A/B/C/D and all preservation controls), API 20/20 suites (99s) and
  OIDC 25/25 tests (one suite, 1s). G1/G4/G5 and the global-fanout guard PASS.
  Conditional three-DC/topology/load/helper SKIPs retain their declared scope.
  `e101d-go-all.exit` and `e101d-go-all.outer.exit` both record actual exit 0.
  Its unchanged command is read from Compose and executed in a shell envelope that writes
  the actual exit atomically to external `e101d-go-all.exit` before the container is removed.

This adds only the terminal-before-aggregate-cleanup K proof. It does not cover deaths
between the three concrete store deletes, root-only partial cleanup, C0/C3, P2 publication,
three-DC/outage behavior or global scheduling/seek persistence/fairness. Root enumeration
and checkpoints retain the inherited fixture-local scope. No product violation reproduced.
E1-01 and X1 remain OPEN; this evidence authorizes no ordinary or production GC activation.

Final audit: no blockers found in this scoped diff. Only integration tests, their required
runner gates and evidence/index/history documents changed. A/B/C meanings and outcomes remain
intact. Source hashes match the final Docker images; exact day/bucket/first_seen/org/L/P/D
coordinates are retained for post-root-removal checks. Failure controls compare full snapshots
and fresh-session live state before disposal. Relative links, Compose configuration and
`git diff --check` PASS. `main@b4a2eaf3c` remained current at final verification.
