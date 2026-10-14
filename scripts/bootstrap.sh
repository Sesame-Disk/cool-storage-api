#!/bin/bash
# Bootstrap script for SesameFS development and multi-region test environments.
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
#   --sandbox    Multi-region only: also apply
#                docker-compose.multiregion.sandbox.yaml (baked region/nginx
#                configs, LB on 8081, USA on 3001). Off by default; needed only
#                on hosts whose Docker daemon cannot bind-mount workspace files
#                and where 8080/3000 are already taken.
#   --help       Show this help message
#
# Examples:
#   ./scripts/bootstrap.sh                     # Start dev mode
#   ./scripts/bootstrap.sh dev                 # Start dev mode
#   ./scripts/bootstrap.sh dev --clean         # Clean start dev mode
#   ./scripts/bootstrap.sh multiregion         # Start multi-region mode
#   ./scripts/bootstrap.sh multiregion --sandbox  # Sandbox host
#   ./scripts/bootstrap.sh --down              # Stop current mode
#
# Schema/bootstrap contract:
#   - `cassandra-bootstrap` converges Cassandra auth/keyspace/replication.
#   - SesameFS applies the embedded CQL migrations on startup.
#   - This wrapper intentionally does not carry ad hoc table DDL.

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

# --------------------------------------------------------------------------
# Multi-region compose selection
# --------------------------------------------------------------------------
# --sandbox is OFF by default: the stock multi-region stack is unchanged unless
# the flag is passed. When it is, select_multiregion_compose() layers
# $SANDBOX_OVERRIDE on docker-compose-multiregion.yaml for hosts whose Docker
# daemon cannot bind-mount workspace files and where 8080/3000 are taken.
SANDBOX=0
SANDBOX_OVERRIDE="docker-compose.multiregion.sandbox.yaml"
MR_COMPOSE_ARGS=()
MR_LB_PORT="8080"

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
    if [ "$SANDBOX" = 1 ]; then
        log_info "Sandbox mode: route with -H 'Host: us.sesamefs.local'; /etc/hosts is not required"
        return 0
    fi
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

# Sandbox mode builds the server images with 'FROM --platform=$BUILDPLATFORM',
# which the classic builder cannot resolve (it fails as "failed to parse
# platform"). Fail with the remedy instead.
check_sandbox_prerequisites() {
    if docker buildx version > /dev/null 2>&1; then
        return 0
    fi

    log_error "Sandbox mode needs the docker buildx plugin (BuildKit)"
    echo "  The sandbox images build with 'FROM --platform=\$BUILDPLATFORM', which the"
    echo "  classic builder cannot resolve; without buildx the build fails as"
    echo "  'failed to parse platform: \"\" is an invalid OS component'."
    echo "  Install it from https://docs.docker.com/go/buildx/ into"
    echo "  ~/.docker/cli-plugins/docker-buildx."
    exit 1
}

wait_for_cassandra() {
    # The compose selection (-f pairs) is forwarded to the docker call.
    log_info "Waiting for Cassandra to be healthy..."

    local retries=60
    local count=0

    while [ $count -lt $retries ]; do
        if $DOCKER_COMPOSE "$@" exec -T cassandra /bin/sh -ec 'cqlsh -u cassandra -p "${CASSANDRA_SUPERUSER_PASSWORD:-cassandra}" -e "describe keyspaces" >/dev/null 2>&1 || cqlsh -u cassandra -p cassandra -e "describe keyspaces" >/dev/null 2>&1'; then
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
    # The compose selection (-f pairs) is forwarded to the docker call.
    log_info "Running canonical Cassandra bootstrap..."
    $DOCKER_COMPOSE "$@" up --no-deps cassandra-bootstrap
    log_success "Cassandra bootstrap completed"
}

wait_for_http_endpoint() {
    local url="$1"
    local label="$2"

    log_info "Waiting for $label to be ready..."

    local retries=30
    local count=0

    while [ $count -lt $retries ]; do
        if curl -fsS "$url" > /dev/null 2>&1; then
            log_success "$label is responding"
            return 0
        fi
        count=$((count + 1))
        echo -n "."
        sleep 2
    done

    echo ""
    log_warning "$label may still be starting. Check logs with: $DOCKER_COMPOSE logs -f"
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
    wait_for_cassandra -f "$compose_file"

    run_cassandra_bootstrap -f "$compose_file"

    log_info "Initializing MinIO buckets..."
    $DOCKER_COMPOSE -f "$compose_file" up -d minio-init
    sleep 3

    log_info "Starting SesameFS..."
    $DOCKER_COMPOSE -f "$compose_file" up -d sesamefs

    wait_for_http_endpoint "http://localhost:${SESAMEFS_HOST_PORT:-8080}/ping" "SesameFS"
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

    local sesamefs_host_port="${SESAMEFS_HOST_PORT:-8080}"

    echo -n "SesameFS API (localhost:${sesamefs_host_port}): "
    if curl -fsS "http://localhost:${sesamefs_host_port}/ping" > /dev/null 2>&1; then
        echo -e "${GREEN}OK${NC}"
    else
        echo -e "${RED}FAIL${NC}"
    fi

    echo -n "MinIO Console (localhost:9001): "
    if curl -fsS http://localhost:9001 > /dev/null 2>&1; then
        echo -e "${GREEN}OK${NC}"
    else
        echo -e "${YELLOW}STARTING${NC}"
    fi

    echo ""
    echo "========================================="
    echo "Quick Start"
    echo "========================================="
    echo "  Test API:      curl http://localhost:${sesamefs_host_port}/ping"
    echo "  Auth test:     curl http://localhost:${sesamefs_host_port}/api2/account/info/ -H 'Authorization: Token dev-token-123'"
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

# Compose selection for multi-region mode: the stock stack, plus the sandbox
# override when --sandbox was passed. Off by default.
select_multiregion_compose() {
    MR_COMPOSE_ARGS=(-f docker-compose-multiregion.yaml)
    MR_LB_PORT="${MR_NGINX_HOST_PORT:-8080}"

    if [ "$SANDBOX" = 1 ]; then
        if [ ! -f "$SANDBOX_OVERRIDE" ]; then
            log_error "Sandbox override not found: $SANDBOX_OVERRIDE"
            exit 1
        fi
        MR_COMPOSE_ARGS+=(-f "$SANDBOX_OVERRIDE")
        MR_LB_PORT="${MR_NGINX_HOST_PORT:-8081}"
        # Host 3000 is taken on sandbox hosts and the base file defaults there.
        # (EU keeps its base default, 4001.)
        export SESAMEFS_USA_HOST_PORT="${SESAMEFS_USA_HOST_PORT:-3001}"
        log_mode "Sandbox overrides enabled: $SANDBOX_OVERRIDE (LB on ${MR_LB_PORT}, USA on ${SESAMEFS_USA_HOST_PORT})"
    fi
}

start_multiregion() {
    log_mode "Starting MULTI-REGION mode (USA + EU with nginx)"
    echo ""

    check_multiregion_hosts

    if [ "$SANDBOX" = 1 ]; then
        check_sandbox_prerequisites
    fi

    log_info "Building SesameFS images..."
    $DOCKER_COMPOSE "${MR_COMPOSE_ARGS[@]}" build

    log_info "Starting infrastructure (Cassandra, MinIO)..."
    $DOCKER_COMPOSE "${MR_COMPOSE_ARGS[@]}" up -d cassandra minio

    # Wait for Cassandra
    wait_for_cassandra "${MR_COMPOSE_ARGS[@]}"

    run_cassandra_bootstrap "${MR_COMPOSE_ARGS[@]}"

    log_info "Initializing MinIO buckets..."
    $DOCKER_COMPOSE "${MR_COMPOSE_ARGS[@]}" up -d minio-init
    sleep 5

    log_info "Starting SesameFS servers (USA, EU)..."
    $DOCKER_COMPOSE "${MR_COMPOSE_ARGS[@]}" up -d sesamefs-usa sesamefs-eu
    sleep 5

    log_info "Starting nginx load balancer..."
    $DOCKER_COMPOSE "${MR_COMPOSE_ARGS[@]}" up -d nginx

    wait_for_http_endpoint "http://localhost:${MR_LB_PORT}/ping" "Load Balancer"
    show_multiregion_status
}

show_multiregion_status() {
    echo ""
    echo "========================================="
    echo "Multi-Region Environment Status"
    echo "========================================="
    $DOCKER_COMPOSE "${MR_COMPOSE_ARGS[@]}" ps

    echo ""
    echo "========================================="
    echo "Endpoints"
    echo "========================================="

    # Route with a Host header so the checks do not depend on /etc/hosts.
    echo -n "Load Balancer (localhost:${MR_LB_PORT}): "
    if curl -fsS "http://localhost:${MR_LB_PORT}/ping" > /dev/null 2>&1; then
        echo -e "${GREEN}OK${NC}"
    else
        echo -e "${RED}FAIL${NC}"
    fi

    echo -n "USA Endpoint (Host: us.sesamefs.local): "
    if curl -fsS -H "Host: us.sesamefs.local" "http://localhost:${MR_LB_PORT}/ping" > /dev/null 2>&1; then
        echo -e "${GREEN}OK${NC}"
    else
        echo -e "${RED}FAIL${NC}"
    fi

    echo -n "EU Endpoint (Host: eu.sesamefs.local): "
    if curl -fsS -H "Host: eu.sesamefs.local" "http://localhost:${MR_LB_PORT}/ping" > /dev/null 2>&1; then
        echo -e "${GREEN}OK${NC}"
    else
        echo -e "${RED}FAIL${NC}"
    fi

    echo ""
    echo "========================================="
    echo "Quick Start"
    echo "========================================="
    echo "  Run tests:     ./scripts/run-tests.sh multiregion all"
    echo "  Failover test: ./scripts/run-tests.sh failover all"
    echo "  View logs:     $DOCKER_COMPOSE ${MR_COMPOSE_ARGS[*]} logs -f"
    echo "  MinIO Console: http://localhost:9001 (minioadmin/minioadmin)"
    echo ""
}

stop_multiregion() {
    log_info "Stopping multi-region environment..."
    $DOCKER_COMPOSE "${MR_COMPOSE_ARGS[@]}" down
    log_success "Multi-region environment stopped"
}

clean_multiregion() {
    log_info "Removing multi-region environment (including volumes)..."
    $DOCKER_COMPOSE "${MR_COMPOSE_ARGS[@]}" down -v
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
    echo "  --sandbox    Multi-region only: also apply $SANDBOX_OVERRIDE"
    echo "               (baked region/nginx configs, LB on 8081, USA on 3001)."
    echo "               Off by default; for hosts whose Docker daemon cannot"
    echo "               bind-mount workspace files and where 8080/3000 are taken."
    echo "  --help       Show this help message"
    echo ""
    echo "Examples:"
    echo "  $0                             # Start dev mode"
    echo "  $0 dev                         # Start dev mode"
    echo "  $0 dev --clean                 # Clean start dev mode"
    echo "  $0 multiregion                 # Start multi-region mode"
    echo "  $0 multiregion --sandbox       # Multi-region on a sandbox host"
    echo "  $0 multiregion --clean         # Clean start multi-region"
    echo "  $0 --down                      # Stop all environments"
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
        --sandbox)
            SANDBOX=1
            ;;
        --help|-h)
            show_help
            exit 0
            ;;
    esac
done

# Resolve the multi-region compose selection (stock stack, plus the sandbox
# override when --sandbox was passed).
if [ "$MODE" = "multiregion" ]; then
    select_multiregion_compose
elif [ "$SANDBOX" = 1 ]; then
    log_warning "--sandbox applies to multi-region mode only; ignoring it for dev mode"
fi

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
