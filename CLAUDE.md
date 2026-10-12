# CLAUDE.md — SesameFS

Seafile-compatible file sync and share server: Go API, Cassandra (multi-DC), S3-compatible block
storage, React web UI. To be released as open source (MIT). These rules apply to every change,
human or AI.

## Rule 1: Be honest — bad news first
Every status update, review and PR description starts with what is wrong, missing, unverified or
riskier than assumed. A failing test is reported first with its output; a partial run is never
"passing"; anything skipped or unverified is said before anything else. No "should work" or
"production ready" without evidence. Unmeasured numbers are labelled unmeasured. When asked for a
recommendation, give one and name the trade-off.

## Rule 2: Engineering quality over speed
Choose the sound design, not the quickest. Never drop a test, gate or review step to save time. No
time estimates in plans or PRs: report done, not done, blocked. Between equally sound designs,
pick the simpler one.

## Rule 3: Backward compatible by default
- APIs are additive: no field removed or retyped, no status code changed on an existing path.
  Seafile desktop and mobile clients are callers too: a sync-protocol change needs evidence they
  still work (`docs/SEAFILE-SYNC-PROTOCOL.md`, `docs/DECISIONS.md` "Protocol-Driven Development").
- A configuration that starts the service today must still start it.
- Schema changes are additive and reversible; state the rollback.
- Every PR body says "Backward compatible: yes/no, and why".

## Rule 4: Small PRs on a feature branch, never `main`
Branch from current `main`; one focused PR at a time; commit only the files changed for the task;
`gh pr create` for the maintainer to review and merge. Rebase on `main` before review; on conflict,
`main`'s content wins. Never force-push a branch under review. Agree the change before writing
runtime code.

## Rule 5: Tests ship with every change
- A bug fix starts with a test that fails before the fix and passes after (RED → GREEN).
- New behaviour gets a test through a public interface (HTTP handler or package API).
- Cassandra/S3 behaviour is verified against the real stack, not only unit fakes.
- Paste the summary of what ran, and say what did not run (integration, `-race`).

## Rule 6: Destructive GC stays off
`GC_ENABLED=false` on every replica in every DC until X1 and the PRE-GC/A1 gate close. X1, E1 and
PRE-GC are owned by the maintainer: read `docs/X1-CRITICAL-PATH.md` before touching GC,
publication or repair code.

## Rule 7: Open-source hygiene
No secrets, real tokens, internal hostnames, customer data or personal emails in code, tests,
docs or commits. New dependencies need a permissive licence (MIT, BSD, Apache-2.0, ISC) and a
reason in the PR. Never copy code from Seafile or another project without checking its licence.

## Rule 8: Docs stay lean and current
Status lives in one place per topic (table below); link to it, don't copy it. When a change closes
or opens an issue, update `docs/KNOWN_ISSUES.md` and `docs/OPEN-WORK-INDEX.md` in the same PR.
`CURRENT_WORK.md` keeps the active and recent work only; each merged slice is also in
`docs/CHANGELOG.md`. Outdated material moves to `docs/legacy/` with a dated name and a reason
(`docs/legacy/README.md`); it is never deleted.

| Need | File |
|---|---|
| Active work and the GC vs. go-live gates | `CURRENT_WORK.md` |
| One-screen open work and blockers | `docs/OPEN-WORK-INDEX.md` |
| Status of record for every issue | `docs/KNOWN_ISSUES.md` |
| Remaining v1 work | `docs/V1-PRODUCTION-ROADMAP.md` |
| GC safety roadmap | `docs/X1-CRITICAL-PATH.md` |
| Architecture decisions | `docs/DECISIONS.md`, `docs/ARCHITECTURE.md` |
| Running tests | `docs/TESTING.md` |
| History | `docs/CHANGELOG.md`, `git log` |

## Commands
Go is not needed on the host; everything runs in Docker.
```bash
bash scripts/test.sh go                 # Go unit tests (also: api, sync, go-integration, all; --quick)
docker compose up -d sesamefs frontend  # single-node dev stack, API on :8080 unless SESAMEFS_HOST_PORT is set
./scripts/run-mr-cluster.sh up | status | replication-test | test | down   # two-region cluster
```
Two-region web UIs: USA `localhost:5173`, EU `localhost:5174`. Login is SSO-only in dev; seed the
dev cookie as described in the README section "Logging into a web UI".
