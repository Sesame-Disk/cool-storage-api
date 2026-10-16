//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	v2pkg "github.com/Sesame-Disk/sesamefs/internal/api/v2"
	"github.com/Sesame-Disk/sesamefs/internal/config"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
)

// crossRepoMoveDestinationHeadBarrier holds the in-process handler right after
// its destination HEAD CAS returns, before source removal starts.
type crossRepoMoveDestinationHeadBarrier struct {
	dst              string
	once             sync.Once
	reached, release chan struct{}
}

func (b *crossRepoMoveDestinationHeadBarrier) ObserveQuery(_ context.Context, q gocql.ObservedQuery) {
	if q.Err != nil || !w2IsHeadCAS(q.Statement) || len(q.Values) < 6 || fmt.Sprint(q.Values[5]) != b.dst {
		return
	}
	b.once.Do(func() {
		close(b.reached)
		select {
		case <-b.release:
		case <-time.After(60 * time.Second):
		}
	})
}

// TestCrossRepoMoveSourceRemovalChecksCopiedIdentity is the regression for
// ISSUE-CROSSREPO-MOVE-SOURCE-IDENTITY-RACE-01. A cross-repo move copies FS-A
// to the destination and then removes the source by name from the latest
// source HEAD. If a writer replaces /a.txt with FS-B in between, the move must
// not remove FS-B: the destination only received FS-A. Like a source that is
// already gone, a replaced source is left in place and the move still succeeds.
func TestCrossRepoMoveSourceRemovalChecksCopiedIdentity(t *testing.T) {
	requireCassandra(t)
	database := shareProjectionDBForTest(t)
	helper := v2pkg.NewFSHelper(database)

	src := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-xmove-identity-src-%d", time.Now().UnixNano()))
	dst := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-xmove-identity-dst-%d", time.Now().UnixNano()))
	var orgID, userID string
	if err := database.Session().Query(`SELECT org_id, owner_id FROM libraries_by_id WHERE library_id = ?`, src).Scan(&orgID, &userID); err != nil {
		t.Fatalf("resolve org/owner for %s: %v", src, err)
	}

	fileName := "xmove-identity.txt"
	uploadResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", src))
	expectStatus(t, uploadResp, http.StatusOK)
	uploadURL := strings.Trim(responseBody(t, uploadResp), "\" \n\r")
	uploadFileThroughLink(t, adminClient, uploadURL, fileName, "/", uniqueText("fs-a"))
	before, err := helper.TraverseToPath(src, "/"+fileName)
	if err != nil || before.TargetEntry == nil {
		t.Fatalf("source FS-A: %v", err)
	}
	fsA := before.TargetEntry.ID

	barrier := &crossRepoMoveDestinationHeadBarrier{dst: dst, reached: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	resume := func() { releaseOnce.Do(func() { close(barrier.release) }) }
	defer resume()
	writerDB := w2EvidenceSession(t, splitEnvOrDefault("CASSANDRA_HOSTS", "cassandra:9042")[0], barrier)
	h := v2pkg.NewBatchOperationHandler(writerDB, &config.Config{})

	body, _ := json.Marshal(map[string]interface{}{
		"src_repo_id": src, "dst_repo_id": dst,
		"src_parent_dir": "/", "dst_parent_dir": "/",
		"src_dirents": []string{fileName}, "conflict_policy": "autorename",
	})
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/sync-batch-move-item/", bytes.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Set("org_id", orgID)
		c.Set("user_id", userID)
		h.SyncBatchMove(c)
	}()

	select {
	case <-barrier.reached:
	case <-done:
		t.Fatalf("move finished before its destination HEAD CAS was observed: %d %s", rec.Code, rec.Body.String())
	case <-time.After(30 * time.Second):
		t.Fatal("destination HEAD CAS not reached")
	}

	// Concurrent writer: replace the source file through the productive update link.
	updateResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/update-link/?p=/", src))
	expectStatus(t, updateResp, http.StatusOK)
	updateURL := strings.Trim(responseBody(t, updateResp), "\" \n\r")
	uploadFileThroughLink(t, adminClient, updateURL, fileName, "/", uniqueText("fs-b"))
	replaced, err := helper.TraverseToPath(src, "/"+fileName)
	if err != nil || replaced.TargetEntry == nil || replaced.TargetEntry.ID == fsA {
		t.Fatalf("source was not replaced with a distinct FS-B: %+v %v", replaced, err)
	}
	fsB := replaced.TargetEntry.ID

	resume()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("move did not finish after release")
	}

	copied, err := helper.TraverseToPath(dst, "/"+fileName)
	if err != nil || copied.TargetEntry == nil || copied.TargetEntry.ID != fsA {
		t.Fatalf("destination must hold the copied FS-A %s: %+v %v (move %d %s)", fsA, copied, err, rec.Code, rec.Body.String())
	}
	after, err := helper.TraverseToPath(src, "/"+fileName)
	t.Logf("cross-repo move response=%d %s; FS-A=%s FS-B=%s source after=%+v err=%v", rec.Code, rec.Body.String(), fsA, fsB, after, err)
	if err != nil || after.TargetEntry == nil || after.TargetEntry.ID != fsB {
		t.Fatalf("RED: cross-repo move removed replacement FS-B %s from the source; only FS-A %s reached the destination", fsB, fsA)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("move with a replaced source must keep its success contract: %d %s", rec.Code, rec.Body.String())
	}
}
