#!/usr/bin/env bash
#
# P4a + R26 mutation evidence: prove the exact-incarnation guards actually fail when
# the invariant they protect is removed.
#
# P4a covers destructive AUTHORITY: which incarnation a claim, release or finalize may
# act on. R26 covers durable IDENTITY: that the same incarnation is part of the primary
# key of the candidate, its discovery row, the queue row and the pending row, so two
# lives of one logical block can never collapse into one row.
#
#   ./scripts/p4a-mutation-validation.sh          # run every mutation
#   ./scripts/p4a-mutation-validation.sh <name>   # run one (see --list)
#   ./scripts/p4a-mutation-validation.sh --list   # names only
#
# Run it in the test container, where the toolchain lives:
#   docker compose run --rm --build gotest bash scripts/p4a-mutation-validation.sh
#
# WHY THIS EXISTS
# ---------------
# A green gate means nothing until it has been shown to go red against the defect it
# exists to catch. Every invariant P4a adds lives in CQL text or in a classifier branch,
# and none of it is protected by the type system: ClaimBlockDelete keeps compiling after
# storage_key is dropped from its IF, a non-applied claim can go back to meaning
# "complete the candidate", and a serial settling read can quietly become an ordinary
# one. Each of those silently reopens R14, R16 or R20.
#
# So each mutation removes exactly ONE invariant and requires the suite to fail — and to
# fail with a P4a assertion rather than for some unrelated reason. A mutation that breaks
# the build, or that trips a different test first, proves nothing about its guard.
#
# PORTABILITY, learned the hard way in X2: line endings and indentation differ between
# the working tree and the container's COPY, so every pattern here matches runs of
# whitespace rather than exact newlines, and every mutation verifies it actually changed
# the file before anything is run.
#
# Unit-level only: no Cassandra, no MinIO, no stack.

set -uo pipefail
cd "$(dirname "$0")/.."

STORE=internal/gc/store_cassandra.go
WORKER=internal/gc/worker.go
MOCK=internal/gc/store_mock.go
PROJECTIONS=internal/db/gc_projection_write_helpers.go
MIGRATION=internal/db/migrations/018_gc_exact_p_candidate_identity.cql

green() { printf '\033[32m%s\033[0m\n' "$*"; }
red()   { printf '\033[31m%s\033[0m\n' "$*" >&2; }

BACKUPS=()
restore() {
  local f
  for f in "${BACKUPS[@]:-}"; do
    if [ -n "$f" ] && [ -f "$f.p4abak" ]; then mv -f "$f.p4abak" "$f"; fi
  done
  BACKUPS=()
}
fail() { red "FAILED: $*"; restore; exit 1; }
trap restore EXIT INT TERM

# mutate <file> <perl-expr> — applies it and PROVES it applied.
mutate() {
  local f="$1" expr="$2"
  cp "$f" "$f.p4abak"
  BACKUPS+=("$f")
  perl -0pi -e "$expr" "$f"
  if cmp -s "$f" "$f.p4abak"; then
    fail "mutation did not apply to $f — the pattern matched nothing, so the run below would prove nothing"
  fi
}

# expect_red <test-regex> <assertion-substring> <description>
expect_red() {
  local pattern="$1" needle="$2" what="$3" out status
  # internal/db carries the migration schema guard; internal/gc carries the rest.
  out="$(go test ./internal/gc/ ./internal/db/ -count=1 -run "$pattern" 2>&1)"
  status=$?
  if [ $status -eq 0 ]; then
    printf '%s\n' "$out" | tail -15 >&2
    fail "$what: the suite stayed GREEN with the invariant removed; that gate is not load-bearing"
  fi
  if ! printf '%s\n' "$out" | grep -qF "$needle"; then
    printf '%s\n' "$out" | tail -25 >&2
    fail "$what: the suite failed, but NOT with the expected P4a assertion (looked for: $needle) — it failed for another reason and proves nothing"
  fi
  green "  RED as required: $what"
}

# --- mutations ---------------------------------------------------------------------

m_claim_drops_storage_key() {
  mutate "$STORE" 's{IF storage_class = \? AND storage_key = \?(\s+)AND gc_state = null}{IF storage_class = ?$1AND gc_state = null}'
  expect_red 'TestP4ADestructiveMutationsNameTheExactIncarnation' 'must condition on "storage_key = ?"' \
    'claim CAS without storage_key (a re-mint keeps the class, so only the key separates two lives)'
  restore
}

m_claim_drops_storage_class() {
  mutate "$STORE" 's{IF storage_class = \? AND storage_key = \?(\s+)AND gc_state = null}{IF storage_key = ?$1AND gc_state = null}'
  expect_red 'TestP4ADestructiveMutationsNameTheExactIncarnation' 'must condition on "storage_class = ?"' \
    'claim CAS without storage_class'
  restore
}

m_claim_allows_existing_owner() {
  mutate "$STORE" 's{AND gc_state = null AND gc_claim_id = null AND gc_claimed_at = null}{AND gc_state != ?}'
  expect_red 'TestP4ADestructiveMutationsNameTheExactIncarnation' 'must condition on "gc_state = null"' \
    'claim over an existing owner (the old IF gc_state != deleting, which overwrites repairing_stub)'
  restore
}

m_release_drops_claimed_at() {
  mutate "$STORE" 's{UPDATE blocks SET gc_state = null, gc_claim_id = null, gc_claimed_at = null(\s+)WHERE org_id = \? AND block_id = \?(\s+)IF gc_state = \? AND gc_claim_id = \? AND gc_claimed_at = \?}{UPDATE blocks SET gc_state = null, gc_claim_id = null, gc_claimed_at = null$1WHERE org_id = ? AND block_id = ?$2IF gc_state = ? AND gc_claim_id = ?}'
  expect_red 'TestP4ADestructiveMutationsNameTheExactIncarnation' 'must condition on "gc_claimed_at = ?"' \
    'release without gc_claimed_at (an attempt that lost the row to a takeover could drop the new owner fence)'
  restore
}

m_finalize_drops_claimed_at() {
  mutate "$STORE" 's{DELETE FROM blocks WHERE org_id = \? AND block_id = \?(\s+)IF gc_state = \? AND gc_claim_id = \? AND gc_claimed_at = \?}{DELETE FROM blocks WHERE org_id = ? AND block_id = ?$1IF gc_state = ? AND gc_claim_id = ?}'
  expect_red 'TestP4ADestructiveMutationsNameTheExactIncarnation' 'must condition on "gc_claimed_at = ?"' \
    'finalize without gc_claimed_at (a re-taken claim can reuse an id; the timestamp separates attempts)'
  restore
}

m_candidate_delete_unconditional() {
  mutate "$STORE" 's{DELETE FROM gc_block_candidates WHERE org_id = \? AND block_id = \? AND storage_class = \? AND storage_key = \?(\s+)IF candidate_at = \?}{DELETE FROM gc_block_candidates WHERE org_id = ? AND block_id = ? AND storage_class = ? AND storage_key = ?}'
  expect_red 'TestP4ADestructiveMutationsNameTheExactIncarnation' 'has no IF clause' \
    'unconditional candidate delete (a late P1 lifecycle erases P2 work item)'
  restore
}

# --- R26: exact-P identity across the durable surfaces ------------------------
#
# P4a bound destructive AUTHORITY to an exact incarnation. R26 makes the exact
# incarnation part of the durable IDENTITY of the candidate, its discovery row,
# the queue row and the pending row. That invariant lives in six primary keys and
# in the Go code that names them; none of it is protected by the type system, and
# dropping any one column keeps compiling. Each mutation below removes exactly one
# of those columns and requires the R26 gates to go red.

m_candidate_delete_drops_p_from_key() {
  mutate "$STORE" 's{DELETE FROM gc_block_candidates WHERE org_id = \? AND block_id = \? AND storage_class = \? AND storage_key = \?(\s+)IF candidate_at = \?}{DELETE FROM gc_block_candidates WHERE org_id = ? AND block_id = ?$1IF candidate_at = ?}'
  expect_red 'TestP4ADestructiveMutationsNameTheExactIncarnation' 'must name' \
    'candidate delete keyed on the logical block (a P1 lifecycle consumes P2 candidate)'
  restore
}

m_candidate_projection_delete_drops_p() {
  mutate "$STORE" 's{DELETE FROM gc_block_candidates_by_day(\s+)WHERE candidate_day = \? AND bucket = \? AND candidate_at = \? AND org_id = \? AND block_id = \? AND storage_class = \? AND storage_key = \?}{DELETE FROM gc_block_candidates_by_day$1WHERE candidate_day = ? AND bucket = ? AND candidate_at = ? AND org_id = ? AND block_id = ?}'
  expect_red 'TestR26MutationsNameTheExactIdentity' 'does not name' \
    'discovery delete keyed without P (a stale P1 cleanup erases P2 discoverability, R26)'
  restore
}

m_stale_discovery_is_not_retired() {
  mutate "$WORKER" 's{w\.store\.DeleteBlockGCCandidateDiscovery\(item\.OrgID, item\.ItemID, item\.BlockGCCandidateIdentity\)}{error\(nil\)}'
  expect_red 'TestR26_StaleDiscoveryNoOpRetiresItsOwnRowInsteadOfLoopingForever' 'discovery rows after the no-op' \
    'stale discovery row left standing (the work item is rebuilt on every scan, forever)'
  restore
}

m_settlement_skips_projection_when_cas_missed() {
  mutate "$STORE" 's{(if !applied \{\s+log\.Printf\("\[GC\] block candidate for org=%s block=%s %s at %s was not deleted[^\n]*\n\t\})}{$1\n\tif !applied \{\n\t\treturn nil\n\t\}}'
  expect_red 'TestR26CandidateSettlementAlwaysRetiresItsDiscoveryRow' 'returns early' \
    'settlement leaves the projection when the canonical CAS misses (no exit from rediscovery)'
  restore
}

m_dlq_selector_drops_identity_at() {
  mutate "$STORE" 's{WHERE org_id = \? AND failed_at = \? AND item_type = \? AND item_id = \? AND candidate_storage_class = \? AND candidate_storage_key = \? AND identity_at = \?(\s+)`, orgID\.String\(\), failedAt, string\(itemType\), itemID, identity\.Target\(\)\.StorageClass, identity\.Target\(\)\.StorageKey, identity\.IdentityAt\)\.(\s+)WithContext}{WHERE org_id = ? AND failed_at = ? AND item_type = ? AND item_id = ? AND candidate_storage_class = ? AND candidate_storage_key = ? LIMIT 1$1`, orgID.String(), failedAt, string(itemType), itemID, identity.Target().StorageClass, identity.Target().StorageKey).$2WithContext}'
  expect_red 'TestR26SingleRowReadsNameTheExactIdentity' 'without selecting on' \
    'DLQ selector ignores identity_at (an admin delete hits a different lifecycle than the one on screen)'
  restore
}

m_queue_complete_drops_p() {
  mutate "$STORE" 's{DELETE FROM gc_queue(\s+)WHERE org_id = \? AND bucket = \? AND queued_at = \? AND item_type = \? AND item_id = \? AND candidate_storage_class = \? AND candidate_storage_key = \? AND identity_at = \?(\s+)`, orgID\.String\(\), gcQueueBucket\(orgID, itemType, itemID\), queuedAt, string\(itemType\), itemID, identity\.Target\(\)\.StorageClass, identity\.Target\(\)\.StorageKey, identity\.IdentityAt\)}{DELETE FROM gc_queue$1WHERE org_id = ? AND bucket = ? AND queued_at = ? AND item_type = ? AND item_id = ? AND identity_at = ?$2`, orgID.String(), gcQueueBucket(orgID, itemType, itemID), queuedAt, string(itemType), itemID, identity.IdentityAt)}'
  expect_red 'TestR26MutationsNameTheExactIdentity' 'does not name' \
    'queue completion keyed without P (completing P1 removes P2 work item)'
  restore
}

# The migration's PRIMARY KEYs are the one part of R26 that no Go-level gate can
# see: dropping P from a key changes no runtime CQL, so every source guard and
# every mutation above stays green while a freshly migrated keyspace collapses P1
# and P2 back into one row. These two prove the schema guard is load-bearing.

m_migration_queue_key_drops_p() {
  mutate "$MIGRATION" 's{PRIMARY KEY \(\(org_id, bucket\), queued_at, item_type, item_id, candidate_storage_class, candidate_storage_key, identity_at\)}{PRIMARY KEY ((org_id, bucket), queued_at, item_type, item_id, identity_at)}'
  expect_red 'TestR26MigrationDeclaresTheExactIdentityKeys' 'must END with'     'migration drops P from the gc_queue key (two lives of one block share a queue row again)'
  restore
}

m_migration_candidate_key_adds_candidate_at() {
  mutate "$MIGRATION" 's{PRIMARY KEY \(\(org_id, block_id\), storage_class, storage_key\)}{PRIMARY KEY ((org_id, block_id), storage_class, storage_key, candidate_at)}'
  expect_red 'TestR26MigrationKeepsCandidateAtOutOfTheCandidateKey' 'includes candidate_at'     'candidate_at promoted into the candidate key (every re-decision becomes another row; settling one strands the rest)'
  restore
}

m_stale_discovery_failure_burns_a_retry() {
  mutate "$WORKER" 's{return failedClosedError\{Reason: "failed to clear stale block GC candidate discovery row", ItemID: item\.ItemID, Err: err\}}{return w.failClosedIfUnavailable("failed to clear stale block GC candidate discovery row", item.ItemID, err)}'
  expect_red 'TestR26_UnretireableDiscoveryRowPostponesInsteadOfCompleting' 'want 0: a failed discovery cleanup must POSTPONE'     'discovery cleanup failure spends a retry (five of them park an ItemBlock in a DLQ it never leaves, and the row it could not retire re-enqueues the same item after expiry)'
  restore
}

m_enqueue_item_mints_block_candidate() {
  mutate "$STORE" 's{\tif itemType == ItemBlock \{\n\t\treturn fmt\.Errorf\("item type %s requires an exact block GC candidate identity; use EnqueueBatch", itemType\)\n\t\}\n}{}'
  expect_red 'TestEnqueueItemRefusesBlockItems' 'reached the database' \
    'raw enqueue accepts ItemBlock again (enqueue fabricates destructive authority with no zero-ref decision)'
  restore
}

m_settlement_read_is_ordinary() {
  mutate "$STORE" 's{Consistency\(gocql\.Serial\)\.(\s+)Scan\(&storageClass, &storageKey, &gcState}{Scan(&storageClass, &storageKey, &gcState}'
  expect_red 'TestP4ASettlementReadsUseTheSerialDomain' 'Consistency(gocql.Serial)' \
    'settling read downgraded to ordinary consistency (a false absent consumes the candidate, R20)'
  restore
}

m_candidate_drops_storage_key() {
  mutate "$STORE" 's{INSERT INTO gc_block_candidates \(org_id, block_id, storage_class, storage_key, candidate_at\)(\s+)VALUES \(\?, \?, \?, \?, \?\) IF NOT EXISTS}{INSERT INTO gc_block_candidates (org_id, block_id, storage_class, candidate_at)$1VALUES (?, ?, ?, ?) IF NOT EXISTS}'
  expect_red 'TestP4ACandidatesCarryTheExactKeyAndNeverDeriveIt' 'must carry storage_key' \
    'candidate persisted without storage_key (nothing carries P1 to the claim)'
  restore
}

m_claim_id_from_candidate_at() {
  mutate "$WORKER" 's{ClaimID:   uuid\.NewString\(\),}{ClaimID:   candidate.CandidateAt.UTC().Format(time.RFC3339Nano),}'
  expect_red 'TestP4AClaimIDIsNeverDerivedFromCandidateAt|TestP4A_EachWorkerAttemptMintsItsOwnClaimID' 'ownership is not per-attempt' \
    'claim id derived from candidate_at (two concurrent attempts share ownership)'
  restore
}

m_fresh_owner_completes_candidate() {
  mutate "$WORKER" 's{\tcase BlockClaimFreshOwner:}{\tcase BlockClaimFreshOwner:\n\t\tif err := w.settleBlockCandidate(item, candidate); err != nil {\n\t\t\treturn err\n\t\t}\n\t\treturn nil\n\tcase BlockClaimOutcome(-1):}'
  expect_red 'TestP4A_FreshOwnerDoesNotSettleTheCandidate' 'a live owner is not completion' \
    'fresh owner treated as completion (consumes the only item able to lift the fence, R16)'
  restore
}

m_destructive_locator_from_readback() {
  mutate "$WORKER" 's{storageClass := attempt\.Target\.StorageClass(\s+)storageKey := attempt\.Target\.StorageKey}{storageClass := blockInfo.StorageClass$1storageKey := blockInfo.StorageKey}'
  expect_red 'TestP4ADestructiveStepsUseTheClaimsLocator' 'must take storageClass and storageKey from attempt.Target' \
    'orphan and S3 delete taking their locator from a post-claim re-read (publishes and destroys whichever incarnation that ordinary read happens to show)'
  restore
}

m_takeover_rereads_the_row() {
  # Type-correct on purpose: a mutation that fails to COMPILE proves nothing about the
  # guard it was aimed at, so this restores the old behaviour in a form the compiler
  # accepts and lets the guard be the thing that objects.
  mutate "$WORKER" 's{outcome, err := w\.store\.ReleaseBlockClaim\(item\.OrgID, item\.ItemID, observed\)}{staleOutcome, err := w.store.ReleaseStaleBlockClaim(item.OrgID, item.ItemID, observed.Target, observed.ClaimedAt.Add(-blockDeleteClaimStaleAfter))
	outcome := BlockReleaseNotOwner
	if staleOutcome == BlockClaimReleased {
		outcome = BlockReleaseReleased
	}}'
  expect_red 'TestP4AStaleTakeoverUsesTheObservedAuthority' 'must not call ReleaseStaleBlockClaim' \
    'takeover re-reading the row instead of CASing against the authority it observed'
  restore
}

m_stale_release_ignores_incarnation() {
  # BOTH implementations, and that is worth stating rather than hiding. The unit suite
  # drives MockStore, so mutating store_cassandra.go alone leaves it green — the
  # incarnation check lives in two mirrored places and neither copy is protected by the
  # other. The production copy is separately pinned by
  # TestP4AStaleClaimReleaseNamesAnIncarnation and by the real-Cassandra leg in
  # internal/integration/p4a_claim_authority_test.go.
  mutate "$STORE" 's~\tif row\.Target != expectedTarget \{~\tif false \&\& row.Target != expectedTarget \{~'
  mutate "$MOCK" 's~!= expectedTarget \{~!= expectedTarget \&\& false \{~'
  expect_red 'TestP4A_StaleClaimReleaseIsBoundToTheCandidatesIncarnation' 'released a fence belonging to P2' \
    'age-based release ignoring the incarnation (a candidate for P1 hands back P2 fence)'
  restore
}

m_candidate_capture_not_serial() {
  mutate "$STORE" 's{\t\tConsistency\(gocql\.Serial\)\.(\s+)Scan\(&storageClass, &storageKey\)}{\t\tScan(&storageClass, \&storageKey)}'
  expect_red 'TestP4ACandidateCaptureUsesTheSerialDomain' 'must read at Consistency(gocql.Serial)' \
    'candidate capture at session consistency (a lagging read replaces a correct candidate with a dead incarnation)'
  restore
}

m_candidate_loop_unbounded() {
  mutate "$STORE" 's~for attempt := 0; attempt < ensureBlockGCCandidateMaxAttempts; attempt\+\+ \{~for \{~'
  expect_red 'TestP4ACandidateRetryLoopIsBounded' 'must be bounded' \
    'unbounded candidate CAS retry loop (a non-converging condition becomes a silent Paxos hot loop)'
  restore
}

m_enqueue_sentinel_is_fatal() {
  mutate "$WORKER" 's~if errors\.Is\(candidateErr, ErrBlockCandidateTargetUnavailable\) \{~if false \&\& errors.Is(candidateErr, ErrBlockCandidateTargetUnavailable) \{~'
  expect_red 'TestP4A_MissingCanonicalRowDoesNotAbortTheEnqueueBatch' 'aborted on a block with nothing reclaimable' \
    'a block with nothing reclaimable aborting the whole enqueue batch (self-poisoning on the fs_object path)'
  restore
}

m_grace_ignores_candidate_at() {
  mutate "$WORKER" 's~if w\.gracePeriod > 0 \&\& candidate\.CandidateAt\.After\(w\.clock\(\)\.Add\(-w\.gracePeriod\)\) \{~if false \{~'
  expect_red 'TestP4A_ReplacedCandidateServesItsOwnGracePeriod' 'inside its grace period' \
    'grace measured only on the queue row (a replaced candidate skips the grace its replacement bought it)'
  restore
}

m_late_loser_settles_candidate() {
  mutate "$WORKER" 's{released != BlockReleaseReleased}{released == BlockReleaseReleased && false}'
  expect_red 'TestP4A_LateLoserCannotConsumeTheCurrentOwnersCandidate' 'a late loser consumed the candidate while another attempt owned the fence' \
    'late loser settles the candidate after a not-owner release (a fence with no work item able to take it over, R16 from the other side)'
  restore
}

m_unreliable_read_skips_release() {
  mutate "$WORKER" 's#\.WithLabelValues\("block_canonical_read_unreliable"\)\.Inc\(\)(\s+)if _, relErr := w\.releaseBlockClaim\(item\.OrgID, item\.ItemID, attempt\); relErr != nil \{(\s+)return relErr(\s+)\}#.WithLabelValues("block_canonical_read_unreliable").Inc()#'
  expect_red 'TestP4A_StalePostClaimReadHandsTheFenceBackAndPostpones' 'fence left standing' \
    'stale post-claim read postpones WITHOUT handing the fence back (a lagging replica becomes a permanent upload refusal)'
  restore
}

m_post_claim_read_error_skips_release() {
  mutate "$WORKER" 's{return w\.releaseAndPostponeUnreliableRead\(item, attempt, fmt\.Sprintf\("failed to load re-referenced block info: %v", infoErr\)\)}{return w.failClosedIfUnavailable("failed to load re-referenced block info", item.ItemID, infoErr)}; s{return w\.releaseAndPostponeUnreliableRead\(item, attempt, fmt\.Sprintf\("failed to load canonical block info: %v", err\)\)}{return w.failClosedIfUnavailable("failed to load canonical block info", item.ItemID, err)}'
  expect_red 'TestP4A_PostClaimReadErrorHandsTheFenceBackAndPostpones' 'fence left standing' \
    'a failed post-claim canonical read returns without handing its fence back'
  restore
}

m_divergent_read_retries() {
  mutate "$WORKER" 's{return w\.releaseAndPostponeUnreliableRead\(item, attempt, fmt\.Sprintf\("canonical row reads back as %s but claim authorized %s", observed, attempt\.Target\)\)}{if _, relErr := w.releaseBlockClaim(item.OrgID, item.ItemID, attempt); relErr != nil {\n\t\t\treturn relErr\n\t\t}\n\t\treturn fmt.Errorf("block %s: the canonical row reads back as %s but the claim authorized %s; refusing to publish or delete either", item.ItemID, observed, attempt.Target)}'
  expect_red 'TestP4A_DivergentPostClaimReadHandsTheFenceBackAndPostpones' 'items in the DLQ' \
    'a divergent post-claim canonical read spends retries and moves its recovery candidate to the DLQ'
  restore
}

m_authority_invalid_retries() {
  mutate "$WORKER" 's{GCFailureCodeBlockAuthorityInvalid,(\s+)GCFailureCodeBlockClaimForeignOwner,}{GCFailureCodeBlockClaimForeignOwner,}'
  expect_red 'TestP4A_LateLoserPostponesInsteadOfSpendingTheRetryBudget' 'is documented as postponing but spends the retry budget' \
    'block_authority_invalid dropped from the postpone list (it retries into a DLQ ItemBlock never leaves, against its own contract)'
  restore
}

m_foreign_owner_retries() {
  mutate "$WORKER" 's{GCFailureCodeBlockClaimForeignOwner,(\s+)GCFailureCodeBlockCandidateWithinGrace,}{GCFailureCodeBlockCandidateWithinGrace,}'
  expect_red 'TestP4A_LateLoserPostponesInsteadOfSpendingTheRetryBudget' 'a late loser must postpone' \
    'late loser spends the retry budget (parks the item in the DLQ, making the preserved candidate unreachable anyway)'
  restore
}

m_candidate_authority_read_is_ordinary() {
  mutate "$STORE" 's{Consistency\(gocql\.Serial\)\.(\s+)Scan\(&candidateAt\)}{Scan(&candidateAt)}'
  expect_red 'TestR26CandidateAuthorityReadUsesTheSerialDomain' 'must read at Consistency(gocql.Serial)' \
    'candidate authority read downgraded to ordinary consistency (a false absent retires discovery, queue and pending, stranding a live candidate)'
  restore
}

m_expiry_bucket_hashes_raw_timestamps() {
  mutate "$PROJECTIONS" 's{cassandraTimestamp\((failedAt|identityAt)\)\.Format}{$1.UTC().Format}g'
  expect_red 'TestGCFailedItemExpiryBucketIsStableAcrossCassandraPrecision' 'once Cassandra has truncated it' \
    'expiry bucket hashed at the caller precision (the INSERT lands in one partition and every DELETE names another, so the expiry row outlives its own deletion)'
  restore
}

MUTATIONS=(
  m_claim_drops_storage_key
  m_claim_drops_storage_class
  m_claim_allows_existing_owner
  m_release_drops_claimed_at
  m_finalize_drops_claimed_at
  m_candidate_delete_unconditional
  m_candidate_delete_drops_p_from_key
  m_candidate_projection_delete_drops_p
  m_stale_discovery_is_not_retired
  m_settlement_skips_projection_when_cas_missed
  m_dlq_selector_drops_identity_at
  m_queue_complete_drops_p
  m_enqueue_item_mints_block_candidate
  m_migration_queue_key_drops_p
  m_migration_candidate_key_adds_candidate_at
  m_stale_discovery_failure_burns_a_retry
  m_settlement_read_is_ordinary
  m_candidate_authority_read_is_ordinary
  m_expiry_bucket_hashes_raw_timestamps
  m_candidate_drops_storage_key
  m_claim_id_from_candidate_at
  m_fresh_owner_completes_candidate
  m_destructive_locator_from_readback
  m_takeover_rereads_the_row
  m_stale_release_ignores_incarnation
  m_candidate_capture_not_serial
  m_candidate_loop_unbounded
  m_enqueue_sentinel_is_fatal
  m_grace_ignores_candidate_at
  m_late_loser_settles_candidate
  m_unreliable_read_skips_release
  m_post_claim_read_error_skips_release
  m_divergent_read_retries
  m_authority_invalid_retries
  m_foreign_owner_retries
)

if [ "${1:-}" = "--list" ]; then
  printf '%s\n' "${MUTATIONS[@]}"
  exit 0
fi

printf 'Baseline (unmutated) must be green...\n'
if ! go test ./internal/gc/ ./internal/db/ -count=1 >/dev/null 2>&1; then
  fail 'the unmutated tree is already red; fix that before running mutations'
fi
green '  baseline green'

if [ $# -gt 0 ]; then
  MUTATIONS=("$1")
fi

for m in "${MUTATIONS[@]}"; do
  printf '\n%s\n' "$m"
  "$m"
done

restore
printf '\n'
green "All ${#MUTATIONS[@]} P4a mutation(s) produced the expected red."
printf 'Each removed invariant was detected by a P4a assertion, not by an unrelated failure.\n'
