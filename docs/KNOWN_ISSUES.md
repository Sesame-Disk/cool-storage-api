# Known Issues - SesameFS

## Scoped E1/X1 acceptance (2026-10-16)

[Final decision submitted for maintainer merge](./E1-FINAL-DISPOSITION.md#final-acceptance-decision-2026-10-16):
close physical-life X1 on that merge; until then X1 remains OPEN. Accept H as
X3/leak for the measured schedule and the scoped A-I/three-DC/source evidence;
I1/M1's PRE-X1 quality criteria are met. This supersedes dated X1 pending lists,
not independent findings or severities. PRE-GC/A1 remain OPEN, ordinary GC OFF.
In particular Phase 5/6, hard-delete fencing, queue/repair convergence, clock
health and upload availability failures are not closed by this decision.

## H late-PUT evidence update (2026-10-16)

[H1](./E1-H1-PROTECTED-SUCCESSOR.md) supplies the previously missing combined
existing-P2 / late-K1 / HEAD witness on one DC with healthy services. Recommend
X3/leak for measured K1 residue; no retired-P1 publication or runtime defect was
reproduced. Stale discovery refuses safely without its durable root token.
[I1's guard criterion is met](./I1-IDENTITY-EXECUTION-INVENTORY.md), as is
[M1's independent-terminal evidence criterion](./M1-INDEPENDENT-CAS-EVIDENCE.md).
Final maintainer acceptance, X1 and PRE-GC/A1 remain separate. Ordinary GC remains OFF.

## Previous E1 final audit dispositions (#308, 2026-10-16)

[The source/evidence audit](./E1-FINAL-DISPOSITION.md) preserves existing
severities and PRE-GC obligations while separating exact physical-life safety.
R30 original-attempt reference retention stays PRE-GC; the current repair owns
`pub:<repo:commit:fs>`, not the historical fresh-repair-UUID identity. H and the
identity/H8 PRE-X1 evidence debt remain open. No X1 or activation closure.

## E1 compatibility fixture cleanup (2026-10-15)

**P2 FOLLOW-UP / GENERAL, CLOSED-FIX for new fixtures.** The [scoped correction](./E1-OWNED-TEARDOWN-PLAN.md)
preserves COMMITTED assertions and verifies automatic exact teardown in Docker.
The original finding: E1-10e and E1-17 COMMITTED cases left orphan/
recovery-root metadata after their block/lifecycle/object teardown. Six owned
pairs were reproduced during E1-7 compatibility and explicitly disposed;
twenty older pairs remain retained. This is independent test hygiene, tracked
in [technical debt](./TECHNICAL-DEBT.md#e1-compatibility-committed-teardown-residues).

## W2-10 content resurrection closed (E1-17, 2026-10-12)

`ISSUE-PC0-CONTENT-RESURRECTION-PUBLICATION-01` is closed for X1.
`RevertFile`, `RestoreTrashItem` (file and directory), `RevertDirectory` and
`RevertDirents` directory items published HEAD on COMMITTED or retired P1 from
an unpublished Sync commit (RED, all shapes). They now admit only settled
history for every block dependency, the E1-10e policy extended recursively to
directories. Concurrent
Phase 5/6 history removal stays PRE-GC. See
[E1-17](./E1-17-W2-10-RESIDUAL-HANDLERS.md).

## Abandoned repair retention moved to PRE-GC (2026-10-12)

`ISSUE-PUBLISH-REPAIR-DEAD-ROW-RETENTION-01` is now **P2 PRE-GC**; it was
FOLLOW-UP / GENERAL. An abandoned publication repair can keep every later
life of L uncollectable (E1-15C), which defeats reclamation once destructive
GC is on. It is not X1. The plan is to measure first: use the existing repair
metrics, then add a retain reason only if the retention is real, then
disposition it before A1 from that data. No TTL is put on the repair row;
the issue entry explains why. Documentation only.

## E1-16 post-D contract alignment (2026-10-10)

The X1 invariant is physical (G4 contract 5; D0 §3): a logical `up:`/`pub:`/`fs:`
reference of L written after COMMITTED D(P1) is not a P1 dependency. X1 §4 now
records W2-1..9 and W2-12..14 CLOSED for X1, with W2-10 OPEN. The following
move from PRE-X1 (or from blocking X1 through their W2 row) to PRE-GC, with
severity unchanged:
`ISSUE-PUBLISH-REPAIR-STALE-RENEWAL-AFTER-CLEAR-01`,
`ISSUE-PUBLICATION-POST-D-LIVENESS-BEFORE-VALIDATION-01`,
`ISSUE-PUBLISH-REPAIR-KNOWN-LOSER-DURABILITY-01`,
`ISSUE-PUBLISH-REPAIR-DISCOVERY-SCALE-01`, `ISSUE-GC-PUB-REF-ZERO-REF-01`,
`ISSUE-PUBLISH-REPAIR-OWNED-PUB-CLEANUP-RACE-01` and
`ISSUE-PUBLISH-REPAIR-PROGRESS-PAXOS-DOMAIN-01`.
`ISSUE-PC0-EXACT-P-FUNNEL-GAP-01` and `ISSUE-SYNC-PUTBLOCK-EXPIRED-PROVENANCE-01`
are closed for X1. Dated sections below that say a W2 row "remains OPEN" are
historical snapshots; X1 §4 holds the current state.
See [E1-16](./E1-16-POST-D-CONTRACT-ALIGNMENT.md).

## Greenfield first-production reconciliation (2026-09-30)

W2-0 is **CLOSED-EVIDENCE for the covered current-version mechanism** under
the [first-production contract](./X1-CRITICAL-PATH.md#first-production-deployment-contract).
The #239 durable repair / final exact-P ordering and #241 native wire loss,
real SIGKILL, independent recovery and three-DC evidence are preserved.
Both incompatible mixed-version combinations still reproduce D(P)+HEAD;
ISSUE-W2-INCOMPATIBLE-MIXED-ROLLOUT-01 tracks them as P1 FOLLOW-UP / GENERAL,
outside v1 first activation. No compatibility gate is inferred or added.
W2-1, W2-2 and W2-6a remain OPEN through the separately tracked R31 residuals.
Other W2 rows are unchanged. W2 completion -> G4 -> G5 -> E1 -> X1 CLOSED ->
PRE-GC -> A1 -> GC ON remains the #201/#236 path; GC stays disabled.

The dated #238/#239 entries and characterization tables below are historical
evidence snapshots. Their old W2-0 OPEN / pending-evidence statements are
superseded by this reconciliation and the current X1 checklist; they do not
define today's closure state.


### PR #239 crossed audit correction (2026-09-30)

Pre-D GC now distinguishes real references, repair-only protection and zero.
Repair-only releases the exact claim and preserves candidate/discovery/queue,
postponing without retry. After COMMITTED, only actual references retain the
existing contradiction policy; a late repair cannot veto D. Both worker
regressions are included in 17 mandatory continuity legs. G2/G3 retirement and
durable physical continuation are proved; the future physical executor remains
outside this PR. W2-0/W2-6a remain OPEN and GC_ENABLED=false.
See [crossed audit evidence](./W2-0-PUBLICATION-CONTINUITY.md).

## W2-0 follow-up — durable repair gate (2026-09-29)

Covered F1/F2/F3 and Sync-provenanced writers now acquire a non-expiring repair
before final exact-P. GC reads every organization repair page at EACH_QUORUM
and repeats the reference probe after a negative scan; acquisition pins LQ.
Sync preserves its captured placement through queueing. UNKNOWN/unreachable
rows retain existing R31 behavior, so stranded repairs can inhibit collection.
W2-0/W2-6a remain OPEN: network ambiguity, OS process-kill and rollout evidence
are pending. Older GC readers must be upgraded; cold-path cost is up to 32
organization range reads plus a ref probe, proportional to pending rows.
See [plan, evidence, individual re-audit and limits](./W2-0-PUBLICATION-CONTINUITY.md).

**Last Updated**: 2026-09-30 (greenfield W2-0 scope reconciliation)

This document tracks all known bugs, limitations, and issues in SesameFS.

**This file is the issue registry of record.** Every open finding from an audit
or readiness review gets an `ISSUE-*` id here, and the audit document links to
that id instead of restating the finding. Audit documents keep the reasoning,
severity rationale and evidence; this file keeps the identity and the current
status. If the two disagree, this file is right about status and the audit doc
is right about why.

---

### X1 critical-path reset (2026-09-29)

[X1-CRITICAL-PATH.md](X1-CRITICAL-PATH.md) is the source of record for the
order of work toward X1 (W2 exit checklist → G4 → G5 → E1 → X1 CLOSED →
PRE-GC list → A1) and for finding classification. Status changes recorded
here: `ISSUE-PCD1B4-CERTIFICATION-WINDOW-FENCE-01` is PRE-GC only (no longer
a productive-consumer prerequisite); `ISSUE-PCD1-CERTIFIED-BASELINE-IMPLEMENTATION-01`
is frozen and re-evaluated in E1; `ISSUE-GC-HARD-DELETE-LEASE-SERIAL-DOMAIN-01`
is CURRENT-RUNTIME (config-dependent) as well as PRE-GC; `ISSUE-GC-PHASE5-CASCADE-SHARED-FSOBJECTS-01`
stays P0 PRE-GC. The A1 gate is every open entry tagged `PRE-GC` or
`PRE-ACTIVATION` in this file and in TECHNICAL-DEBT.md, plus the
X1-CRITICAL-PATH.md §6 table (which also lists the Phase 6 execute-time TOCTOU
and `ISSUE-GC-STALE-CLAIM-SETTLE-RACE-01`). Boundary notes below that name PC-D1B.5 or a productive
consumer as a PC-2 prerequisite are historical. `GC_ENABLED=false` remains
mandatory.

### PC-D1B.4 boundary (2026-09-23)

PC-D1B.4 decides the certification-window lifecycle fence and characterizes
the current races on real Cassandra and an isolated 3-DC fixture
([PC-D1B-CERTIFICATION-WINDOW-FENCE.md](PC-D1B-CERTIFICATION-WINDOW-FENCE.md),
`ISSUE-PCD1B4-CERTIFICATION-WINDOW-FENCE-01`). Decision only: the runtime fence
is PC-D1B.5; no productive consumer, PC-2, Phase 5 fix or mapping-authority
work is included. `GC_ENABLED=false` remains mandatory.

### PC-D1B.1 boundary (2026-09-22)

PR #228 now implements the cold-path one-library / one-HEAD certifier with
read-only commit and fs_object identity-authority verification, strict source
projection reads, fail-closed mapping behavior, exact-P byte/liveness/GC checks,
and final source/HEAD revalidation. This closes the certifier correctness gate;
PC-D1B.3 (2026-09-23) adds the write-once Mapping Authority with cold-path
promotion (M18/M19), so SHA-1-only dependencies with byte-proven mappings can
certify. Neither closes `ISSUE-LIB-DELETED-FENCE-01`, a productive consumer, PC-2, or historical backfill (greenfield non-goal).
`GC_ENABLED=false` remains mandatory.

## Issue Summary by Priority

### 🔴 Production Blockers (Must Fix Before Deploy)
| Issue | Status | See |
|-------|--------|-----|
| **Share-link password bypass** | ✅ Fixed (2026-07-25) | Password-protected share links served file content, and an OnlyOffice download token, to anonymous callers through the public bootstrap endpoints. The gate now runs before either branch does protected work, and the bundle builder drops content it is handed while `needPassword` holds. See ISSUE-SHARELINK-PASSWORD-BYPASS-01 and `docs/PROD-SECURITY-READINESS-20260724.md` NF-1. |
| **Rate limiting on upload/download/blocks** | ✅ Fixed (2026-08-04) | B4 umbrella closed: A1/A2, B, C and D0-D6 are complete. Download admission ships enabled with auto-derived process-local capacities: the effective cgroup budget is 25% when exposed, the 2 GiB fallback uses a 20% design margin, and the clean baseline derives 16 active slots with 4 raw and 12 other streams. Closure evidence includes 33 retryable `profile=block` refusals, HTTP 503 with `Retry-After: 10`, real `seaf-cli` recovery, a stalled client released through the real nginx on the application's own deadline, and cross-route saturation. An opt-in real-process memory probe exists but its output is not recorded as a closure figure. Two findings were **not** closed by it: ISSUE-OBJECT-STORAGE-ANONYMOUS-DOWNLOAD-01, since closed separately on 2026-08-07, and the now-quantified ISSUE-DOWNLOAD-BYTE-RATE-SHAPING-01, still open. See ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01 and `docs/SEAFHTTP-DOWNLOAD-ADMISSION-D0.md`. |
| **Download admission has no bound before the first write** | ✅ Fixed (2026-08-03) | The idle interval now opens at the streaming phase change instead of at the first byte, and a deferred Gin status preserves it rather than clearing it. A stalled first storage read is cancelled by `idle_write_timeout` on both the D4 and D5 producers. See ISSUE-DOWNLOAD-ADMISSION-PRE-FIRST-WRITE-GAP-01. |
| **Anonymous object-storage downloads** | ✅ Closed (2026-08-07) — never affected production | The `mc anonymous set download` lines existed only in the four development/test Compose files. Production deploys from `docker-compose.prod.yml`, which ships no MinIO, against provider-native S3 that is private by default. The lines are now removed; nothing depended on them, since every MinIO consumer authenticates. The original entry overstated the finding by not separating the dev Compose files from the production one. See ISSUE-OBJECT-STORAGE-ANONYMOUS-DOWNLOAD-01. |
| **Chunked upload chunk state is node-local** | 🔴 Open — multi-instance only | `chunkManager` is process-local; non-sticky routing silently drops files. See ISSUE-UPLOAD-CHUNK-MULTINODE-01 (readiness B1). |
| **Library mutations have no permission gate** | ✅ Fixed 2026-08-22 | `UpdateLibrary`, `POST /:repo_id?op=rename` and `ChangeStorageClass` ran behind `authMiddleware` only and never consulted the caller's authority. All three now distinguish the canonical owner from organization-role overrides: owner API keys require `read-write`, organization owner/admin/superadmin overrides require `admin`, repo API tokens and content shares remain insufficient. Negative tests cover all three handlers. See ISSUE-LIBRARY-MUTATION-NO-PERMISSION-CHECK-01, and ISSUE-LIBRARY-CLASS-CHANGE-RESIDENCY-01 for the remaining runtime-placement and historical-content residency scope. |
| **API-key read scope bypasses upload-link and file-share mutations** | 🔴 Open — verified preexisting 2026-08-22 | Six authenticated mutation handlers resolve the user's underlying library authority or link ownership without applying the current credential's API-key scope. A `read` API key can create, update or delete upload links and, for an owner/admin user, create, update or delete user/group shares. Both direct API-key auth and derived sessions are affected. See ISSUE-APIKEY-READ-SCOPE-UPLOADLINK-FILESHARE-01. |
| **Desktop SSO pending-token store** | 🔴 Open — multi-instance only | In-memory per process; poll and callback on different instances never deliver the token. See ISSUE-SSO-PENDING-TOKEN-NODE-LOCAL-01. |
| OIDC Authentication | ✅ Complete (Phase 1) | `docs/OIDC.md` |
| Garbage Collection | 🔴 **Destructive GC disabled; PRE-GC/A1 OPEN** | X1 (`ISSUE-GC-UPLOAD-FENCE-REMATERIALIZATION-01`) closes for the stated physical-life scope on maintainer merge of [the E1 decision](./E1-FINAL-DISPOSITION.md#final-acceptance-decision-2026-10-16); before merge OPEN. Its [criterion disposition](#issue-gc-upload-fence-rematerialization-01-physical-delete-aba-can-remove-re-uploaded-bytes) separates exact identity, ownership, recovery and publication from remaining convergence. X2 is closed under its stable-topology contract. GC stays OFF fleet-wide until the complete [PRE-GC/A1 gate](./X1-CRITICAL-PATH.md#6-pre-gc-list-a1-prerequisites) also closes; this is not production readiness. |
| Monitoring/Health Checks | ✅ Complete | `/health`, `/ready`, `/metrics` + slog logging |
| **Sync Protocol Permissions** | ✅ Fixed (2026-08-07) | `syncAuthMiddleware` accepted public share-link download tokens as repository credentials. Reproduced live as an unauthorized cross-library block write by an anonymous visitor, plus an escalation through `/download-info` into a full repository sync token. `isRepositorySyncToken` now validates the whole scope — `Source == ""`, `Path == "/"`, `RepoID` bound to the route — before the bearer becomes an identity; all three clauses are mutation-verified. A follow-up split `TokenTypeSync` out of `TokenTypeDownload`, so a download bearer is now refused at the store rather than by shape. See ISSUE-SYNC-LINK-TOKEN-AUTH-01. |
| Sync Race Condition | ✅ Fixed (2026-02-18) | 7 bugs fixed: CAS HEAD updates, parent-chain validation, empty root handling |
| Secrets/Env Management | ✅ Complete (2026-02-11) | All docker-compose vars from `.env`; no hardcoded credentials; JWT secret externalized |
| **Programmatic Auth (API keys)** | ✅ Fixed (2026-04-03) | User API keys now support desktop client, CLI, and automation auth in OIDC-only prod |

### G1 PR #207 status

PR #207 adds exact `(P,D)` S3-orphan identity and a non-expiring durable recovery
root. Its `first_seen_at` token is selected once by the block-delete lifecycle CAS
and reused by the root, canonical row, and discovery projection, including
root-only crash replay. Root settlement remains projection-first and
page-bounded. This is implementation evidence only; destructive GC remains
disabled by the independent X1 gate.

### 🟡 High Priority (Core Feature Gaps)
| Issue | Status | Details |
|-------|--------|---------|
| **Default Library on First Login** | 🟡 Pending | Seafile auto-crea una librería "My Library" al primer login del usuario. Nosotros devolvemos `exists:false` en `GET/POST /api2/default-repo/`. El cliente no bloquea, pero el usuario arranca sin ninguna librería. Ver ISSUE-DEFAULT-REPO-01 abajo. |
| Search File Paths | ✅ Fixed | Full paths now populated during sync and backfill |
| Groups Creation | ✅ Tested | User-facing CRUD + members + group sharing verified (20 integration tests) |
| Departments Support | ✅ Complete | Full CRUD, hierarchy, 29 integration tests |
| API Token Library Access | ✅ Complete | 37 integration tests, full RW/RO enforcement |
| Move/Copy Dialog Tree | ✅ Fixed | `with_parents` param missing in ListDirectoryV21 |
| **Cross-Representation Copy/Move** | 🟡 Guarded limitation | Cross-library batch copy/move is now rejected when source and destination libraries use different block representations. Safe same-representation paths continue to work; a re-materializing transform path is still missing. See ISSUE-BLOCK-REPRESENTATION-COPY-01 and `docs/BLOCK-REPRESENTATION-DESIGN.md`. |
| **Cross-Library Block Read Authorization (hash-only)** | 🟡 Hardening backlog | Blocks are content-addressed and deduplicated per **org**, not per library. The hash-only surfaces (bare-SHA `seafhttp` block GET, `CheckBlocks`, SHA-1→SHA-256 mapping resolution) authorize by org + a repo-scoped token + knowledge of the content hash, not by real library membership. Medium severity — a broken object-level authorization (BOLA) gated only by knowing the exact SHA-256/SHA-1; the session materialization gate blocks *claiming* but not the direct block read or the `CheckBlocks` oracle. Library-scoped read authorization is the fix. See ISSUE-BLOCK-CROSS-LIBRARY-READ-01 below. |
| GC TTL Enforcement | Partial | `version_ttl_days` and `auto_delete_days` have storage, API, and scanner wiring, but their current behavior does not fully match the library settings UI. See ISSUE-LIB-RETENTION-01. |
| Admin Panel | ✅ Working in Docker | `/sys/` route serves sysadmin.html via nginx + Go catch-all |
| Frontend Permission UI | 🟡 ~85% Done | API layer returns real permissions on all directory/file endpoints. **Fixed**: `"owner"` permission now mapped to `"rw"` in API responses (was breaking upload button). **Enhanced (2026-03-11)**: Granular `PermissionFlags` (8 flags) now enforced backend-side via `RequirePermFlag()`. Upload/share link uploaders updated. Remaining: some UI components that conditionally render controls based on flags. |
| Modal Dialogs | ✅ All 122 Fixed | All dialog files use Bootstrap classes |
| Library Settings Backend | Partial | API tokens and transfer are complete. History and auto-delete settings persist, but retention/delete semantics are incomplete. See ISSUE-LIB-RETENTION-01. |
| **Desktop SSO Browser UX** | ✅ Fixed (2026-03-04) | After browser SSO login for desktop client, now shows confirmation page with auto-close. See ISSUE-SSO-01 below. |
| **Desktop Sync Active-Active Conflict Recovery** | 🟡 Follow-up coverage debt | `PUT /commit/HEAD` and `POST /update-branch` now use parent-chain validation, CAS, ancestry-gated auto-merge for safe stale siblings, and `503 + Retry-After` fail-closed responses for unsafe conflicts. The real desktop-client harness now proves both the non-overlapping auto-merge race and the same-path unsafe-conflict `503` preservation path. The remaining gap is broader end-to-end scenario coverage. See ISSUE-SYNC-HEAD-RECOVERY-01 below. |
| **Upload "Don't Replace" (Desktop Client)** | ✅ Fixed (2026-05-22) | `upload-link` now defaults to autorename/no-replace, `update-link` defaults to overwrite, and the token policy is persisted in Cassandra for multi-node safety. See ISSUE-UPLOAD-REPLACE-01 below. |
| **UI Translation (`window.gettext`)** | 🔴 Known gap | The language selector is wired end-to-end (cookie, `/i18n/`, i18next + dates switch), but `window.gettext` is an identity stub and there is no translation catalog, so the ~387 files that translate via `gettext()` stay English. Switching language only localizes the embedded editors and date formatting. See ISSUE-I18N-UI-01 below and `docs/I18N-UI-TRANSLATION-GAP-20260620.md`. |
| **Org Logo Upload** | 🟡 Stub | `UpdateOrgLogo` in org_admin.go accepts the file but does not persist it to storage. Returns a static path from settings. Functional as a route placeholder until an asset storage backend is available. |
| **Login Analytics History** | 🟡 Partial | `last_login` is now real and persisted in `users.last_login_at`, but there is still no historical login event dataset for trend analysis, login audit timelines, or period-based "users who logged in" charts. See ISSUE-LOGIN-ANALYTICS-01 below. |
| **File Statistics Pages Are Still Stubbed** | 🟡 Pending | `/sys/statistics/file/` currently returns all-zero series and `/org/statistics-admin/file/` is still unimplemented. Real data depends on new `file_update_logs` and `file_access_logs` tables, not on `login_logs`. See ISSUE-FILE-STATS-01 below. |
| **Org Admin Statistics Can Leak Platform Scope** | 🔴 Confirmed bug | When org-admin pages are mounted with platform-org context, traffic-based org-admin metrics can resolve to the global aggregate instead of a tenant scope. Affects at least org-admin traffic, org-admin per-user traffic, and org-admin active-users. See ISSUE-ORG-STATS-SCOPE-01 below. |
| **Per-User Storage Quota Enforcement** | ✅ Fixed (2026-05-14) | `CheckStorageQuota` now evaluates org and per-user storage caps; upload callers pass `userID`; sync validates the published tree delta before advancing HEAD and waits for the matching storage-counter adjust before returning. See ISSUE-USER-STORAGE-ENFORCE-01 below. |
| **Quota Enforcement Coverage Gaps in V2 Mutations** | ✅ Fixed (2026-05-14) | The affected non-upload mutation handlers now have visible-delta quota wiring. Deleted file/folder restore remains bounded by configured history retention, deleted-library restore remains bounded by trash retention, and cross-repo move still relies on split-phase destination publish plus source removal. Split-phase publish/counter atomicity remains documented as technical debt (§12d/§12e). See ISSUE-QUOTA-COVERAGE-01 below. |
| **Concurrent Hard-Quota Reservation Hardening** | 🟡 Deferred to separate branch | The existing split pre-check → publish → counter-adjust window is still open. A canonical-row reservation prototype was audited and is not merge-ready for PR61 because it leaks reservations on finalize failure, regresses soft-policy evaluation, races with admin resync, and only hardens `seafhttp`. A smaller safe fix now caches repeated chunk prechecks per upload tracker, but that is not reservation hardening. See ISSUE-QUOTA-RESERVATION-01 below. |
| **Chunked Upload Traffic Accounting Semantics** | 🟡 Accepted debt | Web chunked uploads now pre-check traffic against the declared `Content-Range` total, and repeated storage prechecks no longer walk HEAD on every chunk, but traffic is still recorded only after successful finalize. Abandoned chunk sessions can consume bandwidth without advancing counters. See ISSUE-CHUNKED-UPLOAD-TRAFFIC-01 below. |
| **Block Refcount Idempotence After Ambiguous CAS** | ✅ Fixed (2026-05-27) | Replaced mutable `blocks.ref_count` with keyed `block_references`; add/remove is idempotent per referrer. Historical issue retained below. |
| **`blocks` Hot Partition by `org_id`** | ✅ Fixed (2026-05-26) | `blocks`, `gc_block_candidates`, `gc_s3_orphans`, and the block-id mapping tables now use per-block partitioning so no single org concentrates LWT traffic into one Cassandra partition. The GC scan and S3 orphan recovery paths walk per-day discovery projections instead of partition-scanning by org. See ISSUE-BLOCKS-HOT-PARTITION-01 below. |
| **Soft-deleted Libraries Still Accept Star Mutations** | 🟡 Pending | `StarFile` still treats a library as live if the canonical row exists, even when `deleted_at` is set. That leaves a real post-soft-delete write window and can reopen cleanup drift during library cascade. See ISSUE-LIB-DELETED-FENCE-01 below. |
| **Upload S3 PUT Serialized by Metadata Permit** | ✅ Fixed (2026-06-15) | `finalizeUploadBlockMetadataConcurrency = 1` was acquired around the full S3 block PUT, not just the Cassandra LWT. Fixed in `fix/upload-permit-unwrap-s3-put`. See ISSUE-UPLOAD-S3-PERMIT-01 below and `docs/UPLOAD-PERFORMANCE-SECURITY-2026-06.md`. |
| **Double S3 RTT Per Block (Exists + PUT)** | ✅ Fixed for hot upload paths (2026-06-15) | S3 HEAD replaced by a Cassandra `ProbeBlockReuse` (reuse / direct-PUT / GC-fence) on six server-side upload funnels. NOT global: legacy `BlockStore` Exists+PUT methods remain for unmigrated callers, and the reuse path keeps a canonical-verify HEAD. Fixed in `perf/p2-cassandra-first-hot-reuse`. See ISSUE-UPLOAD-S3-DOUBLE-RTT-01 below and `docs/UPLOAD-PERFORMANCE-SECURITY-2026-06.md`. |
| **Manual GC Triggers Not Gated on `GC.Enabled`** | ✅ Fixed (2026-08-22) | `TriggerWorker`/`TriggerScanner` checked neither `Enabled` nor `started`, so the `GC_ENABLED=false` kill switch rested on a disabled service having no consumer goroutine rather than on a check where the decision is made — and `POST /api/v2.1/admin/gc/run` answered `{"started":true}` on nodes where nothing ran. Never a live bypass; hardened before a refactor could make it one. See ISSUE-GC-MANUAL-TRIGGER-NOT-GATED-01 below. |
| **Read Paths Ignore `storage_key`** | ✅ Fixed by P1 locator authority (2026-08-21); P2/R9/R24 closed 2026-08-24 | Canonical reads, HEAD/existence, reuse/repair, normal GC delete, and orphan recovery consume the persisted exact key and support both legacy deterministic and minted incarnation locators. Every exact-key `BlockStore` operation rejects a key outside its configured prefix plus canonical org ID, and authority sites use `ValidatePhysicalLocator` rather than re-deriving equality. Arbitrary locator formats remain unsupported. See ISSUE-BLOCK-STORAGE-KEY-READS-01 below. |
| **Library HEAD Publish Has No Serial-Domain Contract** | ✅ Fixed 2026-09-14 | The seven canonical HEAD-authority LWTs (four legacy writers, two PC-D1A authority primitives, and PC-D1B.1's context-aware witness CAS) pin `SerialConsistency(db.LibraryHeadSerialConsistency)` = global `SERIAL` and no longer inherit `serial_consistency`. `LOCAL_SERIAL` remains valid for other LWTs. PC-D1A adds authority-only primitives; PC-D1B.1 adds the cold-path certifier but no productive consumer. See ISSUE-LIBRARY-HEAD-SERIAL-DOMAIN-01 below. |
| **Chunked Upload Chunk State Is Node-Local** | 🔴 See Production Blockers | Canonical status is in the Production Blockers table above (`ISSUE-UPLOAD-CHUNK-MULTINODE-01`). Listed here only as a cross-reference for the upload-debt cluster — do not maintain a second status. |

### GC Library-Delete Cleanup Audit (2026-07-10, refreshed 2026-07-16 — P10 fixed)

Follow-up debt from the PR #123 audit. **Verdict (2026-07-16): P10 is fixed through PR-3.** The
original pre-PR-2 bug was GC's org-scoped liveness check versus a content-hash-shared S3 object.
API reads/writes, normal GC deletion, and orphan recovery now use the same org-scoped locator; the
legacy global APIs are removed. The previous "no open known issue can delete live content" verdict
was **retracted** when P10 was found: it only
ever reasoned about liveness within a single org. Within one key layout the delete path is still sound. P6a/P6b safety guards and P1/P1b/P2 durable
purge + cascade are **fixed** on `main` (PR #129). The planned **greenfield prod deploy** starts
from an empty cluster — reconcile/backfill (8A–8C) and pre-fix `gc_pending_items` orphans are
**not required**. Note this depends on a **recreated keyspace + empty/new buckets, never written
by an older binary**: an empty dashboard or `gc_queue = 0` does *not* prove that (this audit began
on a cluster that reported 0 libraries / 0 bytes and still held blocks + MinIO objects). Full
audit: `docs/GC-DELETE-CLEANUP-INVESTIGATION.md`.

| Issue | Status | Details |
|-------|--------|---------|
| **Org-Admin Trash Delete Defers Cleanup** | 🟢 Fixed (6A/6B + parity follow-up, 2026-07-14) | Permanent-delete now stamps a durable `deleted_libraries.purge_requested_at` (migration 012). The immediate Phase-13-deduplicated `library_cascade` enqueue is wired on the v2.1 owner path and the platform/org-admin delete paths; the legacy `/api2/repos/deleted/:repo_id` registration still falls back to marker + Phase 13 recovery. In the wired paths, reclamation normally lands around the grace period; if that best-effort enqueue is lost, Phase 13 adds up to one `ScanInterval` before recovery. See ISSUE-GC-ORG-TRASH-NO-CASCADE-01 below. |
| **Non-Durable, Content-Only Delete Handoff** | 🟢 Resolved (6A/6B + org-admin parity, 2026-07-14) | Correctness is durable via `deleted_libraries.purge_requested_at` + Phase 13; all wired permanent-delete paths (v2.1 owner + platform + org-admin single/bulk) now call `Service.EnqueueLibraryCascade` (identity-matched to Phase 13, a dedup no-op) as a best-effort accelerator, and a lost enqueue costs only latency. The legacy `/api2/repos/deleted/:repo_id` route mounts the handler with `libHandler=nil` and relies on marker + Phase 13. See ISSUE-GC-DELETE-HANDOFF-DURABILITY-01 below. |
| **Stale `gc_libraries_by_policy` on Direct Delete** | 🟡 Low — transient for new deletes | `hardDeleteLibraryRowsFn` does not synchronously call `AddDeleteLibraryPolicyQuery`, but the durable cascade's `HardDeleteLibrary` clears both policy rows. At most a short stale window for new deletes; branch 2 is optional polish. Not a greenfield-prod blocker. See ISSUE-GC-POLICY-INDEX-STALE-01 below. |
| **`pub:` Refs Lack Discoverable Zero-Ref Transition** | 🟡 Confirmed gap (Med, P2 follow-up) — runtime-confirmed by E1-14 | `up:` refs have an expiry projection (`gc_provisional_block_refs` + Phase 0); `pub:` refs do not. When the last `pub:` expires by 35-day Cassandra TTL, nothing runs the zero-ref→candidate transition. E1-14 measured this with real Office writers: no candidate/queue/D, P1/K1 retained. Storage retention, not incorrect deletion. See ISSUE-GC-PUB-REF-ZERO-REF-01 below. |
| **Phase 13 Logs But Does Not Propagate Enqueue Errors** | 🟡 Confirmed gap (Med) | `scanExpiredDeletedLibraries` logs `EnqueueBatch` failures but returns `nil`, and logs+`continue`s on per-library dedupe failure, so the failure is invisible to the phase result/health/metrics and the scan cycle can appear successful. See ISSUE-GC-PHASE13-ERROR-VISIBILITY-01 below. |
| **Integration Suite Leaves DB + MinIO Residue** | 🟡 Test hygiene — **1A/1B/1C/1G fixed; one S3-only orphan open** | The global `ProcessOnce(storage=nil)` fan-out (the only one that deleted other tests' DB rows while orphaning their S3 objects), the permanent `pub:foreign` ref, the upload fixtures' stranded blocks, and both blocks a full run used to strand (1G — the eternal `fs:` one from the zip fixture's SHA-1 corruption, and the `up:sync:` provisional from `quotas_test.go`) are **fixed** and guarded. Still open: one ~90-byte **S3-only object with no `blocks` row**, not yet attributed to a test — undiscoverable by any GC phase. Shared keyspace/buckets and the global `/admin/gc/run` triggers remain as designed. Dev-cluster only; does not affect prod safety. See ISSUE-GC-TEST-RESIDUE-01 below. |
| **Existence Checks Fail Open (transient errors, P6a)** | ✅ Fixed (2026-07-10) | `LibraryExists`/`GroupExists` now propagate non-`ErrNotFound` errors and scanner Phases 3/4/9 fail closed. Phase 9 scans `shares_by_group` directly and uses each projection row's `OrgID`, with unit and real-Cassandra regression coverage. See ISSUE-GC-EXISTENCE-CHECK-FAILOPEN-01 below. |
| **Worker Canonical Revalidation of Orphan Work (P6b)** | ✅ Fixed (2026-07-10) | Durable `canonical_absent` work is point-read against `libraries[(org_id, library_id)]` under the existing library lock; presence/read/fence/unknown-mode paths fail closed. Retry/DLQ and legacy compatibility are covered. See ISSUE-GC-ORPHAN-WORKER-REVALIDATION-01 below. |
| **Cascade Deletes Counter Before Hard Delete** | ✅ Fixed (2026-07-11) | `cascadeDeleteLibrary` now hard-deletes the canonical row before removing the per-library storage counter, closing a crash+restore window that could reactivate an under-counted library. The reordering (the real fix) covers both cascade callers; the counter auto-reclaim is wired only into `processLibraryCascade`. See ISSUE-GC-CASCADE-COUNTER-ORDERING-01 below. |
| **Org Cascade Can Leak an Inert Counter Row** | 🟡 Confirmed gap (Low) | If the per-library counter delete fails/crashes after the hard delete during an **org** cascade, the retry cannot re-find the (now canonical-absent) library to reclaim its counter. The row is inert — aggregates are adjusted at soft-delete and nothing sums `lib:*` counters — so no accounting impact. See ISSUE-GC-ORG-CASCADE-COUNTER-LEAK-01 below. |
| **Legacy `NULL + false` Orphan Rows Run Unguarded** | ⏸ N/A (greenfield prod) | Orphan queue/DLQ rows written before migration 011 would skip the canonical guard. Not present on a fresh deploy. See ISSUE-GC-LEGACY-ORPHAN-UNGUARDED-01 below. |
| **Org Cascade Re-Soft-Deletes on Marker Drift** | 🟡 Confirmed, defense-in-depth (Low) | If `libraries.deleted_at` is set but the `deleted_libraries` marker is absent, the org cascade re-runs `SoftDeleteLibrary`, re-stamping `deleted_at` and re-subtracting aggregates (double-decrement, clamped). Unreachable under normal ops (marker + canonical are written/cleared atomically), so a corruption-only hardening. See ISSUE-GC-ORG-CASCADE-REMARK-01 below. |
| **Markerless Artifacts Are Undiscoverable** | 🟡 Confirmed gap (Med) | Phase 3/4 discovery only enumerates live/deleted library indexes, not surviving commit/fs_object partitions. Drift/manual-ops edge case; not reproduced on the current live path. See ISSUE-GC-ORPHAN-ARTIFACT-DISCOVERY-01 below. |
| **Phase 9 Group-Share Discovery Is a Global Scan** | 🟠 Pending (Med) | The immediate fix streams `shares_by_group` in bounded driver pages with cancellation, but Cassandra still scans every partition. Replace with bucketed active-partition discovery. See ISSUE-GC-GROUP-SHARE-DISCOVERY-SCAN-01 below. |
| **GC Worker/Scanner Robustness (E1/E2/E4/E5/E6)** | 🟡 Confirmed, low-sev | Engine fragility: postpone observability, queue lifecycle arbitration, `dryRun` race vs hard-cutover semantics, pending-projection drift audit, and the S3-orphan per-row claim decision. Block pending leak (E4) fixed for new work (P9). See ISSUE-GC-ENGINE-ROBUSTNESS-01 below. |
| **No Reconcile/Backfill for Existing Orphans** | ⏸ Deferred (greenfield prod) | Brownfield clusters with pre-fix residue may need a read-only reconcile pass. **Not required** for the planned empty prod deploy. See ISSUE-GC-RECONCILE-BACKFILL-01 below. |
| **Block `gc_pending_items` Rows Leak (library-scope mismatch)** | 🟢 Fixed (2026-07-13) | Confirmed live-path leak: `ItemBlock` enqueued with the real `library_id` while the pending key is library-scoped and dedup checks use `uuid.Nil`. Fixed by standardizing block enqueue on `uuid.Nil` + store backstop. Pre-existing orphans on brownfield clusters only. See ISSUE-GC-PENDING-ITEM-BLOCK-LIBRARY-SCOPE-01 below. |
| **Forward SHA-1 Mappings Retained After Physical GC** | 🟡 Confirmed retention debt (Low) | R11a intentionally keeps `block_id_mappings` after physical block deletion. Rows are idempotent per `(org, representation, SHA-1)` rather than per delete churn, but they have no TTL or logical-death reaper and can resolve to a 404 until rematerialization. No live bytes or references are deleted. See ISSUE-GC-LOGICAL-MAPPING-RETENTION-01 below. |

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
| **Superadmin: Org Soft-Delete/Restore UI** | ✅ Fixed (2026-03-25) | Status column with color-coded badges and separate Deactivate / Delete / Reactivate / Restore actions are implemented in `frontend/src/pages/sys-admin/orgs/orgs-content.js`. **This row said "frontend TODO" until 2026-07-25** while the detail section below already recorded it resolved on 2026-03-25 — a summary/detail contradiction inside this same file. Re-verified against the frontend 2026-07-25. See ISSUE-FRONTEND-ORG-DELETE-01 below. |
| **Superadmin: Deleted Orgs List/Filter** | ✅ Fixed (2026-03-25) | Status filter support landed with the same change; search results carry the lifecycle actions too. Same stale-row correction as above. |
| **Org Admin: Org Deletion Awareness** | 🟡 Soft-delete lifecycle ready; bootstrap + frontend TODO | Soft-delete / grace exists server-side, but org-admin bootstrap does not expose `deleted_at` or grace remaining, and the dashboard has no banner. See `ISSUE-ORG-ADMIN-DELETION-BANNER-01`. |

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

### ISSUE-I18N-UI-01: `window.gettext` Is an Identity Stub — Main UI Does Not Translate

**Status**: 🔴 Known gap (2026-06-20) — intentionally not fixed in `fix/language-selector-locales`
**Severity**: High for non-English deployments — selector works, but most of the UI stays English
**Affected**: The entire `gettext()`-based SPA chrome, dialogs, settings, admin/org-admin panels

#### Problem

The frontend has two translation systems and only the minor one is wired:

- **`window.gettext(...)`** is the primary mechanism (387 source files import it). After the
  frontend/backend separation, the Django `jsi18n` catalog that used to populate it was dropped
  and replaced by an identity stub in `runtime-bootstrap.js`
  (`window.gettext = (message) => message`). There is no translation catalog in the repo and no
  backend endpoint that serves one, so every `gettext('X')` returns `'X'` (English) unchanged.
- **i18next** (`t()`/`useTranslation`) is used by 0 application files directly — only internally by
  the bundled `@seafile/seafile-editor` / sdoc-editor components. This is what the language
  selector, `locale-utils`, and the i18n whitelist actually drive.

Net effect: switching language localizes the embedded editors and date/calendar formatting, plus a
few hard-coded labels, but the bulk of the application UI remains in English.

#### Working

- Language selector end-to-end (cookie persistence, `/i18n/` handler, `currentLang`/`langCode`).
- Editor (markdown/sdoc) and `moment`/calendar localization.
- Single source of truth for the locale set (`frontend/src/utils/supported-locales.json`,
  drift-guarded against the Go backend).

#### Remaining Work

Real UI translation needs new infrastructure **and** translation data that does not exist yet:
string extraction → per-locale catalogs → translation content → a catalog endpoint/asset → a real
`window.gettext`/`ngettext` installed by bootstrap before render. See
`docs/I18N-UI-TRANSLATION-GAP-20260620.md` for the full design and suggested phasing
(loader-only first, data later). Tracked as tech debt in TECHNICAL-DEBT.md §20.

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

---

### ISSUE-BLOCK-REPRESENTATION-COPY-01: Cross-Representation Library Copy/Move Is Intentionally Blocked

**Status**: 🟡 Guarded limitation (2026-07-07)
**Severity**: Medium - fail-closed behavior is correct, but users cannot yet move/copy directly between every library pair
**Affected**: Cross-library batch copy/move when source and destination libraries use different block representations

#### Problem

External SHA-1 block IDs are now resolved inside a library representation domain,
not org-wide. A plaintext library and an encrypted library can legitimately reuse
the same external SHA-1 for different physical SHA-256 blocks.

Reusing the source `fs_object` block list across those domains would therefore risk
resolving blocks against the wrong byte representation.

#### Current Behavior

- same-representation copy/move paths are allowed;
- cross-representation batch copy/move is rejected before copying `fs_objects`;
- direct runtime reads no longer fall back to treating SHA-1 as a canonical
  internal block ID.

This is the safe PR1 boundary for representation-aware mappings.

#### Remaining Work

- Add a real transform path that re-materializes destination blocks in the target
  representation domain.
- Backfill explicit `block_representation_id` metadata on legacy rows so the guard
  no longer depends on runtime fallback for older libraries.

#### Related Docs

- `docs/BLOCK-REPRESENTATION-DESIGN.md`
- `docs/ARCHITECTURE.md`

### ISSUE-BLOCK-CROSS-LIBRARY-READ-01: Hash-Only Block Surfaces Authorize by Org, Not Library Membership

**Status**: 🟡 Hardening backlog / known authorization gap (BOLA) (2026-07-07)
**Severity**: Medium — a broken object-level authorization, not merely defense-in-depth. Exploitability is limited by the precondition that the caller knows the exact SHA-256/SHA-1 of the target block, but knowing a hash is not authorization: common-content hashes can be predictable, shared, or leaked via metadata/logs. The session materialization gate only blocks *claiming* an org block into a new file — it does **not** gate the direct `GET /block/:block_id` read or the `CheckBlocks` existence oracle. Representation-awareness narrows the exposure (encrypted libraries use a per-library `representation_id`, so cross-library resolution across encrypted libs is blocked); the residual is same-representation plaintext libraries and the bare SHA-256 read path.
**Affected**: `seafhttp` bare-SHA block GET, `POST /api/v2/blocks/check` (`CheckBlocks`), and SHA-1→SHA-256 mapping resolution.

#### Problem

Physical blocks are content-addressed and deduplicated per **org** (`blocks`,
`block_id_mappings`, and S3 objects are keyed by `(org_id, hash)`), not per library.
The hash-only surfaces authenticate a repo-scoped token and check org + hash, but do
not verify that the caller is a member of the *library that actually references the
block*. Within one org, a party who already knows a block's SHA-256/SHA-1 can:

- confirm the block exists org-wide via `CheckBlocks` (an existence oracle), and
- in principle read block bytes it can address by hash even if it only holds a token
  for a different library in the same org.

Claiming an org block you did not upload *into a new file* is already blocked (the
session-aware check reports S3-only blocks as `needs_upload`), but that gate does not
cover the direct `GET /block/:block_id` read path or the `CheckBlocks` existence
oracle — those remain authorized by org + hash only. Knowing the exact 256-bit
content address is often derived from possessing the content, which limits real-world
exploitability, but it is a precondition, not an authorization check. Treat this as a
genuine (if hard-to-reach) broken authorization to be closed, not a purely theoretical
defense-in-depth nicety.

#### Remaining Work (future hardening)

1. Authorize hash-only reads by real membership of the library that references the block.
2. Block cross-library reads that address a block purely by its SHA-256.
3. Prevent `CheckBlocks` from acting as a cross-library existence oracle.
4. Evolve `block_references` toward `(library_id, ref_kind)` so liveness/authorization can be reasoned about per library rather than per org.
5. Strengthen the publication invariant for `fs:` references so a library only ever gains references to blocks it legitimately owns.

#### Related Docs

- `docs/BLOCK-REPRESENTATION-DESIGN.md`
- `docs/ARCHITECTURE.md`

### (file statistics follow-up, see ISSUE-FILE-STATS-01)

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

`quota_total` is persisted to the `users` table and validated on write against the org's `storage_quota` ([internal/api/v2/write_helpers.go:901-912](../internal/api/v2/write_helpers.go#L901-L912)). `CheckStorageQuota` now receives `userID`, reads `users.quota_bytes`, reads the live per-user counter `user:<orgID>:<userID>`, and returns the more restrictive result between org-level storage and per-user storage.

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

The handler-by-handler approach was chosen over centralizing the logic in `FSHelper.UpdateLibraryHead`. A small shared helper module was added in [internal/api/v2/quota_helpers.go](../internal/api/v2/quota_helpers.go) with three primitives:

- `fsEntryStats(fsHelper, repoID, entry)` returns `(size, fileCount)` for a file or directory entry, recursing through directories.
- `fsEntryDelta(fsHelper, repoID, newEntry, replacing)` returns the `(bytes, files)` delta when `newEntry` is added to a tree, optionally replacing an existing entry.
- `preCheckStorageQuotaForDelta(c, orgID, userID, deltaBytes)` performs the quota pre-check and writes a 403 on failure.
- `applyStorageCounterDelta(c, db, orgID, userID, repoID, deltaBytes, deltaFiles)` calls `AdjustStorageCountersByDeltaSync` and writes a 500 if the counter adjust fails.

Wiring per handler:

1. **CopyFile** (`internal/api/v2/files.go`, `CopyFile`) — captures the replaced entry when `conflict_policy = replace`, computes delta against the source `TargetEntry`, pre-checks before mutating the tree, applies counter delta after `UpdateLibraryHead`.
2. **copyBatchFiles** (`internal/api/v2/files.go`, `copyBatchFiles`) — same wiring per item in the loop.
3. **RevertFile** (`internal/api/v2/files.go`, `RevertFile`) — delta uses `oldEntry` from the target commit, subtracts the size of `existingEntry` when replacing.
4. **RevertDirectory** (`internal/api/v2/files.go`, `RevertDirectory`) — delta walks both trees (old and replaced) recursively via `fsEntryStats`.
5. **RestoreTrashItem** ([trash.go:329](../internal/api/v2/trash.go#L329)) — pre-checks the visible delta; counter delta replaces the previous fire-and-forget `IncrementStorageCounters`.
6. **RevertDirents** ([trash.go:656](../internal/api/v2/trash.go#L656)) — per-item pre-check; items exceeding quota fall into `failedItems` so the batch returns partial success instead of a hard error.
7. **OnlyOffice `saveEditedDocument`** ([onlyoffice.go:831](../internal/api/v2/onlyoffice.go#L831)) — pre-check moved before the S3 `PutBlockData` so we never store bytes that would be rejected. The traversal previously done late in the function is reused.
8. **Cross-repo batch (`processSingleItem`)** ([batch_operations.go:324](../internal/api/v2/batch_operations.go#L324)) — pre-checks the destination delta, applies the destination counter increment after the destination publish, and decrements the source library counter on move.

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
- [internal/api/v2/quota_helpers.go](../internal/api/v2/quota_helpers.go) — shared primitives.
- [internal/traffic/storage.go:136](../internal/traffic/storage.go#L136) — `AdjustStorageCountersByDeltaSync` with negative-clamp protection.

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

> **Related:** the *resumability* side of chunked uploads (`file-uploaded-bytes`, why
> it returns `0` today, and the prerequisites for real resume) is analyzed separately in
> [UPLOAD-RESUME-ANALYSIS-20260619.md](./UPLOAD-RESUME-ANALYSIS-20260619.md).

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
exists. Block-path Paxos remains for first-writer metadata creation, GC
claim/release/finalize, conditional identity repair and orphan lifecycle transitions;
these operations do not mutate a shared reference counter. See
`internal/db/block_references.go`, `FSHelper.RegisterUploadedBlock` /
`RegisterFSObjectBlockReferences`, and the GC worker's `processBlock` /
`removeFSObjectBlockReferences`. Original analysis kept below for context.

**Severity (when active)**: Medium-High operational risk — preferred fail-closed and no unsafe rollback, but some retry paths could still inflate `blocks.ref_count`
**Affected**: `IncrementOrCreateBlock`, sync `PUT /seafhttp/repo/:repo_id/block/:block_id`, seafhttp upload finalize paths, future block-ref accounting callers

#### Historical Behavior Before The Row-Per-Reference Fix

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

The audited hotfix branch fixed the production outage class better than `main` at
that time, but it did **not** provide a fully idempotent block-registration contract
under ambiguous Paxos outcomes. The row-per-reference redesign subsequently removed
that mutable-refcount contract.

#### What Is Already Narrowed

- Copy/move code paths that need rollback already use `IncrementBlockRefCountsTracked()` so they receive the exact list of confirmed increments to unwind on publish failure.
- The older `IncrementBlockRefCounts()` helper now attempts rollback of previously confirmed increments before returning an error. That narrows the partial-progress footgun, but rollback is still best-effort because `DecrementBlockRefCountsOnce()` does not surface a rollback error back to the caller.
- The former chunked-upload permit was intentionally only a **process-local** pressure valve. It lowered the chance of `blocks`-table Paxos storms from one finalize wave, but it was not cluster-wide serialization.
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
- **Direction implemented later**: use row-per-reference for liveness so concurrent writers do not collide on a mutable counter. This did not remove the separate first-writer metadata LWT or the conditional GC/orphan lifecycle transitions.

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
- `block_id_mappings` → `PRIMARY KEY ((org_id, representation_id, external_id))`
- `block_id_mappings_by_internal` → historical only; dropped in PR7 after GC moved to `blocks.sha1`

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

### ISSUE-SYNC-LINK-TOKEN-AUTH-01: Sync Auth Accepts Public Share-Link Download Tokens

**Status**: ✅ **Fixed 2026-08-07** — was a pre-existing authorization gap, not
introduced by D2
**Severity**: Was Critical — reproduced as an unauthorized cross-library block
write by an anonymous public-link visitor, not only as a read bypass
**Affected**: `syncAuthMiddleware` and the authenticated `/seafhttp/repo/:repo_id/*` routes
**Source of record**: Code-verified 2026-08-02; report reviewed during D2 audit;
live exploit and fix 2026-08-07

#### Problem

`syncAuthMiddleware` accepted any valid `TokenTypeDownload` token from the
`Seafile-Repo-Token` header, query parameter, form body or `Authorization`
header. It set `user_id`, `org_id` and `repo_id` from the token without
rejecting `Source == "link"`, requiring the repository-root token shape, or
binding the token's repository to the `:repo_id` route parameter.

A public share-link download token has exactly that accepted type, carries
`Source == "link"`, `RepoID` for the shared library, `Path` for the shared file,
and `UserID` equal to the share-link creator. Replaying that bearer as a sync
credential reached the repository sync surface as the creator;
`checkSyncPermission` then evaluated the creator's library permissions rather
than the share-link's narrower permission.

The threat model needs no token theft, interception, log access or XSS. **The
anonymous visitor is the legitimate recipient of that bearer** — `?dl=1` answers
with a 302 to `/seafhttp/files/<token>/<name>` so the visitor can fetch the
shared file. The defect is that the same bearer also authenticated sync.

Two consequences were worse than the original entry recorded:

1. **Credential-shape escalation.** `/download-info` is inside the same route
   group and mints `CreateDownloadToken(orgID, repoID, "/", userID)` — a
   repository-root token with `Source == ""`. A path-scoped public bearer could
   therefore be exchanged for an ordinary full repository sync credential.
2. **No repository binding at all.** The middleware set `repo_id` in the Gin
   context, but nothing read it: every sync handler uses `c.Param("repo_id")`.
   A token minted for one library reached any other, bounded only by what the
   token's owner happened to be permitted to do there.

The locked-files body path had a narrower, separate defense: its validator
rejects path-scoped and `Source == "link"` download tokens before querying lock
data. That defense did not protect the route-level sync middleware.

This was not a D2 regression. D2 changed download-token `SourceID` wiring and
did not touch `syncAuthMiddleware`.

#### Reproduced Impact

`internal/integration/sync_link_token_auth_test.go` plays the anonymous
visitor: it requests `?dl=1` with no credential, reads the bearer out of the
`Location` header and replays it as `Seafile-Repo-Token`. Against the
unfixed server:

| Route | Result |
|---|---|
| `GET /commit/HEAD`, `/fs-id-list`, `/permission-check`, `/quota-check` | `200` |
| `POST /pack-fs`, `/check-fs` | `200` |
| `POST /check-blocks`, `/recv-fs`, `/update-branch` | `400` |

The `400`s were not refusals: `checkSyncPermission` runs before body parsing on
those handlers, so authentication and authorization had both passed and only the
probe payload was rejected.

Write access was then confirmed as an **unauthorized cross-library block
write**, not inferred from the `400`s: `PUT /seafhttp/repo/<victim>/block/<sha256>`
with a share-link bearer returned `200` against a library that was never shared,
and the owner's own authenticated `check-blocks` stopped reporting the block as
needed — the object had landed in storage under the victim repository's
authority.

Stated precisely, because the distinction matters: `PutBlock` materializes the
block, it does not by itself attach it to the library's file tree. Block
materialization and the logical reference are separate steps in SesameFS, so
this is an unauthorized write into the victim's block storage rather than a
directly visible change to its files. The write routes that *would* complete
that second step — `recv-fs`, `update-branch` — were reached and authorized in
the same run, and answered `400` only on the probe payload.

`/download-info` returned a real repository sync token. A token minted for one
library returned `200` on another library's `commit/HEAD`.

#### Resolution

`isRepositorySyncToken` in `internal/api/server.go` states the accepted shape as
an allowlist, and `syncAuthMiddleware` validates it **before** the bearer
becomes a user identity. A download token authenticates the sync surface only
when all three hold:

- `Source == ""`. Deliberately positive rather than "reject `link`": only `""`
  and `"link"` are ever written today — the `"web"` value in the `AccessToken`
  comment is produced nowhere — so any future source has to be admitted here on
  purpose instead of arriving by default.
- `Path == "/"`. The repository-root shape the four sync-token mint sites issue.
  A token minted to read one file is not a repository credential.
- `RepoID` matches `c.Param("repo_id")`, compared case-insensitively.

The dead `c.Set("repo_id", accessToken.RepoID)` was removed. Nothing read it,
and leaving it implied a binding that did not exist.

All three clauses are load-bearing, verified by mutation against the live
server — each removal fails exactly one test and no other:

| Clause removed | Test that fails |
|---|---|
| `Source == ""` | `TestSyncAuthRejectsRootDirectoryShareLinkToken` |
| `Path == "/"` | `TestSyncAuthRejectsFileScopedDownloadToken` |
| `RepoID` binding | `TestSyncAuthLegitimateTokenIsBoundToItsRepository` |

> **Superseded by the `TokenTypeSync` follow-up below.** The `Source` row no
> longer holds: that bearer is a download token, so the type check refuses it at
> the store before the predicate runs, and removing the source clause now leaves
> the whole integration suite green. Re-measured 2026-08-07. The source clause is
> still load-bearing and still mutation-verified, but at unit level — see the
> follow-up section.

The source clause is not redundant with the path clause, which is why the
root-directory case is pinned separately. A share link on the **library root**
mints a zip bearer with `Path == "/"` and the shared library's own `RepoID`, so
it satisfies both other clauses; only the source check refuses it. "Share my
whole library" is ordinary usage.

The binding test deliberately uses a *legitimate* sync token rather than a link
token. Every other negative test starts from `Source == "link"`, so they all
keep passing when the binding is removed — they were proving the exploit, not
the contract.

#### Evidence

- `internal/api/sync_token_shape_test.go` — table-driven unit contract for
  `isRepositorySyncToken`, including the unknown-future-source case.
- `internal/integration/sync_link_token_auth_test.go` — eight live tests:
  the full replay table, `/download-info` escalation, cross-library read,
  cross-library block write, root-directory link, file-scoped token,
  legitimate-token binding, and a positive test proving ordinary repository
  sync tokens still work.
- Regression: `TestSync*`, `TestSeafhttp*`, `TestLockedFiles*`,
  `TestCheckBlocks*`, `TestWebBlockUpload*` and `TestGC_*` pass in 163.8s; the
  full `-short` unit suite passes.

#### Follow-up: `TokenTypeSync` (2026-08-07)

The audit noted that the fix above rested on a structural shape rather than a
distinct credential: `TokenTypeDownload` served both downloads and sync, so a
future `CreateDownloadToken(..., "/", ...)` with an empty source would have been
accepted by sync authentication. That was tracked as debt and closed separately
in the same session.

`TokenTypeSync` now exists as its own type. `CreateSyncToken(orgID, repoID,
userID)` is the only **exported** constructor that mints it and it **takes no
path**, so the repository-root shape is no longer a value a caller supplies — it
is a property of the constructor. The generic `CreateToken` on both the
in-memory manager and the Cassandra store refuses `TokenTypeSync` outright, and
both guards carry their own regression, so that is enforced rather than merely
conventional. The unexported `createToken` helper still accepts any type,
because `CreateSyncToken` is built on it; Go offers no way to hide it from the
rest of its own package, so the guarantee is precisely "no supported constructor
outside this package can mint one", not "the value is unreachable". `GetToken` compares the stored type exactly, so a
download bearer is refused at the store before any shape logic runs.

> **Rollout: this change is not backward compatible, and that is only safe
> because nothing is deployed.**
>
> No *schema* migration was involved — `token_type` is a free `TEXT` column —
> but that is not the same as being rollout-safe, and the two should not be
> confused. Every sync credential minted before this change is stored as
> `token_type = "download"`, so after the change both validators reject it. The
> incompatibility runs in both directions: an old node mints `download` that a
> new node refuses, a new node mints `sync` that an old node refuses, and a
> rollback strands every client that has already re-minted.
>
> SesameFS has no deployed instance and no live tokens, so this cost is zero
> here. It would **not** be zero on an instance already serving clients. Do not
> cherry-pick this onto one without either proving that the desktop client
> re-acquires through `download-info` on rejection, or staging it: first teach
> every node to accept both shapes while still minting the old one, then switch
> minting once no old node remains, then drop the compatibility branch after one
> full token TTL. A bulk CQL rewrite of `download` → `sync` keyed on
> `Path == "/"` is not a substitute, since it would also convert any legitimate
> root-path download token that happened to exist.

The four sync-token mint sites (`handleRepoTokens`, `GetDownloadInfo`, the v2
repo-download-info handler and library creation) switched to it; the two
validators (`syncAuthMiddleware`, `GetLockedFiles`) now ask for
`TokenTypeSync`. `HandleDownload` and `HandleZipDownload` keep
`TokenTypeDownload`. The `SyncTokenCreator` and `LibraryTokenCreator`
interfaces no longer expose download-token minting where the only use was a
sync token.

`isRepositorySyncToken` keeps all its clauses and gains a type check. The
repository binding is still the live authorization decision — a dedicated type
does nothing to stop a sync token for one library being replayed against
another. `Source` and `Path` become assertions that the mint path was not
widened rather than caller-supplied values, which is why they stay.

Two tests pin the type clause specifically: a download token and an upload token
carrying the *perfect* sync shape — rooted path, empty source, right repository
— which only the type refuses. Mutation-verified: removing the type clause fails
exactly those two cases.

Both are unit-level by necessity, and the reason needs stating carefully.
Root-path download tokens **do** still exist: a share link on a library root
mints its zip bearer with `Path == "/"`, which is precisely why
`TestSyncAuthRejectsRootDirectoryShareLinkToken` exists. What no mint site can
produce is a root-path download token with an **empty source** — the zip bearer
carries `Source == "link"`. So the exact fixture those two unit cases need
cannot be created through any API, which is the point rather than a gap in
coverage.

The distinction that actually changed is this: `Path == "/"` no longer implies
"sync credential". A `TokenTypeDownload` with a root path may legitimately
exist; its type is what stops it authenticating sync.

Adding the type moved where the other clauses are pinned, and the mutation
evidence was re-measured rather than assumed to carry over:

| Clause removed | Before the type split | After |
|---|---|---|
| `Source == ""` | `TestSyncAuthRejectsRootDirectoryShareLinkToken` (integration) | `TestIsRepositorySyncToken/unknown_future_source_with_the_right_shape` and `TestGetLockedFiles_RejectsNarrowerDownloadTokens` (unit) — **the full integration suite now passes without it** |
| `Path == "/"` | `TestSyncAuthRejectsFileScopedDownloadToken` | unchanged |
| `RepoID` binding | `TestSyncAuthLegitimateTokenIsBoundToItsRepository` | unchanged |

The `Source` row moved because the root-directory share-link bearer is a
download token: the type check refuses it at the store before the predicate
runs. The clause still matters — a *sync* token with an unknown source is
exactly what it refuses — but only the unit cases, which carry the correct
type, can reach it. The integration test stays anyway: it is the real-world
exploit shape, and a live end-to-end refusal is worth keeping whichever clause
does the work.

`TestSyncAndDownloadTokensDoNotCrossSurfaces` pins the separation in both
directions: a sync token cannot fetch bytes from `/seafhttp/files/`, and an
ordinary download still returns its exact content. That second half is the
regression a type split is most likely to cause.

#### Related Docs

- [OPEN-WORK-INDEX.md](OPEN-WORK-INDEX.md)
- `internal/api/server.go` (`isRepositorySyncToken`, `syncAuthMiddleware`)
- `internal/db/tokens.go`, `internal/api/seafhttp.go` (`TokenTypeSync`, `CreateSyncToken`)
- `internal/api/sync.go` (`RegisterSyncRoutes`, `checkSyncPermission`)
- `internal/api/sync_locked_files_test.go` (narrow-token defense)

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

**Status**: ✅ Fixed 2026-08-03
**Severity**: Low–Medium — no client-visible corruption, but traffic can be over-recorded
**Affected**: `streaming.StreamBlocks` and `streamFileFromBlocks` (`internal/api/seafhttp.go`)

#### What Is True Today

`StreamBlocks` ([`streaming.go`](../internal/streaming/streaming.go#L288)) returns an
error, and `streamFileFromBlocks` records the writer delta rather than the nominal
file size. The success log is emitted only after the stream returns without an
error. A failure after headers are sent still cannot change the client status, but
it no longer appears as a successful stream or over-counts billed bytes.

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

- **Increment** (`IncrementBlockRefCounts`, `IncrementBlockRefCountsTracked`): propagate the resolution error and abort the copy/publish. This is correct and *safer* than before — `IncrementOrCreateBlock` does `INSERT ... IF NOT EXISTS` ([`fs_helpers.go:996`](../internal/api/v2/fs_helpers.go#L996)), so incrementing an unresolved SHA-1 would create a **phantom SHA-1 row** while leaving the real SHA-256 block un-incremented → potential data loss on a later delete. Failing closed prevents that. Callers here are pre-commit and can abort.
- **Decrement** (`DecrementBlockRefCountsOnce`): resolves **before** consuming the idempotency marker (so a failure doesn't burn the marker), and on resolution failure logs `ERROR` and returns `nil` (decrements nothing). `decrementBlockRefCount` already skips rows that don't exist ([`fs_helpers.go:1107`](../internal/api/v2/fs_helpers.go#L1107)), so there is **no corruption** — but two limitations remain:
  1. **Abort is total, not partial.** If one block of N fails to resolve, the whole decrement is skipped (the old best-effort path decremented the resolvable ones). So the *magnitude* of a leak is larger, though it is now logged.
  2. **The leak is permanent.** The `blocks WHERE org_id` backfill scan was removed (see ISSUE-BLOCKS-HOT-PARTITION-01), so an inflated `ref_count` is never re-discovered as zero-ref. The ~10 post-commit callers also can't distinguish "0 zero-ref blocks" from "aborted" because the signature stays `[]string`.

#### When It Triggers

Only for **SHA-1 blocks** (Seafile desktop client uploads, which need mapping resolution) **and** a Cassandra timeout / missing mapping during resolution. SHA-256 blocks (64 chars) never hit `block_id_mappings`, so the normal path is unaffected.

#### Resolution

Deferred to the `ref_count` counter→per-block-row redesign. That change replaces the LWT-counter model entirely, at which point resolution and decrement semantics are reworked end-to-end rather than patched here.

---

### ISSUE-UPLOAD-S3-PERMIT-01: Upload Finalization Permit Serialized S3 PUT

**Status**: ✅ Fixed (2026-06-15, branch `fix/upload-permit-unwrap-s3-put`)
**Severity**: Critical — process-wide serialization of all block PUTs to S3
**Affected**: `finalizeUploadStreaming` in `internal/api/seafhttp.go`

#### Problem

`finalizeUploadBlockMetadataConcurrency = 1` creates a single-slot semaphore.
The permit was acquired before `retrySeafHTTPBlockMaterialization`, which enclosed
inside the critical section: S3 Exists, S3 PUT, Cassandra LWT, and Cassandra fence
check. The 8-worker pool (`finalizeUploadConcurrency`) had no effect — all workers
blocked on the single slot. Every block in every upload for the entire process was
serialized.

#### Fix

Permit moved inside the `materialize` callback so it guards only the Cassandra
LWT (`registerUploadedBlockAndMappingForUploadFn`). S3 Exists+PUT and the fence
check now run in parallel across all 8 workers.

#### Regression Test

`TestFinalizeUploadBlockMetadataPermitDoesNotBlockS3Put` verifies that the S3 PUT
callback completes while the metadata permit is externally held.

#### Related

- `docs/UPLOAD-PERFORMANCE-SECURITY-2026-06.md` — full audit
- `docs/TECHNICAL-DEBT.md` §25 — updated

---

### ISSUE-UPLOAD-S3-DOUBLE-RTT-01: Double S3 Round-Trip Per Block

**Status**: ✅ Fixed (2026-06-15, branch `perf/p2-cassandra-first-hot-reuse`)
**Severity**: Medium — doubled per-block S3 latency (~100 ms per block on real S3)
**Affected**: upload paths in `seafhttp.go`, `sync.go`, `files.go`, `onlyoffice.go`

#### Problem

`PutBlockAuto` / `PutBlock` / `PutBlockData` in `internal/storage/blocks.go` each
call `bs.s3.Exists(ctx, key)` before `bs.s3.PutAuto(...)`. For new blocks this cost
2 S3 round-trips. On real S3 (~50 ms RTT each), a 1 GB file (128 × 8 MB blocks)
totals ~12.8 s of aggregate RTT work if serialized; with 8 ideal parallel workers
the wall-clock RTT floor is ~1.6 s, plus transfer and queuing. While P-1 was active,
both RTTs were serialized process-wide on top of this.

#### Fix (Cassandra-first probe, "Option B")

The S3 HEAD is replaced by `DB.ProbeBlockReuse(orgID, blockID)` in
`internal/db/block_references.go`, which reads `blocks` metadata, `block_references`,
and the `gc_s3_orphans` fence to return one of:

- `BlockReuseReusable` → `EnsureReusableBlockPresent`: verify the canonical object
  exists (HEAD on the declared key) and repair it (direct PUT) only if missing
- `BlockReuseNeedsPut` → `PutObjectAutoDirect` at the resolved locator (direct PUT, no HEAD)
- `BlockReuseBlockedByGC` → `ErrBlockDeleteInProgress` (retry/back-off)
- `BlockReuseUnknownError` → fail closed before S3 PUT

Wired into all seven governed upload funnels. Each successful materialization performs
a post-reference canonical confirmation, so new blocks pay a second probe plus a
canonical HEAD after metadata publication; dedup hits are confirmed the same way and
repair only when the object is actually missing.

#### Scope — NOT a global removal of the HEAD

This is fixed for the *server-side hot upload paths*, not across the whole repo:

- The `BlockStore` methods `PutBlockAuto` / `PutBlock` / `PutBlockData`
  (`internal/storage/blocks.go`) still do `Exists` + `PutAuto` for legacy callers;
  the session-mode `v2/blocks.go` handler is migrated, and its intentionally
  metadata-free no-session branch was removed outright in PR-7 (finding F8), so
  there is no longer a no-session handler path to migrate.
- The `Reusable` path does not skip S3: `EnsureReusableBlockPresent` adds a targeted
  HEAD on the canonical key (with repair-on-miss) so the upload never publishes a
  reference to a physically-missing object. This is a deliberate safety/perf trade —
  the HEAD is back on dedup hits, but reuse is now self-healing.

#### Why skipping the legacy PUT is safe

GC-race safety derives from the *reference-first, then fence-check* protocol in
`FSHelper.RegisterUploadedBlock` (`fs_helpers.go`) versus GC's
*claim-then-verify-references* in `worker.go:processBlock` (L410–443) — a
Dekker-style mutual flagging — not from the S3 PUT. The `gc_s3_orphans` fence is
written before the S3 `DeleteObject`, so probes can observe the normal in-flight
delete window. This does **not** close X1: after a recoverer clears Cassandra state,
an already-issued S3 DELETE can still arrive late, and Cassandra claim generations
cannot revoke it. Only never-reused physical keys close that ABA component. The
canonical verify/repair on the reuse path is an additional check, not permission to
enable destructive GC.

The materialization retry loop (`retrySeafHTTPBlockMaterialization`,
`v2.RetryUploadedBlockMaterialization` and the template-CreateFile wrapper) retries on
a GC delete fence (`ErrBlockDeleteInProgress`) in **either** phase, and on a transient
failure that is **tagged** `v2.ErrBlockMaterializationTransient`. The materialize phase
tags `RegisterUploadedBlock` Cassandra I/O and mapping writes. The shared store helper
also tags canonical HEAD, repair PUT and direct PUT failures; the web-session funnel
uses it directly, while all reusable paths reach its tagged HEAD/repair logic through
`EnsureReusableBlockPresent`. Raw probe errors and the six older manual direct-PUT
branches remain non-retryable. As of PR-3,
`RegisterUploadedBlock` no longer waits out the fence internally: it reads the fence
once and propagates `ErrBlockDeleteInProgress`, so the wrapper repeats store→materialize
when the fence is observed. PR-5 adds a mandatory canonical confirmation after
materialization, repairing bytes even when a complete GC cycle cleared its fence before
the writer observed it. A permanent
metadata failure (`db.ErrBlockMetadataPermanent`) is returned untagged and not retried.
PR-8 replaced the provisional-expiry rollback guard with a single logged batch that writes
the reference and its expiry tracking together (F10), so that write is now retryable like
the rest — there is no half-written state left to compensate for. The retry metric
`block_upload_materialization_retries_total{surface,reason}` labels the reason by the
failing phase (`probe` = store phase, `materialization` = metadata materialize phase,
`gc_fence`), so a metadata-write failure is never labeled `probe`.

#### Caveats

- `AccountBlockOnce` still deduplicates by block position (index), not SHA-256, so
  same-content blocks at different positions within one upload produce separate
  probes/PUTs. The Cassandra probe provides cross-upload dedup, matching what the
  old S3 HEAD did — no dedup regression.
- S3 HEAD and repair-PUT failures from `EnsureReusableBlockPresent` are tagged
  `ErrBlockMaterializationTransient`, so the bounded whole-cycle retry handles a
  flaky confirmation. Exhaustion still fails closed after metadata/reference writes;
  a later idempotent upload resumes from the authoritative metadata state.

#### Related

- `docs/UPLOAD-PERFORMANCE-SECURITY-2026-06.md` — P-2
- `docs/TECHNICAL-DEBT.md` §25
- ISSUE-BLOCK-STORAGE-KEY-READS-01 below — P1 locator-authority follow-up

---

### ISSUE-BLOCK-STORAGE-KEY-READS-01: Read Paths Ignore `storage_key` (key derived from hash)

**Status**: ✅ Fixed by P1 locator authority (2026-08-21); minted incarnation support closed with P2/R9/R24 (2026-08-24)
**Severity**: Resolved safety prerequisite; High if an unvalidated locator reaches a canonical path
**Affected**: `internal/storage/blocks.go` read methods; GC delete path

#### Problem

The `blocks` table has a `storage_key` column recording where a block's object
lives. P1 now resolves that persisted key for canonical reads, reuse/repair and
GC deletion. The low-level hash-based methods remain deterministic convenience
APIs for legacy/no-metadata callers; canonical production paths do not use them
to choose a physical target.

`EnsureReusableBlockPresent` (`internal/api/v2/upload_reuse.go`) consumes and
validates the persisted key. An empty, padded, foreign or malformed
`storage_key` fails closed before any S3 HEAD or repair PUT.

The destructive side validates rather than blindly trusting the row.
`ValidatePhysicalLocator` centralizes that check in the org-scoped `BlockStore`:
it first requires a well-formed SHA-256 block ID, then enforces the store's
configured prefix plus canonical org ID, then accepts only the legacy
deterministic key or that key with one canonical lowercase UUID incarnation
suffix. `processBlock` calls it during
the authorization phase (before
`StartBlockDeleteOrphan` and `FinalizeBlockDelete`), and `RecoverS3Orphans` calls
it on the reloaded canonical row. Neither path records or consumes recovery state
for a refused delete. `storage.BlockStore` no longer
exposes hash-derived `PutBlockAutoDirect`/`DeleteBlock` at all, so the storage
layer never picks a delete target on a caller's behalf. That is the whole of what
the signature guarantees: `StorageKeyForHash` is still public, so passing a
derived key to `DeleteBlockByStorageKey` compiles. Where the key came from is
pinned by tests, not by the compiler.

#### Compatibility and scope

New rowless materializations persist a non-empty minted key returned by the same
resolution used for the PUT. Existing deterministic rows remain valid for reads,
HEAD, reuse, repair and GC validation. API reads, verify/repair and GC consume
the exact persisted value; a missing or normalized value is not tolerated.

#### The latent risk

P2 supports only its specified incarnation grammar. It does not authorize an
arbitrary key layout, prefix migration or re-sharding. Those require a separate,
explicit locator migration across reads, writes and GC.

#### Resolution

P1 made `storage_key` authoritative on canonical paths. P2 carries a freshly
minted, never-reused value through PUT and a single-use global-SERIAL canonical
install; an ambiguous install receives one SERIAL settlement read and is never
resubmitted. Settlement finding another tuple or no row is known lost and may
clean only the exact attempted key; a direct same-tuple non-applied result is a
non-authorizing contradiction. Fresh authority additionally requires the strict
minted-only locator grammar before provisional-reference or metadata mutation.

**Resolution path:** canonical consumers use the value already resolved from
Cassandra. The `BlockStore` remains org-scoped at construction, and
`storage_key` must be non-empty, unmodified, and match either the legacy or
minted grammar for that logical block.

The locator-taking APIs (`PutObjectAutoDirect`, `ObjectExists`, the exact-key
`Get`/reader/size methods, and `DeleteBlockByStorageKey`) accept a caller-supplied
key, but they all pass through one `BlockStore` guard before backend access. The
guard requires the raw key to begin with this store's configured prefix plus its
canonical org ID and does not normalize or rewrite the key. This makes the tenant
boundary structural for exact-key operations instead of relying on each caller.

**Structural prerequisite for P2 (implemented 2026-08-24, PR #184).** Org isolation
is now enforced inside `BlockStore` for every exact-key GET/PUT/HEAD/DELETE, using
the configured prefix and canonical org ID, and a key equal to the bare tenant
prefix is refused because it names no object. Destructive locator validation is
centralized in `ValidatePhysicalLocator`, which checks a well-formed SHA-256 block
id, then tenant ownership, then the then-required equality — in that order, because `hashToKey`
returns `<prefix><org>/` plus the id for an id shorter than four characters, so a
degenerate id derives the very key the equality then compares it against. This
paragraph records the prerequisite at landing; P2 subsequently replaced the
equality with legacy-or-minted grammar and closed R9/R24. It did not close X1 or
authorize destructive GC; `GC_ENABLED=false` remains mandatory.

**P2 authority inventory (closed 2026-08-24).** All four sites now delegate to
`ValidatePhysicalLocator`; an AST guard rejects production calls to
`StorageKeyForHash` at authority sites and pins minting to the rowless branch:

| Site | Path |
|---|---|
| `storage.ValidatePhysicalLocator` | covers `gc.processBlock` and `gc.RecoverS3Orphans` |
| `ResolveNeedsPutBlockStore` | `internal/api/v2/upload_reuse.go` |
| `Reusable` branch of `StoreUploadedBlockForProbe` | `internal/api/v2/upload_reuse.go` |
| `newCanonicalBlockReader` | `internal/streaming/canonical_block_reader.go` |

P2 closes R9 and R24 only. Condemned-incarnation writer safety (P3), exact-`P`
destructive lifecycle (P4), X1, and destructive-GC activation remain open.
`GC_ENABLED=false` remains mandatory fleet-wide.

#### Related

- `docs/UPLOAD-PERFORMANCE-SECURITY-2026-06.md` — P-2 caveat 2
- `docs/TECHNICAL-DEBT.md` §25

---

### ISSUE-UPLOAD-CHUNK-MULTINODE-01: Chunked Upload Chunk State Is Node-Local

**Status**: 🔴 Open — multi-instance production blocker (B1)
**Severity**: High for multi-node topologies; no impact on single-node
**Affected**: `var chunkManager` in `internal/api/seafhttp.go`
**Source of record**: B1 / UP-1 in `docs/PROD-SECURITY-READINESS-20260724.md`

#### Problem

`chunkManager` is a process-global variable backed by
`map[string]*ChunkUpload` plus temp files in `os.TempDir()`. Upload state
is entirely node-local. If a load balancer routes chunks from the same upload
to different nodes, node B creates an empty tracker and finalization fails
(live-confirmed 2026-07-24: HTTP 200 `success:true` with no dirent).

Note: upload *tokens* are Cassandra-backed and multi-node safe
(`NewCassandraTokenAdapter`). The problem is chunk state only.

#### Mitigations

- **Immediate (zero server changes):** sticky sessions at the LB keyed on the
  upload token. The token is already available in Cassandra; the LB can use it
  as a consistent hash key.
- **Permanent:** distribute chunk state (Redis / Cassandra) or materialize blocks
  directly to S3 as chunks arrive, eliminating node-local staging entirely.

#### Related

- `docs/PROD-SECURITY-READINESS-20260724.md` — B1 / UP-1
- `docs/UPLOAD-PERFORMANCE-SECURITY-2026-06.md` — S-1
- `docs/TECHNICAL-DEBT.md` §25
- `ISSUE-SSO-PENDING-TOKEN-NODE-LOCAL-01` (the other node-local multi-instance blocker)

---

### ISSUE-LIBRARY-INITIAL-HEAD-CONCURRENCY-01: Concurrent initial-library HEAD initialization can race

**Status**: ✅ **Resolved 2026-09-11** (branch `fix/h1-conditional-head-initializer`) — both the original concurrent-initializer race and the multi-DC HEAD-reversion variant registered 2026-09-10 by the PC-0 audit. HEAD initialization now goes through the CAS domain; see *Resolution* below. Broader R31 remains open and is unrelated.
**Severity**: High (P1) — library initialization correctness; in multi-DC, HEAD monotonicity (resolved)
**Affected**: `SyncHandler.createInitialCommit` in `internal/api/sync.go` (reachable from `GET /seafhttp/repo/:repo_id/commit/HEAD`), `FSHelper.InitializeLibraryFS` in `internal/api/v2/fs_helpers.go` (group / org-admin / admin-extra library creation), initial `libraries`/`commits`/`fs_objects` writes
**Registered**: 2026-09-05, during the PR #203/#204 scope audit

#### Problem

The sync HEAD read can observe an empty `head_commit_id` in more than one concurrent request, and each request can then enter `createInitialCommit`. The initializer creates the deterministic empty root, derives the initial commit ID from `repoID`, `rootID`, and `time.Now().Unix()`, inserts the commit, and updates the canonical and lookup HEAD rows through an unconditional `LoggedBatch`. There is no conditional initializer claim or equivalent authority that selects one winner. Same-second requests can also derive the same initial commit ID, while requests in different seconds can create competing initial commits and let the last batch overwrite the visible HEAD.

This is separate from the abandoned PR #203 shared-root loser cleanup: that cleanup never exists in current `main` and remains a historical pitfall only. The issue concerns the preexisting initializer authority in the sync path.

#### Multi-DC HEAD-reversion variant (PC-0 audit, 2026-09-10)

`libraries.head_commit_id` has **six** production writers (pinned by
`TestPC0RawHeadColumnWritersAreInventoried`): two CAS primitives, two
creation-time `INSERT`s of a fresh partition, and two **unconditional
`UPDATE … SET head_commit_id` in a `LoggedBatch`** on an *existing* row —
`InitializeLibraryFS` (a separate batch after the `libraries` INSERT) and
`createInitialCommit`. `GetHeadCommit` reads `head_commit_id` at session
consistency (`LOCAL_QUORUM`) and, on `""`, calls `createInitialCommit`. So a
`GET` can move HEAD, and it does so outside the LWT domain:

```text
dc-na:  library row replicated everywhere with head=''
dc-na:  HEAD := C1 via the production CAS (IF head_commit_id='', SERIAL) — applied
dc-eu:  replica still holds head='' (lag / partition / hints pending)
dc-eu:  GET /commit/HEAD → LOCAL_QUORUM read '' → createInitialCommit
        → LoggedBatch UPDATE head=C0 (no IF)
every DC converges to C0 by timestamp; C1 (with files) is no longer HEAD
```

Reproduced on the real 3-DC fixture (`docker-compose.cassandra-3dc.yaml`,
hints off, dc-eu stopped during the CAS and restarted blind) with the exact
CQL shapes: `scripts/pc0-initial-head-xdc-probe.sh` ends with
`RESULT: HEAD REVERTED to C0-initial-empty` in all three DCs. The control leg
— the same initialization expressed as `IF head_commit_id = ''` from the
blind DC — is rejected and reports the real HEAD. `docs/TECHNICAL-DEBT.md`
§19.e's premise that these writes are safe because "the library has no
concurrent writers at first-touch" is therefore false in multi-DC; the
first-touch of a row is not the first-touch of the cluster.

#### Resolution (2026-09-11)

One conditional initializer, `FSHelper.InitializeLibraryHeadIfUnset`
(`internal/api/v2/fs_helpers.go`), is now the only way a library gets its
first HEAD:

```sql
UPDATE libraries SET head_commit_id = ?, root_commit_id = ?, size_bytes = 0, file_count = 0, updated_at = ?
WHERE org_id = ? AND library_id = ?
IF head_commit_id = null AND created_at != null
```

- `IF head_commit_id = null` alone **applies on a missing row and upserts a
  phantom library** (verified on Cassandra 5.0.9); `created_at != null`
  anchors the condition to an existing row, so a missing row is reported as
  `ErrLibraryHeadNotFound` instead of being created.
- The outcome is a tri-state, not a boolean: `InitialHeadApplied`,
  `InitialHeadAlreadyInitialized` (definitive `applied=false` with an
  existing head — the only demonstrated KNOWN_LOSER) and
  `InitialHeadUnknown` (ambiguous CAS whose SERIAL confirmation shows a head
  that is not ours: we may have won and been succeeded, so ownership of our
  commit is not attributable). The caller always adopts the canonical head;
  UNKNOWN never authorizes cleanup.
- UNKNOWN never discards anything. A demonstrated KNOWN_LOSER discards the
  commit row it inserted (`ShouldDiscardLosingInitialCommit` →
  `DiscardLosingInitialCommit`), and so does a definitive rejection
  (`ErrLibraryHeadNotFound` / `ErrLibraryHeadUninitializable`, inside
  `InitializeLibraryHeadIfUnset`) whose CAS demonstrably never published it;
  both only because initial commit ids are now attempt-unique
  (`InitialCommitID` mixes a UUID; the Sync path used to derive the id from
  the second, so two attempts could share it). The discard is **best
  effort**: a failed DELETE or a crash before it leaves a dangling empty-root
  commit (logged, no retry, no durable witness); its GC treatment is
  `ISSUE-GC-PHASE5-CASCADE-SHARED-FSOBJECTS-01`. It is not a guarantee.
- An UNINITIALIZED row in a shape this server never writes — an
  empty-string head, or a null head on a row whose `created_at` is null — is
  refused (`ErrLibraryHeadUninitializable`), not "repaired"; a missing row is
  `ErrLibraryHeadNotFound`. `created_at` only gates an uninitialized row: a
  row that already carries a non-empty HEAD is adopted
  (`InitialHeadAlreadyInitialized`) whatever `created_at` holds, because an
  existing HEAD must never be overwritten (unit-pinned in
  `TestClassifyInitialHeadCAS`, "existing head with null created_at is
  adopted"). The refused shapes are pinned against a real Cassandra
  (`TestInitializeLibraryHeadIfUnsetMissingRowIsNotFound`,
  `TestInitializeLibraryHeadIfUnsetRefusesInvalidRows`).
- Ambiguous CAS errors are settled by the same SERIAL confirmation read the
  HEAD-advance primitive uses (`resolveInitialHeadAmbiguity`); an
  unconfirmable outcome is `ErrLibraryHeadPublicationUnknown`.
- Adopting another writer's HEAD is fail-closed on **local servability**:
  `EnsureAdoptedHeadCommitVisible` requires the commit row behind the adopted
  HEAD to be readable in this datacenter at the session consistency
  `GET /commit/:id` uses (local read; on a miss an `EACH_QUORUM` read pulls
  the row through every datacenter and lets read repair populate the local
  replica; then a local re-read). Otherwise
  `ErrLibraryHeadCommitNotVisibleLocally` (retryable). A blind datacenter
  therefore never hands a client a HEAD whose commit it would 404.
- `InitializationErrorForbidsRollback` (UNKNOWN, or adopted-but-not-visible)
  stops the three creation paths (`CreateGroupOwnedLibrary`,
  `AddOrgGroupOwnedLibrary`, `AdminAddGroupOwnedLibrary`) from calling
  `rollbackNewLibrary`: a library whose HEAD may already be published is
  preserved as-is (durable, owned by the caller; the share step was not
  attempted) and the client gets a `500` that says so and carries the
  preserved `repo_id` plus `group_share: "not_attempted"`
  (`respondGroupLibraryCreationPreserved`). The same answer, for the same
  reason, when `createLibraryShare` returns an error after the HEAD was
  published — with `group_share: "unconfirmed"`, never "absent": the share
  is a `LoggedBatch` whose error does not prove it did not apply, so the
  share's existence is UNKNOWN and must be checked before sharing by hand.
  Past publication the handler cannot tell "our HEAD" from "adopted HEAD",
  so it has no cleanup authority. It is deliberately not a `503 Retry-After`:
  repeating the POST mints another library under the same name, it does not
  resume this one (`ISSUE-GROUP-LIBRARY-CREATION-RESUMABILITY-01`).
  Definitive pre-publication failures still roll back — but only after
  `rollbackNewLibrary` takes authority in the HEAD Paxos domain
  (`deleteUnpublishedLibraryRow`: `DELETE FROM libraries ... IF head_commit_id
  = null`, ambiguity settled by a SERIAL read). The creator's own failure says
  nothing about who else initialized the library: its id is discoverable as
  soon as the creation batch is durable, and `GET /commit/HEAD` from a client
  that just listed it publishes through the same conditional path. If a HEAD
  exists the rollback is refused (`ErrLibraryRollbackRefusedHeadPublished`)
  and the handler answers the preserved `500` instead; if it applies, no HEAD
  was ever published and none can be (the initializer's CAS finds no row).
  Pinned against a real Cassandra in
  `TestRollbackNewLibraryRefusesWhenAnotherInitializerPublishedHead` and by
  the `pc0ConsistencyPins` entry for `deleteUnpublishedLibraryRow`. The
  price of the split is a crash window between the authority LWT and the
  cleanup batch that can leave ghost projections behind a deleted canonical
  row — `ISSUE-LIBRARY-ROLLBACK-GHOST-PROJECTIONS-01`, closed by a durable
  discovery marker plus the same HEAD LWT on recovery.
- `InitializeLibraryFS` and Sync `createInitialCommit` insert the root
  fs_object and the commit row first (a winning HEAD never points at a
  missing commit), then publish through the primitive.
- `GET /seafhttp/repo/:id/commit/HEAD` still initializes an uninitialized
  library, but only through the conditional path, and it returns the HEAD
  the Paxos round settled on once that HEAD's commit is servable locally —
  a blind datacenter answers with the real HEAD instead of overwriting it,
  or with `503 Retry-After` while the commit is not yet visible. Other
  initialization failures are a 500, never an empty `head_commit_id`.

Evidence:

- Unit (`internal/api/v2`): `TestClassifyInitialHeadCAS`,
  `TestResolveInitialHeadAmbiguity` (ambiguity matrix),
  `TestAmbiguousInitialCASThatAppliedThenAdvancedRetainsCommit` (initial CAS
  C0 applies, response lost, HEAD advances C0→C1, confirm sees C1 ⇒ UNKNOWN,
  C0 retained), `TestShouldDiscardLosingInitialCommit`,
  `TestInitializationErrorForbidsRollback`, `TestInitialCommitIDIsAttemptUnique`.
- Integration, default stack: `TestSyncGetHeadCommitConcurrentInitializersConvergeOnOneHead`
  (16 concurrent `GET /commit/HEAD` on a null-head library converge on one
  HEAD, no dangling commits), `TestInitializeLibraryFSKeepsExistingHead`,
  `TestInitializeLibraryHeadIfUnsetMissingRowIsNotFound` (real Cassandra: no
  phantom row), `TestInitializeLibraryHeadIfUnsetRefusesInvalidRows`.
- `scripts/h1-initial-head-multidc-validation.sh` (real 3-DC,
  handler-level; gate `SESAMEFS_REQUIRE_H1_INITIAL_HEAD_MULTIDC_EVIDENCE=1`,
  `TestH1InitialHeadBlindDCDoesNotRevert3DC`): one full dc-eu
  stop/restart cycle per initializer, each on its own library, blindness
  (HEAD and commit row) asserted immediately before the initializer runs;
  each initializer driven from blind `dc-eu` keeps and returns the HEAD
  `dc-na` published, leaves exactly one commit row, and the commit behind
  the adopted HEAD is servable from `dc-eu` at the consistency
  `GET /commit/:id` uses. RED→GREEN: the same script run against the
  pre-fix production files from `main` fails at the sync leg with
  `createInitialCommit from blind dc-eu settled on <new commit>, want the
  canonical HEAD <C1>` — the reversion — and passes with the fix.
- `scripts/pc0-initial-head-xdc-probe.sh --expect-cas-fix` (CQL-shape level).
- Source contracts: `TestPC0RawHeadColumnWritersAreInventoried` now lists
  five `head_commit_id` writers (two CAS advances, one CAS initializer, two
  creation-time INSERTs); `TestPC0NoUnconditionalHeadUpdateRemains` fails on
  any reintroduced unconditional UPDATE; `TestPC0CriticalConsistencyPrimitivesArePinned`
  pins both load-bearing clauses (`IF head_commit_id = null`,
  `AND created_at != null`) independently. Mutation legs M10 (whole
  condition), M10a (null-head clause) and M10b (created_at clause) are RED.

Not changed by H1: APPLIED / KNOWN_LOSER / UNKNOWN classification. The HEAD
serial domain is now an explicit global `SERIAL` pin
(`ISSUE-LIBRARY-HEAD-SERIAL-DOMAIN-01`, closed 2026-09-14); a
`LOCAL_SERIAL` session default no longer applies to these HEAD LWTs. The
initializer also shares the CAS-then-derived-sync crash window of
`UpdateLibraryHead` (`TECHNICAL-DEBT.md` §19.f).

#### Second review round (2026-09-11): four runtime gaps found and closed

Three independent reviews of the resolution above (before it merged) found
four defects that survived the first round; all four are fixed on the same
branch and covered by new tests.

1. **The ambiguous-CAS classifier missed the real native-protocol-v4 CAS
   timeout shape.** `isAmbiguousLibraryHeadUpdateError` only matched
   `gocql.RequestErrCASWriteUnknown` (native protocol v5's
   `CAS_WRITE_UNKNOWN`, `errors.go` `ErrCodeCASWriteUnknown = 0x1700`),
   `gocql.ErrTimeoutNoResponse`, and `gocql.ErrConnectionClosed`. This server
   is pinned to protocol v4 by default (`config.Database.ProtoVersion`), and
   v4 has no `CAS_WRITE_UNKNOWN` code: an ambiguous CAS timeout there is a
   plain `RequestErrWriteTimeout` (or `RequestErrWriteFailure`) tagged
   `WriteType: "CAS"`. Unclassified, a real ambiguous CAS timeout would have
   been treated as a definitive failure, skipping the SERIAL confirmation and
   letting `InitializationErrorForbidsRollback` return `false` on a write
   that may have already applied — directly defeating this issue's own
   UNKNOWN-never-authorizes-destruction guarantee. Fixed by extending the
   classifier to match `*gocql.RequestErrWriteTimeout` /
   `*gocql.RequestErrWriteFailure` with `WriteType == "CAS"`. Covered by
   `TestIsAmbiguousLibraryHeadUpdateError` (both types, wrapped and
   unwrapped, plus non-CAS `WriteType` values that must stay unambiguous).
2. **The three creation handlers' `503 Retry-After` promised a resumption
   the server could not deliver.** `CreateGroupOwnedLibrary`,
   `AddOrgGroupOwnedLibrary`, and `AdminAddGroupOwnedLibrary` correctly
   stopped calling `rollbackNewLibrary` on a pending outcome, but answered
   `503 Retry-After` as if repeating the POST would finish that library; it
   minted a fresh `library_id` instead, so the preserved library sat durable
   with no group share while every retry created another one. A first fix
   introduced a `pending_group_library_creations` marker so a retry would
   resume the preserved library; a cross-audit of that marker (identity
   without `group_id`, no single-owner acquisition, ordinary-consistency
   lookup reopening "local miss ⇒ absent" across datacenters, unconditional
   release, 7-day TTL outliving the library, `MaxLibraries` TOCTOU, resumed
   attempts regaining rollback authority) showed it was a durable
   idempotency protocol for group-library creation — a different subsystem
   from making the first HEAD safe, and one whose every fix opened another
   finding. It was removed from this resolution. The three handlers now
   answer honestly: `500`, `preserved: true`, the preserved `repo_id` and
   name in the body, a WARNING log line with the ids for operators, and no
   `Retry-After` (`respondGroupLibraryCreationPreserved`). The library is
   visible in the owner's library list and can be shared with the group or
   deleted by hand. Durable, exactly-once resumption of the logical create is
   registered as `ISSUE-GROUP-LIBRARY-CREATION-RESUMABILITY-01` with the
   worked design (branch `feat/group-library-creation-claims`).
3. **`SettleAdoptedInitialHead` skipped the KNOWN_LOSER's best-effort cleanup
   when the adopted HEAD's visibility check failed first.** It ran
   `EnsureAdoptedHeadCommitVisible` before
   `ShouldDiscardLosingInitialCommit`/`DiscardLosingInitialCommit`, so a
   demonstrated KNOWN_LOSER's own attempt-unique commit — fully attributable
   regardless of whether the *winning* HEAD happens to be visible here yet —
   was never discarded on a retryable visibility failure, and every retry
   could leave another one dangling. Fixed by running the best-effort discard
   unconditionally first, then the visibility check; UNKNOWN still never
   discards. Covered by
   `TestSettleAdoptedInitialHeadDiscardsKnownLoserEvenWhenVisibilityCheckFails`.
4. **A definitive (non-ambiguous) CAS rejection left its own commit row
   behind.** `classifyInitialHeadCAS` returning `ErrLibraryHeadNotFound` or
   `ErrLibraryHeadUninitializable` — ownership of `commitID` is fully
   attributable to this attempt, since the CAS demonstrably did not apply for
   a reason unrelated to any other writer — was propagated by
   `InitializeLibraryHeadIfUnset` without discarding the commit row it had
   just inserted; since ids are now attempt-unique, repeated attempts against
   the same broken row (or a library deleted mid-attempt) each left another
   one. Fixed with a best-effort `DELETE` inside `InitializeLibraryHeadIfUnset`
   scoped to exactly those two sentinel errors (never UNKNOWN, never
   `InitialHeadAlreadyInitialized`, which already has its own discard path).
   Covered by extending
   `TestInitializeLibraryHeadIfUnsetMissingRowIsNotFound` and
   `TestInitializeLibraryHeadIfUnsetRefusesInvalidRows` (real Cassandra) to
   seed the attempt's commit row first and assert it is gone afterward.

Also corrected in the same round: `docs/PUBLICATION-PROTOCOL-CHARACTERIZATION.md`
§3.4/§19 wording that stated the KNOWN_LOSER commit discard and the "real,
usable HEAD" claim as unqualified guarantees (the discard is best effort; the
adopted-HEAD guarantee is that its `commits` row will not 404, not that its
tree is already locally present — see
`ISSUE-LIBRARY-HEAD-ADOPTED-TREE-VISIBILITY-01`), and stale
`TestPC0RawHeadColumnWritersAreInventoried`/`TestPC0AllHeadCallersAreInventoried`
comments in `internal/db/pc0_publication_inventory_contract_test.go` still
describing "two unconditional HEAD initializers" after this issue's
resolution replaced them with the one conditional initializer.

#### Third review round (2026-09-11): scope cut, then four residuals

The `pending_group_library_creations` marker from round 2 was removed (see
item 2 above and `ISSUE-GROUP-LIBRARY-CREATION-RESUMABILITY-01`). The
re-audit of the cut branch found one runtime defect and three contract
defects, all fixed here:

1. **The v5 `CAS_WRITE_UNKNOWN` shape was never matched either.** The
   classifier's `errors.As` target for `RequestErrCASWriteUnknown` was a
   *value* (inherited from `main`), but the driver decodes that error as
   `*RequestErrCASWriteUnknown` (`frame.go`), so the match was dead code and
   the unit tests, built from value literals, passed without exercising the
   real shape. Under protocol v5 (permitted by configuration) an applied-but-
   unacknowledged initial CAS would have been a definite failure and a
   creation handler could have rolled the library back. Fixed with a pointer
   target; `TestIsAmbiguousLibraryHeadUpdateError` and the
   `resolveLibraryHeadUpdateError` tests now use the driver's pointer form.
2. **A `createLibraryShare` error was reported as "no group share".** The
   share is a `LoggedBatch`; its error does not prove it did not apply.
   `respondGroupLibraryCreationPreserved` now reports `group_share` as
   `not_attempted` (HEAD ended UNKNOWN before the share step) or
   `unconfirmed` (share write errored), never "absent".
3. **`scripts/h1-initial-head-multidc-validation.sh` was committed `100644`.**
   Now `100755` like the probe it pairs with.
4. **Contract wording.** "No other writer knows the id" (a fresh library is
   discoverable once its creation batch is durable; the claim is now "rare in
   the normal workflow, and a concurrent initializer is what H1 survives"),
   and `created_at = null` being described as unconditionally refused (it
   only gates an uninitialized row; a non-empty HEAD is adopted regardless —
   pinned in `TestClassifyInitialHeadCAS`).

#### Fourth review round (2026-09-11): rollback authority, and two contract residuals

1. **A creator's pre-CAS failure could still destroy a HEAD another
   initializer had published.** `InitializeLibraryFS` now writes the root
   fs_object and the attempt's commit in a batch *before* the CAS; if that
   batch failed, the error was (correctly) classified as definitive for this
   attempt, and the three creation handlers called `rollbackNewLibrary`,
   which deleted `libraries`, `libraries_by_id`, all `fs_objects` and all
   `commits` for the library unconditionally. But "this attempt did not
   publish" is not "nobody published": the library id is discoverable once
   the creation batch is durable, and a client that listed it can reach
   `GET /commit/HEAD` → `createInitialCommit` → CAS null→C1 before the
   creator's batch error returns — after which the creator's rollback erased
   the canonical row, C1 and its root. Pre-existing in shape (`main` also
   rolled back on any initialization error) but it falsified the guarantee
   this issue declares closed. Fixed inside the helper, not the callers:
   `rollbackNewLibrary` first runs `deleteUnpublishedLibraryRow`
   (`DELETE ... IF head_commit_id = null`, SERIAL-confirmed on ambiguity);
   only an applied delete — no HEAD ever published, none possible afterwards
   — proceeds to the derived rows, fs_objects and commits. Otherwise
   `ErrLibraryRollbackRefusedHeadPublished`, which the handlers map to the
   preserved `500` (`group_share: "not_attempted"`). Regression:
   `TestRollbackNewLibraryRefusesWhenAnotherInitializerPublishedHead` (real
   Cassandra: B publishes through the production initializer, A's rollback is
   refused, B's library/HEAD/commit/root survive), plus the applies and
   missing-row cases; the guard's clause is a `pc0ConsistencyPins` entry.
2. **Three trackers still called the preserved library "unshared".** The
   share state can be `unconfirmed` (share batch errored but may have
   applied); severity line here, `OPEN-WORK-INDEX`, and PC-0 §15 reworded.
3. **`ISSUE-LIBRARY-HEAD-SERIAL-DOMAIN-01` listed two HEAD LWTs; H1 added a
   third** (and this round a fourth conditional statement, the rollback
   guard), all inheriting the configurable `serial_consistency`. The issue
   now lists them. Closed 2026-09-14: those four statements pin global
   `SERIAL` explicitly; `LOCAL_SERIAL` remains valid for other LWTs.

#### Fifth review round (2026-09-11): debt registered, contract wording

1. **Round 4's two-phase rollback opened a crash window** between the
   authority LWT and the cleanup batch (ghost active projections behind a
   deleted canonical row; no reconciler covers it — the claim that "the
   admin trash/stale-projection sweeps" would prune it was wrong:
   `ReconcileDeletedAdminLibraryRowsByOrg` walks trash rows only and
   `SyncAdminLibraryReadModel` returns nil on a missing canonical row).
   Deliberately **not** fixed in this PR: registered as
   `ISSUE-LIBRARY-ROLLBACK-GHOST-PROJECTIONS-01` with the effects, the two
   candidate seams and the required fault-injection test.
2. **"Only a KNOWN_LOSER may discard its commit" was false since round 2**:
   a definitive rejection (`ErrLibraryHeadNotFound` /
   `ErrLibraryHeadUninitializable`) also discards the attempt's commit, inside
   `InitializeLibraryHeadIfUnset`, because the CAS demonstrably never
   published it. Reworded on every surface (code comments, this issue,
   CHANGELOG, CURRENT_WORK, PC-0 §3.4, PR body);
   `ShouldDiscardLosingInitialCommit` is now described as the only cleanup
   predicate for a *competing-head* outcome.

#### Scope / disposition (historical)

Tracked separately from W2, R31 repair, and the library HEAD serial-domain issue. It did not affect the narrow SessionUpload own-liveness/exact-placement guarantee, and W2 did not add lifecycle or initial-library changes. The fix was the separate, prioritized follow-up the PC-0 audit asked for and a `PublicationCoordinator` prerequisite (`docs/PUBLICATION-PROTOCOL-CHARACTERIZATION.md` §3.4, §6 PUBL-9, §7, M9).

#### Related

- `ISSUE-LIBRARY-HEAD-SERIAL-DOMAIN-01` — HEAD CAS serial-domain contract (closed 2026-09-14)
- `internal/api/v2/fs_helpers.go:InitializeLibraryFS` — v2 initialization path (had the same unconditional shape; now publishes through `InitializeLibraryHeadIfUnset`)
- `docs/PUBLICATION-PROTOCOL-CHARACTERIZATION.md` §3.4 — writer inventory and multi-DC reproduction
- `scripts/pc0-initial-head-xdc-probe.sh` — 3-DC reproduction of the old shape (bug mode) and acceptance of the new shape (`--expect-cas-fix`)
- `scripts/h1-initial-head-multidc-validation.sh` — 3-DC handler-level evidence
- `docs/PR203-SCOPE-AUDIT.md` — historical classification and disposition

---

### ISSUE-LIBRARY-ROLLBACK-GHOST-PROJECTIONS-01: A Crash Between the Rollback Authority LWT and Its Cleanup Batch Leaves Ghost Active Projections

**Status**: ✅ **Resolved 2026-09-11** (branch `fix/library-rollback-ghost-projections`) — durable `library_rollback_pending` marker + bounded fair reaper; the HEAD LWT remains the only cleanup authority
**Severity**: Medium (operational: a ghost library in active read models that counts against `MaxLibraries`, holds its name and shows in admin listings; no HEAD/data safety impact)
**Affected**: `rollbackNewLibrary` / `deleteUnpublishedLibraryRow` (`internal/api/v2/write_helpers.go`, `internal/api/v2/library_rollback.go`), called by `CreateGroupOwnedLibrary`, `AddOrgGroupOwnedLibrary`, `AdminAddGroupOwnedLibrary` on a definitive initialization failure
**Registered**: 2026-09-11, H1 review round 5 (introduced by round 4 of `ISSUE-LIBRARY-INITIAL-HEAD-CONCURRENCY-01`'s resolution)

#### Problem

Round 4 made `rollbackNewLibrary` take authority before destroying anything:

```text
1. DELETE FROM libraries WHERE org_id = ? AND library_id = ?
   IF head_commit_id = null                              ← LWT, HEAD domain
2. LoggedBatch:
   DELETE policies, admin/owner/org/global projections,
          libraries_by_id, fs_objects, commits
```

That closed a real hole (a creator's own failure could erase a HEAD another
initializer had published on the already-discoverable id). Before round 4
every delete, canonical row included, was one `LoggedBatch`: a failure left
an orphan library that was at least *consistent*. Now there is a window:

```text
step 1 applied → canonical libraries row gone
process killed / step 2 fails
→ libraries_by_id                       still present
→ libraries_by_owner                    still present (active, deleted_at null)
→ libraries_by_org_updated              still present
→ libraries_admin_global_by_updated     still present
→ gc library policies, fs_objects, commits   may still be present
```

Nothing reconciled that state before this issue's resolution:

- `ReconcileDeletedAdminLibraryRowsByOrg` walks `libraries_deleted_by_org`
  (trash rows) only.
- `SyncAdminLibraryReadModel` returns `nil` when the canonical row is
  missing; it never deletes stale active projections.
- The GC library cascade (`processLibraryCascade`) keys on the
  `deleted_libraries` marker, which a rollback never writes.
- `rollbackNewLibrary` itself is idempotent over a missing canonical row
  (`TestRollbackNewLibraryOnMissingCanonicalRowStillClearsDerivedRows`), but
  nothing durable made it run again until the recovery seam below.

Effects: `CountActiveLibraries` (reads `libraries_by_org_updated`) keeps
counting the ghost against `MaxLibraries`; `ownerHasActiveLibraryNamed`
(reads `libraries_by_owner`) keeps the name occupied; admin listings and
search consume the projections filtering only on `deleted_at` and show a
library whose canonical row does not exist. Manual cleanup: delete the
projection rows for the `library_id` (the WARNING log line from the failed
rollback carries org and library ids).

Exposure: only a creation whose initialization failed definitively (pre-CAS
batch error, definitive CAS rejection) *and then* crashed or failed again
inside its rollback — two failures in one cold path.

#### Direction (implemented 2026-09-11)

Do not remove the LWT; the authority gate is required. Two candidate seams
were considered. This PR implemented **durable marker + bounded idempotent
reaper**. Conditional soft-delete was rejected here because
`InitializeLibraryHeadIfUnset` (`IF head_commit_id = null AND created_at !=
null`) can still apply on a row with `deleted_at != null`.

#### Resolution (2026-09-11)

`rollbackNewLibrary` now:

1. Persists `library_rollback_pending` (fixed `recovery_bucket`, identity
   `(org_id, library_id)`, snapshot of `owner_id` + `created_at` for exact
   read-model deletes). No TTL. Marker write failure **skips** the authority
   DELETE.
2. Takes authority with the same `deleteUnpublishedLibraryRow`
   (`DELETE FROM libraries ... IF head_commit_id = null`).
3. Runs the existing derived cleanup idempotently.
4. Deletes the marker only after cleanup succeeds.

A Server-owned reaper (`RecoverPendingLibraryRollbacks`, independent of
`GC_ENABLED`) enumerates pending rows from Cassandra and **re-enters the
same LWT**. Finding a marker is not permission to destroy derived state.
Sweeps stay bounded (256). Progress is a clustering cursor per bucket plus a
rotating start bucket: durable ≠ eventually processed unless failed rows
cannot pin the next sweep to the same prefix.

- Marker = durable recovery/discovery
- HEAD LWT = cleanup authority

Evidence: `TestRollbackNewLibraryCrashAfterAuthorityRecoveredByReaper`
(authority applied → crash before cleanup → ghosts remain → recovery seam
clears them), plus refuse-on-HEAD, missing-canonical, cleanup-failure,
marker-delete-failure, concurrent reaper, and marker-persist-failure tests
in `internal/api/v2/` (`-tags integration -run RollbackNewLibrary`).
Fairness: `TestRecoverPendingLibraryRollbacksDoesNotStarveLaterMarkerInSameBucket`
and `TestRecoverPendingLibraryRollbacksDoesNotStarveLaterBucket`. Schema
guards pin migration 023, **effective** `default_time_to_live = 0` after the
full migration chain (a later `ALTER TABLE` cannot sneak in a TTL), and one
row per library. PC-0 pins keep `IF head_commit_id = null` and that recovery
calls `deleteUnpublishedLibraryRow`.

Not changed: `InitializeLibraryHeadIfUnset`, group-library creation
resumability, HEAD serial domain, GC cascade / S3 delete.

#### Related

- `ISSUE-LIBRARY-INITIAL-HEAD-CONCURRENCY-01` — round 4 (the authority gate
  this is the price of)
- `TestRollbackNewLibraryOnMissingCanonicalRowStillClearsDerivedRows` — the
  idempotent re-run the reaper relies on
- `TestRollbackNewLibraryCrashAfterAuthorityRecoveredByReaper` — authority
  applied, crash, recovery seam

---

### ISSUE-GROUP-LIBRARY-CREATION-RESUMABILITY-01: A Preserved Group-Library Creation Cannot Be Resumed by Retrying the POST

**Status**: 🟡 Open — follow-up, not a blocker of `ISSUE-LIBRARY-INITIAL-HEAD-CONCURRENCY-01`
**Severity**: Medium (UX/operational: one preserved library per affected attempt whose group-share state is `not_attempted` or `unconfirmed`; no H1 safety impact)
**Affected**: `CreateGroupOwnedLibrary` (`internal/api/v2/groups.go`), `AddOrgGroupOwnedLibrary` (`internal/api/v2/org_admin_groups.go`), `AdminAddGroupOwnedLibrary` (`internal/api/v2/admin_extra.go`)
**Registered**: 2026-09-11, splitting the `pending_group_library_creations` marker out of `ISSUE-LIBRARY-INITIAL-HEAD-CONCURRENCY-01`'s resolution

#### Problem

Group-owned library creation is three steps: mint the library rows, publish
the first HEAD, create the group share. Since H1 the middle step can end
UNKNOWN (ambiguous CAS that the SERIAL confirmation could not settle, or an
adopted HEAD whose commit is not yet locally visible) and, once the HEAD is
published, a share failure leaves a library nobody has cleanup authority
over. In both cases the handler preserves the library and answers `500` with
`preserved: true`, the `repo_id`, and `group_share` set to `not_attempted`
(HEAD ended UNKNOWN before the share step) or `unconfirmed` (the share batch
returned an error, which does not prove it did not apply)
(`respondGroupLibraryCreationPreserved`). That library is durable, owned by
the caller, visible in the owner's library list, and counts against
`MaxLibraries`. Repeating the POST mints another library under the same
name; nothing resumes the first.

In the normal workflow this is rare: the library id is minted by the
request and only becomes discoverable (owner library list, admin
projections) once the creation batch is durable, a window of one
initializer call before the HEAD publish — so an UNKNOWN there is in practice
an ambiguous CAS timeout, and a concurrent initializer racing through that
window (e.g. a client that listed the new repo and hit `GET /commit/HEAD`)
is exactly the case H1's conditional initializer is built to survive. Rare
enough that the honest answer was preferred over shipping the protocol
below inside H1; not "impossible".

#### Direction (design worked, not merged)

Branch `feat/group-library-creation-claims` carries a worked design and its
unit tests. The shape the cross-audit converged on: one durable claim row
per logical create keyed by `(org, owner, group, name)`, acquired with
`INSERT ... IF NOT EXISTS` after the library rows are written (Paxos decides
one owner for concurrent same-operation requests; a blind datacenter observes
an existing claim through the serial domain instead of a local-quorum miss),
released only with `DELETE ... IF library_id = ?`, fixing `share_id` and
`created_at` so a resumed share upserts the same projection rows
(`shares_by_group` clusters on `created_at`), no TTL, stale claims (library
gone or trashed) released by the next same-operation request, the admission
gate applied only to the minting path, an explicit storage-class mismatch
refused with `409` while a defaulted class follows the preserved library (in
flexible residency the default depends on the routing hostname), and a
resumed attempt never rolling back on any later failure. Its audit already
lists the evidence it must ship with: different-group/same-name must not
resume, concurrent same-operation creates mint one library, claim in `dc-na`
then retry from a blind `dc-eu` resumes the same library, attempt A cannot
release attempt B's claim, share-then-crash retry does not re-share,
preserved+resume+initializer/share failure does not destroy, quota
fresh/resumed decision made once, claim lifetime bounded by the library's.

Note the behavioral change it implies and that H1 deliberately does not
make: concurrent same-name creates by the same owner for the same group
collapse into one library instead of N.

Also in this follow-up's scope, pre-existing in `main`: the creation batch
itself is not request-idempotent. A `LoggedBatch` that applied but whose
response was lost gives the client an error before H1 even runs, and the
client's retry mints another id/library. The single-owner claim design
above covers it (the claim is acquired after, and points at, the rows the
batch wrote).

#### Related

- `ISSUE-LIBRARY-INITIAL-HEAD-CONCURRENCY-01` — the resolution whose
  preserved-outcome path this follows up
- `respondGroupLibraryCreationPreserved` (`internal/api/v2/groups.go`) — the
  interim honest answer

---

### ISSUE-LIBRARY-HEAD-ADOPTED-TREE-VISIBILITY-01: Adopting a Blind-DC HEAD Proves Only Its Commit Row, Not the Tree Behind It

**Status**: 🟡 Open — follow-up, not a blocker of `ISSUE-LIBRARY-INITIAL-HEAD-CONCURRENCY-01`
**Severity**: Medium/High (P1 as a general cross-DC metadata-visibility gap; not H1-specific and pre-existing)
**Affected**: `FSHelper.EnsureAdoptedHeadCommitVisible` (`internal/api/v2/fs_helpers.go`), any code path that serves a repo's tree right after adopting a HEAD published in another datacenter
**Registered**: 2026-09-11, reviewing `ISSUE-LIBRARY-INITIAL-HEAD-CONCURRENCY-01`'s resolution

#### Problem

`EnsureAdoptedHeadCommitVisible` fail-closes on exactly one thing: the adopted
HEAD's own `commits` row is readable in this datacenter (local read, then
`EACH_QUORUM` to pull it through read repair, then a local re-read). That is
precisely the H1 contract — `GET /commit/HEAD` never hands back a HEAD whose
commit 404s here — and the 3-DC evidence
(`scripts/h1-initial-head-multidc-validation.sh`) genuinely proves it.

It does not prove the *tree* behind that commit — `root_fs_id` and every
`fs_objects` row it (transitively) references — is locally present, nor the
commit's *ancestry* (parent `commits` rows a client walking history, or a
repair classifier walking parents, would read next). The 3-DC
test cannot currently show this gap because every initializer's HEAD is the
same deterministic empty-root commit: the losing initializer on the blind DC
inserts that identical content-addressed empty root itself before losing the
CAS, so the root is already local regardless of what the visibility check
does. A non-empty remote HEAD (files already written on the winning DC before
the blind DC catches up) is not exercised:

```text
dc-na: commit C1 with root R1 (non-empty) fully written, HEAD := C1
dc-eu: was down for the whole window; comes back blind

dc-eu: CAS discovers C1, EnsureAdoptedHeadCommitVisible repairs only
       commits(C1) via EACH_QUORUM
dc-eu: GET /commit/HEAD → C1; GET /commit/C1 → 200 (repaired)
dc-eu: commit says root_fs_id = R1
dc-eu: GET /fs/R1 (or a file read under it) → still 404: R1 was never
       repaired, only the commit row was
```

This is a narrower instance of the general cross-DC metadata-visibility
problem this codebase already tracks elsewhere (fs_objects/tree rows lagging
behind a HEAD that has already converged) — not something
`ISSUE-LIBRARY-INITIAL-HEAD-CONCURRENCY-01` introduced or was ever scoped to
close. Expanding the initial-HEAD fix to repair the whole tree before
answering would have turned a narrowly-scoped concurrency/monotonicity fix
into a full metadata-repair project; `docs/PUBLICATION-PROTOCOL-CHARACTERIZATION.md`
§3.4 now says explicitly that the initializer's guarantee is "this HEAD's
commit row will not 404 here," not "this repo is fully servable here."

#### Direction (not yet implemented)

Not prescribed. Two shapes worth weighing when this is picked up:
narrow (extend `EnsureAdoptedHeadCommitVisible` to also confirm the root
`fs_objects` row, leaving deeper subtree lag as a still-separate,
already-tracked problem) versus broad (a general adopted-HEAD tree repair
shared with the rest of the cross-DC metadata-visibility work). Needs its own
audit before either is chosen.

#### Related

- `ISSUE-LIBRARY-INITIAL-HEAD-CONCURRENCY-01` — the initializer whose
  adopted-HEAD guarantee this narrows
- `docs/PUBLICATION-PROTOCOL-CHARACTERIZATION.md` §3.4 — corrected wording

---

### ISSUE-LIBRARY-HEAD-SERIAL-DOMAIN-01: Library HEAD Publish Has No Explicit Serial-Domain Contract

**Status**: ✅ Fixed 2026-09-14
**Severity**: Medium (correctness under a supported configuration); no impact on the shipped `SERIAL` default or on single-DC
**Affected**: `updateLibraryHeadWithStats` in `internal/api/sync.go`, `UpdateLibraryHead` and `InitializeLibraryHeadIfUnset` in `internal/api/v2/fs_helpers.go`; the creation-rollback guard `deleteUnpublishedLibraryRow` in `internal/api/v2/write_helpers.go`
**Registered**: 2026-08-24, as the deliberate out-of-scope boundary of P0/R12

#### Problem

The four conditional library-HEAD LWTs (two advances, the initializer, and the
creation-rollback DELETE) are lightweight transactions. None called
`SerialConsistency(...)`, so they took the Paxos phase from
`cluster.SerialConsistency` (`database.serial_consistency` /
`CASSANDRA_SERIAL_CONSISTENCY`). The shipped production default is `SERIAL`, but
`LOCAL_SERIAL` is a supported value and the cluster test profiles use it.

Under `LOCAL_SERIAL` with multi-region replication, the compare-and-set is
serialized within one DC only. Two DCs could each apply an advancement from the
same parent.

#### Why it was not in P0/R12

R12 covers the canonical block/orphan lifecycle tables. Library HEAD is a
separate invariant. Pinning it was registered rather than silently folded into
R12.

#### Fix

Every competing HEAD-authority LWT now calls
`SerialConsistency(db.LibraryHeadSerialConsistency)` with
`LibraryHeadSerialConsistency = gocql.Serial`, never derived from config.
`serial_consistency` may still control other LWTs. PC-0 inventories competing
HEAD mutations from production `Query`/`Bind` CQL (cas writers and the DELETE
IF guard) and chain-pins the MapScanCAS serial
domain (`TestPC0HeadSerialDomainPinsGlobalSerial`). Mutation gate
`scripts/library-head-serial-domain-mutation-validation.sh` (M1–M28) goes RED
if any seam is downgraded to `LOCAL_SERIAL`, if an explicit pin is removed, if
the constant itself becomes `LOCAL_SERIAL`, if a new DELETE IF names
`head_commit_id` after another predicate or uses `sesamefs.libraries`, if
a HEAD DELETE is issued through unresolvable CQL (`Query(fmt.Sprintf(...))`),
if a package-level `var fn = func` hides a DELETE IF, if the allowlisted
`UpdateLibrary` dynamic Query becomes a HEAD LWT, if a SET fragment or lock
`fmt.Sprintf` format inside an allowlisted caller is not source-resolvable,
if `UpdateLibrary`'s `updates` initializer preloads `head_commit_id`, if
`&updates` is passed to a helper, if the SET loop ranges something other
than `updates`, if `poison(&query)` appends a HEAD IF, if `update` is
assigned inside the SET loop, if `Migrator.apply` reassigns `stmt` via
`strings.Join`, if a lock helper format becomes
`DELETE FROM libraries ... IF EXISTS`, if `poison(&stmt)` keeps a stale
CQL binding, if a second `SerialConsistency(localSerial)` last-write-wins, if a
second HEAD `Query.Exec()` hides inside an inventoried writer, if a `range`
rebind leaves a stale CQL binding, if an
embedded `migrations/*.cql` statement competes for HEAD, if a
migration writes `head_commit_id` / whole-row-deletes `libraries` under
`IF EXISTS` without naming the column in IF, or if a concat
`UPDATE libraries SET` + `head_commit_id` Query appears outside the
inventoried CAS writers.
Real 3-DC evidence
(`scripts/library-head-serial-domain-multidc-validation.sh`) is self-managed
(not `./scripts/test.sh api`): it opens sessions
with default `LOCAL_SERIAL` and requires exactly one winner for concurrent
advance and concurrent initial HEAD.

Claim: all current writers and guards that compete for canonical
`libraries.head_commit_id` authority participate in one global `SERIAL` Paxos
domain, independent of the Cassandra session serial-consistency default. This
satisfies the PC-D1 global SERIAL prerequisite. It does not implement the
certified baseline frontier, close PC-2/W2/R31/X1, or activate GC.

#### Related

- `docs/DATABASE-GUIDE.md` — Phase 5 consistency table
- `ISSUE-SYNC-HEAD-RECOVERY-01` — the conflict-recovery design this LWT backs
- `docs/PC-D1-INHERITED-DEPENDENCY-CONTINUITY.md` — global SERIAL prerequisite
- `docs/CHANGELOG.md` — 2026-08-23 P0/R12 entry

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

Observed in SeaDrive 3.0.19 client logs after successful SSO login and basic sync. Sync works despite these errors — they degrade UX or efficiency but are non-fatal. SD-01 is fixed (2026-07-02, see below — earlier "not a gap" framing from the same day was itself wrong, corrected after live-testing a real server). SD-02 is confirmed to 404 the same way against a real server when notifications are disabled — not a gap. SD-05 (nginx false-positive notification detection) is fixed.

---

### ISSUE-SD-01: `POST /seafhttp/repo/locked-files` — File Lock Status — FIXED (2026-07-02)

**Observed**: SeaDrive/desktop client logs `Bad response code for GET .../seafhttp/repo/locked-files: 404`
**When**: Immediately after repo trees are loaded, before first sync cycle

**False start, then correction, both same day (2026-07-02)**: First pass cloned the public `haiwen/seafile-server` (`master`, Community Edition) and found no `/repo/locked-files` route in the Go fileserver's route table — concluded this was upstream parity, not a gap, and decided not to implement it. That was wrong. Live-tested against `app.nihaoshares.com`, a genuine company-operated **Seafile Pro 11.0.16** instance (confirmed via `/api2/server-info/` reporting `"features": ["seafile-basic", "seafile-pro", "file-search"]`):

```
GET  /seafhttp/repo/locked-files                    → 400, body "EOF"   (empty-body JSON decode error — the handler IS real)
POST /seafhttp/repo/locked-files  body: []           → 200, body []
POST /seafhttp/repo/locked-files  body: [{repo_id,token,ts}] (nonexistent repo) → 200, body []
```

The `"EOF"` body is the literal string Go's `json.Decoder.Decode()` returns on an empty body — proof the route is real and JSON-body-driven, not a blanket 404. Locked-files (and folder-perm, see ISSUE-SD-06) are Seafile **Pro/Enterprise-only** features (closed-source), which is why they're absent from the public CE repo but present against a real Pro server. Since our own `/api2/server-info/` already advertises `"seafile-pro"` in its `features` array, we were already telling clients we support this tier without implementing two of its defining sync endpoints.

**Confirmed wire format**: POST-only (GET 400s on the real server too — the client's error-log message says "GET" but the actual call, per `daemon/http-tx-mgr.c` `get_locked_files_thread`, is always `http_post`). Body is a JSON array of `{repo_id, token, ts}`; response is a JSON array where **repos with no active locks are omitted entirely** (confirmed: even a garbage/nonexistent repo_id returns `[]`, not an entry with an empty `locked_files` list).

**Fix**: Implemented `POST /seafhttp/repo/locked-files` in `internal/api/sync.go` (`GetLockedFiles`), backed by the real `locked_files` table via `db.ListRepoLocks` (`internal/db/file_locks.go`).

**Security hardening (same day, post-review)**: the first cut returned real lock paths for any posted `repo_id` without validating the per-repo `token` in the body — an information-disclosure hole (lock paths leak file/folder names, and repo UUIDs appear in URLs/logs/share flows so they must not be treated as secrets). Now every body entry is authenticated: the entry's `token` must resolve via the token store (`TokenTypeDownload`) **and** belong to that same `repo_id`, or the entry is silently omitted — indistinguishable from "no locks", so the endpoint never confirms whether a guessed repo_id exists. The token's user also enables a real `by_me` (lock holder == token's user, case-insensitive), instead of hardcoded `false` which could have made SeaDrive show a user's own locks as foreign. Also added: 500-entry cap, per-request `repo_id` dedupe, `ShouldBindJSON` (avoid Gin's auto-abort double-write), and fail-closed behavior when no token validator is wired. Note the endpoint is registered without route middleware only because it's multi-repo (no `:repo_id` in path) — it is NOT unauthenticated.

**Second hardening round (same day)**: `TokenTypeDownload` is shared by three grant shapes — the repo-level sync token from download-info (`Path=="/"`, `Source==""`), path-scoped file-download tokens (e.g. `/docs/report.docx`), and share-link tokens (`Source=="link"`, created by `CreateLinkDownloadToken`). Only the first may enumerate repo locks; the handler now additionally requires `Path == "/" && Source != "link"` so a share-link recipient or a single-file download token cannot widen into repo-wide lock visibility (verified `Source` survives the `CassandraTokenAdapter.GetToken` conversion, so the check holds against the production store). Also: `http.MaxBytesReader` (256 KiB) before JSON decoding on this middleware-less route; dedupe moved **after** token validation so a duplicate entry with a stale token can't shadow a later valid one; nil-guard on the validator's returned token.

**Additional hardening (same day, follow-up review)**: the body-token path now also reuses the same account/org usability check that `syncAuthMiddleware` applies to repo-token requests, so a deactivated user with an old token cannot keep enumerating lock metadata. And lock-table lookup failures no longer degrade to "no locks" — the handler now fails closed with `503 file lock status unavailable` instead of pretending the repo is unlocked.

**Residual limitation**: whether genuine Seafile Pro validates these tokens is still unverified (Pro is closed-source; our black-box probes with nonexistent repos can't distinguish invalid-token from no-locks responses) — we validate regardless, since the client always sends real tokens and failing closed costs nothing.

**Files**: `internal/api/sync.go`, `internal/db/file_locks.go`, `internal/api/sync_locked_files_test.go`, `internal/db/file_locks_test.go`

---

### ISSUE-SD-06: `POST /seafhttp/repo/folder-perm` Response Was `{}` Instead of `[]` — FIXED (2026-07-02)

**Discovered** while live-testing ISSUE-SD-01 against `app.nihaoshares.com` (Seafile Pro 11.0.16): `folder-perm` uses the **same array-based wire format** as `locked-files`, not the `{}` object our handler had returned since 2026-02-19:

```
GET  /seafhttp/repo/folder-perm                     → 400, body "EOF"
POST /seafhttp/repo/folder-perm  body: [{repo_id,token,ts}] → 200, body []
```

**Impact**: Low in practice — SeaDrive never logged an error against our `{}` response, so it evidently tolerates the wrong shape (or never got far enough to strictly parse it). But it wasn't protocol-correct, and since we don't implement folder-level permissions at all yet, the honest answer for every request is "no restrictions anywhere," which the real server expresses as an empty array.

**Fix**: `GetFolderPerm` now returns `[]` instead of `{}` for both GET and POST. Kept as a single stub answer (empty array unconditionally) — we have no folder-permission data source to look up yet.

**Files**: `internal/api/sync.go`, `internal/api/sync_locked_files_test.go`

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

### ISSUE-SD-05: `frontend/nginx.conf` SPA Catch-All Faked a "Notification Server Enabled" Response — FIXED (2026-07-02)

**Observed**: Client logs showed `Notification server is enabled on the remote server http://localhost:3000.` immediately followed by a 404 on `GET .../seafhttp/repo/:id/jwt-token`.

**Root cause**: The desktop client autodetects the notification (WebSocket) server by calling `GET <server>/notification/ping` and treats **any HTTP 200** as "alive" — it never inspects the body (confirmed against `daemon/http-tx-mgr.c` `check_notif_server_thread`, upstream). Our deployment terminates the client's base URL at `frontend/nginx.conf`, whose SPA catch-all (`location / { try_files $uri $uri/ /index.html; }`) returned `200 index.html` for `/notification/ping` since no dedicated location existed for it. The client then believed notifications were enabled and started requesting `jwt-token`, which correctly 404s per ISSUE-SD-02 (we don't run a notification server) — but the client had already been misled into thinking it should expect JWTs, producing a confusing log sequence even though nothing was actually broken.

**Fix**: Added a dedicated `location /notification/ { return 404; }` block in `frontend/nginx.conf`, placed before the SPA catch-all. The client now correctly detects "notifications disabled" up front from the `/ping` call itself and never requests `jwt-token`, so the log noise disappears entirely. Sync behavior is unaffected either way (notifications were never actually used) — this only cleans up the client-side log/detection path.

**Files**: `frontend/nginx.conf`

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
**Fix**: Register both GET and POST as static routes (no auth middleware) before the wildcard group. Response was `{}` — no auth is needed either way. **Update (2026-07-02)**: the response shape itself was wrong; see ISSUE-SD-06 — real wire format is an empty array `[]`, not `{}`.
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

### ISSUE-GC-LOGICAL-MAPPING-RETENTION-01: Forward SHA-1 mappings have no logical-death reaper

**Status**: 🟡 Open — retention debt; not a destructive-GC safety blocker
**Discovered**: 2026-08-17
**Priority**: Low/Medium — storage growth and stale lookup metadata
**Affected**: `block_id_mappings`, SHA-1 block lookup, future logical cleanup

R11a correctly decouples the logical SHA-1 → SHA-256 mapping from the physical block
lifecycle, so physical GC must not delete this row. The tradeoff is that mappings have
no TTL and no logical-death reaper. A row can therefore resolve a SHA-1 to an internal
block ID whose canonical `blocks` row and S3 bytes are gone; the lookup then returns
404 until the same content is materialized again.

This is bounded by distinct logical content, not delete churn: the mapping writer is
idempotent for an existing `(org_id, representation_id, external_id)` key. It remains
an unbounded retention cost over the lifetime of a tenant, however. A future reaper
must define logical ownership, representation scope, rematerialization races and a
safe negative-authority rule before deleting mappings. Do not reuse the physical GC
path or enable destructive GC as part of this issue. See B.3 in
`GC-X1-CLOSURE-OPTIONS.md`.

### ISSUE-GC-UPLOAD-FENCE-REMATERIALIZATION-01: Physical-delete ABA can remove re-uploaded bytes

**Status**: **CLOSED-FIX / CLOSED-EVIDENCE for the accepted physical-life scope,
effective on maintainer merge of [#312](https://github.com/Sesame-Disk/sesamefs/pull/312)**.
Before that merge: OPEN. The merge accepts the [final E1 decision](./E1-FINAL-DISPOSITION.md#final-acceptance-decision-2026-10-16),
including its greenfield/same-release premises and evidence limits; it closes this
issue's X1 criteria, not PRE-GC/A1 or destructive-GC activation.
**Discovered**: 2026-07-21
**Priority**: Blocker — potential loss of a live physical object
**Affected**: `gc_s3_orphans`, S3 orphan recovery, upload rematerialization

#### Disposition of the original X1 criteria (2026-10-17)

| Original criterion | Current implementation and evidence | Disposition on acceptance |
|---|---|---|
| Stale DELETE must not remove a new physical life | Fresh canonical K2 differs from retired K1; recovery validates the stored org/class/K1 locator and deletes only that exact key. [G4 real-service coexistence](./GC-G4-IMPLEMENTATION-PLAN.md#evidence-and-remaining-gates) and corrected [G5 evidence](./GC-G5-3DC-VALIDATION.md) cover intact P2 metadata/bytes/refs after D1 recovery. | CLOSED-FIX / CLOSED-EVIDENCE for the covered protocol; physical non-overlap is not required. |
| Two workers must not share claim authority; stale release/takeover must preserve the owner | [processBlock](../internal/gc/worker.go) generates a fresh UUID per attempt. [P4a ownership controls](../internal/gc/p4a_claim_ownership_test.go) cover distinct attempts, losing release/finalize, stale takeover and exact target. | CLOSED-FIX for ownership; the separate pre-PREPARED candidate-settlement scheduling race remains PRE-GC. |
| An authorized deletion must retain exact continuation after the canonical row retires | G1/G2/G3 root, COMMITTED handoff and durable P/D lifecycle precede retirement. G4 continues D1 without touching P2; terminal replay cannot reacquire authority. [A-I process cuts](./E1-01-CRASH-EVIDENCE-PLAN.md#01-recorded-evidence-by-exact-cut) cover named handoff/cleanup windows. | CLOSED-FIX / CLOSED-EVIDENCE; root-only C0 safety is retention, not autonomous completion. Unknown/refusal, scheduling and fault qualifications remain in the final decision. |
| R3/BorrowedFS and all W2 writers must not publish HEAD depending on retired P1 | [W2 inventory](./X1-CRITICAL-PATH.md#4-w2-exit-checklist) disposes every W2 row: own protection/repair and exact-P checks, or E1-17 settled source admission. [E1-16](./E1-16-POST-D-CONTRACT-ALIGNMENT.md) records the physical contract; named native wire/SIGKILL/three-DC evidence retains its limits. | CLOSED-FIX / CLOSED-EVIDENCE per W2 row. The historical W1-only explanation below no longer describes the complete implementation. |
| Delayed authorized PUT and replacement publication | [H1](./E1-H1-PROTECTED-SUCCESSOR.md) combines protected P2 before physical recovery, late K1 PUT and actual HEAD/canonical reads on P2. | Measured K1 residue is X3/leak for this healthy one-DC schedule; no universal native S3 fault claim. Leak/retention disposition before A1 remains required. |

The historical Blocker severity is preserved; this is a disposition of its
failure sequences, not a severity downgrade. PRE-GC/A1 and their full tagged
inventory remain OPEN, including Phase 5/6 metadata deletion, queue/repair
convergence, clock health and hard-delete fencing. Availability failures stay
FAIL. Ordinary `GC_ENABLED=false` remains mandatory in every DC.

#### Historical discovery and partial-implementation narrative

The following records the original sequence and W1-only evidence. Its OPEN and
"not closed" conclusions are historical, superseded for the accepted scope by
the disposition above; retain it as provenance, not a second current status.

An S3 delete already authorized for `blocks/<org>/<hash>` can complete after its
visible Cassandra fence clears and after a writer stores byte-identical content at
the same key. Content addressing makes ETag/value comparison unable to distinguish
the old lifecycle from the new one. Claim-stub repair does not close this in-flight
physical-delete ABA. Cassandra authorization generations or claim generations alone
cannot revoke an S3 DELETE already in flight. Never-reused physical keys close only the
stale-delete component of X1: a stale delete can then target only the old key. Publication,
claim ownership and recovery liveness must also be implemented and verified. Keep
destructive GC disabled until the complete X1 criteria pass.

R3 is part of this same blocker, not a separate issue: `check-blocks` is an advisory
read and does not reserve a block or bind its result to a later publish. A client can
skip it, or GC can remove a zero-reference block after the check and before
`stageSyncCommitBlockDelta`. That stage resolves the retained SHA-1 mapping and writes
`pub:` references without an atomic physical-existence/post-stage check, so a commit
can publish an `fs_object` that points at already-missing bytes. The race matrix tracks
this as R3; closing it requires the publication-fence/post-stage validation that is
already part of the X1 closure work.

W1 now closes the BorrowedFS `CreateFileFromBlocks` segment through the HEAD
CAS: the writer establishes its own `up:<session>` liveness for each distinct
borrowed block and re-validates the exact observed physical placement
(`db.ValidateBorrowedFSPublicationAuthority`, LOCAL_QUORUM -- not a bare fence
check, since a fence-only check cannot see a block GC has already fully
retired, and not the SERIAL repair boundary either, since that would pay a
Paxos round trip per BorrowedFS block on this dedup hot path) before HEAD.
The eight-leg real Cassandra+MinIO evidence gate is green. This does not
close the broader issue: R31/W2 still covers `up -> pub -> HEAD -> fs` crash
continuity, and other writer funnels remain outside W1.

Design analysis: `UPLOAD-FENCE-FINDINGS-REGISTRY.md` X1. Accepted architecture
(D0 freeze, X1 still OPEN):
[GC-X1-PHYSICAL-LIFE-HANDOFF-PLAN.md](./GC-X1-PHYSICAL-LIFE-HANDOFF-PLAN.md).
Historical option comparison and race matrix remain in
[GC-X1-CLOSURE-OPTIONS.md](./GC-X1-CLOSURE-OPTIONS.md) and are **not** the
active roadmap.

E1 delayed-PUT evidence (2026-10-03) is recorded in
[the E1 execution ledger](./E1-FULL-GC-PUBLICATION-RECOVERY.md), using Cassandra
plus pinned SILO `RELEASE.2026-09-16T00-00-00Z` (MinIO-compatible S3): after terminal
D, a held real Sync PutBlock left bytes at the retired K1 but installed a new
canonical P2 and no P1 `fs:` reference. This does not exercise an in-flight S3
DELETE racing a same-key re-upload, so it does not close or reduce this issue.

---

### ISSUE-GC-CROSS-DC-REFERENCE-VISIBILITY-01: GC can miss a live reference in another DC

**Status**: ✅ **Closed 2026-08-14** (implemented 2026-08-13) — formally closed on the date the five-leg evidence and both mutations actually ran, not the date the code landed
**Discovered**: 2026-07-21
**Priority**: Blocker — potential deletion of a live block
**Affected**: `block_references`, GC claim-then-verify, RF 1 per DC deployments

**The defect.** Reference writes and GC liveness reads both used `LOCAL_QUORUM`;
`SERIAL` applies to the conditional `blocks` transition, not to those ordinary
reference rows. With RF 1 per DC, the write quorum in one DC and the read quorum in
another need not intersect, so GC could read "zero references" for a block whose only
reference was already acknowledged elsewhere. The one-hour grace period mitigates
normal lag but is not a correctness bound.

**The fix (2026-08-13).** Implemented without r3: no generations, no physical
incarnations, no extra writer round trip, and no `SERIAL+ALL` fence — that fence serves
the publication TOCTOU, which is a different property.

An earlier draft of this line said "no writer hot-path change", which was wrong once the
producer pin below landed: reference writes now name their consistency explicitly rather
than inheriting it. The precise claim is *no additional round trip and no WAN
consistency on the upload path* — `EACH_QUORUM` is confined to the GC read. The pin is
also a **ceiling**, not just a floor: a deployment that had configured `EACH_QUORUM` or
`ALL` for reference writes is lowered to `LOCAL_QUORUM`, giving up the remote
acknowledgement that operator was asking for. Safe under X2 (the destructive read is
`EACH_QUORUM` regardless, so it still intersects) and inert in every shipped profile,
which already ran `LOCAL_QUORUM` — but it is a real reduction and is recorded as one.

The invariant now enforced is:

> Every physical delete is authorized by a liveness read that intersects every DC
> able to acknowledge a `LOCAL_QUORUM` reference write.

- `BlockHasReferencesGlobal` pins `EACH_QUORUM` per query (never inherited from the
  session) and backs `processBlock`'s claim-then-verify — the only read that may
  authorize destruction there. An unreachable DC makes it error; the error aborts the
  delete and hands the claim back, so failing closed does not also fence the block.
- The pre-claim check, the scanner and `enqueueZeroRefBlocks` deliberately stay at
  session consistency. The zero-check is asymmetric: a locally visible row is proof
  the block is alive, so aborting early is always correct, while a local zero proves
  nothing and authorizes nothing.
- **Every producer pins the write** at `db.BlockReferenceWriteConsistency`
  (`LOCAL_QUORUM`) per statement, rather than inheriting the session. This is the
  write half of the same intersection: the `EACH_QUORUM` read is only trustworthy
  because the write it must intersect reached a quorum in the DC that acknowledged it,
  and `ONE` is an accepted `database.consistency` under which one replica can
  acknowledge a reference a later 2-of-3 per-DC read quorum never sees — with a
  replication map that still looks perfect. It is pinned at the writers because it
  cannot be enforced at the reader: references are written by API nodes, separate
  processes with their own configuration that no check the GC worker runs can observe.
  The three statements today are the two in `AddBlockReference` (TTL and permanent) and
  the one in `AddProvisionalBlockReferenceWithExpiry`'s logged batch;
  `TestBlockReferenceProducersPinWriteConsistency` scans the module and fails on a new
  one that forgets. That scan is **syntactic and its limits are worth knowing**: it
  matches `INSERT\s+INTO\s+block_references` inside unquoted string literals, so
  reformatting can no longer blind it (the original fixed-substring form was blind to a
  line break, and because the count is a floor, a fourth producer in that shape passed
  silently — verified by adding one). It still cannot see a statement assembled at
  runtime via `fmt.Sprintf` or const concatenation; such a producer must be reviewed by
  hand, and the convention is to keep reference `INSERT`s as plain literals.

  `RemoveBlockReference` is deliberately *not* pinned, but **not** for the reason stated
  here previously. The old justification — "an under-replicated DELETE leaves the row
  visible, so GC declines to collect and the bytes survive a pass" — is not a property
  of Cassandra: a DELETE writes a timestamped tombstone, the mutation goes to every
  replica regardless of level, and last-write-wins reconciliation means a quorum read
  touching that tombstone resolves to absent and repairs the rest. There is no
  structural bias toward keeping data. What makes the exemption safe is the protocol:
  the X2 premise is about *creating* a live reference, while removal is only ever issued
  once the referrer has lost authority (a TTL'd publish attempt, or an `fs_object` being
  deleted). Publish/remove races are the publication fence, which is X1.

  **What the pin cannot reach is references already written.** It binds this binary
  forward; a row acknowledged by an older one carries whatever level that process was
  configured with, and no read can tell them apart afterwards. In practice there is
  nothing to reach: every shipped profile has always been `LOCAL_QUORUM`, destructive
  GC has never been enabled in any environment, and the gate refuses to start deleting
  under a non-quorum configuration anyway. Stated because it is the kind of gap that
  should be written down rather than assumed away — and if a deployment ever *did* run
  `ONE`, the remedy is a repair of `block_references` before destructive GC is ever
  turned on, not a code change.
- `RecoverS3Orphans` performs its **own** `BlockHasReferencesGlobal` before destroying
  bytes. It could have inherited authorization transitively — an orphan row cannot
  exist unless `processBlock`'s verify already passed — but that implication only runs
  forward in time, so it rested on a greenfield precondition that is unenforceable in
  code and *silent* if it ever stops holding. Recovery is the cold path; the read is
  cheap and the guarantee is now self-contained.
- A destructive topology gate (`ValidateDestructiveGCTopology`) refuses to delete
  unless live keyspace replication is `NetworkTopologyStrategy` with a positive RF in
  every mapped DC, the local DC among them, **and the live map exactly equal to the
  declared one**. The last clause is the one with teeth: the quorum-intersection proof
  is about the replica set that *accepted* each write, so shrinking the map after
  references were acknowledged elsewhere passes every structural check while
  `EACH_QUORUM` quietly stops being obliged to contact those DCs — and Cassandra does
  not relocate historical data on `ALTER`.

  **Changing topology while reference state exists is an operational migration, not a
  safe consequence of setting `GC_ENABLED=false`** — the distinction matters. The gate
  *detects live/config drift*; it does not certify history and it cannot block a
  concurrent `ALTER`. The supported baseline is that the replication DC set and RF
  remain immutable while existing `block_references` may have been acknowledged under
  that topology. If a topology change is required, destructive GC stays disabled and
  all block-reference producers are quiesced while operators run a separately
  certified migration: reconcile the old replica set, apply the new map and its data
  movement/repair, verify that historical reference state is present in the new
  authorizing replica set, update the declared map, validate, and only then resume
  producers. `GC_ENABLED=false` alone is not that certification.

  The gate is part of `GCStore`, so a store that drops it fails to compile rather than
  silently disarming; it guards both destructive paths; and it is re-evaluated per
  attempt — including once more at the commit point, immediately before the first
  destructive statement, which narrows the window between "gate passed" and "bytes
  destroyed" from the whole walk to two statements. That narrows a race the
  operational rule already forbids; it does not certify topology history, and nothing
  in the code can.

  **Scope of that check, precisely.** It compares the topology in effect now against
  the topology this process is configured with now. That catches the realistic
  accident — the keyspace altered without the deployment config, or the reverse — but
  it is not proof that the map is unchanged *since the references were written*: an
  operator who changes both together and restarts passes the gate while historical
  references still live in the dropped datacenters. Closing that in code needs a
  certified fingerprint (persist the map at first destructive activation; require
  explicit recertification after any change), which is deliberately not built. Until
  that exists, **topology/RF immutability or a separately certified migration is an
  operational precondition, not an enforced invariant**. Tracked as follow-up in
  `GC-X2-MULTIDC-VALIDATION.md`.
- Fail-closed is observable: `GCErrorsTotal{reason="liveness_verify_unavailable"}`,
  `{reason="destructive_topology_gate"}` and `{reason="cluster_unavailable"}`, plus
  `GCAuditEventsTotal{event="gc_block_delete_failed_closed"}`. Its counterpart
  `{reason="liveness_verify_failed"}` means the opposite condition and both destructive
  paths emit it: the global verify failed for a reason the cluster's availability does
  not explain — a `ReadFailure` from a tombstone-heavy `block_references` partition
  being the realistic one — which is specific to that block and does not resolve on its
  own. Neither deletes anything. They differ in what happens next and in who should
  look: the block path spends a retry on it so it reaches the DLQ and a human, the
  orphan sweep defers and holds its day cursor, and neither moves the blocked mark
  below, because one poisoned partition says nothing about whether the path can
  authorize deletes at all. Those are counters, and
  a counter cannot express *duration* — which is the whole signal, because failing
  closed is silent by design: nothing errors, nothing reaches the DLQ, and a
  permanently rejecting gate looks exactly like a fleet with nothing to collect. The
  pair `gc_destructive_last_blocked_timestamp_seconds{path}` and
  `gc_destructive_last_liveness_success_timestamp_seconds{path}` closes that, with
  `path` being `"block"` or `"orphan"`. Alert on:

  ```yaml
  expr: gc_destructive_last_blocked_timestamp_seconds
          > gc_destructive_last_liveness_success_timestamp_seconds
  for: 1h
  ```

  read as "the last evidence was a refusal, and an hour has passed without evidence to
  the contrary". Do **not** reduce it to `time() - ..._liveness_success > 3600`, which
  fires an hour after the last success — possibly seconds after a refusal began — nor
  to `max_over_time(...)`, which says "was blocked at least once recently".

  **Why a pair rather than one boolean gauge.** An earlier revision shipped
  `gc_destructive_deletes_blocked` as 0/1 and cleared it at the end of any worker pass
  that refused nothing. That is unsound in both directions and was actively harmful in
  one: because a postponed candidate is requeued with `queued_at=now` and waits out a
  full grace period, an ongoing outage produces runs of passes that attempt *nothing*
  between refusals, and each of those cleared the gauge and restarted the `for: 1h`
  window — so an outage that never ended never alerted. Latching the gauge instead
  merely inverts the lie, reporting an outage that ended whenever the fleet runs out of
  work. Two timestamps have no third state to mishandle: silence does not move them.
  The recovery half advances when the global read RETURNS, including when it finds the
  block still referenced — that is proof the environment can authorize, and requiring a
  completed delete would latch any fleet whose candidates are all live. A passing
  topology gate does not advance it: the gate proves the replication map still gives
  `EACH_QUORUM` per-DC meaning, not that a quorum is currently reachable. Both series
  are seeded to 0 at registration for both paths, so a never-exercised path reads as
  not blocked instead of dropping out of the comparison, and a first refusal after boot
  alerts without anyone inventing a startup success. Both are process-local: a restart
  resets the `for:` window. The `path` label is required because the two destructive
  paths fail independently — without it a clean worker pass would speak for an orphan
  sweep that is still refusing every delete.
- Fail-closed also does not consume the item's retry budget: the failure is systematic,
  so the ordinary five-retry path would DLQ every in-flight block within minutes of an
  outage, and block items are not auto-recoverable from the DLQ while the scanner's day
  cursor has already moved past their candidates. Fail-closed postpones instead,
  costing latency rather than the work — one grace period per postpone, since
  `RequeueItem` stamps `queued_at=now` and `DequeueBatch` only sees rows older than the
  grace period.

  **This follows the reason, not the statement.** The EACH_QUORUM verify is the call in
  the destructive walk that a datacenter outage breaks first and most reliably — it is
  the only level that demands a quorum in *every* DC. But `ClaimBlockDelete`,
  `BlockExists`, `GetBlockInfo` and `StartBlockDeleteOrphan` can fail on the same
  degraded cluster, and one of them runs *before* the verify. Protecting only the verify
  would have left the exact loss the protection exists to prevent reachable through the
  statement immediately preceding it. Every availability failure in the walk now
  postpones (`isClusterUnavailableError`: server-reported
  Unavailable/Overloaded/ReadTimeout/WriteTimeout, plus the driver's
  no-response/no-connection sentinels). Scope is deliberately narrow — a malformed
  statement or an unknown column still spends its retries and reaches the DLQ, because
  those *do* say something about the item and a human needs to see them.

  **Do not infer a rule about serial consistency from this.** An earlier draft claimed
  that `SERIAL` "needs a quorum in every DC", so a single remote DC outage would take
  `ClaimBlockDelete` down. That is wrong, and it was wrong in a direction that mattered:
  `SERIAL` takes a **global** quorum over all replicas of the token range (2 of 3 with
  RF 1 in each of three DCs — which one unreachable DC does not defeat), while
  `LOCAL_SERIAL` takes a quorum among the local DC's replicas. `EACH_QUORUM` is the
  per-DC level. The advisory built on that mistaken reasoning recommended moving
  multi-DC deployments to `LOCAL_SERIAL`; it has been **removed**, because narrowing the
  Paxos domain on the `blocks` partition is exactly the linearization question X1 still
  has open, and this gate has no business pushing a deployment either way.

  **Known limitation of the classifier.** The timeout codes are ambiguous: a read or
  write timeout can mean a degraded cluster, but also a hot partition, LWT contention on
  one row, or too tight a deadline. Treating them as environmental means a pathological
  item postpones indefinitely rather than surfacing in the DLQ. They are included anyway
  because a partial outage does produce timeouts and losing work there is the worse
  failure. **Such an item is not individually visible.** The blocked/liveness pair is
  per *path*: one block timing out advances the blocked half and the next healthy
  block's verify advances the success half straight past it, so the alert reads clear
  while that item postpones forever. Bounding environmental postpones per item needs a
  counter distinct from `retry_count` — a queue-protocol change, and X1's to make.
  Until then, a persistently timing-out item stalls silently.

  **This change WIDENED that residual, deliberately.** Before it, exactly one condition
  postponed without spending a retry: `library_hard_delete_in_progress` (tracked as E1
  in `ISSUE-GC-ENGINE-ROBUSTNESS-01`, "no postpone bound"). There are now four —
  `destructive_fail_closed`, `block_claim_not_yet_stale` and
  `block_claim_release_unconfirmed` join it — and `failClosedIfUnavailable` applies the
  first at *every* statement of the destructive walk rather than at one call site. The
  unbounded-postpone surface therefore grew from one narrow condition to most of the
  block path. Each addition is individually correct (losing the work item is the worse
  failure in every one of these cases), but the aggregate is that E1 stopped being a
  corner case and became the block path's default failure mode under a degraded
  cluster. Whoever builds the postpone bound should size it for that, not for E1 as
  originally written.

  `block_claim_release_unconfirmed` is the one that postpones on **non**-environmental
  errors too, and it is the sharpest instance of this trade: a permanently failing
  stale-claim release will postpone forever instead of reaching the DLQ. It is accepted
  because the alternative is worse — spending the budget strands a *live, still
  referenced* block behind `gc_state='deleting'` with no work item left to lift it, and
  `BlockDeleteFenceActive` then refuses every future upload of that content. The
  visibility is bought back explicitly: alert on
  `gc_errors_total{type="stale_claim_release_failed"}`, which fires only in that case
  and means a human has to intervene. Pinned by
  `TestX2_StaleClaimReleaseFailureSurvivesTheRetryBudget`.
- A `gc_s3_orphans` row whose block still has references is refused, logged, and
  counted (`GCAuditEventsTotal{event="gc_s3_orphan_referenced_deferred"}`) — but it
  does **not** fail the scanner phase. A failed phase suppresses `last_scan_success`,
  so one anomalous row would freeze that timestamp forever and make a healthy fleet
  indistinguishable from a broken one; `gc_s3_orphans` also has no resolved state to
  acknowledge. Alert on the counter.
- **~~`ISSUE-GC-STALE-CLAIM-READ-CONSISTENCY-01`~~ (✅ RESOLVED, `333db3c5e`, P4a
  — predates this branch).** `ReleaseStaleBlockClaim`'s claim observation used to run
  at session consistency, which could miss a claim committed cross-DC (RF 1 per DC,
  non-intersecting replica sets) or still in its same-DC Paxos window, and
  incorrectly report `BlockClaimAbsent` — authorizing `processBlock` to fall through
  and DELETE the candidate/queue-item that was the only thing left to lift a live
  fence. **No data loss** (nothing here authorizes a delete); the cost would have
  been a permanent upload refusal on that content. Fixed by P0/R12: every
  conditional mutation on `blocks` is now pinned to `SerialConsistency(gocql.Serial)`,
  giving the partition exactly one global serial domain, so this read
  (`settleBlockDeleteClaimState`, `Consistency(gocql.Serial)`) is a global
  linearizable read that correctly intersects it — closing both the cross-DC and
  Paxos-window gaps this entry described without the `EACH_QUORUM`/`LOCAL_SERIAL`
  trade-off it used to weigh. See the doc comment directly above
  `ReleaseStaleBlockClaim` in `internal/gc/store_cassandra.go`, which already
  narrates this resolution. This entry was stale documentation, not a live gap;
  found and corrected while re-auditing `#212` (G3) on 2026-09-10.
- **`ISSUE-GC-ROOT-CYCLE-CLOCK-ASSUMPTION-01` (OPEN, P2 PRE-GC / OPERABILITY).**
  Registered 2026-10-03 during G5 contract review. The THIS-PR unconditional
  finite-cycle wording is corrected; the activation prerequisite remains OPEN.
  The seek key begins with `gc_claimed_at` from the claiming application's
  wall clock. Capturing `Until` bounds keys, not insertion membership: a root
  published after capture with `After < key <= Until` joins the active cycle,
  while a root inserted behind `After` is not visited until wrap. Clock skew,
  regression or delayed publication can therefore extend the active cycle;
  arbitrary indefinitely backdated arrivals have no unconditional revisit
  guarantee. No permanent starvation counterexample in the supported healthy
  clock regime or exact-P/data-loss defect is claimed.

  Eventual wrap requires advancing clocks with bounded, observed fleet skew,
  finite claims minted in finite time, finite outstanding pre-cutoff claims,
  and successful subsequent discovery/checkpoint ticks. Once newly minted
  timestamps exceed the captured cutoff, new claims sort outside the cycle.
  The per-tick page/row bound and exact `(P,D)` destructive authority are
  unchanged; there is no fixed wall-clock completion/revisit deadline.

  Before GC activation, synchronize every application node that mints claim
  timestamps with NTP or equivalent, define and observe the acceptable fleet
  skew/progress bound, and specify what prevents activation or suspends GC
  when clock health is unknown, regresses or exceeds that bound. Merely
  enabling NTP is not evidence that the bound holds. G5 adds no clock-health
  enforcement or monitoring implementation; verification belongs to E1/A1.
  This is an operational prerequisite, not a scheduler/schema redesign in G5.
  It does not close the independent pre-PREPARED stale-claim race below.
- **`ISSUE-GC-STALE-CLAIM-SETTLE-RACE-01` (OPEN, P1 FOLLOW-UP / PRE-GC).**
  The same-P Absent/new-owner interleaving was reproduced during G5. It is a
  pre-PREPARED scheduling gap, outside durable COMMITTED-root recovery. G5's
  attempted referenced-candidate retention is withdrawn: keeping candidate_at
  let a later live-to-zero epoch inherit expired grace. Closing this issue must
  preserve recovery scheduling without preserving stale zero-ref eligibility;
  a second non-atomic claim read is insufficient. Neither CLOSED-EVIDENCE nor
  CLOSED-FIX applies. Historical counterexample follows.

  `ReleaseStaleBlockClaim`'s SERIAL observation and the caller's later
  `settleBlockCandidate` are two separate operations, not one CAS: after the
  observation reports `BlockClaimAbsent` (row present for this exact P, no
  claim), a different worker can legitimately win `ClaimBlockDelete` on that
  same row/same P in the gap before the first worker's `settleBlockCandidate`
  runs. `settleBlockCandidate` deletes the block-GC candidate unconditionally
  by its own identity, with no re-check of claim state at delete time, so the
  first worker can retire the candidate/queue-item authority the second
  worker's brand-new claim would need to recover if that second worker then
  crashes mid-delete. This is orthogonal to the read-consistency gap above
  (`ISSUE-GC-STALE-CLAIM-READ-CONSISTENCY-01`): even a perfectly accurate
  SERIAL read is stale by the time the unconditional delete runs.
  **`BlockClaimMissing` (row gone) is NOT part of this race**, unlike an
  earlier version of this entry claimed: with the canonical row absent,
  `ClaimBlockDelete` cannot "win" a claim on it — it observes the same missing
  row and reports `BlockClaimCanonicalRowMissing` too, materializing nothing.
  The only way a row reappears for that block_id is a fresh writer installing
  a brand-new physical life P2 (`InstallBlockMetadata`'s first-writer
  `INSERT ... IF NOT EXISTS`), which is a different operation entirely, and
  any GC candidate later discovered for P2 has its own identity
  (`BlockGCCandidateIdentity` is keyed by exact `Target`, i.e. exact P, plus
  `candidate_at` — see `internal/gc/store.go`). The stale worker's
  `settleBlockCandidate`, scoped to the old P1 identity, cannot touch or
  consume P2's candidate; `DeleteBlockGCCandidate(P1) != DeleteBlockGCCandidate(P2)`.
  Predates G3 (`#212`); confirmed during independent re-reviews of `#212` on
  2026-09-10 while checking whether `fa5f69066`'s
  `BlockClaimMissing`/`BlockClaimAbsent` split changed this window — it does
  not, and #212 does not widen it (the split also does not narrow the real,
  `BlockClaimAbsent`-only race). Not fixed here: needs its own PRE-GC
  follow-up, e.g. making the candidate delete conditional (a single atomic CAS)
  on the exact claim/authority state that authorized removing it. A plain
  re-read immediately before the delete is NOT a sufficient fix on its own —
  two separate operations (a fresh read, then a separate delete) are still
  TOCTOU; the two must become one atomic operation. Exposure today is nil:
  destructive GC runs nowhere (`GC_ENABLED=false`).
- **`ISSUE-GC-REFERENCED-ORPHAN-LIFECYCLE-01` (SUPERSEDED / LEGACY-NOT-REACHABLE under greenfield v1).**
  G4 irrevocable exact-D continuation and G5 current-protocol durable roots
  supersede the historical empty-state/day-cursor/90-day executor. No deployed
  old data exists; no quarantine state or compatibility executor is required.
  The historical storage-leak counterexample is preserved below. The historical bullet above
  used to justify itself with "the condition is permanent by construction — the row
  survives and every sweep rediscovers it". That is false. A sweep ending without a
  phase error advances the day cursor, and the next starts only `gcScanOverlapDays`
  back, so once the cursor passes the row's bucket nothing revisits it; the row then
  TTLs out at 90 days, taking the recovery metadata with it. If the anomalous reference
  later goes away, the bytes are never collected — and the counter above goes quiet at
  the same moment, so the alert stops firing while the condition persists. No live data
  is deleted: recovery refuses, it does not guess. The fix is a lifecycle of its own —
  a durable deferred/quarantine state, or re-projection into a future bucket — not a
  `phaseErr`, which is the thing that froze the scanner in the first place.

**Evidence.** `internal/gc/x2_cross_dc_liveness_test.go` (sixteen regressions — this
count has now been wrong twice, first stating twelve against a file of thirteen and
then fourteen against a file that grew to sixteen, which is why the instruction below
matters more than the number; derive it with
`grep -c '^func Test' internal/gc/x2_cross_dc_liveness_test.go`),
`internal/gc/x2_audit_followups_test.go` (fifteen; the post-implementation audits: the
cross-candidate stale-claim release and its fresh-claim boundary, availability failures
at the claim not burning retries, non-availability errors still reaching the DLQ from
both the claim and the global verify, the topology gate never caching a rejection and
being re-checked at the commit point, and the blocked/liveness pair — surviving a pass
that attempts nothing, clearing only on a real read, staying per-path, and ordering a
commit-point refusal after a success in the same walk) and
`internal/db/destructive_gc_topology_test.go` (the gate's decision logic against
synthetic replication maps, including a shrunk map that passes every structural
check). Every assertion is mutation-verified — each was confirmed to fail against a
deliberately reverted implementation, including the canary that reverting the single
`BlockHasReferencesGlobal` call makes the suite delete a live block under an
unavailable DC.

The closing round added three of those and mutation-verified each independently:
`TestX2_StaleClaimReleaseFailureSurvivesTheRetryBudget` (red when the branch is routed
back through `failClosedIfUnavailable`, and separately red when the dedicated counter
is mislabelled), the hardened
`TestBlockReferenceProducersPinWriteConsistency` (a fourth unpinned producer written
with a line break after `INSERT` was added to `block_references.go`: the previous
fixed-substring scan stayed **green**, the whitespace-tolerant one goes red), and the
three-DC `TestX2_FailsClosedWhenTheReferenceDatacenterIsDown` (red under a `QUORUM`
destructive read, via `scripts/x2-multidc-validation.sh --mutate-quorum`).

**Closure evidence (2026-08-14).** All five legs ran green on
`docker-compose.cassandra-3dc.yaml` — Cassandra 5.0.9, three datacenters, RF 1 each,
the production shape — driven by
[`scripts/x2-multidc-validation.sh`](../scripts/x2-multidc-validation.sh):

1. **Visibility.** Against a deliberately divergent cluster (hinted handoff disabled,
   the other two DCs stopped during the write), `LOCAL_QUORUM` from dc-na is blind to
   the reference while `EACH_QUORUM` from dc-na sees it. Both halves asserted against
   the same state; the first is what makes the second mean anything.
2. **Fail closed.** With dc-asia stopped the destructive read errors —
   *Cannot achieve consistency level EACH_QUORUM in DC dc-asia* — rather than
   reporting zero. A false zero here is data loss.
3. **Fail closed with the DC holding the ONLY reference down.** Against a *fresh*
   divergent state with dc-eu stopped, the destructive read errors — *Cannot achieve
   consistency level EACH_QUORUM in DC dc-eu*. This is the leg that separates
   `EACH_QUORUM` from plain `QUORUM`, which would be satisfied by the two blind
   datacenters and answer zero.
4. **Topology gate.** Accepts the declared three-DC map; refuses a session declaring
   only dc-na against that same keyspace.

**And the legs were proven able to fail — both of them.** With
`.Consistency(gocql.EachQuorum)` downgraded to `LocalQuorum`, against a *fresh*
divergent state, leg 1 goes red with "X2 REGRESSION: reference acknowledged at
LOCAL_QUORUM in dc-eu is invisible to the EACH_QUORUM read from dc-na". Downgraded
instead to `Quorum`, with dc-eu stopped, leg 2b goes red with "X2 REGRESSION: the
destructive read returned zero references while dc-eu — the only datacenter holding
one — was unreachable". A regression that cannot fail is not evidence; the second
mutation is the one that rules out the plausible WRONG fix rather than the original
defect.

Two notes for anyone re-running it. The visibility leg is **single-use per block id**:
the `EACH_QUORUM` read performs blocking read repair to satisfy its own consistency
level, so it propagates the row to dc-na as a side effect of reading it — the script
mints fresh ids each run. And **RF 3 is out of scope**: under
`NetworkTopologyStrategy` the factor is per DC, so it would need nine nodes, and it is
hardening rather than closure.

**Two DCs reproduce the defect but cannot rule out the wrong fix**, which is why
`docker-compose.mr-cluster.yaml` is not the instrument. At two DCs with RF 1 a
`LOCAL_QUORUM` read from dc-na is still blind to a reference acknowledged in dc-eu
while `EACH_QUORUM` still sees it, so the bug and its fix *are* distinguishable there.
What is not distinguishable is `EACH_QUORUM` from plain `QUORUM`: with two replicas
total a non-local `QUORUM` is 2 of 2 and intersects everything by accident, so a
two-DC suite would bless `QUORUM` — which is not a valid closure. Only at three DCs
does `QUORUM` become 2 of 3 and able to miss the replica holding the reference. The
integration tests skip when fewer than three DCs are configured, so a two-DC
environment cannot report a false pass.

**Closing X2 does not enable destructive GC.** `GC_ENABLED=false` remains mandatory on
every replica in every DC. X1's original criteria are disposed in
[its current issue entry](#issue-gc-upload-fence-rematerialization-01-physical-delete-aba-can-remove-re-uploaded-bytes):
scoped closure is effective on maintainer merge of the E1 decision; before then
OPEN. PRE-GC/A1 remain OPEN and independently prohibit activation. Design evidence:
`UPLOAD-FENCE-FINDINGS-REGISTRY.md` X2; the historical independence analysis is in
[GC-X1-CLOSURE-OPTIONS.md](./GC-X1-CLOSURE-OPTIONS.md).
The accepted architecture is frozen in
[GC-X1-PHYSICAL-LIFE-HANDOFF-PLAN.md](./GC-X1-PHYSICAL-LIFE-HANDOFF-PLAN.md)
(D0, 2026-09-02, documentation only). The generational-fence protocol explored
on `docs/gc-x1-x2-generation-fence-final` was abandoned 2026-08-14. Historical
option comparison stays in the CLOSURE-OPTIONS file and is not the active
roadmap. Nothing in D0 enables deletion.

---

### ISSUE-UPLOAD-PUT-BEFORE-INTENT-01: Crash before metadata leaves undiscoverable S3 bytes

**Status**: 🟡 Open
**Discovered**: 2026-07-21
**Priority**: Medium — storage leak, not live-data deletion
**Affected**: block upload and legacy resumable upload

The physical PUT precedes any GC-discoverable block metadata/reference or durable
physical-object intent. A session may already have staged accounting state, but that
state does not identify the stored object for reclamation. Minted keys broaden the
existing leak beyond a process crash: an ambiguous PUT, unresolved request-local
install uncertainty, or failed exact cleanup can also leave bytes no GC phase can
discover safely. Pre-INSTALL representation retries now retain one already-PUT target,
including when Cassandra reports that a replica is bootstrapping, and a permanent,
canceled, or exhausted representation preparation performs bounded detached exact
cleanup without submitting INSTALL; KnownLost cleanup also retries the exact target.
This only narrows the representation-resolution window. The post-materialization
confirmation probe is now phase-aware: a temporary rowless `NeedsPut` observation
returns retryable `canonical state not visible` and retries confirmation without
minting another target after a successful INSTALL. Cancellation races and
provisional-reference or delete-fence failures before INSTALL can still return to
an outer retry that remints while the prior target survives; those remain existing
X3 leak cases, not closed pre-INSTALL windows.
The final cleanup result is exposed by
`block_upload_fresh_physical_cleanup_total{result,reason}`, but that process-local
counter is not durable object intent and cannot reconcile a failed cleanup. P2 has no
durable attempt table or reconciliation worker. P2 therefore does not close X3.
Closing it requires durable
physical-object intent before PUT or a sweeper with a safe ownership proof. Tracking:
`UPLOAD-FENCE-FINDINGS-REGISTRY.md` X3.

**The cleanup counter is narrower than the set of paths that retain bytes, and must
not be read as coverage of this issue.** `block_upload_fresh_physical_cleanup_total`
is emitted only where a cleanup is actually attempted: the conclusively unsubmitted
pre-INSTALL branch and KnownLost. It is NOT emitted by the two branches that
deliberately keep their object — ambiguous settlement and the direct single-use
identity contradiction — nor by the provisional-reference and delete-fence failures
described above, which return the retryable sentinel and leave the minted target
behind for an outer remint. The confirmation-only rowless state is deliberately
different: it retains no new target and never authorizes a mint. The remaining
pre-INSTALL retention paths still have no other signal, so a counter showing no
`result="failure"` samples is consistent with every retained object this issue
describes and must not be quoted as evidence that none exist. Making the class
observable means adding a `result="retained"` outcome on the deliberate-retention
branches and counting the pre-INSTALL sentinel returns; that is a small, separable
change, it is still not durable object intent, and it would not close X3 on its own.

---

### ISSUE-GC-MULTIINSTANCE-01: Multi-instance GC coordination and split-brain hardening

**Status**: 🟡 Lease implemented; destructive activation blocked by OPEN PRE-GC/A1 (and X1 until maintainer acceptance)
**Discovered**: 2026-03-17
**Priority**: 🟡 High — required before scaling to multiple replicas
**Affected**: `internal/gc/worker.go`, `internal/gc/scanner.go`, `internal/gc/gc.go`

**Original problem (now coordinated by `gc_leases`):**
Before the LWT lease, multiple server replicas could execute worker and scanner work in parallel, causing:

1. **DequeueBatch without locking**: `SELECT ... LIMIT ?` returns the same items to all instances. Both process the same items simultaneously.
2. **Scanner without leader election**: Multiple scanners enqueue the same orphans as duplicates (the PK includes `queued_at = time.Now()`, so each INSERT creates a distinct row).
3. **Snapshot drift** (substantially resolved): the original `gc_queue_stats` counter table was retired from the baseline schema. Queue/DLQ totals now live in `gc_stats` and `gc_org_stats`, dirty orgs are exact-recalculated from canonical rows off the write path, hot `COUNT(*)` reads are gone, and DLQ expiry is explicit. Snapshots remain approximate until the background/admin refresh runs. See ARCHITECTURE.md / GC-SERVICE-ANALYSIS.md.

**Multi-instance duplication itself does not add a separate data-loss path**, but
destructive GC is not currently safe to enable because the upload-fence audit found
the independent physical-delete ABA and cross-DC visibility blockers above:
- `DeleteBlock` uses a claim-then-verify delete fence: only one instance can win the claim, and the winner re-checks live `block_references` before touching S3
- `block_references` rows make fs_object cleanup idempotent; retrying the same delete removes the same keyed refs again instead of replaying a counter decrement
- Cassandra DELETEs are idempotent

**Actual impact of this issue**: wasted work (CPU/network overhead) and slightly
incorrect admin counters. This statement does not close or downgrade the independent
destructive-GC blockers above.

**Current operational decision (updated 2026-08-14):**
- Keep `gc.enabled=false` and `GC_ENABLED=false` on every replica in every DC while X1 or PRE-GC/A1 remains open.
- X2 is closed under the stable-topology operational contract. A replication DC-set or RF change with existing reference state requires a separately certified migration before GC can be reconsidered.
- The Cassandra LWT lease (`gc_leases`) coordinates participants but does not close X1 and is not permission to enable GC.
- Only after X1 closes **and** the PRE-GC / A1 gate ([X1-CRITICAL-PATH.md](X1-CRITICAL-PATH.md) §6) closes may designated replicas in one DC set `GC_ENABLED=true` and participate under the lease. Every replica in every other DC remains false.

**Leader Election via LWT:**
- Implemented with `gc_leases` and TTL-backed heartbeats.
- Enabled replicas try `INSERT ... IF NOT EXISTS` first, then renew ownership with `UPDATE ... IF instance_id = ?`.
- If the leader dies or loses its lease, another enabled replica can take over automatically after lease expiry.

**Recommended future direction:**
- After X1 and the PRE-GC / A1 gate close, keep the explicit `GC_ENABLED=true` activation model so only designated replicas in one DC opt in.
- Consider exposing lease state/owner in admin status if operators want clearer observability during failover drills.

**Multi-region deployment note (updated 2026-08-14):**
While X1 or the PRE-GC / A1 gate remains open, running GC in even one DC is unsafe: keep it disabled in all DCs. After both close, restricting participants to a single DC is **critical**. The shipped default uses `SERIAL` for LWT operations (a global serial phase relative to the effective replica set); an environment override can change that. Running GC on multiple DCs would cause:
- `DequeueBatch` (non-LWT SELECT) returning the same items to workers in different DCs
- Scanner in both DCs enqueueing duplicate orphans
- Unnecessary cross-DC Paxos transaction contention on every LWT

Post-activation (X1 + PRE-GC / A1) topology is `GC_ENABLED=true` only on designated replicas in one DC and `GC_ENABLED=false` everywhere else. Until both close, the topology is `GC_ENABLED=false` everywhere. The lease provides failover only among designated replicas in that one DC.

Block-level conditional operations include first-writer metadata creation, GC claim,
claim release/finalize, and orphan lifecycle transitions; production defaults these
LWTs to `SERIAL` (global serial phase). Do not change a multi-DC deployment to `LOCAL_SERIAL`. This
still does not serialize ordinary `LOCAL_QUORUM` `block_references` writes, which is
the separate visibility blocker documented above.

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

**Key decision**: Should groups/departments be managed via OIDC provider (claims-based sync) or locally in SesameFS? The full analysis with 3 options was in `CURRENT_WORK.md` "PRIORITY 1" in February 2026 (`git show 6554adb81:CURRENT_WORK.md`).

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

**Remaining Frontend Work**: the list of UI elements needing permission checks was in `CURRENT_WORK.md` in January 2026 (`git show f624f9316:CURRENT_WORK.md`, "Frontend Permission UI Completion").

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

### Garbage Collection — IMPLEMENTED; P10 CROSS-ORG BLOCK DELETE FIXED
**Status**: Core engine implemented (2026-01-30), major overhaul (2026-03-17);
2026-07-10 audit found P1–P10 follow-ups. Refreshed 2026-07-16: the safety-classification gaps
(P6a/P6b) and the normal permanent-delete path (P1/P1b/P2, PR #129) are **closed** on `main`.
Remaining debt is reclamation edge cases (P4/P7), observability (P5), test hygiene (one unattributed S3-only orphan; 1A–1C/1G done), and
the Medium Phase 9 global-scan scale debt (P8). **P10's proven pre-fix cross-org shared-object
deletion is fixed through PR-3 with org-scoped physical keys and real Cassandra+MinIO regressions.**
**Files**: `internal/gc/` — gc.go, queue.go, worker.go, scanner.go, store.go, store_cassandra.go, gc_hooks.go, gc_adapter.go
**Tests**: 55 Go unit tests + 21 bash integration tests
**Admin API**: `GET /api/v2.1/admin/gc/status`, `POST /api/v2.1/admin/gc/run`

**2026-03-17 overhaul:**
- Worker: 7 item types (block, commit, fs_object, block_mapping, share_link, share, restore_job)
- Scanner: 8 phases (orphaned blocks/commits/fs_objects, expired share links/versions/auto-delete/shares/restore jobs)
- Commit deletion now cascades → root fs_object → child entries → blocks (was missing cascade)
- Library deletion enqueues all artifacts (shares, tags, tokens, locked files)
- GC now avoids full-table scans on block deletion by resolving SHA-1 from `blocks.sha1` and deleting the single forward mapping row by key
- `walkFSTree` converted from recursive to iterative (prevents stack overflow)
- Stats persisted to `gc_stats` table on shutdown, restored on startup (survives container restarts)
- Scanner runs immediately on startup before entering 24h ticker loop

**Current audit status:** do not treat this historical implementation-complete milestone as
an end-to-end safety certification. See
[GC-DELETE-CLEANUP-INVESTIGATION.md](GC-DELETE-CLEANUP-INVESTIGATION.md) and the
`ISSUE-GC-*` entries below.

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

**Current code correction (2026-10-17):** this title and the diagnosis below are
historical. The handler now selects off-HEAD-chain commits outside keep_days and
calls the registered GC commit enqueuer; it is not a blanket no-op. The current
worker treats these unguarded commits as metadata-only retirement, with the
[same shared-tree protection/retention trade-off as Phase 5](./GC-PHASE5-SHARED-FSOBJECTS.md#result-and-contract).
The issue remains OPEN for [follow-up reconciliation](./TECHNICAL-DEBT.md#phase-5-follow-ups-suite-wiring-and-trash-clean-tracking)
of expected cleanup/error behavior against the actual handler. No runtime issue
is closed or downgraded by this tracking correction; no Trash API changes here.
The original heading is retained so existing links still resolve.

#### Historical diagnosis (2026-02-24)

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

### ~~ISSUE-FRONTEND-ORG-DELETE-01~~: Superadmin Org Soft-Delete/Restore UI — ✅ RESOLVED

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

When `saveEditedDocument` rolls back a materialized block after a publish failure, it calls `DecrementBlockRefCountsOnce` + `enqueueZeroRefBlocks`. The mapping rows are inserted before rollback, and R11a intentionally lets the physical GC lifecycle leave that logical mapping in place.

There is still ordinary async-cleanup risk if the enqueue or GC worker path is unavailable, but that is covered by the fire-and-forget cleanup debt in `TECHNICAL-DEBT.md`; it is not a separate confirmed OnlyOffice mapping leak.

**Evidence**:
- OnlyOffice rollback path: `internal/api/v2/onlyoffice.go` calls `DecrementBlockRefCountsOnce` and `enqueueZeroRefBlocks` after metadata publish failure.
- The reverse table `block_id_mappings_by_internal` was dropped in PR7. R11a keeps the forward mapping independent of physical GC; a later logical-death reaper remains optional follow-up work.

---

### ISSUE-LIB-DELETED-FENCE-01: Soft-deleted libraries still accept star mutations

**Status**: 🟡 Pending
**Severity**: Medium-High - lifecycle correctness gap with data-drift risk during GC cascade
**Affected**: `POST /api/v2.1/starred-items/`, `POST /api2/starredfiles`, and any repo-scoped mutating path that only checks canonical existence

#### Problem

Library soft-delete is a two-phase lifecycle:

1. The API marks `libraries.deleted_at` and inserts a `deleted_libraries` marker.
2. Later, GC acquires the hard-delete lock, cleans auxiliary tables, and permanently removes the library.

PC-D1A's authority-only LWTs also fail closed when `deleted_at` is already
visible as non-null. This guard does not serialize the ordinary production
soft-delete/restore/hard-delete lifecycle with the global HEAD Paxos domain;
`ISSUE-LIB-DELETED-FENCE-01` remains a prerequisite before a productive
PC-D1B consumer relies on the frontier across concurrent lifecycle activity.

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
- Platform writes route deterministically by a canonical UUID hash (`CounterShard` / `CounterShardUUID`), so each org's inc/dec path stays balanced on the same shard even if callers vary letter case in UUID strings.
- The initial shard width is `32`, matching the repo's other modest fan-out bucket choices and giving more write-dispersion headroom for multiregion global aggregates.

**Why this shape is safe**:
- The hot quota paths still read single-partition org/user/library counters.
- Only cold sysadmin/global readers fan out across shards.
- Reconciliation also buckets platform expected totals by the same deterministic shard.
- Counter writes remain non-idempotent Cassandra operations: do not mark them idempotent, do not mix them into non-counter batches, and do not rely on automatic retries to replay them safely.

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

### ISSUE-GC-ORG-TRASH-NO-CASCADE-01: Org-Admin Trash Delete Defers Content Cleanup

**Status**: 🟢 Fixed (P1/P1b, 6A/6B + org-admin parity follow-up — 2026-07-14)
**Severity**: High — a permanent-delete action reports success and hides the library immediately, but its blocks/commits/counter/tags could linger for the whole retention window
**Affected**: `DELETE /org/:org_id/admin/trash-libraries/:rid/` (`DeleteOrgTrashLibrary`), `DELETE /org/:org_id/admin/trash-libraries/` (`CleanOrgTrashLibraries`)

#### Fix (this PR — durable purge-request marker, 6A/6B)

Migration `012` adds `deleted_libraries.purge_requested_at`. Permanent-delete now routes
through a single writer (`hardDeleteLibraryRowsFn`, shared by all four paths — superadmin
single/bulk and org-admin single/bulk) that stamps it. Phase 13's
`ListExpiredDeletedLibraries` now treats a row as eligible when
`purge_requested_at IS set OR deleted_at < cutoff`, so a permanent delete is picked up on
the **next scan** instead of after the configured `TrashRetentionDays` (~30d default). The
existing `ItemLibraryCascade` then performs the full reclamation it already owns (contents →
`HardDeleteLibrary` = policy index + read models + marker, `DeleteLibraryStorageCounter`).

**Two edge invariants (post-review):**
- The writer **preserves the original `deleted_at`** (the trash time) rather than resetting it
  to now. Phase 13 dedups `library_cascade` by `deleted_at`; resetting it would let a cascade
  already queued under the original identity be enqueued a second time. `purge_requested_at`
  provides eligibility independently, so no reset is needed.
- The v2.1 owner path and the platform/org-admin permanent-delete paths now **immediately queue
  the durable `ItemLibraryCascade`** (`gcEnqueuer.EnqueueLibraryCascade`) so reclamation starts
  on the next worker tick instead of waiting up to a full `ScanInterval` (default **24h**) for
  Phase 13 to discover the marker. This enqueue mirrors Phase 13 exactly — `QueuedAt =
  deleted_at`, nil library id, same representation — so it is the **same** row Phase 13 would
  create: a dedup no-op, never a second producer (the earlier content-only
  `EnqueueLibraryDeletion`, identity = now, was a second producer and was replaced). The legacy
  `/api2/repos/deleted/:repo_id` route still mounts the shared handler with `libHandler=nil`, so
  it skips this accelerator and relies on the durable marker + Phase 13.

**Grace period semantics:** because the cascade (immediate or Phase-13) uses
`QueuedAt = deleted_at`, the GC grace period is measured from the **original trash time**, not
from the permanent-delete action. A library that has been in trash longer than the grace period
is therefore processable on the next tick; one trashed moments before permanent-delete still
waits out the grace window. This is intentional and consistent between both producers.

**Timing (not "instant"):** this changes *eligibility*, not the grace gate. The cascade is
enqueued with `QueuedAt = deleted_at`, and the worker only dequeues items older than the
configured `grace_period` (default 1h) — the grace window is intentionally preserved. With the
immediate `EnqueueLibraryCascade` wired on the v2.1 owner path plus the platform/org-admin
permanent-delete paths, net reclamation latency there drops from the configured
`TrashRetentionDays` (~30d default) to about the grace period in the common case; if that
best-effort enqueue is lost, Phase 13 recovers it on the next scan. The legacy
`/api2/repos/deleted/:repo_id` route still adds up to one `ScanInterval` because it relies on the
marker-only recovery path. Normal soft-delete leaves `purge_requested_at` null and keeps the
retention behavior unchanged.

**Restore/delete race follow-up (landed):** the permanent-delete path now keeps the shared
library hard-delete lease alive for the entire share/upload-link cleanup window before the final
hard-delete batch. That closes the stale-lease hole where a very long link cleanup could let
`restoreDeletedLibrary` steal the lease and restore the repo after links were already removed.

The org-admin follow-up deliberately uses the same durable `ItemLibraryCascade`, not a separate
content-only accelerator, so every producer shares one identity and Phase 13 remains the only
recovery path needed.

#### Problem (original, pre-fix)

Both org-admin trash-delete paths hard-delete the library rows and insert the `deleted_libraries` marker, then return `200`, but — unlike the superadmin `PermanentDeleteRepo` — they do **not**:

- enqueue an immediate GC cascade (`EnqueueLibraryDeletion`),
- delete the storage counter,
- clean library tags.

They rely on scanner Phase 13 (`scanExpiredDeletedLibraries`), which only picks up a deleted library after `TrashRetentionDays` (~30 days by default). Worse, the hard-delete re-inserts the marker with `deleted_at = time.Now()` ([org_admin_repos.go:432](../internal/api/v2/org_admin_repos.go#L432), [library_delete_helpers.go:36](../internal/api/v2/library_delete_helpers.go#L36)), **resetting the retention clock**, so Phase 13's pickup is 30 days from the permanent-delete action, not from the original trashing.

This is not an eternal leak (the marker keeps the content recoverable, and the cascade can run even after the `libraries` row is gone because `GetLibraryDeletedAt` reads the marker — [store_cassandra.go:1249](../internal/gc/store_cassandra.go#L1249)), but the user asked for a *permanent* delete and the physical content can survive for the full retention window.

#### Evidence

- [internal/api/v2/library_delete_helpers.go:98-116](../internal/api/v2/library_delete_helpers.go#L98) (`deleteResolvedTrashLibrary`)
- [internal/api/v2/org_admin_repos.go:390-448](../internal/api/v2/org_admin_repos.go#L390) (`CleanOrgTrashLibraries`)

#### Follow-up Landed (org-admin immediate cascade parity)

The remaining latency-only follow-up is now implemented:

- `DeleteOrgTrashLibrary` now immediately enqueues the same durable,
  Phase-13-deduplicated `ItemLibraryCascade` used by the other permanent-delete paths.
- `CleanOrgTrashLibraries` now does the same after each successful hard-delete.

#### Related Docs

- `docs/GC-DELETE-CLEANUP-INVESTIGATION.md` (P1/P1b)

---

### ISSUE-GC-DELETE-HANDOFF-DURABILITY-01: Permanent-Delete Handoff Is a Fire-and-Forget Goroutine

**Status**: 🟢 Resolved (6A/6B + edge review, 2026-07-13)
**Severity**: was High/Med — correctness is now durable via the marker; the immediate enqueue is a pure best-effort accelerator
**Affected**: `PermanentDeleteRepo` (`permanentDeleteResolvedRepo`), `AdminCleanTrashLibraries` (`processAdminTrashCandidates`)

#### Resolution

Correctness no longer depends on the fire-and-forget enqueue. Every permanent-delete writer
stamps the durable `deleted_libraries.purge_requested_at` marker (migration 012); Phase 13 is
eligible on `purge_requested_at != null OR deleted_at < cutoff` and enqueues the full
`ItemLibraryCascade` (content + counter + policy + marker cleanup). The superadmin/platform
paths additionally call `Service.EnqueueLibraryCascade` immediately so reclamation starts on
the next worker tick instead of after up to a `ScanInterval`. That immediate enqueue is
**best-effort**: it produces the byte-for-byte-identical row Phase 13 would (so it is a dedup
no-op, not the old content-only second producer), and if the goroutine is lost to a
restart/exit the marker still drives Phase 13. The only consequence of a lost enqueue is
latency (up to one scan), never lost cleanup.

#### Historical problem (pre-fix)

The superadmin permanent-delete paths enqueued GC cleanup with `runAsyncLibraryDeleteSideEffectFn`
(`go fn()`). The old `EnqueueLibraryDeletion` enqueued **contents** (identity = now,
`LibraryGuardNone`), not `ItemLibraryCascade`, and did not own final marker/policy cleanup, so a
restart could lose it and the full cascade waited out retention. Replaced by the durable marker +
identity-matched `EnqueueLibraryCascade`.

#### Related Docs

- `docs/GC-DELETE-CLEANUP-INVESTIGATION.md` (P2)

---

### ISSUE-TRASH-RESTORE-CONFLICT-STATUS-01: Restore/Delete Precondition Conflicts Surface as 500

**Status**: 🟡 Confirmed, pending API polish (2026-07-14)
**Severity**: Low — no data-loss or GC safety issue, but expected restore/delete races are exposed as generic server errors
**Affected**: `PUT /api/v2.1/repos/deleted/:repo_id/` (`RestoreDeletedRepo`), `PUT /org/:org_id/admin/trash-libraries/:rid/` (`RestoreOrgTrashLibrary`)

#### Problem

`restoreDeletedLibrary()` now distinguishes normal precondition outcomes under the shared
hard-delete lease:

- the canonical row is already gone (`"library is pending permanent deletion"`),
- the canonical row is active (`"library is not in trash"`),
- or the restore lost the lease before its batch (`"lost library restore lock ..."`).

The HTTP handlers still flatten all of those errors to `500 {"error":"failed to restore library"}`.
That makes a normal restore/delete race look like a server fault and hides useful client-visible
state.

#### Fix direction

- Map `pending permanent deletion` / lost-lease contention to `409 Conflict`.
- Map `library is not in trash` to `400 Bad Request`.
- Preserve `500` only for real storage/DB failures.

#### Notes

This is intentionally tracked as API debt only. The underlying GC/restore safety is already
fail-closed because restore re-checks the canonical row under the same stale-aware hard-delete
lease before writing.

---

### ISSUE-GC-POLICY-INDEX-STALE-01: `gc_libraries_by_policy` Rows Leak on Direct Library Delete

**Status**: 🟡 Low — transient for new deletes (refined 2026-07-15)
**Severity**: Low for new deletes — no mis-processing; at most a short stale row until cascade runs
**Affected**: All direct-delete paths that go through `hardDeleteLibraryRowsFn`

#### Problem

The `gc_libraries_by_policy` index rows are maintained by the policy-setting endpoints (`library_settings.go` disabling `version_ttl`/`auto_delete`, `admin_libraries.go` history edit), by `rollbackNewLibrary` ([write_helpers.go:874-875](../internal/api/v2/write_helpers.go#L874)), and by the GC cascade's `HardDeleteLibrary` ([store_cassandra.go:3911-3912](../internal/gc/store_cassandra.go#L3911)). The confirmed gap is that the shared direct hard-delete helper `hardDeleteLibraryRowsFn` ([library_delete_helpers.go:52-79](../internal/api/v2/library_delete_helpers.go#L52)) does **not** include the `AddDeleteLibraryPolicyQuery` deletes in its synchronous batch.

Since PR #129, every wired permanent-delete path enqueues the durable `library_cascade`, and `HardDeleteLibrary` removes both policy index rows. For **new deletes** this is at most a transient stale row between the API batch and the cascade — not a permanent leak. Scanner Phase 5/6 re-read the `libraries` row per policy entry and `continue` on `ErrNotFound`, so stale rows are skipped, not mis-processed. Historical accumulation matters only on **brownfield** clusters that existed before the cascade fix.

#### Fix Direction (branch 2 — optional polish)

Fold both `AddDeleteLibraryPolicyQuery` calls (version_ttl + auto_delete) into the `hardDeleteLibraryRowsFn` batch so every direct-delete path clears the index synchronously. The cascade path already does this, so behavior converges.

#### Related Docs

- `docs/GC-DELETE-CLEANUP-INVESTIGATION.md` (F2/P3)

---

### ISSUE-GC-PUB-REF-ZERO-REF-01: `pub:` Refs Lack a Discoverable Zero-Ref Transition

**Status**: 🟡 Confirmed gap (2026-07-10); runtime-confirmed by E1-14 (2026-10-07), OPEN P2 follow-up
**Severity**: Medium — storage retention / GC discovery; E1-14 measured no incorrect-deletion (X1) path from the transition itself
**Scope**: PRE-GC (E1-16, 2026-10-10). W2-14 X1 safety is CLOSED-EVIDENCE. This transition/convergence residual previously blocked X1 through W2-14 and now blocks A1
**Affected**: publish-attempt block references (`pub:<attempt>`), block GC discovery

#### Problem

Provisional upload refs (`up:<op>`) register a durable expiry projection (`gc_provisional_block_refs` + `_by_day`); scanner Phase 0 walks it, waits for the ref's own Cassandra TTL to retire it (PR-8/F9 — the scanner no longer deletes the ref, since deleting a just-renewed one would unpin a live upload), then checks `BlockHasReferences` and promotes the block to `gc_block_candidates` if it hit zero. Publish-attempt refs (`pub:<attempt>`) do **not** register any such projection — `AddPublishAttemptReferences` ([block_references.go:339](../internal/db/block_references.go#L339)) only writes the ref with a 35-day Cassandra TTL.

Scenario: a block is kept alive solely by a `pub:` ref from a dead publish attempt. The `up:` ref expires at 2 days; the scanner sees the `pub:` still present and does not create a candidate. At 35 days Cassandra silently expires the `pub:` ref. Now the block is at zero refs, but nothing runs the zero-ref → `EnsureBlockGCCandidate` transition, because `scanOrphanedBlocks` ([scanner.go:335](../internal/gc/scanner.go#L335)) only walks candidates that already exist. The `blocks` row, mapping, and S3 object can be retained indefinitely.

#### Runtime confirmation — E1-14 (2026-10-07)

[E1-14](./E1-14-PUB-ZERO-REF-TRANSITION.md) reproduces the scenario with the
real Office/CreateFile writer and real Cassandra/SILO. The writer either stops
after `pub:` staging and before repair queueing (no repair), or leaves a durable
UNKNOWN repair. `up:` is retired by actual TTL and resolved by Phase 0; the final
`pub:` (verified to have no tracker/projection) is retired by actual TTL to
global EACH_QUORUM zero refs. Owned-scope Phase 0, Phase 1 and the productive
worker create no candidate, queue row, delete lifecycle or recovery root, and
P1/K1 stay retained. A disposable mutation that routes TTL-bound `pub:` writes
through the `up:` expiry-projection writer makes the same leg reach a productive
Phase 0 candidate. The missing projection is therefore the cause.

Classification: **P2 FOLLOW-UP**, storage-retention / GC-discovery gap. The
measured safety subset is NOT-X1-RED (`pub:` expiry alone cannot reach D).
The no-repair schedule (writer death between `pub:` staging and repair
queueing) retains the block indefinitely. Not fixed in E1-14. Once a projection
exists, the E1-11/E1-13 repair guard becomes load-bearing for this transition
and the E1-14 matrix plus guard-omission control must be re-run.

#### Fix Direction (branch 7)

Give `pub:` a discoverable expiry projection mirroring `up:`: register the projection when the ref is created (roll back the ref if the projection write fails); on expiry the scanner deletes the ref, checks `BlockHasReferences`, and creates a candidate if zero. The TTL stays as a backstop; the projection guarantees the transition. Ownership stays with the publish/expiry subsystem — the library cascade must never remove `pub:` refs (invariant #2).

#### Related Docs

- `docs/GC-DELETE-CLEANUP-INVESTIGATION.md` (P4)
- `docs/WEB-BLOCK-UPLOAD.md`

---

### ISSUE-GC-PHASE13-ERROR-VISIBILITY-01: Phase 13 Reports Success on Enqueue Failure

**Status**: 🟡 Confirmed gap (2026-07-10)
**Severity**: Medium — observability/health, not data loss (marker allows retry)
**Affected**: `Scanner.scanExpiredDeletedLibraries` (Phase 13)

#### Problem

`scanExpiredDeletedLibraries` ([scanner.go:1256-1319](../internal/gc/scanner.go#L1256)):

- on `EnqueueBatch` failure, logs the error but leaves `enqueued = 0` and returns `nil` ([scanner.go:1304-1315](../internal/gc/scanner.go#L1304));
- on per-library `PendingItemExists` failure, logs and `continue`s ([scanner.go:1267-1271](../internal/gc/scanner.go#L1267)).

The failures are logged, but not surfaced through the phase result or a dedicated failure metric. Because the phase returns `nil`, the overall scan cycle can appear successful even though no cascade reached the queue. The `deleted_libraries` marker allows a later retry, so this is not data loss, but health and metrics are misleading.

#### Fix Direction (branch 5)

Keep processing all markers, accumulate errors (`errors.Join`), return the joined error, add a dedicated failure metric, and keep separate `enqueued` / `skipped` / `failed` counters. Do not flag global success when delivery failed. Do not change eligibility rules in this branch.

#### Related Docs

- `docs/GC-DELETE-CLEANUP-INVESTIGATION.md` (P5)

---

### ISSUE-GC-EXISTENCE-CHECK-FAILOPEN-01: Scanner Existence Reads Can Fail Open

**Status**: ✅ Fixed (2026-07-10, branch `fix/gc-existence-check-failopen` / roadmap 1D)
**Severity**: High — a transient Cassandra read failure could lead to destructive work for a live library
**Affected**: `LibraryExists`, `GroupExists`, scanner Phases 3/4/9, commit/fs_object worker guard

#### Resolution

- `LibraryExists` and `GroupExists` now return `(false, nil)` **only** for `gocql.ErrNotFound` and propagate every other error ([store_cassandra.go:2357-2373](../internal/gc/store_cassandra.go#L2357), [store_cassandra.go:3314-3329](../internal/gc/store_cassandra.go#L3314)).
- Phase 9 no longer discards the `GroupExists` error — on error it skips the share (never deletes it) and records a phase error ([scanner.go:1073-1090](../internal/gc/scanner.go#L1073)).
- Phases 3/4 handle the `LibraryExists` error explicitly (skip + surface `phaseErr`) so a transient read cannot enqueue a live library's commits/fs_objects ([scanner.go:532-546](../internal/gc/scanner.go#L532), [scanner.go:624-638](../internal/gc/scanner.go#L624)).
- Phase 9 reads `OrgID` from each `shares_by_group` projection row and only falls back to the library→org projection when it is absent — surfacing that fallback failure instead of silently skipping a share. `ScanAllGroupShares` streams the projection directly in 256-row driver pages, so deleted-group partitions remain discoverable without materializing the full result; the Cassandra-wide scan is tracked separately as P8. The existence cache is a single-entry `(org_id, group_id)` "last partition" cache (O(1) memory) that opportunistically reuses the result (or error) for consecutive rows of the same partition; correctness does not depend on scan ordering, since a partition reappearing later just triggers another lookup ([scanner.go:1013-1094](../internal/gc/scanner.go#L1013)).
- The worker guard (`acquireLibraryDeleteGuard`) already propagated `LibraryExists` errors; with the store fix it is now genuinely fail-closed.
- Regression tests inject a transient existence error and assert no live commit/fs_object is enqueued and no valid group share is deleted, plus that Phase 9 cleans via the share `OrgID` without the library lookup: `TestScanner_ScanOrphanedGroupShares_FailClosedOnGroupExistsError`, `TestScanner_ScanOrphanedGroupShares_UsesShareOrgIDWithoutLibraryLookup`, `TestScanner_ScanOrphanedCommits_FailClosedOnLibraryExistsError`, `TestScanner_ScanOrphanedFSObjects_FailClosedOnLibraryExistsError` (`internal/gc/scanner_test.go`). MockStore gained `libraryExistsErr`/`groupExistsErr` injection hooks.

**Scope:** this closes the **transient-error** fail-open (P6a). The formerly separate P6b
execution-time gap is also fixed: queued Phase 3/4 orphan work is revalidated against the canonical
library under the existing library lock. P7 markerless discovery remains separate.

The original analysis is retained below for provenance.

#### Problem

`CassandraStore.LibraryExists` returns `(false, nil)` for **every** query error instead of only
`gocql.ErrNotFound` ([store_cassandra.go:2357-2373](../internal/gc/store_cassandra.go#L2357)).
Phases 3/4 enumerate live and deleted library IDs, call `LibraryExists`, and treat false as an
orphan. If that read fails transiently but the next org/representation read succeeds, the scanner
enqueues live commits/fs_objects with `RequiresLibraryDeletedCheck=false`
([scanner.go:531-579](../internal/gc/scanner.go#L531),
[scanner.go:623-671](../internal/gc/scanner.go#L623)). `acquireLibraryDeleteGuard` intentionally
bypasses all validation for such items ([worker.go:1731-1733](../internal/gc/worker.go#L1731)), so
the worker can delete live commits/fs_objects and release their legitimate `fs:` refs. The safe
physical block claim cannot compensate after GC itself removed the live reference.

`GroupExists` has the same error swallowing, and Phase 9 discards its error, so a transient read
failure can delete a still-valid group share
([store_cassandra.go:3314-3329](../internal/gc/store_cassandra.go#L3314),
[scanner.go:1073-1090](../internal/gc/scanner.go#L1073)).

#### Fix Direction (branch 1D — before reclamation optimizations)

- Return `false,nil` only for `gocql.ErrNotFound`; propagate every other error.
- Stop discarding `LibraryExists`/`GroupExists` errors in scanners; accumulate/return them.
- Add defense-in-depth guards to destructive orphan items so a live library is revalidated at
  execution time. Preserve the correct deletion identity when a marker exists.
- Add fault-injection tests: existence read fails, subsequent reads succeed, and no live
  commit/fs_object/ref/share is deleted.

---

### ISSUE-GC-ORPHAN-WORKER-REVALIDATION-01: Worker Canonical Revalidation for Orphan Commit/FS Work

**Status**: ✅ Fixed (2026-07-10) — Medium safety hardening / defense-in-depth
**Severity**: Medium — resolved
**Affected**: scanner Phases 3/4, queue/retry/DLQ persistence, commit/fs_object worker paths, library restore

#### Resolution (branch 1E)

- Added durable `LibraryGuardMode`: Phase 3/4 orphan work uses `canonical_absent`; normal
  cascade children use `deleted_at_identity`.
- Preserved the mode through queue, postpone, retry, DLQ, and DLQ requeue. For rolling upgrades,
  empty mode plus the legacy boolean `true` resolves to `deleted_at_identity`.
- The worker acquires the existing library hard-delete lock and performs the authoritative O(1)
  point read `libraries[(org_id, library_id)]`. Present means skip; read errors and unknown modes
  fail closed.
- Immediately before destructive commit/fs_object/reference mutations, the worker synchronously
  renews the owned token as a fence. Restore acquires and conditionally releases the same lock.
- Tests cover scanner→worker mode propagation, live canonical rows under projection drift, genuine
  absence, canonical read failure, historical delete markers, legacy mode compatibility, retry/DLQ
  round trips, and unknown-mode rejection.

#### Scope boundary

This closes P6b for work that reaches the queue. P7 remains open: artifact partitions absent from both
discovery indexes are not found by Phases 3/4, so no worker guard can run for undiscovered work.

#### Related Docs

- `docs/GC-DELETE-CLEANUP-INVESTIGATION.md` (P6b, Verdict 1, branch roadmap)
- `ISSUE-GC-EXISTENCE-CHECK-FAILOPEN-01` (P6a, fixed)
- `ISSUE-GC-ORPHAN-ARTIFACT-DISCOVERY-01` (P7 markerless discovery, still open)

---

### ISSUE-GC-CASCADE-COUNTER-ORDERING-01: Library Cascade Deleted the Storage Counter Before the Hard Delete

**Status**: ✅ Fixed (2026-07-11) — Medium safety hardening
**Severity**: Medium — resolved (storage under-count / quota-bypass window; content was never at risk)
**Affected**: `cascadeDeleteLibrary`, `processLibraryCascade`, `restoreDeletedLibrary`

#### Problem

`cascadeDeleteLibrary` used the order `enqueue children → DeleteLibraryStorageCounter →
HardDeleteLibrary` (inherited from `main`). While the canonical `libraries` row is still present
the library is restorable. If the worker crashed after the counter delete but before the hard
delete, and the lease later went stale/expired, `restoreDeletedLibrary` (stale-aware) could steal
the lease, observe a present-and-soft-deleted canonical row, and reactivate the library. Its
`AdjustAggregateStorageCounters(increment=true)` reads the per-library counter first and no-ops
when it is zero/absent — which it now is — so the org/user/platform aggregates were never
re-credited: an under-count and potential quota bypass. Library **content** was never at risk: the
child guard postpones every commit/fs_object while the canonical row exists, so nothing is purged
in this window.

#### Resolution

- Reordered to `enqueue → fence → HardDeleteLibrary → DeleteLibraryStorageCounter`. Once the
  canonical row is gone, restore reads `ErrNotFound` and refuses ("pending permanent deletion"),
  so no reactivation can observe the removed counter.
- The counter delete after the hard delete is pure reclamation and is not fenced (no live library
  left to corrupt). A crash between the two deletes leaves an orphaned counter, reclaimed on the
  next cascade pass: `processLibraryCascade` sees the marker gone, confirms the canonical row is
  absent via `CanonicalLibraryExists`, and idempotently deletes the counter
  (`reclaimHardDeletedLibraryStorageCounter`). A restored library (canonical present) is never
  touched.
- The `gc_library_cascade_deleted` audit is written immediately after the hard delete so the
  definitive event is recorded even if the reclamation must be retried.
- Tests: `TestWorker_ProcessLibraryCascade_HardDeletePrecedesCounterCleanup`,
  `..._CounterFailureReclaimedOnRetry`, `..._RestoredLibraryCounterNotReclaimed`.

#### Scope of the reclamation (important)

The **reordering** — the actual safety fix that closes the restore/under-count window — lives in the
shared `cascadeDeleteLibrary`, so it protects **both** callers: `processLibraryCascade` and
`processOrgCascade`. Once the canonical row is gone, restore refuses regardless of which cascade
removed it.

The **auto-reclamation** of a counter orphaned by a crash between the hard delete and the counter
cleanup is only wired into `processLibraryCascade` (its queue item carries the `libraryID`, so a
retry re-runs against the same library). `processOrgCascade` discovers libraries via
`ListLibrariesForOrg`, which reads the canonical `libraries` table; a library that was already
hard-deleted is gone from that list, so an org-cascade retry cannot re-find it to reclaim its
counter. See ISSUE-GC-ORG-CASCADE-COUNTER-LEAK-01.

---

### ISSUE-GC-ORG-CASCADE-COUNTER-LEAK-01: Org Cascade Can Leak an Inert Storage-Counter Row

**Status**: 🟡 Confirmed gap — Low (storage hygiene; no accounting/data impact)
**Severity**: Low — a dead `lib:<org>:<library>` row for a fully-deleted org, never read again
**Affected**: `processOrgCascade` → `cascadeDeleteLibrary` (counter reclamation only)

#### Problem

`cascadeDeleteLibrary` deletes the per-library storage counter after `HardDeleteLibrary`. If that
counter delete fails (or the worker crashes) after the hard delete during an **org** cascade, the
org-cascade retry re-lists libraries via `ListLibrariesForOrg` (canonical table) and no longer sees
the hard-deleted library, so `reclaimHardDeletedLibraryStorageCounter` — which only runs in
`processLibraryCascade` — is never invoked for it. The `lib:<org>:<library>` counter row is retained.

#### Why it is Low

The org/user/platform aggregates are adjusted at **soft-delete** time, independently of the lib
counter (`storage.go`: *"Aggregate scopes were already adjusted by a prior soft-delete"*). No code
path sums or scans `lib:*` counters into an aggregate — the only readers are per-library
soft-delete/restore/sync, all impossible once the library (and, here, the entire org) is
hard-deleted. The leaked row is therefore inert: it never revives the library, never affects any
live counter, and never causes an under- or over-count. It is a dead row occupying a little
Cassandra space.

#### Future fix (if this is ever worth closing)

Make the counter cleanup **durable and parent-independent**: enqueue an `ItemLibraryCounterCleanup`
item (carrying `org_id` + `library_id` + `identity_at`) in `cascadeDeleteLibrary` before the hard
delete. On processing: canonical present → skip (complete); canonical absent → idempotent
`DeleteLibraryStorageCounter`. That single durable mechanism would cover both cascade callers and
survive a full process crash, replacing both the inline delete and the library-cascade-only
reclamation. Deferred because it adds a new safety-critical queue-item type and a queue item per
library delete to clean up an otherwise inert row.

---

### ISSUE-GC-LEGACY-ORPHAN-UNGUARDED-01: Pre-Migration-011 Orphan Rows Run Without the Canonical Guard

**Status**: 🟡 Confirmed gap — Low (not a regression; pre-production has no such backlog)
**Severity**: Low — bounded to clusters that already ran an older GC binary in production
**Affected**: `gc_queue` / `gc_failed_items` rows enqueued before migration 011, worker guard path

#### Problem

The pre-011 scanner enqueued Phase 3/4 orphan commits/fs_objects with
`requires_library_deleted_check=false` and no `library_guard_mode`. After the upgrade those rows
hydrate as `LibraryGuardNone`, so the new worker deletes them **without** acquiring the library
lock, without the canonical point-read, and without fencing — exactly as `main` did (orphans were
purged with no revalidation there). This is therefore **not a regression**, but it means P6b's
execution-time canonical guard does not retroactively cover work that was already queued. A
lingering pre-011 pending marker can also make Phase 3/4 skip re-enqueuing a correctly-stamped
`canonical_absent` row for the same artifact.

A blanket `false → canonical_absent` backfill is unsafe: other legitimate producers (e.g.
`EnqueueCommits` used by `CleanRepoTrash`) enqueue commit work for **live** libraries where
canonical presence is expected.

#### Why it is Low here

Per project posture (pre-production, empty server, no legacy-data preservation) there is no
production orphan backlog to inherit, and the stop-the-world GC upgrade (see `docs/DEPLOY.md`)
plus a queue/DLQ drain removes any transient rows. Re-enabling remains prohibited
while X1 or PRE-GC/A1 is open; the backlog preflight is an additional activation gate.

#### Options if a real backlog ever exists

- After X1 closes, a fail-closed preflight that also refuses activation while commit/fs_object rows exist with
  `library_guard_mode IS NULL AND requires_library_deleted_check=false`.
- A `legacy_unclassified` mode that is quarantined (never auto-executed) for operator triage.
- A `work_source` / `queue_protocol_version` column so this ambiguity cannot recur.

---

### ISSUE-GC-ORG-CASCADE-REMARK-01: Org Cascade Re-Soft-Deletes a Library on Marker/Canonical Drift

**Status**: 🟡 Confirmed, defense-in-depth — Low (precondition unreachable under normal ops)
**Severity**: Low — storage double-decrement (clamped, reconcilable), only under corruption
**Affected**: `processOrgCascade`

#### Problem

`processOrgCascade` now decides on the `deleted_libraries` marker: marker absent + canonical
present → `SoftDeleteLibrary`. In a drift state where `libraries.deleted_at = T1` but the marker is
missing, it re-runs the full soft delete — re-stamping `deleted_at = T2`, changing the delete
identity, and re-subtracting the library's bytes from the aggregates. Because the per-library
counter is not cleared by soft delete, the second `AdjustAggregateStorageCounters(false)` reads the
same bytes and subtracts again (clamped to non-negative). `main` used `lib.DeletedAt` directly and
did not re-soft-delete, so this is a behavioral change in that state.

#### Why it is Low

Every path that writes `libraries.deleted_at` also writes the `deleted_libraries` marker in the
same `LoggedBatch` (`softDeleteLibrary`, GC `SoftDeleteLibrary`), and restore/hard-delete clear both
atomically. Cassandra logged batches are atomic, so the marker/canonical pair does not drift under
normal operation; the double-decrement requires genuine corruption.

#### Suggested hardening

Add an `EnsureDeletedLibraryMarker` helper that, when the canonical row is already soft-deleted
(`deleted_at != null`) but the marker is missing, reconstructs only the `deleted_libraries` row
from the existing canonical `deleted_at` / storage class / representation, **without** re-running
counter adjustments. Reserve the full `SoftDeleteLibrary` for a canonical row that is still active.

---

### ISSUE-GC-GROUP-SHARE-DISCOVERY-SCAN-01: Phase 9 Uses a Global `shares_by_group` Scan

**Status**: 🟠 Pending (2026-07-10) — provisional streaming mitigation merged with P6a
**Severity**: Medium — Cassandra performance and operability at scale
**Affected**: `ScanAllGroupShares`, scanner Phase 9

#### Current bounded mitigation

Phase 9 must discover `shares_by_group` partitions after their canonical group row is gone. The
old groups-driven N+1 could not do that. The immediate implementation therefore scans the share
projection directly, but processes it as a context-aware stream with a 256-row driver page size:
rows are handled immediately, cancellation can interrupt the query, and a late-page error is
surfaced by the phase. Process memory is bounded to the driver page plus a single-entry
`(org_id, group_id)` existence cache (O(1)) that opportunistically reuses the result (or error)
for consecutive rows of the same partition — the group-existence dedup does not grow with the
number of distinct groups, and correctness does not depend on scan ordering.

This does **not** make the Cassandra read scalable: without a partition key, every Phase 9 cycle
still reads the global table. Treat the stream as a correctness-first bridge for controlled current
volumes, not the final discovery architecture.

#### Required future design (roadmap 1F)

Add a bucketed active-partition projection such as `gc_group_share_partitions`, keyed by a fixed
bucket and `(org_id, group_id)`. Register the partition when creating a group share; remove it when
the partition becomes empty; scan buckets and share partitions with explicit cursors/page bounds;
and provide reconcile/backfill for projection drift. Preserve direct orphan discovery after the
`groups` row is deleted. Do not regress to groups-first enumeration or a global in-memory result.

#### Verification still needed for 1F

- Multi-page/bucket traversal and cursor resume.
- Cancellation and partial-page failure without losing durable progress.
- Reconcile of missing/stale partition-index rows.
- Load test on a representative multi-node Cassandra cluster.

---

### ISSUE-GC-ORPHAN-ARTIFACT-DISCOVERY-01: Markerless Commit/FS Partitions Are Invisible

**Status**: 🟡 Confirmed gap (2026-07-10) — reachable on a fresh cluster via terminal child-work loss; not a launch blocker, but **not** greenfield-exempt
**Severity**: Medium — markerless artifacts can remain indefinitely and evade Phases 3/4
**Affected**: `ListDistinctCommitLibraries`, `ListDistinctFSObjectLibraries`, scanner Phases 3/4

#### Problem

Despite their names, `ListDistinctCommitLibraries` and `ListDistinctFSObjectLibraries` both call
`listGCArtifactLibraries`, which enumerates only `libraries_by_id` plus `deleted_libraries`
([store_cassandra.go:2314-2355](../internal/gc/store_cassandra.go#L2314)). It never enumerates
library IDs from surviving commit/fs_object partitions or a dedicated discovery projection.

Once both library indexes/markers are gone, remaining commits/fs_objects are invisible to the
orphan phases. This directly matches the **audit dev-cluster snapshot** (`libraries=0`,
`deleted_libraries=0`, but `commits=4`) — residue dominated by integration-test drift, not the
current live delete path (~50 GB exercise left zero content residue).

**This is not confined to brownfield clusters.** The cascade enqueues its children
([worker.go:1325](../internal/gc/worker.go#L1325)) *before* `HardDeleteLibrary` drops the
canonical row and the marker ([worker.go:1338](../internal/gc/worker.go#L1338)) — deliberately,
so a concurrent restore cannot resurrect a partially-purged library. The consequence is that from
that point on, the children are the **only** thing that knows the artifacts exist. If a child
exhausts its retries it lands in `gc_failed_items`, and Phase 10's `DeleteExpiredFailedItem`
([store_cassandra.go:891-942](../internal/gc/store_cassandra.go#L891)) removes the DLQ row and
its pending projection in one batch. The commit/fs_object then survives with no library index, no
marker, and no queue/DLQ trace — exactly the P7 shape, on a **fresh** cluster.

Trigger set: **terminal child-work loss / DLQ expiry, corruption, or manual drift; never a normal
successful delete.** The failure mode is under-reclamation (storage retained), never incorrect
deletion of live content. It is not a launch blocker, but 8D is not made unnecessary by starting
from an empty keyspace.

#### Fix Direction (branch 8D)

- Add a durable, bounded discovery projection for artifact library IDs, **or** retain a durable
  library cleanup identity until every child **completes successfully**.
- **Contract — do not weaken this to "terminal state".** Retry exhaustion and DLQ expiry *are*
  terminal states, and they are exactly what produces P7 today: releasing the identity on those
  transitions rebuilds the same hole this branch exists to close. Retry exhaustion or DLQ expiry
  must **preserve or quarantine** the discovery identity and surface it for retry or operator
  intervention. It must **never** erase the last discoverable reference to surviving artifacts.
  "Successful completion of every child" is the only safe release condition.
- Brownfield: report markerless artifact partitions through the read-only reconciler (8A) first.
- Never solve this with a new full-table production scan or direct S3 deletion.

**Greenfield prod:** no *historical* markerless partitions exist, so nothing needs repairing
before launch — but the DLQ-expiry path above can create them on any cluster, so 8D remains
genuinely open rather than brownfield-only.

---

### ISSUE-GC-TEST-RESIDUE-01: Integration Suite Leaves DB + MinIO Residue

**Status**: 🟡 Test hygiene (2026-07-10)
**Severity**: Medium — pollutes the shared dev keyspace/bucket and produced the misleading "leak" snapshot in the audit
**Affected**: `internal/integration` test suite, shared Cassandra keyspace + MinIO bucket

#### Problem

**Historical non-production observation:** the integration suite ran against a **shared** dev keyspace and MinIO bucket with no global
truncate/teardown; cleanup is only by ephemeral library name. Confirmed residue/interference:

1. `TestMain` did not start/own the external `Service`, but the dev backend had GC enabled and
   normal tests call global `/api/v2.1/admin/gc/run`; therefore "nothing drains" is false.
2. `TestAdminIdentityProjectionRegression_HardDeleteOrganization` constructs a worker with
   `storage=nil` and calls global `ProcessOnce()`
   ([admin_identity_projection_regression_test.go:333-334](../internal/integration/admin_identity_projection_regression_test.go#L333)).
   It can process unrelated active orgs. For block items, nil storage removes Cassandra metadata
   without deleting S3, plausibly contributing to unmatched MinIO objects.
3. **`TestWebBlockUploadForeignPubRefNotPermanent`** injects a `pub:foreign-<ts>` ref with **no
   TTL and no `t.Cleanup`** and removes the session's `up:` ref
   ([web_block_upload_test.go:945-950](../internal/integration/web_block_upload_test.go#L945)).

Note (mixed, not fully clean): `gc_block_representation_resolve_test.go` intentionally builds unstamped legacy markers and removes them via fixture cleanup — legitimate. But `library_projection_regression_test.go` still has a **failure-only** restore (`if !t.Failed() { return }`) that re-inserts `deleted_libraries` **without** `block_representation_id` ([library_projection_regression_test.go:2090-2094](../internal/integration/library_projection_regression_test.go#L2090)), so a failing run can recreate an unstamped marker in the shared keyspace.

#### Fix Direction (branches 1A/1B/1C)

- **Branch 1A**: fix `TestWebBlockUploadForeignPubRefNotPermanent` — capture the referrer in a variable, add a `t.Cleanup` that deletes **all** rows/objects identified by the fixture's exact org/library/session/operation/block IDs (ref + block + mapping + S3 object **+ the provisional expiry projection** created by the real upload session), or insert the fake `pub:` with a TTL and still clean up; assert the fake ref is gone. Clean only exact-ID artifacts — never broad cleanup. Preserve the test's purpose (a foreign `pub:` must not count as a permanent ref).
- **Branch 1B** ✅ **DONE** *(root-caused and closed 2026-07-15)*

  **Why upload fixtures leaked, precisely.** `cleanupBlockUploadSessionForTest` deleted the
  session's `up:` reference with **raw CQL**. That drops the block's last referrer without going
  through the production release path, so nothing calls `EnsureBlockGCCandidate` — and
  `scanOrphanedBlocks` only walks candidates that already exist. The block was therefore
  **zero-ref and undiscoverable**: the only thing that ever rescued it was Phase 0 firing on the
  provisional expiry projection, **two days later** (the same shape as P4 for `pub:`). Every suite
  run left its uploaded blocks + S3 objects parked for 48h, which is a large part of the drift the
  audit had to untangle. Measured on a clean single-node docker stack (`configs/config.docker.yaml`:
  `scan_interval 30s`, `grace_period 1m`, `trash_retention_days 0`), `-run TestWebBlockUpload`:

  | | blocks | mappings | prov_refs | prov_by_day | MinIO |
  | --- | --- | --- | --- | --- | --- |
  | before fix, right after suite | 20 | 20 | 4 | 4 | 21 |
  | before fix, after GC drained | **4** | **4** | **4** | **4** | **5** ← stuck 2 days, GC idle |
  | after fix, right after suite | 15 | 15 | **0** | **0** | 16 |

  The `fs:` refs of committed files are *not* leaks: the library cascade releases them through the
  real GC path (15 of 15 released, 16 of 20 blocks reclaimed). Only the staged-but-uncommitted
  blocks were stranded.

  **Fix:** `releaseStagedBlockForTest` now drops the expiry projection and, when the block has no
  referrer left, its `blocks` row, forward mapping and S3 object. A block that still has a referrer
  (a committed `fs:`) belongs to a library and is left to that library's cascade. It hangs off
  `cleanupBlockUploadSessionForTest`, which `webCreateBlockSession` already registers for every
  session, so every upload fixture inherits it.

  **Unstamped-marker restore, also fixed.** `registerLibraryBaseRowRestoreCleanup` re-inserted
  `deleted_libraries` **without** `block_representation_id`, recreating the exact state PR #123
  fixed: Phase 13 then has to recover the representation from the `libraries` row and counts it as
  drift, and if that row is gone the library is stranded in trash forever. It also dropped
  `purge_requested_at` (migration 012), losing the permanent-delete signal. The snapshot now
  captures both and the restore writes them back. Verified on the real path by forcing the test to
  fail: the restored marker carries `block_representation_id=plain:v1` (previously empty) and a
  correctly-null `purge_requested_at`.

  **Corrupting fixtures must restore what teardown reads.** `TestWebBlockUploadReuploadRepairsMissingBlockSHA1`
  blanks `blocks.sha1` on purpose. On the happy path the re-upload repairs it, but this test is a
  **known local flaky** (returns 200 instead of the expected 409 on a 1-node stack), so it
  regularly aborts with the column still blank. `blocks.sha1` is the only way to name a block's
  forward `block_id_mappings` row — the reverse index was dropped in migration 006 — so teardown
  skipped the mapping and then deleted the block and its S3 object, stranding a mapping pointing at
  nothing. Reproduced by forcing the failure: `mappings=1` with everything else 0; with the restore
  in place, 0. The test now re-stamps the known sha1 in a `t.Cleanup` registered after the session's
  (LIFO ⇒ runs first), using `IF EXISTS` so it cannot resurrect a row GC already reclaimed.

  **Scope of the fix, measured honestly.** 1A/1B cover the `web_block_upload_test.go` fixtures.
  A **full** suite run (`go test -tags integration ./internal/integration/`, 236s, all green)
  drains from **122 blocks / 117 S3 objects** down to **2**, not 0:

  - `fs:<lib>:<path>` pinning one block whose library is gone from **both** `libraries` and
    `deleted_libraries`, with GC idle (`gc_queue=0`, `candidates=0`). This is the **F1/P7 shape and
    is eternal** — no phase can rediscover it. Most likely source is a fixture that removes library
    base rows with raw CQL (e.g. `removeLibraryBaseRowsForFallbackTest`), whose restore only fires
    on failure, so a *passing* run strands whatever the library still referenced.
  - `up:sync:<session>:<block>` from the sync-protocol fixtures — same class 1B fixed, different
    upload path. It carries a provisional expiry projection, so Phase 0 reclaims it in ~2 days.

  **1G — root-caused and fixed (2026-07-16).** Neither leftover was what the roadmap guessed
  (`removeLibraryBaseRowsForFallbackTest`); running each file in isolation cleared both `TestSync*`
  and `*Projection*`, so the suspects were wrong.

  - **The eternal `fs:` block was `TestZipDownloadFailsBeforeHeadersWhenLegacyMappingIsMissing`.**
    It deliberately corrupts two things and restored neither: it rewrites `fs_objects.block_ids`
    to the **legacy SHA-1** layout, and deletes the SHA-1→SHA-256 `block_id_mappings` row. But
    `block_references` is keyed by the canonical **SHA-256**, so the library cascade read SHA-1s,
    released references that do not exist, and left the real `fs:` ref in place — pinning the
    block with the library gone from both indexes, which no GC phase can rediscover (F1/P7 shape).
    The `audit_log` proves the cascade ran (`gc_library_cascade_deleted`) and the fs_object was
    processed (`Deleted fs_object 8ed27335…`) — the release simply targeted the wrong ids. Both
    corruptions are now undone in a `t.Cleanup` registered after the library's, so LIFO restores
    canonical ids before the cascade runs. Proven both ways on a clean stack: without the restore
    the ref sits unchanged for 210s with GC idle (`gc_queue=0`, `libraries=0`) — **eternal**; with
    it, the ref is released at ~90s and the block reclaimed by ~180s.
  - **The `up:sync:` provisional was `quotas_test.go`'s `uploadSyncBlockStatus`** (not the sync
    suite): it PUTs a block through the seafhttp sync path and never commits, so the handler's
    `up:sync:<repo>:<block>` pin and its expiry projection survive until Phase 0 fires two days
    later. Teardown now hangs off that shared helper. `prov` went 1 → 0.

  **Measurement correction:** earlier residue numbers in this file undercounted S3. The dev stack
  had **five** buckets (`sesamefs-blocks`, `-usa`, `-eu`, `-china`, `-archive`) and the script only
  counted `sesamefs-blocks`; libraries created with `storage_id: hot-s3-usa` (zip/region/history
  tests) write to `sesamefs-usa`, which had been accumulating blocks unseen across runs. Always
  count every bucket. **R23b added a sixth**, `sesamefs-legacy-blocks`: the legacy `hot` backend
  moved off `sesamefs-blocks` so one namespace no longer answers to two class names, so a library
  explicitly placed on class `hot` writes there.

  **Teardown contract — a missing `blocks` row means STOP.** `releaseStagedBlockForTest`
  deliberately does **not** delete the S3 object when the `blocks` row is gone. S3 keys are now
  org-scoped (`hashToKey` ⇒ `blocks/<org_id>/<h0:2>/<h2:4>/<hash>`), so cross-org deletion is no
  longer the hazard — P10 makes that impossible by construction. The reason to stop is different:
  without the `blocks` row the helper knows neither the authoritative `storage_class`/bucket (one of
  the six) the object lives in, nor can it prove the object belongs to *this* fixture. The zero-ref
  check only proves *this* org is finished with the reference; the `blocks` row is the fixture's only
  evidence that it materialized the object here. Deleting without it would mean removing a hash we
  cannot prove we created, from a bucket we are guessing. So it must fail closed rather than delete
  from S3 directly. If a future fixture needs "delete without metadata", it must opt in explicitly and
  prove: a test-exclusive hash, the right bucket, and that its own request could have physically
  created the object.

  **Still open (new, smaller):** one ~90-byte S3 object with **no `blocks` row** survives a run —
  an S3-only orphan that no GC phase can discover (blocks are found through candidates, and
  S3-orphan recovery only replays `gc_s3_orphans`). Not yet attributed to a test. Full-suite
  hygiene is therefore **not** yet at a zero delta across all five buckets.

  Do not run a global GC over the shared keyspace (invariants #5/#6).
- **Branch 1C** ✅ **DONE**: the last direct global `ProcessOnce(storage=nil)`
  (`admin_identity_projection_regression_test.go`, org-cascade hard-delete test) now calls
  `ProcessOrgOnce(ctx, orgUUID)`, matching the pattern the rest of `gc_integration_test.go`
  already used. `TestNoGlobalGCFanoutInIntegrationSuite` statically scans the package and fails if
  any test reintroduces `.ProcessOnce(`; it carries **no** build tag, so it runs in the normal
  `go test ./...` pass without Cassandra/MinIO. Admin GC triggers inventoried: `triggerGCWorker` /
  `triggerGCScanner` (~20 call sites) drive the **real** backend worker with **real** storage —
  globally noisy, but they cannot orphan S3 objects the way a `storage=nil` worker does, so
  baselining them stays with 1B rather than blocking 1C.

#### Related Docs

- `docs/GC-DELETE-CLEANUP-INVESTIGATION.md` (F3, Verdict 2)

---

### ISSUE-PUBLISH-REPAIR-TIMEOUT-CLEANUP-01: Publish repair treats lease expiry as cleanup authority

**Status**: ✅ Closed for the shared W2 repair authority defect (2026-09-06); broader R31 publication continuity remains open
**Severity**: High (P1) — R31 publication continuity across pre- and post-HEAD outcomes
**Affected**: `internal/api/v2/publish_repair.go`, published-block-reference repair, pending publication cleanup

#### Problem

Historically, the pre-CAS repair lease (`publishedBlockReferenceRepairPreCASLease`) postponed an unreachable repair while the lease was live and permitted cleanup after the lease expired. Lease expiry only says that the original repair window elapsed; it is not proof that a possibly-applied HEAD CAS did not publish. The same distinction covers the HEAD-CAS/lease race and the `confirmed-lost` versus `ErrLibraryHeadConflict` cleanup asymmetry. Timeout is not revocation.

Cleanup selected by elapsed time can therefore remove an attempt commit or reference while definitive HEAD/reconciliation evidence is unavailable. The race is reachable before HEAD CAS when a live writer outlives the repair lease, and after an ambiguous or applied HEAD outcome. It is not part of the W2 narrow block-liveness/exact-placement guarantee. Destructive GC remains disabled.

#### Scope / disposition

This branch fixes the shared published-block-reference repair used by the upload-link engine and `CreateFileFromBlocks`: lease expiry is no longer cleanup authority; UNKNOWN, failed, or otherwise non-reachable confirmation retains the durable repair and does not actively remove its artifacts; and definitive CAS losers are cleaned synchronously by the request that received the conflict. The durable repair row uses ordinary insert/delete mutations throughout; retry backoff is process-local advisory state, so restart may retry a retained row earlier without entering a mixed LWT lifecycle. The real Cassandra/MinIO gate covers shared-engine success, crash after applied HEAD, ambiguous applied/unknown outcomes, lease expiry, reachable ancestry, restart replay, a real synchronous loser path, and a writer paused before `CreateFileFromBlocks` HEAD while repair runs. The `pub:` reference still has a finite 35-day TTL and no discoverable zero-ref transition; that is the documented R31 follow-up in `ISSUE-GC-PUB-REF-ZERO-REF-01`, not silently closed here. Other publication funnels and the remaining R31 protocol work stay open; this does not add lifecycle or terminal-authority state, migrations, GC-worker changes, or destructive activation.

---

### ISSUE-PUBLISH-REPAIR-REACHABILITY-01: Repair HEAD reachability and ancestry are not bounded authority

**Status**: ✅ Closed 2026-09-10 for the shared published-block-reference repair classifier; broader W2/R31 convergence remains open
**Severity**: High (P1) — multi-DC publication-repair correctness and convergence
**Affected**: publish-repair HEAD lookup and commit ancestry walk

#### Problem

Historically, the repair path read HEAD through its ordinary read path and walked `parent_id` ancestry without a bounded, multi-DC authority proof. A stale or locally incomplete HEAD view, an unavailable datacenter, or a deep/malformed ancestry chain could misclassify publication state before HEAD CAS or after an ambiguous/applied HEAD outcome. This is a publication-repair concern identified in the #203 audit; it is not a reason to add SERIAL/EACH_QUORUM calls or ancestry scans to the W2 pre-HEAD hot path.

#### Scope / disposition

The shared post-HEAD repair cold path now has an explicit `REACHABLE` / `DEFINITELY_NOT_REACHABLE` / `UNKNOWN` classification. It requests the canonical org-scoped HEAD in the `SERIAL` observation domain, then validates at most 1024 commits sequentially with `EACH_QUORUM`, under one 30-second context. These consistency levels define the recovery observation; they do not create a durable global negative witness. Target-at-HEAD and target-as-ancestor are positive only after the corresponding commit row is observed. Missing rows, read errors, timeout/cancellation, cycles, malformed ancestry, natural genesis without the target, and bound exhaustion are `UNKNOWN`; they retain the durable repair and every artifact. HEAD is not reread after a positive result because reachability from a real observed HEAD is monotonic under the append-only parent protocol.

`DEFINITELY_NOT_REACHABLE` is represented but deliberately has no producer: `LOCAL_SERIAL` remains a supported multi-DC configuration and repair rows contain no durable known-loser witness, so the current model cannot prove a global negative safely. Settlement retains this class as well until a future protocol supplies that authority. The writer hot path is unchanged; worst-case classifier cost is one `SERIAL` HEAD read plus 1024 sequential `EACH_QUORUM` commit reads, capped at 30 seconds. The real 3-DC gate covers locally blind publication, later-HEAD ancestor recognition, delayed commit visibility, and one-DC-unavailable retention. OnlyOffice keeps its independent legacy boolean traversal and remains outside this closure. Discovery scaling, known-loser durability, Sync expired provenance, `pub:` zero-ref, other funnels, and overall R31 remain open. The library HEAD serial-domain contract is closed (`ISSUE-LIBRARY-HEAD-SERIAL-DOMAIN-01`, 2026-09-14).

**Extension (2026-09-07, W2 Sync `PutBlock` -> HEAD slice):** direct-HEAD repair rows are shared by every writer of the target `commit_id`. A readiness failure creates no durable repair row; after readiness succeeds and the shared row is queued, queue ambiguity, ambiguous-CAS, and divergent-CAS request-local outcomes retain it. Only positive settlement clears the row. Auto-merge uses a fresh UUID attempt ID, so its cleanup is structurally unique. The real residuals are retained bookkeeping rows after abandoned/ambiguous attempts and expired-provenance continuity before the readiness gate. This branch does not add durable known-loser authority or close R31.

### ISSUE-W2-SYNC-NOPUTBLOCK-PREHEAD-01: Sync can publish HEAD after GC commits D for a block without PutBlock provenance

**Status**: Resolved pre-HEAD (2026-10-01); RED on PR #244, ten safety regressions GREEN. Post-HEAD R31 remains OPEN
**Severity**: P1 — a supported Sync dedup/reuse flow can publish durable liveness for a condemned physical placement
**Affected**: W2-4, direct Sync HEAD promotion and auto-merge, `internal/api/sync.go`

#### Historical finding (PR #244)

Real `CheckBlocks` can resolve a web-uploaded SHA-1 alias and canonical SHA-256 block as already present. The Sync commit then receives file metadata through `RecvFS` without any Sync `PutBlock`. Its HEAD readiness scope gate finds no `up:sync:<repo>:<block>` and returns no placement to validate. Attempt-local `pub:` is staged before readiness; the durable repair row is queued after readiness. Once the web upload's TTL-bound `up:` has expired, a current GC worker can observe zero at EACH_QUORUM, prepare the exact orphan and commit D(P) before the repair appears. Sync subsequently queues repair and publishes HEAD, then promotes `fs:` for that same P. The final check only covers the empty PutBlock-provenanced subset, so it does not reject the committed or fully retired placement.

This is separate from expired Sync PutBlock provenance: W2-4 has no Sync PutBlock at all. It also shows that `CheckBlocks` success is only an observation of existence at check time, not publication continuity.

#### Evidence and disposition

`internal/integration/w2_sync_no_putblock_test.go` executes ten real Cassandra/MinIO legs: direct and auto-merge, each with writer-first, GC before attempt staging, GC between initial readiness and durable repair, repair-first, and fully retired P before HEAD. On PR #244, the normal SHA-1 dedup path and a canonical SHA-256 path after physical retirement both reproduced D(P)+HEAD. The repair-first control prevents GC from establishing zero. PR #244 used `SESAMEFS_W24_ASSERT_SAFETY=1` for the historical RED. Safety is now unconditional; `scripts/w2-sync-no-putblock-validation.sh` creates a unique Docker project, requires ten GREEN legs, and checks negative evidence gates.

The fix captures every added canonical block's exact placement independently of upload provenance and revalidates it after the existing durable repair is acquired. Renewal remains scoped to observed Sync upload pins; dedup never manufactures one. The ten legs now require GC-first rejection, unchanged HEAD, no permanent reference, and ownership-correct cleanup; writer-first/repair-first controls succeed. See [the W2-4 investigation](W2-4-SYNC-NO-PUTBLOCK.md) and [implementation plan](W2-4-SYNC-PUBLICATION-FIX-PLAN.md). W2-4 is closed only for the demonstrated pre-HEAD gap; W2-5, post-HEAD R31, and GC activation remain separate.

### ISSUE-SYNC-PUTBLOCK-EXPIRED-PROVENANCE-01: Expired PutBlock provenance is outside the scoped HEAD guarantee

**Status**: ✅ Closed for X1 (E1-16, 2026-10-10). Both Sync HEAD paths validate every added block after durable repair regardless of provenance or pod, so expired provenance takes the W2-4 RED→GREEN path. W2-3 is CLOSED-EVIDENCE; post-HEAD runs through the shared repair guard. History: historical pre-HEAD scope bypass superseded by the W2-4 all-block check; W2-3 remained OPEN for its separate evidence/settlement requirements
**Severity**: High (P1) - Sync liveness continuity across provisional TTL expiry
**Affected**: Sync PutBlock -> HEAD when the deterministic `up:sync:<repo>:<block>` row expires before the pre-HEAD readiness gate

#### Problem

The historical scoped W2 path renewed and fenced only when an existing own-liveness row was observable. W2-4 now validates every added block independently of provenance, so expired provenance cannot bypass exact-P validation. The absent upload row is not recreated from a commit delta.

#### Scope / disposition

W2-3 is not declared closed by no-PutBlock evidence. Its independent expiry/retry evidence and post-HEAD R31 requirements remain separate. Do not fabricate provenance from a commit delta, clear a shared repair row from expiry, or weaken the fail-closed ownership rules.

### ISSUE-SYNC-PUTBLOCK-CROSS-DC-PROVENANCE-VISIBILITY-01: PutBlock provenance may be invisible at a receiving DC

**Status**: ✅ Resolved (2026-09-08) for the scope-gate decision; real 3-DC evidence attached
**Severity**: High (P1) - cross-DC visibility of scoped Sync provenance
**Affected**: `syncBlockHasOwnLivenessProvenanceFn` (`internal/api/sync.go`), `BlockReferenceExistsLocalQuorum`/`BlockReferenceExistsEachQuorum` (`internal/db/block_references.go`)

#### Problem

`PutBlock` writes the deterministic `up:sync:<repo>:<block>` provenance in the datacenter handling the upload. An immediate HEAD retry handled by another datacenter can read with `LOCAL_QUORUM` before that reference is visible there. The receiving node then observes no provenance and deliberately skips renewal and exact-placement validation, even though the PutBlock happened.

This is distinct from `ISSUE-GC-CROSS-DC-REFERENCE-VISIBILITY-01`: that X2 issue covers destructive GC authorization and is closed. This issue concerns writer-side provenance observation before HEAD.

#### Fix

`syncBlockHasOwnLivenessProvenanceFn` now tries `BlockReferenceExistsLocalQuorum` first (same-DC, zero added WAN); a hit or a local read error both settle the answer immediately, with no fallback attempted. Only a clean local "not found" -- ambiguous between genuine absence and cross-DC replication not yet converged -- escalates to `BlockReferenceExistsEachQuorum`, the exact-referrer analog of the existing `BlockHasReferencesGlobal` (same quorum-intersection argument: a write acknowledged at `LOCAL_QUORUM` in any datacenter necessarily intersects an `EACH_QUORUM` read once it has landed on a quorum of that datacenter's own replicas, independent of cross-DC async replication). A global miss means no currently observable live provenance -- it does not prove PutBlock never happened, only that no live `up:` reference is visible right now (see `ISSUE-SYNC-PUTBLOCK-EXPIRED-PROVENANCE-01` for provenance past its 48h TTL, indistinguishable from never-existed) -- and the block is left untouched (never fabricates a reference from the commit delta); a global error fails closed exactly like a local error. No change to `ValidateBorrowedFSPublicationAuthority`, no `SERIAL`/Paxos added, no GC change.

Declared and frozen in `docs/R3-LIVENESS-CONTINUITY.md`'s "Declared exception: Sync PutBlock-provenanced readiness" / "Cross-DC provenance fallback: cost and availability" sections, and in `TestR3SyncPutBlockReadinessDeclaredExceptionIsFrozen`'s allow-list.

**Bounded fail-fast**: `syncCommitProvenancedBlockIDs` (the fan-out that calls the scope gate once per distinct block) uses `errgroup.WithContext` instead of a plain `errgroup.Group`; each goroutine checks `ctx.Done()` before issuing its own check, so the first fatal error (local or global) stops admitting additional block-level provenance checks (the admission loop keeps creating goroutines for later blocks, but each returns immediately via the cancelled context instead of making its own external call). A degraded/unreachable datacenter therefore fails a large commit's readiness after at most `syncCommitBlockPlacementConcurrency` (20) block-level provenance checks admitted/in flight -- each of which may itself issue both a `LOCAL_QUORUM` and an `EACH_QUORUM` read, and a check already past its own cancellation gate is not cancelled mid-flight -- not after attempting the same failing lookup for every one of up to `N` blocks.

#### Evidence

- Unit: `internal/api/sync_w2_putblock_xdc_provenance_test.go` pins the routing (local hit/error never calls the fallback; local miss + global hit recovers; local miss + global miss stays unprovenanced; local miss + global error fails closed) and proves bounded fail-fast (`TestSyncCommitProvenancedBlockIDs_GlobalFailureStopsAdditionalDBProbes`: 200 blocks, all clean local misses, a failing global fallback -- asserts at most the concurrency bound (20), not a multiple of it as an earlier version allowed; observed exactly 20 in practice, though only the upper bound is a real runtime guarantee; confirmed 200/200 without the `errgroup.WithContext` fix).
- `internal/db/block_references_test.go`: `TestSyncBlockReferenceCrossDCFallbackConsistencyIsEachQuorum` pins the named `SyncBlockReferenceCrossDCFallbackConsistency` constant to `gocql.EachQuorum` at unit speed, independent of the real 3-DC leg.
- Mutation: `scripts/w2-sync-putblock-head-mutation-validation.sh` M12 bypasses the fallback (RED), M13 removes the fan-out cancellation (RED against the bounded fail-fast test), M14 weakens the named consistency constant to `LOCAL_QUORUM` (RED against the pin test), M15 rebinds `BlockReferenceExistsEachQuorum`'s call site away from the named consistency constant (RED against `TestBlockReferenceExistsEachQuorumBindsTheNamedConsistencyConstant`), M16 Sync success deletes repair-owned `pub:` on the hot path (RED against `TestClearSyncCommitBlockReferenceRepairsDoesNotDeletePublishAttemptRefs`) -- 16/16 mutations produce the expected RED.
- Real 3-DC (`scripts/w2-sync-putblock-xdc-provenance-validation.sh`, `internal/integration/sync_w2_putblock_xdc_provenance_multidc_test.go`): PutBlock simulated in `dc-eu` while `dc-na`/`dc-asia` are stopped; `dc-na` restarted and queried immediately, before any hint/repair delivery. Confirmed against the real fixture: **without** the fallback the leg is RED (`found=false`, the exact bug); **with** it, GREEN (`found=true`). A further leg stops `dc-asia` alone and confirms the fallback fails closed (bounded error, not a hang, not a silent absence) rather than treating "one DC down" as ordinary absence.

#### Why genuinely-unprovenanced blocks now share fate with the cross-DC fallback

A local `LOCAL_QUORUM` miss is observationally identical between two cases the scope gate cannot tell apart without the fallback itself: (A) PutBlock happened in another datacenter and has not yet replicated here, and (B) this block genuinely has no PutBlock provenance anywhere (client-side dedup via `CheckBlocks`, or reuse from another commit -- the "Sync commit whose block had no associated PutBlock" `UNKNOWN` funnel row, which this issue does not touch or reclassify). Recovering case A requires escalating to `BlockReferenceExistsEachQuorum` on every clean local miss, which necessarily includes every case-B block too. Before this fix, a case-B block resolved via `LOCAL_QUORUM` alone with zero cross-DC dependency; after it, the same block now depends on `EACH_QUORUM` succeeding, which fails if any datacenter is unreachable. This is a real, previously-absent availability dependency for commits made of dedup-only blocks, not merely an added-latency number -- see the one-DC-down finding above.

**This is not fixable within this issue's scope without either weakening correctness or expanding scope, and neither is done here:**

- **A plain `QUORUM` (cluster-wide, not per-DC) fallback would not be correct for this topology.** With one replica per datacenter (the deployed `dc-na:1, dc-eu:1, dc-asia:1` shape), a cluster-wide `QUORUM` read of 2-of-3 replicas can be satisfied by two datacenters that never received the write, silently returning a false negative -- exactly the bug this issue closes, reintroduced. `EACH_QUORUM` is not an arbitrary stronger choice; it is the minimum consistency that provably intersects a `LOCAL_QUORUM` write made in an arbitrary other datacenter, because it is the only level that forces contact with every datacenter.
- **Strengthening the `up:sync:<repo>:<block>` *write* itself (e.g., to `EACH_QUORUM` or `QUORUM`) would trade a narrow, conditional cost for a broad, unconditional one.** The write happens on every `PutBlock` call, including the overwhelming majority that never need cross-DC recovery; moving the cost there would add cross-DC WAN latency to every block upload instead of only to the ambiguous read path. That is a larger regression than the one being avoided, not a fix for it.
- **Classifying "genuinely unprovenanced" without a global read requires new state or a protocol change** (for example, a client-supplied hint distinguishing "I PutBlock'd this" from "the server told me this already exists"), which is explicitly out of scope for this focused slice per its own invariants (no client hints, no protocol change, no expansion into full W2/R31 classification).

**What was done instead**: the fan-out that calls the scope gate is now bounded fail-fast (see above), so the *blast radius* of this dependency during a real datacenter outage is capped at at most `syncCommitBlockPlacementConcurrency` (20) block-level provenance checks admitted per commit, not up to `N`. The trade-off itself -- dedup-only commits now share the cross-DC fallback's availability domain -- is accepted and documented here as a deliberate decision, not silently absorbed into a latency table. It is the direct, unavoidable consequence of closing this P1 using only the signal already available (a referrer-scoped existence check) without a wider redesign.

#### Cost/availability characterization (single-DC dev stack unless noted)

`TestW2SyncXDCProvenanceCostCharacterization` (`SESAMEFS_MEASURE_W2_SYNC_XDC_COST=1`) measured wall-clock latency for the real scope-gate function under the production concurrency bound (20), N=1/10/100/1000, all-local-hit / one-local-miss / all-unprovenanced / mixed-half:

| N | all-hit | one-miss | all-unprovenanced | mixed-half |
|---|---|---|---|---|
| 1 | 6.1ms | 5.2ms | 4.1ms | 3.4ms |
| 10 | 2.4ms | 3.8ms | 3.4ms | 3.7ms |
| 100 | 9.3ms | 15.7ms | 19.2ms | 23.4ms |
| 1000 | 79.2ms | 60.4ms | 98.6ms | 212.5ms |

**Caveat for this table**: this stack's keyspace has one datacenter, so `EACH_QUORUM` resolves to the same replica set `LOCAL_QUORUM` would -- there is no real WAN hop here. This is a low-latency local reference measurement (it isolates the added-round-trip cost in a low-latency environment), not a demonstrated lower bound on any production topology's latency, and an exact measurement of added query count (1 extra read per local miss, exactly as designed); single-host noise (visible in the non-monotonic mixed-half row) means these numbers should not be read as precise ms-level guarantees.

**Real cross-DC measurement, scope-gate only (the scenario the single-DC table above structurally cannot produce)**: `TestW2SyncXDCAllCrossDCHitCostAtN3DC`, run against the real 3-DC fixture (`scripts/w2-sync-putblock-xdc-provenance-validation.sh`), measured **only** `syncBlockHasOwnLivenessProvenanceFn` itself (the `LOCAL_QUORUM`-then-`EACH_QUORUM` provenance decision) for a set of blocks confirmed genuinely cross-DC-hit -- every block recovered via the `EACH_QUORUM` fallback from `dc-na`, at the same concurrency bound (20):

| N | all-cross-DC-hit, scope-gate only (real 3-DC, one clean sample) |
|---|---|
| 1 | 17.1ms |
| 10 | 38.9ms |
| 100 | 291.6ms |
| 1000 | 1.67s (observed range across runs: 1.67s-8.1s -- see note below) |

This is the corrected methodology, using **disjoint, non-overlapping block sets per N** and an explicit `BlockReferenceExistsLocalQuorum` precondition check immediately before timing each scenario, confirming every block in it is still genuinely locally-blind. An earlier version of this test reused overlapping slices (N=10 as blocks[0:10], N=100 as blocks[0:100], etc.): an earlier scenario's `EACH_QUORUM` query against a shared block could silently heal dc-na's local copy of it -- most plausibly Cassandra's own read-repair reconciling a replica it noticed disagreed, though this was not directly observed and no specific mechanism is asserted here -- before a later, larger-N scenario queried the same block again, a real precondition violation on its own, independent of what it did to the timing. That version measured 3.4-5.0s at N=1000 across two runs; the disjoint-set version's first clean sample measured 1.67s. **The old methodology is invalidated by the precondition violation alone, and its number is retired rather than explained**: a block healed this way would skip the `EACH_QUORUM` round trip entirely on a later scenario, which should have made the contaminated run *faster*, not slower, than the clean one -- the reverse of what was actually measured. What produced the 3.4-5.0s figure (run-to-run variance, host/scheduler load, warmup, or something else) is not established and is not claimed here.

**The disjoint-set methodology is sound; a single one of its numbers is not a latency bound.** Five further runs of the real 3-DC script (`scripts/w2-sync-putblock-xdc-provenance-validation.sh`, one full pass per run) observed N=1000 scope-gate cost ranging from 1.67s up to 8.1s on the same fixture and code, sometimes immediately after a Cassandra restart and sometimes well after the cluster had been serving other `EACH_QUORUM` traffic for tens of seconds. That is high, real variance intrinsic to a resource-constrained, single-Docker-host 3-node cluster under sequential load -- not a bug, and not evidence the disjoint-set fix is wrong (every sample used the same locally-blind, non-overlapping precondition), but it does mean **no single number in this table, including 1.67s, should be read as "the" cost of a large recovered commit.** Treat the table as one clean sample of query-count-bounded behavior -- exactly N fallback reads, at most 20 concurrently in flight at any moment (a continuously-refilled pool via `errgroup.SetLimit`, not discrete synchronized batches of 20) -- not a wall-clock guarantee. Wall-clock scales roughly with `N/20` when per-read latency is comparable across reads, which is a real but approximate consequence of the concurrency bound, not a separate exact formula. This environment is container-to-container on one Docker host, not true production WAN: it confirms real Cassandra multi-DC consistency semantics (`EACH_QUORUM` genuinely crossing per-DC replica sets) and the query-count formula, not a production latency prediction.

**This is the scope-gate's cost, not the full end-to-end HEAD-readiness cost for a recovered block.** A block that resolves `found=true` via the `EACH_QUORUM` fallback still goes on to run the rest of #206's pre-HEAD pipeline exactly as a local hit would: `resolveSyncCommitBlockPlacements` (`ProbeBlockReuse`, up to 3 reads), `ensureSyncCommitBlockOwnLiveness` (1 read + 1 logged-batch write), and `validateSyncCommitBlockPublicationFences` (`ValidateBorrowedFSPublicationAuthority`, 2 reads) -- see "Declared exception: Sync PutBlock-provenanced readiness" in `docs/R3-LIVENESS-CONTINUITY.md` for the exact per-primitive breakdown. None of those three stages is new to #210 or newly WAN-bound by it: all are `LOCAL_QUORUM` or session-inherited (production: `LOCAL_QUORUM`), exactly as they already were for a local hit under #206. The genuinely-unprovenanced case never reaches them at all (the block is correctly left untouched), so it pays *only* the scope-gate number above; the genuine-recovery case pays the scope-gate number above *plus* that bounded, local-only, non-WAN pipeline cost. The table above is therefore the right number for "how expensive is recovering cross-DC provenance" and the measured scope-gate component of the total HEAD-readiness cost for a commit containing a recovered block -- not an exact end-to-end figure (which would require seeding real block metadata, a `blocks` row, not only the `up:` reference, to measure directly, and was not done here) and not a lower bound for any other run or topology, given the 1.67s-8.1s variance this same fixture already showed for the scope-gate number alone. The additional pipeline cost on top of it is bounded, local, and already characterized by #206.

**Correction to a claim this section originally made**: the all-genuinely-unprovenanced (dedup) case does **not** resolve at the cheap single-DC-table speed in a real multi-DC deployment. `EACH_QUORUM` must contact a quorum of replicas in *every* datacenter to determine "not found" just as much as it does to determine "found" -- absence is not cheaper to prove than presence at this consistency level. What is exactly known: an all-dedup commit pays the identical fallback query count and the identical cross-DC availability dependency as the all-cross-DC-hit measurement above -- N `EACH_QUORUM` reads, at most 20 concurrently in flight, no pathological multiplication beyond that bound. What is **not** separately measured on the real 3-DC fixture is the all-dedup case's own wall-clock latency: Cassandra may do different internal work resolving "every replica agrees absent" versus "replicas disagree, one holds the row," so the all-cross-DC-hit numbers above should not be read as a direct measurement of the dedup case, only as the best available proxy given the identical query-count and availability profile. The single-DC table's only valid use is measuring *query count* and confirming there is no pathological multiplication of round trips.

**One-DC-down is an availability-domain change, not merely a latency number**: with a datacenter unreachable, `TestW2SyncXDCFallbackFailsClosedWhenADatacenterIsDown3DC` confirms the fallback fails closed (bounded error) for any block that reaches it -- including a genuinely unprovenanced one that would otherwise have cost nothing. A Sync commit with dedup-only blocks and zero real PutBlock provenance now depends on every datacenter's availability to publish HEAD, where before it did not. This is the real trade-off decision, not a footnote.

#### Decision

Ship the fix as scoped, with the real-latency number taken seriously rather than minimized: correctness is unconditional (the fallback is the only way to close this P1), the query-count formula is exact and bounded (`1` extra `EACH_QUORUM` read per local miss, never on a hit, and the fan-out fix above bounds the failure case to at most `syncCommitBlockPlacementConcurrency`, 20, probes in flight), but the real 3-DC measurement shows this is not free at scale -- observed between roughly 1.7 and 8 seconds at N=1000 across separate runs of the same fixture and code (scope-gate only; see the pipeline-cost note above for what a genuine recovery additionally pays, and the cost table above for why no single number in that range is "the" cost), on a modest container-to-container 3-DC fixture, for an all-recovery large commit -- and, sharing the identical fallback query count and cross-DC availability dependency though not separately measured at wall-clock, for an all-genuinely-unprovenanced one too. That is a real, user-visible latency cost for large dedup-heavy or cross-DC-recovered commits, not a rounding error, whichever end of the observed range production lands closer to.

**Decision confirmed 2026-09-09 by the project owner**, directly, in a live session, after a fourth audit pass flagged that the PR body's own "confirmed by the project owner" claim had no corroborating record on GitHub (zero PR comments, zero reviews) and could not be taken on the implementing agent's word alone: ship #210 as scoped, accepting the availability-domain change and the real latency cost documented above -- **conditioned on that cost and the follow-up optimization debt being documented accurately**, which is why the cost-benchmark wording fixes elsewhere in this section (the retired 3.4-5.0s causal claim, the dropped "real inter-datacenter distance" language) are part of this same confirmation, not a separate cleanup. The rationale: no cost-free alternative exists within this issue's scope (a plain `QUORUM` fallback is unsafe with one replica per DC; strengthening the `up:` write itself would impose an unconditional cost on every `PutBlock`, not only the ambiguous case), and the fan-out fix bounds the blast radius of both the availability dependency and the latency cost to at most `syncCommitBlockPlacementConcurrency` (20) block-level provenance checks admitted/in flight per commit rather than unbounded growth.

The case for shipping anyway: (1) this closes a genuine correctness gap -- the alternative is silently under-protecting cross-DC PutBlock provenance, which #206 already established as unacceptable; (2) the cost is bounded in query count (no unbounded/pathological multiplication, thanks to the fan-out fix): exactly N fallback `EACH_QUORUM` reads for N clean local misses, at most 20 concurrently in flight, with wall-clock scaling roughly (not exactly) with `N/20` when per-read latency is comparable -- "bounded" describes the query-count formula, which is exact; the observed 1.67s-8.1s wall-clock range at N=1000 above means the formula's real-world latency is not tightly predictable on this local fixture, and should not be read as one; (3) it only manifests for large (roughly 100+) block deltas in a single commit, not the common small-commit case (the N=1/10 rows stay well under 100ms even on the real 3-DC fixture); and (4) the lever that directly cuts this cost -- raising `syncCommitBlockPlacementConcurrency` above 20 -- is a one-line, low-risk change, but changing it now would mean guessing at a number without real production topology to validate it against, which is exactly the mistake `ISSUE-SYNC-PUTBLOCK-READINESS-HOTPATH-COST-01` exists to avoid.

This real measurement materially raises that follow-up's priority: it is no longer a nice-to-have optimization, it is the direct mitigation for a latency cost now measured in seconds for large commits. It remains out of this PR's scope because tuning the concurrency default responsibly needs real production multi-region latency data this local 3-DC fixture cannot provide, not because the cost is negligible. The availability-domain change for dedup-heavy commits (one DC down blocks HEAD publish for such commits, which never happened before this fix) is accepted and documented here as a deliberate, analyzed trade-off, not silently absorbed into a performance number.

Expired provenance past the 48h TTL remains unsolved (indistinguishable from true absence at either consistency level) and stays `ISSUE-SYNC-PUTBLOCK-EXPIRED-PROVENANCE-01`.

### ISSUE-W2-PUBLISH-PIN-EXPIRY-BEFORE-HEAD-01: TTL-bound own pin does not prove continuity through HEAD

**Status**: CLOSED-EVIDENCE — covered current-version W2-0 mechanism under the supported greenfield first-production contract. W2-6a was OPEN through R31 until E1-16 (2026-10-10) closed it for X1.
**Severity**: High (P1).
**Scope**: Shared pre-HEAD mechanism, covered F1/F2/F3 and PutBlock-provenanced Sync. Historical #238 full-closure blocker; v1 scope reconciled after #239/#241.
**Registered**: 2026-09-29, PR #238 cross-audit.

Baseline at #238: `PublishAttemptReferenceTTLSeconds` is 35 days; own `up:` is 48 hours.
`ValidateBorrowedFSPublicationAuthority` checks placement and GC fences, not
reference liveness. If both pins expire, the validator can authorize still-valid
P, then GC can prove zero refs and commit D before the separate HEAD CAS.
Expiry during a pause after validation has the same consequence. Repair intent
does not guarantee a live pin at the destructive zero-proof read. The shared
four-case proof is conditional on pin survival; historical adopter closure
claims require re-audit. Renewing a TTL or adding another advisory check alone
does not exclude a pause between that operation and HEAD.

Closure requires a demonstrated continuity or fail-closed mechanism excluding
GC commitment between authority validation and HEAD, including pin-expiry
interleavings. The #239 durable repair gate plus final exact-P and #241 wire/SIGKILL/recovery/3-DC evidence meet this requirement for the covered current-version chains. UNKNOWN and definitely-not-reachable repair outcomes retain the guard; a reachable outcome promotes real refs before deleting repair. No independent current-version W2-0 residual was identified. R31 settlement/liveness remains separately OPEN in W2-11..14.
See [W2-6a evidence limits](./W2-6A-CREATEFILE-EXACT-P.md) and W2-0 in X1.

See [real wire/crash/rollout audit](./W2-0-WIRE-CRASH-ROLLOUT.md) and the
[first-production contract](./X1-CRITICAL-PATH.md#first-production-deployment-contract).
The mixed-version limitation is preserved separately below.

### ISSUE-W2-INCOMPATIBLE-MIXED-ROLLOUT-01: incompatible rolling upgrade can publish HEAD after D

**Status**: Open — unsupported future incompatible rolling upgrade; both counterexamples preserved.
**Severity**: High (P1).
**Scope**: FOLLOW-UP / GENERAL. Outside the v1 X1 and first-GC-activation prerequisites.
**Registered**: 2026-09-30, scope reconciliation of merged #241 evidence.

The pinned pre-#239 binary (50c50903e7ac49c04ef36f460dccc35ce122dfd6)
reproduces D(P)+HEAD in both combinations: a new writer with old productive
GC, and an old writer with current productive Worker.ProcessOrgOnce.
The first GC does not observe repair protection; the second writer lacks the
repair-before-final-exact-P ordering. Real candidates, destructive retirement
and durable HEAD publication are required by the unchanged tests.

The supported v1 contract starts clean, with compatible audited components
before first traffic and no old attempts/processes or inherited dataset.
Therefore neither mixed-version sequence is a v1 first-activation blocker.
This is a scope correction, not a fix or acceptance of either unsafe sequence.
If incompatible rolling upgrades with active GC are supported later, a
reviewable compatibility/drain protocol and evidence are required; simply
upgrading readers first is insufficient. No registry, version gate or new
machinery is introduced here. Existing current-version PRE-GC/A1 obligations
remain unchanged.

Evidence: [#241 mixed-version counterexamples](./W2-0-WIRE-CRASH-ROLLOUT.md#mixed-version-counterexamples).
Deployment contract: [X1 source of record](./X1-CRITICAL-PATH.md#first-production-deployment-contract).

### ISSUE-PC0-EXACT-P-FUNNEL-GAP-01: Publication-authority/continuity before HEAD is not uniform by provenance

**Status**: ✅ Closed for X1 (E1-16, 2026-10-10). Every funnel listed below is closed pre-HEAD: CLOSED-FIX for the measured RED→GREEN fixes, and CLOSED-EVIDENCE for the W2-9 directory/multi-block variants that run the same verified code (X1 §4 records the state per row). Their post-HEAD R31 safety is CLOSED-EVIDENCE through the funnel-independent EACH_QUORUM repair guard (X1 §4 W2-6/6a/7/8/9; [E1-16](./E1-16-POST-D-CONTRACT-ALIGNMENT.md)). Content resurrection (W2-10) is tracked separately by `ISSUE-PC0-CONTENT-RESURRECTION-PUBLICATION-01`, and R31 convergence is PRE-GC. History: 🟡 Open — partially fixed. `UploadFile` fixed by W2-6 (PR #237, 2026-09-29): it passes its materialized exact placement to the shared finalizer, which re-validates it after `pub:` and immediately before HEAD (evidence `TestW2UploadFileExactPlacementBeforeHead`, RED on the previous code). W2-6a adds the missing exact-P check for `CreateFile` Office templates by carrying the actual SHA-256/class/key into the same final check after durable `pub:` (nine real Cassandra/MinIO legs plus reuse and empty-file controls). E1-4 / W2-8 fixes OnlyOffice pre-HEAD exact-P (real callback RED after COMMITTED/TERMINAL P1; fixed rejection and P2 replay). OnlyOffice post-HEAD/R31 remains open. SeafHTTP single-shot (E1-5a) and streaming (E1-5b, selective same-tracker recovery) measured pre-HEAD are CLOSED-FIX; post-HEAD/R31 remain open; cross-repo E1-09 measured single-file/block plaintext pre-HEAD is CLOSED-FIX, broader W2-9 OPEN; covered current-version W2-0 is CLOSED-EVIDENCE under the first-production contract and R31 remains OPEN.
**Severity**: High (P1) — W2 writer protocol completeness
**Affected**: ~~`UploadFile` → `finalizeStoredUploadMetadataOnce` with `commitBlocks=nil`~~ (fixed, W2-6); `CreateFile` Office-template publication (W2-6a exact-P CLOSED-FIX); OnlyOffice `publishEditedDocumentMetadata` (E1-4 pre-HEAD CLOSED-FIX); SeafHTTP single-shot (E1-5a) and streaming once-path (E1-5b) pre-HEAD CLOSED-FIX; cross-repo `processSingleItem` (E1-09 CLOSED-FIX, plus directories/multi-block CLOSED-EVIDENCE by E1-16). Post-HEAD R31 safety for all of them is CLOSED-EVIDENCE through the shared repair guard (E1-16); convergence is PRE-GC
**Registered**: 2026-09-09, PC-0 publication-protocol characterization

#### Problem

W1/W2 proved that publishing against a retired or changed exact physical placement `(storage_class, storage_key)` is unsafe for some provenances. Today's pre-HEAD exact-P fence exists only when the caller supplies placements:

- `CreateFileFromBlocks` passes `commitBlocks` into the shared finalizer.
- `CreateFile` Office templates pass their actual materialized placement to the same validator after durable `pub:` (W2-6a); empty CreateFile has no block.
- Sync readiness now captures and fences every added canonical block; only upload-pin renewal remains scoped to PutBlock provenance (W2-4).
- OnlyOffice callbacks carry their original materialized SHA-256/class/key through durable repair to final exact-P before HEAD (E1-4 / W2-8); COMMITTED/TERMINAL rejection and terminal P2 replay are covered, while post-HEAD/R31 stays OPEN.

`UploadFile` called `finalizeStoredUploadMetadata(..., nil)`, so
`validateCommitBlockPublicationFences` was a no-op (fixed by W2-6, PR #237). `CreateFile` Office templates now call it too (W2-6a). The original characterization also identified OnlyOffice, SeafHTTP and cross-repo
without that final exact-P mechanism. Subsequent E1-4/E1-5/E1-09 fixes cover their
measured pre-HEAD contracts; broader funnels and post-HEAD/R31 remain open.

This is a **publication-readiness/authority gap by provenance**, not a
prescription that every funnel must run another exact-P read just before
HEAD. Own-`up:` materialization can also close continuity by keeping
renewal/TTL overlap (`up:` → GC fence → install/repair → own `up:` remains).
BorrowedFS/late pin still needs exact-P because the pin may arrive after GC
won. A bare exact-P read alone still has a TOCTOU gap without destination protection.
E1-09 acquires destination pub: and durable repair before the final read; its
repair-first control measures that protection even after pub: expires.

This is a completeness gap in the current writer protocol, not a new race
invented by PC-0.

#### Scope / disposition

Recorded by PC-0 (`docs/PUBLICATION-PROTOCOL-CHARACTERIZATION.md`). Do not
absorb a single fence recipe into every funnel in the characterization PR.
A future `PublicationCoordinator` should require `PublishableInput` (not
merely classified input). `BORROWED` becomes publishable only after acquiring
durable own liveness; exact-P revalidation of foreign `fs:` does not
substitute for that pin. Migrating funnels is later PCs. W2 remains OPEN.

#### Related

- [PUBLICATION-PROTOCOL-CHARACTERIZATION.md](PUBLICATION-PROTOCOL-CHARACTERIZATION.md)
- `ISSUE-SYNC-PUTBLOCK-EXPIRED-PROVENANCE-01`
- `ISSUE-SYNC-PUTBLOCK-CROSS-DC-PROVENANCE-VISIBILITY-01`

### ISSUE-PC0-INHERITED-DEPENDENCY-CONTINUITY-01: PublishableInput is scoped to newly-live dependencies only, not R3's full work set

**Status**: Decision resolved by PC-D1 (2026-09-12); PC-D1A authority foundation landed 2026-09-19; PR #228 completed the fail-closed certifier gate; PC-D1B.3 mapping authority/M18-M19 is implemented in PR #233 (merged). The certification-window runtime and first productive consumer remain OPEN before PC-2 (2026-09-29: frozen by the X1 critical-path reset; neither is an X1 prerequisite and the fence is PRE-GC only, see [X1-CRITICAL-PATH.md](X1-CRITICAL-PATH.md) §5)
**Severity**: High (P1) — candidate coordinator boundary completeness
**Affected**: the publication authority/continuity definitions and candidate coordinator boundary in `docs/PUBLICATION-PROTOCOL-CHARACTERIZATION.md` (§2, §6 PUBL-1/PUBL-2, §10, §14), plus the implemented PR #228 certifier gate, PC-D1B.3 mapping authority in PR #233, the certification-window lifecycle fence, and the first productive consumer (historical backfill is a greenfield non-goal). PC-D1A now provides the canonical witness/HEAD authority foundation; no productive funnel is affected and no productive runtime behavior or GC activation is in this issue closure.
**Registered**: 2026-09-09, PC-0 publication-protocol characterization audit

#### Problem

PC-0 defines "Publication authority / continuity" and "Publishable input" as covering every physical dependency a HEAD will **newly live on**, and the candidate coordinator boundary in §10/§14 inherits that same scope: `PublishableInput` is only required for the delta a funnel is adding, not for dependencies a new HEAD inherits unchanged from the old HEAD.

`docs/R3-LIVENESS-CONTINUITY.md`'s "Logical positive block delta" section already flags exactly this gap:

> `LogicalPositiveBlockDelta` ... is **not** defined as the complete R3 work
> set. Future work must also consider dependencies inherited from the old
> HEAD whose liveness continuity is absent or inconclusive.

PC-0 characterizes today's writers using precisely that delta shape (new blocks only) and carries it into the candidate `PublishableInput` contract while explicitly reproducing and recording R3's caveat without resolving it. If a future `PublicationCoordinator` requires `PublishableInput` only for newly-live dependencies, any continuity gap already present in an inherited dependency (for example, a block that first reached an earlier HEAD through a funnel whose W2 status was `CONDITIONAL` or `UNKNOWN` at the time, per the per-funnel matrix in §5) is carried forward into every later commit that keeps referencing it, and the coordinator boundary as currently drafted has no step that would ever revisit it.

This does not prove the boundary is wrong: re-validating every reachable dependency would be O(tree size) per publish instead of O(new blocks). PC-D1 resolves the architectural question with a certified baseline frontier, preserving the hot path after a valid witness while requiring full certification for an absent or stale witness. PC-D1A now provides the canonical witness columns, fail-closed validity, HEAD-fenced baseline CAS, and atomic HEAD+witness global-SERIAL authority primitives. PR #228 implements the PC-D1B.1 certifier gate: authority-verified metadata, exact-P and GC-aware liveness, revalidation, and witness settlement. Mapping promotion for SHA-1-only identities is implemented in PR #233; lifecycle fencing and the first productive consumer remain prerequisites before PC-2. Historical backfill is a greenfield non-goal. The Phase 5 counterexample below remains a separate PRE-GC issue.

#### Scope / disposition

Recorded by PC-D1 (`docs/PC-D1-INHERITED-DEPENDENCY-CONTINUITY.md`). The architecture decision, PC-D1A authority foundation, and PR #228 fail-closed certifier gate are complete. This issue remains OPEN for lifecycle fencing and productive integration required before PC-2. Mapping coverage is implemented in PR #233 and must merge before a consumer relies on it.
The single responsibility owner is the **certified baseline frontier**: observe and fully certify a concrete HEAD under continuity contract V, then persist a witness only while that HEAD is still current.
A valid witness has the semantic form `library X / certified through HEAD H / under continuity contract V`. `WorkSetScopeNewlyLive` may be used incrementally only while that witness matches the current HEAD and accepted contract.
If the witness is absent, stale, or invalid, the coordinator must fail closed to baseline certification; it may not treat inherited UNKNOWN/CONDITIONAL dependencies as covered by the delta.
This keeps coordinator and GC ownership distinct: the coordinator/frontier certifies positive continuity, while GC still needs its own sharing-aware negative-retention fix before activation.
PC-2 may assume the canonical witness columns, fail-closed authority validity, and HEAD-fenced/atomic global-SERIAL primitives; it may not assume that PR #233's mapping authority has merged, or that the exact-P/liveness handshake, lifecycle serialization, or a productive consumer already exist.
No funnel is migrated by PC-D1A, and no GC configuration changes are part of this issue closure.

**Historical PC-0 counterexample:** its Phase 5 cascade is now fixed by
[metadata-only commit retirement](./GC-PHASE5-SHARED-FSOBJECTS.md); the original
argument below remains discovery provenance, not today's cascade behavior.
Phase 6/concurrent metadata destruction still do not supply universal continuity.

"The current GC already protects them" is **not** one of the options. GC Phase 5 (`scanExpiredVersions`) enqueues any
commit outside the HEAD parent chain older than `version_ttl_days`;
`processCommit → processFSObject` then cascades the commit's whole tree,
removing `fs:` references and deleting fs_object rows without checking
whether the same content-addressed `fs_id` is still reachable from HEAD.
Phase 6 builds a keep-set; Phase 5's cascade does not. Dangling commits that
share almost their entire tree with HEAD exist in normal operation (v2 CAS
losers keep their `commits` row, orphan Sync `PutCommit` rows, Sync
auto-merge targets), so a live HEAD's tree can be deleted. Frozen by
`internal/gc` `TestPC0Characterization_Phase5CascadeRemovesFSObjectsSharedWithHEAD`
and tracked as `ISSUE-GC-PHASE5-CASCADE-SHARED-FSOBJECTS-01` (P0 latent,
PRE-GC).

W2/R31 remain OPEN either way; this finding does not change their status.

#### Related

- [R3-LIVENESS-CONTINUITY.md, "Logical positive block delta"](R3-LIVENESS-CONTINUITY.md)
- [PUBLICATION-PROTOCOL-CHARACTERIZATION.md](PUBLICATION-PROTOCOL-CHARACTERIZATION.md)
- `ISSUE-GC-PHASE5-CASCADE-SHARED-FSOBJECTS-01` — the counterexample

### ISSUE-PCD1-CERTIFIED-BASELINE-IMPLEMENTATION-01: Durable inherited-continuity witness and atomic HEAD frontier (PC-D1A + PC-D1B.1 certifier landed)

**Status**: Open - PC-D1 architecture and PC-D1A foundation landed; PR #228 closes the fail-closed certifier gate on 2026-09-22 and PC-D1B.3 adds mapping authority/M18-M19 on 2026-09-23. Lifecycle fencing, and the first productive consumer remain required before PC-2; historical backfill is a greenfield non-goal. **2026-09-29:** frozen by the X1 critical-path reset ([X1-CRITICAL-PATH.md](X1-CRITICAL-PATH.md) §5); neither the consumer nor PC-2 is an X1 prerequisite, and E1 re-evaluates whether the certified baseline is needed once W2 is closed
**Severity**: High (P1) - publication continuity prerequisite
**Affected**: future coordinator adoption and every library whose inherited
dependencies have not been certified through its current HEAD
**Registered**: 2026-09-12, PC-D1 decision

#### Problem

PC-D1 selects the certified baseline frontier as the sole owner of inherited
continuity. PC-D1A adds durable certified-through-HEAD and
continuity-contract columns, fail-closed `LibraryState` validity, a
HEAD-fenced baseline witness CAS, and an atomic HEAD+witness compare-and-set
authority primitive. PR #228 now supplies the cold-path certifier; no productive consumer exists yet.
Historical backfill is a greenfield non-goal: deployment is greenfield
(`docs/DEPLOY.md`), so there are no pre-authority production rows to
certify retroactively. See `ISSUE-PCD1B-METADATA-IDENTITY-AUTHORITY-01`. The validity check and both authority LWTs also require
`deleted_at = null`; a canonical row whose deletion is already visible to
the LWT cannot certify or advance the frontier. This predicate is not a
global lifecycle serialization fence: ordinary production soft-delete,
restore, and hard-delete writes do not currently share the global HEAD Paxos
domain. Full delete/restore/hard-delete concurrency remains the separate
`ISSUE-LIB-DELETED-FENCE-01` follow-up and is a prerequisite before a
productive PC-D1B consumer relies on the frontier across concurrent lifecycle
activity. Treating `WorkSetScopeNewlyLive` as
complete before that state is valid would recreate the PC-0 continuity gap.

#### Implemented protocol and remaining work

PR #228 consumes the metadata authority protocol read-only. It verifies the captured `HEAD -> root_fs_id` projection and every reachable fs_object before resolving dependencies, then derives paired-file SHA-256 dependencies from the claim-bound list. A compatibility mapping can agree but cannot override that list; disagreement is `identity_conflict`. SHA-1-only files whose canonical dependency depends on a mapping without authority return `identity_unproven` before physical or liveness work. The traversal rejects noncanonical authority-bound fs_id values as `malformed_tree` instead of trimming to another row; Cassandra zero-block files certify only when the exact source projection has a durable claim. Seafile's `EMPTY_SHA1` (`000...000`) is the canonical empty root/directory/file identity that clients never upload: once the verified commit or directory entry binds it, the reachable subtree is empty (no synthetic fs_object, no fs_object claim); the commit claim itself remains mandatory. Missing, conflicting, partial, or unavailable identity proof creates no witness; unavailable authority remains UNKNOWN.

Only after identity proof does the certifier capture exact minted P, prove physical bytes, establish permanent current-library liveness visible at EACH_QUORUM, and revalidate P plus current GC authority. Before witness settlement it rechecks HEAD, the commit claim/source, every reachable fs_object claim/source, exact P, physical bytes, permanent liveness, and GC authority; the final witness CAS remains HEAD-fenced and global SERIAL. M1-M15 and the isolated 3-DC scenarios exercise this boundary; M14a/b cover fs_object and commit H-to-R authority bypass, while M15a/b cover unauthoritative SHA-1-only resolution and paired mapping disagreement (17 mutation legs for 15 frozen contracts).

Mapping-authority representation and M18/M19 promotion landed with PC-D1B.3 (2026-09-23). Remaining PC-D1 work is the certification-window lifecycle fence, and a first productive consumer before PC-2. GC Phase 5 remains a separate pre-GC issue. Historical backfill is a greenfield non-goal. `GC_ENABLED=false` remains mandatory.
This issue does not authorize GC activation, Phase 5 changes,
content-resurrection fixes, or changes to W2/R31/X1 status.

#### Related

- [PC-D1 decision record](PC-D1-INHERITED-DEPENDENCY-CONTINUITY.md)
- `ISSUE-PC0-INHERITED-DEPENDENCY-CONTINUITY-01`
- `ISSUE-GC-PHASE5-CASCADE-SHARED-FSOBJECTS-01`
- `ISSUE-PCD1B-METADATA-IDENTITY-AUTHORITY-01` - the metadata identity-authority prerequisite for the certifier this issue asks for

### ISSUE-PCD1B-METADATA-IDENTITY-AUTHORITY-01: A complete metadata identity is not an authoritative one, and baseline certification cannot tell them apart

**Status**: 🟡 Certifier correctness closed by PR #228 on 2026-09-22: commit/fs_object claims are consumed read-only and unproven or conflicting metadata cannot receive a witness. PC-D1B.3 (2026-09-23) adds the Mapping Authority. It is a write-once global-SERIAL claim per `(org_id, representation_id, external_id)`, acquired only on the cold path after the canonical block, in the claimed representation, has stored bytes that hash to both the SHA-256 and the SHA-1 (semantic provenance). Promotion then freezes the ordinary `block_id_mappings` row to the claim with a dominant `USING TIMESTAMP`, so every wall-clock ordinary write is inert (temporal authority). A SHA-1-only dependency certifies only through a claim with a frozen projection whose block is still in the library's representation, and this is rechecked before the witness CAS. A diverged projection is `identity_conflict` and is never repaired, and a mapping without provenance stays `identity_unproven`. Lifecycle fencing and a productive consumer remain tracked by `ISSUE-PCD1-CERTIFIED-BASELINE-IMPLEMENTATION-01`.

PR #233's final cross-audit follow-up pins productive `block_id_mappings`
resolution to `LOCAL_QUORUM`: `EACH_QUORUM` reaches a quorum in every
replica-holding DC, not every replica, so a session-level `ONE` read could
otherwise choose a stale RF>1 replica. A repository-wide production-Go contract
also inventories all mapping mutations: only the authority freeze may specify
an explicit timestamp, the ordinary INSERT cannot specify one, and production
DELETE remains prohibited by R11a. `CassandraStore.lookupBlockMapping()` in GC
still inherits session consistency; it is tracked separately as P2/PRE-GC and
must use the pinned read contract or gain an explicit consistency proof before
GC activation. `GC_ENABLED=false` remains mandatory.
**Severity**: High (P1) - certified-baseline correctness prerequisite
**Affected**: the PC-D1B.1 certifier (`CertifyLibraryBaseline`, `readContinuityCommitProjectionContext`, `walkContinuityTree`, `resolveBlockIDs`), every `commits` / `fs_objects` writer and deletion path, `block_id_mappings` and every one of its writers (`WriteBlockIDMapping`, the web-only `WriteVerifiedWebBlockMapping`, and any future mapping writer), and any future consumer of the continuity witness
**Registered**: 2026-09-20, PC-D1B metadata-identity authority audit

#### Problem

Before PR #228, baseline certification resolved `HEAD -> root_fs_id`, walked the reachable tree, and chose SHA-256 dependencies through ordinary reads; complete but divergent commit or fs_object rows could therefore be certified. PR #228 now verifies the exact commit and reachable fs_object projections against immutable identity claims and revalidates those projections before the witness. Missing, conflicting, partial, or unavailable authority cannot continue as certified. Before PC-D1B.3, SHA-1-only mapping-dependent files remained fail-closed because Mapping Authority was separate coverage work; PR #233 now adds that cold-path coverage.

Five sub-gaps belong to the same finding:

- **Canonical list in the digest.** `fs_id` is derived from the Seafile SHA-1
  representation, so two complete rows can agree on `fs_id`, object type, size
  and logical SHA-1 list while naming different canonical SHA-256 `block_ids`.
  A file authority digest that omits the canonical list accepts both under one
  claim, and the physical dependency can change underneath a settled witness.
  The frozen projection must bind both lists.
- **Mapping authority.** When a file identity names SHA-1 ids with no paired
  canonical SHA-256, `block_id_mappings` alone decides which physical bytes the
  liveness proof is about, and that table has no identity authority. It is
  authority only in that case: for a paired row the authority-bound canonical
  list fixes the dependency and the mapping need only agree. Mapping authority
  is acquired by promotion on the cold path, never by adding a per-block LWT to
  the upload hot path, which `docs/WEB-BLOCK-UPLOAD.md` rules out by design.
  A promotion must neutralize pre-fence mutations that can still be delivered,
  including pending hints, and must validate the value it claims against an
  independent trusted source: convergence proves agreement, not provenance, and
  promoting the currently converged row of a SHA-1-only identity would decide
  its dependency from the artifact whose provenance is in question. Promotion
  is coverage work, not history: `storeSyncFSObject` writes the wire SHA-1 list
  into `block_ids` and leaves `seafile_block_ids_sha1` unset, so a library
  created after launch can hold a SHA-1-only identity that needs one.
  **Implemented by PC-D1B.3 (2026-09-23):** `block_mapping_authority_claims`
  (migration 027) holds the write-once claim. Promotion proves the candidate
  from the representation-bound stored bytes and neutralizes pre-fence and
  in-flight mutations by freezing the ordinary row at a dominant write
  timestamp. The certifier consumes only a frozen claim and refuses a diverged
  row. Server-side-encrypted web and OnlyOffice mappings hash plaintext, cannot
  be proved without the key, and stay <code>identity_unproven</code>.
- **Deletion and re-creation.** `fs_objects` rows are deleted in production by
  library-creation rollback and by GC; `commits` rows also by the
  failed-publish cleanup and two guarded v2 FS-helper discards.
  (`CleanupFailedPublishArtifacts` receives `fsIDs` and never deletes them, and
  `cleanupFailedPublishDeleteFSObjectFn` has no production caller.) None of
  them consults authority, and the same key can be written again afterwards. A
  protocol that fences only new rows does not cover this.
- **Serial domain.** The claim must pin the canonical global `SERIAL` domain
  explicitly. Inheriting `database.serial_consistency` /
  `CASSANDRA_SERIAL_CONSISTENCY` may yield `LOCAL_SERIAL`, whose per-DC Paxos
  domain gives no cross-DC no-conflicting-writer guarantee.
- **Certification window.** A covered identity deleted between the certifier's
  final revalidation and its witness settlement still lets the PC-D1A witness
  CAS apply, because that CAS predicates only `head_commit_id` and `deleted_at`
  on the `libraries` row. The witness is then born false rather than merely
  going stale, and re-reading the row before the CAS has the same TOCTOU
  window. Scope on current evidence: the only production delete that can reach
  an identity in the current reachable tree is the GC expired-version cascade
  (`ISSUE-GC-PHASE5-CASCADE-SHARED-FSOBJECTS-01`, P0 latent, PRE-GC), dormant
  under `GC_ENABLED=false`. Library-creation rollback removes the canonical row
  the CAS predicates on, the failed-publish cleanup deletes no `fs_objects` at
  all, and both v2 FS-helper discards are guarded against the winning HEAD. So
  this is mandatory PRE-GC and before the first productive consumer, and it
  re-scopes to a certifier blocker if a non-GC reachable delete is shown.
  PC-D1B.4 selects the mechanism (per-library destruction epoch plus pending
  destruction intents, predicated by the witness CAS) and characterizes the
  race: `ISSUE-PCD1B4-CERTIFICATION-WINDOW-FENCE-01`.

#### Scope / disposition

The decision record owns the reasoning, the rejected alternatives (notably
read-time stabilization at `EACH_QUORUM`), the frozen identity projection, and
the required M14-M19 plus 3-DC evidence, split by the PR that owns each
property and stated at the layer that owns it, so no PR needs a consumer that
has landed (M16-M17 against the claim and digest alone, M14-M15 in the certifier gate); M18-M19 landed with the PC-D1B.3 promotion path. The decision freezes the
certification-window invariant and leaves its mechanism (generation/epoch in
the CAS predicate, a delete fence, frontier invalidation, or an equivalent
protocol) to the implementation PR; that fence is mandatory before destructive
GC activation and before the first productive consumer rather than a merge
precondition for the certifier, and re-scopes if a non-GC reachable delete
appears. Minimum certifier correctness - fail closed as `identity_unproven` /
`identity_conflict` / `UNKNOWN` - is likewise separable from coverage: a
certifier that refuses every identity it cannot prove is correct but not yet
useful, so cold-path mapping promotion is a distinct work item. Deployment is
greenfield (`docs/DEPLOY.md`), so historical cutover, pre-authority backfill
and forensic reconstruction are non-goals rather than roadmap stages; they
return only if that contract is deliberately changed. Nothing here authorizes
a productive consumer, lifecycle serialization, PC-2, funnel migration, or GC
activation. `GC_ENABLED=false` remains mandatory.

#### Related

- [PC-D1B metadata identity authority decision](PC-D1B-METADATA-IDENTITY-AUTHORITY.md)
- [PC-D1 decision record](PC-D1-INHERITED-DEPENDENCY-CONTINUITY.md)
- `ISSUE-PCD1-CERTIFIED-BASELINE-IMPLEMENTATION-01`
- `ISSUE-PC0-INHERITED-DEPENDENCY-CONTINUITY-01`
- `ISSUE-LIBRARY-HEAD-SERIAL-DOMAIN-01` - the canonical global `SERIAL` domain the claim must reuse
- `ISSUE-SYNC-PUTCOMMIT-NOT-WRITE-ONCE-01`, `ISSUE-SYNC-RECVFS-NOT-WRITE-ONCE-01` - the #208 fixes that narrow current write paths without attesting stored rows
- `ISSUE-GC-PHASE5-CASCADE-SHARED-FSOBJECTS-01` - the registered counterexample for a delete reaching a HEAD-reachable identity; fixing that cascade is separate work
- `ISSUE-PCD1B-AUTHORITY-CLAIM-RETIREMENT-01` - the accepted debt created by non-expiring claims

### ISSUE-PCD1B-AUTHORITY-CLAIM-RETIREMENT-01: Identity authority claims are non-expiring and accumulate with no retirement protocol

**Status**: 🟡 Open - registered 2026-09-20 with the PC-D1B decision; accepted debt, nothing to implement in PR #228 or PR #229
**Severity**: Medium (P2) - follow-up / tech debt
**Affected**: the future per-identity authority store for `commits`, `fs_objects` and `block_id_mappings`
**Registered**: 2026-09-20, PC-D1B metadata-identity authority decision

#### Problem

`ISSUE-PCD1B-METADATA-IDENTITY-AUTHORITY-01` deliberately makes an authority
claim write-once and non-expiring, and keeps it alive when its source row is
deleted so that a re-created key cannot mint fresh provenance. The consequence
is that claims accumulate with no way to remove them: failed initializations,
losing commits, deleted identities and deleted libraries all leave a permanent
row behind. This is a greenfield cost, not historical residue - a cluster that
starts empty still accrues it from the first failed publish onward.

#### Scope / disposition

Retiring a claim is its own fenced, evidence-backed protocol and is explicitly
out of scope for the decision and for the certifier. Until it exists, no path
may retire a claim, and the correct behavior for an identity that will never
return is to leave its claim in place. This issue exists so the debt does not
disappear when the metadata-authority issue closes; it does not authorize any
retirement mechanism, and it is not a merge precondition for PR #228 or #229.

#### Related

- `ISSUE-PCD1B-METADATA-IDENTITY-AUTHORITY-01`
- [PC-D1B metadata identity authority decision](PC-D1B-METADATA-IDENTITY-AUTHORITY.md)

### ISSUE-PCD1B4-CERTIFICATION-WINDOW-FENCE-01: A witness can be born over, or survive, the destruction of the state it certifies

**Status**: 🟢 Decision CLOSED / MERGEABLE 2026-09-24 (CW-M33 accepted-Paxos barrier and post-barrier proof-issuance races + G21/G22 RED, CW-M34 in-flight materialization contract/evidence); PC-D1B.5 runtime OPEN and mandatory before destructive GC activation. Reclassified PRE-GC only on 2026-09-29 ([X1-CRITICAL-PATH.md](X1-CRITICAL-PATH.md) §6): the destroyers that can falsify a witness (D1–D3) are GC-only, so it no longer gates a productive consumer; re-scope §18 to what D1–D3 need before implementing. WIP parked on `feat/pc-d1b5-certification-window-fence-runtime`
**Severity**: High (P1) — certified-baseline correctness; dormant today (no production caller of the certifier or the witness, `GC_ENABLED=false`)
**Affected**: `CertifyLibraryBaseline` witness settlement, `CommitLibraryContinuityWitness*`, `AdvanceLibraryCertifiedFrontier`, the identity-gateway source deletes and the GC `fs:` reference removal
**Registered**: 2026-09-23, PC-D1B.4

#### Problem

The witness CAS predicates only `head_commit_id` and `deleted_at` on the
canonical `libraries` row. Deleting the HEAD commit, a reachable fs_object or a
permanent `fs:` reference after the certifier's final revalidation still lets
the witness settle (R4, R5; also through a trash round trip, R3b, and through
UNKNOWN settlement, R11b), and a destruction after settlement leaves the
witness valid (R10b), including across restore (R10). Semantic replacement at
the same key is already impossible (immutable identity claims, R6/R7).
Library soft-delete/restore/hard delete do not change what the witness
asserts. With `GC_ENABLED=false` no production path destroys HEAD-reachable
state; the gateway primitives and GC Phase 5/6 can.

#### Decision (PC-D1B.4)

A per-library destruction fence on the canonical row, in the global SERIAL HEAD
domain, remains the selected architecture: E destruction epoch, P token-to-owner
generation, S superseded high-water, generation-owned intents, and witness
invalidation. CW-M27 closes the local-timestamp proof gap: when S0 is non-null,
every covered row is reaffirmed with its identical verified projection through
EACH_QUORUM on every certification attempt, regardless of local
`WRITETIME > S0`; partial/UNKNOWN is not global proof, so retry repeats
EACH_QUORUM. CW-M28 closes a future tombstone relative to the same clock. CW-M29
closes the cross-node variant: a fast GC clock cannot issue a tombstone ahead of
a slow writer's clock just because it is legal under the GC node's local wall
clock. The contract requires a fleet-wide `ClockSafetyLease` with a
finite maximum pairwise skew Δ, fresh monotonic health for every timestamp
source, `safe_now = gc_local_now - Δ - 1us`, and no destructive write when the
lease/health/safe interval is absent. The lease must cover GC workers, supported
timestamp-sending clients, and Cassandra coordinators that assign default write
timestamps. W for a whole-row delete remains the maximum timestamp of every
live regular cell the tombstone must dominate, including display metadata.

CW-M31 closes the cross-DC visibility part of the absent-row bypass: a local
`CanonicalLibraryExists == false` is only a local observation and never mints a
bypass capability. A global EACH_QUORUM absence read sees the remote row in the
3-DC dc-na-absent / dc-eu-present test. **CW-M33 closes the separate Paxos
stability gap:** `GlobalCanonicalAbsenceProof` must establish both global row
absence and that no HEAD proposal begun before proof issuance can become
effective afterward. The accepted-Paxos test-only latch on Cassandra 5.0.9
pauses the H0→H1 proposal after acceptance and before commit, then hard-deletes
`libraries` with `ballot-1`; a global-SERIAL read settles H1 before the following
EACH_QUORUM absence check, so no proof is minted. G21 removes the SERIAL barrier
and turns RED when H1 resurrects after an EACH_QUORUM-only proof. The test image
is isolated to this Docker fixture; the stock Cassandra image and production
runtime are unchanged. The post-barrier issuance race is also covered: SERIAL
observes H0, a new H0→H1 proposal starts and pauses, hard delete lands, and the
subsequent EACH_QUORUM read sees absence; the saved SERIAL-present result still
refuses the capability. G22 removes that SERIAL-result predicate and turns RED
with proof followed by the resumed H1. Reproducibility pins binary digest
`d35e159439b302146f964919904f84fd3c2cebf347272b8cb8c4368c1cf200e5`, upstream
source commit `b5f2a54210d541339c2e7c17a794195cac0e67c2`, and SHA-256 checksums
for both patched Java files in `scripts/cassandra-cw-m33/cassandra-source.sha256`.

CW-M32 assigns CW-M29 admission to every timestamp authority, not only GC:
commit/fs_object/permanent-reference writers and Cassandra coordinators must
not accept timestamped writes without current clock-health authority. **CW-M34
adds the separate operation-lifetime gap:** a healthy lease at admission does
not protect a timestamped write already capable of settling after `g`. Before
GC commits `g`, no admitted covered write at `w <= g` may remain able to settle
later without recovery. Freeze the property, not the implementation: drain
permits through definite settlement then revalidate liveness, or use a proven
equivalent; alternatively recover/re-materialize above the destructive floor
and verify visibility. UNKNOWN/timeouts retain protection while a write can
still land. The model covers drain+revalidation and recovery; a pinned Cassandra
5.0.9 3-DC transport-gated request returns success while remaining hidden under
the later tombstone. This is a characterized current hazard, not runtime
protection.

The intent LWT compares observed E/P/S and the non-null immutable canonical
`created_at` sentinel; canonical absence is NOT_APPLIED and must not create a
partial row. Tokens derive from persisted durable queue `identity_at`; enqueue
already persists its effective value and requeue preserves it. UUIDv5 now has
an exact namespace UUID, byte encoding, and block/commit/fs_object known vectors
(CW-M30). V1 has no
batch amortization, and an ambiguous reaffirmation may be repeated. Each successful
destroyer completes with `DELETE pending[t] IF pending[t] = g`, so a stale
attempt cannot clear a retry's protection. Queue items holding an entry leave
the queue (DLQ expiry, operator delete) only after abandon-by-takeover, and a
backpressure cap bounds the pending map. Best-effort D4/D5 cleanups of commits
proven never to be HEAD take a `ProvenUncoveredCleanupCapability` and write no
fence state. The certifier captures the fence before its final revalidation,
refuses a busy library, and its CAS predicates the captured epoch. Witness
shape stays `(H, V)` and validity is unchanged, but productive witness authority
must be read at global SERIAL. Soft-delete, restore and hard delete need no
change. CW-M27–CW-M34 decision contracts and evidence, UUIDv5 vectors, and
cross-DC visibility are closed. CW-M34's model and G20 mutation demonstrate
that a one-shot health check is insufficient; real 3-DC Cassandra confirms a
timestamped materialization can return success while hidden under a later
tombstone. Its lifetime barrier/recovery remains PC-D1B.5 runtime work.
The performance contract now counts the SERIAL absence-proof read plus the
EACH_QUORUM recheck, and distinguishes normal local lifetime tracking from
exceptional drain/revalidation or recovery I/O.
Therefore the PC-D1B.4 decision is CLOSED / MERGEABLE; PC-D1B.5 remains
mandatory before activation/consumer. Evidence includes the two-clock, stable-
absence and in-flight models, block/commit/fs_object UUIDv5 vectors,
real-Cassandra absent-row/whole-row-W/future-tombstone and CW-M34
characterizations, isolated 3-DC post-UNKNOWN, local-absent/remote-present,
CW-M33 accepted-Paxos, post-barrier issuance/G21/G22 mutations and CW-M34 tests,
plus lifecycle/destroyer/alias/clock/vector/in-flight guards (G1-G20, G21, G22, G16 and
C1 RED).

#### Remaining

PC-D1B.5 implements the fence and its mutation contract (CW-M1..M16,
CW-M18..CW-M34) exactly as scoped in the decision record, wires stable global
absence proof and writer lifetime protection/recovery, implements the CW-M29
lease and frozen UUIDv5 tuple, and inverts the UNSAFE characterization rows.
It must land before destructive GC activation or the first productive consumer.
Phase 5/6 fixes, mapping authority, soft-delete serialization
(`ISSUE-LIB-DELETED-FENCE-01`), the productive consumer and PC-2 remain
separate.

#### Related

- [PC-D1B-CERTIFICATION-WINDOW-FENCE.md](PC-D1B-CERTIFICATION-WINDOW-FENCE.md)
- `ISSUE-PCD1B-METADATA-IDENTITY-AUTHORITY-01`, `ISSUE-PCD1-CERTIFIED-BASELINE-IMPLEMENTATION-01`
- `ISSUE-GC-PHASE5-CASCADE-SHARED-FSOBJECTS-01`, `ISSUE-PC0-CONTENT-RESURRECTION-PUBLICATION-01`

### ISSUE-PCD1B-MAPPING-PROJECTION-STABILITY-01: After Mapping Authority, a mutable mapping row can diverge from the authority a witness rests on

**Status**: ✅ Fixed by PR #233 (merged 2026-09-26); PRE-CONSUMER
**Severity**: High (P1) — closure required before the first productive consumer; dormant today (no SHA-1-only file can be certified on `main`, no consumer)
**Affected**: `block_id_mappings` writers (`WriteBlockIDMapping` is a plain upsert), ordinary block resolution readers, and certification of SHA-1-only files
**Registered**: 2026-09-23, PC-D1B.4

The cross-audit found that a witness could rest on mapping authority A while a
later ordinary write made the mutable `(org_id, representation_id,
external_id)` row resolve to B. Mapping rows are org-scoped and shared across
libraries, so the per-library PC-D1B.4 destruction fence cannot cover them.

PR #233 closes the finding by freezing the ordinary projection to the durable
claim with a dominant timestamp at `EACH_QUORUM`, then pinning productive
mapping resolution to `LOCAL_QUORUM`, which intersects that freeze in the
reader's local DC. A repository-wide production-Go mutation inventory requires
the sole ordinary INSERT to omit explicit timestamps, permits explicit
timestamps only on the authority freeze, and prohibits production DELETE under
R11a. The closed-world CQL inventory rejects every unclassified statement
mentioning the table. Directed mutations M20, T2/H3 and DEL1 turn RED if those
guarantees are weakened. The separate GC resolver `CassandraStore.lookupBlockMapping()` still
inherits session consistency; it remains P2/PRE-GC follow-up and
`GC_ENABLED=false` remains mandatory until that resolver is pinned or proved
safe. This issue is separate from PC-D1B.5.

### ISSUE-PCD1B-PAIRED-MAPPING-COMPATIBILITY-01: A paired SHA-1 projection can diverge for legacy readers after a witness

**Status**: 🟡 Open — registered 2026-09-26; CURRENT-RUNTIME / FOLLOW-UP
**Severity**: P2 — compatibility/integrity for legacy SHA-1 readers
**Affected**: Paired `fs_objects` with canonical SHA-256 A and logical SHA-1 E
**Blocks #233**: No

For paired files, the witness dependency is the canonical SHA-256 A from the
claim-bound `fs_object`. The compatible mapping E→A is checked but is not
promoted or frozen as Mapping Authority. A later ordinary write can therefore
make an existing legacy SHA-1-only reader resolve E→B after the witness, while
the canonical witness still names A and remains valid. This is a preexisting
runtime compatibility/integrity issue; it was not introduced by PR #233 and
does not block it. Decide separately whether paired compatibility projections
need their own authority/freeze or whether those readers should resolve through
canonical identity.

### ISSUE-PCD1B-MAPPING-CLAIM-PROJECTION-RECONCILIATION-01: A durable mapping claim can remain stranded from its projection

**Status**: 🟡 Open — registered 2026-09-26; PRE-CONSUMER
**Severity**: P2 — operational recovery
**Affected**: Mapping promotion interrupted after the durable claim and before the projection freeze
**Blocks #233**: No

A crash after claiming A but before freezing can be followed by an ordinary
write that leaves the mutable projection at B. The existing protocol correctly
fails closed: it does not repair the row and does not create a witness. The
claim can nevertheless remain unusable until the projection agrees again or a
separately designed reconciliation/retirement protocol is implemented. Define
that recovery protocol before the first productive consumer; do not weaken the
immutable claim or the no-repair behavior in #233.

### PRE-X1 audit debt: identity claim-table CQL inventory and CAS mutation granularity

**Status**: I1 CLOSED-FIX (guard quality); M1 CLOSED-EVIDENCE (independent-terminal quality criterion)
**Severity**: P2 — structural guard and evidence quality
**Blocks #233**: No

The original `identity_authority_claims` guard relied on a literal/string inventory
that can miss a table name assembled from fragments across production files.
Before PRE-X1, move that table to a closed-world inventory evaluated at each
Query/Batch execution site, following the execution-site approach used for
`block_mapping_authority_claims` in #233. Keep this separate from #233 and do
not expand the Mapping Authority claim-table fix into the older identity claim
guard.

The H8 upload mutation in `scripts/pc-d1b3-mapping-authority-mutation-validation.sh`
also exercises ten CAS terminals in one source mutation. Because another terminal
can keep the mutation red, the result does
not independently prove that every terminal remains inventoried. Split H8
into one directed mutation per terminal before treating those checks as
independent evidence. This is test debt, not a runtime blocker for #233.

**E1 final audit (2026-10-16):** [I1/M1](./E1-FINAL-DISPOSITION.md#remaining-x1-work)
remain P2 PRE-X1 quality debt. The existing identity guard passes and current
literal claim sites are one INSERT/one SELECT; that does not prove execution-site
completeness. H8 belongs to the mapping cold-path guard, not the identity
primitive. No new current-runtime defect or productive guard is claimed.

**I1 update (2026-10-16):** [Execution inventory evidence](./I1-IDENTITY-EXECUTION-INVENTORY.md)
closes the identity portion for current classified source: shared Query/Batch
analysis, exact INSERT/read owners and 1/1 counts, preserved literal/schema
checks, two demonstrated old-guard blind spots and 13 compiled negatives.
Normal/race DB pass with nine seed skips. This is a test-guard fix, not a runtime
data-loss fix. M1 retains P2 PRE-X1; E1/X1 acceptance and GC activation remain separate.

**M1 update (2026-10-16):** [Independent H8 evidence](./M1-INDEPENDENT-CAS-EVIDENCE.md)
closes the remaining mutation-granularity condition. Ten compiled cases each
require their selected terminal diagnostic, with a GREEN non-CAS control and
normal/race source guards. A disabled ScanCAS recognition is masked by the old
aggregate predicate but refused by the new selector. No production guard/runtime
change; H1/I1/M1 results now await maintainer final E1 acceptance. X1 OPEN, GC OFF.

### ISSUE-PCD1B-CONTINUITY-LWT-GHOST-ROW-01: A continuity LWT racing a hard delete can leave a HEAD-less ghost `libraries` row

**Status**: 🟡 Open — characterized 2026-09-23 (PC-D1B.4, R9g and R9i); PRE-GC. Supersedes the earlier id `ISSUE-PCD1B4-WITNESS-GHOST-ROW-01`
**Severity**: Low — never a valid witness (HEAD is null); GC liveness only
**Affected**: `CommitLibraryContinuityWitnessContext` today, and the PC-D1B.5 destruction intent/completion LWTs, versus `hardDeleteLibraryRowsFn` / `CassandraStore.HardDeleteLibrary`
**Registered**: 2026-09-23, PC-D1B.4

The hard delete is a plain row delete with a client timestamp; continuity
LWTs are Paxos writes with a ballot timestamp. If an LWT's read precedes the
delete at the replicas and its ballot is later than the delete's timestamp,
the merged row keeps the LWT's live cells: the witness columns for the witness
CAS (R9g), and the epoch/pending/superseded columns for the future destruction
intent (R9i, characterized with any LWT on the row). An `IF` on existing
columns does not prevent it. `ContinuityWitnessValidFor` is false (HEAD is
null), but `CanonicalLibraryExists` and restore's canonical read see a
library: guarded cascade children postpone and the row is never reclaimed.
Frozen by `TestPCD1B4Characterization_PostWitnessLifecycle/R9g` and `/R9i`
(timestamp-order models on real Cassandra). Required before GC activation: a
HEAD-less `libraries` row counts as canonically absent for every GC and
restore decision, or is cleaned, after the GC's own authority check (a
global-SERIAL canonical row delete is the alternative). Not a
certification-window blocker.

### ISSUE-PCD1B-STALE-TOMBSTONE-DISPLAY-METADATA-01: A superseded destruction generation's row tombstone can erase fs_object display metadata

**Status**: 🟡 Open — registered 2026-09-23 (PC-D1B.4 cross-audit); PRE-GC
**Severity**: Medium — metadata loss, not witness or identity loss
**Affected**: PC-D1B.5 generation-fenced fs_object deletes and the display-only columns `obj_name`, `full_path`, `mtime`
**Registered**: 2026-09-23, PC-D1B.4

Under the PC-D1B.4 fence a paused, superseded destruction generation may
still deliver its row tombstone at `ts(g)`. Certifier reaffirmation rewrites
the identity projection above the superseded high-water mark, so the
certified identity survives, but display-only cells written at or before
`ts(g)` stay shadowed. Before GC activation, either the reaffirmation or the
destroyer's verification must restore/avoid display metadata loss (for example
column deletes limited to the identity projection plus a separate row-marker
policy), with a characterization. Not part of PC-D1B.5.

### ISSUE-GC-HARD-DELETE-LEASE-SERIAL-DOMAIN-01: The library hard-delete lease inherits `serial_consistency`

**Status**: ✅ Closed in PR #234 on 2026-09-29 — every hard-delete lease LWT (library, user and org) now uses explicit global `SERIAL`, and an acquire with an unknown CAS outcome makes a best-effort own-token release and never reports success (classified CURRENT-RUNTIME / CONFIG-DEPENDENT and PRE-GC by the 2026-09-29 X1 reset; both classifications are discharged)
**Severity**: Medium — multi-DC lifecycle serialization
**Affected**: `acquireHardDeleteLock` / `renewHardDeleteLock` / `releaseHardDeleteLock` (`internal/gc/store_cassandra.go`), used by restore, API permanent delete and the GC library/user/org cascades
**Registered**: 2026-09-23, PC-D1B.4

All four lease LWTs apply `SerialConsistency(gocql.Serial)`: acquire (insert
and stale takeover), renew, and conditional release. This is independent of
the configured session default `database.serial_consistency` /
`CASSANDRA_SERIAL_CONSISTENCY`. The user and org leases share the same helpers
and are pinned as well; they are only reached by the GC worker, so that part
was PRE-GC only.

**Unknown CAS outcome.** With one global Paxos domain, two DCs racing for the
same lease can receive `CAS_WRITE_UNKNOWN` ("proposal accepted by 1 but not a
quorum") or a CAS write timeout. The isolated 3-DC fixture reproduced it in
6 of 41 concurrent races before the settle step existed. Such a proposal can
still be committed by a later Paxos round; the caller had already returned an
error without releasing, so the lease stayed owned by a token nobody renews
and every restore/permanent delete of that library waited for stale takeover
(about 90 minutes). `settleHardDeleteLockAcquire` now classifies the error
(`isAmbiguousHardDeleteLockCASError`, mirroring
`isAmbiguousLibraryHeadUpdateError`) and runs the conditional release for the
same token, then always returns the original error, so no caller proceeds
without a known lease. The release is **best-effort**, not a guarantee: it can
itself fail or time out (the error then says so: "own-token release also
failed ... the lease may stay held until stale takeover"), and a release whose
Paxos round does not observe the in-flight proposal returns NOT_APPLIED while a
later round can still commit that proposal. In both cases the token can remain
the single owner until stale takeover (~90 min) or the 6 h TTL. This is a
liveness residual only: it never creates a second owner. Settling until the
outcome is known (for example a bounded SERIAL read-and-release loop) is left
as a follow-up; it is not a merge requirement for this issue.
Cost: global SERIAL adds cross-DC round trips to acquire/renew/release, and a
DC that cannot reach a Paxos quorum of all replicas cannot restore or
permanently delete a library.

**Residual (accepted, not fixed here): renew/release with an unknown outcome.**
The same contention can return `CAS_WRITE_UNKNOWN` or a CAS write timeout for
`renewHardDeleteLock` and `releaseHardDeleteLock`; the 3-DC fixture reproduced it
on a renew right after a contended acquire. Only acquire is settled. A renew
error fails closed on every path: the GC cascade heartbeat
(`newHardDeleteLease`) records the error and the cascade aborts, and the restore
(`restoreDeletedLibrary`) and permanent-delete (`permanentlyDeleteTrashedLibraryCandidate`,
including its link-cleanup heartbeat) fences return an error before their final
batch. The deferred release then **attempts** to drop the lease by token; that
release can itself have an unknown outcome or fail. How that error is surfaced
depends on the path:

- API restore and permanent delete log the release error from their deferred
  release.
- The GC user, library and org cascades release through
  `hardDeleteLease.Close()`, which only records the error in the lease
  (`setErr`). Close runs deferred, after the caller's final `Check()`, so the
  error is neither logged nor returned.
- The GC library child-item guard (`acquireLibraryDeleteGuard` release closure)
  discards the release error (`_ =`).

In every case, if the delete did not commit, the lease may remain until stale
takeover (~90 min) or its 6 h TTL; the cost is a delayed retry of the same
operation, never a second owner. Renew and release are idempotent under the
caller's own token, so a bounded retry on an unknown outcome (and surfacing a
deferred release error from the GC paths) would be safe; that is a liveness
follow-up, not a safety issue, and does not block this closure.

Evidence: `internal/gc/store_cassandra_serial_domain_test.go` checks that every
`session.Query` in the three helpers is pinned to `gocql.Serial`, that all three
acquire entry points settle an unknown outcome, the ambiguity classifier, and
the settle behavior. `scripts/library-hard-delete-lease-serial-domain-mutation-validation.sh`
runs seven directed mutations inside the Docker image (pin removed from renew,
release downgraded to `LOCAL_SERIAL`, stale-takeover pin removed, library acquire
not settled, settle skipping the release, a failed settlement release
reported as merely attempted, `CAS_WRITE_UNKNOWN` classified as definite); each goes RED for its own reason. The isolated Cassandra 3-DC harness
(`scripts/library-hard-delete-lease-serial-domain-multidc-validation.sh`) runs
with client sessions configured as `LOCAL_SERIAL`: concurrent contenders from
dc-na and dc-eu never had two owners (`EACH_QUORUM`), an ambiguous contender
never reported success (if it still owns the lease after settlement the round
is logged as the residual above, cleared and re-raced), correct and wrong tokens behaved correctly for
renewal/release, dc-eu acquired after release, and stale takeover still
succeeded. With the pin downgraded to `LOCAL_SERIAL` the same race reports two
owners in most runs. Final Docker run after the rebase onto `main@cd591709c` (2026-09-30): 60/60
pinned races passed, the official harness passed 2/2 on fresh fixtures (one run
exercised and settled an ambiguous acquire), and the `LOCAL_SERIAL` downgrade
reported two owners in 16/20 runs.

This closes only the lease's SERIAL-domain issue. It does not close the
separately tracked stale-owner non-fencing issue or establish GC activation
readiness.

### ISSUE-GC-HARD-DELETE-LEASE-NONFENCING-01: A stale lease owner can resume its final lifecycle batch

**Status**: 🔴 Open — CURRENT-RUNTIME / FOLLOW-UP; also required PRE-GC. Found in the PR #232 final cross-audit (2026-09-24); not part of the PC-D1B.4 decision
**Severity**: High (P1)
**Scope**: CURRENT-RUNTIME / FOLLOW-UP; also required PRE-GC
**Introduced by #232**: No
**Blocks #232**: No — this does not create a valid HEAD/witness
**Affected**: `DELETE /api/v2.1/repos/deleted/:repo_id[/]` (`PermanentDeleteRepo`), `acquireHardDeleteLock` / `renewHardDeleteLock` / `releaseHardDeleteLock`, `hardDeleteLibraryRowsFn`, `restoreDeletedLibrary` and their final lifecycle batches
**Registered**: 2026-09-24, PR #232 final cross-audit

The current authenticated API route is registered independently of GC worker
configuration. `PermanentDeleteRepo` calls
`permanentlyDeleteTrashedLibraryCandidate()`, which synchronously reaches
`hardDeleteLibraryRowsFn()` to delete the canonical library rows. Thus
`GC_ENABLED=false` does **not** protect this permanent-delete/restore lifecycle
from the stale-owner race; asynchronous file-data reclamation is separate.

The renewable lease detects and permits takeover after a stale heartbeat, but
the final restore/permanent-delete batch is not fenced by the generation that
currently owns the lease. A holder can renew, pause past the stale interval,
lose ownership to a competing restore/delete, then resume and apply its old
unconditional batch. For example: permanent delete pauses; restore takes over
and restores the library; the old delete resumes and removes the restored row.
The inverse stale-restore schedule can recreate partial canonical cells after
hard delete.

This is distinct from `ISSUE-GC-HARD-DELETE-LEASE-SERIAL-DOMAIN-01`: using global
SERIAL for the lease LWTs orders acquisition but does not fence a previously
authorized holder's later batch. Treat this as a current-runtime lifecycle/API
follow-up, and close it before destructive GC activation as well. Introduce a
generation/token carried to the final mutation and require that mutation to
prove current ownership, or another equivalent fencing protocol. Characterize
both stale-delete-after-restore and stale-restore-after-delete with the old
owner paused after renewal and resumed only after takeover. Keep it out of
PC-D1B.4; `GC_ENABLED=false` is not protection from this API path.

### ISSUE-GC-USER-HARD-DELETE-RESTORE-SERIALIZATION-01: User restore does not serialize with the user hard-delete lease

**Status**: 🔴 Open — PRE-GC / A1. Found in the PR #234 final cross-audit (2026-09-30)
**Severity**: High (P1)
**Scope**: PRE-GC / A1 (only the GC worker reaches `HardDeleteUser`)
**Introduced by #234**: No
**Blocks #234**: No
**Affected**: `activateUser` (`internal/api/v2/write_helpers.go`, reached from the admin and org-admin user reactivation/restore endpoints), `Worker.processUserCascade` and the org cascade (`internal/gc/worker.go`), `CassandraStore.HardDeleteUser`
**Registered**: 2026-09-30, PR #234 final cross-audit

`processUserCascade` acquires `gc_user_hard_delete_locks` so that, per its own
comment, "a concurrent activateUser (restore) cannot race between the
stale-check above and the final HardDeleteUser write". `activateUser` does not
take that lease: it only checks the lock row with an ordinary (session
consistency) read, then writes the user back to active. `HardDeleteUser` then
deletes `users` unconditionally. Schedule, reachable in a single DC as well:

1. `activateUser` reads no lock row.
2. The worker acquires the user lease and re-reads `deleted_at` (still the
   queued identity), so the post-lock stale check passes.
3. `activateUser` writes the user as active with `deleted_at = null`.
4. `HardDeleteUser` deletes the just-reactivated user.

Across DCs the window widens because the ordinary read in step 1 may not
observe a lease already acquired in another DC. Fix direction: make restore a
lease participant (acquire the same user lease with its own token, re-check
`deleted_at` under it, fence the write and release), or make the final
`HardDeleteUser` conditional on the queued `deleted_at` identity. This is a
separate issue from `ISSUE-GC-HARD-DELETE-LEASE-NONFENCING-01` (stale lease
owner) and from the global SERIAL pin of the lease itself, which #234 closed.
`GC_ENABLED=false` keeps it unreachable today; it must close before activation.

### ISSUE-PC0-CONTENT-RESURRECTION-PUBLICATION-01: Revert/restore paths lack continuous own publication liveness (RevertDirents file admission partially fixed)

**Status**: ✅ Closed for X1 (W2-10 CLOSED-FIX, E1-17, 2026-10-12). Every handler and shape admits only settled history for every block dependency: each block-bearing file's own permanent `fs:`, a strict recursive directory read, original exact P, and a recheck before every HEAD CAS. RED→GREEN was demonstrated for all shapes on the unpublished-source schedule ([E1-17](./E1-17-W2-10-RESIDUAL-HANDLERS.md)). Still open as PRE-GC: concurrent Phase 5/6 history removal between observation and HEAD. No continuous own liveness or ancestry certification is added. History: PC-0 characterization; E1-10e first fixed RevertDirents file admission
**Severity**: High (P1) — W2 writer protocol completeness / funnel migration
**Affected**: `RevertFile`, `RevertDirectory` (`internal/api/v2/files.go`), `RestoreTrashItem`, `RevertDirents` (`internal/api/v2/trash.go`)
**Registered**: 2026-09-10, PC-0 publication-protocol characterization audit

**E1-6 update (2026-10-04)**: [retained-history RevertFile characterization](./E1-6-REVERTFILE-PUBLICATION-SAFETY.md)
shows that productive deletion from HEAD keeps the historical permanent fs:
pin. With its own upload up: lapsed, the real worker skips the candidate before
claim/global proof on a positive local reference. One/two-block and real CAS
retry controls retain original P/K. This does not prove continuity if retention
removes metadata/refs concurrently; no extant-history COMMITTED/TERMINAL RED
was reproduced and no runtime migration/fix or W2-10 closure is claimed.

**E1-10 RestoreTrashItem update (2026-10-05)**: [single-file retained-history evidence](./E1-10-RESTORETRASH-RETAINED-HISTORY.md)
measures productive upload/delete/restore with one plaintext block. The sole
historical fs: stops the real worker before claim/D, and restore preserves exact
P/K/bytes. A real competing HEAD forces CAS retry preserving both entries.
This is positive evidence only for that retained schedule; no retirement RED,
new fence or general W2-10 closure. The existing P1 remains OPEN, including
concurrent retention/cleanup and the other resurrection funnels.

**E1-10b directory update (2026-10-05)**: [single-directory retained-history evidence](./E1-10B-RESTORETRASH-DIRECTORY-HISTORY.md)
measures one child/plaintext block. Real directory deletion retains the child fs:
and productive GC stops before claim/D. Actual restore preserves full named
HEAD/root/DIR1/FILE_FS1/P/K/bytes; competing HEAD forces retry preserving both.
No production fix; separate gate and isolated child leave #259's contract intact.
Only the measured retained-history subset is covered. This P1 remains OPEN for
broader resurrection shapes and retention/Phase5/6/R31 interactions.

**E1-10c RevertDirectory update (2026-10-06)**: [retained-directory evidence](./E1-10C-REVERTDIRECTORY-RETAINED-HISTORY.md)
measures one child/plaintext block with historical DIR1 captured outside retry.
Inherited child fs: prevents real worker claim/D; actual revert preserves named
HEAD/root/DIR1/FILE_FS1/P/K/bytes, with real HEAD conflict/retry preserving both.
Only integration scheduling seams added; no new liveness/fence. This P1 remains
OPEN for RevertDirents, broader shapes/policies and retention/Phase5/6/R31.

**E1-10d RevertDirents update (2026-10-06)**: [single-dirent evidence](./E1-10D-REVERTDIRENTS-RETAINED-HISTORY.md)
measures one root file/path/plaintext block with retained history. Real local
positive fs: read settles candidate before claim/D. Actual form handler reuses
FILE_FS1/P/K/bytes; competing HEAD causes per-item retry preserving both files.
No runtime liveness/fence fix. Only the measured retained-history subset is
covered; this P1 remains OPEN for broader batch/shapes/policies, retention,
Phase5/6 and R31. Prior RevertFile disposition is unchanged.

#### Problem

PC-0 originally listed these four handlers among the tree mutations that
"do not stage new block liveness". The observation is literally true — they
stage nothing — and that is the problem. Each one reads a historical commit,
lifts an `oldEntry` / fs_object out of it, inserts it into the current tree,
creates a new commit, and CASes HEAD:

```text
current HEAD:      does not depend on FS1
historical commit: contains FS1 → blocks P1…
RestoreTrashItem / RevertFile
    ↓
new HEAD:          depends on FS1 → P1… again
```

GC Phase 6 is the other side of the same race: its keep set is computed at
scan time and its fs_object items carry no library guard and no execute-time
reachability recheck, so a HEAD that re-references an fs_id outside the keep
set between scan and execution loses it (PC-D1B.4 inventory, finding F3).
Tracked and reproduced (in-flight Sync publication) as
`ISSUE-GC-PHASE6-FSOBJECT-EXECUTION-TOCTOU-01` since 2026-10-19.

That is a positive block-dependency delta (PC-0's own
`LogicalPositiveBlockDelta` definition) with `BORROWED` provenance: the only
liveness those blocks have is the historical `fs:<library>:<fs_id>`
reference, which trash retention / version TTL GC is entitled to remove
concurrently. No `up:` pin, no `pub:`, no durable repair intent. E1-10e adds scoped
source admission and exact-P validation to RevertDirents files; the other
handlers still lack this fence. This is weaker than cross-repo copy, which at least stages `pub:` and
queues repair.

#### Scope / disposition

Reclassified in PC-0 §3.2 → §3.5 as content-resurrection publication paths
(R1–R4) and inventoried as `pc0HeadContentResurrection`;
`TestPC0ContentResurrectionPathsObservedWithoutPublicationSeams` freezes the
observed absence of seams and forces reclassification as a block-publication
funnel when one of them is migrated. The fix belongs to W2 / funnel
migration: the future coordinator must treat them as `BORROWED` adapters that
acquire a durable own pin on the resurrected fs_object's blocks (and
`ExpectedP`) before staging. Do not fix the four routes in the
characterization PR.

#### Related

- `docs/PUBLICATION-PROTOCOL-CHARACTERIZATION.md` §3.5, §5 (R1–R4), §10
- `ISSUE-PC0-EXACT-P-FUNNEL-GAP-01` — the same class of gap for funnels that do stage
- `ISSUE-GC-PHASE5-CASCADE-SHARED-FSOBJECTS-01` — the GC side that can remove the borrowed reference

### ISSUE-GC-PHASE5-CASCADE-SHARED-FSOBJECTS-01: Phase 5 expired-version cascade deletes fs_objects shared with the live HEAD

**Status**: **CLOSED-FIX for the unsafe commit-to-tree cascade on merge of this change** (2026-10-17).
[Phase 5 plan and evidence](./GC-PHASE5-SHARED-FSOBJECTS.md): unguarded commit retirement
is metadata-only. Shared trees/refs are preserved; guarded library destruction
keeps its cascade. **PRE-GC reclamation disposition remains OPEN**: exclusive
fs_objects/refs are deliberately retained until a sharing-aware pass; no retention
bound or Phase 6 safety is certified. Ordinary GC remains OFF.
**Severity**: Historical P0 latent for the reproduced destructive cascade; preserved, not downgraded. This fix does not authorize activation.
**Scope**: PRE-GC runtime follow-up — discovered by PC-0 (2026-09-10); fixed for commit-to-tree cascade here. Phase 6, concurrent commit publication/destruction and hard-delete fencing remain separate obligations.
**Affected**: `Scanner.scanExpiredVersions` (Phase 5), `Worker.processCommit`, `Worker.processFSObject`, `removeFSObjectBlockReferences` in `internal/gc`

#### Historical problem (reproduced RED before the fix)

Phase 5 walks the HEAD parent chain to build a keep-set of **commits** and
enqueues every other commit older than `version_ttl_days` as `ItemCommit`.
The worker then:

```text
processCommit
  → enqueue root fs_object (cascade)
  → DeleteCommit
processFSObject
  → enqueue children
  → remove fs: block references
  → DeleteFSObject
```

Nothing asks whether the content-addressed `fs_id` being deleted is still
reachable from the current HEAD (or from any retained commit). fs_objects are
shared across commits by construction, so a dangling commit's tree overlaps
almost entirely with HEAD's. The items carry no library hard-delete guard
mode either, so `acquireLibraryDeleteGuard` is effectively a no-op for them.
Phase 6 (`scanAutoDeleteExpiredObjects`) builds a keep-set of fs_objects
across retained commit trees; Phase 5's cascade has no equivalent.

Dangling commits exist in normal operation: v2 CAS losers keep their
`commits` row (`CleanupFailedPublishAttempt` does not delete it), Sync
`PutCommit` rows whose client never promoted them, and every Sync auto-merge
target commit.

```text
commit C-old (not HEAD-reachable): root-X → dir-D → file-F
commit C-head (HEAD):              root-H → dir-D → file-F   (same fs_ids)
Phase 5: enqueue C-old → cascade root-X → dir-D, file-F deleted → C-head broken
```

The former MockStore characterization
`TestPC0Characterization_Phase5CascadeRemovesFSObjectsSharedWithHEAD` is inverted
into `TestPhase5CommitRetirementPreservesFSObjectsSharedWithHEAD`. The real
Cassandra/S3 `TestPhase5SharedTreesRealStack` reproduces loss with current identity
gateways and passes after the fix for shared subtrees and identical roots,
including an old HEAD ancestor and a recent off-chain commit.

#### Why it matters beyond GC

It is the concrete counterexample that removes "ordinary GC reachability
already protects inherited dependencies" from the options of
`ISSUE-PC0-INHERITED-DEPENDENCY-CONTINUITY-01`, and it is the negative side
of `ISSUE-PC0-CONTENT-RESURRECTION-PUBLICATION-01`.

#### Accepted fix and remaining reclamation obligation

The worker now deletes only the commit row for every unguarded ItemCommit,
including Phase 5, `CleanRepoTrash -> Service.EnqueueCommits` on live libraries
when the GC hook is registered, and already-queued unguarded work. CleanRepoTrash
gets the same shared-tree protection and exclusive-tree retention trade-off.
Pre-011 rows with no mode/flag decode as unguarded; a true legacy deleted-check
flag still resolves to the guarded library path. Root/descendant fs_objects and permanent refs
are not enqueued or mutated by that retirement. Guarded library cleanup retains
its existing behavior, including the legacy deleted-check flag. No queue/schema
migration, API field/status change or GC-default change is required.

Exclusive trees can now be over-retained when no sharing-aware cleanup runs;
that is the chosen safety trade-off, not a reclamation fix. Phase 6 remains
unsafe under its execute-time TOCTOU and is not endorsed as an activation-ready
replacement. Previously queued fs_object descendants are not retroactively
classified, and previously deleted data are not repaired. Resolve retention and
metadata-destruction fencing under the full PRE-GC/A1 gate before enabling GC.

`CleanRepoTrash` already queues commits; its historical no-op diagnosis needs
[separate reconciliation](./TECHNICAL-DEBT.md#phase-5-follow-ups-suite-wiring-and-trash-clean-tracking).
Further trash cleanup must not bypass this protection by directly enqueuing
shared fs_objects. This change does not certify the Trash HTTP path separately.

#### Related

- `ISSUE-PC0-INHERITED-DEPENDENCY-CONTINUITY-01`, `ISSUE-PC0-CONTENT-RESURRECTION-PUBLICATION-01`
- `ISSUE-LIB-RETENTION-01`, `ISSUE-TRASH-CLEAN-01`
- `docs/PUBLICATION-PROTOCOL-CHARACTERIZATION.md` §6 PUBL-10, §10, §15

### ISSUE-GC-PHASE6-FSOBJECT-EXECUTION-TOCTOU-01: Phase 6 deletes fs_objects that a HEAD published after the scan depends on

**Status**: 🔴 Open — RED reproduced on main@bb89d7b12 with real Cassandra/S3 (2026-10-19); no fix. [Plan and evidence](./GC-PHASE6-FSOBJECT-TOCTOU.md)
**Severity**: High (P1) — HEAD metadata loss and permanent-reference removal; physical byte loss not observed (see below)
**Scope**: PRE-GC / A1. Ordinary deployments run `GC_ENABLED=false`; no current-runtime claim
**Affected**: `Scanner.scanAutoDeleteExpiredObjects` (Phase 6), `Worker.processFSObject`, `removeFSObjectBlockReferences` in `internal/gc`
**Registered**: 2026-10-19; the execute-time half of PC-D1B.4 finding F3, previously recorded only under `ISSUE-PC0-CONTENT-RESURRECTION-PUBLICATION-01`

Phase 6 computes its keep set (HEAD ancestry plus commits newer than
`auto_delete_days`) from a commit listing, then lists fs_objects and enqueues
every one outside the set as an unguarded `ItemFSObject`. Nothing binds the
item to the HEAD it was computed against or to the object's age, and the
worker's unguarded fence is a no-op: it removes the permanent `fs:` references
and the fs_object row without re-establishing that the selection still holds.
After #314 Phase 6 is the only productive origin of unguarded `ItemFSObject`;
the worker's directory cascade propagates that mode to children
(`ISSUE-GC-PHASE6-DIRECTORY-CASCADE-SHARED-FSOBJECTS-01`).

Reproduced schedule: a productive Sync `PutCommit` + `RecvFS` lands between the
selector's commit listing and its fs_object listing; the client then completes
`PutBlock` and `PUT commit/HEAD`; the real worker executes the stale selection.
HEAD's root and file fs_objects and the permanent `fs:<repo>:<file>` reference
are deleted while HEAD depends on them (12/12 runs, 3 with race; 4 on the final test source). The same
writer landing before the scan keeps everything (control). The Sync `up:` pin
(48h TTL) was the only remaining reference, so no block candidate was created and
the canonical row and S3 bytes survived in this run; whether its later expiry
leads to physical deletion was not measured. Restore/revert re-referencing is
another entry to the same race and was not run.

Not closed by W2-10 (that row is about writers reinstalling retired P; its X1
closure is unchanged) or by the certification-window fence (PC-D1B.4 §15). The
fix is a separate PR whose behaviour change is a maintainer decision; options
are recorded in the plan.

#### Related

- `ISSUE-PC0-CONTENT-RESURRECTION-PUBLICATION-01` (F3), `ISSUE-GC-PHASE5-CASCADE-SHARED-FSOBJECTS-01`
- `ISSUE-GC-PHASE6-DIRECTORY-CASCADE-SHARED-FSOBJECTS-01`, `ISSUE-AUTO-DELETE-EXPIRY-CONTRACT-01`
- [X1-CRITICAL-PATH §6](./X1-CRITICAL-PATH.md#6-pre-gc-list-a1-prerequisites)

### ISSUE-GC-PHASE6-DIRECTORY-CASCADE-SHARED-FSOBJECTS-01: Phase 6's worker cascade deletes children that HEAD still reaches

**Status**: 🔴 Open — found in the P6-1 audit and reproduced on main@bb89d7b12 with real Cassandra/S3 (2026-10-19); no fix. [Plan and evidence](./GC-PHASE6-FSOBJECT-TOCTOU.md#1b-directory-cascade-without-the-keep-set-found-in-this-prs-audit)
**Severity**: P0 latent, provisional: the same pattern and the same classification as `ISSUE-GC-PHASE5-CASCADE-SHARED-FSOBJECTS-01`; deterministic, no race. The maintainer confirms
**Scope**: PRE-GC / A1. Ordinary deployments run `GC_ENABLED=false`; no current-runtime claim
**Affected**: `Worker.processFSObject` (directory child enqueue), `removeFSObjectBlockReferences`, `enqueueZeroRefBlocks` in `internal/gc`; selection by `Scanner.scanAutoDeleteExpiredObjects`

The Phase 6 selector checks its keep set only for the fs_objects it lists. When
the worker processes a selected directory, it enqueues every child not already
pending with the parent's unguarded mode and never consults the keep set, so a
child HEAD still reaches is deleted. Selected directories that share children
with HEAD are ordinary: the root of a commit outside HEAD's ancestry once it is
older than `auto_delete_days`, and received directories that no commit
references.

Reproduced with HEAD published before the scan and a received orphan directory
whose child is HEAD's file: the scan selects only the orphan, then the worker
deletes HEAD's file fs_object and its permanent `fs:` reference (4/4 runs, 1
with race). With the Sync `up:` pin explicitly lapsed first (its 48h TTL, as for
a file that has been in HEAD longer), zero references remain and the worker
itself puts HEAD's block in `gc_block_candidates` and the queue. The block item
was not executed, so S3 deletion is not measured. The off-chain-commit form was
not run.

A fix must stop the cascade from reaching objects outside its selection's
contract. The scanner-side keep set alone cannot. P6-2 option A (refuse every
unguarded `ItemFSObject` before children are enqueued) covers it; an age
criterion does not.

### ISSUE-AUTO-DELETE-EXPIRY-CONTRACT-01: `expires_at` promises deletion that Phase 6 never performs for HEAD files

**Status**: 🟡 Open — found 2026-10-19 while reviewing Phase 6; pre-existing, not fixed
**Severity**: Medium (P2) — product contract
**Scope**: FOLLOW-UP / GENERAL
**Affected**: `ListDirectoryV21` (`internal/api/v2/files.go`), `Scanner.scanAutoDeleteExpiredObjects`

The directory listing reports `expires_at = mtime + auto_delete_days` for files
in the current tree. Phase 6 keeps every object reachable from HEAD, so it never
removes a file that is still in HEAD, even with GC enabled: the countdown
reaches zero and nothing happens. Phase 6 behaves as history pruning, not as
deletion of old current files. No frontend consumer of this field was found by
search. Define the intended `auto_delete_days` contract (confirm the Seafile
semantics first) before Phase 6 reclamation is redesigned.

### ISSUE-PUBLISH-REPAIR-REACHABILITY-CONVERGENCE-01: The bounded repair walk can leave UNKNOWN unconvergeable while `pub:` still expires

**Status**: ✅ Closed 2026-09-12 — resumable SERIAL-anchored walk + explicit `pub:` renewal while unresolved; not a #213 regression
**Severity**: High (P1) — R31 convergence; dangerous only once GC is destructive
**Scope**: PRE-X1 / PRE-GC follow-up
**Affected**: `classifyPublishedCommitReachability`, `publishedCommitReachabilityMaxNodes = 1024`, `publishedCommitReachabilityTimeout = 30s`, retry backoff up to 6 h in `internal/api/v2/publish_repair.go`; `PublishAttemptReferenceTTLSeconds = 35d`

#### Problem

#213 closed a narrower property — inconclusive evidence ⇒ `UNKNOWN` ⇒ retain
— and that remains correct. The new problem is that `UNKNOWN` may never
converge. The classifier walked at most 1024 ancestors **from the current
HEAD** toward the target:

```text
repair target = C0
HEAD advances:  C1 → C2 → … → C1500     (an active library)
classifier:     looks at ≤1024 ancestors of HEAD → C0 never appears → UNKNOWN
```

Every retry started from a HEAD that was further away, so the situation only
degraded. The window is realistic: parent reads are `EACH_QUORUM`, so one
unavailable DC yields `UNKNOWN` and the retry backoff climbs to 6 h; a busy
library accumulates >1024 commits during a multi-hour outage. Meanwhile the
attempt's `pub:` references still expire at 35 d, after which the block is
kept alive by nothing if promotion never happened.

#### Fix

The repair row is the durable unit. Migration 024 adds
`reachability_anchor_head_commit_id`, `reachability_cursor_commit_id`, and
`reachability_anchor_exhausted` with no TTL. The first pass records one SERIAL
canonical HEAD as the anchor and
starts the cursor there. Each ancestry chunk walks at most 1024 EACH_QUORUM
parents from the cursor under the existing 30-second context. SERIAL HEAD is
observed when creating or replacing that anchor; later retries of the same
snapshot do not re-read HEAD. A visit that clean-walks to genesis may
re-observe HEAD and walk a second chunk (at most two HEAD observations /
2048 parent reads). A re-anchor CAS loser that reloads an already-exhausted
newer snapshot re-enters re-anchor instead of replaying that prefix. The
walk persists the next unread
commit after any safely completed prefix (full 1024-node exhaustion, timeout,
or a later parent-read error) using a SERIAL LWT
(`IF created_at = loaded AND anchor = expected AND cursor = expected`) so
concurrent workers cannot regress progress and a stale CAS cannot mutate a
requeued generation of the same primary key. A failure on the first node does
not advance. That LWT is
never cleanup authority; INSERT/DELETE of the repair row stay ordinary.
`created_at = loaded` closes a finished DELETE+requeue ABA when the requeued
row's Cassandra timestamp differs. CQL TIMESTAMP is millisecond precision, so
a same-millisecond requeue can reuse `created_at`. Ordinary queue
INSERT/DELETE are not in the same Paxos protocol as progress, and those LWTs
share the 32 bucket partitions
(`ISSUE-PUBLISH-REPAIR-PROGRESS-PAXOS-DOMAIN-01`).
Target found ⇒ existing REACHABLE promotion. A missing repair row is a
terminal no-op: it is not positive reachability and must not promote or renew
`pub:`. Root without the target, cycles, malformed ancestry, parent errors, and
timeouts stay `UNKNOWN` and retain — there is still no durable global-negative
witness. After a clean walk to genesis, `reachability_anchor_exhausted` is
persisted before the SERIAL HEAD re-read so a deadline on that read cannot
replay the same prefix; a newer SERIAL HEAD may then replace the exhausted
snapshot so a repair that ran before the target was published can still
converge in the same visit (a second SERIAL HEAD plus a second 1024-node
chunk under the remaining 30s budget; timeout, 1024-node bound, EACH_QUORUM
error, cycle, and malformed ancestry do not re-anchor). While the row is
unresolved, each visit can write/refresh a per-row `pub:<repo:commit:fsID>` for
`staged_block_ids` (`AddPublishAttemptReferences`) **after** classification.
That identity is not `pub:<commitID>`: v2 already uses the commit as the
publication attempt, shared by every file of the commit. Settling one repair
therefore cannot drop a sibling's renewal. An unresolved visit can
write/refresh that `pub:` after classification while the row is still pending.
That is not gap-free: if prior liveness expires before that write, a zero-ref
interval exists even if the later renewal recreates `pub:`
(`ISSUE-PUBLISH-REPAIR-RENEWAL-AFTER-CLASSIFY-01`). If discovery starts after
expiry, the gap already existed
(`ISSUE-PUBLISH-REPAIR-DISCOVERY-SCALE-01`). The 6 h value caps only process-local retry backoff, not discovery visit
interval. The shared worker best-effort
removes that repair-owned identity *before* deleting the durable row. Ordinary
Sync success only deletes repair rows and does not walk blocks to DELETE
repair-owned `pub:` (`ISSUE-PUBLISH-REPAIR-OWNED-PUB-CLEANUP-RACE-01`).
Owner-sweep still uses the
#213 FromStore classifier.

#### Scope / disposition

Closed for moving-HEAD **reachability convergence** on the shared published-block-reference
repair worker. Do not reopen #213. This does not prove gap-free `pub:`
continuity for an arbitrarily long repair. Known-loser durability, `pub:`
zero-ref discovery, repair discovery scale, renew-after-classify liveness,
bucketed-progress Paxos contention, PC-2, GC behavior, and TTL-bounded leftover repair-owned `pub:` after
concurrent settlement remain separate.

#### Related

- `ISSUE-PUBLISH-REPAIR-REACHABILITY-01` (closed, narrow), `ISSUE-GC-PUB-REF-ZERO-REF-01`, `ISSUE-PUBLISH-REPAIR-DISCOVERY-SCALE-01`, `ISSUE-PUBLISH-REPAIR-OWNED-PUB-CLEANUP-RACE-01`, `ISSUE-PUBLISH-REPAIR-RENEWAL-AFTER-CLASSIFY-01`, `ISSUE-PUBLISH-REPAIR-PROGRESS-PAXOS-DOMAIN-01`
- `docs/PUBLICATION-PROTOCOL-CHARACTERIZATION.md` §9, §15

### ISSUE-PUBLISH-REPAIR-OWNED-PUB-CLEANUP-RACE-01: Eager repair-owned `pub:` cleanup is best-effort against concurrent renewal

**Status**: Open follow-up (2026-09-13) — accepted over-retention; not a #219 R31-C1 blocker
**Severity**: Medium (P2) — storage retention until the 35-day `pub:` TTL; not under-retention
**Scope**: PRE-GC / R31 residual of repair settlement (PRE-X1 until [E1-16](./E1-16-POST-D-CONTRACT-ALIGNMENT.md), 2026-10-10: over-retention of a logical reference is not an X1 dependency)
**Affected**: `renewPublishedBlockReferenceRepairLivenessIfPending`, `settlePublishedBlockReferenceRepair`

#### Problem

The shared repair worker removes per-row `pub:<repo:commit:fsID>` and then
deletes the durable repair row. That order closes the crash window where the
row vanished first. It does not close a concurrent renewal of **the same
row**:

```text
worker: row pending = true
cleanup: remove pub:<repo:commit:fsID>
worker: AddPublishAttemptReferences(pub:<repo:commit:fsID>)
worker: row still pending
cleanup: DELETE repair row

fs: permanent              present
repair row                 absent
pub:<repo:commit:fsID>     present until TTL
```

Ordinary Sync success does not attempt this cleanup: leftover repair-owned
`pub:` after a successful Sync is accepted until TTL, so the hot path does
not pay N sequential DELETEs for identities the worker usually never created.
After successful publication the leftover `pub:` is safe over-retention,
analogous to the existing `up:` TTL policy. Zero ownerless `pub:` would need
durable settling/resolved coordination that the renewal path respects; that
is a separate protocol, not R31-C1.

#### Disposition

Keep worker remove-then-delete as best-effort crash-window hygiene. Do not
put per-block repair-owned `pub:` DELETE on the Sync success path. Do not
claim settlement can never leave ownerless repair-owned `pub:`. Closing the
race belongs with `ISSUE-GC-PUB-REF-ZERO-REF-01` / a future settlement state,
not a widening of the reachability classifier.

#### Related

- `ISSUE-PUBLISH-REPAIR-REACHABILITY-CONVERGENCE-01` (closed), `ISSUE-GC-PUB-REF-ZERO-REF-01`, `ISSUE-PUBLISH-REPAIR-RENEWAL-AFTER-CLASSIFY-01`

### ISSUE-PUBLISH-REPAIR-RENEWAL-AFTER-CLASSIFY-01: Repair-owned `pub:` is renewed after the bounded classifier, not before it

**Status**: **CLOSED-EVIDENCE — covered current-version pre-D safety contract** (E1-11). Non-expiring repair acquired by the covered writer before final exact-P/HEAD vetoes a new D through the destructive EACH_QUORUM scan, even after real up:/pub: expiry during/after classification. REACHABLE promotes fs: before clearing the guard; UNKNOWN/classifier errors retain it; destructive scan errors fail closed. See [plan and evidence](./E1-11-CURRENT-RUNTIME-REPAIR-LIVENESS.md).
**Severity**: Historical P1 destructive-gap claim superseded for this covered mechanism; no claim for a repair acquired after COMMITTED D or an unadopted funnel.
**Scope**: Current-version pre-D safety only. W2-12/13/14 X1 safety was closed by E1-16 (2026-10-10). Concurrent cleanup/over-retention, discovery/convergence bounds and broader R31 convergence remain OPEN as PRE-GC. No health gate or renewal-before-classify runtime is added.

The diagnosis and rejected designs below describe the pre-#239 premise. Pub: can still expire and be recreated; zero real refs alone no longer authorize a new D while the matching durable repair survives. #220/#222/#224 remain rejected historical designs. Their design gate still applies to any future runtime redesign, not to this evidence-only rebaseline.
**Affected**: `repairPublishedBlockReferenceRepair`, `classifyPublishedBlockReferenceRepairCommitResumable`, `renewPublishedBlockReferenceRepairLivenessIfPending`
**Rejected approaches**: PR #220 (`fix/r31-publish-repair-renew-before-classify`, closed 2026-09-17), PR #222 (`fix/r31-renew-before-classify-minimal`, last head `eeb2eba7e`, closed 2026-09-18). Nothing from either is in `main`

#### Historical problem (pre-#239)

The current visit order is hydrate → classify (SERIAL HEAD, up to 30s of
EACH_QUORUM parent reads, progress LWTs) → then, on UNKNOWN/error, call
`AddPublishAttemptReferences` for `pub:<repo:commit:fsID>`. That primitive
does not check that a valid `pub:` still exists; it writes/recreates the pin
while the repair row is pending. REACHABLE likewise promotes `fs:` only after
the walk.

If prior liveness expires before that write, a zero-ref interval exists even
when the later renewal succeeds. Under the pre-#239 premise, that gap could
become a delete once GC was destructive; recreating `pub:` afterwards did not
close the interval. E1-11 supersedes that destructive-gap claim for the covered
current-version contract: zero real refs do not authorize a new D while the
matching non-expiring repair survives.

This is not a regression versus `main` (main had no UNKNOWN renewal) and does
not invalidate the resumable walk. R31-C1 closed moving-HEAD **convergence**,
not gap-free liveness handoff. A visit that starts after the last valid `pub:`
has already expired is a discovery/TTL problem
(`ISSUE-PUBLISH-REPAIR-DISCOVERY-SCALE-01`, `ISSUE-GC-PUB-REF-ZERO-REF-01`)
and would remain open even if renewal moved before classify.

#### Rejected approaches (2026-09-17/18) — summary; canonical record elsewhere

Two runtime attempts were closed without merge. The full postmortem, the
counterexamples, the invariants and the mandatory design gate live in
[PUBLISH-REPAIR-LIVENESS-REJECTED-DESIGNS.md](./PUBLISH-REPAIR-LIVENESS-REJECTED-DESIGNS.md);
this entry only summarizes.

```text
#220 rejected:
  pre-walk durable 35d renewal widened main's crash window (an ownerless
  35-day pin for a visit that crashes after a concurrent clear); the
  durable cleanup-witness protocol built to recover it was not shown to
  combine unlimited retries with structurally bounded durable state. Not
  established: that explicitly accepting and bounding that TTL-bounded,
  stable-identity over-retention residual is invalid.

#222 rejected:
  a transient, write-only pub:<...>:walk pre-pass avoided durable-state
  growth but inserted work before main's unbounded stable-owner handoff
  (N LOCAL_QUORUM INSERTs bounded only by database.timeout); schedules
  exist where main retains continuous liveness and #222 creates a
  zero-ref interval (partial walk fan-out; successful pre-pass + long
  handoff).
```

The earlier "intended follow-up" here (renew the durable pin immediately
after hydrate, before the walk) is exactly #220's first shape and is
withdrawn. The two invariants any next design must prove first:

```text
1. MAIN-LIVENESS NON-REGRESSION — if main keeps a block continuously live
   under an admissible schedule, the replacement must not create a
   zero-reference interval.
2. BOUNDED DURABLE STATE UNDER UNLIMITED RETRIES — any durable state
   introduced by retries must remain structurally bounded; if correctness
   depends on reclaiming, deleting or reusing it, the exact
   ownership/coverage must be proven first; TTL-bounded over-retention may
   be explicitly accepted when it cannot create under-retention.
```

Historical observation from those audits, closed by the scoped follow-up
below (and independent of whatever shape the next renewal design takes):
`ISSUE-PUBLISH-REPAIR-GONE-CHECK-XDC-AUTHORITY-01` — before closure, the
post-write gone-check of the 35d renewal decided absence on the
session-consistency read and then removed the repair-owned pin. The fix now
treats that absence as non-authoritative and lets the pin expire under its
existing TTL.

Historical operational model at the design-record parent (canonical record §8):
the 35d `pub:` TTL is a
crash/recovery backstop — every content funnel attempts `stage pub: → queue
repair → HEAD → request-local fs: promotion attempt → clear repair on success` inside the request,
so the durable repair covers only the abnormal post-HEAD interval; the row
has no TTL; the sweep runs on every node (startup, then 1 min) behind a
5 min advisory lease with 5 min – 6 h process-local retry hints; that
expected cadence is not a proven bound. Design hypothesis recorded there as
UNPROVEN: liveness maintenance separate from reachability classification;
its smallest in-visit form (refresh the stable pin as a fan-out before the
classifier) was **rejected** by the design proof of PR #224
([R31-REPAIR-LIVENESS-DESIGN-PROOF.md](./R31-REPAIR-LIVENESS-DESIGN-PROOF.md)):
a failable pre-step ahead of `main`'s handoff delays the blocks it fails
to cover unless an independent owner or a proven temporal invariant covers
them — V0 and #222 had neither. At that point no runtime redesign existed and
this issue remained OPEN. E1-11 later established that renewal-before-classify
is not required for the covered current-version pre-D safety contract.

Discovery/completion bounds (W2-13), candidate creation at the last `pub:` expiry
(W2-14), known-loser durability (W2-12), and the registered cleanup/retention
follow-ups remain separate OPEN contracts. Discovery after TTL expiry does not
by itself authorize a new D while the matching durable repair survives. This
evidence does not cover an unadopted funnel or a repair acquired after COMMITTED
D. Broader PRE-GC requirements remain; no classifier redesign is prescribed.

#### Related

- `ISSUE-PUBLISH-REPAIR-REACHABILITY-CONVERGENCE-01` (closed), `ISSUE-PUBLISH-REPAIR-DISCOVERY-SCALE-01`, `ISSUE-GC-PUB-REF-ZERO-REF-01`, `ISSUE-PUBLISH-REPAIR-OWNED-PUB-CLEANUP-RACE-01`
- [PUBLISH-REPAIR-LIVENESS-REJECTED-DESIGNS.md](./PUBLISH-REPAIR-LIVENESS-REJECTED-DESIGNS.md) (canonical record of #220/#222 and the design gate)
- `ISSUE-PUBLISH-REPAIR-GONE-CHECK-XDC-AUTHORITY-01` (closed)

### ISSUE-PUBLISH-REPAIR-GONE-CHECK-XDC-AUTHORITY-01: Repair-owned `pub:` cleanup decides absence on a local read

**Status**: CLOSED (2026-09-19; fixed by the scoped gone-check follow-up) — PRE-X1 / PRE-GC
**Severity**: Medium (P2) — historical defect: destructive authority was local, which violated the cleanup rule established in [PUBLISH-REPAIR-LIVENESS-REJECTED-DESIGNS.md](./PUBLISH-REPAIR-LIVENESS-REJECTED-DESIGNS.md) §2.6 / §4.8.
**Scope**: PRE-X1 / PRE-GC
**Affected**: `renewPublishedBlockReferenceRepairLivenessIfPending`, `publishedBlockReferenceRepairStillPending`, `loadLivePublishedBlockReferenceRepair` (session consistency, `LOCAL_QUORUM` in every shipped profile), `cleanupFailedPublishRemoveAttemptReferencesFn`

#### Problem

Before closure, after renewing `pub:<repo:commit:fsID>` for a pending row, the
visit re-reads the repair row at session consistency; if that local read no longer sees the
row it removes the references it just wrote. A local absence is thus treated
as proof that the repair row is globally gone. Under the multi-DC rule that
#220/#222 established — *local absence is never destructive authority;
removing liveness on absence needs global-enough authority and must fail
closed when that authority is unavailable* — this decision is made with the
wrong authority. #222 prototyped an `EACH_QUORUM` decider
(`publishedBlockReferenceRepairGoneForCleanup`: local read may only retain,
local absence escalated to an `EACH_QUORUM` read, unavailable DC keeps the
pin) with a directed real 3-DC leg; none of it is in `main`.

#### Resolution

The post-renewal gone branch now returns the existing gone sentinel without
calling `cleanupFailedPublishRemoveAttemptReferencesFn`. The stable
repair-owned `pub:<repo:commit:fsID>` therefore expires under its existing
35-day TTL. The repair-row read remains non-authoritative, but it no longer
controls a destructive liveness transition.

#### Why not under-retention today

Before closure, the compensation was reachable only after `hydrate` observed
the row on the same local read, so the row must have become locally absent in between —
i.e. a tombstone reached the local DC. For every writer of that tombstone
in `main`, removing the repair-owned pin cannot under-retain a live
publication, each for its own reason:

```text
successful publication clear     fs: already owns the live blocks
                                 (clear runs after promotion)
known-loser / failed-attempt     the publication is proven non-live:
clear                            CleanupFailedPublishAttempt deletes its
                                 commit, removes its publication-attempt
                                 refs and releases its pending ownership;
                                 shared / content-addressed fs_object
                                 metadata is NOT deleted here
                                 (releasePendingPublishedFileOwner), and no
                                 live publication owner needs continuity
                                 for this losing attempt
worker REACHABLE settlement      fs: is installed before the pin is removed
                                 and the row deleted
progress-only residue reap       the row has no staged blocks
requeue of the same identity     the requeuing publisher stages a fresh
                                 publication-attempt pin first (35d; v2-like
                                 pub:<commitID>, Sync pub:<publishAttemptID>)
```

The defect is therefore that the *authority* is wrong, not that a loss has
been shown. Each of those preconditions is an implicit dependency that the
next renewal design, or any new clear path, can break silently.

#### Disposition and remaining follow-up

This issue is closed by the bounded-over-retention fix above. The next design for
`ISSUE-PUBLISH-REPAIR-RENEWAL-AFTER-CLASSIFY-01` **must account for** this
cleanup-authority decision (the "cleanup authority" column of its phase
table); that issue, `ISSUE-PUBLISH-REPAIR-OWNED-PUB-CLEANUP-RACE-01`, X1
and GC remain open. No `EACH_QUORUM` absence decider was added.

**Dated follow-up (2026-10-07, E1-15A):** E1-15A measured that keeping the
renewed pin can leave a durable reference after exact COMMITTED D
(`ISSUE-PUBLISH-REPAIR-STALE-RENEWAL-AFTER-CLEAR-01`). The local gone
observation is still never destructive. It is now escalated to an
EACH_QUORUM read; confirmed global absence withdraws exactly the renewed
pin, and an unavailable read retains it as above.

#### Related

- `ISSUE-PUBLISH-REPAIR-RENEWAL-AFTER-CLASSIFY-01`, `ISSUE-PUBLISH-REPAIR-OWNED-PUB-CLEANUP-RACE-01`
- [PUBLISH-REPAIR-LIVENESS-REJECTED-DESIGNS.md](./PUBLISH-REPAIR-LIVENESS-REJECTED-DESIGNS.md) §1, §2.6, §4.8

### ISSUE-PUBLISH-REPAIR-STALE-RENEWAL-AFTER-CLEAR-01: A stale repair visitor could renew `pub:` after a legitimate clear and COMMITTED D

**Status**: 🟠 Mitigated (2026-10-07, E1-15A), P1 PRE-GC OPEN residual (PRE-X1 until E1-16, 2026-10-10) — the post-D pin is withdrawn only when the global check and the withdrawal both succeed; the write itself is not fenced
**Severity**: High (P1) — durable logical reference of L added after exact COMMITTED D(P1); practical harm limited to dead-pin over-retention, no reachable HEAD and no P1 reinstall
**Scope**: PRE-GC / E1-15 shared repair cancellation. [E1-16](./E1-16-POST-D-CONTRACT-ALIGNMENT.md) aligned the X1 invariant with G4 contract 5: a post-D reference of L is not a P1 dependency
**Affected**: `renewPublishedBlockReferenceRepairLivenessIfPending` (shared by every repair funnel)

#### Problem

The UNKNOWN visit re-checks the durable repair row twice before renewing, but
the renewal write is unfenced. Consider a visitor that passed its last
re-check (row present). Before it writes, a real known-loser cleanup clears
the row, the real `up:` expires, Phase 0 creates a natural candidate and the
productive worker reaches exact COMMITTED D(P1). The visitor then writes
`pub:<repo:commit:fs>`. The post-renewal local gone observation was not
cleanup authority, so the pin stayed until its 35-day TTL. Reproduced on
current runtime by
[E1-15A](./E1-15A-STALE-REPAIR-VISITOR-CANCELLATION.md) `clear-after-recheck`.
A visitor paused before the re-checks is already safe.

#### Resolution

After renewal, local absence is escalated to an EACH_QUORUM read of the row.
GC re-reads references after its negative EACH_QUORUM repair scan, so a
committed D implies the row tombstone precedes that read. Confirmed global
absence withdraws exactly the repair-owned pin; local absence or an
unavailable read retains it, as in GONE-CHECK-XDC. Decider unit tests, a
source guard, mandatory E1-15A legs and two causal mutations (revert fix;
bypass re-check) cover it.

#### Residual (OPEN, P1 PRE-GC; PRE-X1 until E1-16)

E1-15A shows that an Office visit which completes its global check and
withdrawal leaves no post-D reference. The fix does not prevent the transient
creation of that reference, and it does not guarantee withdrawal after a crash,
a failed DELETE or unavailable authority. The residual pin is a retention
problem: the measured mitigation is proven, but it is not a publication fence.
Under E1-16 it no longer blocks X1 and still blocks A1.
Four paths leave the dead pin until TTL, all reachable in current code:
(1) a crash after the acknowledged renewal INSERT; (2) an EACH_QUORUM check
error; (3) a failed withdrawal DELETE (in 2 and 3 the visit logs, returns Gone
and ends normally); (4) an ambiguous or partial renewal. In case 4 the renewal
returns an error after some `pub:` rows (or a timed-out INSERT) were applied,
and the function returns before any re-check or global check. Case 4 predates
#271. Multi-DC (a positive local re-check) is argued to be excluded by quorum
intersection plus blocking read repair, but not measured. Writer funnels share
the write-before-validate pattern (hypothesis, unmeasured; see the E1-15A
review section). Other E1-15 interleavings and funnel matrices stay open.

#### Related

- `ISSUE-PUBLISH-REPAIR-GONE-CHECK-XDC-AUTHORITY-01`, `ISSUE-PUBLISH-REPAIR-OWNED-PUB-CLEANUP-RACE-01`, `ISSUE-PUBLISH-REPAIR-RENEWAL-AFTER-CLASSIFY-01`

### ISSUE-PUBLICATION-POST-D-LIVENESS-BEFORE-VALIDATION-01: Writers can durably write liveness after COMMITTED D, before their final fence

**Status**: 🔴 Confirmed (2026-10-08, E1-15B), P1 PRE-GC OPEN (PRE-X1 until E1-16, 2026-10-10) — characterized, not fixed
**Severity**: High (P1) — durable logical reference of L written after exact COMMITTED D(P1); no HEAD advanced and no P1 reinstalled; harm is a dead pin, renewed indefinitely when a repair was queued
**Scope**: PRE-GC / E1-15 shared class (writer staging + repair worker). [E1-16](./E1-16-POST-D-CONTRACT-ALIGNMENT.md) aligned the X1 invariant with G4 contract 5
**Affected**: CreateFile measured (`stagePendingPublishedFiles` → `queuePendingPublishedFileRepairs` → `insertCommit` → `validateCommitBlockPublicationFences`); other W2 funnels share the order (unmeasured)

#### Problem

Writers stage liveness first and validate exact-P afterwards. Nothing
between materialization and staging checks claim or D, and staging resolves
through retained `block_id_mappings`. A writer resumed after exact COMMITTED
D(P1) therefore writes `pub:` post-D. When it completes, the final fence
rejects it and cleans up, so the pre-HEAD guarantee holds. When it dies
first:

```text
crash after staging          durable pub:<commit>, 35d TTL, no repair
crash after queue+commit     durable pub:<commit> + non-expiring repair;
                             every sweep: native UNKNOWN, renews
                             pub:<repo:commit:fs> post-D, retains repair
```

Measured with real Office writers, natural COMMITTED D and SIGKILL by
[E1-15B](./E1-15B-WRITER-POST-D-STAGING-CRASH.md) (two sweeps renewed after
D). Same class as the #271 stale-renewal residual.

#### Physical disposition (E1-15C, 2026-10-08)

Measured on Office: post-D references and the dead repair do not reinstall P1,
prevent or revert its TERMINAL retirement, or make HEAD depend on retired P1.
A rematerialized P2 is published legitimately. The demonstrated harm is
retention/convergence (see `ISSUE-PUBLISH-REPAIR-DEAD-ROW-RETENTION-01`). The
contract reconciliation proposed in
[E1-15C](./E1-15C-POST-D-PHYSICAL-LIFE-DISPOSITION.md) was approved on
2026-10-10 ([E1-16](./E1-16-POST-D-CONTRACT-ALIGNMENT.md)). The label moved
from P1 PRE-X1 to P1 PRE-GC.

#### Direction (not started)

A shared design, not per-funnel guards. A write fence alone does not cover
the second case: a durable repair for an attempt that can never reach HEAD
keeps renewing, so its settlement also needs authority.

#### Related

- `ISSUE-PUBLISH-REPAIR-STALE-RENEWAL-AFTER-CLEAR-01`, `ISSUE-PUBLISH-REPAIR-DEAD-ROW-RETENTION-01`, `ISSUE-PUBLISH-REPAIR-OWNED-PUB-CLEANUP-RACE-01`

### ISSUE-PUBLISH-REPAIR-DEAD-ROW-RETENTION-01: A dead/unreachable publication's repair row surviving request-local cleanup can be retained and repeatedly re-pinned indefinitely

**Status**: OPEN — P2 PRE-GC since 2026-10-12, previously FOLLOW-UP / GENERAL (registered 2026-09-18 while reviewing the repair-worker observability, `feat/publish-repair-observability`; contract narrowed 2026-09-19). Over-retention / reclamation efficiency; not a safety issue. Measure before designing a fix (plan below)
**Severity**: Medium (P2) — over-retention / reclamation efficiency: a genuinely dead/unreachable publication attempt whose repair row survived request-local cleanup has no terminal settlement path; successful unresolved visits can repeatedly refresh its 35-day `pub:`. No under-retention, no data loss. Does not block X1 safety closure. Must be measured, and dispositioned from that measurement, before destructive-GC activation (A1).
**Scope**: PRE-GC (decided 2026-10-12): storage cost, worker load and GC reclaim efficiency. Not X1
**Affected**: `CleanupFailedPublishAttempt` + `clearPendingPublishedFileRepairs` / `cleanupSeafHTTPFailedPublishAttempt` / `cleanupOnlyOfficeFailedPublishAttempt` (known-loser and pre-HEAD abort/rollback paths after the durable row is queued), `repairPublishedBlockReferenceRepairVisit` (UNKNOWN renewal of those residues)

#### E1-15C evidence (2026-10-08)

[E1-15C](./E1-15C-POST-D-PHYSICAL-LIFE-DISPOSITION.md) measures the
consequence for later physical lives. It uses a real Office writer and a
natural exact COMMITTED D(P1). The writer is SIGKILLed after repair queueing,
which leaves a repair R that can never reach HEAD.

- R stays native UNKNOWN on every sweep, because it classifies by commit and
  c1 is never an ancestor of HEAD. Each sweep renews `pub:<repo:commit:fs>` on
  logical block L.
- After P1 is TERMINAL, a real rematerialization of the same content installs
  P2 under a new key. With every other real reference retired by TTL, the
  productive worker **vetoes D(P2) solely because of R**. Every later life of L
  in that org is uncollectable while R exists.
- P1 itself is unaffected: TERMINAL completes, nothing is reinstalled and
  HEAD only ever depends on P2.
- Design constraint (measured): the rematerialized file has the same
  content-addressed fs_id as R, so a settlement of R must never remove
  `fs:<repo:fs_id>`.

Proposed, pending explicit decision: reconsider this as a pre-activation
convergence gate (destructive GC would otherwise accumulate permanently
uncollectable blocks). Scope line above is unchanged until decided.
E1-16 (2026-10-10) approved the X1 contract split and left this scope
unchanged. The gate question was decided on 2026-10-12: P2 PRE-GC (see below).

**E1-15D correction (2026-10-09):** such a repair is *abandoned*, not provably
dead. Real Sync `UpdateBranch?head=<its commit>` auto-merges the commit's
content into HEAD even after HEAD moved on, so the repair protects a commit
that can still be published. No authority exists today to cancel it:
`DefinitelyNotReachable` has no emitter, and time is not authority. A safe
settlement needs durable authority that the original attempt can no longer
publish, plus an analysis showing that a concurrent Sync promotion of the
same commit stays protected by its own references; a global commit
cancellation is only a fallback option
([E1-15D](./E1-15D-POST-D-CONTRACT-DECISION.md) §3). Deleting the row
today, without that authority, is not proposed.

**E1-15E narrowing (2026-10-09):** the repair worker now settles a repair whose
commit is **superseded**: the resumable walk from its SERIAL HEAD anchor
reaches the commit's own immutable parent before the commit, and the anchor
is not that parent. Settlement removes only the repair-owned `pub:` and the
row ([E1-15E](./E1-15E-ABANDONED-REPAIR-SETTLEMENT.md)). Measured: a natural
D(P1) is no longer vetoed; Sync publications after or during the settlement
stay protected by their own references. Still retained (UNKNOWN), so this
issue stays OPEN but narrowed:
- HEAD still equal to the commit's parent;
- the commit row is missing: a crash between repair queueing and
  `insertCommit`, or a known-loser/pre-HEAD cleanup that deleted the commit
  and then failed to clear the row;
- ancestry the resumable walk cannot complete.

**E1-15F (2026-10-09):** SUPERSEDED was validated on real three-DC Cassandra
([E1-15F](./E1-15F-SUPERSEDED-CROSS-DC.md)). One defect from #275 was fixed: a
failed EACH_QUORUM read of the commit's parent let the walk pass the parent
without the witness, which left the repair unsettled until HEAD moved again
(liveness only, safe direction). That visit now stays UNKNOWN without
walking.

#### Problem

The indefinite leak is a **dead/unreachable publication repair row that
survived request-local cleanup**. It is not "failed clear after any
publication".

When a publication attempt is a proven loser (`ErrLibraryHeadConflict`) a
v2-like funnel deletes its commit and attempt refs
(`CleanupFailedPublishAttempt`, `cleanupSeafHTTPFailedPublishAttempt`,
`cleanupOnlyOfficeFailedPublishAttempt`) and then clears its repair rows
(`clearPendingPublishedFileRepairs`). If that clear fails, the request logs
a warning (`CreateFile` / `UploadFile` after the known-loser cleanup) or
returns the joined cleanup error, and the row remains with
`staged_block_ids` intact.

The same residue appears when the row is already queued and a **pre-HEAD**
abort or rollback then fails to clear it: `CreateFile` / `UploadFile` /
`processSingleItem` join `CleanupFailedPublishAttempt` with
`clearPendingPublishedFileRepairs` after `insertCommit` (and, on
`UploadFile`, after `fileFromBlocksBeforeHeadBarrier` /
`validateCommitBlockPublicationFences`); SeafHTTP's
`cleanupSeafHTTPFailedPublishAttempt` and OnlyOffice's
`cleanupOnlyOfficeFailedPublishAttempt` include the clear on those abort
paths. If the clear fails, a row associated with a publication that never
reaches HEAD survives.

The dead repair row has no terminal settlement path under the current
protocol. It remains durably discoverable and is revisited according to
the worker's retry/backoff policy: a visit that returns an error or
retention installs a process-local retry hint, so intermediate sweeps can
`skipped_retry_hint` (5 min → 6 h;
[PUBLISH-REPAIR-OBSERVABILITY.md](./PUBLISH-REPAIR-OBSERVABILITY.md)). It
is not visited on every sweep.

When a later visit runs, the classifier does not observe the target
reachable from HEAD (the commit was deleted, never inserted, or is off
the ancestry); natural exhaustion is still UNKNOWN (no durable negative
cleanup authority). A visit does not always prove a successful refresh:
hydrate, classify, or renewal can fail, and a renewal error may be
partial or ambiguous (`failed` does not certify renewal; even `retained`
does not prove the pin was refreshed). Successful unresolved visits can
repeatedly refresh the repair-owned `pub:<repo:commit:fsID>` references
for another 35 days, so otherwise-dead blocks can remain over-retained
indefinitely while renewal continues succeeding. For a proven loser that
pin is the only thing keeping those blocks alive.

Do **not** classify ordinary **post-success** clear failure as this leak.
If HEAD published and promotion completed, the commit remains reachable:
the next worker visit classifies REACHABLE, retries the idempotent `fs:`
promotion, removes the repair-owned `pub:`, and retries DELETE of the
repair row. That path can still leave a backlog until a later visit
succeeds, and the runbook may list it as a way a pending row is created;
it is not an indefinite dead-row leak. The same is true of a post-HEAD
reconciliation that did not complete: those rows are the worker's intended
work, classified REACHABLE when the commit is on the ancestry.

Sync direct-HEAD repair rows are shared by every writer of that
`targetCommitID`. Some request-local losses deliberately leave those rows
durable (`clearSyncCommitBlockReferenceRepairsFn`); that is not this leak.

#### Intended follow-up

Not fixed here (observability PR). Options for a scoped follow-up:
bounded retry of the clear in the request; a worker-side settlement that
rests on a **durable positive loser/cleanup witness** tied to the exact
publication / repair identity and proving that this attempt cannot become
reachable; or accepting the cost with an alert on
`publish_repair_oldest_pending_age_seconds`.

Do **not** treat observed absence of the row's commit as authority to
settle or to remove liveness. Raw absence is negative evidence, not a
durable cleanup witness: `SELECT commit` → not found, a log, a prior
execution, or a later missing commit row does not prove the publication
cannot become reachable. "Was cleaned by a loser path" is usable only if
that fact is itself persisted as a durable, verifiable witness for that
identity. Any design that would remove liveness must pass the gate of
[PUBLISH-REPAIR-LIVENESS-REJECTED-DESIGNS.md](./PUBLISH-REPAIR-LIVENESS-REJECTED-DESIGNS.md)
§6 (and the local-absence / observed-absence rule of §2.6 / §4.8).

#### Decision and measurement plan (2026-10-12)

The project owner moved this issue to **P2 PRE-GC**. E1-15C measured an
abandoned repair vetoing D(P2) and every later life of L, so destructive GC
would keep that content's storage indefinitely. That contradicts what A1
activates GC for. It does not affect X1: nothing is reinstalled or lost.
This change is documentation only. Measure first, and design a fix only if
the measurement shows the retention matters.

**Why no TTL on the repair row.** The row protects a publication that may
already be in HEAD (crash after the HEAD CAS, before `fs:`). Time-based
cleanup was the earlier pre-CAS lease, removed by
`ISSUE-PUBLISH-REPAIR-TIMEOUT-CLEANUP-01` because lease expiry "is not proof
that a possibly-applied HEAD CAS did not publish". The HEAD CAS
(`UpdateLibraryHeadFromSnapshot`) carries no deadline, so elapsed time does
not bound when a paused writer can still publish. Any cleanup must pass
[PUBLISH-REPAIR-LIVENESS-REJECTED-DESIGNS.md](./PUBLISH-REPAIR-LIVENESS-REJECTED-DESIGNS.md) §6.

**What a later HEAD does to a retained row.** A visit classifies the row
from the evidence it can read:

- **REACHABLE.** HEAD contains the repair commit: `fs:` is promoted and the
  row is settled.
- **SUPERSEDED** (E1-15E/F). HEAD moved on without the commit, and the walk
  reached the commit's own parent as a valid witness: only the repair-owned
  `pub:` and the row are removed.
- **UNKNOWN.** The evidence is incomplete, so the row is retained.

A later commit in the library therefore resolves the row only when one of
the first two outcomes can be proven. Three shapes stay retained:

1. **HEAD still equals the repair commit's parent** (the library has been idle
   since the crash). If the commit row exists, the next library commit that
   moves HEAD lets the walk reach that parent, and the row settles as
   SUPERSEDED, or as REACHABLE if the new HEAD contains the commit.
2. **The commit row is missing.** This happens after a crash between repair
   queueing and `insertCommit`, or after a loser cleanup that deleted the
   commit and then failed to clear the row. The parent read returns
   not-found, so there is no SUPERSEDED witness. The visit stays a clean
   UNKNOWN even after HEAD moves.
3. **Ancestry the walk cannot complete.** This covers the 1024-node chunk
   limit (the cursor advances, but that visit returns an error), an ancestry
   cycle, a malformed parent, or a failed parent read. These visits return an
   error.

**How visit outcomes map to the shapes.** `retained` means the visit ended
inconclusive without an error, which is typical of shapes 1 and 2.
`failed` means the visit returned an error. That includes shape 3 and
operational causes such as an unavailable DC or a timeout. `failed` does not
rule out retention: investigate its cause.

**Phase 1: existing signals, no code.** Use the metrics in
[PUBLISH-REPAIR-OBSERVABILITY.md](./PUBLISH-REPAIR-OBSERVABILITY.md#abandoned-repair-retention-pre-gc-measurement):

- `publish_repair_pending_rows` and
  `publish_repair_oldest_pending_age_seconds`. These are per-node gauges,
  updated only by a sweep that listed every bucket.
- `publish_repair_last_complete_sweep_timestamp_seconds` and
  `publish_repair_sweep_duration_seconds`, to check that those gauges are
  fresh.
- `publish_repair_visits_total` by outcome. This counter is aggregate and
  does not identify the row behind an outcome.

A healthy row settles within minutes, through the request-local promotion
or a sweep shortly after its advisory 5 min scheduling lease
(`lease_expires_at` never authorizes cleanup). Revisits are not guaranteed
on any schedule:
- the 6 h `publishedBlockReferenceRepairRetryMax` caps a process-local
  backoff, which "does not bound how soon the discovery sweep will visit the
  row again";
- the sweep is sequential, so its span depends on backlog and on Cassandra.

**24 h is an operational investigation threshold, not a revisit
guarantee.** On a node whose last complete sweep is fresh, an oldest pending
age above 24 h makes that node's backlog a retention candidate, whether its
visits end `retained` or `failed`. Find the rows themselves read-only (list
the repair table) and, for each one, check:
- whether the commit row exists;
- whether HEAD equals the commit's parent;
- whether the walk progressed (`reachability_cursor_commit_id`);
- its `staged_block_ids`.

Classify each row into a shape, or into an operational cause, and record the
count, age and shape per environment.

**Phase 2: only if Phase 1 shows real retention.** Add the missing
observability as a separate code PR. Today `retained` "proves neither why
the row was retained", so the PR would add a retain reason for the three
shapes above and an estimate of the blocks/bytes those rows pin. No gate
reads it.

**Phase 3: disposition before A1, from the data.**
- **Shape 1** resolves with library activity when its commit row exists;
  accepting it with monitoring is a legitimate outcome.
- **Shape 2, and any shape 3 that stays unresolved**, are investigated by
  cause. A walk past the 1024-node chunk limit persists its cursor and can
  finish on later visits. A transient read error clears when the
  infrastructure recovers. Corrupt ancestry or a cycle is an integrity
  problem to investigate. A persistent `failed` visit is not cancellation
  authority and does not show that cancellation is the right fix.
- **A durable cancellation is needed only when it is shown** that the
  original publication must be invalidated and that no other safe
  resolution exists. Even then it must be authority the original writer's
  HEAD CAS cannot outrun: a check in the same serial domain as HEAD (E1-15D
  option B) or a proven equivalent. Time is never that authority.
- In every outcome the settlement may remove only the repair-owned `pub:` and
  the row, never `fs:<repo:fs_id>` (E1-15C/D).

#### Related

- `ISSUE-PUBLISH-REPAIR-OWNED-PUB-CLEANUP-RACE-01` (35-day residual of a renewal racing a clear), `ISSUE-PUBLISH-REPAIR-RENEWAL-AFTER-CLASSIFY-01`, `ISSUE-PUBLISH-REPAIR-KNOWN-LOSER-DURABILITY-01` (crash after a known loser, before request-local cleanup: no durable loser witness; distinct from a failed clear after cleanup ran)
- [PUBLISH-REPAIR-OBSERVABILITY.md](./PUBLISH-REPAIR-OBSERVABILITY.md)

### ISSUE-PUBLISH-REPAIR-PROGRESS-PAXOS-DOMAIN-01: Reachability progress LWTs share 32 bucket partitions with ordinary queue writes

**Status**: Open follow-up (2026-09-13) — availability/protocol residual of #219; not an R31-C1 safety blocker
**Severity**: Medium (P2) — partition-granular Paxos contention and mixed LWT/ordinary timestamps; not destructive cleanup
**Scope**: PRE-GC / R31 residual of resumable progress (PRE-X1 until [E1-16](./E1-16-POST-D-CONTRACT-ALIGNMENT.md), 2026-10-10: availability residual, not destructive authority)
**Affected**: `published_block_reference_repairs` `PRIMARY KEY ((bucket), org_id, repo_id, commit_id, fs_id)`, `publishedBlockReferenceRepairBuckets = 32`, SERIAL progress LWTs, ordinary INSERT/DELETE

#### Problem

#219 stores SERIAL LWT progress (anchor, cursor, exhaustion, re-anchor) on the
bucketed discovery table. The partition key is only `bucket`, and there are 32
buckets, so unrelated repairs that hash to the same bucket share one Paxos
domain. Cassandra LWT is single-partition; concurrent CAS on the same partition
contends. Scheduled repairs and multiple processes can hit distinct rows in
that partition at once. `main` already used 32 buckets for ordinary discovery;
the new contention is the progress LWTs.

The same row also mixes ordinary queue INSERT and settlement DELETE with those
LWTs. Cassandra does not serialize ordinary writes against LWT and uses a
different timestamp mechanism; mixing both on one row can stall or leave a
partial progress snapshot. The unit tests that freeze INSERT/DELETE as
ordinary document the intended cheap queue, not a single serialized lifecycle.

`created_at = loaded` binds progress CAS to the hydrated TIMESTAMP and closes
a finished DELETE+requeue ABA when the requeued row's Cassandra timestamp
differs. CQL TIMESTAMP is millisecond precision, so DELETE+requeue of the same
primary key in the same millisecond can reuse `created_at` and let a stale CAS
match.

None of this authorizes cleanup. It can delay monotonic progress under a
repair storm or leave a cursor that a later visit must rediscover.

#### Progress-only residue (closed 2026-09-13)

The concrete partial snapshot has one shape: the settlement DELETE tombstones
the queue cells, a progress LWT whose Paxos commit carries a later timestamp
re-materializes `reachability_*` cells, and the row survives with only its
primary key and those cells (`created_at`, `lease_expires_at`,
`staged_block_ids` null). The bucket listing returned that row on every sweep,
`repairPublishedBlockReferenceRepair` no-oped on it (no staged blocks), and
nothing ever deleted it: an unbounded, permanent leak in the discovery scan.

The sweep now recognizes that shape (`publishedBlockReferenceRepairIsProgressOnly`:
all three ordinary cells absent — the queue INSERT writes them atomically, so a
legitimate row can never present that way) and tombstones **only the three
reachability cells** with
`DELETE reachability_* FROM … IF created_at = null AND lease_expires_at = null`
at `SERIAL`. It must never delete the row. The requeue INSERT is an ordinary
write outside Paxos, so the LWT can evaluate `created_at = null` against a
quorum that has not yet seen an already-acknowledged requeue; if that requeue
carries a timestamp older than the reaper's ballot, a *row* tombstone would
shadow the durable repair in reconciliation (lost requeue → `NoLongerPending`
→ no promote, no renewal). Cell tombstones on columns the ordinary INSERT never
writes cannot shadow anything it wrote: the worst case of that race is a fresh
row losing progress it did not have — a replay, the class this issue already
accepts. A residue row has no row marker, so removing its cells removes it from
the listing. The reaper is not settlement and not cleanup authority. Progress-LWT
helpers still never delete the row
(`TestPublishedBlockReferenceRepairProgressUsesMonotonicCAS`). Evidence:
`TestRunPublishedBlockReferenceRepairSweepReapsProgressOnlyResidue`,
`TestReapPublishedBlockReferenceRepairProgressOnlyRowIsConditionalAndSerial`,
real-Cassandra W2 leg `TestW2PublishedRepairSweepReapsProgressOnlyResidue`
(`progress_residue_reap`: an UPDATE-only residue is reaped by one production
sweep; a queued row survives the conditional reap and the sweep; a requeue
landed with `USING TIMESTAMP` one minute older than the reaper's tombstones
survives and is not reaped again; a negative control shows the same race
against a whole-row conditional DELETE loses the requeue), mutations
`m_residue_reaper_removed`, `m_residue_reaper_unconditional`,
`m_residue_reaper_deletes_whole_row`.

The residue can also appear *after* a worker listed the row live: another
worker settles it, a late progress LWT leaves residue, and the first worker
then hydrates or revalidates. Every read of the durable row now goes through
`loadLivePublishedBlockReferenceRepair`, which treats a progress-only row like
a missing one (`errPublishedBlockReferenceRepairGone`), and the merge takes the
loaded ordinary cells as authoritative instead of keeping the listed copy's
`staged_block_ids`/`created_at`. So a residue is never classified, never renews
`pub:<repo:commit:fsID>` for stale staged blocks, and is never promoted from a
stale copy — including on a mid-classify CAS-miss reload. Evidence:
`TestRepairPublishedBlockReferenceRepairListedLiveThenLoadedResidueIsNoOp`,
`TestClassifyPublishedBlockReferenceRepairCASMissOnResidueIsGoneAndDoesNotRenew`,
mutation `m_hydrate_trusts_listed_cells_on_residue`. The mixed ordinary/LWT
lifecycle itself remains the open follow-up below.

The residue reaper is itself one more LWT on the shared bucket partition, but
it fires only for residue rows, which exist only after the race above.

#### Intended follow-up

Keep the bucketed repair table ordinary for discovery. Move
anchor/cursor/exhausted to a progress table partitioned by exact repair
identity, LWT-only, with a UUID/timeuuid generation. Do not convert the whole
queue to LWT. Do not reopen the resumable walk.

#### Related

- `ISSUE-PUBLISH-REPAIR-REACHABILITY-CONVERGENCE-01` (closed), `ISSUE-PUBLISH-REPAIR-DISCOVERY-SCALE-01`

### ISSUE-PUBLISH-REPAIR-CROSS-CHUNK-CYCLE-01: Resumed reachability chunks detect only cycles that return to the anchored HEAD

**Status**: Open follow-up (2026-09-13) — not an R31-C1 blocker
**Severity**: Medium (P2) — pathological corrupt ancestry only; UNKNOWN/retain, never cleanup authority
**Scope**: R31 residual of the resumable walk
**Affected**: `walkPublishedCommitReachabilitySeeded`, `publishedBlockReferenceRepairWalkSeeds`

#### Problem

Each resumed chunk rebuilds `visited` from scratch and seeds only the durable
anchored HEAD. A cycle longer than `publishedCommitReachabilityMaxNodes` that
leads back to HEAD is detected (HEAD cannot be its own ancestor). A cycle of
that length that lies entirely below HEAD — `H → A → B1 → … → B1100 → B1` —
is not: every chunk advances the cursor around the cycle and the row stays
UNKNOWN forever without a cycle error. Valid ancestry (a DAG) never presents
this shape; R31-C1's convergence claim is about valid ancestry under a moving
HEAD and is unaffected. Cycles shorter than one chunk are detected in-chunk.

#### Intended follow-up

If corrupt-ancestry diagnosis matters, persist a Brent/Floyd witness (one
commit + step count) next to the cursor, or bound the total nodes a snapshot
may visit (anchor depth) and mark the snapshot exhausted-with-error past it.
Either needs its own progress column and CAS guard; do not widen the in-memory
visited set across visits.

#### Related

- `ISSUE-PUBLISH-REPAIR-REACHABILITY-CONVERGENCE-01` (closed), `ISSUE-PUBLISH-REPAIR-PROGRESS-PAXOS-DOMAIN-01`

### ISSUE-PUBLISH-HEAD-TREE-STATS-COST-01: Every HEAD publish walks the full directory tree for stats inside the stage→HEAD window

**Status**: 🟡 Open — registered by the PC-0 audit (2026-09-10); cost/latency, not safety
**Severity**: Medium (P2) — performance; widens the `pub:`-only and LWT-contention windows
**Scope**: FOLLOW-UP (optimize if worthwhile); PC-0 §12 corrected
**Affected**: `FSHelper.UpdateLibraryHead → CalculateLibraryStats → calculateDirStats` (`internal/api/v2/fs_helpers.go`); `SyncHandler.updateLibraryHeadWithStats → commitTreeStats` + `commitTreeStatsStrict` (`internal/api/sync.go`)

#### Problem

`calculateDirStats` reads `fs_objects.dir_entries` once per directory,
recursively, with no cache or memo, at session consistency. v2/SeafHTTP/
OnlyOffice/cross-repo/resurrection publishes pay it once, **after** `pub:`
and the durable repair row are written and **before** the CAS. Sync pays it
**twice** before its CAS (previous tree + new tree, to derive the counter
delta). For a library with D directories that is O(D) sequential reads per
publish (2·O(D) for Sync). A read error is logged and the subtree silently
counted as zero, so the stats are also best-effort.

#### Scope / disposition

Document the real cost (done in PC-0 §8/§12). Optimization — incremental
deltas from the mutation, content-addressed memoization, or moving the walk
out of the pre-CAS window — is a follow-up; a future coordinator must not
make a full tree walk part of its HEAD step.

#### Related

- `docs/PUBLICATION-PROTOCOL-CHARACTERIZATION.md` §12
- `ISSUE-SYNC-PUTBLOCK-READINESS-HOTPATH-COST-01`

### ISSUE-SYNC-PUTBLOCK-READINESS-HOTPATH-COST-01: Sync PutBlock readiness O(N) cost has no tuned concurrency, redundant-read, or scheduling optimization yet

**Status**: Confirmed performance/tech-debt follow-up (2026-09-08); elevated priority (2026-09-09) with real measured data; not a blocker for #206 or #210
**Severity**: P2 (performance, not correctness) - Sync PutBlock -> HEAD readiness hot-path latency
**Affected**: `ensureSyncCommitBlockPublicationReadiness` and its four bounded-concurrency stages (`internal/api/sync.go`)

#### Problem

#206's declared O(N)-per-provenanced-block readiness cost (see the "Declared exception: Sync PutBlock-provenanced readiness" section of `docs/R3-LIVENESS-CONTINUITY.md`) ships with a fixed, unmeasured concurrency bound and no attempt to remove a known-duplicate existence check or to reduce cross-block barrier latency. For a large single-commit block delta (for example, one new file with several hundred blocks), this is the dominant added latency in HEAD promotion.

**2026-09-09 update**: #210's real 3-DC cross-DC-hit measurement (`ISSUE-SYNC-PUTBLOCK-CROSS-DC-PROVENANCE-VISIBILITY-01`) gives this issue concrete motivating data: on the real 3-DC Cassandra fixture, N=1000 blocks under the current concurrency bound (20) took **between ~1.7 and ~8 seconds across separate runs** (scope-gate only; roughly 17-39ms at N=1/10, ~290ms at N=100), and the same fallback query count and cross-DC availability dependency apply to a large all-genuinely-unprovenanced (dedup) commit once any datacenter-level fallback is in play, not only to genuine cross-DC recovery -- the dedup case's own wall-clock was not separately measured on the real 3-DC fixture and should not be assumed identical. This is no longer a hypothetical latency concern; concurrency tuning (item 1 below) is the direct, lowest-risk mitigation and should be prioritized accordingly once real production multi-region latency is available to validate a new default against.

#### Scope / disposition

Three separate, independent lines of follow-up work, none implemented here and none required for #206 or #210 to merge:

1. **Concurrency tuning**: `syncCommitBlockPlacementConcurrency` (currently a hardcoded `20`) bounds wall-clock latency, not total Cassandra load. Measure with real deltas at N=100/500/1000 against a representative cluster before deciding whether to raise the default, and consider making it configurable (env var) rather than a compile-time constant. #210's real 3-DC numbers above are a starting reference point, not a substitute for measuring against actual production topology/latency.
2. **Eliminate a redundant read**: `ProbeBlockReuse`'s internal `BlockHasReferences` check re-proves "some reference exists" immediately after `syncCommitProvenancedBlockIDs` already proved the exact `up:sync:<repo>:<block>` referrer exists at `LOCAL_QUORUM`. A narrower placement-only probe that skips that specific internal read (for this caller only) would save one of three reads per provenanced block. Needs its own review and mutation coverage before landing; do not change the shared `ProbeBlockReuse` contract used by other callers.
3. **Per-block pipeline instead of global barriers**: today all N blocks finish stage *k* before any block starts stage *k+1* (four sequential `errgroup.Wait()` barriers). Replacing that with one bounded-concurrency pool where each goroutine runs the full four-step sequence for one block would not reduce total Cassandra operations, but would remove the "wait for the slowest of the wave" tax and improve P99 tail latency under non-uniform response times. This changes operational scheduling shape and needs its own audit, not a drive-by change.

Explicitly out of scope for all three, and not a valid direction for any of them:

- **No `IN` multi-partition batching.** `blocks`, `block_references`, and `gc_s3_orphans` all partition on `(org_id, block_id)` -- a `block_id IN (...)` query spans as many partitions as blocks, which is Cassandra's classic single-coordinator scatter-gather anti-pattern. The current per-block, bounded-concurrency, token-aware parallel reads are already the idiomatic pattern for this schema; collapsing them into `IN` queries would be a regression, not an optimization.
- **Do not remove the placement read or the final exact-placement validation.** The "resolve placement, then re-validate placement after renewal" pair is the actual safety mechanism #206 exists to add (it is what catches a GC race between reading placement and publishing HEAD). Removing either reopens the gap this PR closes.
- **Do not cache provenance in process memory** to skip the `BlockReferenceExistsLocalQuorum` scope-gate read. `PutBlock` and `PUT commit/HEAD` are separate HTTP requests that can land on different nodes in a multi-instance fleet; the Cassandra round trip is what makes the cross-process check correct, not an implementation detail to optimize away.

Any of these three lines, if implemented, must keep `TestR3SyncPutBlockReadinessDeclaredExceptionIsFrozen` and the "Declared exception" section of `docs/R3-LIVENESS-CONTINUITY.md` in sync with the actual reachable call set and consistency levels.

---

### ISSUE-SYNC-PUTCOMMIT-NOT-WRITE-ONCE-01: PutCommit does not enforce immutable commit identity

**Status**: ✅ Fixed by PR #208; merged (2026-09-08)
**Severity**: High (P1) - General Sync/W2 commit-identity integrity
**Origin**: PRE-EXISTING
**Affected**: `SyncHandler.PutCommit` (`internal/api/sync.go`), `commits` table

#### Problem

The Sync/R3 model assumes that `(library_id, commit_id)` identifies one
immutable snapshot. `PutCommit` historically used a plain Cassandra `INSERT`,
so a second request with the same `commit_id` could overwrite `parent_id` or
`root_fs_id`. Cassandra treats that insert as an upsert. A concurrent rewrite
can therefore make a HEAD publish one root while a later reader resolves the
same HEAD commit ID to another root.

#### Scope / disposition

PR #208 now enforces first-writer-wins with Cassandra `IF NOT EXISTS` LWT at
the commit row boundary. Identical `(parent_id, root_fs_id)` retries remain
idempotent; conflicting reuse and concurrent conflicting first writers are
rejected without mutating the winner. This closes the pre-existing identity
blocker for the prerequisite itself. PR #208 is merged and PR #206 has been
rebased; this pre-existing issue does not block the scoped #206 re-audit.

---

### ISSUE-SYNC-RECVFS-NOT-WRITE-ONCE-01: RecvFS does not enforce immutable fs identity

**Status**: ✅ Fixed by PR #208; merged (2026-09-08)
**Severity**: High (P1) - General Sync/W2 fs-object identity integrity
**Origin**: PRE-EXISTING
**Affected**: `SyncHandler.RecvFS` (`internal/api/sync.go`), `fs_objects` table

#### Problem

`RecvFS` historically trusted the 40-byte `fs_id` supplied on the wire and
persisted the decompressed JSON fields with a plain `INSERT`. It did not verify
`SHA1(exact decompressed JSON bytes) == fs_id`, and Cassandra treated the insert
as an upsert. The same `(library_id, fs_id)` could therefore be reused to change
`block_ids` and the reachable tree semantics. That directly defeats a
publication proof which captured the earlier file object before HEAD.

#### Scope / disposition

PR #208 now verifies `SHA1(exact decompressed JSON bytes) == fs_id` before
persistence and installs immutable fields through a LOCAL_QUORUM read plus an
ordinary write. File identity compares the logical Seafile SHA-1 block list
(`seafile_block_ids_sha1`, falling back to legacy `block_ids`) rather than
canonical physical SHA-256 IDs; file completeness does not require
`dir_entries`, while directory identity uses exact `dir_entries` and ignores
`block_ids`. The primitive distinguishes absent, complete, partial, and
pre-existing metadata-only placeholder rows, preserves `obj_name/full_path`,
and performs no per-object SERIAL/Paxos round. RecvFS no longer creates new
child placeholders that `CheckFS` could mistake for complete objects; any
storage read/write failure is fail-closed with 5xx, semantic conflicts return
409, and non-lowercase wire IDs are rejected with 400. Real Cassandra/MinIO
coverage includes canonical replay, placeholder completion, semantic conflict,
uppercase rejection, identical retry, and published-tree replay. PR #208 is
merged and PR #206 has been rebased for its scoped W2 re-audit.

#### Required contract

- valid `fs_id` for the exact decompressed payload is accepted;
- identical retries are idempotent and never rewrite the row;
- a claimed `fs_id` whose payload hash differs is rejected before persistence;
- an already-published tree cannot be mutated by replaying `RecvFS` under an existing identity.

---

### ISSUE-PUBLISH-REPAIR-DISCOVERY-SCALE-01: UNKNOWN repair discovery is scan-bound

**Status**: OPEN follow-up — R31/W2-13 discovery, convergence and scale; outside the covered E1-11 pre-D safety contract (originally registered 2026-09-07)
**Severity**: Medium (P2) - R31 performance and convergence at sustained UNKNOWN-row volume
**Scope**: PRE-GC (E1-16, 2026-10-10). W2-13 X1 safety is CLOSED-EVIDENCE: the destructive guard reads the repair table directly, independent of discovery. This convergence residual previously blocked X1 through W2-13 and now blocks A1
**Affected**: `internal/api/v2/publish_repair.go`, published-block-reference repair worker

#### Problem

The correctness slice keeps UNKNOWN or unavailable publication confirmations in a
durable repair row and retries them with advisory age-based backoff. The current
simple worker discovers those rows by reading every repair bucket on each minute
tick. As retained UNKNOWN rows and bucket churn grow, the repeated base-table
reads can consume increasing Cassandra work even though each individual repair
remains fail-closed. A process pause or outage can also defer discovery until the
next base-table sweep; this is a scalability and convergence-cost issue, not a
publication-safety hole.

Retry backoff is process-local advisory state and never mutates the durable repair
row, so it adds no Paxos contention. A restart forgets the hint and may cause an
earlier safe retry. The repair table still has 32 bucket partitions, and multiple
nodes may duplicate the cold-path work while full scans rediscover UNKNOWN rows;
that is bounded by fail-closed behavior but remains a discovery/convergence cost.

#### Scope / disposition

This remains a separate R31/W2-13 discovery/convergence requirement, outside
E1-11's covered current-version pre-D safety contract. Do not solve it by weakening UNKNOWN
retention, cleanup authority, or positive-reachability-only settlement. A
separate follow-up must characterize rows without a schedule, overdue rows,
missed ticks, outages, restart, concurrent rescheduling, stale/orphan hints,
partition growth, tombstones, multi-node duplicate retry, fairness, and bounded
work per tick before selecting a durable discovery design. Scheduler state must
remain separate from publication authority, and scheduler failure may delay work
but must not make a durable repair undiscoverable indefinitely. There is still
no hard time-to-visit or completion bound; this is an OPEN operational and
convergence requirement. It is not a TTL-renewal prerequisite for the covered
pre-D safety contract: the matching non-expiring repair is consulted directly
by the destructive EACH_QUORUM proof and vetoes a new D even if pub: has expired.
E1-11 closes that covered W2-11 contract without a sweep-health gate; metrics
and discovery cadence do not supply destructive authority. A repair first
acquired after COMMITTED D cannot revoke D, and unadopted funnels are not covered.
See [E1-11 evidence](./E1-11-CURRENT-RUNTIME-REPAIR-LIVENESS.md). W2-13 remains
OPEN; no bounded discovery design is implemented here.

#### E1-13 measured discovery-delay evidence (2026-10-07)

[E1-13](./E1-13-REPAIR-DISCOVERY-DELAY-SAFETY.md) uses productive Office repairs
in naturally earlier buckets, with an exact-session/library observer before
repair execution. Target refs naturally expire after controlled TTL shortening;
its non-expiring repair remains unvisited with no durable classifier progress.
Productive GC directly finds the matching repair at EQ, releases claim and
preserves candidate/P1/K1 with no D/root. The sweep later reaches target and
natively retains UNKNOWN while renewing exact repair-owned pub, with unchanged
HEAD/tree/P and no fs promotion. A SIGKILL/restart rediscovers backlog in a new
process; guard-only omission reaches exact COMMITTED and own TERMINAL recovery.

Only this measured pre-D safety subset is CLOSED-EVIDENCE. This does not implement
bounded discovery or close W2-13 overall. Backlog spans owned orgs and is finite;
no same-org GC scan scalability, arbitrary delay, fairness, populated-backoff
restart, missed ticks, sustained churn, multi-node duplicate work or completion
bound is established. Corrected-source final checks and cleanup are recorded in
the linked evidence. Scheduler design and all broader operational requirements
above remain OPEN. W2-12/14, concurrent cleanup, E1/X1 and activation remain OPEN.

#### Related

- `ISSUE-PUBLISH-REPAIR-RENEWAL-AFTER-CLASSIFY-01`, `ISSUE-GC-PUB-REF-ZERO-REF-01`

---

### ISSUE-PUBLISH-REPAIR-KNOWN-LOSER-DURABILITY-01: Definitive CAS-loser cleanup has no durable witness

**Status**: OPEN — R31/W2-12 durable loser authority, cleanup and convergence; E1-12 measures the Office crash pre-D safety subset
**Severity**: Medium (P2) - R31 convergence and retention
**Scope**: PRE-GC (E1-16, 2026-10-10). W2-12 X1 safety is CLOSED-EVIDENCE: only REACHABLE promotes, UNKNOWN retains, and the guard vetoes D. This convergence residual previously blocked X1 through W2-12 and now blocks A1
**Affected**: definitive library-HEAD CAS loser cleanup and post-restart repair classification

#### Problem

After a request receives a definitive library-HEAD CAS conflict, it normally
cleans the exact attempt's staged references and repair rows synchronously. A
crash after the definitive loser result but before that cleanup completes leaves
no durable known-loser witness. After restart, the repair worker cannot infer the
loser from a timeout or lease expiry; it correctly classifies the outcome as
UNKNOWN and retains the repair and artifacts. This is safe but can retain
references until a future reconciliation authority discovers the known loser.

#### Current E1-12 evidence (2026-10-07)

The [E1-12 matrix](./E1-12-KNOWN-LOSER-CRASH-SAFETY.md) obtains an actual
Office/CreateFile HEAD CAS applied=false, verifies Linux SIGKILL before local
cleanup and retains the original durable repair. After real temporary-reference
expiry, productive GC observes the matching repair, releases the claim and
preserves candidate/P1/K1 with no D/root. Two fresh-process bucket sweeps classify
UNKNOWN and renew exact repair-owned pub: without fs: promotion or HEAD/tree/P
changes. The omission of only the pending-repair scan reaches exact COMMITTED;
productive TERMINAL recovery and independent fixture teardown complete.

This is positive evidence for the measured current-version pre-D safety subset,
not a durable loser witness or eventual artifact cleanup. W2-12 overall remains
OPEN (P2 convergence/retention), as do W2-13/14, concurrent cleanup, broader
funnels, E1/X1 and activation. Normal production queries/TTL/protocol are unchanged;
the new Office observation is a no-op outside integration. Full regression status
is recorded in the evidence page; no discovery latency bound is claimed.

#### Historical scope / disposition (2026-09-07)

This remains an R31 follow-up and does not block this branch. Direct Sync
request-local outcomes now conservatively retain shared repair rows; they are
not durable known-loser witnesses. A crash after a definitive loser result can
therefore leave no durable loser classification, and a future design needs a
known-loser witness or equivalent authority while preserving fail-closed
retention when that witness is unavailable. This branch does not claim
known-loser durability or expired-provenance continuity.

---

### ISSUE-GC-ENGINE-ROBUSTNESS-01: GC Worker/Scanner Robustness (E1/E2/E4/E5/E6)

**Status**: 🟡 Confirmed, low-severity (2026-07-10)
**Severity**: Low / Low-Med — engine fragility and observability; former E3 is High P6
**Affected**: `internal/gc` worker + scanner

#### Problem

A worker/scanner engine review confirmed E1/E2/E4/E5 as low-severity. Former E3 was
incorrectly described as fail-safe: the Cassandra store swallows the error, making it the High
P6 issue above.

- **E1 — no postpone bound.** `postponeItem` re-queues a lock-contended (`hard_delete_in_progress`) item with `RetryCount` unchanged ([worker.go:361-376](../internal/gc/worker.go#L361)). Intentional (lock contention should not push toward the DLQ), but with no bound and no metric a permanently stuck hard-delete lock loops forever with no DLQ/alert.

  **Rescoped 2026-08-14 — E1 is no longer a corner case.** The X2 series added three
  more unbounded-postpone conditions to the original one (`destructive_fail_closed`,
  `block_claim_not_yet_stale`, `block_claim_release_unconfirmed`), and
  `failClosedIfUnavailable` applies the first at *every* statement of the destructive
  block walk rather than at a single call site. So the surface grew from one narrow
  lock-contention path to most of the block path under a degraded cluster. Each
  addition is individually correct — losing the work item is the worse failure in all
  of them — but a postpone bound now has to be sized for the block path, not for hard
  delete locks, and it needs a counter distinct from `retry_count`, which is a queue
  protocol change. Detail and the per-condition reasoning are under
  `ISSUE-GC-CROSS-DC-REFERENCE-VISIBILITY-01`. `block_claim_release_unconfirmed` is the
  only one that postpones on non-environmental errors, and it carries a dedicated
  `gc_errors_total{type="stale_claim_release_failed"}` counter for exactly that reason.
- **E6 — queue lifecycle arbitration is not global.** `DequeueBatch` is a plain `SELECT`
  without a lease, so multiple workers may hold the same `gc_queue` row. `RequeueItem`
  uses an ordinary logged `DELETE(old)` + `INSERT(new)` batch, while `CompleteItem` and
  `FailItem` use their own ordinary batches. If a stale worker reaches a generic requeue
  after another lifecycle has advanced the row, Cassandra can apply the insert even when
  the delete addressed nothing; `queued_at` is part of the key, so a duplicate lifecycle
  remains durable. The same authority gap covers `Requeue` vs `Complete`, `Requeue` vs
  `Fail`, `Complete` vs `Fail`, DLQ and pending-marker state. This is a queue-protocol
  follow-up, not closed by the P4a late-loser fix: a `block_claim_foreign_owner` result now
  exits at `processOrg` without calling `CompleteItem`, `RequeueItem`, `FailItem` or retry
  mutation, but ordinary retry/postpone paths still need one lifecycle authority.

  **E1 disposition (2026-10-16): OPEN PRE-GC** for queue lifecycle/liveness,
  with the existing Low/Low-Med severity preserved. The [exact-D block safety
  argument](./E1-FINAL-DISPOSITION.md) does not close these ordinary queue races
  or certify other item types. Replaying the same D/K1 is not new authority over K2.

  One interleaving belongs explicitly in that race matrix, because it shows why the
  follow-up cannot be "put a CAS on `RequeueItem`". P4a binds the QUEUE decision to the
  CLAIM outcome, and those are observed at different instants:

  ```
  A releases its claim   -> Released      (A genuinely still owned it here)
                            [window]
  B claims               -> D2            (A is now stale, and does not know)
  A returns to processOrg -> Requeue, or FailItem at the retry cap
  ```

  A passed the ownership check honestly and then mutated the queue as a stale worker. At
  the cap that parks an `ItemBlock` in a DLQ it never leaves while B holds a live fence —
  the same stranded shape P4a closes from the other side. Closing it needs the queue
  lifecycle to carry its own authority (a lease, or a lifecycle token that makes a stale
  mutation impossible), not a conditional write on one transition. Measured evidence for
  the duplicate-row half is recorded in `docs/GC-QUEUE-DEPTH-MODEL.md`.
- **E2 — `dryRun` data race vs cutover semantics.** `dryRun` is read/written concurrently
  without synchronization. `atomic.Bool` fixes the Go race and visibility, but does not stop work
  already past its check; hard cutover requires drain/serialization or destructive-step rechecks.
- **E3 — escalated to P6a and fixed.** `LibraryExists`/`GroupExists` previously swallowed errors
  as "missing"; non-`ErrNotFound` errors now propagate and scanners fail closed.
- **E4 — pending projection drift.** Queue completion, DLQ
  deletion, and DLQ expiry remove `gc_pending_items` in their logged batches
  (`CompleteItem` [store_cassandra.go:580-604](../internal/gc/store_cassandra.go#L580),
  `DeleteFailedItem` [store_cassandra.go:868-889](../internal/gc/store_cassandra.go#L868),
  `DeleteExpiredFailedItem` [store_cassandra.go:891-942](../internal/gc/store_cassandra.go#L891),
  all via `addPendingItemDeleteBatchQuery`
  [store_cassandra.go:475](../internal/gc/store_cassandra.go#L475)). No independent reconcile
  exists for historical/manual/ambiguous drift. A blind TTL could expire valid dedup protection
  while queue/DLQ work remains live. **Update (2026-07-13): a concrete, unbounded live-path
  source of this drift was found, root-caused, and fixed — the block/library-scope mismatch.
  See `ISSUE-GC-PENDING-ITEM-BLOCK-LIBRARY-SCOPE-01` below.** The independent reconcile/backstop
  (10D) is still open for pre-existing orphans and other drift sources.
- **E5 — S3-orphan recovery lease-only exclusion.** `RecoverS3Orphans` ([worker.go:616-789](../internal/gc/worker.go#L616)) has no per-row LWT; mutual exclusion relies on the leader lease. Real double-processing risk only under a lease split-brain.

#### Reviewed and found NOT to be bugs

- "Infinite DLQ↔queue loop when the marker is deleted mid-cascade" — **not a bug**: the skip returns `nil` → the item is `Complete()`d (removed) ([worker.go:1243-1244](../internal/gc/worker.go#L1243) → [worker.go:311](../internal/gc/worker.go#L311)); Phase 13 lists from the marker, so a gone marker is not re-listed.
- "Resurrection guard missing for the `pending_s3` phase" — **not a bug**: the branch checks `BlockExists` before the S3 delete and defers if the canonical row exists ([worker.go:712-725](../internal/gc/worker.go#L712)).
- "Child-enqueue vs parent-delete race in `processCommit`/`processFSObject`" — **mitigated**: children carry `RequiresLibraryDeletedCheck` and their own `acquireLibraryDeleteGuard` catches restore/re-delete ([worker.go:1729-1830](../internal/gc/worker.go#L1729)).

#### Fix Direction (branches 10A–10E)

- 10A: `atomic.Bool` plus an explicit decision on hard-cutover semantics.
- 10B: consistently surface remaining scanner errors; P5/P6 own their specific paths.
- 10C: meter/bound repeated postpones without premature DLQ.
- 10C also needs an explicit lifecycle authority for `Requeue`/`Complete`/`Fail`; do not
  add a partial LWT to `RequeueItem` while the other queue transitions remain ordinary.
- 10D: audit/reconcile pending rows against queue + retained DLQ; no standalone TTL unless
  lifetimes are coordinated.
- 10E: decide E5 explicitly — accept leader-lease exclusion as design because operations are
  largely idempotent, or add a per-row orphan claim/LWT if split-brain tolerance is required.

#### Related Docs

- `docs/GC-DELETE-CLEANUP-INVESTIGATION.md` (Engine-level review)
- `docs/GC-SERVICE-ANALYSIS.md`

---

### ISSUE-GC-CROSS-ORG-BLOCK-DELETE-01: complete org-scoped physical block isolation

**Status**: ✅ **FIXED (2026-07-16); org-scoped-key series complete through PR-3**
**Severity**: **High pre-fix; resolved by end-to-end org-scoped physical keys.**
**Affected**: Historical pre-fix `internal/gc` block deletion and orphan recovery.

#### Problem

The reproduced pre-PR-2 root cause was:

- **S3 objects were global per storage class.** API funnels used
  `blocks/<h0:2>/<h2:4>/<hash>`, so every org storing those bytes in one class shared an object.
- **Liveness is per-org.** `blocks` and `block_references` are partitioned by `org_id`.

`processBlock` mixes the two: it decides with `BlockHasReferences(item.OrgID, item.ItemID)`
([worker.go:412](../internal/gc/worker.go#L412), [store_cassandra.go:1849](../internal/gc/store_cassandra.go#L1849)),
finalizes the org-scoped row ([store_cassandra.go:2118](../internal/gc/store_cassandra.go#L2118)), then
deletes the **global** key ([worker.go:583](../internal/gc/worker.go#L583) →
[blocks.go:251](../internal/storage/blocks.go#L251)). "No refs **in this org**" is treated as "nobody
needs these bytes".

The claim+verify fence did not help: it re-checked the same org-scoped partition. PR-2 moved API
writes/reads to `blocks/<org_id>/<h0:2>/<h2:4>/<hash>`; PR-3 moved GC delete and orphan recovery to
that same locator and removed the legacy global APIs.

#### Confirmation

**Real two-org E2E (operator, 2026-07-16) — the definitive evidence.** ~2 GB of **identical** files
uploaded through the API into **two** orgs. MinIO showed **2 GB**, not 4 — the upload path deduplicates
globally by content hash, so both orgs' metadata pointed at one physical copy. Deleting **all** files
in one org and draining GC left the MinIO volume **empty**, and the other org's files become
**unreadable** — the external request has already returned 200, then the 404/`NoSuchKey` happens
internally while fetching the block, truncating the body:

```
[ServeRawFile] Failed to get block 0/1: ... GetObject ... StatusCode: 404 ... NoSuchKey
GET /repo/3acb1e2b-…/raw/01.pdf status=200   (headers already sent, body truncated)
```

This is unambiguous live-content deletion from real uploads, and it also shows the 404 lands
**mid-response** (status line already 200), so the download fails with a corrupt body, not a clean error.

**Seeded repro (single-node docker stack) — the mechanism in isolation.** For a tighter loop, org A
uploaded through the API and org B was **seeded to the equivalent post-upload state** (`blocks[(B,hash)]`
+ an `fs:` ref for the same hash — exactly what B's own upload produces; org B did **not** re-run the
upload). Precondition verified: **2 `blocks` rows, 1 S3 object**. Deleting org A's library and draining
GC left, at ~210s, `objetoS3=0`, `blocks_rows=1`, **org B's `fs:` ref still live** — metadata intact,
content gone. (Because org B was seeded rather than re-uploaded, this is a "second org in the equivalent
state" repro; the real E2E above is the full-upload proof. The committed regression test below must use
two real uploads.)

Ordinary multi-tenancy triggers this: identical READMEs, empty files, shared templates, any
re-uploaded document.

#### Resolution design

1. **Chosen: org-scope the physical key** — `blocks/<org_id>/<h0:2>/<h2:4>/<hash>`. Aligns physical
   ownership with the existing per-org tables, claims and references; no coordination needed. Costs
   cross-org dedup (each org stores its own copy). **Migration-free on an empty (greenfield) store —
   but not trivial: it requires *all* storage paths (write, read, reuse, verify, GC delete + orphan
   recovery) to resolve the org-scoped locator, and the org must enter the `BlockStore` at
   construction with fail-closed validation so no path can ever produce a global key again.** This
   is the implemented approach — see the org-scoped-key series below.
2. **Global liveness per `(storage_class, block_id)`** — a global ref/owner registry plus a global claim
   before physical delete. Keeps cross-org dedup, but needs global coordination and is materially more
   complex.

Do **not** paper over it with a pre-delete `HEAD`/existence probe: that is a TOCTOU race, not a fix.

#### Required regression test (cross-org)

Two orgs upload identical bytes to the same storage class; delete one org's library and drain GC;
assert the other org can still download the file **and** the physical object still exists. This is
implemented by `TestGC_CrossOrgIdenticalBlockDeleteIsolation` against real Cassandra+MinIO. It uses
**two dedicated, test-exclusive tenant orgs** (created at runtime via the superadmin admin API, each
authenticated with a directly-minted API key) rather than the shared default/platform orgs, so the
private per-org GC drain (`ProcessOrgOnce`) only ever touches this test's work. The S3 orphan recovery
E2E also seeds an identical sibling-org object and verifies it survives byte-for-byte. Complementary
coverage: the API-level `TestWebBlockUploadIdenticalBytesUseDistinctOrgKeys` proves identical bytes
resolve to distinct physical keys across the default and platform orgs, and the adapter unit test
`TestStorageManagerAdapterScopesPlatformAndTenantKeys` pins `PlatformOrgID` handling in GC.

**Manual full-loop confirmation (2026-07-16), mirroring the original bug repro:** the same ~0.5 GB
file was uploaded into **two orgs**; MinIO showed ~**1 GB** used (no cross-org dedup — each org holds
its own `blocks/<org_id>/...` object). Deleting the library in one org and draining GC dropped MinIO
back to ~**0.5 GB**, and the other org kept **streaming its file** with no interruption. This is the
exact scenario that emptied the bucket and truncated the sibling org's download before the fix; it now
closes end to end.

#### Series progress (org-scoped-key)

The fix ships as a small, sequential series of branches (each its own PR):

- ✅ **PR-0 — docs.** This section + the audit doc corrected (header, mid-response 404, path list).
- ✅ **PR-1 — storage layer (org-aware `BlockStore`), no behavior change.** `NewOrgBlockStore`
  ([internal/storage/blocks.go](../internal/storage/blocks.go)) validates the org id **fail-closed**
  (must be a valid UUID, normalized to canonical form; rejects empty/whitespace/non-UUID/path chars)
  and org-scopes `hashToKey` → `blocks/<org_id>/<h0:2>/<h2:4>/<hash>`. The **nil UUID is accepted on
  purpose** — it is the platform org (`PlatformOrgID`, `00000000-…`), a real partition in the per-org
  `blocks`/`block_references` tables; the empty-string guard still catches an actually-missing org.
  `Manager.GetBlockStoreForOrg` /
  `GetHealthyBlockStoreForOrg` cache per `(org, class)` via a struct key
  ([internal/storage/storage.go](../internal/storage/storage.go)). Legacy `NewBlockStore`/
  `GetBlockStore` were kept temporarily until caller migration and are removed by PR-3.
  Unit tests cover fail-closed validation, org-scoped keys, per-org cache separation, and pin the
  legacy key (regression). This was plumbing only; production activation starts in PR-2.
- ✅ **PR-2 — API funnels.** Write/read/reuse/verify/fallback paths in v2 files/blocks/OnlyOffice,
  seafhttp, sync and shared file/share-link resolution now construct or resolve an org-scoped store.
  Reuse always derives the canonical key from that store and fails closed if a persisted `storage_key`
  differs. The process-wide org-less API singleton is no longer constructed. Integration coverage pins
  same-org dedup across libraries and distinct physical keys plus byte-for-byte reads across the default
  and platform orgs. This intermediate state required GC to remain disabled until PR-3.
- ✅ **PR-3 — GC own branch:** delete + orphan recovery resolve `(org_id, normalized canonical storage_class)`,
  empty orphan classes fail closed, and the legacy global APIs are removed so the
  storage layer no longer derives a delete target on a caller's behalf. That is
  not, by itself, a compiler proof that a caller loaded `blocks.storage_key` or
  that an explicit locator belongs to the org; the cross-org Cassandra+MinIO
  regressions above pin the production caller checks.
- Broader multiregion/bucket coverage remains useful follow-up coverage, but it is not required to
  close P10's locator mismatch: the exact-class adapter contract and org-scoped physical isolation
  are covered in PR-3.

#### Related Docs

- [GC-DELETE-CLEANUP-INVESTIGATION.md](GC-DELETE-CLEANUP-INVESTIGATION.md) — P10 in the confirmed-gap table.

---

### ISSUE-GC-PENDING-ITEM-BLOCK-LIBRARY-SCOPE-01: Block `gc_pending_items` rows leak (library-scope mismatch)

**Status**: 🟢 Fixed (2026-07-13) — brownfield orphan sweep optional (10D/8B); N/A on greenfield prod
**Severity**: Low-Med — unbounded metadata growth + scan cost; **no data-safety impact**
**Affected**: `internal/gc` block enqueue paths, `gc_pending_items`

#### Problem (confirmed live-path leak, was E4 "drift risk") — HISTORICAL, fixed in PR #128

> **This section describes the code as it was at `253e08fef`** (the pre-fix parent of
> `869a455f3`). Its code links are **pinned to that commit on purpose**: on current `main` these
> lines read `uuid.Nil` and would contradict the text. See "Fix" below for current-code links.

`gc_pending_items` accumulated orphaned `block` rows proportional to deleted block volume. A real
50 GB upload → delete → GC-drain on a wiped dev cluster drove it from ~575 to **9,633 rows**
(9,629 `block`), with `gc_queue = 0` / `gc_block_candidates = 0` / `gc_failed_items = 0` and no
TTL (`default_time_to_live = 0`). Sampled orphan rows pointed at blocks that were already
physically deleted. The 50 GB of **content** (blocks, refs, commits, S3 objects) was reclaimed
correctly — only the dedup rows leaked.

#### Root cause (at `253e08fef`)

`gc_pending_items` is keyed by `library_id` (in the partition `bucket` hash
[store_cassandra.go:251-259](../internal/gc/store_cassandra.go#L251) **and** as a clustering
column [001_initial_schema.cql:1206](../internal/db/migrations/001_initial_schema.cql#L1206)),
but `gc_queue` is not (library-independent bucket + key). Blocks are content-addressed;
`processBlock` never reads `item.LibraryID` ([worker.go:403-468](../internal/gc/worker.go#L403)).
All three block-enqueue **dedup checks** used `uuid.Nil`, but two paths **wrote** the pending row
under the **real** `libraryID`:

- `worker.enqueueZeroRefBlocks` — `LibraryID: libraryID` ([worker.go:1619@253e08f](https://github.com/Sesame-Disk/sesamefs/blob/253e08fef/internal/gc/worker.go#L1619)) — **the live leaker** (every cascade/version/auto-delete block release).
- `Service.EnqueueBlock` — passed `libraryID` ([gc.go:422@253e08f](https://github.com/Sesame-Disk/sesamefs/blob/253e08fef/internal/gc/gc.go#L422)) — latent; sole non-test caller already passed `uuid.Nil`.
- `scanner.scanOrphanedBlocks` — `LibraryID: uuid.Nil` ([scanner.go:386@253e08f](https://github.com/Sesame-Disk/sesamefs/blob/253e08fef/internal/gc/scanner.go#L386)) — **correct reference.**

A single producer is self-consistent (`CompleteItem` re-reads `library_id` from the same queue
row it completes). The leak needed **two** producers enqueuing the same block/candidate under
different `library_id`s (worker `realLib` + scanner `Nil`, or two libraries sharing a block):
same `candidate_at` collapsed them to one `gc_queue` row (last writer wins on the non-key
`library_id` column) but each had already written its own `gc_pending_items` row
(`bucket(realLib)` + `bucket(Nil)`). `CompleteItem` deleted only the row matching the surviving
queue row's `library_id` ([store_cassandra.go:580-604](../internal/gc/store_cassandra.go#L580) —
unchanged by the fix, so this link tracks current `main`); the other orphaned forever.

**Bug, not intentional (git history):** `worker.go` enqueue written with the real `libraryID`
on 2026-04-09 (`e9a9b369b`); its dedup check migrated to `uuid.Nil` on 2026-04-30 (`5dee7eee2`)
**without** updating the paired enqueue — incomplete migration. Scanner phase (2026-05-26,
`f3597a935`) does both sides `Nil`, codifying the intended convention.

#### Fix — merged 2026-07-13, PR #128

`fix/gc-pending-items-block-library-scope` (`869a455f3` producers, `08402b3f9` central coercion).
Two layers; links below track **current `main`**:
1. **Producers** — every `ItemBlock` enqueue keys `uuid.Nil` (`worker.enqueueZeroRefBlocks`
   [worker.go:1633](../internal/gc/worker.go#L1633), `Service.EnqueueBlock`, and
   `scanner.scanOrphanedBlocks` [scanner.go:386](../internal/gc/scanner.go#L386)), matching the
   dedup checks.
2. **Central backstop** — the store pending helpers (`addPendingItemBatchQuery`,
   `addPendingItemDeleteBatchQuery`, `PendingItemExists`) coerce `ItemBlock`'s `library_id` to
   `uuid.Nil` via `pendingItemLibraryID`
   ([store_cassandra.go:448-464](../internal/gc/store_cassandra.go#L448)), so every pending
   write/delete/dedup-read for a block lands on one key regardless of the caller. This makes the
   invariant impossible for a future producer to break (the original bug was exactly a partial
   migration between the check and the write).

Content-only, no data-safety impact. Coverage: a pure-function unit test for the coercion, a
worker unit test that the producer keys `uuid.Nil` (fails pre-fix), and an integration test that
reproduces the real **two-producer** collision (worker cascade + scanner `uuid.Nil` at the same
`candidate_at`) and asserts no orphaned pending row survives the drain.

#### Still open

Pre-existing orphaned rows on **brownfield** clusters are not self-healed by the fix — they need
the reconcile sweep (`ISSUE-GC-RECONCILE-BACKFILL-01` / branch 8B) or a coordinated one-off
cleanup. **Not present on greenfield prod.** The general `gc_pending_items` reconcile/backstop is
10D under `ISSUE-GC-ENGINE-ROBUSTNESS-01`.

#### Related Docs

- `docs/GC-DELETE-CLEANUP-INVESTIGATION.md` ("Live-path verification and confirmed `gc_pending_items` leak (2026-07-13)")

---

### ISSUE-GC-RECONCILE-BACKFILL-01: No Reconcile/Backfill for Already-Orphaned Blocks + S3 Objects

**Status**: ⏸ Deferred for greenfield prod (2026-07-15) — brownfield follow-up only
**Severity**: Medium — production hygiene for **existing** clusters with pre-fix residue
**Affected**: brownfield clusters with residue from the gaps above or from pre-#123 deletes

#### Problem

The GC engine has crash recovery for its own in-flight deletes (`gc_s3_orphans`) but no sweeper for
content orphaned by historical delete-path gaps or pre-#123 behavior: `fs:` refs whose library is
gone, blocks with no ref and no candidate, candidates with no queue item, stale
`gc_libraries_by_policy` rows, pre-fix `gc_pending_items` orphans, and S3 objects with no `blocks`
row. Blind `TRUNCATE` is unsafe — truncating `block_references` without deleting the S3 objects
would orphan MinIO further.

**Greenfield prod deploy:** starting from an empty keyspace and buckets, this issue does **not**
apply. No reconcile/backfill pass is required before launch.

#### Fix Direction (branches 8A–8C — brownfield only)

- 8A: **read-only** reconcile (`--dry-run` default, per-org scope, paginated, JSON, no S3 delete) that reports every orphan class above.
- 8B: low-risk repairs (delete stale policy rows; re-enqueue existing candidates; block with no refs → `EnsureBlockGCCandidate`, never a direct delete).
- 8C: conservative `fs:` orphan repair (fs_object exists → enqueue; missing + lib gone + marker → delete only that ref → zero-ref → candidate). Do not touch `pub:` or delete S3 directly from the reconciler; let the safe worker protocol do the physical delete.

#### Related Docs

- `docs/GC-DELETE-CLEANUP-INVESTIGATION.md` (Proposed work item 4, branch roadmap)

---

### ISSUE-DOWNLOAD-NO-404-01: SeafHTTP download can never report a file as gone

**Status**: 🟡 Accepted trade-off (PR-6) — **decision deferred**, see Fix Direction
**Severity**: Medium — operational cost and a cross-layer inconsistency, not data loss
**Affected**: `GET /seafhttp/files/:token/*filepath` and the ZIP directory walk

#### Problem

PR-6 made download fail closed. Absence is never reported: a validated directory
listing that does not name the entry produces the internal `errDirEntryAbsent`
sentinel, but the HTTP classifier maps it to **503 + `Retry-After`**, exactly like
dangling metadata and corruption. The endpoint therefore has **no 404 at all**.

The reason absence cannot be trusted: production metadata reads use `LOCAL_QUORUM`,
and `access_tokens` and `libraries`/`fs_objects` are independent partitions with no
global replication order. A node can serve a freshly minted download token while
reading a directory listing that has not replicated yet, so "the listing does not
name it" may simply mean "this DC has not seen the create". Answering 404 there
tells a sync client the file was deleted, and it may drop its local copy — the
failure mode PR-6 exists to prevent.

**Accepted costs:**

1. **A genuinely deleted file never says so.** It answers 503 forever, so clients
   retry until their own budget runs out. Reaching this state requires the file to
   be deleted between token issuance and use, so the window is narrow, but the
   retries are unbounded work for a request that can never succeed.
2. **The layers disagree.** `internal/api/v2` still answers 404 for a missing file
   or directory; only the SeafHTTP download surface is 503-only. A client that talks
   to both sees two different answers for the same missing path.

An earlier draft of the PR-6 plan specified "404 only when a directory was read,
fully validated, and does not list the entry". That was reversed deliberately; this
entry records the reversal and its price so it is not rediscovered as a bug.

#### Fix Direction

**Deployment context (confirmed 2026-07-23).** Production is multi-DC with one
region each in the Americas, Asia and Europe, and measured cross-DC latency of
**several seconds**. That settles two things:

1. The stale-snapshot window this issue exists for is real and wide, not
   theoretical. A local listing can trail another region by seconds, so a local
   miss is a weak signal for absence. Keeping 404 out of the download surface is
   the right default for this topology.
2. Any fix that adds a global consensus read to the hot path is rejected on
   principle — the project already avoids Paxos in hot paths for this reason.
   `ALL`, global `QUORUM` and `SERIAL` are all off the table, and not only
   because two of them are unsound here (`QUORUM` has no guaranteed intersection
   with a local write quorum across DCs; `SERIAL` only linearizes LWTs on one
   partition and does not order `access_tokens`, `libraries`, `commits` and
   `fs_objects` relative to each other).

**Deferred decision.** Whether to reintroduce a 404 at all, and at what cost, is
an open product decision. It is NOT blocking PR-6: the current behaviour is the
safe direction, and the cost is retries plus a cross-layer inconsistency, not
data loss. Revisit when someone owns the client-retry budget question.

The only viable direction identified so far, because it needs no global read:

- Carry the commit id the token was minted against, and treat "listing older than
  that commit" as unproven rather than absent. It separates replication lag from a
  real delete using only local reads, so it costs nothing on the hot path.

Rejected, recorded so they are not re-proposed:

- **Read at `ALL` on the miss path.** It is the only consistency that can observe
  every replica under ordinary `LOCAL_QUORUM` writes, but with several seconds of
  cross-DC latency it turns every miss into a multi-second request, and one
  unreachable replica turns it into an error. Unacceptable on a download path.
- **Global `QUORUM` or `SERIAL`.** Unsound here regardless of cost: `QUORUM` has no
  guaranteed intersection with a local write quorum across DCs, and `SERIAL` only
  linearizes LWTs on a single partition — it does not order `access_tokens`,
  `libraries`, `commits` and `fs_objects` relative to each other.
- **Restrict the 404 to single-DC deployments.** Production is multi-DC in all
  regions, so this would be dead configuration.

Revisit alongside X2 (multi-DC reasoning is derived, never reproduced) in
`docs/UPLOAD-FENCE-FINDINGS-REGISTRY.md`.

#### Related Docs

- `docs/GC-UPLOAD-FENCE-PR-PLAN.md` (PR-6 scope and contract)
- `docs/UPLOAD-FENCE-FINDINGS-REGISTRY.md` (F5, F13, X2)

---

### ISSUE-ENCRYPTED-FLAG-UNCHECKED-01: A failed `encrypted` probe defaulted to "not encrypted"

**Status**: ✅ Fixed (2026-07-22) — all six fail-open sites closed; kept as the rationale record
**Severity**: was High (durable plaintext in an encrypted library; two authorization gates opened by a timeout)
**Affected**: `internal/api/v2` — `files.go`, `batch_operations.go`, `fileview.go`, `sharelink_view.go`

#### Problem

The library encryption flag was read as `Scan(&encrypted)` with the error either
**discarded** or explicitly turned into "allow". Go leaves `encrypted` false on
failure, so any transient Cassandra error — a timeout, a coordinator hiccup,
cross-DC lag — was indistinguishable from "this library is not encrypted", and the
caller took the plaintext branch.

Six sites, in two classes. The distinction matters: the first class corrupts or
exposes data, the second bypasses an authorization gate.

**Class A — error discarded, plaintext branch taken:**

| Site | Consequence |
|---|---|
| `files.go` `UploadFile` | **Persisted plaintext into a library the user believes is encrypted.** Durable, and not detectable afterwards from the stored object. The worst of the six. |
| `fileview.go` `ServeRawFile` | Streamed ciphertext to the viewer labelled as the real content. |
| `sharelink_view.go` `handleShareLinkRaw` | Served ciphertext as a `200` on a **public, unauthenticated** surface. |
| `sharelink_view.go` `readFileContentAsText` | Embedded ciphertext into a share-link preview as if it were the file's text. |

**Class B — error converted into permission (`return true`):**

| Site | Consequence |
|---|---|
| `files.go` `requireDecryptSession` | A probe failure granted access to an encrypted library with no decrypt session. Guards **20 call sites**. |
| `batch_operations.go` `checkDecryptSession` | Same, guarding 2 call sites. |

Both Class B comments read "Library not found - let the caller handle it", which is
the reasoning error: `err != nil` covers a timeout as well as a missing row, and only
one of those is safe to treat as "no library, no encryption".

PR-6 found and fixed this shape first in SeafHTTP's `HandleZipDownload`, which had the
same discarded error and produced a `200` ZIP of ciphertext.

#### Fix

`libraryIsEncrypted` in `internal/api/v2/encryption.go` is now the single probe and
propagates its error; `seafHTTPLookupLibraryEncryptedFn` is its SeafHTTP twin. Each
caller fails closed **in whatever way its own contract allows**, which is not one
uniform response:

- handlers with a `gin.Context` emit `respondEncryptionProbeUnavailable` — a
  retryable `503` + `Retry-After`, deliberately **not** `403 lib_need_decrypt`,
  because a failed probe means we do not know whether the library is encrypted and
  the client must retry rather than be told to prompt for a password;
- `readFileContentAsText` now returns `(string, error)`. It reports `("", nil)`
  only where there is genuinely nothing to inline (no target entry, over the 1MB
  limit) and an error otherwise, which the share bootstrap turns into 503 for a
  transient failure and 403 for a locked encrypted library — matching what the raw
  share-link surface already answered for that state. Returning `""` for either
  case rendered a non-empty document as a silently blank 200;
- the two guards return `false` after writing the 503, so the gate closes.

Three further probes (`block_upload_session.go`, and the two historic-file paths in
`fileview.go`) already failed closed and were left as they were.

**A missing library row now answers 503 too, and that is deliberate.**
`libraryIsEncrypted` propagates `gocql.ErrNotFound` like any other error, so a
library that has no row for `(org_id, library_id)` produces the retryable 503
rather than falling through to the handler's own not-found handling. The two
Class B comments justified their `return true` as "library not found", but a
`LOCAL_QUORUM` read cannot tell a genuinely absent library from one this DC has
not replicated yet — the same reasoning as `ISSUE-DOWNLOAD-NO-404-01`, and
treating the missing row as "not encrypted" is exactly the permissive default
this issue exists to remove.

The reachability is narrow, which is why it is recorded rather than mitigated:
`orgID` always comes from the caller's own context (`c.GetString("org_id")`), so
libraries are org-scoped with no cross-org read pattern, and every handler runs
its permission check first — `HasLibraryAccessCtx` fails for a library that does
not exist and `respondIfLibraryMissing` answers the proper 404 there. What is
left is the race where the library is deleted between the permission check and
the probe, where 503 is the honest answer. If a surface ever needs to
distinguish the two, it must resolve absence explicitly before the probe, not by
reading a failed probe as permission.

**Gate ordering is part of the contract, not just the probe.** `ServeRawFile`
originally ran its ETag revalidation *before* the probe. `Cache-Control: private,
no-cache` lets a browser keep the decrypted bytes and only forces revalidation, so a
`304` re-authorised a cached plaintext copy after the decrypt session expired, and
also hid the probe-failure `503` from any request carrying `If-None-Match`. The gate
now precedes every short circuit and every write in all four handlers, pinned by
`TestEncryptionGateRunsBeforeShortCircuitsAndWrites`, which compares the source
positions of the two calls — a helper-level unit test cannot observe ordering.

**Watch for recurrence:** the general shape is a discarded `Scan` error on a flag
whose zero value is the permissive answer. A new probe that bypasses
`libraryIsEncrypted` reintroduces it.

#### Related Docs

- `docs/GC-UPLOAD-FENCE-PR-PLAN.md` (PR-6 fixed the SeafHTTP ZIP instance first)

---

### ISSUE-SHARELINK-PASSWORD-BYPASS-01: Password-protected share links serve content to anonymous callers

**Status**: ✅ Fixed (2026-07-25) — kept as the rationale record; filed 2026-07-24, re-verified against code 2026-07-25
**Severity**: was High — authentication-control bypass with content disclosure; single-node reachable, no hash or credential needed beyond the public link token
**Affected**: `GET /api/v2.1/share-links/:token/bootstrap[/]` and `GET /api/v2.1/share-links/:token/files/bootstrap[/]`
**Source of record**: NF-1 / SH-6 in `docs/PROD-SECURITY-READINESS-20260724.md`

#### Problem

Both public bootstrap endpoints build their payload through
`emitShareFileBootstrap` → `buildShareFileBootstrapResponse`
(`internal/api/v2/sharelink_view.go`), and **neither handler checks the link
password first**. `GetShareLinkBootstrap` and `GetShareLinkFileBootstrap` verify
only that the link resolves and is neither expired nor disabled.

Two distinct disclosures follow from that one missing gate:

1. **Inline text/markdown content.** For a `sharedFileViewText` or
   `sharedFileViewMarkdown` bundle, `buildShareFileBootstrapResponse` calls
   `readFileContentAsText` (1 MB ceiling) and
   `buildSharedFileBundleBootstrap` puts the result in `pageOptions.fileContent`
   unconditionally. The same payload also sets `needPassword: true`, so the
   server tells the client to prompt for a password **while shipping the bytes
   the password protects**. The password prompt is decorative; the content is
   already in the JSON body.

2. **OnlyOffice download token** — *not recorded in the original report, found
   during the 2026-07-25 code validation*. When `onlyoffice.enabled` is true and
   the shared file is OnlyOffice-viewable (`doc/docx/odt/xls/xlsx/ods/ppt/pptx/odp`
   and friends), `buildShareFileBootstrapResponse` takes the OnlyOffice branch
   **before** the text branch and calls `buildOnlyOfficeShareBootstrap`, which
   mints a real `CreateLinkDownloadToken` and embeds the resulting download URL in
   the response. An anonymous caller therefore receives a working download
   credential for a password-protected office document. This path has no 1 MB
   ceiling and is not limited to text, so it is broader than (1); it is narrower
   only in requiring OnlyOffice to be configured with a JWT secret.

The raw/download surface is **correctly gated** — `handleShareLinkRawOrDownload`
answers 403 `Password required` before serving — which is why the gap survived:
the obvious exploit path is closed and the bootstrap JSON was assumed to be
metadata.

`buildSharedDirPageBootstrap` (directory links) is a lesser case: it computes
`passwordVerified` and does not embed file content, and the dirent listing
endpoints (`ListShareLinkDirents` and the download/raw/zip paths) each verify the
password. Directory links leak only link metadata (library name, sharer name).

#### Fix Direction

Gate the content read, not just the response. In
`buildShareFileBootstrapResponse`, verify
`sl.passwordHash == "" || h.verifyShareLinkPasswordCookie(c, sl.token, sl.passwordHash)`
**before** the OnlyOffice branch, and on failure return a bundle whose
`needPassword` is true and whose `fileContent` is empty and whose OnlyOffice
config is absent. Putting the check inside the builder rather than in the two
handlers is what makes it hold: the two endpoints already share this builder, so
one gate covers both, and a third caller cannot forget it.

Regression coverage must assert the **body**, not the status: a 200 whose
`pageOptions.fileContent` is empty and that carries no `onlyOfficeConfig` is the
contract. Cover both endpoints and both branches (text/markdown and an
OnlyOffice-viewable extension with OnlyOffice enabled) — a test that only drives
`.md` will not see the download-token leak.

#### Fix as shipped (2026-07-25)

Three layers, because they fail differently:

1. **`buildShareFileBootstrapResponse` resolves the password once, before either
   branch can do protected work.** The OnlyOffice branch is skipped outright, so
   `CreateLinkDownloadToken` is never reached for an unverified caller, and the
   inline-text read is skipped rather than performed-and-discarded — that read is
   a Cassandra lookup plus an S3 fetch plus a decrypt, so serving it would have
   let anyone holding the token drive that work on every request.
2. **`buildSharedFileBundleBootstrap` drops `fileContent` and `smartLinkMap`
   whenever `needPassword` is true.** It already computed `passwordVerified` for
   the flag; now the flag and the data cannot disagree. This is the layer that
   survives a future caller assembling content itself.
3. **`buildOnlyOfficeShareBootstrap` fails closed on an unverified password**
   (`errShareLinkPasswordRequired`) before minting, so a future direct call of
   the helper cannot reopen the token half even if it bypasses the builder gate.

`needPassword` still ships as `true` and the frontend still renders the password
dialog, so the UX is unchanged: `SharedLinkPasswordDialog` posts to
`/api/v2.1/public-links/:token/check-password` and reloads, and the reload
re-fetches the bootstrap with the cookie set and gets the content.

Coverage, in two tiers:

**Unit** (`internal/api/v2/sharelink_view_password_gate_test.go`): both branches
withheld without a cookie (asserting the body and that the token was never
minted), both served with a valid HMAC cookie and on unprotected links, the
bundle builder dropping content handed to it directly, the OnlyOffice helper
failing closed when called without a cookie, and an AST check over **every
non-test file in package `v2`** that both endpoints reach the gate through the
one emitter and that only the gated builder calls the OnlyOffice helper.

**Integration** (`internal/integration/sharelink_password_gate_test.go`), two
tests because one fixture cannot reach both halves:

- `TestShareLinkBootstrapPasswordGateOnBothEndpoints` — the **inline-content**
  half across **both public endpoints**. Anonymous HTTP against the live cluster,
  no auth header and no cookie, asserting the body withholds `fileContent` while
  keeping `needPassword: true`; then the real `check-password` exchange and a
  re-request with the returned cookie, asserting the content *is* served. That
  second half matters as much as the first: a gate that always denied would pass
  the exploit assertion and silently break the feature.
- `TestShareLinkBootstrapWithholdsOnlyOfficeCredentialWithoutPassword` — the
  **credential** half. It needs a separate test because the fixture above is
  `notes.md` and `isOnlyOfficeViewable("md")` is **false**: Markdown never enters
  the branch that mints the token, so asserting `onlyOfficeConfig` absent there
  would pass against the vulnerable code. Only an OnlyOffice-viewable extension
  reaches it. Its verified half doubles as a guard against a vacuous pass — if
  OnlyOffice were disabled or its JWT secret unset, the helper would error, the
  builder would fall back to the plain bundle, and `onlyOfficeConfig` would be
  absent in *both* directions; requiring it present after the password exchange
  turns that misconfiguration into a failure rather than a false green.

The AST check earns its keep but is deliberately described as a tripwire, not a
proof — it is syntactic, never executes a handler, and cannot see a call reached
through a function value or another package. The two integration tests are what
close the runtime gap, now for both halves.

Every assertion was verified by mutation rather than trusted green. At unit
level: reverting any one layer, or letting an endpoint bypass the emitter, or
adding a second caller of the OnlyOffice helper, fails a named test. At
integration level, against the rebuilt live cluster:

- Reverting all three layers reproduced the **content** exploit verbatim — both
  endpoints answered `200` with `"fileContent":"SECRET-…"` next to
  `"needPassword":true`.
- Reverting only the two OnlyOffice guards reproduced the **credential** exploit
  verbatim — an anonymous caller received a signed OnlyOffice config carrying a
  live download URL (`/seafhttp/files/<token>/quarterly.docx`) and its JWT.

That second mutation is the one the earlier `.md`-only coverage could not have
caught, which is exactly why the split fixture exists.

#### Related Docs

- `docs/PROD-SECURITY-READINESS-20260724.md` (NF-1 / SH-6, checklist item 0)
- `ISSUE-SHARELINK-DOWNLOAD-CAP-RACE-01` (same file, adjacent share-link enforcement gap)

---

### ISSUE-SHARELINK-DOWNLOAD-CAP-RACE-01: Download cap and `single_use` are race-bypassable

**Status**: 🟡 Open — accepted-as-best-effort until fixed, but the docs must stop calling it enforced
**Severity**: Medium
**Affected**: `handleShareLinkDownload` in `internal/api/v2/sharelink_view.go`
**Source of record**: NF-2 in `docs/PROD-SECURITY-READINESS-20260724.md`

#### Problem

`max_downloads` and `single_use` are checked against a counter whose update is
fire-and-forget: after the download token is minted, the handler spawns
`go func()` that either deletes the consumed single-use link or increments
`download_count`. The check has already passed by then, so N concurrent requests
all observe the pre-increment count. A cap of 1 admits N downloads and a
single-use link can be consumed repeatedly.

This contradicts the "expiry, caps, scopes — verified good" framing that appears
in the readiness summary and in older share-link documentation. The tokens,
constant-time password compare and scope re-checks are sound; the **counting** is
not.

#### Fix Direction

The cap is a mutual-exclusion problem, not a counting problem. Either make the
consume-and-check a conditional update (LWT on the link row, admitting only the
writer that applied), or accept it explicitly as best-effort and say so in the
UI and the API docs. Do not fix it by making the goroutine synchronous — that
narrows the window without closing it.

#### Related Docs

- `docs/PROD-SECURITY-READINESS-20260724.md` (NF-2, §4 correction)

---

### ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01: Incomplete abuse controls on the seafhttp upload/download/block surfaces and equivalent storage-backed read surfaces

**Status**: ✅ **Closed 2026-08-04** — **A1/A2, B, C and D0-D6 are all closed.** Closing this umbrella did **not** by itself clear the production verdict. `ISSUE-OBJECT-STORAGE-ANONYMOUS-DOWNLOAD-01` let a caller who knows a bucket/key bypass every control described here, and §13 of the D0 contract required that it be tracked separately rather than absorbed; it was closed on its own terms on 2026-08-07. `ISSUE-DOWNLOAD-BYTE-RATE-SHAPING-01` stays open, now with a measured figure instead of a deferral. Historical detail follows. D0 freezes the contract and inventory in `docs/SEAFHTTP-DOWNLOAD-ADMISSION-D0.md`; D1 adds the isolated coordinator, schema and metrics, D2 gives every public download-token mint flow a stable `SourceID`, D3 supplies the idle-write writer plus actual-route gzip/proxy reachability, D4 wires one coordinator through the listed non-block producer lifetimes, and D5 streams authenticated block GET through `CanonicalBlockReader` under `ProfileBlock` without full-block materialization, and D6 now derives clean-deployment capacities from the effective memory budget with a safety margin. Auto mode is the code default; only an explicit `enabled: false` opts out. The 2026-08-04 follow-up corrected the fault-drill proof to count only retryable `profile=block` reasons and to require HTTP 503 with `Retry-After`; it recorded 33 such refusals before real `seaf-cli` recovered. `ISSUE-DOWNLOAD-ADMISSION-PRE-FIRST-WRITE-GAP-01`, which left an admitted request with no D-owned deadline between the end of preparation and its first response write, was fixed in the shared lifecycle on 2026-08-03 and was the last prerequisite for D6. C bounds check-blocks admission on its own capacity, deduplicates lookups, bounds and cancels the metadata fan-out, and closed the gzip hole that would have made its admitted lifetime unenforceable.
**Severity**: High — abuse/DoS control gap on the highest-cost endpoints
**Affected**: `POST /seafhttp/upload-api/:token`, `GET /seafhttp/files/:token/*filepath`, `PUT/GET /seafhttp/repo/:repo_id/block/:block_id`, `POST /seafhttp/repo/:repo_id/check-blocks`, `GET /seafhttp/zip/:token`, `GET /repo/:repo_id/raw/*filepath`, `GET /repo/:repo_id/history/download`, `GET /repo/:repo_id/history/raw`, share-link raw under `/d/:token`, and the share-file bootstrap inline-content read. D's authoritative producer inventory is the D0 contract, not this list
**Source of record**: B4 / SEC-2 / SH-1 in `docs/PROD-SECURITY-READINESS-20260724.md`; **X10** in `docs/UPLOAD-FENCE-FINDINGS-REGISTRY.md` is **subcontract B** of this umbrella (not the whole surface)

#### Problem

This issue is the single record for what was previously filed twice: B4 in the
readiness report and X10 in the upload-fence registry. They **overlap** but are
not identical in scope — B4 covers the full seafhttp abuse surface; X10 focuses
on authenticated block PUT concurrency / aggregate memory after PR-10's
per-request body cap.

Current guard state (2026-08-04; the other rows retain their original verification dates):

| Surface | Limiter |
|---|---|
| `/seafhttp/upload-api/:token` | After a valid token resolves as link-origin, admission to permission/body/storage work has stable-source rate budgets and non-blocking per-source/per-node in-flight caps; token lookup and arbitrary invalid-token traffic are outside these guards |
| `/seafhttp/files/:token/*filepath` | D4 `file` admission through protected preparation and response streaming |
| `/seafhttp/repo/:repo_id/block/:block_id` PUT | per-(org, user) and per-node admitted lifetime before the request body, with bounded waiters and a real connection deadline (B, closed 2026-07-30) |
| `/seafhttp/repo/:repo_id/block/:block_id` GET | D5 `block` admission through size → stream response lifetime; neither the canonical nor the legacy no-metadata path materializes the whole block. The follow-up D6 drill recorded 33 retryable refusals (`admission_timeout`, `auth_user_full`, `node_full`, `profile_full`, `auth_user_queue_full` or `node_queue_full` — the set the drill sums), confirmed HTTP 503 with `Retry-After: 10`, and recovered (2026-08-04) |
| `/seafhttp/repo/:repo_id/check-blocks` | per-(org, user) and per-node admission before the body read, with bounded waiters, an admitted lifetime, and a bounded cancellable metadata fan-out (2026-07-31) |
| `/seafhttp/zip/:token` | D4 `zip` admission through `zipWriter.Close()`; optional request-start `zipRL` remains separate |
| `/api/v2/blocks/check` | per-IP limiter (`rate.Every(time.Second)`, burst 120 ≈ 60/min sustained) |
| `/api/v2/blocks/upload` | per-user concurrency limiter (`MaxConcurrentBlockUploadsPerUser`, default 8) |
| `/api/v2.1/share-links/*`, `/api/v2.1/upload-links/*`, `/d/:token` | per-IP `slRL` |
| `/repo/:repo_id/raw/*filepath`, `/repo/:repo_id/history/*` | D4 `raw` or `history` admission through Range/stream response lifetime |
| `/d/:token` or `/d/:token/files[/]` with `raw=1` | D4 `link_raw` admission plus the existing per-IP `slRL` |
| `/api/v2.1/share-links/:token[/files]/bootstrap` (inline text) | D4 `link_inline` admission through JSON response write; preparation uses the admitted context and the route bypasses gzip |

So the original B4 wording — "no dedicated rate limit on upload / download /
blocks / share-link paths" — is **partly stale**: the share-link and web-block
paths do have some limiters. The accurate residual gap is the unbounded
storage-backed read work, including seafhttp downloads/block GET and the raw,
history, ZIP, share-raw and inline-content paths listed by D0. A request-start
limiter on a public route is not an active-transfer bound.

X10 adds the interaction with PR-10's body caps: bounding one `PutBlock` to
~257 MiB is not an aggregate bound while N concurrent uploads are unbounded, and
the body is still fully buffered by `io.ReadAll` before hashing. Pre-sizing the
read buffer from `Content-Length` is **not** the fix — that value is
attacker-controlled, so an empty body would allocate the full cap.

#### Closable subcontracts

Closing any one row is **not** enough to close this umbrella. Each needs its
own fix + regression.

| ID | Surface | Status | Close when | Notes |
|---|---|---|---|---|
| **A1** | Anonymous upload **post-token attempt rate** (`HandleUpload` / upload-api) | ✅ **Closed 2026-07-29** | — | Initial guard landed 2026-07-28; stable remint identity and the final fail-closed contract completed A1 on 2026-07-29. Per-(IP, stable link identity) and per-link limiters run after valid token resolution; see "Subcontract A" below |
| **A2** | Anonymous upload **in-flight concurrency** | ✅ **Closed 2026-07-29** | — | Non-blocking process-local caps are acquired before permission, multipart/body, staging, or storage work; defaults are `16` per stable source and `128` per node |
| **B** | Authenticated block **PUT** concurrency (= registry **X10**) | ✅ **Closed 2026-07-30** | — | A pre-gate global ticket plus per-user/node admissions bound active, transitioning and parked requests before body reads; real-TCP deadlines cover stalled bodies; complete-lifetime memory trials support 24 slots at an 80 MiB design cost; real `seaf-cli` recovered from the shipped 10s wait and `Retry-After: 10` |
| **C** | `check-blocks` request-rate **and** work amplification (= **X11** companion) | ✅ **Closed 2026-07-31** | — | Own admission instance (separate capacity from B) before the body read, deduplicated lookups, configured fan-out and cancellation coverage for both canonical metadata phases, and an admitted lifetime that now reaches the socket. Cancellation stops new dispatch; already-issued Cassandra queries remain bounded by the driver's finite timeout. Id cap is configurable and capped at its inherited 100k; see "Subcontract C" below |
| **D** | All storage-backed download/inline-content reads, including seafhttp download, block GET, ZIP, raw/history and share raw | ✅ **Closed 2026-08-04** | D1-D5 cover every listed producer with one process-local, bounded and atomic coordinator; D6 measured the per-admission cost (4.0 MiB plaintext, ~68 MiB at the accepted 16 MiB encrypted block, ~184.5 MiB for a capped iWork preview; 72/192 MiB design costs), derives capacities from the effective budget with 20% safety headroom, enables the section and provides client, proxy, saturation and opt-in process-memory evidence | The original row named seafhttp download/block GET. D0 expands closure by flow so raw/history/share/ZIP cannot bypass the same bandwidth/resource exhaustion control |

#### Subcontract A: stable-link request and concurrency admission (A1/A2 closed 2026-07-29)

`HandleUpload` consults `allowUploadLinkWrite` immediately after the token
resolves, before the permission lookup, the body read and any storage work. Over
budget it answers **429 with `Retry-After`**, which is appropriate here because
this is browser traffic — the sync protocol is a different route with a client
that does *not* treat 429 as retryable.

This is deliberately not a total endpoint request-rate guard. `Source` is not
known until token resolution completes, so the token-store lookup occurs first.
Arbitrary invalid-token requests never reach A1/A2 and remain outside these
guards; A1 therefore makes no claim to protect Cassandra token lookups. It stays
closed under the narrower subcontract of bounding admission to the subsequent
permission, body and storage work for valid link-origin tokens.

**The gate keys on `AccessToken.Source == "link"`, not on the route, and that
distinction is the whole design.** `/seafhttp/upload-api/:token` serves *both* the
anonymous upload-link flow and authenticated web uploads. A limiter installed as
route middleware — the obvious implementation — would have throttled ordinary
signed-in users, and a test asserting only "link tokens get 429" would not have
noticed. The regression asserts the negative too: with the bucket exhausted,
`Source` of `""` and `"web"` still pass. Mutation-verified.

**Two buckets, because one address is not one user.**

| Bucket | Key | What only it can see |
|---|---|---|
| per-client | (client IP, stable public-link identity), per node | one uploader going too fast |
| per-link | stable public-link identity, all IPs on one node | one leaked upload URL hit from many addresses or repeatedly reminted |

Keyed on IP alone, one person uploading through one link would throttle every
colleague behind the same NAT using a *different* link — the limiter would cause
the outage it exists to prevent. That isolation is its own regression, also
mutation-verified. Both buckets survive seafhttp token remints because every
newly written short-lived link credential carries a non-empty, stable, non-secret
identity derived from the public link bearer at mint time. Writers reject a
blank `SourceID`, and `HandleUpload` fails closed if one is nevertheless read.
This is a greenfield contract with no legacy-token fallback. The live Cassandra
integration test verifies the migration 013 column, two distinct remints
preserving the exact `SourceID`, and blank writer rejection.

The two A1 buckets form one attempt-rate decision: the stable-source token is
reserved first. If that bucket rejects, the request returns 429 without touching
the per-client map, so traffic for an exhausted leaked link cannot grow retained
state with attacker-controlled IP keys. If the later per-client bucket rejects,
only the accepted source reservation is cancelled. Once both A1 reservations
succeed they remain consumed even if A2 subsequently rejects the request with
429; A1 is not an accepted-heavy-work-only accounting scheme.

**The client had to be fixed for the server bound to be safe at all.**
`@seafile/resumablejs` does not list 429 in `permanentErrors`, so it retries —
but with `chunkRetryInterval` unset it retries *immediately*, and those attempts
count against `maxChunkRetries: 3`. Four attempts inside a few milliseconds
against a bucket that refills a couple of times a second means the file is
reported permanently failed. A limiter meant to slow an upload down would instead
kill it. The pinned library is patched at install time to capture status and
`Retry-After` before it clears the XHR, pass them with the triggering chunk to
`fileRetry`, and make its delayed-retry timer cancelable. The application waits
for `max(Retry-After, capped exponential backoff)`: its exponential interval caps
at 16 seconds, jitter is one-sided when the server supplied the floor, and the
per-chunk throttled retry ceiling is 30. A behavioral test drives 13 consecutive
429 responses and then a success. All three anonymous-capable uploaders are wired
to it (upload-link page, shared-link uploader, main uploader).

Two details there are load-bearing and were not obvious:

- **Retry policy is per chunk, not per uploader instance.** Instance options are
  shared by every concurrent chunk, so one network retry could otherwise erase
  another chunk's throttling policy. The patch also gives 429 its own retry
  ceiling inside `status()`, where the library decides whether to emit the event;
  this keeps a late first 429 observable even after ordinary retries.
- **Delayed retries are cancelable.** The upstream library did not retain its
  timeout handle, so canceling a file during backoff could still send it later.
  The patch stores and clears that timer from the chunk's existing abort path.
- **Jitter is one-sided when the server named a time.** `Retry-After` is a floor,
  not an estimate. Symmetric jitter would sometimes retry before the bucket the
  server just described as empty had refilled. Around our own exponential guess
  there is no such floor, so jitter goes both ways there.

**Attribution and capacity depend on deployment.** The rate buckets and in-flight
counters are process-local node-capacity guards, not cluster-global quotas, so
aggregate fleet admission can approach the configured values multiplied by the
number of nodes. `ClientIP()` is only the real client when
`server.trusted_proxies` names the proxy in front of Go. Left unset behind a
proxy, clients using the same public link are attributed to the proxy IP and
share one per-client bucket; different links remain isolated. The server warns
rather than failing because running with no proxy is legitimate.

Config: `seafhttp.upload_link_writes_per_minute` (600) / `upload_link_write_burst`
(1200), `upload_link_source_writes_per_minute` (12000) /
`upload_link_source_write_burst` (24000), each with a `SEAFHTTP_*` env override;
`0` disables either bucket. The values are deliberately generous starting points,
not measured ones — the failure mode of a rate limit on a data path is a real
person's upload dying, so they should be tightened from
`upload_link_write_throttled_total{reason}` rather than guessed downward.
`Validate()` rejects a live rate with a zero burst (a bucket with no capacity
refuses everything) but does *not* require the burst to exceed the rate: those
are independent dimensions of a token bucket.

Prometheus exposes `upload_link_write_throttled_total{reason}` for A1 rejection,
plus `upload_link_inflight_rejected_total{reason}`,
`upload_link_inflight_current`, and
`upload_link_source_inflight_occupancy`. The latter two report current node
in-flight work and sampled per-source occupancy respectively; the occupancy
histogram deliberately has no source label.

**A2 closes the simultaneous-work gap.** `HandleUpload` acquires a non-blocking
slot after token and rate admission but before permission checks, multipart/body
reads, staging, or storage. The built-in and shipped defaults are `16` concurrent
writes per stable source and `128` across the node; either cap can be disabled
independently with `0`. The source key survives seafhttp token remints, so minting
a new short-lived upload URL does not reset either A1 or A2 on that node. A
rejection is `429` with `Retry-After`, and a deferred idempotent release covers
all admitted exits.

The A2 keys are `seafhttp.upload_link_max_inflight_per_source` and
`seafhttp.upload_link_max_inflight_per_node`, with env overrides
`SEAFHTTP_UPLOAD_LINK_MAX_INFLIGHT_PER_SOURCE` and
`SEAFHTTP_UPLOAD_LINK_MAX_INFLIGHT_PER_NODE`. Validation ceilings are `4096` per
source and `65536` per node; when both caps are enabled, the per-source cap must
not exceed the per-node cap. Like A1, these counters are process-local: adding
nodes increases aggregate fleet capacity. This closes subcontract A only; the
  pre-token lookup and process-local residuals remain. At the time of this A
  closure, B, C and D were still open, so the B4 umbrella remained a production
  blocker.

#### Subcontract B: what the right-sized cap does and does not do (2026-07-28)

The old 257 MiB bound came from the **wrong domain**: it was derived from
`chunking.adaptive.absolute_max` (the web uploader's 256 MiB chunk ceiling),
which never governed this route — and the adaptive chunker has no production
caller at all. The 16 MiB replacement leaves ample headroom over both the current
official client's 4 MiB CDC maximum and SesameFS's related 8 MiB server-side
split (`uploadBlockSize`); those are different producers and are not conflated.

`seafhttp.sync_block_max_bytes` now defaults to **16 MiB**, with a validated
ceiling of 64 MiB and `SEAFHTTP_SYNC_BLOCK_MAX_BYTES`
to override. Unlike `chunked_staging_max_bytes`, **`0` is rejected rather than
meaning unlimited**: an unbounded body on this route is the F12 defect, so no
configuration restores it.

Note what the 8 MiB figure is and is not: it is **the size this code splits at**,
not a production measurement. `sync_put_block_body_bytes` was added precisely
because the previous cap was set from an inherited constant rather than from
observed traffic, and the next adjustment should come from that histogram.

Read both metrics for what they actually observe, which is less than it is
tempting to claim:

- `sync_put_block_body_bytes` measures **request bodies that passed the size
  gate**. It is observed right after the read, before the block id is checked and
  before anything is stored, so it is not a distribution of *successful* uploads
  and not evidence about client behaviour on its own. Correlate it with
  successful block validation before moving the cap.
- `sync_put_block_rejected_total{reason="too_large"}` reports **over-cap
  attempts**. A non-zero value is where the failure mode of lowering a cap would
  first appear, but it does not by itself prove legitimate traffic was rejected —
  an authenticated client sending deliberately oversized bodies moves the same
  counter. It is a prompt to investigate, not a verdict on the cap.

A tighter signal — the histogram observed after hash validation, or a
labelled outcome counter — is worth having before the next adjustment, and is
not in this change.

**This does not close B.** It cuts the per-request ceiling 16×; it does not bound
the aggregate, because N concurrent uploads still cost N × the configured cap.
Closing B needs a cap on total in-flight block readers, acquired **before**
`io.ReadAll` — the point is to bound simultaneous memory, not the number of S3
PUTs.

**Why the aggregate bound was deliberately not attempted alongside it.** The
official client runs up to 5 simultaneous sync tasks × 3 block threads, so **one
legitimate desktop can have ~15 concurrent `PutBlock` requests**. A limiter set
at, say, 8 that rejects immediately would punish a single honest client. It also
classifies 502/503/504 as network errors and retries, but has no special handling
for 429 — so the overload response on this route must be **503 + `Retry-After`**,
never 429, and the design must be *bounded wait, then* 503 rather than immediate
rejection. Those values need to come from a fault-injection run against the real
client, not from a guess.

#### Subcontract B: the aggregate bound (2026-07-30)

The bound the section above says is missing now exists. `PutBlock` acquires an
admission from `syncBlockInflightLimiter` before `readLimitedRequestBody`, and
holds it until the handler returns — through hashing and the storage write, not
just the read, because that whole span is what the buffered body costs.

**Shape of the guard.**

- Two gates: per `(org, user)` first, per node second. Both are held before any
  body is read, so the memory ceiling does not depend on the order; fairness
  does. Acquiring the node slot first would let one identity park every node slot
  while its own requests queue on the per-user gate — capacity consumed without
  memory consumed — which defeats the "one user cannot monopolise the node"
  property. Waiting on your own gate holds no node admission, but does hold one
  bounded global entry ticket.
- One deadline for both gates, derived from the request context, so total wait is
  bounded by `sync_block_admission_wait` rather than twice it, and a client that
  disconnects mid-wait cancels immediately. No timer or goroutine outlives the
  request.
- Per-user and per-node waiter caps (`16` / `128` by default) are reserved before
  parking. A full queue answers 503 immediately without allocating another
  admission timer or adding another gate reference.
- Before any user gate is created, a global entry ticket bounds the complete
  admission mechanism at active-node capacity plus the global waiter budget.
  This closes the distinct-identity transition between `acquireUser` and
  `acquireNode`; a regression launches 1,000 identities and requires both ticket
  occupancy and `perUser` cardinality to remain within the configured bound.
  Exhausting that ring reports its own `entry_queue_full` reason, kept separate
  from `node_queue_full` on purpose: a full waiter queue is a saturated node that
  wants capacity, while a full entry ring means the global admission envelope is
  full before any further per-user state can be created (a brief cardinality
  spike or sustained pressure), and one label for both would leave an operator
  unable to tell them apart. `sync_put_block_entries_current` is the occupancy
  counterpart to read either against.
- Once admitted, one 5-minute processing deadline (ceiling 30 minutes) covers body
  read, hashing and storage. Expiry
  sets the effective request deadline as a **read deadline on the connection**
  without replacing an earlier configured server read timeout, cancels object-storage I/O, stops
  Cassandra phases at callback boundaries, returns 503 + `Retry-After`, and closes
  the HTTP connection. Both slots release when the handler returns; an
  already-running Cassandra query remains bounded by the separately required
  finite DB timeout, so no work path can retain capacity indefinitely.

  The connection deadline is load-bearing and replaced an earlier
  `context.AfterFunc` + `Body.Close()` that could not work: net/http's body `Read`
  and `Close` share one mutex, so a handler parked in `Read` holds it and the
  `Close` queues behind it instead of interrupting it. With `server.read_timeout`
  deliberately `0`, a stalled-but-connected peer would then hold its admission
  indefinitely — a much sharper failure than the memory one this subcontract
  fixes, since a handful of stalled authenticated connections would deny block
  upload to the whole node. The fake-body unit tests could not see this (their
  `Close` unblocks their `Read`), so the closure test drives a **real TCP
  connection** that goes silent mid-body; reverting to the body-close path fails
  it. Separate real-TCP regressions cover the admitted deadline, an earlier
  inherited request deadline, and an earlier `server.read_timeout`.

  **That deadline was itself bypassable, found and fixed 2026-07-31.** Reaching
  the connection depends on `http.NewResponseController` walking the writer chain,
  and that walk follows `Unwrap()`. gin's concrete `*responseWriter` implements
  `Unwrap`, but the `gin.ResponseWriter` **interface** does not declare it — so a
  middleware that embeds the interface exposes neither `SetReadDeadline` nor
  `Unwrap` and terminates the walk. `gin-contrib/gzip` is exactly such a wrapper,
  it is installed globally, it replaces `c.Writer` before `c.Next()`, and it does
  not filter by method. The block route was not in its exclusion list, so an
  authenticated client sending `Accept-Encoding: gzip`, announcing a body, writing
  a few bytes and going silent got an admission whose deadline never reached the
  socket — the exact capacity leak the deadline exists to prevent, reachable with
  one request header. A reproduction differing from the passing regression only by
  that header was never answered at all.

  Two changes close it. The block route is excluded from gzip (blocks are opaque
  binary and a PUT's response is a few bytes, so compression buys nothing there
  anyway), and a failure to install the deadline on a server-handled request is
  now fail-closed and counted rather than silently degraded:
  `sync_put_block_read_deadline_unsupported_total` must stay at zero, and any
  non-zero value is a wiring defect. The refusal **drops the connection** instead
  of answering 503, because a 503 is not deliverable there: net/http drains the
  body the peer never finished sending before the response reaches the socket, and
  with no deadline installed that drain is the unbounded wait being refused. A
  dropped connection is what the sync client already classifies as transient.
  Requests with no connection at all — test recorders, non-net/http writers — keep
  the body-close fallback, which genuinely does end their ordinary readers.

  Regressions cover all three paths: the shipped gzip middleware plus a real
  stalled TCP connection, a writer wrapper that hides `Unwrap` (must drop and
  count), and a synthetic writer (must keep the fallback and not count). Removing
  the gzip exclusion fails the first.

  The last phase is deliberately exempt: once the bytes are stored and the mapping
  registered there is nothing left for a timeout to prevent, so an expiry there
  returns 200 rather than buying a redundant re-upload. Earlier phases still fail
  closed — in particular the reconfirming `store()` of store→materialize→store
  refuses to run on a dead context, so a register that commits as the deadline
  expires still ends 503. That costs one redundant re-upload and keeps the GC-race
  reconfirmation mandatory; the alternative would fail open.
- Overload answers **503 + `Retry-After`**, never 429, after a bounded wait — the
  contract this section already specified. Rejections carry a fixed reason set
  (`user`, `node`, `user_queue_full`, `node_queue_full`, `client_gone`). The first
  four are capacity signals reading different things — the gate reasons mean a
  request waited out its budget, the `queue_full` ones mean it was turned away
  before it could wait at all. `client_gone` is counted separately so ordinary
  client churn does not read as overload.
- Cheap rejections stay ahead of the gate: a missing permission, an unparseable
  block id, or an over-cap declared length is refused without spending or waiting
  for a slot.

**Where the defaults come from.** The corrected opt-in probe uses two identities,
fills the exact node cap with unique 16 MiB bodies, samples one correlated
RSS/heap/cgroup tuple every 50ms from request ramp onward, subtracts a stable idle
baseline, and restarts the node between trials. It holds every completed body at
EOF to establish the plateau, then releases all readers and continues sampling
while `inflight > 0` through downstream drain. The result is the worse sample from
that complete observed window, with retained memory divided by the original
admitted count rather than a lower drain-time gauge.

That stronger probe **invalidated the previous 64 MiB design**: a clean 28-slot
trial needed about 68.6 MiB per admission after the 1.25 factor. The defaults were
therefore revised rather than grandfathered. Three clean-process trials at the new
24-slot cap all peaked post-release; the worst correlated cgroup result was
59.5 MiB raw and 74.4 MiB after the factor. This is rounded conservatively to an
**80 MiB design cost**. Thus 24 slots reserve 1.875 GiB of the explicit 2 GiB
`sync_block_memory_budget_bytes`, leaving 128 MiB route-budget headroom.
`Validate()` keeps that design cost as a floor, scales it upward when
`sync_block_max_bytes` exceeds 16 MiB, and rejects any node-cap/budget combination
that exceeds it. The probe itself fails if the measured full-lifetime cost exceeds
the design or if the calculated safe cap falls below the configured cap.

`sync_block_max_inflight_per_user` (16) is a fairness split, not a memory one: it
sits just above the ~15 concurrent `PutBlock` requests one official client can
issue, so a single honest desktop is never queued by its own budget, while two
identities can fill the 24-slot node. All of a user's devices share it.

The integration fixture uses 250ms only to keep CI fast. It holds request bodies
open, observes `inflight == 2`, and deterministically proves both the same-user
gate and the cross-user node gate, independent of MinIO throughput. The same
fault harness was also run against the shipped 10s wait: real `seaf-cli` received
two 503 responses with `Retry-After: 10`, retried, published all payloads byte for
byte, reached stable synchronized state, and drained every slot.

**Original closure criteria — seven of seven met.**

| # | Criterion | Status |
|---|---|---|
| 1 | No body is read without a slot | ✅ unit + mutation-verified: moving the acquisition after the read fails 7 tests |
| 2 | An aggregate per-node bound exists | ✅ deterministic held-body integration proves the node gate across two identities |
| 3 | One user cannot monopolise the node | ✅ per-user gate acquired first; isolation test |
| 4 | No unbounded wait, no leaked slots | ✅ bounded waiter queues, shared wait deadline, admitted processing deadline with a ceiling, **connection read deadline** proven on a real stalled TCP connection, object-storage cancellation, finite Cassandra query timeout, race/leak/drain tests |
| 5 | Peak RAM quantified | ✅ three clean-process, baseline-subtracted, correlated RSS/cgroup trials from request ramp through post-release drain at the actual 24-slot cap |
| 6 | The real client recovers under saturation | ✅ deterministic disposable-state harness, including the shipped `10s → 503 → Retry-After: 10` cycle, publication integrity and drain |
| 7 | Defaults from measurement, not guesswork | ✅ old 64 MiB design rejected by full-lifetime evidence; revised 80 MiB cost includes 1.25 factor and explicit 2 GiB validation |

**Criterion 6 closure.** `scripts/fault-inject-block-admission.sh` now starts
from disposable client state, initializes `seaf-cli` normally, synchronizes an
empty watched worktree, and then occupies both node-3 admissions with controlled
slow PUTs. An independent sentinel pins 503 + positive `Retry-After`; only then
does the script create payloads and use the rejection counter to prove the real
client reached the saturated route. After releasing the fault it requires a new
admission, stable `synchronized`, byte-for-byte API downloads of every payload,
and a zero in-flight gauge. It passed twice consecutively in fast mode from cold
containers (4 and 3 client rejections), then passed with the shipped 10-second
wait (2 client rejections and `Retry-After: 10`). The client reports
`error Network error` transiently after the injected 503; that is its expected
retryable classification, not a terminal test failure.

**Post-implementation audit resolved.** A pre-gate ticket bounds distinct-user
transition cardinality; waiter state remains bounded per user and per node;
admitted work has a real connection body deadline plus bounded downstream phases;
the process test distinguishes both gates; and the memory probe measures correlated
baseline-subtracted RSS, heap and cgroup across the complete observed admitted window at
all 24 admissions. These were closure blockers, not deferred hardening.

**Unrelated defect found while measuring, fixed 2026-07-30.** `/metrics` was
served double-gzipped when the client negotiated compression. The configured
metrics path is now excluded from the engine gzip middleware, leaving
compression to `promhttp`; a regression test decompresses exactly once and
requires Prometheus text rather than a second gzip stream.

#### Subcontract C: bounding accepted check-blocks work (2026-07-31)

`maxCheckBlockIDs` bounded the **parse**, and that is all it ever bounded. An
accepted list still drove one Cassandra point read per legacy SHA-1 id — the
shape the desktop client actually sends — **sequentially**, through a
`db.GetBlockIDMapping` that took no `context` at all. So a client that
disconnected mid-request paid nothing and the server ran the remaining reads to
completion for nobody. There was no deduplication either: one id repeated to the
cap cost the same as a list of distinct ids while being far cheaper to send. And
nothing bounded how many such requests ran at once.

**Four bounds, because the cap was only ever the first one.**

- **Admission before the body read.** `CheckBlocks` acquires from its own
  `syncAdmissionLimiter` instance before `readLimitedAdmittedRequestBody`, so an
  over-capacity node refuses at the cost of one parked goroutine rather than
  buffering 16 MiB and then resolving the list. Cheap rejections — a missing
  permission — stay ahead of the gate.
- **A separate instance, deliberately.** The limiter *mechanism* is now shared
  with subcontract B (gates, waiter accounting, entry ring, single deadline); the
  *capacity* is not. The two routes exhaust different resources — buffered body
  memory there, Cassandra and object-store metadata work here — so one storming
  must not be able to spend the other's admissions. The check-blocks parser and
  per-request maps also consume memory proportional to the accepted id cap; that
  memory is bounded by admission and `check_blocks_max_ids`, but has not been
  assigned a B-style measured byte budget. Two regressions assert both directions;
  pointing the check-blocks handler at `blockInflight` fails them.
- **Deduplication.** Ids are deduplicated before any lookup, so cost tracks
  unique ids. `sync_check_blocks_lookups_total` against
  `sync_check_blocks_ids_per_request` is what makes that visible;
  before, the two lists above were indistinguishable in both cost and telemetry.
- **A bounded, cancellable fan-out.** `check_blocks_lookup_fanout` (8) bounds both
  metadata phases — the new ctx-aware `GetBlockIDMappingContext` resolution and
  the canonical existence check, which previously ran at a hardcoded 32 and 10
  regardless of configuration. Every worker checks the group context before
  issuing a read, so a disconnect or an expired lifetime stops dispatching new
  lookups and cancels context-aware reads already in flight. The Cassandra
  driver's finite query timeout remains the bound for a query already issued.

**The node budget is the product, and validation enforces it as such:**

```
concurrent metadata lookups from this route
    <= check_blocks_max_inflight_per_node x check_blocks_lookup_fanout
     = 8 x 8 = 64          (ceiling 256)
```

Either factor alone says nothing about that quantity, which is how a "harmless"
fan-out bump would quietly multiply what one node puts on Cassandra.

**The id cap is now configuration, and was deliberately not lowered.** 100000
remains the default *and* the validation ceiling: `check_blocks_max_ids` can only
be lowered. That number is inherited client compatibility, never a measured one —
a large initial sync posts the block list of one commit, and the desktop client
does not re-batch after a 413, so lowering it on a guess trades a bounded
amplification for an unbounded risk of breaking a legitimate sync.
`sync_check_blocks_ids_per_request` is the instrument that will justify a lower
value. The fault drill measures it in a client-only phase using a before/after
delta; its slow holders are excluded from that measurement. It reports the
observed distribution, including requests above 256 ids, rather than treating a
small-worktree sample as a client contract.

**The gzip trap from subcontract B was still live on this route.** The admitted
lifetime is what makes an admission recoverable and what stops the metadata work
when a client goes away, and it only reaches the socket while nothing in the
writer chain hides `Unwrap()`. B excluded the block route from `gin-contrib/gzip`
and left check-blocks inside it, which was harmless while check-blocks had no
lifetime — and became a **total outage** the moment it got one: with the deadline
uninstallable, the fail-closed path dropped the connection on *every* request. A
plain-router unit test cannot see this; the integration suite against the real
stack caught it on its first run, before merge. The route is now excluded, and a
real-TCP regression over the shipped middleware fails if the exclusion is removed.

**Closure criteria.**

| # | Criterion | Status |
|---|---|---|
| 1 | Rate/concurrency on the route | ✅ per-user and per-node admission with bounded waiters and an entry ring; 503 + `Retry-After`, never 429 |
| 2 | No body is read without a slot | ✅ unit regression asserts a refused request never touches its body |
| 3 | Per-accepted-request work is bounded | ✅ dedup + configured fan-out on both phases; mutation-verified (removing either fails) |
| 4 | Cancellation bounds Cassandra work | ✅ ctx-aware mapping read; dispatch stops on cancellation, no new lookups are scheduled, and already-issued queries remain bounded by the driver timeout |
| 5 | An admission is always recoverable | ✅ admitted lifetime with a connection read deadline, plus the gzip exclusion that lets it reach the socket |
| 6 | One route cannot spend the other's budget | ✅ separate instances, asserted in both directions at unit and integration level |
| 7 | The real client recovers under saturation | ✅ `scripts/fault-inject-check-blocks-admission.sh`: saturate, prove refusal from server counters *and* the client log, release, require stable `synchronized`, byte-for-byte payload verification and a zero in-flight gauge |

**Criterion 7 closure.** `scripts/fault-inject-check-blocks-admission.sh` creates
a disposable organization, owner, and API key for every run, then starts from
disposable client state and synchronizes an empty watched worktree. Its cleanup
deletes the library and organization; interrupted leftovers are swept by their
organization prefix, so drill traffic never consumes the shared dev quota. It
then holds both node-3 admissions with looping rate-limited bodies. (The holder
body has to be large: curl rate-limits per transfer buffer, so a payload that
fits in one buffer is written in a single go and occupies nothing — the first
attempt failed for exactly that reason.) An independent sentinel pins 503 + positive
`Retry-After` before the client baseline is taken, so the drill cannot mistake
its own request for the desktop's. It then requires the server's rejection
counter **and** the client's own log to show the refusal, and only afterwards
releases the fault and demands stable `synchronized`, byte-for-byte API downloads
of every payload, and a zero in-flight gauge. It passed at the squeezed 250ms
wait and again at the shipped 10-second wait; `seaf-cli` reported the injected
503 as `error Network error`, which is its retryable classification, and finished
the sync.

**Real-client cardinality evidence.** The drill first synchronizes a clean
worktree without holders, snapshots the histogram, and then reports only the
before/after delta. The later 20k-id holders cannot contaminate that result. This
is still a small-library sample. The histogram is observed after parsing and
before ID classification, so it includes malformed traffic that reached the
parser; it is not by itself a distribution of legitimate client requests and is
**not** grounds to lower the 100k cap. The compatibility ceiling remains until
the opt-in large-cardinality probe below is considered alongside production
traffic.

For the lifetime boundary, run the real Cassandra probe in Docker:

```bash
docker compose --profile test run --rm --build \
  -e CHECK_BLOCKS_LARGE_PROBE=1 go-integration-test \
  go test -tags integration -run '^TestCheckBlocksLargeCardinalityLifetime$' \
  -v -count=1 -timeout 12m ./internal/integration
```

That probe sends 100000 unique legacy ids and 100000 unique canonical ids to
the real node-3 route, exercising the mapping phase separately and then the
canonical location plus real object-store existence phases. On the merge
candidate it completed in **57.62s** for legacy mappings and **2m10.30s** for
canonical location/existence, well below the shipped 5-minute lifetime. The
canonical metadata rows are temporary and cleaned in bounded batches. These
results are evidence for the shipped lifetime, not evidence that 100k-id
existence requests are cheap or that the cap should be lowered.

The fault drill's disposable owner API key is a test-only Cassandra fixture.
The public self-service key endpoint requires an already authenticated owner,
while administrative key issuance is intentionally limited to platform users.
The fixture therefore mirrors `apikeys.Manager.CreateKey` by writing both
`api_keys` and `api_keys_by_user`, then exchanges the raw key through the real
`/api2/auth-token/` endpoint. If the production key schema changes, this fixture
must be updated with it.

The lifetime probe bounds request work and admitted concurrency, but does not
measure process RSS or cgroup memory under eight concurrent 100k-id requests.
That remains follow-up evidence for choosing the node cap; it does not reopen
the C closure criteria.

**What this does not claim.** The accepted cardinality is still a compatibility
bound rather than a measured one — criterion 3 bounds the *work per id* and the
*concurrent requests*, not the list length. Process-local, like every other guard
here: fleet capacity scales with node count. Closing C did not close the
umbrella; at this D0-D5 snapshot **D6 (download admission operating evidence)
remained open**, so B4 remained a production blocker until the closure recorded
in the current rows above.

#### Subcontract D: download admission contract and inventory (D0-D5, 2026-08-03)

D0 was documentation only and froze the scope and criteria for the final open
subcontract in [`docs/SEAFHTTP-DOWNLOAD-ADMISSION-D0.md`](./SEAFHTTP-DOWNLOAD-ADMISSION-D0.md).
D1 now provides the isolated coordinator, configuration, fixed-label metrics and
bounded-state tests on the implementation branch. D2 now wires the stable
public-link `SourceID` through normal download, public OnlyOffice and public ZIP
token minting, and rejects blank link identities in token writers and download
consumers. D3 now supplies the reusable idle-write writer, fail-closed writer
reachability checks, actual-route gzip exclusions and frontend proxy buffering
for the public bootstrap routes. D4 now bootstraps one coordinator and wires file,
ZIP, authenticated raw/history, public share raw and inline text/Markdown through
admission, preparation and response cleanup. D5 streams `SyncHandler.GetBlock`
through `GetBlockSize`/`GetBlockReader` under `ProfileBlock` on that coordinator,
preserves `last_accessed` after quota and before the body, and records traffic from
bytes successfully written. The route's writer reachability is regressed over a
real socket in both directions — the shipped gzip stack must leave the block GET
writer unwrapped, and a middleware that hides the connection must fail closed
rather than stream without an installable idle-write deadline — because that is
the same defect subcontract C shipped on `check-blocks`. The quota pre-check and
the `last_accessed` write both run on the preparation context, so neither holds a
slot past the preparation deadline when Cassandra stalls, and a reader that
reaches EOF early is released as `storage_error` rather than `completed`. D6 owns
measured capacities plus the real-nginx slow-client drill.

D5 classifies this route as authenticated traffic because the sync surface has no
legitimate public flow. That is a **dependency** on `syncAuthMiddleware`, not a
property D5 proves. While `ISSUE-SYNC-LINK-TOKEN-AUTH-01` was open, a public
share-link bearer reaching this route was admitted as the link creator, so
criterion 4 fairness was not end-to-end proven for block GET. That issue was
fixed on 2026-08-07 and the dependency is now satisfied: only a repository sync
token bound to the route's library reaches this surface. The dependency itself
remains — D5 still does not prove it, the middleware does. Streaming and admission do **not** close
`ISSUE-BLOCK-CROSS-LIBRARY-READ-01`.

The original D row named the seafhttp file download and authenticated block GET.
The closure scope is intentionally defined by **storage-backed byte production**
so an attacker cannot evade the shared node ceiling by switching to an equivalent
route. It includes:

- `HandleDownload` / `streamFileFromBlocks` (`/seafhttp/files`);
- `HandleZipDownload` (`/seafhttp/zip`);
- `SyncHandler.GetBlock` (block GET);
- `ServeRawFile`, `DownloadHistoricFile` and `ServeHistoricFileRaw`;
- `handleShareLinkRaw` for `raw=1`;
- `readFileContentAsText`, the public inline text/Markdown storage read.

Within `raw`, the `preview=1` iWork branch of `ServeRawFile` is the worst memory
case in D: it buffers the entire source file into one `bytes.Buffer` before
parsing it as a ZIP. The source file **is** gated by the existing
`getMaxFileSizeForPreview` check, which returns `413` before the block store is
resolved and before buffering — but an iWork file is neither video nor text, so
that gate is the general `FileView.MaxPreviewBytes`, 1 GiB in every shipped
config and twenty times the 50 MB `MaxIWorkPreviewBytes` that caps only the
extracted preview. The path is bounded, by a general preview limit rather than
an in-memory budget validated for a fully buffered producer. D4 proves the
existing gate still precedes admission and buffering; D6 must measure the real
per-request peak (buffer capacity, in-flight encrypted and decrypted block,
extracted preview, ZIP overhead) so `max_active_raw` and `MaxPreviewBytes` can
be set against a stated memory budget. iWork stays inside the `raw` profile:
the D0 profile enum, configuration keys, environment variables and metric
labels are closed sets, so giving the buffered branch its own cap would be a
contract amendment, not a D4 implementation choice.

Redirects, bootstrap JSON without inline content, OnlyOffice configuration and
the share-link `dl=1` mint step do not consume a long-lived slot. OnlyOffice's
final `/seafhttp/files` request does. There is no implicit HEAD contract; Range
and multi-range operations remain inside the admission for their complete
response lifetime.

The D coordinator must grant all applicable dimensions atomically or none:

```text
authenticated: node + authenticated-user
public link:   node + stable-link-source + client-link
```

Public link traffic must never consume the admission budget of the link owner's
authenticated user. `AccessToken.UserID` remains the authorization/decryption
principal; `SourceID` is the remint-resistant admission identity. Migration 013
already provides `access_tokens.source_id`; D2 adds the source-ID parameter to
`CreateLinkDownloadToken` and wires normal download, public OnlyOffice and
public ZIP minting. New link tokens fail closed without a non-blank source ID;
the clean deployment requires no legacy fallback or backfill.

The node ceiling is shared across profiles, while block, file, raw, history,
link-raw, ZIP and inline-text capacities are measured separately. D does not
reuse B/C's short-work defaults or move B/C's white-box limiter implementation.
Long downloads use a short or disabled queue plus a sliding idle-write deadline,
not a short absolute transfer lifetime. The coordinator has bounded entries and
waiters, removes idle identities, creates no per-identity goroutines/timers and
must drain to zero after sustained churn. A refusal is `503` with `Retry-After`
on every profile, not only the desktop block route.

The D0 contract also freezes the configuration keys and metric series so D1 does
not improvise them. Configuration is a `download_admission` section with an
`enabled` flag, flat per-profile caps rather than a map so environment overrides
are possible, and a per-key zero policy: zero is a legitimate "no queue" for the
wait and waiter keys, but the node ceiling, the three identity caps, the
preparation and idle-write deadlines and `retry_after` must be positive when
enabled, or startup fails. `retry_after` is its own key rather than derived from
the wait as B and C derive it, because a download slot does not free on the
timescale of the queue.

The `download_admission_*` series carry the occupancy invariant
`active_current == sum(active_by_profile)`, deliberately not a sum over identity
dimensions — a public transfer occupies `link_source` and `client_link` at once,
so that sum double-counts every public byte. Waiters need the same treatment:
an unlabelled gauge counts parked requests, a second one shows which gate they
are blocked on. The invariant is over coordinator state and stable snapshots,
not over every concurrent scrape. No label carries a bearer, IP, user, org, repo
or source identity, and every label value set is enumerated in the contract.

The current block gzip exclusion already covers both block methods through
`/seafhttp/repo/.*/block/.*`. D3 replaces the stale API raw/history patterns
with actual `/repo/...` route exclusions, adds a blanket `/d/...` exclusion and
excludes both inline bootstrap routes. The supported frontend nginx topology
also disables proxy buffering for those bootstrap routes, while egress cost
remains a D6 measurement. The D3 writer tests prove that the connection deadline
is reachable and that an unsupported writer fails closed.

The compressible inline-text bootstrap is on **different** routes —
`/api/v2.1/share-links/:token/bootstrap` and `.../files/bootstrap` — and D3 now
excludes them from Go gzip and proxy buffering. The deadline writer is installed
by D4 immediately before the inline response is produced; it is installed
before each underlying write/flush, cleared after the stream and fail-closed
before headers if the connection is unreachable.

`GetBlockReader` and `GetBlockSize` already exist. D5 wires them into
`SyncHandler.GetBlock` so the block is streamed opaquely with authoritative size,
context cancellation and preserved response states. D5 preserves the current
`last_accessed` placement: `GetBlock` writes it after the quota pre-check and
before the response body is sent. Redefining it as post-complete-delivery is
outside D5 and requires its own issue and evidence. No current reader uses
`blocks.last_accessed` for retention or deletion, but any future GC/cold-storage
consumer must revisit that contract. D5 is complete: the route acquires
`ProfileBlock` on the shared coordinator, uses size for quota/`Content-Length`,
streams via `GetBlockReader`, and records traffic from bytes successfully
written rather than nominal size.

`GetBlockSize` may be used for the preflight quota decision and
`Content-Length`, but it must not become the recorded transfer amount. The
previous buffered implementation accounted `len(data)`, which is exact for the
completed block. After streaming, D5 counts bytes successfully written and
passes that actual count to `traffic.RecordCheckedTransfer`; a partial transfer
must not regress to nominal-size overbilling. The broader `StreamBlocks` false
success/over-counting issue remains separately tracked as
`ISSUE-STREAMBLOCKS-VOID-01`, which was fixed on 2026-08-03.

D0-D6 is the implementation order: contract/inventory, coordinator, public-link
identity, writer lifetime/gzip, existing stream integration, block GET streaming,
then evidence and closure documentation. The object-storage Compose policy
`mc anonymous set download` is separately tracked as
`ISSUE-OBJECT-STORAGE-ANONYMOUS-DOWNLOAD-01`; D must not silently claim that
direct bucket access is protected by application admission.

#### Fix Direction

Implement D through the D0-D6 sequence. The sequence is now complete with
focused, real-middleware and integration evidence. The old 257 MiB `PutBlock`
cap note is historical: B right-sized and bounded that path; it is not remaining
D work. Direct object storage and byte-rate shaping remain separate findings.

#### Related Docs

- `docs/UPLOAD-FENCE-FINDINGS-REGISTRY.md` (X10 = subcontract B; X11 = related to C)
- `docs/PROD-SECURITY-READINESS-20260724.md` (B4, SEC-2, SH-1, checklist item 1)
- `docs/SEAFHTTP-DOWNLOAD-ADMISSION-D0.md` (D0 contract, inventory and PR sequence)
- `docs/SECURITY-ASSESSMENT-2026-04.md` **H-7** — the original 2026-04 filing. (The readiness report cited H-5 for this; H-5 is share-link token enumeration. H-7 is the rate-limit finding.)
- `ISSUE-CHECKBLOCKS-WORK-AMPLIFICATION-01` (subcontract C detail)
- `ISSUE-DOWNLOAD-ADMISSION-PRE-FIRST-WRITE-GAP-01` (shared D3/D4/D5 phase-change gap; was the last prerequisite for D6, fixed 2026-08-03)

---

### ISSUE-DOWNLOAD-ADMISSION-PRE-FIRST-WRITE-GAP-01: Download admission has no deadline between preparation and the first response write

**Status**: ✅ **Fixed 2026-08-03**. Was never a regression from any single
subcontract: the gap was in the shared D3 phase change and was reachable through
both the D4 and D5 producers, so the fix landed in the shared lifecycle rather
than in either handler. No longer blocks D6.
**Severity**: High once `download_admission.enabled` is true; latent while it is
false, which it is in every shipped configuration.
**Affected**: `httputil.DownloadAdmission.StartStreaming` /
`httputil.IdleWriteWriter`, reached by `SeafHTTPHandler.streamFileFromBlocks`
(D4, `file`/`link_raw`/`zip`) and `SyncHandler.GetBlock` (D5, `block`), and by
any future producer using the same lifecycle.
**Source of record**: found reviewing PR #158 (D5)

Each admitted request passes through three phases. Two are bounded and one is
not:

| Phase | Bound |
|---|---|
| Preparation — reader resolution, size, quota, `last_accessed` | `preparation_deadline` |
| **First storage read — `GetBlockReader` / first prefetch** | **none** |
| Response writes | `idle_write_timeout` |

`StartStreaming` cancels the preparation context and returns
`context.WithCancel(parent)` — a cancelable context with **no deadline**. The
idle-write timer is armed only by `IdleWriteWriter.progress()`, which runs after
a successful `Write`/`Flush`, or after `WriteHeader`/`WriteHeaderNow` **if the
underlying writer became `Written()`**. Gin defers its status, so `c.Status(200)`
records the code and leaves `Written()` false; `IdleWriteWriter.WriteHeader` then
takes the `clearDeadlineWithoutProgress()` branch, which explicitly clears the
socket deadline and arms nothing.

So between the phase change and the first byte, a stalled first storage read
holds its admission slot until the client disconnects or the object-store SDK's
own timeout fires. Neither is the configured D bound, and that bound is the whole
point of the subcontract: an accepted request whose expensive work is unbounded
is the condition D exists to prevent.

Both producers are reachable:

- **D5** — `openReader(streamCtx)` in `SyncHandler.GetBlock`;
- **D4** — `StreamBlocks` starts `PrefetchBlock` for block 0 before emitting any
  byte, which the streaming code already documents as a blocking S3 request.

Moving `c.Status(200)` ahead of the reader open does **not** fix it. D4 already
has exactly that ordering and still has the gap, because the status is deferred.
Committing headers with `WriteHeaderNow()` would arm the timer, but it forfeits
the ability to answer `404`/`500` on a post-preflight race and would have to be
repeated in every producer.

**The fix** landed in the shared lifecycle, as **two** rules rather than one.
Arming the interval at the phase change is necessary but **not sufficient**:

1. `IdleWriteWriter.StartIdleInterval()` opens the interval before any output
   exists, and `DownloadAdmission.StartStreaming` is its only caller. It is
   separate from `NewIdleWriteWriter`, which keeps owning the reachability probe,
   and arming twice is a no-op rather than a second timer. Arming happens after
   the writer is installed but **before** the preparation deadline is retired, so
   no externally reachable state has both deadlines off, and it runs outside the
   lifecycle mutex because the timer callback claims the terminal cause. If it
   fails, the transition rolls back completely: original writer restored, partial
   deadline cleared, `streamCtx` cancelled, a non-`completed` cause claimed, and
   an error returned before the producer touches storage.
2. A **deferred header preserves the interval**. `WriteHeader`/`WriteHeaderNow`
   previously took `clearDeadlineWithoutProgress()` when Gin recorded a status
   without committing, which would have erased the new interval a moment after it
   was armed — `c.Status(200)` sits exactly there in both producers. It now
   restores the original absolute deadline instead: a deferred header is not
   progress, so it must not extend the interval, but it must not delete it
   either. Before arming, the pre-existing "no timer at all" behaviour is kept.

Real progress still replaces the interval from the progress instant, so the total
span since the phase change can approach `2 × idle_write_timeout` when the first
byte arrives just before the initial interval ends. That is correct — there was
progress. What must not happen, and is regressed, is the initial callback
surviving, two intervals stacking, or a deferred status resetting the clock.

Release ordering is unchanged and now pinned: the timeout claims the cause and
cancels `streamCtx`, but the lease is released only when the producer's deferred
`Finish` runs. Freeing capacity from the timeout callback while the cancelled
read is still executing would let the coordinator admit past its real ceiling.
Within `Finish`, the failure response is committed before the slot is handed
back — the response lifetime is not over while the status is uncommitted — and
the release itself is deferred rather than a plain statement afterwards, for the
same reason the ZIP producer registers its release before `zipWriter.Close`: a
response writer that panics while committing would otherwise strand the slot for
the life of the process.

Bounding the phase exposed a terminal state that did not exist while it hung, and
closing it is part of this fix. A failed writer rejects **every** later write,
including the producer's own pre-header error, so `c.JSON(500)` on the D5 path
and `respondSeafHTTPDownloadError` on the D4 path both wrote nothing and Gin
committed its default `200` through the underlying writer — bypassing the
wrapper. The client saw a timed-out download as an **empty file that transferred
successfully**, and on block GET that is indistinguishable from a legitimately
empty block, turning a retryable timeout into silent corruption. `Finish` now
answers `503` with `Retry-After` when the transfer ends on a non-`completed`
cause having committed no output. `503` rather than `500` because this is
transient unavailability under the same retryable contract B and C proved against
real `seaf-cli`; `completed`, `client_disconnect` and `panic` are excluded, the
last so Gin's recovery keeps owning its `500`. Nothing is written once any output
exists, so a failure after headers still stops the stream instead of appending to
it.

Swapping the status alone is not enough, because producers stage the file's
representation headers **before** their first storage read: D4 sets
`Content-Disposition`, `Content-Type` and the file's full `Content-Length` ahead
of the block-0 prefetch. A `503` inheriting a `Content-Length` of the whole file
declares a body that never arrives, so `net/http` closes the connection and the
client reads an unexpected EOF instead of the `Retry-After` contract the response
exists to deliver — the retry path would be broken by the very fix meant to
enable it. A stale `Content-Disposition` would also make a browser save the error
as the file, and a stale `ETag`/`Cache-Control` could let it be cached as the
resource. `Finish` therefore drops the entity headers
(`Content-Disposition`, `Content-Encoding`, `Content-Range`, `Content-Type`,
`Accept-Ranges`, `ETag`, `Last-Modified`, `Expires`), sets `Cache-Control:
no-store` and `Content-Length: 0`. The reset is deliberately a named list rather
than a blanket clear: CORS, security and quota-warning headers belong to the
stack rather than the entity, and wiping them from every timed-out transfer would
be a silent second defect.

One classification race is closed with it. `expire()` commits the writer to
failed under the writer's own mutex, releases it, and only then calls back to
claim the terminal cause on the lifecycle. A handler finishing inside that
handoff could claim `completed` first and record a killed transfer as a clean
one. `finishCause` now consults the writer's terminal error before accepting
`completed`. The lock order is safe in one direction only, and it holds: no
writer path holds its own mutex while calling into the lifecycle — `expire`,
`fail`, `beforeWrite`, `progress` and `Finish` all release it before notifying —
so `l.mu` then `w.mu` never inverts.

Coverage: writer-level regressions for arming without output, idempotent arming,
deferred-header preservation before and after arming, first progress replacing
the initial interval, `Finish` clearing both timer and deadline, and expiry with
no output at all; producer-level regressions for a blocked `GetBlockReader` (D5)
and a blocked first prefetch (D4), each proving cancellation in bounded time, the
lease still held while the cancelled work runs, exactly one `idle_write_timeout`
release afterwards, nothing counted as `completed`, and `active_current == 0`;
plus fast-failure cases proving a reader open that fails quickly still answers
`404`/`500`, since arming must not commit headers; both producer regressions also
assert the `503` and its `Retry-After`, and lifecycle tests cover the committed-
response case being left alone and `completed` losing to a failed writer.
Removing any of the three behaviours fails its regression, mutation-verified —
and the D4 producer test fails on rule 2 alone, which is the evidence that arming
by itself would not have closed the gap.

**Why this was not a merge blocker for D5**: D5 reproduced D4's shape rather than
introducing a new gap, `download_admission.enabled` is `false` in every shipped
configuration, and a D5-local workaround would have duplicated logic while
leaving D4 exposed.

---

### ISSUE-OBJECT-STORAGE-ANONYMOUS-DOWNLOAD-01: Anonymous bucket policy in the development Compose stacks

**Status**: ✅ **Closed 2026-08-07 — never affected production; dev-only
configuration, now removed**
**Severity**: Downgraded from blocker. It was scoped to the development and
test Compose stacks, which is not a production posture at all.
**Affected**: Development/test Compose storage initialization only
**Source of record**: D0 contract in
`docs/SEAFHTTP-DOWNLOAD-ADMISSION-D0.md`; production posture follow-up to
`ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01`

#### What the finding actually was

Four Compose files ran `mc anonymous set download` on their MinIO buckets:
`docker-compose.yaml`, `docker-compose.mr.yaml`, `docker-compose.mr-cluster.yaml`
and `docker-compose-multiregion.yaml`. The policy was real — measured against
the development stack on 2026-08-07, `GET /sesamefs-blocks/?list-type=2`
returned HTTP 200 with full `blocks/<org_id>/<xx>/<yy>/<sha256>` keys and a
subsequent object GET returned block content in the clear, because MinIO's
`download` policy grants `s3:ListBucket` alongside `s3:GetObject`. Writes were
refused (`PUT`/`DELETE` → 403).

**None of those four files is a production deployment path.** All four are
development and test stacks; the `mr` and `mr-cluster` ones exist to exercise
cross-region replication behind a Playwright suite. Production deploys from
`docker-compose.prod.yml`, which has no MinIO service and no `mc` init
container — `DEPLOY.md` describes it as "no MinIO, no dev tools" — against
provider-native S3 configured in `configs/config.prod.yaml`, where AWS and
Cloudflare R2 buckets are private by default.

So the original registry entry overstated the finding by describing "supported
Compose storage policies" without distinguishing the development files from the
production one. There was never a production exposure to fix, and no deployed
data was ever anonymously readable.

#### Resolution

The `mc anonymous set download` invocations were removed from all four Compose
files. Nothing depended on them: every MinIO consumer in the repository
authenticates. The Go integration suites build `S3Config` with explicit
credentials, the multi-region Playwright spec verifies replication by
downloading through the application API as an authenticated user rather than
from the bucket, and the only unauthenticated request to port 9000 anywhere is
`scripts/test.sh` probing `/minio/health/live`, a MinIO server endpoint
independent of bucket policy. The commands dated to the initial scaffolding
commit `896c647df` and had simply been copied forward into each new Compose
file since.

`DEPLOY.md` step 0.1 now states the obvious requirement explicitly — keep the
bucket private, SesameFS reads with its own credentials and never needs
anonymous access — with a one-line `curl` for anyone who wants to confirm it.

Verified on the development stack: with all buckets switched to private, the
S3-touching integration tests (`TestGC_*`, `TestWebBlockUpload*`,
`TestNeedsPutUsesCanonicalMinIOBucket`, `TestProvisionalRollback*`,
`TestNoSessionBlockEndpointsRejected`) pass in 122.9s, matching the green
baseline recorded before the policy was changed. Re-running the edited
`minio-init` leaves every bucket private.

No legacy object migration or data backfill was needed. This issue does not
gate the readiness verdict and never should have.

#### Related Docs

- `docs/SEAFHTTP-DOWNLOAD-ADMISSION-D0.md` (D boundary and separate blocker)
- `docs/DEPLOY.md` step 0.1 (bucket creation and verification)
- `docs/OPEN-WORK-INDEX.md` (production blocker row)
- `docs/PROD-SECURITY-READINESS-20260724.md` (dated posture note)

---

### ISSUE-SSO-PENDING-TOKEN-NODE-LOCAL-01: Desktop-client SSO pending-token store is in-memory

**Status**: 🔴 Open — multi-instance blocker (B5)
**Severity**: High for any multi-instance rollout; **no single-node impact**
**Affected**: `clientSSOStore` in `internal/api/server.go`; the desktop SSO poll and OIDC callback routes
**Source of record**: B5 in `docs/PROD-SECURITY-READINESS-20260724.md`

#### Problem

`clientSSOStore` is a per-process `map[string]*clientSSOEntry` behind a mutex.
The OIDC callback writes the successful `apiToken` into that map on whichever
instance served the callback; the desktop client polls for the token on
whichever instance the load balancer picks. When those differ, the poll returns
`status: waiting` forever and the client never receives its token.

Live-confirmed 2026-07-24 on the two-instance multiregion stack: a full OIDC
flow (mock IdP, RS256 signature + nonce + issuer all validated) completed its
callback on `usa`; polling `usa` returned `status:success` with the token, and
polling `eu` returned `status:waiting`. "SSO works in the desktop client" only
exercises the same-instance happy path.

Distinct from `ISSUE-SSO-01`, which was about the browser confirmation page UX
and is fixed. This one is about where the pending token lives.

#### Fix Direction

Move the pending-token store to Cassandra with the same TTL the in-memory
cleanup loop applies, mirroring how upload tokens were already made multi-node
safe. Sticky routing at the LB is a workaround for B1 that also happens to hide
this, but it is not a fix: the token is short-lived and the poll is a separate
connection, so any rebalance during the flow reopens it.

#### Related Docs

- `docs/PROD-SECURITY-READINESS-20260724.md` (B5, checklist item 2 — "Routing")
- `docs/OIDC.md` (self-documents the gap)
- `ISSUE-UPLOAD-CHUNK-MULTINODE-01` (the other node-local-state blocker; same deploy precondition)

---

### ISSUE-AUTOLOGIN-COOKIE-INSECURE-01: `handleAutoLogin` hardcodes `Secure=false` on the session cookie

**Status**: 🟡 Open
**Severity**: Low–Medium — depends on whether `/client-login` is reachable in production
**Affected**: `handleAutoLogin` in `internal/api/server.go`; routes `GET /client-login[/]`
**Source of record**: NF-4 in `docs/PROD-SECURITY-READINESS-20260724.md`

#### Problem

`handleAutoLogin` sets the `sesamefs_auth` cookie with `secure` hardcoded to
`false` (commented "false for localhost"), while the OIDC callback path derives
it from `c.Request.TLS`. Behind external TLS termination the auto-login cookie
therefore ships without the `Secure` attribute and can be sent over a plaintext
downgrade.

The two paths are inconsistent in the other direction too, which is worth fixing
together: auto-login sets `httpOnly=true`, the callback sets `httpOnly=false`
(see `ISSUE-SESSION-COOKIE-NOT-HTTPONLY-01`). Neither path is the intended
contract; they are two independent guesses at the same cookie.

#### Fix Direction

One helper that sets `sesamefs_auth` for every path, deriving `Secure` from
`Request.TLS` or a trusted forwarded-proto, with a single decision on
`httpOnly`. Confirm whether `/client-login` is exposed in production at all — if
it is a dev-only affordance it should be gated behind `auth.dev_mode` rather
than hardened.

#### Related Docs

- `docs/PROD-SECURITY-READINESS-20260724.md` (NF-4, checklist item 6)

---

### ISSUE-SESSION-COOKIE-NOT-HTTPONLY-01: `sesamefs_auth` is a replayable bearer token in a JS-readable cookie

**Status**: ✅ Fixed 2026-08-12
**Severity**: High — an XSS yields full session-token theft, not merely a read surface
**Affected**: `internal/api/server.go`, `internal/api/v2/auth.go` — the OIDC callback cookie
write and the auth resolution order

#### What Is True Today

All four previously non-`HttpOnly` writers of `sesamefs_auth` — the OIDC login and logout
pair in each of `internal/api/server.go` and `internal/api/v2/auth.go` — now set
`httpOnly=true`, funneled through one `setAuthCookie` helper per package so the flag can't
drift between login and logout again. `handleAutoLogin` is a fifth writer of this cookie and
is untouched: it already set `httpOnly=true`, so it was never part of this defect. `Secure` is
unchanged (still derived from `c.Request.TLS`; the separate `ISSUE-AUTOLOGIN-COOKIE-INSECURE-01`
covers the one site — `handleAutoLogin` — that hardcodes it, and the broader TLS-terminating-
proxy gap tracked in `TECHNICAL-DEBT.md` #21).

One knock-on, dev-only: seeding the session from the browser console
(`document.cookie = "sesamefs_auth=..."`, the SSO-less dev login in `README.md`) is now
ignored by the browser whenever a real `HttpOnly` cookie of that name already exists — per
RFC 6265 a script may not overwrite one. It still works from a clean profile, and the README
records the caveat. Playwright's `context.addCookies` is unaffected because it writes to the
browser's cookie jar directly, not through JS, so `mobile-frontend/e2e-sesamefs/` keeps working.

What was verified before closing: a repository-wide search (including `mobile-frontend/`)
found no JS code anywhere in this repository that reads this cookie's value — the only
`document.cookie` touching it was a best-effort clear in `frontend/src/utils/auth-state.js`,
and the server already clears it authoritatively on logout regardless. The desktop-client SSO
flow gets its token via `clientSSOStore` polling (`docs/OIDC.md`), not by reading this cookie
from an embedded WebView, contradicting the comment that used to justify `httpOnly=false`. The
project owner confirmed no client outside this repository depends on reading it either.
`internal/api/server_test.go` (`TestServerSetAuthCookie`) and `internal/api/v2/auth_test.go`
(`TestAuthHandlerSetAuthCookie`, and the extended `TestLogout`) pin `HttpOnly` on both helpers,
covering login and logout without needing to mock a real OIDC exchange.

#### Related Docs

- `docs/PROD-SECURITY-READINESS-20260724.md` (SEC-3, NF-3, checklist item 6) — dated snapshot,
  not retro-edited; this entry is the live status.
- `docs/OIDC.md`, `docs/diagrams/auth-layer.md` — updated to match.
- `ISSUE-AUTOLOGIN-COOKIE-INSECURE-01` (the same cookie's `Secure` flag, still open, separate fix)

---

### ISSUE-AUDIT-TRAIL-INCOMPLETE-01: `audit_log` records deletions but not grants

**Status**: 🟡 Open — compliance gap under IOCD
**Severity**: Low–Medium (compliance/forensics, not correctness)
**Affected**: `audit_log` writers across `internal/api/v2`; table defined in `internal/db/migrations/001_initial_schema.cql`
**Source of record**: NF-6 in `docs/PROD-SECURITY-READINESS-20260724.md`; compounds RB-3

#### Problem

An `audit_log` table exists and is written by exactly six sites, verified
2026-07-25:

| Writer | Action logged |
|---|---|
| `admin.go` | `organization.update` |
| `admin_extra.go` | `delete_address_book_group` |
| `admin_groups.go` | `delete_group` |
| `groups.go` | `delete_group` |
| `departments.go` | `delete_department` |
| `internal/gc/store_cassandra.go` | GC cascade actions |

Nothing writes an entry for share-link creation, group-member **adds**,
permission grants, role changes, or logins. The trail is therefore one-sided:
it can show that access was removed but never that it was granted, which under
IOCD is arguably worse than no trail, because it looks complete.

The original NF-6 wording said "delete-only"; `organization.update` is the one
non-delete writer, so the precise statement is "deletes plus one org-update
path".

#### Fix Direction

Decide the event set first (grant, revoke, share create/delete, membership
add/remove, role change, login), then write them through one helper rather than
six inline `INSERT`s — the current copies already drift in their `details`
shape and in whether the error is checked. Pairs with RB-3
(`permission_audit_logs`) rather than being separate work.

#### Related Docs

- `docs/PROD-SECURITY-READINESS-20260724.md` (NF-6, RB-3, checklist item 5)
- `docs/ADMIN-FEATURES.md`

---

### ISSUE-UPLOAD-SIZE-GUARDS-BOTH-ZERO-01: Both chunked-upload size guards can be disabled together

**Status**: 🟡 Open — latent in shipped configs, no config invariant prevents it
**Severity**: Low (config), but the failure mode is an unbounded node-local `Truncate`
**Affected**: `internal/config/config.go` validation; `chunked_staging_max_bytes` and `server.max_upload_mb`
**Source of record**: NF-7 in `docs/PROD-SECURITY-READINESS-20260724.md`; overlaps UP-4

#### Problem

`max_upload_mb: 0` is documented as "unlimited" and
`chunked_staging_max_bytes: 0` is documented as "guard disabled". Set both and
the first chunk of a chunked upload performs `Truncate(attacker_declared_total)`
against a node-local temp file with nothing bounding the **logical** size.
`Validate()` rejects a *negative* `chunked_staging_max_bytes` but has no
invariant against the both-zero combination.

Two facts sharpen this beyond the original NF-7 note (verified 2026-07-25):

1. **Every shipped config sets `chunked_staging_max_bytes: 0`** — including
   `config.prod.yaml`. So the staging guard is off by default everywhere, and
   UP-4's "operator must set a real per-node value" is not an omission in one
   file, it is the shipped default in all of them.
   **And unlike most prod settings, there is no `.env` escape hatch**
   (verified 2026-07-25): `config.go` has env overrides for `SEAFHTTP_TOKEN_TTL`
   and the three `SEAFHTTP_ZIP_*` limits, and a full `WEB_UPLOADS_*` set, but
   **none for `seafhttp.chunked_staging_max_bytes` and none for
   `server.max_upload_mb`**. Both can only be changed by editing the YAML. An
   operator following the usual "configure prod through the environment" pattern
   therefore cannot turn this guard on at all, which is why it should get a
   non-zero default rather than a deployment note.
2. The only bound in prod is `max_upload_mb: 102400` (100 GiB). A single upload
   may therefore declare a **logical** size of up to 100 GiB. On sparse-file-
   capable filesystems (ext4/xfs) `Truncate` does **not** allocate that much
   immediately; physical pressure grows as chunks are written. Concurrent
   sessions can still exhaust `/tmp` because no aggregate staging budget is
   enabled.

#### Fix Direction

Add a `Validate()` invariant rejecting `max_upload_mb == 0 && chunked_staging_max_bytes == 0`,
and give `chunked_staging_max_bytes` a non-zero default sized to a plausible node
rather than shipping the guard off. Both are cheap; the second is what actually
changes the deployed posture. Add `SEAFHTTP_CHUNKED_STAGING_MAX_BYTES` (and
arguably `SERVER_MAX_UPLOAD_MB`) to the env-override set at the same time, so the
value is reachable the way every other prod setting is.

#### Related Docs

- `docs/PROD-SECURITY-READINESS-20260724.md` (NF-7, UP-4, UP-6, checklist item 4)

---

### ISSUE-ORG-SCOPE-CHECK-PER-HANDLER-01: Cross-tenant authorization is copy-pasted, not middleware

**Status**: 🟡 Open — latent; no current gap, verified and live-tested
**Severity**: Low today, High the day someone forgets it
**Affected**: the `/org/:org_id/admin` route group; `requireOrgAccess` call sites in `internal/api/v2`
**Source of record**: NF-5 / RB-6 in `docs/PROD-SECURITY-READINESS-20260724.md`

#### Problem

The `/org/:org_id/admin` group's middleware checks the caller's JWT org *role*,
not that the `:org_id` in the path equals the caller's own org. The actual
cross-tenant gate is `requireOrgAccess`, called individually inside roughly 50
handlers. An audit of every admin route registration found **no handler missing
it** and live testing confirmed a tenant admin gets 403 on other orgs' admin
routes — so this is not an open hole. It is a structural fragility: the safety
property depends on every future handler author remembering one call, and the
failure mode is a silent cross-tenant BOLA that no test would catch unless it
specifically targets the new handler.

#### Fix Direction

Hoist the `:org_id` == caller-org check into the sub-group middleware and leave
`requireOrgAccess` in place as defense in depth. The migration is mechanical and
turns "every author must remember" into "an author must actively opt out".

#### Related Docs

- `docs/PROD-SECURITY-READINESS-20260724.md` (NF-5, RB-6, checklist item 6)

---

### ISSUE-ORG-ADMIN-DELETION-BANNER-01: Org-admin UI has no soft-delete grace banner

**Status**: 🟡 Open — backend bootstrap exposure + frontend banner required
**Severity**: Low (UX / operator awareness)
**Affected**: org bootstrap payload (`loadBootstrapOrgData` / `buildOrgBootstrapPageOptions`) and org-admin dashboard frontend; org soft-delete / grace lifecycle already exists server-side
**Source of record**: `KNOWN_ISSUES.md` Frontend Pending summary row (left without an id until 2026-07-25)

#### Problem

When an organization is soft-deleted and sitting in the grace window before
cascade, the **org-admin** dashboard does not show a banner/warning with days
remaining. Superadmin soft-delete/restore UI and the deleted-orgs filter are
already fixed (`ISSUE-FRONTEND-ORG-DELETE-01`); this tenant-facing awareness
gap is the remaining row from that group.

This is **not** frontend-only. `loadBootstrapOrgData` selects name, settings,
plan, quotas, and period fields from `organizations` but **not** `deleted_at`
(or status lifecycle). `buildOrgBootstrapPageOptions` then exposes `orgID`,
name, members, and settings flags — still without deletion state or grace
remaining — so the SPA has nothing to render even if a banner component were
added.

#### Fix Direction

Three edits, in order: add the field to the `bootstrapOrgData` struct, add
`deleted_at` (and status) to the `loadBootstrapOrgData` SELECT — it currently
reads name, settings, max_users, plan, billing_cycle, quota_policy,
storage_config, the quota columns and the period columns, and nothing about
lifecycle — then expose status and grace remaining (from `deleted_at` +
`GC.OrgGraceDays`, default 30) in `buildOrgBootstrapPageOptions`. Only then
reuse the sysadmin retention messaging helpers on the org-admin frontend shell
(banner or dashboard card).

#### Related Docs

- [OPEN-WORK-INDEX.md](OPEN-WORK-INDEX.md)
- `ISSUE-FRONTEND-ORG-DELETE-01` (resolved superadmin counterpart)
- `internal/api/bootstrap.go` (`loadBootstrapOrgData`, `buildOrgBootstrapPageOptions`)

---

### ISSUE-SYNC-UNBOUNDED-BODIES-01: Four sync handlers still read the request body unbounded

**Status**: ✅ Fixed 2026-08-12
**Severity**: Medium — authenticated memory-pressure DoS
**Affected**: `PutCommit`, `PackFS`, `RecvFS`, `CheckFS` in `internal/api/sync.go`
**Source of record**: **X9** in `docs/UPLOAD-FENCE-FINDINGS-REGISTRY.md`

#### What Is True Today

All four handlers now read through the shared `readLimitedRequestBody` helper (the
same one PR-10 wired into `PutBlock`/`check-blocks` for F12), each with a cap
derived from its own payload profile rather than one shared constant:

- `PutCommit`, `PackFS`, `CheckFS` — plain consts (`maxPutCommitBodyBytes` = 1 MiB;
  `maxPackFSBodyBytes`, `maxCheckFSBodyBytes` = 16 MiB, matching `check-blocks`'s
  existing const, since both carry the same small id-list shape).
- `RecvFS` — **configuration**, not a const (`config.SeafHTTP.RecvFSMaxBytes`,
  default 128 MiB via `config.DefaultRecvFSMaxBytes`, resolved by
  `syncRecvFSMaxBytes()`). Unlike the other three, RecvFS carries a real batch of
  packed FS objects with no measured client batch size or protocol-documented
  ceiling to anchor a fixed number on (`docs/SEAFILE-SYNC-PROTOCOL-RFC.md` does
  not specify one) — the default is deliberately generous, and an operator can
  raise it via `SEAFHTTP_RECV_FS_MAX_BYTES` or `recv_fs_max_bytes` without a code
  change if a real large commit needs more headroom. Zero and negative values are
  rejected at boot, and a malformed env value is reported rather than silently
  dropped back to the default: an unbounded body is the defect the cap closes, so
  no configuration may restore it.

**A byte cap alone was not enough on the two id-list routes.** `pack-fs` and
`check-fs` parsed their id list the naive way — `strings.Split` on the whole body,
or `json.Unmarshal` into a `[]string` — so a body *under* the 16 MiB cap still
expanded ~17x: 16 MiB of bare newlines becomes ~16.7M string headers (~268 MB).
That is the cardinality half of the same defect, already solved for `check-blocks`
by a parser that refuses during the parse instead of after the list is
materialized. Both routes now share it (`parseBoundedIDList`, generalized from
`parseCheckBlockIDs`), with id caps **derived from their byte caps**
(`maxPackFSIDs`, `maxCheckFSIDs` = byte cap / `minFSIDWireBytes`). Deriving them
matters: the densest well-formed body the byte cap admits carries exactly the cap
in ids, so the id cap is unreachable for real traffic and can only fire on
degenerate input — it is not a new limit on large libraries.
`TestFSIDCapsCannotCutWellFormedBodies` pins that invariant arithmetically;
`TestFSIDCountCapsCutAmplification` asserts the 413 through the handler and,
separately, measures the parse at 16.0 MB — just its `string(body)` — against the
same 96 MB ceiling the `check-blocks` canary uses. The two are separate on purpose:
an allocation canary wrapped around a whole round trip also measures
`readLimitedRequestBody`'s 16 MiB read, which the ceiling was never derived for and
which varies enough by platform to make a shared threshold meaningless (50.6 MB on
go1.26/windows, 113.9 MB on go1.25/linux, same code).

**Scope of the fix, precisely:** this closes the **unbounded body per request**
and the id-list amplification within it. Three things it does **not** do, all now
tracked rather than left implicit:

- It does not bound aggregate memory under concurrency — N concurrent `RecvFS`
  requests near the 128 MiB cap can still sum to N × the cap
  (`ISSUE-SYNC-METADATA-CONCURRENCY-01`).
- **It does not make `RecvFS` a bounded-memory handler.** The body cap bounds the
  *compressed* bytes; `RecvFS` then inflates each packed object with an unbounded
  `io.ReadAll(zlibReader)`, and DEFLATE's best case is ~1029:1 (measured), so a
  128 MiB body can inflate to ~126 GiB. The buffered body is not this handler's
  largest allocation, and reading this entry as "recv-fs memory is now bounded"
  would be wrong (`ISSUE-RECVFS-DECOMPRESSION-AMPLIFICATION-01`).
- **The derived id caps are not a work bound.** They were derived against one axis
  only — never reject a well-formed body — and are silent on what an accepted list
  costs. Nothing deduplicates it, so ~409,000 repeats of one valid id are a
  well-formed request; `pack-fs` materializes the whole response for it. No id cap
  is a work bound on its own — `check-blocks`' 100,000 is a parse bound too, and
  what closed X11 there was dedup, context, fan-out and admission, not the number.
  Tracked as `ISSUE-SYNC-FSID-WORK-AMPLIFICATION-01`.

#### Related Docs

- `docs/UPLOAD-FENCE-FINDINGS-REGISTRY.md` (X9, and X10/X11 for what a cap is not)
- `docs/GC-UPLOAD-FENCE-PR-PLAN.md` (PR-10 scope)
- `ISSUE-SYNC-METADATA-CONCURRENCY-01` (the aggregate bound this fix does not provide)

---

### ISSUE-SYNC-FSID-WORK-AMPLIFICATION-01: `pack-fs`/`check-fs` bound the parse, not the work it triggers

**Status**: 🟡 Open — found 2026-08-12 auditing the X9 caps
**Severity**: High for `pack-fs` (one request can materialize a multi-GiB response), Medium for `check-fs`
**Affected**: `PackFS`, `CheckFS` in `internal/api/sync.go`
**Source of record**: opened 2026-08-12; the fs-id equivalent of `ISSUE-CHECKBLOCKS-WORK-AMPLIFICATION-01` (X11)

#### Problem

`maxPackFSIDs`/`maxCheckFSIDs` (409,200) are derived from the 16 MiB byte caps so
they can never cut a well-formed body — see `ISSUE-SYNC-UNBOUNDED-BODIES-01`. That
derivation was chosen against exactly one axis, *do not reject real traffic*, and is
silent on a second: **the work an accepted list triggers**. Nothing deduplicates the
list, so a well-formed request may repeat one valid fs id ~409,000 times.

`PackFS` is the severe half. Per requested id it issues one sequential Cassandra
point read, marshals and zlib-compresses the object, and appends it to a single
`bytes.Buffer`, which is only handed to `c.Data` once the whole loop finishes — so
the entire response is materialized in memory. With a repeated id, response size is
`409,200 × (that object's compressed size)`: ~80 MiB for a small file object, and
multiple GiB if the repeat target is a large directory. It requires only
`PermissionR`.

`CheckFS` shares the fan-out but not the buffer: the per-id check is a Cassandra
query, **not** a lookup in the `computedToStored` map (the map only translates the
id), so an accepted list is ~409,000 sequential point reads holding the handler.
`buildFSIDMapping` is per-request, not per-id, so it is not the amplifying term.

**X11 already met this exact shape on `check-blocks`, and its dated note describes
it almost word for word:** "identical ids were resolved once per occurrence rather
than once. So the cheapest possible request — one id repeated, then abandoned — was
also among the most expensive to serve." It also names a second term this entry
would otherwise miss: that route's mapping read "took no `context` at all, so a
client disconnect could not stop the loop". `PackFS` and `CheckFS` have the same
gap — both call `h.db.Session().Query(...)` with no context, so abandoning the
request does not stop the work it started.

Note what the 100,000 cap on `check-blocks` is and is not. The registry is explicit
that it "was chosen as a safe parse bound rather than validated against Cassandra,
the S3 pool, response size or client cancellation" — it is **not** a work bound and
is not what closed X11. So "these routes allow 4x check-blocks' cap" is the wrong
comparison to draw; the right one is that `check-blocks` grew the controls below and
these two routes have none of them.

None of this is new in the X9 fix — before it, both routes had no body cap and
therefore no id bound whatsoever, so the current state is strictly better. It is
filed because the X9 caps should not be mistaken for a work bound.

#### Fix Direction

X11's resolution is the template, and it is worth following closely because it
already answered the question this entry would otherwise re-litigate. On
`check-blocks` the route now deduplicates ids **before any lookup**, resolves them
through a context-carrying DB call at a configured fan-out, and takes an admission
from its **own** limiter instance — separate capacity, so one route storming cannot
spend the other's slots. The same four moves apply here.

Two route-specific additions:

- `PackFS` also needs its **response** bounded, not just its lookups: dedup fixes
  the DB fan-out but a repeated id still emits one record per occurrence into a
  single `bytes.Buffer`. Prefer streaming records to `c.Writer` over accumulating
  them. Whoever does this must decide what happens when a cap is reached *after*
  headers are on the wire — at that point a clean 413 is no longer available, which
  is the same late-failure class as `ISSUE-ZIP-STREAM-LATEFAIL-01`.
- Confirm the client contract before deduplicating the **response** (as opposed to
  the work): the response is a stream of `(id, size, data)` records and a client
  indexing by id should not care, but that must be verified, not assumed. Resolving
  each distinct id once while still emitting one record per requested id needs no
  such confirmation and is the safe half.

**Do not lower the id caps as part of this.** X11 considered exactly that and
deliberately declined: it kept 100k as the default and turned it into a validation
ceiling, on the grounds that "lowering it on a guess would trade a bounded
amplification for an unbounded risk of 413-ing a legitimate initial sync." It added
`sync_check_blocks_ids_per_request` as the evidence a future reduction would need.
The equivalent metric for these two routes is the prerequisite here too — the caps
are derived to be unreachable by well-formed bodies precisely so they never do the
413-ing X11 warns about.

#### Related Docs

- `ISSUE-CHECKBLOCKS-WORK-AMPLIFICATION-01` / registry X11 and its 2026-07-31 dated note (the same defect on the block route; closed by dedup-before-lookup, a context-carrying resolve at a configured fan-out, and its own admission capacity — **not** by lowering the id cap, which it deliberately kept)
- `ISSUE-SYNC-UNBOUNDED-BODIES-01` (where the 409,200 caps come from, and why they are derived that way)
- `ISSUE-SYNC-METADATA-CONCURRENCY-01` (the aggregate term; this issue is per-request)

---

### ISSUE-RECVFS-DECOMPRESSION-AMPLIFICATION-01: `recv-fs` inflates each packed object unbounded

**Status**: ✅ Fixed 2026-10-05 (`fix/sync-recvfs-decompression-bound`, merged to main by PR #261) — found 2026-08-12 while auditing the X9 caps
**Severity**: High — one authenticated request can exhaust process memory; the body cap does not bound it
**Affected**: `RecvFS` in `internal/api/sync.go` (the `io.ReadAll(zlibReader)` inside the per-object loop)
**Source of record**: opened 2026-08-12; pre-existing, **not** introduced by `ISSUE-SYNC-UNBOUNDED-BODIES-01`

#### Problem

`recv-fs` carries packed objects as `40-byte id + 4-byte size + zlib-compressed JSON`.
The X9 cap (`seafhttp.recv_fs_max_bytes`, default 128 MiB) bounds the **compressed**
body. The handler then decompresses each object with an unbounded `io.ReadAll` over
a `zlib.Reader`, so the largest allocation the handler makes is not the one that was
capped.

Measured, not estimated — `compress/zlib` at `BestCompression` over a run of
identical bytes:

| plaintext | compressed | ratio |
|---|---|---|
| 1 MiB | 1,043 B | 1005:1 |
| 16 MiB | 16,320 B | 1028:1 |
| 128 MiB | 130,466 B | 1029:1 |

So a **128 KiB** request inflates to ~128 MiB, and a request at the 128 MiB body cap
inflates to ~126 GiB. The `objSize` header is attacker-controlled and only checked
against the remaining body length, so a single object may span the whole body.

This is why closing X9 must not be read as "recv-fs memory is bounded": it bounds the
buffered body, which is not the dominant term.

#### Fix Direction

Bound the decompressed side, not just the compressed one — `io.LimitReader` around
the `zlib.Reader` (plus a total across the batch, since the loop runs per object),
rejecting rather than truncating, because a truncated fs object would be silently
stored with a body that no longer hashes to its `fs_id`.

The cap value should **not** be guessed the way `recv_fs_max_bytes` had to be. Here
there is something to measure: an fs object is either a directory's `dirents` or a
file's `block_ids`, and both have computable worst cases from this deployment's own
data (largest directory entry count; largest file ÷ block size). Measure those, then
set a per-object cap with real headroom. Until then, note that the compressed body
cap does bound *how much work* an attacker gets per request — it just does not bound
the memory that work allocates.

When it is implemented, `io.LimitReader(zlibReader, max)` alone is not the fix:
reading `max` and accepting what came back silently truncates. Read `max+1` (or
check for a trailing byte) and **reject the object** — a partial decompressed object
must not be parsed or persisted as if the complete packed object had arrived. Note
that the reason is *not* "the stored body would no longer hash to its `fs_id`":
whether that equality holds at all is exactly what
`ISSUE-RECVFS-FSID-UNVERIFIED-01` leaves open, so this fix must not be justified by
an invariant this repository has not established.

#### Resolution (2026-10-05)

Two new caps bound the decompressed side, both configurable and both required
(zero or negative fails at boot, like `recv_fs_max_bytes`):

- `seafhttp.recv_fs_max_object_bytes` (`SEAFHTTP_RECV_FS_MAX_OBJECT_BYTES`),
  default **16 MiB**: the decompressed JSON of one packed object.
- `seafhttp.recv_fs_max_inflated_bytes` (`SEAFHTTP_RECV_FS_MAX_INFLATED_BYTES`),
  default **64 MiB**: the decompressed bytes of the whole request. It must be at
  least the object cap.

`inflateRecvFSObject` reads through `io.LimitReader(zr, limit+1)`, where `limit` is
the smaller of the object cap and what remains of the request cap. A `limit+1`-th
byte means the object is over: the request is rejected **413** and the object is
neither parsed nor stored. Nothing is truncated. The two 413 bodies are
`{"error":"fs object too large when decompressed","max_object_bytes":N}` and
`{"error":"recv-fs batch too large when decompressed","max_inflated_bytes":N}`.
Objects earlier in the same body were already stored, as with every other
mid-batch rejection in this handler. Bytes inflated by an object that then fails
to decompress (a bad adler32 trailer, say) still count toward the request cap. The
object is skipped as before, but it can no longer buy uncounted inflate work.

The caps are measured, as this entry asked, not guessed:

- **Object cap = at Cassandra's default CQL request limit.** A received object is
  written in one CQL request, and Cassandra 5.0.9's defaults reject requests over
  16 MiB. The limit covers the whole request, not only the JSON.
  Measured on the two-DC `docker-compose.mr-cluster.yaml` stack through the real
  `recv-fs` route on unmodified `main`: directory objects of 4/8/12/15/15.9 MiB
  JSON stored (200), while 16.1/17/24 MiB failed 500 with "Request is too big:
  length 16882132 exceeds maximum allowed length 16777216". Exactly 16 MiB was
  not measured. The cap bounds inflation; it is not a persistence guarantee, since
  an object just under it can still be rejected once the request's overhead is
  added, while anything over it could only be inflated and then fail to store.
  The cap is about 380k block ids per
  file object, a ~2 TiB file at the stock client's 6 MiB minimum CDC block.
- **Request cap = representative client batches plus headroom.** Seafile's
  `send_fs_objects` (daemon/http-tx-mgr.c) stops packing a batch once it reaches
  `MAX_OBJECT_PACK_SIZE` (1 MiB) compressed. That bounds the compressed size, not
  the inflated one. Fs JSON with distinct random hex ids compresses about 2:1, so
  representative batches stay far below 64 MiB. It is not a guarantee for every
  batch the protocol allows: six distinct file objects of 270k repeated block ids
  each (11.07 MiB apiece, within the object cap) pack into ~199 KiB and inflate to
  66.43 MiB, which gets a 413. That is the intended safety behaviour; the cap is
  kept, and raising it needs evidence of real batches that hit it.

Measured effect of one ~1 MiB request whose single valid, correctly addressed
directory object inflates to 256 MiB:

| | Before (unmodified `main`) | After |
|---|---|---|
| Unit (`TestRecvFSRejectsOversizedObjectBeforeMaterializing`) | 200, object stored, 1950.6 MiB allocated | 413, not stored, 102.9 MiB allocated |
| Live two-DC cluster, USA node cgroup `memory.peak` | 109 → 815 MiB, 7.5 s, then 500 at storage | not measured live; the unit row above is the after evidence |

Tests: `internal/api/sync_recvfs_inflate_test.go` (exact object-cap boundary,
request-cap boundary and mid-batch crossing, failed objects counted, a stock-shaped
~5 MB batch of 333 objects stored byte-for-byte under the defaults, the allocation
canary) and `TestEnvOverrideRecvFSInflateCaps`. Omission controls were run on the
fix: not counting failed objects, dropping the request cap, truncating instead
of rejecting, and dropping the read limit each turn at least one test RED.

Correction to the Fix Direction above: `RecvFS` has checked
`fs_id == SHA-1(decompressed JSON)` since 433d5910f (2026-09-07). That predates
this fix and is not what it relies on: the cap rejects on size alone, before the
hash is computed.

Still open: this bounds one request. N concurrent requests still cost N times the
caps plus N buffered bodies; that aggregate term is
`ISSUE-SYNC-METADATA-CONCURRENCY-01`.

#### Related Docs

- `ISSUE-SYNC-UNBOUNDED-BODIES-01` (the body cap this sits behind)
- `ISSUE-SYNC-METADATA-CONCURRENCY-01` (the aggregate term; N concurrent inflates)
- `ISSUE-RECVFS-FSID-UNVERIFIED-01` (the same handler trusts the id it stores)
- `docs/SEAFILE-SYNC-PROTOCOL-RFC.md` §5.6.1 (the packed-object wire format)

---

### ISSUE-RECVFS-FSID-UNVERIFIED-01: `recv-fs` stores the client's fs_id without checking it addresses the content

**Status**: 🟡 Open — **question, not a diagnosed defect**; establish the Seafile contract before anyone "fixes" it
**Severity**: Unrated pending that answer — either content-addressing integrity, or by design
**Affected**: `RecvFS` in `internal/api/sync.go`
**Source of record**: opened 2026-08-12 during the X9 audit

#### Problem

`RecvFS` takes the 40 bytes the client supplies, decompresses the object body, and
inserts `fs_objects(library_id=repoID, fs_id=<client-supplied>, …)`. It never
recomputes `SHA-1(jsonData)` to confirm the id addresses the content it is filed
under. The handler's own comment asserts the invariant it does not check: *"the
fs_id is the SHA1 hash of the exact JSON content"*.

Not for want of the machinery. `computeCorrectedObject` in this same file hashes an
object's JSON to derive its `ComputedFSID`, which is how the computed↔stored mapping
is built at all — so the write path declines a check the read path depends on. And
on the read side the id *is* treated as content-addressed:
`internal/integration/sync_protocol_regression_test.go` records that the served JSON
"must re-hash to the requested fs_id (otherwise the desktop client rejects the
object)".

#### Fix Direction

Not "add a hash check". The obvious fix — recompute and reject on mismatch — is very
likely **wrong here**, and that is the point of filing this as a question.

SesameFS deliberately maintains a *stored* fs id that differs from the *computed*
one. `CheckFS` says so directly ("Client sends COMPUTED fs_ids (SHA-1 of corrected
JSON), but we store objects with their ORIGINAL (stored) fs_ids"), and
`buildFSIDMapping`/`collectCorrectedObjects` — built on the `computeCorrectedObject`
hashing above — exist precisely to translate between them. A strict
`fs_id == SHA-1(body)` check in `RecvFS` would reject writes this design intends to
accept. The existence of that mapping layer is the whole reason this is a question:
the divergence is known and compensated for, so what is undetermined is whether the
write path is *allowed* to introduce a new one.

So the real question is which of these is true, and the answer decides everything:

- the id is genuinely content-addressed and the mapping layer compensates for a
  historical divergence — in which case verification belongs here, and its absence
  means a client can file arbitrary content under an id another client will later
  fetch by hash; or
- the id is a client-chosen key that this deployment never treated as a hash — in
  which case the comments asserting otherwise are the bug, and they should be
  corrected before someone adds a check that breaks sync.

Answer that from the Seafile protocol contract and the mapping layer's history
first. Do not add a hash check on the strength of this entry alone.

#### Related Docs

- `docs/SEAFILE-SYNC-PROTOCOL-RFC.md` §5.6.1 (the wire format and its id definition)
- `ISSUE-RECVFS-DECOMPRESSION-AMPLIFICATION-01` (same handler, unrelated cause)

---

### ISSUE-SYNC-METADATA-CONCURRENCY-01: The sync metadata routes have no aggregate memory bound

**Status**: 🟡 Open — successor to X9's per-request cap
**Severity**: Medium — authenticated memory-pressure DoS, aggregate rather than per-request
**Affected**: `RecvFS`, `PackFS`, `CheckFS`, `PutCommit` in `internal/api/sync.go`
**Source of record**: opened 2026-08-12 when `ISSUE-SYNC-UNBOUNDED-BODIES-01` closed

#### Problem

`ISSUE-SYNC-UNBOUNDED-BODIES-01` bounds **one** request on each of these routes.
It does not bound N of them. `RecvFS` is the one that matters: its default cap is
128 MiB and its body is fully buffered before parsing, so 8 concurrent uploads
cost ~1 GiB of body buffers, 16 cost ~2 GiB, before anything else the process is
doing. The route group carries only `syncAuthMiddleware` — no concurrency gate, no
rate limit, no memory budget.

This is the same shape as **X10** on the block routes, and X10 is the reason this
entry exists as its own issue: X10 was closed by work scoped explicitly to
`PutBlock` (`sync_block_max_inflight_per_node`, `sync_block_memory_budget_bytes`,
the admission limiter and its 503 + `Retry-After` semantics). None of that machinery
covers the metadata routes, so closing X9 without opening this would have retired
the narrow finding and lost the layer underneath it.

#### Fix Direction

The block-route admission work is the template, not a new design: a bound on
in-flight metadata readers acquired **before** the buffering read, answering
**503 + `Retry-After`** after a bounded wait — never 429, which the official client
does not classify as retryable. Two details differ from the block path and should be
settled first:

- `RecvFS`'s 128 MiB default is generous precisely because no client batch size was
  ever measured. Measuring what the official client actually sends per `recv-fs`
  (upstream batches packed objects rather than sending a whole commit at once) would
  likely allow a far smaller cap, which is cheaper than an admission gate and should
  be evaluated before building one.
- The metadata routes should not draw from the block routes' capacity: one storming
  must not spend the other's slots, the same separation X10's dated note records.

#### Related Docs

- `docs/UPLOAD-FENCE-FINDINGS-REGISTRY.md` (X10 — the closed block-route equivalent,
  and its dated note on capacity separation)
- `ISSUE-SYNC-UNBOUNDED-BODIES-01` (the per-request cap this sits on top of)
- `ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01` (the umbrella X10 was subcontract B of)

---

### ISSUE-BATCH-MOVE-FALSE-SUCCESS-01: Legacy `moveBatchFiles` returns success without moving

**Status**: ✅ Fixed 2026-08-12 — false success fixed; legacy same-repo batch move via
`POST /file/move` remains unsupported and now returns 501 instead of a fabricated 200
**Severity**: Medium — false-success on a still-reachable handler
**Affected**: `FileHandler.moveBatchFiles` in `internal/api/v2/files.go` (reached when `MoveFile` gets `len(srcPaths) > 1`)
**Source of record**: PENDING-ISSUES-AUDIT-2026-05-14 item 4; code-verified 2026-07-25

#### What Is True Today

`moveBatchFiles` previously contained `// TODO: Implement actual batch move logic`
and returned `{"success": true, "moved": N}` for same-repo multi-file moves
without updating the FS tree; cross-repo batch move already correctly returned 501.
It now returns `501 Not Implemented` for the same-repo case too, with an error
pointing at the real endpoint. This is a bug fix, not new functionality: legacy
same-repo batch move via this endpoint is still not implemented, it just no
longer lies about having succeeded.

The **UI** path never used this and is unaffected: `seafileAPI.moveDirWithPolicy`
goes through `POST /api/v2.1/repos/sync-batch-move-item/` /
`async-batch-move-item/` → `SyncBatchMove`/`AsyncBatchMove` → `processSingleItem`
in `batch_operations.go` (integration-tested). The defect was reachable only by an
API client posting multiple `src` paths to the legacy `MoveFile` endpoint.

#### Related Docs

- [OPEN-WORK-INDEX.md](OPEN-WORK-INDEX.md) migration table
- `ISSUE-BLOCK-REPRESENTATION-COPY-01` (adjacent batch move/copy constraints)

---

### ISSUE-ACCOUNTS-M2M-PATH-01: Accounts M2M path lacks source tagging and idempotency

**Status**: 🟡 Open — channel works; hygiene incomplete
**Severity**: Medium — operability / audit provenance, not an auth hole
**Affected**: `AdminHandler.UpdateOrganization` and admin user CRUD under `/admin`
**Source of record**: former `quotas-pending-issues.txt` item 2; code-verified 2026-07-25

#### Problem

**What already works (do not re-open as "Accounts cannot provision"):**

- `/admin` requires `RequireSuperAdmin` + `apikeys.RequireScope(ScopeAdmin)`
- `UpdateOrganization`, `AdminAddOrgUser` / update / delete, `AdminCreateUser`,
  and `PreviewOrganizationPlanChange` exist and are usable by Accounts today
- Org-local user writes are gated by `Accounts.DisableOrgUserWrites` (default true)

**What is still open:**

- Audit rows from `UpdateOrganization` hardcode
  `override_source: "manual-superadmin"` even when the caller is an Accounts
  service key — no `source=accounts` distinction
- No version / `updated_at` idempotency for M2M retries
- User create/update/delete paths do not write `audit_log` at all (compounds
  `ISSUE-AUDIT-TRAIL-INCOMPLETE-01`)

#### Fix Direction

Tag Accounts-originated mutations (`source=accounts` or equivalent), add an
idempotency key / conditional update for org plan/quota PUTs, and emit
`audit_log` for admin user CRUD. Optional request signing is a separate deferred
item (`ISSUE-ACCOUNTS-M2M-REQUEST-SIGNING-01`).

#### Related Docs

- `docs/PLANS-AND-PERMISSIONS.md` §Accounts Provisioning
- `docs/ACCOUNTS-DASHBOARD-INTEGRATION.md`
- `ISSUE-ACCOUNTS-PROVISIONING-RUNBOOK-01`
- `ISSUE-AUDIT-TRAIL-INCOMPLETE-01`

---

### ISSUE-ACCOUNTS-PROVISIONING-RUNBOOK-01: Accounts platform API-key runbook was never written

**Status**: 🟡 Open — docs/ops debt
**Severity**: Low (documentation) — high operational value before go-live
**Affected**: docs only; auth mechanics already exist
**Source of record**: `PLANS-AND-PERMISSIONS.md`, `V1-PRODUCTION-ROADMAP.md`

#### Problem

`ACCOUNTS-PROVISIONING-RUNBOOK.md` has been referenced for bootstrap, rotation,
smoke-test and revocation of the dedicated platform service-account API key, but
the file was never created. The channel itself works (see
`ISSUE-ACCOUNTS-M2M-PATH-01`).

#### Fix Direction

Write the runbook (create platform user, mint `ScopeAdmin` key, rotate, revoke,
smoke against `PUT /admin/organizations/:org_id/` and user CRUD). Link it from
PLANS and the roadmap.

#### Related Docs

- [OPEN-WORK-INDEX.md](OPEN-WORK-INDEX.md)
- `docs/PLANS-AND-PERMISSIONS.md`
- `docs/V1-PRODUCTION-ROADMAP.md`

---

### ISSUE-ACCOUNTS-M2M-REQUEST-SIGNING-01: Optional request signing beyond admin API key

**Status**: 🟡 Accepted deferred for v1 — not a go-live blocker
**Severity**: Low (defense-in-depth hardening)
**Affected**: Accounts → SesameFS admin M2M routes
**Source of record**: readiness SEC-4 (reframed 2026-07-25)

#### Problem

M2M calls authenticate with a platform admin API key only. There is no
per-request HMAC/JWT signing. That is **by design for v1**: the admin API-key
channel is the intended Accounts integration and already works. Signing would be
extra theft-resistance if a key leaks, not a missing auth path.

#### Fix Direction

Post-v1: optional request signing (HMAC or short-lived JWT) on top of the
existing API key. Do not block go-live on this.

#### Related Docs

- `ISSUE-ACCOUNTS-M2M-PATH-01`
- `docs/PROD-SECURITY-READINESS-20260724.md` SEC-4

---

### ISSUE-TRAFFIC-RECORDER-DROPS-01: Saturated traffic recorder drops events silently

**Status**: 🟡 Open
**Severity**: Medium — observability / billing undercount under load
**Affected**: `traffic.Recorder.recordAsync` in `internal/traffic/recorder.go`
**Source of record**: quotas-pending item 12; PENDING-ISSUES-AUDIT item 16

#### Problem

When `maxInflight` is full, `recordAsync` hits a silent `default:` drop — no
counter, no log. Traffic can be under-counted without an operator signal.

#### Fix Direction

Export a drop counter + log-at-most-once / metric; optionally block or sample
instead of silent discard.

#### Related Docs

- [OPEN-WORK-INDEX.md](OPEN-WORK-INDEX.md)
- `docs/QUOTAS-AND-TRAFFIC-PLAN.md`

---

### ISSUE-MID-OPERATION-REVOCATION-01: Permission not re-checked mid long-running op

**Status**: 🟡 Open
**Severity**: Medium (upload/ZIP) / Low (mid-stream download only)
**Affected**: chunked upload session start auth; mid-stream download; ZIP/bulk
**Source of record**: readiness UP-5 / DL-5 / RB-4

#### Problem

Chunked uploads authorize at session start only. New download requests
re-check `HasLibraryAccess`, but an already-streaming download has no in-flight
re-check. ZIP/bulk revoke-during-op continues to completion.

#### Fix Direction

Decide per surface: re-check on each chunk / periodic mid-stream check / accept
as documented best-effort for streams already committed. One issue tracks the
class so the three readiness rows do not drift.

#### Related Docs

- `docs/PROD-SECURITY-READINESS-20260724.md` UP-5, DL-5, RB-4

---

### ISSUE-SHARELINK-NO-ORG-SCOPE-01: No org-internal share-link scope

**Status**: 🟡 Open / BY DESIGN option
**Severity**: Medium (product)
**Affected**: share-link create + `resolveShareLink`
**Source of record**: readiness SH-2; `BUG-SHARE-LINK-NO-INTERNAL-SCOPE-20260618.md`

#### Problem

Every public link is token-only and anonymously reachable; there is no
"org members only" scope.

#### Fix Direction

Product decision: add an org-internal scope, or document as intentional.

---

### ISSUE-SHARELINK-CREATOR-KEY-01: Encrypted share links use creator's decrypt key

**Status**: 🟡 Open
**Severity**: Medium
**Affected**: encrypted share-link decrypt path in `sharelink_view.go`
**Source of record**: readiness SH-3

#### Problem

Encrypted library share links decrypt using the **creator's** key/session model,
not receiver auth. Revoking the creator's decrypt session can break receivers;
create-time does not reject encrypted libraries (serve-time 403 only).

#### Fix Direction

Document the model and/or require creator decrypt session / reject create for
encrypted libs until a receiver-auth design exists.

---

### ISSUE-SHARELINK-BCRYPT-COST-01: Share-link passwords use bcrypt cost 10

**Status**: 🟡 Open (low)
**Severity**: Low
**Affected**: `share_links.go` `bcrypt.GenerateFromPassword` ×3
**Source of record**: readiness SH-4

#### Problem

Passwords use `bcrypt.DefaultCost` (10), not Argon2id / higher bcrypt cost.
Earlier docs claimed cost 12.

#### Fix Direction

Raise cost or migrate to Argon2id; keep constant-time compare.

---

### ISSUE-ENCRYPTED-VIEW-CONTENT-LENGTH-01: Encrypted v2 view/share omit Content-Length

**Status**: 🟡 Open (scoped)
**Severity**: Low
**Affected**: `fileview.go`, `sharelink_view.go` inline/share-raw surfaces
**Source of record**: readiness DL-6

#### Problem

Primary SeafHTTP download sets `Content-Length` from plaintext `size_bytes`;
v2 inline-view / share-raw encrypted surfaces omit it.

#### Fix Direction

Set length from known plaintext size on those surfaces, or document the omission.

---

### ISSUE-DOWNLOAD-NO-REHASH-01: Download does not re-hash block bytes

**Status**: 🟡 Open — defense-in-depth
**Severity**: Medium (defense-in-depth)
**Affected**: download / streaming paths
**Source of record**: readiness DL-7

#### Problem

Block integrity on download relies on the object store; bytes are not re-hashed
before serve.

#### Fix Direction

Optional verify-on-read for high-assurance deployments; not a correctness bug
given content-addressed storage + store checksums.

---

### ISSUE-OIDC-ROLE-RECONCILIATION-01: OIDC claims vs manual role overrides undefined

**Status**: 🟡 Open — design debt
**Severity**: Medium (design)
**Affected**: OIDC login role sync vs admin overrides
**Source of record**: readiness RB-2

#### Problem

Authoritative-source rule for OIDC role claims versus manual admin role
overrides is undefined; re-sync on login can conflict with admin changes.

#### Fix Direction

Decide SoT (IdP wins / admin wins / last-write-wins with audit) in
`PLANS-AND-PERMISSIONS.md` / `ROLES-AND-PERMISSIONS.md` and implement.

---

### ISSUE-UPLOAD-PER-BLOCK-PAXOS-01: One `SERIAL` LWT/Paxos transaction per block on upload

**Status**: 🟡 Open — deferred PR-11 pending measurement
**Severity**: High (perf), not a correctness blocker
**Affected**: metadata-registering upload paths (`InstallBlockMetadata` and
tuple-bound repair CAS/LWTs)
**Source of record**: registry **X4**; readiness UP-2; P-4; [X1/X4 hot-path characterization](./UPLOAD-PAXOS-HOT-PATH-X1-CHARACTERIZATION.md)

#### Problem

Each block invocation that reaches metadata registration pays one `SERIAL`
LWT/Paxos transaction when the effective serial setting is `SERIAL`. The shipped
production default/example uses `SERIAL`, but an environment override can select
`LOCAL_SERIAL`, and a single-region deployment has no WAN leg. New content/full
registration is ~128 such transactions per GiB at 8 MiB blocks in a spanning
multi-DC topology; browser/sync preflight may bypass fully deduplicated blocks.
This is a shared cost of governed upload paths when they reach registration, not a
universal per-file cost.

#### Fix Direction

Do not start PR-11 until per-statement production latency, metadata-permit wait
and placement proposals are characterized. Do not switch production to
`LOCAL_SERIAL` as a mitigation.

**Removing the LWT is not the goal.** Under A-prime it performs the first-writer
arbitration that keeps one canonical physical placement per `(org_id, block_id)`
while preserving org-global deduplication. Keeping it is a legitimate outcome if
measurement shows the cost is acceptable. Any removal must first supply an
equivalent global arbiter without changing A-prime semantics.

See [registry X4 design notes](./UPLOAD-FENCE-FINDINGS-REGISTRY.md)
and the [X1/X4 characterization](./UPLOAD-PAXOS-HOT-PATH-X1-CHARACTERIZATION.md).

---

### ISSUE-CANONICAL-READ-FANOUT-01: Canonical read fan-out unvalidated

**Status**: 🟡 Open
**Severity**: Medium (perf / capacity)
**Affected**: canonical block read path
**Source of record**: registry **X5**

#### Problem

One Cassandra point read per unique block before the first byte. Existing
benchmark substitutes an in-memory function for Cassandra.

#### Fix Direction

Validate against a real cluster (driver, pool, latency, load) before claiming
hot-path readiness.

---

### ISSUE-IWORK-PREVIEW-413-NO-MESSAGE-01: An oversized iWork preview shows raw JSON in the viewer

**Status**: 🟡 **Open — user-visible, frontend only**
**Severity**: Low — no data or capacity impact; a bad error experience on one preview branch
**Affected**: `frontend/src/components/dialog/file-preview-dialog.js`, the `preview=1` branch of `GET /repo/:repo_id/raw/*filepath`

#### What Is True Today

D6 capped the iWork *source* document at `fileview.max_iwork_source_bytes`
(32 MiB shipped), because that preview branch buffers the whole document and
costs ~6x its size when the library is encrypted. A `.key`, `.pages` or
`.numbers` above the cap now answers `413` with a JSON body where it previously
rendered a preview. The cap itself is deliberate and bounded — it applies only
to the buffering branch, so downloading the same file still works, and
`ServeHistoricFileRaw` passes `false` explicitly.

The frontend has no branch for that response. `file-preview-dialog.js` renders
`<img src={previewURL}>`; the `413` fails the image load, `onError` sets
`iworkPreviewType: 'pdf'`, and the re-render puts the *same URL* in an
`<iframe>`. So the request is made twice and the viewer ends up displaying the
raw JSON error body — untranslated, inside the modal:

```json
{"error":"file too large for inline preview (… bytes, max 33554432)"}
```

The image→PDF fallback exists for older iWork documents that carry a QuickLook
PDF instead of a JPEG. A `413` is not that case, so the fallback fires for a
reason it was not written for.

#### Why It Is Contained

The `413` is emitted *before* `acquireFileViewDownloadAdmission`
(`internal/api/v2/fileview.go`), so neither request consumes a download
admission slot. The doubled request costs a round trip, not capacity.

#### Fix Direction

Frontend only. It needs a small restructure rather than a guard on the existing
path: `<img onError>` reports *that* the load failed, never the HTTP status, so
there is no way to tell a `413` from a missing QuickLook preview from inside the
current fallback. The preview has to be fetched explicitly.

Request the preview URL once with `fetch`, then branch on `res.status`: on `413`
render a translated message naming the limit and pointing at download — something
like "this document is too large to preview (max 32 MiB); download it to open
it". The backend already returns the limit in the JSON body, so no API change is
needed.

**The image→PDF fallback must survive the change.** A `200` does not by itself
say whether the payload is a QuickLook JPEG or a QuickLook PDF, which is the
distinction that fallback exists to make, so removing it would break older iWork
documents. Keep it and feed it the response already in hand:

```text
fetch(previewURL) once
  ├─ 413 → translated "too large to preview" message
  └─ 200 → url = URL.createObjectURL(blob)
             ├─ <img src={url}>
             └─ onError → <iframe src={url}>   // same object URL, no refetch
```

Branching on the response's `Content-Type` instead would remove the second
render entirely, but only if the backend is verified to label both payloads
correctly on this route — worth checking before relying on it, and the object-URL
form above is correct either way. Both shapes keep the request count at one,
which the current code does not: today the fallback is *how* a failure is
discovered, so every oversized document is fetched twice.

Two adjacent decisions are worth taking at the same time, and both are product
calls rather than defects:

- **Raise the cap.** `docs/DEPLOY.md` carries the measured trade-off; at the
  2 GiB reference budget, moving the source cap from 32 MiB to 64 MiB takes a
  node from 16 concurrent downloads to 9.
- **Give the preview its own admission profile.** Ordinary raw streaming costs
  ~4 MiB plaintext, or up to 72 MiB under the encrypted design at the accepted
  16 MiB block size, while *each* shipped raw slot is budgeted at 192 MiB for
  the iWork preview worst case — a branch only `preview=1` on three extensions
  can reach. The four raw slots therefore account for 768 MiB, about 47% of the
  1632 MiB modeled baseline. A separate profile with one or two slots would
  return raw slots to their real cost and allow larger previews. It is not free:
  §12 of `docs/SEAFHTTP-DOWNLOAD-ADMISSION-D0.md` freezes the profile enum, its
  metric label set and the config schema, so this needs an explicit amendment to
  that contract rather than an implementation change.

A third option avoids the memory cost entirely: buffer the source to a temporary
file and read the ZIP through `io.ReaderAt`. The archive needs random access,
not residency. More invasive, and it moves the cost to disk.

**Source of record**: D6 closure, `ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01`

---

### ISSUE-DOWNLOAD-BYTE-RATE-SHAPING-01: Download concurrency does not cap byte rate

**Status**: 🟡 **Open — quantified by D6, not closed by it**
**Severity**: Medium (capacity hardening), not a D correctness blocker by itself

**D6 measurement (2026-08-03).** `TestDownloadAdmissionEgressScalesWithConcurrency`
measured one transfer at 356 MiB/s and six concurrent at 1699 MiB/s aggregate —
**4.8× the single-transfer rate at a six-way budget**. Near-linear scaling is the
residual demonstrated rather than asserted: admission bounds how many transfers
run, nothing bounds how fast each one runs, so a node's egress ceiling is
`max_active_per_node × per-transfer throughput`. Those absolute figures are
loopback numbers from a container stack and must be re-measured on production
hardware before the concurrency cap is read as an egress budget; the scaling
relationship is what holds regardless of hardware.
**Affected**: process/node egress for file, raw, history, ZIP, share and block
downloads
**Source of record**: D0 contract in
`docs/SEAFHTTP-DOWNLOAD-ADMISSION-D0.md`

#### Problem

Subcontract D bounds accepted concurrent transfers, storage readers, response
writers and aggregate work. It does not by itself bound bytes per second. One
admitted transfer can consume most or all of a node's available egress, and a
generous node concurrency cap can still create an unacceptable bandwidth
plateau.

This residual is intentionally not hidden inside D's closure claim. D6 must
measure aggregate throughput, per-transfer throughput, duration and the node's
egress budget under the measured D defaults. If the result is unacceptable,
implement byte-rate shaping or an equivalent trusted ingress/network QoS policy.

#### Fix Direction

Do not copy a byte-rate value from B/C or guess one from request counts. Use the
D6 measurements to choose between application shaping, ingress shaping and an
explicit accepted network budget. Keep this issue separate from D's admission
correctness and from the production object-storage privacy issue.

---

### ISSUE-CHECKBLOCKS-WORK-AMPLIFICATION-01: check-blocks work unbounded after parse

**Status**: ✅ **Fixed 2026-07-31** — closed with subcontract C of the rate-limit umbrella
**Severity**: Medium
**Affected**: `SyncHandler.CheckBlocks` / seafhttp check-blocks
**Source of record**: registry **X11**

#### Problem

`maxCheckBlockIDs` (100k) bounds the parser, not the ~100k sequential Cassandra
reads an accepted request can trigger.

#### Resolution

The reads are now deduplicated, issued at a configured fan-out rather than
serially, and cancellable: `db.GetBlockIDMappingContext` replaces the contextless
read whose loop a client disconnect could not stop. An admission gate bounds how
many such requests run at once, so the node's exposure is the product
(`check_blocks_max_inflight_per_node` × `check_blocks_lookup_fanout`) rather than
unbounded, and an admitted lifetime bounds each one in time. The accepted
cardinality is now configuration capped at its inherited 100k, with
`sync_check_blocks_ids_per_request` as the evidence for lowering it later. Full
detail, closure criteria and what it does not claim are in the "Subcontract C"
section of `ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01`.

---

### ISSUE-READ-AFTER-WRITE-CROSS-DC-01: Read-after-write across DCs

**Status**: 🟡 Open
**Severity**: Medium (availability)
**Affected**: canonical lookups after remote upload
**Source of record**: registry **X6**

#### Problem

Missing-row retry is 3×25 ms (local lag). Cross-DC visibility can yield
transient 404/503, false-missing `check-blocks`, needless re-uploads. Safe
(fail-closed) but an availability dependency. Related to X2's consistency model.

#### Fix Direction

Document as expected under RF-1-per-DC, or extend retry / use a stronger read
for hot post-upload paths. Multi-DC tests still missing.

---

### ISSUE-LIBRARY-CLASS-CHANGE-RESIDENCY-01: strict residency is not enforced through the full placement lifecycle

**Status**: 🟡 Partial — endpoint preference validation improved 2026-08-22; concurrency, runtime placement, failover and historical content remain open
**Severity**: Medium (policy / compliance)
**Affected**: `LibraryHandler.ChangeStorageClass`; organization storage-policy updates; request-time placement in v2, Sync and SeafHTTP; storage-manager failover; canonical reuse and future migration semantics
**Source of record**: [storage-class placement options](./STORAGE-CLASS-PLACEMENT-OPTIONS.md) - "Residency policy, failover and preference changes"

#### Problem

Library creation enforces the organization's residency policy through
`resolveCreateStorageClassForOrg` (`internal/api/v2/storage_policy.go`): under
`data_residency: strict` the requested class must be configured, hot-tier and
mapped to the organization's `default_region`.

`ChangeStorageClass` originally did not repeat that check. It validated only
`isKnownStorageClass(req.StorageClass)`, allowing an existing library preference
to bypass both region and tier constraints.

**Fixed 2026-08-22:** the handler now reads the current organization policy and
calls `validateMutableStorageClass`. Every policy rejects cold primary placement;
under `strict`, the requested hot class must map to `default_region`. Canonical and
administrative projections are then updated in the same logged batch. Focused tests
cover same-region hot acceptance plus cross-region and cold rejection.

**The endpoint validation is not a concurrency fence.** The policy read happens
before the library write and Cassandra's logged batch does not isolate that prior
read. A concurrent `flexible -> strict` transition can therefore commit between
validation and the preference update, leaving a value that the new policy would
reject. This is not a regression from `main`, where the endpoint did not consult
policy, but it limits the claim to best-effort request validation.

**Original tier defect.** Creation routes the requested class
through `validateRequestedCreateStorageClass`, which rejects a cold class with
`storage class must use hot tier` via `isHotStorageClass`. `isKnownStorageClass`
performs no tier check, so `ChangeStorageClass` accepts a configured cold class
that creation would have refused. Both halves of the create-time contract —
region and tier — are missing from the change path.

**Scope of the cold half, stated precisely so it is not over-read.** Cold storage
is nominal in the current tree, not implemented:

- `initStorageClass` maps `tier: cold` to `storage.AccessDelayed`
  (`internal/api/server.go`), but nothing acts on that value. `accessType` is read
  only by `GetAccessType` and by the guard inside `S3Store.InitiateRestore`
  (`internal/storage/s3.go`).
- No PUT path sets an S3 storage class. `PutObjectInput` never carries
  `StorageClass`, so a class labelled cold writes ordinary objects into an
  ordinary bucket and they stay immediately readable.
- The restore API is a stub: `RestoreHandler.InitiateRestore`
  (`internal/api/v2/restore.go`) carries four TODOs, records an empty
  `BlockIDs`, never calls the storage layer, and returns a placeholder job.
- `Manager.GetHotBackends` / `GetColdBackends` are inventory helpers with no
  caller in the read or write path.

Pointing a library at a cold class would not currently make its future blocks
subject to retrieval delay. Creation and preference changes now both reject that
unsupported primary-placement mode; cold-tier primary placement remains future
design work.

The fix is a placement-policy gate, not data movement: existing canonical blocks
keep their persisted `blocks.storage_class`, physical object and derived key. A
preference persisted before a later `flexible -> strict` transition can still be
out of region and the materialization resolver still honors it without consulting
the policy. The same preference-first pattern exists in the v2, Sync and SeafHTTP
materialization paths, so strict residency must be enforced where each new
destination is selected rather than inferred from this endpoint check.

A second, narrower path to the same outcome is separately noted: strict policy does
not constrain `failover_class` either, so a new block can be persisted outside the
allowed region when the preferred class is already marked `Unhealthy` or `Failed`.
That path is currently unreachable at runtime - `CheckHealth` holds the marking
logic but has no automatic caller, so no class is ever marked unhealthy in a
running server. Note that
automating health monitoring would make the failover path live, so it hardens
availability and weakens residency at the same time; the two must be decided
together.

**Authorization fixed 2026-08-22:** canonical owners may use session or
`read-write` API-key credentials; organization owner/admin/superadmin overrides
require session or `admin` API-key credentials. Content shares and repo API tokens
cannot change library configuration.

#### Fix Direction

**Decided semantics (2026-08-21):** `strict` constrains placement, not only the
create-time default. It is a promise about **where future bytes land**, and it is
enforced by region, not by forbidding the operation:

```text
flexible:
    library.storage_class may change to any configured hot class.
    cross-region failover permitted per configuration.

strict(region = R):
    library.storage_class may change, but only to a hot class in R.
        a request naming a class outside R is rejected.

    new materialization:
        preferred class in R
        -> same-region failover allowed
        -> cross-region failover forbidden
        -> no healthy backend in R: fail closed, do not place outside R

    existing canonical blocks:
        never reinterpreted or moved automatically.
```

An earlier draft of this entry made `ChangeStorageClass` a `flexible`-only
operation. That was rejected as more restrictive than residency requires: moving a
library from one in-region hot class to another in-region hot class breaks no
residency promise, and forbidding it removes a legitimate feature to buy nothing.

**Multiple hot classes in one logical region remain inexpressible.** `storageClassRegion` attributes
a region to a class only by finding it as the `hot` or `cold` entry of some
`region_classes` mapping, and `RegionClassConfig` holds exactly one `Hot` and one
`Cold` string per region. A second hot class in the same region therefore resolves
to no region at all, so the strict check would reject `hot-eu-a -> hot-eu-b` today
rather than allow it. Implementing this rule as written requires the region
attribution to admit more than one hot class per region.

**The class's own `region` field cannot supply that attribution.** An earlier draft
of this entry offered it as an alternative; it is not one. `StorageClassConfig.Region`
is the provider region — `internal/config/config.go` documents it as "AWS region" —
and every shipped profile uses it as such, against logical region names that are not
the same strings. `configs/config.prod.yaml` keys `region_classes` on `na`/`eu`/`asia`
while the corresponding classes carry `region: us-east-1`, `eu-west-1` and
`ap-southeast-1`; `configs/config.example.yaml` and `configs/config.docker.yaml` key
on `usa`/`eu`/`china` against `us-east-1`, `eu-west-1` and `cn-north-1`. Residency is
asserted over the logical name, so deriving it from the provider field would compare
the wrong domain — and would also collapse distinct logical regions that happen to
share a provider region.

The decisive case is `cold-glacier-usa`: in every profile that defines a cold class
at all (`config.example.yaml`, `config.docker.yaml` and the four regional/cluster
profiles) it is the `cold` entry of **both** `usa` and `eu`. Class-to-logical-region
is therefore not even a function today, so no per-class scalar field can encode it.
The workable shapes are `region_classes` gaining a membership list
(`eu: {hot: [hot-eu-a, hot-eu-b]}`), or a new explicit logical field on the class
(`placement_region: eu`) kept separate from the provider `region` — and as long as one
class may serve two logical regions, the membership list is the honest model. That change
belongs with the rule, not after it.

Implementation status and remaining shape:

1. **Implemented:** read the organization's policy with `readOrgStoragePolicy`. Under
   `orgDataResidencyStrict`, require the requested class to resolve to
   `policy.DefaultRegion` — the check `resolveStrictCreateStorageClass` already
   performs with `storageClassRegion`.
2. **Implemented:** route the mutation through `validateMutableStorageClass`, which
   restores the hot-tier requirement for flexible and strict policy and the region
   requirement for strict policy.
3. **Implemented:** focused tests prove strict accepts an in-region hot class, rejects an
   out-of-region one and rejects a cold one; flexible accepts any hot class and
   rejects a cold one.

4. **Open:** give the placement resolver the policy as well. Gating the endpoint alone
   leaves a library whose stored class is already out of region — from before the
   switch to `strict`, or from a `flexible` period — materializing new blocks
   outside it, because `ResolveStorageClass` honours `library.storage_class`
   unconditionally.

**The failover half is now decided too, because it follows from the same promise.**
A block belonging to a strict organization must never fail over to a class outside
the allowed region. `GetHealthyBackend` must therefore receive the policy, restrict
the walk to in-region classes, and return an error rather than crossing the region
when the in-region chain is exhausted. Saying "strict constrains placement" while
leaving failover free to cross the region would make the promise void. This is
latent today only because nothing calls `CheckHealth` automatically; it becomes
live with the health checker, so the two must land together.

**Still undecided: the transition into strict.** Neither this rule nor the failover
rule makes historical content resident. `admin.go` updates an organization's
`storage_config` after `validateOrgStoragePolicy`, which checks only that a strict
policy carries a `default_region` and that the region has a configured hot class.
It never inspects existing libraries or blocks, so an organization can flip
`flexible -> strict`, or move its `default_region`, while its canonical blocks sit
elsewhere. Three candidate rules, none chosen:

1. **Reject the transition** while any library or canonical block resolves outside
   the target region. Strongest, and needs an enumeration the schema does not make
   cheap.
2. **Accept and require migration**, tracking the organization as non-compliant
   until an explicit migration job moves the referenced bytes. Depends on the
   migration feature that is still a TODO.
3. **Define strict as forward-only** — it governs materializations after the
   policy takes effect and makes no claim about content already placed. Weakest
   promise and cheapest, but still **not** what the code does today.

**None of the three transition semantics is implemented, including option 3.** An earlier draft of this
entry said option 3 was "what the code does by omission". That is wrong. Forward-only
would require materialization to consult the policy, and no placement resolver does:
`Manager.ResolveStorageClass` returns `library.storage_class` first and never falls
back or re-checks (`internal/storage/storage.go`), and the request chain that feeds
it — `resolveLibraryBlockStoreForRequestContext` ->
`lookupLibraryStorageClassContextFn` -> `resolvePreferredLibraryStorageClassForRequest`
(`internal/api/v2/storage_resolution.go`) — never reads the organization's policy.

What the code implements is narrower than any of the three: `strict` gates the
class chosen at library creation and a later explicit preference change, but it
does not re-check an already-persisted preference when policy itself changes. A
library created while the organization was `flexible`, holding an out-of-region
class, keeps materializing new blocks into that class after the switch to `strict`.

Two consequences for the fix:

- Gating `ChangeStorageClass` is **not sufficient** even for option 3. The
  placement resolver has to receive the policy too, so a stored out-of-region class
  cannot keep placing new blocks outside the region. That is the same plumbing the
  decided failover rule needs, and it should land as one change.
- The decision still has to be made and stated wherever `strict` is described to a
  customer, because "strict data residency" reads as option 1. This connects the
  commercial meaning of `strict` to A-prime and to the future chargeable migration;
  do not let it be settled by default — and do not describe the current behaviour as
  forward-only, because it is weaker than that.

**The same undecided question governs deduplicated reuse, which is where `strict`
and A-prime actually meet.** Everything above concerns where a *new* object lands.
Reuse never writes a new object, and under A-prime that is deliberate: a block is
canonical per `(org, hash)`, and the placement report states the consequence as
designed behaviour rather than a defect — "a library that now prefers EU reuses a
hash already canonical in NA on purpose"
([STORAGE-CLASS-PLACEMENT-OPTIONS.md](./STORAGE-CLASS-PLACEMENT-OPTIONS.md)). So an
organization that flips to `strict(EU)` and then uploads content whose hash is
already canonical in NA takes the `Reusable` branch: `ProbeBlockReuse` returns the
canonical class, `ResolveCanonicalBlockStore` resolves NA, and the upload succeeds
with **no new bytes outside EU** while the content a strict library now references
keeps living in NA. Gating `ChangeStorageClass`, gating the placement resolver and
gating failover do not touch this path at all — the reuse decision is read from
`blocks.storage_class`, never from the policy.

Each candidate rule therefore has to state its reuse semantics explicitly:

1. **Reject the transition** — the enumeration this option needs *is* the set of
   canonical blocks the organization references, so it already answers the reuse
   question: after a transition, nothing referenced sits outside the region.
2. **Accept and require migration** — reuse stays allowed while the organization is
   marked non-compliant, and the migration job is what eventually makes referenced
   content resident.
3. **Forward-only** — reuse stays allowed indefinitely. The promise is "new bytes
   land in R", explicitly not "referenced bytes live in R".

There is a fourth position that must not be adopted silently: **residency of
referenced content**, where a strict library may not publish a reference to
canonical bytes outside R until they are replicated or migrated into R. It is the
reading a customer is most likely to give `data_residency: strict`, it is the most
expensive, and it is in direct tension with org-global deduplication as A-prime
defines it — one of the two has to give. Whichever is chosen belongs next to the
transition rule wherever `strict` is described commercially.

Do not couple this to block migration. Changing the preference and moving bytes are
separate operations; the migration TODO in the same handler stays out of scope.

---

### ISSUE-LIBRARY-MUTATION-NO-PERMISSION-CHECK-01: Three library mutation handlers have no permission gate

**Status**: ✅ **Fixed 2026-08-22** — gated on `LibraryHandler.requireLibraryConfigAuthority`, negative tests landed
**Severity**: High (authorization / configuration integrity)
**Affected**: `UpdateLibrary`, `LibraryOperation` → `RenameLibrary`, `ChangeStorageClass` (`internal/api/v2/libraries.go`)

#### Problem

Library routes are registered on a group whose only middleware is
`authMiddleware` (`internal/api/server_routes.go`, `protected.Use(s.authMiddleware())`),
under both the `/api/v2` and `/api2` prefixes. This project's convention is that
library-level authorization is checked **inside** each handler, and most handlers
do exactly that:

| Handler | Route | Gate |
|---|---|---|
| `GetLibrary` | `GET /:repo_id` | `GetLibraryPermission` → 403 |
| `DeleteLibrary` | `DELETE /:repo_id` | `IsLibraryOwner` → 403 |
| `UpdateLibrary` | `PUT /:repo_id` | **none** |
| `LibraryOperation` (`op=rename`) | `POST /:repo_id` | **none** |
| `ChangeStorageClass` | `POST /:repo_id/storage-class` | **none** |

The three unguarded handlers never read `user_id` and never call
`h.permMiddleware`. Their only library-scoped read is
`readLiveLibraryStateFn(session, orgID, repoID)` → `db.ReadLiveLibraryState`,
which is an existence/liveness check partitioned by `(org_id, library_id)`. It
blocks cross-organization access; it does not establish that the caller may
mutate this library. The `libraries` writes are scoped `WHERE org_id = ? AND
library_id = ?`, and the `libraries_by_id` companion write is keyed by
`library_id` alone; no statement in any of the three carries an owner predicate.

Consequence: any authenticated member of an organization can mutate any library
in that organization, including libraries they have no permission to read.

- `UpdateLibrary` changes `name`, `description` and `version_ttl_days`. The last
  one is written through `AddUpsertLibraryPolicyQuery` /
  `AddDeleteLibraryPolicyQuery`, so an unauthorized caller can shorten another
  library's version retention.
- `RenameLibrary` changes `name` in `libraries` and `libraries_by_id`.
- `ChangeStorageClass` changes the library's future-placement preference; the
  effect on residency policy is tracked separately as
  `ISSUE-LIBRARY-CLASS-CHANGE-RESIDENCY-01`.

Existing canonical blocks are not moved or reinterpreted by any of the three, so
this is a configuration-integrity and authorization defect, not data loss.

#### Additional evidence: this is a route surface, not three stray functions

`RegisterLibraryRoutesWithToken` constructs a `PermissionMiddleware` and hands it
to the handler, but applies it to **no** route in the `repos` group. The affected
handlers are reachable through **five** registrations — `PUT /:repo_id`,
`PUT /:repo_id/`, `POST /:repo_id`, `POST /:repo_id/` and
`POST /:repo_id/storage-class` — and the group is registered under both the
`/api/v2` and `/api2` prefixes. That shaped the fix: the gate had to live in the
handler, because a per-route middleware would have to be repeated five times and
the sixth registration would forget it.

#### Fix (2026-08-22)

One shared gate, `LibraryHandler.requireLibraryConfigAuthority`, called by all
three handlers immediately after the live-library check:

- **Canonical owner:** session credentials or an API key with at least
  `read-write` scope.
- **Organization owner/admin/superadmin override:** session credentials or an API
  key with `admin` scope. The role is read explicitly instead of inferred from an
  aggregated library permission.
- **Content shares are insufficient:** even `rw` or `admin` share labels decide
  what is *in* a library, not what it is called, how long its versions are kept,
  or where its future blocks are placed.
- **Repo API tokens are refused outright**, before the permission lookup. Such a
  token is a content credential scoped to one library and mints at most `rw`;
  letting the minting user's own permissions answer for it would hand the token a
  reach it was never issued.
- **Empty `user_id` fails closed** rather than resolving permissions for `""`.
- **Organization-role lookup errors return 500**, never a silent allow.

Ordered after the liveness check to match `DeleteLibrary`. That leaves the same
within-org existence signal `DeleteLibrary` already accepts (an unauthorized
caller can still distinguish a live library in their own org from a deleted one);
closing it is a separate decision that should move all four handlers at once
rather than leave the pair inconsistent.

#### Verification

`internal/api/v2/library_mutation_authority_test.go` runs every case against all
three handlers, driving rename through `LibraryOperation` the way the route
actually dispatches:

- content-share callers without an administrative org role → 403
- canonical owner session/`read-write` and org-admin session/`admin` → allowed
- owner `read` and org-admin `read-write` API keys → 403
- missing `user_id` → 403
- repo API token → 403
- organization-role lookup error → 500
- `DeleteLibrary` and library settings carry explicit scope matrices

Confirmed red before the fix: with the three gate call sites removed, the `rw`
case fails and the handler proceeds into the database.

---

### ISSUE-GC-MANUAL-TRIGGER-NOT-GATED-01: Manual GC trigger APIs were not gated on `GC.Enabled`

**Status**: ✅ **Fixed 2026-08-22** — manual triggers gated on `Service.ManualTriggerError`, DLQ lifecycle gate and regressions landed
**Severity**: Low as shipped (never a live bypass), but it protected the barrier the entire X1 programme runs behind
**Affected**: `gc.Service.TriggerWorker`, `gc.Service.TriggerScanner`, `Server.handleGCRun`

#### Problem

`GC_ENABLED=false` is the kill switch that keeps destructive GC off while X1 is
open. It is pinned in `docker-compose.prod.yml` and defaults to false in
`config.DefaultConfig`. Re-verifying it after PR #181 surfaced that the *runtime*
surface was never checked, only the config surface.

Three facts together:

1. `NewServer` constructs `gcService` unconditionally — the constructor is guarded
   by `database != nil`, not by `cfg.GC.Enabled`. A node with GC disabled still
   has a live `*gc.Service`.
2. The superadmin route `POST /api/v2.1/admin/gc/run` is registered
   unconditionally and reaches that service.
3. `TriggerWorker` and `TriggerScanner` checked neither `config.Enabled` nor
   `started`. They only wrote to a size-1 buffered channel.

What made it safe in practice is that `Service.Start` returns early when
`!s.config.Enabled`, before launching `runWorkerLoop` and `runScannerLoop`, so no
goroutine existed to consume the token.

**That is the problem.** The kill switch rested on "a disabled service happens to
have no consumer for this channel" — an emergent property of `Start`'s control
flow — rather than on a stated invariant at the point of decision. Any future
refactor that launched those loops unconditionally (or started them before the
`Enabled` check, or added a second consumer) would have silently promoted the
admin endpoint into a live bypass of `GC_ENABLED=false`, with no test failing.

This matters more than the severity suggests because of the production posture:
**the current readiness posture keeps `GC_ENABLED=false` on every replica in every
datacenter** and still serves this endpoint. The disabled fleet was exactly
the population relying on the emergent guarantee.

Two smaller defects fell out of the same gap:

- The endpoint answered `{"started": true, "message": "GC worker triggered"}` on
  a disabled node, for a run that no goroutine existed to perform. An operator
  could not tell a triggered run from a silently discarded one.
- The token was left parked in the buffered channel. Had GC later been enabled on
  that process, the first loop iteration would have consumed it and performed one
  run nobody asked for.

#### Wider than "manual triggers" — the DLQ path too

Review of the first fix found `Service.DeleteFailedItem` and
`Service.RequeueFailedItem` carrying the same gap with a worse consequence. Both
call `tryClaimLeadershipForAdmin`, which does not merely check leadership — it
**claims the lease**. A superadmin acting on any GC-disabled replica could
therefore take GC leadership away from the one datacenter that actually drains
the queue, causing a GC outage from a node incapable of doing the work. They now
refuse with `ErrGCDisabled` before claiming.

So the real defect was never "manual triggers are ungated"; it was **"the kill
switch is honoured on some superadmin GC surfaces and not others"**.

One kill switch, but **two predicates**, because the surfaces differ in kind:

| Surface | Predicate | Why |
|---|---|---|
| Manual triggers (async) | `Enabled && started && leader` | A follower's loop would consume the token and return without doing work |
| DLQ requeue/delete (sync) | `Enabled && started` | The store work is inline and can claim leadership itself, but a stopped or never-started node must not reclaim the lease during shutdown |

Collapsing these into one predicate is still a live trap: DLQ does not require
current leadership, while a manual trigger does. Lifecycle tests pin the active
requirement for both surfaces.

#### Fix (2026-08-22)

`Service.ManualTriggerError` (exported to the API layer) gates manual triggers on
active lifecycle and current leadership; `Service.gcAdminMutationError` gates
the DLQ mutations on active lifecycle without requiring current leadership.
`TriggerWorker` and `TriggerScanner` now return a `bool`, so a refused trigger is
a value the caller must handle rather than a silent no-op.
`DeleteFailedItem`/`RequeueFailedItem` refuse with `ErrGCDisabled` or
`ErrGCNotRunning` **before** `tryClaimLeadershipForAdmin`, so a disabled or
stopped replica cannot take the lease. Both DLQ HTTP handlers map those lifecycle
errors to `503` alongside `ErrNotLeader`: neither is a server fault. A context
error maps to `503` for the same reason (corrected 2026-08-23, it previously
answered `500`): the store binds `ctx` to the read phase and re-checks it
immediately before the commit, so **if** the call returns a context error it did
not reach its commit point and wrote nothing. The converse does not hold — a
cancellation arriving after that last check is deliberately ignored and the call
returns the batch's own definite outcome — which is exactly why the mapping is
safe: `503` is never answered for a mutation that may have applied.

`handleGCRun` checks it up front and answers `503` with `{"started": false}` —
**before** applying the optional `dry_run` override, so a disabled node's admin
surface is inert end to end rather than accepting a config mutation for a service
that will not run. On an active node, admission and the optional override commit
together; a refused trigger does not change the runtime mode.

**The predicate must not take `s.mu`.** The first version read
`config.Enabled && started` under the mutex and reintroduced a shutdown deadlock:
`Stop()` holds `s.mu` across `s.wg.Wait()`, and `runScannerOnce` — running in a
goroutine that `Wait` is waiting for — calls `TriggerWorker` after auto-requeuing
recoverable failed items. Stop waited for the scanner; the scanner waited for
Stop's lock; `Server.Shutdown` hung. It now reads an `atomic.Bool` written only by
`Start`/`Stop`. `config.Enabled` is set once in `NewService` and never mutated
(`SetDryRun` touches `DryRun` only), so one flag captures both conditions.

`Start()` and `Stop()` drain the trigger channels under `triggerMu`, which also
serializes the check-and-send path. A trigger that is already admitted is
drained before shutdown or restart; a trigger that arrives after the lifecycle
flag closes is refused. That makes "no unrequested run fires at enable time" an
invariant without holding a mutex across `wg.Wait()`.

#### Verification

`internal/gc/manual_trigger_gate_test.go`:

- disabled service → `AcceptsManualTriggers` false, both triggers refused, **and
  both buffered channels asserted empty** (the parked-token regression)
- enabled but not started → refused
- enabled and started → accepted
- after `Stop()` → refused
- nil service → refused
- **`Stop()` concurrent with a trigger completes** — the deadlock regression,
  verified red against the mutex version (hangs the full 15s budget) and green
  against the atomic
- `Start()` discards a stale token left by a trigger that raced `Stop()`
- DLQ requeue/delete on a disabled service → `ErrGCDisabled`
- DLQ requeue/delete on an **enabled but unstarted** service → `ErrGCNotRunning`
- DLQ requeue/delete after `Stop()` → `ErrGCNotRunning` without a lease claim
- manual triggers on an active follower → `ErrNotLeader` and no queued token

`internal/api/gc_run_gate_test.go` pins the HTTP contract, which is where an
operator actually meets this: disabled node → `503` with `started:false` for
worker, scanner, defaulted and unparseable bodies; a `dry_run` override on a
disabled node **does not commit the runtime override**; enabled+started → `200` with
`started:true`; no service → `503`; and a cancelled request context on either DLQ
mutation → `503`, not `500`.

#### Why this is not `ISSUE-GC-UPLOAD-FENCE-REMATERIALIZATION-01`

X1 is about whether destructive GC is *correct* when enabled. This is about
whether "disabled" is enforced where it is decided. They are independent, and
this fix is not progress against any of X1's four closure criteria.

---

### ISSUE-APIKEY-READ-SCOPE-UPLOADLINK-FILESHARE-01: Read-scoped API keys can perform upload-link and file-share mutations

**Status**: 🔴 Open — verified preexisting 2026-08-22
**Severity**: High (authorization / least-privilege bypass)
**Affected**: `UploadLinkHandler.CreateUploadLink`, `UpdateUploadLink`,
`DeleteUploadLink`; `FileShareHandler.CreateShare`, `UpdateSharePermission`,
`DeleteShare`

#### Problem

API-key scope is the authority of the current credential. Bare
`PermissionMiddleware.HasLibraryAccess` instead resolves the underlying user's
authority from Cassandra and cannot inspect request context. The affected upload
and share handlers ask only the second question, or compare only
`created_by == user_id`, without applying the credential ceiling.

`HasLibraryAccessCtx` applies that ceiling through the middleware's private scope
predicate; bare `HasLibraryAccess` does not. Creator identity likewise proves who
owns an existing link, not whether the current credential is allowed to mutate it.

This defect predates the 2026-08-22 readiness branch. That branch applies explicit
scope checks to the bounded library-configuration fixes but does not claim to
repair unrelated existing routes.

#### Affected Paths

- Upload-link creation requests `PermissionRW` through bare
  `HasLibraryAccess`.
- Upload-link update and delete use creator-only checks.
- File-share create, update and delete request `PermissionAdmin` through bare
  `HasLibraryAccess`.
- `RequirePermFlagForRepo` does not replace the missing API-key scope ceiling.
- Direct API keys and sessions derived through `/api2/auth-token/` are affected;
  both preserve `api_key_scope`, but these handlers do not consume it.

#### Impact

A `read` key belonging to a user with stronger canonical authority can exceed its
declared scope. An owner/admin user's read key can delegate, alter or revoke user
or group access, and a read key can manage upload links created by that user. This
violates the advertised `read` / `read-write` / `admin` contract.

#### Fix Direction

- Apply the context-aware API-key scope ceiling before every mutation.
- Require at least `read-write` for upload-link create, update and delete.
- Require `admin` for user/group share create, update and delete.
- Keep library authority and creator ownership checks; scope is an additional
  credential ceiling, not a replacement.
- Decide explicitly whether repo API tokens may create upload links rather than
  acquiring that behavior accidentally through a helper substitution.
- Add direct-key and derived-session scope matrices for all six handlers.

#### Verification Needed

- `read` is denied on all six mutation handlers.
- `read-write` is allowed only on upload-link operations for an otherwise
  authorized caller.
- `admin` is required for file-share administration.
- Ordinary session behavior remains unchanged.
- Revoked and expired derived credentials retain their existing failure behavior.

---

### ISSUE-GC-DRYRUN-OVERRIDE-STICKY-01: A manual GC run can lower `GC_DRY_RUN` for the rest of the process's life

**Status**: 🔴 Open — verified preexisting 2026-08-23 (not introduced by the 2026-08-22 readiness branch)
**Severity**: Medium — currently unreachable in production, becomes live exactly when destructive GC is activated
**Affected**: `Server.handleGCRun`, `gc.Service.TriggerWorkerWithDryRun`, `gc.Service.TriggerScannerWithDryRun`, `gc.Service.SetDryRun`

#### Problem

Destructive GC sits behind a two-rung ladder: `GC_ENABLED` decides whether GC runs
at all, and `GC_DRY_RUN` decides whether a running GC deletes anything. After the
2026-08-22 hardening the two rungs have very different strength.

`GC_ENABLED` is read once in `NewService`, is never mutated at runtime, and is now
enforced at the decision point of every superadmin GC surface
(`ISSUE-GC-MANUAL-TRIGGER-NOT-GATED-01`). No API call can raise it.

`GC_DRY_RUN` is different. `POST /api/v2.1/admin/gc/run` accepts an optional
`dry_run` boolean, and an **accepted** trigger stores it into `Service.dryRun` and
`Worker.dryRun`. That value is not scoped to the run it accompanied: it replaces
the runtime mode for the remaining life of the process. Nothing restores the
configured value — `SetDryRun` has no production caller, and the only ways back are
another `/gc/run` call carrying the opposite value or a process restart. The change
is visible only as the `dry_run` field of `GET /api/v2.1/admin/gc/status`, and it
is not written to the audit log.

So a configured `GC_DRY_RUN=true` — the safety rung directly below the one the
whole X1 programme runs behind — can be lowered by one superadmin request and stays
lowered. The reverse is equally sticky: a single `{"dry_run": true}` parks that node
in a non-deleting mode indefinitely, so trash never purges and storage grows with
no error raised anywhere.

The override is process-local and not persisted, so it affects only the node that
served the request and does not survive a restart. That bounds the blast radius; it
does not make the surface honest.

#### Why this is not a defect of the 2026-08-22 branch

`main` already behaved this way: `handleGCRun` called `Service.SetDryRun(*req.DryRun)`
with exactly the same lifetime. The readiness branch changed two things and neither
is the lifetime:

- the override now commits **inside** the trigger admission gate, so a *refused*
  trigger can no longer change the mode (that was a real defect, and it is fixed);
- the flag became `atomic.Bool`, closing the data race against a running worker.

The branch's own CHANGELOG states that runtime dry-run semantics remain the global
behavior inherited from `main`. This entry records the surface that inheritance
leaves open, so it is not rediscovered as "new" at activation time.

#### Reachability

Not reachable today: with `GC_ENABLED=false` fleet-wide, `ManualTriggerError`
refuses the trigger **before** the override is applied, and the refusal path is
pinned by `TestHandleGCRun_DisabledNodeDoesNotApplyDryRunOverride`. It becomes
reachable on the designated GC location the moment destructive GC is activated —
which is precisely the window in which `GC_DRY_RUN=true` is the intended brake.

#### Fix Direction

Pick one, in preference order:

1. **Scope the override to its run.** Apply the value with the admitted trigger and
   restore the configured mode when that run completes, so `dry_run` is a property
   of the request rather than of the process.
2. **Treat config as a floor.** Refuse `{"dry_run": false}` while `config.DryRun` is
   true, and require a deliberate configuration change plus restart to lower the
   rung — matching how `GC_ENABLED` behaves.
3. **If a sticky runtime mode is genuinely wanted**, move it to its own explicit
   endpoint with an audit-log entry naming the actor, rather than letting it ride
   along on a "run once" request.

#### Verification Needed

- An accepted trigger carrying `dry_run` does not alter the mode observed by the
  *next* run (option 1), or is refused when it would lower the configured rung
  (option 2).
- `GET /admin/gc/status` reflects the configured mode again after the triggered run
  finishes.
- A mode change, however it is finally expressed, appears in the audit log.
- The existing invariant stays green: a refused trigger never commits an override.

---

## See Also

- [OPEN-WORK-INDEX.md](OPEN-WORK-INDEX.md) - **Start here**: scoped open-work screen + tracker migration table
- [IMPLEMENTATION_STATUS.md](IMPLEMENTATION_STATUS.md) - Component completion status
- [API-REFERENCE.md](API-REFERENCE.md) - API endpoint documentation
- [TECHNICAL-DEBT.md](TECHNICAL-DEBT.md) - Architectural issues
- [CURRENT_WORK.md](../CURRENT_WORK.md) - Active priorities
- [GC-DELETE-CLEANUP-INVESTIGATION.md](GC-DELETE-CLEANUP-INVESTIGATION.md) - Full GC delete-cleanup audit (P1–P10, invariants, branch roadmap; refreshed 2026-07-16 — P10 cross-org deletion fixed through PR-3)

## E1-7 / W2-5 scoped evidence update — 2026-10-04

[RecvFS-first characterization](./E1-7-SYNC-RECVFS-BEFORE-PUTBLOCK.md) is GREEN
for six named direct ordered legs, including pre-existing P1 and productive
COMMITTED/TERMINAL retirement before PutBlock. Early HEAD rejects; PutBlock
creates P2 and publication settles normally. Fresh metadata alone creates no P;
that absence is not extrapolated to reused content. No runtime fix is added.
CLOSED-EVIDENCE applies only to that measured ordered contract. W2-5/E1-05
overall, R31/W2-11..14, W2-10, E1 and X1 remain OPEN; production GC remains OFF.


## E1-09 / W2-9 source-purge publication fix — 2026-10-05

`ISSUE-W2-CROSSREPO-RETIRED-LIFE-PUBLICATION-01`: productive source purge and
GC COMMITTED/TERMINAL reproduce destination HEAD/fs: after P1 retirement in
both copy and move. Fixed by per-attempt source P capture, staged-ID equality
and final exact-P after durable repair, before HEAD. Scope: single file/block,
plaintext, same org/representation pre-HEAD CLOSED-FIX. Directories/multiblock,
multi-DC and post-HEAD/R31 stay OPEN; E1/X1 and production GC OFF unchanged.
Async task progress also had a reproduced data race; fields are now snapshotted
under the TaskStore lock. Move source disappearance after safe destination
publication remains existing partial-operation semantics, not rollback.
[Evidence and limits](./E1-09-CROSS-REPO-PUBLICATION-SAFETY.md).


## ISSUE-CROSSREPO-MOVE-SOURCE-IDENTITY-RACE-01

**Status:** CLOSED-FIX (2026-10-16). **Severity:** P1 / GENERAL follow-up, pre-existing; not introduced by PR #258 and not a blocker of its physical-life publication contract.

**Resolution.** `processSingleItem` now keeps the fs_id of the source entry that the published destination attempt copied. Source removal skips when the current entry at the path has a different fs_id, the same way it already skips a source that is gone. The move still returns success, and the replacement stays in the source. The source storage counter is not decremented, because the writer's own update already accounted for the replacement. A directory whose contents change between copy and removal is also left in place, so the user ends up with both copies rather than losing the concurrent edit. `TestCrossRepoMoveSourceRemovalChecksCopiedIdentity` holds the move after its destination HEAD CAS, replaces the source through the update link and releases it. It was RED on main (200, source emptied, FS-B gone) and is GREEN with the fix. The original finding follows as history.

After destination publication, `processSingleItem` retries source removal against the latest source HEAD, resolves the path again and calls `RemoveEntryFromList` by name. It does not compare the current entry's fs_id with the entry copied by the destination attempt. A concurrent writer can replace `/a.txt` FS-A with FS-B after FS-A was copied; source removal can then delete FS-B while the destination contains only FS-A. Reanchoring to the newer HEAD means the source CAS does not establish copied-object identity. This is confirmed by source inspection; no deterministic runtime reproduction is claimed here.

A separate fix should retain the successfully copied source identity and require current source entry fs_id equality before deletion, returning conflict or an explicitly defined no-op on mismatch. Keep the existing destination-first partial-success semantics separate from that identity check. No fix or closure is included in E1-09.


## ISSUE-FILEFROMBLOCKS-WORKER-FANOUT-01

**Status:** OPEN. **Severity:** P2 / GENERAL follow-up, pre-existing; not introduced by #258 and not a blocker of E1-09.

`verifyManifestBlocks` and `ensureCommitBlockOwnLiveness` in file_from_blocks.go create a goroutine per distinct block before their semaphore admission. A supported manifest can contain 131,072 blocks. Query concurrency is bounded but worker creation is not. PR #258 bounds its source capture and shared final validator only; these two earlier phases remain unchanged. A separate resource-bound fix should admit workers before goroutine creation, preserving cancellation and fail-closed publication outcomes. Confirmed by source inspection; no closure is claimed here.


## ISSUE-RESURRECTION-ADMISSION-DIRECTORY-COST-01

**Status:** OPEN. **Severity:** P2 / CURRENT-RUNTIME performance follow-up, introduced deliberately by E1-17 (2026-10-12). Not a safety issue.

E1-17 made `RestoreTrashItem`, `RevertDirectory` and `RevertDirents` admit a directory only after checking every file in its historical subtree. This closed a real X1 violation. Each file costs about seven reads:
- the library row;
- the SHA-1 mapping;
- the file layout;
- its own permanent `fs:`;
- the canonical block row;
- the `fs:` and exact-P recheck before HEAD.

Files are walked sequentially, and blocks within a file run in parallel (bound 20).

**Baseline.** Before E1-17, `main` already walked the subtree once for the quota delta (`fsEntryDelta` → `collectDirStats`): one `fs_objects` read per node, sequential, on every HEAD attempt. E1-17 therefore takes a directory restore from about one read per file to about eight. Of those, four per block are the admission itself (`fs:` and exact P, at capture and before HEAD). The rest are partly avoidable:
- the `libraries` row is re-read for every file, though it is the same row;
- the layout read duplicates the stats walk;
- the SHA-1 mapping applies only to Sync blocks.

Very large directories may approach request timeouts. This has **not** been measured.

**Next step.** Measure restore latency for large directories. Optimize only if it matters:
- read the library row once per request;
- share the layout read with the stats walk;
- bound file-level concurrency.

Do not weaken the per-file admission or turn a missing object into an empty directory.

## ISSUE-SEAFHTTP-UPLOAD-FINALIZE-LEASE-ERROR-500-01

**Status:** OPEN. **Severity:** P2 / CURRENT-RUNTIME availability follow-up, multi-DC; pre-existing. The measured lease-acquisition failures precede HEAD publication. No X1 safety closure or violation follows from these availability results.

`TestMultiInstanceSeafHTTPUploadWhileMovingNoLostFiles` fails intermittently on a real three-DC fixture, where concurrent SeafHTTP uploads race a same-repo move:

| Run | Result |
|---|---|
| G5 run, 2026-10-02 | FAIL |
| E1-16 full run, 2026-10-10 | FAIL |
| E1-16 isolated `-count=3` | 1 of 3 FAIL |

**Historical observation (2026-10-10).** The client received `500 {"error":"file stored but metadata update failed"}` about 400 ms into finalization. Server evidence places the failure before `commitUploadedFile`, in `acquireSeafHTTPUploadFinalizePermit`: the local permit, or the LWT of `acquireSeafHTTPDistributedUploadFinalizeLeaseWithIntervals`. That function returns any acquisition error unchanged, and `writeSeafHTTPUploadError` maps every unclassified error to the generic 500. The error was not logged, so that run did not identify the exact cause.

**2026-10-16 current reproduction after #305.** The [three-API ledger](./E1-THREE-API-RESIDUALS.md)
records SeafHTTP FAIL 3/3 before edits; one fails in the competing move's HEAD
CAS and two in upload finalization. A temporary diagnostic run captures the
upload lease's native SERIAL CAS WriteTimeout (Received=1, BlockFor=2). Generic
upload error writers now retain the cause in server logs while preserving
client responses and all lease/publication/retry semantics. This fixes missing
diagnostics, not the 500. Original failures and the new availability issue below
remain OPEN; current evidence does not prove every historical failure's cause.
Next step: design and verify an outcome-aware lease-acquisition recovery or retry policy for the captured error. A CAS timeout alone does not establish non-publication or safe ownership; a blanket conversion to 409/503 requires separate outcome and caller-compatibility evidence. The client retry contract keys on the 409 conflict response (`writeSeafHTTPUploadError` CLIENT_CONTRACT comment), not on this 500. Do not change lease semantics without that evidence.


## ISSUE-V2-UPLOAD-HEAD-CAS-TIMEOUT-500-01

**Status:** OPEN (2026-10-16). **Severity:** P2 / CURRENT-RUNTIME availability,
pre-existing; reproduces on main before this diagnostic-only change.
TestConcurrentTwoRegionV2UploadsNoLostCommits fails 2/3 in the three-API baseline
with upload 500s. The [current ledger](./E1-THREE-API-RESIDUALS.md) captures native
SERIAL CAS WriteTimeout during HEAD publication; a separate run also captures a
failed SERIAL confirmation and ErrLibraryHeadPublicationUnknown. Current entries
or content may already be published despite an error response. Do not infer a
negative publication proof from the 500 or blindly retry/rollback. Preserve the
existing unknown-outcome contract and isolate a future correction with its own
public-path RED/GREEN evidence. This PR adds diagnostic visibility only.

## ISSUE-GC-SYNTHETIC-SIBLING-TEARDOWN-01

**Status:** OPEN (2026-10-16). **Severity:** P2 / FOLLOW-UP / GENERAL test hygiene.
The synthetic plain/encrypted-sibling retirement test removes blocks/mappings/
queue rows but leaves its intentionally measured COMMITTED orphan, independent
root/discovery and PUBLISHED lifecycle after teardown. [The ledger](./E1-THREE-API-RESIDUALS.md)
records owned post-run disposal, not automatic cleanup or productive recovery.
A future fixture-only cleanup must preserve the original post-G3 assertions and
use the exact owned authority; do not expand shared x1Cleanup or enable GC.

### E1-10e: unpublished source counterexample and scoped fix (2026-10-06)

Base main@5071d1701624b291a95db9ade8b5ff6abdbd63d7. Productive web upload
followed by Sync PutCommit/RecvFS persists an unpublished commit/tree/file with
SHA-1 block IDs and a canonical mapping, but no permanent fs: or repair.
Only the owned temporary upload reference exists. After its controlled lapse,
real GC reaches COMMITTED or TERMINAL for exact P1 without removing history.
The original RevertDirents handler then successfully publishes HEAD depending
on that retired P1. This is a source-admission counterexample outside Phase5/6;
metadata existence does not prove retained permanent liveness.

RevertDirents non-directory items now require their own settled
fs:<repo>:<historical-file> for every canonical block, capture original exact P,
and recheck those references plus existing exact-P authority immediately before
each HEAD CAS. Missing/malformed/unknown evidence fails the item with HEAD intact.
Live unpublished metadata is deliberately rejected too; normal Sync publication
followed by deletion and restoration remains supported. No pin/pub:/repair is
acquired, no bytes are rematerialized and commit ancestry is not certified.

Measured one root file/path/plaintext block, same repo/org and one DC:
RevertDirents source-admission subset = CLOSED-FIX. Directories, other handlers,
broader batches/layouts, concurrent retention/cleanup, Phase5/6 and R31 remain
OPEN. Foreign settled fs: is not a continuous own-pin proof through HEAD.
W2-10/E1/X1 remain OPEN; production GC OFF, shared development GC unchanged.
See [frozen plan and evidence](./E1-10E-W210-RESIDUAL-DISPOSITION.md).

## ISSUE-E16-REPEATED-EVIDENCE-GATE-01

**Status:** OPEN. **Severity:** P2. **Scope:** FOLLOW-UP / test evidence;
pre-existing on main@25b74624ed5f, not introduced or worsened by PR #286.

`TestE16RevertFileRetainedHistory` keeps a global `e16Evidence` map across
`go test -count` repetitions and credits each leg before fixture cleanups.
TestMain checks only the accumulated map. A complete first repetition followed
by an early Cassandra-query SKIP therefore exits 0 without evidence for the
second repetition. A cleanup SKIP can also occur after the leg is credited.

The first sequence was reproduced in the actual Docker integration runner using
a disposable source copy: close its test session before the second availability
probe; `requireCassandra` takes its existing SKIP path, but all six first-run
slots remain true and the test exits 0 (`tmp/pr286-review-e16-baseline.log`).
A disposable cleanup-SKIP probe on the `normal` leg also exits 0 after that
leg was credited (`tmp/pr286-review-e16-cleanup-baseline.log`). Both sequences
were checked against actual E1-16 code, not a standalone imitation harness.
This is evidence-gate unsoundness, not a reproduced product/storage violation.

Follow-up: reset the map per invocation, credit only after successful nonskipped
cleanups, and check each parent invocation from a cleanup registered before
`requireCassandra`; retain TestMain's omitted-test check. Verify both orders of
early SKIP and cleanup SKIP in Docker. E1-16 runtime/test implementation is
unchanged by #286; no X1/E1 closure or activation decision follows from this issue.
