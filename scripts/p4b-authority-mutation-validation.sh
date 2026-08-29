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
  local f="$1" expr="$2" before
  if [ ! -f "$f.p4b2bak" ]; then
    cp "$f" "$f.p4b2bak"
    BACKUPS+=("$f")
  fi
  before="$(mktemp)"
  cp "$f" "$before"
  perl -0pi -e "$expr" "$f"
  if cmp -s "$f" "$before"; then
    rm -f "$before"
    fail "mutation did not apply to $f"
  fi
  rm -f "$before"
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

m_already_committed_skips_each_quorum() {
  mutate "$STORE" 's#return s\.maybeConfirmAlreadyCommittedHandoffEachQuorum\(orgID, blockID, authority, classifyBlockDeleteHandoffRow\(row, authority\)\)#classified := classifyBlockDeleteHandoffRow(row, authority)\n\treturn classified, classified.Cause#'
  mutate "$STORE" 's#return s\.confirmAlreadyCommittedHandoffEachQuorum\(orgID, blockID, authority\)#return classified, nil#g'
  mutate "$MOCK" 's{if m\.claimBlockDeleteEachQuorumErr != nil \|\| confirmed\.Outcome != BlockDeleteHandoffAlreadyCommitted}{if false && (m.claimBlockDeleteEachQuorumErr != nil || confirmed.Outcome != BlockDeleteHandoffAlreadyCommitted)}'
  expect_red 'TestP4B_AlreadyCommittedHandoffFailsClosedWhenEachQuorumUnconfirmed' 'must not be treated as durable authority' \
    'AlreadyCommitted skips EACH_QUORUM confirmation'
  restore
}

m_committed_owner_skips_each_quorum() {
  mutate "$STORE" 's#return s\.maybeConfirmCommittedOwnerEachQuorum\(orgID, blockID, attempt, settled\)#return settled, nil#'
  mutate "$STORE" 's#return s\.maybeConfirmCommittedOwnerEachQuorum\(orgID, blockID, attempt, row\.result\(attempt, staleBefore\)\)#return row.result(attempt, staleBefore), nil#'
  mutate "$MOCK" 's#func \(m \*MockStore\) confirmMockCommittedOwner\(row blockDeleteClaimRow, attempt BlockDeleteAuthority, result BlockClaimResult\) \(BlockClaimResult, error\) \{\s+if result.Outcome != BlockClaimCommittedOwner#func (m *MockStore) confirmMockCommittedOwner(row blockDeleteClaimRow, attempt BlockDeleteAuthority, result BlockClaimResult) (BlockClaimResult, error) {\n\treturn result, nil\n\tif result.Outcome != BlockClaimCommittedOwner#'
  expect_red 'TestP4B_CommittedOwnerFailsClosedWhenEachQuorumUnconfirmed' 'must not be success' \
    'CommittedOwner skips EACH_QUORUM confirmation'
  restore
}

m_committed_owner_skips_refs() {
  mutate "$WORKER" 's#if alreadyCommitted \{\s+hasRefs, err = w\.store\.BlockHasReferencesGlobal#if false {\n\t\thasRefs, err = w.store.BlockHasReferencesGlobal#'
  expect_red 'TestProcessBlockCommittedHandoffIsNotReleasedOnPreClaimRefsBranch' 'want committed-pending contradiction' \
    'CommittedOwner no longer treats global refs as a contradiction'
  restore
}

m_orphan_skips_lifecycle() {
  mutate "$STORE" 's#lifecycle := s\.insertBlockDeleteLifecycle\(orgID, blockID, proposed\)\s+if lifecycle\.Outcome == StartBlockDeleteOrphanLifecycleAdvanced \{\s+return lifecycle\s+\}\s+if lifecycle\.Outcome != StartBlockDeleteOrphanCreated && lifecycle\.Outcome != StartBlockDeleteOrphanSameAuthority \{\s+return lifecycle\s+\}\s+##'
  mutate "$MOCK" 's#lifecycle := m\.insertMockBlockDeleteLifecycleLocked\(orgID, blockID, proposed\)\s+if lifecycle\.Outcome == StartBlockDeleteOrphanLifecycleAdvanced \{\s+m\.mu\.Unlock\(\)\s+return lifecycle\s+\}\s+if lifecycle\.Outcome != StartBlockDeleteOrphanCreated && lifecycle\.Outcome != StartBlockDeleteOrphanSameAuthority \{\s+m\.mu\.Unlock\(\)\s+return lifecycle\s+\}\s+##'
  expect_red 'TestP4B_StartBlockDeleteOrphanSourceContract' 'must insert the durable D tombstone' \
    'StartBlockDeleteOrphan no longer inserts the lifecycle tombstone'
  restore
}

m_clear_orphan_skips_terminal() {
  mutate "$WORKER" 's#terminated, err := w\.store\.TerminateBlockDeleteLifecycle.*?return w\.store\.DeleteS3Orphan#return w.store.DeleteS3Orphan#s'
  expect_red 'TestProcessBlockCommittedOwnerDoesNotMintANewClaim' 'must CAS the lifecycle tombstone to terminal' \
    'DeleteS3Orphan runs without a published→terminal CAS'
  restore
}

m_terminal_lifecycle_is_same_authority() {
  mutate "$STORE" 's#case BlockDeleteLifecyclePhaseTerminal:\s+result\.Outcome = StartBlockDeleteOrphanLifecycleAdvanced\s+result\.Cause = errors\.New\("block-delete lifecycle is terminal; D is no longer destructive authority"\)#case BlockDeleteLifecyclePhaseTerminal:\n\t\tresult.Outcome = StartBlockDeleteOrphanSameAuthority#'
  mutate "$STORE" 's#case BlockDeleteLifecyclePhaseTerminal:\s+return BlockDeleteFinalizeResult\{\s+Outcome: BlockDeleteAlreadyComplete,\s+Cause:   errors\.New\("lifecycle is terminal; physical delete is not authorized"\),\s+\}#case BlockDeleteLifecyclePhaseTerminal:\n\t\treturn BlockDeleteFinalizeResult{Outcome: BlockDeleteAlreadyFinalized, Cause: cause}#'
  expect_red 'TestP4B_AlreadyFinalizedDoesNotAuthorizeWhenLifecycleTerminal' 'want already_complete' \
    'phase=terminal is treated as SameAuthority / AlreadyFinalized that authorizes S3'
  restore
}

m_finalize_ignores_lifecycle_certificate() {
  mutate "$STORE" 's#if !lifeFound \{\s+return BlockDeleteFinalizeResult\{\s+Outcome: BlockDeleteNotAuthority#if false \&\& !lifeFound {\n\treturn BlockDeleteFinalizeResult{\n\t\tOutcome: BlockDeleteNotAuthority#'
  mutate "$STORE" 's#if !stored\.sameAuthority\(proposed\) \{\s+return BlockDeleteFinalizeResult\{\s+Outcome: BlockDeleteNotAuthority#if false \&\& !stored.sameAuthority(proposed) {\n\treturn BlockDeleteFinalizeResult{\n\t\tOutcome: BlockDeleteNotAuthority#'
  mutate "$STORE" 's#default:\s+return BlockDeleteFinalizeResult\{\s+Outcome: BlockDeleteInvalid#default:\n\t\treturn BlockDeleteFinalizeResult{\n\t\t\tOutcome: BlockDeleteAlreadyFinalized#'
  expect_red 'TestP4B_FinalizeAbsentRequiresExactPublishedLifecycleCertificate' 'want fail-closed' \
    'missing/mismatch/garbage lifecycle still yields AlreadyFinalized'
  restore
}

m_already_finalized_authorizes_s3() {
  mutate "$WORKER" 's#finalized\.authorizesPhysicalDelete\(\)#finalized.ok()#'
  expect_red 'TestP4B_WorkerAlreadyFinalizedLoserDoesNotDeleteP1AfterWriterPut' 'AlreadyFinalized must not emit a second DELETE of P1' \
    'AlreadyFinalized is treated as permission to delete bytes'
  restore
}

m_orphan_skips_lifecycle_postcheck() {
  mutate "$STORE" 's#return s\.confirmPublishedLifecycleAfterOrphan\(orgID, blockID, proposed, s\.ensureS3OrphanProjectionResult\(orgID, blockID, result\)\)#return s.ensureS3OrphanProjectionResult(orgID, blockID, result)#'
  mutate "$STORE" 's#return s\.confirmPublishedLifecycleAfterOrphan\(orgID, blockID, proposed, s\.confirmSameAuthorityOrphanResult\(orgID, blockID, proposed, classified\.Result\)\)#return s.confirmSameAuthorityOrphanResult(orgID, blockID, proposed, classified.Result)#'
  mutate "$STORE" 's#return s\.confirmPublishedLifecycleAfterOrphan\(orgID, blockID, proposed, s\.settleStartBlockDeleteOrphan\(orgID, blockID, proposed, err\)\)#return s.settleStartBlockDeleteOrphan(orgID, blockID, proposed, err)#'
  mutate "$STORE" 's#return s\.confirmPublishedLifecycleAfterOrphan\(orgID, blockID, proposed, s\.settleStartBlockDeleteOrphan\(orgID, blockID, proposed, nil\)\)#return s.settleStartBlockDeleteOrphan(orgID, blockID, proposed, nil)#'
  mutate "$MOCK" 's#return m\.confirmPublishedLifecycleAfterOrphanLocked\(orgID, blockID, proposed, m\.confirmSameAuthorityOrphanResultLocked\(orgID, blockID, classified\)\)#return m.confirmSameAuthorityOrphanResultLocked(orgID, blockID, classified)#'
  mutate "$MOCK" 's#return m\.confirmPublishedLifecycleAfterOrphanLocked\(orgID, blockID, proposed, m\.ensureS3OrphanProjectionResultLocked\(orgID, blockID, result\)\)#return m.ensureS3OrphanProjectionResultLocked(orgID, blockID, result)#'
  expect_red 'TestP4B_StartBlockDeleteOrphanPostCheckSeesTerminalAfterPublicationRace' 'must not return Created' \
    'StartBlockDeleteOrphan no longer SERIAL-checks the lifecycle after orphan INSERT'
  restore
}

m_recovery_ignores_terminal_lifecycle() {
  mutate "$WORKER" 's#lifecycle := w\.store\.ObserveBlockDeleteLifecycle\(canonical\.OrgID, canonical\.BlockID, committedBlockDeleteAuthority\(canonical\.Authority\)\)#lifecycle := StartBlockDeleteOrphanResult{Outcome: StartBlockDeleteOrphanSameAuthority}#'
  expect_red 'TestP4B_RecoverS3OrphansTerminalLifecycleDoesNotDeleteS3' 'must not authorize recovery S3' \
    'pending_s3 recovery ignores a terminal lifecycle tombstone'
  restore
}

m_lifecycle_veto_uses_each_quorum() {
  mutate "$STORE" 's{(func \(s \*CassandraStore\) settleBlockDeleteLifecycleState.*?Consistency\()gocql\.Serial}{$1gocql.EachQuorum}s'
  expect_red 'TestP4B_InsertBlockDeleteLifecycleSourceContract' 'must read at Consistency(gocql.Serial)' \
    'lifecycle decision reads are downgraded from SERIAL to EACH_QUORUM'
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
  m_already_committed_skips_each_quorum
  m_committed_owner_skips_each_quorum
  m_committed_owner_skips_refs
  m_orphan_skips_lifecycle
  m_clear_orphan_skips_terminal
  m_terminal_lifecycle_is_same_authority
  m_finalize_ignores_lifecycle_certificate
  m_already_finalized_authorizes_s3
  m_orphan_skips_lifecycle_postcheck
  m_recovery_ignores_terminal_lifecycle
  m_lifecycle_veto_uses_each_quorum
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
