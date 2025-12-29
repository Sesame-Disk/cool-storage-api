#!/bin/bash
# Multi-Region Test Script for SesameFS
#
# This script tests the multi-region setup including:
# - Basic connectivity to each region
# - Block upload/download to different regions
# - Failover when a server goes down
# - Storage routing based on hostname
#
# Prerequisites:
#   1. Add to /etc/hosts:
#      127.0.0.1 us.sesamefs.local eu.sesamefs.local sesamefs.local
#
#   2. Start the multi-region stack:
#      docker-compose -f docker-compose-multiregion.yaml up -d
#
# Usage:
#   ./scripts/test-multiregion.sh [test_name]
#
# Available tests:
#   connectivity  - Test basic connectivity
#   upload        - Test block uploads to each region
#   routing       - Test hostname-based routing
#   failover      - Test failover scenarios
#   all           - Run all tests (default)

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration (use environment variables if set, otherwise defaults for host)
TOKEN="${TOKEN:-dev-token-123}"
BASE_URL="${BASE_URL:-http://localhost:8080}"
USA_URL="${USA_URL:-http://us.sesamefs.local:8080}"
EU_URL="${EU_URL:-http://eu.sesamefs.local:8080}"

# Detect sha command (macOS uses shasum, Linux uses sha1sum/sha256sum)
if command -v sha1sum &> /dev/null; then
    SHA1_CMD="sha1sum"
    SHA256_CMD="sha256sum"
else
    SHA1_CMD="shasum -a 1"
    SHA256_CMD="shasum -a 256"
fi

# Helper functions
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

check_prerequisites() {
    log_info "Checking prerequisites..."

    # Check if running in container (skip hosts check if so)
    if [ -f /.dockerenv ] || grep -q docker /proc/1/cgroup 2>/dev/null; then
        IN_CONTAINER=true
        log_info "Running in container - using service names for routing"
    else
        IN_CONTAINER=false
        # Check if hosts are configured (only needed on host)
        if ! grep -q "us.sesamefs.local" /etc/hosts 2>/dev/null; then
            log_warning "Add to /etc/hosts: 127.0.0.1 us.sesamefs.local eu.sesamefs.local sesamefs.local"
        fi
    fi

    # Check if services are running
    if ! curl -s "$BASE_URL/ping" > /dev/null 2>&1; then
        log_error "Services not running. Start with: docker-compose -f docker-compose-multiregion.yaml up -d"
        exit 1
    fi

    log_success "Prerequisites OK"
}

# ==========================================================================
# CONNECTIVITY TESTS
# ==========================================================================
test_connectivity() {
    echo ""
    echo "=========================================="
    echo "CONNECTIVITY TESTS"
    echo "=========================================="

    # Test nginx
    log_info "Testing nginx (load balancer)..."
    RESP=$(curl -s -o /dev/null -w "%{http_code}" "$BASE_URL/ping")
    if [ "$RESP" = "200" ]; then
        log_success "Nginx is responding"
    else
        log_error "Nginx not responding (HTTP $RESP)"
        return 1
    fi

    # Test USA endpoint
    log_info "Testing USA endpoint..."
    RESP=$(curl -s -o /dev/null -w "%{http_code}" "$USA_URL/ping" 2>/dev/null || echo "000")
    if [ "$RESP" = "200" ]; then
        log_success "USA endpoint responding"
    else
        log_warning "USA endpoint not responding (HTTP $RESP) - check /etc/hosts"
    fi

    # Test EU endpoint
    log_info "Testing EU endpoint..."
    RESP=$(curl -s -o /dev/null -w "%{http_code}" "$EU_URL/ping" 2>/dev/null || echo "000")
    if [ "$RESP" = "200" ]; then
        log_success "EU endpoint responding"
    else
        log_warning "EU endpoint not responding (HTTP $RESP) - check /etc/hosts"
    fi

    # Test auth
    log_info "Testing authentication..."
    RESP=$(curl -s "$BASE_URL/api2/account/info/" -H "Authorization: Token $TOKEN")
    if echo "$RESP" | grep -q "email"; then
        log_success "Authentication working"
    else
        log_error "Authentication failed: $RESP"
        return 1
    fi
}

# ==========================================================================
# UPLOAD TESTS
# ==========================================================================
test_upload() {
    echo ""
    echo "=========================================="
    echo "UPLOAD TESTS"
    echo "=========================================="

    # Create a test repo with unique name
    REPO_NAME="multiregion-test-$(date +%s)"
    log_info "Creating test repository..."
    REPO_RESP=$(curl -s -X POST "$BASE_URL/api2/repos" \
        -H "Authorization: Token $TOKEN" \
        -H "Content-Type: application/json" \
        -d "{\"name\":\"$REPO_NAME\"}")
    REPO_ID=$(echo "$REPO_RESP" | grep -o '"id":"[^"]*"' | cut -d'"' -f4)

    if [ -z "$REPO_ID" ]; then
        log_error "Failed to create repo: $REPO_RESP"
        return 1
    fi
    log_success "Created repo: $REPO_ID"

    # Test data
    TEST_DATA="Hello from multi-region test at $(date)"
    SHA1_HASH=$(echo -n "$TEST_DATA" | $SHA1_CMD | cut -d' ' -f1)
    SHA256_HASH=$(echo -n "$TEST_DATA" | $SHA256_CMD | cut -d' ' -f1)

    log_info "Test data SHA-1: $SHA1_HASH"
    log_info "Test data SHA-256: $SHA256_HASH"

    # Upload block via load balancer (should go to default region)
    log_info "Uploading block via load balancer..."
    RESP=$(curl -s -o /dev/null -w "%{http_code}" -X PUT \
        "$BASE_URL/seafhttp/repo/$REPO_ID/block/$SHA1_HASH" \
        -H "Authorization: Token $TOKEN" \
        -H "Content-Type: application/octet-stream" \
        --data-raw "$TEST_DATA")

    if [ "$RESP" = "200" ]; then
        log_success "Block uploaded via load balancer"
    else
        log_error "Block upload failed (HTTP $RESP)"
        return 1
    fi

    # Retrieve block
    log_info "Retrieving block..."
    RETRIEVED=$(curl -s "$BASE_URL/seafhttp/repo/$REPO_ID/block/$SHA1_HASH" \
        -H "Authorization: Token $TOKEN")

    if [ "$RETRIEVED" = "$TEST_DATA" ]; then
        log_success "Block retrieved correctly"
    else
        log_error "Block data mismatch"
        echo "Expected: $TEST_DATA"
        echo "Got: $RETRIEVED"
        return 1
    fi

    # Check which bucket has the block (skip if in container without docker access)
    log_info "Checking storage location..."
    if [ "$IN_CONTAINER" = "true" ]; then
        echo "  (storage check skipped in container mode)"
    else
        echo ""
        echo "Blocks in USA bucket:"
        docker exec cool-storage-api-minio-1 mc ls local/sesamefs-usa/ 2>/dev/null || \
            docker exec sesamefs-multiregion-minio-1 mc ls local/sesamefs-usa/ 2>/dev/null || \
            echo "  (unable to check)"

        echo ""
        echo "Blocks in EU bucket:"
        docker exec cool-storage-api-minio-1 mc ls local/sesamefs-eu/ 2>/dev/null || \
            docker exec sesamefs-multiregion-minio-1 mc ls local/sesamefs-eu/ 2>/dev/null || \
            echo "  (unable to check)"
    fi
}

# ==========================================================================
# ROUTING TESTS
# ==========================================================================
test_routing() {
    echo ""
    echo "=========================================="
    echo "ROUTING TESTS"
    echo "=========================================="

    # Create repos for each region
    log_info "Creating test repos for routing test..."

    # USA repo
    USA_REPO_NAME="usa-routing-test-$(date +%s)"
    USA_REPO=$(curl -s -X POST "$USA_URL/api2/repos" \
        -H "Authorization: Token $TOKEN" \
        -H "Content-Type: application/json" \
        -d "{\"name\":\"$USA_REPO_NAME\"}" 2>/dev/null | grep -o '"id":"[^"]*"' | cut -d'"' -f4)

    # EU repo
    EU_REPO_NAME="eu-routing-test-$(date +%s)"
    EU_REPO=$(curl -s -X POST "$EU_URL/api2/repos" \
        -H "Authorization: Token $TOKEN" \
        -H "Content-Type: application/json" \
        -d "{\"name\":\"$EU_REPO_NAME\"}" 2>/dev/null | grep -o '"id":"[^"]*"' | cut -d'"' -f4)

    if [ -n "$USA_REPO" ]; then
        log_success "USA repo created: $USA_REPO"

        # Upload to USA
        USA_DATA="USA region data $(date)"
        USA_HASH=$(echo -n "$USA_DATA" | $SHA1_CMD | cut -d' ' -f1)

        RESP=$(curl -s -o /dev/null -w "%{http_code}" -X PUT \
            "$USA_URL/seafhttp/repo/$USA_REPO/block/$USA_HASH" \
            -H "Authorization: Token $TOKEN" \
            -H "Content-Type: application/octet-stream" \
            --data-raw "$USA_DATA" 2>/dev/null)

        if [ "$RESP" = "200" ]; then
            log_success "Block uploaded to USA region"
        else
            log_warning "USA upload failed (HTTP $RESP)"
        fi
    else
        log_warning "Could not create USA repo (check /etc/hosts)"
    fi

    if [ -n "$EU_REPO" ]; then
        log_success "EU repo created: $EU_REPO"

        # Upload to EU
        EU_DATA="EU region data $(date)"
        EU_HASH=$(echo -n "$EU_DATA" | $SHA1_CMD | cut -d' ' -f1)

        RESP=$(curl -s -o /dev/null -w "%{http_code}" -X PUT \
            "$EU_URL/seafhttp/repo/$EU_REPO/block/$EU_HASH" \
            -H "Authorization: Token $TOKEN" \
            -H "Content-Type: application/octet-stream" \
            --data-raw "$EU_DATA" 2>/dev/null)

        if [ "$RESP" = "200" ]; then
            log_success "Block uploaded to EU region"
        else
            log_warning "EU upload failed (HTTP $RESP)"
        fi
    else
        log_warning "Could not create EU repo (check /etc/hosts)"
    fi
}

# ==========================================================================
# FAILOVER TESTS
# ==========================================================================
test_failover() {
    echo ""
    echo "=========================================="
    echo "FAILOVER TESTS"
    echo "=========================================="

    log_warning "Failover tests require manual intervention"
    echo ""
    echo "To test failover:"
    echo ""
    echo "1. Stop USA server:"
    echo "   docker-compose -f docker-compose-multiregion.yaml stop sesamefs-usa"
    echo ""
    echo "2. Test that requests still work (should failover to EU):"
    echo "   curl http://localhost:8080/ping"
    echo "   curl http://localhost:8080/api2/repos/ -H 'Authorization: Token $TOKEN'"
    echo ""
    echo "3. Check nginx logs for failover:"
    echo "   docker-compose -f docker-compose-multiregion.yaml logs nginx"
    echo ""
    echo "4. Restart USA server:"
    echo "   docker-compose -f docker-compose-multiregion.yaml start sesamefs-usa"
    echo ""

    # Automated check
    log_info "Current server status:"
    USA_STATUS=$(curl -s -o /dev/null -w "%{http_code}" "$USA_URL/ping" 2>/dev/null || echo "DOWN")
    EU_STATUS=$(curl -s -o /dev/null -w "%{http_code}" "$EU_URL/ping" 2>/dev/null || echo "DOWN")
    LB_STATUS=$(curl -s -o /dev/null -w "%{http_code}" "$BASE_URL/ping" 2>/dev/null || echo "DOWN")

    echo "  Load Balancer: $LB_STATUS"
    echo "  USA Server: $USA_STATUS"
    echo "  EU Server: $EU_STATUS"
}

# ==========================================================================
# MAIN
# ==========================================================================
main() {
    echo "========================================"
    echo "SesameFS Multi-Region Test Suite"
    echo "========================================"
    echo ""

    TEST="${1:-all}"

    check_prerequisites

    case "$TEST" in
        connectivity)
            test_connectivity
            ;;
        upload)
            test_upload
            ;;
        routing)
            test_routing
            ;;
        failover)
            test_failover
            ;;
        all)
            test_connectivity
            test_upload
            test_routing
            test_failover
            ;;
        *)
            echo "Unknown test: $TEST"
            echo "Available: connectivity, upload, routing, failover, all"
            exit 1
            ;;
    esac

    echo ""
    echo "========================================"
    echo "Tests Complete"
    echo "========================================"
}

main "$@"
