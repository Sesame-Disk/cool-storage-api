//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	apipkg "github.com/Sesame-Disk/sesamefs/internal/api"
	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type e1ProtectedSuccessor struct {
	org                                uuid.UUID
	repo, block, class, k1, k2, parent string
	metadata                           gcpkg.BlockInfo
	refs                               []string
	root                               gcpkg.S3OrphanRecoveryRootInfo
}

func e1InstallProtectedSuccessor(t *testing.T, database *dbpkg.DB, store *gcpkg.CassandraStore, bs *storage.BlockStore, org uuid.UUID, repo, block, class, k1 string, content []byte, invoke func([]byte, string) *httptest.ResponseRecorder) *e1ProtectedSuccessor {
	t.Helper()
	// Read the actual D1 certificate and root, rather than manufacturing D.
	var a gcpkg.BlockDeleteAuthority
	var phase string
	if err := database.Session().Query(`SELECT claim_id, claimed_at, storage_class, storage_key, phase FROM gc_block_delete_lifecycles WHERE org_id = ? AND block_id = ?`, org.String(), block).Scan(&a.ClaimID, &a.ClaimedAt, &a.Target.StorageClass, &a.Target.StorageKey, &phase); err != nil || phase != gcpkg.BlockDeleteLifecyclePhasePublished || a.Target.StorageKey != k1 || a.Target.StorageClass != class {
		t.Fatalf("H1 requires published exact D1 before P2: authority=%+v phase=%s err=%v", a, phase, err)
	}
	root, found, err := store.GetS3OrphanRecoveryRootExact(org, block, a)
	if err != nil || !found {
		t.Fatalf("H1 missing actual D1 root: found=%v err=%v", found, err)
	}
	if orphan, found, err := store.GetS3OrphanExact(org, block, a); err != nil || !found || orphan.RecoveryState != gcpkg.S3OrphanRecoveryStateCommitted {
		t.Fatalf("H1 D1 is not COMMITTED: orphan=%+v found=%v err=%v", orphan, found, err)
	}
	// The competing writer traverses the same production Sync handler; only
	// its newly minted K2 bypasses the K1-specific hold.
	rec := invoke(content, sha1hex(content))
	if rec.Code != http.StatusOK {
		t.Fatalf("H1 competing Sync writer: %d %s", rec.Code, rec.Body.String())
	}
	info, err := store.GetBlockInfo(org, block)
	if err != nil || info.StorageKey == "" || info.StorageKey == k1 || info.StorageClass != class {
		t.Fatalf("H1 missing distinct P2: %+v err=%v", info, err)
	}
	refs, err := database.ListBlockReferrers(org.String(), block)
	wantRef := apipkg.SyncBlockUploadReferrerForIntegration(repo, block)
	if err != nil || len(refs) != 1 || refs[0] != wantRef {
		t.Fatalf("H1 P2 not protected by actual upload: refs=%v err=%v", refs, err)
	}
	s := &e1ProtectedSuccessor{org: org, repo: repo, block: block, class: class, k1: k1, k2: info.StorageKey, metadata: info, refs: refs, root: root, parent: borrowedFSReadHead(t, database, org.String(), repo)}
	s.assertP2(t, database, store, bs, content, refs)
	// K1 was intentionally missing to authorize repair. The still-COMMITTED
	// orphan/root and nonterminal D1 prove DELETE has not been acknowledged.
	scope := &w2OwnedRecoveryStore{GCStore: store, org: org, block: block}
	if n, err := w2Worker(t, scope, class).RecoverS3Orphans(t.Context(), 1000); err != nil || n != 1 {
		t.Fatalf("H1 real D1 continuation: n=%d err=%v", n, err)
	}
	s.assertP2(t, database, store, bs, content, refs)
	t.Logf("H1 ordered witness: org=%s block=%s D1=%s K1=%s P2=%s protected before physical recovery", org, block, a.ClaimID, k1, s.k2)
	return s
}

func (s *e1ProtectedSuccessor) assertP2(t *testing.T, database *dbpkg.DB, store *gcpkg.CassandraStore, bs *storage.BlockStore, content []byte, refs []string) {
	t.Helper()
	info, err := store.GetBlockInfo(s.org, s.block)
	if err != nil || !reflect.DeepEqual(info, s.metadata) {
		t.Fatalf("H1 P2 metadata changed: got=%+v want=%+v err=%v", info, s.metadata, err)
	}
	var size int
	if err := database.Session().Query(`SELECT size_bytes FROM blocks WHERE org_id = ? AND block_id = ?`, s.org.String(), s.block).Scan(&size); err != nil || size != len(content) {
		t.Fatalf("H1 P2 size metadata: %d err=%v", size, err)
	}
	data, err := bs.GetBlockByStorageKey(t.Context(), s.k2)
	if err != nil || !bytes.Equal(data, content) {
		t.Fatalf("H1 K2 damaged: err=%v", err)
	}
	actual, err := database.ListBlockReferrers(s.org.String(), s.block)
	if err != nil || !reflect.DeepEqual(actual, refs) {
		t.Fatalf("H1 references changed: got=%v want=%v err=%v", actual, refs, err)
	}
}

// Only discovery replays a previously read real root. Every authoritative read,
// lifecycle transition and physical operation still uses the real store.
type e1StaleD1Discovery struct {
	*w2OwnedRecoveryStore
	root   gcpkg.S3OrphanRecoveryRootInfo
	visits int
}

func (s *e1StaleD1Discovery) ListS3OrphanRecoveryRoots(bucket int, state []byte, limit int) (gcpkg.S3OrphanRecoveryRootPage, error) {
	if bucket == dbpkg.GCDiscoveryBucket("s3-orphan-recovery-root", s.root.Authority.Target.StorageClass, s.root.Authority.Target.StorageKey, s.root.Authority.ClaimID, s.root.Authority.ClaimedAt.UTC().Format(time.RFC3339Nano)) {
		s.visits++
		return gcpkg.S3OrphanRecoveryRootPage{Roots: []gcpkg.S3OrphanRecoveryRootInfo{s.root}}, nil
	}
	return gcpkg.S3OrphanRecoveryRootPage{}, nil
}

func (s *e1ProtectedSuccessor) publishAndReplay(t *testing.T, database *dbpkg.DB, store *gcpkg.CassandraStore, bs *storage.BlockStore, handler *apipkg.SyncHandler, user string, content []byte) {
	t.Helper()
	s.assertP2(t, database, store, bs, content, s.refs)
	// No further PutBlock is allowed to hide the result of the stale request.
	fileJSON := mustMarshalSyncObjectForTest(t, map[string]interface{}{"block_ids": []string{sha1hex(content)}, "size": len(content), "type": 1, "version": 1})
	file := syncSHA1HexForTest(fileJSON)
	rootJSON := mustMarshalSyncObjectForTest(t, map[string]interface{}{"dirents": []apipkg.FSEntry{{ID: file, Mode: 33188, Mtime: time.Now().Unix(), Name: "h1.txt", Size: int64(len(content))}}, "type": 3, "version": 1})
	root := syncSHA1HexForTest(rootJSON)
	commit := syncSHA1HexForTest([]byte("h1-" + uuid.NewString()))
	payload := mustMarshalSyncObjectForTest(t, map[string]interface{}{"commit_id": commit, "repo_id": s.repo, "root_id": root, "parent_id": s.parent, "description": "H1 delayed writer", "ctime": time.Now().Unix(), "version": 1})
	invoke := func(method, path string, body []byte, params gin.Params, fn func(*gin.Context)) {
		t.Helper()
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(method, path, bytes.NewReader(body))
		c.Params = append(gin.Params{{Key: "repo_id", Value: s.repo}}, params...)
		c.Set("org_id", s.org.String())
		c.Set("user_id", user)
		fn(c)
		if rec.Code != http.StatusOK {
			t.Fatalf("H1 actual %s continuation: %d %s", path, rec.Code, rec.Body.String())
		}
	}
	invoke(http.MethodPut, "/seafhttp/repo/"+s.repo+"/commit/"+commit, payload, gin.Params{{Key: "commit_id", Value: commit}}, handler.PutCommit)
	invoke(http.MethodPost, "/seafhttp/repo/"+s.repo+"/recv-fs", packSyncFSObjectsForTest(t, syncPackedFSObject{fsID: file, jsonData: fileJSON}, syncPackedFSObject{fsID: root, jsonData: rootJSON}), nil, handler.RecvFS)
	invoke(http.MethodPost, "/seafhttp/repo/"+s.repo+"/update-branch?head="+commit, nil, nil, handler.UpdateBranch)
	if head := borrowedFSReadHead(t, database, s.org.String(), s.repo); head != commit {
		t.Fatalf("H1 published HEAD mismatch: %s", head)
	}
	var persistedRoot string
	if err := database.Session().Query(`SELECT root_fs_id FROM commits WHERE library_id = ? AND commit_id = ?`, s.repo, commit).Scan(&persistedRoot); err != nil || persistedRoot != root {
		t.Fatalf("H1 commit root mismatch: %s err=%v", persistedRoot, err)
	}
	var entriesJSON string
	if err := database.Session().Query(`SELECT dir_entries FROM fs_objects WHERE library_id = ? AND fs_id = ?`, s.repo, root).Scan(&entriesJSON); err != nil {
		t.Fatal(err)
	}
	var entries []apipkg.FSEntry
	if err := json.Unmarshal([]byte(entriesJSON), &entries); err != nil || len(entries) != 1 || entries[0].ID != file {
		t.Fatalf("H1 root does not reach exact file: %s err=%v", entriesJSON, err)
	}
	var storedBlocks []string
	if err := database.Session().Query(`SELECT block_ids FROM fs_objects WHERE library_id = ? AND fs_id = ?`, s.repo, file).Scan(&storedBlocks); err != nil || len(storedBlocks) != 1 || (storedBlocks[0] != s.block && storedBlocks[0] != sha1hex(content)) {
		t.Fatalf("H1 file does not resolve logical block: %v err=%v", storedBlocks, err)
	}
	permanent := dbpkg.BlockReferrerForFSObject(s.repo, file)
	if exists, err := database.BlockReferenceExistsEachQuorum(s.org.String(), s.block, permanent); err != nil || !exists {
		t.Fatalf("H1 HEAD missing permanent protection: exists=%v err=%v", exists, err)
	}
	refs, err := database.ListBlockReferrers(s.org.String(), s.block)
	if err != nil {
		t.Fatal(err)
	}
	s.assertP2(t, database, store, bs, content, refs)
	// Remove only the explicitly owned permanent reference at teardown.
	t.Cleanup(func() {
		if err := database.RemoveBlockReference(s.org.String(), s.block, permanent); err != nil {
			t.Errorf("H1 permanent-ref cleanup: %v", err)
		}
	})
	assertDownload := func() {
		t.Helper()
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/seafhttp/repo/"+s.repo+"/block/"+sha1hex(content), nil)
		c.Params = gin.Params{{Key: "repo_id", Value: s.repo}, {Key: "block_id", Value: sha1hex(content)}}
		c.Set("org_id", s.org.String())
		c.Set("user_id", user)
		handler.GetBlock(c)
		if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), content) {
			t.Fatalf("H1 canonical download: %d %s", rec.Code, rec.Body.String())
		}
	}
	assertDownload()
	for i := 0; i < 2; i++ {
		replay := &e1StaleD1Discovery{w2OwnedRecoveryStore: &w2OwnedRecoveryStore{GCStore: gcpkg.NewCassandraStore(database), org: s.org, block: s.block}, root: s.root}
		// A cached discovery row is not the durable token. After cleanup, the
		// real store refuses settlement without it; require that exact failure.
		wantError := fmt.Sprintf("recover root org=%s block=%s: refusing to settle exact S3 orphan org=%s block=%s without a durable recovery-root token", s.org, s.block, s.org, s.block)
		if n, err := w2Worker(t, replay, s.class).RecoverS3Orphans(t.Context(), 1000); err == nil || err.Error() != wantError || n != 0 || replay.visits != 1 {
			t.Fatalf("H1 stale D1 replay: n=%d visits=%d err=%v; want exact missing-token refusal", n, replay.visits, err)
		}
		if n, err := w2Worker(t, &w2OwnedRecoveryStore{GCStore: store, org: s.org, block: s.block}, s.class).RecoverS3Orphans(t.Context(), 1000); err != nil || n != 0 {
			t.Fatalf("H1 fresh durable discovery: n=%d err=%v", n, err)
		}
		s.assertP2(t, database, store, bs, content, refs)
		assertDownload()
		if head := borrowedFSReadHead(t, database, s.org.String(), s.repo); head != commit {
			t.Fatalf("H1 replay changed HEAD: %s", head)
		}
		if data, err := bs.GetBlockByStorageKey(t.Context(), s.k1); err != nil || !bytes.Equal(data, content) {
			t.Fatalf("H1 replay did not retain late K1 residue: err=%v", err)
		}
		var phase string
		if err := database.Session().Query(`SELECT phase FROM gc_block_delete_lifecycles WHERE org_id = ? AND block_id = ? AND claim_id = ?`, s.org.String(), s.block, s.root.Authority.ClaimID).Scan(&phase); err != nil || phase != gcpkg.BlockDeleteLifecyclePhaseTerminal {
			t.Fatalf("H1 replay changed terminal D1: %s err=%v", phase, err)
		}
		if _, found, err := store.GetS3OrphanExact(s.org, s.block, s.root.Authority); err != nil || found {
			t.Fatalf("H1 replay republished orphan: found=%v err=%v", found, err)
		}
		if _, found, err := store.GetS3OrphanRecoveryRootExact(s.org, s.block, s.root.Authority); err != nil || found {
			t.Fatalf("H1 replay republished root: found=%v err=%v", found, err)
		}
	}
	// A reachable HEAD must remain readable with P1 physically unavailable,
	// not just return matching bytes while the late K1 residue also exists.
	if err := bs.DeleteBlockByStorageKey(t.Context(), s.k1); err != nil {
		t.Fatal(err)
	}
	if exists, err := bs.ObjectExists(t.Context(), s.k1); err != nil || exists {
		t.Fatalf("H1 cannot prove P1-independent HEAD: K1 exists=%v err=%v", exists, err)
	}
	assertDownload()
	s.assertP2(t, database, store, bs, content, refs)
	// The physical K1 residue is an owned leak, never an accepted canonical life.
	t.Cleanup(func() {
		if err := bs.DeleteBlockByStorageKey(context.Background(), s.k1); err != nil {
			t.Errorf("H1 K1 residue cleanup: %v", err)
		}
		if exists, err := bs.ObjectExists(context.Background(), s.k1); err != nil || exists {
			t.Errorf("H1 K1 survived cleanup: %v %v", exists, err)
		}
	})
	t.Logf("H1 PASS: HEAD=%s root=%s file=%s resolves P2=%s; fs protection present; two stale D1 replays refused safely; fresh discovery empty", commit, root, file, s.k2)
}

// Registered before the owned row/object cleanup callbacks, so these are
// independent post-cleanup reads, not merely successful DELETE responses.
func (s *e1ProtectedSuccessor) assertCleanup(t *testing.T, database *dbpkg.DB, store *gcpkg.CassandraStore, bs *storage.BlockStore) {
	t.Helper()
	for _, key := range []string{s.k1, s.k2} {
		if exists, err := bs.ObjectExists(context.Background(), key); err != nil || exists {
			t.Errorf("H1 teardown object remains: %s exists=%v err=%v", key, exists, err)
		}
	}
	if exists, err := store.BlockExists(s.org, s.block); err != nil || exists {
		t.Errorf("H1 teardown canonical exists=%v err=%v", exists, err)
	}
	if refs, err := database.ListBlockReferrers(s.org.String(), s.block); err != nil || len(refs) != 0 {
		t.Errorf("H1 teardown references=%v err=%v", refs, err)
	}
	if _, found, err := store.GetS3OrphanExact(s.org, s.block, s.root.Authority); err != nil || found {
		t.Errorf("H1 teardown orphan found=%v err=%v", found, err)
	}
	if _, found, err := store.GetS3OrphanRecoveryRootExact(s.org, s.block, s.root.Authority); err != nil || found {
		t.Errorf("H1 teardown root found=%v err=%v", found, err)
	}
	var claim string
	if err := database.Session().Query(`SELECT claim_id FROM gc_block_delete_lifecycles WHERE org_id = ? AND block_id = ? AND claim_id = ?`, s.org.String(), s.block, s.root.Authority.ClaimID).Scan(&claim); !errors.Is(err, gocql.ErrNotFound) {
		t.Errorf("H1 teardown lifecycle remains: %s err=%v", claim, err)
	}
	t.Logf("H1 cleanup checked owned P1/P2 objects, canonical, refs, orphan/root and lifecycle: org=%s block=%s", s.org, s.block)
}
