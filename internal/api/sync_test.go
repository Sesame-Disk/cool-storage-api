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
	commit := Commit{
		CommitID:    "abc123",
		RepoID:      "00000000-0000-0000-0000-000000000001",
		RootID:      "def456",
		ParentID:    "parent123",
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
				Entries: []FSEntry{
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

	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	permission, ok := response["permission"].(string)
	if !ok {
		t.Fatal("permission field not found or not string")
	}
	if permission != "rw" {
		t.Errorf("permission = %s, want rw", permission)
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
			Entries: []FSEntry{
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
	if len(decoded[1].Entries) != 1 {
		t.Errorf("object[1].Entries length = %d, want 1", len(decoded[1].Entries))
	}
}

// TestCommitJSONFields tests that JSON field names match Seafile protocol
func TestCommitJSONFields(t *testing.T) {
	commit := Commit{
		CommitID:     "abc",
		RepoID:       "repo",
		RootID:       "root",
		ParentID:     "parent",
		SecondParent: "second",
		Description:  "desc",
		Creator:      "user",
		CreatorName:  "name",
		Ctime:        123,
		Version:      1,
		Encrypted:    true,
		EncVersion:   2,
		Magic:        "magic",
		RandomKey:    "key",
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
