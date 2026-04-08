#!/bin/bash
#
# Test script for file operations (Move, Copy, Delete, Rename)
# Tests Tasks #2 and #3: Move and Copy file operations
#
# Prerequisites:
# - SesameFS backend running on localhost:8082
# - At least one library exists with files
#
# Usage: ./test-file-operations.sh [token] [repo_id]

set -e

TOKEN="${1:-dev-token-admin}"
REPO_ID="${2:-}"
BASE_URL="${SESAMEFS_URL:-http://localhost:8082}"

echo "==================================================="
echo "File Operations Test (Move, Copy, Delete, Rename)"
echo "==================================================="
echo "Base URL: $BASE_URL"
echo "Token: $TOKEN"
echo ""

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

pass() { echo -e "${GREEN}✓ PASS${NC}: $1"; }
fail() { echo -e "${RED}✗ FAIL${NC}: $1"; }
info() { echo -e "${YELLOW}→${NC} $1"; }

# API helpers
api_get() {
    curl -s -w "\n%{http_code}" -H "Authorization: Token $TOKEN" "$BASE_URL$1"
}

api_post() {
    curl -s -w "\n%{http_code}" -X POST \
        -H "Authorization: Token $TOKEN" \
        -H "Content-Type: application/x-www-form-urlencoded" \
        -d "$2" "$BASE_URL$1"
}

api_post_json() {
    curl -s -w "\n%{http_code}" -X POST \
        -H "Authorization: Token $TOKEN" \
        -H "Content-Type: application/json" \
        -d "$2" "$BASE_URL$1"
}

api_delete() {
    curl -s -w "\n%{http_code}" -X DELETE \
        -H "Authorization: Token $TOKEN" "$BASE_URL$1"
}

check_response() {
    local response="$1"
    local expected_status="$2"
    local description="$3"

    status=$(echo "$response" | tail -1)
    body=$(echo "$response" | head -n -1)

    if [ "$status" = "$expected_status" ]; then
        pass "$description (got $status)"
        return 0
    else
        fail "$description (expected $expected_status, got $status)"
        echo "    Response: $body"
        return 1
    fi
}

# Always create a fresh test library to avoid conflicts
info "Creating fresh test library..."
TIMESTAMP=$(date +%s)
create_response=$(api_post_json "/api/v2.1/repos/" "{\"repo_name\":\"FileOpsTest-${TIMESTAMP}\"}")
create_body=$(echo "$create_response" | head -n -1)
create_status=$(echo "$create_response" | tail -1)
REPO_ID=$(echo "$create_body" | grep -o '"repo_id":"[^"]*"' | cut -d'"' -f4)

if [ -z "$REPO_ID" ] || [ "$REPO_ID" = "null" ]; then
    fail "Could not create test library"
    echo "Response: $create_body (status: $create_status)"
    exit 1
fi

# Cleanup function
cleanup() {
    info "Cleaning up test library..."
    api_delete "/api/v2.1/repos/${REPO_ID}/" > /dev/null 2>&1 || true
}
trap cleanup EXIT

echo ""
echo "Using library: $REPO_ID"
echo ""

# Setup: Create test structure using v2.1 API
echo "=== Setup: Creating test files and directories ==="

# Create test directory
info "Creating /test-dir..."
response=$(api_post_json "/api/v2.1/repos/$REPO_ID/dir/?p=/test-dir" "{}")
check_response "$response" "201" "Create /test-dir" || true

# Create a file for testing (using api2 at root level where it works)
info "Creating /test-file.txt..."
response=$(api_post "/api2/repos/$REPO_ID/file/?p=/test-file.txt&operation=create" "")
check_response "$response" "201" "Create /test-file.txt" || true

# Create source directory for move/copy tests
info "Creating /source-dir..."
response=$(api_post_json "/api/v2.1/repos/$REPO_ID/dir/?p=/source-dir" "{}")
check_response "$response" "201" "Create /source-dir" || true

# Create target directory for move/copy tests
info "Creating /target-dir..."
response=$(api_post_json "/api/v2.1/repos/$REPO_ID/dir/?p=/target-dir" "{}")
check_response "$response" "201" "Create /target-dir" || true

# Create nested directories to serve as test items (using v2.1 which handles nesting correctly)
info "Creating /source-dir/movable.txt (as directory)..."
response=$(api_post_json "/api/v2.1/repos/$REPO_ID/dir/?p=/source-dir/movable.txt" "{}")
check_response "$response" "201" "Create /source-dir/movable.txt" || true

# Create another item for copy test
info "Creating /source-dir/copyable.txt (as directory)..."
response=$(api_post_json "/api/v2.1/repos/$REPO_ID/dir/?p=/source-dir/copyable.txt" "{}")
check_response "$response" "201" "Create /source-dir/copyable.txt" || true

echo ""
echo "=== Test: Rename File ==="
info "Renaming /test-file.txt to /renamed-file.txt..."
response=$(api_post_json "/api2/repos/$REPO_ID/file/?p=/test-file.txt&operation=rename" '{"newname":"renamed-file.txt"}')
check_response "$response" "200" "Rename file"

echo ""
echo "=== Test: Rename Directory ==="
info "Renaming /test-dir to /renamed-dir..."
response=$(api_post_json "/api2/repos/$REPO_ID/dir/?p=/test-dir&operation=rename" '{"newname":"renamed-dir"}')
check_response "$response" "200" "Rename directory"

echo ""
echo "=== Test: Move File (using batch API) ==="
info "Moving /source-dir/movable.txt to /target-dir..."
response=$(api_post_json "/api/v2.1/repos/sync-batch-move-item/" \
    "{\"src_repo_id\":\"$REPO_ID\",\"src_parent_dir\":\"/source-dir\",\"dst_repo_id\":\"$REPO_ID\",\"dst_parent_dir\":\"/target-dir\",\"src_dirents\":[\"movable.txt\"]}")
check_response "$response" "200" "Move file to target-dir"

# Verify file moved
info "Verifying file in target-dir..."
response=$(api_get "/api/v2.1/repos/$REPO_ID/dir/?p=/target-dir")
body=$(echo "$response" | head -n -1)
if echo "$body" | grep -q "movable.txt"; then
    pass "File found in target-dir"
else
    fail "File not found in target-dir"
fi

echo ""
echo "=== Test: Copy File (using batch API) ==="
info "Copying /source-dir/copyable.txt to /target-dir..."
response=$(api_post_json "/api/v2.1/repos/sync-batch-copy-item/" \
    "{\"src_repo_id\":\"$REPO_ID\",\"src_parent_dir\":\"/source-dir\",\"dst_repo_id\":\"$REPO_ID\",\"dst_parent_dir\":\"/target-dir\",\"src_dirents\":[\"copyable.txt\"]}")
check_response "$response" "200" "Copy file to target-dir"

# Verify original still exists
info "Verifying original file still in source-dir..."
response=$(api_get "/api/v2.1/repos/$REPO_ID/dir/?p=/source-dir")
body=$(echo "$response" | head -n -1)
if echo "$body" | grep -q "copyable.txt"; then
    pass "Original file still in source-dir"
else
    fail "Original file missing from source-dir"
fi

# Verify copy exists
info "Verifying copy in target-dir..."
response=$(api_get "/api/v2.1/repos/$REPO_ID/dir/?p=/target-dir")
body=$(echo "$response" | head -n -1)
if echo "$body" | grep -q "copyable.txt"; then
    pass "Copy found in target-dir"
else
    fail "Copy not found in target-dir"
fi

echo ""
echo "=== Test: Delete File ==="
info "Deleting /renamed-file.txt..."
response=$(api_delete "/api2/repos/$REPO_ID/file/?p=/renamed-file.txt")
check_response "$response" "200" "Delete file"

echo ""
echo "=== Test: Delete Directory ==="
info "Deleting /renamed-dir..."
response=$(api_delete "/api2/repos/$REPO_ID/dir/?p=/renamed-dir")
check_response "$response" "200" "Delete directory"

echo ""
echo "=== Test: List Directory ==="
info "Listing root directory..."
response=$(api_get "/api/v2.1/repos/$REPO_ID/dir/?p=/")
check_response "$response" "200" "List directory"

echo ""
echo "==================================================="
echo "File Operations Test Complete"
echo "==================================================="
