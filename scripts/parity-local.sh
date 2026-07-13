#!/usr/bin/env bash
# Run the mobile PWA parity suite against a live NON-DEV / LOCAL-AUTH stack, the
# reproducible way — no ad-hoc flags, no false upload/service-worker failures.
#
# What it does:
#   1. Idempotently provisions the harness's local-auth users (user@/admin@ in a
#      real tenant org; superadmin bootstrap is assumed already seeded).
#   2. Runs Playwright INSIDE the `mobile-test` container (the host cannot launch
#      browsers here) against the app on the compose network.
#   3. Uses a loopback secure-context proxy (PARITY_PROXY_TARGET) so the browser
#      sees http://localhost:18073 — a SECURE context — making Service Workers +
#      crypto.subtle uploads work exactly as they do for a real user.
#
# Prereqs: the stack is already up in local-auth mode
#   (docker compose --profile auth up -d ...; MOBILE_FRONTEND_HOST_PORT=18073).
#
# Usage:
#   scripts/parity-local.sh                 # full matrix (all viewports)
#   scripts/parity-local.sh onlyoffice.spec.ts files.spec.ts   # specific specs
#   PW_PROJECT=phone scripts/parity-local.sh   # single viewport project
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

BASE_URL="${PARITY_BASE_URL:-http://localhost:18073}"

echo "[parity-local] provisioning local-auth users ..."
PARITY_BASE_URL="$BASE_URL" node mobile-frontend/e2e-parity/provision-local-users.mjs

# Rebuild so any spec/helper/config edits are baked into the runner image.
echo "[parity-local] building mobile-test image ..."
docker compose --profile test build mobile-test >/dev/null

PROJECT_ARG=""
[ -n "${PW_PROJECT:-}" ] && PROJECT_ARG="--project=${PW_PROJECT}"

echo "[parity-local] running parity suite ${*:-<full matrix>} ..."
docker compose --profile test run --rm \
  -e PARITY_BASE_URL=http://localhost:18073 \
  -e PARITY_API_URL=http://localhost:18073 \
  -e PARITY_AUTH_MODE=local \
  -e PARITY_PROXY_TARGET="${PARITY_PROXY_TARGET:-mobile-frontend:80}" \
  -e PARITY_PROXY_PORT=18073 \
  -e PW_WORKERS="${PW_WORKERS:-4}" \
  -e PW_RETRIES="${PW_RETRIES:-1}" \
  mobile-test bash -lc "npx playwright test --config=playwright.parity.config.ts ${PROJECT_ARG} $*"
