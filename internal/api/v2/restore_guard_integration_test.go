//go:build integration

package v2

import (
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

var (
	restoreGuardDBOnce sync.Once
	restoreGuardDB     *dbpkg.DB
	restoreGuardDBErr  error
)

func restoreGuardDBForTest(t *testing.T) *dbpkg.DB {
	t.Helper()
	restoreGuardDBOnce.Do(func() {
		cfg := config.DatabaseConfig{
			Hosts:       restoreGuardHosts(restoreGuardEnv("CASSANDRA_HOSTS", "cassandra:9042")),
			Keyspace:    restoreGuardEnv("CASSANDRA_KEYSPACE", "sesamefs"),
			Consistency: restoreGuardEnv("CASSANDRA_CONSISTENCY", "LOCAL_QUORUM"),
			LocalDC:     restoreGuardEnv("CASSANDRA_LOCAL_DC", "datacenter1"),
			Username:    os.Getenv("CASSANDRA_USERNAME"),
			Password:    os.Getenv("CASSANDRA_PASSWORD"),
		}
		restoreGuardDB, restoreGuardDBErr = dbpkg.New(cfg)
	})
	if restoreGuardDBErr != nil {
		t.Fatalf("connect Cassandra for restore-guard test: %v", restoreGuardDBErr)
	}
	return restoreGuardDB
}

func restoreGuardEnv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func restoreGuardHosts(value string) []string {
	parts := strings.Split(value, ",")
	hosts := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			hosts = append(hosts, p)
		}
	}
	if len(hosts) == 0 {
		return []string{"cassandra:9042"}
	}
	return hosts
}

// A stale GC hard-delete lock can be stolen by restore's stale-aware lease
// acquisition. Because a Cassandra UPDATE is an upsert, restore must NOT be able
// to recreate the canonical `libraries` row over content a crashed worker already
// began purging. The only safe state to restore from is the original soft-deleted
// canonical row; if it is gone, restore must reject and leave `libraries` absent.
func TestRestoreDeletedLibrary_RejectsWhenCanonicalRowAbsent(t *testing.T) {
	db := restoreGuardDBForTest(t)
	session := db.Session()
	orgID, libraryID, ownerID := uuid.New(), uuid.New(), uuid.New()

	// Purge residue: a stale GC lock (heartbeat 2h old, so the lease is stealable),
	// but NO canonical `libraries` row.
	staleAt := time.Now().UTC().Add(-2 * time.Hour)
	if err := session.Query(`
		INSERT INTO gc_library_hard_delete_locks (library_id, started_at, heartbeat, lease_token)
		VALUES (?, ?, ?, ?)`,
		libraryID.String(), staleAt, staleAt, uuid.New().String()).Exec(); err != nil {
		t.Fatalf("seed stale lock: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Query(`DELETE FROM gc_library_hard_delete_locks WHERE library_id = ?`, libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM libraries WHERE org_id = ? AND library_id = ?`, orgID.String(), libraryID.String()).Exec()
	})

	if err := restoreDeletedLibrary(db, orgID.String(), ownerID.String(), libraryID.String()); err == nil {
		t.Fatal("restore must reject when the canonical libraries row is absent")
	}

	var got string
	scanErr := session.Query(`SELECT library_id FROM libraries WHERE org_id = ? AND library_id = ?`,
		orgID.String(), libraryID.String()).Scan(&got)
	if !errors.Is(scanErr, gocql.ErrNotFound) {
		t.Fatalf("canonical libraries row must remain absent after a rejected restore; scanErr=%v got=%q", scanErr, got)
	}
}

// A present-but-active canonical row (deleted_at == null) is not in trash: restore
// must reject rather than run its clearing batch.
func TestRestoreDeletedLibrary_RejectsWhenCanonicalRowActive(t *testing.T) {
	db := restoreGuardDBForTest(t)
	session := db.Session()
	orgID, libraryID, ownerID := uuid.New(), uuid.New(), uuid.New()

	if err := session.Query(`
		INSERT INTO libraries (org_id, library_id, owner_id, name, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		orgID.String(), libraryID.String(), ownerID.String(), "restore-guard-active",
		time.Now().UTC(), time.Now().UTC()).Exec(); err != nil {
		t.Fatalf("seed active library: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Query(`DELETE FROM libraries WHERE org_id = ? AND library_id = ?`, orgID.String(), libraryID.String()).Exec()
	})

	if err := restoreDeletedLibrary(db, orgID.String(), ownerID.String(), libraryID.String()); err == nil {
		t.Fatal("restore must reject a library that is not in trash (deleted_at is null)")
	}

	var deletedAt time.Time
	if err := session.Query(`SELECT deleted_at FROM libraries WHERE org_id = ? AND library_id = ?`,
		orgID.String(), libraryID.String()).Scan(&deletedAt); err != nil {
		t.Fatalf("read canonical library after rejected restore: %v", err)
	}
	if !deletedAt.IsZero() {
		t.Fatal("active library must stay active (deleted_at null) after a rejected restore")
	}
}
