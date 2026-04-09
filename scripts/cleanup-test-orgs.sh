#!/bin/sh

set -eu

BASE_URL="${1:-${API_URL:-${SESAMEFS_URL:-http://localhost:8082}}}"
SUPERADMIN_TOKEN="${SUPERADMIN_TOKEN:-dev-token-superadmin}"

curl -fsS -H "Authorization: Token $SUPERADMIN_TOKEN" \
  "$BASE_URL/api/v2.1/admin/organizations/" | \
  jq -r '.organizations[]? | select(((.org_name // .name) | test("^(Test Tenant|Updated Tenant|inttest-)")) and (.status != "deleted")) | [.org_id, (.org_name // .name), .status] | @tsv' | \
  while IFS="	" read -r org_id org_name org_status; do
    if [ -z "$org_id" ]; then
      continue
    fi

    status=$(curl -s -o /dev/null -w "%{http_code}" -X DELETE \
      -H "Authorization: Token $SUPERADMIN_TOKEN" \
      "$BASE_URL/api/v2.1/admin/organizations/$org_id/")

    if [ "$status" = "200" ]; then
      echo "[cleanup-test-orgs] soft-deleted $org_name ($org_id) from status=$org_status"
    else
      echo "[cleanup-test-orgs] delete returned $status for $org_name ($org_id)"
    fi
  done