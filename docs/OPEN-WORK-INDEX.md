# Open Work Index

**Last updated:** 2026-08-22
**Scope (narrowed 2026-07-25):** production blockers, recent readiness /
upload-fence audit follow-ups, and leftovers from consolidating the parallel
pending-work trackers. **This is not the entire product backlog.** Roadmap /
product / UX items that are not defects live in
[V1-PRODUCTION-ROADMAP.md](./V1-PRODUCTION-ROADMAP.md),
[PLANS-AND-PERMISSIONS.md](./PLANS-AND-PERMISSIONS.md), and
[TECHNICAL-DEBT.md](./TECHNICAL-DEBT.md). The migration table at the bottom
records where every deleted-tracker item went.

## How this repo tracks work

Three layers, and each has exactly one job. Most of the contradictions this index
was created to fix came from a finding living in two layers at once with only one
of them updated.

| Layer | File(s) | Owns | Does **not** own |
|---|---|---|---|
| **Registry of record** | [KNOWN_ISSUES.md](./KNOWN_ISSUES.md) | The `ISSUE-*` id, **current** status, severity, fix direction | The deep reasoning behind a finding |
| **Audit / analysis docs** | [PROD-SECURITY-READINESS-20260724.md](./PROD-SECURITY-READINESS-20260724.md), [UPLOAD-FENCE-FINDINGS-REGISTRY.md](./UPLOAD-FENCE-FINDINGS-REGISTRY.md), `SECURITY-ASSESSMENT-*`, `UPLOAD-*` | Evidence, severity rationale, why alternatives were rejected, verification performed | Live status — audits are dated snapshots (`Status as of YYYY-MM-DD`) that link to `ISSUE-*` |
| **This index** | `OPEN-WORK-INDEX.md` | The one-screen list for **this scope** and the cross-references / migration table | Full product backlog; any detail beyond one line |

**Rules that keep it honest:**

1. A finding gets **one** `ISSUE-*` id, even when two audits found it
   independently. When that happens, say so in the issue (see
   `ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01`, which umbrellas B4 and X10 with
   closable subcontracts).
2. **Status changes go in `KNOWN_ISSUES.md` only.** An audit document is a
   snapshot of a date; do not retro-edit its verdict — add a dated note. Registry
   Open/Closed tables and the PR-plan status table are **historical / PR-progress**
   views, not competing live trackers.
3. Cite code by **symbol name**, not `file.go:1234`. Line numbers rot.
4. A finding must not appear in multiple status tables with independently
   maintained or contradictory states. An executive summary may **reference** the
   canonical row (e.g. "also listed under Production Blockers"); do not maintain
   two competing status columns for the same id.
5. Do not claim this index lists "everything open in the repo". It lists the
   scope in the header. Use the migration table for deleted-tracker provenance.
6. **Effective config = `configs/*.yaml` + the environment. Never conclude
   anything from the YAML alone.** `Config.Load()` parses YAML, then
   `applyEnvOverrides()` replaces selected fields from the environment, then
   `Validate()` checks the result — `Validate()` does **not** apply overrides.
   Prod runs `config.prod.yaml` *with* a prod `.env` derived from
   `.env.prod.example`. The YAML may hold inert placeholders (e.g.
   `replication_dcs: {datacenter1: 1}`) while the env template ships the real
   multi-DC posture (`CASSANDRA_REPLICATION_DCS=dc-na:1,dc-eu:1,dc-asia:1`).
   Before calling a config value stale, read **both** the YAML and
   `.env.prod.example`, and confirm the symbol path in `applyEnvOverrides()`.
   The inverse also matters: some fields (`chunked_staging_max_bytes`,
   `server.max_upload_mb`) have **no** env override, so for those the YAML
   really is the whole story — see `ISSUE-UPLOAD-SIZE-GUARDS-BOTH-ZERO-01`.

---

## Production blockers — must close before go-live

**Single-node go-live still has open HIGH findings, and they are independent of
X1.** Corrected 2026-08-22: this summary previously read "no single-node go-live
blockers remain", which contradicted the HIGH rows in this document's own
High/Medium table a few lines below. Do not restore that claim.

Three gates, kept separate on purpose:

- **Enabling destructive GC** — blocked by X1
  (`ISSUE-GC-UPLOAD-FENCE-REMATERIALIZATION-01`) **alone**; X2 closed 2026-08-14.
  See the GC section below.
- **Single-node go-live** — blocked by the resource-amplification findings
  below (`ISSUE-RECVFS-DECOMPRESSION-AMPLIFICATION-01`,
  `ISSUE-SYNC-FSID-WORK-AMPLIFICATION-01`). Nothing about GC gates these, and
  closing X1 does not close them. `ISSUE-ZIP-STREAM-LATEFAIL-01` is Medium per
  the registry, not a go-live blocker.
- **Multi-instance operation** — additionally blocked by the two node-local
  state issues in the table below.

What genuinely closed: NF-1 2026-07-25; B4 2026-08-04; the object-storage
posture issue and the sync public-link token auth gap 2026-08-07;
`ISSUE-LIBRARY-MUTATION-NO-PERMISSION-CHECK-01` 2026-08-22. See
[PROD-SECURITY-READINESS-20260724.md](./PROD-SECURITY-READINESS-20260724.md)
(dated snapshot) and
[PROD-READINESS-VERIFICATION-20260822.md](./PROD-READINESS-VERIFICATION-20260822.md)
(baseline re-verification at `a1570b186`, with follow-up fixes at `913c3892c`).

| Issue | Sev | One line | Detail |
|---|---|---|---|
| `ISSUE-UPLOAD-CHUNK-MULTINODE-01` | HIGH | Chunked-upload state is node-local; non-sticky routing silently loses files | Readiness B1 — **multi-instance only** |
| `ISSUE-SSO-PENDING-TOKEN-NODE-LOCAL-01` | HIGH | Desktop-SSO pending token is in-memory per process | Readiness B5 — **multi-instance only** |

The single-node HIGH rows are **not duplicated here**; their canonical rows live
in the High/Medium table below, per rule 4.

### Recently closed

Kept briefly so a reader who arrives with the old blocker list can see it moved
rather than vanished. Drop rows once they stop being recent; status of record
stays in [KNOWN_ISSUES.md](./KNOWN_ISSUES.md).

- `ISSUE-SESSION-COOKIE-NOT-HTTPONLY-01` — **Fixed 2026-08-12** (readiness SEC-3 /
  NF-3). The `sesamefs_auth` cookie is `httpOnly=true` on every OIDC login and
  logout writer, funneled through one `setAuthCookie` helper per package. Verified
  first that nothing in this repository reads it from JS. `Secure` is untouched —
  that is `ISSUE-AUTOLOGIN-COOKIE-INSECURE-01`, still open.
- `ISSUE-SYNC-UNBOUNDED-BODIES-01` — **Fixed 2026-08-12** (registry X9). All four
  remaining sync handlers read through `readLimitedRequestBody`; `pack-fs` and
  `check-fs` also gained the id-count cap that stops a body under the byte cap from
  expanding ~17x. It bounds one request body at a time and nothing more — and for
  `RecvFS`, only the **compressed** body at that. The three residuals are all above:
  the aggregate term is `ISSUE-SYNC-METADATA-CONCURRENCY-01`,
  the inflate is `ISSUE-RECVFS-DECOMPRESSION-AMPLIFICATION-01`, and the work an
  accepted id list triggers is `ISSUE-SYNC-FSID-WORK-AMPLIFICATION-01`.
- `ISSUE-BATCH-MOVE-FALSE-SUCCESS-01` — **Fixed 2026-08-12.** Legacy same-repo batch
  move via `POST /file/move` returned `{"success":true,"moved":N}` without touching
  the FS tree; it now returns 501 pointing at `sync-batch-move-item/`. Still
  unimplemented, no longer lying about it. The UI never used this path.
- `ISSUE-SHARELINK-PASSWORD-BYPASS-01` — **Fixed 2026-07-25** (readiness NF-1 /
  SH-6). Gate runs before the inline read and the OnlyOffice token mint; the
  bundle builder drops protected content; the OnlyOffice helper fails closed.
  Live integration covers both halves: both public endpoints for inline
  content, and a `.docx` fixture for the OnlyOffice credential (a `.md` fixture
  cannot reach that branch). Both mutation-verified against the cluster.
- `ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01` — **Closed 2026-08-04** (B4 umbrella;
  A1/A2, B, C and D0-D6 complete). Download admission is enabled in every
  shipped configuration and its capacities are validated against the measured
  2 GiB process-local budget. Byte-rate shaping remains a separate finding;
  object-storage anonymity was closed separately on 2026-08-07.
- `ISSUE-SYNC-LINK-TOKEN-AUTH-01` — **Fixed 2026-08-07.** `syncAuthMiddleware`
  accepted any valid download token as a repository credential. Reproduced live
  as an unauthorized cross-library block write by an anonymous share-link
  visitor, plus an escalation through `/download-info` into a full repository
  sync token, and a total absence of token-to-route repository binding.
  `isRepositorySyncToken`
  now validates `Source == ""`, `Path == "/"` and a route-bound `RepoID` before
  the bearer becomes an identity. All three clauses mutation-verified against
  the live server; ordinary sync tokens unaffected.
- `ISSUE-OBJECT-STORAGE-ANONYMOUS-DOWNLOAD-01` — **Closed 2026-08-07. Never
  affected production.** The `mc anonymous set download` lines existed only in
  the four development/test Compose files, not in `docker-compose.prod.yml`,
  which ships no MinIO and targets provider-native S3 that is private by
  default. The lines are removed; nothing depended on them, since every MinIO
  consumer in the repository authenticates. The old entry overstated the
  finding by not separating the dev Compose files from the production one.
- `ISSUE-STREAMBLOCKS-VOID-01` — **Fixed 2026-08-03**; `StreamBlocks` now
  returns errors and the seafhttp caller records delivered bytes only.

## Blockers that keep destructive GC disabled

X1 is open with no closed design. X2 closed 2026-08-14 (implemented 2026-08-13), proven on a real three-DC
cluster. `gc.enabled: false` remains required on every replica in every DC — it now
rests on X1 alone.

**Read X1 as the whole fence-and-physical-identity workstream, not just the stale
DELETE.** Never-reused physical keys are necessary but not sufficient: a shared
per-candidate claim id lets one worker drop the publication fence while another is
still deleting under it, and the reuse probe can then hand a writer back the very
incarnation being destroyed — which physical-key uniqueness cannot prevent, because no
new incarnation is created. Closure criteria are in Registry X1.

**Closure options are compared in
[GC-X1-CLOSURE-OPTIONS.md](./GC-X1-CLOSURE-OPTIONS.md)** (2026-08-14), which replaces the
abandoned generational-fence ADR. Nothing there is accepted and no X1
implementation has started; the separate P1 locator-authority foundation does
not satisfy the X1 closure criteria.

| Issue | Sev | One line | Detail |
|---|---|---|---|
| `ISSUE-GC-UPLOAD-FENCE-REMATERIALIZATION-01` | Blocker | Physical-delete ABA **plus** the publication-fence race: an authorized S3 delete can land after a byte-identical re-upload, and a shared claim id lets another worker drop the fence mid-delete | Registry X1 (four closure criteria) · [closure options](./GC-X1-CLOSURE-OPTIONS.md) |
| `ISSUE-GC-CROSS-DC-REFERENCE-VISIBILITY-01` | ✅ Closed 2026-08-14 | Destructive liveness reads at `EACH_QUORUM` behind a topology gate; five-leg three-DC evidence green, both mutations (`LOCAL_QUORUM` and `QUORUM`) confirmed red | [Registry X2](./UPLOAD-FENCE-FINDINGS-REGISTRY.md) · [X2 runbook](./GC-X2-MULTIDC-VALIDATION.md) |

## High / Medium — open (audit follow-ups)

| Issue | Sev | One line | Detail |
|---|---|---|---|
| `ISSUE-LIBRARY-MUTATION-NO-PERMISSION-CHECK-01` | ✅ Closed 2026-08-22 | The three handlers now gate on `LibraryHandler.requireLibraryConfigAuthority`, which requires `PermissionOwner` (library owner or org owner/admin/superadmin); content `rw` no longer carries configuration authority. Negative tests cover all three handlers | [known issue](./KNOWN_ISSUES.md) |
| `ISSUE-GC-STALE-CLAIM-READ-CONSISTENCY-01` | MEDIUM | `ReleaseStaleBlockClaim` decides "no claim to release" from a session-consistency read, and that zero makes the caller consume the candidate — so a claim taken by a GC worker in ANOTHER datacenter (RF 1 per DC: the quorums do not intersect) can be missed, stranding a live block behind `gc_state='deleting'`. No data loss; the cost is a permanent upload refusal. Found auditing X2; the clean fix depends on X1's serial-domain decision (EACH_QUORUM here would couple ordinary queue drain to every DC being up; SERIAL collides with R12) |
| `ISSUE-GC-REFERENCED-ORPHAN-LIFECYCLE-01` | MEDIUM | A `gc_s3_orphans` row refused for still having references falls out of the working set once the day cursor passes it, then TTLs out at 90 days — storage leak, and the alerting counter goes quiet with it | Found auditing X2; needs a deferred/quarantine state, not a `phaseErr` |
| `ISSUE-GC-LOGICAL-MAPPING-RETENTION-01` | LOW/MEDIUM | R11a intentionally preserves SHA-1 → SHA-256 mappings after physical GC; without a separate logical-death reaper, stale rows accumulate and may resolve to a 404 until rematerialization | R11a/B.3 accepted tradeoff · [known issue](./KNOWN_ISSUES.md) |
| `ISSUE-GC-MANUAL-TRIGGER-NOT-GATED-01` | ✅ Closed 2026-08-22 | The superadmin GC surfaces did not check `GC.Enabled`: manual triggers answered `{"started":true}` on nodes where nothing ran, and the DLQ requeue/delete path *claimed the GC lease* from a disabled replica. All are now explicitly lifecycle-gated; manual triggers additionally require current leadership | Found re-verifying the kill switch post-#181; defence in depth, never a live bypass |
| `ISSUE-RECVFS-DECOMPRESSION-AMPLIFICATION-01` | HIGH | `recv-fs` inflates each object unbounded; 128 MiB body → ~126 GiB at DEFLATE's measured 1029:1 | Found auditing X9; the body cap does not bound this |
| `ISSUE-SYNC-FSID-WORK-AMPLIFICATION-01` | HIGH | `pack-fs` materializes the whole response: ~409k repeats of one valid id, `PermissionR` only. `check-fs` shares the fan-out | Found auditing X9; the fs-id equivalent of the closed X11 |
| `ISSUE-RECVFS-FSID-UNVERIFIED-01` | ? | `recv-fs` never checks the client's fs_id hashes the content it stores — but the stored-vs-computed mapping may make that by design | Open **question**; settle the contract before "fixing" |
| `ISSUE-ZIP-STREAM-LATEFAIL-01` | MEDIUM | ZIP download can truncate after `200 OK` | Readiness DL-2. Severity corrected 2026-08-22 to match [KNOWN_ISSUES.md](./KNOWN_ISSUES.md), which has rated it Medium since the 2026-05-27 preflight narrowing — truncated/retryable download, not corruption |
| `ISSUE-BLOCK-CROSS-LIBRARY-READ-01` | MEDIUM | Cross-library block read (BOLA), gated only by knowing the 256-bit hash | Readiness B2/SEC-1 |
| `ISSUE-SHARELINK-DOWNLOAD-CAP-RACE-01` | MEDIUM | Download cap and `single_use` are race-bypassable | Readiness NF-2 / SH-5 |
| `ISSUE-SYNC-METADATA-CONCURRENCY-01` | MEDIUM | Sync metadata routes bound one body, not N — 16 concurrent `recv-fs` ≈ 2 GiB | Successor to X9; X10's equivalent for the block routes is closed |
| `ISSUE-AUDIT-TRAIL-INCOMPLETE-01` | MEDIUM | `audit_log` records deletions but never grants | Readiness NF-6 / RB-3 |
| `ISSUE-UPLOAD-PUT-BEFORE-INTENT-01` | MEDIUM | S3 PUT precedes durable intent; a crash leaves an undiscoverable object | Registry X3 |
| `ISSUE-QUOTA-RESERVATION-01` | MEDIUM | TOCTOU between quota pre-check and publish | Readiness UP-3 |
| `ISSUE-DOWNLOAD-NO-404-01` | MEDIUM | Deleted file answers 503 forever; layers disagree | Registry X8 — **accepted cost of PR-6** |
| `ISSUE-ACCOUNTS-M2M-PATH-01` | MEDIUM | Admin API key channel works; M2M lacks `source=accounts` + idempotency | Code-verified 2026-07-25 |
| `ISSUE-MID-OPERATION-REVOCATION-01` | MEDIUM | Revoke mid chunked-upload / mid-stream / ZIP does not abort in flight | UP-5 / DL-5 / RB-4 |
| `ISSUE-SHARELINK-NO-ORG-SCOPE-01` | MEDIUM | No org-internal share-link scope (token-only, anonymous) | SH-2 — product / BY DESIGN option |
| `ISSUE-SHARELINK-CREATOR-KEY-01` | MEDIUM | Encrypted share links decrypt with creator's key | SH-3 |
| `ISSUE-TRAFFIC-RECORDER-DROPS-01` | MEDIUM | Saturated traffic recorder drops events silently | No counter / log |
| `ISSUE-LIBRARY-CLASS-CHANGE-RESIDENCY-01` | MEDIUM | `ChangeStorageClass` validates only that the class is known, so a `strict` org can move an existing library's preference outside its allowed region **and onto a cold tier** — neither half of the create-time contract is re-applied. **Decided:** under `strict` the endpoint must accept only an in-region hot class, and new materializations must not fail over across the region | Decided, not implemented. **Still open:** what `strict` promises about content placed before the policy took effect — the transition is ungated today. Narrowed 2026-08-22 by the closure of `ISSUE-LIBRARY-MUTATION-NO-PERMISSION-CHECK-01`: the residency transition is now reachable only by a library owner or org admin, not by any org member. Still ungated on residency itself |

## Low / latent / deferred hardening

| Issue | Sev | One line |
|---|---|---|
| `ISSUE-IWORK-PREVIEW-413-NO-MESSAGE-01` | LOW (UX) | An iWork document above the D6 source cap renders the raw JSON error inside the preview modal, and the image→PDF fallback re-requests it. Frontend only; carries the two product decisions D6 left open — raising the cap, or a separate admission profile (needs a §12 amendment) |
| `ISSUE-ORG-SCOPE-CHECK-PER-HANDLER-01` | LOW (latent) | Cross-tenant gate is copy-pasted into ~50 handlers, not middleware. No current gap. |
| `ISSUE-AUTOLOGIN-COOKIE-INSECURE-01` | LOW–MED | `handleAutoLogin` hardcodes cookie `Secure=false` |
| `ISSUE-UPLOAD-SIZE-GUARDS-BOTH-ZERO-01` | LOW (config) | Both chunked-upload size guards can be disabled together; staging guard is `0` in **every** shipped config |
| `ISSUE-ACCOUNTS-PROVISIONING-RUNBOOK-01` | LOW (docs) | Bootstrap/rotation/smoke/revoke runbook for the Accounts platform API key was never written |
| `ISSUE-ORG-ADMIN-DELETION-BANNER-01` | LOW (UX) | Soft-delete lifecycle exists; org bootstrap omits `deleted_at`/grace + frontend banner TODO |
| `ISSUE-ACCOUNTS-M2M-REQUEST-SIGNING-01` | LOW (hardening) | Optional HMAC/JWT request signing beyond admin API key — **not required for v1**; channel is API key by design |
| `ISSUE-SHARELINK-BCRYPT-COST-01` | LOW | Share-link passwords use `bcrypt.DefaultCost` (10), not Argon2id |
| `ISSUE-ENCRYPTED-VIEW-CONTENT-LENGTH-01` | LOW | v2 inline-view/share-raw omit `Content-Length` on encrypted downloads |
| `ISSUE-DOWNLOAD-NO-REHASH-01` | MEDIUM (defense-in-depth) | Download does not re-hash blocks; trusts object store |
| `ISSUE-OIDC-ROLE-RECONCILIATION-01` | MEDIUM (design) | OIDC claims ↔ manual role overrides: authoritative-source rule undefined |

## Performance — deferred pending measurement

| Item | Sev | One line |
|---|---|---|
| `ISSUE-UPLOAD-PER-BLOCK-PAXOS-01` (= X4 / P-4 / UP-2) | HIGH (perf) | One `SERIAL` LWT/Paxos transaction per block invocation that reaches metadata registration when the effective setting is `SERIAL`; browser/sync preflight can bypass fully deduplicated blocks. The shipped default uses `SERIAL`, but env and topology determine effective WAN cost. **P0 is not an X4 performance fix**. **PR-11, not started** — characterize latency, permit wait, Paxos settings and placement identity first. See [X1/X4 characterization](./UPLOAD-PAXOS-HOT-PATH-X1-CHARACTERIZATION.md). |
| `ISSUE-CANONICAL-READ-FANOUT-01` (= X5) | MEDIUM | Canonical read fan-out never validated against a real cluster |
| `ISSUE-DOWNLOAD-BYTE-RATE-SHAPING-01` | MEDIUM (deferred) | D bounds aggregate accepted work, not bytes per second; measure node egress at D6 before choosing shaping |
| `ISSUE-READ-AFTER-WRITE-CROSS-DC-01` (= X6) | MEDIUM | Read-after-write across DCs; 3×25 ms retry covers local lag only |

## Verification debt

Not findings — things nobody has proven either way.

- **A multi-DC harness now exists, and only X2 uses it.**
  `docker-compose.cassandra-3dc.yaml` (three DCs, RF 1) plus
  `scripts/x2-multidc-validation.sh` reproduce cross-DC consistency behaviour at the
  wire level, and X2 is closed on that evidence — divergent-state visibility,
  fail-closed with a DC down, reference-DC-down with the `QUORUM` mutation, both
  topology-gate halves, and both consistency mutations. **X6 and the remaining cross-DC assumptions are still derived
  from the production consistency contract and have never been reproduced**, even
  though the instrument to do it is now checked in.
- **No production latency measurement** for the per-block LWT (X4 / `ISSUE-UPLOAD-PER-BLOCK-PAXOS-01`); the characterization is documented in [UPLOAD-PAXOS-HOT-PATH-X1-CHARACTERIZATION.md](./UPLOAD-PAXOS-HOT-PATH-X1-CHARACTERIZATION.md), but no runtime measurement has landed.
- **The six older upload funnels** have never been driven individually under a
  live fence; coverage proves the three retry wrapper mechanisms instead.
- **PR-10 did not run the full Compose integration suite** (PR-2..PR-6 did).
- `TECHNICAL-DEBT.md` §32 — the three retry wrappers were never consolidated.
- **No repo-root CI workflow** yet (see `TECHNICAL-DEBT.md` / roadmap) — tracked
  as verification debt, not duplicated as a security ISSUE here.

---

## Document consolidation (2026-07-25)

The contradictions this index exists to prevent came from **seven** places all
claiming to say what was pending. Consolidated as follows.

**Removed** (each item disposition is in the migration table below):

| File | Why | Where its content lives now |
|---|---|---|
| `PENDING-ISSUES-AUDIT-2026-05-14.md` | Orphan — linked from nothing, 2.5 months stale | migration table + roadmap / `KNOWN_ISSUES` / new ISSUEs |
| `sesamefs-pending-issues.txt` (root) | Header already named the four docs as canonical | those docs |
| `quotas-pending-issues.txt` (root) | Stale copy; many items already DONE in code | migration table (code-verified 2026-07-25) |
| `UPLOAD-PERFORMANCE-PR58-AUDIT.md` | Half of one PR58 research archive | `UPLOAD-PR58-RESEARCH-ARCHIVE.md` |
| `UPLOAD-S3-RELAY-BOTTLENECK.md` | (same) | (same) |

**Marked historical, kept for evidence:** `SECURITY-ASSESSMENT-2026-04.md`,
`-v2`, `-v3`. Each carries a banner saying its status column is a dated
snapshot. `-v4` stays current-ish; the readiness report supersedes it for
upload/download/sharing specifically.

**Still to decide** — left alone rather than merged blindly:

- `KNOWN_ISSUES.md` vs `TECHNICAL-DEBT.md` — defect vs architectural debt boundary
  is real but inconsistently applied.
- `V1-PRODUCTION-ROADMAP.md` vs `IMPLEMENTATION_STATUS.md` vs `CURRENT_WORK.md`
  — three readiness percentages / blocker lists. Candidate for one file.

---

## Tracker migration table — all unique items

Every **unique** open or noteworthy item from the three deleted trackers is
mapped below. Duplicate rows that only restated the same work are not repeated;
see the `sesamefs-pending-issues.txt` note at the end. Status verified against
**code** on 2026-07-25 where marked. Disposition codes: **DONE** =
implemented (with concrete symbol/evidence); **OPEN** = still work; **PARTIAL** =
usable mechanism exists but the original tracker’s full contract is unfinished;
**DECISION** = product choice, not a defect; **ROADMAP** = owned by
roadmap/PLANS, not this index's defect list; **DUPLICATE** = already had an ISSUE.

### From `quotas-pending-issues.txt`

| Old item | Disposition | Canonical | Notes (code-verified) |
|---|---|---|---|
| 0. V2 mutations bypass quota | DONE | `ISSUE-QUOTA-COVERAGE-01` | Fixed 2026-05-14 |
| 1. Per-user storage quota on uploads | DONE | `ISSUE-USER-STORAGE-ENFORCE-01` | Fixed 2026-05-14 |
| 2. Formalizar path M2M Accounts → SesameFS | OPEN (partial) | `ISSUE-ACCOUNTS-M2M-PATH-01` | **Channel works today:** `RequireSuperAdmin` + `apikeys.ScopeAdmin` on `/admin`; `UpdateOrganization`, `AdminAddOrgUser` / update / delete exist. Still missing: `source=accounts` audit tag (hardcoded `manual-superadmin`), idempotency |
| 3. preview/evaluate plan change | DONE | `PreviewOrganizationPlanChange` | Route live |
| 4. Invite/create users vía Accounts | DONE (operational) | Admin user CRUD + Accounts remote | **Live today:** Accounts provisions/invites users via a dedicated superadmin API key calling `AdminCreateUser` / `AdminAddOrgUser` (org-local writes gated by `Accounts.DisableOrgUserWrites`, default true; `max_users` enforced). Residual hygiene is tracked separately as `ISSUE-ACCOUNTS-M2M-PATH-01` (audit source + idempotency), not as missing invite/provisioning. Optional Phase 4.5 UX polish in `PLANS-AND-PERMISSIONS.md` is roadmap, not a blocker for this item. |
| 5. External org identifier | DECISION | `PLANS-AND-PERMISSIONS.md` | No `external_org_id` in schema — wait for Accounts contract |
| 6. External billing IDs | DECISION | `PLANS-AND-PERMISSIONS.md` | No billing columns yet |
| 7. Snapshot pre-downgrade users | DECISION / ROADMAP | `PLANS-AND-PERMISSIONS.md` | Preview computes counts; no persisted activation snapshot |
| 8. Quota error messages | PARTIAL | `traffic.TrafficQuotaExceededResponse` + `file-uploader.js` `getQuotaErrorMessage` | Upload UI maps `storage` / `traffic-*` reasons. Residual: some handlers pass `includeReason=false`; sync still uses plain strings — polish, not a new ISSUE |
| 9. Quota user → org visualization | DONE | `frontend/.../sys-admin/users/user-info.js` + `AdminGetOrgUser` | Shows effective + `(inherited)` for storage/upload/download |
| 10. Traffic quotas admin UI | DONE | `set-org-traffic-quotas.js` + `UpdateOrganization` traffic fields | Sysadmin can view/edit org traffic quotas |
| 11. Audit log quotas/provisioning | OPEN (partial) | `ISSUE-AUDIT-TRAIL-INCOMPLETE-01` + `ISSUE-ACCOUNTS-M2M-PATH-01` | Org updates write `audit_log` but always `manual-superadmin`; user CRUD writes nothing |
| 12. Recorder drop metrics | OPEN | `ISSUE-TRAFFIC-RECORDER-DROPS-01` | `Recorder.recordAsync` silent drop |
| 13. Risk dashboard / notifications | ROADMAP | `PLANS-AND-PERMISSIONS.md` Phase 3 | Soft `X-Quota-Warning` exists; no dashboard |
| Missing Accounts runbook | OPEN | `ISSUE-ACCOUNTS-PROVISIONING-RUNBOOK-01` | File never existed |

### From `PENDING-ISSUES-AUDIT-2026-05-14.md`

| Old item | Disposition | Canonical | Notes |
|---|---|---|---|
| 1. Per-user storage quota | DONE | `ISSUE-USER-STORAGE-ENFORCE-01` | |
| 1b. Quota coverage gaps | DONE | `ISSUE-QUOTA-COVERAGE-01` | |
| 2. Backup / DR | ROADMAP | `V1-PRODUCTION-ROADMAP.md` | No backup scripts / DR doc yet |
| 3. Persistent audit / activity | OPEN | `ISSUE-AUDIT-TRAIL-INCOMPLETE-01` | |
| 4. Batch move false-success | OPEN | `ISSUE-BATCH-MOVE-FALSE-SUCCESS-01` | `FileHandler.moveBatchFiles` still TODO + `success:true`; UI path `SyncBatchMove` is real |
| 5. GC queue recounts | DONE / historical | GC docs | Moved out of hot path |
| 6. Public/upload-link resume | OPEN / BY DESIGN | resume analyses | Safe stub |
| 7. Upload "do not replace" | DONE | `ISSUE-UPLOAD-REPLACE-01` | |
| 8. File-operation statistics | OPEN | `ISSUE-FILE-STATS-01` | Already in `KNOWN_ISSUES` |
| 9. Antivirus | ROADMAP | `V1-PRODUCTION-ROADMAP.md` §6 | No `internal/av/` |
| 10. Storage class / lifecycle | ROADMAP | roadmap + cold-storage notes | |
| 11. Default repo stub | OPEN | `ISSUE-DEFAULT-REPO-01` | Already in `KNOWN_ISSUES` |
| 12. Compatibility stubs | ROADMAP / debt | `TECHNICAL-DEBT.md` | |
| 13. Cold storage restore placeholder | ROADMAP | `V1-PRODUCTION-ROADMAP.md` | `InitiateRestore` placeholder |
| 14. Text diff | OPEN | `KNOWN_ISSUES` version-diff gap / `TECHNICAL-DEBT` | Frontend URL commented out |
| 15. Org-admin stats platform leak | OPEN | `ISSUE-ORG-STATS-SCOPE-01` | Already in `KNOWN_ISSUES` |
| 16. Traffic recorder drops | OPEN | `ISSUE-TRAFFIC-RECORDER-DROPS-01` | Same as quotas item 12 |
| 17. Devices/license stubs | ROADMAP | sysadmin stubs | |
| 18. CI / E2E maturity | ROADMAP / debt | `TECHNICAL-DEBT.md` | No repo-root `.github/workflows` |

### From `sesamefs-pending-issues.txt`

Every **unique** item from this tracker is already mapped above (via the quotas
table or the May audit table). Duplicate rows that only pointed at those same
canonical docs are not repeated here. Hard-blocker GC multi-instance wording is
superseded by the upload-fence X1/X2 + `gc.enabled: false` posture. Accounts
work is covered by the quotas migration rows (`ISSUE-ACCOUNTS-M2M-PATH-01`,
runbook; invite/create via Accounts is DONE operationally).

---

## Completed series (for context, not work)

- **Upload-fence / canonical-storage series, PR-1..PR-10** (#137–#146): all
  fourteen `F` findings closed. See
  [GC-UPLOAD-FENCE-PR-PLAN.md](./GC-UPLOAD-FENCE-PR-PLAN.md) for PR merge progress
  and [UPLOAD-FENCE-FINDINGS-REGISTRY.md](./UPLOAD-FENCE-FINDINGS-REGISTRY.md)
  for the findings themselves. **Defect status lives in `KNOWN_ISSUES.md`.**
- **Org-scoped block deletion (P10)**, PR-1..PR-3 (#134–#136): block keys are
  org-scoped end to end; cross-org delete isolation closed.
