#!/usr/bin/env bash
# Benchmark: Concurrent user simulation
# Tests how the system handles multiple simultaneous authenticated users
# hitting the account/info endpoint (lightweight, auth-heavy).
#
# Usage:
#   ./benchmark-concurrent-users.sh --host https://sfs.nihaoshares.com --token TOKEN

set -euo pipefail

HOST=""
TOKEN=""
USERS="1 5 10 25 50"
REQUESTS_PER_USER=10
INSECURE=""

usage() {
  echo "benchmark-concurrent-users.sh"
  echo "Usage: $0 --host URL --token TOKEN [--users '1 5 10 25 50'] [--requests 10] [-k]"
}

while [ $# -gt 0 ]; do
  case "$1" in
    --host)     HOST="${2:-}"; shift 2 ;;
    --token)    TOKEN="${2:-}"; shift 2 ;;
    --users)    USERS="${2:-}"; shift 2 ;;
    --requests) REQUESTS_PER_USER="${2:-}"; shift 2 ;;
    -k)         INSECURE="-k"; shift ;;
    -h)         usage; exit 0 ;;
    *)          echo "Unknown: $1"; usage; exit 2 ;;
  esac
done

HOST="${HOST%/}"
[ -z "$HOST" ] || [ -z "$TOKEN" ] && { usage; exit 2; }

calc() { awk "BEGIN{printf \"%.1f\", $1}" 2>/dev/null || echo "?"; }

echo "=========================================="
echo " SesameFS Concurrent Users Benchmark"
echo " Host: $HOST"
echo " Requests/user: $REQUESTS_PER_USER"
echo "=========================================="

RESULTS_DIR=$(mktemp -d)
trap "rm -rf $RESULTS_DIR" EXIT

simulate_user() {
  local user_num=$1
  local count=$2
  local successes=0
  local failures=0
  local total_ms=0

  for i in $(seq 1 "$count"); do
    start=$(date +%s%N)
    code=$(curl -sS $INSECURE -o /dev/null -w "%{http_code}" \
      -H "Authorization: Token $TOKEN" \
      "$HOST/api2/account/info" --max-time 10 2>/dev/null || echo "000")
    end=$(date +%s%N)
    ms=$(( (end - start) / 1000000 ))
    total_ms=$((total_ms + ms))

    if [ "$code" = "200" ]; then
      successes=$((successes + 1))
    else
      failures=$((failures + 1))
    fi
  done

  local avg_ms=$((total_ms / count))
  echo "$successes,$failures,$avg_ms,$total_ms"
}

printf "\n  %-8s  %-10s  %-8s  %-8s  %-10s  %-10s  %-8s\n" \
  "Users" "Requests" "Success" "Fail" "Time(ms)" "Req/s" "Avg(ms)"
echo "  -------  ----------  --------  --------  ----------  ----------  --------"

for user_count in $USERS; do
  total_requests=$((user_count * REQUESTS_PER_USER))

  start=$(date +%s%N)
  pids=()
  for u in $(seq 1 "$user_count"); do
    simulate_user "$u" "$REQUESTS_PER_USER" > "$RESULTS_DIR/user_$u.csv" &
    pids+=($!)
  done
  for pid in "${pids[@]}"; do wait "$pid" 2>/dev/null || true; done
  end=$(date +%s%N)

  wall_ms=$(( (end - start) / 1000000 ))

  total_success=0
  total_fail=0
  total_avg=0
  for f in "$RESULTS_DIR"/user_*.csv; do
    IFS=',' read -r succ fail avg _ < "$f"
    total_success=$((total_success + succ))
    total_fail=$((total_fail + fail))
    total_avg=$((total_avg + avg))
  done
  overall_avg=$((total_avg / user_count))

  if [ "$wall_ms" -gt 0 ]; then
    rps=$(calc "$total_requests * 1000 / $wall_ms")
  else
    rps="inf"
  fi

  printf "  %-8s  %-10s  %-8s  %-8s  %-10s  %-10s  %-8s\n" \
    "$user_count" "$total_requests" "$total_success" "$total_fail" "$wall_ms" "$rps" "${overall_avg}ms"

  rm -f "$RESULTS_DIR"/user_*.csv
done

echo
echo "=========================================="
echo " Benchmark Complete"
echo "=========================================="
