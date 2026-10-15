#!/usr/bin/env bash
# Real 3-DC evidence for ISSUE-SYNC-PUTBLOCK-CROSS-DC-PROVENANCE-VISIBILITY-01.
#
# This is intentionally separate from scripts/w2-post-head-multidc-validation.sh:
# that script exercises the post-HEAD publication-repair classifier against a
# HEAD written only in dc-eu, while this one exercises the pre-HEAD Sync
# PutBlock provenance scope gate against an up:sync:<repo>:<block> reference
# acknowledged only in dc-eu. Reuses the same 3-DC fixture, runner image, and
# w2PostHead3DCConnect/w2PostHead3DCEndpoints Go helpers.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

THREE_DC=(docker compose -f docker-compose.cassandra-3dc.yaml)
DEFAULT_COMPOSE=(docker compose -p sesamefs -f docker-compose.yaml)
RUNNER=sesamefs-w2-sync-xdc-3dc-runner
IMAGE=sesamefs-w2-sync-xdc-3dc
NETWORK=sesamefs-cassandra-3dc_default
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
	# Docker's healthcheck reports a Cassandra node "healthy" as soon as its
	# process/port is up, which can be before it has fully rejoined gossip
	# ring membership from every other DC's perspective. An EACH_QUORUM read
	# needs every DC to actually ack, so poll nodetool status from the given
	# node until all three DCs show "UN" (Up/Normal) rather than racing a
	# real cluster-membership convergence with a fixed sleep. This does NOT
	# wait for any specific write to replicate -- with hinted handoff
	# disabled and no repair triggered, a write made while a node was down
	# stays invisible to that node's LOCAL_QUORUM reads regardless of how
	# long gossip takes to stabilize; it only waits for the nodes to be
	# reachable at all.
	local node="$1"
	local status
	for _ in $(seq 1 60); do
		status="$(docker exec "sesamefs-cassandra-$node" nodetool status 2>/dev/null | grep -c '^UN ' || true)"
		[ "$status" = "3" ] && return 0
		sleep 2
	done
	fail "gossip did not stabilize to 3 UN nodes from dc-$node's view (last count: $status)"
}

wait_each_quorum_ready() {
	# gossip showing 3 UN nodes (wait_gossip_stable) is necessary but not
	# sufficient: a restarted node can be reachable and ring-visible before
	# it reliably participates in EACH_QUORUM coordination, producing
	# "Operation timed out - received only N responses" on the very next
	# real EACH_QUORUM read or write -- observed hitting both the production
	# scope-gate read and this script's own post-test cleanup deletes.
	# Retry a cheap, real EACH_QUORUM read against the already-migrated
	# schema (a harmless, unrestricted LIMIT 1 range read, no ALLOW
	# FILTERING needed) until it actually succeeds, rather than trusting
	# gossip status alone.
	local node="$1"
	for _ in $(seq 1 30); do
		if docker exec "sesamefs-cassandra-$node" cqlsh -e "CONSISTENCY EACH_QUORUM; SELECT * FROM sesamefs.gc_provisional_block_refs LIMIT 1;" >/dev/null 2>&1; then
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

step "Build an isolated Docker Go runner on both networks"
docker build -f Dockerfile.gotest -t "$IMAGE" .
docker rm -f "$RUNNER" >/dev/null 2>&1 || true
docker run -d --name "$RUNNER" --network "$NETWORK" "$IMAGE" sleep 3600 >/dev/null
docker network connect sesamefs_default "$RUNNER"

step "Apply schema through dc-na"
runner_env dc-na env \
	CASSANDRA_HOSTS=cassandra-na:9042 \
	go run ./cmd/sesamefs migrate

step "Simulate a real PutBlock landing in dc-eu, with dc-na and dc-asia stopped"
for n in na eu asia; do docker exec "sesamefs-cassandra-$n" nodetool disablehandoff >/dev/null; done
"${THREE_DC[@]}" stop cassandra-na cassandra-asia
if ! write_output="$(runner_env dc-eu env \
	W2_SYNC_XDC_WRITE_EU=1 \
	${W2_SYNC_XDC_BLOCK_COUNT:+W2_SYNC_XDC_BLOCK_COUNT="$W2_SYNC_XDC_BLOCK_COUNT"} \
	go test -tags integration -count=1 -timeout 5m ./internal/integration/ -run '^TestW2SyncXDCPutBlockWritesProvenanceInEU3DC$' -v 2>&1)"; then
	echo "$write_output"
	fail "3-DC PutBlock provenance write test failed"
fi
echo "$write_output"
require_pass "$write_output" TestW2SyncXDCPutBlockWritesProvenanceInEU3DC
ORG="$(sed -n 's/.*W2_SYNC_XDC_ORG=\([0-9a-f-]*\).*/\1/p' <<<"$write_output" | tail -1)"
REPO="$(sed -n 's/.*W2_SYNC_XDC_REPO=\([0-9a-f-]*\).*/\1/p' <<<"$write_output" | tail -1)"
BLOCK="$(sed -n 's/.*W2_SYNC_XDC_BLOCK=\([0-9a-f]*\).*/\1/p' <<<"$write_output" | tail -1)"
EXPIRES_AT="$(sed -n 's/.*W2_SYNC_XDC_EXPIRES_AT=\([^ ]*\).*/\1/p' <<<"$write_output" | tail -1)"
COST_PREFIX="$(sed -n 's/.*W2_SYNC_XDC_COST_PREFIX=\([^ ]*\).*/\1/p' <<<"$write_output" | tail -1)"
COST_COUNT="$(sed -n 's/.*W2_SYNC_XDC_COST_COUNT=\([0-9]*\).*/\1/p' <<<"$write_output" | tail -1)"
COST_EXPIRES_AT="$(sed -n 's/.*W2_SYNC_XDC_COST_EXPIRES_AT=\([^ ]*\).*/\1/p' <<<"$write_output" | tail -1)"
[ -n "$ORG" ] && [ -n "$REPO" ] && [ -n "$BLOCK" ] && [ -n "$EXPIRES_AT" ] || fail "could not capture the seeded W2 Sync XDC ids"
[ -n "$COST_PREFIX" ] && [ -n "$COST_COUNT" ] && [ -n "$COST_EXPIRES_AT" ] || fail "could not capture the seeded W2 Sync XDC cost-characterization fixture"

step "Restart dc-na and dc-asia; query the pre-HEAD scope gate from blind dc-na immediately"
"${THREE_DC[@]}" start cassandra-na cassandra-asia
wait_healthy na
wait_healthy asia
wait_gossip_stable na
wait_each_quorum_ready na

runner_env dc-na env \
	SESAMEFS_REQUIRE_W2_SYNC_PUTBLOCK_XDC_EVIDENCE=1 \
	W2_SYNC_XDC_ORG="$ORG" W2_SYNC_XDC_REPO="$REPO" W2_SYNC_XDC_BLOCK="$BLOCK" W2_SYNC_XDC_EXPIRES_AT="$EXPIRES_AT" \
	go test -tags integration -count=1 ./internal/integration/ -run '^TestW2SyncXDCRecoversCrossDCProvenanceFromBlindNA3DC$' -v

step "Measure the all-cross-DC-hit cost scenario on the real 3-DC Cassandra fixture (N=1/10/100/1000)"
# Deliberately does NOT set SESAMEFS_REQUIRE_W2_SYNC_PUTBLOCK_XDC_EVIDENCE=1:
# that gate's completeness check lives in TestMain and requires the named
# recovery leg (TestW2SyncXDCRecoversCrossDCProvenanceFromBlindNA3DC) to have
# run and set w2SyncXDCEvidence=true within the SAME go test process. That
# leg already ran and was required in the previous step's own process; this
# is a separate go test invocation for the cost measurement only, and the
# cost test itself already skips cleanly if its own env vars are unset.
runner_env dc-na env \
	W2_SYNC_XDC_ORG="$ORG" W2_SYNC_XDC_REPO="$REPO" \
	W2_SYNC_XDC_COST_PREFIX="$COST_PREFIX" W2_SYNC_XDC_COST_COUNT="$COST_COUNT" \
	W2_SYNC_XDC_COST_EXPIRES_AT="$COST_EXPIRES_AT" \
	go test -tags integration -count=1 -timeout 5m ./internal/integration/ -run '^TestW2SyncXDCAllCrossDCHitCostAtN3DC$' -v

step "Stop dc-asia only; the fallback must fail closed, not hang or silently report absence"
"${THREE_DC[@]}" stop cassandra-asia
if ! down_output="$(runner_env dc-na env \
	W2_SYNC_XDC_ONE_DC_DOWN=1 \
	go test -tags integration -count=1 ./internal/integration/ -run '^TestW2SyncXDCFallbackFailsClosedWhenADatacenterIsDown3DC$' -v 2>&1)"; then
	echo "$down_output"
	fail "one-DC-down fail-closed leg failed"
fi
echo "$down_output"
require_pass "$down_output" TestW2SyncXDCFallbackFailsClosedWhenADatacenterIsDown3DC
"${THREE_DC[@]}" start cassandra-asia
wait_healthy asia

echo
echo "W2 Sync PutBlock cross-DC provenance evidence passed: dc-na's LOCAL_QUORUM read stayed blind to the dc-eu PutBlock, the EACH_QUORUM fallback still recovered its provenance before HEAD, and the same fallback failed closed (not silently absent, not hung) with one datacenter down."
