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

m_wrapper_second_stage_in_pending_files_loop() {
  reset_fixture
  mutate "$FIXTURE/internal/api/v2/fs_helpers.go" 's@(if err := stagePendingPublishedFilesAddReferencesFn\(h\.db, orgID, repoID, attemptID, resolved\); err != nil \{)@_ = r3DuplicatePendingFileStage(h.db, orgID, repoID, attemptID, resolved)\n\t\t$1@'
  expect_red ./internal/db '^TestR3PublicationKnownFanoutIsSinglePass$' \
    'unlisted call r3DuplicatePendingFileStage' 'a second named wrapper in the pendingFiles loop exceeds the fan-out contract'
}

m_wrapper_second_insert_in_normalized_block_loop() {
  reset_fixture
  mutate "$FIXTURE/internal/db/block_references.go" 's@(if err := addPublishAttemptReferenceFn\(database, orgID, blockID, referrer, repoID\); err != nil \{)@_ = r3DuplicateNormalizedBlockInsert(database, orgID, blockID, referrer, repoID)\n\t\t$1@'
  expect_red ./internal/db '^TestR3PublicationKnownFanoutIsSinglePass$' \
    'unlisted call r3DuplicateNormalizedBlockInsert' 'a second named wrapper in the NormalizeBlockIDs loop exceeds the fan-out contract'
}

m_persist_fn_second_publication_sink() {
  reset_fixture
  mutate "$FIXTURE/internal/api/v2/fs_helpers.go" 's@(var stagePendingPublishedFilesPersistFn = func[^\{]+\{)@$1\n\t_ = db.AddPublishAttemptReferences(h.db, pending.cleanupOrgID, repoID, pending.cleanupAttemptID, pending.internalBlockIDs)@'
  expect_red ./internal/db '^TestR3PublicationKnownFanoutIsSinglePass$' \
    'stagePendingPublishedFilesPersistFn reaches AddPublishAttemptReferences' 'an allowed persist seam cannot become a second publication sink'
}

m_funclit_second_publication_sink() {
  reset_fixture
  mutate "$FIXTURE/internal/api/v2/fs_helpers.go" 's@(if err := stagePendingPublishedFilesAddReferencesFn\(h\.db, orgID, repoID, attemptID, resolved\); err != nil \{)@func() { _ = db.AddPublishAttemptReferences(h.db, orgID, repoID, attemptID, resolved) }()\n\t\t$1@'
  expect_red ./internal/db '^TestR3PublicationKnownFanoutIsSinglePass$' \
    'has a nested FuncLit' 'a nested FuncLit in the pendingFiles loop exceeds the fan-out contract'
}

m_v2_session_query_after_stage() {
  reset_fixture
  mutate "$FIXTURE/internal/api/v2/files.go" 's@(func \(h \*FileHandler\) finalizeStoredUploadMetadataOnce[\s\S]*?)(\tif err := fsHelper.stagePendingPublishedFiles)@$1\tsession := h.db.Session()\n$2@'
  mutate "$FIXTURE/internal/api/v2/files.go" 's@(func \(h \*FileHandler\) finalizeStoredUploadMetadataOnce[\s\S]*?)(\tif err := queuePendingPublishedFileRepairs)@$1\t_ = session.Query("SELECT gc_state FROM blocks WHERE org_id = ? AND block_id = ?", orgID, "r3-block")\n$2@'
  expect_red ./internal/db '^TestR3PublicationStageToHeadHasNoUnlistedDirectDBCalls$' \
    'v2/finalizeStoredUploadMetadataOnce adds authority CQL' 'a pre-acquired session Query between stage and HEAD is an authority read'
}

m_v2_dynamic_query_after_stage() {
  reset_fixture
  mutate "$FIXTURE/internal/api/v2/files.go" 's@(func \(h \*FileHandler\) finalizeStoredUploadMetadataOnce[\s\S]*?)(\tif err := fsHelper.stagePendingPublishedFiles)@$1\tsession := h.db.Session()\n$2@'
  mutate "$FIXTURE/internal/api/v2/files.go" 's@(func \(h \*FileHandler\) finalizeStoredUploadMetadataOnce[\s\S]*?)(\tif err := queuePendingPublishedFileRepairs)@$1\tquery := "SELECT gc_state FROM blocks WHERE org_id = ? AND block_id = ?"\n\t_ = session.Query(query, orgID, "r3-block")\n$2@'
  expect_red ./internal/db '^TestR3PublicationStageToHeadHasNoUnlistedDirectDBCalls$' \
    'v2/finalizeStoredUploadMetadataOnce direct CQL callsites' 'a Query whose statement is a variable still consumes the stage-to-HEAD CQL budget'
}

m_v2_const_query_after_stage() {
  reset_fixture
  mutate "$FIXTURE/internal/api/v2/files.go" 's@(func \(h \*FileHandler\) finalizeStoredUploadMetadataOnce[\s\S]*?)(\tif err := fsHelper.stagePendingPublishedFiles)@$1\tsession := h.db.Session()\n$2@'
  mutate "$FIXTURE/internal/api/v2/files.go" 's@(func \(h \*FileHandler\) finalizeStoredUploadMetadataOnce[\s\S]*?)(\tif err := queuePendingPublishedFileRepairs)@$1\tconst r3AuthorityQuery = "SELECT gc_state FROM blocks WHERE org_id = ? AND block_id = ?"\n\t_ = session.Query(r3AuthorityQuery, orgID, "r3-block")\n$2@'
  expect_red ./internal/db '^TestR3PublicationStageToHeadHasNoUnlistedDirectDBCalls$' \
    'v2/finalizeStoredUploadMetadataOnce direct CQL callsites' 'a Query whose statement is a constant still consumes the stage-to-HEAD CQL budget'
}

m_v2_dynamic_insert_after_stage() {
  reset_fixture
  mutate "$FIXTURE/internal/api/v2/files.go" 's@(func \(h \*FileHandler\) finalizeStoredUploadMetadataOnce[\s\S]*?)(\tif err := fsHelper.stagePendingPublishedFiles)@$1\tsession := h.db.Session()\n$2@'
  mutate "$FIXTURE/internal/api/v2/files.go" 's@(func \(h \*FileHandler\) finalizeStoredUploadMetadataOnce[\s\S]*?)(\tif err := queuePendingPublishedFileRepairs)@$1\tquery := "INSERT INTO r3_publication_guards (org_id, block_id) VALUES (?, ?)"\n\t_ = session.Query(query, orgID, "r3-block")\n$2@'
  expect_red ./internal/db '^TestR3PublicationStageToHeadHasNoUnlistedDirectDBCalls$' \
    'v2/finalizeStoredUploadMetadataOnce direct CQL callsites' 'a Query whose INSERT text is a variable still consumes the stage-to-HEAD CQL budget'
}

m_v2_preacquired_query_literal_bind_after_stage() {
  reset_fixture
  mutate "$FIXTURE/internal/api/v2/files.go" 's@(func \(h \*FileHandler\) finalizeStoredUploadMetadataOnce[\s\S]*?)(\tif err := fsHelper.stagePendingPublishedFiles)@$1\tsession := h.db.Session()\n\tprepared := session.Query("SELECT gc_state FROM blocks WHERE org_id = ? AND block_id = ?")\n$2@'
  mutate "$FIXTURE/internal/api/v2/files.go" 's@(func \(h \*FileHandler\) finalizeStoredUploadMetadataOnce[\s\S]*?)(\tif err := queuePendingPublishedFileRepairs)@$1\t_ = prepared.Bind("my-org", "r3-block")\n$2@'
  expect_red ./internal/db '^TestR3PublicationStageToHeadHasNoUnlistedDirectDBCalls$' \
    'v2/finalizeStoredUploadMetadataOnce direct CQL callsites' 'a pre-acquired Query Bind between stage and HEAD still consumes the CQL source-callsite budget'
}

m_v2_db_read_inside_head_argument() {
  reset_fixture
  mutate "$FIXTURE/internal/db/block_references.go" 's@(// AddPublishAttemptReferences stages)@func (database *DB) R3NeutralStringReturningRead(orgID, blockID string) string {\n\t_ = database.Session().Query("SELECT gc_state FROM blocks WHERE org_id = ? AND block_id = ?", orgID, blockID).Exec()\n\treturn ""\n}\n\n$1@'
  mutate "$FIXTURE/internal/api/v2/files.go" 's@(func \(h \*FileHandler\) finalizeStoredUploadMetadataOnce[\s\S]*?)(if err := fsHelper.UpdateLibraryHeadFromSnapshot\(snapshot, repoID, newCommitID, snapshot\.HeadCommitID\); err != nil \{)@$1if err := fsHelper.UpdateLibraryHeadFromSnapshot(snapshot, repoID, h.db.R3NeutralStringReturningRead(orgID, "r3-block"), snapshot.HeadCommitID); err != nil {@'
  expect_red ./internal/db '^TestR3PublicationStageToHeadHasNoUnlistedDirectDBCalls$' \
    'v2/finalizeStoredUploadMetadataOnce adds direct DB method R3NeutralStringReturningRead' 'a DB read in a HEAD argument is still in the stage-to-HEAD interval'
}

m_v2_local_db_alias_post_stage_read() {
  reset_fixture
  mutate "$FIXTURE/internal/db/block_references.go" 's@(// AddPublishAttemptReferences stages)@func (database *DB) R3NeutralAliasPostStageRead(orgID, blockID string) error {\n\treturn database.Session().Query("SELECT gc_state FROM blocks WHERE org_id = ? AND block_id = ?", orgID, blockID).Exec()\n}\n\n$1@'
  mutate "$FIXTURE/internal/api/v2/files.go" 's@(func \(h \*FileHandler\) finalizeStoredUploadMetadataOnce[\s\S]*?)(\tif err := queuePendingPublishedFileRepairs)@$1\tdatabase := h.db\n\tif err := database.R3NeutralAliasPostStageRead(orgID, "r3-block"); err != nil { return "", 0, 0, err }\n$2@'
  expect_red ./internal/db '^TestR3PublicationStageToHeadHasNoUnlistedDirectDBCalls$' \
    'v2/finalizeStoredUploadMetadataOnce adds direct DB method R3NeutralAliasPostStageRead' 'a local db alias cannot hide a stage-to-HEAD authority read'
}

m_v2_local_db_method_value_post_stage_read() {
  reset_fixture
  mutate "$FIXTURE/internal/db/block_references.go" 's@(// AddPublishAttemptReferences stages)@func (database *DB) R3NeutralMethodValuePostStageRead(orgID, blockID string) error {\n\treturn database.Session().Query("SELECT gc_state FROM blocks WHERE org_id = ? AND block_id = ?", orgID, blockID).Exec()\n}\n\n$1@'
  mutate "$FIXTURE/internal/api/v2/files.go" 's@(func \(h \*FileHandler\) finalizeStoredUploadMetadataOnce[\s\S]*?)(\tif err := queuePendingPublishedFileRepairs)@$1\tr3PostStageRead := h.db.R3NeutralMethodValuePostStageRead\n\tif err := r3PostStageRead(orgID, "r3-block"); err != nil { return "", 0, 0, err }\n$2@'
  expect_red ./internal/db '^TestR3PublicationStageToHeadHasNoUnlistedDirectDBCalls$' \
    'v2/finalizeStoredUploadMetadataOnce adds db method value r3PostStageRead' 'a db method value cannot hide a stage-to-HEAD authority read'
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

m_materialization_local_db_alias_read() {
  reset_fixture
  mutate "$FIXTURE/internal/db/block_references.go" 's@(// AddPublishAttemptReferences stages)@func (database *DB) R3NeutralAliasPostMetadataRead(orgID, blockID string) error {\n\treturn database.Session().Query("SELECT gc_state FROM blocks WHERE org_id = ? AND block_id = ?", orgID, blockID).Exec()\n}\n\n$1@'
  mutate "$FIXTURE/internal/api/v2/fs_helpers.go" 's@(WithLabelValues\("applied"\)\.Inc\(\))@$1\n\tdatabase := h.db\n\tif err := database.R3NeutralAliasPostMetadataRead(orgID, blockID); err != nil { return err }@'
  expect_red ./internal/db '^TestR3MaterializationHasNoUnlistedDirectDBCall$' \
    'R3 MATERIALIZATION BUDGET: direct DB method R3NeutralAliasPostMetadataRead' 'a local db alias cannot hide a post-metadata materialization read'
}
