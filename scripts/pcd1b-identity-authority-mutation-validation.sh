#!/usr/bin/env bash
# PC-D1B identity-authority source mutations. Each protocol-incorrect edit must
# make the primitive's own contract tests RED. No application stack is required.
#
# Runs `go test` locally by default. Set PCD1B_MUTATION_IMAGE to a gotest image
# to run inside Docker instead (the repository's usual path when the host cannot
# execute test binaries).
#
# Mutations are matched with \r?\n-tolerant patterns because the working tree
# may be CRLF on Windows checkouts.
set -uo pipefail
cd "$(dirname "$0")/.."

TARGET=internal/db/identity_authority.go
PROD_TARGET=internal/api/v2/library_rollback.go
GATEWAY_TARGET=internal/db/identity_gateway.go
INVENTORY_TARGET=internal/db/pcd1b_identity_writer_inventory_test.go
SCHEMA_TARGET=internal/db/migrations/001_initial_schema.cql
TEST_IMAGE=${PCD1B_MUTATION_IMAGE:-}
DOCKER_CACHE_VOLUME=${PCD1B_MUTATION_CACHE:-sesamefs-pcd1b-go-build-cache}
BACKUP="$TARGET.pcd1bbak"
PROD_BACKUP="$PROD_TARGET.pcd1bbak"
GATEWAY_BACKUP="$GATEWAY_TARGET.pcd1bbak"
INVENTORY_BACKUP="$INVENTORY_TARGET.pcd1bbak"
SCHEMA_BACKUP="$SCHEMA_TARGET.pcd1bbak"

green() { echo "RED as required: $*"; }
fail() { echo "FAILED: $*" >&2; restore; exit 1; }
restore() {
	[ -f "$BACKUP" ] && mv -f "$BACKUP" "$TARGET"
	[ -f "$PROD_BACKUP" ] && mv -f "$PROD_BACKUP" "$PROD_TARGET"
	[ -f "$GATEWAY_BACKUP" ] && mv -f "$GATEWAY_BACKUP" "$GATEWAY_TARGET"
	[ -f "$INVENTORY_BACKUP" ] && mv -f "$INVENTORY_BACKUP" "$INVENTORY_TARGET"
	[ -f "$SCHEMA_BACKUP" ] && mv -f "$SCHEMA_BACKUP" "$SCHEMA_TARGET"
	return 0
}
trap 'status=$?; restore; exit "$status"' EXIT INT TERM

mutate_file() {
	local file="$1" backup="$2" expr="$3"
	restore
	cp "$file" "$backup"
	perl -0pi -e "$expr" "$file"
	cmp -s "$file" "$backup" && fail "mutation did not apply to $file"
	return 0
}
mutate() { mutate_file "$TARGET" "$BACKUP" "$1"; }
mutate_prod() { mutate_file "$PROD_TARGET" "$PROD_BACKUP" "$1"; }
mutate_gateway() { mutate_file "$GATEWAY_TARGET" "$GATEWAY_BACKUP" "$1"; }
mutate_inventory() { mutate_file "$INVENTORY_TARGET" "$INVENTORY_BACKUP" "$1"; }
mutate_schema() { mutate_file "$SCHEMA_TARGET" "$SCHEMA_BACKUP" "$1"; }

run_tests() {
	local pattern="$1"
	if [ -n "$TEST_IMAGE" ]; then
		local container_name="${PCD1B_MUTATION_CONTAINER:-sesamefs-pcd1b-mutation-test-$$}"
		docker rm -f "$container_name" >/dev/null 2>&1 || true
		docker run --rm --name "$container_name" -v "$(pwd):/build" -v "${DOCKER_CACHE_VOLUME}:/root/.cache/go-build" -w /build "$TEST_IMAGE" /usr/local/go/bin/go test ./internal/db -short -count=1 -run "$pattern" 2>&1
	else
		go test ./internal/db -short -count=1 -run "$pattern" 2>&1
	fi
}

expect_red() {
	local pattern="$1" label="$2" needle="${3:-}" out status
	out="$(run_tests "$pattern")"
	status=$?
	if [ "$status" -eq 0 ]; then
		echo "$out"
		fail "$label stayed green"
	fi
	if [ -n "$needle" ] && ! grep -q -- "$needle" <<<"$out"; then
		echo "$out"
		fail "$label went red for the wrong reason (expected: $needle)"
	fi
	echo "$out" | grep -E "^(--- FAIL|FAIL|ok|.*_test.go:[0-9]+:)" | head -8
	green "$label"
}

# --- M17: the file digest must bind the canonical SHA-256 list ---------------
m17_drop_canonical_list() {
	mutate 's/list\(normalizeIdentityBlockIDs\(canonicalSHA256IDs\)\)\.\r?\n/list(nil).\n/'
	expect_red '^TestFileIdentityDigestBindsCanonicalBlockIDs$' "M17: canonical SHA-256 list dropped from the file digest" "canonical list is not bound"
}

m17_drop_length_prefix() {
	mutate 's/(\t\tvar length \[\]byte\r?\n\t\tlength = appendUint64\(length, uint64\(len\(field\)\)\)\r?\n\t\th\.Write\(length\)\r?\n)/\t\t_ = field\n/'
	expect_red '^TestIdentityDigestFieldBoundariesAreUnambiguous$' "M17b: field length prefix removed" "not length-delimited"
}

m17_commit_drop_creator() {
	mutate 's/canonicalCreatorID, err := canonicalIdentityUUID\(creatorID\)/_, err = canonicalIdentityUUID(creatorID)/; s/str\(canonicalCreatorID\)/str("")/'
	expect_red '^TestCommitIdentityDigestBindsCompleteProjection$' "M17c: creator_id dropped from commit V1" "changing creator did not change"
}

m17_commit_drop_description() {
	mutate 's/\t\tstr\(description\)\.\r?\n/\t\tstr("")\.\n/'
	expect_red '^TestCommitIdentityDigestBindsCompleteProjection$' "M17d: description dropped from commit V1" "changing description did not change"
}

m17_commit_drop_created_at() {
	mutate 's/int64\(createdAt\.UnixMilli\(\)\)/int64(0)/'
	expect_red '^TestCommitIdentityDigestBindsCompleteProjection$' "M17e: created_at dropped from commit V1" "changing created_at did not change"
}

m17_uuid_library_spelling() {
	mutate 's/str\(canonicalLibraryID\)/str(libraryID)/g; s/(canonicalLibraryID, err := canonicalIdentityUUID\(libraryID\)\r?\n)/$1\t_ = canonicalLibraryID\n/g'
	expect_red '^TestIdentityDigestCanonicalizesUUIDFieldsAndRejectsInvalidUUIDs$' "M17f: library UUID spelling used instead of Cassandra UUID value" "equivalent UUID spellings produced different"
}

m17_uuid_creator_spelling() {
	mutate 's/str\(canonicalCreatorID\)/str(creatorID)/; s/canonicalCreatorID, err := canonicalIdentityUUID\(creatorID\)/_, err = canonicalIdentityUUID(creatorID)/'
	expect_red '^TestIdentityDigestCanonicalizesUUIDFieldsAndRejectsInvalidUUIDs$' "M17g: creator UUID spelling used instead of Cassandra UUID value" "equivalent UUID spellings produced different"
}

m17_uppercase_digest_accepted() {
	mutate 's/\s*\|\| digest != strings\.ToLower\(digest\)//'
	expect_red '^TestClaimIdentityAuthorityValidatesInput$' "M17h: noncanonical uppercase digest accepted" "expected validation failure"
}

# --- M16: claim lifecycle at the claim layer ----------------------------------
m16_ttl_on_claim() {
	mutate 's/VALUES \(\?, \?, \?, \?, \?, \?\) IF NOT EXISTS/VALUES (?, ?, ?, ?, ?, ?) IF NOT EXISTS USING TTL 3600/'
	expect_red '^TestIdentityAuthorityPinsGlobalSerialExplicitly$' "M16a: claim given a TTL" "must not carry a TTL"
}

m16_delete_path() {
	mutate 's/(func classifyIdentityClaim)/\/\/ DELETE FROM identity_authority_claims WHERE library_id = ?\n$1/'
	expect_red '^TestIdentityAuthorityClaimIsWriteOnce$' "M16b: a delete path added to the primitive" "must not contain"
}

m16_conflict_collapsed() {
	mutate 's/if stored != nil && stored\.DigestVersion == digestVersion && stored\.Digest == digest \{\r?\n\t\treturn IdentityClaimIdempotent/if stored != nil {\n\t\treturn IdentityClaimIdempotent/'
	expect_red '^TestClassifyIdentityClaimRefusesDifferentDigest$' "M16c: a different digest classified as an idempotent retry" "want conflict"
}

m16_nil_readback_as_fresh() {
	mutate 's/\treturn IdentityClaimConflict\r?\n\}/\tif stored == nil {\n\t\treturn IdentityClaimEstablished\n\t}\n\treturn IdentityClaimConflict\n}/'
	expect_red '^TestClassifyIdentityClaimRefusesDifferentDigest$' "M16d: missing read-back treated as a fresh first claim" "never a fresh first claim"
}

# --- SERIAL domain ------------------------------------------------------------
m_serial_local() {
	mutate 's/SerialConsistency\(LibraryHeadSerialConsistency\)/SerialConsistency(gocql.LocalSerial)/'
	expect_red '^TestIdentityAuthorityPinsGlobalSerialExplicitly$' "claim demoted to LOCAL_SERIAL" "LOCAL_SERIAL"
}

m16_repo_update() {
	mutate_prod 's/(func cleanupRolledBackLibraryDerivedState)/var pcd1bClaimUpdateMutation = "UPDATE identity_authority_claims SET digest = ? WHERE library_id = ?"\n\n$1/'
	expect_red '^TestIdentityAuthorityClaimsAreImmutableRepositoryWide$' "M16e: repository-wide claim UPDATE added outside the primitive" "unauthorized operation on identity_authority_claims"
}

m16_repo_delete() {
	mutate_prod 's/(func cleanupRolledBackLibraryDerivedState)/var pcd1bClaimDeleteMutation = "DELETE FROM identity_authority_claims WHERE library_id = ?"\n\n$1/'
	expect_red '^TestIdentityAuthorityClaimsAreImmutableRepositoryWide$' "M16f: repository-wide claim DELETE added outside the primitive" "unauthorized operation on identity_authority_claims"
}

m16_repo_truncate() {
	mutate_prod 's/(func cleanupRolledBackLibraryDerivedState)/var pcd1bClaimTruncateMutation = "TRUNCATE identity_authority_claims"\n\n$1/'
	expect_red '^TestIdentityAuthorityClaimsAreImmutableRepositoryWide$' "M16g: repository-wide claim TRUNCATE added outside the primitive" "unauthorized operation on identity_authority_claims"
}

m16_repo_ttl() {
	mutate_prod 's/(func cleanupRolledBackLibraryDerivedState)/var pcd1bClaimTTLMutation = "INSERT INTO identity_authority_claims (library_id) VALUES (?) USING TTL 3600"\n\n$1/'
	expect_red '^TestIdentityAuthorityClaimsAreImmutableRepositoryWide$' "M16h: repository-wide claim INSERT with TTL added outside the primitive" "unauthorized operation on identity_authority_claims"
}

m16_schema_alter_ttl() {
	mutate_schema 's/$/\nALTER TABLE identity_authority_claims WITH default_time_to_live = 3600;/'
	expect_red '^TestIdentityAuthorityClaimsAreImmutableRepositoryWide$' "M16i: schema default TTL added to identity claims" "unauthorized operation on identity_authority_claims"
}

m16_schema_drop() {
	mutate_schema 's/$/\nDROP TABLE identity_authority_claims;/'
	expect_red '^TestIdentityAuthorityClaimsAreImmutableRepositoryWide$' "M16j: schema DROP added for identity claims" "unauthorized operation on identity_authority_claims"
}

# --- no-bypass inventory ------------------------------------------------------
m_inventory_new_writer() {
	mutate_prod 's/(func cleanupRolledBackLibraryDerivedState)/var pcd1bMutationLeak = "INSERT INTO commits (library_id) VALUES (?)"\n\n$1/'
	expect_red '^TestIdentityWritersAreInventoried$' "inventory: an uninventoried commits writer added to production" "unlisted commits/fs_objects writer"
}

m_inventory_entry_dropped() {
	mutate_inventory 's/internal\/db\/identity_gateway\.go:AddAuthorizedCommitToBatch/internal\/db\/identity_gateway.go:Missing/'
	expect_red '^TestIdentityWriterShapesAreFrozen$' "inventory: a real writer entry removed from the inventory" "Missing"
}

m_inventory_shape_drift() {
	mutate_inventory 's/decl: "AddAuthorizedCommitToBatch", shape: identityWriteInsert/decl: "AddAuthorizedCommitToBatch", shape: identityWriteDisplayOnly/'
	expect_red '^TestIdentityWriterShapesAreFrozen$' "inventory: gateway commit shape drifted" "now has a insert statement"
}

m_inventory_creator_field() {
	mutate_inventory 's/(func TestIdentitySemanticUpdatesAreClassified[\s\S]*?)(creator_id)/$1creator_id_removed/'
	expect_red '^TestIdentitySemanticUpdatesAreClassified$' "inventory: creator_id omitted from commit semantic fields" "semantic field creator_id_removed"
}

m_inventory_description_field() {
	mutate_inventory 's/(func TestIdentitySemanticUpdatesAreClassified[\s\S]*?)(description)/$1description_removed/'
	expect_red '^TestIdentitySemanticUpdatesAreClassified$' "inventory: description omitted from commit semantic fields" "semantic field description_removed"
}

m_inventory_created_at_field() {
	mutate_inventory 's/(func TestIdentitySemanticUpdatesAreClassified[\s\S]*?)(created_at)/$1created_at_removed/'
	expect_red '^TestIdentitySemanticUpdatesAreClassified$' "inventory: created_at omitted from commit semantic fields" "semantic field created_at_removed"
}



# --- B1-B12: productive wiring fence ------------------------------------------
# These mutations deliberately target the changed branch, not only the legacy
# primitive. Each must make a focused gateway/no-bypass contract RED.
b1_direct_commit_insert() {
	mutate_prod 's/(func cleanupRolledBackLibraryDerivedState)/var pcd1bB1 = "INSERT INTO commits (library_id, commit_id) VALUES (?, ?)"\n\n$1/'
	expect_red '^TestIdentityWritersAreInventoried$' "B1: direct commits INSERT outside gateway" "unlisted commits/fs_objects writer"
}
b2_direct_fs_insert() {
	mutate_prod 's/(func cleanupRolledBackLibraryDerivedState)/var pcd1bB2 = "INSERT INTO fs_objects (library_id, fs_id) VALUES (?, ?)"\n\n$1/'
	expect_red '^TestIdentityWritersAreInventoried$' "B2: direct fs_objects INSERT outside gateway" "unlisted commits/fs_objects writer"
}
b3_direct_semantic_update() {
	mutate_prod 's/(func cleanupRolledBackLibraryDerivedState)/var pcd1bB3 = "UPDATE fs_objects SET block_ids = ? WHERE library_id = ? AND fs_id = ?"\n\n$1/'
	expect_red '^TestIdentityWritersAreInventoried$' "B3: direct semantic UPDATE outside gateway" "unlisted commits/fs_objects writer"
}
b4_direct_commit_delete() {
	mutate_prod 's/(func cleanupRolledBackLibraryDerivedState)/var pcd1bB4 = "DELETE FROM commits WHERE library_id = ?"\n\n$1/'
	expect_red '^TestIdentityWritersAreInventoried$' "B4: direct commits DELETE outside gateway" "unlisted commits/fs_objects writer"
}
b5_direct_fs_delete() {
	mutate_prod 's/(func cleanupRolledBackLibraryDerivedState)/var pcd1bB5 = "DELETE FROM fs_objects WHERE library_id = ?"\n\n$1/'
	expect_red '^TestIdentityWritersAreInventoried$' "B5: direct fs_objects DELETE outside gateway" "unlisted commits/fs_objects writer"
}
b6_display_only_escape() {
	mutate_prod 's/(func cleanupRolledBackLibraryDerivedState)/var pcd1bB6 = "UPDATE fs_objects SET obj_name = ?, block_ids = ? WHERE library_id = ? AND fs_id = ?"\n\n$1/'
	expect_red '^TestIdentityWritersAreInventoried$|^TestIdentityWriterShapesAreFrozen$' "B6: display-only update escapes semantic allowlist" "unlisted commits/fs_objects writer"
}
b7_source_before_claim_contract() {
	# Remove the actual Established-path source verification while leaving the
	# helper and other paths intact. The contract counts every required branch.
	mutate_gateway 's/case IdentityClaimEstablished:\r?\n\t\tif err := verifyCommitSourceProjection\(ctx, session, p\); err != nil \{/case IdentityClaimEstablished:\n\t\tif err := error(nil); err != nil {/;'
	expect_red '^TestIdentityGatewayAuthorizationOrderingAndRecoveryContracts$' "B7: source verification contract weakened" "source verification calls"
}
b8_conflict_authorizes_source() {
	# Return a capability from the real commit Conflict branch before its exact
	# second claim; this must fail the branch-level re-claim contract.
	mutate_gateway 's/retry, retryErr := ClaimIdentityAuthorityAt\(ctx, session, p.LibraryID, IdentityKindCommit, p.CommitID, SupportedIdentityDigestVersion, recoveredDigest, recovered\)/retry, retryErr := IdentityClaimResult{}, error(nil)\n\t\treturn \&AuthorizedCommit{projection: p}, nil/'
	expect_red '^TestIdentityGatewayAuthorizationOrderingAndRecoveryContracts$' "B8: Conflict accepted as source authorization" "exact idempotent re-claim"
}
b9_unknown_authorizes_source() {
	# Remove the actual Unknown early return and make the switch return a
	# capability for Unknown in both gateways. The fail-closed contract must go RED.
	mutate_gateway 's/if err != nil \|\| result.Outcome == IdentityClaimUnknown \{/if err != nil {/g; s/return outcome == IdentityClaimEstablished \|\| outcome == IdentityClaimIdempotent/return outcome == IdentityClaimEstablished || outcome == IdentityClaimIdempotent || outcome == IdentityClaimUnknown/; s/(case IdentityClaimEstablished:\r?\n)/case IdentityClaimUnknown:\n\t\treturn \&AuthorizedCommit{projection: p}, nil\n\t$1/; s/(case IdentityClaimEstablished, IdentityClaimIdempotent:\r?\n)/case IdentityClaimUnknown:\n\t\treturn \&AuthorizedFSObject{projection: p}, nil\n\t$1/'
	expect_red '^TestIdentityGatewayAuthorizationOrderingAndRecoveryContracts$' "B9: Unknown accepted as source authorization" "unknown claim outcomes"
}
b10_fresh_retry_timestamp() {
	mutate_gateway 's/func commitRetryCreatedAt\(candidate, stored time.Time\) time.Time \{\r?\n\tif stored.IsZero\(\) \{\r?\n\t\treturn canonicalIdentityCreatedAt\(candidate\)\r?\n\t\}\r?\n\treturn canonicalIdentityCreatedAt\(stored\)\r?\n\}/func commitRetryCreatedAt(candidate, stored time.Time) time.Time {\n\treturn canonicalIdentityCreatedAt(candidate)\n}/'
	expect_red '^TestCommitRetryUsesDurableClaimTimestamp$' "B10: retry minted a fresh server timestamp" "stored claim"
}
b11_writer_authority_removed() {
	mutate_prod 's/AddUnpublishedLibraryIdentityPartitionDeletesToBatch/AddUnpublishedLibraryIdentityPartitionDeletesToBatchBypass/'
	expect_red '^TestIdentityProductionWritersUseGateway$' "B11: rollback writer bypasses gateway" "bypasses gateway"
}
b12_divergence_overwritten() {
    mutate_gateway 's/!identitySourceDigestMatches\(actualDigest, expectedDigest\)/actualDigest == "" || expectedDigest == ""/'
    expect_red '^TestIdentityGatewayAuthorizationOrderingAndRecoveryContracts$' "B12: divergent source row would be overwritten" "divergence"
}

# --- fence hardening ----------------------------------------------------------
b13_dynamic_concat_cql() {
	mutate_prod 's/(func cleanupRolledBackLibraryDerivedState)/var pcd1bB13 = "INSERT INTO " + "commits (library_id, commit_id) VALUES (?, ?)"\n\n$1/'
	expect_red '^TestIdentityDynamicCQLIsClosed$|^TestIdentityWritersAreInventoried$' "B13: concatenated identity CQL escaped the fence" "unlisted commits/fs_objects writer\\|dynamic or unresolved identity CQL"
}
b14_partition_delete_caller() {
	mutate_prod 's/(func cleanupRolledBackLibraryDerivedState)/var pcd1bB14 = dbpkg.AddUnpublishedLibraryIdentityPartitionDeletesToBatch(nil, "")\n\n$1/'
	expect_red '^TestIdentityPartitionDeleteHasOnlyAuthorizedCaller$' "B14: unauthorized partition-delete caller" "partition delete callers"
}
b15_forged_capability() {
	mutate_prod 's/(func cleanupRolledBackLibraryDerivedState)/var pcd1bB15 = dbpkg.AuthorizedCommit{}\n\n$1/'
	expect_red '^TestIdentityCapabilitiesConstructedOnlyByAuthorization$' "B15: forged AuthorizedCommit capability" "identity capability constructed"
}
b16_semantic_migration() {
	mutate_schema 's/(CREATE TABLE)/UPDATE commits SET root_fs_id = ? WHERE library_id = ?;\n\n$1/'
	expect_red '^TestIdentitySemanticCQLDoesNotExistInMigrations$' "B16: semantic migration CQL escaped the fence" "semantic commits/fs_objects CQL"
}
b17_fmt_sprintf_identity_cql() {
	mutate_prod 's/(func cleanupRolledBackLibraryDerivedState)/var pcd1bB17 = fmt.Sprintf("INSERT INTO %s (library_id) VALUES (?)", "commits")\n\n$1/'
	expect_red '^TestIdentityDynamicCQLIsClosed$' "B17: fmt.Sprintf identity CQL escaped the fence" "unlisted commits/fs_objects writer\\|dynamic or unresolved identity CQL"
}
b18_unresolved_identity_query() {
	mutate_prod 's/(batch := session.Batch\(gocql.LoggedBatch\))/$1\n\tquery := "INSERT INTO " + fmt.Sprint("commits") + " (library_id) VALUES (?)"\n\tif false { _ = session.Query(query) }/'
	expect_red '^TestIdentityDynamicCQLIsClosed$' "B18: unresolved identity Query escaped the fence" "unlisted commits/fs_objects writer\\|dynamic or unresolved identity CQL"
}
b22_projection_access_fence() {
	mutate 's/(func ClaimIdentityAuthority)/var pcd1bB22 = func(cap *AuthorizedCommit) string { return cap.projection.RootFSID }\n\n$1/'
	expect_red '^TestIdentityCapabilitiesConstructedOnlyByAuthorization$' "B22: capability projection accessed outside gateway" "projection access"
}
b23_strings_join_identity_cql() {
	mutate 's/(func ClaimIdentityAuthority)/var pcd1bB23 = strings.Join([]string{"INSERT INTO", "commits", "(library_id) VALUES (?)"}, " ")\n\n$1/'
	expect_red '^TestIdentityDynamicCQLIsClosed$' "B23: strings.Join identity CQL escaped the fence" "dynamic or unresolved identity CQL"
}
b24_helper_returned_identity_query() {
	mutate 's/(func ClaimIdentityAuthority)/func pcd1bB24BuildQuery() string { return "INSERT INTO " + "commits" + " (library_id) VALUES (?)" }\nfunc pcd1bB24UseQuery(session *gocql.Session) { q := pcd1bB24BuildQuery(); session.Query(q) }\n\n$1/'
	expect_red '^TestIdentityDynamicCQLIsClosed$' "B24: helper-returned identity Query escaped the fence" "dynamic or unresolved identity CQL"
}
b19_forged_pointer_capability() {
	mutate_prod 's/(func cleanupRolledBackLibraryDerivedState)/var pcd1bB19 = new(dbpkg.AuthorizedCommit)\n\n$1/'
	expect_red '^TestIdentityCapabilitiesConstructedOnlyByAuthorization$' "B19: pointer capability forged outside gateway" "identity capability constructed"
}
b20_verifier_fallback() {
	mutate_gateway 's/\treturn outcome, nil/\tif outcome == IdentityVerificationConflict {\n\t\treturn IdentityVerificationVerified, nil\n\t}\n\treturn outcome, nil/'
	expect_red '^TestIdentityGatewayAuthorizationOrderingAndRecoveryContracts$' "B20: paired verifier accepted a SHA1-only fallback" "non-exact SHA1-only fallback"
}
b21_compatibility_reclaim_removed() {
	mutate_gateway 's/if !identityExactRetryMatches\(retry, SupportedIdentityDigestVersion, sha1OnlyDigest\)/if retry.Outcome == IdentityClaimUnknown/'
	expect_red '^TestIdentityGatewayAuthorizationOrderingAndRecoveryContracts$' "B21: mixed-funnel compatibility skipped exact re-claim" "exact re-claim checks"
}

# --- scope guard --------------------------------------------------------------
m_premature_consumer() {
	mutate_prod 's/(func cleanupRolledBackLibraryDerivedState)/var _ = ClaimIdentityAuthority\n\n$1/'
	expect_red '^TestIdentityAuthorityPrimitiveHasNoRawProductionCallerOutsideGateway$' "scope: a production consumer of the primitive appeared" "production consumers"
}

# I1: resolve identity CQL at the actual Query/Batch sites using the existing
# shared execution inventory. Both table fragments live in different production
# files; the old per-file literal inventory alone could not see these operations.
i1_fixture() {
    local statement="$1" execution="$2"
    restore
    cp "$TARGET" "$BACKUP"
    cp "$GATEWAY_TARGET" "$GATEWAY_BACKUP"
    printf '\nconst i1ClaimPrefix = "identity_authority_"\nfunc i1ClaimTable() string { return i1ClaimPrefix + i1ClaimSuffix }\nfunc i1GuardMutation(session *gocql.Session) { statement := %s; %s }\n' "$statement" "$execution" >> "$TARGET"
    printf '\nconst i1ClaimSuffix = "claims"\n' >> "$GATEWAY_TARGET"
}

i1_expect_red() {
    local label="$1" diagnostic="$2" compiled
    if ! compiled="$(run_tests '^$')"; then
        echo "$compiled"
        fail "$label did not compile; a build failure is not guard evidence"
    fi
    expect_red '^TestIdentityAuthorityClaimsAreImmutableRepositoryWide$' "$label" "$diagnostic"
}

i1_positive_local_query() {
    mutate 's/(\texisting := map\[string\]interface\{\}\{\}\r?\n)\tapplied, err := session.Query\((`[^`]+`), /statement := $2\n$1\tapplied, err := session.Query(statement, /'
    local out
    if ! out="$(run_tests '^TestIdentityAuthorityClaimsAreImmutableRepositoryWide$')"; then
        echo "$out"; fail "I1 legitimate local statement control failed"
    fi
    echo "GREEN control: I1 approved identity Query through a local binding"
}

i1_execution_mutations() {
    i1_positive_local_query
    i1_fixture '"SELECT now() FROM system.local"' '_ = session.Query(statement).Exec()'
    local out
    if ! out="$(run_tests '^TestIdentityAuthorityClaimsAreImmutableRepositoryWide$')"; then
        echo "$out"; fail "I1 unrelated Query control failed"
    fi
    echo "GREEN control: I1 unrelated resolved Query"
    i1_fixture '"UPDATE " + i1ClaimTable() + " SET digest = ? WHERE library_id = ?"' '_ = session.Query(statement).Exec()'
    i1_expect_red 'I1 cross-file helper UPDATE' 'unauthorized identity execution'
    i1_fixture '"DELETE FROM " + i1ClaimPrefix + i1ClaimSuffix + " WHERE library_id = ?"' '_ = session.Query(statement).Exec()'
    i1_expect_red 'I1 cross-file concatenated DELETE' 'unauthorized identity execution'
    i1_fixture 'fmt.Sprintf("TRUNCATE %s%s", i1ClaimPrefix, i1ClaimSuffix)' '_ = session.Query(statement).Exec()'
    i1_expect_red 'I1 formatted TRUNCATE' 'unresolved/dynamic Query argument in i1GuardMutation'
    i1_fixture '"INSERT INTO " + i1ClaimTable() + " (library_id) VALUES (?) USING TTL 10"' '_ = session.Query(statement).Exec()'
    i1_expect_red 'I1 INSERT with TTL' 'unauthorized identity execution'
    i1_fixture '"SELECT digest_version, digest, created_at FROM " + i1ClaimTable() + " WHERE library_id = ? AND identity_kind = ? AND identity_id = ?"' '_ = session.Query(statement).Exec()'
    i1_expect_red 'I1 SELECT outside approved owner' 'unauthorized identity execution'
    i1_fixture '"UPDATE " + i1ClaimTable() + " SET digest = ? WHERE library_id = ?"' 'b := session.Batch(gocql.LoggedBatch); b.Query(statement); _ = session.ExecuteBatch(b)'
    i1_expect_red 'I1 Batch.Query' 'unauthorized identity execution'
    i1_fixture '"DELETE FROM " + i1ClaimTable() + " WHERE library_id = ?"' 'b := session.Batch(gocql.LoggedBatch); b.Bind(statement, func(*gocql.QueryInfo) ([]interface{}, error) { return nil, nil }); _ = session.ExecuteBatch(b)'
    i1_expect_red 'I1 Batch.Bind' 'unauthorized identity execution'
    i1_fixture '"TRUNCATE " + i1ClaimTable()' 'b := session.Batch(gocql.LoggedBatch); b.Entries = append(b.Entries, gocql.BatchEntry{Stmt: statement}); _ = session.ExecuteBatch(b)'
    i1_expect_red 'I1 BatchEntry' 'unauthorized identity BatchEntry execution'
    i1_fixture 'strings.Join([]string{"DELETE FROM ", i1ClaimPrefix, i1ClaimSuffix}, "")' 'query := session.Query; _ = query(statement).Exec()'
    i1_expect_red 'I1 Query method alias' 'Session.Query method values/aliases'
    mutate 's/\z/\nfunc i1GuardMutation(session *gocql.Session, statement string) { _ = session.Query(statement).Exec() }\n/'
    i1_expect_red 'I1 unclassified dynamic Query' 'unresolved/dynamic Query argument in i1GuardMutation'
    mutate 's/(\texisting := map\[string\]interface\{\}\{\}\r?\n)\tapplied, err := session.Query\((`[^`]+`), /statement := $2\n$1\t_ = session.Query(statement).Exec()\n\tapplied, err := session.Query(statement, /'
    i1_expect_red 'I1 duplicate approved INSERT site with one literal' 'identity_authority_claims execution inventory INSERT=2 SELECT=1'
    mutate 's/(\texisting := map\[string\]interface\{\}\{\}\r?\n)\tapplied, err := session.Query\((`[^`]+`), /statement := $2\n$1\tb := session.Batch(gocql.LoggedBatch); b.Query(statement); _ = session.ExecuteBatch(b)\n\tapplied, err := session.Query(statement, /'
    i1_expect_red 'I1 approved INSERT submitted through Batch.Query' 'unauthorized identity execution'
    mutate 's/SELECT digest_version, digest, created_at\r?\n/SELECT digest_version, digest, created_at, identity_id\n/'
    i1_expect_red 'I1 SELECT projection drift' 'unauthorized identity execution'
    restore
}

case "${PCD1B_MUTATION_SCOPE:-all}" in
    all|i1) ;;
    *) fail "unknown PCD1B_MUTATION_SCOPE (use all or i1)" ;;
esac

echo "== baseline must be GREEN =="
restore
if ! run_tests 'TestIdentity|TestFileIdentity|TestCommitIdentity|TestClaimIdentity|TestClassifyIdentity' >/dev/null 2>&1; then
	fail "baseline is not green; fix the tests before validating mutations"
fi
echo "baseline green"

i1_execution_mutations
if [ "${PCD1B_MUTATION_SCOPE:-all}" = i1 ]; then
    echo "== all I1 mutations RED; controls GREEN; sources restored =="
    exit 0
fi


m17_drop_canonical_list
m17_drop_length_prefix
m17_commit_drop_creator
m17_commit_drop_description
m17_commit_drop_created_at
m17_uuid_library_spelling
m17_uuid_creator_spelling
m17_uppercase_digest_accepted
m16_ttl_on_claim
m16_delete_path
m16_conflict_collapsed
m16_repo_update
m16_repo_delete
m16_repo_truncate
m16_repo_ttl
m16_schema_alter_ttl
m16_schema_drop
m16_nil_readback_as_fresh
m_serial_local
m_inventory_new_writer
m_inventory_entry_dropped
m_inventory_shape_drift
m_inventory_creator_field
m_inventory_description_field
m_inventory_created_at_field
m_premature_consumer
b1_direct_commit_insert
b2_direct_fs_insert
b3_direct_semantic_update
b4_direct_commit_delete
b5_direct_fs_delete
b6_display_only_escape
b7_source_before_claim_contract
b8_conflict_authorizes_source
b9_unknown_authorizes_source
b10_fresh_retry_timestamp
b11_writer_authority_removed
b12_divergence_overwritten
b13_dynamic_concat_cql
b14_partition_delete_caller
b15_forged_capability
b16_semantic_migration
b17_fmt_sprintf_identity_cql
b18_unresolved_identity_query
b19_forged_pointer_capability
b20_verifier_fallback
b21_compatibility_reclaim_removed
b22_projection_access_fence
b23_strings_join_identity_cql
b24_helper_returned_identity_query

restore
echo "== all PC-D1B identity-authority mutations RED as required =="
