# Technical Debt & Migration Plan

This document tracks known technical debt and provides actionable plans for addressing each issue while the system remains in use.

---

## 1. Multi-Host ServiceURL — ✅ FIXED (2026-02-09, simplified 2026-03-30)

### Status
The frontend now uses `window.location.origin` by default for API calls, enabling multi-tenant deployments where different hostnames (us.sesamefs.com, eu.sesamefs.com) route to the same system.

### What Was Done
- `frontend/public/index.html`: `serviceURL` stays same-origin (`''`)
- `frontend/src/utils/seafile-api.js`: Fallback `const server = serviceURL || window.location.origin` handles the empty case
- No hardcoded `localhost` references remain in `frontend/src/`

### Result
- `https://us.sesamefs.com` → API calls go to `https://us.sesamefs.com/api/...`
- `https://eu.sesamefs.com` → API calls go to `https://eu.sesamefs.com/api/...`
- Docker-based local development keeps `/api...` same-origin through the frontend nginx container

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

---

## 20. Block Metadata Hot-Path LWT (DEFERRED)

### Current State
The row-per-reference migration removed Paxos from `block_references`, but block materialization still uses `INSERT ... IF NOT EXISTS` in `UpsertBlockMetadata` for the canonical `blocks` row.

### Why It Was Deferred
This branch keeps the LWT because it still pins one canonical `(storage_class, storage_key)` for a content-addressed block. A blind last-writer-wins rewrite could repoint reads and GC to a backend that does not actually hold the canonical copy.

### Follow-Up Plan
1. Split block materialization into a hot re-upload path and a cold-create path.
2. Use a cheap read/confirmation path for already-known blocks.
3. Keep LWT only for true first-writer creation, or replace it with an equivalent ownership scheme that preserves the canonical physical copy invariant.
4. Re-run the Docker `go-all-test` path plus focused upload/concurrency integration tests before changing this.

### Regression Tests To Keep
- `TestConcurrentV2UploadsNoLostCommits`
- `TestChunkedUploadConflictRollbackCleansStateBeforeFreshReupload`
- `TestSyncRecvFSBeforePutBlockPublishesDownloadableFile`

---

## 21. Cassandra-Real Coverage For GC Claim Retry (DEFERRED)

### Current State
`ClaimBlockDelete` now does an explicit `SELECT gc_state, gc_claim_id` after a failed CAS, instead of trusting the partial row returned by Cassandra for `UPDATE ... IF`.

### Why It Was Deferred
The code path is corrected, but the current tree still lacks a real Cassandra regression that proves a retry of the same logical candidate recognizes its prior claim under Cassandra's actual CAS return semantics.

### Follow-Up Plan
1. Add a Cassandra-backed store test that seeds a claimed block row.
2. Retry `ClaimBlockDelete` with the same `claimID` and assert ownership is recognized.
3. Retry with a different `claimID` and assert it is rejected.
4. Keep the existing S3-orphan recovery tests green alongside the new coverage.

---

## 22. SeafHTTP Publish-Attempt ID Resolution Mismatch (DEFERRED)

### Current State
The direct SeafHTTP finalize paths still call `db.StagePublishAttemptReferences(..., nil)` and queue durable publish-repair rows with external SHA-1 block IDs. Block liveness itself lives on internal SHA-256 IDs.

The current code is not in the highest-risk state anymore because:
- successful provisional-ref writers (direct uploads, SeafHTTP, sync, and OnlyOffice edits) keep the `up:` ref alive until publish finishes, then release it on success while the GC scanner recovers abandoned refs by per-referrer expiry;
- permanent `fs:` refs are still added through `RegisterFSObjectBlockReferences`, which resolves external IDs before writing liveness rows.

### Why It Was Deferred
This mismatch is now a contract / cleanup gap, not the top merge blocker. The main correctness blockers in this branch were the destructive mapping rollback, partial `pub:` stage leak, and abandoned-upload recovery gap. Those are now addressed via forward-mapping preservation, partial-stage rollback cleanup, and per-referrer provisional-expiry tracking plus success-path `up:` ref release.

### Follow-Up Plan
1. Resolve SeafHTTP single-shot and multiblock finalize inputs to internal SHA-256 IDs before calling `StagePublishAttemptReferences`.
2. Queue durable publish-repair rows with those resolved internal IDs, matching the v2/sync contract.
3. Keep the permanent `fs:` registration path unchanged so external block IDs in `fs_objects` remain backward-compatible.
4. Re-run focused SeafHTTP finalize tests plus publish-repair integration coverage.

### Regression Tests To Keep
- `TestCreateOfficeFileLeavesNoPublishAttemptRefs`
- `TestSyncRecvFSBeforePutBlockPublishesDownloadableFile`
- `TestConcurrentV2UploadsNoLostCommits`

---

## 23. Row-Per-Reference Schema / Docs Cleanup

Closed in the current branch: the dead `gc_processed_items` bootstrap table is gone from `001_initial_schema.cql`, the GC store no longer exposes a marker API that nothing calls, and the remaining docs now point at `block_references` / delete-fence GC as the active model instead of the retired counter-marker design.
4. Re-run the focused GC / upload / publish tests after the cleanup branch to catch accidental drift.

---

## 24. Sync fs_object Reference Retention (DEFERRED)

### Current State
`fs:*` block references mean "this persisted fs_object contains the block", not
"this block is present in the current HEAD". Sync finalization therefore preserves
`fs:<library_id>:<fs_id>` rows when a file leaves HEAD; the permanent reference is
removed only when the retained fs_object itself is swept by reachability/retention
GC.

### Why It Was Deferred
This is the safe merge behavior for the row-per-reference branch because old
commits, history downloads, and retention windows can still reference fs_objects
that are no longer in HEAD. Removing `fs:*` from the sync writer path can make a
block look dead while retained metadata still points at it.

### Follow-Up Plan
1. Formalize the fs_object retention horizon and gc_grace contract.
2. Make the fs_object GC the only permanent-reference remover.
3. Add Cassandra-backed coverage for sync delete/replace followed by history
   download across the retention window.
4. Add metrics for retained-unreachable fs_objects and delayed block reclamation.

---

## 25. SeafHTTP Finalize Operational Hardening

### Current State
Correctness blockers for merge have been handled in the writer path:
- SeafHTTP upload refs use a per-attempt/session operation ID instead of a reusable
  access token.
- S3 orphan fences are no longer cleared by writers; recovery/GC owns S3 deletes.
- Large pending-published owner rows keep `block_ids` out of the logged owner +
  discovery batch, avoiding Cassandra "Batch too large" failures during large
  finalization.

### Resolved

**P-1 — Metadata permit serialized the S3 PUT** ✅ Fixed (2026-06-15,
branch `fix/upload-permit-unwrap-s3-put`)

`finalizeUploadBlockMetadataConcurrency = 1` was acquired before
`retrySeafHTTPBlockMaterialization`, accidentally serializing the S3 PUT
alongside the Cassandra LWT. The permit now wraps only
`registerUploadedBlockAndMappingForUploadFn` (the LWT); S3 Exists+PUT and the
fence check run in parallel across all 8 workers. Regression test:
`TestFinalizeUploadBlockMetadataPermitDoesNotBlockS3Put`. See
`docs/UPLOAD-PERFORMANCE-SECURITY-2026-06.md`.

**P-2 — Double S3 round-trip per block (Exists + PUT)** ✅ Fixed (2026-06-15,
branch `perf/p2-cassandra-first-hot-reuse`)

The pre-PUT S3 HEAD is replaced by `DB.ProbeBlockReuse(orgID, blockID)`
(`internal/db/block_references.go`), which reads `blocks` metadata,
`block_references`, and the `gc_s3_orphans` fence and returns
reusable / needs-put / blocked-by-GC / unknown-error. Needs-put blocks use
`PutBlockAutoDirect` (no HEAD); reusable blocks run `EnsureReusableBlockPresent`
(verify the canonical object via HEAD on the declared key, repair via direct PUT
only if missing); blocked-by-GC returns `ErrBlockDeleteInProgress` to retry;
unknown-error falls open to the legacy Exists+PUT. Wired into all five upload paths
(`seafhttp.go` ×2, `sync.go`, `files.go` ×2, `onlyoffice.go`). Per-block cost: a
new block now pays ≤2 Cassandra reads (~1 ms each) + 1 direct PUT and no HEAD;
dedup hits keep one canonical-verify HEAD. Safety is unchanged — it rests on the
reference-first/fence-check protocol in `RegisterUploadedBlock`, not on the PUT
(see KNOWN_ISSUES ISSUE-UPLOAD-S3-DOUBLE-RTT-01). The materialize retry loop now
also retries `ErrBlockDeleteInProgress` from the `store` phase.

**Scope (not global):** this fixes the server-side hot upload paths only. The
`BlockStore` methods `PutBlockAuto` / `PutBlock` / `PutBlockData` still do
`Exists` + `PutAuto` and remain in use as the probe-error fallback and by
unmigrated callers (e.g. `v2/blocks.go` `UploadBlock`). The HEAD was not removed
from `BlockStore` globally.

Tests: `ProbeBlockReuse` decision matrix (`block_references_test.go`),
reusable/needs-put `finalizeUploadStreaming` paths (`seafhttp_test.go`),
`EnsureReusableBlockPresent` exists/repair paths and shared retry helper
(`upload_reuse_test.go`).

**P-2b - Generic S3 multipart uploads still sent parts serially** - Fixed
(2026-06-17, branch `perf/s3-multipart-parallel-parts`)

`internal/storage/s3.go` `PutLarge` now reads the source stream sequentially but
submits `UploadPart` requests through a bounded worker pool
(`MultipartUploadConcurrency = 4`) and preserves deterministic part ordering for
`CompleteMultipartUpload`. This closes the mismatch where the code comment said
"supports parallel uploads" while the implementation still did a simple
read/upload/read/upload loop.

Scope note: this helps callers that reach `S3Store.PutAuto()` above the
100 MB multipart threshold. It does not materially change the default
SeafHTTP web finalize path because that flow still splits uploads into 8 MB
blocks before storing them, so those block PUTs normally stay below the generic
multipart threshold. The main web-upload relay limits therefore remain S-1/S-3
below plus the broader store-and-forward finalize shape documented in
`docs/UPLOAD-S3-RELAY-BOTTLENECK.md`.

Operational note: the abort path now uses a fresh bounded context so cleanup can
still run after the caller context is cancelled, but that does not eliminate the
general object-store hygiene recommendation. Operators should still enable a
bucket lifecycle rule that aborts old incomplete multipart uploads, because
best-effort aborts can still fail under network/provider incidents and S3-style
stores may retain orphaned MPU parts until an explicit abort or lifecycle sweep.

### Remaining Debt

- **Block reads ignore `storage_key`** (pre-existing, surfaced by P-2). Every read
  in `internal/storage/blocks.go` (`GetBlock`, `GetBlockReader`, `GetBlockSize`,
  `BlockExists`, …) derives the S3 key from the content hash via `hashToKey(hash)`
  and never consults the canonical `storage_key` column; GC deletes the same way.
  Only `EnsureReusableBlockPresent` (P-2) honors `storage_key`. Harmless today
  because `storage_key` is either empty (4 of 5 upload paths write `""`) or equal to
  the hash-derived key (OnlyOffice), so the hash-derived key is always correct. The
  risk surfaces only if a future write persists a `storage_key` that differs from
  `hashToKey(hash)` — reads/GC would then target a different object than verify/
  repair. Fix: make `storage_key` the primary locator for reads + deletes, hash-
  derived key as fallback. See KNOWN_ISSUES ISSUE-BLOCK-STORAGE-KEY-READS-01.
- Pending-owner and publish-repair sweepers need explicit per-run limits and
  metrics for skipped, repaired, released, and failed rows.
- The upload-finalize `gc_leases` role should include `orgID` for extra isolation.
- Lease acquisition can poll LWT every 25ms under contention; add backoff/jitter or
  a lower-pressure queueing path if hot libraries contend frequently.
- If a crash happens after the owner+discovery batch but before the separate
  `block_ids` update, `pub:<attempt>` refs can survive until TTL. This is a known
  leak window, not data loss; close it later with chunked block-id metadata or a
  repair source that can reconstruct staged IDs.

**S-1 — Chunk state is node-local** (pending)

Upload tokens in production are Cassandra-backed (`server.go:190–192`,
`NewCassandraTokenAdapter`) and multi-node safe. The actual multi-node blocker
is `chunkManager` (`seafhttp.go:375`): a process-global `map[string]*ChunkUpload`
plus temp files under `os.TempDir()`. If a load balancer routes chunks from
the same upload to different nodes, node B creates an empty tracker and
finalization fails. Immediate mitigation: sticky sessions at the LB keyed on
the upload token (already in Cassandra, no server changes needed). Permanent
fix: distributed chunk state (Redis) or on-the-fly block materialization as
chunks arrive, eliminating node-local staging.

**S-2 — `server.max_upload_mb` not enforced on chunked uploads** - Fixed
(2026-06-18, branch `upload/chunked-hardening-limits`)

Chunked uploads now enforce `server.max_upload_mb` before a new tracker creates
or truncates its temp file. The guard lives in the chunk-manager create path, so
chunked requests above the configured limit fail closed with HTTP 413 instead of
allocating local staging first.

**S-3 — Full /tmp staging with no disk admission limit** - Mitigated
(2026-06-18, branch `upload/chunked-hardening-limits`)

The full temp-file staging model still exists, but the node can now expose an
explicit reservation budget through `seafhttp.chunked_staging_max_bytes`. New
chunked uploads are rejected before tracker creation if their declared size
would push the sum of active staged uploads above that budget. The default
remains `0` (disabled) to preserve existing deployments until operators choose a
node-local value that matches real disk capacity.

---

## 5. Web Upload Pipeline Follow-Ups (PENDING)

### Current State
The current web uploader now has two safe improvements in place:
- UI switches to `Saving...` when the last chunk is waiting on server-side finalization.
- The next queued file can start while the previous file is still finalizing its last chunk.
- `web_uploads.simultaneous_uploads` is now treated as a client-side ceiling:
  each upload session starts at `1`, ramps up only on stable high-throughput
  links, and drops back to `1` on retries or network/server trouble.

The backend upload hot path also now avoids one repeated cost: successful chunked
storage pre-checks are cached on the in-memory upload tracker, so later chunk requests
for the same upload stop re-walking the current HEAD just to reach the same answer.
`finalizeUploadStreaming()` still revalidates against the current HEAD before publish,
so this optimization does not change the canonical HEAD / CAS safety model.

Backend finalization is also partially improved: `finalizeUploadStreaming()` now parallelizes block PUTs and metadata writes instead of doing them fully serially.

Important current limit:
- lowering the adaptive target does not cancel chunks already in flight; it only
  stops refilling additional chunk requests above the new target, which keeps
  the downgrade safe but not literally instantaneous.

### Still Pending

**1. Stream blocks to storage as chunks arrive**

Current chunked uploads still land in a temp file first and only become blocks during finalization in [internal/api/seafhttp.go](internal/api/seafhttp.go). That means large uploads still pay a real finalization phase at the end, even though it is shorter than before.

Pending improvement:
- Convert each arriving 8 MB chunk directly into a stored block.
- Persist per-upload block manifests incrementally.
- Leave the last request to do only the final metadata commit.

Main risks to design for:
- Out-of-order chunk arrival.
- Cleanup of orphaned blocks when commit fails or upload is abandoned.
- Encrypted libraries must keep block encryption and block-ID mapping behavior identical.
- Resume logic must know which blocks are already materialized.

**2. Migrate the web frontend to the block API flow**

The browser still uploads through `seafhttp` + ResumableJS. The repo already has block-oriented APIs in [internal/api/v2/blocks.go](internal/api/v2/blocks.go), but the web client does not use them yet.

Pending improvement:
- Hash blocks client-side.
- Use `POST /api/v2/blocks/check` for dedup/resume.
- Upload missing blocks individually.
- Commit the file from the block manifest with a separate final step.

Why this is deferred:
- It is a protocol-level frontend migration, not a small UX patch.
- It must preserve folder uploads, replace flows, shared links, upload links, retries, and progress UX.
- It needs explicit browser-side hashing/performance validation on large files.

**3. Decide and formalize chunked upload traffic semantics**

The recent upload fix made `HandleUpload` use the declared `Content-Range` total for chunked traffic pre-checks, but traffic is still recorded only after successful `finalizeUploadStreaming()`.

Current consequences:
- clearly over-quota chunked uploads are blocked early against the full declared upload size
- repeated chunk requests no longer repeat the same visible-tree storage pre-check once a tracker has already passed it for the same path/size/replace tuple
- abandoned chunk sessions, janitor-reaped temp files, and finalize failures can still consume real bandwidth without incrementing `traffic_period_usage`
- retried chunks are idempotent at the temp-file layer, but traffic accounting is not yet defined per chunk because there is no per-chunk recorder path
- invalid or missing `Content-Range` currently falls back to the non-chunked upload path instead of enforcing a strict resumable-upload protocol

Why this is acceptable for now:
- standard paid tiers include very generous upload headroom (50 TB/month), so the commercial pressure on upload-side overages is low
- paid-plan overage billing happens outside SesameFS; SesameFS mainly enforces hard limits and warning thresholds
- the recent fix still closed the more important bypass where a large chunked upload could under-state the pre-check size using request `Content-Length`

Future fix options:
- keep the current model but document it explicitly as `completed logical upload bytes`, not exact wire bytes
- or move to per-chunk traffic recording / reservation with reconciliation on completion, retry, or abandonment
- if per-chunk recording lands, replace the current `declared total on every request` pre-check with chunk-bytes or session-reservation logic to avoid false rejections after partial accounting
- add tests for aborted uploads, finalize failures, duplicate chunk retries, and malformed `Content-Range`

**4. Finish the remaining `ReadAll` limits audit**

`httputil.ReadAllWithLimit` fails closed at 1 GiB (`1 << 30`) before calling
`io.ReadAll` when the declared size is too large, and also rejects bodies that
stream past the limit.

Current production callers:
- `SeafHTTPHandler.HandleUpload` in [internal/api/seafhttp.go](internal/api/seafhttp.go)
- `FileHandler.UploadFile` in [internal/api/v2/files.go](internal/api/v2/files.go)

Test-only callers live in [internal/httputil/read_limit_test.go](internal/httputil/read_limit_test.go).
Chunked web uploads already avoid whole-file reads on the request path.

Still pending outside this branch's upload-finalization scope:
- audit direct sync-protocol request-body reads and decompressed zlib reads in [internal/api/sync.go](internal/api/sync.go) and give each endpoint an explicit protocol-sized cap or streaming parser
- cap diagnostic/body reads from external OIDC HTTP responses in [internal/auth/oidc.go](internal/auth/oidc.go)
- audit storage/buffer/streaming `ReadAll` helpers that are safe only when the caller already controls object size
- keep tests/HTTP response reads as test-only exceptions unless they become production helpers
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

## 6. Programmatic Auth Follow-ups — ✅ Core gap fixed (2026-04-03)

### Current Supported Flow

OIDC-only production deployments are no longer blocked for non-browser clients:

1. Users create API keys through `GET/POST/DELETE /api/v2.1/api-keys/`.
2. Desktop clients, SeaDrive, and CLI tools call `POST /api2/auth-token/` with:
   - `username` = user email
   - `password` = raw API key
- Desktop/mobile sync clients in OIDC-only deployments
- `seaf-cli` and similar headless user tools
- User-scoped automation without relying on dev tokens
### Remaining Debt

| Item | Status | Notes |
|---|---|---|
| OIDC browser flow (`/api/v2.1/auth/oidc/login`) | ✅ Works | Primary browser login |
| User API keys (`/api/v2.1/api-keys/`) | ✅ Works | Current answer for desktop/CLI/automation |

The product now has a working user-scoped auth path, but it still lacks a first-class answer for:

- browser-assisted device login without pre-created API keys

Those are no longer production blockers for user-driven clients, but they are still legitimate backlog items.

---

## 7. Real Multilingual Support and Backend HTML Ownership — ⚠️ Deferred

### Status
Spanish and Chinese support are not blocked by a single missing catalog. The current stack is effectively English-first end to end.

### Why This Is Larger Than a Simple Frontend Translation Pass
- `internal/api/bootstrap.go` hardcodes `langCode: "en"`, `currentLang.langCode: "en"`, and a one-item `langList`.
- `internal/api/bootstrap_test.go` asserts that English-only bootstrap shape, so current tests encode the limitation.
- `internal/templates/html/base.html` still renders `<html lang="en">` and backend-owned HTML pages contain hardcoded English labels.
- Frontend runtime defaults in `frontend/src/bootstrap/runtime-bootstrap.js` also fall back to English-only bootstrap data.
- Locale naming is inconsistent across the stack: `zh-CN`, `zh_CN`, and `zh-cn` all appear in different places.
- Much of the app still depends on `window.gettext(...)` strings. If the gettext catalogs are not loaded for a page, the current fallback is to return the English source string.

### What Real Spanish and Chinese Support Would Require
1. Make bootstrap language-aware instead of hardcoding English.
2. Decide and enforce one locale format across backend, frontend, and vendor editors.
3. Define the supported language list in backend config and expose it through bootstrap.
4. Implement or replace the advertised `languageChange` flow so language selection persists across requests.
5. Audit frontend strings that currently rely on English-source `gettext(...)` fallbacks.
6. Verify vendor editor locale assets and namespace loading for both Seafile editor and SDoc editor.
7. Localize the remaining backend-served HTML pages or migrate those pages into frontend-owned shells.

### Recommended Near-Term Direction
Treat real multilingual support as a tracked migration, not a quick patch. For now, keep English as the supported product language and avoid presenting Spanish/Chinese as partially available until bootstrap, locale normalization, and backend-owned HTML are aligned.

### Backend-Owned HTML Surfaces
The backend still renders a narrow but important set of full HTML pages:
- `onlyoffice_editor.html` for full-page OnlyOffice editor bootstrap
- `error_page.html` as the fallback page for those flows
- `login_success.html` for the desktop-client SSO callback bridge

Detailed route and ownership map: `docs/BACKEND-HTML-SURFACES.md`.

### Migration Assessment
Completed safely now:
- inline authenticated file preview now redirects to a frontend-owned standalone shell
- historic file preview now redirects to the same frontend-owned standalone shell

Good candidates to move next:
- `error_page.html`

The remaining low-risk win is `error_page.html`, because it is still a presentation shell around existing backend flows.

Medium-complexity migration:
- `onlyoffice_editor.html`

This can likely move to a frontend-owned shell, but the replacement must preserve secure OnlyOffice bootstrap/config loading and current auth/error handling.

Keep backend-owned for now:
- `login_success.html`

This page is acting as a browser-to-desktop bridge for `sesamefs://` return URLs. Keeping that bridge in Go is reasonable until there is a dedicated frontend-safe handoff design for native client login completion.

### Deferred Direction
Move preview/editor shells into the frontend and leave the backend responsible only for:
- bootstrap JSON
- raw file and preview streams
- OnlyOffice config APIs
- auth and session redirects
- native-client callback bridges that must complete before the SPA loads

## 8. Fake `UUID@sesamefs.local` Emails — ⚠️ Partially Fixed (2026-02-22)

### Status
### What Was Fixed


### Remaining: Display Fields (Safe to Fix, Low Risk)

These return incorrect data to the client but do not affect stored data. Fix by using a similar `resolveOwnerEmail`-style DB lookup.

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

### High Priority
- [ ] **Device Flow / service-account auth follow-up** (Section 6) — user-scoped API keys are complete, but there is still no first-class userless automation flow.
- [ ] **Persist and enforce API key scope in derived sessions** (Section 14) — `/api2/auth-token/` currently drops API key scope when minting a session.
- [ ] **Define SeafHTTP upload-token contract and resumability for public links** (Section 15) — TTL, retries, cleanup, and resume semantics are still inconsistent.

### Short-Term (As Encountered)
- [ ] Fix dialogs as users report issues
- [ ] Add tests when fixing bugs
- [ ] Fix remaining fake-email display fields (Section 8) — `files.go`, `seafhttp.go`, `starred.go`
- [x] **Extract `cleanupLibraryTags` to batch** (Section 12) — 6 separate DELETEs → 1 LoggedBatch ✅ (2026-03-18)
- [ ] **Extract share link helpers** (Section 12) — `createShareLink`/`deleteShareLink` touch 4 tables
- [ ] **Add test for `LeaveShareRepo`** (Section 12) — bug was undetected due to missing test
- [ ] **Standardize logging to `slog`** (Section 12) — new helpers use `log.Printf`, inconsistent

### Long-Term (Backlog)
- [ ] Migrate all ~100 dialogs (can be automated with script)
- [ ] Increase test coverage to 40%
- [ ] Consider forking seafile-js for any customization needs
- [ ] OIDC JWT signature verification
- [ ] Decide on FS object modifier fix (Section 8) — accept mixed-state history or leave as-is
- [ ] **Extract remaining ~41 inline batches to DRY helpers** (Section 12) — incremental migration
- [ ] **Add concurrency tests for `renameGroup` and `reserveNextFileTagID`** (Section 12)

---

## 9. SeafHTTP Token Auth: 403 → 401 — ✅ FIXED (2026-02-22)

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

## 10. Library Deletion: Cleanup Paths — ✅ RESOLVED (2026-03-18), Gap B pending

### Status
~~Known gaps. Fully documented 2026-02-24. Implementation planned (see below).~~
**Resolved** (2026-03-18): GC worker now cleans **all** library artifacts via `enqueueLibraryArtifacts`:
- shares, share links, tags, API tokens, locked files (2026-03-17)
- starred_files, monitored_repos, restore_jobs, tag counters (repo_tag_counters, file_tag_counters, repo_tag_file_counts) (2026-03-18)

Scanner Phase 7 cleans expired user-to-user shares. **Phase 9** (2026-03-18) cleans orphaned group shares (shares where `shared_to` is a deleted group). Only Gap B (`CleanRepoTrash` stub) remains.

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
| `starred_files` | `user_id, repo_id` | ✅ **Resolved** (2026-03-18) — `DeleteStarredFilesByLibrary` |
| `monitored_repos` | `user_id, repo_id` | ✅ **Resolved** (2026-03-18) — `DeleteMonitoredReposByLibrary` |
| `restore_jobs` | `org_id, library_id` | ✅ **Resolved** (2026-03-18) — `DeleteRestoreJobsByLibrary` |
| `repo_tag_counters` | `repo_id` | ✅ **Resolved** (2026-03-18) — `DeleteRepoTagCounters` |
| `file_tag_counters` | `repo_id` | ✅ **Resolved** (2026-03-18) — `DeleteFileTagCounters` |
| `repo_tag_file_counts` | `repo_id, tag_id` | ✅ **Resolved** (2026-03-18) — `DeleteRepoTagFileCounts` |

Additionally, scanner **Phase 7** catches expired user-to-user shares independently, and **Phase 9** catches orphaned group shares (group deleted but shares remain).

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
| 5 | GC scanner orphan phases | ✅ Done (2026-03-17) — Phases 7+8 (shares, restore jobs); Phase 9 (2026-03-18, group shares) |
| 6 | Tests | ✅ Done (2026-03-17) — scanner + worker tests updated |
| 7 | Starred/monitored/counters cleanup | ✅ Done (2026-03-18) — 6 new tables cleaned in `enqueueLibraryArtifacts` |
| 8 | Group deletion cascade | ✅ Done (2026-03-18) — atomic batches + async shares cleanup |
| 9 | Audit log for deletions | ✅ Done (2026-03-18) — `audit_log` table (365-day TTL) |

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

## 11. Inline HTML in Go Code — ✅ FIXED (2026-03-05)

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

## 12. Atomic Batch Operations & DRY Helpers Refactoring — 🟡 PARTIAL (2026-03-18)

### Status
Commit `305d8b21` introduced atomic `LoggedBatch` operations and DRY helper functions for dual-table writes. Local fixes applied for 4 bugs introduced in the commit. **Core pattern established; remaining inline batches should migrate to helpers incrementally.**

### What Was Done (Commit 305d8b21 + Local Fixes)

**New files:**
- `internal/api/v2/write_helpers.go` — 10 helper functions for atomic dual-table writes
- `internal/api/v2/group_cleanup.go` — 4 helper functions for group lifecycle cleanup

**Patterns established:**
- All dual-table writes use `gocql.LoggedBatch` for atomicity (all-or-nothing)
- Helpers accept `interface{ Session() *gocql.Session }` for testability
- Large member updates use `UnloggedBatch` in chunks of 50
- Counter allocation uses LWT (`IF NOT EXISTS` + CAS retry) to prevent race conditions
- API token creation uses LWT to prevent duplicate `app_name` entries
- In-memory state maps use TTL-based pruning to prevent memory leaks

**Bugs fixed in local changes (all introduced in same commit):**
1. `write_helpers.go` — `failedCount` variable not declared (compilation error)
2. `file_shares.go:879` — incorrect arguments to `deleteLibraryShare` in `LeaveShareRepo`
3. `group_cleanup.go` — individual DELETEs in loop → refactored to `LoggedBatch` per library
4. `libraries_test.go:729` — type switch without variable capture

### Remaining: Inline Batches Not Yet Extracted to Helpers

~41 inline `LoggedBatch` usages remain across handlers. High-priority candidates:

| Candidate | File(s) | Tables | Why |
|-----------|---------|--------|-----|
| ~~`cleanupLibraryTags`~~ | ~~`tags.go:823-846`~~ | ~~6 tables~~ | ✅ **Fixed** — 6 DELETEs → 1 LoggedBatch |
| `createShareLink` / `deleteShareLink` | `share_links.go` | 4 tables | Most complex dual-write, easy to get wrong |
| `createGroup` / `deleteGroup` | `groups.go` | 3 tables | Frequent operation, 5 inline batches |
| `createDepartment` / `deleteDepartment` | `departments.go` | 2 tables | Same pattern as groups |
| Library transfer (admin) | `admin.go`, `admin_extra.go`, `org_admin.go` | 2 tables | `updateLibraryOwner` helper exists but may not be used everywhere |

### Remaining: Test Coverage Gaps

| Gap | Risk |
|-----|------|
| `LeaveShareRepo` — no test (bug was undetected) | High — the args-swap bug had no test |
| `renameGroup` — no concurrency test | Medium — Phase 2 (UnloggedBatch) can lose updates |
| `reserveNextFileTagID` — no unit test for CAS retry | Medium — complex retry logic untested |
| `cleanupGroupShares` — no test for batch cleanup | Low — tested indirectly via integration |

### Remaining: Consistency Issues

| Issue | Severity | Notes |
|-------|----------|-------|
| Mixed `log.Printf` / `slog` logging | Low | New helpers use `log.Printf`, rest of codebase moving to `slog` |
| `renameGroup` Phase 1/Phase 2 non-atomic | Low | Phase 1 (groups+groups_by_id) succeeds but Phase 2 (groups_by_member) can partially fail. Inherent to Cassandra denormalization — document as known limitation |
| `file_count` counter increment (tags.go) | Low | Simple Cassandra counter, can lose increments under network partitions. UI-only impact |

### Read-Model And Sync Hardening (2026-04-02)

Recent refactors moved the backend toward a stable initial Cassandra schema with canonical tables plus denormalized read models. The direction is sound, but a few important caveats remain and should stay visible:

| Issue | Severity | Notes |
|-------|----------|-------|
| Sync `HEAD` derived-state update is split-phase | High | `libraries` advances via CAS first, then `libraries_by_id` plus admin projection are resynced immediately afterward. This is an intentional Cassandra limitation, not a fully atomic multi-table transaction. |
| Async library stats are still eventual | Medium | `size_bytes` / `file_count` are recalculated asynchronously after sync `HEAD` changes, so brief staleness is expected. The stale-overwrite bug was closed by conditioning stat persistence on the current `head_commit_id`, but recomputation still remains asynchronous by design. |
| Conditional stats write still resyncs projection in a second step | Medium | After the guarded stats update succeeds on `libraries`, the admin library projection is refreshed in a separate step. A short immediate retry now reduces transient failures, but if all attempts fail the canonical stats remain correct while the admin projection can still lag until another repair or write occurs. |
| Admin library/group lists still paginate in memory | Medium | Read models removed earlier lookup pain, but list handlers still materialize rows and sort/page in Go. Acceptable for initial scale; revisit before large-cardinality admin usage. |
| Best-effort counters require recount fallback | Medium | `admin_link_counts_by_org` is a cached accelerator, not source of truth. Recount/invalidations are part of the design and must not be removed by future cleanup. |
| Docs must stay aligned with code, not old roadmap text | Medium | `CHANGELOG`, `CASSANDRA-OPTIMIZATION-GUIDE`, and `V1-PRODUCTION-ROADMAP` drifted once the read-model work landed. Future refactors should update docs in the same change. |

**Current mitigation and remaining solutions for the conditional stats/projection gap:**
- Current mitigation: a small immediate retry loop now wraps the projection refresh after a successful conditional stats update, reducing transient Cassandra/network failures.
- Stronger follow-up: persist a repair marker or enqueue a background resync job so projection repair survives process restarts and can always rebuild from canonical state.
- Operational contract: canonical rows remain authoritative, and admin projections may temporarily lag if the second-step refresh still fails after retries.

**Required engineering pattern going forward:**
- Canonical rows remain authoritative.
- New read models should use immutable primary keys when possible.
- Use `LoggedBatch` dual-write by default.
- If a conditional write is needed and Cassandra cannot span tables, CAS the canonical row first, then resync derived rows immediately from canonical state.
- For refactors touching projections/counters/sync state, write integration regressions first and keep them in `internal/integration/`.

---

## 13. Storage & Traffic Quotas — Scalability Debt (2026-03-25)

### 12a. `COUNT(*)` for Max Users Check

**File:** `internal/traffic/checker.go` — `CheckMaxUsers`

```go
SELECT COUNT(*) FROM users WHERE org_id = ?
```

Full partition scan on every user-add. Acceptable for v1 because:
- Enterprise orgs often have `max_users <= 0` → check skipped entirely
- Free orgs are small (1 user) → scan is trivial
- Paid orgs with explicit limit are rare and small

**Future fix (v2):** Add a `user_count` counter to `storage_counters` (scope `org:<id>:users`), increment on user create, decrement on delete. Single counter read instead of partition scan.

### 12b. Hot Partition for Platform Traffic Aggregate

**File:** `internal/traffic/recorder.go` — `recordCounters`

All traffic across all orgs updates a single partition: `org_id = 00000000-0000-0000-0000-000000000000`. Under high global traffic, this creates a hotspot on a single ScyllaDB node.

**Impact:** Only affects sysadmin traffic charts. No user-facing impact until very high volume.

**Future fix (v2):** Shard the platform aggregate key by `shard_id` (1–10, random). Query all shards and sum for the dashboard. Or compute platform totals asynchronously from per-org data.

### 12c. Storage Counter Functions Location

**Status:** Resolved.

`IncrementStorageCounters`, `DecrementStorageCounters`, and `ReadStorageUsed` now live in `internal/traffic/storage.go` alongside the shared traffic helpers.

The GC path now reuses the shared implementation via `traffic.AdjustAggregateStorageCounters(...)`, so the old duplication in `internal/gc/store_cassandra.go` has been removed.

### 12d. Storage Quota Publish/Counter Atomicity

**Status:** Open technical debt.

Upload and sync publish paths now do the right quota math for the visible or committed delta, and sync HEAD publication now waits for the storage-counter adjustment before returning. That closes the earlier fire-and-forget drift on sync commit publish.

What is still not atomic:
- quota enforcement is still `pre-check -> publish commit/head -> adjust storage_counters`
- concurrent publishes can both pass the same pre-check against stale usage and only exceed the cap once both are visible
- if the post-publish counter adjustment fails, the request can return an error after the new visible state already exists
- post-publish counter failure policy is still mixed across handlers: some paths return 500 immediately, while others log and continue after the publish succeeds
- cross-repo move still publishes the destination before removing the source; if the source half fails afterward, the operation can temporarily behave like a copy even though quota was pre-checked as a net move

Update 2026-05-21: sync HEAD publish no longer tries to reconcile aggregate storage counters inline on idempotent retries. The request path now keeps post-CAS repair O(1): it repairs the current library-scope counter and derived projection synchronously, queues platform/org/user reconciliation requests into `gc_storage_counter_reconciliation`, and returns a retryable error instead of scanning all libraries from the sync handler. Aggregate repair is deferred to GC scanner phase 11, which now recomputes requested scopes from canonical `libraries.size_bytes` / `file_count` rather than from possibly drifted library counter rows.

This is a Cassandra consistency/transaction-shaping debt, not a remaining missing-hook bug like ISSUE-QUOTA-COVERAGE-01.

**Future fix (v2):** Centralize tree-delta publish through one quota-aware primitive. Options include a reservation step before publish, a narrower CAS-backed workflow that couples quota usage and HEAD publication closely enough that concurrent writers cannot both spend the same remaining bytes, or a reconciliation/compensating-job path that can finish the source-removal half of a cross-repo move after the destination publish succeeds. If product keeps success-after-publish for some handlers, add durable repair/reconciliation for missed counter updates instead of relying on log-only drift.

### 12e. Upload Storage Pre-check Cost

**Status:** Open technical debt. Non-blocking. Performance/efficiency only — correctness is already right.

The visible-delta storage pre-check landed for upload paths in 2026-05-14 and closed the previous over/under-counting bug (replace no longer sobre-cuenta `+fileSize`). The pre-check is correct but its current shape pays repeated cost on a few hot paths.

#### Hot spots

**1. Chunked uploads still pay the full visible-delta traversal on first check and finalize**
([internal/api/seafhttp.go:1069](internal/api/seafhttp.go#L1069))

`HandleUpload` now caches a successful chunked storage pre-check on the in-memory upload tracker (`HasQuotaPrecheck` / `MarkQuotaPrecheck`), so later chunk requests with the same `(parentDir, totalSize, replace)` tuple no longer re-run `checkUploadStorageQuotaForCurrentHead` on every request. Finalization still calls `checkUploadStorageQuotaForCurrentHead` again inside `finalizeUploadStreaming` ([internal/api/seafhttp.go:1403](internal/api/seafhttp.go#L1403)) so publish is revalidated against the current HEAD.

That means the hot path cost is no longer O(N chunks); the remaining cost is the full visible-delta traversal on the first accepted chunk plus the authoritative re-check at finalize. This is still worth optimizing for large/deep directory trees, but it is no longer the per-chunk storm described in earlier audits.

Possible mitigations (in order of preference):
- Cache the resolved `(headCommitID, rootFSID, parentResult)` or a higher-level delta context for the lifetime of one chunked-upload session and only re-resolve when finalize detects a changed HEAD.
- Reuse the same traversal helper family as `v2/files.go` so path-resolution and future caching improvements are shared across upload surfaces.
- If we later need tighter client/server resumable reconciliation, expand the tracker cache to retain the resolved target-state hash rather than only the boolean fact that a pre-check succeeded.

**2. `directoryEntriesAtPath` does one Cassandra query per path segment**
([internal/api/seafhttp.go:1327](internal/api/seafhttp.go#L1327))

The traversal does `SELECT dir_entries FROM fs_objects` once per directory level. A target path of `/a/b/c/d/file.txt` is 5 queries (root + 4 nested dirs). Even with the per-tracker pre-check cache, the first chunk and finalize still re-walk the same path, so deep/nested uploads keep paying that traversal cost twice.

Possible mitigations:
- Reuse `FSHelper.TraverseToPath` (which `v2/files.go:UploadFile` already uses) instead of carrying a separate traversal implementation in `seafhttp.go`. That centralizes path-traversal performance work for everyone.
- Add a path-resolution cache scoped to one upload session.

**3. Repeated `traffic.GetChecker()` calls inside one handler**
([internal/api/seafhttp.go:991](internal/api/seafhttp.go#L991) + [seafhttp.go:1282](internal/api/seafhttp.go#L1282) inside the helper, [internal/api/sync.go:874](internal/api/sync.go#L874) + [sync.go:927](internal/api/sync.go#L927))

`GetChecker()` returns the process-wide singleton, so this is cheap. It is mostly a cosmetic concern: handlers that already resolved a non-nil checker for the traffic pre-check call `GetChecker()` again for the post-dedup or visible-delta storage pre-check. Trivial inline cleanup; bundle with the work above when it happens.

#### Why this is not blocking

The work above is purely about cost. The pre-check answers and the counter adjustments are already correct, the regressions are already covered by tests, and the failure modes degrade to "extra Cassandra reads on the upload hot path," not to incorrect quota decisions. The TOCTOU concern itself stays in §12d above; this section is only about how often we pay the cost of *being* correct.

### 12f. Chunked Upload Traffic Semantics and Abort Accounting

**Status:** Open technical debt. Product contract documented, not yet unified with raw-bandwidth accounting.

The recent upload change made `HandleUpload` use the declared `Content-Range` total for chunked traffic pre-checks, but the recorder still runs only after successful `finalizeUploadStreaming()`. Today the web chunked path behaves like a coarse whole-upload reservation for blocking and a completed-upload model for recording.

What that means today:
- uploads that exceed the declared total quota are still blocked early
- uploads abandoned mid-session, janitor-reaped temp files, and finalize failures can consume network bytes without incrementing `traffic_period_usage`
- retried chunks are idempotent at the temp-file layer, but traffic accounting is not yet idempotent per chunk because there is no per-chunk recording path
- invalid or missing `Content-Range` falls back to the non-chunked upload path instead of enforcing a strict resumable-upload contract

Why this is currently acceptable:
- standard paid tiers include 50 TB/month of upload traffic, so the commercial pressure on upload-side overages is low
- paid-plan overage billing lives outside SesameFS; SesameFS mainly enforces hard caps and warning thresholds
- the current fix still closes the more important bypass where a large chunked upload could under-state the pre-check size via per-request `Content-Length`

Future fix options:
- keep current semantics but document them as `completed logical upload bytes`, not exact wire bytes
- or move to per-chunk recording / session reservation with reconciliation on completion, retry, or abandonment
- if per-chunk recording lands, replace the current `declared total on every request` pre-check with chunk-bytes or reservation-token logic to avoid false rejections after partial accounting
- add tests for aborted uploads, finalize failures, duplicate chunk retries, and malformed `Content-Range`

---

## 14. API Key Scope Hardening Follow-up (2026-04-04)

### Status

Core hardening landed. Direct API key auth and sessions minted from API keys now preserve scope metadata, cap effective role by scope, and enforce scope in the central library/sync permission paths.

### What Works Today

- API keys support three scopes: `read`, `read-write`, `admin`
- Direct API key authentication stores `api_key_scope` in request context
- Sessions minted from API keys via `/api2/auth-token/` now also persist `api_key_scope`
- Effective org role is capped by scope:
  - `read` -> at most `readonly`
  - `read-write` -> at most `user`
  - `admin` -> actual org role
- Central library permission checks and sync permission checks now enforce API key scope for both direct API key auth and derived sessions
- Routes that explicitly use `RequireScope(...)` still enforce admin-only or CRUD-specific scope correctly
- Sessions minted from API keys retain:
  - source provenance (`source_api_key_hash`)
  - expiry inheritance from the API key
  - revoke fan-out through `sessions_by_api_key`

### Remaining Debt

- direct API key auth inside `authMiddleware` still has no dedicated application-level rate limit beyond edge protections
- scope enforcement is still partly route-fragile for endpoints that rely only on bare authentication and do not pass through the central library/sync checks or an explicit `RequireScope(...)`
- `admin` scope keys remain intentionally powerful and should be issued only to trusted tooling
- some legacy handlers still compare raw `superadmin` role strings; any cross-org authority checks should be normalized to `IsPlatformSuperAdmin(...)` rather than relying on role text alone

### Impact

- the current model is substantially safer than the initial implementation, but it still benefits from a route-by-route audit to prove that every sensitive endpoint is covered by role checks, library permission checks, or explicit scope middleware
- brute-force protection for direct API key auth is still mostly delegated to edge rate limiting and token entropy rather than an application-specific limiter

### Current Code Shape

- `internal/apikeys/scope_middleware.go` enforces scope when `api_key_scope` exists in request context
- `internal/api/server.go` now sets constrained role + `api_key_scope` for direct API key auth
- `internal/auth/session.go` now stores `source_api_key_hash` and `api_key_scope` on derived sessions
- `internal/middleware/permissions.go` enforces API key scope in central library permission paths
- `internal/api/sync.go` enforces API key scope in sync read/write permission checks

### Recommended Fix Path

1. Add dedicated application-layer rate limiting for direct API key auth attempts, not only for `/api2/auth-token/`.
2. Build an explicit route audit/test matrix for `read`, `read-write`, and `admin` API keys across the most sensitive endpoint families.
3. Keep admin-scope key issuance narrow and document that these keys are equivalent to high-trust automation credentials.

### Non-Issues After Hardening

- derived sessions are now invalidated on API key revocation through `sessions_by_api_key`
- deactivate/delete flows now revoke user API keys in addition to user sessions

---

## 15. SeafHTTP Upload Tokens: TTL, Resume Semantics, And Public-Link Fragility (2026-04-04)

### Status

Known behavior gap. The transport path is mostly unified, but token lifecycle and resumability semantics are not clearly defined across authenticated uploads, share-link uploads, and public upload links.

### Important Clarification

Normal authenticated uploads and upload-link uploads both end up using the same SeafHTTP endpoint:

- upload URL shape: `/seafhttp/upload-api/:token`
- request validation: `internal/api/seafhttp.go`
- token storage: `access_tokens` with Cassandra TTL

So raw upload throughput is not inherently different just because one flow uses an upload token. Normal uploads also use upload tokens.

### Why Public / Upload-Link Flows Feel Slower

The main difference is resumability and recovery, not the underlying upload pipe.

Authenticated uploader flow:
- calls `getFileServerUploadLink(...)` to mint the SeafHTTP upload URL
- can also call `getFileUploadedBytes(...)`
- resumes from the already-uploaded offset when retrying a file

Public/upload-link uploader flow:
- calls `sharedUploadLinkGetFileUploadUrl(...)` or `sharedLinkGetFileUploadUrl(...)` to mint the SeafHTTP upload URL
- cannot call `getFileUploadedBytes(...)` because there is no authenticated user session/token for that API
- explicitly falls back to `markChunksCompleted(0)` and restarts from byte 0 on retry

That means the public/upload-link path is usually perceived as slower or more fragile after interruptions, even though the actual chunk upload endpoint is the same.

### Current Token Semantics

- upload/download tokens are stored in `access_tokens`
- expiration is TTL-based, default `1h`
- token validity is checked at request start
- a single long-running request that starts before expiry can usually finish
- the next chunk/request after expiry fails

### Remaining Ambiguities / Risks

| Issue | Risk | Notes |
|-------|------|-------|
| TTL-only token model | Medium | There is no explicit persisted `expires_at`; expiration is implicit in Cassandra TTL. |
| Public-link uploads cannot resume from server-known offset | High | Retries re-upload from zero, which is expensive for large files or flaky networks. |
| One-shot vs reusable token contract is unclear | Medium | Current flow looks TTL-based, not strictly single-use per upload attempt. |
| Abandoned chunk-upload cleanup is unclear | Medium | `ChunkManager` cleanup is obvious on successful completion, but there is no documented sweeper contract for interrupted uploads. |
| Expiry semantics across multi-request uploads are under-documented | Medium | A file may partially upload successfully, then fail on the next chunk after token expiry. |

### Recommended Fix Path

1. Define the product contract explicitly:
  - whether upload tokens are single-use or reusable until TTL expiry
  - whether expiry is checked only at request start or also during long uploads
  - what the supported resume behavior is for public-link uploads
2. Add an authenticated-equivalent offset probe for public/share upload flows, or another signed mechanism that allows safe resume without a full user session.
3. Add cleanup/expiration handling for abandoned chunk-upload state and document the retention window.
4. Add integration tests for:
  - token expiry between chunks
  - interrupted public upload resume
  - abandoned partial upload cleanup

### Operational Note

If users report that “normal uploads are faster than upload-token uploads”, the likely explanation is not the tokenized SeafHTTP path itself. The more likely cause is that authenticated uploads can resume from server-known progress while public/upload-link uploads restart from zero after interruption.

---

## 16. Admin Organization/User Projections And Recipient Share Reads — 🟡 Live, Performance-Relevant, Still Maturing (2026-04-06)

### Status

The optimization tables are no longer schema-only, and they are no longer just write-side scaffolding.

What is now live in code:

- `PermissionMiddleware.GetUserLibraries()` now resolves direct-user and group shares through `shares_by_recipient`, removing the old `ALLOW FILTERING` recipient scans from that path.
- Sys-admin organization list/search now reads from the organization admin projection tables.
- Sys-admin user list/search and sys-admin listing now read from the user admin projection tables.
- User/org projection rows are now synchronized from the main create/update/status-change paths, OIDC auto-provisioning, OIDC email-adoption/relogin reconciliation, `last_login_at` touches, and GC hard deletes.

That means the read-model work is already improving API behavior in production-shaped paths:

- Sys-admin global user/org APIs avoid the older canonical fan-out + recompute path and serve directly from denormalized projection rows.
- Recipient-centric library resolution now uses the recipient-oriented table that matches the access pattern instead of filtering canonical share rows.
- The remaining debt is operational maturity and repairability, not “should we use these tables at all?”.

Activated admin projection tables:

- `organization_admin_buckets`
- `organization_admin_buckets_by_status`
- `organizations_admin_by_created`
- `organizations_admin_by_status_created`
- `organization_admin_projection_state`
- `user_admin_global_buckets`
- `user_admin_buckets_by_status`
- `users_admin_global_by_created`
- `users_admin_global_by_status_created`
- `user_admin_projection_state`

### Evidence From Code

- `internal/db/admin_identity_read_models.go` now contains the read-model sync/list/delete helpers for organizations and users.
- `internal/api/v2/admin.go` and `internal/api/v2/admin_extra_organizations.go` now read sys-admin organization views from projection tables.
- `internal/api/v2/admin_users.go` now reads superadmin global user views from projection tables.
- `internal/middleware/permissions.go` now uses `shares_by_recipient` for recipient-centric accessible-library enumeration.
- `internal/auth/oidc.go` now batches canonical OIDC provisioning/reconciliation writes together with the affected admin user/org projection rows instead of trailing `SyncAdmin*ReadModel` calls.
- `internal/integration/admin_identity_projection_regression_test.go` and `internal/integration/oidc_projection_regression_test.go` now cover ownership transfer, hard-delete cascades, OIDC auto-provision, OIDC email adoption, and mapped-user relogin reconciliation.

### Why This Is Debt

- The tables are active now, but they still need more operational maturity than the older read models.
- The sync coverage is strong on runtime mutation paths, but seed/bootstrap style paths are still not treated as first-class projection writers.
- There is still no explicit rebuild/backfill command for these projections if they drift later, but that is not a launch blocker while the database is still effectively greenfield.

### Decision For Now

Keep the projections and use them for sys-admin global views. The current implementation already justifies the tables.

For the current pre-production phase, the priority is:

1. Integrity and atomicity from day 0.
2. Runtime coverage on the real write paths.
3. A reliable base for future org-admin and superadmin queries.

Because there is no meaningful production data to salvage yet, explicit rebuild/backfill tooling is deferred work rather than immediate debt.

Remaining follow-ups:

1. Decide whether seed/bootstrap flows should populate these projections directly or whether pre-prod/test bootstrap can continue to rely on normal runtime writes.
2. After this V1 base is stable, add rebuild/backfill tooling for `organizations_admin_*` and `users_admin_*` from canonical `organizations` and `users` if operations actually need it.
3. After this V1 base is stable, decide whether to add an operator-facing audit/rebuild verification command so drift can be detected before an admin notices stale list/search results.

### Important Non-Issue: Other Read Models Are Already Live

This audit does **not** apply to the following tables, which do have live runtime wiring:

- Group admin projections via `internal/db/admin_group_read_models.go` and `internal/api/v2/admin_groups.go`
- Library admin projections via `internal/db/admin_library_read_models.go` and `internal/api/v2/write_helpers.go`
- Admin link projections via `internal/db/admin_link_read_models.go`, `internal/api/v2/share_links.go`, and GC cleanup paths
- Share recipient projections via `internal/db/share_read_models.go` and `internal/api/v2/file_shares.go`

### Clarification: `shares_by_recipient` Is Now Used Where It Fits Best

- Recipient-centric accessible-library enumeration now uses `shares_by_recipient`.
- Repo-centric permission checks still query canonical `shares` by `library_id`, which is already the natural primary-key path for those checks and does not rely on `ALLOW FILTERING`.

That means the remaining split is intentional by access pattern, not a leftover `ALLOW FILTERING` gap in `GetUserLibraries` anymore.

What still remains as separate debt is the org-scoped owned-library scan inside
`GetUserLibraries`: it no longer uses `ALLOW FILTERING`, but it still walks the
canonical `libraries` partition by `org_id` and filters by `owner_id` in Go.
That is now tracked as a tombstone/partition-scan concern in
`docs/SCHEMA-BOTTLENECK-AUDIT.md`, not as an `ALLOW FILTERING` regression.

---

## 17. Org Storage Policy: Migration And Operational Follow-ups — 🟡 Sys-admin and org-admin UI landed (2026-04-09)

### Status

The backend base for org-level storage policy is now live for **new library creation**.

What is implemented:

- `organizations.storage_config` stores `data_residency` (`strict` / `flexible`) plus `default_region` semantics: fallback in `flexible`, required pinned region in `strict`
- sys-admin org detail/update endpoints expose and persist `storage_policy`
- sys-admin and org-admin info pages can view and edit `storage_policy`
- create-time storage resolution honors org policy for:
  - personal library creation
  - group-owned library creation
  - org-admin group-owned library creation
  - superadmin create-on-behalf-of-user
- focused integration tests validate both `strict` and `flexible` modes across those flows
- create-time placement is intentionally restricted to hot classes only

### What Is Still Deferred

1. Migration or background job support for moving existing libraries between regions
2. Policy effects beyond create-time, such as retroactive enforcement or relocation
3. Product/UX decisions for cold-tier primary placement during create flows
4. Optional org projection denormalization of `storage_config` if list/search views ever need it

### Why This Is Still Debt

The current slice is enough to enforce residency for new libraries and to manage policy from the admin UI, but it is not yet the full lifecycle:

- existing libraries remain where they are until an explicit migration design exists
- policy changes are not retroactive
- cold-tier create semantics are still intentionally deferred

### Decision For Now

Keep this as backend-first infrastructure. Do not expand it implicitly into migration or frontend behavior until those pieces are designed as separate work.

When resuming this line of work, the recommended order is:

1. Design migration semantics for existing libraries
2. Decide whether policy should stay create-time-only or become enforceable for post-create operations
3. Decide whether org policy should appear in additional admin list/search surfaces beyond org info

---

## 18. Library History Retention And Auto-Delete Semantics (2026-05-15)

### Status

Open technical debt. The visible controls are wired and persisted, but they currently describe stronger behavior than the backend actually performs.

### What Works Today

- `GET/PUT /api2/repos/:repo_id/history-limit/` persists `keep_days` in `libraries.version_ttl_days`.
- `GET/PUT /api/v2.1/repos/:repo_id/auto-delete/` persists `auto_delete_days` in `libraries.auto_delete_days`.
- Both positive settings are projected into `gc_libraries_by_policy`.
- GC scanner phases are wired:
  - Phase 5 `expired_versions` reads libraries with `version_ttl_days`.
  - Phase 6 `auto_delete` reads libraries with `auto_delete_days`.
- The frontend exposes the owner dialogs and the sysadmin history dialog.
- Focused verification passed on 2026-05-15: `go test ./internal/gc ./internal/api/v2 -count=1`.

### Why This Is Debt

The implementation is internally consistent for the current tests, but the product contract is not aligned:

- **History Setting: "Don't keep history"**
  - PUT accepts `keep_days=0`.
  - GET maps database value `0` to `-1`, so the dialog reopens as "Keep full history".
  - That makes the no-history setting effectively non-round-trippable through the UI.

- **History Setting: "Only keep N days"**
  - GC Phase 5 preserves the full HEAD parent chain.
  - `GetFileHistoryV21`, `GetFileRevisions`, and `GetRepoHistory` list commits without applying `version_ttl_days`.
  - Result: normal linear history can remain visible/restorable beyond the configured window.

- **Auto deletion: "delete files not modified within N days"**
  - GC Phase 6 preserves the full HEAD tree.
  - It enqueues fs_objects only when they are not referenced by HEAD or recent commit trees.
  - Result: current files that are old by `mtime` are not automatically deleted, despite the UI wording.

- **Expiry countdown mismatch**
  - Directory listings expose `expires_at = entry.MTime + auto_delete_days`.
  - GC auto-delete decisions are based on commit age and reachability, not directly on file `mtime`.
  - Result: UI can show a file expiry timestamp that does not correspond to actual deletion behavior.

- **Bootstrap wrapper alignment**
  - Resolved in the current tree: `scripts/bootstrap.sh` and `scripts/bootstrap-multiregion.sh` now delegate to compose `cassandra-bootstrap` plus app-run migrations instead of carrying ad hoc schema DDL.
  - Keep these wrappers as convenience entry points only; `internal/db/migrations/001_initial_schema.cql` remains the clean-boot schema source of truth.

### Decision Needed

Before changing code, decide what each control promises:

1. Visible/restorable history window only.
2. Physical storage reclamation of old history.
3. Automatic deletion of current files that have not been modified recently.
4. Old-history-object cleanup only, with UI text adjusted to match.

These are different products. Treating them as one setting is how the current ambiguity happened.

### Recommended Fix Path

1. Fix `GetHistoryLimit` so `0` round-trips as `0`, not `-1`.
2. Add API tests for `keep_days=0` and positive `keep_days` round trips.
3. If product wants visible retention, filter history APIs by `version_ttl_days`.
4. If product wants physical pruning, design safe commit-chain pruning/compaction before deleting linear history commits.
5. If product wants stale current-file auto-deletion, implement a dedicated job that:
   - scans HEAD-visible files by `mtime`
   - publishes a normal delete commit instead of deleting fs_objects behind HEAD
   - handles locks, encryption, concurrent HEAD changes, permissions, audit logs, and quota/storage counter updates
6. If product wants only old-history-object cleanup, rename the UI away from "Automatically delete files..." and remove or relabel `expires_at`.
7. Keep bootstrap wrappers free of schema-like DDL snippets so they cannot drift from migrations.

### Tests To Add

- `keep_days=0` remains `0` after GET.
- `keep_days=N` hides or rejects history older than N days if visible retention is selected.
- A current HEAD-visible file with old `mtime` is either deleted by a stale-file job or explicitly not shown as expiring.
- `expires_at` is not emitted as a hard countdown unless deletion behavior actually honors it.
- GC scanner tests continue to prove it never deletes objects still required by HEAD.

### Linked Issue

Tracked in `docs/KNOWN_ISSUES.md` as ISSUE-LIB-RETENTION-01.

---

## 19. Multiregion HEAD Safety Follow-ups (2026-05-18)

### Context

Branch `feat/multiregion-head-safety` landed the canonical HEAD CAS pattern, server-side conflict retries for uploads and v2 mutations, atomic same-repo move in a single commit, and rollback of materialized blocks on publish failure. A walkthrough of the resulting code surfaced the following debt items. None of them block the merge; they document liabilities that future iterations should address.

### 19.a. Four Separate Retry-Loop Implementations

The repo now has four implementations of the same "retry on `ErrLibraryHeadConflict` / `ErrHeadConflict`" pattern. They share the same backoff schedule, but the orchestration is still duplicated across sync, upload, and v2 mutation paths:

| Caller | File | Backoff | Jitter | Max delay |
|---|---|---|---|---|
| v2 mutations (rename/move/delete/etc.) | `internal/api/v2/fs_helpers.go` (`retryLibraryHeadMutation`) | exponential (50→100→200→400) | ~25ms | 400ms |
| Chunked + single-shot upload | `internal/api/seafhttp.go` (`commitUploadedFileMultiBlock`, `commitUploadedFile`) | exponential (50→100→200→400) | ~25ms | 400ms |
| Web `UploadFile` | `internal/api/v2/files.go` (`finalizeStoredUploadMetadata`) | exponential (50→100→200→400) | ~25ms | 400ms |
| Sync HEAD publish (`PutCommit HEAD`, `UpdateBranch`) | `internal/api/sync.go` | exponential (50→100→200→400) | ~25ms | 400ms |

Update 2026-05-19: both upload seams now also fail closed if `IncrementOrCreateBlock` cannot confirm the `blocks` row, so uploads no longer continue to library metadata publish after a block-metadata registration failure.

The remaining debt here is not the old blind-overwrite bug. `PUT /commit/HEAD` and `POST /update-branch` now use parent-chain validation, CAS, bounded retry, and server-side auto-merge for non-overlapping stale commits, with regression and multi-node convergence coverage on both routes. The real desktop-client active-active proof now lives in `scripts/test-sync-active-active.sh`, so the remaining debt is duplicated retry orchestration rather than missing proof or false-success fallback semantics.

For the retrying paths, the debt remains maintainability rather than correctness: the upload-specific `*_Once` extraction is already in place, and migrating both upload loops to `retryLibraryHeadMutation` would still remove duplicated retry bookkeeping and keep future contention tuning in one place.

### 19.b. `CleanupFileTagsByPrefix` Performance

`internal/api/v2/tags.go:586-625` scans the full `file_tags` partition for the repo (`SELECT file_path FROM file_tags WHERE repo_id = ?`), filters in-memory by prefix, then fires N+1 individual deletes (one per descendant). For repositories with high tag cardinality, replacing a directory becomes O(total_tags) work, executed asynchronously but still loading Cassandra.

Acceptable today (replace-directory is rare and the goroutine does not block the response), but worth revisiting if tag volume per repo grows or if directory replace becomes more frequent.

Mitigation paths:
- Secondary index by path prefix.
- Move tag storage to a partition that allows range scans by file_path.
- Push the delete loop into a `LoggedBatch` to halve round-trips.

### 19.c. `CollectBlockIDsRecursive` Repeats on Every Retry Attempt

`internal/api/v2/batch_operations.go:547-551` (cross-repo) and `:851` (same-repo) re-collect the replaced entry's block IDs at the top of every retry attempt. If a CAS conflict triggers retry and `replacedEntry.ID` did not change, the recursive read is redundant work.

Hard to avoid safely (the replaced entry can legitimately change between attempts), so the current behavior is the correct conservative choice — but the retry cost is amortized poorly when a directory replace has many descendants.

The same retry-amplification pattern applies to copy block reference accounting: same-repo copy pins copied block refs inside the retry attempt, rolls them back if the HEAD CAS loses, and pins again on the next attempt. This is functionally correct and keeps refcounts balanced, but a contended copy of N blocks can issue multiple rounds of LWT refcount writes before one publish wins.

Retried metadata mutations can also leave unpublished rows behind. Each failed attempt may have created new `fs_objects` and a `commits` row whose commit never becomes a library HEAD. This is not a functional regression, but CAS retries multiply the amount of unreachable metadata compared with a single failed publish. A future maintenance pass should add observability or cleanup for unpublished commit/fs-object rows.

Failed-publish cleanup intentionally does not delete unreachable-looking `fs_objects` today. `fs_id` is content-addressed, so concurrent in-flight publish attempts for the same content can legitimately share the same `fs_object` row before any winning commit becomes visible. A cleanup pass that only scans currently reachable commits can therefore misclassify a shared in-flight `fs_object` as dead and delete content another still-running attempt needs. Any future cleanup for unpublished `fs_objects` must track per-attempt ownership or another explicit in-flight lease; visible-commit reachability alone is not a safe predicate.

The same pattern now applies to sync auto-merge snapshotting: `internal/api/sync.go` reads base/current/target `root_fs_id` in separate queries before attempting the merge. The final HEAD CAS still rejects stale work correctly, but concurrent head movement can waste merge computation and temporary fs-object materialization before the publish loses.

### 19.d. `UpdateLibraryHeadFromSnapshot` Validates an Argument Every Caller Passes Mechanically

`internal/api/v2/fs_helpers.go` requires `expectedHead` as a parameter and validates `expectedHead == snapshot.HeadCommitID`. Current production call sites in `batch_operations.go`, `files.go`, `onlyoffice.go`, and `trash.go` all pass `snapshot.HeadCommitID` literally.

The validation only triggers if someone passes a non-snapshot value — which no current caller does. The unit test (`TestUpdateLibraryHeadFromSnapshotRejectsMismatchedExpectedHead`) covers a path that production never takes.

Either:
- Drop the parameter and rely on the docstring contract.
- Or rename to make the gate explicit (`UpdateLibraryHeadFromSnapshotIfExpected(snapshot, repoID, commitID, expectedHead)`) and force callers to pick a value.

### 19.e. Initial-Commit Paths Bypass CAS Without an Inline Comment

Two paths perform unconditional `UPDATE libraries SET head_commit_id = ...`:
- `internal/api/sync.go` — initial commit during sync repo creation.
- `internal/api/v2/fs_helpers.go` — `InitializeLibraryFS` for v2 library bootstrap.

Both are correct (the library has no concurrent writers at first-touch), but they look identical to the legacy non-CAS behavior the rest of the file has been migrated away from. A future contributor reviewing for "missing CAS" could "fix" these and break the bootstrap throughput.

Add a single-line comment at both sites explaining `bootstrap-only path; no concurrent writers possible`.

### 19.f. Crash Window Between CAS Commit and `syncLibraryHeadDerivedState`

`internal/api/v2/fs_helpers.go` advances `libraries` via CAS in `UpdateLibraryHead`, then in a separate non-conditional batch refreshes `libraries_by_id` plus the admin projection rows via `syncLibraryHeadDerivedState`. A process crash between the two operations leaves canonical `head_commit_id` advanced while derived rows lag.

This is documented as accepted debt in section 12 (Read-Model And Sync Hardening). Functional reads are unaffected — `GetHeadCommitID`, `GetRootFSID`, `OnlyOffice getFileID`, `TrashHandler.CleanRepoTrash`, `SyncHandler.GetHeadCommitsMulti` all resolve via canonical (`libraries`). But admin projections (`libraries_by_org_updated`, `libraries_admin_global_by_updated`) can show stale `size_bytes`/`file_count`/`updated_at` until the next write to that library writes through.

Mitigations described in section 12 (background projection repair job, repair marker) still apply.

### 19.g. No Unit Tests for `processSameRepoMove` and `updateDirectoryAtPathFromRoot`

`internal/api/v2/batch_operations.go:764-903` and `:905-954` contain recursive tree-rebuild logic. Coverage today:
- Pure helpers (`isPathWithin`, `isDirectoryEntry`, `replacedDestinationTagCleanup`, `shouldSkipSourceRemovalAfterMove`) → unit tested in `batch_operations_test.go`.
- End-to-end behavior → integration-tested in `internal/integration/same_repo_move_test.go` (5 scenarios: atomicity, nested tree, cycle prevention, descendant cycle, replace).

Missing: unit tests with mocked `FSHelper` that exercise `updateDirectoryAtPathFromRoot` recursion across depth ≥ 2 without spinning up Cassandra. A regression in the recursive tree rebuild (e.g., forgetting to update a grandparent's pointer) would silently corrupt repo metadata; integration tests catch it only if the test setup happens to use a deep-enough tree.

Path to add: introduce an `fsTreeStore` interface that `FSHelper` satisfies, and unit-test with an in-memory implementation.

### 19.h. Cross-Repo Move Leaks Source Block References Until GC

In `internal/api/v2/batch_operations.go:626`, `pinCopiedTreeBlockRefs` increments source block refs for the destination's future reference. The source-removal step (`internal/api/v2/batch_operations.go:702-758`) commits the source-side tree update but does **not** explicitly decrement source's reference to those blocks.

Net effect: every cross-repo move leaves `+1` per moved block on `blocks.ref_count`. The leak is reclaimed when GC eventually walks the source's older commit chain and decrements the references encoded there. Functionally correct (eventually consistent), but the counter is briefly inflated. Affects:
- Storage GC threshold timing (a block at `ref_count=2` instead of `ref_count=1` may keep an extra block alive past its lifecycle).
- Counter-based observability (any tool that reads `blocks.ref_count` to estimate storage will be wrong during the lag).

Same-repo move via `processSameRepoMove` does not have this problem (no pin/unpin needed; references stay).

### 19.i. Async Cleanup Goroutines Are Fire-and-Forget With No Observability

Multiple sites schedule cleanup as `go ...` with only `log.Printf` on failure:
- tag cleanup after move/delete/replace (`CleanupFileTagsByPath`, `CleanupFileTagsByPrefix`, `MoveFileTagsByPath`, `MoveFileTagsByPrefix`);
- block ref cleanup after replace/delete (`DecrementBlockRefCountsOnce` followed by `enqueueZeroRefBlocks` or GC enqueue);
- storage-counter cleanup after async delete paths.

A transient DB failure on any of these is logged and forgotten. No retry, no metric, no surface for ops to know cleanup is lagging. Idempotency (via `DecrementBlockRefCountsOnce`'s LWT, and `INSERT IF NOT EXISTS` patterns) means re-running is safe, but there is no mechanism that triggers a re-run on transient failure.

Update 2026-05-19: the OnlyOffice save path is now partially hardened. `saveEditedDocument` persists `onlyoffice_pending_blocks` before the S3 PUT, stores the candidate `publish_commit_id` before the library-head CAS, and reconciles stale pending rows conservatively by checking reachability from the current head. Reconciliation now runs both inline on later saves and from the GC scanner, so quiet libraries no longer depend on another OnlyOffice save to revisit stale rows. That narrows the crash window for OnlyOffice materialized blocks, but the broader debt still applies to the other async cleanup goroutines listed above.

Remaining OnlyOffice-specific follow-ups after that hardening:
- The 5-minute stale cutoff is heuristic. A save that remains in-flight for more than that window after the pending row is inserted but before `publish_commit_id` is persisted can still be misclassified as abandoned by a later save in the same org. There is no heartbeat or LWT guard on the row yet.
- Reconciliation still scans the org partition in `onlyoffice_pending_blocks`. The inline save-path trigger remains hot on every OnlyOffice save, and the GC scanner phase is broad org-wide recovery rather than a targeted per-library worker with cooldowns or lease coordination.
- `scanOnlyOfficePendingBlocks` returns the number of organizations reconciled, and `ScanOnce` currently folds that value into its generic `enqueued` total. This is observability debt only: the phase does not enqueue GC queue rows, so the completion log can overstate "enqueued" work after a successful OnlyOffice reconciliation pass. Future cleanup should either return `0` from non-enqueue phases and rely on phase-specific metrics, or split the scanner result type into `enqueued`, `reconciled`, and `recovered` counters.
- If the process dies after `PutBlockData` succeeds but before `IncrementOrCreateBlock` writes the `blocks` row, the stale pending row can now be discovered later, but the reconciler still only knows how to decrement refcounts and delete the pending row. Because no `blocks` row exists in that window, the physical storage object can remain orphaned; this diff improves observability and bounded row cleanup, but it does not fully close that storage-leak path yet.
- Commit reachability still walks `parent_id` without a hop bound. Deep histories or malformed ancestry can make reconciliation expensive even though cycle detection is present.
- `blockMetadataRegistered` is now effectively a readability guard only: any pre-registration failure returns early, so the later rollback branch only sees the `true` case. Safe, but low-priority cleanup remains.
- `IncrementOrCreateBlock` failure is now intentionally fatal. This is a deliberate behavior change from the earlier buggy path that could continue to metadata publish after failing to confirm the block row.
- There is still no direct end-to-end test for durable `onlyoffice_pending_blocks` write/reconcile behavior. The current suite covers commit reachability and the broader integration environment, but it does not create pending rows and assert both outcomes: reachable `publish_commit_id` rows are dropped without rollback, and abandoned rows decrement refs/enqueue zero-ref blocks via the scanner path. Fault-injection around `updateOnlyOfficePendingBlockCommitID`, `PutBlockData`, and stale-row reconciliation should be added before the next major OnlyOffice cleanup refactor.

Mitigation: pipe these through a small `cleanupQueue` (durable or in-memory bounded) with retry. Out of scope for this branch; track separately.

### 19.j. Upload Saving-Phase Performance Speedup Deferred

The work in `feat/library-write-coordinator` (block pipeline + local mutex coordinator, 17-55× speedup on the "Saving..." phase) was intentionally left out of `feat/multiregion-head-safety` per the [PR58 audit](UPLOAD-PERFORMANCE-PR58-AUDIT.md). The performance baseline of the current branch is the same as `main`; the speedup work lives in branches `feat/library-write-coordinator` and `feat/uploadperformance` and must be re-evaluated separately under contention before merge.

This is intentional debt, tracked here for completeness with section 5 (Web Upload Pipeline Follow-Ups).

### 19.k. Office-Template `CreateFile` Can Leave an Unpublished Physical Block After Exhausted CAS Retries

`internal/api/v2/files.go:1068-1141` now delays Office template upload until after parent-path validation and duplicate-name checks, which fixes the larger regression where `.docx/.xlsx/.pptx` creates could materialize data before a guaranteed `404` or `409`.

However, once a retry attempt successfully uploads the template blob and later loses the HEAD CAS, `templateBlockStored` remains true across retries. If the retry loop eventually exhausts without publishing any commit, the physical blob can remain in block storage even though no library HEAD references it.

This is acceptable for now because the template block ID is deterministic and content-addressed (SHA-256 of the template bytes), so repeated retries do not spray distinct blobs and future creates may reuse the same object. A naive rollback delete would be unsafe without tracking whether the blob pre-existed or is concurrently referenced by another successful create.

If this ever needs to be tightened, the safe options are:
- record/refcount template blobs before publish so failed creates can roll back via metadata rather than raw deletes;
- or track whether `PutBlockData` created a new object vs reused an existing one, then delete only newly-created unreferenced blobs on final failure.

### 19.l. Legacy Multiregion Shell Harness Lags the Canonical Active-Active Proofs

The dedicated multiregion shell harnesses (`scripts/test-multiregion.sh` and
`scripts/test-failover.sh`) still focus on connectivity, routing, and manual
failover drills. They do not carry the full same-library concurrent-write
coverage that now lives in Go integration tests under `internal/integration/`.

This is accepted for the current branch because the correctness-critical proofs
now run through:
- the main compose `test` profile with `sesamefs`, `sesamefs-node-2`, and `sesamefs-node-3` for broad multi-container HEAD races;
- dedicated two-region Go tests for upload finalization against `docker-compose-multiregion.yaml` when region-pinned behavior matters.

The remaining debt is operational ergonomics: the shell harnesses are still the
easiest place to script smoke checks for routing/failover, but they are no
longer the canonical regression surface for active-active write correctness.

### 19.m. Directory Read Paths Still Resolve HEAD and Root in Separate Queries

`ListDirectory` and `ListDirectoryV21` still read `head_commit_id` from
`libraries`, then do a second `SELECT` on `commits` for `root_fs_id`. This is
the same split-read shape used inside `GetLibraryHeadSnapshot`; there is still
no truly atomic cross-table snapshot for read paths.

That means a transient replication or visibility gap can still surface as
`failed to load library data` even though the state is converging correctly.
This is currently treated as read-availability debt rather than write
correctness debt:
- the canonical HEAD source of truth is still `libraries.head_commit_id`;
- write paths rebuild from a fixed HEAD/root pair before publishing;
- the remaining issue is that some read paths can return a transient 500 rather
  than retrying or surfacing a more explicit transient-read failure.

If we tighten this later, the safe follow-up is a bounded read retry/backoff or
explicit transient error mapping around the `libraries -> commits` lookup pair,
not merely swapping `ListDirectory` over to `GetLibraryHeadSnapshot`.

### 19.n. Generic Upload Paths Still Have a `PutBlockData` -> `IncrementOrCreateBlock` Leak Window

The normal upload seams still materialize the physical block before the `blocks`
row is confirmed:

- `internal/api/seafhttp.go` single-shot and chunked finalize paths write the
  block to storage, then write the SHA-1/SHA-256 mapping rows, then call
  `IncrementOrCreateBlock`.
- `internal/api/v2/files.go` direct upload does the same three-step order.

If the process dies or `IncrementOrCreateBlock` fails after the physical object
is written but before the `blocks` row exists, the request now fails closed, but
the physical object can remain orphaned because existing S3-orphan recovery only
covers the opposite order (DB row already deleted, later S3 delete failed).

This is storage-leak debt, not a data-loss regression and not a "distinct blob
per retry" problem: the object key is content-addressed (SHA-256 of the stored
bytes), so repeated retries hit the same object key.

If we tighten this later, the safe follow-up is a durable pending-upload marker
or a recovery path that can reconcile object-store keys against missing block
rows. A raw delete-on-failure path would be unsafe without proving the object
was newly created and still unreferenced.

### 19.o. Concurrent Quota Exhaustion Across Nodes Still Has Coverage Gaps

Current concurrency coverage now includes a targeted multi-instance per-user
storage-quota race in `internal/integration/quotas_test.go`
(`TestMultiInstancePerUserStorageQuotaBlocksConcurrentUploads`), so the repo is
no longer relying only on single-instance quota tests.

The remaining gap is narrower: there is still no targeted test that forces
3-node contention, org-level hard storage quota contention, or quota rejection
during sync auto-merge/publish paths under concurrent head movement.

That leaves an active-active question partially unproven: whether simultaneous
finalize and publish attempts that each pass their own pre-check can still
over-admit bytes under tighter caps outside the currently covered per-user
upload race. Treat this as test debt first, not as a confirmed production bug.

The right follow-up is to extend the multi-instance quota-race coverage in the
default compose `test` profile using `SESAMEFS_URL`, `SESAMEFS_URL_2`, and
`SESAMEFS_URL_3`, before any launch claim depends on strict active-active quota
enforcement.

### 19.p. Ambiguous `blocks` LWT Outcomes Still Lack Durable Idempotence

The upload/sync hotfix intentionally changed `resolveIncrementBlockMutationError`
and `resolveInsertBlockMutationError` to return
`ErrBlockMutationOutcomeUnknown` when Cassandra shows a visible post-LWT state
but the request cannot prove that **this** operation produced it.

That is the safer correctness stance for this branch:

- do not claim success when another writer may have produced the same visible
  state;
- do not roll back a refcount mutation that may already be visible and
  referenced by a later successful publish.

The cost is idempotence debt across client retries:

- `internal/api/sync.go` now returns `500` from `PutBlock` when
  `IncrementOrCreateBlock` cannot attribute the LWT outcome. If the first LWT
  actually applied but the ACK was lost, a client retry can increment the same
  block again.
- chunked `seafhttp` finalize now drops the tracker and rolls back only the
  blocks that were already accounted before the ambiguous block. That avoids
  unsafe rollback of the ambiguous current block, but a full re-upload can
  still increment it again.
- single-shot/direct upload seams share the same fail-closed tradeoff.

This is accepted hotfix debt, not a reason to keep the old behavior. The old
behavior could continue to publish metadata after failing to confirm the block
row. The current branch instead chooses "fail closed, no unsafe rollback" over
false success. The residual failure mode is refcount inflation / delayed GC,
not immediate data loss.

The copy/move surface is narrower than the generic upload surface:

- active publish paths already use `IncrementBlockRefCountsTracked()` and roll
  back the exact confirmed subset on failure;
- the older `IncrementBlockRefCounts()` wrapper now attempts rollback of the
  previously confirmed subset before returning an error. That narrows the old
  partial-progress footgun, but rollback is still best-effort because the
  `DecrementBlockRefCountsOnce()` path does not propagate a rollback failure
  back to the caller. A later hardening branch should either remove this
  generic wrapper or make rollback failures visible/retryable.

What would fully close this debt is durable idempotency or reconciliation at
the block-registration layer itself: an operation key persisted with the block
mutation, a pending-upload/block-promotion table that can reconcile ambiguous
outcomes, or a later refcount repair pass derived from published commit
reachability.

This should stay framed against the two viable design directions:

- **Option B: mutable refcount with CAS/LWT**. Read the current `ref_count`,
  compute the new value, and `UPDATE ... IF ref_count = <value_read>`. This is
  the classic optimistic-concurrency approach. It is correct and safe, and it
  is acceptable when the business cost of a mistake is data loss. The downside
  is that in multiregion it pays full cross-DC Paxos on every contended write.
- **Option C: references as rows rather than a mutable integer**. Store one row
  per `(block_id, referrer)` and make add/remove be `INSERT`/`DELETE` of those
  rows. That removes most writer collisions at the modeling layer instead of
  trying to resolve them later on one hot counter row.

The strongest future direction is likely a hybrid: use row-per-reference where
the steady-state workload can tolerate it, and keep expensive LWT only at the
irreversible GC moment when the system is about to delete from S3 and must prove
that no live references remain.

### 19.q. Chunked Block-Metadata LWT Throttling Is Process-Local, Not Cluster-Wide

`internal/api/seafhttp.go` now gates chunked finalize block-metadata writes with
`finalizeUploadBlockMetadataConcurrency = 1`. This directly targets the prod
incident class where one finalize wave fans out many concurrent
`IncrementOrCreateBlock` Paxos rounds and triggers Cassandra slow-query / CAS
timeout logs.

That gate is intentionally narrow:

- it only serializes block-metadata LWTs inside one SesameFS process;
- it does not coordinate across replicas, regions, sync `PutBlock`, direct v2
  uploads, or copy/move block-pin paths;
- real cross-process serialization still relies on Cassandra `SERIAL`,
  appropriate `CASSANDRA_TIMEOUT`, and deployment topology choices.

This is still worth shipping because it reduces the hottest self-inflicted
pressure source without changing protocol semantics. It should just stay
documented as an operational mitigation, not as a full correctness guarantee.

The current audit did **not** confirm the feared same-finalize self-deadlock.
`finalizeUploadStreaming()` spawns one goroutine per block, and each block
goroutine acquires the metadata permit once, runs `AccountBlockOnce(...)`, and
releases the permit when that goroutine returns. There is no current code path
where one block goroutine acquires a second permit before releasing the first.

The remaining debt here is coverage rather than a confirmed correctness bug.
Current tests prove:

- the permit primitive blocks a second caller while the first holds it;
- chunked HTTP upload flows still round-trip and recover from known finalize
  conflicts.

What is still missing is the integration test that forces
`finalizeUploadStreaming()` down the true multi-block path with a file larger
than `uploadBlockSize` while `finalizeUploadBlockMetadataConcurrency = 1`.
That test should exist before future refactors rely on the throttle as a stable
invariant.

### 19.r. `blocks` Is Hot-Partitioned By `org_id` — RESOLVED (2026-05-26)

The schema branch landed: `blocks`, `gc_block_candidates`, `gc_s3_orphans`,
`block_id_mappings`, and `block_id_mappings_by_internal` now all use per-block
(or per-(org, key)) compound partition keys. No single org concentrates LWT
traffic into one Cassandra partition any more. The platform org
(`00000000-0000-0000-0000-000000000000`) no longer behaves as a central
hotspot for shared traffic.

The two paths that relied on partition-scanning by org were rewritten:

- `gc.Scanner.scanOrphanedBlocks` walks `gc_block_candidates_by_day` by
  `(day, bucket)` from a persisted cursor instead of enumerating candidate orgs
  and partition-scanning the canonical table.
- `gc.Worker.RecoverS3Orphans` walks `gc_s3_orphans_by_day` from a persisted cursor
  across all discovery buckets, and cold start now scans the full 90-day TTL
  horizon instead of a fixed 14-day recovery window.

The old `ListBlocksForOrg` partition scan that served as a backfill safety net
is gone; the new contract is that `DecrementBlockRefCountsOnce` →
`enqueueZeroRefBlocks` → `gcBlockEnqueuer.EnqueueBlocks` →
`EnsureBlockGCCandidate` is the only path a block ever takes into GC.
`gc_zero_ref_enqueue_failures_total` remains the alertable hard-failure signal
that this contract was violated, while
`gc_block_candidate_discovery_degraded_total{source=...}` captures the softer
case where the canonical candidate row succeeded but the
`gc_block_candidates_by_day` repair/write degraded.

`streaming.QueryBlockSizes` was updated to use parallel single-row reads
(`blockSizesConcurrency = 32`) instead of a 100-element `IN` query, because
each block_id now resolves to its own partition.

Closes `docs/KNOWN_ISSUES.md → ISSUE-BLOCKS-HOT-PARTITION-01`. The wider DB
bottleneck audit lives in `docs/SCHEMA-BOTTLENECK-AUDIT.md` — those items are
NOT addressed in this branch.

---

## Pending-published fs_object cleanup (row-per-owner model)

The publish flow tracks each in-flight fs_object with a row-per-owner in
`pending_published_fs_objects` (+ the `_by_day` discovery projection). Two known
gaps remain after closing the shared-delete data-loss race:

**Medium — orphaned fs_object metadata leaks in live libraries.**
`releasePendingPublishedFileOwner` now only deletes the owner row and returns; it
deliberately does NOT delete the `fs_objects` row, because `fs_id` is
content-addressed and a concurrent publish attempt may have just begun reusing
the same row (deleting it there caused real corruption). The failed-attempt sweep
path (`cleanupPendingPublishedFileOwnerAttempt` →
`cleanupPendingPublishedFileAttemptArtifacts` → `CleanupFailedPublishArtifacts`)
removes the commit and the `pub:<attempt>` refs but likewise leaves the
`fs_objects` row. The GC scanner only collects fs_objects for libraries that no
longer exist (`Scanner.scanOrphanedFSObjects` skips any library where
`LibraryExists` is true), so a failed publish inside an ACTIVE library leaks an
fs_object metadata row.

Impact: the data-loss risk is resolved; what remains is a bounded metadata leak —
the row is content-addressed (re-adopted by a later identical publish) and the
backing blocks are still reclaimed once their `pub:` refs hit the 35d TTL.
Pending fix option: a GC pass that sweeps fs_objects unreachable from any commit
in a live library after a grace period, re-verifying reachability AND
owner-absence at delete time to stay race-safe.

**Low — `pub:<attempt>` refs can leak until TTL on a crash between the owner
batch and the block_ids write.** `UpsertPendingPublishedFSObjectOwner` writes the
owner primary row + `_by_day` projection in one `LoggedBatch` (block_ids kept OUT
to avoid `batch too large` on multi-GB uploads), then writes `block_ids` in a
separate `UPDATE`. If the process dies between the two, the owner is already
discoverable by the sweep but carries no `block_ids`; the sweep's artifact
cleanup (`CleanupFailedPublishArtifacts`) only removes `pub:` refs when it has
block IDs, so those refs leak until their 35d TTL expires. This is an operational
leak only — not a visibility or correctness problem — and self-heals via TTL. The
same future GC pass over abandoned owners could reconcile these earlier.

---

*Last updated: 2026-06-01*
