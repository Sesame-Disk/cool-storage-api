package v2

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// TestRepoTag_Struct tests RepoTag struct fields
func TestRepoTag_Struct(t *testing.T) {
	tag := RepoTag{
		ID:        1,
		RepoID:    "repo-123",
		Name:      "Important",
		Color:     "#FF0000",
		FileCount: 5,
	}

	if tag.ID != 1 {
		t.Errorf("ID = %d, want 1", tag.ID)
	}
	if tag.Name != "Important" {
		t.Errorf("Name = %q, want %q", tag.Name, "Important")
	}
	if tag.Color != "#FF0000" {
		t.Errorf("Color = %q, want %q", tag.Color, "#FF0000")
	}
}

// TestFileTagResponse_Struct tests FileTagResponse struct fields
func TestFileTagResponse_Struct(t *testing.T) {
	fileTag := FileTagResponse{
		ID:        1,
		RepoTagID: 10,
		Name:      "Review",
		Color:     "#00FF00",
	}

	if fileTag.ID != 1 {
		t.Errorf("ID = %d, want 1", fileTag.ID)
	}
	if fileTag.RepoTagID != 10 {
		t.Errorf("RepoTagID = %d, want 10", fileTag.RepoTagID)
	}
	if fileTag.Name != "Review" {
		t.Errorf("Name = %q, want %q", fileTag.Name, "Review")
	}
}

// TestNewTagHandler tests handler creation
func TestNewTagHandler(t *testing.T) {
	handler := NewTagHandler(nil)
	if handler == nil {
		t.Error("NewTagHandler returned nil")
	}
}

// TestListRepoTags_InvalidRepoID tests invalid repo_id handling
func TestListRepoTags_InvalidRepoID(t *testing.T) {
	r := gin.New()
	handler := NewTagHandler(nil)

	r.GET("/repos/:repo_id/repo-tags", handler.ListRepoTags)

	// Invalid UUID format
	req := httptest.NewRequest("GET", "/repos/invalid-uuid/repo-tags", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	// With nil db, should return empty array (no error for backwards compat)
	if w.Code == http.StatusNotFound {
		t.Error("Route should be found")
	}
}

// TestListRepoTags_EmptyResult tests empty tag list
func TestListRepoTags_EmptyResult(t *testing.T) {
	r := gin.New()
	handler := NewTagHandler(nil) // nil db returns empty list

	r.GET("/repos/:repo_id/repo-tags", handler.ListRepoTags)

	req := httptest.NewRequest("GET", "/repos/00000000-0000-0000-0000-000000000001/repo-tags", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	// Should return empty array
	body := w.Body.String()
	if !strings.Contains(body, "repo_tags") {
		t.Errorf("Response should contain repo_tags field, got: %s", body)
	}
}

// TestCreateRepoTag_Validation tests validation of create request
func TestCreateRepoTag_Validation(t *testing.T) {
	r := gin.New()
	handler := NewTagHandler(nil)

	r.POST("/repos/:repo_id/repo-tags", handler.CreateRepoTag)

	tests := []struct {
		name       string
		formData   url.Values
		wantStatus int
	}{
		{
			name:       "missing all fields - validation fails",
			formData:   url.Values{},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "with both fields - success (nil db)",
			formData: url.Values{
				"name":  {"Test Tag"},
				"color": {"#FF0000"},
			},
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := strings.NewReader(tt.formData.Encode())
			req := httptest.NewRequest("POST", "/repos/00000000-0000-0000-0000-000000000001/repo-tags", body)
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

// TestUpdateRepoTag_InvalidTagID tests invalid tag_id handling
func TestUpdateRepoTag_InvalidTagID(t *testing.T) {
	r := gin.New()
	handler := NewTagHandler(nil)

	r.PUT("/repos/:repo_id/repo-tags/:tag_id", handler.UpdateRepoTag)

	tests := []struct {
		name       string
		tagID      string
		wantStatus int
	}{
		{
			name:       "non-numeric tag_id",
			tagID:      "abc",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "negative tag_id - accepted as number",
			tagID:      "-1",
			wantStatus: http.StatusOK, // Negative numbers are valid integers
		},
		{
			name:       "valid tag_id",
			tagID:      "123",
			wantStatus: http.StatusOK, // Nil db returns success
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			formData := url.Values{
				"name":  {"Updated Name"},
				"color": {"#00FF00"},
			}
			body := strings.NewReader(formData.Encode())
			req := httptest.NewRequest("PUT", "/repos/00000000-0000-0000-0000-000000000001/repo-tags/"+tt.tagID, body)
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

// TestDeleteRepoTag_InvalidTagID tests invalid tag_id handling
func TestDeleteRepoTag_InvalidTagID(t *testing.T) {
	r := gin.New()
	handler := NewTagHandler(nil)

	r.DELETE("/repos/:repo_id/repo-tags/:tag_id", handler.DeleteRepoTag)

	req := httptest.NewRequest("DELETE", "/repos/00000000-0000-0000-0000-000000000001/repo-tags/invalid", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestGetFileTags_MissingPath tests missing path parameter
// Note: Current implementation returns empty tags even without path (Seafile compat)
func TestGetFileTags_MissingPath(t *testing.T) {
	r := gin.New()
	handler := NewTagHandler(nil)

	r.GET("/repos/:repo_id/file-tags", handler.GetFileTags)

	req := httptest.NewRequest("GET", "/repos/00000000-0000-0000-0000-000000000001/file-tags", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	// Current implementation returns 200 with empty tags
	// TODO: Consider adding path validation if needed
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

// TestGetFileTags_WithPath tests file tags with valid path
func TestGetFileTags_WithPath(t *testing.T) {
	r := gin.New()
	handler := NewTagHandler(nil)

	r.GET("/repos/:repo_id/file-tags", handler.GetFileTags)

	req := httptest.NewRequest("GET", "/repos/00000000-0000-0000-0000-000000000001/file-tags?path=/test/file.txt", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	// Should return OK with empty tags (nil db returns empty)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

// TestAddFileTag_MissingFields tests validation of add file tag request
func TestAddFileTag_MissingFields(t *testing.T) {
	r := gin.New()
	handler := NewTagHandler(nil)

	r.POST("/repos/:repo_id/file-tags", handler.AddFileTag)

	tests := []struct {
		name       string
		formData   url.Values
		wantStatus int
	}{
		{
			name:       "missing all fields",
			formData:   url.Values{},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "missing repo_tag_id",
			formData: url.Values{
				"file_path": {"/test/file.txt"},
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "missing file_path",
			formData: url.Values{
				"repo_tag_id": {"1"},
			},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := strings.NewReader(tt.formData.Encode())
			req := httptest.NewRequest("POST", "/repos/00000000-0000-0000-0000-000000000001/file-tags", body)
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

// TestRemoveFileTag_InvalidFileTagID tests invalid file_tag_id handling
func TestRemoveFileTag_InvalidFileTagID(t *testing.T) {
	r := gin.New()
	handler := NewTagHandler(nil)

	r.DELETE("/repos/:repo_id/file-tags/:file_tag_id", handler.RemoveFileTag)

	req := httptest.NewRequest("DELETE", "/repos/00000000-0000-0000-0000-000000000001/file-tags/invalid", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestTagColors_ValidFormats tests common tag color formats
func TestTagColors_ValidFormats(t *testing.T) {
	colors := []string{
		"#FF0000",  // Red
		"#00FF00",  // Green
		"#0000FF",  // Blue
		"#FFFFFF",  // White
		"#000000",  // Black
		"#FFA500",  // Orange
		"#800080",  // Purple
	}

	for _, color := range colors {
		tag := RepoTag{
			ID:    1,
			Name:  "Test",
			Color: color,
		}

		if tag.Color != color {
			t.Errorf("Color = %q, want %q", tag.Color, color)
		}
	}
}
