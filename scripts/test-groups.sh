#!/bin/bash
# =============================================================================
# User-Facing Groups Integration Tests for SesameFS
# =============================================================================
#
# Tests the user-facing group endpoints (NOT admin endpoints).
#
# Flow:
#   1. Create group as user
#   2. List groups (verify new group appears)
#   3. Get group details
#   4. Rename group
#   5. Add member to group
#   6. List group members
#   7. Share library to group, verify member can see it
#   8. Remove member from group
#   9. Delete group
#
# Usage:
#   ./scripts/test-groups.sh [options]
#
# Options:
#   --verbose     Show curl response bodies
#   --help        Show this help
#
# Requirements:
#   - Backend running at $API_URL (default: http://localhost:8082)
#   - Dev tokens configured for all roles (see config.yaml)
#   - curl, jq installed
#
# =============================================================================

set -e

# Configuration
API_URL="${API_URL:-http://localhost:8082}"

# Dev tokens (from config.yaml / configs/config.docker.yaml)
ADMIN_TOKEN="dev-token-admin"
USER_TOKEN="dev-token-user"

# Options
VERBOSE=false

for arg in "$@"; do
    case "$arg" in
        --verbose) VERBOSE=true ;;
        --help)
            echo "Usage: ./scripts/test-groups.sh [--verbose] [--help]"
            exit 0
            ;;
    esac
done

# Tracked resources for cleanup
GROUP_ID=""
TEST_REPO_ID=""

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
# Cleanup
# =============================================================================

cleanup() {
    echo ""
    echo -e "${BLUE}[INFO]${NC} Cleaning up test resources..."

    # Delete test library
    if [ -n "$TEST_REPO_ID" ]; then
        curl -s -o /dev/null -X DELETE \
            -H "Authorization: Token $ADMIN_TOKEN" \
            "${API_URL}/api/v2.1/repos/${TEST_REPO_ID}/" 2>/dev/null || true
        echo -e "${BLUE}[INFO]${NC} Deleted test library: $TEST_REPO_ID"
    fi

    # Delete test group
    if [ -n "$GROUP_ID" ]; then
        curl -s -o /dev/null -X DELETE \
            -H "Authorization: Token $ADMIN_TOKEN" \
            -H "Content-Type: application/json" \
            "${API_URL}/api/v2.1/groups/${GROUP_ID}/" 2>/dev/null || true
        echo -e "${BLUE}[INFO]${NC} Deleted test group: $GROUP_ID"
    fi
}
trap cleanup EXIT

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

# Returns HTTP status code only
api_status() {
    local method="$1"
    local endpoint="$2"
    local token="$3"
    local data="$4"

    local url="${API_URL}${endpoint}"
    local opts=(-s -o /dev/null -w "%{http_code}")

    if [ -n "$token" ]; then
        opts+=(-H "Authorization: Token $token")
    fi

    opts+=(-H "Content-Type: application/json")

    if [ -n "$data" ]; then
        opts+=(-d "$data")
    fi

    curl "${opts[@]}" -X "$method" "$url"
}

# Returns response body
api_body() {
    local method="$1"
    local endpoint="$2"
    local token="$3"
    local data="$4"

    local url="${API_URL}${endpoint}"
    local opts=(-s)

    if [ -n "$token" ]; then
        opts+=(-H "Authorization: Token $token")
    fi

    opts+=(-H "Content-Type: application/json")

    if [ -n "$data" ]; then
        opts+=(-d "$data")
    fi

    local body
    body=$(curl "${opts[@]}" -X "$method" "$url")

    if [ "$VERBOSE" = true ]; then
        echo -e "${YELLOW}[RESP]${NC} $method $endpoint" >&2
        echo "$body" | jq . 2>/dev/null >&2 || echo "$body" >&2
    fi

    echo "$body"
}

# Returns HTTP status code for form data requests
api_form_status() {
    local method="$1"
    local endpoint="$2"
    local token="$3"
    shift 3
    local url="${API_URL}${endpoint}"
    local opts=(-s -o /dev/null -w "%{http_code}")
    if [ -n "$token" ]; then
        opts+=(-H "Authorization: Token $token")
    fi
    for field in "$@"; do
        opts+=(-F "$field")
    done
    curl "${opts[@]}" -X "$method" "$url"
}

# Returns response body for form data requests
api_form_body() {
    local method="$1"
    local endpoint="$2"
    local token="$3"
    shift 3
    local url="${API_URL}${endpoint}"
    local opts=(-s)
    if [ -n "$token" ]; then
        opts+=(-H "Authorization: Token $token")
    fi
    for field in "$@"; do
        opts+=(-F "$field")
    done
    local body
    body=$(curl "${opts[@]}" -X "$method" "$url")
    if [ "$VERBOSE" = true ]; then
        echo -e "${YELLOW}[RESP]${NC} $method $endpoint" >&2
        echo "$body" | jq . 2>/dev/null >&2 || echo "$body" >&2
    fi
    echo "$body"
}

# =============================================================================
# Pre-flight
# =============================================================================

log_section "Pre-flight Checks"

STATUS=$(curl -s -o /dev/null -w "%{http_code}" "${API_URL}/api2/ping/")
run_test "Backend is reachable" "200" "$STATUS"
if [ "$STATUS" != "200" ]; then
    echo "Backend not reachable at $API_URL"
    exit 1
fi

# =============================================================================
# Test 1: Create Group
# =============================================================================

log_section "Group CRUD Operations"

TIMESTAMP=$(date +%s)
GROUP_NAME="TestGroup-${TIMESTAMP}"

# Create group
BODY=$(api_body "POST" "/api/v2.1/groups/" "$ADMIN_TOKEN" "{\"group_name\":\"${GROUP_NAME}\"}")
STATUS=$(api_status "POST" "/api/v2.1/groups/" "$ADMIN_TOKEN" "{\"group_name\":\"${GROUP_NAME}2\"}")
run_test "Create group returns 201" "201" "$STATUS"

GROUP_ID=$(echo "$BODY" | jq -r '.id // empty')
if [ -n "$GROUP_ID" ] && [ "$GROUP_ID" != "null" ]; then
    log_info "Created group: $GROUP_ID (name: $GROUP_NAME)"
    run_test "Create group returns valid group ID" "true" "true"
else
    log_fail "Failed to create group - cannot continue"
    run_test "Create group returns valid group ID" "true" "false"
    exit 1
fi

# Verify response contains expected fields
RESP_NAME=$(echo "$BODY" | jq -r '.name // empty')
run_test "Create group returns correct name" "$GROUP_NAME" "$RESP_NAME"

RESP_MEMBER_COUNT=$(echo "$BODY" | jq -r '.member_count // 0')
run_test "Create group returns member_count=1 (creator)" "1" "$RESP_MEMBER_COUNT"

# =============================================================================
# Test 2: List Groups
# =============================================================================

log_section "List Groups"

BODY=$(api_body "GET" "/api/v2.1/groups/" "$ADMIN_TOKEN")
STATUS=$(api_status "GET" "/api/v2.1/groups/" "$ADMIN_TOKEN")
run_test "List groups returns 200" "200" "$STATUS"

# Check that our group appears in the list
FOUND=$(echo "$BODY" | jq --arg gid "$GROUP_ID" '[.[] | select(.id == $gid)] | length')
run_test "List groups includes newly created group" "1" "$FOUND"

# =============================================================================
# Test 3: Get Group Details
# =============================================================================

log_section "Get Group Details"

BODY=$(api_body "GET" "/api/v2.1/groups/${GROUP_ID}/" "$ADMIN_TOKEN")
STATUS=$(api_status "GET" "/api/v2.1/groups/${GROUP_ID}/" "$ADMIN_TOKEN")
run_test "Get group details returns 200" "200" "$STATUS"

DETAIL_NAME=$(echo "$BODY" | jq -r '.name // empty')
run_test "Get group returns correct name" "$GROUP_NAME" "$DETAIL_NAME"

# =============================================================================
# Test 4: Rename Group
# =============================================================================

log_section "Rename Group"

NEW_GROUP_NAME="RenamedGroup-${TIMESTAMP}"
STATUS=$(api_status "PUT" "/api/v2.1/groups/${GROUP_ID}/" "$ADMIN_TOKEN" "{\"group_name\":\"${NEW_GROUP_NAME}\"}")
run_test "Rename group returns 200" "200" "$STATUS"

# Verify rename took effect
BODY=$(api_body "GET" "/api/v2.1/groups/${GROUP_ID}/" "$ADMIN_TOKEN")
RENAMED=$(echo "$BODY" | jq -r '.name // empty')
run_test "Group name updated after rename" "$NEW_GROUP_NAME" "$RENAMED"

# =============================================================================
# Test 5: Add Member
# =============================================================================

log_section "Group Members"

# Add user@sesamefs.local as a member
STATUS=$(api_form_status "POST" "/api/v2.1/groups/${GROUP_ID}/members/" "$ADMIN_TOKEN" \
    "email=user@sesamefs.local")
run_test "Add member to group returns 200" "200" "$STATUS"

# =============================================================================
# Test 6: List Members
# =============================================================================

BODY=$(api_body "GET" "/api/v2.1/groups/${GROUP_ID}/members/" "$ADMIN_TOKEN")
STATUS=$(api_status "GET" "/api/v2.1/groups/${GROUP_ID}/members/" "$ADMIN_TOKEN")
run_test "List group members returns 200" "200" "$STATUS"

MEMBER_COUNT=$(echo "$BODY" | jq '. | length')
run_test "Group has 2 members (owner + added member)" "2" "$MEMBER_COUNT"

# Verify the added member is in the list
HAS_USER=$(echo "$BODY" | jq '[.[] | select(.email == "user@sesamefs.local")] | length')
run_test "Member list includes user@sesamefs.local" "1" "$HAS_USER"

# =============================================================================
# Test 7: Share Library to Group
# =============================================================================

log_section "Share Library to Group"

# Create a test library
LIB_BODY=$(api_body "POST" "/api/v2.1/repos/" "$ADMIN_TOKEN" "{\"repo_name\":\"GroupShareTest-${TIMESTAMP}\"}")
TEST_REPO_ID=$(echo "$LIB_BODY" | jq -r '.repo_id // empty')

if [ -n "$TEST_REPO_ID" ] && [ "$TEST_REPO_ID" != "null" ]; then
    log_info "Created test library: $TEST_REPO_ID"

    # Share library to group
    SHARE_STATUS=$(api_form_status "PUT" "/api2/repos/${TEST_REPO_ID}/dir/shared_items/?p=/" "$ADMIN_TOKEN" \
        "share_type=group" \
        "group_id=${GROUP_ID}" \
        "permission=r")
    run_test "Share library to group returns 200" "200" "$SHARE_STATUS"

    # Verify group member can see it via beshared-repos
    BESHARED_BODY=$(api_body "GET" "/api2/beshared-repos/" "$USER_TOKEN")
    FOUND_SHARED=$(echo "$BESHARED_BODY" | jq --arg rid "$TEST_REPO_ID" '[.[] | select(.repo_id == $rid)] | length')
    run_test "Group member sees shared library via beshared-repos" "1" "$FOUND_SHARED"
else
    log_info "Could not create test library, skipping share tests"
    TOTAL_TESTS=$((TOTAL_TESTS + 2))
    FAILED_TESTS=$((FAILED_TESTS + 2))
    FAILED_TEST_NAMES+=("Share library to group")
    FAILED_TEST_NAMES+=("Group member sees shared library")
fi

# =============================================================================
# Test 8: Remove Member
# =============================================================================

log_section "Remove Member"

STATUS=$(api_status "DELETE" "/api/v2.1/groups/${GROUP_ID}/members/user@sesamefs.local/" "$ADMIN_TOKEN")
run_test "Remove member from group returns 200" "200" "$STATUS"

# Verify member removed
BODY=$(api_body "GET" "/api/v2.1/groups/${GROUP_ID}/members/" "$ADMIN_TOKEN")
MEMBER_COUNT=$(echo "$BODY" | jq '. | length')
run_test "Group has 1 member after removal (owner only)" "1" "$MEMBER_COUNT"

# =============================================================================
# Test 9: Delete Group
# =============================================================================

log_section "Delete Group"

STATUS=$(api_status "DELETE" "/api/v2.1/groups/${GROUP_ID}/" "$ADMIN_TOKEN")
run_test "Delete group returns 200" "200" "$STATUS"

# Verify group is gone from list
BODY=$(api_body "GET" "/api/v2.1/groups/" "$ADMIN_TOKEN")
FOUND=$(echo "$BODY" | jq --arg gid "$GROUP_ID" '[.[] | select(.id == $gid)] | length')
run_test "Deleted group no longer in list" "0" "$FOUND"

# Clear GROUP_ID so cleanup doesn't try to delete again
GROUP_ID=""

# =============================================================================
# Summary
# =============================================================================

log_section "Results"

echo ""
echo -e "Total:  ${TOTAL_TESTS}"
echo -e "Passed: ${GREEN}${PASSED_TESTS}${NC}"
echo -e "Failed: ${RED}${FAILED_TESTS}${NC}"
echo ""

if [ ${#FAILED_TEST_NAMES[@]} -gt 0 ]; then
    echo -e "${RED}Failed tests:${NC}"
    for name in "${FAILED_TEST_NAMES[@]}"; do
        echo -e "  ${RED}✗${NC} $name"
    done
fi

echo ""

[ "$FAILED_TESTS" -eq 0 ]
