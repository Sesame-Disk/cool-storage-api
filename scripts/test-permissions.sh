#!/bin/bash
# Comprehensive Permission Test Script for SesameFS
# Tests all 4 user roles: admin, user, readonly, guest
# Verifies API permissions are correctly enforced

set -e

# Configuration
API_URL="${API_URL:-http://localhost:8082}"
SUPERADMIN_TOKEN="dev-token-superadmin"
ADMIN_TOKEN="dev-token-admin"
USER_TOKEN="dev-token-user"
READONLY_TOKEN="dev-token-readonly"
GUEST_TOKEN="dev-token-guest"
DEFAULT_ORG_ID="00000000-0000-0000-0000-000000000001"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Counters
TOTAL_TESTS=0
PASSED_TESTS=0
FAILED_TESTS=0

# Test result tracking
declare -a FAILED_TEST_NAMES
CLEANUP_DONE=false
ORG_UPGRADE_ACTIVE=false
ORG_ORIGINAL_PAYLOAD=""

log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[PASS]${NC} $1"
}

log_fail() {
    echo -e "${RED}[FAIL]${NC} $1"
}

log_section() {
    echo ""
    echo -e "${YELLOW}========================================${NC}"
    echo -e "${YELLOW}$1${NC}"
    echo -e "${YELLOW}========================================${NC}"
}

# Test function
# Usage: run_test "test_name" expected_status actual_status
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

# API call helper
# Usage: api_call "METHOD" "endpoint" "token" [data]
api_call() {
    local method="$1"
    local endpoint="$2"
    local token="$3"
    local data="$4"

    local url="${API_URL}${endpoint}"
    local opts=(-s -o /dev/null -w "%{http_code}")

    if [ -n "$token" ]; then
        opts+=(-H "Authorization: Token $token")
    fi

    if [ "$method" == "POST" ] || [ "$method" == "PUT" ]; then
        opts+=(-H "Content-Type: application/json")
        if [ -n "$data" ]; then
            opts+=(-d "$data")
        fi
    fi

    curl "${opts[@]}" -X "$method" "$url"
}

# Get response body helper
api_get_body() {
    local endpoint="$1"
    local token="$2"

    local url="${API_URL}${endpoint}"
    curl -s -H "Authorization: Token $token" "$url"
}

api_json() {
    local method="$1"
    local endpoint="$2"
    local token="$3"
    local data="$4"

    local url="${API_URL}${endpoint}"
    local opts=(-s -H "Content-Type: application/json")

    if [ -n "$token" ]; then
        opts+=(-H "Authorization: Token $token")
    fi

    if [ -n "$data" ]; then
        opts+=(-d "$data")
    fi

    curl "${opts[@]}" -X "$method" "$url"
}

# Cleanup test libraries created by this test run
cleanup_test_libraries() {
    log_info "Cleaning up test libraries..."

    # Get all libraries for admin and user, delete any starting with "test-"
    for token in "$ADMIN_TOKEN" "$USER_TOKEN"; do
        local libs=$(api_get_body "/api/v2.1/repos/?type=mine" "$token")
        local lib_ids=$(echo "$libs" | jq -r '.repos[] | select(.repo_name | startswith("test-")) | .repo_id')

        for lib_id in $lib_ids; do
            api_call "DELETE" "/api/v2.1/repos/${lib_id}/" "$token" > /dev/null 2>&1
        done
    done
}

upgrade_default_org_for_permission_tests() {
    log_info "Upgrading default org for permission tests"

    local org_body
    org_body=$(api_get_body "/api/v2.1/admin/organizations/${DEFAULT_ORG_ID}/" "$SUPERADMIN_TOKEN")

    ORG_ORIGINAL_PAYLOAD=$(echo "$org_body" | jq -c '{
        storage_quota: .storage_quota,
        traffic_quota: .traffic_quota,
        traffic_upload_quota: .traffic_upload_quota,
        traffic_download_quota: .traffic_download_quota,
        max_users: .max_users,
        plan: .plan,
        quota_policy: .quota_policy,
        billing_cycle: .billing_cycle,
        current_period_started_at: .current_period_started_at,
        current_period_ends_at: .current_period_ends_at
    }')

    local upgrade_payload='{"storage_quota":-1,"traffic_quota":-1,"traffic_upload_quota":-1,"traffic_download_quota":-1,"max_users":50,"plan":"test-soft-upgraded","quota_policy":"soft","billing_cycle":"monthly"}'
    local status
    status=$(api_call "PUT" "/api/v2.1/admin/organizations/${DEFAULT_ORG_ID}/" "$SUPERADMIN_TOKEN" "$upgrade_payload")
    run_test "Default org upgrade returns 200" "200" "$status"

    if [ "$status" = "200" ]; then
        ORG_UPGRADE_ACTIVE=true
    fi
}

restore_default_org_after_permission_tests() {
    if [ "$ORG_UPGRADE_ACTIVE" != true ] || [ -z "$ORG_ORIGINAL_PAYLOAD" ]; then
        return
    fi

    log_info "Restoring default org after permission tests"
    api_json "PUT" "/api/v2.1/admin/organizations/${DEFAULT_ORG_ID}/" "$SUPERADMIN_TOKEN" "$ORG_ORIGINAL_PAYLOAD" > /dev/null 2>&1 || true
    ORG_UPGRADE_ACTIVE=false
}

cleanup() {
    if [ "$CLEANUP_DONE" = true ]; then
        return
    fi

    log_section "Cleanup"
    restore_default_org_after_permission_tests
    cleanup_test_libraries
    CLEANUP_DONE=true
}

trap cleanup EXIT

# ============================================
# Account Info Tests
# ============================================
test_account_info() {
    log_section "Testing Account Info API"

    # Test superadmin account info
    local sa_info=$(api_get_body "/api2/account/info/" "$SUPERADMIN_TOKEN")
    local sa_can_add=$(echo "$sa_info" | jq -r '.can_add_repo')
    local sa_role=$(echo "$sa_info" | jq -r '.role')
    local sa_is_staff=$(echo "$sa_info" | jq -r '.is_staff')

    run_test "Superadmin: can_add_repo should be true" "true" "$sa_can_add"
    run_test "Superadmin: role should be superadmin" "superadmin" "$sa_role"
    run_test "Superadmin: is_staff should be true" "true" "$sa_is_staff"

    # Test admin account info
    local admin_info=$(api_get_body "/api2/account/info/" "$ADMIN_TOKEN")
    local admin_can_add=$(echo "$admin_info" | jq -r '.can_add_repo')
    local admin_role=$(echo "$admin_info" | jq -r '.role')
    local admin_name=$(echo "$admin_info" | jq -r '.name')

    run_test "Admin: can_add_repo should be true" "true" "$admin_can_add"
    run_test "Admin: role should be admin" "admin" "$admin_role"
    run_test "Admin: name should match seeded name" "Admin User" "$admin_name"

    # Test user account info
    local user_info=$(api_get_body "/api2/account/info/" "$USER_TOKEN")
    local user_can_add=$(echo "$user_info" | jq -r '.can_add_repo')
    local user_role=$(echo "$user_info" | jq -r '.role')

    run_test "User: can_add_repo should be true" "true" "$user_can_add"
    run_test "User: role should be user" "user" "$user_role"

    # Test readonly account info
    local readonly_info=$(api_get_body "/api2/account/info/" "$READONLY_TOKEN")
    local readonly_can_add=$(echo "$readonly_info" | jq -r '.can_add_repo')
    local readonly_role=$(echo "$readonly_info" | jq -r '.role')
    local readonly_name=$(echo "$readonly_info" | jq -r '.name')

    run_test "Readonly: can_add_repo should be false" "false" "$readonly_can_add"
    run_test "Readonly: role should be readonly" "readonly" "$readonly_role"
    run_test "Readonly: name should be Read-Only User" "Read-Only User" "$readonly_name"

    # Test guest account info
    local guest_info=$(api_get_body "/api2/account/info/" "$GUEST_TOKEN")
    local guest_can_add=$(echo "$guest_info" | jq -r '.can_add_repo')
    local guest_role=$(echo "$guest_info" | jq -r '.role')

    run_test "Guest: can_add_repo should be false" "false" "$guest_can_add"
    run_test "Guest: role should be guest" "guest" "$guest_role"
}

# ============================================
# Library Creation Tests
# ============================================
test_library_creation() {
    log_section "Testing Library Creation Permissions"

    # Generate unique names using timestamp
    local timestamp=$(date +%s)

    # Superadmin should be able to create libraries
    local sa_status=$(api_call "POST" "/api/v2.1/repos/" "$SUPERADMIN_TOKEN" "{\"repo_name\":\"test-sa-lib-${timestamp}\"}")
    run_test "Superadmin: create library should succeed (200)" "200" "$sa_status"

    # Admin should be able to create libraries
    local admin_resp=$(curl -s -H "Authorization: Token $ADMIN_TOKEN" \
        -H "Content-Type: application/json" \
        -d "{\"repo_name\":\"test-admin-lib-${timestamp}\"}" \
        "${API_URL}/api/v2.1/repos/")
    local admin_lib_id=$(echo "$admin_resp" | jq -r '.repo_id // empty')
    local status=$(api_call "POST" "/api/v2.1/repos/" "$ADMIN_TOKEN" "{\"repo_name\":\"test-admin-lib2-${timestamp}\"}")
    run_test "Admin: create library should succeed (200)" "200" "$status"

    # User should be able to create libraries
    local user_resp=$(curl -s -H "Authorization: Token $USER_TOKEN" \
        -H "Content-Type: application/json" \
        -d "{\"repo_name\":\"test-user-lib-${timestamp}\"}" \
        "${API_URL}/api/v2.1/repos/")
    local user_lib_id=$(echo "$user_resp" | jq -r '.repo_id // empty')
    status=$(api_call "POST" "/api/v2.1/repos/" "$USER_TOKEN" "{\"repo_name\":\"test-user-lib2-${timestamp}\"}")
    run_test "User: create library should succeed (200)" "200" "$status"

    # Readonly should NOT be able to create libraries
    status=$(api_call "POST" "/api/v2.1/repos/" "$READONLY_TOKEN" "{\"repo_name\":\"test-readonly-lib-${timestamp}\"}")
    run_test "Readonly: create library should fail (403)" "403" "$status"

    # Guest should NOT be able to create libraries
    status=$(api_call "POST" "/api/v2.1/repos/" "$GUEST_TOKEN" "{\"repo_name\":\"test-guest-lib-${timestamp}\"}")
    run_test "Guest: create library should fail (403)" "403" "$status"

    # Cleanup: delete created libraries
    if [ -n "$admin_lib_id" ] && [ "$admin_lib_id" != "null" ]; then
        api_call "DELETE" "/api/v2.1/repos/${admin_lib_id}/" "$ADMIN_TOKEN" > /dev/null 2>&1
        log_info "Cleaned up admin test library"
    fi
    if [ -n "$user_lib_id" ] && [ "$user_lib_id" != "null" ]; then
        api_call "DELETE" "/api/v2.1/repos/${user_lib_id}/" "$USER_TOKEN" > /dev/null 2>&1
        log_info "Cleaned up user test library"
    fi

    # Also cleanup the duplicate test libraries
    cleanup_test_libraries
}

# ============================================
# Library Access Tests
# ============================================
test_library_access() {
    log_section "Testing Library Access Permissions"

    # Get admin's libraries
    local admin_libs=$(api_get_body "/api/v2.1/repos/?type=mine" "$ADMIN_TOKEN")
    local admin_lib_count=$(echo "$admin_libs" | jq '.repos | length')
    log_info "Admin has $admin_lib_count libraries"

    # Get first admin library ID if exists
    local admin_lib_id=$(echo "$admin_libs" | jq -r '.repos[0].repo_id // empty')

    if [ -n "$admin_lib_id" ]; then
        # User should NOT see admin's private libraries
        local user_can_access=$(api_call "GET" "/api/v2.1/repos/${admin_lib_id}/" "$USER_TOKEN")
        run_test "User: access admin's library should fail (403)" "403" "$user_can_access"

        # Readonly should NOT see admin's private libraries
        local readonly_can_access=$(api_call "GET" "/api/v2.1/repos/${admin_lib_id}/" "$READONLY_TOKEN")
        run_test "Readonly: access admin's library should fail (403)" "403" "$readonly_can_access"

        # Guest should NOT see admin's private libraries
        local guest_can_access=$(api_call "GET" "/api/v2.1/repos/${admin_lib_id}/" "$GUEST_TOKEN")
        run_test "Guest: access admin's library should fail (403)" "403" "$guest_can_access"
    else
        log_info "No admin libraries found, skipping access tests"
    fi
}

# ============================================
# User Isolation Tests
# ============================================
test_user_isolation() {
    log_section "Testing User Isolation"

    # Get user's own libraries
    local user_libs=$(api_get_body "/api/v2.1/repos/?type=mine" "$USER_TOKEN")
    local user_lib_ids=$(echo "$user_libs" | jq -r '.repos[].repo_id')

    # Get readonly's view of libraries
    local readonly_libs=$(api_get_body "/api/v2.1/repos/?type=mine" "$READONLY_TOKEN")
    local readonly_lib_count=$(echo "$readonly_libs" | jq '.repos | length')

    # Readonly user should have 0 libraries (they can't create any)
    run_test "Readonly: should have 0 own libraries" "0" "$readonly_lib_count"

    # Guest should have 0 libraries
    local guest_libs=$(api_get_body "/api/v2.1/repos/?type=mine" "$GUEST_TOKEN")
    local guest_lib_count=$(echo "$guest_libs" | jq '.repos | length')
    run_test "Guest: should have 0 own libraries" "0" "$guest_lib_count"
}

# ============================================
# Encrypted Library Tests
# ============================================
test_encrypted_libraries() {
    log_section "Testing Encrypted Library Access"

    # Create an encrypted library as admin
    local create_resp=$(curl -s -H "Authorization: Token $ADMIN_TOKEN" \
        -H "Content-Type: application/json" \
        -d '{"repo_name":"test-encrypted","encrypted":true,"passwd":"testpass123"}' \
        "${API_URL}/api/v2.1/repos/")

    local enc_lib_id=$(echo "$create_resp" | jq -r '.repo_id // empty')

    if [ -n "$enc_lib_id" ] && [ "$enc_lib_id" != "null" ]; then
        log_info "Created encrypted library: $enc_lib_id"

        # Without decrypt session, directory listing should fail
        local status=$(api_call "GET" "/api/v2.1/repos/${enc_lib_id}/dir/?p=/" "$ADMIN_TOKEN")
        run_test "Encrypted library: dir listing without password should fail (403)" "403" "$status"

        # Try to share encrypted library (should fail)
        status=$(api_call "POST" "/api/v2.1/repos/${enc_lib_id}/dir/shared_items/" "$ADMIN_TOKEN" \
            '{"share_type":"user","permission":"r","username":"user@sesamefs.local"}')
        run_test "Encrypted library: sharing should fail (400 or 403)" "true" "$([ "$status" == "400" ] || [ "$status" == "403" ] && echo true || echo false)"

        # Cleanup: delete the test library
        api_call "DELETE" "/api/v2.1/repos/${enc_lib_id}/" "$ADMIN_TOKEN" > /dev/null
    else
        log_info "Could not create encrypted library for testing"
    fi
}

# ============================================
# Write Operation Tests (for readonly/guest)
# ============================================
test_write_operations() {
    log_section "Testing Write Operation Restrictions"

    # First, create a library as admin
    local create_resp=$(curl -s -H "Authorization: Token $ADMIN_TOKEN" \
        -H "Content-Type: application/json" \
        -d '{"repo_name":"test-write-ops"}' \
        "${API_URL}/api/v2.1/repos/")

    local lib_id=$(echo "$create_resp" | jq -r '.repo_id // empty')

    if [ -n "$lib_id" ] && [ "$lib_id" != "null" ]; then
        log_info "Created test library: $lib_id"

        # Readonly user trying to create folder (should fail with 403)
        local status=$(api_call "POST" "/api/v2.1/repos/${lib_id}/dir/?p=/testfolder" "$READONLY_TOKEN" '{"operation":"mkdir"}')
        run_test "Readonly: create folder should fail (403)" "403" "$status"

        # Guest user trying to create folder (should fail with 403)
        status=$(api_call "POST" "/api/v2.1/repos/${lib_id}/dir/?p=/testfolder" "$GUEST_TOKEN" '{"operation":"mkdir"}')
        run_test "Guest: create folder should fail (403)" "403" "$status"

        # Admin can create folder
        status=$(api_call "POST" "/api/v2.1/repos/${lib_id}/dir/?p=/adminfolder" "$ADMIN_TOKEN" '{"operation":"mkdir"}')
        run_test "Admin: create folder should succeed (200 or 201)" "true" "$([ "$status" == "200" ] || [ "$status" == "201" ] && echo true || echo false)"

        # Cleanup: delete the test library
        api_call "DELETE" "/api/v2.1/repos/${lib_id}/" "$ADMIN_TOKEN" > /dev/null
    else
        log_info "Could not create test library for write operations"
    fi
}

# ============================================
# Summary
# ============================================
print_summary() {
    log_section "Test Summary"

    echo ""
    echo "Total tests:  $TOTAL_TESTS"
    echo -e "Passed:       ${GREEN}$PASSED_TESTS${NC}"
    echo -e "Failed:       ${RED}$FAILED_TESTS${NC}"
    echo ""

    if [ $FAILED_TESTS -gt 0 ]; then
        echo -e "${RED}Failed tests:${NC}"
        for test in "${FAILED_TEST_NAMES[@]}"; do
            echo "  - $test"
        done
        echo ""
        exit 1
    else
        echo -e "${GREEN}All tests passed!${NC}"
        exit 0
    fi
}

# ============================================
# Main
# ============================================
main() {
    log_section "SesameFS Permission Test Suite"
    log_info "API URL: $API_URL"
    echo ""

    # Check if API is reachable
    if ! curl -s -o /dev/null -w "%{http_code}" "${API_URL}/api2/ping/" | grep -q "200"; then
        log_fail "API not reachable at ${API_URL}"
        exit 1
    fi
    log_success "API is reachable"

    # Clean up any leftover test data before starting
    cleanup_test_libraries
    upgrade_default_org_for_permission_tests

    # Run test suites
    test_account_info
    test_library_creation
    test_library_access
    test_user_isolation
    test_encrypted_libraries
    test_write_operations

    # Final cleanup
    cleanup

    # Print summary
    print_summary
}

main "$@"
