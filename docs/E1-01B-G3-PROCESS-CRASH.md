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

Validated source: `18d86c36e420d538e02bff33cb6fa9b571fb1bc1`.
Test hashes (SHA-256):
- Shared E1-01A/B harness: `a3a7d2c184bf773a80ef838c77b78b90db9e5943a49d1514326d45369478d633`.
- G3-specific test: `28c31a498397e21863cddaba217a3533fd622018a08091904f61626e71932daa`.

Linux Docker, Cassandra 5.0.9, `datacenter1`/RF1, real `LOCAL_QUORUM`/`SERIAL` configuration
and EACH_QUORUM reads where the contract requires them; SILO `RELEASE.2026-09-16T00-00-00Z`.
The four APIs, Cassandra and SILO are healthy; isolated e19 explicitly reports background GC OFF.
Runner `go-integration-test`: `sha256:8e8e1d110f19a5e164788d7aba67c7310a9ba4552c977ca9052d662a7c6f4795`.
Runner `go-all-test`: `sha256:7e0baad0569a06c049cc9aa5ab3717ef1babd6a570c6ed67314681a3231a37c6`.
E19 backend: `sha256:a53db0b97cb82304f58973aae8d848ad67fa0789489c891b760df4f83223be4e`.
Running/test-source identities were checked before final-image validation.

| Cut | Action | Queue replay before physical recovery | Physical recovery + independent replay |
| --- | --- | --- | --- |
| C4 before-retirement | SIGKILL | Retires P1, settles owned candidate/queue; exact D/root/lifecycle and K1 intact | Exact TERMINAL, K1 absent; PASS |
| C4 before-retirement | Resume | Real worker retires/settles; new-process empty replay harmless | Exact TERMINAL, K1 absent; PASS |
| C5 after-retirement | SIGKILL | Canonical-row-missing replay settles retained work; exact D/root/lifecycle and K1 intact | Exact TERMINAL, K1 absent; PASS |
| C5 after-retirement | Resume | Real worker settles; new-process empty replay harmless | Exact TERMINAL, K1 absent; PASS |

Parent verifies SERIAL canonical state and exact published lifecycle, COMMITTED orphan/root,
original candidate/queue, K1 bytes, HEAD and refs before killing. Both queue replays use fresh
processes/sessions; parent compares full orphan/lifecycle rows, root and bytes with the pre-kill
snapshot. Physical recovery/replay uses separate processes and requires completion markers.
Failed/skipped legs retain authority, queue state and the snapshot through all inherited cleanups.
C5's negative proves the retired canonical row does not permit teardown to erase the continuation.

Local ignored `tmp/` evidence:
- `e101b-directed-ready.log`: A+B 8/8 legs, both failure-preservation controls and gate PASS.
- `e101b-race.log`: `-race -count=2`, A+B 16/16 legs, both failure controls twice PASS (173.518s).
- `e101b-unit-vet-image.log`: complete short Go tree and normal/integration vet PASS.
- `e101b-omission.log`: G3-only filtered C4/kill rejects the other three required named legs.
- `e101b-unavailable.log`: G3 gate alone rejects unavailable backend instead of zero-test PASS.
- `e101b-s3-negative.log`: ephemeral container source copy faults only the recovery child's S3
  endpoint. Actual SKIP/exit 0 is rejected; C5 D/root/lifecycle and K1 remain, snapshot retained.
  `e101b-s3-negative-disposal.log` verifies live state in a fresh session before explicit owned
  disposal; the external JSON artifact remains byte-identical. This is harness/availability evidence.
- `e101b-go-all.log`: standard required `go-all-test` exit 0; full short Go units,
  complete integration (888.498s, A+B and both failure controls included), API 20/20 suites
  and OIDC 25/25 tests PASS. G1/G4/G5 and the global-fanout guard PASS; conditional topology,
  load-profile and parent-only helper SKIPs retain their intended scope.
  Reproduce with `docker compose --profile test run --rm go-all-test` on the configured fleet.

Initial Docker services were absent and the gate failed; that attempt is not counted as crash
proof. The bind-based short-tree scan was stopped during OneDrive/node_modules discovery and
replaced by the complete immutable-image check. Neither is a product RED.

Audit scope: only C4/C5 plus small reuse of the already-tested A harness; no productive hooks,
fabricated lifecycle state, worker/schema/writer/scheduler/GC activation changes or extra funnels.
G4 coexistence remains an unchanged regression. Queue discovery is filtered by owned org/L/P
with a fail-closed read bound; root enumeration/checkpoints retain the declared test-local scope.
No product violation was reproduced in these schedules. This does not prove global scheduling,
outages, three-DC behavior or the other E1-01 cuts. E1-01 and X1 remain OPEN.
