#!/usr/bin/env bash
# X1 strict-nonoverlap characterization mutations. Production sources are copied
# to an isolated fixture; the working tree is never patched.

set -uo pipefail
cd "$(dirname "$0")/.."

if ! command -v go >/dev/null 2>&1; then
  if command -v go.exe >/dev/null 2>&1; then
    go() { command go.exe "$@"; }
  else
    export PATH="/usr/local/go/bin:/c/Program Files/Go/bin:${PATH:-}"
  fi
fi

green() { printf '\033[32m%s\033[0m\n' "$*"; }
red() { printf '\033[31m%s\033[0m\n' "$*" >&2; }
fail() { red "FAILED: $*"; exit 1; }

if ! command -v perl >/dev/null 2>&1; then
  fail "perl is required for isolated fixture mutations"
fi

FIXTURE=""
reset_fixture() {
  if [ -n "$FIXTURE" ] && [ -d "$FIXTURE" ]; then rm -rf "$FIXTURE"; fi
  FIXTURE="$(mktemp -d)"
  mkdir -p "$FIXTURE/internal/integration" "$FIXTURE/internal/gc"
  cp internal/integration/x1_strict_nonoverlap_*.go "$FIXTURE/internal/integration/"
  cp internal/gc/store_cassandra.go "$FIXTURE/internal/gc/"
}
trap 'if [ -n "$FIXTURE" ] && [ -d "$FIXTURE" ]; then rm -rf "$FIXTURE"; fi' EXIT INT TERM

mutate() {
  local file="$1" expression="$2" before
  before="$(mktemp)"
  cp "$file" "$before"
  perl -0pi -e "$expression" "$file"
  if cmp -s "$file" "$before"; then
    rm -f "$before"
    fail "mutation did not apply to $file"
  fi
  rm -f "$before"
}

x1_source_root() {
  if command -v cygpath >/dev/null 2>&1; then
    cygpath -w "$FIXTURE"
  else
    printf '%s' "$FIXTURE"
  fi
}

expect_red() {
  local pattern="$1" needle="$2" description="$3" out status root
  root="$(x1_source_root)"
  out="$(export X1_SOURCE_ROOT="$root"; go test ./internal/gc -count=1 -run "$pattern" 2>&1)"
  status=$?
  if [ "$status" -eq 0 ]; then
    printf 'X1_SOURCE_ROOT=%s\n' "$root" >&2
    printf '%s\n' "$out" | tail -20 >&2
    fail "$description: suite stayed green"
  fi
  if ! printf '%s\n' "$out" | grep -qF "$needle"; then
    printf 'X1_SOURCE_ROOT=%s\n' "$root" >&2
    printf '%s\n' "$out" | tail -30 >&2
    fail "$description: failed without targeted assertion: $needle"
  fi
  green "  RED as required: $description"
}

m_finalize_before_delete() {
  reset_fixture
  local file="$FIXTURE/internal/integration/x1_strict_nonoverlap_characterization_test.go"
  mutate "$file" 's/x1Attempt\(target, "F1"\)\)\r?\n([ \t]+)if err := blockStore.DeleteBlockByStorageKey/x1Attempt(target, "F1"))\n${1}_, _ = store.FinalizeBlockDelete(orgID, blockID, gcpkg.CommittedBlockDeleteAuthorityForTest(authority))\n${1}if err := blockStore.DeleteBlockByStorageKey/'
  expect_red '^TestX1CandidateHarnessDeletesBeforeFinalize$' \
    'postDeleteCrash must call DeleteBlockByStorageKey before FinalizeBlockDelete' 'finalize before physical delete'
}

m_f2_finalize_before_delete() {
  reset_fixture
  local file="$FIXTURE/internal/integration/x1_strict_nonoverlap_characterization_test.go"
  mutate "$file" 's/x1Attempt\(target, "F2s"\)\)\r?\n([ \t]+)if err := blockStore.DeleteBlockByStorageKey/x1Attempt(target, "F2s"))\n${1}_, _ = store.FinalizeBlockDelete(orgID, blockID, gcpkg.CommittedBlockDeleteAuthorityForTest(authority))\n${1}if err := blockStore.DeleteBlockByStorageKey/'
  expect_red '^TestX1CandidateHarnessDeletesBeforeFinalize$' \
    'ambiguousFinalizeSafety must call DeleteBlockByStorageKey before FinalizeBlockDelete' 'F2-safety finalize before physical delete'
}

m_f0b1_ignores_pending() {
  reset_fixture
  local file="$FIXTURE/internal/integration/x1_strict_nonoverlap_characterization_test.go"
  mutate "$file" 's/PendingItemExists/QueueItemExists/g'
  expect_red '^TestX1F0b1LocksOnPendingItemsNotQueue$' \
    'gc_pending_items via PendingItemExists' 'F0b1 watches queue instead of pending'
}

m_h_drops_put_wrapper() {
  reset_fixture
  local file="$FIXTURE/internal/integration/x1_strict_nonoverlap_characterization_test.go"
  mutate "$file" 's/PutBlockMaterializationTarget/putBlockMaterializationTargetOmitted/g'
  expect_red '^TestX1HUsesExportedPutCallback$' \
    'must call PutBlockMaterializationTarget' 'H no longer uses the exported put callback'
}

m_f2_collapses_safety_and_convergence() {
  reset_fixture
  local file="$FIXTURE/internal/integration/x1_strict_nonoverlap_characterization_test.go"
  mutate "$file" 's/x1NonoverlapEvidence.ambiguousFinalizeConvergence = true/x1NonoverlapEvidence.ambiguousFinalizeSafety = true/'
  expect_red '^TestX1F2SafetyAndConvergenceAreSeparateAssignments$' \
    'ambiguousFinalizeConvergence by name' 'F2-convergence reuses the safety flag'
}

m_evidence_drops_named_leg() {
  reset_fixture
  local file="$FIXTURE/internal/integration/x1_strict_nonoverlap_evidence_test.go"
  mutate "$file" 's/"borrowedFSPublish"/"borrowed"/g'
  expect_red '^TestX1EvidenceMissingListsEveryNamedLeg$' \
    'missing() must name "borrowedFSPublish" individually' 'completeness drops a named leg'
}

m_lookback_shrinks() {
  reset_fixture
  local file="$FIXTURE/internal/gc/store_cassandra.go"
  mutate "$file" 's/gcInitialScanLookbackDays             = 7/gcInitialScanLookbackDays             = 1/'
  expect_red '^TestX1ScannerLookbackSource$' \
    'gcInitialScanLookbackDays             = 7' 'scanner lookback constant changed'
}

m_worker_driven_harness() {
  reset_fixture
  local file="$FIXTURE/internal/integration/x1_strict_nonoverlap_characterization_test.go"
  mutate "$file" 's/(func TestX1StrictNonoverlapCharacterization\([^\)]*\) \{)/${1}\n\t_ = worker.processBlock/'
  expect_red '^TestX1HarnesMustNotCallProcessBlock$' \
    'must not drive worker.go processBlock' 'harness starts calling processBlock'
}

m_orphan_on_candidate() {
  reset_fixture
  local file="$FIXTURE/internal/integration/x1_strict_nonoverlap_characterization_test.go"
  mutate "$file" 's/x1NonoverlapEvidence.writerFirst = true/_ = store.StartBlockDeleteOrphan(orgID, blockID, gcpkg.CommittedBlockDeleteAuthorityForTest(x1Attempt(target, "mut")), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", time.Now())\n\t\tx1NonoverlapEvidence.writerFirst = true/'
  expect_red '^TestX1CandidateHarnessDoesNotPublishOrphan$' \
    'must not call StartBlockDeleteOrphan' 'candidate harness publishes orphan'
}

m_f0b1_uses_failitem() {
  reset_fixture
  local file="$FIXTURE/internal/integration/x1_strict_nonoverlap_characterization_test.go"
  mutate "$file" 's/x1DeleteQueueRowKeepPending\(t, database, orgID, blockID, candidate\)/x1DeleteQueueRowKeepPending(t, database, orgID, blockID, candidate)\n\t\t_ = store.FailItem(gcpkg.QueueItem{})/'
  expect_red '^TestX1F0b1LocksOnPendingItemsNotQueue$' \
    'must not use FailItem' 'F0b1 reconstructs queue-loss via FailItem/DLQ'
}

m_h_drops_resurrection_log() {
  reset_fixture
  local file="$FIXTURE/internal/integration/x1_strict_nonoverlap_characterization_test.go"
  mutate "$file" 's/t\.Fatal\("H: expected residual authorized PUT to resurrect K1 on characterization baseline"\)/t.Log("H: expected residual authorized PUT to resurrect K1 on characterization baseline")/'
  expect_red '^TestX1HUsesExportedPutCallback$' \
    'H must fail the characterization baseline if K1 is not resurrected' 'H no longer fails closed when K1 is not resurrected'
}

m_e_allows_p2_while_p1() {
  reset_fixture
  local file="$FIXTURE/internal/integration/x1_strict_nonoverlap_characterization_test.go"
  mutate "$file" 's/t\.Fatal\("E: P2 must not acquire canonical authority while P1 remains"\)/t.Log("E: P2 must not acquire canonical authority while P1 remains")/'
  expect_red '^TestX1EAttemptsFailedPhysicalDelete$' \
    'E must refuse P2 install while P1 remains' 'E no longer refuses P2 while P1 remains'
}

MUTATIONS=(
  m_finalize_before_delete
  m_f2_finalize_before_delete
  m_f0b1_ignores_pending
  m_h_drops_put_wrapper
  m_f2_collapses_safety_and_convergence
  m_evidence_drops_named_leg
  m_lookback_shrinks
  m_worker_driven_harness
  m_orphan_on_candidate
  m_f0b1_uses_failitem
  m_h_drops_resurrection_log
  m_e_allows_p2_while_p1
)

if [ "${1:-}" = "--list" ]; then
  printf '%s\n' "${MUTATIONS[@]}"
  exit 0
fi

green "X1 non-overlap mutation validation"
for mutation in "${MUTATIONS[@]}"; do
  green "→ $mutation"
  "$mutation"
done
green "all X1 non-overlap mutations stayed RED"
