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

## Result and disposition recommendation

Final test source: `99c7a4f1a`; frozen plan: `c20b6b4ba`.
`TestE1DelayedPutWithProtectedSuccessor` extends the existing fixture; no
productive Go, schema, API, dependency or GC-default changes were needed.

The original Sync request is held after exact P1/K1 repair authorization and
before its real PUT. `ProcessOrgOnce` commits D1 and retires P1. A second real
Sync PutBlock installs K2 through the normal rowless writer and registers its
upload reference before physical recovery. Actual PUBLISHED D1, COMMITTED
orphan and durable root are read, not synthesized. K1 starts absent to trigger
the legitimate repair path; this drill proves the subsequent real DELETE
continuation/terminal certificate, not removal of initially present K1 bytes.
The separate G4 coexistence control covers initially present K1/K2 objects.

After recovery, releasing the old PUT leaves K1 residue and returns 200 in the
measured runs. It neither reinstalls P1 nor changes P2's location, creation/SHA-1
metadata, size, bytes or reference set. Without another PutBlock, actual
PutCommit, RecvFS and UpdateBranch handlers publish HEAD. Cassandra reads verify
HEAD -> commit -> root dirent -> file -> logical block, its mapping and canonical
P2, with permanent `fs:` protection. Actual GetBlock returns the expected bytes.
These are in-process production HTTP handlers with real Cassandra/S3; permission
context is supplied by the fixture. Authentication middleware is not certified.

Two fresh workers receive a previously read D1 discovery root. Canonical/root
reads remain real. Each refuses settlement with the exact missing durable-root
token error, recovers zero items and preserves TERMINAL D1, absent orphan/root,
HEAD, P2 metadata/bytes/refs, canonical download and K1 residue. Fresh durable
discovery returns zero without error. After both replays, explicit removal of
K1 still leaves the published canonical download readable with P2 intact, proving
no physical dependency on the residue. **The stale-discovery replay is a safe
refusal, not a successful settlement or a new convergence guarantee.**

Recommend resolving D0 H's named combined one-DC healthy-service evidence gap;
the observed late K1 bytes are **X3/leak**, not a retired-P1 HEAD dependency.
This is a scoped recommendation for final maintainer acceptance. It does not
close X1, I1/M1, PRE-GC/A1 or authorize ordinary destructive GC.

## Validation and audit

Docker project `sesamefs-h1`, new owned volumes/keyspace `sesamefs_e19`, one DC
`datacenter1` RF=1; Cassandra 5.0.9 and SILO
`RELEASE.2026-09-16T00-00-00Z`. Ordinary API GC status explicitly reports OFF.
Go 1.25.14 compiles current handlers/worker into the integration process.
Standard Compose forwards H1 to `SESAMEFS_E19_ISOLATED_URL`; the same test binary
runs the owned schedule in a child with all three URLs/keyspace aligned. The
wrapper requires successful child exit and explicit schedule/cleanup evidence.
Direct runs set `SESAMEFS_E1_H1_CHILD=1` after selecting the isolated environment.
The provisioning API reuses `sesamefs-e101i-api` (binary SHA-256
`ee229e9114fe9a2d46dda0492fefdc8ba5719259727cefe61d2caed16ef39870`);
non-test `cmd`/`internal` sources are unchanged from its source `5302ab08d`.

- Direct `go test -tags integration -json -count=2 -timeout 8m -run
  '^(TestE1DelayedPutCannotRestoreRetiredPhysicalLife|TestE1DelayedPutWithProtectedSuccessor|TestNoGlobalGCFanoutInIntegrationSuite|TestG4CassandraMinIOPhysicalLifeCoexistence)$'
  ./internal/integration`: PASS, 12 named test/subtest events, zero skips.
- Same direct selection excluding the separate G4 control, with `-race -count=2`:
  PASS, 10 named events, zero skips/races.
- Standard isolated-wrapper selection of `TestE1DelayedPutWithProtectedSuccessor`
  with `-race -count=1`: PASS, one parent case plus its child's actual schedule,
  no-GC control and cleanup. The child inherits the race-instrumented binary.
- An unreachable isolated endpoint makes the wrapper FAIL even though the child
  exits zero without running tests. This directed negative verifies that an
  omitted/skipped child cannot masquerade as evidence. JSON validation requires
  exact direct names/counts, package success, ordered H1 witness, publication and
  post-cleanup evidence; exit 0 alone is insufficient.
- Two test-only mutations on an archived `8aff621b8` source copy compile and fail at the
  expected runtime oracle: recovery before P2 rejects non-PUBLISHED D1; deleting
  K2 after recovery rejects damaged bytes. Neither is a reproduced product bug.
- Owned K1/K2 objects, canonical row, all block refs, exact orphan/root and
  lifecycle are independently absent after teardown. Existing upload cleanup
  additionally verifies expiry/discovery and mapping cleanup. Standard tenant/
  library soft deletion retains its normal metadata/history; no whole-keyspace
  or historical-fixture purge is claimed.

Initial checks were not passing: a value/pointer mismatch and an unused import
prevented compilation; an API-key HTTP attempt received 401 because SeafHTTP
requires its own authentication; the first replay assertion incorrectly expected
nil instead of the real missing-token refusal. Tests now use the same direct
handler boundary as the upload fixture and require that exact refusal. No
production fix is inferred from these fixture corrections.

Integration sources compile with normal Go vet enabled. No full Go, full
integration, three-DC, native fault, API/OIDC or desktop/mobile matrix was rerun.
Existing three-API availability failures remain FAIL. Runtime compatibility is
unchanged. Ignored local evidence: `tmp/h1-*`. The owned stack is stopped after
validation; volumes retained. Backward compatible: yes, evidence only.
