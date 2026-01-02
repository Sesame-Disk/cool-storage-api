package v2

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// TestErrorPageHTML tests the error page HTML generator
func TestErrorPageHTML(t *testing.T) {
	tests := []struct {
		title    string
		message  string
		expected []string
	}{
		{
			title:   "File Not Found",
			message: "The requested file could not be found.",
			expected: []string{
				"<title>File Not Found - SesameFS</title>",
				"<h1>File Not Found</h1>",
				"<p>The requested file could not be found.</p>",
			},
		},
		{
			title:   "Authentication Required",
			message: "Please provide a valid authentication token.",
			expected: []string{
				"<title>Authentication Required - SesameFS</title>",
				"<h1>Authentication Required</h1>",
				"<p>Please provide a valid authentication token.</p>",
			},
		},
		{
			title:   "Internal Error",
			message: "Something went wrong.",
			expected: []string{
				"<!DOCTYPE html>",
				"error-container",
				"#c0392b", // Error color
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.title, func(t *testing.T) {
			result := errorPageHTML(tt.title, tt.message)

			for _, exp := range tt.expected {
				if !strings.Contains(result, exp) {
					t.Errorf("errorPageHTML(%q, %q) missing expected content: %q", tt.title, tt.message, exp)
				}
			}
		})
	}
}

// TestOnlyOfficeEditorHTML tests the OnlyOffice editor HTML generator
func TestOnlyOfficeEditorHTML(t *testing.T) {
	config := OnlyOfficeConfig{
		Document: OnlyOfficeDocument{
			FileType: "docx",
			Key:      "abc123def456",
			Title:    "test-document.docx",
			URL:      "https://example.com/download/test.docx",
		},
		DocumentType: "word",
		EditorConfig: OnlyOfficeEditorConfig{
			CallbackURL: "https://example.com/callback",
			Mode:        "edit",
			User: OnlyOfficeUser{
				ID:   "user-123",
				Name: "Test User",
			},
		},
		Token: "jwt-token-here",
	}

	result := onlyOfficeEditorHTML("https://office.example.com/api.js", config, "test-document.docx")

	// Check for required elements
	// Note: Go templates escape / as \/ in URLs within script tags
	expected := []string{
		"<!DOCTYPE html>",
		"<title>test-document.docx - SesameFS</title>",
		`<script src="https://office.example.com/api.js"></script>`,
		`"fileType": "docx"`,
		`"key": "abc123def456"`,
		`"title": "test-document.docx"`,
		"example.com", // URL is escaped, just check domain
		"test.docx",   // And filename
		`"documentType": "word"`,
		`"mode": "edit"`,
		`"id": "user-123"`,
		`"name": "Test User"`,
		`"token": "jwt-token-here"`,
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
		},
		DocumentType: "cell",
		EditorConfig: OnlyOfficeEditorConfig{
			CallbackURL: "https://example.com/callback",
			Mode:        "view",
			User: OnlyOfficeUser{
				ID:   "viewer-456",
				Name: "Viewer User",
			},
		},
		// Token is empty - should not include token in config
	}

	result := onlyOfficeEditorHTML("https://office.example.com/api.js", config, "data.xlsx")

	// Should NOT contain token field when token is empty
	if strings.Contains(result, `"token":`) {
		t.Error("onlyOfficeEditorHTML should not include token field when token is empty")
	}

	// Should still contain other required fields
	if !strings.Contains(result, `"mode": "view"`) {
		t.Error("onlyOfficeEditorHTML missing mode field")
	}

	if !strings.Contains(result, `"documentType": "cell"`) {
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

// TestFileViewAuthMiddleware tests the authentication middleware
func TestFileViewAuthMiddleware(t *testing.T) {
	cfg := &config.Config{
		Auth: config.AuthConfig{
			DevMode: true,
			DevTokens: []config.DevTokenEntry{
				{Token: "valid-token-123", UserID: "user-1", OrgID: "org-1"},
				{Token: "admin-token-456", UserID: "admin", OrgID: "org-admin"},
			},
		},
	}

	h := &FileViewHandler{
		config: cfg,
	}

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
			r.Use(h.fileViewAuthMiddleware(cfg))
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

// TestFileViewAuthMiddlewareNonDevMode tests auth middleware in non-dev mode
func TestFileViewAuthMiddlewareNonDevMode(t *testing.T) {
	cfg := &config.Config{
		Auth: config.AuthConfig{
			DevMode: false, // Not in dev mode
		},
	}

	h := &FileViewHandler{
		config: cfg,
	}

	r := gin.New()
	r.Use(h.fileViewAuthMiddleware(cfg))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	// Even with a token, should fail in non-dev mode (OIDC not implemented)
	req, _ := http.NewRequest("GET", "/test?token=any-token", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("non-dev mode should return 401, got %d", w.Code)
	}
}

// TestViewFileRedirectsNonOfficeFiles tests that non-office files redirect to download
func TestViewFileRedirectsNonOfficeFiles(t *testing.T) {
	h := &FileViewHandler{
		config: &config.Config{
			OnlyOffice: config.OnlyOfficeConfig{
				Enabled:        true,
				ViewExtensions: []string{"docx", "xlsx", "pptx"},
			},
		},
		serverURL: "http://localhost:8080",
	}

	r := gin.New()
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
			name:           "png file redirects",
			filepath:       "/image.png",
			expectStatus:   http.StatusFound,
			expectRedirect: true,
		},
		{
			name:           "txt file redirects",
			filepath:       "/readme.txt",
			expectStatus:   http.StatusFound,
			expectRedirect: true,
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
				if !strings.Contains(location, "/api/v2/files/repo-123/download") {
					t.Errorf("redirect location = %q, expected download URL", location)
				}
			}
		})
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
		serverURL: "http://localhost:8080",
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
	if !strings.Contains(location, "/download") {
		t.Errorf("expected redirect to download, got %q", location)
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
		serverURL: "http://localhost:8080",
	}

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user_id", "test-user")
		c.Set("org_id", "test-org")
		c.Next()
	})
	r.GET("/lib/:repo_id/file/*filepath", h.ViewFile)

	tests := []struct {
		name         string
		requestPath  string
		expectInPath string
	}{
		{
			name:         "path with leading slash",
			requestPath:  "/lib/repo-123/file/docs/file.txt",
			expectInPath: "/docs/file.txt",
		},
		{
			name:         "path in subdirectory",
			requestPath:  "/lib/repo-123/file/nested/deep/file.pdf",
			expectInPath: "/nested/deep/file.pdf",
		},
		{
			name:         "root file",
			requestPath:  "/lib/repo-123/file/root.txt",
			expectInPath: "/root.txt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", tt.requestPath, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			// Check redirect URL contains the normalized path
			location := w.Header().Get("Location")
			if !strings.Contains(location, "p="+tt.expectInPath) {
				t.Errorf("redirect URL = %q, expected to contain p=%s", location, tt.expectInPath)
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

	// Register routes
	RegisterFileViewRoutes(r, nil, cfg, nil, nil, "http://localhost:8080", nil)

	// Test that the route exists
	req, _ := http.NewRequest("GET", "/lib/repo-123/file/test.txt?token=test-token", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Should get a redirect (not 404), proving route is registered
	if w.Code == http.StatusNotFound {
		t.Error("route /lib/:repo_id/file/*filepath not registered")
	}

	// With valid token, should redirect to download
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
		},
		DocumentType: "word",
		EditorConfig: OnlyOfficeEditorConfig{
			CallbackURL: "http://example.com/callback",
			Mode:        "edit",
			User:        OnlyOfficeUser{ID: "1", Name: "User"},
		},
	}

	result := onlyOfficeEditorHTML("http://office/api.js", config, "doc.docx")

	// Check for customization options in the generated HTML
	customizations := []string{
		`"autosave": true`,
		`"forcesave": true`,
		`"chat": false`,
		`"comments": true`,
		`"spellcheck": true`,
		`"uiTheme": "theme-light"`,
		`"height": "100%"`,
		`"width": "100%"`,
		`"type": "desktop"`,
	}

	for _, custom := range customizations {
		if !strings.Contains(result, custom) {
			t.Errorf("onlyOfficeEditorHTML missing customization: %s", custom)
		}
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
		},
		DocumentType: "cell",
		EditorConfig: OnlyOfficeEditorConfig{
			CallbackURL: "http://example.com/callback",
			Mode:        "view",
			User:        OnlyOfficeUser{ID: "1", Name: "User"},
		},
	}

	result := onlyOfficeEditorHTML("http://office/api.js", config, "sheet.xlsx")

	// Check for loading state elements
	loadingElements := []string{
		"loading-spinner",
		"Loading document...",
		"@keyframes spin",
		"animation: spin",
	}

	for _, elem := range loadingElements {
		if !strings.Contains(result, elem) {
			t.Errorf("onlyOfficeEditorHTML missing loading element: %s", elem)
		}
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
		},
		DocumentType: "slide",
		EditorConfig: OnlyOfficeEditorConfig{
			CallbackURL: "http://example.com/callback",
			Mode:        "edit",
			User:        OnlyOfficeUser{ID: "1", Name: "User"},
		},
	}

	result := onlyOfficeEditorHTML("http://office/api.js", config, "slides.pptx")

	// Check for error handling in JavaScript
	errorHandling := []string{
		"catch (e)",
		"console.error",
		"Failed to load editor",
		"e.message",
	}

	for _, elem := range errorHandling {
		if !strings.Contains(result, elem) {
			t.Errorf("onlyOfficeEditorHTML missing error handling: %s", elem)
		}
	}
}
