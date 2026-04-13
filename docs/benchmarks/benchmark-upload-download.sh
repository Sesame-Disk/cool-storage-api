#!/usr/bin/env bash
# Benchmark: Upload and Download throughput
# Measures single-file and concurrent upload/download performance
# using the seafhttp upload-link / file-download flow (same as real clients).
#
# Usage:
#   ./benchmark-upload-download.sh --host https://sfs.nihaoshares.com --token TOKEN --repo REPO

set -euo pipefail

HOST=""
TOKEN=""
REPO=""
SIZES="1 10 100"      # MB
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
    -k)            INSECURE="-k"; shift ;;
    -h)            usage; exit 0 ;;
    *)             echo "Unknown: $1"; usage; exit 2 ;;
  esac
done

HOST="${HOST%/}"
[ -z "$HOST" ] || [ -z "$TOKEN" ] || [ -z "$REPO" ] && { usage; exit 2; }

# Portable calculator (no bc required)
calc() { awk "BEGIN{printf \"%.1f\", $1}" 2>/dev/null || echo "?"; }

AUTH=(-H "Authorization: Token $TOKEN")

echo "=========================================="
echo " SesameFS Upload/Download Benchmark"
echo " Host: $HOST"
echo " Repo: $REPO"
echo "=========================================="

TMPDIR=$(mktemp -d)
trap "rm -rf $TMPDIR" EXIT

# Generate test files
for size in $SIZES; do
  dd if=/dev/urandom of="$TMPDIR/test_${size}mb.bin" bs=1M count="$size" 2>/dev/null
done

# --- Single-file upload ---
echo
echo "--- Single File Upload ---"
for size in $SIZES; do
  file="$TMPDIR/test_${size}mb.bin"

  link=$(curl -sS $INSECURE "${AUTH[@]}" \
    "$HOST/api2/repos/$REPO/upload-link/" 2>/dev/null | tr -d '"')

  if [ -z "$link" ] || echo "$link" | grep -q error; then
    echo "  ${size}MB -> SKIP: could not get upload link"
    continue
  fi

  start=$(date +%s%N)
  code=$(curl -sS $INSECURE -o /dev/null -w "%{http_code}" \
    -X POST "$link" \
    "${AUTH[@]}" \
    -F "file=@$file;filename=bench_${size}mb.bin" \
    -F "parent_dir=/" \
    -F "replace=1" \
    --max-time 300 2>/dev/null || echo "000")
  end=$(date +%s%N)

  ms=$(( (end - start) / 1000000 ))
  if [ "$ms" -gt 0 ]; then
    mbps=$(calc "$size * 8000 / $ms")
  else
    mbps="inf"
  fi

  printf "  %4dMB upload:  %6dms  ~%s Mbps  (HTTP %s)\n" "$size" "$ms" "$mbps" "$code"
done

# --- Single-file download ---
echo
echo "--- Single File Download ---"
for size in $SIZES; do
  link=$(curl -sS $INSECURE "${AUTH[@]}" \
    "$HOST/api2/repos/$REPO/file/?p=/bench_${size}mb.bin&reuse=1" 2>/dev/null | tr -d '"')

  if [ -z "$link" ] || echo "$link" | grep -q error; then
    echo "  ${size}MB -> SKIP: file not found (run upload first)"
    continue
  fi

  start=$(date +%s%N)
  dl_bytes=$(curl -sS $INSECURE -o /dev/null -w "%{size_download}" \
    "$link" --max-time 300 2>/dev/null || echo "0")
  end=$(date +%s%N)

  ms=$(( (end - start) / 1000000 ))
  dl_mb=$(calc "${dl_bytes%.*} / 1048576")
  if [ "$ms" -gt 0 ]; then
    mbps=$(calc "${dl_bytes%.*} * 8000 / $ms / 1048576")
  else
    mbps="inf"
  fi

  printf "  %4dMB download: %6dms  %sMB received  ~%s Mbps\n" "$size" "$ms" "$dl_mb" "$mbps"
done

# --- Concurrent uploads ---
echo
echo "--- Concurrent 1MB Uploads ---"
for conc in $CONCURRENCY; do
  file="$TMPDIR/test_1mb.bin"
  [ -f "$file" ] || dd if=/dev/urandom of="$file" bs=1M count=1 2>/dev/null

  start=$(date +%s%N)
  pids=()
  for i in $(seq 1 "$conc"); do
    (
      link=$(curl -sS $INSECURE "${AUTH[@]}" \
        "$HOST/api2/repos/$REPO/upload-link/" 2>/dev/null | tr -d '"')
      curl -sS $INSECURE -o /dev/null \
        -X POST "$link" \
        "${AUTH[@]}" \
        -F "file=@$file;filename=conc_${i}.bin" \
        -F "parent_dir=/" \
        -F "replace=1" \
        --max-time 60 2>/dev/null
    ) &
    pids+=($!)
  done
  for pid in "${pids[@]}"; do wait "$pid" 2>/dev/null || true; done
  end=$(date +%s%N)

  ms=$(( (end - start) / 1000000 ))
  if [ "$ms" -gt 0 ]; then
    mbps=$(calc "$conc * 8000 / $ms")
  else
    mbps="inf"
  fi

  printf "  %2d concurrent: %6dms total  ~%s Mbps aggregate\n" "$conc" "$ms" "$mbps"
done

# --- Concurrent downloads ---
echo
echo "--- Concurrent 1MB Downloads ---"
for conc in $CONCURRENCY; do
  link=$(curl -sS $INSECURE "${AUTH[@]}" \
    "$HOST/api2/repos/$REPO/file/?p=/bench_1mb.bin&reuse=1" 2>/dev/null | tr -d '"')

  if [ -z "$link" ] || echo "$link" | grep -q error; then
    echo "  SKIP: bench_1mb.bin not found"
    break
  fi

  start=$(date +%s%N)
  pids=()
  for i in $(seq 1 "$conc"); do
    curl -sS $INSECURE -o /dev/null "$link" --max-time 60 2>/dev/null &
    pids+=($!)
  done
  for pid in "${pids[@]}"; do wait "$pid" 2>/dev/null || true; done
  end=$(date +%s%N)

  ms=$(( (end - start) / 1000000 ))
  if [ "$ms" -gt 0 ]; then
    mbps=$(calc "$conc * 8000 / $ms")
  else
    mbps="inf"
  fi

  printf "  %2d concurrent: %6dms total  ~%s Mbps aggregate\n" "$conc" "$ms" "$mbps"
done

echo
echo "=========================================="
echo " Benchmark Complete"
echo "=========================================="
