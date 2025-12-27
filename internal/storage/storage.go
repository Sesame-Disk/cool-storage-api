package storage

import (
	"context"
	"io"
	"time"
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
	Name        string            `yaml:"name"`
	Type        string            `yaml:"type"`         // s3, glacier, filesystem
	AccessType  AccessType        `yaml:"access_type"`  // hot or cold
	Endpoint    string            `yaml:"endpoint"`     // For S3-compatible
	Bucket      string            `yaml:"bucket"`       // S3 bucket
	Vault       string            `yaml:"vault"`        // Glacier vault
	Region      string            `yaml:"region"`       // AWS region
	Path        string            `yaml:"path"`         // Filesystem path
	Options     map[string]string `yaml:"options"`      // Additional options
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
	Name       string            `yaml:"name"`
	Priority   int               `yaml:"priority"`   // Lower = higher priority
	Conditions PolicyConditions  `yaml:"conditions"` // When this policy applies
	Backend    string            `yaml:"backend"`    // Which backend to use
}

// PolicyConditions defines when a storage policy applies
type PolicyConditions struct {
	// Current simple conditions
	TenantIDs    []string `yaml:"tenant_ids,omitempty"`    // Specific tenants
	FileTypes    []string `yaml:"file_types,omitempty"`    // File extensions
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
	StorageKey    string    `json:"storage_key"`
	Backend       string    `json:"backend"`
	Status        string    `json:"status"` // completed, failed
	CompletedAt   time.Time `json:"completed_at"`
	ExpiresAt     time.Time `json:"expires_at"` // When the restored copy expires
	Error         string    `json:"error,omitempty"`
}

// Manager manages multiple storage backends and policies
type Manager struct {
	backends map[string]Store
	policies []StoragePolicy
	lifecycle []LifecycleRule
}

// NewManager creates a new storage manager
func NewManager() *Manager {
	return &Manager{
		backends: make(map[string]Store),
		policies: []StoragePolicy{},
		lifecycle: []LifecycleRule{},
	}
}

// RegisterBackend registers a storage backend
func (m *Manager) RegisterBackend(name string, store Store) {
	m.backends[name] = store
}

// GetBackend returns a storage backend by name
func (m *Manager) GetBackend(name string) (Store, bool) {
	store, ok := m.backends[name]
	return store, ok
}

// SelectBackend chooses the appropriate backend based on policies
// For now, just returns the default. Future: evaluate conditions
func (m *Manager) SelectBackend(tenantID string, fileType string, size int64) string {
	// TODO: Implement policy evaluation
	// For now, return "hot" as default
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
