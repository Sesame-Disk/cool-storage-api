# E1-01C — physical recovery process-crash evidence

Frozen plan: 2026-10-13, base `main@acc6c6f45efdfb0cca7826c943beeb6a7229c316` (#283).
Only C6/C7 of [E1-01](./E1-01-CRASH-EVIDENCE-PLAN.md); E1-01/X1 OPEN, production GC OFF.
Missing process-death evidence is not a demonstrated runtime defect.

The current recovery executes exact retirement -> S3 DELETE -> phase persistence ->
TERMINAL -> orphan/discovery/root removal. Existing G4 post-delete failure tests use mocks;
E1-01A/B kill the work-processing actor before physical recovery, not this recovery actor.

- C6 `after-delete`: pause before the real MarkS3OrphanMappingCleanupPending call, after
  productive DELETE returned success. Parent verifies K1 absent, canonical retired, exact
  COMMITTED orphan in pending_s3, published lifecycle and durable root.
- C7 `after-phase`: pause after that real phase write succeeds, before TERMINAL. Parent
  verifies pending_mapping_cleanup, the same exact D/root/published lifecycle, K1 absent.
- Real fixture upload and ProcessOrgOnce create and settle the committed handoff. A distinct
  recovery process uses owned root enumeration and production RecoverS3Orphans. No CQL state
  fabrication, productive hook, new storage interface or fake successful outcome.
- Each cut has SIGKILL and same-boundary resume controls; parent verifies the durable cut
  before death. Independent fresh-process recovery and another replay must reach TERMINAL,
  remove exact orphan/root, retain canonical/K1 absence, unchanged HEAD and zero refs/repairs/work.
- Reuse the existing scoped subprocess, snapshots, teardown guards and four-leg gate. A failed
  C7 leg must preserve the post-delete continuation through all cleanups; prove live state
  from a fresh session before explicit disposal. Do not require retained bytes/queue after deletion.
- Required gate in both Docker runners rejects omitted legs, unavailable backend and recovery
  child SKIP. Shared fixtures run sequentially. Freeze source/image identities and exact topology.

Budget: one small integration file, minimal shared helper/gate changes, Docker gate and this ledger;
roughly five files and 350 added lines. C8 cleanup substeps, C0/C3, P2 publication and 3-DC residuals
remain separate. No runtime/schema/writer/scheduler/default/activation changes.

Validation: directed A+B+C and preservation controls; race repeat; omission/unavailable/SKIP
negatives; normal/integration vet and complete short Go tree; standard Docker integration/API/OIDC
with G1–G5 controls. Audit authority, cut order, successful-call delegation, independent sessions,
required markers, preservation and exact cleanup before PR. A reproduced product defect gets its
own small fix PR, with the unmodified-source failing schedule retained first.

Evidence: pending implementation and Docker verification; no new PASS or closure claimed.
