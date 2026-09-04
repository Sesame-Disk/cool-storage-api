package v2

import (
	"strings"
	"testing"
)

func TestHeadCommitIDGuardAcceptsFormattedQualifiedWriter(t *testing.T) {
	const src = "package v2\n" +
		"func formattedWriter() error {\n" +
		"	return session.Query(\"UPDATE sesamefs.libraries\\nSET\\nhead_commit_id = ?, publication_state = ?\\nWHERE org_id = ? AND library_id = ?\\nIF head_commit_id = ? AND publication_state = ?\", headID, activeState, orgID, repoID, expectedHead, db.LibraryPublicationStateActive).SerialConsistency(gocql.Serial).Exec()\n" +
		"}\n"
	fset, file := headCommitIDGuardParseSnippet(t, src)
	violations, found := checkHeadCommitIDWriters(fset, file)
	if found != 1 || len(violations) != 0 {
		t.Fatalf("formatted qualified writer: found=%d violations=%v, want one writer and no violations", found, violations)
	}
}

func TestHeadCommitIDGuardCatchesWrongPublicationStateOperator(t *testing.T) {
	const src = "package v2\n" +
		"func mutatedWriter() error {\n" +
		"	return session.Query(\"UPDATE libraries SET head_commit_id = ?, publication_state = ? WHERE org_id = ? AND library_id = ? IF head_commit_id = ? AND publication_state != ?\", headID, activeState, orgID, repoID, expectedHead, activeState).SerialConsistency(gocql.Serial).Exec()\n" +
		"}\n"
	fset, file := headCommitIDGuardParseSnippet(t, src)
	violations, found := checkHeadCommitIDWriters(fset, file)
	if found != 1 || len(violations) != 1 || !strings.Contains(violations[0].message, "does not condition on publication_state") {
		t.Fatalf("wrong operator: found=%d violations=%v, want one publication condition violation", found, violations)
	}
}

func TestHeadCommitIDGuardCatchesNonActivePublicationStateBind(t *testing.T) {
	const src = "package v2\n" +
		"func mutatedWriter() error {\n" +
		"	return session.Query(\"UPDATE libraries SET head_commit_id = ?, publication_state = ? WHERE org_id = ? AND library_id = ? IF head_commit_id = ? AND publication_state = ?\", headID, activeState, orgID, repoID, expectedHead, terminalState).SerialConsistency(gocql.Serial).Exec()\n" +
		"}\n"
	fset, file := headCommitIDGuardParseSnippet(t, src)
	violations, found := checkHeadCommitIDWriters(fset, file)
	if found != 1 || len(violations) != 1 || !strings.Contains(violations[0].message, "bound to ACTIVE") {
		t.Fatalf("non-ACTIVE bind: found=%d violations=%v, want one ACTIVE-bind violation", found, violations)
	}
}
