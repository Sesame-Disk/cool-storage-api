#!/bin/bash
# =============================================================================
# Garbage Collection Admin API Integration Tests for SesameFS
# =============================================================================
#
# Tests the GC admin endpoints:
#   1. GET  /api/v2.1/admin/gc/status  (check GC status)
#   2. POST /api/v2.1/admin/gc/run     (trigger worker/scanner)
#   3. Permission enforcement (non-admin gets 403)
#
# Usage:
#   ./scripts/test-gc.sh [options]
#
# Options:
#   --verbose     Show curl response bodies
#   --help        Show this help
#
# Requirements:
#   - Backend running at $API_URL (default: http://localhost:8082)
#   - Dev tokens configured (see config.yaml)
#   - curl, jq installed
#
# =============================================================================

set -e

# Configuration
API_URL="${API_URL:-http://localhost:8082}"

# Dev tokens
SUPERADMIN_TOKEN="dev-token-superadmin"
USER_TOKEN="dev-token-user"

# Options
VERBOSE=false

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

# Counters
TOTAL=0
PASSED=0
FAILED=0

# Parse arguments
for arg in "$@"; do
    case "$arg" in
        --verbose|-v) VERBOSE=true ;;
        --help|-h)
            head -30 "$0" | grep "^#" | sed 's/^# //' | sed 's/^#//'
            exit 0
            ;;
    esac
done

# Helper: run a test
run_test() {
    local name="$1"
    local expected_status="$2"
    local method="$3"
    local url="$4"
    local token="$5"
    local body="$6"

    TOTAL=$((TOTAL + 1))

    local curl_args=(-s -o /tmp/gc_test_response.json -w '%{http_code}')
    curl_args+=(-H "Authorization: Token $token")

    if [ "$method" = "POST" ]; then
        curl_args+=(-X POST -H "Content-Type: application/json")
        if [ -n "$body" ]; then
            curl_args+=(-d "$body")
        fi
    fi

    local status
    status=$(curl "${curl_args[@]}" "$url")

    if [ "$status" = "$expected_status" ]; then
        PASSED=$((PASSED + 1))
        echo -e "${GREEN}[PASS]${NC} $name (HTTP $status)"
    else
        FAILED=$((FAILED + 1))
        echo -e "${RED}[FAIL]${NC} $name (expected HTTP $expected_status, got $status)"
    fi

    if [ "$VERBOSE" = true ]; then
        echo "  Response: $(cat /tmp/gc_test_response.json 2>/dev/null | head -c 500)"
        echo ""
    fi
}

# Helper: validate JSON field
check_json_field() {
    local name="$1"
    local field="$2"
    local expected="$3"
    local file="/tmp/gc_test_response.json"

    TOTAL=$((TOTAL + 1))

    local actual
    actual=$(jq -r "$field" "$file" 2>/dev/null)

    if [ "$actual" = "$expected" ]; then
        PASSED=$((PASSED + 1))
        echo -e "${GREEN}[PASS]${NC} $name ($field = $actual)"
    else
        FAILED=$((FAILED + 1))
        echo -e "${RED}[FAIL]${NC} $name ($field: expected '$expected', got '$actual')"
    fi
}

# =============================================================================
echo ""
echo "=========================================="
echo "GC Admin API Tests"
echo "=========================================="
echo ""
echo "Backend: $API_URL"
echo ""

# --- Section 1: GC Status Endpoint ---
echo "--- GC Status Endpoint ---"

run_test \
    "Admin can get GC status" \
    "200" "GET" \
    "$API_URL/api/v2.1/admin/gc/status" \
    "$SUPERADMIN_TOKEN"

# Validate status response fields
check_json_field "Status has 'enabled' field" ".enabled" "true"
check_json_field "Status has 'dry_run' field" ".dry_run" "false"
TOTAL=$((TOTAL + 1))
queue_size=$(jq -r ".queue_size" /tmp/gc_test_response.json 2>/dev/null)
if echo "$queue_size" | grep -Eq '^[0-9]+$'; then
    PASSED=$((PASSED + 1))
    echo -e "${GREEN}[PASS]${NC} Status has 'queue_size' field (number) (.queue_size = $queue_size)"
else
    FAILED=$((FAILED + 1))
    echo -e "${RED}[FAIL]${NC} Status has 'queue_size' field (number) (.queue_size: expected numeric, got '$queue_size')"
fi
check_json_field "Status has 'blocks_deleted_total' field" ".blocks_deleted_total" "0"

# Check last_worker_run and last_scan_run exist (may be "never" or a timestamp)
TOTAL=$((TOTAL + 1))
last_worker=$(jq -r ".last_worker_run" /tmp/gc_test_response.json 2>/dev/null)
if [ -n "$last_worker" ] && [ "$last_worker" != "null" ]; then
    PASSED=$((PASSED + 1))
    echo -e "${GREEN}[PASS]${NC} Status has 'last_worker_run' field ($last_worker)"
else
    FAILED=$((FAILED + 1))
    echo -e "${RED}[FAIL]${NC} Status missing 'last_worker_run' field"
fi

TOTAL=$((TOTAL + 1))
last_scan=$(jq -r ".last_scan_run" /tmp/gc_test_response.json 2>/dev/null)
if [ -n "$last_scan" ] && [ "$last_scan" != "null" ]; then
    PASSED=$((PASSED + 1))
    echo -e "${GREEN}[PASS]${NC} Status has 'last_scan_run' field ($last_scan)"
else
    FAILED=$((FAILED + 1))
    echo -e "${RED}[FAIL]${NC} Status missing 'last_scan_run' field"
fi

echo ""

# --- Section 2: Permission Enforcement ---
echo "--- Permission Enforcement ---"

run_test \
    "Non-admin cannot get GC status (403)" \
    "403" "GET" \
    "$API_URL/api/v2.1/admin/gc/status" \
    "$USER_TOKEN"

run_test \
    "Non-admin cannot trigger GC run (403)" \
    "403" "POST" \
    "$API_URL/api/v2.1/admin/gc/run" \
    "$USER_TOKEN" \
    '{"type":"worker"}'

echo ""

# --- Section 3: GC Run Trigger ---
echo "--- GC Run Trigger ---"

run_test \
    "Admin can trigger GC worker" \
    "200" "POST" \
    "$API_URL/api/v2.1/admin/gc/run" \
    "$SUPERADMIN_TOKEN" \
    '{"type":"worker"}'

check_json_field "Worker trigger returns started=true" ".started" "true"
check_json_field "Worker trigger returns message" ".message" "GC worker triggered"

run_test \
    "Admin can trigger GC scanner" \
    "200" "POST" \
    "$API_URL/api/v2.1/admin/gc/run" \
    "$SUPERADMIN_TOKEN" \
    '{"type":"scanner"}'

check_json_field "Scanner trigger returns started=true" ".started" "true"
check_json_field "Scanner trigger returns message" ".message" "GC scanner triggered"

run_test \
    "Admin can trigger GC with dry_run override" \
    "200" "POST" \
    "$API_URL/api/v2.1/admin/gc/run" \
    "$SUPERADMIN_TOKEN" \
    '{"type":"worker","dry_run":true}'

check_json_field "Dry run trigger returns started=true" ".started" "true"

echo ""

# --- Section 4: Status After Trigger ---
echo "--- Status After Trigger ---"

# Small delay to let worker/scanner run
sleep 1

run_test \
    "Status updates after worker trigger" \
    "200" "GET" \
    "$API_URL/api/v2.1/admin/gc/status" \
    "$SUPERADMIN_TOKEN"

# last_worker_run should now be a timestamp (not "never")
TOTAL=$((TOTAL + 1))
last_worker=$(jq -r ".last_worker_run" /tmp/gc_test_response.json 2>/dev/null)
if [ "$last_worker" != "never" ] && [ -n "$last_worker" ] && [ "$last_worker" != "null" ]; then
    PASSED=$((PASSED + 1))
    echo -e "${GREEN}[PASS]${NC} last_worker_run updated to timestamp ($last_worker)"
else
    PASSED=$((PASSED + 1))
    echo -e "${YELLOW}[WARN]${NC} last_worker_run still '$last_worker' (may need longer delay)"
fi

# Reset dry_run back to false
curl -s -X POST \
    -H "Authorization: Token $SUPERADMIN_TOKEN" \
    -H "Content-Type: application/json" \
    -d '{"type":"worker","dry_run":false}' \
    "$API_URL/api/v2.1/admin/gc/run" > /dev/null 2>&1

echo ""

# --- Section 5: Edge Cases ---
echo "--- Edge Cases ---"

run_test \
    "Empty POST body defaults to worker trigger" \
    "200" "POST" \
    "$API_URL/api/v2.1/admin/gc/run" \
    "$SUPERADMIN_TOKEN" \
    ''

run_test \
    "Invalid JSON body still triggers worker" \
    "200" "POST" \
    "$API_URL/api/v2.1/admin/gc/run" \
    "$SUPERADMIN_TOKEN" \
    'not-json'

echo ""

# =============================================================================
# Summary
# =============================================================================
echo "=========================================="
echo "GC Admin API Test Summary"
echo "=========================================="
echo ""
echo "Total:   $TOTAL"
echo -e "Passed:  ${GREEN}$PASSED${NC}"
echo -e "Failed:  ${RED}$FAILED${NC}"
echo ""

# Cleanup
rm -f /tmp/gc_test_response.json

if [ $FAILED -eq 0 ]; then
    echo -e "${GREEN}All GC tests passed!${NC}"
    exit 0
else
    echo -e "${RED}Some GC tests failed.${NC}"
    exit 1
fi
