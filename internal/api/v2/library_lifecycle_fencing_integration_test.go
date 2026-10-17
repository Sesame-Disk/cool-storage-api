//go:build integration

package v2

import (
	"context"
	"errors"
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
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// The observer pauses after Cassandra acknowledges the final ordinary renewal,
// before the caller can send its lifecycle batch. It does not replace any write.
type lifecycleRenewPause struct {
	once            sync.Once
	reached, resume chan struct{}
}

func (p *lifecycleRenewPause) ObserveQuery(_ context.Context, q gocql.ObservedQuery) {
	if q.Err == nil && strings.Contains(q.Statement, "SET heartbeat = ?, lease_token = ?") && strings.Contains(q.Statement, "gc_library_hard_delete_locks") {
		p.once.Do(func() { close(p.reached); <-p.resume })
	}
}

func TestLibraryLifecycleStaleOwnersRealCassandra(t *testing.T) {
	if os.Getenv("SESAMEFS_LIFECYCLE_REAL_STACK") != "1" {
		t.Skip("requires isolated migrated Cassandra: SESAMEFS_LIFECYCLE_REAL_STACK=1")
	}
	if os.Getenv("GC_ENABLED") != "false" {
		t.Fatal("GC_ENABLED must be false")
	}
	// Wait for existing async side effects before closing the observed session.
	oldAsync := runAsyncLibraryDeleteSideEffectFn
	runAsyncLibraryDeleteSideEffectFn = func(fn func()) { fn() }
	defer func() { runAsyncLibraryDeleteSideEffectFn = oldAsync }()
	cfg := config.DatabaseConfig{Hosts: restoreGuardHosts(restoreGuardEnv("CASSANDRA_HOSTS", "cassandra:9042")), Keyspace: os.Getenv("CASSANDRA_KEYSPACE"), LocalDC: "datacenter1", Consistency: "LOCAL_QUORUM", SerialConsistency: "LOCAL_SERIAL", Username: os.Getenv("CASSANDRA_USERNAME"), Password: os.Getenv("CASSANDRA_PASSWORD")}
	if !strings.HasPrefix(cfg.Keyspace, "sesamefs_lifecycle") {
		t.Fatal("requires an owned sesamefs_lifecycle* keyspace")
	}
	control, err := dbpkg.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	for _, oldDelete := range []bool{true, false} {
		name := "stale_delete_after_restore"
		if !oldDelete {
			name = "stale_restore_after_delete"
		}
		t.Run(name, func(t *testing.T) {
			org, lib, owner := uuid.NewString(), uuid.NewString(), uuid.NewString()
			now := time.Now().UTC().Truncate(time.Millisecond)
			deleted := now.Add(-time.Hour)
			s := control.Session()
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			must(s.Query(`INSERT INTO users (org_id,user_id,role,name) VALUES (?,?,?,?)`, org, owner, "user", "lifecycle-fixture").Exec())
			must(s.Query(`INSERT INTO libraries (org_id,library_id,owner_id,name,storage_class,head_commit_id,created_at,updated_at,deleted_at) VALUES (?,?,?,?,?,?,?,?,?)`, org, lib, owner, "lifecycle-fixture", "hot", "retained-head", now, now, deleted).Exec())
			must(s.Query(`INSERT INTO libraries_by_id (library_id,org_id,owner_id,head_commit_id) VALUES (?,?,?,?)`, lib, org, owner, "retained-head").Exec())
			must(s.Query(`INSERT INTO deleted_libraries (library_id,org_id,deleted_at,storage_class) VALUES (?,?,?,?)`, lib, org, deleted, "hot").Exec())
			row, err := dbpkg.ReadAdminLibraryProjectionRow(s, org, lib)
			must(err)
			seed := s.Batch(gocql.LoggedBatch)
			dbpkg.AddUpsertAdminLibraryReadModelQuery(seed, row)
			must(seed.Exec())
			// The whole keyspace belongs to this run. Remove exact fixture rows; global
			// discovery/reconciliation rows are removed with the isolated keyspace.
			t.Cleanup(func() {
				batch := s.Batch(gocql.LoggedBatch)
				dbpkg.AddDeleteAdminLibraryReadModelQuery(batch, row)
				batch.Query(`DELETE FROM libraries WHERE org_id=? AND library_id=?`, org, lib)
				batch.Query(`DELETE FROM libraries_by_id WHERE library_id=?`, lib)
				batch.Query(`DELETE FROM deleted_libraries WHERE library_id=?`, lib)
				batch.Query(`DELETE FROM users WHERE org_id=? AND user_id=?`, org, owner)
				batch.Query(`DELETE FROM gc_library_hard_delete_locks WHERE library_id=?`, lib)
				if err := batch.Exec(); err != nil {
					t.Error(err)
				}
			})
			p := &lifecycleRenewPause{reached: make(chan struct{}), resume: make(chan struct{})}
			var resume sync.Once
			unblock := func() { resume.Do(func() { close(p.resume) }) }
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
			go func() { done <- call(pausedDB, oldDelete) }()
			select {
			case <-p.reached:
			case <-time.After(30 * time.Second):
				unblock()
				response := <-done
				t.Fatalf("old owner never reached acknowledged renewal: %d %s", response.Code, response.Body.String())
			}
			// Age the heartbeat of exactly the owned fixture, not a shared lease. This
			// deterministically models suspension beyond the production stale interval.
			must(s.Query(`UPDATE gc_library_hard_delete_locks SET heartbeat=? WHERE library_id=?`, now.Add(-2*time.Hour), lib).Exec())
			winner := call(control, !oldDelete)
			if winner.Code != http.StatusOK {
				unblock()
				<-done
				t.Fatalf("takeover response %d: %s", winner.Code, winner.Body.String())
			}
			unblock()
			var loser *httptest.ResponseRecorder
			select {
			case loser = <-done:
			case <-time.After(30 * time.Second):
				t.Fatal("old owner did not finish")
			}
			wantStatus := http.StatusInternalServerError
			if oldDelete {
				wantStatus = http.StatusConflict
			}
			if loser.Code != wantStatus {
				t.Errorf("stale owner status=%d want=%d body=%s", loser.Code, wantStatus, loser.Body.String())
			}
			var head string
			var deletedAt time.Time
			err = s.Query(`SELECT head_commit_id,deleted_at FROM libraries WHERE org_id=? AND library_id=?`, org, lib).Scan(&head, &deletedAt)
			if oldDelete {
				if err != nil || head != "retained-head" || !deletedAt.IsZero() {
					t.Errorf("restored canonical row lost: head=%q deleted=%v err=%v", head, deletedAt, err)
				}
				var projectionDeleted time.Time
				if err := s.Query(`SELECT deleted_at FROM libraries_by_org_updated WHERE org_id=? AND library_id=?`, org, lib).Scan(&projectionDeleted); err != nil || !projectionDeleted.IsZero() {
					t.Errorf("restored projection lost: deleted=%v err=%v", projectionDeleted, err)
				}
			} else {
				if !errors.Is(err, gocql.ErrNotFound) {
					t.Errorf("permanent delete was resurrected: head=%q deleted=%v err=%v", head, deletedAt, err)
				}
				var id string
				if err := s.Query(`SELECT library_id FROM libraries_by_org_updated WHERE org_id=? AND library_id=?`, org, lib).Scan(&id); !errors.Is(err, gocql.ErrNotFound) {
					t.Errorf("stale restore recreated projection: %v", err)
				}
			}
			var lockID string
			if err := s.Query(`SELECT library_id FROM gc_library_hard_delete_locks WHERE library_id=?`, lib).Scan(&lockID); !errors.Is(err, gocql.ErrNotFound) {
				t.Errorf("lease leaked after acknowledged operations: %v", err)
			}
		})
	}
}
