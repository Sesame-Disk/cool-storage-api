#!/bin/bash
# =============================================================================
# Cross-Library Copy/Move Integrity Tests for SesameFS
# =============================================================================
#
# Regression tests for the cross-library copy/move bug where fs_objects were
# not created in the destination library. Since fs_objects is keyed by
# (library_id, fs_id), reusing the source fs_id without creating a new row
# in the destination library made copied files unreadable.
#
# These tests upload files with real content, copy/move them across libraries,
# then verify the files are actually downloadable from the destination.
#
# Test Scenarios:
#   1.  Cross-library copy — file appears in destination listing
#   2.  Cross-library copy — file is downloadable from destination
#   3.  Cross-library copy — downloaded content matches original
#   4.  Cross-library copy directory — files inside are listed
#   5.  Cross-library copy directory — files inside are downloadable
#   6.  Cross-library move — file appears in destination listing
#   7.  Cross-library move — file is downloadable from destination
#   8.  Cross-library move — file removed from source
#   9.  Cross-library copy multiple files — all are downloadable
#  10.  Cross-library copy to subdirectory — file is downloadable
#
# Usage:
#   ./scripts/test-cross-library-integrity.sh [options]
#
# Options:
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
API_URL="${API_URL:-${SESAMEFS_URL:-http://localhost:8082}}"
ADMIN_TOKEN="${ADMIN_TOKEN:-dev-token-admin}"

# Options
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
        log_success "$test_name"
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
        log_success "$test_name"
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

# Returns both body and status
api_full() {
    local method="$1" endpoint="$2" token="$3" data="$4"
    local url="${API_URL}${endpoint}"
    local opts=(-s -w "\n%{http_code}")
    if [ -n "$token" ]; then opts+=(-H "Authorization: Token $token"); fi
    opts+=(-H "Content-Type: application/json")
    if [ -n "$data" ]; then opts+=(-d "$data"); fi
    curl "${opts[@]}" -X "$method" "$url"
}

# Create a library, return repo_id
create_library() {
    local name="$1"
    local body
    body=$(api_body "POST" "/api/v2.1/repos/" "$ADMIN_TOKEN" "{\"repo_name\":\"$name\"}")
    echo "$body" | jq -r '.repo_id // empty'
}

# Delete a library
delete_library() {
    local repo_id="$1"
    api_status "DELETE" "/api/v2.1/repos/${repo_id}/" "$ADMIN_TOKEN" > /dev/null
}

# Create a directory
create_dir() {
    local repo_id="$1" dir_path="$2"
    api_status "POST" "/api/v2.1/repos/${repo_id}/dir/?p=${dir_path}" "$ADMIN_TOKEN" '{}' > /dev/null
}

# Create an empty file
create_file() {
    local repo_id="$1" file_path="$2"
    api_status "POST" "/api/v2.1/repos/${repo_id}/file/?p=${file_path}&operation=create" "$ADMIN_TOKEN" > /dev/null
}

# List directory contents
list_dir() {
    local repo_id="$1" dir_path="$2"
    api_body "GET" "/api/v2.1/repos/${repo_id}/dir/?p=${dir_path}" "$ADMIN_TOKEN"
}

# Upload a file with actual content — returns HTTP status
upload_file() {
    local repo_id="$1" dir_path="$2" filename="$3" content="$4"
    local tmpfile=$(mktemp)
    echo -n "$content" > "$tmpfile"

    # Get upload link
    local response
    response=$(curl -s -w "\n%{http_code}" \
        -H "Authorization: Token $ADMIN_TOKEN" \
        "${API_URL}/api2/repos/${repo_id}/upload-link/?p=${dir_path}")
    local link_body=$(echo "$response" | head -n -1)
    local link_status=$(echo "$response" | tail -1)

    if [ "$link_status" != "200" ]; then
        rm -f "$tmpfile"
        if [ "$VERBOSE" = true ]; then
            echo -e "  ${YELLOW}Upload link failed:${NC} $link_status - $link_body" >&2
        fi
        echo "$link_status"
        return
    fi

    local upload_url=$(echo "$link_body" | tr -d '"')

    # Upload file
    response=$(curl -s -w "\n%{http_code}" -X POST "$upload_url" \
        -H "Authorization: Token $ADMIN_TOKEN" \
        -F "file=@${tmpfile};filename=${filename}" \
        -F "parent_dir=${dir_path}" \
        -F "relative_path=")
    local upload_status=$(echo "$response" | tail -1)

    rm -f "$tmpfile"
    echo "$upload_status"
}

# Get download link for a file — returns the URL
get_download_link() {
    local repo_id="$1" file_path="$2"
    local body
    body=$(curl -s \
        -H "Authorization: Token $ADMIN_TOKEN" \
        "${API_URL}/api2/repos/${repo_id}/file/?p=${file_path}")
    # Response is a quoted URL string
    echo "$body" | tr -d '"'
}

# Download file content — returns the content
download_file_content() {
    local repo_id="$1" file_path="$2"
    local download_url
    download_url=$(get_download_link "$repo_id" "$file_path")

    if [ -z "$download_url" ] || [ "$download_url" = "null" ] || [[ "$download_url" != http* ]]; then
        echo "ERROR_NO_DOWNLOAD_LINK"
        return
    fi

    curl -s -H "Authorization: Token $ADMIN_TOKEN" "$download_url"
}

# Download file and return HTTP status
download_file_status() {
    local repo_id="$1" file_path="$2"
    local download_url
    download_url=$(get_download_link "$repo_id" "$file_path")

    if [ -z "$download_url" ] || [ "$download_url" = "null" ] || [[ "$download_url" != http* ]]; then
        echo "NO_LINK"
        return
    fi

    curl -s -o /dev/null -w "%{http_code}" \
        -H "Authorization: Token $ADMIN_TOKEN" "$download_url"
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

# Async batch move — returns HTTP status (cross-repo path)
async_batch_move() {
    local src_repo="$1" src_dir="$2" dst_repo="$3" dst_dir="$4"
    shift 4
    local dirents=""
    for d in "$@"; do
        if [ -n "$dirents" ]; then dirents="${dirents},"; fi
        dirents="${dirents}\"${d}\""
    done
    api_status "POST" "/api/v2.1/repos/async-batch-move-item/" "$ADMIN_TOKEN" \
        "{\"src_repo_id\":\"${src_repo}\",\"src_parent_dir\":\"${src_dir}\",\"dst_repo_id\":\"${dst_repo}\",\"dst_parent_dir\":\"${dst_dir}\",\"src_dirents\":[${dirents}]}"
}

# Async batch copy with policy — returns HTTP status
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

# Parse options
for arg in "$@"; do
    case $arg in
        --verbose) VERBOSE=true ;;
        --help)
            head -40 "$0" | tail -35
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
# Setup: Create two test libraries
# =============================================================================

SRC_REPO=""
DST_REPO=""

setup() {
    log_section "Setup: Creating test libraries"

    local ts=$(date +%s)

    SRC_REPO=$(create_library "cross-lib-src-${ts}")
    if [ -z "$SRC_REPO" ]; then
        log_fail "Could not create source library"
        exit 1
    fi
    log_info "Created source library: $SRC_REPO"

    DST_REPO=$(create_library "cross-lib-dst-${ts}")
    if [ -z "$DST_REPO" ]; then
        log_fail "Could not create destination library"
        exit 1
    fi
    log_info "Created destination library: $DST_REPO"

    # Upload test files with real content to source library
    log_info "Uploading test files to source library..."

    local status

    status=$(upload_file "$SRC_REPO" "/" "test-doc.txt" "Hello from the source library! This is test content for cross-library copy.")
    if [ "$status" = "200" ] || [ "$status" = "201" ]; then
        log_info "Uploaded test-doc.txt"
    else
        log_fail "Failed to upload test-doc.txt (status: $status)"
    fi

    status=$(upload_file "$SRC_REPO" "/" "report.pdf" "Fake PDF content - this simulates a real document with binary-like data: $(head -c 200 /dev/urandom | base64 2>/dev/null || echo 'random-data-placeholder')")
    if [ "$status" = "200" ] || [ "$status" = "201" ]; then
        log_info "Uploaded report.pdf"
    else
        log_fail "Failed to upload report.pdf (status: $status)"
    fi

    # Create a folder with files for directory copy test
    create_dir "$SRC_REPO" "/docs"
    status=$(upload_file "$SRC_REPO" "/docs" "readme.md" "# Documentation\nThis is the readme for the docs folder.")
    if [ "$status" = "200" ] || [ "$status" = "201" ]; then
        log_info "Uploaded docs/readme.md"
    else
        log_fail "Failed to upload docs/readme.md (status: $status)"
    fi

    status=$(upload_file "$SRC_REPO" "/docs" "notes.txt" "Some important notes for cross-library testing.")
    if [ "$status" = "200" ] || [ "$status" = "201" ]; then
        log_info "Uploaded docs/notes.txt"
    else
        log_fail "Failed to upload docs/notes.txt (status: $status)"
    fi

    # Create files for batch and subdirectory tests
    status=$(upload_file "$SRC_REPO" "/" "batch-file1.txt" "Batch file 1 content")
    if [ "$status" = "200" ] || [ "$status" = "201" ]; then
        log_info "Uploaded batch-file1.txt"
    else
        log_fail "Failed to upload batch-file1.txt (status: $status)"
    fi

    status=$(upload_file "$SRC_REPO" "/" "batch-file2.txt" "Batch file 2 content")
    if [ "$status" = "200" ] || [ "$status" = "201" ]; then
        log_info "Uploaded batch-file2.txt"
    else
        log_fail "Failed to upload batch-file2.txt (status: $status)"
    fi

    status=$(upload_file "$SRC_REPO" "/" "move-me.txt" "This file will be moved to another library.")
    if [ "$status" = "200" ] || [ "$status" = "201" ]; then
        log_info "Uploaded move-me.txt"
    else
        log_fail "Failed to upload move-me.txt (status: $status)"
    fi

    # Create subdirectory in destination for subdirectory test
    create_dir "$DST_REPO" "/incoming"

    sleep 1
    log_info "Setup complete"
}

# =============================================================================
# Test 1: Cross-library copy — file appears in destination listing
# =============================================================================

test_cross_copy_file_listing() {
    log_section "1. Cross-library copy — file appears in destination listing"

    local status=$(async_batch_copy "$SRC_REPO" "/" "$DST_REPO" "/" "test-doc.txt")
    run_test "Async cross-library copy returns 200" "200" "$status"

    # Wait for async task
    sleep 2

    local body=$(list_dir "$DST_REPO" "/")
    run_test_contains "File appears in destination listing" '"name":"test-doc.txt"' "$body"

    # Source should still have the file (it's a copy)
    body=$(list_dir "$SRC_REPO" "/")
    run_test_contains "File still in source (copy, not move)" '"name":"test-doc.txt"' "$body"
}

# =============================================================================
# Test 2: Cross-library copy — file is downloadable from destination
# =============================================================================

test_cross_copy_file_downloadable() {
    log_section "2. Cross-library copy — file downloadable from destination"

    # Get download link from destination
    local download_url
    download_url=$(get_download_link "$DST_REPO" "/test-doc.txt")

    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    if [ -n "$download_url" ] && [ "$download_url" != "null" ] && [[ "$download_url" == http* ]]; then
        log_success "Got download link from destination library"
        PASSED_TESTS=$((PASSED_TESTS + 1))
    else
        log_fail "Failed to get download link from destination (got: $download_url)"
        FAILED_TESTS=$((FAILED_TESTS + 1))
        FAILED_TEST_NAMES+=("Got download link from destination library")
        return
    fi

    # Actually download the file
    local dl_status
    dl_status=$(download_file_status "$DST_REPO" "/test-doc.txt")
    run_test "Download from destination returns 200" "200" "$dl_status"
}

# =============================================================================
# Test 3: Cross-library copy — downloaded content matches original
# =============================================================================

test_cross_copy_content_matches() {
    log_section "3. Cross-library copy — content matches original"

    local src_content
    src_content=$(download_file_content "$SRC_REPO" "/test-doc.txt")

    local dst_content
    dst_content=$(download_file_content "$DST_REPO" "/test-doc.txt")

    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    if [ "$src_content" = "$dst_content" ] && [ "$src_content" != "ERROR_NO_DOWNLOAD_LINK" ]; then
        log_success "Content matches between source and destination"
        PASSED_TESTS=$((PASSED_TESTS + 1))
    else
        log_fail "Content mismatch or download error"
        FAILED_TESTS=$((FAILED_TESTS + 1))
        FAILED_TEST_NAMES+=("Content matches between source and destination")
        if [ "$VERBOSE" = true ]; then
            echo -e "  ${YELLOW}Source (first 80 chars):${NC} ${src_content:0:80}"
            echo -e "  ${YELLOW}Dest   (first 80 chars):${NC} ${dst_content:0:80}"
        fi
    fi
}

# =============================================================================
# Test 4: Cross-library copy directory — files inside are listed
# =============================================================================

test_cross_copy_dir_listing() {
    log_section "4. Cross-library copy directory — files listed in destination"

    local status=$(async_batch_copy "$SRC_REPO" "/" "$DST_REPO" "/" "docs")
    run_test "Async cross-library directory copy returns 200" "200" "$status"

    # Wait for async task
    sleep 2

    # Check that the directory exists in destination
    local body=$(list_dir "$DST_REPO" "/")
    run_test_contains "Directory 'docs' appears in destination" '"name":"docs"' "$body"

    # Check that files inside the directory exist
    body=$(list_dir "$DST_REPO" "/docs")
    run_test_contains "readme.md listed inside copied directory" '"name":"readme.md"' "$body"
    run_test_contains "notes.txt listed inside copied directory" '"name":"notes.txt"' "$body"
}

# =============================================================================
# Test 5: Cross-library copy directory — files inside are downloadable
# =============================================================================

test_cross_copy_dir_files_downloadable() {
    log_section "5. Cross-library copy directory — files downloadable"

    # Download readme.md from destination
    local dl_status
    dl_status=$(download_file_status "$DST_REPO" "/docs/readme.md")
    run_test "Download docs/readme.md from destination returns 200" "200" "$dl_status"

    # Download notes.txt from destination
    dl_status=$(download_file_status "$DST_REPO" "/docs/notes.txt")
    run_test "Download docs/notes.txt from destination returns 200" "200" "$dl_status"

    # Verify content matches
    local src_content dst_content

    src_content=$(download_file_content "$SRC_REPO" "/docs/readme.md")
    dst_content=$(download_file_content "$DST_REPO" "/docs/readme.md")

    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    if [ "$src_content" = "$dst_content" ] && [ "$src_content" != "ERROR_NO_DOWNLOAD_LINK" ]; then
        log_success "docs/readme.md content matches across libraries"
        PASSED_TESTS=$((PASSED_TESTS + 1))
    else
        log_fail "docs/readme.md content mismatch"
        FAILED_TESTS=$((FAILED_TESTS + 1))
        FAILED_TEST_NAMES+=("docs/readme.md content matches across libraries")
    fi

    src_content=$(download_file_content "$SRC_REPO" "/docs/notes.txt")
    dst_content=$(download_file_content "$DST_REPO" "/docs/notes.txt")

    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    if [ "$src_content" = "$dst_content" ] && [ "$src_content" != "ERROR_NO_DOWNLOAD_LINK" ]; then
        log_success "docs/notes.txt content matches across libraries"
        PASSED_TESTS=$((PASSED_TESTS + 1))
    else
        log_fail "docs/notes.txt content mismatch"
        FAILED_TESTS=$((FAILED_TESTS + 1))
        FAILED_TEST_NAMES+=("docs/notes.txt content matches across libraries")
    fi
}

# =============================================================================
# Test 6: Cross-library move — file appears in destination listing
# =============================================================================

test_cross_move_file_listing() {
    log_section "6. Cross-library move — file appears in destination"

    local status=$(async_batch_move "$SRC_REPO" "/" "$DST_REPO" "/" "move-me.txt")
    run_test "Async cross-library move returns 200" "200" "$status"

    # Wait for async task
    sleep 2

    local body=$(list_dir "$DST_REPO" "/")
    run_test_contains "Moved file appears in destination" '"name":"move-me.txt"' "$body"
}

# =============================================================================
# Test 7: Cross-library move — file is downloadable from destination
# =============================================================================

test_cross_move_file_downloadable() {
    log_section "7. Cross-library move — file downloadable from destination"

    local dl_status
    dl_status=$(download_file_status "$DST_REPO" "/move-me.txt")
    run_test "Download moved file from destination returns 200" "200" "$dl_status"

    # Verify content
    local content
    content=$(download_file_content "$DST_REPO" "/move-me.txt")
    run_test_contains "Moved file has expected content" "moved to another library" "$content"
}

# =============================================================================
# Test 8: Cross-library move — file removed from source
# =============================================================================

test_cross_move_file_removed_from_source() {
    log_section "8. Cross-library move — file removed from source"

    local body=$(list_dir "$SRC_REPO" "/")
    run_test_not_contains "Moved file removed from source" '"name":"move-me.txt"' "$body"
}

# =============================================================================
# Test 9: Cross-library copy multiple files — all are downloadable
# =============================================================================

test_cross_copy_multiple_downloadable() {
    log_section "9. Cross-library copy multiple files — all downloadable"

    local status=$(async_batch_copy "$SRC_REPO" "/" "$DST_REPO" "/" "batch-file1.txt" "batch-file2.txt")
    run_test "Async cross-library batch copy returns 200" "200" "$status"

    # Wait for async task
    sleep 2

    # Verify both files are listed
    local body=$(list_dir "$DST_REPO" "/")
    run_test_contains "batch-file1.txt in destination" '"name":"batch-file1.txt"' "$body"
    run_test_contains "batch-file2.txt in destination" '"name":"batch-file2.txt"' "$body"

    # Verify both files are downloadable
    local dl_status

    dl_status=$(download_file_status "$DST_REPO" "/batch-file1.txt")
    run_test "Download batch-file1.txt from destination returns 200" "200" "$dl_status"

    dl_status=$(download_file_status "$DST_REPO" "/batch-file2.txt")
    run_test "Download batch-file2.txt from destination returns 200" "200" "$dl_status"

    # Verify content
    local content
    content=$(download_file_content "$DST_REPO" "/batch-file1.txt")
    run_test_contains "batch-file1.txt has correct content" "Batch file 1 content" "$content"

    content=$(download_file_content "$DST_REPO" "/batch-file2.txt")
    run_test_contains "batch-file2.txt has correct content" "Batch file 2 content" "$content"
}

# =============================================================================
# Test 10: Cross-library copy to subdirectory — file is downloadable
# =============================================================================

test_cross_copy_to_subdir_downloadable() {
    log_section "10. Cross-library copy to subdirectory — file downloadable"

    local status=$(async_batch_copy "$SRC_REPO" "/" "$DST_REPO" "/incoming" "report.pdf")
    run_test "Async cross-library copy to subdir returns 200" "200" "$status"

    # Wait for async task
    sleep 2

    # Verify file is listed in the subdirectory
    local body=$(list_dir "$DST_REPO" "/incoming")
    run_test_contains "report.pdf in /incoming/" '"name":"report.pdf"' "$body"

    # Verify file is downloadable
    local dl_status
    dl_status=$(download_file_status "$DST_REPO" "/incoming/report.pdf")
    run_test "Download report.pdf from /incoming/ returns 200" "200" "$dl_status"
}

# =============================================================================
# Cleanup
# =============================================================================

cleanup() {
    log_section "Cleanup"
    if [ -n "$SRC_REPO" ]; then
        delete_library "$SRC_REPO" || true
        log_info "Deleted source library: $SRC_REPO"
        SRC_REPO=""
    fi
    if [ -n "$DST_REPO" ]; then
        delete_library "$DST_REPO" || true
        log_info "Deleted destination library: $DST_REPO"
        DST_REPO=""
    fi
}

trap cleanup EXIT

# =============================================================================
# Summary
# =============================================================================

print_summary() {
    echo ""
    echo -e "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${CYAN}  Cross-Library Copy/Move Integrity Test Summary${NC}"
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
    echo -e "${CYAN}SesameFS Cross-Library Copy/Move Integrity Tests${NC}"
    echo -e "API URL: ${API_URL}"
    echo ""

    preflight
    setup

    # Cross-library copy tests (1-5)
    test_cross_copy_file_listing
    test_cross_copy_file_downloadable
    test_cross_copy_content_matches
    test_cross_copy_dir_listing
    test_cross_copy_dir_files_downloadable

    # Cross-library move tests (6-8)
    test_cross_move_file_listing
    test_cross_move_file_downloadable
    test_cross_move_file_removed_from_source

    # Batch and subdirectory tests (9-10)
    test_cross_copy_multiple_downloadable
    test_cross_copy_to_subdir_downloadable

    print_summary

    [ $FAILED_TESTS -eq 0 ]
}

main
