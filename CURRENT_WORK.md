# Current Work - SesameFS

**Last Updated**: 2026-09-02
**Session**: D0 X1 physical-life handoff architecture freeze (docs + contract tests). #200 merged. X1 still OPEN. GC_ENABLED=false

**📏 File Size Rule**: Keep this file under **500 lines** unless unavoidable. Move detailed content to:
- `docs/KNOWN_ISSUES.md` - Detailed bug tracking
- `docs/CHANGELOG.md` - Session history
- `docs/IMPLEMENTATION_STATUS.md` - Component status
- Other appropriate documentation files

---

## 🚀 NEW SESSION? START HERE

**PROJECT STATUS**: ~85-90% production ready (see `docs/IMPLEMENTATION_STATUS.md`)

### ⚠️ Read this distinction before quoting any blocker count

Two different gates get confused constantly, and conflating them is how "X1 is
the only blocker" turns into "X1 closed → ship it":

| Gate | Blocked by | Status |
|---|---|---|
| **Activating destructive GC** (`GC_ENABLED=true`) | **X1 alone.** X2 closed 2026-08-14. | X1 OPEN; architecture frozen in D0, not implemented |
| **Putting SesameFS in production at all** | **Independent security / resource findings that have nothing to do with GC** | Several open — see below |

X1 is the sole blocker for the *first* row **only**. It is not the sole
production blocker, and no status document should say that it is.

**🔴 PRODUCTION BLOCKERS** (Must complete before deploy):
1. ~~**OIDC Authentication**~~ - ✅ **COMPLETE** (Phase 1 - Basic Login)
2. **Destructive Garbage Collection** - 🔴 **BLOCKED** by X1 physical-delete ABA — the sole blocker *for activating deletion*: X2 cross-DC reference visibility closed 2026-08-14 (destructive liveness at `EACH_QUORUM` behind a topology gate, proven on a real three-DC cluster). Keep `GC_ENABLED=false` on every replica in every DC; the implementation and lease exist but are not permission to activate deletion.
3. ~~**Monitoring/Health Checks**~~ - ✅ **COMPLETE** (Structured logging, `/health`, `/ready`, `/metrics`)
4. **Non-GC readiness findings** - 🔴 **OPEN**, independent of X1. Canonical status per id in [docs/KNOWN_ISSUES.md](docs/KNOWN_ISSUES.md), one-screen list in [docs/OPEN-WORK-INDEX.md](docs/OPEN-WORK-INDEX.md). Single-node HIGHs still open: `ISSUE-RECVFS-DECOMPRESSION-AMPLIFICATION-01`, `ISSUE-SYNC-FSID-WORK-AMPLIFICATION-01`, `ISSUE-APIKEY-READ-SCOPE-UPLOADLINK-FILESHARE-01`. Multi-instance additionally requires `ISSUE-UPLOAD-CHUNK-MULTINODE-01` and `ISSUE-SSO-PENDING-TOKEN-NODE-LOCAL-01`. (`ISSUE-LIBRARY-MUTATION-NO-PERMISSION-CHECK-01` closed 2026-08-22.)

**Then review**:
1. **"What's Next"** → Top priorities (work on #1 unless user specifies)
2. **"Frozen Components"** → What NOT to touch (breaks desktop clients)
3. **"Critical Context"** → Essential facts to remember

### Quick Context
1. **Sync Protocol**: Baseline-verified for the current desktop sync hardening scope. Do not treat it as frozen; compatibility-sensitive follow-up coverage still exists.
2. **Backend API**: ~98% complete by surface count, which is not the same as ready — see blocker #4 for the open authorization/resource findings. OIDC ✅, GC implementation present; destructive activation blocked by X1 alone (X2 closed 2026-08-14), Library Settings ✅, Monitoring ✅, Departments ✅, Admin Panel (groups/users) ✅, OIDC Group/Dept Sync ✅, Tag cascade ✅, Admin Link Management ✅, Upload Links ✅, Org Admin Panel ✅, Superadmin Departments ✅, Custom Share Permissions ✅
3. **Frontend UI**: ~85% complete (all modals migrated, About modal rebranded, File History UI ✅, History Download ✅, Snapshot View ✅, Restore from History ✅, Share Dialog all 8 tabs ✅, permission UI ~75% with granular flags, ~51 ModalPortal wrappers to clean up, folder icons ✅). Plans/permissions Phase 3 is in progress, not closed.
4. **Test flow**: Prefer Docker-first validation. `./scripts/test.sh sync` now runs the single-client sync suite plus the real active-active desktop harness; default behavior is fail-fast and `--keep-going` is opt-in.
5. **Current risk shape**: destructive GC must remain disabled fleet-wide. Of the two confirmed live-data safety blockers, X2 is closed (fix proven on a real three-DC cluster, regression mutation-verified) and X1 remains open — so the *deletion-activation* gate genuinely rests on X1 alone. The upload-fence PR series addresses separate writer/GC races and does not close it. **Product go-live is a different gate** and is not held by X1: see the table above and blocker #4.
6. **X1 design state:** accepted architecture is frozen in [`docs/GC-X1-PHYSICAL-LIFE-HANDOFF-PLAN.md`](docs/GC-X1-PHYSICAL-LIFE-HANDOFF-PLAN.md) (D0, 2026-09-02). That is a documentation freeze, not X1 closure and not GC activation. Historical option comparison remains in [`docs/GC-X1-CLOSURE-OPTIONS.md`](docs/GC-X1-CLOSURE-OPTIONS.md) and is **not** the active roadmap. #199/#200 are characterization evidence only. `GC_ENABLED=false` is pinned explicitly in `docker-compose.prod.yml`. The next productive PRs are W1 (BorrowedFS own-liveness) then W2/R31, then G1 P4c-orphan — not a protocol rewrite of `worker.go` in D0.
7. **Current X1/P3 key:** P1 locator authority merged 2026-08-21 (PR #181), P0/R12 landed 2026-08-23 (PR #183), and P2/R9/R24 closed 2026-08-24 after its structural prerequisite (PR #184). This branch implements the P3 writer boundary: existing-incarnation PUTs revalidate exact `(storage_class, storage_key)` authority immediately before PUT, and metadata repair is non-creating and tuple-bound. Docker evidence for R10/R13/R17 is green, including a deliberate-mutation run in which reordering the fence reads, making the repair create-capable, re-tagging a permanent failure as retryable, or restoring SERIAL to the dedup path each turns a gate red. The cross-DC half of the consistency contract is now MEASURED on the real three-datacenter fixture (`scripts/x2-multidc-validation.sh --p3`): with dc-na down neither fence publication may complete, a fence published in dc-eu blocks a dc-na writer, and both weaker publication levels turn the fail-closed leg red — the `QUORUM` mutation being what the third datacenter exists for. Fence reads pin their own consistency so the argument does not depend on `database.consistency`, which accepts `ONE`. R13 is recorded as two rows: the P3 writer boundary (GREEN) and strict A+ non-overlap (OPEN, dependent on R14/P4 per-attempt claim identity, the residual `worker.go` already named). R18/R27 are explicitly open because rejected `up:` references are retained and deferred-orphan rescheduling is not implemented. Keep `GC_ENABLED=false` on every replica in every DC.

8. **Current X1/P4a key:** this branch binds the destructive claim to the exact physical incarnation and to a per-attempt owner. Migration `017` puts `storage_key` on `gc_block_candidates` and its `_by_day` projection; `EnsureBlockGCCandidate` captures it from the canonical row and refuses to write a candidate it cannot name a `P` for. The claim CAS is `IF storage_class = ? AND storage_key = ? AND gc_state = null AND gc_claim_id = null AND gc_claimed_at = null`, `claimID` is a fresh UUID per ATTEMPT (`blockDeleteClaimID` is deleted and its absence is gated), and release, stale takeover, finalize and candidate cleanup all condition on the exact tuple. `ClaimBlockDelete` returns a classified outcome, so a non-applied CAS is no longer read as completion. Evidence is green on real Cassandra under `SESAMEFS_REQUIRE_P4A_EVIDENCE=1` (four legs: exclusive ownership with exact takeover, the ABA case, retry semantics, and stale-claim release bound to the observed incarnation), with deliberate mutations red via `scripts/p4a-mutation-validation.sh` (see the count below). **Recorded as R14a GREEN / R14b OPEN, R16 GREEN, R20 PARTIAL — the claim path settles in the serial domain, the orphan path does not.** `StartBlockDeleteOrphan` is deliberately untouched and remains the residual that keeps strict A+ non-overlap open; that is P4b. Technical debt #21 and #22 are closed; #23 (GC no longer sweeps metadata-free stubs) and #24 (pre-017 candidate rows need a fresh zero-ref decision, not a backfill) are opened as follow-ups, #24 being a PRE-ACTIVATION requirement rather than a merge blocker. A second review pass found and fixed four defects in the first cut: the stale takeover re-read the row instead of CASing against the authority it observed (so a P1 worker could drop P2 fence); the same hole existed at the owner-agnostic pre-check call site, reachable with no clock skew; the orphan publication and the S3 delete took their locator from an ordinary post-claim re-read rather than from the claim; and ErrBlockCandidateTargetUnavailable was fatal at all three enqueue sites, which was self-poisoning on the fs_object path. The deliberate mutations are red — the script reports its own current total, and the evidence update below is authoritative for it; two of them earned their keep by exposing a non-compiling mutation and a store/mock mirror with neither copy protecting the other. One behavioural narrowing to be aware of: GC no longer deletes metadata-free stub rows, because it has no exact authority over a row with no locator — an unclaimed stub is not an upload fence, and the only producer of a `deleting` metadata-free stub was the old claim CAS. The writer path does claim metadata-free rows, under `gc_state='repairing_stub'`, and cleans up its own; those are not a delete fence and were never GC's to touch. A third review pass closed the last claim-side hole: `releaseBlockClaim` collapsed `BlockReleaseNotOwner` into a bare `nil`, so a late loser — an attempt whose claim was taken over while it worked — walked through the "re-referenced after claim" unwind and consumed the CURRENT owner's candidate. Nothing about the candidate changes in that race (same block, same `P`, same `candidate_at`), so the exact-`P` CAS cannot refuse it; the wrapper now returns the outcome and settlement requires `BlockReleaseReleased`. R16 is GREEN only with both entrances closed — `BlockClaimFreshOwner` at the claim and this one at the release. Landed with it: the post-claim stub branches (driven by an ordinary read the claim had already contradicted in the serial domain, and DLQ-bound with the fence up) are gone in favour of hand-back-and-postpone; `GCFailureCodeBlockAuthorityInvalid` was documented as postponing but was never in `shouldPostponeWithoutRetry`; and the grace postpone and an unnameable claim owner got their own codes. Keep `GC_ENABLED=false` on every replica in every DC.

**P4a evidence update (2026-08-27):** This supersedes every earlier P4a status wording in
this file and every earlier mutation count. The script output is authoritative.
`internal/integration/p4a_claim_authority_test.go` has four real-Cassandra legs: exact
ownership/takeover, physical ABA, retry under real CAS, and stale-claim release bound to
the observed incarnation. `scripts/p4a-mutation-validation.sh` prints its own total on a
clean run — cite that, not a number copied from prose.

**P4a claim visibility (2026-08-28):** After an uncertain LWT, SERIAL seeing our own
`claimID` is not `BlockClaimAcquired`. Production confirms the canonical `blocks` row at
`EACH_QUORUM` (exact P, `deleting`, claim id, `claimed_at`) before granting destructive
authority — the same split P4b-1 uses for orphan `SameTarget`. Direct `applied=true`
stays Acquired. That confirmation read requires effective `blocks.read_repair=BLOCKING`
(empty is not the default); source/schema and `SESAMEFS_REQUIRE_P4A_EVIDENCE=1` pin it.
Mutations `m_settled_own_claim_skips_each_quorum`,
`m_settled_claim_visibility_downgrades_to_local_quorum`,
`m_claim_loses_explicit_non_idempotent_pin`,
`m_claim_loses_zero_retry_policy`,
`m_claim_loses_non_speculative_policy` and
`m_blocks_disables_blocking_read_repair` hold those gates. The mock follows the same
exact classifier, including `claimed_at`, and the initial claim LWT explicitly disables
driver retries and speculative execution.

**Audit hardening files (2026-08-28):** `internal/gc/store_mock.go`,
`internal/gc/store_cassandra.go`, `internal/gc/p4a_claim_ownership_test.go`,
`internal/gc/p4a_claim_authority_guard_test.go`,
`scripts/p4a-mutation-validation.sh`, `docs/TESTING.md`, `docs/CHANGELOG.md` and
`CURRENT_WORK.md`. Docker validation passed the full 50-mutation P4a/R26 matrix and the
full short Go suite.

A fourth review pass closed ordinary post-claim `GetBlockInfo` errors and divergent
locators: each now releases the exact claim, preserves the candidate, and postpones
without consuming retry budget.

A **fifth** pass closed the other half of the same principle. Preserving the candidate
only preserves recovery while a work item can still carry it back, and five post-claim
unwinds — the global verify's non-availability branch, a non-canonical `storage_class`, an
empty/untrimmed `storage_key`, a failed `GetBlockStoreForOrg`, a rejected
`ValidatePhysicalLocator` — released the fence and then returned an ordinary error without
looking at whether the fence was still theirs. A late loser therefore spent the item's
retry budget, and at the cap `ItemBlock` reached a DLQ it never leaves, past a scanner day
cursor already advanced to `today-1`: candidate present, work item unreachable, foreign
fence standing. An item already near the cap needed ONE lost race. All five now return a
classified foreign-owner result on `BlockReleaseNotOwner` (`refuseRetryForForeignClaimOwner`
/ `releaseClaimThenFailWithRetry`), and `processOrg` leaves the stale queue row untouched,
while an attempt that still owns its claim spends retries exactly as before, so a permanent
item defect still reaches the DLQ where a human sees it. Gated by
`TestP4A_ForeignOwnerUnwindDoesNotSpendTheRetryBudget`,
`TestP4A_OwnedUnwindStillSpendsTheRetryBudget`,
`TestP4A_LateLoserDoesNotTouchAnAlreadyAdvancedQueueRow` and
`TestP4AForeignOwnerQueuePolicyPrecedesGenericLifecycle`, plus two new mutations.

The same pass found the mutation gate itself half-open: `m_enqueue_item_mints_block_candidate`
matched literal `\n\t\t` against a CRLF working tree, so it silently applied NOTHING and
aborted the run before the remaining mutations were reached. Fixed to whitespace runs, per
the rule the script's own header states.

P4a remains R14a GREEN / R16 GREEN / R20 claim-side PARTIAL. P4b-2 now carries that
claim authority through orphan publication and finalize: `CommitBlockDeleteOrphanHandoff`
is the irreversible commit on `blocks`, resume is `CommittedOwner` of the stored D, and
`SameAuthority` / `DifferentAuthority` replace same-P-only `SameTarget`. **Explicitly still
open:** X1 itself, R18/R27, P4c-orphan PK, and enabling GC. `GC_ENABLED=false` remains required.

**Queue lifecycle review update (2026-08-27):** The attempted generic LWT hardening of
`RequeueItem` is withdrawn from this branch. `CompleteItem`, `FailItem` and `RequeueItem`
must not be presented as one atomic lifecycle: the first two use ordinary batches and a
partial CAS on requeue would create a false guarantee. `RequeueItem` is restored to the
ordinary logged `DELETE(old)` + `INSERT(new)` path; its concurrent race is a documented
follow-up, not a P4a closure claim.

The mergeable late-loser rule is narrower. `blockClaimForeignOwnerError` is handled by
`processOrg` before generic logging, retry, postpone or DLQ handling. The stale worker calls
none of `CompleteItem`, `RequeueItem`, `FailItem` or retry mutation, and its queue identity,
candidate and retry count remain unchanged. The five P4a ownership/unwind fixes remain
active, including the owned path's normal retry/DLQ behavior. `GC_ENABLED=false` remains
required while the follow-up chooses one authority for `Requeue`/`Complete`/`Fail`, DLQ and
pending state.

**P4b-1 update (2026-08-27):** `StartBlockDeleteOrphan` now publishes the canonical row
with a write-once `EachQuorum + Serial` LWT and no driver retry/speculation. Its result is
classified as `Created`, `SameTarget`, `DifferentTarget`, `NotPublished`, `Ambiguous`,
`Invalid`, `ProjectionUnconfirmed` or `LifecycleAdvanced`; same-target resumes use the stored `first_seen_at`
and repair the identity-only discovery projection only while the row is still `pending_s3`.
The worker only releases/postpones a
confirmed conflict or absent settled row; uncertain, malformed, unconfirmed projection
or advanced-phase states retain claim, candidate and queue lifecycle. Publication-invalid has its own
failure code: the untouched check runs before the postpone check, so reusing
`block_authority_invalid` would have silently taken the candidate-authority error off the
postpone path P4a had just put it on. Unit coverage and ten deliberate
mutations are green/red as required, and real-Cassandra evidence is gated by
`SESAMEFS_REQUIRE_P4B_EVIDENCE=1`. Write-once also drops the stale-phase reset.
A same-P row already at `pending_mapping_cleanup` no longer authorizes finalize; recovery
may still clear that completed-phase row without a physical delete. For that stale-phase
ambiguity itself the trade stays leak-biased until R14b binds incarnation. `SameTarget`
confirms canonical `EACH_QUORUM` visibility before finalize and does not renew TTL
(R28: crash-retry still authorizes Finalize because `Created` already inserted the
fence; remaining TTL is not a lease). `NotPublished` requires SERIAL-confirmed absence.
With a datacenter down, P3 accepts `BlockClaimAmbiguous` (err may be nil after
SERIAL settlement of an unowned row; when SERIAL sees our claim but
`EACH_QUORUM` confirmation is unavailable, the outcome is Ambiguous with a non-nil
error) and orphan `NotPublished` or `Ambiguous`; never claim
`Acquired` or orphan `Created`/`SameAuthority`. P4b-2 then binds that publication to the
exact P4a claim authority. `GC_ENABLED=false` remains required.

**P4b-2 update (2026-08-28):** R14b is GREEN. Migration `019` adds `blocks.gc_orphan_handoff`
(null until commit, never written false) and `gc_s3_orphans.gc_claim_id` / `gc_claimed_at`.
Migration `020` adds `gc_block_delete_lifecycles` (PK `((org_id, block_id), claim_id)`,
phases `published`/`terminal`, never DELETE). Do not fold 019 or 020 into
`001_initial_schema.cql`. `CommitBlockDeleteOrphanHandoff` is the irreversible commit
(EACH_QUORUM + SERIAL, exact `(P,D)`). `AlreadyCommitted` and `CommittedOwner` require
canonical `EACH_QUORUM` visibility; an empty non-applied handoff CAS SERIAL-settles.
After commit: no release, takeover, Complete, Requeue, Fail, or retry++. `CommittedOwner`
revalidates locator/store/topology, then treats `BlockHasReferencesGlobal` as a
contradiction detector (error or refs>0 → `committed_pending`; R3 stays OPEN). Orphan
publication INSERTs the lifecycle tombstone first and SERIAL-re-reads it after the
orphan INSERT; terminal D cannot recreate an authorizing orphan. Finalize
`AlreadyFinalized` requires an exact published `(P,D)` certificate and does **not**
authorize S3 — only applied `Finalized` does. Missing/mismatch/garbage certificates
fail closed. `RecoverS3Orphans` `pending_s3` SERIAL-observes D before S3 and will not
DELETE against terminal (stale-orphan clear is allowed). Production `DeleteS3Orphan`
runs only after `published → terminal`. Never-delete lifecycle partitions grow by one
row per D (operational follow-up). Evidence: unit + `scripts/p4b-authority-mutation-validation.sh`;
real Cassandra `internal/integration/p4b_claim_orphan_authority_test.go` under
`SESAMEFS_REQUIRE_P4B_EVIDENCE=1`. 3-DC P3 fail-closed: with dc-na down,
`CommitBlockDeleteOrphanHandoff` from dc-eu is Ambiguous, not
Committed/AlreadyCommitted. P4b-1's write-once script stays
`scripts/p4b-mutation-validation.sh`. X1 stays OPEN (winner-`Finalized` Physical ABA
is not closed here). `GC_ENABLED=false`.

**D0 update (2026-09-02):** #199 (strict non-overlap characterization) and #200
(BorrowedFS HEAD characterization) are merged as evidence-only PRs. The accepted
closure architecture is now physical-life handoff: orphan becomes durable DELETE
authority for exact `(P,D)` *after* a confirmed handoff, `blocks(P1)` may retire
before `DeleteExact(K1)`, and P2 may overlap P1 cleanup because `P1 != P2`.
Source of record: [`docs/GC-X1-PHYSICAL-LIFE-HANDOFF-PLAN.md`](docs/GC-X1-PHYSICAL-LIFE-HANDOFF-PLAN.md).
**Still CURRENT/TRANSITIONAL:** orphan `(org,L)` writer fence, Finalize-before-S3,
recovery ref re-check, P4c-orphan PK, 90-day orphan TTL. R31 OPEN. H not
reclassified. R18/R27 pending re-evaluation. 020 stays. Activation remains A1
after E1. `GC_ENABLED=false`.

### Inter-session Update (2026-05-21)

- PR61 focus is desktop sync conflict hardening and closeout, not audit-log expansion.
- Sync HEAD promotion now has branch-parity coverage for both `PUT /seafhttp/repo/:repo_id/commit/HEAD` and `POST /seafhttp/repo/:repo_id/update-branch`.
- The hardening baseline is now verified for same-tree idempotence, safe non-overlapping auto-merge, and fail-closed retryable `503` behavior for unsafe conflicts.
- Real active-active desktop proof now exists via two `seaf-cli` clients in Docker hitting separate backend nodes.
- `./scripts/test.sh sync` now chains the single-client sync suite and the active-active harness; it stops on the first failing suite by default, with `--keep-going` available when you need aggregate failure reporting.
- `./scripts/test.sh` now also prints failure excerpts for compose-backed and script-backed suites, so Docker/test-runner failures are immediately visible without digging through full logs.
- Sync cleanup was tightened to release/delete stale `sync-test-*` and `sync-aa-*` libraries so the suite no longer drifts into `Library limit reached` failures.
- A broader canonical quota-reservation prototype was audited and explicitly split out of this branch; the confirmed defects are documented in `docs/KNOWN_ISSUES.md`, and PR61 should keep only the test-runner improvement from that line of work.
- Remaining sync follow-up debt is narrower: deeper-tree active-active branches, quota rejection during auto-merge, and broader 3-node/org-level quota contention races.

### Step 2: Before Making ANY Code Changes
- ✅ Check `docs/IMPLEMENTATION_STATUS.md` - Is component 🔒 FROZEN?
- ✅ If FROZEN → DO NOT MODIFY without explicit user approval
- ✅ If ✅ COMPLETE → Modify with caution, verify tests pass
- ✅ If 🟡 PARTIAL / ❌ TODO → Safe to actively develop

### Step 3: At End of Session - Update Documentation
**📋 MANDATORY: Run [docs/SESSION_CHECKLIST.md](docs/SESSION_CHECKLIST.md)**

---

## Current Branch Summary ✅

**Date**: 2026-05-21
**Focus**: Desktop Sync Conflict Hardening + Docker-first Validation + PR61 Closeout

### Completed In This Branch Slice

- Hardened sync HEAD publish behavior to distinguish safe same-tree retries from unsafe divergent conflicts.
- Added integration proof for both handler entry points: `PUT /seafhttp/repo/:repo_id/commit/HEAD` and `POST /seafhttp/repo/:repo_id/update-branch`.
- Added multi-instance convergence coverage for parent-promotion races during retry.
- Added a real-client active-active harness with safe auto-merge and unsafe `503` scenarios.
- Folded the active-active harness into `./scripts/test.sh sync` and made the wrapper fail-fast by default.
- Fixed `--keep-going`, active-active `--keep`, and sync test cleanup/capacity drift.
- Realigned the docs so status and testing guidance no longer contradict the code and current validation surface.

### Remaining Follow-up Debt

- Extend desktop sync coverage into deeper-tree active-active branches and quota rejection during auto-merge.
- Add broader multi-node quota-race coverage beyond the current per-user concurrent upload test.
- Keep `CURRENT_WORK.md` and related status docs synced whenever the branch focus shifts; this file had drifted badly enough to become misleading.

## Historical Session Summary ✅

**Date**: 2026-03-31
**Focus**: Frontend/Backend Split Audit + Nginx Production Hardening + Bug Fixes

### Completed This Session (Session 59)

#### Nginx frontend container — 6 production bugs fixed ✅
All were silent failures that would only surface under production load:
- `client_max_body_size 100G` at server block (was missing → nginx default 1MB blocked large uploads)
- Proxy timeouts: `proxy_read_timeout 3600s`, `proxy_send_timeout 3600s`, `proxy_connect_timeout 30s` at server block
- `proxy_buffering off; proxy_request_buffering off` on transfer routes (`/d/`, `/u/d/`, `/lib/`, `/repo/`, `/seafhttp/`)
- `proxy_http_version 1.1` + `proxy_set_header Connection ""` on all proxy locations (HTTP/1.1 keepalive)
- `sendfile on; tcp_nopush on; tcp_nodelay on` at server block
- `gzip_vary on; gzip_comp_level 6`

#### Nginx production reverse proxy — improvements ✅
- Upstream keepalive: `keepalive 32/16/8` on all 3 upstream blocks
- Separate rate limit zone for file transfers (`transfer` zone 20r/s vs `api` zone 100r/s)
- Content-Security-Policy header added
- All `add_header` directives now use `always`
- `client_max_body_size 100G` (was 20G)
- Frontend location: `proxy_send_timeout 3600s`, `proxy_connect_timeout 30s`

#### Bundle hash coupling fix ✅
- `internal/api/v2/sharelink_view.go` — `fetchBundleManifest()` fetches `asset-manifest.json` from
  frontend container at startup. 3-level fallback: HTTP → filesystem scan → hardcoded.
  `FRONTEND_URL` env var added to both docker-compose files.

#### Logout fixes ✅
- `internal/api/server.go` — `handleLogout` now calls `SessionManager.InvalidateSession(token)`
  before clearing cookie and redirecting (server-side session was never invalidated before)
- `frontend/src/components/common/logout.js` + `account.js` — clear `sesamefs_auth_token` and
  `custom_permissions_*` from localStorage on click (was left behind after backend redirect)

**Files changed**: `frontend/nginx.conf`, `nginx/nginx.conf.template`, `internal/api/v2/sharelink_view.go`, `internal/api/server.go`, `frontend/src/components/common/logout.js`, `frontend/src/components/common/account.js`, `docker-compose.yaml`, `docker-compose.prod.yml`, `docs/V1-PRODUCTION-ROADMAP.md`, `docs/CHANGELOG.md`, `CURRENT_WORK.md`

### Previous Session (Session 55) — Org Admin Panel + Superadmin Parity

**Date**: 2026-03-05

#### Org Admin Panel — Full Implementation ✅

Implemented complete org admin panel in `internal/api/v2/org_admin.go` with 50+ endpoints covering:

- **Users**: CRUD, password reset, owned/shared repos, search, import, invite (12 endpoints)
- **Groups**: CRUD, members, group libraries, search (13 endpoints)
- **Repositories**: List, delete, transfer, browse dirents (4 endpoints)
- **Trash Libraries**: List, clean, delete single, restore (4 endpoints)
- **Departments & Address Book**: List departments, full address book group CRUD with ancestors (6 endpoints)
- **Group Owned Libraries**: Create + soft-delete (2 endpoints)
- **Share Links**: List + delete with org ownership verification (2 endpoints)
- **Upload Links**: List + delete with org ownership verification (2 endpoints)
- **Devices**: Empty responses — no device table (3 endpoints)

**Performance fixes applied:**
- `resolveUsersMap()` — batch user resolution replacing N+1 queries
- No ALLOW FILTERING — `ListOrgGroupLibraries` iterates org libs + checks shares by partition key
- `sort.Slice` — replaced O(n²) bubble sort in `ListOrgRepos`
- Group quotas stored in `organizations.settings['group_quota_{groupID}']`

#### Superadmin Parity — Departments/Address Book/Group-Owned Libs ✅

Added 9 new endpoints to superadmin panel in `internal/api/v2/admin_extra.go`:
- `AdminListOrgDepartments`, `AdminListAddressBookGroups`, `AdminAddAddressBookGroup`
- `AdminGetAddressBookGroup` (with ancestors), `AdminUpdateAddressBookGroup`, `AdminDeleteAddressBookGroup`
- `AdminAddGroupOwnedLibrary`, `AdminDeleteGroupOwnedLibrary`
- `AdminUpdateGroupMemberRole`

Routes registered in `internal/api/v2/admin.go`.

#### Documentation Updated ✅

- `docs/ADMIN-FEATURES.md` — Added §4 (Superadmin departments), §5 (Org Admin Panel full docs), §6 (Parity table)
- `docs/ENDPOINT-REGISTRY.md` — Registered all 50+ org admin + 9 superadmin endpoints
- `docs/IMPLEMENTATION_STATUS.md` — Updated admin panel rows, added org admin entry, updated metrics
- `CURRENT_WORK.md` — This update

**Files changed**: `org_admin.go`, `admin.go`, `admin_extra.go`, `departments.go`, `ADMIN-FEATURES.md`, `ENDPOINT-REGISTRY.md`, `IMPLEMENTATION_STATUS.md`, `CURRENT_WORK.md`

### Previous Session (Session 54) — Upload File Replace/Autorename Fix

**Problem**: `replace=0` in upload was not triggering auto-rename (`file (1).ext`), default was overwriting.
**Fix**: Updated upload handler to check `replace` param correctly.

### Previous Sessions (53 and earlier — see docs/CHANGELOG.md)

- **Session 53**: Admin trash libraries 405 fix + cleanup handler + orphan data documentation
- **Session 52**: Retrocompat fix — pre-index users, admin `/sys/users/` multi-org fix
- **Session 45**: Superadmin script (`make-superadmin.sh`) + CreateOrganization seafile-js compat
- **Session 44**: Desktop client file browser fixes (oid header, upload/download protocol, trailing slash)
- **Session 33-34**: Admin share link + upload link management (13 endpoints) + verification
- **Session 32**: Bug fix sprint (5 bugs) + tag management enhancement
- **Session 30**: Snapshot view, revert with conflict handling
- **Sessions 22-29**: Admin panel, OIDC sync, File History UI, GC metrics, search, trash
- **Sessions 12-21**: GC, Monitoring, Departments, modal migration, move/copy fixes
- **Sessions 1-11**: Core API, tags, permissions, OIDC, library settings, OnlyOffice

---

## What's Next (Priority Order) 🎯

### 🔴 PRIORITY 1: PR61 Desktop Sync Hardening Closeout

**Status**: 🟡 Baseline verification is complete; the remaining work is merge hygiene and narrower follow-up coverage, not core sync correctness.
**Details**: `internal/api/sync.go`, `internal/integration/library_projection_regression_test.go`, `internal/integration/multi_instance_mutations_test.go`, `scripts/test-sync-active-active.sh`, `scripts/test.sh`

**Use this branch for:**

1. Running Docker-first closeout validation before merge (`./scripts/test.sh sync` first).
2. Extending the active-active sync matrix into deeper-tree conflicts or quota-rejection branches.
3. Keeping branch-status docs aligned with the validated behavior actually present in code.

**Do not let this branch drift into:**

1. Audit-log expansion.
2. Broad frontend cleanup unrelated to desktop sync.
3. New role/ownership behavior changes without a separate branch discussion.
4. Canonical quota reservation/resync experiments; keep that work in a dedicated follow-up branch.

### ✅ ~~PRIORITY 1: Admin Library Management~~ — DONE (2026-02-12)

**Status**: ✅ Complete — 12 endpoints implemented in `internal/api/v2/admin.go`
**Details**: [docs/ADMIN-FEATURES.md](docs/ADMIN-FEATURES.md) § 1

All admin library endpoints implemented: list, search, get, create, delete, transfer, browse dirents, history settings, shared items, trash libraries. Frontend `seafile-api.js` methods already wired.

### ✅ ~~PRIORITY 2: Admin Share Link & Upload Link Management~~ — DONE (2026-02-12)

**Status**: ✅ Complete — 13 endpoints across 5 files
**Details**: [docs/ADMIN-FEATURES.md](docs/ADMIN-FEATURES.md) § 2

Admin share link list/delete fixed; upload links full feature (DB tables, user CRUD, admin list/delete); per-user link endpoints; frontend API methods added.

### 🔴 PRIORITY 3: Audit Logs & Activity Logs — PRIORITIZE NEXT

**Status**: 🟡 Partial foundations exist: `audit_log` for deletion events and `users.last_login_at` for latest successful login, but no historical login/file activity dataset or APIs
**Details**: [docs/ADMIN-FEATURES.md](docs/ADMIN-FEATURES.md) § 3

**Two related systems need implementation:**

1. **Audit Logs** (admin-facing): Login logs, file access logs, file update logs, permission audit logs. Needed for compliance and admin visibility. Frontend pages exist at `frontend/src/pages/sys-admin/logs-page/` and `frontend/src/pages/org-admin/org-logs-*.js`.

2. **Activity Feed** (user-facing): The `/api/v2.1/activities/` endpoint currently returns stub `{"events": []}`. The dashboard activities feed and file activity panels depend on this. Frontend components exist (`frontend/src/pages/dashboard/activity-item.js`, `frontend/src/models/activity.js`).

**What exists today:**
- `internal/middleware/audit.go` — 13 action types defined, `AuditEvent` struct, console-only logging, 8 unit tests
- `audit_log` table — persists deletion/compliance events for GC, groups, departments
- `users.last_login_at` — real latest-login timestamp updated on successful auth and exposed in admin/org-admin user responses
- Frontend UI components for both admin logs and user activity feed

**Next logical slice:**
- Implement `login_logs` first as the bridge between point-in-time `last_login_at` and real historical audit/reporting
- Reuse the successful-auth hooks already touching `users.last_login_at`
- Expose login history to the existing admin login-log pages before expanding to file/activity logs

**Explicit pending gap:**
- `/sys/statistics/file/` and `/org/statistics-admin/file/` still cannot be made real without `file_update_logs` and `file_access_logs`
- That dependency is separate from `login_logs`; login audit work does not unblock file-operation charts by itself
- There is also a confirmed scope bug in org-admin statistics: traffic-based org-admin metrics can resolve to platform-wide aggregates when the org-admin shell is mounted with platform-org context

**Remaining full backlog:**
- 5 dedicated Cassandra tables (login_logs, file_access_logs, file_update_logs, permission_audit_logs, activities) with 90-day TTL
- New `internal/api/v2/audit.go` handler file (~5 endpoints)
- Async DB write integration (buffered channel pattern) across ~15 existing handlers
- Wire up frontend pages to real API endpoints

### ~~🟡 PRIORITY 4: File History UI Wiring~~ — ✅ COMPLETE (Session 23)

Detail sidebar now has Info | History tabs for files. Full-page history also works. Integration tests: 17 assertions passing.

### 📋 PRIORITY 4: Test Coverage Improvement

**Status**: Go integration test framework built (Session 24), coverage gaps identified

**Current unit test coverage** (from `go test -cover`):
| Package | Coverage | Lines | Priority |
|---------|----------|-------|----------|
| `internal/crypto` | 90.8% | ~600 | ✅ ABOVE THRESHOLD (was 69.6%) |
| `internal/api/v2` | 20.5% | 14,136 | HIGH — biggest codebase, most untested |
| `internal/api` | 19.1% | 4,769 | HIGH — sync protocol edge cases |
| `internal/db` | 0% | 1,139 | MEDIUM — all DB access only via integration |
| `internal/middleware` | 42.1% | 752 | MEDIUM — permission logic |
| `internal/storage` | 46.4% | 1,561 | MEDIUM — S3/block edge cases |
| `internal/templates` | 0% | 327 | LOW — email rendering |
| `internal/logging` | 0% | 66 | LOW — instrumentation |
| `internal/metrics` | 0% | 111 | LOW — instrumentation |

**Next steps** (in priority order):
1. **Add more Go integration tests** — share links, admin endpoints, groups, batch ops (parallels existing bash tests)
2. **DB interface mock** — define `Store` interface for `internal/db`, implement mock, unlock unit tests for all handlers
3. **API v2 handler unit tests** — error paths, validation edge cases in `files.go` (3,564 lines), `admin.go` (1,462 lines)
4. **Concurrent access tests** — race detector integration tests for simultaneous uploads/downloads
5. **testcontainers-go** — real Cassandra in CI for `internal/db` unit tests

**Frontend Testing Strategy** (7 test files currently, need expansion):
- Current: `utils.test.js`, `dirent.test.js`, `modal-pattern.test.js`, `seafile-api-tags.test.js`, `seafile-api-oidc.test.js`, `permission-checks.test.js`, `dirent-list-item.test.js`
- **Metrics to track**: Component coverage (% of components with tests), critical path coverage (login→upload→share flow), API mock coverage
- **Priority areas**: Dialog components (conflict dialogs, restore dialogs), API integration layer, permission-based UI visibility
- **Tools**: Jest + React Testing Library (already configured), consider adding Cypress for E2E

### 📋 PRIORITY 5: Frontend Cleanup (Lower)

- **ModalPortal Wrapper Cleanup** — ~51 parent components have unnecessary `<ModalPortal>` wrappers (harmless, cosmetic)
- **Frontend Permission UI** — ~60% complete, readonly/guest users still see some buttons they can't use

---

## Strategic Roadmap

### Phase 1: Production Blockers 🔴 — ALL COMPLETE ✅

| Item | Status | Notes |
|------|--------|-------|
| **OIDC Authentication** | ✅ DONE | Phase 1 complete |
| **Garbage Collection** | ✅ DONE | Queue worker + scanner + admin API |
| **Health Checks/Monitoring** | ✅ DONE | `/health`, `/ready`, `/metrics`, slog logging |

### Phase 2: Core Feature Completion

| Item | Status | Notes |
|------|--------|-------|
| **Admin Panel (Groups/Users)** | ✅ DONE | Option A (OIDC-managed). 16 endpoints + OIDC sync. 29 tests. |
| **Admin Library Management** | ✅ DONE | 12 endpoints in admin.go. See [ADMIN-FEATURES.md](docs/ADMIN-FEATURES.md) § 1 |
| **Admin Link Management** | ✅ DONE | Share + upload links. 13 endpoints. See [ADMIN-FEATURES.md](docs/ADMIN-FEATURES.md) § 2 |
| **Superadmin Departments/Address Book** | ✅ DONE | 9 endpoints. See [ADMIN-FEATURES.md](docs/ADMIN-FEATURES.md) § 4 |
| **Org Admin Panel** | ✅ DONE | 50+ endpoints. Full parity with superadmin. See [ADMIN-FEATURES.md](docs/ADMIN-FEATURES.md) § 5 |
| **Org Delete (3-state lifecycle)** | ✅ DONE (backend) | active → deactivated → deleted (30-day grace → cascade). Frontend TODO: ISSUE-FRONTEND-ORG-DELETE-01 |
| **Audit Logs** | ❌ TODO | 5 tables, ~5 endpoints, ~15 handler integrations. See [ADMIN-FEATURES.md](docs/ADMIN-FEATURES.md) § 3 |
| **File History UI** | ✅ DONE | Detail sidebar History tab + full-page view. 17 integration tests. |
| **GC TTL Enforcement** | ✅ DONE | Scanner Phase 5 (version_ttl_days) + Phase 6 (auto_delete_days) + share link deletion |
| **Frontend Modal Migration** | ✅ 122/122 | All done; ~51 ModalPortal wrappers to clean up |
| **Library Settings Backend** | ✅ DONE | History, API tokens, auto-delete, transfer |
| **Department Management** | ✅ DONE | Admin CRUD + hierarchy, 29 integration tests |
| **Frontend Permission UI** | 🟡 ~60% | Hide/disable based on role |

### Phase 3: Already Complete ✅

| Item | Status | Completed |
|------|--------|-----------|
| Sync Protocol | ✅ 🔒 FROZEN | 2026-01-16 |
| File Operations Backend | ✅ COMPLETE | 2026-01-27 |
| Batch Move/Copy | ✅ COMPLETE | 2026-01-27 |
| Sharing System | ✅ COMPLETE | 2026-01-22 |
| Groups Management | ✅ COMPLETE | 2026-01-22 |
| Department Management | ✅ COMPLETE | 2026-01-31 |
| Admin Panel (Groups/Users) | ✅ COMPLETE | 2026-02-02 |
| OIDC Group/Dept Sync | ✅ COMPLETE | 2026-02-02 |
| File Tags | ✅ COMPLETE | 2026-02-12 (cascade+rename) |
| Permission Middleware | ✅ COMPLETE | 2026-01-27 |
| OnlyOffice Integration | ✅ 🔒 FROZEN | 2026-01-29 |
| Search | ✅ COMPLETE | 2026-01-22 |

### Phase 4: Future Features (Lower Priority)

| Item | Priority | Notes |
|------|----------|-------|
| Thumbnails | LOW | Visual polish |
| File Comments | LOW | Collaboration feature |
| Watch/Unwatch | LOW | Needs notification system |
| Multi-region Replication | LOW | Future scaling |

---

## Frozen/Stable Components 🔒

**Freeze procedure**: See [docs/RELEASE-CRITERIA.md](docs/RELEASE-CRITERIA.md) for the formal stability rules and Component Test Map. Components need ≥ 80% Go coverage, ≥ 90% integration endpoint coverage, zero open bugs, and 3 clean sessions in 🟢 RELEASE-CANDIDATE before reaching 🔒 FROZEN.

### ⚠️ CRITICAL: Sync Code FROZEN (2026-01-19)
**User directive**: DO NOT MODIFY sync code without explicit approval

### Code Files - Sync Protocol 🔒
- `internal/api/sync.go` (lines 949-952, 125-130, 1405-1492) - Protocol formats
- `internal/api/v2/encryption.go` - Password endpoints

### Code Files - Crypto 🔒 (Frozen 2026-02-04)
- `internal/crypto/crypto.go` - PBKDF2, Argon2id, AES-256-CBC (90.8% unit test coverage, 39 tests)

### Code Files - Monitoring/Health 🔒 (Updated 2026-02-04)
- `internal/health/health.go` - Liveness and readiness probes 🔒
- `internal/metrics/metrics.go` - Prometheus metric definitions (GC metrics expanded Session 28)
- `internal/metrics/middleware.go` - Request metrics middleware 🔒
- `internal/logging/logging.go` - Structured logging setup 🔒

### Code Files - OnlyOffice 🔒 (Frozen 2026-01-29)
- `internal/api/v2/fileview.go` - File view auth wrapper + OnlyOffice editor HTML (json.Marshal config). Note: History download handler added (Session 25) — OnlyOffice code paths unchanged.
- `internal/api/v2/onlyoffice.go` - OnlyOffice API endpoint + JWT signing + editor callback

### Code Files - Web Downloads (Updated 2026-02-16)
- `internal/api/seafhttp.go` - `streamFileFromBlocks()` (primary download path — prefetch pipeline, 4MB buffers)
- `internal/api/seafhttp.go` - `HandleDownload()` (token validation, 4MB streaming buffer)
- `internal/api/seafhttp.go` - `addFileToZip()` (ZIP Store method, batch block resolve, 4MB buffers)
- `internal/api/seafhttp.go` - `resolveBlockIDs()` (batch Cassandra IN queries, 100/batch)
- `internal/api/v2/fileview.go` - `ServeRawFile()` / `DownloadHistoricFile()` (batch resolve + 4MB buffers)
- `internal/api/v2/sharelink_view.go` - Share link raw file streaming (batch resolve + 4MB buffers)
- `internal/storage/s3.go` - Custom HTTP transport (64 conn/host, 128KB read buffers)
- ⚠️ `getFileFromBlocks()` is DEPRECATED — kept only for upload metadata path

### Frontend Components 🔒 (Frozen 2026-01-23)
- `frontend/src/pages/my-libs/` - Library list view
- `frontend/src/pages/starred/` - Starred files & libraries
- `frontend/src/components/dirent-list-view/` - File download functionality

### Protocol Behaviors 🔒
- fs-id-list: JSON array (NOT newline-separated)
- Commit objects: OMIT `no_local_history` field
- `encrypted` field: integer in download-info, string in commits
- `is_corrupted` field: integer 0 (NOT boolean)
- `/seafhttp/` auth: `Seafile-Repo-Token` header (NOT `Authorization`)

---

## Critical Context for Next Session 📝

### 🎯 Project Goal
**Mission**: Build complete Seafile replacement ready for production
**Target Users**: Global cloud storage, especially needing China access
**Timeline**: ASAP but thorough - "want it soon, do it right"

### 📊 Current State (Updated 2026-03-05)
- **Sync Protocol**: 100% working, desktop clients fully compatible 🔒 FROZEN
- **Backend API**: ~98% implemented — OIDC ✅, GC implementation present; destructive activation blocked by X1 alone (X2 closed 2026-08-14), Library Settings ✅, OnlyOffice ✅, Tags cascade ✅, Org Admin Panel ✅, Superadmin Departments ✅
- **Frontend UI**: ~83% functional (all modals migrated, folder icons ✅, ~51 ModalPortal wrappers to clean up)
- **Production Ready**: blocked for destructive GC until X1 closes (X2 closed 2026-08-14); keep `GC_ENABLED=false` on every replica/DC
- **Admin Panels**: Both superadmin and org admin at feature parity
- **Active Bugs**: tracked canonically in `docs/KNOWN_ISSUES.md`; X1 is the sole remaining GC blocker (X2 closed)

### Critical Facts to Remember

**Permissions System** (UPDATED 2026-01-27):
- Backend: ✅ 100% COMPLETE - All endpoints check permissions
- Frontend: 🟡 ~30% - "New Library" button done, many features remain
- API returns: `can_add_repo`, `can_share_repo`, `can_add_group`, etc.
- Check `window.app.pageOptions.canAddRepo` in render methods

**User Roles**:
- `admin` → Full access, `is_staff: true`
- `user` → Can create libraries, share, upload
- `readonly` → View only, no write operations
- `guest` → Most restricted, view only

**Test Users** (password: `password` for all):
- `admin@sesamefs.local` (token: `dev-token-admin`)
- `user@sesamefs.local` (token: `dev-token-user`)
- `readonly@sesamefs.local` (token: `dev-token-readonly`)
- `guest@sesamefs.local` (token: `dev-token-guest`)

---

## Documentation Map 📚

### Session Continuity (Read First Every Session)
- **[CURRENT_WORK.md](CURRENT_WORK.md)** - This file - Session state, priorities
- **[docs/KNOWN_ISSUES.md](docs/KNOWN_ISSUES.md)** - Detailed bug tracking
- **[docs/CHANGELOG.md](docs/CHANGELOG.md)** - Session history
- **[docs/IMPLEMENTATION_STATUS.md](docs/IMPLEMENTATION_STATUS.md)** - Component stability matrix

### Protocol & Sync (🔒 Reference Implementation)
- **[docs/SEAFILE-SYNC-PROTOCOL-RFC.md](docs/SEAFILE-SYNC-PROTOCOL-RFC.md)** - Formal RFC with test vectors 🔒
- **[docs/ENCRYPTION.md](docs/ENCRYPTION.md)** - Encrypted libraries, PBKDF2, Argon2id

### Implementation Guides
- **[docs/API-REFERENCE.md](docs/API-REFERENCE.md)** - API endpoints, implementation status
- **[docs/ENDPOINT-REGISTRY.md](docs/ENDPOINT-REGISTRY.md)** - ⚠️ CHECK BEFORE ADDING ENDPOINTS
- **[docs/FRONTEND.md](docs/FRONTEND.md)** - React frontend patterns, modal fixes
- **[CLAUDE.md](CLAUDE.md)** - Complete project context for AI assistant

---

## Quick Commands

```bash
# Run server
docker compose up -d sesamefs frontend

# Rebuild after changes
docker compose build --no-cache sesamefs frontend && docker compose up -d

# Test API with different users
curl -H "Authorization: Token dev-token-admin" http://localhost:8082/api2/account/info/
curl -H "Authorization: Token dev-token-readonly" http://localhost:8082/api2/account/info/

# Run tests (ALWAYS use test.sh)
./scripts/test.sh api              # Bash integration tests (335+ assertions)
./scripts/test.sh go               # Go unit tests
./scripts/test.sh go-integration   # Go integration tests (requires backend)
./scripts/test.sh all              # Everything
./scripts/test.sh api --quick      # Skip slow tests
```

---

## End of Session Checklist

**📋 See [docs/SESSION_CHECKLIST.md](docs/SESSION_CHECKLIST.md) for complete checklist**

Quick reminders:
- [x] Update `CURRENT_WORK.md` (what was done, next priorities)
- [x] Update `docs/KNOWN_ISSUES.md` (bugs fixed/discovered)
- [x] Update `docs/CHANGELOG.md` (add session entry)
- [x] Keep `CURRENT_WORK.md` under 500 lines
