package v2

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// setupBatchRouter creates a test router with batch operation handler (nil DB)
func setupBatchRouter() (*gin.Engine, *BatchOperationHandler) {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		c.Set("org_id", "00000000-0000-0000-0000-000000000001")
		c.Set("user_id", "00000000-0000-0000-0000-000000000001")
		c.Next()
	})

	h := &BatchOperationHandler{
		db:             nil,
		config:         nil,
		permMiddleware: nil, // nil = skip permission checks
		tasks:          &TaskStore{tasks: make(map[string]*AsyncTask)},
	}

	return r, h
}

func TestIsSameLocationMove(t *testing.T) {
	tests := []struct {
		name          string
		opType        string
		srcRepoID     string
		dstRepoID     string
		srcParentPath string
		dstDir        string
		want          bool
	}{
		{name: "same repo and dir move", opType: "move", srcRepoID: "repo", dstRepoID: "repo", srcParentPath: "/dir", dstDir: "/dir", want: true},
		{name: "normalized paths", opType: "move", srcRepoID: "repo", dstRepoID: "repo", srcParentPath: "dir", dstDir: "/dir/", want: true},
		{name: "copy is not same-location move", opType: "copy", srcRepoID: "repo", dstRepoID: "repo", srcParentPath: "/dir", dstDir: "/dir", want: false},
		{name: "different dir", opType: "move", srcRepoID: "repo", dstRepoID: "repo", srcParentPath: "/src", dstDir: "/dst", want: false},
		{name: "different repo", opType: "move", srcRepoID: "src", dstRepoID: "dst", srcParentPath: "/dir", dstDir: "/dir", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSameLocationMove(tt.opType, tt.srcRepoID, tt.dstRepoID, tt.srcParentPath, tt.dstDir); got != tt.want {
				t.Fatalf("isSameLocationMove() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestShouldSkipSourceRemovalAfterMove(t *testing.T) {
	tests := []struct {
		name   string
		result *PathTraverseResult
		err    error
		want   bool
	}{
		{name: "traverse error", err: errors.New("boom"), want: true},
		{name: "nil result", want: true},
		{name: "missing target entry", result: &PathTraverseResult{}, want: true},
		{name: "existing target entry", result: &PathTraverseResult{TargetEntry: &FSEntry{Name: "file.txt"}}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldSkipSourceRemovalAfterMove(tt.result, tt.err); got != tt.want {
				t.Fatalf("shouldSkipSourceRemovalAfterMove() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsPathWithin(t *testing.T) {
	tests := []struct {
		name      string
		candidate string
		parent    string
		want      bool
	}{
		{name: "same path", candidate: "/src", parent: "/src", want: true},
		{name: "child path", candidate: "/src/child", parent: "/src", want: true},
		{name: "sibling prefix is not child", candidate: "/src2/child", parent: "/src", want: false},
		{name: "root contains child", candidate: "/src", parent: "/", want: true},
		{name: "root does not contain itself", candidate: "/", parent: "/", want: false},
		{name: "normalizes paths", candidate: "src/child/", parent: "/src", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPathWithin(tt.candidate, tt.parent); got != tt.want {
				t.Fatalf("isPathWithin(%q, %q) = %v, want %v", tt.candidate, tt.parent, got, tt.want)
			}
		})
	}
}

func TestReplacedDestinationTagCleanup(t *testing.T) {
	tests := []struct {
		name         string
		dstDir       string
		itemName     string
		entry        *FSEntry
		wantPath     string
		wantByPrefix bool
	}{
		{name: "no replaced entry", dstDir: "/dst", itemName: "file.txt", wantPath: "", wantByPrefix: false},
		{name: "root destination file", dstDir: "/", itemName: "file.txt", entry: &FSEntry{Name: "file.txt", Mode: ModeFile}, wantPath: "/file.txt", wantByPrefix: false},
		{name: "nested destination file", dstDir: "/dst/", itemName: "file.txt", entry: &FSEntry{Name: "file.txt", Mode: ModeFile}, wantPath: "/dst/file.txt", wantByPrefix: false},
		{name: "directory cleanup uses prefix", dstDir: "/dst", itemName: "dir", entry: &FSEntry{Name: "dir", Mode: ModeDir}, wantPath: "/dst/dir", wantByPrefix: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPath, gotByPrefix := replacedDestinationTagCleanup(tt.dstDir, tt.itemName, tt.entry)
			if gotPath != tt.wantPath {
				t.Fatalf("replacedDestinationTagCleanup() path = %q, want %q", gotPath, tt.wantPath)
			}
			if gotByPrefix != tt.wantByPrefix {
				t.Fatalf("replacedDestinationTagCleanup() byPrefix = %v, want %v", gotByPrefix, tt.wantByPrefix)
			}
		})
	}
}

func TestMovedItemTagMutation(t *testing.T) {
	tests := []struct {
		name         string
		srcPath      string
		dstDir       string
		itemName     string
		entry        *FSEntry
		wantOldPath  string
		wantNewPath  string
		wantByPrefix bool
	}{
		{name: "missing entry", srcPath: "/src/file.txt", dstDir: "/dst", itemName: "file.txt"},
		{name: "file move", srcPath: "/src/file.txt", dstDir: "/dst", itemName: "file.txt", entry: &FSEntry{Name: "file.txt", Mode: ModeFile}, wantOldPath: "/src/file.txt", wantNewPath: "/dst/file.txt", wantByPrefix: false},
		{name: "directory move", srcPath: "/src/dir", dstDir: "/dst", itemName: "dir", entry: &FSEntry{Name: "dir", Mode: ModeDir}, wantOldPath: "/src/dir", wantNewPath: "/dst/dir", wantByPrefix: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotOldPath, gotNewPath, gotByPrefix := movedItemTagMutation(tt.srcPath, tt.dstDir, tt.itemName, tt.entry)
			if gotOldPath != tt.wantOldPath {
				t.Fatalf("movedItemTagMutation() oldPath = %q, want %q", gotOldPath, tt.wantOldPath)
			}
			if gotNewPath != tt.wantNewPath {
				t.Fatalf("movedItemTagMutation() newPath = %q, want %q", gotNewPath, tt.wantNewPath)
			}
			if gotByPrefix != tt.wantByPrefix {
				t.Fatalf("movedItemTagMutation() byPrefix = %v, want %v", gotByPrefix, tt.wantByPrefix)
			}
		})
	}
}

func TestEnqueueZeroRefBlocks_GroupsByCanonicalBlockStorageClass(t *testing.T) {
	oldLoad := loadZeroRefBlockStorageClassesFn
	defer func() {
		loadZeroRefBlockStorageClassesFn = oldLoad
		SetGCHooks(nil, nil, nil)
	}()

	enqueuer := &mockGCEnqueuer{}
	SetGCHooks(enqueuer, nil, nil)
	loadZeroRefBlockStorageClassesFn = func(database *db.DB, orgID string, blockIDs []string) (map[string][]string, error) {
		if database == nil {
			t.Fatal("expected non-nil database placeholder")
		}
		if orgID != "org-1" {
			t.Fatalf("orgID = %q, want org-1", orgID)
		}
		if !reflect.DeepEqual(blockIDs, []string{"block-hot-1", "block-cold-1", "block-hot-2"}) {
			t.Fatalf("blockIDs = %#v, want original order", blockIDs)
		}
		return map[string][]string{
			"hot-a":  {"block-hot-1", "block-hot-2"},
			"cold-b": {"block-cold-1"},
		}, nil
	}

	enqueueZeroRefBlocks(&db.DB{}, "org-1", "repo-1", []string{"block-hot-1", "block-cold-1", "block-hot-2"})

	want := []gcEnqueueCall{
		{orgID: "org-1", blockIDs: []string{"block-cold-1"}, storageClass: "cold-b"},
		{orgID: "org-1", blockIDs: []string{"block-hot-1", "block-hot-2"}, storageClass: "hot-a"},
	}
	if !reflect.DeepEqual(enqueuer.calls, want) {
		t.Fatalf("enqueue calls = %#v, want %#v", enqueuer.calls, want)
	}
}

func TestBatchOperationErrorResponse_MapsKnownErrors(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		wantStatus   int
		wantError    string
		wantReason   string
		wantConflict []string
	}{
		{name: "head conflict", err: ErrLibraryHeadConflict, wantStatus: http.StatusConflict, wantError: "library was modified concurrently; retry the move", wantReason: "library was modified concurrently; retry the move"},
		{name: "source missing", err: ErrBatchSourceNotFound, wantStatus: http.StatusNotFound, wantError: "source item not found", wantReason: "source item not found"},
		{name: "destination missing", err: ErrBatchDestinationNotFound, wantStatus: http.StatusNotFound, wantError: "destination directory not found", wantReason: "destination directory not found"},
		{name: "cross representation", err: ErrBatchCrossRepresentationUnsupported, wantStatus: http.StatusBadRequest, wantError: "source and destination libraries use different block representations", wantReason: "source and destination libraries use different block representations"},
		{name: "quota exceeded", err: ErrStorageQuotaExceeded, wantStatus: http.StatusForbidden, wantError: "storage quota exceeded", wantReason: "storage quota exceeded"},
		{name: "conflict", err: &ConflictError{ItemName: "renamed.txt"}, wantStatus: http.StatusConflict, wantError: "conflict", wantReason: "conflict", wantConflict: []string{"renamed.txt"}},
		{name: "generic", err: errors.New("boom"), wantStatus: http.StatusInternalServerError, wantError: "failed to move file.txt", wantReason: "failed to move file.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, payload, reason := batchOperationErrorResponse(tt.err, "move", "file.txt")
			if status != tt.wantStatus {
				t.Fatalf("status = %d, want %d", status, tt.wantStatus)
			}
			if got := payload["error"]; got != tt.wantError {
				t.Fatalf("error = %v, want %q", got, tt.wantError)
			}
			if reason != tt.wantReason {
				t.Fatalf("reason = %q, want %q", reason, tt.wantReason)
			}
			if tt.wantConflict != nil {
				items, ok := payload["conflicting_items"].([]string)
				if !ok {
					t.Fatalf("conflicting_items type = %T, want []string", payload["conflicting_items"])
				}
				if len(items) != len(tt.wantConflict) {
					t.Fatalf("conflicting_items len = %d, want %d", len(items), len(tt.wantConflict))
				}
				for i, want := range tt.wantConflict {
					if items[i] != want {
						t.Fatalf("conflicting_items[%d] = %q, want %q", i, items[i], want)
					}
				}
			}
		})
	}
}

func TestSyncBatchMove_InvalidJSON(t *testing.T) {
	r, h := setupBatchRouter()
	r.POST("/api/v2.1/repos/sync-batch-move-item/", h.SyncBatchMove)

	req, _ := http.NewRequest("POST", "/api/v2.1/repos/sync-batch-move-item/", bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestSyncBatchMove_MissingSrcRepoID(t *testing.T) {
	r, h := setupBatchRouter()
	r.POST("/api/v2.1/repos/sync-batch-move-item/", h.SyncBatchMove)

	body := BatchRequest{
		SrcRepoID:    "",
		DstRepoID:    "dst-repo",
		SrcParentDir: "/",
		DstParentDir: "/",
		SrcDirents:   []string{"file.txt"},
	}
	jsonBody, _ := json.Marshal(body)

	req, _ := http.NewRequest("POST", "/api/v2.1/repos/sync-batch-move-item/", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != "src_repo_id is required" {
		t.Errorf("error = %v, want 'src_repo_id is required'", resp["error"])
	}
}

func TestSyncBatchMove_MissingDstRepoID(t *testing.T) {
	r, h := setupBatchRouter()
	r.POST("/api/v2.1/repos/sync-batch-move-item/", h.SyncBatchMove)

	body := BatchRequest{
		SrcRepoID:    "src-repo",
		DstRepoID:    "",
		SrcParentDir: "/",
		DstParentDir: "/",
		SrcDirents:   []string{"file.txt"},
	}
	jsonBody, _ := json.Marshal(body)

	req, _ := http.NewRequest("POST", "/api/v2.1/repos/sync-batch-move-item/", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != "dst_repo_id is required" {
		t.Errorf("error = %v, want 'dst_repo_id is required'", resp["error"])
	}
}

func TestSyncBatchMove_EmptyDirents(t *testing.T) {
	r, h := setupBatchRouter()
	r.POST("/api/v2.1/repos/sync-batch-move-item/", h.SyncBatchMove)

	body := BatchRequest{
		SrcRepoID:    "src-repo",
		DstRepoID:    "dst-repo",
		SrcParentDir: "/",
		DstParentDir: "/",
		SrcDirents:   []string{},
	}
	jsonBody, _ := json.Marshal(body)

	req, _ := http.NewRequest("POST", "/api/v2.1/repos/sync-batch-move-item/", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != "src_dirents is required" {
		t.Errorf("error = %v, want 'src_dirents is required'", resp["error"])
	}
}

func TestSyncBatchCopy_InvalidJSON(t *testing.T) {
	r, h := setupBatchRouter()
	r.POST("/api/v2.1/repos/sync-batch-copy-item/", h.SyncBatchCopy)

	req, _ := http.NewRequest("POST", "/api/v2.1/repos/sync-batch-copy-item/", bytes.NewBufferString("{bad"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestGetTaskProgress_MissingTaskID(t *testing.T) {
	r, h := setupBatchRouter()
	r.GET("/api/v2.1/copy-move-task/", h.GetTaskProgress)

	req, _ := http.NewRequest("GET", "/api/v2.1/copy-move-task/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != "task_id is required" {
		t.Errorf("error = %v, want 'task_id is required'", resp["error"])
	}
}

func TestGetTaskProgress_NotFound(t *testing.T) {
	r, h := setupBatchRouter()
	r.GET("/api/v2.1/copy-move-task/", h.GetTaskProgress)

	req, _ := http.NewRequest("GET", "/api/v2.1/copy-move-task/?task_id=nonexistent", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestGetTaskProgress_ExistingTask(t *testing.T) {
	r, h := setupBatchRouter()
	r.GET("/api/v2.1/copy-move-task/", h.GetTaskProgress)

	// Add a task to the store
	h.tasks.mu.Lock()
	h.tasks.tasks["test-task-123"] = &AsyncTask{
		ID:     "test-task-123",
		Type:   "copy",
		Status: "done",
		Total:  5,
		Done:   4,
		Failed: 1,
	}
	h.tasks.mu.Unlock()

	req, _ := http.NewRequest("GET", "/api/v2.1/copy-move-task/?task_id=test-task-123", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp["task_id"] != "test-task-123" {
		t.Errorf("task_id = %v, want test-task-123", resp["task_id"])
	}
	if resp["done"] != true {
		t.Errorf("done = %v, want true", resp["done"])
	}
	if resp["total"] != float64(5) {
		t.Errorf("total = %v, want 5", resp["total"])
	}
	if resp["successful"] != float64(4) {
		t.Errorf("successful = %v, want 4", resp["successful"])
	}
	if resp["failed"] != float64(1) {
		t.Errorf("failed = %v, want 1", resp["failed"])
	}
}

func TestGetTaskProgress_ProcessingTask(t *testing.T) {
	r, h := setupBatchRouter()
	r.GET("/api/v2.1/copy-move-task/", h.GetTaskProgress)

	h.tasks.mu.Lock()
	h.tasks.tasks["in-progress"] = &AsyncTask{
		ID:     "in-progress",
		Type:   "move",
		Status: "processing",
		Total:  10,
		Done:   3,
		Failed: 0,
	}
	h.tasks.mu.Unlock()

	req, _ := http.NewRequest("GET", "/api/v2.1/copy-move-task/?task_id=in-progress", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)

	// Processing task should have done=false
	if resp["done"] != false {
		t.Errorf("done = %v, want false (task still processing)", resp["done"])
	}
}

func TestBatchRequest_JSONBinding(t *testing.T) {
	tests := []struct {
		name       string
		body       map[string]interface{}
		wantStatus int
	}{
		{
			name: "valid request",
			body: map[string]interface{}{
				"src_repo_id":    "repo-1",
				"dst_repo_id":    "repo-2",
				"src_parent_dir": "/",
				"dst_parent_dir": "/target",
				"src_dirents":    []string{"file.txt", "photo.jpg"},
			},
			wantStatus: http.StatusOK,
		},
		{
			name:       "empty body",
			body:       map[string]interface{}{},
			wantStatus: http.StatusOK, // JSON binding succeeds with zero values
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := gin.New()
			r.POST("/test", func(c *gin.Context) {
				var req BatchRequest
				if err := c.ShouldBindJSON(&req); err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
					return
				}
				c.JSON(http.StatusOK, req)
			})

			jsonBody, _ := json.Marshal(tt.body)
			req, _ := http.NewRequest("POST", "/test", bytes.NewBuffer(jsonBody))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d, body = %s", w.Code, tt.wantStatus, w.Body.String())
			}
		})
	}
}

func TestRegisterBatchOperationRoutes(t *testing.T) {
	r := gin.New()
	rg := r.Group("/api/v2.1")
	RegisterBatchOperationRoutes(rg, nil, nil)

	routes := []struct {
		method string
		path   string
	}{
		{"POST", "/api/v2.1/repos/sync-batch-move-item/"},
		{"POST", "/api/v2.1/repos/sync-batch-copy-item/"},
		{"POST", "/api/v2.1/repos/async-batch-move-item/"},
		{"POST", "/api/v2.1/repos/async-batch-copy-item/"},
		{"GET", "/api/v2.1/copy-move-task/"},
	}

	for _, rt := range routes {
		req, _ := http.NewRequest(rt.method, rt.path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code == http.StatusNotFound {
			t.Errorf("route %s %s not registered", rt.method, rt.path)
		}
	}
}

func TestTaskStore_ConcurrentAccess(t *testing.T) {
	store := &TaskStore{tasks: make(map[string]*AsyncTask)}

	// Write
	store.mu.Lock()
	store.tasks["task-1"] = &AsyncTask{ID: "task-1", Status: "processing"}
	store.mu.Unlock()

	// Read
	store.mu.RLock()
	task, exists := store.tasks["task-1"]
	store.mu.RUnlock()

	if !exists {
		t.Fatal("task not found")
	}
	if task.ID != "task-1" {
		t.Errorf("task ID = %s, want task-1", task.ID)
	}
}

func TestAsyncTask_JSONFormat(t *testing.T) {
	task := AsyncTask{
		ID:           "task-abc",
		Type:         "copy",
		Status:       "done",
		Total:        10,
		Done:         8,
		Failed:       2,
		FailedReason: "permission denied",
	}

	data, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("failed to marshal AsyncTask: %v", err)
	}

	var decoded map[string]interface{}
	json.Unmarshal(data, &decoded)

	if decoded["task_id"] != "task-abc" {
		t.Errorf("task_id = %v, want task-abc", decoded["task_id"])
	}
	if decoded["type"] != "copy" {
		t.Errorf("type = %v, want copy", decoded["type"])
	}
	if decoded["status"] != "done" {
		t.Errorf("status = %v, want done", decoded["status"])
	}
	if decoded["failed_reason"] != "permission denied" {
		t.Errorf("failed_reason = %v, want 'permission denied'", decoded["failed_reason"])
	}
}

func TestConflictPolicy_Deserialization(t *testing.T) {
	tests := []struct {
		name           string
		conflictPolicy string
	}{
		{name: "replace", conflictPolicy: "replace"},
		{name: "autorename", conflictPolicy: "autorename"},
		{name: "skip", conflictPolicy: "skip"},
		{name: "empty string", conflictPolicy: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := BatchRequest{
				SrcRepoID:      "src-repo",
				DstRepoID:      "dst-repo",
				SrcParentDir:   "/",
				DstParentDir:   "/",
				SrcDirents:     []string{"file.txt"},
				ConflictPolicy: tt.conflictPolicy,
			}

			data, err := json.Marshal(req)
			if err != nil {
				t.Fatalf("failed to marshal BatchRequest: %v", err)
			}

			var decoded BatchRequest
			err = json.Unmarshal(data, &decoded)
			if err != nil {
				t.Fatalf("failed to unmarshal BatchRequest: %v", err)
			}

			if decoded.ConflictPolicy != tt.conflictPolicy {
				t.Errorf("conflict_policy = %q, want %q", decoded.ConflictPolicy, tt.conflictPolicy)
			}
		})
	}
}

func TestConflictError_Formatting(t *testing.T) {
	err := &ConflictError{ItemName: "test.txt"}
	want := "item with name 'test.txt' already exists in destination"
	if err.Error() != want {
		t.Errorf("ConflictError.Error() = %q, want %q", err.Error(), want)
	}
}

func TestSyncBatchCopy_MissingSrcRepoID(t *testing.T) {
	r, h := setupBatchRouter()
	r.POST("/api/v2.1/repos/sync-batch-copy-item/", h.SyncBatchCopy)

	body := BatchRequest{
		SrcRepoID:    "",
		DstRepoID:    "dst-repo",
		SrcParentDir: "/",
		DstParentDir: "/",
		SrcDirents:   []string{"file.txt"},
	}
	jsonBody, _ := json.Marshal(body)

	req, _ := http.NewRequest("POST", "/api/v2.1/repos/sync-batch-copy-item/", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != "src_repo_id is required" {
		t.Errorf("error = %v, want 'src_repo_id is required'", resp["error"])
	}
}

func TestSyncBatchCopy_EmptyDirents(t *testing.T) {
	r, h := setupBatchRouter()
	r.POST("/api/v2.1/repos/sync-batch-copy-item/", h.SyncBatchCopy)

	body := BatchRequest{
		SrcRepoID:    "src-repo",
		DstRepoID:    "dst-repo",
		SrcParentDir: "/",
		DstParentDir: "/",
		SrcDirents:   []string{},
	}
	jsonBody, _ := json.Marshal(body)

	req, _ := http.NewRequest("POST", "/api/v2.1/repos/sync-batch-copy-item/", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != "src_dirents is required" {
		t.Errorf("error = %v, want 'src_dirents is required'", resp["error"])
	}
}

func TestAsyncBatchMove_InvalidJSON(t *testing.T) {
	r, h := setupBatchRouter()
	r.POST("/api/v2.1/repos/async-batch-move-item/", h.AsyncBatchMove)

	req, _ := http.NewRequest("POST", "/api/v2.1/repos/async-batch-move-item/", bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestAsyncBatchCopy_InvalidJSON(t *testing.T) {
	r, h := setupBatchRouter()
	r.POST("/api/v2.1/repos/async-batch-copy-item/", h.AsyncBatchCopy)

	req, _ := http.NewRequest("POST", "/api/v2.1/repos/async-batch-copy-item/", bytes.NewBufferString("{invalid"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestGetTaskProgress_ReturnsCorrectFields(t *testing.T) {
	r, h := setupBatchRouter()
	r.GET("/api/v2.1/copy-move-task/", h.GetTaskProgress)

	h.tasks.mu.Lock()
	h.tasks.tasks["fields-check"] = &AsyncTask{
		ID:           "fields-check",
		Type:         "move",
		Status:       "done",
		Total:        7,
		Done:         5,
		Failed:       2,
		FailedReason: "no such file",
	}
	h.tasks.mu.Unlock()

	req, _ := http.NewRequest("GET", "/api/v2.1/copy-move-task/?task_id=fields-check", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp["task_id"] != "fields-check" {
		t.Errorf("task_id = %v, want fields-check", resp["task_id"])
	}
	if resp["done"] != true {
		t.Errorf("done = %v, want true", resp["done"])
	}
	if resp["successful"] != float64(5) {
		t.Errorf("successful = %v, want 5", resp["successful"])
	}
	if resp["failed"] != float64(2) {
		t.Errorf("failed = %v, want 2", resp["failed"])
	}
	if resp["total"] != float64(7) {
		t.Errorf("total = %v, want 7", resp["total"])
	}
	if resp["failed_reason"] != "no such file" {
		t.Errorf("failed_reason = %v, want 'no such file'", resp["failed_reason"])
	}
}

func TestTaskStore_ConcurrentSafety(t *testing.T) {
	store := &TaskStore{tasks: make(map[string]*AsyncTask)}

	done := make(chan struct{})

	// Spawn writers
	for i := 0; i < 10; i++ {
		go func(id int) {
			defer func() { done <- struct{}{} }()
			taskID := fmt.Sprintf("task-%d", id)
			store.mu.Lock()
			store.tasks[taskID] = &AsyncTask{
				ID:     taskID,
				Status: "processing",
				Total:  id,
			}
			store.mu.Unlock()
		}(i)
	}

	// Spawn readers
	for i := 0; i < 10; i++ {
		go func(id int) {
			defer func() { done <- struct{}{} }()
			taskID := fmt.Sprintf("task-%d", id)
			store.mu.RLock()
			_ = store.tasks[taskID]
			store.mu.RUnlock()
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 20; i++ {
		<-done
	}

	// Verify all writes succeeded
	store.mu.RLock()
	count := len(store.tasks)
	store.mu.RUnlock()

	if count != 10 {
		t.Errorf("task count = %d, want 10", count)
	}
}

func TestCheckWritePermission_NilPermMiddleware(t *testing.T) {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("org_id", "00000000-0000-0000-0000-000000000001")
		c.Set("user_id", "00000000-0000-0000-0000-000000000001")
		c.Next()
	})

	h := &BatchOperationHandler{
		db:             nil,
		permMiddleware: nil,
		tasks:          &TaskStore{tasks: make(map[string]*AsyncTask)},
	}

	var result bool
	r.GET("/test-perm", func(c *gin.Context) {
		result = h.checkWritePermission(c, "org-1", "user-1")
		c.JSON(http.StatusOK, gin.H{"allowed": result})
	})

	req, _ := http.NewRequest("GET", "/test-perm", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if !result {
		t.Errorf("checkWritePermission with nil permMiddleware = false, want true")
	}
}

func TestSyncBatchMove_ValidRequestNilDB(t *testing.T) {
	r, h := setupBatchRouter()
	r.POST("/api/v2.1/repos/sync-batch-move-item/", h.SyncBatchMove)

	body := BatchRequest{
		SrcRepoID:    "src-repo",
		DstRepoID:    "dst-repo",
		SrcParentDir: "/",
		DstParentDir: "/",
		SrcDirents:   []string{"file.txt"},
	}
	jsonBody, _ := json.Marshal(body)

	req, _ := http.NewRequest("POST", "/api/v2.1/repos/sync-batch-move-item/", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// With nil DB and nil permMiddleware, the handler passes validation and returns
	// 500 via the explicit nil DB guard.
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d (nil DB should return explicit 500)", w.Code, http.StatusInternalServerError)
	}
}

func TestBatchRequest_AllConflictPolicyValues(t *testing.T) {
	tests := []struct {
		name   string
		policy string
	}{
		{name: "replace policy", policy: "replace"},
		{name: "autorename policy", policy: "autorename"},
		{name: "skip policy", policy: "skip"},
		{name: "empty policy", policy: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, h := setupBatchRouter()
			r.POST("/api/v2.1/repos/sync-batch-copy-item/", h.SyncBatchCopy)

			bodyMap := map[string]interface{}{
				"src_repo_id":     "src-repo",
				"dst_repo_id":     "dst-repo",
				"src_parent_dir":  "/",
				"dst_parent_dir":  "/",
				"src_dirents":     []string{"file.txt"},
				"conflict_policy": tt.policy,
			}
			jsonBody, _ := json.Marshal(bodyMap)

			req, _ := http.NewRequest("POST", "/api/v2.1/repos/sync-batch-copy-item/", bytes.NewBuffer(jsonBody))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			// The request should pass JSON binding and validation (not 400).
			// With nil DB it should return 500 via the explicit nil DB guard.
			// The key assertion is that it does NOT fail on conflict_policy parsing.
			if w.Code == http.StatusBadRequest {
				t.Errorf("status = %d, conflict_policy %q should not cause a bad request", w.Code, tt.policy)
			}
		})
	}
}
