//go:build integration

package api

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
	initialCommitAuthorityDBOnce sync.Once
	initialCommitAuthorityDB     *dbpkg.DB
	initialCommitAuthorityDBErr  error
)

func initialCommitAuthorityDBForTest(t *testing.T) *dbpkg.DB {
	t.Helper()
	initialCommitAuthorityDBOnce.Do(func() {
		hosts := strings.Split(initialCommitAuthorityEnv("CASSANDRA_HOSTS", "cassandra:9042"), ",")
		cleanHosts := make([]string, 0, len(hosts))
		for _, host := range hosts {
			if host = strings.TrimSpace(host); host != "" {
				cleanHosts = append(cleanHosts, host)
			}
		}
		if len(cleanHosts) == 0 {
			cleanHosts = []string{"cassandra:9042"}
		}
		initialCommitAuthorityDB, initialCommitAuthorityDBErr = dbpkg.New(config.DatabaseConfig{
			Hosts:       cleanHosts,
			Keyspace:    initialCommitAuthorityEnv("CASSANDRA_KEYSPACE", "sesamefs"),
			Consistency: initialCommitAuthorityEnv("CASSANDRA_CONSISTENCY", "LOCAL_QUORUM"),
			LocalDC:     initialCommitAuthorityEnv("CASSANDRA_LOCAL_DC", "datacenter1"),
			Username:    os.Getenv("CASSANDRA_USERNAME"),
			Password:    os.Getenv("CASSANDRA_PASSWORD"),
		})
	})
	if initialCommitAuthorityDBErr != nil {
		t.Fatalf("connect Cassandra for initial-commit authority test: %v", initialCommitAuthorityDBErr)
	}
	return initialCommitAuthorityDB
}

func initialCommitAuthorityEnv(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func TestCreateInitialCommit_RejectsTerminalPublicationAuthority(t *testing.T) {
	db := initialCommitAuthorityDBForTest(t)
	session := db.Session()
	orgID, libraryID, ownerID := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC().Truncate(time.Millisecond)

	if err := session.Query(`
		INSERT INTO libraries (org_id, library_id, owner_id, name, created_at, updated_at, publication_state)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		orgID.String(), libraryID.String(), ownerID.String(), "initial-commit-guard-terminal",
		now.Add(-4*time.Hour), now, dbpkg.LibraryPublicationStateTerminal).Exec(); err != nil {
		t.Fatalf("seed terminal library for initial commit: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Query(`DELETE FROM fs_objects WHERE library_id = ?`, libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM commits WHERE library_id = ?`, libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM libraries_by_id WHERE library_id = ?`, libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM libraries WHERE org_id = ? AND library_id = ?`, orgID.String(), libraryID.String()).Exec()
	})

	_, err := (&SyncHandler{db: db}).createInitialCommit(libraryID.String(), orgID.String(), ownerID.String())
	if err == nil {
		t.Fatal("initial commit must reject terminal publication authority")
	}

	var headCommitID string
	var publicationState string
	if err := session.Query(`SELECT head_commit_id, publication_state FROM libraries WHERE org_id = ? AND library_id = ?`,
		orgID.String(), libraryID.String()).Scan(&headCommitID, &publicationState); err != nil {
		t.Fatalf("read terminal library after rejected initial commit: %v", err)
	}
	if headCommitID != "" || publicationState != dbpkg.LibraryPublicationStateTerminal {
		t.Fatalf("terminal library changed after rejected initial commit: head=%q publication_state=%q", headCommitID, publicationState)
	}
	var stagedFSID, stagedCommitID string
	fsErr := session.Query(`SELECT fs_id FROM fs_objects WHERE library_id = ? LIMIT 1`, libraryID.String()).Scan(&stagedFSID)
	commitErr := session.Query(`SELECT commit_id FROM commits WHERE library_id = ? LIMIT 1`, libraryID.String()).Scan(&stagedCommitID)
	if !errors.Is(fsErr, gocql.ErrNotFound) || !errors.Is(commitErr, gocql.ErrNotFound) {
		t.Fatalf("rejected initial commit left staged artifacts: fsErr=%v fsID=%q commitErr=%v commitID=%q", fsErr, stagedFSID, commitErr, stagedCommitID)
	}
}
