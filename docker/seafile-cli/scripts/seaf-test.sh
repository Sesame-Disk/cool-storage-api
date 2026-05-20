#!/bin/bash
# Seafile CLI testing helper script
# Usage: seaf-test.sh <command> [args]

set -e

# Configuration
SEAF_SERVER="${SEAF_SERVER_URL:-http://sesamefs:8080}"
SEAF_USER="${SEAF_USERNAME:-00000000-0000-0000-0000-000000000001}"
SEAF_PASS="${SEAF_PASSWORD:-dev-token-123}"
SEAF_TOKEN="${SEAF_TOKEN:-}"
CONFIG_DIR="${CCNET_CONF_DIR:-/home/seafuser/.ccnet}"
DATA_DIR="${SEAFILE_DATA_DIR:-/seafile-data}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

# Initialize seaf-cli config directory
cmd_init() {
    log_info "Initializing seafile client..."

    # seaf-cli init exits early if directory exists, so we create files manually
    if [ ! -f "$CONFIG_DIR/seafile.ini" ]; then
        log_info "Creating config files..."
        mkdir -p "$CONFIG_DIR/logs"
        mkdir -p "$DATA_DIR/seafile-data"
        echo "$DATA_DIR/seafile-data" > "$CONFIG_DIR/seafile.ini"
        log_info "Created seafile.ini pointing to $DATA_DIR/seafile-data"
    else
        log_warn "Config already exists at $CONFIG_DIR"
    fi

    log_info "Initialized config at $CONFIG_DIR"
}

# Start seafile daemon
cmd_start() {
    log_info "Starting seafile daemon..."
    seaf-cli start -c "$CONFIG_DIR"
    sleep 2
    log_info "Daemon started"
}

# Stop seafile daemon
cmd_stop() {
    log_info "Stopping seafile daemon..."
    seaf-cli stop -c "$CONFIG_DIR" || true
    log_info "Daemon stopped"
}

# Get auth token from server
cmd_get_token() {
    log_info "Getting auth token from $SEAF_SERVER..." >&2

    response=$(curl -s -X POST "$SEAF_SERVER/api2/auth-token/" \
        -d "username=$SEAF_USER" \
        -d "password=$SEAF_PASS")

    token=$(echo "$response" | jq -r '.token // empty')

    if [ -z "$token" ]; then
        log_error "Failed to get token: $response" >&2
        return 1
    fi

    log_info "Got token: ${token:0:20}..." >&2
    echo "$token"
}

# List remote libraries
cmd_list_remote() {
    token="${1:-$SEAF_TOKEN}"
    if [ -z "$token" ]; then
        token=$(cmd_get_token)
    fi

    log_info "Listing remote libraries..."
    curl -s -H "Authorization: Token $token" "$SEAF_SERVER/api2/repos/" | jq .
}

# List local libraries
cmd_list_local() {
    log_info "Listing local libraries..."
    seaf-cli list -c "$CONFIG_DIR"
}

# Show sync status
cmd_status() {
    log_info "Checking sync status..."
    seaf-cli status -c "$CONFIG_DIR"
}

# Download/sync a library
cmd_sync() {
    local library_id="$1"
    local local_dir="$2"

    if [ -z "$library_id" ]; then
        log_error "Usage: seaf-test.sh sync <library_id> [local_dir]"
        return 1
    fi

    local_dir="${local_dir:-$DATA_DIR/$library_id}"
    mkdir -p "$local_dir"

    log_info "Syncing library $library_id to $local_dir..."
    log_info "Server: $SEAF_SERVER"

    # Use non-interactive username/password auth. Token-only invocation can still
    # prompt for username with current seaf-cli builds.
    seaf-cli sync -c "$CONFIG_DIR" \
        -l "$library_id" \
        -s "$SEAF_SERVER" \
        -d "$local_dir" \
        -u "$SEAF_USER" \
        -p "$SEAF_PASS"

    log_info "Sync initiated. Use 'seaf-test.sh status' to check progress."
}

# Download a library (creates new local copy)
cmd_download() {
    local library_id="$1"
    local local_dir="$2"

    if [ -z "$library_id" ]; then
        log_error "Usage: seaf-test.sh download <library_id> [local_dir]"
        return 1
    fi

    local_dir="${local_dir:-$DATA_DIR/$library_id}"

    log_info "Downloading library $library_id to $local_dir..."
    log_info "Server: $SEAF_SERVER"

    # Use non-interactive username/password auth. Token-only invocation can still
    # prompt for username with current seaf-cli builds.
    seaf-cli download -c "$CONFIG_DIR" \
        -l "$library_id" \
        -s "$SEAF_SERVER" \
        -d "$local_dir" \
        -u "$SEAF_USER" \
        -p "$SEAF_PASS"

    log_info "Download initiated. Use 'seaf-test.sh status' to check progress."
}

# Desync a library
cmd_desync() {
    local local_dir="$1"

    if [ -z "$local_dir" ]; then
        log_error "Usage: seaf-test.sh desync <local_dir>"
        return 1
    fi

    log_info "Desyncing $local_dir..."
    seaf-cli desync -c "$CONFIG_DIR" -d "$local_dir"
}

# Create a new library
cmd_create() {
    local lib_name="$1"

    if [ -z "$lib_name" ]; then
        log_error "Usage: seaf-test.sh create <library_name>"
        return 1
    fi

    token="${SEAF_TOKEN:-$(cmd_get_token)}"

    log_info "Creating library: $lib_name"
    curl -s -X POST "$SEAF_SERVER/api2/repos/" \
        -H "Authorization: Token $token" \
        -d "name=$lib_name" | jq .
}

# Watch logs
cmd_logs() {
    log_info "Watching seafile logs..."
    tail -f "$CONFIG_DIR/logs/seafile.log" 2>/dev/null || \
        log_warn "No log file found. Start the daemon first."
}

# Run a full test cycle
cmd_test_cycle() {
    log_info "Running full test cycle..."

    # 1. Initialize
    cmd_init

    # 2. Start daemon
    cmd_start

    # 3. Get token
    token=$(cmd_get_token)
    export SEAF_TOKEN="$token"

    # 4. List remote libraries
    log_info "Remote libraries:"
    cmd_list_remote "$token"

    # 5. Show status
    cmd_status

    log_info "Test cycle complete. Use 'seaf-test.sh sync <library_id>' to sync a library."
}

# Show help
cmd_help() {
    cat << 'EOF'
Seafile CLI Testing Helper

Usage: seaf-test.sh <command> [args]

Commands:
  init              Initialize seafile client config
  start             Start seafile daemon
  stop              Stop seafile daemon
  get-token         Get auth token from server
  list-remote       List remote libraries
  list-local        List locally synced libraries
  status            Show sync status
  sync <id> [dir]   Sync a library (uses existing local folder)
  download <id>     Download a library (creates new local copy)
  desync <dir>      Desync a library
  create <name>     Create a new library on server
  logs              Watch seafile logs
  test-cycle        Run full initialization and test cycle
  help              Show this help

Environment Variables:
  SEAF_SERVER_URL   Server URL (default: http://sesamefs:8080)
  SEAF_USERNAME     Username (default: dev user UUID)
  SEAF_PASSWORD     Password (default: dev-token-123)
  SEAF_TOKEN        Pre-obtained auth token (optional)

Examples:
  # Initialize and start
  seaf-test.sh init
  seaf-test.sh start

  # List available libraries
  seaf-test.sh list-remote

  # Sync a library
  seaf-test.sh sync abc123-library-id-here

  # Run full test
  seaf-test.sh test-cycle
EOF
}

# Main dispatch
case "${1:-help}" in
    init)        cmd_init ;;
    start)       cmd_start ;;
    stop)        cmd_stop ;;
    get-token)   cmd_get_token ;;
    list-remote) cmd_list_remote "$2" ;;
    list-local)  cmd_list_local ;;
    status)      cmd_status ;;
    sync)        cmd_sync "$2" "$3" ;;
    download)    cmd_download "$2" "$3" ;;
    desync)      cmd_desync "$2" ;;
    create)      cmd_create "$2" ;;
    logs)        cmd_logs ;;
    test-cycle)  cmd_test_cycle ;;
    help|--help|-h) cmd_help ;;
    *)
        log_error "Unknown command: $1"
        cmd_help
        exit 1
        ;;
esac
