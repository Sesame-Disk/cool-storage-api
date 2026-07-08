package api

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	v2 "github.com/Sesame-Disk/sesamefs/internal/api/v2"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"golang.org/x/time/rate"
)

func init() {
	gin.SetMode(gin.TestMode)
}

type mockObjectStore struct {
	data []byte
}

func TestAcquireChunkedUploadLibraryFinalizePermitSerializesSameRepo(t *testing.T) {
	releaseFirst, err := acquireChunkedUploadLibraryFinalizePermit(context.Background(), "repo-1")
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	releasedFirst := false
	defer func() {
		if !releasedFirst {
			releaseFirst()
		}
	}()

	blockedCtx, cancelBlocked := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancelBlocked()
	releaseSecond, err := acquireChunkedUploadLibraryFinalizePermit(blockedCtx, "repo-1")
	if releaseSecond != nil {
		releaseSecond()
		t.Fatal("second acquire should not succeed while first permit is held for the same repo")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second acquire error = %v, want context deadline exceeded while blocked", err)
	}

	releaseFirst()
	releasedFirst = true

	releaseSecond, err = acquireChunkedUploadLibraryFinalizePermit(context.Background(), "repo-1")
	if err != nil {
		t.Fatalf("second acquire failed after release: %v", err)
	}
	releaseSecond()
}

func TestAcquireChunkedUploadLibraryFinalizePermitDoesNotBlockDifferentRepos(t *testing.T) {
	releaseFirst, err := acquireChunkedUploadLibraryFinalizePermit(context.Background(), "repo-1")
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	defer releaseFirst()

	releaseSecond, err := acquireChunkedUploadLibraryFinalizePermit(context.Background(), "repo-2")
	if err != nil {
		t.Fatalf("second acquire for different repo failed: %v", err)
	}
	releaseSecond()
}

func TestNewSeafHTTPUploadMetadataFinalizeContext_IgnoresCanceledRequest(t *testing.T) {
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	cancelRequest()

	finalizeCtx, cancelFinalize := newSeafHTTPUploadMetadataFinalizeContext()
	defer cancelFinalize()

	if err := requestCtx.Err(); !errors.Is(err, context.Canceled) {
		t.Fatalf("requestCtx.Err() = %v, want context canceled", err)
	}
	if err := finalizeCtx.Err(); err != nil {
		t.Fatalf("finalizeCtx.Err() = %v, want nil for server-side finalize context", err)
	}

	select {
	case <-finalizeCtx.Done():
		t.Fatal("server-side finalize context should stay alive independent of the request context")
	default:
	}
}

func TestAcquireSeafHTTPDistributedUploadFinalizeLeaseRenewsWhileHeld(t *testing.T) {
	origTryAcquire := tryAcquireSeafHTTPUploadFinalizeLeaseFn
	origRenew := renewSeafHTTPUploadFinalizeLeaseFn
	origRelease := releaseSeafHTTPUploadFinalizeLeaseFn
	defer func() {
		tryAcquireSeafHTTPUploadFinalizeLeaseFn = origTryAcquire
		renewSeafHTTPUploadFinalizeLeaseFn = origRenew
		releaseSeafHTTPUploadFinalizeLeaseFn = origRelease
	}()

	leaseTTL := 40 * time.Millisecond
	renewInterval := 10 * time.Millisecond

	type leaseState struct {
		token     string
		expiresAt time.Time
	}

	var (
		mu         sync.Mutex
		state      = map[string]leaseState{}
		renewCount int
	)

	tryAcquireSeafHTTPUploadFinalizeLeaseFn = func(ctx context.Context, database *db.DB, leaseRole, leaseToken string, ttlSeconds int) (bool, error) {
		mu.Lock()
		defer mu.Unlock()
		now := time.Now()
		current := state[leaseRole]
		if current.token == "" || !now.Before(current.expiresAt) {
			state[leaseRole] = leaseState{token: leaseToken, expiresAt: now.Add(leaseTTL)}
			return true, nil
		}
		return false, nil
	}

	renewSeafHTTPUploadFinalizeLeaseFn = func(ctx context.Context, database *db.DB, leaseRole, leaseToken string, ttlSeconds int) (bool, error) {
		mu.Lock()
		defer mu.Unlock()
		now := time.Now()
		current := state[leaseRole]
		if current.token != leaseToken || !now.Before(current.expiresAt) {
			return false, nil
		}
		renewCount++
		current.expiresAt = now.Add(leaseTTL)
		state[leaseRole] = current
		return true, nil
	}

	releaseSeafHTTPUploadFinalizeLeaseFn = func(ctx context.Context, database *db.DB, leaseRole, leaseToken string) error {
		mu.Lock()
		defer mu.Unlock()
		current := state[leaseRole]
		if current.token == leaseToken {
			delete(state, leaseRole)
		}
		return nil
	}

	leaseCtxFirst, releaseFirst, err := acquireSeafHTTPDistributedUploadFinalizeLeaseWithIntervals(context.Background(), &db.DB{}, "repo-1", leaseTTL, time.Millisecond, renewInterval)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	if leaseCtxFirst == nil {
		t.Fatal("first lease context must not be nil")
	}
	releasedFirst := false
	defer func() {
		if !releasedFirst {
			releaseFirst()
		}
	}()

	time.Sleep(leaseTTL + 20*time.Millisecond)

	blockedCtx, cancelBlocked := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelBlocked()
	leaseCtxSecond, releaseSecond, err := acquireSeafHTTPDistributedUploadFinalizeLeaseWithIntervals(blockedCtx, &db.DB{}, "repo-1", leaseTTL, time.Millisecond, renewInterval)
	_ = leaseCtxSecond
	if releaseSecond != nil {
		releaseSecond()
		t.Fatal("second distributed acquire should not succeed while the first lease is still being renewed")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second acquire error = %v, want context deadline exceeded while the renewed lease is held", err)
	}
	if renewCount == 0 {
		t.Fatal("expected distributed upload finalize lease to renew before TTL expiry")
	}

	releaseFirst()
	releasedFirst = true
	leaseCtxSecond, releaseSecond, err = acquireSeafHTTPDistributedUploadFinalizeLeaseWithIntervals(context.Background(), &db.DB{}, "repo-1", leaseTTL, time.Millisecond, renewInterval)
	if err != nil {
		t.Fatalf("second acquire after release failed: %v", err)
	}
	if leaseCtxSecond == nil {
		t.Fatal("second lease context must not be nil")
	}
	releaseSecond()
}

func TestAcquireSeafHTTPDistributedUploadFinalizeLeaseCancelsContextWhenLost(t *testing.T) {
	origTryAcquire := tryAcquireSeafHTTPUploadFinalizeLeaseFn
	origRenew := renewSeafHTTPUploadFinalizeLeaseFn
	origRelease := releaseSeafHTTPUploadFinalizeLeaseFn
	defer func() {
		tryAcquireSeafHTTPUploadFinalizeLeaseFn = origTryAcquire
		renewSeafHTTPUploadFinalizeLeaseFn = origRenew
		releaseSeafHTTPUploadFinalizeLeaseFn = origRelease
	}()

	leaseTTL := 40 * time.Millisecond
	renewInterval := 10 * time.Millisecond

	type leaseState struct {
		token     string
		expiresAt time.Time
	}

	var (
		mu    sync.Mutex
		state = map[string]leaseState{}
	)

	tryAcquireSeafHTTPUploadFinalizeLeaseFn = func(ctx context.Context, database *db.DB, leaseRole, leaseToken string, ttlSeconds int) (bool, error) {
		mu.Lock()
		defer mu.Unlock()
		now := time.Now()
		current := state[leaseRole]
		if current.token == "" || !now.Before(current.expiresAt) {
			state[leaseRole] = leaseState{token: leaseToken, expiresAt: now.Add(leaseTTL)}
			return true, nil
		}
		return false, nil
	}

	renewCalls := 0
	renewSeafHTTPUploadFinalizeLeaseFn = func(ctx context.Context, database *db.DB, leaseRole, leaseToken string, ttlSeconds int) (bool, error) {
		mu.Lock()
		defer mu.Unlock()
		renewCalls++
		if renewCalls == 1 {
			state[leaseRole] = leaseState{token: "other-holder", expiresAt: time.Now().Add(leaseTTL)}
			return false, nil
		}
		current := state[leaseRole]
		if current.token != leaseToken {
			return false, nil
		}
		current.expiresAt = time.Now().Add(leaseTTL)
		state[leaseRole] = current
		return true, nil
	}

	releaseSeafHTTPUploadFinalizeLeaseFn = func(ctx context.Context, database *db.DB, leaseRole, leaseToken string) error {
		mu.Lock()
		defer mu.Unlock()
		current := state[leaseRole]
		if current.token == leaseToken {
			delete(state, leaseRole)
		}
		return nil
	}

	leaseCtx, releaseFirst, err := acquireSeafHTTPDistributedUploadFinalizeLeaseWithIntervals(context.Background(), &db.DB{}, "repo-1", leaseTTL, time.Millisecond, renewInterval)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	defer releaseFirst()

	select {
	case <-leaseCtx.Done():
		t.Fatal("lease context canceled before loss was observed")
	default:
	}

	select {
	case <-leaseCtx.Done():
	case <-time.After(200 * time.Millisecond):
		t.Fatal("lease context was not canceled after renewal reported lease loss")
	}

	time.Sleep(leaseTTL + 20*time.Millisecond)
	leaseCtxSecond, releaseSecond, err := acquireSeafHTTPDistributedUploadFinalizeLeaseWithIntervals(context.Background(), &db.DB{}, "repo-1", leaseTTL, time.Millisecond, renewInterval)
	if err != nil {
		t.Fatalf("second acquire after lost lease failed: %v", err)
	}
	if leaseCtxSecond == nil {
		t.Fatal("second lease context must not be nil")
	}
	releaseSecond()
}

func (m *mockObjectStore) Put(ctx context.Context, blockID string, data io.Reader, size int64) (string, error) {
	_, _ = io.Copy(io.Discard, data)
	return blockID, nil
}

func (m *mockObjectStore) Get(ctx context.Context, storageKey string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(m.data)), nil
}

func (m *mockObjectStore) Delete(ctx context.Context, storageKey string) error {
	return nil
}

func (m *mockObjectStore) Exists(ctx context.Context, storageKey string) (bool, error) {
	return true, nil
}

func (m *mockObjectStore) GetAccessType() storage.AccessType {
	return storage.AccessImmediate
}

func (m *mockObjectStore) InitiateRestore(ctx context.Context, storageKey string) (string, error) {
	return "", nil
}

func (m *mockObjectStore) CheckRestoreStatus(ctx context.Context, storageKey string) (bool, error) {
	return true, nil
}

func (m *mockObjectStore) GetRestoreExpiry(ctx context.Context, storageKey string) (*time.Time, error) {
	return nil, nil
}

// ============================================================================
// ChunkManager janitor Tests
// ============================================================================

// newTestChunkManager creates a ChunkManager with a controllable clock and
// isolated tempDir. It does NOT start the janitor goroutine — tests call
// sweepOnce() directly.
func newTestChunkManager(t *testing.T) (*ChunkManager, string) {
	t.Helper()
	dir := t.TempDir()
	cm := &ChunkManager{
		uploads:         make(map[string]*ChunkUpload),
		outcomes:        make(map[string]cachedFinalizeOutcome),
		tempDir:         dir,
		janitorInterval: time.Hour, // irrelevant — goroutine not started
		trackerTTL:      1 * time.Hour,
		diskTTL:         2 * time.Hour,
		outcomeTTL:      chunkFinalizeOutcomeTTL,
		outcomeLimit:    chunkFinalizeOutcomeLimit,
		now:             time.Now,
		stopCh:          make(chan struct{}),
	}
	return cm, dir
}

// TestChunkJanitor_SweepStaleTracker verifies that a tracker with no recent
// activity gets reaped and its temp file deleted.
func TestChunkJanitor_SweepStaleTracker(t *testing.T) {
	cm, dir := newTestChunkManager(t)

	// Create a real temp file in the isolated dir.
	f, err := os.CreateTemp(dir, "sesamefs_upload_tok1_file.txt")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	f.Close()

	past := time.Now().Add(-90 * time.Minute)
	upload := &ChunkUpload{
		Token:     "tok1",
		Filename:  "file.txt",
		TempFile:  nil, // already closed above; Cleanup will fail on Close but Remove still runs
		TempPath:  f.Name(),
		updatedAt: past,
	}
	upload.TempFile, _ = os.OpenFile(f.Name(), os.O_RDWR, 0600)
	cm.uploads["tok1:file.txt"] = upload

	// Advance clock past trackerTTL.
	cm.now = func() time.Time { return time.Now().Add(70 * time.Minute) }
	cm.sweepOnce()

	if _, exists := cm.uploads["tok1:file.txt"]; exists {
		t.Error("stale tracker should have been removed from map")
	}
	if _, err := os.Stat(f.Name()); !os.IsNotExist(err) {
		t.Errorf("temp file should be deleted, stat err=%v", err)
	}
}

// TestChunkJanitor_ActiveTrackerNotSwept verifies that a tracker with recent
// activity is NOT swept.
func TestChunkJanitor_ActiveTrackerNotSwept(t *testing.T) {
	cm, dir := newTestChunkManager(t)

	f, err := os.CreateTemp(dir, "sesamefs_upload_tok2_file.txt")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	f.Close()

	upload := &ChunkUpload{
		Token:     "tok2",
		Filename:  "file.txt",
		TempFile:  nil,
		TempPath:  f.Name(),
		updatedAt: time.Now(), // just now
	}
	upload.TempFile, _ = os.OpenFile(f.Name(), os.O_RDWR, 0600)
	cm.uploads["tok2:file.txt"] = upload

	// Clock advances only 5 minutes — well below the 1-hour tracker TTL.
	cm.now = func() time.Time { return time.Now().Add(5 * time.Minute) }
	cm.sweepOnce()

	if _, exists := cm.uploads["tok2:file.txt"]; !exists {
		t.Error("active tracker should NOT be removed from map")
	}
	if _, err := os.Stat(f.Name()); err != nil {
		t.Errorf("temp file should still exist, stat err=%v", err)
	}
	upload.TempFile.Close()
}

// TestChunkJanitor_DiskOrphanCleaned verifies that a prefixed temp file on
// disk whose tracker is gone and whose mtime is past diskTTL gets deleted.
func TestChunkJanitor_DiskOrphanCleaned(t *testing.T) {
	cm, dir := newTestChunkManager(t)

	// Create an "orphan" file (no in-memory tracker).
	f, err := os.CreateTemp(dir, "sesamefs_upload_stale_orphan")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	f.Close()

	// Back-date the file's mtime to 3 hours ago.
	staleTime := time.Now().Add(-3 * time.Hour)
	os.Chtimes(f.Name(), staleTime, staleTime)

	cm.sweepOnce()

	if _, err := os.Stat(f.Name()); !os.IsNotExist(err) {
		t.Errorf("orphaned disk file should be removed, stat err=%v", err)
	}
}

func TestWriteSeafHTTPUploadError_MapsSentinelErrors(t *testing.T) {
	tests := []struct {
		name               string
		err                error
		genericMsg         string
		wantStatus         int
		wantError          string
		wantLibNeedDecrypt bool
	}{
		{name: "head conflict", err: v2.ErrLibraryHeadConflict, genericMsg: "failed to finalize upload", wantStatus: http.StatusConflict, wantError: "library was modified concurrently; retry the upload"},
		{name: "wrapped head conflict (mirrors retry_exhausted production path)", err: fmt.Errorf("%w: failed to finalize upload metadata after 20 attempts", v2.ErrLibraryHeadConflict), genericMsg: "failed to finalize upload", wantStatus: http.StatusConflict, wantError: "library was modified concurrently; retry the upload"},
		{name: "block delete in progress", err: v2.ErrBlockDeleteInProgress, genericMsg: "failed to finalize upload", wantStatus: http.StatusConflict, wantError: "block is being deleted; retry the upload"},
		{name: "quota exceeded", err: errStorageQuotaExceeded, genericMsg: "failed to finalize upload", wantStatus: http.StatusForbidden, wantError: "storage quota exceeded"},
		// Encrypted library without a decrypt session must surface the app-wide
		// 403 { lib_need_decrypt: true } contract (not a generic 500) so the
		// frontend re-opens the repo password dialog. See isLibraryEncryptedError.
		{name: "encrypted not unlocked", err: v2.ErrLibraryEncryptedNotUnlocked, genericMsg: "failed to finalize upload", wantStatus: http.StatusForbidden, wantError: "Library is encrypted", wantLibNeedDecrypt: true},
		{name: "wrapped encrypted not unlocked", err: fmt.Errorf("finalize: %w", v2.ErrLibraryEncryptedNotUnlocked), genericMsg: "failed to finalize upload", wantStatus: http.StatusForbidden, wantError: "Library is encrypted", wantLibNeedDecrypt: true},
		{name: "generic", err: errors.New("boom"), genericMsg: "failed to finalize upload", wantStatus: http.StatusInternalServerError, wantError: "failed to finalize upload"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			writeSeafHTTPUploadError(c, tt.err, tt.genericMsg)

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", w.Code, tt.wantStatus)
			}

			var resp map[string]interface{}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			if got := resp["error"]; got != tt.wantError {
				t.Fatalf("error = %v, want %q", got, tt.wantError)
			}
			if tt.wantLibNeedDecrypt && resp["lib_need_decrypt"] != true {
				t.Fatalf("lib_need_decrypt = %v, want true (frontend needs this flag to prompt for the password)", resp["lib_need_decrypt"])
			}
		})
	}
}

func TestRetrySeafHTTPBlockMaterialization_RetriesFencedBlock(t *testing.T) {
	oldBackoff := seafHTTPBlockMaterializationRetryBackoffFn
	oldSleep := seafHTTPBlockMaterializationSleepFn
	t.Cleanup(func() {
		seafHTTPBlockMaterializationRetryBackoffFn = oldBackoff
		seafHTTPBlockMaterializationSleepFn = oldSleep
	})

	var slept []time.Duration
	seafHTTPBlockMaterializationRetryBackoffFn = func(attempt int) time.Duration {
		return time.Duration(attempt) * time.Millisecond
	}
	seafHTTPBlockMaterializationSleepFn = func(delay time.Duration) {
		slept = append(slept, delay)
	}

	storeCalls := 0
	materializeCalls := 0
	err := retrySeafHTTPBlockMaterialization("HandleUpload", "block-1", func() error {
		storeCalls++
		return nil
	}, func() error {
		materializeCalls++
		if materializeCalls < 3 {
			return v2.ErrBlockDeleteInProgress
		}
		return nil
	}, nil)
	if err != nil {
		t.Fatalf("retrySeafHTTPBlockMaterialization() error = %v, want nil", err)
	}
	if storeCalls != 3 {
		t.Fatalf("storeCalls = %d, want 3", storeCalls)
	}
	if materializeCalls != 3 {
		t.Fatalf("materializeCalls = %d, want 3", materializeCalls)
	}
	if !reflect.DeepEqual(slept, []time.Duration{time.Millisecond, 2 * time.Millisecond}) {
		t.Fatalf("slept = %#v, want [1ms 2ms]", slept)
	}
}

func TestRetrySeafHTTPBlockMaterialization_ClearsFenceWithoutSleeping(t *testing.T) {
	oldBackoff := seafHTTPBlockMaterializationRetryBackoffFn
	oldSleep := seafHTTPBlockMaterializationSleepFn
	t.Cleanup(func() {
		seafHTTPBlockMaterializationRetryBackoffFn = oldBackoff
		seafHTTPBlockMaterializationSleepFn = oldSleep
	})

	seafHTTPBlockMaterializationRetryBackoffFn = func(attempt int) time.Duration {
		return time.Millisecond
	}
	sleepCalls := 0
	seafHTTPBlockMaterializationSleepFn = func(delay time.Duration) {
		sleepCalls++
	}

	storeCalls := 0
	materializeCalls := 0
	resolveCalls := 0
	err := retrySeafHTTPBlockMaterialization("HandleUpload", "block-1", func() error {
		storeCalls++
		return nil
	}, func() error {
		materializeCalls++
		if materializeCalls == 1 {
			return v2.ErrBlockDeleteInProgress
		}
		return nil
	}, func() (bool, error) {
		resolveCalls++
		return true, nil
	})
	if err != nil {
		t.Fatalf("retrySeafHTTPBlockMaterialization() error = %v, want nil", err)
	}
	if storeCalls != 2 {
		t.Fatalf("storeCalls = %d, want 2", storeCalls)
	}
	if materializeCalls != 2 {
		t.Fatalf("materializeCalls = %d, want 2", materializeCalls)
	}
	if resolveCalls != 1 {
		t.Fatalf("resolveCalls = %d, want 1", resolveCalls)
	}
	if sleepCalls != 0 {
		t.Fatalf("sleepCalls = %d, want 0", sleepCalls)
	}
}

func TestRetrySeafHTTPBlockMaterialization_RetriesStoreFence(t *testing.T) {
	oldBackoff := seafHTTPBlockMaterializationRetryBackoffFn
	oldSleep := seafHTTPBlockMaterializationSleepFn
	t.Cleanup(func() {
		seafHTTPBlockMaterializationRetryBackoffFn = oldBackoff
		seafHTTPBlockMaterializationSleepFn = oldSleep
	})

	seafHTTPBlockMaterializationRetryBackoffFn = func(attempt int) time.Duration {
		return time.Duration(attempt) * time.Millisecond
	}
	var slept []time.Duration
	seafHTTPBlockMaterializationSleepFn = func(delay time.Duration) {
		slept = append(slept, delay)
	}

	storeCalls := 0
	materializeCalls := 0
	err := retrySeafHTTPBlockMaterialization("HandleUpload", "block-1", func() error {
		storeCalls++
		if storeCalls == 1 {
			return v2.ErrBlockDeleteInProgress
		}
		return nil
	}, func() error {
		materializeCalls++
		return nil
	}, nil)
	if err != nil {
		t.Fatalf("retrySeafHTTPBlockMaterialization() error = %v, want nil", err)
	}
	if storeCalls != 2 {
		t.Fatalf("storeCalls = %d, want 2", storeCalls)
	}
	if materializeCalls != 1 {
		t.Fatalf("materializeCalls = %d, want 1", materializeCalls)
	}
	if !reflect.DeepEqual(slept, []time.Duration{time.Millisecond}) {
		t.Fatalf("slept = %#v, want [1ms]", slept)
	}
}

func TestRetrySeafHTTPBlockMaterialization_StopsOnNonRetryableError(t *testing.T) {
	oldBackoff := seafHTTPBlockMaterializationRetryBackoffFn
	oldSleep := seafHTTPBlockMaterializationSleepFn
	t.Cleanup(func() {
		seafHTTPBlockMaterializationRetryBackoffFn = oldBackoff
		seafHTTPBlockMaterializationSleepFn = oldSleep
	})

	seafHTTPBlockMaterializationRetryBackoffFn = func(attempt int) time.Duration {
		return time.Millisecond
	}
	sleepCalls := 0
	seafHTTPBlockMaterializationSleepFn = func(delay time.Duration) {
		sleepCalls++
	}

	storeCalls := 0
	materializeCalls := 0
	wantErr := errors.New("boom")
	err := retrySeafHTTPBlockMaterialization("HandleUpload", "block-1", func() error {
		storeCalls++
		return nil
	}, func() error {
		materializeCalls++
		return wantErr
	}, nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("retrySeafHTTPBlockMaterialization() error = %v, want %v", err, wantErr)
	}
	if storeCalls != 1 {
		t.Fatalf("storeCalls = %d, want 1", storeCalls)
	}
	if materializeCalls != 1 {
		t.Fatalf("materializeCalls = %d, want 1", materializeCalls)
	}
	if sleepCalls != 0 {
		t.Fatalf("sleepCalls = %d, want 0", sleepCalls)
	}
}

func TestStageSeafHTTPPublishAttemptReferences_UsesResolvedInternalBlockIDs(t *testing.T) {
	oldResolve := resolveSeafHTTPStoredBlockIDsFn
	oldStage := stageSeafHTTPPublishAttemptReferencesFn
	t.Cleanup(func() {
		resolveSeafHTTPStoredBlockIDsFn = oldResolve
		stageSeafHTTPPublishAttemptReferencesFn = oldStage
	})

	resolveCalls := 0
	resolveSeafHTTPStoredBlockIDsFn = func(fsHelper *v2.FSHelper, orgID string, blockIDs []string) ([]string, error) {
		resolveCalls++
		if orgID != "org-1" {
			t.Fatalf("resolve orgID = %q, want org-1", orgID)
		}
		if !reflect.DeepEqual(blockIDs, []string{"sha1-a", "sha1-b"}) {
			t.Fatalf("resolve blockIDs = %#v, want external ids", blockIDs)
		}
		return []string{"sha256-a", "sha256-a", "sha256-b"}, nil
	}
	stageSeafHTTPPublishAttemptReferencesFn = func(database *db.DB, orgID, repoID, attemptID string, blockIDs []string, resolve db.BlockIDResolver) ([]string, error) {
		if resolve != nil {
			t.Fatal("stage helper should pass resolved internal IDs directly")
		}
		if orgID != "org-1" || repoID != "repo-1" || attemptID != "commit-1" {
			t.Fatalf("stage args = %s/%s/%s, want org-1/repo-1/commit-1", orgID, repoID, attemptID)
		}
		want := []string{"sha256-a", "sha256-b"}
		if !reflect.DeepEqual(blockIDs, want) {
			t.Fatalf("stage blockIDs = %#v, want %#v", blockIDs, want)
		}
		return append([]string(nil), blockIDs...), nil
	}

	staged, err := stageSeafHTTPPublishAttemptReferences(&v2.FSHelper{}, nil, "org-1", "repo-1", "commit-1", []string{"sha1-a", "sha1-b"})
	if err != nil {
		t.Fatalf("stageSeafHTTPPublishAttemptReferences() error = %v", err)
	}
	if resolveCalls != 1 {
		t.Fatalf("resolveCalls = %d, want 1", resolveCalls)
	}
	if !reflect.DeepEqual(staged, []string{"sha256-a", "sha256-b"}) {
		t.Fatalf("staged = %#v, want internal IDs", staged)
	}
}

func TestFinalizeSeafHTTPPublishedBlockReferences_UsesStagedInternalIDsForPromotionAndRepair(t *testing.T) {
	oldPromote := promoteSeafHTTPPublishAttemptReferencesFn
	oldSchedule := schedulePublishedFSObjectBlockReferenceRepairFn
	oldClear := clearPublishedFSObjectBlockReferenceRepairFn
	t.Cleanup(func() {
		promoteSeafHTTPPublishAttemptReferencesFn = oldPromote
		schedulePublishedFSObjectBlockReferenceRepairFn = oldSchedule
		clearPublishedFSObjectBlockReferenceRepairFn = oldClear
	})

	t.Run("success clears durable repair after internal promotion", func(t *testing.T) {
		promoteCalls := 0
		clearCalls := 0
		scheduleCalls := 0
		promoteSeafHTTPPublishAttemptReferencesFn = func(database *db.DB, orgID, attemptID string, blockIDs []string, registerPermanent func() error) error {
			promoteCalls++
			if orgID != "org-1" || attemptID != "commit-1" {
				t.Fatalf("promote args = %s/%s, want org-1/commit-1", orgID, attemptID)
			}
			if !reflect.DeepEqual(blockIDs, []string{"sha256-a"}) {
				t.Fatalf("promote blockIDs = %#v, want staged internal IDs", blockIDs)
			}
			return nil
		}
		clearPublishedFSObjectBlockReferenceRepairFn = func(database *db.DB, orgID, repoID, commitID, fsID string) error {
			clearCalls++
			if orgID != "org-1" || repoID != "repo-1" || commitID != "commit-1" || fsID != "fs-1" {
				t.Fatalf("clear args = %s/%s/%s/%s", orgID, repoID, commitID, fsID)
			}
			return nil
		}
		schedulePublishedFSObjectBlockReferenceRepairFn = func(database *db.DB, orgID, repoID, commitID, fsID, label string, stagedBlockIDs []string) {
			scheduleCalls++
		}

		finalizeSeafHTTPPublishedBlockReferences(&v2.FSHelper{}, nil, "org-1", "repo-1", "commit-1", "fs-1", "commitUploadedFile", []string{"sha1-a"}, []string{"sha256-a"})

		if promoteCalls != 1 {
			t.Fatalf("promoteCalls = %d, want 1", promoteCalls)
		}
		if clearCalls != 1 {
			t.Fatalf("clearCalls = %d, want 1", clearCalls)
		}
		if scheduleCalls != 0 {
			t.Fatalf("scheduleCalls = %d, want 0", scheduleCalls)
		}
	})

	t.Run("failure schedules durable repair with internal staged IDs", func(t *testing.T) {
		promoteSeafHTTPPublishAttemptReferencesFn = func(database *db.DB, orgID, attemptID string, blockIDs []string, registerPermanent func() error) error {
			if !reflect.DeepEqual(blockIDs, []string{"sha256-a", "sha256-b"}) {
				t.Fatalf("promote blockIDs = %#v, want staged internal IDs", blockIDs)
			}
			return errors.New("boom")
		}
		clearCalls := 0
		clearPublishedFSObjectBlockReferenceRepairFn = func(database *db.DB, orgID, repoID, commitID, fsID string) error {
			clearCalls++
			return nil
		}
		scheduleCalls := 0
		schedulePublishedFSObjectBlockReferenceRepairFn = func(database *db.DB, orgID, repoID, commitID, fsID, label string, stagedBlockIDs []string) {
			scheduleCalls++
			if orgID != "org-1" || repoID != "repo-1" || commitID != "commit-1" || fsID != "fs-1" || label != "commitUploadedFileMultiBlock" {
				t.Fatalf("schedule args = %s/%s/%s/%s/%s", orgID, repoID, commitID, fsID, label)
			}
			if !reflect.DeepEqual(stagedBlockIDs, []string{"sha256-a", "sha256-b"}) {
				t.Fatalf("scheduled stagedBlockIDs = %#v, want internal IDs", stagedBlockIDs)
			}
		}

		finalizeSeafHTTPPublishedBlockReferences(&v2.FSHelper{}, nil, "org-1", "repo-1", "commit-1", "fs-1", "commitUploadedFileMultiBlock", []string{"sha1-a", "sha1-b"}, []string{"sha256-a", "sha256-b"})

		if scheduleCalls != 1 {
			t.Fatalf("scheduleCalls = %d, want 1", scheduleCalls)
		}
		if clearCalls != 0 {
			t.Fatalf("clearCalls = %d, want 0", clearCalls)
		}
	})
}

func TestCleanupSeafHTTPFailedPublishAttempt_JoinsArtifactCleanupAndQueueClear(t *testing.T) {
	oldCleanup := cleanupSeafHTTPFailedPublishAttemptFn
	oldClear := clearPublishedFSObjectBlockReferenceRepairFn
	oldRelease := releaseSeafHTTPPendingFSObjectOwnerFn
	t.Cleanup(func() {
		cleanupSeafHTTPFailedPublishAttemptFn = oldCleanup
		clearPublishedFSObjectBlockReferenceRepairFn = oldClear
		releaseSeafHTTPPendingFSObjectOwnerFn = oldRelease
	})

	cleanupErr := errors.New("cleanup failed")
	clearErr := errors.New("clear failed")
	cleanupCalls := 0
	cleanupSeafHTTPFailedPublishAttemptFn = func(database *db.DB, orgID, repoID, attemptID, commitID string, fsIDs, blockIDs []string) error {
		cleanupCalls++
		if orgID != "org-1" || repoID != "repo-1" || attemptID != "commit-1" || commitID != "commit-1" {
			t.Fatalf("cleanup args = %s/%s/%s/%s, want org-1/repo-1/commit-1/commit-1", orgID, repoID, attemptID, commitID)
		}
		if !reflect.DeepEqual(fsIDs, []string{"fs-1"}) {
			t.Fatalf("cleanup fsIDs = %#v, want []string{\"fs-1\"}", fsIDs)
		}
		if !reflect.DeepEqual(blockIDs, []string{"sha256-a"}) {
			t.Fatalf("cleanup blockIDs = %#v, want []string{\"sha256-a\"}", blockIDs)
		}
		return cleanupErr
	}
	releaseCalls := 0
	releaseSeafHTTPPendingFSObjectOwnerFn = func(database *db.DB, repoID, fsID, ownerID string, createdAt time.Time) error {
		releaseCalls++
		return nil
	}
	clearCalls := 0
	clearPublishedFSObjectBlockReferenceRepairFn = func(database *db.DB, orgID, repoID, commitID, fsID string) error {
		clearCalls++
		if orgID != "org-1" || repoID != "repo-1" || commitID != "commit-1" || fsID != "fs-1" {
			t.Fatalf("clear args = %s/%s/%s/%s, want org-1/repo-1/commit-1/fs-1", orgID, repoID, commitID, fsID)
		}
		return clearErr
	}

	err := cleanupSeafHTTPFailedPublishAttempt(nil, "org-1", "repo-1", "commit-1", "fs-1", []string{"sha256-a"})
	if !errors.Is(err, cleanupErr) {
		t.Fatalf("cleanupSeafHTTPFailedPublishAttempt() error = %v, want cleanupErr %v", err, cleanupErr)
	}
	if errors.Is(err, clearErr) {
		t.Fatalf("cleanupSeafHTTPFailedPublishAttempt() error = %v, do not want clearErr %v", err, clearErr)
	}
	if cleanupCalls != 1 {
		t.Fatalf("cleanupCalls = %d, want 1", cleanupCalls)
	}
	if releaseCalls != 0 {
		t.Fatalf("releaseCalls = %d, want 0", releaseCalls)
	}
	if clearCalls != 0 {
		t.Fatalf("clearCalls = %d, want 0", clearCalls)
	}
}

func TestHandleChunkedFinalizeError_RollsBackConflictAndCleansUp(t *testing.T) {
	oldRollback := rollbackUploadedBlockRefsFn
	oldCleanup := cleanupChunkUploadFn
	defer func() {
		rollbackUploadedBlockRefsFn = oldRollback
		cleanupChunkUploadFn = oldCleanup
	}()

	var rollbackCalled bool
	var gotOrgID, gotRepoID string
	var gotBlockIDs []string
	var cleanedUpload *ChunkUpload
	var gotOperationID string
	rollbackUploadedBlockRefsFn = func(database *db.DB, orgID, repoID, operationID string, blockIDs []string) {
		rollbackCalled = true
		gotOrgID = orgID
		gotRepoID = repoID
		gotOperationID = operationID
		gotBlockIDs = append([]string(nil), blockIDs...)
	}
	cleanupChunkUploadFn = func(upload *ChunkUpload) {
		cleanedUpload = upload
	}

	h := &SeafHTTPHandler{}
	token := &AccessToken{OrgID: "org-1", RepoID: "repo-1", Token: "upload-token"}
	upload := &ChunkUpload{
		Token:       "upload-token",
		Filename:    "file.bin",
		Finalizing:  true,
		OperationID: "chunk-op-conflict",
		accountedBlockPosition: map[int]string{
			2: "block-a",
			0: "block-a",
			1: "block-b",
		},
	}

	h.handleChunkedFinalizeError(token, "upload-token", "file.bin", upload, fmt.Errorf("%w: failed to finalize upload metadata after 20 attempts", v2.ErrLibraryHeadConflict))

	if !rollbackCalled {
		t.Fatal("expected conflict finalize error to roll back accounted blocks")
	}
	if gotOrgID != "org-1" || gotRepoID != "repo-1" {
		t.Fatalf("rollback org/repo = %s/%s, want org-1/repo-1", gotOrgID, gotRepoID)
	}
	if gotOperationID != "chunk-op-conflict" {
		t.Fatalf("rollback operation ID = %s, want chunk-op-conflict", gotOperationID)
	}
	if !reflect.DeepEqual(gotBlockIDs, []string{"block-a", "block-b", "block-a"}) {
		t.Fatalf("rollback block IDs = %#v, want %#v", gotBlockIDs, []string{"block-a", "block-b", "block-a"})
	}
	if cleanedUpload != upload {
		t.Fatal("cleanup should target the tracked upload instance")
	}
	if !upload.Finalizing {
		t.Fatal("conflict cleanup should not reset tracker state before cleanup")
	}
}

func TestHandleChunkedFinalizeError_RollsBackQuotaExceededAndCleansUp(t *testing.T) {
	oldRollback := rollbackUploadedBlockRefsFn
	oldCleanup := cleanupChunkUploadFn
	defer func() {
		rollbackUploadedBlockRefsFn = oldRollback
		cleanupChunkUploadFn = oldCleanup
	}()

	var rollbackCalled bool
	var gotOrgID, gotRepoID string
	var gotBlockIDs []string
	var cleanupCalled bool
	var gotOperationID string
	rollbackUploadedBlockRefsFn = func(database *db.DB, orgID, repoID, operationID string, blockIDs []string) {
		rollbackCalled = true
		gotOrgID = orgID
		gotRepoID = repoID
		gotOperationID = operationID
		gotBlockIDs = append([]string(nil), blockIDs...)
	}
	cleanupChunkUploadFn = func(upload *ChunkUpload) {
		cleanupCalled = true
	}

	h := &SeafHTTPHandler{}
	token := &AccessToken{OrgID: "org-2", RepoID: "repo-2"}
	upload := &ChunkUpload{
		Finalizing:  true,
		OperationID: "chunk-op-quota",
		accountedBlockPosition: map[int]string{
			0: "block-x",
			1: "block-y",
		},
	}

	h.handleChunkedFinalizeError(token, "upload-token", "file.bin", upload, fmt.Errorf("inner finalize failure: %w", errStorageQuotaExceeded))

	if !rollbackCalled {
		t.Fatal("expected quota_exceeded finalize error to roll back accounted blocks")
	}
	if !cleanupCalled {
		t.Fatal("expected quota_exceeded finalize error to clean up the tracker")
	}
	if gotOrgID != "org-2" || gotRepoID != "repo-2" {
		t.Fatalf("rollback org/repo = %s/%s, want org-2/repo-2", gotOrgID, gotRepoID)
	}
	if gotOperationID != "chunk-op-quota" {
		t.Fatalf("rollback operation ID = %s, want chunk-op-quota", gotOperationID)
	}
	if !reflect.DeepEqual(gotBlockIDs, []string{"block-x", "block-y"}) {
		t.Fatalf("rollback block IDs = %#v, want %#v", gotBlockIDs, []string{"block-x", "block-y"})
	}
}

func TestHandleChunkedFinalizeError_ResetsNonConflictState(t *testing.T) {
	oldRollback := rollbackUploadedBlockRefsFn
	oldCleanup := cleanupChunkUploadFn
	defer func() {
		rollbackUploadedBlockRefsFn = oldRollback
		cleanupChunkUploadFn = oldCleanup
	}()

	rollbackCalled := false
	cleanupCalled := false
	rollbackUploadedBlockRefsFn = func(database *db.DB, orgID, repoID, operationID string, blockIDs []string) {
		rollbackCalled = true
	}
	cleanupChunkUploadFn = func(upload *ChunkUpload) {
		cleanupCalled = true
	}

	h := &SeafHTTPHandler{}
	token := &AccessToken{OrgID: "org-1", RepoID: "repo-1", Token: "upload-token"}
	upload := &ChunkUpload{Finalizing: true}

	h.handleChunkedFinalizeError(token, "upload-token", "file.bin", upload, errors.New("boom"))

	if rollbackCalled {
		t.Fatal("non-conflict finalize error should not roll back accounted blocks here")
	}
	if cleanupCalled {
		t.Fatal("non-conflict finalize error should keep the tracker for retry")
	}
	if upload.Finalizing {
		t.Fatal("non-conflict finalize error should reset finalization state")
	}
}

func TestHandleChunkedFinalizeError_CleansUpUnknownBlockMutationOutcome(t *testing.T) {
	oldRollback := rollbackUploadedBlockRefsFn
	oldCleanup := cleanupChunkUploadFn
	defer func() {
		rollbackUploadedBlockRefsFn = oldRollback
		cleanupChunkUploadFn = oldCleanup
	}()

	rollbackCalled := false
	var gotBlockIDs []string
	cleanupCalled := false
	var gotOperationID string
	rollbackUploadedBlockRefsFn = func(database *db.DB, orgID, repoID, operationID string, blockIDs []string) {
		rollbackCalled = true
		gotOperationID = operationID
		gotBlockIDs = append([]string(nil), blockIDs...)
	}
	cleanupChunkUploadFn = func(upload *ChunkUpload) {
		cleanupCalled = true
	}

	h := &SeafHTTPHandler{}
	token := &AccessToken{OrgID: "org-1", RepoID: "repo-1"}
	upload := &ChunkUpload{Finalizing: true, OperationID: "chunk-op-unknown", accountedBlockPosition: map[int]string{0: "block-a", 1: "block-b"}}

	h.handleChunkedFinalizeError(token, "upload-token", "file.bin", upload, fmt.Errorf("block mutation ambiguous: %w", v2.ErrBlockMutationOutcomeUnknown))

	if !rollbackCalled {
		t.Fatal("unknown block mutation outcome should roll back previously accounted blocks")
	}
	if !reflect.DeepEqual(gotBlockIDs, []string{"block-a", "block-b"}) {
		t.Fatalf("rollback block IDs = %#v, want %#v", gotBlockIDs, []string{"block-a", "block-b"})
	}
	if gotOperationID != "chunk-op-unknown" {
		t.Fatalf("rollback operation ID = %s, want chunk-op-unknown", gotOperationID)
	}
	if !cleanupCalled {
		t.Fatal("unknown block mutation outcome should clean up the tracker")
	}
	if !upload.Finalizing {
		t.Fatal("unknown block mutation outcome should not reset tracker state before cleanup")
	}
}

func TestHandleSingleShotMetadataError_RollsBackOnError(t *testing.T) {
	oldRollback := rollbackUploadedBlockRefsFn
	defer func() {
		rollbackUploadedBlockRefsFn = oldRollback
	}()

	var rollbackCalled bool
	var gotOrgID, gotRepoID string
	var gotBlockIDs []string
	var gotOperationID string
	rollbackUploadedBlockRefsFn = func(database *db.DB, orgID, repoID, operationID string, blockIDs []string) {
		rollbackCalled = true
		gotOrgID = orgID
		gotRepoID = repoID
		gotOperationID = operationID
		gotBlockIDs = append([]string(nil), blockIDs...)
	}

	h := &SeafHTTPHandler{}
	token := &AccessToken{OrgID: "org-1", RepoID: "repo-1", Token: "upload-token"}

	h.handleSingleShotMetadataError(token, "single-op", "block-internal", errors.New("boom"))

	if !rollbackCalled {
		t.Fatal("expected single-shot metadata error to roll back the promoted block")
	}
	if gotOrgID != "org-1" || gotRepoID != "repo-1" {
		t.Fatalf("rollback org/repo = %s/%s, want org-1/repo-1", gotOrgID, gotRepoID)
	}
	if gotOperationID != "single-op" {
		t.Fatalf("rollback operation ID = %s, want single-op", gotOperationID)
	}
	if !reflect.DeepEqual(gotBlockIDs, []string{"block-internal"}) {
		t.Fatalf("rollback block IDs = %#v, want %#v", gotBlockIDs, []string{"block-internal"})
	}
}

func TestHandleSingleShotMetadataError_SkipsSuccessAndEmptyBlockID(t *testing.T) {
	oldRollback := rollbackUploadedBlockRefsFn
	defer func() {
		rollbackUploadedBlockRefsFn = oldRollback
	}()

	rollbackCalled := false
	rollbackUploadedBlockRefsFn = func(database *db.DB, orgID, repoID, operationID string, blockIDs []string) {
		rollbackCalled = true
	}

	h := &SeafHTTPHandler{}
	token := &AccessToken{OrgID: "org-1", RepoID: "repo-1", Token: "upload-token"}

	h.handleSingleShotMetadataError(token, "single-op", "block-internal", nil)
	if rollbackCalled {
		t.Fatal("successful finalize should not roll back the promoted block")
	}

	h.handleSingleShotMetadataError(token, "single-op", "   ", errors.New("boom"))
	if rollbackCalled {
		t.Fatal("missing internal block ID should not roll back anything")
	}
}

func TestHandleSingleShotMetadataError_SkipsUnknownPublicationOutcome(t *testing.T) {
	oldRollback := rollbackUploadedBlockRefsFn
	defer func() {
		rollbackUploadedBlockRefsFn = oldRollback
	}()

	rollbackCalled := false
	rollbackUploadedBlockRefsFn = func(database *db.DB, orgID, repoID, operationID string, blockIDs []string) {
		rollbackCalled = true
	}

	h := &SeafHTTPHandler{}
	token := &AccessToken{OrgID: "org-1", RepoID: "repo-1", Token: "upload-token"}

	h.handleSingleShotMetadataError(token, "single-op", "block-internal", v2.ErrLibraryHeadPublicationUnknown)
	if rollbackCalled {
		t.Fatal("unknown publication outcome should not roll back the promoted block")
	}
}

// TestChunkJanitor_DiskFileNotYetStale verifies that a newer orphan file is
// left alone (still within diskTTL).
func TestChunkJanitor_DiskFileNotYetStale(t *testing.T) {
	cm, dir := newTestChunkManager(t)

	f, err := os.CreateTemp(dir, "sesamefs_upload_fresh_orphan")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	f.Close()
	// mtime is "now" by default — well within diskTTL.

	cm.sweepOnce()

	if _, err := os.Stat(f.Name()); err != nil {
		t.Errorf("fresh file should survive sweep, stat err=%v", err)
	}
}

// TestChunkJanitor_NonPrefixedFileIgnored verifies that files without the
// sesamefs_upload_ prefix are never touched.
func TestChunkJanitor_NonPrefixedFileIgnored(t *testing.T) {
	cm, dir := newTestChunkManager(t)

	f, err := os.CreateTemp(dir, "unrelated_file")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	f.Close()
	// Back-date it so it would be cleaned if it had the right prefix.
	old := time.Now().Add(-24 * time.Hour)
	os.Chtimes(f.Name(), old, old)

	cm.sweepOnce()

	if _, err := os.Stat(f.Name()); err != nil {
		t.Errorf("non-prefixed file should be ignored, stat err=%v", err)
	}
}

func TestChunkUpload_TouchRefreshesActivity(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "sesamefs_upload_touch")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	defer f.Close()

	past := time.Now().Add(-2 * time.Hour)
	upload := &ChunkUpload{TempFile: f, TempPath: f.Name(), updatedAt: past}
	upload.Touch()

	if !upload.updatedAt.After(past) {
		t.Fatal("Touch should refresh updatedAt")
	}
}

func TestNewTokenManager(t *testing.T) {
	tests := []struct {
		name        string
		ttl         time.Duration
		expectedTTL time.Duration
	}{
		{
			name:        "custom TTL",
			ttl:         30 * time.Minute,
			expectedTTL: 30 * time.Minute,
		},
		{
			name:        "zero TTL uses default",
			ttl:         0,
			expectedTTL: DefaultTokenTTL,
		},
		{
			name:        "negative TTL uses default",
			ttl:         -1 * time.Hour,
			expectedTTL: DefaultTokenTTL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tm := NewTokenManager(tt.ttl)
			if tm == nil {
				t.Fatal("NewTokenManager returned nil")
			}
			if tm.tokenTTL != tt.expectedTTL {
				t.Errorf("tokenTTL = %v, want %v", tm.tokenTTL, tt.expectedTTL)
			}
			if tm.tokens == nil {
				t.Error("tokens map should be initialized")
			}
		})
	}
}

func TestTokenManagerCreateToken(t *testing.T) {
	tm := NewTokenManager(1 * time.Hour)

	token, err := tm.CreateToken(TokenTypeUpload, "org1", "repo1", "/path", "user1", "", 1*time.Hour)
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	if token == nil {
		t.Fatal("token should not be nil")
	}
	if token.Token == "" {
		t.Error("token string should not be empty")
	}
	if len(token.Token) != 32 { // 16 bytes = 32 hex chars
		t.Errorf("token length = %d, want 32", len(token.Token))
	}
	if token.Type != TokenTypeUpload {
		t.Errorf("Type = %s, want %s", token.Type, TokenTypeUpload)
	}
	if token.OrgID != "org1" {
		t.Errorf("OrgID = %s, want org1", token.OrgID)
	}
	if token.RepoID != "repo1" {
		t.Errorf("RepoID = %s, want repo1", token.RepoID)
	}
	if token.Path != "/path" {
		t.Errorf("Path = %s, want /path", token.Path)
	}
	if token.UserID != "user1" {
		t.Errorf("UserID = %s, want user1", token.UserID)
	}
	if token.ExpiresAt.Before(time.Now()) {
		t.Error("ExpiresAt should be in the future")
	}
}

func TestTokenManagerCreateUploadToken(t *testing.T) {
	tm := NewTokenManager(1 * time.Hour)

	tokenStr, err := tm.CreateUploadToken("org1", "repo1", "/upload/path", "user1")
	if err != nil {
		t.Fatalf("CreateUploadToken failed: %v", err)
	}

	if tokenStr == "" {
		t.Error("token string should not be empty")
	}

	// Verify we can retrieve it
	token, ok := tm.GetToken(tokenStr, TokenTypeUpload)
	if !ok {
		t.Error("token should be retrievable")
	}
	if token.Type != TokenTypeUpload {
		t.Errorf("Type = %s, want %s", token.Type, TokenTypeUpload)
	}
	if token.Replace {
		t.Error("CreateUploadToken should default Replace to false")
	}
}

func TestTokenManagerCreateUpdateToken(t *testing.T) {
	tm := NewTokenManager(1 * time.Hour)

	tokenStr, err := tm.CreateUpdateToken("org1", "repo1", "/upload/path", "user1")
	if err != nil {
		t.Fatalf("CreateUpdateToken failed: %v", err)
	}

	token, ok := tm.GetToken(tokenStr, TokenTypeUpload)
	if !ok {
		t.Fatal("update token should be retrievable as an upload token")
	}
	if !token.Replace {
		t.Error("CreateUpdateToken should default Replace to true")
	}
}

func TestTokenManagerCreateDownloadToken(t *testing.T) {
	tm := NewTokenManager(1 * time.Hour)

	tokenStr, err := tm.CreateDownloadToken("org1", "repo1", "/file.txt", "user1")
	if err != nil {
		t.Fatalf("CreateDownloadToken failed: %v", err)
	}

	if tokenStr == "" {
		t.Error("token string should not be empty")
	}

	// Verify we can retrieve it
	token, ok := tm.GetToken(tokenStr, TokenTypeDownload)
	if !ok {
		t.Error("token should be retrievable")
	}
	if token.Type != TokenTypeDownload {
		t.Errorf("Type = %s, want %s", token.Type, TokenTypeDownload)
	}
}

func TestTokenManagerGetToken(t *testing.T) {
	tm := NewTokenManager(1 * time.Hour)

	// Create tokens
	uploadToken, _ := tm.CreateUploadToken("org1", "repo1", "/", "user1")
	downloadToken, _ := tm.CreateDownloadToken("org1", "repo1", "/file.txt", "user1")

	tests := []struct {
		name         string
		tokenStr     string
		expectedType TokenType
		wantOK       bool
	}{
		{
			name:         "valid upload token",
			tokenStr:     uploadToken,
			expectedType: TokenTypeUpload,
			wantOK:       true,
		},
		{
			name:         "valid download token",
			tokenStr:     downloadToken,
			expectedType: TokenTypeDownload,
			wantOK:       true,
		},
		{
			name:         "upload token with wrong type",
			tokenStr:     uploadToken,
			expectedType: TokenTypeDownload,
			wantOK:       false,
		},
		{
			name:         "download token with wrong type",
			tokenStr:     downloadToken,
			expectedType: TokenTypeUpload,
			wantOK:       false,
		},
		{
			name:         "non-existent token",
			tokenStr:     "nonexistent",
			expectedType: TokenTypeUpload,
			wantOK:       false,
		},
		{
			name:         "empty token",
			tokenStr:     "",
			expectedType: TokenTypeUpload,
			wantOK:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token, ok := tm.GetToken(tt.tokenStr, tt.expectedType)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if tt.wantOK && token == nil {
				t.Error("token should not be nil when ok is true")
			}
		})
	}
}

func TestResolveLibraryBlockStoreUsesDefaultStorageClass(t *testing.T) {
	manager := storage.NewManager()
	manager.SetDefaultClass("hot-minio-local")
	manager.RegisterBackend("hot-minio-local", &storage.S3Store{}, "")

	h := &SeafHTTPHandler{storageManager: manager}

	blockStore, storageClass, err := h.resolveLibraryBlockStore("", "org-id", "repo-id")
	if err != nil {
		t.Fatalf("resolveLibraryBlockStore returned error: %v", err)
	}
	if blockStore == nil {
		t.Fatal("resolveLibraryBlockStore returned nil block store")
	}
	if storageClass != "hot-minio-local" {
		t.Fatalf("resolveLibraryBlockStore storage class = %q, want %q", storageClass, "hot-minio-local")
	}
}

func TestZipTraversalBudgetRejectsDepthLimit(t *testing.T) {
	budget := &zipTraversalBudget{maxEntries: 10, maxDepth: 1, maxBytes: 1024}
	err := budget.noteDirectory(2)
	if err == nil {
		t.Fatal("expected depth limit error")
	}
	if !isZipLimitError(err) {
		t.Fatalf("expected zip limit error, got %v", err)
	}
}

func TestZipTraversalBudgetRejectsFileCountLimit(t *testing.T) {
	budget := &zipTraversalBudget{maxEntries: 1, maxDepth: 10, maxBytes: 1024}
	if err := budget.noteFile(100); err != nil {
		t.Fatalf("first noteFile() error = %v", err)
	}
	err := budget.noteFile(100)
	if err == nil {
		t.Fatal("expected file count limit error")
	}
	if !isZipLimitError(err) {
		t.Fatalf("expected zip limit error, got %v", err)
	}
}

func TestZipTraversalBudgetRejectsByteLimit(t *testing.T) {
	budget := &zipTraversalBudget{maxEntries: 10, maxDepth: 10, maxBytes: 100}
	if err := budget.noteFile(60); err != nil {
		t.Fatalf("first noteFile() error = %v", err)
	}
	err := budget.noteFile(50)
	if err == nil {
		t.Fatal("expected byte limit error")
	}
	if !isZipLimitError(err) {
		t.Fatalf("expected zip limit error, got %v", err)
	}
}

func TestResolveLibraryBlockStoreUsesHostnameFallback(t *testing.T) {
	manager := storage.NewManager()
	manager.SetDefaultClass("hot-minio-local")
	manager.SetEndpointRegions(map[string]string{"eu.example.com": "eu"})
	manager.SetRegionClasses(map[string]storage.RegionClassConfig{
		"eu": {Hot: "hot-minio-eu"},
	})
	manager.RegisterBackend("hot-minio-local", &storage.S3Store{}, "")
	manager.RegisterBackend("hot-minio-eu", &storage.S3Store{}, "")

	h := &SeafHTTPHandler{
		storageManager: manager,
	}

	blockStore, storageClass, err := h.resolveLibraryBlockStore("eu.example.com", "org-id", "repo-id")
	if err != nil {
		t.Fatalf("resolveLibraryBlockStore returned error: %v", err)
	}
	if blockStore == nil {
		t.Fatal("resolveLibraryBlockStore returned nil block store")
	}
	if storageClass != "hot-minio-eu" {
		t.Fatalf("resolveLibraryBlockStore storage class = %q, want %q", storageClass, "hot-minio-eu")
	}
}

func TestTokenManagerGetTokenExpired(t *testing.T) {
	tm := NewTokenManager(1 * time.Hour)

	// Create a token with very short TTL
	token, _ := tm.CreateToken(TokenTypeUpload, "org1", "repo1", "/", "user1", "", 1*time.Millisecond)

	// Wait for it to expire
	time.Sleep(10 * time.Millisecond)

	// Should not be retrievable
	_, ok := tm.GetToken(token.Token, TokenTypeUpload)
	if ok {
		t.Error("expired token should not be retrievable")
	}
}

func TestTokenManagerDeleteToken(t *testing.T) {
	tm := NewTokenManager(1 * time.Hour)

	tokenStr, _ := tm.CreateUploadToken("org1", "repo1", "/", "user1")

	// Verify token exists
	_, ok := tm.GetToken(tokenStr, TokenTypeUpload)
	if !ok {
		t.Fatal("token should exist before deletion")
	}

	// Delete token
	err := tm.DeleteToken(tokenStr)
	if err != nil {
		t.Fatalf("DeleteToken failed: %v", err)
	}

	// Verify token is gone
	_, ok = tm.GetToken(tokenStr, TokenTypeUpload)
	if ok {
		t.Error("token should not exist after deletion")
	}
}

func TestTokenManagerDeleteNonExistent(t *testing.T) {
	tm := NewTokenManager(1 * time.Hour)

	// Deleting non-existent token should not error
	err := tm.DeleteToken("nonexistent")
	if err != nil {
		t.Errorf("DeleteToken should not error for non-existent token: %v", err)
	}
}

func TestTokenManagerTokenUniqueness(t *testing.T) {
	tm := NewTokenManager(1 * time.Hour)

	tokens := make(map[string]bool)
	for i := 0; i < 100; i++ {
		tokenStr, err := tm.CreateUploadToken("org", "repo", "/", "user")
		if err != nil {
			t.Fatalf("CreateUploadToken failed: %v", err)
		}
		if tokens[tokenStr] {
			t.Errorf("duplicate token generated: %s", tokenStr)
		}
		tokens[tokenStr] = true
	}
}

func TestTokenManagerImplementsInterface(t *testing.T) {
	// Compile-time check that TokenManager implements TokenStore
	var _ TokenStore = (*TokenManager)(nil)
}

func TestTokenTypeConstants(t *testing.T) {
	if TokenTypeUpload != "upload" {
		t.Errorf("TokenTypeUpload = %s, want upload", TokenTypeUpload)
	}
	if TokenTypeDownload != "download" {
		t.Errorf("TokenTypeDownload = %s, want download", TokenTypeDownload)
	}
}

func TestDefaultTokenTTL(t *testing.T) {
	if DefaultTokenTTL != 1*time.Hour {
		t.Errorf("DefaultTokenTTL = %v, want 1h", DefaultTokenTTL)
	}
}

// ============================================================================
// AccessToken struct tests
// ============================================================================

func TestAccessTokenFields(t *testing.T) {
	now := time.Now()
	token := AccessToken{
		Token:     "abc123",
		Type:      TokenTypeUpload,
		OrgID:     "org-1",
		RepoID:    "repo-1",
		Path:      "/documents/file.txt",
		UserID:    "user-1",
		ExpiresAt: now.Add(1 * time.Hour),
		CreatedAt: now,
	}

	if token.Token != "abc123" {
		t.Errorf("Token = %s, want abc123", token.Token)
	}
	if token.Type != TokenTypeUpload {
		t.Errorf("Type = %s, want upload", token.Type)
	}
	if token.Path != "/documents/file.txt" {
		t.Errorf("Path = %s, want /documents/file.txt", token.Path)
	}
}

// ============================================================================
// SeafHTTPHandler tests
// ============================================================================

// MockTokenStore implements TokenStore for testing
type MockTokenStore struct {
	tokens map[string]*AccessToken
}

func NewMockTokenStore() *MockTokenStore {
	return &MockTokenStore{
		tokens: make(map[string]*AccessToken),
	}
}

func (m *MockTokenStore) CreateUploadToken(orgID, repoID, path, userID string) (string, error) {
	token := &AccessToken{
		Token:     "mock-upload-token",
		Type:      TokenTypeUpload,
		OrgID:     orgID,
		RepoID:    repoID,
		Path:      path,
		Replace:   false,
		UserID:    userID,
		ExpiresAt: time.Now().Add(1 * time.Hour),
		CreatedAt: time.Now(),
	}
	m.tokens[token.Token] = token
	return token.Token, nil
}

func (m *MockTokenStore) CreateUpdateToken(orgID, repoID, path, userID string) (string, error) {
	token := &AccessToken{
		Token:     "mock-update-token",
		Type:      TokenTypeUpload,
		OrgID:     orgID,
		RepoID:    repoID,
		Path:      path,
		Replace:   true,
		UserID:    userID,
		ExpiresAt: time.Now().Add(1 * time.Hour),
		CreatedAt: time.Now(),
	}
	m.tokens[token.Token] = token
	return token.Token, nil
}

func (m *MockTokenStore) CreateDownloadToken(orgID, repoID, path, userID string) (string, error) {
	token := &AccessToken{
		Token:     "mock-download-token",
		Type:      TokenTypeDownload,
		OrgID:     orgID,
		RepoID:    repoID,
		Path:      path,
		UserID:    userID,
		ExpiresAt: time.Now().Add(1 * time.Hour),
		CreatedAt: time.Now(),
	}
	m.tokens[token.Token] = token
	return token.Token, nil
}

func (m *MockTokenStore) GetToken(tokenStr string, expectedType TokenType) (*AccessToken, bool) {
	token, ok := m.tokens[tokenStr]
	if !ok || token.Type != expectedType {
		return nil, false
	}
	return token, true
}

func (m *MockTokenStore) DeleteToken(tokenStr string) error {
	delete(m.tokens, tokenStr)
	return nil
}

func (m *MockTokenStore) CreateOneTimeLoginToken(userID, orgID, authToken string) (string, error) {
	return "mock-one-time-token", nil
}

func (m *MockTokenStore) ConsumeOneTimeLoginToken(oneTimeToken string) (string, error) {
	return "mock-auth-token", nil
}

func (m *MockTokenStore) CreateLinkUploadToken(orgID, repoID, path, userID string) (string, error) {
	token := &AccessToken{
		Token:     "mock-link-upload-token",
		Type:      TokenTypeUpload,
		Source:    "link",
		OrgID:     orgID,
		RepoID:    repoID,
		Path:      path,
		Replace:   false,
		UserID:    userID,
		ExpiresAt: time.Now().Add(1 * time.Hour),
		CreatedAt: time.Now(),
	}
	m.tokens[token.Token] = token
	return token.Token, nil
}

func (m *MockTokenStore) CreateLinkDownloadToken(orgID, repoID, path, userID string) (string, error) {
	token := &AccessToken{
		Token:     "mock-link-download-token",
		Type:      TokenTypeDownload,
		Source:    "link",
		OrgID:     orgID,
		RepoID:    repoID,
		Path:      path,
		UserID:    userID,
		ExpiresAt: time.Now().Add(1 * time.Hour),
		CreatedAt: time.Now(),
	}
	m.tokens[token.Token] = token
	return token.Token, nil
}

func TestNewSeafHTTPHandler(t *testing.T) {
	tokenStore := NewMockTokenStore()
	handler := NewSeafHTTPHandler(nil, nil, nil, tokenStore, nil, nil)

	if handler == nil {
		t.Fatal("NewSeafHTTPHandler returned nil")
	}
	if handler.tokenStore == nil {
		t.Error("tokenStore should be set")
	}
}

func TestSeafHTTPHandlerUploadNoStorage(t *testing.T) {
	tokenStore := NewMockTokenStore()
	// Add a valid upload token
	tokenStore.CreateUploadToken("org1", "repo1", "/", "user1")

	handler := NewSeafHTTPHandler(nil, nil, nil, tokenStore, nil, nil) // nil storage

	r := gin.New()
	r.POST("/seafhttp/upload-api/:token", handler.HandleUpload)

	// Create multipart form
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, _ := writer.CreateFormFile("file", "test.txt")
	part.Write([]byte("test content"))
	writer.Close()

	req, _ := http.NewRequest("POST", "/seafhttp/upload-api/mock-upload-token", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Should fail because storage is nil
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestSeafHTTPHandlerUploadInvalidToken(t *testing.T) {
	tokenStore := NewMockTokenStore()
	handler := NewSeafHTTPHandler(nil, nil, nil, tokenStore, nil, nil)

	r := gin.New()
	r.POST("/seafhttp/upload-api/:token", handler.HandleUpload)

	// Create multipart form
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, _ := writer.CreateFormFile("file", "test.txt")
	part.Write([]byte("test content"))
	writer.Close()

	req, _ := http.NewRequest("POST", "/seafhttp/upload-api/invalid-token", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestSeafHTTPHandlerUploadNoFile(t *testing.T) {
	tokenStore := NewMockTokenStore()
	tokenStore.CreateUploadToken("org1", "repo1", "/", "user1")
	handler := NewSeafHTTPHandler(nil, nil, nil, tokenStore, nil, nil)

	r := gin.New()
	r.POST("/seafhttp/upload-api/:token", handler.HandleUpload)

	// Request without file - but storage is nil, so returns 503 first
	req, _ := http.NewRequest("POST", "/seafhttp/upload-api/mock-upload-token", nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Storage check happens before file check, so we get 503
	// Testing "no file" scenario requires integration testing with real storage
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestSeafHTTPHandlerUploadNoFileWithStorageManager(t *testing.T) {
	tokenStore := NewMockTokenStore()
	tokenStore.CreateUploadToken("org1", "repo1", "/", "user1")
	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, tokenStore, nil, nil)

	r := gin.New()
	r.POST("/seafhttp/upload-api/:token", handler.HandleUpload)

	req, _ := http.NewRequest("POST", "/seafhttp/upload-api/mock-upload-token", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestSeafHTTPHandlerDownloadInvalidToken(t *testing.T) {
	tokenStore := NewMockTokenStore()
	handler := NewSeafHTTPHandler(nil, nil, nil, tokenStore, nil, nil)

	r := gin.New()
	r.GET("/seafhttp/files/:token/*filepath", handler.HandleDownload)

	req, _ := http.NewRequest("GET", "/seafhttp/files/invalid-token/file.txt", nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestSeafHTTPHandlerDownloadNoStorage(t *testing.T) {
	tokenStore := NewMockTokenStore()
	tokenStore.CreateDownloadToken("org1", "repo1", "/file.txt", "user1")
	handler := NewSeafHTTPHandler(nil, nil, nil, tokenStore, nil, nil) // nil storage

	r := gin.New()
	r.GET("/seafhttp/files/:token/*filepath", handler.HandleDownload)

	req, _ := http.NewRequest("GET", "/seafhttp/files/mock-download-token/file.txt", nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestSeafHTTPHandlerDownloadWithStorageManagerObjectFallback(t *testing.T) {
	tokenStore := NewMockTokenStore()
	tokenStore.CreateDownloadToken("org1", "repo1", "/file.txt", "user1")
	manager := storage.NewManager()
	manager.SetDefaultClass("hot-s3-eu")
	manager.RegisterBackend("hot-s3-eu", &mockObjectStore{data: []byte("hello")}, "")
	handler := NewSeafHTTPHandler(nil, manager, nil, tokenStore, nil, nil)

	r := gin.New()
	r.GET("/seafhttp/files/:token/*filepath", handler.HandleDownload)

	req, _ := http.NewRequest("GET", "/seafhttp/files/mock-download-token/file.txt", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if w.Body.String() != "hello" {
		t.Fatalf("body = %q, want %q", w.Body.String(), "hello")
	}
}

func TestGenerateFileID(t *testing.T) {
	id1 := generateFileID("key1")
	id2 := generateFileID("key2")

	// Should be 40 hex chars (20 bytes)
	if len(id1) != 40 {
		t.Errorf("id length = %d, want 40", len(id1))
	}

	// Should be unique (random)
	if id1 == id2 {
		t.Error("generateFileID should produce unique IDs")
	}
}

func TestBytesReader(t *testing.T) {
	data := []byte("hello world")
	reader := newBytesReader(data)

	// Read in parts
	buf := make([]byte, 5)
	n, err := reader.Read(buf)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if n != 5 {
		t.Errorf("n = %d, want 5", n)
	}
	if string(buf) != "hello" {
		t.Errorf("buf = %q, want hello", buf)
	}

	// Read rest
	buf = make([]byte, 10)
	n, err = reader.Read(buf)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if n != 6 {
		t.Errorf("n = %d, want 6", n)
	}
	if string(buf[:n]) != " world" {
		t.Errorf("buf = %q, want ' world'", buf[:n])
	}

	// Read at EOF
	n, err = reader.Read(buf)
	if err != io.EOF {
		t.Errorf("err = %v, want EOF", err)
	}
	if n != 0 {
		t.Errorf("n = %d, want 0", n)
	}
}

func TestBytesReaderEmpty(t *testing.T) {
	reader := newBytesReader([]byte{})
	buf := make([]byte, 10)

	n, err := reader.Read(buf)
	if err != io.EOF {
		t.Errorf("err = %v, want EOF", err)
	}
	if n != 0 {
		t.Errorf("n = %d, want 0", n)
	}
}

// ============================================================================
// sanitizeFilename Tests
// ============================================================================

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"normal filename", "test.txt", "test.txt"},
		{"filename with spaces", "my file.txt", "my_file.txt"},
		{"filename with slashes", "path/to/file.txt", "path_to_file.txt"},
		{"filename with special chars", "file@#$%.txt", "file____.txt"},
		{"filename with dots and hyphens", "my-file.v2.tar.gz", "my-file.v2.tar.gz"},
		{"empty string", "", ""},
		{"filename with unicode", "文件.txt", "__.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeFilename(tt.input)
			if result != tt.expected {
				t.Errorf("sanitizeFilename(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// ============================================================================
// parseContentRange Tests
// ============================================================================

func TestParseContentRange(t *testing.T) {
	tests := []struct {
		name      string
		header    string
		wantStart int64
		wantEnd   int64
		wantTotal int64
		wantOK    bool
	}{
		{"valid range", "bytes 0-1023/5000", 0, 1023, 5000, true},
		{"middle chunk", "bytes 1024-2047/5000", 1024, 2047, 5000, true},
		{"last chunk", "bytes 4096-4999/5000", 4096, 4999, 5000, true},
		{"empty header", "", 0, 0, 0, false},
		{"invalid format", "invalid", 0, 0, 0, false},
		{"missing bytes prefix", "0-100/200", 0, 0, 0, false},
		{"single byte", "bytes 0-0/1", 0, 0, 1, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end, total, ok := parseContentRange(tt.header)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok {
				if start != tt.wantStart {
					t.Errorf("start = %d, want %d", start, tt.wantStart)
				}
				if end != tt.wantEnd {
					t.Errorf("end = %d, want %d", end, tt.wantEnd)
				}
				if total != tt.wantTotal {
					t.Errorf("total = %d, want %d", total, tt.wantTotal)
				}
			}
		})
	}
}

// ============================================================================
// OneTimeLoginToken Tests
// ============================================================================

func TestTokenManagerOneTimeLoginToken(t *testing.T) {
	tm := NewTokenManager(1 * time.Hour)

	tokenStr, err := tm.CreateOneTimeLoginToken("user-1", "org-1", "auth-token-xyz")
	if err != nil {
		t.Fatalf("CreateOneTimeLoginToken failed: %v", err)
	}
	if tokenStr == "" {
		t.Error("token string should not be empty")
	}

	// Consume the token
	authToken, err := tm.ConsumeOneTimeLoginToken(tokenStr)
	if err != nil {
		t.Fatalf("ConsumeOneTimeLoginToken failed: %v", err)
	}
	if authToken != "auth-token-xyz" {
		t.Errorf("authToken = %q, want %q", authToken, "auth-token-xyz")
	}

	// Token should be consumed (single-use)
	_, err = tm.ConsumeOneTimeLoginToken(tokenStr)
	if err == nil {
		t.Error("consumed token should return error on second use")
	}
}

func TestTokenManagerOneTimeLoginToken_NonExistent(t *testing.T) {
	tm := NewTokenManager(1 * time.Hour)

	_, err := tm.ConsumeOneTimeLoginToken("nonexistent")
	if err == nil {
		t.Error("expected error for non-existent token")
	}
}

func TestTokenManagerOneTimeLoginToken_Expired(t *testing.T) {
	tm := NewTokenManager(1 * time.Hour)

	tokenStr, _ := tm.CreateOneTimeLoginToken("user-1", "org-1", "auth-token")

	// Manually expire the token
	tm.mu.Lock()
	tm.tokens[tokenStr].ExpiresAt = time.Now().Add(-1 * time.Second)
	tm.mu.Unlock()

	_, err := tm.ConsumeOneTimeLoginToken(tokenStr)
	if err == nil {
		t.Error("expected error for expired token")
	}
}

func TestTokenManagerOneTimeLoginToken_WrongType(t *testing.T) {
	tm := NewTokenManager(1 * time.Hour)

	// Create a regular upload token
	uploadToken, _ := tm.CreateUploadToken("org-1", "repo-1", "/", "user-1")

	// Try to consume as one-time login token
	_, err := tm.ConsumeOneTimeLoginToken(uploadToken)
	if err == nil {
		t.Error("expected error when consuming non-login token")
	}
}

// ============================================================================
// ChunkManager Tests
// ============================================================================

func TestNewChunkManager(t *testing.T) {
	cm := NewChunkManager()
	if cm == nil {
		t.Fatal("NewChunkManager returned nil")
	}
	if cm.uploads == nil {
		t.Error("uploads map should be initialized")
	}
	if cm.tempDir == "" {
		t.Error("tempDir should not be empty")
	}
}

func TestChunkManagerGetOrCreateUpload(t *testing.T) {
	cm := NewChunkManager()

	upload, err := cm.GetOrCreateUpload("token1", "file.txt", "/", 1024)
	if err != nil {
		t.Fatalf("GetOrCreateUpload failed: %v", err)
	}
	if upload == nil {
		t.Fatal("upload should not be nil")
	}
	if upload.Token != "token1" {
		t.Errorf("Token = %q, want %q", upload.Token, "token1")
	}
	if upload.Filename != "file.txt" {
		t.Errorf("Filename = %q, want %q", upload.Filename, "file.txt")
	}
	if upload.TotalSize != 1024 {
		t.Errorf("TotalSize = %d, want %d", upload.TotalSize, 1024)
	}

	// Getting the same upload should return the existing one
	upload2, err := cm.GetOrCreateUpload("token1", "file.txt", "/", 1024)
	if err != nil {
		t.Fatalf("GetOrCreateUpload (2nd call) failed: %v", err)
	}
	if upload2 != upload {
		t.Error("expected same upload instance for same key")
	}

	// Different key should create a new upload
	upload3, err := cm.GetOrCreateUpload("token2", "file.txt", "/", 2048)
	if err != nil {
		t.Fatalf("GetOrCreateUpload (different key) failed: %v", err)
	}
	if upload3 == upload {
		t.Error("expected different upload instance for different key")
	}

	// Cleanup
	upload.Cleanup()
	upload3.Cleanup()
	cm.CleanupUpload("token1", "file.txt")
	cm.CleanupUpload("token2", "file.txt")
}

func TestChunkManagerGetOrCreateUploadRejectsConfiguredMaxUploadBytes(t *testing.T) {
	cm, _ := newTestChunkManager(t)

	upload, err := cm.GetOrCreateUploadWithLimits("token1", "too-big.bin", "/", 11, 10, 0)
	if upload != nil {
		t.Fatal("upload should be nil when the max upload size is exceeded")
	}
	if !errors.Is(err, errChunkedUploadTooLarge) {
		t.Fatalf("error = %v, want errChunkedUploadTooLarge", err)
	}
	if got := cm.GetUpload("token1", "too-big.bin"); got != nil {
		t.Fatal("rejected upload must not stay tracked")
	}
}

func TestChunkManagerGetOrCreateUploadRejectsNonPositiveTotalSize(t *testing.T) {
	cm, _ := newTestChunkManager(t)

	for _, total := range []int64{0, -1} {
		upload, err := cm.GetOrCreateUploadWithLimits("token1", "bad-total.bin", "/", total, 0, 0)
		if upload != nil {
			t.Fatalf("total=%d: upload should be nil for non-positive total size", total)
		}
		if !errors.Is(err, errChunkedUploadInvalidTotalSize) {
			t.Fatalf("total=%d: error = %v, want errChunkedUploadInvalidTotalSize", total, err)
		}
		if got := cm.GetUpload("token1", "bad-total.bin"); got != nil {
			t.Fatalf("total=%d: rejected upload must not stay tracked", total)
		}
	}
}

func TestChunkManagerGetOrCreateUploadRejectsWhenStagingBudgetWouldBeExceeded(t *testing.T) {
	cm, _ := newTestChunkManager(t)

	uploadA, err := cm.GetOrCreateUploadWithLimits("token1", "a.bin", "/", 7, 0, 10)
	if err != nil {
		t.Fatalf("GetOrCreateUploadWithLimits(uploadA) failed: %v", err)
	}
	defer cm.CleanupTrackedUpload(uploadA)

	uploadB, err := cm.GetOrCreateUploadWithLimits("token2", "b.bin", "/", 4, 0, 10)
	if uploadB != nil {
		t.Fatal("upload should be nil when staging budget would be exceeded")
	}
	if !errors.Is(err, errChunkedUploadStagingLimitExceeded) {
		t.Fatalf("error = %v, want errChunkedUploadStagingLimitExceeded", err)
	}
	if got := cm.GetUpload("token2", "b.bin"); got != nil {
		t.Fatal("rejected upload must not stay tracked")
	}
}

func TestChunkManagerTracksSameBasenameSeparatelyByIdentityAndPath(t *testing.T) {
	cm, _ := newTestChunkManager(t)

	uploadA, err := cm.GetOrCreateUploadByIdentity("token1", "ident-a", "/a", "file.txt", 10)
	if err != nil {
		t.Fatalf("GetOrCreateUploadByIdentity(uploadA) failed: %v", err)
	}
	uploadAAgain, err := cm.GetOrCreateUploadByIdentity("token1", "ident-a", "/a", "file.txt", 10)
	if err != nil {
		t.Fatalf("GetOrCreateUploadByIdentity(uploadA again) failed: %v", err)
	}
	if uploadAAgain != uploadA {
		t.Fatal("same upload identity should reuse the tracked upload")
	}

	uploadB, err := cm.GetOrCreateUploadByIdentity("token1", "ident-b", "/b", "file.txt", 10)
	if err != nil {
		t.Fatalf("GetOrCreateUploadByIdentity(uploadB) failed: %v", err)
	}
	defer cm.CleanupTrackedUpload(uploadB)

	if uploadA == uploadB {
		t.Fatal("different upload identities/paths must not share a tracker")
	}
	if uploadA.TempPath == uploadB.TempPath {
		t.Fatalf("different tracked uploads must not share a temp path: %s", uploadA.TempPath)
	}
	if got := cm.GetUploadByIdentity("token1", "ident-a", "/a", "file.txt", 10); got != uploadA {
		t.Fatal("GetUploadByIdentity should return uploadA")
	}
	if got := cm.GetUploadByIdentity("token1", "ident-b", "/b", "file.txt", 10); got != uploadB {
		t.Fatal("GetUploadByIdentity should return uploadB")
	}

	cm.CleanupTrackedUpload(uploadA)
	if got := cm.GetUploadByIdentity("token1", "ident-a", "/a", "file.txt", 10); got != nil {
		t.Fatal("cleanup of uploadA should remove only uploadA")
	}
	if got := cm.GetUploadByIdentity("token1", "ident-b", "/b", "file.txt", 10); got != uploadB {
		t.Fatal("cleanup of uploadA must not remove uploadB")
	}
}

func TestChunkManagerFallbackTrackerKeySeparatesRepeatedBasenamesAcrossParentDirs(t *testing.T) {
	cm, _ := newTestChunkManager(t)

	uploadA, err := cm.GetOrCreateUpload("token1", "same.txt", "/a", 10)
	if err != nil {
		t.Fatalf("GetOrCreateUpload(uploadA) failed: %v", err)
	}
	defer cm.CleanupTrackedUpload(uploadA)

	uploadB, err := cm.GetOrCreateUpload("token1", "same.txt", "/b", 10)
	if err != nil {
		t.Fatalf("GetOrCreateUpload(uploadB) failed: %v", err)
	}
	defer cm.CleanupTrackedUpload(uploadB)

	if uploadA == uploadB {
		t.Fatal("different parent dirs must not share a fallback tracker")
	}
	if uploadA.TempPath == uploadB.TempPath {
		t.Fatalf("different fallback trackers must not share a temp path: %s", uploadA.TempPath)
	}
}

func TestChunkUploadWriteAndRead(t *testing.T) {
	cm := NewChunkManager()

	upload, err := cm.GetOrCreateUpload("token1", "test.bin", "/", 10)
	if err != nil {
		t.Fatalf("GetOrCreateUpload failed: %v", err)
	}
	defer func() {
		upload.Cleanup()
		cm.CleanupUpload("token1", "test.bin")
	}()

	// Write chunk
	err = upload.WriteChunk([]byte("hello"), 0, 4)
	if err != nil {
		t.Fatalf("WriteChunk failed: %v", err)
	}

	// Write second chunk
	err = upload.WriteChunk([]byte("world"), 5, 9)
	if err != nil {
		t.Fatalf("WriteChunk (2nd) failed: %v", err)
	}

	// Check completeness
	if !upload.IsComplete() {
		t.Error("upload should be complete after writing all bytes")
	}

	// Read content
	content, err := upload.GetContent()
	if err != nil {
		t.Fatalf("GetContent failed: %v", err)
	}
	if string(content) != "helloworld" {
		t.Errorf("content = %q, want %q", string(content), "helloworld")
	}
}

func TestChunkUploadIsComplete_Incomplete(t *testing.T) {
	cm := NewChunkManager()

	upload, err := cm.GetOrCreateUpload("token1", "test.bin", "/", 100)
	if err != nil {
		t.Fatalf("GetOrCreateUpload failed: %v", err)
	}
	defer func() {
		upload.Cleanup()
		cm.CleanupUpload("token1", "test.bin")
	}()

	if upload.IsComplete() {
		t.Error("empty upload should not be complete")
	}

	upload.WriteChunk([]byte("partial"), 0, 6)
	if upload.IsComplete() {
		t.Error("partially written upload should not be complete")
	}
}

func TestChunkUploadIsComplete_OutOfOrderLastChunk(t *testing.T) {
	cm := NewChunkManager()

	upload, err := cm.GetOrCreateUpload("token1", "test.bin", "/", 12)
	if err != nil {
		t.Fatalf("GetOrCreateUpload failed: %v", err)
	}
	defer func() {
		upload.Cleanup()
		cm.CleanupUpload("token1", "test.bin")
	}()

	if err := upload.WriteChunk([]byte("world!"), 6, 11); err != nil {
		t.Fatalf("WriteChunk last chunk failed: %v", err)
	}
	if upload.IsComplete() {
		t.Fatal("upload should not be complete when only the final range was received")
	}
	if upload.TryStartFinalization() {
		t.Fatal("upload should not start finalization with missing leading range")
	}

	if err := upload.WriteChunk([]byte("hello "), 0, 5); err != nil {
		t.Fatalf("WriteChunk first chunk failed: %v", err)
	}
	if !upload.IsComplete() {
		t.Fatal("upload should be complete after all ranges are received")
	}
	if !upload.TryStartFinalization() {
		t.Fatal("first finalization attempt should win")
	}
	if upload.TryStartFinalization() {
		t.Fatal("second finalization attempt should be rejected")
	}
	upload.ResetFinalization()
	if !upload.TryStartFinalization() {
		t.Fatal("retry should be able to restart finalization after a transient failure")
	}

	content, err := upload.GetContent()
	if err != nil {
		t.Fatalf("GetContent failed: %v", err)
	}
	if string(content) != "hello world!" {
		t.Errorf("content = %q, want %q", string(content), "hello world!")
	}
}

func TestChunkUploadTryStartFinalizationMetricsDifferentiateStates(t *testing.T) {
	cm := NewChunkManager()

	upload, err := cm.GetOrCreateUpload("token-metrics", "metric-test.bin", "/", 10)
	if err != nil {
		t.Fatalf("GetOrCreateUpload failed: %v", err)
	}
	defer func() {
		upload.Cleanup()
		cm.CleanupUpload("token-metrics", "metric-test.bin")
	}()

	beforeNotComplete := testutil.ToFloat64(metrics.ChunkUploadFinalizationAttemptsTotal.WithLabelValues("not_complete"))
	beforeAlreadyFinalizing := testutil.ToFloat64(metrics.ChunkUploadFinalizationAttemptsTotal.WithLabelValues("already_finalizing"))
	beforeStarted := testutil.ToFloat64(metrics.ChunkUploadFinalizationAttemptsTotal.WithLabelValues("started"))

	if upload.TryStartFinalization() {
		t.Fatal("upload should not start finalization before all ranges are received")
	}
	if got := testutil.ToFloat64(metrics.ChunkUploadFinalizationAttemptsTotal.WithLabelValues("not_complete")); got != beforeNotComplete+1 {
		t.Fatalf("not_complete metric = %v, want %v", got, beforeNotComplete+1)
	}

	if err := upload.WriteChunk([]byte("hello"), 0, 4); err != nil {
		t.Fatalf("WriteChunk first chunk failed: %v", err)
	}
	if err := upload.WriteChunk([]byte("world"), 5, 9); err != nil {
		t.Fatalf("WriteChunk second chunk failed: %v", err)
	}

	if !upload.TryStartFinalization() {
		t.Fatal("first finalization attempt should win once upload is complete")
	}
	if got := testutil.ToFloat64(metrics.ChunkUploadFinalizationAttemptsTotal.WithLabelValues("started")); got != beforeStarted+1 {
		t.Fatalf("started metric = %v, want %v", got, beforeStarted+1)
	}

	if upload.TryStartFinalization() {
		t.Fatal("second finalization attempt should be rejected while finalizing")
	}
	if got := testutil.ToFloat64(metrics.ChunkUploadFinalizationAttemptsTotal.WithLabelValues("already_finalizing")); got != beforeAlreadyFinalizing+1 {
		t.Fatalf("already_finalizing metric = %v, want %v", got, beforeAlreadyFinalizing+1)
	}
}

func TestChunkUploadWriteDuringFinalizationIsIdempotentOnly(t *testing.T) {
	cm := NewChunkManager()

	upload, err := cm.GetOrCreateUpload("token1", "test.bin", "/", 10)
	if err != nil {
		t.Fatalf("GetOrCreateUpload failed: %v", err)
	}
	defer func() {
		upload.Cleanup()
		cm.CleanupUpload("token1", "test.bin")
	}()

	if err := upload.WriteChunk([]byte("hello"), 0, 4); err != nil {
		t.Fatalf("WriteChunk first chunk failed: %v", err)
	}
	if err := upload.WriteChunk([]byte("world"), 5, 9); err != nil {
		t.Fatalf("WriteChunk second chunk failed: %v", err)
	}
	if !upload.TryStartFinalization() {
		t.Fatal("upload should enter finalization")
	}

	if err := upload.WriteChunk([]byte("XXXXX"), 5, 9); err != nil {
		t.Fatalf("duplicate range during finalization should be idempotent: %v", err)
	}
	content, err := upload.GetContent()
	if err != nil {
		t.Fatalf("GetContent failed: %v", err)
	}
	if string(content) != "helloworld" {
		t.Fatalf("duplicate finalizing write mutated temp file: got %q", string(content))
	}

	if err := upload.WriteChunk([]byte("!"), 10, 10); err == nil {
		t.Fatal("new range during finalization should be rejected")
	}

	upload.ResetFinalization()
	if err := upload.WriteChunk([]byte("XXXXX"), 5, 9); err != nil {
		t.Fatalf("duplicate range after failed finalization should stay idempotent: %v", err)
	}
	content, err = upload.GetContent()
	if err != nil {
		t.Fatalf("GetContent after reset failed: %v", err)
	}
	if string(content) != "helloworld" {
		t.Fatalf("post-reset duplicate write mutated temp file: got %q", string(content))
	}
}

// A second complete chunk request that arrives while finalization is already in
// flight (e.g. a resumable retry of the final chunk after the original finalize
// response was lost) must become a waiter and receive the same result the winner
// publishes — not a bare {"success":true} ack the client cannot turn into a
// dirent. This is the server-side root cause of big files reporting "Uploaded"
// while never appearing in the listing.
func TestChunkUploadClaimFinalizationWaiterReceivesWinnerResult(t *testing.T) {
	cm := NewChunkManager()
	upload, err := cm.GetOrCreateUpload("token-waiter", "big.zip", "/", 10)
	if err != nil {
		t.Fatalf("GetOrCreateUpload failed: %v", err)
	}
	defer func() {
		upload.Cleanup()
		cm.CleanupUpload("token-waiter", "big.zip")
	}()

	if err := upload.WriteChunk([]byte("hello"), 0, 4); err != nil {
		t.Fatalf("WriteChunk first chunk failed: %v", err)
	}
	if err := upload.WriteChunk([]byte("world"), 5, 9); err != nil {
		t.Fatalf("WriteChunk second chunk failed: %v", err)
	}

	claim, _ := upload.ClaimFinalization()
	if claim != finalizeClaimWinner {
		t.Fatalf("first claim = %v, want winner", claim)
	}

	waiterClaim, waiterDone := upload.ClaimFinalization()
	if waiterClaim != finalizeClaimWaiter {
		t.Fatalf("second claim = %v, want waiter", waiterClaim)
	}
	if waiterDone == nil {
		t.Fatal("waiter must receive a non-nil done channel")
	}

	select {
	case <-waiterDone:
		t.Fatal("waiter woke before the winner published a result")
	default:
	}

	upload.PublishFinalizeSuccess("file-id-123", "big.zip", 10)

	select {
	case <-waiterDone:
	case <-time.After(2 * time.Second):
		t.Fatal("waiter was not released after the winner published")
	}

	outcome, ok := upload.FinalizeOutcome()
	if !ok || outcome == nil {
		t.Fatal("waiter could not read the published finalize outcome")
	}
	if outcome.err != nil {
		t.Fatalf("outcome.err = %v, want nil", outcome.err)
	}
	if outcome.fileID != "file-id-123" || outcome.actualFilename != "big.zip" || outcome.totalSize != 10 {
		t.Fatalf("outcome = %+v, want fileID=file-id-123 name=big.zip size=10", outcome)
	}
}

// A finalization failure must wake waiters with the error so they surface a
// retryable response instead of a false success.
func TestChunkUploadClaimFinalizationWaiterReceivesFailure(t *testing.T) {
	cm := NewChunkManager()
	upload, err := cm.GetOrCreateUpload("token-waiter-fail", "big.zip", "/", 10)
	if err != nil {
		t.Fatalf("GetOrCreateUpload failed: %v", err)
	}
	defer func() {
		upload.Cleanup()
		cm.CleanupUpload("token-waiter-fail", "big.zip")
	}()

	if err := upload.WriteChunk([]byte("hello"), 0, 4); err != nil {
		t.Fatalf("WriteChunk first chunk failed: %v", err)
	}
	if err := upload.WriteChunk([]byte("world"), 5, 9); err != nil {
		t.Fatalf("WriteChunk second chunk failed: %v", err)
	}

	if claim, _ := upload.ClaimFinalization(); claim != finalizeClaimWinner {
		t.Fatalf("first claim = %v, want winner", claim)
	}
	_, waiterDone := upload.ClaimFinalization()

	finalizeErr := fmt.Errorf("block store unavailable")
	upload.PublishFinalizeFailure(finalizeErr)

	select {
	case <-waiterDone:
	case <-time.After(2 * time.Second):
		t.Fatal("waiter was not released after the winner published a failure")
	}

	outcome, ok := upload.FinalizeOutcome()
	if !ok || outcome == nil || outcome.err == nil {
		t.Fatalf("waiter outcome = %+v, want a non-nil error", outcome)
	}
	if !errors.Is(outcome.err, finalizeErr) {
		t.Fatalf("outcome.err = %v, want %v", outcome.err, finalizeErr)
	}
}

// The finalize-outcome cache lets a final-chunk retry that lands after the
// winner already finalized AND cleaned up its tracker still receive the real
// file id, instead of a bare ack the client cannot confirm. This is the
// residual window behind big files reporting failure when retried late amid
// other concurrent uploads.
func TestChunkManagerFinalizeOutcomeCache(t *testing.T) {
	cm, _ := newTestChunkManager(t)
	base := time.Now()
	cm.now = func() time.Time { return base }
	cm.outcomeTTL = 5 * time.Minute

	const id = "ident-A"
	cm.CacheFinalizeOutcome("tok", id, "/", "big.zip", "file-id-123", "big.zip", 100)

	got, ok := cm.LookupFinalizeOutcome("tok", id, "/", "big.zip", 100)
	if !ok {
		t.Fatal("expected cached finalize outcome to be found")
	}
	if got.fileID != "file-id-123" || got.actualFilename != "big.zip" || got.totalSize != 100 {
		t.Fatalf("cached outcome = %+v, want fileID=file-id-123 name=big.zip size=100", got)
	}

	// A different total size must NOT match (cheap extra guard).
	if _, ok := cm.LookupFinalizeOutcome("tok", id, "/", "big.zip", 999); ok {
		t.Fatal("size mismatch must miss the cache")
	}
	// A different token must not collide.
	if _, ok := cm.LookupFinalizeOutcome("other-tok", id, "/", "big.zip", 100); ok {
		t.Fatal("different token must miss the cache")
	}
	// Same basename + same size but a DIFFERENT parent dir (folder upload through
	// the same token) must not read the other file's id.
	if _, ok := cm.LookupFinalizeOutcome("tok", id, "/sub", "big.zip", 100); ok {
		t.Fatal("different parent dir must miss the cache")
	}
	// A DIFFERENT upload identifier (a new upload of the same name/size/path,
	// possibly different content) must miss, never reading the stale id.
	if _, ok := cm.LookupFinalizeOutcome("tok", "ident-B", "/", "big.zip", 100); ok {
		t.Fatal("different upload identifier must miss the cache")
	}
	// An empty identifier must never be cached nor served, so a client that does
	// not send one can never read another upload's id.
	cm.CacheFinalizeOutcome("tok", "", "/", "no-ident.zip", "leaked-id", "no-ident.zip", 100)
	if _, ok := cm.LookupFinalizeOutcome("tok", "", "/", "no-ident.zip", 100); ok {
		t.Fatal("empty identifier must never hit the cache")
	}

	// After the TTL the entry must miss and be swept.
	cm.now = func() time.Time { return base.Add(6 * time.Minute) }
	if _, ok := cm.LookupFinalizeOutcome("tok", id, "/", "big.zip", 100); ok {
		t.Fatal("expired entry must miss the cache")
	}
	cm.sweepOnce()
	cm.mu.RLock()
	remaining := len(cm.outcomes)
	cm.mu.RUnlock()
	if remaining != 0 {
		t.Fatalf("expired entries must be swept from the cache, %d remain", remaining)
	}
}

func TestChunkManagerFinalizeOutcomeCacheCapacityEvictsOldest(t *testing.T) {
	cm, _ := newTestChunkManager(t)
	base := time.Now()
	current := base
	cm.now = func() time.Time { return current }
	cm.outcomeTTL = time.Hour
	cm.outcomeLimit = 2

	beforeCapacityEvictions := testutil.ToFloat64(metrics.ChunkUploadFinalizeOutcomeCacheEvictionsTotal.WithLabelValues("capacity"))

	cm.CacheFinalizeOutcome("tok", "ident-A", "/", "a.bin", "file-a", "a.bin", 10)
	current = base.Add(1 * time.Second)
	cm.CacheFinalizeOutcome("tok", "ident-B", "/", "b.bin", "file-b", "b.bin", 20)
	current = base.Add(2 * time.Second)
	cm.CacheFinalizeOutcome("tok", "ident-C", "/", "c.bin", "file-c", "c.bin", 30)

	cm.mu.RLock()
	size := len(cm.outcomes)
	cm.mu.RUnlock()
	if size != 2 {
		t.Fatalf("outcome cache size = %d, want 2 after capacity pruning", size)
	}

	if _, ok := cm.LookupFinalizeOutcome("tok", "ident-A", "/", "a.bin", 10); ok {
		t.Fatal("oldest cached outcome should be evicted once the hard cap is exceeded")
	}
	if _, ok := cm.LookupFinalizeOutcome("tok", "ident-B", "/", "b.bin", 20); !ok {
		t.Fatal("second cached outcome should remain after capacity pruning")
	}
	if _, ok := cm.LookupFinalizeOutcome("tok", "ident-C", "/", "c.bin", 30); !ok {
		t.Fatal("newest cached outcome should remain after capacity pruning")
	}

	if got := testutil.ToFloat64(metrics.ChunkUploadFinalizeOutcomeCacheEntries); got != 2 {
		t.Fatalf("outcome cache size gauge = %v, want 2", got)
	}
	if got := testutil.ToFloat64(metrics.ChunkUploadFinalizeOutcomeCacheEvictionsTotal.WithLabelValues("capacity")); got != beforeCapacityEvictions+1 {
		t.Fatalf("capacity eviction counter = %v, want %v", got, beforeCapacityEvictions+1)
	}
}

func TestChunkUploadAccountBlockOnceSurvivesFinalizeRetry(t *testing.T) {
	cm := NewChunkManager()

	upload, err := cm.GetOrCreateUpload("token1", "test.bin", "/", 10)
	if err != nil {
		t.Fatalf("GetOrCreateUpload failed: %v", err)
	}
	defer func() {
		upload.Cleanup()
		cm.CleanupUpload("token1", "test.bin")
	}()

	calls := 0
	account := func() error {
		calls++
		return nil
	}
	if err := upload.AccountBlockOnce(0, "block-a", account); err != nil {
		t.Fatalf("first account failed: %v", err)
	}
	if err := upload.AccountBlockOnce(0, "block-a", account); err != nil {
		t.Fatalf("retry account failed: %v", err)
	}
	if calls != 1 {
		t.Fatalf("account called %d times, want 1", calls)
	}
	if err := upload.AccountBlockOnce(0, "block-b", account); err == nil {
		t.Fatal("same block position changing identity should be rejected")
	}
	accounted, err := upload.BlockAlreadyAccounted(0, "block-a")
	if err != nil {
		t.Fatalf("BlockAlreadyAccounted failed: %v", err)
	}
	if !accounted {
		t.Fatal("block position should be marked accounted")
	}
	accounted, err = upload.BlockAlreadyAccounted(1, "block-a")
	if err != nil {
		t.Fatalf("BlockAlreadyAccounted for missing position failed: %v", err)
	}
	if accounted {
		t.Fatal("missing block position should not be marked accounted")
	}
}

func TestAcquireFinalizeUploadBlockMetadataPermitSerializesCallers(t *testing.T) {
	releaseFirst, err := acquireFinalizeUploadBlockMetadataPermit(context.Background())
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	releasedFirst := false
	defer func() {
		if !releasedFirst {
			releaseFirst()
		}
	}()

	blockedCtx, cancelBlocked := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancelBlocked()
	releaseSecond, err := acquireFinalizeUploadBlockMetadataPermit(blockedCtx)
	if releaseSecond != nil {
		releaseSecond()
		t.Fatal("second acquire should not succeed while first permit is held")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second acquire error = %v, want context deadline exceeded while blocked", err)
	}

	releaseFirst()
	releasedFirst = true

	releaseSecond, err = acquireFinalizeUploadBlockMetadataPermit(context.Background())
	if err != nil {
		t.Fatalf("second acquire failed after release: %v", err)
	}
	releaseSecond()
}

func TestFinalizeUploadStreamingFailsClosedWhenEncryptionStatusLookupFails(t *testing.T) {
	cm, _ := newTestChunkManager(t)

	upload, err := cm.GetOrCreateUpload("token1", "test.bin", "/", 5)
	if err != nil {
		t.Fatalf("GetOrCreateUpload failed: %v", err)
	}
	defer func() {
		upload.Cleanup()
		cm.CleanupUpload("token1", "test.bin")
	}()

	if err := upload.WriteChunk([]byte("hello"), 0, 4); err != nil {
		t.Fatalf("WriteChunk failed: %v", err)
	}

	originalQuota := checkUploadStorageQuotaForCurrentHeadFn
	originalEncrypted := lookupLibraryEncryptedForUploadFn
	t.Cleanup(func() {
		checkUploadStorageQuotaForCurrentHeadFn = originalQuota
		lookupLibraryEncryptedForUploadFn = originalEncrypted
	})

	checkUploadStorageQuotaForCurrentHeadFn = func(h *SeafHTTPHandler, orgID, repoID, userID, parentDir, filename string, fileSize int64, replace bool) (int64, int64, error) {
		return fileSize, 1, nil
	}

	lookupErr := errors.New("lookup failed")
	lookupLibraryEncryptedForUploadFn = func(h *SeafHTTPHandler, orgID, repoID string) (bool, error) {
		return false, lookupErr
	}

	handler := NewSeafHTTPHandler(nil, nil, nil, nil, nil, nil)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/seafhttp/upload-api/token1", nil)
	token := &AccessToken{OrgID: "org1", RepoID: "repo1", UserID: "user1", Token: "token1"}

	_, _, _, _, err = handler.finalizeUploadStreaming(c, token, upload, "/", "test.bin", "", 5, false)
	if !errors.Is(err, lookupErr) {
		t.Fatalf("finalizeUploadStreaming error = %v, want wrapped lookup error %v", err, lookupErr)
	}
}

func TestFinalizeUploadStreamingDoesNotWrapS3PutInMetadataPermit(t *testing.T) {
	cm, _ := newTestChunkManager(t)

	upload, err := cm.GetOrCreateUpload("token1", "test.bin", "/", 5)
	if err != nil {
		t.Fatalf("GetOrCreateUpload failed: %v", err)
	}
	defer func() {
		upload.Cleanup()
		cm.CleanupUpload("token1", "test.bin")
	}()

	if err := upload.WriteChunk([]byte("hello"), 0, 4); err != nil {
		t.Fatalf("WriteChunk failed: %v", err)
	}

	releaseHeld, err := acquireFinalizeUploadBlockMetadataPermit(context.Background())
	if err != nil {
		t.Fatalf("failed to pre-acquire metadata permit: %v", err)
	}
	releasedHeld := false
	defer func() {
		if !releasedHeld {
			releaseHeld()
		}
	}()

	originalPut := putUploadedBlockAutoFn
	originalRegister := registerUploadedBlockAndMappingForUploadFn
	originalQuota := checkUploadStorageQuotaForCurrentHeadFn
	originalEncrypted := lookupLibraryEncryptedForUploadFn
	originalCommit := commitSeafHTTPUploadedFileMultiBlockFn
	t.Cleanup(func() {
		putUploadedBlockAutoFn = originalPut
		registerUploadedBlockAndMappingForUploadFn = originalRegister
		checkUploadStorageQuotaForCurrentHeadFn = originalQuota
		lookupLibraryEncryptedForUploadFn = originalEncrypted
		commitSeafHTTPUploadedFileMultiBlockFn = originalCommit
	})

	checkUploadStorageQuotaForCurrentHeadFn = func(h *SeafHTTPHandler, orgID, repoID, userID, parentDir, filename string, fileSize int64, replace bool) (int64, int64, error) {
		return fileSize, 1, nil
	}
	lookupLibraryEncryptedForUploadFn = func(h *SeafHTTPHandler, orgID, repoID string) (bool, error) {
		return false, nil
	}

	putStarted := make(chan struct{})
	putUploadedBlockAutoFn = func(_ context.Context, _ *storage.BlockStore, hash string, data []byte) (string, error) {
		close(putStarted)
		return hash, nil
	}

	registerCalled := make(chan struct{}, 1)
	registerUploadedBlockAndMappingForUploadFn = func(_ *db.DB, _, _, _, _ string, _ int, _, _, _ string) error {
		registerCalled <- struct{}{}
		return nil
	}

	expectedSHA1 := sha1.Sum([]byte("hello"))
	expectedBlockID := hex.EncodeToString(expectedSHA1[:])
	commitSeafHTTPUploadedFileMultiBlockFn = func(h *SeafHTTPHandler, ctx context.Context, orgID, repoID, userID, parentDir, filename, fileID string, blockIDs []string, fileSize int64, replace bool) (string, string, int64, int64, error) {
		if orgID != "org1" || repoID != "repo1" || userID != "user1" {
			return "", "", 0, 0, fmt.Errorf("unexpected commit identity %s/%s/%s", orgID, repoID, userID)
		}
		if parentDir != "/" || filename != "test.bin" || replace {
			return "", "", 0, 0, fmt.Errorf("unexpected commit target dir=%s filename=%s replace=%v", parentDir, filename, replace)
		}
		if fileID != expectedBlockID {
			return "", "", 0, 0, fmt.Errorf("commit fileID = %s, want %s", fileID, expectedBlockID)
		}
		if !reflect.DeepEqual(blockIDs, []string{expectedBlockID}) {
			return "", "", 0, 0, fmt.Errorf("commit blockIDs = %v, want [%s]", blockIDs, expectedBlockID)
		}
		if fileSize != 5 {
			return "", "", 0, 0, fmt.Errorf("commit fileSize = %d, want 5", fileSize)
		}
		return "commit-1", filename, 0, 0, nil
	}

	handler := NewSeafHTTPHandler(&storage.S3Store{}, nil, nil, nil, nil, nil)
	token := &AccessToken{OrgID: "org1", RepoID: "repo1", UserID: "user1", Token: "token1"}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/seafhttp/upload-api/token1", nil)

	type finalizeResult struct {
		fileID         string
		actualFilename string
		err            error
	}
	done := make(chan finalizeResult, 1)
	go func() {
		fileID, actualFilename, _, _, err := handler.finalizeUploadStreaming(c, token, upload, "/", "test.bin", "", 5, false)
		done <- finalizeResult{fileID: fileID, actualFilename: actualFilename, err: err}
	}()

	select {
	case <-putStarted:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("S3 PUT did not start while metadata permit was pre-held")
	}

	select {
	case <-registerCalled:
		t.Fatal("metadata registration started before the permit was released")
	case <-time.After(25 * time.Millisecond):
	}

	select {
	case res := <-done:
		t.Fatalf("finalizeUploadStreaming returned before metadata permit release: %+v", res)
	case <-time.After(25 * time.Millisecond):
	}

	releaseHeld()
	releasedHeld = true

	select {
	case <-registerCalled:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("metadata registration did not run after the permit was released")
	}

	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("finalizeUploadStreaming returned unexpected error: %v", res.err)
		}
		if res.fileID != expectedBlockID {
			t.Fatalf("finalizeUploadStreaming fileID = %s, want %s", res.fileID, expectedBlockID)
		}
		if res.actualFilename != "test.bin" {
			t.Fatalf("finalizeUploadStreaming actualFilename = %s, want test.bin", res.actualFilename)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("finalizeUploadStreaming did not complete after the permit was released")
	}
}

// finalizeUploadStreamingReuseFixture wires the common mocks for the
// Cassandra-first reuse tests below and returns call counters. The legacy
// Exists+PUT path (putUploadedBlockAutoFn) is the only one that issues the
// old upload-backend HEAD, so a zero count there proves the reusable/needs-put
// logic stayed off that legacy path.
func finalizeUploadStreamingReuseFixture(t *testing.T, decision db.BlockReuseProbe) (legacyPuts, directPuts, reusableChecks, registerCalls *atomic.Int32, run func() (string, error)) {
	t.Helper()

	cm, _ := newTestChunkManager(t)
	upload, err := cm.GetOrCreateUpload("token1", "test.bin", "/", 5)
	if err != nil {
		t.Fatalf("GetOrCreateUpload failed: %v", err)
	}
	t.Cleanup(func() {
		upload.Cleanup()
		cm.CleanupUpload("token1", "test.bin")
	})
	if err := upload.WriteChunk([]byte("hello"), 0, 4); err != nil {
		t.Fatalf("WriteChunk failed: %v", err)
	}

	originalProbe := probeUploadedBlockReuseForUploadFn
	originalLegacyPut := putUploadedBlockAutoFn
	originalDirectPut := putUploadedBlockAutoDirectForUploadFn
	originalEnsureReusable := ensureReusableBlockPresentForUploadFn
	originalRegister := registerUploadedBlockAndMappingForUploadFn
	originalQuota := checkUploadStorageQuotaForCurrentHeadFn
	originalEncrypted := lookupLibraryEncryptedForUploadFn
	originalCommit := commitSeafHTTPUploadedFileMultiBlockFn
	t.Cleanup(func() {
		probeUploadedBlockReuseForUploadFn = originalProbe
		putUploadedBlockAutoFn = originalLegacyPut
		putUploadedBlockAutoDirectForUploadFn = originalDirectPut
		ensureReusableBlockPresentForUploadFn = originalEnsureReusable
		registerUploadedBlockAndMappingForUploadFn = originalRegister
		checkUploadStorageQuotaForCurrentHeadFn = originalQuota
		lookupLibraryEncryptedForUploadFn = originalEncrypted
		commitSeafHTTPUploadedFileMultiBlockFn = originalCommit
	})

	checkUploadStorageQuotaForCurrentHeadFn = func(h *SeafHTTPHandler, orgID, repoID, userID, parentDir, filename string, fileSize int64, replace bool) (int64, int64, error) {
		return fileSize, 1, nil
	}
	lookupLibraryEncryptedForUploadFn = func(h *SeafHTTPHandler, orgID, repoID string) (bool, error) {
		return false, nil
	}
	probeUploadedBlockReuseForUploadFn = func(database *db.DB, orgID, blockID string) (db.BlockReuseProbe, error) {
		return decision, nil
	}

	legacyPuts = &atomic.Int32{}
	directPuts = &atomic.Int32{}
	reusableChecks = &atomic.Int32{}
	registerCalls = &atomic.Int32{}

	putUploadedBlockAutoFn = func(_ context.Context, _ *storage.BlockStore, hash string, _ []byte) (string, error) {
		legacyPuts.Add(1)
		return hash, nil
	}
	putUploadedBlockAutoDirectForUploadFn = func(_ context.Context, _ *storage.BlockStore, hash string, _ []byte) (string, error) {
		directPuts.Add(1)
		return hash, nil
	}
	ensureReusableBlockPresentForUploadFn = func(_ context.Context, _ string, _ db.BlockReuseProbe, _ []byte, _ *storage.Manager, _ *storage.BlockStore, _ string) (string, error) {
		reusableChecks.Add(1)
		return "", nil
	}
	registerUploadedBlockAndMappingForUploadFn = func(_ *db.DB, _, _, _, _ string, _ int, _, _, _ string) error {
		registerCalls.Add(1)
		return nil
	}
	commitSeafHTTPUploadedFileMultiBlockFn = func(h *SeafHTTPHandler, ctx context.Context, orgID, repoID, userID, parentDir, filename, fileID string, blockIDs []string, fileSize int64, replace bool) (string, string, int64, int64, error) {
		return "commit-1", filename, 0, 0, nil
	}

	handler := NewSeafHTTPHandler(&storage.S3Store{}, nil, nil, nil, nil, nil)
	token := &AccessToken{OrgID: "org1", RepoID: "repo1", UserID: "user1", Token: "token1"}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/seafhttp/upload-api/token1", nil)

	run = func() (string, error) {
		fileID, _, _, _, err := handler.finalizeUploadStreaming(c, token, upload, "/", "test.bin", "", 5, false)
		return fileID, err
	}
	return legacyPuts, directPuts, reusableChecks, registerCalls, run
}

// TestFinalizeUploadStreamingReusableVerifiesCanonicalNotLegacyPut verifies that
// when the Cassandra probe reports a block reusable, finalization routes through
// the canonical verify/repair step (EnsureReusableBlockPresent) exactly once and
// never touches the legacy Exists+PUT path or the direct PUT, yet still registers
// the block reference + mapping. The canonical verify itself (an S3 HEAD on the
// declared canonical key, with repair-on-miss) is exercised by the unit tests in
// internal/api/v2/upload_reuse_test.go.
func TestFinalizeUploadStreamingReusableVerifiesCanonicalNotLegacyPut(t *testing.T) {
	legacyPuts, directPuts, reusableChecks, registerCalls, run := finalizeUploadStreamingReuseFixture(t,
		db.BlockReuseProbe{Decision: db.BlockReuseReusable, StorageClass: "hot"})

	expectedSHA1 := sha1.Sum([]byte("hello"))
	expectedBlockID := hex.EncodeToString(expectedSHA1[:])

	fileID, err := run()
	if err != nil {
		t.Fatalf("finalizeUploadStreaming error = %v, want nil", err)
	}
	if fileID != expectedBlockID {
		t.Fatalf("fileID = %s, want %s", fileID, expectedBlockID)
	}
	if got := legacyPuts.Load(); got != 0 {
		t.Errorf("legacy Exists+PUT (HEAD) calls = %d, want 0 for a reusable block", got)
	}
	if got := directPuts.Load(); got != 0 {
		t.Errorf("direct PUT calls = %d, want 0 for a reusable block", got)
	}
	if got := reusableChecks.Load(); got != 1 {
		t.Errorf("reusable canonical checks = %d, want 1", got)
	}
	if got := registerCalls.Load(); got != 1 {
		t.Errorf("register ref/mapping calls = %d, want 1", got)
	}
}

// TestFinalizeUploadStreamingNeedsPutUsesDirectPut verifies that when the probe
// reports the block needs storing, finalization performs exactly one direct PUT
// (no S3 HEAD via the legacy path) and registers the block reference + mapping.
func TestFinalizeUploadStreamingNeedsPutUsesDirectPut(t *testing.T) {
	legacyPuts, directPuts, reusableChecks, registerCalls, run := finalizeUploadStreamingReuseFixture(t,
		db.BlockReuseProbe{Decision: db.BlockReuseNeedsPut, StorageClass: "hot"})

	expectedSHA1 := sha1.Sum([]byte("hello"))
	expectedBlockID := hex.EncodeToString(expectedSHA1[:])

	fileID, err := run()
	if err != nil {
		t.Fatalf("finalizeUploadStreaming error = %v, want nil", err)
	}
	if fileID != expectedBlockID {
		t.Fatalf("fileID = %s, want %s", fileID, expectedBlockID)
	}
	if got := legacyPuts.Load(); got != 0 {
		t.Errorf("legacy Exists+PUT (HEAD) calls = %d, want 0 (needs_put must use the direct PUT)", got)
	}
	if got := directPuts.Load(); got != 1 {
		t.Errorf("direct PUT calls = %d, want 1", got)
	}
	if got := reusableChecks.Load(); got != 0 {
		t.Errorf("reusable canonical checks = %d, want 0", got)
	}
	if got := registerCalls.Load(); got != 1 {
		t.Errorf("register ref/mapping calls = %d, want 1", got)
	}
}

// TestFinalizeUploadBlockMetadataPermitDoesNotBlockS3Put guards the lower-level
// materialization helper against accidentally re-wrapping the S3 PUT inside the
// metadata permit.
func TestFinalizeUploadBlockMetadataPermitDoesNotBlockS3Put(t *testing.T) {
	// Hold the single metadata permit up front — no defer, released explicitly below.
	releaseHeld, err := acquireFinalizeUploadBlockMetadataPermit(context.Background())
	if err != nil {
		t.Fatalf("failed to pre-acquire metadata permit: %v", err)
	}

	originalPut := putUploadedBlockAutoFn
	putStarted := make(chan struct{})
	putUploadedBlockAutoFn = func(_ context.Context, _ *storage.BlockStore, _ string, _ []byte) (string, error) {
		close(putStarted)
		return "key", nil
	}
	t.Cleanup(func() { putUploadedBlockAutoFn = originalPut })

	originalRegister := registerUploadedBlockAndMappingForUploadFn
	registerUploadedBlockAndMappingForUploadFn = func(_ *db.DB, _, _, _, _ string, _ int, _, _, _ string) error {
		return nil
	}
	t.Cleanup(func() { registerUploadedBlockAndMappingForUploadFn = originalRegister })

	done := make(chan error, 1)
	go func() {
		done <- retrySeafHTTPBlockMaterialization("test", "sha256-block",
			func() error {
				_, putErr := putUploadedBlockAutoFn(context.Background(), nil, "sha256-block", []byte("data"))
				return putErr
			},
			func() error {
				rel, permitErr := acquireFinalizeUploadBlockMetadataPermit(context.Background())
				if permitErr != nil {
					return permitErr
				}
				defer rel()
				return registerUploadedBlockAndMappingForUploadFn(nil, "", "", "", "", 0, "", "", "")
			},
			nil,
		)
	}()

	// S3 PUT must complete while the permit is held externally.
	select {
	case <-putStarted:
	case <-time.After(500 * time.Millisecond):
		releaseHeld() // release before Fatal so other tests can acquire the permit
		t.Fatal("S3 PUT blocked while metadata permit was held; permit must only guard Cassandra write")
	}

	// Release the pre-held permit so the goroutine's LWT can proceed.
	// Single explicit release — no defer to avoid double-release deadlock.
	releaseHeld()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("retrySeafHTTPBlockMaterialization returned unexpected error: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("retrySeafHTTPBlockMaterialization did not complete after the permit was released")
	}
}

// TestFinalizeUploadS3PutPrecedesPermitRelease verifies the lower-level helper
// sequencing invariant: the S3 PUT (store callback) is invoked before the
// metadata permit is released (end of materialize callback).
func TestFinalizeUploadS3PutPrecedesPermitRelease(t *testing.T) {
	var seq atomic.Int64
	var putSeq, permitReleaseSeq int64

	originalPut := putUploadedBlockAutoFn
	putUploadedBlockAutoFn = func(_ context.Context, _ *storage.BlockStore, _ string, _ []byte) (string, error) {
		putSeq = seq.Add(1)
		return "key", nil
	}
	t.Cleanup(func() { putUploadedBlockAutoFn = originalPut })

	originalRegister := registerUploadedBlockAndMappingForUploadFn
	registerUploadedBlockAndMappingForUploadFn = func(_ *db.DB, _, _, _, _ string, _ int, _, _, _ string) error {
		return nil
	}
	t.Cleanup(func() { registerUploadedBlockAndMappingForUploadFn = originalRegister })

	// Pre-hold the single permit: the goroutine's materialize callback must block
	// until we release it, proving the store callback does not need the permit.
	releaseHeld, err := acquireFinalizeUploadBlockMetadataPermit(context.Background())
	if err != nil {
		t.Fatalf("pre-acquire failed: %v", err)
	}

	putInvoked := make(chan struct{})
	done := make(chan error, 1)

	go func() {
		done <- retrySeafHTTPBlockMaterialization("test", "sha256-block",
			func() error {
				_, putErr := putUploadedBlockAutoFn(context.Background(), nil, "sha256-block", []byte("data"))
				close(putInvoked)
				return putErr
			},
			func() error {
				rel, permitErr := acquireFinalizeUploadBlockMetadataPermit(context.Background())
				if permitErr != nil {
					return permitErr
				}
				defer func() {
					permitReleaseSeq = seq.Add(1)
					rel()
				}()
				return registerUploadedBlockAndMappingForUploadFn(nil, "", "", "", "", 0, "", "", "")
			},
			nil,
		)
	}()

	// PUT must run while the permit is still held externally.
	select {
	case <-putInvoked:
	case <-time.After(500 * time.Millisecond):
		releaseHeld()
		t.Fatal("S3 PUT was not invoked while metadata permit was pre-held")
	}

	// Allow materialize to proceed and record its permit-release sequence number.
	releaseHeld()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("retrySeafHTTPBlockMaterialization did not complete after permit release")
	}

	if putSeq >= permitReleaseSeq {
		t.Errorf("S3 PUT (seq=%d) must precede permit release (seq=%d)", putSeq, permitReleaseSeq)
	}
}

func TestChunkUploadQuotaPrecheckCacheMatchesMetadata(t *testing.T) {
	cm, _ := newTestChunkManager(t)

	upload, err := cm.GetOrCreateUpload("token1", "test.bin", "/docs", 10)
	if err != nil {
		t.Fatalf("GetOrCreateUpload failed: %v", err)
	}
	defer func() {
		upload.Cleanup()
		cm.CleanupUpload("token1", "test.bin")
	}()

	if upload.HasQuotaPrecheck("/docs", 10, true) {
		t.Fatal("new upload should not start with a cached quota precheck")
	}

	upload.MarkQuotaPrecheck("/docs", 10, true)

	if !upload.HasQuotaPrecheck("/docs", 10, true) {
		t.Fatal("matching chunk metadata should hit the cached quota precheck")
	}
	if upload.HasQuotaPrecheck("/other", 10, true) {
		t.Fatal("different parent dir must not reuse cached quota precheck")
	}
	if upload.HasQuotaPrecheck("/docs", 11, true) {
		t.Fatal("different total size must not reuse cached quota precheck")
	}
	if upload.HasQuotaPrecheck("/docs", 10, false) {
		t.Fatal("different replace mode must not reuse cached quota precheck")
	}
	if got := cm.GetUpload("token1", "test.bin"); got != upload {
		t.Fatal("GetUpload should return the tracked upload instance")
	}
	if got := cm.GetUploadByIdentity("token1", "", "/docs", "test.bin", 10); got != upload {
		t.Fatal("GetUploadByIdentity should return the tracked upload instance")
	}
	if got := cm.GetUpload("token1", "missing.bin"); got != nil {
		t.Fatal("GetUpload should return nil for unknown uploads")
	}
}

// ============================================================================
// TokenManager Concurrent Access Tests
// ============================================================================

func TestTokenManagerConcurrentAccess(t *testing.T) {
	tm := NewTokenManager(1 * time.Hour)
	done := make(chan struct{})

	// Concurrent token creation and deletion
	for i := 0; i < 50; i++ {
		go func() {
			tokenStr, _ := tm.CreateUploadToken("org", "repo", "/", "user")
			tm.GetToken(tokenStr, TokenTypeUpload)
			tm.DeleteToken(tokenStr)
			done <- struct{}{}
		}()
	}

	for i := 0; i < 50; i++ {
		<-done
	}
}

func TestRegisterSeafHTTPRoutes(t *testing.T) {
	tokenStore := NewMockTokenStore()
	handler := NewSeafHTTPHandler(nil, nil, nil, tokenStore, nil, nil)

	r := gin.New()
	handler.RegisterSeafHTTPRoutes(r)

	// Test that routes are registered by checking they don't 404
	tests := []struct {
		method string
		path   string
	}{
		{"POST", "/seafhttp/upload-api/test-token"},
		{"GET", "/seafhttp/files/test-token/file.txt"},
		{"GET", "/seafhttp/zip/test-token"},
	}

	for _, tt := range tests {
		req, _ := http.NewRequest(tt.method, tt.path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		// Should not be 404 (route exists, just may fail auth)
		if w.Code == http.StatusNotFound {
			t.Errorf("%s %s returned 404, route not registered", tt.method, tt.path)
		}
	}
}

func TestRegisterSeafHTTPRoutesZipRateLimit(t *testing.T) {
	tokenStore := NewMockTokenStore()
	handler := NewSeafHTTPHandler(nil, nil, nil, tokenStore, nil, nil)

	r := gin.New()
	rl := middleware.NewRateLimiter(rate.Every(time.Hour), 1)
	defer rl.Stop()
	handler.RegisterSeafHTTPRoutes(r, rl.Limit())

	req1 := httptest.NewRequest(http.MethodGet, "/seafhttp/zip/test-token", nil)
	req1.RemoteAddr = "198.51.100.10:12345"
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)
	if w1.Code != http.StatusUnauthorized {
		t.Fatalf("first status = %d, want %d", w1.Code, http.StatusUnauthorized)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/seafhttp/zip/test-token", nil)
	req2.RemoteAddr = "198.51.100.10:12345"
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d, want %d", w2.Code, http.StatusTooManyRequests)
	}
}
