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

## Env vars
| Var | Default | Meaning |
|-----|---------|---------|
| `PARITY_BASE_URL` | `http://localhost:18073` | Mobile app origin under test |
| `PARITY_API_URL` | = BASE_URL | API origin (same-origin proxy) |
| `PARITY_AUTH_MODE` | `dev-token` | `dev-token` or `local` (sesameauth login) |
| `PW_WORKERS` | auto | Cap parallel workers (use `3` for the dev server) |
| `PW_HTML` | — | Add HTML reporter when set |

## Constraint
The Docker daemon here cannot bind-mount arbitrary host paths — a containerized
CI run must COPY the suite into the image at build (see `tests/e2e-localauth`).
