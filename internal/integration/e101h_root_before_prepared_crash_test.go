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

const e101hEvidenceEnv = "SESAMEFS_REQUIRE_E101_ROOT_PREPARED_CRASH_EVIDENCE"
const e101hRootCut = "root-before-prepared"
const e101hWorkerCompleted = "E101H_WORKER_COMPLETED"
const e101hRetentionCompleted = "E101H_RETENTION_COMPLETED"

var e101hObserved = map[string]bool{}
var e101hCuts = []string{e101hRootCut}

func e101hMissing() []string   { return e101MissingLegs(e101hObserved, e101hCuts) }
func e101hCut(cut string) bool { return cut == e101hRootCut }

// Hold the successful exact root INSERT before the real producer can submit
// PREPARED. Parent reads, not the callback, certify the unresolved C0 state.
type e101hRootObserver struct {
	t    *testing.T
	data e101ProcessData
	once sync.Once
}

func (o *e101hRootObserver) ObserveQuery(_ context.Context, q gocql.ObservedQuery) {
	if q.Err != nil || !strings.HasPrefix(strings.Join(strings.Fields(q.Statement), " "), "INSERT INTO gc_s3_orphan_recovery_roots ") {
		return
	}
	d, a := o.data, o.data.Authority
	if a.IsZero() || d.FirstSeenAt.IsZero() {
		return
	}
	want := []interface{}{g1RecoveryRootBucket(a), a.ClaimedAt, d.Org.String(), d.Block, a.Target.StorageClass, a.Target.StorageKey, a.ClaimID, d.FirstSeenAt, d.FirstSeenAt}
	if reflect.DeepEqual(q.Values, want) {
		o.once.Do(func() { e101Pause(o.t, d, a) })
	}
}

type e101hPreparationStore struct {
	*w2ClosureOwnedQueue
	observer *e101hRootObserver
}

func (s *e101hPreparationStore) PrepareBlockDeleteOrphan(org uuid.UUID, block string, a gcpkg.BlockDeleteAuthority, sha1 string, now time.Time) gcpkg.StartBlockDeleteOrphanResult {
	o := s.observer
	a.ClaimedAt = a.ClaimedAt.UTC().Truncate(time.Millisecond)
	if org != o.data.Org || block != o.data.Block || a.IsZero() || a.Target != o.data.Candidate.Target || !s.visited || len(s.liveness) != 1 || s.liveness[0] != dbpkg.BlockPublicationZero || len(s.errs) != 1 || s.errs[0] != nil {
		o.t.Fatal("C0 caller lacks exact owned claim/P and productive zero-liveness")
	}
	o.data.Authority, o.data.FirstSeenAt = a, now.UTC().Truncate(time.Millisecond)
	return s.GCStore.PrepareBlockDeleteOrphan(org, block, a, sha1, now)
}
func e101hChild(t *testing.T, data e101ProcessData) {
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	observer := &e101hRootObserver{t: t, data: data}
	database, err := dbpkg.NewWithObservers(cfg.Database, observer, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	scope := &w2ClosureOwnedQueue{GCStore: gcpkg.NewCassandraStore(database), org: data.Org, block: data.Block, identity: data.Candidate.Identity()}
	if n, err := w2Worker(t, &e101hPreparationStore{w2ClosureOwnedQueue: scope, observer: observer}, data.Class).ProcessOrgOnce(t.Context(), data.Org); err != nil || n != 1 || !scope.visited {
		t.Fatalf("C0 productive worker n=%d visited=%v err=%v", n, scope.visited, err)
	}
	t.Log(e101hWorkerCompleted)
}

// A zero result is evidence only if the executor actually visited this exact
// root and classified its missing orphan and unpublished lifecycle.
type e101hRetentionStore struct {
	*w2OwnedRecoveryStore
	data                e101ProcessData
	absent, unpublished bool
}

func (s *e101hRetentionStore) GetS3OrphanExact(org uuid.UUID, block string, a gcpkg.BlockDeleteAuthority) (gcpkg.S3OrphanInfo, bool, error) {
	row, found, err := s.GCStore.GetS3OrphanExact(org, block, a)
	if org == s.data.Org && block == s.data.Block && a == s.data.Authority && err == nil && !found {
		s.absent = true
	}
	return row, found, err
}
func (s *e101hRetentionStore) ObserveBlockDeleteLifecycle(org uuid.UUID, block string, a gcpkg.CommittedBlockDeleteAuthority) gcpkg.StartBlockDeleteOrphanResult {
	result := s.GCStore.ObserveBlockDeleteLifecycle(org, block, a)
	if org == s.data.Org && block == s.data.Block && a.Authority() == s.data.Authority && result.Outcome == gcpkg.StartBlockDeleteOrphanNotPublished {
		s.unpublished = true
	}
	return result
}
func e101hRetentionChild(t *testing.T, database *dbpkg.DB, data e101ProcessData) {
	scope := &e101hRetentionStore{w2OwnedRecoveryStore: &w2OwnedRecoveryStore{GCStore: gcpkg.NewCassandraStore(database), org: data.Org, block: data.Block}, data: data}
	if n, err := w2Worker(t, scope, data.Class).RecoverS3Orphans(t.Context(), 1000); err != nil || n != 0 || !scope.absent || !scope.unpublished {
		t.Fatalf("C0 retention n=%d absent=%v unpublished=%v err=%v", n, scope.absent, scope.unpublished, err)
	}
	t.Log(e101hRetentionCompleted)
}
func TestE101RootBeforePreparedProcessCrash(t *testing.T) {
	e101hObserved = map[string]bool{}
	t.Cleanup(func() {
		if os.Getenv(e101hEvidenceEnv) == "1" {
			if missing := e101hMissing(); len(missing) != 0 {
				t.Errorf("required root-before-prepared legs missing: %s", strings.Join(missing, ","))
			}
		}
	})
	if endpoint := os.Getenv("SESAMEFS_E19_ISOLATED_URL"); endpoint != "" && os.Getenv("SESAMEFS_E101H_CHILD") != "1" {
		e101IsolatedMatrix(t, endpoint, "TestE101RootBeforePreparedProcessCrash", e101hEvidenceEnv, "SESAMEFS_E101H_CHILD", e101hObserved, e101hCuts)
		return
	}
	e19RequireNoBackgroundGC(t)
	e101RunMatrix(t, e101hCuts, e101hObserved, e101hBoundary, nil)
}
func TestE101RootBeforePreparedFailureSnapshotSurvivesTeardown(t *testing.T) {
	e101FailureControl(t, "TestE101RootBeforePreparedProcessCrash", e101hRootCut, e101hEvidenceEnv, 1, 0)
}
func e101hUncommitted(t *testing.T, database *dbpkg.DB, data e101ProcessData) {
	t.Helper()
	a := data.Authority
	var claim, state, class, key string
	var at time.Time
	var committed *bool
	err := database.Session().Query(`SELECT gc_claim_id,gc_claimed_at,gc_state,storage_class,storage_key,gc_orphan_handoff FROM blocks WHERE org_id=? AND block_id=?`, data.Org.String(), data.Block).Consistency(gocql.Serial).Scan(&claim, &at, &state, &class, &key, &committed)
	if err != nil || claim != a.ClaimID || !at.Equal(a.ClaimedAt) || state != dbpkg.BlockGCStateDeleting || class != a.Target.StorageClass || key != a.Target.StorageKey || committed != nil {
		t.Fatalf("C0 exact uncommitted claim not proved: claim=%s at=%v state=%s P=(%s,%s) handoff=%v err=%v", claim, at, state, class, key, committed, err)
	}
}
func e101hBoundary(t *testing.T, fx *w2CreateFileFixture, data e101ProcessData) {
	t.Helper()
	a := data.Authority
	if a.IsZero() || a.Target != fx.target || data.FirstSeenAt.IsZero() {
		t.Fatalf("wrong C0 identity: %+v", data)
	}
	e101hUncommitted(t, fx.database, data)
	store := gcpkg.NewCassandraStore(fx.database)
	if obs := store.ObserveBlockDeleteLifecycle(data.Org, data.Block, gcpkg.CommittedBlockDeleteAuthorityForTest(a)); obs.Outcome != gcpkg.StartBlockDeleteOrphanNotPublished {
		t.Fatalf("C0 lifecycle is not absent: %+v", obs)
	}
	snap := e101Capture(fx.database, data)
	if len(snap.Errors) != 0 || !snap.RootFound || snap.Root.Authority != a || !snap.Root.FirstSeenAt.Equal(data.FirstSeenAt) || !snap.K1Exists || len(snap.K1Bytes) == 0 || len(snap.Rows["blocks"]) != 1 || len(snap.Rows["gc_s3_orphans"]) != 0 || len(snap.Rows["gc_s3_orphans_by_day"]) != 0 || len(snap.Rows["gc_block_delete_lifecycles"]) != 0 {
		t.Fatalf("C0 root/canonical/K1 or absent orphan/lifecycle/discovery not proved: %+v", snap)
	}
	e101bBookkeeping(t, fx.database, data, true)
	e17AssertRefs(t, fx)
	fx.assertHeadUnchanged(t)
	w2AssertBytes(t, fx)
	if rows := w2Repairs(t, fx); len(rows) != 0 {
		t.Fatalf("C0 invented repairs: %+v", rows)
	}
	t.Logf("durable C0 P=%+v D=%+v first_seen=%s: uncommitted claim, root retained, orphan/discovery/lifecycle absent, canonical/K1 intact", a.Target, a, data.FirstSeenAt)
}
func e101hRetain(t *testing.T, database *dbpkg.DB, data e101ProcessData, before e101Snapshot, stage string) {
	t.Helper()
	data.Mode, data.Expect = "retain", stage
	cmd, cancel := e101Command(t, data)
	defer cancel()
	out, err := cmd.CombinedOutput()
	t.Logf("independent C0 retention stage=%s: %s", stage, out)
	if err != nil || !strings.Contains(string(out), e101hRetentionCompleted) {
		t.Fatalf("C0 retention completion not proved: %v", err)
	}
	after := e101Capture(database, data)
	if len(after.Errors) != 0 || !reflect.DeepEqual(before, after) {
		t.Fatalf("C0 retention changed the durable snapshot: before=%+v after=%+v", before, after)
	}
	t.Logf("C0_SAFE_RETENTION_VERIFIED stage=%s P=%+v D=%+v; no automatic root disposal", stage, data.Authority.Target, data.Authority)
}

// Explicit owned-fixture disposal AFTER producer death and verified retention,
// or fresh post-failure comparison. This is not a recovery/convergence witness.
func e101hDispose(t *testing.T, database *dbpkg.DB, data e101ProcessData) {
	t.Helper()
	store := gcpkg.NewCassandraStore(database)
	obs := store.ObserveBlockDeleteLifecycle(data.Org, data.Block, gcpkg.CommittedBlockDeleteAuthorityForTest(data.Authority))
	if obs.Outcome == gcpkg.StartBlockDeleteOrphanSameAuthority || obs.Outcome == gcpkg.StartBlockDeleteOrphanLifecycleAdvanced {
		e101Recover(t, data) // A legitimately resumed producer already committed D.
		return
	}
	if obs.Outcome != gcpkg.StartBlockDeleteOrphanNotPublished {
		t.Fatalf("C0 disposal has unknown lifecycle: %+v", obs)
	}
	e101hUncommitted(t, database, data)
	before := e101Capture(database, data)
	if len(before.Errors) != 0 || !before.RootFound || before.Root.Authority != data.Authority || !before.Root.FirstSeenAt.Equal(data.FirstSeenAt) || !before.K1Exists || len(before.K1Bytes) == 0 || len(before.Rows["gc_s3_orphans"]) != 0 || len(before.Rows["gc_s3_orphans_by_day"]) != 0 {
		t.Fatalf("C0 disposal lost retained identity: %+v", before)
	}
	revoked := store.AbortBlockDeleteHandoff(data.Org, data.Block, data.Authority)
	if revoked.Outcome != gcpkg.BlockDeleteAbortApplied {
		t.Fatalf("C0 disposal cannot revoke exact uncommitted claim: %+v", revoked)
	}
	if err := store.DeletePreparedBlockDeleteOrphan(data.Org, data.Block, data.Authority); err != nil {
		t.Fatal(err)
	}
	if outcome, err := database.ValidateBlockRepairAuthority(data.Org.String(), data.Block, dbpkg.BlockPhysicalLocation{StorageClass: data.Authority.Target.StorageClass, StorageKey: data.Authority.Target.StorageKey}); err != nil || outcome != dbpkg.BlockRepairAuthorityAuthorized {
		t.Fatalf("C0 disposal did not release original P: %v %v", outcome, err)
	}
	after := e101Capture(database, data)
	if len(after.Errors) != 0 || after.RootFound || !after.K1Exists || !reflect.DeepEqual(before.K1Bytes, after.K1Bytes) || len(after.Rows["blocks"]) != 1 || len(after.Rows["gc_s3_orphans"]) != 0 || len(after.Rows["gc_s3_orphans_by_day"]) != 0 || len(after.Rows["gc_block_delete_lifecycles"]) != 0 || !reflect.DeepEqual(before.Rows["head"], after.Rows["head"]) {
		t.Fatalf("C0 explicit disposal damaged canonical/K1/HEAD: %+v", after)
	}
	t.Log("E101H_EXPLICIT_DISPOSAL_AFTER_RETENTION: exact claim revoked, root removed; canonical/K1 preserved until ordinary fixture teardown")
}
