# Legacy documentation

Historical or superseded documents, with evidence bodies preserved and relocated links repaired.
An archived unchecked task is not automatically resolved; when a statement here conflicts with a living doc, the living doc
wins. Current status lives in `CURRENT_WORK.md`, `docs/OPEN-WORK-INDEX.md`, `docs/KNOWN_ISSUES.md`
and `docs/X1-CRITICAL-PATH.md`.

**Last updated:** 2026-10-13

| File | Archived | Why | Replaced by |
|---|---|---|---|
| `CURRENT_WORK-HISTORY-2026-10-12.md` | 2026-10-12 | Sections cut from `CURRENT_WORK.md` (lines 528–1335, 1356–1973): September slices, session summaries, the February "What's Next", Strategic Roadmap, the retired "frozen components" list and Quick Commands | `CURRENT_WORK.md`, `docs/CHANGELOG.md`, `AGENTS.md` |
| `TECHNICAL-DEBT-HISTORY-2026-10-12.md` | 2026-10-12 | Resolved sections and superseded coverage/checklist snapshots of `docs/TECHNICAL-DEBT.md`; open tasks still have active tracking; the live file keeps a one-line stub at each moved heading so existing `§` references still resolve | `docs/TECHNICAL-DEBT.md` |
| `IMPLEMENTATION_STATUS-SNAPSHOT-2026-07-21.md` | 2026-10-12 | Readiness percentages, freeze rules, blockers and priorities from `docs/IMPLEMENTATION_STATUS.md`, last maintained in January–July | `docs/V1-PRODUCTION-ROADMAP.md`, `docs/X1-CRITICAL-PATH.md`, `docs/KNOWN_ISSUES.md` |
| `README-ROADMAP-2026-10-12.md` | 2026-10-12 | The README's phase checklist had stopped tracking reality (finished work unchecked; Elasticsearch and WOPI plans not pursued) | `docs/V1-PRODUCTION-ROADMAP.md`, `docs/OPEN-WORK-INDEX.md` |
| `SESSION_CHECKLIST-2026-02-02.md` | 2026-10-12 | End-of-session ritual; points to `CLAUDE.md` sections that no longer exist and to the retired 🔒 FROZEN model. No edits since February | `AGENTS.md` rules; per-PR updates to `CURRENT_WORK.md`, `KNOWN_ISSUES.md`, `CHANGELOG.md` |
| `RELEASE-CRITERIA-2026-03-17.md` | 2026-10-12 | Freeze-and-soak procedure no longer followed (`sync.go`, `seafhttp.go`, `onlyoffice.go` have changed hundreds of times since their "freeze") | `docs/V1-PRODUCTION-ROADMAP.md`, `docs/TESTING.md` |
| `SYNC-PROTOCOL-INVESTIGATION-WORKFLOW-2026-01-18.md` | 2026-10-12 | Its "stock Seafile is the reference" workflow is already in `docs/DECISIONS.md` ("Protocol-Driven Development"); nothing links to it | `docs/DECISIONS.md` |
| `MANUAL_TESTING_GUIDE-2026-01-23.md` | 2026-10-12 | January curl walkthrough using `go run`; replaced by Docker-based tests | `docs/TESTING.md`, `scripts/test.sh` |
| `CASSANDRA-TO-TIDB-MIGRATION-2026-01-19.md` | 2026-10-12 | Evaluation whose own verdict was to stay with Cassandra | `docs/DECISIONS.md` (decision recorded there) |
| `SEARCH_AND_OPTIMIZATION_PLAN.md` | 2026-01-22 | Recommended Elasticsearch; search was built on Cassandra SASI indexes instead | `docs/ARCHITECTURE.md` |
| `SYNC_BUG_MULTIFILE_20260116.md` | 2026-01-16 | Resolved multi-file sync bug investigation | `docs/CHANGELOG.md` |
| `PERMISSION-ROLLOUT-PLAN.md`, `PERMISSION-ROLLOUT-COMPLETE.md` | 2026-01-24 | Permission middleware rollout, completed | `internal/middleware/permissions.go`, `docs/ROLES-AND-PERMISSIONS.md` |
| `PROTOCOL-COMPARISON-SUMMARY-2024-12-29.md` | 2024-12-29 | mitmproxy-based testing approach | `docker/seafile-cli-debug/run-sync-comparison.sh`, `docs/SEAFILE-SYNC-PROTOCOL-RFC.md` |
| `SEAFILE-IMPLEMENTATION-GUIDE-2024-12-29.md` | 2024-12-29 | Superseded by the formal protocol docs | `docs/SEAFILE-SYNC-PROTOCOL-RFC.md`, `docs/SEAFILE-SYNC-PROTOCOL.md` |
| `MULTI-REGION-DATABASE-ALTERNATIVES-2025-01-19.md`, `POSTGRES-MULTI-REGION-REPLICATION-2025-01-19.md` | 2025-01-19 | Early database evaluations; Cassandra multi-DC was chosen | `docs/DATABASE-GUIDE.md` |

## Rules

- Move a document here when it is replaced, describes an approach no longer used, or is redundant.
  Do not move active plans, references for planned features, or documents still linked from
  living docs (update the links first).
- Name it `{ORIGINAL_NAME}-{YYYY-MM-DD}.md`, using the last-modified or replaced date.
- When only part of a living doc is outdated, cut that part into a dated file here, name the
  source commit and line ranges in its header, and leave a one-line pointer behind if other docs
  reference the moved section.
- Preserve open-item pointers and update relative links when moving a record; archiving is not closure.
- Never delete files from this folder. Git history is not a substitute: people browsing the repo
  should be able to find the record.
