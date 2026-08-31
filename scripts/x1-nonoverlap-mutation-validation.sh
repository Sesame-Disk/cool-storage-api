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
    'DeleteBlockByStorageKey before FinalizeBlockDelete' 'finalize before physical delete'
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

MUTATIONS=(
  m_finalize_before_delete
  m_f0b1_ignores_pending
  m_h_drops_put_wrapper
  m_f2_collapses_safety_and_convergence
  m_evidence_drops_named_leg
  m_lookback_shrinks
  m_worker_driven_harness
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
