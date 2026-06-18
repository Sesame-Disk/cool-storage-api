#!/bin/bash
# Run the TRUE multi-region SesameFS stack: a real 2-DC Cassandra cluster
# (cassandra-usa + cassandra-eu), one MinIO per region with active-active bucket
# replication, two region servers each pinned to their own DC + MinIO, an nginx
# LB, the React frontend, and a Playwright runner whose suite proves replication.
#
# One command boots everything and verifies replication actually works:
#   - Cassandra: both DCs join one cluster; the `sesamefs` keyspace is RF {usa:1, eu:1}
#   - MinIO: objects written to one region's bucket mirror into the other region
#   - App-level: the Playwright multi-region spec writes via USA, reads via EU (and vice-versa)
#
# Usage:
#   ./scripts/run-mr-cluster.sh                 # up (if needed) + run the full suite
#   ./scripts/run-mr-cluster.sh up              # boot the cluster + frontend
#   ./scripts/run-mr-cluster.sh replication-test# fast infra-level replication proof (Cassandra + MinIO)
#   ./scripts/run-mr-cluster.sh test            # run the Playwright suite (incl. cross-region spec)
#   ./scripts/run-mr-cluster.sh status          # service status + cluster topology + URLs
#   ./scripts/run-mr-cluster.sh logs [svc]      # tail logs
#   ./scripts/run-mr-cluster.sh down            # tear down (keeps volumes)
#   ./scripts/run-mr-cluster.sh down -v         # tear down + delete volumes
#
# FE/API host ports (approved free list): 5173 web UI | 8000 LB | 8088 USA | 8081 EU
# (8080 is held by a host process on this box, so the LB uses 8000.)
# DB/MinIO host ports (9xxx):  9142 cassandra-usa | 9143 cassandra-eu
#                              9100/9101 minio-usa API/console | 9102/9103 minio-eu API/console
set -euo pipefail

# Colors
RED='\033[0;31m'; GREEN='\033[0;32m'; BLUE='\033[0;34m'; YELLOW='\033[0;33m'; NC='\033[0m'
log_info()    { echo -e "${BLUE}[INFO]${NC} $1"; }
log_success() { echo -e "${GREEN}[OK]${NC} $1"; }
log_warn()    { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error()   { echo -e "${RED}[ERROR]${NC} $1"; }

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$PROJECT_DIR"

# This host's Docker daemon socket is not at the default path; auto-detect it.
if [ -z "${DOCKER_HOST:-}" ] && [ ! -S /var/run/docker.sock ] && [ -S /run/docker/docker.sock ]; then
  export DOCKER_HOST=unix:///run/docker/docker.sock
fi

# --- FE/API ports (approved free list) --------------------------------------
export LB_HOST_PORT="${LB_HOST_PORT:-8000}"
export SESAMEFS_USA_HOST_PORT="${SESAMEFS_USA_HOST_PORT:-8088}"
export SESAMEFS_EU_HOST_PORT="${SESAMEFS_EU_HOST_PORT:-8081}"
export FRONTEND_HOST_PORT="${FRONTEND_HOST_PORT:-5173}"
export FRONTEND_EU_HOST_PORT="${FRONTEND_EU_HOST_PORT:-5174}"
# --- DB / MinIO ports (9xxx) -------------------------------------------------
export CASSANDRA_USA_HOST_PORT="${CASSANDRA_USA_HOST_PORT:-9142}"
export CASSANDRA_EU_HOST_PORT="${CASSANDRA_EU_HOST_PORT:-9143}"
export MINIO_USA_API_HOST_PORT="${MINIO_USA_API_HOST_PORT:-9100}"
export MINIO_USA_CONSOLE_HOST_PORT="${MINIO_USA_CONSOLE_HOST_PORT:-9101}"
export MINIO_EU_API_HOST_PORT="${MINIO_EU_API_HOST_PORT:-9102}"
export MINIO_EU_CONSOLE_HOST_PORT="${MINIO_EU_CONSOLE_HOST_PORT:-9103}"

NET=sesamefs-mr-cluster
PROJECT=sesamefs-mrc
COMPOSE=(docker compose -f docker-compose.mr-cluster.yaml -p "$PROJECT")

# The single-node stack uses the SAME FE/API ports; it must be down first.
SINGLE=(docker compose -f docker-compose.mr.yaml -p sesamefs-mr)

wait_http() { # url, name, max_seconds
  local url="$1" name="$2" max="${3:-240}" waited=0
  log_info "Waiting for $name ($url) ..."
  until curl -fsS -o /dev/null "$url" 2>/dev/null; do
    sleep 3; waited=$((waited+3))
    if [ "$waited" -ge "$max" ]; then log_error "$name not ready after ${max}s"; return 1; fi
  done
  log_success "$name is ready"
}

print_urls() {
  cat <<EOF
  Web UI — USA region                     : http://localhost:${FRONTEND_HOST_PORT}
  Web UI — EU region                      : http://localhost:${FRONTEND_EU_HOST_PORT}
  Load balancer (USA primary, EU backup)  : http://localhost:${LB_HOST_PORT}
  USA region (direct, DC=usa)             : http://localhost:${SESAMEFS_USA_HOST_PORT}
  EU region (direct, DC=eu)               : http://localhost:${SESAMEFS_EU_HOST_PORT}
  MinIO USA console                       : http://localhost:${MINIO_USA_CONSOLE_HOST_PORT}  (minioadmin/minioadmin)
  MinIO EU  console                       : http://localhost:${MINIO_EU_CONSOLE_HOST_PORT}  (minioadmin/minioadmin)
EOF
}

cluster_topology() {
  log_info "Cassandra cluster topology (nodetool status):"
  "${COMPOSE[@]}" exec -T cassandra-usa nodetool status 2>/dev/null || log_warn "nodetool status unavailable"
}

cmd_up() {
  if "${SINGLE[@]}" ps --status running -q 2>/dev/null | grep -q .; then
    log_warn "Single-node stack (docker-compose.mr.yaml) is running and holds the FE/API ports — tearing it down."
    "${SINGLE[@]}" --profile test down || true
  fi
  log_info "Building + starting the multi-region CLUSTER (project: $PROJECT) ..."
  log_info "First boot forms a 2-DC Cassandra cluster — allow a few minutes."
  "${COMPOSE[@]}" up -d --build nginx sesamefs-usa sesamefs-eu frontend frontend-eu
  wait_http "http://localhost:${LB_HOST_PORT}/health" "nginx LB" 360
  wait_http "http://localhost:${SESAMEFS_USA_HOST_PORT}/ping" "USA region" 360
  wait_http "http://localhost:${SESAMEFS_EU_HOST_PORT}/ping" "EU region" 360
  wait_http "http://localhost:${FRONTEND_HOST_PORT}/" "USA web UI" 180
  wait_http "http://localhost:${FRONTEND_EU_HOST_PORT}/" "EU web UI" 180
  log_success "Cluster is up."
  echo; cluster_topology; echo
  print_urls
}

ensure_up() {
  if ! curl -fsS -o /dev/null "http://localhost:${SESAMEFS_EU_HOST_PORT}/ping" 2>/dev/null; then
    log_warn "Cluster not reachable — bringing it up first."
    cmd_up
  fi
}

# Fast, app-independent proof that both replication paths work.
cmd_replication_test() {
  ensure_up
  local rc=0

  echo; log_info "[1/2] Cassandra: both datacenters must be Up/Normal in one cluster"
  local status; status="$("${COMPOSE[@]}" exec -T cassandra-usa nodetool status 2>/dev/null || true)"
  echo "$status"
  if [ "$(echo "$status" | grep -c '^UN')" -ge 2 ] && echo "$status" | grep -q 'Datacenter: usa' && echo "$status" | grep -q 'Datacenter: eu'; then
    log_success "Cassandra 2-DC cluster healthy (usa + eu both UN)."
  else
    log_error "Cassandra cluster did not show two Up/Normal DCs."; rc=1
  fi

  echo; log_info "[2/2] MinIO: an object written to minio-usa must mirror to minio-eu"
  if docker run --rm --network "$NET" --entrypoint sh minio/mc:latest -ec '
      mc alias set usa http://minio-usa:9000 minioadmin minioadmin >/dev/null
      mc alias set eu  http://minio-eu:9000  minioadmin minioadmin >/dev/null
      obj="repltest-$(date +%s)-$$.txt"
      payload="replication-probe-$(date +%s)"
      echo "$payload" | mc pipe usa/sesamefs-usa/$obj
      echo "wrote usa/sesamefs-usa/$obj — waiting for it on eu ..."
      for i in $(seq 1 30); do
        if got="$(mc cat eu/sesamefs-usa/$obj 2>/dev/null)"; then
          if [ "$got" = "$payload" ]; then echo "replicated to eu/sesamefs-usa/$obj"; mc rm usa/sesamefs-usa/$obj >/dev/null 2>&1 || true; exit 0; fi
        fi
        sleep 2
      done
      echo "object did not replicate to eu within timeout"; exit 1
    '; then
    log_success "MinIO active-active replication working (usa -> eu)."
  else
    log_error "MinIO replication probe failed."; rc=1
  fi

  echo
  if [ "$rc" -eq 0 ]; then
    log_success "Infra-level replication proof PASSED. Run './scripts/run-mr-cluster.sh test' for the full app-level proof."
  else
    log_error "Infra-level replication proof FAILED."
  fi
  return "$rc"
}

cmd_test() {
  ensure_up
  log_info "Building Playwright runner image ..."
  "${COMPOSE[@]}" --profile test build playwright >/dev/null
  log_info "Running Playwright suite (incl. cross-region replication spec) ..."
  echo
  set +e
  "${COMPOSE[@]}" --profile test run --rm playwright
  local code=$?
  set -e
  echo
  if [ "$code" -eq 0 ]; then log_success "Playwright suite PASSED"; else log_error "Playwright suite FAILED (exit $code)"; fi
  return "$code"
}

cmd_status() {
  "${COMPOSE[@]}" ps
  echo; cluster_topology
  echo; log_info "URLs:"; print_urls
}

case "${1:-test}" in
  up)                 cmd_up ;;
  test|"")            cmd_test ;;
  replication-test|repl) cmd_replication_test ;;
  status)             cmd_status ;;
  logs)               shift; "${COMPOSE[@]}" logs -f --tail=100 "$@" ;;
  down)               shift; "${COMPOSE[@]}" --profile test down "$@"; log_success "Cluster down." ;;
  *)                  log_error "Unknown command: $1"; sed -n '2,30p' "$0"; exit 1 ;;
esac
