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
# Run it through compose (the service pins the squeezed node and uses disposable
# client state):
#
#   docker compose --profile test run --rm block-admission-fault-test
#
# Node 3 ships at 2/2/250ms. Two authenticated, rate-limited PUTs occupy those
# slots before the watched worktree is changed. The test therefore controls the
# refusal window instead of hoping that ordinary storage latency causes one.
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
HOLDER_RATE="${HOLDER_RATE:-8k}"

LIBRARY_PREFIX="fault-inject-block-admission"

log()  { printf '[fault-inject] %s\n' "$*"; }
fail() { printf '[fault-inject] FAIL: %s\n' "$*"; exit 1; }

metric() {
  # $1 = exact metric line prefix. Scrape failures are fatal to the caller;
  # CounterVec series that have never been touched are legitimately absent and
  # read as zero only after a successful scrape.
  local body value
  body=$(curl -fsS -H 'Accept-Encoding: identity' "${SESAMEFS_URL}/metrics" 2>/dev/null) \
    || return 1
  value=$(printf '%s\n' "${body}" | awk -v p="$1" 'index($0, p) == 1 { print $2; exit }')
  printf '%s\n' "${value:-0}"
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
HOLDER_PIDS=""
cleanup() {
  status=$?
  trap - EXIT INT TERM
  for pid in ${HOLDER_PIDS}; do
    kill "${pid}" >/dev/null 2>&1 || true
  done
  for pid in ${HOLDER_PIDS}; do
    wait "${pid}" >/dev/null 2>&1 || true
  done
  if [ -n "${CURRENT_SYNC_DIR}" ]; then
    seaf-cli desync -c "${SYNC_CONFIG_DIR}" -d "${CURRENT_SYNC_DIR}" >/dev/null 2>&1
  fi
  seaf-cli stop -c "${SYNC_CONFIG_DIR}" >/dev/null 2>&1 || true
  if [ "${status}" -ne 0 ] && [ -f "${SYNC_CONFIG_DIR}/logs/seafile.log" ]; then
    log "last seafile.log lines:"
    tail -80 "${SYNC_CONFIG_DIR}/logs/seafile.log" 2>/dev/null || true
  fi
  rm -rf "${CURRENT_SYNC_DIR}" "${SYNC_CONFIG_DIR}" "${SYNC_DATA_DIR}/seafile-data" >/dev/null 2>&1
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

# A clean client database is part of the fixture. Sharing sync-test's persistent
# volumes made registration depend on stale entries whose libraries no longer
# existed, which is what produced the misleading "waiting for sync" runs.
rm -rf "${SYNC_CONFIG_DIR}" "${SYNC_DATA_DIR:?}/"*
mkdir -p "${SYNC_DATA_DIR}" || fail "could not create ${SYNC_DATA_DIR}"
log "initialising seafile client config at ${SYNC_CONFIG_DIR}"
seaf-cli init -c "${SYNC_CONFIG_DIR}" -d "${SYNC_DATA_DIR}" \
  >/tmp/seaf-cli-init.log 2>&1 \
  || fail "seaf-cli init failed: $(cat /tmp/seaf-cli-init.log)"
[ -s "${SYNC_CONFIG_DIR}/seafile.ini" ] \
  || fail "seaf-cli init did not create seafile.ini"

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

sync_state() {
  seaf-cli status -c "${SYNC_CONFIG_DIR}" 2>/dev/null \
    | awk -v name="${repo_name}" '$1 == name {$1=""; sub(/^[[:space:]]+/, ""); print; exit}'
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

# Establish the empty worktree first. Creating payloads before registration made
# upload depend on the initial scanner; changing an already watched worktree is
# the same path a real desktop uses after startup.
log "waiting for the empty worktree to synchronize"
stable=0
waited=0
state=""
while [ "${waited}" -lt 90 ]; do
  state=$(sync_state)
  case "${state}" in
    synchronized)
      stable=$((stable + 1))
      [ "${stable}" -ge 3 ] && break
      ;;
    *error*) fail "client reported initial sync state '${state}'" ;;
    *) stable=0 ;;
  esac
  sleep 2
  waited=$((waited + 2))
done
[ "${stable}" -ge 3 ] || fail "empty worktree did not reach stable synchronized state (last '${state}')"

inflight=$(metric 'sync_put_block_inflight_current') || fail "could not read initial in-flight metric"
[ "$(awk -v v="${inflight}" 'BEGIN{print (v == 0) ? 1 : 0}')" -eq 1 ] \
  || fail "fixture is not idle before saturation: inflight=${inflight}"

# Hold both node-3 admissions open with slow authenticated bodies. The block hash
# is valid, although holders are terminated as soon as the real client is seen
# being refused.
holder_file=/tmp/block-admission-holder.bin
dd if=/dev/urandom of="${holder_file}" bs=1M count=1 2>/dev/null \
  || fail "could not create holder body"
holder_hash=$(sha256sum "${holder_file}" | awk '{print $1}')
log "occupying both block-PUT admissions"
for n in 1 2; do
  curl -sS -o "/tmp/block-holder-${n}.body" -w '%{http_code}' -X PUT \
    "${SESAMEFS_URL}/seafhttp/repo/${repo_id}/block/${holder_hash}" \
    -H "Authorization: Token ${DEV_API_TOKEN}" \
    -H "Content-Type: application/octet-stream" \
    --limit-rate "${HOLDER_RATE}" --upload-file "${holder_file}" >"/tmp/block-holder-${n}.status" &
  HOLDER_PIDS="${HOLDER_PIDS} $!"
done

waited=0
while [ "${waited}" -lt 30 ]; do
  inflight=$(metric 'sync_put_block_inflight_current') || fail "could not read in-flight metric while saturating"
  [ "$(awk -v v="${inflight}" 'BEGIN{print (v == 2) ? 1 : 0}')" -eq 1 ] && break
  sleep 1
  waited=$((waited + 1))
done
[ "$(awk -v v="${inflight}" 'BEGIN{print (v == 2) ? 1 : 0}')" -eq 1 ] \
  || fail "could not deterministically occupy both admissions (inflight=${inflight}; holder statuses $(cat /tmp/block-holder-*.status 2>/dev/null))"

# Pin the HTTP response contract independently, then take the client baseline so
# this sentinel cannot be mistaken for desktop activity.
printf sentinel >/tmp/block-admission-sentinel.bin
sentinel_hash=$(sha256sum /tmp/block-admission-sentinel.bin | awk '{print $1}')
sentinel_status=$(curl -sS -D /tmp/block-admission-sentinel.headers \
  -o /tmp/block-admission-sentinel.out -w '%{http_code}' -X PUT \
  "${SESAMEFS_URL}/seafhttp/repo/${repo_id}/block/${sentinel_hash}" \
  -H "Authorization: Token ${DEV_API_TOKEN}" \
  -H "Content-Type: application/octet-stream" \
  --data-binary @/tmp/block-admission-sentinel.bin)
retry_after=$(awk 'tolower($1) == "retry-after:" {gsub(/\r/, "", $2); print $2; exit}' /tmp/block-admission-sentinel.headers)
[ "${sentinel_status}" = "503" ] || fail "saturated sentinel returned ${sentinel_status}, want 503"
case "${retry_after}" in
  ''|*[!0-9]*|0) fail "saturated 503 has invalid Retry-After '${retry_after}'" ;;
esac
log "saturated sentinel returned 503 with Retry-After=${retry_after}"

refused_before=$(metric 'sync_put_block_admission_rejected_total{reason="user"}') || fail "could not read user rejection baseline"
node_refused_before=$(metric 'sync_put_block_admission_rejected_total{reason="node"}') || fail "could not read node rejection baseline"

log "generating ${FILE_COUNT} x ${FILE_MIB} MiB in the watched worktree"
i=0
while [ "${i}" -lt "${FILE_COUNT}" ]; do
  dd if=/dev/urandom of="${sync_dir}/payload-${i}.bin" bs=1M count="${FILE_MIB}" 2>/dev/null \
    || fail "could not create payload ${i}"
  i=$((i + 1))
done

# Do not release the holders until the server proves the real client reached the
# saturated route. No other request is issued after the baseline above.
waited=0
total_refused=0
client_503_seen=0
while [ "${waited}" -lt 90 ]; do
  refused_after=$(metric 'sync_put_block_admission_rejected_total{reason="user"}') || fail "could not read user rejection counter"
  node_refused_after=$(metric 'sync_put_block_admission_rejected_total{reason="node"}') || fail "could not read node rejection counter"
  refused_delta=$(awk -v a="${refused_after}" -v b="${refused_before}" 'BEGIN{print a-b}')
  node_delta=$(awk -v a="${node_refused_after}" -v b="${node_refused_before}" 'BEGIN{print a-b}')
  total_refused=$(awk -v a="${refused_delta}" -v b="${node_delta}" 'BEGIN{print a+b}')
  if grep -F "Bad response code for PUT ${SESAMEFS_URL}/seafhttp/repo/${repo_id}/block/" \
      "${SYNC_CONFIG_DIR}/logs/seafile.log" 2>/dev/null | grep -qF ': 503.'; then
    client_503_seen=1
  fi
  if [ "${client_503_seen}" -eq 1 ] \
      && [ "$(awk -v v="${total_refused}" 'BEGIN{print (v > 0) ? 1 : 0}')" -eq 1 ]; then
    break
  fi
  case "$(sync_state)" in
    "error Network error") ;;
    *error*) fail "client reported an error before reaching the saturated block route" ;;
  esac
  sleep 2
  waited=$((waited + 2))
done
[ "$(awk -v v="${total_refused}" 'BEGIN{print (v > 0) ? 1 : 0}')" -eq 1 ] \
  || fail "server rejection counter did not move for the client's block PUT"
[ "${client_503_seen}" -eq 1 ] \
  || fail "seaf-cli log contains no 503 for this repository's block PUT"
log "real client was refused: user=${refused_delta} node=${node_delta}"

for pid in ${HOLDER_PIDS}; do
  kill "${pid}" >/dev/null 2>&1 || true
done
for pid in ${HOLDER_PIDS}; do
  wait "${pid}" >/dev/null 2>&1 || true
done
HOLDER_PIDS=""
log "released fault; waiting for client retry and recovery"

stable=0
state=""
waited=0
while [ "${waited}" -lt "${SYNC_TIMEOUT}" ]; do
  state=$(sync_state)
  case "${state}" in
    synchronized)
      stable=$((stable + 1))
      [ "${stable}" -ge 3 ] && break
      ;;
    "error Network error")
      # This is the client's transient classification for the injected 503. It
      # remains visible until its network-error retry timer starts the next task.
      stable=0
      ;;
    *error*) fail "client reported sync state '${state}' after fault release" ;;
    *) stable=0 ;;
  esac
  sleep 3
  waited=$((waited + 3))
done
[ "${stable}" -ge 3 ] || fail "sync did not recover within ${SYNC_TIMEOUT}s (last '${state}')"

# A synchronized status alone can precede publication. Download every payload
# through the API and compare bytes to prove the retried blocks became the
# committed remote files.
i=0
while [ "${i}" -lt "${FILE_COUNT}" ]; do
  local_file="${sync_dir}/payload-${i}.bin"
  downloaded="/tmp/payload-${i}.downloaded"
  verified=0
  attempts=0
  while [ "${attempts}" -lt 20 ]; do
    download_link=$(curl -sS "${SESAMEFS_URL_LOCAL}/api2/repos/${repo_id}/file/?p=/payload-${i}.bin" \
      -H "Authorization: Token ${DEV_API_TOKEN}" | tr -d '"')
    if [ -n "${download_link}" ] && [ "${download_link}" != "null" ]; then
      if curl -sS "${download_link}" -H "Authorization: Token ${DEV_API_TOKEN}" -o "${downloaded}" \
        && cmp -s "${local_file}" "${downloaded}"; then
        verified=1
        break
      fi
    fi
    sleep 2
    attempts=$((attempts + 1))
  done
  [ "${verified}" -eq 1 ] || fail "remote payload-${i}.bin is missing or differs"
  i=$((i + 1))
done

# A stranded admission would quietly cost the node capacity after every spike.
waited=0
while [ "${waited}" -lt 60 ]; do
  inflight=$(metric 'sync_put_block_inflight_current') || fail "could not read final in-flight metric"
  [ "$(awk -v v="${inflight}" 'BEGIN{print (v == 0) ? 1 : 0}')" -eq 1 ] && break
  sleep 1
  waited=$((waited + 1))
done
if [ "$(awk -v v="${inflight}" 'BEGIN{print (v == 0) ? 1 : 0}')" -ne 1 ]; then
  fail "sync_put_block_inflight_current is ${inflight} after the run; admissions leaked"
fi

log "PASS: real client absorbed ${total_refused} capacity 503(s), retried, published verified payloads, and drained all slots"
