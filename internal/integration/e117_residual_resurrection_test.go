//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	apipkg "github.com/Sesame-Disk/sesamefs/internal/api"
	v2pkg "github.com/Sesame-Disk/sesamefs/internal/api/v2"
	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const e117EvidenceEnv = "SESAMEFS_REQUIRE_E117_RESIDUAL_RESURRECTION_EVIDENCE"

// E1-17 measures the W2-10 handlers E1-10e did not cover, with the same
// unpublished-source schedule: real web upload -> Sync PutCommit/RecvFS (no
// fs:/repair) -> owned temporary up: lapse -> real GC to COMMITTED/TERMINAL.
var (
	e117Shapes   = []string{"revertfile", "restoretrash-file", "restoretrash-dir", "revertdirectory", "revertdirents-dir"}
	e117States   = []string{"live-unpublished", "committed", "terminal"}
	e117Evidence = map[string]bool{}
)

func e117Legs() []string {
	var legs []string
	for _, shape := range e117Shapes {
		for _, state := range e117States {
			legs = append(legs, shape+"/"+state)
		}
	}
	return legs
}

func e117Missing(observed map[string]bool) []string {
	var missing []string
	for _, leg := range e117Legs() {
		if !observed[leg] {
			missing = append(missing, leg)
		}
	}
	return missing
}

func e117IsDirShape(shape string) bool {
	return strings.HasSuffix(shape, "-dir") || shape == "revertdirectory"
}

func TestE117ResidualResurrectionSourceAdmission(t *testing.T) {
	if endpoint := os.Getenv("SESAMEFS_E117_ISOLATED_URL"); endpoint != "" && os.Getenv("SESAMEFS_E117_CHILD") != "1" {
		e117RunIsolated(t, endpoint)
		return
	}
	requireCassandra(t)
	if os.Getenv("SESAMEFS_TEST_IN_CONTAINER") != "1" {
		t.Fatal("E1-17 requires Docker")
	}
	for _, endpoint := range []string{superadminClient.baseURL, envOrDefault("SESAMEFS_URL_2", "http://sesamefs-node-2:8080"), envOrDefault("SESAMEFS_URL_3", "http://sesamefs-node-3:8080")} {
		if err := e19CheckGCDisabled(newTestClient(endpoint, superadminClient.token)); err != nil {
			t.Fatalf("E1-17 owned-worker isolation: %v", err)
		}
	}
	for _, shape := range e117Shapes {
		t.Run(shape, func(t *testing.T) {
			for _, state := range e117States {
				t.Run(state, func(t *testing.T) {
					e117Leg(t, shape, state)
					if !t.Failed() {
						e117Evidence[shape+"/"+state] = true
					}
				})
			}
		})
	}
}

func e117Leg(t *testing.T, shape, state string) {
	database := shareProjectionDBForTest(t)
	class := x1StorageClass(t)
	web := newSessionUploadHeadFixture(t, database, newBorrowedFSHeadHandler(t, database, class))
	fx := &w2CreateFileFixture{w2UploadFileFixture: &w2UploadFileFixture{borrowedFSHeadFixture: web.borrowedFSHeadFixture}}
	sourceRef := web.sessionRef
	var retired gcpkg.BlockDeleteAuthority
	// Canonical metadata disappears at COMMITTED: retain exact K for teardown.
	cleanupStore := newVerificationS3Store(t)
	cleanupKey := fx.target.StorageKey
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := cleanupStore.Delete(ctx, cleanupKey); err != nil {
			t.Errorf("cleanup exact K: %v", err)
			return
		}
		if found, err := cleanupStore.Exists(ctx, cleanupKey); err != nil || found {
			t.Errorf("cleanup exact K remains: %v %v", found, err)
			return
		}
		e1DisposeOwnedRecovery(t, fx, retired)
	})
	x1Cleanup(t, database, fx.orgUUID, fx.blockID)
	syncRef := dbpkg.BlockReferrerForUpload("sync:" + fx.repoID + ":" + fx.blockID)
	cleanupUploadedBlockArtifactsForTest(t, fx.orgID, fx.repoID, fx.blockID, fx.sha1ID, sourceRef, syncRef)
	syncHandler := w24Handler(t, database, class)

	// Unpublished Sync tree: root -> file, or root -> directory -> file.
	isDir := e117IsDirShape(shape)
	dirName := "e117-dir-" + uuid.NewString()[:8]
	fileJSON := mustMarshalSyncObjectForTest(t, map[string]interface{}{"block_ids": []string{fx.sha1ID}, "size": len(fx.content), "type": 1, "version": 1})
	file := syncSHA1HexForTest(fileJSON)
	fileEntry := apipkg.FSEntry{ID: file, Mode: 33188, Mtime: time.Now().Unix(), Name: fx.filename, Size: int64(len(fx.content))}
	objects := []syncPackedFSObject{{fsID: file, jsonData: fileJSON}}
	rootEntries := []apipkg.FSEntry{fileEntry}
	dir := ""
	if isDir {
		dirJSON := mustMarshalSyncObjectForTest(t, map[string]interface{}{"dirents": []apipkg.FSEntry{fileEntry}, "type": 3, "version": 1})
		dir = syncSHA1HexForTest(dirJSON)
		objects = append(objects, syncPackedFSObject{fsID: dir, jsonData: dirJSON})
		rootEntries = []apipkg.FSEntry{{ID: dir, Mode: 16384, Mtime: time.Now().Unix(), Name: dirName}}
	}
	rootJSON := mustMarshalSyncObjectForTest(t, map[string]interface{}{"dirents": rootEntries, "type": 3, "version": 1})
	root := syncSHA1HexForTest(rootJSON)
	objects = append(objects, syncPackedFSObject{fsID: root, jsonData: rootJSON})
	commit := syncSHA1HexForTest([]byte("e117-" + uuid.NewString()))
	payload := mustMarshalSyncObjectForTest(t, map[string]interface{}{"commit_id": commit, "repo_id": fx.repoID, "root_id": root, "parent_id": fx.headBefore, "description": "E1-17 unpublished source", "ctime": time.Now().Unix(), "version": 1})
	e17OK(t, e17Request(fx, http.MethodPut, "/commit/"+commit, "commit_id", commit, payload, syncHandler.PutCommit))
	e17OK(t, e17Request(fx, http.MethodPost, "/recv-fs", "", "", packSyncFSObjectsForTest(t, objects...), syncHandler.RecvFS))
	var storedRoot, parent string
	if err := database.Session().Query(`SELECT root_fs_id, parent_id FROM commits WHERE library_id = ? AND commit_id = ?`, fx.repoID, commit).Scan(&storedRoot, &parent); err != nil || storedRoot != root || parent != fx.headBefore {
		t.Fatalf("unpublished commit root=%s parent=%s err=%v", storedRoot, parent, err)
	}
	e17AssertMapping(t, fx, true)
	e17AssertRefs(t, fx, sourceRef)
	fx.assertHeadUnchanged(t)
	w2AssertBytes(t, fx)
	if rows := w2Repairs(t, fx); len(rows) != 0 {
		t.Fatalf("unexpected repair %+v", rows)
	}

	original := fx.target
	if state == "committed" || state == "terminal" {
		// Owned temporary lapse only; never delete a historical permanent fs:.
		if err := database.RemoveBlockReference(fx.orgID, fx.blockID, sourceRef); err != nil {
			t.Fatal(err)
		}
		e17AssertRefs(t, fx)
		retired = e17CommitGC(t, fx)
		if retired.Target != original {
			t.Fatal("retired wrong P")
		}
		if state == "terminal" {
			e17Recover(t, fx, retired)
		}
	}

	trace := e13Observer(fx.orgID, fx.blockID, "e117-writer")
	writerDB := w2EvidenceSession(t, splitEnvOrDefault("CASSANDRA_HOSTS", "cassandra:9042")[0], trace)
	itemPath := "/" + fx.filename
	historicalID := file
	if isDir {
		itemPath = "/" + dirName
		historicalID = dir
	}
	historicalVisits, headVisits := 0, 0
	onHistorical := func(fsID string) {
		historicalVisits++
		if fsID != historicalID {
			t.Fatalf("wrong historical entry %s, want %s", fsID, historicalID)
		}
		fx.assertHeadUnchanged(t)
	}
	onHead := func() { headVisits++ }
	switch shape {
	case "revertfile":
		t.Cleanup(v2pkg.SetRevertFilePublicationBarriersForTest(fx.repoID, onHistorical, onHead))
	case "restoretrash-file", "restoretrash-dir":
		t.Cleanup(v2pkg.SetRestoreTrashPublicationBarriersForTest(fx.repoID, onHistorical, onHead))
	case "revertdirectory":
		t.Cleanup(v2pkg.SetRevertDirectoryPublicationBarriersForTest(fx.repoID, onHistorical, onHead))
	case "revertdirents-dir":
		t.Cleanup(v2pkg.SetRevertDirentsPublicationBarriersForTest(fx.repoID, func(path, fsID string) {
			if path != itemPath {
				t.Fatalf("wrong item path %s", path)
			}
			onHistorical(fsID)
		}, func(string) { onHead() }))
	}

	rec := e117Invoke(t, fx, shape, writerDB, class, commit, itemPath)
	actualHead := borrowedFSReadHead(t, database, fx.orgID, fx.repoID)
	t.Logf("E1-17 %s/%s response=%d %s HEAD before=%s after=%s historical=%d HEAD attempts=%d exact P1=%+v D=%+v",
		shape, state, rec.Code, strings.TrimSpace(rec.Body.String()), fx.headBefore, actualHead, historicalVisits, headVisits, original, retired)
	if actualHead != fx.headBefore {
		// The unsafe outcome: HEAD now reaches the historical file.
		e117AssertHeadReaches(t, fx, actualHead, dirName, isDir, file)
		t.Fatalf("E1-17 VIOLATION %s/%s: unpublished historical source published HEAD %s (exact P1 state=%s)", shape, state, actualHead, state)
	}

	// Rejected: no publication, no own liveness, source state unchanged.
	e117AssertRejectedResponse(t, shape, rec, itemPath)
	if historicalVisits != 1 || headVisits != 0 {
		t.Fatalf("admission must reject before HEAD: historical=%d HEAD=%d", historicalVisits, headVisits)
	}
	e17AssertMapping(t, fx, true)
	switch state {
	case "live-unpublished":
		e17AssertRefs(t, fx, sourceRef)
		w2AssertBytes(t, fx)
		e12AssertNoDeleteLifecycle(t, fx)
	default:
		e17AssertRefs(t, fx)
		x1AssertCanonicalAbsent(t, gcpkg.NewCassandraStore(database), fx.orgUUID, fx.blockID)
		if state == "committed" {
			e19AssertCommitted(t, fx, retired)
		}
	}
	if rows := w2Repairs(t, fx); len(rows) != 0 {
		t.Fatalf("rejected restore installed repair %+v", rows)
	}
	trace.mu.Lock()
	writes, events, errs := trace.repairWrites, len(trace.settlementWrites), len(trace.queryErrors)
	trace.mu.Unlock()
	if writes != 0 || events != 0 || errs != 0 {
		t.Fatalf("publication writes/errors %d/%d/%d", writes, events, errs)
	}
}

func e117Invoke(t *testing.T, fx *w2CreateFileFixture, shape string, writerDB *dbpkg.DB, class, commit, itemPath string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	jsonBody := func(v map[string]string) *bytes.Reader {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return bytes.NewReader(raw)
	}
	switch shape {
	case "revertfile":
		form := url.Values{"commit_id": {commit}}
		c.Request = httptest.NewRequest(http.MethodPost, "/api/v2.1/repos/"+fx.repoID+"/file/?operation=revert&p="+url.QueryEscape(itemPath), strings.NewReader(form.Encode()))
		c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	case "revertdirectory":
		c.Request = httptest.NewRequest(http.MethodPost, "/api/v2.1/repos/"+fx.repoID+"/dir/?operation=revert&p="+url.QueryEscape(itemPath), jsonBody(map[string]string{"commit_id": commit, "conflict_policy": ""}))
		c.Request.Header.Set("Content-Type", "application/json")
	case "restoretrash-file", "restoretrash-dir":
		c.Request = httptest.NewRequest(http.MethodPost, "/api/v2.1/repos/"+fx.repoID+"/file/restore/", jsonBody(map[string]string{"commit_id": commit, "p": itemPath}))
		c.Request.Header.Set("Content-Type", "application/json")
	case "revertdirents-dir":
		form := url.Values{"commit_id": {commit}, "path": {itemPath}}
		c.Request = httptest.NewRequest(http.MethodPost, "/api/v2.1/repos/"+fx.repoID+"/trash/revert-dirents/", strings.NewReader(form.Encode()))
		c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	default:
		t.Fatalf("unknown shape %s", shape)
	}
	c.Params = gin.Params{{Key: "repo_id", Value: fx.repoID}}
	c.Set("org_id", fx.orgID)
	c.Set("user_id", fx.userID)
	switch shape {
	case "revertfile":
		newBorrowedFSHeadHandler(t, writerDB, class).RevertFile(c)
	case "revertdirectory":
		newBorrowedFSHeadHandler(t, writerDB, class).RevertDirectory(c)
	case "restoretrash-file", "restoretrash-dir":
		v2pkg.NewTrashHandler(writerDB).RestoreTrashItem(c)
	case "revertdirents-dir":
		v2pkg.NewTrashHandler(writerDB).RevertDirents(c)
	}
	return rec
}

// The handlers keep their existing failure contracts: RevertDirents reports the
// item in its 200 failed list; the others answer their generic error status.
func e117AssertRejectedResponse(t *testing.T, shape string, rec *httptest.ResponseRecorder, itemPath string) {
	t.Helper()
	if shape == "revertdirents-dir" {
		var response struct {
			Success []json.RawMessage `json:"success"`
			Failed  []struct {
				Path  string `json:"path"`
				IsDir bool   `json:"is_dir"`
			} `json:"failed"`
		}
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &response) != nil ||
			len(response.Success) != 0 || len(response.Failed) != 1 || response.Failed[0].Path != itemPath || !response.Failed[0].IsDir {
			t.Fatalf("RevertDirents rejection contract: %d %s", rec.Code, rec.Body.String())
		}
		return
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("%s rejection contract: %d %s", shape, rec.Code, rec.Body.String())
	}
}

// e117AssertHeadReaches documents a RED outcome: the published HEAD tree reaches
// the historical file, directly or inside the restored directory.
func e117AssertHeadReaches(t *testing.T, fx *w2CreateFileFixture, head, dirName string, isDir bool, file string) {
	t.Helper()
	if !isDir {
		w24AssertHeadReaches(t, fx, head, file)
		return
	}
	var root, rootJSON, dirJSON string
	if err := fx.database.Session().Query(`SELECT root_fs_id FROM commits WHERE library_id = ? AND commit_id = ?`, fx.repoID, head).Scan(&root); err != nil {
		t.Fatal(err)
	}
	if err := fx.database.Session().Query(`SELECT dir_entries FROM fs_objects WHERE library_id = ? AND fs_id = ?`, fx.repoID, root).Scan(&rootJSON); err != nil {
		t.Fatal(err)
	}
	var rootEntries []apipkg.FSEntry
	if err := json.Unmarshal([]byte(rootJSON), &rootEntries); err != nil {
		t.Fatal(err)
	}
	for _, entry := range rootEntries {
		if entry.Name != dirName {
			continue
		}
		if err := fx.database.Session().Query(`SELECT dir_entries FROM fs_objects WHERE library_id = ? AND fs_id = ?`, fx.repoID, entry.ID).Scan(&dirJSON); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(dirJSON, file) {
			return
		}
	}
	t.Fatalf("published HEAD %s does not reach %s/%s: %s", head, dirName, file, rootJSON)
}

func TestE117ResidualResurrectionRequiresEveryNamedLeg(t *testing.T) {
	legs := e117Legs()
	if len(legs) != 15 || len(e117Missing(nil)) != 15 {
		t.Fatal("contract must not shrink")
	}
	all := map[string]bool{}
	for _, leg := range legs {
		all[leg] = true
	}
	if len(e117Missing(all)) != 0 {
		t.Fatal("complete evidence rejected")
	}
	for _, leg := range legs {
		delete(all, leg)
		missing := e117Missing(all)
		if len(missing) != 1 || missing[0] != leg {
			t.Fatal("missing leg not rejected")
		}
		all[leg] = true
	}
}

// Use the existing manual-GC proof keyspace; the shared dev daemon stays active.
func e117RunIsolated(t *testing.T, endpoint string) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	childRun := "^TestE117ResidualResurrectionSourceAdmission"
	// Preserve subtest filters: a filtered run must not secretly run all legs.
	if _, sub, ok := strings.Cut(flag.Lookup("test.run").Value.String(), "/"); ok {
		childRun += "/" + sub
	}
	cmd := exec.CommandContext(ctx, binary, "-test.run="+childRun, "-test.v", "-test.count=1", "-test.timeout=5m")
	for _, entry := range os.Environ() {
		name := strings.SplitN(entry, "=", 2)[0]
		if strings.HasPrefix(name, "SESAMEFS_REQUIRE_") || name == "SESAMEFS_URL" || name == "SESAMEFS_URL_2" || name == "SESAMEFS_URL_3" || name == "CASSANDRA_KEYSPACE" || strings.HasSuffix(name, "_CHILD") {
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	cmd.Env = append(cmd.Env, e117EvidenceEnv+"=1", "SESAMEFS_E117_CHILD=1", "CASSANDRA_KEYSPACE=sesamefs_e19", "SESAMEFS_URL="+endpoint, "SESAMEFS_URL_2="+endpoint, "SESAMEFS_URL_3="+endpoint)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("isolated E1-17 evidence failed: %v", err)
	}
	for _, name := range e117Missing(nil) {
		e117Evidence[name] = true
	}
}
