# Library lifecycle final-write exclusion

## Frozen plan (2026-10-17)

Defect: a library lease holder can pause after its last renewal, lose the lease,
then execute an unconditional restore/permanent-delete batch after the new owner.
This affects authenticated APIs even with ordinary GC disabled.

Scope: the final library lifecycle batches in API permanent delete, restore and
GC library/org cascades. User/org leases, Phase 6, metadata publication and
sharing-aware reclamation remain separate. Preserve HTTP fields/statuses.

Protocol: add an additive `finalizing` column to the existing library lease.
A global SERIAL LWT may promote only the current preparatory token to a durable,
non-expiring final section. Takeover, renewal and ordinary release must not erase
or expire that section. Run the existing batch while holding it, then conditionally
release only after acknowledged completion. Stale preparatory tokens cannot enter.
Unknown/error outcomes retain exclusion; never rerun an ambiguous batch automatically.

Trade-off: a crashed/failed final section blocks that library until recovery.
No automatic timeout recovery is safe against a paused owner or delayed batch.
Recovery and rollout instructions must require quiescing every old owner and
settling its outstanding writes before repair/release. Drain all old binaries
before using this protocol; leave the additive column on rollback. Mixed versions
are not a certified lifecycle configuration. Ordinary GC remains OFF.

Evidence: first add deterministic real-Cassandra HTTP regressions for both stale
orders, using a query observer to pause after successful renewal. Record RED on
main, then GREEN after the fix. Verify final-section takeover refusal, TTL removal,
ordinary-release refusal, error/unknown retention and acknowledged completion;
exercise the public package mutation API and real worker finalization too.
Run affected Docker unit/race packages, existing restore guards, vet and formatting.
Audit all lease entry points and final batch call sites, link/status consistency,
cleanup and remote-main freshness before opening a focused PR. Report every skip
and unrun suite; no X1 or PRE-GC/A1 activation decision is made here.

## Implemented protocol and scope

The final section is an exclusion latch, not another pre-write ownership read.
Migration 028 adds `finalizing boolean`; old preparatory rows decode as null.
The library-specific acquisition, renewal and release helpers all pin global
SERIAL even with a LOCAL_SERIAL session. User/org lease semantics are unchanged.

1. Acquire/renew the preparatory token with the existing TTL and stale interval.
2. Immediately before the final batch, conditionally promote the same token
   while `finalizing=null`. Rewrite started_at, heartbeat, token and finalizing
   with `USING TTL 0`; all four cells are non-expiring. Cassandra documents that
   [TTL 0 overrides a table default TTL](https://cassandra.apache.org/doc/stable/cassandra/developing/cql/cql_singlefile.html).
3. Takeover compares the observed heartbeat and requires finalizing=null. A
   renewal either refreshes the preparatory TTL or renews a final section without
   expiry. Ordinary deferred release requires finalizing=null.
4. Execute the prepared final batch. Restore also finishes its synchronous
   aggregate-counter adjustment before acknowledged completion removes the
   final section. Any error/panic/unknown outcome leaves exclusion in place; an
   attempt with the same finalizing token cannot replay the callback.

Call sites: API permanent delete (`hardDeleteLibraryRowsFn`), library restore
(`restoreDeletedLibrary`), and the GC library/org cascades' final
prepared library-delete batch. Its projection reads happen before promotion;
read/preparation failures release the preparatory lease and preserve queue retry.
Canonical rows, markers and final-batch read models stay inside this section. Share-link cleanup before finalization and asynchronous
tag cleanup are not certified by this fix. Existing queue children and block/S3
protocols do not change. No S3 mutation is introduced here.

Backward compatible: yes for HTTP fields, types/statuses, queue formats and
supported Cassandra consistency settings. A stale delete uses the existing 409
conflict response; stale restore retains its existing 500 failure response.
The additive migration and drained rollout are required. Availability changes:
an interrupted final section can block the library indefinitely. The issue
remains OPEN, P1 CURRENT-RUNTIME / PRE-GC for recovery; PRE-GC/A1 remain OPEN.

## Recovery, rollout and rollback

This PR does not implement automatic final-section recovery. Never delete or
expire a retained latch merely because its heartbeat is old. A paused owner or
an unacknowledged Cassandra batch may still write. A new token is not proof that
those writes stopped. The latch records ownership, not a replayable operation
journal; it does not identify a safe restore/delete outcome by itself.

Before deploying, apply migration 028 and drain/stop every old API and GC worker
owner. Prevent old processes from resuming, including across DCs. Old binaries
ignore finalizing and can steal, renew with TTL or release a final section;
ordinary rolling old/new overlap is not certified for this protocol.

For a retained section, keep that library blocked. Recovery requires an operator
maintenance window that quiesces all possible owners, establishes settlement of
outstanding requests/batch-log replay, and reconciles the canonical row, deletion
marker and affected projections to a separately established intended outcome.
If those prerequisites cannot be established, retain exclusion and escalate;
there is no generally safe timeout or generic online unlock command here.
Only after settlement and repair may the exact owned latch be removed. The
integration fixture's unconditional teardown is not a production recovery tool.

Rollback similarly drains all owners and settles/repairs retained final sections
before starting older writers. Leave the additive column in place; no data-table
rollback is needed. Returning to old writers restores the original stale-owner
risk. Ordinary GC remains OFF during rollout, validation, recovery and rollback.

## Docker evidence and reproduction

Use an isolated Cassandra 5.0.9 instance, no host ports, with an explicitly owned
`sesamefs_lifecycle*` keyspace (datacenter1, RF=1). Create the keyspace before
migration. The test refuses ordinary keyspace names and requires GC_ENABLED=false.
Inside a Docker Go source runner from the repository root, export:

```sh
export CONFIG_PATH="$(pwd)/configs/config.docker.yaml"
export AUTH_DEV_MODE=true ONLYOFFICE_ENABLED=false GC_ENABLED=false
export CASSANDRA_HOSTS='<owned-cassandra-host>:9042'
export CASSANDRA_KEYSPACE=sesamefs_lifecycle
# Set the isolated fixture's Cassandra credentials if authentication is enabled.
go run ./cmd/sesamefs migrate
SESAMEFS_LIFECYCLE_REAL_STACK=1 go test -json -tags integration -count=1 -timeout 3m ./internal/gc ./internal/api/v2 -run '^TestLibraryLifecycle|^TestRestoreDeletedLibrary_'
SESAMEFS_LIFECYCLE_REAL_STACK=1 go test -race -json -tags integration -count=1 -timeout 4m ./internal/gc ./internal/api/v2 -run '^TestLibraryLifecycle|^TestRestoreDeletedLibrary_'
go test -json -short -count=1 ./internal/gc ./internal/db ./internal/api/v2
go test -race -json -short -count=1 ./internal/gc ./internal/db ./internal/api/v2
```

The real tests live in packages gc and api/v2; the standard integration suite
currently tags internal/integration only. These explicit executions, not standard
suite wiring, provide the real evidence. Sessions deliberately default to
LOCAL_SERIAL; lease operations pin SERIAL. HTTP tests use production handlers,
DB-backed caller roles, valid trashed libraries and real read models. Their
query observer pauses after an acknowledged renewal; aging only that fixture's
heartbeat models suspension beyond the stale interval without waiting 90 minutes.

- Accepted RED: the final HTTP regression on unchanged pre-fix runtime fails
  both schedules: stale delete removes the restored row/read model; stale restore
  recreates canonical cells/read models after permanent deletion. Both returned
  success. The initial run overlapped fixture migration and one leg did not reach
  its pause; it is excluded. The accepted run starts after migrations complete.
- GREEN normal and race: both HTTP schedules and the three existing restore
  guards pass. The final-section package test verifies stale-token rejection,
  aged-heartbeat takeover refusal, ordinary-release refusal, non-expiring cells,
  successful completion, failed/unknown callback retention, panic retention and
  the real organization-scoped worker's final canonical batch.
- The unknown case injects a timeout return from the mutation callback; the
  interruption case injects a panic. Neither is native network fault injection
  nor an OS process-crash experiment. No native CAS-unknown or crash recovery
  certificate is claimed.
- Initial harness fixes: Queue.Enqueue signature corrected to EnqueueBatch;
  TTL nulls decode as zero via MapScan, now checked against a positive
  preparatory-TTL control. Source inventories now reflect the specialized
  library helpers; their SERIAL pins are guarded separately.
- Pre-existing P2 FOLLOW-UP: permanent delete's tag side effect logs
  `Counter and non-counter mutations cannot exist in the same batch` even on
  unchanged runtime. The test waits for the side effect before session close;
  the final lifecycle result does not certify tag cleanup. Tracked as
  `ISSUE-LIBRARY-TAG-CLEANUP-MIXED-BATCH-01`.

Full application integration, API/OIDC scripts, sync/desktop/mobile, three DCs,
physical S3 deletion and native fault/crash recovery are not run by this slice.
This is local evidence, not independent CI or an activation certificate.

### Initial audit result (306542a9b, superseded by the review correction below)

| Check | Result |
|---|---|
| RED, archived main@14e8d0cfb + final HTTP regression | exit 1; both stale schedules fail as expected (2 subtests + parent) |
| Real Cassandra GREEN, normal and race | each exit 0; GC 8 PASS events (6 subtests + parent + SERIAL guard), API 6 (2 subtests + parent + 3 restore guards); no SKIP/races |
| Final affected-package race | exit 0; GC 802, DB 528, API v2 2041 PASS events; DB 9 seed and API 5 DB-dependent SKIP; no races |
| Final go test -short -count=1 ./... | exit 0; 21 tested packages, no FAIL; 22 named test SKIP (9 seed, 5 API permissions/shares, 6 chunker long/demo, 2 account DB); untagged integration source guards only |
| go vet -tags integration ./...; gofmt; added local links/anchors; diff whitespace | PASS |
| Lease/final-batch call-site audit | API permanent delete and restore plus library/org GC final batches covered; generic dynamic lease helpers now user/org only |
| Teardown | lock, library, lookup and queue counts 0; owned keyspace absent after DROP; owned Cassandra container/anonymous data removed |

Local ignored evidence is retained in `tmp/lifecycle-evidence-20261017/`:
35 files include accepted JSON/exit logs, migration output, final Go/CQL source
copies and teardown evidence. SHA256SUMS manifest SHA-256:
`fbcff699a21cf45f344645882c72e1654ff12e6cfb27ca39d41c1df722720a0d`.
Source copies match the executed runner (line endings normalized). Earlier failed
setup/harness logs remain outside the accepted bundle; they are not relabeled as
passing. This audit is local and has no independent CI/reviewer certificate.

No THIS-PR blocker was found within the final-write exclusion contract. The P1
recovery/availability limit is deliberate and remains OPEN; accepting this safety
trade-off does not accept activation. The existing shared development primary
still reports GC_ENABLED=true, pre-existing and unchanged. No shared stack
settings or data were used for the new fixture; the policy remains ordinary GC OFF.


## Review correction: counter lifetime and preparation failures (2026-10-17)

The initial head released restore exclusion before the synchronous aggregate
adjustment. A real Cassandra regression on 306542a9b reproduces org usage 900
instead of 1000 after a concurrent soft/permanent delete removes the library's
counter. A second pause while the helper reads its source allows a permanent
delete with 200 when it should receive 409. Both are THIS-PR regressions; the
initial no-blocker audit above is superseded. Restore now executes the final
batch and AdjustAggregateStorageCounters in the same callback before release.
The helper's pre-existing best-effort read/write error handling is unchanged:
this does not make counters transactional or guarantee repair under failures.

The initial worker also built its batch/read models inside final exclusion.
PrepareLibraryHardDelete now builds the batch first, then the worker renews and
promotes its token before executing only the prepared write. A projection-read
failure is retryable and cannot create a durable final latch. The canonical
mutation inventory now names the builder, with the same lifecycle classification.
A unit regression and a real-Cassandra worker fixture inject a preparation-read
error at the store boundary; they verify no final-section entry, no canonical
delete, no retained lock and preserved queue retry. The unit case is RED on
306542a9b. This is boundary fault injection, not a native server ReadTimeout.

Both new counter schedules, both original stale-owner schedules, existing restore
guards and final-section tests pass on the corrected runtime. Normal and race
runs include the real worker and injected preparation failure. The general
unknown-write/panic/crash availability trade-off remains OPEN P1 CURRENT-RUNTIME /
PRE-GC. No timeout unlock, automatic recovery or new activation decision is added.

The review also checked library and org cascade call sites, renewal after
preparation (a lost token cannot execute the prepared batch), final batch queries,
API status compatibility and counter/delete ordering. No additional regression
was found within those exercised paths. Full tagged integration, API/OIDC,
clients, three DCs and native faults remain unrun; zero regressions universally
is not established by these tests. Existing mixed counter/tag cleanup still logs
its tracked pre-existing failure; it is not certified here.

### Review verification results

Final corrected source: both counter schedules and the injected preparation
failure are RED against 306542a9b and GREEN after correction. Normal and race
selected runs each have GC 10 / API 9 PASS events, no SKIP or races. These include
the default worker regression, real worker normal/preparation-failure legs,
original lifecycle schedules, two counter windows and existing restore guards.
Final affected-package race: GC 803 / DB 528 / API 2041 PASS, with the same nine
seed and five DB-dependent API SKIP. Final short suite: 21 tested packages PASS,
22 named test SKIP; full tagged integration is not implied. Vet with integration
tags, formatting, added links, inventories and documentation guards pass.

The owned Cassandra fixture has zero retained library leases; its keyspace and
container/anonymous data have been removed. The new counter fixtures assert
canonical/lease cleanup; isolated keyspace teardown removes their remaining
counter/reconciliation projections. Shared deployments are unchanged.

Accepted review evidence: `tmp/lifecycle-review-evidence-20261017/`, 41 files,
including final source snapshots matching the Docker runner. Manifest SHA-256:
`3f8bb4522146d9897ab61478fd4adbccd0481e977b0538acd18feb29221162df`.
The original 35-file evidence bundle stays frozen. Initial Cassandra readiness
failure and the first stale inventory assertion are excluded from accepted GREEN;
neither is presented as passing. This remains local validation without CI.
