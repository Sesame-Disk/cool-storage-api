package v2

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestGetRestoreStatus_MissingPath(t *testing.T) {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		c.Set("org_id", "00000000-0000-0000-0000-000000000001")
		c.Set("user_id", "00000000-0000-0000-0000-000000000001")
		c.Next()
	})

	h := &RestoreHandler{db: nil, config: nil}
	r.GET("/api/v2.1/repos/:repo_id/file/restore-status", h.GetRestoreStatus)

	// No ?p= parameter
	req, _ := http.NewRequest("GET", "/api/v2.1/repos/00000000-0000-0000-0000-000000000001/file/restore-status", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != "path is required" {
		t.Errorf("error = %v, want 'path is required'", resp["error"])
	}
}

func TestGetRestoreJob_InvalidJobID(t *testing.T) {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		c.Set("org_id", "00000000-0000-0000-0000-000000000001")
		c.Set("user_id", "00000000-0000-0000-0000-000000000001")
		c.Next()
	})

	h := &RestoreHandler{db: nil, config: nil}
	r.GET("/api/v2.1/repos/:repo_id/restore-jobs/:job_id", h.GetRestoreJob)

	req, _ := http.NewRequest("GET", "/api/v2.1/repos/00000000-0000-0000-0000-000000000001/restore-jobs/not-a-uuid", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != "invalid job_id" {
		t.Errorf("error = %v, want 'invalid job_id'", resp["error"])
	}
}

func TestGetRestoreJob_InvalidRepoID(t *testing.T) {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		c.Set("org_id", "00000000-0000-0000-0000-000000000001")
		c.Set("user_id", "00000000-0000-0000-0000-000000000001")
		c.Next()
	})

	h := &RestoreHandler{db: nil, config: nil}
	r.GET("/api/v2.1/repos/:repo_id/restore-jobs/:job_id", h.GetRestoreJob)

	req, _ := http.NewRequest("GET", "/api/v2.1/repos/not-a-uuid/restore-jobs/00000000-0000-0000-0000-000000000001", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != "invalid repo_id" {
		t.Errorf("error = %v, want 'invalid repo_id'", resp["error"])
	}
}

func TestInitiateRestore_MissingPath(t *testing.T) {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		c.Set("org_id", "00000000-0000-0000-0000-000000000001")
		c.Set("user_id", "00000000-0000-0000-0000-000000000001")
		c.Next()
	})

	h := &RestoreHandler{db: nil, config: nil}
	r.POST("/api/v2.1/repos/:repo_id/file/restore", h.InitiateRestore)

	req, _ := http.NewRequest("POST", "/api/v2.1/repos/00000000-0000-0000-0000-000000000001/file/restore", nil)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestRegisterRestoreRoutes(t *testing.T) {
	r := gin.New()
	r.Use(gin.Recovery()) // Prevent nil-db panics from crashing test
	rg := r.Group("/api/v2.1")
	RegisterRestoreRoutes(rg, nil, nil)

	routes := []struct {
		method string
		path   string
	}{
		{"POST", "/api/v2.1/repos/test-repo/file/restore"},
		{"GET", "/api/v2.1/repos/test-repo/file/restore-status"},
		{"GET", "/api/v2.1/repos/test-repo/restore-jobs/00000000-0000-0000-0000-000000000001"},
		// ListRestoreJobs and GetRestoreJob access db directly, so skip those
		// in route registration test (they panic with nil db even with Recovery)
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

func TestInitiateRestoreRequest_JSONBinding(t *testing.T) {
	r := gin.New()
	r.POST("/test", func(c *gin.Context) {
		var req InitiateRestoreRequest
		if err := c.ShouldBind(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, req)
	})

	tests := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{
			name:       "valid request",
			body:       `{"path": "/archived/file.txt"}`,
			wantStatus: http.StatusOK,
		},
		{
			name:       "missing path",
			body:       `{}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid json",
			body:       `{bad`,
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("POST", "/test", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}
