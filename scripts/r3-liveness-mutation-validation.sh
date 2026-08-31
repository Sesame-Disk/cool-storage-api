#!/usr/bin/env bash
# R3 characterization mutation evidence. Production sources are copied to an
# isolated fixture and parsed by source-contract tests; the working tree is
# never patched by this script.

set -uo pipefail
cd "$(dirname "$0")/.."

green() { printf '\033[32m%s\033[0m\n' "$*"; }
red() { printf '\033[31m%s\033[0m\n' "$*" >&2; }
fail() { red "FAILED: $*"; exit 1; }

FIXTURE=""
reset_fixture() {
  if [ -n "$FIXTURE" ] && [ -d "$FIXTURE" ]; then rm -rf "$FIXTURE"; fi
  FIXTURE="$(mktemp -d)"
  mkdir -p "$FIXTURE/internal/api/v2" "$FIXTURE/internal/api" "$FIXTURE/internal/db"
  cp internal/api/v2/*.go "$FIXTURE/internal/api/v2/"
  cp internal/api/*.go "$FIXTURE/internal/api/"
  cp internal/db/*.go "$FIXTURE/internal/db/"
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

expect_red() {
  local package="$1" pattern="$2" needle="$3" description="$4" out status
  out="$(R3_SOURCE_ROOT="$FIXTURE" go test "$package" -count=1 -run "$pattern" 2>&1)"
  status=$?
  if [ $status -eq 0 ]; then
    printf '%s\n' "$out" | tail -20 >&2
    fail "$description: suite stayed green"
  fi
  if ! printf '%s\n' "$out" | grep -qF "$needle"; then
    printf '%s\n' "$out" | tail -30 >&2
    fail "$description: failed without targeted assertion: $needle"
  fi
  green "  RED as required: $description"
}

m_fence_before_up() {
  reset_fixture
  local file="$FIXTURE/internal/api/v2/fs_helpers.go"
  mutate "$file" 's@(\tif err := registerUploadedBlockAddProvisionalRefFn\(h, orgID, blockID, referrer, libraryID, target\.StorageClass, expiresAt\); err != nil \{)@\t_, _ = registerUploadedBlockFenceActiveFn(h, orgID, blockID)\n$1@'
  expect_red ./internal/api/v2 '^TestR3RegisterUploadedBlockTargetPinsBeforeAuthority$' \
    'provisional up must precede post-pin fence' 'fence executes before up'
}

m_no_up() {
  reset_fixture
  mutate "$FIXTURE/internal/api/v2/fs_helpers.go" 's/registerUploadedBlockAddProvisionalRefFn/r3OmittedProvisionalRefFn/g'
  expect_red ./internal/api/v2 '^TestR3RegisterUploadedBlockTargetPinsBeforeAuthority$' \
    'must call registerUploadedBlockAddProvisionalRefFn' 'writer omits up'
}

m_no_fence() {
  reset_fixture
  mutate "$FIXTURE/internal/api/v2/fs_helpers.go" 's/registerUploadedBlockFenceActiveFn/r3OmittedFenceFn/g'
  expect_red ./internal/api/v2 '^TestR3RegisterUploadedBlockTargetPinsBeforeAuthority$' \
    'must call registerUploadedBlockFenceActiveFn' 'writer omits post-pin fence'
}

m_continue_on_fence() {
  reset_fixture
  mutate "$FIXTURE/internal/api/v2/fs_helpers.go" 's/if deleteFenceActive \{/if false \&\& deleteFenceActive {/'
  expect_red ./internal/api/v2 '^TestR3RegisterUploadedBlockTargetRejectsActiveFence$' \
    'an active post-pin fence must return ErrBlockDeleteInProgress' 'writer continues on active fence'
}

m_metadata_before_fence() {
  reset_fixture
  local file="$FIXTURE/internal/api/v2/fs_helpers.go"
  mutate "$file" 's@(\tdeleteFenceActive, err := registerUploadedBlockFenceActiveFn\(h, orgID, blockID\))@\t_ = registerUploadedBlockRepairMetadataFn(h, orgID, libraryID, blockID, sha1ID, sizeBytes, target)\n$1@'
  expect_red ./internal/api/v2 '^TestR3RegisterUploadedBlockTargetPinsBeforeAuthority$' \
    'metadata authority must remain downstream of the post-pin fence' 'metadata executes before fence'
}

m_drop_up_after_success() {
  reset_fixture
  mutate "$FIXTURE/internal/api/v2/fs_helpers.go" 's/(func \(h \*FSHelper\) RegisterUploadedBlockTarget[^\{]+\{)/$1\n\t_ = RemoveBlockReference()/'
  expect_red ./internal/api/v2 '^TestR3RegisterUploadedBlockTargetNeverDropsSuccessfulUploadPin$' \
    'must not remove its provisional up pin via RemoveBlockReference' 'successful materialization drops up'
}

m_authority_read_in_finalize() {
  reset_fixture
  mutate "$FIXTURE/internal/db/block_references.go" 's/(func AddPublishAttemptReferences[^\{]+\{)/$1\n\t_ = ValidateBlockPublishAuthority()/'
  expect_red ./internal/db '^TestR3PublicationHotPathHasNoPerBlockAuthorityReads$' \
    'per-block authority helper ValidateBlockPublishAuthority' 'v2 publication primitive gains an authority read'
}

m_foreign_fs_is_not_session_pin() {
  reset_fixture
  mutate "$FIXTURE/internal/api/v2/file_from_blocks.go" 's/if referrer == sessionReferrer \{/if strings.HasPrefix(referrer, "fs:") {/'
  expect_red ./internal/api/v2 '^TestR3BlockOwnershipProvenanceChecksExactSessionReferrer$' \
    'exact session referrer comparison not found' 'foreign fs mutation removes exact session provenance'
}

m_ownership_extra_db_io() {
  reset_fixture
  mutate "$FIXTURE/internal/api/v2/file_from_blocks.go" 's/return classifyBlockReferrerProvenance\(referrers, referrer\), nil/_ = database.Session().Query("SELECT referrer FROM block_references WHERE org_id = ? AND block_id = ?", orgID, blockID).Exec()\n\treturn classifyBlockReferrerProvenance(referrers, referrer), nil/'
  expect_red ./internal/api/v2 '^TestR3ClassifyBlockOwnershipUsesSingleReferrerPartitionRead$' \
    'unlisted call' 'ownership classifier gains a second Cassandra operation'
}

source scripts/r3-hotpath-extra-mutations.sh

MUTATIONS=(
  m_fence_before_up
  m_no_up
  m_no_fence
  m_continue_on_fence
  m_metadata_before_fence
  m_drop_up_after_success
  m_authority_read_in_finalize
  m_foreign_fs_is_not_session_pin
  m_ownership_extra_db_io
  m_authority_read_hidden_in_add_ref_funcvar
  m_cross_package_authority_wrapper
  m_qualified_inline_authority_select
  m_cross_package_receiver_method
  m_local_function_alias_authority_read
  m_extra_per_block_cql_insert
  m_typed_single_value_bind
  m_local_receiver_method_value_authority_read
  m_duplicate_existing_per_block_publication_io
  m_duplicate_existing_per_normalized_block_io
  m_wrapper_second_stage_in_pending_files_loop
  m_wrapper_second_insert_in_normalized_block_loop
  m_persist_fn_second_publication_sink
  m_funclit_second_publication_sink
  m_v2_post_stage_authority_read_before_head
  m_v2_fshelper_post_stage_authority_read_before_head
  m_v2_session_query_after_stage
  m_v2_dynamic_query_after_stage
  m_v2_const_query_after_stage
  m_v2_dynamic_insert_after_stage
  m_v2_preacquired_query_literal_bind_after_stage
  m_v2_db_read_inside_head_argument
  m_v2_local_db_alias_post_stage_read
  m_v2_local_db_method_value_post_stage_read
  m_sync_post_stage_authority_read_before_head
  m_materialization_post_metadata_authority_read
  m_materialization_local_db_alias_read
)

if [ "${1:-}" = "--list" ]; then
  printf '%s\n' "${MUTATIONS[@]}"
  exit 0
fi

for mutation in "${MUTATIONS[@]}"; do
  green "Running $mutation"
  "$mutation" || fail "$mutation returned non-zero"
done

green "R3 liveness/hot-path mutation evidence passed"
