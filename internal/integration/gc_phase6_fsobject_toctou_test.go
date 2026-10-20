//go:build integration

package integration

import (
	"bytes"
	"errors"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	apipkg "github.com/Sesame-Disk/sesamefs/internal/api"
	"github.com/Sesame-Disk/sesamefs/internal/config"
	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// Destructive real-stack evidence for ISSUE-GC-PHASE6-FSOBJECT-EXECUTION-TOCTOU-01
// and ISSUE-GC-PHASE6-DIRECTORY-CASCADE-SHARED-FSOBJECTS-01. On main@bb89d7b12 the
// same schedules lost HEAD metadata (docs/GC-PHASE6-FSOBJECT-TOCTOU.md §4). With
// Phase 6 suspended, the selector enqueues nothing and the worker completes the
// stale unguarded rows it used to produce without mutation.
const (
	gcPhase6TOCTOUEvidenceEnv = "SESAMEFS_REQUIRE_GC_PHASE6_TOCTOU_EVIDENCE"
	gcPhase6ChildEnv          = "SESAMEFS_GC_PHASE6_CHILD"
)

// The real Phase 6 selector, narrowed to one owned library. The productive
// writer runs once inside the selector's own gap: after the commit listing that
// built the keep set, before the fs_object listing it was compared against.
type phase6InFlightStore struct {
	gcpkg.GCStore
	library uuid.UUID
	inGap   func()
	fired   bool
}

func (s *phase6InFlightStore) ListLibrariesWithAutoDelete() ([]gcpkg.LibraryAutoDeleteInfo, error) {
	rows, err := s.GCStore.ListLibrariesWithAutoDelete()
	if err != nil {
		return nil, err
	}
	var owned []gcpkg.LibraryAutoDeleteInfo
	for _, row := range rows {
		if row.LibraryID == s.library {
			owned = append(owned, row)
		}
	}
	return owned, nil
}

func (s *phase6InFlightStore) ListFSObjectIDsForLibrary(libraryID uuid.UUID) ([]string, error) {
	if libraryID == s.library && !s.fired {
		s.fired = true
		s.inGap()
	}
	return s.GCStore.ListFSObjectIDsForLibrary(libraryID)
}

// The fixture library lives in a shared org; the worker drains only this
// library's fs_object rows. Every mutation runs through the real store.
type phase6OwnedQueue struct {
	gcpkg.GCStore
	library  uuid.UUID
	dequeued []string
}

func (s *phase6OwnedQueue) DequeueBatch(org uuid.UUID, _ int, cutoff time.Time) ([]gcpkg.QueueItem, error) {
	rows, err := s.GCStore.DequeueBatch(org, 100000, cutoff)
	if err != nil {
		return nil, err
	}
	var owned []gcpkg.QueueItem
	for _, row := range rows {
		if row.LibraryID == s.library && row.ItemType == gcpkg.ItemFSObject {
			owned = append(owned, row)
			s.dequeued = append(s.dequeued, row.ItemID)
		}
	}
	return owned, nil
}

// phase6OwnedItems returns this library's queue rows plus the fixture block's
// rows (block items are keyed under uuid.Nil, not the library).
func phase6OwnedItems(t *testing.T, store *gcpkg.CassandraStore, org, library uuid.UUID, blockID string) []gcpkg.QueueItem {
	t.Helper()
	rows, err := store.DequeueBatch(org, 100000, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("list owned queue rows: %v", err)
	}
	var owned []gcpkg.QueueItem
	for _, row := range rows {
		if row.LibraryID == library || (row.ItemType == gcpkg.ItemBlock && row.ItemID == blockID) {
			owned = append(owned, row)
		}
	}
	return owned
}

func phase6FSObjectExists(t *testing.T, store *gcpkg.CassandraStore, library uuid.UUID, fsID string) bool {
	t.Helper()
	_, err := store.GetFSObject(library, fsID)
	if errors.Is(err, gocql.ErrNotFound) {
		return false
	}
	if err != nil {
		t.Fatalf("read fs_object %s: %v", fsID, err)
	}
	return true
}

const (
	phase6SelectorSuspended = "selector-suspended"
	phase6StaleInFlight     = "stale-in-flight-selection"
	phase6OrphanSharesFile  = "stale-orphan-shares-head-file"
	phase6OrphanSharesAged  = "stale-orphan-shares-aged-head-file"
)

// All legs share one productive Sync writer and one received-but-uncommitted
// orphan directory.
//   - selector-suspended: the writer lands inside the selector's gap (the
//     reproduced race) and the orphan has HEAD's file as its child (the
//     reproduced cascade); the suspended selector must enqueue nothing.
//   - stale-in-flight-selection: the rows the old selector produced for that
//     race (orphan, root, file) are queued before HEAD publishes them.
//   - stale-orphan-shares-head-file: HEAD is published; a stale row names the
//     orphan directory whose child is HEAD's file.
//   - stale-orphan-shares-aged-head-file: the same after an explicit lapse of the
//     Sync up: pin (its 48h TTL); the permanent fs: is HEAD's only reference.
//
// In every stale leg the worker must complete each row, mutate nothing, and
// count each one as suspended.
func TestGCPhase6InFlightSyncPublicationKeepsHeadTree(t *testing.T) {
	// The standard runners point SESAMEFS_URL at the ordinary fleet, whose GC may be
	// on. Re-run this test as a child against the isolated, GC-OFF backend.
	if endpoint := os.Getenv("SESAMEFS_E19_ISOLATED_URL"); endpoint != "" && os.Getenv(gcPhase6ChildEnv) != "1" {
		e101IsolatedMatrix(t, endpoint, "TestGCPhase6InFlightSyncPublicationKeepsHeadTree", gcPhase6TOCTOUEvidenceEnv, gcPhase6ChildEnv, map[string]bool{}, nil)
		return
	}
	if os.Getenv(gcPhase6TOCTOUEvidenceEnv) != "1" {
		t.Skipf("destructive Phase 6 evidence needs an isolated GC-OFF backend: set SESAMEFS_E19_ISOLATED_URL or %s=1", gcPhase6TOCTOUEvidenceEnv)
	}
	database := shareProjectionDBForTest(t)
	var release string
	if err := database.Session().Query(`SELECT release_version FROM system.local`).Scan(&release); err != nil {
		t.Fatalf("Phase 6 evidence requires Cassandra: %v", err)
	}
	// Every replica sharing this keyspace must report GC off: one with GC on
	// would scan the same auto-delete library and race the worker.
	e19RequireNoBackgroundGC(t)
	for _, leg := range []string{phase6SelectorSuspended, phase6StaleInFlight, phase6OrphanSharesFile, phase6OrphanSharesAged} {
		reached := false
		t.Run(leg, func(t *testing.T) { phase6Leg(t, database, leg, &reached) })
		// A helper SKIP (S3 or storage class unavailable) must not pass as evidence.
		if !reached {
			t.Errorf("leg %s did not reach its oracle", leg)
		}
	}
}

func phase6Leg(t *testing.T, database *dbpkg.DB, leg string, reached *bool) {
	class := x1StorageClass(t)
	fx := &w2CreateFileFixture{w2UploadFileFixture: newW2UploadFileFixture(t, database, newBorrowedFSHeadHandler(t, database, class))}
	library := uuid.MustParse(fx.repoID)
	store := gcpkg.NewCassandraStore(database)
	handler := w24Handler(t, database, class)

	// The Sync upload (same objects as the E1-7 fresh leg).
	fileJSON := mustMarshalSyncObjectForTest(t, map[string]interface{}{"block_ids": []string{fx.sha1ID}, "size": len(fx.content), "type": 1, "version": 1})
	file := syncSHA1HexForTest(fileJSON)
	rootJSON := mustMarshalSyncObjectForTest(t, map[string]interface{}{"dirents": []apipkg.FSEntry{{ID: file, Mode: 33188, Mtime: time.Now().Unix(), Name: fx.filename, Size: int64(len(fx.content))}}, "type": 3, "version": 1})
	root := syncSHA1HexForTest(rootJSON)
	commit := syncSHA1HexForTest([]byte("phase6-" + uuid.NewString()))
	payload := mustMarshalSyncObjectForTest(t, map[string]interface{}{"commit_id": commit, "repo_id": fx.repoID, "root_id": root, "parent_id": fx.headBefore, "description": "Phase 6 in-flight publication", "ctime": time.Now().Unix(), "version": 1})
	syncRef := dbpkg.BlockReferrerForUpload("sync:" + fx.repoID + ":" + fx.blockID)
	permanent := dbpkg.BlockReferrerForFSObject(fx.repoID, file)
	cleanupUploadedBlockArtifactsForTest(t, fx.orgID, fx.repoID, fx.blockID, fx.sha1ID, syncRef, permanent)
	t.Cleanup(func() {
		for bucket := 0; bucket < dbpkg.PublishedBlockReferenceRepairBuckets; bucket++ {
			if err := database.Session().Query(`DELETE FROM published_block_reference_repairs WHERE bucket = ? AND org_id = ? AND repo_id = ?`, bucket, fx.orgID, fx.repoID).Exec(); err != nil {
				t.Error(err)
			}
		}
		queue := gcpkg.NewQueue(store)
		for _, item := range phase6OwnedItems(t, store, fx.orgUUID, library, fx.blockID) {
			if err := queue.Complete(item); err != nil {
				t.Errorf("cleanup owned queue row %s/%s: %v", item.ItemType, item.ItemID, err)
			}
		}
		if left := phase6OwnedItems(t, store, fx.orgUUID, library, fx.blockID); len(left) != 0 {
			t.Errorf("owned queue cleanup left %d rows", len(left))
		}
	})

	// Productive policy write: libraries.auto_delete_days and its GC projection.
	autoDelete := "/api/v2.1/repos/" + fx.repoID + "/auto-delete/"
	resp := adminClient.PutJSON(t, autoDelete, map[string]int{"auto_delete_days": 30})
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	t.Cleanup(func() {
		resp := adminClient.PutJSON(t, autoDelete, map[string]int{"auto_delete_days": 0})
		expectStatus(t, resp, http.StatusOK)
		resp.Body.Close()
	})

	writer := func() {
		e17OK(t, e17Request(fx, http.MethodPut, "/commit/"+commit, "commit_id", commit, payload, handler.PutCommit))
		e17OK(t, e17Request(fx, http.MethodPost, "/recv-fs", "", "", packSyncFSObjectsForTest(t, syncPackedFSObject{fsID: file, jsonData: fileJSON}, syncPackedFSObject{fsID: root, jsonData: rootJSON}), handler.RecvFS))
	}
	publish := func() {
		e17OK(t, e17Request(fx, http.MethodPut, "/block/"+fx.sha1ID, "block_id", fx.sha1ID, fx.content, handler.PutBlock))
		fx.target = fx.readTarget(t)
		e17OK(t, e17Request(fx, http.MethodPut, "/commit/HEAD?head="+commit, "commit_id", "HEAD", nil, handler.PutCommit))
		if head := borrowedFSReadHead(t, database, fx.orgID, fx.repoID); head != commit {
			t.Fatalf("HEAD=%s want=%s", head, commit)
		}
		w24AssertHeadReaches(t, fx, commit, file)
		e17AssertRefs(t, fx, syncRef, permanent)
	}
	// Received metadata that no commit references. Its child does not exist in the
	// in-flight leg; elsewhere it is HEAD's file.
	cascade := leg != phase6StaleInFlight
	orphanChild := sha1hex([]byte("phase6-orphan-child-" + uuid.NewString()))
	if leg == phase6OrphanSharesFile || leg == phase6OrphanSharesAged {
		writer()
		publish()
	}
	if cascade {
		orphanChild = file
	}
	if leg == phase6OrphanSharesAged {
		if err := database.RemoveBlockReference(fx.orgID, fx.blockID, syncRef); err != nil {
			t.Fatal(err)
		}
		e17AssertRefs(t, fx, permanent)
	}
	orphanJSON := mustMarshalSyncObjectForTest(t, map[string]interface{}{"dirents": []apipkg.FSEntry{{ID: orphanChild, Mode: 33188, Mtime: time.Now().Unix(), Name: "phase6-orphan-" + uuid.NewString()[:8] + ".txt", Size: 1}}, "type": 3, "version": 1})
	orphan := syncSHA1HexForTest(orphanJSON)
	e17OK(t, e17Request(fx, http.MethodPost, "/recv-fs", "", "", packSyncFSObjectsForTest(t, syncPackedFSObject{fsID: orphan, jsonData: orphanJSON}), handler.RecvFS))

	// Stale work: exactly the rows the pre-suspension selector produced.
	var seeded []string
	switch leg {
	case phase6SelectorSuspended:
		scope := &phase6InFlightStore{GCStore: store, library: library, inGap: writer}
		n, err := gcpkg.NewScanner(scope, gcpkg.NewQueue(scope), &gcpkg.Stats{}, config.GCConfig{}).ScanAutoDeleteExpiredObjectsOnce(t.Context())
		if err != nil || n != 0 {
			t.Fatalf("suspended Phase 6 selector: n=%d err=%v, want 0, nil", n, err)
		}
		if scope.fired {
			t.Fatal("suspended Phase 6 selector still lists fs_objects")
		}
		writer() // the in-gap write did not run: complete the upload for the oracle
		publish()
	case phase6StaleInFlight:
		writer()
		seeded = []string{orphan, root, file}
	default:
		seeded = []string{orphan}
	}
	if len(seeded) > 0 {
		now := time.Now()
		var stale []gcpkg.QueueItem
		for _, id := range seeded {
			stale = append(stale, gcpkg.QueueItem{OrgID: fx.orgUUID, QueuedAt: now, IdentityAt: now, ItemType: gcpkg.ItemFSObject, ItemID: id, LibraryID: library, BlockRepresentationID: dbpkg.PlainBlockRepresentationID})
		}
		if err := gcpkg.NewQueue(store).EnqueueBatch(stale); err != nil {
			t.Fatalf("seed stale unguarded rows: %v", err)
		}
	}
	if leg == phase6StaleInFlight {
		publish() // HEAD now depends on the rows queued against the old HEAD
	}
	for _, item := range phase6OwnedItems(t, store, fx.orgUUID, library, fx.blockID) {
		if item.ItemType != gcpkg.ItemFSObject || item.LibraryGuardMode != gcpkg.LibraryGuardNone || item.RequiresLibraryDeletedCheck {
			t.Fatalf("unexpected owned queue row before the worker: %+v", item)
		}
	}

	// The real worker executes the stale rows.
	suspendedBefore := testutil.ToFloat64(metrics.GCUnguardedFSObjectItemsSuspendedTotal)
	owned := &phase6OwnedQueue{GCStore: store, library: library}
	worker := w2Worker(t, owned, class)
	for pass := 0; pass < 4; pass++ {
		processed, err := worker.ProcessOrgOnce(t.Context(), fx.orgUUID)
		if err != nil {
			t.Fatalf("worker pass %d: %v", pass, err)
		}
		if processed == 0 {
			break
		}
	}
	var blockRows int
	for _, item := range phase6OwnedItems(t, store, fx.orgUUID, library, fx.blockID) {
		if item.ItemType == gcpkg.ItemFSObject {
			t.Errorf("PHASE6 VIOLATION (%s): fs_object row %s left queued or fanned out", leg, item.ItemID)
		}
		if item.ItemType == gcpkg.ItemBlock {
			blockRows++
		}
	}
	*reached = true
	suspended := testutil.ToFloat64(metrics.GCUnguardedFSObjectItemsSuspendedTotal) - suspendedBefore

	if head := borrowedFSReadHead(t, database, fx.orgID, fx.repoID); head != commit {
		t.Errorf("HEAD moved: %s", head)
	}
	// Safety oracle. Each check is reported separately so a regression names every loss.
	for name, fsID := range map[string]string{"HEAD root": root, "HEAD file": file, "orphan": orphan} {
		if !phase6FSObjectExists(t, store, library, fsID) {
			t.Errorf("PHASE6 VIOLATION (%s): %s fs_object %s deleted", leg, name, fsID)
		}
	}
	refs, err := database.ListBlockReferrers(fx.orgID, fx.blockID)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(refs)
	if !containsStringForTest(refs, permanent) {
		t.Errorf("PHASE6 VIOLATION (%s): permanent %s removed while HEAD reaches it; refs=%v", leg, permanent, refs)
	}
	var candidates int
	if err := database.Session().Query(`SELECT count(*) FROM gc_block_candidates WHERE org_id = ? AND block_id = ?`, fx.orgID, fx.blockID).Scan(&candidates); err != nil {
		t.Fatal(err)
	}
	if candidates != 0 || blockRows != 0 {
		t.Errorf("PHASE6 VIOLATION (%s): HEAD block %s became a GC candidate: candidates=%d queued=%d", leg, fx.blockID, candidates, blockRows)
	}
	canonical, canonicalErr := store.GetBlockInfo(fx.orgUUID, fx.blockID)
	if canonicalErr != nil || canonical.StorageKey != fx.target.StorageKey {
		t.Errorf("PHASE6 VIOLATION (%s): canonical P changed: %+v err=%v", leg, canonical, canonicalErr)
	}
	data, bytesErr := newVerificationBlockStore(t, fx.orgID).GetBlockByStorageKey(t.Context(), fx.target.StorageKey)
	if bytesErr != nil || !bytes.Equal(data, fx.content) {
		t.Errorf("PHASE6 VIOLATION (%s): HEAD bytes at %s lost: %v", leg, fx.target.StorageKey, bytesErr)
	}
	// Non-vacuity: every stale row was dequeued and counted as suspended, so the
	// oracle above was reached through the worker, not around it.
	if suspended != float64(len(seeded)) || len(owned.dequeued) != len(seeded) {
		t.Errorf("worker must visit and suspend every stale row: seeded=%d dequeued=%v suspended=%v", len(seeded), owned.dequeued, suspended)
	}
	t.Logf("EVIDENCE phase6 leg=%s: HEAD=%s root=%s file=%s orphan=%s seeded=%d dequeued=%v suspended=%v refs=[%s] candidates=%d queuedBlock=%d canonical=%v bytes=%v",
		leg, commit, root, file, orphan, len(seeded), owned.dequeued, suspended, strings.Join(refs, " "), candidates, blockRows, canonicalErr == nil, bytesErr == nil)
}
