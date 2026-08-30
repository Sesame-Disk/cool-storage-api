# Extra fail-closed mutations for the R3 publication cost guard. This file is
# sourced by r3-liveness-mutation-validation.sh after its fixture helpers exist.

m_authority_read_hidden_in_add_ref_funcvar() {
  reset_fixture
  mutate "$FIXTURE/internal/db/block_references.go" 's/(var addPublishAttemptReferenceFn = func[^\{]+\{)/$1\n\t_, _ = database.BlockDeleteFenceActive(orgID, blockID)/'
  expect_red ./internal/db '^TestR3PublicationHotPathIsFailClosed$' \
    'per-block authority helper BlockDeleteFenceActive' 'authority read hidden in add-ref function seam'
}

m_cross_package_authority_wrapper() {
  reset_fixture
  mutate "$FIXTURE/internal/db/block_references.go" 's@(// AddPublishAttemptReferences stages)@func R3NeutralCheckedPublish(database *DB) error {\n\t_ = database.Session().Query("SELECT gc_state FROM blocks")\n\treturn nil\n}\n\n$1@'
  mutate "$FIXTURE/internal/api/v2/fs_helpers.go" 's@(var stagePendingPublishedFilesAddReferencesFn = db.AddPublishAttemptReferences)@$1\nvar r3NeutralCheckedPublishFn = db.R3NeutralCheckedPublish@; s@(func \(h \*FSHelper\) stagePendingPublishedFiles\([^\{]+\{)@$1\n\t_ = r3NeutralCheckedPublishFn(h.db)@'
  expect_red ./internal/db '^TestR3PublicationHotPathIsFailClosed$' \
    'submitted canonical/orphan CQL authority read' 'cross-package function seam hides an authority wrapper'
}

m_qualified_inline_authority_select() {
  reset_fixture
  mutate "$FIXTURE/internal/db/block_references.go" 's/(func AddPublishAttemptReferences[^\{]+\{)/$1\n\t_ = database.Session().Query("SELECT gc_state FROM sesamefs.blocks")/'
  expect_red ./internal/db '^TestR3PublicationHotPathIsFailClosed$' \
    'submitted canonical/orphan CQL authority read' 'qualified inline authority SELECT enters publication'
}

m_cross_package_receiver_method() {
  reset_fixture
  mutate "$FIXTURE/internal/db/block_references.go" 's@(// AddPublishAttemptReferences stages)@func (database *DB) R3NeutralReceiverRead(orgID, blockID string) error {\n\treturn database.Session().Query("SELECT gc_state FROM blocks WHERE org_id = ? AND block_id = ?", orgID, blockID).Exec()\n}\n\n$1@'
  mutate "$FIXTURE/internal/api/v2/fs_helpers.go" 's@(func \(h \*FSHelper\) stagePendingPublishedFiles\([^\{]+\{)@$1\n\t_ = h.db.R3NeutralReceiverRead(orgID, "r3-block")@'
  expect_red ./internal/db '^TestR3PublicationHotPathTypedReceiversAndCQLBudget$' \
    'canonical/orphan authority CQL is reachable' 'typed receiver method hides a cross-package authority read'
}

m_local_function_alias_authority_read() {
  reset_fixture
  mutate "$FIXTURE/internal/db/block_references.go" 's@(// AddPublishAttemptReferences stages)@func R3NeutralLocalAliasRead(database *DB) error {\n\treturn database.Session().Query("SELECT gc_state FROM blocks").Exec()\n}\n\n$1@'
  mutate "$FIXTURE/internal/api/v2/fs_helpers.go" 's@(func \(h \*FSHelper\) stagePendingPublishedFiles\([^\{]+\{)@$1\n\tr3LocalCheck := db.R3NeutralLocalAliasRead\n\t_ = r3LocalCheck(h.db)@'
  expect_red ./internal/db '^TestR3PublicationHotPathTypedReceiversAndCQLBudget$' \
    'canonical/orphan authority CQL is reachable' 'local function alias hides an authority read'
}

m_extra_per_block_cql_insert() {
  reset_fixture
  mutate "$FIXTURE/internal/db/block_references.go" 's/(var addPublishAttemptReferenceFn = func[^\{]+\{)/$1\n\t_ = database.Session().Query("INSERT INTO r3_publication_guards (org_id, block_id) VALUES (?, ?)", orgID, blockID).Exec()/'
  expect_red ./internal/db '^TestR3PublicationHotPathTypedReceiversAndCQLBudget$' \
    'db.AddPublishAttemptReferences submitted CQL callsites = 3, want 2' 'extra per-block CQL INSERT exceeds the frozen budget'
}

m_local_receiver_method_value_authority_read() {
  reset_fixture
  mutate "$FIXTURE/internal/db/block_references.go" 's@(// AddPublishAttemptReferences stages)@func (database *DB) R3NeutralMethodValueRead(orgID, blockID string) error {\n\treturn database.Session().Query("SELECT gc_state FROM blocks WHERE org_id = ? AND block_id = ?", orgID, blockID).Exec()\n}\n\n$1@'
  mutate "$FIXTURE/internal/api/v2/fs_helpers.go" 's@(func \(h \*FSHelper\) stagePendingPublishedFiles\([^\{]+\{)@$1\n\tr3MethodValueCheck := h.db.R3NeutralMethodValueRead\n\t_ = r3MethodValueCheck(orgID, "r3-block")@'
  expect_red ./internal/db '^TestR3PublicationHotPathTypedReceiversAndCQLBudget$' \
    'unresolved local function seam r3MethodValueCheck' 'receiver method value cannot hide an authority read'
}

m_duplicate_existing_per_block_publication_io() {
  reset_fixture
  mutate "$FIXTURE/internal/api/v2/fs_helpers.go" 's@(\tif err := stagePendingPublishedFilesAddReferencesFn\(h\.db, orgID, repoID, attemptID, resolved\); err != nil \{)@\tif err := stagePendingPublishedFilesAddReferencesFn(h.db, orgID, repoID, attemptID, resolved); err != nil {\n\t\treturn err\n\t}\n$1@'
  expect_red ./internal/db '^TestR3PublicationKnownFanoutIsSinglePass$' \
    'stagePendingPublishedFiles calls AddReferences 2 times per pending file, want 1' 'duplicated existing per-file staging I/O exceeds the fan-out contract'
}

m_duplicate_existing_per_normalized_block_io() {
  reset_fixture
  mutate "$FIXTURE/internal/db/block_references.go" 's@(if err := addPublishAttemptReferenceFn\(database, orgID, blockID, referrer, repoID\); err != nil \{)@if err := addPublishAttemptReferenceFn(database, orgID, blockID, referrer, repoID); err != nil {\n\t\t\treturn staged, err\n\t\t}\n\t\t$1@'
  expect_red ./internal/db '^TestR3PublicationKnownFanoutIsSinglePass$' \
    'addPublishAttemptReferencesRows calls addPublishAttemptReferenceFn 2 times per block, want 1' 'duplicated existing per-block reference I/O exceeds the fan-out contract'
}

m_v2_post_stage_authority_read_before_head() {
  reset_fixture
  mutate "$FIXTURE/internal/db/block_references.go" 's@(// AddPublishAttemptReferences stages)@func (database *DB) R3NeutralPostStageRead(orgID, blockID string) error {\n\treturn database.Session().Query("SELECT gc_state FROM blocks WHERE org_id = ? AND block_id = ?", orgID, blockID).Exec()\n}\n\n$1@'
  mutate "$FIXTURE/internal/api/v2/files.go" 's@(func \(h \*FileHandler\) finalizeStoredUploadMetadataOnce[\s\S]*?)(\tif err := queuePendingPublishedFileRepairs)@$1\tif err := h.db.R3NeutralPostStageRead(orgID, "r3-block"); err != nil { return "", 0, 0, err }\n$2@'
  expect_red ./internal/db '^TestR3PublicationStageToHeadHasNoUnlistedDirectDBCalls$' \
    'v2/finalizeStoredUploadMetadataOnce adds direct DB method R3NeutralPostStageRead' 'v2 stage-to-HEAD boundary gains an authority read'
}

m_v2_fshelper_post_stage_authority_read_before_head() {
  reset_fixture
  mutate "$FIXTURE/internal/db/block_references.go" 's@(// AddPublishAttemptReferences stages)@func (database *DB) R3NeutralFSHelperPostStageRead(orgID, blockID string) error {\n\treturn database.Session().Query("SELECT gc_state FROM blocks WHERE org_id = ? AND block_id = ?", orgID, blockID).Exec()\n}\n\n$1@'
  mutate "$FIXTURE/internal/api/v2/files.go" 's@(func \(h \*FileHandler\) finalizeStoredUploadMetadataOnce[\s\S]*?)(\tif err := queuePendingPublishedFileRepairs)@$1\tif err := fsHelper.db.R3NeutralFSHelperPostStageRead(orgID, "r3-block"); err != nil { return "", 0, 0, err }\n$2@'
  expect_red ./internal/db '^TestR3PublicationStageToHeadHasNoUnlistedDirectDBCalls$' \
    'v2/finalizeStoredUploadMetadataOnce adds direct DB method R3NeutralFSHelperPostStageRead' 'v2 fsHelper.db stage-to-HEAD boundary gains an authority read'
}

m_sync_post_stage_authority_read_before_head() {
  reset_fixture
  mutate "$FIXTURE/internal/db/block_references.go" 's@(// AddPublishAttemptReferences stages)@func (database *DB) R3NeutralPostStageRead(orgID, blockID string) error {\n\treturn database.Session().Query("SELECT gc_state FROM blocks WHERE org_id = ? AND block_id = ?", orgID, blockID).Exec()\n}\n\n$1@'
  mutate "$FIXTURE/internal/api/sync.go" 's@(func \(h \*SyncHandler\) handleSyncHeadPromotion[\s\S]*?)(\t\tcleanupStaged := true)@$1\t\tif err := h.db.R3NeutralPostStageRead(orgID, "r3-block"); err != nil { return }\n$2@'
  expect_red ./internal/db '^TestR3PublicationStageToHeadHasNoUnlistedDirectDBCalls$' \
    'sync/handleSyncHeadPromotion adds direct DB method R3NeutralPostStageRead' 'sync stage-to-HEAD boundary gains an authority read'
}

m_materialization_post_metadata_authority_read() {
  reset_fixture
  mutate "$FIXTURE/internal/db/block_references.go" 's@(// AddPublishAttemptReferences stages)@func (database *DB) R3NeutralPostMetadataRead(orgID, blockID string) error {\n\treturn database.Session().Query("SELECT gc_state FROM blocks WHERE org_id = ? AND block_id = ?", orgID, blockID).Exec()\n}\n\n$1@'
  mutate "$FIXTURE/internal/api/v2/fs_helpers.go" 's@(WithLabelValues\("applied"\)\.Inc\(\))@$1\n\tif err := h.db.R3NeutralPostMetadataRead(orgID, blockID); err != nil { return err }@'
  expect_red ./internal/db '^TestR3MaterializationHasNoUnlistedDirectDBCall$' \
    'R3 MATERIALIZATION BUDGET: direct DB method R3NeutralPostMetadataRead' 'materialization gains a post-metadata authority read'
}
