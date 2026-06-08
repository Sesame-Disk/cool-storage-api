#!/bin/bash
# Bootstrap script for SesameFS multi-region test environment.
#
# This script sets up the complete multi-region environment including:
# - Cassandra keyspace/auth bootstrap via `cassandra-bootstrap`
# - MinIO S3-compatible storage with regional buckets
# - Two SesameFS servers (USA and EU regions)
# - nginx load balancer
#
# Usage:
#   ./scripts/bootstrap-multiregion.sh [options]
#
# Options:
#   --clean    Remove existing volumes and start fresh
#   --down     Stop and remove all containers
#   --status   Show status of all services
#   --help     Show this help message

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

COMPOSE_FILE="docker-compose-multiregion.yaml"
PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Detect docker compose command
if docker compose version &> /dev/null; then
    DOCKER_COMPOSE="docker compose"
elif docker-compose version &> /dev/null; then
    DOCKER_COMPOSE="docker-compose"
else
    DOCKER_COMPOSE="docker-compose"  # fallback
fi

# Helper functions
log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[OK]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

check_prerequisites() {
    log_info "Checking prerequisites..."

    # Check Docker
    if ! command -v docker &> /dev/null; then
        log_error "Docker is not installed. Please install Docker first."
        exit 1
    fi
    log_success "Docker is installed"

    # Check Docker Compose (try both docker compose and docker-compose)
    if docker compose version &> /dev/null; then
        DOCKER_COMPOSE="docker compose"
    elif docker-compose version &> /dev/null; then
        DOCKER_COMPOSE="docker-compose"
    else
        log_error "Docker Compose is not available. Please install Docker Compose."
        exit 1
    fi
    log_success "Docker Compose is available ($DOCKER_COMPOSE)"

    # Check if Docker daemon is running
    if ! docker info &> /dev/null; then
        log_error "Docker daemon is not running. Please start Docker."
        exit 1
    fi
    log_success "Docker daemon is running"

    # Check /etc/hosts
    if ! grep -q "us.sesamefs.local" /etc/hosts 2>/dev/null; then
        log_warning "Hostname entries not found in /etc/hosts"
        echo ""
        echo "  Add these entries to /etc/hosts for hostname-based routing:"
        echo "    127.0.0.1 us.sesamefs.local eu.sesamefs.local sesamefs.local"
        echo ""
        echo "  Run: sudo sh -c 'echo \"127.0.0.1 us.sesamefs.local eu.sesamefs.local sesamefs.local\" >> /etc/hosts'"
        echo ""
    else
        log_success "Hostname entries found in /etc/hosts"
    fi
}

wait_for_cassandra() {
    log_info "Waiting for Cassandra to be healthy..."

    local retries=60
    local count=0

    while [ $count -lt $retries ]; do
        if $DOCKER_COMPOSE -f "$COMPOSE_FILE" exec -T cassandra cqlsh localhost -e "DESCRIBE KEYSPACES" &> /dev/null; then
            log_success "Cassandra is ready"
            return 0
        fi
        count=$((count + 1))
        echo -n "."
        sleep 2
    done

    echo ""
    log_error "Cassandra failed to start within timeout"
    return 1
}

run_cassandra_bootstrap() {
    log_info "Running canonical Cassandra bootstrap..."
    $DOCKER_COMPOSE -f "$COMPOSE_FILE" up --no-deps cassandra-bootstrap
    log_success "Cassandra bootstrap completed"
}

wait_for_services() {
    log_info "Waiting for SesameFS servers to be ready..."

    local retries=30
    local count=0

    while [ $count -lt $retries ]; do
        if curl -s http://localhost:8080/ping > /dev/null 2>&1; then
            log_success "SesameFS is responding"
            return 0
        fi
        count=$((count + 1))
        echo -n "."
        sleep 2
    done

    echo ""
    log_warning "Services may still be starting. Check logs with: docker-compose -f $COMPOSE_FILE logs -f"
}

show_status() {
    echo ""
    echo "========================================="
    echo "Service Status"
    echo "========================================="
    $DOCKER_COMPOSE -f "$COMPOSE_FILE" ps

    echo ""
    echo "========================================="
    echo "Endpoint Tests"
    echo "========================================="

    echo -n "Load Balancer (localhost:8080): "
    if curl -s http://localhost:8080/ping > /dev/null 2>&1; then
        echo -e "${GREEN}OK${NC}"
    else
        echo -e "${RED}FAIL${NC}"
    fi

    echo -n "USA Endpoint (us.sesamefs.local:8080): "
    if curl -s http://us.sesamefs.local:8080/ping > /dev/null 2>&1; then
        echo -e "${GREEN}OK${NC}"
    else
        echo -e "${YELLOW}FAIL (check /etc/hosts)${NC}"
    fi

    echo -n "EU Endpoint (eu.sesamefs.local:8080): "
    if curl -s http://eu.sesamefs.local:8080/ping > /dev/null 2>&1; then
        echo -e "${GREEN}OK${NC}"
    else
        echo -e "${YELLOW}FAIL (check /etc/hosts)${NC}"
    fi

    echo ""
    echo "========================================="
    echo "Useful Commands"
    echo "========================================="
    echo "  View logs:     docker compose -f $COMPOSE_FILE logs -f"
    echo "  Run tests:     ./scripts/test-multiregion.sh all"
    echo "  Stop stack:    ./scripts/bootstrap-multiregion.sh --down"
    echo "  MinIO Console: http://localhost:9001 (minioadmin/minioadmin)"
    echo ""
}

start_infrastructure() {
    log_info "Building SesameFS images..."
    $DOCKER_COMPOSE -f "$COMPOSE_FILE" build

    log_info "Starting infrastructure (Cassandra, MinIO)..."
    $DOCKER_COMPOSE -f "$COMPOSE_FILE" up -d cassandra minio

    wait_for_cassandra

    run_cassandra_bootstrap

    log_info "Starting MinIO initialization..."
    $DOCKER_COMPOSE -f "$COMPOSE_FILE" up -d minio-init
    sleep 5

    log_info "Starting SesameFS servers..."
    $DOCKER_COMPOSE -f "$COMPOSE_FILE" up -d sesamefs-usa sesamefs-eu
    sleep 5

    log_info "Starting nginx load balancer..."
    $DOCKER_COMPOSE -f "$COMPOSE_FILE" up -d nginx

    wait_for_services
}

stop_services() {
    log_info "Stopping all services..."
    $DOCKER_COMPOSE -f "$COMPOSE_FILE" down
    log_success "All services stopped"
}

clean_start() {
    log_info "Removing existing volumes and containers..."
    $DOCKER_COMPOSE -f "$COMPOSE_FILE" down -v
    log_success "Clean slate ready"
}

show_help() {
    echo "Bootstrap script for SesameFS Multi-Region Test Environment"
    echo ""
    echo "Usage: $0 [options]"
    echo ""
    echo "Options:"
    echo "  (none)     Start the multi-region environment"
    echo "  --clean    Remove existing volumes and start fresh"
    echo "  --down     Stop and remove all containers"
    echo "  --status   Show status of all services"
    echo "  --help     Show this help message"
    echo ""
    echo "After starting, test with:"
    echo "  curl http://localhost:8080/ping"
    echo "  ./scripts/test-multiregion.sh all"
}

# Main
cd "$PROJECT_DIR"

case "${1:-}" in
    --help|-h)
        show_help
        exit 0
        ;;
    --down)
        stop_services
        exit 0
        ;;
    --clean)
        clean_start
        check_prerequisites
        start_infrastructure
        show_status
        ;;
    --status)
        show_status
        exit 0
        ;;
    *)
        echo ""
        echo "========================================="
        echo "SesameFS Multi-Region Bootstrap"
        echo "========================================="
        echo ""
        check_prerequisites
        start_infrastructure
        show_status
        ;;
esac
