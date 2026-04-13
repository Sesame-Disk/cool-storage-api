#!/usr/bin/env bash
# Benchmark: Storage operations (block upload, block download, chunking)
# Measures block-level storage performance for production sizing.
#
# Usage:
#   ./benchmark-storage-ops.sh --host http://localhost:8082 --token TOKEN --repo REPO

set -euo pipefail

HOST=""
TOKEN=""
REPO=""
BLOCK_COUNT=50
INSECURE=""

usage() {
  echo "benchmark-storage-ops.sh"
  echo "Usage: $0 --host URL --token TOKEN --repo REPO [--blocks 50] [-k]"
}

while [ $# -gt 0 ]; do
  case "$1" in
    --host)   HOST="${2:-}"; shift 2 ;;
    --token)  TOKEN="${2:-}"; shift 2 ;;
    --repo)   REPO="${2:-}"; shift 2 ;;
    --blocks) BLOCK_COUNT="${2:-}"; shift 2 ;;
    -k)       INSECURE="-k"; shift ;;
    *)        echo "Unknown: $1"; usage; exit 2 ;;
  esac
done

HOST="${HOST%/}"
[ -z "$HOST" ] || [ -z "$TOKEN" ] || [ -z "$REPO" ] && { usage; exit 2; }

echo "=========================================="
echo " SesameFS Storage Operations Benchmark"
echo " Host: $HOST"
echo " Repo: $REPO"
echo " Block count: $BLOCK_COUNT"
echo "=========================================="

TMPDIR=$(mktemp -d)
trap "rm -rf $TMPDIR" EXIT

# Generate random blocks of various sizes
echo
echo "--- Block Upload Performance ---"
for block_size_kb in 64 256 1024 4096; do
  dd if=/dev/urandom of="$TMPDIR/block_${block_size_kb}k.bin" bs=1K count="$block_size_kb" 2>/dev/null
  file="$TMPDIR/block_${block_size_kb}k.bin"

  # Compute SHA-256 hash
  hash=$(sha256sum "$file" | cut -d' ' -f1)

  start=$(date +%s%N)
  code=$(curl -sS $INSECURE -o /dev/null -w "%{http_code}" \
    -X PUT "$HOST/api/v2/repos/$REPO/blocks/$hash" \
    -H "Authorization: Token $TOKEN" \
    -H "Content-Type: application/octet-stream" \
    --data-binary "@$file" \
    --max-time 30 2>/dev/null || echo "000")
  end=$(date +%s%N)

  ms=$(( (end - start) / 1000000 ))
  printf "  Upload %5dKB block: %5dms (HTTP %s, hash=%s...)\n" "$block_size_kb" "$ms" "$code" "${hash:0:16}"
done

# API endpoint latency profile
echo
echo "--- Endpoint Latency Profile ---"
ENDPOINTS=(
  "GET /api2/ping"
  "GET /health"
  "GET /ready"
  "GET /api/v2.1/bootstrap"
  "GET /api2/account/info"
  "GET /api2/repos/"
)

for entry in "${ENDPOINTS[@]}"; do
  method=$(echo "$entry" | cut -d' ' -f1)
  path=$(echo "$entry" | cut -d' ' -f2)

  latencies=()
  for i in $(seq 1 20); do
    ms=$(curl -sS $INSECURE -o /dev/null -w "%{time_total}" \
      -X "$method" \
      -H "Authorization: Token $TOKEN" \
      "$HOST$path" --max-time 10 2>/dev/null || echo "1.000")
    ms_int=$(echo "$ms * 1000" | bc 2>/dev/null | cut -d. -f1)
    latencies+=("${ms_int:-0}")
  done

  # Sort and compute p50/p95/p99
  sorted=($(printf '%s\n' "${latencies[@]}" | sort -n))
  count=${#sorted[@]}
  p50=${sorted[$((count * 50 / 100))]}
  p95=${sorted[$((count * 95 / 100))]}
  p99=${sorted[$((count * 99 / 100))]}
  min=${sorted[0]}
  max=${sorted[$((count - 1))]}

  printf "  %-35s p50=%4dms  p95=%4dms  p99=%4dms  min=%4dms  max=%4dms\n" \
    "$method $path" "$p50" "$p95" "$p99" "$min" "$max"
done

# Memory usage snapshot
echo
echo "--- Server Resource Snapshot ---"
if command -v docker &>/dev/null; then
  docker stats --no-stream --format "table {{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}\t{{.NetIO}}" 2>/dev/null | head -10 || echo "  (docker stats unavailable)"
else
  echo "  (docker not available for resource monitoring)"
fi

echo
echo "=========================================="
echo " Benchmark Complete"
echo "=========================================="
