#!/bin/bash
# Bootstrap script for SesameFS Development Environment
#
# This script sets up the SesameFS environment with two modes:
#
# DEV MODE (default): Single instance, minimal resources
#   - One SesameFS server
#   - One Cassandra node
#   - One MinIO instance
#   - No nginx (direct access)
#   - Ideal for local development
#
# MULTI-REGION MODE: Full multi-region setup
#   - Two SesameFS servers (USA, EU)
#   - nginx load balancer
#   - Regional S3 buckets
#   - Failover testing
#
# Usage:
#   ./scripts/bootstrap.sh [mode] [options]
#
# Modes:
#   dev          Development mode (default) - single instance
#   multiregion  Multi-region mode - full setup with nginx
#
# Options:
#   --clean      Remove existing volumes and start fresh
#   --down       Stop and remove all containers
#   --status     Show status of all services
#   --help       Show this help message
#
# Examples:
#   ./scripts/bootstrap.sh                  # Start dev mode
#   ./scripts/bootstrap.sh dev              # Start dev mode
#   ./scripts/bootstrap.sh dev --clean      # Clean start dev mode
#   ./scripts/bootstrap.sh multiregion      # Start multi-region mode
#   ./scripts/bootstrap.sh --down           # Stop current mode

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

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

log_mode() {
    echo -e "${CYAN}[MODE]${NC} $1"
}

check_prerequisites() {
    log_info "Checking prerequisites..."

    # Check Docker
    if ! command -v docker &> /dev/null; then
        log_error "Docker is not installed. Please install Docker first."
        exit 1
    fi
    log_success "Docker is installed"

    # Check Docker Compose
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
}

check_multiregion_hosts() {
    # Only check /etc/hosts for multi-region mode
    if ! grep -q "us.sesamefs.local" /etc/hosts 2>/dev/null; then
        log_warning "Hostname entries not found in /etc/hosts"
        echo ""
        echo "  Add these entries for hostname-based routing:"
        echo "    127.0.0.1 us.sesamefs.local eu.sesamefs.local sesamefs.local"
        echo ""
        echo "  Or run: sudo sh -c 'echo \"127.0.0.1 us.sesamefs.local eu.sesamefs.local sesamefs.local\" >> /etc/hosts'"
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
        if docker exec cool-storage-api-cassandra-1 cqlsh localhost -e "DESCRIBE KEYSPACES" &> /dev/null; then
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

init_cassandra_schema() {
    log_info "Initializing Cassandra schema..."

    local container="cool-storage-api-cassandra-1"

    # Create keyspace
    docker exec $container cqlsh localhost -e "CREATE KEYSPACE IF NOT EXISTS sesamefs WITH replication = {'class': 'SimpleStrategy', 'replication_factor': 1};"
    log_success "Keyspace created"

    # Core tables
    docker exec $container cqlsh localhost -e "CREATE TABLE IF NOT EXISTS sesamefs.organizations (org_id UUID PRIMARY KEY, name TEXT, settings MAP<TEXT, TEXT>, storage_quota BIGINT, storage_used BIGINT, chunking_polynomial BIGINT, storage_config MAP<TEXT, TEXT>, created_at TIMESTAMP);"
    docker exec $container cqlsh localhost -e "CREATE TABLE IF NOT EXISTS sesamefs.users (org_id UUID, user_id UUID, email TEXT, name TEXT, role TEXT, oidc_sub TEXT, quota_bytes BIGINT, used_bytes BIGINT, created_at TIMESTAMP, PRIMARY KEY ((org_id), user_id));"
    docker exec $container cqlsh localhost -e "CREATE TABLE IF NOT EXISTS sesamefs.users_by_email (email TEXT PRIMARY KEY, user_id UUID, org_id UUID);"
    docker exec $container cqlsh localhost -e "CREATE TABLE IF NOT EXISTS sesamefs.users_by_oidc (oidc_issuer TEXT, oidc_sub TEXT, user_id UUID, org_id UUID, PRIMARY KEY ((oidc_issuer), oidc_sub));"
    docker exec $container cqlsh localhost -e "CREATE TABLE IF NOT EXISTS sesamefs.libraries (org_id UUID, library_id UUID, owner_id UUID, name TEXT, description TEXT, encrypted BOOLEAN, enc_version INT, magic TEXT, random_key TEXT, root_commit_id TEXT, head_commit_id TEXT, storage_class TEXT, size_bytes BIGINT, file_count BIGINT, version_ttl_days INT, created_at TIMESTAMP, updated_at TIMESTAMP, PRIMARY KEY ((org_id), library_id));"
    docker exec $container cqlsh localhost -e "CREATE TABLE IF NOT EXISTS sesamefs.commits (library_id UUID, commit_id TEXT, parent_id TEXT, root_fs_id TEXT, creator_id UUID, description TEXT, created_at TIMESTAMP, PRIMARY KEY ((library_id), commit_id));"
    docker exec $container cqlsh localhost -e "CREATE TABLE IF NOT EXISTS sesamefs.fs_objects (library_id UUID, fs_id TEXT, obj_type TEXT, obj_name TEXT, dir_entries TEXT, block_ids LIST<TEXT>, size_bytes BIGINT, mtime BIGINT, PRIMARY KEY ((library_id), fs_id));"
    docker exec $container cqlsh localhost -e "CREATE TABLE IF NOT EXISTS sesamefs.blocks (org_id UUID, block_id TEXT, size_bytes INT, storage_class TEXT, storage_key TEXT, ref_count INT, created_at TIMESTAMP, last_accessed TIMESTAMP, PRIMARY KEY ((org_id), block_id));"
    docker exec $container cqlsh localhost -e "CREATE TABLE IF NOT EXISTS sesamefs.block_id_mappings (org_id UUID, external_id TEXT, internal_id TEXT, created_at TIMESTAMP, PRIMARY KEY ((org_id), external_id));"
    docker exec $container cqlsh localhost -e "CREATE TABLE IF NOT EXISTS sesamefs.share_links (share_token TEXT PRIMARY KEY, org_id UUID, library_id UUID, file_path TEXT, created_by UUID, permission TEXT, password_hash TEXT, expires_at TIMESTAMP, download_count INT, max_downloads INT, created_at TIMESTAMP);"
    docker exec $container cqlsh localhost -e "CREATE TABLE IF NOT EXISTS sesamefs.shares (library_id UUID, share_id UUID, shared_by UUID, shared_to UUID, shared_to_type TEXT, permission TEXT, created_at TIMESTAMP, expires_at TIMESTAMP, PRIMARY KEY ((library_id), share_id));"
    docker exec $container cqlsh localhost -e "CREATE TABLE IF NOT EXISTS sesamefs.restore_jobs (org_id UUID, job_id UUID, library_id UUID, block_ids LIST<TEXT>, glacier_job_id TEXT, status TEXT, requested_at TIMESTAMP, completed_at TIMESTAMP, expires_at TIMESTAMP, PRIMARY KEY ((org_id), job_id));"
    docker exec $container cqlsh localhost -e "CREATE TABLE IF NOT EXISTS sesamefs.hostname_mappings (hostname TEXT PRIMARY KEY, org_id UUID, settings MAP<TEXT, TEXT>, created_at TIMESTAMP, updated_at TIMESTAMP);"

    # Handle access_tokens separately due to reserved keyword
    docker exec $container cqlsh localhost -e 'CREATE TABLE IF NOT EXISTS sesamefs.access_tokens ("token" TEXT PRIMARY KEY, token_type TEXT, org_id UUID, repo_id UUID, file_path TEXT, user_id UUID, created_at TIMESTAMP);'

    log_success "All tables created"
}

wait_for_sesamefs() {
    log_info "Waiting for SesameFS to be ready..."

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
    log_warning "Services may still be starting. Check logs with: $DOCKER_COMPOSE logs -f"
}

# ==========================================================================
# DEV MODE
# ==========================================================================

start_dev() {
    local compose_file="docker-compose.yaml"

    log_mode "Starting DEVELOPMENT mode (single instance)"
    echo ""

    log_info "Building SesameFS image..."
    $DOCKER_COMPOSE -f "$compose_file" build sesamefs

    log_info "Starting infrastructure (Cassandra, MinIO)..."
    $DOCKER_COMPOSE -f "$compose_file" up -d cassandra minio

    # Wait for Cassandra
    wait_for_cassandra

    # Initialize schema
    init_cassandra_schema

    log_info "Initializing MinIO buckets..."
    $DOCKER_COMPOSE -f "$compose_file" up -d minio-init
    sleep 3

    log_info "Starting SesameFS..."
    $DOCKER_COMPOSE -f "$compose_file" up -d sesamefs

    wait_for_sesamefs
    show_dev_status
}

show_dev_status() {
    local compose_file="docker-compose.yaml"

    echo ""
    echo "========================================="
    echo "Development Environment Status"
    echo "========================================="
    $DOCKER_COMPOSE -f "$compose_file" ps

    echo ""
    echo "========================================="
    echo "Endpoints"
    echo "========================================="

    echo -n "SesameFS API (localhost:8080): "
    if curl -s http://localhost:8080/ping > /dev/null 2>&1; then
        echo -e "${GREEN}OK${NC}"
    else
        echo -e "${RED}FAIL${NC}"
    fi

    echo -n "MinIO Console (localhost:9001): "
    if curl -s http://localhost:9001 > /dev/null 2>&1; then
        echo -e "${GREEN}OK${NC}"
    else
        echo -e "${YELLOW}STARTING${NC}"
    fi

    echo ""
    echo "========================================="
    echo "Quick Start"
    echo "========================================="
    echo "  Test API:      curl http://localhost:8080/ping"
    echo "  Auth test:     curl http://localhost:8080/api2/account/info/ -H 'Authorization: Token dev-token-123'"
    echo "  View logs:     $DOCKER_COMPOSE logs -f sesamefs"
    echo "  MinIO Console: http://localhost:9001 (minioadmin/minioadmin)"
    echo ""
    echo "  Run locally:   go run ./cmd/sesamefs serve"
    echo "                 (stop sesamefs container first: $DOCKER_COMPOSE stop sesamefs)"
    echo ""
}

stop_dev() {
    local compose_file="docker-compose.yaml"
    log_info "Stopping development environment..."
    $DOCKER_COMPOSE -f "$compose_file" down
    log_success "Development environment stopped"
}

clean_dev() {
    local compose_file="docker-compose.yaml"
    log_info "Removing development environment (including volumes)..."
    $DOCKER_COMPOSE -f "$compose_file" down -v
    log_success "Clean slate ready"
}

# ==========================================================================
# MULTI-REGION MODE
# ==========================================================================

start_multiregion() {
    local compose_file="docker-compose-multiregion.yaml"

    log_mode "Starting MULTI-REGION mode (USA + EU with nginx)"
    echo ""

    check_multiregion_hosts

    log_info "Building SesameFS images..."
    $DOCKER_COMPOSE -f "$compose_file" build

    log_info "Starting infrastructure (Cassandra, MinIO)..."
    $DOCKER_COMPOSE -f "$compose_file" up -d cassandra minio

    # Wait for Cassandra
    wait_for_cassandra

    # Initialize schema
    init_cassandra_schema

    log_info "Initializing MinIO buckets..."
    $DOCKER_COMPOSE -f "$compose_file" up -d minio-init
    sleep 5

    log_info "Starting SesameFS servers (USA, EU)..."
    $DOCKER_COMPOSE -f "$compose_file" up -d sesamefs-usa sesamefs-eu
    sleep 5

    log_info "Starting nginx load balancer..."
    $DOCKER_COMPOSE -f "$compose_file" up -d nginx

    wait_for_sesamefs
    show_multiregion_status
}

show_multiregion_status() {
    local compose_file="docker-compose-multiregion.yaml"

    echo ""
    echo "========================================="
    echo "Multi-Region Environment Status"
    echo "========================================="
    $DOCKER_COMPOSE -f "$compose_file" ps

    echo ""
    echo "========================================="
    echo "Endpoints"
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
    echo "Quick Start"
    echo "========================================="
    echo "  Run tests:     ./scripts/run-tests.sh multiregion all"
    echo "  Failover test: ./scripts/run-tests.sh failover all"
    echo "  View logs:     $DOCKER_COMPOSE -f $compose_file logs -f"
    echo "  MinIO Console: http://localhost:9001 (minioadmin/minioadmin)"
    echo ""
}

stop_multiregion() {
    local compose_file="docker-compose-multiregion.yaml"
    log_info "Stopping multi-region environment..."
    $DOCKER_COMPOSE -f "$compose_file" down
    log_success "Multi-region environment stopped"
}

clean_multiregion() {
    local compose_file="docker-compose-multiregion.yaml"
    log_info "Removing multi-region environment (including volumes)..."
    $DOCKER_COMPOSE -f "$compose_file" down -v
    log_success "Clean slate ready"
}

# ==========================================================================
# HELP
# ==========================================================================

show_help() {
    echo "Bootstrap script for SesameFS Development Environment"
    echo ""
    echo "Usage: $0 [mode] [options]"
    echo ""
    echo "Modes:"
    echo "  dev          Development mode (default) - single instance, minimal resources"
    echo "  multiregion  Multi-region mode - full setup with nginx load balancer"
    echo ""
    echo "Options:"
    echo "  --clean      Remove existing volumes and start fresh"
    echo "  --down       Stop and remove all containers"
    echo "  --status     Show status of all services"
    echo "  --help       Show this help message"
    echo ""
    echo "Examples:"
    echo "  $0                       # Start dev mode"
    echo "  $0 dev                   # Start dev mode"
    echo "  $0 dev --clean           # Clean start dev mode"
    echo "  $0 multiregion           # Start multi-region mode"
    echo "  $0 multiregion --clean   # Clean start multi-region"
    echo "  $0 --down                # Stop all environments"
    echo ""
    echo "Development workflow:"
    echo "  1. Start infrastructure:  $0 dev"
    echo "  2. Stop sesamefs:         docker compose stop sesamefs"
    echo "  3. Run locally:           go run ./cmd/sesamefs serve"
    echo "  4. Make changes, restart Go process"
    echo ""
    echo "Multi-region testing:"
    echo "  1. Start multi-region:    $0 multiregion"
    echo "  2. Run tests:             ./scripts/run-tests.sh multiregion all"
    echo "  3. Test failover:         ./scripts/run-tests.sh failover all"
    echo ""
}

# ==========================================================================
# MAIN
# ==========================================================================

cd "$PROJECT_DIR"

# Parse arguments
MODE="dev"
ACTION="start"

for arg in "$@"; do
    case "$arg" in
        dev|multiregion)
            MODE="$arg"
            ;;
        --clean)
            ACTION="clean"
            ;;
        --down)
            ACTION="down"
            ;;
        --status)
            ACTION="status"
            ;;
        --help|-h)
            show_help
            exit 0
            ;;
    esac
done

# Execute action
echo ""
echo "========================================="
echo "SesameFS Bootstrap"
echo "========================================="
echo ""

check_prerequisites

case "$MODE-$ACTION" in
    dev-start)
        start_dev
        ;;
    dev-clean)
        clean_dev
        start_dev
        ;;
    dev-down)
        stop_dev
        ;;
    dev-status)
        show_dev_status
        ;;
    multiregion-start)
        start_multiregion
        ;;
    multiregion-clean)
        clean_multiregion
        start_multiregion
        ;;
    multiregion-down)
        stop_multiregion
        ;;
    multiregion-status)
        show_multiregion_status
        ;;
    *)
        log_error "Unknown mode/action: $MODE $ACTION"
        show_help
        exit 1
        ;;
esac
