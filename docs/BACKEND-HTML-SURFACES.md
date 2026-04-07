# Backend HTML Surfaces

This document maps the HTML surfaces that are still owned by the Go backend and separates them from routes that already rely on frontend shells plus bootstrap JSON.

## Frontend-Owned Public Pages

These routes are not backend HTML debt anymore:

| Surface | Browser Route | Backend Behavior | Source |
|---|---|---|---|
| Public share page | `/d/:token` | Returns bootstrap/JSON APIs; direct non-`dl`/`raw` page rendering is intentionally delegated to the frontend shell | `internal/api/server_routes.go`, `internal/api/v2/sharelink_view.go` |
| Public share file page | `/d/:token/files/?p=...` | Same pattern: frontend shell owns page rendering, backend only serves bootstrap/raw/download | `internal/api/server_routes.go`, `internal/api/v2/sharelink_view.go` |
| Public upload page | `/u/d/:token` | Frontend shell owns page rendering; backend serves bootstrap/upload APIs | `internal/api/v2/sharelink_view.go` |

The backend explicitly returns JSON errors such as `public share pages are served by the frontend shell` for those page routes when they are hit without the frontend shell.

## Frontend-Owned Preview Pages

These authenticated preview routes now land on a frontend-owned standalone shell:

| Surface | Browser Route | Backend Behavior | Frontend Shell |
|---|---|---|---|
| Authenticated file preview | `/lib/:repo_id/file/*filepath` for inline-previewable files | Redirects to `/file-preview/?repo_id=...&p=...` | `/file-preview/` -> `filepreview.html` |
| Historic file preview | `/repo/:repo_id/history/view?...` for previewable files | Redirects to `/file-preview/?repo_id=...&p=...&obj_id=...` | `/file-preview/` -> `filepreview.html` |

The backend still owns download/raw endpoints and auth enforcement for those flows, but no longer owns the page shell itself.

## Backend-Owned HTML Pages

| Template / Source | Browser Route(s) | Handler | Purpose | Migration Fit |
|---|---|---|---|---|
| `onlyoffice_editor.html` | `/lib/:repo_id/file/*filepath` for OnlyOffice-supported files | `FileViewHandler.ViewFile` -> `serveOnlyOfficeEditor` | Full-page OnlyOffice bootstrap shell with signed config | Medium complexity |
| `error_page.html` | Multiple file-view failure paths | `errorPageHTML`, `onlyOfficeEditorHTML`, share/file download failure branches | Small fallback HTML page for user-facing errors inside preview/editor flows | Good candidate now |
| `login_success.html` | `/oauth/callback/` when desktop SSO return URL starts with `seafile://` | `Server.handleOAuthCallback` | Browser-to-desktop completion bridge after OIDC login | Keep in backend for now |

## Route and Ownership Details

### 1. Authenticated file preview

- Registration: `internal/api/v2/fileview.go` registers `/lib/:repo_id/file/*filepath`.
- Behavior:
  - `dl=1` redirects to download.
  - OnlyOffice file types render `onlyoffice_editor.html`.
  - Inline-previewable file types redirect to `/file-preview/`.
  - Other file types redirect to download.
- Current split:
  - Backend still owns auth enforcement plus raw/download endpoints.
  - Frontend owns the preview shell and rendering.

### 2. Historic file preview

- Registration: `internal/api/v2/fileview.go` registers `/repo/:repo_id/history/view`.
- Behavior:
  - Redirects previewable revisions to `/file-preview/` with `obj_id` and `p` query params.
  - Non-previewable revisions still fall back to download.
- Current split:
  - Backend still owns auth enforcement plus raw/download endpoints.
  - Frontend owns the preview shell and rendering.

### 3. OnlyOffice full-page editor

- Registration: same `/lib/:repo_id/file/*filepath` route, selected by file extension and OnlyOffice enablement.
- Behavior:
  - Generates download token and callback URL.
  - Builds OnlyOffice config.
  - Optionally signs JWT.
  - Renders `onlyoffice_editor.html`.
- Migration constraint:
  - A frontend shell can replace the HTML, but it must preserve secure config generation, callback wiring, and error handling from the backend.

### 4. Error fallback page

- Source: `error_page.html` rendered through `templates.RenderString(...)`.
- Current use:
  - File download token failures.
  - Missing preview file states.
  - OnlyOffice config/render failures.
- Migration fit:
  - Best moved alongside preview/editor shells so visual ownership stays in one place.

### 5. Desktop SSO success page

- Registration: `/oauth/callback` in `internal/api/server_routes.go`.
- Behavior:
  - After OIDC code exchange, if `ReturnURL` starts with `seafile://`, backend sets the `sesamefs_auth` cookie, marks the pending SSO token as successful, and renders `login_success.html`.
  - The page attempts `window.close()` and tells the user to return to the desktop application.
- Why it should remain backend-owned for now:
  - It is part of a native-client handoff flow, not a normal SPA page.
  - The browser can reach this state before any frontend bootstrap is available.

## Suggested Migration Order

1. Move `error_page.html` into a frontend-owned shell or shared static error surface.
2. Move `onlyoffice_editor.html` only after defining a stable frontend bootstrap contract for the editor config.
3. Leave `login_success.html` in Go until there is a dedicated design for native-client completion outside server-rendered HTML.