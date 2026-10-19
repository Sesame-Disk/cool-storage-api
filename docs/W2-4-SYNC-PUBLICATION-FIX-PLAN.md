# W2-4 Sync publication authority fix

Base: `main@3f0b787a2` (characterization PR #244 merged).

Scope: close the demonstrated pre-HEAD no-PutBlock publication gap. W2-5,
R31, G4/G5, PC-D1B, and GC activation remain separate work.

## Implementation

1. Convert the ten Cassandra/MinIO characterization legs into safety
   regressions. Require unchanged HEAD and no permanent file reference when
   GC wins; preserve successful writer-first and repair-first controls.
   Assert that successful auto-merge preserves both local and remote entries.
2. Reproduce RED on the unchanged runtime before editing production code.
3. Capture exact placements for every distinct canonical block in the
   added-file delta. Keep Sync `up:` renewal restricted to the observed
   PutBlock-provenanced subset; never fabricate an upload pin for dedup.
4. Reuse the existing durable per-file repair and final exact-placement
   validation in both direct and auto-merge paths. Carry the captured
   placements across repair acquisition without re-scoping provenance.
   Preserve shared direct-HEAD repair retention on uncertain outcomes and
   unique auto-merge cleanup.
5. Require W2-4 named-leg evidence in the ordinary integration runners.
   Make the standalone Docker validation require GREEN safety evidence and
   verify that filtered coverage/unavailable infrastructure cannot pass.

## Safety argument

If GC's settled claim wins before repair acquisition, the final exact-P
read rejects the deleting/changed/missing placement. If durable repair is
visible first, GC's EACH_QUORUM liveness probe cannot establish zero. Repair
survives temporary-reference expiry and remains until permanent references
are established or the existing ownership rules allow cleanup. No new
schema, pin type, coordinator, GC behavior, or protocol field is needed.

## Validation and audit

Run tests in Docker: targeted RED/GREEN, full Go unit and integration suites,
API/OIDC, frontend, mobile, and desktop Sync including active-active where
the fixture supports it. Audit production ordering, mixed provenanced and
unprovenanced commits, exact-placement changes, error cleanup, and evidence
gates. Record any failures and infrastructure limits explicitly. Preserve
the historical characterization and update the live checklist only to the
extent established by the new evidence; this PR cannot declare X1 closed.

### Executed evidence (2026-10-01)

All application tests below ran in Docker. Host commands only orchestrated
containers and inspected source/logs.

| Check | Result |
|---|---|
| New safety assertions with unchanged #244 runtime | RED: six GC-first legs reported D(P)+HEAD; four controls passed; new unit scope/order assertions failed |
| Fixed W2-4 direct/auto-merge matrix | 10/10 GREEN |
| Fresh-stack standalone validation | GREEN; filtered named-leg coverage and unavailable backend both fail closed |
| Mutation: resolve only provenanced placements | Semantic D(P)+HEAD RED |
| Mutation: discard captured placements before final validation | Semantic D(P)+HEAD RED |
| `go test ./... -count=1 -cover -timeout15m` | PASS, 21 packages |
| Targeted API readiness/repair tests with race detector | PASS |
| Full `go test -tags integration -v -count=1 -timeout20m ./internal/integration/...` | PASS; 449 top-level passes, 84 top-level skips, including specialized fixtures |
| Frontend lint + Jest | PASS; 34 suites, 471 tests |
| Real Seafile CLI encrypted/unencrypted bidirectional Sync | PASS, 11/11 |
| Real two-client/two-node active-active Sync | PASS: non-overlapping edits converge via observed auto-merge; same-path conflict exhausts 503 retries and preserves both local edits |
| API shell suite | 20 categories passed; GC category initially failed because controlled W2-4 fixture disables GC; separate enabled-GC admin rerun passed 23/23 |
| Extra enabled-GC Go tests on the populated audit database | 44 passed, 2 skipped, 2 failed: deleted-library scan saw 2 eligible rows rather than 1; grace-period test timed out waiting for worker progress |
| OIDC shell suite | Incomplete: disabled fixture first; enabled local issuer then rejected by the existing private-IP SSRF guard. No production security override added |
| Mobile typecheck/lint/Vitest/smoke | Not green: two missing `Dirent.locked_by_me` type errors; lint has 62 warnings/0 errors; Vitest has 9 incorrectly discovered Playwright suites and 1 timer failure (348 tests pass); smoke 2 pass/8 fail because password-login selectors are absent |

Mobile/frontend/desktop source is identical to the base. Mobile failures are
outside this runtime change. The two additional GC failures have fixture
interference/worker-progress symptoms; they are recorded as unresolved, not
claimed to be fixed or proven unrelated by a baseline comparison. Specialized
three-DC, process/admission and opt-in physical-GC matrices were not executed.
This is not an all-repository-green claim.

### Final source audit

- All distinct added canonical blocks are resolved once per readiness call;
  the existing concurrency bound remains 20. Only the provenanced subset is
  passed to upload-pin renewal. Empty deltas perform no placement query.
- Both publication paths retain the original exact placement across repair
  acquisition and validate it before HEAD; changed/blocked/unknown authority
  fails closed. Capturing a replacement after acquisition is not permitted.
- Rejection before repair releases only request-local publication staging.
  Direct rejection after queue retains shared repair; unique auto-merge repair
  is cleaned up by its existing owner. Successful controls settle permanent
  references and repair as before.
- Retirement assertions inspect the minted physical S3 key, rather than a
  legacy deterministic hash key. GC claim acquisition precedes its real
  EACH_QUORUM zero probe, matching the worker's ordering.
- Integration evidence uses real HTTP upload/dedup/metadata receipt and the
  production UpdateBranch handler, with real Cassandra/MinIO authority
  primitives. TTL expiry is modeled by deleting only temporary fixture refs;
  it does not prove wall-clock expiry, a complete scheduled worker race, or
  post-HEAD recovery. R31, W2-3/W2-5, G4/G5 and X1 remain separate.

Runner limitations were worked around only in temporary audit infrastructure:
the shell runner's missing Dockerfile.test, archived Debian package URLs in
the Seafile image, and non-executable host wait helper. The temporary CLI image
uses Debian bookworm, Python 3 and the same Seafile AppImage version. These
workarounds do not modify committed product or desktop infrastructure.
(2026-10-19: the shell runner's Dockerfile was never committed because the
`.gitignore` rule `*.test` hid it; `api-test`, `oidc-test` and the multi-region
`test-runner` now build from `Dockerfile.gotest`, PR #317.)
