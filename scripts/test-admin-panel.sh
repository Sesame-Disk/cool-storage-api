#!/bin/bash
# =============================================================================
# Admin Panel (Groups + Users by Email) Integration Tests for SesameFS
# =============================================================================
#
# Tests the admin panel endpoints for group management and email-based user management.
# These are distinct from the superadmin org/user management endpoints tested in test-admin-api.sh.
#
# Flow:
#   1. Admin Group Management (create, list, search, members, transfer, delete)
#   2. Admin User Management (email-based: list, search, get, create, update, deactivate)
#   3. Permission enforcement (non-admin gets 403)
#
# Usage:
#   ./scripts/test-admin-panel.sh [options]
#
# Options:
#   --quick       Skip cleanup (leave test data for inspection)
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

# Dev tokens (from config.yaml / config.docker.yaml)
SUPERADMIN_TOKEN="dev-token-superadmin"
ADMIN_TOKEN="dev-token-admin"
USER_TOKEN="dev-token-user"

# Platform org (all zeros)
PLATFORM_ORG_ID="00000000-0000-0000-0000-000000000000"
# Default org
DEFAULT_ORG_ID="00000000-0000-0000-0000-000000000001"

TIMESTAMP=$(date +%s)
GROUP_TEST_NAME="TestAdminGroup-${TIMESTAMP}"
GROUP_MEMBER_EMAIL="groupmember-${TIMESTAMP}@sesamefs.local"
TEST_USER_EMAIL="testpanel-${TIMESTAMP}@sesamefs.local"

# Will be set during test
GROUP_ID=""

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

cleanup() {
    if [ -n "$GROUP_ID" ]; then
        api_status "DELETE" "/api/v2.1/admin/groups/${GROUP_ID}/" "$SUPERADMIN_TOKEN" "" > /dev/null 2>&1 || true
        GROUP_ID=""
    fi

    if [ -n "$TEST_USER_EMAIL" ]; then
        api_status "DELETE" "/api/v2.1/admin/users/${TEST_USER_EMAIL}/" "$SUPERADMIN_TOKEN" "" > /dev/null 2>&1 || true
        TEST_USER_EMAIL=""
    fi

    if [ -n "$GROUP_MEMBER_EMAIL" ]; then
        api_status "DELETE" "/api/v2.1/admin/users/${GROUP_MEMBER_EMAIL}/" "$SUPERADMIN_TOKEN" "" > /dev/null 2>&1 || true
        GROUP_MEMBER_EMAIL=""
    fi
}

trap cleanup EXIT

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
# Test 1: Admin Group Management
# =============================================================================

log_section "Admin Group Management"

# Create a platform-org user so group membership and ownership transfer stay within the same org.
STATUS=$(api_status "POST" "/api/v2.1/admin/users/" "$SUPERADMIN_TOKEN" \
    "{\"email\":\"${GROUP_MEMBER_EMAIL}\",\"name\":\"Group Member\"}")
run_test "Create platform user for group tests returns 201" "201" "$STATUS"

# 1. List all groups (empty initially)
STATUS=$(api_status "GET" "/api/v2.1/admin/groups/" "$SUPERADMIN_TOKEN")
run_test "List all groups returns 200" "200" "$STATUS"

BODY=$(api_body "GET" "/api/v2.1/admin/groups/" "$SUPERADMIN_TOKEN")
HAS_GROUPS=$(echo "$BODY" | jq 'has("groups")')
run_test "List groups response has 'groups' array" "true" "$HAS_GROUPS"

# 2. Create group via admin API
BODY=$(api_body "POST" "/api/v2.1/admin/groups/" "$SUPERADMIN_TOKEN" \
    "{\"group_name\":\"${GROUP_TEST_NAME}\"}")
STATUS=$(api_status "POST" "/api/v2.1/admin/groups/" "$SUPERADMIN_TOKEN" \
    "{\"group_name\":\"${GROUP_TEST_NAME}\"}")
run_test "Create group via admin API returns 201" "201" "$STATUS"

GROUP_ID=$(echo "$BODY" | jq -r '.id // empty')
if [ -n "$GROUP_ID" ] && [ "$GROUP_ID" != "null" ]; then
    log_success "Created group: $GROUP_ID"
    run_test "Create group returns valid group ID" "true" "true"
else
    log_fail "Failed to create group"
    run_test "Create group returns valid group ID" "true" "false"
fi

# 3. List groups — verify new group appears
BODY=$(api_body "GET" "/api/v2.1/admin/groups/" "$SUPERADMIN_TOKEN")
GROUP_COUNT=$(echo "$BODY" | jq '.groups | length')
log_info "Found $GROUP_COUNT groups"
run_test "List groups shows at least 1 group" "true" "$([ "$GROUP_COUNT" -ge 1 ] && echo true || echo false)"

# 4. Search groups by name
STATUS=$(api_status "GET" "/api/v2.1/admin/search-group/?query=${GROUP_TEST_NAME}" "$SUPERADMIN_TOKEN")
run_test "Search groups by name returns 200" "200" "$STATUS"

BODY=$(api_body "GET" "/api/v2.1/admin/search-group/?query=${GROUP_TEST_NAME}" "$SUPERADMIN_TOKEN")
SEARCH_RESULTS=$(echo "$BODY" | jq '. | length')
log_info "Search found $SEARCH_RESULTS results"
run_test "Search finds at least 1 matching group" "true" "$([ "$SEARCH_RESULTS" -ge 1 ] && echo true || echo false)"

# 5. Add member to group
if [ -n "$GROUP_ID" ]; then
    STATUS=$(api_status "POST" "/api/v2.1/admin/groups/${GROUP_ID}/members/" "$SUPERADMIN_TOKEN" \
        "{\"emails\":[\"${GROUP_MEMBER_EMAIL}\"]}")
    run_test "Add member to group returns 200" "200" "$STATUS"

    # 6. List group members — verify member
    STATUS=$(api_status "GET" "/api/v2.1/admin/groups/${GROUP_ID}/members/" "$SUPERADMIN_TOKEN")
    run_test "List group members returns 200" "200" "$STATUS"

    BODY=$(api_body "GET" "/api/v2.1/admin/groups/${GROUP_ID}/members/" "$SUPERADMIN_TOKEN")
    MEMBER_COUNT=$(echo "$BODY" | jq '. | length')
    log_info "Group has $MEMBER_COUNT members"
    run_test "Group has at least 1 member" "true" "$([ "$MEMBER_COUNT" -ge 1 ] && echo true || echo false)"

    # 7. Remove member from group
    STATUS=$(api_status "DELETE" "/api/v2.1/admin/groups/${GROUP_ID}/members/${GROUP_MEMBER_EMAIL}/" "$SUPERADMIN_TOKEN")
    run_test "Remove member from group returns 200" "200" "$STATUS"

    # 8. Transfer group ownership
    STATUS=$(api_status "PUT" "/api/v2.1/admin/groups/${GROUP_ID}/" "$SUPERADMIN_TOKEN" \
        "{\"new_owner\":\"${GROUP_MEMBER_EMAIL}\"}")
    run_test "Transfer group ownership returns 200" "200" "$STATUS"

    # 9. List group libraries (empty)
    STATUS=$(api_status "GET" "/api/v2.1/admin/groups/${GROUP_ID}/libraries/" "$SUPERADMIN_TOKEN")
    run_test "List group libraries returns 200" "200" "$STATUS"

    BODY=$(api_body "GET" "/api/v2.1/admin/groups/${GROUP_ID}/libraries/" "$SUPERADMIN_TOKEN")
    LIB_COUNT=$(echo "$BODY" | jq '. | length')
    run_test "Group libraries list returns valid response" "true" "$([ "$LIB_COUNT" -ge 0 ] && echo true || echo false)"

    # 10. Delete group
    STATUS=$(api_status "DELETE" "/api/v2.1/admin/groups/${GROUP_ID}/" "$SUPERADMIN_TOKEN")
    run_test "Delete group returns 200" "200" "$STATUS"
fi

# 11. Permission: non-admin gets 403
STATUS=$(api_status "GET" "/api/v2.1/admin/groups/" "$USER_TOKEN")
run_test "Non-admin list groups returns 403" "403" "$STATUS"

# =============================================================================
# Test 2: Admin User Management (Email-Based)
# =============================================================================

log_section "Admin User Management (Email-Based)"

# 1. List all users
STATUS=$(api_status "GET" "/api/v2.1/admin/users/" "$SUPERADMIN_TOKEN")
run_test "List all users returns 200" "200" "$STATUS"

BODY=$(api_body "GET" "/api/v2.1/admin/users/" "$SUPERADMIN_TOKEN")
HAS_DATA=$(echo "$BODY" | jq 'has("data")')
HAS_TOTAL=$(echo "$BODY" | jq 'has("total_count")')
run_test "List users response has 'data' array" "true" "$HAS_DATA"
run_test "List users response has 'total_count'" "true" "$HAS_TOTAL"

USER_COUNT=$(echo "$BODY" | jq '.total_count')
log_info "Found $USER_COUNT users"

# 2. Search user by email
STATUS=$(api_status "GET" "/api/v2.1/admin/search-user/?query=admin" "$SUPERADMIN_TOKEN")
run_test "Search user by email returns 200" "200" "$STATUS"

BODY=$(api_body "GET" "/api/v2.1/admin/search-user/?query=admin" "$SUPERADMIN_TOKEN")
SEARCH_RESULTS=$(echo "$BODY" | jq '.users | length')
log_info "Search found $SEARCH_RESULTS results"

# 3. Get user by email
STATUS=$(api_status "GET" "/api/v2.1/admin/users/admin@sesamefs.local/" "$SUPERADMIN_TOKEN")
run_test "Get user by email returns 200" "200" "$STATUS"

BODY=$(api_body "GET" "/api/v2.1/admin/users/admin@sesamefs.local/" "$SUPERADMIN_TOKEN")
EMAIL=$(echo "$BODY" | jq -r '.email // empty')
run_test "Get user returns correct email" "admin@sesamefs.local" "$EMAIL"

# 4. Create user via admin (use unique email to avoid conflicts)
STATUS=$(api_status "POST" "/api/v2.1/admin/users/" "$SUPERADMIN_TOKEN" \
    "{\"email\":\"${TEST_USER_EMAIL}\",\"name\":\"Test Panel User\"}")
run_test "Create user via admin returns 201" "201" "$STATUS"

# 5. Update user role by email
STATUS=$(api_status "PUT" "/api/v2.1/admin/users/${TEST_USER_EMAIL}/" "$SUPERADMIN_TOKEN" \
    '{"role":"readonly"}')
run_test "Update user role by email returns 200" "200" "$STATUS"

# 6. Get updated user — verify role
BODY=$(api_body "GET" "/api/v2.1/admin/users/${TEST_USER_EMAIL}/" "$SUPERADMIN_TOKEN")
ROLE=$(echo "$BODY" | jq -r '.role // empty')
run_test "Get updated user shows role=readonly" "readonly" "$ROLE"

# 7. Deactivate user by email
STATUS=$(api_status "DELETE" "/api/v2.1/admin/users/${TEST_USER_EMAIL}/" "$SUPERADMIN_TOKEN")
run_test "Deactivate user by email returns 200" "200" "$STATUS"

# 8. List admin users
STATUS=$(api_status "GET" "/api/v2.1/admin/admins/" "$SUPERADMIN_TOKEN")
run_test "List admin users returns 200" "200" "$STATUS"

BODY=$(api_body "GET" "/api/v2.1/admin/admins/" "$SUPERADMIN_TOKEN")
ADMIN_COUNT=$(echo "$BODY" | jq '. | length')
log_info "Found $ADMIN_COUNT admin users"

# Cleanup the temporary platform user created for group tests.
STATUS=$(api_status "DELETE" "/api/v2.1/admin/users/${GROUP_MEMBER_EMAIL}/" "$SUPERADMIN_TOKEN")
run_test "Deactivate group test user returns 200" "200" "$STATUS"
GROUP_MEMBER_EMAIL=""

# 9. Permission: non-admin gets 403
STATUS=$(api_status "GET" "/api/v2.1/admin/users/" "$USER_TOKEN")
run_test "Non-admin list users returns 403" "403" "$STATUS"

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
