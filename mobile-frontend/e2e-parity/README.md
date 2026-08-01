# Parity E2E suite (`e2e-parity/`)

Multi-viewport, full-CRUD tests that drive the **mobile app against a live
backend**. Every spec runs on all 6 viewport projects (small/standard/large
phone, portrait/large tablet, landscape). Coverage is tracked in
[`PARITY-MATRIX.md`](./PARITY-MATRIX.md) — the suite is "done" when it's 100% ✅.

## How auth works
`global-setup.ts` logs in each role (`user`, `admin`) against the live stack and
saves a `storageState` with the app's `seahub_token` in localStorage. Specs
start authenticated. Org-admin specs opt into the admin session with
`test.use({ storageState: authStateFile('admin') })`.

## Run modes

**A. Dev loop (live source + HMR)** — fastest while building features. Point the
Astro dev server's API proxy at the running stack's web nginx, then run the
suite against the dev server. Cap workers so the single dev server isn't
overwhelmed (avoids flaky nav timing):
```bash
MOBILE_DEV_API=http://localhost:18000 npm run dev        # terminal 1 (serves :4321)
PARITY_BASE_URL=http://localhost:4321 PW_WORKERS=3 \
  npx playwright test --config=playwright.parity.config.ts   # terminal 2
```

**B. Against the built container (production build)** — the default. Assumes the
`mobile-frontend` container is up (host port 18073):
```bash
npm run test:parity            # PARITY_BASE_URL defaults to http://localhost:18073
```

**C. Non-dev / local-auth stack, inside the runner container** — the reproducible
way when the host can't launch browsers. One command provisions the local-auth
users, builds the `mobile-test` image, and runs the suite through a
secure-context proxy (see below):
```bash
scripts/parity-local.sh                       # full matrix
scripts/parity-local.sh onlyoffice.spec.ts    # specific specs
PW_PROJECT=phone scripts/parity-local.sh      # single viewport
```

## Secure context (why uploads / service worker can falsely fail)
The PWA uses **secure-context-only** browser APIs: Service Workers and
`crypto.subtle` (upload SHA-256 hashing). A secure context is HTTPS **or**
`http://localhost` / `127.0.0.1`. When the suite runs inside the `mobile-test`
container and reaches the app via a container-DNS host (`http://mobile-frontend`),
that's an **insecure** context, so uploads + SW silently fail — an environment
artifact, not a PWA bug. Set `PARITY_PROXY_TARGET=mobile-frontend:80` and point
`PARITY_BASE_URL` at `http://localhost:18073`; `global-setup` starts a loopback
forwarder (`helpers/secure-proxy.ts`) so the browser sees a secure `localhost`
origin. `scripts/parity-local.sh` wires this up for you.

## Local-auth user provisioning
In non-dev mode (`AUTH_DEV_MODE=false`) only the bootstrap superadmin is seeded.
`node e2e-parity/provision-local-users.mjs` idempotently creates `user@` and
`admin@` (org admin) in a real tenant org with the harness passwords. Run it once
after bringing the stack up (or just use `scripts/parity-local.sh`, which calls it).

## Env vars
| Var | Default | Meaning |
|-----|---------|---------|
| `PARITY_BASE_URL` | `http://localhost:18073` | Mobile app origin under test |
| `PARITY_API_URL` | = BASE_URL | API origin (same-origin proxy) |
| `PARITY_AUTH_MODE` | `dev-token` | `dev-token` or `local` (sesameauth login) |
| `PARITY_PROXY_TARGET` | — | `host:port` to forward `127.0.0.1:PARITY_PROXY_PORT` to (secure context) |
| `PARITY_PROXY_PORT` | `18073` | Loopback port the secure-context proxy listens on |
| `PW_WORKERS` | auto | Cap parallel workers (use `3` for the dev server) |
| `PW_HTML` | — | Add HTML reporter when set |

## Constraint
The Docker daemon here cannot bind-mount arbitrary host paths — a containerized
CI run must COPY the suite into the image at build (see `tests/e2e-localauth`).
