package v2

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	"github.com/gin-gonic/gin"
)

// UploadBlock reports whether THIS request created the block: 201 with New=true, or
// 200 with New=false. Phase-scoping didPutAny to the initial phase changed what a
// confirmation-phase repair PUT does to that answer -- it used to flip a reused block
// to 201/New=true, and now it does not. That is the correct meaning of New, but it is
// a response-contract property with no test of its own, so these two pin both sides.

const (
	newContractOrgID     = "3fa85f64-5717-4562-b3fc-2c963f66afa6"
	newContractUserID    = "4fa85f64-5717-4562-b3fc-2c963f66afa6"
	newContractSessionID = "sess-new-contract"
)

// uploadBlockNewContractCounts records physical writes. The shared store helper
// routes BOTH the initial NeedsPut write and the confirmation repair through the
// same seam, so the phase, not the function, is what distinguishes them -- which is
// exactly the distinction this contract turns on.
type uploadBlockNewContractCounts struct {
	puts int
}

// uploadBlockNewContractHarness stubs the session, staging and materialization seams
// and returns the recorded response. probe drives the per-phase decision, and exists
// says whether the canonical object is present when a Reusable probe is verified.
func uploadBlockNewContractHarness(
	t *testing.T,
	probe func(callNumber int, orgID, blockID string) db.BlockReuseProbe,
	exists func(callNumber int) bool,
) (*httptest.ResponseRecorder, *uploadBlockNewContractCounts) {
	t.Helper()
	stubSuccessfulLibraryStorageClassLookup(t, "")
	fastBlockMaterializationRetries(t)

	oldGetSession := getBlockUploadSessionFn
	oldCount := countSessionStagedBlocksFn
	oldReserve := reserveSessionStagedBlockFn
	oldProbe := probeUploadedBlockReuseFn
	oldFreshPut := putUploadedBlockAutoDirectFn
	oldRepairPut := repairCanonicalBlockDirectFn
	oldExists := reusableCanonicalObjectExistsFn
	oldRegister := registerUploadedBlockTargetForMaterializationFn
	oldMapping := writeVerifiedWebBlockMappingFn
	oldTraffic := recordBlockUploadTrafficFn
	t.Cleanup(func() {
		getBlockUploadSessionFn = oldGetSession
		countSessionStagedBlocksFn = oldCount
		reserveSessionStagedBlockFn = oldReserve
		probeUploadedBlockReuseFn = oldProbe
		putUploadedBlockAutoDirectFn = oldFreshPut
		repairCanonicalBlockDirectFn = oldRepairPut
		reusableCanonicalObjectExistsFn = oldExists
		registerUploadedBlockTargetForMaterializationFn = oldRegister
		writeVerifiedWebBlockMappingFn = oldMapping
		recordBlockUploadTrafficFn = oldTraffic
	})

	getBlockUploadSessionFn = func(*db.DB, string) (db.BlockUploadSession, bool, error) {
		return db.BlockUploadSession{
			SessionID: newContractSessionID, OrgID: newContractOrgID, UserID: newContractUserID, RepoID: "repo-1",
			BlockSizeBytes: 1024, StagedBucketCount: 1, StagedBucketCap: 10,
		}, true, nil
	}
	countSessionStagedBlocksFn = func(*db.DB, string, int, int) (int, error) { return 0, nil }
	reserveSessionStagedBlockFn = func(*db.DB, string, int, string, int64) error { return nil }

	probeCalls := 0
	probeUploadedBlockReuseFn = func(_ *db.DB, orgID, blockID string) (db.BlockReuseProbe, error) {
		probeCalls++
		return probe(probeCalls, orgID, blockID), nil
	}

	counts := &uploadBlockNewContractCounts{}
	putUploadedBlockAutoDirectFn = func(_ context.Context, _ *storage.BlockStore, key string, _ []byte) (string, error) {
		counts.puts++
		return key, nil
	}
	repairCanonicalBlockDirectFn = func(_ context.Context, _ *storage.BlockStore, key string, _ []byte) (string, error) {
		counts.puts++
		return key, nil
	}
	existsCalls := 0
	reusableCanonicalObjectExistsFn = func(context.Context, *storage.BlockStore, string) (bool, error) {
		existsCalls++
		return exists(existsCalls), nil
	}
	registerUploadedBlockTargetForMaterializationFn = func(context.Context, *db.DB, string, string, string, string, int, BlockMaterializationTarget, string) error {
		return nil
	}
	writeVerifiedWebBlockMappingFn = func(*db.DB, string, string, string, string) error { return nil }
	recordBlockUploadTrafficFn = func(traffic.TrafficPeriodRecorder, traffic.QuotaStatus, string, string, string, int64) {
	}

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("org_id", newContractOrgID)
		c.Set("user_id", newContractUserID)
		c.Next()
	})
	h := &BlockHandler{
		storage: &storage.S3Store{}, db: &db.DB{},
		config: &config.Config{WebUploads: config.WebUploadsConfig{EnableWebBlockUpload: true}},
	}
	r.POST("/api/v2/blocks/upload", h.UploadBlock)
	req := httptest.NewRequest(http.MethodPost, "/api/v2/blocks/upload", bytes.NewBufferString("hello"))
	req.ContentLength = 5
	req.Header.Set("X-Block-Upload-Session", newContractSessionID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w, counts
}

func reusableProbeFor(orgID, blockID string) db.BlockReuseProbe {
	return db.BlockReuseProbe{
		Decision:     db.BlockReuseReusable,
		StorageClass: "legacy",
		StorageKey:   fmt.Sprintf("blocks/%s/%s/%s/%s", orgID, blockID[:2], blockID[2:4], blockID),
	}
}

func decodeUploadBlockResponse(t *testing.T, w *httptest.ResponseRecorder) UploadBlockResponse {
	t.Helper()
	var body UploadBlockResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response %q: %v", w.Body.String(), err)
	}
	return body
}

// TestUploadBlockFreshPutAnswers201New reports a block this request actually created.
func TestUploadBlockFreshPutAnswers201New(t *testing.T) {
	w, counts := uploadBlockNewContractHarness(t,
		func(call int, orgID, blockID string) db.BlockReuseProbe {
			if call == 1 {
				// Initial: rowless, so this request mints and PUTs the block.
				return db.BlockReuseProbe{Decision: db.BlockReuseNeedsPut}
			}
			// Confirmation: the block this request installed is now canonical.
			return reusableProbeFor(orgID, blockID)
		},
		func(int) bool { return true },
	)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", w.Code, w.Body.String())
	}
	if body := decodeUploadBlockResponse(t, w); !body.New {
		t.Errorf("New = %v, want true; this request created the block", body.New)
	}
	if counts.puts != 1 {
		t.Errorf("physical writes = %d, want 1 (the initial mint+PUT only)", counts.puts)
	}
}

// TestUploadBlockConfirmationRepairKeeps200NotNew is the regression this contract
// exists for. The block was already reusable, so this request did not create it; the
// confirmation phase then finds the object missing and repairs it. That repair is a
// physical write, but it must NOT promote the answer to 201/New=true.
func TestUploadBlockConfirmationRepairKeeps200NotNew(t *testing.T) {
	w, counts := uploadBlockNewContractHarness(t,
		// Reusable in BOTH phases: the block already existed before this request.
		func(_ int, orgID, blockID string) db.BlockReuseProbe { return reusableProbeFor(orgID, blockID) },
		// Present initially, gone by confirmation -> confirmation repairs it.
		func(call int) bool { return call == 1 },
	)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	if body := decodeUploadBlockResponse(t, w); body.New {
		t.Errorf("New = %v, want false; a confirmation-phase repair must not turn a reused block into a created one", body.New)
	}
	if counts.puts != 1 {
		t.Errorf("physical writes = %d, want exactly 1 (the confirmation repair); without it this test would not exercise the contract at all", counts.puts)
	}
}

// TestUploadBlockInitialRepairOfExistingCanonicalKeeps200NotNew is the other half of
// the same contract, and the half the confirmation-phase test cannot reach. The
// shared store helper returns didPut=true for a Reusable probe whose canonical
// OBJECT is missing and gets repaired -- and that repair can happen in the INITIAL
// phase. Counting any initial-phase physical write as "created" therefore reports
// 201/New=true for a block that was already canonical before this request started,
// which contradicts both UploadBlockResponse.New and the handler comment.
//
// New must follow the fresh-install AUTHORITY, not the fact that bytes were written.
func TestUploadBlockInitialRepairOfExistingCanonicalKeeps200NotNew(t *testing.T) {
	w, counts := uploadBlockNewContractHarness(t,
		// Already canonical in both phases: this request did not create it.
		func(_ int, orgID, blockID string) db.BlockReuseProbe { return reusableProbeFor(orgID, blockID) },
		// Missing on the INITIAL verification -> repaired there, present afterwards.
		func(call int) bool { return call != 1 },
	)

	if counts.puts != 1 {
		t.Fatalf("physical writes = %d, want 1 (the initial-phase repair); without it this test would not exercise the contract", counts.puts)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; repairing an already-canonical block is not creating it, body=%s", w.Code, w.Body.String())
	}
	if body := decodeUploadBlockResponse(t, w); body.New {
		t.Errorf("New = %v, want false; the block was canonical before this request", body.New)
	}
}
