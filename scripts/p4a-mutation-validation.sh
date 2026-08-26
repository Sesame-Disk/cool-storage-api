#!/usr/bin/env bash
#
# P4a mutation evidence: prove the exact-incarnation, per-attempt claim guards actually
# fail when the invariant they protect is removed.
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
  out="$(go test ./internal/gc/ -count=1 -run "$pattern" 2>&1)"
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
  mutate "$STORE" 's{DELETE FROM gc_block_candidates WHERE org_id = \? AND block_id = \?(\s+)IF candidate_at = \? AND storage_class = \? AND storage_key = \?}{DELETE FROM gc_block_candidates WHERE org_id = ? AND block_id = ?}'
  expect_red 'TestP4ADestructiveMutationsNameTheExactIncarnation' 'has no IF clause' \
    'unconditional candidate delete (a late P1 lifecycle erases P2 work item)'
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
  mutate "$WORKER" 's{\tcase BlockClaimFreshOwner:}{\tcase BlockClaimFreshOwner:\n\t\tif err := w.settleBlockCandidate(item, candidate, candidateFound); err != nil {\n\t\t\treturn err\n\t\t}\n\t\treturn nil\n\tcase BlockClaimOutcome(-1):}'
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

MUTATIONS=(
  m_claim_drops_storage_key
  m_claim_drops_storage_class
  m_claim_allows_existing_owner
  m_release_drops_claimed_at
  m_finalize_drops_claimed_at
  m_candidate_delete_unconditional
  m_settlement_read_is_ordinary
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
)

if [ "${1:-}" = "--list" ]; then
  printf '%s\n' "${MUTATIONS[@]}"
  exit 0
fi

printf 'Baseline (unmutated) must be green...\n'
if ! go test ./internal/gc/ -count=1 >/dev/null 2>&1; then
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
