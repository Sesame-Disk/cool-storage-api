#!/bin/bash
# =============================================================================
# Nested Move/Copy Integration Tests for SesameFS
# =============================================================================
#
# Tests move and copy operations with nested folder hierarchies at various depths.
# These are regression tests for the tree-rebuild logic (RebuildPathToRoot) that
# must correctly update ancestor fs_objects when moving/copying into or out of
# deeply nested directories.
#
# Test Scenarios:
#   1.  Move file: root → depth-1
#   2.  Move file: root → depth-2
#   3.  Move file: root → depth-3
#   4.  Move file: depth-2 → depth-2 (sibling)
#   5.  Move file: depth-3 → root
#   6.  Move folder: root → depth-2
#   7.  Move folder with contents: depth-1 → depth-2
#   8.  Move multiple items in one batch: root → depth-2
#   9.  Copy file: root → depth-1
#  10.  Copy file: root → depth-2
#  11.  Copy file: root → depth-3
#  12.  Copy file: depth-2 → depth-2 (sibling)
#  13.  Copy file: depth-3 → root
#  14.  Copy folder: root → depth-2
#  15.  Copy folder with contents: depth-1 → depth-2
#  16.  Copy multiple items in one batch: root → depth-2
#  17.  Verify tree integrity after chained moves (multiple sequential moves)
#  18.  Verify tree integrity after chained copies
#  19.  Move into freshly-created deep path (depth-4)
#  20.  Copy into freshly-created deep path (depth-4)
#  21.  Move file — duplicate name rejected (409)
#  22.  Copy file — duplicate name rejected (409)
#  23.  Move folder — duplicate name rejected (409)
#  24.  Move duplicate name at depth-2 (409)
#  25.  Conflict response includes conflicting_items list
#  26.  conflict_policy=replace overwrites existing file
#  27.  conflict_policy=autorename creates "file (1).ext"
#  28.  conflict_policy=skip silently skips
#  29.  Cross-repo copy — duplicate name returns 409
#  30.  Cross-repo copy — conflict response body
#  31.  Cross-repo copy — replace policy works
#  32.  Cross-repo copy — autorename policy works
#  33.  Cross-repo copy — nested path conflict returns 409
#  34.  Move with autorename — source file removed correctly
#  35.  Copy from nested path to root — same name conflict + replace + autorename
#
# Usage:
#   ./scripts/test-nested-move-copy.sh [options]
#
# Options:
#   --quick       Skip slow deep-path tests
#   --verbose     Show curl response bodies
#   --help        Show this help
#
# Requirements:
#   - Backend running at $API_URL (default: http://localhost:8082)
#   - Dev tokens configured
#   - curl, jq installed
#
# =============================================================================

set -e

# Configuration
API_URL="${API_URL:-http://localhost:8082}"
ADMIN_TOKEN="${ADMIN_TOKEN:-dev-token-admin}"

# Options
QUICK_MODE=false
VERBOSE=false

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

# Counters
TOTAL_TESTS=0
PASSED_TESTS=0
FAILED_TESTS=0
declare -a FAILED_TEST_NAMES

# =============================================================================
# Helpers
# =============================================================================

log_info()    { echo -e "${BLUE}[INFO]${NC} $1"; }
log_success() { echo -e "${GREEN}[PASS]${NC} $1"; }
log_fail()    { echo -e "${RED}[FAIL]${NC} $1"; }
log_section() {
    echo ""
    echo -e "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${CYAN}  $1${NC}"
    echo -e "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
}

run_test() {
    local test_name="$1"
    local expected="$2"
    local actual="$3"

    TOTAL_TESTS=$((TOTAL_TESTS + 1))

    if [ "$expected" == "$actual" ]; then
        log_success "$test_name (expected: $expected, got: $actual)"
        PASSED_TESTS=$((PASSED_TESTS + 1))
    else
        log_fail "$test_name (expected: $expected, got: $actual)"
        FAILED_TESTS=$((FAILED_TESTS + 1))
        FAILED_TEST_NAMES+=("$test_name")
    fi
}

run_test_contains() {
    local test_name="$1"
    local needle="$2"
    local haystack="$3"

    TOTAL_TESTS=$((TOTAL_TESTS + 1))

    if echo "$haystack" | grep -q "$needle"; then
        log_success "$test_name (contains '$needle')"
        PASSED_TESTS=$((PASSED_TESTS + 1))
    else
        log_fail "$test_name (missing '$needle' in response)"
        FAILED_TESTS=$((FAILED_TESTS + 1))
        FAILED_TEST_NAMES+=("$test_name")
        if [ "$VERBOSE" = true ]; then
            echo -e "  ${YELLOW}Response:${NC} $haystack" | head -5
        fi
    fi
}

run_test_not_contains() {
    local test_name="$1"
    local needle="$2"
    local haystack="$3"

    TOTAL_TESTS=$((TOTAL_TESTS + 1))

    if ! echo "$haystack" | grep -q "$needle"; then
        log_success "$test_name (correctly missing '$needle')"
        PASSED_TESTS=$((PASSED_TESTS + 1))
    else
        log_fail "$test_name (unexpectedly found '$needle')"
        FAILED_TESTS=$((FAILED_TESTS + 1))
        FAILED_TEST_NAMES+=("$test_name")
    fi
}

# Returns HTTP status code only
api_status() {
    local method="$1" endpoint="$2" token="$3" data="$4"
    local url="${API_URL}${endpoint}"
    local opts=(-s -o /dev/null -w "%{http_code}")
    if [ -n "$token" ]; then opts+=(-H "Authorization: Token $token"); fi
    opts+=(-H "Content-Type: application/json")
    if [ -n "$data" ]; then opts+=(-d "$data"); fi
    curl "${opts[@]}" -X "$method" "$url"
}

# Returns response body
api_body() {
    local method="$1" endpoint="$2" token="$3" data="$4"
    local url="${API_URL}${endpoint}"
    local opts=(-s)
    if [ -n "$token" ]; then opts+=(-H "Authorization: Token $token"); fi
    opts+=(-H "Content-Type: application/json")
    if [ -n "$data" ]; then opts+=(-d "$data"); fi
    local body
    body=$(curl "${opts[@]}" -X "$method" "$url")
    if [ "$VERBOSE" = true ]; then
        echo -e "${YELLOW}[RESP]${NC} $method $endpoint" >&2
        echo "$body" | jq . 2>/dev/null >&2 || echo "$body" >&2
    fi
    echo "$body"
}

# Create a library, return repo_id
create_library() {
    local name="$1"
    local body
    body=$(api_body "POST" "/api/v2.1/repos/" "$ADMIN_TOKEN" "{\"repo_name\":\"$name\"}")
    echo "$body" | jq -r '.repo_id // empty'
}

# Create a directory (mkdir)
create_dir() {
    local repo_id="$1" path="$2"
    api_status "POST" "/api/v2.1/repos/${repo_id}/dir/?p=${path}" "$ADMIN_TOKEN" '{}' > /dev/null
}

# Create a file with content via the create operation
create_file() {
    local repo_id="$1" path="$2"
    api_status "POST" "/api/v2.1/repos/${repo_id}/file/?p=${path}&operation=create" "$ADMIN_TOKEN" > /dev/null
}

# List directory contents
list_dir() {
    local repo_id="$1" path="$2"
    api_body "GET" "/api/v2.1/repos/${repo_id}/dir/?p=${path}" "$ADMIN_TOKEN"
}

# Sync batch move
batch_move() {
    local src_repo="$1" src_dir="$2" dst_repo="$3" dst_dir="$4"
    shift 4
    # Remaining args are dirent names
    local dirents=""
    for d in "$@"; do
        if [ -n "$dirents" ]; then dirents="${dirents},"; fi
        dirents="${dirents}\"${d}\""
    done
    api_status "POST" "/api/v2.1/repos/sync-batch-move-item/" "$ADMIN_TOKEN" \
        "{\"src_repo_id\":\"${src_repo}\",\"src_parent_dir\":\"${src_dir}\",\"dst_repo_id\":\"${dst_repo}\",\"dst_parent_dir\":\"${dst_dir}\",\"src_dirents\":[${dirents}]}"
}

# Sync batch copy
batch_copy() {
    local src_repo="$1" src_dir="$2" dst_repo="$3" dst_dir="$4"
    shift 4
    local dirents=""
    for d in "$@"; do
        if [ -n "$dirents" ]; then dirents="${dirents},"; fi
        dirents="${dirents}\"${d}\""
    done
    api_status "POST" "/api/v2.1/repos/sync-batch-copy-item/" "$ADMIN_TOKEN" \
        "{\"src_repo_id\":\"${src_repo}\",\"src_parent_dir\":\"${src_dir}\",\"dst_repo_id\":\"${dst_repo}\",\"dst_parent_dir\":\"${dst_dir}\",\"src_dirents\":[${dirents}]}"
}

# Sync batch move with conflict_policy — returns HTTP status
batch_move_with_policy() {
    local src_repo="$1" src_dir="$2" dst_repo="$3" dst_dir="$4" policy="$5"
    shift 5
    local dirents=""
    for d in "$@"; do
        if [ -n "$dirents" ]; then dirents="${dirents},"; fi
        dirents="${dirents}\"${d}\""
    done
    api_status "POST" "/api/v2.1/repos/sync-batch-move-item/" "$ADMIN_TOKEN" \
        "{\"src_repo_id\":\"${src_repo}\",\"src_parent_dir\":\"${src_dir}\",\"dst_repo_id\":\"${dst_repo}\",\"dst_parent_dir\":\"${dst_dir}\",\"src_dirents\":[${dirents}],\"conflict_policy\":\"${policy}\"}"
}

# Sync batch copy with conflict_policy — returns HTTP status
batch_copy_with_policy() {
    local src_repo="$1" src_dir="$2" dst_repo="$3" dst_dir="$4" policy="$5"
    shift 5
    local dirents=""
    for d in "$@"; do
        if [ -n "$dirents" ]; then dirents="${dirents},"; fi
        dirents="${dirents}\"${d}\""
    done
    api_status "POST" "/api/v2.1/repos/sync-batch-copy-item/" "$ADMIN_TOKEN" \
        "{\"src_repo_id\":\"${src_repo}\",\"src_parent_dir\":\"${src_dir}\",\"dst_repo_id\":\"${dst_repo}\",\"dst_parent_dir\":\"${dst_dir}\",\"src_dirents\":[${dirents}],\"conflict_policy\":\"${policy}\"}"
}

# Sync batch move — returns body (for checking conflict details)
batch_move_body() {
    local src_repo="$1" src_dir="$2" dst_repo="$3" dst_dir="$4"
    shift 4
    local dirents=""
    for d in "$@"; do
        if [ -n "$dirents" ]; then dirents="${dirents},"; fi
        dirents="${dirents}\"${d}\""
    done
    api_body "POST" "/api/v2.1/repos/sync-batch-move-item/" "$ADMIN_TOKEN" \
        "{\"src_repo_id\":\"${src_repo}\",\"src_parent_dir\":\"${src_dir}\",\"dst_repo_id\":\"${dst_repo}\",\"dst_parent_dir\":\"${dst_dir}\",\"src_dirents\":[${dirents}]}"
}

# Async batch copy — returns HTTP status (cross-repo path)
async_batch_copy() {
    local src_repo="$1" src_dir="$2" dst_repo="$3" dst_dir="$4"
    shift 4
    local dirents=""
    for d in "$@"; do
        if [ -n "$dirents" ]; then dirents="${dirents},"; fi
        dirents="${dirents}\"${d}\""
    done
    api_status "POST" "/api/v2.1/repos/async-batch-copy-item/" "$ADMIN_TOKEN" \
        "{\"src_repo_id\":\"${src_repo}\",\"src_parent_dir\":\"${src_dir}\",\"dst_repo_id\":\"${dst_repo}\",\"dst_parent_dir\":\"${dst_dir}\",\"src_dirents\":[${dirents}]}"
}

# Async batch copy — returns body (for checking conflict details)
async_batch_copy_body() {
    local src_repo="$1" src_dir="$2" dst_repo="$3" dst_dir="$4"
    shift 4
    local dirents=""
    for d in "$@"; do
        if [ -n "$dirents" ]; then dirents="${dirents},"; fi
        dirents="${dirents}\"${d}\""
    done
    api_body "POST" "/api/v2.1/repos/async-batch-copy-item/" "$ADMIN_TOKEN" \
        "{\"src_repo_id\":\"${src_repo}\",\"src_parent_dir\":\"${src_dir}\",\"dst_repo_id\":\"${dst_repo}\",\"dst_parent_dir\":\"${dst_dir}\",\"src_dirents\":[${dirents}]}"
}

# Async batch copy with conflict_policy — returns HTTP status
async_batch_copy_with_policy() {
    local src_repo="$1" src_dir="$2" dst_repo="$3" dst_dir="$4" policy="$5"
    shift 5
    local dirents=""
    for d in "$@"; do
        if [ -n "$dirents" ]; then dirents="${dirents},"; fi
        dirents="${dirents}\"${d}\""
    done
    api_status "POST" "/api/v2.1/repos/async-batch-copy-item/" "$ADMIN_TOKEN" \
        "{\"src_repo_id\":\"${src_repo}\",\"src_parent_dir\":\"${src_dir}\",\"dst_repo_id\":\"${dst_repo}\",\"dst_parent_dir\":\"${dst_dir}\",\"src_dirents\":[${dirents}],\"conflict_policy\":\"${policy}\"}"
}

# Async batch move with conflict_policy — returns HTTP status
async_batch_move_with_policy() {
    local src_repo="$1" src_dir="$2" dst_repo="$3" dst_dir="$4" policy="$5"
    shift 5
    local dirents=""
    for d in "$@"; do
        if [ -n "$dirents" ]; then dirents="${dirents},"; fi
        dirents="${dirents}\"${d}\""
    done
    api_status "POST" "/api/v2.1/repos/async-batch-move-item/" "$ADMIN_TOKEN" \
        "{\"src_repo_id\":\"${src_repo}\",\"src_parent_dir\":\"${src_dir}\",\"dst_repo_id\":\"${dst_repo}\",\"dst_parent_dir\":\"${dst_dir}\",\"src_dirents\":[${dirents}],\"conflict_policy\":\"${policy}\"}"
}

# Delete a library
delete_library() {
    local repo_id="$1"
    local attempt
    for attempt in 1 2 3; do
        api_status "DELETE" "/api/v2.1/repos/${repo_id}/" "$ADMIN_TOKEN" "" > /dev/null || true
        local status
        status=$(api_status "GET" "/api/v2.1/repos/${repo_id}/" "$ADMIN_TOKEN" "")
        if [ "$status" = "404" ]; then
            return 0
        fi
        sleep 1
    done
    return 1
}

# Parse options
for arg in "$@"; do
    case $arg in
        --quick)   QUICK_MODE=true ;;
        --verbose) VERBOSE=true ;;
        --help)
            head -45 "$0" | tail -40
            exit 0
            ;;
    esac
done

# =============================================================================
# Pre-flight
# =============================================================================

preflight() {
    log_section "Pre-flight Checks"
    if ! curl -s -o /dev/null -w "%{http_code}" "${API_URL}/api2/ping/" | grep -q "200"; then
        log_fail "API not reachable at ${API_URL}"
        echo "  Start the backend: docker compose up -d sesamefs"
        exit 1
    fi
    log_success "API is reachable at ${API_URL}"

    if ! command -v jq &> /dev/null; then
        log_fail "jq is required but not installed"
        exit 1
    fi

    local status=$(api_status "GET" "/api2/account/info/" "$ADMIN_TOKEN")
    run_test "Admin token is valid" "200" "$status"
}

# =============================================================================
# Setup: Create test library with nested structure
# =============================================================================

REPO_ID=""
REPO_ID2=""

setup_library() {
    log_section "Setup: Creating test library with nested structure"

    local ts=$(date +%s)
    REPO_ID=$(create_library "nested-move-copy-test-${ts}")

    if [ -z "$REPO_ID" ]; then
        log_fail "Could not create test library"
        exit 1
    fi
    log_info "Created library: $REPO_ID"

    # Build folder tree:
    #   /a/
    #   /a/b/
    #   /a/b/c/
    #   /a/b/c/d/         (depth 4)
    #   /x/
    #   /x/y/
    #   /x/y/z/
    create_dir "$REPO_ID" "/a"
    create_dir "$REPO_ID" "/a/b"
    create_dir "$REPO_ID" "/a/b/c"
    create_dir "$REPO_ID" "/a/b/c/d"
    create_dir "$REPO_ID" "/x"
    create_dir "$REPO_ID" "/x/y"
    create_dir "$REPO_ID" "/x/y/z"

    # Create test files at various locations
    create_file "$REPO_ID" "/root-file1.md"
    create_file "$REPO_ID" "/root-file2.md"
    create_file "$REPO_ID" "/root-file3.md"
    create_file "$REPO_ID" "/root-file4.md"
    create_file "$REPO_ID" "/root-file5.md"
    create_file "$REPO_ID" "/root-file6.md"
    create_file "$REPO_ID" "/a/file-a.md"
    create_file "$REPO_ID" "/a/b/file-ab.md"
    create_file "$REPO_ID" "/a/b/c/file-abc.md"
    create_file "$REPO_ID" "/x/y/file-xy.md"
    create_file "$REPO_ID" "/x/y/z/file-xyz.md"

    # Create folders that will be moved/copied
    create_dir "$REPO_ID" "/folder-to-move"
    create_file "$REPO_ID" "/folder-to-move/inside.md"
    create_dir "$REPO_ID" "/folder-to-copy"
    create_file "$REPO_ID" "/folder-to-copy/inside.md"
    create_dir "$REPO_ID" "/a/sub-to-move"
    create_file "$REPO_ID" "/a/sub-to-move/nested-inside.md"
    create_dir "$REPO_ID" "/a/sub-to-copy"
    create_file "$REPO_ID" "/a/sub-to-copy/nested-inside.md"

    # Multiple items for batch test
    create_file "$REPO_ID" "/batch1.md"
    create_file "$REPO_ID" "/batch2.md"
    create_file "$REPO_ID" "/batch3.md"
    create_file "$REPO_ID" "/batch-c1.md"
    create_file "$REPO_ID" "/batch-c2.md"
    create_file "$REPO_ID" "/batch-c3.md"

    sleep 1
    log_info "Setup complete — folder tree created"

    # Create second library for cross-repo tests
    REPO_ID2=$(create_library "nested-move-copy-test2-${ts}")
    if [ -z "$REPO_ID2" ]; then
        log_fail "Could not create second test library"
        exit 1
    fi
    log_info "Created second library: $REPO_ID2"

    # Seed second library with some files to create conflicts
    create_file "$REPO_ID2" "/cross-dup.md"
    create_file "$REPO_ID2" "/cross-replace.md"
    create_file "$REPO_ID2" "/cross-rename.md"
    create_dir "$REPO_ID2" "/sub"
    create_file "$REPO_ID2" "/sub/nested-dup.md"

    sleep 1
    log_info "Second library seeded"
}

# =============================================================================
# MOVE TESTS
# =============================================================================

test_move_root_to_depth1() {
    log_section "1. Move file: root → depth-1 (/a/)"

    local status=$(batch_move "$REPO_ID" "/" "$REPO_ID" "/a" "root-file1.md")
    run_test "Move root→depth-1 returns 200" "200" "$status"

    local body=$(list_dir "$REPO_ID" "/a")
    run_test_contains "File appears in /a/" '"name":"root-file1.md"' "$body"

    body=$(list_dir "$REPO_ID" "/")
    run_test_not_contains "File removed from root" '"name":"root-file1.md"' "$body"
}

test_move_root_to_depth2() {
    log_section "2. Move file: root → depth-2 (/a/b/)"

    local status=$(batch_move "$REPO_ID" "/" "$REPO_ID" "/a/b" "root-file2.md")
    run_test "Move root→depth-2 returns 200" "200" "$status"

    local body=$(list_dir "$REPO_ID" "/a/b")
    run_test_contains "File appears in /a/b/" '"name":"root-file2.md"' "$body"

    body=$(list_dir "$REPO_ID" "/")
    run_test_not_contains "File removed from root" '"name":"root-file2.md"' "$body"

    # Verify ancestors are intact
    body=$(list_dir "$REPO_ID" "/a")
    run_test_contains "Parent /a/ still has /b/" '"name":"b"' "$body"
}

test_move_root_to_depth3() {
    log_section "3. Move file: root → depth-3 (/a/b/c/)"

    local status=$(batch_move "$REPO_ID" "/" "$REPO_ID" "/a/b/c" "root-file3.md")
    run_test "Move root→depth-3 returns 200" "200" "$status"

    local body=$(list_dir "$REPO_ID" "/a/b/c")
    run_test_contains "File appears in /a/b/c/" '"name":"root-file3.md"' "$body"

    # Verify entire ancestor chain is intact
    body=$(list_dir "$REPO_ID" "/")
    run_test_contains "Root still has /a/" '"name":"a"' "$body"

    body=$(list_dir "$REPO_ID" "/a")
    run_test_contains "/a/ still has /b/" '"name":"b"' "$body"

    body=$(list_dir "$REPO_ID" "/a/b")
    run_test_contains "/a/b/ still has /c/" '"name":"c"' "$body"
}

test_move_depth2_to_depth2() {
    log_section "4. Move file: depth-2 → depth-2 (/a/b/ → /x/y/)"

    local status=$(batch_move "$REPO_ID" "/a/b" "$REPO_ID" "/x/y" "file-ab.md")
    run_test "Move depth-2→depth-2 returns 200" "200" "$status"

    local body=$(list_dir "$REPO_ID" "/x/y")
    run_test_contains "File appears in /x/y/" '"name":"file-ab.md"' "$body"

    body=$(list_dir "$REPO_ID" "/a/b")
    run_test_not_contains "File removed from /a/b/" '"name":"file-ab.md"' "$body"

    # Verify both ancestor chains intact
    body=$(list_dir "$REPO_ID" "/a")
    run_test_contains "/a/ still has /b/" '"name":"b"' "$body"

    body=$(list_dir "$REPO_ID" "/x")
    run_test_contains "/x/ still has /y/" '"name":"y"' "$body"
}

test_move_depth3_to_root() {
    log_section "5. Move file: depth-3 → root (/a/b/c/ → /)"

    local status=$(batch_move "$REPO_ID" "/a/b/c" "$REPO_ID" "/" "file-abc.md")
    run_test "Move depth-3→root returns 200" "200" "$status"

    local body=$(list_dir "$REPO_ID" "/")
    run_test_contains "File appears in root" '"name":"file-abc.md"' "$body"

    body=$(list_dir "$REPO_ID" "/a/b/c")
    run_test_not_contains "File removed from /a/b/c/" '"name":"file-abc.md"' "$body"
}

test_move_folder_root_to_depth2() {
    log_section "6. Move folder: root → depth-2 (/folder-to-move → /x/y/)"

    local status=$(batch_move "$REPO_ID" "/" "$REPO_ID" "/x/y" "folder-to-move")
    run_test "Move folder root→depth-2 returns 200" "200" "$status"

    local body=$(list_dir "$REPO_ID" "/x/y")
    run_test_contains "Folder appears in /x/y/" '"name":"folder-to-move"' "$body"

    # Verify folder contents survived the move
    body=$(list_dir "$REPO_ID" "/x/y/folder-to-move")
    run_test_contains "Folder contents intact after move" '"name":"inside.md"' "$body"

    body=$(list_dir "$REPO_ID" "/")
    run_test_not_contains "Folder removed from root" '"name":"folder-to-move"' "$body"
}

test_move_folder_depth1_to_depth2() {
    log_section "7. Move folder with contents: depth-1 → depth-2 (/a/sub-to-move → /x/y/)"

    local status=$(batch_move "$REPO_ID" "/a" "$REPO_ID" "/x/y" "sub-to-move")
    run_test "Move folder depth-1→depth-2 returns 200" "200" "$status"

    local body=$(list_dir "$REPO_ID" "/x/y")
    run_test_contains "Folder appears in /x/y/" '"name":"sub-to-move"' "$body"

    # Verify nested contents survived
    body=$(list_dir "$REPO_ID" "/x/y/sub-to-move")
    run_test_contains "Nested contents intact after move" '"name":"nested-inside.md"' "$body"

    body=$(list_dir "$REPO_ID" "/a")
    run_test_not_contains "Folder removed from /a/" '"name":"sub-to-move"' "$body"
}

test_move_multiple_batch() {
    log_section "8. Move multiple items in batch: root → depth-2 (/a/b/)"

    local status=$(batch_move "$REPO_ID" "/" "$REPO_ID" "/a/b" "batch1.md" "batch2.md" "batch3.md")
    run_test "Batch move (3 items) returns 200" "200" "$status"

    local body=$(list_dir "$REPO_ID" "/a/b")
    run_test_contains "batch1.md in /a/b/" '"name":"batch1.md"' "$body"
    run_test_contains "batch2.md in /a/b/" '"name":"batch2.md"' "$body"
    run_test_contains "batch3.md in /a/b/" '"name":"batch3.md"' "$body"

    body=$(list_dir "$REPO_ID" "/")
    run_test_not_contains "batch1.md removed from root" '"name":"batch1.md"' "$body"
    run_test_not_contains "batch2.md removed from root" '"name":"batch2.md"' "$body"
    run_test_not_contains "batch3.md removed from root" '"name":"batch3.md"' "$body"
}

# =============================================================================
# COPY TESTS
# =============================================================================

test_copy_root_to_depth1() {
    log_section "9. Copy file: root → depth-1 (/a/)"

    local status=$(batch_copy "$REPO_ID" "/" "$REPO_ID" "/a" "root-file4.md")
    run_test "Copy root→depth-1 returns 200" "200" "$status"

    local body=$(list_dir "$REPO_ID" "/a")
    run_test_contains "Copy appears in /a/" '"name":"root-file4.md"' "$body"

    body=$(list_dir "$REPO_ID" "/")
    run_test_contains "Original still in root" '"name":"root-file4.md"' "$body"
}

test_copy_root_to_depth2() {
    log_section "10. Copy file: root → depth-2 (/a/b/)"

    local status=$(batch_copy "$REPO_ID" "/" "$REPO_ID" "/a/b" "root-file5.md")
    run_test "Copy root→depth-2 returns 200" "200" "$status"

    local body=$(list_dir "$REPO_ID" "/a/b")
    run_test_contains "Copy appears in /a/b/" '"name":"root-file5.md"' "$body"

    body=$(list_dir "$REPO_ID" "/")
    run_test_contains "Original still in root" '"name":"root-file5.md"' "$body"

    # Verify ancestors intact
    body=$(list_dir "$REPO_ID" "/a")
    run_test_contains "Parent /a/ still has /b/" '"name":"b"' "$body"
}

test_copy_root_to_depth3() {
    log_section "11. Copy file: root → depth-3 (/x/y/z/)"

    local status=$(batch_copy "$REPO_ID" "/" "$REPO_ID" "/x/y/z" "root-file6.md")
    run_test "Copy root→depth-3 returns 200" "200" "$status"

    local body=$(list_dir "$REPO_ID" "/x/y/z")
    run_test_contains "Copy appears in /x/y/z/" '"name":"root-file6.md"' "$body"

    body=$(list_dir "$REPO_ID" "/")
    run_test_contains "Original still in root" '"name":"root-file6.md"' "$body"

    # Verify entire ancestor chain
    body=$(list_dir "$REPO_ID" "/x")
    run_test_contains "/x/ still has /y/" '"name":"y"' "$body"

    body=$(list_dir "$REPO_ID" "/x/y")
    run_test_contains "/x/y/ still has /z/" '"name":"z"' "$body"
}

test_copy_depth2_to_depth2() {
    log_section "12. Copy file: depth-2 → depth-2 (/x/y/ → /a/b/)"

    local status=$(batch_copy "$REPO_ID" "/x/y" "$REPO_ID" "/a/b" "file-xy.md")
    run_test "Copy depth-2→depth-2 returns 200" "200" "$status"

    local body=$(list_dir "$REPO_ID" "/a/b")
    run_test_contains "Copy appears in /a/b/" '"name":"file-xy.md"' "$body"

    body=$(list_dir "$REPO_ID" "/x/y")
    run_test_contains "Original still in /x/y/" '"name":"file-xy.md"' "$body"
}

test_copy_depth3_to_root() {
    log_section "13. Copy file: depth-3 → root (/x/y/z/ → /)"

    local status=$(batch_copy "$REPO_ID" "/x/y/z" "$REPO_ID" "/" "file-xyz.md")
    run_test "Copy depth-3→root returns 200" "200" "$status"

    local body=$(list_dir "$REPO_ID" "/")
    run_test_contains "Copy appears in root" '"name":"file-xyz.md"' "$body"

    body=$(list_dir "$REPO_ID" "/x/y/z")
    run_test_contains "Original still in /x/y/z/" '"name":"file-xyz.md"' "$body"
}

test_copy_folder_root_to_depth2() {
    log_section "14. Copy folder: root → depth-2 (/folder-to-copy → /x/y/)"

    local status=$(batch_copy "$REPO_ID" "/" "$REPO_ID" "/x/y" "folder-to-copy")
    run_test "Copy folder root→depth-2 returns 200" "200" "$status"

    local body=$(list_dir "$REPO_ID" "/x/y")
    run_test_contains "Folder copy appears in /x/y/" '"name":"folder-to-copy"' "$body"

    # Verify folder contents in copy
    body=$(list_dir "$REPO_ID" "/x/y/folder-to-copy")
    run_test_contains "Copied folder contents intact" '"name":"inside.md"' "$body"

    # Verify original still exists
    body=$(list_dir "$REPO_ID" "/")
    run_test_contains "Original folder still in root" '"name":"folder-to-copy"' "$body"
}

test_copy_folder_depth1_to_depth2() {
    log_section "15. Copy folder with contents: depth-1 → depth-2 (/a/sub-to-copy → /x/y/)"

    local status=$(batch_copy "$REPO_ID" "/a" "$REPO_ID" "/x/y" "sub-to-copy")
    run_test "Copy folder depth-1→depth-2 returns 200" "200" "$status"

    local body=$(list_dir "$REPO_ID" "/x/y")
    run_test_contains "Folder copy appears in /x/y/" '"name":"sub-to-copy"' "$body"

    # Verify nested contents in copy
    body=$(list_dir "$REPO_ID" "/x/y/sub-to-copy")
    run_test_contains "Copied nested contents intact" '"name":"nested-inside.md"' "$body"

    # Verify original still exists
    body=$(list_dir "$REPO_ID" "/a")
    run_test_contains "Original folder still in /a/" '"name":"sub-to-copy"' "$body"
}

test_copy_multiple_batch() {
    log_section "16. Copy multiple items in batch: root → depth-2 (/a/b/)"

    local status=$(batch_copy "$REPO_ID" "/" "$REPO_ID" "/a/b" "batch-c1.md" "batch-c2.md" "batch-c3.md")
    run_test "Batch copy (3 items) returns 200" "200" "$status"

    local body=$(list_dir "$REPO_ID" "/a/b")
    run_test_contains "batch-c1.md in /a/b/" '"name":"batch-c1.md"' "$body"
    run_test_contains "batch-c2.md in /a/b/" '"name":"batch-c2.md"' "$body"
    run_test_contains "batch-c3.md in /a/b/" '"name":"batch-c3.md"' "$body"

    # Originals still in root
    body=$(list_dir "$REPO_ID" "/")
    run_test_contains "batch-c1.md still in root" '"name":"batch-c1.md"' "$body"
    run_test_contains "batch-c2.md still in root" '"name":"batch-c2.md"' "$body"
    run_test_contains "batch-c3.md still in root" '"name":"batch-c3.md"' "$body"
}

# =============================================================================
# CHAINED OPERATIONS (sequential moves/copies that stress tree rebuild)
# =============================================================================

test_chained_moves() {
    log_section "17. Chained moves — multiple sequential moves in same tree"

    # Move file-a.md from /a/ to /a/b/c/ then back to /a/
    # This stresses the rebuild logic with rapid ancestor updates

    local status=$(batch_move "$REPO_ID" "/a" "$REPO_ID" "/a/b/c" "file-a.md")
    run_test "Chain move 1: /a/ → /a/b/c/ returns 200" "200" "$status"

    local body=$(list_dir "$REPO_ID" "/a/b/c")
    run_test_contains "File arrived in /a/b/c/" '"name":"file-a.md"' "$body"

    # Move it back
    status=$(batch_move "$REPO_ID" "/a/b/c" "$REPO_ID" "/a" "file-a.md")
    run_test "Chain move 2: /a/b/c/ → /a/ returns 200" "200" "$status"

    body=$(list_dir "$REPO_ID" "/a")
    run_test_contains "File back in /a/" '"name":"file-a.md"' "$body"

    # Verify full tree integrity — all directories still navigable
    body=$(list_dir "$REPO_ID" "/a/b")
    run_test_contains "After chained moves: /a/b/ has /c/" '"name":"c"' "$body"

    body=$(list_dir "$REPO_ID" "/a/b/c")
    run_test_contains "After chained moves: /a/b/c/ has /d/" '"name":"d"' "$body"
}

test_chained_copies() {
    log_section "18. Chained copies — copy cascading through nested dirs"

    # Copy file-a.md from /a/ → /a/b/, then /a/b/ → /a/b/c/
    local status=$(batch_copy "$REPO_ID" "/a" "$REPO_ID" "/a/b" "file-a.md")
    run_test "Chain copy 1: /a/ → /a/b/ returns 200" "200" "$status"

    status=$(batch_copy "$REPO_ID" "/a/b" "$REPO_ID" "/a/b/c" "file-a.md")
    run_test "Chain copy 2: /a/b/ → /a/b/c/ returns 200" "200" "$status"

    # All three locations should have the file
    local body=$(list_dir "$REPO_ID" "/a")
    run_test_contains "Original still in /a/" '"name":"file-a.md"' "$body"

    body=$(list_dir "$REPO_ID" "/a/b")
    run_test_contains "Copy in /a/b/" '"name":"file-a.md"' "$body"

    body=$(list_dir "$REPO_ID" "/a/b/c")
    run_test_contains "Copy in /a/b/c/" '"name":"file-a.md"' "$body"
}

# =============================================================================
# DEEP PATH TESTS (depth 4+)
# =============================================================================

test_move_into_depth4() {
    log_section "19. Move into depth-4 (/a/b/c/d/)"

    if [ "$QUICK_MODE" = true ]; then
        log_info "Skipping (--quick mode)"
        return
    fi

    # Create a file at root to move deep
    create_file "$REPO_ID" "/deep-move.md"
    sleep 1

    local status=$(batch_move "$REPO_ID" "/" "$REPO_ID" "/a/b/c/d" "deep-move.md")
    run_test "Move root→depth-4 returns 200" "200" "$status"

    local body=$(list_dir "$REPO_ID" "/a/b/c/d")
    run_test_contains "File appears in /a/b/c/d/" '"name":"deep-move.md"' "$body"

    # Verify full chain intact
    body=$(list_dir "$REPO_ID" "/a")
    run_test_contains "After deep move: /a/ has /b/" '"name":"b"' "$body"

    body=$(list_dir "$REPO_ID" "/a/b")
    run_test_contains "After deep move: /a/b/ has /c/" '"name":"c"' "$body"

    body=$(list_dir "$REPO_ID" "/a/b/c")
    run_test_contains "After deep move: /a/b/c/ has /d/" '"name":"d"' "$body"
}

test_copy_into_depth4() {
    log_section "20. Copy into depth-4 (/a/b/c/d/)"

    if [ "$QUICK_MODE" = true ]; then
        log_info "Skipping (--quick mode)"
        return
    fi

    create_file "$REPO_ID" "/deep-copy.md"
    sleep 1

    local status=$(batch_copy "$REPO_ID" "/" "$REPO_ID" "/a/b/c/d" "deep-copy.md")
    run_test "Copy root→depth-4 returns 200" "200" "$status"

    local body=$(list_dir "$REPO_ID" "/a/b/c/d")
    run_test_contains "Copy appears in /a/b/c/d/" '"name":"deep-copy.md"' "$body"

    body=$(list_dir "$REPO_ID" "/")
    run_test_contains "Original still in root" '"name":"deep-copy.md"' "$body"

    # Verify full chain intact
    body=$(list_dir "$REPO_ID" "/a")
    run_test_contains "After deep copy: /a/ has /b/" '"name":"b"' "$body"

    body=$(list_dir "$REPO_ID" "/a/b")
    run_test_contains "After deep copy: /a/b/ has /c/" '"name":"c"' "$body"

    body=$(list_dir "$REPO_ID" "/a/b/c")
    run_test_contains "After deep copy: /a/b/c/ has /d/" '"name":"d"' "$body"
}

# =============================================================================
# Test 21: Move file — duplicate name rejected
# =============================================================================

test_move_duplicate_name() {
    log_section "21. Move file — duplicate name rejected"

    # Create two files with the same name in different directories
    create_file "$REPO_ID" "/dup-src-move.md"
    create_file "$REPO_ID" "/a/dup-src-move.md"

    # Try to move /dup-src-move.md → /a/ where /a/dup-src-move.md already exists
    local status=$(batch_move "$REPO_ID" "/" "$REPO_ID" "/a" "dup-src-move.md")
    run_test "Move to dir with same-name file returns 409" "409" "$status"

    # Verify source file is still in place (move was rejected)
    local body=$(list_dir "$REPO_ID" "/")
    run_test_contains "Source file still at root after rejected move" '"name":"dup-src-move.md"' "$body"

    # Verify destination file is unchanged
    body=$(list_dir "$REPO_ID" "/a")
    run_test_contains "Existing file still in /a/" '"name":"dup-src-move.md"' "$body"
}

# =============================================================================
# Test 22: Copy file — duplicate name rejected
# =============================================================================

test_copy_duplicate_name() {
    log_section "22. Copy file — duplicate name rejected"

    # /dup-src-move.md still exists at root from test 21
    # /a/dup-src-move.md still exists from test 21
    # Create a fresh file for this test
    create_file "$REPO_ID" "/dup-src-copy.md"
    create_file "$REPO_ID" "/a/dup-src-copy.md"

    # Try to copy /dup-src-copy.md → /a/ where /a/dup-src-copy.md already exists
    local status=$(batch_copy "$REPO_ID" "/" "$REPO_ID" "/a" "dup-src-copy.md")
    run_test "Copy to dir with same-name file returns 409" "409" "$status"

    # Verify source file is still in place
    local body=$(list_dir "$REPO_ID" "/")
    run_test_contains "Source file still at root after rejected copy" '"name":"dup-src-copy.md"' "$body"

    # Verify destination file is unchanged
    body=$(list_dir "$REPO_ID" "/a")
    run_test_contains "Existing file still in /a/" '"name":"dup-src-copy.md"' "$body"
}

# =============================================================================
# Test 23: Move folder — duplicate name rejected
# =============================================================================

test_move_folder_duplicate_name() {
    log_section "23. Move folder — duplicate name rejected"

    # Create a folder "shared" at root and also under /a/
    create_dir "$REPO_ID" "/shared"
    create_dir "$REPO_ID" "/a/shared"

    # Try to move /shared → /a/ where /a/shared already exists
    local status=$(batch_move "$REPO_ID" "/" "$REPO_ID" "/a" "shared")
    run_test "Move folder with same name returns 409" "409" "$status"

    # Both should still exist
    local body=$(list_dir "$REPO_ID" "/")
    run_test_contains "Source folder still at root" '"name":"shared"' "$body"

    body=$(list_dir "$REPO_ID" "/a")
    run_test_contains "Existing folder still in /a/" '"name":"shared"' "$body"
}

# =============================================================================
# Test 24: Move to dir with same-name file at depth-2
# =============================================================================

test_move_duplicate_depth2() {
    log_section "24. Move duplicate name at depth-2"

    create_file "$REPO_ID" "/a/deep-dup.md"
    create_file "$REPO_ID" "/a/b/deep-dup.md"

    # Try to move /a/deep-dup.md → /a/b/ where /a/b/deep-dup.md exists
    local status=$(batch_move "$REPO_ID" "/a" "$REPO_ID" "/a/b" "deep-dup.md")
    run_test "Move duplicate at depth-2 returns 409" "409" "$status"

    # Source should still be in /a/
    local body=$(list_dir "$REPO_ID" "/a")
    run_test_contains "Source still in /a/" '"name":"deep-dup.md"' "$body"

    # Destination should still have its own copy
    body=$(list_dir "$REPO_ID" "/a/b")
    run_test_contains "Existing file still in /a/b/" '"name":"deep-dup.md"' "$body"
}

# =============================================================================
# Test 25: Conflict response includes conflicting_items list
# =============================================================================

test_conflict_response_body() {
    log_section "25. Conflict response includes conflicting_items"

    # /dup-src-move.md and /a/dup-src-move.md should still exist from test 21
    local body=$(batch_move_body "$REPO_ID" "/" "$REPO_ID" "/a" "dup-src-move.md")
    run_test_contains "Conflict body has error=conflict" '"error":"conflict"' "$body"
    run_test_contains "Conflict body has conflicting_items" '"conflicting_items"' "$body"
    run_test_contains "Conflict body lists dup-src-move.md" '"dup-src-move.md"' "$body"
}

# =============================================================================
# Test 26: conflict_policy=replace overwrites existing file
# =============================================================================

test_conflict_replace() {
    log_section "26. conflict_policy=replace overwrites existing"

    # Create fresh files for this test
    create_file "$REPO_ID" "/replace-src.md"
    create_file "$REPO_ID" "/a/replace-src.md"

    # Copy with replace policy
    local status=$(batch_copy_with_policy "$REPO_ID" "/" "$REPO_ID" "/a" "replace" "replace-src.md")
    run_test "Copy with replace policy returns 200" "200" "$status"

    # Source should still exist (copy, not move)
    local body=$(list_dir "$REPO_ID" "/")
    run_test_contains "Source still at root after replace-copy" '"name":"replace-src.md"' "$body"

    # Destination should still have exactly one copy (replaced)
    body=$(list_dir "$REPO_ID" "/a")
    run_test_contains "File exists in /a/ after replace" '"name":"replace-src.md"' "$body"
}

# =============================================================================
# Test 27: conflict_policy=autorename creates "file (1).ext"
# =============================================================================

test_conflict_autorename() {
    log_section "27. conflict_policy=autorename creates renamed copy"

    # Create fresh files
    create_file "$REPO_ID" "/rename-src.md"
    create_file "$REPO_ID" "/a/rename-src.md"

    # Copy with autorename policy
    local status=$(batch_copy_with_policy "$REPO_ID" "/" "$REPO_ID" "/a" "autorename" "rename-src.md")
    run_test "Copy with autorename policy returns 200" "200" "$status"

    # Destination should have both original and renamed
    local body=$(list_dir "$REPO_ID" "/a")
    run_test_contains "Original still in /a/" '"name":"rename-src.md"' "$body"
    run_test_contains "Renamed copy in /a/" '"name":"rename-src (1).md"' "$body"
}

# =============================================================================
# Test 28: conflict_policy=skip silently skips
# =============================================================================

test_conflict_skip() {
    log_section "28. conflict_policy=skip silently skips"

    # Create fresh files
    create_file "$REPO_ID" "/skip-src.md"
    create_file "$REPO_ID" "/a/skip-src.md"

    # Move with skip policy — should succeed but not actually move
    local status=$(batch_move_with_policy "$REPO_ID" "/" "$REPO_ID" "/a" "skip" "skip-src.md")
    run_test "Move with skip policy returns 200" "200" "$status"

    # Source should still exist (skipped)
    local body=$(list_dir "$REPO_ID" "/")
    run_test_contains "Source still at root after skip" '"name":"skip-src.md"' "$body"

    # Destination should still have original
    body=$(list_dir "$REPO_ID" "/a")
    run_test_contains "Original still in /a/ after skip" '"name":"skip-src.md"' "$body"
}

# =============================================================================
# Test 29: Cross-repo copy — duplicate name returns 409
# =============================================================================

test_cross_repo_copy_conflict() {
    log_section "29. Cross-repo copy — duplicate name returns 409"

    # Create source file in REPO_ID with same name as existing in REPO_ID2
    create_file "$REPO_ID" "/cross-dup.md"

    # Try to copy cross-repo where same name exists
    local status=$(async_batch_copy "$REPO_ID" "/" "$REPO_ID2" "/" "cross-dup.md")
    run_test "Cross-repo copy with same-name file returns 409" "409" "$status"

    # Verify source file still exists
    local body=$(list_dir "$REPO_ID" "/")
    run_test_contains "Source still in repo1 after rejected cross-repo copy" '"name":"cross-dup.md"' "$body"

    # Verify destination file is unchanged
    body=$(list_dir "$REPO_ID2" "/")
    run_test_contains "Existing file still in repo2" '"name":"cross-dup.md"' "$body"
}

# =============================================================================
# Test 30: Cross-repo copy — conflict response body
# =============================================================================

test_cross_repo_copy_conflict_body() {
    log_section "30. Cross-repo copy — conflict response includes details"

    local body=$(async_batch_copy_body "$REPO_ID" "/" "$REPO_ID2" "/" "cross-dup.md")
    run_test_contains "Cross-repo conflict has error=conflict" '"error":"conflict"' "$body"
    run_test_contains "Cross-repo conflict has conflicting_items" '"conflicting_items"' "$body"
    run_test_contains "Cross-repo conflict lists cross-dup.md" '"cross-dup.md"' "$body"
}

# =============================================================================
# Test 31: Cross-repo copy — replace policy works
# =============================================================================

test_cross_repo_copy_replace() {
    log_section "31. Cross-repo copy — replace policy"

    # Create source file
    create_file "$REPO_ID" "/cross-replace.md"

    # Copy with replace policy — should succeed (returns task_id for async)
    local status=$(async_batch_copy_with_policy "$REPO_ID" "/" "$REPO_ID2" "/" "replace" "cross-replace.md")
    run_test "Cross-repo copy with replace returns 200" "200" "$status"

    # Wait for async task to complete
    sleep 2

    # Source should still exist (it's a copy)
    local body=$(list_dir "$REPO_ID" "/")
    run_test_contains "Source still in repo1 after replace-copy" '"name":"cross-replace.md"' "$body"

    # Destination should have the file
    body=$(list_dir "$REPO_ID2" "/")
    run_test_contains "File in repo2 after replace" '"name":"cross-replace.md"' "$body"
}

# =============================================================================
# Test 32: Cross-repo copy — autorename policy works
# =============================================================================

test_cross_repo_copy_autorename() {
    log_section "32. Cross-repo copy — autorename policy"

    # Create source file
    create_file "$REPO_ID" "/cross-rename.md"

    # Copy with autorename
    local status=$(async_batch_copy_with_policy "$REPO_ID" "/" "$REPO_ID2" "/" "autorename" "cross-rename.md")
    run_test "Cross-repo copy with autorename returns 200" "200" "$status"

    # Wait for async task
    sleep 2

    # Both original and renamed should exist in destination
    local body=$(list_dir "$REPO_ID2" "/")
    run_test_contains "Original still in repo2" '"name":"cross-rename.md"' "$body"
    run_test_contains "Renamed copy in repo2" '"name":"cross-rename (1).md"' "$body"
}

# =============================================================================
# Test 33: Cross-repo copy — nested path conflict returns 409
# =============================================================================

test_cross_repo_nested_conflict() {
    log_section "33. Cross-repo copy — nested path conflict returns 409"

    # Create source file in REPO_ID to match REPO_ID2/sub/nested-dup.md
    create_dir "$REPO_ID" "/sub2"
    create_file "$REPO_ID" "/sub2/nested-dup.md"

    # Try to copy cross-repo into /sub/ where same name exists
    local status=$(async_batch_copy "$REPO_ID" "/sub2" "$REPO_ID2" "/sub" "nested-dup.md")
    run_test "Cross-repo nested copy conflict returns 409" "409" "$status"
}

# =============================================================================
# Test 34: Move with autorename — source file removed correctly
# =============================================================================

test_move_autorename_source_removed() {
    log_section "34. Move with autorename — source file removed"

    # Create fresh files
    create_file "$REPO_ID" "/mv-rename-src.md"
    create_file "$REPO_ID" "/a/mv-rename-src.md"

    # Move with autorename: source /mv-rename-src.md → /a/ where /a/mv-rename-src.md exists
    local status=$(batch_move_with_policy "$REPO_ID" "/" "$REPO_ID" "/a" "autorename" "mv-rename-src.md")
    run_test "Move with autorename returns 200" "200" "$status"

    # Source should be REMOVED (it's a move, not a copy)
    local body=$(list_dir "$REPO_ID" "/")
    run_test_not_contains "Source removed from root after autorename-move" '"name":"mv-rename-src.md"' "$body"

    # Destination should have both original and renamed
    body=$(list_dir "$REPO_ID" "/a")
    run_test_contains "Original still in /a/" '"name":"mv-rename-src.md"' "$body"
    run_test_contains "Renamed file in /a/" '"name":"mv-rename-src (1).md"' "$body"
}

# =============================================================================
# Test 35: Same-library copy from nested path to root (user's exact scenario)
# =============================================================================

test_copy_nested_to_root_conflict() {
    log_section "35. Copy from nested path to root — same name conflict"

    # Simulate the user's scenario: /test/test/test.docx → / where /test.docx exists
    create_dir "$REPO_ID" "/deep-test"
    create_dir "$REPO_ID" "/deep-test/inner"
    create_file "$REPO_ID" "/deep-test/inner/conflict-file.md"
    create_file "$REPO_ID" "/conflict-file.md"

    # Copy should get 409
    local status=$(batch_copy "$REPO_ID" "/deep-test/inner" "$REPO_ID" "/" "conflict-file.md")
    run_test "Copy nested to root with conflict returns 409" "409" "$status"

    # Now retry with replace policy
    status=$(batch_copy_with_policy "$REPO_ID" "/deep-test/inner" "$REPO_ID" "/" "replace" "conflict-file.md")
    run_test "Copy nested to root with replace returns 200" "200" "$status"

    # Now retry autorename (need a new conflict)
    status=$(batch_copy_with_policy "$REPO_ID" "/deep-test/inner" "$REPO_ID" "/" "autorename" "conflict-file.md")
    run_test "Copy nested to root with autorename returns 200" "200" "$status"

    # Verify both exist at root
    local body=$(list_dir "$REPO_ID" "/")
    run_test_contains "Original at root" '"name":"conflict-file.md"' "$body"
    run_test_contains "Renamed copy at root" '"name":"conflict-file (1).md"' "$body"
}

# =============================================================================
# Cleanup
# =============================================================================

cleanup() {
    log_section "Cleanup"
    if [ -n "$REPO_ID" ]; then
        if delete_library "$REPO_ID"; then
            log_info "Deleted test library: $REPO_ID"
        else
            log_fail "Failed to delete test library: $REPO_ID"
        fi
        REPO_ID=""
    fi
    if [ -n "$REPO_ID2" ]; then
        if delete_library "$REPO_ID2"; then
            log_info "Deleted second test library: $REPO_ID2"
        else
            log_fail "Failed to delete second test library: $REPO_ID2"
        fi
        REPO_ID2=""
    fi
}

trap cleanup EXIT

# =============================================================================
# Summary
# =============================================================================

print_summary() {
    echo ""
    echo -e "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${CYAN}  Nested Move/Copy Test Summary${NC}"
    echo -e "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo ""
    echo -e "  Total:  $TOTAL_TESTS"
    echo -e "  ${GREEN}Passed: $PASSED_TESTS${NC}"
    echo -e "  ${RED}Failed: $FAILED_TESTS${NC}"

    if [ ${#FAILED_TEST_NAMES[@]} -gt 0 ]; then
        echo ""
        echo -e "  ${RED}Failed tests:${NC}"
        for name in "${FAILED_TEST_NAMES[@]}"; do
            echo -e "    - $name"
        done
    fi

    echo ""
    if [ $FAILED_TESTS -eq 0 ]; then
        echo -e "  ${GREEN}All tests passed!${NC}"
    else
        echo -e "  ${RED}Some tests failed.${NC}"
    fi
    echo ""
}

# =============================================================================
# Run All Tests
# =============================================================================

main() {
    echo -e "${CYAN}SesameFS Nested Move/Copy Integration Tests${NC}"
    echo -e "API URL: ${API_URL}"
    echo ""

    preflight
    setup_library

    # Move tests (1-8)
    test_move_root_to_depth1
    test_move_root_to_depth2
    test_move_root_to_depth3
    test_move_depth2_to_depth2
    test_move_depth3_to_root
    test_move_folder_root_to_depth2
    test_move_folder_depth1_to_depth2
    test_move_multiple_batch

    # Copy tests (9-16)
    test_copy_root_to_depth1
    test_copy_root_to_depth2
    test_copy_root_to_depth3
    test_copy_depth2_to_depth2
    test_copy_depth3_to_root
    test_copy_folder_root_to_depth2
    test_copy_folder_depth1_to_depth2
    test_copy_multiple_batch

    # Chained operations (17-18)
    test_chained_moves
    test_chained_copies

    # Deep path tests (19-20)
    test_move_into_depth4
    test_copy_into_depth4

    # Duplicate name tests (21-24)
    test_move_duplicate_name
    test_copy_duplicate_name
    test_move_folder_duplicate_name
    test_move_duplicate_depth2

    # Conflict policy tests (25-28)
    test_conflict_response_body
    test_conflict_replace
    test_conflict_autorename
    test_conflict_skip

    # Cross-repo conflict tests (29-33)
    test_cross_repo_copy_conflict
    test_cross_repo_copy_conflict_body
    test_cross_repo_copy_replace
    test_cross_repo_copy_autorename
    test_cross_repo_nested_conflict

    # Autorename move + nested-to-root tests (34-35)
    test_move_autorename_source_removed
    test_copy_nested_to_root_conflict

    if [ "$QUICK_MODE" = true ]; then
        log_info "Quick mode still performs cleanup on exit"
    fi

    print_summary

    [ $FAILED_TESTS -eq 0 ]
}

main
