#!/bin/bash

# =============================================================================
# OIDC Integration Tests for SesameFS
# =============================================================================
# Tests the OIDC authentication endpoints and SSO flow
#
# Usage:
#   ./scripts/test-oidc.sh [options]
#
# Options:
#   --quick     Skip tests that require external OIDC provider
#   --verbose   Show detailed request/response output
#   --help      Show this help message
#
# Requirements:
#   - Backend running at $BASE_URL (default: http://localhost:8082)
#   - curl, jq installed
#
# =============================================================================

set -e

# Configuration
BASE_URL="${BASE_URL:-http://localhost:8082}"
QUICK_MODE=false
VERBOSE=false

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# Test counters
PASSED=0
FAILED=0
SKIPPED=0
TOTAL=0

# =============================================================================
# Helper Functions
# =============================================================================

log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[PASS]${NC} $1"
}

log_error() {
    echo -e "${RED}[FAIL]${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_skip() {
    echo -e "${CYAN}[SKIP]${NC} $1"
}

log_section() {
    echo ""
    echo -e "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${CYAN}  $1${NC}"
    echo -e "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
}

run_test() {
    local name="$1"
    local cmd="$2"
    local expected_status="${3:-200}"

    TOTAL=$((TOTAL + 1))

    if $VERBOSE; then
        echo -e "${BLUE}[TEST]${NC} $name"
        echo "Command: $cmd"
    fi

    # Run the command and capture output
    local response
    local http_code

    response=$(eval "$cmd" 2>&1) || true

    # Extract HTTP code from response if present
    if echo "$response" | grep -q "HTTP_CODE:"; then
        http_code=$(echo "$response" | grep "HTTP_CODE:" | cut -d: -f2)
        response=$(echo "$response" | grep -v "HTTP_CODE:")
    else
        http_code="000"
    fi

    if $VERBOSE; then
        echo "Response: $response"
        echo "HTTP Code: $http_code"
    fi

    if [ "$http_code" = "$expected_status" ]; then
        log_success "$name"
        PASSED=$((PASSED + 1))
        return 0
    else
        log_error "$name (got $http_code, expected $expected_status)"
        if ! $VERBOSE; then
            echo "  Response: $response"
        fi
        FAILED=$((FAILED + 1))
        return 1
    fi
}

skip_test() {
    local name="$1"
    local reason="$2"
    TOTAL=$((TOTAL + 1))
    SKIPPED=$((SKIPPED + 1))
    log_skip "$name - $reason"
}

# HTTP request helper with status code extraction
http_get() {
    local url="$1"
    local output=$(curl -s -w "\nHTTP_CODE:%{http_code}" "$url")
    echo "$output"
}

http_post() {
    local url="$1"
    local data="$2"
    local content_type="${3:-application/json}"
    local output=$(curl -s -w "\nHTTP_CODE:%{http_code}" -X POST \
        -H "Content-Type: $content_type" \
        -d "$data" "$url")
    echo "$output"
}

http_delete() {
    local url="$1"
    local token="$2"
    local auth_header=""
    if [ -n "$token" ]; then
        auth_header="-H \"Authorization: Token $token\""
    fi
    local output=$(eval "curl -s -w '\nHTTP_CODE:%{http_code}' -X DELETE $auth_header '$url'")
    echo "$output"
}

# =============================================================================
# Pre-flight Checks
# =============================================================================

check_prerequisites() {
    log_section "Pre-flight Checks"

    # Check if backend is running
    log_info "Checking backend at $BASE_URL..."
    if curl -s -o /dev/null -w "%{http_code}" "$BASE_URL/api/v2.1/auth/oidc/config/" | grep -q "200"; then
        log_success "Backend is running"
    else
        log_error "Backend is not running at $BASE_URL"
        echo "Please start the backend first:"
        echo "  docker-compose up -d sesamefs"
        exit 1
    fi

    # Check for required tools
    for tool in curl jq; do
        if ! command -v $tool &> /dev/null; then
            log_error "$tool is required but not installed"
            exit 1
        fi
    done
    log_success "Required tools available"
}

# =============================================================================
# OIDC Configuration Tests
# =============================================================================

test_oidc_config() {
    log_section "OIDC Configuration Endpoint"

    # Test GET /api/v2.1/auth/oidc/config/
    run_test "GET /api/v2.1/auth/oidc/config/ returns 200" \
        "http_get '$BASE_URL/api/v2.1/auth/oidc/config/'" \
        "200"

    # Verify response structure
    log_info "Verifying response structure..."
    local response=$(curl -s "$BASE_URL/api/v2.1/auth/oidc/config/")

    TOTAL=$((TOTAL + 1))
    if echo "$response" | jq -e '.enabled' > /dev/null 2>&1; then
        log_success "Response contains 'enabled' field"
        PASSED=$((PASSED + 1))
    else
        log_error "Response missing 'enabled' field"
        FAILED=$((FAILED + 1))
    fi

    # Check that client_secret is NOT exposed
    TOTAL=$((TOTAL + 1))
    if echo "$response" | jq -e '.client_secret' > /dev/null 2>&1; then
        log_error "Response should NOT contain client_secret"
        FAILED=$((FAILED + 1))
    else
        log_success "client_secret is not exposed"
        PASSED=$((PASSED + 1))
    fi

    # Check OIDC enabled status
    local enabled=$(echo "$response" | jq -r '.enabled')
    log_info "OIDC enabled: $enabled"

    if [ "$enabled" = "true" ]; then
        TOTAL=$((TOTAL + 1))
        if echo "$response" | jq -e 'keys == ["enabled"]' > /dev/null 2>&1; then
            log_success "Public OIDC config exposes only the enabled flag"
            PASSED=$((PASSED + 1))
        else
            log_error "OIDC config exposes unexpected public fields"
            FAILED=$((FAILED + 1))
        fi
    fi
}

# =============================================================================
# OIDC Login URL Tests
# =============================================================================

test_oidc_login() {
    log_section "OIDC Login URL Endpoint"

    # Check if OIDC is enabled first
    local config=$(curl -s "$BASE_URL/api/v2.1/auth/oidc/config/")
    local enabled=$(echo "$config" | jq -r '.enabled')

    if [ "$enabled" != "true" ]; then
        skip_test "GET /api/v2.1/auth/oidc/login/" "OIDC is not enabled"
        return
    fi

    if $QUICK_MODE; then
        skip_test "GET /api/v2.1/auth/oidc/login/" "Skipped in quick mode (requires OIDC provider)"
        return
    fi

    # Let the server choose the configured redirect URI. The public config
    # endpoint intentionally does not expose redirect allowlists anymore.
    run_test "GET /api/v2.1/auth/oidc/login/ returns 200" \
        "http_get '$BASE_URL/api/v2.1/auth/oidc/login/'" \
        "200"

    # Verify authorization_url is returned
    local response=$(curl -s "$BASE_URL/api/v2.1/auth/oidc/login/")

    TOTAL=$((TOTAL + 1))
    local auth_url=$(echo "$response" | jq -r '.authorization_url')
    if [ -n "$auth_url" ] && [ "$auth_url" != "null" ]; then
        log_success "Authorization URL returned"
        PASSED=$((PASSED + 1))

        # Verify URL contains required OIDC parameters
        TOTAL=$((TOTAL + 1))
        if echo "$auth_url" | grep -q "client_id="; then
            log_success "Authorization URL contains client_id"
            PASSED=$((PASSED + 1))
        else
            log_error "Authorization URL missing client_id"
            FAILED=$((FAILED + 1))
        fi

        TOTAL=$((TOTAL + 1))
        if echo "$auth_url" | grep -q "response_type=code"; then
            log_success "Authorization URL contains response_type=code"
            PASSED=$((PASSED + 1))
        else
            log_error "Authorization URL missing response_type=code"
            FAILED=$((FAILED + 1))
        fi

        TOTAL=$((TOTAL + 1))
        if echo "$auth_url" | grep -q "state="; then
            log_success "Authorization URL contains state"
            PASSED=$((PASSED + 1))
        else
            log_error "Authorization URL missing state"
            FAILED=$((FAILED + 1))
        fi
    else
        log_error "No authorization_url in response"
        FAILED=$((FAILED + 1))
    fi
}

# =============================================================================
# OIDC Callback Tests
# =============================================================================

test_oidc_callback() {
    log_section "OIDC Callback Endpoint"

    # Test missing required fields
    run_test "POST /api/v2.1/auth/oidc/callback/ rejects missing fields" \
        "http_post '$BASE_URL/api/v2.1/auth/oidc/callback/' '{\"code\": \"test\"}'" \
        "400"

    # Test invalid state
    run_test "POST /api/v2.1/auth/oidc/callback/ rejects invalid state" \
        "http_post '$BASE_URL/api/v2.1/auth/oidc/callback/' '{\"code\": \"test\", \"state\": \"invalid\", \"redirect_uri\": \"http://localhost:3000/sso\"}'" \
        "401"

    # Test invalid JSON
    run_test "POST /api/v2.1/auth/oidc/callback/ rejects invalid JSON" \
        "http_post '$BASE_URL/api/v2.1/auth/oidc/callback/' 'not json'" \
        "400"
}

# =============================================================================
# OIDC Logout URL Tests
# =============================================================================

test_oidc_logout() {
    log_section "OIDC Logout URL Endpoint"

    # Test logout URL endpoint
    run_test "GET /api/v2.1/auth/oidc/logout/ returns 200" \
        "http_get '$BASE_URL/api/v2.1/auth/oidc/logout/'" \
        "200"

    # Verify response structure
    local response=$(curl -s "$BASE_URL/api/v2.1/auth/oidc/logout/")

    TOTAL=$((TOTAL + 1))
    if echo "$response" | jq -e '.enabled' > /dev/null 2>&1; then
        log_success "Response contains 'enabled' field"
        PASSED=$((PASSED + 1))
    else
        log_error "Response missing 'enabled' field"
        FAILED=$((FAILED + 1))
    fi

    # Check if logout_url is present when OIDC is enabled
    local enabled=$(echo "$response" | jq -r '.enabled')
    if [ "$enabled" = "true" ]; then
        if ! $QUICK_MODE; then
            TOTAL=$((TOTAL + 1))
            local logout_url=$(echo "$response" | jq -r '.logout_url')
            if [ -n "$logout_url" ] && [ "$logout_url" != "null" ] && [ "$logout_url" != "" ]; then
                log_success "Logout URL returned: $logout_url"
                PASSED=$((PASSED + 1))

                # Verify logout URL contains required parameters
                TOTAL=$((TOTAL + 1))
                if echo "$logout_url" | grep -q "client_id="; then
                    log_success "Logout URL contains client_id"
                    PASSED=$((PASSED + 1))
                else
                    log_error "Logout URL missing client_id"
                    FAILED=$((FAILED + 1))
                fi
            else
                log_warning "No logout URL (IdP may not support end_session_endpoint)"
                PASSED=$((PASSED + 1))
            fi
        else
            skip_test "Logout URL validation" "Skipped in quick mode"
        fi
    fi

    # Test with post_logout_redirect_uri
    run_test "GET /api/v2.1/auth/oidc/logout/ accepts post_logout_redirect_uri" \
        "http_get '$BASE_URL/api/v2.1/auth/oidc/logout/?post_logout_redirect_uri=http://localhost:3000/login/'" \
        "200"
}

# =============================================================================
# Session Endpoint Tests
# =============================================================================

test_session_endpoints() {
    log_section "Session Management Endpoints"

    # Test session info without token
    run_test "GET /api/v2.1/auth/session/ requires authentication" \
        "http_get '$BASE_URL/api/v2.1/auth/session/'" \
        "401"

    # Test session info with invalid token
    TOTAL=$((TOTAL + 1))
    local response=$(curl -s -w "\nHTTP_CODE:%{http_code}" \
        -H "Authorization: Token invalid-token-12345" \
        "$BASE_URL/api/v2.1/auth/session/")
    local http_code=$(echo "$response" | grep "HTTP_CODE:" | cut -d: -f2)
    if [ "$http_code" = "401" ]; then
        log_success "GET /api/v2.1/auth/session/ rejects invalid token"
        PASSED=$((PASSED + 1))
    else
        log_error "GET /api/v2.1/auth/session/ should reject invalid token (got $http_code)"
        FAILED=$((FAILED + 1))
    fi

    # Test logout without token (strict contract: token required)
    run_test "DELETE /api/v2.1/auth/session/ rejects missing token" \
        "http_delete '$BASE_URL/api/v2.1/auth/session/'" \
        "401"

    # Test logout with invalid token
    TOTAL=$((TOTAL + 1))
    response=$(curl -s -w "\nHTTP_CODE:%{http_code}" -X DELETE \
        -H "Authorization: Token some-token" \
        "$BASE_URL/api/v2.1/auth/session/")
    http_code=$(echo "$response" | grep "HTTP_CODE:" | cut -d: -f2)
    if [ "$http_code" = "401" ]; then
        log_success "DELETE /api/v2.1/auth/session/ rejects invalid token"
        PASSED=$((PASSED + 1))
    else
        log_error "DELETE /api/v2.1/auth/session/ should reject invalid token (got $http_code)"
        FAILED=$((FAILED + 1))
    fi
}

# =============================================================================
# Trailing Slash Tests
# =============================================================================

test_trailing_slash() {
    log_section "Trailing Slash Handling"

    local endpoints=(
        "/api/v2.1/auth/oidc/config"
        "/api/v2.1/auth/oidc/config/"
        "/api/v2.1/auth/oidc/logout"
        "/api/v2.1/auth/oidc/logout/"
    )

    for endpoint in "${endpoints[@]}"; do
        run_test "GET $endpoint returns 200" \
            "http_get '$BASE_URL$endpoint'" \
            "200"
    done
}

# =============================================================================
# Main
# =============================================================================

show_help() {
    echo "OIDC Integration Tests for SesameFS"
    echo ""
    echo "Usage: $0 [options]"
    echo ""
    echo "Options:"
    echo "  --quick     Skip tests that require external OIDC provider"
    echo "  --verbose   Show detailed request/response output"
    echo "  --help      Show this help message"
    echo ""
    echo "Environment Variables:"
    echo "  BASE_URL    Backend URL (default: http://localhost:8082)"
}

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --quick)
            QUICK_MODE=true
            shift
            ;;
        --verbose)
            VERBOSE=true
            shift
            ;;
        --help)
            show_help
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            show_help
            exit 1
            ;;
    esac
done

echo ""
echo "╔═══════════════════════════════════════════════════════════════╗"
echo "║              SesameFS OIDC Integration Tests                  ║"
echo "╚═══════════════════════════════════════════════════════════════╝"
echo ""
echo "Backend URL: $BASE_URL"
echo "Quick Mode: $QUICK_MODE"
echo "Verbose: $VERBOSE"

# Run tests
check_prerequisites
test_oidc_config
test_oidc_login
test_oidc_callback
test_oidc_logout
test_session_endpoints
test_trailing_slash

# Summary
log_section "Test Summary"
echo ""
echo -e "  Total:   $TOTAL"
echo -e "  ${GREEN}Passed:  $PASSED${NC}"
echo -e "  ${RED}Failed:  $FAILED${NC}"
echo -e "  ${CYAN}Skipped: $SKIPPED${NC}"
echo ""

if [ $FAILED -gt 0 ]; then
    echo -e "${RED}Some tests failed!${NC}"
    exit 1
else
    echo -e "${GREEN}All tests passed!${NC}"
    exit 0
fi
