#!/bin/bash
# Run multi-region tests inside a container
#
# This script launches tests inside the Docker network, eliminating the need
# to configure /etc/hosts on your host machine. Tests can access services
# directly by their Docker service names.
#
# Usage:
#   ./scripts/run-tests.sh [test-script] [test-name]
#
# Examples:
#   ./scripts/run-tests.sh                          # Run test-multiregion.sh all
#   ./scripts/run-tests.sh multiregion              # Run test-multiregion.sh all
#   ./scripts/run-tests.sh multiregion connectivity # Run test-multiregion.sh connectivity
#   ./scripts/run-tests.sh failover                 # Run test-failover.sh all
#   ./scripts/run-tests.sh failover setup           # Run test-failover.sh setup
#   ./scripts/run-tests.sh failover upload          # Run test-failover.sh upload

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m'

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="docker-compose-multiregion.yaml"

# Detect docker compose command
if docker compose version &> /dev/null; then
    DOCKER_COMPOSE="docker compose"
else
    DOCKER_COMPOSE="docker-compose"
fi

log_info() { echo -e "${BLUE}[INFO]${NC} $1"; }
log_success() { echo -e "${GREEN}[OK]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

cd "$PROJECT_DIR"

# Parse arguments
TEST_SCRIPT="${1:-multiregion}"
TEST_NAME="${2:-all}"

case "$TEST_SCRIPT" in
    multiregion|multi)
        SCRIPT_PATH="/scripts/test-multiregion.sh"
        ;;
    failover|fail)
        SCRIPT_PATH="/scripts/test-failover.sh"
        ;;
    *)
        echo "Usage: $0 [multiregion|failover] [test-name]"
        echo ""
        echo "Test scripts:"
        echo "  multiregion  - Basic connectivity, upload, routing tests"
        echo "  failover     - Large file upload, failover tests"
        echo ""
        echo "Test names for 'multiregion':"
        echo "  all, connectivity, upload, routing, failover"
        echo ""
        echo "Test names for 'failover':"
        echo "  all, setup, upload, download, upload-fail, recovery, cleanup"
        exit 1
        ;;
esac

# Check if services are running
log_info "Checking if services are running..."
if ! curl -s http://localhost:8080/ping > /dev/null 2>&1; then
    log_error "Services not running. Start with: ./scripts/bootstrap-multiregion.sh"
    exit 1
fi
log_success "Services are running"

# Build test container if needed
log_info "Building test container..."
$DOCKER_COMPOSE -f "$COMPOSE_FILE" --profile test build test-runner > /dev/null 2>&1

# Run tests in container
log_info "Running tests in container..."
echo ""

$DOCKER_COMPOSE -f "$COMPOSE_FILE" --profile test run --rm \
    test-runner \
    bash "$SCRIPT_PATH" "$TEST_NAME"

echo ""
log_success "Test run complete"
