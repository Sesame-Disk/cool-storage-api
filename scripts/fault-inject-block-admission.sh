#!/bin/bash
#
# Fault injection for subcontract B (= registry X10) of
# ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01: does the real desktop sync client survive
# the block-PUT admission gate refusing it?
#
# The gate answers 503 + Retry-After — deliberately not 429, which the official
# client does not treat as retryable. That choice is only worth anything if the
# client actually recovers, and no unit test can establish it. This drives real
# seaf-cli against a node whose caps are squeezed to the point where refusal is
# unavoidable, and checks that:
#
#   1. the sync reaches 'synchronized', and
#   2. the server's own counters confirm it really was refused, and
#   3. no admission is left stranded afterwards.
#
# Check 2 is the load-bearing one. scripts/test-sync.sh syncs a handful of small
# files and its whole suite issues about two block PUTs, never concurrently — it
# passes against any cap and proves nothing. Every "the client was never
# refused" failure below means the run did not test anything, and is reported as
# a failure rather than a pass for exactly that reason.
#
# Run it through compose (the service pins the squeezed node):
#
#   docker compose --profile test run --rm block-admission-fault-test
#
# The target node must be started with tight caps. Node 3 ships at 2/2/250ms for
# the integration suite; for this, squeeze it further:
#   SEAFHTTP_SYNC_BLOCK_MAX_INFLIGHT_PER_NODE=1
#   SEAFHTTP_SYNC_BLOCK_MAX_INFLIGHT_PER_USER=1
#   SEAFHTTP_SYNC_BLOCK_ADMISSION_WAIT=0s
#
# KNOWN LIMITATION (2026-07-30): this script does not yet reliably drive
# seaf-cli onto the block route from a cold container. It frequently registers
# the sync and then sits in "waiting for sync" with zero block PUTs, and reports
# that as a failure — correctly, since such a run tests nothing, but it means
# the script is not yet a push-button regression.
#
# The result it is meant to capture HAS been observed manually: against node 3 at
# 1/1/0s, real seaf-cli took 22 x 503, 26 admissions, reached 'synchronized', and
# left sync_put_block_inflight_current at 0. Reproducing that by hand is
# currently more reliable than this script:
#
#   docker compose --profile test run --rm --entrypoint /bin/bash sync-test -lc '
#     rid=$(curl -s -X POST http://sesamefs-node-3:8080/api/v2.1/repos/ \
#       -H "Authorization: Token dev-token-admin" -H "Content-Type: application/json" \
#       -d "{\"name\":\"fi-1\"}" | sed "s/.*\"repo_id\":\"\([^\"]*\)\".*/\1/")
#     d=/seafile-data/fi-1; mkdir -p $d
#     dd if=/dev/urandom of=$d/a.bin bs=1M count=6 2>/dev/null
#     seaf-cli start -c /home/seafuser/.ccnet; sleep 3
#     seaf-cli sync -c /home/seafuser/.ccnet -l "$rid" -s http://sesamefs-node-3:8080 \
#       -d "$d" -u 00000000-0000-0000-0000-000000000001 -p dev-token-123
#     for i in $(seq 8); do sleep 10; seaf-cli status -c /home/seafuser/.ccnet; done'
#
# then read sync_put_block_admission_rejected_total off the node's /metrics.
# Making this script match that reliability is the remaining work on subcontract
# B's client-recovery criterion.
#
set -u

SESAMEFS_URL="${SESAMEFS_URL:-http://sesamefs:8080}"
SESAMEFS_URL_LOCAL="${SESAMEFS_URL_LOCAL:-${SESAMEFS_URL}}"
DEV_API_TOKEN="${DEV_API_TOKEN:-dev-token-admin}"
DEV_PASSWORD="${DEV_PASSWORD:-dev-token-123}"
DEV_USER="${DEV_USER:-00000000-0000-0000-0000-000000000001}"
SYNC_CONFIG_DIR="${SYNC_CONFIG_DIR:-/home/seafuser/.ccnet}"
SYNC_DATA_DIR="${SYNC_DATA_DIR:-/seafile-data}"

# Big enough that the client indexes into several blocks and runs its block
# threads concurrently, small enough to finish quickly.
FILE_COUNT="${FILE_COUNT:-3}"
FILE_MIB="${FILE_MIB:-8}"
SYNC_TIMEOUT="${SYNC_TIMEOUT:-240}"

LIBRARY_PREFIX="fault-inject-block-admission"

log()  { printf '[fault-inject] %s\n' "$*"; }
fail() { printf '[fault-inject] FAIL: %s\n' "$*"; exit 1; }

metric() {
  # $1 = metric line prefix. Prints the value, or 0 when absent.
  # Accept-Encoding: identity because /metrics comes back double-gzipped when
  # the client negotiates compression (promhttp gzips, and the engine's gzip
  # middleware does not exclude the path).
  wget -qO- --header='Accept-Encoding: identity' "${SESAMEFS_URL}/metrics" 2>/dev/null \
    | awk -v p="$1" 'index($0, p) == 1 { print $2; found=1 } END { if (!found) print 0 }' \
    | head -1
}

delete_library() {
  [ -n "${1:-}" ] || return 0
  curl -s -o /dev/null -X DELETE "${SESAMEFS_URL_LOCAL}/api/v2.1/repos/${1}/" \
    -H "Authorization: Token ${DEV_API_TOKEN}"
}

# Cleanup runs on every exit path, including the failures above.
#
# It is not hygiene, it is a prerequisite: the dev account's library quota is a
# low single digit, so a run that leaves its library behind makes the *next* run
# fail to create one. Before the shape check on repo_id existed, that failure
# was silent and surfaced as a bogus "the client was never refused".
CURRENT_REPO_ID=""
CURRENT_SYNC_DIR=""
cleanup() {
  status=$?
  if [ -n "${CURRENT_SYNC_DIR}" ]; then
    seaf-cli desync -c "${SYNC_CONFIG_DIR}" -d "${CURRENT_SYNC_DIR}" >/dev/null 2>&1
    rm -rf "${CURRENT_SYNC_DIR}" >/dev/null 2>&1
  fi
  delete_library "${CURRENT_REPO_ID}"
  exit "${status}"
}
trap cleanup EXIT INT TERM

# Sweep anything a previous interrupted run left behind, so the quota is free.
sweep_previous_runs() {
  log "sweeping libraries left by previous runs"
  curl -s "${SESAMEFS_URL_LOCAL}/api/v2.1/repos/" -H "Authorization: Token ${DEV_API_TOKEN}" \
    | tr '{' '\n' \
    | grep -F "${LIBRARY_PREFIX}" \
    | sed -n 's/.*"repo_id":"\([^"]*\)".*/\1/p' \
    | sort -u \
    | while read -r stale_repo; do
        [ -n "${stale_repo}" ] || continue
        log "  deleting stale library ${stale_repo}"
        delete_library "${stale_repo}"
      done
}

log "target ${SESAMEFS_URL}"
sweep_previous_runs

refused_before=$(metric 'sync_put_block_admission_rejected_total{reason="user"}')
node_refused_before=$(metric 'sync_put_block_admission_rejected_total{reason="node"}')
log "refusals before: user=${refused_before} node=${node_refused_before}"

repo_name="${LIBRARY_PREFIX}-$(date +%s)"
create_response=$(curl -s -X POST "${SESAMEFS_URL_LOCAL}/api/v2.1/repos/" \
  -H "Authorization: Token ${DEV_API_TOKEN}" \
  -H "Content-Type: application/json" \
  -d "{\"name\":\"${repo_name}\"}")
repo_id=$(printf '%s' "${create_response}" | sed -n 's/.*"repo_id":"\([^"]*\)".*/\1/p')

# Check the shape, not just non-emptiness: a failed create returns a JSON error
# body, and a bare -n test accepts that body as an id. The sync then registers
# against a garbage id and sits in "waiting for sync" having uploaded nothing,
# which reads as "the client was never refused" instead of a setup failure.
case "${repo_id}" in
  ????????-????-????-????-????????????) ;;
  *) fail "could not create library: ${create_response}" ;;
esac
CURRENT_REPO_ID="${repo_id}"
log "library ${repo_id}"

sync_dir="${SYNC_DATA_DIR}/${repo_name}"
CURRENT_SYNC_DIR="${sync_dir}"
mkdir -p "${sync_dir}" || fail "could not create ${sync_dir}"

log "generating ${FILE_COUNT} x ${FILE_MIB} MiB of incompressible data"
i=0
while [ "${i}" -lt "${FILE_COUNT}" ]; do
  dd if=/dev/urandom of="${sync_dir}/payload-${i}.bin" bs=1M count="${FILE_MIB}" 2>/dev/null
  i=$((i + 1))
done

# Initialise the config on a fresh volume. `seaf-cli init` exits early when the
# directory already exists, so the files are written directly — same approach as
# docker/seafile-cli/scripts/seaf-test.sh. Without this the daemon never starts
# and every later check measures an idle client.
if [ ! -f "${SYNC_CONFIG_DIR}/seafile.ini" ]; then
  log "initialising seafile client config at ${SYNC_CONFIG_DIR}"
  mkdir -p "${SYNC_CONFIG_DIR}/logs" "${SYNC_DATA_DIR}/seafile-data" || fail "could not create client config dirs"
  echo "${SYNC_DATA_DIR}/seafile-data" > "${SYNC_CONFIG_DIR}/seafile.ini"
fi

# Start the daemon and confirm it answers before registering anything. Do NOT
# stop it first: a stop/start cycle in a fresh container leaves the daemon
# accepting the sync command and then never uploading.
seaf-cli start -c "${SYNC_CONFIG_DIR}" >/dev/null 2>&1
daemon_up=0
waited=0
while [ "${waited}" -lt 60 ]; do
  if seaf-cli status -c "${SYNC_CONFIG_DIR}" >/dev/null 2>&1; then
    daemon_up=1
    break
  fi
  sleep 2
  waited=$((waited + 2))
done
[ "${daemon_up}" -eq 1 ] || fail "seafile daemon did not come up within 60s"

# The ~/.ccnet and /seafile-data volumes outlive the container, so previous runs
# can leave sync entries whose libraries no longer exist. Those wedge later
# syncs in "waiting for sync".
for stale in $(seaf-cli status -c "${SYNC_CONFIG_DIR}" 2>/dev/null | tail -n +2 | awk '{print $1}'); do
  [ -n "${stale}" ] || continue
  log "desyncing stale entry ${stale}"
  seaf-cli desync -c "${SYNC_CONFIG_DIR}" -d "${SYNC_DATA_DIR}/${stale}" >/dev/null 2>&1
done

sync_state() {
  # seaf-cli status lists the sync *directory* basename, which is why the local
  # directory is named after the library. The state is multi-word ("waiting for
  # sync", "unknown error"), so everything after the first column is the state —
  # taking only $2 turns "unknown error" into "unknown" and hides real failures.
  seaf-cli status -c "${SYNC_CONFIG_DIR}" 2>/dev/null \
    | grep -F "${repo_name}" | head -1 \
    | sed 's/^[^[:space:]]*[[:space:]]*//' \
    | sed 's/[[:space:]]*$//'
}

block_activity() {
  a=$(metric 'sync_put_block_admission_wait_seconds_count{outcome="admitted"}')
  r=$(metric 'sync_put_block_admission_wait_seconds_count{outcome="rejected"}')
  awk -v a="${a}" -v r="${r}" 'BEGIN{print a+r}'
}

log "syncing"
seaf-cli sync -c "${SYNC_CONFIG_DIR}" -l "${repo_id}" -s "${SESAMEFS_URL}" \
  -d "${sync_dir}" -u "${DEV_USER}" -p "${DEV_PASSWORD}" \
  || fail "seaf-cli sync command rejected"

# Confirm the entry actually registered. The CLI can return 0 and leave nothing
# behind, and every later check would then be measuring an idle daemon.
registered=0
waited=0
while [ "${waited}" -lt 60 ]; do
  [ -n "$(sync_state)" ] && { registered=1; break; }
  sleep 2
  waited=$((waited + 2))
done
[ "${registered}" -eq 1 ] || fail "seaf-cli accepted the sync but never registered it; nothing would have been uploaded"

# Wait for the client to actually reach the block route before judging anything.
# seaf-cli reports 'synchronized' as soon as the sync is registered, *before* it
# has indexed the directory and uploaded, so breaking on that first
# 'synchronized' yields an instantaneous clean run that never touched the gate.
activity_baseline=$(block_activity)
waited=0
while [ "${waited}" -lt "${SYNC_TIMEOUT}" ]; do
  now=$(block_activity)
  if [ "$(awk -v n="${now}" -v b="${activity_baseline}" 'BEGIN{print (n > b) ? 1 : 0}')" -eq 1 ]; then
    log "client reached the block route after ${waited}s"
    break
  fi
  case "$(sync_state)" in
    *error*) fail "client reported a sync error before uploading any block" ;;
  esac
  sleep 5
  waited=$((waited + 5))
done

# Then wait for it to settle: 'synchronized' must hold across consecutive polls,
# since it flickers between block batches.
stable=0
state=""
while [ "${waited}" -lt "${SYNC_TIMEOUT}" ]; do
  state=$(sync_state)
  case "${state}" in
    synchronized)
      stable=$((stable + 1))
      [ "${stable}" -ge 3 ] && break
      ;;
    *error*) fail "client reported sync state '${state}' under admission pressure" ;;
    *) stable=0 ;;
  esac
  sleep 5
  waited=$((waited + 5))
done
log "sync loop ended after ${waited}s in state '${state}'"

refused_after=$(metric 'sync_put_block_admission_rejected_total{reason="user"}')
node_refused_after=$(metric 'sync_put_block_admission_rejected_total{reason="node"}')
admitted=$(metric 'sync_put_block_admission_wait_seconds_count{outcome="admitted"}')
inflight=$(metric 'sync_put_block_inflight_current')

refused_delta=$(awk -v a="${refused_after}" -v b="${refused_before}" 'BEGIN{print a-b}')
node_delta=$(awk -v a="${node_refused_after}" -v b="${node_refused_before}" 'BEGIN{print a-b}')
total_refused=$(awk -v a="${refused_delta}" -v b="${node_delta}" 'BEGIN{print a+b}')

log "refusals during run: user=${refused_delta} node=${node_delta}"
log "admitted total=${admitted}, inflight now=${inflight}, final state='${state}'"

if [ "$(awk -v v="${total_refused}" 'BEGIN{print (v > 0) ? 1 : 0}')" -ne 1 ]; then
  fail "the client was never refused, so this run tested nothing; tighten the node's caps or grow the payload"
fi

[ "${state}" = "synchronized" ] || fail "sync did not reach 'synchronized' within ${SYNC_TIMEOUT}s (last state '${state}')"

# A stranded admission would quietly cost the node capacity after every spike.
if [ "$(awk -v v="${inflight}" 'BEGIN{print (v == 0) ? 1 : 0}')" -ne 1 ]; then
  fail "sync_put_block_inflight_current is ${inflight} after the run; admissions leaked"
fi

log "PASS: client absorbed ${total_refused} x 503 and completed the sync"
