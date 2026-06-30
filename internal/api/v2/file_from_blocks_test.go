package v2

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/db"
)

// hex64 builds deterministic, distinct valid lowercase-hex SHA-256 ids for
// manifest validation tests (n keeps each block unique without colliding).
func hex64(n int) string { return fmt.Sprintf("%064x", n) }

func validBlock(n int, size int64) fileFromBlocksBlock {
	return fileFromBlocksBlock{SHA256: hex64(n), Size: size}
}

func TestManifestDigest_DependsOnSHA256AndSize(t *testing.T) {
	// The client no longer sends a SHA-1; the digest is the true content identity
	// (ordered SHA-256s + sizes). It must vary with content/size and be stable.
	base := func(sha256 string, size int64) *fileFromBlocksRequest {
		return &fileFromBlocksRequest{
			ParentDir: "/", Filename: "f.bin", Size: size,
			Blocks: []fileFromBlocksBlock{{SHA256: sha256, Size: size}},
		}
	}
	if base(hex64(1), 100).manifestDigest() == base(hex64(2), 100).manifestDigest() {
		t.Fatal("manifestDigest must differ when sha256 differs")
	}
	if base(hex64(1), 100).manifestDigest() == base(hex64(1), 101).manifestDigest() {
		t.Fatal("manifestDigest must differ when size differs")
	}
	if base(hex64(1), 100).manifestDigest() != base(hex64(1), 100).manifestDigest() {
		t.Fatal("manifestDigest must be stable for identical manifests")
	}
}

func TestValidateManifest_AcceptsBlocks(t *testing.T) {
	req := &fileFromBlocksRequest{
		Filename: "movie.mov",
		Size:     WebUploadBlockSize + 100,
		Blocks: []fileFromBlocksBlock{
			validBlock(1, WebUploadBlockSize),
			validBlock(2, 100),
		},
	}
	if err := validateManifest(req, WebUploadBlockSize); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
}

func TestValidateManifest_RejectsInvalidSHA256(t *testing.T) {
	for _, sha256 := range []string{"", "zz", strings.Repeat("a", 63), strings.Repeat("g", 64)} {
		req := &fileFromBlocksRequest{
			Filename: "f.bin",
			Size:     100,
			Blocks:   []fileFromBlocksBlock{{SHA256: sha256, Size: 100}},
		}
		if err := validateManifest(req, WebUploadBlockSize); err == nil {
			t.Fatalf("expected rejection for invalid sha256 %q", sha256)
		}
	}
}

func TestValidateManifest_RejectsConflictingSizeForSameSHA256(t *testing.T) {
	// Same content (same SHA-256) declared with two different sizes is a lie that
	// would corrupt the committed file's size/offsets; it must be rejected.
	req := &fileFromBlocksRequest{
		Filename: "f.bin",
		Size:     WebUploadBlockSize * 2,
		Blocks: []fileFromBlocksBlock{
			{SHA256: hex64(7), Size: WebUploadBlockSize},
			{SHA256: hex64(7), Size: WebUploadBlockSize - 1},
		},
	}
	if err := validateManifest(req, WebUploadBlockSize); err == nil {
		t.Fatal("expected rejection for conflicting size for same sha256")
	}
}

func TestValidateManifest_HonorsConfiguredBlockSize(t *testing.T) {
	// The non-final block size is validated against the CONFIGURED CAS block size,
	// not a hardcoded 8 MB. With a 4 MB configured size, a 4 MB non-final block is
	// valid and an 8 MB one is rejected -- and vice versa.
	const fourMB = int64(4 * 1024 * 1024)

	okReq := &fileFromBlocksRequest{
		Filename: "f.bin",
		Size:     fourMB + 100,
		Blocks: []fileFromBlocksBlock{
			validBlock(1, fourMB),
			validBlock(2, 100),
		},
	}
	if err := validateManifest(okReq, fourMB); err != nil {
		t.Fatalf("4 MB non-final block rejected under 4 MB config: %v", err)
	}

	badReq := &fileFromBlocksRequest{
		Filename: "f.bin",
		Size:     WebUploadBlockSize + 100,
		Blocks: []fileFromBlocksBlock{
			validBlock(1, WebUploadBlockSize),
			validBlock(2, 100),
		},
	}
	if err := validateManifest(badReq, fourMB); err == nil {
		t.Fatal("8 MB non-final block must be rejected under a 4 MB block-size config")
	}
}

func TestValidateManifest_AllowsRepeatedIdenticalBlocks(t *testing.T) {
	// A file may legitimately reference the same content block twice; identical
	// (sha256, size) repeats must be allowed.
	b := validBlock(3, WebUploadBlockSize)
	req := &fileFromBlocksRequest{
		Filename: "f.bin",
		Size:     WebUploadBlockSize * 2,
		Blocks:   []fileFromBlocksBlock{b, b},
	}
	if err := validateManifest(req, WebUploadBlockSize); err != nil {
		t.Fatalf("repeated identical block rejected: %v", err)
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

func TestCommittedFileIDFromSession(t *testing.T) {
	valid := strings.Repeat("a", 40)
	if got := committedFileIDFromSession(db.BlockUploadSession{ResultCommitID: valid}); got != valid {
		t.Fatalf("committedFileIDFromSession(valid) = %q, want %q", got, valid)
	}
	if got := committedFileIDFromSession(db.BlockUploadSession{ResultCommitID: strings.Repeat("b", 64)}); got != "" {
		t.Fatalf("committedFileIDFromSession(sha256) = %q, want empty", got)
	}
	if got := committedFileIDFromSession(db.BlockUploadSession{ResultCommitID: "not-a-fsid"}); got != "" {
		t.Fatalf("committedFileIDFromSession(invalid) = %q, want empty", got)
	}
}
