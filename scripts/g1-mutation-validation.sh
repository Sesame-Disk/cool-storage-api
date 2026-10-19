#!/usr/bin/env bash
#
# G1 mutation evidence. Each mutation removes one exact-identity, durable-root,
# or fail-closed invariant and must make its focused test red.
#
#   ./scripts/g1-mutation-validation.sh          # run every mutation
#   ./scripts/g1-mutation-validation.sh <name>   # run one mutation
#   ./scripts/g1-mutation-validation.sh --list   # list mutations
#
# Unit-level only: no Cassandra, MinIO, or application stack is required.

set -uo pipefail
cd "$(dirname "$0")/.."

MIGRATION=internal/db/migrations/001_initial_schema.cql
STORE=internal/gc/store_cassandra.go
MOCK=internal/gc/store_mock.go
WORKER=internal/gc/worker.go
AUTHORITY=internal/gc/store.go
DB_REFS=internal/db/block_references.go

green() { printf '\033[32m%s\033[0m\n' "$*"; }
red() { printf '\033[31m%s\033[0m\n' "$*" >&2; }

BACKUPS=()
restore() {
  local f
  for f in "${BACKUPS[@]:-}"; do
    if [ -n "$f" ] && [ -f "$f.g1bak" ]; then mv -f "$f.g1bak" "$f"; fi
  done
  BACKUPS=()
}
fail() { red "FAILED: $*"; restore; exit 1; }
trap restore EXIT INT TERM

mutate() {
  local f="$1" expr="$2"
  cp "$f" "$f.g1bak"
  BACKUPS+=("$f")
  perl -0pi -e "$expr" "$f"
  if cmp -s "$f" "$f.g1bak"; then
    fail "mutation did not apply to $f"
  fi
}

expect_red_pkg() {
  local package="$1" pattern="$2" needle="$3" what="$4" out status
  out="$(go test "$package" -count=1 -run "$pattern" 2>&1)"
  status=$?
  if [ $status -eq 0 ]; then
    printf '%s\n' "$out" >&2
    fail "$what: the suite stayed green"
  fi
  if [ -n "$needle" ] && ! printf '%s\n' "$out" | grep -qF "$needle"; then
    printf '%s\n' "$out" >&2
    fail "$what: failed without the expected assertion: $needle"
  fi
  green "  RED as required: $what"
}

expect_red() { expect_red_pkg ./internal/gc "$@"; }

m1_canonical_identity_loses_delete_authority() {
  mutate "$MIGRATION" 's{PRIMARY KEY \(\(org_id, block_id\), storage_class, storage_key, gc_claim_id, gc_claimed_at\)}{PRIMARY KEY ((org_id, block_id), storage_class, storage_key)}'
  out="$(go test ./internal/db -count=1 -run TestR26MigrationDeclaresTheExactIdentityKeys 2>&1)"
  [ $? -ne 0 ] || fail 'canonical orphan key mutation stayed green'
  green '  RED as required: canonical orphan identity loses D'
  restore
}

m2_projection_identity_loses_delete_authority() {
  mutate "$MIGRATION" 's{PRIMARY KEY \(\(first_seen_day, bucket\), first_seen_at, org_id, block_id,\s+storage_class, storage_key, gc_claim_id, gc_claimed_at\)}{PRIMARY KEY ((first_seen_day, bucket), first_seen_at, org_id, block_id, storage_class, storage_key)}'
  out="$(go test ./internal/db -count=1 -run TestR26MigrationDeclaresTheExactIdentityKeys 2>&1)"
  [ $? -ne 0 ] || fail 'discovery identity mutation stayed green'
  green '  RED as required: discovery identity loses D'
  restore
}

m3_canonical_delete_omits_delete_authority() {
  mutate "$STORE" 's{(DELETE FROM gc_s3_orphans\n\s+WHERE org_id = \? AND block_id = \? AND storage_class = \? AND storage_key = \?\n)\s+AND gc_claim_id = \? AND gc_claimed_at = \?}{$1}'
  expect_red 'TestG1SourceContractsKeepRootBeforeCanonicalAndSettlementBounded' 'canonical orphan delete must include the exact D identity' \
    'canonical deletion loses the exact D identity'
  restore
}

m4_recovery_root_required() {
  mutate "$MIGRATION" 's{CREATE TABLE IF NOT EXISTS gc_s3_orphan_recovery_roots}{CREATE TABLE IF NOT EXISTS gc_s3_orphan_recovery_roots_removed}'
  out="$(go test ./internal/db -count=1 -run TestG1MigrationDeclaresExactOrphanRecoveryRoot 2>&1)"
  [ $? -ne 0 ] || fail 'recovery-root migration mutation stayed green'
  green '  RED as required: durable recovery root disappears'
  restore
}

m5_no_orphan_ttl() {
  mutate "$MIGRATION" 's{(CREATE TABLE IF NOT EXISTS gc_s3_orphans \(.*?)default_time_to_live = 0}{${1}default_time_to_live = 7776000}s'
  expect_red 'TestS3OrphanMigrationHasNoExpirySchedule' 'must have no expiry schedule' \
    'pending orphan identity regains a TTL'
  restore
}

m6_prepared_authorizes_s3() {
	mutate "$WORKER" 's{strings\.EqualFold\(strings\.TrimSpace\(canonical\.RecoveryState\), S3OrphanRecoveryStatePrepared\)}{false}g'
	expect_red 'TestG1PreparedRecoveryStateIsRetainedWithoutPhysicalDelete' 'prepared recovery' \
		'PREPARED is allowed into physical recovery'
	restore
}

m7_root_before_canonical_settlement() {
	mutate "$WORKER" 's{w\.terminateThenDeleteS3Orphan\(root\.OrgID, root\.BlockID, root\.FirstSeenAt, committedBlockDeleteAuthority\(root\.Authority\)\)}{w.store.DeleteS3OrphanRecoveryRoot(root.OrgID, root.BlockID, root.Authority)}'
	expect_red 'TestG1TerminalLifecycleCleansRootAfterCanonicalLoss|TestG1TerminalRootSettlementIsPageBoundedAndExact' 'projection' \
		'root deletion precedes exact canonical/projection settlement'
	restore
}

m8_claim_timestamp_loses_millisecond_normalization() {
	mutate "$AUTHORITY" 's{authority\.ClaimedAt = authority\.ClaimedAt\.UTC\(\)\.Truncate\(time\.Millisecond\)}{authority.ClaimedAt = authority.ClaimedAt.UTC()}'
	expect_red 'TestG1MockOrphanRowsRemainDistinctByExactPD' 'was not normalized to milliseconds' \
		'claim authority loses Cassandra millisecond normalization'
	restore
}

m9_missing_canonical_is_settled() {
	mutate "$WORKER" 's{case StartBlockDeleteOrphanLifecycleAdvanced:\n[ \t]*// A terminal D is the exact settlement certificate for this}{case StartBlockDeleteOrphanInvalid:\n\t\t\t\t\t// A terminal D is the exact settlement certificate for this}; s{case StartBlockDeleteOrphanSameAuthority:\s*metrics\.GCAuditEventsTotal\.WithLabelValues\("gc_s3_orphan_root_waiting_for_canonical"\)\.Inc\(\)}{case StartBlockDeleteOrphanLifecycleAdvanced:}'
	expect_red 'TestG1RootWithoutCanonicalIsRetained' 'root-only recovery' \
		'missing canonical is treated as settled'
	restore
}

m10_writer_fence_ignores_orphan() {
	mutate "$DB_REFS" 's{return blockDeleteFenceHasS3OrphanFn\(db, orgID, blockID\)}{return false, nil}'
	expect_red_pkg ./internal/db 'TestP3BlockDeleteFenceSurvivesOrphanHandoff' 'rowless read must not be reported as unfenced' \
		'writer fence ignores pending orphan existence'
	restore
}

m11_canonical_storage_key_identity() {
	mutate "$MIGRATION" 's{PRIMARY KEY \(\(org_id, block_id\), storage_class, storage_key, gc_claim_id, gc_claimed_at\)}{PRIMARY KEY ((org_id, block_id), storage_class, gc_claim_id, gc_claimed_at)}'
	out="$(go test ./internal/db -count=1 -run TestR26MigrationDeclaresTheExactIdentityKeys 2>&1)"
	[ $? -ne 0 ] || fail 'canonical storage-key identity mutation stayed green'
	green '  RED as required: canonical orphan identity loses storage_key'
	restore
}

m12_projection_storage_key_identity() {
	mutate "$MIGRATION" 's{PRIMARY KEY \(\(first_seen_day, bucket\), first_seen_at, org_id, block_id,\s+storage_class, storage_key, gc_claim_id, gc_claimed_at\)}{PRIMARY KEY ((first_seen_day, bucket), first_seen_at, org_id, block_id, storage_class, gc_claim_id, gc_claimed_at)}'
	out="$(go test ./internal/db -count=1 -run TestR26MigrationDeclaresTheExactIdentityKeys 2>&1)"
	[ $? -ne 0 ] || fail 'discovery storage-key identity mutation stayed green'
	green '  RED as required: discovery identity loses storage_key'
	restore
}

m13_recovery_root_publication_removed() {
	mutate "$MOCK" 's|if _, ok := m\.s3OrphanRecoveryRoots\[key\]; !ok \{|if false \{|'
	expect_red 'TestG1RootOnlyReplayReusesLifecycleTokenAcrossDifferentClocks' 'recovery root' \
		'recovery root publication is removed'
	restore
}

m14_lifecycle_token_is_stable() {
	mutate "$MOCK" 's{rootFirstSeenAt := lifecycle.FirstSeenAt}{rootFirstSeenAt := now}'
	expect_red 'TestG1RootOnlyReplayReusesLifecycleTokenAcrossDifferentClocks' 'replay token' \
		'root-only replay changes first_seen_at'
	restore
}

m15_root_settlement_is_page_bounded() {
	mutate "$WORKER" 's|ListS3OrphanRecoveryRoots\(bucket, pageState, pageSize\)|ListS3OrphanRecoveryRoots(bucket, pageState, pageSize + 1)|'
	expect_red 'TestG1SourceContractsKeepRootBeforeCanonicalAndSettlementBounded' 'bounded page size' \
		'root reconciliation stops passing its bounded page size'
	restore
}

m16_utc_root_day() {
	mutate "$WORKER" 's{rootScanStart = db\.GCProjectionUTCDate\(firstSeenAt\)}{rootScanStart = firstSeenAt}'
	expect_red 'TestG1RootScanReturnsUTCProjectionDay' 'root scan start' \
		'root recovery returns a time-of-day instead of a UTC day'
	restore
}

m17_root_errors_do_not_freeze_by_day_cursor() {
	mutate "$WORKER" 's{recovered := rootRecovered\n\tvar phaseErr error}{recovered := rootRecovered\n\tvar phaseErr = rootErr}'
	expect_red 'TestG1RootErrorDoesNotFreezeByDayCursor' 'cursor after root-only error' \
		'root enumeration error freezes the by-day cursor'
	restore
}

MUTATIONS=(
  m1_canonical_identity_loses_delete_authority
  m2_projection_identity_loses_delete_authority
  m3_canonical_delete_omits_delete_authority
  m4_recovery_root_required
  m5_no_orphan_ttl
  m6_prepared_authorizes_s3
  m7_root_before_canonical_settlement
  m8_claim_timestamp_loses_millisecond_normalization
  m9_missing_canonical_is_settled
  m10_writer_fence_ignores_orphan
  m11_canonical_storage_key_identity
  m12_projection_storage_key_identity
  m13_recovery_root_publication_removed
  m14_lifecycle_token_is_stable
  m15_root_settlement_is_page_bounded
  m16_utc_root_day
  m17_root_errors_do_not_freeze_by_day_cursor
)

if [ "${1:-}" = "--list" ]; then
  printf '%s\n' "${MUTATIONS[@]}"
  exit 0
fi

printf 'Baseline (unmutated) must be green...\n'
if ! go test ./internal/gc -count=1 >/dev/null 2>&1; then
  fail 'the unmutated internal/gc suite is already red'
fi
green '  baseline green'

if [ $# -gt 0 ]; then
  MUTATIONS=("$1")
fi

for mutation in "${MUTATIONS[@]}"; do
  printf '\n%s\n' "$mutation"
  "$mutation"
done

restore
green "All ${#MUTATIONS[@]} G1 mutation(s) produced the expected red."
