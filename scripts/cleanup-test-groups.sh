#!/bin/sh

set -eu

BASE_URL="${1:-${API_URL:-${SESAMEFS_URL:-http://localhost:8082}}}"
SUPERADMIN_TOKEN="${SUPERADMIN_TOKEN:-dev-token-superadmin}"

curl -fsS -H "Authorization: Token $SUPERADMIN_TOKEN" \
  "$BASE_URL/api/v2.1/admin/groups/" | \
  jq -r '.groups[]? | select((.name // .group_name // "") | test("^(TestAdminGroup-|TestGroup-)")) | [.id, (.name // .group_name // "")] | @tsv' | \
  while IFS="	" read -r group_id group_name; do
    if [ -z "$group_id" ]; then
      continue
    fi

    status=$(curl -s -o /dev/null -w "%{http_code}" -X DELETE \
      -H "Authorization: Token $SUPERADMIN_TOKEN" \
      "$BASE_URL/api/v2.1/admin/groups/$group_id/")

    if [ "$status" = "200" ]; then
      echo "[cleanup-test-groups] deleted $group_name ($group_id)"
    else
      echo "[cleanup-test-groups] delete returned $status for $group_name ($group_id)"
    fi
  done