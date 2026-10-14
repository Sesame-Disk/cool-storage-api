# Technical debt: historical sections (archived 2026-10-12)

**Why archived:** resolved sections, dated coverage/monitoring snapshots and a superseded
summary checklist from docs/TECHNICAL-DEBT.md. Archiving the checklist does not resolve its
unchecked items. Active debt remains in [../TECHNICAL-DEBT.md](../TECHNICAL-DEBT.md), including
authentication (§6), modifier/email policy (§8), batch/test debt (§12), API-key scopes (§14),
and upload-token/resume semantics (§15). Issue status remains in
[../KNOWN_ISSUES.md](../KNOWN_ISSUES.md). Coverage instructions live in [../TESTING.md](../TESTING.md).

The historical bodies are preserved; relative links are adjusted for their new location. The
live file keeps moved headings as pointers. Section numbers are not unique (5 and 20–25 occur
more than once); identify ambiguous entries by title.

**Source:** `docs/TECHNICAL-DEBT.md` at commit `5c3163871`. Each block below names its original lines.

---

<!-- from docs/TECHNICAL-DEBT.md lines 160-176 at 5c3163871 -->
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


<!-- from docs/TECHNICAL-DEBT.md lines 238-296 at 5c3163871 -->
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


<!-- from docs/TECHNICAL-DEBT.md lines 326-349 at 5c3163871 -->
## 21. Cassandra-Real Coverage For GC Claim Retry (RESOLVED 2026-08-26, P4a)

### Resolution
`TestP4A_RetryOfTheSameCandidateUnderRealCAS` in
`internal/integration/p4a_claim_authority_test.go` exercises a retry of the same logical
candidate against real Cassandra, and the sibling legs cover the interleaved-owner and
ABA cases. The explicit post-CAS `SELECT` this entry described is gone: a non-applied LWT
returns the row's current values, which is a serial-domain observation, so the classifier
reads those directly and only falls back to a SERIAL settling read when the LWT's outcome
is genuinely unknown.

### What The Answer Turned Out To Be
Not what this entry assumed. It expected a retry to "recognize its prior claim", which was
true only because the claim id derived from `candidateAt` and was therefore shared by
every attempt — the same property that let two concurrent workers both own a row. Under
per-attempt identity a retry is a NEW attempt with a new id: it observes a live owner and
postpones, and an abandoned claim is recovered by stale takeover rather than by inheriting
the id. The test pins that behaviour explicitly so the old expectation cannot creep back.

### Gates
`TestP4A_RetryOfTheSameCandidateUnderRealCAS`, under `SESAMEFS_REQUIRE_P4A_EVIDENCE=1`.

---


<!-- from docs/TECHNICAL-DEBT.md lines 965-997 at 5c3163871 -->
## Summary: Action Items

### Immediate (This Week)
- [x] **Fix serviceURL** - Changed to use `window.location.origin` ✅ (2026-02-09)
- [ ] **Document modal pattern** in CLAUDE.md (done)

### High Priority
- [ ] **Device Flow / service-account auth follow-up** (Section 6) — user-scoped API keys are complete, but there is still no first-class userless automation flow.
- [ ] **Close API-key scope bypasses in upload-link and file-share mutations**
  (Section 14; `ISSUE-APIKEY-READ-SCOPE-UPLOADLINK-FILESHARE-01`) — derived
  sessions preserve scope, but six route-level mutation checks do not consume it.
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


<!-- from docs/TECHNICAL-DEBT.md lines 998-1016 at 5c3163871 -->
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


<!-- from docs/TECHNICAL-DEBT.md lines 1127-1149 at 5c3163871 -->
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


<!-- from docs/TECHNICAL-DEBT.md lines 1150-1171 at 5c3163871 -->
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


<!-- from docs/TECHNICAL-DEBT.md lines 1789-1801 at 5c3163871 -->
### 19.e. RESOLVED (2026-09-11): Initial-Commit Paths Bypassed CAS

Two paths performed unconditional `UPDATE libraries SET head_commit_id = ...`:
- `internal/api/sync.go` — initial commit during sync repo creation.
- `internal/api/v2/fs_helpers.go` — `InitializeLibraryFS` for v2 library bootstrap.

Both now publish through `FSHelper.InitializeLibraryHeadIfUnset`
(`IF head_commit_id = null AND created_at != null`); no unconditional
`head_commit_id` UPDATE remains (`TestPC0NoUnconditionalHeadUpdateRemains`).
See `ISSUE-LIBRARY-INITIAL-HEAD-CONCURRENCY-01` (resolved).

~~Both are correct (the library has no concurrent writers at first-touch)~~ — that premise was false in multi-DC: both updated an *existing* row after a session-consistency read, and `createInitialCommit` was reachable from `GET /seafhttp/repo/:id/commit/HEAD`, so a datacenter whose replica had not yet received a HEAD another datacenter published by CAS could revert it (reproduced on the real 3-DC fixture by the PC-0 audit, 2026-09-10). Resolved 2026-09-11: `TestPC0RawHeadColumnWritersAreInventoried` lists the single CAS initializer, `TestPC0NoUnconditionalHeadUpdateRemains` fails on any reintroduced unconditional UPDATE, and `TestPC0CriticalConsistencyPrimitivesArePinned` pins both load-bearing clauses. The `GET` still initializes, conditionally, and only returns an adopted HEAD once its commit is locally servable. Do not add a "no concurrent writers possible" comment.


<!-- from docs/TECHNICAL-DEBT.md lines 1822-1837 at 5c3163871 -->
### 19.h. RESOLVED: Cross-Repo Move Mutable-Refcount Lag

> Historical note: this section described the retired mutable `blocks.ref_count` model.
> The 2026-05-27 row-per-reference redesign removed these increment/decrement symbols and
> gives each destination/source fs_object an idempotent keyed `fs:` reference.

In `internal/api/v2/batch_operations.go:626`, `pinCopiedTreeBlockRefs` increments source block refs for the destination's future reference. The source-removal step (`internal/api/v2/batch_operations.go:702-758`) commits the source-side tree update but does **not** explicitly decrement source's reference to those blocks.

Historical net effect: every cross-repo move left `+1` per moved block on
`blocks.ref_count` until GC walked the old source chain. This no longer describes live code.
Affects in the retired model:
- Storage GC threshold timing (a block at `ref_count=2` instead of `ref_count=1` may keep an extra block alive past its lifecycle).
- Counter-based observability (any tool that reads `blocks.ref_count` to estimate storage will be wrong during the lag).

Same-repo move via `processSameRepoMove` does not have this problem (no pin/unpin needed; references stay).


<!-- from docs/TECHNICAL-DEBT.md lines 1963-2034 at 5c3163871 -->
### 19.p. RESOLVED: Ambiguous Mutable-Refcount LWT Idempotence

> Historical rationale only. The row-per-reference redesign removed
> `IncrementBlockRefCounts*` / `DecrementBlockRefCountsOnce`; a retried add/delete now targets
> the same `(block_id, referrer)` key. The remaining publish crash windows are tracked through
> `pub:`/`up:` expiry and pending-publish issues, not mutable refcount inflation.

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

**Resolved design decision (2026-05-27): Option C was implemented.** The alternatives below are
retained only as historical rationale:

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


<!-- from docs/TECHNICAL-DEBT.md lines 2075-2111 at 5c3163871 -->
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
is gone; the live contract is keyed `fs:` removal → `BlockHasReferences` →
`enqueueZeroRefBlocks` → `EnsureBlockGCCandidate`.
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


<!-- from docs/TECHNICAL-DEBT.md lines 2472-2494 at 5c3163871 -->
## 22. `ReleaseStaleBlockClaim` reads at session consistency (RESOLVED 2026-08-26, P4a)

### Resolution
The observing read is now `settleBlockDeleteClaimState`, a `SELECT` on the `blocks`
partition at query consistency `SERIAL` — not `Query.SerialConsistency`, which configures
conditional mutations and is ignored for ordinary SELECTs. It is the single observing
read for both `ReleaseStaleBlockClaim` and the ambiguous-claim path, and the release it
drives is a CAS against the exact `(storage_class, storage_key, gc_claim_id,
gc_claimed_at)` it observed.

### Why It Was Safe To Fix Now And Not Before
The original objection was that a global SERIAL read need not intersect a claim committed
under LOCAL_SERIAL, and that mixing the two levels on `blocks` is the one-serial-domain
violation R12 tracks. P0/R12 settled that on 2026-08-23 by pinning every conditional
mutation on `blocks` to `SerialConsistency(gocql.Serial)`: there is now exactly one global
serial domain, so a global SERIAL read intersects it. The stale code comment arguing
against the fix has been rewritten rather than deleted, so the reasoning survives.

### Gates
`TestP4ASettlementReadsUseTheSerialDomain`, plus the `m_settlement_read_is_ordinary`
mutation in `scripts/p4a-mutation-validation.sh`, which requires the guard to go red when
the read is downgraded.
