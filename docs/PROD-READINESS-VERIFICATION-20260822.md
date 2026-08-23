# Production Readiness Verification — post PR #181

**Date:** 2026-08-22
**Baseline HEAD:** `a1570b186` (Merge PR #181, `feat/p1-locator-authority`)
**Follow-up fixes verified:** `913c3892c` (code/tests commit immediately preceding this documentation update)
**Scope:** independent code-level re-verification of **selected** production-readiness
findings — what PR #181 delivered, plus the audit-arc findings listed in §B. Every
claim below was re-confirmed by reading the code at `913c3892c`.

**This is not a sweep of everything open.** [OPEN-WORK-INDEX.md](./OPEN-WORK-INDEX.md)
carries roughly twice as many open rows in its High/Medium table as appear here;
notably `ISSUE-SYNC-METADATA-CONCURRENCY-01` (the aggregate-concurrency sibling of
§B.5, ~2 GiB across 16 concurrent `recv-fs`) is open and **not** re-verified here.
Status of record for every id stays in [KNOWN_ISSUES.md](./KNOWN_ISSUES.md); this
document is a dated snapshot, per the index's layering rules.

**Citation convention:** code is cited by **symbol name**, per rule 3 of
[OPEN-WORK-INDEX.md](./OPEN-WORK-INDEX.md) — line numbers rot, and an earlier draft
of this document had already drifted against the tree it was written from.

---

## Verdict

The GC/storage architecture is genuinely converging: PR #181 delivers real
end-to-end locator authority and the negative property is proven with a real
Cassandra + MinIO integration test (§A). Destructive GC remains disabled by
config, and that switch is now enforced at its decision point rather than by
accident (§B.1).

**The system is not production-ready as-is.** Ten defects are verified below
(B.2–B.11): **five HIGH** and five medium/low. **One** of them — B.2 — closed on
2026-08-22 in the same change as this document; **nine remain open**.

Hardening gaps found while writing this were fixed in that same change
(`ISSUE-GC-MANUAL-TRIGGER-NOT-GATED-01`, §B.1). They are recorded under B.1 and
deliberately **not** counted among the ten: B.1 is a positive verification of the
kill switch, not a readiness defect.

Two gates must not be conflated, because doing so is how "X1 closed" becomes
"ship it":

| Gate | Blocked by |
|---|---|
| Enabling destructive GC (`GC_ENABLED=true`) | **X1 alone** (`ISSUE-GC-UPLOAD-FENCE-REMATERIALIZATION-01`); X2 closed 2026-08-14 |
| Putting SesameFS in production at all | The independent findings in §B — **none of which X1 gates** |

The most urgent open non-X1 blocker is
**`ISSUE-RECVFS-DECOMPRESSION-AMPLIFICATION-01`** — HIGH, single-node, measured
1029:1 amplification. It is a bounded fix, needs no architectural decision, and
does not touch the GC lifecycle.

---

## A. What PR #181 actually delivered (verified)

The central claim is that the persisted `storage_key` is now the authority
end-to-end. Confirmed:

- **The authorization phase resolves the destination store BEFORE any destructive
  step.** In `gc.Worker`'s block-delete path, an empty or non-canonical
  `storage_key`, an unresolvable store, or a persisted key that differs from
  `BlockStoreDeleter.StorageKeyForHash` all release the claim via
  `releaseBlockClaim` and return. The row is never left half-destroyed, and a
  mismatch increments `GCErrorsTotal{reason="block_storage_key_mismatch"}`.
- **Recovery applies the same check.** The S3-orphan recovery path compares the
  reloaded `storage_key` against the derived org-scoped key, retains the cursor,
  and refuses to delete on mismatch
  (`GCErrorsTotal{reason="s3_orphan_storage_key_mismatch"}`).
- **The negative property is proven against real Cassandra + MinIO** —
  `internal/integration/gc_s3_locator_authority_test.go` (build tag
  `integration`). Two orgs are seeded, a canonical row is planted carrying the
  victim org's key, the worker is driven in-process, and the test asserts: both
  objects still exist in MinIO; the canonical row was not deleted; the claim was
  released (`gc_state=""`, `gc_claim_id=""`); and no orphan row was recorded, so a
  later sweep cannot inherit the corrupt locator.

Source-level guard tests (`r11a_mapping_surface_guard_test.go`,
`r21_orphan_surface_guard_test.go`) carry **no** build tag, so they run in the
ordinary CI unit pass and a future refactor cannot silently reintroduce
`DELETE FROM block_id_mappings` in production Go or add a second orphan writer.

**What #181 did not do.** `storage_key` remains *deterministic*: `K = hashToKey(L)`.
#181 changed which value is authoritative, not whether the value is recomputable —
the guard compares persisted against derived precisely because they should agree.
It therefore cannot discriminate two different physical lives at the same address.
That discrimination is P2/R12's job and is not merged. **P1 does not by itself
satisfy any of X1's four closure criteria.**

Schema: migration `016_gc_s3_orphans_storage_key.cql`.

---

## B. Findings (code-verified 2026-08-22)

Ten defects: five HIGH (B.2–B.6), five medium/low (B.7–B.11). B.1 is a positive
verification of the kill switch and is not counted among them.

Severities follow [KNOWN_ISSUES.md](./KNOWN_ISSUES.md), the registry of record.
An earlier draft of this document rated B.7 HIGH; the registry has it Medium, and
the registry wins.

### B.1 GC destructive kill-switch — pinned OFF and now enforced at the decision point ✓

- `docker-compose.prod.yml` → `GC_ENABLED=false`, with a comment naming X1.
- `config.DefaultConfig` → `GCConfig.Enabled: false`.
- `applyEnvOverrides` → `GC_ENABLED` accepts only `"true"` or `"1"`.

**Newly found and fixed 2026-08-22** (`ISSUE-GC-MANUAL-TRIGGER-NOT-GATED-01`): the
*config* surface was gated but the *runtime* surface was not. `NewServer`
constructs `gcService` unconditionally, `POST /api/v2.1/admin/gc/run` is
registered unconditionally, and `Service.TriggerWorker`/`TriggerScanner` checked
neither `Enabled` nor `started`. Nothing ran only because `Service.Start` returns
before launching its loops when disabled — so the kill switch rested on an
emergent property of `Start`'s control flow, not on a stated invariant. This
mattered operationally: **the current readiness posture keeps `GC_ENABLED=false`
on every replica in every datacenter; once destructive GC is approved, only the
designated activation location will enable it and every other node will remain
disabled**. Disabled nodes served that endpoint and answered
`{"started": true}` for runs that never happened. Now gated on
`Service.ManualTriggerError`, with `handleGCRun` returning `503` before the
`dry_run` override for disabled, stopped, or non-leader nodes. Never a live
bypass; hardened before a refactor could make it one.

Review of that fix widened it twice, and both are worth recording because they
are the same lesson:

- **The first guard reintroduced a shutdown deadlock.** Reading
  `config.Enabled && started` under `s.mu` collided with `Service.Stop`, which
  originally held `s.mu` across `s.wg.Wait()` while `runScannerOnce` called
  `TriggerWorker` from inside a goroutine that `Wait` was waiting for. The
  predicate now reads an atomic and shutdown no longer holds the lifecycle mutex
  while draining. A guard placed on a hot shutdown path has to be lock-free.
- **`Service.DeleteFailedItem` and `RequeueFailedItem` had the same gap**, and a
  worse consequence: they call `tryClaimLeadershipForAdmin`, which *claims the
  lease*. An operator on any disabled or stopping replica could take GC leadership
  away from the one datacenter that drains the queue. Both now refuse with
  `ErrGCDisabled`/`ErrGCNotRunning` before claiming. The original finding was
  scoped to "manual triggers"; the actual defect was "the kill switch is honoured
  on some admin surfaces and not others".

  One kill switch, but **two predicates**. Manual triggers use
  `Enabled && started && leader`: a follower's loop would consume the token and
  return without doing work. DLQ requeue/delete use `Enabled && started` but do
  not require current leadership, because their store work is inline and the
  operation can claim leadership itself. Requiring the active lifecycle prevents
  a stopped node from reclaiming the lease during HTTP shutdown.

- **A shutdown timeout no longer publishes a false stopped state.** The service
  remains `stopping`, refuses triggers and restart, and keeps the old run's
  `WaitGroup` isolated until DLQ and workers actually drain. Lease renewal remains
  active during that drain; only the finalizer stops renewal, releases leadership
  and transitions to `stopped`. `StopWithContext` returns its context error to
  `Server.Shutdown`.

### B.2 Library configuration mutations have no permission gate — HIGH → ✅ Fixed 2026-08-22

`ISSUE-LIBRARY-MUTATION-NO-PERMISSION-CHECK-01`

`LibraryHandler.UpdateLibrary`, `LibraryHandler.RenameLibrary` (dispatched from
`LibraryOperation` on `op=rename`) and `LibraryHandler.ChangeStorageClass` accepted
only `orgID` from `authMiddleware` and `repoID` from the URL, then applied the
update. Any authenticated org member could rename any library in the org, rewrite
its description, shorten its `version_ttl_days` retention, or move its
storage-class preference.

Additional evidence beyond the original finding: `RegisterLibraryRoutesWithToken`
constructs a `PermissionMiddleware` and applies it to **no** route in the `repos`
group. The three handlers are reachable through **five** registrations (with and
without trailing slash) under **two** prefixes (`/api/v2`, `/api2`) — so this was a
route surface, not three stray functions, and the gate had to live in the handler.

Fixed by `LibraryHandler.requireLibraryConfigAuthority`, which compares the caller
to the canonical owner before considering an explicit organization role. Owner
credentials require API-key scope `read-write`; organization
owner/admin/superadmin overrides require scope `admin`. Content shares remain
insufficient and repo API tokens are refused outright. Negative tests in
`internal/api/v2/library_mutation_authority_test.go`, confirmed red before the fix.

**The first version of this fix was incomplete**, and the gap is worth recording
because it is easy to repeat. `GetLibraryPermission` answers only *"what is this
USER's authority over this library"* — it reads the org role and the owner column
from Cassandra and collapses canonical ownership and organization-role override
onto `PermissionOwner`. That model cannot express their different credential
ceilings. The final gate checks canonical identity and organization role
separately, then applies the corresponding API-key scope.

`ISSUE-LIBRARY-CLASS-CHANGE-RESIDENCY-01` is now **partially closed**:
`ChangeStorageClass` rejects cold primary placement and re-applies a strict
organization's `default_region`. Runtime failover, already-persisted preferences,
historical/reused content and migration remain open.

### B.3 Chunked upload state is node-local — HIGH, multi-instance only

`ISSUE-UPLOAD-CHUNK-MULTINODE-01`

`internal/api` holds a package-level `var chunkManager = NewChunkManager()`.
Without upload-token-sticky routing at the LB, a `chunk0 → instance A`,
`final → instance B` split silently loses the file (confirmed empirically in
[PROD-SECURITY-READINESS-20260724.md](./PROD-SECURITY-READINESS-20260724.md)).

### B.4 Desktop SSO pending-token store is per-process — HIGH, multi-instance only

`ISSUE-SSO-PENDING-TOKEN-NODE-LOCAL-01`

`clientSSOStore` is a `map[string]*clientSSOEntry` behind a `sync.RWMutex`, created
per `Server` by `newClientSSOStore`. Poll and OIDC callback landing on different
instances never rendezvous.

### B.5 `recv-fs` decompression amplification — HIGH, single-node

`ISSUE-RECVFS-DECOMPRESSION-AMPLIFICATION-01`

`SyncHandler.RecvFS` bounds the request body with `readLimitedRequestBody`, but
decompresses each individual zlib object inside the packed body with an unbounded
`io.ReadAll` over the `zlib.Reader`. At DEFLATE's measured 1029:1 ratio a 128 MiB
body inflates to ≈126 GiB. The body cap does not bound this.

### B.6 `pack-fs` work amplification — HIGH, single-node

`ISSUE-SYNC-FSID-WORK-AMPLIFICATION-01`

`SyncHandler.PackFS` iterates user-supplied fs ids accepted by
`parseBoundedIDList(packFSIDListSpec())` and materializes a JSON object per id into
a single `bytes.Buffer`. The cap is `maxPackFSIDs = maxPackFSBodyBytes /
minFSIDWireBytes` = 16 MiB / 41 = **409,200** ids. One valid id repeated that many
times triggers ~409k Cassandra point reads and ~409k marshals behind
`PermissionR`. The byte cap bounds the parse, not the work it triggers. `CheckFS`
shares the fan-out.

### B.7 ZIP download late-fail — MEDIUM

`ISSUE-ZIP-STREAM-LATEFAIL-01`

`SeafHTTPHandler.HandleZipDownload` writes `200 OK` and constructs the
`zip.Writer` over `c.Writer` before streaming. A failure inside `addDirToZip` can
then only be recorded (`FailStreamError`, log) — the client receives a truncated
but nominally successful download.

### B.8 Cross-library block read (BOLA) — MEDIUM

`ISSUE-BLOCK-CROSS-LIBRARY-READ-01`

Blocks are content-addressed and deduplicated **per org**, not per library.
`SyncHandler.GetBlock` calls `checkSyncPermission(c, repoID, PermissionR)` — which
verifies read on the URL's `repoID` — then resolves the block by
`(org_id, representation_id, block_id)` with no check that the block belongs to
that library.

**Wider than a single handler:** the registry records three hash-only surfaces —
the bare-SHA `seafhttp` block GET, `CheckBlocks`, and SHA-1 → SHA-256 mapping
resolution. `CheckBlocks` is the cheaper half: a cross-library **existence oracle**
that confirms a candidate hash before any read is attempted. Library-scoped read
authorization is the fix.

### B.9 `ReleaseStaleBlockClaim` read consistency — MEDIUM

`ISSUE-GC-STALE-CLAIM-READ-CONSISTENCY-01`

`CassandraStore.ReleaseStaleBlockClaim` decides "no claim to release" from a
session-consistency read (`LOCAL_QUORUM` in every shipped profile). Under the
production multi-DC posture (RF 1 per DC) `LOCAL_QUORUM` reads in different DCs do
not intersect, so a claim taken by a worker in another DC can be missed: the
function returns `BlockClaimAbsent`, the caller consumes the candidate, and the
live block stays fenced behind `gc_state='deleting'`. No data loss; permanent
upload refusal until an operator intervenes. The clean fix is coupled to the X1
serial-domain decision (R12) and the code says so.

### B.10 Referenced-orphan lifecycle silent leak — MEDIUM

`ISSUE-GC-REFERENCED-ORPHAN-LIFECYCLE-01`

In S3-orphan recovery, a row refused for still having references increments
`GCAuditEventsTotal{event="gc_s3_orphan_referenced_deferred"}`, logs, and
`continue`s. It is not promoted into a future bucket and has no durable deferred
state, so once the day cursor advances past its bucket nothing revisits it; the
row TTLs at 90 days and the alerting counter goes quiet while the condition
persists. Storage leak, not data loss. The code already carries this as a
documented `KNOWN GAP`.

### B.11 `block_id_mappings` has no reaper — LOW/MEDIUM

`ISSUE-GC-LOGICAL-MAPPING-RETENTION-01`

`DELETE FROM block_id_mappings` appears in **zero** production Go files;
`r11a_mapping_surface_guard_test.go` enforces that at CI time. This was PR #175's
deliberate scope cut — physical GC does not own the logical SHA-1 → SHA-256
mapping — and no separate reaper exists. Stale rows accumulate and can resolve to
a 404 for legacy SHA-1 reads until rematerialization.

---

## C. Additional observations

Not tracked as blockers.

1. **Fence observability is partial, not absent.** `gc_fence` exists, but as a
   *label value* on `block_upload_materialization_retries_total{surface,reason}` —
   a counter of upload retries that collided with a GC fence. What does not exist
   is any measure of fence **age or duration**, so an indefinitely retained fence
   cannot be distinguished from repeated short-lived ones. That matters for
   R22a's `EACH_QUORUM` liveness cost: during a single-DC outage the upload fence
   can become indefinite, and without an age signal the degradation is easy to
   miss — and easy for a future X1 design to treat as solved when it is deferred.

2. **Crash window from PR #176** — a successful S3 delete without a phase advance
   can re-issue the delete. Characterized by tests; no fix, no ticket promotion.

3. **The schema/binary invariant is operational, not enforced.** Nothing fails
   fast if a binary predating a migration talks to a newer schema. With #181 the
   invariant now extends through **016** (`gc_s3_orphans.storage_key`), not just
   014/015. Currently a runbook responsibility.

4. **`GCConfig.DryRun` is runtime-mutable.** `Service.SetDryRun` is reachable from
   `handleGCRun` via the optional `dry_run` field, so the prod `false` is a
   default rather than an immutable pin. Setting it to `true` suppresses destructive
   execution; setting it back to `false` restores normal destructive behavior once
   GC is otherwise enabled and authorized. The trigger admission and override now
   commit together, so a refused trigger does not change the runtime mode.

---

## D. Reading order

1. [CHANGELOG.md](./CHANGELOG.md) — the audit-arc narrative for the #169–#181 series.
2. [GC-X1-CLOSURE-OPTIONS.md](./GC-X1-CLOSURE-OPTIONS.md) — X1 options, race matrix, open questions. Analysis, not a decision.
3. [PROD-SECURITY-READINESS-20260724.md](./PROD-SECURITY-READINESS-20260724.md) — the security readiness snapshot (dated; do not retro-edit its verdict).
4. [OPEN-WORK-INDEX.md](./OPEN-WORK-INDEX.md) — the one-screen live index.
5. [KNOWN_ISSUES.md](./KNOWN_ISSUES.md) — status of record per `ISSUE-*` id.
6. This document — dated re-verification from baseline `a1570b186`, with the
   follow-up fixes verified at `913c3892c`.

---

## E. Not covered here

- The full product backlog — see [V1-PRODUCTION-ROADMAP.md](./V1-PRODUCTION-ROADMAP.md) and [TECHNICAL-DEBT.md](./TECHNICAL-DEBT.md).
- The open High/Medium rows in [OPEN-WORK-INDEX.md](./OPEN-WORK-INDEX.md) not listed in §B — see the scope note at the top.
- X1 closure options. No option is accepted; no P0/P2/P3/P4 code is in flight.
- Encryption posture ([ENCRYPTION.md](./ENCRYPTION.md)), OIDC, sharing crypto and Accounts-M2M hardening — considered adequate in the July audit, not re-verified here.
