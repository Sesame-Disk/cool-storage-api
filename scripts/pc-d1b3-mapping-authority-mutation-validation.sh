#!/usr/bin/env bash
# PC-D1B.3 Mapping Authority source mutations. Each protocol-incorrect edit
# must turn its targeted contract RED with that contract's own diagnostic.
# All Go tests run inside Docker; sources are restored on every exit.
set -uo pipefail
cd "$(dirname "$0")/.."

TEST_IMAGE=${PCD1B3_MUTATION_IMAGE:-golang:1.25.12-trixie}
RUNNER="sesamefs-pcd1b3-mutation-runner-$$"
PRIMITIVE=internal/db/block_mapping_authority.go
CERTIFIER=internal/db/library_continuity_certifier.go
WRITERS=internal/db/block_references.go
INTEGRATION=internal/db/block_mapping_authority_integration.go
BACKUP_SUFFIX=".pcd1b3bak.$$"
MUTATED=()
STACK_PROJECT="sesamefs-pcd1b3-mutation-stack-$$"
STACK_STARTED=0

green() { echo "RED as required: $*"; }
fail() { echo "FAILED: $*" >&2; restore; exit 1; }

restore() {
	for target in "${MUTATED[@]}"; do
		if [ -f "$target$BACKUP_SUFFIX" ]; then
			mv -f "$target$BACKUP_SUFFIX" "$target"
		fi
	done
	MUTATED=()
}

cleanup() {
	restore
	if [ "$STACK_STARTED" -eq 1 ]; then
		docker compose -p "$STACK_PROJECT" down --volumes --remove-orphans --rmi local >/dev/null 2>&1 || true
	fi
	docker rm -f "$RUNNER" >/dev/null 2>&1 || true
}

trap cleanup EXIT INT TERM

mutate_many() {
	restore
	while [ "$#" -gt 0 ]; do
		local target="$1" expression="$2"
		cp "$target" "$target$BACKUP_SUFFIX"
		MUTATED+=("$target")
		perl -0pi -e "$expression" "$target"
		cmp -s "$target" "$target$BACKUP_SUFFIX" && fail "mutation of $target did not apply"
		shift 2
	done
}

mutate() {
	mutate_many "$@"
}

expect_red() {
    local label="$1" diagnostic="$2" test_pattern="$3" out status
    out="$(docker exec "$RUNNER" go test ./internal/db -count=1 -run "$test_pattern" 2>&1)"
    status=$?
    echo "$out"
    if [ "$status" -eq 0 ]; then
        fail "$label stayed green"
    fi
    if [[ "$out" != *"$diagnostic"* ]]; then
        fail "$label did not trip its targeted contract assertion: $diagnostic"
    fi
    green "$label"
}

# M18a: a claim is consumed without a frozen projection, so a stale ordinary
# write could still make readers resolve elsewhere after the witness.
m18a_consume_without_frozen_projection() {
    mutate "$PRIMITIVE" 's/if !claimed \|\| r\.Projection != BlockMappingProjectionFrozen \{/if !claimed {/'
    expect_red "M18a consume authority without a frozen projection" "a claim without a frozen projection must never resolve" '^TestContinuityWalkerRequiresFrozenProjection$'
}

# M18e: promotion skips the projection freeze (temporal authority).
m18e_promotion_skips_freeze() {
    mutate "$PRIMITIVE" 's/state, freezeErr := ports\.freeze\(ctx, identity, authority\)/_ = authority; state, freezeErr := BlockMappingProjectionFrozen, error(nil)/'
    expect_red "M18e promotion without projection freeze" "promotion must freeze the ordinary projection to the claimed authority" '^TestPromoteBlockMappingAuthorityClaimsOnlyProvedCandidateThenFreezes$'
}

# M18f: the freeze overwrites a projection that already diverged.
m18f_freeze_repairs_divergence() {
    mutate "$PRIMITIVE" 's/if row\.found && NormalizeBlockID\(row\.internalID\) != authority \{/if false \&\& row.found \&\& NormalizeBlockID(row.internalID) != authority {/'
    expect_red "M18f freeze repairs a diverged projection" "a diverged projection must never be overwritten" '^TestBlockMappingProjectionDecisionNeverRepairsDivergence$'
}

# M18g: the pre-witness recheck accepts an agreeing but unfrozen projection.
m18g_recheck_accepts_unfrozen() {
    mutate "$CERTIFIER" 's/if !found \|\| !frozen \|\| NormalizeBlockID\(mappedID\) != authoritativeID \{/if !found || NormalizeBlockID(mappedID) != authoritativeID {/'
    expect_red "M18g pre-witness recheck accepts an unfrozen projection" "unfrozen same before witness" '^TestRevalidateContinuityMappingAuthorityBeforeWitness$'
}

# R1: promotion accepts hash-valid bytes from another representation.
r1_promotion_ignores_representation() {
    mutate "$PRIMITIVE" 's/if blockRepresentationID != identity\.representationID \{/if false \&\& blockRepresentationID != identity.representationID {/'
    expect_red "R1 cross-representation provenance" "proved a plain:v1 mapping" '^TestBlockMappingProvenanceBindsRepresentation$'
}

# R2: consumption ignores the representation of the named canonical block.
r2_consumption_ignores_representation() {
    mutate "$CERTIFIER" 's/if blockRepresentationID != representationID \{/if false \&\& blockRepresentationID != representationID {/'
    expect_red "R2 consumed claim outside the library representation" "mapped block in another representation resolved" '^TestContinuityWalkerBindsMappedBlockRepresentation$'
}

# E1: stored claims with unsupported evidence are accepted.
e1_accept_unsupported_evidence() {
    mutate "$PRIMITIVE" 's/claim\.Evidence == BlockMappingEvidencePhysicalBytesV1 &&//'
    expect_red "E1 unsupported claim evidence accepted" "was accepted as usable authority" '^TestStoredBlockMappingClaimRequiresSupportedEvidence$'
}

# M18d: the pre-witness mapping recheck is dropped, so a mutable write landing
# during certification can be witnessed.
m18d_drop_pre_witness_mapping_recheck() {
    mutate "$CERTIFIER" 's/if err := revalidateContinuityMappingAuthority\(ctx, mappingAuthority, orgID, representationID, dependencies\.sha1Mappings\); err != nil \{/if err := error(nil); err != nil {/'
    expect_red "M18d pre-witness mapping recheck removed" "mapping authority must be rechecked after final metadata revalidation and before the witness" '^TestCertifierRechecksMappingAuthorityBeforeWitness$'
}

# M18b: a promotion that lost the claim race reports its own candidate.
m18b_conflict_reports_candidate() {
    mutate "$PRIMITIVE" 's/Outcome: BlockMappingAuthorityConflict, Authority: stored\.InternalID, Candidate: candidate\}/Outcome: BlockMappingAuthorityConflict, Authority: candidate, Candidate: candidate}/'
    expect_red "M18b losing promotion rewrites authority" "losing promotion must resolve the durable winner A" '^TestBlockMappingAuthorityConflictKeepsDurableWinner$'
}

# M18c: an existing claim is ignored and the mutable mapping is re-promoted.
m18c_existing_claim_ignored() {
    mutate "$PRIMITIVE" 's/return settledBlockMappingClaim\(&stored, ""\)/_ = stored/'
    expect_red "M18c existing authority re-derived from the mutable mapping" "want already-authoritative A" '^TestPromoteBlockMappingAuthorityReturnsExistingClaimWithoutReadingMutable$'
}

# M19a: promotion claims the converged mutable candidate without provenance.
m19a_promote_without_provenance() {
    mutate "$PRIMITIVE" 's/proof, err := ports\.prove\(ctx, identity, candidate\)/proof, err := blockMappingProvenance{identity: identity, internalID: candidate, evidence: BlockMappingEvidencePhysicalBytesV1}, error(nil)/'
    expect_red "M19a convergence accepted as provenance" "converged mutable mapping without provenance must stay UNPROVEN" '^TestBlockMappingConvergenceIsNotProvenance$'
}

# M19b: provenance accepts bytes that only match the SHA-256 candidate.
m19b_sha256_only_provenance() {
    mutate "$PRIMITIVE" 's/if contentSHA1 != identity\.externalID \{/if false \&\& contentSHA1 != identity.externalID {/'
    expect_red "M19b SHA-256-only provenance" "stored bytes that only match the SHA-256 candidate were accepted as provenance" '^TestBlockMappingProvenanceRequiresBothContentDigests$'
}

# S1: the claim inherits a per-datacenter LOCAL_SERIAL domain.
s1_claim_local_serial() {
    mutate "$PRIMITIVE" 's/SerialConsistency\(LibraryHeadSerialConsistency\)/SerialConsistency(gocql.LocalSerial)/'
    expect_red "S1 LOCAL_SERIAL mapping claim" "mapping authority claim must pin global SERIAL explicitly" '^TestBlockMappingAuthorityPinsGlobalSerial$'
}

# S2: the authority read uses LOCAL_SERIAL.
s2_read_local_serial() {
    mutate "$PRIMITIVE" 's/Consistency\(IdentityAuthorityReadConsistency\)/Consistency(gocql.LocalSerial)/'
    expect_red "S2 LOCAL_SERIAL mapping authority read" "mapping authority read must use the global SERIAL read consistency" '^TestBlockMappingAuthorityPinsGlobalSerial$'
}

# H1: the upload mapping writer becomes a per-block LWT.
h1_upload_writer_lwt() {
    mutate "$WRITERS" 's/(INSERT INTO block_id_mappings \(org_id, representation_id, external_id, internal_id, created_at\) VALUES \(\?, \?, \?, \?, \?\))/$1 IF NOT EXISTS/'
    expect_red "H1 upload hot-path Paxos" "issues conditional/authority CQL" '^TestBlockMappingAuthorityAcquisitionIsColdPathOnly$'
}

# H2: the upload mapping writer acquires mapping authority.
h2_upload_writer_promotes() {
    mutate "$WRITERS" 's/(func \(db \*DB\) writeCheckedBlockIDMapping\([^)]*\) error \{)/$1\n\t_, _ = db.PromoteBlockMappingAuthority(context.Background(), nil, orgID, representationID, externalID)/'
    expect_red "H2 upload hot-path promotion" "references PromoteBlockMappingAuthority" '^TestBlockMappingAuthorityAcquisitionIsColdPathOnly$'
}

# I1: a production path deletes (retires) a mapping claim.
i1_claim_retirement() {
    mutate "$PRIMITIVE" 's/\z/\nconst blockMappingAuthorityRetireCQL = "DELETE FROM block_mapping_authority_claims WHERE org_id = ? AND representation_id = ? AND external_id = ?"\n/'
    expect_red "I1 mapping claim retirement" "unauthorized operation on block_mapping_authority_claims" '^TestBlockMappingAuthorityClaimsAreImmutableRepositoryWide$'
}

# M20: productive mapping resolution falls back to a potentially stale ONE
# replica after the EACH_QUORUM temporal freeze.
m20_projection_reader_one() {
    mutate "$PRIMITIVE" 's/BlockMappingProjectionReadConsistency = gocql\.LocalQuorum/BlockMappingProjectionReadConsistency = gocql.One/'
    expect_red "M20 productive mapping reader uses ONE" "productive block mapping consistency = ONE, want LOCAL_QUORUM" '^TestBlockMappingProjectionReadsPinLocalQuorum$'
}

# M21: an ambiguous CAS result from the strong metadata read is not retried.
m21_no_ambiguous_serial_retry() {
    mutate "$PRIMITIVE" 's/blockMappingSerialReadRetryLimit = 2/blockMappingSerialReadRetryLimit = 0/'
    expect_red "M21 strong SERIAL read does not retry ambiguous CAS" "strong SERIAL metadata read =" '^TestProveBlockMappingCandidateRetriesAmbiguousCASOnStrongRead$'
}

# T2/H3: an ordinary writer supplies a timestamp above the authority freeze.
t2_ordinary_writer_supersedes_freeze() {
    mutate "$WRITERS" 's/(INSERT INTO block_id_mappings \([^)]*\) VALUES \(\?, \?, \?, \?, \?\))/$1 USING TIMESTAMP ?/; s/, createdAt\)\.Exec\(\)/, createdAt, BlockMappingProjectionFrozenTimestamp + 1).Exec()/'
    expect_red "T2/H3 ordinary mapping writer timestamp exceeds the freeze" "ordinary block_id_mappings INSERT must not specify USING TIMESTAMP" '^TestBlockMappingMutationsAreRepositoryWideInventoried$'
}

# DEL1: a productive mapping DELETE bypasses the R11a mutation contract.
del1_productive_mapping_delete() {
    mutate "$WRITERS" 's/\z/\nconst blockMappingAuthorityDeleteMutation = "DELETE FROM block_id_mappings WHERE org_id = ?"\n/'
    expect_red "DEL1 production mapping DELETE" "production DELETE from block_id_mappings is prohibited by R11a" '^TestBlockMappingMutationsAreRepositoryWideInventoried$'
}

# H3: a production caller bypasses provenance/claim promotion by invoking the
# projection-freeze primitive directly.
a1_external_freeze_caller() {
    mutate "$WRITERS" 's/\z/\nfunc pcd1b3ExternalFreezeCallerMutation(ctx context.Context, session *gocql.Session, identity blockMappingIdentity, authority string) { _, _ = freezeBlockMappingProjection(ctx, session, identity, authority) }\n/'
    expect_red "A1 external projection-freeze caller" "references freezeBlockMappingProjection" '^TestBlockMappingAuthorityAcquisitionIsColdPathOnly$'
}

# T3: a dominant-timestamp UPDATE is assembled by a helper from local,
# concatenated strings and then passed through a Query variable.
t3_dynamic_timestamp_mapping_update() {
    mutate "$WRITERS" 's/\z/\nfunc pcd1b3DynamicTimestampUpdateCQLMutation() string { query := "UPDATE block_id_" + "mappings USING TIMESTAMP ? SET internal_id = ? WHERE org_id = ? AND representation_id = ? AND external_id = ?"; return query }\nfunc pcd1b3DynamicTimestampUpdateMutation(session *gocql.Session) { query := pcd1b3DynamicTimestampUpdateCQLMutation(); session.Query(query, BlockMappingProjectionFrozenTimestamp, "id", "org", "plain:v1", "sha1") }\n/'
    expect_red "T3 dynamically assembled dominant-timestamp UPDATE" "only freezeBlockMappingProjection may use explicit-timestamp block_id_mappings UPDATE" '^TestBlockMappingMutationsAreRepositoryWideInventoried$'
}

# T4: an unresolved runtime table suffix must not make a dynamic Query escape
# the mapping mutation inventory.
t4_unresolved_mapping_query_argument() {
    mutate "$WRITERS" 's/\z/\nfunc pcd1b3UnknownMappingUpdateCQLMutation(table string) string { return "UPDATE block_id_" + table + " USING TIMESTAMP ? SET internal_id = ? WHERE org_id = ? AND representation_id = ? AND external_id = ?" }\nfunc pcd1b3UnknownMappingUpdateMutation(session *gocql.Session, table string) { query := pcd1b3UnknownMappingUpdateCQLMutation(table); session.Query(query, BlockMappingProjectionFrozenTimestamp, "id", "org", "plain:v1", "sha1") }\n/'
    expect_red "T4 unresolved dynamic mapping Query argument" "unresolved/dynamic block_id_mappings Query argument" '^TestBlockMappingMutationsAreRepositoryWideInventoried$'
}

# D2: strings.Join assembles a production DELETE across literals.
d2_dynamic_mapping_delete() {
    mutate "$WRITERS" 's/\z/\nfunc pcd1b3DynamicDeleteCQLMutation() string { return strings.Join([]string{"DELETE FROM block_id_", "mappings WHERE org_id = ? AND representation_id = ? AND external_id = ?"}, "") }\nfunc pcd1b3DynamicDeleteMutation(session *gocql.Session) { query := pcd1b3DynamicDeleteCQLMutation(); session.Query(query, "org", "plain:v1", "sha1") }\n/'
    expect_red "D2 dynamically assembled mapping DELETE" "production DELETE from block_id_mappings is prohibited by R11a" '^TestBlockMappingMutationsAreRepositoryWideInventoried$'
}

# R3: every mapping SELECT must be in the reader inventory, even if it has no
# pinned consistency floor.
r3_unclassified_mapping_select() {
	mutate "$WRITERS" 's/\z/\nfunc pcd1b3UnclassifiedMappingReaderMutation(session *gocql.Session) { session.Query("SELECT internal_id FROM block_id_mappings WHERE org_id = ? AND representation_id = ? AND external_id = ?", "org", "plain:v1", "sha1").Scan(new(string)) }\n/'
	expect_red "R3 unclassified mapping SELECT" "unclassified production block_id_mappings SELECT" '^TestBlockMappingMutationsAreRepositoryWideInventoried$'
}

# T5: the Query call must resolve q at that point in statement order, before a
# later harmless assignment overwrites the local's final value.
t5_query_before_harmless_reassignment() {
	mutate "$WRITERS" 's/\z/\nfunc pcd1b3QueryBeforeHarmlessReassignmentMutation(session *gocql.Session) { q := "DELETE FROM block_id_mappings WHERE org_id = ? AND representation_id = ? AND external_id = ?"; session.Query(q, "org", "plain:v1", "sha1").Exec(); q = "SELECT now() FROM system.local" }\n/'
	expect_red "T5 dangerous Query followed by harmless reassignment" "production DELETE from block_id_mappings is prohibited by R11a" '^TestBlockMappingMutationsAreRepositoryWideInventoried$'
}

# T6: a known mapping CQL argument must flow into a same-package generic helper
# that performs the Query.
t6_mapping_cql_through_generic_helper() {
	mutate "$WRITERS" 's/\z/\nfunc pcd1b3GenericCQLHelperMutation(session *gocql.Session, q string) { session.Query(q).Exec() }\nfunc pcd1b3GenericCQLHelperCallerMutation(session *gocql.Session) { pcd1b3GenericCQLHelperMutation(session, "DELETE FROM block_id_mappings WHERE org_id = ? AND representation_id = ? AND external_id = ?") }\n/'
	expect_red "T6 mapping CQL passed through a generic helper argument" "production DELETE from block_id_mappings is prohibited by R11a" '^TestBlockMappingMutationsAreRepositoryWideInventoried$'
}

# T7: runtime table identity leaves an unresolved Query argument and must fail
# closed even though no full block_id_mappings token can be reconstructed.
t7_runtime_mapping_table_identity() {
	mutate "$WRITERS" 's/\z/\nfunc pcd1b3RuntimeTableIdentityMutation(session *gocql.Session, table string) { q := "SELECT internal_id FROM " + table + " WHERE org_id = ?"; session.Query(q, "org") }\n/'
	expect_red "T7 unresolved runtime table identity" "unresolved/dynamic block_id_mappings Query argument" '^TestBlockMappingMutationsAreRepositoryWideInventoried$'
}

# H3: the concrete upload conflict pre-check is part of the no-Paxos inventory,
# including CAS terminals unrelated to mapping authority.
h3_upload_precheck_cas() {
	mutate "$WRITERS" 's/(func \(db \*DB\) getBlockIDMappingForWriteCheck\([^\n]*\) \{\n)/$1\t_, _ = db.Session().Query("UPDATE unrelated SET state = ? IF EXISTS", "x").ScanCAS()\n/'
	expect_red "H3 upload pre-check gains a CAS call" "upload mapping writer getBlockIDMappingForWriteCheck runs an LWT" '^TestBlockMappingAuthorityAcquisitionIsColdPathOnly$'
}

# H4: the upload pre-check must also reject a conditional CQL statement even
# when it is consumed through Exec rather than a method named CAS.
h4_upload_precheck_conditional_cql() {
	mutate "$WRITERS" 's/(func \(db \*DB\) getBlockIDMappingForWriteCheck\([^\n]*\) \{\n)/$1\t_ = db.Session().Query("DELETE FROM unrelated WHERE id = ? IF EXISTS", "x").Exec()\n/'
	expect_red "H4 upload pre-check gains conditional CQL" "upload mapping writer getBlockIDMappingForWriteCheck issues conditional/authority CQL" '^TestBlockMappingAuthorityAcquisitionIsColdPathOnly$'
}

# T8: Batch.Bind is a second gocql CQL entry point and must feed the mapping
# mutation inventory just like Query.
t8_mapping_batch_bind() {
	mutate "$WRITERS" 's/\z/\nfunc pcd1b3BatchBindMappingMutation(session *gocql.Session) { batch := session.Batch(gocql.LoggedBatch); batch.Bind("DELETE FROM block_id_mappings WHERE org_id = ? AND representation_id = ? AND external_id = ?", func(info *gocql.QueryInfo) ([]interface{}, error) { return nil, nil }); _ = session.ExecuteBatch(batch) }\n/'
	expect_red "T8 mapping mutation through Batch.Bind" "production DELETE from block_id_mappings is prohibited by R11a" '^TestBlockMappingMutationsAreRepositoryWideInventoried$'
}

# T9: callers can append a public gocql.BatchEntry with a mapping statement.
t9_mapping_batchentry_statement() {
	mutate "$WRITERS" 's/\z/\nfunc pcd1b3BatchEntryMappingMutation(session *gocql.Session) { batch := session.Batch(gocql.LoggedBatch); entry := gocql.BatchEntry{Stmt: "DELETE FROM block_id_mappings WHERE org_id = ? AND representation_id = ? AND external_id = ?"}; batch.Entries = append(batch.Entries, entry); _ = session.ExecuteBatch(batch) }\n/'
	expect_red "T9 mapping mutation through BatchEntry" "submits block_id_mappings CQL through gocql.BatchEntry" '^TestBlockMappingMutationsAreRepositoryWideInventoried$'
}

# T10: public access to Batch.Entries can inject a statement without a new
# BatchEntry literal at the mutation point.
t10_direct_batch_entries_statement() {
	mutate "$WRITERS" 's/\z/\nfunc pcd1b3DirectBatchEntriesMappingMutation(session *gocql.Session) { batch := session.Batch(gocql.LoggedBatch); batch.Query("SELECT now() FROM system.local"); batch.Entries[0].Stmt = "DELETE FROM block_id_mappings WHERE org_id = ? AND representation_id = ? AND external_id = ?"; _ = session.ExecuteBatch(batch) }\n/'
	expect_red "T10 direct Batch.Entries mapping injection" "directly accesses gocql.Batch.Entries" '^TestBlockMappingMutationsAreRepositoryWideInventoried$'
}

# T11: taking Session.Query as a method value severs the Query callsite from
# the structural CQL inventory.
t11_query_method_value_alias() {
	mutate "$WRITERS" 's/\z/\nfunc pcd1b3QueryMethodValueAliasMutation(session *gocql.Session) { query := session.Query; query("DELETE FROM block_id_mappings WHERE org_id = ? AND representation_id = ? AND external_id = ?", "org", "plain:v1", "sha1").Exec() }\n/'
	expect_red "T11 Session.Query method-value alias" "Session.Query method values/aliases are forbidden" '^TestBlockMappingMutationsAreRepositoryWideInventoried$'
}

# T14: an allowlist is valid only while this exact dynamic Query keeps its
# fixed table marker; a matching literal elsewhere in the function is not enough.
t14_dynamic_query_allowlist_marker_moved() {
	mutate "internal/api/v2/admin_link_helpers.go" 's/(func listAdminLinkProjectionCursorPage\([^\n]*\) \{.*?FROM )admin_links_by_created/$1unrelated_table/s'
	expect_red "T14 dynamic Query allowlist marker moved away from its callsite" "dynamic Query lost fixed table marker FROM admin_links_by_created at this call site" '^TestBlockMappingMutationsAreRepositoryWideInventoried$'
}

# T15: taking Batch.Bind as a method value is another route around direct CQL
# entry-point discovery.
t15_batch_bind_method_value_alias() {
	mutate "$WRITERS" 's/\z/\nfunc pcd1b3BatchBindMethodValueAliasMutation(session *gocql.Session) { batch := session.Batch(gocql.LoggedBatch); bind := batch.Bind; bind("DELETE FROM block_id_mappings WHERE org_id = ? AND representation_id = ? AND external_id = ?", func(info *gocql.QueryInfo) ([]interface{}, error) { return nil, nil }); _ = session.ExecuteBatch(batch) }\n/'
	expect_red "T15 Batch.Bind method-value alias" "Batch.Bind method values/aliases are forbidden" '^TestBlockMappingMutationsAreRepositoryWideInventoried$'
}

# T12: a helper with mutually exclusive CQL return paths is not a constant
# builder even when one branch is harmless.
t12_branching_cql_helper() {
	mutate "$WRITERS" 's/\z/\nfunc pcd1b3BranchingCQLMutation(flag bool) string { if flag { return "DELETE FROM block_id_mappings WHERE org_id = ? AND representation_id = ? AND external_id = ?" }; return "SELECT now() FROM system.local" }\nfunc pcd1b3BranchingCQLQueryMutation(session *gocql.Session, flag bool) { session.Query(pcd1b3BranchingCQLMutation(flag), "org", "plain:v1", "sha1").Exec() }\n/'
	expect_red "T12 ambiguous helper return paths" "unresolved/dynamic block_id_mappings Query argument" '^TestBlockMappingMutationsAreRepositoryWideInventoried$'
}

# T13: a local CQL value assigned in a branch is unresolved at the Query
# callsite even if its pre-branch initializer was safe to classify.
t13_branch_assignment_cql() {
	mutate "$WRITERS" 's/\z/\nfunc pcd1b3BranchAssignedCQLMutation(session *gocql.Session, flag bool) { query := "SELECT now() FROM system.local"; if flag { query = "DELETE FROM block_id_mappings WHERE org_id = ? AND representation_id = ? AND external_id = ?" }; session.Query(query, "org", "plain:v1", "sha1").Exec() }\n/'
	expect_red "T13 branch-assigned CQL value" "unresolved/dynamic Query argument in pcd1b3BranchAssignedCQLMutation" '^TestBlockMappingMutationsAreRepositoryWideInventoried$'
}

# T16: goto-based control flow cannot make the resolver pick the textually last
# assignment when an earlier branch can jump around it.
t16_goto_branch_cql() {
	mutate "$WRITERS" 's/\z/\nfunc pcd1b3GotoBranchMappingMutation(session *gocql.Session, flag bool) { query := "DELETE FROM block_id_mappings WHERE org_id = ? AND representation_id = ? AND external_id = ?"; if flag { goto safe }; query = "SELECT now() FROM system.local"; safe: session.Query(query, "org", "plain:v1", "sha1").Exec() }\n/'
	expect_red "T16 goto-ambiguous CQL value" "unresolved/dynamic Query argument in pcd1b3GotoBranchMappingMutation" '^TestBlockMappingMutationsAreRepositoryWideInventoried$'
}

# A3: even an in-file wrapper must not become a second caller of the raw freeze
# primitive, because callers outside the promotion ports could skip provenance.
a3_same_file_freeze_wrapper() {
	mutate_many "$PRIMITIVE" 's/\z/\nfunc pcd1b3RawMappingFreezeWrapperMutation(ctx context.Context, session *gocql.Session, identity blockMappingIdentity, authority string) (BlockMappingProjectionState, error) { return freezeBlockMappingProjection(ctx, session, identity, authority) }\n/' "$WRITERS" 's/\z/\nfunc pcd1b3ExternalRawMappingFreezeWrapperMutation(ctx context.Context, session *gocql.Session, identity blockMappingIdentity, authority string) { _, _ = pcd1b3RawMappingFreezeWrapperMutation(ctx, session, identity, authority) }\n/'
	expect_red "A3 same-file freeze wrapper has an external caller" "freezeBlockMappingProjection direct caller must be blockMappingPromotionPorts" '^TestBlockMappingAuthorityAcquisitionIsColdPathOnly$'
}

# A4: the ports builder itself is a capability factory and may only feed the
# public cold-path promotion helper from PromoteBlockMappingAuthority.
a4_external_ports_freeze_bypass() {
	mutate "$WRITERS" 's/\z/\nfunc pcd1b3ExternalPortsFreezeBypassMutation(db *DB, identity blockMappingIdentity) { ports := db.blockMappingPromotionPorts(nil); _, _ = ports.freeze(context.Background(), identity, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") }\n/'
	expect_red "A4 external promotion-ports freeze bypass" "blockMappingPromotionPorts may only be called by PromoteBlockMappingAuthority" '^TestBlockMappingAuthorityAcquisitionIsColdPathOnly$'
}

# A5: callers cannot obtain the claim capability and manufacture a provenance
# value inside package db, bypassing the physical-byte proof.
a5_external_ports_forged_claim() {
	mutate "$WRITERS" 's/\z/\nfunc pcd1b3ExternalPortsForgedClaimMutation(db *DB, identity blockMappingIdentity) { ports := db.blockMappingPromotionPorts(nil); proof := blockMappingProvenance{identity: identity, internalID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", evidence: BlockMappingEvidencePhysicalBytesV1}; _, _, _ = ports.claim(context.Background(), proof) }\n/'
	expect_red "A5 external promotion-ports forged claim" "blockMappingPromotionPorts may only be called by PromoteBlockMappingAuthority" '^TestBlockMappingAuthorityAcquisitionIsColdPathOnly$'
}

# H5: a global SERIAL authority read must not become reachable from the
# concrete upload conflict pre-check, even as a direct call.
h5_upload_precheck_serial_authority_read() {
	mutate "$WRITERS" 's/(func \(db \*DB\) getBlockIDMappingForWriteCheck\([^\n]*\) \{\n)/$1\t_, _, _ = ReadBlockMappingAuthority(context.Background(), db.Session(), orgID, representationID, externalID)\n/'
	expect_red "H5 upload pre-check reaches Mapping Authority SERIAL reader" "upload mapping writer getBlockIDMappingForWriteCheck reaches cold-path ReadBlockMappingAuthority" '^TestBlockMappingAuthorityAcquisitionIsColdPathOnly$'
}

# H6: a helper and package constant cannot hide conditional CQL from the
# transitive no-Paxos upload-path inventory.
h6_upload_precheck_indirect_conditional_cql() {
	mutate "$WRITERS" 's/(func \(db \*DB\) getBlockIDMappingForWriteCheck\([^\n]*\) \{\n)/$1\tif err := pcd1b3ConditionalUploadHelper(db); err != nil { return "", false, err }\n/; s/\z/\nconst pcd1b3HiddenConditionalUploadCQL = "INSERT INTO unrelated_table (id) VALUES (?) IF NOT EXISTS"\nfunc pcd1b3ConditionalUploadHelper(db *DB) error { return db.Session().Query(pcd1b3HiddenConditionalUploadCQL, "x").Exec() }\n/'
	expect_red "H6 helper-hidden conditional CQL in upload pre-check" "upload mapping writer getBlockIDMappingForWriteCheck issues conditional CQL through" '^TestBlockMappingAuthorityAcquisitionIsColdPathOnly$'
}

# T17: mapping CQL is a closed-world inventory; operations other than the
# authorized select, insert and freeze-update shapes are rejected.
t17_truncate_mapping_table() {
	mutate "$WRITERS" 's/\z/\nfunc pcd1b3TruncateMappingTableMutation(session *gocql.Session) { _ = session.Query("TRUNCATE block_id_mappings").Exec() }\n/'
	expect_red "T17 TRUNCATE block_id_mappings" "unrecognized/dynamic block_id_mappings CQL mutation" '^TestBlockMappingMutationsAreRepositoryWideInventoried$'
}

# T18: a package var is mutable across functions and is never a constant query
# source, even when its initializer is harmless.
t18_mutable_global_cql_value() {
	mutate "$WRITERS" 's/\z/\nvar pcd1b3RuntimeBlockMappingQueryMutation = "SELECT now() FROM system.local"\nfunc pcd1b3SwitchRuntimeBlockMappingQueryMutation() { pcd1b3RuntimeBlockMappingQueryMutation = "DELETE FROM block_id_mappings WHERE org_id = ?" }\nfunc pcd1b3ExecuteRuntimeBlockMappingQueryMutation(session *gocql.Session) { session.Query(pcd1b3RuntimeBlockMappingQueryMutation, "org").Exec() }\n/'
	expect_red "T18 mutable package-global CQL value" "unresolved/dynamic block_id_mappings Query argument" '^TestBlockMappingMutationsAreRepositoryWideInventoried$'
}

# T19: a captured string write in a nested FuncLit makes a helper return
# ambiguous; textual closure assignments cannot replace its runtime value.
t19_nested_closure_cql_assignment() {
	mutate "$WRITERS" 's/\z/\nfunc pcd1b3NestedClosureMappingHelperMutation() string { query := "DELETE FROM block_id_mappings WHERE org_id = ?"; _ = func() { query = "SELECT now() FROM system.local" }; return query }\nfunc pcd1b3NestedClosureMappingQueryMutation(session *gocql.Session) { session.Query(pcd1b3NestedClosureMappingHelperMutation(), "org").Exec() }\n/'
	expect_red "T19 nested FuncLit CQL assignment" "unresolved/dynamic block_id_mappings Query argument" '^TestBlockMappingMutationsAreRepositoryWideInventoried$'
}

# R4: LOCAL_QUORUM on an unrelated query must not satisfy the mapping reader's
# query-chain consistency contract.
r4_unrelated_query_has_local_quorum() {
	mutate "$WRITERS" 's/Consistency\(BlockMappingProjectionReadConsistency\)\.//; s/(func \(db \*DB\) GetBlockIDMappingContext\([^\n]*\) \{\n)/$1\t_ = db.Session().Query("SELECT x FROM something_else").Consistency(BlockMappingProjectionReadConsistency)\n/'
	expect_red "R4 unrelated query supplies LOCAL_QUORUM" "block mapping SELECT consistency=, want BlockMappingProjectionReadConsistency" '^TestBlockMappingMutationsAreRepositoryWideInventoried$'
}

# A2: a function-value alias in the allowed primitive file cannot be called
# from elsewhere to bypass provenance and claim acquisition.
a2_aliased_freeze_caller() {
	mutate_many "$PRIMITIVE" 's/\z/\nvar pcd1b3RawFreezeAliasMutation = freezeBlockMappingProjection\n/' "$WRITERS" 's/\z/\nfunc pcd1b3ExternalAliasedFreezeCallerMutation(ctx context.Context, session *gocql.Session, identity blockMappingIdentity, authority string) { _, _ = pcd1b3RawFreezeAliasMutation(ctx, session, identity, authority) }\n/'
	expect_red "A2 alias of projection-freeze primitive" "freezeBlockMappingProjection may not be taken as a function value or aliased" '^TestBlockMappingAuthorityAcquisitionIsColdPathOnly$'
}

# T1 (behavioral, real Cassandra + MinIO, uses a private Compose project):
# replace the dominant-timestamp freeze with an ordinary rewrite. An ordinary
# write between the final recheck and the witness CAS then reaches readers
# after the witness, which the real reproducer must catch.
t1_freeze_without_dominant_timestamp() {
	mutate "$PRIMITIVE" 's/UPDATE block_id_mappings USING TIMESTAMP \? SET internal_id = \?/UPDATE block_id_mappings SET internal_id = ?/; s/`, BlockMappingProjectionFrozenTimestamp, authority, /`, authority, /; s/row\.writeTime == BlockMappingProjectionFrozenTimestamp/row.writeTime > 0/g'
	local out status
	STACK_STARTED=1
	out="$(SESAMEFS_HOST_PORT=0 CASSANDRA_HOST_PORT=0 MINIO_API_HOST_PORT=0 MINIO_CONSOLE_HOST_PORT=0 FRONTEND_HOST_PORT=0 ONLYOFFICE_HOST_PORT=0 \
		docker compose -p "$STACK_PROJECT" --profile test run --rm --build \
        -e PATH=/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/bin:/sbin \
        -e SESAMEFS_REQUIRE_X1_NONOVERLAP_CHARACTERIZATION=0 \
        -e SESAMEFS_REQUIRE_BORROWEDFS_OWN_LIVENESS_EVIDENCE=0 \
        go-integration-test go test -tags integration -count=1 ./internal/integration/ \
        -run '^TestBlockMappingAuthorityCertifierRealCassandra$/ordinary_write_between_final_recheck_and_witness_CAS_is_inert$' 2>&1)"
    status=$?
    echo "$out" | grep -E -- "--- (PASS|FAIL)|_test.go:[0-9]+:" || true
    if [ "$status" -eq 0 ]; then
        fail "T1 fence-less freeze stayed green on real Cassandra"
    fi
    if [[ "$out" != *"want frozen"* ]]; then
        fail "T1 did not trip the post-witness projection assertion"
    fi
    green "T1 fence-less freeze lets a pre-CAS ordinary write reach readers after the witness"
}

# A6: a same-file wrapper must not make the raw durable claim available to a
# caller that can manufacture physical-byte provenance.
a6_same_file_raw_claim_wrapper() {
	mutate_many "$PRIMITIVE" 's/\z/\nfunc pcd1b3RawClaimWrapperMutation(ctx context.Context, session *gocql.Session, proof blockMappingProvenance) (IdentityClaimOutcome, *BlockMappingAuthorityClaim, error) { return claimBlockMappingAuthority(ctx, session, proof) }\n/' "$WRITERS" 's/\z/\nfunc pcd1b3ForgedRawClaimCallerMutation(ctx context.Context, session *gocql.Session, identity blockMappingIdentity) { proof := blockMappingProvenance{identity: identity, internalID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", evidence: BlockMappingEvidencePhysicalBytesV1}; _, _, _ = pcd1b3RawClaimWrapperMutation(ctx, session, proof) }\n/'
	expect_red "A6 same-file raw claim wrapper and forged provenance" "claimBlockMappingAuthority direct caller must be blockMappingPromotionPorts" '^TestBlockMappingAuthorityAcquisitionIsColdPathOnly$'
}

# A7/A8: promotion-port capabilities may not escape their direct protocol
# callsite as function values.
a7_ports_freeze_function_value() {
	mutate "$WRITERS" 's/\z/\nfunc pcd1b3FreezeCapabilityValueMutation(ports blockMappingPromotionPorts, identity blockMappingIdentity) { freezeFn := ports.freeze; _, _ = freezeFn(context.Background(), identity, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") }\n/'
	expect_red "A7 ports.freeze function value" "blockMappingPromotionPorts .freeze capability may only be used as a direct call" '^TestBlockMappingAuthorityAcquisitionIsColdPathOnly$'
}

a8_ports_claim_function_value() {
	mutate "$WRITERS" 's/\z/\nfunc pcd1b3ClaimCapabilityValueMutation(ports blockMappingPromotionPorts, identity blockMappingIdentity) { claimFn := ports.claim; proof := blockMappingProvenance{identity: identity, internalID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", evidence: BlockMappingEvidencePhysicalBytesV1}; _, _, _ = claimFn(context.Background(), proof) }\n/'
	expect_red "A8 ports.claim function value" "blockMappingPromotionPorts .claim capability may only be used as a direct call" '^TestBlockMappingAuthorityAcquisitionIsColdPathOnly$'
}

# H7: resolve SERIAL through a local enum alias on the upload call graph.
h7_upload_precheck_aliased_serial_consistency() {
	mutate "$WRITERS" 's/(func \(db \*DB\) getBlockIDMappingForWriteCheck\([^\n]*\) \{\n)/$1\tpcd1b3AliasedSerialConsistencyMutation(db)\n/; s/\z/\nfunc pcd1b3AliasedSerialConsistencyMutation(db *DB) { cl := IdentityAuthorityReadConsistency; _ = db.Session().Query("SELECT now() FROM system.local").Consistency(cl).Exec() }\n/'
	expect_red "H7 aliased SERIAL consistency in upload pre-check" "reaches a global SERIAL read through" '^TestBlockMappingAuthorityAcquisitionIsColdPathOnly$'
}

# H8: one driver CAS terminal per mutation. Another terminal (or conditional
# CQL) must never stand in for the selected terminal's own diagnostic.
h8_cas_terminal() {
    local terminal="$1" setup="$2" invocation="$3" compiled
    M1_CAS_SETUP="$setup" M1_CAS_INVOCATION="$invocation" mutate "$WRITERS" 's/(func \(db \*DB\) getBlockIDMappingForWriteCheck\([^\n]*\) \{\r?\n)/$1\tpcd1b3CASTerminalMutation(db)\n/ or die "H8 upload call site not found"; $_ .= "\nfunc pcd1b3CASTerminalMutation(db *DB) { $ENV{M1_CAS_SETUP}; $ENV{M1_CAS_INVOCATION} }\n";'
    if ! compiled="$(docker exec "$RUNNER" go test ./internal/db -count=1 -run '^$' 2>&1)"; then
        echo "$compiled"
        fail "H8 $terminal did not compile; a build failure is not guard evidence"
    fi
    echo "COMPILED H8: $terminal"
    expect_red "H8 $terminal" "reaches an LWT ($terminal)" '^TestBlockMappingAuthorityAcquisitionIsColdPathOnly$'
}

h8_upload_precheck_scan_cas() { h8_cas_terminal ScanCAS 'q := db.Session().Query("UPDATE unrelated SET state = ? IF EXISTS", "x")' '_, _ = q.ScanCAS()'; }
h8_upload_precheck_map_scan_cas() { h8_cas_terminal MapScanCAS 'q := db.Session().Query("UPDATE unrelated SET state = ? IF EXISTS", "x")' '_, _ = q.MapScanCAS(map[string]interface{}{})'; }
h8_upload_precheck_scan_cas_context() { h8_cas_terminal ScanCASContext 'q := db.Session().Query("UPDATE unrelated SET state = ? IF EXISTS", "x")' '_, _ = q.ScanCASContext(context.Background())'; }
h8_upload_precheck_map_scan_cas_context() { h8_cas_terminal MapScanCASContext 'q := db.Session().Query("UPDATE unrelated SET state = ? IF EXISTS", "x")' '_, _ = q.MapScanCASContext(context.Background(), map[string]interface{}{})'; }
h8_upload_precheck_exec_cas() { h8_cas_terminal ExecCAS 'batch := db.Session().Batch(gocql.LoggedBatch); batch.Query("UPDATE unrelated SET state = ? IF EXISTS", "x")' '_, _, _ = batch.ExecCAS()'; }
h8_upload_precheck_map_exec_cas() { h8_cas_terminal MapExecCAS 'batch := db.Session().Batch(gocql.LoggedBatch); batch.Query("UPDATE unrelated SET state = ? IF EXISTS", "x")' '_, _, _ = batch.MapExecCAS(map[string]interface{}{})'; }
h8_upload_precheck_exec_cas_context() { h8_cas_terminal ExecCASContext 'batch := db.Session().Batch(gocql.LoggedBatch); batch.Query("UPDATE unrelated SET state = ? IF EXISTS", "x")' '_, _, _ = batch.ExecCASContext(context.Background())'; }
h8_upload_precheck_map_exec_cas_context() { h8_cas_terminal MapExecCASContext 'batch := db.Session().Batch(gocql.LoggedBatch); batch.Query("UPDATE unrelated SET state = ? IF EXISTS", "x")' '_, _, _ = batch.MapExecCASContext(context.Background(), map[string]interface{}{})'; }
h8_upload_precheck_execute_batch_cas() { h8_cas_terminal ExecuteBatchCAS 'batch := db.Session().Batch(gocql.LoggedBatch); batch.Query("UPDATE unrelated SET state = ? IF EXISTS", "x")' '_, _, _ = db.Session().ExecuteBatchCAS(batch)'; }
h8_upload_precheck_map_execute_batch_cas() { h8_cas_terminal MapExecuteBatchCAS 'batch := db.Session().Batch(gocql.LoggedBatch); batch.Query("UPDATE unrelated SET state = ? IF EXISTS", "x")' '_, _, _ = db.Session().MapExecuteBatchCAS(batch, map[string]interface{}{})'; }

H8_MUTATIONS=(
    h8_upload_precheck_scan_cas
    h8_upload_precheck_map_scan_cas
    h8_upload_precheck_scan_cas_context
    h8_upload_precheck_map_scan_cas_context
    h8_upload_precheck_exec_cas
    h8_upload_precheck_map_exec_cas
    h8_upload_precheck_exec_cas_context
    h8_upload_precheck_map_exec_cas_context
    h8_upload_precheck_execute_batch_cas
    h8_upload_precheck_map_execute_batch_cas
)

# Preserve the old public selector, now running ten independent cases and a
# non-CAS control through the same upload helper path.
h8_upload_precheck_all_cas_terminals() {
    local mutation out
    for mutation in "${H8_MUTATIONS[@]}"; do "$mutation"; done
    mutate "$WRITERS" 's/(func \(db \*DB\) getBlockIDMappingForWriteCheck\([^\n]*\) \{\r?\n)/$1\tpcd1b3NonCASMutation(db)\n/ or die "H8 upload call site not found"; $_ .= "\nfunc pcd1b3NonCASMutation(db *DB) { _ = db.Session().Query(\"UPDATE unrelated SET state = ?\", \"x\").Exec() }\n";'
    if ! out="$(docker exec "$RUNNER" go test ./internal/db -count=1 -run '^TestBlockMappingAuthorityAcquisitionIsColdPathOnly$' 2>&1)"; then
        echo "$out"; fail "H8 ordinary non-CAS control failed"
    fi
    echo "GREEN H8: ordinary non-CAS upload helper"
    restore
}

# H9: a local function-value alias must not hide an LWT helper from the
# transitive upload call-graph walk.
h9_upload_precheck_lwt_function_value_alias() {
	mutate "$WRITERS" 's/(func \(db \*DB\) getBlockIDMappingForWriteCheck\([^\n]*\) \{\n)/$1\tpcd1b3AliasedLWTUploadHelperMutation(db)\n/; s/\z/\nfunc pcd1b3AliasedLWTUploadHelperMutation(db *DB) { helper := pcd1b3LWTUploadHelperMutation; helper(db) }\nfunc pcd1b3LWTUploadHelperMutation(db *DB) { _, _ = db.Session().Query("UPDATE unrelated SET state = ? IF EXISTS", "x").ScanCAS() }\n/'
	expect_red "H9 function-value alias to upload LWT helper" "reaches an LWT (ScanCAS)" '^TestBlockMappingAuthorityAcquisitionIsColdPathOnly$'
}

# T20: taking the address of a tracked Query string makes indirect writes
# possible and must poison that value at the callsite.
t20_pointer_alias_query_mutation() {
	mutate "$WRITERS" 's/\z/\nfunc pcd1b3PointerAliasBlockMappingQueryMutation(session *gocql.Session) { blockMappingQuery := "SELECT now() FROM system.local"; pointer := &blockMappingQuery; *pointer = "DELETE FROM block_id_mappings WHERE org_id = ?"; session.Query(blockMappingQuery, "org").Exec() }\n/'
	expect_red "T20 pointer alias mutates Query string" "unresolved/dynamic block_id_mappings Query argument in pcd1b3PointerAliasBlockMappingQueryMutation" '^TestBlockMappingMutationsAreRepositoryWideInventoried$'
}

# T21: a helper's explicit *gocql.Batch return type carries Batch.Entries
# lineage into its caller and keeps entry statements in the closed-world scan.
t21_helper_returned_batch_entries() {
	mutate "$WRITERS" 's/\z/\nfunc pcd1b3MappingBatchFromHelperMutation(session *gocql.Session) *gocql.Batch { return session.NewBatch(gocql.LoggedBatch) }\nfunc pcd1b3HelperReturnedBatchEntriesMutation(session *gocql.Session) { batch := pcd1b3MappingBatchFromHelperMutation(session); batch.Query("SELECT now() FROM system.local"); batch.Entries[0].Stmt = "DELETE FROM block_id_mappings WHERE org_id = ? AND representation_id = ? AND external_id = ?"; _ = session.ExecuteBatch(batch) }\n/'
	expect_red "T21 helper-returned Batch.Entries mapping injection" "directly accesses gocql.Batch.Entries or an unproven .Entries receiver" '^TestBlockMappingMutationsAreRepositoryWideInventoried$'
}

# A9: declaration counts do not constrain function or method values. Neither
# the ports factory nor the raw promotion helper may escape its direct call.
a9_promotion_factory_and_helper_function_values() {
	mutate "$PRIMITIVE" 's/\z/\nvar pcd1b3RawPromotionPortsValue = (*DB).blockMappingPromotionPorts\nvar pcd1b3RawPromotionHelperValue = promoteBlockMappingAuthority\n/'
	expect_red "A9 promotion factory/helper function values" "may only be used as its authorized direct call" '^TestBlockMappingAuthorityAcquisitionIsColdPathOnly$'
}

# A10: the integration hook can create caller-chosen claims only while the Go
# integration build constraint remains pinned to the exact integration tag.
a10_integration_claim_build_constraint() {
	mutate "$INTEGRATION" 's{\A//go:build integration\n}{}'
	expect_red "A10 integration claim build constraint" "integration claim capability has no pinned //go:build integration constraint" '^TestBlockMappingAuthorityAcquisitionIsColdPathOnly$'
}

# I2: table identity must be resolved at the execution site even when a name
# fragment is declared in another production file.
i2_cross_file_claim_table_mutation() {
	mutate_many "$PRIMITIVE" 's/\z/\nconst pcd1b3ClaimTableSuffixMutation = "authority_claims"\n/' "$WRITERS" 's/\z/\nfunc pcd1b3CrossFileClaimTableMutation(db *DB) { query := "DELETE FROM block_mapping_" + pcd1b3ClaimTableSuffixMutation + " WHERE org_id = ?"; _ = db.Session().Query(query, "org").Exec() }\n/'
	expect_red "I2 cross-file claim-table mutation" "unauthorized execution of block_mapping_authority_claims" '^TestBlockMappingMutationsAreRepositoryWideInventoried$'
}

# H10: the upload no-Paxos walker must resolve same-package receiver methods
# on non-DB helper structs reached from an upload root.
h10_non_db_receiver_hides_upload_lwt() {
	mutate "$WRITERS" 's/(func \(db \*DB\) getBlockIDMappingForWriteCheck\([^\n]*\) \{\n)/$1\tpcd1b3UploadOpsMutation{db: db}.run()\n/; s/\z/\ntype pcd1b3UploadOpsMutation struct { db *DB }\nfunc (ops pcd1b3UploadOpsMutation) run() { query := ops.db.Session().Query("UPDATE unrelated SET state = ? IF EXISTS", "x"); _, _ = query.ScanCAS() }\n/'
	expect_red "H10 LWT hidden behind non-DB receiver method" "reaches an LWT (ScanCAS)" '^TestBlockMappingAuthorityAcquisitionIsColdPathOnly$'
}

# T22: helper method resolution must use declaration identity. Same-name
# methods with different receivers are unresolved until a receiver is proven.
t22_same_name_method_cql_helpers() {
	mutate "$WRITERS" 's/\z/\ntype pcd1b3DangerousBuilderMutation struct{}\ntype pcd1b3HarmlessBuilderMutation struct{}\nfunc (pcd1b3DangerousBuilderMutation) mappingQuery() string { return "DELETE FROM block_id_mappings WHERE org_id = ?" }\nfunc (pcd1b3HarmlessBuilderMutation) mappingQuery() string { return "SELECT now() FROM system.local" }\nfunc pcd1b3SameNameMethodCQLMutation(session *gocql.Session) { dangerous := pcd1b3DangerousBuilderMutation{}; _ = session.Query(dangerous.mappingQuery(), "org").Exec() }\n/'
	expect_red "T22 same-name CQL helper methods" "production DELETE from block_id_mappings is prohibited by R11a" '^TestBlockMappingMutationsAreRepositoryWideInventoried$'
}

# T23: a driver timestamp override can supersede the dominant freeze even
# while the ordinary INSERT's CQL text remains unchanged.
t23_ordinary_query_with_timestamp() {
	mutate "$WRITERS" 's/createdAt\)\.Exec\(\)/createdAt).WithTimestamp(BlockMappingProjectionFrozenTimestamp + 1).Exec()/'
	expect_red "T23 ordinary Query.WithTimestamp" "block_id_mappings Query/Batch must not use WithTimestamp" '^TestBlockMappingMutationsAreRepositoryWideInventoried$'
}

# T24: batches have the same timestamp override surface as ordinary Queries.
t24_ordinary_batch_with_timestamp() {
	mutate "$WRITERS" 's/\z/\nfunc pcd1b3OrdinaryMappingBatchTimestampMutation(session *gocql.Session) { batch := session.Batch(gocql.LoggedBatch); batch.Query("INSERT INTO block_id_mappings (org_id, representation_id, external_id, internal_id, created_at) VALUES (?, ?, ?, ?, ?)", "org", "plain:v1", "sha1", "sha256", time.Now()); batch.WithTimestamp(BlockMappingProjectionFrozenTimestamp + 1); _ = session.ExecuteBatch(batch) }\n/'
	expect_red "T24 ordinary Batch.WithTimestamp" "block_id_mappings Query/Batch must not use WithTimestamp" '^TestBlockMappingMutationsAreRepositoryWideInventoried$'
}

# H11: SetConsistency is an alternate driver setter and must not hide a global
# SERIAL read on any reachable upload helper.
h11_upload_set_consistency_serial() {
	mutate "$WRITERS" 's/(func \(db \*DB\) getBlockIDMappingForWriteCheck\([^\n]*\) \{\n)/$1\tpcd1b3SetConsistencyUploadHelperMutation(db)\n/; s/\z/\nfunc pcd1b3SetConsistencyUploadHelperMutation(db *DB) { query := db.Session().Query("SELECT now() FROM system.local"); query.SetConsistency(gocql.Serial); _ = query.Scan(new(string)) }\n/'
	expect_red "H11 upload SetConsistency(SERIAL)" "uses forbidden SetConsistency" '^TestBlockMappingAuthorityAcquisitionIsColdPathOnly$'
}

# R5: a later SetConsistency(ONE) cannot supersede the productive reader's
# required LOCAL_QUORUM assignment.
r5_mapping_reader_set_consistency_override() {
	mutate "$WRITERS" 's/err = db\.Session\(\)\.Query\(/mappingQuery := db.Session().Query(/; s/WithContext\(ctx\)\.\n\t\tConsistency\(BlockMappingProjectionReadConsistency\)\.\n\t\tScan\(&internalID\)/WithContext(ctx).\n\t\tConsistency(BlockMappingProjectionReadConsistency)\n\tmappingQuery.SetConsistency(gocql.One)\n\terr = mappingQuery.Scan(&internalID)/'
	expect_red "R5 mapping reader SetConsistency(ONE)" "block mapping SELECT consistency=" '^TestBlockMappingMutationsAreRepositoryWideInventoried$'
}

ALL_MUTATIONS=(
    m18a_consume_without_frozen_projection
    m18d_drop_pre_witness_mapping_recheck
    m18e_promotion_skips_freeze
    m18f_freeze_repairs_divergence
    m18g_recheck_accepts_unfrozen
    m18b_conflict_reports_candidate
    m18c_existing_claim_ignored
    m19a_promote_without_provenance
    m19b_sha256_only_provenance
    s1_claim_local_serial
    s2_read_local_serial
    h1_upload_writer_lwt
    h2_upload_writer_promotes
    i1_claim_retirement
    r1_promotion_ignores_representation
    r2_consumption_ignores_representation
    e1_accept_unsupported_evidence
    m20_projection_reader_one
    m21_no_ambiguous_serial_retry
    t2_ordinary_writer_supersedes_freeze
    del1_productive_mapping_delete
    a1_external_freeze_caller
    t3_dynamic_timestamp_mapping_update
    t4_unresolved_mapping_query_argument
    d2_dynamic_mapping_delete
    r3_unclassified_mapping_select
    t5_query_before_harmless_reassignment
    t6_mapping_cql_through_generic_helper
    t7_runtime_mapping_table_identity
    h3_upload_precheck_cas
    h4_upload_precheck_conditional_cql
    t8_mapping_batch_bind
    t9_mapping_batchentry_statement
    t10_direct_batch_entries_statement
    t11_query_method_value_alias
    t14_dynamic_query_allowlist_marker_moved
    t15_batch_bind_method_value_alias
    t12_branching_cql_helper
    t13_branch_assignment_cql
    t16_goto_branch_cql
    r4_unrelated_query_has_local_quorum
    a2_aliased_freeze_caller
    a3_same_file_freeze_wrapper
    a4_external_ports_freeze_bypass
    a5_external_ports_forged_claim
    h5_upload_precheck_serial_authority_read
    h6_upload_precheck_indirect_conditional_cql
    t17_truncate_mapping_table
    t18_mutable_global_cql_value
    t19_nested_closure_cql_assignment
    a6_same_file_raw_claim_wrapper
    a7_ports_freeze_function_value
    a8_ports_claim_function_value
    h7_upload_precheck_aliased_serial_consistency
    "${H8_MUTATIONS[@]}"
    h9_upload_precheck_lwt_function_value_alias
    t20_pointer_alias_query_mutation
    t21_helper_returned_batch_entries
    a9_promotion_factory_and_helper_function_values
    a10_integration_claim_build_constraint
    i2_cross_file_claim_table_mutation
    h10_non_db_receiver_hides_upload_lwt
    t22_same_name_method_cql_helpers
    t23_ordinary_query_with_timestamp
    t24_ordinary_batch_with_timestamp
    h11_upload_set_consistency_serial
    r5_mapping_reader_set_consistency_override
)

WITH_INTEGRATION=0
if [ "${1:-}" = "--with-integration" ]; then
    WITH_INTEGRATION=1
    shift
fi

if [ "${1:-}" = "--list" ]; then
    printf '%s\n' "${ALL_MUTATIONS[@]}"
    exit 0
fi

WORKSPACE_PATH="$(pwd)"
case "${OSTYPE:-}" in
    msys*|cygwin*)
        WORKSPACE_PATH="$(cygpath -m "$WORKSPACE_PATH")"
        ;;
esac

MSYS_NO_PATHCONV=1 docker run -d --name "$RUNNER" \
    -e PATH=/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    -v "$WORKSPACE_PATH:/build" -w /build "$TEST_IMAGE" sleep 3600 >/dev/null
for _ in $(seq 1 30); do
    if docker exec "$RUNNER" go version >/dev/null 2>&1; then
        break
    fi
    sleep 1
done
docker exec "$RUNNER" go version >/dev/null || fail "Docker mutation runner did not start"

# Baseline: every targeted contract must be green before any mutation.
baseline="$(docker exec "$RUNNER" go test ./internal/db -count=1 -run '^(TestContinuityWalkerRequiresFrozenProjection|TestCertifierRechecksMappingAuthorityBeforeWitness|TestPromoteBlockMappingAuthorityClaimsOnlyProvedCandidateThenFreezes|TestBlockMappingProjectionDecisionNeverRepairsDivergence|TestRevalidateContinuityMappingAuthorityBeforeWitness|TestBlockMappingAuthorityConflictKeepsDurableWinner|TestPromoteBlockMappingAuthorityReturnsExistingClaimWithoutReadingMutable|TestBlockMappingConvergenceIsNotProvenance|TestBlockMappingProvenanceRequiresBothContentDigests|TestBlockMappingProvenanceBindsRepresentation|TestContinuityWalkerBindsMappedBlockRepresentation|TestStoredBlockMappingClaimRequiresSupportedEvidence|TestBlockMappingAuthorityPinsGlobalSerial|TestBlockMappingAuthorityAcquisitionIsColdPathOnly|TestBlockMappingAuthorityClaimsAreImmutableRepositoryWide|TestBlockMappingProjectionReadsPinLocalQuorum|TestBlockMappingAuthoritySerialReadRetriesOnlyAmbiguousCAS|TestProveBlockMappingCandidateRetriesAmbiguousCASOnStrongRead|TestBlockMappingMutationsAreRepositoryWideInventoried)$' 2>&1)" || {
    echo "$baseline"
    fail "targeted contracts are not green before mutation"
}

if [ -n "${1:-}" ]; then
	while [ "$#" -gt 0 ]; do
		if [ "$1" = h8_upload_precheck_all_cas_terminals ]; then
			h8_upload_precheck_all_cas_terminals
			shift
			continue
		fi
		selected=0
		for mutation in "${ALL_MUTATIONS[@]}"; do
			if [ "$mutation" = "$1" ]; then
				"$mutation"
				selected=1
				break
			fi
		done
		[ "$selected" -eq 1 ] || fail "unknown mutation $1"
		shift
	done
	restore
	exit 0
fi

for mutation in "${ALL_MUTATIONS[@]}"; do
    "$mutation"
done
restore
SOURCE_TOTAL=${#ALL_MUTATIONS[@]}
TOTAL=$SOURCE_TOTAL
if [ "$WITH_INTEGRATION" -eq 1 ]; then
    t1_freeze_without_dominant_timestamp
    restore
    TOTAL=$((TOTAL + 1))
fi
echo "PC-D1B.3 source mutation legs are RED as required (${SOURCE_TOTAL}/${SOURCE_TOTAL})"
if [ "$WITH_INTEGRATION" -eq 1 ]; then
	 echo "PC-D1B.3 source mutations plus real-Cassandra T1 are RED as required (${TOTAL}/${TOTAL})"
fi
