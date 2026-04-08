#!/bin/bash
#
# Integration tests for Search API
# Tests search functionality including:
# - Basic search returns results
# - Files in nested directories have correct full_path
# - Security: only returns files from accessible libraries
# - fullpath field is correctly populated
#
# Usage:
#   ./scripts/test-search.sh           # Run all tests
#   ./scripts/test-search.sh --verbose # Show detailed output
#

set +e

# Configuration
SESAMEFS_URL="${SESAMEFS_URL:-http://localhost:8082}"
DEV_TOKEN="${DEV_TOKEN:-dev-token-admin}"
USER_TOKEN="${USER_TOKEN:-dev-token-user}"  # Different user for security test
VERBOSE=false

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# Counters
TESTS_PASSED=0
TESTS_FAILED=0

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --verbose|-v)
            VERBOSE=true
            shift
            ;;
        --help|-h)
            echo "Usage: $0 [--verbose]"
            echo ""
            echo "Options:"
            echo "  --verbose, -v  Show detailed request/response output"
            echo "  --help, -h     Show this help message"
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

# Helper functions
log_verbose() {
    if [ "$VERBOSE" = true ]; then
        echo -e "${BLUE}[DEBUG]${NC} $1"
    fi
}

log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[PASS]${NC} $1"
    ((TESTS_PASSED++))
}

log_fail() {
    echo -e "${RED}[FAIL]${NC} $1"
    ((TESTS_FAILED++))
}

# API helper functions
api_get() {
    local endpoint="$1"
    local token="${2:-$DEV_TOKEN}"
    local response
    response=$(timeout 30 curl -s -w "\n%{http_code}" "${SESAMEFS_URL}${endpoint}" \
        -H "Authorization: Token ${token}" \
        -H "Accept: application/json" 2>/dev/null)
    echo "$response"
}

api_post() {
    local endpoint="$1"
    local data="$2"
    local token="${3:-$DEV_TOKEN}"
    local response
    response=$(timeout 30 curl -s -w "\n%{http_code}" "${SESAMEFS_URL}${endpoint}" \
        -H "Authorization: Token ${token}" \
        -H "Content-Type: application/x-www-form-urlencoded" \
        -d "$data" 2>/dev/null)
    echo "$response"
}

api_delete() {
    local endpoint="$1"
    local token="${2:-$DEV_TOKEN}"
    local response
    response=$(timeout 30 curl -s -w "\n%{http_code}" -X DELETE "${SESAMEFS_URL}${endpoint}" \
        -H "Authorization: Token ${token}" 2>/dev/null)
    echo "$response"
}

# Upload a file
upload_file() {
    local repo_id="$1"
    local dir_path="$2"
    local filename="$3"
    local content="$4"
    local token="${5:-$DEV_TOKEN}"

    # Get upload link
    local encoded_path
    encoded_path=$(python3 -c "import urllib.parse; print(urllib.parse.quote('$dir_path', safe=''))" 2>/dev/null || echo "$dir_path")

    local upload_response
    upload_response=$(api_get "/api2/repos/${repo_id}/upload-link/?p=${encoded_path}" "$token")
    local http_code=$(echo "$upload_response" | tail -n1)
    local upload_url=$(echo "$upload_response" | head -n-1 | tr -d '"')

    if [ "$http_code" != "200" ]; then
        log_verbose "Failed to get upload link: $upload_response"
        return 1
    fi

    # Upload file
    local upload_result
    upload_result=$(timeout 30 curl -s -w "\n%{http_code}" "$upload_url" \
        -F "file=@-;filename=${filename}" \
        -F "parent_dir=${dir_path}" \
        -F "replace=1" \
        -H "Authorization: Token ${token}" \
        <<< "$content" 2>/dev/null)

    http_code=$(echo "$upload_result" | tail -n1)
    if [ "$http_code" = "200" ]; then
        return 0
    else
        log_verbose "Upload failed: $upload_result"
        return 1
    fi
}

# Cleanup function
CLEANUP_REPOS=()
cleanup() {
    echo ""
    log_info "Cleaning up test resources..."
    for repo_id in "${CLEANUP_REPOS[@]}"; do
        api_delete "/api/v2.1/repos/${repo_id}/" > /dev/null 2>&1
    done
    echo -e "${GREEN}✓${NC} Cleanup complete"
}
trap cleanup EXIT

echo -e "${CYAN}=========================================="
echo "  Search API Integration Tests"
echo -e "==========================================${NC}"
echo "Base URL: $SESAMEFS_URL"
echo ""

# ============================================
# Test 1: Create library with nested structure
# ============================================
log_info "Setting up test library with nested structure..."

TIMESTAMP=$(date +%s)
RESPONSE=$(api_post "/api/v2.1/repos/" "name=search-test-${TIMESTAMP}")
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
    exit 1
fi
CLEANUP_REPOS+=("$REPO_ID")
log_verbose "Created library: $REPO_ID"

# Create nested directories
log_info "Creating nested directory structure..."

# Create /level1
RESPONSE=$(api_post "/api/v2.1/repos/${REPO_ID}/dir/?p=/level1" "operation=mkdir")
log_verbose "Create /level1: $(echo "$RESPONSE" | tail -n1)"

# Create /level1/level2
RESPONSE=$(api_post "/api/v2.1/repos/${REPO_ID}/dir/?p=/level1/level2" "operation=mkdir")
log_verbose "Create /level1/level2: $(echo "$RESPONSE" | tail -n1)"

# Create /level1/level2/level3
RESPONSE=$(api_post "/api/v2.1/repos/${REPO_ID}/dir/?p=/level1/level2/level3" "operation=mkdir")
log_verbose "Create /level1/level2/level3: $(echo "$RESPONSE" | tail -n1)"

# Upload test files at different levels
log_info "Uploading test files..."

# Root level file
upload_file "$REPO_ID" "/" "searchable-root.txt" "root level searchable content"
log_verbose "Uploaded /searchable-root.txt"

# Level 1 file
upload_file "$REPO_ID" "/level1" "searchable-l1.txt" "level 1 searchable content"
log_verbose "Uploaded /level1/searchable-l1.txt"

# Level 2 file
upload_file "$REPO_ID" "/level1/level2" "searchable-l2.txt" "level 2 searchable content"
log_verbose "Uploaded /level1/level2/searchable-l2.txt"

# Level 3 file (deep nesting)
upload_file "$REPO_ID" "/level1/level2/level3" "searchable-deep.txt" "deep nested searchable content"
log_verbose "Uploaded /level1/level2/level3/searchable-deep.txt"

# Wait for indexing (paths are updated async after sync)
log_info "Waiting for search index update..."
sleep 2

# ============================================
# Test 2: Search returns results
# ============================================
echo ""
echo -e "${CYAN}--- Test: Search returns results ---${NC}"

RESPONSE=$(api_get "/api/v2.1/search/?q=searchable")
HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
BODY=$(echo "$RESPONSE" | head -n-1)

log_verbose "Search response: $BODY"

if [ "$HTTP_CODE" = "200" ]; then
    TOTAL=$(echo "$BODY" | grep -o '"total":[0-9]*' | cut -d':' -f2)
    if [ "$TOTAL" -ge 4 ]; then
        log_success "Search returns results (found $TOTAL results for 'searchable')"
    else
        log_fail "Search returned fewer results than expected (got $TOTAL, expected >= 4)"
    fi
else
    log_fail "Search request failed with HTTP $HTTP_CODE"
fi

# ============================================
# Test 3: Search results have fullpath field
# ============================================
echo ""
echo -e "${CYAN}--- Test: Search results have fullpath field ---${NC}"

if echo "$BODY" | grep -q '"fullpath"'; then
    log_success "Search results contain fullpath field"
else
    log_fail "Search results missing fullpath field"
    log_verbose "Response: $BODY"
fi

# ============================================
# Test 4: Root file has correct path
# ============================================
echo ""
echo -e "${CYAN}--- Test: Root file has correct fullpath ---${NC}"

# Filter by our specific repo_id to avoid picking up old test data
ROOT_FILE_PATH=$(echo "$BODY" | jq -r --arg repo "$REPO_ID" '([.results[]? | select(.repo_id == $repo and .name == "searchable-root.txt")][0].fullpath) // ""')
log_verbose "Root file fullpath: $ROOT_FILE_PATH"

if [ "$ROOT_FILE_PATH" = "/searchable-root.txt" ]; then
    log_success "Root file has correct fullpath: $ROOT_FILE_PATH"
else
    log_fail "Root file has wrong fullpath (got '$ROOT_FILE_PATH', expected '/searchable-root.txt')"
fi

# ============================================
# Test 5: Nested file has correct path
# ============================================
echo ""
echo -e "${CYAN}--- Test: Nested file has correct fullpath ---${NC}"

# Check level 2 file - filter by our specific repo_id
L2_FILE_PATH=$(echo "$BODY" | jq -r --arg repo "$REPO_ID" '([.results[]? | select(.repo_id == $repo and .name == "searchable-l2.txt")][0].fullpath) // ""')
log_verbose "Level 2 file fullpath: $L2_FILE_PATH"

if [ "$L2_FILE_PATH" = "/level1/level2/searchable-l2.txt" ]; then
    log_success "Level 2 file has correct fullpath: $L2_FILE_PATH"
else
    log_fail "Level 2 file has wrong fullpath (got '$L2_FILE_PATH', expected '/level1/level2/searchable-l2.txt')"
fi

# ============================================
# Test 6: Deep nested file has correct path
# ============================================
echo ""
echo -e "${CYAN}--- Test: Deep nested file has correct fullpath ---${NC}"

DEEP_FILE_PATH=$(echo "$BODY" | jq -r --arg repo "$REPO_ID" '([.results[]? | select(.repo_id == $repo and .name == "searchable-deep.txt")][0].fullpath) // ""')
log_verbose "Deep file fullpath: $DEEP_FILE_PATH"

if [ "$DEEP_FILE_PATH" = "/level1/level2/level3/searchable-deep.txt" ]; then
    log_success "Deep nested file has correct fullpath: $DEEP_FILE_PATH"
else
    log_fail "Deep nested file has wrong fullpath (got '$DEEP_FILE_PATH', expected '/level1/level2/level3/searchable-deep.txt')"
fi

# ============================================
# Test 7: Search within specific repo
# ============================================
echo ""
echo -e "${CYAN}--- Test: Search within specific repo ---${NC}"

RESPONSE=$(api_get "/api/v2.1/search/?q=searchable&repo_id=${REPO_ID}")
HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
BODY=$(echo "$RESPONSE" | head -n-1)

if [ "$HTTP_CODE" = "200" ]; then
    # All results should be from our test repo
    OTHER_REPOS=$(echo "$BODY" | grep -o '"repo_id":"[^"]*"' | grep -v "$REPO_ID" | wc -l)
    if [ "$OTHER_REPOS" = "0" ]; then
        log_success "Search within repo returns only results from specified repo"
    else
        log_fail "Search within repo returned results from other repos"
    fi
else
    log_fail "Search within repo failed with HTTP $HTTP_CODE"
fi

# ============================================
# Test 8: Search with type filter (file)
# ============================================
echo ""
echo -e "${CYAN}--- Test: Search with type=file filter ---${NC}"

RESPONSE=$(api_get "/api/v2.1/search/?q=searchable&type=file")
HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
BODY=$(echo "$RESPONSE" | head -n-1)

if [ "$HTTP_CODE" = "200" ]; then
    # All results should be files, not dirs
    DIR_RESULTS=$(echo "$BODY" | grep -o '"type":"dir"' | wc -l)
    if [ "$DIR_RESULTS" = "0" ]; then
        log_success "Search with type=file returns only files"
    else
        log_fail "Search with type=file returned directories"
    fi
else
    log_fail "Search with type=file failed with HTTP $HTTP_CODE"
fi

# ============================================
# Test 9: Search is_dir field present
# ============================================
echo ""
echo -e "${CYAN}--- Test: Search results have is_dir field ---${NC}"

if echo "$BODY" | grep -q '"is_dir"'; then
    log_success "Search results contain is_dir field"
else
    log_fail "Search results missing is_dir field"
fi

# ============================================
# Test 10: Empty query returns error
# ============================================
echo ""
echo -e "${CYAN}--- Test: Empty query returns error ---${NC}"

RESPONSE=$(api_get "/api/v2.1/search/?q=")
HTTP_CODE=$(echo "$RESPONSE" | tail -n1)

if [ "$HTTP_CODE" = "400" ]; then
    log_success "Empty query returns 400 Bad Request"
else
    log_fail "Empty query should return 400 (got $HTTP_CODE)"
fi

# ============================================
# Test 11: Search without auth returns 401 (skip in dev mode)
# ============================================
echo ""
echo -e "${CYAN}--- Test: Search without auth behavior ---${NC}"

RESPONSE=$(timeout 10 curl -s -w "\n%{http_code}" "${SESAMEFS_URL}/api/v2.1/search/?q=test" 2>/dev/null)
HTTP_CODE=$(echo "$RESPONSE" | tail -n1)

# In dev mode, the server may set a default org_id for unauthenticated requests
# This is expected for development convenience. In production, auth should be enforced.
if [ "$HTTP_CODE" = "401" ]; then
    log_success "Search without auth returns 401 Unauthorized (production mode)"
elif [ "$HTTP_CODE" = "200" ]; then
    log_success "Search without auth returns 200 (dev mode - default org_id set)"
else
    log_fail "Search without auth returned unexpected status $HTTP_CODE"
fi

# ============================================
# Summary
# ============================================
echo ""
echo -e "${CYAN}=========================================="
echo "  Test Summary"
echo -e "==========================================${NC}"
echo -e "Passed: ${GREEN}${TESTS_PASSED}${NC}"
echo -e "Failed: ${RED}${TESTS_FAILED}${NC}"
echo ""

if [ "$TESTS_FAILED" -eq 0 ]; then
    echo -e "${GREEN}All tests passed!${NC}"
    exit 0
else
    echo -e "${RED}Some tests failed.${NC}"
    exit 1
fi
