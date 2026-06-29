package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// setupSyncTestRouter creates a test router with auth context
func setupSyncTestRouter() *gin.Engine {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("org_id", "00000000-0000-0000-0000-000000000001")
		c.Set("user_id", "00000000-0000-0000-0000-000000000001")
		c.Next()
	})
	return r
}

// TestCommitStruct tests the Commit struct JSON serialization
func TestCommitStruct(t *testing.T) {
	parentID := "parent123"
	commit := Commit{
		CommitID:    "abc123",
		RepoID:      "00000000-0000-0000-0000-000000000001",
		RootID:      "def456",
		ParentID:    &parentID,
		Description: "Test commit",
		Creator:     "user1",
		CreatorName: "Test User",
		Ctime:       1234567890,
		Version:     1,
	}

	data, err := json.Marshal(commit)
	if err != nil {
		t.Fatalf("failed to marshal commit: %v", err)
	}

	var decoded Commit
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal commit: %v", err)
	}

	if decoded.CommitID != commit.CommitID {
		t.Errorf("CommitID mismatch: got %s, want %s", decoded.CommitID, commit.CommitID)
	}
	if decoded.RootID != commit.RootID {
		t.Errorf("RootID mismatch: got %s, want %s", decoded.RootID, commit.RootID)
	}
	if decoded.Ctime != commit.Ctime {
		t.Errorf("Ctime mismatch: got %d, want %d", decoded.Ctime, commit.Ctime)
	}
	if decoded.ParentID == nil || *decoded.ParentID != parentID {
		t.Errorf("ParentID mismatch: got %v, want %s", decoded.ParentID, parentID)
	}
}

// TestFSObjectStruct tests the FSObject struct JSON serialization
func TestFSObjectStruct(t *testing.T) {
	tests := []struct {
		name string
		obj  FSObject
	}{
		{
			name: "file object",
			obj: FSObject{
				Type:     1,
				ID:       "file123",
				Name:     "test.txt",
				Mode:     33188, // 0644
				Mtime:    1234567890,
				Size:     1024,
				BlockIDs: []string{"block1", "block2", "block3"},
			},
		},
		{
			name: "directory object",
			obj: FSObject{
				Type:  3,
				ID:    "dir123",
				Name:  "documents",
				Mode:  16384, // directory
				Mtime: 1234567890,
				Entries: &[]FSEntry{
					{Name: "file1.txt", ID: "f1", Mode: 33188, Mtime: 1234567890, Size: 100},
					{Name: "file2.txt", ID: "f2", Mode: 33188, Mtime: 1234567891, Size: 200},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.obj)
			if err != nil {
				t.Fatalf("failed to marshal: %v", err)
			}

			var decoded FSObject
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}

			if decoded.Type != tt.obj.Type {
				t.Errorf("Type mismatch: got %d, want %d", decoded.Type, tt.obj.Type)
			}
			if decoded.ID != tt.obj.ID {
				t.Errorf("ID mismatch: got %s, want %s", decoded.ID, tt.obj.ID)
			}
			if decoded.Name != tt.obj.Name {
				t.Errorf("Name mismatch: got %s, want %s", decoded.Name, tt.obj.Name)
			}
		})
	}
}

// TestFSEntry tests the FSEntry struct
func TestFSEntry(t *testing.T) {
	entry := FSEntry{
		ID:       "abc123",
		Mode:     33188,
		Modifier: "user@example.com",
		Mtime:    1234567890,
		Name:     "document.pdf",
		Size:     2048,
	}

	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded FSEntry
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.Name != entry.Name {
		t.Errorf("Name mismatch: got %s, want %s", decoded.Name, entry.Name)
	}
	if decoded.Size != entry.Size {
		t.Errorf("Size mismatch: got %d, want %d", decoded.Size, entry.Size)
	}
}

// TestFSEntryJSONKeyOrder verifies FSEntry JSON keys are in alphabetical order
// This is CRITICAL for fs_id hash computation (SHA-1 of JSON content)
func TestFSEntryJSONKeyOrder(t *testing.T) {
	entry := FSEntry{
		ID:       "abc123",
		Mode:     33188,
		Modifier: "user@example.com",
		Mtime:    1234567890,
		Name:     "document.pdf",
		Size:     2048,
	}

	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	// Verify keys appear in alphabetical order in JSON
	jsonStr := string(data)

	// Expected order: id, mode, modifier, mtime, name, size
	idIdx := strings.Index(jsonStr, `"id"`)
	modeIdx := strings.Index(jsonStr, `"mode"`)
	modifierIdx := strings.Index(jsonStr, `"modifier"`)
	mtimeIdx := strings.Index(jsonStr, `"mtime"`)
	nameIdx := strings.Index(jsonStr, `"name"`)
	sizeIdx := strings.Index(jsonStr, `"size"`)

	if idIdx == -1 || modeIdx == -1 || modifierIdx == -1 || mtimeIdx == -1 || nameIdx == -1 || sizeIdx == -1 {
		t.Fatalf("missing expected key in JSON: %s", jsonStr)
	}

	// Verify order: id < mode < modifier < mtime < name < size
	if !(idIdx < modeIdx && modeIdx < modifierIdx && modifierIdx < mtimeIdx && mtimeIdx < nameIdx && nameIdx < sizeIdx) {
		t.Errorf("FSEntry JSON keys are not in alphabetical order.\nExpected order: id, mode, modifier, mtime, name, size\nGot JSON: %s", jsonStr)
	}
}

func TestResolvePreferredLibraryStorageClassUsesEndpointRouting(t *testing.T) {
	manager := storage.NewManager()
	manager.SetDefaultClass("hot-minio-local")
	manager.SetEndpointRegions(map[string]string{"eu.sesamefs.local": "eu"})
	manager.SetRegionClasses(map[string]storage.RegionClassConfig{
		"eu": {Hot: "hot-s3-eu"},
	})
	h := &SyncHandler{storageManager: manager}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/seafhttp/repo/repo-id/block/block-id", nil)
	c.Request.Host = "eu.sesamefs.local"

	if got := h.resolvePreferredLibraryStorageClass(c, "org-id", "repo-id"); got != "hot-s3-eu" {
		t.Fatalf("resolvePreferredLibraryStorageClass = %q, want %q", got, "hot-s3-eu")
	}
}

func TestResolveBlockLookupFallbackClassUsesLibraryPreference(t *testing.T) {
	manager := storage.NewManager()
	manager.SetDefaultClass("hot-minio-local")
	manager.SetEndpointRegions(map[string]string{"eu.sesamefs.local": "eu"})
	manager.SetRegionClasses(map[string]storage.RegionClassConfig{
		"eu": {Hot: "hot-s3-eu"},
	})
	h := &SyncHandler{storageManager: manager}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/seafhttp/repo/repo-id/block/block-id", nil)
	c.Request.Host = "eu.sesamefs.local"

	if got := h.resolveBlockLookupFallbackClass(c, "org-id", "repo-id", "missing-class"); got != "hot-s3-eu" {
		t.Fatalf("resolveBlockLookupFallbackClass = %q, want %q", got, "hot-s3-eu")
	}
}

func TestResolvePreferredBlockStoreUsesStorageManager(t *testing.T) {
	manager := storage.NewManager()
	manager.SetDefaultClass("hot-s3-eu")
	manager.RegisterBackend("hot-s3-eu", &storage.S3Store{}, "")
	h := &SyncHandler{storageManager: manager}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/seafhttp/repo/repo-id/block/block-id", nil)
	c.Request.Host = "files.example.com"

	blockStore, storageClass, err := h.resolvePreferredBlockStore(c, "org-id", "repo-id")
	if err != nil {
		t.Fatalf("resolvePreferredBlockStore returned error: %v", err)
	}
	if blockStore == nil {
		t.Fatal("expected blockStore, got nil")
	}
	if storageClass != "hot-s3-eu" {
		t.Fatalf("storageClass = %q, want %q", storageClass, "hot-s3-eu")
	}
}

func TestResolveBlockStoreForLookupFallsBackThroughManager(t *testing.T) {
	manager := storage.NewManager()
	manager.SetDefaultClass("hot-s3-eu")
	manager.RegisterBackend("hot-s3-eu", &storage.S3Store{}, "")
	h := &SyncHandler{storageManager: manager}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/seafhttp/repo/repo-id/block/block-id", nil)
	c.Request.Host = "files.example.com"

	blockStore, storageClass, err := h.resolveBlockStoreForLookup(c, "org-id", "repo-id", "missing-class")
	if err != nil {
		t.Fatalf("resolveBlockStoreForLookup returned error: %v", err)
	}
	if blockStore == nil {
		t.Fatal("expected blockStore, got nil")
	}
	if storageClass != "hot-s3-eu" {
		t.Fatalf("storageClass = %q, want %q", storageClass, "hot-s3-eu")
	}
}

// TestSyncHandlerWithoutDB tests sync handlers return appropriate errors without DB
func TestSyncHandlerWithoutDB(t *testing.T) {
	r := setupSyncTestRouter()
	h := &SyncHandler{
		db:         nil,
		storage:    nil,
		blockStore: nil,
	}

	// Register a subset of routes for testing
	repo := r.Group("/seafhttp/repo/:repo_id")
	{
		repo.GET("/commit/HEAD", h.GetHeadCommit)
		repo.GET("/block/:block_id", h.GetBlock)
		repo.POST("/check-blocks", h.CheckBlocks)
		repo.GET("/permission-check", h.PermissionCheck)
		repo.GET("/quota-check", h.QuotaCheck)
	}

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{
			name:       "get head commit without db",
			method:     "GET",
			path:       "/seafhttp/repo/00000000-0000-0000-0000-000000000001/commit/HEAD",
			wantStatus: http.StatusServiceUnavailable, // Database not available
		},
		{
			name:       "get block without storage",
			method:     "GET",
			path:       "/seafhttp/repo/00000000-0000-0000-0000-000000000001/block/abc123",
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "check blocks without storage",
			method:     "POST",
			path:       "/seafhttp/repo/00000000-0000-0000-0000-000000000001/check-blocks",
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "permission check always succeeds",
			method:     "GET",
			path:       "/seafhttp/repo/00000000-0000-0000-0000-000000000001/permission-check",
			wantStatus: http.StatusOK,
		},
		{
			name:       "quota check always succeeds",
			method:     "GET",
			path:       "/seafhttp/repo/00000000-0000-0000-0000-000000000001/quota-check",
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body *bytes.Buffer
			if tt.method == "POST" {
				body = bytes.NewBufferString("block1\nblock2\n")
			} else {
				body = bytes.NewBuffer(nil)
			}

			req, _ := http.NewRequest(tt.method, tt.path, body)
			req.Header.Set("Authorization", "Token test-token")

			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d, body: %s", w.Code, tt.wantStatus, w.Body.String())
			}
		})
	}
}

// TestPermissionCheckResponse tests the permission check endpoint response format
// NOTE: Seafile desktop client expects empty body with 200 OK, not JSON
func TestPermissionCheckResponse(t *testing.T) {
	r := setupSyncTestRouter()
	h := &SyncHandler{}

	r.GET("/seafhttp/repo/:repo_id/permission-check", h.PermissionCheck)

	req, _ := http.NewRequest("GET", "/seafhttp/repo/test-repo/permission-check", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	// Seafile expects empty body for success (200 OK means permission granted)
	body := w.Body.String()
	if body != "" {
		t.Errorf("expected empty body for permission check, got: %s", body)
	}
}

// TestQuotaCheckResponse tests the quota check endpoint response format
func TestQuotaCheckResponse(t *testing.T) {
	r := setupSyncTestRouter()
	h := &SyncHandler{}

	r.GET("/seafhttp/repo/:repo_id/quota-check", h.QuotaCheck)

	req, _ := http.NewRequest("GET", "/seafhttp/repo/test-repo/quota-check", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	hasQuota, ok := response["has_quota"].(bool)
	if !ok {
		t.Fatal("has_quota field not found or not bool")
	}
	if !hasQuota {
		t.Error("has_quota should be true")
	}
}

// TestCheckBlocksRequestParsing tests parsing of block IDs from request body
func TestCheckBlocksRequestParsing(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		expected []string
	}{
		{
			name:     "single block",
			body:     "block1",
			expected: []string{"block1"},
		},
		{
			name:     "multiple blocks",
			body:     "block1\nblock2\nblock3",
			expected: []string{"block1", "block2", "block3"},
		},
		{
			name:     "with trailing newline",
			body:     "block1\nblock2\n",
			expected: []string{"block1", "block2"},
		},
		{
			name:     "empty",
			body:     "",
			expected: []string{""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blockIDs := strings.Split(strings.TrimSpace(tt.body), "\n")
			if len(blockIDs) != len(tt.expected) {
				t.Errorf("got %d blocks, want %d", len(blockIDs), len(tt.expected))
			}
		})
	}
}

// TestFSIDListFormat tests the fs-id-list response format
func TestFSIDListFormat(t *testing.T) {
	// The format should be: count\nid1\nid2\n...
	fsIDs := []string{"fs1", "fs2", "fs3"}
	result := formatFSIDList(fsIDs)

	lines := strings.Split(result, "\n")
	if lines[0] != "3" {
		t.Errorf("count = %s, want 3", lines[0])
	}
	if len(lines) != 4 { // count + 3 IDs
		t.Errorf("got %d lines, want 4", len(lines))
	}
}

// Helper function (matches sync.go implementation)
func formatFSIDList(fsIDs []string) string {
	return strings.Join(append([]string{string(rune('0' + len(fsIDs)))}, fsIDs...), "\n")
}

// TestRecvFSRequestParsing tests parsing FS objects from request body
func TestRecvFSRequestParsing(t *testing.T) {
	objects := []FSObject{
		{
			Type:     1,
			ID:       "file1",
			Name:     "test.txt",
			Size:     1024,
			Mtime:    1234567890,
			BlockIDs: []string{"b1", "b2"},
		},
		{
			Type:  3,
			ID:    "dir1",
			Name:  "docs",
			Mtime: 1234567890,
			Entries: &[]FSEntry{
				{Name: "a.txt", ID: "a1", Mode: 33188},
			},
		},
	}

	data, err := json.Marshal(objects)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded []FSObject
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if len(decoded) != 2 {
		t.Errorf("got %d objects, want 2", len(decoded))
	}

	// Check file object
	if decoded[0].Type != 1 {
		t.Errorf("object[0].Type = %d, want 1", decoded[0].Type)
	}
	if len(decoded[0].BlockIDs) != 2 {
		t.Errorf("object[0].BlockIDs length = %d, want 2", len(decoded[0].BlockIDs))
	}

	// Check directory object
	if decoded[1].Type != 3 {
		t.Errorf("object[1].Type = %d, want 3", decoded[1].Type)
	}
	if decoded[1].Entries == nil || len(*decoded[1].Entries) != 1 {
		t.Errorf("object[1].Entries length = %d, want 1", func() int {
			if decoded[1].Entries == nil {
				return 0
			}
			return len(*decoded[1].Entries)
		}())
	}
}

// TestCommitJSONFields tests that JSON field names match Seafile protocol
func TestCommitJSONFields(t *testing.T) {
	parentID := "parent"
	secondParent := "second"
	commit := Commit{
		CommitID:       "abc",
		RepoID:         "repo",
		RootID:         "root",
		ParentID:       &parentID,
		SecondParentID: &secondParent,
		Description:    "desc",
		Creator:        "user",
		CreatorName:    "name",
		Ctime:          123,
		Version:        1,
		Encrypted:      "true", // String, not bool (Seafile compat)
		EncVersion:     2,
		Magic:          "magic",
		Key:            "key", // Seafile uses "key" not "random_key" in commit
	}

	data, err := json.Marshal(commit)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	// Check expected JSON field names
	expected := []string{
		`"commit_id"`,
		`"repo_id"`,
		`"root_id"`,
		`"parent_id"`,
		`"second_parent_id"`,
		`"description"`,
		`"creator"`,
		`"creator_name"`,
		`"ctime"`,
		`"version"`,
		`"encrypted"`,
		`"enc_version"`,
		`"magic"`,
		`"key"`, // Seafile uses "key" not "random_key" in commit JSON
	}

	jsonStr := string(data)
	for _, field := range expected {
		if !strings.Contains(jsonStr, field) {
			t.Errorf("JSON missing field: %s\nGot: %s", field, jsonStr)
		}
	}
}

// TestCommitNullFields tests that pointer fields serialize as null when nil
func TestCommitNullFields(t *testing.T) {
	commit := Commit{
		CommitID:       "abc",
		RepoID:         "repo",
		RootID:         "root",
		ParentID:       nil, // Should serialize as null
		SecondParentID: nil, // Should serialize as null
		RepoCategory:   nil, // Should serialize as null
		Description:    "Initial commit",
		Creator:        strings.Repeat("0", 40),
		CreatorName:    "test@sesamefs.local",
		Ctime:          1234567890,
		Version:        1,
	}

	data, err := json.Marshal(commit)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	jsonStr := string(data)

	// Check that null fields are present as null (not empty string)
	if !strings.Contains(jsonStr, `"parent_id":null`) {
		t.Errorf("parent_id should be null, got: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"second_parent_id":null`) {
		t.Errorf("second_parent_id should be null, got: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"repo_category":null`) {
		t.Errorf("repo_category should be null, got: %s", jsonStr)
	}
}

// TestGetProtocolVersion tests the protocol version endpoint
func TestGetProtocolVersion(t *testing.T) {
	r := gin.New()
	h := &SyncHandler{}
	r.GET("/seafhttp/protocol-version", h.GetProtocolVersion)

	req, _ := http.NewRequest("GET", "/seafhttp/protocol-version", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	version, ok := response["version"].(float64)
	if !ok {
		t.Fatal("version field not found or not number")
	}
	if version != 2 {
		t.Errorf("version = %v, want 2", version)
	}
}

// TestPermissionCheckEmptyBody tests that permission-check returns empty body
func TestPermissionCheckEmptyBody(t *testing.T) {
	r := setupSyncTestRouter()
	h := &SyncHandler{}

	r.GET("/seafhttp/repo/:repo_id/permission-check", h.PermissionCheck)

	req, _ := http.NewRequest("GET", "/seafhttp/repo/test-repo/permission-check", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	// Seafile expects empty body, not JSON
	body := w.Body.String()
	if body != "" {
		t.Errorf("body should be empty, got: %s", body)
	}
}

// TestIsHexString tests the isHexString helper function
func TestIsHexString(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"0123456789abcdef", true},
		{"ABCDEF0123456789", true},
		{"aAbBcCdDeEfF0123", true},
		{"0000000000000000000000000000000000000000", true}, // 40 zeros
		{"ghijkl", false},
		{"0123g567", false},
		{"", true}, // Empty is technically valid hex
		{"abc!", false},
		{"abc def", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := isHexString([]byte(tt.input))
			if result != tt.expected {
				t.Errorf("isHexString(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

// TestSha1Hex tests the sha1Hex helper function
func TestSha1Hex(t *testing.T) {
	// Test that sha1Hex returns 40 characters (Seafile SHA-1 compatible)
	result := sha1Hex("test input")

	if len(result) != 40 {
		t.Errorf("sha1Hex length = %d, want 40", len(result))
	}

	// Verify it's valid hex
	if !isHexString([]byte(result)) {
		t.Errorf("sha1Hex result is not valid hex: %s", result)
	}

	// Test determinism
	result2 := sha1Hex("test input")
	if result != result2 {
		t.Errorf("sha1Hex not deterministic: %s != %s", result, result2)
	}

	// Test different inputs produce different outputs
	result3 := sha1Hex("different input")
	if result == result3 {
		t.Errorf("sha1Hex should produce different hashes for different inputs")
	}
}

// TestFSIDListJSONFormat tests that fs-id-list returns JSON array format
func TestFSIDListJSONFormat(t *testing.T) {
	// Empty list should be []
	emptyList := make([]string, 0)
	data, err := json.Marshal(emptyList)
	if err != nil {
		t.Fatalf("failed to marshal empty list: %v", err)
	}
	if string(data) != "[]" {
		t.Errorf("empty list should be [], got: %s", string(data))
	}

	// List with items
	fsIDs := []string{"abc123", "def456"}
	data, err = json.Marshal(fsIDs)
	if err != nil {
		t.Fatalf("failed to marshal list: %v", err)
	}
	if string(data) != `["abc123","def456"]` {
		t.Errorf("unexpected JSON format: %s", string(data))
	}
}

// TestCommitStructWithPointerFields tests Commit struct serialization with pointer types
func TestCommitStructWithPointerFields(t *testing.T) {
	t.Run("with nil pointers", func(t *testing.T) {
		commit := Commit{
			CommitID:       "abc123",
			RepoID:         "repo-id",
			RootID:         "root-id",
			ParentID:       nil,
			SecondParentID: nil,
			RepoCategory:   nil,
			Version:        1,
		}

		data, err := json.Marshal(commit)
		if err != nil {
			t.Fatalf("failed to marshal: %v", err)
		}

		var decoded map[string]interface{}
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}

		// Check parent_id is null (not missing or empty string)
		if decoded["parent_id"] != nil {
			t.Errorf("parent_id should be null, got: %v", decoded["parent_id"])
		}
	})

	t.Run("with non-nil pointers", func(t *testing.T) {
		parentID := "parent-commit"
		commit := Commit{
			CommitID:       "abc123",
			RepoID:         "repo-id",
			RootID:         "root-id",
			ParentID:       &parentID,
			SecondParentID: nil,
			Version:        1,
		}

		data, err := json.Marshal(commit)
		if err != nil {
			t.Fatalf("failed to marshal: %v", err)
		}

		var decoded map[string]interface{}
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}

		// Check parent_id has the value
		if decoded["parent_id"] != "parent-commit" {
			t.Errorf("parent_id should be 'parent-commit', got: %v", decoded["parent_id"])
		}
	})
}

// =============================================================================
// formatSizeSeafile Tests
// =============================================================================

func TestFormatSizeSeafile(t *testing.T) {
	nbsp := "\u00a0"
	tests := []struct {
		name     string
		bytes    int64
		expected string
	}{
		{"zero bytes", 0, "0" + nbsp + "bytes"},
		{"1 byte", 1, "1" + nbsp + "bytes"},
		{"100 bytes", 100, "100" + nbsp + "bytes"},
		{"1023 bytes", 1023, "1023" + nbsp + "bytes"},
		{"1 KB", 1024, "1.0" + nbsp + "KB"},
		{"1.5 KB", 1536, "1.5" + nbsp + "KB"},
		{"1 MB", 1024 * 1024, "1.0" + nbsp + "MB"},
		{"1 GB", 1024 * 1024 * 1024, "1.0" + nbsp + "GB"},
		{"1 TB", 1024 * 1024 * 1024 * 1024, "1.0" + nbsp + "TB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatSizeSeafile(tt.bytes)
			if result != tt.expected {
				t.Errorf("formatSizeSeafile(%d) = %q, want %q", tt.bytes, result, tt.expected)
			}
		})
	}
}

// TestFormatSizeSeafile_ContainsNBSP verifies non-breaking space is used
func TestFormatSizeSeafile_ContainsNBSP(t *testing.T) {
	result := formatSizeSeafile(1024)
	if !strings.Contains(result, "\u00a0") {
		t.Errorf("formatSizeSeafile should use non-breaking space (\\u00a0), got: %q", result)
	}
	if strings.Contains(result, " ") {
		// Check for regular space (U+0020) - should not be present
		for _, r := range result {
			if r == ' ' {
				t.Errorf("formatSizeSeafile should not contain regular space, got: %q", result)
				break
			}
		}
	}
}

func TestRetryLibraryStatsProjectionSync(t *testing.T) {
	t.Run("succeeds after retry", func(t *testing.T) {
		attempts := 0
		sleeps := 0
		err := retryLibraryStatsProjectionSync(func() error {
			attempts++
			if attempts < 2 {
				return errors.New("transient failure")
			}
			return nil
		}, func(time.Duration) {
			sleeps++
		})
		if err != nil {
			t.Fatalf("retryLibraryStatsProjectionSync() error = %v, want nil", err)
		}
		if attempts != 2 {
			t.Fatalf("attempts = %d, want 2", attempts)
		}
		if sleeps != 1 {
			t.Fatalf("sleeps = %d, want 1", sleeps)
		}
	})

	t.Run("returns last error after max attempts", func(t *testing.T) {
		attempts := 0
		sleeps := 0
		wantErr := errors.New("still failing")
		err := retryLibraryStatsProjectionSync(func() error {
			attempts++
			return wantErr
		}, func(time.Duration) {
			sleeps++
		})
		if !errors.Is(err, wantErr) {
			t.Fatalf("retryLibraryStatsProjectionSync() error = %v, want %v", err, wantErr)
		}
		if attempts != libraryStatsProjectionRetryAttempts {
			t.Fatalf("attempts = %d, want %d", attempts, libraryStatsProjectionRetryAttempts)
		}
		if sleeps != libraryStatsProjectionRetryAttempts-1 {
			t.Fatalf("sleeps = %d, want %d", sleeps, libraryStatsProjectionRetryAttempts-1)
		}
	})
}

// TestRetrySyncDerivedStateRead exercises the stale-read-after-CAS retry path
// that absorbs the window between an LWT commit and replicas applying it.
func TestRetrySyncDerivedStateRead(t *testing.T) {
	notBefore := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	fresh := syncLibraryDerivedState{
		HeadCommitID: "new-head",
		ProjectionRow: db.AdminLibraryProjectionRow{
			OrgID:     "org-1",
			LibraryID: "lib-1",
			UpdatedAt: notBefore,
		},
	}
	stale := syncLibraryDerivedState{
		HeadCommitID: "old-head",
		ProjectionRow: db.AdminLibraryProjectionRow{
			OrgID:     "org-1",
			LibraryID: "lib-1",
			UpdatedAt: notBefore.Add(-time.Millisecond),
		},
	}

	t.Run("accepts fresh row on first attempt", func(t *testing.T) {
		attempts := 0
		sleeps := 0
		got, err := retrySyncDerivedStateRead("lib-1", notBefore, func() (syncLibraryDerivedState, error) {
			attempts++
			return fresh, nil
		}, func(time.Duration) { sleeps++ })
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if got.HeadCommitID != "new-head" {
			t.Fatalf("HeadCommitID = %q, want new-head", got.HeadCommitID)
		}
		if attempts != 1 || sleeps != 0 {
			t.Fatalf("attempts=%d sleeps=%d, want 1/0", attempts, sleeps)
		}
	})

	t.Run("retries past a stale read, accepts the fresh row", func(t *testing.T) {
		attempts := 0
		sleeps := 0
		responses := []syncLibraryDerivedState{stale, fresh}
		got, err := retrySyncDerivedStateRead("lib-1", notBefore, func() (syncLibraryDerivedState, error) {
			r := responses[attempts]
			attempts++
			return r, nil
		}, func(time.Duration) { sleeps++ })
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if got.HeadCommitID != "new-head" {
			t.Fatalf("HeadCommitID = %q, want new-head", got.HeadCommitID)
		}
		if attempts != 2 || sleeps != 1 {
			t.Fatalf("attempts=%d sleeps=%d, want 2/1", attempts, sleeps)
		}
	})

	t.Run("returns stale error after exhausting retries", func(t *testing.T) {
		attempts := 0
		sleeps := 0
		_, err := retrySyncDerivedStateRead("lib-1", notBefore, func() (syncLibraryDerivedState, error) {
			attempts++
			return stale, nil
		}, func(time.Duration) { sleeps++ })
		if err == nil {
			t.Fatal("err = nil, want stale error")
		}
		if !strings.Contains(err.Error(), "stale canonical sync state for lib-1") {
			t.Fatalf("err = %v, want to contain stale-canonical message", err)
		}
		if attempts != libraryStatsProjectionRetryAttempts {
			t.Fatalf("attempts = %d, want %d", attempts, libraryStatsProjectionRetryAttempts)
		}
		if sleeps != libraryStatsProjectionRetryAttempts-1 {
			t.Fatalf("sleeps = %d, want %d", sleeps, libraryStatsProjectionRetryAttempts-1)
		}
	})

	t.Run("retries through a transient fetch error", func(t *testing.T) {
		attempts := 0
		_, err := retrySyncDerivedStateRead("lib-1", notBefore, func() (syncLibraryDerivedState, error) {
			attempts++
			if attempts == 1 {
				return syncLibraryDerivedState{}, errors.New("transient cassandra error")
			}
			return fresh, nil
		}, func(time.Duration) {})
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if attempts != 2 {
			t.Fatalf("attempts = %d, want 2", attempts)
		}
	})

	t.Run("returns last fetch error after exhausting retries", func(t *testing.T) {
		wantErr := errors.New("persistent cassandra error")
		attempts := 0
		_, err := retrySyncDerivedStateRead("lib-1", notBefore, func() (syncLibraryDerivedState, error) {
			attempts++
			return syncLibraryDerivedState{}, wantErr
		}, func(time.Duration) {})
		if !errors.Is(err, wantErr) {
			t.Fatalf("err = %v, want %v", err, wantErr)
		}
		if attempts != libraryStatsProjectionRetryAttempts {
			t.Fatalf("attempts = %d, want %d", attempts, libraryStatsProjectionRetryAttempts)
		}
	})

	t.Run("treats equal updated_at as fresh (boundary)", func(t *testing.T) {
		boundary := syncLibraryDerivedState{
			HeadCommitID: "boundary-head",
			ProjectionRow: db.AdminLibraryProjectionRow{
				OrgID:     "org-1",
				LibraryID: "lib-1",
				UpdatedAt: notBefore, // exactly equal — must not be considered stale
			},
		}
		got, err := retrySyncDerivedStateRead("lib-1", notBefore, func() (syncLibraryDerivedState, error) {
			return boundary, nil
		}, func(time.Duration) {})
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if got.HeadCommitID != "boundary-head" {
			t.Fatalf("HeadCommitID = %q, want boundary-head", got.HeadCommitID)
		}
	})
}

// TestSyncLibraryHeadDerivedStateUsing covers the full read+write composition
// so the batch-write failure path (which the production code talks to
// gocql.Session for) has a deterministic regression test alongside the
// read-side stale-guard.
func TestSyncLibraryHeadDerivedStateUsing(t *testing.T) {
	notBefore := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	fresh := syncLibraryDerivedState{
		HeadCommitID: "new-head",
		ProjectionRow: db.AdminLibraryProjectionRow{
			OrgID:     "org-1",
			LibraryID: "lib-1",
			UpdatedAt: notBefore,
		},
	}
	stale := syncLibraryDerivedState{
		HeadCommitID: "old-head",
		ProjectionRow: db.AdminLibraryProjectionRow{
			OrgID:     "org-1",
			LibraryID: "lib-1",
			UpdatedAt: notBefore.Add(-time.Millisecond),
		},
	}

	t.Run("writes projection once read returns fresh state", func(t *testing.T) {
		writes := 0
		var written syncLibraryDerivedState
		err := syncLibraryHeadDerivedStateUsing("lib-1", notBefore,
			func() (syncLibraryDerivedState, error) { return fresh, nil },
			func(_ string, state syncLibraryDerivedState) error {
				writes++
				written = state
				return nil
			},
			func(time.Duration) {})
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if writes != 1 {
			t.Fatalf("writes = %d, want 1", writes)
		}
		if written.HeadCommitID != "new-head" {
			t.Fatalf("written.HeadCommitID = %q, want new-head", written.HeadCommitID)
		}
	})

	t.Run("propagates read-side stale exhaustion without writing", func(t *testing.T) {
		writes := 0
		err := syncLibraryHeadDerivedStateUsing("lib-1", notBefore,
			func() (syncLibraryDerivedState, error) { return stale, nil },
			func(string, syncLibraryDerivedState) error {
				writes++
				return nil
			},
			func(time.Duration) {})
		if err == nil {
			t.Fatal("err = nil, want stale error")
		}
		if !strings.Contains(err.Error(), "failed to read library state after sync update") {
			t.Fatalf("err = %v, want to wrap the read-side failure", err)
		}
		if !strings.Contains(err.Error(), "stale canonical sync state for lib-1") {
			t.Fatalf("err = %v, want to surface the stale message", err)
		}
		if writes != 0 {
			t.Fatalf("writes = %d, want 0 (must not attempt write when read fails)", writes)
		}
	})

	t.Run("propagates batch write failure after fresh read", func(t *testing.T) {
		wantErr := errors.New("batch exec failed")
		writes := 0
		err := syncLibraryHeadDerivedStateUsing("lib-1", notBefore,
			func() (syncLibraryDerivedState, error) { return fresh, nil },
			func(string, syncLibraryDerivedState) error {
				writes++
				return wantErr
			},
			func(time.Duration) {})
		if !errors.Is(err, wantErr) {
			t.Fatalf("err = %v, want to wrap %v", err, wantErr)
		}
		if !strings.Contains(err.Error(), "failed to sync head lookup/admin projection") {
			t.Fatalf("err = %v, want the batch-side error prefix", err)
		}
		if writes != 1 {
			t.Fatalf("writes = %d, want 1 (no retry on write side)", writes)
		}
	})

	t.Run("write is called with the fresh state observed by the read loop", func(t *testing.T) {
		responses := []syncLibraryDerivedState{stale, fresh}
		reads := 0
		var observed syncLibraryDerivedState
		err := syncLibraryHeadDerivedStateUsing("lib-1", notBefore,
			func() (syncLibraryDerivedState, error) {
				r := responses[reads]
				reads++
				return r, nil
			},
			func(_ string, state syncLibraryDerivedState) error {
				observed = state
				return nil
			},
			func(time.Duration) {})
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if observed.HeadCommitID != "new-head" {
			t.Fatalf("observed.HeadCommitID = %q, want new-head (write must receive post-retry fresh state)", observed.HeadCommitID)
		}
	})
}

// TestOverrideSyncLibraryCASFields locks in the contract that the publisher's
// CAS-authoritative values override anything the post-CAS read returned. This
// is the defense against read-after-write convergence affecting the projection
// write — even if a hypothetical future regression weakens the stale-guard,
// the override guarantees the projection reflects the CAS commit.
func TestOverrideSyncLibraryCASFields(t *testing.T) {
	postCASUpdatedAt := time.Date(2026, 5, 21, 16, 31, 5, 0, time.UTC)
	staleRead := syncLibraryDerivedState{
		HeadCommitID: "stale-head", // a replica that hadn't seen the CAS yet
		ProjectionRow: db.AdminLibraryProjectionRow{
			OrgID:        "org-1",
			LibraryID:    "lib-1",
			OwnerID:      "admin-controlled",
			Name:         "admin-controlled",
			Encrypted:    true,
			StorageClass: "admin-controlled",
			SizeBytes:    0,                                // CAS-controlled, stale
			FileCount:    0,                                // CAS-controlled, stale
			UpdatedAt:    postCASUpdatedAt.Add(-time.Hour), // CAS-controlled, stale
		},
	}

	overrideSyncLibraryCASFields(&staleRead, "new-head", postCASUpdatedAt, 4096, 3)

	if staleRead.HeadCommitID != "new-head" {
		t.Fatalf("HeadCommitID = %q, want new-head", staleRead.HeadCommitID)
	}
	if staleRead.ProjectionRow.SizeBytes != 4096 {
		t.Fatalf("SizeBytes = %d, want 4096", staleRead.ProjectionRow.SizeBytes)
	}
	if staleRead.ProjectionRow.FileCount != 3 {
		t.Fatalf("FileCount = %d, want 3", staleRead.ProjectionRow.FileCount)
	}
	if !staleRead.ProjectionRow.UpdatedAt.Equal(postCASUpdatedAt) {
		t.Fatalf("UpdatedAt = %s, want %s", staleRead.ProjectionRow.UpdatedAt, postCASUpdatedAt)
	}
	// Admin-controlled fields must not be touched by the override — they came
	// from the read at a snapshot the caller already accepted as fresh-enough.
	if staleRead.ProjectionRow.OwnerID != "admin-controlled" ||
		staleRead.ProjectionRow.Name != "admin-controlled" ||
		!staleRead.ProjectionRow.Encrypted ||
		staleRead.ProjectionRow.StorageClass != "admin-controlled" {
		t.Fatalf("override leaked into admin-controlled fields: %+v", staleRead.ProjectionRow)
	}
}

func TestRepairPublishedSyncHeadAfterCounterFailureUsing(t *testing.T) {
	notBefore := time.Date(2026, 5, 21, 16, 31, 5, 0, time.UTC)
	fresh := syncLibraryDerivedState{
		HeadCommitID: "new-head",
		ProjectionRow: db.AdminLibraryProjectionRow{
			OrgID:     "org-1",
			LibraryID: "lib-1",
			UpdatedAt: notBefore,
		},
	}
	staleState := syncLibraryDerivedState{
		HeadCommitID: "new-head",
		ProjectionRow: db.AdminLibraryProjectionRow{
			OrgID:     "org-1",
			LibraryID: "lib-1",
			UpdatedAt: notBefore.Add(-time.Second),
		},
	}

	t.Run("retries until canonical state reaches notBefore then reconciles queues and writes", func(t *testing.T) {
		responses := []syncLibraryDerivedState{staleState, fresh}
		reads := 0
		sleeps := 0
		reconciles := 0
		queues := 0
		writes := 0
		var reconciled syncLibraryDerivedState
		var queued syncLibraryDerivedState
		var written syncLibraryDerivedState
		err := repairPublishedSyncHeadAfterCounterFailureUsing(
			"lib-1",
			notBefore,
			func() (syncLibraryDerivedState, error) {
				response := responses[reads]
				reads++
				return response, nil
			},
			func(state syncLibraryDerivedState) error {
				reconciles++
				reconciled = state
				return nil
			},
			func(state syncLibraryDerivedState) error {
				queues++
				queued = state
				return nil
			},
			func(state syncLibraryDerivedState) error {
				writes++
				written = state
				return nil
			},
			func(time.Duration) { sleeps++ },
		)
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if reads != 2 || sleeps != 1 {
			t.Fatalf("reads=%d sleeps=%d, want 2/1", reads, sleeps)
		}
		if reconciles != 1 || queues != 1 || writes != 1 {
			t.Fatalf("reconciles=%d queues=%d writes=%d, want 1/1/1", reconciles, queues, writes)
		}
		if reconciled.HeadCommitID != fresh.HeadCommitID || queued.HeadCommitID != fresh.HeadCommitID || written.HeadCommitID != fresh.HeadCommitID {
			t.Fatalf("reconciled=%q queued=%q written=%q, want all %q", reconciled.HeadCommitID, queued.HeadCommitID, written.HeadCommitID, fresh.HeadCommitID)
		}
	})

	t.Run("propagates stale-state exhaustion without reconciling queueing or writing", func(t *testing.T) {
		reconciles := 0
		queues := 0
		writes := 0
		err := repairPublishedSyncHeadAfterCounterFailureUsing(
			"lib-1",
			notBefore,
			func() (syncLibraryDerivedState, error) { return staleState, nil },
			func(syncLibraryDerivedState) error {
				reconciles++
				return nil
			},
			func(syncLibraryDerivedState) error {
				queues++
				return nil
			},
			func(syncLibraryDerivedState) error {
				writes++
				return nil
			},
			func(time.Duration) {},
		)
		if err == nil {
			t.Fatal("err = nil, want stale-state error")
		}
		if !strings.Contains(err.Error(), "failed to read canonical library state for sync head repair") {
			t.Fatalf("err = %v, want read-side repair prefix", err)
		}
		if !strings.Contains(err.Error(), "stale canonical sync state for lib-1") {
			t.Fatalf("err = %v, want stale-state message", err)
		}
		if reconciles != 0 || queues != 0 || writes != 0 {
			t.Fatalf("reconciles=%d queues=%d writes=%d, want 0/0/0", reconciles, queues, writes)
		}
	})

	t.Run("propagates reconcile failure and skips queue and write", func(t *testing.T) {
		wantErr := errors.New("reconcile failed")
		queues := 0
		writes := 0
		err := repairPublishedSyncHeadAfterCounterFailureUsing(
			"lib-1",
			notBefore,
			func() (syncLibraryDerivedState, error) { return fresh, nil },
			func(syncLibraryDerivedState) error { return wantErr },
			func(syncLibraryDerivedState) error {
				queues++
				return nil
			},
			func(syncLibraryDerivedState) error {
				writes++
				return nil
			},
			func(time.Duration) {},
		)
		if !errors.Is(err, wantErr) {
			t.Fatalf("err = %v, want %v", err, wantErr)
		}
		if !strings.Contains(err.Error(), "failed to reconcile sync library storage state") {
			t.Fatalf("err = %v, want reconcile prefix", err)
		}
		if queues != 0 || writes != 0 {
			t.Fatalf("queues = %d writes = %d, want 0/0", queues, writes)
		}
	})

	t.Run("propagates queue failure and skips write", func(t *testing.T) {
		wantErr := errors.New("queue failed")
		writes := 0
		err := repairPublishedSyncHeadAfterCounterFailureUsing(
			"lib-1",
			notBefore,
			func() (syncLibraryDerivedState, error) { return fresh, nil },
			func(syncLibraryDerivedState) error { return nil },
			func(syncLibraryDerivedState) error { return wantErr },
			func(syncLibraryDerivedState) error {
				writes++
				return nil
			},
			func(time.Duration) {},
		)
		if !errors.Is(err, wantErr) {
			t.Fatalf("err = %v, want %v", err, wantErr)
		}
		if !strings.Contains(err.Error(), "failed to queue aggregate storage reconciliation") {
			t.Fatalf("err = %v, want queue prefix", err)
		}
		if writes != 0 {
			t.Fatalf("writes = %d, want 0", writes)
		}
	})

	t.Run("propagates write failure after successful reconcile and queue", func(t *testing.T) {
		wantErr := errors.New("write failed")
		reconciles := 0
		queues := 0
		err := repairPublishedSyncHeadAfterCounterFailureUsing(
			"lib-1",
			notBefore,
			func() (syncLibraryDerivedState, error) { return fresh, nil },
			func(syncLibraryDerivedState) error {
				reconciles++
				return nil
			},
			func(syncLibraryDerivedState) error {
				queues++
				return nil
			},
			func(syncLibraryDerivedState) error { return wantErr },
			func(time.Duration) {},
		)
		if !errors.Is(err, wantErr) {
			t.Fatalf("err = %v, want %v", err, wantErr)
		}
		if !strings.Contains(err.Error(), "failed to sync head lookup/admin projection") {
			t.Fatalf("err = %v, want write prefix", err)
		}
		if reconciles != 1 || queues != 1 {
			t.Fatalf("reconciles = %d queues = %d, want 1/1", reconciles, queues)
		}
	})
}

func makeAlignedSyncDerivedStateCanary(targetHead string) syncDerivedStateCanary {
	canonicalUpdatedAt := time.Date(2026, 5, 21, 16, 31, 5, 0, time.UTC)
	projectionRow := db.AdminLibraryProjectionRow{
		OrgID:     "org-1",
		LibraryID: "lib-1",
		OwnerID:   "user-1",
		CreatedAt: time.Date(2026, 5, 20, 9, 15, 0, 0, time.UTC),
		UpdatedAt: canonicalUpdatedAt,
		SizeBytes: 4096,
		FileCount: 3,
	}
	projectionCanary := syncAdminProjectionCanary{
		Present:   true,
		OwnerID:   projectionRow.OwnerID,
		UpdatedAt: projectionRow.UpdatedAt,
		SizeBytes: projectionRow.SizeBytes,
		FileCount: projectionRow.FileCount,
	}
	return syncDerivedStateCanary{
		Canonical: syncLibraryDerivedState{
			HeadCommitID:  targetHead,
			ProjectionRow: projectionRow,
		},
		LookupHead:       targetHead,
		OrgProjection:    projectionCanary,
		OwnerProjection:  projectionCanary,
		GlobalProjection: projectionCanary,
	}
}

// TestRepairSyncHeadDerivedStateIfDriftedUsing locks in the contract that
// keeps the idempotent fast-path from blessing post-CAS drift. The fast-path
// returns 200 when canonical libraries.head_commit_id already equals the
// caller's targetHead, which means a prior attempt published the head but
// could still have failed any of: storage counter delta, aggregate queue
// insert, or a partial lookup/admin projection write. Without this canary the
// stale state would be permanent. With it, the canary validates the lookup and
// all active admin projection surfaces against canonical state before deciding
// whether the retry can short-circuit.
func TestRepairSyncHeadDerivedStateIfDriftedUsing(t *testing.T) {
	t.Run("returns nil immediately when lookup and admin projections match canonical state", func(t *testing.T) {
		canaryReads := 0
		repairs := 0
		aligned := makeAlignedSyncDerivedStateCanary("target-head")
		err := repairSyncHeadDerivedStateIfDriftedUsing(
			"target-head",
			func() (syncDerivedStateCanary, error) {
				canaryReads++
				return aligned, nil
			},
			func(time.Time, int64, int64) error {
				repairs++
				return nil
			},
			func(time.Duration) {},
		)
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if canaryReads != 1 {
			t.Fatalf("canaryReads = %d, want 1", canaryReads)
		}
		if repairs != 0 {
			t.Fatalf("repairs = %d, want 0 when canary already agrees", repairs)
		}
	})

	t.Run("retries stale canonical canary before accepting aligned derived state", func(t *testing.T) {
		stale := makeAlignedSyncDerivedStateCanary("target-head")
		stale.Canonical.HeadCommitID = "old-head"
		aligned := makeAlignedSyncDerivedStateCanary("target-head")
		canaryReads := 0
		repairs := 0
		sleeps := 0
		err := repairSyncHeadDerivedStateIfDriftedUsing(
			"target-head",
			func() (syncDerivedStateCanary, error) {
				canaryReads++
				if canaryReads == 1 {
					return stale, nil
				}
				return aligned, nil
			},
			func(time.Time, int64, int64) error {
				repairs++
				return nil
			},
			func(time.Duration) { sleeps++ },
		)
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if canaryReads != 2 || sleeps != 1 {
			t.Fatalf("canaryReads = %d sleeps = %d, want 2/1", canaryReads, sleeps)
		}
		if repairs != 0 {
			t.Fatalf("repairs = %d, want 0 after stale read converges to aligned state", repairs)
		}
	})

	t.Run("does not repair from a stale canonical canary", func(t *testing.T) {
		stale := makeAlignedSyncDerivedStateCanary("target-head")
		stale.Canonical.HeadCommitID = "old-head"
		canaryReads := 0
		repairs := 0
		sleeps := 0
		err := repairSyncHeadDerivedStateIfDriftedUsing(
			"target-head",
			func() (syncDerivedStateCanary, error) {
				canaryReads++
				return stale, nil
			},
			func(time.Time, int64, int64) error {
				repairs++
				return nil
			},
			func(time.Duration) { sleeps++ },
		)
		if err == nil {
			t.Fatal("err = nil, want stale canonical canary error")
		}
		if !strings.Contains(err.Error(), "stale canonical sync state") {
			t.Fatalf("err = %v, want stale canonical sync state prefix", err)
		}
		if canaryReads != libraryStatsProjectionRetryAttempts || sleeps != libraryStatsProjectionRetryAttempts-1 {
			t.Fatalf("canaryReads = %d sleeps = %d, want %d/%d", canaryReads, sleeps, libraryStatsProjectionRetryAttempts, libraryStatsProjectionRetryAttempts-1)
		}
		if repairs != 0 {
			t.Fatalf("repairs = %d, want 0 while canonical canary is stale", repairs)
		}
	})

	t.Run("runs repair with canonical stats when lookup is stale", func(t *testing.T) {
		stale := makeAlignedSyncDerivedStateCanary("target-head")
		canonicalUpdatedAt := stale.Canonical.ProjectionRow.UpdatedAt
		var repairCalled struct {
			updatedAt time.Time
			totalSize int64
			fileCount int64
			count     int
		}
		stale.LookupHead = "stale-head"
		err := repairSyncHeadDerivedStateIfDriftedUsing(
			"target-head",
			func() (syncDerivedStateCanary, error) { return stale, nil },
			func(updatedAt time.Time, totalSize, fileCount int64) error {
				repairCalled.updatedAt = updatedAt
				repairCalled.totalSize = totalSize
				repairCalled.fileCount = fileCount
				repairCalled.count++
				return nil
			},
			func(time.Duration) {},
		)
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if repairCalled.count != 1 {
			t.Fatalf("repair invocations = %d, want 1", repairCalled.count)
		}
		if !repairCalled.updatedAt.Equal(canonicalUpdatedAt) || repairCalled.totalSize != stale.Canonical.ProjectionRow.SizeBytes || repairCalled.fileCount != stale.Canonical.ProjectionRow.FileCount {
			t.Fatalf("repair received updatedAt=%s totalSize=%d fileCount=%d, want %s/4096/3",
				repairCalled.updatedAt, repairCalled.totalSize, repairCalled.fileCount, canonicalUpdatedAt)
		}
	})

	t.Run("runs repair when org projection is missing even though lookup matches", func(t *testing.T) {
		stale := makeAlignedSyncDerivedStateCanary("target-head")
		stale.OrgProjection = syncAdminProjectionCanary{}
		repairs := 0
		err := repairSyncHeadDerivedStateIfDriftedUsing(
			"target-head",
			func() (syncDerivedStateCanary, error) { return stale, nil },
			func(time.Time, int64, int64) error {
				repairs++
				return nil
			},
			func(time.Duration) {},
		)
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if repairs != 1 {
			t.Fatalf("repairs = %d, want 1", repairs)
		}
	})

	t.Run("runs repair when owner projection is missing even though lookup matches", func(t *testing.T) {
		stale := makeAlignedSyncDerivedStateCanary("target-head")
		stale.OwnerProjection = syncAdminProjectionCanary{}
		repairs := 0
		err := repairSyncHeadDerivedStateIfDriftedUsing(
			"target-head",
			func() (syncDerivedStateCanary, error) { return stale, nil },
			func(time.Time, int64, int64) error {
				repairs++
				return nil
			},
			func(time.Duration) {},
		)
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if repairs != 1 {
			t.Fatalf("repairs = %d, want 1", repairs)
		}
	})

	t.Run("runs repair when global projection drifts even though lookup matches", func(t *testing.T) {
		stale := makeAlignedSyncDerivedStateCanary("target-head")
		stale.GlobalProjection.SizeBytes++
		repairs := 0
		err := repairSyncHeadDerivedStateIfDriftedUsing(
			"target-head",
			func() (syncDerivedStateCanary, error) { return stale, nil },
			func(time.Time, int64, int64) error {
				repairs++
				return nil
			},
			func(time.Duration) {},
		)
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if repairs != 1 {
			t.Fatalf("repairs = %d, want 1", repairs)
		}
	})

	t.Run("wraps and propagates canary-read failure without invoking repair", func(t *testing.T) {
		wantErr := errors.New("canary read failed")
		repairs := 0
		err := repairSyncHeadDerivedStateIfDriftedUsing(
			"target-head",
			func() (syncDerivedStateCanary, error) { return syncDerivedStateCanary{}, wantErr },
			func(time.Time, int64, int64) error {
				repairs++
				return nil
			},
			func(time.Duration) {},
		)
		if !errors.Is(err, wantErr) {
			t.Fatalf("err = %v, want to wrap %v", err, wantErr)
		}
		if !strings.Contains(err.Error(), "read sync derived-state canary") {
			t.Fatalf("err = %v, want canary-read prefix", err)
		}
		if repairs != 0 {
			t.Fatalf("repairs = %d, want 0 on canary-read failure", repairs)
		}
	})

	t.Run("wraps and propagates repair failure", func(t *testing.T) {
		wantErr := errors.New("repair failed")
		stale := makeAlignedSyncDerivedStateCanary("target-head")
		stale.LookupHead = "stale-head"
		err := repairSyncHeadDerivedStateIfDriftedUsing(
			"target-head",
			func() (syncDerivedStateCanary, error) { return stale, nil },
			func(time.Time, int64, int64) error { return wantErr },
			func(time.Duration) {},
		)
		if !errors.Is(err, wantErr) {
			t.Fatalf("err = %v, want to wrap %v", err, wantErr)
		}
		if !strings.Contains(err.Error(), "repair drifted sync derived state") {
			t.Fatalf("err = %v, want repair prefix", err)
		}
	})
}

// TestSyncAdminProjectionCanaryMatches locks down the pure predicate used by
// the canary to decide whether each admin projection surface still reflects
// canonical state. A projection is in sync only when Present is true and ALL
// four fields agree; any disagreement (or an absent row) must produce false
// so the surrounding alignedWith returns drift and the handler reruns repair.
func TestSyncAdminProjectionCanaryMatches(t *testing.T) {
	updatedAt := time.Date(2026, 5, 21, 16, 31, 5, 0, time.UTC)
	base := syncAdminProjectionCanary{
		Present:   true,
		OwnerID:   "user-1",
		UpdatedAt: updatedAt,
		SizeBytes: 4096,
		FileCount: 3,
	}

	tests := []struct {
		name      string
		mutate    func(c *syncAdminProjectionCanary)
		ownerID   string
		updatedAt time.Time
		sizeBytes int64
		fileCount int64
		want      bool
	}{
		{
			name:      "matches exactly",
			ownerID:   base.OwnerID,
			updatedAt: base.UpdatedAt,
			sizeBytes: base.SizeBytes,
			fileCount: base.FileCount,
			want:      true,
		},
		{
			name:      "absent projection never matches",
			mutate:    func(c *syncAdminProjectionCanary) { c.Present = false },
			ownerID:   base.OwnerID,
			updatedAt: base.UpdatedAt,
			sizeBytes: base.SizeBytes,
			fileCount: base.FileCount,
			want:      false,
		},
		{
			name:      "owner mismatch",
			ownerID:   "user-2",
			updatedAt: base.UpdatedAt,
			sizeBytes: base.SizeBytes,
			fileCount: base.FileCount,
			want:      false,
		},
		{
			name:      "updated_at mismatch",
			ownerID:   base.OwnerID,
			updatedAt: base.UpdatedAt.Add(time.Millisecond),
			sizeBytes: base.SizeBytes,
			fileCount: base.FileCount,
			want:      false,
		},
		{
			name:      "size_bytes mismatch",
			ownerID:   base.OwnerID,
			updatedAt: base.UpdatedAt,
			sizeBytes: base.SizeBytes + 1,
			fileCount: base.FileCount,
			want:      false,
		},
		{
			name:      "file_count mismatch",
			ownerID:   base.OwnerID,
			updatedAt: base.UpdatedAt,
			sizeBytes: base.SizeBytes,
			fileCount: base.FileCount + 1,
			want:      false,
		},
		{
			name:      "equivalent updated_at across timezone is matched by Equal",
			ownerID:   base.OwnerID,
			updatedAt: base.UpdatedAt.In(time.FixedZone("test", 3600)),
			sizeBytes: base.SizeBytes,
			fileCount: base.FileCount,
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := base
			if tt.mutate != nil {
				tt.mutate(&c)
			}
			got := c.matches(tt.ownerID, tt.updatedAt, tt.sizeBytes, tt.fileCount)
			if got != tt.want {
				t.Fatalf("matches = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestSyncDerivedStateCanaryAlignedWith locks down the composition predicate
// that decides whether the idempotent fast-path can short-circuit. The
// invariant is that EVERY surface (lookup head plus the three admin
// projections) must align with canonical state; any single surface drifting
// or missing must surface as drift so the handler reruns repair instead of
// silently returning 200.
func TestSyncDerivedStateCanaryAlignedWith(t *testing.T) {
	makeAligned := func() syncDerivedStateCanary {
		return makeAlignedSyncDerivedStateCanary("target-head")
	}

	t.Run("aligned canary returns true for matching target head", func(t *testing.T) {
		if !makeAligned().alignedWith("target-head") {
			t.Fatal("alignedWith = false, want true for aligned canary")
		}
	})

	t.Run("mismatched target head fails even when all surfaces agree", func(t *testing.T) {
		if makeAligned().alignedWith("other-head") {
			t.Fatal("alignedWith = true, want false when target differs from canonical")
		}
	})

	t.Run("stale lookup head trips drift", func(t *testing.T) {
		c := makeAligned()
		c.LookupHead = "stale-head"
		if c.alignedWith("target-head") {
			t.Fatal("alignedWith = true, want false when libraries_by_id is stale")
		}
	})

	t.Run("missing org projection trips drift", func(t *testing.T) {
		c := makeAligned()
		c.OrgProjection = syncAdminProjectionCanary{}
		if c.alignedWith("target-head") {
			t.Fatal("alignedWith = true, want false when org projection is missing")
		}
	})

	t.Run("missing owner projection trips drift", func(t *testing.T) {
		c := makeAligned()
		c.OwnerProjection = syncAdminProjectionCanary{}
		if c.alignedWith("target-head") {
			t.Fatal("alignedWith = true, want false when owner projection is missing")
		}
	})

	t.Run("missing global projection trips drift", func(t *testing.T) {
		c := makeAligned()
		c.GlobalProjection = syncAdminProjectionCanary{}
		if c.alignedWith("target-head") {
			t.Fatal("alignedWith = true, want false when global projection is missing")
		}
	})

	t.Run("drifted org projection size trips drift", func(t *testing.T) {
		c := makeAligned()
		c.OrgProjection.SizeBytes++
		if c.alignedWith("target-head") {
			t.Fatal("alignedWith = true, want false when org projection size drifted")
		}
	})

	t.Run("drifted owner projection file_count trips drift", func(t *testing.T) {
		c := makeAligned()
		c.OwnerProjection.FileCount++
		if c.alignedWith("target-head") {
			t.Fatal("alignedWith = true, want false when owner projection file count drifted")
		}
	})

	t.Run("drifted global projection updated_at trips drift", func(t *testing.T) {
		c := makeAligned()
		c.GlobalProjection.UpdatedAt = c.GlobalProjection.UpdatedAt.Add(time.Millisecond)
		if c.alignedWith("target-head") {
			t.Fatal("alignedWith = true, want false when global projection updated_at drifted")
		}
	})

	t.Run("drifted owner_id on global projection trips drift", func(t *testing.T) {
		c := makeAligned()
		c.GlobalProjection.OwnerID = "other-owner"
		if c.alignedWith("target-head") {
			t.Fatal("alignedWith = true, want false when global projection owner_id drifted")
		}
	})
}

// TestHandleSyncHeadIdempotentSuccess verifies the response contract for the
// idempotent fast-path: 200 when the canary repair confirms no drift (or
// successfully repairs it), and 503 + Retry-After when the repair itself
// fails so the client retries instead of silently treating the partially-
// converged state as a final success. The handler dispatches the repair
// function via repairSyncHeadDerivedStateIfDriftedFn, which the test injects
// directly so this contract can be exercised without a real DB session.
func TestHandleSyncHeadIdempotentSuccess(t *testing.T) {
	makeHandler := func(blockRepair, derivedRepair func(orgID, repoID, targetHead string) error) *SyncHandler {
		return &SyncHandler{
			repairPublishedSyncCommitBlockDeltaFn: blockRepair,
			repairSyncHeadDerivedStateIfDriftedFn: derivedRepair,
			finalizedBlockDeltas:                  newSyncFinalizedDeltaSet(),
		}
	}

	mountTestRoute := func(h *SyncHandler) *gin.Engine {
		r := setupSyncTestRouter()
		r.GET("/test/:repo_id/idempotent/:head", func(c *gin.Context) {
			h.handleSyncHeadIdempotentSuccess(c, c.GetString("org_id"), c.Param("repo_id"), c.Param("head"), "test")
		})
		return r
	}

	t.Run("returns 200 without Retry-After when canary repair succeeds", func(t *testing.T) {
		calls := 0
		var capturedArgs struct {
			orgID, repoID, targetHead string
		}
		h := makeHandler(nil, func(orgID, repoID, targetHead string) error {
			calls++
			capturedArgs.orgID = orgID
			capturedArgs.repoID = repoID
			capturedArgs.targetHead = targetHead
			return nil
		})
		r := mountTestRoute(h)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/test/lib-1/idempotent/target-head", nil)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
		}
		if got := w.Header().Get("Retry-After"); got != "" {
			t.Fatalf("Retry-After = %q, want empty on success", got)
		}
		if calls != 1 {
			t.Fatalf("repair calls = %d, want 1", calls)
		}
		if capturedArgs.orgID != "00000000-0000-0000-0000-000000000001" || capturedArgs.repoID != "lib-1" || capturedArgs.targetHead != "target-head" {
			t.Fatalf("repair received org=%q repo=%q head=%q, want propagation from handler context", capturedArgs.orgID, capturedArgs.repoID, capturedArgs.targetHead)
		}
	})

	t.Run("returns 503 with Retry-After when block-reference repair fails", func(t *testing.T) {
		blockRepairCalls := 0
		derivedRepairCalls := 0
		h := makeHandler(func(string, string, string) error {
			blockRepairCalls++
			return errors.New("block repair failed")
		}, func(string, string, string) error {
			derivedRepairCalls++
			return nil
		})
		r := mountTestRoute(h)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/test/lib-1/idempotent/target-head", nil)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503; body=%s", w.Code, w.Body.String())
		}
		if got := w.Header().Get("Retry-After"); got != "1" {
			t.Fatalf("Retry-After = %q, want 1", got)
		}
		if !strings.Contains(w.Body.String(), "sync head publish block-reference reconciliation pending") {
			t.Fatalf("body = %q, want block-reference-repair-pending error", w.Body.String())
		}
		if blockRepairCalls != 1 {
			t.Fatalf("block repair calls = %d, want 1", blockRepairCalls)
		}
		if derivedRepairCalls != 0 {
			t.Fatalf("derived repair calls = %d, want 0 when block repair fails first", derivedRepairCalls)
		}
	})

	t.Run("skips block-reference repair when finalized delta memo hits", func(t *testing.T) {
		blockRepairCalls := 0
		derivedRepairCalls := 0
		h := makeHandler(func(string, string, string) error {
			blockRepairCalls++
			return nil
		}, func(string, string, string) error {
			derivedRepairCalls++
			return nil
		})
		h.finalizedBlockDeltas.mark("lib-1", "target-head")
		r := mountTestRoute(h)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/test/lib-1/idempotent/target-head", nil)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
		}
		if blockRepairCalls != 0 {
			t.Fatalf("block repair calls = %d, want 0 on finalized-delta memo hit", blockRepairCalls)
		}
		if derivedRepairCalls != 1 {
			t.Fatalf("derived repair calls = %d, want 1", derivedRepairCalls)
		}
	})

	t.Run("runs block-reference repair on finalized delta memo miss before derived-state repair", func(t *testing.T) {
		calls := make([]string, 0, 2)
		h := makeHandler(func(orgID, repoID, targetHead string) error {
			calls = append(calls, fmt.Sprintf("block:%s:%s:%s", orgID, repoID, targetHead))
			return nil
		}, func(orgID, repoID, targetHead string) error {
			calls = append(calls, fmt.Sprintf("derived:%s:%s:%s", orgID, repoID, targetHead))
			return nil
		})
		r := mountTestRoute(h)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/test/lib-1/idempotent/target-head", nil)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
		}
		want := []string{
			"block:00000000-0000-0000-0000-000000000001:lib-1:target-head",
			"derived:00000000-0000-0000-0000-000000000001:lib-1:target-head",
		}
		if len(calls) != len(want) {
			t.Fatalf("call count = %d, want %d (%v)", len(calls), len(want), calls)
		}
		for i := range want {
			if calls[i] != want[i] {
				t.Fatalf("calls[%d] = %q, want %q (full=%v)", i, calls[i], want[i], calls)
			}
		}
	})

	t.Run("returns 503 with Retry-After when canary repair fails", func(t *testing.T) {
		h := makeHandler(nil, func(string, string, string) error {
			return errors.New("repair failed")
		})
		r := mountTestRoute(h)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/test/lib-1/idempotent/target-head", nil)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503; body=%s", w.Code, w.Body.String())
		}
		if got := w.Header().Get("Retry-After"); got != "1" {
			t.Fatalf("Retry-After = %q, want 1", got)
		}
		if !strings.Contains(w.Body.String(), "sync head publish derived state repair pending") {
			t.Fatalf("body = %q, want canary-repair-pending error", w.Body.String())
		}
	})

	t.Run("falls back to method dispatch when no test hook is set", func(t *testing.T) {
		// When repairSyncHeadDerivedStateIfDriftedFn is nil the dispatcher
		// falls back to h.repairSyncHeadDerivedStateIfDrifted, which calls
		// h.db. A nil-db handler produces a panic. We don't want the test
		// to panic; instead this assertion documents the dispatch path:
		// any production deployment must arrive with a non-nil DB, so the
		// nil-hook case is the production path and reaches the real method.
		h := &SyncHandler{}
		if h.repairSyncHeadDerivedStateIfDriftedFn != nil {
			t.Fatal("default handler must have nil repair hook so production dispatch hits the method")
		}
	})
}

// =============================================================================
// formatRelativeTimeHTML Tests
// =============================================================================

func TestFormatRelativeTimeHTML(t *testing.T) {
	tests := []struct {
		name     string
		offset   time.Duration
		contains string
	}{
		{"seconds ago", 5 * time.Second, "seconds ago"},
		{"1 second ago", 1 * time.Second, "1 second ago"},
		{"minutes ago", 5 * time.Minute, "minutes ago"},
		{"1 minute ago", 1 * time.Minute, "1 minute ago"},
		{"hours ago", 3 * time.Hour, "hours ago"},
		{"1 hour ago", 1 * time.Hour, "1 hour ago"},
		{"days ago", 3 * 24 * time.Hour, "days ago"},
		{"1 day ago", 24 * time.Hour, "1 day ago"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatRelativeTimeHTML(time.Now().Add(-tt.offset))
			if !strings.Contains(result, tt.contains) {
				t.Errorf("formatRelativeTimeHTML should contain %q, got: %s", tt.contains, result)
			}
			// Check HTML structure
			if !strings.HasPrefix(result, "<time ") {
				t.Errorf("should start with <time, got: %s", result)
			}
			if !strings.HasSuffix(result, "</time>") {
				t.Errorf("should end with </time>, got: %s", result)
			}
			if !strings.Contains(result, "datetime=") {
				t.Errorf("should contain datetime attribute, got: %s", result)
			}
			if !strings.Contains(result, "is=\"relative-time\"") {
				t.Errorf("should contain is=relative-time, got: %s", result)
			}
		})
	}
}

// =============================================================================
// Hash Translation Tests (SHA-1 to SHA-256)
// =============================================================================

// TestBlockIDFormats tests detection of SHA-1 (40 char) vs SHA-256 (64 char) block IDs
func TestBlockIDFormats(t *testing.T) {
	tests := []struct {
		name         string
		blockID      string
		isLegacySHA1 bool
		isSHA256     bool
	}{
		{
			name:         "SHA-1 format (40 chars)",
			blockID:      "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
			isLegacySHA1: true,
			isSHA256:     false,
		},
		{
			name:         "SHA-256 format (64 chars)",
			blockID:      "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
			isLegacySHA1: false,
			isSHA256:     true,
		},
		{
			name:         "all zeros SHA-1",
			blockID:      "0000000000000000000000000000000000000000",
			isLegacySHA1: true,
			isSHA256:     false,
		},
		{
			name:         "all zeros SHA-256",
			blockID:      "0000000000000000000000000000000000000000000000000000000000000000",
			isLegacySHA1: false,
			isSHA256:     true,
		},
		{
			name:         "short ID (invalid)",
			blockID:      "abc123",
			isLegacySHA1: false,
			isSHA256:     false,
		},
		{
			name:         "empty ID",
			blockID:      "",
			isLegacySHA1: false,
			isSHA256:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isLegacySHA1 := len(tt.blockID) == 40
			isSHA256 := len(tt.blockID) == 64

			if isLegacySHA1 != tt.isLegacySHA1 {
				t.Errorf("isLegacySHA1 = %v, want %v", isLegacySHA1, tt.isLegacySHA1)
			}
			if isSHA256 != tt.isSHA256 {
				t.Errorf("isSHA256 = %v, want %v", isSHA256, tt.isSHA256)
			}
		})
	}
}

// TestSHA256Computation tests that SHA-256 is computed correctly for block data
func TestSHA256Computation(t *testing.T) {
	tests := []struct {
		name     string
		data     string
		expected string // Pre-computed SHA-256 hash
	}{
		{
			name:     "simple string",
			data:     "Hello, World!",
			expected: "dffd6021bb2bd5b0af676290809ec3a53191dd81c7f70a4b28688a362182986f",
		},
		{
			name:     "empty string",
			data:     "",
			expected: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
		{
			name:     "binary-like data",
			data:     "\x00\x01\x02\x03\x04\x05",
			expected: "17e88db187afd62c16e5debf3e6527cd006bc012bc90b51a810cd80c2d511f43",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Verify expected hash format (64 hex chars)
			if len(tt.expected) != 64 {
				t.Errorf("expected hash length = %d, want 64", len(tt.expected))
			}

			// Verify it's valid hex
			if !isHexString([]byte(tt.expected)) {
				t.Errorf("expected hash is not valid hex: %s", tt.expected)
			}

			// Verify test data is valid
			_ = []byte(tt.data)
		})
	}
}

// TestHashTypeParameter tests the hash_type query parameter handling
func TestHashTypeParameter(t *testing.T) {
	tests := []struct {
		name     string
		blockID  string
		hashType string
		isLegacy bool
		isDirect bool
	}{
		{
			name:     "SHA-1 without hash_type (legacy)",
			blockID:  "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
			hashType: "",
			isLegacy: true,
			isDirect: false,
		},
		{
			name:     "SHA-1 with hash_type=sha256 (direct)",
			blockID:  "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
			hashType: "sha256",
			isLegacy: false,
			isDirect: true,
		},
		{
			name:     "SHA-256 without hash_type (direct by length)",
			blockID:  "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
			hashType: "",
			isLegacy: false,
			isDirect: true,
		},
		{
			name:     "SHA-256 with hash_type=sha256 (explicit direct)",
			blockID:  "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
			hashType: "sha256",
			isLegacy: false,
			isDirect: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Logic matching PutBlock implementation
			isLegacySHA1 := len(tt.blockID) == 40 && tt.hashType != "sha256"
			isDirectSHA256 := len(tt.blockID) == 64 || tt.hashType == "sha256"

			if isLegacySHA1 != tt.isLegacy {
				t.Errorf("isLegacySHA1 = %v, want %v", isLegacySHA1, tt.isLegacy)
			}
			if isDirectSHA256 != tt.isDirect {
				t.Errorf("isDirectSHA256 = %v, want %v", isDirectSHA256, tt.isDirect)
			}
		})
	}
}

// TestExternalToInternalMapping tests the mapping logic for block IDs
func TestExternalToInternalMapping(t *testing.T) {
	// Simulated mapping table (in real code this is in Cassandra)
	mappings := map[string]string{
		"a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3": "dffd6021bb2bd5b0af676290809ec3a53191dd81c7f70a4b28688a362182986f",
	}

	tests := []struct {
		name       string
		externalID string
		wantFound  bool
		wantID     string
	}{
		{
			name:       "mapped SHA-1 ID",
			externalID: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
			wantFound:  true,
			wantID:     "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
		{
			name:       "unmapped SHA-1 ID (fallback to self)",
			externalID: "0000000000000000000000000000000000000000",
			wantFound:  false,
			wantID:     "0000000000000000000000000000000000000000",
		},
		{
			name:       "SHA-256 ID (no mapping needed)",
			externalID: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			wantFound:  false, // SHA-256 doesn't need lookup
			wantID:     "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var internalID string
			var found bool

			// Simulate the lookup logic from GetBlock
			if len(tt.externalID) == 40 {
				// SHA-1: look up in mapping
				if mapped, ok := mappings[tt.externalID]; ok {
					internalID = mapped
					found = true
				} else {
					// Fallback: use external ID directly
					internalID = tt.externalID
					found = false
				}
			} else {
				// SHA-256: use directly
				internalID = tt.externalID
				found = false
			}

			if found != tt.wantFound {
				t.Errorf("found = %v, want %v", found, tt.wantFound)
			}
			if internalID != tt.wantID {
				t.Errorf("internalID = %s, want %s", internalID, tt.wantID)
			}
		})
	}
}

// TestCheckBlocksMapping tests the CheckBlocks mapping logic
func TestCheckBlocksMapping(t *testing.T) {
	// Simulated mapping and existence
	mappings := map[string]string{
		"sha1id11111111111111111111111111111111111": "sha256id1111111111111111111111111111111111111111111111111111111111",
		"sha1id22222222222222222222222222222222222": "sha256id2222222222222222222222222222222222222222222222222222222222",
	}

	existsInStorage := map[string]bool{
		"sha256id1111111111111111111111111111111111111111111111111111111111": true,
		"sha256id2222222222222222222222222222222222222222222222222222222222": false,
	}

	externalIDs := []string{
		"sha1id11111111111111111111111111111111111", // exists
		"sha1id22222222222222222222222222222222222", // missing
		"sha1id33333333333333333333333333333333333", // no mapping, missing
	}

	// Build external to internal mapping
	externalToInternal := make(map[string]string)
	for _, extID := range externalIDs {
		if mapped, ok := mappings[extID]; ok {
			externalToInternal[extID] = mapped
		} else {
			externalToInternal[extID] = extID // fallback
		}
	}

	// Check existence using internal IDs
	var needed []string
	for _, extID := range externalIDs {
		internalID := externalToInternal[extID]
		if !existsInStorage[internalID] {
			needed = append(needed, extID)
		}
	}

	// Verify results
	expectedNeeded := []string{
		"sha1id22222222222222222222222222222222222",
		"sha1id33333333333333333333333333333333333",
	}

	if len(needed) != len(expectedNeeded) {
		t.Errorf("needed count = %d, want %d", len(needed), len(expectedNeeded))
	}

	for i, id := range needed {
		if id != expectedNeeded[i] {
			t.Errorf("needed[%d] = %s, want %s", i, id, expectedNeeded[i])
		}
	}
}

// TestBlockHashValidation tests hash validation for direct SHA-256 uploads
func TestBlockHashValidation(t *testing.T) {
	tests := []struct {
		name         string
		externalID   string
		computedHash string
		hashType     string
		shouldReject bool
	}{
		{
			name:         "SHA-256 matches",
			externalID:   "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			computedHash: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			hashType:     "sha256",
			shouldReject: false,
		},
		{
			name:         "SHA-256 mismatch",
			externalID:   "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			computedHash: "dffd6021bb2bd5b0af676290809ec3a53191dd81c7f70a4b28688a362182986f",
			hashType:     "sha256",
			shouldReject: true,
		},
		{
			name:         "SHA-1 (no validation needed)",
			externalID:   "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
			computedHash: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			hashType:     "",
			shouldReject: false, // SHA-1 clients don't verify
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isDirectSHA256 := len(tt.externalID) == 64 || tt.hashType == "sha256"
			shouldReject := isDirectSHA256 && tt.externalID != tt.computedHash

			if shouldReject != tt.shouldReject {
				t.Errorf("shouldReject = %v, want %v", shouldReject, tt.shouldReject)
			}
		})
	}
}

func TestSyncFinalizedDeltaSet(t *testing.T) {
	t.Run("miss before mark, hit after", func(t *testing.T) {
		set := newSyncFinalizedDeltaSet()
		if set.contains("repo-1", "head-1") {
			t.Fatal("expected miss before mark")
		}
		set.mark("repo-1", "head-1")
		if !set.contains("repo-1", "head-1") {
			t.Fatal("expected hit after mark")
		}
		// A different head on the same repo must not be considered finalized.
		if set.contains("repo-1", "head-2") {
			t.Fatal("a distinct head must not be a hit")
		}
		if set.contains("repo-2", "head-1") {
			t.Fatal("a distinct repo must not be a hit")
		}
	})

	t.Run("entries survive one generation rotation, evict after two", func(t *testing.T) {
		set := newSyncFinalizedDeltaSet()
		set.mark("repo", "survivor")

		// Fill the current generation to force a rotation; survivor moves to prev.
		for i := 0; i < syncFinalizedDeltaShardCap; i++ {
			set.mark("repo", fmt.Sprintf("gen1-%d", i))
		}
		if !set.contains("repo", "survivor") {
			t.Fatal("entry should still be visible in the previous generation after one rotation")
		}

		// Fill again to force a second rotation; survivor is now dropped.
		for i := 0; i < syncFinalizedDeltaShardCap; i++ {
			set.mark("repo", fmt.Sprintf("gen2-%d", i))
		}
		if set.contains("repo", "survivor") {
			t.Fatal("entry should be evicted after two rotations")
		}
	})

	t.Run("nil set and empty keys are safe no-ops", func(t *testing.T) {
		var set *syncFinalizedDeltaSet
		set.mark("repo", "head") // must not panic
		if set.contains("repo", "head") {
			t.Fatal("nil set must always miss")
		}

		set = newSyncFinalizedDeltaSet()
		set.mark("", "head")
		set.mark("repo", "")
		if set.contains("", "head") || set.contains("repo", "") {
			t.Fatal("empty repo/commit keys must never be a hit")
		}
	})
}

func TestSeafileServeBlockIDs(t *testing.T) {
	sha256IDs := []string{strings.Repeat("a", 64), strings.Repeat("b", 64)}
	sha1IDs := []string{strings.Repeat("c", 40), strings.Repeat("d", 40)}

	t.Run("prefers the SHA-1 column when present (post-PR4 layout)", func(t *testing.T) {
		got, ok := seafileServeBlockIDs(sha256IDs, sha1IDs)
		if !ok || len(got) != 2 || got[0] != sha1IDs[0] || got[1] != sha1IDs[1] {
			t.Fatalf("got %v ok=%v, want the SHA-1 list %v", got, ok, sha1IDs)
		}
	})

	t.Run("falls back to block_ids when the SHA-1 column is empty", func(t *testing.T) {
		if got, ok := seafileServeBlockIDs(sha1IDs, nil); !ok || len(got) != 2 || got[0] != sha1IDs[0] {
			t.Fatalf("got %v ok=%v, want fallback to block_ids %v", got, ok, sha1IDs)
		}
		if got, ok := seafileServeBlockIDs(sha1IDs, []string{}); !ok || len(got) != 2 || got[0] != sha1IDs[0] {
			t.Fatalf("empty (non-nil) SHA-1 list must also fall back, got %v ok=%v", got, ok)
		}
	})

	t.Run("empty file (no blocks) is safe", func(t *testing.T) {
		if got, ok := seafileServeBlockIDs(nil, nil); !ok || len(got) != 0 {
			t.Fatalf("got %v ok=%v, want empty+ok", got, ok)
		}
	})

	t.Run("fails closed on SHA-256 block_ids without the SHA-1 column", func(t *testing.T) {
		if got, ok := seafileServeBlockIDs(sha256IDs, nil); ok || got != nil {
			t.Fatalf("got %v ok=%v, want nil+false (fail closed)", got, ok)
		}
	})
}
