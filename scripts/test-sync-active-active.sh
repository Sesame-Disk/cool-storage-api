#!/bin/bash
#
# SesameFS real desktop-client active-active sync proof.
#
# This script runs two isolated seaf-cli clients against different SesameFS
# nodes, stages divergent local changes from the same synced base state, then
# verifies that both changes converge in the remote library and on both clients.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

COMPOSE_PROJECT_NAME="${COMPOSE_PROJECT_NAME:-sesamefs}"
PRIMARY_SERVICE="${PRIMARY_SERVICE:-seafile-cli-aa-1}"
SECONDARY_SERVICE="${SECONDARY_SERVICE:-seafile-cli-aa-2}"
PRIMARY_API_URL="${PRIMARY_API_URL:-http://localhost:8080}"
SECONDARY_HEALTH_URL="${SECONDARY_HEALTH_URL:-http://sesamefs-node-2:8080/health}"
DEV_API_TOKEN="${DEV_API_TOKEN:-dev-token-admin}"
REPO_PREFIX="sync-aa"
SYNC_CONFIG_DIR="/home/seafuser/.ccnet"
SYNC_DATA_DIR="/seafile-data"

KEEP_STATE=false
VERBOSE=false
COMPOSE_BUILD=true
SCENARIO="all"

REPO_ID=""
REPO_NAME=""
SYNC_DIR=""
STARTED_SERVICES=()

log_info() { echo "[INFO] $1"; }
log_success() { echo "[PASS] $1"; }
log_error() { echo "[FAIL] $1" >&2; }
log_warn() { echo "[WARN] $1"; }
log_verbose() {
  if [ "$VERBOSE" = true ]; then
    echo "[DEBUG] $1"
  fi
}

client_status_output() {
  local service="$1"
  docker_exec_service "$service" seaf-cli status -c "$SYNC_CONFIG_DIR" 2>/dev/null || true
}

client_repo_status_line() {
  local service="$1"
  local output=""

  output=$(client_status_output "$service")
  printf '%s\n' "$output" | awk -v repo_name="$REPO_NAME" 'index($0, repo_name) == 1 { print; exit }'
}

status_line_is_synchronized() {
  local status_line="$1"
  case "$status_line" in
    *" synchronized"*)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

backend_logs() {
  compose logs --no-color sesamefs sesamefs-node-2 2>/dev/null || true
}

wait_for_backend_log_pattern() {
  local pattern="$1"
  local description="$2"
  local max_wait="${3:-30}"
  local waited=0

  while [ $waited -lt $max_wait ]; do
    if backend_logs | grep -F "$pattern" > /dev/null 2>&1; then
      return 0
    fi
    sleep 1
    waited=$((waited + 1))
  done

  log_error "Backend logs never showed ${description}"
  if [ "$VERBOSE" = true ]; then
    backend_logs | tail -n 120 || true
  fi
  exit 1
}

compose() {
  (
    cd "$PROJECT_DIR"
    COMPOSE_PROJECT_NAME="$COMPOSE_PROJECT_NAME" docker compose "$@"
  )
}

service_container_name() {
  local service="$1"
  local container_id=""
  local container_name=""

  container_id=$(compose ps -q "$service" 2>/dev/null | head -n1 || true)
  if [ -z "$container_id" ]; then
    return 1
  fi

  container_name=$(docker inspect --format '{{.Name}}' "$container_id" 2>/dev/null | sed 's#^/##')
  if [ -z "$container_name" ]; then
    return 1
  fi

  echo "$container_name"
}

service_running() {
  local service="$1"
  local container_id=""
  local running="false"

  container_id=$(compose ps -q "$service" 2>/dev/null | head -n1 || true)
  if [ -z "$container_id" ]; then
    return 1
  fi

  running=$(docker inspect --format '{{.State.Running}}' "$container_id" 2>/dev/null || echo "false")
  [ "$running" = "true" ]
}

remember_service_if_new() {
  local service="$1"
  if ! service_running "$service"; then
    STARTED_SERVICES+=("$service")
  fi
}

docker_exec_service() {
  local service="$1"
  shift
  local container_name=""

  container_name=$(service_container_name "$service")
  docker exec "$container_name" "$@"
}

docker_exec_service_stdin() {
  local service="$1"
  shift
  local container_name=""

  container_name=$(service_container_name "$service")
  docker exec -i "$container_name" "$@"
}

docker_exec_service_shell() {
  local service="$1"
  local command="$2"
  local container_name=""

  container_name=$(service_container_name "$service")
  docker exec "$container_name" /bin/bash -lc "$command"
}

json_query() {
  if command -v jq > /dev/null 2>&1; then
    jq "$@"
  else
    docker_exec_service_stdin "$PRIMARY_SERVICE" jq "$@"
  fi
}

require_tooling() {
  if ! command -v docker > /dev/null 2>&1; then
    log_error "docker is required"
    exit 1
  fi

  if ! compose version > /dev/null 2>&1; then
    log_error "docker compose is required"
    exit 1
  fi

  if ! command -v curl > /dev/null 2>&1; then
    log_error "curl is required"
    exit 1
  fi
}

ensure_stack() {
  local compose_args=(--profile test --profile debug up -d)

  remember_service_if_new "sesamefs-node-2"
  remember_service_if_new "$PRIMARY_SERVICE"
  remember_service_if_new "$SECONDARY_SERVICE"

  if [ "$COMPOSE_BUILD" = true ]; then
    compose_args+=(--build)
  fi

  compose_args+=(sesamefs sesamefs-node-2 "$PRIMARY_SERVICE" "$SECONDARY_SERVICE")

  log_info "Starting active-active proof stack"
  compose "${compose_args[@]}"
}

wait_for_services() {
  log_info "Waiting for node 1 health"
  "$SCRIPT_DIR/wait-for-http.sh" "${PRIMARY_API_URL}/health" sesamefs > /dev/null

  log_info "Waiting for node 2 health"
  local waited=0
  while [ $waited -lt 120 ]; do
    if docker_exec_service "$PRIMARY_SERVICE" curl -sf "$SECONDARY_HEALTH_URL" > /dev/null 2>&1; then
      return 0
    fi
    sleep 1
    waited=$((waited + 1))
  done

  log_error "sesamefs-node-2 did not become healthy"
  exit 1
}

initialize_client() {
  local service="$1"

  log_verbose "Initializing ${service}"
  docker_exec_service_shell "$service" "seaf-test.sh init > /dev/null 2>&1 || true; seaf-test.sh start > /dev/null 2>&1 || true"

  local waited=0
  while [ $waited -lt 30 ]; do
    if docker_exec_service_shell "$service" "seaf-test.sh status > /dev/null 2>&1"; then
      return 0
    fi
    sleep 1
    waited=$((waited + 1))
  done

  log_error "${service} failed to start seaf-cli"
  exit 1
}

create_library() {
  REPO_NAME="${REPO_PREFIX}-$(date +%s)-$RANDOM"
  log_info "Creating library ${REPO_NAME}"

  local response=""
  response=$(curl -s -X POST "${PRIMARY_API_URL}/api2/repos/" \
    -H "Authorization: Token ${DEV_API_TOKEN}" \
    -H "Content-Type: application/x-www-form-urlencoded" \
    -d "name=${REPO_NAME}")

  REPO_ID=$(echo "$response" | json_query -r '.repo_id // empty')
  if [ -z "$REPO_ID" ]; then
    log_error "Failed to create library: $response"
    exit 1
  fi

  SYNC_DIR="${SYNC_DATA_DIR}/active-active-${REPO_ID}"
  log_success "Created library ${REPO_ID}"
}

cleanup_stale_client_sync_dirs() {
  local service=""

  for service in "$PRIMARY_SERVICE" "$SECONDARY_SERVICE"; do
    docker_exec_service_shell "$service" "
      for dir in '${SYNC_DATA_DIR}'/active-active-*; do
        [ -e \"\$dir\" ] || continue
        seaf-cli desync -c '${SYNC_CONFIG_DIR}' -d \"\$dir\" > /dev/null 2>&1 || true
        rm -rf \"\$dir\"
      done
    " || true
  done
}

prepare_synced_library() {
  create_library
  start_sync_on_client "$PRIMARY_SERVICE"
  start_sync_on_client "$SECONDARY_SERVICE"
  wait_for_client_sync "$PRIMARY_SERVICE"
  wait_for_client_sync "$SECONDARY_SERVICE"
}

reset_current_repo_state() {
  cleanup_repo_state
  REPO_ID=""
  REPO_NAME=""
  SYNC_DIR=""
}

delete_library() {
  if [ -z "$REPO_ID" ]; then
    return 0
  fi

  curl -s -X DELETE "${PRIMARY_API_URL}/api/v2.1/repos/${REPO_ID}/" \
    -H "Authorization: Token ${DEV_API_TOKEN}" > /dev/null 2>&1 || true
}

start_sync_on_client() {
  local service="$1"

  log_verbose "Starting sync on ${service}"
  docker_exec_service_shell "$service" "mkdir -p '${SYNC_DIR}'; seaf-test.sh sync '${REPO_ID}' '${SYNC_DIR}' > /dev/null 2>&1 || true"
}

client_sync_status() {
  local service="$1"
  local output=""

  output=$(docker_exec_service "$service" seaf-cli status -c "$SYNC_CONFIG_DIR" 2>/dev/null || true)
  printf '%s\n' "$output" | awk -v repo_name="$REPO_NAME" '$1 == repo_name { print $2; exit }'
}

wait_for_client_sync() {
  local service="$1"
  local max_wait="${2:-60}"
  local waited=0

  while [ $waited -lt $max_wait ]; do
    local status=""
    status=$(client_sync_status "$service")
    if [ "$status" = "synchronized" ]; then
      return 0
    fi
    sleep 1
    waited=$((waited + 1))
  done

  log_error "${service} did not reach synchronized state"
  docker_exec_service "$service" seaf-cli status -c "$SYNC_CONFIG_DIR" 2>/dev/null || true
  exit 1
}

write_client_file() {
  local service="$1"
  local file_name="$2"
  local content="$3"
  local container_name=""

  container_name=$(service_container_name "$service")
  printf '%s\n' "$content" | docker exec -i "$container_name" /bin/sh -lc "cat > '${SYNC_DIR}/${file_name}'"
}

stop_clients() {
  local stop_pids=()
  local service=""

  for service in "$PRIMARY_SERVICE" "$SECONDARY_SERVICE"; do
    docker_exec_service_shell "$service" "seaf-test.sh stop > /dev/null 2>&1 || true" &
    stop_pids+=("$!")
  done
  for pid in "${stop_pids[@]}"; do
    wait "$pid"
  done

  sleep 1
}

start_clients() {
  local start_pids=()
  local service=""

  for service in "$PRIMARY_SERVICE" "$SECONDARY_SERVICE"; do
    docker_exec_service_shell "$service" "seaf-test.sh start > /dev/null 2>&1 || true" &
    start_pids+=("$!")
  done
  for pid in "${start_pids[@]}"; do
    wait "$pid"
  done

  sleep 3
}

trigger_concurrent_sync() {
  stop_clients
  start_clients
}

wait_for_remote_entry() {
  local file_name="$1"
  local max_wait="${2:-60}"
  local waited=0

  while [ $waited -lt $max_wait ]; do
    local listing=""
    listing=$(curl -s "${PRIMARY_API_URL}/api2/repos/${REPO_ID}/dir/?p=/" \
      -H "Authorization: Token ${DEV_API_TOKEN}")

    if echo "$listing" | json_query -e --arg name "$file_name" '.[] | select(.name == $name)' > /dev/null 2>&1; then
      return 0
    fi

    sleep 1
    waited=$((waited + 1))
  done

  return 1
}

wait_for_remote_entries() {
  local first_name="$1"
  local second_name="$2"

  if ! wait_for_remote_entry "$first_name" 60; then
    log_error "Remote library never showed ${first_name}"
    exit 1
  fi

  if ! wait_for_remote_entry "$second_name" 60; then
    log_error "Remote library never showed ${second_name}"
    exit 1
  fi
}

client_file_content() {
  local service="$1"
  local file_name="$2"

  docker_exec_service_shell "$service" "cat '${SYNC_DIR}/${file_name}' 2>/dev/null" || true
}

wait_for_client_content() {
  local service="$1"
  local file_name="$2"
  local expected="$3"
  local max_wait="${4:-60}"
  local waited=0

  while [ $waited -lt $max_wait ]; do
    local actual=""
    actual=$(client_file_content "$service" "$file_name")
    if [ "$actual" = "$expected" ]; then
      return 0
    fi
    sleep 1
    waited=$((waited + 1))
  done

  log_error "${service} never converged ${file_name} to expected content"
  exit 1
}

wait_for_any_client_non_synchronized() {
  local max_wait="${1:-60}"
  local waited=0

  while [ $waited -lt $max_wait ]; do
    local primary_status_line=""
    local secondary_status_line=""

    primary_status_line=$(client_repo_status_line "$PRIMARY_SERVICE")
    secondary_status_line=$(client_repo_status_line "$SECONDARY_SERVICE")

    if [ -n "$primary_status_line" ] && [ -n "$secondary_status_line" ]; then
      if ! status_line_is_synchronized "$primary_status_line" || ! status_line_is_synchronized "$secondary_status_line"; then
        log_verbose "Primary status: ${primary_status_line}"
        log_verbose "Secondary status: ${secondary_status_line}"
        return 0
      fi
    fi

    sleep 1
    waited=$((waited + 1))
  done

  log_error "Both clients still report synchronized after the unsafe conflict window"
  echo "Primary status: $(client_repo_status_line "$PRIMARY_SERVICE")" >&2
  echo "Secondary status: $(client_repo_status_line "$SECONDARY_SERVICE")" >&2
  exit 1
}

assert_client_content() {
  local service="$1"
  local file_name="$2"
  local expected="$3"
  local actual=""

  actual=$(client_file_content "$service" "$file_name")
  if [ "$actual" != "$expected" ]; then
    log_error "${service} content mismatch for ${file_name}"
    echo "Expected: ${expected}" >&2
    echo "Actual:   ${actual}" >&2
    exit 1
  fi
}

finalize_successful_scenario_state() {
  local preserve_state="$1"

  if [ "$KEEP_STATE" = true ] && [ "$preserve_state" = true ]; then
    return 0
  fi

  reset_current_repo_state
}

run_non_overlapping_auto_merge_scenario() {
  local preserve_state="${1:-false}"
  local first_name="client-1.txt"
  local second_name="client-2.txt"
  local first_content="client-1 active-active proof $(date -Iseconds) $RANDOM"
  local second_content="client-2 active-active proof $(date -Iseconds) $RANDOM"

  log_info "Scenario: non-overlapping concurrent writes should auto-merge"
  prepare_synced_library

  log_info "Stopping both clients so local changes stay offline until the race starts"
  stop_clients

  log_info "Staging divergent local changes from the same synced base state"
  write_client_file "$PRIMARY_SERVICE" "$first_name" "$first_content"
  write_client_file "$SECONDARY_SERVICE" "$second_name" "$second_content"

  log_info "Starting both clients concurrently from the same synced base state"
  start_clients
  wait_for_remote_entries "$first_name" "$second_name"
  wait_for_backend_log_pattern "parent mismatch for repo ${REPO_ID}" "a sync parent mismatch for ${REPO_ID}"
  wait_for_backend_log_pattern "auto-merged repo ${REPO_ID}" "an auto-merge publish for ${REPO_ID}"

  log_info "Triggering a pull round so both clients absorb the winning head"
  trigger_concurrent_sync
  wait_for_client_sync "$PRIMARY_SERVICE"
  wait_for_client_sync "$SECONDARY_SERVICE"

  wait_for_client_content "$PRIMARY_SERVICE" "$first_name" "$first_content"
  wait_for_client_content "$PRIMARY_SERVICE" "$second_name" "$second_content"
  wait_for_client_content "$SECONDARY_SERVICE" "$first_name" "$first_content"
  wait_for_client_content "$SECONDARY_SERVICE" "$second_name" "$second_content"

  log_success "Non-overlapping concurrent writes converged via observed auto-merge"
  finalize_successful_scenario_state "$preserve_state"
}

run_same_path_unsafe_conflict_scenario() {
  local preserve_state="${1:-false}"
  local shared_name="collision.txt"
  local first_content="client-1 unsafe-conflict proof $(date -Iseconds) $RANDOM"
  local second_content="client-2 unsafe-conflict proof $(date -Iseconds) $RANDOM"

  log_info "Scenario: same-path concurrent writes should fail closed with 503 and preserve local state"
  prepare_synced_library

  log_info "Stopping both clients so conflicting local edits remain offline until publish"
  stop_clients

  log_info "Staging conflicting local writes to the same path"
  write_client_file "$PRIMARY_SERVICE" "$shared_name" "$first_content"
  write_client_file "$SECONDARY_SERVICE" "$shared_name" "$second_content"

  log_info "Starting both clients concurrently to trigger an unsafe sync conflict"
  start_clients
  wait_for_remote_entry "$shared_name" 60
  wait_for_backend_log_pattern "auto-merge could not safely resolve repo ${REPO_ID}" "an unmergeable auto-merge decision for ${REPO_ID}"
  wait_for_backend_log_pattern "parent mismatch retry budget exhausted for repo ${REPO_ID}" "a fail-closed 503 outcome for ${REPO_ID}"
  wait_for_any_client_non_synchronized 60

  assert_client_content "$PRIMARY_SERVICE" "$shared_name" "$first_content"
  assert_client_content "$SECONDARY_SERVICE" "$shared_name" "$second_content"

  log_success "Unsafe same-path conflict preserved both local edits after observed 503 exhaustion"
  finalize_successful_scenario_state "$preserve_state"
}

cleanup_repo_state() {
  if [ -n "$SYNC_DIR" ]; then
    docker_exec_service_shell "$PRIMARY_SERVICE" "seaf-cli desync -c '$SYNC_CONFIG_DIR' -d '$SYNC_DIR' > /dev/null 2>&1 || true; rm -rf '$SYNC_DIR'" || true
    docker_exec_service_shell "$SECONDARY_SERVICE" "seaf-cli desync -c '$SYNC_CONFIG_DIR' -d '$SYNC_DIR' > /dev/null 2>&1 || true; rm -rf '$SYNC_DIR'" || true
  fi

  delete_library
}

cleanup_services() {
  local service=""
  for service in "${STARTED_SERVICES[@]}"; do
    compose stop "$service" > /dev/null 2>&1 || true
  done
}

cleanup() {
  if [ "$KEEP_STATE" = true ]; then
    log_warn "Keeping active-active proof state (--keep)"
    if [ -n "$REPO_ID" ]; then
      echo "Library: ${REPO_ID} (${REPO_NAME})"
      echo "Sync dir: ${SYNC_DIR}"
    fi
    return 0
  fi

  cleanup_repo_state
  cleanup_services
}

parse_args() {
  while [ $# -gt 0 ]; do
    case "$1" in
      --keep)
        KEEP_STATE=true
        ;;
      --verbose|-v)
        VERBOSE=true
        ;;
      --no-build)
        COMPOSE_BUILD=false
        ;;
      --scenario)
        if [ $# -lt 2 ]; then
          log_error "--scenario requires one of: all, safe-auto-merge, unsafe-503"
          exit 1
        fi
        SCENARIO="$2"
        shift
        ;;
      --help|-h)
        cat <<'EOF'
SesameFS Active-Active Desktop Proof

Usage: ./scripts/test-sync-active-active.sh [options]

Options:
  --keep       Keep the proof library and running client services for inspection
  --verbose    Show additional debug logging
  --no-build   Reuse existing compose images without rebuilding
  --scenario   all | safe-auto-merge | unsafe-503 (default: all)
  --help       Show this help message
EOF
        exit 0
        ;;
      *)
        log_error "Unknown option: $1"
        exit 1
        ;;
    esac
    shift
  done
}

main() {
  parse_args "$@"
  trap cleanup EXIT

  echo ""
  echo "=========================================="
  echo "  SesameFS Active-Active Desktop Proof"
  echo "=========================================="
  echo ""

  require_tooling
  ensure_stack
  wait_for_services

  initialize_client "$PRIMARY_SERVICE"
  initialize_client "$SECONDARY_SERVICE"
  cleanup_stale_client_sync_dirs

  case "$SCENARIO" in
    all)
      run_non_overlapping_auto_merge_scenario false
      run_same_path_unsafe_conflict_scenario true
      log_success "All active-active desktop conflict scenarios passed"
      ;;
    safe-auto-merge)
      run_non_overlapping_auto_merge_scenario true
      ;;
    unsafe-503)
      run_same_path_unsafe_conflict_scenario true
      ;;
    *)
      log_error "Unknown scenario: ${SCENARIO}"
      exit 1
      ;;
  esac
}

main "$@"
