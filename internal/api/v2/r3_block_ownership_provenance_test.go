package v2

import (
	"go/ast"
	"go/token"
	"sort"
	"strings"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/db"
)

func TestR3BlockOwnershipProvenanceMatrix(t *testing.T) {
	ownReferrer := db.BlockReferrerForUpload("session-1")
	foreignUploadReferrer := db.BlockReferrerForUpload("session-2")
	foreignPublishReferrer := db.BlockReferrerForPublishAttempt("attempt-1")
	borrowedReferrer := db.BlockReferrerForFSObject("library-1", "fs-1")
	secondBorrowedReferrer := db.BlockReferrerForFSObject("library-2", "fs-2")

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
			name:      "borrowed fs only",
			referrers: []string{borrowedReferrer},
			want:      blockCommitLivenessBorrowedFS,
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
			name:      "borrowed fs before own session pin",
			referrers: []string{borrowedReferrer, ownReferrer},
			want:      blockCommitLivenessSessionUpload,
		},
		{
			name:      "own session pin before borrowed fs",
			referrers: []string{ownReferrer, borrowedReferrer},
			want:      blockCommitLivenessSessionUpload,
		},
		{
			name:      "multiple borrowed fs",
			referrers: []string{borrowedReferrer, secondBorrowedReferrer},
			want:      blockCommitLivenessBorrowedFS,
		},
		{
			name:      "session referrer prefix collision is not own session",
			referrers: []string{ownReferrer + ":suffix"},
			want:      blockCommitLivenessNone,
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

func TestR3BlockOwnershipProvenanceDistinguishesBorrowedFS(t *testing.T) {
	ownReferrer := db.BlockReferrerForUpload("session-1")
	borrowedReferrer := db.BlockReferrerForFSObject("library-1", "fs-1")
	got := classifyBlockReferrerProvenance([]string{borrowedReferrer}, ownReferrer)
	if got != blockCommitLivenessBorrowedFS {
		t.Fatalf("foreign fs must be classified as borrowed-fs: got %d, want %d", got, blockCommitLivenessBorrowedFS)
	}
}

func TestR3CurrentCommitReadinessPolicyPreservesForeignFSBehavior(t *testing.T) {
	tests := []struct {
		provenance blockCommitLivenessProvenance
		wantReady  bool
	}{
		{provenance: blockCommitLivenessSessionUpload, wantReady: true},
		{provenance: blockCommitLivenessBorrowedFS, wantReady: true},
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
	allowed := map[string]int{
		"ListBlockReferrers":              1,
		"classifyBlockReferrerProvenance": 1,
	}
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if r3CallName(call) == "" {
			t.Fatal("R3 OWNERSHIP READ CONTRACT: unresolved call in classifyBlockOwnership")
		}
		return true
	})
	calls := r3CallPositions(fn)
	var unlisted []string
	for name := range calls {
		if _, ok := allowed[name]; !ok {
			unlisted = append(unlisted, name)
		}
	}
	sort.Strings(unlisted)
	if len(unlisted) > 0 {
		t.Fatalf("R3 OWNERSHIP READ CONTRACT: unlisted call %s in classifyBlockOwnership", strings.Join(unlisted, ", "))
	}
	for name, want := range allowed {
		if got := len(calls[name]); got != want {
			t.Fatalf("R3 OWNERSHIP READ CONTRACT: %s calls = %d, want %d", name, got, want)
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
