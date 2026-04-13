#!/usr/bin/env bash
# Benchmark: Upload and Download throughput
# Measures single-file and concurrent upload/download performance.
#
# Usage:
#   ./benchmark-upload-download.sh --host http://localhost:8082 --token TOKEN --repo REPO
#   ./benchmark-upload-download.sh --host https://sfs.nihaoshares.com --token TOKEN --repo REPO

set -euo pipefail

HOST=""
TOKEN=""
REPO=""
SIZES="1 10 100"  # MB
CONCURRENCY="1 4 8"
INSECURE=""

usage() {
  echo "benchmark-upload-download.sh"
  echo "Usage: $0 --host URL --token TOKEN --repo REPO [--sizes '1 10 100'] [--concurrency '1 4 8'] [-k]"
}

while [ $# -gt 0 ]; do
  case "$1" in
    --host)        HOST="${2:-}"; shift 2 ;;
    --token)       TOKEN="${2:-}"; shift 2 ;;
    --repo)        REPO="${2:-}"; shift 2 ;;
    --sizes)       SIZES="${2:-}"; shift 2 ;;
    --concurrency) CONCURRENCY="${2:-}"; shift 2 ;;
    -k|--insecure) INSECURE="-k"; shift ;;
    -h|--help)     usage; exit 0 ;;
    *)             echo "Unknown arg: $1"; usage; exit 2 ;;
  esac
done

HOST="${HOST%/}"
[ -z "$HOST" ] || [ -z "$TOKEN" ] || [ -z "$REPO" ] && { usage; exit 2; }

echo "=========================================="
echo " SesameFS Upload/Download Benchmark"
echo " Host: $HOST"
echo " Repo: $REPO"
echo "=========================================="

RESULTS_FILE=$(mktemp)
echo "operation,size_mb,concurrency,elapsed_s,throughput_mbps,status" > "$RESULTS_FILE"

# Generate test files
TMPDIR=$(mktemp -d)
trap "rm -rf $TMPDIR" EXIT

for size in $SIZES; do
  dd if=/dev/urandom of="$TMPDIR/test_${size}mb.bin" bs=1M count="$size" 2>/dev/null
done

# Single-file upload benchmark
echo
echo "--- Upload Benchmark ---"
for size in $SIZES; do
  file="$TMPDIR/test_${size}mb.bin"

  # Get upload link
  upload_link=$(curl -sS $INSECURE -H "Authorization: Token $TOKEN" \
    "$HOST/api2/repos/$REPO/upload-link/" 2>/dev/null | tr -d '"')

  if [ -z "$upload_link" ] || echo "$upload_link" | grep -q "error"; then
    echo "  SKIP: Could not get upload link for ${size}MB"
    continue
  fi

  start=$(date +%s%N)
  http_code=$(curl -sS $INSECURE -o /dev/null -w "%{http_code}" \
    -X POST "$upload_link" \
    -H "Authorization: Token $TOKEN" \
    -F "file=@$file;filename=bench_${size}mb.bin" \
    -F "parent_dir=/" \
    -F "replace=1" \
    --max-time 300 2>/dev/null || echo "000")
  end=$(date +%s%N)

  elapsed=$(echo "scale=3; ($end - $start) / 1000000000" | bc 2>/dev/null || echo "0")
  if [ "$elapsed" != "0" ] && [ "$elapsed" != "" ]; then
    throughput=$(echo "scale=2; $size / $elapsed * 8" | bc 2>/dev/null || echo "0")
  else
    throughput="0"
  fi

  printf "  Upload %4dMB: %6ss, %6s Mbps (HTTP %s)\n" "$size" "$elapsed" "$throughput" "$http_code"
  echo "upload,$size,1,$elapsed,$throughput,$http_code" >> "$RESULTS_FILE"
done

# Single-file download benchmark
echo
echo "--- Download Benchmark ---"
for size in $SIZES; do
  # Get download link
  dl_link=$(curl -sS $INSECURE -H "Authorization: Token $TOKEN" \
    "$HOST/api2/repos/$REPO/file/?p=/bench_${size}mb.bin&reuse=1" 2>/dev/null | tr -d '"')

  if [ -z "$dl_link" ] || echo "$dl_link" | grep -q "error"; then
    echo "  SKIP: Could not get download link for ${size}MB (file may not exist)"
    continue
  fi

  start=$(date +%s%N)
  http_code=$(curl -sS $INSECURE -o /dev/null -w "%{http_code}" \
    "$dl_link" --max-time 300 2>/dev/null || echo "000")
  end=$(date +%s%N)

  elapsed=$(echo "scale=3; ($end - $start) / 1000000000" | bc 2>/dev/null || echo "0")
  if [ "$elapsed" != "0" ] && [ "$elapsed" != "" ]; then
    throughput=$(echo "scale=2; $size / $elapsed * 8" | bc 2>/dev/null || echo "0")
  else
    throughput="0"
  fi

  printf "  Download %4dMB: %6ss, %6s Mbps (HTTP %s)\n" "$size" "$elapsed" "$throughput" "$http_code"
  echo "download,$size,1,$elapsed,$throughput,$http_code" >> "$RESULTS_FILE"
done

# Concurrent upload benchmark
echo
echo "--- Concurrent Upload Benchmark (1MB files) ---"
for conc in $CONCURRENCY; do
  file="$TMPDIR/test_1mb.bin"
  start=$(date +%s%N)

  pids=()
  for i in $(seq 1 "$conc"); do
    upload_link=$(curl -sS $INSECURE -H "Authorization: Token $TOKEN" \
      "$HOST/api2/repos/$REPO/upload-link/" 2>/dev/null | tr -d '"')
    curl -sS $INSECURE -o /dev/null \
      -X POST "$upload_link" \
      -H "Authorization: Token $TOKEN" \
      -F "file=@$file;filename=bench_conc_${i}.bin" \
      -F "parent_dir=/" \
      -F "replace=1" \
      --max-time 60 2>/dev/null &
    pids+=($!)
  done

  for pid in "${pids[@]}"; do wait "$pid" 2>/dev/null || true; done
  end=$(date +%s%N)

  elapsed=$(echo "scale=3; ($end - $start) / 1000000000" | bc 2>/dev/null || echo "0")
  total_mb=$conc
  if [ "$elapsed" != "0" ] && [ "$elapsed" != "" ]; then
    throughput=$(echo "scale=2; $total_mb / $elapsed * 8" | bc 2>/dev/null || echo "0")
  else
    throughput="0"
  fi

  printf "  %2d concurrent 1MB uploads: %6ss total, %6s Mbps aggregate\n" "$conc" "$elapsed" "$throughput"
  echo "concurrent_upload,1,$conc,$elapsed,$throughput,200" >> "$RESULTS_FILE"
done

# API latency benchmark
echo
echo "--- API Latency Benchmark ---"
for endpoint in "/api2/ping" "/health" "/ready" "/api/v2.1/bootstrap"; do
  total=0
  count=10
  for i in $(seq 1 $count); do
    ms=$(curl -sS $INSECURE -o /dev/null -w "%{time_total}" \
      "$HOST$endpoint" --max-time 10 2>/dev/null)
    ms_int=$(echo "$ms * 1000" | bc 2>/dev/null | cut -d. -f1)
    total=$((total + ${ms_int:-0}))
  done
  avg=$((total / count))
  printf "  %-35s avg %4dms (%d samples)\n" "$endpoint" "$avg" "$count"
done

echo
echo "=========================================="
echo " Benchmark Complete"
echo " Results: $RESULTS_FILE"
echo "=========================================="
cat "$RESULTS_FILE"
