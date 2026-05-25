#!/usr/bin/env bash
# Runs the full frontend test gate locally or in CI.
# Mirrors what a future .github/workflows/frontend-ci.yml would run.
#
# Usage:
#   ./scripts/ci-frontend.sh           # full chain
#   ./scripts/ci-frontend.sh unit      # lint + Jest + Vitest only (skip e2e)
#   ./scripts/ci-frontend.sh e2e       # Playwright only (frontend must be up)

set -euo pipefail

cd "$(dirname "$0")/.."

mode="${1:-all}"

run_unit() {
  echo "== frontend lint + Jest + Vitest =="
  docker compose --profile test build frontend-test
  docker compose --profile test run --rm frontend-test
}

run_e2e() {
  echo "== frontend Playwright (mobile + tablet + desktop) =="
  docker compose --profile test build frontend-e2e
  docker compose --profile test up \
    --abort-on-container-exit \
    --exit-code-from frontend-e2e \
    frontend frontend-e2e
}

case "$mode" in
  unit)
    run_unit
    ;;
  e2e)
    run_e2e
    ;;
  all)
    run_unit
    run_e2e
    ;;
  *)
    echo "Unknown mode: $mode (expected: all|unit|e2e)" >&2
    exit 2
    ;;
esac

echo "== frontend CI: PASS =="
