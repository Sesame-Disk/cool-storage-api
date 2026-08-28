#!/usr/bin/env bash
#
# P4b-2 / R14b mutation evidence: prove the orphan-handoff commit, exact (P,D)
# orphan identity, and finalize predicate fail when their invariants are removed.
#
#   ./scripts/p4b-authority-mutation-validation.sh          # run every mutation
#   ./scripts/p4b-authority-mutation-validation.sh <name>   # run one (see --list)
#   ./scripts/p4b-authority-mutation-validation.sh --list   # names only
#
# P4b-1 write-once publication evidence remains scripts/p4b-mutation-validation.sh.
# Unit-level only: no Cassandra, MinIO, or application stack is required.

set -uo pipefail
cd "$(dirname "$0")/.."

STORE=internal/gc/store_cassandra.go
MOCK=internal/gc/store_mock.go
WORKER=internal/gc/worker.go
GUARD=internal/gc/p4a_claim_authority_guard_test.go

green() { printf '\033[32m%s\033[0m\n' "$*"; }
red() { printf '\033[31m%s\033[0m\n' "$*" >&2; }

BACKUPS=()
restore() {
  local f
  for f in "${BACKUPS[@]:-}"; do
    if [ -n "$f" ] && [ -f "$f.p4b2bak" ]; then mv -f "$f.p4b2bak" "$f"; fi
  done
  BACKUPS=()
}
fail() { red "FAILED: $*"; restore; exit 1; }
trap restore EXIT INT TERM

mutate() {
  local f="$1" expr="$2"
  cp "$f" "$f.p4b2bak"
  BACKUPS+=("$f")
  perl -0pi -e "$expr" "$f"
  if cmp -s "$f" "$f.p4b2bak"; then
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
    fail "$what: failed without the expected P4b-2 assertion: $needle"
  fi
  green "  RED as required: $what"
}

m_handoff_drops_storage_class() {
  mutate "$STORE" 's{IF storage_class = \? AND storage_key = \?\s+AND gc_state = \? AND gc_claim_id = \? AND gc_claimed_at = \?\s+AND gc_orphan_handoff = null}{IF storage_key = ? AND gc_state = ? AND gc_claim_id = ? AND gc_claimed_at = ? AND gc_orphan_handoff = null}'
  expect_red 'TestP4ADestructiveMutationsNameTheExactIncarnation|TestP4B_CommitBlockDeleteOrphanHandoffSourceContract' 'storage_class = ?' \
    'handoff LWT drops storage_class from its IF'
  restore
}

m_handoff_drops_storage_key() {
  mutate "$STORE" 's{IF storage_class = \? AND storage_key = \?\s+AND gc_state = \? AND gc_claim_id = \? AND gc_claimed_at = \?\s+AND gc_orphan_handoff = null}{IF storage_class = ? AND gc_state = ? AND gc_claim_id = ? AND gc_claimed_at = ? AND gc_orphan_handoff = null}'
  expect_red 'TestP4ADestructiveMutationsNameTheExactIncarnation|TestP4B_CommitBlockDeleteOrphanHandoffSourceContract' 'storage_key = ?' \
    'handoff LWT drops storage_key from its IF'
  restore
}

m_handoff_drops_claim_id() {
  mutate "$STORE" 's{AND gc_state = \? AND gc_claim_id = \? AND gc_claimed_at = \?\s+AND gc_orphan_handoff = null}{AND gc_state = ? AND gc_claimed_at = ? AND gc_orphan_handoff = null}'
  expect_red 'TestP4ADestructiveMutationsNameTheExactIncarnation|TestP4B_CommitBlockDeleteOrphanHandoffSourceContract' 'gc_claim_id = ?' \
    'handoff LWT drops gc_claim_id from its IF'
  restore
}

m_handoff_drops_claimed_at() {
  mutate "$STORE" 's{AND gc_state = \? AND gc_claim_id = \? AND gc_claimed_at = \?\s+AND gc_orphan_handoff = null}{AND gc_state = ? AND gc_claim_id = ? AND gc_orphan_handoff = null}'
  expect_red 'TestP4ADestructiveMutationsNameTheExactIncarnation|TestP4B_CommitBlockDeleteOrphanHandoffSourceContract' 'gc_claimed_at = ?' \
    'handoff LWT drops gc_claimed_at from its IF'
  restore
}

m_release_ignores_handoff() {
  mutate "$STORE" 's{AND storage_class = \? AND storage_key = \?\s+AND gc_orphan_handoff = null}{AND storage_class = ? AND storage_key = ?}'
  mutate "$MOCK" 's{if orphanHandoffCommitted\(b\.GCOrphanHandoff\) \{\s+return BlockReleaseNotOwner, nil\s+\}}{}'
  expect_red 'TestP4B_ReleaseAndClaimRefuseCommittedHandoff' 'want not_owner' \
    'ReleaseBlockClaim can drop a committed handoff'
  restore
}

m_stale_release_treats_handoff_as_releasable() {
  mutate "$STORE" 's{if orphanHandoffCommitted\(row\.GCOrphanHandoff\) \{\s+return BlockClaimCommittedHandoff, nil\s+\}}{}'
  mutate "$MOCK" 's{if orphanHandoffCommitted\(b\.GCOrphanHandoff\) \{\s+return BlockClaimCommittedHandoff, nil\s+\}}{}'
  expect_red 'TestP4B_ReleaseAndClaimRefuseCommittedHandoff|TestProcessBlockCommittedHandoffIsNotReleasedOnPreClaimRefsBranch' 'committed_handoff' \
    'stale release treats a committed handoff as an ordinary claim'
  restore
}

m_orphan_omits_claim_columns() {
  mutate "$STORE" 's{last_error, gc_claim_id, gc_claimed_at\)}{last_error)}'
  expect_red 'TestP4B_StartBlockDeleteOrphanSourceContract' 'must persist the committed claim authority' \
    'orphan INSERT no longer persists D'
  restore
}

m_same_p_diff_d_is_same_authority() {
  mutate "$STORE" 's{if !row\.Authority\.sameClaim\(proposed\) \{\s+result\.Outcome = StartBlockDeleteOrphanDifferentAuthority\s+result\.Cause = errors\.New\("existing S3 orphan names a different delete authority"\)\s+return result\s+\}}{}'
  expect_red 'TestP4B_SettledOrphanClassification' 'want different_authority' \
    'same-P different-D is classified as an idempotent resume'
  restore
}

m_finalize_drops_handoff() {
  mutate "$STORE" 's{AND gc_orphan_handoff = true}{}'
  expect_red 'TestP4ADestructiveMutationsNameTheExactIncarnation|TestP4B_FinalizeRequiresCommittedHandoff' 'gc_orphan_handoff = true' \
    'FinalizeBlockDelete no longer requires a committed handoff'
  restore
}

m_handoff_each_quorum_to_quorum() {
  mutate "$STORE" 's{(func \(s \*CassandraStore\) CommitBlockDeleteOrphanHandoff.*?Consistency\()gocql\.EachQuorum}{$1gocql.Quorum}s'
  expect_red 'TestP4B_CommitBlockDeleteOrphanHandoffSourceContract' 'must pin regular consistency to EachQuorum' \
    'handoff LWT is downgraded from EACH_QUORUM to QUORUM'
  restore
}

m_handoff_local_serial() {
  mutate "$STORE" 's{(func \(s \*CassandraStore\) CommitBlockDeleteOrphanHandoff.*?SerialConsistency\()gocql\.Serial}{$1gocql.LocalSerial}s'
  expect_red 'TestP4B_CommitBlockDeleteOrphanHandoffSourceContract' 'must pin the LWT serial domain' \
    'handoff LWT serial phase is LOCAL_SERIAL'
  restore
}

m_claim_omits_handoff_predicate() {
  mutate "$STORE" 's{AND gc_state = null AND gc_claim_id = null AND gc_claimed_at = null\s+AND gc_orphan_handoff = null}{AND gc_state = null AND gc_claim_id = null AND gc_claimed_at = null}'
  expect_red 'TestP4ADestructiveMutationsNameTheExactIncarnation' 'gc_orphan_handoff = null' \
    'claim LWT no longer includes gc_orphan_handoff, so CommittedOwner cannot be classified from the CAS map'
  restore
}

m_worker_releases_after_handoff() {
  # ReleaseBlockClaim now refuses handoff=true, so calling the old unwind is not
  # enough on its own: the mock must also drop that refusal or the suite stays green.
  mutate "$WORKER" 's{case StartBlockDeleteOrphanDifferentTarget, StartBlockDeleteOrphanDifferentAuthority, StartBlockDeleteOrphanUnboundAuthority, StartBlockDeleteOrphanNotPublished:\s+return blockDeleteCommittedPendingError\{ItemID: item.ItemID, Err: publication.Cause\}}{case StartBlockDeleteOrphanDifferentTarget, StartBlockDeleteOrphanDifferentAuthority, StartBlockDeleteOrphanUnboundAuthority, StartBlockDeleteOrphanNotPublished:\n\t\treturn w.releaseOrphanClaimAndPostpone(item, deleteAuthority, GCFailureCodeBlockOrphanConflict, publication.Cause)}'
  mutate "$MOCK" 's{if orphanHandoffCommitted\(b\.GCOrphanHandoff\) \{\s+return BlockReleaseNotOwner, nil\s+\}}{}'
  expect_red 'TestP4B_WorkerDifferentTargetLeavesCommittedClaimUntouched' 'must retain the committed claim' \
    'worker releases a committed authority after a competing orphan outcome'
  restore
}

MUTATIONS=(
  m_handoff_drops_storage_class
  m_handoff_drops_storage_key
  m_handoff_drops_claim_id
  m_handoff_drops_claimed_at
  m_release_ignores_handoff
  m_stale_release_treats_handoff_as_releasable
  m_orphan_omits_claim_columns
  m_same_p_diff_d_is_same_authority
  m_finalize_drops_handoff
  m_handoff_each_quorum_to_quorum
  m_handoff_local_serial
  m_claim_omits_handoff_predicate
  m_worker_releases_after_handoff
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

green "P4b-2 authority mutations: all ${#MUTATIONS[@]} RED as required"
