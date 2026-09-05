#!/usr/bin/env bash
#
# X2 closure evidence, automated.
#
# Runs the five closure legs that ISSUE-GC-CROSS-DC-REFERENCE-VISIBILITY-01 needs before it
# can be marked Closed, against the real three-datacenter Cassandra fixture. The prose
# runbook in docs/GC-X2-MULTIDC-VALIDATION.md remains the explanation; this is the
# executable form, because a multi-step manual procedure that nobody can run in one
# command is a procedure that quietly stops being run.
#
#   ./scripts/x2-multidc-validation.sh            # full run, tears the stack down
#   ./scripts/x2-multidc-validation.sh --keep     # leave the stack up afterwards
#   ./scripts/x2-multidc-validation.sh --no-up    # reuse an already-running stack
#   ./scripts/x2-multidc-validation.sh --mutate   # prove leg 1 goes RED when the
#                                                 # destructive read is downgraded to
#                                                 # LOCAL_QUORUM (the original defect).
#   ./scripts/x2-multidc-validation.sh --mutate-quorum
#                                                 # prove leg 2b goes RED when it is
#                                                 # downgraded to QUORUM (the plausible
#                                                 # WRONG fix).
#                                                 # Both mutations imply --no-up and
#                                                 # --keep, so they need a stack that is
#                                                 # ALREADY up (run --keep first); they
#                                                 # are not standalone entry points.
#
# WHY TWO MUTATIONS, AND WHY NEITHER IS REDUNDANT
# -----------------------------------------------
# They refute different claims. --mutate refutes "the fix is unnecessary": it shows the
# test detects the ORIGINAL defect, a destructive read at LOCAL_QUORUM. --mutate-quorum
# refutes "a simpler fix would do": it shows a destructive read at plain QUORUM returns
# a false zero on live data. That second one is the entire reason this fixture has three
# datacenters rather than two — at two DCs with RF 1, QUORUM is 2 of 2 and intersects
# everything by accident, so a two-DC fixture cannot tell the right fix from the wrong
# one. Without it, "three DCs rule QUORUM out" is an argument in a document; with it, it
# is a test that fails if someone implements the wrong fix.
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

# The fixture itself is Docker-only. Keep the harness process on the host for
# docker compose orchestration, but run every Go command in the gotest image so
# migrations and evidence cannot silently use a host toolchain or host checkout.
# Mounting the checkout read-only is deliberate: mutation modes edit the host
# checkout before invoking this runner, so the container sees the mutated source.
X2_TEST_IMAGE="${X2_TEST_IMAGE:-sesamefs-gotest:latest}"
X2_TEST_ENV_FILE="${X2_TEST_ENV_FILE:-.env}"
X2_TEST_NETWORK="${X2_TEST_NETWORK:-host}"
X2_TEST_HOST="${X2_TEST_HOST:-}"
if [ -z "$X2_TEST_HOST" ]; then
  if [ "$X2_TEST_NETWORK" = "host" ]; then
    X2_TEST_HOST=127.0.0.1
  else
    X2_TEST_HOST=host.docker.internal
  fi
fi
if [ -z "${X2_TEST_BACKEND_URL:-}" ]; then
  if [ "$X2_TEST_NETWORK" = "host" ]; then
    X2_TEST_BACKEND_URL=http://127.0.0.1:8080
  else
    X2_TEST_BACKEND_URL=http://sesamefs:8080
  fi
fi
export X2_DC_HOSTS="dc-na=${X2_TEST_HOST}:${CASSANDRA_NA_HOST_PORT:-9242},dc-eu=${X2_TEST_HOST}:${CASSANDRA_EU_HOST_PORT:-9243},dc-asia=${X2_TEST_HOST}:${CASSANDRA_ASIA_HOST_PORT:-9244}"

TEST_DOCKER_ARGS=(--network "$X2_TEST_NETWORK")
if [ "$X2_TEST_NETWORK" != "host" ]; then
  TEST_DOCKER_ARGS+=(--add-host host.docker.internal:host-gateway)
fi

DO_UP=1
DO_DOWN=1
DO_MUTATE=0
MUTATE_TO=""
DO_P3=0
P3_MUTATE=0
for arg in "$@"; do
  case "$arg" in
    --keep)   DO_DOWN=0 ;;
    --no-up)  DO_UP=0 ;;
    --mutate)        DO_MUTATE=1; MUTATE_TO=LocalQuorum; DO_UP=0; DO_DOWN=0 ;;
    --mutate-quorum) DO_MUTATE=1; MUTATE_TO=Quorum;      DO_UP=0; DO_DOWN=0 ;;
    --p3)               DO_P3=1; DO_UP=0; DO_DOWN=0 ;;
    --p3-mutate)        DO_P3=1; P3_MUTATE=1; MUTATE_TO=LocalQuorum; DO_UP=0; DO_DOWN=0 ;;
    --p3-mutate-quorum) DO_P3=1; P3_MUTATE=1; MUTATE_TO=Quorum;      DO_UP=0; DO_DOWN=0 ;;
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
  export CASSANDRA_HOSTS="${X2_TEST_HOST}:$1"
  export CASSANDRA_LOCAL_DC="$2"
}

docker_go_with() {
  local env_args=()
  while [ "$#" -gt 0 ] && [[ "$1" == *=* ]]; do
    env_args+=(--env "$1")
    shift
  done
  docker run --rm \
    "${TEST_DOCKER_ARGS[@]}" \
    --env-file "$X2_TEST_ENV_FILE" \
    --env SESAMEFS_URL="$X2_TEST_BACKEND_URL" \
    --env CASSANDRA_HOSTS="${CASSANDRA_HOSTS:-}" \
    --env CASSANDRA_LOCAL_DC="${CASSANDRA_LOCAL_DC:-}" \
    --env CASSANDRA_USERNAME= \
    --env CASSANDRA_PASSWORD= \
    --env CASSANDRA_REPLICATION_CLASS="${CASSANDRA_REPLICATION_CLASS:-}" \
    --env CASSANDRA_REPLICATION_DCS="${CASSANDRA_REPLICATION_DCS:-}" \
    --env X2_DC_HOSTS="$X2_DC_HOSTS" \
    --env X2_DIVERGENT_ORG="${X2_DIVERGENT_ORG:-}" \
    --env X2_DIVERGENT_BLOCK="${X2_DIVERGENT_BLOCK:-}" \
    --env X2_WRITE_DIVERGENT="${X2_WRITE_DIVERGENT:-}" \
    --env X2_EXPECT_DC_DOWN="${X2_EXPECT_DC_DOWN:-}" \
    --env X2_EXPECT_REFERENCE_DC_DOWN="${X2_EXPECT_REFERENCE_DC_DOWN:-}" \
    --env P3_EXPECT_DC_DOWN="${P3_EXPECT_DC_DOWN:-}" \
    "${env_args[@]}" \
    --volume "$PWD:/build:ro" \
    --workdir /build \
    "$X2_TEST_IMAGE" go "$@"
}

docker_go() {
  docker_go_with "$@"
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
  out="$(docker_go test -tags integration -count=1 ./internal/integration/ -run "$pattern" -v 2>&1)"
  rc=$?
  set -e
  echo "$out"
  grep -qE "^--- PASS: ${pattern}" <<<"$out" \
    || fail "$label — no '--- PASS: ${pattern}...' in the output (skipped, or never ran)"
  [ $rc -eq 0 ] || fail "$label — the test passed but the package run failed; see above"
}

# Re-enable hints on a node that is UP. Retries briefly rather than once: nodetool
# talks to JMX, which can be busy mid-drain, and a single miss here would report a node
# as unconfirmed for no reason. Not a boot wait — see cleanup for why nothing waits for
# a node to come up.
enable_hints() {
  local name="sesamefs-cassandra-$1" deadline=$((SECONDS + 30))
  while :; do
    if docker exec "$name" nodetool enablehandoff >/dev/null 2>&1; then
      return 0
    fi
    [ $SECONDS -lt $deadline ] || return 1
    sleep 2
  done
}

# Re-enable hints on the way out however we exit. Without this an aborted run leaves
# the fixture in a state where any later test builds divergence it did not ask for —
# which is far more confusing than a stack that is simply gone.
#
# WHICH NODES NEED WHAT, because an earlier version treated them alike and got the
# order backwards: it ran enablehandoff and THEN docker start, in one loop, so an abort
# during build_divergence — where two of three nodes are deliberately stopped, and by
# far the likeliest place to abort — sent the command to a stopped container, straight
# into `|| true`. The fixture did come back with hints on, but by luck rather than by
# this function working, while the message claimed otherwise either way.
#
# The two cases are genuinely different, and separating them is what keeps this fast:
#
#   still running — it still holds the disabled state this script set, and only
#                   nodetool can undo it. It answers immediately, so we wait 30s and
#                   report a node that does not.
#   stopped       — disablehandoff is runtime state; booting discards it in favour of
#                   the cassandra.yaml default, which is enabled. Starting the node IS
#                   the restore. Waiting out its several-minute boot to watch it
#                   confirm what the restart already guarantees would turn every
#                   aborted run into a long hang.
cleanup() {
  local rc=$?
  step "Cleanup"

  # Nothing to restore on a stack that is about to be destroyed.
  if [ "$DO_DOWN" = "1" ]; then
    "${COMPOSE[@]}" down -v >/dev/null 2>&1 || true
    echo "stack torn down"
    exit $rc
  fi

  local live=() booting=() missing=() unconfirmed=()
  for n in "${NODES[@]}"; do
    # `missing` is its own case rather than folded into `stopped`: reporting a restart
    # of a container that does not exist would be exactly the kind of unverified claim
    # this function was rewritten to stop making. Reachable via --no-up/--mutate
    # against a stack that was never brought up.
    case "$(docker inspect -f '{{.State.Running}}' "sesamefs-cassandra-$n" 2>/dev/null || echo missing)" in
      true)
        if enable_hints "$n"; then live+=("$n"); else unconfirmed+=("$n"); fi
        ;;
      false)
        docker start "sesamefs-cassandra-$n" >/dev/null 2>&1 || true
        booting+=("$n")
        ;;
      *)
        missing+=("$n")
        ;;
    esac
  done

  echo "stack left running (--keep)"
  [ ${#live[@]} -eq 0 ]    || echo "  hints confirmed back on: ${live[*]}"
  [ ${#booting[@]} -eq 0 ] || echo "  restarted (hints return enabled from cassandra.yaml): ${booting[*]}"
  [ ${#missing[@]} -eq 0 ] || echo "  no such container, nothing to restore: ${missing[*]}"
  if [ ${#unconfirmed[@]} -gt 0 ]; then
    # Deliberately not a failure: it does not invalidate a run that already finished,
    # and overwriting rc would hide the result this script exists to report. It IS
    # loud, because the next run against this fixture would build a divergence that
    # hints can quietly heal, and a silently weakened leg 1 is the worst outcome here.
    printf '\033[31m  WARNING: hints NOT confirmed back on: %s\033[0m\n' "${unconfirmed[*]}" >&2
    printf '\033[31m  Check with: docker exec sesamefs-cassandra-<node> nodetool statushandoff\033[0m\n' >&2
    printf '\033[31m  Do not trust a leg-1 run against this stack until it reports running.\033[0m\n' >&2
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
  out="$(docker_go_with X2_WRITE_DIVERGENT=1 test -tags integration -count=1 ./internal/integration/ \
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

LEG2B=TestX2_FailsClosedWhenTheReferenceDatacenterIsDown

# Assert a node is DOWN, and stay asserting it for the length of a leg.
#
# Leg 2b's whole claim is "the datacenter holding the only reference was unreachable
# when the read happened". A `docker compose stop` at the top of the leg does not
# establish that for the ~1-5 minutes the go test then takes, and this is not
# hypothetical: an ABORTED earlier run leaves an EXIT trap that restarts every stopped
# node, so a leg starting moments later can watch dc-eu boot underneath it. That
# produced a leg 2b that passed for the wrong reason (EACH_QUORUM could not reach a
# node that was mid-boot rather than one that was stopped) and, minutes later, a
# QUORUM read that found the row through a recovered dc-eu and read-repaired it to
# every replica — destroying the fixture.
#
# The test itself refuses to report either PASS or REGRESSION when it sees the row, so
# nothing false was published. This is the other half: check before and after, so the
# harness names the cause instead of leaving a confusing rebuild.
require_stopped() {
  local n="$1" when="$2"
  local state
  state="$(docker inspect -f '{{.State.Running}}' "sesamefs-cassandra-$n" 2>/dev/null || echo missing)"
  [ "$state" = false ] || fail "cassandra-$n must be stopped $when for this leg to mean anything, but docker reports Running=$state. An aborted run's cleanup restarts stopped nodes — make sure no other x2-multidc-validation.sh is running against this fixture, then retry."
}

# --mutate / --mutate-quorum: prove the legs CAN fail.
#
# The checklist demands this and it is the whole reason the legs are worth running. A
# green test only means something if it goes red against the very defect it exists to
# catch. Each mutation targets the leg that discriminates it:
#
#   LocalQuorum → leg 1  (the original defect: a local read that is blind to dc-eu)
#   Quorum      → leg 2b (the wrong fix: 2 of 3 satisfied by the two blind DCs)
#
# Leg 1 cannot carry the QUORUM mutation. With all three DCs up, a QUORUM read from
# dc-na is satisfied by any two replicas, so whether it sees the dc-eu row depends on
# which two the coordinator happens to reach — it would pass or fail by chance. Leg 2b
# removes that freedom by taking dc-eu away entirely: QUORUM then has exactly one
# possible answer, and it is the wrong one.
#
# Both run against a freshly divergent cluster, because read repair healed the last one.
if [ "${DO_MUTATE:-0}" = "1" ]; then
  for n in "${NODES[@]}"; do wait_healthy "$n"; done
  case "$MUTATE_TO" in
    LocalQuorum) MUTATE_LEG=$LEG1;  MUTATE_DESC="leg 1" ;;
    Quorum)      MUTATE_LEG=$LEG2B; MUTATE_DESC="leg 2b" ;;
    *) fail "internal: unknown mutation target '$MUTATE_TO'" ;;
  esac
  step "MUTATION — downgrade the destructive read to ${MUTATE_TO}; ${MUTATE_DESC} must FAIL"
  src=internal/db/block_references.go
  cp "$src" "$src.x2bak"
  restore_src() { [ -f "$src.x2bak" ] && mv "$src.x2bak" "$src"; }
  trap 'restore_src; cleanup' EXIT
  perl -0pi -e "s/\Q.Consistency(gocql.EachQuorum)\E/.Consistency(gocql.${MUTATE_TO})/" "$src"
  cmp -s "$src" "$src.x2bak" && fail "mutation did not apply — the EACH_QUORUM pin moved?"

  build_divergence
  set +e
  if [ "$MUTATE_TO" = "Quorum" ]; then
    # Take away the ONLY datacenter holding the reference. EACH_QUORUM must error
    # here; QUORUM is satisfied by the two blind DCs and answers "no references".
    "${COMPOSE[@]}" stop cassandra-eu
    require_stopped eu "before the mutated read"
    out="$(docker_go_with X2_EXPECT_REFERENCE_DC_DOWN=1 test -tags integration -count=1 ./internal/integration/ -run "$MUTATE_LEG" -v 2>&1)"
    rc=$?
    require_stopped eu "for the whole mutated read"
    "${COMPOSE[@]}" start cassandra-eu
    wait_healthy eu
  else
    out="$(docker_go test -tags integration -count=1 ./internal/integration/ -run "$MUTATE_LEG" -v 2>&1)"
    rc=$?
  fi
  set -e
  echo "$out" | grep -E '^--- (PASS|FAIL)|X2 REGRESSION' || true
  restore_src
  trap cleanup EXIT

  # Three separate things have to be true, and grepping for the message alone proves
  # only the third. A mutation run is the formal evidence that a data-loss guard can
  # detect its own defect, so it should not rest on "the string appeared somewhere in
  # the output" — today X2 REGRESSION comes only from t.Fatalf, but that is a property
  # of the current tests, not something this script checks.
  #
  #   1. the package run FAILED           (not: skipped, not: built and passed)
  #   2. the TARGET leg failed            (not: some other test in the package)
  #   3. it failed for the X2 reason      (not: a compile error or a broken fixture)
  [ "$rc" -ne 0 ] \
    || fail "${MUTATE_DESC} exited ZERO under a ${MUTATE_TO} destructive read — the mutation did not take effect, or the test never ran"
  grep -qE "^--- FAIL: ${MUTATE_LEG}( |$)" <<<"$out" \
    || fail "the package failed under a ${MUTATE_TO} destructive read, but not at ${MUTATE_LEG} — check the output above for a build or fixture failure masquerading as evidence"
  grep -q 'X2 REGRESSION' <<<"$out" \
    || fail "${MUTATE_LEG} failed under a ${MUTATE_TO} destructive read, but NOT with an X2 REGRESSION assertion — it failed for some other reason and proves nothing"
  printf '\n\033[32mMutation confirmed: %s goes red when the destructive read is downgraded to %s.\033[0m\n' "$MUTATE_DESC" "$MUTATE_TO"
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
docker_go_with \
  CASSANDRA_HOSTS="${X2_TEST_HOST}:${CASSANDRA_NA_HOST_PORT:-9242}" \
  CASSANDRA_LOCAL_DC=dc-na \
  CASSANDRA_REPLICATION_CLASS=NetworkTopologyStrategy \
  CASSANDRA_REPLICATION_DCS=dc-na:1,dc-eu:1,dc-asia:1 \
  run ./cmd/sesamefs migrate

# ---------------------------------------------------------------------------
# P3 writer-fence legs, on the same fixture.
#
# Placed here deliberately: after the schema, BEFORE X2's hinted-handoff disable and
# divergence build. P3 needs a healthy cluster and a schema and nothing else, and an
# earlier version of this block sat after the X2 legs -- so `--p3` quietly ran the
# whole X2 suite first and measured P3 against whatever fixture state X2 left behind.
# These legs exit as soon as they are done, so nothing below runs in P3 mode.
#
# X2 asks whether a destructive READ intersects every datacenter. P3 asks the
# mirror question about the fence WRITE: can GC condemn an incarnation whose fence
# a writer in another datacenter cannot see? The answer must be no, and the proof
# has a different shape from X2's.
#
# X2 builds a divergent cluster and reads it two ways. P3 cannot: with a DC down an
# EACH_QUORUM publication does not complete at all. That is the property, not an
# obstacle -- the publication either obtains a quorum in every DC, or it fails and
# nothing is condemned. So the green leg asserts the failure, and the mutations
# show that the weaker levels succeed and leave dc-na blind.
#
# --p3-mutate-quorum is the one that needs three datacenters. At two DCs with RF 1,
# QUORUM is 2 of 2 and fails with a DC down exactly like EACH_QUORUM, so a two-DC
# fixture cannot tell the right fix from the wrong one.
if [ "${DO_P3:-0}" = "1" ]; then
  for n in "${NODES[@]}"; do wait_healthy "$n"; done

  if [ "${P3_MUTATE:-0}" = "1" ]; then
    step "P3 MUTATION -- downgrade the fence publishers to ${MUTATE_TO}; the fail-closed leg must FAIL"
    psrc=internal/gc/store_cassandra.go
    cp "$psrc" "$psrc.p3bak"
    restore_p3src() { [ -f "$psrc.p3bak" ] && mv "$psrc.p3bak" "$psrc"; }
    trap 'restore_p3src; cleanup' EXIT
    # No leading dot, unlike X2's mutation. block_references.go writes
    # `.Consistency(gocql.EachQuorum)` inline; the GC store puts the call on its own
    # line, so the dot belongs to the previous line and a pattern anchored on it
    # silently matches nothing here. The cmp below is what caught that.
    perl -0pi -e "s/\QConsistency(gocql.EachQuorum)\E/Consistency(gocql.${MUTATE_TO})/g" "$psrc"
    cmp -s "$psrc" "$psrc.p3bak" && fail "P3 mutation did not apply -- did the EACH_QUORUM pins move out of $psrc?"
  fi

  step "P3 LEG 1 -- with dc-na stopped, neither fence publication may succeed"
  point_harness_at "${CASSANDRA_EU_HOST_PORT:-9243}" dc-eu
  "${COMPOSE[@]}" stop cassandra-na
  require_stopped na "for the whole P3 publication attempt"

  set +e
  p3out="$(docker_go_with P3_EXPECT_DC_DOWN=1 test -tags integration -count=1 ./internal/integration/ -run TestP3_FencePublicationFailsClosedWhenADatacenterIsDown -v 2>&1)"
  p3rc=$?
  set -e
  require_stopped na "for the whole P3 publication attempt"
  "${COMPOSE[@]}" start cassandra-na
  wait_healthy na
  point_harness_at "${CASSANDRA_NA_HOST_PORT:-9242}" dc-na
  echo "$p3out"

  if [ "${P3_MUTATE:-0}" = "1" ]; then
    restore_p3src
    trap cleanup EXIT
    grep -qE "^--- FAIL: TestP3_FencePublicationFailsClosedWhenADatacenterIsDown( |$)" <<<"$p3out" \
      || fail "the publishers were downgraded to ${MUTATE_TO} but the fail-closed leg did not fail -- it proves nothing"
    grep -q "P3 REGRESSION" <<<"$p3out" \
      || fail "the leg failed under ${MUTATE_TO} publishers but NOT with a P3 REGRESSION assertion -- it failed for some other reason"
    printf '\n\033[32mP3 mutation confirmed: at %s the fence publishes while dc-na cannot see it, and the leg goes RED.\033[0m\n' "$MUTATE_TO"
    exit 0
  fi

  grep -qE "^--- PASS: TestP3_FencePublicationFailsClosedWhenADatacenterIsDown" <<<"$p3out" \
    || fail "P3 leg 1 -- no PASS line (skipped, or never ran)"
  [ $p3rc -eq 0 ] || fail "P3 leg 1 -- the test passed but the package run failed; see above"

  step "P3 LEG 2 -- a fence published in dc-eu blocks a writer reading from dc-na"
  run_leg "P3 leg 2 (cross-DC fence visibility)" TestP3_WriterInAnotherDatacenterObservesTheFence

  printf '\n\033[32mP3 cross-DC legs green (fail-closed publication, cross-DC fence visibility).\033[0m\n'
  printf 'Mutations are separate entry points: --p3-mutate (vs LOCAL_QUORUM), --p3-mutate-quorum (vs QUORUM, the leg that needs three DCs).\n'
  exit 0
fi

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

step "6b. LEG 2b — the DC holding the ONLY reference is down; the read must ERROR, never zero"
# A FRESH divergence is mandatory. Leg 1's EACH_QUORUM read performed blocking read
# repair to satisfy its own consistency level, so the row it was testing now exists in
# dc-na — and against a healed cluster this leg would pass for the wrong reason,
# proving nothing about which consistency level was used.
build_divergence
"${COMPOSE[@]}" stop cassandra-eu
require_stopped eu "before the leg 2b read"
X2_EXPECT_REFERENCE_DC_DOWN=1 run_leg "leg 2b (fail closed with the reference DC down)" "$LEG2B"
require_stopped eu "for the whole leg 2b read"
"${COMPOSE[@]}" start cassandra-eu
wait_healthy eu
point_harness_at "${CASSANDRA_NA_HOST_PORT:-9242}" dc-na

step "7. LEG 3 — topology gate: accepts the declared map, refuses an under-declared one"
run_leg "leg 3a (gate accepts the declared 3-DC map)" TestX2_TopologyGateAcceptsThreeDCNetworkTopology
run_leg "leg 3b (gate refuses an under-declared map)" TestX2_TopologyGateRejectsAnUnderDeclaredMap

printf '\n\033[32mAll X2 closure legs green (1, 2, 2b, 3a, 3b).\033[0m\n'
printf 'Mutation legs are separate entry points: --mutate (leg 1 vs LOCAL_QUORUM), --mutate-quorum (leg 2b vs QUORUM).\n'
printf 'divergent org=%s block=%s\n' "$X2_DIVERGENT_ORG" "$X2_DIVERGENT_BLOCK"
