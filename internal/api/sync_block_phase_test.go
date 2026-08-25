package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"

	v2 "github.com/Sesame-Disk/sesamefs/internal/api/v2"
)

// TestPutBlockForwardsPhaseAndGatesQuotaToInitial pins the two phase-dependent
// behaviors of the desktop-sync hot path that the AST guard cannot express.
//
// The guard proves the funnel passes SOME forwarded phase parameter; it cannot
// prove the retry driver's confirmation phase actually reaches the resolver, and
// it cannot see into the quota conditional at all. Both were live regressions
// before this branch: a confirmation observation that re-entered the mint path,
// and a storage-quota precheck that re-ran during confirmation and returned a 403
// on a block whose INSTALL had already been applied.
//
// Replacing "if phase == BlockMaterializationInitial" with an unconditional check
// in PutBlock's quota gate, or passing the constant instead of the parameter to
// the resolver, must fail here.
func TestPutBlockForwardsPhaseAndGatesQuotaToInitial(t *testing.T) {
	oldProbe := syncProbeUploadedBlockReuseFn
	oldPrepare := syncPrepareUploadedBlockProbeFn
	oldResolve := syncResolveNeedsPutBlockStoreFn
	oldMaterialize := syncPutBlockMaterializationTargetFn
	oldPut := syncPutBlockAutoDirectFn
	oldRegister := registerUploadedBlockTargetAndMappingForSyncFn
	oldRetry := syncRetryUploadedBlockMaterializationFn
	oldLookupClass := lookupLibraryStorageClassForSyncFn
	t.Cleanup(func() {
		syncProbeUploadedBlockReuseFn = oldProbe
		syncPrepareUploadedBlockProbeFn = oldPrepare
		syncResolveNeedsPutBlockStoreFn = oldResolve
		syncPutBlockMaterializationTargetFn = oldMaterialize
		syncPutBlockAutoDirectFn = oldPut
		registerUploadedBlockTargetAndMappingForSyncFn = oldRegister
		syncRetryUploadedBlockMaterializationFn = oldRetry
		lookupLibraryStorageClassForSyncFn = oldLookupClass
	})

	lookupLibraryStorageClassForSyncFn = func(*SyncHandler, string, string) (string, error) { return "hot", nil }
	syncProbeUploadedBlockReuseFn = func(*db.DB, string, string) (db.BlockReuseProbe, error) {
		return db.BlockReuseProbe{Decision: db.BlockReuseNeedsPut}, nil
	}
	syncPrepareUploadedBlockProbeFn = func(_ *db.DB, _, _ string, probe db.BlockReuseProbe) (db.BlockReuseProbe, error) {
		return probe, nil
	}

	var resolvedPhases []v2.BlockMaterializationPhase
	syncResolveNeedsPutBlockStoreFn = func(_ *storage.Manager, preferred *storage.BlockStore, class string, _ db.BlockReuseProbe, _, _ string, phase v2.BlockMaterializationPhase) (v2.BlockMaterializationTarget, error) {
		resolvedPhases = append(resolvedPhases, phase)
		return v2.BlockMaterializationTarget{Store: preferred, StorageClass: class, StorageKey: "key", FreshInstall: phase == v2.BlockMaterializationInitial}, nil
	}
	syncPutBlockMaterializationTargetFn = func(ctx context.Context, _ *db.DB, _ string, _ string, target v2.BlockMaterializationTarget, data []byte, put func(context.Context, *storage.BlockStore, string, []byte) (string, error)) (string, error) {
		return put(ctx, target.Store, target.StorageKey, data)
	}
	syncPutBlockAutoDirectFn = func(context.Context, *storage.BlockStore, string, []byte) (string, error) {
		return "key", nil
	}
	registerUploadedBlockTargetAndMappingForSyncFn = func(context.Context, *db.DB, string, string, string, string, int, v2.BlockMaterializationTarget, string) error {
		return nil
	}

	// An allowing checker: the point is WHEN it is consulted, not what it says.
	// A refusing one would mask the regression, because the initial phase would
	// short-circuit before confirmation ever ran.
	quota := &fakeAPIQuotaChecker{
		storageStatus: traffic.QuotaStatus{Allowed: true},
		trafficStatus: traffic.QuotaStatus{Allowed: true},
	}
	setAPIQuotaChecker(t, quota)

	var initialQuotaChecks, confirmationQuotaChecks int
	syncRetryUploadedBlockMaterializationFn = func(_ context.Context, _, _ string, store func(v2.BlockMaterializationPhase) error, materialize func() error, _ func(), _ func() (bool, error)) error {
		if err := store(v2.BlockMaterializationInitial); err != nil {
			return err
		}
		initialQuotaChecks = len(quota.storageBytes)
		if err := materialize(); err != nil {
			return err
		}
		if err := store(v2.BlockMaterializationConfirmation); err != nil {
			return err
		}
		confirmationQuotaChecks = len(quota.storageBytes) - initialQuotaChecks
		return nil
	}

	h := newInflightTestHandler(t, syncInflightConfig(1, 1, 5*time.Second))
	h.db = &db.DB{}
	h.storage = &storage.S3Store{}
	r := putBlockRouterFor(h, "00000000-0000-0000-0000-000000000001", "user")
	req := httptest.NewRequest(http.MethodPut, "/seafhttp/repo/repo/block/0123456789012345678901234567890123456789", strings.NewReader("hello"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body = %q, want 200", w.Code, w.Body.String())
	}

	want := []v2.BlockMaterializationPhase{v2.BlockMaterializationInitial, v2.BlockMaterializationConfirmation}
	if len(resolvedPhases) != len(want) {
		t.Fatalf("resolver saw %d phases (%v), want %v", len(resolvedPhases), resolvedPhases, want)
	}
	for i, phase := range want {
		if resolvedPhases[i] != phase {
			t.Errorf("resolver phase[%d] = %d, want %d; the funnel is not forwarding the driver's phase", i, resolvedPhases[i], phase)
		}
	}

	if initialQuotaChecks != 1 {
		t.Errorf("initial-phase storage quota checks = %d, want 1", initialQuotaChecks)
	}
	if confirmationQuotaChecks != 0 {
		t.Errorf("confirmation-phase storage quota checks = %d, want 0; re-running the precheck after INSTALL is applied is the late-403 regression", confirmationQuotaChecks)
	}
}
