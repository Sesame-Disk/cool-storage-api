//go:build integration

package integration

import (
	"os"
	"strings"
	"testing"
	"time"

	v2api "github.com/Sesame-Disk/sesamefs/internal/api/v2"
	"github.com/Sesame-Disk/sesamefs/internal/config"
	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

const (
	w2PostHeadMultidcEvidenceEnv = "SESAMEFS_REQUIRE_W2_POST_HEAD_MULTIDC_EVIDENCE"
	w2PostHeadMultidcEndpoints   = "W2_POST_HEAD_3DC_HOSTS"
)

var w2PostHeadMultidcEvidence bool

func w2PostHead3DCEndpoints(t *testing.T) map[string]string {
	t.Helper()

	raw := strings.TrimSpace(os.Getenv(w2PostHeadMultidcEndpoints))
	if raw == "" {
		t.Fatalf("%s must name dc-na, dc-eu, and dc-asia", w2PostHeadMultidcEndpoints)
	}
	endpoints := make(map[string]string)
	for _, entry := range strings.Split(raw, ",") {
		dc, host, ok := strings.Cut(strings.TrimSpace(entry), "=")
		dc, host = strings.TrimSpace(dc), strings.TrimSpace(host)
		if !ok || dc == "" || host == "" {
			t.Fatalf("malformed %s entry %q; want dc=host:port", w2PostHeadMultidcEndpoints, entry)
		}
		endpoints[dc] = host
	}
	for _, dc := range []string{"dc-na", "dc-eu", "dc-asia"} {
		if strings.TrimSpace(endpoints[dc]) == "" {
			t.Fatalf("%s is missing %s", w2PostHeadMultidcEndpoints, dc)
		}
	}
	return endpoints
}

func w2PostHead3DCConnect(t *testing.T, dc string, endpoints map[string]string) *dbpkg.DB {
	t.Helper()

	database, err := dbpkg.New(config.DatabaseConfig{
		Hosts:             []string{endpoints[dc]},
		Keyspace:          envOrDefault("CASSANDRA_KEYSPACE", "sesamefs"),
		Consistency:       "LOCAL_QUORUM",
		SerialConsistency: "SERIAL",
		LocalDC:           dc,
		ReplicationClass:  "NetworkTopologyStrategy",
		ReplicationDCs: map[string]int{
			"dc-na":   1,
			"dc-eu":   1,
			"dc-asia": 1,
		},
		Username: os.Getenv("CASSANDRA_USERNAME"),
		Password: os.Getenv("CASSANDRA_PASSWORD"),
	})
	if err != nil {
		t.Fatalf("connect to %s: %v", dc, err)
	}
	t.Cleanup(database.Close)
	return database
}

func w2PostHead3DCIDs(t *testing.T) (orgID, repoID, parentID string) {
	t.Helper()
	for name, value := range map[string]string{
		"W2_POST_HEAD_ORG":    os.Getenv("W2_POST_HEAD_ORG"),
		"W2_POST_HEAD_REPO":   os.Getenv("W2_POST_HEAD_REPO"),
		"W2_POST_HEAD_PARENT": os.Getenv("W2_POST_HEAD_PARENT"),
	} {
		if strings.TrimSpace(value) == "" {
			t.Fatalf("%s is required", name)
		}
	}
	return os.Getenv("W2_POST_HEAD_ORG"), os.Getenv("W2_POST_HEAD_REPO"), os.Getenv("W2_POST_HEAD_PARENT")
}

// TestW2PostHeadSeedGlobalBaseFor3DC creates the stale base HEAD in every DC.
// The runner script invokes this before it stops dc-na and dc-asia.
func TestW2PostHeadSeedGlobalBaseFor3DC(t *testing.T) {
	if os.Getenv("W2_POST_HEAD_SEED_BASE") != "1" {
		t.Skip("W2_POST_HEAD_SEED_BASE is not set")
	}
	endpoints := w2PostHead3DCEndpoints(t)
	database := w2PostHead3DCConnect(t, "dc-na", endpoints)
	orgID, repoID, parentID := uuid.NewString(), uuid.NewString(), "w2-3dc-parent-"+uuid.NewString()
	now := time.Now().UTC()

	if err := database.Session().Query(`
		INSERT INTO libraries (org_id, library_id, name, head_commit_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, orgID, repoID, "w2-post-head-3dc", parentID, now, now).Consistency(gocql.EachQuorum).Exec(); err != nil {
		t.Fatalf("seed global base library: %v", err)
	}
	if err := database.Session().Query(`
		INSERT INTO commits (library_id, commit_id, parent_id, root_fs_id, description, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, repoID, parentID, "", "w2-3dc-root-"+uuid.NewString(), "w2 3dc base", now).Consistency(gocql.EachQuorum).Exec(); err != nil {
		t.Fatalf("seed global base commit: %v", err)
	}
	t.Logf("W2_POST_HEAD_ORG=%s", orgID)
	t.Logf("W2_POST_HEAD_REPO=%s", repoID)
	t.Logf("W2_POST_HEAD_PARENT=%s", parentID)
}

// TestW2PostHeadWriteRemoteCommitFor3DC publishes the new HEAD only in dc-eu
// while the other two datacenters are stopped and hinted handoff is disabled.
func TestW2PostHeadWriteRemoteCommitFor3DC(t *testing.T) {
	if os.Getenv("W2_POST_HEAD_WRITE_REMOTE") != "1" {
		t.Skip("W2_POST_HEAD_WRITE_REMOTE is not set")
	}
	endpoints := w2PostHead3DCEndpoints(t)
	database := w2PostHead3DCConnect(t, "dc-eu", endpoints)
	orgID, repoID, parentID := w2PostHead3DCIDs(t)
	commitID := "w2-3dc-remote-" + uuid.NewString()
	now := time.Now().UTC()

	if err := database.Session().Query(`
		INSERT INTO commits (library_id, commit_id, parent_id, root_fs_id, description, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, repoID, commitID, parentID, "w2-3dc-root-"+uuid.NewString(), "w2 3dc remote head", now).Consistency(gocql.LocalQuorum).Exec(); err != nil {
		t.Fatalf("seed remote commit in dc-eu: %v", err)
	}
	casState := map[string]interface{}{}
	applied, err := database.Session().Query(`
		UPDATE libraries SET head_commit_id = ?, updated_at = ?
		WHERE org_id = ? AND library_id = ?
		IF head_commit_id = ?
		`, commitID, now, orgID, repoID, parentID).
		Consistency(gocql.LocalQuorum).
		SerialConsistency(gocql.LocalSerial).
		MapScanCAS(casState)
	if err != nil {
		t.Fatalf("publish remote HEAD CAS in dc-eu: %v (state=%v)", err, casState)
	}
	if !applied {
		t.Fatalf("remote HEAD CAS lost unexpectedly: state=%v", casState)
	}
	t.Logf("W2_POST_HEAD_COMMIT=%s", commitID)
}

// TestW2PostHeadRepairDoesNotMisclassifyRemoteHead3DC proves the minimum
// multi-DC contract for this slice: a local blind read must never turn a
// commit published in another DC into positive non-publication evidence.
func TestW2PostHeadRepairDoesNotMisclassifyRemoteHead3DC(t *testing.T) {
	if os.Getenv(w2PostHeadMultidcEvidenceEnv) != "1" {
		t.Skipf("%s is not set", w2PostHeadMultidcEvidenceEnv)
	}
	endpoints := w2PostHead3DCEndpoints(t)
	database := w2PostHead3DCConnect(t, "dc-na", endpoints)
	orgID, repoID, parentID := w2PostHead3DCIDs(t)
	commitID := strings.TrimSpace(os.Getenv("W2_POST_HEAD_COMMIT"))
	if commitID == "" {
		t.Fatal("W2_POST_HEAD_COMMIT is required")
	}
	t.Cleanup(func() {
		_ = database.Session().Query(`DELETE FROM commits WHERE library_id = ?`, repoID).Consistency(gocql.EachQuorum).Exec()
		_ = database.Session().Query(`DELETE FROM libraries WHERE org_id = ? AND library_id = ?`, orgID, repoID).Consistency(gocql.EachQuorum).Exec()
	})

	var localHead string
	if err := database.Session().Query(`
		SELECT head_commit_id FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Consistency(gocql.LocalQuorum).Scan(&localHead); err != nil {
		t.Fatalf("read local stale HEAD from dc-na: %v", err)
	}
	if localHead != parentID {
		t.Fatalf("fixture is not divergent: dc-na local HEAD=%q, want stale parent %q", localHead, parentID)
	}

	outcome, err := v2api.PublishedBlockReferenceRepairCommitOutcomeForIntegration(database, orgID, repoID, commitID)
	if outcome == "definitely_not_published" {
		t.Fatalf("W2 3DC REGRESSION: dc-na local blindness classified a commit published in dc-eu as definitely not published (err=%v)", err)
	}
	if outcome != "reachable" && outcome != "unknown" {
		t.Fatalf("unexpected W2 3DC repair outcome %q (err=%v)", outcome, err)
	}
	if err != nil {
		t.Logf("W2 3DC safe fail-closed outcome=%s with confirmation error: %v", outcome, err)
	} else {
		t.Logf("W2 3DC remote publication outcome=%s; local LOCAL_QUORUM remained blind", outcome)
	}
	w2PostHeadMultidcEvidence = true
}
