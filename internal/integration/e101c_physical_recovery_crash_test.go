//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

const e101cEvidenceEnv = "SESAMEFS_REQUIRE_E101_PHYSICAL_CRASH_EVIDENCE"

var e101cObserved = map[string]bool{}
var e101cCuts = []string{"after-delete", "after-phase"}

func e101cMissing() []string   { return e101MissingLegs(e101cObserved, e101cCuts) }
func e101cCut(cut string) bool { return cut == e101cCuts[0] || cut == e101cCuts[1] }

// Capture only the real successful retirement, then let the worker settle its work.
type e101cPreparationStore struct {
	*w2ClosureOwnedQueue
	authority gcpkg.BlockDeleteAuthority
}

func (s *e101cPreparationStore) FinalizeBlockDelete(org uuid.UUID, block string, a gcpkg.CommittedBlockDeleteAuthority) (gcpkg.BlockDeleteFinalizeResult, error) {
	r, err := s.GCStore.FinalizeBlockDelete(org, block, a)
	if org == s.org && block == s.block && err == nil && r.Outcome == gcpkg.BlockDeleteFinalized {
		s.authority = a.Authority()
	}
	return r, err
}

// C6 pauses before the phase call, C7 after its real successful return. The caller
// already completed the real S3 DELETE; the parent verifies absence independently.
type e101cPausedStore struct {
	*w2OwnedRecoveryStore
	t    *testing.T
	data e101ProcessData
}

func (s *e101cPausedStore) MarkS3OrphanMappingCleanupPending(org uuid.UUID, block string, a gcpkg.BlockDeleteAuthority, sha1 string, now time.Time) error {
	own := org == s.data.Org && block == s.data.Block && a == s.data.Authority
	if own && s.data.Cut == e101cCuts[0] {
		e101Pause(s.t, s.data, a)
	}
	err := s.GCStore.MarkS3OrphanMappingCleanupPending(org, block, a, sha1, now)
	if own && s.data.Cut == e101cCuts[1] && err == nil {
		e101Pause(s.t, s.data, a)
	}
	return err
}
func e101cChild(t *testing.T, database *dbpkg.DB, data e101ProcessData) {
	store := gcpkg.NewCassandraStore(database)
	if data.Mode == "prepare-physical" {
		scope := &w2ClosureOwnedQueue{GCStore: store, org: data.Org, block: data.Block, identity: data.Candidate.Identity()}
		capture := &e101cPreparationStore{w2ClosureOwnedQueue: scope}
		if n, err := w2Worker(t, capture, data.Class).ProcessOrgOnce(t.Context(), data.Org); err != nil || n != 1 || !scope.visited || len(scope.liveness) != 1 || scope.liveness[0] != dbpkg.BlockPublicationZero || scope.errs[0] != nil || capture.authority.IsZero() {
			t.Fatalf("productive physical preparation n=%d visited=%v D=%+v err=%v", n, scope.visited, capture.authority, err)
		}
		a := capture.authority
		a.ClaimedAt = a.ClaimedAt.UTC().Truncate(time.Millisecond)
		encoded, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(data.Marker+".prepared", encoded, 0600); err != nil {
			t.Fatal(err)
		}
		t.Log("E101C_PREPARATION_COMPLETED")
		return
	}
	scoped := &w2OwnedRecoveryStore{GCStore: store, org: data.Org, block: data.Block}
	var paused gcpkg.GCStore = &e101cPausedStore{w2OwnedRecoveryStore: scoped, t: t, data: data}
	if e101dCut(data.Cut) {
		paused = &e101dPausedStore{w2OwnedRecoveryStore: scoped, t: t, data: data}
	}
	if n, err := w2Worker(t, paused, data.Class).RecoverS3Orphans(t.Context(), 1000); err != nil || n != 1 {
		t.Fatalf("physical recovery n=%d err=%v", n, err)
	}
	t.Log("E101C_PHYSICAL_COMPLETED")
}
func e101cPrepare(t *testing.T, data e101ProcessData) e101ProcessData {
	data.Mode = "prepare-physical"
	cmd, cancel := e101Command(t, data)
	out, err := cmd.CombinedOutput()
	cancel()
	t.Logf("separate productive preparation: %s", out)
	if err != nil || !bytes.Contains(out, []byte("E101C_PREPARATION_COMPLETED")) {
		t.Fatalf("preparation not proved: %v", err)
	}
	encoded, err := os.ReadFile(data.Marker + ".prepared")
	if err != nil || json.Unmarshal(encoded, &data.Authority) != nil || data.Authority.IsZero() || data.Authority.Target != data.Candidate.Target {
		t.Fatalf("preparation exact D unavailable: %v", err)
	}
	e101bBookkeeping(t, shareProjectionDBForTest(t), data, false)
	if e101dCut(data.Cut) {
		data.FirstSeenAt = e101dFirstSeen(t, data)
	}
	data.Mode = "physical"
	return data
}
func TestE101PhysicalRecoveryProcessCrash(t *testing.T) {
	e101cObserved = map[string]bool{}
	if endpoint := os.Getenv("SESAMEFS_E19_ISOLATED_URL"); endpoint != "" && os.Getenv("SESAMEFS_E101C_CHILD") != "1" {
		e101IsolatedMatrix(t, endpoint, "TestE101PhysicalRecoveryProcessCrash", e101cEvidenceEnv, "SESAMEFS_E101C_CHILD", e101cObserved, e101cCuts)
		return
	}
	e19RequireNoBackgroundGC(t)
	e101RunMatrix(t, e101cCuts, e101cObserved, e101cBoundary, nil)
}
func TestE101PhysicalFailureSnapshotSurvivesTeardown(t *testing.T) {
	e101FailureControl(t, "TestE101PhysicalRecoveryProcessCrash", e101cCuts[1], e101cEvidenceEnv, 0, 1)
}
func e101cBoundary(t *testing.T, fx *w2CreateFileFixture, data e101ProcessData) {
	t.Helper()
	a := data.Authority
	if a.IsZero() || a.Target != fx.target {
		t.Fatalf("wrong physical cut identity: %+v", a)
	}
	store := gcpkg.NewCassandraStore(fx.database)
	var key string
	if err := fx.database.Session().Query(`SELECT storage_key FROM blocks WHERE org_id=? AND block_id=?`, data.Org.String(), data.Block).Consistency(gocql.Serial).Scan(&key); !errors.Is(err, gocql.ErrNotFound) {
		t.Fatalf("physical canonical retirement not observed by SERIAL: key=%s err=%v", key, err)
	}
	phase := gcpkg.S3OrphanPhasePendingS3
	if data.Cut == e101cCuts[1] {
		phase = gcpkg.S3OrphanPhasePendingMappingCleanup
	}
	row, found, err := store.GetS3OrphanExact(data.Org, data.Block, a)
	if err != nil || !found || row.Authority != a || row.RecoveryState != gcpkg.S3OrphanRecoveryStateCommitted || row.RecoveryPhase != phase {
		t.Fatalf("physical exact continuation: %+v found=%v err=%v", row, found, err)
	}
	root, found, err := store.GetS3OrphanRecoveryRootExact(data.Org, data.Block, a)
	if err != nil || !found || root.Authority != a || !root.FirstSeenAt.Equal(row.FirstSeenAt) {
		t.Fatalf("physical exact root: %+v found=%v err=%v", root, found, err)
	}
	if observation := store.ObserveBlockDeleteLifecycle(data.Org, data.Block, gcpkg.CommittedBlockDeleteAuthorityForTest(a)); observation.Outcome != gcpkg.StartBlockDeleteOrphanSameAuthority {
		t.Fatalf("physical lifecycle advanced before cut: %+v", observation)
	}
	exists, err := newVerificationBlockStore(t, fx.orgID).ObjectExists(t.Context(), a.Target.StorageKey)
	if err != nil || exists {
		t.Fatalf("successful physical DELETE not observed: exists=%v err=%v", exists, err)
	}
	e101bBookkeeping(t, fx.database, data, false)
	e17AssertRefs(t, fx)
	fx.assertHeadUnchanged(t)
	if rows := w2Repairs(t, fx); len(rows) != 0 {
		t.Fatalf("physical cut invented repairs: %+v", rows)
	}
	t.Logf("durable physical pre-kill cut=%s phase=%s org=%s L=%s P=%+v D=%+v K1 absent root=%+v", data.Cut, phase, data.Org, data.Block, a.Target, a, root)
}
