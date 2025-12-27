package storage

import (
	"context"
	"io"
	"testing"
	"time"
)

// mockStore implements Store interface for testing
type mockStore struct {
	accessType AccessType
	healthy    bool
}

func (m *mockStore) Put(ctx context.Context, blockID string, data io.Reader, size int64) (string, error) {
	return blockID, nil
}

func (m *mockStore) Get(ctx context.Context, storageKey string) (io.ReadCloser, error) {
	return nil, nil
}

func (m *mockStore) Delete(ctx context.Context, storageKey string) error {
	return nil
}

func (m *mockStore) Exists(ctx context.Context, storageKey string) (bool, error) {
	if !m.healthy {
		return false, io.ErrUnexpectedEOF // Simulate connection error
	}
	return false, nil // Not found is OK
}

func (m *mockStore) GetAccessType() AccessType {
	return m.accessType
}

func (m *mockStore) InitiateRestore(ctx context.Context, storageKey string) (string, error) {
	return "", nil
}

func (m *mockStore) CheckRestoreStatus(ctx context.Context, storageKey string) (bool, error) {
	return true, nil
}

func (m *mockStore) GetRestoreExpiry(ctx context.Context, storageKey string) (*time.Time, error) {
	return nil, nil
}

func TestManagerRegisterBackend(t *testing.T) {
	m := NewManager()
	store := &mockStore{accessType: AccessImmediate, healthy: true}

	m.RegisterBackend("hot-s3-usa", store, "hot-s3-eu")

	// Verify backend was registered
	got, ok := m.GetBackend("hot-s3-usa")
	if !ok {
		t.Fatal("backend not found after registration")
	}
	if got != store {
		t.Error("returned store doesn't match registered store")
	}

	// Verify health was initialized
	health := m.GetHealth("hot-s3-usa")
	if health == nil {
		t.Fatal("health not initialized")
	}
	if health.FailoverClass != "hot-s3-eu" {
		t.Errorf("failover class = %s, want hot-s3-eu", health.FailoverClass)
	}
}

func TestManagerResolveStorageClass(t *testing.T) {
	m := NewManager()

	// Register backends
	m.RegisterBackend("hot-s3-usa", &mockStore{accessType: AccessImmediate}, "")
	m.RegisterBackend("hot-s3-eu", &mockStore{accessType: AccessImmediate}, "")
	m.RegisterBackend("hot-s3-china", &mockStore{accessType: AccessImmediate}, "")
	m.RegisterBackend("cold-glacier-usa", &mockStore{accessType: AccessDelayed}, "")

	// Configure endpoint regions
	m.SetEndpointRegions(map[string]string{
		"us.sesamefs.com":    "usa",
		"eu.sesamefs.com":    "eu",
		"cn.sesamefs.com":    "china",
		"*.sesamefs.com":     "usa", // Default
		"localhost":          "usa",
	})

	// Configure region classes
	m.SetRegionClasses(map[string]RegionClassConfig{
		"usa": {Hot: "hot-s3-usa", Cold: "cold-glacier-usa"},
		"eu":  {Hot: "hot-s3-eu", Cold: ""},
		"china": {Hot: "hot-s3-china", Cold: ""},
	})

	m.SetDefaultClass("hot-s3-usa")

	tests := []struct {
		name         string
		hostname     string
		libraryClass string
		tier         string
		want         string
	}{
		{
			name:     "USA endpoint hot",
			hostname: "us.sesamefs.com",
			tier:     "hot",
			want:     "hot-s3-usa",
		},
		{
			name:     "EU endpoint hot",
			hostname: "eu.sesamefs.com",
			tier:     "hot",
			want:     "hot-s3-eu",
		},
		{
			name:     "China endpoint hot",
			hostname: "cn.sesamefs.com",
			tier:     "hot",
			want:     "hot-s3-china",
		},
		{
			name:     "USA endpoint cold",
			hostname: "us.sesamefs.com",
			tier:     "cold",
			want:     "cold-glacier-usa",
		},
		{
			name:     "Wildcard endpoint",
			hostname: "files.sesamefs.com",
			tier:     "hot",
			want:     "hot-s3-usa",
		},
		{
			name:         "Library override",
			hostname:     "us.sesamefs.com",
			libraryClass: "hot-s3-eu",
			tier:         "hot",
			want:         "hot-s3-eu",
		},
		{
			name:         "Invalid library falls back",
			hostname:     "us.sesamefs.com",
			libraryClass: "nonexistent",
			tier:         "hot",
			want:         "hot-s3-usa",
		},
		{
			name:     "localhost",
			hostname: "localhost",
			tier:     "hot",
			want:     "hot-s3-usa",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := m.ResolveStorageClass(tt.hostname, tt.libraryClass, tt.tier)
			if got != tt.want {
				t.Errorf("ResolveStorageClass(%s, %s, %s) = %s, want %s",
					tt.hostname, tt.libraryClass, tt.tier, got, tt.want)
			}
		})
	}
}

func TestManagerGetHealthyBackend(t *testing.T) {
	m := NewManager()

	usaStore := &mockStore{accessType: AccessImmediate, healthy: true}
	euStore := &mockStore{accessType: AccessImmediate, healthy: true}

	m.RegisterBackend("hot-s3-usa", usaStore, "hot-s3-eu")
	m.RegisterBackend("hot-s3-eu", euStore, "")

	t.Run("healthy backend returns immediately", func(t *testing.T) {
		store, class, err := m.GetHealthyBackend("hot-s3-usa")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if class != "hot-s3-usa" {
			t.Errorf("class = %s, want hot-s3-usa", class)
		}
		if store != usaStore {
			t.Error("wrong store returned")
		}
	})

	t.Run("unhealthy backend uses failover", func(t *testing.T) {
		// Mark USA as unhealthy
		m.UpdateHealth("hot-s3-usa", HealthUnhealthy, nil)

		store, class, err := m.GetHealthyBackend("hot-s3-usa")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if class != "hot-s3-eu" {
			t.Errorf("class = %s, want hot-s3-eu (failover)", class)
		}
		if store != euStore {
			t.Error("wrong store returned")
		}
	})

	t.Run("nonexistent backend returns error", func(t *testing.T) {
		_, _, err := m.GetHealthyBackend("nonexistent")
		if err == nil {
			t.Error("expected error for nonexistent backend")
		}
	})
}

func TestManagerCheckHealth(t *testing.T) {
	m := NewManager()

	t.Run("healthy backend", func(t *testing.T) {
		store := &mockStore{accessType: AccessImmediate, healthy: true}
		m.RegisterBackend("test-healthy", store, "")

		status := m.CheckHealth(context.Background(), "test-healthy")
		if status != HealthHealthy {
			t.Errorf("status = %s, want healthy", status)
		}
	})

	t.Run("unhealthy backend", func(t *testing.T) {
		store := &mockStore{accessType: AccessImmediate, healthy: false}
		m.RegisterBackend("test-unhealthy", store, "")

		status := m.CheckHealth(context.Background(), "test-unhealthy")
		if status != HealthUnhealthy {
			t.Errorf("status = %s, want unhealthy", status)
		}
	})

	t.Run("nonexistent backend", func(t *testing.T) {
		status := m.CheckHealth(context.Background(), "nonexistent")
		if status != HealthFailed {
			t.Errorf("status = %s, want failed", status)
		}
	})
}

func TestManagerListBackends(t *testing.T) {
	m := NewManager()

	m.RegisterBackend("hot-s3-usa", &mockStore{}, "")
	m.RegisterBackend("hot-s3-eu", &mockStore{}, "")
	m.RegisterBackend("cold-glacier-usa", &mockStore{}, "")

	backends := m.ListBackends()
	if len(backends) != 3 {
		t.Errorf("got %d backends, want 3", len(backends))
	}

	// Check all are present (order not guaranteed)
	found := make(map[string]bool)
	for _, b := range backends {
		found[b] = true
	}

	for _, name := range []string{"hot-s3-usa", "hot-s3-eu", "cold-glacier-usa"} {
		if !found[name] {
			t.Errorf("backend %s not found in list", name)
		}
	}
}

func TestHealthStatusString(t *testing.T) {
	tests := []struct {
		status HealthStatus
		want   string
	}{
		{HealthUnknown, "unknown"},
		{HealthHealthy, "healthy"},
		{HealthDegraded, "degraded"},
		{HealthUnhealthy, "unhealthy"},
		{HealthFailed, "failed"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.status.String(); got != tt.want {
				t.Errorf("String() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestManagerGetHotColdBackends(t *testing.T) {
	m := NewManager()

	m.RegisterBackend("hot-s3-usa", &mockStore{accessType: AccessImmediate}, "")
	m.RegisterBackend("hot-s3-eu", &mockStore{accessType: AccessImmediate}, "")
	m.RegisterBackend("cold-glacier-usa", &mockStore{accessType: AccessDelayed}, "")

	hot := m.GetHotBackends()
	if len(hot) != 2 {
		t.Errorf("got %d hot backends, want 2", len(hot))
	}

	cold := m.GetColdBackends()
	if len(cold) != 1 {
		t.Errorf("got %d cold backends, want 1", len(cold))
	}
}

func TestManagerGetBlockStore(t *testing.T) {
	m := NewManager()

	t.Run("non-S3 store returns error", func(t *testing.T) {
		// Register a mock store (not S3Store)
		m.RegisterBackend("mock-store", &mockStore{accessType: AccessImmediate}, "")

		_, err := m.GetBlockStore("mock-store")
		if err == nil {
			t.Error("expected error for non-S3 store")
		}
		if err.Error() != "storage class mock-store is not an S3 backend" {
			t.Errorf("unexpected error message: %v", err)
		}
	})

	t.Run("nonexistent class returns error", func(t *testing.T) {
		_, err := m.GetBlockStore("nonexistent")
		if err == nil {
			t.Error("expected error for nonexistent storage class")
		}
		if err.Error() != "storage class nonexistent not found" {
			t.Errorf("unexpected error message: %v", err)
		}
	})
}

func TestManagerGetHealthyBlockStore(t *testing.T) {
	m := NewManager()

	t.Run("non-S3 store returns error", func(t *testing.T) {
		// Register mock stores (not S3Store)
		m.RegisterBackend("mock-usa", &mockStore{accessType: AccessImmediate, healthy: true}, "")

		_, _, err := m.GetHealthyBlockStore("mock-usa")
		if err == nil {
			t.Error("expected error for non-S3 store")
		}
	})

	t.Run("nonexistent class returns error", func(t *testing.T) {
		_, _, err := m.GetHealthyBlockStore("nonexistent")
		if err == nil {
			t.Error("expected error for nonexistent storage class")
		}
	})
}

func TestManagerBlockStoreCaching(t *testing.T) {
	m := NewManager()

	// Verify that blockStores map is initialized
	if m.blockStores == nil {
		t.Error("blockStores map should be initialized")
	}

	// Register a non-S3 store and try to get BlockStore
	m.RegisterBackend("test-store", &mockStore{accessType: AccessImmediate}, "")

	// First call should fail (not S3)
	_, err1 := m.GetBlockStore("test-store")
	if err1 == nil {
		t.Error("expected error for non-S3 store")
	}

	// Second call should also fail (not cached because of error)
	_, err2 := m.GetBlockStore("test-store")
	if err2 == nil {
		t.Error("expected error for non-S3 store on second call")
	}
}

func TestManagerDefaultClass(t *testing.T) {
	m := NewManager()

	// Initially empty
	if m.defaultClass != "" {
		t.Errorf("defaultClass should be empty initially, got %s", m.defaultClass)
	}

	// Set default
	m.SetDefaultClass("hot-s3-usa")
	if m.defaultClass != "hot-s3-usa" {
		t.Errorf("defaultClass = %s, want hot-s3-usa", m.defaultClass)
	}
}

func TestManagerEndpointRegionsMapping(t *testing.T) {
	m := NewManager()

	// Set endpoint regions
	m.SetEndpointRegions(map[string]string{
		"us.example.com": "usa",
		"eu.example.com": "eu",
	})

	// Verify mapping was set
	if len(m.endpointRegions) != 2 {
		t.Errorf("expected 2 endpoint regions, got %d", len(m.endpointRegions))
	}

	if m.endpointRegions["us.example.com"] != "usa" {
		t.Errorf("expected usa for us.example.com, got %s", m.endpointRegions["us.example.com"])
	}
}

func TestManagerRegionClassesMapping(t *testing.T) {
	m := NewManager()

	// Set region classes
	m.SetRegionClasses(map[string]RegionClassConfig{
		"usa": {Hot: "hot-s3-usa", Cold: "cold-glacier-usa"},
		"eu":  {Hot: "hot-s3-eu", Cold: ""},
	})

	// Verify mapping was set
	if len(m.regionClasses) != 2 {
		t.Errorf("expected 2 region classes, got %d", len(m.regionClasses))
	}

	usaConfig := m.regionClasses["usa"]
	if usaConfig.Hot != "hot-s3-usa" {
		t.Errorf("expected hot-s3-usa for usa hot, got %s", usaConfig.Hot)
	}
	if usaConfig.Cold != "cold-glacier-usa" {
		t.Errorf("expected cold-glacier-usa for usa cold, got %s", usaConfig.Cold)
	}
}

func TestManagerUpdateHealthTracking(t *testing.T) {
	m := NewManager()

	store := &mockStore{accessType: AccessImmediate, healthy: true}
	m.RegisterBackend("test-store", store, "")

	// Initial health should be unknown
	health := m.GetHealth("test-store")
	if health.Status != HealthUnknown {
		t.Errorf("initial status = %s, want unknown", health.Status)
	}

	// Update to healthy
	m.UpdateHealth("test-store", HealthHealthy, nil)
	health = m.GetHealth("test-store")
	if health.Status != HealthHealthy {
		t.Errorf("status = %s, want healthy", health.Status)
	}
	if health.ConsecutiveFails != 0 {
		t.Errorf("consecutive fails = %d, want 0", health.ConsecutiveFails)
	}

	// Update to unhealthy (should increment fails)
	m.UpdateHealth("test-store", HealthUnhealthy, nil)
	health = m.GetHealth("test-store")
	if health.ConsecutiveFails != 1 {
		t.Errorf("consecutive fails = %d, want 1", health.ConsecutiveFails)
	}

	// Update to failed (should increment again)
	m.UpdateHealth("test-store", HealthFailed, nil)
	health = m.GetHealth("test-store")
	if health.ConsecutiveFails != 2 {
		t.Errorf("consecutive fails = %d, want 2", health.ConsecutiveFails)
	}

	// Back to healthy (should reset fails)
	m.UpdateHealth("test-store", HealthHealthy, nil)
	health = m.GetHealth("test-store")
	if health.ConsecutiveFails != 0 {
		t.Errorf("consecutive fails = %d after healthy, want 0", health.ConsecutiveFails)
	}
}
