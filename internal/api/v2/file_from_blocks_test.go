package v2

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/db"
)

// hex40/hex64 build deterministic, distinct valid lowercase-hex IDs for manifest
// validation tests (n keeps each block's pair unique without colliding).
func hex40(n int) string { return fmt.Sprintf("%040x", n) }
func hex64(n int) string { return fmt.Sprintf("%064x", n) }

func validDualBlock(n int, size int64) fileFromBlocksBlock {
	return fileFromBlocksBlock{SHA1: hex40(n), SHA256: hex64(n), Size: size}
}

func TestManifestDigest_DependsOnSHA1(t *testing.T) {
	// Two manifests with identical SHA-256/size but different SHA-1 must produce
	// different digests: the fs_object id derives from SHA-1, so they are different
	// files and a retry of one must not idempotently replay the other.
	base := func(sha1 string) *fileFromBlocksRequest {
		return &fileFromBlocksRequest{
			ParentDir: "/", Filename: "f.bin", Size: 100,
			Blocks: []fileFromBlocksBlock{{SHA1: sha1, SHA256: hex64(1), Size: 100}},
		}
	}
	if base(hex40(1)).manifestDigest() == base(hex40(2)).manifestDigest() {
		t.Fatal("manifestDigest must differ when only sha1 differs")
	}
	// Same everything → same digest (stable for honest idempotent replay).
	if base(hex40(1)).manifestDigest() != base(hex40(1)).manifestDigest() {
		t.Fatal("manifestDigest must be stable for identical manifests")
	}
}

func TestValidateManifest_AcceptsDualHashBlocks(t *testing.T) {
	req := &fileFromBlocksRequest{
		Filename: "movie.mov",
		Size:     WebUploadBlockSize + 100,
		Blocks: []fileFromBlocksBlock{
			validDualBlock(1, WebUploadBlockSize),
			validDualBlock(2, 100),
		},
	}
	if err := validateManifest(req); err != nil {
		t.Fatalf("valid dual-hash manifest rejected: %v", err)
	}
}

func TestValidateManifest_RejectsMissingOrInvalidSHA1(t *testing.T) {
	for _, sha1 := range []string{"", "zz", strings.Repeat("a", 39), strings.Repeat("g", 40)} {
		req := &fileFromBlocksRequest{
			Filename: "f.bin",
			Size:     100,
			Blocks:   []fileFromBlocksBlock{{SHA1: sha1, SHA256: hex64(1), Size: 100}},
		}
		if err := validateManifest(req); err == nil {
			t.Fatalf("expected rejection for invalid sha1 %q", sha1)
		}
	}
}

func TestValidateManifest_RejectsConflictingSHA1ForSameSHA256(t *testing.T) {
	// Same content (same SHA-256) declared with two different SHA-1s is a lie that
	// would corrupt the SHA-1→SHA-256 mapping; it must be rejected.
	req := &fileFromBlocksRequest{
		Filename: "f.bin",
		Size:     WebUploadBlockSize + WebUploadBlockSize,
		Blocks: []fileFromBlocksBlock{
			{SHA1: hex40(1), SHA256: hex64(7), Size: WebUploadBlockSize},
			{SHA1: hex40(2), SHA256: hex64(7), Size: WebUploadBlockSize},
		},
	}
	if err := validateManifest(req); err == nil {
		t.Fatal("expected rejection for conflicting sha1 for same sha256")
	}
}

func TestValidateManifest_RejectsConflictingSHA256ForSameSHA1(t *testing.T) {
	req := &fileFromBlocksRequest{
		Filename: "f.bin",
		Size:     WebUploadBlockSize + WebUploadBlockSize,
		Blocks: []fileFromBlocksBlock{
			{SHA1: hex40(9), SHA256: hex64(1), Size: WebUploadBlockSize},
			{SHA1: hex40(9), SHA256: hex64(2), Size: WebUploadBlockSize},
		},
	}
	if err := validateManifest(req); err == nil {
		t.Fatal("expected rejection for conflicting sha256 for same sha1")
	}
}

func TestValidateManifest_AllowsRepeatedIdenticalDualBlocks(t *testing.T) {
	// A file may legitimately reference the same content block twice; identical
	// (sha1, sha256, size) repeats must be allowed.
	b := validDualBlock(3, WebUploadBlockSize)
	req := &fileFromBlocksRequest{
		Filename: "f.bin",
		Size:     WebUploadBlockSize * 2,
		Blocks:   []fileFromBlocksBlock{b, b},
	}
	if err := validateManifest(req); err != nil {
		t.Fatalf("repeated identical dual block rejected: %v", err)
	}
}

func TestClassifyBlockUploadCommitConflict_ReturnsPublishedResultForMatchingDigest(t *testing.T) {
	session := db.BlockUploadSession{
		ManifestDigest: "digest-a",
		ResultFilename: "published.txt",
	}

	resultName, errorCode, errorMessage := classifyBlockUploadCommitConflict(session, true, "digest-a")
	if resultName != "published.txt" {
		t.Fatalf("resultName = %q, want published.txt", resultName)
	}
	if errorCode != "" || errorMessage != "" {
		t.Fatalf("unexpected error classification: code=%q message=%q", errorCode, errorMessage)
	}
}

func TestClassifyBlockUploadCommitConflict_DetectsDifferentCommittedFile(t *testing.T) {
	session := db.BlockUploadSession{
		ManifestDigest: "digest-a",
		ResultFilename: "other.txt",
	}

	resultName, errorCode, errorMessage := classifyBlockUploadCommitConflict(session, true, "digest-b")
	if resultName != "" {
		t.Fatalf("resultName = %q, want empty", resultName)
	}
	if errorCode != blockUploadCommittedDifferentFileConflictCode {
		t.Fatalf("errorCode = %q, want %q", errorCode, blockUploadCommittedDifferentFileConflictCode)
	}
	if errorMessage != "session already committed a different file" {
		t.Fatalf("errorMessage = %q, want permanent different-file conflict", errorMessage)
	}
}

func TestClassifyBlockUploadCommitConflict_TreatsMissingResultAsInProgress(t *testing.T) {
	session := db.BlockUploadSession{
		ManifestDigest: "digest-a",
	}

	resultName, errorCode, errorMessage := classifyBlockUploadCommitConflict(session, true, "digest-a")
	if resultName != "" {
		t.Fatalf("resultName = %q, want empty", resultName)
	}
	if errorCode != blockUploadCommitInProgressCode {
		t.Fatalf("errorCode = %q, want %q", errorCode, blockUploadCommitInProgressCode)
	}
	if errorMessage != "commit still in progress; retry" {
		t.Fatalf("errorMessage = %q, want retryable in-progress conflict", errorMessage)
	}
}

func TestClassifyBlockUploadCommitConflict_TreatsMissingSessionStateAsInProgress(t *testing.T) {
	resultName, errorCode, errorMessage := classifyBlockUploadCommitConflict(db.BlockUploadSession{}, false, "digest-a")
	if resultName != "" {
		t.Fatalf("resultName = %q, want empty", resultName)
	}
	if errorCode != blockUploadCommitInProgressCode {
		t.Fatalf("errorCode = %q, want %q", errorCode, blockUploadCommitInProgressCode)
	}
	if errorMessage != "commit still in progress; retry" {
		t.Fatalf("errorMessage = %q, want retryable in-progress conflict", errorMessage)
	}
}
