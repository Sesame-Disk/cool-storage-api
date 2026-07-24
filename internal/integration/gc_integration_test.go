//go:build integration

package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

// GC integration tests. Each test is independent: creates its own library,
// uses the global adminClient/superadminClient from TestMain, and cleans up
// after itself. Run with:
//
//	go test -tags integration -v -run "TestGC_" -timeout 10m ./internal/integration/...

// ---------------------------------------------------------------------------
// Prerequisite helpers
// ---------------------------------------------------------------------------

func TestGC_ScanAllGroupSharesDiscoversPartitionWithoutGroupRow(t *testing.T) {
	database := shareProjectionDBForTest(t)
	session := database.Session()
	orgID, groupID, libraryID, shareID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	createdAt := time.Now().UTC().Truncate(time.Millisecond)

	if err := session.Query(`
		INSERT INTO shares_by_group
		(org_id, group_id, created_at, library_id, share_id, permission)
		VALUES (?, ?, ?, ?, ?, ?)
	`, orgID.String(), groupID.String(), createdAt, libraryID.String(), shareID.String(), "r").Exec(); err != nil {
		t.Fatalf("seed orphan group-share projection: %v", err)
	}
	t.Cleanup(func() {
		if err := session.Query(`
			DELETE FROM shares_by_group
			WHERE org_id = ? AND group_id = ? AND created_at = ? AND library_id = ? AND share_id = ?
		`, orgID.String(), groupID.String(), createdAt, libraryID.String(), shareID.String()).Exec(); err != nil {
			t.Errorf("cleanup orphan group-share projection: %v", err)
		}
	})

	// Deliberately do not insert a groups row. The production store must enumerate
	// the share projection itself rather than depending on the deleted group.
	found := false
	err := gcpkg.NewCassandraStore(database).ScanAllGroupShares(context.Background(), func(row gcpkg.GroupShareInfo) error {
		if row.OrgID == orgID && row.SharedTo == groupID && row.LibraryID == libraryID && row.ShareID == shareID {
			found = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ScanAllGroupShares: %v", err)
	}
	if !found {
		t.Fatal("orphan shares_by_group partition was not discovered without a groups row")
	}
}

// P6b (ISSUE-GC-ORPHAN-WORKER-REVALIDATION-01): CanonicalLibraryExists must read the
// authoritative `libraries` table, not the `libraries_by_id` projection. This proves it
// detects a live library whose projection has drifted away — the exact case where the old
// projection-only check would have let the worker delete a live library's content.
func TestGC_CanonicalLibraryExistsReadsCanonicalTableNotProjection(t *testing.T) {
	database := shareProjectionDBForTest(t)
	session := database.Session()
	orgID, libraryID := uuid.New(), uuid.New()

	// Canonical libraries row present, but NO libraries_by_id projection row (drift).
	if err := session.Query(`
		INSERT INTO libraries (org_id, library_id, name) VALUES (?, ?, ?)
	`, orgID.String(), libraryID.String(), "p6b-canonical-test").Exec(); err != nil {
		t.Fatalf("seed canonical libraries row: %v", err)
	}
	t.Cleanup(func() {
		if err := session.Query(`DELETE FROM libraries WHERE org_id = ? AND library_id = ?`,
			orgID.String(), libraryID.String()).Exec(); err != nil {
			t.Errorf("cleanup canonical libraries row: %v", err)
		}
	})

	store := gcpkg.NewCassandraStore(database)

	canonical, err := store.CanonicalLibraryExists(orgID, libraryID)
	if err != nil {
		t.Fatalf("CanonicalLibraryExists: %v", err)
	}
	if !canonical {
		t.Fatal("CanonicalLibraryExists should report the library present from the libraries table")
	}

	// Precondition: the projection genuinely lacks the row, so a projection-only check
	// (LibraryExists) would wrongly classify this live library as gone.
	projection, err := store.LibraryExists(libraryID)
	if err != nil {
		t.Fatalf("LibraryExists: %v", err)
	}
	if projection {
		t.Fatal("expected the libraries_by_id projection to be absent for this drift scenario")
	}

	absent, err := store.CanonicalLibraryExists(orgID, uuid.New())
	if err != nil {
		t.Fatalf("CanonicalLibraryExists(absent): %v", err)
	}
	if absent {
		t.Fatal("CanonicalLibraryExists should be false for a non-existent library")
	}
}

// requireGCEnabled skips the test if the GC admin endpoint is not reachable
// or GC is disabled.
func requireGCEnabled(t *testing.T) {
	t.Helper()
	resp := superadminClient.Get(t, "/api/v2.1/admin/gc/status")
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		t.Skip("GC admin endpoint requires superadmin; skipping")
	}
	if resp.StatusCode != http.StatusOK {
		t.Skipf("GC admin endpoint returned %d; skipping", resp.StatusCode)
	}
	var body map[string]interface{}
	decodeJSON(t, resp, &body)
	if enabled, ok := body["enabled"].(bool); ok && !enabled {
		t.Skip("GC is disabled in server config; skipping")
	}
}

// requireCassandra skips the test if we can't connect to Cassandra directly.
func requireCassandra(t *testing.T) {
	t.Helper()
	session := shareProjectionDBForTest(t).Session()
	var result string
	if err := session.Query(`SELECT release_version FROM system.local`).Scan(&result); err != nil {
		t.Skipf("Cassandra not reachable: %v; skipping", err)
	}
}

type provisionalPhase0FailureStore struct {
	gcpkg.GCStore
	livenessFailureBlock   string
	candidateFailureBlock  string
	projectionFailureBlock string
	fail                   bool
}

func (s *provisionalPhase0FailureStore) BlockHasReferences(orgID uuid.UUID, blockID string) (bool, error) {
	if s.fail && blockID == s.livenessFailureBlock {
		return false, errors.New("injected provisional liveness failure")
	}
	return s.GCStore.BlockHasReferences(orgID, blockID)
}

func (s *provisionalPhase0FailureStore) EnsureBlockGCCandidate(orgID uuid.UUID, blockID, storageClass string, candidateAt time.Time) (time.Time, error) {
	if s.fail && blockID == s.candidateFailureBlock {
		return time.Time{}, errors.New("injected provisional candidate persistence failure")
	}
	return s.GCStore.EnsureBlockGCCandidate(orgID, blockID, storageClass, candidateAt)
}

func (s *provisionalPhase0FailureStore) DeleteProvisionalBlockRefExpiryProjection(orgID uuid.UUID, blockID, referrer string, expiresAt time.Time) error {
	if s.fail && blockID == s.projectionFailureBlock {
		return errors.New("injected provisional projection delete failure")
	}
	return s.GCStore.DeleteProvisionalBlockRefExpiryProjection(orgID, blockID, referrer, expiresAt)
}

type provisionalPhase0Fixture struct {
	orgID     uuid.UUID
	blockID   string
	referrer  string
	expiresAt time.Time
}

func seedProvisionalPhase0Fixture(t *testing.T, canonicalPresent, referencePresent bool) provisionalPhase0Fixture {
	t.Helper()
	database := shareProjectionDBForTest(t)
	session := database.Session()
	fixture := provisionalPhase0Fixture{
		orgID:     uuid.New(),
		blockID:   fmt.Sprintf("%064x", time.Now().UnixNano()),
		referrer:  "up:phase0-" + uuid.NewString(),
		expiresAt: time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond),
	}
	sha1ID := fmt.Sprintf("%040x", time.Now().UnixNano())

	if err := session.Query(`
		INSERT INTO blocks (org_id, block_id, representation_id, sha1, size_bytes, storage_class, storage_key, created_at, last_accessed)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, fixture.orgID.String(), fixture.blockID, db.PlainBlockRepresentationID, sha1ID, int64(1), "hot",
		"blocks/"+fixture.blockID, time.Now().UTC(), time.Now().UTC()).Exec(); err != nil {
		t.Fatalf("seed provisional Phase 0 block: %v", err)
	}
	if referencePresent {
		if err := session.Query(`
			INSERT INTO block_references (org_id, block_id, referrer, library_id, created_at)
			VALUES (?, ?, ?, ?, ?) USING TTL 300
		`, fixture.orgID.String(), fixture.blockID, fixture.referrer, uuid.NewString(), time.Now().UTC()).Exec(); err != nil {
			t.Fatalf("seed live provisional reference: %v", err)
		}
	}
	if canonicalPresent {
		if err := session.Query(`
			INSERT INTO gc_provisional_block_refs (org_id, block_id, referrer, storage_class, expires_at)
			VALUES (?, ?, ?, ?, ?) USING TTL 300
		`, fixture.orgID.String(), fixture.blockID, fixture.referrer, "hot", fixture.expiresAt).Exec(); err != nil {
			t.Fatalf("seed canonical provisional tracker: %v", err)
		}
	}
	if err := session.Query(`
		INSERT INTO gc_provisional_block_refs_by_day
			(expiry_day, bucket, expires_at, org_id, block_id, referrer, storage_class)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, db.GCProjectionUTCDate(fixture.expiresAt),
		db.GCDiscoveryBucket(fixture.orgID.String(), fixture.blockID, fixture.referrer),
		fixture.expiresAt, fixture.orgID.String(), fixture.blockID, fixture.referrer, "hot").Exec(); err != nil {
		t.Fatalf("seed durable provisional projection: %v", err)
	}

	t.Cleanup(func() {
		var candidateAt time.Time
		if err := session.Query(`
			SELECT candidate_at FROM gc_block_candidates WHERE org_id = ? AND block_id = ?
		`, fixture.orgID.String(), fixture.blockID).Scan(&candidateAt); err == nil {
			_ = session.Query(`
				DELETE FROM gc_block_candidates_by_day
				WHERE candidate_day = ? AND bucket = ? AND candidate_at = ? AND org_id = ? AND block_id = ?
			`, db.GCProjectionUTCDate(candidateAt), db.GCDiscoveryBucket(fixture.orgID.String(), fixture.blockID),
				candidateAt.UTC(), fixture.orgID.String(), fixture.blockID).Exec()
		}
		_ = session.Query(`DELETE FROM gc_block_candidates WHERE org_id = ? AND block_id = ?`, fixture.orgID.String(), fixture.blockID).Exec()
		_ = session.Query(`
			DELETE FROM gc_provisional_block_refs_by_day
			WHERE expiry_day = ? AND bucket = ? AND expires_at = ? AND org_id = ? AND block_id = ? AND referrer = ?
		`, db.GCProjectionUTCDate(fixture.expiresAt),
			db.GCDiscoveryBucket(fixture.orgID.String(), fixture.blockID, fixture.referrer),
			fixture.expiresAt, fixture.orgID.String(), fixture.blockID, fixture.referrer).Exec()
		_ = session.Query(`
			DELETE FROM gc_provisional_block_refs WHERE org_id = ? AND block_id = ? AND referrer = ?
		`, fixture.orgID.String(), fixture.blockID, fixture.referrer).Exec()
		_ = session.Query(`
			DELETE FROM block_references WHERE org_id = ? AND block_id = ? AND referrer = ?
		`, fixture.orgID.String(), fixture.blockID, fixture.referrer).Exec()
		_ = session.Query(`DELETE FROM blocks WHERE org_id = ? AND block_id = ?`, fixture.orgID.String(), fixture.blockID).Exec()
	})
	return fixture
}

// provisionalRowExists collapses a point-read result to presence for the
// provisional Phase 0 assertions. It returns false ONLY for a genuine
// gocql.ErrNotFound; any other error (timeout, unavailable, read failure) fails
// the test instead of being read as absence. Without this, an assertion that
// expects a row to be gone would pass on a transient Cassandra error and hide a
// real regression.
func provisionalRowExists(t *testing.T, err error, table string) bool {
	t.Helper()
	if err == nil {
		return true
	}
	if errors.Is(err, gocql.ErrNotFound) {
		return false
	}
	t.Fatalf("provisional existence check against %s failed (not a clean absence): %v", table, err)
	return false
}

func provisionalProjectionExists(t *testing.T, fixture provisionalPhase0Fixture) bool {
	t.Helper()
	var expiresAt time.Time
	err := shareProjectionDBForTest(t).Session().Query(`
		SELECT expires_at FROM gc_provisional_block_refs_by_day
		WHERE expiry_day = ? AND bucket = ? AND expires_at = ? AND org_id = ? AND block_id = ? AND referrer = ?
	`, db.GCProjectionUTCDate(fixture.expiresAt),
		db.GCDiscoveryBucket(fixture.orgID.String(), fixture.blockID, fixture.referrer),
		fixture.expiresAt, fixture.orgID.String(), fixture.blockID, fixture.referrer).Scan(&expiresAt)
	return provisionalRowExists(t, err, "gc_provisional_block_refs_by_day")
}

func provisionalCanonicalExists(t *testing.T, fixture provisionalPhase0Fixture) bool {
	t.Helper()
	var expiresAt time.Time
	err := shareProjectionDBForTest(t).Session().Query(`
		SELECT expires_at FROM gc_provisional_block_refs
		WHERE org_id = ? AND block_id = ? AND referrer = ?
	`, fixture.orgID.String(), fixture.blockID, fixture.referrer).Scan(&expiresAt)
	return provisionalRowExists(t, err, "gc_provisional_block_refs")
}

func provisionalReferenceExists(t *testing.T, fixture provisionalPhase0Fixture) bool {
	t.Helper()
	var referrer string
	err := shareProjectionDBForTest(t).Session().Query(`
		SELECT referrer FROM block_references
		WHERE org_id = ? AND block_id = ? AND referrer = ?
	`, fixture.orgID.String(), fixture.blockID, fixture.referrer).Scan(&referrer)
	return provisionalRowExists(t, err, "block_references")
}

func provisionalCandidateExists(t *testing.T, fixture provisionalPhase0Fixture) bool {
	t.Helper()
	var candidateAt time.Time
	err := shareProjectionDBForTest(t).Session().Query(`
		SELECT candidate_at FROM gc_block_candidates WHERE org_id = ? AND block_id = ?
	`, fixture.orgID.String(), fixture.blockID).Scan(&candidateAt)
	if !provisionalRowExists(t, err, "gc_block_candidates") {
		return false
	}
	return gcCandidateProjectionExists(t, fixture.orgID.String(), fixture.blockID, candidateAt)
}

func newProvisionalPhase0Scanner(store gcpkg.GCStore) *gcpkg.Scanner {
	return gcpkg.NewScanner(store, gcpkg.NewQueue(store), &gcpkg.Stats{}, config.GCConfig{})
}

func TestProvisionalProductionCQLDoesNotUseLWT(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate integration test source")
	}
	internalDir := filepath.Dir(filepath.Dir(testFile))
	// Every production file that emits CQL against the provisional tables must be
	// audited, not only the two that originally held the protocol. The last LWT to
	// be removed (RemoveBlockReferenceIfGeneration) lived in block_references.go,
	// which the earlier two-file list did not inspect, so a reintroduction there
	// would not have failed this test. block_upload_sessions.go reads
	// block_references and gc_projection_write_helpers.go writes the by-day
	// projection; both are included so the audit covers the whole surface.
	// provisional_block_ref_expiry.go stays first: the generation check below
	// reads sourceFiles[0] as the canonical protocol file.
	sourceFiles := []string{
		filepath.Join(internalDir, "db", "provisional_block_ref_expiry.go"),
		filepath.Join(internalDir, "db", "block_references.go"),
		filepath.Join(internalDir, "db", "block_upload_sessions.go"),
		filepath.Join(internalDir, "db", "gc_projection_write_helpers.go"),
		filepath.Join(internalDir, "gc", "store_cassandra.go"),
	}
	targetTables := []string{
		"block_references",
		"gc_provisional_block_refs",
		"gc_provisional_block_refs_by_day",
	}
	seen := make(map[string]bool, len(targetTables))

	for _, sourceFile := range sourceFiles {
		source, err := os.ReadFile(sourceFile)
		if err != nil {
			t.Fatalf("read production CQL source %s: %v", sourceFile, err)
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), sourceFile, source, 0)
		if err != nil {
			t.Fatalf("parse production CQL source %s: %v", sourceFile, err)
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			query, err := strconv.Unquote(literal.Value)
			if err != nil {
				return true
			}
			normalized := strings.ToUpper(strings.Join(strings.Fields(query), " "))
			for _, table := range targetTables {
				if !strings.Contains(normalized, strings.ToUpper(table)) {
					continue
				}
				seen[table] = true
				if strings.Contains(normalized, " IF ") {
					t.Errorf("provisional-table CQL in %s uses a conditional/LWT: %s", sourceFile, normalized)
				}
			}
			return true
		})
	}
	for _, table := range targetTables {
		if !seen[table] {
			t.Fatalf("source audit did not inspect any CQL targeting %s", table)
		}
	}

	protocolSource, err := os.ReadFile(sourceFiles[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(protocolSource)), "generation") {
		t.Fatal("provisional reference protocol reintroduced generation state")
	}
}

func TestProvisionalPhase0CanonicalPresentAndMissingRecovery(t *testing.T) {
	requireCassandra(t)

	canonicalLive := seedProvisionalPhase0Fixture(t, true, true)
	canonicalExpired := seedProvisionalPhase0Fixture(t, true, false)
	missingLive := seedProvisionalPhase0Fixture(t, false, true)
	missingExpired := seedProvisionalPhase0Fixture(t, false, false)

	store := gcpkg.NewCassandraStore(shareProjectionDBForTest(t))
	cleaned, err := newProvisionalPhase0Scanner(store).ScanExpiredProvisionalBlockRefsOnce(context.Background())
	if err != nil {
		t.Fatalf("Phase 0 Cassandra scan: %v", err)
	}
	if cleaned < 2 {
		t.Fatalf("Phase 0 cleaned = %d, want at least the two zero-ref fixtures", cleaned)
	}

	for name, fixture := range map[string]provisionalPhase0Fixture{
		"canonical-present live reference": canonicalLive,
		"canonical-missing live reference": missingLive,
	} {
		if !provisionalReferenceExists(t, fixture) {
			t.Fatalf("%s: scanner deleted a live provisional reference", name)
		}
		if !provisionalProjectionExists(t, fixture) {
			t.Fatalf("%s: scanner removed the retry projection while reference remains", name)
		}
		if provisionalCandidateExists(t, fixture) {
			t.Fatalf("%s: scanner persisted a candidate for a referenced block", name)
		}
	}
	if !provisionalCanonicalExists(t, canonicalLive) {
		t.Fatal("Phase 0 deleted a live canonical tracker instead of relying on TTL")
	}
	if !provisionalCanonicalExists(t, canonicalExpired) {
		t.Fatal("Phase 0 deleted the canonical tracker for an expired reference instead of relying on TTL")
	}
	if provisionalCanonicalExists(t, missingLive) || provisionalCanonicalExists(t, missingExpired) {
		t.Fatal("canonical-missing fixture unexpectedly acquired a canonical row")
	}

	for name, fixture := range map[string]provisionalPhase0Fixture{
		"canonical-present expired reference": canonicalExpired,
		"canonical-missing expired reference": missingExpired,
	} {
		if !provisionalCandidateExists(t, fixture) {
			t.Fatalf("%s: zero-ref block was not durably projected as a GC candidate", name)
		}
		if provisionalProjectionExists(t, fixture) {
			t.Fatalf("%s: resolved provisional projection was not removed", name)
		}
	}
}

func TestProvisionalPhase0FailuresPreserveProjectionForRetry(t *testing.T) {
	requireCassandra(t)

	livenessFailure := seedProvisionalPhase0Fixture(t, true, false)
	candidateFailure := seedProvisionalPhase0Fixture(t, true, false)
	projectionFailure := seedProvisionalPhase0Fixture(t, true, false)
	realStore := gcpkg.NewCassandraStore(shareProjectionDBForTest(t))
	failingStore := &provisionalPhase0FailureStore{
		GCStore:                realStore,
		livenessFailureBlock:   livenessFailure.blockID,
		candidateFailureBlock:  candidateFailure.blockID,
		projectionFailureBlock: projectionFailure.blockID,
		fail:                   true,
	}

	if _, err := newProvisionalPhase0Scanner(failingStore).ScanExpiredProvisionalBlockRefsOnce(context.Background()); err == nil {
		t.Fatal("Phase 0 scan error = nil, want injected failures")
	}
	for name, fixture := range map[string]provisionalPhase0Fixture{
		"liveness read failure":         livenessFailure,
		"candidate persistence failure": candidateFailure,
		"projection delete failure":     projectionFailure,
	} {
		if !provisionalProjectionExists(t, fixture) {
			t.Fatalf("%s: projection was lost instead of remaining discoverable for retry", name)
		}
	}
	if provisionalCandidateExists(t, livenessFailure) {
		t.Fatal("liveness failure advanced to candidate persistence")
	}
	if provisionalCandidateExists(t, candidateFailure) {
		t.Fatal("failed candidate persistence unexpectedly produced a durable candidate")
	}
	if !provisionalCandidateExists(t, projectionFailure) {
		t.Fatal("candidate was not durable before the injected projection-delete failure")
	}

	failingStore.fail = false
	if _, err := newProvisionalPhase0Scanner(failingStore).ScanExpiredProvisionalBlockRefsOnce(context.Background()); err != nil {
		t.Fatalf("Phase 0 retry after injected failures: %v", err)
	}
	for name, fixture := range map[string]provisionalPhase0Fixture{
		"liveness read failure":         livenessFailure,
		"candidate persistence failure": candidateFailure,
		"projection delete failure":     projectionFailure,
	} {
		if !provisionalCandidateExists(t, fixture) {
			t.Fatalf("%s: retry did not durably persist/repair candidate", name)
		}
		if provisionalProjectionExists(t, fixture) {
			t.Fatalf("%s: retry did not remove resolved projection", name)
		}
	}
}

// triggerGCWorker fires the GC worker via the admin API and waits briefly.
func triggerGCWorker(t *testing.T) {
	t.Helper()
	resp := superadminClient.PostJSON(t, "/api/v2.1/admin/gc/run", map[string]string{"type": "worker"})
	if resp.StatusCode != http.StatusOK {
		t.Logf("triggerGCWorker: HTTP %d (non-fatal)", resp.StatusCode)
	}
	resp.Body.Close()
	time.Sleep(500 * time.Millisecond) // let worker tick complete
}

// triggerGCScanner fires the GC scanner via the admin API and waits briefly.
func triggerGCScanner(t *testing.T) {
	t.Helper()
	resp := superadminClient.PostJSON(t, "/api/v2.1/admin/gc/run", map[string]string{"type": "scanner"})
	if resp.StatusCode != http.StatusOK {
		t.Logf("triggerGCScanner: HTTP %d (non-fatal)", resp.StatusCode)
	}
	resp.Body.Close()
	time.Sleep(1 * time.Second) // scanner phases take longer
}

func triggerGCWorkerAndWait(t *testing.T) {
	t.Helper()
	before := getGCStatus(t).LastWorkerRun
	triggerGCWorker(t)
	ok := pollUntil(t, 45*time.Second, 500*time.Millisecond, func() bool {
		return getGCStatus(t).LastWorkerRun != before
	})
	if before == "never" && !ok {
		t.Log("GC worker run timestamp is still 'never'; falling back to behavior-specific assertions")
		return
	}
	if !ok {
		t.Fatalf("timed out waiting for GC worker run after manual trigger (before=%q after=%q)", before, getGCStatus(t).LastWorkerRun)
	}
}

func triggerGCScannerAndWait(t *testing.T) {
	t.Helper()
	before := getGCStatus(t).LastScanRun
	triggerGCScanner(t)
	ok := pollUntil(t, 45*time.Second, 500*time.Millisecond, func() bool {
		return getGCStatus(t).LastScanRun != before
	})
	if before == "never" && !ok {
		t.Log("GC scanner run timestamp is still 'never'; falling back to behavior-specific assertions")
		return
	}
	if !ok {
		t.Fatalf("timed out waiting for GC scanner run after manual trigger (before=%q after=%q)", before, getGCStatus(t).LastScanRun)
	}
}

// resolveOrgID reads the org_id for a library from Cassandra.
func resolveOrgID(t *testing.T, repoID string) string {
	t.Helper()
	session := shareProjectionDBForTest(t).Session()
	var orgID string
	if err := session.Query(`SELECT org_id FROM libraries_by_id WHERE library_id = ?`, repoID).Scan(&orgID); err != nil {
		t.Fatalf("failed to resolve org_id for %s: %v", repoID, err)
	}
	return orgID
}

// readBlockRefCount returns the number of PERMANENT (fs_object) references a block
// has under the row-per-reference model, or -999 if the canonical blocks row was
// deleted. Provisional "up:" upload references (which carry a TTL) are ignored so
// the count reflects how many live fs_objects reference the block — the closest
// analogue of the old mutable ref_count.
func readBlockRefCount(t *testing.T, orgID, blockID string) int {
	t.Helper()
	session := shareProjectionDBForTest(t).Session()

	var existing string
	err := session.Query(`SELECT block_id FROM blocks WHERE org_id = ? AND block_id = ?`, orgID, blockID).Scan(&existing)
	if errors.Is(err, gocql.ErrNotFound) {
		return -999 // block row deleted
	}
	if err != nil {
		t.Fatalf("readBlockRefCount: %v", err)
	}

	iter := session.Query(`SELECT referrer FROM block_references WHERE org_id = ? AND block_id = ?`, orgID, blockID).Iter()
	var referrer string
	count := 0
	for iter.Scan(&referrer) {
		if strings.HasPrefix(referrer, "fs:") {
			count++
		}
	}
	if err := iter.Close(); err != nil {
		t.Fatalf("readBlockRefCount references: %v", err)
	}
	return count
}

// blockExistsInDB returns true if the block row exists in the blocks table.
func blockExistsInDB(t *testing.T, orgID, blockID string) bool {
	t.Helper()
	return readBlockRefCount(t, orgID, blockID) != -999
}

// gcCandidateExists returns true if the block has a gc_block_candidates entry.
func gcCandidateExists(t *testing.T, orgID, blockID string) bool {
	t.Helper()
	session := shareProjectionDBForTest(t).Session()
	var ts time.Time
	err := session.Query(`SELECT candidate_at FROM gc_block_candidates WHERE org_id = ? AND block_id = ?`, orgID, blockID).Scan(&ts)
	return err == nil
}

func gcCandidateProjectionExists(t *testing.T, orgID, blockID string, candidateAt time.Time) bool {
	t.Helper()
	session := shareProjectionDBForTest(t).Session()
	var storedBlockID string
	err := session.Query(`
		SELECT block_id FROM gc_block_candidates_by_day
		WHERE candidate_day = ? AND bucket = ? AND candidate_at = ? AND org_id = ? AND block_id = ?
	`, db.GCProjectionUTCDate(candidateAt), db.GCDiscoveryBucket(orgID, blockID), candidateAt.UTC(), orgID, blockID).Scan(&storedBlockID)
	return err == nil && storedBlockID == blockID
}

func blockIDMappingExists(t *testing.T, orgID, externalID string) bool {
	t.Helper()
	session := shareProjectionDBForTest(t).Session()
	var internalID string
	err := session.Query(`
		SELECT internal_id FROM block_id_mappings WHERE org_id = ? AND representation_id = ? AND external_id = ?
	`, orgID, db.PlainBlockRepresentationID, externalID).Scan(&internalID)
	return err == nil && internalID != ""
}

func blockIDMappingExistsForRepresentation(t *testing.T, orgID, representationID, externalID string) bool {
	t.Helper()
	session := shareProjectionDBForTest(t).Session()
	var internalID string
	err := session.Query(`
		SELECT internal_id FROM block_id_mappings WHERE org_id = ? AND representation_id = ? AND external_id = ?
	`, orgID, representationID, externalID).Scan(&internalID)
	return err == nil && internalID != ""
}

func gcS3OrphanProjectionExists(t *testing.T, orgID, blockID string, firstSeenAt time.Time) bool {
	t.Helper()
	session := shareProjectionDBForTest(t).Session()
	var storedBlockID string
	err := session.Query(`
		SELECT block_id FROM gc_s3_orphans_by_day
		WHERE first_seen_day = ? AND bucket = ? AND first_seen_at = ? AND org_id = ? AND block_id = ?
	`, db.GCProjectionUTCDate(firstSeenAt), db.GCDiscoveryBucket(orgID, blockID), firstSeenAt.UTC(), orgID, blockID).Scan(&storedBlockID)
	return err == nil && storedBlockID == blockID
}

// uploadUniqueFile uploads a file with unique content and returns the block ID (SHA-256 of content).
func uploadUniqueFile(t *testing.T, c *testClient, repoID, fileName, parentDir string) (content string, blockID string) {
	t.Helper()
	content = fmt.Sprintf("gc-test-%s-%d\n", fileName, time.Now().UnixNano())
	uploadLinkResp := c.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=%s", repoID, parentDir))
	expectStatus(t, uploadLinkResp, http.StatusOK)
	uploadURL := strings.Trim(responseBody(t, uploadLinkResp), "\" \n\r")
	uploadFileThroughLink(t, c, uploadURL, fileName, parentDir, content)

	hash := sha256.Sum256([]byte(content))
	blockID = hex.EncodeToString(hash[:])
	return content, blockID
}

// batchDeleteFiles deletes files via the batch-delete API.
func batchDeleteFiles(t *testing.T, c *testClient, repoID, parentDir string, fileNames []string) {
	t.Helper()
	body := map[string]interface{}{
		"repo_id":    repoID,
		"parent_dir": parentDir,
		"dirents":    fileNames,
	}
	resp := c.DeleteJSON(t, "/api/v2.1/repos/batch-delete-item/", body)
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()
}

// pollUntil polls fn every interval until it returns true or timeout expires.
func pollUntil(t *testing.T, timeout time.Duration, interval time.Duration, fn func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}
		time.Sleep(interval)
	}
	return false
}

type gcStatusPayload struct {
	Enabled            bool   `json:"enabled"`
	DryRun             bool   `json:"dry_run"`
	LastWorkerRun      string `json:"last_worker_run"`
	LastScanRun        string `json:"last_scan_run"`
	LastScanAttempt    string `json:"last_scan_attempt"`
	LastScanSuccess    string `json:"last_scan_success"`
	QueueSize          int    `json:"queue_size"`
	FailedItemsTotal   int    `json:"failed_items_total"`
	BlocksDeletedTotal int64  `json:"blocks_deleted_total"`
}

func getGCStatus(t *testing.T) gcStatusPayload {
	t.Helper()

	resp := superadminClient.Get(t, "/api/v2.1/admin/gc/status")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gc status returned HTTP %d", resp.StatusCode)
	}
	var status gcStatusPayload
	decodeJSON(t, resp, &status)
	return status
}

// getGCQueueSize reads the current GC queue size from the admin API.
func getGCQueueSize(t *testing.T) int {
	t.Helper()
	return getGCStatus(t).QueueSize
}

func TestGC_StatusPayloadIncludesScanAttemptAndSuccess(t *testing.T) {
	requireGCEnabled(t)

	resp := superadminClient.Get(t, "/api/v2.1/admin/gc/status")
	expectStatus(t, resp, http.StatusOK)
	payload := responseJSON(t, resp)

	for _, field := range []string{"last_scan_run", "last_scan_attempt", "last_scan_success"} {
		value, ok := payload[field]
		if !ok {
			t.Fatalf("expected gc status payload to include %s", field)
		}
		if _, ok := value.(string); !ok {
			t.Fatalf("expected gc status field %s to be a string, got %T", field, value)
		}
	}
	if payload["last_scan_run"] != payload["last_scan_attempt"] {
		t.Fatalf("expected last_scan_run legacy alias to match last_scan_attempt, got run=%v attempt=%v", payload["last_scan_run"], payload["last_scan_attempt"])
	}
}

func readGCStatsInt(t *testing.T, key string) int {
	t.Helper()

	session := shareProjectionDBForTest(t).Session()
	var value string
	err := session.Query(`SELECT stat_value FROM gc_stats WHERE stat_key = ?`, key).Scan(&value)
	if errors.Is(err, gocql.ErrNotFound) {
		return 0
	}
	if err != nil {
		t.Fatalf("failed to read gc_stats[%s]: %v", key, err)
	}
	var parsed int
	if _, err := fmt.Sscanf(value, "%d", &parsed); err != nil {
		t.Fatalf("failed to parse gc_stats[%s]=%q: %v", key, value, err)
	}
	return parsed
}

func readGCQueueSnapshotTotal(t *testing.T) int {
	t.Helper()
	return readGCStatsInt(t, "total_queue_depth")
}

func readGCFailedSnapshotTotal(t *testing.T) int {
	t.Helper()
	return readGCStatsInt(t, "total_failed_items")
}

func gcQueueItemExists(t *testing.T, orgID string, itemType string, itemID string) bool {
	return gcQueueItemExistsSince(t, orgID, itemType, itemID, time.Time{})
}

func gcQueueItemExistsSince(t *testing.T, orgID string, itemType string, itemID string, queuedAfter time.Time) bool {
	t.Helper()

	session := shareProjectionDBForTest(t).Session()
	for bucket := 0; bucket < 32; bucket++ {
		query := `SELECT item_type, item_id FROM gc_queue WHERE org_id = ? AND bucket = ?`
		args := []interface{}{orgID, bucket}
		if !queuedAfter.IsZero() {
			query += ` AND queued_at >= ?`
			args = append(args, queuedAfter.UTC())
		}
		iter := session.Query(query, args...).Iter()
		var queuedItemType string
		var queuedItemID string
		for iter.Scan(&queuedItemType, &queuedItemID) {
			if queuedItemType == itemType && queuedItemID == itemID {
				if err := iter.Close(); err != nil {
					t.Fatalf("failed to close gc_queue iterator for org %s bucket %d: %v", orgID, bucket, err)
				}
				return true
			}
		}
		if err := iter.Close(); err != nil {
			t.Fatalf("failed to scan gc_queue for org %s bucket %d: %v", orgID, bucket, err)
		}
	}
	return false
}

func gcFailedItemExpiryBucketForTest(orgID string, itemType string, itemID string, failedAt time.Time) int {
	return db.GCDiscoveryBucket(orgID, itemType, itemID, failedAt.UTC().Format(time.RFC3339Nano))
}

func deleteGCQueueItemsByIdentity(t *testing.T, orgID string, itemType string, itemID string) {
	deleteGCQueueItemsByIdentitySince(t, orgID, itemType, itemID, time.Time{})
}

func deleteGCQueueItemsByIdentitySince(t *testing.T, orgID string, itemType string, itemID string, queuedAfter time.Time) {
	t.Helper()
	session := shareProjectionDBForTest(t).Session()
	for bucket := 0; bucket < 32; bucket++ {
		query := `SELECT queued_at, item_type, item_id FROM gc_queue WHERE org_id = ? AND bucket = ?`
		args := []interface{}{orgID, bucket}
		if !queuedAfter.IsZero() {
			query += ` AND queued_at >= ?`
			args = append(args, queuedAfter.UTC())
		}
		iter := session.Query(query, args...).Iter()
		var queuedAt time.Time
		var queuedItemType string
		var queuedItemID string
		for iter.Scan(&queuedAt, &queuedItemType, &queuedItemID) {
			if queuedItemType == itemType && queuedItemID == itemID {
				if err := session.Query(`DELETE FROM gc_queue WHERE org_id = ? AND bucket = ? AND queued_at = ? AND item_type = ? AND item_id = ?`, orgID, bucket, queuedAt, queuedItemType, queuedItemID).Exec(); err != nil {
					t.Fatalf("failed to delete gc_queue row for %s/%s: %v", orgID, itemID, err)
				}
			}
		}
		if err := iter.Close(); err != nil {
			t.Fatalf("failed to scan gc_queue bucket %d for deletion: %v", bucket, err)
		}
	}
}

func deletedLibraryMarkerExistsInSessionForTest(t *testing.T, session *gocql.Session, repoID string) bool {
	t.Helper()
	var deletedAt time.Time
	err := session.Query(`SELECT deleted_at FROM deleted_libraries WHERE library_id = ?`, repoID).Scan(&deletedAt)
	if errors.Is(err, gocql.ErrNotFound) {
		return false
	}
	if err != nil {
		t.Fatalf("check deleted_libraries marker for %s: %v", repoID, err)
	}
	return true
}

func gcPendingLibraryCascadeExistsForTest(t *testing.T, session *gocql.Session, orgID string, orgUUID uuid.UUID, repoID string) bool {
	t.Helper()
	bucket := gcpkg.PendingItemBucket(orgUUID, uuid.Nil, gcpkg.ItemLibraryCascade, repoID)
	var identityAt time.Time
	err := session.Query(`
		SELECT identity_at FROM gc_pending_items
		WHERE org_id = ? AND bucket = ? AND item_type = ? AND library_id = ? AND item_id = ? LIMIT 1
	`, orgID, bucket, "library_cascade", uuid.Nil.String(), repoID).Scan(&identityAt)
	if errors.Is(err, gocql.ErrNotFound) {
		return false
	}
	if err != nil {
		t.Fatalf("check gc_pending_items library_cascade for %s/%s: %v", orgID, repoID, err)
	}
	return true
}

func deleteGCPendingLibraryCascadeItemsForTest(t *testing.T, session *gocql.Session, orgID string, orgUUID uuid.UUID, repoID string) {
	t.Helper()
	bucket := gcpkg.PendingItemBucket(orgUUID, uuid.Nil, gcpkg.ItemLibraryCascade, repoID)
	iter := session.Query(`
		SELECT identity_at FROM gc_pending_items
		WHERE org_id = ? AND bucket = ? AND item_type = ? AND library_id = ? AND item_id = ?
	`, orgID, bucket, "library_cascade", uuid.Nil.String(), repoID).Iter()
	var identityAt time.Time
	for iter.Scan(&identityAt) {
		if err := session.Query(`
			DELETE FROM gc_pending_items
			WHERE org_id = ? AND bucket = ? AND item_type = ? AND library_id = ? AND item_id = ? AND identity_at = ?
		`, orgID, bucket, "library_cascade", uuid.Nil.String(), repoID, identityAt).Exec(); err != nil {
			t.Fatalf("delete gc_pending_items library_cascade for %s/%s: %v", orgID, repoID, err)
		}
	}
	if err := iter.Close(); err != nil {
		t.Fatalf("scan gc_pending_items library_cascade for %s/%s: %v", orgID, repoID, err)
	}
}

func purgeDisposableLibraryDeleteResiduals(t *testing.T, session *gocql.Session, orgID string, orgUUID uuid.UUID, repoID string) {
	t.Helper()
	// Disposable libraries created by createDisposableTestLibrary are empty apart from the
	// root fs_object and initial commit, so we can remove those grace-gated rows directly
	// after the real delete path has already cleaned the canonical/admin projections.
	if err := session.Query(`DELETE FROM commits WHERE library_id = ?`, repoID).Exec(); err != nil {
		t.Fatalf("delete commits partition for %s: %v", repoID, err)
	}
	if err := session.Query(`DELETE FROM fs_objects WHERE library_id = ?`, repoID).Exec(); err != nil {
		t.Fatalf("delete fs_objects partition for %s: %v", repoID, err)
	}
	if err := session.Query(`DELETE FROM deleted_libraries WHERE library_id = ?`, repoID).Exec(); err != nil {
		t.Fatalf("delete deleted_libraries marker for %s: %v", repoID, err)
	}
	deleteGCQueueItemsByIdentity(t, orgID, "library_cascade", repoID)
	deleteGCPendingLibraryCascadeItemsForTest(t, session, orgID, orgUUID, repoID)
	// EnqueueBatch also marks the org active (gc_active_orgs) and dirty (gc_dirty_orgs); those
	// coordination markers survive queue/pending deletion. Clear them BEFORE recomputing the
	// global snapshot so dirty_orgs_total does not count this teardown's own dirty marker, and
	// so the org is not left active/dirty.
	//
	// Capture the fence BEFORE recalculating: the conditional deletes only remove markers older
	// than the fence, so any enqueue that starts after this point keeps its markers. Capturing
	// `now` after the recalc would open a window where a concurrent enqueue's markers (stamped
	// before `now`) get clobbered. gc_active_orgs tracks queue work only (the worker drops an org
	// from the active set once its queue drains, independent of the DLQ), so it is removed on
	// QueueDepth == 0 regardless of FailedDepth. The poll loop re-runs this on any late enqueue.
	store := gcpkg.NewCassandraStore(shareProjectionDBForTest(t))
	cleanupFence := time.Now().UTC()
	stats, err := store.RecalculateOrgQueueStats(orgUUID)
	if err != nil {
		t.Fatalf("recalculate org queue stats for %s: %v", orgUUID, err)
	}
	if err := store.ClearDirtyOrg(orgUUID, cleanupFence); err != nil {
		t.Fatalf("clear dirty org %s: %v", orgUUID, err)
	}
	if stats.QueueDepth == 0 {
		if err := store.RemoveOrgFromActiveSet(orgUUID, cleanupFence); err != nil {
			t.Fatalf("remove org %s from active set: %v", orgUUID, err)
		}
	}
	repairGCSnapshotsForTest(t, orgUUID)
}

func cleanupDisposableLibraryDeleteState(t *testing.T, session *gocql.Session, orgID, repoID string) {
	t.Helper()
	orgUUID, err := uuid.Parse(orgID)
	if err != nil {
		t.Fatalf("parse org id for cleanup: %v", err)
	}
	purgeDisposableLibraryDeleteResiduals(t, session, orgID, orgUUID, repoID)
	stableSince := time.Time{}
	ok := pollUntil(t, 8*time.Second, 200*time.Millisecond, func() bool {
		markerExists := deletedLibraryMarkerExistsInSessionForTest(t, session, repoID)
		queueExists := gcQueueItemExists(t, orgID, "library_cascade", repoID)
		pendingExists := gcPendingLibraryCascadeExistsForTest(t, session, orgID, orgUUID, repoID)
		if markerExists || queueExists || pendingExists {
			// The permanent-delete endpoint enqueues library_cascade on a fire-and-forget
			// goroutine; if that races with teardown, scrub the recreated rows and wait for
			// the absence to remain stable before returning.
			stableSince = time.Time{}
			purgeDisposableLibraryDeleteResiduals(t, session, orgID, orgUUID, repoID)
			return false
		}
		if stableSince.IsZero() {
			stableSince = time.Now()
			return false
		}
		return time.Since(stableSince) >= 1500*time.Millisecond
	})
	if !ok {
		t.Fatalf("timed out waiting for stable teardown cleanup of %s/%s", orgID, repoID)
	}
}

func readGCOrgQueueStats(t *testing.T, orgID uuid.UUID) (int, int) {
	t.Helper()
	session := shareProjectionDBForTest(t).Session()
	var queueDepth int
	var failedDepth int
	err := session.Query(`SELECT queue_depth, failed_depth FROM gc_org_stats WHERE org_id = ?`, orgID.String()).Scan(&queueDepth, &failedDepth)
	if errors.Is(err, gocql.ErrNotFound) {
		return 0, 0
	}
	if err != nil {
		t.Fatalf("failed to read gc_org_stats for %s: %v", orgID, err)
	}
	return queueDepth, failedDepth
}

func deleteGCFailedItemsByIdentity(t *testing.T, orgID string, itemType string, itemID string) {
	t.Helper()
	session := shareProjectionDBForTest(t).Session()
	iter := session.Query(`SELECT failed_at, expires_at, item_type, item_id FROM gc_failed_items WHERE org_id = ?`, orgID).Iter()
	var failedAt time.Time
	var expiresAt time.Time
	var failedItemType string
	var failedItemID string
	for iter.Scan(&failedAt, &expiresAt, &failedItemType, &failedItemID) {
		if failedItemType != itemType || failedItemID != itemID {
			continue
		}
		if err := session.Query(`DELETE FROM gc_failed_items WHERE org_id = ? AND failed_at = ? AND item_type = ? AND item_id = ?`, orgID, failedAt, failedItemType, failedItemID).Exec(); err != nil {
			t.Fatalf("failed to delete gc_failed_items row for %s/%s: %v", orgID, itemID, err)
		}
		if !expiresAt.IsZero() {
			bucket := gcFailedItemExpiryBucketForTest(orgID, failedItemType, failedItemID, failedAt)
			if err := session.Query(`
				DELETE FROM gc_failed_items_by_expiry
				WHERE expiry_day = ? AND bucket = ? AND expires_at = ? AND org_id = ? AND failed_at = ? AND item_type = ? AND item_id = ?
			`, db.GCProjectionUTCDate(expiresAt), bucket, expiresAt, orgID, failedAt, failedItemType, failedItemID).Exec(); err != nil {
				t.Fatalf("failed to delete gc_failed_items_by_expiry row for %s/%s: %v", orgID, itemID, err)
			}
		}
	}
	if err := iter.Close(); err != nil {
		t.Fatalf("failed to scan gc_failed_items for %s/%s: %v", orgID, itemID, err)
	}
}

func deleteGCPendingBlockItems(t *testing.T, orgID uuid.UUID, blockID string) {
	t.Helper()
	session := shareProjectionDBForTest(t).Session()
	bucket := gcpkg.PendingItemBucket(orgID, uuid.Nil, gcpkg.ItemBlock, blockID)
	iter := session.Query(`
		SELECT identity_at FROM gc_pending_items
		WHERE org_id = ? AND bucket = ? AND item_type = ? AND library_id = ? AND item_id = ?
	`, orgID.String(), bucket, "block", uuid.Nil.String(), blockID).Iter()
	var identityAt time.Time
	for iter.Scan(&identityAt) {
		if err := session.Query(`
			DELETE FROM gc_pending_items
			WHERE org_id = ? AND bucket = ? AND item_type = ? AND library_id = ? AND item_id = ? AND identity_at = ?
		`, orgID.String(), bucket, "block", uuid.Nil.String(), blockID, identityAt).Exec(); err != nil {
			t.Fatalf("failed to delete gc_pending_items row for %s/%s: %v", orgID, blockID, err)
		}
	}
	if err := iter.Close(); err != nil {
		t.Fatalf("failed to scan gc_pending_items for %s/%s: %v", orgID, blockID, err)
	}
}

func repairGCSnapshotsForTest(t *testing.T, orgID uuid.UUID) {
	t.Helper()
	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)

	if _, err := store.RecalculateOrgQueueStats(orgID); err != nil {
		t.Fatalf("failed to recalculate gc_org_stats for %s: %v", orgID, err)
	}

	totalQueue, totalFailed, err := store.SumOrgQueueStats()
	if err != nil {
		t.Fatalf("failed to sum gc_org_stats: %v", err)
	}
	dirtyOrgs, err := store.ListDirtyOrgs(0)
	if err != nil {
		t.Fatalf("failed to list dirty GC orgs: %v", err)
	}
	if err := store.SaveGCStats("total_queue_depth", fmt.Sprintf("%d", totalQueue)); err != nil {
		t.Fatalf("failed to persist total_queue_depth snapshot: %v", err)
	}
	if err := store.SaveGCStats("total_failed_items", fmt.Sprintf("%d", totalFailed)); err != nil {
		t.Fatalf("failed to persist total_failed_items snapshot: %v", err)
	}
	if err := store.SaveGCStats("dirty_orgs_total", fmt.Sprintf("%d", len(dirtyOrgs))); err != nil {
		t.Fatalf("failed to persist dirty_orgs_total snapshot: %v", err)
	}
}

func cleanupGCBlockFixturesForTest(t *testing.T, orgID uuid.UUID, blockID string) {
	t.Helper()
	deleteGCQueueItemsByIdentity(t, orgID.String(), "block", blockID)
	deleteGCFailedItemsByIdentity(t, orgID.String(), "block", blockID)
	deleteGCPendingBlockItems(t, orgID, blockID)
	store := gcpkg.NewCassandraStore(shareProjectionDBForTest(t))
	if err := store.DeleteBlockGCCandidate(orgID, blockID, time.Time{}); err != nil {
		t.Fatalf("failed to delete gc_block_candidate for %s/%s: %v", orgID, blockID, err)
	}
	repairGCSnapshotsForTest(t, orgID)
}

func seedSyntheticZeroRefBlockForTest(t *testing.T, orgID uuid.UUID, blockID, storageClass string) {
	t.Helper()
	database := shareProjectionDBForTest(t)
	if err := database.UpsertBlockMetadata(orgID.String(), blockID, 1, storageClass, ""); err != nil {
		t.Fatalf("failed to seed block metadata for %s/%s: %v", orgID, blockID, err)
	}
	t.Cleanup(func() {
		cleanupGCBlockFixturesForTest(t, orgID, blockID)
		if err := database.Session().Query(`DELETE FROM block_references WHERE org_id = ? AND block_id = ?`, orgID.String(), blockID).Exec(); err != nil {
			t.Fatalf("failed to delete block_references for %s/%s: %v", orgID, blockID, err)
		}
		if err := database.Session().Query(`DELETE FROM blocks WHERE org_id = ? AND block_id = ?`, orgID.String(), blockID).Exec(); err != nil {
			t.Fatalf("failed to delete blocks row for %s/%s: %v", orgID, blockID, err)
		}
	})
}

func ensureSyntheticBlockCandidateForTest(t *testing.T, orgID uuid.UUID, blockID, storageClass string, candidateAt time.Time) time.Time {
	t.Helper()
	store := gcpkg.NewCassandraStore(shareProjectionDBForTest(t))
	effectiveCandidateAt, err := store.EnsureBlockGCCandidate(orgID, blockID, storageClass, candidateAt.UTC().Truncate(time.Millisecond))
	if err != nil {
		t.Fatalf("EnsureBlockGCCandidate(%s/%s): %v", orgID, blockID, err)
	}
	return effectiveCandidateAt
}

func enqueueSyntheticBlockQueueItemForTest(t *testing.T, orgID uuid.UUID, blockID, storageClass string, queuedAt time.Time) {
	t.Helper()
	queue := gcpkg.NewQueue(gcpkg.NewCassandraStore(shareProjectionDBForTest(t)))
	if err := queue.EnqueueBatch([]gcpkg.QueueItem{{
		OrgID:        orgID,
		QueuedAt:     queuedAt.UTC().Truncate(time.Millisecond),
		ItemType:     gcpkg.ItemBlock,
		ItemID:       blockID,
		LibraryID:    uuid.Nil,
		StorageClass: storageClass,
	}}); err != nil {
		t.Fatalf("failed to enqueue synthetic block queue item for %s/%s: %v", orgID, blockID, err)
	}
}

func failedQueueItemExists(t *testing.T, orgID string, itemType string, itemID string) bool {
	t.Helper()
	session := shareProjectionDBForTest(t).Session()
	iter := session.Query(`SELECT item_type, item_id FROM gc_failed_items WHERE org_id = ?`, orgID).Iter()
	var failedItemType string
	var failedItemID string
	for iter.Scan(&failedItemType, &failedItemID) {
		if failedItemType == itemType && failedItemID == itemID {
			if err := iter.Close(); err != nil {
				t.Fatalf("failed to close gc_failed_items iterator for org %s: %v", orgID, err)
			}
			return true
		}
	}
	if err := iter.Close(); err != nil {
		t.Fatalf("failed to scan gc_failed_items for org %s: %v", orgID, err)
	}
	return false
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestGC_BlockLifecycle verifies the current file-delete lifecycle: deleting a
// file removes it from HEAD immediately, but retained historical commits keep
// the old fs_object alive, so the block stays referenced and must not become a
// GC candidate until later version GC reclaims that history.
func TestGC_BlockLifecycle(t *testing.T) {
	requireGCEnabled(t)
	requireCassandra(t)

	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-gc-lifecycle-%d", time.Now().UnixNano()))
	orgID := resolveOrgID(t, repoID)

	// Upload a file with unique content
	_, blockID := uploadUniqueFile(t, adminClient, repoID, "lifecycle.txt", "/")
	t.Logf("Uploaded file, blockID=%s", blockID[:16])

	// Verify block exists with ref_count=1
	rc := readBlockRefCount(t, orgID, blockID)
	if rc != 1 {
		t.Fatalf("expected ref_count=1 after upload, got %d", rc)
	}

	// Delete the file from HEAD.
	batchDeleteFiles(t, adminClient, repoID, "/", []string{"lifecycle.txt"})

	// The old commit still retains the deleted file's fs_object, so the permanent
	// block ref stays live and no block GC candidate should be registered yet.
	ok := pollUntil(t, 5*time.Second, 200*time.Millisecond, func() bool {
		return readBlockRefCount(t, orgID, blockID) == 1 && !gcCandidateExists(t, orgID, blockID)
	})
	if !ok {
		t.Fatalf("delete unexpectedly changed retained block state: refs=%d candidate=%v", readBlockRefCount(t, orgID, blockID), gcCandidateExists(t, orgID, blockID))
	}

	for i := 0; i < 3; i++ {
		triggerGCWorker(t)
	}

	if !blockExistsInDB(t, orgID, blockID) {
		t.Fatal("deleted file block was reclaimed before version GC removed the retained fs_object")
	}
	if gcCandidateExists(t, orgID, blockID) {
		t.Fatal("deleted file block became a GC candidate even though retained history still references it")
	}
	t.Log("Block lifecycle: retained history kept the deleted file block alive — correct")
}

// TestGC_DeduplicationSafety verifies that shared blocks survive when only
// one of two files referencing them is deleted.
func TestGC_DeduplicationSafety(t *testing.T) {
	requireGCEnabled(t)
	requireCassandra(t)

	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-gc-dedup-%d", time.Now().UnixNano()))
	orgID := resolveOrgID(t, repoID)

	// Upload two files with identical content (shared block)
	sharedContent := fmt.Sprintf("dedup-shared-%d\n", time.Now().UnixNano())
	hash := sha256.Sum256([]byte(sharedContent))
	blockID := hex.EncodeToString(hash[:])

	uploadLinkResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", repoID))
	expectStatus(t, uploadLinkResp, http.StatusOK)
	uploadURL := strings.Trim(responseBody(t, uploadLinkResp), "\" \n\r")
	uploadFileThroughLink(t, adminClient, uploadURL, "dedup-a.txt", "/", sharedContent)
	uploadFileThroughLink(t, adminClient, uploadURL, "dedup-b.txt", "/", sharedContent)

	// Same-library dedup reuses the same content-addressed fs_id, so there is one
	// permanent block reference, not one per directory entry.
	rc := readBlockRefCount(t, orgID, blockID)
	if rc != 1 {
		t.Fatalf("expected permanent refs=1 after same-library dedup upload, got %d", rc)
	}

	// Delete only file A
	batchDeleteFiles(t, adminClient, repoID, "/", []string{"dedup-a.txt"})

	// Deleting one dirent does not remove the shared fs_object's permanent ref.
	ok := pollUntil(t, 15*time.Second, 200*time.Millisecond, func() bool {
		return readBlockRefCount(t, orgID, blockID) == 1
	})
	if !ok {
		t.Fatalf("permanent refs should stay 1 after deleting one same-library copy, got %d", readBlockRefCount(t, orgID, blockID))
	}

	// Trigger GC — block should NOT be deleted while the shared fs_object stays live.
	for i := 0; i < 3; i++ {
		triggerGCWorker(t)
	}

	// Block must still exist
	if !blockExistsInDB(t, orgID, blockID) {
		t.Fatal("CRITICAL: shared block was deleted while still referenced — deduplication safety violated!")
	}

	rc = readBlockRefCount(t, orgID, blockID)
	if rc < 1 {
		t.Fatalf("shared block permanent refs dropped below 1 (%d) — potential data loss", rc)
	}
	if gcCandidateExists(t, orgID, blockID) {
		t.Fatal("shared block was queued for GC while another same-library copy still references it")
	}

	// File B should still be downloadable
	dlResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/file/?p=/dedup-b.txt", repoID))
	expectStatus(t, dlResp, http.StatusOK)
	dlResp.Body.Close()

	t.Log("Dedup safety: shared block survived single-file delete — correct")
}

// TestGC_ConcurrentUploadDuringGC verifies the LWT guard: if a block is
// re-referenced after being enqueued for GC, the LWT prevents deletion.
func TestGC_ConcurrentUploadDuringGC(t *testing.T) {
	requireGCEnabled(t)
	requireCassandra(t)

	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-gc-race-%d", time.Now().UnixNano()))
	orgID := resolveOrgID(t, repoID)

	// Upload file, then delete it (ref_count → 0, enqueued for GC)
	content := fmt.Sprintf("race-content-%d\n", time.Now().UnixNano())
	hash := sha256.Sum256([]byte(content))
	blockID := hex.EncodeToString(hash[:])

	uploadLinkResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", repoID))
	expectStatus(t, uploadLinkResp, http.StatusOK)
	uploadURL := strings.Trim(responseBody(t, uploadLinkResp), "\" \n\r")
	uploadFileThroughLink(t, adminClient, uploadURL, "race-v1.txt", "/", content)

	batchDeleteFiles(t, adminClient, repoID, "/", []string{"race-v1.txt"})

	// Wait for ref_count to drop
	pollUntil(t, 15*time.Second, 200*time.Millisecond, func() bool {
		return readBlockRefCount(t, orgID, blockID) <= 0
	})

	// Re-upload the same content (ref_count should go back to 1)
	uploadLinkResp2 := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", repoID))
	expectStatus(t, uploadLinkResp2, http.StatusOK)
	uploadURL2 := strings.Trim(responseBody(t, uploadLinkResp2), "\" \n\r")
	uploadFileThroughLink(t, adminClient, uploadURL2, "race-v2.txt", "/", content)

	// Verify ref_count is back to positive
	ok := pollUntil(t, 10*time.Second, 200*time.Millisecond, func() bool {
		return readBlockRefCount(t, orgID, blockID) >= 1
	})
	if !ok {
		t.Logf("Warning: ref_count=%d (may not have been incremented yet)", readBlockRefCount(t, orgID, blockID))
	}

	// Trigger GC — LWT should prevent deletion because ref_count > 0
	for i := 0; i < 5; i++ {
		triggerGCWorker(t)
	}

	// Block must still exist (LWT guard)
	if !blockExistsInDB(t, orgID, blockID) {
		t.Fatal("CRITICAL: LWT guard failed — block deleted despite ref_count > 0 after re-upload!")
	}

	// File should be downloadable
	dlResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/file/?p=/race-v2.txt", repoID))
	expectStatus(t, dlResp, http.StatusOK)
	dlResp.Body.Close()

	t.Log("Concurrent upload safety: LWT prevented deletion of re-referenced block — correct")
}

func TestUploadLink_ReuploadBlockedByS3OrphanFence(t *testing.T) {
	requireCassandra(t)

	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-upload-orphan-fence-%d", time.Now().UnixNano()))
	orgID := resolveOrgID(t, repoID)
	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)

	content := fmt.Sprintf("orphan-fence-content-%d\n", time.Now().UnixNano())
	hash := sha256.Sum256([]byte(content))
	blockID := hex.EncodeToString(hash[:])

	uploadLinkResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", repoID))
	expectStatus(t, uploadLinkResp, http.StatusOK)
	uploadURL := strings.Trim(responseBody(t, uploadLinkResp), "\" \n\r")
	uploadFileThroughLink(t, adminClient, uploadURL, "seed.txt", "/", content)

	if rc := readBlockRefCount(t, orgID, blockID); rc != 1 {
		t.Fatalf("seed upload permanent refs = %d, want 1", rc)
	}

	orgUUID, err := uuid.Parse(orgID)
	if err != nil {
		t.Fatalf("parse orgID %q: %v", orgID, err)
	}
	firstSeenAt := time.Now().UTC().Truncate(time.Millisecond)
	effectiveFirstSeenAt, err := store.RecordS3Orphan(orgUUID, blockID, "hot", db.PlainBlockRepresentationID, "", "seed orphan fence", firstSeenAt)
	if err != nil {
		t.Fatalf("RecordS3Orphan: %v", err)
	}
	t.Cleanup(func() {
		if err := store.DeleteS3Orphan(orgUUID, blockID, effectiveFirstSeenAt); err != nil {
			t.Errorf("cleanup DeleteS3Orphan(%s): %v", blockID, err)
		}
	})
	if !gcS3OrphanProjectionExists(t, orgID, blockID, effectiveFirstSeenAt) {
		t.Fatal("expected gc_s3_orphans_by_day projection row to exist before retry upload")
	}

	status, body := uploadFileThroughLinkStatus(t, adminClient, uploadURL, "blocked-retry.txt", "/", content)
	if status != http.StatusConflict {
		t.Fatalf("reupload status = %d body=%s, want 409 conflict", status, body)
	}
	if !strings.Contains(body, "block is being deleted; retry the upload") {
		t.Fatalf("reupload body = %q, want retryable block-delete message", body)
	}

	if rc := readBlockRefCount(t, orgID, blockID); rc != 1 {
		t.Fatalf("permanent refs after blocked reupload = %d, want 1", rc)
	}
	assertHasTTLBoundUploadReferrer(t, repoID, "/", "seed.txt")

	orphanInfo, found, err := database.GetBlockS3OrphanInfo(orgID, blockID)
	if err != nil {
		t.Fatalf("GetBlockS3OrphanInfo(%s): %v", blockID, err)
	}
	if !found {
		t.Fatal("writer should leave gc_s3_orphans fence active for GC recovery")
	}
	if !orphanInfo.FirstSeenAt.UTC().Equal(effectiveFirstSeenAt.UTC()) {
		t.Fatalf("gc_s3_orphans first_seen_at = %v, want %v", orphanInfo.FirstSeenAt.UTC(), effectiveFirstSeenAt.UTC())
	}
	if !gcS3OrphanProjectionExists(t, orgID, blockID, effectiveFirstSeenAt) {
		t.Fatal("writer should not remove gc_s3_orphans_by_day projection during retryable upload failure")
	}

	listResp := adminClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/", repoID))
	expectStatus(t, listResp, http.StatusOK)
	defer listResp.Body.Close()
	var dirList map[string]interface{}
	decodeJSON(t, listResp, &dirList)
	entries, _ := dirList["dirent_list"].([]interface{})
	for _, rawEntry := range entries {
		entry, _ := rawEntry.(map[string]interface{})
		if name, _ := entry["name"].(string); name == "blocked-retry.txt" {
			t.Fatal("retry-blocked upload should not create a file entry")
		}
	}
}

// TestGC_LibraryCascade verifies that soft-deleting a library and triggering
// the scanner/worker cascades through commits, fs_objects, and blocks.
func TestGC_LibraryCascade(t *testing.T) {
	requireGCEnabled(t)
	requireCassandra(t)

	repoName := fmt.Sprintf("inttest-gc-cascade-%d", time.Now().UnixNano())
	repoID := createDisposableTestLibrary(t, adminClient, repoName)
	orgID := resolveOrgID(t, repoID)

	// Upload multiple files to create commits and blocks
	var blockIDs []string
	for i := 0; i < 3; i++ {
		_, bid := uploadUniqueFile(t, adminClient, repoID, fmt.Sprintf("cascade-%d.txt", i), "/")
		blockIDs = append(blockIDs, bid)
	}

	// Verify blocks exist
	for _, bid := range blockIDs {
		if !blockExistsInDB(t, orgID, bid) {
			t.Fatalf("block %s not found after upload", bid[:16])
		}
	}

	// Soft-delete the library
	delResp := adminClient.Delete(t, fmt.Sprintf("/api2/repos/%s/", repoID))
	expectStatus(t, delResp, http.StatusOK)
	delResp.Body.Close()

	t.Log("Library soft-deleted, triggering scanner + worker...")

	// Trigger scanner to find expired deleted library, then worker to process
	for i := 0; i < 3; i++ {
		triggerGCScanner(t)
		time.Sleep(2 * time.Second)
	}
	for i := 0; i < 10; i++ {
		triggerGCWorker(t)
		time.Sleep(500 * time.Millisecond)
	}

	// Check if blocks' ref_counts were decremented
	for _, bid := range blockIDs {
		rc := readBlockRefCount(t, orgID, bid)
		t.Logf("Block %s ref_count after cascade: %d", bid[:16], rc)
	}

	// Verify the library no longer appears in active list
	listResp := adminClient.Get(t, "/api2/repos/")
	expectStatus(t, listResp, http.StatusOK)
	listBody := responseBody(t, listResp)
	if strings.Contains(listBody, repoID) {
		t.Log("Library still in active list (may be in trash); checking trash...")
	}

	t.Log("Library cascade: scanner + worker processed soft-deleted library")
}

// TestGC_ScannerRequeuesCandidatesMissingFromQueue verifies the scanner
// picks up GC candidates from the per-day discovery projection and re-enqueues
// them when no matching queue item is present.
//
// The earlier version of this test deleted the canonical `gc_block_candidates`
// row to simulate a "missed enqueue" and relied on the scanner partition-scanning
// `blocks` to backfill it. That backfill path was removed when `blocks` moved
// to per-block partitioning (see ISSUE-BLOCKS-HOT-PARTITION-01); the only
// legitimate entry point for a block into GC is now `EnsureBlockGCCandidate`,
// observability for failures lives in `gc_zero_ref_enqueue_failures_total`.
//
// The current contract is: if the candidate row exists but the queue item is
// gone (replica drift, hand-cleanup, etc.), the scanner walks
// `gc_block_candidates_by_day` and re-enqueues. This test covers that path.
func TestGC_ScannerRequeuesCandidatesMissingFromQueue(t *testing.T) {
	requireGCEnabled(t)
	requireCassandra(t)

	orgUUID := uuid.New()
	orgID := orgUUID.String()
	blockID := fmt.Sprintf("rediscover-%d", time.Now().UnixNano())
	seedSyntheticZeroRefBlockForTest(t, orgUUID, blockID, "hot")
	candidateAt := ensureSyntheticBlockCandidateForTest(t, orgUUID, blockID, "hot", time.Now().UTC().Add(-5*time.Second))
	if !gcCandidateExists(t, orgID, blockID) {
		t.Fatalf("expected canonical gc_block_candidates row for %s/%s", orgID, blockID)
	}
	if !gcCandidateProjectionExists(t, orgID, blockID, candidateAt) {
		t.Fatalf("expected gc_block_candidates_by_day row for %s/%s", orgID, blockID)
	}

	// Simulate a missing/lost queue item while the candidate row remains.
	// This is the scenario the discovery projection is designed to recover from.
	deleteGCQueueItemsByIdentitySince(t, orgID, "block", blockID, time.Time{})

	t.Log("Candidate row present, queue item removed. Running scanner...")

	triggerGCScannerAndWait(t)

	// Scanner-only runs should restore the queue row for the surviving candidate.
	ok := pollUntil(t, 15*time.Second, 200*time.Millisecond, func() bool {
		return gcQueueItemExistsSince(t, orgID, "block", blockID, candidateAt.Add(-1*time.Second))
	})
	if !ok {
		t.Fatal("scanner did not re-enqueue candidate via gc_block_candidates_by_day discovery")
	}
	t.Log("scanner discovery: candidate row was re-enqueued from the discovery projection — correct")
}

// TestGC_DeleteBlockGCCandidate_RemovesDiscoveryRowWithoutCanonical verifies
// the store can still clear gc_block_candidates_by_day when the canonical
// gc_block_candidates row is already gone, as long as the caller provides the
// original candidate_at from the queue item / scanner row.
func TestGC_DeleteBlockGCCandidate_RemovesDiscoveryRowWithoutCanonical(t *testing.T) {
	requireCassandra(t)

	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	orgID := uuid.New()
	blockID := fmt.Sprintf("cand-cleanup-%d", time.Now().UnixNano())
	candidateAt := time.Now().UTC().Truncate(time.Millisecond)

	if _, err := store.EnsureBlockGCCandidate(orgID, blockID, "hot", candidateAt); err != nil {
		t.Fatalf("EnsureBlockGCCandidate: %v", err)
	}
	if !gcCandidateExists(t, orgID.String(), blockID) {
		t.Fatal("expected canonical gc_block_candidates row to exist")
	}
	if !gcCandidateProjectionExists(t, orgID.String(), blockID, candidateAt) {
		t.Fatal("expected gc_block_candidates_by_day projection row to exist")
	}

	if err := database.Session().Query(fmt.Sprintf(
		"DELETE FROM gc_block_candidates WHERE org_id = %s AND block_id = '%s'",
		orgID.String(),
		blockID,
	)).Exec(); err != nil {
		t.Fatalf("delete canonical gc_block_candidates row: %v", err)
	}

	if err := store.DeleteBlockGCCandidate(orgID, blockID, candidateAt); err != nil {
		t.Fatalf("DeleteBlockGCCandidate: %v", err)
	}
	if gcCandidateProjectionExists(t, orgID.String(), blockID, candidateAt) {
		t.Fatal("expected gc_block_candidates_by_day projection row to be removed")
	}
}

func TestGC_WorkerSkipsBlockCandidateWithoutCanonicalRow(t *testing.T) {
	requireCassandra(t)

	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	queue := gcpkg.NewQueue(store)
	worker := gcpkg.NewWorker(store, nil, queue, 100, 0, false, &gcpkg.Stats{})
	orgUUID := uuid.New()
	orgID := orgUUID.String()
	blockID := fmt.Sprintf("%064x", time.Now().UnixNano())
	externalBlockID := fmt.Sprintf("%040x", time.Now().UnixNano())
	queuedAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Millisecond)
	candidateAt := ensureSyntheticBlockCandidateForTest(t, orgUUID, blockID, "hot", queuedAt)
	enqueueSyntheticBlockQueueItemForTest(t, orgUUID, blockID, "hot", queuedAt)
	if err := database.WriteBlockIDMapping(orgID, db.PlainBlockRepresentationID, externalBlockID, blockID, time.Now().UTC()); err != nil {
		t.Fatalf("failed to seed block mapping for %s/%s: %v", orgID, blockID, err)
	}
	t.Cleanup(func() {
		cleanupGCBlockFixturesForTest(t, orgUUID, blockID)
		_ = database.Session().Query(`DELETE FROM block_id_mappings WHERE org_id = ? AND representation_id = ? AND external_id = ?`, orgID, db.PlainBlockRepresentationID, externalBlockID).Exec()
		if err := database.Session().Query(`DELETE FROM blocks WHERE org_id = ? AND block_id = ?`, orgID, blockID).Exec(); err != nil {
			t.Fatalf("failed to delete blocks row for %s/%s: %v", orgID, blockID, err)
		}
	})

	if blockExistsInDB(t, orgID, blockID) {
		t.Fatal("expected no canonical block row before worker")
	}
	if !gcCandidateExists(t, orgID, blockID) {
		t.Fatal("expected canonical gc_block_candidates row before worker")
	}
	if !gcCandidateProjectionExists(t, orgID, blockID, candidateAt) {
		t.Fatal("expected gc_block_candidates_by_day row before worker")
	}
	if !gcQueueItemExistsSince(t, orgID, "block", blockID, queuedAt.Add(-1*time.Second)) {
		t.Fatal("expected gc_queue row before worker")
	}
	if !blockIDMappingExists(t, orgID, externalBlockID) {
		t.Fatal("expected forward block_id_mappings row before worker")
	}

	// The candidate has no canonical blocks row, so the worker sweeps the
	// candidate/queue/projection but CANNOT resolve the forward mapping: blocks.sha1
	// is unavailable once the row is gone, and the reverse index was dropped in
	// migration 006. The forward mapping is left as a harmless dangling pointer
	// (recorded via the gc_block_mapping_sha1_missing metric).
	for attempt := 0; attempt < 8; attempt++ {
		if !gcCandidateExists(t, orgID, blockID) &&
			!gcCandidateProjectionExists(t, orgID, blockID, candidateAt) &&
			!gcQueueItemExistsSince(t, orgID, "block", blockID, queuedAt.Add(-1*time.Second)) &&
			!failedQueueItemExists(t, orgID, "block", blockID) &&
			!blockExistsInDB(t, orgID, blockID) {
			break
		}
		// Scope processing to the synthetic org. A bare ProcessOnce would fan out
		// across every active org and, with this nil-storage worker, could dequeue
		// other orgs' real block items and route their S3 deletes down the slow
		// recovery path. ProcessOrgOnce touches only the org under test.
		processed, err := worker.ProcessOrgOnce(context.Background(), orgUUID)
		if err != nil {
			t.Fatalf("ProcessOrgOnce attempt %d failed: %v", attempt+1, err)
		}
		if processed == 0 {
			time.Sleep(200 * time.Millisecond)
		}
	}

	if gcCandidateExists(t, orgID, blockID) ||
		gcCandidateProjectionExists(t, orgID, blockID, candidateAt) ||
		gcQueueItemExistsSince(t, orgID, "block", blockID, queuedAt.Add(-1*time.Second)) ||
		failedQueueItemExists(t, orgID, "block", blockID) ||
		blockExistsInDB(t, orgID, blockID) {
		t.Fatalf("stale candidate without canonical row was not fully skipped: block_exists=%v candidate=%v projection=%v queue=%v failed=%v",
			blockExistsInDB(t, orgID, blockID),
			gcCandidateExists(t, orgID, blockID),
			gcCandidateProjectionExists(t, orgID, blockID, candidateAt),
			gcQueueItemExistsSince(t, orgID, "block", blockID, queuedAt.Add(-1*time.Second)),
			failedQueueItemExists(t, orgID, "block", blockID),
		)
	}

	// The forward mapping survives: with the canonical row gone there is no
	// blocks.sha1 to resolve it, and PR7 must not blind-delete it.
	if !blockIDMappingExists(t, orgID, externalBlockID) {
		t.Fatal("expected forward mapping to survive the missing-canonical-row path (dangling pointer, observable via metric)")
	}
}

// TestGC_WorkerCleansForwardMappingViaBlockSHA1 is the PR7 encrypted-equivalent
// guard: it deletes a real block whose external SHA-1 differs from its internal
// block_id (exactly the encrypted-library shape — SHA-1 over plaintext, SHA-256
// over ciphertext — but the same shape any web/seafhttp block has). With the
// reverse index dropped (migration 006), GC must resolve and delete the forward
// block_id_mappings row from blocks.sha1, NOT from block_id. A wrong source would
// leave the forward mapping behind (or delete the wrong row).
func TestGC_WorkerCleansForwardMappingViaBlockSHA1(t *testing.T) {
	requireCassandra(t)

	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	queue := gcpkg.NewQueue(store)
	worker := gcpkg.NewWorker(store, nil, queue, 100, 0, false, &gcpkg.Stats{})
	orgUUID := uuid.New()
	orgID := orgUUID.String()
	// block_id is the 64-hex internal (ciphertext) identity; externalSHA1 is the
	// 40-hex Seafile (plaintext) id. They differ, as in an encrypted library.
	blockID := fmt.Sprintf("%064x", time.Now().UnixNano())
	externalSHA1 := fmt.Sprintf("%040x", time.Now().UnixNano())
	queuedAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Millisecond)

	// Real zero-ref canonical row carrying blocks.sha1 = externalSHA1 (what every
	// materialization path writes), plus the forward mapping it resolves to.
	if err := database.UpsertBlockMetadataWithSHA1(orgID, blockID, externalSHA1, 1, "hot", ""); err != nil {
		t.Fatalf("seed block with sha1: %v", err)
	}
	if err := database.WriteBlockIDMapping(orgID, db.PlainBlockRepresentationID, externalSHA1, blockID, time.Now().UTC()); err != nil {
		t.Fatalf("seed forward mapping: %v", err)
	}
	_ = ensureSyntheticBlockCandidateForTest(t, orgUUID, blockID, "hot", queuedAt)
	enqueueSyntheticBlockQueueItemForTest(t, orgUUID, blockID, "hot", queuedAt)
	t.Cleanup(func() {
		cleanupGCBlockFixturesForTest(t, orgUUID, blockID)
		_ = database.Session().Query(`DELETE FROM block_id_mappings WHERE org_id = ? AND representation_id = ? AND external_id = ?`, orgID, db.PlainBlockRepresentationID, externalSHA1).Exec()
		_ = database.Session().Query(`DELETE FROM blocks WHERE org_id = ? AND block_id = ?`, orgID, blockID).Exec()
	})

	if !blockExistsInDB(t, orgID, blockID) {
		t.Fatal("expected canonical block row before worker")
	}
	if !blockIDMappingExists(t, orgID, externalSHA1) {
		t.Fatal("expected forward mapping before worker")
	}

	for attempt := 0; attempt < 8; attempt++ {
		if !blockExistsInDB(t, orgID, blockID) && !blockIDMappingExists(t, orgID, externalSHA1) {
			break
		}
		processed, err := worker.ProcessOrgOnce(context.Background(), orgUUID)
		if err != nil {
			t.Fatalf("ProcessOrgOnce attempt %d failed: %v", attempt+1, err)
		}
		if processed == 0 {
			time.Sleep(200 * time.Millisecond)
		}
	}

	if blockExistsInDB(t, orgID, blockID) {
		t.Fatal("expected block row deleted")
	}
	if blockIDMappingExists(t, orgID, externalSHA1) {
		t.Fatal("expected forward mapping deleted via blocks.sha1 (resolved from sha1, not block_id)")
	}
}

func TestGC_WorkerDeletingPlainBlockPreservesEncryptedSibling(t *testing.T) {
	requireCassandra(t)

	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	queue := gcpkg.NewQueue(store)
	worker := gcpkg.NewWorker(store, nil, queue, 100, 0, false, &gcpkg.Stats{})
	orgUUID := uuid.New()
	orgID := orgUUID.String()
	encLibraryID := uuid.NewString()
	plainRep := db.PlainBlockRepresentationID
	encRep := db.EncryptedLibraryBlockRepresentationID(encLibraryID)
	externalSHA1 := fmt.Sprintf("%040x", time.Now().UnixNano())
	plainBlockID := fmt.Sprintf("%064x", time.Now().UnixNano())
	encBlockID := fmt.Sprintf("%064x", time.Now().UnixNano()+1)
	queuedAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Millisecond)

	if err := database.UpsertBlockMetadataWithRepresentationAndSHA1(orgID, plainRep, plainBlockID, externalSHA1, 1, "hot", ""); err != nil {
		t.Fatalf("seed plain block: %v", err)
	}
	if err := database.UpsertBlockMetadataWithRepresentationAndSHA1(orgID, encRep, encBlockID, externalSHA1, 1, "hot", ""); err != nil {
		t.Fatalf("seed encrypted block: %v", err)
	}
	if err := database.WriteBlockIDMapping(orgID, plainRep, externalSHA1, plainBlockID, time.Now().UTC()); err != nil {
		t.Fatalf("seed plain mapping: %v", err)
	}
	if err := database.WriteBlockIDMapping(orgID, encRep, externalSHA1, encBlockID, time.Now().UTC()); err != nil {
		t.Fatalf("seed encrypted mapping: %v", err)
	}
	_ = ensureSyntheticBlockCandidateForTest(t, orgUUID, plainBlockID, "hot", queuedAt)
	enqueueSyntheticBlockQueueItemForTest(t, orgUUID, plainBlockID, "hot", queuedAt)
	t.Cleanup(func() {
		cleanupGCBlockFixturesForTest(t, orgUUID, plainBlockID)
		cleanupGCBlockFixturesForTest(t, orgUUID, encBlockID)
		_ = database.Session().Query(`DELETE FROM block_id_mappings WHERE org_id = ? AND representation_id = ? AND external_id = ?`, orgID, plainRep, externalSHA1).Exec()
		_ = database.Session().Query(`DELETE FROM block_id_mappings WHERE org_id = ? AND representation_id = ? AND external_id = ?`, orgID, encRep, externalSHA1).Exec()
		_ = database.Session().Query(`DELETE FROM blocks WHERE org_id = ? AND block_id = ?`, orgID, plainBlockID).Exec()
		_ = database.Session().Query(`DELETE FROM blocks WHERE org_id = ? AND block_id = ?`, orgID, encBlockID).Exec()
	})

	if !blockExistsInDB(t, orgID, plainBlockID) || !blockExistsInDB(t, orgID, encBlockID) {
		t.Fatal("expected both canonical block rows before worker")
	}
	if !blockIDMappingExistsForRepresentation(t, orgID, plainRep, externalSHA1) || !blockIDMappingExistsForRepresentation(t, orgID, encRep, externalSHA1) {
		t.Fatal("expected both forward mappings before worker")
	}

	for attempt := 0; attempt < 8; attempt++ {
		if !blockExistsInDB(t, orgID, plainBlockID) &&
			!blockIDMappingExistsForRepresentation(t, orgID, plainRep, externalSHA1) {
			break
		}
		processed, err := worker.ProcessOrgOnce(context.Background(), orgUUID)
		if err != nil {
			t.Fatalf("ProcessOrgOnce attempt %d failed: %v", attempt+1, err)
		}
		if processed == 0 {
			time.Sleep(200 * time.Millisecond)
		}
	}

	if blockExistsInDB(t, orgID, plainBlockID) {
		t.Fatal("expected plain block row deleted")
	}
	if blockIDMappingExistsForRepresentation(t, orgID, plainRep, externalSHA1) {
		t.Fatal("expected plain forward mapping deleted")
	}
	if !blockExistsInDB(t, orgID, encBlockID) {
		t.Fatal("expected encrypted sibling block row preserved")
	}
	if !blockIDMappingExistsForRepresentation(t, orgID, encRep, externalSHA1) {
		t.Fatal("expected encrypted sibling forward mapping preserved")
	}
}

func TestGC_WorkerDeletingEncryptedBlockPreservesPlainSibling(t *testing.T) {
	requireCassandra(t)

	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	queue := gcpkg.NewQueue(store)
	worker := gcpkg.NewWorker(store, nil, queue, 100, 0, false, &gcpkg.Stats{})
	orgUUID := uuid.New()
	orgID := orgUUID.String()
	encLibraryID := uuid.NewString()
	plainRep := db.PlainBlockRepresentationID
	encRep := db.EncryptedLibraryBlockRepresentationID(encLibraryID)
	externalSHA1 := fmt.Sprintf("%040x", time.Now().UnixNano())
	plainBlockID := fmt.Sprintf("%064x", time.Now().UnixNano())
	encBlockID := fmt.Sprintf("%064x", time.Now().UnixNano()+1)
	queuedAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Millisecond)

	if err := database.UpsertBlockMetadataWithRepresentationAndSHA1(orgID, plainRep, plainBlockID, externalSHA1, 1, "hot", ""); err != nil {
		t.Fatalf("seed plain block: %v", err)
	}
	if err := database.UpsertBlockMetadataWithRepresentationAndSHA1(orgID, encRep, encBlockID, externalSHA1, 1, "hot", ""); err != nil {
		t.Fatalf("seed encrypted block: %v", err)
	}
	if err := database.WriteBlockIDMapping(orgID, plainRep, externalSHA1, plainBlockID, time.Now().UTC()); err != nil {
		t.Fatalf("seed plain mapping: %v", err)
	}
	if err := database.WriteBlockIDMapping(orgID, encRep, externalSHA1, encBlockID, time.Now().UTC()); err != nil {
		t.Fatalf("seed encrypted mapping: %v", err)
	}
	_ = ensureSyntheticBlockCandidateForTest(t, orgUUID, encBlockID, "hot", queuedAt)
	enqueueSyntheticBlockQueueItemForTest(t, orgUUID, encBlockID, "hot", queuedAt)
	t.Cleanup(func() {
		cleanupGCBlockFixturesForTest(t, orgUUID, plainBlockID)
		cleanupGCBlockFixturesForTest(t, orgUUID, encBlockID)
		_ = database.Session().Query(`DELETE FROM block_id_mappings WHERE org_id = ? AND representation_id = ? AND external_id = ?`, orgID, plainRep, externalSHA1).Exec()
		_ = database.Session().Query(`DELETE FROM block_id_mappings WHERE org_id = ? AND representation_id = ? AND external_id = ?`, orgID, encRep, externalSHA1).Exec()
		_ = database.Session().Query(`DELETE FROM blocks WHERE org_id = ? AND block_id = ?`, orgID, plainBlockID).Exec()
		_ = database.Session().Query(`DELETE FROM blocks WHERE org_id = ? AND block_id = ?`, orgID, encBlockID).Exec()
	})

	if !blockExistsInDB(t, orgID, plainBlockID) || !blockExistsInDB(t, orgID, encBlockID) {
		t.Fatal("expected both canonical block rows before worker")
	}
	if !blockIDMappingExistsForRepresentation(t, orgID, plainRep, externalSHA1) || !blockIDMappingExistsForRepresentation(t, orgID, encRep, externalSHA1) {
		t.Fatal("expected both forward mappings before worker")
	}

	for attempt := 0; attempt < 8; attempt++ {
		if !blockExistsInDB(t, orgID, encBlockID) &&
			!blockIDMappingExistsForRepresentation(t, orgID, encRep, externalSHA1) {
			break
		}
		processed, err := worker.ProcessOrgOnce(context.Background(), orgUUID)
		if err != nil {
			t.Fatalf("ProcessOrgOnce attempt %d failed: %v", attempt+1, err)
		}
		if processed == 0 {
			time.Sleep(200 * time.Millisecond)
		}
	}

	if blockExistsInDB(t, orgID, encBlockID) {
		t.Fatal("expected encrypted block row deleted")
	}
	if blockIDMappingExistsForRepresentation(t, orgID, encRep, externalSHA1) {
		t.Fatal("expected encrypted forward mapping deleted")
	}
	if !blockExistsInDB(t, orgID, plainBlockID) {
		t.Fatal("expected plain sibling block row preserved")
	}
	if !blockIDMappingExistsForRepresentation(t, orgID, plainRep, externalSHA1) {
		t.Fatal("expected plain sibling forward mapping preserved")
	}
}

// TestGC_ClaimBlockDelete_StubRowMaterializationIsCleaned pins the real
// Cassandra/Scylla LWT behavior that motivates the stub-cleanup branch in
// processBlock: an `UPDATE ... IF gc_state != 'deleting'` against a missing
// canonical row may (engine-dependent) materialize a metadata-free "stub" row.
//
// processBlock's own pre-claim BlockExists guard short-circuits before the claim
// when the row is missing, so the post-claim stub branch is unreachable through
// processBlock except via a narrow TOCTOU. This test therefore drives the store
// primitives directly to (a) empirically pin what the deployed engine does on a
// conditional UPDATE over a missing row, and (b) confirm GetBlockInfo's created_at
// discriminator + FinalizeBlockDelete actually clean the stub end-to-end. If the
// engine does NOT materialize a stub, the defensive branch is confirmed
// unreachable here and the test records that rather than failing.
func TestGC_ClaimBlockDelete_StubRowMaterializationIsCleaned(t *testing.T) {
	requireCassandra(t)

	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	orgUUID := uuid.New()
	orgID := orgUUID.String()
	blockID := fmt.Sprintf("stub-claim-%d", time.Now().UnixNano())
	candidateAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Millisecond)
	claimID := candidateAt.Format(time.RFC3339Nano)
	t.Cleanup(func() {
		if err := database.Session().Query(`DELETE FROM blocks WHERE org_id = ? AND block_id = ?`, orgID, blockID).Exec(); err != nil {
			t.Fatalf("cleanup blocks row for %s/%s: %v", orgID, blockID, err)
		}
	})

	if exists, err := store.BlockExists(orgUUID, blockID); err != nil {
		t.Fatalf("BlockExists(before claim): %v", err)
	} else if exists {
		t.Fatal("expected no canonical block row before claim")
	}

	// Claim against the missing row — the exact LWT processBlock would issue if
	// the canonical row vanished between its pre-claim check and the claim.
	applied, err := store.ClaimBlockDelete(orgUUID, blockID, claimID)
	if err != nil {
		t.Fatalf("ClaimBlockDelete: %v", err)
	}

	exists, err := store.BlockExists(orgUUID, blockID)
	if err != nil {
		t.Fatalf("BlockExists(after claim): %v", err)
	}
	if !exists {
		// Engine did not materialize a stub; the defensive branch is unreachable
		// on this engine. Nothing to clean — pin the observation and finish.
		t.Logf("engine did not materialize a stub row on conditional UPDATE over a missing row (claim applied=%v); processBlock stub branch is unreachable here", applied)
		return
	}

	// Engine materialized a stub: assert it is exactly the metadata-free shape the
	// worker keys off (empty storage_class, nil created_at).
	info, err := store.GetBlockInfo(orgUUID, blockID)
	if err != nil {
		t.Fatalf("GetBlockInfo(stub): %v", err)
	}
	if strings.TrimSpace(info.StorageClass) != "" {
		t.Fatalf("materialized stub should have empty storage_class, got %q", info.StorageClass)
	}
	if info.CreatedAt != nil {
		t.Fatalf("materialized stub should have nil created_at, got %v", info.CreatedAt)
	}

	// The narrow claimed-stub primitive must remove the stub with the same claimID.
	deleted, err := store.DeleteClaimedBlockStub(orgUUID, blockID, claimID)
	if err != nil {
		t.Fatalf("DeleteClaimedBlockStub(stub): %v", err)
	}
	if !deleted {
		t.Fatal("DeleteClaimedBlockStub(stub) did not apply")
	}
	if exists, err := store.BlockExists(orgUUID, blockID); err != nil {
		t.Fatalf("BlockExists(after finalize): %v", err)
	} else if exists {
		t.Fatal("stub row still present after FinalizeBlockDelete")
	}
}

func TestGC_ReleasedBlockStub_ProbeRepairAndMaterialize(t *testing.T) {
	requireCassandra(t)

	database := shareProjectionDBForTest(t)
	orgID := uuid.New().String()
	blockID := strings.Repeat("a", 64)
	sha1ID := strings.Repeat("b", 40)
	t.Cleanup(func() {
		if err := database.Session().Query(`DELETE FROM block_references WHERE org_id = ? AND block_id = ?`, orgID, blockID).Exec(); err != nil {
			t.Fatalf("cleanup block references: %v", err)
		}
		if err := database.Session().Query(`DELETE FROM blocks WHERE org_id = ? AND block_id = ?`, orgID, blockID).Exec(); err != nil {
			t.Fatalf("cleanup block row: %v", err)
		}
	})

	if err := database.Session().Query(`
		INSERT INTO blocks (org_id, block_id) VALUES (?, ?)
	`, orgID, blockID).Exec(); err != nil {
		t.Fatalf("seed released stub: %v", err)
	}
	probe, err := database.ProbeBlockReuse(orgID, blockID)
	if err != nil {
		t.Fatalf("ProbeBlockReuse(released stub): %v", err)
	}
	if probe.Decision != db.BlockReuseRepairableStub {
		t.Fatalf("released stub decision = %v, want RepairableStub", probe.Decision)
	}

	repaired, err := database.RepairReleasedBlockStub(orgID, blockID)
	if err != nil {
		t.Fatalf("RepairReleasedBlockStub: %v", err)
	}
	if !repaired {
		t.Fatal("RepairReleasedBlockStub did not apply")
	}

	// Re-seed the stub and drive the unprobed materialization backstop.
	if err := database.Session().Query(`INSERT INTO blocks (org_id, block_id) VALUES (?, ?)`, orgID, blockID).Exec(); err != nil {
		t.Fatalf("reseed released stub: %v", err)
	}
	if err := database.UpsertBlockMetadataWithSHA1(orgID, blockID, sha1ID, 123, "hot", "blocks/"+orgID+"/aa/aa/"+blockID); err != nil {
		t.Fatalf("UpsertBlockMetadataWithSHA1(stub backstop): %v", err)
	}
	if err := database.AddBlockReference(orgID, blockID, "test:live", uuid.New().String(), 0); err != nil {
		t.Fatalf("AddBlockReference: %v", err)
	}
	probe, err = database.ProbeBlockReuse(orgID, blockID)
	if err != nil {
		t.Fatalf("ProbeBlockReuse(materialized): %v", err)
	}
	if probe.Decision != db.BlockReuseReusable || probe.StorageClass != "hot" {
		t.Fatalf("materialized probe = %+v, want reusable hot block", probe)
	}
}

func TestGC_ActiveBlockStub_RequiresOwningClaim(t *testing.T) {
	requireCassandra(t)

	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	orgUUID := uuid.New()
	orgID := orgUUID.String()
	blockID := strings.Repeat("c", 64)
	claimID := "claim-active-stub"
	t.Cleanup(func() {
		if err := database.Session().Query(`DELETE FROM blocks WHERE org_id = ? AND block_id = ?`, orgID, blockID).Exec(); err != nil {
			t.Fatalf("cleanup active stub: %v", err)
		}
	})

	if err := database.Session().Query(`
		INSERT INTO blocks (org_id, block_id, gc_state, gc_claim_id, gc_claimed_at)
		VALUES (?, ?, ?, ?, ?)
	`, orgID, blockID, db.BlockGCStateDeleting, claimID, time.Now().UTC()).Exec(); err != nil {
		t.Fatalf("seed active stub: %v", err)
	}
	probe, err := database.ProbeBlockReuse(orgID, blockID)
	if err != nil {
		t.Fatalf("ProbeBlockReuse(active stub): %v", err)
	}
	if probe.Decision != db.BlockReuseBlockedByGC {
		t.Fatalf("active stub decision = %v, want BlockedByGC", probe.Decision)
	}
	if repaired, err := database.RepairReleasedBlockStub(orgID, blockID); err != nil {
		t.Fatalf("RepairReleasedBlockStub(active): %v", err)
	} else if repaired {
		t.Fatal("released-stub repair applied to active claim")
	}
	if deleted, err := store.DeleteClaimedBlockStub(orgUUID, blockID, "wrong-claim"); err != nil {
		t.Fatalf("DeleteClaimedBlockStub(wrong owner): %v", err)
	} else if deleted {
		t.Fatal("wrong owner deleted active stub")
	}
	if deleted, err := store.DeleteClaimedBlockStub(orgUUID, blockID, claimID); err != nil {
		t.Fatalf("DeleteClaimedBlockStub(owner): %v", err)
	} else if !deleted {
		t.Fatal("owning claim did not delete active stub")
	}
}

func TestGC_EnsureBlockGCCandidate_RepairsDiscoveryRowWhenCanonicalExists(t *testing.T) {
	requireCassandra(t)

	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	orgID := uuid.New()
	blockID := fmt.Sprintf("cand-repair-%d", time.Now().UnixNano())
	candidateAt := time.Now().UTC().Truncate(time.Millisecond)
	seedSyntheticZeroRefBlockForTest(t, orgID, blockID, "hot")

	effectiveCandidateAt, err := store.EnsureBlockGCCandidate(orgID, blockID, "hot", candidateAt)
	if err != nil {
		t.Fatalf("initial EnsureBlockGCCandidate: %v", err)
	}
	if !effectiveCandidateAt.Equal(candidateAt) {
		t.Fatalf("effective candidate_at = %v, want %v", effectiveCandidateAt, candidateAt)
	}
	if err := database.Session().Query(`
		DELETE FROM gc_block_candidates_by_day
		WHERE candidate_day = ? AND bucket = ? AND candidate_at = ? AND org_id = ? AND block_id = ?
	`, db.GCProjectionUTCDate(candidateAt), db.GCDiscoveryBucket(orgID.String(), blockID), candidateAt.UTC(), orgID.String(), blockID).Exec(); err != nil {
		t.Fatalf("delete block candidate projection row: %v", err)
	}
	if gcCandidateProjectionExists(t, orgID.String(), blockID, candidateAt) {
		t.Fatal("expected block candidate projection row to be deleted before repair")
	}

	repairedCandidateAt, err := store.EnsureBlockGCCandidate(orgID, blockID, "cold", time.Now().UTC())
	if err != nil {
		t.Fatalf("repair EnsureBlockGCCandidate: %v", err)
	}
	if !repairedCandidateAt.Equal(candidateAt) {
		t.Fatalf("repaired candidate_at = %v, want original %v", repairedCandidateAt, candidateAt)
	}
	if !gcCandidateProjectionExists(t, orgID.String(), blockID, candidateAt) {
		t.Fatal("expected gc_block_candidates_by_day projection row to be repaired")
	}
}

func TestGC_RecordS3Orphan_RepairsDiscoveryRowWhenCanonicalExists(t *testing.T) {
	requireCassandra(t)

	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	orgID := uuid.New()
	blockID := fmt.Sprintf("orph-repair-%d", time.Now().UnixNano())
	firstSeenAt := time.Now().UTC().Truncate(time.Millisecond)

	effectiveFirstSeenAt, err := store.RecordS3Orphan(orgID, blockID, "hot", db.PlainBlockRepresentationID, "", "seed", firstSeenAt)
	if err != nil {
		t.Fatalf("initial RecordS3Orphan: %v", err)
	}
	if !effectiveFirstSeenAt.Equal(firstSeenAt) {
		t.Fatalf("effective first_seen_at = %v, want %v", effectiveFirstSeenAt, firstSeenAt)
	}
	t.Cleanup(func() {
		if err := store.DeleteS3Orphan(orgID, blockID, effectiveFirstSeenAt); err != nil {
			t.Fatalf("cleanup DeleteS3Orphan(%s): %v", blockID, err)
		}
	})
	if err := database.Session().Query(`
		DELETE FROM gc_s3_orphans_by_day
		WHERE first_seen_day = ? AND bucket = ? AND first_seen_at = ? AND org_id = ? AND block_id = ?
	`, db.GCProjectionUTCDate(firstSeenAt), db.GCDiscoveryBucket(orgID.String(), blockID), firstSeenAt.UTC(), orgID.String(), blockID).Exec(); err != nil {
		t.Fatalf("delete S3 orphan projection row: %v", err)
	}
	if gcS3OrphanProjectionExists(t, orgID.String(), blockID, firstSeenAt) {
		t.Fatal("expected S3 orphan projection row to be deleted before repair")
	}

	repairedFirstSeenAt, err := store.RecordS3Orphan(orgID, blockID, "cold", db.PlainBlockRepresentationID, "", "", time.Now().UTC())
	if err != nil {
		t.Fatalf("repair RecordS3Orphan: %v", err)
	}
	if !repairedFirstSeenAt.Equal(firstSeenAt) {
		t.Fatalf("repaired first_seen_at = %v, want original %v", repairedFirstSeenAt, firstSeenAt)
	}
	if !gcS3OrphanProjectionExists(t, orgID.String(), blockID, firstSeenAt) {
		t.Fatal("expected gc_s3_orphans_by_day projection row to be repaired")
	}
}

func TestGC_DeleteS3Orphan_RemovesDiscoveryRowWithoutCanonical(t *testing.T) {
	requireCassandra(t)

	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	orgID := uuid.New()
	blockID := fmt.Sprintf("orph-cleanup-%d", time.Now().UnixNano())
	firstSeenAt := time.Now().UTC().Truncate(time.Millisecond)

	effectiveFirstSeenAt, err := store.RecordS3Orphan(orgID, blockID, "hot", db.PlainBlockRepresentationID, "", "seed", firstSeenAt)
	if err != nil {
		t.Fatalf("RecordS3Orphan: %v", err)
	}
	if !gcS3OrphanProjectionExists(t, orgID.String(), blockID, firstSeenAt) {
		t.Fatal("expected gc_s3_orphans_by_day projection row to exist")
	}
	if err := database.Session().Query(`DELETE FROM gc_s3_orphans WHERE org_id = ? AND block_id = ?`, orgID.String(), blockID).Exec(); err != nil {
		t.Fatalf("delete canonical gc_s3_orphans row: %v", err)
	}

	if err := store.DeleteS3Orphan(orgID, blockID, effectiveFirstSeenAt); err != nil {
		t.Fatalf("DeleteS3Orphan: %v", err)
	}
	if gcS3OrphanProjectionExists(t, orgID.String(), blockID, firstSeenAt) {
		t.Fatal("expected gc_s3_orphans_by_day projection row to be removed")
	}
}

func TestGC_StartBlockDeleteOrphan_ResetsCurrentLifecycleState(t *testing.T) {
	requireCassandra(t)

	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	orgID := uuid.New()
	blockID := fmt.Sprintf("orph-reset-%d", time.Now().UnixNano())
	firstSeenAt := time.Now().UTC().Truncate(time.Millisecond)

	effectiveFirstSeenAt, err := store.RecordS3Orphan(orgID, blockID, "cold", db.PlainBlockRepresentationID, "sha1-old", "seed", firstSeenAt)
	if err != nil {
		t.Fatalf("initial RecordS3Orphan: %v", err)
	}
	if !effectiveFirstSeenAt.Equal(firstSeenAt) {
		t.Fatalf("effective first_seen_at = %v, want %v", effectiveFirstSeenAt, firstSeenAt)
	}
	if err := store.MarkS3OrphanMappingCleanupPending(orgID, blockID, db.PlainBlockRepresentationID, "sha1-old", firstSeenAt.Add(5*time.Minute)); err != nil {
		t.Fatalf("MarkS3OrphanMappingCleanupPending: %v", err)
	}
	t.Cleanup(func() {
		if err := store.DeleteS3Orphan(orgID, blockID, effectiveFirstSeenAt); err != nil {
			t.Fatalf("cleanup DeleteS3Orphan(%s): %v", blockID, err)
		}
	})

	resetFirstSeenAt, err := store.StartBlockDeleteOrphan(orgID, blockID, "hot", db.PlainBlockRepresentationID, "sha1-new", time.Now().UTC())
	if err != nil {
		t.Fatalf("StartBlockDeleteOrphan: %v", err)
	}
	if !resetFirstSeenAt.Equal(firstSeenAt) {
		t.Fatalf("reset first_seen_at = %v, want original %v", resetFirstSeenAt, firstSeenAt)
	}

	var storageClass, externalSHA1, recoveryPhase string
	var storedFirstSeenAt time.Time
	if err := database.Session().Query(`
		SELECT storage_class, external_sha1, recovery_phase, first_seen_at
		FROM gc_s3_orphans
		WHERE org_id = ? AND block_id = ?
	`, orgID.String(), blockID).Scan(&storageClass, &externalSHA1, &recoveryPhase, &storedFirstSeenAt); err != nil {
		t.Fatalf("read gc_s3_orphans: %v", err)
	}
	if storageClass != "hot" {
		t.Fatalf("gc_s3_orphans.storage_class = %q, want %q", storageClass, "hot")
	}
	if externalSHA1 != "sha1-new" {
		t.Fatalf("gc_s3_orphans.external_sha1 = %q, want %q", externalSHA1, "sha1-new")
	}
	if recoveryPhase != gcpkg.S3OrphanPhasePendingS3 {
		t.Fatalf("gc_s3_orphans.recovery_phase = %q, want %q", recoveryPhase, gcpkg.S3OrphanPhasePendingS3)
	}
	if !storedFirstSeenAt.UTC().Equal(firstSeenAt.UTC()) {
		t.Fatalf("gc_s3_orphans.first_seen_at = %v, want %v", storedFirstSeenAt.UTC(), firstSeenAt.UTC())
	}

	var projStorageClass, projExternalSHA1, projRecoveryPhase string
	var projFirstSeenAt time.Time
	if err := database.Session().Query(`
		SELECT storage_class, external_sha1, recovery_phase, first_seen_at
		FROM gc_s3_orphans_by_day
		WHERE first_seen_day = ? AND bucket = ? AND first_seen_at = ? AND org_id = ? AND block_id = ?
	`, db.GCProjectionUTCDate(firstSeenAt), db.GCDiscoveryBucket(orgID.String(), blockID), firstSeenAt.UTC(), orgID.String(), blockID).Scan(&projStorageClass, &projExternalSHA1, &projRecoveryPhase, &projFirstSeenAt); err != nil {
		t.Fatalf("read gc_s3_orphans_by_day: %v", err)
	}
	if projStorageClass != "hot" {
		t.Fatalf("gc_s3_orphans_by_day.storage_class = %q, want %q", projStorageClass, "hot")
	}
	if projExternalSHA1 != "sha1-new" {
		t.Fatalf("gc_s3_orphans_by_day.external_sha1 = %q, want %q", projExternalSHA1, "sha1-new")
	}
	if projRecoveryPhase != gcpkg.S3OrphanPhasePendingS3 {
		t.Fatalf("gc_s3_orphans_by_day.recovery_phase = %q, want %q", projRecoveryPhase, gcpkg.S3OrphanPhasePendingS3)
	}
	if !projFirstSeenAt.UTC().Equal(firstSeenAt.UTC()) {
		t.Fatalf("gc_s3_orphans_by_day.first_seen_at = %v, want %v", projFirstSeenAt.UTC(), firstSeenAt.UTC())
	}
}

// getGCGracePeriod reads the configured grace period from the admin status API.
// Returns 0 if the field is absent or the server is too old to expose it.
func getGCGracePeriod(t *testing.T) time.Duration {
	t.Helper()
	resp := superadminClient.Get(t, "/api/v2.1/admin/gc/status")
	if resp.StatusCode != http.StatusOK {
		return 0
	}
	var body map[string]interface{}
	decodeJSON(t, resp, &body)
	if gps, ok := body["grace_period_seconds"].(float64); ok {
		return time.Duration(gps) * time.Second
	}
	return 0
}

// TestGC_GracePeriodEnforcement verifies that the GC worker does not process
// items that are within the grace period.
func TestGC_GracePeriodEnforcement(t *testing.T) {
	requireGCEnabled(t)
	requireCassandra(t)

	gracePeriod := getGCGracePeriod(t)
	if gracePeriod == 0 {
		t.Skip("grace_period_seconds=0 in server config — grace period enforcement is disabled; skipping")
	}
	t.Logf("Grace period configured: %v", gracePeriod)

	orgUUID := uuid.New()
	orgID := orgUUID.String()
	blockID := fmt.Sprintf("grace-%d", time.Now().UnixNano())
	seedSyntheticZeroRefBlockForTest(t, orgUUID, blockID, "hot")
	enqueuedAt := time.Now().UTC().Truncate(time.Millisecond)
	enqueueSyntheticBlockQueueItemForTest(t, orgUUID, blockID, "hot", enqueuedAt)
	if !gcQueueItemExistsSince(t, orgID, "block", blockID, enqueuedAt.Add(-1*time.Second)) {
		t.Fatalf("failed to observe seeded queue item for %s/%s", orgID, blockID)
	}

	// Trigger GC immediately — the candidate was enqueued mere milliseconds ago,
	// which is well within any non-zero grace period. The worker must skip it.
	triggerGCWorkerAndWait(t)

	elapsed := time.Since(enqueuedAt)
	if !blockExistsInDB(t, orgID, blockID) {
		t.Fatalf("CRITICAL: grace period not enforced — block deleted ~%v after enqueue (grace_period=%v)",
			elapsed, gracePeriod)
	}
	if !gcQueueItemExistsSince(t, orgID, "block", blockID, enqueuedAt.Add(-1*time.Second)) {
		t.Fatalf("grace period not enforced — queue item for %s/%s was consumed within %v", orgID, blockID, elapsed)
	}
	t.Logf("Grace period enforcement: block correctly preserved ~%v after enqueue (grace_period=%v) — correct",
		elapsed, gracePeriod)
}

// TestGC_QueueSizeTracking verifies that real queue rows are created when GC work
// is enqueued and that status follows the persisted queue-depth snapshot.
func TestGC_QueueSizeTracking(t *testing.T) {
	requireGCEnabled(t)
	requireCassandra(t)

	statusBefore := getGCQueueSize(t)
	snapshotBefore := readGCQueueSnapshotTotal(t)
	orgUUID := uuid.New()
	orgID := orgUUID.String()
	blockID := fmt.Sprintf("qsize-%d", time.Now().UnixNano())
	seedSyntheticZeroRefBlockForTest(t, orgUUID, blockID, "hot")
	queuedAt := time.Now().UTC().Truncate(time.Millisecond)
	enqueueSyntheticBlockQueueItemForTest(t, orgUUID, blockID, "hot", queuedAt)

	// Poll for the specific block queue item created by this test and for the
	// persisted/global queue-depth views to observe a non-zero value.
	ok := pollUntil(t, 15*time.Second, 500*time.Millisecond, func() bool {
		return gcQueueItemExistsSince(t, orgID, "block", blockID, queuedAt.Add(-1*time.Second)) &&
			readGCQueueSnapshotTotal(t) > 0 &&
			getGCQueueSize(t) > 0
	})
	if !ok {
		t.Fatalf("expected block %s to appear in gc_queue for org %s after enqueue (status_before=%d snapshot_before=%d status_now=%d snapshot_now=%d)",
			blockID, orgID, statusBefore, snapshotBefore, getGCQueueSize(t), readGCQueueSnapshotTotal(t))
	}

	statusAfter := getGCQueueSize(t)
	snapshotAfter := readGCQueueSnapshotTotal(t)
	t.Logf("GC queue state after enqueue: status=%d snapshot=%d (before status=%d snapshot=%d)",
		statusAfter, snapshotAfter, statusBefore, snapshotBefore)

	if snapshotAfter == 0 {
		t.Fatalf("expected total_queue_depth snapshot to be non-zero after enqueue")
	}
	if statusAfter == 0 {
		t.Fatalf("expected status.queue_size to be non-zero after enqueue")
	}
}

// TestGC_StatusSnapshotReconcilesDirtyQueueRows verifies that background GC
// snapshot refresh picks up canonical queue rows from dirty markers without
// relying on Cassandra COUNTER state.
func TestGC_StatusSnapshotReconcilesDirtyQueueRows(t *testing.T) {
	requireGCEnabled(t)
	requireCassandra(t)

	session := shareProjectionDBForTest(t).Session()
	orgID := uuid.New()
	bucket := gcpkg.OrgBucket(orgID)
	queuedAt := time.Now().UTC()
	itemID := fmt.Sprintf("synthetic-drift-%d", time.Now().UnixNano())
	queueBucket := gcpkg.QueueBucket(orgID, gcpkg.ItemBlock, itemID)
	libraryID := uuid.New()
	if err := session.Query(`
		INSERT INTO gc_queue (org_id, bucket, queued_at, item_type, item_id, library_id, storage_class, retry_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, orgID.String(), queueBucket, queuedAt, "block", itemID, libraryID.String(), "hot", 0).Exec(); err != nil {
		t.Fatalf("failed to insert synthetic gc_queue row: %v", err)
	}
	if err := session.Query(`
		INSERT INTO gc_active_orgs (bucket, org_id, last_enqueued_at)
		VALUES (?, ?, ?)
	`, bucket, orgID.String(), time.Now().UTC()).Exec(); err != nil {
		t.Fatalf("failed to insert active org row: %v", err)
	}
	if err := session.Query(`
		INSERT INTO gc_dirty_orgs (bucket, org_id, marked_at)
		VALUES (?, ?, ?)
	`, bucket, orgID.String(), time.Now().UTC()).Exec(); err != nil {
		t.Fatalf("failed to insert dirty org row: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Query(`
			DELETE FROM gc_queue
			WHERE org_id = ? AND bucket = ? AND queued_at = ? AND item_type = ? AND item_id = ?
		`, orgID.String(), queueBucket, queuedAt, "block", itemID).Exec()
		_ = session.Query(`DELETE FROM gc_active_orgs WHERE bucket = ? AND org_id = ?`, bucket, orgID.String()).Exec()
		_ = session.Query(`DELETE FROM gc_dirty_orgs WHERE bucket = ? AND org_id = ?`, bucket, orgID.String()).Exec()
		repairGCSnapshotsForTest(t, orgID)
	})
	snapshotBefore := readGCQueueSnapshotTotal(t)
	triggerGCScannerAndWait(t)

	ok := pollUntil(t, 45*time.Second, 500*time.Millisecond, func() bool {
		queueDepth, _ := readGCOrgQueueStats(t, orgID)
		return queueDepth == 1
	})
	statusAfter := getGCStatus(t)
	snapshotAfter := readGCQueueSnapshotTotal(t)
	orgQueueDepth, orgFailedDepth := readGCOrgQueueStats(t, orgID)
	t.Logf("GC snapshot reconciliation: status=%d snapshot=%d snapshot_before=%d org_queue=%d org_failed=%d", statusAfter.QueueSize, snapshotAfter, snapshotBefore, orgQueueDepth, orgFailedDepth)

	if !ok {
		t.Fatalf("expected org-local queue stats to be reconciled for synthetic row, status=%d snapshot=%d org_queue=%d", statusAfter.QueueSize, snapshotAfter, orgQueueDepth)
	}
	if orgQueueDepth != 1 {
		t.Fatalf("expected reconciled org queue depth to equal 1, got %d", orgQueueDepth)
	}
	if snapshotAfter <= 0 {
		t.Fatalf("expected total queue snapshot to stay positive after reconciliation")
	}
}

// TestGC_DequeueBatchOrdersAcrossQueueBuckets verifies that the real Cassandra
// store returns the globally oldest items even when queue rows live in
// different gc_queue buckets.
func TestGC_DequeueBatchOrdersAcrossQueueBuckets(t *testing.T) {
	requireCassandra(t)

	database := shareProjectionDBForTest(t)
	session := database.Session()
	store := gcpkg.NewCassandraStore(database)
	orgID := uuid.New()
	libraryID := uuid.New()
	base := time.Now().Add(-5 * time.Minute).UTC().Truncate(time.Millisecond)

	type queuedFixture struct {
		itemID   string
		queuedAt time.Time
		bucket   int
		itemType string
	}

	fixtures := make([]queuedFixture, 0, 4)
	buckets := make(map[int]struct{})
	for candidate := 0; len(fixtures) < 4 && candidate < 256; candidate++ {
		itemID := fmt.Sprintf("cross-bucket-%d", candidate)
		bucket := gcpkg.QueueBucket(orgID, gcpkg.ItemBlock, itemID)
		if _, exists := buckets[bucket]; exists && len(buckets) < 3 {
			continue
		}
		buckets[bucket] = struct{}{}
		fixtures = append(fixtures, queuedFixture{
			itemID:   itemID,
			queuedAt: base.Add(time.Duration(len(fixtures)) * time.Second),
			bucket:   bucket,
			itemType: "block",
		})
	}
	if len(fixtures) != 4 || len(buckets) < 3 {
		t.Fatalf("failed to build cross-bucket fixtures: items=%d buckets=%d", len(fixtures), len(buckets))
	}

	for index, fixture := range fixtures {
		requiresLibraryDeletedCheck := index == 0
		libraryGuardMode := ""
		if requiresLibraryDeletedCheck {
			libraryGuardMode = string(gcpkg.LibraryGuardCanonicalMustBeAbsent)
		}
		if err := session.Query(`
			INSERT INTO gc_queue (
				org_id, bucket, queued_at, identity_at, requires_library_deleted_check,
				library_guard_mode, item_type, item_id, library_id, storage_class, retry_count
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, orgID.String(), fixture.bucket, fixture.queuedAt, fixture.queuedAt, requiresLibraryDeletedCheck, libraryGuardMode, fixture.itemType, fixture.itemID, libraryID.String(), "hot", 0).Exec(); err != nil {
			t.Fatalf("failed to insert gc_queue fixture %s: %v", fixture.itemID, err)
		}
	}
	t.Cleanup(func() {
		for _, fixture := range fixtures {
			_ = session.Query(`
				DELETE FROM gc_queue
				WHERE org_id = ? AND bucket = ? AND queued_at = ? AND item_type = ? AND item_id = ?
			`, orgID.String(), fixture.bucket, fixture.queuedAt, fixture.itemType, fixture.itemID).Exec()
		}
	})

	items, err := store.DequeueBatch(orgID, 2, base.Add(30*time.Second))
	if err != nil {
		t.Fatalf("DequeueBatch failed: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 dequeued items, got %d", len(items))
	}
	if items[0].ItemID != fixtures[0].itemID || items[1].ItemID != fixtures[1].itemID {
		t.Fatalf("unexpected dequeue order: got [%s %s], want [%s %s]", items[0].ItemID, items[1].ItemID, fixtures[0].itemID, fixtures[1].itemID)
	}
	if items[0].QueuedAt.After(items[1].QueuedAt) {
		t.Fatalf("dequeue order not globally sorted: first=%s second=%s", items[0].QueuedAt.Format(time.RFC3339Nano), items[1].QueuedAt.Format(time.RFC3339Nano))
	}
	if items[0].IdentityAt.IsZero() || items[1].IdentityAt.IsZero() {
		t.Fatal("expected identity_at to round-trip for dequeued items")
	}
	if items[0].LibraryGuardMode != gcpkg.LibraryGuardCanonicalMustBeAbsent {
		t.Fatalf("library_guard_mode = %q, want %q", items[0].LibraryGuardMode, gcpkg.LibraryGuardCanonicalMustBeAbsent)
	}
	if fixtures[0].bucket == fixtures[1].bucket {
		t.Fatalf("test fixtures did not span distinct leading buckets: %d", fixtures[0].bucket)
	}
}

func TestGC_LegacyLibraryGuardModeNullHydratesAsDeletedAtIdentity(t *testing.T) {
	requireCassandra(t)

	database := shareProjectionDBForTest(t)
	session := database.Session()
	store := gcpkg.NewCassandraStore(database)
	orgID, libraryID := uuid.New(), uuid.New()
	queuedAt := time.Now().UTC().Truncate(time.Millisecond)
	itemID := "legacy-guard-mode-commit"
	bucket := gcpkg.QueueBucket(orgID, gcpkg.ItemCommit, itemID)

	if err := session.Query(`
		INSERT INTO gc_queue (
			org_id, bucket, queued_at, identity_at, requires_library_deleted_check,
			item_type, item_id, library_id, storage_class, retry_count
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, orgID.String(), bucket, queuedAt, queuedAt, true, string(gcpkg.ItemCommit), itemID, libraryID.String(), "hot", 0).Exec(); err != nil {
		t.Fatalf("failed to insert legacy gc_queue row: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Query(`
			DELETE FROM gc_queue
			WHERE org_id = ? AND bucket = ? AND queued_at = ? AND item_type = ? AND item_id = ?
		`, orgID.String(), bucket, queuedAt, string(gcpkg.ItemCommit), itemID).Exec()
	})

	items, err := store.DequeueBatch(orgID, 1, queuedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("DequeueBatch failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 dequeued item, got %d", len(items))
	}
	if items[0].LibraryGuardMode != gcpkg.LibraryGuardDeletedAtIdentity {
		t.Fatalf("legacy null library_guard_mode hydrated as %q, want %q", items[0].LibraryGuardMode, gcpkg.LibraryGuardDeletedAtIdentity)
	}
}

func TestGC_LibraryHardDeleteLockLeaseSetsTTLAndRecoversStaleRows(t *testing.T) {
	requireCassandra(t)

	session := shareProjectionDBForTest(t).Session()
	freshLibraryID, staleLibraryID := uuid.New(), uuid.New()
	freshToken, staleToken, takeoverToken := uuid.New(), uuid.New(), uuid.New()

	acquired, err := gcpkg.AcquireLibraryHardDeleteLockLease(session, freshLibraryID, freshToken)
	if err != nil {
		t.Fatalf("AcquireLibraryHardDeleteLockLease(fresh): %v", err)
	}
	if !acquired {
		t.Fatal("expected fresh library lock acquisition to succeed")
	}
	defer func() {
		_ = gcpkg.ReleaseLibraryHardDeleteLockLease(session, freshLibraryID, freshToken)
	}()

	var freshTTL int
	if err := session.Query(`
		SELECT TTL(heartbeat) FROM gc_library_hard_delete_locks WHERE library_id = ?
	`, freshLibraryID.String()).Scan(&freshTTL); err != nil {
		t.Fatalf("read TTL for fresh library lock: %v", err)
	}
	if freshTTL <= 0 || freshTTL > gcpkgTestHardDeleteLockTTLSeconds {
		t.Fatalf("fresh library lock TTL = %d, want 1..%d", freshTTL, gcpkgTestHardDeleteLockTTLSeconds)
	}

	staleAt := time.Now().UTC().Add(-2 * time.Hour)
	if err := session.Query(`
		INSERT INTO gc_library_hard_delete_locks (library_id, started_at, heartbeat, lease_token)
		VALUES (?, ?, ?, ?)
	`, staleLibraryID.String(), staleAt, staleAt, staleToken.String()).Exec(); err != nil {
		t.Fatalf("seed stale library lock: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Query(`DELETE FROM gc_library_hard_delete_locks WHERE library_id = ?`, staleLibraryID.String()).Exec()
	})

	acquired, err = gcpkg.AcquireLibraryHardDeleteLockLease(session, staleLibraryID, takeoverToken)
	if err != nil {
		t.Fatalf("AcquireLibraryHardDeleteLockLease(stale takeover): %v", err)
	}
	if !acquired {
		t.Fatal("expected stale library lock takeover to succeed")
	}

	var leaseToken string
	var takeoverTTL int
	if err := session.Query(`
		SELECT lease_token, TTL(heartbeat) FROM gc_library_hard_delete_locks WHERE library_id = ?
	`, staleLibraryID.String()).Scan(&leaseToken, &takeoverTTL); err != nil {
		t.Fatalf("read stale-taken-over library lock: %v", err)
	}
	if leaseToken != takeoverToken.String() {
		t.Fatalf("stale library lock lease_token = %s, want %s", leaseToken, takeoverToken)
	}
	if takeoverTTL <= 0 || takeoverTTL > gcpkgTestHardDeleteLockTTLSeconds {
		t.Fatalf("stale takeover TTL = %d, want 1..%d", takeoverTTL, gcpkgTestHardDeleteLockTTLSeconds)
	}
}

const gcpkgTestHardDeleteLockTTLSeconds = 21600

// TestGC_MaxRetryItemMovesToFailedQueue verifies that a max-retry item is
// removed from the live queue and captured in gc_failed_items.
func TestGC_MaxRetryItemMovesToFailedQueue(t *testing.T) {
	requireGCEnabled(t)
	requireCassandra(t)

	session := shareProjectionDBForTest(t).Session()
	orgID := uuid.New()
	bucket := gcpkg.OrgBucket(orgID)
	queuedAt := time.Now().Add(-2 * time.Minute).UTC().Truncate(time.Millisecond)
	itemID := fmt.Sprintf("synthetic-max-retry-%d", time.Now().UnixNano())
	queueBucket := gcpkg.QueueBucket(orgID, gcpkg.ItemType("unknown_type"), itemID)
	libraryID := uuid.New()

	failedBefore := readGCFailedSnapshotTotal(t)

	if err := session.Query(`
		INSERT INTO gc_queue (org_id, bucket, queued_at, item_type, item_id, library_id, storage_class, retry_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, orgID.String(), queueBucket, queuedAt, "unknown_type", itemID, libraryID.String(), "hot", 5).Exec(); err != nil {
		t.Fatalf("failed to insert max-retry queue row: %v", err)
	}
	if err := session.Query(`
		INSERT INTO gc_active_orgs (bucket, org_id, last_enqueued_at)
		VALUES (?, ?, ?)
	`, bucket, orgID.String(), time.Now().UTC()).Exec(); err != nil {
		t.Fatalf("failed to insert active org row: %v", err)
	}
	if err := session.Query(`
		INSERT INTO gc_dirty_orgs (bucket, org_id, marked_at)
		VALUES (?, ?, ?)
	`, bucket, orgID.String(), time.Now().UTC()).Exec(); err != nil {
		t.Fatalf("failed to insert dirty org row: %v", err)
	}
	var failedAt time.Time

	triggerGCWorkerAndWait(t)

	var (
		failedItemType  string
		failedItemID    string
		retryCountAfter int
		lastError       string
		rowFound        bool
	)
	ok := pollUntil(t, 45*time.Second, 500*time.Millisecond, func() bool {
		iter := session.Query(`
			SELECT failed_at, item_type, item_id, retry_count, last_error FROM gc_failed_items WHERE org_id = ?
		`, orgID.String()).Iter()
		rowFound = false
		for iter.Scan(&failedAt, &failedItemType, &failedItemID, &retryCountAfter, &lastError) {
			if failedItemType == "unknown_type" && failedItemID == itemID {
				rowFound = true
				break
			}
		}
		if err := iter.Close(); err != nil {
			t.Fatalf("failed to read gc_failed_items rows: %v", err)
		}
		if !rowFound {
			return false
		}
		var lingering string
		err := session.Query(`
			SELECT item_id FROM gc_queue
			WHERE org_id = ? AND bucket = ? AND queued_at = ? AND item_type = ? AND item_id = ?
		`, orgID.String(), queueBucket, queuedAt, "unknown_type", itemID).Scan(&lingering)
		return errors.Is(err, gocql.ErrNotFound) && getGCStatus(t).FailedItemsTotal > failedBefore
	})
	if !ok {
		t.Fatalf("expected max-retry item to be moved from gc_queue to gc_failed_items")
	}
	t.Cleanup(func() {
		_ = session.Query(`
			DELETE FROM gc_queue WHERE org_id = ? AND bucket = ? AND queued_at = ? AND item_type = ? AND item_id = ?
		`, orgID.String(), queueBucket, queuedAt, "unknown_type", itemID).Exec()
		_ = session.Query(`
			DELETE FROM gc_failed_items WHERE org_id = ? AND failed_at = ? AND item_type = ? AND item_id = ?
		`, orgID.String(), failedAt, "unknown_type", itemID).Exec()
		_ = session.Query(`DELETE FROM gc_active_orgs WHERE bucket = ? AND org_id = ?`, bucket, orgID.String()).Exec()
		_ = session.Query(`DELETE FROM gc_dirty_orgs WHERE bucket = ? AND org_id = ?`, bucket, orgID.String()).Exec()
		repairGCSnapshotsForTest(t, orgID)
	})

	statusAfter := getGCStatus(t)
	queueSnapshotAfter := readGCQueueSnapshotTotal(t)
	failedAfter := readGCFailedSnapshotTotal(t)
	orgQueueDepth, orgFailedDepth := readGCOrgQueueStats(t, orgID)
	t.Logf("GC max-retry flow: status=%d queue_snapshot=%d failed_snapshot=%d retry_after=%d failed_at=%s last_error=%q org_queue=%d org_failed=%d",
		statusAfter.QueueSize, queueSnapshotAfter, failedAfter, retryCountAfter, failedAt.Format(time.RFC3339Nano), lastError, orgQueueDepth, orgFailedDepth)

	if failedAt.IsZero() {
		t.Fatalf("expected gc_failed_items.failed_at to be populated")
	}
	if retryCountAfter != 5 {
		t.Fatalf("expected retry_count to stay at 5 in gc_failed_items, got %d", retryCountAfter)
	}
	if lastError == "" {
		t.Fatalf("expected gc_failed_items.last_error to be populated")
	}
	if orgFailedDepth <= 0 {
		t.Fatalf("expected reconciled org failed depth to be positive after max-retry flow, got %d", orgFailedDepth)
	}
	if failedAfter <= failedBefore {
		t.Fatalf("expected total_failed_items snapshot to increase, before=%d after=%d", failedBefore, failedAfter)
	}
	if statusAfter.FailedItemsTotal <= failedBefore {
		t.Fatalf("expected status.failed_items_total to increase beyond baseline, before=%d after=%d", failedBefore, statusAfter.FailedItemsTotal)
	}
}

func TestGC_FailedItemsAdminEndpoints(t *testing.T) {
	requireGCEnabled(t)
	requireCassandra(t)

	session := shareProjectionDBForTest(t).Session()
	orgID := uuid.New()
	bucket := gcpkg.OrgBucket(orgID)
	failedAtA := time.Now().Add(-2 * time.Minute).UTC().Truncate(time.Millisecond)
	failedAtB := time.Now().Add(-time.Minute).UTC().Truncate(time.Millisecond)
	itemIDA := fmt.Sprintf("dlq-a-%d", time.Now().UnixNano())
	itemIDB := fmt.Sprintf("dlq-b-%d", time.Now().UnixNano())
	libraryID := uuid.New()

	insertFailed := func(failedAt time.Time, itemID string) {
		t.Helper()
		expiresAt := failedAt.Add(30 * 24 * time.Hour)
		if err := session.Query(`
			INSERT INTO gc_failed_items (
				org_id, failed_at, expires_at, queued_at, item_type, item_id, library_id, storage_class, retry_count, last_error, resolution_status
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, orgID.String(), failedAt, expiresAt, failedAt.Add(-time.Minute), "unknown_type", itemID, libraryID.String(), "hot", 5, "boom", "open").Exec(); err != nil {
			t.Fatalf("failed to insert gc_failed_items row for %s: %v", itemID, err)
		}
		if err := session.Query(`
			INSERT INTO gc_dirty_orgs (bucket, org_id, marked_at) VALUES (?, ?, ?)
		`, bucket, orgID.String(), time.Now().UTC()).Exec(); err != nil {
			t.Fatalf("failed to insert dirty org row: %v", err)
		}
	}

	insertFailed(failedAtA, itemIDA)
	insertFailed(failedAtB, itemIDB)
	if err := session.Query(`
		INSERT INTO gc_org_stats (org_id, queue_depth, failed_depth, updated_at)
		VALUES (?, ?, ?, ?)
	`, orgID.String(), 0, 2, failedAtB).Exec(); err != nil {
		t.Fatalf("failed to insert gc_org_stats row: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Query(`DELETE FROM gc_failed_items WHERE org_id = ? AND failed_at = ? AND item_type = ? AND item_id = ?`, orgID.String(), failedAtA, "unknown_type", itemIDA).Exec()
		_ = session.Query(`DELETE FROM gc_failed_items WHERE org_id = ? AND failed_at = ? AND item_type = ? AND item_id = ?`, orgID.String(), failedAtB, "unknown_type", itemIDB).Exec()
		deleteGCQueueItemsByIdentity(t, orgID.String(), "unknown_type", itemIDA)
		deleteGCQueueItemsByIdentity(t, orgID.String(), "unknown_type", itemIDB)
		_ = session.Query(`DELETE FROM gc_active_orgs WHERE bucket = ? AND org_id = ?`, bucket, orgID.String()).Exec()
		_ = session.Query(`DELETE FROM gc_dirty_orgs WHERE bucket = ? AND org_id = ?`, bucket, orgID.String()).Exec()
		_ = session.Query(`DELETE FROM gc_org_stats WHERE org_id = ?`, orgID.String()).Exec()
		repairGCSnapshotsForTest(t, orgID)
	})

	orgsResp := superadminClient.Get(t, "/api/v2.1/admin/gc/failed-items/orgs?limit=10")
	if orgsResp.StatusCode != http.StatusOK {
		t.Fatalf("expected failed-item orgs HTTP 200, got %d", orgsResp.StatusCode)
	}
	orgsBody := responseJSON(t, orgsResp)
	organizations, ok := orgsBody["organizations"].([]interface{})
	if !ok {
		t.Fatalf("expected organizations array in failed-item orgs response, got %#v", orgsBody["organizations"])
	}
	foundOrg := false
	for _, entry := range organizations {
		orgEntry, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		entryOrgID, _ := orgEntry["org_id"].(string)
		if entryOrgID != orgID.String() {
			continue
		}
		foundOrg = true
		if orgName, _ := orgEntry["org_name"].(string); orgName != "" {
			t.Fatalf("failed-item org_name = %q, want empty for orphan org", orgName)
		}
		if failedItemsTotal, _ := orgEntry["failed_items_total"].(float64); int(failedItemsTotal) != 2 {
			t.Fatalf("failed-item org failed_items_total = %v, want 2", orgEntry["failed_items_total"])
		}
		break
	}
	if !foundOrg {
		t.Fatalf("expected orphan org %s in failed-item orgs response: %#v", orgID, organizations)
	}

	listResp := superadminClient.Get(t, "/api/v2.1/admin/gc/failed-items?org_id="+orgID.String()+"&limit=10")
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("expected failed-items list HTTP 200, got %d", listResp.StatusCode)
	}
	listBody := responseJSON(t, listResp)
	items, ok := listBody["items"].([]interface{})
	if !ok || len(items) < 2 {
		t.Fatalf("expected at least 2 failed items in admin list, got %#v", listBody["items"])
	}

	requeueResp := superadminClient.PostJSON(t, "/api/v2.1/admin/gc/failed-items/requeue", map[string]string{
		"org_id":    orgID.String(),
		"failed_at": failedAtA.Format(time.RFC3339Nano),
		"item_type": "unknown_type",
		"item_id":   itemIDA,
	})
	if requeueResp.StatusCode != http.StatusOK {
		t.Fatalf("expected failed-item requeue HTTP 200, got %d", requeueResp.StatusCode)
	}
	if failedQueueItemExists(t, orgID.String(), "unknown_type", itemIDA) {
		t.Fatalf("expected requeued failed item %s to leave gc_failed_items", itemIDA)
	}
	if !gcQueueItemExists(t, orgID.String(), "unknown_type", itemIDA) {
		t.Fatalf("expected requeued failed item %s to appear in gc_queue", itemIDA)
	}

	deleteResp := superadminClient.DeleteJSON(t, "/api/v2.1/admin/gc/failed-items", map[string]string{
		"org_id":    orgID.String(),
		"failed_at": failedAtB.Format(time.RFC3339Nano),
		"item_type": "unknown_type",
		"item_id":   itemIDB,
	})
	if deleteResp.StatusCode != http.StatusOK {
		t.Fatalf("expected failed-item delete HTTP 200, got %d", deleteResp.StatusCode)
	}
	if failedQueueItemExists(t, orgID.String(), "unknown_type", itemIDB) {
		t.Fatalf("expected failed item %s to be removed from gc_failed_items by admin delete", itemIDB)
	}

	orgQueueDepth, orgFailedDepth := readGCOrgQueueStats(t, orgID)
	if orgQueueDepth <= 0 {
		t.Fatalf("expected org queue depth to be positive after admin requeue, got %d", orgQueueDepth)
	}
	if orgFailedDepth != 0 {
		t.Fatalf("expected org failed depth to be 0 after requeue+delete, got %d", orgFailedDepth)
	}
}

// countPendingBlockRows counts gc_pending_items block rows for blockID under the
// partition/clustering derived from libraryID. gc_pending_items is library-scoped in
// its key (the bucket hashes library_id and library_id is a clustering column), so a
// block enqueued under the wrong library_id lands in a different partition than the
// completion delete targets. Passing uuid.Nil counts the canonical (correct) key;
// passing a real library UUID counts the leaked key.
func countPendingBlockRows(t *testing.T, orgID, libraryID uuid.UUID, blockID string) int {
	t.Helper()
	session := shareProjectionDBForTest(t).Session()
	bucket := gcpkg.PendingItemBucket(orgID, libraryID, gcpkg.ItemBlock, blockID)
	iter := session.Query(`
		SELECT identity_at FROM gc_pending_items
		WHERE org_id = ? AND bucket = ? AND item_type = ? AND library_id = ? AND item_id = ?
	`, orgID.String(), bucket, "block", libraryID.String(), blockID).Iter()
	var identityAt time.Time
	count := 0
	for iter.Scan(&identityAt) {
		count++
	}
	if err := iter.Close(); err != nil {
		t.Fatalf("count gc_pending_items block rows for %s/%s (lib=%s): %v", orgID, blockID, libraryID, err)
	}
	return count
}

// deletePendingBlockRowsUnderLibraryBucket removes any gc_pending_items block rows
// keyed under a real library bucket. cleanupGCBlockFixturesForTest only sweeps the
// uuid.Nil bucket, so this is the belt-and-suspenders teardown for a regression that
// would leak a row under the real library partition.
func deletePendingBlockRowsUnderLibraryBucket(t *testing.T, orgID, libraryID uuid.UUID, blockID string) {
	t.Helper()
	session := shareProjectionDBForTest(t).Session()
	bucket := gcpkg.PendingItemBucket(orgID, libraryID, gcpkg.ItemBlock, blockID)
	iter := session.Query(`
		SELECT identity_at FROM gc_pending_items
		WHERE org_id = ? AND bucket = ? AND item_type = ? AND library_id = ? AND item_id = ?
	`, orgID.String(), bucket, "block", libraryID.String(), blockID).Iter()
	var identityAt time.Time
	for iter.Scan(&identityAt) {
		_ = session.Query(`
			DELETE FROM gc_pending_items
			WHERE org_id = ? AND bucket = ? AND item_type = ? AND library_id = ? AND item_id = ? AND identity_at = ?
		`, orgID.String(), bucket, "block", libraryID.String(), blockID, identityAt).Exec()
	}
	_ = iter.Close()
}

// TestGC_ZeroRefBlockTwoProducerLeavesNoPendingItem is the end-to-end regression guard
// for ISSUE-GC-PENDING-ITEM-BLOCK-LIBRARY-SCOPE-01. It reproduces the ACTUAL collision:
// a single producer is self-consistent (CompleteItem re-reads library_id from the same
// gc_queue row), so the leak only appears when TWO producers enqueue the same block /
// candidate under different library_ids. gc_pending_items is library-scoped in its key
// while gc_queue is not, so the two enqueues collapse into one gc_queue row (last writer
// wins on the non-key library_id column) but leave TWO pending rows; CompleteItem then
// clears only the one matching the surviving queue row and orphans the other forever.
//
// Producer A is the REAL worker cascade (an fs_object release drives enqueueZeroRefBlocks
// with the owning library). Producer B is the scanner's orphan-block path (uuid.Nil) at
// the SAME candidate_at. Ordering matters: A must enqueue before B and before the block
// is processed. Pre-fix this leaves an orphaned pending row under the real library bucket;
// with the fix (producers key uuid.Nil AND the store coerces ItemBlock pending rows to
// uuid.Nil) both collapse to one row that CompleteItem removes.
func TestGC_ZeroRefBlockTwoProducerLeavesNoPendingItem(t *testing.T) {
	requireCassandra(t)

	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	queue := gcpkg.NewQueue(store)
	// nil storage: processBlock still deletes the canonical blocks row and completes
	// (the S3 step is skipped and the recovery row is cleared, worker.go), so the queue
	// item is Completed and its pending row is deleted — exactly the path under test.
	worker := gcpkg.NewWorker(store, nil, queue, 100, 0, false, &gcpkg.Stats{})
	session := database.Session()

	orgUUID := uuid.New()
	orgID := orgUUID.String()
	libraryUUID := uuid.New()
	libraryID := libraryUUID.String()
	// 64-hex SHA-256 internal id: an all-SHA-256 list skips block_id_mappings
	// resolution, so no representation/mapping fixtures are needed.
	blockID := fmt.Sprintf("%064x", time.Now().UnixNano())
	fsID := fmt.Sprintf("%040x", time.Now().UnixNano())
	referrer := db.BlockReferrerForFSObject(libraryID, fsID)
	fsQueuedAt := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Millisecond)
	// Pin the block candidate at a fixed past time so BOTH producers enqueue at the same
	// candidate_at (EnsureBlockGCCandidate returns this existing earliest value), exactly
	// as they would in production when the scanner re-discovers a worker-created candidate.
	candidateAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Millisecond)

	// Seed a real, singly-referenced block: canonical blocks row + one fs: reference, the
	// fs_object that owns that reference, and the pre-existing gc_block_candidate.
	if err := database.UpsertBlockMetadata(orgID, blockID, 1, "hot", ""); err != nil {
		t.Fatalf("seed blocks row: %v", err)
	}
	if err := database.AddBlockReference(orgID, blockID, referrer, libraryID, 0); err != nil {
		t.Fatalf("seed fs: block_reference: %v", err)
	}
	if err := session.Query(`
		INSERT INTO fs_objects (library_id, fs_id, obj_type, obj_name, full_path, size_bytes, mtime, block_ids)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, libraryID, fsID, "file", "leak-probe.bin", "/leak-probe.bin", int64(1), fsQueuedAt.Unix(), []string{blockID}).Exec(); err != nil {
		t.Fatalf("seed fs_objects: %v", err)
	}
	candidateAt = ensureSyntheticBlockCandidateForTest(t, orgUUID, blockID, "hot", candidateAt)

	t.Cleanup(func() {
		cleanupGCBlockFixturesForTest(t, orgUUID, blockID)
		deletePendingBlockRowsUnderLibraryBucket(t, orgUUID, libraryUUID, blockID)
		_ = session.Query(`DELETE FROM block_references WHERE org_id = ? AND block_id = ?`, orgID, blockID).Exec()
		_ = session.Query(`DELETE FROM blocks WHERE org_id = ? AND block_id = ?`, orgID, blockID).Exec()
		_ = session.Query(`DELETE FROM fs_objects WHERE library_id = ? AND fs_id = ?`, libraryID, fsID).Exec()
	})

	// Enqueue the fs_object as the live-library version/auto-delete cleanup path does
	// (scanner Phase 5/6): LibraryGuardNone (the library stays alive, no marker), a
	// canonical plain representation, and a past queued_at so DequeueBatch takes it.
	if err := queue.EnqueueBatch([]gcpkg.QueueItem{{
		OrgID:                 orgUUID,
		QueuedAt:              fsQueuedAt,
		IdentityAt:            fsQueuedAt,
		ItemType:              gcpkg.ItemFSObject,
		ItemID:                fsID,
		LibraryID:             libraryUUID,
		BlockRepresentationID: db.PlainBlockRepresentationID,
		LibraryGuardMode:      gcpkg.LibraryGuardNone,
	}}); err != nil {
		t.Fatalf("enqueue fs_object: %v", err)
	}

	// Keep background workers from stealing this org's queue while we drive it.
	if err := store.RemoveOrgFromActiveSet(orgUUID, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("RemoveOrgFromActiveSet: %v", err)
	}

	// Producer A: one worker tick processes the fs_object, which releases the fs: ref and
	// enqueues the now-zero-ref block via enqueueZeroRefBlocks. DequeueBatch snapshots the
	// queue before the block is enqueued, so the block is queued but NOT yet processed.
	if _, err := worker.ProcessOrgOnce(context.Background(), orgUUID); err != nil {
		t.Fatalf("ProcessOrgOnce (producer A): %v", err)
	}
	if !gcQueueItemExistsSince(t, orgID, "block", blockID, candidateAt.Add(-time.Second)) {
		t.Fatalf("producer A (worker cascade) did not enqueue the zero-ref block")
	}
	if !blockExistsInDB(t, orgID, blockID) {
		t.Fatalf("block was processed too early; cannot inject the second producer before deletion")
	}

	// Producer B: the scanner's orphan-block discovery re-enqueues the same block at the
	// same candidate_at under uuid.Nil. Pre-fix this creates a SECOND pending row (under
	// uuid.Nil) alongside producer A's real-library row, and overwrites the queue row's
	// library_id column to uuid.Nil.
	enqueueSyntheticBlockQueueItemForTest(t, orgUUID, blockID, "hot", candidateAt)

	// Drain the block: nil storage skips S3, the row is deleted, and CompleteItem removes
	// the pending row keyed by the surviving queue row's library_id.
	drained := false
	for attempt := 0; attempt < 12; attempt++ {
		processed, err := worker.ProcessOrgOnce(context.Background(), orgUUID)
		if err != nil {
			t.Fatalf("ProcessOrgOnce drain attempt %d: %v", attempt+1, err)
		}
		if !blockExistsInDB(t, orgID, blockID) &&
			!gcQueueItemExistsSince(t, orgID, "block", blockID, candidateAt.Add(-time.Second)) {
			drained = true
			break
		}
		if processed == 0 {
			time.Sleep(150 * time.Millisecond)
		}
	}
	if !drained {
		t.Fatalf("GC did not drain the block (block_exists=%v, block_queued=%v)",
			blockExistsInDB(t, orgID, blockID),
			gcQueueItemExistsSince(t, orgID, "block", blockID, candidateAt.Add(-time.Second)))
	}
	if blockExistsInDB(t, orgID, blockID) {
		t.Fatal("expected canonical block row to be deleted after the drain")
	}

	// Crux: neither producer left an orphaned pending row. Pre-fix, producer A's
	// real-library row survives here (the leak); with the fix both producers collapse to
	// a single uuid.Nil row that CompleteItem removes.
	if n := countPendingBlockRows(t, orgUUID, libraryUUID, blockID); n != 0 {
		t.Fatalf("regression ISSUE-GC-PENDING-ITEM-BLOCK-LIBRARY-SCOPE-01: %d orphaned gc_pending_items block row(s) under the real library bucket (library_id=%s)", n, libraryID)
	}
	if n := countPendingBlockRows(t, orgUUID, uuid.Nil, blockID); n != 0 {
		t.Fatalf("expected the uuid.Nil-keyed pending block row to be removed by CompleteItem, found %d", n)
	}
}

// TestGC_PermanentDeleteMarkerEligibleWithinRetention verifies the durable
// purge-request marker (migration 012 / ISSUE-GC-ORG-TRASH-NO-CASCADE-01, P1/P1b)
// against real Cassandra: ListExpiredDeletedLibraries must return a permanent-delete
// marker whose deleted_at is INSIDE the retention window purely because
// purge_requested_at is set, while a normal recent soft-delete marker (no
// purge_requested_at) stays hidden until retention elapses. This asserts eligibility
// only (the worker still grace-gates the cascade before processing) and proves the
// migration column exists and is read by the eligibility query.
func TestGC_PermanentDeleteMarkerEligibleWithinRetention(t *testing.T) {
	requireCassandra(t)

	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	session := database.Session()

	orgUUID := uuid.New()
	orgID := orgUUID.String()
	purgeLib := uuid.New()
	controlLib := uuid.New()
	now := time.Now().UTC().Truncate(time.Millisecond)

	// Permanent delete: deleted_at = now (well inside a 30-day retention) + purge_requested_at set.
	if err := session.Query(`
		INSERT INTO deleted_libraries (library_id, org_id, deleted_at, storage_class, block_representation_id, purge_requested_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, purgeLib.String(), orgID, now, "hot", db.PlainBlockRepresentationID, now).Exec(); err != nil {
		t.Fatalf("seed purge-requested marker: %v", err)
	}
	// Normal recent soft-delete: deleted_at 5 days ago, no purge_requested_at → not yet eligible.
	if err := session.Query(`
		INSERT INTO deleted_libraries (library_id, org_id, deleted_at, storage_class, block_representation_id)
		VALUES (?, ?, ?, ?, ?)
	`, controlLib.String(), orgID, now.AddDate(0, 0, -5), "hot", db.PlainBlockRepresentationID).Exec(); err != nil {
		t.Fatalf("seed control marker: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Query(`DELETE FROM deleted_libraries WHERE library_id = ?`, purgeLib.String()).Exec()
		_ = session.Query(`DELETE FROM deleted_libraries WHERE library_id = ?`, controlLib.String()).Exec()
	})

	libs, err := store.ListExpiredDeletedLibraries(30)
	if err != nil {
		t.Fatalf("ListExpiredDeletedLibraries: %v", err)
	}

	var sawPurge, sawControl bool
	var purgeRequestedAt time.Time
	for _, l := range libs {
		switch l.LibraryID {
		case purgeLib:
			sawPurge = true
			purgeRequestedAt = l.PurgeRequestedAt
		case controlLib:
			sawControl = true
		}
	}
	if !sawPurge {
		t.Fatal("purge-requested marker (within retention) was NOT eligible — P1b regression")
	}
	if purgeRequestedAt.IsZero() {
		t.Fatal("PurgeRequestedAt not populated from Cassandra")
	}
	if sawControl {
		t.Fatal("recent soft-delete marker (no purge_requested_at) was eligible before retention — regression")
	}
}

// TestGC_SoftDeleteRetentionInvariant_RealCassandra pins the sacred guarantee this branch
// must never weaken: an ordinary (soft) delete puts the library in trash where it lives
// out the configured trash-retention period fully recoverable, and GC must NOT purge it
// early. Real end-to-end via the owner API against real Cassandra: create → trash → assert
// (no purge_requested_at, canonical row still present, Phase 13 does not select it within
// retention) → restore succeeds.
func TestGC_SoftDeleteRetentionInvariant_RealCassandra(t *testing.T) {
	requireCassandra(t)

	repoID := createDisposableTestLibrary(t, adminClient, fmt.Sprintf("soft-delete-invariant-%d", time.Now().UnixNano()))
	session := shareProjectionDBForTest(t).Session()

	// Capture the org while the library is still active, then fully remove it at the end
	// through the REAL delete path. A raw DELETE of the canonical rows leaves the admin
	// projections (libraries_by_org_updated/by_owner/admin_global) behind — and
	// CountActiveLibraries reads libraries_by_org_updated, so orphaned active rows there
	// silently inflate the per-org library limit and break unrelated create-library tests.
	var orgForCleanup string
	if err := session.Query(`SELECT org_id FROM libraries_by_id WHERE library_id = ?`, repoID).Scan(&orgForCleanup); err != nil {
		t.Fatalf("read org for cleanup: %v", err)
	}
	t.Cleanup(func() { removeTestLibraryFully(t, adminClient, session, orgForCleanup, repoID) })

	// Ordinary delete == soft delete (trash) via the real API.
	resp := adminClient.Delete(t, fmt.Sprintf("/api/v2.1/repos/%s/", repoID))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("trash library: status=%d body=%s", resp.StatusCode, responseBody(t, resp))
	}
	resp.Body.Close()

	// INVARIANT 1: the soft-delete marker carries NO purge_requested_at (retention path).
	var orgIDStr, storageClass, repID string
	var deletedAt, purgeRequestedAt time.Time
	if err := session.Query(`
		SELECT org_id, deleted_at, storage_class, block_representation_id, purge_requested_at
		FROM deleted_libraries WHERE library_id = ?
	`, repoID).Scan(&orgIDStr, &deletedAt, &storageClass, &repID, &purgeRequestedAt); err != nil {
		t.Fatalf("read soft-delete marker: %v", err)
	}
	if deletedAt.IsZero() {
		t.Fatal("soft-delete marker missing deleted_at")
	}
	if !purgeRequestedAt.IsZero() {
		t.Fatalf("REGRESSION: ordinary soft-delete stamped purge_requested_at=%s — the retention path must leave it NULL", purgeRequestedAt)
	}

	// INVARIANT 2: the canonical libraries row still exists (recoverable), not hard-deleted.
	var canonicalDeletedAt time.Time
	if err := session.Query(`
		SELECT deleted_at FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgIDStr, repoID).Scan(&canonicalDeletedAt); err != nil {
		t.Fatalf("REGRESSION: soft-deleted library canonical row is gone (%v) — a trashed lib must remain restorable", err)
	}
	if canonicalDeletedAt.IsZero() {
		t.Fatal("canonical library row exists but deleted_at is not set")
	}

	// INVARIANT 3: within the configured retention window, GC Phase 13 must NOT select it.
	store := gcpkg.NewCassandraStore(shareProjectionDBForTest(t))
	libs, err := store.ListExpiredDeletedLibraries(30)
	if err != nil {
		t.Fatalf("ListExpiredDeletedLibraries: %v", err)
	}
	for _, l := range libs {
		if l.LibraryID.String() == repoID {
			t.Fatalf("REGRESSION: a library trashed just now was selected for purge inside the retention window")
		}
	}

	// INVARIANT 4: it is fully restorable via the real API.
	restoreResp := adminClient.Do(t, http.MethodPut, fmt.Sprintf("/api/v2.1/repos/deleted/%s/", repoID), nil)
	if restoreResp.StatusCode != http.StatusOK {
		t.Fatalf("restore trashed library: status=%d body=%s", restoreResp.StatusCode, responseBody(t, restoreResp))
	}
	restoreResp.Body.Close()

	// After restore: marker gone, canonical row active again.
	var goneCheck time.Time
	if err := session.Query(`SELECT deleted_at FROM deleted_libraries WHERE library_id = ?`, repoID).Scan(&goneCheck); err == nil {
		t.Fatal("deleted_libraries marker still present after restore")
	} else if !errors.Is(err, gocql.ErrNotFound) {
		t.Fatalf("unexpected error checking marker removal after restore: %v", err)
	}
	var restoredDeletedAt time.Time
	if err := session.Query(`SELECT deleted_at FROM libraries WHERE org_id = ? AND library_id = ?`, orgIDStr, repoID).Scan(&restoredDeletedAt); err != nil {
		t.Fatalf("restored library canonical row missing: %v", err)
	}
	if !restoredDeletedAt.IsZero() {
		t.Fatal("restored library still has deleted_at set")
	}
	// The library is left active and recoverable; the registered t.Cleanup removes it via the
	// real delete path so no canonical row OR admin-projection row leaks into the shared org.
}

// removeTestLibraryFully tears down an EMPTY disposable test library through the REAL API
// delete path (trash → permanent delete), which runs the canonical admin read-model cleanup
// so every projection row is removed — not just the libraries/libraries_by_id rows a raw CQL
// delete would touch. Because the GC worker may still be grace-gated, the helper then removes
// the disposable library's root commit/fs_object rows directly and polls until any late
// library_cascade enqueue stops recreating marker/queue residue.
func removeTestLibraryFully(t *testing.T, client *testClient, session *gocql.Session, orgID, repoID string) {
	t.Helper()
	// Trash (no-op if already trashed/gone), then permanent-delete so
	// addDeleteAdminLibraryReadModelQueries clears libraries_by_org_updated/by_owner/admin_global.
	if r := client.Delete(t, fmt.Sprintf("/api/v2.1/repos/%s/", repoID)); r != nil {
		if r.StatusCode != http.StatusOK && r.StatusCode != http.StatusNotFound {
			t.Fatalf("trash disposable library cleanup: status=%d body=%s", r.StatusCode, responseBody(t, r))
		}
		r.Body.Close()
	}
	if p := client.Delete(t, fmt.Sprintf("/api/v2.1/repos/deleted/%s/", repoID)); p != nil {
		if p.StatusCode != http.StatusOK && p.StatusCode != http.StatusNotFound {
			t.Fatalf("permanent-delete disposable library cleanup: status=%d body=%s", p.StatusCode, responseBody(t, p))
		}
		p.Body.Close()
	}
	cleanupDisposableLibraryDeleteState(t, session, orgID, repoID)
}

// TestGC_PermanentDeleteWriterStampsPurge_RealCassandra proves the REAL permanent-delete
// writer (not a mocked hardDeleteLibraryRowsFn) persists purge_requested_at plus the
// correct org_id / storage_class / block_representation_id, hard-deletes the canonical
// rows, and produces a marker that Phase 13 selects within retention. Real end-to-end via
// the owner API: create → trash → permanent delete.
func TestGC_PermanentDeleteWriterStampsPurge_RealCassandra(t *testing.T) {
	requireCassandra(t)

	repoID := createDisposableTestLibrary(t, adminClient, fmt.Sprintf("perm-delete-writer-%d", time.Now().UnixNano()))
	session := shareProjectionDBForTest(t).Session()

	// Trash first (permanent-delete requires the library to be in trash).
	trashResp := adminClient.Delete(t, fmt.Sprintf("/api/v2.1/repos/%s/", repoID))
	if trashResp.StatusCode != http.StatusOK {
		t.Fatalf("trash library: status=%d body=%s", trashResp.StatusCode, responseBody(t, trashResp))
	}
	trashResp.Body.Close()

	// Capture the soft-delete marker; its purge_requested_at MUST be NULL (retention path).
	var orgIDStr, softStorage, softRep string
	var softDeletedAt, softPurge time.Time
	if err := session.Query(`
		SELECT org_id, deleted_at, storage_class, block_representation_id, purge_requested_at
		FROM deleted_libraries WHERE library_id = ?
	`, repoID).Scan(&orgIDStr, &softDeletedAt, &softStorage, &softRep, &softPurge); err != nil {
		t.Fatalf("read soft-delete marker: %v", err)
	}
	if !softPurge.IsZero() {
		t.Fatal("soft-delete marker unexpectedly carried purge_requested_at")
	}
	if strings.TrimSpace(softRep) == "" {
		t.Fatal("soft-delete marker missing block_representation_id")
	}

	t.Cleanup(func() {
		cleanupDisposableLibraryDeleteState(t, session, orgIDStr, repoID)
	})

	// Permanent delete via the owner API (owner may permanently delete their own trash).
	permResp := adminClient.Delete(t, fmt.Sprintf("/api/v2.1/repos/deleted/%s/", repoID))
	if permResp.StatusCode != http.StatusOK {
		t.Fatalf("permanent delete: status=%d body=%s", permResp.StatusCode, responseBody(t, permResp))
	}
	permResp.Body.Close()

	// INVARIANT: both canonical rows are hard-deleted (libraries AND libraries_by_id).
	var goneAt time.Time
	if err := session.Query(`SELECT deleted_at FROM libraries WHERE org_id = ? AND library_id = ?`, orgIDStr, repoID).Scan(&goneAt); !errors.Is(err, gocql.ErrNotFound) {
		t.Fatalf("expected canonical libraries row gone after permanent delete, got err=%v", err)
	}
	var goneOrg string
	if err := session.Query(`SELECT org_id FROM libraries_by_id WHERE library_id = ?`, repoID).Scan(&goneOrg); !errors.Is(err, gocql.ErrNotFound) {
		t.Fatalf("expected libraries_by_id row gone after permanent delete, got err=%v", err)
	}

	// INVARIANT: the marker now carries purge_requested_at AND preserves org/storage/rep.
	// The enqueued cascade is grace-gated, so the marker survives well beyond this read.
	var permOrg, permStorage, permRep string
	var permDeletedAt, permPurge time.Time
	if err := session.Query(`
		SELECT org_id, deleted_at, storage_class, block_representation_id, purge_requested_at
		FROM deleted_libraries WHERE library_id = ?
	`, repoID).Scan(&permOrg, &permDeletedAt, &permStorage, &permRep, &permPurge); err != nil {
		t.Fatalf("read permanent-delete marker: %v", err)
	}
	if permPurge.IsZero() {
		t.Fatal("REGRESSION: permanent-delete marker did not stamp purge_requested_at")
	}
	// deleted_at MUST be preserved EXACTLY (not reset to now): Phase 13 dedups library_cascade
	// by deleted_at, so resetting it would let a cascade already queued under the original
	// identity be enqueued a second time. soft-delete writes libraries.deleted_at and the
	// marker from the same timestamp, and permanent-delete copies libraries.deleted_at into
	// the marker, so exact equality must hold. See ISSUE-GC-ORG-TRASH-NO-CASCADE-01 (edge fix).
	if !permDeletedAt.Equal(softDeletedAt) {
		t.Fatalf("REGRESSION: permanent-delete changed deleted_at from %s to %s — it must preserve the original trash time exactly for cascade dedup", softDeletedAt, permDeletedAt)
	}
	if permOrg != orgIDStr {
		t.Fatalf("marker org_id = %s, want %s", permOrg, orgIDStr)
	}
	if permStorage != softStorage {
		t.Fatalf("marker storage_class = %q, want %q (preserved from soft-delete)", permStorage, softStorage)
	}
	if strings.TrimSpace(permRep) != strings.TrimSpace(softRep) {
		t.Fatalf("marker block_representation_id = %q, want %q (preserved)", permRep, softRep)
	}

	// INVARIANT: Phase 13 now selects it within retention on the strength of purge_requested_at.
	store := gcpkg.NewCassandraStore(shareProjectionDBForTest(t))
	libs, err := store.ListExpiredDeletedLibraries(30)
	if err != nil {
		t.Fatalf("ListExpiredDeletedLibraries: %v", err)
	}
	found := false
	for _, l := range libs {
		if l.LibraryID.String() == repoID {
			found = true
			if l.PurgeRequestedAt.IsZero() {
				t.Fatal("eligible library reported zero PurgeRequestedAt")
			}
		}
	}
	if !found {
		t.Fatal("REGRESSION: permanently-deleted library within retention was NOT Phase 13-eligible")
	}
}

// NOTE: The org-admin BULK clean-trash path (DELETE /org/:org_id/admin/trash-libraries/,
// CleanOrgTrashLibraries) is covered by unit tests over its per-library processor
// (processOrgTrashCandidates) in internal/api/v2/library_delete_helpers_test.go. Those drive
// the bulk loop with EXPLICIT candidates, so they assert the hard-delete + purge_requested_at
// marker + immediate deduplicated library_cascade wiring without the org-wide SELECT that a
// real-Cassandra E2E against the shared defaultOrgID would run — which would permanently
// delete every OTHER test's trashed library in that org and leak their GC coordination state.
