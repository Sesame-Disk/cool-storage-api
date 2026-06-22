# Bug/gap: share links have no "internal / org-only" scope (all links are public)

**Date:** 2026-06-18
**Severity:** Medium (access-control gap — a link intended for internal/org use is openable by anyone)
**Area:** `internal/api/v2/share_links.go` (create), `internal/api/v2/sharelink_view.go` (access)
**Guard test:** `sesamefs-bugs.spec.ts` → "an internal/org-scoped share link is not accessible anonymously" (`@bug`).

## RESOLUTION (2026-06-19): not a bug — internal/org-only links already exist as smart-links

This was re-examined and is **WONTFIX as framed.** SesameFS already has a first-class
org-only link: the **smart-link**. It is created via `GetSmartLink`
(`GET /api/v2.1/smart-link/?repo_id=…&path=…`), persisted in `share_links` with
`link_type='internal'`, and its access path **already enforces exactly the intended
audience**:

- `ResolveSmartLink` ([`internal/api/v2/files.go`](../internal/api/v2/files.go)) runs
  behind `smartLinkAuthMiddleware` ([`internal/api/server.go`](../internal/api/server.go)),
  which **redirects unauthenticated visitors to login** — so anonymous access is denied.
- It then requires `link_type == "internal"` (else 404) **and** `org_id == userOrgID`
  (else `403 "access denied"`) — so a member of a *different* org is denied too.

So the two link audiences are, by design:

- **`share` / `upload` links — public by token** (anyone with the link). This is intended
  and must not change.
- **smart-links (`link_type='internal'`) — authenticated, same-org only.** This is the
  "internal / intra-org" link the finding asked for, and it is already enforced.

The original finding mis-modeled "internal" as a missing *scope on public share links*.
It is instead a *separate link type* that already exists and is already access-controlled.

**Action taken:** none in the backend. The `@bug` guard test
("an internal/org-scoped share link is not accessible anonymously") targets the **wrong
path** — it creates the link via `POST /api/v2.1/share-links/` (the public link endpoint)
and probes `/share-links/:token/dirents/` (the public access path). The real internal
link is created with `GetSmartLink` and accessed via `/smart-link/:token`. The test
should be **retargeted to the smart-link flow** (or removed), not used to drive a
redundant scope column onto public share links.

---

## Original finding (kept for context — superseded by the resolution above)

## What happens

A SesameFS **share link** is purely **token-scoped and public**: anyone who has the token — a different organization, or an anonymous visitor — can open it (subject only to an optional password, expiry, and download cap). There is **no way to create a link that is restricted to the owner's organization** (an "internal / intra-org" link) so that non-members are denied. The only access-controlled sharing is the **library user/group share** (which *is* org-scoped and correctly denies non-recipients — see the cross-region test in `sesamefs-mr-sharelinks.spec.ts`).

So the scenario "share a link internally so user C (not a recipient / outside the org) cannot open it" is not achievable today.

## Root cause

- **Create** — `CreateShareLink` ([`internal/api/v2/share_links.go`](../internal/api/v2/share_links.go)) accepts `repo_id`, `path`, `password`, `expire_*`, `permissions`. There is **no scope/audience/internal param**; the request struct `ShareLinkCreateRequest` has no such field.
- **Access** — `resolveShareLink` ([`internal/api/v2/sharelink_view.go`](../internal/api/v2/sharelink_view.go)) looks the link up **by token only** and gates on `active`, expiry, download cap, and password. There is **no org-membership or login check**. The `dirents` endpoint (`GET /api/v2.1/share-links/:token/dirents/`) is reachable anonymously.
- The schema's `share_links.link_type` can be `'share' | 'upload' | 'internal'`, but the user-facing `'share'` links carry no audience enforcement; `'internal'` is only used today for markdown smart-links scoped to the same org/library, not as a user-creatable "org-only" link.

## Proposed fix

1. Add an explicit **scope/audience** to share links — e.g. `scope: 'public' | 'internal'` (persist it on `share_links`).
2. On the access path (`resolveShareLink` / the `dirents`/`/d/:token` handlers): for an `internal` link, **require an authenticated user whose `org_id` matches the link's `org_id`** (and otherwise return 403/404). Public links keep current behavior.
3. Add the param to `CreateShareLink` and surface it in the share dialog UI.

This preserves public links (anyone with the token, by design) while making "internal" links genuinely org-restricted, so a non-recipient/outsider is denied. The guard test asserts the minimal invariant: an `internal`-scoped link must reject anonymous access (it does not today).
