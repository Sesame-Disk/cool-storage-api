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
CONCURRENT_SIZE_MB=""
CONCURRENT_MAX_TIME="300"
INSECURE=""
STATS_INTERVAL=""
STATS_FILTER='(^|-)sesamefs(-[0-9]+)?$|(^|-)minio(-[0-9]+)?$|(^|-)cassandra(-[0-9]+)?$'
STATS_LOG=""
STATS_PID=""

usage() {
  echo "benchmark-upload-download.sh"
  echo "Usage: $0 --host URL --token TOKEN --repo REPO [--sizes '1 10 100'] [--concurrency '1 4 8'] [--concurrent-size 32] [--concurrent-max-time 300] [--stats-interval 2] [--stats-filter 'sesamefs|minio|cassandra'] [--stats-log path.csv] [-k]"
}

docker_stats_available() {
  command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1
}

capture_stats_sample() {
  timestamp=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
  docker stats --no-stream --format '{{.Name}},{{.CPUPerc}},{{.MemUsage}},{{.MemPerc}},{{.NetIO}}' 2>/dev/null |
    awk -F',' -v ts="$timestamp" -v filter="$STATS_FILTER" '
      filter == "" || $1 ~ filter {
        print ts "," $0
      }
    ' >> "$STATS_LOG"
}

start_stats_sampler() {
  [ -n "$STATS_INTERVAL" ] || return 0

  if ! docker_stats_available; then
    echo
    echo "--- Resource Sampling ---"
    echo "  docker stats not available; skipping periodic CPU/RAM sampling"
    return 0
  fi

  cat > "$STATS_LOG" <<'EOF'
timestamp,name,cpu_perc,mem_usage,mem_perc,net_io
EOF

  capture_stats_sample

  (
    while :; do
      sleep "$STATS_INTERVAL"
      capture_stats_sample
    done
  ) &
  STATS_PID=$!

  echo
  echo "--- Resource Sampling ---"
  echo "  interval: ${STATS_INTERVAL}s"
  if [ -n "$STATS_FILTER" ]; then
    echo "  filter:   $STATS_FILTER"
  else
    echo "  filter:   <all containers>"
  fi
  echo "  log:      $STATS_LOG"
}

stop_stats_sampler() {
  [ -n "$STATS_PID" ] || return 0
  kill "$STATS_PID" 2>/dev/null || true
  wait "$STATS_PID" 2>/dev/null || true
  STATS_PID=""
}

print_stats_summary() {
  [ -s "$STATS_LOG" ] || return 0

  echo
  echo "--- Peak Container Resource Usage ---"
  awk -F',' '
    function trim(value) {
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", value)
      return value
    }

    function parse_percent(value) {
      value = trim(value)
      sub(/%$/, "", value)
      return value + 0
    }

    function parse_bytes(value, normalized, number, unit) {
      normalized = trim(value)
      gsub(/ /, "", normalized)
      if (normalized == "" || normalized == "--") {
        return 0
      }
      if (match(normalized, /^([0-9.]+)([A-Za-z]+)$/, parts) == 0) {
        return normalized + 0
      }
      number = parts[1] + 0
      unit = parts[2]
      if (unit == "B") return number
      if (unit == "kB") return number * 1000
      if (unit == "MB") return number * 1000 * 1000
      if (unit == "GB") return number * 1000 * 1000 * 1000
      if (unit == "TB") return number * 1000 * 1000 * 1000 * 1000
      if (unit == "KiB") return number * 1024
      if (unit == "MiB") return number * 1024 * 1024
      if (unit == "GiB") return number * 1024 * 1024 * 1024
      if (unit == "TiB") return number * 1024 * 1024 * 1024 * 1024
      return number
    }

    function format_bytes(value) {
      if (value >= 1024 * 1024 * 1024) return sprintf("%.1f GiB", value / (1024 * 1024 * 1024))
      if (value >= 1024 * 1024) return sprintf("%.1f MiB", value / (1024 * 1024))
      if (value >= 1024) return sprintf("%.1f KiB", value / 1024)
      return sprintf("%.0f B", value)
    }

    NR == 1 { next }

    {
      name = trim($2)
      cpu = parse_percent($3)
      split($4, mem_parts, "/")
      mem_bytes = parse_bytes(mem_parts[1])
      mem_perc = parse_percent($5)
      net_io = trim($6)

      seen[name] = 1
      if (!(name in peak_cpu) || cpu > peak_cpu[name]) peak_cpu[name] = cpu
      if (!(name in peak_mem) || mem_bytes > peak_mem[name]) peak_mem[name] = mem_bytes
      if (!(name in peak_mem_perc) || mem_perc > peak_mem_perc[name]) peak_mem_perc[name] = mem_perc
      last_net[name] = net_io
    }

    END {
      printf "  %-30s %10s %14s %10s %20s\n", "container", "peak cpu", "peak mem", "peak mem%", "last net io"
      for (name in seen) {
        printf "  %-30s %9.2f%% %14s %9.2f%% %20s\n", name, peak_cpu[name], format_bytes(peak_mem[name]), peak_mem_perc[name], last_net[name]
      }
    }
  ' "$STATS_LOG"
}

while [ $# -gt 0 ]; do
  case "$1" in
    --host)        HOST="${2:-}"; shift 2 ;;
    --token)       TOKEN="${2:-}"; shift 2 ;;
    --repo)        REPO="${2:-}"; shift 2 ;;
    --sizes)       SIZES="${2:-}"; shift 2 ;;
    --concurrency) CONCURRENCY="${2:-}"; shift 2 ;;
    --concurrent-size) CONCURRENT_SIZE_MB="${2:-}"; shift 2 ;;
    --concurrent-max-time) CONCURRENT_MAX_TIME="${2:-}"; shift 2 ;;
    --stats-interval) STATS_INTERVAL="${2:-}"; shift 2 ;;
    --stats-filter) STATS_FILTER="${2:-}"; shift 2 ;;
    --stats-log)   STATS_LOG="${2:-}"; shift 2 ;;
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
STATS_LOG="${STATS_LOG:-$TMPDIR/docker-stats.csv}"
trap 'stop_stats_sampler; rm -rf "$TMPDIR"' EXIT

if [ -n "$STATS_INTERVAL" ]; then
  case "$STATS_INTERVAL" in
    ''|*[!0-9.]*)
      echo "Invalid --stats-interval: $STATS_INTERVAL" >&2
      exit 2
      ;;
  esac
fi

if [ -z "$CONCURRENT_SIZE_MB" ]; then
  set -- $SIZES
  CONCURRENT_SIZE_MB="${1:-1}"
fi

case "$CONCURRENT_SIZE_MB" in
  ''|*[!0-9]*)
    echo "Invalid --concurrent-size: $CONCURRENT_SIZE_MB" >&2
    exit 2
    ;;
esac

case "$CONCURRENT_MAX_TIME" in
  ''|*[!0-9]*)
    echo "Invalid --concurrent-max-time: $CONCURRENT_MAX_TIME" >&2
    exit 2
    ;;
esac

# Generate test files
for size in $SIZES; do
  dd if=/dev/urandom of="$TMPDIR/test_${size}mb.bin" bs=1M count="$size" 2>/dev/null
done
[ -f "$TMPDIR/test_${CONCURRENT_SIZE_MB}mb.bin" ] || dd if=/dev/urandom of="$TMPDIR/test_${CONCURRENT_SIZE_MB}mb.bin" bs=1M count="$CONCURRENT_SIZE_MB" 2>/dev/null

start_stats_sampler

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
echo "--- Concurrent ${CONCURRENT_SIZE_MB}MB Uploads ---"
for conc in $CONCURRENCY; do
  file="$TMPDIR/test_${CONCURRENT_SIZE_MB}mb.bin"

  start=$(date +%s%N)
  pids=()
  for i in $(seq 1 "$conc"); do
    (
      link=$(curl -sS $INSECURE "${AUTH[@]}" \
        "$HOST/api2/repos/$REPO/upload-link/" 2>/dev/null | tr -d '"')
      curl -sS $INSECURE -o /dev/null \
        -X POST "$link" \
        "${AUTH[@]}" \
        -F "file=@$file;filename=conc_${CONCURRENT_SIZE_MB}mb_${i}.bin" \
        -F "parent_dir=/" \
        -F "replace=1" \
        --max-time "$CONCURRENT_MAX_TIME" 2>/dev/null
    ) &
    pids+=($!)
  done
  for pid in "${pids[@]}"; do wait "$pid" 2>/dev/null || true; done
  end=$(date +%s%N)

  ms=$(( (end - start) / 1000000 ))
  if [ "$ms" -gt 0 ]; then
    mbps=$(calc "$conc * $CONCURRENT_SIZE_MB * 8000 / $ms")
  else
    mbps="inf"
  fi

  printf "  %2d concurrent: %6dms total  ~%s Mbps aggregate\n" "$conc" "$ms" "$mbps"
done

# --- Concurrent downloads ---
echo
echo "--- Concurrent ${CONCURRENT_SIZE_MB}MB Downloads ---"
for conc in $CONCURRENCY; do
  link=$(curl -sS $INSECURE "${AUTH[@]}" \
    "$HOST/api2/repos/$REPO/file/?p=/bench_${CONCURRENT_SIZE_MB}mb.bin&reuse=1" 2>/dev/null | tr -d '"')

  if [ -z "$link" ] || echo "$link" | grep -q error; then
    echo "  SKIP: bench_${CONCURRENT_SIZE_MB}mb.bin not found"
    break
  fi

  start=$(date +%s%N)
  pids=()
  for i in $(seq 1 "$conc"); do
    curl -sS $INSECURE -o /dev/null "$link" --max-time "$CONCURRENT_MAX_TIME" 2>/dev/null &
    pids+=($!)
  done
  for pid in "${pids[@]}"; do wait "$pid" 2>/dev/null || true; done
  end=$(date +%s%N)

  ms=$(( (end - start) / 1000000 ))
  if [ "$ms" -gt 0 ]; then
    mbps=$(calc "$conc * $CONCURRENT_SIZE_MB * 8000 / $ms")
  else
    mbps="inf"
  fi

  printf "  %2d concurrent: %6dms total  ~%s Mbps aggregate\n" "$conc" "$ms" "$mbps"
done

echo
echo "=========================================="
echo " Benchmark Complete"
echo "=========================================="

print_stats_summary

if [ -s "$STATS_LOG" ]; then
  echo
  echo "docker stats log saved to: $STATS_LOG"
fi
