# AGENTS.md — SesameFS

Seafile-compatible file sync and share server: Go API, Cassandra (multi-DC), S3-compatible block
storage, React web UI. Intended open-source licence: MIT; existing third-party code must still
satisfy its own licence. These working rules apply to human and AI contributions.

## Rule 1: Be honest — bad news first
Report failures, missing evidence and material uncertainty before successes. Include the relevant
failure output; a partial run is never "passing". Label unmeasured numbers and name skipped checks.
Do not claim "production ready" without evidence. Give a recommendation and its trade-off.

## Rule 2: Engineering quality over speed
Choose the sound design and keep required tests, gates and reviews. No time estimates in plans or
PRs: report done, not done or blocked. Between equally sound designs, pick the simpler one.

## Rule 3: Backward compatible by default
- Preserve API fields, types and documented status codes by default. A correction to defective or
  insecure behaviour needs an explicit compatibility assessment, caller evidence and a migration
  plan if it breaks supported clients; do not silently change the contract.
- Seafile desktop and mobile clients are callers too. Sync-protocol changes need evidence for the
  affected clients (see [protocol](docs/SEAFILE-SYNC-PROTOCOL.md) and
  [Protocol-Driven Development](docs/DECISIONS.md)).
- Preserve supported configurations. Schema changes are additive and reversible by default;
  document rollback and any explicitly approved exception.
- Every PR body says "Backward compatible: yes/no, and why".

## Rule 4: Small PRs on a feature branch, never main
Start new work from current main; when continuing an existing branch, preserve its work. Commit
only task files and open a focused PR for the maintainer to review and merge. Update from main
before review. Resolve conflicts by preserving both the current contract and the intended change;
never blindly take one side. Keep a backup ref before a rebase. Rewriting a published branch needs
explicit authorization and an exact force-with-lease; never overwrite concurrent remote work.
An explicit implementation request authorizes the agreed scope; clarify only unresolved design
choices that materially affect it.

## Rule 5: Verify the behaviour you change
- A runtime bug fix starts with a regression that fails before the fix and passes after (RED → GREEN).
- New runtime behaviour gets a test through a public interface (HTTP handler or package API).
- Changed Cassandra/S3 behaviour is verified against the real stack, not only unit fakes.
- Documentation-only changes need link, command and consistency checks; do not add runtime tests
  solely to mirror prose. Report what ran and what did not (including integration and race tests).

## Rule 6: Destructive GC stays off in ordinary deployments
Keep GC_ENABLED=false on every ordinary deployment replica in every DC until X1 and PRE-GC/A1
close. Authorized destructive evidence tests may run the real worker in isolated fixtures with
explicitly owned test data and verified cleanup; this does not authorize enabling a shared stack
or production. Read [X1-CRITICAL-PATH](docs/X1-CRITICAL-PATH.md) before touching GC, publication or
repair code. The maintainer owns X1, E1 and activation decisions; this file closes no gate.

## Rule 7: Open-source hygiene
Do not add secrets, real tokens, internal hostnames, customer data or personal emails to code,
tests, docs or commits. New dependencies need a permissive licence (MIT, BSD, Apache-2.0, ISC)
and a reason in the PR. Never copy third-party code, including Seafile code, without checking
its licence and attribution requirements.

## Rule 8: Docs stay lean and current
Keep a source of record per topic; summaries link to it. Update [known issues](docs/KNOWN_ISSUES.md)
and the [open-work index](docs/OPEN-WORK-INDEX.md) when opening or closing an issue.
CURRENT_WORK.md keeps active/recent slices and gate summaries; record merged slices in CHANGELOG.
Archive outdated material with a dated name, provenance and reason in [legacy](docs/legacy/README.md).
Preserve evidence and fix relocated links. An archived checklist does not close its open items:
leave a pointer to their active tracking. Never delete archived records.

## Rule 9: Audit severity and scope separately
Apply [X1 finding classification](docs/X1-CRITICAL-PATH.md#3-finding-classification-rule).
A finding introduced or worsened by the PR, or one that falsifies its contract, blocks that PR.
A pre-existing independent finding is a follow-up, with severity preserved. X1 and PRE-GC/A1
blockers remain tracked against their own gates. Hypothetical code is not a finding. Require a
concrete failure sequence in existing code under the stated deployment contract; do not expand
PR scope merely to clear independent debt.

| Need | Source of record |
|---|---|
| Active/recent work and gate summaries | [CURRENT_WORK.md](CURRENT_WORK.md) |
| Open work and blockers | [OPEN-WORK-INDEX.md](docs/OPEN-WORK-INDEX.md) |
| Status per issue | [KNOWN_ISSUES.md](docs/KNOWN_ISSUES.md) |
| Dated v1 planning | [V1-PRODUCTION-ROADMAP.md](docs/V1-PRODUCTION-ROADMAP.md) |
| GC order, closure and audit scope | [X1-CRITICAL-PATH.md](docs/X1-CRITICAL-PATH.md) |
| Decisions and architecture | [DECISIONS.md](docs/DECISIONS.md), [ARCHITECTURE.md](docs/ARCHITECTURE.md) |
| Tests | [TESTING.md](docs/TESTING.md) |
| History | [CHANGELOG.md](docs/CHANGELOG.md), git log |

## Commands
Go tests run in Docker; Go is not required on the host. Run only the relevant checks.

```bash
bash scripts/test.sh go                 # Go unit tests; other categories: api, sync, go-integration, all
# Single-node development stack (separate from the two-region cluster):
docker compose up -d sesamefs frontend  # API :8080 unless SESAMEFS_HOST_PORT overrides it
```

Two-region cluster commands below are separate invocations; choose the action needed:

```bash
./scripts/run-mr-cluster.sh up
./scripts/run-mr-cluster.sh status
./scripts/run-mr-cluster.sh replication-test
./scripts/run-mr-cluster.sh test
./scripts/run-mr-cluster.sh down         # stops the cluster; retains volumes
```

Two-region UI defaults: USA localhost:5173, EU localhost:5174. For dev login, follow
[Logging into a web UI](README.md#multi-region-testing). Agent loading and version requirements
are documented in [Contributing](README.md#contributing).
