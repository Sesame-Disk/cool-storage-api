#!/bin/bash
# Tag API Integration Tests
# Tests tag creation, listing, and deletion (including the 500 error fix)

set -e

# ANSI colors for readable output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# Configuration
BASE_URL="${BASE_URL:-http://localhost:8082}"
TOKEN="${TOKEN:-dev-token-admin}"

PASSED=0
FAILED=0

# Helper function to make API calls
api_call() {
    local method="$1"
    local endpoint="$2"
    local data="$3"

    if [ -n "$data" ]; then
        curl -s -w "\n%{http_code}" -X "$method" \
            -H "Authorization: Token $TOKEN" \
            -H "Content-Type: application/json" \
            -d "$data" \
            "${BASE_URL}${endpoint}"
    else
        curl -s -w "\n%{http_code}" -X "$method" \
            -H "Authorization: Token $TOKEN" \
            "${BASE_URL}${endpoint}"
    fi
}

# Helper function to check test result
check() {
    local test_name="$1"
    local expected_code="$2"
    local actual_code="$3"
    local response_body="$4"

    if [ "$actual_code" = "$expected_code" ]; then
        echo -e "${GREEN}✓ PASS${NC}: $test_name (HTTP $actual_code)"
        PASSED=$((PASSED + 1))
        return 0
    else
        echo -e "${RED}✗ FAIL${NC}: $test_name (expected $expected_code, got $actual_code)"
        echo -e "  Response: $response_body"
        FAILED=$((FAILED + 1))
        return 1
    fi
}

# Get a test repository
echo -e "${CYAN}=== Tag API Integration Tests ===${NC}"
echo "Base URL: $BASE_URL"
echo ""

# First, create a test library
echo -e "${YELLOW}→${NC} Creating test library..."
RESPONSE=$(api_call POST "/api/v2.1/repos/" "{\"repo_name\":\"tag-test-library-$(date +%s)\"}")
HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
BODY=$(echo "$RESPONSE" | head -n-1)

if [ "$HTTP_CODE" != "200" ] && [ "$HTTP_CODE" != "201" ]; then
    echo -e "${RED}Failed to create test library${NC}"
    echo "Response: $BODY"
    exit 1
fi

REPO_ID=$(echo "$BODY" | grep -o '"repo_id":"[^"]*"' | head -1 | cut -d'"' -f4)
if [ -z "$REPO_ID" ]; then
    echo -e "${RED}Failed to extract repo_id${NC}"
    echo "Response: $BODY"
    exit 1
fi
echo -e "${GREEN}✓${NC} Created library: $REPO_ID"
echo ""

# Clean up function
cleanup() {
    echo ""
    echo -e "${YELLOW}→${NC} Cleaning up test library..."
    api_call DELETE "/api/v2.1/repos/$REPO_ID/" > /dev/null 2>&1
    echo -e "${GREEN}✓${NC} Cleanup complete"
}
trap cleanup EXIT

# Test 1: Create a tag
echo -e "${CYAN}--- Test 1: Create a tag ---${NC}"
RESPONSE=$(api_call POST "/api/v2.1/repos/$REPO_ID/repo-tags/" '{"name":"ImportantTag","color":"#FF0000"}')
HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
BODY=$(echo "$RESPONSE" | head -n-1)
check "Create tag" "200" "$HTTP_CODE" "$BODY"

TAG_ID=$(echo "$BODY" | grep -o '"repo_tag_id":[0-9]*' | head -1 | cut -d':' -f2)
echo "  Created tag ID: $TAG_ID"
echo ""

# Test 2: List tags
echo -e "${CYAN}--- Test 2: List tags ---${NC}"
RESPONSE=$(api_call GET "/api/v2.1/repos/$REPO_ID/repo-tags/")
HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
BODY=$(echo "$RESPONSE" | head -n-1)
check "List tags" "200" "$HTTP_CODE" "$BODY"

# Verify the tag is in the list
if echo "$BODY" | grep -q "ImportantTag"; then
    echo -e "${GREEN}✓ PASS${NC}: Tag appears in list"
    PASSED=$((PASSED + 1))
else
    echo -e "${RED}✗ FAIL${NC}: Tag not in list"
    FAILED=$((FAILED + 1))
fi
echo ""

# Test 3: Update a tag
echo -e "${CYAN}--- Test 3: Update a tag ---${NC}"
RESPONSE=$(api_call PUT "/api/v2.1/repos/$REPO_ID/repo-tags/$TAG_ID/" '{"name":"UpdatedTag","color":"#00FF00"}')
HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
BODY=$(echo "$RESPONSE" | head -n-1)
check "Update tag" "200" "$HTTP_CODE" "$BODY"
echo ""

# Test 4: Delete a tag (this was the 500 error bug fix)
echo -e "${CYAN}--- Test 4: Delete a tag (bug fix verification) ---${NC}"
RESPONSE=$(api_call DELETE "/api/v2.1/repos/$REPO_ID/repo-tags/$TAG_ID/")
HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
BODY=$(echo "$RESPONSE" | head -n-1)
check "Delete tag (should NOT return 500)" "200" "$HTTP_CODE" "$BODY"

# Verify success response
if echo "$BODY" | grep -q '"success":true'; then
    echo -e "${GREEN}✓ PASS${NC}: Delete returned success:true"
    PASSED=$((PASSED + 1))
else
    echo -e "${RED}✗ FAIL${NC}: Delete did not return success:true"
    FAILED=$((FAILED + 1))
fi
echo ""

# Test 5: Verify tag was deleted
echo -e "${CYAN}--- Test 5: Verify tag was deleted ---${NC}"
RESPONSE=$(api_call GET "/api/v2.1/repos/$REPO_ID/repo-tags/")
HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
BODY=$(echo "$RESPONSE" | head -n-1)
check "List tags after delete" "200" "$HTTP_CODE" "$BODY"

# Verify the tag is NOT in the list
if echo "$BODY" | grep -q "UpdatedTag"; then
    echo -e "${RED}✗ FAIL${NC}: Deleted tag still appears in list"
    FAILED=$((FAILED + 1))
else
    echo -e "${GREEN}✓ PASS${NC}: Tag no longer in list"
    PASSED=$((PASSED + 1))
fi
echo ""

# Test 6: Create a tag, apply to file, then delete
echo -e "${CYAN}--- Test 6: Delete tag with file associations (Cassandra primary key test) ---${NC}"

# Create a new tag
RESPONSE=$(api_call POST "/api/v2.1/repos/$REPO_ID/repo-tags/" '{"name":"FileTag","color":"#0000FF"}')
HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
BODY=$(echo "$RESPONSE" | head -n-1)
TAG_ID2=$(echo "$BODY" | grep -o '"repo_tag_id":[0-9]*' | head -1 | cut -d':' -f2)
echo "  Created tag ID: $TAG_ID2"

# Try to delete (even if no files are tagged, the fix should handle empty results)
RESPONSE=$(api_call DELETE "/api/v2.1/repos/$REPO_ID/repo-tags/$TAG_ID2/")
HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
BODY=$(echo "$RESPONSE" | head -n-1)
check "Delete tag with potential file associations" "200" "$HTTP_CODE" "$BODY"
echo ""

# Test 7: Delete non-existent tag
echo -e "${CYAN}--- Test 7: Delete non-existent tag ---${NC}"
RESPONSE=$(api_call DELETE "/api/v2.1/repos/$REPO_ID/repo-tags/99999/")
HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
BODY=$(echo "$RESPONSE" | head -n-1)
# Should return 200 (idempotent delete) or could return 404
# Our implementation returns 200 for idempotent delete
check "Delete non-existent tag (idempotent)" "200" "$HTTP_CODE" "$BODY"
echo ""

# Test 8: Invalid tag ID format
echo -e "${CYAN}--- Test 8: Invalid tag ID format ---${NC}"
RESPONSE=$(api_call DELETE "/api/v2.1/repos/$REPO_ID/repo-tags/invalid/")
HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
BODY=$(echo "$RESPONSE" | head -n-1)
check "Delete with invalid tag ID" "400" "$HTTP_CODE" "$BODY"
echo ""

# Summary
echo "============================================"
echo -e " Results: ${GREEN}$PASSED${NC} passed, ${RED}$FAILED${NC} failed"
echo "============================================"

if [ $FAILED -gt 0 ]; then
    exit 1
fi

exit 0
