package api

import (
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/gin-gonic/gin"
)

// HostnameMapping represents a hostname to org_id mapping
type HostnameMapping struct {
	Hostname  string            `json:"hostname"`
	OrgID     string            `json:"org_id"`
	Settings  map[string]string `json:"settings,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// HostnameResolver resolves hostnames to organization IDs
// It caches mappings for performance with automatic refresh
type HostnameResolver struct {
	db           *db.DB
	cache        map[string]*HostnameMapping
	cacheMu      sync.RWMutex
	cacheTTL     time.Duration
	lastRefresh  time.Time
	defaultOrgID string // Fallback org for unmapped hosts
}

// NewHostnameResolver creates a new hostname resolver
func NewHostnameResolver(database *db.DB, defaultOrgID string, cacheTTL time.Duration) *HostnameResolver {
	if cacheTTL <= 0 {
		cacheTTL = 5 * time.Minute
	}

	hr := &HostnameResolver{
		db:           database,
		cache:        make(map[string]*HostnameMapping),
		cacheTTL:     cacheTTL,
		defaultOrgID: defaultOrgID,
	}

	// Initial cache load
	hr.refreshCache()

	// Background cache refresh
	go hr.backgroundRefresh()

	return hr
}

// Resolve resolves a hostname to an org_id
func (hr *HostnameResolver) Resolve(hostname string) (string, *HostnameMapping, bool) {
	// Normalize hostname (lowercase, remove port)
	hostname = normalizeHostname(hostname)

	hr.cacheMu.RLock()
	mapping, found := hr.cache[hostname]
	hr.cacheMu.RUnlock()

	if found {
		return mapping.OrgID, mapping, true
	}

	// Try wildcard matching (e.g., *.example.com)
	parts := strings.Split(hostname, ".")
	for i := 1; i < len(parts); i++ {
		wildcard := "*." + strings.Join(parts[i:], ".")
		hr.cacheMu.RLock()
		mapping, found = hr.cache[wildcard]
		hr.cacheMu.RUnlock()
		if found {
			return mapping.OrgID, mapping, true
		}
	}

	// Fallback to default org
	if hr.defaultOrgID != "" {
		return hr.defaultOrgID, nil, false
	}

	return "", nil, false
}

// refreshCache loads all hostname mappings from the database
func (hr *HostnameResolver) refreshCache() {
	if hr.db == nil {
		return
	}

	iter := hr.db.Session().Query(`
		SELECT hostname, org_id, settings, created_at, updated_at
		FROM hostname_mappings
	`).Iter()

	newCache := make(map[string]*HostnameMapping)
	var hostname, orgID string
	var settings map[string]string
	var createdAt, updatedAt time.Time

	for iter.Scan(&hostname, &orgID, &settings, &createdAt, &updatedAt) {
		newCache[hostname] = &HostnameMapping{
			Hostname:  hostname,
			OrgID:     orgID,
			Settings:  settings,
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		}
	}

	if err := iter.Close(); err != nil {
		log.Printf("Warning: Failed to refresh hostname cache: %v", err)
		return
	}

	hr.cacheMu.Lock()
	hr.cache = newCache
	hr.lastRefresh = time.Now()
	hr.cacheMu.Unlock()

	log.Printf("Hostname cache refreshed: %d mappings loaded", len(newCache))
}

// backgroundRefresh periodically refreshes the cache
func (hr *HostnameResolver) backgroundRefresh() {
	ticker := time.NewTicker(hr.cacheTTL)
	for range ticker.C {
		hr.refreshCache()
	}
}

// AddMapping adds a new hostname mapping
func (hr *HostnameResolver) AddMapping(hostname, orgID string, settings map[string]string) error {
	hostname = normalizeHostname(hostname)
	now := time.Now()

	err := hr.db.Session().Query(`
		INSERT INTO hostname_mappings (hostname, org_id, settings, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
	`, hostname, orgID, settings, now, now).Exec()

	if err != nil {
		return err
	}

	// Update cache immediately
	hr.cacheMu.Lock()
	hr.cache[hostname] = &HostnameMapping{
		Hostname:  hostname,
		OrgID:     orgID,
		Settings:  settings,
		CreatedAt: now,
		UpdatedAt: now,
	}
	hr.cacheMu.Unlock()

	return nil
}

// RemoveMapping removes a hostname mapping
func (hr *HostnameResolver) RemoveMapping(hostname string) error {
	hostname = normalizeHostname(hostname)

	err := hr.db.Session().Query(`
		DELETE FROM hostname_mappings WHERE hostname = ?
	`, hostname).Exec()

	if err != nil {
		return err
	}

	// Update cache immediately
	hr.cacheMu.Lock()
	delete(hr.cache, hostname)
	hr.cacheMu.Unlock()

	return nil
}

// ListMappings returns all hostname mappings
func (hr *HostnameResolver) ListMappings() []*HostnameMapping {
	hr.cacheMu.RLock()
	defer hr.cacheMu.RUnlock()

	mappings := make([]*HostnameMapping, 0, len(hr.cache))
	for _, m := range hr.cache {
		mappings = append(mappings, m)
	}
	return mappings
}

// normalizeHostname normalizes a hostname for comparison
func normalizeHostname(hostname string) string {
	// Lowercase
	hostname = strings.ToLower(hostname)

	// Remove port if present
	if idx := strings.LastIndex(hostname, ":"); idx != -1 {
		hostname = hostname[:idx]
	}

	// Remove trailing dot (FQDN format)
	hostname = strings.TrimSuffix(hostname, ".")

	return hostname
}

// HostnameMiddleware creates a Gin middleware that resolves the hostname
// and sets the org_id in the request context
func HostnameMiddleware(resolver *HostnameResolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Get hostname from request
		hostname := c.Request.Host

		// Try X-Forwarded-Host header (for reverse proxies)
		if fwdHost := c.GetHeader("X-Forwarded-Host"); fwdHost != "" {
			hostname = fwdHost
		}

		// Resolve hostname to org_id
		orgID, mapping, found := resolver.Resolve(hostname)

		if !found && orgID == "" {
			// No mapping and no default org - reject the request
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "unknown hostname",
				"message": "This hostname is not configured for any organization",
			})
			c.Abort()
			return
		}

		// Set org_id in context (can be overridden by auth middleware for specific users)
		c.Set("hostname_org_id", orgID)
		c.Set("hostname_mapping", mapping)
		c.Set("resolved_hostname", normalizeHostname(hostname))

		c.Next()
	}
}

// RequireHostnameOrg is a middleware that ensures org_id from hostname is used
// This is for endpoints that should respect multi-tenant hostname routing
func RequireHostnameOrg() gin.HandlerFunc {
	return func(c *gin.Context) {
		hostnameOrgID := c.GetString("hostname_org_id")

		// If user is authenticated, check that their org matches the hostname org
		userOrgID := c.GetString("org_id")
		if userOrgID != "" && hostnameOrgID != "" && userOrgID != hostnameOrgID {
			c.JSON(http.StatusForbidden, gin.H{
				"error": "organization mismatch",
				"message": "Your account belongs to a different organization than this hostname",
			})
			c.Abort()
			return
		}

		// If no org_id set yet, use the hostname org
		if userOrgID == "" && hostnameOrgID != "" {
			c.Set("org_id", hostnameOrgID)
		}

		c.Next()
	}
}
