# Bug: `file/detail` reports the requesting user as the file's last modifier

**Date:** 2026-06-18
**Severity:** Low (cosmetic / metadata-only — no data, replication, sharing, or permission impact)
**Area:** `internal/api/v2/files.go` — `GetFileDetail`
**Found via:** the multi-region collaboration E2E spec (`mobile-frontend/e2e-sesamefs/sesamefs-mr-collab.spec.ts`), which logged a file's modifier as different depending on who queried it.

## What happens

`GET /api/v2.1/repos/:repo_id/file/detail/?p=<path>` returns `last_modifier_email` / `last_modifier_name` set to **whoever is making the request**, not the user who actually last modified the file. So every caller sees themselves as the modifier of every file.

Live evidence — a file written by **admin**, then read by two different users against both regions:

| Query | `last_modifier_email` | `mtime` |
|---|---|---|
| USA, as admin | `…0001` (admin) | 1781765196 |
| EU, as admin  | `…0001` (admin) | 1781765196 |
| USA, as user  | `…0002` (user) ❌ | 1781765196 |
| EU, as user   | `…0002` (user) ❌ | 1781765196 |

The value tracks the **caller**, not the region and not the real author. `mtime` (the modified *date*) is correct and identical everywhere, and content/replication are unaffected — this is purely the attribution field in this one endpoint.

## Root cause

In `GetFileDetail`, the modifier is hard-coded from the authenticated caller instead of the file's stored author:

- [`internal/api/v2/files.go:1694`](../internal/api/v2/files.go#L1694) — `userEmail := userID + "@sesamefs.local"` where `userID` is the **requesting** user (`c.GetString("user_id")`).
- [`internal/api/v2/files.go:1721-1722`](../internal/api/v2/files.go#L1721) — that `userEmail` is then emitted as `last_modifier_email` and `last_modifier_name`.

The directory-listing path *intends* to surface the real author — it assigns `dirent.ModifierEmail = entry.Modifier` at [`internal/api/v2/files.go:557`](../internal/api/v2/files.go#L557) and [`internal/api/v2/files.go:3692-3693`](../internal/api/v2/files.go#L3692) — but in practice `entry.Modifier` comes back **empty**, so a `GET .../dir/?p=/` listing omits the modifier entirely (verified live: an uploaded file's dirent has no `modifier_email`). In other words the real author is **not currently persisted/surfaced anywhere**, and `file/detail` papers over the gap by returning the caller instead.

## Proposed fix

Two parts:

1. **Persist the writing user as the file's modifier** at write time (upload, update/replace, copy/move) so the fs entry / commit carries the real author and `entry.Modifier` is populated.
2. **Read that real modifier in `GetFileDetail`** instead of the caller — and keep `userEmail`/`userID` only where the *caller's* identity is genuinely needed (permission checks, `starred`):

```go
modifier := entry.Modifier            // the real author, once persisted
"last_modifier_email":         modifier,
"last_modifier_name":          strings.Split(modifier, "@")[0],
"last_modifier_contact_email": modifier,
```

Until the author is persisted, `file/detail` must at minimum **not** attribute the file to the requester.

A regression guard already exists: `sesamefs-sharing.spec.ts` has a `test.fail()` test — "file/detail reports the real author, not the requester (known gap)" — that writes a file as the owner and asserts the recipient does **not** see themselves as its modifier. It is expected-to-fail today; when this bug is fixed it will pass and Playwright will flag the unexpected pass so the marker (and this doc) can be removed. The richer multi-user assertions in `sesamefs-mr-collab.spec.ts` currently only log the modifier and assert it is a known participant — tighten them once the fix lands.

## Notes

- The desktop SPA does not currently render this field, so user-facing impact today is near zero — but any API client or a future "Modified by" column would show wrong attribution.
- Worth confirming `entry.Modifier` is populated for all write paths (upload, update/replace, copy/move) so the fix is consistent across operations.

## Resolution (fix/file-detail-modifier)

Implemented in `internal/api/v2/files.go`. Both parts of the proposed fix landed plus identity normalization:

1. **Persist the writer as the modifier** on every write path — `modifierIdentityForUser(userID)` (`<uid>@sesamefs.local`) is stamped on `CreateFile`, stored-upload finalize, OnlyOffice save, and revert; copy/move preserve the source's modifier (content didn't change). Desktop-client commits already carry a real-address modifier via the Seafile dirent.
2. **Read the real modifier, never the requester.** `GetFileDetail` resolves identity via `resolveModifierIdentity` → `composeLastModifierIdentity`:
   - real address (desktop client) → resolved to the account display name, else local-part;
   - synthetic `<uuid>@sesamefs.local` (web/OnlyOffice) → resolved to the real account by user id;
   - blame fallback (commit-history walk, bounded at `fileLastModifierWalkCap=64`) **only** for legacy entries with no stamped modifier, and **skipped for directories**;
   - unresolved → **empty**, never the caller. The synthetic `<uid>@sesamefs.local` fallback is blanked by `publicModifierIdentity` so the internal marker never leaks to clients.
3. **Directory listings normalized too** — `ListDirectory` / `ListDirectoryV21` run the same resolution via `newPersistedModifierResolver`, so they surface the real identity instead of the raw `<uuid>@sesamefs.local` marker (the old gap noted above).

### Registered performance risk — unpaginated listing × per-entry modifier resolution

`ListDirectory` / `ListDirectoryV21` are **not paginated**: they return every entry and now resolve a modifier per entry. Mitigations in place: a **request-scoped cache** keyed by raw modifier (each distinct modifier resolved once), and a short-circuit in `lookupUserNameByEmail` that skips the always-missing `users_by_email` query for synthetic `<uuid>@sesamefs.local` markers (one `users` lookup per distinct modifier, common case).

- **Typical** (a directory with few distinct uploaders): negligible — a handful of queries.
- **Pathological** (thousands of files each by a different user): up to one `users` query per distinct user, issued **serially** on a hot listing path → possible latency regression.

If large multi-uploader directories become common, the fix is to **batch-resolve distinct modifiers** (single multi-key fetch) or **paginate the listing**, rather than micro-optimizing the per-entry path. See `newPersistedModifierResolver` in `internal/api/v2/files.go` for the in-code note.
