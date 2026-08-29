#!/usr/bin/env bash
#
# R3 publish-fence mutation evidence: prove the post-stage authority check,
# attempt-scoped rollback, and stage-before-promote order fail when their
# invariants are removed.
#
#   ./scripts/r3-publish-fence-mutation-validation.sh          # every mutation
#   ./scripts/r3-publish-fence-mutation-validation.sh <name>     # one (see --list)
#   ./scripts/r3-publish-fence-mutation-validation.sh --list     # names only
#
# Unit/AST only: no Cassandra, MinIO, or application stack is required.

set -uo pipefail
cd "$(dirname "$0")/.."

AUTH=internal/db/block_publish_authority.go
REFS=internal/db/block_references.go
FS=internal/api/v2/fs_helpers.go
SYNC=internal/api/sync.go
FILES=internal/api/v2/files.go
REPAIR=internal/api/v2/publish_repair.go
GUARD=internal/api/r3_publish_handshake_guard_test.go

green() { printf '\033[32m%s\033[0m\n' "$*"; }
red() { printf '\033[31m%s\033[0m\n' "$*" >&2; }

BACKUPS=()
restore() {
  local f
  for f in "${BACKUPS[@]:-}"; do
    if [ -n "$f" ] && [ -f "$f.r3bak" ]; then mv -f "$f.r3bak" "$f"; fi
  done
  BACKUPS=()
}
fail() { red "FAILED: $*"; restore; exit 1; }
trap restore EXIT INT TERM

mutate() {
  local f="$1" expr="$2" before
  if [ ! -f "$f.r3bak" ]; then
    cp "$f" "$f.r3bak"
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
  local packages="$1" pattern="$2" needle="$3" what="$4" out status
  out="$(go test $packages -count=1 -run "$pattern" 2>&1)"
  status=$?
  if [ $status -eq 0 ]; then
    printf '%s\n' "$out" | tail -15 >&2
    fail "$what: the suite stayed green"
  fi
  if ! printf '%s\n' "$out" | grep -qF "$needle"; then
    printf '%s\n' "$out" | tail -25 >&2
    fail "$what: failed without the expected R3 assertion: $needle"
  fi
  green "  RED as required: $what"
}

m_remove_post_check_stage() {
  mutate "$REFS" 's#if err := FinishCheckedPublishAttempt\(database, orgID, repoID, attemptID, staged\); err != nil \{\s+return nil, err\s+\}##'
  expect_red './internal/db' 'TestR3StagePublishAttemptReferencesChecksAfterAdd' 'FinishCheckedPublishAttempt' \
    'StagePublishAttemptReferences no longer post-checks after a complete stage'
  restore
}

m_remove_post_check_funnel_b() {
  # error(nil) still compiles; the AST guard looks for the FinishChecked seam call.
  mutate "$FS" 's#stagePendingPublishedFilesFinishCheckedFn\(h\.db, orgID, repoID, attemptID, stagedBlockIDs\)#error(nil)#'
  expect_red './internal/api' 'TestR3FunnelBFinishCheckedRunsAfterAdds' 'FinishChecked' \
    'stagePendingPublishedFiles no longer post-checks after a complete batch'
  restore
}

m_check_before_stage() {
  mutate "$REFS" 's#resolved = NormalizeBlockIDs\(resolved\)\s+staged, err := addPublishAttemptReferencesRows\(database, orgID, repoID, attemptID, resolved\)#resolved = NormalizeBlockIDs(resolved)\n\tif err := FinishCheckedPublishAttempt(database, orgID, repoID, attemptID, resolved); err != nil {\n\t\treturn nil, err\n\t}\n\tstaged, err := addPublishAttemptReferencesRows(database, orgID, repoID, attemptID, resolved)#'
  mutate "$REFS" 's#if err := FinishCheckedPublishAttempt\(database, orgID, repoID, attemptID, staged\); err != nil \{\s+return nil, err\s+\}\s+return staged, nil#return staged, nil#'
  expect_red './internal/db' 'TestR3StagePublishAttemptReferencesChecksAfterAdd' 'pre-stage check' \
    'R3 check runs before addPublishAttemptReferencesRows'
  restore
}

m_ignore_deleting() {
  mutate "$AUTH" 's#if activeClaim \{#if false \&\& activeClaim {#'
  expect_red './internal/db' 'TestValidatePublishAttemptAuthorityClassifiesWriterFence' 'want deleting' \
    'an active deleting claim is treated as Active'
  restore
}

m_ignore_handoff() {
  mutate "$AUTH" 's#if row\.GCOrphanHandoff != nil \{#if false \&\& row.GCOrphanHandoff != nil {#'
  expect_red './internal/db' 'TestValidatePublishAttemptAuthorityHandoffIsNeverActive' 'ignoring handoff' \
    'gc_orphan_handoff=true is treated as Active'
  restore
}

m_accept_handoff_false() {
  mutate "$AUTH" 's#return BlockPublishAuthorityInvalid, fmt\.Errorf\("%w: block %s has gc_orphan_handoff=false; only null-to-true is a valid handoff", ErrBlockPublishAuthorityDenied, blockID\)#return BlockPublishAuthorityActive, nil#'
  expect_red './internal/db' 'TestValidatePublishAttemptAuthorityHandoffFalseIsInvalid' 'false must not be treated as Active' \
    'gc_orphan_handoff=false is treated as Active'
  restore
}

m_orphan_read_error_is_absent() {
  mutate "$AUTH" 's#if err != nil \{\s+return BlockPublishAuthorityUnavailable, fmt\.Errorf\("%w: read S3 orphan publish fence for %s: %w", ErrBlockPublishAuthorityDenied, blockID, err\)\s+\}#if err != nil {\n\t\thasOrphan = false\n\t\terr = nil\n\t}#'
  expect_red './internal/db' 'TestValidatePublishAttemptAuthorityClassifiesWriterFence|TestFinishCheckedPublishAttemptOrphanReadErrorRollsBack' 'want unavailable' \
    'an orphan read error is treated as no orphan'
  restore
}

m_ignore_repairing_stub() {
  mutate "$AUTH" 's#if repairClaim \{#if false \&\& repairClaim {#'
  expect_red './internal/db' 'TestValidatePublishAttemptAuthorityClassifiesWriterFence' 'want repairing' \
    'repairing_stub is treated as Active'
  restore
}

m_ignore_orphan() {
  mutate "$AUTH" 's#if hasOrphan \{\s+return BlockPublishAuthorityOrphaned, fmt\.Errorf\("%w: block %s has an S3 orphan fence", ErrBlockPublishAuthorityDenied, blockID\)#if false \&\& hasOrphan {\n\t\treturn BlockPublishAuthorityOrphaned, fmt.Errorf("%w: block %s has an S3 orphan fence", ErrBlockPublishAuthorityDenied, blockID)#'
  expect_red './internal/db' 'TestValidatePublishAttemptAuthorityClassifiesWriterFence' 'want orphaned' \
    'a live S3 orphan is treated as Active'
  restore
}

m_accept_missing() {
  mutate "$AUTH" 's#return BlockPublishAuthorityMissing, fmt\.Errorf\("%w: canonical row for block %s is absent", ErrBlockPublishAuthorityDenied, blockID\)#return BlockPublishAuthorityActive, nil#'
  expect_red './internal/db' 'TestValidatePublishAttemptAuthorityClassifiesWriterFence' 'want missing' \
    'an absent canonical row is treated as Active'
  restore
}

m_accept_empty_storage_key() {
  # Keep strings.TrimSpace so the package still compiles; empty key is the predicate.
  mutate "$AUTH" 's#row\.StorageKey == "" \|\|#false ||#'
  expect_red './internal/db' 'TestValidatePublishAttemptAuthorityClassifiesWriterFence' 'want invalid' \
    'an empty storage_key is accepted as a publishable locator'
  restore
}

m_lq_to_one() {
  mutate "$REFS" 's#const BlockFenceReadConsistency = gocql\.LocalQuorum#const BlockFenceReadConsistency = gocql.One#'
  expect_red './internal/db' 'TestR3FenceReadConsistencyIsLocalQuorum|TestP3FenceReadConsistencyIsLocalQuorum' 'want gocql.LocalQuorum' \
    'BlockFenceReadConsistency is lowered from LOCAL_QUORUM to ONE'
  restore
}

m_continue_after_failure() {
  mutate "$REFS" 's#if err := FinishCheckedPublishAttempt\(database, orgID, repoID, attemptID, staged\); err != nil \{\s+return nil, err\s+\}#if err := FinishCheckedPublishAttempt(database, orgID, repoID, attemptID, staged); err != nil {\n\t\t_ = err\n\t}#'
  expect_red './internal/db' 'TestStagePublishAttemptReferencesDeniedRollsBackAfterAdd' 'want ErrBlockPublishAuthorityDenied' \
    'Stage continues to success after R3 denial'
  restore
}

m_skip_rollback() {
  mutate "$AUTH" 's#cleanupErr := RemovePublishAttemptReferences\(database, orgID, attemptID, staged\)#cleanupErr := error(nil)#'
  expect_red './internal/db' 'TestFinishCheckedPublishAttemptRollsBackThisAttemptOnly' 'this attempt' \
    'denied attempts keep their pub: rows'
  restore
}

m_rollback_without_attempt_id() {
  mutate "$AUTH" 's#RemovePublishAttemptReferences\(database, orgID, attemptID, staged\)#RemovePublishAttemptReferences(database, orgID, "", staged)#'
  expect_red './internal/db' 'TestFinishCheckedPublishAttemptRollsBackThisAttemptOnly' 'want org-1/' \
    'rollback no longer names this attempt id'
  restore
}

m_rollback_failure_is_success() {
  mutate "$AUTH" 's#if cleanupErr != nil \{\s+metrics\.PublishAttemptRollbackTotal\.WithLabelValues\("error"\)\.Inc\(\)\s+denied := publishAuthorityDeniedError\(outcome, checkErr\)\s+return errors\.Join\(denied, fmt\.Errorf\("rollback staged publish-attempt refs for %s: %w", attemptID, cleanupErr\)\)\s+\}#if cleanupErr != nil {\n\t\tmetrics.PublishAttemptRollbackTotal.WithLabelValues("error").Inc()\n\t\treturn nil\n\t}#'
  expect_red './internal/db' 'TestFinishCheckedPublishAttemptRollbackFailureIsNeverSuccess' 'must never be treated as publication success' \
    'a rollback failure is treated as publication success'
  restore
}

m_sync_repair_skips_stage() {
  mutate "$SYNC" 's#delta, err := h\.stageSyncCommitBlockDelta\(orgID, repoID, targetCommitID\)\s+if err != nil \{\s+return err\s+\}\s+return h\.finalizeSyncCommitBlockDelta\(orgID, repoID, targetCommitID, delta\)#delta, err := buildSyncCommitBlockDeltaFn(h, repoID, targetCommitID)\n\tif err != nil {\n\t\treturn err\n\t}\n\treturn h.finalizeSyncCommitBlockDelta(orgID, repoID, targetCommitID, delta)#'
  expect_red './internal/api' 'TestR3ProductionPromoteCallersStageBeforePromote' 'promotes without staging' \
    'sync published-HEAD repair promotes without staging'
  restore
}

m_createfile_bypass() {
  mutate "$FILES" 's#fsHelper\.stagePendingPublishedFiles#fsHelper.promotePendingPublishedFiles#g'
  expect_red './internal/api' 'TestR3ProductionPromoteCallersStageBeforePromote' 'promotes without staging' \
    'CreateFile/upload promote without staging'
  restore
}

m_copy_bypass() {
  mutate "internal/api/v2/batch_operations.go" 's#fsHelper\.stagePendingPublishedFiles#fsHelper.promotePendingPublishedFiles#g'
  expect_red './internal/api' 'TestR3ProductionPromoteCallersStageBeforePromote' 'promotes without staging' \
    'copy/move promotes without staging'
  restore
}

m_onlyoffice_bypass() {
  mutate "internal/api/v2/onlyoffice.go" 's#fsHelper\.stagePendingPublishedFiles#fsHelper.promotePendingPublishedFiles#g'
  expect_red './internal/api' 'TestR3ProductionPromoteCallersStageBeforePromote' 'promotes without staging' \
    'OnlyOffice promotes without staging'
  restore
}

m_direct_add() {
  mutate "$SYNC" 's#var stageSyncPublishAttemptReferencesFn = db\.StagePublishAttemptReferences#var stageSyncPublishAttemptReferencesFn = db.StagePublishAttemptReferences\n\tvar _ = db.AddPublishAttemptReferences#'
  # A CallExpr is what the guard looks for; a bare identifier is not enough.
  mutate "$SYNC" 's#var _ = db\.AddPublishAttemptReferences#func r3MutationDirectAdd() { _ = db.AddPublishAttemptReferences(nil, "", "", "", nil) }#'
  expect_red './internal/api' 'TestR3ProductionDoesNotCallAddPublishAttemptReferencesDirectly' 'calls AddPublishAttemptReferences directly' \
    'production calls AddPublishAttemptReferences without an R3 funnel'
  restore
}

m_sync_repair_drop_return() {
  mutate "$SYNC" 's#delta, err := h\.stageSyncCommitBlockDelta\(orgID, repoID, targetCommitID\)\s+if err != nil \{\s+return err\s+\}\s+return h\.finalizeSyncCommitBlockDelta#delta, err := h.stageSyncCommitBlockDelta(orgID, repoID, targetCommitID)\n\tif err != nil {\n\t\t_ = err\n\t}\n\treturn h.finalizeSyncCommitBlockDelta#'
  expect_red './internal/api' 'TestPublishedSyncRepairPartialStageFailureDoesNotFinalize' 'want stage boom' \
    'sync repair continues to promote after a stage error'
  restore
}

m_head_drop_return() {
  mutate "$SYNC" 's#c\.JSON\(http\.StatusServiceUnavailable, gin\.H\{"error": "sync head publish block references pending; retry"\}\)\s+return#c.JSON(http.StatusServiceUnavailable, gin.H{"error": "sync head publish block references pending; retry"})#'
  expect_red './internal/api' 'TestR3ProductionStageErrorsAbortBeforePromote' 'does not abort on error before promote' \
    'handleSyncHeadPromotion continues after a stage error'
  restore
}

m_automerge_drop_return() {
  mutate "$SYNC" 's#delta, err := h\.stageSyncCommitBlockDelta\(orgID, repoID, mergedCommitID\)\s+if err != nil \{\s+return false, err\s+\}#delta, err := h.stageSyncCommitBlockDelta(orgID, repoID, mergedCommitID)\n\tif err != nil {\n\t\t_ = err\n\t}#'
  expect_red './internal/api' 'TestR3ProductionStageErrorsAbortBeforePromote' 'does not abort on error before promote' \
    'tryAutoMergeSyncHeadPromotion continues after a stage error'
  restore
}

m_seafhttp_drop_return() {
  mutate "internal/api/seafhttp.go" 's#return "", "", 0, 0, fmt\.Errorf\("failed to stage publish-attempt block references: %w", err\)#_ = err#'
  expect_red './internal/api' 'TestR3ProductionStageErrorsAbortBeforePromote' 'does not abort on error before promote' \
    'SeafHTTP continues after a stage error'
  restore
}

m_createfile_drop_return() {
  mutate "$FILES" 's#if err := fsHelper\.stagePendingPublishedFiles\(([^;]+)\); err != nil \{#_ = fsHelper.stagePendingPublishedFiles($1); if false {#g'
  expect_red './internal/api' 'TestR3ProductionStageErrorsAbortBeforePromote' 'does not abort on error before promote' \
    'CreateFile/upload continue after a stage error'
  restore
}

m_copy_drop_return() {
  mutate "internal/api/v2/batch_operations.go" 's#if err := fsHelper\.stagePendingPublishedFiles\(([^;]+)\); err != nil \{#_ = fsHelper.stagePendingPublishedFiles($1); if false {#g'
  expect_red './internal/api' 'TestR3ProductionStageErrorsAbortBeforePromote' 'does not abort on error before promote' \
    'copy/move continues after a stage error'
  restore
}

m_onlyoffice_drop_return() {
  mutate "internal/api/v2/onlyoffice.go" 's#if err := fsHelper\.stagePendingPublishedFiles\(([^;]+)\); err != nil \{#_ = fsHelper.stagePendingPublishedFiles($1); if false {#'
  expect_red './internal/api' 'TestR3ProductionStageErrorsAbortBeforePromote' 'does not abort on error before promote' \
    'OnlyOffice continues after a stage error'
  restore
}

m_v2_repair_runs_r3() {
  mutate "$REPAIR" 's#if err := publishedBlockReferenceRepairPromoteFn\(helper, repair\.OrgID, repair\.RepoID, repair\.CommitID, pending\); err != nil \{#if err := db.FinishCheckedPublishAttempt(database, repair.OrgID, repair.RepoID, repair.CommitID, repair.StagedBlockIDs); err != nil {\n\t\t\treturn err\n\t\t}\n\t\tif err := publishedBlockReferenceRepairPromoteFn(helper, repair.OrgID, repair.RepoID, repair.CommitID, pending); err != nil {#'
  expect_red './internal/api' 'TestR3PublishRepairMustNotRunAuthorityCheck' 'promote-only' \
    'v2 durable repair runs R3+rollback on a possibly-already-published HEAD'
  restore
}

MUTATIONS=(
  m_remove_post_check_stage
  m_remove_post_check_funnel_b
  m_check_before_stage
  m_ignore_deleting
  m_ignore_handoff
  m_accept_handoff_false
  m_orphan_read_error_is_absent
  m_ignore_repairing_stub
  m_ignore_orphan
  m_accept_missing
  m_accept_empty_storage_key
  m_lq_to_one
  m_continue_after_failure
  m_skip_rollback
  m_rollback_without_attempt_id
  m_rollback_failure_is_success
  m_sync_repair_skips_stage
  m_sync_repair_drop_return
  m_head_drop_return
  m_automerge_drop_return
  m_seafhttp_drop_return
  m_createfile_bypass
  m_createfile_drop_return
  m_copy_bypass
  m_copy_drop_return
  m_onlyoffice_bypass
  m_onlyoffice_drop_return
  m_direct_add
  m_v2_repair_runs_r3
)

if [ "${1:-}" = "--list" ]; then
  printf '%s\n' "${MUTATIONS[@]}"
  exit 0
fi

if [ -n "${1:-}" ]; then
  found=0
  for m in "${MUTATIONS[@]}"; do
    if [ "$m" = "$1" ]; then found=1; "$m"; break; fi
  done
  [ "$found" = 1 ] || fail "unknown mutation $1 (try --list)"
  green "R3 mutation $1 went RED as required."
  exit 0
fi

for m in "${MUTATIONS[@]}"; do
  green "==> $m"
  "$m"
done
green "All ${#MUTATIONS[@]} R3 mutations went RED as required."
