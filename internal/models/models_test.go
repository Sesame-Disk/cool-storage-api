package models

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestLibraryJSONSerialization(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	lib := Library{
		LibraryID:      uuid.New(),
		OrgID:          uuid.New(),
		OwnerID:        uuid.New(),
		Name:           "Test Library",
		Description:    "A test library",
		Encrypted:      false,
		StorageClass:   "hot",
		SizeBytes:      1024,
		FileCount:      10,
		VersionTTLDays: 90,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	// Marshal to JSON
	data, err := json.Marshal(lib)
	if err != nil {
		t.Fatalf("Failed to marshal Library: %v", err)
	}

	// Verify JSON field names
	var jsonMap map[string]interface{}
	if err := json.Unmarshal(data, &jsonMap); err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	expectedFields := []string{"id", "org_id", "owner_id", "name", "encrypted", "storage_class", "size", "file_count", "version_ttl_days", "created_at", "updated_at"}
	for _, field := range expectedFields {
		if _, ok := jsonMap[field]; !ok {
			t.Errorf("Expected field %q not found in JSON", field)
		}
	}

	// Description should be present when not empty
	if _, ok := jsonMap["description"]; !ok {
		t.Error("description should be present when not empty")
	}
}

func TestUserJSONSerialization(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	user := User{
		UserID:     uuid.New(),
		OrgID:      uuid.New(),
		Email:      "test@example.com",
		Name:       "Test User",
		Role:       "admin",
		QuotaBytes: 1024 * 1024 * 1024,
		UsedBytes:  512 * 1024 * 1024,
		CreatedAt:  now,
	}

	data, err := json.Marshal(user)
	if err != nil {
		t.Fatalf("Failed to marshal User: %v", err)
	}

	var jsonMap map[string]interface{}
	if err := json.Unmarshal(data, &jsonMap); err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	if jsonMap["email"] != "test@example.com" {
		t.Errorf("email = %v, want test@example.com", jsonMap["email"])
	}
	if jsonMap["role"] != "admin" {
		t.Errorf("role = %v, want admin", jsonMap["role"])
	}

	// OIDCSub should not be in JSON (json:"-")
	if _, ok := jsonMap["oidc_sub"]; ok {
		t.Error("oidc_sub should not be in JSON")
	}
}

func TestShareLinkJSONSerialization(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	expires := now.Add(7 * 24 * time.Hour)
	maxDownloads := 100

	link := ShareLink{
		Token:         "abc123",
		OrgID:         uuid.New(),
		LibraryID:     uuid.New(),
		Path:          "/documents/file.pdf",
		CreatedBy:     uuid.New(),
		Permission:    "download",
		PasswordHash:  "secret",
		ExpiresAt:     &expires,
		DownloadCount: 5,
		MaxDownloads:  &maxDownloads,
		CreatedAt:     now,
	}

	data, err := json.Marshal(link)
	if err != nil {
		t.Fatalf("Failed to marshal ShareLink: %v", err)
	}

	var jsonMap map[string]interface{}
	if err := json.Unmarshal(data, &jsonMap); err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	if jsonMap["token"] != "abc123" {
		t.Errorf("token = %v, want abc123", jsonMap["token"])
	}
	if jsonMap["permission"] != "download" {
		t.Errorf("permission = %v, want download", jsonMap["permission"])
	}

	// PasswordHash should NOT be in JSON (json:"-")
	if _, ok := jsonMap["password_hash"]; ok {
		t.Error("password_hash should not be in JSON")
	}
}

func TestRestoreJobJSONSerialization(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	completed := now.Add(2 * time.Hour)
	expires := now.Add(24 * time.Hour)

	job := RestoreJob{
		JobID:        uuid.New(),
		OrgID:        uuid.New(),
		LibraryID:    uuid.New(),
		BlockIDs:     []string{"block1", "block2", "block3"},
		GlacierJobID: "glacier-job-123",
		Status:       "completed",
		RequestedAt:  now,
		CompletedAt:  &completed,
		ExpiresAt:    &expires,
	}

	data, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("Failed to marshal RestoreJob: %v", err)
	}

	var jsonMap map[string]interface{}
	if err := json.Unmarshal(data, &jsonMap); err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	if jsonMap["status"] != "completed" {
		t.Errorf("status = %v, want completed", jsonMap["status"])
	}
	if jsonMap["glacier_job_id"] != "glacier-job-123" {
		t.Errorf("glacier_job_id = %v, want glacier-job-123", jsonMap["glacier_job_id"])
	}

	blockIDs, ok := jsonMap["block_ids"].([]interface{})
	if !ok || len(blockIDs) != 3 {
		t.Errorf("block_ids length = %d, want 3", len(blockIDs))
	}
}

func TestFileInfoJSONSerialization(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	file := FileInfo{
		Type:       "file",
		Name:       "document.pdf",
		Path:       "/documents/document.pdf",
		MTime:      now,
		Permission: "rw",
		Size:       1024,
		Starred:    false,
	}

	data, err := json.Marshal(file)
	if err != nil {
		t.Fatalf("Failed to marshal FileInfo: %v", err)
	}

	var jsonMap map[string]interface{}
	if err := json.Unmarshal(data, &jsonMap); err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	if jsonMap["type"] != "file" {
		t.Errorf("type = %v, want file", jsonMap["type"])
	}
	if jsonMap["name"] != "document.pdf" {
		t.Errorf("name = %v, want document.pdf", jsonMap["name"])
	}
}

func TestDownloadLinkJSONSerialization(t *testing.T) {
	expires := time.Now().Add(1 * time.Hour)
	link := DownloadLink{
		URL:       "https://example.com/download/abc123",
		ExpiresAt: expires,
	}

	data, err := json.Marshal(link)
	if err != nil {
		t.Fatalf("Failed to marshal DownloadLink: %v", err)
	}

	var jsonMap map[string]interface{}
	if err := json.Unmarshal(data, &jsonMap); err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	if jsonMap["url"] != "https://example.com/download/abc123" {
		t.Errorf("url = %v, want https://example.com/download/abc123", jsonMap["url"])
	}
}

func TestUploadLinkJSONSerialization(t *testing.T) {
	expires := time.Now().Add(1 * time.Hour)
	link := UploadLink{
		URL:       "https://example.com/upload/abc123",
		Token:     "upload-token-123",
		ExpiresAt: expires,
	}

	data, err := json.Marshal(link)
	if err != nil {
		t.Fatalf("Failed to marshal UploadLink: %v", err)
	}

	var jsonMap map[string]interface{}
	if err := json.Unmarshal(data, &jsonMap); err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	if jsonMap["url"] != "https://example.com/upload/abc123" {
		t.Errorf("url = %v, want https://example.com/upload/abc123", jsonMap["url"])
	}
	if jsonMap["token"] != "upload-token-123" {
		t.Errorf("token = %v, want upload-token-123", jsonMap["token"])
	}
}

func TestOrganizationJSONSerialization(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	org := Organization{
		OrgID:        uuid.New(),
		Name:         "Test Organization",
		Settings:     map[string]string{"theme": "dark"},
		StorageQuota: 1024 * 1024 * 1024 * 1024, // 1TB
		StorageUsed:  512 * 1024 * 1024 * 1024,  // 512GB
		CreatedAt:    now,
	}

	data, err := json.Marshal(org)
	if err != nil {
		t.Fatalf("Failed to marshal Organization: %v", err)
	}

	var jsonMap map[string]interface{}
	if err := json.Unmarshal(data, &jsonMap); err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	if jsonMap["name"] != "Test Organization" {
		t.Errorf("name = %v, want Test Organization", jsonMap["name"])
	}

	// ChunkingPolynomial should not be in JSON (json:"-")
	if _, ok := jsonMap["chunking_polynomial"]; ok {
		t.Error("chunking_polynomial should not be in JSON")
	}
}

func TestFSObjectJSONSerialization(t *testing.T) {
	obj := FSObject{
		LibraryID: uuid.New(),
		FSID:      "abc123def456",
		Type:      "file",
		Name:      "test.txt",
		BlockIDs:  []string{"block1", "block2"},
		SizeBytes: 2048,
		MTime:     time.Now().Unix(),
	}

	data, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("Failed to marshal FSObject: %v", err)
	}

	var jsonMap map[string]interface{}
	if err := json.Unmarshal(data, &jsonMap); err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	if jsonMap["fs_id"] != "abc123def456" {
		t.Errorf("fs_id = %v, want abc123def456", jsonMap["fs_id"])
	}
	if jsonMap["type"] != "file" {
		t.Errorf("type = %v, want file", jsonMap["type"])
	}
}

func TestBlockJSONSerialization(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	block := Block{
		OrgID:        uuid.New(),
		BlockID:      "sha256hash",
		SizeBytes:    1024,
		StorageClass: "hot",
		StorageKey:   "org123/sha256hash",
		RefCount:     3,
		CreatedAt:    now,
		LastAccessed: now,
	}

	data, err := json.Marshal(block)
	if err != nil {
		t.Fatalf("Failed to marshal Block: %v", err)
	}

	var jsonMap map[string]interface{}
	if err := json.Unmarshal(data, &jsonMap); err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	if jsonMap["block_id"] != "sha256hash" {
		t.Errorf("block_id = %v, want sha256hash", jsonMap["block_id"])
	}
	if jsonMap["storage_class"] != "hot" {
		t.Errorf("storage_class = %v, want hot", jsonMap["storage_class"])
	}
}
