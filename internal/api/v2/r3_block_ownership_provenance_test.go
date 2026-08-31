package v2

import (
	"go/ast"
	"go/token"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/db"
)

func TestR3BlockOwnershipProvenanceMatrix(t *testing.T) {
	ownReferrer := db.BlockReferrerForUpload("session-1")
	foreignUploadReferrer := db.BlockReferrerForUpload("session-2")
	foreignPublishReferrer := db.BlockReferrerForPublishAttempt("attempt-1")
	committedReferrer := db.BlockReferrerForFSObject("library-1", "fs-1")
	secondCommittedReferrer := db.BlockReferrerForFSObject("library-2", "fs-2")

	tests := []struct {
		name      string
		referrers []string
		want      blockCommitLivenessProvenance
	}{
		{
			name:      "own session pin only",
			referrers: []string{ownReferrer},
			want:      blockCommitLivenessSessionUpload,
		},
		{
			name:      "committed fs only",
			referrers: []string{committedReferrer},
			want:      blockCommitLivenessCommittedFS,
		},
		{
			name:      "foreign pub only",
			referrers: []string{foreignPublishReferrer},
			want:      blockCommitLivenessNone,
		},
		{
			name:      "foreign upload only",
			referrers: []string{foreignUploadReferrer},
			want:      blockCommitLivenessNone,
		},
		{
			name:      "empty",
			referrers: nil,
			want:      blockCommitLivenessNone,
		},
		{
			name:      "committed fs before own session pin",
			referrers: []string{committedReferrer, ownReferrer},
			want:      blockCommitLivenessSessionUpload,
		},
		{
			name:      "own session pin before committed fs",
			referrers: []string{ownReferrer, committedReferrer},
			want:      blockCommitLivenessSessionUpload,
		},
		{
			name:      "multiple committed fs",
			referrers: []string{committedReferrer, secondCommittedReferrer},
			want:      blockCommitLivenessCommittedFS,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyBlockReferrerProvenance(test.referrers, ownReferrer); got != test.want {
				t.Fatalf("provenance = %d, want %d", got, test.want)
			}
		})
	}
}

func TestR3BlockOwnershipProvenanceDistinguishesCommittedFS(t *testing.T) {
	ownReferrer := db.BlockReferrerForUpload("session-1")
	committedReferrer := db.BlockReferrerForFSObject("library-1", "fs-1")
	got := classifyBlockReferrerProvenance([]string{committedReferrer}, ownReferrer)
	if got != blockCommitLivenessCommittedFS {
		t.Fatalf("foreign fs must be classified as committed-fs: got %d, want %d", got, blockCommitLivenessCommittedFS)
	}
}

func TestR3CurrentCommitReadinessPolicyPreservesForeignFSBehavior(t *testing.T) {
	tests := []struct {
		provenance blockCommitLivenessProvenance
		wantReady  bool
	}{
		{provenance: blockCommitLivenessSessionUpload, wantReady: true},
		{provenance: blockCommitLivenessCommittedFS, wantReady: true},
		{provenance: blockCommitLivenessNone, wantReady: false},
		{provenance: blockCommitLivenessProvenance(255), wantReady: false},
	}

	for _, test := range tests {
		if got := blockCommitProvenanceCurrentlyReady(test.provenance); got != test.wantReady {
			t.Errorf("provenance %d ready = %t, want %t", test.provenance, got, test.wantReady)
		}
	}
}

func TestR3ClassifyBlockOwnershipUsesSingleReferrerPartitionRead(t *testing.T) {
	path := r3SourcePath("internal", "api", "v2", "file_from_blocks.go")
	_, fn := r3ParseFunction(t, path, "classifyBlockOwnership")
	calls := r3CallPositions(fn)
	if got := len(calls["ListBlockReferrers"]); got != 1 {
		t.Fatalf("R3 OWNERSHIP READ CONTRACT: ListBlockReferrers calls = %d, want 1", got)
	}
	if got := len(calls["classifyBlockReferrerProvenance"]); got != 1 {
		t.Fatalf("R3 OWNERSHIP READ CONTRACT: pure provenance classifier calls = %d, want 1", got)
	}
	forbidden := []string{
		"BlockHasReferrer",
		"BlockDeleteFenceActive",
		"BlockHasReferencesGlobal",
		"GetBlockInfo",
		"GetS3OrphanGlobal",
		"ValidateBlockPublishAuthority",
	}
	for _, name := range forbidden {
		if got := len(calls[name]); got != 0 {
			t.Fatalf("R3 OWNERSHIP READ CONTRACT: forbidden %s calls = %d, want 0", name, got)
		}
	}
}

func TestR3BlockOwnershipProvenanceChecksExactSessionReferrer(t *testing.T) {
	path := r3SourcePath("internal", "api", "v2", "file_from_blocks.go")
	_, fn := r3ParseFunction(t, path, "classifyBlockReferrerProvenance")
	foundExactSessionComparison := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		binary, ok := node.(*ast.BinaryExpr)
		if !ok || binary.Op != token.EQL {
			return true
		}
		left, leftOK := binary.X.(*ast.Ident)
		right, rightOK := binary.Y.(*ast.Ident)
		if leftOK && rightOK && ((left.Name == "referrer" && right.Name == "sessionReferrer") ||
			(left.Name == "sessionReferrer" && right.Name == "referrer")) {
			foundExactSessionComparison = true
		}
		return true
	})
	if !foundExactSessionComparison {
		t.Fatal("R3 OWNERSHIP PROVENANCE CONTRACT: exact session referrer comparison not found")
	}
}

func TestR3CheckAndCommitUseSharedReadinessPolicy(t *testing.T) {
	functions := []struct {
		path     string
		function string
	}{
		{path: r3SourcePath("internal", "api", "v2", "file_from_blocks.go"), function: "classifyBlockForCommit"},
		{path: r3SourcePath("internal", "api", "v2", "blocks.go"), function: "checkBlocksReadyParallel"},
	}

	for _, test := range functions {
		t.Run(test.function, func(t *testing.T) {
			_, fn := r3ParseFunction(t, test.path, test.function)
			calls := r3CallPositions(fn)
			if got := len(calls["blockCommitProvenanceCurrentlyReady"]); got != 1 {
				t.Fatalf("R3 POLICY CONTRACT: %s calls shared readiness policy %d times, want 1", test.function, got)
			}
		})
	}
}
