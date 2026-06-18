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
1. Enforce lock ownership in `LockFile` (acquire-conflict + unlock-owner check). Flips the concurrency known-gap test.
2. Enforce `manual` locks on the write paths (shared helper used by upload/update/move/rename/delete + seafhttp commit). Flips the `sesamefs-locks.spec.ts` known-gap test.
3. Add `lock_type` + expiry; wire OnlyOffice open→lock and callback→unlock; allow co-editors; block sync writes. Requires an OnlyOffice document server to test end-to-end (the `sesamefs-locks.spec.ts` OnlyOffice case skips until one is configured).
