//go:build integration

package v2

// Pause after final-section release, then delete the restored library. The
// restore must have finished aggregate adjustment before it releases exclusion.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type restoreCounterPause struct {
	once            sync.Once
	reached, resume chan struct{}
	mode            string
}

func (p *restoreCounterPause) hit() { p.once.Do(func() { close(p.reached); <-p.resume }) }

// Pause either after exclusion is released or while the counter helper is
// reading its library source. Both boundaries use acknowledged real queries.
func (p *restoreCounterPause) ObserveQuery(_ context.Context, q gocql.ObservedQuery) {
	if q.Err != nil {
		return
	}
	released := strings.Contains(q.Statement, "DELETE FROM gc_library_hard_delete_locks") && strings.Contains(q.Statement, "finalizing=true")
	adjusting := strings.Contains(q.Statement, "SELECT bytes_used, file_count FROM storage_counters")
	if (p.mode == "released" && released) || (p.mode == "adjusting" && adjusting) {
		p.hit()
	}
}

func TestLibraryLifecycleRestoreCountersRealCassandra(t *testing.T) {
	if os.Getenv("SESAMEFS_LIFECYCLE_REAL_STACK") != "1" {
		t.Skip("requires owned Cassandra: SESAMEFS_LIFECYCLE_REAL_STACK=1")
	}
	for _, mode := range []string{"released", "adjusting"} {
		t.Run(mode, func(t *testing.T) { testRestoreCounterWindow(t, mode) })
	}
}
func testRestoreCounterWindow(t *testing.T, mode string) {
	if os.Getenv("SESAMEFS_LIFECYCLE_REAL_STACK") != "1" {
		t.Skip("requires owned migrated Cassandra: SESAMEFS_LIFECYCLE_REAL_STACK=1")
	}
	if os.Getenv("GC_ENABLED") != "false" {
		t.Fatal("ordinary GC must be disabled")
	}
	oldAsync := runAsyncLibraryDeleteSideEffectFn
	runAsyncLibraryDeleteSideEffectFn = func(fn func()) { fn() }
	defer func() { runAsyncLibraryDeleteSideEffectFn = oldAsync }()
	cfg := config.DatabaseConfig{Hosts: restoreGuardHosts(restoreGuardEnv("CASSANDRA_HOSTS", "cassandra:9042")), Keyspace: os.Getenv("CASSANDRA_KEYSPACE"), LocalDC: "datacenter1", Consistency: "LOCAL_QUORUM", SerialConsistency: "LOCAL_SERIAL"}
	if !strings.HasPrefix(cfg.Keyspace, "sesamefs_lifecycle") {
		t.Fatal("owned keyspace required")
	}
	control, err := dbpkg.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	s := control.Session()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	org, lib, owner := uuid.NewString(), uuid.NewString(), uuid.NewString()
	now := time.Now().UTC().Truncate(time.Millisecond)
	deleted := now.Add(-time.Hour)
	must(s.Query(`INSERT INTO users (org_id,user_id,role,name) VALUES (?,?,?,?)`, org, owner, "user", "audit-fixture").Exec())
	must(s.Query(`INSERT INTO libraries (org_id,library_id,owner_id,name,storage_class,head_commit_id,created_at,updated_at,deleted_at) VALUES (?,?,?,?,?,?,?,?,?)`, org, lib, owner, "audit-fixture", "hot", "retained-head", now, now, deleted).Exec())
	must(s.Query(`INSERT INTO libraries_by_id (library_id,org_id,owner_id,head_commit_id) VALUES (?,?,?,?)`, lib, org, owner, "retained-head").Exec())
	must(s.Query(`INSERT INTO deleted_libraries (library_id,org_id,deleted_at,storage_class) VALUES (?,?,?,?)`, lib, org, deleted, "hot").Exec())
	row, err := dbpkg.ReadAdminLibraryProjectionRow(s, org, lib)
	must(err)
	seed := s.Batch(gocql.LoggedBatch)
	dbpkg.AddUpsertAdminLibraryReadModelQuery(seed, row)
	must(seed.Exec())
	defer func() {
		cleanup := s.Batch(gocql.LoggedBatch)
		dbpkg.AddDeleteAdminLibraryReadModelQuery(cleanup, row)
		cleanup.Query(`DELETE FROM libraries WHERE org_id=? AND library_id=?`, org, lib)
		cleanup.Query(`DELETE FROM libraries_by_id WHERE library_id=?`, lib)
		cleanup.Query(`DELETE FROM deleted_libraries WHERE library_id=?`, lib)
		cleanup.Query(`DELETE FROM libraries_deleted_by_org WHERE org_id=?`, org)
		cleanup.Query(`DELETE FROM gc_library_hard_delete_locks WHERE library_id=?`, lib)
		cleanup.Query(`DELETE FROM users WHERE org_id=? AND user_id=?`, org, owner)
		if err := cleanup.Exec(); err != nil {
			t.Error(err)
		}
		var found string
		if err := s.Query(`SELECT library_id FROM libraries WHERE org_id=? AND library_id=?`, org, lib).Scan(&found); err != gocql.ErrNotFound {
			t.Errorf("owned library cleanup: %v", err)
		}
		if err := s.Query(`SELECT library_id FROM gc_library_hard_delete_locks WHERE library_id=?`, lib).Scan(&found); err != gocql.ErrNotFound {
			t.Errorf("owned lease cleanup: %v", err)
		}
	}()
	total := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
	add := func(scope string, bytes int64) {
		must(s.Query(`UPDATE storage_counters SET bytes_used = bytes_used + ?, file_count = file_count + ? WHERE scope = ? AND shard = 0 AND day = ?`, bytes, int64(1), scope, total).Exec())
	}
	add(traffic.LibraryStorageScope(org, lib), 100)  // trashed library's own usage
	add(traffic.OrganizationStorageScope(org), 1000) // other org usage; trashed lib excluded
	readOrg := func() int64 {
		var b int64
		must(s.Query(`SELECT bytes_used FROM storage_counters WHERE scope = ? AND shard = 0 AND day = ?`, traffic.OrganizationStorageScope(org), total).Scan(&b))
		return b
	}

	p := &restoreCounterPause{reached: make(chan struct{}), resume: make(chan struct{}), mode: mode}
	var ro sync.Once
	unblock := func() { ro.Do(func() { close(p.resume) }) }
	defer unblock()
	pausedDB, err := dbpkg.NewWithObservers(cfg, p, nil)
	must(err)
	defer pausedDB.Close()
	call := func(database *dbpkg.DB, remove bool) *httptest.ResponseRecorder {
		h := &DeletedLibraryHandler{db: database, permMiddleware: middleware.NewPermissionMiddleware(database)}
		method := http.MethodPut
		if remove {
			method = http.MethodDelete
		}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(method, "/api/v2.1/repos/deleted/"+lib+"/", nil)
		c.Set("org_id", org)
		c.Set("user_id", owner)
		c.Params = gin.Params{{Key: "repo_id", Value: lib}}
		if remove {
			h.PermanentDeleteRepo(c)
		} else {
			h.RestoreDeletedRepo(c)
		}
		return w
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- call(pausedDB, false) }()
	select {
	case <-p.reached:
	case <-time.After(30 * time.Second):
		unblock()
		r := <-done
		t.Fatalf("restore never reached pause: %d %s", r.Code, r.Body.String())
	}
	t.Logf("paused restore after final write; org aggregate=%d", readOrg())
	softErr := softDeleteLibrary(control, org, owner, owner, lib)
	if softErr != nil {
		unblock()
		<-done
		t.Fatal(softErr)
	}
	t.Logf("concurrent soft delete; org aggregate=%d", readOrg())
	del := call(control, true)
	wantDelete := http.StatusOK
	if mode == "adjusting" {
		wantDelete = http.StatusConflict
	}
	if del.Code != wantDelete {
		t.Errorf("concurrent permanent delete: status=%d want=%d body=%s", del.Code, wantDelete, del.Body.String())
	}
	unblock()
	var restore *httptest.ResponseRecorder
	select {
	case restore = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("restore did not finish")
	}
	if restore.Code != http.StatusOK {
		t.Errorf("restore status=%d body=%s", restore.Code, restore.Body.String())
	}
	got := readOrg()
	t.Logf("restore status=%d; FINAL org aggregate=%d (correct=1000)", restore.Code, got)
	if got != 1000 {
		t.Errorf("org storage aggregate drift: got %d want 1000", got)
	}
}
