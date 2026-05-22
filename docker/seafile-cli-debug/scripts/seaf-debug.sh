#!/bin/bash
# Seafile Protocol Debug/Capture Script
# Captures all HTTP traffic between seaf-cli and a real Seafile server
# Usage: seaf-debug.sh <command> [args]

set -e
set +H  # Disable history expansion to allow ! in passwords

# Configuration - Real Seafile server
SEAF_SERVER="${SEAF_SERVER_URL:-https://app.nihaoconsult.com}"
SEAF_USER="${SEAF_USERNAME:-abel.aguzmans@gmail.com}"
SEAF_PASS="${SEAF_PASSWORD:-Qwerty123!}"
SEAF_TOKEN="${SEAF_TOKEN:-}"

# Known test libraries from the reference server
KNOWN_LIBRARY="aa89cce8-f2f1-44ad-98f0-1ea87ca6a3ed"  # My Library (Organization) - has content
EMPTY_LIBRARY="4cf09f26-ebef-4d5c-8363-144932d9a65b"  # Test From CLI - empty

# Paths
CONFIG_DIR="${CCNET_CONF_DIR:-/home/seafuser/.ccnet}"
DATA_DIR="${SEAFILE_DATA_DIR:-/seafile-data}"
CAPTURE_DIR="${CAPTURE_DIR:-/captures}"
PROXY_PORT=8888
PROXY_PID_FILE="/tmp/mitmproxy.pid"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

log_info() { echo -e "${GREEN}[INFO]${NC} $1" >&2; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1" >&2; }
log_error() { echo -e "${RED}[ERROR]${NC} $1" >&2; }
log_capture() { echo -e "${CYAN}[CAPTURE]${NC} $1" >&2; }
log_section() { echo -e "\n${BLUE}========== $1 ==========${NC}\n" >&2; }

# Initialize seaf-cli config
cmd_init() {
    log_info "Initializing seafile client for debug..."

    mkdir -p "$CONFIG_DIR/logs"
    mkdir -p "$DATA_DIR/seafile-data"
    mkdir -p "$CAPTURE_DIR"

    # Create seafile.ini
    echo "$DATA_DIR/seafile-data" > "$CONFIG_DIR/seafile.ini"

    log_info "Initialized config at $CONFIG_DIR"
}

# Start mitmproxy in background
cmd_start_proxy() {
    log_info "Starting mitmproxy on port $PROXY_PORT..."

    mkdir -p /mitmproxy

    # Start mitmdump with our capture addon
    mitmdump --listen-port $PROXY_PORT \
        --set confdir=/mitmproxy \
        -s /usr/local/bin/capture_addon.py \
        --ssl-insecure \
        --showhost \
        > "$CAPTURE_DIR/mitmproxy.log" 2>&1 &

    echo $! > "$PROXY_PID_FILE"
    sleep 2

    # Copy CA cert for SSL verification
    if [ -f /mitmproxy/mitmproxy-ca-cert.pem ]; then
        export SSL_CERT_FILE=/mitmproxy/mitmproxy-ca-cert.pem
        export REQUESTS_CA_BUNDLE=/mitmproxy/mitmproxy-ca-cert.pem
        log_info "CA cert available at /mitmproxy/mitmproxy-ca-cert.pem"
    fi

    log_info "mitmproxy started (PID: $(cat $PROXY_PID_FILE))"
}

# Stop mitmproxy
cmd_stop_proxy() {
    if [ -f "$PROXY_PID_FILE" ]; then
        log_info "Stopping mitmproxy..."
        kill $(cat "$PROXY_PID_FILE") 2>/dev/null || true
        rm -f "$PROXY_PID_FILE"
    fi
}

# Make proxied curl request
pcurl() {
    curl --proxy "http://127.0.0.1:$PROXY_PORT" --proxy-insecure -k "$@"
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
}

# Get auth token (capture auth flow)
cmd_get_token() {
    log_capture "Capturing auth-token request..." >&2

    response=$(pcurl -s -X POST "$SEAF_SERVER/api2/auth-token/" \
        --data-urlencode "username=$SEAF_USER" \
        --data-urlencode "password=$SEAF_PASS")

    token=$(echo "$response" | jq -r '.token // empty')

    if [ -z "$token" ]; then
        log_error "Failed to get token: $response" >&2
        return 1
    fi

    log_info "Got token: ${token:0:20}..." >&2
    echo "$token"
}

# Capture account info
cmd_capture_account_info() {
    token="${1:-$SEAF_TOKEN}"

    log_section "ACCOUNT INFO ENDPOINTS"

    log_capture "GET /api2/account/info/"
    pcurl -s -H "Authorization: Token $token" "$SEAF_SERVER/api2/account/info/" | jq .

    log_capture "GET /api2/server-info/"
    pcurl -s -H "Authorization: Token $token" "$SEAF_SERVER/api2/server-info/" | jq .
}

# Capture library listing
cmd_capture_list_repos() {
    token="${1:-$SEAF_TOKEN}"

    log_section "LIBRARY LISTING ENDPOINTS"

    log_capture "GET /api2/repos/"
    pcurl -s -H "Authorization: Token $token" "$SEAF_SERVER/api2/repos/" | jq .

    log_capture "GET /api/v2.1/repos/"
    pcurl -s -H "Authorization: Token $token" "$SEAF_SERVER/api/v2.1/repos/" | jq .
}

# Capture download-info (sync token)
cmd_capture_download_info() {
    token="${1:-$SEAF_TOKEN}"
    repo_id="${2:-$KNOWN_LIBRARY}"

    log_section "DOWNLOAD INFO (SYNC TOKEN)"

    log_capture "GET /api2/repos/$repo_id/download-info/"
    pcurl -s -H "Authorization: Token $token" "$SEAF_SERVER/api2/repos/$repo_id/download-info/" | jq .
}

# Capture commit operations
cmd_capture_commits() {
    token="${1:-$SEAF_TOKEN}"
    repo_id="${2:-$KNOWN_LIBRARY}"

    log_section "COMMIT OPERATIONS"

    # Get sync token first
    log_info "Getting sync token..."
    sync_info=$(pcurl -s -H "Authorization: Token $token" "$SEAF_SERVER/api2/repos/$repo_id/download-info/")
    sync_token=$(echo "$sync_info" | jq -r '.token')

    if [ -z "$sync_token" ] || [ "$sync_token" == "null" ]; then
        log_error "Failed to get sync token"
        return 1
    fi

    log_info "Sync token: ${sync_token:0:20}..."

    log_capture "GET /seafhttp/repo/$repo_id/commit/HEAD"
    head_response=$(pcurl -s -H "Seafile-Repo-Token: $sync_token" \
        "$SEAF_SERVER/seafhttp/repo/$repo_id/commit/HEAD")
    echo "$head_response" | jq .

    # Get commit ID from HEAD
    commit_id=$(echo "$head_response" | jq -r '.id // empty')

    if [ -n "$commit_id" ] && [ "$commit_id" != "null" ]; then
        log_capture "GET /seafhttp/repo/$repo_id/commit/$commit_id"
        pcurl -s -H "Seafile-Repo-Token: $sync_token" \
            "$SEAF_SERVER/seafhttp/repo/$repo_id/commit/$commit_id" | jq .
    fi

    echo "$sync_token"
}

# Capture fs-id-list
cmd_capture_fs_id_list() {
    token="${1:-$SEAF_TOKEN}"
    repo_id="${2:-$KNOWN_LIBRARY}"

    log_section "FS-ID-LIST OPERATIONS"

    # Get sync token
    sync_info=$(pcurl -s -H "Authorization: Token $token" "$SEAF_SERVER/api2/repos/$repo_id/download-info/")
    sync_token=$(echo "$sync_info" | jq -r '.token')

    # Get HEAD commit
    head_response=$(pcurl -s -H "Seafile-Repo-Token: $sync_token" \
        "$SEAF_SERVER/seafhttp/repo/$repo_id/commit/HEAD")
    commit_id=$(echo "$head_response" | jq -r '.id // empty')

    log_info "Commit ID: $commit_id"

    log_capture "GET /seafhttp/repo/$repo_id/fs-id-list/?server-head=$commit_id"
    pcurl -s -H "Seafile-Repo-Token: $sync_token" \
        "$SEAF_SERVER/seafhttp/repo/$repo_id/fs-id-list/?server-head=$commit_id" | jq .
}

# Capture pack-fs
cmd_capture_pack_fs() {
    token="${1:-$SEAF_TOKEN}"
    repo_id="${2:-$KNOWN_LIBRARY}"

    log_section "PACK-FS OPERATIONS"

    # Get sync token
    sync_info=$(pcurl -s -H "Authorization: Token $token" "$SEAF_SERVER/api2/repos/$repo_id/download-info/")
    sync_token=$(echo "$sync_info" | jq -r '.token')

    # Get HEAD commit
    head_response=$(pcurl -s -H "Seafile-Repo-Token: $sync_token" \
        "$SEAF_SERVER/seafhttp/repo/$repo_id/commit/HEAD")
    commit_id=$(echo "$head_response" | jq -r '.id // empty')
    root_id=$(echo "$head_response" | jq -r '.root_id // empty')

    log_info "Commit ID: $commit_id, Root ID: $root_id"

    if [ -n "$root_id" ] && [ "$root_id" != "null" ]; then
        log_capture "POST /seafhttp/repo/$repo_id/pack-fs/ with root_id=$root_id"

        # Save binary response
        pcurl -s -X POST -H "Seafile-Repo-Token: $sync_token" \
            -H "Content-Type: application/json" \
            "$SEAF_SERVER/seafhttp/repo/$repo_id/pack-fs/" \
            -d "[\"$root_id\"]" \
            -o "$CAPTURE_DIR/pack-fs-response.bin"

        log_info "Binary response saved to $CAPTURE_DIR/pack-fs-response.bin"
        log_info "First 200 bytes (hex):"
        xxd "$CAPTURE_DIR/pack-fs-response.bin" | head -15

        # Try to parse the pack-fs format
        log_info "\nParsing pack-fs format..."
        python3 << 'PYEOF'
import os
import zlib
import json

filepath = os.environ.get('CAPTURE_DIR', '/captures') + '/pack-fs-response.bin'
try:
    with open(filepath, 'rb') as f:
        data = f.read()

    print(f"Total size: {len(data)} bytes")

    offset = 0
    entry_num = 0
    while offset + 44 <= len(data):
        entry_num += 1
        fs_id = data[offset:offset+40].decode('ascii')
        size = int.from_bytes(data[offset+40:offset+44], 'big')
        print(f"\nEntry {entry_num}:")
        print(f"  FS ID: {fs_id}")
        print(f"  Compressed size: {size}")

        if offset + 44 + size <= len(data):
            compressed = data[offset+44:offset+44+size]
            try:
                decompressed = zlib.decompress(compressed)
                print(f"  Decompressed size: {len(decompressed)}")
                try:
                    parsed = json.loads(decompressed.decode('utf-8'))
                    print(f"  Content: {json.dumps(parsed, indent=4)[:500]}")
                except:
                    print(f"  Content (text): {decompressed.decode('utf-8', errors='replace')[:200]}")
            except Exception as e:
                print(f"  Decompression failed: {e}")
                print(f"  Raw hex: {compressed[:50].hex()}")

        offset += 44 + size

        if entry_num >= 5:
            print(f"\n... (showing first 5 entries, {len(data) - offset} bytes remaining)")
            break

except Exception as e:
    print(f"Error: {e}")
PYEOF
    fi
}

# Capture check-fs
cmd_capture_check_fs() {
    token="${1:-$SEAF_TOKEN}"
    repo_id="${2:-$KNOWN_LIBRARY}"

    log_section "CHECK-FS OPERATIONS"

    # Get sync token
    sync_info=$(pcurl -s -H "Authorization: Token $token" "$SEAF_SERVER/api2/repos/$repo_id/download-info/")
    sync_token=$(echo "$sync_info" | jq -r '.token')

    # Get root_id from HEAD
    head_response=$(pcurl -s -H "Seafile-Repo-Token: $sync_token" \
        "$SEAF_SERVER/seafhttp/repo/$repo_id/commit/HEAD")
    root_id=$(echo "$head_response" | jq -r '.root_id // empty')

    log_capture "POST /seafhttp/repo/$repo_id/check-fs with existing fs_id"
    pcurl -s -X POST -H "Seafile-Repo-Token: $sync_token" \
        -H "Content-Type: application/json" \
        "$SEAF_SERVER/seafhttp/repo/$repo_id/check-fs" \
        -d "[\"$root_id\"]" | jq .

    log_capture "POST /seafhttp/repo/$repo_id/check-fs with non-existent fs_id"
    pcurl -s -X POST -H "Seafile-Repo-Token: $sync_token" \
        -H "Content-Type: application/json" \
        "$SEAF_SERVER/seafhttp/repo/$repo_id/check-fs" \
        -d "[\"0000000000000000000000000000000000000000\"]" | jq .
}

# Capture check-blocks
cmd_capture_check_blocks() {
    token="${1:-$SEAF_TOKEN}"
    repo_id="${2:-$KNOWN_LIBRARY}"

    log_section "CHECK-BLOCKS OPERATIONS"

    # Get sync token
    sync_info=$(pcurl -s -H "Authorization: Token $token" "$SEAF_SERVER/api2/repos/$repo_id/download-info/")
    sync_token=$(echo "$sync_info" | jq -r '.token')

    log_capture "POST /seafhttp/repo/$repo_id/check-blocks with test block IDs"
    pcurl -s -X POST -H "Seafile-Repo-Token: $sync_token" \
        -H "Content-Type: application/json" \
        "$SEAF_SERVER/seafhttp/repo/$repo_id/check-blocks" \
        -d "[\"0000000000000000000000000000000000000000\", \"1111111111111111111111111111111111111111\"]" | jq .
}

# Capture block download
cmd_capture_block_download() {
    token="${1:-$SEAF_TOKEN}"
    repo_id="${2:-$KNOWN_LIBRARY}"

    log_section "BLOCK DOWNLOAD"

    # Get sync token
    sync_info=$(pcurl -s -H "Authorization: Token $token" "$SEAF_SERVER/api2/repos/$repo_id/download-info/")
    sync_token=$(echo "$sync_info" | jq -r '.token')

    # Get fs-id-list and find a file with blocks
    head_response=$(pcurl -s -H "Seafile-Repo-Token: $sync_token" \
        "$SEAF_SERVER/seafhttp/repo/$repo_id/commit/HEAD")
    commit_id=$(echo "$head_response" | jq -r '.id // empty')
    root_id=$(echo "$head_response" | jq -r '.root_id // empty')

    # Get fs objects to find file with blocks
    log_info "Getting fs objects to find file blocks..."

    pcurl -s -X POST -H "Seafile-Repo-Token: $sync_token" \
        -H "Content-Type: application/json" \
        "$SEAF_SERVER/seafhttp/repo/$repo_id/pack-fs/" \
        -d "[\"$root_id\"]" \
        -o "$CAPTURE_DIR/pack-fs-for-blocks.bin"

    # Parse to find block IDs
    python3 << 'PYEOF'
import os
import zlib
import json

filepath = os.environ.get('CAPTURE_DIR', '/captures') + '/pack-fs-for-blocks.bin'
try:
    with open(filepath, 'rb') as f:
        data = f.read()

    offset = 0
    block_ids = []
    while offset + 44 <= len(data):
        fs_id = data[offset:offset+40].decode('ascii')
        size = int.from_bytes(data[offset+40:offset+44], 'big')

        if offset + 44 + size <= len(data):
            compressed = data[offset+44:offset+44+size]
            try:
                decompressed = zlib.decompress(compressed)
                parsed = json.loads(decompressed.decode('utf-8'))

                # Check if it's a file (seafile) object with block_ids
                if isinstance(parsed, dict) and 'block_ids' in parsed:
                    block_ids.extend(parsed['block_ids'])
                    print(f"Found file: size={parsed.get('size', 0)}, blocks={len(parsed['block_ids'])}")
            except:
                pass

        offset += 44 + size

    if block_ids:
        print(f"\nFound {len(block_ids)} block IDs")
        # Save first few for download testing
        with open(os.environ.get('CAPTURE_DIR', '/captures') + '/block_ids.txt', 'w') as f:
            for bid in block_ids[:10]:
                f.write(bid + '\n')
        print(f"Saved first 10 block IDs to block_ids.txt")
    else:
        print("No block IDs found (library may be empty or only contain directories)")

except Exception as e:
    print(f"Error: {e}")
PYEOF

    # Download a block if we found any
    if [ -f "$CAPTURE_DIR/block_ids.txt" ]; then
        block_id=$(head -1 "$CAPTURE_DIR/block_ids.txt")
        if [ -n "$block_id" ]; then
            log_capture "GET /seafhttp/repo/$repo_id/block/$block_id"
            pcurl -s -H "Seafile-Repo-Token: $sync_token" \
                "$SEAF_SERVER/seafhttp/repo/$repo_id/block/$block_id" \
                -o "$CAPTURE_DIR/block-$block_id.bin"

            log_info "Block downloaded, size: $(wc -c < $CAPTURE_DIR/block-$block_id.bin) bytes"
            log_info "First 100 bytes (hex):"
            xxd "$CAPTURE_DIR/block-$block_id.bin" | head -7
        fi
    fi
}

# Capture permission check
cmd_capture_permission_check() {
    token="${1:-$SEAF_TOKEN}"
    repo_id="${2:-$KNOWN_LIBRARY}"

    log_section "PERMISSION CHECK"

    # Get sync token
    sync_info=$(pcurl -s -H "Authorization: Token $token" "$SEAF_SERVER/api2/repos/$repo_id/download-info/")
    sync_token=$(echo "$sync_info" | jq -r '.token')

    log_capture "GET /seafhttp/repo/$repo_id/permission-check/?op=download"
    response=$(pcurl -s -w "\nHTTP_CODE:%{http_code}" -H "Seafile-Repo-Token: $sync_token" \
        "$SEAF_SERVER/seafhttp/repo/$repo_id/permission-check/?op=download")
    echo "$response"

    log_capture "GET /seafhttp/repo/$repo_id/permission-check/?op=upload"
    response=$(pcurl -s -w "\nHTTP_CODE:%{http_code}" -H "Seafile-Repo-Token: $sync_token" \
        "$SEAF_SERVER/seafhttp/repo/$repo_id/permission-check/?op=upload")
    echo "$response"
}

# Capture protocol version
cmd_capture_protocol_version() {
    log_section "PROTOCOL VERSION"

    log_capture "GET /seafhttp/protocol-version"
    pcurl -s "$SEAF_SERVER/seafhttp/protocol-version" | jq .
}

# Capture upload-link (for upload operations)
cmd_capture_upload_link() {
    token="${1:-$SEAF_TOKEN}"
    repo_id="${2:-$KNOWN_LIBRARY}"

    log_section "UPLOAD LINK"

    log_capture "GET /api2/repos/$repo_id/upload-link/"
    pcurl -s -H "Authorization: Token $token" "$SEAF_SERVER/api2/repos/$repo_id/upload-link/" | jq .

    log_capture "GET /api2/repos/$repo_id/update-link/"
    pcurl -s -H "Authorization: Token $token" "$SEAF_SERVER/api2/repos/$repo_id/update-link/" | jq .
}

# Run full capture sequence
cmd_capture_all() {
    log_section "STARTING FULL PROTOCOL CAPTURE"
    log_info "Server: $SEAF_SERVER"
    log_info "User: $SEAF_USER"
    log_info "Library: $KNOWN_LIBRARY"

    # Initialize
    cmd_init

    # Start proxy
    cmd_start_proxy

    # Get token
    token=$(cmd_get_token)
    export SEAF_TOKEN="$token"

    # Capture all operations
    cmd_capture_protocol_version
    cmd_capture_account_info "$token"
    cmd_capture_list_repos "$token"
    cmd_capture_download_info "$token" "$KNOWN_LIBRARY"
    cmd_capture_commits "$token" "$KNOWN_LIBRARY"
    cmd_capture_fs_id_list "$token" "$KNOWN_LIBRARY"
    cmd_capture_pack_fs "$token" "$KNOWN_LIBRARY"
    cmd_capture_check_fs "$token" "$KNOWN_LIBRARY"
    cmd_capture_check_blocks "$token" "$KNOWN_LIBRARY"
    cmd_capture_block_download "$token" "$KNOWN_LIBRARY"
    cmd_capture_permission_check "$token" "$KNOWN_LIBRARY"
    cmd_capture_upload_link "$token" "$KNOWN_LIBRARY"

    # Stop proxy
    cmd_stop_proxy

    log_section "CAPTURE COMPLETE"
    log_info "All captures saved to $CAPTURE_DIR"
    log_info "Run 'ls -la $CAPTURE_DIR' to see captured files"
}

# Run seaf-cli through proxy (for sync testing)
cmd_sync_with_capture() {
    repo_id="${1:-$KNOWN_LIBRARY}"
    log_section "SYNC WITH CAPTURE"

    # Initialize
    cmd_init

    # Start proxy
    cmd_start_proxy

    # Set proxy for seaf-cli
    export HTTP_PROXY="http://127.0.0.1:$PROXY_PORT"
    export HTTPS_PROXY="http://127.0.0.1:$PROXY_PORT"
    export http_proxy="http://127.0.0.1:$PROXY_PORT"
    export https_proxy="http://127.0.0.1:$PROXY_PORT"

    # Start daemon
    cmd_start

    # Sync library
    local_dir="$DATA_DIR/$repo_id"
    mkdir -p "$local_dir"

    log_info "Syncing library $repo_id..."
    seaf-cli sync -c "$CONFIG_DIR" \
        -l "$repo_id" \
        -s "$SEAF_SERVER" \
        -d "$local_dir" \
        -u "$SEAF_USER" \
        -p "$SEAF_PASS"

    log_info "Sync initiated. Waiting for completion..."
    sleep 10

    # Check status
    seaf-cli status -c "$CONFIG_DIR"

    # View logs
    log_info "Client logs:"
    cat "$CONFIG_DIR/logs/seafile.log" | tail -50

    # Stop
    cmd_stop
    cmd_stop_proxy

    log_info "Captures saved to $CAPTURE_DIR"
}

# Show help
cmd_help() {
    cat << 'EOF'
Seafile Protocol Debug/Capture Tool

Usage: seaf-debug.sh <command> [args]

Capture Commands:
  capture-all              Run full protocol capture sequence
  capture-protocol         Capture protocol-version endpoint
  capture-account          Capture account info endpoints
  capture-list             Capture library listing
  capture-download-info    Capture download-info (sync token)
  capture-commits          Capture commit operations
  capture-fs-id-list       Capture fs-id-list
  capture-pack-fs          Capture pack-fs (binary format analysis)
  capture-check-fs         Capture check-fs
  capture-check-blocks     Capture check-blocks
  capture-blocks           Capture block download
  capture-permission       Capture permission-check
  capture-upload-link      Capture upload-link

Proxy Commands:
  start-proxy              Start mitmproxy
  stop-proxy               Stop mitmproxy

Client Commands:
  init                     Initialize seaf-cli config
  start                    Start seafile daemon
  stop                     Stop seafile daemon
  get-token                Get auth token from server
  sync-with-capture        Run seaf-cli sync through proxy

Other:
  help                     Show this help

Environment Variables:
  SEAF_SERVER_URL   Server URL (default: https://app.nihaoconsult.com)
  SEAF_USERNAME     Username (default: abel.aguzmans@gmail.com)
  SEAF_PASSWORD     Password
  CAPTURE_DIR       Where to save captures (default: /captures)

Examples:
  # Full capture sequence (recommended)
  seaf-debug.sh capture-all

  # Capture specific operation
  seaf-debug.sh capture-pack-fs

  # Sync a library with traffic capture
  seaf-debug.sh sync-with-capture aa89cce8-f2f1-44ad-98f0-1ea87ca6a3ed
EOF
}

# Main dispatch
case "${1:-help}" in
    init)                    cmd_init ;;
    start)                   cmd_start ;;
    stop)                    cmd_stop ;;
    start-proxy)             cmd_start_proxy ;;
    stop-proxy)              cmd_stop_proxy ;;
    get-token)               cmd_get_token ;;
    capture-all)             cmd_capture_all ;;
    capture-protocol)        cmd_start_proxy; cmd_capture_protocol_version; cmd_stop_proxy ;;
    capture-account)         cmd_start_proxy; t=$(cmd_get_token); cmd_capture_account_info "$t"; cmd_stop_proxy ;;
    capture-list)            cmd_start_proxy; t=$(cmd_get_token); cmd_capture_list_repos "$t"; cmd_stop_proxy ;;
    capture-download-info)   cmd_start_proxy; t=$(cmd_get_token); cmd_capture_download_info "$t" "$2"; cmd_stop_proxy ;;
    capture-commits)         cmd_start_proxy; t=$(cmd_get_token); cmd_capture_commits "$t" "$2"; cmd_stop_proxy ;;
    capture-fs-id-list)      cmd_start_proxy; t=$(cmd_get_token); cmd_capture_fs_id_list "$t" "$2"; cmd_stop_proxy ;;
    capture-pack-fs)         cmd_start_proxy; t=$(cmd_get_token); cmd_capture_pack_fs "$t" "$2"; cmd_stop_proxy ;;
    capture-check-fs)        cmd_start_proxy; t=$(cmd_get_token); cmd_capture_check_fs "$t" "$2"; cmd_stop_proxy ;;
    capture-check-blocks)    cmd_start_proxy; t=$(cmd_get_token); cmd_capture_check_blocks "$t" "$2"; cmd_stop_proxy ;;
    capture-blocks)          cmd_start_proxy; t=$(cmd_get_token); cmd_capture_block_download "$t" "$2"; cmd_stop_proxy ;;
    capture-permission)      cmd_start_proxy; t=$(cmd_get_token); cmd_capture_permission_check "$t" "$2"; cmd_stop_proxy ;;
    capture-upload-link)     cmd_start_proxy; t=$(cmd_get_token); cmd_capture_upload_link "$t" "$2"; cmd_stop_proxy ;;
    sync-with-capture)       cmd_sync_with_capture "$2" "$3" ;;
    help|--help|-h)          cmd_help ;;
    *)
        log_error "Unknown command: $1"
        cmd_help
        exit 1
        ;;
esac
