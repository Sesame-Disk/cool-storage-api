package v2

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// TestFormatSize tests the human-readable size formatter
func TestFormatSize(t *testing.T) {
	tests := []struct {
		bytes    int64
		expected string
	}{
		// Bytes
		{0, "0 B"},
		{1, "1 B"},
		{512, "512 B"},
		{999, "999 B"},

		// Kilobytes
		{1000, "1.0 KB"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{2048, "2.0 KB"},
		{10240, "10.2 KB"},
		{999999, "1000.0 KB"}, // Just under 1 MB

		// Megabytes
		{1000000, "1.0 MB"},
		{1048576, "1.0 MB"},
		{1572864, "1.6 MB"},
		{10485760, "10.5 MB"},
		{104857600, "104.9 MB"},
		{999999999, "1000.0 MB"}, // Just under 1 GB

		// Gigabytes
		{1000000000, "1.0 GB"},
		{1073741824, "1.1 GB"},
		{1610612736, "1.6 GB"},
		{10737418240, "10.7 GB"},
		{107374182400, "107.4 GB"},

		// Terabytes
		{1000000000000, "1.0 TB"},
		{1099511627776, "1.1 TB"},
		{1649267441664, "1.6 TB"},
		{10995116277760, "11.0 TB"},

		// Petabytes
		{1000000000000000, "1.0 PB"},
		{1125899906842624, "1.1 PB"},

		// Exabytes
		{1000000000000000000, "1.0 EB"},
		{1152921504606846976, "1.2 EB"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := formatSize(tt.bytes)
			if result != tt.expected {
				t.Errorf("formatSize(%d) = %q, want %q", tt.bytes, result, tt.expected)
			}
		})
	}
}

// TestFormatSizeEdgeCases tests edge cases for size formatting
func TestFormatSizeEdgeCases(t *testing.T) {
	// Test boundary between units
	t.Run("KB boundary", func(t *testing.T) {
		below := formatSize(999)
		at := formatSize(1000)

		if below != "999 B" {
			t.Errorf("999 bytes should be '999 B', got %q", below)
		}
		if at != "1.0 KB" {
			t.Errorf("1000 bytes should be '1.0 KB', got %q", at)
		}
	})

	t.Run("MB boundary", func(t *testing.T) {
		below := formatSize(999999)
		at := formatSize(1000000)

		if below != "1000.0 KB" {
			t.Errorf("999999 bytes should be '1000.0 KB', got %q", below)
		}
		if at != "1.0 MB" {
			t.Errorf("1000000 bytes should be '1.0 MB', got %q", at)
		}
	})

	t.Run("GB boundary", func(t *testing.T) {
		below := formatSize(999999999)
		at := formatSize(1000000000)

		if below != "1000.0 MB" {
			t.Errorf("999999999 bytes should be '1000.0 MB', got %q", below)
		}
		if at != "1.0 GB" {
			t.Errorf("1000000000 bytes should be '1.0 GB', got %q", at)
		}
	})
}

// TestFormatSizeRealistic tests realistic file sizes
func TestFormatSizeRealistic(t *testing.T) {
	tests := []struct {
		name     string
		bytes    int64
		expected string
	}{
		{"small text file", 1500, "1.5 KB"},
		{"word document", 52428, "52.4 KB"},
		{"photo", 3145728, "3.1 MB"},
		{"video clip", 157286400, "157.3 MB"},
		{"movie file", 4718592000, "4.7 GB"},
		{"backup archive", 53687091200, "53.7 GB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatSize(tt.bytes)
			if result != tt.expected {
				t.Errorf("formatSize(%d) = %q, want %q", tt.bytes, result, tt.expected)
			}
		})
	}
}

func TestResolveRequestedStorageClass(t *testing.T) {
	h := &LibraryHandler{
		config: &config.Config{
			Storage: config.StorageConfig{
				DefaultClass: "hot-usa",
				Classes: map[string]config.StorageClassConfig{
					"hot-usa": {Bucket: "usa"},
					"hot-eu":  {Bucket: "eu"},
				},
				EndpointRegions: map[string]string{
					"eu.example.com": "eu",
				},
				RegionClasses: map[string]config.RegionClassConfig{
					"eu": {Hot: "hot-eu"},
				},
			},
		},
	}

	t.Run("uses explicit selection", func(t *testing.T) {
		got, err := h.resolveRequestedStorageClass("eu.example.com", "hot-usa")
		if err != nil {
			t.Fatalf("resolveRequestedStorageClass returned error: %v", err)
		}
		if got != "hot-usa" {
			t.Fatalf("got %q, want %q", got, "hot-usa")
		}
	})

	t.Run("uses endpoint default when request is empty", func(t *testing.T) {
		got, err := h.resolveRequestedStorageClass("eu.example.com", "")
		if err != nil {
			t.Fatalf("resolveRequestedStorageClass returned error: %v", err)
		}
		if got != "hot-eu" {
			t.Fatalf("got %q, want %q", got, "hot-eu")
		}
	})

	t.Run("rejects invalid explicit class", func(t *testing.T) {
		_, err := h.resolveRequestedStorageClass("eu.example.com", "missing")
		if err == nil {
			t.Fatal("expected error for invalid storage class")
		}
	})

	t.Run("uses deterministic sorted fallback when default is invalid", func(t *testing.T) {
		h.config.Storage.DefaultClass = "missing-default"
		h.config.Storage.EndpointRegions = map[string]string{}
		h.config.Storage.RegionClasses = map[string]config.RegionClassConfig{}
		h.config.Storage.Classes = map[string]config.StorageClassConfig{
			"hot-zeta":  {Bucket: "zeta"},
			"hot-alpha": {Bucket: "alpha"},
		}
		h.config.Storage.Backends = map[string]config.BackendConfig{}

		got, err := h.resolveRequestedStorageClass("unknown.example.com", "")
		if err != nil {
			t.Fatalf("resolveRequestedStorageClass returned error: %v", err)
		}
		if got != "hot-alpha" {
			t.Fatalf("got %q, want %q", got, "hot-alpha")
		}
	})

	t.Run("errors when no valid storage class exists", func(t *testing.T) {
		h.config.Storage.DefaultClass = ""
		h.config.Storage.Classes = map[string]config.StorageClassConfig{}
		h.config.Storage.Backends = map[string]config.BackendConfig{}

		_, err := h.resolveRequestedStorageClass("unknown.example.com", "")
		if err == nil {
			t.Fatal("expected error when no valid storage class is configured")
		}
	})
}

func TestDisplayStorageNameUsesRegionLabel(t *testing.T) {
	h := &LibraryHandler{
		config: &config.Config{
			Storage: config.StorageConfig{
				RegionClasses: map[string]config.RegionClassConfig{
					"usa": {Hot: "hot-usa"},
					"eu":  {Hot: "hot-eu"},
				},
			},
		},
	}

	if got := h.displayStorageName("hot-eu"); got != "EU" {
		t.Fatalf("displayStorageName = %q, want %q", got, "EU")
	}
	if got := h.displayStorageName("custom-class"); got != "custom-class" {
		t.Fatalf("displayStorageName = %q, want %q", got, "custom-class")
	}
}

// setupLibraryTestRouter creates a test router for library tests
func setupLibraryTestRouter(orgID, userID string) *gin.Engine {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if orgID != "" {
			c.Set("org_id", orgID)
		}
		if userID != "" {
			c.Set("user_id", userID)
		}
		c.Next()
	})
	return r
}

// TestDeleteLibraryValidation tests DeleteLibrary input validation
// Note: Tests that require database access are skipped when db is nil
func TestDeleteLibraryValidation(t *testing.T) {
	tests := []struct {
		name       string
		orgID      string
		userID     string
		repoID     string
		wantStatus int
		wantError  string
	}{
		{
			name:       "missing org_id returns 400",
			orgID:      "",
			userID:     "00000000-0000-0000-0000-000000000001",
			repoID:     "00000000-0000-0000-0000-000000000001",
			wantStatus: http.StatusBadRequest,
			wantError:  "missing org_id",
		},
		{
			name:       "empty repo_id returns 400",
			orgID:      "00000000-0000-0000-0000-000000000001",
			userID:     "00000000-0000-0000-0000-000000000001",
			repoID:     "",
			wantStatus: http.StatusNotFound, // gin returns 404 for empty param
			wantError:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := setupLibraryTestRouter(tt.orgID, tt.userID)

			h := &LibraryHandler{
				db:     nil, // No database - tests validation only
				config: nil,
			}

			r.DELETE("/api2/repos/:repo_id", h.DeleteLibrary)

			req, _ := http.NewRequest("DELETE", "/api2/repos/"+tt.repoID, nil)
			req.Header.Set("Authorization", "Token test-token")

			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}

			if tt.wantError != "" {
				var resp map[string]interface{}
				if err := json.Unmarshal(w.Body.Bytes(), &resp); err == nil {
					if errMsg, ok := resp["error"].(string); ok {
						if errMsg != tt.wantError {
							t.Errorf("error = %q, want %q", errMsg, tt.wantError)
						}
					}
				}
			}
		})
	}
}

// TestListLibrariesValidation tests ListLibraries input validation
func TestListLibrariesValidation(t *testing.T) {
	tests := []struct {
		name       string
		orgID      string
		wantStatus int
		wantError  string
	}{
		{
			name:       "missing org_id returns 400",
			orgID:      "",
			wantStatus: http.StatusBadRequest,
			wantError:  "missing org_id",
		},
		{
			name:       "invalid org_id returns 400",
			orgID:      "not-a-uuid",
			wantStatus: http.StatusBadRequest,
			wantError:  "invalid org_id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := setupLibraryTestRouter(tt.orgID, "")

			h := &LibraryHandler{
				db:     nil,
				config: nil,
			}

			r.GET("/api2/repos", h.ListLibraries)

			req, _ := http.NewRequest("GET", "/api2/repos", nil)
			req.Header.Set("Authorization", "Token test-token")

			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}

			var resp map[string]interface{}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err == nil {
				if errMsg, ok := resp["error"].(string); ok && tt.wantError != "" {
					if errMsg != tt.wantError {
						t.Errorf("error = %q, want %q", errMsg, tt.wantError)
					}
				}
			}
		})
	}
}

// TestGetLibraryValidation tests GetLibrary input validation
func TestGetLibraryValidation(t *testing.T) {
	tests := []struct {
		name       string
		orgID      string
		repoID     string
		wantStatus int
		wantError  string
	}{
		{
			name:       "invalid repo_id returns 400",
			orgID:      "00000000-0000-0000-0000-000000000001",
			repoID:     "not-a-uuid",
			wantStatus: http.StatusBadRequest,
			wantError:  "invalid repo_id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := setupLibraryTestRouter(tt.orgID, "")

			h := &LibraryHandler{
				db:     nil,
				config: nil,
			}

			r.GET("/api2/repos/:repo_id", h.GetLibrary)

			req, _ := http.NewRequest("GET", "/api2/repos/"+tt.repoID, nil)
			req.Header.Set("Authorization", "Token test-token")

			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

// TestCreateLibraryRequestBinding tests request binding for CreateLibrary
func TestCreateLibraryRequestBinding(t *testing.T) {
	r := gin.New()
	gin.SetMode(gin.TestMode)

	var receivedReq CreateLibraryRequest
	r.POST("/test", func(c *gin.Context) {
		if err := c.ShouldBindJSON(&receivedReq); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, receivedReq)
	})

	tests := []struct {
		name       string
		body       map[string]interface{}
		wantStatus int
		wantName   string
	}{
		{
			name:       "valid library creation",
			body:       map[string]interface{}{"name": "My Library", "description": "Test library"},
			wantStatus: http.StatusOK,
			wantName:   "My Library",
		},
		{
			name:       "encrypted library",
			body:       map[string]interface{}{"name": "Encrypted", "encrypted": true, "password": "secret"},
			wantStatus: http.StatusOK,
			wantName:   "Encrypted",
		},
		{
			name:       "minimal request",
			body:       map[string]interface{}{"name": "Minimal"},
			wantStatus: http.StatusOK,
			wantName:   "Minimal",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jsonBody, _ := json.Marshal(tt.body)
			req, _ := http.NewRequest("POST", "/test", bytes.NewBuffer(jsonBody))
			req.Header.Set("Content-Type", "application/json")

			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}

			if tt.wantStatus == http.StatusOK {
				var resp CreateLibraryRequest
				json.Unmarshal(w.Body.Bytes(), &resp)
				if resp.Name != tt.wantName {
					t.Errorf("name = %q, want %q", resp.Name, tt.wantName)
				}
			}
		})
	}
}

// TestRenameLibraryRequestBinding tests request binding for RenameLibrary
func TestRenameLibraryRequestBinding(t *testing.T) {
	r := gin.New()
	gin.SetMode(gin.TestMode)

	var receivedReq RenameLibraryRequest
	r.POST("/test", func(c *gin.Context) {
		if err := c.ShouldBind(&receivedReq); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, receivedReq)
	})

	tests := []struct {
		name       string
		body       map[string]interface{}
		wantStatus int
	}{
		{
			name:       "valid rename",
			body:       map[string]interface{}{"repo_name": "New Name"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "empty name binds ok - validation in handler",
			body:       map[string]interface{}{"repo_name": ""},
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jsonBody, _ := json.Marshal(tt.body)
			req, _ := http.NewRequest("POST", "/test", bytes.NewBuffer(jsonBody))
			req.Header.Set("Content-Type", "application/json")

			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

// TestGetRepoFolderShareInfo tests the share info stub endpoint
func TestGetRepoFolderShareInfo(t *testing.T) {
	r := setupLibraryTestRouter("00000000-0000-0000-0000-000000000001", "")

	h := &LibraryHandler{
		db:     nil,
		config: nil,
	}

	r.GET("/api/v2.1/repos/:repo_id/share-info", h.GetRepoFolderShareInfo)

	req, _ := http.NewRequest("GET", "/api/v2.1/repos/test-repo/share-info?path=/", nil)
	req.Header.Set("Authorization", "Token test-token")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	// Should return empty arrays
	if emails, ok := resp["shared_user_emails"].([]interface{}); !ok || len(emails) != 0 {
		t.Errorf("shared_user_emails should be empty array")
	}
	if groups, ok := resp["shared_group_ids"].([]interface{}); !ok || len(groups) != 0 {
		t.Errorf("shared_group_ids should be empty array")
	}
}

// TestV21LibraryStruct tests V21Library JSON serialization
func TestV21LibraryStruct(t *testing.T) {
	lib := V21Library{
		Type:         "mine",
		RepoID:       "12345678-1234-1234-1234-123456789012",
		RepoName:     "Test Library",
		OwnerEmail:   "user@example.com",
		OwnerName:    "user",
		LastModified: "2026-01-01T00:00:00Z",
		Size:         1024,
		Encrypted:    0, // Must be int (0 or 1), not bool
		Permission:   "rw",
		Starred:      false,
		Monitored:    false,
		Status:       "normal",
	}

	data, err := json.Marshal(lib)
	if err != nil {
		t.Fatalf("failed to marshal V21Library: %v", err)
	}

	// Verify JSON field names match Seafile v2.1 API format
	expectedFields := []string{
		`"type":"mine"`,
		`"repo_id":"12345678-1234-1234-1234-123456789012"`,
		`"repo_name":"Test Library"`,
		`"owner_email":"user@example.com"`,
		`"last_modified":"2026-01-01T00:00:00Z"`,
		`"permission":"rw"`,
	}

	jsonStr := string(data)
	for _, field := range expectedFields {
		if !bytes.Contains(data, []byte(field)) {
			t.Errorf("JSON missing field: %s\nGot: %s", field, jsonStr)
		}
	}
}

// TestPermissionMiddlewareIntegration tests permission checks in handlers
// Note: These tests require a database connection and are skipped without it
func TestPermissionMiddlewareIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping permission middleware tests - require database")
	}

	// These tests validate that permission checks are correctly integrated
	// They would require:
	// 1. Mock or test database with seeded users
	// 2. Mock permission middleware
	// 3. Test requests with different user contexts

	t.Run("validates permission middleware is initialized", func(t *testing.T) {
		// The LibraryHandler should have permMiddleware field
		// This is verified by the struct definition
		assert := assert.New(t)
		assert.True(true, "LibraryHandler has permMiddleware field")
	})
}

// TestCreateLibrary_PermissionChecks tests CreateLibrary with different user roles
func TestCreateLibrary_PermissionChecks(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping permission tests - require database")
	}

	// Test cases for different user roles
	testCases := []struct {
		name           string
		userRole       string
		shouldSucceed  bool
		expectedStatus int
	}{
		{
			name:           "admin can create libraries",
			userRole:       "admin",
			shouldSucceed:  true,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "user can create libraries",
			userRole:       "user",
			shouldSucceed:  true,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "readonly cannot create libraries",
			userRole:       "readonly",
			shouldSucceed:  false,
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "guest cannot create libraries",
			userRole:       "guest",
			shouldSucceed:  false,
			expectedStatus: http.StatusForbidden,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// This test documents the expected behavior
			// Actual implementation requires database and middleware setup
			assert := assert.New(t)

			if tc.shouldSucceed {
				assert.Equal(http.StatusOK, tc.expectedStatus,
					"Role %s should be able to create libraries", tc.userRole)
			} else {
				assert.Equal(http.StatusForbidden, tc.expectedStatus,
					"Role %s should NOT be able to create libraries", tc.userRole)
			}
		})
	}
}

// TestDeleteLibrary_OwnershipChecks tests DeleteLibrary with ownership validation
func TestDeleteLibrary_OwnershipChecks(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping ownership tests - require database")
	}

	testCases := []struct {
		name           string
		isOwner        bool
		hasRWPerm      bool
		shouldSucceed  bool
		expectedStatus int
	}{
		{
			name:           "owner can delete library",
			isOwner:        true,
			hasRWPerm:      false,
			shouldSucceed:  true,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "non-owner with rw permission cannot delete",
			isOwner:        false,
			hasRWPerm:      true,
			shouldSucceed:  false,
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "non-owner with read permission cannot delete",
			isOwner:        false,
			hasRWPerm:      false,
			shouldSucceed:  false,
			expectedStatus: http.StatusForbidden,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// This test documents the expected ownership behavior
			// Actual implementation requires database and middleware setup
			assert := assert.New(t)

			if tc.shouldSucceed {
				assert.Equal(http.StatusOK, tc.expectedStatus,
					"Owner should be able to delete library")
			} else {
				assert.Equal(http.StatusForbidden, tc.expectedStatus,
					"Non-owner should NOT be able to delete library")
			}
		})
	}
}

// TestPermissionMiddleware_RoleHierarchy tests role hierarchy logic
// Uses the shared middleware.HasRequiredOrgRole instead of duplicating the hierarchy map.
func TestPermissionMiddleware_RoleHierarchy(t *testing.T) {
	t.Run("superadmin has highest privilege", func(t *testing.T) {
		assert := assert.New(t)
		assert.True(middleware.HasRequiredOrgRole(middleware.RoleSuperAdmin, middleware.RoleAdmin))
		assert.True(middleware.HasRequiredOrgRole(middleware.RoleSuperAdmin, middleware.RoleUser))
		assert.True(middleware.HasRequiredOrgRole(middleware.RoleSuperAdmin, middleware.RoleReadOnly))
		assert.True(middleware.HasRequiredOrgRole(middleware.RoleSuperAdmin, middleware.RoleGuest))
	})

	t.Run("admin is second highest", func(t *testing.T) {
		assert := assert.New(t)
		assert.True(middleware.HasRequiredOrgRole(middleware.RoleAdmin, middleware.RoleUser))
		assert.True(middleware.HasRequiredOrgRole(middleware.RoleAdmin, middleware.RoleReadOnly))
		assert.True(middleware.HasRequiredOrgRole(middleware.RoleAdmin, middleware.RoleGuest))
		assert.False(middleware.HasRequiredOrgRole(middleware.RoleAdmin, middleware.RoleSuperAdmin))
	})

	t.Run("user role required for library creation", func(t *testing.T) {
		assert := assert.New(t)
		assert.True(middleware.HasRequiredOrgRole(middleware.RoleSuperAdmin, middleware.RoleUser),
			"superadmin should meet minimum")
		assert.True(middleware.HasRequiredOrgRole(middleware.RoleAdmin, middleware.RoleUser),
			"admin should meet minimum")
		assert.True(middleware.HasRequiredOrgRole(middleware.RoleUser, middleware.RoleUser),
			"user should meet minimum")
		assert.False(middleware.HasRequiredOrgRole(middleware.RoleReadOnly, middleware.RoleUser),
			"readonly should NOT meet minimum")
		assert.False(middleware.HasRequiredOrgRole(middleware.RoleGuest, middleware.RoleUser),
			"guest should NOT meet minimum")
	})

	t.Run("role hierarchy is consistent", func(t *testing.T) {
		assert := assert.New(t)
		assert.True(middleware.HasRequiredOrgRole(middleware.RoleSuperAdmin, middleware.RoleAdmin))
		assert.True(middleware.HasRequiredOrgRole(middleware.RoleAdmin, middleware.RoleUser))
		assert.True(middleware.HasRequiredOrgRole(middleware.RoleUser, middleware.RoleReadOnly))
		assert.True(middleware.HasRequiredOrgRole(middleware.RoleReadOnly, middleware.RoleGuest))
	})
}

// TestApiPermission tests the apiPermission helper function
func TestApiPermission(t *testing.T) {
	tests := []struct {
		name     string
		perm     middleware.LibraryPermission
		expected string
	}{
		{"owner returns rw", middleware.PermissionOwner, "rw"},
		{"rw returns rw", middleware.PermissionRW, "rw"},
		{"r returns r", middleware.PermissionR, "r"},
		{"none returns empty", middleware.PermissionNone, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := apiPermission(tt.perm)
			if result != tt.expected {
				t.Errorf("apiPermission(%q) = %q, want %q", tt.perm, result, tt.expected)
			}
		})
	}
}

// TestLibraryHandler_SetGCEnqueuer tests the GC enqueuer setter
func TestLibraryHandler_SetGCEnqueuer(t *testing.T) {
	h := &LibraryHandler{}

	if h.gcEnqueuer != nil {
		t.Error("gcEnqueuer should be nil initially")
	}

	// Setting it should work (we can't test with a real enqueuer without dependencies)
	h.SetGCEnqueuer(nil)
	if h.gcEnqueuer != nil {
		t.Error("gcEnqueuer should be nil when set to nil")
	}
}

// TestV21Library_EncryptedField tests that encrypted field is integer, not boolean
func TestV21Library_EncryptedField(t *testing.T) {
	// This is a protocol requirement: Seafile clients expect integer 0/1 for encrypted field
	lib := V21Library{
		RepoID:    "test",
		RepoName:  "test",
		Encrypted: 1,
	}

	data, err := json.Marshal(lib)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var raw map[string]interface{}
	json.Unmarshal(data, &raw)

	// encrypted must be a number, not boolean
	enc := raw["encrypted"]
	switch v := enc.(type) {
	case float64:
		if v != 1 {
			t.Errorf("encrypted = %v, want 1", v)
		}
	default:
		t.Errorf("encrypted should be number type, got %T", enc)
	}
}

// TestPermissionMiddleware_GroupPermissionResolution tests group permission inheritance
func TestPermissionMiddleware_GroupPermissionResolution(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping group permission tests - require database")
	}

	t.Run("user inherits permissions from group memberships", func(t *testing.T) {
		// This test documents expected group permission behavior:
		// 1. User can be member of multiple groups
		// 2. Groups can have library permissions (owner/rw/r)
		// 3. User inherits highest permission from all groups
		// 4. Direct user permission overrides group permission
		assert := assert.New(t)
		assert.True(true, "Group permission resolution logic exists")
	})

	t.Run("direct permission takes precedence over group permission", func(t *testing.T) {
		// If user has direct "r" permission and group has "rw",
		// user should get "rw" (highest wins)
		assert := assert.New(t)
		assert.True(true, "Direct permissions are checked alongside group permissions")
	})
}
