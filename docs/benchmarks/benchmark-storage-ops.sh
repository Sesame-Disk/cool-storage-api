#!/usr/bin/env bash
# Benchmark: Storage operations (block upload, block download, chunking)
# Measures block-level storage performance for production sizing.
#
# Usage:
#   ./benchmark-storage-ops.sh --host https://sfs.nihaoshares.com --token TOKEN --repo REPO

set -euo pipefail

HOST=""
TOKEN=""
REPO=""
INSECURE=""

usage() {
  echo "benchmark-storage-ops.sh"
  echo "Usage: $0 --host URL --token TOKEN --repo REPO [-k]"
}

while [ $# -gt 0 ]; do
  case "$1" in
    --host)   HOST="${2:-}"; shift 2 ;;
    --token)  TOKEN="${2:-}"; shift 2 ;;
    --repo)   REPO="${2:-}"; shift 2 ;;
    -k)       INSECURE="-k"; shift ;;
    -h)       usage; exit 0 ;;
    *)        echo "Unknown: $1"; usage; exit 2 ;;
  esac
done

HOST="${HOST%/}"
[ -z "$HOST" ] || [ -z "$TOKEN" ] || [ -z "$REPO" ] && { usage; exit 2; }

AUTH=(-H "Authorization: Token $TOKEN")

echo "=========================================="
echo " SesameFS Storage Operations Benchmark"
echo " Host: $HOST"
echo " Repo: $REPO"
echo "=========================================="

TMPDIR=$(mktemp -d)
trap "rm -rf $TMPDIR" EXIT

# --- Block Upload ---
# NOTE: this intentionally exercises the legacy no-session POST /api/v2/blocks/upload
# path for raw storage timing. That path stores bytes but does NOT materialize the
# block metadata/reference rows used by the web session flow, so it must not be
# treated as a correctness benchmark for resumable web uploads.
echo
echo "--- Block Upload (POST /api/v2/blocks/upload) ---"
for size_kb in 64 256 1024 4096; do
  dd if=/dev/urandom of="$TMPDIR/block.bin" bs=1K count="$size_kb" 2>/dev/null

  start=$(date +%s%N)
  result=$(curl -sS $INSECURE -w "\n%{http_code}" \
    -X POST "$HOST/api/v2/blocks/upload" \
    "${AUTH[@]}" \
    -H "Content-Type: application/octet-stream" \
    --data-binary "@$TMPDIR/block.bin" \
    --max-time 30 2>/dev/null)
  end=$(date +%s%N)

  code=$(echo "$result" | tail -1)
  body=$(echo "$result" | head -1)
  ms=$(( (end - start) / 1000000 ))

  hash=""
  if echo "$body" | grep -q '"hash"'; then
    hash=$(echo "$body" | grep -o '"hash":"[^"]*"' | cut -d'"' -f4 | head -c 16)
  fi

  printf "  %5dKB -> HTTP %s  %5dms" "$size_kb" "$code" "$ms"
  [ -n "$hash" ] && printf "  hash=%s..." "$hash"
  echo
done

# NOTE: the "Block Download (GET /api/v2/blocks/:hash)" benchmark was removed:
# the bare-hash block read endpoint no longer exists (it was an unauthorized
# cross-tenant content oracle; see docs/WEB-BLOCK-UPLOAD.md finding 11).
# Download performance is covered by the file download benchmark below.

# --- File Upload via seafhttp ---
echo
echo "--- File Upload (seafhttp upload-link flow) ---"
for size_kb in 100 1000 10000; do
  dd if=/dev/urandom of="$TMPDIR/file_${size_kb}k.bin" bs=1K count="$size_kb" 2>/dev/null
  size_label="${size_kb}KB"
  [ "$size_kb" -ge 1000 ] && size_label="$((size_kb / 1000))MB"

  # Get upload link
  link=$(curl -sS $INSECURE "${AUTH[@]}" \
    "$HOST/api2/repos/$REPO/upload-link/" 2>/dev/null | tr -d '"')

  if [ -z "$link" ] || echo "$link" | grep -q error; then
    echo "  $size_label -> SKIP: could not get upload link"
    continue
  fi

  start=$(date +%s%N)
  code=$(curl -sS $INSECURE -o /dev/null -w "%{http_code}" \
    -X POST "$link" \
    "${AUTH[@]}" \
    -F "file=@$TMPDIR/file_${size_kb}k.bin;filename=bench_${size_kb}k.bin" \
    -F "parent_dir=/" \
    -F "replace=1" \
    --max-time 120 2>/dev/null || echo "000")
  end=$(date +%s%N)

  ms=$(( (end - start) / 1000000 ))
  if [ "$ms" -gt 0 ]; then
    kbps=$(( size_kb * 1000 / ms ))
  else
    kbps="inf"
  fi
  printf "  %6s upload -> HTTP %s  %6dms  ~%s KB/s\n" "$size_label" "$code" "$ms" "$kbps"
done

# --- File Download via seafhttp ---
echo
echo "--- File Download (seafhttp download flow) ---"
for size_kb in 100 1000 10000; do
  size_label="${size_kb}KB"
  [ "$size_kb" -ge 1000 ] && size_label="$((size_kb / 1000))MB"

  link=$(curl -sS $INSECURE "${AUTH[@]}" \
    "$HOST/api2/repos/$REPO/file/?p=/bench_${size_kb}k.bin&reuse=1" 2>/dev/null | tr -d '"')

  if [ -z "$link" ] || echo "$link" | grep -q error; then
    echo "  $size_label -> SKIP: file not found (upload first)"
    continue
  fi

  start=$(date +%s%N)
  actual_bytes=$(curl -sS $INSECURE -o /dev/null -w "%{size_download}" \
    "$link" --max-time 120 2>/dev/null || echo "0")
  end=$(date +%s%N)

  ms=$(( (end - start) / 1000000 ))
  actual_kb=$(( ${actual_bytes%.*} / 1024 ))
  if [ "$ms" -gt 0 ]; then
    kbps=$(( actual_kb * 1000 / ms ))
  else
    kbps="inf"
  fi
  printf "  %6s download -> %6dms  %s KB received  ~%s KB/s\n" "$size_label" "$ms" "$actual_kb" "$kbps"
done

# --- API Latency ---
echo
echo "--- API Endpoint Latency (20 samples each) ---"
for endpoint in "/api2/ping" "/health" "/ready" "/api2/account/info" "/api2/repos/"; do
  times=()
  for i in $(seq 1 20); do
    ms=$(curl -sS $INSECURE -o /dev/null -w "%{time_total}" \
      "${AUTH[@]}" \
      "$HOST$endpoint" --max-time 10 2>/dev/null || echo "1.000")
    # Convert seconds (e.g. 0.853) to milliseconds using string manipulation
    sec_part="${ms%%.*}"
    frac_part="${ms#*.}"
    frac_part="${frac_part}000"  # pad
    frac_part="${frac_part:0:3}" # trim to 3 digits
    ms_int=$(( ${sec_part:-0} * 1000 + 10#${frac_part:-0} ))
    times+=("${ms_int:-0}")
  done

  sorted=($(printf '%s\n' "${times[@]}" | sort -n))
  count=${#sorted[@]}
  p50=${sorted[$((count / 2))]}
  p95=${sorted[$((count * 95 / 100))]}
  min=${sorted[0]}
  max=${sorted[$((count - 1))]}

  printf "  %-25s p50=%4dms  p95=%4dms  min=%4dms  max=%4dms\n" \
    "$endpoint" "$p50" "$p95" "$min" "$max"
done

# --- Resource snapshot ---
echo
echo "--- Server Resource Snapshot ---"
if docker stats --no-stream --format "table {{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}\t{{.NetIO}}" 2>/dev/null | head -10; then
  true
else
  echo "  (docker stats not available — running against remote host)"
fi

echo
echo "=========================================="
echo " Benchmark Complete"
echo "=========================================="
