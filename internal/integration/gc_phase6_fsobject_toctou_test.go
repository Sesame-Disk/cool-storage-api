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
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

// Destructive real-stack evidence for ISSUE-GC-PHASE6-FSOBJECT-EXECUTION-TOCTOU-01
// and ISSUE-GC-PHASE6-DIRECTORY-CASCADE-SHARED-FSOBJECTS-01. It asserts the
// safety property, so it is RED on a runtime without a fix and stays out of
// ordinary runs until that fix makes it GREEN.
const gcPhase6TOCTOUEvidenceEnv = "SESAMEFS_REQUIRE_GC_PHASE6_TOCTOU_EVIDENCE"

// The real Phase 6 selector, narrowed to one owned library. The productive
// writer runs once inside the selector's own gap: after the commit listing that
// builds the keep set, before the fs_object listing it is compared against.
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
	phase6InFlightGap      = "in-flight-gap"
	phase6ListedBeforeScan = "listed-before-scan"
	phase6OrphanSharesFile = "orphan-shares-head-file"
	phase6OrphanSharesAged = "orphan-shares-aged-head-file"
)

// All legs share one productive Sync writer and one received-but-uncommitted
// orphan directory. "in-flight-gap" lands the writer between the selector's
// commit and fs_object listings. "listed-before-scan" lands it first, so its
// commit is in the keep set: the control that the gap, not the writer or the
// oracle, causes that loss. "orphan-shares-head-file" publishes HEAD before the
// scan and gives the orphan HEAD's file as its child: no race, only the
// worker's directory cascade. "orphan-shares-aged-head-file" is the same after
// an explicit lapse of the Sync up: pin (its 48h TTL), as for a file that has
// been in HEAD longer: the permanent fs: is then its only reference.
func TestGCPhase6InFlightSyncPublicationKeepsHeadTree(t *testing.T) {
	if os.Getenv(gcPhase6TOCTOUEvidenceEnv) != "1" {
		t.Skipf("destructive Phase 6 evidence on owned fixtures; RED until the Phase 6 fix: %s=1", gcPhase6TOCTOUEvidenceEnv)
	}
	database := shareProjectionDBForTest(t)
	var release string
	if err := database.Session().Query(`SELECT release_version FROM system.local`).Scan(&release); err != nil {
		t.Fatalf("Phase 6 evidence requires Cassandra: %v", err)
	}
	// Every replica sharing this keyspace must report GC off: one with GC on
	// would scan the same auto-delete library and race the worker.
	e19RequireNoBackgroundGC(t)
	for _, leg := range []string{phase6InFlightGap, phase6ListedBeforeScan, phase6OrphanSharesFile, phase6OrphanSharesAged} {
		reached := false
		t.Run(leg, func(t *testing.T) { phase6Leg(t, database, leg, &reached) })
		// A helper SKIP (S3 or storage class unavailable) must not pass as evidence.
		if !reached {
			t.Fatalf("leg %s did not reach its oracle", leg)
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
	// Positive control: received metadata that no commit references. Its child
	// does not exist, except in the cascade leg, where it is HEAD's file.
	cascade := leg == phase6OrphanSharesFile || leg == phase6OrphanSharesAged
	orphanChild := sha1hex([]byte("phase6-orphan-child-" + uuid.NewString()))
	if cascade {
		writer()
		publish()
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
	if exists := phase6FSObjectExists(t, store, library, file); exists != cascade || !phase6FSObjectExists(t, store, library, orphan) {
		t.Fatal("fixture precondition: before the scan only the orphan control (and, in the cascade leg, HEAD's tree) exists")
	}

	scope := &phase6InFlightStore{GCStore: store, library: library, inGap: func() {}}
	want := []string{orphan}
	switch leg {
	case phase6InFlightGap:
		scope.inGap = writer
		want = append(want, root, file)
	case phase6ListedBeforeScan:
		writer()
	}
	n, err := gcpkg.NewScanner(scope, gcpkg.NewQueue(scope), &gcpkg.Stats{}, config.GCConfig{}).ScanAutoDeleteExpiredObjectsOnce(t.Context())
	if err != nil || !scope.fired {
		t.Fatalf("Phase 6 selector: n=%d fired=%v err=%v", n, scope.fired, err)
	}
	selected := map[string]gcpkg.QueueItem{}
	for _, item := range phase6OwnedItems(t, store, fx.orgUUID, library, fx.blockID) {
		selected[item.ItemID] = item
	}
	for _, id := range want {
		item, ok := selected[id]
		if !ok || item.ItemType != gcpkg.ItemFSObject || item.LibraryGuardMode != gcpkg.LibraryGuardNone || item.RequiresLibraryDeletedCheck {
			t.Fatalf("selection not reproduced: %s not selected as an unguarded fs_object: %+v (n=%d)", id, item, n)
		}
	}
	if len(selected) != len(want) || n != len(want) {
		t.Fatalf("selection must be exactly %v (HEAD tree kept): n=%d rows=%d", want, n, len(selected))
	}

	// The client completes; HEAD now depends on the selected objects.
	if !cascade {
		publish()
	}

	// The real worker executes the selection made against the old HEAD.
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
			t.Fatalf("worker did not drain the owned selection; a partial run proves nothing: %+v", item)
		}
		if item.ItemType == gcpkg.ItemBlock {
			blockRows++
		}
	}
	*reached = true

	if phase6FSObjectExists(t, store, library, orphan) {
		t.Fatal("positive control: the worker did not execute the orphan fs_object, so the oracle below would be vacuous")
	}
	if head := borrowedFSReadHead(t, database, fx.orgID, fx.repoID); head != commit {
		t.Errorf("HEAD moved: %s", head)
	}
	// Safety oracle. Each check is reported separately so RED names every loss.
	if !phase6FSObjectExists(t, store, library, root) {
		t.Errorf("PHASE6 VIOLATION (%s): HEAD root fs_object %s deleted", leg, root)
	}
	if !phase6FSObjectExists(t, store, library, file) {
		t.Errorf("PHASE6 VIOLATION (%s): HEAD file fs_object %s deleted", leg, file)
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
	t.Logf("EVIDENCE phase6 leg=%s: HEAD=%s root=%s file=%s orphan=%s dequeued=%v refs=[%s] candidates=%d queuedBlock=%d canonical=%v bytes=%v",
		leg, commit, root, file, orphan, owned.dequeued, strings.Join(refs, " "), candidates, blockRows, canonicalErr == nil, bytesErr == nil)
}
