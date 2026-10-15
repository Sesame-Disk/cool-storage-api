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

const e101gEvidenceEnv = "SESAMEFS_REQUIRE_E101_PUBLISHED_PREPARED_CRASH_EVIDENCE"
const e101gPromotionCut = "published-before-orphan-commit"
const e101gWorkerCompleted = "E101G_WORKER_COMPLETED"

var e101gObserved = map[string]bool{}
var e101gCuts = []string{e101gPromotionCut}

func e101gMissing() []string   { return e101MissingLegs(e101gObserved, e101gCuts) }
func e101gCut(cut string) bool { return cut == e101gPromotionCut }

// Hold the real promotion caller after its successful exact lifecycle INSERT,
// before it can issue the PREPARED-to-COMMITTED orphan CAS. Parent reads certify C3.
type e101gLifecycleObserver struct {
	t    *testing.T
	data e101ProcessData
	once sync.Once
}

func (o *e101gLifecycleObserver) ObserveQuery(_ context.Context, q gocql.ObservedQuery) {
	if q.Err != nil || !strings.HasPrefix(strings.Join(strings.Fields(q.Statement), " "), "INSERT INTO gc_block_delete_lifecycles ") {
		return
	}
	d, a := o.data, o.data.Authority
	if a.IsZero() || d.FirstSeenAt.IsZero() {
		return
	}
	want := []interface{}{d.Org.String(), d.Block, a.ClaimID, a.ClaimedAt, a.Target.StorageClass, a.Target.StorageKey, gcpkg.BlockDeleteLifecyclePhasePublished, d.FirstSeenAt}
	if reflect.DeepEqual(q.Values, want) {
		o.once.Do(func() { e101Pause(o.t, d, a) })
	}
}

type e101gPromotionStore struct {
	*w2ClosureOwnedQueue
	observer *e101gLifecycleObserver
}

func (s *e101gPromotionStore) PromoteBlockDeleteOrphan(org uuid.UUID, block string, committed gcpkg.CommittedBlockDeleteAuthority) gcpkg.StartBlockDeleteOrphanResult {
	o := s.observer
	a := committed.Authority()
	a.ClaimedAt = a.ClaimedAt.UTC().Truncate(time.Millisecond)
	if org != o.data.Org || block != o.data.Block || a.IsZero() || a.Target != o.data.Candidate.Target || !s.visited || len(s.liveness) != 1 || s.liveness[0] != dbpkg.BlockPublicationZero || len(s.errs) != 1 || s.errs[0] != nil {
		o.t.Fatal("C3 caller lacks exact owned D/P and productive zero-liveness")
	}
	row, found, err := s.GCStore.GetS3OrphanExact(org, block, a)
	if err != nil || !found || row.Authority != a || row.RecoveryState != gcpkg.S3OrphanRecoveryStatePrepared || row.FirstSeenAt.IsZero() {
		o.t.Fatalf("C3 caller lacks exact PREPARED token: %+v found=%v err=%v", row, found, err)
	}
	o.data.Authority, o.data.FirstSeenAt = a, row.FirstSeenAt.UTC().Truncate(time.Millisecond)
	return s.GCStore.PromoteBlockDeleteOrphan(org, block, committed)
}

func e101gChild(t *testing.T, data e101ProcessData) {
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	observer := &e101gLifecycleObserver{t: t, data: data}
	database, err := dbpkg.NewWithObservers(cfg.Database, observer, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	scope := &w2ClosureOwnedQueue{GCStore: gcpkg.NewCassandraStore(database), org: data.Org, block: data.Block, identity: data.Candidate.Identity()}
	store := &e101gPromotionStore{w2ClosureOwnedQueue: scope, observer: observer}
	if n, err := w2Worker(t, store, data.Class).ProcessOrgOnce(t.Context(), data.Org); err != nil || n != 1 || !scope.visited {
		t.Fatalf("C3 productive worker n=%d visited=%v err=%v", n, scope.visited, err)
	}
	t.Log(e101gWorkerCompleted)
}

func TestE101PublishedPreparedProcessCrash(t *testing.T) {
	e101gObserved = map[string]bool{}
	t.Cleanup(func() {
		if os.Getenv(e101gEvidenceEnv) == "1" {
			if missing := e101gMissing(); len(missing) != 0 {
				t.Errorf("required published-prepared legs missing: %s", strings.Join(missing, ","))
			}
		}
	})
	if endpoint := os.Getenv("SESAMEFS_E19_ISOLATED_URL"); endpoint != "" && os.Getenv("SESAMEFS_E101G_CHILD") != "1" {
		e101IsolatedMatrix(t, endpoint, "TestE101PublishedPreparedProcessCrash", e101gEvidenceEnv, "SESAMEFS_E101G_CHILD", e101gObserved, e101gCuts)
		return
	}
	e19RequireNoBackgroundGC(t)
	e101RunMatrix(t, e101gCuts, e101gObserved, e101gBoundary, e101gBeforeRecovery)
}

func TestE101PublishedPreparedFailureSnapshotSurvivesTeardown(t *testing.T) {
	e101FailureControl(t, "TestE101PublishedPreparedProcessCrash", e101gPromotionCut, e101gEvidenceEnv, 1, 1)
}

func e101gBoundary(t *testing.T, fx *w2CreateFileFixture, data e101ProcessData) {
	t.Helper()
	a := data.Authority
	if a.IsZero() || a.Target != fx.target || data.FirstSeenAt.IsZero() {
		t.Fatalf("wrong C3 identity: %+v", data)
	}
	var claim, state, class, key string
	var at time.Time
	var committed *bool
	err := fx.database.Session().Query(`SELECT gc_claim_id,gc_claimed_at,gc_state,storage_class,storage_key,gc_orphan_handoff FROM blocks WHERE org_id=? AND block_id=?`, data.Org.String(), data.Block).Consistency(gocql.Serial).Scan(&claim, &at, &state, &class, &key, &committed)
	if err != nil || claim != a.ClaimID || !at.Equal(a.ClaimedAt) || state != dbpkg.BlockGCStateDeleting || class != a.Target.StorageClass || key != a.Target.StorageKey || committed == nil || !*committed {
		t.Fatalf("C3 blocks do not prove exact committed D: claim=%s at=%v state=%s P=(%s,%s) handoff=%v err=%v", claim, at, state, class, key, committed, err)
	}
	store := gcpkg.NewCassandraStore(fx.database)
	row, found, err := store.GetS3OrphanExact(data.Org, data.Block, a)
	if err != nil || !found || row.Authority != a || row.RecoveryState != gcpkg.S3OrphanRecoveryStatePrepared || row.RecoveryPhase != gcpkg.S3OrphanPhasePendingS3 || !row.FirstSeenAt.Equal(data.FirstSeenAt) {
		t.Fatalf("C3 is not exact PREPARED/pending_s3: %+v found=%v err=%v", row, found, err)
	}
	if obs := store.ObserveBlockDeleteLifecycle(data.Org, data.Block, gcpkg.CommittedBlockDeleteAuthorityForTest(a)); obs.Outcome != gcpkg.StartBlockDeleteOrphanSameAuthority {
		t.Fatalf("C3 lifecycle is not exact PUBLISHED: %+v", obs)
	}
	snapshot := e101Capture(fx.database, data)
	if len(snapshot.Errors) != 0 || !snapshot.RootFound || snapshot.Root.Authority != a || !snapshot.Root.FirstSeenAt.Equal(data.FirstSeenAt) || !snapshot.K1Exists || len(snapshot.K1Bytes) == 0 || len(snapshot.Rows["blocks"]) != 1 || len(snapshot.Rows["gc_s3_orphans"]) != 1 || len(snapshot.Rows["gc_s3_orphans_by_day"]) != 1 {
		t.Fatalf("C3 root/discovery/canonical/K1 not intact: %+v", snapshot)
	}
	e101bBookkeeping(t, fx.database, data, true)
	e17AssertRefs(t, fx)
	fx.assertHeadUnchanged(t)
	w2AssertBytes(t, fx)
	if rows := w2Repairs(t, fx); len(rows) != 0 {
		t.Fatalf("C3 invented repairs: %+v", rows)
	}
	t.Logf("durable C3 P=%+v D=%+v first_seen=%s: committed handoff, PUBLISHED lifecycle, PREPARED orphan, root/discovery/canonical/K1 intact", a.Target, a, data.FirstSeenAt)
}

func e101gBeforeRecovery(t *testing.T, fx *w2CreateFileFixture, data e101ProcessData, _ e101Snapshot) {
	// Orphan recovery goes first so the killed leg exercises real PREPARED promotion,
	// rather than letting a queue worker complete promotion before that recovery.
	e101Recover(t, data)
	store := gcpkg.NewCassandraStore(fx.database)
	if obs := store.ObserveBlockDeleteLifecycle(data.Org, data.Block, gcpkg.CommittedBlockDeleteAuthorityForTest(data.Authority)); obs.Outcome != gcpkg.StartBlockDeleteOrphanLifecycleAdvanced {
		t.Fatalf("C3 independent recovery is not TERMINAL: %+v", obs)
	}
	snap := e101Capture(fx.database, data)
	if len(snap.Errors) != 0 || snap.RootFound || snap.K1Exists || len(snap.Rows["blocks"]) != 0 || len(snap.Rows["gc_s3_orphans"]) != 0 || len(snap.Rows["gc_s3_orphans_by_day"]) != 0 {
		t.Fatalf("C3 physical recovery did not settle exact identity: %+v", snap)
	}
	for replay := 0; replay < 2; replay++ {
		data.Mode = "settle"
		cmd, cancel := e101Command(t, data)
		out, err := cmd.CombinedOutput()
		cancel()
		t.Logf("independent C3 queue settlement/replay: %s", out)
		if err != nil || !strings.Contains(string(out), "E101B_QUEUE_REPLAY_COMPLETED") {
			t.Fatalf("C3 queue settlement not proved: %v", err)
		}
		e101bBookkeeping(t, fx.database, data, false)
		e101dAssertSettled(t, fx.database, data)
	}
}
