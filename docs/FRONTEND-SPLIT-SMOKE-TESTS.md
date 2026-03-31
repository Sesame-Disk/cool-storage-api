# Frontend Split Smoke Tests

This checklist validates the Phase 1 desktop split where the React frontend is served separately from the Go backend and hydrates dynamic state from the bootstrap contract before mount.

## Scope

Run these tests after changes to any of the following:

- `internal/api/bootstrap.go`
- `frontend/public/*.html`
- `frontend/src/bootstrap/runtime-bootstrap.js`
- `frontend/src/app-entry.js`
- `frontend/src/pages/org-admin/bootstrap-entry.js`
- `frontend/src/pages/sys-admin/bootstrap-entry.js`
- `frontend/nginx.conf`
- `nginx/nginx.conf.template`
- `frontend/src/setupProxy.js`

## Environment

Expected local environment:

- `docker compose up -d --build`
- Desktop frontend reachable on `http://localhost:3000`
- Backend reachable through the frontend origin via nginx proxy

Automation entrypoint:

- `cd mobile-frontend && npm run test:smoke`
- Legacy alias kept for compatibility: `cd mobile-frontend && npm run test:e2e:desktop-split`
- Override credentials or target with env vars when needed:
	- `DESKTOP_BASE_URL`
	- `DESKTOP_SMOKE_USER_EMAIL`
	- `DESKTOP_SMOKE_USER_PASSWORD`
	- `DESKTOP_SMOKE_ORG_ADMIN_EMAIL`
	- `DESKTOP_SMOKE_ORG_ADMIN_PASSWORD`
	- `DESKTOP_SMOKE_SYS_ADMIN_EMAIL`
	- `DESKTOP_SMOKE_SYS_ADMIN_PASSWORD`

Credential note:

- Local Docker dev uses built-in `@sesamefs.local` users and dev-token passwords.
- Any non-local target must provide explicit `DESKTOP_SMOKE_*` credentials.

Test users:

- regular org user
- org admin or org owner
- platform superadmin
- unauthenticated browser session

## Smoke Matrix

### 1. Login redirect

1. Open `/` in a clean browser session.
2. Confirm redirect to `/login/`.
3. Log in as a regular user.
4. Confirm redirect back to the requested page.

Expected:

- Login page renders without missing config or blank shell.
- Successful login returns to the original route.

### 2. Logout

1. Log in.
2. Trigger logout from the account menu.
3. Open `/` again.

Expected:

- Session is cleared.
- App redirects back to `/login/`.

### 3. Dashboard

1. Log in as a regular user.
2. Open `/`.
3. Confirm main navigation renders.
4. Confirm quota banner and dashboard widgets do not crash.

Expected:

- Main app loads from `index.html`.
- `window.app.pageOptions` values are available before first render.

### 4. Library view

1. Open a library from the dashboard or left nav.
2. Open a folder.
3. Open a file preview.

Expected:

- Library listing loads.
- Directory navigation works.
- File view routes under `/lib/` and `/repo/` proxy correctly.

### 5. Share link flow

1. Open a library where the current user can share.
2. Open the share dialog.
3. Create a share link.
4. Open the generated `/d/...` link in a new tab.

Expected:

- Share controls render according to bootstrap permissions.
- Share link page loads through the frontend public shell plus bootstrap API.
- Password-protected links block `dirents` before verification and load normally after password submission.

### 6. Upload link flow

1. Open the share dialog for a writable folder.
2. Create an upload link.
3. Open the generated `/u/...` link in a new tab.

Expected:

- Upload link controls render according to bootstrap permissions.
- Upload link page loads through the frontend public shell plus bootstrap API.
- Password-protected upload links block upload URL issuance before verification and return a valid `/seafhttp/upload-api/` target after password submission.

### 7. Org admin shell

1. Log in as an org admin or owner.
2. Open `/org/`.
3. Navigate to users, groups, links, and subscription pages.
4. Repeat with a non-org-admin user.

Expected:

- Org admin routes load from `orgadmin.html`.
- Authorized users see the panel.
- Unauthorized users see the access-denied screen from the bootstrap loader.
- No second bootstrap fetch is required after bundle import.

### 8. Sys admin shell

1. Log in as platform superadmin.
2. Open `/sys/`.
3. Navigate to users, groups, orgs, repos, and logs.
4. Repeat with a non-superadmin user.

Expected:

- Sys admin routes load from `sysadmin.html`.
- Authorized users see the panel.
- Unauthorized users see the access-denied screen from the bootstrap loader.
- Modules that read `window.sysadmin.pageOptions` at import time work correctly.

### 9. Subscription page

1. Log in as an org owner.
2. Open `/subscription/`.
3. Open `/org/subscription`.

Expected:

- Standalone subscription page loads via `subscription.html`.
- Org-scoped subscription view loads inside the org admin shell.

### 10. SAML and org custom routes

Run only if the deployment uses these features.

1. Open `/saml2/` routes used by the deployment.
2. Open `/org/custom/` routes used by the deployment.

Expected:

- Routes hit the backend, not the SPA shell.
- No frontend `try_files` fallback captures these requests.

## Exit Criteria

Phase 1 split is considered healthy when:

- all routes above render from the correct shell or backend target
- unauthorized admin access is blocked before app mount
- the main app does not rely on `account/info` as the normal first hydration path
- HTML shells stay minimal and do not carry large dynamic `pageOptions` payloads
- no route depends on Go-side HTML string substitution for user or org context