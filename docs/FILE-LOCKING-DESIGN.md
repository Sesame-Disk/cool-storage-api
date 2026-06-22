# File locking: how it should work (exclusive locks vs OnlyOffice co-editing)

**Date:** 2026-06-18
**Status:** design note / advice — not yet implemented
**Area:** `internal/api/v2/files.go` (lock handler + write paths), `internal/api/v2/onlyoffice.go`, `internal/db/migrations` (`locked_files`)
**Related:** known-gap tests `sesamefs-concurrency.spec.ts` (lock steal) and `sesamefs-locks.spec.ts` (write enforcement); bug [BUG-FILE-DETAIL-MODIFIER-20260618](BUG-FILE-DETAIL-MODIFIER-20260618.md) is unrelated.

There are two legitimate, *opposite* requirements:

- **Exclusive lock** — one user "checks out" a file so nobody else can change it (desktop/sync edit of a binary, or a deliberate manual lock). Single writer.
- **OnlyOffice co-editing** — many users edit one online document *at the same time* through the OnlyOffice document server, which does the real-time merge. Many writers, but only *through OnlyOffice*; offline/sync clients must still be kept out so they don't clobber the live session.

These can't be served by one undifferentiated lock. The fix is a lock that carries a **type**.

## Current state (what exists today)

- Table `locked_files(repo_id, path, locked_by, locked_at)` — [`migrations/001_initial_schema.cql:991`](../internal/db/migrations/001_initial_schema.cql#L991). No lock **type**, no **expiry**.
- `LockFile` — [`internal/api/v2/files.go:4146`](../internal/api/v2/files.go#L4146): `lock` is a blind `INSERT` (no "already locked?" check → silently steals); `unlock` is a blind `DELETE` (no ownership check → anyone with `modify` can release someone else's lock). Only the `modify` permission flag is checked.
- **No write path enforces the lock** — upload/update/replace, rename, move, delete, and the seafhttp commit/block path never read `locked_files`. The lock is **cosmetic**: it shows in directory listings (`is_locked`, `lock_owner`, `locked_by_me`) but stops nothing.
- **OnlyOffice ignores locks entirely** — `GetEditorConfig` sets no lock when a doc is opened; the save callback `EditorCallback` ([`onlyoffice.go`](../internal/api/v2/onlyoffice.go)) writes the file back and sets/clears no lock. OnlyOffice is `enabled: false` in the cluster configs.
- No library-owner/admin distinction in the permission model (`shares.permission` is only `rw`/`r`), so there's no one allowed to force-unlock.

## Recommended design

### 1. Give the lock a type and an expiry
Extend `locked_files` (or add columns): `lock_type TEXT` (`manual` | `online_office`), `expires_at TIMESTAMP` (nullable). Keep `locked_by`, `locked_at`.

### 2. Exclusive (`manual`) locks
- **Acquire:** reject (`409`) if the file is already locked by someone else; allow refresh by the owner. Library owner/admin may force-acquire.
- **Release:** only the lock owner, or library owner/admin (force-unlock). Reject others (`403`). *(This is the gap the `sesamefs-concurrency.spec.ts` "lock steal" known-gap test encodes.)*
- **Enforce on every write:** upload/update/replace, rename, move, delete, **and** the seafhttp commit/block path must reject a write to a file `manual`-locked by a *different* user (`403`). *(This is the gap the new `sesamefs-locks.spec.ts` "rejects writes from a different user" known-gap test encodes.)*
- **Auto-expire** after a configurable TTL (e.g. hours) and support `refresh-lock`, so a crashed client can't lock a file forever.

### 3. OnlyOffice (`online_office`) locks — collaborative
- When a doc is opened for editing (`GetEditorConfig`), set an `online_office` lock for the path if one doesn't exist. This lock is **shared, not exclusive**: it must *not* block other users from joining the same OnlyOffice session (OnlyOffice merges their edits), but it **does** block `manual` lock acquisition and all sync/direct writes (they get `423 Locked` / `403` "locked by online office").
- The **OnlyOffice save callback is the only authorized writer** — `EditorCallback` (status `2`/`6`) must be allowed to publish past the lock, and on "no editors left" (status `4`, and the `onlyoffice_doc_keys` mapping is torn down) it must **release** the `online_office` lock. Use the existing doc-key/session mapping for presence so the lock clears when the last editor leaves.
- Never auto-promote an `online_office` lock to exclusive — concurrent editors are the expected case.

### 4. Permission/role
Use `libraries.owner_id` to recognise the library owner (and any future admin role) as the principal allowed to **force-unlock** either lock type. The current `rw`/`r` split is not enough.

## Minimal implementation order
1. ✅ **DONE (2026-06-19).** Enforce lock ownership in `LockFile`, **atomically via LWT** (no check-then-write race): acquire is `INSERT … IF NOT EXISTS` (`db.AcquireFileLock`) — a different user gets `409`, the owner gets a refresh; unlock is `DELETE … IF locked_by = ?` (`db.ReleaseFileLock`) — a non-owner gets `403`, a missing lock is an idempotent `200`. This closes the multi-region race where two requests could both pass a read check and then upsert. Covered by `TestLockFile_ConflictReturns409`.
2. ✅ **DONE (2026-06-19).** Enforce `manual` locks on the write paths via shared `db` helpers and injectable seams:
   - `db.FileLockedByOther` (exact path) and `db.SubtreeLockedByOther` (path + descendants), both fail-closed (`ErrFileLockStatusUnavailable` → `503`) so an unverifiable lock never silently allows a write.
   - **Exact-path** enforcement: single-shot upload overwrite (`UploadFile`), chunked/seafhttp overwrite (`HandleUpload`, gated on `replace`).
   - **Subtree** enforcement (blocks acting on a folder that contains a file locked by another user): `RenameFile`, `DeleteFile`, `MoveFile`, `BatchDeleteItems`.
   - **Batch move/copy via the `sync-/async-batch-*` endpoints and `/file/move`** is enforced in `BatchOperationHandler.processSingleItem`: a `move` checks the **source subtree**; a `replace` conflict policy checks the **destination** path.
   - **The `/file/copy/` path does NOT go through `processSingleItem`** — `CopyFile` and `copyBatchFiles` call `copyItemWithinRepoWithRetry`, which enforces the **`replace` destination lock** directly (autorename/skip are exempt since they never overwrite).
   - All of the above map to `403` (`ErrBatchItemLocked`) / `503` (`ErrBatchLockStatusUnavailable`). Covered by `TestProcessSingleItem_*`, `TestCopyItemWithinRepo_*`, and the `file_lock_enforcement_test.go` handler tests.
2b. ✅ **DONE (2026-06-19).** Keep lock rows consistent when the **owner** restructures a locked path (the subtree pre-check guarantees any lock under the path is the operator's own):
   - **Rename** → `db.RelocateLocksUnder` rewrites the operator's locks from the old path/prefix to the new one (`rewriteLockedPath`, unit-tested).
   - **Delete / batch-delete** → `db.ClearLocksUnder` drops the operator's now-orphaned locks under the deleted path.
   - **Move** (same-repo, via `processSingleItem`/`processSameRepoMove`) → clears the source locks (the destination name is autorename-dependent, so we clear rather than guess; the owner re-locks at the destination).
   - All run **after** the FS commit succeeds and only touch `locked_files`; failures are logged, never failing the structural op.

3. ⏳ **PENDING.** Add `lock_type` + expiry; wire OnlyOffice open→lock and callback→unlock; allow co-editors; block sync writes. Requires an OnlyOffice document server to test end-to-end (the `sesamefs-locks.spec.ts` OnlyOffice case skips until one is configured).

## Known remaining debt (not blockers, tracked for follow-up)

- **Move relocation across repos / autorename name** — same-repo move clears source locks rather than relocating to the exact (possibly autorenamed) destination; cross-repo move lock maintenance is not wired. Acceptable: no stale/phantom rows result, only a dropped own-lock.
- **`SubtreeLockedByOther` / lock maintenance scan the whole per-repo lock partition** — fine while locks-per-repo stays low; revisit (secondary index or bounded scan) if a repo can accumulate many simultaneous locks, as folder rename/move/delete pay one scan each.
- **Lock TTL/expiry** — locks are still permanent (no `expires_at`), so a crashed client can hold one until an owner/admin unlocks. Auto-expiry + `refresh-lock` are part of step 3.
- **Public upload/update-link actor semantics** — a link upload acts as the token's owner, so it would pass an owner lock check. Decide whether link uploads should be treated as an external actor (subject to locks) before relying on locks against link writes.
- ✅ **DONE (2026-06-22). `RevertFile` / `RevertDirectory`** — a `replace`-policy revert overwrites the target, so it is now lock-checked: `RevertFile` enforces the **exact-path** lock, `RevertDirectory` the **subtree** lock, both only when `conflict_policy == "replace"` (autorename/keep_both restore under a new name and skip does nothing, so they never overwrite a locked file — exempt, mirroring the copy path). Maps to `403` (`lock_owner` in body) / `503` on unverifiable lock. Covered by `TestRevertFile_*` and `TestRevertDirectory_ReplaceRejectsLockedSubtree`.
