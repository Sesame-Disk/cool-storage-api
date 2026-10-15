//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	apipkg "github.com/Sesame-Disk/sesamefs/internal/api"
	"github.com/Sesame-Disk/sesamefs/internal/config"
	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const e17EvidenceEnv = "SESAMEFS_REQUIRE_E17_SYNC_RECVFS_CHARACTERIZATION"

var e17Legs = []string{"fresh", "fresh-head-before-put", "reused", "reused-committed", "reused-terminal", "post-put-gc"}
var e17Evidence = map[string]bool{}

func e17Missing(observed map[string]bool) []string {
	var missing []string
	for _, leg := range e17Legs {
		if !observed[leg] {
			missing = append(missing, leg)
		}
	}
	return missing
}

// All Sync phases execute current productive handlers and real storage. Only
// candidate discovery is narrowed; no mapping/P/ref/HEAD/D response is invented.
func TestE17SyncRecvFSBeforePutBlock(t *testing.T) {
	requireCassandra(t)
	database := shareProjectionDBForTest(t)
	class := x1StorageClass(t)
	for _, leg := range e17Legs {
		t.Run(leg, func(t *testing.T) {
			var fx *w2CreateFileFixture
			var sourceRef string
			if strings.HasPrefix(leg, "reused") {
				// Seed bytes/mapping through a productive web block upload, without HEAD/fs:.
				web := newSessionUploadHeadFixture(t, database, newBorrowedFSHeadHandler(t, database, class))
				fx = &w2CreateFileFixture{w2UploadFileFixture: &w2UploadFileFixture{borrowedFSHeadFixture: web.borrowedFSHeadFixture}}
				sourceRef = web.sessionRef
				x1Cleanup(t, database, fx.orgUUID, fx.blockID)
			} else {
				fx = &w2CreateFileFixture{w2UploadFileFixture: newW2UploadFileFixture(t, database, newBorrowedFSHeadHandler(t, database, class))}
			}
			// Register after x1Cleanup: read/delete current K before it drops the
			// canonical row, and remove the owned Sync expiry projection as well.
			cleanupRefs := []string{dbpkg.BlockReferrerForUpload("sync:" + fx.repoID + ":" + fx.blockID)}
			if sourceRef != "" {
				cleanupRefs = append(cleanupRefs, sourceRef)
			}
			cleanupUploadedBlockArtifactsForTest(t, fx.orgID, fx.repoID, fx.blockID, fx.sha1ID, cleanupRefs...)
			t.Cleanup(func() {
				for bucket := 0; bucket < dbpkg.PublishedBlockReferenceRepairBuckets; bucket++ {
					if err := database.Session().Query(`DELETE FROM published_block_reference_repairs WHERE bucket = ? AND org_id = ? AND repo_id = ?`, bucket, fx.orgID, fx.repoID).Exec(); err != nil {
						t.Error(err)
					}
				}
			})
			trace := e13Observer(fx.orgID, fx.blockID, "e17-writer")
			writerDB := w2EvidenceSession(t, splitEnvOrDefault("CASSANDRA_HOSTS", "cassandra:9042")[0], trace)
			handler := w24Handler(t, writerDB, class)
			fileJSON := mustMarshalSyncObjectForTest(t, map[string]interface{}{"block_ids": []string{fx.sha1ID}, "size": len(fx.content), "type": 1, "version": 1})
			file := syncSHA1HexForTest(fileJSON)
			rootJSON := mustMarshalSyncObjectForTest(t, map[string]interface{}{"dirents": []apipkg.FSEntry{{ID: file, Mode: 33188, Mtime: time.Now().Unix(), Name: fx.filename, Size: int64(len(fx.content))}}, "type": 3, "version": 1})
			root := syncSHA1HexForTest(rootJSON)
			commit := syncSHA1HexForTest([]byte("e17-" + uuid.NewString()))
			payload := mustMarshalSyncObjectForTest(t, map[string]interface{}{"commit_id": commit, "repo_id": fx.repoID, "root_id": root, "parent_id": fx.headBefore, "description": "E1-7 RecvFS before PutBlock", "ctime": time.Now().Unix(), "version": 1})
			e17OK(t, e17Request(fx, http.MethodPut, "/commit/"+commit, "commit_id", commit, payload, handler.PutCommit))
			fx.assertHeadUnchanged(t)
			var storedRoot, storedParent string
			if err := database.Session().Query(`SELECT root_fs_id, parent_id FROM commits WHERE library_id = ? AND commit_id = ?`, fx.repoID, commit).Scan(&storedRoot, &storedParent); err != nil || storedRoot != root || storedParent != fx.headBefore {
				t.Fatalf("stored commit: root=%s parent=%s err=%v", storedRoot, storedParent, err)
			}
			packed := packSyncFSObjectsForTest(t, syncPackedFSObject{fsID: file, jsonData: fileJSON}, syncPackedFSObject{fsID: root, jsonData: rootJSON})
			recv := func() { e17OK(t, e17Request(fx, http.MethodPost, "/recv-fs", "", "", packed, handler.RecvFS)) }
			recv()
			e17AssertMetadata(t, fx, file, root)
			fx.assertHeadUnchanged(t)
			w24AssertNoSyncPin(t, fx)
			if rows := w2Repairs(t, fx); len(rows) != 0 {
				t.Fatalf("RecvFS installed repair: %+v", rows)
			}
			var original gcpkg.BlockDeleteTarget
			if sourceRef == "" {
				e17AssertMapping(t, fx, false)
				x1AssertCanonicalAbsent(t, gcpkg.NewCassandraStore(database), fx.orgUUID, fx.blockID)
				e17AssertRefs(t, fx)
				// Real queue scan narrowed to this logical block. No candidate is created
				// for absent metadata, and no physical target is manufactured for a claim.
				scope := &e17AbsentQueue{GCStore: gcpkg.NewCassandraStore(database), org: fx.orgUUID, block: fx.blockID}
				if n, err := w2Worker(t, scope, class).ProcessOrgOnce(t.Context(), fx.orgUUID); err != nil || n != 0 || !scope.scanned {
					t.Fatalf("absent-P GC: n=%d scanned=%v err=%v", n, scope.scanned, err)
				}
				x1AssertCanonicalAbsent(t, gcpkg.NewCassandraStore(database), fx.orgUUID, fx.blockID)
				if leg == "fresh-head-before-put" {
					e17RejectedHead(t, fx, handler, commit)
				}
			} else {
				original = fx.target
				if fx.readTarget(t) != original {
					t.Fatal("RecvFS changed pre-existing original P")
				}
				e17AssertMapping(t, fx, true)
				e17AssertRefs(t, fx, sourceRef)
				e17PositiveGC(t, fx)
			}
			// Exact immutable replay is also real RecvFS; metadata and liveness must not change.
			recv()
			e17AssertMetadata(t, fx, file, root)
			w24AssertNoSyncPin(t, fx)
			var retired gcpkg.BlockDeleteAuthority
			store := gcpkg.NewCassandraStore(database)
			if leg == "reused-committed" || leg == "reused-terminal" {
				// Explicit lapse of the one source-owned temporary pin; never erase fs:.
				e17AssertRefs(t, fx, sourceRef)
				if err := database.RemoveBlockReference(fx.orgID, fx.blockID, sourceRef); err != nil {
					t.Fatal(err)
				}
				e17AssertRefs(t, fx)
				retired = e17CommitGC(t, fx)
				if leg == "reused-terminal" {
					e17Recover(t, fx, retired)
				}
				e17RejectedHead(t, fx, handler, commit)
				recv()
				e17AssertMetadata(t, fx, file, root)
				e17AssertMapping(t, fx, true)
				e17AssertRefs(t, fx)
			}
			e17OK(t, e17Request(fx, http.MethodPut, "/block/"+fx.sha1ID, "block_id", fx.sha1ID, fx.content, handler.PutBlock))
			fx.target = fx.readTarget(t)
			e17AssertMapping(t, fx, true)
			syncRef := dbpkg.BlockReferrerForUpload("sync:" + fx.repoID + ":" + fx.blockID)
			if sourceRef == "" || !retired.IsZero() {
				e17AssertRefs(t, fx, syncRef)
			} else {
				e17AssertRefs(t, fx, sourceRef, syncRef)
			}
			if !retired.IsZero() && fx.target == original {
				t.Fatalf("PutBlock reused retired P1: %+v", original)
			}
			if sourceRef != "" && retired.IsZero() && fx.target != original {
				t.Fatalf("healthy PutBlock changed P1: before=%+v after=%+v", original, fx.target)
			}
			if leg == "post-put-gc" {
				e17PositiveGC(t, fx)
			}
			// Idempotent PutBlock must keep exact P; no second physical incarnation.
			e17OK(t, e17Request(fx, http.MethodPut, "/block/"+fx.sha1ID, "block_id", fx.sha1ID, fx.content, handler.PutBlock))
			if current := fx.readTarget(t); current != fx.target {
				t.Fatalf("PutBlock replay changed P: %+v -> %+v", fx.target, current)
			}
			e17AssertMetadata(t, fx, file, root)
			e17OK(t, e17Request(fx, http.MethodPut, "/commit/HEAD?head="+commit, "commit_id", "HEAD", nil, handler.PutCommit))
			if head := borrowedFSReadHead(t, database, fx.orgID, fx.repoID); head != commit {
				t.Fatalf("HEAD=%s want=%s", head, commit)
			}
			w24AssertHeadReaches(t, fx, commit, file)
			e17AssertMetadata(t, fx, file, root)
			permanent := dbpkg.BlockReferrerForFSObject(fx.repoID, file)
			if sourceRef != "" && retired.IsZero() {
				e17AssertRefs(t, fx, sourceRef, syncRef, permanent)
			} else {
				e17AssertRefs(t, fx, syncRef, permanent)
			}
			var fsTTL int
			if err := database.Session().Query(`SELECT TTL(created_at) FROM block_references WHERE org_id = ? AND block_id = ? AND referrer = ?`, fx.orgID, fx.blockID, permanent).Scan(&fsTTL); err != nil || fsTTL != 0 {
				t.Fatalf("permanent fs TTL=%d err=%v", fsTTL, err)
			}
			var upTTL int
			if err := database.Session().Query(`SELECT TTL(created_at) FROM block_references WHERE org_id = ? AND block_id = ? AND referrer = ?`, fx.orgID, fx.blockID, syncRef).Scan(&upTTL); err != nil || upTTL < 170000 {
				t.Fatalf("Sync up TTL=%d err=%v", upTTL, err)
			}
			if rows := w2Repairs(t, fx); len(rows) != 0 {
				t.Fatalf("settled repair remains: %+v", rows)
			}
			trace.mu.Lock()
			writes := append([]string(nil), trace.settlementWrites...)
			repairs := trace.repairWrites
			errs := append([]string(nil), trace.queryErrors...)
			trace.mu.Unlock()
			if repairs != 1 || len(writes) != 2 || writes[0] != "fs:" || writes[1] != "repair-delete" || len(errs) != 0 {
				t.Fatalf("productive settlement trace: repairs=%d writes=%v errors=%v", repairs, writes, errs)
			}
			// Replaying the same HEAD is no-op for placement/publication.
			e17OK(t, e17Request(fx, http.MethodPut, "/commit/HEAD?head="+commit, "commit_id", "HEAD", nil, handler.PutCommit))
			if current := fx.readTarget(t); current != fx.target {
				t.Fatalf("HEAD replay changed P: %+v", current)
			}
			if borrowedFSReadHead(t, database, fx.orgID, fx.repoID) != commit {
				t.Fatal("HEAD replay moved HEAD")
			}
			e17AssertMapping(t, fx, true)
			if rows := w2Repairs(t, fx); len(rows) != 0 {
				t.Fatalf("HEAD replay left repair: %+v", rows)
			}
			trace.mu.Lock()
			replayRepairs, replayWrites := trace.repairWrites, len(trace.settlementWrites)
			trace.mu.Unlock()
			if replayRepairs != repairs || replayWrites != len(writes) {
				t.Fatal("HEAD replay repeated settlement")
			}
			w2AssertBytes(t, fx)
			e17OKBytes(t, e17Request(fx, http.MethodGet, "/block/"+fx.sha1ID, "block_id", fx.sha1ID, nil, handler.GetBlock), fx.content)
			if leg == "reused-committed" {
				// Complete old D1 after P2 is reachable: only K1 may be deleted.
				e17Recover(t, fx, retired)
				w2AssertBytes(t, fx)
				if fx.readTarget(t) != fx.target {
					t.Fatal("old D recovery changed new P2")
				}
			}
			if retired.IsZero() {
				e12AssertNoDeleteLifecycle(t, fx)
			} else {
				if _, found, err := store.GetS3OrphanExact(fx.orgUUID, fx.blockID, retired); err != nil || found {
					t.Fatalf("old orphan remains: %v %v", found, err)
				}
			}
			t.Logf("EVIDENCE %s: metadata-only RecvFS; original=%+v final=%+v HEAD=%s; repair/fs settlement ordered", leg, original, fx.target, commit)
			if !t.Failed() {
				e17Evidence[leg] = true
			}
		})
	}
}

func e17Request(fx *w2CreateFileFixture, method, suffix, param, value string, body []byte, handler func(*gin.Context)) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, "/seafhttp/repo/"+fx.repoID+suffix, bytes.NewReader(body))
	if strings.HasPrefix(suffix, "/commit/") {
		c.Request.Header.Set("Content-Type", "application/json")
	} else {
		c.Request.Header.Set("Content-Type", "application/octet-stream")
	}
	c.Params = gin.Params{{Key: "repo_id", Value: fx.repoID}}
	if param != "" {
		c.Params = append(c.Params, gin.Param{Key: param, Value: value})
	}
	c.Set("org_id", fx.orgID)
	c.Set("user_id", fx.userID)
	handler(c)
	return rec
}
func e17OK(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("productive Sync: %d %s", rec.Code, rec.Body.String())
	}
}
func e17OKBytes(t *testing.T, rec *httptest.ResponseRecorder, want []byte) {
	t.Helper()
	e17OK(t, rec)
	if !bytes.Equal(rec.Body.Bytes(), want) {
		t.Fatalf("Sync bytes mismatch: %q", rec.Body.Bytes())
	}
}
func e17AssertMapping(t *testing.T, fx *w2CreateFileFixture, present bool) {
	t.Helper()
	internal, found, err := fx.database.GetBlockIDMapping(fx.orgID, dbpkg.PlainBlockRepresentationID, fx.sha1ID)
	if err != nil || found != present || (present && internal != fx.blockID) {
		t.Fatalf("mapping internal=%s found=%v want=%v err=%v", internal, found, present, err)
	}
}
func e17AssertRefs(t *testing.T, fx *w2CreateFileFixture, expected ...string) {
	t.Helper()
	refs, err := fx.database.ListBlockReferrers(fx.orgID, fx.blockID)
	if err != nil || len(refs) != len(expected) {
		t.Fatalf("refs=%v want=%v err=%v", refs, expected, err)
	}
	for _, ref := range expected {
		if !containsStringForTest(refs, ref) {
			t.Fatalf("missing exact ref %s in %v", ref, refs)
		}
	}
}
func e17AssertMetadata(t *testing.T, fx *w2CreateFileFixture, file, root string) {
	t.Helper()
	var ids, paired []string
	var size int64
	if err := fx.database.Session().Query(`SELECT block_ids, seafile_block_ids_sha1, size_bytes FROM fs_objects WHERE library_id = ? AND fs_id = ?`, fx.repoID, file).Scan(&ids, &paired, &size); err != nil || len(ids) != 1 || ids[0] != fx.sha1ID || len(paired) != 0 || size != int64(len(fx.content)) {
		t.Fatalf("SHA-1 metadata ids=%v paired=%v size=%d err=%v", ids, paired, size, err)
	}
	var entries string
	if err := fx.database.Session().Query(`SELECT dir_entries FROM fs_objects WHERE library_id = ? AND fs_id = ?`, fx.repoID, root).Scan(&entries); err != nil || !strings.Contains(entries, file) || !strings.Contains(entries, fx.filename) {
		t.Fatalf("root metadata=%s err=%v", entries, err)
	}
	var dirents []apipkg.FSEntry
	if err := json.Unmarshal([]byte(entries), &dirents); err != nil || len(dirents) != 1 || dirents[0].ID != file || dirents[0].Name != fx.filename || dirents[0].Size != int64(len(fx.content)) {
		t.Fatalf("exact root dirent: %+v err=%v", dirents, err)
	}
}
func e17RejectedHead(t *testing.T, fx *w2CreateFileFixture, handler *apipkg.SyncHandler, commit string) {
	t.Helper()
	rec := e17Request(fx, http.MethodPut, "/commit/HEAD?head="+commit, "commit_id", "HEAD", nil, handler.PutCommit)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("pre-PutBlock HEAD should fail closed: %d %s", rec.Code, rec.Body.String())
	}
	fx.assertHeadUnchanged(t)
	e17AssertRefs(t, fx)
	if rows := w2Repairs(t, fx); len(rows) != 0 {
		t.Fatalf("rejected early HEAD left repair: %+v", rows)
	}
}

// Narrow real dequeue results; fresh RecvFS has no materialized target/candidate.
type e17AbsentQueue struct {
	gcpkg.GCStore
	org     uuid.UUID
	block   string
	scanned bool
}

func (s *e17AbsentQueue) DequeueBatch(org uuid.UUID, _ int, cutoff time.Time) ([]gcpkg.QueueItem, error) {
	rows, err := s.GCStore.DequeueBatch(org, 100000, cutoff)
	s.scanned = true
	for _, row := range rows {
		if row.OrgID == s.org && row.ItemType == gcpkg.ItemBlock && row.ItemID == s.block {
			return nil, fmt.Errorf("fresh metadata unexpectedly has block candidate: %+v", row)
		}
	}
	return nil, err
}
func e17PositiveGC(t *testing.T, fx *w2CreateFileFixture) {
	t.Helper()
	target := fx.readTarget(t)
	expectedRefs, err := fx.database.ListBlockReferrers(fx.orgID, fx.blockID)
	if err != nil || len(expectedRefs) != 1 || !strings.HasPrefix(expectedRefs[0], "up:") {
		t.Fatalf("positive proof prerequisite: %v %v", expectedRefs, err)
	}
	// A peer may consume this discovery identity before our session reaches it.
	// Re-discover at most three times; never count a missing probe as evidence.
	for attempt := 0; attempt < 3; attempt++ {
		proof := &e16WorkerObserver{e13ProofObserver: e13Observer(fx.orgID, fx.blockID, "e17-positive")}
		workerDB := w2EvidenceSession(t, splitEnvOrDefault("CASSANDRA_HOSTS", "cassandra:9042")[0], proof)
		store := gcpkg.NewCassandraStore(workerDB)
		c := w2Candidate(t, store, fx.orgUUID, fx.blockID, target.StorageClass)
		scope := &w2ClosureOwnedQueue{GCStore: store, org: fx.orgUUID, block: fx.blockID, identity: c.Identity()}
		n, workerErr := w2Worker(t, scope, target.StorageClass).ProcessOrgOnce(t.Context(), fx.orgUUID)
		proof.mu.Lock()
		local := append([]int(nil), proof.localRows...)
		errs := append([]string(nil), proof.queryErrors...)
		globalReads, repairBuckets := len(proof.refs), len(proof.buckets)
		proof.mu.Unlock()
		_, found, candidateErr := store.GetBlockGCCandidateExact(fx.orgUUID, fx.blockID, c.Identity())
		if candidateErr != nil || scope.visited || globalReads != 0 || repairBuckets != 0 || len(errs) != 0 {
			t.Fatalf("positive proof changed/faulted: candidate=%v err=%v global=%v refs=%d buckets=%d errors=%v", found, candidateErr, scope.visited, globalReads, repairBuckets, errs)
		}
		e17AssertRefs(t, fx, expectedRefs...)
		if fx.readTarget(t) != target {
			t.Fatal("peer changed protected exact P")
		}
		e12AssertNoDeleteLifecycle(t, fx)
		if workerErr == nil && n == 1 && len(local) == 1 && local[0] == 1 && !found {
			return
		}
		peerConsumed := !found && len(local) == 0 && ((workerErr == nil && n == 1) || (workerErr != nil && strings.Contains(workerErr.Error(), "expected exactly one owned real queue row, found 0")))
		if !peerConsumed {
			t.Fatalf("positive probe not observed: n=%d err=%v local=%v candidate=%v", n, workerErr, local, found)
		}
		t.Logf("peer consumed discovery attempt %d; bounded re-discovery still requires our actual positive reference read", attempt+1)
	}
	t.Fatal("three discovery attempts failed to obtain a real positive GC probe")
}
func e17CommitGC(t *testing.T, fx *w2CreateFileFixture) gcpkg.BlockDeleteAuthority {
	t.Helper()
	proof := e13Observer(fx.orgID, fx.blockID, "e17-zero")
	workerDB := w2EvidenceSession(t, splitEnvOrDefault("CASSANDRA_HOSTS", "cassandra:9042")[0], proof)
	// Preserve the observed native session, supplying the full validated fixture
	// topology to the productive gate. All current callers already load config.
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	workerDB = dbpkg.NewPublicationEvidenceDB(workerDB.Session(), cfg.Database)
	store := gcpkg.NewCassandraStore(workerDB)
	c := w2Candidate(t, store, fx.orgUUID, fx.blockID, fx.target.StorageClass)
	scope := &w2ClosureOwnedQueue{GCStore: store, org: fx.orgUUID, block: fx.blockID, identity: c.Identity()}
	if n, err := w2Worker(t, scope, fx.target.StorageClass).ProcessOrgOnce(t.Context(), fx.orgUUID); err != nil || n != 1 || !scope.visited {
		t.Fatalf("GC retirement n=%d proof=%v err=%v", n, scope.visited, err)
	}
	proof.mu.Lock()
	refs := append([]int(nil), proof.refs...)
	buckets := len(proof.buckets)
	errs := append([]string(nil), proof.queryErrors...)
	proof.mu.Unlock()
	if len(refs) != 2 || refs[0] != 0 || refs[1] != 0 || buckets != dbpkg.PublishedBlockReferenceRepairBuckets || len(errs) != 0 {
		t.Fatalf("real zero proof refs=%v repairBuckets=%d errors=%v", refs, buckets, errs)
	}
	var a gcpkg.BlockDeleteAuthority
	var phase string
	if err := fx.database.Session().Query(`SELECT claim_id, claimed_at, storage_class, storage_key, phase FROM gc_block_delete_lifecycles WHERE org_id = ? AND block_id = ?`, fx.orgID, fx.blockID).Consistency(gocql.EachQuorum).Scan(&a.ClaimID, &a.ClaimedAt, &a.Target.StorageClass, &a.Target.StorageKey, &phase); err != nil || phase != gcpkg.BlockDeleteLifecyclePhasePublished || a.Target != fx.target {
		t.Fatalf("D1 lifecycle %+v phase=%s err=%v", a, phase, err)
	}
	orphan, found, err := store.GetS3OrphanExact(fx.orgUUID, fx.blockID, a)
	if err != nil || !found || orphan.RecoveryState != gcpkg.S3OrphanRecoveryStateCommitted || orphan.StorageClass != fx.target.StorageClass || orphan.StorageKey != fx.target.StorageKey {
		t.Fatalf("exact COMMITTED orphan %+v found=%v err=%v", orphan, found, err)
	}
	if _, found, err := store.GetS3OrphanRecoveryRootExact(fx.orgUUID, fx.blockID, a); err != nil || !found {
		t.Fatalf("exact recovery root: %v %v", found, err)
	}
	x1AssertCanonicalAbsent(t, store, fx.orgUUID, fx.blockID)
	w2AssertBytes(t, fx)
	return a
}
func e17Recover(t *testing.T, fx *w2CreateFileFixture, a gcpkg.BlockDeleteAuthority) {
	t.Helper()
	store := gcpkg.NewCassandraStore(fx.database)
	scope := &w2OwnedRecoveryStore{GCStore: store, org: fx.orgUUID, block: fx.blockID}
	n, err := w2Worker(t, scope, a.Target.StorageClass).RecoverS3Orphans(t.Context(), 1000)
	if err != nil || n < 0 || n > 1 {
		t.Fatalf("exact recovery n=%d err=%v", n, err)
	}
	// A peer may finish this exact root. A zero visit count is accepted only after
	// the same durable terminal certificate and physical/root checks as our visit.
	deadline := time.Now().Add(20 * time.Second)
	for {
		var phase, class, key string
		var claimedAt time.Time
		if err := fx.database.Session().Query(`SELECT phase, storage_class, storage_key, claimed_at FROM gc_block_delete_lifecycles WHERE org_id = ? AND block_id = ? AND claim_id = ?`, fx.orgID, fx.blockID, a.ClaimID).Consistency(gocql.Serial).Scan(&phase, &class, &key, &claimedAt); err != nil || class != a.Target.StorageClass || key != a.Target.StorageKey || !claimedAt.Equal(a.ClaimedAt) || (phase != gcpkg.BlockDeleteLifecyclePhasePublished && phase != gcpkg.BlockDeleteLifecyclePhaseTerminal) {
			t.Fatalf("exact lifecycle phase=%s P=%s/%s claimed=%s err=%v", phase, class, key, claimedAt, err)
		}
		exists, objectErr := newVerificationS3Store(t).Exists(t.Context(), a.Target.StorageKey)
		_, orphanFound, orphanErr := store.GetS3OrphanExact(fx.orgUUID, fx.blockID, a)
		_, rootFound, rootErr := store.GetS3OrphanRecoveryRootExact(fx.orgUUID, fx.blockID, a)
		if objectErr != nil || orphanErr != nil || rootErr != nil {
			t.Fatalf("terminal observation failed: object=%v orphan=%v root=%v", objectErr, orphanErr, rootErr)
		}
		if phase == gcpkg.BlockDeleteLifecyclePhaseTerminal && !exists && !orphanFound && !rootFound {
			if n == 0 {
				t.Log("peer completed exact D1; SERIAL terminal tuple/time, absent K1/orphan/root certified")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("exact terminal certificate missing: phase=%s K1=%v orphan=%v root=%v visits=%d", phase, exists, orphanFound, rootFound, n)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
func TestE17SyncRecvFSEvidenceRequiresEveryNamedLeg(t *testing.T) {
	required := []string{"fresh", "fresh-head-before-put", "reused", "reused-committed", "reused-terminal", "post-put-gc"}
	all := map[string]bool{}
	for _, leg := range required {
		all[leg] = true
	}
	if len(e17Legs) != len(required) || len(e17Missing(nil)) != len(required) || len(e17Missing(all)) != 0 {
		t.Fatal("six named E1-7 legs must not shrink")
	}
	for _, leg := range required {
		delete(all, leg)
		if missing := e17Missing(all); len(missing) != 1 || missing[0] != leg {
			t.Fatalf("missing %s hidden: %v", leg, missing)
		}
		all[leg] = true
	}
}
