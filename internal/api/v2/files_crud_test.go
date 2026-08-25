package v2

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// Test DirectoryOperation routing
func TestDirectoryOperation_Routing(t *testing.T) {
	r := gin.New()
	handler := &FileHandler{}

	// Mock routes
	r.POST("/dir", handler.DirectoryOperation)

	tests := []struct {
		name       string
		operation  string
		wantCalled string
	}{
		{"default mkdir", "", "mkdir"},
		{"explicit mkdir", "mkdir", "mkdir"},
		{"rename", "rename", "rename"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := "/dir"
			if tt.operation != "" {
				url += "?operation=" + tt.operation
			}
			req := httptest.NewRequest("POST", url, nil)
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			// The handler will fail due to missing DB, but we can verify routing
			// Just ensure it doesn't return 404 (route matched)
			if w.Code == http.StatusNotFound {
				t.Errorf("Route not found for operation=%q", tt.operation)
			}
		})
	}
}

// Test DirectoryOperation invalid operation
func TestDirectoryOperation_InvalidOperation(t *testing.T) {
	r := gin.New()
	handler := &FileHandler{}

	r.POST("/dir", handler.DirectoryOperation)

	req := httptest.NewRequest("POST", "/dir?operation=invalid", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// Test FileOperation routing
func TestFileOperation_Routing(t *testing.T) {
	r := gin.New()
	handler := &FileHandler{}

	r.POST("/file", handler.FileOperation)

	tests := []struct {
		name       string
		operation  string
		wantStatus int
	}{
		{"no operation", "", http.StatusBadRequest},
		{"rename", "rename", http.StatusBadRequest}, // Will fail due to missing params, but route matches
		{"create", "create", http.StatusBadRequest}, // Will fail due to missing params, but route matches
		{"invalid", "invalid", http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := "/file"
			if tt.operation != "" {
				url += "?operation=" + tt.operation
			}
			req := httptest.NewRequest("POST", url, nil)
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

// Test DeleteFile missing path
func TestDeleteFile_MissingPath(t *testing.T) {
	r := gin.New()
	handler := &FileHandler{}

	r.DELETE("/file", handler.DeleteFile)

	req := httptest.NewRequest("DELETE", "/file", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// Test DeleteFile root path
func TestDeleteFile_RootPath(t *testing.T) {
	r := gin.New()
	handler := &FileHandler{}

	r.DELETE("/file", handler.DeleteFile)

	req := httptest.NewRequest("DELETE", "/file?p=/", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// Test DeleteDirectory invalid path
func TestDeleteDirectory_InvalidPath(t *testing.T) {
	r := gin.New()
	handler := &FileHandler{}

	r.DELETE("/dir", handler.DeleteDirectory)

	tests := []struct {
		name string
		path string
	}{
		{"empty path", ""},
		{"root path", "/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := "/dir"
			if tt.path != "" {
				url += "?p=" + tt.path
			}
			req := httptest.NewRequest("DELETE", url, nil)
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestResolveLibraryBlockStoreBuildsOrgScopedS3Fallback(t *testing.T) {
	const orgID = "00000000-0000-0000-0000-000000000001"
	h := &FileHandler{
		config:  &config.Config{Storage: config.StorageConfig{DefaultClass: "hot-minio-local"}},
		storage: &storage.S3Store{},
	}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v2.1/repos/repo-id/file/", nil)
	c.Request.Host = "localhost"

	blockStore, storageClass, err := h.resolveLibraryBlockStore(c, orgID, "repo-id")
	if err != nil {
		t.Fatalf("resolveLibraryBlockStore returned error: %v", err)
	}
	if blockStore == nil {
		t.Fatal("expected blockStore, got nil")
	}
	if storageClass != "hot-minio-local" {
		t.Fatalf("storageClass = %q, want %q", storageClass, "hot-minio-local")
	}
	if got, want := blockStore.StorageKeyForHash("abcd1234"), "blocks/"+orgID+"/ab/cd/abcd1234"; got != want {
		t.Fatalf("StorageKeyForHash() = %q, want %q", got, want)
	}
}

func TestResolveLibraryBlockStoreUsesStorageManager(t *testing.T) {
	manager := storage.NewManager()
	manager.SetDefaultClass("hot-s3-eu")
	manager.RegisterBackend("hot-s3-eu", &storage.S3Store{}, "")

	h := &FileHandler{
		config:         &config.Config{Storage: config.StorageConfig{DefaultClass: "hot-s3-eu"}},
		storageManager: manager,
	}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v2.1/repos/repo-id/file/", nil)
	c.Request.Host = "files.example.com"

	blockStore, storageClass, err := h.resolveLibraryBlockStore(c, "00000000-0000-0000-0000-000000000001", "repo-id")
	if err != nil {
		t.Fatalf("resolveLibraryBlockStore returned error: %v", err)
	}
	if blockStore == nil {
		t.Fatal("expected blockStore, got nil")
	}
	if storageClass != "hot-s3-eu" {
		t.Fatalf("storageClass = %q, want %q", storageClass, "hot-s3-eu")
	}
}

// Test RenameFile missing params
func TestRenameFile_MissingParams(t *testing.T) {
	r := gin.New()
	handler := &FileHandler{}

	r.POST("/file", func(c *gin.Context) {
		c.Request.URL.RawQuery = "operation=rename"
		handler.FileOperation(c)
	})

	tests := []struct {
		name     string
		path     string
		newname  string
		wantCode int
	}{
		{"missing path", "", "newname.txt", http.StatusBadRequest},
		{"missing newname", "/file.txt", "", http.StatusBadRequest},
		{"root path", "/", "newname.txt", http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			form := url.Values{}
			if tt.newname != "" {
				form.Set("newname", tt.newname)
			}

			url := "/file?operation=rename"
			if tt.path != "" {
				url += "&p=" + tt.path
			}

			req := httptest.NewRequest("POST", url, strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if w.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", w.Code, tt.wantCode)
			}
		})
	}
}

// Test RenameDirectory missing params
func TestRenameDirectory_MissingParams(t *testing.T) {
	r := gin.New()
	handler := &FileHandler{}

	r.POST("/dir", func(c *gin.Context) {
		c.Request.URL.RawQuery = "operation=rename"
		handler.DirectoryOperation(c)
	})

	tests := []struct {
		name     string
		path     string
		newname  string
		wantCode int
	}{
		{"missing path", "", "newdir", http.StatusBadRequest},
		{"root path", "/", "newdir", http.StatusBadRequest},
		{"missing newname", "/dir", "", http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			form := url.Values{}
			if tt.newname != "" {
				form.Set("newname", tt.newname)
			}

			url := "/dir?operation=rename"
			if tt.path != "" {
				url += "&p=" + tt.path
			}

			req := httptest.NewRequest("POST", url, strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if w.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", w.Code, tt.wantCode)
			}
		})
	}
}

// Test CreateFile missing path
func TestCreateFile_MissingPath(t *testing.T) {
	r := gin.New()
	handler := &FileHandler{}

	r.POST("/file", func(c *gin.Context) {
		c.Request.URL.RawQuery = "operation=create"
		handler.FileOperation(c)
	})

	req := httptest.NewRequest("POST", "/file?operation=create", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestRetryCreateFileTemplateBlockMaterializationRetriesFencedBlock(t *testing.T) {
	oldBackoff := createFileTemplateBlockRetryBackoffFn
	oldSleep := createFileTemplateBlockSleepFn
	t.Cleanup(func() {
		createFileTemplateBlockRetryBackoffFn = oldBackoff
		createFileTemplateBlockSleepFn = oldSleep
	})

	createFileTemplateBlockRetryBackoffFn = func(attempt int) time.Duration { return 0 }
	createFileTemplateBlockSleepFn = func(time.Duration) {}

	storeCalls := 0
	registerCalls := 0
	resetCalls := 0
	err := retryCreateFileTemplateBlockMaterialization(func() error {
		storeCalls++
		return nil
	}, func() error {
		registerCalls++
		if registerCalls == 1 {
			return fmt.Errorf("register template block: %w", ErrBlockDeleteInProgress)
		}
		return nil
	}, func() {
		resetCalls++
	})
	if err != nil {
		t.Fatalf("retryCreateFileTemplateBlockMaterialization() error = %v, want nil", err)
	}
	if storeCalls != 3 {
		t.Fatalf("storeCalls = %d, want 3 (retry plus confirmation)", storeCalls)
	}
	if registerCalls != 2 {
		t.Fatalf("registerCalls = %d, want 2", registerCalls)
	}
	if resetCalls != 2 {
		t.Fatalf("resetCalls = %d, want 2 (retry plus confirmation)", resetCalls)
	}
}

func TestRetryCreateFileTemplateBlockMaterializationStopsOnNonRetryableError(t *testing.T) {
	storeCalls := 0
	registerCalls := 0
	wantErr := errors.New("boom")
	err := retryCreateFileTemplateBlockMaterialization(func() error {
		storeCalls++
		return nil
	}, func() error {
		registerCalls++
		return wantErr
	}, func() {
		t.Fatal("resetStored should not run for non-retryable errors")
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("retryCreateFileTemplateBlockMaterialization() error = %v, want %v", err, wantErr)
	}
	if storeCalls != 1 {
		t.Fatalf("storeCalls = %d, want 1", storeCalls)
	}
	if registerCalls != 1 {
		t.Fatalf("registerCalls = %d, want 1", registerCalls)
	}
}

func TestCreateFileTemplateTargetSurvivesHeadConflictWithoutRemintOrPut(t *testing.T) {
	oldHeadDelay := libraryHeadMutationRetryDelay
	oldHeadJitter := libraryHeadMutationRetryJitter
	t.Cleanup(func() {
		libraryHeadMutationRetryDelay = oldHeadDelay
		libraryHeadMutationRetryJitter = oldHeadJitter
	})
	libraryHeadMutationRetryDelay = 0
	libraryHeadMutationRetryJitter = 0

	want := BlockMaterializationTarget{StorageClass: "hot", StorageKey: "blocks/org/ab/cd/hash.minted", FreshInstall: true}
	target := BlockMaterializationTarget{}
	stored := false
	canonicalInstalled := false
	putCalls := 0
	registerCalls := 0
	outerAttempts := 0

	err := retryLibraryHeadMutation("CreateFile", func() error {
		outerAttempts++
		if err := retryCreateFileTemplateBlockMaterialization(func() error {
			if stored {
				return nil
			}
			if canonicalInstalled {
				target = want
				target.FreshInstall = false
			} else {
				target = want
				putCalls++
			}
			stored = true
			return nil
		}, func() error {
			registerCalls++
			if target.StorageClass != want.StorageClass || target.StorageKey != want.StorageKey {
				t.Fatalf("registration target = %+v, want exact %+v", target, want)
			}
			canonicalInstalled = true
			return nil
		}, func() {
			stored = false
			target = BlockMaterializationTarget{}
		}); err != nil {
			return err
		}
		if outerAttempts == 1 {
			return ErrLibraryHeadConflict
		}
		return nil
	})
	if err != nil {
		t.Fatalf("forced-conflict CreateFile materialization: %v", err)
	}
	if outerAttempts != 2 || registerCalls != 2 || putCalls != 1 {
		t.Fatalf("attempts/registers/PUTs = %d/%d/%d, want 2/2/1", outerAttempts, registerCalls, putCalls)
	}
	if !stored || target.StorageClass != want.StorageClass || target.StorageKey != want.StorageKey || target.FreshInstall {
		t.Fatalf("surviving state = stored:%v target:%+v, want exact canonical target", stored, target)
	}
}

// Test CreateDirectory root path
func TestCreateDirectory_RootPath(t *testing.T) {
	r := gin.New()
	handler := &FileHandler{}

	r.POST("/dir", handler.DirectoryOperation)

	req := httptest.NewRequest("POST", "/dir?p=/", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// Test GetFileInfo missing path
func TestGetFileInfo_MissingPath(t *testing.T) {
	r := gin.New()
	handler := &FileHandler{}

	r.GET("/file", handler.GetFileInfo)

	req := httptest.NewRequest("GET", "/file", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// Test GetFileDetail missing path
func TestGetFileDetail_MissingPath(t *testing.T) {
	r := gin.New()
	handler := &FileHandler{}

	r.GET("/file/detail", handler.GetFileDetail)

	req := httptest.NewRequest("GET", "/file/detail", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// Test MoveFile missing params
func TestMoveFile_MissingParams(t *testing.T) {
	r := gin.New()
	handler := &FileHandler{}

	r.POST("/file/move", handler.MoveFile)

	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{"missing src", `{"dst_dir":"/dest/"}`, true},
		{"missing dst", `{"src_path":"/source/file.txt"}`, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/file/move", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if tt.wantErr && w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}
		})
	}
}

// Test CopyFile missing params
func TestCopyFile_MissingParams(t *testing.T) {
	r := gin.New()
	handler := &FileHandler{}

	r.POST("/file/copy", handler.CopyFile)

	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{"missing src", `{"dst_dir":"/dest/"}`, true},
		{"missing dst", `{"src_path":"/source/file.txt"}`, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/file/copy", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if tt.wantErr && w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}
		})
	}
}

// Test MoveFileRequest struct binding
func TestMoveFileRequest_StructBinding(t *testing.T) {
	r := gin.New()

	r.POST("/test", func(c *gin.Context) {
		var req MoveFileRequest
		if err := c.ShouldBind(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, req)
	})

	tests := []struct {
		name       string
		body       string
		wantFields map[string]string
	}{
		{
			name: "new format",
			body: `{"src_repo_id":"repo1","src_path":"/old.txt","dst_repo_id":"repo2","dst_dir":"/new/"}`,
			wantFields: map[string]string{
				"SrcRepoID": "repo1",
				"SrcPath":   "/old.txt",
				"DstRepoID": "repo2",
			},
		},
		{
			name: "legacy format with filename",
			body: `{"src_dir":"/path/","filename":"file.txt"}`,
			wantFields: map[string]string{
				"SrcDir":   "/path/",
				"Filename": "file.txt",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/test", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
				return
			}

			var result map[string]interface{}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatalf("Failed to parse response: %v", err)
			}

			// Response uses JSON field names, convert to check
			fieldMap := map[string]string{
				"SrcRepoID": "src_repo_id",
				"SrcPath":   "src_path",
				"DstRepoID": "dst_repo_id",
				"DstDir":    "dst_dir",
				"SrcDir":    "src_dir",
				"Filename":  "filename",
			}

			for field, expected := range tt.wantFields {
				jsonField := fieldMap[field]
				if result[jsonField] != expected {
					t.Errorf("%s = %v, want %q", field, result[jsonField], expected)
				}
			}
		})
	}
}

// Test CopyFileRequest struct binding
func TestCopyFileRequest_StructBinding(t *testing.T) {
	r := gin.New()

	r.POST("/test", func(c *gin.Context) {
		var req CopyFileRequest
		if err := c.ShouldBind(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, req)
	})

	body := `{"src_repo_id":"repo1","src_path":"/source.txt","dst_repo_id":"repo2","dst_dir":"/dest/"}`
	req := httptest.NewRequest("POST", "/test", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}
