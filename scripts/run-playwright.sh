#!/bin/bash
# Run the SesameFS Playwright E2E workload in Docker against the multi-region stack.
#
# One command boots the multi-region environment (nginx LB + USA/EU regions +
# shared Cassandra/MinIO) plus a React frontend, then runs every spec under
# mobile-frontend/e2e-sesamefs/ inside a Playwright container. Exit code is the
# narrow pass/fail signal: 0 == all specs passed, non-zero == something failed.
#
# Add more coverage by dropping any `*.spec.ts` into mobile-frontend/e2e-sesamefs/.
# It is picked up automatically — no edits to this script needed.
#
# Usage:
#   ./scripts/run-playwright.sh            # up (if needed) + run the suite
#   ./scripts/run-playwright.sh up         # just boot the stack + frontend
#   ./scripts/run-playwright.sh test       # run the suite (assumes stack is up)
#   ./scripts/run-playwright.sh status     # show service status + URLs
#   ./scripts/run-playwright.sh logs [svc] # tail logs
#   ./scripts/run-playwright.sh down       # tear the stack down (keeps volumes)
#   ./scripts/run-playwright.sh down -v    # tear down and delete volumes
#
# Ports (3000/8080/8082 from the list were already taken by host processes):
#   8000 nginx LB | 8088 USA region | 8081 EU region | 5173 web UI
#   5000 MinIO S3 | 8002 MinIO console | 9042 Cassandra (infra)
set -euo pipefail

# Colors
RED='\033[0;31m'; GREEN='\033[0;32m'; BLUE='\033[0;34m'; YELLOW='\033[0;33m'; NC='\033[0m'
log_info()    { echo -e "${BLUE}[INFO]${NC} $1"; }
log_success() { echo -e "${GREEN}[OK]${NC} $1"; }
log_warn()    { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error()   { echo -e "${RED}[ERROR]${NC} $1"; }

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$PROJECT_DIR"

# --- Ports (override-able, but defaulted to the approved free list) ---------
export LB_HOST_PORT="${LB_HOST_PORT:-8000}"
export SESAMEFS_USA_HOST_PORT="${SESAMEFS_USA_HOST_PORT:-8088}"
export SESAMEFS_EU_HOST_PORT="${SESAMEFS_EU_HOST_PORT:-8081}"
export MINIO_API_HOST_PORT="${MINIO_API_HOST_PORT:-5000}"
export MINIO_CONSOLE_HOST_PORT="${MINIO_CONSOLE_HOST_PORT:-8002}"
export CASSANDRA_HOST_PORT="${CASSANDRA_HOST_PORT:-9042}"
export FRONTEND_HOST_PORT="${FRONTEND_HOST_PORT:-5173}"

PROJECT=sesamefs-mr
COMPOSE=(docker compose -f docker-compose.mr.yaml -p "$PROJECT")

wait_http() { # url, name, max_seconds
  local url="$1" name="$2" max="${3:-180}" waited=0
  log_info "Waiting for $name ($url) ..."
  until curl -fsS -o /dev/null "$url" 2>/dev/null; do
    sleep 3; waited=$((waited+3))
    if [ "$waited" -ge "$max" ]; then
      log_error "$name not ready after ${max}s"; return 1
    fi
  done
  log_success "$name is ready"
}

print_urls() {
  cat <<EOF
  Web UI (React SPA, talks to USA region) : http://localhost:${FRONTEND_HOST_PORT}
  Load balancer (USA primary, EU backup)  : http://localhost:${LB_HOST_PORT}
  USA region (direct)                     : http://localhost:${SESAMEFS_USA_HOST_PORT}
  EU region (direct)                      : http://localhost:${SESAMEFS_EU_HOST_PORT}
  MinIO console                           : http://localhost:${MINIO_CONSOLE_HOST_PORT}  (minioadmin/minioadmin)
EOF
}

cmd_up() {
  log_info "Building + starting multi-region stack + frontend (project: $PROJECT) ..."
  "${COMPOSE[@]}" up -d --build nginx sesamefs-usa sesamefs-eu frontend
  wait_http "http://localhost:${LB_HOST_PORT}/health" "nginx LB" 240
  wait_http "http://localhost:${SESAMEFS_USA_HOST_PORT}/ping" "USA region" 240
  wait_http "http://localhost:${SESAMEFS_EU_HOST_PORT}/ping" "EU region" 240
  wait_http "http://localhost:${FRONTEND_HOST_PORT}/" "web UI" 120
  log_success "Stack is up."
  print_urls
}

cmd_test() {
  # Ensure the frontend is reachable before spending time building the runner.
  if ! curl -fsS -o /dev/null "http://localhost:${FRONTEND_HOST_PORT}/" 2>/dev/null; then
    log_warn "Frontend not reachable on :${FRONTEND_HOST_PORT} — bringing the stack up first."
    cmd_up
  fi
  # Normal run excludes @bug-tagged tests; `test --bugs` runs only those.
  local grep_arg="--grep-invert @bug"
  local label="suite (excludes @bug)"
  local bugs=0
  if [ "${1:-}" = "--bugs" ]; then
    grep_arg="--grep @bug"; label="@bug suite (proofs + fix-targets — failures expected)"; bugs=1
  fi
  log_info "Building Playwright runner image ..."
  "${COMPOSE[@]}" --profile test build playwright >/dev/null
  log_info "Running Playwright ${label} ..."
  echo
  set +e
  "${COMPOSE[@]}" --profile test run --rm playwright \
    bash -lc "npx playwright test --config=playwright.sesamefs.config.ts ${grep_arg}"
  local code=$?
  set -e
  echo
  if [ "$bugs" -eq 1 ]; then
    log_warn "@bug run complete (exit $code). Failures here are EXPECTED until the fixes land."
  elif [ "$code" -eq 0 ]; then
    log_success "Playwright suite PASSED"
  else
    log_error "Playwright suite FAILED (exit $code)"
  fi
  return "$code"
}

cmd_status() {
  "${COMPOSE[@]}" ps
  echo; log_info "URLs:"; print_urls
}

case "${1:-test}" in
  up)     cmd_up ;;
  test|"") shift || true; cmd_test "$@" ;;
  status) cmd_status ;;
  logs)   shift; "${COMPOSE[@]}" logs -f --tail=100 "$@" ;;
  down)   shift; "${COMPOSE[@]}" --profile test down "$@"; log_success "Stack down." ;;
  *)      log_error "Unknown command: $1"; sed -n '2,30p' "$0"; exit 1 ;;
esac
