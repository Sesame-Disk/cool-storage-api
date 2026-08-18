package storage

import (
	"context"
	"fmt"
	"io"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
)

// AccessType defines whether storage is immediately accessible or requires retrieval
type AccessType string

const (
	// AccessImmediate means files are available immediately (S3, local disk, etc.)
	AccessImmediate AccessType = "hot"

	// AccessDelayed means files require a restore operation before access (Glacier, etc.)
	AccessDelayed AccessType = "cold"
)

// Backend represents a storage backend configuration
type Backend struct {
	Name       string            `yaml:"name"`
	Type       string            `yaml:"type"`        // s3, glacier, filesystem
	AccessType AccessType        `yaml:"access_type"` // hot or cold
	Endpoint   string            `yaml:"endpoint"`    // For S3-compatible
	Bucket     string            `yaml:"bucket"`      // S3 bucket
	Vault      string            `yaml:"vault"`       // Glacier vault
	Region     string            `yaml:"region"`      // AWS region
	Path       string            `yaml:"path"`        // Filesystem path
	Options    map[string]string `yaml:"options"`     // Additional options
}

// IsHot returns true if the backend provides immediate access
func (b *Backend) IsHot() bool {
	return b.AccessType == AccessImmediate
}

// IsCold returns true if the backend requires retrieval time
func (b *Backend) IsCold() bool {
	return b.AccessType == AccessDelayed
}

// Store defines the interface for storage backends
type Store interface {
	// Put stores a block and returns the storage key
	Put(ctx context.Context, blockID string, data io.Reader, size int64) (string, error)

	// Get retrieves a block by its storage key
	Get(ctx context.Context, storageKey string) (io.ReadCloser, error)

	// Delete removes a block
	Delete(ctx context.Context, storageKey string) error

	// Exists checks if a block exists
	Exists(ctx context.Context, storageKey string) (bool, error)

	// GetAccessType returns whether this is hot or cold storage
	GetAccessType() AccessType

	// For cold storage only:

	// InitiateRestore starts a restore operation for a cold storage object
	// Returns a job ID that can be used to check status
	InitiateRestore(ctx context.Context, storageKey string) (string, error)

	// CheckRestoreStatus checks if a restore operation is complete
	// Returns true if the object is now accessible
	CheckRestoreStatus(ctx context.Context, storageKey string) (bool, error)

	// GetRestoreExpiry returns when a restored object will expire
	GetRestoreExpiry(ctx context.Context, storageKey string) (*time.Time, error)
}

// StoragePolicy defines rules for selecting storage backends
type StoragePolicy struct {
	Name       string           `yaml:"name"`
	Priority   int              `yaml:"priority"`   // Lower = higher priority
	Conditions PolicyConditions `yaml:"conditions"` // When this policy applies
	Backend    string           `yaml:"backend"`    // Which backend to use
}

// PolicyConditions defines when a storage policy applies
type PolicyConditions struct {
	// Current simple conditions
	TenantIDs    []string `yaml:"tenant_ids,omitempty"`     // Specific tenants
	FileTypes    []string `yaml:"file_types,omitempty"`     // File extensions
	MinSizeBytes int64    `yaml:"min_size_bytes,omitempty"` // Minimum file size
	MaxSizeBytes int64    `yaml:"max_size_bytes,omitempty"` // Maximum file size

	// Future conditions (for flexibility)
	// Regions      []string `yaml:"regions,omitempty"`       // Geographic regions
	// IPRanges     []string `yaml:"ip_ranges,omitempty"`     // IP address ranges
	// TimeRanges   []string `yaml:"time_ranges,omitempty"`   // Time-based rules
	// Custom       map[string]interface{} `yaml:"custom,omitempty"`
}

// LifecycleRule defines when to transition blocks between storage classes
type LifecycleRule struct {
	Name         string `yaml:"name"`
	FromBackend  string `yaml:"from_backend"`
	ToBackend    string `yaml:"to_backend"`
	AgeDays      int    `yaml:"age_days"`       // Days since last access
	MinSizeBytes int64  `yaml:"min_size_bytes"` // Only transition files larger than this
}

// RestoreNotification represents a notification that a cold storage restore is complete
type RestoreNotification struct {
	StorageKey  string    `json:"storage_key"`
	Backend     string    `json:"backend"`
	Status      string    `json:"status"` // completed, failed
	CompletedAt time.Time `json:"completed_at"`
	ExpiresAt   time.Time `json:"expires_at"` // When the restored copy expires
	Error       string    `json:"error,omitempty"`
}

// HealthStatus represents the health state of a storage backend
type HealthStatus int

const (
	HealthUnknown HealthStatus = iota
	HealthHealthy
	HealthDegraded  // Slow responses but working
	HealthUnhealthy // Primary down, using failover
	HealthFailed    // All endpoints down
)

func (h HealthStatus) String() string {
	switch h {
	case HealthHealthy:
		return "healthy"
	case HealthDegraded:
		return "degraded"
	case HealthUnhealthy:
		return "unhealthy"
	case HealthFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// BackendHealth tracks the health status of a storage backend
type BackendHealth struct {
	Status           HealthStatus
	LastCheck        time.Time
	LastError        error
	ConsecutiveFails int
	FailoverClass    string // Fallback class if this one is down
}

// blockStoreKey caches an org-scoped BlockStore by (org, storage class). A struct
// key (never a concatenated string) so distinct (org, class) pairs can never collide.
type blockStoreKey struct {
	orgID string
	class string
}

// Manager manages multiple storage backends and policies
type Manager struct {
	backends map[string]Store
	// blockStoresByOrg caches the org-scoped BlockStore per (org, class).
	blockStoresByOrg map[blockStoreKey]*BlockStore
	blockStoresMu    sync.RWMutex
	health           map[string]*BackendHealth
	healthMu         sync.RWMutex
	policies         []StoragePolicy
	lifecycle        []LifecycleRule
	defaultClass     string
	localRegion      string
	endpointRegions  map[string]string            // hostname → region
	regionClasses    map[string]RegionClassConfig // region → {hot, cold}
}

// RegionClassConfig maps a region to its hot and cold storage classes
type RegionClassConfig struct {
	Hot  string
	Cold string
}

// NewManager creates a new storage manager
func NewManager() *Manager {
	return &Manager{
		backends:         make(map[string]Store),
		blockStoresByOrg: make(map[blockStoreKey]*BlockStore),
		health:           make(map[string]*BackendHealth),
		policies:         []StoragePolicy{},
		lifecycle:        []LifecycleRule{},
		endpointRegions:  make(map[string]string),
		regionClasses:    make(map[string]RegionClassConfig),
	}
}

// SetDefaultClass sets the default storage class
func (m *Manager) SetDefaultClass(class string) {
	m.defaultClass = class
}

// SetEndpointRegions sets the hostname to region mapping
func (m *Manager) SetEndpointRegions(mapping map[string]string) {
	m.endpointRegions = mapping
}

// SetRegionClasses sets the region to storage class mapping
func (m *Manager) SetRegionClasses(mapping map[string]RegionClassConfig) {
	m.regionClasses = mapping
}

// SetLocalRegion sets the node-local default region for shared hostnames.
func (m *Manager) SetLocalRegion(region string) {
	m.localRegion = strings.ToLower(strings.TrimSpace(region))
}

// RegisterBackend registers a storage backend with optional failover
func (m *Manager) RegisterBackend(name string, store Store, failoverClass string) {
	m.backends[name] = store
	m.healthMu.Lock()
	m.health[name] = &BackendHealth{
		Status:        HealthUnknown,
		FailoverClass: failoverClass,
	}
	m.healthMu.Unlock()
}

// GetBackend returns a storage backend by name
func (m *Manager) GetBackend(name string) (Store, bool) {
	store, ok := m.backends[name]
	return store, ok
}

// ResolvePhysicalStorageClass validates the canonical class that names a
// physical namespace. R23a deliberately uses storage_class itself as B, so
// this resolver never follows failover or rewrites the label. Callers that
// selected a backend through GetHealthyBackend must pass its returned class.
func (m *Manager) ResolvePhysicalStorageClass(className string) (string, error) {
	if className == "" {
		return "", fmt.Errorf("storage class is empty")
	}
	// Same canon as configuration validation, from one definition, so a name
	// cannot certify at one layer and fail at the other. This resolver also
	// vets classes read back from persisted rows, not just configured ones.
	if !config.IsCanonicalStorageClassName(className) {
		return "", fmt.Errorf("storage class %q is not canonical", className)
	}
	if _, ok := m.backends[className]; !ok {
		return "", fmt.Errorf("storage class %s not found", className)
	}
	return className, nil
}

// GetHealthyBackend returns a healthy backend for the given class, with failover
func (m *Manager) GetHealthyBackend(preferredClass string) (Store, string, error) {
	// Fall back to default class if empty
	if preferredClass == "" && m.defaultClass != "" {
		preferredClass = m.defaultClass
	}

	// Failover configurations commonly form a cycle so either region can be
	// primary. Walk iteratively with a visited set: when every member of that
	// cycle is unhealthy, return an error instead of exhausting the goroutine
	// stack while trying the same classes forever.
	visited := make(map[string]struct{})
	for {
		if _, seen := visited[preferredClass]; seen {
			return nil, "", fmt.Errorf("storage failover cycle detected at class %s", preferredClass)
		}
		visited[preferredClass] = struct{}{}

		// Try preferred class first
		store, ok := m.backends[preferredClass]
		if !ok {
			break
		}
		m.healthMu.RLock()
		health := m.health[preferredClass]
		m.healthMu.RUnlock()

		if health == nil || health.Status == HealthHealthy || health.Status == HealthUnknown || health.Status == HealthDegraded {
			return store, preferredClass, nil
		}

		// Preferred is unhealthy, try failover
		if health.FailoverClass != "" {
			log.Printf("Storage class %s is %s, trying failover to %s",
				preferredClass, health.Status, health.FailoverClass)
			preferredClass = health.FailoverClass
			continue
		}
		break
	}

	// No backend found
	return nil, "", fmt.Errorf("no healthy backend available for class %s", preferredClass)
}

// ResolveStorageClass determines the storage class based on context
// Priority: library override > endpoint region > default
func (m *Manager) ResolveStorageClass(hostname string, libraryClass string, tier string) string {
	// 1. Library override takes precedence
	if libraryClass != "" {
		if _, ok := m.backends[libraryClass]; ok {
			return libraryClass
		}
		log.Printf("Warning: library storage class %s not found, falling back", libraryClass)
	}

	// 2. Resolve region from hostname
	region := m.resolveRegion(hostname)

	// 3. Get class for region and tier
	if regionConfig, ok := m.regionClasses[region]; ok {
		if tier == "cold" && regionConfig.Cold != "" {
			return regionConfig.Cold
		}
		if regionConfig.Hot != "" {
			return regionConfig.Hot
		}
	}

	// 4. Fallback to default
	if m.defaultClass != "" {
		return m.defaultClass
	}

	// 5. Last resort: return first available backend
	for name := range m.backends {
		return name
	}

	return ""
}

// ResolveEndpointRegion resolves a request hostname to a configured region.
// Priority: exact hostname > wildcard hostname > local region fallback > global default.
func ResolveEndpointRegion(hostname string, endpointRegions map[string]string, localRegion string) string {
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	localRegion = strings.ToLower(strings.TrimSpace(localRegion))

	for pattern, region := range endpointRegions {
		if strings.EqualFold(strings.TrimSpace(pattern), hostname) {
			return strings.ToLower(strings.TrimSpace(region))
		}
	}

	if hostname != "" {
		type wildcardRegion struct {
			pattern string
			region  string
		}
		wildcards := make([]wildcardRegion, 0, len(endpointRegions))
		for pattern, region := range endpointRegions {
			trimmedPattern := strings.TrimSpace(pattern)
			if len(trimmedPattern) > 1 && trimmedPattern[0] == '*' {
				wildcards = append(wildcards, wildcardRegion{
					pattern: trimmedPattern,
					region:  strings.ToLower(strings.TrimSpace(region)),
				})
			}
		}
		sort.Slice(wildcards, func(i, j int) bool {
			if len(wildcards[i].pattern) == len(wildcards[j].pattern) {
				return wildcards[i].pattern < wildcards[j].pattern
			}
			return len(wildcards[i].pattern) > len(wildcards[j].pattern)
		})

		for _, wildcard := range wildcards {
			suffix := strings.ToLower(wildcard.pattern[1:])
			if len(hostname) > len(suffix) && strings.HasSuffix(hostname, suffix) {
				return wildcard.region
			}
		}
	}

	if localRegion != "" {
		return localRegion
	}

	for pattern, region := range endpointRegions {
		if strings.TrimSpace(pattern) == "*" {
			return strings.ToLower(strings.TrimSpace(region))
		}
	}

	return "default"
}

// resolveRegion maps a hostname to a region
func (m *Manager) resolveRegion(hostname string) string {
	return ResolveEndpointRegion(hostname, m.endpointRegions, m.localRegion)
}

// UpdateHealth updates the health status of a backend
func (m *Manager) UpdateHealth(name string, status HealthStatus, err error) {
	m.healthMu.Lock()
	defer m.healthMu.Unlock()

	if health, ok := m.health[name]; ok {
		health.Status = status
		health.LastCheck = time.Now()
		health.LastError = err
		if status == HealthHealthy {
			health.ConsecutiveFails = 0
		} else if status == HealthFailed || status == HealthUnhealthy {
			health.ConsecutiveFails++
		}
	}
}

// CheckHealth performs a health check on a backend
func (m *Manager) CheckHealth(ctx context.Context, name string) HealthStatus {
	store, ok := m.backends[name]
	if !ok {
		return HealthFailed
	}

	// Simple health check: try to check if a known key exists
	// This verifies connectivity without storing/retrieving data
	start := time.Now()
	_, err := store.Exists(ctx, "__health_check__")
	elapsed := time.Since(start)

	var status HealthStatus
	if err != nil {
		// Check if it's a "not found" error (which is OK) vs connection error
		errStr := err.Error()
		if contains(errStr, "NotFound") || contains(errStr, "NoSuchKey") || contains(errStr, "404") {
			// Key not found is OK - backend is healthy
			status = HealthHealthy
			err = nil
		} else {
			status = HealthUnhealthy
		}
	} else if elapsed > 5*time.Second {
		status = HealthDegraded
	} else {
		status = HealthHealthy
	}

	m.UpdateHealth(name, status, err)
	return status
}

// CheckAllHealth performs health checks on all backends
func (m *Manager) CheckAllHealth(ctx context.Context) map[string]HealthStatus {
	results := make(map[string]HealthStatus)
	for name := range m.backends {
		results[name] = m.CheckHealth(ctx, name)
	}
	return results
}

// GetHealth returns the current health status of a backend
func (m *Manager) GetHealth(name string) *BackendHealth {
	m.healthMu.RLock()
	defer m.healthMu.RUnlock()
	if health, ok := m.health[name]; ok {
		return health
	}
	return nil
}

// ListBackends returns all registered backend names
func (m *Manager) ListBackends() []string {
	var names []string
	for name := range m.backends {
		names = append(names, name)
	}
	return names
}

// SelectBackend chooses the appropriate backend based on policies
// For now, just returns the default. Future: evaluate conditions
func (m *Manager) SelectBackend(tenantID string, fileType string, size int64) string {
	// TODO: Implement policy evaluation
	// For now, return default
	if m.defaultClass != "" {
		return m.defaultClass
	}
	return "hot"
}

// GetHotBackends returns all backends with immediate access
func (m *Manager) GetHotBackends() []string {
	var result []string
	for name, store := range m.backends {
		if store.GetAccessType() == AccessImmediate {
			result = append(result, name)
		}
	}
	return result
}

// GetColdBackends returns all backends that require retrieval
func (m *Manager) GetColdBackends() []string {
	var result []string
	for name, store := range m.backends {
		if store.GetAccessType() == AccessDelayed {
			result = append(result, name)
		}
	}
	return result
}

// GetBlockStoreForOrg returns an org-scoped BlockStore for the given org and
// storage class. The returned store writes/reads/deletes at org-scoped keys
// (blocks/<org_id>/...), so one org's GC can never touch another org's object.
// Fails closed on an empty/invalid org id. Stores are cached and reused per
// canonical (org, class).
func (m *Manager) GetBlockStoreForOrg(orgID, className string) (*BlockStore, error) {
	normalizedOrgID, err := normalizeOrgID(orgID)
	if err != nil {
		return nil, err
	}
	physicalClass, err := m.ResolvePhysicalStorageClass(className)
	if err != nil {
		return nil, err
	}
	key := blockStoreKey{orgID: normalizedOrgID, class: physicalClass}

	// Check cache first.
	m.blockStoresMu.RLock()
	if bs, ok := m.blockStoresByOrg[key]; ok {
		m.blockStoresMu.RUnlock()
		return bs, nil
	}
	m.blockStoresMu.RUnlock()

	store, ok := m.backends[physicalClass]
	if !ok {
		return nil, fmt.Errorf("storage class %s not found", physicalClass)
	}

	// Cast to S3Store (BlockStore requires S3Store)
	s3Store, ok := store.(*S3Store)
	if !ok {
		return nil, fmt.Errorf("storage class %s is not an S3 backend", physicalClass)
	}

	bs, err := NewOrgBlockStore(s3Store, "blocks/", normalizedOrgID)
	if err != nil {
		return nil, err
	}

	m.blockStoresMu.Lock()
	defer m.blockStoresMu.Unlock()
	if existing, ok := m.blockStoresByOrg[key]; ok {
		return existing, nil
	}

	m.blockStoresByOrg[key] = bs
	return bs, nil
}

// GetHealthyBlockStoreForOrg returns an org-scoped BlockStore for a healthy
// backend with failover, mirroring GetHealthyBlockStore. Fails closed on an
// empty/invalid org id and caches by canonical (org, class).
func (m *Manager) GetHealthyBlockStoreForOrg(orgID, preferredClass string) (*BlockStore, string, error) {
	normalizedOrgID, err := normalizeOrgID(orgID)
	if err != nil {
		return nil, "", err
	}

	store, actualClass, err := m.GetHealthyBackend(preferredClass)
	if err != nil {
		return nil, "", err
	}
	physicalClass, err := m.ResolvePhysicalStorageClass(actualClass)
	if err != nil {
		return nil, "", err
	}

	// Cast to S3Store
	s3Store, ok := store.(*S3Store)
	if !ok {
		return nil, "", fmt.Errorf("storage class %s is not an S3 backend", physicalClass)
	}

	key := blockStoreKey{orgID: normalizedOrgID, class: physicalClass}

	m.blockStoresMu.RLock()
	if bs, ok := m.blockStoresByOrg[key]; ok {
		m.blockStoresMu.RUnlock()
		return bs, physicalClass, nil
	}
	m.blockStoresMu.RUnlock()

	bs, err := NewOrgBlockStore(s3Store, "blocks/", normalizedOrgID)
	if err != nil {
		return nil, "", err
	}

	m.blockStoresMu.Lock()
	defer m.blockStoresMu.Unlock()
	if existing, ok := m.blockStoresByOrg[key]; ok {
		return existing, physicalClass, nil
	}

	m.blockStoresByOrg[key] = bs
	return bs, physicalClass, nil
}
