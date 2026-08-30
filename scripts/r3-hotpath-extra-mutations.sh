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
