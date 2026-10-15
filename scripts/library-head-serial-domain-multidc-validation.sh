#!/usr/bin/env bash
# Real 3-DC evidence for ISSUE-LIBRARY-HEAD-SERIAL-DOMAIN-01.
#
# Both datacenters stay up. Sessions are opened with default
# SerialConsistency=LOCAL_SERIAL; production HEAD LWTs must still compete in
# global SERIAL, so concurrent advances and initializers have exactly one winner.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

THREE_DC=(docker compose -p sesamefs-cassandra-3dc -f docker-compose.cassandra-3dc.yaml)
# Do not force -p sesamefs: this workspace's .env sets COMPOSE_PROJECT_NAME
# (and WSL host ports). Forcing the default project name recreates that
# stack onto ports already owned by the running project.
DEFAULT_COMPOSE=(docker compose -f docker-compose.yaml)
RUNNER=sesamefs-library-head-serial-domain-3dc-runner
IMAGE=sesamefs-library-head-serial-domain-3dc
NETWORK=
BACKEND_NETWORK=
KEEP=0
DEFAULT_BACKEND_WAS_RUNNING=0

for arg in "$@"; do
	case "$arg" in
		--keep) KEEP=1 ;;
		*) echo "usage: $0 [--keep]" >&2; exit 2 ;;
	esac
done

step() { printf '\033[1m==> %s\033[0m\n' "$*"; }
fail() { printf '\033[31mFAILED: %s\033[0m\n' "$*" >&2; exit 1; }

cleanup() {
	local rc=$?
	set +e
	docker rm -f "$RUNNER" >/dev/null 2>&1 || true
	if [ "$KEEP" -eq 0 ]; then
		if [ "$DEFAULT_BACKEND_WAS_RUNNING" -eq 0 ]; then
			"${DEFAULT_COMPOSE[@]}" stop sesamefs >/dev/null 2>&1 || true
		fi
		"${THREE_DC[@]}" down -v >/dev/null 2>&1 || true
	else
		echo "3-DC fixture left running (--keep)"
	fi
	exit "$rc"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

wait_healthy() {
	local node="$1" status
	for _ in $(seq 1 120); do
		status="$(docker inspect -f '{{.State.Health.Status}}' "sesamefs-cassandra-$node" 2>/dev/null || true)"
		[ "$status" = "healthy" ] && return 0
		sleep 5
	done
	fail "sesamefs-cassandra-$node did not become healthy"
}

wait_gossip_stable() {
	local node="$1" status
	for _ in $(seq 1 60); do
		status="$(docker exec "sesamefs-cassandra-$node" nodetool status 2>/dev/null | grep -c '^UN ' || true)"
		[ "$status" = "3" ] && return 0
		sleep 2
	done
	fail "gossip did not stabilize to 3 UN nodes from dc-$node (last count: $status)"
}

wait_each_quorum_ready() {
	local node="$1"
	for _ in $(seq 1 30); do
		if docker exec "sesamefs-cassandra-$node" cqlsh -e "CONSISTENCY EACH_QUORUM; SELECT * FROM sesamefs.libraries LIMIT 1;" >/dev/null 2>&1; then
			return 0
		fi
		sleep 2
	done
	fail "EACH_QUORUM reads from dc-$node did not become reliable after gossip stabilized"
}

wait_bootstrap() {
	local status
	for _ in $(seq 1 120); do
		status="$(docker inspect -f '{{.State.Status}}:{{.State.ExitCode}}' sesamefs-cassandra-3dc-bootstrap 2>/dev/null || true)"
		[ "$status" = "exited:0" ] && return 0
		case "$status" in
			exited:*) docker logs sesamefs-cassandra-3dc-bootstrap | tail -40; fail "3-DC schema bootstrap failed: $status" ;;
		esac
		sleep 5
	done
	fail "3-DC schema bootstrap did not finish"
}

runner_env() {
	local dc="$1"
	local node="${dc#dc-}"
	shift
	docker exec "$RUNNER" env \
		SESAMEFS_URL=http://sesamefs:8080 \
		CASSANDRA_HOSTS="cassandra-$node:9042" \
		CASSANDRA_LOCAL_DC="$dc" \
		CASSANDRA_KEYSPACE=sesamefs \
		CASSANDRA_REPLICATION_CLASS=NetworkTopologyStrategy \
		CASSANDRA_REPLICATION_DCS=dc-na:1,dc-eu:1,dc-asia:1 \
		W2_POST_HEAD_3DC_HOSTS=dc-na=cassandra-na:9042,dc-eu=cassandra-eu:9042,dc-asia=cassandra-asia:9042 \
		"$@"
}

require_pass() {
	local output="$1" test_name="$2"
	grep -q "^--- PASS: $test_name" <<<"$output" || {
		echo "$output"
		fail "$test_name did not produce a PASS line"
	}
}

pick_running_backend() {
	local name status
	for name in sesamefs-dev-wsl-sesamefs-1 sesamefs-sesamefs-1; do
		status="$(docker inspect -f '{{.State.Status}}' "$name" 2>/dev/null || true)"
		[ "$status" = "running" ] || continue
		printf '%s\n' "$name"
		return 0
	done
	return 1
}

step "Attach to a running SesameFS backend; start the real three-DC Cassandra fixture"
BACKEND_CONTAINER=""
if BACKEND_CONTAINER="$(pick_running_backend)"; then
	DEFAULT_BACKEND_WAS_RUNNING=1
	step "Using already-running backend $BACKEND_CONTAINER (will not recreate it)"
else
	"${DEFAULT_COMPOSE[@]}" up -d --no-recreate sesamefs
	BACKEND_CONTAINER="$("${DEFAULT_COMPOSE[@]}" ps -q sesamefs)"
fi
[ -n "$BACKEND_CONTAINER" ] || fail "could not resolve sesamefs backend container"
BACKEND_NETWORK="$(docker inspect -f '{{range $name, $_ := .NetworkSettings.Networks}}{{$name}}{{end}}' "$BACKEND_CONTAINER")"
[ -n "$BACKEND_NETWORK" ] || fail "could not resolve the SesameFS Docker network"
"${THREE_DC[@]}" up -d
for n in na eu asia; do wait_healthy "$n"; done
wait_bootstrap
NA_CONTAINER="$("${THREE_DC[@]}" ps -q cassandra-na)"
[ -n "$NA_CONTAINER" ] || fail "could not resolve cassandra-na container id"
NETWORK="$(docker inspect -f '{{range $name, $_ := .NetworkSettings.Networks}}{{$name}}{{end}}' "$NA_CONTAINER")"
[ -n "$NETWORK" ] || fail "could not resolve the real three-DC Docker network"

step "Build an isolated Docker Go runner on both networks"
docker build -f Dockerfile.gotest -t "$IMAGE" .
docker rm -f "$RUNNER" >/dev/null 2>&1 || true
docker run -d --name "$RUNNER" --network "$NETWORK" "$IMAGE" sleep 3600 >/dev/null
if [ "$BACKEND_NETWORK" != "$NETWORK" ]; then
	docker network connect "$BACKEND_NETWORK" "$RUNNER"
fi

step "Apply schema through dc-na"
runner_env dc-na env CASSANDRA_HOSTS=cassandra-na:9042 go run ./cmd/sesamefs migrate
for n in na eu asia; do wait_gossip_stable "$n"; done
for n in na eu asia; do wait_each_quorum_ready "$n"; done

step "Race production HEAD advances and initializers under session default LOCAL_SERIAL"
if ! output="$(runner_env dc-na env \
	SESAMEFS_REQUIRE_LIBRARY_HEAD_SERIAL_DOMAIN_EVIDENCE=1 \
	go test -tags integration -count=1 ./internal/integration/ -run '^TestLibraryHeadSerialDomainConcurrentAdvance3DC$|^TestLibraryHeadSerialDomainInitialHead3DC$|^TestEveryEvidenceGateIsWiredIntoTestMain$' -v 2>&1)"; then
	echo "$output"
	fail "library HEAD SERIAL-domain 3-DC evidence failed"
fi
echo "$output"
require_pass "$output" TestLibraryHeadSerialDomainConcurrentAdvance3DC
require_pass "$output" TestLibraryHeadSerialDomainInitialHead3DC
require_pass "$output" TestEveryEvidenceGateIsWiredIntoTestMain

echo
echo "ISSUE-LIBRARY-HEAD-SERIAL-DOMAIN-01 3-DC evidence passed: with session default LOCAL_SERIAL, concurrent v2 HEAD advances and concurrent initializers each produced exactly one global winner."
