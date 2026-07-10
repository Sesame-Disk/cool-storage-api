# Local Authentication (username / password)

SesameFS supports three authentication paths that can be mixed and matched:

| Path | What it is | Real sessions? | Intended use |
|------|-----------|----------------|--------------|
| **OIDC** | SSO against an external IdP | ✅ | Enterprise / Accounts integration |
| **Dev tokens** | Static bearer tokens in `config.yaml` | ❌ (injected into request context) | Local development only — **never in production** |
| **Local auth** | Real accounts with hashed passwords | ✅ | Self-hosted / open-source installs without an IdP |

This document covers **local auth**. It is **optional** and **disabled by default**.

---

## Architecture

Local auth is served by a **separate, optional container** — `sesameauth` — so the
internet-facing login surface can be scaled and isolated independently, or simply
not run at all.

```
                         ┌────────────────────┐
  POST /auth/local/login │     sesameauth     │  mints a session (SessionManager)
  ───────────────────────▶  (login service)   ├───────────────┐
                         └─────────┬──────────┘               │ writes
                                   │ reads users (read-only)   │ sessions table
                                   │ owns  user_credentials    ▼
                         ┌─────────┴───────────────────────────────────┐
                         │              Cassandra (shared keyspace)     │
                         └─────────┬───────────────────────────────────┘
                                   │ validates the same session
                         ┌─────────┴──────────┐
   any authenticated API │      sesamefs      │
   ───────────────────────▶  (storage service)│
                         └────────────────────┘
```

Key design decisions:

- **No changes to the `users` table.** Credentials live in their own
  `user_credentials` table (migration `009_local_auth.cql`). A password is a
  single-access-pattern secret — never a query/sort dimension — so it is kept out
  of the widely-read `users` aggregate and its admin projections.
- **Sessions are shared, not reinvented.** `sesameauth` mints sessions through the
  same `SessionManager` the storage service already uses, writing the shared
  `sessions` table. The storage service therefore validates a locally-minted
  session identically to an OIDC one — including server-side revocation.
- **Optional dependency.** The storage service has no hard dependency on
  `sesameauth`. OIDC-only or dev-only deployments simply never start it.

---

## Enabling local auth

Set these (see `.env.example`):

```bash
AUTH_LOCAL_ENABLED=true
BOOTSTRAP_ADMIN_EMAIL=superadmin@sesamefs.local   # must map to a seeded user
BOOTSTRAP_ADMIN_PASSWORD=change-me-to-a-strong-password
```

Then start the stack plus the auth service:

```bash
docker compose up -d                          # storage + deps
docker compose --profile auth up -d sesameauth # optional login service
```

On first boot the storage service attaches the bootstrap password to the seeded
admin user (idempotent; it never overwrites an existing password).

### Configuration reference

| Env var | Default | Meaning |
|---------|---------|---------|
| `AUTH_LOCAL_ENABLED` | `false` | Master toggle for local auth |
| `AUTH_LOCAL_MIN_PASSWORD_LENGTH` | `8` | Minimum password length (policy) |
| `AUTH_LOCAL_MAX_FAILED_ATTEMPTS` | `5` | Failed logins per `email\|ip` before lockout (`0` disables) |
| `AUTH_LOCAL_LOCKOUT_DURATION` | `15m` | How long an actor is blocked after lockout |
| `BOOTSTRAP_ADMIN_EMAIL` | — | Seeded user to receive a first password |
| `BOOTSTRAP_ADMIN_PASSWORD` | — | The bootstrap password (set once) |

---

## Endpoints (`sesameauth`)

### `GET /api/v2.1/auth/methods`
Advertises which methods are enabled so a login UI can render the right options.
```json
{ "local": true, "oidc": false }
```

### `POST /api/v2.1/auth/local/login`
```json
{ "email": "user@example.com", "password": "..." }
```
On success returns a session token (also set as the `sesamefs_auth` cookie):
```json
{
  "token": "…", "user_id": "…", "org_id": "…",
  "email": "…", "name": "…", "role": "…",
  "expires_at": 1783574317, "must_change_password": false
}
```
`401` invalid credentials · `429` locked out · `403` account inactive.

### `POST /api/v2.1/auth/local/change-password`
Authenticated (send the session token). Verifies the current password.
```json
{ "current_password": "…", "new_password": "…" }
```

Use the returned token against the **storage** service exactly like any session:
```bash
curl -H "Authorization: Token <token>" http://localhost:8080/api2/account/info/
```

---

## Admin user management (storage service)

When local auth is enabled, the existing org-admin endpoints manage credentials:

- `POST /api/v2.1/org/:org_id/admin/users/` — create a user. Include
  `"password"` to set it explicitly, or omit it to receive a one-time
  `temp_password` in the response (with `must_change_password: true`).
- `PUT  /api/v2.1/org/:org_id/admin/users/:email/set-password/` — set/reset a
  password. Body `{"password": "..."}` or omit to generate a temporary one.

Accounts are **admin-created**; there is no public self-service registration.

---

## Security notes

- Passwords are hashed with **bcrypt** (`golang.org/x/crypto/bcrypt`). Plaintext
  is never stored or logged.
- **Lockout** is keyed by `email|ip` (see `local_login_failures`, 24h TTL) so one
  abusive client cannot lock a victim's account across all clients, while a
  focused attack on a single account is still throttled.
- Unknown emails still consume a failure attempt, so login timing/lockout can't be
  used to enumerate which accounts exist.
- Temporary passwords are returned to the admin **once** and are flagged
  `must_change_password`.

---

## Reverse-proxy note

`sesameauth` listens on its own port (`8091` on the host by default). In a
browser deployment, route the auth paths to it on the same origin as the app so
the session cookie is first-party, e.g. in nginx:

```nginx
location /api/v2.1/auth/local/  { proxy_pass http://sesameauth:8080; }
location /api/v2.1/auth/methods { proxy_pass http://sesameauth:8080; }
# everything else → storage service
location / { proxy_pass http://sesamefs:8080; }
```

---

## Testing

End-to-end tests drive the real `sesameauth` + `sesamefs` containers:

```bash
docker compose --profile auth --profile auth-test run --rm localauth-e2e
```

Unit tests for the hashing/policy layer:

```bash
docker compose run --rm --no-deps gotest go test ./internal/localauth/...
```
