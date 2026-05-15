# Pending Issues Audit - 2026-05-14

Scope:
- Root pending files: `sesamefs-pending-issues.txt`, `quotas-pending-issues.txt`
- Supporting docs: primarily `docs/KNOWN_ISSUES.md`, `docs/TECHNICAL-DEBT.md`, `docs/V1-PRODUCTION-ROADMAP.md`, `docs/ACCOUNTS-DASHBOARD-INTEGRATION.md`
- Validation method: static code verification against `internal/`, `frontend/src/`, `configs/`, `scripts/`, and route registration.

Legend:
- Severity: P0 = launch/tenant/data blocker, P1 = production essential or serious product bug, P2 = important backlog, P3 = polish/debt.
- Effort: S = small, M = medium, L = large, XL = multi-area/project.

## Confirmed And Ordered

| Rank | Issue | Severity | Effort | Status |
|---:|---|---|---|---|
| 1 | Per-user storage quota enforcement | P0 | M | Fixed 2026-05-14 |
| 1b | Quota enforcement coverage gaps in V2 mutations (copy / revert / restore-from-trash / OnlyOffice save / cross-repo batch) | P1 | M | Fixed 2026-05-14; retention/move debt remains documented |
| 2 | Backup / disaster recovery is not production-ready | P0 | L | Confirmed |
| 3 | Persistent audit trail / activity feed / Accounts audit provenance is missing | P1 | L | Confirmed |
| 4 | Batch file move can report success without moving files | P1 | M | Confirmed, broader than docs |
| 5 | GC queue exact recounts still use Cassandra `COUNT(*)` over queue partitions | P1 | L | Confirmed |
| 6 | Public/upload-link resume semantics remain fragile | P1 | M/L | Confirmed |
| 7 | Upload "do not replace" is still ambiguous for desktop/client endpoint semantics | P1 | M | Partially confirmed |
| 8 | File-operation statistics have no real event dataset | P1 | L | Confirmed |
| 9 | Antivirus / malware scanning does not exist | P1 | L | Confirmed |
| 10 | Storage class / multi-region lifecycle is incomplete for existing data | P1 | L/XL | Partially confirmed |
| 11 | Default repo / first-login library is still a stub | P2 | S/M | Confirmed |
| 12 | Activities, notifications, devices, shared folders, wikis remain compatibility stubs | P2 | M/XL | Confirmed |
| 13 | Cold storage restore endpoint is a placeholder | P2 | M/L | Confirmed |
| 14 | Text diff between file versions is not implemented | P2 | M | Confirmed |
| 15 | Org-admin statistics can resolve platform aggregate when the org shell is platform-scoped | P2 | S/M | Confirmed but severity should be downgraded |
| 16 | Traffic recorder drops saturated events silently | P2 | S/M | Confirmed |
| 17 | Devices/license metadata in sysadmin dashboards is still unavailable/stubbed | P3 | M | Confirmed |
| 18 | CI / frontend E2E / coverage maturity is still incomplete | P3 | M/L | Confirmed |

## Details

### 1. Per-user storage quota enforcement

Fixed after this audit. `CheckStorageQuota` now accepts `orgID`, `userID`, and `additionalBytes`, reads both `organizations.storage_quota` and `users.quota_bytes`, and compares the matching org/user `storage_counters` scopes.

Evidence:
- `internal/traffic/checker.go:43` defines `CheckStorageQuota(orgID, userID string, additionalBytes int64)`.
- `internal/traffic/checker.go:58-78` reads org quota/counter plus user quota/counter when `userID` is present.
- Upload callers pass `userID` and now check the visible storage delta where needed: web/direct uploads cover chunked totals and replace deltas, block uploads skip storage checks for deduplicated blocks, and sync HEAD publishing validates the final tree delta.
- Per-user storage counters are present elsewhere (`traffic.ReadStorageUsed(... "user:<org>:<user>")`) and are now used by the upload storage pre-check.

Sync commit publishing now also checks the committed tree delta before advancing HEAD and waits for the matching storage-counter delta before the request returns.

Residual debt remains: the publish/counter flow is still split-phase under concurrency, so this audit now treats that as technical debt rather than as an open coverage gap on upload/sync paths.

### 1b. Quota enforcement coverage gaps in V2 mutations

Fixed 2026-05-14. The handler-by-handler approach was applied: a small shared module `internal/api/v2/quota_helpers.go` exposes `fsEntryStats`, `fsEntryDelta`, `preCheckStorageQuotaForDelta`, and `applyStorageCounterDelta`, and the following handlers were wired in: `CopyFile`, `copyBatchFiles`, `RevertFile`, `RevertDirectory`, `RestoreTrashItem`, `RevertDirents`, `OnlyOffice saveEditedDocument`, and `batch_operations.processSingleItem` (covers cross-repo `AsyncBatchCopy` / `AsyncBatchMove`).

Move cross-repo also decrements the source library counter so per-library views stay consistent. OnlyOffice does the pre-check *before* the S3 `PutBlockData` so a quota-exceeded save never persists bytes.

Three new integration tests cover the regressions (`TestCopyFileEnforcesPerUserStorageQuota`, `TestRestoreTrashItemEnforcesPerUserStorageQuota`, `TestRevertFileEnforcesPerUserStorageQuota`); the full `docker compose run --rm go-integration-test` suite is green.

Residual contract note:
- This fix closes the missing quota wiring only. It does not change deleted-item retention semantics.
- Deleted file/folder restore inside a live library remains bounded by the library's historical commit retention (`version_ttl_days`), plus the normal `gc_queue` grace once a commit is enqueued.
- Deleted-library restore remains bounded by `trash_retention_days`.
- Cross-repo move still publishes destination before removing source. Rare partial-success behavior is accepted for now as technical debt and is a better future target for reconciliation or compensating jobs than for a rushed handler-level patch.

Original confirmed scope of the bug (kept for context):

Affected paths:

- `CopyFile` / `copyBatchFiles` / batch copy
  - `internal/api/v2/files.go:2326`, `internal/api/v2/files.go:2917`, `internal/api/v2/batch_operations.go:494`
  - Adds bytes at destination, no pre-check, no counter increment.
- `MoveFile` inter-repo
  - `internal/api/v2/files.go:1965`
  - Per-library counter drifts; org/user totals stay correct only when both sides share an owner.
- `RevertFile` / `RevertDirectory`
  - `internal/api/v2/files.go:3553`, `internal/api/v2/files.go:3698`
  - Reverting can restore a larger version. No pre-check, no counter delta.
- `RestoreTrashItem` / `RevertDirents`
  - `internal/api/v2/trash.go:329`, `internal/api/v2/trash.go:656`
  - Counters *are* incremented at `trash.go:452` and `trash.go:787`, but there is no pre-check; a tenant whose cap dropped while items were in trash can be pushed over silently.
- OnlyOffice `saveEditedDocument`
  - `internal/api/v2/onlyoffice.go:1014`
  - Document edit publishes a new commit whose size can grow or shrink. No pre-check, no counter adjustment. Counters drift on every save.

Why it slipped: `FSHelper.UpdateLibraryHead` is the convergence point for all V2 FS mutations but only recomputes `libraries.size_bytes` / `file_count`; the counter side was wired at handler level for uploads only. The handlers above were never wired in.

Template for the fix already exists in sync (`internal/api/sync.go:2133` for the pre-check, `internal/traffic/storage.go:136` for the delta-apply with negative clamp). Open question is whether to centralize the delta logic inside `FSHelper.UpdateLibraryHead` or wire it explicitly in each handler.

Tracked in `docs/KNOWN_ISSUES.md` → ISSUE-QUOTA-COVERAGE-01 and `quotas-pending-issues.txt` item 0.

### 2. Backup / disaster recovery is not production-ready

Confirmed. The roadmap says no backup scripts/runbook/restore drill exist, and repository search found no actual `scripts/backup-cassandra.sh`, `scripts/backup-s3.sh`, or `docs/DISASTER-RECOVERY.md`.

Evidence:
- `docs/V1-PRODUCTION-ROADMAP.md:440-477` documents missing backup, restore runbook, RTO/RPO, and restore drill.
- `rg --files | rg -i "backup|restore|disaster|dr|runbook"` only finds restore-job code/docs, not operational backup tooling.

Recommendation: implement Cassandra snapshot backup, object-store replication/sync backup, config backup, restore runbook, and one real restore drill.

### 3. Persistent audit trail / activity feed / Accounts audit provenance is missing

Confirmed. Audit logging currently logs to process logs only, and the user-facing activities endpoint returns an empty stub. The Accounts integration docs also list explicit audit tagging for service credentials as future work.

Evidence:
- `internal/middleware/audit.go:83-88` logs to console and has a TODO for an `audit_logs` table.
- `internal/api/server.go:1981-1987` returns `{"events":[]}` for activities.
- `docs/ACCOUNTS-DASHBOARD-INTEGRATION.md:1147-1152` lists Accounts service-credential audit tagging and membership API follow-ups.

Recommendation: define immutable audit tables, write audit events from admin/provisioning/security-sensitive handlers, include `source`/actor/service credential metadata, and back activities/admin logs from that dataset.

### 4. Batch file move can report success without moving files

Confirmed, and more severe than the root text suggests. Cross-repo batch move correctly returns 501, but same-repo batch move just appends names to `movedFiles` and returns success without updating the FS tree.

Evidence:
- `internal/api/v2/files.go:2283-2289` rejects cross-repo batch move.
- `internal/api/v2/files.go:2291-2305` does not perform the move; it returns success after building a synthetic moved list.

Recommendation: either implement real batch move or return 501 for all batch moves until it is real. Returning false success is the dangerous part.

### 5. GC queue exact recounts still use Cassandra `COUNT(*)`

Confirmed. GC now has leadership, but queue recount debt remains. The code still exact-counts every queue bucket in hot/status/reconcile paths.

Evidence:
- `internal/gc/gc.go:739` calls `RecountOrgQueueDepth`.
- `internal/gc/store_cassandra.go:456-462` and `internal/gc/store_cassandra.go:786-790` execute `SELECT COUNT(*) FROM gc_queue WHERE org_id = ? AND bucket = ?`.

Recommendation: move queue-depth snapshots to queue write paths and keep exact scans only as infrequent repair/scrub tooling.

### 6. Public/upload-link resume semantics remain fragile

Confirmed from docs and current token model. Upload/download tokens are TTL-based and there is no obvious signed offset probe for public upload-link resumability. This is not a raw throughput bug; it is retry/resume fragility.

Evidence:
- `internal/db/tokens.go:32-37` describes Cassandra TTL token storage.
- `docs/TECHNICAL-DEBT.md` section 15 documents missing public/share upload resume and ambiguous token contract.

Recommendation: define token lifecycle semantics, add safe resume/offset checks for public upload flows, and add abandoned upload cleanup tests.

### 7. Upload "do not replace" remains ambiguous for desktop/client endpoint semantics

Partially confirmed. Web UI now explicitly sends `replace=0` for "do not replace", so the docs are stale for the web path. Desktop/client compatibility is still ambiguous because both `upload-link` and `update-link` route to the same `GetUploadLink`, token structs have no `Replace` field, and SeafHTTP defaults to replace when the form omits/overrides it.

Evidence:
- `internal/api/v2/file_routes.go:40-41` maps both `/upload-link` and `/update-link` to `GetUploadLink`.
- `internal/api/seafhttp.go:50-60` and `internal/db/tokens.go:21-30` have no token-level replace flag.
- `internal/api/seafhttp.go:1021-1022` defaults `replace` to `"1"`.
- `frontend/src/components/file-uploader/file-uploader.js:733-738` sends `replace=0` for web "do not replace", so the web-specific claim is no longer accurate.

Recommendation: add token-level replace semantics or separate upload/update token creation for clients that communicate intent via endpoint rather than form field.

### 8. File-operation statistics have no real event dataset

Confirmed. Sysadmin file statistics return zero-filled rows, and org-admin file statistics are explicitly not implemented.

Evidence:
- `internal/api/v2/admin_extra_stats.go:301-319` returns zero values for `added`, `deleted`, `modified`, and `visited`.
- `internal/api/v2/org_admin_stats.go:17-19` returns `notImplemented` for org file stats.

Recommendation: add immutable file update/access events and aggregate both sysadmin and org-admin statistics from them.

### 9. Antivirus / malware scanning does not exist

Confirmed. The roadmap says this is unimplemented, `enable_file_scan` is false in bootstrap, and code search found no ClamAV/malware scan integration.

Evidence:
- `docs/V1-PRODUCTION-ROADMAP.md:274-315` describes the desired design.
- `internal/api/bootstrap.go:665` exposes `enable_file_scan: false`.
- Code search for virus/clamav/malware has no implementation.

Recommendation: treat as product/security backlog unless V1 must support malware scanning before launch.

### 10. Storage class / multi-region lifecycle is incomplete for existing data

Partially confirmed. Create-time storage policy is much more implemented than old roadmap text says, including strict/flexible org policy and region class resolution. The remaining real gap is migration/lifecycle for existing libraries and policy after create.

Evidence:
- Implemented: `internal/api/v2/storage_policy.go` resolves create-time org storage policy.
- Implemented: `internal/storage/storage.go:256-275` resolves library override/endpoint/default class.
- Still stubbed: `internal/storage/storage.go:432-440` `SelectBackend` returns default.
- Still missing migration: `internal/api/v2/libraries.go:1157` TODO after changing library storage class.
- Docs confirm: `docs/DEPLOY.md:1543` notes existing-library migration is manual.

Recommendation: separate this into two tracks: create-time residency is largely done; existing-library migration and retroactive policy enforcement remain large work.

### 11. Default repo / first-login library is still a stub

Confirmed. Both GET and POST `/api2/default-repo/` route to a handler that always returns no default repo.

Evidence:
- `internal/api/server_routes.go:169-172` registers GET and POST default repo.
- `internal/api/server.go:1118-1124` returns `{"exists": false, "repo_id": ""}`.

Recommendation: create a default personal library on first login or on POST `/api2/default-repo/`, depending on desired product behavior.

### 12. Compatibility stubs remain

Confirmed. These are not all launch blockers, but they are real product gaps.

Evidence:
- Activities: `internal/api/server.go:1981-1987`.
- Notifications: `internal/api/server.go:1989-1995`.
- Shared folders: `internal/api/server.go:2004-2008`.
- Devices: `internal/api/server.go:2010-2014` and org-admin device stubs in `internal/api/v2/org_admin_stats.go:123-166`.
- Wikis: `internal/api/server.go:2016-2020`.

Recommendation: keep them documented as compatibility stubs, not hidden bugs. Promote only the ones required by target clients/users.

### 13. Cold storage restore endpoint is a placeholder

Confirmed. It creates a restore job with empty `BlockIDs` and TODO comments instead of checking object state or initiating restore.

Evidence:
- `internal/api/v2/restore.go:54-64`.

Recommendation: implement only if cold/Glacier class will be enabled in production; otherwise hide/disable the endpoint.

### 14. Text diff between file versions is not implemented

Confirmed. Frontend legacy code has a `text_diff` URL, but the action is commented out and there is no backend route/handler for text diff.

Evidence:
- `frontend/src/pages/file-history-old/history-item.js:51` builds a diff URL.
- `frontend/src/pages/file-history-old/history-item.js:129` comments out the Diff action.
- Code search found no backend `text_diff` route.

Recommendation: implement backend diff endpoint plus UI only if text/code file history is in scope.

### 15. Org-admin statistics can resolve platform aggregate when platform-scoped

Confirmed, but severity should be lower than older docs imply. The platform aggregate is keyed by the nil UUID, and the org-admin frontend uses `window.org.pageOptions.orgID`. If a platform superadmin opens the org-admin shell with platform org context, calls go to `/api/v2.1/org/00000000-0000-0000-0000-000000000000/admin/statistics/...`, which is interpreted by stats helpers as platform aggregate.

Evidence:
- Platform org id: `internal/middleware/permissions.go:41-43`.
- Bootstrap org id injection: `internal/api/bootstrap.go:600-603`.
- Frontend uses injected org id: `frontend/src/utils/constants.js` exports `orgID` from `window.org.pageOptions`.
- Stats helpers document nil UUID as platform aggregate: `internal/api/v2/admin_extra_stats.go:204-210`.
- Org stats pass the route org id into the same helper: `internal/api/v2/org_admin_stats.go:38-64`.

Why downgraded: `requireOrgAccess` restricts tenant admins to their own org and only platform superadmins can access arbitrary org ids. This looks like a misleading platform-superadmin/org-shell context issue, not a tenant data leak to ordinary org admins.

Recommendation: do not let the org-admin SPA bootstrap with platform org id, or hide org-admin stats when `orgID == PlatformOrgID`.

### 16. Traffic recorder drops saturated events silently

Confirmed. The recorder caps outstanding goroutines and drops new events on saturation without logging or metrics.

Evidence:
- `internal/traffic/recorder.go:31-33` defines `maxInflight = 256`.
- `internal/traffic/recorder.go:80-90` drops in the default branch.

Recommendation: add `traffic_recorder_dropped_total`, rate-limited logging, and optionally a bounded queue/backpressure mode.

### 17. Devices/license metadata in sysadmin dashboards remains unavailable/stubbed

Confirmed, lower severity. Core sysinfo KPIs are real, but device counts are nil and devices list is empty.

Evidence:
- `internal/api/v2/admin_extra_system.go:81-94`.
- Org-admin devices are also stubbed: `internal/api/v2/org_admin_stats.go:123-166`.

Recommendation: hide unavailable fields or implement device tracking.

### 18. CI / frontend E2E / coverage maturity is incomplete

Confirmed. There is no `.github/` workflow in the repository root. Docs still list E2E/testcontainers/coverage improvements as not started or future work.

Evidence:
- `rg .github` fails because the folder does not exist.
- `docs/TECHNICAL-DEBT.md:137-185` proposes adding `.github/workflows/test.yml`.
- `docs/TESTING.md:765-766` marks testcontainers and frontend E2E as not started.

Recommendation: add a minimal CI gate first: Go unit tests, frontend tests/build, migration check, then integration/E2E as a second phase.

## No Longer Pending / Stale In The Root Lists

These entries should be removed or rewritten in the root pending files:

1. GC multi-instance safety as a P0 blocker.
   - No longer accurate as written. `internal/gc/lease.go` implements a Cassandra leader lease, and `internal/gc/gc.go:214-222` acquires it before worker/scanner startup. Remaining GC issue is the queue recount operability debt, not absence of leadership.

2. Security headers as missing.
   - No longer accurate. `internal/middleware/securityheaders.go` exists and is registered in `internal/api/server.go:150`.

3. `ReadHeaderTimeout` as missing.
   - No longer accurate. `internal/config/config.go:437` defines it, default is `10s` at `internal/config/config.go:668`, and server uses it at `internal/api/server.go:1942-1948`.

4. Preview/evaluate plan endpoint as pending.
   - No longer accurate. `quotas-pending-issues.txt` already marks it complete, and docs/code reference `POST /api/v2.1/admin/organizations/:org_id/preview-plan-change/`.

5. Web upload "do not replace" as broken.
   - Stale for web path. The frontend now sends `replace=0` in `frontend/src/components/file-uploader/file-uploader.js:733-738`. Desktop/client endpoint semantics remain pending.

6. Storage classes as "base infra only".
   - Stale as a blanket statement. Create-time org policy and class resolution are implemented. Existing-library migration and full lifecycle remain pending.

## Recommended Practical Order

1. Disable or fix false-success batch move.
2. Create backup/DR runbook plus first restore drill.
3. Add persistent audit/activity foundation and Accounts provenance.
4. Remove GC queue exact recounts from hot paths.
5. Decide upload token replace/resume contracts.
6. Finish storage migration lifecycle only if multi-region/cold storage is in V1.
7. Triage stubs by product need: file stats, default repo, devices, activities, notifications, wikis.
8. Add CI baseline and only then expand E2E/coverage gates.
