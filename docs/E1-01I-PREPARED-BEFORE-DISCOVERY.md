# E1-01I — PREPARED before discovery process crash

## Frozen plan (2026-10-16)

Base: main@a0a767591 (#306). Advances D0 §25 G1/G2 and E1-01's explicitly
unwitnessed PREPARED-without-projection process window. No runtime defect is
presumed and no X1 closure or activation is authorized.

1. Reuse the existing Linux Docker SIGKILL/resume harness, real Cassandra/S3,
   query observer and org-scoped worker. Hold only the successful exact orphan
   INSERT inside Prepare, before its discovery publication. No fabricated CQL
   setup, new productive hooks or global worker fan-out.
2. Parent independently proves exact uncommitted blocks claim, matching root and
   PREPARED/pending_s3 orphan, absent by-day/lifecycle, intact K1/canonical/HEAD,
   zero refs/repairs and retained owned queue/candidate. Both kill and resume
   legs must reach that same state.
3. Kill must be OS SIGKILL. Fresh-process root recovery must actually visit the
   identity, republish discovery and abort only the uncommitted claim; verify
   original physical bytes/HEAD survive and orphan/root settle. Fresh replay
   must be harmless. The resume control completes normal committed deletion.
4. Preserve failed-fixture snapshots and live state before explicit disposal.
   Require both legs with a fail-closed evidence gate. Check missing-leg,
   missing-test and unavailable-service negatives; use a directed omission of
   the actual projection-repair call to prove that recovery observation matters.
5. Run named tests in Docker, race in parent and child, repeated/reversed legs,
   existing A/H compatibility and relevant guard/vet checks. Audit exact counts,
   cleanup, links and source diff; refresh main before publishing a focused PR.

Scope: one process cut with two controls, existing harness wiring and bounded
tracking. No retry/timeout/schema/API/GC-default change. One-DC evidence is not
three-DC, global scheduling, partition or full-suite certification. A reproduced
runtime defect requires its own RED/GREEN correction; report failures first.
Backward compatible: yes, test/evidence only unless a separately proven fix is needed.