# Open Work Index

**Last updated:** 2026-07-25
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
4. A finding lives in exactly one table within a document. Duplicating a row into
   "open" and "closed" is how F13 ended up recorded as both.
5. Do not claim this index lists "everything open in the repo". It lists the
   scope in the header. Use the migration table for deleted-tracker provenance.
6. **Effective config = `configs/*.yaml` + the environment. Never conclude
   anything from the YAML alone.** Prod runs `config.prod.yaml` *with* the prod
   `.env`, and `config.Validate()` lets env values replace YAML ones outright, so
   a YAML entry may be an inert placeholder. This rule exists because the
   multi-DC replication line got "corrected" twice on a YAML-only reading —
   `config.prod.yaml` says `datacenter1: 1`, the prod `.env` says
   `dc-na:1,dc-eu:1,dc-asia:1`, and the env is what runs. Before calling a config
   value stale, grep `.env.prod.example` **and** the `os.Getenv` block in
   `internal/config/config.go`. The inverse also matters: some fields
   (`chunked_staging_max_bytes`, `server.max_upload_mb`) have **no** env override,
   so for those the YAML really is the whole story — see
   `ISSUE-UPLOAD-SIZE-GUARDS-BOTH-ZERO-01`.

---

## Production blockers — must close before go-live

**Readiness verdict is no-go as-is.** Single-node deployments are blocked by
NF-1 and B4; multi-instance adds B1 and B5. See
[PROD-SECURITY-READINESS-20260724.md](./PROD-SECURITY-READINESS-20260724.md).

| Issue | Sev | One line | Detail |
|---|---|---|---|
| `ISSUE-SHARELINK-PASSWORD-BYPASS-01` | HIGH | Password-protected share links serve file content **and an OnlyOffice download token** to anonymous callers | Readiness NF-1 — **single-node** |
| `ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01` | HIGH | No rate/concurrency limit on the seafhttp upload, download and block routes (umbrella; four closable subcontracts) | Readiness B4 ⊇ registry X10 |
| `ISSUE-UPLOAD-CHUNK-MULTINODE-01` | HIGH | Chunked-upload state is node-local; non-sticky routing silently loses files | Readiness B1 — **multi-instance only** |
| `ISSUE-SSO-PENDING-TOKEN-NODE-LOCAL-01` | HIGH | Desktop-SSO pending token is in-memory per process | Readiness B5 — **multi-instance only** |

## Blockers that keep destructive GC disabled

Both are open, neither has a closed design, and `gc.enabled: false` is required
on every replica in every DC until both close.

| Issue | Sev | One line | Detail |
|---|---|---|---|
| `ISSUE-GC-UPLOAD-FENCE-REMATERIALIZATION-01` | Blocker | Physical-delete ABA: an authorized S3 delete can land after a byte-identical re-upload | Registry X1 |
| `ISSUE-GC-CROSS-DC-REFERENCE-VISIBILITY-01` | Blocker (multi-DC) | `LOCAL_QUORUM` references can be invisible to GC in another DC | Registry X2 |

## High / Medium — open (audit follow-ups)

| Issue | Sev | One line | Detail |
|---|---|---|---|
| `ISSUE-SESSION-COOKIE-NOT-HTTPONLY-01` | HIGH | `sesamefs_auth` is a replayable bearer in a JS-readable cookie → XSS = token theft | Readiness SEC-3 / NF-3 |
| `ISSUE-STREAMBLOCKS-VOID-01` | HIGH | `StreamBlocks` returns void → false "complete" log and over-billed traffic | Readiness DL-1 |
| `ISSUE-ZIP-STREAM-LATEFAIL-01` | HIGH | ZIP download can truncate after `200 OK` | Readiness DL-2 |
| `ISSUE-BLOCK-CROSS-LIBRARY-READ-01` | MEDIUM | Cross-library block read (BOLA), gated only by knowing the 256-bit hash | Readiness B2/SEC-1 |
| `ISSUE-SHARELINK-DOWNLOAD-CAP-RACE-01` | MEDIUM | Download cap and `single_use` are race-bypassable | Readiness NF-2 / SH-5 |
| `ISSUE-SYNC-UNBOUNDED-BODIES-01` | MEDIUM | Four sync handlers still read the body unbounded | Registry X9 |
| `ISSUE-AUDIT-TRAIL-INCOMPLETE-01` | MEDIUM | `audit_log` records deletions but never grants | Readiness NF-6 / RB-3 |
| `ISSUE-UPLOAD-PUT-BEFORE-INTENT-01` | MEDIUM | S3 PUT precedes durable intent; a crash leaves an undiscoverable object | Registry X3 |
| `ISSUE-QUOTA-RESERVATION-01` | MEDIUM | TOCTOU between quota pre-check and publish | Readiness UP-3 |
| `ISSUE-DOWNLOAD-NO-404-01` | MEDIUM | Deleted file answers 503 forever; layers disagree | Registry X8 — **accepted cost of PR-6** |
| `ISSUE-BATCH-MOVE-FALSE-SUCCESS-01` | MEDIUM | Legacy `moveBatchFiles` returns `success:true` without moving | API footgun; UI uses `SyncBatchMove` |
| `ISSUE-ACCOUNTS-M2M-PATH-01` | MEDIUM | Admin API key channel works; M2M lacks `source=accounts` + idempotency | Code-verified 2026-07-25 |
| `ISSUE-MID-OPERATION-REVOCATION-01` | MEDIUM | Revoke mid chunked-upload / mid-stream / ZIP does not abort in flight | UP-5 / DL-5 / RB-4 |
| `ISSUE-SHARELINK-NO-ORG-SCOPE-01` | MEDIUM | No org-internal share-link scope (token-only, anonymous) | SH-2 — product / BY DESIGN option |
| `ISSUE-SHARELINK-CREATOR-KEY-01` | MEDIUM | Encrypted share links decrypt with creator's key | SH-3 |
| `ISSUE-TRAFFIC-RECORDER-DROPS-01` | MEDIUM | Saturated traffic recorder drops events silently | No counter / log |

## Low / latent / deferred hardening

| Issue | Sev | One line |
|---|---|---|
| `ISSUE-ORG-SCOPE-CHECK-PER-HANDLER-01` | LOW (latent) | Cross-tenant gate is copy-pasted into ~50 handlers, not middleware. No current gap. |
| `ISSUE-AUTOLOGIN-COOKIE-INSECURE-01` | LOW–MED | `handleAutoLogin` hardcodes cookie `Secure=false` |
| `ISSUE-UPLOAD-SIZE-GUARDS-BOTH-ZERO-01` | LOW (config) | Both chunked-upload size guards can be disabled together; staging guard is `0` in **every** shipped config |
| `ISSUE-ACCOUNTS-PROVISIONING-RUNBOOK-01` | LOW (docs) | Bootstrap/rotation/smoke/revoke runbook for the Accounts platform API key was never written |
| `ISSUE-ACCOUNTS-M2M-REQUEST-SIGNING-01` | LOW (hardening) | Optional HMAC/JWT request signing beyond admin API key — **not required for v1**; channel is API key by design |
| `ISSUE-SHARELINK-BCRYPT-COST-01` | LOW | Share-link passwords use `bcrypt.DefaultCost` (10), not Argon2id |
| `ISSUE-ENCRYPTED-VIEW-CONTENT-LENGTH-01` | LOW | v2 inline-view/share-raw omit `Content-Length` on encrypted downloads |
| `ISSUE-DOWNLOAD-NO-REHASH-01` | MEDIUM (defense-in-depth) | Download does not re-hash blocks; trusts object store |
| `ISSUE-OIDC-ROLE-RECONCILIATION-01` | MEDIUM (design) | OIDC claims ↔ manual role overrides: authoritative-source rule undefined |

## Performance — deferred pending measurement

| Item | Sev | One line |
|---|---|---|
| `ISSUE-UPLOAD-PER-BLOCK-PAXOS-01` (= X4 / P-4 / UP-2) | HIGH (perf) | One global Paxos round per block. **PR-11, not started** — need a per-statement latency metric first. |
| `ISSUE-CANONICAL-READ-FANOUT-01` (= X5) | MEDIUM | Canonical read fan-out never validated against a real cluster |
| `ISSUE-CHECKBLOCKS-WORK-AMPLIFICATION-01` (= X11) | MEDIUM | 100k id cap bounds the parser, not the ~100k sequential Cassandra reads |
| `ISSUE-READ-AFTER-WRITE-CROSS-DC-01` (= X6) | MEDIUM | Read-after-write across DCs; 3×25 ms retry covers local lag only |

## Verification debt

Not findings — things nobody has proven either way.

- **No multi-DC test exists.** X2, X6 and the whole cross-DC line of reasoning
  are derived from the production consistency contract, never reproduced.
- **No production latency measurement** for the per-block LWT (X4 / `ISSUE-UPLOAD-PER-BLOCK-PAXOS-01`).
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

## Tracker migration table (exhaustive)

Every open or noteworthy item from the three deleted trackers. Status verified
against **code** on 2026-07-25 where marked. Disposition codes: **DONE** =
implemented; **OPEN** = still work; **DECISION** = product choice, not a defect;
**ROADMAP** = owned by roadmap/PLANS, not this index's defect list;
**DUPLICATE** = already had an ISSUE.

### From `quotas-pending-issues.txt`

| Old item | Disposition | Canonical | Notes (code-verified) |
|---|---|---|---|
| 0. V2 mutations bypass quota | DONE | `ISSUE-QUOTA-COVERAGE-01` | Fixed 2026-05-14 |
| 1. Per-user storage quota on uploads | DONE | `ISSUE-USER-STORAGE-ENFORCE-01` | Fixed 2026-05-14 |
| 2. Formalizar path M2M Accounts → SesameFS | OPEN (partial) | `ISSUE-ACCOUNTS-M2M-PATH-01` | **Channel works today:** `RequireSuperAdmin` + `apikeys.ScopeAdmin` on `/admin`; `UpdateOrganization`, `AdminAddOrgUser` / update / delete exist. Still missing: `source=accounts` audit tag (hardcoded `manual-superadmin`), idempotency |
| 3. preview/evaluate plan change | DONE | `PreviewOrganizationPlanChange` | Route live |
| 4. Invite/create users vía Accounts | DONE (SesameFS API) / ROADMAP (invite UX) | `PLANS-AND-PERMISSIONS.md` Phase 4.5 | Admin user CRUD usable by Accounts; org-local writes gated by `Accounts.DisableOrgUserWrites`. Invite-token product story stays in PLANS |
| 5. External org identifier | DECISION | `PLANS-AND-PERMISSIONS.md` | No `external_org_id` in schema — wait for Accounts contract |
| 6. External billing IDs | DECISION | `PLANS-AND-PERMISSIONS.md` | No billing columns yet |
| 7. Snapshot pre-downgrade users | DECISION / ROADMAP | `PLANS-AND-PERMISSIONS.md` | Preview computes counts; no persisted activation snapshot |
| 8. Quota error messages | DONE (mostly) | traffic helpers + upload UI | Residual inconsistency is polish, not a tracker row |
| 9. Quota user → org visualization | DONE | sysadmin user-info + `AdminGetOrgUser` | Shows inherited quotas |
| 10. Traffic quotas admin UI | DONE | `set-org-traffic-quota` dialog + `UpdateOrganization` | |
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

Header already deferred to the four canonical docs. Hard-blocker GC multi-instance
item is superseded by the upload-fence X1/X2 + `gc.enabled: false` posture.
Accounts pointer redirected here via the quotas migration rows above.

---

## Completed series (for context, not work)

- **Upload-fence / canonical-storage series, PR-1..PR-10** (#137–#146): all
  fourteen `F` findings closed. See
  [GC-UPLOAD-FENCE-PR-PLAN.md](./GC-UPLOAD-FENCE-PR-PLAN.md) for PR merge progress
  and [UPLOAD-FENCE-FINDINGS-REGISTRY.md](./UPLOAD-FENCE-FINDINGS-REGISTRY.md)
  for the findings themselves. **Defect status lives in `KNOWN_ISSUES.md`.**
- **Org-scoped block deletion (P10)**, PR-1..PR-3 (#134–#136): block keys are
  org-scoped end to end; cross-org delete isolation closed.
