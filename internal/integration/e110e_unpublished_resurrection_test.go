//go:build integration

package integration

import (
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
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const e110eEvidenceEnv = "SESAMEFS_REQUIRE_E110E_UNPUBLISHED_RESURRECTION_EVIDENCE"

var e110eEvidence = map[string]bool{}
var e110eLegs = []string{"live-unpublished", "committed", "terminal", "published-sync-history"}

func e110eMissing(observed map[string]bool) []string {
	var missing []string
	for _, leg := range e110eLegs {
		if !observed[leg] {
			missing = append(missing, leg)
		}
	}
	return missing
}

func TestUnpublishedResurrectionSourceAdmission(t *testing.T) {
	if endpoint := os.Getenv("SESAMEFS_E110E_ISOLATED_URL"); endpoint != "" && os.Getenv("SESAMEFS_E110E_CHILD") != "1" {
		e110eRunIsolated(t, endpoint)
		return
	}
	requireCassandra(t)
	if os.Getenv("SESAMEFS_TEST_IN_CONTAINER") != "1" {
		t.Fatal("E1-10e requires Docker")
	}
	for _, endpoint := range []string{superadminClient.baseURL, envOrDefault("SESAMEFS_URL_2", "http://sesamefs-node-2:8080"), envOrDefault("SESAMEFS_URL_3", "http://sesamefs-node-3:8080")} {
		if err := e19CheckGCDisabled(newTestClient(endpoint, superadminClient.token)); err != nil {
			t.Fatalf("E1-10e owned-worker isolation: %v", err)
		}
	}
	for _, leg := range e110eLegs {
		t.Run(leg, func(t *testing.T) {
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
			fileJSON := mustMarshalSyncObjectForTest(t, map[string]interface{}{"block_ids": []string{fx.sha1ID}, "size": len(fx.content), "type": 1, "version": 1})
			file := syncSHA1HexForTest(fileJSON)
			rootJSON := mustMarshalSyncObjectForTest(t, map[string]interface{}{"dirents": []apipkg.FSEntry{{ID: file, Mode: 33188, Mtime: time.Now().Unix(), Name: fx.filename, Size: int64(len(fx.content))}}, "type": 3, "version": 1})
			root := syncSHA1HexForTest(rootJSON)
			commit := syncSHA1HexForTest([]byte("e110e-" + uuid.NewString()))
			payload := mustMarshalSyncObjectForTest(t, map[string]interface{}{"commit_id": commit, "repo_id": fx.repoID, "root_id": root, "parent_id": fx.headBefore, "description": "E1-10e unpublished source", "ctime": time.Now().Unix(), "version": 1})
			e17OK(t, e17Request(fx, http.MethodPut, "/commit/"+commit, "commit_id", commit, payload, syncHandler.PutCommit))
			packed := packSyncFSObjectsForTest(t, syncPackedFSObject{fsID: file, jsonData: fileJSON}, syncPackedFSObject{fsID: root, jsonData: rootJSON})
			e17OK(t, e17Request(fx, http.MethodPost, "/recv-fs", "", "", packed, syncHandler.RecvFS))
			var storedRoot, parent string
			if err := database.Session().Query(`SELECT root_fs_id, parent_id FROM commits WHERE library_id = ? AND commit_id = ?`, fx.repoID, commit).Scan(&storedRoot, &parent); err != nil || storedRoot != root || parent != fx.headBefore {
				t.Fatalf("unpublished commit root=%s parent=%s err=%v", storedRoot, parent, err)
			}
			e17AssertMetadata(t, fx, file, root)
			e17AssertMapping(t, fx, true)
			e17AssertRefs(t, fx, sourceRef)
			fx.assertHeadUnchanged(t)
			w2AssertBytes(t, fx)
			if rows := w2Repairs(t, fx); len(rows) != 0 {
				t.Fatalf("unexpected repair %+v", rows)
			}
			original := fx.target
			if leg == "committed" || leg == "terminal" {
				// Owned temporary lapse only; never delete a historical permanent fs:.
				if err := database.RemoveBlockReference(fx.orgID, fx.blockID, sourceRef); err != nil {
					t.Fatal(err)
				}
				e17AssertRefs(t, fx)
				retired = e17CommitGC(t, fx)

				if retired.Target != original {
					t.Fatal("retired wrong P")
				}
				if leg == "terminal" {
					e17Recover(t, fx, retired)
				}
			}
			permanent := dbpkg.BlockReferrerForFSObject(fx.repoID, file)
			if leg == "published-sync-history" {
				e17OK(t, e17Request(fx, http.MethodPut, "/block/"+fx.sha1ID, "block_id", fx.sha1ID, fx.content, syncHandler.PutBlock))
				e17OK(t, e17Request(fx, http.MethodPut, "/commit/HEAD?head="+commit, "commit_id", "HEAD", nil, syncHandler.PutCommit))
				if borrowedFSReadHead(t, database, fx.orgID, fx.repoID) != commit {
					t.Fatal("Sync never won HEAD")
				}
				e17AssertRefs(t, fx, sourceRef, syncRef, permanent)
				deletion := &e110dDeleteObserver{repo: fx.repoID, itemPath: "/" + fx.filename, scope: traffic.LibraryStorageScope(fx.orgID, fx.repoID), bytes: int64(len(fx.content))}
				deleteDB := w2EvidenceSession(t, splitEnvOrDefault("CASSANDRA_HOSTS", "cassandra:9042")[0], deletion)
				deleteHandler := newBorrowedFSHeadHandler(t, deleteDB, class)
				deleted := e16FileRequest(fx, http.MethodDelete, "", nil, deleteHandler.DeleteFile)
				if deleted.Code != http.StatusOK {
					t.Fatalf("delete published source: %d %s", deleted.Code, deleted.Body.String())
				}
				waitForIntegrationCondition(t, "published source delete housekeeping", deletion.complete)
				fx.headBefore = borrowedFSReadHead(t, database, fx.orgID, fx.repoID)
				if fx.headBefore == commit {
					t.Fatal("delete did not advance HEAD")
				}
				snapshot, err := v2pkg.NewFSHelper(database).GetLibraryHeadSnapshot(fx.repoID)
				if err != nil {
					t.Fatal(err)
				}
				var entriesJSON string
				if err := database.Session().Query(`SELECT dir_entries FROM fs_objects WHERE library_id = ? AND fs_id = ?`, fx.repoID, snapshot.RootFSID).Scan(&entriesJSON); err != nil {
					t.Fatal(err)
				}
				var entries []v2pkg.FSEntry
				if err := json.Unmarshal([]byte(entriesJSON), &entries); err != nil || v2pkg.FindEntryInList(entries, fx.filename) != nil {
					t.Fatalf("published delete root: %s %v", entriesJSON, err)
				}
				for _, ref := range []string{sourceRef, syncRef} {
					if err := database.RemoveBlockReference(fx.orgID, fx.blockID, ref); err != nil {
						t.Fatal(err)
					}
				}
				e17AssertRefs(t, fx, permanent)
			}
			trace := e13Observer(fx.orgID, fx.blockID, "e110e-writer")
			writerDB := w2EvidenceSession(t, splitEnvOrDefault("CASSANDRA_HOSTS", "cassandra:9042")[0], trace)
			handler := v2pkg.NewTrashHandler(writerDB)
			historicalVisits, headVisits := 0, 0
			t.Cleanup(v2pkg.SetRevertDirentsPublicationBarriersForTest(fx.repoID, func(itemPath, fsID string) {
				historicalVisits++
				if itemPath != "/"+fx.filename || fsID != file {
					t.Fatalf("wrong actual historical entry %s %s", itemPath, fsID)
				}
				fx.assertHeadUnchanged(t)
			}, func(itemPath string) {
				headVisits++
				if itemPath != "/"+fx.filename {
					t.Fatal("wrong HEAD item")
				}
			}))
			body := url.Values{"commit_id": {commit}, "path": {"/" + fx.filename}}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/v2.1/repos/"+fx.repoID+"/trash/revert-dirents/", strings.NewReader(body.Encode()))
			c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			c.Params = gin.Params{{Key: "repo_id", Value: fx.repoID}}
			c.Set("org_id", fx.orgID)
			c.Set("user_id", fx.userID)
			handler.RevertDirents(c)
			var response struct {
				Success []json.RawMessage `json:"success"`
				Failed  []struct {
					Path  string `json:"path"`
					IsDir bool   `json:"is_dir"`
				} `json:"failed"`
			}
			if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &response) != nil {
				t.Fatalf("response %d %s", rec.Code, rec.Body.String())
			}
			actualHead := borrowedFSReadHead(t, database, fx.orgID, fx.repoID)
			t.Logf("E1-10e %s response=%s HEAD before=%s after=%s historical=%d HEAD attempts=%d exact P1=%+v D=%+v", leg, rec.Body.String(), fx.headBefore, actualHead, historicalVisits, headVisits, original, retired)
			if actualHead != fx.headBefore {
				w24AssertHeadReaches(t, fx, actualHead, file)
			}
			e17AssertMetadata(t, fx, file, root)
			e17AssertMapping(t, fx, true)
			if leg == "published-sync-history" {
				e17AssertRefs(t, fx, permanent)
				w2AssertBytes(t, fx)
				e12AssertNoDeleteLifecycle(t, fx)
				if fx.readTarget(t) != original {
					t.Fatal("published source changed P")
				}
			} else if leg == "live-unpublished" {
				e17AssertRefs(t, fx, sourceRef)
				w2AssertBytes(t, fx)
				e12AssertNoDeleteLifecycle(t, fx)
			} else {
				e17AssertRefs(t, fx)
				x1AssertCanonicalAbsent(t, gcpkg.NewCassandraStore(database), fx.orgUUID, fx.blockID)
				if leg == "committed" {
					e19AssertCommitted(t, fx, retired)
				}
			}
			if rows := w2Repairs(t, fx); len(rows) != 0 {
				t.Fatalf("revert installed repair %+v", rows)
			}
			trace.mu.Lock()
			writes, events, errs := trace.repairWrites, len(trace.settlementWrites), len(trace.queryErrors)
			trace.mu.Unlock()
			if writes != 0 || events != 0 || errs != 0 {
				t.Fatalf("publication writes/errors %d/%d/%d", writes, events, errs)
			}
			if leg == "published-sync-history" {
				if len(response.Success) != 1 || len(response.Failed) != 0 || historicalVisits != 1 || headVisits != 1 || actualHead == fx.headBefore {
					t.Fatalf("published source rejected: %s", rec.Body.String())
				}
			} else if len(response.Success) != 0 || len(response.Failed) != 1 || response.Failed[0].Path != "/"+fx.filename || response.Failed[0].IsDir || historicalVisits != 1 || headVisits != 0 || actualHead != fx.headBefore {
				t.Fatalf("unpublished source admitted: %s historical=%d HEAD=%d", rec.Body.String(), historicalVisits, headVisits)
			}
			if !t.Failed() {
				e110eEvidence[leg] = true
			}
		})
	}
}

func TestUnpublishedResurrectionSourceAdmissionRequiresEveryNamedLeg(t *testing.T) {
	required := []string{"live-unpublished", "committed", "terminal", "published-sync-history"}
	all := map[string]bool{}
	if len(e110eLegs) != len(required) || len(e110eMissing(nil)) != len(required) {
		t.Fatal("contract must not shrink")
	}
	for _, leg := range required {
		all[leg] = true
	}
	if len(e110eMissing(all)) != 0 {
		t.Fatal("complete evidence rejected")
	}
	for _, leg := range required {
		delete(all, leg)
		missing := e110eMissing(all)
		if len(missing) != 1 || missing[0] != leg {
			t.Fatal("missing leg not rejected")
		}
		all[leg] = true
	}
}

// Use the existing manual-GC proof keyspace; the shared dev daemon stays active.
// The same binary retains race instrumentation and the child's required gate.
func e110eRunIsolated(t *testing.T, endpoint string) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	childRun := "^TestUnpublishedResurrectionSourceAdmission"
	// Preserve subtest filters: a normal-only run must not secretly run all legs.
	if _, sub, ok := strings.Cut(flag.Lookup("test.run").Value.String(), "/"); ok {
		childRun += "/" + sub
	}
	cmd := exec.CommandContext(ctx, binary, "-test.run="+childRun, "-test.v", "-test.count=1", "-test.timeout=3m")
	for _, entry := range os.Environ() {
		name := strings.SplitN(entry, "=", 2)[0]
		if strings.HasPrefix(name, "SESAMEFS_REQUIRE_") || name == "SESAMEFS_URL" || name == "SESAMEFS_URL_2" || name == "SESAMEFS_URL_3" || name == "CASSANDRA_KEYSPACE" || name == "SESAMEFS_E110E_CHILD" || name == "SESAMEFS_E110D_CHILD" || name == "SESAMEFS_E110C_CHILD" || name == "SESAMEFS_E110B_CHILD" || name == "SESAMEFS_E110_CHILD" || name == "SESAMEFS_E19_CHILD" || name == "SESAMEFS_W2_PROCESS_CHILD" {
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	cmd.Env = append(cmd.Env, e110eEvidenceEnv+"=1", "SESAMEFS_E110E_CHILD=1", "CASSANDRA_KEYSPACE=sesamefs_e19", "SESAMEFS_URL="+endpoint, "SESAMEFS_URL_2="+endpoint, "SESAMEFS_URL_3="+endpoint)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("isolated E1-10e evidence failed: %v", err)
	}
	for _, name := range e110eMissing(nil) {
		e110eEvidence[name] = true
	}
}
