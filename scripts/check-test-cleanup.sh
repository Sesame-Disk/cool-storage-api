#!/bin/bash

set -euo pipefail

API_URL="${API_URL:-${SESAMEFS_URL:-http://localhost:8082}}"
SUPERADMIN_TOKEN="${SUPERADMIN_TOKEN:-dev-token-superadmin}"
ADMIN_TOKEN="${ADMIN_TOKEN:-dev-token-admin}"
USER_TOKEN="${USER_TOKEN:-dev-token-user}"
DEFAULT_ORG_ID="${DEFAULT_ORG_ID:-00000000-0000-0000-0000-000000000001}"

echo "ORG_QUOTA_POLICY:"
curl -s -H "Authorization: Token ${SUPERADMIN_TOKEN}" \
  "${API_URL}/api/v2.1/admin/organizations/${DEFAULT_ORG_ID}/" | jq -r '.quota_policy'

echo "ACTIVE_TEST_ORGS:"
curl -s -H "Authorization: Token ${SUPERADMIN_TOKEN}" \
  "${API_URL}/api/v2.1/admin/organizations/" | jq -r '.organizations[]? | select(((.org_name // .name) | test("^(Test Tenant|Updated Tenant)")) and (.status != "deleted")) | (.org_name // .name)'

echo "DELETED_TEST_ORGS_PENDING_GC:"
curl -s -H "Authorization: Token ${SUPERADMIN_TOKEN}" \
  "${API_URL}/api/v2.1/admin/organizations/" | jq -r '.organizations[]? | select(((.org_name // .name) | test("^(Test Tenant|Updated Tenant)")) and (.status == "deleted")) | (.org_name // .name)'

echo "LEFTOVER_TEST_LIBS_ADMIN:"
curl -s -H "Authorization: Token ${ADMIN_TOKEN}" \
  "${API_URL}/api/v2.1/repos/?type=mine" | jq -r '.repos[]? | select(.repo_name | test("^(test-|nested-move-copy-test|FileOpsTest-|HistoryTest-|cross-lib-src-|cross-lib-dst-|with-parents-test$|api-token-test-|tag-test-library-)")) | .repo_name'

echo "LEFTOVER_TEST_LIBS_USER:"
curl -s -H "Authorization: Token ${USER_TOKEN}" \
  "${API_URL}/api/v2.1/repos/?type=mine" | jq -r '.repos[]? | select(.repo_name | test("^(test-|nested-move-copy-test|FileOpsTest-|HistoryTest-|cross-lib-src-|cross-lib-dst-|with-parents-test$|api-token-test-|tag-test-library-)")) | .repo_name'