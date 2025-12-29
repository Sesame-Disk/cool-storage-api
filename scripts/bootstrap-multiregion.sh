#!/bin/bash
# Bootstrap script for SesameFS Multi-Region Test Environment
#
# This script sets up the complete multi-region environment including:
# - Cassandra database with schema
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

    # Create keyspace
    docker exec cool-storage-api-cassandra-1 cqlsh localhost -e "CREATE KEYSPACE IF NOT EXISTS sesamefs WITH replication = {'class': 'SimpleStrategy', 'replication_factor': 1};"
    log_success "Keyspace created"

    # Create tables
    docker exec cool-storage-api-cassandra-1 cqlsh localhost -e "CREATE TABLE IF NOT EXISTS sesamefs.organizations (org_id UUID PRIMARY KEY, name TEXT, settings MAP<TEXT, TEXT>, storage_quota BIGINT, storage_used BIGINT, chunking_polynomial BIGINT, storage_config MAP<TEXT, TEXT>, created_at TIMESTAMP);"
    docker exec cool-storage-api-cassandra-1 cqlsh localhost -e "CREATE TABLE IF NOT EXISTS sesamefs.users (org_id UUID, user_id UUID, email TEXT, name TEXT, role TEXT, oidc_sub TEXT, quota_bytes BIGINT, used_bytes BIGINT, created_at TIMESTAMP, PRIMARY KEY ((org_id), user_id));"
    docker exec cool-storage-api-cassandra-1 cqlsh localhost -e "CREATE TABLE IF NOT EXISTS sesamefs.users_by_email (email TEXT PRIMARY KEY, user_id UUID, org_id UUID);"
    docker exec cool-storage-api-cassandra-1 cqlsh localhost -e "CREATE TABLE IF NOT EXISTS sesamefs.users_by_oidc (oidc_issuer TEXT, oidc_sub TEXT, user_id UUID, org_id UUID, PRIMARY KEY ((oidc_issuer), oidc_sub));"
    docker exec cool-storage-api-cassandra-1 cqlsh localhost -e "CREATE TABLE IF NOT EXISTS sesamefs.libraries (org_id UUID, library_id UUID, owner_id UUID, name TEXT, description TEXT, encrypted BOOLEAN, enc_version INT, magic TEXT, random_key TEXT, root_commit_id TEXT, head_commit_id TEXT, storage_class TEXT, size_bytes BIGINT, file_count BIGINT, version_ttl_days INT, created_at TIMESTAMP, updated_at TIMESTAMP, PRIMARY KEY ((org_id), library_id));"
    docker exec cool-storage-api-cassandra-1 cqlsh localhost -e "CREATE TABLE IF NOT EXISTS sesamefs.commits (library_id UUID, commit_id TEXT, parent_id TEXT, root_fs_id TEXT, creator_id UUID, description TEXT, created_at TIMESTAMP, PRIMARY KEY ((library_id), commit_id));"
    docker exec cool-storage-api-cassandra-1 cqlsh localhost -e "CREATE TABLE IF NOT EXISTS sesamefs.fs_objects (library_id UUID, fs_id TEXT, obj_type TEXT, obj_name TEXT, dir_entries TEXT, block_ids LIST<TEXT>, size_bytes BIGINT, mtime BIGINT, PRIMARY KEY ((library_id), fs_id));"
    docker exec cool-storage-api-cassandra-1 cqlsh localhost -e "CREATE TABLE IF NOT EXISTS sesamefs.blocks (org_id UUID, block_id TEXT, size_bytes INT, storage_class TEXT, storage_key TEXT, ref_count INT, created_at TIMESTAMP, last_accessed TIMESTAMP, PRIMARY KEY ((org_id), block_id));"
    docker exec cool-storage-api-cassandra-1 cqlsh localhost -e "CREATE TABLE IF NOT EXISTS sesamefs.block_id_mappings (org_id UUID, external_id TEXT, internal_id TEXT, created_at TIMESTAMP, PRIMARY KEY ((org_id), external_id));"
    docker exec cool-storage-api-cassandra-1 cqlsh localhost -e "CREATE TABLE IF NOT EXISTS sesamefs.share_links (share_token TEXT PRIMARY KEY, org_id UUID, library_id UUID, file_path TEXT, created_by UUID, permission TEXT, password_hash TEXT, expires_at TIMESTAMP, download_count INT, max_downloads INT, created_at TIMESTAMP);"
    docker exec cool-storage-api-cassandra-1 cqlsh localhost -e "CREATE TABLE IF NOT EXISTS sesamefs.shares (library_id UUID, share_id UUID, shared_by UUID, shared_to UUID, shared_to_type TEXT, permission TEXT, created_at TIMESTAMP, expires_at TIMESTAMP, PRIMARY KEY ((library_id), share_id));"
    docker exec cool-storage-api-cassandra-1 cqlsh localhost -e "CREATE TABLE IF NOT EXISTS sesamefs.restore_jobs (org_id UUID, job_id UUID, library_id UUID, block_ids LIST<TEXT>, glacier_job_id TEXT, status TEXT, requested_at TIMESTAMP, completed_at TIMESTAMP, expires_at TIMESTAMP, PRIMARY KEY ((org_id), job_id));"
    docker exec cool-storage-api-cassandra-1 cqlsh localhost -e "CREATE TABLE IF NOT EXISTS sesamefs.hostname_mappings (hostname TEXT PRIMARY KEY, org_id UUID, settings MAP<TEXT, TEXT>, created_at TIMESTAMP, updated_at TIMESTAMP);"

    # Handle access_tokens separately due to reserved keyword
    docker exec cool-storage-api-cassandra-1 cqlsh localhost -e 'CREATE TABLE IF NOT EXISTS sesamefs.access_tokens ("token" TEXT PRIMARY KEY, token_type TEXT, org_id UUID, repo_id UUID, file_path TEXT, user_id UUID, created_at TIMESTAMP);'

    log_success "All tables created"
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

    # Wait for Cassandra
    wait_for_cassandra

    # Initialize schema
    init_cassandra_schema

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
