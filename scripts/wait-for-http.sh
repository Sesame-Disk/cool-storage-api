#!/bin/sh

set -eu

TARGET_URL="${1:-}"
WAIT_LABEL="${2:-service}"
TIMEOUT_SECONDS="${WAIT_FOR_HTTP_TIMEOUT_SECONDS:-120}"
SLEEP_SECONDS="${WAIT_FOR_HTTP_SLEEP_SECONDS:-2}"

if [ -z "$TARGET_URL" ]; then
  echo "usage: $0 <url> [label]" >&2
  exit 2
fi

start_ts=$(date +%s)
attempt=0

echo "[wait-for-http] waiting for ${WAIT_LABEL} at ${TARGET_URL} (timeout: ${TIMEOUT_SECONDS}s)"

while true; do
  if curl -fsS "$TARGET_URL" > /dev/null 2>&1; then
    elapsed=$(( $(date +%s) - start_ts ))
    echo "[wait-for-http] ${WAIT_LABEL} is ready after ${elapsed}s"
    exit 0
  fi

  attempt=$((attempt + 1))
  elapsed=$(( $(date +%s) - start_ts ))
  if [ "$elapsed" -ge "$TIMEOUT_SECONDS" ]; then
    echo "[wait-for-http] timed out after ${elapsed}s waiting for ${WAIT_LABEL} at ${TARGET_URL}" >&2
    exit 1
  fi

  echo "[wait-for-http] still waiting for ${WAIT_LABEL} (${elapsed}s elapsed, attempt ${attempt})"
  sleep "$SLEEP_SECONDS"
done