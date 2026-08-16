#!/bin/bash
#
# Test script for batch move/copy operations
# Tests: sync-batch-move, sync-batch-copy, async-batch-move, async-batch-copy, copy-move-task
#
# Prerequisites:
# - SesameFS backend running on localhost:8082
#
# Usage: ./test-batch-operations.sh [token]

set -e

TOKEN="${1:-dev-token-admin}"
BASE_URL="${SESAMEFS_URL:-http://localhost:8082}"

echo "==================================================="
echo "Batch Operations Test (Move/Copy)"
echo "==================================================="
echo "Base URL: $BASE_URL"
echo "Token: $TOKEN"
echo ""

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Counters
TOTAL_TESTS=0
PASSED_TESTS=0
FAILED_TESTS=0

pass() {
    echo -e "${GREEN}✓ PASS${NC}: $1"
    PASSED_TESTS=$((PASSED_TESTS + 1))
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
}

fail() {
    echo -e "${RED}✗ FAIL${NC}: $1"
    FAILED_TESTS=$((FAILED_TESTS + 1))
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
}

info() { echo -e "${YELLOW}→${NC} $1"; }

api_get() {
    curl -s -w "\n%{http_code}" -H "Authorization: Token $TOKEN" "$BASE_URL$1"
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

# Check for 200 or 201 (success codes)
check_success() {
    local response="$1"
    local description="$2"

    status=$(echo "$response" | tail -1)
    body=$(echo "$response" | head -n -1)

    if [ "$status" = "200" ] || [ "$status" = "201" ]; then
        pass "$description (got $status)"
        return 0
    else
        fail "$description (expected 200 or 201, got $status)"
        echo "    Response: $body"
        return 1
    fi
}

# Keep the cleanup trap active before the create request. If the request or the
# response parsing fails after the server has created the library, the global
# cleanup script still has the batch-ops-test- prefix as a second safety net.
REPO_ID=""
cleanup() {
    if [ -z "$REPO_ID" ] || [ "$REPO_ID" = "null" ]; then
        return
    fi
    info "Cleaning up test library..."
    api_delete "/api/v2.1/repos/${REPO_ID}/" > /dev/null 2>&1 || true
    REPO_ID=""
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# Create a test library
info "Creating test library..."
TIMESTAMP=$(date +%s)
create_response=$(api_post_json "/api/v2.1/repos/" "{\"repo_name\":\"batch-ops-test-${TIMESTAMP}\"}")
create_body=$(echo "$create_response" | head -n -1)
create_status=$(echo "$create_response" | tail -1)

REPO_ID=$(echo "$create_body" | grep -o '"repo_id":"[^"]*"' | cut -d'"' -f4)

if [ -z "$REPO_ID" ] || [ "$REPO_ID" = "null" ]; then
    fail "Could not create test library"
    echo "Response: $create_body"
    exit 1
fi

pass "Created test library: $REPO_ID"
echo ""

# Setup: Create test folders
echo "=== Setup: Creating test folders ==="

# Create source folder
info "Creating /source-folder..."
response=$(api_post_json "/api/v2.1/repos/${REPO_ID}/dir/?p=/source-folder" '{}')
check_success "$response" "Create /source-folder"

# Create destination folder
info "Creating /dest-folder..."
response=$(api_post_json "/api/v2.1/repos/${REPO_ID}/dir/?p=/dest-folder" '{}')
check_success "$response" "Create /dest-folder"

# Create items inside source folder
info "Creating /source-folder/item1..."
response=$(api_post_json "/api/v2.1/repos/${REPO_ID}/dir/?p=/source-folder/item1" '{}')
check_success "$response" "Create /source-folder/item1"

info "Creating /source-folder/item2..."
response=$(api_post_json "/api/v2.1/repos/${REPO_ID}/dir/?p=/source-folder/item2" '{}')
check_success "$response" "Create /source-folder/item2"

info "Creating /source-folder/item3..."
response=$(api_post_json "/api/v2.1/repos/${REPO_ID}/dir/?p=/source-folder/item3" '{}')
check_success "$response" "Create /source-folder/item3"

echo ""
echo "=== Test 1: Sync Batch Move ==="
info "Moving item1 from /source-folder to /dest-folder..."
response=$(api_post_json "/api/v2.1/repos/sync-batch-move-item/" "{
    \"src_repo_id\": \"${REPO_ID}\",
    \"src_parent_dir\": \"/source-folder\",
    \"dst_repo_id\": \"${REPO_ID}\",
    \"dst_parent_dir\": \"/dest-folder\",
    \"src_dirents\": [\"item1\"]
}")
check_response "$response" "200" "Sync batch move"

# Verify item1 is in dest-folder
info "Verifying item1 in dest-folder..."
response=$(api_get "/api/v2.1/repos/${REPO_ID}/dir/?p=/dest-folder")
body=$(echo "$response" | head -n -1)
if echo "$body" | grep -q '"name":"item1"'; then
    pass "item1 found in dest-folder"
else
    fail "item1 NOT found in dest-folder"
fi

# Verify item1 is NOT in source-folder
info "Verifying item1 removed from source-folder..."
response=$(api_get "/api/v2.1/repos/${REPO_ID}/dir/?p=/source-folder")
body=$(echo "$response" | head -n -1)
if echo "$body" | grep -q '"name":"item1"'; then
    fail "item1 still in source-folder (should have been moved)"
else
    pass "item1 correctly removed from source-folder"
fi

echo ""
echo "=== Test 2: Sync Batch Copy ==="
info "Copying item2 from /source-folder to /dest-folder..."
response=$(api_post_json "/api/v2.1/repos/sync-batch-copy-item/" "{
    \"src_repo_id\": \"${REPO_ID}\",
    \"src_parent_dir\": \"/source-folder\",
    \"dst_repo_id\": \"${REPO_ID}\",
    \"dst_parent_dir\": \"/dest-folder\",
    \"src_dirents\": [\"item2\"]
}")
check_response "$response" "200" "Sync batch copy"

# Verify item2 is in dest-folder
info "Verifying item2 in dest-folder..."
response=$(api_get "/api/v2.1/repos/${REPO_ID}/dir/?p=/dest-folder")
body=$(echo "$response" | head -n -1)
if echo "$body" | grep -q '"name":"item2"'; then
    pass "item2 found in dest-folder"
else
    fail "item2 NOT found in dest-folder"
fi

# Verify item2 is STILL in source-folder (copy, not move)
info "Verifying item2 still in source-folder..."
response=$(api_get "/api/v2.1/repos/${REPO_ID}/dir/?p=/source-folder")
body=$(echo "$response" | head -n -1)
if echo "$body" | grep -q '"name":"item2"'; then
    pass "item2 still in source-folder (copy preserved original)"
else
    fail "item2 removed from source-folder (should be a copy)"
fi

echo ""
echo "=== Test 3: Async Batch Move ==="
info "Async moving item3 from /source-folder to /dest-folder..."
response=$(api_post_json "/api/v2.1/repos/async-batch-move-item/" "{
    \"src_repo_id\": \"${REPO_ID}\",
    \"src_parent_dir\": \"/source-folder\",
    \"dst_repo_id\": \"${REPO_ID}\",
    \"dst_parent_dir\": \"/dest-folder\",
    \"src_dirents\": [\"item3\"]
}")
check_response "$response" "200" "Async batch move returns task_id"

body=$(echo "$response" | head -n -1)
TASK_ID=$(echo "$body" | grep -o '"task_id":"[^"]*"' | cut -d'"' -f4)

if [ -z "$TASK_ID" ] || [ "$TASK_ID" = "null" ]; then
    fail "No task_id returned from async move"
else
    pass "Received task_id: $TASK_ID"
fi

# Wait a moment for async operation
sleep 1

echo ""
echo "=== Test 4: Query Task Progress ==="
info "Checking task progress..."
response=$(api_get "/api/v2.1/copy-move-task/?task_id=${TASK_ID}")
check_response "$response" "200" "Get task progress"

body=$(echo "$response" | head -n -1)
if echo "$body" | grep -q '"done":true'; then
    pass "Task completed (done: true)"
else
    fail "Task not completed"
fi

# Verify item3 is in dest-folder
info "Verifying item3 in dest-folder..."
response=$(api_get "/api/v2.1/repos/${REPO_ID}/dir/?p=/dest-folder")
body=$(echo "$response" | head -n -1)
if echo "$body" | grep -q '"name":"item3"'; then
    pass "item3 found in dest-folder"
else
    fail "item3 NOT found in dest-folder"
fi

echo ""
echo "=== Test 5: Error Handling - Duplicate Item ==="
info "Trying to copy item2 again (should fail - already exists)..."
response=$(api_post_json "/api/v2.1/repos/sync-batch-copy-item/" "{
    \"src_repo_id\": \"${REPO_ID}\",
    \"src_parent_dir\": \"/source-folder\",
    \"dst_repo_id\": \"${REPO_ID}\",
    \"dst_parent_dir\": \"/dest-folder\",
    \"src_dirents\": [\"item2\"]
}")
status=$(echo "$response" | tail -1)
body=$(echo "$response" | head -n -1)

if [ "$status" = "409" ]; then
    pass "Correctly rejected duplicate item (409 Conflict)"
elif [ "$status" = "500" ] && echo "$body" | grep -q "already exists"; then
    pass "Correctly rejected duplicate item (500 + error message)"
else
    fail "Should have rejected duplicate item (got status $status)"
fi

echo ""
echo "=== Test 6: Permission Check (Readonly User) ==="
info "Readonly user trying to move (should fail with 403)..."
response=$(curl -s -w "\n%{http_code}" -X POST \
    -H "Authorization: Token dev-token-readonly" \
    -H "Content-Type: application/json" \
    -d "{
        \"src_repo_id\": \"${REPO_ID}\",
        \"src_parent_dir\": \"/dest-folder\",
        \"dst_repo_id\": \"${REPO_ID}\",
        \"dst_parent_dir\": \"/source-folder\",
        \"src_dirents\": [\"item1\"]
    }" \
    "$BASE_URL/api/v2.1/repos/sync-batch-move-item/")
check_response "$response" "403" "Readonly user move rejected"

echo ""
echo "==================================================="
echo "Batch Operations Test Summary"
echo "==================================================="
echo "Total tests:  $TOTAL_TESTS"
echo -e "Passed:       ${GREEN}$PASSED_TESTS${NC}"
echo -e "Failed:       ${RED}$FAILED_TESTS${NC}"
echo ""

if [ $FAILED_TESTS -eq 0 ]; then
    echo -e "${GREEN}All tests passed!${NC}"
    exit 0
else
    echo -e "${RED}Some tests failed.${NC}"
    exit 1
fi
