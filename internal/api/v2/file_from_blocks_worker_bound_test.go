package v2

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
)

// maxManifestBlocks is the supported manifest ceiling recorded in
// ISSUE-FILEFROMBLOCKS-WORKER-FANOUT-01.
const maxManifestBlocks = 131072

// waitForWorkerWave blocks until the admission-bounded first wave is active.
func waitForWorkerWave(t *testing.T, started <-chan struct{}) {
	t.Helper()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for range blockVerifyConcurrency {
		select {
		case <-started:
		case <-timeout.C:
			t.Fatal("worker wave did not start")
		}
	}
}

// A semaphore acquired inside already-created goroutines bounds active work
// but leaves roughly 131k blocked goroutines for a maximum-size manifest.
func TestVerifyManifestBlocksBoundsGoroutineFanout(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, blockVerifyConcurrency)
	var once sync.Once
	var calls atomic.Int32

	orig := classifyBlockForCommitFn
	t.Cleanup(func() { classifyBlockForCommitFn = orig })
	classifyBlockForCommitFn = func(*FileHandler, string, string, string, int64, bool) (int, string, blockCommitLivenessProvenance, db.BlockPhysicalLocation, error) {
		if calls.Add(1) <= blockVerifyConcurrency {
			started <- struct{}{}
		}
		<-release
		return blockStatusNeedsUpload, "", blockCommitLivenessNone, db.BlockPhysicalLocation{}, nil
	}

	hashes := make([]string, maxManifestBlocks)
	sizeByHash := make(map[string]int64, maxManifestBlocks)
	existsMap := make(map[string]bool, maxManifestBlocks)
	for i := range hashes {
		hashes[i] = hex64(i)
		existsMap[hashes[i]] = true
	}

	baseline := runtime.NumGoroutine()
	done := make(chan error, 1)
	completed := make(chan struct{})
	go func() {
		defer close(completed)
		_, _, _, err := (&FileHandler{}).verifyManifestBlocks(context.Background(), "org", "up:session", hashes, sizeByHash, existsMap)
		done <- err
	}()
	defer func() {
		once.Do(func() { close(release) })
		<-completed
	}()

	waitForWorkerWave(t, started)
	// Give a broken fan-out loop time to create its queued workers.
	time.Sleep(50 * time.Millisecond)
	if delta := runtime.NumGoroutine() - baseline; delta > blockVerifyConcurrency+32 {
		t.Errorf("goroutine fan-out = %d; expected a bounded worker wave", delta)
	}
	once.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatalf("verifyManifestBlocks: %v", err)
	}
	if calls.Load() != maxManifestBlocks {
		t.Fatalf("classified %d blocks, want %d", calls.Load(), maxManifestBlocks)
	}
}

func TestEnsureCommitBlockOwnLivenessBoundsGoroutineFanout(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, blockVerifyConcurrency)
	var once sync.Once
	var calls atomic.Int32

	orig := registerUploadedBlockAddProvisionalRefFn
	t.Cleanup(func() { registerUploadedBlockAddProvisionalRefFn = orig })
	registerUploadedBlockAddProvisionalRefFn = func(*FSHelper, string, string, string, string, string, time.Time) error {
		if calls.Add(1) <= blockVerifyConcurrency {
			started <- struct{}{}
		}
		<-release
		return nil
	}

	blocks := make([]commitBlockPlacement, maxManifestBlocks)
	for i := range blocks {
		blocks[i] = commitBlockPlacement{blockID: hex64(i), storageClass: "hot", storageKey: "key"}
	}

	baseline := runtime.NumGoroutine()
	done := make(chan error, 1)
	completed := make(chan struct{})
	go func() {
		defer close(completed)
		done <- (&FileHandler{}).ensureCommitBlockOwnLiveness(context.Background(), "org", "repo", "session", blocks)
	}()
	defer func() {
		once.Do(func() { close(release) })
		<-completed
	}()

	waitForWorkerWave(t, started)
	time.Sleep(50 * time.Millisecond)
	if delta := runtime.NumGoroutine() - baseline; delta > blockVerifyConcurrency+32 {
		t.Errorf("goroutine fan-out = %d; expected a bounded worker wave", delta)
	}
	once.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatalf("ensureCommitBlockOwnLiveness: %v", err)
	}
	if calls.Load() != maxManifestBlocks {
		t.Fatalf("wrote own liveness for %d blocks, want %d", calls.Load(), maxManifestBlocks)
	}
}

func TestEnsureCommitBlockOwnLivenessCanceledContextSpawnsNoWorkers(t *testing.T) {
	var calls atomic.Int32
	orig := registerUploadedBlockAddProvisionalRefFn
	t.Cleanup(func() { registerUploadedBlockAddProvisionalRefFn = orig })
	registerUploadedBlockAddProvisionalRefFn = func(*FSHelper, string, string, string, string, string, time.Time) error {
		calls.Add(1)
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	blocks := []commitBlockPlacement{{blockID: "b1", storageClass: "hot", storageKey: "k1"}}
	err := (&FileHandler{}).ensureCommitBlockOwnLiveness(ctx, "org", "repo", "session", blocks)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("canceled request performed %d own-liveness writes", calls.Load())
	}
}

func TestVerifyManifestBlocksPropagatesClassificationError(t *testing.T) {
	sentinel := errors.New("classification unavailable")
	orig := classifyBlockForCommitFn
	t.Cleanup(func() { classifyBlockForCommitFn = orig })
	classifyBlockForCommitFn = func(*FileHandler, string, string, string, int64, bool) (int, string, blockCommitLivenessProvenance, db.BlockPhysicalLocation, error) {
		return 0, "", blockCommitLivenessNone, db.BlockPhysicalLocation{}, sentinel
	}

	_, _, _, err := (&FileHandler{}).verifyManifestBlocks(context.Background(), "org", "up:session", []string{hex64(1)}, nil, nil)
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the classification error propagated verbatim", err)
	}
}
