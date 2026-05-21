#!/bin/bash
#
# SesameFS Unified Test Runner
#
# This is the main entry point for running all types of tests.
# It consolidates all test scripts and provides a unified interface.
#
# Usage:
#   ./scripts/test.sh [category] [options]
#
# Categories:
#   api           Run API integration tests (permissions, file-ops, batch, etc.)
#   oidc          Run OIDC authentication tests (config, login, logout, sessions)
#   sync          Run Seafile sync protocol tests plus the active-active desktop conflict proof
#   multiregion   Run multi-region tests (requires multi-region setup)
#   failover      Run failover tests (requires multi-region setup)
#   go            Run Go unit tests
#   go-all        Run all Go tests (unit + integration)
#   go-integration Run Go integration tests (requires backend)
#   frontend      Run frontend tests
#   mobile        Run mobile frontend checks
#   all           Run all applicable tests
#
# Options:
#   --quick       Run quick tests only (skip long-running tests)
#   --verbose     Show detailed output
#   --keep-going  Continue running remaining categories/suites after a failure
#   --list        List available tests without running
#   --help        Show this help message
#
# Examples:
#   ./scripts/test.sh                    # Run API tests (default)
#   ./scripts/test.sh api                # Run API integration tests
#   ./scripts/test.sh api --quick        # Run quick API tests only
#   ./scripts/test.sh sync               # Run sync protocol tests + active-active proof
#   ./scripts/test.sh go                 # Run Go unit tests
#   ./scripts/test.sh all                # Run all tests
#
# Requirements by category:
#   api         - Backend running (docker compose up -d)
#   sync        - Backend on localhost:8080 + seafile-cli container
#   multiregion - Multi-region stack (./scripts/bootstrap.sh multiregion)
#   failover    - Multi-region stack + host docker access
#   go          - Docker compose test profile
#   go-all      - Docker compose test profile + running backend
#   frontend    - Docker compose test profile
#   mobile      - Docker compose test profile
#

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# Counters
TOTAL_SUITES=0
PASSED_SUITES=0
FAILED_SUITES=0

# Options
QUICK_MODE=false
VERBOSE=false
LIST_ONLY=false
COMPOSE_BUILD=true
FAIL_FAST=true

# Parse arguments
CATEGORY=""
for arg in "$@"; do
    case "$arg" in
        --quick)
            QUICK_MODE=true
            ;;
        --verbose|-v)
            VERBOSE=true
            ;;
        --keep-going|--no-fail-fast)
            FAIL_FAST=false
            ;;
        --list)
            LIST_ONLY=true
            ;;
        --no-build)
            COMPOSE_BUILD=false
            ;;
        --fail-fast)
            FAIL_FAST=true
            ;;
        --help|-h)
            head -50 "$0" | grep "^#" | sed 's/^# //' | sed 's/^#//'
            exit 0
            ;;
        -*)
            # Unknown flag, ignore
            ;;
        *)
            # First non-flag argument is the category
            if [ -z "$CATEGORY" ]; then
                CATEGORY="$arg"
            fi
            ;;
    esac
done

# Default category
CATEGORY="${CATEGORY:-api}"

if [ -n "${SESAMEFS_FAIL_FAST:-}" ]; then
    case "${SESAMEFS_FAIL_FAST}" in
        1|true|TRUE|yes|YES)
            FAIL_FAST=true
            ;;
        0|false|FALSE|no|NO)
            FAIL_FAST=false
            ;;
    esac
fi

# Helper functions
log_info() { echo -e "${BLUE}[INFO]${NC} $1"; }
log_success() { echo -e "${GREEN}[PASS]${NC} $1"; }
log_error() { echo -e "${RED}[FAIL]${NC} $1"; }
log_warning() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_section() { echo -e "\n${CYAN}=== $1 ===${NC}\n"; }

cleanup_backend_test_repos() {
    if [ "${SESAMEFS_CLEAN_TEST_REPOS:-0}" != "1" ]; then
        return 0
    fi

    if [ ! -x "$SCRIPT_DIR/cleanup-test-repos.sh" ]; then
        log_warning "cleanup-test-repos.sh not found or not executable; skipping stale repo cleanup"
        return 0
    fi

    log_info "Cleaning stale backend test repositories"
    if ! "$SCRIPT_DIR/cleanup-test-repos.sh" "${SESAMEFS_URL:-http://localhost:3000}"; then
        log_warning "Stale backend test repository cleanup failed; continuing"
    fi
}

cleanup_backend_test_orgs() {
    if [ "${SESAMEFS_CLEAN_TEST_REPOS:-0}" != "1" ]; then
        return 0
    fi

    if [ ! -x "$SCRIPT_DIR/cleanup-test-orgs.sh" ]; then
        log_warning "cleanup-test-orgs.sh not found or not executable; skipping stale org cleanup"
        return 0
    fi

    log_info "Cleaning stale backend test organizations"
    if ! "$SCRIPT_DIR/cleanup-test-orgs.sh"; then
        log_warning "Stale backend test organization cleanup failed; continuing"
    fi
}

cleanup_backend_test_groups() {
    if [ "${SESAMEFS_CLEAN_TEST_REPOS:-0}" != "1" ]; then
        return 0
    fi

    if [ ! -x "$SCRIPT_DIR/cleanup-test-groups.sh" ]; then
        log_warning "cleanup-test-groups.sh not found or not executable; skipping stale group cleanup"
        return 0
    fi

    log_info "Cleaning stale backend test groups"
    if ! "$SCRIPT_DIR/cleanup-test-groups.sh" "${SESAMEFS_URL:-http://localhost:3000}"; then
        log_warning "Stale backend test group cleanup failed; continuing"
    fi
}

cleanup_backend_test_state() {
    cleanup_backend_test_repos
    cleanup_backend_test_orgs
    cleanup_backend_test_groups
}

# Check if a service is available
check_backend() {
    local url="${SESAMEFS_URL:-http://localhost:3000}"
    if curl -s -f "$url/health" > /dev/null 2>&1; then
        return 0
    fi
    return 1
}

check_sync_backend() {
    local url="${SESAMEFS_URL_LOCAL:-http://localhost:8080}"
    if curl -s -f "$url/health" > /dev/null 2>&1; then
        return 0
    fi
    return 1
}

resolve_seafile_cli_container() {
    local container_name="${CLI_CONTAINER:-}"
    local container_id=""

    if [ -n "$container_name" ] && docker ps --format '{{.Names}}' 2>/dev/null | grep -Fxq "$container_name"; then
        echo "$container_name"
        return 0
    fi

    if check_docker_compose; then
        container_id=$(docker compose ps -q seafile-cli 2>/dev/null | head -n1)
        if [ -n "$container_id" ]; then
            container_name=$(docker inspect --format '{{.Name}}' "$container_id" 2>/dev/null | sed 's#^/##')
            if [ -n "$container_name" ]; then
                echo "$container_name"
                return 0
            fi
        fi
    fi

    container_name=$(docker ps --filter 'label=com.docker.compose.service=seafile-cli' --format '{{.Names}}' 2>/dev/null | head -n1)
    if [ -n "$container_name" ]; then
        echo "$container_name"
        return 0
    fi

    container_name=$(docker ps --format '{{.Names}}' 2>/dev/null | grep -E '(^|-)seafile-cli-[0-9]+$' | head -n1 || true)
    if [ -n "$container_name" ]; then
        echo "$container_name"
        return 0
    fi

    return 1
}

check_seafile_cli() {
    local container_name=""
    container_name=$(resolve_seafile_cli_container || true)
    [ -n "$container_name" ]
}

ensure_seafile_cli() {
    # --no-deps: backend readiness is already validated by check_sync_backend
    # before this is called, so we skip recursive `depends_on` startup to keep
    # the host fallback path fast. If seafile-cli ever gains real dependencies,
    # drop --no-deps here.
    local compose_args="--profile debug up -d --no-deps"

    if check_seafile_cli; then
        return 0
    fi

    if ! check_docker_compose; then
        return 1
    fi

    if [ "$COMPOSE_BUILD" = true ]; then
        compose_args="$compose_args --build"
    fi

    log_info "Auto-starting Seafile CLI container"
    if ! docker compose $compose_args seafile-cli; then
        return 1
    fi

    if check_seafile_cli; then
        return 0
    fi

    return 1
}

check_multiregion() {
    if curl -s -f "http://localhost:8082/ping" > /dev/null 2>&1; then
        # Check if nginx is the load balancer (multi-region mode)
        if docker ps --format '{{.Names}}' 2>/dev/null | grep -q "nginx"; then
            return 0
        fi
    fi
    return 1
}

check_go() {
    if ! command -v go &> /dev/null; then
        return 1
    fi

    # Verify the local Go toolchain satisfies go.mod's version requirement.
    # If go.mod requires a newer Go (e.g., 1.25) than what's installed (e.g., 1.22),
    # Go tries to auto-download the toolchain which may fail in restricted envs.
    # GOTOOLCHAIN=local prevents auto-download so we can detect the mismatch.
    cd "$PROJECT_DIR"
    if GOTOOLCHAIN=local go vet ./cmd/sesamefs/... > /dev/null 2>&1; then
        return 0
    fi

    return 1
}

check_cassandra() {
    (echo > /dev/tcp/localhost/9042) 2>/dev/null
    return $?
}

check_minio() {
    curl -s -f "http://localhost:9000/minio/health/live" > /dev/null 2>&1
    return $?
}

check_node() {
    if command -v npm &> /dev/null; then
        return 0
    fi
    return 1
}

check_docker_compose() {
    if docker compose version > /dev/null 2>&1; then
        return 0
    fi
    return 1
}

run_compose_service() {
    local service="$1"
    local name="$2"
    local compose_args="--profile test run --rm"

    TOTAL_SUITES=$((TOTAL_SUITES + 1))

    if [ "$LIST_ONLY" = true ]; then
        echo "  - $name ($service)"
        return 0
    fi

    log_section "Running: $name"

    if [ "$COMPOSE_BUILD" = true ]; then
        compose_args="$compose_args --build"
    fi

    log_info "docker compose $compose_args $service"

    if docker compose $compose_args "$service"; then
        PASSED_SUITES=$((PASSED_SUITES + 1))
        log_success "$name completed"
        return 0
    fi

    FAILED_SUITES=$((FAILED_SUITES + 1))
    log_error "$name failed"
    return 1
}

# Run a test suite
run_suite() {
    local name="$1"
    local script="$2"
    shift 2
    local args="$@"
    local suite_status=0

    TOTAL_SUITES=$((TOTAL_SUITES + 1))

    if [ "$LIST_ONLY" = true ]; then
        echo "  - $name ($script)"
        return 0
    fi

    log_section "Running: $name"

    cleanup_backend_test_state

    if [ -f "$SCRIPT_DIR/$script" ]; then
        if BASE_URL="${SESAMEFS_URL:-http://localhost:3000}" API_URL="${SESAMEFS_URL:-http://localhost:3000}" bash "$SCRIPT_DIR/$script" $args; then
            PASSED_SUITES=$((PASSED_SUITES + 1))
            log_success "$name completed"
        else
            FAILED_SUITES=$((FAILED_SUITES + 1))
            log_error "$name failed"
            suite_status=1
        fi
    else
        log_error "Script not found: $script"
        FAILED_SUITES=$((FAILED_SUITES + 1))
        suite_status=1
    fi

    cleanup_backend_test_state
    return $suite_status
}

run_suite_with_policy() {
    if ! run_suite "$@"; then
        if [ "$FAIL_FAST" = true ]; then
            log_error "Fail-fast enabled; stopping after first failing suite"
            return 1
        fi
    fi
    return 0
}

run_category_with_policy() {
    if ! "$@"; then
        if [ "$FAIL_FAST" = true ]; then
            log_error "Fail-fast enabled; stopping after first failing category"
            return 1
        fi
    fi
    return 0
}

# ==========================================================================
# API Tests - Basic integration tests requiring only backend
# ==========================================================================
run_api_tests() {
    log_section "API Integration Tests"
    local failed_before=$FAILED_SUITES

    if [ "${SESAMEFS_TEST_IN_CONTAINER:-0}" != "1" ] && check_docker_compose; then
        run_compose_service "api-test" "API Integration Tests"
        return $?
    fi

    if ! check_backend; then
        log_error "Backend not available at ${SESAMEFS_URL:-http://localhost:3000}"
        echo ""
        echo "Start the backend with:"
        echo "  docker compose up -d"
        echo ""
        return 1
    fi

    log_success "Backend is available"
    cleanup_backend_test_repos

    # Run test suites
    run_suite_with_policy "Permission System" "test-permissions.sh" || return 1
    run_suite_with_policy "Admin API + Multi-Tenant" "test-admin-api.sh" || return 1
    run_suite_with_policy "File Operations" "test-file-operations.sh" || return 1
    run_suite_with_policy "Batch Operations" "test-batch-operations.sh" || return 1
    run_suite_with_policy "Library Settings" "test-library-settings.sh" || return 1
    local nested_args=""
    [ "$QUICK_MODE" = true ] && nested_args="--quick"
    run_suite_with_policy "Nested Folders" "test-nested-folders.sh" $nested_args || return 1
    local nested_mc_args=""
    [ "$QUICK_MODE" = true ] && nested_mc_args="--quick"
    run_suite_with_policy "Nested Move/Copy" "test-nested-move-copy.sh" $nested_mc_args || return 1
    run_suite_with_policy "Cross-Library Integrity" "test-cross-library-integrity.sh" || return 1
    run_suite_with_policy "Departments" "test-departments.sh" || return 1
    run_suite_with_policy "Admin Panel (Groups + Users)" "test-admin-panel.sh" || return 1
    run_suite_with_policy "Garbage Collection Admin API" "test-gc.sh" || return 1
    run_suite_with_policy "Repo API Tokens" "test-repo-api-tokens.sh" || return 1
    run_suite_with_policy "Directory with_parents" "test-dir-with-parents.sh" || return 1
    run_suite_with_policy "File History API" "test-file-history.sh" || return 1
    run_suite_with_policy "File Preview & Raw Serving" "test-file-preview.sh" || return 1
    run_suite_with_policy "Tag API (Bug Fix)" "test-tags.sh" || return 1
    run_suite_with_policy "Search API (Full Path)" "test-search.sh" || return 1
    run_suite_with_policy "Repo History API" "test-repo-history.sh" || return 1
    run_suite_with_policy "Health, Readiness & Metrics" "test-health.sh" || return 1

    if [ "$QUICK_MODE" = false ]; then
        run_suite_with_policy "Encrypted Library Security" "test-encrypted-library-security.sh" || return 1
    else
        log_info "Skipping encrypted library tests (--quick mode)"
    fi

    [ "$FAILED_SUITES" -eq "$failed_before" ]
}

# ==========================================================================
# Admin API Tests - Role system + multi-tenant
# ==========================================================================
run_admin_tests() {
    log_section "Admin API + Multi-Tenant Tests"

    if ! check_backend; then
        log_error "Backend not available at ${SESAMEFS_URL:-http://localhost:8082}"
        echo ""
        echo "Start the backend with:"
        echo "  docker compose up -d"
        echo ""
        return 1
    fi

    log_success "Backend is available"

    local args=""
    [ "$QUICK_MODE" = true ] && args="--quick"
    [ "$VERBOSE" = true ] && args="$args --verbose"

    run_suite "Admin API + Multi-Tenant" "test-admin-api.sh" $args
}

# ==========================================================================
# Sync Tests - Seafile CLI sync protocol tests and active-active proof
# ==========================================================================
run_sync_tests() {
    log_section "Sync Protocol Tests"

    if [ "${SESAMEFS_TEST_IN_CONTAINER:-0}" != "1" ] && check_docker_compose; then
        local compose_args="--profile test run --rm"
        local aa_args=""
        local failed_before=$FAILED_SUITES

        TOTAL_SUITES=$((TOTAL_SUITES + 1))

        if [ "$LIST_ONLY" = true ]; then
            echo "  - Sync Protocol Tests (sync-test)"
            echo "  - Active-Active Desktop Conflicts (test-sync-active-active.sh)"
            return 0
        fi

        log_section "Running: Sync Protocol Tests"

        if [ "$COMPOSE_BUILD" = true ]; then
            compose_args="$compose_args --build"
        fi

        if [ "$VERBOSE" = true ]; then
            log_info "docker compose $compose_args -e SYNC_TEST_ARGS=--verbose sync-test"
            if docker compose $compose_args -e SYNC_TEST_ARGS=--verbose sync-test; then
                PASSED_SUITES=$((PASSED_SUITES + 1))
                log_success "Sync Protocol Tests completed"
            else
                FAILED_SUITES=$((FAILED_SUITES + 1))
                log_error "Sync Protocol Tests failed"
                if [ "$FAIL_FAST" = true ]; then
                    log_error "Fail-fast enabled; stopping after first failing suite"
                    return 1
                fi
                log_warning "Continuing to Active-Active Desktop Conflicts (--keep-going)"
            fi
        else
            log_info "docker compose $compose_args sync-test"
            if docker compose $compose_args sync-test; then
                PASSED_SUITES=$((PASSED_SUITES + 1))
                log_success "Sync Protocol Tests completed"
            else
                FAILED_SUITES=$((FAILED_SUITES + 1))
                log_error "Sync Protocol Tests failed"
                if [ "$FAIL_FAST" = true ]; then
                    log_error "Fail-fast enabled; stopping after first failing suite"
                    return 1
                fi
                log_warning "Continuing to Active-Active Desktop Conflicts (--keep-going)"
            fi
        fi

        [ "$VERBOSE" = true ] && aa_args="$aa_args --verbose"
        [ "$COMPOSE_BUILD" = false ] && aa_args="$aa_args --no-build"

        if ! run_suite_with_policy "Active-Active Desktop Conflicts" "test-sync-active-active.sh" $aa_args; then
            return 1
        fi

        [ "$FAILED_SUITES" -eq "$failed_before" ]
        return $?
    fi

    local sync_backend_url="${SESAMEFS_URL_LOCAL:-http://localhost:8080}"
    local cli_container=""

    if ! check_sync_backend; then
        log_error "Backend not available at ${sync_backend_url}"
        echo ""
        echo "Start the backend with:"
        echo "  docker compose up -d sesamefs"
        return 1
    fi

    if ! ensure_seafile_cli; then
        log_warning "Seafile CLI container not running"
        echo ""
        echo "Start seafile-cli with:"
        echo "  docker compose --profile debug up -d --build seafile-cli"
        echo ""
        echo "Or skip sync tests with: ./scripts/test.sh api"
        return 1
    fi

    cli_container=$(resolve_seafile_cli_container || true)
    if [ -z "$cli_container" ]; then
        log_error "Unable to resolve the Seafile CLI container name after startup"
        return 1
    fi

    log_success "Seafile CLI container is available: ${cli_container}"

    local args=""
    [ "$VERBOSE" = true ] && args="--verbose"

    CLI_CONTAINER="$cli_container" SESAMEFS_URL_LOCAL="$sync_backend_url" run_suite "Sync Protocol" "test-sync.sh" $args
}

# ==========================================================================
# Multi-Region Tests
# ==========================================================================
run_multiregion_tests() {
    log_section "Multi-Region Tests"
    local failed_before=$FAILED_SUITES

    if ! check_multiregion; then
        log_warning "Multi-region stack not running"
        echo ""
        echo "Start multi-region with:"
        echo "  ./scripts/bootstrap.sh multiregion"
        echo ""
        return 1
    fi

    log_success "Multi-region stack is available"

    run_suite_with_policy "Multi-Region Connectivity" "test-multiregion.sh" "connectivity" || return 1
    run_suite_with_policy "Multi-Region Upload" "test-multiregion.sh" "upload" || return 1
    run_suite_with_policy "Multi-Region Routing" "test-multiregion.sh" "routing" || return 1

    [ "$FAILED_SUITES" -eq "$failed_before" ]
}

# ==========================================================================
# Failover Tests
# ==========================================================================
run_failover_tests() {
    log_section "Failover Tests"
    local failed_before=$FAILED_SUITES

    if ! check_multiregion; then
        log_warning "Multi-region stack not running"
        return 1
    fi

    # Check if running in container (failover tests need host docker access)
    if [ -f /.dockerenv ] || grep -q docker /proc/1/cgroup 2>/dev/null; then
        log_warning "Running in container - failover tests require host execution"
        echo ""
        echo "Run failover tests from host:"
        echo "  ./scripts/test-failover.sh all"
        return 0
    fi

    run_suite_with_policy "Failover Setup" "test-failover.sh" "setup" || return 1
    run_suite_with_policy "Failover Upload" "test-failover.sh" "upload" || return 1

    if [ "$QUICK_MODE" = false ]; then
        run_suite_with_policy "Download Failover" "test-failover.sh" "download" || return 1
        run_suite_with_policy "Upload Failover" "test-failover.sh" "upload-fail" || return 1
        run_suite_with_policy "Recovery" "test-failover.sh" "recovery" || return 1
    fi

    run_suite_with_policy "Failover Cleanup" "test-failover.sh" "cleanup" || return 1

    [ "$FAILED_SUITES" -eq "$failed_before" ]
}

# ==========================================================================
# OIDC Authentication Tests
# ==========================================================================
run_oidc_tests() {
    log_section "OIDC Authentication Tests"

    if [ "${SESAMEFS_TEST_IN_CONTAINER:-0}" != "1" ] && check_docker_compose; then
        run_compose_service "oidc-test" "OIDC Authentication Tests"
        return $?
    fi

    if ! check_backend; then
        log_error "Backend not available at ${SESAMEFS_URL:-http://localhost:3000}"
        return 1
    fi

    log_success "Backend is available"

    local args=""
    [ "$QUICK_MODE" = true ] && args="--quick"
    [ "$VERBOSE" = true ] && args="$args --verbose"

    run_suite "OIDC Authentication" "test-oidc.sh" $args
}

# ==========================================================================
# Go Unit Tests
# ==========================================================================
run_go_tests() {
    log_section "Go Unit Tests"

    if check_docker_compose; then
        run_compose_service "gotest" "Go Unit Tests"
        return $?
    fi

    if check_go; then
        log_info "Running Go tests locally..."
        cd "$PROJECT_DIR"
        if go test ./... -short -cover; then
            PASSED_SUITES=$((PASSED_SUITES + 1))
            log_success "Go tests passed"
        else
            FAILED_SUITES=$((FAILED_SUITES + 1))
            log_error "Go tests failed"
            return 1
        fi
        TOTAL_SUITES=$((TOTAL_SUITES + 1))
    else
        log_info "Go not installed locally, using Docker..."

        # Build and run tests in Docker
        docker build -t sesamefs-gotest -f - "$PROJECT_DIR" << 'EOF'
FROM golang:1.25-alpine
RUN apk add --no-cache git
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
CMD ["go", "test", "./...", "-short", "-cover"]
EOF

        TOTAL_SUITES=$((TOTAL_SUITES + 1))
        if docker run --rm sesamefs-gotest; then
            PASSED_SUITES=$((PASSED_SUITES + 1))
            log_success "Go tests passed (Docker)"
        else
            FAILED_SUITES=$((FAILED_SUITES + 1))
            log_error "Go tests failed (Docker)"
            return 1
        fi
    fi

    return 0
}

# ==========================================================================
# Go Integration Tests (against running backend)
# ==========================================================================
run_go_integration_tests() {
    log_section "Go Integration Tests"

    if check_docker_compose; then
        run_compose_service "go-integration-test" "Go Integration Tests"
        return $?
    fi

    # Check backend (same as other test scripts)
    if ! check_backend; then
        log_error "Backend not available at ${SESAMEFS_URL:-http://localhost:3000}"
        echo ""
        echo "Start the backend with:"
        echo "  docker compose up -d"
        echo ""
        return 1
    fi

    log_success "Backend is available"

    if check_go; then
        log_info "Running Go integration tests locally..."
        cd "$PROJECT_DIR"

        TOTAL_SUITES=$((TOTAL_SUITES + 1))
        if go test -tags integration -v -count=1 -timeout 5m \
            ./internal/integration/...; then
            PASSED_SUITES=$((PASSED_SUITES + 1))
            log_success "Go integration tests passed"
        else
            FAILED_SUITES=$((FAILED_SUITES + 1))
            log_error "Go integration tests failed"
            return 1
        fi
    else
        log_info "Go not installed locally, using Docker..."

        docker build -t sesamefs-gointegration -f - "$PROJECT_DIR" << 'EOF'
FROM golang:1.25-alpine
RUN apk add --no-cache git
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
CMD ["go", "test", "-tags", "integration", "-v", "-count=1", "-timeout", "5m", "./internal/integration/..."]
EOF

        TOTAL_SUITES=$((TOTAL_SUITES + 1))
        if docker run --rm --network host \
            -e SESAMEFS_URL="${SESAMEFS_URL:-http://localhost:3000}" \
            sesamefs-gointegration; then
            PASSED_SUITES=$((PASSED_SUITES + 1))
            log_success "Go integration tests passed (Docker)"
        else
            FAILED_SUITES=$((FAILED_SUITES + 1))
            log_error "Go integration tests failed (Docker)"
            return 1
        fi
    fi

    return 0
}

run_go_all_tests() {
    log_section "All Go Tests"

    if check_docker_compose; then
        run_compose_service "go-all-test" "All Go Tests"
        return $?
    fi

    log_error "docker compose is required to run the full Go test suite"
    return 1
}

# ==========================================================================
# Frontend Tests
# ==========================================================================
run_frontend_tests() {
    log_section "Frontend Tests"

    if check_docker_compose; then
        run_compose_service "frontend-test" "Frontend Tests"
        return $?
    fi

    if ! check_node; then
        log_warning "Node.js/npm not available"
        echo ""
        echo "Install Node.js to run frontend tests, or run in Docker:"
        echo "  docker compose --profile test run --rm --build frontend-test"
        return 1
    fi

    cd "$PROJECT_DIR/frontend"

    if [ ! -d "node_modules" ]; then
        log_info "Installing frontend dependencies..."
        npm install
    fi

    TOTAL_SUITES=$((TOTAL_SUITES + 1))
    if npm test -- --watchAll=false; then
        PASSED_SUITES=$((PASSED_SUITES + 1))
        log_success "Frontend tests passed"
    else
        FAILED_SUITES=$((FAILED_SUITES + 1))
        log_error "Frontend tests failed"
        return 1
    fi

    return 0
}

run_mobile_tests() {
    log_section "Mobile Frontend Checks + Smoke"

    if check_docker_compose; then
        run_compose_service "mobile-test" "Mobile Frontend Checks + Smoke"
        return $?
    fi

    log_error "docker compose is required to run mobile frontend checks plus smoke"
    return 1
}

# ==========================================================================
# List Available Tests
# ==========================================================================
list_tests() {
    echo ""
    echo "Available Test Categories"
    echo "========================="
    echo ""

    echo "api - API Integration Tests (requires: backend)"
    LIST_ONLY=true
    echo "  - Permission System (test-permissions.sh)"
    echo "  - Admin API + Multi-Tenant (test-admin-api.sh)"
    echo "  - File Operations (test-file-operations.sh)"
    echo "  - Batch Operations (test-batch-operations.sh)"
    echo "  - Library Settings (test-library-settings.sh)"
    echo "  - Nested Move/Copy (test-nested-move-copy.sh)"
    echo "  - Departments (test-departments.sh)"
    echo "  - Encrypted Library Security (test-encrypted-library-security.sh)"
    echo "  - Garbage Collection Admin API (test-gc.sh)"
    echo "  - Repo API Tokens (test-repo-api-tokens.sh)"
    echo "  - Directory with_parents (test-dir-with-parents.sh)"
    echo ""

    echo "admin - Admin API + Multi-Tenant Tests (requires: backend)"
    echo "  - Superadmin role validation"
    echo "  - Organization CRUD (superadmin only)"
    echo "  - Tenant admin user management"
    echo "  - Cross-tenant isolation"
    echo "  - Role hierarchy enforcement"
    echo ""

    echo "oidc - OIDC Authentication Tests (requires: backend)"
    echo "  - OIDC Configuration"
    echo "  - Login URL Generation"
    echo "  - Callback Handling"
    echo "  - Logout (Single Logout)"
    echo "  - Session Management"
    echo ""

    echo "sync - Sync Protocol Tests + active-active desktop proof (requires: docker compose for the full path)"
    echo "  - Sync Protocol (test-sync.sh)"
    echo "  - Active-Active Desktop Conflicts (test-sync-active-active.sh)"
    echo ""

    echo "multiregion - Multi-Region Tests (requires: multi-region stack)"
    echo "  - Connectivity (test-multiregion.sh connectivity)"
    echo "  - Upload (test-multiregion.sh upload)"
    echo "  - Routing (test-multiregion.sh routing)"
    echo ""

    echo "failover - Failover Tests (requires: multi-region stack + host docker)"
    echo "  - Setup, Upload, Download Failover, Recovery"
    echo ""

    echo "go - Go Unit Tests (requires: docker compose test profile)"
    echo "  - All packages in internal/"
    echo ""

    echo "go-all - All Go Tests (requires: sesamefs service + docker compose test profile)"
    echo "  - Go unit tests"
    echo "  - Go integration tests"
    echo ""

    echo "go-integration - Go Integration Tests (requires: sesamefs service + docker compose test profile)"
    echo "  - Libraries CRUD (create, rename, delete, list)"
    echo "  - File operations (upload, download, move, copy, delete)"
    echo "  - Permission enforcement (readonly, guest, cross-user isolation)"
    echo "  - Encrypted library support"
    echo ""

    echo "frontend - Frontend Tests (requires: docker compose test profile)"
    echo "  - React component tests"
    echo ""

    echo "mobile - Mobile Frontend Checks (requires: docker compose test profile)"
    echo "  - Typecheck, lint, and Vitest suite"
    echo ""

    echo "all - Run all applicable tests"
    echo ""
}

# ==========================================================================
# Main
# ==========================================================================
main() {
    echo ""
    echo "=========================================="
    echo "SesameFS Test Runner"
    echo "=========================================="
    echo ""

    if [ "$LIST_ONLY" = true ] || [ "$CATEGORY" = "list" ]; then
        list_tests
        exit 0
    fi

    local start_time=$(date +%s)

    case "$CATEGORY" in
        api|integration)
            run_api_tests
            ;;
        admin)
            run_admin_tests
            ;;
        oidc|auth)
            run_oidc_tests
            ;;
        sync)
            run_sync_tests
            ;;
        multiregion|multi)
            run_multiregion_tests
            ;;
        failover)
            run_failover_tests
            ;;
        go|unit)
            run_go_tests
            ;;
        go-all|go-full)
            run_go_all_tests
            ;;
        go-integration|goi)
            run_go_integration_tests
            ;;
        frontend|fe)
            run_frontend_tests
            ;;
        mobile)
            run_mobile_tests
            ;;
        all)
            run_category_with_policy run_api_tests || return 1
            run_category_with_policy run_oidc_tests || return 1
            run_category_with_policy run_go_tests || return 1
            if check_backend; then
                run_category_with_policy run_go_all_tests || return 1
            else
                log_info "Skipping aggregated Go run (backend not available)"
            fi
            run_category_with_policy run_frontend_tests || return 1
            run_category_with_policy run_mobile_tests || return 1
            # Run Go integration tests if backend is available
            if check_backend; then
                run_category_with_policy run_go_integration_tests || return 1
            else
                log_info "Skipping Go integration tests (backend not available)"
            fi
            # Only run these if their prerequisites are met
            if check_docker_compose || check_seafile_cli; then
                run_category_with_policy run_sync_tests || return 1
            else
                log_info "Skipping sync tests (docker compose and seafile-cli are not available)"
            fi
            if check_multiregion; then
                run_category_with_policy run_multiregion_tests || return 1
            else
                log_info "Skipping multiregion tests (stack not running)"
            fi
            ;;
        *)
            log_error "Unknown category: $CATEGORY"
            echo ""
            echo "Run './scripts/test.sh --help' for usage information"
            echo "Run './scripts/test.sh --list' to see available tests"
            exit 1
            ;;
    esac

    local end_time=$(date +%s)
    local duration=$((end_time - start_time))

    # Print summary
    echo ""
    echo "=========================================="
    echo "Test Summary"
    echo "=========================================="
    echo ""
    echo "Total suites:  $TOTAL_SUITES"
    echo -e "Passed:        ${GREEN}$PASSED_SUITES${NC}"
    echo -e "Failed:        ${RED}$FAILED_SUITES${NC}"
    echo "Duration:      ${duration}s"
    echo ""

    if [ $FAILED_SUITES -eq 0 ]; then
        echo -e "${GREEN}All tests passed!${NC}"
        exit 0
    else
        echo -e "${RED}Some tests failed.${NC}"
        exit 1
    fi
}

main "$@"
