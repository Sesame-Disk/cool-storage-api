//go:build integration

package integration

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

const e101bEvidenceEnv = "SESAMEFS_REQUIRE_E101_G3_CRASH_EVIDENCE"

var e101bObserved = map[string]bool{}
var e101bCuts = []string{"before-retirement", "after-retirement"}

func e101bMissing() []string   { return e101MissingLegs(e101bObserved, e101bCuts) }
func e101bCut(cut string) bool { return cut == e101bCuts[0] || cut == e101bCuts[1] }

type e101bPausedStore struct{ *e101PausedStore }

func (s *e101bPausedStore) PromoteBlockDeleteOrphan(org uuid.UUID, block string, a gcpkg.CommittedBlockDeleteAuthority) gcpkg.StartBlockDeleteOrphanResult {
	r := s.GCStore.PromoteBlockDeleteOrphan(org, block, a)
	if org == s.org && block == s.block && s.data.Cut == e101bCuts[0] && (r.Outcome == gcpkg.StartBlockDeleteOrphanCreated || r.Outcome == gcpkg.StartBlockDeleteOrphanSameAuthority) {
		s.hold(a.Authority())
	}
	return r
}
func (s *e101bPausedStore) FinalizeBlockDelete(org uuid.UUID, block string, a gcpkg.CommittedBlockDeleteAuthority) (gcpkg.BlockDeleteFinalizeResult, error) {
	r, err := s.GCStore.FinalizeBlockDelete(org, block, a)
	if org == s.org && block == s.block && s.data.Cut == e101bCuts[1] && err == nil && r.Outcome == gcpkg.BlockDeleteFinalized {
		s.hold(a.Authority())
	}
	return r, err
}

func TestE101G3ProcessCrash(t *testing.T) {
	e101bObserved = map[string]bool{}
	if endpoint := os.Getenv("SESAMEFS_E19_ISOLATED_URL"); endpoint != "" && os.Getenv("SESAMEFS_E101B_CHILD") != "1" {
		e101IsolatedMatrix(t, endpoint, "TestE101G3ProcessCrash", e101bEvidenceEnv, "SESAMEFS_E101B_CHILD", e101bObserved, e101bCuts)
		return
	}
	e19RequireNoBackgroundGC(t) // Also requires the controlled Docker fleet.
	e101RunMatrix(t, e101bCuts, e101bObserved, e101bBoundary, e101bBeforeRecovery)
}
func TestE101G3FailureSnapshotSurvivesTeardown(t *testing.T) {
	e101FailureControl(t, "TestE101G3ProcessCrash", e101bCuts[1], e101bEvidenceEnv, 0, 1)
}
func e101bBoundary(t *testing.T, fx *w2CreateFileFixture, data e101ProcessData) {
	e101bContinuation(t, fx, data, data.Cut == e101bCuts[1])
	e101bBookkeeping(t, fx.database, data, true)
}
func e101bContinuation(t *testing.T, fx *w2CreateFileFixture, data e101ProcessData, retired bool) {
	t.Helper()
	a := data.Authority
	if a.IsZero() || a.Target != fx.target {
		t.Fatalf("wrong G3 boundary identity: %+v", a)
	}
	var claim, state, class, key string
	var at time.Time
	var committed *bool
	err := fx.database.Session().Query(`SELECT gc_claim_id,gc_claimed_at,gc_state,storage_class,storage_key,gc_orphan_handoff FROM blocks WHERE org_id=? AND block_id=?`, fx.orgID, fx.blockID).Consistency(gocql.Serial).Scan(&claim, &at, &state, &class, &key, &committed)
	if retired {
		if !errors.Is(err, gocql.ErrNotFound) {
			t.Fatalf("G3 canonical retirement not observed: err=%v P=(%s,%s)", err, class, key)
		}
	} else if err != nil || claim != a.ClaimID || !at.Equal(a.ClaimedAt) || state != dbpkg.BlockGCStateDeleting || class != a.Target.StorageClass || key != a.Target.StorageKey || committed == nil || !*committed {
		t.Fatalf("G3 exact committed blocks not observed: claim=%s at=%v state=%s P=(%s,%s) handoff=%v err=%v", claim, at, state, class, key, committed, err)
	}
	store := gcpkg.NewCassandraStore(fx.database)
	row, found, err := store.GetS3OrphanExact(data.Org, data.Block, a)
	if err != nil || !found || row.Authority != a || row.RecoveryState != gcpkg.S3OrphanRecoveryStateCommitted || row.RecoveryPhase != gcpkg.S3OrphanPhasePendingS3 {
		t.Fatalf("G3 committed continuation: %+v found=%v err=%v", row, found, err)
	}
	root, found, err := store.GetS3OrphanRecoveryRootExact(data.Org, data.Block, a)
	if err != nil || !found || root.Authority != a || !root.FirstSeenAt.Equal(row.FirstSeenAt) {
		t.Fatalf("G3 exact durable root: %+v found=%v err=%v", root, found, err)
	}
	if observation := store.ObserveBlockDeleteLifecycle(data.Org, data.Block, gcpkg.CommittedBlockDeleteAuthorityForTest(a)); observation.Outcome != gcpkg.StartBlockDeleteOrphanSameAuthority {
		t.Fatalf("G3 exact published lifecycle: %+v", observation)
	}
	e17AssertRefs(t, fx)
	fx.assertHeadUnchanged(t)
	w2AssertBytes(t, fx)
	if rows := w2Repairs(t, fx); len(rows) != 0 {
		t.Fatalf("G3 unexpected repair: %+v", rows)
	}
	t.Logf("durable G3 cut=%s retired=%v P=%+v D=%+v root=%+v K1 intact", data.Cut, retired, a.Target, a, root)
}

// Allow an empty owned queue for an idempotent replay; discovery is still a real bounded read.
type e101bOwnedQueue struct {
	gcpkg.GCStore
	data e101ProcessData
}

func (s *e101bOwnedQueue) DequeueBatch(org uuid.UUID, _ int, cutoff time.Time) ([]gcpkg.QueueItem, error) {
	rows, err := s.GCStore.DequeueBatch(org, 100000, cutoff)
	if err != nil {
		return nil, err
	}
	if len(rows) >= 100000 {
		return nil, fmt.Errorf("G3 queue isolation read bound reached")
	}
	var own []gcpkg.QueueItem
	for _, row := range rows {
		if row.OrgID == s.data.Org && row.ItemType == gcpkg.ItemBlock && row.ItemID == s.data.Block {
			if row.BlockGCCandidateIdentity != s.data.Candidate.Identity() {
				return nil, fmt.Errorf("G3 foreign candidate identity for owned L: %+v", row)
			}
			own = append(own, row)
		}
	}
	if len(own) > 1 {
		return nil, fmt.Errorf("G3 duplicate owned queue: %d", len(own))
	}
	return own, nil
}
func e101bBookkeeping(t *testing.T, database *dbpkg.DB, data e101ProcessData, retained bool) {
	t.Helper()
	store := gcpkg.NewCassandraStore(database)
	candidate, found, err := store.GetBlockGCCandidateExact(data.Org, data.Block, data.Candidate.Identity())
	if err != nil || found != retained || (found && candidate.Identity() != data.Candidate.Identity()) {
		t.Fatalf("G3 candidate retained=%v: %+v found=%v err=%v", retained, candidate, found, err)
	}
	rows, err := (&e101bOwnedQueue{GCStore: store, data: data}).DequeueBatch(data.Org, 100000, time.Now().UTC().Add(time.Hour))
	want := 0
	if retained {
		want = 1
	}
	if err != nil || len(rows) != want || (retained && (rows[0].RetryCount != 0 || !rows[0].QueuedAt.Equal(data.Candidate.CandidateAt))) {
		t.Fatalf("G3 queue retained=%v rows=%+v err=%v", retained, rows, err)
	}
	if !retained {
		pending, err := store.PendingItemExists(data.Org, uuid.Nil, gcpkg.ItemBlock, data.Block, data.Candidate.ItemIdentity())
		if err != nil || pending {
			t.Fatalf("G3 owned pending row remains: %v %v", pending, err)
		}
	}
	failed, err := store.ListFailedItems(data.Org, 100000)
	if err != nil || len(failed) >= 100000 {
		t.Fatalf("G3 failed-items read incomplete: %v", err)
	}
	for _, row := range failed {
		if row.ItemType == gcpkg.ItemBlock && row.ItemID == data.Block {
			t.Fatalf("G3 owned item escaped to DLQ: %+v", row)
		}
	}
}
func e101bReplay(t *testing.T, database *dbpkg.DB, data e101ProcessData) {
	store := &e101bOwnedQueue{GCStore: gcpkg.NewCassandraStore(database), data: data}
	rows, err := store.DequeueBatch(data.Org, 100000, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if n, err := w2Worker(t, store, data.Class).ProcessOrgOnce(t.Context(), data.Org); err != nil || n != len(rows) {
		t.Fatalf("G3 replay processed=%d want=%d err=%v", n, len(rows), err)
	}
	t.Log("E101B_QUEUE_REPLAY_COMPLETED")
}
func e101bBeforeRecovery(t *testing.T, fx *w2CreateFileFixture, data e101ProcessData, before e101Snapshot) {
	t.Helper()
	for replay := 0; replay < 2; replay++ {
		data.Mode = "settle"
		cmd, cancel := e101Command(t, data)
		out, err := cmd.CombinedOutput()
		cancel()
		t.Logf("independent G3 queue replay: %s", out)
		if err != nil || !bytes.Contains(out, []byte("E101B_QUEUE_REPLAY_COMPLETED")) {
			t.Fatalf("G3 queue replay not proved: %v", err)
		}
		e101bBookkeeping(t, fx.database, data, false)
		e101bContinuation(t, fx, data, true)
		after := e101Capture(fx.database, data)
		if len(after.Errors) != 0 || !reflect.DeepEqual(before.Rows["gc_s3_orphans"], after.Rows["gc_s3_orphans"]) || !reflect.DeepEqual(before.Rows["gc_block_delete_lifecycles"], after.Rows["gc_block_delete_lifecycles"]) || before.Root != after.Root || before.RootFound != after.RootFound || before.K1Exists != after.K1Exists || !bytes.Equal(before.K1Bytes, after.K1Bytes) {
			t.Fatalf("G3 queue replay changed exact D/continuation/K1 before physical recovery: errors=%v", after.Errors)
		}
	}
}
