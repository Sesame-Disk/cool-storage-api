package api

import "testing"

func TestBuildUploadSessionIDStableAndDistinct(t *testing.T) {
	first := BuildUploadSessionID("org-1", "repo-1", "user-1", "token-1", "/docs", "file.txt")
	second := BuildUploadSessionID("org-1", "repo-1", "user-1", "token-1", "/docs", "file.txt")
	if first != second {
		t.Fatalf("BuildUploadSessionID() unstable: %q != %q", first, second)
	}

	differentFile := BuildUploadSessionID("org-1", "repo-1", "user-1", "token-1", "/docs", "other.txt")
	if first == differentFile {
		t.Fatal("expected different filenames to produce different upload IDs")
	}

	differentPath := BuildUploadSessionID("org-1", "repo-1", "user-1", "token-1", "/other", "file.txt")
	if first == differentPath {
		t.Fatal("expected different parent directories to produce different upload IDs")
	}

	if len(first) != 64 {
		t.Fatalf("BuildUploadSessionID() length = %d, want 64", len(first))
	}

	trailingSlash := BuildUploadSessionID("org-1", "repo-1", "user-1", "token-1", "/docs/", "file.txt")
	if first != trailingSlash {
		t.Fatal("expected parent dir normalization to ignore trailing slash differences")
	}

	backslashes := BuildUploadSessionID("org-1", "repo-1", "user-1", "token-1", "\\docs\\", "file.txt")
	if first != backslashes {
		t.Fatal("expected parent dir normalization to ignore path separator differences")
	}
}

func TestCassandraUploadStagingStore_RejectsMissingKeys(t *testing.T) {
	store := &CassandraUploadStagingStore{}

	if err := store.UpsertSession(UploadSessionRecord{}); err == nil {
		t.Fatal("expected missing upload id to fail")
	}

	if err := store.UpsertBlock(UploadSessionBlockRecord{UploadID: "upload-1"}); err == nil {
		t.Fatal("expected missing block sha256 to fail")
	}
}
