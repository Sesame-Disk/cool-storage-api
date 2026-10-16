//go:build integration

package integration

import (
	"context"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

const e101iEvidenceEnv = "SESAMEFS_REQUIRE_E101_PREPARED_DISCOVERY_CRASH_EVIDENCE"
const e101iPreparedCut = "prepared-before-discovery"
const e101iWorkerCompleted = "E101I_WORKER_COMPLETED"

var e101iObserved = map[string]bool{}
var e101iCuts = []string{e101iPreparedCut}

func e101iMissing() []string   { return e101MissingLegs(e101iObserved, e101iCuts) }
func e101iCut(cut string) bool { return cut == e101iPreparedCut }

// Hold the successful owned PREPARED INSERT before Prepare can publish discovery.
// Parent reads certify application; a successful observer callback alone cannot.
type e101iPreparedObserver struct {
	t    *testing.T
	data e101ProcessData
	once sync.Once
}

func (o *e101iPreparedObserver) ObserveQuery(_ context.Context, q gocql.ObservedQuery) {
	if q.Err != nil || !strings.HasPrefix(strings.Join(strings.Fields(q.Statement), " "), "INSERT INTO gc_s3_orphans ") {
		return
	}
	d, a := o.data, o.data.Authority
	if a.IsZero() || d.FirstSeenAt.IsZero() {
		return
	}
	want := []interface{}{d.Org.String(), d.Block, a.Target.StorageClass, a.Target.StorageKey, a.ClaimID, a.ClaimedAt, d.SHA1, gcpkg.S3OrphanPhasePendingS3, gcpkg.S3OrphanRecoveryStatePrepared, d.FirstSeenAt, d.FirstSeenAt, 0, ""}
	if reflect.DeepEqual(q.Values, want) {
		o.once.Do(func() { e101Pause(o.t, d, a) })
	}
}

type e101iPreparationStore struct {
	*w2ClosureOwnedQueue
	observer *e101iPreparedObserver
}

func (s *e101iPreparationStore) PrepareBlockDeleteOrphan(org uuid.UUID, block string, a gcpkg.BlockDeleteAuthority, sha1 string, now time.Time) gcpkg.StartBlockDeleteOrphanResult {
	o := s.observer
	a.ClaimedAt = a.ClaimedAt.UTC().Truncate(time.Millisecond)
	if org != o.data.Org || block != o.data.Block || a.IsZero() || a.Target != o.data.Candidate.Target || !s.visited || len(s.liveness) != 1 || s.liveness[0] != dbpkg.BlockPublicationZero || len(s.errs) != 1 || s.errs[0] != nil {
		o.t.Fatal("PREPARED/no-discovery caller lacks exact owned claim/P and productive zero-liveness")
	}
	o.data.Authority, o.data.FirstSeenAt, o.data.SHA1 = a, now.UTC().Truncate(time.Millisecond), strings.TrimSpace(sha1)
	return s.GCStore.PrepareBlockDeleteOrphan(org, block, a, sha1, now)
}
func e101iChild(t *testing.T, data e101ProcessData) {
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	observer := &e101iPreparedObserver{t: t, data: data}
	database, err := dbpkg.NewWithObservers(cfg.Database, observer, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	scope := &w2ClosureOwnedQueue{GCStore: gcpkg.NewCassandraStore(database), org: data.Org, block: data.Block, identity: data.Candidate.Identity()}
	if n, err := w2Worker(t, &e101iPreparationStore{w2ClosureOwnedQueue: scope, observer: observer}, data.Class).ProcessOrgOnce(t.Context(), data.Org); err != nil || n != 1 || !scope.visited {
		t.Fatalf("PREPARED/no-discovery productive worker n=%d visited=%v err=%v", n, scope.visited, err)
	}
	t.Log(e101iWorkerCompleted)
}

func TestE101PreparedBeforeDiscoveryProcessCrash(t *testing.T) {
	e101iObserved = map[string]bool{}
	t.Cleanup(func() {
		if os.Getenv(e101iEvidenceEnv) == "1" {
			if missing := e101iMissing(); len(missing) != 0 {
				t.Errorf("required prepared-before-discovery legs missing: %s", strings.Join(missing, ","))
			}
		}
	})
	if endpoint := os.Getenv("SESAMEFS_E19_ISOLATED_URL"); endpoint != "" && os.Getenv("SESAMEFS_E101I_CHILD") != "1" {
		e101IsolatedMatrix(t, endpoint, "TestE101PreparedBeforeDiscoveryProcessCrash", e101iEvidenceEnv, "SESAMEFS_E101I_CHILD", e101iObserved, e101iCuts)
		return
	}
	e19RequireNoBackgroundGC(t)
	e101RunMatrix(t, e101iCuts, e101iObserved, e101iBoundary, e101iBeforeRecovery)
}
func TestE101PreparedBeforeDiscoveryFailureSnapshotSurvivesTeardown(t *testing.T) {
	e101FailureControl(t, "TestE101PreparedBeforeDiscoveryProcessCrash", e101iPreparedCut, e101iEvidenceEnv, 1, 0)
}
func e101iBoundary(t *testing.T, fx *w2CreateFileFixture, data e101ProcessData) {
	e101AssertBoundary(t, fx, data)
	e101hUncommitted(t, fx.database, data)
	snap := e101Capture(fx.database, data)
	if len(snap.Errors) != 0 || !snap.RootFound || !snap.Root.FirstSeenAt.Equal(data.FirstSeenAt) || !snap.K1Exists || len(snap.K1Bytes) == 0 || len(snap.Rows["blocks"]) != 1 || len(snap.Rows["gc_s3_orphans"]) != 1 || len(snap.Rows["gc_s3_orphans_by_day"]) != 0 || len(snap.Rows["gc_block_delete_lifecycles"]) != 0 {
		t.Fatalf("PREPARED/no-discovery durable boundary not proved: %+v", snap)
	}
	row, found, err := gcpkg.NewCassandraStore(fx.database).GetS3OrphanExact(data.Org, data.Block, data.Authority)
	if err != nil || !found || row.RecoveryPhase != gcpkg.S3OrphanPhasePendingS3 || !row.FirstSeenAt.Equal(data.FirstSeenAt) {
		t.Fatalf("wrong PREPARED token/phase: %+v found=%v err=%v", row, found, err)
	}
	e101bBookkeeping(t, fx.database, data, true)
	if rows := w2Repairs(t, fx); len(rows) != 0 {
		t.Fatalf("invented repairs: %+v", rows)
	}
	t.Log("E101I_BOUNDARY_VERIFIED: exact root/PREPARED, absent discovery/lifecycle, uncommitted claim, canonical/K1/HEAD intact")
}

// Observe the real root executor's projection repair before PREPARED abort can
// delete that projection again. Final absence alone cannot prove this route.
type e101iRecoveryStore struct {
	*w2OwnedRecoveryStore
	t        *testing.T
	database *dbpkg.DB
	data     e101ProcessData
	repaired bool
}

func (s *e101iRecoveryStore) PublishS3OrphanDiscovery(org uuid.UUID, block string, a gcpkg.BlockDeleteAuthority, first time.Time) error {
	if org != s.data.Org || block != s.data.Block || a != s.data.Authority || !first.Equal(s.data.FirstSeenAt) {
		s.t.Fatal("projection repair used foreign authority/token")
	}
	if err := s.GCStore.PublishS3OrphanDiscovery(org, block, a, first); err != nil {
		return err
	}
	snap := e101Capture(s.database, s.data)
	if len(snap.Errors) != 0 || !snap.RootFound || !snap.K1Exists || len(snap.Rows["gc_s3_orphans"]) != 1 || len(snap.Rows["gc_s3_orphans_by_day"]) != 1 {
		s.t.Fatalf("real projection repair not independently visible: %+v", snap)
	}
	e101hUncommitted(s.t, s.database, s.data)
	s.repaired = true
	s.t.Log("E101I_DISCOVERY_REPUBLISHED: exact root/PREPARED still present before abort")
	return nil
}
func e101iRecoveryChild(t *testing.T, database *dbpkg.DB, data e101ProcessData) {
	scope := &e101iRecoveryStore{w2OwnedRecoveryStore: &w2OwnedRecoveryStore{GCStore: gcpkg.NewCassandraStore(database), org: data.Org, block: data.Block}, t: t, database: database, data: data}
	if n, err := w2Worker(t, scope, data.Class).RecoverS3Orphans(t.Context(), 1000); err != nil || n != 1 || !scope.repaired {
		t.Fatalf("PREPARED root recovery n=%d repaired=%v err=%v", n, scope.repaired, err)
	}
	t.Log("E101I_ROOT_RECOVERY_COMPLETED")
}
func e101iBeforeRecovery(t *testing.T, fx *w2CreateFileFixture, data e101ProcessData, _ e101Snapshot) {
	if data.Expect != "aborted" {
		return
	}
	data.Mode = "repair-projection"
	cmd, cancel := e101Command(t, data)
	defer cancel()
	out, err := cmd.CombinedOutput()
	t.Logf("independent PREPARED/no-discovery root recovery: %s", out)
	if err != nil || !strings.Contains(string(out), "E101I_ROOT_RECOVERY_COMPLETED") || !strings.Contains(string(out), "E101I_DISCOVERY_REPUBLISHED") {
		t.Fatalf("projection repair/abort not proved: %v", err)
	}
	e101AssertRecovered(t, fx, data)
	e101bBookkeeping(t, fx.database, data, true) // No queue worker manufactured the recovery.
}

func e101iAssertSettled(t *testing.T, fx *w2CreateFileFixture, data e101ProcessData) {
	t.Helper()
	snap := e101Capture(fx.database, data)
	if len(snap.Errors) != 0 || snap.RootFound || len(snap.Rows["gc_s3_orphans"]) != 0 || len(snap.Rows["gc_s3_orphans_by_day"]) != 0 {
		t.Fatalf("PREPARED/no-discovery metadata not settled: %+v", snap)
	}
	e101bBookkeeping(t, fx.database, data, data.Expect == "aborted")
}
