#!/bin/sh

set -eu

BASE_URL="${1:-${SESAMEFS_URL:-http://localhost:3000}}"

cleanup_for_token() {
  token="$1"
  repos_json=$(curl -fsS -H "Authorization: Token $token" "$BASE_URL/api/v2.1/repos/?type=mine")
  # Regex notes: outer ^ anchors the whole match; per-branch $ (e.g. with-parents-test$, sesamefs-public-smoke$) restricts that branch to an exact-name match.
  echo "$repos_json" | jq -r '.repos[]? | select(.repo_name | test("^(nested-move-copy-test|FileOpsTest-|HistoryTest-|cross-lib-src-|cross-lib-dst-|with-parents-test$|api-token-test-|tag-test-library-|sa-test-lib-|sync-test-|sync-aa-|inttest-|smoke-|sesamefs-public-smoke$|test-)")) | .repo_id' | while read -r repo_id; do
    if [ -n "$repo_id" ]; then
      curl -fsS -o /dev/null -X DELETE -H "Authorization: Token $token" "$BASE_URL/api/v2.1/repos/$repo_id/"
      echo "[cleanup-test-repos] deleted $repo_id for token $token"
    fi
  done
}

cleanup_for_token "${SUPERADMIN_TOKEN:-dev-token-superadmin}"
cleanup_for_token "${ADMIN_TOKEN:-dev-token-admin}"
cleanup_for_token "${USER_TOKEN:-dev-token-user}"
