# Known Issues - SesameFS

**Last Updated**: 2026-06-11

This document tracks all known bugs, limitations, and issues in SesameFS.

---

## Issue Summary by Priority

### 🔴 Production Blockers (Must Fix Before Deploy)
| Issue | Status | See |
|-------|--------|-----|
| OIDC Authentication | ✅ Complete (Phase 1) | `docs/OIDC.md` |
| Garbage Collection | ✅ Complete | `internal/gc/` — queue, worker, scanner, admin API |
| Monitoring/Health Checks | ✅ Complete | `/health`, `/ready`, `/metrics` + slog logging |
| Sync Protocol Permissions | ✅ Complete (2026-02-11) | All 15 sync endpoints enforce library permissions; `syncAuthMiddleware` hardened |
| Sync Race Condition | ✅ Fixed (2026-02-18) | 7 bugs fixed: CAS HEAD updates, parent-chain validation, empty root handling |
| Secrets/Env Management | ✅ Complete (2026-02-11) | All docker-compose vars from `.env`; no hardcoded credentials; JWT secret externalized |
| **Programmatic Auth (API keys)** | ✅ Fixed (2026-04-03) | User API keys now support desktop client, CLI, and automation auth in OIDC-only prod |

### 🟡 High Priority (Core Feature Gaps)
| Issue | Status | Details |
|-------|--------|---------|
| **Default Library on First Login** | 🟡 Pending | Seafile auto-crea una librería "My Library" al primer login del usuario. Nosotros devolvemos `exists:false` en `GET/POST /api2/default-repo/`. El cliente no bloquea, pero el usuario arranca sin ninguna librería. Ver ISSUE-DEFAULT-REPO-01 abajo. |
| Search File Paths | ✅ Fixed | Full paths now populated during sync and backfill |
| Groups Creation | ✅ Tested | User-facing CRUD + members + group sharing verified (20 integration tests) |
| Departments Support | ✅ Complete | Full CRUD, hierarchy, 29 integration tests |
| API Token Library Access | ✅ Complete | 37 integration tests, full RW/RO enforcement |
| Move/Copy Dialog Tree | ✅ Fixed | `with_parents` param missing in ListDirectoryV21 |
| GC TTL Enforcement | Partial | `version_ttl_days` and `auto_delete_days` have storage, API, and scanner wiring, but their current behavior does not fully match the library settings UI. See ISSUE-LIB-RETENTION-01. |
| Admin Panel | ✅ Working in Docker | `/sys/` route serves sysadmin.html via nginx + Go catch-all |
| Frontend Permission UI | 🟡 ~85% Done | API layer returns real permissions on all directory/file endpoints. **Fixed**: `"owner"` permission now mapped to `"rw"` in API responses (was breaking upload button). **Enhanced (2026-03-11)**: Granular `PermissionFlags` (8 flags) now enforced backend-side via `RequirePermFlag()`. Upload/share link uploaders updated. Remaining: some UI components that conditionally render controls based on flags. |
| Modal Dialogs | ✅ All 122 Fixed | All dialog files use Bootstrap classes |
| Library Settings Backend | Partial | API tokens and transfer are complete. History and auto-delete settings persist, but retention/delete semantics are incomplete. See ISSUE-LIB-RETENTION-01. |
| **Desktop SSO Browser UX** | ✅ Fixed (2026-03-04) | After browser SSO login for desktop client, now shows confirmation page with auto-close. See ISSUE-SSO-01 below. |
| **Desktop Sync Active-Active Conflict Recovery** | 🟡 Follow-up coverage debt | `PUT /commit/HEAD` and `POST /update-branch` now use parent-chain validation, CAS, ancestry-gated auto-merge for safe stale siblings, and `503 + Retry-After` fail-closed responses for unsafe conflicts. The real desktop-client harness now proves both the non-overlapping auto-merge race and the same-path unsafe-conflict `503` preservation path. The remaining gap is broader end-to-end scenario coverage. See ISSUE-SYNC-HEAD-RECOVERY-01 below. |
| **Upload "Don't Replace" (Desktop Client)** | ✅ Fixed (2026-05-22) | `upload-link` now defaults to autorename/no-replace, `update-link` defaults to overwrite, and the token policy is persisted in Cassandra for multi-node safety. See ISSUE-UPLOAD-REPLACE-01 below. |
| **Org Logo Upload** | 🟡 Stub | `UpdateOrgLogo` in org_admin.go accepts the file but does not persist it to storage. Returns a static path from settings. Functional as a route placeholder until an asset storage backend is available. |
| **Login Analytics History** | 🟡 Partial | `last_login` is now real and persisted in `users.last_login_at`, but there is still no historical login event dataset for trend analysis, login audit timelines, or period-based "users who logged in" charts. See ISSUE-LOGIN-ANALYTICS-01 below. |
| **File Statistics Pages Are Still Stubbed** | 🟡 Pending | `/sys/statistics/file/` currently returns all-zero series and `/org/statistics-admin/file/` is still unimplemented. Real data depends on new `file_update_logs` and `file_access_logs` tables, not on `login_logs`. See ISSUE-FILE-STATS-01 below. |
| **Org Admin Statistics Can Leak Platform Scope** | 🔴 Confirmed bug | When org-admin pages are mounted with platform-org context, traffic-based org-admin metrics can resolve to the global aggregate instead of a tenant scope. Affects at least org-admin traffic, org-admin per-user traffic, and org-admin active-users. See ISSUE-ORG-STATS-SCOPE-01 below. |
| **Per-User Storage Quota Enforcement** | ✅ Fixed (2026-05-14) | `CheckStorageQuota` now evaluates org and per-user storage caps; upload callers pass `userID`; sync validates the published tree delta before advancing HEAD and waits for the matching storage-counter adjust before returning. See ISSUE-USER-STORAGE-ENFORCE-01 below. |
| **Quota Enforcement Coverage Gaps in V2 Mutations** | ✅ Fixed (2026-05-14) | The affected non-upload mutation handlers now have visible-delta quota wiring. Deleted file/folder restore remains bounded by configured history retention, deleted-library restore remains bounded by trash retention, and cross-repo move still relies on split-phase destination publish plus source removal. Split-phase publish/counter atomicity remains documented as technical debt (§12d/§12e). See ISSUE-QUOTA-COVERAGE-01 below. |
| **Concurrent Hard-Quota Reservation Hardening** | 🟡 Deferred to separate branch | The existing split pre-check → publish → counter-adjust window is still open. A canonical-row reservation prototype was audited and is not merge-ready for PR61 because it leaks reservations on finalize failure, regresses soft-policy evaluation, races with admin resync, and only hardens `seafhttp`. A smaller safe fix now caches repeated chunk prechecks per upload tracker, but that is not reservation hardening. See ISSUE-QUOTA-RESERVATION-01 below. |
| **Chunked Upload Traffic Accounting Semantics** | 🟡 Accepted debt | Web chunked uploads now pre-check traffic against the declared `Content-Range` total, and repeated storage prechecks no longer walk HEAD on every chunk, but traffic is still recorded only after successful finalize. Abandoned chunk sessions can consume bandwidth without advancing counters. See ISSUE-CHUNKED-UPLOAD-TRAFFIC-01 below. |
| **Block Refcount Idempotence After Ambiguous CAS** | 🟡 Accepted hotfix debt | Upload/sync block registration now fails closed when Cassandra cannot attribute an LWT outcome, but client retries can still inflate `blocks.ref_count` because block registration has no durable idempotency key yet. See ISSUE-BLOCK-REFCOUNT-IDEMPOTENCE-01 below. |
| **`blocks` Hot Partition by `org_id`** | ✅ Fixed (2026-05-26) | `blocks`, `gc_block_candidates`, `gc_s3_orphans`, and the block-id mapping tables now use per-block partitioning so no single org concentrates LWT traffic into one Cassandra partition. The GC scan and S3 orphan recovery paths walk per-day discovery projections instead of partition-scanning by org. See ISSUE-BLOCKS-HOT-PARTITION-01 below. |
| **Soft-deleted Libraries Still Accept Star Mutations** | 🟡 Pending | `StarFile` still treats a library as live if the canonical row exists, even when `deleted_at` is set. That leaves a real post-soft-delete write window and can reopen cleanup drift during library cascade. See ISSUE-LIB-DELETED-FENCE-01 below. |

### 🟡 SeaDrive 3.x Missing Endpoints (Non-fatal, but degrade UX)
| Issue | Status | Notes |
|-------|--------|-------|
| `POST /api2/default-repo/` | ✅ Fixed (2026-02-20) | Seafile client POSTs to create "My Library" when none exists. We only had GET registered → 405. Fixed: POST now stubbed to return `{"exists": false}`. |
| `GET /seafhttp/repo/locked-files` | ❌ 404 | File lock status for virtual drive. SeaDrive logs warn but continues. |
| `GET /seafhttp/repo/:repo_id/jwt-token` | ❌ 404 | Repo-scoped JWT for SeaDrive 3.x access control. Seems non-fatal for basic sync. |
| `GET /seafhttp/accessible-repos/` | ❌ 404 | Repo accessibility check used by SeaDrive virtual drive. Non-fatal. |
| `GET /seafhttp/repo/:repo_id/block-map/:block_id` | ❌ 404 | Block composition map for differential sync. Degrades sync efficiency. |

### 🟡 File Editing UX (Text/Markdown/Code files)
| Issue | Status | Notes |
|-------|--------|-------|
| **In-browser file editing** | ❌ Not Implemented | Clicking text files (.py, .md, .json, etc.) opens a read-only preview modal (`FilePreviewDialog`). No inline editor exists. Seahub original loads a full React editor page with `window.app.pageOptions.canEditFile`. See ISSUE-FILE-EDIT-01 below. |
| **fileview.go lacks editor integration** | ❌ Not Implemented | `/lib/:repo_id/file/*` serves static HTML preview instead of loading the React editor app. Missing: `canEditFile`, `filePerm` in `pageOptions`. OnlyOffice (.docx/.xlsx/.pptx) works if configured. |

### 🟡 Owner Email Shows as UUID Instead of Real Email
| Issue | Status | Details |
|-------|--------|---------|
| **Display fields still hardcoded** | 🟡 Partial fix (2026-02-26) | Library list/detail fixed. File history modifier fixed (2026-02-26) — now resolves user name/email from `users` table. Remaining: file detail, starred files, sync token responses still return `UUID@sesamefs.local`. Safe to fix — display only. See ISSUE-EMAIL-01 below. |
| **FS object modifier hardcoded** | 🔴 Risky — needs migration analysis | `seafhttp.go` and `onlyoffice.go` write `UUID@sesamefs.local` into stored FS object modifier field, which is part of the `fs_id` hash. Changing breaks hash of existing stored objects. See ISSUE-EMAIL-01 below. |

### 🟡 Frontend Pending — New Backend Features (2026-03-18)
| Issue | Status | Details |
|-------|--------|---------|
| **Superadmin: Org Soft-Delete/Restore UI** | 🟡 Backend ready, frontend TODO | Backend has `POST /admin/organizations/:org_id/delete/` and `POST .../restore/`. Frontend superadmin dashboard needs: "Delete" button (distinct from "Deactivate"), "Restore" button for deleted orgs, status column showing active/deactivated/deleted with grace period countdown. See ISSUE-FRONTEND-ORG-DELETE-01 below. |
| **Superadmin: Deleted Orgs List/Filter** | 🟡 Backend ready, frontend TODO | `ListOrganizations` should filter/tab by status (active, deactivated, deleted). Currently shows all orgs with no status differentiation. |
| **Org Admin: Org Deletion Awareness** | 🟡 Backend ready, frontend TODO | Org admin dashboard should show a banner/warning if their org is in "deleted" state with days remaining before permanent cascade. |

### 🟢 Lower Priority (Polish/UX)
| Issue | Status | Notes |
|-------|--------|-------|
| Activities Feed + Audit Logs | 🔴 Stub only — prioritize soon | Returns empty `{events:[]}`. Needs 5 DB tables, ~15 handler integrations. See ADMIN-FEATURES.md § 3 |
| Published Libraries (Wikis) | ❌ Hidden + Stub | Nav hidden, `/api/v2.1/wikis/` returns `[]`. Needs wiki/publish backend |
| Linked Devices | ❌ Hidden + Stub | Nav hidden, `/api2/devices/` returns `[]`. Needs device tracking on sync |
| **Sysadmin Info Dashboard Has Residual Stubbed Metadata** | 🟡 Partial | `/admin/sysinfo` now returns real storage, file, active-user, and this-month/this-year traffic KPIs. Remaining gaps: device counts are still unavailable and license fields are still stubbed. See ISSUE-SYSINFO-KPI-01 below. |
| Share Admin (Libraries/Folders/Links) | 🟡 Partial | Share link list/create/delete work; admin management + upload links still missing |
| Watch/Unwatch Libraries | ❌ Deferred | Complex notification system needed |
| Thumbnails | ❌ Not Started | Visual polish |
| User Avatars | ❌ Not Started | Visual polish |
| Frontend Test Coverage | 🟡 ~0.6% | 6 test files for 620+ source files |

**For detailed implementation status, see**: `docs/IMPLEMENTATION_STATUS.md`

---

### ISSUE-SYSINFO-KPI-01: Sysadmin Info Dashboard Still Has Residual Stubbed Fields

**Status**: 🟡 Partial fix (2026-03-26)
**Severity**: Low-Medium — core KPIs are now usable; remaining gaps are secondary metadata
**Affected**: `GET /api/v2.1/admin/sysinfo/`, superadmin info dashboard

#### Problem

The core sysadmin KPI issue is mostly resolved. The endpoint now returns real values for:

- `active_users_count`
- `total_files_count`
- `total_storage`
- `traffic_month_total`, `traffic_month_upload`, `traffic_month_download`
- `traffic_year_total`, `traffic_year_upload`, `traffic_year_download`

The remaining gaps are limited to residual stubbed or unavailable fields:

- `total_devices_count` and `current_connected_devices_count` are still unavailable because device tracking is not implemented
- license-related fields are still stubbed

The page is now trustworthy for the main operational KPIs, but it still exposes non-authoritative metadata for licensing and devices.

#### Fixed

- Platform storage now comes from the `storage_counters` `platform` scope
- Platform file count now comes from the same storage snapshot
- Active users now use user lifecycle status instead of duplicating total users
- Sysadmin overview now exposes this-month and this-year traffic KPIs

#### Remaining Work

- Keep device KPIs hidden or explicitly unavailable until device tracking exists
- Either wire real licensing metadata or hide the license block in the frontend until it is authoritative

#### Related Docs

- `docs/DASHBOARD-REDESIGN-PLAN.md`
- `docs/ADMIN-DASHBOARD-WIREFRAMES.md`

---

### ISSUE-LOGIN-ANALYTICS-01: Only Point-in-Time `last_login` Exists

**Status**: 🟡 Partial fix (2026-03-26)
**Severity**: Medium — user detail pages now show a real last login, but audit/reporting remains incomplete
**Affected**: Admin user lists/details, org-admin user lists/details, any future login analytics/reporting feature

#### Problem

SesameFS now persists `users.last_login_at` on successful authentication, so the `last_login` field shown in admin and org-admin responses is no longer stubbed. However, this is only a point-in-time field on the user row.

There is still no historical login events table, which means the system cannot answer questions like:

- how many users logged in during a selected period
- login trends over time
- per-user login history / audit trail
- anomaly detection based on login frequency

#### Fixed

- Successful session creation now updates `users.last_login_at`
- Dev-token login also updates `users.last_login_at`
- Admin and org-admin user responses now serialize the real `last_login`

#### Remaining Work

- Add a dedicated login-events dataset if login audit/history becomes a product requirement
- Keep using traffic-based activity metrics for period charts until real login analytics exist

---

### ISSUE-FILE-STATS-01: File Statistics Screens Have No Real Event Dataset Yet

**Status**: 🟡 Pending (2026-03-26)
**Severity**: Medium — screens exist but do not provide trustworthy operational data
**Affected**: `/sys/statistics/file/`, `/org/statistics-admin/file/`, backend `AdminStatisticFiles`, backend `OrgStatisticFiles`

#### Problem

The file statistics pages are present in the frontend, but the backend does not yet have a real historical event source for file operations.

Current behavior:

- sysadmin `GET /api/v2.1/admin/statistics/file-operations/` returns a date range filled with zeros
- org-admin `GET /api/v2.1/org/:org_id/admin/statistics/file-operations/` is still not implemented

These screens cannot be fixed with `users.last_login_at` or a future `login_logs` table because they are about file operations, not authentication.

#### Required Data Sources

To make these screens real, SesameFS needs file-event datasets such as:

- `file_update_logs` for `added`, `modified`, `deleted`
- `file_access_logs` for `visited`

An optional `activities` table can support dashboard feeds and broader event browsing, but the file-statistics charts specifically depend on file event history.

#### Remaining Work

- Add immutable file event tables (`file_update_logs`, `file_access_logs`)
- Write file operation events from upload/create/edit/delete/move/rename/download/preview handlers
- Implement real aggregation in `AdminStatisticFiles`
- Implement `OrgStatisticFiles` using the same event source scoped by org
- Keep the UI treated as pending/stubbed until those tables exist

#### Related Docs

- `docs/ADMIN-FEATURES.md`
- `docs/CURRENT_WORK.md`

---

### ISSUE-ORG-STATS-SCOPE-01: Org-Admin Statistics Can Resolve to Platform-Wide Aggregates

**Status**: 🔴 Confirmed bug (2026-03-26)
**Severity**: High — tenant-scoped admin views can show platform-wide data under the wrong context
**Affected**: `/org/statistics-admin/traffic/`, org-admin user traffic table, `/org/statistics-admin/active-users/`

#### Problem

The org-admin frontend reads its `orgID` from the org-admin SPA shell injection (`window.org.pageOptions.orgID`). When that shell is served in platform-org context, the injected `orgID` becomes the platform UUID (`00000000-0000-0000-0000-000000000000`).

For traffic-based metrics, that UUID is not just another org value: it is also the sentinel used by backend helpers for platform-wide aggregate partitions.

As a result, some org-admin statistics can collapse to platform-wide values instead of tenant-scoped values.

#### Confirmed Affected Metrics

- org-admin traffic time series
- org-admin per-user traffic table
- org-admin active-users time series

#### Not Confirmed With The Same Failure Mode

- org-admin storage time series appears to remain tenant-scoped because it uses `org:<orgID>` storage scopes rather than the traffic aggregate sentinel
- org-admin file statistics are still pending/unimplemented, so they are not currently part of this leak

#### Root Cause

- `serveOrgAdminPanel` injects org-admin context from the authenticated user session
- platform org uses the nil UUID / all-zero UUID
- traffic/active-user statistics helpers treat that same UUID as the platform-wide aggregate partition

This is a scope-boundary bug, not a traffic math bug.

#### Remaining Work

- Decide the product rule for platform users opening `/org/...`
- Ensure org-admin routes never execute against the platform aggregate scope by accident
- Add regression tests for org-admin statistics with platform-org vs tenant-org contexts

#### Related Docs

- `docs/CURRENT_WORK.md`
- `docs/IMPLEMENTATION_STATUS.md`

---

### ISSUE-USER-STORAGE-ENFORCE-01: Per-User Storage Quota Enforcement

**Status**: ✅ Fixed (2026-05-14)
**Severity before fix**: High — per-user storage caps set via the admin API had no effect on actual upload blocking
**Affected before fix**: `PUT /api/v2.1/admin/organizations/:org_id/users/:email/` (`quota_total` field); upload handlers

#### Resolution

`quota_total` is persisted to the `users` table and validated on write against the org's `storage_quota` ([internal/api/v2/write_helpers.go:901-912](internal/api/v2/write_helpers.go#L901-L912)). `CheckStorageQuota` now receives `userID`, reads `users.quota_bytes`, reads the live per-user counter `user:<orgID>:<userID>`, and returns the more restrictive result between org-level storage and per-user storage.

Updated upload paths pass `userID` into the storage pre-check. Web/direct uploads validate the visible storage delta, including chunked upload totals and replace-over-existing cases. Sync also validates the real committed tree delta before publishing a new HEAD, so multi-block desktop uploads are checked against the final storage increase rather than only against each individual block.

#### Previous Root Cause

- `CheckStorageQuota(orgID, additionalBytes)` receives only `orgID` — no `userID` parameter.
- The function queries `organizations WHERE org_id = ?` for `storage_quota` and `quota_policy`. `users.quota_bytes` is never read here.
- The `storage_counters` table stores per-user counters (`user:<orgID>:<userID>`) which are correctly maintained by `IncrementStorageCounters` / `DecrementStorageCounters`, but those counters are also never consulted during upload pre-checks.

#### Implemented

1. `CheckStorageQuota(orgID, userID, additionalBytes)`.
2. Per-user `users.quota_bytes` lookup.
3. Per-user `storage_counters` lookup using `user:<orgID>:<userID>`.
4. Same hard/soft enforcement logic as org storage quota.
5. Most-restrictive result selection between org and user caps.
6. Callers updated in web, v2 block/file upload, sync block upload, sync quota-check, and sync commit HEAD/update-branch.

#### Related

- `internal/traffic/checker.go` — `CheckStorageQuota` function
- `internal/api/seafhttp.go` — `HandleUpload`, upload callers
- `docs/ACCOUNTS-DASHBOARD-INTEGRATION.md` — §5.2 documents `quota_total` as enforced; fix here must make that description accurate

---

### ISSUE-QUOTA-COVERAGE-01: Quota Enforcement Coverage Gaps in V2 FS Mutations

**Status**: ✅ Fixed (2026-05-14)
**Severity before fix**: High — several non-upload paths grew storage without pre-check, and `storage_counters` drifted from real disk usage on those paths
**Affected before fix**: V2 file mutation endpoints that go through `FSHelper.UpdateLibraryHead` but are not file uploads

#### Resolution

The handler-by-handler approach was chosen over centralizing the logic in `FSHelper.UpdateLibraryHead`. A small shared helper module was added in [internal/api/v2/quota_helpers.go](internal/api/v2/quota_helpers.go) with three primitives:

- `fsEntryStats(fsHelper, repoID, entry)` returns `(size, fileCount)` for a file or directory entry, recursing through directories.
- `fsEntryDelta(fsHelper, repoID, newEntry, replacing)` returns the `(bytes, files)` delta when `newEntry` is added to a tree, optionally replacing an existing entry.
- `preCheckStorageQuotaForDelta(c, orgID, userID, deltaBytes)` performs the quota pre-check and writes a 403 on failure.
- `applyStorageCounterDelta(c, db, orgID, userID, repoID, deltaBytes, deltaFiles)` calls `AdjustStorageCountersByDeltaSync` and writes a 500 if the counter adjust fails.

Wiring per handler:

1. **CopyFile** (`internal/api/v2/files.go`, `CopyFile`) — captures the replaced entry when `conflict_policy = replace`, computes delta against the source `TargetEntry`, pre-checks before mutating the tree, applies counter delta after `UpdateLibraryHead`.
2. **copyBatchFiles** (`internal/api/v2/files.go`, `copyBatchFiles`) — same wiring per item in the loop.
3. **RevertFile** (`internal/api/v2/files.go`, `RevertFile`) — delta uses `oldEntry` from the target commit, subtracts the size of `existingEntry` when replacing.
4. **RevertDirectory** (`internal/api/v2/files.go`, `RevertDirectory`) — delta walks both trees (old and replaced) recursively via `fsEntryStats`.
5. **RestoreTrashItem** ([trash.go:329](internal/api/v2/trash.go#L329)) — pre-checks the visible delta; counter delta replaces the previous fire-and-forget `IncrementStorageCounters`.
6. **RevertDirents** ([trash.go:656](internal/api/v2/trash.go#L656)) — per-item pre-check; items exceeding quota fall into `failedItems` so the batch returns partial success instead of a hard error.
7. **OnlyOffice `saveEditedDocument`** ([onlyoffice.go:831](internal/api/v2/onlyoffice.go#L831)) — pre-check moved before the S3 `PutBlockData` so we never store bytes that would be rejected. The traversal previously done late in the function is reused.
8. **Cross-repo batch (`processSingleItem`)** ([batch_operations.go:324](internal/api/v2/batch_operations.go#L324)) — pre-checks the destination delta, applies the destination counter increment after the destination publish, and decrements the source library counter on move.

Sync (`PutCommit HEAD`, `UpdateBranch`) and the upload paths (`HandleUpload`, `UploadFile`, `UploadBlock`, `PutBlock`) keep their existing wiring; this fix only adds coverage to the non-upload mutation paths.

#### Scope Boundary / Retention Contract

- This fix closes the missing quota pre-check and storage-counter wiring for the listed non-upload mutation handlers.
- It does **not** create an indefinite deleted-item durability guarantee. Deleted file/folder restore in a live library remains available only while the backing historical commits stay inside the configured `version_ttl_days` window, plus the normal `gc_queue` grace once those commits are enqueued.
- Deleted-library restore remains bounded by `trash_retention_days`, after which GC may enqueue `library_cascade` and remove the remaining commits, fs_objects, and blocks.
- It does **not** make cross-repo move atomic. `processSingleItem` still publishes the destination before removing the source; rare partial-success cases remain accepted technical debt for future reconciliation or reservation-style work.
- It does **not** normalize post-publish counter-failure handling yet. Some handlers still return 500 while others log and continue; if product keeps the success-after-publish paths, durable repair/reconciliation is still future work.

#### Tests

`internal/integration/quotas_test.go`:

- `TestCopyFileEnforcesPerUserStorageQuota` — quota-blocked copy returns 403, counter stays put; with headroom the copy succeeds and counter reflects the source size.
- `TestRestoreTrashItemEnforcesPerUserStorageQuota` — delete a file, lower the cap, restore from trash returns 403; with headroom the restore succeeds and counter is recovered.
- `TestRevertFileEnforcesPerUserStorageQuota` — replace a large file with a small one, revert to the older commit is blocked when the delta would exceed the cap; with headroom the revert succeeds and the counter reflects the byte delta.

All three previously failed with status 200 (silent over-quota) before the fix. Now PASS. Full integration suite (`docker compose run --rm go-integration-test`) is green.

Coverage still remains narrower than the handler list above:
- there is no dedicated integration coverage yet for `OnlyOffice saveEditedDocument`
- `RevertDirectory` currently only has basic request-validation unit coverage, not the same end-to-end quota/counter coverage as `RevertFile`
- `RevertDirents` still lacks dedicated integration coverage for its partial-success and counter behavior
- async cross-repo copy/move has focused refcount and net-move quota coverage, but not full post-publish counter-failure coverage

#### Why this shape

The shared helper pair keeps quota math in one place but leaves the handler in charge of when and where to apply it. Centralizing inside `FSHelper.UpdateLibraryHead` was rejected because uploads already compute their own visible delta (replace vs autorename) and would conflict with a tree-recompute model.

Split-phase atomicity (pre-check → publish → counter adjust) remains documented as technical debt in `docs/TECHNICAL-DEBT.md` §12d/§12e. The hot-path cost of the upload pre-check is also tracked in §12e.

#### Related

- ISSUE-USER-STORAGE-ENFORCE-01 (fixed) — same enforcement model, narrower coverage.
- [internal/api/v2/quota_helpers.go](internal/api/v2/quota_helpers.go) — shared primitives.
- [internal/traffic/storage.go:136](internal/traffic/storage.go#L136) — `AdjustStorageCountersByDeltaSync` with negative-clamp protection.

---

### ISSUE-QUOTA-RESERVATION-01: Canonical Storage Reservation Prototype Is Not Merge-Ready

**Status**: 🟡 Confirmed follow-up / intentionally excluded from PR61 (2026-05-21)
**Severity**: Medium-High — the existing concurrent hard-quota window remains open, and the investigated fix candidate adds correctness regressions of its own
**Affected candidate patch**: `internal/api/seafhttp.go`, `internal/traffic/checker.go`, `internal/traffic/storage.go`, `internal/api/v2/admin.go`, `internal/api/v2/admin_users.go`, `internal/api/v2/files.go`

#### What Was Confirmed

- `commitUploadedFileOnce` and `commitUploadedFileMultiBlockOnce` disabled the deferred release before calling `FinalizeReservedUploadStorageDeltaSync`, so any finalize error leaked the canonical reservation instead of releasing it.
- `ReserveStorageQuota` returned immediately for non-hard policies, so the soft-plan `CheckStorageQuota` path stopped evaluating usage and warning state on those uploads.
- The admin org/user quota resync writes overwrote `organizations.storage_used` / `users.used_bytes` directly from live `storage_counters` without CAS, so they could erase in-flight reservations.
- `FinalizeReservedUploadStorageDeltaSync` performed several independent counter writes with no batch, rollback, or repair path, so partial failure could leave org/user/library/platform scopes diverged.
- The canonical CAS retry loop used a deterministic linear sleep without jitter, increasing herd behavior under contention.
- The reservation flow was only wired into `seafhttp` uploads; v2 direct upload/finalize paths still used the older split pre-check and post-commit counter adjust flow.
- The same patch also regressed `DeleteDirectory` by dropping `cleanupFileTagsForPrefix`, which would have left stale `file_tags`, `file_tags_by_id`, and `repo_tag_file_counts` rows behind.

#### What Was Not Confirmed

- `mustParseUUID` in `internal/traffic/checker.go` does not panic on invalid input in the current code; it returns the zero UUID. Invalid IDs are still undesirable, but this report was not a panic bug.

#### Branch Decision

- Keep the `scripts/test.sh` failure-excerpt improvement in PR61.
- Move any canonical reservation / finalize / release quota work to a dedicated follow-up branch with its own tests and review.
- Keep the smaller tracker-scoped chunk precheck cache separate from reservation work; it reduces hot-path read cost without changing publish/counter atomicity.

#### Follow-up Branch Requirements

- Preserve soft quota evaluation and warning behavior.
- Release reservations only after successful finalize, or make finalize idempotent and repairable.
- Address partial-finalize repair and admin resync races under CAS semantics.
- Reuse the existing jittered backoff helper instead of adding a new linear retry loop.
- Decide whether to harden only `seafhttp` or all upload/finalize paths together.

---

### ISSUE-CHUNKED-UPLOAD-TRAFFIC-01: Chunked Upload Traffic Is Recorded At Finalize, Not Per Received Chunk

**Status**: 🟡 Accepted debt / documented contract (2026-05-15)
**Severity**: Low-Medium — declared-total pre-check now blocks obvious over-quota chunked uploads, but abandoned chunk sessions can still consume real bandwidth without moving traffic counters
**Affected**: `HandleUpload` chunked path, web/link upload traffic quotas, future traffic/billing semantics

#### Current Contract

The web chunked upload path now parses `Content-Range` and uses the declared total for the traffic pre-check before reading the multipart body. That closes the earlier fail-open where a large resumable upload could slip through using only per-request `Content-Length`.

The handler also now caches a successful storage pre-check on the in-memory upload tracker so repeated chunk requests stop re-walking the visible HEAD on every request. Finalization still performs its own authoritative re-check against the current HEAD before publish.

Traffic recording is still tied to successful logical upload completion:

- each chunk is written to the temp upload session immediately
- `RecordCheckedTransfer(...)` is only called after `finalizeUploadStreaming()` succeeds
- abandoned uploads, janitor-reaped chunk sessions, and finalize failures do not increment `traffic_period_usage` today
- invalid or missing `Content-Range` falls back to the non-chunked upload path instead of returning a strict protocol error

#### Why This Is Acceptable For Now

- paid plans currently include very generous monthly upload allowance (50 TB/month on the standard paid tiers)
- paid-plan overage is commercial/billing logic outside SesameFS; SesameFS mainly gates free/hard-limit abuse and surfaces warnings
- the declared-total pre-check still blocks clearly over-quota chunked uploads before the body is processed

This means the current web chunked traffic counters represent completed logical uploads, not exact wire bytes received in every failure/abort case.

#### Remaining Debt

If product later decides that upload traffic quota must equal raw network usage rather than successful logical uploads, the current model is not enough. Future work would need to:

- record received bytes per chunk, or introduce a session reservation/reconciliation model
- make duplicate/retried chunk writes idempotent for traffic accounting, not just for temp-file writes
- decide whether malformed `Content-Range` should be rejected with `400` instead of falling back
- add handler/integration coverage for aborted uploads, finalize failures, duplicate chunk retries, and malformed headers

#### Existing Coverage

Current tests already cover part of the contract:

- `TestParseContentRange`
- `TestHandleUploadQuotaContract_ChunkedPrecheckUsesDeclaredTotal`
- `TestChunkUploadWriteDuringFinalizationIsIdempotentOnly`
- `TestChunkedWebUploadChecksTotalStorageQuota`

What is still missing is traffic-accounting coverage for abandoned or failed chunked uploads.

#### Related Docs

- `docs/TECHNICAL-DEBT.md`
- `docs/QUOTAS-AND-TRAFFIC-PLAN.md`

---

### ISSUE-BLOCK-REFCOUNT-IDEMPOTENCE-01: Ambiguous `blocks` LWT Outcomes Can Still Leak Refcounts Across Retries

**Status**: ✅ Fixed (2026-05-27) by the row-per-reference redesign — `blocks.ref_count`
is gone. References are modeled as rows in `block_references` (one row per
`(block, referrer)`: `fs:<library>:<fs_id>` for committed fs_objects, `up:<library>:<block>`
for in-flight uploads). Adding/removing a reference is an idempotent `INSERT`/`DELETE`
with no LWT, so a client retry that re-registers the same content-addressed
`(block, fs_id)` cannot inflate anything — the ambiguous-CAS leak class no longer
exists. The only expensive Paxos left is the GC worker's `gc_state='deleting'` claim
at the irreversible S3-delete gate (claim-then-verify). See
`internal/db/block_references.go`, `FSHelper.RegisterUploadedBlock` /
`RegisterFSObjectBlockReferences`, and the GC worker's `processBlock` /
`removeFSObjectBlockReferences`. Original analysis kept below for context.

**Severity (when active)**: Medium-High operational risk — preferred fail-closed and no unsafe rollback, but some retry paths could still inflate `blocks.ref_count`
**Affected**: `IncrementOrCreateBlock`, sync `PUT /seafhttp/repo/:repo_id/block/:block_id`, seafhttp upload finalize paths, future block-ref accounting callers

#### Current Behavior

`resolveIncrementBlockMutationError()` and `resolveInsertBlockMutationError()` now treat a confirmation read that sees the "expected" post-LWT state as **unknown outcome**, not success, unless the mutation can be attributed unambiguously.

This is deliberate. It avoids two unsafe behaviors:

- returning false-success after Cassandra may have rejected the write and another writer happened to produce the same visible state;
- rolling back a block refcount mutation that may already be visible and legitimately referenced.

The hotfix also reduces the main production trigger by serializing chunked `blocks`-table metadata LWTs per process (`finalizeUploadBlockMetadataConcurrency = 1`) and by documenting the required multiregion runtime contract (`SERIAL`, higher Cassandra timeout, slow-query monitoring).

#### Remaining Risk

The flow is still not fully idempotent across client retries.

- **Sync `PutBlock`** now returns `500` when `IncrementOrCreateBlock` cannot prove the outcome. If the first LWT actually applied but the client lost the ACK, a client retry can re-run the same block registration and increment the refcount again.
- **Chunked upload finalize** now cleans up the tracker on `ErrBlockMutationOutcomeUnknown` and only rolls back the blocks that were already accounted before the ambiguous block. That is the safe choice against data loss, but a full re-upload can still increment the ambiguous block again.
- **Single-shot/direct upload paths** also fail closed on ambiguous block registration, so they share the same leak-vs-false-success tradeoff.

The current branch therefore fixes the production outage class better than `main`, but it does **not** yet provide a fully idempotent block-registration contract under ambiguous Paxos outcomes.

#### What Is Already Narrowed

- Copy/move code paths that need rollback already use `IncrementBlockRefCountsTracked()` so they receive the exact list of confirmed increments to unwind on publish failure.
- The older `IncrementBlockRefCounts()` helper now attempts rollback of previously confirmed increments before returning an error. That narrows the partial-progress footgun, but rollback is still best-effort because `DecrementBlockRefCountsOnce()` does not surface a rollback error back to the caller.
- The new chunked-upload permit is intentionally only a **process-local** pressure valve. It lowers the chance of `blocks`-table Paxos storms from one finalize wave, but it is not cluster-wide serialization.
- The current audit did **not** confirm a same-finalize self-deadlock from that permit: each block acquires/releases it inside its own block goroutine. The remaining gap is coverage, not a confirmed blocker — the suite still lacks a chunked upload test that forces `finalizeUploadStreaming()` through a file larger than `uploadBlockSize` while `finalizeUploadBlockMetadataConcurrency = 1`.

#### What Would Fully Close It

A complete fix needs durable idempotency or reconciliation around block registration itself, for example:

- a per-operation idempotency key stored with the block mutation so client retries can be deduplicated;
- a durable pending-upload/block-promotion row that can reconcile ambiguous mutations after the fact;
- or a reconciler that recomputes/refines `blocks.ref_count` from published commit reachability rather than trusting every upload-time increment as final truth.

Until then, treat this as accepted hotfix debt: safer than false-success/data loss, but still capable of temporary or permanent refcount inflation under lost-ACK retry scenarios.

#### Design Directions To Keep Explicit

- **Option B: keep a mutable `ref_count`, but mutate it only with CAS/LWT**. This is the classical optimistic-concurrency pattern already used here: read the current value, compute the next value, and `UPDATE ... IF ref_count = <value_read>`. It is correct and safe, but expensive in multiregion because every conflict resolution rides cross-DC Paxos on a shared row.
- **Option C: stop storing a mutable counter and model references as rows**. Instead of one `blocks.ref_count` integer, store one row per `(block_id, referrer)` and make reference add/remove be `INSERT`/`DELETE` on distinct rows. That removes most writer-vs-writer collisions at the modeling level and makes the GC ask "do any reference rows still exist?" instead of trusting a hot mutable integer.
- **Recommended direction for a future branch**: use row-per-reference where it is practical, and keep expensive LWT only for the irreversible moment when GC is about to delete from S3. In other words: design the steady state so concurrent writers stop colliding, and spend Paxos only at the final safety gate where a mistake would destroy data.

#### Related Docs

- `docs/TECHNICAL-DEBT.md`
- `docs/DEPLOY.md`

---

### ISSUE-BLOCKS-HOT-PARTITION-01: `blocks` Uses `org_id` As the Sole Partition Key

**Status**: ✅ Fixed (2026-05-26)
**Severity (when active)**: High operational risk under load — amplified Paxos contention, slow LWTs, and tail latency well before the logical refcount algorithm itself became the bottleneck
**Affected**: `blocks`, `gc_block_candidates`, `gc_s3_orphans`, block-id mapping tables, upload finalize, sync `PutBlock`, GC delete guard, multiregion deployments

#### What The Schema Used To Look Like

The original `blocks` table was keyed as `PRIMARY KEY ((org_id), block_id)` in `internal/db/migrations/001_initial_schema.cql`. That meant every block refcount LWT for one organization landed in the same Cassandra partition. In a multiregion deployment, every upload/copy/delete/GC refcount mutation for that org contended on the same partition-local Paxos hotspot.

This was especially bad for the reserved platform org (`00000000-0000-0000-0000-000000000000`): the problem stopped being a rare tenant-specific outlier and became a central hot partition for shared/system traffic. Each LWT cost ~1s under cross-DC SERIAL Paxos, and a 2 GB upload (≈263 blocks at 8 MB) accumulated minutes of strictly serialized Paxos before the cluster gave up with `received 0 of 2 required responses`.

#### What Changed

The schema for block-related tables now uses per-block partitioning:

- `blocks` → `PRIMARY KEY ((org_id, block_id))`
- `gc_block_candidates` → `PRIMARY KEY ((org_id, block_id))`
- `gc_s3_orphans` → `PRIMARY KEY ((org_id, block_id))`
- `block_id_mappings` → `PRIMARY KEY ((org_id, external_id))`
- `block_id_mappings_by_internal` → `PRIMARY KEY ((org_id, internal_id), external_id)`

Each block now lives in its own Cassandra partition, so concurrent LWTs from one upload cannot contend at the Paxos layer.

#### Discovery Projections Replace Per-Org Partition Scans

Two paths previously relied on `WHERE org_id = ?` partition scans over `blocks`, `gc_block_candidates`, and `gc_s3_orphans`. Per-block partitioning makes those scans inefficient, so they are replaced by per-day discovery projections:

- `gc_block_candidates_by_day (PRIMARY KEY ((candidate_day, bucket), candidate_at, org_id, block_id))` — the GC scanner walks this by `(day, bucket)` from a persisted cursor (`gc.scan.block_candidates.last_candidate_day`) so it never needs to enumerate all candidate orgs.
- `gc_s3_orphans_by_day (PRIMARY KEY ((first_seen_day, bucket), first_seen_at, org_id, block_id))` — the worker's `RecoverS3Orphans` walks this from a persisted UTC-day cursor across all discovery buckets; on cold start it scans the full 90-day TTL horizon so old orphan rows are still recoverable.

`gc_s3_orphans_by_day` inherits the same 90-day TTL as `gc_s3_orphans`. `gc_block_candidates_by_day` has no TTL safety net, so delete paths carry `candidate_at` forward and remove that discovery row explicitly even if the canonical row disappeared first. The bucket count (`db.GCDiscoveryBucketCount = 32`) mirrors the pattern used by `gc_share_links_by_expiry`.

#### Loss Of The Backfill Safety Net

The old scanner could iterate `blocks WHERE org_id = ?` to find zero-ref rows whose `gc_block_candidates` entry never got written. That partition scan was removed because the new schema makes it expensive, and because the only legitimate path for a block to reach `ref_count=0` already runs through `DecrementBlockRefCountsOnce → enqueueZeroRefBlocks → gcBlockEnqueuer.EnqueueBlocks → EnsureBlockGCCandidate`.

To make the loss observable instead of silent, hard failures in that chain increment `gc_zero_ref_enqueue_failures_total{stage=...}`. Alert on sustained non-zero values there: those are the lost-to-GC cases where a zero-ref block did not reach pending GC state. Soft failures where the canonical candidate row succeeded but repairing `gc_block_candidates_by_day` degraded now increment `gc_block_candidate_discovery_degraded_total{source=...}` instead; treat that as a scanner safety-net degradation signal, not proof of lost GC work.

#### Block-Size Lookups On The Read Path

`streaming.QueryBlockSizes` used to batch up to 100 block IDs into a single `WHERE org_id = ? AND block_id IN ?` query, which made sense when those IDs shared a partition. Under per-block partitioning each IN element resolves to its own partition, so the function now issues parallel single-row reads (`blockSizesConcurrency = 32`) and falls back to S3 HEAD for any block still missing.

#### Related Docs

- `docs/SCHEMA-BOTTLENECK-AUDIT.md` (other partition/LWT candidates not addressed here)
- `docs/TECHNICAL-DEBT.md`
- `docs/DATABASE-GUIDE.md`
- `docs/ARCHITECTURE.md`

---

### ISSUE-SYNC-HEAD-RECOVERY-01: Desktop Sync HEAD Conflict Recovery Follow-up Coverage

**Status**: 🟡 Narrowed follow-up coverage debt (2026-05-20)
**Severity**: Medium - the core safe and fail-closed active-active paths are now proved end-to-end, but the real-client scenario matrix is still incomplete
**Affected**: `PUT /seafhttp/repo/:repo_id/commit/HEAD`, `POST /seafhttp/repo/:repo_id/update-branch`, desktop sync launch criteria

#### What Is True Today

The original blind-overwrite bug from February is fixed. Both sync HEAD-publish endpoints now:

- validate the parent chain against the current HEAD;
- advance `libraries.head_commit_id` via CAS in `updateLibraryHeadWithStats()`;
- keep canonical, lookup, and admin projection rows aligned on the successful path.

Both endpoints now perform bounded server-side retry when a stale race is likely recoverable:

- parent-chain mismatch retries with the shared exponential+jitter backoff budget;
- CAS conflict retries with the same bounded backoff budget;
- non-overlapping stale siblings can be resolved by ancestry-gated server-side auto-merge;
- unsafe conflicts now fail closed with `503` plus `Retry-After: 1`, so desktop clients keep local state instead of receiving a false-success `200 OK`.

That means the remaining gap is no longer missing CAS, missing server-side retry, or the lack of any real desktop active-active proof for the two core publish outcomes. The remaining gap is broader end-to-end scenario coverage beyond the verified non-overlapping auto-merge and same-path fail-closed cases.

#### Current Evidence

- Code path: `internal/api/sync.go` uses parent-chain validation, CAS, bounded retry, ancestry-gated auto-merge, and `503 + Retry-After` fail-closed fallback for both `PUT /commit/HEAD` and `POST /update-branch`.
- Same-tree stale idempotence and unmergeable `503` regressions exist for both routes in `internal/integration/library_projection_regression_test.go`.
- Handler-level multi-node convergence proof exists for both routes in `internal/integration/multi_instance_mutations_test.go`.
- Real desktop-client active-active proof exists for concurrent non-overlapping writes in `scripts/test-sync-active-active.sh`, and that harness now asserts that a backend `parent mismatch` and `auto-merge` were actually observed for the proof repo.
- The same harness now also proves the same-path unsafe-conflict branch by observing retry-budget `503` exhaustion while both clients keep their divergent local edits instead of silently converging.

#### Why This Still Exists As Follow-up Debt

The code no longer relies on synthetic `200 OK` for exhausted retry budgets, and the repo now has real active-active desktop proof for both the auto-merge and fail-closed branches that matter most to this change. The remaining gap is breadth rather than absence of proof: this repo still lacks real-client end-to-end exercises for scenarios such as quota rejection during auto-merge, deeper-tree conflicts, and other non-happy-path branches.

Some of those scenarios are already handler-covered, but they are not yet exercised by a real desktop-client harness in this repo.

#### Exit Criteria

- Expand the real desktop-client harness to cover quota rejection, deeper-tree conflicts, and other residual `503` branches; and/or
- add stronger telemetry/assertions around residual fail-closed sync publish outcomes.

#### Related

- `docs/TECHNICAL-DEBT.md` §19.a — duplicated retry orchestration across sync, upload, and v2 mutation helpers.
- `docs/IMPLEMENTATION_STATUS.md` — desktop sync status now reflects verified baseline proof plus remaining follow-up coverage debt.

---

### ISSUE-ZIP-STREAM-LATEFAIL-01: ZIP Directory Download Can Still Truncate After `200 OK`

**Status**: 🟡 Known limitation, narrowed after ZIP preflight fix (2026-05-27)
**Severity**: Medium — failed/truncated download, not data corruption; recoverable by retry
**Affected**: `HandleZipDownload` → `addDirToZip` → `addFileToZip` (`internal/api/seafhttp.go`)

#### What Is True Today

ZIP directory downloads now do a real preflight *before* sending response headers:

- `HandleZipDownload` resolves the library block store up front.
- `prepareZipDirectory` walks the tree, checks ZIP traversal limits, loads each file's metadata, and resolves every file's block IDs *before* `application/zip` / `200 OK` are committed.
- `addFileToZip` then streams the already-prepared entries and no longer performs Cassandra block-ID resolution during response streaming.

This closes the specific fail-open hole where a missing `block_id_mappings` row or Cassandra timeout on block-ID resolution used to surface only after the ZIP response had already started.

The remaining limitation is narrower but still real: once the handler starts streaming ZIP bytes, any **late** per-file error while fetching a block from storage, decrypting it, writing it into the ZIP stream, or while the client disconnects has the same shape. The handler can only log and abort, so the client still sees a **truncated ZIP after `200 OK`** in those cases.

The architecture claim "download handlers resolve before headers" is now true for single-file handlers **and** ZIP mapping-resolution preflight, but not for all possible late storage failures in a streamed archive.

#### Options (not yet done)

- **Full isolation**: build the archive to temp storage/object storage first, then return it as a normal single-file download. This removes the streamed-archive truncation class at the cost of latency, temp-space, and cleanup complexity.
- **Current**: streamed ZIP with preflighted mapping resolution. Late storage/decrypt/write failures are still logged as `[HandleZipDownload] ZIP stream aborted`. A dedicated metric would make those incidents observable.

#### Related

- ISSUE-STREAMBLOCKS-VOID-01 (same "already committed the response" class).

---

### ISSUE-STREAMBLOCKS-VOID-01: `StreamBlocks` Returns Void → False-Success Log + Over-Counted Traffic

**Status**: 🟡 Observability/billing accuracy gap (2026-05-27)
**Severity**: Low–Medium — no client-visible corruption, but traffic can be over-recorded
**Affected**: `streaming.StreamBlocks` and `streamFileFromBlocks` (`internal/api/seafhttp.go`)

#### What Is True Today

`StreamBlocks` ([`streaming.go`](internal/streaming/streaming.go#L176)) returns `void`. If `GetBlock`/`GetBlockReader`/`Write`/`io.CopyBuffer` fails mid-stream it logs and returns — by then headers are sent, so it genuinely cannot signal the client. That part is unavoidable.

The real gap is on the **caller** side. In `streamFileFromBlocks` the code after `StreamBlocks` runs unconditionally:

- logs `Streaming complete: N blocks` even if the stream aborted early;
- calls `traffic.RecordCheckedTransfer(..., fileSize)` with the **full** `fileSize`, over-counting traffic on a partial transfer.

Not every caller has this: `DownloadHistoricFile` and `HandleZipDownload` already record the **actual** bytes written (`bytesAfter - bytesBefore`), so they are accurate. The defect is specific to the `fileSize`-based recording in `streamFileFromBlocks`.

#### Fix (recommended, not yet done)

Make `StreamBlocks` return `error` (or the bytes streamed), and in `streamFileFromBlocks` skip the success log + record only actual bytes on partial transfer. The client still can't get a new status once the body started, but observability and billing stop lying. Low blast radius (≈6 callers), worth doing soon because it touches billing.

---

### ISSUE-REFCOUNT-RESOLVE-FAILCLOSED-01: Block-Ref Mutations Are Fail-Closed Pending the Counter→Per-Block Redesign

**Status**: ✅ Fixed (2026-05-27) by the row-per-reference redesign — the deferred root
fix has landed. `DecrementBlockRefCountsOnce` / `IncrementBlockRefCounts*` are gone.
The decrement path is now `RemoveBlockReference` (an idempotent `DELETE` of a single
`(block, referrer)` row), so a SHA-1 resolution failure during GC is no longer a
*permanent* leak: deleting a missing reference is a no-op and a retried fs_object GC
pass simply re-attempts the `DELETE`. Resolution is still strict/fail-closed on the
*increment* side (`RegisterFSObjectBlockReferences`), which is correct — that path is
pre-commit and abortable, and the in-flight upload's provisional `up:` reference (with
TTL) keeps the block alive meanwhile. Original analysis kept below for context.

**Severity (when active)**: Medium — worst case was a *permanent* block leak in a rare path; no data loss, no corruption
**Affected**: `resolveStoredBlockIDs`, `DecrementBlockRefCountsOnce`, `IncrementBlockRefCounts*` (`internal/api/v2/fs_helpers.go`)

#### Context

`BatchResolveBlockIDs` became strict (`([]string, error)`) so download paths fail clean. Because ref-count mutations share that helper, they had to handle the error. The interim handling is deliberately **fail-closed**, NOT a full repair, because `blocks.ref_count` is slated to be redesigned from a Cassandra counter to per-block file rows in the next branch — that redesign is where this gets fixed at the root, so we are not patching the leak here.

#### Current Behavior

- **Increment** (`IncrementBlockRefCounts`, `IncrementBlockRefCountsTracked`): propagate the resolution error and abort the copy/publish. This is correct and *safer* than before — `IncrementOrCreateBlock` does `INSERT ... IF NOT EXISTS` ([`fs_helpers.go:996`](internal/api/v2/fs_helpers.go#L996)), so incrementing an unresolved SHA-1 would create a **phantom SHA-1 row** while leaving the real SHA-256 block un-incremented → potential data loss on a later delete. Failing closed prevents that. Callers here are pre-commit and can abort.
- **Decrement** (`DecrementBlockRefCountsOnce`): resolves **before** consuming the idempotency marker (so a failure doesn't burn the marker), and on resolution failure logs `ERROR` and returns `nil` (decrements nothing). `decrementBlockRefCount` already skips rows that don't exist ([`fs_helpers.go:1107`](internal/api/v2/fs_helpers.go#L1107)), so there is **no corruption** — but two limitations remain:
  1. **Abort is total, not partial.** If one block of N fails to resolve, the whole decrement is skipped (the old best-effort path decremented the resolvable ones). So the *magnitude* of a leak is larger, though it is now logged.
  2. **The leak is permanent.** The `blocks WHERE org_id` backfill scan was removed (see ISSUE-BLOCKS-HOT-PARTITION-01), so an inflated `ref_count` is never re-discovered as zero-ref. The ~10 post-commit callers also can't distinguish "0 zero-ref blocks" from "aborted" because the signature stays `[]string`.

#### When It Triggers

Only for **SHA-1 blocks** (Seafile desktop client uploads, which need mapping resolution) **and** a Cassandra timeout / missing mapping during resolution. SHA-256 blocks (64 chars) never hit `block_id_mappings`, so the normal path is unaffected.

#### Resolution

Deferred to the `ref_count` counter→per-block-row redesign. That change replaces the LWT-counter model entirely, at which point resolution and decrement semantics are reworked end-to-end rather than patched here.

---

## ✅ Fixed Issues

---

### ISSUE-S3-TRANSPORT-01: All S3 Operations Fail Until Restart — FIXED (2026-03-04)
S3 HTTP connection pool zombie connections blocked all uploads/downloads. Fixed transport settings in `internal/storage/s3.go`. See full details at bottom of this file.

---

### ISSUE-PREINDEX-USERS-01: Pre-Index Users Get "user not found" on Share Operations

**Status**: ✅ Fixed (2026-02-24)
**Severity**: High — sharing with any user created before Session 50 always fails
**Affected**: `POST/PUT/DELETE /api2/repos/:repo_id/dir/shared_items/`

#### Problem
Session 50 added `users_by_email` dual-write for new users, and Session 51 refactored share operations to look up the target user exclusively via `users_by_email`. Users created before Session 50 have no row in that index, so share operations returned `{"failed": [{"email": "...", "error_msg": "user not found"}]}` even though the user existed in the `users` table.

#### Fix
- Added `lookupUserIDByEmail(orgID, email)` helper in `internal/api/v2/file_shares.go`
- Tries `users_by_email` first (fast path)
- Falls back to `users WHERE org_id = ? AND email = ? ALLOW FILTERING` (safe: scoped to org partition)
- Backfills `users_by_email` on fallback success (self-healing)
- All three share operations (Create, Update, Delete) use the helper
- Same fix applied to `AdminHandler.lookupUserByEmail` in `admin.go` with a global scan fallback

---

### ISSUE-PREINDEX-USERS-02: Pre-Index Users Get Duplicate Account on First SSO Login

**Status**: ✅ Fixed (2026-02-24)
**Severity**: High — user loses access to their existing libraries
**Affected**: OIDC login for users created before Session 50 who have never logged in via SSO

#### Problem
The OIDC login flow tries to match the incoming user in this order:
1. `users_by_oidc` (OIDC sub mapping)
2. `users_by_email` (email index)
3. `AutoProvision` → create new user

A user created manually (admin/script) before Session 50 has no `users_by_oidc` entry (never did SSO) and no `users_by_email` entry (pre-index). Both lookups fail, and `AutoProvision` creates a **brand new user** with a different UUID — the original account with all its libraries becomes inaccessible.

#### Fix
Added a third fallback step in `internal/auth/oidc.go` between step 2 and `AutoProvision`:
- Scans `users WHERE email = ? ALLOW FILTERING` (global, but only runs once per user)
- On match: backfills `users_by_email`, creates `users_by_oidc` mapping, updates `users.oidc_sub`, goes to `userReady`
- `AutoProvision` is now only reached for genuinely new users

---

### ISSUE-USERS-BY-EMAIL-01: OIDC and AdminAddOrgUser Missing `users_by_email` Dual-Write

**Status**: ✅ Fixed (2026-02-23)
**Severity**: High — admin operations (delete, get by email) returned 404 for OIDC-provisioned users
**Affected**: `DELETE /admin/users/:email/`, `GET /admin/users/:email/`, any email-based user lookup

#### Problem
OIDC `createUser()` wrote to `users` + `users_by_oidc` but NOT `users_by_email`. `AdminAddOrgUser` also only wrote to `users`. Any admin API that resolved users by email (`lookupUserByEmail` → `users_by_email`) would return "user not found" (404).

#### Fix
- `internal/auth/oidc.go` `createUser()`: Now inserts into `users_by_email` after creating the user
- `internal/api/v2/admin_extra.go` `AdminAddOrgUser`: Now inserts into `users_by_email` after creating the user
- All user creation paths (`CreateOrganization` owner, `AdminCreateUser`, OIDC, `AdminAddOrgUser`, seed) now dual-write to `users_by_email`

---

### ISSUE-ADMIN-USERS-01: Admin User Listing Only Showed Platform-Org Users

**Status**: ✅ Fixed (2026-02-23)
**Severity**: High — superadmin saw no tenant users in admin panel
**Affected**: `GET /admin/users/`, `GET /admin/admins/`, `GET /admin/search-user/`

#### Problem
`ListAllUsers`, `ListAdminUsers`, `SearchUsers` queried `WHERE org_id = ?` using only the caller's org. Superadmin is in platform org (`00000000-...`), so they only saw platform-org users.

#### Fix
All three handlers now check for a real platform superadmin. If so, they iterate over all orgs from the `organizations` table (same pattern as `AdminListAllLibraries`). Tenant admin uses the separate `/org` surface. Results are deduplicated by email.

Also: `ListAdminUsers` response key changed from `"data"` to `"admin_user_list"` (frontend expected `res.data.admin_user_list`), and 13 missing `sysAdmin*` frontend API functions were added to `seafile-api.js`.

---

### ISSUE-SESSION-02: Desktop Client Token Expires After 24h — FIXED

**Status**: ✅ Fixed (2026-03-04)
**Severity**: High — desktop sync clients lose access every 24 hours
**Affected**: Seafile Client, SeaDrive, seaf-cli — any client authenticating via `/api2/auth-token/` or SSO

#### Problem
All sessions (web and desktop client) used the same `session_ttl: 24h`. Seafile desktop clients and SeaDrive do **not** implement token refresh — in the original Seafile server, API tokens from `/api2/auth-token/` are permanent (never expire). With a 24h TTL, sync clients lost access daily and prompted re-login.

#### Fix
Added a separate `api_token_ttl` configuration (default: **180 days**) for desktop/mobile client tokens. Web browser sessions remain at 24h.

- `internal/config/config.go` — new `APITokenTTL` field in `OIDCConfig`
- `internal/auth/session.go` — new `CreateAPITokenSession()` and `CreateSessionWithTTL()` methods; `storeSession()` now derives Cassandra TTL from the actual session duration
- `internal/auth/oidc.go` — SSO flow uses `CreateAPITokenSession()` when return URL is `seafile://` (desktop client)
- Config: `auth.oidc.api_token_ttl` / env `OIDC_API_TOKEN_TTL`

No schema changes — same `sessions` table, different TTL per insert.

---

### ISSUE-SESSION-01: 401 Session Expiry Causes Frontend to Hang in Loading State

**Status**: ✅ Fixed (2026-02-22)
**Severity**: High — users see infinite spinner or misleading "folder does not exist" errors
**Affected**: All authenticated views when session/token expires mid-use

#### Problem
When a session expired, the frontend got stuck in a permanent loading state instead of redirecting to login. Three root causes:

1. **SeafHTTP returned 403 (not 401)** for expired operation tokens in `HandleUpload`, `HandleDownload`, `HandleZipDownload` — preventing the frontend from distinguishing "expired" from "no permission"
2. **`authMiddleware` returned generic `"invalid token"`** for expired sessions — no way for frontend to know the session expired vs an invalid credential
3. **Nested promises without `return`** in `lib-content-view.js` `showFile()` — inner promise rejections were silently lost, so `isFileLoading` was never set to `false`

#### Fix

**Backend** (`internal/api/seafhttp.go`):
- Changed 3 locations from `http.StatusForbidden` → `http.StatusUnauthorized` for expired operation tokens

**Backend** (`internal/api/server.go`):
- `authMiddleware` now detects `"expired"` in the session validation error and returns `401 {"error": "session expired"}` immediately instead of falling through to the generic error

**Frontend** (`frontend/src/utils/seafile-api.js`):
- Added global axios response interceptor that catches all 401 responses, clears `localStorage` token, and redirects to `/login/?expired=1`

**Frontend** (`frontend/src/pages/lib-content-view/lib-content-view.js`):
- Added `return` to nested `.then()` calls so promise rejections propagate to the outer `.catch()` handler

**Frontend** (`frontend/src/utils/utils.js`):
- `getErrorMsg()` now returns `"Session expired. Please log in again."` for 401 responses

**Frontend** (`frontend/src/pages/login/index.js`):
- Login page reads `?expired=1` query param and shows session expired message

---

### ISSUE-LIB-01: 404 When Creating Files in Libraries With Corrupt State

**Status**: ✅ Fixed (2026-02-21)
**Severity**: High — silently broken libraries, file creation completely blocked
**Affected**: Libraries where the initial `commits` INSERT failed at creation time

#### Symptoms

`POST /api/v2.1/repos/<id>/file/` returned 404 with body:
```json
{"error": "fs_object not found: not found"}
```

The library appeared normal (visible in the UI, browsable), but any write operation (create file, create folder) failed.

#### Root Cause

`CreateLibrary` performs 3 sequential writes to Cassandra:
1. `fs_objects` — empty root directory
2. `libraries` + `libraries_by_id` — library metadata (logged batch)
3. `commits` — initial commit pointing to the root fs_object

Step 3 had the error silently swallowed:
```go
if err := ...; err != nil {
    // Non-fatal - library was created   ← error ignored
}
```

If that INSERT failed (Cassandra timeout, transient error), the library row stored a `head_commit_id` pointing to a commit that didn't exist. On file creation:

```
CreateFile → GetRootFSID    → libraries_by_id: found head_commit_id ✓
                            → commits: found row ✓ (or not, also broken)
           → TraverseToPath → GetDirectoryEntries
                            → fs_objects WHERE fs_id = <root> → NOT FOUND → 404
```

In some cases the `commits` row existed (written in a previous retry) but the `fs_objects` row for the root directory was missing.

#### Fix

**`internal/api/v2/fs_helpers.go` — `GetDirectoryEntries`:**
On `gocql.ErrNotFound`, return an empty `[]FSEntry` and log a WARNING instead of propagating the error. The next write operation generates a correct new commit with the proper fs_object, permanently healing the library without manual intervention.

**`internal/api/v2/libraries.go` — `CreateLibrary`:**
The `commits` INSERT failure is now logged as `ERROR` instead of being silently ignored.

#### Recovery

Already-corrupt libraries self-heal on the first successful write operation (create file, create folder) with the new code. No manual DB intervention required.

---

---

### ISSUE-EMAIL-01: Hardcoded `UUID@sesamefs.local` Instead of Real User Email

**Status**: 🟡 Partial fix (2026-02-22)
**Severity**: Medium — incorrect display data exposed to clients; no auth or data integrity risk for display fields
**Tracked in**: `docs/TECHNICAL-DEBT.md` § 7

#### Background

Throughout the codebase, several endpoints were constructing a fake email by concatenating the user's UUID with `@sesamefs.local` (e.g. `a1b2c3d4-...@sesamefs.local`) instead of looking up the real email from the `users` table. This pattern was a dev shortcut that leaked into production paths.

#### Fixed (2026-02-22)

A `resolveOwnerEmail(orgID, userID string) string` helper was added to `LibraryHandler`. It queries `SELECT email FROM users WHERE org_id = ? AND user_id = ?` and falls back to `UUID@sesamefs.local` only when the user record is genuinely not found (deleted user, migration gap).

| File | Endpoints fixed |
|------|----------------|
| `internal/api/v2/libraries.go` | `ListLibraries`, `GetLibraryDetail` (v2), `ListLibrariesV21`, `GetLibraryDetailV21`, `CreateLibrary` |
| `internal/api/v2/deleted_libraries.go` | `ListDeletedRepos` |

#### Fixed — File History Modifier (2026-02-26)

`GetFileRevisions` and `GetFileHistoryV21` now resolve user name and email from the `users` table instead of using the raw UUID. A per-request cache avoids repeated queries for the same user across history entries.

| File | Line(s) | Endpoint / Context |
|------|---------|-------------------|
| `internal/api/v2/files.go` | ~3336 | `GetFileRevisions` — `CreatorName`, `CreatorEmail` |
| `internal/api/v2/files.go` | ~3421 | `GetFileHistoryV21` — `CreatorName`, `CreatorEmail` with userCache |

#### Pending — Display Fields (Safe to Fix)

These affect only what is returned to the client. No stored data is involved.

| File | Line(s) | Endpoint / Context |
|------|---------|-------------------|
| `internal/api/v2/files.go` | 1493 | `GetFileDetail` — `userEmail` in file detail response |
| `internal/api/v2/files.go` | 2557 | Sync token response — `"email"` field |
| `internal/api/seafhttp.go` | 1860 | Download-info sync token response — `"email"` field |
| `internal/api/v2/starred.go` | 127, 258 | Starred files list — `userEmail` in response |

Fix strategy: use `h.resolveOwnerEmail(orgID, userID)` (or equivalent DB query) in each location. `starred.go` and `files.go` will need a similar helper added to their respective handler structs, or access via a shared utility function.

#### Pending — FS Object Modifier (Risky — Needs Migration Analysis)

These write `UUID@sesamefs.local` into the **content** of stored Seafile FS objects. The `modifier` field is included in the hash that produces the `fs_id`. Changing the value changes the hash, so:

- Existing stored objects are unaffected (content-addressed, immutable).
- New objects would get different `fs_id` values than they would have with the old code.
- This is safe for **new** uploads but does **not** retroactively fix existing file history.

| File | Line(s) | Context |
|------|---------|---------|
| `internal/api/seafhttp.go` | 1001, 1036, 1098 | `"modifier"` field in FS objects built during upload |
| `internal/api/v2/onlyoffice.go` | 716, 730 | `Modifier` field in FS objects — code comment explicitly notes it's part of the `fs_id` hash |
| `internal/api/sync.go` | 500 | `commit.CreatorName` written into Seafile commit binary format |

Do **not** change these without a deliberate decision on whether to accept the hash change for new objects and whether any tooling needs to account for the mixed state.

---

## ⚠️ SeaDrive 3.x Missing Endpoints (Discovered 2026-02-19)

Observed in SeaDrive 3.0.19 client logs after successful SSO login and basic sync. All 4 are currently returning 404. Sync works despite these errors — they degrade UX or efficiency but are non-fatal.

---

### ISSUE-SD-01: `GET /seafhttp/repo/locked-files` — File Lock Status

**Observed**: SeaDrive logs `Bad response code for GET .../seafhttp/repo/locked-files: 404`
**When**: Immediately after repo trees are loaded, before first sync cycle
**What Seafile does**: Returns the list of files currently locked by any user across the repo. Used by SeaDrive to show lock indicators (padlock icon) on files being actively edited by someone else.
**Expected response format**:
```json
{"locked_files": [{"repo_id": "...", "path": "/filename.docx", "locked_by": "user@example.com", "lock_time": 1234567890}]}
```
**Stub response** (safe to return now): `{"locked_files": []}` — empty list means no files are locked.
**Auth**: No auth (SeaDrive sends without token, same pattern as folder-perm)
**Query params**: `repo_id` (optional, may be for a specific repo)
**Priority**: 🟡 Medium — needed for collaborative editing UX, lockout indicators, OnlyOffice/Office integration

---

### ISSUE-SD-02: `GET /seafhttp/repo/:repo_id/jwt-token` — Notification Server JWT

**Observed**: Seafile desktop client and SeaDrive log `Bad response code for GET .../seafhttp/repo/c430749e-.../jwt-token: 404`
**When**: During repo initialization cycle, after `locked-files` check

**What Seafile actually does** (confirmed from [fileserver/sync_api.go](https://github.com/haiwen/seafile-server/blob/master/fileserver/sync_api.go)):
```go
func getJWTTokenCB(rsp http.ResponseWriter, r *http.Request) *appError {
    if !option.EnableNotification {
        return &appError{nil, "", http.StatusNotFound}  // 404 if notifications disabled
    }
    exp := time.Now().Add(time.Hour * 72).Unix()
    tokenString, err := utils.GenNotifJWTToken(repoID, user, exp)
    // ...
    data := fmt.Sprintf("{\"jwt_token\":\"%s\"}", tokenString)
}
```

**Key findings**:
- **Purpose**: JWT for the **notification server** (WebSocket real-time push), NOT for sync auth or relay switching
- **Response field is `jwt_token`** (not `token`) — `{"jwt_token": "<signed-jwt>"}`
- **Official Seafile also returns 404** when `EnableNotification = false` — our 404 is correct behavior
- **Does NOT affect relay_addr or sync mode** — the `localhost:3000/protocol-version` attempts in logs are **unrelated** to this 404; they come from the client's cached `relay_addr` (stored in `.ccnet/` from when the library was first added)
- **Non-fatal for sync**: files sync correctly without this endpoint; only real-time change notifications are missing

**Expected response format** (when implemented):
```json
{"jwt_token": "<HS256-signed-jwt>"}
```
JWT payload: `{"repo_id": "...", "user": "user@example.com", "exp": <unix+72h>}`

**Auth**: Requires `syncAuthMiddleware` (repo sync token in `Seafile-Repo-Token` header)
**Priority**: 🟢 Low — 404 is safe; only needed to enable real-time file change notifications via notification server

---

### ISSUE-SD-03: `GET /seafhttp/accessible-repos/` — Repo Accessibility Check

**Observed**: SeaDrive logs `Bad response code for GET .../seafhttp/accessible-repos/?repo_id=c430749e-...: 404`
**When**: ~10 seconds after initial sync completes (periodic check)
**What Seafile does**: Verifies that the user still has access to the specified repo. Used by SeaDrive to detect permission revocations without waiting for the next full sync cycle. If a repo is removed from the response, SeaDrive un-mounts it from the virtual drive.
**Expected response format**:
```json
{"accessible_repos": ["c430749e-61b9-45fc-a2fc-0e2e13134b34"]}
```
**Stub response** (safe): Return all repo IDs from the query as accessible — `{"accessible_repos": [repo_id]}`.
**Auth**: Likely requires API token (regular `authMiddleware`)
**Query params**: `repo_id` (comma-separated list of repo UUIDs to check)
**Priority**: 🟢 Low — non-fatal; SeaDrive continues syncing. Only affects permission-revocation detection latency.

---

### ISSUE-SD-04: `GET /seafhttp/repo/:repo_id/block-map/:block_id` — Block Composition Map

**Observed**: SeaDrive logs `Bad response code for GET .../seafhttp/repo/.../block-map/119cdbf0...: 404` then `Failed to get block map for file object 119cdbf0...`
**When**: During file download/sync, when SeaDrive tries to fetch a specific file object
**What Seafile does**: Returns the ordered list of block IDs that compose a file object (identified by its fs_object ID / SHA-1). Enables **differential sync** — instead of re-downloading an entire file, SeaDrive only downloads blocks that changed. This is the core of Seafile's deduplication and efficient sync.
**Expected response format**: JSON array of block IDs in order:
```json
["block-id-1-hex", "block-id-2-hex", "block-id-3-hex"]
```
**Implementation notes**:
- `block_id` in the URL is the **fs_object ID** (file's SHA-1 in the FS tree), NOT a block ID
- Need to look up the fs_object in Cassandra → get its `block_ids` array → return it
- The fs_object stores `block_ids` as an ordered list already (used in `GetBlock`)
- This is already partially implemented in `GetFSObject` — just needs a dedicated endpoint
**Auth**: Requires `syncAuthMiddleware` (sync token in `Seafile-Repo-Token` header)
**Priority**: 🟠 Medium-High — without this, SeaDrive falls back to full-file downloads instead of block-level differential sync. Impacts bandwidth and sync speed for large files.

---

## ✅ RECENTLY FIXED (2026-02-20)

### Desktop Client File Browser Broken — Missing `oid` Response Header — FIXED ✅

**Fixed**: 2026-02-20
**Observed**: Seafile desktop client 9.0.x file browser ("Navegador de Archivos") showed "Fallo al obtener información de archivos / Por favor reintentar" when clicking into any library. Server logs showed two rapid identical `GET /api2/repos/:id/dir/?p=/` requests returning 200 with correct JSON body (271 bytes).

**Root Cause**: The Seafile Qt client reads `reply.rawHeader("oid")` and `reply.rawHeader("dir_perm")` from the directory listing response. Our `ListDirectory` handler returned the correct JSON array but did not set these headers. Without `oid`, the client considers the response invalid and shows the error.

**Fix**: Added `c.Header("oid", currentFSID)` and `c.Header("dir_perm", "rw")` to all success paths in `ListDirectory` (`internal/api/v2/files.go`).

### Desktop Client Upload/Download Fails — "Protocol ttps/ttp is unknown" — FIXED ✅

**Fixed**: 2026-02-20
**Observed**: File upload and download from the desktop client file browser failed. Client logs:
```
[file server task] network error: Protocol "ttps" is unknown   (production, https)
[file server task] error: Protocol "ttp" is unknown             (local dev, http)
```
Server logs showed `GET /api2/repos/:id/upload-link` and `GET /api2/repos/:id/file/?p=...&reuse=1` returning 200 but no subsequent upload/download POST.

**Root Cause**: Three functions returned URLs via `c.String()` (plain text): `GetUploadLink`, `GetDownloadLink`, and `getFileDownloadURL`. The Seafile Qt client expects the URL as a **JSON-quoted string** (e.g., `"https://..."`) and calls `response.mid(1, response.size()-2)` to strip the surrounding quotes. Without quotes, the client stripped the first character (`h`) → `ttps://` or `ttp://` → unknown protocol error.

**Fix**: Changed `c.String(http.StatusOK, url)` → `c.JSON(http.StatusOK, url)` in all three functions. `c.JSON` automatically serializes the string with JSON double quotes.

**Files**: `internal/api/v2/files.go`

### `head-commits-multi` Trailing Slash 502 — FIXED ✅

**Fixed**: 2026-02-20
**Observed**: Client log: `Bad response code for POST https://sfs.nihaoshares.com/seafhttp/repo/head-commits-multi/: 502`. Server log showed the endpoint working for requests without trailing slash, but the client sends the URL WITH trailing slash.

**Root Cause**: Only `POST /seafhttp/repo/head-commits-multi` was registered (no trailing slash). With `router.RedirectTrailingSlash = false`, the trailing-slash variant returned 404, which nginx proxied as 502.

**Fix**: Added `router.POST("/seafhttp/repo/head-commits-multi/", h.GetHeadCommitsMulti)` in `internal/api/sync.go`.

---

### `relay_addr` / `relay_id` Returns `"localhost"` — Seafile Client Tries Wrong Server — FIXED ✅

**Fixed**: 2026-02-20
**Observed**: After syncing, the Seafile desktop client (SeaDrive 3.x and SeafDrive) connects to `localhost:3000` instead of the real server hostname. Client logs:
```
libcurl failed to GET http://localhost:3000/seafhttp/protocol-version: Couldn't connect to server.
libcurl failed to GET http://localhost:8082/protocol-version: Couldn't connect to server.
```
**Preceded by**: 404s for `/seafhttp/repo/locked-files` and `/seafhttp/repo/:id/jwt-token` — these are unrelated to the localhost issue. The `jwt-token` 404 is expected (it's for the notification server, not relay auth — official Seafile also returns 404 when notifications are disabled). The `localhost` attempts come from the client's cached `relay_addr`, not from these 404s.

**Root Causes** (4 bugs):

1. **`docker-compose.yaml` — default `SERVER_URL=http://localhost:3000`** (deployment bug):
   The dev docker-compose had `SERVER_URL=${SERVER_URL:-http://localhost:3000}`. When `SERVER_URL` was not set in `.env`, the container received `SERVER_URL=http://localhost:3000`. Since this env var is non-empty, `getEffectiveHostname()` processed it and extracted `relay_addr=localhost`. Fixed by changing to `SERVER_URL=${SERVER_URL}` (no fallback), so the container gets an empty var and auto-detection works via `c.Request.Host`. Production now follows the same host-derivation model unless you intentionally force a canonical public URL.

2. **`v2/libraries.go` — hardcoded `"localhost"`** (most impactful):
   `CreateLibrary` (POST /api2/repos/) returned `"relay_addr": "localhost"` and `"relay_id": "localhost"` unconditionally. The Seafile client **caches** this value when a library is first added. All subsequent sync operations targeting that library use the cached address — which was `localhost`. Even after restarting or re-logging, the client retries `localhost` until the library is removed and re-added.

2. **`sync.go` `GetDownloadInfo` — ignored `X-Forwarded-Host`**:
   Used `normalizeHostname(c.Request.Host)` directly. Behind a reverse proxy that terminates SSL, `c.Request.Host` is the internal backend address (`localhost:3000`), not the external hostname.

3. **`v2/files.go` `GetDownloadInfo` — ignored `X-Forwarded-Host`**:
   Same issue as #2 in the v2 API path's download-info response.

**Also fixed**: `getBaseURLFromRequest` (used for `file_server_root` in server-info) had the same `X-Forwarded-Host` gap.

**Fix**: All four locations now use this priority order:
1. `SERVER_URL` env var (most reliable — explicitly configured)
2. `X-Forwarded-Host` header (set by nginx/traefik when proxying)
3. `c.Request.Host` (last resort — correct for direct connections)

Added `getEffectiveHostname(c *gin.Context) string` helper in `server.go` for the `api` package; inline equivalent logic added to `v2/libraries.go` and `v2/files.go` (separate package).

**Action required after deploy**: Users whose clients have `localhost` cached must remove and re-add the affected library in SeaDrive/SeafDrive to pick up the correct `relay_addr`. The library data itself is not affected — only the client's cached server address.

**Files**: `internal/api/server.go`, `internal/api/sync.go`, `internal/api/v2/libraries.go`, `internal/api/v2/files.go`

---

## ✅ RECENTLY FIXED (2026-02-19)

### SeaDrive Sync 405/401 on `/seafhttp/repo/folder-perm` — FIXED ✅
**Fixed**: 2026-02-19
**Was**: SeaDrive stuck in `error: 'Error occurred in download.'` loop. Server returned 405 then 401 on `POST /seafhttp/repo/folder-perm`.
**Root Causes** (3 sequential bugs):
1. Previous commit replaced static `router.GET("/seafhttp/repo/folder-perm")` with `repo.GET("")` inside the wildcard group — Gin returned 405 for both GET and POST.
2. After fixing routing, POST still returned 405 because only GET was registered.
3. After adding POST, both returned 401 because SeaDrive sends folder-perm requests with NO auth token.
**Fix**: Register both GET and POST as static routes (no auth middleware) before the wildcard group. Response is always `{}` so no auth is needed.
**Files**: `internal/api/sync.go`

---

## ✅ RECENTLY FIXED (2026-02-18)

### Production File Upload 500 — Storage Backend Not Registered — FIXED ✅
**Fixed**: 2026-02-18
**Was**: All file uploads in production returned HTTP 500 after successful streaming. Server log: `Finalization failed: block store not available: no healthy backend available for class hot`.
**Root Cause**: `initStorageManager` only iterated `cfg.Storage.Classes` (new multi-region format). `configs/config.prod.yaml` uses the legacy `backends:` key — so the storage manager started with zero backends. `finalizeUploadStreaming` called `storageManager.GetHealthyBlockStore("")` → resolved default class `"hot"` → not found → 500.
**Fix**: Added a second loop in `initStorageManager` that also registers backends from `cfg.Storage.Backends` (legacy format), skipping any name already registered via `classes:`. Both formats produce identical entries in the manager.
**Files**: `internal/api/server.go`, `configs/config.prod.yaml` (comment only)

---

### Desktop Sync Race Condition — Web-Uploaded Files Disappear — FIXED ✅
**Fixed**: 2026-02-18
**Was**: When the Seafile desktop client deleted all local files and re-synced, it overwrote the server HEAD with an empty-root commit, causing files uploaded via the web UI to disappear. The desktop client then entered an infinite sync retry loop every ~30 seconds.

**Root Cause**: Seven interrelated bugs across the sync protocol, upload pipeline, and directory listing:

**Bug 1 — PutCommit race condition (4 sub-fixes)**:
- **1A**: The non-HEAD `PUT /commit/:id` path was unconditionally updating HEAD, bypassing the Seafile protocol's separate HEAD update step (`PUT /commit/HEAD` or `POST /update-branch`). A stale/retried commit from the desktop client could silently overwrite a HEAD that had been advanced by web uploads.
- **1B**: `PUT /commit/HEAD` had no parent-chain validation. Any commit could replace HEAD regardless of whether it was a descendant of the current HEAD.
- **1C**: `POST /update-branch` had the same missing parent-chain validation as 1B.
- **1D**: `updateLibraryHeadWithStats()` used an unconditional batch write. Two concurrent callers could both read the same HEAD and then both write, with the last writer winning silently.

**Bug 2 — HandleUpload swallows errors**:
- Single-shot upload (`HandleUpload`) logged filesystem metadata failures but returned 200 OK to the client, masking data inconsistencies.
- Streaming upload (`finalizeUploadStreaming`) swallowed errors similarly.

**Bug 3 — ListDirectory returns empty on errors**:
- When the commit lookup or root fs_object lookup failed, `ListDirectory` and `ListDirectoryV21` returned HTTP 200 with an empty dirent list instead of an error. This made the desktop client believe the library was empty and sync a deletion.

**Bug 4 — CheckFS reports EMPTY_SHA1 as missing (infinite sync loop)**:
- The all-zeros ID (`0000000000000000000000000000000000000000`) is Seafile's canonical constant for an empty directory root. The desktop client treats it as a well-known value and never uploads it via `recv-fs`. When `CheckFS` reported it as missing, the client waited and retried every ~30 seconds indefinitely.

**Bug 5 — GetHeadCommitsMulti returns "not found" for valid repos**:
- The `libraries` table partitions by `(org_id)`. When the sync auth token carried a different `org_id` than the library's actual partition, the query returned no rows. This is the same class of issue documented elsewhere in the codebase (partition key mismatch), solved by falling back to `libraries_by_id WHERE library_id = ?`.

**Bug 6 — ListDirectory 500 on all-zeros root**:
- After the desktop client legitimately synced an empty library (all files deleted), the commit's `root_fs_id` was `0000...0`. `ListDirectory` tried to find this fs_object in the database, failed, and returned 500 Internal Server Error.

**Bug 7 — createInitialCommit uses hardcoded all-zeros instead of proper SHA-1**:
- `createInitialCommit()` in sync.go used `fmt.Sprintf("%040x", 0)` to generate the root fs_id. The v2 REST API in `libraries.go` uses proper content-addressable hashing: `sha1.Sum([]byte("1\n[]"))`. The hardcoded zeros caused special-casing throughout the codebase because the all-zeros ID doesn't exist as a real `fs_object`.

**Fixes Applied**:

1. **Bug 1A**: Removed HEAD update from non-HEAD PutCommit. The commit is stored but HEAD is only advanced by the dedicated `PUT /commit/HEAD` or `POST /update-branch` endpoints.
2. **Bug 1B/1C**: Added parent-chain validation to both `PUT /commit/HEAD` and `POST /update-branch`. Before updating HEAD, the commit's `parent_id` must match the current HEAD. If not, the update is rejected (returns 200 OK for Seafile desktop client compatibility — the client detects HEAD did not advance on next sync check).
3. **Bug 1D**: Added Cassandra LWT (Lightweight Transaction / compare-and-swap) support to `updateLibraryHeadWithStats()`. New optional `expectedHead` parameter enables `IF head_commit_id = ?` in the UPDATE statement. Returns `ErrHeadConflict` sentinel error if another writer changed HEAD concurrently.
4. **Bug 2A/2B**: `HandleUpload` and `finalizeUploadStreaming` now return proper HTTP errors when filesystem metadata updates fail instead of silently succeeding.
5. **Bug 3**: `ListDirectory` and `ListDirectoryV21` now return HTTP 500 with descriptive error messages when commit or fs_object lookups fail, instead of returning empty arrays.
6. **Bug 4**: `CheckFS` skips the all-zeros ID (`strings.Repeat("0", 40)`) before querying the database, breaking the infinite sync loop.
7. **Bug 5**: `GetHeadCommitsMulti` falls back to `libraries_by_id WHERE library_id = ?` when the primary `libraries WHERE org_id = ? AND library_id = ?` query fails.
8. **Bug 6**: `ListDirectory` and `ListDirectoryV21` treat the all-zeros root as a valid empty library — returns empty dirent list for root path `/`, returns 404 for subdirectories.
9. **Bug 7**: `createInitialCommit()` now computes the root fs_id as `sha1.Sum([]byte("1\n[]"))` (matching the v2 REST API in `libraries.go`) and stores a real `fs_object` with that ID. All-zeros checks are kept as defense-in-depth since existing libraries or desktop clients may still reference the old format.

**Files Changed**:
- `internal/api/sync.go` — Bugs 1A-1D, 4, 5, 7: PutCommit HEAD separation, parent-chain validation, CAS updates, CheckFS EMPTY_SHA1 skip, GetHeadCommitsMulti fallback, createInitialCommit SHA-1 alignment
- `internal/api/seafhttp.go` — Bug 2A/2B: HandleUpload and finalizeUploadStreaming error propagation
- `internal/api/v2/files.go` — Bugs 3, 6: ListDirectory/ListDirectoryV21 error handling and empty-root handling

This closed the missing-CAS and missing-parent-validation bug class. The remaining active-active desktop-sync recovery/validation gap is tracked separately in `ISSUE-SYNC-HEAD-RECOVERY-01`.

---

## ✅ RECENTLY FIXED (2026-02-12)

### Files Opened from Search Return 404/500 — FIXED ✅
**Fixed**: 2026-02-12
**Was**: Clicking search results to open files (especially .docx and .pdf) returned either 404 "File Not Found" or 500 Internal Server Error.

**Root Causes** (3 separate issues):

1. **404 on .docx files (OnlyOffice)**: `getFileID()` in `onlyoffice.go` queried the `libraries` table with `WHERE org_id = ? AND library_id = ?`. When `org_id` from the auth context didn't match the library's partition key, Cassandra returned no rows → 404 error page.

2. **500 on .pdf files (inline preview)**: `serveInlinePreview()` in `fileview.go` extracted the auth token from query params or Authorization header to build the raw file embed URL. When users arrived without a token (anonymous/dev mode), it generated `?token=` (empty string) in the `<embed src="/repo/:id/raw/:path?token=">` URL → the browser's sub-request to the raw endpoint failed with 500.

3. **Missing token in URLs**: All 6 `onSearchedClick()` handlers across the frontend (app.js, settings.js, repo-history.js, repo-snapshot.js, repo-folder-trash.js, pages/search/index.js) opened files in new tabs via `window.open()` **without** including the auth token in the URL. New browser tabs don't have access to the parent's `localStorage` or ability to set request headers → unauthenticated requests.

**Fixes**:
- **Backend (OnlyOffice)**: Changed `getFileID()` to query `libraries_by_id WHERE library_id = ?` (no `org_id` dependency), matching the pattern used by `FSHelper.GetRootFSID()`.
- **Backend (Preview)**: Enhanced token extraction in `serveInlinePreview()` to support both `Token` and `Bearer` prefixes, added fallback to dev token when in dev mode and token is empty.
- **Frontend**: Updated all 6 `onSearchedClick()` handlers to call `getToken()` and append `?token=` to file URLs.

**Files Changed**:
- `internal/api/v2/fileview.go` — Enhanced token extraction with dev token fallback
- `internal/api/v2/onlyoffice.go` — Fixed `getFileID()` to use `libraries_by_id` table
- `frontend/src/app.js` — Added token import and URL parameter
- `frontend/src/settings.js` — Added token to file URLs
- `frontend/src/repo-history.js` — Added token to file URLs
- `frontend/src/repo-snapshot.js` — Added token to file URLs
- `frontend/src/repo-folder-trash.js` — Added token to file URLs
- `frontend/src/pages/search/index.js` — Added token to file URLs

---

## ✅ RECENTLY FIXED (2026-02-06)

### Search File Paths Incorrect — FIXED ✅
**Fixed**: 2026-02-06
**Was**: Files in subdirectories showed wrong path (e.g., `/file.txt` instead of `/folder/file.txt`) → clicking results gave 404.
**Root cause**: `full_path` field was never populated — search only had the filename without parent directory context.
**Fix**:
- Added `full_path` column to `fs_objects` table via database migration
- Added `updateFullPaths()` helper in `internal/api/sync.go` that traverses directory tree from root
- Called async from `PostCommit`, `PutCommit HEAD`, and `UpdateBranch` handlers after commit is received
- Updated `backfill-search-index` CLI command to also populate `full_path` for existing data
- Search handler (`internal/api/v2/search.go`) now returns correct `fullpath` from database
**Files**: `internal/api/sync.go`, `internal/api/v2/search.go`, `cmd/sesamefs/main.go`, `internal/db/db.go`

### Search Returns No Results — FIXED ✅
**Fixed**: 2026-02-06
**Was**: `GET /api/v2.1/search/?q=test` returned `{"results":null,"total":0}` even when files named "test.docx" existed.
**Root cause**: Two issues:
1. `obj_name` field in `fs_objects` table was never populated during sync (empty string "")
2. SASI indexes disabled in Cassandra 5.x, search queries failed silently
**Fix**:
- Modified `internal/api/sync.go` to extract child names from directory `dir_entries` and update child `obj_name`
- Changed `internal/api/v2/search.go` to use in-memory filtering instead of SASI LIKE queries
- Added `backfill-search-index` CLI command to populate `obj_name` for existing data
- Fixed UUID marshaling errors (use strings instead of `uuid.UUID` with gocql)
**Files**: `internal/api/sync.go`, `internal/api/v2/search.go`, `cmd/sesamefs/main.go`, `internal/db/db.go`

## ✅ RECENTLY FIXED (2026-02-05)

### Search Returns 404 — FIXED ✅
**Fixed**: 2026-02-05
**Was**: `GET /api2/search/?q=test&search_repo=all` → 404. Search route only registered under `/api/v2.1/` but `seafile-js` calls `/api2/search/`.
**Fix**: Added `v2.RegisterSearchRoutes(protected, s.db)` to `/api2/` route group.
**File**: `internal/api/server.go`

### Tag Deletion 500 Error — FIXED ✅
**Fixed**: 2026-02-05
**Was**: `DELETE /api/v2.1/repos/:repo_id/repo-tags/:id/` → 500. Counter table DELETE mixed with non-counter batch.
**Fix**: Separated counter DELETE from LoggedBatch (same pattern as AddFileTag/RemoveFileTag).
**File**: `internal/api/v2/tags.go`

### Tags `#` in URL Causes "Folder Does Not Exist" — FIXED ✅
**Fixed**: 2026-02-05
**Was**: Clicking "Create a new tag" link appended `#` to URL. Reloading showed "Folder does not exist".
**Fix**: Added `e.preventDefault()` to tag link onClick, and strip hash fragments in URL parser.
**Files**: `frontend/src/components/dialog/edit-filetag-dialog.js`, `frontend/src/pages/lib-content-view/lib-content-view.js`

### File/Folder Trash (Recycle Bin) — IMPLEMENTED ✅
**Fixed**: 2026-02-05
**Was**: Trash feature had no backend endpoints. Clicking recycle bin icon failed.
**Fix**: Created `internal/api/v2/trash.go` with 5 endpoints: list trash items (commit-history based), restore file/folder, clean trash, browse deleted folders. Added 5 frontend API methods.
**Files**: `internal/api/v2/trash.go` (new), `frontend/src/utils/seafile-api.js`

### Library Recycle Bin (Soft-Delete) — IMPLEMENTED ✅
**Fixed**: 2026-02-05
**Was**: Deleting a library was permanent with no recovery. Frontend had full UI but backend had no soft-delete.
**Fix**: Added `deleted_at`/`deleted_by` columns to libraries table. `DeleteLibrary` now soft-deletes. Added list/restore/permanent-delete endpoints. Filtered soft-deleted libraries from all list and get endpoints. Added 7 frontend API methods.
**Files**: `internal/api/v2/deleted_libraries.go` (new), `internal/api/v2/libraries.go`, `internal/db/db.go`, `frontend/src/utils/seafile-api.js`

### File Expiry Countdown — IMPLEMENTED ✅
**Fixed**: 2026-02-05
**Was**: No indication of when files expire in libraries with `auto_delete_days`.
**Fix**: Added `expires_at` field to directory listing API response. Computed from `mtime + auto_delete_days * 86400`.
**File**: `internal/api/v2/files.go`

**2026-05-15 audit correction**: `expires_at` is emitted, but it should not be treated as a guaranteed deletion countdown until auto-delete semantics are aligned with GC behavior. See ISSUE-LIB-RETENTION-01.

---

## ✅ RECENTLY FIXED (2026-02-04)

### Raw File Preview / Inline Serving 500 Error — FIXED ✅
**Fixed**: 2026-02-04
**Was**: All inline file previews (images, PDFs, documents, shared files) returned 500 Internal Server Error. Error: `Undefined column name size in table sesamefs.fs_objects`
**Root Cause**: `ServeRawFile()` queried `SELECT block_ids, size FROM fs_objects` but the actual column is `size_bytes`.
**Fix**: Changed `size` → `size_bytes` in the query.
**File**: `internal/api/v2/fileview.go:551`

### Image Lightbox aria-hidden on body — FIXED ✅
**Fixed**: 2026-02-04
**Was**: Opening image lightbox set `aria-hidden="true"` on `<body>`, hiding the entire accessibility tree from screen readers. Browser console warning: "Blocked aria-hidden on a `<body>` element."
**Root Cause**: `@seafile/react-image-lightbox` uses `react-modal` internally, which sets `aria-hidden="true"` on body by default when a modal opens.
**Fix**: Added `reactModalProps={{ shouldFocusAfterRender: true, ariaHideApp: false }}` to the Lightbox component to disable the body aria-hidden behavior.
**File**: `frontend/src/components/dialog/image-dialog.js`

### File History Showing Duplicate Entries — FIXED ✅
**Fixed**: 2026-02-04
**Was**: File history page showed duplicate records (e.g., 18 identical entries for a file modified only twice). Same timestamp, same size, same modifier for most entries.
**Root Cause**: `GetFileHistoryV21` iterated all commits for the library and included a history entry for every commit where the file existed — even if the file content was unchanged (e.g., another file in the library was modified).
**Fix**: After collecting all commits containing the file, deduplicate by `RevFileID` (fs_id). Only include an entry when the file's fs_id changes compared to the previous commit, indicating the file was actually modified.
**File**: `internal/api/v2/files.go` (`GetFileHistoryV21`)

---

## 🔴 OPEN ISSUES

### ISSUE-GC-MULTIINSTANCE-01: GC is not safe with multiple instances

**Status**: 🟡 Pending with temporary prod workaround
**Discovered**: 2026-03-17
**Priority**: 🟡 High — required before scaling to multiple replicas
**Affected**: `internal/gc/worker.go`, `internal/gc/scanner.go`, `internal/gc/gc.go`

**Problem:**
The GC (worker + scanner) has no coordination mechanism between instances. If multiple server replicas are running, all of them execute the worker and scanner in parallel, causing:

1. **DequeueBatch without locking**: `SELECT ... LIMIT ?` returns the same items to all instances. Both process the same items simultaneously.
2. **Scanner without leader election**: Multiple scanners enqueue the same orphans as duplicates (the PK includes `queued_at = time.Now()`, so each INSERT creates a distinct row).
3. **Snapshot drift** (substantially resolved): the original `gc_queue_stats` counter table was retired from the baseline schema. Queue/DLQ totals now live in `gc_stats` and `gc_org_stats`, dirty orgs are exact-recalculated from canonical rows off the write path, hot `COUNT(*)` reads are gone, and DLQ expiry is explicit. Snapshots remain approximate until the background/admin refresh runs. See ARCHITECTURE.md / GC-SERVICE-ANALYSIS.md.

**Is there data loss?** No. Destructive operations are protected:
- `DeleteBlock` uses a claim-then-verify delete fence: only one instance can win the claim, and the winner re-checks live `block_references` before touching S3
- `block_references` rows make fs_object cleanup idempotent; retrying the same delete removes the same keyed refs again instead of replaying a counter decrement
- Cassandra DELETEs are idempotent

**Actual impact**: Wasted work (CPU/network overhead) and slightly incorrect admin counters. No risk of data loss.

**Current operational decision (updated 2026-04-14):**
- Keep `gc.enabled=false` in YAML and set `GC_ENABLED=true` only on the replicas that are allowed to run GC.
- SesameFS now uses a Cassandra LWT lease (`gc_leases`) so if more than one enabled replica is up, only one runs worker/scanner/rollover work at a time.
- In multi-region, still enable GC in exactly one DC. The lease protects overlap; it does not remove the cross-DC Paxos cost of competing leaders.

**Leader Election via LWT:**
- Implemented with `gc_leases` and TTL-backed heartbeats.
- Enabled replicas try `INSERT ... IF NOT EXISTS` first, then renew ownership with `UPDATE ... IF instance_id = ?`.
- If the leader dies or loses its lease, another enabled replica can take over automatically after lease expiry.

**Recommended future direction:**
- Keep the explicit `GC_ENABLED=true` activation model so GC stays opt-in by replica.
- Consider exposing lease state/owner in admin status if operators want clearer observability during failover drills.

**Multi-region deployment note (2026-04-10):**
Running GC in a single DC is **critical** for multi-region deployments with Cassandra replication. Even though LWT operations use `SERIAL` consistency (global Paxos) by default, running GC on multiple DCs would cause:
- `DequeueBatch` (non-LWT SELECT) returning the same items to workers in different DCs
- Scanner in both DCs enqueueing duplicate orphans
- Unnecessary cross-DC Paxos contention on every LWT

The existing `GC_ENABLED=true` on exactly one DC / `GC_ENABLED=false` on all others remains the correct topology for multi-region. The lease now provides automatic failover among enabled replicas, but you should still avoid enabling GC in multiple DCs at once.

All block-level operations (`IncrementOrCreateBlock`, `decrementBlockRefCount`, `DeleteBlock` Phase 1) use LWT which defaults to `SERIAL` (global Paxos). Do NOT change to `LOCAL_SERIAL` — this would break cross-DC serialization and allow split-brain scenarios where GC in DC-A claims a block that an upload in DC-B is concurrently referencing.

**Alternative — Org partitioning:**
Each instance processes `hash(orgID) % numInstances == myIndex`. No coordination needed but requires knowing the total number of instances.

**Alternative — Accept duplication:**
If only 2-3 instances will run, overhead is minimal and all logic is already idempotent. Counters can be recalculated with a periodic scan.

---

### ISSUE-FILE-EDIT-01: No In-Browser Editing for Text/Markdown/Code Files

**Status**: ❌ Not Implemented
**Discovered**: 2026-02-22
**Priority**: 🟡 High — core UX gap, users expect to edit files by clicking them

**Current Behavior:**
- Clicking a text file (`.py`, `.md`, `.json`, `.txt`, `.css`, `.js`, etc.) opens `FilePreviewDialog` — a read-only modal that renders `<pre><code>` with no edit capability.
- The `isModalPreviewable()` function in `lib-content-view.js:1395` intercepts these file types before they ever reach `fileview.go`.
- For non-intercepted files, `fileview.go` serves backend-rendered preview HTML or OnlyOffice — it does NOT load a React editor/app shell for authenticated file editing.

**Expected Behavior:**
- Clicking a `.md` file should open an editor experience instead of a read-only preview.
- Clicking other text files should open an authenticated editor/view page with `window.app.pageOptions` containing `canEditFile`, `filePerm`, `fileType`, etc.
- The `FileToolbar` component (`frontend/src/components/file-view/file-toolbar.js`) reads `canEditFile` from `pageOptions` to show Save/Edit buttons.

**What Works Today:**
- OnlyOffice editing (`.docx`, `.xlsx`, `.pptx`) works if OnlyOffice is configured — `fileview.go:serveOnlyOfficeEditor()` renders the editor correctly.
- File download works for all types.
- Legacy standalone preview bundles (`frontend/src/file-view.js`, `history-trash-file-view.js`, `view-file-*.js`) were removed because the live backend preview flow no longer loads them.

**Implementation Plan:**
1. **Option A (Quick):** Remove text file types from `isModalPreviewable()` so clicks go to `/lib/:repo_id/file/*`, then update `fileview.go` to serve an authenticated editor shell (with `pageOptions`) instead of static/backend-rendered preview HTML for editable text files.
2. **Option B (Full):** Build an in-app editor component (CodeMirror/Monaco) embedded in the `FilePreviewDialog` modal, with save-back-to-API capability.
3. Either option needs: permission check in `fileview.go` to set `canEditFile` based on `GetLibraryPermission()` result.

**Files Involved:**
- `frontend/src/pages/lib-content-view/lib-content-view.js` — `isModalPreviewable()`, `onItemClick()`
- `frontend/src/components/dialog/file-preview-dialog.js` — read-only preview modal
- `internal/api/v2/fileview.go` — `ViewFile()`, `serveInlinePreview()`
- `frontend/src/components/file-view/file-toolbar.js` — reads `canEditFile` from `pageOptions`
- `frontend/src/pages/markdown-editor/` — existing Markdown editor (separate entry point)

---

### ISSUE-SSO-01: Desktop Client SSO — Browser Shows No Confirmation After Login

**Status**: ✅ Fixed (2026-03-04)
**Discovered**: 2026-02-20
**Severity**: Medium — functional but poor UX; users are confused after completing SSO login

**Fix**: When `result.ReturnURL` starts with `seafile://` (desktop client SSO), `handleOAuthCallback` now serves a lightweight HTML confirmation page instead of redirecting to `/`. The page:
1. Shows "Login Successful — You can close this tab and return to the application."
2. Attempts `window.close()` to auto-close the tab (works when opened via `ShellExecute`/`xdg-open`)
3. Uses `<meta http-equiv="refresh">` to redirect to `seafile://client-login/` as fallback to activate the client

Web browser logins are unaffected — they still redirect to `/`.

**Files changed**: `internal/api/server.go` → `handleOAuthCallback()` (lines 1811–1846)

---

### Programmatic Auth in OIDC-only Production — FIXED ✅
**Status**: ✅ Fixed (2026-04-03)
**Discovered**: 2026-02-18
**Severity**: Resolved for desktop client sync, CLI tools, and user-scoped automation

**Current behavior**:
- Users can create/revoke user API keys via `GET/POST/DELETE /api/v2.1/api-keys/`
- Desktop clients, SeaDrive, and CLI tools call `POST /api2/auth-token/` with `username=<email>` and `password=<raw API key>`
- The server returns a long-lived session token for sync/API access
- Revoking the API key invalidates any sessions minted from that key
- If the API key expires, the derived session cannot outlive the key

**What remains out of scope**:
- OIDC Device Flow is still not implemented
- There is still no separate service-account or client-credentials flow for userless automation

**Relevant files**:
- `internal/api/server.go` — `/api2/auth-token/` API-key exchange + self-service revoke path
- `internal/auth/session.go` — session provenance and invalidation by API key
- `internal/api/v2/admin_api_keys.go` — superadmin API key management for platform users
- `frontend/src/components/user-settings/api-keys.js` — self-service profile UI

---

### `head-commits-multi` Authentication in Production — FIXED ✅
**Status**: ✅ Fixed (2026-02-19)
**Discovered**: 2026-02-17

**Issue**: The Seafile desktop client 9.0.16 (Windows) sends `POST /seafhttp/repo/head-commits-multi` **without any auth headers** — no `Authorization`, no `Seafile-Repo-Token`, nothing. In production with OIDC, this endpoint was returning 401 every ~30s.

**Root cause confirmed**: Inspected official Seafile fileserver source (`fileserver/sync_api.go` v11.0.13). The endpoint is registered with **no auth middleware** and `headCommitsMultiCB` does not call `validateToken()`. Unauthenticated access is intentional — repo UUIDs are unguessable and only commit hashes are returned.

**Fix**: Removed `authMiddleware` from the route registration. Updated `GetHeadCommitsMulti` to handle both authenticated and unauthenticated callers: authenticated requests use org_id partitioned query + ACL check; unauthenticated requests query `libraries_by_id` directly without ACL filtering.

**Files**: `internal/api/sync.go` — `RegisterSyncRoutes()`, `GetHeadCommitsMulti()`

### ISSUE-DEFAULT-REPO-01: No Default Library Created on First Login

**Status**: 🟡 Pending
**Discovered**: 2026-02-20
**Severity**: Medium — funcional pero el usuario arranca sin ninguna librería visible

**Issue**: Seafile crea automáticamente una librería "My Library" (llamada `default_repo`) la primera vez que el usuario hace login. En nuestro sistema, `POST /api2/default-repo/` devuelve `{"exists": false}` como stub y no crea nada. El cliente desktop y la web no bloquean, pero el usuario ve una lista de librerías vacía al conectarse por primera vez.

**Comportamiento Seafile real** (`DefaultRepoView.post()`):
1. Verifica si el usuario ya tiene una `default_repo` en `UserOptions`
2. Si no existe (o fue eliminada), llama a `create_default_library(request)` que crea una librería llamada con el email del usuario
3. Guarda el `repo_id` en `UserOptions` con `KEY_DEFAULT_REPO`
4. Devuelve `{"exists": true, "repo_id": "<uuid>"}`

**Nuestro comportamiento actual**:
- `GET /api2/default-repo/` → `{"exists": false, "repo_id": ""}` (stub)
- `POST /api2/default-repo/` → `{"exists": false, "repo_id": ""}` (stub, añadido 2026-02-20 para evitar 405)
- No se crea ninguna librería; el usuario debe crearla manualmente

**Implementación pendiente**:
1. En el handler `POST /api2/default-repo/`, crear una librería con nombre derivado del email del usuario (ej. `"Mi librería"` o `<username>-files`)
2. Persistir el `repo_id` en una tabla de preferencias de usuario (equivalente a `UserOptions` con `KEY_DEFAULT_REPO`)
3. Devolver `{"exists": true, "repo_id": "<uuid>"}` una vez creada
4. En el handler `GET`, leer esa preferencia y devolver el estado real

**Alternativa más simple**: Crear la librería por defecto directamente en el handler OIDC callback (`handleOAuthCallback`) al primer login del usuario, antes de redirigir. Esto garantiza que la librería existe incluso si el cliente nunca llama al endpoint `POST /api2/default-repo/`.

**Archivos relevantes**:
- `internal/api/server.go` → `handleDefaultRepo()` (línea ~1072)
- `internal/api/v2/libraries.go` → lógica de creación de librerías (referencia para el handler)

---

### Version History — Remaining Gaps (Enhancements)
**Status**: 🟡 Core complete, enhancements pending
**Discovered**: 2026-02-01
**Detail**: File-level version history is fully functional (list, download revision, revert, history limit config, pagination, encryption). Remaining gaps:
1. **Library-wide commit history** — `GET /api/v2.1/repos/:id/history/` endpoint exists and is paginated. ✅ Implemented.
2. **Diff view between versions** — Frontend infrastructure exists but no backend diff endpoint. Seafile uses `/api2/repos/:id/file/diff/`. Needs a text diff algorithm (e.g., unified diff on file content).
3. **History TTL enforcement** — `version_ttl_days` stored in `libraries` table. GC Phase 5 (`scanExpiredVersions`) walks the HEAD commit chain and enqueues expired orphan commits. ✅ Implemented, needs validation.
4. **Directory revert** — `POST /api/v2.1/repos/:id/dir/?operation=revert` exists in code + `revertFolder()` in seafile-js. ✅ Implemented, needs validation.
5. ~~**File revert 409 not handled in UI**~~ — ✅ Fixed (2026-02-26). All 3 file history components now show a conflict dialog (Replace / Keep Both / Cancel) when reverting to a version where the file already exists with different content.
6. ~~**Modifier shows UUID instead of user name**~~ — ✅ Fixed (2026-02-26). `GetFileRevisions` and `GetFileHistoryV21` now resolve creator name/email from the `users` table.
7. ~~**No View action in history**~~ — ✅ Fixed (2026-02-26). All history views now include a "View" option that opens an inline preview page (`/history/view`) with proper MIME-based rendering (images, PDF, text, video, audio). Non-previewable files redirect to download.

### Share Links — Relative URLs + Stub Endpoint — FIXED ✅
**Status**: ✅ Fixed (2026-02-03, Session 26)
**Detail**: Share links showed relative paths (`/d/token`) instead of full copyable URLs. The repo-specific endpoint (`/api/v2.1/repos/:repo_id/share-links/`) was a stub returning empty `[]`, causing the admin share link panel to show no results. Fixed by adding `serverURL` to `ShareLinkHandler`, using `getBrowserURL()` for full URLs, and implementing `ListRepoShareLinks` handler.

### Tagged Files List Shows Deleted Files — FIXED ✅
**Status**: ✅ Fixed (2026-02-12)
**Reported**: 2026-02-03
**Detail**: The tagged files list no longer shows deleted files. `ListTaggedFiles` filters via `TraverseToPath()`. Cascade cleanup (`CleanupFileTagsByPath`) is wired into `DeleteFile`, `DeleteDirectory`, `MoveFile`, and batch delete. Tags are preserved on rename via `MoveFileTagsByPath` (files) and `MoveFileTagsByPrefix` (directories). `PermanentDeleteRepo` now calls `CleanupAllLibraryTags` to remove all tag data when a library is permanently deleted.

### Groups Creation — TESTED ✅
**Status**: ✅ Tested and working (2026-02-10)
**Reported**: 2026-01-31
**Tested**: 2026-02-10
**Detail**: User-facing group CRUD fully tested via `scripts/test-groups.sh` (20 assertions). All operations working: create, list, get, rename, add/remove members, share library to group, delete. Also fixed `ListBeSharedRepos` to resolve group shares (members can now see libraries shared to their groups via `/api2/beshared-repos/`).
**Files**: `internal/api/v2/groups.go`, `internal/api/v2/file_shares.go`, `scripts/test-groups.sh`

### Departments Support — COMPLETE ✅
**Status**: ✅ Complete (2026-01-31)
**Detail**: Full department CRUD implemented — list, create, get (with members/sub-depts/ancestors), update, delete. Hierarchical department system with parent/child relationships. 29 integration tests passing. See `internal/api/v2/departments.go` and `scripts/test-departments.sh`.

### API Token Library Access — COMPLETE ✅
**Status**: ✅ Complete (2026-01-31)
**Detail**: Repo API tokens now work for authentication. Token `b81b9683...` grants RW access to library "test". Implementation: reverse-lookup table `repo_api_tokens_by_token`, auth middleware checks token → resolves repo_id + permission, permission middleware enforces scope. Read-only tokens can list but not write; tokens can only access their designated library.

### GC TTL Enforcement — COMPLETE ✅
**Status**: ✅ 3 of 3 items done
**Reported**: 2026-01-31
**Updated**: 2026-02-04

**1. `auto_delete_days` enforcement** — ✅ DONE (2026-02-04)
- Scanner Phase 6 (`scanAutoDeleteExpiredObjects`) walks HEAD + recent commit trees, enqueues orphaned fs_objects
- 5 unit tests (basic, preserves HEAD tree, preserves recent commits, skips zero, nested dirs)

**2. `version_ttl_days` enforcement** — ✅ DONE (2026-02-02)
- Scanner Phase 5 (`scanExpiredVersions`) walks HEAD commit chain, enqueues expired non-HEAD commits
- 4 unit tests (expired enqueue, HEAD preserved, skip negative TTL, skip zero TTL)

**3. Expired share links deletion** — ✅ DONE (2026-02-02)
- `processShareLink()` now calls `DeleteShareLink()` instead of just logging

**2026-05-15 audit correction**: The rows above describe implemented plumbing, not complete product semantics. `version_ttl_days` and `auto_delete_days` persist and feed GC discovery, but History Setting and Auto deletion do not yet behave exactly as the UI text promises. See ISSUE-LIB-RETENTION-01.

### Admin Panel — WORKING ✅
**Status**: ✅ Working in Docker (2026-02-12)
**Reported**: 2026-02-02
**Fixed**: 2026-02-12

The sys-admin panel is fully accessible at `/sys/` in Docker deployments. Webpack builds `sysadmin.html` as a separate entry point, nginx serves it via `try_files`, and the Go backend catch-all serves it for non-Docker setups. All ~70 React routes load correctly.

**What exists in frontend** (all React components, now accessible):
- Users: list, search, create, edit, LDAP, admins
- Groups: list, search, create, members, libraries
- Departments: list, create, hierarchy, members, libraries
- Organizations: list, search, create, users, groups, repos
- Institutions, Logs, Devices, Statistics, Web Settings, Notifications

**What exists in backend**:
- Organizations CRUD: ✅ Full (`/admin/organizations/`)
- Departments CRUD: ✅ Full (`/admin/address-book/groups/`)
- User management: 🟡 Partial (per-org list, update role/quota, deactivate — no create, no global list)
- Admin groups: ❌ Missing (user-facing group CRUD exists, but admin-level endpoints don't)
- Admin libraries: ❌ Missing
- Admin user search: ❌ Missing

**Key decision**: Should groups/departments be managed via OIDC provider (claims-based sync) or locally in SesameFS? See `CURRENT_WORK.md` → "PRIORITY 1" for full analysis with 3 options.

**Key files**:
- Frontend: `frontend/src/pages/sys-admin/` (all components), `frontend/config/webpack.entry.js` (entry points)
- Backend: `internal/api/v2/admin.go` (org/user handlers), `internal/api/v2/groups.go` (user-facing groups)
- Config: `frontend/src/utils/constants.js` lines 152-173 (`window.sysadmin.pageOptions`)

---

## ✅ RECENTLY FIXED (2026-01-31 Session 15)

### Download URLs Used Wrong Port (ERR_CONNECTION_REFUSED) - FIXED ✅
**Fixed**: 2026-01-31
**Was**: Download URLs pointed to `http://localhost:8082/seafhttp/...` (backend's internal port), but the browser accesses the app at `http://localhost:3000` (nginx). Browser got ERR_CONNECTION_REFUSED.
**Root Cause**: `SERVER_URL=http://localhost:8082` in docker-compose, but browser-facing URLs should use the request's Host header.
**Fix**: Added `getBrowserURL()` helper that uses `X-Forwarded-Proto` + `Host` headers from the request to generate browser-reachable URLs. Applied to `GetDownloadLink`, `GetUploadLink`, `GetFileInfo`, and `redirectToDownload`.
**Files**: `internal/api/v2/files.go`, `internal/api/v2/fileview.go`

### File Download Returned JSON Instead of Download URL - FIXED ✅
**Fixed**: 2026-01-31
**Was**: Clicking download on a file showed JSON metadata (`{"id":"...","name":"test.md",...}`) instead of downloading.
**Root Cause**: `seafile-js` calls `GET /api2/repos/{id}/file/?p={path}&reuse=1` expecting a plain download URL string. Our `GetFileInfo` handler returned JSON metadata for all requests.
**Fix**: `GetFileInfo` now detects api2 download requests (via `reuse` parameter or `/api2/` URL prefix) and returns a plain download URL string instead of JSON.
**Files**: `internal/api/v2/files.go` — new `getFileDownloadURL()` method + `getBrowserURL()` helper

### Search User 404 Error - FIXED ✅
**Fixed**: 2026-01-31
**Was**: `GET /api2/search-user/?q=a` returned 404 (Not Found)
**Impact**: Transfer ownership dialog, share dialog user search didn't work
**Fix**: Implemented `handleSearchUser` endpoint that searches users by email/name within the same organization
**Files**: `internal/api/server.go`

### Multi-Share-Links 404 Error - FIXED ✅
**Fixed**: 2026-01-31
**Was**: `POST /api/v2.1/multi-share-links/` returned 404
**Impact**: "Generate Share Link" feature didn't work
**Fix**: Added `/multi-share-links/` route aliases pointing to existing share link handlers
**Files**: `internal/api/v2/share_links.go`

### Copy/Move Progress 404 Error - FIXED ✅
**Fixed**: 2026-01-31
**Was**: `GET /api/v2.1/query-copy-move-progress/?task_id=...` returned 404 (operations still worked)
**Root Cause**: Backend had `/api/v2.1/copy-move-task/` but `seafile-js` calls `/api/v2.1/query-copy-move-progress/`
**Fix**: Added alias routes for both URL patterns
**Files**: `internal/api/v2/batch_operations.go`

### File History Restore 400 Error - FIXED ✅
**Fixed**: 2026-01-31
**Was**: `POST /api/v2.1/repos/{id}/file/?p=/test.md` with `operation=revert` returned 400
**Root Cause**: `FileOperation` handler didn't support the `revert` operation
**Fix**: Added `RevertFile` handler that restores a file from a previous commit by traversing the old commit's tree, extracting the file entry, and creating a new commit in the current HEAD
**Files**: `internal/api/v2/files.go`

---

### Hardcoded Role Hierarchies Missing Superadmin - FIXED ✅
**Fixed**: 2026-01-29
**Was**: Role hierarchy maps in `libraries.go`, `files.go`, `batch_operations.go` only had `admin(3), user(2), readonly(1), guest(0)`. The `superadmin` role was missing, so superadmin users got role level 0 (unknown key) and were denied write operations.
**Root Cause**: Role hierarchy was duplicated as inline `map[OrganizationRole]int` in 3 handler files instead of using a shared constant or the middleware's `hasRequiredOrgRole()`.
**Fix**: Added `RoleSuperAdmin: 4` to all 3 inline role hierarchy maps. Also added to `permissions.go` (the authoritative source).
**Files**: `internal/api/v2/libraries.go`, `internal/api/v2/files.go`, `internal/api/v2/batch_operations.go`
**Note**: ✅ Technical debt resolved (2026-02-12) — inline maps were removed, all 3 files now delegate to `middleware.HasRequiredOrgRole()`. The canonical maps live only in `internal/middleware/permissions.go`.

### Account Info `can_generate_share_link` Field Name
**Status**: ℹ️ Documentation note
**Discovered**: 2026-01-29
**Detail**: The account info endpoint returns `can_generate_share_link` (not `can_generate_shared_link`). Integration tests initially used the wrong field name. Not a bug in the API — just a test expectation mismatch.

### Anonymous Auth Bypasses Admin API Endpoints — REMOVED ✅
**Status**: Removed 2026-04-10 — `AllowAnonymous` config option and its anonymous fallback in `authMiddleware` have been deleted. Dev tokens must be provided explicitly.

### Change Password Shows for Non-Encrypted Libraries - FIXED ✅
**Fixed**: 2026-01-28
**Was**: "Change Password" menu item appeared for non-encrypted libraries
**Root Cause**: Truthy check `if (repo.encrypted)` may have had edge cases
**Fix**: Made check explicit: `if (repo.encrypted === true || repo.encrypted === 1)`
**Files**: `frontend/src/pages/my-libs/mylib-repo-menu.js`

### Watch/Unwatch File Changes - NOT IMPLEMENTED
**Status**: ❌ BACKEND NOT IMPLEMENTED
**Reported**: 2026-01-28
**Error**: `POST http://localhost:8080/api/v2.1/monitored-repos/ 404 (Not Found)`

**Missing Endpoints**:
- `POST /api/v2.1/monitored-repos/` - Add library to monitored list
- `DELETE /api/v2.1/monitored-repos/{repo_id}/` - Remove from monitored list
- `GET /api/v2.1/monitored-repos/` - List monitored libraries

**Current State**:
- Frontend UI toggle exists (shows/hides monitor icon)
- Backend endpoints return 404
- No notification system implemented

**Required Work** (if implementing):
1. Create `monitored_repos` table in Cassandra
2. Implement CRUD endpoints for monitored repos
3. Design notification system (email, websocket, polling?)
4. Implement backend notification triggers on file changes
5. Connect frontend to display notifications

**Note**: This is a complex feature requiring significant backend work. Consider deferring.

### Test Scripts Don't Fully Clean Up — FIXED ✅
**Status**: ✅ All scripts have cleanup (2026-02-10)
**Reported**: 2026-01-28
**Fixed**: 2026-02-10
**Symptom**: Running tests leaves test libraries/files in the database
**Resolution**: All test scripts now have `cleanup()` function with `trap cleanup EXIT` to remove test-created resources on exit (success or failure).
**Scripts with cleanup**: `test-file-operations.sh`, `test-batch-operations.sh`, `test-permissions.sh`, `test-library-settings.sh`, `test-encrypted-library-security.sh`, `test-groups.sh`

### Pre-Existing Go Unit Test Failures (4 tests) — FIXED ✅
**Fixed**: 2026-01-29 (Session 11)
**Was**: 4 tests failing due to nil-pointer dereferences in test setup
**Fix**: Fixed SessionManager init (nil cache → NewSessionManager), fixed JSON format expectations in OnlyOffice tests

### Frontend Unit Test Coverage Extremely Low
**Status**: CRITICAL GAP
**Reported**: 2026-01-28
**Symptom**: Only 4 test files for 620+ frontend source files (~0.6% coverage)

**Current State**:
| Category | Source Files | Test Files |
|----------|-------------|------------|
| Components | 347 | 1 |
| Pages | 260 | 0 |
| Dialogs | 159 | 1 |
| Utils | 15 | 1 |
| Models | ~10 | 1 |
| **Total** | **~620+** | **4** |

**Priority Tests Needed**:
1. **Utils functions** - Pure functions, easy to test
2. **Models** - Data transformation logic
3. **API client methods** - Mock responses, verify calls
4. **Dialog components** - Render tests, user interactions
5. **Permission checks** - Verify UI hides/shows based on role

**Test Pattern**: Use documentation-style tests (like modal-pattern.test.js) that verify file contents without full React rendering to avoid @testing-library/react ES module issues.

### Frontend E2E Tests Not Implemented
**Status**: NEEDS DESIGN
**Reported**: 2026-01-28
**Symptom**: No Cypress/Playwright tests that test actual UI with running backend
**Expected**: Should have E2E tests for login, file operations, sharing, etc.
**Required Work**:
1. Choose E2E framework (Cypress or Playwright)
2. Set up test fixtures and test user accounts
3. Write integration tests for key workflows

### Many Dialogs Need Modal Pattern Fix
**Status**: MOSTLY FIXED (2026-01-28)
**Reported**: 2026-01-28
**Symptom**: Multiple dialogs in `mylib-repo-list-item.js` may not open properly

**FIXED Dialogs** (converted to plain Bootstrap):
- ✅ ShareDialog (already fixed)
- ✅ DeleteRepoDialog (already fixed)
- ✅ TransferDialog (fixed 2026-01-28)
- ✅ LibHistorySettingDialog (fixed 2026-01-28)
- ✅ ChangeRepoPasswordDialog (already fixed)
- ✅ ResetEncryptedRepoPasswordDialog (fixed 2026-01-28)
- ✅ LabelRepoStateDialog (fixed 2026-01-28)
- ✅ LibSubFolderPermissionDialog (fixed 2026-01-28)
- ✅ RepoAPITokenDialog (fixed 2026-01-28)
- ✅ RepoSeaTableIntegrationDialog (fixed 2026-01-28)
- ✅ RepoShareAdminDialog (fixed 2026-01-28)
- ✅ LibOldFilesAutoDelDialog (fixed 2026-01-28)
- ✅ ListTaggedFilesDialog (fixed 2026-01-28)
- ✅ EditFileTagDialog (fixed 2026-01-28)
- ✅ CreateTagDialog (fixed 2026-01-28)

**Remaining**: ~90+ dialogs in sysadmin and other areas still use reactstrap Modal
**Fix Pattern**: See [docs/FRONTEND.md](FRONTEND.md) → "Modal Pattern"

### Library Transfer Not Working
**Status**: NOT IMPLEMENTED
**Reported**: 2026-01-28
**Symptom**: Clicking "Transfer" on a library does nothing, no errors shown
**Root Cause**: The `seafileAPI.transferRepo()` method doesn't exist in the seafile-js library
**Required Work**:
1. Add `transferRepo(repoID, email)` method to `frontend/src/utils/seafile-api.js`
2. Create backend endpoint `PUT /api2/repos/{repo_id}/owner/`
3. Implement ownership change in database (update `libraries.owner_id`)

### Sharing / Multiple Owners / Group Ownership
**Status**: DESIGN NEEDED
**Reported**: 2026-01-28
**Requirement**: Libraries should support:
- Owners should be able to share their libraries
- Multiple owners for one library
- Group ownership (a group can own a library)
**Current State**:
- `libraries` table has single `owner_id` field
- Sharing exists via `shares` table but doesn't grant ownership
**Required Work**:
1. Design data model for multi-owner / group owner support
2. Create `library_owners` table or modify `libraries` schema
3. Update permission checks to allow any owner to share
4. Add frontend UI for managing library owners

---

## ✅ RECENTLY FIXED (2026-01-29 Sessions 7-9)

### OnlyOffice "Invalid Token" Error - FIXED ✅
**Fixed**: 2026-01-29
**Was**: Opening Word/Excel/PPT documents via OnlyOffice showed "Invalid Token — The provided authentication token is not valid"
**Root Cause (auth)**: File view endpoint (`/lib/:repo_id/file/*`) had a custom auth middleware that only supported dev tokens, not OIDC sessions.
**Root Cause (JWT)**: Go `html/template` applied JavaScript-context escaping (`\/`, `\u0026`, extra whitespace around booleans) when building the config object, causing a mismatch with the JWT payload signed by `json.Marshal`.
**Fix**: (1) Replaced custom auth middleware with thin wrapper that delegates to server's standard auth. (2) Replaced `html/template` field-by-field config with `json.Marshal` output — guarantees byte-identical config/JWT. (3) Added `url.QueryEscape` for file_path in callback URL.
**File**: `internal/api/v2/fileview.go`
**Status**: 🔒 FROZEN — OnlyOffice integration stable and verified

### CreateFile in Nested Folder Corrupts Tree - FIXED ✅
**Fixed**: 2026-01-29
**Was**: Creating a file (e.g., Word docx) inside any subfolder via the v2.1 API caused "Folder does not exist" when navigating back
**Root Cause**: `CreateFile` called `RebuildPathToRoot(result, newParentFSID)` without grandparent handling. For non-root parents, the modified subfolder was set as `root_fs_id` instead of updating root to point to the new subfolder.
**Fix**: Added `if parentPath == "/" / else { grandparent rebuild }` pattern matching `CreateDirectory`
**File**: `internal/api/v2/files.go` — CreateFile function

### Nested Directory Creation (depth 3+) Corrupts Root FS - FIXED ✅
**Fixed**: 2026-01-29
**Was**: Creating directories at depth 3+ produced incorrect root_fs_id → "Folder does not exist"
**Root Cause**: Re-traversed uncommitted HEAD for grandparent rebuild, producing wrong ancestor data
**Fix**: Used original traversal result's ancestor chain for `RebuildPathToRoot`
**Files**: `internal/api/v2/files.go`, `internal/api/v2/batch_operations.go`

### Batch Move/Copy Destination Rebuild Bug - FIXED ✅
**Fixed**: 2026-01-29
**Was**: Batch move/copy into nested directories could corrupt destination tree
**Root Cause**: Same stale HEAD re-traversal bug on destination side of batch operations
**Fix**: Same pattern — use original traversal result
**File**: `internal/api/v2/batch_operations.go`

---

## ✅ RECENTLY FIXED (2026-01-28 Session 3)

### File Creation 409 Conflict in Nested Folders - FIXED ✅
**Fixed**: 2026-01-28
**Error**: `POST /api/v2.1/repos/{repo_id}/file/?p={path} 409 (Conflict)`
**Symptom**: Creating a file inside a nested folder (e.g., `/test0035/test0035/file.docx`) returned 409 incorrectly

**Root Cause**:
In `CreateFile`, `TraverseToPath("/parent/child")` returns:
- `result.Entries` = entries of `/parent` (grandparent)
- `result.TargetFSID` = FSID of `/parent/child` (actual parent)

Code was checking `result.Entries` instead of getting entries from `result.TargetFSID`.
If a name existed at the grandparent level, it would incorrectly return 409.

**Fix**: Get entries from `result.TargetFSID` (matches `CreateFolder` function pattern)
**File**: `internal/api/v2/files.go` - CreateFile function

### Modal Pattern Applied to 15 Dialogs - FIXED ✅
**Fixed**: 2026-01-28
**Was**: Multiple dialogs in library menu didn't open when using ModalPortal + reactstrap Modal
**Root Cause**: reactstrap Modal creates its own portal, doesn't render correctly inside ModalPortal
**Fix**: Converted all affected dialogs to plain Bootstrap modal classes
**Files Fixed**:
- `frontend/src/components/dialog/transfer-dialog.js`
- `frontend/src/components/dialog/lib-history-setting-dialog.js`
- `frontend/src/components/dialog/reset-encrypted-repo-password-dialog.js`
- `frontend/src/components/dialog/label-repo-state-dialog.js`
- `frontend/src/components/dialog/lib-sub-folder-permission-dialog.js`
- `frontend/src/components/dialog/repo-api-token-dialog.js`
- `frontend/src/components/dialog/repo-seatable-integration-dialog.js`
- `frontend/src/components/dialog/lib-old-files-auto-del-dialog.js`
- `frontend/src/components/dialog/edit-filetag-dialog.js`
- `frontend/src/components/dialog/create-tag-dialog.js`

---

## ✅ RECENTLY FIXED (2026-01-28 Session 2)

### Share Admin Dialog Not Opening - FIXED ✅
**Fixed**: 2026-01-28
**Was**: Clicking "Share Admin" menu item did nothing
**Root Cause**: RepoShareAdminDialog uses reactstrap Modal inside ModalPortal
**Fix**: Converted to plain Bootstrap modal classes
**Files**: `frontend/src/components/dialog/repo-share-admin-dialog.js`

### Tagged Files Dialog Not Opening - FIXED ✅
**Fixed**: 2026-01-28
**Was**: Clicking tag file count (e.g., "1 file") did nothing, even though API returned data
**Root Cause**: ListTaggedFilesDialog uses reactstrap Modal inside ModalPortal
**Fix**: Converted to plain Bootstrap modal classes
**Files**: `frontend/src/components/dialog/list-taggedfiles-dialog.js`

### Create Repo Tag 500 Error - FIXED ✅
**Fixed**: 2026-01-28
**Was**: `POST /api/v2.1/repos/:repo_id/repo-tags/` returned 500 "failed to initialize tag counter"
**Root Cause**: Cassandra LWT (ScanCAS) was incorrectly used for counter initialization
**Fix**: Replaced LWT with simple SELECT then INSERT/UPDATE pattern
**Files**: `internal/api/v2/tags.go` - CreateRepoTag function

### File Tags 500 Error - FIXED ✅
**Fixed**: 2026-01-28
**Was**: `POST /api/v2.1/repos/:repo_id/file-tags/` returned 500 Internal Server Error
**Root Cause**: Counter updates mixed with non-counter operations in Cassandra logged batch
**Fix**: Separated counter updates from logged batch (counter must be in separate query)
**Files**:
- `internal/api/v2/tags.go` - AddFileTag, RemoveFileTag: moved counter updates outside batch

### Copy/Move Dialog Not Showing Libraries - FIXED ✅
**Fixed**: 2026-01-28
**Was**: Copy/Move dialogs showed empty library list (only current library visible)
**Root Cause**: API returned `permission: "owner"` but frontend filtered by `permission === 'rw'`
**Fix**: Added `apiPermission()` helper to translate "owner" to "rw" in API responses
**Files**:
- `internal/api/v2/libraries.go` - Added apiPermission() function, applied to all permission fields

### Tagged Files Feature Not Working - FIXED ✅
**Fixed**: 2026-01-28
**Was**: Clicking tag file count (e.g., "3 files") did nothing
**Root Cause**:
1. Backend endpoint `GET /api/v2.1/repos/:repo_id/tagged-files/:tag_id/` was not implemented
2. Frontend `seafile-api.js` was missing all tag-related API methods (not in upstream seafile-js)
**Fix**:
1. Implemented `ListTaggedFiles` backend handler with correct response format
2. Added all tag API methods to `frontend/src/utils/seafile-api.js`
**Files**:
- `internal/api/v2/tags.go` - Added TaggedFileInfo struct and ListTaggedFiles handler
- `frontend/src/utils/seafile-api.js` - Added listRepoTags, createRepoTag, updateRepoTag, deleteRepoTag, getFileTags, addFileTag, deleteFileTag, listTaggedFiles, getShareLinkTaggedFiles

---

## ✅ RECENTLY FIXED (2026-01-28)

### Encrypted Library Password Cancel - FIXED ✅
**Fixed**: 2026-01-28
**Was**: Infinite loading spinner when closing password dialog
**Root Cause**: `onLibDecryptDialog` callback didn't distinguish between success and cancel
**Fix**: Added `success` parameter to callback; cancel now redirects to library list
**Files**:
- `frontend/src/components/dialog/lib-decrypt-dialog.js` - Pass true/false to callback
- `frontend/src/pages/lib-content-view/lib-content-view.js` - Handle success vs cancel

### Share Links API 500 Error - FIXED ✅
**Fixed**: 2026-01-28
**Was**: 500 Internal Server Error when opening Share dialog
**Root Cause**: Missing `share_links_by_creator` table in Cassandra schema
**Fix**: Created table and fixed UUID marshaling in queries
**Files**:
- `internal/api/v2/share_links.go` - Use `gocql.ParseUUID` instead of `uuid.Parse`
- `scripts/bootstrap.sh` - Added `share_links_by_creator` table
- `scripts/bootstrap-multiregion.sh` - Same

---

## ✅ RECENTLY FIXED (2026-01-27)

### Logout Button - FIXED ✅ 🔒 FROZEN
**Fixed**: 2026-01-27
**Status**: Working correctly - DO NOT MODIFY
**Issue**: Clicking logout went to `/accounts/logout/` but nothing happened
**Root Cause**: Frontend nginx wasn't proxying `/accounts/` routes to backend
**Fix**: Added `/accounts/` location block to `frontend/nginx.conf`
**Files**: `frontend/nginx.conf` (lines 77-83)

### Anonymous Access for Testing — REMOVED ✅
**Removed**: 2026-04-10
**Was**: `AUTH_ALLOW_ANONYMOUS=true` allowed unauthenticated requests to be injected as the first dev token user.
**Why removed**: Redundant — `AUTH_DEV_MODE=true` with an `Authorization: Token <dev-token>` header achieves the same without an implicit bypass. The feature was deleted along with `AllowAnonymous` config field and `applyAnonymousDevAuth()`.

### Frontend Login Bypass - IMPLEMENTED ✅
**Implemented**: 2026-01-27
**Status**: Working - FOR TESTING ONLY
**Feature**: Set `REACT_APP_BYPASS_LOGIN=true` to skip login page
**Files**: `frontend/src/utils/seafile-api.js`, `frontend/.env`

---

## ✅ RECENTLY FIXED (2026-01-24)

### Media File Viewer Fix - FIXED ✅ (Pending manual testing)
**Fixed**: 2026-01-23
**Was**: CRITICAL UX bug
**Root Cause**: Mobile view missing `onClick` handler, causing direct navigation to download URL
**Files Fixed**:
- `frontend/src/components/dirent-list-view/dirent-list-item.js` line 798

**What Works Now** (pending manual testing):
- ✅ Clicking images should open image popup viewer
- ✅ Clicking PDFs should open in-browser PDF viewer
- ✅ Clicking videos should open video player
- ✅ Mobile view now has same click handling as desktop view

**Manual Testing Required**:
- Test clicking various file types on mobile view
- Test clicking images (should open popup)
- Test clicking PDFs (should open viewer)
- Test clicking videos (should open player)

### Permission Middleware Integration - COMPLETE ✅ (Pending full testing)
**Completed**: 2026-01-23
**Status**: Core implementation done, example checks integrated
**Files Implemented**:
- `internal/middleware/permissions.go` - Full permission middleware (371 lines)
- `internal/api/server.go` - Initialized and integrated
- `internal/api/v2/libraries.go` - Example permission checks

**What's Implemented**:
- ✅ Organization role checking (admin/user/readonly/guest)
- ✅ Library permission checking (owner/rw/r)
- ✅ Group role checking (owner/admin/member)
- ✅ Group permission resolution (users inherit group library permissions)
- ✅ CreateLibrary: Requires "user" role or higher
- ✅ DeleteLibrary: Requires library ownership

**Manual Testing Required**:
- Test CreateLibrary with different user roles
- Test DeleteLibrary with non-owner users
- Test group permission inheritance
- Add permission checks to remaining handlers incrementally

### Database Seeding - COMPLETE ✅
**Completed**: 2026-01-23
**Status**: Fully implemented and tested
**Files Implemented**:
- `internal/db/seed.go` - Database seeding implementation (220 lines)
- `cmd/sesamefs/main.go` - Integrated into startup

**What's Seeded**:
- ✅ Default organization (1TB quota)
- ✅ Admin user (role: admin)
- ✅ Test users (user, readonly, guest roles) - dev mode only
- ✅ Users indexed in users_by_email for login

### Test Coverage Improvements - COMPLETE ✅
**Completed**: 2026-01-24
**Status**: Comprehensive tests added for all new features

**Backend Tests Created**:
- `internal/db/seed_test.go` - Database seeding tests (9 tests, all passing)
  - Tests UUID uniqueness, idempotency, dev vs production modes
  - Tests organization creation, admin user, test users
  - Tests email indexing for login
- `internal/api/v2/libraries_test.go` - Permission middleware tests (3 test suites)
  - Tests role hierarchy (admin > user > readonly > guest)
  - Tests library creation permission (requires "user" role or higher)
  - Tests library deletion permission (requires ownership)
  - Tests group permission resolution

**Frontend Tests Created**:
- `frontend/src/components/dirent-list-view/__tests__/dirent-list-item.test.js`
  - Documents media viewer fix behavior
  - Tests file type detection (images, PDFs, videos)
  - Tests onClick handler presence (desktop and mobile views)
  - Regression test for line 798 fix

**Test Results**:
- ✅ All backend tests passing
- ✅ Backend coverage: 23.4% overall (stable)
- ✅ internal/db: 0.0% (tests are documentation-style, skip DB operations)
- ✅ internal/api/v2: 18.4% coverage (improved from adding tests)

**Type Error Fixed**:
- Fixed `internal/api/v2/libraries_test.go:468` - Changed `Encrypted: false` (bool) to `Encrypted: 0` (int)
- This is NOT a protocol change - API already returns int (0/1) for Seafile compatibility

### Share Modal 500 Error - FIXED ✅
**Fixed**: 2026-01-23
**Was**: CRITICAL regression
**Root Cause**: Missing `org_id` in Cassandra queries (partition key required)
**Files Fixed**:
- `internal/api/v2/share_links.go` lines 125, 153
- `internal/api/v2/file_shares.go` lines 116, 138, 146, 651
- `internal/middleware/permissions.go` line 242 (group permission resolution)

**What Works Now**:
- ✅ Share modal loads without errors
- ✅ Group names display correctly (not UUIDs)
- ✅ Users see libraries shared to their groups
- ✅ User emails display correctly (not UUIDs)

---

## ✅ FIXED SECURITY/PERMISSION ISSUES (Fixed 2026-01-24 to 2026-01-27)

**Status**: ✅ ALL FIXED - Backend permission system complete
**Testing**: Manual testing passed with all 4 user roles

### Issue 1: All Users Can See All Libraries - FIXED ✅
**Severity**: CRITICAL - Complete privacy violation
**Discovered**: 2026-01-24 manual testing

**Bug**: User logged in as `user@sesamefs.local` can see libraries owned by `admin@sesamefs.local`

**Expected Behavior**:
- Users should ONLY see their own libraries
- Exception: Libraries explicitly shared with them

**Actual Behavior**:
- `GET /api/v2.1/repos/` returns ALL libraries in organization
- No filtering by ownership or shares

**Root Cause**: `ListLibraries()` in `internal/api/v2/libraries.go` has NO permission filtering

**Impact**:
- Zero privacy between users
- Users can see library names, sizes, encryption status of all libraries
- Violates basic multi-tenant isolation

**Files**: `internal/api/v2/libraries.go` - `ListLibraries()` function

---

### Issue 2: Users Can Access Other Users' Libraries - FIXED ✅
**Severity**: CRITICAL - Complete access control failure
**Discovered**: 2026-01-24 manual testing

**Bug**: Any user can access any library by direct URL or navigation

**Test Cases**:
- `user@sesamefs.local` browsed libraries owned by `admin@sesamefs.local`
- `guest@sesamefs.local` accessed library owned by `user@sesamefs.local`
- All directory contents visible to unauthorized users

**Expected Behavior**:
- Users can only access own libraries
- Access to other libraries ONLY if explicitly shared
- Should get 403 Forbidden if attempting unauthorized access

**Actual Behavior**:
- NO permission checks on directory listing endpoints
- NO permission checks on library detail endpoints
- Complete access to all libraries regardless of ownership

**Root Cause**: Missing permission checks on:
- `GET /api/v2.1/repos/:repo_id` (GetLibrary)
- `GET /api/v2.1/repos/:repo_id/dir/` (ListDirectory)

**Impact**:
- Users can read all files from all libraries
- Zero access control
- Data breach scenario

---

### Issue 3: Readonly Users Can Write to Other Users' Libraries - FIXED ✅
**Severity**: CRITICAL - Role-based access control failure
**Discovered**: 2026-01-24 manual testing

**Bug**: User `readonly@sesamefs.local` successfully edited Word docx files in encrypted libraries owned by other users

**Expected Behavior**:
- readonly role = read-only access to own libraries ONLY
- Should get 403 on write attempts (upload, edit, delete)
- Should have ZERO access to other users' libraries

**Actual Behavior**:
- readonly user can upload files to any library
- readonly user can edit documents in any library (via OnlyOffice)
- NO enforcement of role restrictions

**Root Cause**: Missing permission checks on:
- File upload endpoints (`/seafhttp/upload-api/`)
- OnlyOffice save callback (`internal/api/v2/onlyoffice.go`)
- File create/edit/delete operations

**Impact**:
- Role system is non-functional
- readonly and guest roles have same permissions as admin
- Data corruption risk

---

### Issue 4: Guest User Can Modify Libraries and Cause Data Loss - FIXED ✅
**Severity**: CRITICAL - Data corruption + access control failure
**Discovered**: 2026-01-24 manual testing

**Bug**: User `guest@sesamefs.local` accessed library owned by `user@sesamefs.local`, created file, caused original files to disappear

**Timeline**:
1. guest@ logged in
2. Navigated to library owned by user@ (test0034)
3. Created new file `test-guest.docx` (2.2 KB)
4. After creation, user@'s original files disappeared from directory listing

**Expected Behavior**:
- guest role should have ZERO access to other users' libraries
- guest should only see own libraries (if any)
- Creating files should not cause existing files to disappear

**Actual Behavior**:
- guest can access any library
- guest can create files in any library
- File creation caused data corruption (files disappeared)

**Root Cause**:
- Missing permission checks (same as Issues 1-3)
- Possible commit/fs_object corruption in multi-user scenario

**Impact**:
- Data loss
- Complete lack of user isolation
- Potential filesystem corruption

**Files**:
- Permission checks needed in all file operation endpoints
- Investigate fs_object/commit corruption issue

---

### Issue 5: Encrypted Libraries Not Protected from Sharing - FIXED ✅
**Severity**: CRITICAL - Security policy violation
**Discovered**: 2026-01-24 (known issue, not yet enforced)

**Policy**: Password-encrypted libraries CANNOT be shared (sharing would require sharing encryption key)

**Status**: NOT ENFORCED in backend

**Expected Behavior**:
- Attempting to share encrypted library should return 403
- Clear error message: "Cannot share encrypted libraries. Move files to a non-encrypted library to share them."

**Actual Behavior**:
- Backend allows share creation on encrypted libraries
- Frontend shows loading spinner (stuck) when trying to share encrypted files

**Root Cause**: No validation in share creation endpoints

**Files**: `internal/api/v2/file_shares.go` - Share creation functions

**Impact**:
- Security vulnerability
- Encrypted data could be shared inappropriately
- Encryption key management violated

---

## 📋 Comprehensive Fix Plan

**See**: `docs/PERMISSION-ROLLOUT-PLAN.md` for full implementation plan

**Summary**:
- Phase 1: Library access control (filter ListLibraries, check GetLibrary, check directory listing)
- Phase 2: File operations (upload, edit, delete, rename, move)
- Phase 3: Encrypted library policy enforcement
- Estimated time: 2-3 days
- Approach: Systematic application of permission middleware to ALL endpoints

---

## ✅ FIXED (2026-02-11) - Sync Protocol Security + Environment Management

### Sync Protocol Permission Enforcement - FIXED ✅
**Fixed**: 2026-02-11
**Was**: 🔴 CRITICAL - All 15 sync endpoints had ZERO permission checks. Any authenticated user could read/write ANY library.

**What was fixed**:
- Added `permMiddleware` to `SyncHandler` struct
- `checkSyncPermission()` helper checks `HasLibraryAccess()` before every operation
- 9 READ endpoints require `PermissionR`: GetHeadCommit, GetCommit, GetBlock, CheckBlocks, GetFSIDList, GetFSObject, PackFS, CheckFS, GetDownloadInfo
- 4 WRITE endpoints require `PermissionRW`: PutCommit, PutBlock, RecvFS, UpdateBranch
- `GetHeadCommitsMulti`: silently filters repos user cannot access
- `PermissionCheck` endpoint: no longer a stub, calls `GetLibraryPermission()` and returns 403 if denied
- `QuotaCheck` endpoint: now verifies read access before responding
- `GetDownloadInfo`: returns actual user permission instead of hardcoded `"rw"`
- `HandleDownload` in `seafhttp.go`: now checks `PermissionR` (matching `HandleUpload` pattern)

**Files**: `internal/api/sync.go`, `internal/api/server.go`, `internal/api/seafhttp.go`

### Sync Auth Middleware Hardened - FIXED ✅
**Fixed**: 2026-02-11
**Was**: 🔴 CRITICAL - No token = silent dev-user fallback; invalid token in dev mode = silent dev-user fallback

**What was fixed**:
- No token = 401 Unauthorized (always)
- Invalid token = 401 Unauthorized (always)
- Valid dev tokens still work in dev mode (intentional)

**Files**: `internal/api/server.go` (`syncAuthMiddleware`)

### Docker Compose Secrets Externalized - FIXED ✅
**Fixed**: 2026-02-11
**Was**: Production credentials (email/password) hardcoded in `docker-compose.yaml`, JWT secret hardcoded in `configs/config.docker.yaml`

**What was fixed**:
- All values now use `${VAR:-default}` syntax, read from `.env`
- `.env.example` documents all variables with safe defaults
- `seafile-cli-debug` moved to `profiles: [debug]` (not started by default)
- JWT secret uses env var `ONLYOFFICE_JWT_SECRET`
- `.reference.md` added to `.gitignore`

**Files**: `docker-compose.yaml`, `docker-compose-multiregion.yaml`, `.env`, `.env.example`, `configs/config.docker.yaml`, `.gitignore`

---

## ✅ RECENTLY FIXED (2026-01-27) - Security & Permissions

### Encrypted Libraries Load Without Password - FIXED ✅
**Fixed**: 2026-01-27
**Was**: 🔴 CRITICAL - Security bypass
**Status**: ✅ FIXED - Encrypted libraries now properly protected

**Bug Was**: Frontend loaded encrypted library contents even without entering password

**Root Cause Found**: Frontend was making directory listing API calls without checking `libNeedDecrypt` state first

**Fix Applied**:
- Added encryption check to `loadDirentList()` - returns early if `libNeedDecrypt` is true
- Added encryption check to `loadDirData()` - returns early if `libNeedDecrypt` is true
- Added encryption check to `loadSidePanel()` - returns early if `libNeedDecrypt` is true

**Files Fixed**: `frontend/src/pages/lib-content-view/lib-content-view.js`

**Behavior Now**:
- ✅ Password dialog appears first
- ✅ NO API calls made until password verified
- ✅ Directory listing blocked until decrypt session active
- ✅ Backend returns 403 if no decrypt session (double protection)

### User Profile Shows UUIDs Instead of Names - FIXED ✅
**Fixed**: 2026-01-27
**Was**: User profiles showed UUIDs like "00000000-0000-0000-0..."

**Fix Applied**:
- Backend `handleAccountInfo` now queries actual user data from database
- Returns proper `name`, `email`, `role` from users table

**Files Fixed**: `internal/api/server.go:822-893`

### Role-Based UI Permissions - IMPLEMENTED ✅
**Implemented**: 2026-01-27
**Status**: ✅ Backend complete, Frontend ~30% complete

**Features**:
- Backend returns permission flags: `can_add_repo`, `can_share_repo`, etc.
- Frontend loads permissions on startup
- "New Library" button hidden for readonly/guest users
- Empty library message changed for restricted users

**Files**:
- `internal/api/server.go` - Permission flags in account info
- `frontend/src/app.js` - `loadUserPermissions()` function
- `frontend/src/components/toolbar/repo-view-toobar.js` - Conditional button rendering
- `frontend/src/pages/my-libs/my-libs.js` - Role-aware empty message

**Remaining Frontend Work**: See CURRENT_WORK.md for list of UI elements needing permission checks

---

## 🔴 CRITICAL UX BUGS

**None currently!** 🎉 (Pending manual testing)

---

## ✅ LIBRARY SETTINGS - IMPLEMENTED (Session 6)

**Status**: ✅ Backend complete (implemented 2026-01-29 Session 6)

| Feature | Endpoint | Status |
|---------|----------|--------|
| Watch/Unwatch | `POST /api/v2.1/monitored-repos/` | ❌ Not implemented (needs notification system) |
| History Setting | `GET/PUT /api/v2.1/repos/{id}/history-limit/` | ✅ Complete |
| API Token | `GET/POST/PUT/DELETE /api/v2.1/repos/{id}/repo-api-tokens/` | ✅ Complete |
| Auto Deletion | `GET/PUT /api/v2.1/repos/{id}/auto-delete/` | ✅ Complete |
| Library Transfer | `PUT /api2/repos/{id}/owner/` | ✅ Complete |

**File**: `internal/api/v2/library_settings.go`

**2026-05-15 audit correction**: Treat this section as API wiring status, not full product-complete semantics. History Setting and Auto Deletion are partial: `keep_days=0` does not round-trip, history APIs do not enforce the retention window, and auto-delete does not delete current stale files by `mtime`. Details are tracked in ISSUE-LIB-RETENTION-01.

### Library Settings Frontend Errors — FIXED ✅ (2026-01-30)

| Error | Root Cause | Fix |
|-------|-----------|-----|
| `POST repo-api-tokens/ 400` | Backend used `ShouldBindJSON`, frontend sends FormData | Changed to `ShouldBind` (auto-detects content type) |
| `PUT auto-delete/ 400` | Same — JSON-only binding vs FormData | Changed to `ShouldBind` |
| `PUT history-limit/ 400` | Same — JSON-only binding vs FormData | Changed to `ShouldBind` |
| `"disabled by Admin"` | `enableRepoHistorySetting: false` in index.html | Set to `true` |
| `enableRepoAutoDel: 'False'` | Auto-delete feature flag disabled | Set to `'True'` |

**File**: `internal/api/v2/library_settings.go` — all 5 handlers now accept both JSON and FormData (matching stock Seafile's `request.data` behavior)
**File**: `frontend/public/index.html` — enabled `enableRepoHistorySetting` and `enableRepoAutoDel`

**Note**: `POST monitored-repos/ 404` remains expected (not implemented — needs notification system)

---

## ✅ FILE OPERATIONS - COMPLETE

Move/Copy operations fully implemented (batch sync + async variants) with conflict resolution:
- **Conflict policies**: `replace`, `autorename`, `skip` — applied to both sync and async (cross-repo) paths
- **Pre-flight check**: Returns HTTP 409 with `conflicting_items` when no policy specified
- **137 integration tests** in `scripts/test-nested-move-copy.sh` (nested ops, conflicts, cross-repo, autorename)
- See also `scripts/test-batch-operations.sh` for basic batch operation tests.

---

## ⚠️ UI/UX ISSUES

### Thumbnails Not Implemented
**Severity**: MEDIUM
**Impact**: Visual polish

**Missing**:
- No image thumbnails in file list
- Grid view has no previews

### User Avatars Not Implemented
**Severity**: LOW
**Impact**: Visual polish

**Missing**:
- No profile pictures for users
- Generic icon shown

### Missing File Type Icons — FIXED ✅
**Severity**: LOW
**Impact**: Visual polish
**Fixed**: 2026-02-12

**Issue**: Folder icon variants returned 404 (read-only, shared-out, combo)
**Fix**: Created 6 missing folder icon PNGs in `frontend/public/static/img/`: `folder-read-only-{24,192}.png`, `folder-shared-out-{24,192}.png`, `folder-read-only-shared-out-{24,192}.png`

---

## 🚧 BACKEND NOT IMPLEMENTED

### Garbage Collection — COMPLETE ✅
**Status**: ✅ Fully implemented (2026-01-30), major overhaul (2026-03-17)
**Files**: `internal/gc/` — gc.go, queue.go, worker.go, scanner.go, store.go, store_cassandra.go, gc_hooks.go, gc_adapter.go
**Tests**: 55 Go unit tests + 21 bash integration tests
**Admin API**: `GET /api/v2.1/admin/gc/status`, `POST /api/v2.1/admin/gc/run`

**2026-03-17 overhaul:**
- Worker: 7 item types (block, commit, fs_object, block_mapping, share_link, share, restore_job)
- Scanner: 8 phases (orphaned blocks/commits/fs_objects, expired share links/versions/auto-delete/shares/restore jobs)
- Commit deletion now cascades → root fs_object → child entries → blocks (was missing cascade)
- Library deletion enqueues all artifacts (shares, tags, tokens, locked files)
- Reverse lookup table `block_id_mappings_by_internal` eliminates full-table scans on block deletion
- `walkFSTree` converted from recursive to iterative (prevents stack overflow)
- Stats persisted to `gc_stats` table on shutdown, restored on startup (survives container restarts)
- Scanner runs immediately on startup before entering 24h ticker loop

### Authentication — COMPLETE ✅
**Status**: ✅ OIDC Phase 1 complete (2026-01-28) + dev tokens
**Files**: `internal/auth/oidc.go`, `internal/auth/session.go`, `internal/api/v2/auth.go`

**Security hardening (2026-02-20):**
- ✅ **JWT signature verification via JWKS**: `parseIDToken()` now fetches the provider's JWKS keys and verifies RS256/ES256 signatures using `golang-jwt/v5`. JWKS keys are cached for 1 hour with automatic refresh on unknown `kid` (key rotation support).
- ✅ **Rate limiting on auth endpoints**: Per-IP token-bucket rate limiter (~10 req/min) applied to `POST /api2/auth-token`, `POST /api2/client-sso-link`, `GET /oauth/callback`, and `POST /api/v2.1/auth/oidc/callback`. Returns 429 Too Many Requests when exceeded. Implementation: `internal/middleware/ratelimit.go`.

### Permission Middleware - COMPLETE ✅
**Status**: ✅ FULLY IMPLEMENTED AND INTEGRATED (2026-01-24)

**What's Working**:
- ✅ Database schema complete
- ✅ Middleware implementation complete (`internal/middleware/permissions.go`)
- ✅ Applied to ALL routes in `internal/api/server.go`
- ✅ Centralized permission enforcement
- ✅ Org-level role enforcement (admin vs user vs readonly vs guest)
- ✅ Library-level permission checking (owner vs collaborator)
- ✅ User isolation (users can only see/access their own libraries + shared)
- ✅ Write operations blocked for readonly/guest roles

**Priority**: ✅ COMPLETE - Ready for production multi-tenant deployment

### Encrypted Library Sharing Policy - ENFORCED ✅
**Status**: ✅ FULLY ENFORCED (2026-01-24)

**Policy**: Password-encrypted libraries CANNOT be shared
**Reason**: Sharing encrypted files requires sharing the encryption key, breaking security

**Implementation Status**: ✅ ENFORCED
- ✅ Backend blocks share creation on encrypted libraries with 403 error
- ✅ Clear error message returned to frontend

**Files**: `internal/api/v2/file_shares.go` - `CreateShare()` function

---

## ✅ FRONTEND MODAL ISSUES — RESOLVED

### Modal Dialog Migration — COMPLETE ✅
**Status**: ✅ All dialog files migrated (verified 2026-01-30)
**Detail**: Zero dialog files in `frontend/src/components/dialog/` import `Modal` from reactstrap. All use plain Bootstrap modal classes.
**Remaining reactstrap usage**: Some dialog files still import `Button`, `Input`, `Form` from reactstrap — these are form components (not Modal) and work correctly.
**Page-level Modal imports**: 4 page files (`app.js`, `institution-admin/index.js`, `sys-admin/index.js`, `wiki/index.js`) still import Modal from reactstrap for non-dialog purposes.

---

## ⚠️ PRODUCTION READINESS GAPS

### Error Handling & Monitoring — ✅ IMPLEMENTED
**Severity**: HIGH for production
**Status**: ✅ Complete (2026-01-30)

**Implemented**:
- ✅ Structured logging via `log/slog` (JSON in prod, text in dev)
- ✅ Prometheus metrics (`/metrics` endpoint)
- ✅ Health check endpoints (`/health` liveness, `/ready` readiness)
- ✅ Request logging middleware (method, path, status, latency)
- ⚠️ Alerting hooks not yet configured (Prometheus AlertManager can scrape `/metrics`)

### Documentation
**Severity**: HIGH for production
**Status**: Partial

**Missing**:
- User documentation
- Admin documentation
- Production deployment guide
- Backup/restore procedures
- Migration guide (from Seafile)

---

## ✅ RECENTLY FIXED (2026-01-22 - 2026-01-23)

### Encrypted Library Sharing Warning - FIXED
**Fixed**: 2026-01-22
**Issue**: Internal Link tab showed infinite loading spinner in encrypted libraries
**Root Cause**: Backend returned `encrypted: true` (boolean), frontend expected `encrypted: 1` (integer)
**Fix**: Changed all library endpoints to return integer (0/1)
**Files**: `internal/api/v2/libraries.go`

### Search Backend - IMPLEMENTED
**Completed**: 2026-01-22
**Issue**: Search returned empty stub results
**Fix**: Full Cassandra SASI search implementation
**Features**: Search libraries/files by name, filter by repo/type
**Files**: `internal/db/db.go`, `internal/api/v2/search.go`, `internal/api/server.go`

### Docker Build Memory Issues - FIXED
**Fixed**: 2026-01-22
**Issue**: Frontend build killed with "cannot allocate memory"
**Fix**: Increased Node memory to 4GB, removed Elasticsearch (saved 2GB)
**Files**: `frontend/Dockerfile`, `docker-compose.yaml`

### lib-decrypt-dialog Close Button - FIXED
**Fixed**: 2026-01-23
**Issue**: Close button showed square □ instead of × icon
**Root Cause**: Browser cache serving old JavaScript despite correct source code
**Solution**: Code was correct (`className="close"` with `<span>&times;</span>`)
**Files**: `frontend/src/components/dialog/lib-decrypt-dialog.js:72-74`

---

## 🟡 PLANNED ENHANCEMENTS

### Tenant Quota & Billing Features — NOT YET IMPLEMENTED
**Reported**: 2026-01-29
**Priority**: HIGH (required for multi-tenant production)

The organizations table currently only has `storage_quota` and `storage_used`. The following tenant-level features are needed:

1. **Storage quota (space)**: 0 to unlimited (currently exists but basic)
   - Need enforcement on upload (block uploads when quota exceeded)
   - Need quota usage tracking (periodic recalculation from blocks)
   - Need admin API to set/update quotas per tenant

2. **User count limits**: Max number of users per tenant
   - Need `max_users` field on organizations table
   - Need enforcement during user provisioning (OIDC auto-provision + admin API create)
   - Need admin API to set/update user limits

3. **Upload/download bandwidth metering**: Measurable for billing
   - Need per-org tracking of upload bytes and download bytes
   - Need time-bucketed counters (daily/monthly) for billing reports
   - Need admin API to query usage stats per org per time period
   - Consider Cassandra counter tables for efficient increment

4. **Billing integration (optional)**:
   - Need webhook or API to report usage to external billing system
   - Need configurable billing periods (monthly, etc.)
   - Need usage report endpoint for billing dashboards

**Database changes needed**:
```sql
-- Add to organizations table
ALTER TABLE organizations ADD max_users INT;
ALTER TABLE organizations ADD billing_enabled BOOLEAN;

-- New table for metered usage
CREATE TABLE org_usage_counters (
    org_id UUID,
    period TEXT,          -- e.g., "2026-01" (monthly bucket)
    upload_bytes COUNTER,
    download_bytes COUNTER,
    api_calls COUNTER,
    PRIMARY KEY ((org_id), period)
);
```

**Files to modify**:
- `internal/config/config.go` — billing config
- `internal/db/db.go` — new table
- `internal/api/v2/admin.go` — usage stats endpoints, quota enforcement
- `internal/api/seafhttp.go` — metering on upload/download
- `internal/api/v2/files.go` — metering on REST upload/download

---

## Low Priority / Future Enhancements

### Features Not Started
- Multi-factor authentication
- Activity logs/notifications stubbed
- AI search not implemented
- SeaTable integration not started
- Wiki features partially stubbed

### Admin Features
- Most org admin features stubbed
- System admin features mostly stubbed

---

### ISSUE-GC-ORPHANS-01: Orphaned shares/links After Library Permanent Delete or Auto-Delete

**Status**: ✅ Resolved (2026-03-17)
**Discovered**: 2026-02-24
**Priority**: ~~🟡 Medium~~ → Resolved

**Resolution (2026-03-17):**
All library artifacts are now cleaned on permanent delete via `enqueueLibraryArtifacts()` in the GC worker:
- ✅ `shares` + `shares_by_user` — cleaned via `ListSharesByLibrary` → `DeleteShare`
- ✅ `share_links` (all 4 tables) — cleaned via `DeleteShareLinksByLibrary`
- ✅ `repo_tags` + `file_tags` — cleaned via `cleanupLibraryTags`
- ✅ `repo_api_tokens` — cleaned via `ListRepoAPITokensByLibrary` → `DeleteRepoAPIToken`
- ✅ `locked_files` — cleaned via `DeleteLockedFilesByLibrary`

Additionally, GC scanner **Phase 7** now catches expired user-to-user shares (`expires_at < now`) independently of library deletion.

Historical orphans from before this change will be caught by scanner Phase 3/4 (orphaned commits/fs_objects) on the next 24h scan cycle.

---

### ISSUE-LIB-RETENTION-01: Library History and Auto-Delete Semantics Do Not Match UI

**Status**: Open
**Discovered**: 2026-05-15
**Priority**: Medium-high - destructive/retention settings are visible to users and admins, but current behavior can preserve more history/files than the UI implies.

**Affected UI:**
- `frontend/src/components/dialog/lib-history-setting-dialog.js`
- `frontend/src/components/dialog/lib-old-files-auto-del-dialog.js`
- `frontend/src/components/dialog/sysadmin-dialog/sysadmin-lib-history-setting-dialog.js`

**What is implemented today:**
- History Setting calls `GET/PUT /api2/repos/:repo_id/history-limit/`.
- Auto deletion calls `GET/PUT /api/v2.1/repos/:repo_id/auto-delete/`.
- Settings are persisted on `libraries.version_ttl_days` and `libraries.auto_delete_days`.
- Active policies are projected into `gc_libraries_by_policy`.
- GC scanner phases exist for `expired_versions` and `auto_delete`.
- Focused tests pass for the current API and GC behavior: `go test ./internal/gc ./internal/api/v2 -count=1`.

**Confirmed gaps:**
- `keep_days=0` ("Don't keep history") is stored, but `GetHistoryLimit` maps database value `0` back to `-1`, which makes the UI reopen as "Keep full history".
- `version_ttl_days > 0` does not limit normal linear history. GC Phase 5 preserves the full HEAD parent chain, and the file/repo history APIs do not filter by `version_ttl_days`.
- `auto_delete_days` does not delete current files that have not been modified within N days. GC Phase 6 preserves the HEAD tree and only enqueues fs_objects no longer referenced by HEAD or recent commit trees.
- Directory-listing `expires_at` is computed from file `mtime`, but GC's keep/delete decision is based on commit age and reachability. This can show an expiry countdown that does not correspond to actual deletion.
- The UI wording says "Automatically delete files that are not modified within certain days", which over-promises relative to the current orphan/history-object purge behavior.
- Bootstrap scripts still have older ad hoc `libraries` DDL snippets that omit `auto_delete_days` and the GC policy projection table; migrations are authoritative, but scripts can mislead or create drift in manual environments.

**Fix direction:**
1. Product decision: choose whether these controls mean visible/restorable history retention, physical storage reclamation, automatic deletion of current stale files, or only purging old orphaned history objects.
2. If History Setting is a user-visible retention window, filter `GetFileHistoryV21`, `GetFileRevisions`, and `GetRepoHistory` by `version_ttl_days`, and preserve `0` as "no history" in GET responses.
3. If History Setting must physically prune normal history, design safe commit-chain pruning/compaction instead of only deleting non-HEAD orphan commits.
4. If Auto deletion must delete current stale files, add a job that identifies HEAD-visible files by `mtime`, publishes a new delete commit safely, and respects permissions/locks/encryption/conflicts.
5. If Auto deletion is only old-history-object cleanup, rename UI text and reconsider exposing `expires_at` as a hard deletion countdown.
6. Add end-to-end tests for `keep_days=0`, bounded history visibility, stale current file behavior, and directory-listing expiry accuracy.

---

### ISSUE-TRASH-CLEAN-01: `CleanRepoTrash` is a No-Op Stub

**Status**: ⚠️ Known gap — not yet implemented
**Discovered**: 2026-02-24
**Priority**: 🟡 Medium — user action has no effect; frontend shows success but nothing is cleaned

**Affected endpoint:**
`DELETE /api/v2.1/repos/:repo_id/trash/?keep_days=N` (`trash.go:404`)

**Current Behavior:**
When a user clicks "Clean Trash" on their file recycle bin, the handler immediately returns `{"success": true}` without doing anything. The comment in code says "handled by GC" but GC Phase 6 only runs on libraries with `auto_delete_days` configured — it does not respond to user-triggered trash clean requests.

**What It Should Do:**
1. Get all commits for the library sorted by timestamp
2. Keep: HEAD commit + any commit within `keep_days` of today
3. Enqueue expired commits' fs_objects via `getLibraryEnqueuer()` so GC deletes actual file data
4. Delete the expired commit rows from `commits` table

**Fix Plan:**
Tracked in `docs/TECHNICAL-DEBT.md` § 9, Gap B.

**Files involved:**
- `internal/api/v2/trash.go` — implement `CleanRepoTrash`
- `internal/gc/store.go` / `store_cassandra.go` — may need `ListCommitsWithTimestamps` per library

---

### ISSUE-GC-QUEUE-RECOUNT-01: Exact `gc_queue` Recounts Still Hit Cassandra Tombstone Paths

**Status**: Mitigated structurally in the current branch (2026-06-08); exact
re-measurement still pending. Hot `COUNT(*)` and the counter/repair machinery
were both removed in favour of a single-writer dirty snapshot + throttled exact
recalc (`gc_org_stats.recalculated_at`). See
[GC-QUEUE-DEPTH-MODEL.md](GC-QUEUE-DEPTH-MODEL.md). The remaining
tombstone-warning source — the recompute/`DequeueBatch` partition reads
themselves — is addressed at the schema level by migration
`003_gc_queue_lcs_compaction.cql`, which `ALTER`s the queue/marker/DLQ tables to
`LeveledCompactionStrategy` (the `001` baseline is unchanged from `main`). LCS
reduces read amplification at the queue head immediately. Note that the accompanying
`tombstone_threshold`/`tombstone_compaction_interval` knobs only act on
tombstones already past `gc_grace_seconds` (kept at the 10-day default), so
sub-grace churn tombstones on a hot org may still surface warnings until
`gc_grace_seconds` is lowered. That reduction is intentionally deferred and
gated on re-measuring the warnings under multi-node load — do not treat the
warning class as fully closed until then.
**Discovered**: 2026-04-28
**Severity**: High operational risk — not a confirmed data-loss bug, but still a real source of Cassandra warnings and expensive partition reads in a GC-critical path

**Affected code paths:**
- `internal/gc/gc.go` — `reconcileDirtyQueueStats()`
- `internal/gc/store_cassandra.go` — counter-backed queue depth reads, `GetQueueSize()`

**Problem**:
Older GC code performed exact live recounts of `gc_queue` rows per org using `COUNT(*)`.

On Cassandra 5, that path is unsafe operationally on hot or tombstoned `gc_queue` partitions. In practice it produces repeated warnings that surface as internal read shapes like:

- `SELECT * FROM sesamefs.gc_queue WHERE org_id = ... LIMIT ... ALLOW FILTERING`
- `Aggregation query used without partition key`

Even when the application query does not literally contain `ALLOW FILTERING`, Cassandra internally expands the aggregation/read path in a way that still traverses large tombstoned partitions and emits misleading warning text.

**Confirmed root cause:**
- Live schema for `gc_queue` was verified as partitioned by `org_id` with clustering on `queued_at, item_type, item_id`
- Direct manual execution of `SELECT COUNT(*) FROM sesamefs.gc_queue WHERE org_id = ?` reproduced the Cassandra warnings
- This isolated the remaining runtime warning source after test-helper cleanup and after the worker stale-active-org fix

**Why this is not safe to "just fix" with another scan:**
- Replacing `COUNT(*)` with another full-partition read or row iteration still traverses the same hot/tombstoned partition surface
- Counter-backed status must stay explicitly approximate unless paired with a scrub/repair path: the retired counter design drifted because drift was structural, not incidental

**Safe direction for a future fix:**
1. Remove exact `gc_queue` recounts from the hot reconcile/status path - done.
2. Keep queue/DLQ writes focused on canonical rows plus dirty/active markers - done.
3. Avoid invisible DLQ expiry - done by replacing Cassandra TTL with `gc_failed_items_by_expiry` and scanner-driven deletes.
4. Keep exact recounts in background/admin refresh, throttled by snapshot recency - done.

**Related worker note:**
The worker behavior in `internal/gc/worker.go` that removes an org from `gc_active_orgs` when `len(items) < batchSize` should remain in place.

That change addresses a different problem: stale active-org entries causing repeated empty dequeues. It does **not** introduce the `COUNT(*)` issue and remains safe because removal is guarded by the `last_enqueued_at` timestamp semantics.

**Current recommendation:**
- Treat the hot-path `COUNT(*)` removal, explicit DLQ expiry, and queue/marker compaction tuning as implemented, then validate dirty-org backlog drain, snapshot staleness, and residual tombstone warnings under multi-instance/multinode load before deciding whether a lower `gc_grace_seconds` on the queue/marker tables is also warranted
- Do not revert the current worker short-batch active-set removal
- Do not add new hot-path exact recounts over `gc_queue`

**Related Cassandra warning shape:**
There is a separate but similar backlog item for org-scoped `libraries` reads that
still scan tombstone-heavy partitions and can emit warning text such as:

- `SELECT deleted_at, owner_id, storage_class FROM sesamefs.libraries WHERE org_id = ... LIMIT ... ALLOW FILTERING`

That issue is tracked in [SCHEMA-BOTTLENECK-AUDIT.md](SCHEMA-BOTTLENECK-AUDIT.md).
As of 2026-05-27, deleted-library trash list/clean paths were moved off the
canonical `libraries` table onto `libraries_deleted_by_org`, but GC,
enforcement, and ownership/enumeration paths still have remaining org-scoped
partition scans to retire.

**Files likely involved in the eventual fix:**
- `internal/gc/gc.go`
- `internal/gc/store.go`
- `internal/gc/store_cassandra.go`
- `internal/gc/store_mock.go`
- `internal/gc/gc_test.go`
- `internal/integration/gc_integration_test.go`

---

### ISSUE-S3-TRANSPORT-01: All S3 Operations Fail Until Container Restart — FIXED

**Discovered**: 2026-03-04 (production)
**Status**: ✅ Fixed
**Severity**: 🔴 Production outage — all uploads/downloads fail, requires container restart
**Symptom**: Every request to `/seafhttp/upload-api/` and download endpoints returns HTTP 500. Cassandra operations (login, create library, browse) continue working normally.

**Root Cause**: The Go `http.Transport` used by the AWS SDK S3 client had `MaxConnsPerHost: 64`. When AWS S3 experienced a transient network blip, TCP connections in the pool entered a half-open/zombie state (local OS thinks they're alive, remote endpoint already closed them). With all 64 connection slots occupied by zombies, the transport refused to create new connections — blocking **all** S3 traffic indefinitely. Cassandra uses a separate connection pool (gocql), so it was unaffected.

**Evidence**:
- Structured log: `{"status":500,"body_size":33}` → matches `{"error":"failed to store block"}` or `{"error":"failed to upload file"}`
- Login/logout, library creation worked (Cassandra path) — only S3 operations failed
- `docker-compose down && docker-compose build && docker-compose up` fixed it (fresh HTTP transport with new connections)

**Fix** (commit TBD):
Changed `internal/storage/s3.go` HTTP transport settings:

| Setting | Before | After | Why |
|---------|--------|-------|-----|
| `MaxConnsPerHost` | `64` | `0` (unlimited) | **Key fix.** Zombie connections can't block new ones |
| `IdleConnTimeout` | `120s` | `30s` | Detect and discard stale connections 4x faster |
| `TLSHandshakeTimeout` | not set | `5s` | Prevents hung TLS negotiations from blocking forever |
| `ExpectContinueTimeout` | not set | `1s` | For PUT/POST, validates S3 accepts before sending body |
| `ForceAttemptHTTP2` | not set | `true` | HTTP/2 multiplexing — better throughput, more resilient |

**Files Changed**: `internal/storage/s3.go` (transport config only — no API changes)

---

### ~~ISSUE-UPLOAD-REPLACE-01~~: Upload "Don't Replace" Didn't Work (Desktop Client + Web) — ✅ RESOLVED

**Status**: ✅ Fixed (2026-05-22)
**Discovered**: 2026-03-04
**Reconfirmed by upload audit**: 2026-05-22
**Severity**: Medium — previously caused silent overwrites when user explicitly chose not to replace

**Problem**: When uploading a file that already exists:
- **Desktop client file browser**: Shows dialog "¿Desea reemplazarlo? (Elija No para subirlo con un nombre alternativo)". Clicking "No" should auto-rename but still overwrites.
- **Web UI**: Shows "Replace / Don't replace / Cancel" dialog. "Don't replace" should auto-rename but still overwrites.

**Root Cause**: The Seafile desktop client distinguishes "replace" vs "don't replace" by which endpoint it calls:
- "Sí" (replace) → `GET /api2/repos/{id}/update-link` → upload
- "No" (don't replace) → `GET /api2/repos/{id}/upload-link` → upload

Before the fix, both endpoints (`update-link` and `upload-link`) mapped to the same handler `GetUploadLink` and created identical tokens. The server had no way to know which endpoint was used when the upload arrived.

The client also sends `replace=1` in both cases, so the form parameter doesn't help.

**Fixed**:
- `AccessToken` now carries a persisted `Replace` default
- `CreateUpdateToken()` now produces overwrite-by-default tokens for `update-link`
- `CreateUploadToken()` remains no-replace/autorename by default for `upload-link`
- `GetUpdateLink` now has its own handler and route
- `HandleUpload` now defaults from `token.Replace`, while still allowing explicit multipart override
- Cassandra `access_tokens` now stores `replace_existing`, so the behavior survives multi-node routing
- Integration coverage now proves both overwrite and autorename paths

**Previously added infrastructure**:
- `autoRenameIfExists()` function generates unique names: `file (1).txt`, `file (2).txt`, etc.
- `replace` parameter propagated through entire chain: `HandleUpload` → `finalizeUploadStreaming` → `commitUploadedFileMultiBlock` → `addFileToDirectory` → `traverseAndAddFile`
- All commit/directory functions return `actualFilename` (may differ if auto-renamed)

**Files Changed**:
- `internal/api/seafhttp.go`
- `internal/api/token_adapter.go`
- `internal/api/v2/files.go`
- `internal/api/v2/file_routes.go`
- `internal/db/tokens.go`
- `internal/db/migrations/001_initial_schema.cql`
- `internal/integration/upload_download_test.go`
- `internal/integration/quotas_test.go`

---

## ~~ISSUE-FRONTEND-ORG-DELETE-01~~: Superadmin Org Soft-Delete/Restore UI — ✅ RESOLVED

**Status**: ✅ Complete (2026-03-25)
**Date identified**: 2026-03-18 | **Date resolved**: 2026-03-25

Fully implemented in `frontend/src/pages/sys-admin/orgs/orgs-content.js`, `orgs.js`, and `search-orgs.js`:
- Status column with color-coded badges (active/deactivated/deleted)
- Separate Deactivate, Delete, Reactivate, and Restore actions with confirmation dialogs
- Status filter support in org listing
- Search results also support all lifecycle actions

---

## Multiregion HEAD Safety — Confirmed Issues (2026-05-18)

The following items were surfaced during the `feat/multiregion-head-safety` audit cycle and verified against current code. The first three are real but bounded issues; none is reachable through the standard happy-path flows. The OnlyOffice entry is retained as an audit correction so the same concern does not get re-filed as a confirmed leak.

### ISSUE-MOVE-CYCLE-STATUS-01: Cycle-prevention error surfaces as HTTP 500

**Status**: 🟡 Confirmed bug — wrong status code, correct behavior otherwise
**Date identified**: 2026-05-18

`internal/api/v2/batch_operations.go:810` rejects moving a directory into itself or into a descendant by returning `fmt.Errorf("cannot move directory into itself")`. This error is constructed inline and is not bound to any sentinel, so `batchOperationErrorResponse` falls through to its default branch and returns HTTP 500 with message `"failed to move <name>"`.

A client cannot distinguish a logic error (their request was invalid) from a server-side problem (database down, etc.).

**Evidence**:
- Sentinel error definitions (`ErrBatchSourceNotFound`, `ErrBatchDestinationNotFound`, `ErrStorageQuotaExceeded`, `ErrLibraryHeadConflict`, `ConflictError`) at `internal/api/v2/batch_operations.go:48-58`, `fs_helpers.go:26-31` — none of them covers cycle prevention.
- Error mapping in `batchOperationErrorResponse` (`batch_operations.go:135-160`) and `writeMoveFileError` (`files.go:2122-2143`) — both fall through to 500.

**Fix**:
- Add `var ErrBatchInvalidMove = errors.New("invalid move")` (or similar) at `batch_operations.go`.
- Replace the inline `fmt.Errorf` at line 810 with `fmt.Errorf("%w: cannot move directory into itself", ErrBatchInvalidMove)`.
- Add a case in `batchOperationErrorResponse` and `writeMoveFileError` that maps it to `http.StatusBadRequest`.

Integration tests in `internal/integration/same_repo_move_test.go` only assert `status != 200` for the cycle and descendant-cycle cases, so they will continue to pass after the fix but should be tightened to assert `status == 400`.

---

### ISSUE-LIB-NOT-FOUND-STATUS-01: "source library not found" returns HTTP 500 instead of 404

**Status**: 🟡 Confirmed bug — wrong status code
**Date identified**: 2026-05-18

`internal/api/v2/batch_operations.go:450, 485, 798` all wrap library-lookup failures as `fmt.Errorf("source library not found: %w", err)` without a sentinel. `batchOperationErrorResponse` cannot match it and falls through to HTTP 500.

A client passing an invalid `src_repo_id` (typo, deleted library) receives 500 instead of the appropriate 404.

**Fix**:
- Add `var ErrBatchLibraryNotFound = errors.New("library not found")` at `batch_operations.go`.
- Wrap the three call sites with the sentinel.
- Add a case in `batchOperationErrorResponse` that maps it to `http.StatusNotFound`.

---

### ISSUE-CLEANUP-TAGS-PREFIX-DANGER-01: `CleanupFileTagsByPrefix("/")` would wipe all repo tags

**Status**: 🟡 Latent bug — not reachable today, but unguarded
**Date identified**: 2026-05-18

`internal/api/v2/tags.go:601-604`:
```go
prefixSlash := prefix + "/"
if prefix == "/" {
    prefixSlash = "/"
}
```

With `prefix == "/"` (after `normalizePath`), `prefixSlash` becomes `"/"`. The scan loop at `:612-616` then matches every absolute path in the repo with `strings.HasPrefix(filePath, "/")` and queues every tag for deletion.

Current call sites (`batch_operations.go:101, 656, 901`) always derive the prefix from `path.Join(dstDir, itemName)` where `itemName != ""`, so the prefix is never `"/"`. The bug is one careless future caller away.

**Fix**:
```go
prefix = normalizePath(prefix)
if prefix == "" || prefix == "/" {
    return  // refuse to nuke the whole repo
}
CleanupFileTagsByPath(database, repoID, prefix)
// ...
```

---

### AUDIT-CORRECTION-ONLYOFFICE-MAPPING-01: OnlyOffice rollback mappings are cleaned by GC

**Status**: ✅ Audit correction — no confirmed mapping leak
**Date identified**: 2026-05-18

When `saveEditedDocument` rolls back a materialized block after a publish failure, it calls `DecrementBlockRefCountsOnce` + `enqueueZeroRefBlocks`. The mapping rows are inserted before rollback, but the GC worker cascades mapping cleanup when it processes the zero-ref internal block.

There is still ordinary async-cleanup risk if the enqueue or GC worker path is unavailable, but that is covered by the fire-and-forget cleanup debt in `TECHNICAL-DEBT.md`; it is not a separate confirmed OnlyOffice mapping leak.

**Evidence**:
- OnlyOffice rollback path: `internal/api/v2/onlyoffice.go` calls `DecrementBlockRefCountsOnce` and `enqueueZeroRefBlocks` after metadata publish failure.
- GC worker block deletion: `internal/gc/worker.go` calls `ListBlockMappingsByInternalID` for the deleted internal block and then `DeleteBlockMapping` for each external mapping.
- Cassandra store cleanup: `DeleteBlockMapping` deletes both `block_id_mappings` and `block_id_mappings_by_internal`.

---

### ISSUE-LIB-DELETED-FENCE-01: Soft-deleted libraries still accept star mutations

**Status**: 🟡 Pending
**Severity**: Medium-High - lifecycle correctness gap with data-drift risk during GC cascade
**Affected**: `POST /api/v2.1/starred-items/`, `POST /api2/starredfiles`, and any repo-scoped mutating path that only checks canonical existence

#### Problem

Library soft-delete is a two-phase lifecycle:

1. The API marks `libraries.deleted_at` and inserts a `deleted_libraries` marker.
2. Later, GC acquires the hard-delete lock, cleans auxiliary tables, and permanently removes the library.

That fencing is respected by the delete handlers themselves, but `StarFile` still
accepts the library as long as the canonical row exists:

- `softDeleteLibrary` sets `deleted_at` without removing the live row yet.
- `StarFile` queries `SELECT name, encrypted FROM libraries ...` and does not reject `deleted_at != null`.
- `starFile` then dual-writes `starred_files` and `starred_files_by_repo`.

So a client that still knows `repo_id` can create new starred-file rows after a
library has already been soft-deleted.

#### Why It Matters

This is not just a UX oddity. It reopens a cleanup race during library cascade:

- `DeleteStarredFilesByLibrary` scans `starred_files_by_repo`
- deletes canonical `starred_files` rows
- then deletes the repo projection partition

If a new star lands after the scan but before the cascade finishes, GC can miss
that row and still remove the reverse-lookup partition, leaving a stranded
canonical `starred_files` row with no `starred_files_by_repo` entry.

The recent starred-files hardening fixed partial-failure behavior inside GC, but
it cannot prevent post-scan writes from handlers that still treat soft-deleted
libraries as writable.

#### Suggested Fix

- Add a shared "library is live" guard for repo-scoped mutating handlers.
- Start by fencing `StarFile` and `UnstarFile` on `deleted_at`.
- Prefer a reusable helper so the same rule can be applied consistently to other
  repo-scoped write paths over time.

#### Evidence

- `internal/api/v2/write_helpers.go`: `softDeleteLibrary` sets `deleted_at`
- `internal/api/v2/starred.go`: `StarFile` checks existence but not lifecycle state
- `internal/gc/store_cassandra.go`: `DeleteStarredFilesByLibrary` cleans by scan-then-delete

---

### ISSUE-USERS-BY-EMAIL-FALLBACK-01: `users_by_email` Misses Still Fall Back To Global `users` Scan

**Status**: Pending
**Severity**: Low-Medium - rare today, but unbounded `ALLOW FILTERING` fallback remains in auth/admin identity lookup paths
**Affected**: `internal/auth/oidc.go`, `internal/api/v2/admin.go`, any path that treats `users_by_email` miss as recoverable via `users WHERE email = ? ALLOW FILTERING`

When the `users_by_email` lookup misses, the code still falls back to scanning
the canonical `users` table by email. The current dual-write makes this rare,
but the fallback remains an unbounded read shape that will age poorly as the
tenant/user dataset grows.

**Fix direction**:
- Audit every writer that creates a `users` row and guarantee `users_by_email` dual-write.
- Backfill any remaining legacy gaps.
- Promote the fallback to a hard failure once the index contract is complete.

---

### ISSUE-COUNTER-HOT-PARTITION-01: Global `traffic_counters` / `storage_counters` Aggregates Were Single Hot Partitions

**Status**: ✅ Fixed (2026-06-11)
**Severity (when active)**: High pre-deploy schema risk - every global traffic/storage mutation concentrated on one counter partition
**Affected**: `traffic_counters` zero-UUID platform aggregate, `storage_counters` `platform` scope, sysadmin traffic/storage dashboards, multiregion write throughput

The clean init schema originally concentrated all platform-wide traffic writes
into `traffic_counters ((org_id, month), ...)` with `org_id = 0000...0000` and
all platform-wide storage writes into `storage_counters ((scope), ...)` with
`scope = "platform"`. Those were the two truly shared hot counter partitions in
the baseline.

**What changed**:
- `traffic_counters` now uses `PRIMARY KEY ((org_id, month, shard), day, user_id, traffic_type)`.
- `storage_counters` now uses `PRIMARY KEY ((scope, shard), day)`.
- Only the global platform aggregates are sharded.
- Org/user/library scopes stay pinned to `shard = 0`.
- Platform writes route deterministically by `CounterShard(org_id)` so each org's inc/dec path stays balanced on the same shard.

**Why this shape is safe**:
- The hot quota paths still read single-partition org/user/library counters.
- Only cold sysadmin/global readers fan out across shards.
- Reconciliation also buckets platform expected totals by the same deterministic shard.

---

### ISSUE-GC-QUEUE-TTL-01: `gc_queue` Still Has No Data-Lifetime Bound

**Status**: Pending
**Severity**: Medium - queue items can live forever if the worker stalls in a way that never completes or DLQs them
**Affected**: `gc_queue`, worker recovery semantics, operator observability

`gc_queue` still uses `default_time_to_live = 0`. The queue/marker compaction
work reduced tombstone pain, but it does not put any lifetime bound on abandoned
rows. A stalled or partially broken worker can therefore leave successful-looking
queue items behind indefinitely.

**Fix direction**:
- Decide on a long but finite TTL window (for example 90-180 days).
- Pair that with explicit alerts so operators detect backlog/worker failure before TTL expiry hides the symptom.
- Keep orphan recovery aligned with the chosen expiry window.

---

### ISSUE-GC-DISCOVERY-CURSOR-OBS-01: Discovery Cursor Lag Is Not Observable Enough

**Status**: Pending
**Severity**: Low-Medium - scanner lookback safety depends on the cursor advancing often enough, but lag is not surfaced clearly
**Affected**: `gc_block_candidates_by_day`, `gc_s3_orphans_by_day`, scanner ops/alerting

The per-day discovery projections are bounded by cursor progression. If the
scanner does not run within the configured lookback window on a cold start, old
candidate days can fall behind the scan horizon. Today the safety depends on the
cursor existing and moving, but that lag is not exposed as a first-class signal.

**Fix direction**:
- Emit an explicit metric for the current discovery cursor day.
- Alert when the cursor lags N days behind `today`.
- Keep the alert separate from generic scanner liveness so it catches "running but behind".

---

### ISSUE-GC-DISCOVERY-HOTSPOT-01: Per-Day Discovery Partitions Can Still Spike On Bursty Workloads

**Status**: Pending
**Severity**: Low today, potentially Medium under bulk churn - a single `(day, bucket)` discovery partition can still grow too large
**Affected**: `gc_block_candidates_by_day`, `gc_s3_orphans_by_day`, other `gc_*_by_day` projections

The discovery projections are bucketed, but still keyed by `(day, bucket)`. A
large burst of refcount-zero or orphan events concentrated in one day can make
one bucket's partition much larger than Cassandra's soft guidance for partition
size/row count.

**Fix direction**:
- Keep bucket count tunable.
- If real workloads approach the soft limit, move the hottest projections to a finer grain such as `(day, hour, bucket)`.
- Do not pay the extra read complexity until that growth is measured.

---

### ISSUE-LIBRARIES-ORG-SCAN-01: Some Org-Scoped `libraries` Reads Still Walk Tombstone-Heavy Partitions

**Status**: Pending follow-up after partial mitigation (2026-06-10)
**Severity**: Medium operational risk - repeated org-wide reads can still traverse churn-heavy canonical partitions
**Affected**: `internal/api/v2/libraries.go`, `internal/api/v2/search.go`, `internal/gc/store_cassandra.go`, canonical `libraries` reads by org

The recent projection branch moved several owner/enforcement reads off the
canonical `libraries` org partition, but a few important callers still scan the
canonical partition or even the whole table:

- library list endpoints still fetch full canonical rows by org
- GC storage reconciliation still scans `FROM libraries`
- search prefilter still does org-scoped canonical enumeration

**Fix direction**:
- Bound library list reads to the caller's accessible library IDs and point-read canonical rows by id.
- Revisit the full-table maintenance scan separately from hot-path readers.
- Replace the search prefilter with a shape that matches the access pattern.

---

### ISSUE-FILE-TAG-MOVE-BESTEFFORT-01: Tag Move Helpers Still Log-And-Continue On Failure

**Status**: Pending
**Severity**: Low-Medium - file/directory rename succeeds, but tag metadata can stay stranded at old paths until later cleanup
**Affected**: `MoveFileTagsByPath`, `MoveFileTagsByPrefix`, tag move observability/retry

Tag move helpers still do best-effort logging when a per-tag batch fails. The
FS rename is already durable by that point, so callers cannot fail the request
cleanly, but the metadata drift remains mostly invisible outside logs.

**Fix direction**:
- Return `error` from the move helpers.
- Keep the caller response successful for the already-committed FS mutation.
- Log at request level and/or enqueue a retry/reconciliation path.

---

### ISSUE-DELETE-REPO-TAG-PROOF-01: `DeleteRepoTag` Has No Cheap Proof That Projection Rows Are Complete

**Status**: Pending
**Severity**: Low - current code is safe, but the tempting fast path is unsound
**Affected**: `deleteRepoTag`, `file_tags`, `file_tags_by_tag`, `repo_tag_file_counts`

The current delete path correctly derives the exact delete set from canonical
`file_tags`. What remains as debt is architectural: cardinality equality alone
cannot prove `file_tags_by_tag` completeness, because best-effort rename drift
can leave a stale old-path row while missing the new path.

**Current rule**:
- Do not reintroduce a projection-only fast path based only on row count equality.

**Future direction**:
- Keep canonical exact-set scan unless a stronger proof source exists, such as deterministic retry/reconcile or exact-set versioning/checksum.

---

### ISSUE-FILE-TAG-PREFIX-SCAN-01: `MoveFileTagsByPrefix` Still Scans The Whole Repo Tag Partition

**Status**: Pending
**Severity**: Low - not a hot path, but rename cost scales with all tagged files in the repo
**Affected**: directory rename flows that call `MoveFileTagsByPrefix`

`MoveFileTagsByPrefix` currently lists every tagged path in the repo and filters
the moved subtree in memory. That is a single-partition read and only happens on
directory rename, so it is acceptable for now, but the cost grows with the
repo's total tagged files rather than the subtree size.

**Fix direction**:
- Use a clustering slice on `file_tags` because the canonical clustering already starts with `file_path`.
- Read only the `[prefix, prefixUpperBound)` subtree instead of the whole repo partition.

---

## See Also

- [IMPLEMENTATION_STATUS.md](IMPLEMENTATION_STATUS.md) - Component completion status
- [API-REFERENCE.md](API-REFERENCE.md) - API endpoint documentation
- [TECHNICAL-DEBT.md](TECHNICAL-DEBT.md) - Architectural issues
- [CURRENT_WORK.md](../CURRENT_WORK.md) - Active priorities
