//go:build integration

package gc

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

func TestLibraryLifecycleFinalSectionRealCassandra(t *testing.T) {
	if os.Getenv("SESAMEFS_LIFECYCLE_REAL_STACK") != "1" {
		t.Skip("requires owned migrated Cassandra: SESAMEFS_LIFECYCLE_REAL_STACK=1")
	}
	if os.Getenv("GC_ENABLED") != "false" {
		t.Fatal("ordinary GC must be disabled")
	}
	keyspace := os.Getenv("CASSANDRA_KEYSPACE")
	if !strings.HasPrefix(keyspace, "sesamefs_lifecycle") {
		t.Fatal("requires an owned sesamefs_lifecycle* keyspace")
	}
	database, err := db.New(config.DatabaseConfig{Hosts: strings.Split(os.Getenv("CASSANDRA_HOSTS"), ","), Keyspace: keyspace, LocalDC: "datacenter1", Consistency: "LOCAL_QUORUM", SerialConsistency: "LOCAL_SERIAL", Username: os.Getenv("CASSANDRA_USERNAME"), Password: os.Getenv("CASSANDRA_PASSWORD")})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	s := database.Session()
	store := NewCassandraStore(database)
	for _, outcome := range []string{"acknowledged", "write_error", "unknown", "panic"} {
		t.Run(outcome, func(t *testing.T) {
			library, token := uuid.New(), uuid.New()
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			t.Cleanup(func() {
				if err := s.Query(`DELETE FROM gc_library_hard_delete_locks WHERE library_id=?`, library.String()).Exec(); err != nil {
					t.Error(err)
				}
			})
			acquired, err := AcquireLibraryHardDeleteLockLease(s, library, token)
			must(err)
			if !acquired {
				t.Fatal("acquire")
			}
			var preparingTTL int
			must(s.Query(`SELECT TTL(lease_token) FROM gc_library_hard_delete_locks WHERE library_id=?`, library.String()).Scan(&preparingTTL))
			if preparingTTL <= 0 {
				t.Fatal("preparatory control has no TTL")
			}
			mutation := func() error {
				// Even with an aged heartbeat, finalization is durable and cannot be stolen.
				must(s.Query(`UPDATE gc_library_hard_delete_locks USING TTL 0 SET heartbeat=? WHERE library_id=?`, time.Now().Add(-3*time.Hour), library.String()).Exec())
				acquired, err := AcquireLibraryHardDeleteLockLease(s, library, uuid.New())
				must(err)
				if acquired {
					t.Fatal("final section stolen")
				}
				must(ReleaseLibraryHardDeleteLockLease(s, library, token))
				owned, err := RenewLibraryHardDeleteLockLease(s, library, token)
				must(err)
				if !owned {
					t.Fatal("ordinary release removed final section")
				}
				row := map[string]interface{}{}
				must(s.Query(`SELECT finalizing,TTL(started_at) AS a,TTL(heartbeat) AS b,TTL(lease_token) AS c,TTL(finalizing) AS d FROM gc_library_hard_delete_locks WHERE library_id=?`, library.String()).MapScan(row))
				if row["finalizing"] != true {
					t.Fatal("not finalizing")
				}
				for _, col := range []string{"a", "b", "c", "d"} {
					// MapScan decodes a null CQL TTL as the integer zero.
					if row[col] != nil && row[col] != 0 {
						t.Errorf("final section cell %s expires: %v", col, row[col])
					}
				}
				switch outcome {
				case "write_error":
					return errors.New("injected acknowledged failure")
				case "unknown":
					return gocql.ErrTimeoutNoResponse
				case "panic":
					panic("injected owner interruption")
				}
				return nil
			}
			panicked := false
			run := func() {
				defer func() {
					if recover() != nil {
						panicked = true
					}
				}()
				err = store.WithLibraryLifecycleMutation(library, token, mutation)
			}
			run()
			if outcome == "acknowledged" {
				must(err)
				acquired, err = AcquireLibraryHardDeleteLockLease(s, library, uuid.New())
				must(err)
				if !acquired {
					t.Fatal("completed final section was not released")
				}
				return
			}
			if outcome == "panic" && !panicked {
				t.Fatal("panic not injected")
			}
			if outcome != "panic" && err == nil {
				t.Fatal("mutation error was hidden")
			}
			if outcome == "unknown" && !errors.Is(err, gocql.ErrTimeoutNoResponse) {
				t.Fatalf("unknown cause lost: %v", err)
			}
			must(ReleaseLibraryHardDeleteLockLease(s, library, token))
			acquired, err = AcquireLibraryHardDeleteLockLease(s, library, uuid.New())
			must(err)
			if acquired {
				t.Fatal("failed/unknown final section was released")
			}
			called := false
			err = store.WithLibraryLifecycleMutation(library, token, func() error { called = true; return nil })
			if called || !errors.Is(err, ErrLibraryLifecycleOwnershipLost) {
				t.Fatalf("unknown final section replayed: called=%v err=%v", called, err)
			}
		})
	}
	t.Run("stale_token_never_executes", func(t *testing.T) {
		library, oldToken, newToken := uuid.New(), uuid.New(), uuid.New()
		t.Cleanup(func() {
			if err := s.Query(`DELETE FROM gc_library_hard_delete_locks WHERE library_id=?`, library.String()).Exec(); err != nil {
				t.Error(err)
			}
		})
		if ok, err := AcquireLibraryHardDeleteLockLease(s, library, oldToken); err != nil || !ok {
			t.Fatalf("acquire: %v", err)
		}
		if err := s.Query(`UPDATE gc_library_hard_delete_locks SET heartbeat=? WHERE library_id=?`, time.Now().Add(-3*time.Hour), library.String()).Exec(); err != nil {
			t.Fatal(err)
		}
		if ok, err := AcquireLibraryHardDeleteLockLease(s, library, newToken); err != nil || !ok {
			t.Fatalf("takeover: %v", err)
		}
		called := false
		err := store.WithLibraryLifecycleMutation(library, oldToken, func() error { called = true; return nil })
		if called || !errors.Is(err, ErrLibraryLifecycleOwnershipLost) {
			t.Fatalf("stale callback executed=%v err=%v", called, err)
		}
	})
	t.Run("worker_final_batch", func(t *testing.T) {
		org, library, owner := uuid.New(), uuid.New(), uuid.New()
		deleted := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Millisecond)
		must := func(err error) {
			t.Helper()
			if err != nil {
				t.Fatal(err)
			}
		}
		must(s.Query(`INSERT INTO libraries (org_id,library_id,owner_id,name,storage_class,block_representation_id,created_at,deleted_at) VALUES (?,?,?,?,?,?,?,?)`, org.String(), library.String(), owner.String(), "lifecycle-worker", "hot", db.PlainBlockRepresentationID, deleted, deleted).Exec())
		must(s.Query(`INSERT INTO libraries_by_id (library_id,org_id) VALUES (?,?)`, library.String(), org.String()).Exec())
		must(s.Query(`INSERT INTO deleted_libraries (library_id,org_id,deleted_at,storage_class,block_representation_id) VALUES (?,?,?,?,?)`, library.String(), org.String(), deleted, "hot", db.PlainBlockRepresentationID).Exec())
		t.Cleanup(func() {
			for _, query := range []struct {
				cql  string
				args []interface{}
			}{
				{`DELETE FROM libraries WHERE org_id=? AND library_id=?`, []interface{}{org.String(), library.String()}},
				{`DELETE FROM libraries_by_id WHERE library_id=?`, []interface{}{library.String()}},
				{`DELETE FROM deleted_libraries WHERE library_id=?`, []interface{}{library.String()}},
				{`DELETE FROM gc_library_hard_delete_locks WHERE library_id=?`, []interface{}{library.String()}},
			} {
				if err := s.Query(query.cql, query.args...).Exec(); err != nil {
					t.Error(err)
				}
			}
		})
		queue := NewQueue(store)
		must(queue.EnqueueBatch([]QueueItem{{OrgID: org, LibraryID: library, ItemType: ItemLibraryCascade, ItemID: library.String(), QueuedAt: deleted, IdentityAt: deleted, StorageClass: "hot", BlockRepresentationID: db.PlainBlockRepresentationID}}))
		worker := NewWorker(store, nil, queue, 10, 0, false, &Stats{})
		n, err := worker.ProcessOrgOnce(context.Background(), org)
		if err != nil || n != 1 {
			t.Fatalf("worker n=%d err=%v", n, err)
		}
		exists, err := store.CanonicalLibraryExists(org, library)
		must(err)
		if exists {
			t.Fatal("worker did not execute final batch")
		}
		var lock string
		if err := s.Query(`SELECT library_id FROM gc_library_hard_delete_locks WHERE library_id=?`, library.String()).Scan(&lock); !errors.Is(err, gocql.ErrNotFound) {
			t.Fatalf("worker final section remains: %v", err)
		}
	})
}
