# H1 — Protected successor with an authorized late PUT

## Frozen plan (2026-10-16)

Base: main@a51800595884e99edcb74c702504b688d640df81 (#308).
Scope: D0 section 21 H and the H1 action in [the final audit](./E1-FINAL-DISPOSITION.md#remaining-x1-work).

1. Keep the existing delayed-PUT/no-GC controls. Add a named variant using the
   actual Sync PutBlock handler, Cassandra and S3. Hold only the authorized
   K1 PUT; allow a legitimate competing Sync writer to install/protect P2/K2.
2. Drive the real organization-scoped GC worker through D1 commit/canonical
   retirement, then install P2 before the separate real physical recovery deletes
   K1. Prove exact committed D1 and protected P2 coexist before recovery.
3. Release the original PUT after deletion, then send its metadata and actual
   HEAD continuation. Require successful publication on P2, exact K2 bytes and
   metadata, persistent fs protection, and no reinstallation/dependency on P1.
4. Replay a captured stale D1 discovery through a fresh worker with real
   authoritative reads. Check terminal D1, unchanged P2/HEAD/references and K1
   residue. Verify cleanup of owned physical and database artifacts.
5. Run the named controls and new schedule in Docker, repeat under race, and
   test the oracle with a directed invalid-order/identity negative where useful.
   Do not claim RED/GREEN runtime repair unless a real defect is reproduced.
6. Audit against refreshed main, update concise tracking, commit only this
   bounded slice and create a reviewable PR with measured results and skips.

No runtime/schema/API/default change is presumed. One-DC healthy-service evidence
is not a multi-DC/fault certificate. I1/M1, final E1 acceptance and PRE-GC remain
separate. The maintainer owns gate closure; ordinary GC remains disabled.
Backward compatible: yes, integration evidence only.
