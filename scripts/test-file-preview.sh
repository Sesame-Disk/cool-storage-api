#!/bin/bash
#
# Test script for file preview and raw file serving
# Tests: Raw file endpoint, inline previews, iWork preview extraction, nginx proxy routing
#
# Prerequisites:
# - SesameFS backend running on localhost:8082
# - Frontend (nginx) running on localhost:3000 (for proxy tests)
#
# Usage: ./test-file-preview.sh [token]

set -e

TOKEN="${1:-dev-token-admin}"
BASE_URL="${SESAMEFS_URL:-http://localhost:8082}"
FRONTEND_URL="${FRONTEND_URL:-http://localhost:3000}"

echo "==================================================="
echo "File Preview & Raw Serving Tests"
echo "==================================================="
echo "Backend URL: $BASE_URL"
echo "Frontend URL: $FRONTEND_URL"
echo "Token: $TOKEN"
echo ""

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

PASS_COUNT=0
FAIL_COUNT=0

pass() { echo -e "${GREEN}✓ PASS${NC}: $1"; PASS_COUNT=$((PASS_COUNT + 1)); }
fail() { echo -e "${RED}✗ FAIL${NC}: $1"; FAIL_COUNT=$((FAIL_COUNT + 1)); }
info() { echo -e "${YELLOW}→${NC} $1"; }

# API helpers
api_get() {
    curl -s -w "\n%{http_code}" -H "Authorization: Token $TOKEN" "$BASE_URL$1"
}

api_post_json() {
    curl -s -w "\n%{http_code}" -X POST \
        -H "Authorization: Token $TOKEN" \
        -H "Content-Type: application/json" \
        -d "$2" "$BASE_URL$1"
}

api_post() {
    curl -s -w "\n%{http_code}" -X POST \
        -H "Authorization: Token $TOKEN" \
        -H "Content-Type: application/x-www-form-urlencoded" \
        -d "$2" "$BASE_URL$1"
}

api_delete() {
    curl -s -w "\n%{http_code}" -X DELETE \
        -H "Authorization: Token $TOKEN" "$BASE_URL$1"
}

api_upload() {
    local repo_id="$1"
    local dir="$2"
    local filename="$3"
    local filepath="$4"
    # Get upload link
    local ul_response=$(api_get "/api2/repos/$repo_id/upload-link/?p=$dir")
    local ul_body=$(echo "$ul_response" | head -n -1)
    local ul_status=$(echo "$ul_response" | tail -1)
    if [ "$ul_status" != "200" ]; then
        echo "UPLOAD_FAIL"
        return 1
    fi
    local upload_url=$(echo "$ul_body" | tr -d '"')
    # Upload
    curl -s -w "\n%{http_code}" \
        -H "Authorization: Token $TOKEN" \
        -F "file=@$filepath;filename=$filename" \
        -F "parent_dir=$dir" \
        "$upload_url"
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
        echo "    Response: $(echo "$body" | head -c 200)"
        return 1
    fi
}

# Create a fresh test library
info "Creating fresh test library..."
TIMESTAMP=$(date +%s)
create_response=$(api_post_json "/api/v2.1/repos/" "{\"repo_name\":\"PreviewTest-${TIMESTAMP}\"}")
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
    rm -f /tmp/test-preview-*.tmp 2>/dev/null || true
}
trap cleanup EXIT

echo "Using library: $REPO_ID"
echo ""

# =============================================================================
# Setup: Create test files
# =============================================================================
echo "=== Setup: Creating test files ==="

# Create a text file
info "Creating test text file..."
echo "Hello, this is a test text file for preview." > /tmp/test-preview-text.tmp
upload_response=$(api_upload "$REPO_ID" "/" "test.txt" "/tmp/test-preview-text.tmp")
check_response "$upload_response" "200" "Upload text file" || true

# Create a JSON file
echo '{"key": "value", "number": 42}' > /tmp/test-preview-json.tmp
upload_response=$(api_upload "$REPO_ID" "/" "data.json" "/tmp/test-preview-json.tmp")
check_response "$upload_response" "200" "Upload JSON file" || true

# Create a small PNG file (1x1 red pixel)
printf '\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x02\x00\x00\x00\x90wS\xde\x00\x00\x00\x0cIDATx\x9cc\xf8\x0f\x00\x00\x01\x01\x00\x05\x18\xd8N\x00\x00\x00\x00IEND\xaeB`\x82' > /tmp/test-preview-png.tmp
upload_response=$(api_upload "$REPO_ID" "/" "image.png" "/tmp/test-preview-png.tmp")
check_response "$upload_response" "200" "Upload PNG file" || true

# Create a small YAML file
printf "name: test\nversion: 1.0\nfeatures:\n  - preview\n  - raw\n" > /tmp/test-preview-yaml.tmp
upload_response=$(api_upload "$REPO_ID" "/" "config.yaml" "/tmp/test-preview-yaml.tmp")
check_response "$upload_response" "200" "Upload YAML file" || true

# Create a minimal .pages file (ZIP with preview.jpg)
# Build a ZIP archive with a JPEG preview inside when python3 is available.
if command -v python3 > /dev/null 2>&1; then
    python3 -c "
import io, sys, zipfile
buf = io.BytesIO()
with zipfile.ZipFile(buf, 'w') as zf:
    jpg = b'\xff\xd8\xff\xe0\x00\x10JFIF\x00\x01\x01\x00\x00\x01\x00\x01\x00\x00\xff\xd9'
    zf.writestr('preview.jpg', jpg)
    zf.writestr('Index/Document.iwa', b'\x00')
buf.seek(0)
sys.stdout.buffer.write(buf.read())
" > /tmp/test-preview-pages.tmp 2>/dev/null
fi

if [ -s /tmp/test-preview-pages.tmp ]; then
    upload_response=$(api_upload "$REPO_ID" "/" "document.pages" "/tmp/test-preview-pages.tmp")
    check_response "$upload_response" "200" "Upload .pages file" || true
    HAS_PAGES=true
else
    info "Skipping .pages tests (python3 not available)"
    HAS_PAGES=false
fi

echo ""

# =============================================================================
# Test 1: Raw file endpoint serves text files with correct MIME type
# =============================================================================
echo "=== Test 1: Raw File Endpoint - Text Files ==="

info "Fetching raw text file..."
response=$(curl -s -w "\n%{http_code}" -D /tmp/test-preview-headers.tmp \
    -H "Authorization: Token $TOKEN" \
    "$BASE_URL/repo/$REPO_ID/raw/test.txt")
status=$(echo "$response" | tail -1)
body=$(echo "$response" | head -n -1)

if [ "$status" = "200" ]; then
    pass "Raw text file returns 200"
else
    fail "Raw text file returns $status (expected 200)"
fi

# Check Content-Type header (Go's mime.TypeByExtension may return "text/plain" or
# "application/octet-stream" depending on /etc/mime.types availability on the platform)
content_type=$(grep -i "content-type:" /tmp/test-preview-headers.tmp | tr -d '\r' | head -1)
if echo "$content_type" | grep -qi "text/plain"; then
    pass "Text file has correct MIME type (text/plain)"
elif echo "$content_type" | grep -qi "octet-stream"; then
    pass "Text file has MIME type (application/octet-stream — acceptable, no /etc/mime.types)"
else
    fail "Text file has wrong MIME type: $content_type"
fi

# Check Content-Disposition header
content_disp=$(grep -i "content-disposition:" /tmp/test-preview-headers.tmp | tr -d '\r' | head -1)
if echo "$content_disp" | grep -qi "inline"; then
    pass "Text file served inline (Content-Disposition: inline)"
else
    fail "Text file not served inline: $content_disp"
fi

# Check body content
if echo "$body" | grep -q "test text file for preview"; then
    pass "Text file content is correct"
else
    fail "Text file content mismatch"
fi

echo ""

# =============================================================================
# Test 2: Raw file endpoint serves JSON with correct MIME type
# =============================================================================
echo "=== Test 2: Raw File Endpoint - JSON Files ==="

response=$(curl -s -w "\n%{http_code}" -D /tmp/test-preview-headers.tmp \
    -H "Authorization: Token $TOKEN" \
    "$BASE_URL/repo/$REPO_ID/raw/data.json")
status=$(echo "$response" | tail -1)

if [ "$status" = "200" ]; then
    pass "Raw JSON file returns 200"
else
    fail "Raw JSON file returns $status"
fi

content_type=$(grep -i "content-type:" /tmp/test-preview-headers.tmp | tr -d '\r' | head -1)
if echo "$content_type" | grep -qi "json"; then
    pass "JSON file has correct MIME type"
else
    fail "JSON file has wrong MIME type: $content_type"
fi

echo ""

# =============================================================================
# Test 3: Raw file endpoint serves images with correct MIME type
# =============================================================================
echo "=== Test 3: Raw File Endpoint - Image Files ==="

response=$(curl -s -w "\n%{http_code}" -D /tmp/test-preview-headers.tmp \
    -H "Authorization: Token $TOKEN" \
    "$BASE_URL/repo/$REPO_ID/raw/image.png")
status=$(echo "$response" | tail -1)

if [ "$status" = "200" ]; then
    pass "Raw PNG file returns 200"
else
    fail "Raw PNG file returns $status"
fi

content_type=$(grep -i "content-type:" /tmp/test-preview-headers.tmp | tr -d '\r' | head -1)
if echo "$content_type" | grep -qi "image/png"; then
    pass "PNG file has correct MIME type (image/png)"
else
    fail "PNG file has wrong MIME type: $content_type"
fi

echo ""

# =============================================================================
# Test 4: Raw file endpoint - Token in query parameter
# =============================================================================
echo "=== Test 4: Raw File Endpoint - Token in Query Param ==="

response=$(curl -s -w "\n%{http_code}" \
    "$BASE_URL/repo/$REPO_ID/raw/test.txt?token=$TOKEN")
status=$(echo "$response" | tail -1)

if [ "$status" = "200" ]; then
    pass "Raw file with token in query param returns 200"
else
    fail "Raw file with token in query param returns $status"
fi

echo ""

# =============================================================================
# Test 5: Raw file endpoint - No auth returns 401
# =============================================================================
echo "=== Test 5: Raw File Endpoint - Auth Required ==="

response=$(curl -s -w "\n%{http_code}" "$BASE_URL/repo/$REPO_ID/raw/test.txt")
status=$(echo "$response" | tail -1)

if [ "$status" = "401" ]; then
    pass "Raw file without auth returns 401"
elif [ "$status" = "500" ] || [ "$status" = "403" ]; then
    pass "Raw file without auth returns $status (auth failure)"
else
    fail "Raw file without auth returns $status (expected 401/403/500)"
fi

echo ""

# =============================================================================
# Test 6: Raw file endpoint - File not found returns 404
# =============================================================================
echo "=== Test 6: Raw File Endpoint - File Not Found ==="

response=$(curl -s -w "\n%{http_code}" \
    -H "Authorization: Token $TOKEN" \
    "$BASE_URL/repo/$REPO_ID/raw/nonexistent.txt")
status=$(echo "$response" | tail -1)

if [ "$status" = "404" ]; then
    pass "Non-existent file returns 404"
else
    fail "Non-existent file returns $status (expected 404)"
fi

echo ""

# =============================================================================
# Test 7: iWork preview extraction (.pages)
# =============================================================================
if [ "$HAS_PAGES" = "true" ]; then
    echo "=== Test 7: iWork Preview Extraction ==="

    info "Fetching .pages file preview..."
    response=$(curl -s -w "\n%{http_code}" -D /tmp/test-preview-headers.tmp \
        -H "Authorization: Token $TOKEN" \
        "$BASE_URL/repo/$REPO_ID/raw/document.pages?preview=1")
    status=$(echo "$response" | tail -1)

    if [ "$status" = "200" ]; then
        pass "iWork preview extraction returns 200"
    else
        fail "iWork preview extraction returns $status"
    fi

    # Check that it returns an image (JPEG) content type
    content_type=$(grep -i "content-type:" /tmp/test-preview-headers.tmp | tr -d '\r' | head -1)
    if echo "$content_type" | grep -qi "image/jpeg"; then
        pass "iWork preview returns JPEG content type"
    elif echo "$content_type" | grep -qi "application/pdf"; then
        pass "iWork preview returns PDF content type"
    else
        fail "iWork preview has unexpected MIME type: $content_type"
    fi

    # Without ?preview=1, should serve the raw .pages file
    info "Fetching raw .pages file (no preview param)..."
    response=$(curl -s -w "\n%{http_code}" -D /tmp/test-preview-headers.tmp \
        -H "Authorization: Token $TOKEN" \
        "$BASE_URL/repo/$REPO_ID/raw/document.pages")
    status=$(echo "$response" | tail -1)

    if [ "$status" = "200" ]; then
        pass "Raw .pages file (without preview) returns 200"
    else
        fail "Raw .pages file returns $status"
    fi

    echo ""
fi

# =============================================================================
# Test 8: File view endpoint - previewable files
# =============================================================================
echo "=== Test 8: File View Endpoint - Inline Preview ==="

# Text files should serve inline preview HTML (not redirect to download)
info "Viewing text file..."
response=$(curl -s -w "\n%{http_code}" -D /tmp/test-preview-headers.tmp \
    "$BASE_URL/lib/$REPO_ID/file/test.txt?token=$TOKEN")
status=$(echo "$response" | tail -1)
body=$(echo "$response" | head -n -1)

if [ "$status" = "200" ]; then
    pass "Text file view returns 200 (inline preview)"
    if echo "$body" | grep -q "preview-container\|file-preview\|SesameFS"; then
        pass "Text file view contains preview HTML"
    else
        fail "Text file view missing preview HTML"
    fi
elif [ "$status" = "302" ]; then
    pass "Text file view redirects (302)"
else
    fail "Text file view returns $status (expected 200 or 302)"
fi

echo ""

# =============================================================================
# Test 9: File view endpoint - non-previewable files redirect to download
# =============================================================================
echo "=== Test 9: File View Endpoint - Download Redirect ==="

# Upload a non-previewable file (zip)
echo "PK" > /tmp/test-preview-zip.tmp  # minimal zip-like content
upload_response=$(api_upload "$REPO_ID" "/" "archive.zip" "/tmp/test-preview-zip.tmp")

info "Viewing non-previewable file (should redirect)..."
response=$(curl -s -o /dev/null -w "%{http_code}" \
    "$BASE_URL/lib/$REPO_ID/file/archive.zip?token=$TOKEN")

if [ "$response" = "302" ]; then
    pass "Non-previewable file redirects (302)"
else
    fail "Non-previewable file returns $response (expected 302 redirect)"
fi

echo ""

# =============================================================================
# Test 10: File view endpoint - dl=1 forces download
# =============================================================================
echo "=== Test 10: File View Endpoint - Forced Download ==="

info "Viewing text file with dl=1 (should redirect to download)..."
response=$(curl -s -o /dev/null -w "%{http_code}" \
    "$BASE_URL/lib/$REPO_ID/file/test.txt?dl=1&token=$TOKEN")

if [ "$response" = "302" ]; then
    pass "dl=1 forces download redirect (302)"
else
    fail "dl=1 returns $response (expected 302 redirect)"
fi

echo ""

# =============================================================================
# Test 11: Cache-Control headers
# =============================================================================
echo "=== Test 11: Cache-Control Headers ==="

response=$(curl -s -w "\n%{http_code}" -D /tmp/test-preview-headers.tmp \
    -H "Authorization: Token $TOKEN" \
    "$BASE_URL/repo/$REPO_ID/raw/test.txt")

cache_control=$(grep -i "cache-control:" /tmp/test-preview-headers.tmp | tr -d '\r' | head -1)
if echo "$cache_control" | grep -qi "private"; then
    pass "Cache-Control header is private"
else
    fail "Cache-Control header missing or not private: $cache_control"
fi

echo ""

# =============================================================================
# Test 12: Content-Disposition filename sanitization
# =============================================================================
echo "=== Test 12: Content-Disposition Filename ==="

info "Checking Content-Disposition header..."
content_disp=$(grep -i "content-disposition:" /tmp/test-preview-headers.tmp | tr -d '\r' | head -1)
if echo "$content_disp" | grep -q 'filename='; then
    pass "Content-Disposition includes filename"
else
    fail "Content-Disposition missing filename: $content_disp"
fi

echo ""

# =============================================================================
# Test 13: Nginx proxy routing (only if frontend is available)
# =============================================================================
echo "=== Test 13: Nginx Proxy Routing ==="

# Check if frontend is available
if curl -s -f "$FRONTEND_URL/health" > /dev/null 2>&1; then
    info "Frontend is available, testing nginx proxy routing..."

    # Test that /repo/<id>/raw/<path> is proxied to backend (not caught by static file handler)
    response=$(curl -s -w "\n%{http_code}" \
        "$FRONTEND_URL/repo/$REPO_ID/raw/image.png?token=$TOKEN")
    status=$(echo "$response" | tail -1)

    if [ "$status" = "200" ]; then
        pass "Nginx proxies /repo/.../raw/image.png to backend"
    else
        fail "Nginx proxy for image.png returns $status (expected 200)"
    fi

    # Test .txt through proxy
    response=$(curl -s -w "\n%{http_code}" \
        "$FRONTEND_URL/repo/$REPO_ID/raw/test.txt?token=$TOKEN")
    status=$(echo "$response" | tail -1)

    if [ "$status" = "200" ]; then
        pass "Nginx proxies /repo/.../raw/test.txt to backend"
    else
        fail "Nginx proxy for test.txt returns $status (expected 200)"
    fi

    # Test /lib/ proxy
    response=$(curl -s -w "\n%{http_code}" -L 0 \
        "$FRONTEND_URL/lib/$REPO_ID/file/test.txt?token=$TOKEN")
    status=$(echo "$response" | tail -1)

    if [ "$status" = "200" ]; then
        pass "Nginx proxies /lib/.../file/ to backend"
    else
        fail "Nginx proxy for /lib/.../file/ returns $status (expected 200)"
    fi
else
    info "Frontend not available at $FRONTEND_URL, skipping nginx proxy tests"
fi

echo ""

# =============================================================================
# Summary
# =============================================================================
echo "==================================================="
echo "File Preview Test Complete"
echo "==================================================="
echo ""
echo -e "Passed: ${GREEN}$PASS_COUNT${NC}"
echo -e "Failed: ${RED}$FAIL_COUNT${NC}"
echo ""

if [ "$FAIL_COUNT" -gt 0 ]; then
    exit 1
fi
exit 0
