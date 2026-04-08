#!/bin/bash
# =============================================================================
# Repo API Token Authentication Integration Tests
# =============================================================================
#
# Tests that repo API tokens correctly authenticate requests and enforce:
#   - Library-scoped access (token can only access its designated library)
#   - Permission levels (read-only vs read-write)
#   - Token lifecycle (create, use, update permission, delete)
#   - Cross-library denial (token for lib A cannot access lib B)
#
# Usage:
#   ./scripts/test-repo-api-tokens.sh [options]
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
API_URL="${API_URL:-http://localhost:8082}"
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
CLEANUP_DONE=false

REPO_A=""
REPO_B=""

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

# Parse arguments
for arg in "$@"; do
    case "$arg" in
        --verbose) VERBOSE=true ;;
        --help)
            head -25 "$0" | grep "^#" | sed 's/^# //' | sed 's/^#//'
            exit 0
            ;;
    esac
done

# API helpers
api_status() {
    local method="$1" endpoint="$2" token="$3" data="$4"
    local url="${API_URL}${endpoint}"
    local opts=(-s -o /dev/null -w "%{http_code}")
    if [ -n "$token" ]; then opts+=(-H "Authorization: Token $token"); fi
    opts+=(-H "Content-Type: application/json")
    if [ -n "$data" ]; then opts+=(-d "$data"); fi
    curl "${opts[@]}" -X "$method" "$url"
}

api_body() {
    local method="$1" endpoint="$2" token="$3" data="$4"
    local url="${API_URL}${endpoint}"
    local opts=(-s)
    if [ -n "$token" ]; then opts+=(-H "Authorization: Token $token"); fi
    opts+=(-H "Content-Type: application/json")
    if [ -n "$data" ]; then opts+=(-d "$data"); fi
    curl "${opts[@]}" -X "$method" "$url"
}

run_test() {
    local description="$1" expected="$2" actual="$3"
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    if [ "$expected" = "$actual" ]; then
        PASSED_TESTS=$((PASSED_TESTS + 1))
        log_success "$description (expected: $expected, got: $actual)"
    else
        FAILED_TESTS=$((FAILED_TESTS + 1))
        FAILED_TEST_NAMES+=("$description")
        log_fail "$description (expected: $expected, got: $actual)"
    fi
}

run_test_contains() {
    local description="$1" expected_substr="$2" actual="$3"
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    if echo "$actual" | grep -q "$expected_substr"; then
        PASSED_TESTS=$((PASSED_TESTS + 1))
        log_success "$description (contains '$expected_substr')"
    else
        FAILED_TESTS=$((FAILED_TESTS + 1))
        FAILED_TEST_NAMES+=("$description")
        log_fail "$description (missing '$expected_substr' in: ${actual:0:120})"
    fi
}

run_test_not_contains() {
    local description="$1" unexpected_substr="$2" actual="$3"
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    if ! echo "$actual" | grep -q "$unexpected_substr"; then
        PASSED_TESTS=$((PASSED_TESTS + 1))
        log_success "$description (correctly missing '$unexpected_substr')"
    else
        FAILED_TESTS=$((FAILED_TESTS + 1))
        FAILED_TEST_NAMES+=("$description")
        log_fail "$description (unexpectedly found '$unexpected_substr')"
    fi
}

cleanup() {
    if [ "$CLEANUP_DONE" = true ]; then
        return
    fi

    if [ -n "$REPO_A" ]; then
        api_status "DELETE" "/api/v2.1/repos/${REPO_A}/" "$ADMIN_TOKEN" "" > /dev/null 2>&1 || true
        log_info "Deleted test library A: $REPO_A"
        REPO_A=""
    fi

    if [ -n "$REPO_B" ]; then
        api_status "DELETE" "/api/v2.1/repos/${REPO_B}/" "$ADMIN_TOKEN" "" > /dev/null 2>&1 || true
        log_info "Deleted test library B: $REPO_B"
        REPO_B=""
    fi

    CLEANUP_DONE=true
}

trap cleanup EXIT

# =============================================================================
# Pre-flight
# =============================================================================

echo -e "${CYAN}SesameFS Repo API Token Authentication Tests${NC}"
echo "API URL: $API_URL"
echo ""

log_section "Pre-flight Checks"

# Check API is reachable
if curl -s -f "$API_URL/health" > /dev/null 2>&1; then
    log_success "API is reachable at $API_URL"
else
    log_fail "API not reachable at $API_URL"
    exit 1
fi

# Check admin token
STATUS=$(api_status "GET" "/api2/account/info/" "$ADMIN_TOKEN")
run_test "Admin token is valid" "200" "$STATUS"

# =============================================================================
# Setup: Create two test libraries
# =============================================================================

log_section "Setup: Create test libraries"

LIB_A=$(api_body "POST" "/api/v2.1/repos/" "$ADMIN_TOKEN" '{"name":"api-token-test-A"}')
REPO_A=$(echo "$LIB_A" | jq -r '.repo_id')
log_info "Created library A: $REPO_A"

LIB_B=$(api_body "POST" "/api/v2.1/repos/" "$ADMIN_TOKEN" '{"name":"api-token-test-B"}')
REPO_B=$(echo "$LIB_B" | jq -r '.repo_id')
log_info "Created library B: $REPO_B"

# Create a file in each library for testing
api_status "POST" "/api/v2.1/repos/${REPO_A}/file/?p=/file-a.md&operation=create" "$ADMIN_TOKEN" > /dev/null
api_status "POST" "/api/v2.1/repos/${REPO_B}/file/?p=/file-b.md&operation=create" "$ADMIN_TOKEN" > /dev/null
log_info "Created test files in both libraries"

# =============================================================================
# 1. Create API tokens
# =============================================================================

log_section "1. Create API tokens"

# Create RW token for library A
RW_RESP=$(api_body "POST" "/api/v2.1/repos/${REPO_A}/repo-api-tokens/" "$ADMIN_TOKEN" \
    '{"app_name":"rw-test-app","permission":"rw"}')
RW_TOKEN=$(echo "$RW_RESP" | jq -r '.api_token')
run_test "Create RW token returns token" "64" "${#RW_TOKEN}"
run_test_contains "RW token has correct permission" '"permission":"rw"' "$RW_RESP"
run_test_contains "RW token has app_name" '"app_name":"rw-test-app"' "$RW_RESP"

# Create RO token for library A
RO_RESP=$(api_body "POST" "/api/v2.1/repos/${REPO_A}/repo-api-tokens/" "$ADMIN_TOKEN" \
    '{"app_name":"ro-test-app","permission":"r"}')
RO_TOKEN=$(echo "$RO_RESP" | jq -r '.api_token')
run_test "Create RO token returns token" "64" "${#RO_TOKEN}"
run_test_contains "RO token has correct permission" '"permission":"r"' "$RO_RESP"

# Create RW token for library B
RW_B_RESP=$(api_body "POST" "/api/v2.1/repos/${REPO_B}/repo-api-tokens/" "$ADMIN_TOKEN" \
    '{"app_name":"rw-test-b","permission":"rw"}')
RW_B_TOKEN=$(echo "$RW_B_RESP" | jq -r '.api_token')
run_test "Create token for library B" "64" "${#RW_B_TOKEN}"

# Duplicate app_name should fail
DUP_STATUS=$(api_status "POST" "/api/v2.1/repos/${REPO_A}/repo-api-tokens/" "$ADMIN_TOKEN" \
    '{"app_name":"rw-test-app","permission":"r"}')
run_test "Duplicate app_name returns 409" "409" "$DUP_STATUS"

# =============================================================================
# 2. RW token — read access to its library
# =============================================================================

log_section "2. RW token — read access to its library"

STATUS=$(api_status "GET" "/api/v2.1/repos/${REPO_A}/dir/?p=/" "$RW_TOKEN")
run_test "RW token can list directory" "200" "$STATUS"

BODY=$(api_body "GET" "/api/v2.1/repos/${REPO_A}/dir/?p=/" "$RW_TOKEN")
run_test_contains "Directory listing has file" '"name":"file-a.md"' "$BODY"

STATUS=$(api_status "GET" "/api/v2.1/repos/${REPO_A}/" "$RW_TOKEN")
run_test "RW token can get library info" "200" "$STATUS"

BODY=$(api_body "GET" "/api/v2.1/repos/${REPO_A}/" "$RW_TOKEN")
run_test_contains "Library info has repo_name" '"repo_name":"api-token-test-A"' "$BODY"

# =============================================================================
# 3. RW token — write access to its library
# =============================================================================

log_section "3. RW token — write access to its library"

STATUS=$(api_status "POST" "/api/v2.1/repos/${REPO_A}/file/?p=/created-by-rw-token.md&operation=create" "$RW_TOKEN")
run_test "RW token can create file" "201" "$STATUS"

BODY=$(api_body "GET" "/api/v2.1/repos/${REPO_A}/dir/?p=/" "$RW_TOKEN")
run_test_contains "Created file is visible" '"name":"created-by-rw-token.md"' "$BODY"

# Create a directory
STATUS=$(api_status "POST" "/api/v2.1/repos/${REPO_A}/dir/?p=/rw-folder" "$RW_TOKEN" '{}')
run_test "RW token can create directory" "201" "$STATUS"

# =============================================================================
# 4. RO token — read access to its library
# =============================================================================

log_section "4. RO token — read access to its library"

STATUS=$(api_status "GET" "/api/v2.1/repos/${REPO_A}/dir/?p=/" "$RO_TOKEN")
run_test "RO token can list directory" "200" "$STATUS"

STATUS=$(api_status "GET" "/api/v2.1/repos/${REPO_A}/" "$RO_TOKEN")
run_test "RO token can get library info" "200" "$STATUS"

# =============================================================================
# 5. RO token — write denied
# =============================================================================

log_section "5. RO token — write operations denied"

STATUS=$(api_status "POST" "/api/v2.1/repos/${REPO_A}/file/?p=/ro-should-fail.md&operation=create" "$RO_TOKEN")
run_test "RO token cannot create file" "403" "$STATUS"

STATUS=$(api_status "POST" "/api/v2.1/repos/${REPO_A}/dir/?p=/ro-should-fail-dir" "$RO_TOKEN" '{}')
run_test "RO token cannot create directory" "403" "$STATUS"

# Verify file was NOT created
BODY=$(api_body "GET" "/api/v2.1/repos/${REPO_A}/dir/?p=/" "$RO_TOKEN")
run_test_not_contains "Denied file was not created" '"name":"ro-should-fail.md"' "$BODY"

# =============================================================================
# 6. Cross-library denial
# =============================================================================

log_section "6. Cross-library access denied"

# Token for library A should not access library B
STATUS=$(api_status "GET" "/api/v2.1/repos/${REPO_B}/dir/?p=/" "$RW_TOKEN")
run_test "RW token for A cannot list B" "403" "$STATUS"

STATUS=$(api_status "GET" "/api/v2.1/repos/${REPO_B}/" "$RW_TOKEN")
run_test "RW token for A cannot get info of B" "403" "$STATUS"

STATUS=$(api_status "POST" "/api/v2.1/repos/${REPO_B}/file/?p=/cross-lib.md&operation=create" "$RW_TOKEN")
run_test "RW token for A cannot create file in B" "403" "$STATUS"

# Token for library B should not access library A
STATUS=$(api_status "GET" "/api/v2.1/repos/${REPO_A}/dir/?p=/" "$RW_B_TOKEN")
run_test "RW token for B cannot list A" "403" "$STATUS"

# =============================================================================
# 7. Invalid / missing tokens
# =============================================================================

log_section "7. Invalid and missing tokens"

# Completely bogus token
STATUS=$(api_status "GET" "/api/v2.1/repos/${REPO_A}/dir/?p=/" "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
run_test "Bogus token cannot access library" "401" "$STATUS"

# Empty token
STATUS=$(curl -s -o /dev/null -w "%{http_code}" -H "Authorization: Token " "${API_URL}/api/v2.1/repos/${REPO_A}/dir/?p=/")
run_test "Empty token cannot access library" "401" "$STATUS"

# =============================================================================
# 8. Token permission update
# =============================================================================

log_section "8. Update token permission"

# Update RO token to RW
STATUS=$(api_status "PUT" "/api/v2.1/repos/${REPO_A}/repo-api-tokens/ro-test-app" "$ADMIN_TOKEN" \
    '{"permission":"rw"}')
run_test "Update RO→RW returns 200" "200" "$STATUS"

# Now the formerly-RO token should be able to write
STATUS=$(api_status "POST" "/api/v2.1/repos/${REPO_A}/file/?p=/upgraded-token.md&operation=create" "$RO_TOKEN")
run_test "Upgraded token can create file" "201" "$STATUS"

# Revert back to read-only
api_status "PUT" "/api/v2.1/repos/${REPO_A}/repo-api-tokens/ro-test-app" "$ADMIN_TOKEN" \
    '{"permission":"r"}' > /dev/null

# Verify it's read-only again
STATUS=$(api_status "POST" "/api/v2.1/repos/${REPO_A}/file/?p=/should-fail-again.md&operation=create" "$RO_TOKEN")
run_test "Reverted token cannot create file" "403" "$STATUS"

# =============================================================================
# 9. List tokens
# =============================================================================

log_section "9. List and manage tokens"

BODY=$(api_body "GET" "/api/v2.1/repos/${REPO_A}/repo-api-tokens/" "$ADMIN_TOKEN")
run_test_contains "List tokens has rw-test-app" '"app_name":"rw-test-app"' "$BODY"
run_test_contains "List tokens has ro-test-app" '"app_name":"ro-test-app"' "$BODY"

# =============================================================================
# 10. Delete token and verify it stops working
# =============================================================================

log_section "10. Delete token revokes access"

# Verify RW token works before deletion
STATUS=$(api_status "GET" "/api/v2.1/repos/${REPO_A}/dir/?p=/" "$RW_TOKEN")
run_test "RW token works before deletion" "200" "$STATUS"

# Delete the RW token
STATUS=$(api_status "DELETE" "/api/v2.1/repos/${REPO_A}/repo-api-tokens/rw-test-app" "$ADMIN_TOKEN")
run_test "Delete token returns 200" "200" "$STATUS"

# Verify deleted token no longer works
STATUS=$(api_status "GET" "/api/v2.1/repos/${REPO_A}/dir/?p=/" "$RW_TOKEN")
run_test "Deleted token cannot access library" "401" "$STATUS"

# Verify RO token still works
STATUS=$(api_status "GET" "/api/v2.1/repos/${REPO_A}/dir/?p=/" "$RO_TOKEN")
run_test "Other token still works after sibling deletion" "200" "$STATUS"

# =============================================================================
# 11. Non-owner cannot manage tokens
# =============================================================================

log_section "11. Permission enforcement on token management"

STATUS=$(api_status "GET" "/api/v2.1/repos/${REPO_A}/repo-api-tokens/" "dev-token-user")
run_test "Non-owner cannot list tokens" "403" "$STATUS"

STATUS=$(api_status "POST" "/api/v2.1/repos/${REPO_A}/repo-api-tokens/" "dev-token-user" \
    '{"app_name":"sneaky","permission":"rw"}')
run_test "Non-owner cannot create tokens" "403" "$STATUS"

# =============================================================================
# Cleanup
# =============================================================================

log_section "Cleanup"
cleanup

# =============================================================================
# Summary
# =============================================================================

log_section "Repo API Token Test Summary"

echo ""
echo "  Total:  $TOTAL_TESTS"
echo -e "  ${GREEN}Passed: $PASSED_TESTS${NC}"
echo -e "  ${RED}Failed: $FAILED_TESTS${NC}"
echo ""

if [ $FAILED_TESTS -gt 0 ]; then
    echo -e "  ${RED}Failed tests:${NC}"
    for name in "${FAILED_TEST_NAMES[@]}"; do
        echo "    - $name"
    done
    echo ""
    exit 1
fi

echo -e "  ${GREEN}All tests passed!${NC}"
