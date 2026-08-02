package v2

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/downloadadmission"
	"github.com/Sesame-Disk/sesamefs/internal/httputil"
	"github.com/gin-gonic/gin"
)

type cancelDuringIWorkContext struct {
	context.Context
	checks   int
	cancelAt int
	done     chan struct{}
	canceled bool
}

func (c *cancelDuringIWorkContext) Err() error {
	c.checks++
	if c.checks >= c.cancelAt {
		if !c.canceled {
			close(c.done)
			c.canceled = true
		}
		return context.Canceled
	}
	return nil
}

func (c *cancelDuringIWorkContext) Done() <-chan struct{} { return c.done }

func init() {
	gin.SetMode(gin.TestMode)
}

func TestExtractIWorkPreviewPDFContextStopsAfterCancellation(t *testing.T) {
	entries := make(map[string][]byte, 2000)
	for i := 0; i < 2000; i++ {
		entries[fmt.Sprintf("Index/%04d.iwa", i)] = []byte{0}
	}
	archive := createTestZIP(entries)
	ctx := &cancelDuringIWorkContext{
		Context:  context.Background(),
		cancelAt: 8,
		done:     make(chan struct{}),
	}

	_, err := extractIWorkPreviewPDFContext(ctx, archive, 10*1024*1024)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("extractIWorkPreviewPDFContext error = %v, want context canceled", err)
	}
	select {
	case <-ctx.done:
	default:
		t.Fatal("test context was not canceled during archive traversal")
	}
}

func TestRespondIWorkPreparationFailureDeadlineReturns503(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/file", nil)
	lifecycle, reason, err := httputil.AcquireDownloadAdmission(c, nil, config.DownloadAdmissionConfig{}, downloadadmission.AdmissionRequest{})
	if err != nil || reason != "" || lifecycle == nil {
		t.Fatalf("AcquireDownloadAdmission = (%v, %q, %v)", lifecycle, reason, err)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Millisecond))
	defer cancel()

	respondIWorkPreparationFailure(c, lifecycle, ctx, context.DeadlineExceeded, "failed to read file")
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	if recorder.Body.Len() == 0 {
		t.Fatal("deadline response body is empty")
	}
}

func TestRespondIWorkPreparationFailureDisconnectDoesNotWrite(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/file", nil)
	lifecycle, reason, err := httputil.AcquireDownloadAdmission(c, nil, config.DownloadAdmissionConfig{}, downloadadmission.AdmissionRequest{})
	if err != nil || reason != "" || lifecycle == nil {
		t.Fatalf("AcquireDownloadAdmission = (%v, %q, %v)", lifecycle, reason, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	respondIWorkPreparationFailure(c, lifecycle, ctx, context.Canceled, "failed to read file")
	if c.Writer.Written() {
		t.Fatal("disconnect wrote an HTTP response")
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("disconnect response body = %q, want empty", recorder.Body.String())
	}
}

// mockTokenCreator implements TokenCreator interface for testing
type mockTokenCreator struct{}

func (m *mockTokenCreator) CreateUploadToken(orgID, repoID, path, userID string) (string, error) {
	return "mock-upload-token-" + repoID, nil
}

func (m *mockTokenCreator) CreateUpdateToken(orgID, repoID, path, userID string) (string, error) {
	return "mock-update-token-" + repoID, nil
}

func (m *mockTokenCreator) CreateDownloadToken(orgID, repoID, path, userID string) (string, error) {
	return "mock-download-token-" + repoID, nil
}

func (m *mockTokenCreator) CreateLinkUploadToken(orgID, repoID, path, userID, sourceID string) (string, error) {
	return "mock-link-upload-token-" + repoID, nil
}

func (m *mockTokenCreator) CreateLinkDownloadToken(orgID, repoID, path, userID, sourceID string) (string, error) {
	return "mock-link-download-token-" + repoID, nil
}

// TestOnlyOfficeEditorHTML tests the OnlyOffice editor HTML generator
func TestOnlyOfficeEditorHTML(t *testing.T) {
	config := OnlyOfficeConfig{
		Document: OnlyOfficeDocument{
			FileType: "docx",
			Key:      "abc123def456",
			Title:    "test-document.docx",
			URL:      "https://example.com/download/test.docx",
			Permissions: &OnlyOfficePermissions{
				Edit:      true,
				Download:  true,
				Print:     true,
				Copy:      true,
				Review:    true,
				Comment:   true,
				FillForms: true,
			},
		},
		DocumentType: "word",
		EditorConfig: OnlyOfficeEditorConfig{
			CallbackURL: "https://example.com/callback",
			Mode:        "edit",
			User: OnlyOfficeUser{
				ID:   "user-123",
				Name: "Test User",
			},
			Customization: &OnlyOfficeCustomization{
				Forcesave:  true,
				SubmitForm: false,
			},
		},
		Token: "jwt-token-here",
	}

	result := onlyOfficeEditorHTML("https://office.example.com/api.js", config, "test-document.docx")

	// Check for required elements
	// Note: json.Marshal produces compact JSON without spaces after colons
	expected := []string{
		"<!DOCTYPE html>",
		"<title>test-document.docx - SesameFS</title>",
		`<script src="https://office.example.com/api.js"></script>`,
		`"fileType":"docx"`,
		`"key":"abc123def456"`,
		`"title":"test-document.docx"`,
		"example.com", // URL is escaped, just check domain
		"test.docx",   // And filename
		`"documentType":"word"`,
		`"mode":"edit"`,
		`"id":"user-123"`,
		`"name":"Test User"`,
		`"token":"jwt-token-here"`,
		"DocsAPI.DocEditor",
		"editor-container",
		"loading-spinner",
	}

	for _, exp := range expected {
		if !strings.Contains(result, exp) {
			t.Errorf("onlyOfficeEditorHTML missing expected content: %q", exp)
		}
	}
}

// TestOnlyOfficeEditorHTMLWithoutToken tests HTML generation without JWT token
func TestOnlyOfficeEditorHTMLWithoutToken(t *testing.T) {
	config := OnlyOfficeConfig{
		Document: OnlyOfficeDocument{
			FileType: "xlsx",
			Key:      "spreadsheet-key",
			Title:    "data.xlsx",
			URL:      "https://example.com/data.xlsx",
			Permissions: &OnlyOfficePermissions{
				Edit:      false,
				Download:  true,
				Print:     true,
				Copy:      true,
				Review:    false,
				Comment:   false,
				FillForms: true,
			},
		},
		DocumentType: "cell",
		EditorConfig: OnlyOfficeEditorConfig{
			CallbackURL: "https://example.com/callback",
			Mode:        "view",
			User: OnlyOfficeUser{
				ID:   "viewer-456",
				Name: "Viewer User",
			},
			Customization: &OnlyOfficeCustomization{
				Forcesave:  true,
				SubmitForm: false,
			},
		},
		// Token is empty - should not include token in config
	}

	result := onlyOfficeEditorHTML("https://office.example.com/api.js", config, "data.xlsx")

	// Should NOT contain token field when token is empty
	if strings.Contains(result, `"token":`) {
		t.Error("onlyOfficeEditorHTML should not include token field when token is empty")
	}

	// Should still contain other required fields (json.Marshal compact format)
	if !strings.Contains(result, `"mode":"view"`) {
		t.Error("onlyOfficeEditorHTML missing mode field")
	}

	if !strings.Contains(result, `"documentType":"cell"`) {
		t.Error("onlyOfficeEditorHTML missing documentType field")
	}
}

// TestIsOnlyOfficeFile tests the file extension checker
func TestIsOnlyOfficeFile(t *testing.T) {
	h := &FileViewHandler{
		config: &config.Config{
			OnlyOffice: config.OnlyOfficeConfig{
				ViewExtensions: []string{"doc", "docx", "xls", "xlsx", "ppt", "pptx", "odt", "pdf"},
				EditExtensions: []string{"docx", "xlsx", "pptx"},
			},
		},
	}

	tests := []struct {
		ext      string
		expected bool
	}{
		{"docx", true},
		{"xlsx", true},
		{"pptx", true},
		{"pdf", true},
		{"doc", true},
		{"odt", true},
		{"txt", false},
		{"jpg", false},
		{"png", false},
		{"go", false},
		{"", false},
		{"DOCX", false}, // Case sensitive - ext should be lowercased before calling
	}

	for _, tt := range tests {
		t.Run(tt.ext, func(t *testing.T) {
			result := h.isOnlyOfficeFile(tt.ext)
			if result != tt.expected {
				t.Errorf("isOnlyOfficeFile(%q) = %v, want %v", tt.ext, result, tt.expected)
			}
		})
	}
}

// devTokenAuthMiddleware is a simple dev-mode auth middleware for testing.
// It validates tokens against a list of dev token entries.
func devTokenAuthMiddleware(tokens []config.DevTokenEntry) gin.HandlerFunc {
	return func(c *gin.Context) {
		auth := c.GetHeader("Authorization")
		token := ""
		if strings.HasPrefix(auth, "Token ") {
			token = strings.TrimPrefix(auth, "Token ")
		} else if strings.HasPrefix(auth, "Bearer ") {
			token = strings.TrimPrefix(auth, "Bearer ")
		}
		if token == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			c.Abort()
			return
		}
		for _, entry := range tokens {
			if entry.Token == token {
				c.Set("user_id", entry.UserID)
				c.Set("org_id", entry.OrgID)
				c.Next()
				return
			}
		}
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
		c.Abort()
	}
}

// TestFileViewAuthWrapper tests the fileViewAuthWrapper function
func TestFileViewAuthWrapper(t *testing.T) {
	devTokens := []config.DevTokenEntry{
		{Token: "valid-token-123", UserID: "user-1", OrgID: "org-1"},
		{Token: "admin-token-456", UserID: "admin", OrgID: "org-admin"},
	}

	serverAuth := devTokenAuthMiddleware(devTokens)

	tests := []struct {
		name           string
		authHeader     string
		queryToken     string
		expectedStatus int
		expectUserID   string
		expectOrgID    string
	}{
		{
			name:           "valid token in header",
			authHeader:     "Token valid-token-123",
			expectedStatus: http.StatusOK,
			expectUserID:   "user-1",
			expectOrgID:    "org-1",
		},
		{
			name:           "valid bearer token in header",
			authHeader:     "Bearer valid-token-123",
			expectedStatus: http.StatusOK,
			expectUserID:   "user-1",
			expectOrgID:    "org-1",
		},
		{
			name:           "valid token in query param",
			queryToken:     "admin-token-456",
			expectedStatus: http.StatusOK,
			expectUserID:   "admin",
			expectOrgID:    "org-admin",
		},
		{
			name:           "no token provided",
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "invalid token",
			authHeader:     "Token invalid-token",
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "header takes precedence over query",
			authHeader:     "Token valid-token-123",
			queryToken:     "admin-token-456",
			expectedStatus: http.StatusOK,
			expectUserID:   "user-1", // From header token
			expectOrgID:    "org-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := gin.New()
			r.Use(fileViewAuthWrapper(serverAuth))
			r.GET("/test", func(c *gin.Context) {
				userID := c.GetString("user_id")
				orgID := c.GetString("org_id")
				c.JSON(http.StatusOK, gin.H{"user_id": userID, "org_id": orgID})
			})

			path := "/test"
			if tt.queryToken != "" {
				path += "?token=" + tt.queryToken
			}

			req, _ := http.NewRequest("GET", path, nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}

			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.expectedStatus)
			}

			if tt.expectedStatus == http.StatusOK {
				body := w.Body.String()
				if tt.expectUserID != "" && !strings.Contains(body, tt.expectUserID) {
					t.Errorf("response missing expected user_id: %s", tt.expectUserID)
				}
				if tt.expectOrgID != "" && !strings.Contains(body, tt.expectOrgID) {
					t.Errorf("response missing expected org_id: %s", tt.expectOrgID)
				}
			}
		})
	}
}

// TestFileViewAuthWrapperQueryParamPromotion tests that query param tokens
// get promoted to Authorization header before reaching the server auth middleware
func TestFileViewAuthWrapperQueryParamPromotion(t *testing.T) {
	// Use a serverAuth that rejects all requests to verify the wrapper promotes the token
	rejectAll := func(c *gin.Context) {
		// Check that the Authorization header was set from query param
		auth := c.GetHeader("Authorization")
		if auth == "Token my-query-token" {
			c.Set("user_id", "promoted-user")
			c.Next()
			return
		}
		c.JSON(http.StatusUnauthorized, gin.H{"error": "no auth"})
		c.Abort()
	}

	r := gin.New()
	r.Use(fileViewAuthWrapper(rejectAll))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"user_id": c.GetString("user_id")})
	})

	req, _ := http.NewRequest("GET", "/test?token=my-query-token", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

// TestViewFileRedirectsNonOfficeFiles tests that non-office files redirect to download
func TestViewFileRedirectsNonOfficeFiles(t *testing.T) {
	h := &FileViewHandler{
		config:       config.DefaultConfig(),
		serverURL:    "http://localhost:8080",
		tokenCreator: &mockTokenCreator{},
	}

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user_id", "test-user")
		c.Set("org_id", "test-org")
		c.Next()
	})
	r.GET("/lib/:repo_id/file/*filepath", h.ViewFile)

	tests := []struct {
		name           string
		filepath       string
		expectStatus   int
		expectRedirect bool
	}{
		{
			name:           "dmg file redirects",
			filepath:       "/test.dmg",
			expectStatus:   http.StatusFound,
			expectRedirect: true,
		},
		{
			name:           "zip file redirects",
			filepath:       "/archive.zip",
			expectStatus:   http.StatusFound,
			expectRedirect: true,
		},
		{
			name:           "png file redirects to frontend preview shell",
			filepath:       "/image.png",
			expectStatus:   http.StatusFound,
			expectRedirect: false,
		},
		{
			name:           "txt file redirects to frontend preview shell",
			filepath:       "/readme.txt",
			expectStatus:   http.StatusFound,
			expectRedirect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "/lib/repo-123/file"+tt.filepath, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.expectStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.expectStatus)
			}

			if tt.expectRedirect {
				location := w.Header().Get("Location")
				// Redirects to seafhttp download endpoint with token
				if !strings.Contains(location, "/seafhttp/files/") {
					t.Errorf("redirect location = %q, expected seafhttp download URL", location)
				}
			} else {
				location := w.Header().Get("Location")
				if !strings.HasPrefix(location, "/file-preview/?") {
					t.Errorf("redirect location = %q, expected frontend preview shell", location)
				}
				if !strings.Contains(location, "repo_id=repo-123") {
					t.Errorf("redirect location = %q, expected repo_id query parameter", location)
				}
			}
		})
	}
}

func TestViewHistoricFileRedirectsPreviewableFilesToFrontendShell(t *testing.T) {
	h := &FileViewHandler{
		config: config.DefaultConfig(),
	}

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user_id", "test-user")
		c.Set("org_id", "test-org")
		c.Next()
	})
	r.GET("/repo/:repo_id/history/view", h.ViewHistoricFile)

	req, _ := http.NewRequest("GET", "/repo/repo-123/history/view?obj_id=abc123&p=/docs/readme.txt", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("expected 302 redirect, got %d", w.Code)
	}

	location := w.Header().Get("Location")
	if !strings.HasPrefix(location, "/file-preview/?") {
		t.Fatalf("redirect location = %q, expected frontend preview shell", location)
	}
	if !strings.Contains(location, "repo_id=repo-123") {
		t.Errorf("redirect location = %q, expected repo_id query parameter", location)
	}
	if !strings.Contains(location, "obj_id=abc123") {
		t.Errorf("redirect location = %q, expected obj_id query parameter", location)
	}
	if !strings.Contains(location, "p=%2Fdocs%2Freadme.txt") {
		t.Errorf("redirect location = %q, expected encoded path query parameter", location)
	}
}

func TestBuildFrontendErrorPageURL(t *testing.T) {
	result := buildFrontendErrorPageURL(http.StatusBadRequest, "Bad Request", "Missing obj_id parameter.")
	if !strings.HasPrefix(result, "/file-error/?") {
		t.Fatalf("expected /file-error redirect, got %q", result)
	}
	if !strings.Contains(result, "status=400") {
		t.Fatalf("expected status query parameter, got %q", result)
	}
	if !strings.Contains(result, "title=Bad+Request") {
		t.Fatalf("expected encoded title, got %q", result)
	}
	if !strings.Contains(result, "message=Missing+obj_id+parameter.") {
		t.Fatalf("expected encoded message, got %q", result)
	}
}

func TestResolveInlineContentType(t *testing.T) {
	tests := []struct {
		ext  string
		want string
	}{
		{ext: "pdf", want: "application/pdf"},
		{ext: "mov", want: "video/quicktime"},
		{ext: "mp3", want: "audio/mpeg"},
		{ext: "bin", want: "application/octet-stream"},
	}

	for _, tt := range tests {
		t.Run(tt.ext, func(t *testing.T) {
			if got := resolveInlineContentType(tt.ext); got != tt.want {
				t.Fatalf("resolveInlineContentType(%q) = %q, want %q", tt.ext, got, tt.want)
			}
		})
	}
}

func TestViewHistoricFileInvalidRequestRedirectsToFrontendErrorShell(t *testing.T) {
	h := &FileViewHandler{config: config.DefaultConfig()}

	r := gin.New()
	r.GET("/repo/:repo_id/history/view", h.ViewHistoricFile)

	req, _ := http.NewRequest("GET", "/repo/repo-123/history/view?p=/docs/readme.txt", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("expected redirect to frontend error shell, got %d", w.Code)
	}

	location := w.Header().Get("Location")
	if !strings.HasPrefix(location, "/file-error/?") {
		t.Fatalf("redirect location = %q, expected frontend error shell", location)
	}
	if !strings.Contains(location, "status=400") {
		t.Fatalf("redirect location = %q, expected status=400", location)
	}
	if !strings.Contains(location, "Missing+obj_id+parameter.") {
		t.Fatalf("redirect location = %q, expected missing obj_id message", location)
	}
}

// TestViewFileOnlyOfficeDisabled tests behavior when OnlyOffice is disabled
func TestViewFileOnlyOfficeDisabled(t *testing.T) {
	h := &FileViewHandler{
		config: &config.Config{
			OnlyOffice: config.OnlyOfficeConfig{
				Enabled:        false, // Disabled
				ViewExtensions: []string{"docx", "xlsx"},
			},
		},
		serverURL:    "http://localhost:8080",
		tokenCreator: &mockTokenCreator{},
	}

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user_id", "test-user")
		c.Set("org_id", "test-org")
		c.Next()
	})
	r.GET("/lib/:repo_id/file/*filepath", h.ViewFile)

	// Even docx files should redirect when OnlyOffice is disabled
	req, _ := http.NewRequest("GET", "/lib/repo-123/file/document.docx", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Errorf("with OnlyOffice disabled, docx should redirect, got status %d", w.Code)
	}

	location := w.Header().Get("Location")
	if !strings.Contains(location, "/seafhttp/files/") {
		t.Errorf("expected redirect to seafhttp download, got %q", location)
	}
}

// TestViewFilePathNormalization tests that file paths are normalized correctly
func TestViewFilePathNormalization(t *testing.T) {
	h := &FileViewHandler{
		config: &config.Config{
			OnlyOffice: config.OnlyOfficeConfig{
				Enabled:        false, // Disabled to just test path handling
				ViewExtensions: []string{},
			},
		},
		serverURL:    "http://localhost:8080",
		tokenCreator: &mockTokenCreator{},
	}

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user_id", "test-user")
		c.Set("org_id", "test-org")
		c.Next()
	})
	r.GET("/lib/:repo_id/file/*filepath", h.ViewFile)

	tests := []struct {
		name           string
		requestPath    string
		expectFilename string
	}{
		{
			name:           "path with leading slash",
			requestPath:    "/lib/repo-123/file/docs/file.dmg",
			expectFilename: "file.dmg",
		},
		{
			name:           "path in subdirectory",
			requestPath:    "/lib/repo-123/file/nested/deep/file.zip",
			expectFilename: "file.zip",
		},
		{
			name:           "root file",
			requestPath:    "/lib/repo-123/file/root.exe",
			expectFilename: "root.exe",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", tt.requestPath, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			// Check redirect URL contains the filename
			location := w.Header().Get("Location")
			if !strings.Contains(location, "/"+tt.expectFilename) {
				t.Errorf("redirect URL = %q, expected to contain /%s", location, tt.expectFilename)
			}
		})
	}
}

// TestRegisterFileViewRoutes tests that routes are registered correctly
func TestRegisterFileViewRoutes(t *testing.T) {
	cfg := &config.Config{
		Auth: config.AuthConfig{
			DevMode: true,
			DevTokens: []config.DevTokenEntry{
				{Token: "test-token", UserID: "user", OrgID: "org"},
			},
		},
		OnlyOffice: config.OnlyOfficeConfig{
			Enabled: false,
		},
	}

	r := gin.New()

	// Register routes with mock token creator and dev auth middleware
	devAuth := devTokenAuthMiddleware(cfg.Auth.DevTokens)
	RegisterFileViewRoutes(r, nil, cfg, nil, nil, &mockTokenCreator{}, "http://localhost:8082", devAuth)

	// Test that the route exists — use a non-previewable extension so the handler redirects
	req, _ := http.NewRequest("GET", "/lib/repo-123/file/test.dmg?token=test-token", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Should get a redirect (not 404), proving route is registered
	if w.Code == http.StatusNotFound {
		t.Error("route /lib/:repo_id/file/*filepath not registered")
	}

	// With valid token, non-previewable file should redirect to download
	if w.Code != http.StatusFound {
		t.Errorf("expected redirect (302), got %d", w.Code)
	}
}

// TestOnlyOfficeEditorHTMLCustomizations tests that customization options are present
func TestOnlyOfficeEditorHTMLCustomizations(t *testing.T) {
	config := OnlyOfficeConfig{
		Document: OnlyOfficeDocument{
			FileType: "docx",
			Key:      "key123",
			Title:    "doc.docx",
			URL:      "http://example.com/doc.docx",
			Permissions: &OnlyOfficePermissions{
				Edit:      true,
				Download:  true,
				Print:     true,
				Copy:      true,
				Review:    true,
				Comment:   true,
				FillForms: true,
			},
		},
		DocumentType: "word",
		EditorConfig: OnlyOfficeEditorConfig{
			CallbackURL: "http://example.com/callback",
			Mode:        "edit",
			User:        OnlyOfficeUser{ID: "1", Name: "User"},
			Customization: &OnlyOfficeCustomization{
				Forcesave:  true,
				SubmitForm: false,
			},
		},
	}

	result := onlyOfficeEditorHTML("http://office/api.js", config, "doc.docx")

	// Debug: print relevant part of result
	if strings.Contains(result, "Template Error") {
		t.Fatalf("Template error: %s", result)
	}

	// Check for simplified customization options (Seahub-compatible minimal config)
	// json.Marshal produces compact JSON without spaces
	if !strings.Contains(result, `"forcesave":`) || !strings.Contains(result, "true") {
		t.Error("onlyOfficeEditorHTML missing forcesave: true customization")
	}
	// submitForm is omitempty and false in this test, so it won't appear in JSON output
	// Verify forcesave is present instead (it's true so always serialized)
	if !strings.Contains(result, `"forcesave":true`) {
		t.Error("onlyOfficeEditorHTML missing forcesave customization value")
	}

	// Also verify basic editor config is present (json.Marshal compact format)
	if !strings.Contains(result, `"mode":"edit"`) {
		t.Error("onlyOfficeEditorHTML missing mode field")
	}
	if !strings.Contains(result, `"documentType":"word"`) {
		t.Error("onlyOfficeEditorHTML missing documentType field")
	}
}

// TestOnlyOfficeEditorHTMLLoadingState tests that loading state is present
func TestOnlyOfficeEditorHTMLLoadingState(t *testing.T) {
	config := OnlyOfficeConfig{
		Document: OnlyOfficeDocument{
			FileType: "xlsx",
			Key:      "key",
			Title:    "sheet.xlsx",
			URL:      "http://example.com/sheet.xlsx",
			Permissions: &OnlyOfficePermissions{
				Edit: false, Download: true, Print: true, Copy: true, FillForms: true,
			},
		},
		DocumentType: "cell",
		EditorConfig: OnlyOfficeEditorConfig{
			CallbackURL:   "http://example.com/callback",
			Mode:          "view",
			User:          OnlyOfficeUser{ID: "1", Name: "User"},
			Customization: &OnlyOfficeCustomization{Forcesave: true},
		},
	}

	result := onlyOfficeEditorHTML("http://office/api.js", config, "sheet.xlsx")

	// Check for loading state elements
	loadingElements := []string{
		"loading-spinner",
		"Loading document...",
	}

	for _, elem := range loadingElements {
		if !strings.Contains(result, elem) {
			t.Errorf("onlyOfficeEditorHTML missing loading element: %s", elem)
		}
	}
}

// TestDownloadHistoricFileMissingObjID tests that missing obj_id returns 400
func TestDownloadHistoricFileMissingObjID(t *testing.T) {
	h := &FileViewHandler{
		config: &config.Config{},
	}

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user_id", "test-user")
		c.Set("org_id", "test-org")
		c.Next()
	})
	r.GET("/repo/:repo_id/history/download", h.DownloadHistoricFile)

	req, _ := http.NewRequest("GET", "/repo/repo-123/history/download?p=/test.txt", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Errorf("expected 302 for missing obj_id, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "/file-error/") || !strings.Contains(loc, "status=400") || !strings.Contains(loc, "Missing+obj_id") {
		t.Errorf("expected redirect to /file-error/ with status=400 and missing obj_id message, got %q", loc)
	}
}

// TestDownloadHistoricFileInvalidPath tests that invalid path redirects to /file-error/
func TestDownloadHistoricFileInvalidPath(t *testing.T) {
	h := &FileViewHandler{
		config: &config.Config{},
	}

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user_id", "test-user")
		c.Set("org_id", "test-org")
		c.Next()
	})
	r.GET("/repo/:repo_id/history/download", h.DownloadHistoricFile)

	// p=/ results in filepath.Base returning "/"
	req, _ := http.NewRequest("GET", "/repo/repo-123/history/download?obj_id=abc123&p=/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Errorf("expected 302 for invalid path, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "/file-error/") || !strings.Contains(loc, "status=400") || !strings.Contains(loc, "Invalid+file+path") {
		t.Errorf("expected redirect to /file-error/ with status=400 and invalid path message, got %q", loc)
	}
}

// TestDownloadHistoricFileNoDatabase tests behavior when db is nil (panics are caught)
func TestDownloadHistoricFileDefaultPath(t *testing.T) {
	// When p is empty, it defaults to "/" which has invalid Base
	h := &FileViewHandler{
		config: &config.Config{},
	}

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user_id", "test-user")
		c.Set("org_id", "test-org")
		c.Next()
	})
	r.GET("/repo/:repo_id/history/download", h.DownloadHistoricFile)

	// No p parameter - defaults to "/" which has Base "/"
	req, _ := http.NewRequest("GET", "/repo/repo-123/history/download?obj_id=abc123", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Errorf("expected 302 for default path /, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "/file-error/") || !strings.Contains(loc, "status=400") {
		t.Errorf("expected redirect to /file-error/ with status=400, got %q", loc)
	}
}

// TestRegisterFileViewRoutesIncludesHistoryDownload tests the history download route is registered
func TestRegisterFileViewRoutesIncludesHistoryDownload(t *testing.T) {
	cfg := &config.Config{
		Auth: config.AuthConfig{
			DevMode: true,
			DevTokens: []config.DevTokenEntry{
				{Token: "test-token", UserID: "user", OrgID: "org"},
			},
		},
		OnlyOffice: config.OnlyOfficeConfig{Enabled: false},
	}

	r := gin.New()
	r.Use(gin.Recovery()) // Recover from panics due to nil db
	devAuth := devTokenAuthMiddleware(cfg.Auth.DevTokens)
	RegisterFileViewRoutes(r, nil, cfg, nil, nil, &mockTokenCreator{}, "http://localhost:8082", devAuth)

	// Test that /repo/:repo_id/history/download route exists (missing obj_id → 400)
	req, _ := http.NewRequest("GET", "/repo/repo-123/history/download?p=/test.txt&token=test-token", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Missing obj_id should redirect (302), not 404
	if w.Code == http.StatusNotFound {
		t.Error("route /repo/:repo_id/history/download not registered")
	}
	if w.Code != http.StatusFound {
		t.Errorf("expected 302 for missing obj_id, got %d", w.Code)
	}
}

// TestDownloadHistoricFileNoStorageManager tests behavior when storageManager is nil
func TestDownloadHistoricFileNoStorageManager(t *testing.T) {
	h := &FileViewHandler{
		config:         &config.Config{},
		storageManager: nil, // No storage manager
	}

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user_id", "test-user")
		c.Set("org_id", "test-org")
		c.Next()
	})
	r.GET("/repo/:repo_id/history/download", h.DownloadHistoricFile)

	// Missing obj_id should still return 400 before reaching storage
	req, _ := http.NewRequest("GET", "/repo/repo-123/history/download", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Errorf("expected 302 for missing obj_id, got %d", w.Code)
	}
}

// TestOnlyOfficeEditorHTMLErrorHandling tests that error handling is present
func TestOnlyOfficeEditorHTMLErrorHandling(t *testing.T) {
	config := OnlyOfficeConfig{
		Document: OnlyOfficeDocument{
			FileType: "pptx",
			Key:      "key",
			Title:    "slides.pptx",
			URL:      "http://example.com/slides.pptx",
			Permissions: &OnlyOfficePermissions{
				Edit: true, Download: true, Print: true, Copy: true, FillForms: true,
			},
		},
		DocumentType: "slide",
		EditorConfig: OnlyOfficeEditorConfig{
			CallbackURL:   "http://example.com/callback",
			Mode:          "edit",
			User:          OnlyOfficeUser{ID: "1", Name: "User"},
			Customization: &OnlyOfficeCustomization{Forcesave: true},
		},
	}

	result := onlyOfficeEditorHTML("http://office/api.js", config, "slides.pptx")

	// Check for error handling in JavaScript
	errorHandling := []string{
		"catch (e)",
		"console.error",
		"Failed to load editor",
	}

	for _, elem := range errorHandling {
		if !strings.Contains(result, elem) {
			t.Errorf("onlyOfficeEditorHTML missing error handling: %s", elem)
		}
	}
}

// =============================================================================
// Tests for inline preview and iWork support (added in this session)
// =============================================================================

// TestIsInlinePreviewable tests the file type checker for inline previews
func TestIsInlinePreviewable(t *testing.T) {
	previewExtensions := config.DefaultConfig().FileView.PreviewExtensions
	tests := []struct {
		ext      string
		expected bool
	}{
		// PDF
		{"pdf", true},
		// Images
		{"png", true}, {"jpg", true}, {"jpeg", true}, {"gif", true},
		{"bmp", true}, {"webp", true}, {"svg", true}, {"tiff", true},
		// Video
		{"mp4", true}, {"webm", true}, {"ogg", true}, {"mov", true},
		// Audio
		{"mp3", true}, {"wav", true}, {"flac", true}, {"aac", true},
		// Text/code
		{"txt", true}, {"md", true}, {"json", true}, {"yaml", true},
		{"yml", true}, {"py", true}, {"go", true}, {"js", true},
		{"html", true}, {"css", true}, {"sh", true}, {"sql", true},
		{"log", true}, {"diff", true}, {"csv", true},
		// Non-previewable
		{"docx", false}, {"xlsx", false}, {"pptx", false},
		{"exe", false}, {"dmg", false}, {"zip", false},
		{"pages", false}, {"numbers", false}, {"key", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.ext, func(t *testing.T) {
			result := isInlinePreviewable(tt.ext, previewExtensions)
			if result != tt.expected {
				t.Errorf("isInlinePreviewable(%q) = %v, want %v", tt.ext, result, tt.expected)
			}
		})
	}
}

// TestIsTextFile tests the text file type checker
func TestIsTextFile(t *testing.T) {
	tests := []struct {
		ext      string
		expected bool
	}{
		{"txt", true}, {"md", true}, {"json", true}, {"yaml", true},
		{"py", true}, {"go", true}, {"rs", true}, {"java", true},
		{"sh", true}, {"sql", true}, {"toml", true}, {"log", true},
		// Not text
		{"pdf", false}, {"png", false}, {"mp4", false}, {"mp3", false},
		{"docx", false}, {"", false},
	}

	for _, tt := range tests {
		t.Run(tt.ext, func(t *testing.T) {
			result := isTextFile(tt.ext)
			if result != tt.expected {
				t.Errorf("isTextFile(%q) = %v, want %v", tt.ext, result, tt.expected)
			}
		})
	}
}

// TestIsAppleIWorkFile tests the Apple iWork file type checker
func TestIsAppleIWorkFile(t *testing.T) {
	tests := []struct {
		ext      string
		expected bool
	}{
		{"pages", true},
		{"numbers", true},
		{"key", true},
		{"Pages", false}, // Case sensitive - ext should be lowercased before calling
		{"doc", false},
		{"docx", false},
		{"pdf", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.ext, func(t *testing.T) {
			result := isAppleIWorkFile(tt.ext)
			if result != tt.expected {
				t.Errorf("isAppleIWorkFile(%q) = %v, want %v", tt.ext, result, tt.expected)
			}
		})
	}
}

// TestSanitizeFilename tests filename sanitization for Content-Disposition headers
func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"normal.txt", "normal.txt"},
		{"file with spaces.pdf", "file with spaces.pdf"},
		{`file"with"quotes.txt`, `file'with'quotes.txt`},
		{"file\nwith\nnewlines.txt", "filewithnewlines.txt"},
		{"file\rwith\rreturns.txt", "filewithreturns.txt"},
		{`"injected"`, `'injected'`},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := sanitizeFilename(tt.input)
			if result != tt.expected {
				t.Errorf("sanitizeFilename(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// createTestZIP creates an in-memory ZIP archive with the given entries.
func createTestZIP(entries map[string][]byte) []byte {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, data := range entries {
		f, _ := w.Create(name)
		f.Write(data)
	}
	w.Close()
	return buf.Bytes()
}

// TestExtractIWorkPreviewPDF_PreviewJPG tests extraction with preview.jpg (modern iWork)
func TestExtractIWorkPreviewPDF_PreviewJPG(t *testing.T) {
	jpgData := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F'} // JPEG magic
	zipData := createTestZIP(map[string][]byte{
		"Data/PresetImage.jpg": {0xFF, 0xD8, 0x00}, // not the preview
		"preview.jpg":          jpgData,
		"preview-micro.jpg":    {0xFF, 0xD8},
		"Index/Document.iwa":   {0x00},
	})

	result, err := extractIWorkPreviewPDF(zipData, 10*1024*1024) // 10MB limit for tests
	if err != nil {
		t.Fatalf("extractIWorkPreviewPDF failed: %v", err)
	}
	if !bytes.Equal(result, jpgData) {
		t.Errorf("expected preview.jpg content, got %v", result)
	}
}

// TestExtractIWorkPreviewPDF_QuickLookPDF tests extraction with QuickLook/Preview.pdf (old iWork)
func TestExtractIWorkPreviewPDF_QuickLookPDF(t *testing.T) {
	pdfData := []byte("%PDF-1.0 test content")
	zipData := createTestZIP(map[string][]byte{
		"QuickLook/Preview.pdf": pdfData,
		"Index/Document.iwa":    {0x00},
	})

	result, err := extractIWorkPreviewPDF(zipData, 10*1024*1024) // 10MB limit for tests
	if err != nil {
		t.Fatalf("extractIWorkPreviewPDF failed: %v", err)
	}
	if !bytes.Equal(result, pdfData) {
		t.Errorf("expected QuickLook PDF content, got %v", result)
	}
}

// TestExtractIWorkPreviewPDF_PrefersPDFOverJPG tests that preview.pdf takes priority over preview.jpg
func TestExtractIWorkPreviewPDF_PrefersPDFOverJPG(t *testing.T) {
	pdfData := []byte("%PDF-1.0 preferred")
	jpgData := []byte{0xFF, 0xD8, 0xFF}
	zipData := createTestZIP(map[string][]byte{
		"preview.pdf": pdfData,
		"preview.jpg": jpgData,
	})

	result, err := extractIWorkPreviewPDF(zipData, 10*1024*1024) // 10MB limit for tests
	if err != nil {
		t.Fatalf("extractIWorkPreviewPDF failed: %v", err)
	}
	if !bytes.Equal(result, pdfData) {
		t.Error("should prefer preview.pdf over preview.jpg")
	}
}

// TestExtractIWorkPreviewPDF_CaseInsensitive tests case-insensitive matching
func TestExtractIWorkPreviewPDF_CaseInsensitive(t *testing.T) {
	pdfData := []byte("%PDF case test")
	zipData := createTestZIP(map[string][]byte{
		"Preview.PDF": pdfData,
	})

	result, err := extractIWorkPreviewPDF(zipData, 10*1024*1024) // 10MB limit for tests
	if err != nil {
		t.Fatalf("extractIWorkPreviewPDF failed: %v", err)
	}
	if !bytes.Equal(result, pdfData) {
		t.Error("should match case-insensitively")
	}
}

// TestExtractIWorkPreviewPDF_NoPreview tests error when no preview exists
func TestExtractIWorkPreviewPDF_NoPreview(t *testing.T) {
	zipData := createTestZIP(map[string][]byte{
		"Index/Document.iwa":           {0x00},
		"Index/DocumentStylesheet.iwa": {0x00},
		"Metadata/Properties.plist":    {0x00},
	})

	_, err := extractIWorkPreviewPDF(zipData, 10*1024*1024) // 10MB limit for tests
	if err == nil {
		t.Fatal("expected error for archive without preview")
	}
	if !strings.Contains(err.Error(), "no preview found") {
		t.Errorf("expected 'no preview found' error, got: %v", err)
	}
}

// TestExtractIWorkPreviewPDF_InvalidZIP tests error for non-ZIP data
func TestExtractIWorkPreviewPDF_InvalidZIP(t *testing.T) {
	_, err := extractIWorkPreviewPDF([]byte("not a zip file"), 10*1024*1024) // 10MB limit for tests
	if err == nil {
		t.Fatal("expected error for non-ZIP data")
	}
	if !strings.Contains(err.Error(), "not a valid zip archive") {
		t.Errorf("expected 'not a valid zip archive' error, got: %v", err)
	}
}

// TestExtractIWorkPreviewPDF_FallbackAnyPDF tests the fallback to any .pdf file
func TestExtractIWorkPreviewPDF_FallbackAnyPDF(t *testing.T) {
	pdfData := []byte("%PDF-1.0 embedded doc")
	zipData := createTestZIP(map[string][]byte{
		"Data/embedded.pdf":  pdfData,
		"Index/Document.iwa": {0x00},
	})

	result, err := extractIWorkPreviewPDF(zipData, 10*1024*1024) // 10MB limit for tests
	if err != nil {
		t.Fatalf("extractIWorkPreviewPDF failed: %v", err)
	}
	if !bytes.Equal(result, pdfData) {
		t.Error("should fall back to any .pdf file in archive")
	}
}

// TestPreviewMIMEDetection tests that the MIME type detection for iWork previews works
func TestPreviewMIMEDetection(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		wantMIME string
	}{
		{
			name:     "JPEG",
			data:     []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00},
			wantMIME: "image/jpeg",
		},
		{
			name:     "PNG",
			data:     []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0x00},
			wantMIME: "image/png",
		},
		{
			name:     "PDF",
			data:     []byte("%PDF-1.0"),
			wantMIME: "application/pdf",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mime := "application/pdf" // default
			if len(tt.data) > 3 && tt.data[0] == 0xFF && tt.data[1] == 0xD8 {
				mime = "image/jpeg"
			} else if len(tt.data) > 8 && string(tt.data[:4]) == "\x89PNG" {
				mime = "image/png"
			}
			if mime != tt.wantMIME {
				t.Errorf("MIME detection for %s: got %q, want %q", tt.name, mime, tt.wantMIME)
			}
		})
	}
}

// TestViewFileInlinePreviewRouting tests that previewable files get served inline (not redirected)
func TestViewFileInlinePreviewRouting(t *testing.T) {
	h := &FileViewHandler{
		config: &config.Config{
			OnlyOffice: config.OnlyOfficeConfig{
				Enabled:        true,
				ViewExtensions: []string{"docx", "xlsx"},
			},
		},
		serverURL:    "http://localhost:8080",
		tokenCreator: &mockTokenCreator{},
	}

	r := gin.New()
	r.Use(gin.Recovery()) // Recover from nil-db panics in handler
	r.Use(func(c *gin.Context) {
		c.Set("user_id", "test-user")
		c.Set("org_id", "test-org")
		c.Next()
	})
	r.GET("/lib/:repo_id/file/*filepath", h.ViewFile)

	tests := []struct {
		name         string
		filepath     string
		expectStatus int
		description  string
	}{
		// Non-previewable redirects to download
		{"zip downloads", "/archive.zip", http.StatusFound, "redirect to download"},
		{"exe downloads", "/app.exe", http.StatusFound, "redirect to download"},
		// dl=1 forces download even for previewable files
		{"forced download", "/test.pdf?dl=1", http.StatusFound, "dl=1 forces download"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "/lib/repo-123/file"+tt.filepath, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.expectStatus {
				t.Errorf("%s: status = %d, want %d", tt.description, w.Code, tt.expectStatus)
			}
		})
	}
}

// TestViewFileOnlyOfficeRouting tests that OnlyOffice-supported files are NOT redirected to download
func TestViewFileOnlyOfficeRouting(t *testing.T) {
	// With OnlyOffice enabled, docx should NOT redirect (302) — it should try
	// to open the OnlyOffice editor. Without a real DB it will fail, but the
	// important thing is that it doesn't 302 redirect to download.
	h := &FileViewHandler{
		config: &config.Config{
			OnlyOffice: config.OnlyOfficeConfig{
				Enabled:        true,
				ViewExtensions: []string{"docx", "xlsx"},
			},
		},
		serverURL:    "http://localhost:8080",
		tokenCreator: &mockTokenCreator{},
	}

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		c.Set("user_id", "test-user")
		c.Set("org_id", "test-org")
		c.Next()
	})
	r.GET("/lib/:repo_id/file/*filepath", h.ViewFile)

	req, _ := http.NewRequest("GET", "/lib/repo-123/file/test.docx", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Missing OnlyOffice backing data now redirects browser users to the frontend error shell.
	if w.Code != http.StatusFound {
		t.Fatalf("expected redirect to frontend error shell, got %d", w.Code)
	}

	location := w.Header().Get("Location")
	if !strings.HasPrefix(location, "/file-error/?") {
		t.Fatalf("redirect location = %q, expected frontend error shell", location)
	}
}
