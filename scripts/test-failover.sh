#!/bin/bash
# SesameFS Multi-Region Failover Test Script
#
# This script tests failover scenarios including:
# - Large file upload (1GB)
# - Download failover (stop server mid-download)
# - Upload failover (stop server mid-upload)
# - Recovery after server restart
#
# Usage:
#   ./scripts/test-failover.sh [test_name]
#
# Available tests:
#   setup       - Create test files and repo
#   upload      - Upload 1GB file to test throughput
#   download    - Test download failover
#   upload-fail - Test upload failover
#   recovery    - Test recovery after restart
#   all         - Run all tests (default)
#   cleanup     - Clean up test files

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# Configuration (use environment variables if set, otherwise defaults for host)
TOKEN="${TOKEN:-dev-token-123}"
BASE_URL="${BASE_URL:-http://localhost:8080}"
TEST_DIR="/tmp/sesamefs-failover-test"
CHUNK_SIZE_MB=8
NUM_CHUNKS=128  # 1GB total
COMPOSE_FILE="docker-compose-multiregion.yaml"
PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Detect sha command (macOS uses shasum, Linux uses sha1sum)
if command -v sha1sum &> /dev/null; then
    SHA1_CMD="sha1sum"
else
    SHA1_CMD="shasum -a 1"
fi

# Detect if running in container
if [ -f /.dockerenv ] || grep -q docker /proc/1/cgroup 2>/dev/null; then
    IN_CONTAINER=true
else
    IN_CONTAINER=false
fi

# Helper functions
log_info() { echo -e "${BLUE}[INFO]${NC} $1"; }
log_success() { echo -e "${GREEN}[PASS]${NC} $1"; }
log_error() { echo -e "${RED}[FAIL]${NC} $1"; }
log_warning() { echo -e "${YELLOW}[WARN]${NC} $1"; }

check_services() {
    log_info "Checking services..."
    if ! curl -s "$BASE_URL/ping" > /dev/null 2>&1; then
        log_error "Services not running. Start with: ./scripts/bootstrap-multiregion.sh"
        exit 1
    fi
    log_success "Services are running"

    # Warn if failover tests won't work in container without docker socket
    if [ "$IN_CONTAINER" = "true" ] && ! docker ps > /dev/null 2>&1; then
        log_warning "Docker socket not available - failover tests (download, upload-fail) will be skipped"
    fi
}

# ==========================================================================
# SETUP
# ==========================================================================
test_setup() {
    echo ""
    echo "=========================================="
    echo "SETUP: Creating test files"
    echo "=========================================="

    # Clean up any previous test
    rm -rf "$TEST_DIR"
    mkdir -p "$TEST_DIR"
    cd "$TEST_DIR"

    log_info "Creating $NUM_CHUNKS x ${CHUNK_SIZE_MB}MB chunks ($(($NUM_CHUNKS * $CHUNK_SIZE_MB / 1024))GB total)..."

    for i in $(seq 1 $NUM_CHUNKS); do
        dd if=/dev/urandom of="chunk_$(printf '%03d' $i)" bs=1M count=$CHUNK_SIZE_MB 2>/dev/null
        echo -n "."
    done
    echo ""

    log_success "Created $(ls chunk_* | wc -l | tr -d ' ') chunks"

    # Create test repo with unique name
    REPO_NAME="failover-test-$(date +%s)"
    log_info "Creating test repository..."
    REPO_RESP=$(curl -s -X POST "$BASE_URL/api2/repos" \
        -H "Authorization: Token $TOKEN" \
        -H "Content-Type: application/json" \
        -d "{\"name\":\"$REPO_NAME\"}")

    REPO_ID=$(echo "$REPO_RESP" | grep -o '"id":"[^"]*"' | cut -d'"' -f4)

    if [ -z "$REPO_ID" ]; then
        log_error "Failed to create repo: $REPO_RESP"
        exit 1
    fi

    echo "$REPO_ID" > "$TEST_DIR/repo_id"
    log_success "Created repo: $REPO_ID"
}

# ==========================================================================
# UPLOAD TEST
# ==========================================================================
test_upload() {
    echo ""
    echo "=========================================="
    echo "UPLOAD: Testing 1GB upload throughput"
    echo "=========================================="

    cd "$TEST_DIR"
    REPO_ID=$(cat repo_id 2>/dev/null)

    if [ -z "$REPO_ID" ]; then
        log_error "No repo_id found. Run 'setup' first."
        exit 1
    fi

    local success=0
    local failed=0
    local start_time=$(date +%s)

    for chunk in chunk_*; do
        HASH=$($SHA1_CMD "$chunk" | cut -d' ' -f1)
        RESP=$(curl -s -o /dev/null -w "%{http_code}" -X PUT \
            "$BASE_URL/seafhttp/repo/$REPO_ID/block/$HASH" \
            -H "Authorization: Token $TOKEN" \
            -H "Content-Type: application/octet-stream" \
            --data-binary "@$chunk" \
            --max-time 60)

        if [ "$RESP" = "200" ]; then
            success=$((success + 1))
            echo "$HASH" >> "$TEST_DIR/block_ids.txt"
            echo -n "."
        else
            failed=$((failed + 1))
            echo -n "X"
        fi
    done
    echo ""

    local end_time=$(date +%s)
    local duration=$((end_time - start_time))
    local mb_per_sec=$(echo "scale=2; $NUM_CHUNKS * $CHUNK_SIZE_MB / $duration" | bc 2>/dev/null || echo "?")

    echo ""
    log_info "Upload Results:"
    echo "  Successful: $success / $NUM_CHUNKS"
    echo "  Failed: $failed"
    echo "  Duration: ${duration}s"
    echo "  Throughput: ${mb_per_sec} MB/s"

    if [ $failed -eq 0 ]; then
        log_success "All blocks uploaded successfully"
    else
        log_error "$failed blocks failed"
    fi
}

# ==========================================================================
# DOWNLOAD FAILOVER TEST
# ==========================================================================
test_download_failover() {
    echo ""
    echo "=========================================="
    echo "DOWNLOAD FAILOVER: Stop server mid-download"
    echo "=========================================="

    # This test requires docker access to stop/start containers
    if [ "$IN_CONTAINER" = "true" ]; then
        log_warning "Skipping download failover test (requires host docker access)"
        echo ""
        echo "To run this test, execute from host:"
        echo "  ./scripts/test-failover.sh download"
        return 0
    fi

    cd "$TEST_DIR"
    REPO_ID=$(cat repo_id 2>/dev/null)

    if [ -z "$REPO_ID" ]; then
        log_error "No repo_id found. Run 'setup' and 'upload' first."
        exit 1
    fi

    local success=0
    local failed=0
    local count=0
    local failover_point=$((NUM_CHUNKS / 2))

    log_info "Will stop USA server after $failover_point blocks"
    echo ""

    for chunk in chunk_*; do
        count=$((count + 1))
        HASH=$($SHA1_CMD "$chunk" | cut -d' ' -f1)

        # Download block
        RESP=$(curl -s -o "$TEST_DIR/verify_$chunk" -w "%{http_code}" \
            "$BASE_URL/seafhttp/repo/$REPO_ID/block/$HASH" \
            -H "Authorization: Token $TOKEN" \
            --max-time 30)

        if [ "$RESP" = "200" ]; then
            if diff -q "$chunk" "$TEST_DIR/verify_$chunk" > /dev/null 2>&1; then
                success=$((success + 1))
                echo -n "."
            else
                failed=$((failed + 1))
                echo -n "M"  # Mismatch
            fi
            rm -f "$TEST_DIR/verify_$chunk"
        else
            failed=$((failed + 1))
            echo -n "X"
        fi

        # Stop USA server at midpoint
        if [ $count -eq $failover_point ]; then
            echo ""
            log_warning ">>> STOPPING USA SERVER <<<"
            cd "$PROJECT_DIR"
            docker-compose -f $COMPOSE_FILE stop sesamefs-usa > /dev/null 2>&1
            sleep 2
            log_warning ">>> USA STOPPED, CONTINUING WITH EU <<<"
            cd "$TEST_DIR"
        fi
    done
    echo ""

    # Restart USA
    cd "$PROJECT_DIR"
    docker-compose -f $COMPOSE_FILE start sesamefs-usa > /dev/null 2>&1

    echo ""
    log_info "Download Failover Results:"
    echo "  Verified: $success / $NUM_CHUNKS"
    echo "  Failed: $failed"

    if [ $failed -eq 0 ]; then
        log_success "Download failover test PASSED"
    else
        log_error "Download failover test FAILED ($failed errors)"
    fi
}

# ==========================================================================
# UPLOAD FAILOVER TEST
# ==========================================================================
test_upload_failover() {
    echo ""
    echo "=========================================="
    echo "UPLOAD FAILOVER: Stop server mid-upload"
    echo "=========================================="

    # This test requires docker access to stop/start containers
    if [ "$IN_CONTAINER" = "true" ]; then
        log_warning "Skipping upload failover test (requires host docker access)"
        echo ""
        echo "To run this test, execute from host:"
        echo "  ./scripts/test-failover.sh upload-fail"
        return 0
    fi

    cd "$TEST_DIR"
    REPO_ID=$(cat repo_id 2>/dev/null)

    if [ -z "$REPO_ID" ]; then
        log_error "No repo_id found. Run 'setup' first."
        exit 1
    fi

    # Create new test chunks
    log_info "Creating 64 new chunks for upload failover test..."
    mkdir -p "$TEST_DIR/upload_test"
    cd "$TEST_DIR/upload_test"

    for i in $(seq 1 64); do
        dd if=/dev/urandom of="new_chunk_$(printf '%02d' $i)" bs=1M count=8 2>/dev/null
        echo -n "."
    done
    echo ""

    local success=0
    local failed=0
    local count=0
    local failover_point=32

    log_info "Will stop EU server after $failover_point blocks"
    echo ""

    for chunk in new_chunk_*; do
        count=$((count + 1))
        HASH=$($SHA1_CMD "$chunk" | cut -d' ' -f1)

        RESP=$(curl -s -o /dev/null -w "%{http_code}" -X PUT \
            "$BASE_URL/seafhttp/repo/$REPO_ID/block/$HASH" \
            -H "Authorization: Token $TOKEN" \
            -H "Content-Type: application/octet-stream" \
            --data-binary "@$chunk" \
            --max-time 60)

        if [ "$RESP" = "200" ]; then
            success=$((success + 1))
            echo -n "."
        else
            failed=$((failed + 1))
            echo -n "X"
        fi

        # Stop EU server at midpoint
        if [ $count -eq $failover_point ]; then
            echo ""
            log_warning ">>> STOPPING EU SERVER <<<"
            cd "$PROJECT_DIR"
            docker-compose -f $COMPOSE_FILE stop sesamefs-eu > /dev/null 2>&1
            sleep 2
            log_warning ">>> EU STOPPED, CONTINUING WITH USA <<<"
            cd "$TEST_DIR/upload_test"
        fi
    done
    echo ""

    # Restart EU
    cd "$PROJECT_DIR"
    docker-compose -f $COMPOSE_FILE start sesamefs-eu > /dev/null 2>&1

    # Cleanup
    rm -rf "$TEST_DIR/upload_test"

    echo ""
    log_info "Upload Failover Results:"
    echo "  Uploaded: $success / 64"
    echo "  Failed: $failed"

    if [ $failed -eq 0 ]; then
        log_success "Upload failover test PASSED"
    else
        log_error "Upload failover test FAILED ($failed errors)"
    fi
}

# ==========================================================================
# RECOVERY TEST
# ==========================================================================
test_recovery() {
    echo ""
    echo "=========================================="
    echo "RECOVERY: Test after server restart"
    echo "=========================================="

    cd "$TEST_DIR"
    REPO_ID=$(cat repo_id 2>/dev/null)

    # Verify both servers are running
    log_info "Waiting for servers to be ready..."
    sleep 5

    # Test a few blocks
    log_info "Verifying blocks are accessible..."

    local verified=0
    local count=0

    for chunk in chunk_*; do
        count=$((count + 1))
        [ $count -gt 10 ] && break  # Just test first 10

        HASH=$($SHA1_CMD "$chunk" | cut -d' ' -f1)
        RESP=$(curl -s -o "$TEST_DIR/verify_$chunk" -w "%{http_code}" \
            "$BASE_URL/seafhttp/repo/$REPO_ID/block/$HASH" \
            -H "Authorization: Token $TOKEN" \
            --max-time 10)

        if [ "$RESP" = "200" ] && diff -q "$chunk" "$TEST_DIR/verify_$chunk" > /dev/null 2>&1; then
            verified=$((verified + 1))
            echo -n "."
        else
            echo -n "X"
        fi
        rm -f "$TEST_DIR/verify_$chunk"
    done
    echo ""

    if [ $verified -eq 10 ]; then
        log_success "Recovery test PASSED: All blocks verified"
    else
        log_error "Recovery test: Only $verified/10 blocks verified"
    fi
}

# ==========================================================================
# CLEANUP
# ==========================================================================
test_cleanup() {
    echo ""
    echo "=========================================="
    echo "CLEANUP"
    echo "=========================================="

    log_info "Removing test files..."
    rm -rf "$TEST_DIR"
    log_success "Cleanup complete"
}

# ==========================================================================
# MAIN
# ==========================================================================
main() {
    echo ""
    echo "========================================"
    echo "SesameFS Failover Test Suite"
    echo "========================================"
    echo ""

    TEST="${1:-all}"
    cd "$PROJECT_DIR"

    check_services

    case "$TEST" in
        setup)
            test_setup
            ;;
        upload)
            test_upload
            ;;
        download)
            test_download_failover
            ;;
        upload-fail)
            test_upload_failover
            ;;
        recovery)
            test_recovery
            ;;
        cleanup)
            test_cleanup
            ;;
        all)
            test_setup
            test_upload
            test_download_failover
            test_upload_failover
            test_recovery
            test_cleanup
            ;;
        *)
            echo "Usage: $0 [setup|upload|download|upload-fail|recovery|cleanup|all]"
            exit 1
            ;;
    esac

    echo ""
    echo "========================================"
    echo "Tests Complete"
    echo "========================================"
}

main "$@"
