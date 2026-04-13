#!/usr/bin/env bash
# Benchmark: Concurrent user simulation
# Tests how the system handles multiple simultaneous authenticated users.
#
# Usage:
#   ./benchmark-concurrent-users.sh --host http://localhost:8082 --token TOKEN

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
    *)          echo "Unknown: $1"; usage; exit 2 ;;
  esac
done

HOST="${HOST%/}"
[ -z "$HOST" ] || [ -z "$TOKEN" ] && { usage; exit 2; }

echo "=========================================="
echo " SesameFS Concurrent Users Benchmark"
echo " Host: $HOST"
echo " Requests/user: $REQUESTS_PER_USER"
echo "=========================================="

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

  avg_ms=$((total_ms / count))
  echo "$user_num,$successes,$failures,$avg_ms,$total_ms"
}

for user_count in $USERS; do
  echo
  echo "--- $user_count concurrent users x $REQUESTS_PER_USER requests ---"

  start=$(date +%s%N)

  RESULTS_DIR=$(mktemp -d)
  pids=()

  for u in $(seq 1 "$user_count"); do
    simulate_user "$u" "$REQUESTS_PER_USER" > "$RESULTS_DIR/user_$u.csv" &
    pids+=($!)
  done

  for pid in "${pids[@]}"; do wait "$pid" 2>/dev/null || true; done
  end=$(date +%s%N)

  total_elapsed=$(echo "scale=3; ($end - $start) / 1000000000" | bc 2>/dev/null || echo "0")
  total_requests=$((user_count * REQUESTS_PER_USER))
  rps=$(echo "scale=1; $total_requests / $total_elapsed" | bc 2>/dev/null || echo "0")

  total_success=0
  total_fail=0
  total_avg_ms=0

  for f in "$RESULTS_DIR"/user_*.csv; do
    IFS=',' read -r _ succ fail avg _ < "$f"
    total_success=$((total_success + succ))
    total_fail=$((total_fail + fail))
    total_avg_ms=$((total_avg_ms + avg))
  done
  overall_avg_ms=$((total_avg_ms / user_count))

  printf "  Total requests:  %d\n" "$total_requests"
  printf "  Successes:       %d\n" "$total_success"
  printf "  Failures:        %d\n" "$total_fail"
  printf "  Total time:      %ss\n" "$total_elapsed"
  printf "  Throughput:      %s req/s\n" "$rps"
  printf "  Avg latency:     %dms\n" "$overall_avg_ms"

  if [ "$total_fail" -gt 0 ]; then
    fail_pct=$(echo "scale=1; $total_fail * 100 / $total_requests" | bc 2>/dev/null || echo "?")
    printf "  Failure rate:    %s%%\n" "$fail_pct"
  fi

  rm -rf "$RESULTS_DIR"
done

echo
echo "=========================================="
echo " Benchmark Complete"
echo "=========================================="
