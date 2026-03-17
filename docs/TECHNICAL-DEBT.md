# Technical Debt & Migration Plan

This document tracks known technical debt and provides actionable plans for addressing each issue while the system remains in use.

---

## 1. Multi-Host ServiceURL — ✅ FIXED (2026-02-09)

### Status
The frontend now uses `window.location.origin` by default for API calls, enabling multi-tenant deployments where different hostnames (us.sesamefs.com, eu.sesamefs.com) route to the same system.

### What Was Done
- `frontend/public/index.html`: `serviceURL` set to `window.SESAMEFS_API_URL || ''` (empty by default)
- `frontend/src/utils/seafile-api.js`: Fallback `const server = serviceURL || window.location.origin` handles the empty case
- `docker-compose.yaml`: `SESAMEFS_API_URL=` is empty for production builds
- `docker-entrypoint.sh`: Only injects `SESAMEFS_API_URL` when explicitly set at container runtime
- No hardcoded `localhost` references remain in `frontend/src/`

### Result
- `https://us.sesamefs.com` → API calls go to `https://us.sesamefs.com/api/...`
- `https://eu.sesamefs.com` → API calls go to `https://eu.sesamefs.com/api/...`
- Dev mode: Set `SESAMEFS_API_URL` env var or use nginx proxy (frontend port 3001 → backend port 8082)

---

## 2. Modal Pattern — ✅ MIGRATION COMPLETE (2026-01-30)

### Status
All 122 modal dialog components have been migrated from reactstrap `<Modal>` to plain Bootstrap modal classes. Zero dialog files import `Modal` from reactstrap.

### Remaining Cleanup: ModalPortal Wrapper Removal
~51 parent components still wrap already-fixed dialog components in `<ModalPortal>`. This is harmless (dialogs render correctly) but unnecessary. Remove wrappers opportunistically when touching these files.

**Before** (unnecessary wrapper):
```jsx
{this.state.isDialogOpen && (
  <ModalPortal>
    <SomeDialog toggle={this.toggle} />
  </ModalPortal>
)}
```

**After** (direct render):
```jsx
{this.state.isDialogOpen && (
  <SomeDialog toggle={this.toggle} />
)}
```

Parent components with `<ModalPortal>` wrappers are in:
- `components/dirent-list-view/`
- `components/dirent-grid-view/`
- `components/toolbar/`
- `components/user-settings/`
- `pages/sys-admin/`
- `pages/org-admin/`
- `pages/groups/`
- `pages/my-libs/`
- `pages/wikis/`

---

## 3. seafile-js Hardcoded Paths (NO ACTION NEEDED)

### Problem
The `seafile-js` npm package has hardcoded API paths that cannot be changed without forking.

### Impact
- Backend MUST implement exact Seafile API paths
- Cannot use custom API prefixes

### Solution
**This is acceptable** - we're building a Seafile-compatible API, so matching their paths is intentional.

### Documented Constraints
The backend must implement these exact paths (from seafile-js):
| Method | Path |
|--------|------|
| listRepos | `GET /api/v2.1/repos/` |
| deleteRepo | `DELETE /api/v2.1/repos/:id/` |
| listDir | `GET /api/v2.1/repos/:id/dir/?p=:path` |
| lockfile | `PUT /api/v2.1/repos/:id/file/?p=:path` |
| etc. | See docs/API-REFERENCE.md |

---

## 4. Test Coverage (ONGOING)

### Current State
| Package | Coverage | Target |
|---------|----------|--------|
| `internal/config` | 92.5% | Maintain |
| `internal/chunker` | 79.2% | Maintain |
| `internal/storage` | 46.6% | 60% |
| `internal/api` | 17.5% | 40% |
| `internal/api/v2` | 16.3% | 40% |

### Strategy: Test As You Fix

When fixing a bug or adding a feature:
1. Write a test that reproduces the issue
2. Fix the issue
3. Verify test passes
4. Commit both together

### High-Value Tests to Add

**1. API Handler Tests** (`internal/api/v2/*_test.go`)
```go
// Test request validation
func TestCreateLibrary_EmptyName(t *testing.T) {
    // Should return 400 Bad Request
}

// Test authorization
func TestDeleteLibrary_NotOwner(t *testing.T) {
    // Should return 403 Forbidden
}
```

**2. Integration Tests** (with mock DB)
```go
// Test full flow with mocked dependencies
func TestUploadDownloadRoundtrip(t *testing.T) {
    // Upload file, verify stored, download, verify contents
}
```

**3. Frontend Tests** (`frontend/src/**/__tests__/`)
```javascript
// Test API client error handling
describe('seafile-api', () => {
    it('handles 401 by redirecting to login', async () => {
        // ...
    });
});
```

### CI Integration
Add to `.github/workflows/test.yml`:
```yaml
- name: Check coverage threshold
  run: |
    go test ./... -coverprofile=coverage.out
    COVERAGE=$(go tool cover -func=coverage.out | grep total | awk '{print $3}' | sed 's/%//')
    if (( $(echo "$COVERAGE < 25" | bc -l) )); then
      echo "Coverage $COVERAGE% is below threshold 25%"
      exit 1
    fi
```

---

## 5. Frontend Features Pending

### Authentication & Session
| Feature | Status | Notes |
|---------|--------|-------|
| Logout button | ✅ Working | `/accounts/logout/` clears localStorage |
| Session management | ⚠️ Basic | Dev tokens only, no OIDC yet |

### Notifications
| Feature | Status | Notes |
|---------|--------|-------|
| `/api/v2.1/notifications/` | ⚠️ Stub | Returns empty array |
| Real-time notifications | ❌ Not implemented | Would need WebSocket or polling |
| Activity feed | ❌ Not implemented | `/api2/events/` not implemented |

### Sharing Features
| Feature | Status | Notes |
|---------|--------|-------|
| "Shared with me" page | ⚠️ Shows own libs | Needs filter by `type: "shared"` |
| Share dialog | ⚠️ Modal shows | Backend share endpoints are stubs |
| Move/Copy dialogs | ⚠️ Modal shows | Backend move/copy partially implemented |
| Groups | ⚠️ Stub | `/api/v2.1/groups/` returns empty |

### File Viewer
| Feature | Status | Notes |
|---------|--------|-------|
| OnlyOffice (docx, xlsx, pptx) | ✅ Working | Full editing support, auth token handling fixed (2026-02-12) |
| New Office file creation | ✅ Working | Creates with valid template (not 0 bytes) |
| Images (jpg, png, etc.) | ✅ Working | Inline preview via `/lib/:id/file/*path`, raw serving via `/repo/:id/raw/*path` |
| PDF viewer | ✅ Working | Inline `<embed>` preview implemented (2026-02-12) |
| Video/Audio player | ✅ Working | Inline HTML5 video/audio players implemented (2026-02-12) |
| Text file viewer | ✅ Working | Code-highlighted text preview with syntax support (2026-02-12) |
| Thumbnails | ❌ Not implemented | `/thumbnail/` endpoint missing |

### Library Settings Dialogs
| Dialog | Status | Notes |
|--------|--------|-------|
| History settings | ✅ Complete | Full CRUD implemented |
| Auto-delete settings | ✅ Complete | Full CRUD implemented |
| API tokens | ✅ Complete | Full CRUD implemented |
| Transfer ownership | ✅ Complete | Backend implemented |

---

## 6. Programmatic Auth Gap — ⚠️ BLOCKS PROD (2026-02-18)

### Problem

In production (`dev_mode=false`, OIDC-only), there is **no way to get an API token
programmatically** — without a browser. This blocks two critical scenarios:

1. **Seafile desktop/mobile client login** — the client calls
   `POST /api2/auth-token/` with username+password to get a session token.
   In prod this endpoint always returns `401` (there is a `// TODO` in the code).

2. **Programmatic/admin access** — scripts, CI pipelines, the `seaf-cli` tool,
   or any API consumer that can't open a browser has no way to authenticate.

### Root Cause

`internal/api/server.go` — `handleAuthToken` function:

```go
// In dev mode: checks dev tokens by username match → works
if s.config.Auth.DevMode {
    // ... matches dev tokens ...
}

// In prod: TODO, falls through to 401
// TODO: Implement OIDC password grant or redirect to OIDC flow
c.JSON(http.StatusUnauthorized, gin.H{
    "non_field_errors": "Unable to login with provided credentials.",
})
```

### What Exists Today

| Method | Status | Notes |
|---|---|---|
| `POST /api2/auth-token/` username+password | ❌ Broken in prod | TODO in server.go |
| OIDC browser flow (`/api/v2.1/auth/oidc/login`) | ✅ Works | Browser only |
| Library-scoped API tokens | ✅ Works | Requires browser login first; single-library scope |
| Personal Access Tokens (user-level) | ❌ Not implemented | - |
| OIDC Device Flow (RFC 8628) | ❌ Not implemented | Best fit for CLI tools |
| OIDC Client Credentials grant | ❌ Not implemented | For service-to-service |

### Impact

- Seafile desktop client **cannot log in** in production → sync is broken
- `seaf-cli` **cannot authenticate** without dev tokens
- Admin scripts **cannot automate** API calls
- Users **cannot get tokens** without a browser

### Solutions (pick one or combine)

**Option A — OIDC Device Authorization Flow (RFC 8628)** ← Recommended

The cleanest long-term solution for CLI tools and headless clients:
1. Client calls `POST /api2/auth-token/` → server responds with a device code + URL
2. User opens the URL in a browser, approves
3. Client polls until approved → gets session token

Requires the OIDC provider (`accounts.sesamedisk.com`) to support Device Flow.

**Option B — Personal Access Tokens (PATs)**

Admin/users generate long-lived tokens via the web UI or admin API.
The token is a random string stored in Cassandra, scoped to the user (not a library).

- Implementation: ~200 lines in a new `internal/api/v2/access_tokens.go`
- Endpoints: `POST/GET/DELETE /api/v2.1/user/access-tokens/`
- Storage: new `personal_access_tokens` Cassandra table

**Option C — Allow OIDC-issued tokens in `/api2/auth-token/`**

After the user completes browser OIDC login, generate a longer-lived token they
can copy and use for CLI/API access. Simpler than PATs, less elegant.

### Workaround for Current Testing Phase

Keep `AUTH_DEV_MODE=true` with specific dev tokens while testing in prod.
This unblocks desktop client and CLI testing at the cost of real OIDC auth.

```bash
# In .env — temporary workaround while PATs / Device Flow are not implemented:
AUTH_DEV_MODE=true
AUTH_ALLOW_ANONYMOUS=false
# dev tokens defined in config.prod.yaml → auth.dev_tokens
```

### Priority

**High** — blocks any non-browser client (desktop sync, CLI, automation).
Must be resolved before promoting to general availability.

---

## 7. Fake `UUID@sesamefs.local` Emails — ⚠️ Partially Fixed (2026-02-22)

### Status

Several endpoints were hardcoding a fake email (`userID + "@sesamefs.local"`) instead of querying the real user email from the `users` table. This was a dev shortcut that leaked into production paths.

### What Was Fixed

A `resolveOwnerEmail(orgID, userID string) string` method was added to `LibraryHandler`. It performs `SELECT email FROM users WHERE org_id = ? AND user_id = ?` and falls back to `UUID@sesamefs.local` only when the user genuinely doesn't exist in the DB.

Fixed in `internal/api/v2/libraries.go` (5 occurrences: `ListLibraries`, `GetLibraryDetail`, `ListLibrariesV21`, `GetLibraryDetailV21`, `CreateLibrary`) and `internal/api/v2/deleted_libraries.go` (`ListDeletedRepos`).

### Remaining: Display Fields (Safe to Fix, Low Risk)

These return incorrect data to the client but do not affect stored data. Fix by using a similar `resolveOwnerEmail`-style DB lookup.

| File | Line(s) | Context |
|------|---------|---------|
| `internal/api/v2/files.go` | 1493 | `GetFileDetail` response |
| `internal/api/v2/files.go` | 2557 | Sync token response `"email"` field |
| `internal/api/v2/files.go` | 3384, 3525, 3669 | File version history `CreatorEmail` |
| `internal/api/seafhttp.go` | 1860 | Download-info `"email"` field |
| `internal/api/v2/starred.go` | 127, 258 | Starred files response `userEmail` |

### Remaining: FS Object Modifier (Risky — Needs Decision)

The `modifier` field is part of the Seafile FS object hash (`fs_id`). Changing it for future uploads is safe (existing objects are immutable and content-addressed), but creates a mixed state in history where old entries have UUID-emails and new ones have real emails.

| File | Line(s) | Context |
|------|---------|---------|
| `internal/api/seafhttp.go` | 1001, 1036, 1098 | `"modifier"` in FS objects during upload |
| `internal/api/v2/onlyoffice.go` | 716, 730 | `Modifier` in FS objects (comment: affects `fs_id` hash) |
| `internal/api/sync.go` | 500 | `commit.CreatorName` in Seafile commit binary format |

**Decision needed before touching these**: accept mixed-state history or not?

### Legitimate Uses (Do Not Change)

| File | Why OK |
|------|--------|
| `internal/api/v2/admin.go:1681` | Fallback INSIDE `resolveOwnerEmail` — correct by design |
| `internal/api/v2/monitored_repos.go:93` | Already queries DB first; fallback only |
| `internal/api/server.go:1148` | Dev-mode token auth — parses `UUID@sesamefs.local` as login format intentionally |
| `internal/db/seed.go` | Seed / test data |

### See Also

`docs/KNOWN_ISSUES.md` — ISSUE-EMAIL-01 for full table of affected locations.

---

## Summary: Action Items

### Immediate (This Week)
- [x] **Fix serviceURL** - Changed to use `window.location.origin` ✅ (2026-02-09)
- [ ] **Document modal pattern** in CLAUDE.md (done)

### High Priority — Blocks Production
- [ ] **Programmatic auth gap** (Section 6) — Seafile clients and CLI cannot auth in OIDC-only mode.
  Implement Personal Access Tokens (PATs) or OIDC Device Flow.
  Workaround: keep `AUTH_DEV_MODE=true` with specific dev tokens during testing phase.

### Short-Term (As Encountered)
- [ ] Fix dialogs as users report issues
- [ ] Add tests when fixing bugs
- [ ] Fix remaining fake-email display fields (Section 7) — `files.go`, `seafhttp.go`, `starred.go`

### Long-Term (Backlog)
- [ ] Migrate all ~100 dialogs (can be automated with script)
- [ ] Increase test coverage to 40%
- [ ] Consider forking seafile-js for any customization needs
- [ ] OIDC JWT signature verification (Section 6)
- [ ] Decide on FS object modifier fix (Section 7) — accept mixed-state history or leave as-is

---

## 8. SeafHTTP Token Auth: 403 → 401 — ✅ FIXED (2026-02-22)

### Status
Fixed. SeafHTTP endpoints (`HandleUpload`, `HandleDownload`, `HandleZipDownload`) now return `401 Unauthorized` instead of `403 Forbidden` for invalid/expired operation tokens. The `authMiddleware` also returns a specific `"session expired"` error when the session validation fails due to expiry, rather than the generic `"invalid token"`.

### What Was Wrong
- `seafhttp.go` returned `403` for expired upload/download/zip tokens — incorrect HTTP semantics (403 = "you're authenticated but lack permission", 401 = "not authenticated, please re-authenticate")
- `server.go` `authMiddleware` swallowed the "session expired" error from `ValidateSession()` and returned a generic `"invalid token"`, making it impossible for the frontend to distinguish expired sessions from bad credentials

### What Was Done
- `internal/api/seafhttp.go`: 3 locations changed from `http.StatusForbidden` → `http.StatusUnauthorized`
- `internal/api/server.go`: `authMiddleware` checks `strings.Contains(err.Error(), "expired")` after `ValidateSession` fails and returns `401 {"error": "session expired"}` immediately

### Result
- Frontend global 401 interceptor can now reliably catch session expiry across all endpoint types
- Clients can distinguish "re-authenticate" (401) from "no permission" (403)

---

## 9. Library Deletion: Cleanup Paths — ✅ RESOLVED (2026-03-17), Gap B pending

### Status
~~Known gaps. Fully documented 2026-02-24. Implementation planned (see below).~~
**Resolved** (2026-03-17): GC worker now cleans all library artifacts (shares, share links, tags, API tokens, locked files) via `enqueueLibraryArtifacts`. Scanner Phase 7 cleans expired user-to-user shares. Only Gap B (`CleanRepoTrash` stub) remains.

### Overview

There are **three code paths** that should clean up library-related data but each has a different deficiency:

| Path | Endpoint | File/object data | `shares` | `share_links` | `upload_links` |
|------|----------|-----------------|----------|---------------|----------------|
| User permanent delete | `DELETE /repos/deleted/:id/` | ✅ GC async | ✅ GC async (artifacts) | ✅ GC async (artifacts) | ✅ GC async (artifacts) |
| Admin bulk clean | `DELETE /admin/trash-libraries/` | ✅ GC async | ✅ GC async (artifacts) | ✅ GC async (artifacts) | ✅ GC async (artifacts) |
| GC auto-delete scanner (Phase 6) | background / `auto_delete_days` | ✅ fs_objects enqueued | ✅ Phase 7 (shares) / artifacts (links) | ✅ via artifacts | ✅ via artifacts |
| User file-trash clean | `DELETE /repos/:id/trash/` | **stub — nothing** | — | — | — |

---

### Gap A: Orphaned `shares`, `share_links` ~~, `upload_links`~~ — ✅ RESOLVED (2026-03-17)

**Fully resolved** (2026-03-17): GC worker `enqueueLibraryArtifacts()` now cleans all library artifacts on permanent delete:

| Table | Partition Key | Status |
|-------|---------------|--------|
| `shares` + `shares_by_user` | `library_id` | ✅ **Resolved** — `ListSharesByLibrary` → `DeleteShare` in GC worker |
| `share_links` (all 4 tables) | `link_token` | ✅ **Resolved** — `DeleteShareLinksByLibrary` in GC worker |
| `repo_tags` + `file_tags` | `library_id` | ✅ **Resolved** — `cleanupLibraryTags` in GC worker |
| `repo_api_tokens` | `library_id` | ✅ **Resolved** — `ListRepoAPITokensByLibrary` → `DeleteRepoAPIToken` |
| `locked_files` | `repo_id` | ✅ **Resolved** — `DeleteLockedFilesByLibrary` |

Additionally, scanner **Phase 7** catches expired user-to-user shares independently.

---

### Gap B: `CleanRepoTrash` is a stub

`DELETE /api/v2.1/repos/:repo_id/trash/` (`trash.go:404`) is the endpoint the frontend calls when a user clicks "Clean Trash" on their file recycle bin. The handler currently does nothing:

```go
// For now, we acknowledge the request — actual commit pruning is handled by GC.
_ = keepDays
_ = repoID
c.JSON(http.StatusOK, gin.H{"success": true})
```

The comment says "handled by GC" but that's aspirational — the GC scanner (Phase 6) only runs on libraries with `auto_delete_days` configured, not on user-triggered trash clean requests. A user explicitly clicking "clean trash" gets a silent no-op.

What the endpoint should do:
1. Collect all commits that are not the HEAD and are older than `keep_days`
2. Enqueue their fs_objects and blocks for GC (decrement ref counts)
3. Delete the commit rows from `commits` table

**Tracked as**: `ISSUE-TRASH-CLEAN-01` in `docs/KNOWN_ISSUES.md`

---

### Gap C: GC Phase 6 does not clean shares/links after `auto_delete_days` — ✅ RESOLVED (2026-03-17)

~~`scanAutoDeleteExpiredObjects` (Phase 6) didn't clean shares/links for expired versions.~~

**Resolved**: Scanner Phase 7 (`scanExpiredShares`) now cleans expired user-to-user shares independently. Share links with `expires_at` are auto-cleaned by scanner Phase 2. When a library is fully deleted, `enqueueLibraryArtifacts` cleans all remaining artifacts.

---

### What Gets Cleaned Today (for reference)

`PermanentDeleteRepo` and `AdminCleanTrashLibraries`:
- ✅ `libraries` + `libraries_by_id` rows (sync)
- ✅ Tag metadata (`file_tags`, `repo_tag_counters`, etc.) — async
- ✅ Commits, fs_objects, blocks — via GC queue (async, after grace period)

GC Phase 6:
- ✅ Old fs_objects outside the `auto_delete_days` window — enqueued for GC

---

### Implementation Status

| Step | Description | Status |
|------|-------------|--------|
| 1 | Lookup tables (`share_links_by_library`) | ✅ Done (2026-03-13) |
| 2 | Dual-write in share link creation/deletion | ✅ Done (2026-03-13) |
| 3 | Cleanup on permanent delete (Gap A) | ✅ Done (2026-03-17) — `enqueueLibraryArtifacts` in GC worker |
| 4 | Implement `CleanRepoTrash` (Gap B) | ❌ Pending — `trash.go` still a stub |
| 5 | GC scanner orphan phases | ✅ Done (2026-03-17) — Phases 7+8 (shares, restore jobs) |
| 6 | Tests | ✅ Done (2026-03-17) — scanner + worker tests updated |

### Remaining: Gap B (`CleanRepoTrash`)

**Files to touch:**
- `internal/api/v2/trash.go` — implement `CleanRepoTrash`
- `internal/gc/store.go` / `store_cassandra.go` — may need `ListCommitsWithTimestamps` per library
- `internal/gc/scanner.go` — new orphan detection phase

---

## Monitoring Technical Debt

### Commands
```bash
# Count remaining ModalPortal wrappers in parent components
grep -rl "ModalPortal" frontend/src/ | wc -l

# Check test coverage
go test ./... -coverprofile=coverage.out && go tool cover -func=coverage.out | grep total

# Find hardcoded URLs
grep -r "localhost:8080" frontend/src/
```

### Metrics to Track
| Metric | Current | Target | How to Check |
|--------|---------|--------|--------------|
| Broken dialogs | 0 ✅ | 0 | All 122 migrated (2026-01-30) |
| Test coverage | 25% | 40% | go test -cover |
| Hardcoded URLs | 0 ✅ | 0 | grep localhost |

---

## 10. Inline HTML in Go Code — ✅ FIXED (2026-03-05)

### Status
All inline HTML has been migrated from Go source files to `html/template` files with base template inheritance and external CSS.

### What Was Done
- Created `internal/templates/html/base.html` — base template with `{{block}}` system
- Created 10 page templates extending the base via `{{define}}` blocks
- Created `internal/templates/html_templates.go` — template manager using `embed.FS`
- Created `frontend/public/static/css/sesamefs-pages.css` — shared CSS for all backend-rendered pages
- Migrated all `fmt.Sprintf` HTML generation from `fileview.go`, `sharelink_view.go`, and `server.go`
- Extracted `buildPreviewContent()` helper to eliminate duplicated preview-building code
- Removed legacy `seahub_token` cleanup from logout template

### Result
- Zero inline HTML pages in Go production code (only one-line fallback strings for template errors)
- Consistent styling via shared CSS file (matches React frontend brand colors)
- Templates auto-escape user input (XSS protection built into `html/template`)
- New pages can be added by creating a `.html` file and a data struct — no CSS duplication needed

---

*Last updated: 2026-03-17*
