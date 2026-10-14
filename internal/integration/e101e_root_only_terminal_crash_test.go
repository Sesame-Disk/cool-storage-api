//go:build integration

package integration

import (
	"context"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

const e101eEvidenceEnv = "SESAMEFS_REQUIRE_E101_ROOT_ONLY_CRASH_EVIDENCE"
const e101eRootCut = "terminal-root-only"

var e101eObserved = map[string]bool{}
var e101eCuts = []string{e101eRootCut}

func e101eMissing() []string   { return e101MissingLegs(e101eObserved, e101eCuts) }
func e101eCut(cut string) bool { return cut == e101eRootCut }

// Observe only the successful exact discovery DELETE in the real aggregate
// cleanup. Blocking here holds its caller before the next root DELETE.
// The parent independently proves the persisted state before killing/resuming.
type e101eDiscoveryObserver struct {
	t    *testing.T
	data e101ProcessData
	once sync.Once
}

func (o *e101eDiscoveryObserver) ObserveQuery(_ context.Context, q gocql.ObservedQuery) {
	if q.Err != nil || !strings.HasPrefix(strings.Join(strings.Fields(q.Statement), " "), "DELETE FROM gc_s3_orphans_by_day WHERE ") {
		return
	}
	d := o.data
	a := d.Authority
	want := []interface{}{dbpkg.GCProjectionUTCDate(d.FirstSeenAt), dbpkg.GCDiscoveryBucket(d.Org.String(), d.Block), d.FirstSeenAt.UTC(), d.Org.String(), d.Block, a.Target.StorageClass, a.Target.StorageKey, a.ClaimID, a.ClaimedAt}
	if reflect.DeepEqual(q.Values, want) {
		o.once.Do(func() { e101Pause(o.t, d, a) })
	}
}

func e101eChild(t *testing.T, data e101ProcessData) {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	observer := &e101eDiscoveryObserver{t: t, data: data}
	database, err := dbpkg.NewWithObservers(cfg.Database, observer, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	store := gcpkg.NewCassandraStore(database)
	scoped := &w2OwnedRecoveryStore{GCStore: store, org: data.Org, block: data.Block}
	if n, err := w2Worker(t, scoped, data.Class).RecoverS3Orphans(t.Context(), 1000); err != nil || n != 1 {
		t.Fatalf("root-only cut recovery n=%d err=%v", n, err)
	}
	t.Log("E101C_PHYSICAL_COMPLETED") // Shared no-kill protocol requires productive completion.
}

func TestE101RootOnlyTerminalProcessCrash(t *testing.T) {
	e101eObserved = map[string]bool{}
	// Register before availability checks: every repetition must prove both legs,
	// including one that exits early via SKIP. Subtest evidence waits for teardown.
	t.Cleanup(func() {
		if os.Getenv(e101eEvidenceEnv) == "1" {
			if missing := e101eMissing(); len(missing) != 0 {
				t.Errorf("required root-only legs missing: %s", strings.Join(missing, ","))
			}
		}
	})
	if endpoint := os.Getenv("SESAMEFS_E19_ISOLATED_URL"); endpoint != "" && os.Getenv("SESAMEFS_E101E_CHILD") != "1" {
		e101IsolatedMatrix(t, endpoint, "TestE101RootOnlyTerminalProcessCrash", e101eEvidenceEnv, "SESAMEFS_E101E_CHILD", e101eObserved, e101eCuts)
		return
	}
	e19RequireNoBackgroundGC(t)
	e101RunMatrix(t, e101eCuts, e101eObserved, e101eBoundary, nil)
}

func TestE101RootOnlyFailureSnapshotSurvivesTeardown(t *testing.T) {
	e101FailureControl(t, "TestE101RootOnlyTerminalProcessCrash", e101eRootCut, e101eEvidenceEnv, 0, 1)
}

func e101eBoundary(t *testing.T, fx *w2CreateFileFixture, data e101ProcessData) {
	t.Helper()
	a := data.Authority
	if a.IsZero() || a.Target != fx.target || data.FirstSeenAt.IsZero() {
		t.Fatalf("wrong root-only boundary identity: %+v", data)
	}
	store := gcpkg.NewCassandraStore(fx.database)
	if observation := store.ObserveBlockDeleteLifecycle(data.Org, data.Block, gcpkg.CommittedBlockDeleteAuthorityForTest(a)); observation.Outcome != gcpkg.StartBlockDeleteOrphanLifecycleAdvanced {
		t.Fatalf("root-only lifecycle is not TERMINAL: %+v", observation)
	}
	snapshot := e101Capture(fx.database, data)
	if len(snapshot.Errors) != 0 || !snapshot.RootFound || snapshot.Root.Authority != a || !snapshot.Root.FirstSeenAt.Equal(data.FirstSeenAt) || snapshot.K1Exists || len(snapshot.Rows["blocks"]) != 0 || len(snapshot.Rows["gc_s3_orphans"]) != 0 || len(snapshot.Rows["gc_s3_orphans_by_day"]) != 0 {
		t.Fatalf("cut is not exact root-only TERMINAL: %+v", snapshot)
	}
	e101bBookkeeping(t, fx.database, data, false)
	e17AssertRefs(t, fx)
	fx.assertHeadUnchanged(t)
	if rows := w2Repairs(t, fx); len(rows) != 0 {
		t.Fatalf("root-only cut invented repairs: %+v", rows)
	}
	t.Logf("durable root-only cut P=%+v D=%+v first_seen=%s: TERMINAL, orphan/discovery absent, root retained, canonical/K1 absent", a.Target, a, data.FirstSeenAt)
}
