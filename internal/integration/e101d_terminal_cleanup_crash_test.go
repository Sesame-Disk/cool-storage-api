//go:build integration

package integration

import (
	"os"
	"testing"
	"time"

	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/google/uuid"
)

const e101dEvidenceEnv = "SESAMEFS_REQUIRE_E101_TERMINAL_CRASH_EVIDENCE"
const e101dTerminalCut = "terminal-before-cleanup"

var e101dObserved = map[string]bool{}
var e101dCuts = []string{e101dTerminalCut}

func e101dMissing() []string          { return e101MissingLegs(e101dObserved, e101dCuts) }
func e101dCut(cut string) bool        { return cut == e101dTerminalCut }
func e101RecoveryCut(cut string) bool { return e101cCut(cut) || e101dCut(cut) }

// The productive caller already persisted TERMINAL. Pause before this real
// aggregate call; its internal orphan/discovery/root substeps are separate cuts.
type e101dPausedStore struct {
	*w2OwnedRecoveryStore
	t    *testing.T
	data e101ProcessData
}

func (s *e101dPausedStore) DeleteS3Orphan(org uuid.UUID, block string, a gcpkg.BlockDeleteAuthority, firstSeen time.Time) error {
	if org == s.data.Org && block == s.data.Block && a == s.data.Authority && e101dCut(s.data.Cut) {
		if firstSeen.IsZero() || !firstSeen.Equal(s.data.FirstSeenAt) {
			s.t.Fatal("terminal cleanup caller lost exact discovery coordinate")
		}
		e101Pause(s.t, s.data, a)
	}
	return s.GCStore.DeleteS3Orphan(org, block, a, firstSeen)
}
func e101dFirstSeen(t *testing.T, data e101ProcessData) time.Time {
	store := gcpkg.NewCassandraStore(shareProjectionDBForTest(t))
	root, found, err := store.GetS3OrphanRecoveryRootExact(data.Org, data.Block, data.Authority)
	if err != nil || !found || root.Authority != data.Authority || root.FirstSeenAt.IsZero() {
		t.Fatalf("terminal preparation lost exact root coordinate: %+v found=%v err=%v", root, found, err)
	}
	return root.FirstSeenAt
}
func TestE101TerminalCleanupProcessCrash(t *testing.T) {
	e101dObserved = map[string]bool{}
	if endpoint := os.Getenv("SESAMEFS_E19_ISOLATED_URL"); endpoint != "" && os.Getenv("SESAMEFS_E101D_CHILD") != "1" {
		e101IsolatedMatrix(t, endpoint, "TestE101TerminalCleanupProcessCrash", e101dEvidenceEnv, "SESAMEFS_E101D_CHILD", e101dObserved, e101dCuts)
		return
	}
	e19RequireNoBackgroundGC(t)
	e101RunMatrix(t, e101dCuts, e101dObserved, e101dBoundary, nil)
}
func TestE101TerminalFailureSnapshotSurvivesTeardown(t *testing.T) {
	e101FailureControl(t, "TestE101TerminalCleanupProcessCrash", e101dTerminalCut, e101dEvidenceEnv, 0, 1)
}
func e101dBoundary(t *testing.T, fx *w2CreateFileFixture, data e101ProcessData) {
	t.Helper()
	a := data.Authority
	if a.IsZero() || a.Target != fx.target || data.FirstSeenAt.IsZero() {
		t.Fatalf("wrong terminal boundary identity: %+v", data)
	}
	store := gcpkg.NewCassandraStore(fx.database)
	if observation := store.ObserveBlockDeleteLifecycle(data.Org, data.Block, gcpkg.CommittedBlockDeleteAuthorityForTest(a)); observation.Outcome != gcpkg.StartBlockDeleteOrphanLifecycleAdvanced {
		t.Fatalf("exact lifecycle is not TERMINAL: %+v", observation)
	}
	row, found, err := store.GetS3OrphanExact(data.Org, data.Block, a)
	if err != nil || !found || row.Authority != a || row.RecoveryState != gcpkg.S3OrphanRecoveryStateCommitted || row.RecoveryPhase != gcpkg.S3OrphanPhasePendingMappingCleanup || !row.FirstSeenAt.Equal(data.FirstSeenAt) {
		t.Fatalf("terminal cut lost exact orphan: %+v found=%v err=%v", row, found, err)
	}
	snapshot := e101Capture(fx.database, data)
	if len(snapshot.Errors) != 0 || !snapshot.RootFound || snapshot.Root.Authority != a || !snapshot.Root.FirstSeenAt.Equal(data.FirstSeenAt) || snapshot.K1Exists || len(snapshot.Rows["blocks"]) != 0 || len(snapshot.Rows["gc_s3_orphans_by_day"]) != 1 {
		t.Fatalf("terminal cut lost root/discovery or restored canonical/K1: %+v", snapshot)
	}
	e101bBookkeeping(t, fx.database, data, false)
	e17AssertRefs(t, fx)
	fx.assertHeadUnchanged(t)
	if rows := w2Repairs(t, fx); len(rows) != 0 {
		t.Fatalf("terminal cut invented repairs: %+v", rows)
	}
	t.Logf("durable terminal pre-kill org=%s L=%s P=%+v D=%+v first_seen=%s orphan/discovery/root retained; K1/canonical absent", data.Org, data.Block, a.Target, a, data.FirstSeenAt)
}
func e101dAssertSettled(t *testing.T, database *dbpkg.DB, data e101ProcessData) {
	t.Helper()
	snapshot := e101Capture(database, data)
	if len(snapshot.Errors) != 0 || snapshot.RootFound || snapshot.K1Exists || snapshot.QueueFound || len(snapshot.Rows["blocks"]) != 0 || len(snapshot.Rows["gc_s3_orphans"]) != 0 || len(snapshot.Rows["gc_s3_orphans_by_day"]) != 0 {
		t.Fatalf("terminal cleanup/replay did not settle exact identity: %+v", snapshot)
	}
}
