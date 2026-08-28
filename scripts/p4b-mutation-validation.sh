#!/usr/bin/env bash
#
# P4b mutation evidence: prove the orphan publication guards fail when their
# write-once, serial-settlement, or fail-closed invariant is removed.
#
#   ./scripts/p4b-mutation-validation.sh          # run every mutation
#   ./scripts/p4b-mutation-validation.sh <name>   # run one (see --list)
#   ./scripts/p4b-mutation-validation.sh --list   # names only
#
# Unit-level only: no Cassandra, MinIO, or application stack is required.

set -uo pipefail
cd "$(dirname "$0")/.."

STORE=internal/gc/store_cassandra.go
MOCK=internal/gc/store_mock.go
WORKER=internal/gc/worker.go

green() { printf '\033[32m%s\033[0m\n' "$*"; }
red() { printf '\033[31m%s\033[0m\n' "$*" >&2; }

BACKUPS=()
restore() {
  local f
  for f in "${BACKUPS[@]:-}"; do
    if [ -n "$f" ] && [ -f "$f.p4bbak" ]; then mv -f "$f.p4bbak" "$f"; fi
  done
  BACKUPS=()
}
fail() { red "FAILED: $*"; restore; exit 1; }
trap restore EXIT INT TERM

mutate() {
  local f="$1" expr="$2"
  cp "$f" "$f.p4bbak"
  BACKUPS+=("$f")
  perl -0pi -e "$expr" "$f"
  if cmp -s "$f" "$f.p4bbak"; then
    fail "mutation did not apply to $f"
  fi
}

expect_red() {
  local pattern="$1" needle="$2" what="$3" out status
  out="$(go test ./internal/gc -count=1 -run "$pattern" 2>&1)"
  status=$?
  if [ $status -eq 0 ]; then
    printf '%s\n' "$out" | tail -15 >&2
    fail "$what: the suite stayed green"
  fi
  if ! printf '%s\n' "$out" | grep -qF "$needle"; then
    printf '%s\n' "$out" | tail -25 >&2
    fail "$what: failed without the expected P4b assertion: $needle"
  fi
  green "  RED as required: $what"
}

m_lwt_loses_write_once() {
  mutate "$STORE" 's{VALUES \(\?, \?, \?, \?, \?, \?, \?, \?, \?, \?\) IF NOT EXISTS}{VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)}'
  expect_red 'TestP4B_StartBlockDeleteOrphanSourceContract' 'IF NOT EXISTS' \
    'orphan publication loses its write-once LWT'
  restore
}

m_settlement_read_is_ordinary() {
  mutate "$STORE" 's{(SELECT storage_class, storage_key, first_seen_at\s+FROM gc_s3_orphans\s+WHERE org_id = \? AND block_id = \?\s+`, orgID\.String\(\), blockID\)\.\s+)Consistency\(gocql\.Serial\)\.}{$1}'
  expect_red 'TestP4B_StartBlockDeleteOrphanSourceContract' 'must read the canonical row at Consistency(gocql.Serial)' \
    'orphan settlement read is downgraded from SERIAL'
  restore
}

m_same_target_uses_proposed_timestamp() {
  mutate "$MOCK" 's{result\.FirstSeenAt = existing\.FirstSeenAt}{result.FirstSeenAt = now}'
  expect_red 'TestStore_StartBlockDeleteOrphan_SameTargetUsesStoredFirstSeenAndRepairsProjection' 'effective first_seen_at' \
    'same-target retry uses the proposed timestamp instead of the stored lifecycle token'
  restore
}

m_projection_uncertainty_finalizes() {
  mutate "$WORKER" 's{case StartBlockDeleteOrphanProjectionUnconfirmed:\s+w\.recordDestructiveBlocked\(destructivePathBlock\)\s+return blockOrphanPublicationError\{ItemID: item\.ItemID, Code: GCFailureCodeBlockOrphanProjectionUnconfirmed, Err: publication\.Cause\}}{case StartBlockDeleteOrphanProjectionUnconfirmed:\n\t\torphanFirstSeenAt = publication.FirstSeenAt}'
  expect_red 'TestP4B_WorkerProjectionUnconfirmedLeavesClaimAndQueueUntouched' 'want untouched publication refusal' \
    'projection uncertainty is incorrectly allowed to finalize the destructive lifecycle'
  restore
}

m_publication_invalid_reuses_the_candidate_code() {
  mutate "$WORKER" 's{GCFailureCodeBlockOrphanInvalid:}{GCFailureCodeBlockOrphanInvalid,
		GCFailureCodeBlockAuthorityInvalid:}'
  expect_red 'TestP4A_LateLoserLeavesQueueUntouched' 'its documented postpone is unreachable' \
    'publication-invalid shares block_authority_invalid, which silently moves the candidate code off its documented postpone path'
  restore
}

MUTATIONS=(
  m_lwt_loses_write_once
  m_settlement_read_is_ordinary
  m_same_target_uses_proposed_timestamp
  m_projection_uncertainty_finalizes
  m_publication_invalid_reuses_the_candidate_code
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
green "All ${#MUTATIONS[@]} P4b mutation(s) produced the expected red."
