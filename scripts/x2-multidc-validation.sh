#!/usr/bin/env bash
#
# X2 closure evidence, automated.
#
# Runs the three legs that ISSUE-GC-CROSS-DC-REFERENCE-VISIBILITY-01 needs before it
# can be marked Closed, against the real three-datacenter Cassandra fixture. The prose
# runbook in docs/GC-X2-MULTIDC-VALIDATION.md remains the explanation; this is the
# executable form, because a multi-step manual procedure that nobody can run in one
# command is a procedure that quietly stops being run.
#
#   ./scripts/x2-multidc-validation.sh            # full run, tears the stack down
#   ./scripts/x2-multidc-validation.sh --keep     # leave the stack up afterwards
#   ./scripts/x2-multidc-validation.sh --no-up    # reuse an already-running stack
#   ./scripts/x2-multidc-validation.sh --mutate   # prove leg 1 goes RED when the
#                                                 # destructive read is downgraded.
#                                                 # Implies --no-up and --keep, so it
#                                                 # needs a stack that is ALREADY up
#                                                 # (run --keep first); it is not a
#                                                 # standalone entry point.
#
# THE ONE THING TO UNDERSTAND BEFORE TRUSTING A GREEN RUN
# -------------------------------------------------------
# Leg 1 needs a cluster that is deliberately DIVERGENT. Cassandra sends every mutation
# to all replicas regardless of consistency level — the level only decides how many
# acknowledgements the coordinator waits for — so "write in dc-eu, read from dc-na"
# succeeds whether or not the read is EACH_QUORUM, and would pass on the unfixed code.
# The divergence is built by stopping the other two DCs during the write with hinted
# handoff disabled, so dc-na's replica genuinely never receives the row.
#
# The visibility test asserts BOTH halves against that state, and fails loudly if
# dc-na can already see the reference locally. That first assertion is what makes the
# second one mean anything.
#
# NOTE: the leg is single-use per block id. An EACH_QUORUM read performs blocking
# read repair to satisfy its consistency level, so the row IS propagated to dc-na by
# the very read being tested. Re-running against the same X2_DIVERGENT_BLOCK fails the
# local-blindness assertion — correctly. This script mints a fresh id each run.

set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

COMPOSE=(docker compose -f docker-compose.cassandra-3dc.yaml)
NODES=(na eu asia)
export X2_DC_HOSTS="dc-na=127.0.0.1:${CASSANDRA_NA_HOST_PORT:-9242},dc-eu=127.0.0.1:${CASSANDRA_EU_HOST_PORT:-9243},dc-asia=127.0.0.1:${CASSANDRA_ASIA_HOST_PORT:-9244}"

DO_UP=1
DO_DOWN=1
DO_MUTATE=0
for arg in "$@"; do
  case "$arg" in
    --keep)   DO_DOWN=0 ;;
    --no-up)  DO_UP=0 ;;
    --mutate) DO_MUTATE=1; DO_UP=0; DO_DOWN=0 ;;
    *) echo "unknown flag: $arg" >&2; exit 2 ;;
  esac
done

step() { printf '\n\033[1m==> %s\033[0m\n' "$*"; }
fail() { printf '\n\033[31mFAILED: %s\033[0m\n' "$*" >&2; exit 1; }

# The integration package's TestMain needs a reachable backend and a reachable
# Cassandra for its own ephemeral-library cleanup, neither of which these pure
# database tests use. Point it at a live node of THIS fixture; its default,
# `cassandra:9042`, is a docker-internal name that does not resolve from the host, and
# an unverifiable cleanup fails the whole package however the tests went.
point_harness_at() {
  export CASSANDRA_HOSTS="127.0.0.1:$1"
  export CASSANDRA_LOCAL_DC="$2"
}

# Run one leg and require that the test ACTUALLY RAN.
#
# This matters more than the exit code. TestMain calls os.Exit(0) when the backend is
# not reachable, and `go test` reports a skipped package as success — so a run that
# executed nothing looks identical to a run that proved everything. Demanding the
# named PASS line is the difference between evidence and a green tick.
run_leg() {
  local label="$1" pattern="$2"; shift 2
  local out rc
  # `set -e` aborts on a failing assignment, which would kill the script here before
  # rc is ever read — losing the diagnosis below and reporting a bare non-zero exit
  # instead of WHICH leg failed and why. Suspend it around the run, exactly as the
  # mutation leg does. This cannot manufacture a green: the PASS-line check below is
  # what decides, and it runs under set -e again.
  set +e
  out="$(go test -tags integration -count=1 ./internal/integration/ -run "$pattern" -v 2>&1)"
  rc=$?
  set -e
  echo "$out"
  grep -qE "^--- PASS: ${pattern}" <<<"$out" \
    || fail "$label — no '--- PASS: ${pattern}...' in the output (skipped, or never ran)"
  [ $rc -eq 0 ] || fail "$label — the test passed but the package run failed; see above"
}

# Re-enable hints on the way out however we exit. Without this an aborted run leaves
# the fixture in a state where any later test builds divergence it did not ask for —
# which is far more confusing than a stack that is simply gone.
cleanup() {
  local rc=$?
  step "Cleanup"
  for n in "${NODES[@]}"; do
    docker exec "sesamefs-cassandra-$n" nodetool enablehandoff >/dev/null 2>&1 || true
    docker start "sesamefs-cassandra-$n" >/dev/null 2>&1 || true
  done
  if [ "$DO_DOWN" = "1" ]; then
    "${COMPOSE[@]}" down -v >/dev/null 2>&1 || true
    echo "stack torn down"
  else
    echo "stack left running (--keep); hints re-enabled"
  fi
  exit $rc
}
trap cleanup EXIT

wait_healthy() {
  local name="sesamefs-cassandra-$1" deadline=$((SECONDS + 420))
  while [ $SECONDS -lt $deadline ]; do
    case "$(docker inspect -f '{{.State.Health.Status}}' "$name" 2>/dev/null || echo missing)" in
      healthy) return 0 ;;
    esac
    sleep 5
  done
  fail "$name did not become healthy in time"
}

# Wait for the keyspace-creating container to FINISH, and require exit 0.
#
# Three nodes going healthy does not mean the keyspace exists: the bootstrap container
# only starts once all three are healthy and then polls until all three datacenters are
# visible in gossip. Without this wait the script races it, and whichever side wins
# creates the keyspace — migrate would create it from CASSANDRA_REPLICATION_DCS (now
# passed explicitly for exactly that reason, so both orderings produce the same 3-DC
# map) and the bootstrap's ALTER would arrive afterwards. The race never produced a
# false green, because a keyspace replicated anywhere but all three DCs makes the
# divergent write fail loudly, but "correct because one side happens to win" is not a
# reproducible fixture.
#
# Polls docker inspect rather than `docker compose wait`, which is a recent subcommand;
# the container name is fixed in the compose file.
wait_bootstrap() {
  local name=sesamefs-cassandra-3dc-bootstrap deadline=$((SECONDS + 420)) state
  while [ $SECONDS -lt $deadline ]; do
    state="$(docker inspect -f '{{.State.Status}}:{{.State.ExitCode}}' "$name" 2>/dev/null || echo missing:)"
    case "$state" in
      exited:0) return 0 ;;
      exited:*)
        docker logs "$name" 2>&1 | tail -30
        fail "the keyspace bootstrap container exited ${state#exited:}; see its log above"
        ;;
    esac
    sleep 5
  done
  fail "$name did not finish in time"
}

LEG1=TestX2_DivergentReferenceIsInvisibleLocallyAndVisibleGlobally

# Build a cluster state where dc-na genuinely does not have the reference: disable
# hints, stop the other two DCs, write through dc-eu, bring them back. Exports
# X2_DIVERGENT_ORG/BLOCK for the visibility leg.
#
# Single-use per block id. The EACH_QUORUM read under test performs blocking read
# repair to satisfy its own consistency level, so it propagates the row to dc-na as a
# side effect of reading it. Each call therefore mints fresh ids.
build_divergence() {
  for n in "${NODES[@]}"; do docker exec "sesamefs-cassandra-$n" nodetool disablehandoff >/dev/null; done
  "${COMPOSE[@]}" stop cassandra-na cassandra-asia
  point_harness_at "${CASSANDRA_EU_HOST_PORT:-9243}" dc-eu
  local out rc
  # Same reason as run_leg: under `set -e` a failing assignment aborts the script
  # before the diagnosis below can run, turning "the divergent write failed" into a
  # bare non-zero exit. The PASS check that follows is what decides.
  set +e
  out="$(X2_WRITE_DIVERGENT=1 go test -tags integration -count=1 ./internal/integration/ \
    -run TestX2_WriteReferenceForDivergence -v 2>&1)"
  rc=$?
  set -e
  echo "$out" | grep -E 'X2_DIVERGENT_(ORG|BLOCK)|--- (PASS|FAIL)' || true
  grep -q '^--- PASS: TestX2_WriteReferenceForDivergence' <<<"$out" \
    || { echo "$out"; fail "divergent write did not run"; }
  # Named PASS but non-zero package exit: TestMain's own cleanup failed, so the fixture
  # is in an unknown state and the ids below would be built on it. Same check run_leg
  # makes, for the same reason.
  [ $rc -eq 0 ] || { echo "$out"; fail "divergent write passed but the package run failed; see above"; }
  X2_DIVERGENT_ORG="$(sed -n 's/.*X2_DIVERGENT_ORG=\([0-9a-f-]*\).*/\1/p'    <<<"$out" | tail -1)"
  X2_DIVERGENT_BLOCK="$(sed -n 's/.*X2_DIVERGENT_BLOCK=\(x2-[0-9a-f-]*\).*/\1/p' <<<"$out" | tail -1)"
  [ -n "$X2_DIVERGENT_ORG" ] && [ -n "$X2_DIVERGENT_BLOCK" ] || fail "could not capture the divergent ids"
  export X2_DIVERGENT_ORG X2_DIVERGENT_BLOCK
  "${COMPOSE[@]}" start cassandra-na cassandra-asia
  for n in na asia; do wait_healthy "$n"; done
  point_harness_at "${CASSANDRA_NA_HOST_PORT:-9242}" dc-na
}

# --mutate: prove leg 1 CAN fail.
#
# The checklist demands this and it is the whole reason the leg is worth running. A
# green visibility test only means something if the same test goes red when the
# destructive read is downgraded to the consistency level that caused the defect. Run
# against a freshly divergent cluster, because read repair already healed the last one.
if [ "${DO_MUTATE:-0}" = "1" ]; then
  for n in "${NODES[@]}"; do wait_healthy "$n"; done
  step "MUTATION — downgrade the destructive read to LOCAL_QUORUM; leg 1 must FAIL"
  src=internal/db/block_references.go
  cp "$src" "$src.x2bak"
  restore_src() { [ -f "$src.x2bak" ] && mv "$src.x2bak" "$src"; }
  trap 'restore_src; cleanup' EXIT
  perl -0pi -e 's/\Q.Consistency(gocql.EachQuorum)\E/.Consistency(gocql.LocalQuorum)/' "$src"
  cmp -s "$src" "$src.x2bak" && fail "mutation did not apply — the EACH_QUORUM pin moved?"

  build_divergence
  set +e
  out="$(go test -tags integration -count=1 ./internal/integration/ -run "$LEG1" -v 2>&1)"
  set -e
  echo "$out" | grep -E '^--- (PASS|FAIL)|X2 REGRESSION' || true
  restore_src
  trap cleanup EXIT
  grep -q 'X2 REGRESSION' <<<"$out" \
    || fail "leg 1 did NOT fail under a LOCAL_QUORUM destructive read — the test cannot detect the defect it exists for"
  printf '\n\033[32mMutation confirmed: leg 1 goes red when the destructive read is downgraded.\033[0m\n'
  exit 0
fi

if [ "$DO_UP" = "1" ]; then
  step "1. Stand up three real datacenters (nodes join one at a time)"
  "${COMPOSE[@]}" up -d
fi
for n in "${NODES[@]}"; do wait_healthy "$n"; done
if [ "$DO_UP" = "1" ]; then
  step "1b. Wait for the keyspace bootstrap to finish"
  wait_bootstrap
fi

# The replication map is passed explicitly rather than left to the config defaults.
# `migrate` creates the keyspace itself when it is missing, and with only
# CASSANDRA_LOCAL_DC set it would fall back to {dc-na: 1} — an under-declared map, which
# is precisely the topology leg 3b asserts the destructive gate must REFUSE. Stating the
# full map here makes both creators produce the same keyspace, so the outcome no longer
# depends on who wins.
step "2. Apply the schema through the local DC"
CASSANDRA_HOSTS="127.0.0.1:${CASSANDRA_NA_HOST_PORT:-9242}" CASSANDRA_LOCAL_DC=dc-na \
  CASSANDRA_REPLICATION_CLASS=NetworkTopologyStrategy \
  CASSANDRA_REPLICATION_DCS=dc-na:1,dc-eu:1,dc-asia:1 \
  go run ./cmd/sesamefs migrate

step "3. Disable hinted handoff (load-bearing: hints would erase the divergence)"
for n in "${NODES[@]}"; do
  docker exec "sesamefs-cassandra-$n" nodetool disablehandoff
done

step "4. Build the divergent state: write the reference in dc-eu alone"
build_divergence

step "5. LEG 1 — visibility: LOCAL_QUORUM(dc-na)=false AND EACH_QUORUM(dc-na)=true"
run_leg "leg 1 (divergent visibility)" "$LEG1"

step "6. LEG 2 — fail closed: with dc-asia down the destructive read must ERROR"
# No need to guard the restart: the EXIT trap brings every node back however this
# leg ends, so a failure here still leaves the fixture usable.
"${COMPOSE[@]}" stop cassandra-asia
X2_EXPECT_DC_DOWN=1 run_leg "leg 2 (fail closed with a DC down)" TestX2_EachQuorumFailsClosedWhenADatacenterIsDown
"${COMPOSE[@]}" start cassandra-asia
wait_healthy asia

step "7. LEG 3 — topology gate: accepts the declared map, refuses an under-declared one"
run_leg "leg 3a (gate accepts the declared 3-DC map)" TestX2_TopologyGateAcceptsThreeDCNetworkTopology
run_leg "leg 3b (gate refuses an under-declared map)" TestX2_TopologyGateRejectsAnUnderDeclaredMap

printf '\n\033[32mAll three X2 closure legs green.\033[0m\n'
printf 'divergent org=%s block=%s\n' "$X2_DIVERGENT_ORG" "$X2_DIVERGENT_BLOCK"
