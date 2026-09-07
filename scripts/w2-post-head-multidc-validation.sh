#!/usr/bin/env bash
# Real 3-DC evidence for the W2 CreateFileFromBlocks post-HEAD repair boundary.
#
# This is intentionally separate from scripts/x2-multidc-validation.sh: X2/P3
# exercise GC reference/fence consistency, while this flow exercises the
# publication-repair classifier against a HEAD that is written only in dc-eu.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

THREE_DC=(docker compose -f docker-compose.cassandra-3dc.yaml)
DEFAULT_COMPOSE=(docker compose -p sesamefs -f docker-compose.yaml)
RUNNER=sesamefs-w2-post-head-3dc-runner
IMAGE=sesamefs-w2-post-head-3dc
NETWORK=sesamefs-cassandra-3dc_default
KEEP=0

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
	for n in na asia; do
		docker start "sesamefs-cassandra-$n" >/dev/null 2>&1 || true
	done
	for n in na eu asia; do
		docker exec "sesamefs-cassandra-$n" nodetool enablehandoff >/dev/null 2>&1 || true
	done
	docker rm -f "$RUNNER" >/dev/null 2>&1 || true
	if [ "$KEEP" -eq 0 ]; then
		"${THREE_DC[@]}" down -v >/dev/null 2>&1 || true
	else
		echo "3-DC fixture left running (--keep)"
	fi
	exit "$rc"
}
trap cleanup EXIT INT TERM

wait_healthy() {
	local node="$1"
	local status
	for _ in $(seq 1 120); do
		status="$(docker inspect -f '{{.State.Health.Status}}' "sesamefs-cassandra-$node" 2>/dev/null || true)"
		[ "$status" = "healthy" ] && return 0
		sleep 5
	done
	fail "sesamefs-cassandra-$node did not become healthy"
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
	local output="$1"
	local test_name="$2"
	grep -q "^--- PASS: $test_name" <<<"$output" || {
		echo "$output"
		fail "$test_name did not produce a PASS line"
	}
}

step "Start the normal backend and the real three-DC Cassandra fixture"
"${DEFAULT_COMPOSE[@]}" up -d sesamefs
"${THREE_DC[@]}" up -d
for n in na eu asia; do wait_healthy "$n"; done
wait_bootstrap

step "Build an isolated Docker Go runner on both networks"
docker build -f Dockerfile.gotest -t "$IMAGE" .
docker rm -f "$RUNNER" >/dev/null 2>&1 || true
docker run -d --name "$RUNNER" --network "$NETWORK" "$IMAGE" sleep 3600 >/dev/null
docker network connect sesamefs_default "$RUNNER"

step "Apply schema through dc-na"
runner_env dc-na env \
	CASSANDRA_HOSTS=cassandra-na:9042 \
	go run ./cmd/sesamefs migrate

step "Seed a globally visible base HEAD"
if ! seed_output="$(runner_env dc-na env W2_POST_HEAD_SEED_BASE=1 go test -tags integration -count=1 ./internal/integration/ -run '^TestW2PostHeadSeedGlobalBaseFor3DC$' -v 2>&1)"; then
	echo "$seed_output"
	fail "3-DC base HEAD seed test failed"
fi
echo "$seed_output"
require_pass "$seed_output" TestW2PostHeadSeedGlobalBaseFor3DC
ORG="$(sed -n 's/.*W2_POST_HEAD_ORG=\([0-9a-f-]*\).*/\1/p' <<<"$seed_output" | tail -1)"
REPO="$(sed -n 's/.*W2_POST_HEAD_REPO=\([0-9a-f-]*\).*/\1/p' <<<"$seed_output" | tail -1)"
PARENT="$(sed -n 's/.*W2_POST_HEAD_PARENT=\([^ ]*\).*/\1/p' <<<"$seed_output" | tail -1)"
[ -n "$ORG" ] && [ -n "$REPO" ] && [ -n "$PARENT" ] || fail "could not capture seeded W2 3-DC ids"

step "Create a deliberately divergent remote HEAD in dc-eu"
for n in na eu asia; do docker exec "sesamefs-cassandra-$n" nodetool disablehandoff >/dev/null; done
"${THREE_DC[@]}" stop cassandra-na cassandra-asia
if ! remote_output="$(runner_env dc-eu env \
	W2_POST_HEAD_WRITE_REMOTE=1 \
	W2_POST_HEAD_ORG="$ORG" W2_POST_HEAD_REPO="$REPO" W2_POST_HEAD_PARENT="$PARENT" \
	go test -tags integration -count=1 ./internal/integration/ -run '^TestW2PostHeadWriteRemoteCommitFor3DC$' -v 2>&1)"; then
	echo "$remote_output"
	fail "3-DC remote HEAD write test failed"
fi
echo "$remote_output"
require_pass "$remote_output" TestW2PostHeadWriteRemoteCommitFor3DC
COMMIT="$(sed -n 's/.*W2_POST_HEAD_COMMIT=\([a-z0-9-]*\).*/\1/p' <<<"$remote_output" | tail -1)"
[ -n "$COMMIT" ] || fail "could not capture remote W2 3-DC commit id"

"${THREE_DC[@]}" start cassandra-na cassandra-asia
wait_healthy na
wait_healthy asia

step "Run the production repair classifier from blind dc-na"
runner_env dc-na env \
	SESAMEFS_REQUIRE_W2_POST_HEAD_MULTIDC_EVIDENCE=1 \
	W2_POST_HEAD_ORG="$ORG" W2_POST_HEAD_REPO="$REPO" W2_POST_HEAD_PARENT="$PARENT" W2_POST_HEAD_COMMIT="$COMMIT" \
	go test -tags integration -count=1 ./internal/integration/ -run '^TestW2PostHeadRepairDoesNotMisclassifyRemoteHead3DC$' -v

echo
echo "W2 3-DC post-HEAD reachability evidence passed: dc-na stayed locally blind while repair did not authorize cleanup of the dc-eu publication."
