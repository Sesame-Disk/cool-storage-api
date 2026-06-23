package v2

import (
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/db"
)

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
