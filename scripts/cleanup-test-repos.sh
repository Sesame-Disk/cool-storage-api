#!/bin/sh

set -eu

BASE_URL="${1:-${SESAMEFS_URL:-http://localhost:3000}}"

cleanup_for_token() {
  token="$1"
  repos_json=$(curl -fsS -H "Authorization: Token $token" "$BASE_URL/api/v2.1/repos/?type=mine")
  echo "$repos_json" | jq -r '.repos[]? | select(.repo_name | test("^(nested-move-copy-test|FileOpsTest-|HistoryTest-|cross-lib-src-|cross-lib-dst-|with-parents-test$|api-token-test-|tag-test-library-|test-)")) | .repo_id' | while read -r repo_id; do
    if [ -n "$repo_id" ]; then
      curl -fsS -o /dev/null -X DELETE -H "Authorization: Token $token" "$BASE_URL/api/v2.1/repos/$repo_id/"
      echo "[cleanup-test-repos] deleted $repo_id for token $token"
    fi
  done
}

cleanup_for_token "${ADMIN_TOKEN:-dev-token-admin}"
cleanup_for_token "${USER_TOKEN:-dev-token-user}"