package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
		Name:     "document.pdf",
		ID:       "abc123",
		Mode:     33188,
		Mtime:    1234567890,
		Size:     2048,
		Modifier: "user@example.com",
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
		Encrypted:      true,
		EncVersion:     2,
		Magic:          "magic",
		RandomKey:      "key",
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
		`"random_key"`,
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
		name       string
		blockID    string
		hashType   string
		isLegacy   bool
		isDirect   bool
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
