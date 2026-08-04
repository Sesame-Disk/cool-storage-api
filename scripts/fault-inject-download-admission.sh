#!/bin/bash
#
# Fault injection for subcontract D of ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01:
# does the real desktop sync client survive the download-admission gate refusing
# its block GETs?
#
# D answers 503 + Retry-After on every profile — deliberately not 429, which the
# official client does not treat as retryable. Subcontract B proved that choice
# on the PUT side of the same route with real seaf-cli. Criterion 11 needs the
# same evidence for the read side, and no unit test can produce it: the question
# is what the client does, not what the server returns.
#
# The drill saturates the download budget for a bounded window while the client
# is pulling a library down, then checks that:
#
#   1. the sync reaches 'synchronized' after the window closes, and
#   2. the server's own counters confirm it really was refused, and
#   3. no admission is left stranded afterwards.
#
# Check 2 is the load-bearing one. A client that was never refused proves
# nothing, so that outcome is reported as a failure rather than a pass — the
# same rule the B and C drills use, and for the same reason.
#
# Run it through compose:
#
#   docker compose --profile test run --rm --build download-admission-fault-test
#
set -u

SESAMEFS_URL="${SESAMEFS_URL:-http://sesamefs:8080}"
SESAMEFS_URL_LOCAL="${SESAMEFS_URL_LOCAL:-${SESAMEFS_URL}}"
DEV_API_TOKEN="${DEV_API_TOKEN:-dev-token-admin}"
DEV_PASSWORD="${DEV_PASSWORD:-dev-token-123}"
DEV_USER="${DEV_USER:-00000000-0000-0000-0000-000000000001}"
SYNC_CONFIG_DIR="${SYNC_CONFIG_DIR:-/home/seafuser/.ccnet}"
SYNC_DATA_DIR="${SYNC_DATA_DIR:-/seafile-data}"

FILE_COUNT="${FILE_COUNT:-3}"
FILE_MIB="${FILE_MIB:-8}"
SYNC_TIMEOUT="${SYNC_TIMEOUT:-300}"
# Holders occupy the budget for a bounded window and then let go, so the client
# has to be refused and then recover rather than simply failing.
HOLDER_COUNT="${HOLDER_COUNT:-6}"
HOLDER_RATE="${HOLDER_RATE:-2k}"
HOLDER_SECONDS="${HOLDER_SECONDS:-90}"

LIBRARY_PREFIX="fault-inject-download-admission"

log()  { printf '[fault-inject] %s\n' "$*"; }
fail() { printf '[fault-inject] FAIL: %s\n' "$*"; exit 1; }

# Sum an exact metric name, optionally restricted to exact profile/reason labels.
# Rejections are labelled by reason, and the client-recovery assertion must not
# count client_gone as a server-owned retryable refusal.
metric_sum() {
  local body
  body=$(curl -fsS -H 'Accept-Encoding: identity' "${SESAMEFS_URL}/metrics" 2>/dev/null) || return 1
  printf '%s\n' "${body}" \
    | awk -v wanted_metric="$1" -v wanted_profile="${2:-}" -v wanted_reason="${3:-}" '
      function has_label(labels, name, value) {
        return index(labels, name "=\"" value "\"") > 0
      }
      $0 !~ /^#/ {
        metric = $1
        open = index(metric, "{")
        if (open > 0) {
          metric_name = substr(metric, 1, open - 1)
          labels = substr(metric, open + 1, length(metric) - open - 1)
        } else {
          metric_name = metric
          labels = ""
        }
        if (metric_name != wanted_metric) {
          next
        }
        if (wanted_profile != "" && !has_label(labels, "profile", wanted_profile)) {
          next
        }
        if (wanted_reason != "" && !has_label(labels, "reason", wanted_reason)) {
          next
        }
        s += $NF
      }
      END { printf "%.0f\n", s+0 }'
}

retryable_block_rejected() {
  local reason value total=0
  for reason in admission_timeout auth_user_full node_full profile_full; do
    value=$(metric_sum 'download_admission_rejected_by_profile_total' block "${reason}") || return 1
    total=$((total + value))
  done
  printf '%s\n' "${total}"
}

wait_for_active() {
  local want="$1"
  local timeout="$2"
  local waited=0
  local active=0
  while [ "${waited}" -lt "${timeout}" ]; do
    active=$(metric_sum 'download_admission_active_current') || fail "could not scrape active admissions"
    if [ "${active}" -ge "${want}" ]; then
      log "observed ${active} active admissions"
      return 0
    fi
    sleep 1
    waited=$((waited + 1))
  done
  fail "only ${active} active admissions after ${timeout}s; holders did not fill the intended identity budget"
}

delete_library() {
  [ -n "${1:-}" ] || return 0
  curl -s -o /dev/null -X DELETE "${SESAMEFS_URL_LOCAL}/api/v2.1/repos/${1}/" \
    -H "Authorization: Token ${DEV_API_TOKEN}"
}

CURRENT_REPO_ID=""
CURRENT_SYNC_DIR=""

# Cleanup is a prerequisite, not hygiene: the dev account's library quota is a
# low single digit, so a run that leaves its library behind makes the next run
# fail to create one.
cleanup() {
  status=$?
  pkill -f 'curl.*--limit-rate' >/dev/null 2>&1 || true
  seaf-cli stop -c "${SYNC_CONFIG_DIR}" >/dev/null 2>&1 || true
  rm -rf "${CURRENT_SYNC_DIR}" "${SYNC_CONFIG_DIR}" "${SYNC_DATA_DIR}/seafile-data" >/dev/null 2>&1
  delete_library "${CURRENT_REPO_ID}"
  exit "${status}"
}
trap cleanup EXIT INT TERM

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

repo_name="${LIBRARY_PREFIX}-$(date +%s)"
create_response=$(curl -s -X POST "${SESAMEFS_URL_LOCAL}/api/v2.1/repos/" \
  -H "Authorization: Token ${DEV_API_TOKEN}" \
  -H "Content-Type: application/json" \
  -d "{\"name\":\"${repo_name}\"}")
repo_id=$(printf '%s' "${create_response}" | sed -n 's/.*"repo_id":"\([^"]*\)".*/\1/p')

# Check the shape rather than non-emptiness: a failed create returns a JSON error
# body, and a bare -n test would accept that body as an id.
case "${repo_id}" in
  ????????-????-????-????-????????????) ;;
  *) fail "could not create library: ${create_response}" ;;
esac
CURRENT_REPO_ID="${repo_id}"
log "library ${repo_id}"

# Seed content through the API so the client has something to pull down. The
# refusal under test is on the read path, so the upload must not be part of it.
log "seeding ${FILE_COUNT} x ${FILE_MIB} MiB through the upload API"
upload_link=$(curl -s "${SESAMEFS_URL_LOCAL}/api2/repos/${repo_id}/upload-link/?p=/" \
  -H "Authorization: Token ${DEV_API_TOKEN}" | tr -d '"')
[ -n "${upload_link}" ] || fail "could not obtain an upload link"

seed_dir=$(mktemp -d)
for i in $(seq 1 "${FILE_COUNT}"); do
  dd if=/dev/urandom of="${seed_dir}/seed-${i}.bin" bs=1M count="${FILE_MIB}" status=none
  curl -s -o /dev/null -X POST "${upload_link}" \
    -H "Authorization: Token ${DEV_API_TOKEN}" \
    -F "file=@${seed_dir}/seed-${i}.bin" \
    -F "parent_dir=/" || fail "seed upload ${i} failed"
done
rm -rf "${seed_dir}"

first_file=$(curl -s "${SESAMEFS_URL_LOCAL}/api2/repos/${repo_id}/dir/?p=/" \
  -H "Authorization: Token ${DEV_API_TOKEN}" \
  | sed -n 's/.*"name":"\([^"]*\.bin\)".*/\1/p' | head -1)
[ -n "${first_file}" ] || fail "seeded library lists no file to hold slots against"

case "${HOLDER_COUNT}" in
  6) ;;
  *) fail "HOLDER_COUNT must stay 6: the shipped per-user download cap is 6, and extra holders would create false rejections" ;;
esac

active_before=$(metric_sum 'download_admission_active_current') \
  || fail "could not scrape active admissions before the holders"
[ "${active_before}" -eq 0 ] \
  || fail "download probe node is not idle before the holders: active_current=${active_before}"

# Saturate exactly the per-user budget for a bounded window. Extra holders would
# reject before seaf-cli starts and make the evidence impossible to attribute.
log "holding ${HOLDER_COUNT} download slots at ${HOLDER_RATE} for ${HOLDER_SECONDS}s"
for _ in $(seq 1 "${HOLDER_COUNT}"); do
  curl -s -o /dev/null \
    --limit-rate "${HOLDER_RATE}" --max-time "${HOLDER_SECONDS}" \
    -H "Authorization: Token ${DEV_API_TOKEN}" \
    -H 'Accept-Encoding: identity' \
    "${SESAMEFS_URL_LOCAL}/repo/${repo_id}/raw/${first_file}" &
done

wait_for_active "${HOLDER_COUNT}" 30

# Prove the HTTP refusal contract independently while the holders are admitted.
# This probe is intentionally completed before the client baseline below, so its
# refusal cannot be mistaken for seaf-cli evidence.
contract_headers=$(mktemp)
contract_status=$(curl -sS -D "${contract_headers}" -o /dev/null --max-time 10 -w '%{http_code}' \
  -H "Authorization: Token ${DEV_API_TOKEN}" \
  -H 'Accept-Encoding: identity' \
  "${SESAMEFS_URL_LOCAL}/repo/${repo_id}/raw/${first_file}" || true)
contract_retry_after=$(awk 'BEGIN { IGNORECASE=1 } tolower($1) == "retry-after:" { gsub("\r", "", $2); print $2; exit }' "${contract_headers}")
rm -f "${contract_headers}"
if [ "${contract_status}" != "503" ]; then
  fail "saturated refusal returned HTTP ${contract_status:-unknown}, want 503"
fi
case "${contract_retry_after}" in
  ''|*[!0-9]*) fail "saturated 503 carried invalid Retry-After ${contract_retry_after:-empty}" ;;
  0) fail "saturated 503 carried non-positive Retry-After" ;;
esac
log "saturated refusal contract: HTTP ${contract_status} with Retry-After ${contract_retry_after}s"

# Take the baseline only after the holders and the independent contract probe
# are complete. Their requests never retry, so every later retryable block
# refusal is attributable to seaf-cli.
rejected_before=$(metric_sum 'download_admission_rejected_total') \
  || fail "could not scrape ${SESAMEFS_URL}/metrics after holders stabilized"
retryable_block_rejected_before=$(retryable_block_rejected) \
  || fail "could not scrape block-profile rejection metric before the client"
log "rejections after holder stabilization: ${rejected_before}; retryable block-profile: ${retryable_block_rejected_before}"

# Client state is part of the fixture: sharing sync-test's persistent volumes
# made registration depend on stale entries whose libraries no longer existed.
rm -rf "${SYNC_CONFIG_DIR}" "${SYNC_DATA_DIR:?}/"*
mkdir -p "${SYNC_DATA_DIR}" || fail "could not create ${SYNC_DATA_DIR}"
seaf-cli init -c "${SYNC_CONFIG_DIR}" -d "${SYNC_DATA_DIR}" \
  >/tmp/seaf-cli-init.log 2>&1 \
  || fail "seaf-cli init failed: $(cat /tmp/seaf-cli-init.log)"

sync_dir="${SYNC_DATA_DIR}/${repo_name}"
CURRENT_SYNC_DIR="${sync_dir}"
mkdir -p "${sync_dir}" || fail "could not create ${sync_dir}"

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

log "pulling the library down while the budget is saturated"
seaf-cli sync -c "${SYNC_CONFIG_DIR}" -l "${repo_id}" -s "${SESAMEFS_URL}" \
  -d "${sync_dir}" -u "${DEV_USER}" -p "${DEV_PASSWORD}" \
  >/tmp/seaf-cli-sync.log 2>&1 \
  || fail "seaf-cli sync command failed: $(cat /tmp/seaf-cli-sync.log)"

sync_state() {
  seaf-cli status -c "${SYNC_CONFIG_DIR}" 2>/dev/null \
    | awk -v name="${repo_name}" '$1 == name {$1=""; sub(/^[[:space:]]+/, ""); print; exit}'
}

waited=0
state=""
while [ "${waited}" -lt "${SYNC_TIMEOUT}" ]; do
  state=$(sync_state)
  case "${state}" in
    *synchronized*) break ;;
  esac
  sleep 5
  waited=$((waited + 5))
done

wait 2>/dev/null || true

case "${state}" in
  *synchronized*) log "client reached '${state}' after ${waited}s" ;;
  *) fail "client never synchronized (last state: '${state:-unknown}')" ;;
esac

downloaded=$(find "${sync_dir}" -name '*.bin' -type f 2>/dev/null | wc -l)
[ "${downloaded}" -ge "${FILE_COUNT}" ] \
  || fail "client reported synchronized but pulled ${downloaded}/${FILE_COUNT} files"
log "client pulled ${downloaded} files"

rejected_after=$(metric_sum 'download_admission_rejected_total') \
  || fail "could not scrape ${SESAMEFS_URL}/metrics after the run"
retryable_block_rejected_after=$(retryable_block_rejected) \
  || fail "could not scrape block-profile rejection metric after the client"
log "rejections after: ${rejected_after}"

# A run in which nothing was refused did not test anything. Reporting it as a
# pass is how a guard silently stops being exercised.
if [ "${rejected_after}" -le "${rejected_before}" ]; then
  fail "the client was never refused (${rejected_before} -> ${rejected_after}); the drill proved nothing"
fi
if [ "${retryable_block_rejected_after}" -le "${retryable_block_rejected_before}" ]; then
  fail "the client produced no retryable profile=block refusal (${retryable_block_rejected_before} -> ${retryable_block_rejected_after}); client_gone and unrelated reasons are excluded"
fi
log "refusals during the run: $((rejected_after - rejected_before)); retryable block-profile refusals: $((retryable_block_rejected_after - retryable_block_rejected_before))"

# And nothing may be stranded once the load stops.
waited=0
active=1
while [ "${waited}" -lt 90 ]; do
  active=$(metric_sum 'download_admission_active_current') || fail "metrics scrape failed while draining"
  [ "${active}" -eq 0 ] && break
  sleep 3
  waited=$((waited + 3))
done
[ "${active}" -eq 0 ] || fail "admissions did not drain: active_current=${active}"
log "all admissions drained"

log "PASS: real seaf-cli was refused by download admission and recovered"
