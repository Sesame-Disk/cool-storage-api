package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// TestNormalizeHostname tests hostname normalization
func TestNormalizeHostname(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"example.com", "example.com"},
		{"EXAMPLE.COM", "example.com"},
		{"Example.Com", "example.com"},
		{"example.com:8080", "example.com"},
		{"EXAMPLE.COM:443", "example.com"},
		{"example.com.", "example.com"},
		{"example.com.:8080", "example.com"},
		{"sub.example.com", "sub.example.com"},
		{"SUB.EXAMPLE.COM:9000", "sub.example.com"},
		{"localhost", "localhost"},
		{"localhost:3000", "localhost"},
		{"192.168.1.1", "192.168.1.1"},
		{"192.168.1.1:8080", "192.168.1.1"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := normalizeHostname(tt.input)
			if result != tt.expected {
				t.Errorf("normalizeHostname(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestHostnameMapping tests the HostnameMapping struct
func TestHostnameMapping(t *testing.T) {
	now := time.Now()
	mapping := HostnameMapping{
		Hostname:  "files.example.com",
		OrgID:     "00000000-0000-0000-0000-000000000001",
		Settings:  map[string]string{"theme": "dark", "locale": "en"},
		CreatedAt: now,
		UpdatedAt: now,
	}

	if mapping.Hostname != "files.example.com" {
		t.Errorf("Hostname = %s, want files.example.com", mapping.Hostname)
	}
	if mapping.OrgID != "00000000-0000-0000-0000-000000000001" {
		t.Errorf("OrgID = %s, want 00000000-0000-0000-0000-000000000001", mapping.OrgID)
	}
	if mapping.Settings["theme"] != "dark" {
		t.Errorf("Settings[theme] = %s, want dark", mapping.Settings["theme"])
	}
}

// TestHostnameResolverWithDefaultOrg tests resolver with default org fallback
func TestHostnameResolverWithDefaultOrg(t *testing.T) {
	defaultOrgID := "default-org-123"
	resolver := &HostnameResolver{
		db:           nil,
		cache:        make(map[string]*HostnameMapping),
		defaultOrgID: defaultOrgID,
		cacheTTL:     5 * time.Minute,
	}

	// Unknown hostname should return default org
	orgID, mapping, found := resolver.Resolve("unknown.example.com")

	if orgID != defaultOrgID {
		t.Errorf("orgID = %s, want %s", orgID, defaultOrgID)
	}
	if mapping != nil {
		t.Error("mapping should be nil for unknown hostname")
	}
	if found {
		t.Error("found should be false for unknown hostname")
	}
}

// TestHostnameResolverWithCache tests resolver with cached mappings
func TestHostnameResolverWithCache(t *testing.T) {
	resolver := &HostnameResolver{
		db:           nil,
		cache:        make(map[string]*HostnameMapping),
		defaultOrgID: "",
		cacheTTL:     5 * time.Minute,
	}

	// Add mappings to cache
	resolver.cache["files.acme.com"] = &HostnameMapping{
		Hostname: "files.acme.com",
		OrgID:    "acme-org-123",
	}
	resolver.cache["storage.globex.io"] = &HostnameMapping{
		Hostname: "storage.globex.io",
		OrgID:    "globex-org-456",
	}

	tests := []struct {
		hostname    string
		expectedOrg string
		found       bool
	}{
		{"files.acme.com", "acme-org-123", true},
		{"FILES.ACME.COM", "acme-org-123", true}, // Case insensitive
		{"files.acme.com:8080", "acme-org-123", true}, // Port stripped
		{"storage.globex.io", "globex-org-456", true},
		{"unknown.com", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.hostname, func(t *testing.T) {
			orgID, _, found := resolver.Resolve(tt.hostname)
			if found != tt.found {
				t.Errorf("found = %v, want %v", found, tt.found)
			}
			if orgID != tt.expectedOrg {
				t.Errorf("orgID = %s, want %s", orgID, tt.expectedOrg)
			}
		})
	}
}

// TestHostnameResolverWildcard tests wildcard hostname matching
func TestHostnameResolverWildcard(t *testing.T) {
	resolver := &HostnameResolver{
		db:           nil,
		cache:        make(map[string]*HostnameMapping),
		defaultOrgID: "",
		cacheTTL:     5 * time.Minute,
	}

	// Add wildcard mapping
	resolver.cache["*.example.com"] = &HostnameMapping{
		Hostname: "*.example.com",
		OrgID:    "example-org",
	}

	tests := []struct {
		hostname    string
		expectedOrg string
		found       bool
	}{
		{"files.example.com", "example-org", true},
		{"storage.example.com", "example-org", true},
		{"api.example.com", "example-org", true},
		{"example.com", "", false}, // Wildcard doesn't match bare domain
		{"sub.sub.example.com", "example-org", true}, // Matches via *.example.com
		{"other.com", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.hostname, func(t *testing.T) {
			orgID, _, found := resolver.Resolve(tt.hostname)
			if found != tt.found {
				t.Errorf("found = %v, want %v", found, tt.found)
			}
			if orgID != tt.expectedOrg {
				t.Errorf("orgID = %s, want %s", orgID, tt.expectedOrg)
			}
		})
	}
}

// TestHostnameMiddleware tests the hostname resolution middleware
func TestHostnameMiddleware(t *testing.T) {
	resolver := &HostnameResolver{
		db:           nil,
		cache:        make(map[string]*HostnameMapping),
		defaultOrgID: "default-org",
		cacheTTL:     5 * time.Minute,
	}

	resolver.cache["files.acme.com"] = &HostnameMapping{
		Hostname: "files.acme.com",
		OrgID:    "acme-org",
		Settings: map[string]string{"feature_x": "enabled"},
	}

	r := gin.New()
	r.Use(HostnameMiddleware(resolver))
	r.GET("/test", func(c *gin.Context) {
		orgID := c.GetString("hostname_org_id")
		hostname := c.GetString("resolved_hostname")
		c.JSON(http.StatusOK, gin.H{
			"org_id":   orgID,
			"hostname": hostname,
		})
	})

	tests := []struct {
		name       string
		host       string
		wantStatus int
		wantOrg    string
	}{
		{
			name:       "known hostname",
			host:       "files.acme.com",
			wantStatus: http.StatusOK,
			wantOrg:    "acme-org",
		},
		{
			name:       "unknown hostname with default",
			host:       "unknown.example.com",
			wantStatus: http.StatusOK,
			wantOrg:    "default-org",
		},
		{
			name:       "known hostname with port",
			host:       "files.acme.com:8080",
			wantStatus: http.StatusOK,
			wantOrg:    "acme-org",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "/test", nil)
			req.Host = tt.host

			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

// TestHostnameMiddlewareNoDefault tests middleware without default org
func TestHostnameMiddlewareNoDefault(t *testing.T) {
	resolver := &HostnameResolver{
		db:           nil,
		cache:        make(map[string]*HostnameMapping),
		defaultOrgID: "", // No default
		cacheTTL:     5 * time.Minute,
	}

	r := gin.New()
	r.Use(HostnameMiddleware(resolver))
	r.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "OK")
	})

	req, _ := http.NewRequest("GET", "/test", nil)
	req.Host = "unknown.example.com"

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Should return 404 for unknown hostname without default
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

// TestHostnameMiddlewareXForwardedHost tests X-Forwarded-Host header handling
func TestHostnameMiddlewareXForwardedHost(t *testing.T) {
	resolver := &HostnameResolver{
		db:           nil,
		cache:        make(map[string]*HostnameMapping),
		defaultOrgID: "",
		cacheTTL:     5 * time.Minute,
	}

	resolver.cache["frontend.example.com"] = &HostnameMapping{
		Hostname: "frontend.example.com",
		OrgID:    "frontend-org",
	}

	r := gin.New()
	r.Use(HostnameMiddleware(resolver))
	r.GET("/test", func(c *gin.Context) {
		orgID := c.GetString("hostname_org_id")
		c.String(http.StatusOK, orgID)
	})

	req, _ := http.NewRequest("GET", "/test", nil)
	req.Host = "backend-internal:8080" // Internal hostname
	req.Header.Set("X-Forwarded-Host", "frontend.example.com") // Real frontend

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if w.Body.String() != "frontend-org" {
		t.Errorf("org_id = %s, want frontend-org", w.Body.String())
	}
}

// TestRequireHostnameOrgMiddleware tests the org requirement middleware
func TestRequireHostnameOrgMiddleware(t *testing.T) {
	resolver := &HostnameResolver{
		db:           nil,
		cache:        make(map[string]*HostnameMapping),
		defaultOrgID: "",
		cacheTTL:     5 * time.Minute,
	}

	resolver.cache["acme.example.com"] = &HostnameMapping{
		Hostname: "acme.example.com",
		OrgID:    "acme-org",
	}

	r := gin.New()
	r.Use(HostnameMiddleware(resolver))
	r.Use(RequireHostnameOrg())
	r.GET("/test", func(c *gin.Context) {
		orgID := c.GetString("org_id")
		c.String(http.StatusOK, orgID)
	})

	tests := []struct {
		name       string
		host       string
		userOrgID  string // Simulated user org from auth
		wantStatus int
	}{
		{
			name:       "matching org",
			host:       "acme.example.com",
			userOrgID:  "acme-org",
			wantStatus: http.StatusOK,
		},
		{
			name:       "no user org - uses hostname org",
			host:       "acme.example.com",
			userOrgID:  "",
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create router with simulated auth
			r := gin.New()
			r.Use(HostnameMiddleware(resolver))
			r.Use(func(c *gin.Context) {
				if tt.userOrgID != "" {
					c.Set("org_id", tt.userOrgID)
				}
				c.Next()
			})
			r.Use(RequireHostnameOrg())
			r.GET("/test", func(c *gin.Context) {
				c.String(http.StatusOK, "OK")
			})

			req, _ := http.NewRequest("GET", "/test", nil)
			req.Host = tt.host

			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d, body: %s", w.Code, tt.wantStatus, w.Body.String())
			}
		})
	}
}

// TestRequireHostnameOrgMismatch tests org mismatch rejection
func TestRequireHostnameOrgMismatch(t *testing.T) {
	resolver := &HostnameResolver{
		db:           nil,
		cache:        make(map[string]*HostnameMapping),
		defaultOrgID: "",
		cacheTTL:     5 * time.Minute,
	}

	resolver.cache["acme.example.com"] = &HostnameMapping{
		Hostname: "acme.example.com",
		OrgID:    "acme-org",
	}

	r := gin.New()
	r.Use(HostnameMiddleware(resolver))
	r.Use(func(c *gin.Context) {
		// Simulate user from different org
		c.Set("org_id", "different-org")
		c.Next()
	})
	r.Use(RequireHostnameOrg())
	r.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "OK")
	})

	req, _ := http.NewRequest("GET", "/test", nil)
	req.Host = "acme.example.com"

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Should reject due to org mismatch
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

// TestHostnameResolverListMappings tests listing all mappings
func TestHostnameResolverListMappings(t *testing.T) {
	resolver := &HostnameResolver{
		db:           nil,
		cache:        make(map[string]*HostnameMapping),
		defaultOrgID: "",
		cacheTTL:     5 * time.Minute,
	}

	resolver.cache["a.example.com"] = &HostnameMapping{Hostname: "a.example.com", OrgID: "org-a"}
	resolver.cache["b.example.com"] = &HostnameMapping{Hostname: "b.example.com", OrgID: "org-b"}
	resolver.cache["c.example.com"] = &HostnameMapping{Hostname: "c.example.com", OrgID: "org-c"}

	mappings := resolver.ListMappings()

	if len(mappings) != 3 {
		t.Errorf("got %d mappings, want 3", len(mappings))
	}

	// Check all expected mappings are present
	found := make(map[string]bool)
	for _, m := range mappings {
		found[m.Hostname] = true
	}

	for _, expected := range []string{"a.example.com", "b.example.com", "c.example.com"} {
		if !found[expected] {
			t.Errorf("missing mapping for %s", expected)
		}
	}
}
