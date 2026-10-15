#!/usr/bin/env bash
# Real 3-DC evidence for the W2 CreateFileFromBlocks post-HEAD repair boundary.
#
# This is intentionally separate from scripts/x2-multidc-validation.sh: X2/P3
# exercise GC reference/fence consistency, while this flow exercises the
# publication-repair classifier against a HEAD that is written only in dc-eu.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

THREE_DC=(docker compose -f docker-compose.cassandra-3dc.yaml)
DEFAULT_COMPOSE_PROJECT="${COMPOSE_PROJECT_NAME:-sesamefs}"
DEFAULT_COMPOSE=(docker compose -p "$DEFAULT_COMPOSE_PROJECT" -f docker-compose.yaml)
RUNNER=sesamefs-w2-post-head-3dc-runner
IMAGE=sesamefs-w2-post-head-3dc
NETWORK=
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
	for n in na asia; do
		docker start "sesamefs-cassandra-$n" >/dev/null 2>&1 || true
	done
	for n in na eu asia; do
		docker exec "sesamefs-cassandra-$n" nodetool enablehandoff >/dev/null 2>&1 || true
	done
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

wait_gossip_stable() {
	local node="$1"
	local status
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
		if docker exec "sesamefs-cassandra-$node" cqlsh -e "CONSISTENCY EACH_QUORUM; SELECT * FROM sesamefs.gc_provisional_block_refs LIMIT 1;" >/dev/null 2>&1; then
			return 0
		fi
		sleep 2
	done
	fail "EACH_QUORUM reads from dc-$node did not become reliable after gossip stabilized"
}

wait_serial_head_ready() {
	local node="$1" org_id="$2" repo_id="$3"
	for _ in $(seq 1 30); do
		if docker exec "sesamefs-cassandra-$node" cqlsh -e "CONSISTENCY SERIAL; SELECT head_commit_id FROM sesamefs.libraries WHERE org_id = $org_id AND library_id = $repo_id;" >/dev/null 2>&1; then
			return 0
		fi
		sleep 2
	done
	fail "SERIAL HEAD reads from dc-$node did not become reliable after gossip stabilized"
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
if [ -n "$("${DEFAULT_COMPOSE[@]}" ps -q --status running sesamefs 2>/dev/null)" ]; then
	DEFAULT_BACKEND_WAS_RUNNING=1
fi
"${DEFAULT_COMPOSE[@]}" up -d sesamefs
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
if ! docker inspect -f '{{range $name, $_ := .NetworkSettings.Networks}}{{println $name}}{{end}}' "$RUNNER" | grep -qx "${DEFAULT_COMPOSE_PROJECT}_default"; then
	docker network connect "${DEFAULT_COMPOSE_PROJECT}_default" "$RUNNER"
fi

step "Apply schema through dc-na"
runner_env dc-na env \
	CASSANDRA_HOSTS=cassandra-na:9042 \
	go run ./cmd/sesamefs migrate

for n in na eu asia; do wait_gossip_stable "$n"; done
for n in na eu asia; do wait_each_quorum_ready "$n"; done

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
wait_gossip_stable na
wait_gossip_stable eu
wait_gossip_stable asia
wait_each_quorum_ready na
wait_each_quorum_ready eu
wait_each_quorum_ready asia

step "Run the production repair classifier from blind dc-na"
if ! blind_output="$(runner_env dc-na env \
	SESAMEFS_REQUIRE_W2_POST_HEAD_MULTIDC_EVIDENCE=1 \
	W2_POST_HEAD_ORG="$ORG" W2_POST_HEAD_REPO="$REPO" W2_POST_HEAD_PARENT="$PARENT" W2_POST_HEAD_COMMIT="$COMMIT" \
	go test -tags integration -count=1 ./internal/integration/ -run '^TestW2PostHeadRepairDoesNotMisclassifyRemoteHead3DC$|^TestEveryEvidenceGateIsWiredIntoTestMain$' -v 2>&1)"; then
	echo "$blind_output"
	fail "blind-dc reachability classification failed"
fi
echo "$blind_output"
require_pass "$blind_output" TestW2PostHeadRepairDoesNotMisclassifyRemoteHead3DC
wait_gossip_stable asia
wait_each_quorum_ready asia
wait_serial_head_ready na "$ORG" "$REPO"
wait_serial_head_ready eu "$ORG" "$REPO"

step "Advance HEAD and preserve the remote target as an ancestor"
if ! advance_output="$(runner_env dc-na env \
	W2_POST_HEAD_ADVANCE=1 \
	W2_POST_HEAD_ORG="$ORG" W2_POST_HEAD_REPO="$REPO" W2_POST_HEAD_PARENT="$PARENT" W2_POST_HEAD_COMMIT="$COMMIT" \
	go test -tags integration -count=1 ./internal/integration/ -run '^TestW2PostHeadAdvanceRemoteCommitFor3DC$' -v 2>&1)"; then
	echo "$advance_output"
	fail "advanced HEAD setup failed"
fi
echo "$advance_output"
require_pass "$advance_output" TestW2PostHeadAdvanceRemoteCommitFor3DC
ADVANCED_COMMIT="$(sed -n 's/.*W2_POST_HEAD_ADVANCED_COMMIT=\([a-z0-9-]*\).*/\1/p' <<<"$advance_output" | tail -1)"
[ -n "$ADVANCED_COMMIT" ] || fail "could not capture advanced W2 3-DC commit id"

step "Classify the earlier target as reachable from the later HEAD in another DC"
if ! ancestor_output="$(runner_env dc-eu env \
	W2_POST_HEAD_VERIFY_ADVANCED=1 \
	W2_POST_HEAD_ORG="$ORG" W2_POST_HEAD_REPO="$REPO" W2_POST_HEAD_PARENT="$PARENT" \
	W2_POST_HEAD_COMMIT="$COMMIT" W2_POST_HEAD_ADVANCED_COMMIT="$ADVANCED_COMMIT" \
	go test -tags integration -count=1 ./internal/integration/ -run '^TestW2PostHeadAncestorAfterAdvancementIsReachable3DC$' -v 2>&1)"; then
	echo "$ancestor_output"
	fail "advanced ancestor reachability failed"
fi
echo "$ancestor_output"
require_pass "$ancestor_output" TestW2PostHeadAncestorAfterAdvancementIsReachable3DC

step "Stop one DC and prove incomplete evidence retains the repair"
"${THREE_DC[@]}" stop cassandra-asia
if ! unavailable_output="$(runner_env dc-na env \
	W2_POST_HEAD_VERIFY_UNAVAILABLE=1 \
	W2_POST_HEAD_ORG="$ORG" W2_POST_HEAD_REPO="$REPO" W2_POST_HEAD_PARENT="$PARENT" \
	W2_POST_HEAD_COMMIT="$COMMIT" W2_POST_HEAD_ADVANCED_COMMIT="$ADVANCED_COMMIT" \
	go test -tags integration -count=1 ./internal/integration/ -run '^TestW2PostHeadUnavailableDCIsUnknownAndRetained3DC$' -v 2>&1)"; then
	echo "$unavailable_output"
	fail "unavailable-DC conservative retention failed"
fi
echo "$unavailable_output"
require_pass "$unavailable_output" TestW2PostHeadUnavailableDCIsUnknownAndRetained3DC

step "Prove resumable cursor/anchor stay durable while one DC is down"
if ! cursor_outage_output="$(runner_env dc-na env \
	W2_POST_HEAD_VERIFY_CURSOR_OUTAGE=1 \
	W2_POST_HEAD_ORG="$ORG" W2_POST_HEAD_REPO="$REPO" W2_POST_HEAD_PARENT="$PARENT" \
	W2_POST_HEAD_COMMIT="$COMMIT" W2_POST_HEAD_ADVANCED_COMMIT="$ADVANCED_COMMIT" \
	go test -tags integration -count=1 ./internal/integration/ -run '^TestW2PostHeadResumableCursorRetainsProgressWhileDCUnavailable3DC$' -v 2>&1)"; then
	echo "$cursor_outage_output"
	fail "unavailable-DC resumable cursor retention failed"
fi
echo "$cursor_outage_output"
require_pass "$cursor_outage_output" TestW2PostHeadResumableCursorRetainsProgressWhileDCUnavailable3DC
CURSOR_FSID="$(sed -n 's/.*W2_POST_HEAD_CURSOR_FSID=\([^ ]*\).*/\1/p' <<<"$cursor_outage_output" | tail -1)"
CURSOR_BLOCK="$(sed -n 's/.*W2_POST_HEAD_CURSOR_BLOCK=\([^ ]*\).*/\1/p' <<<"$cursor_outage_output" | tail -1)"
[ -n "$CURSOR_FSID" ] && [ -n "$CURSOR_BLOCK" ] || fail "could not capture resumable cursor repair ids"

"${THREE_DC[@]}" start cassandra-asia
wait_healthy asia
wait_gossip_stable asia
wait_each_quorum_ready na
wait_each_quorum_ready eu
wait_each_quorum_ready asia
wait_serial_head_ready na "$ORG" "$REPO"

step "Resume the same durable cursor from two DCs after HEAD moved"
if ! cursor_resume_output="$(runner_env dc-na env \
	W2_POST_HEAD_VERIFY_CURSOR_RESUME=1 \
	W2_POST_HEAD_ORG="$ORG" W2_POST_HEAD_REPO="$REPO" W2_POST_HEAD_PARENT="$PARENT" \
	W2_POST_HEAD_COMMIT="$COMMIT" W2_POST_HEAD_ADVANCED_COMMIT="$ADVANCED_COMMIT" \
	W2_POST_HEAD_CURSOR_FSID="$CURSOR_FSID" W2_POST_HEAD_CURSOR_BLOCK="$CURSOR_BLOCK" \
	go test -tags integration -count=1 ./internal/integration/ -run '^TestW2PostHeadResumableCursorResumesAfterOutageAndIgnoresMovingHEAD3DC$' -v 2>&1)"; then
	echo "$cursor_resume_output"
	fail "resumable cursor resume after outage failed"
fi
echo "$cursor_resume_output"
require_pass "$cursor_resume_output" TestW2PostHeadResumableCursorResumesAfterOutageAndIgnoresMovingHEAD3DC

echo
echo "R31-C1 3-DC reachability evidence passed: local blindness and unavailable evidence retained repair, a later HEAD preserved ancestor reachability, the SERIAL anchor survived the outage, later HEAD movement did not replace it, and two DCs resumed from that same anchor. This does not claim a concurrent cross-DC cursor CAS race."
