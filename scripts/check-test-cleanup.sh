#!/bin/bash

set -euo pipefail

API_URL="${1:-${API_URL:-${SESAMEFS_URL:-http://localhost:8082}}}"
SUPERADMIN_TOKEN="${SUPERADMIN_TOKEN:-dev-token-superadmin}"
ADMIN_TOKEN="${ADMIN_TOKEN:-dev-token-admin}"
USER_TOKEN="${USER_TOKEN:-dev-token-user}"
DEFAULT_ORG_ID="${DEFAULT_ORG_ID:-00000000-0000-0000-0000-000000000001}"

print_section() {
  label="$1"
  value="$2"
  echo "$label"
  if [ -n "$value" ]; then
    printf '%s\n' "$value"
  fi
}

ACTIVE_TEST_ORGS=$(curl -s -H "Authorization: Token ${SUPERADMIN_TOKEN}" \
  "${API_URL}/api/v2.1/admin/organizations/?status=active" | jq -r '.organizations[]? | select(((.org_name // .name) | test("^(Test Tenant|Updated Tenant|inttest-)"))) | (.org_name // .name)')

DELETED_TEST_ORGS=$(curl -s -H "Authorization: Token ${SUPERADMIN_TOKEN}" \
  "${API_URL}/api/v2.1/admin/organizations/?status=deleted" | jq -r '.organizations[]? | select(((.org_name // .name) | test("^(Test Tenant|Updated Tenant|inttest-)"))) | (.org_name // .name)')

LEFTOVER_TEST_LIBS_SUPERADMIN=$(curl -s -H "Authorization: Token ${SUPERADMIN_TOKEN}" \
  "${API_URL}/api/v2.1/repos/?type=mine" | jq -r '.repos[]? | select(.repo_name | test("^(batch-ops-test-|encrypted-test-|FileOpsTest-|HistoryRetest-|HistoryStateCheck-|HistoryTest-|history-test-library-|nested-move-copy-test|cross-lib-src-|cross-lib-dst-|search-test-|with-parents-test$|api-token-test-|tag-test-library-|sa-test-lib-|test-encrypted$|test-libsettings-|test-write-ops$|failover-test-|multiregion-test-|sync-test-encrypted-|sync-test-unencrypted-|usa-routing-test-|eu-routing-test-)")) | .repo_name')

LEFTOVER_TEST_LIBS_ADMIN=$(curl -s -H "Authorization: Token ${ADMIN_TOKEN}" \
  "${API_URL}/api/v2.1/repos/?type=mine" | jq -r '.repos[]? | select(.repo_name | test("^(batch-ops-test-|encrypted-test-|FileOpsTest-|HistoryRetest-|HistoryStateCheck-|HistoryTest-|history-test-library-|nested-move-copy-test|cross-lib-src-|cross-lib-dst-|search-test-|with-parents-test$|api-token-test-|tag-test-library-|test-encrypted$|test-libsettings-|test-write-ops$|failover-test-|multiregion-test-|sync-test-encrypted-|sync-test-unencrypted-|usa-routing-test-|eu-routing-test-)")) | .repo_name')

LEFTOVER_TEST_LIBS_USER=$(curl -s -H "Authorization: Token ${USER_TOKEN}" \
  "${API_URL}/api/v2.1/repos/?type=mine" | jq -r '.repos[]? | select(.repo_name | test("^(batch-ops-test-|encrypted-test-|FileOpsTest-|HistoryRetest-|HistoryStateCheck-|HistoryTest-|history-test-library-|nested-move-copy-test|cross-lib-src-|cross-lib-dst-|search-test-|with-parents-test$|api-token-test-|tag-test-library-|test-encrypted$|test-libsettings-|test-write-ops$|failover-test-|multiregion-test-|sync-test-encrypted-|sync-test-unencrypted-|usa-routing-test-|eu-routing-test-)")) | .repo_name')

ACTIVE_TEST_GROUPS=$(curl -s -H "Authorization: Token ${SUPERADMIN_TOKEN}" \
  "${API_URL}/api/v2.1/admin/groups/" | jq -r '.groups[]? | select((.name // .group_name // "") | test("^(TestAdminGroup-|TestGroup-)")) | (.name // .group_name // "")')

echo "ORG_QUOTA_POLICY:"
curl -s -H "Authorization: Token ${SUPERADMIN_TOKEN}" \
  "${API_URL}/api/v2.1/admin/organizations/${DEFAULT_ORG_ID}/" | jq -r '.quota_policy'

print_section "ACTIVE_TEST_ORGS:" "$ACTIVE_TEST_ORGS"
print_section "DELETED_TEST_ORGS_PENDING_GC:" "$DELETED_TEST_ORGS"
print_section "LEFTOVER_TEST_LIBS_SUPERADMIN:" "$LEFTOVER_TEST_LIBS_SUPERADMIN"
print_section "LEFTOVER_TEST_LIBS_ADMIN:" "$LEFTOVER_TEST_LIBS_ADMIN"
print_section "LEFTOVER_TEST_LIBS_USER:" "$LEFTOVER_TEST_LIBS_USER"
print_section "ACTIVE_TEST_GROUPS:" "$ACTIVE_TEST_GROUPS"

if [ -n "$ACTIVE_TEST_ORGS$LEFTOVER_TEST_LIBS_SUPERADMIN$LEFTOVER_TEST_LIBS_ADMIN$LEFTOVER_TEST_LIBS_USER$ACTIVE_TEST_GROUPS" ]; then
  echo "CLEANUP_STATUS: dirty"
  exit 1
fi

echo "CLEANUP_STATUS: clean"