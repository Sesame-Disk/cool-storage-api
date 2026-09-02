//go:build integration

package v2

import (
	"sync"
	"time"
)

type fileFromBlocksPublicationBarriers struct {
	repoID           string
	afterVerified    func()
	afterBorrowedPin func()
	afterStaged      func()
	beforeHead       func() error
}

var (
	fileFromBlocksBarrierMu                    sync.Mutex
	fileFromBlocksPublicationBarriersInstalled *fileFromBlocksPublicationBarriers
)

// SetFileFromBlocksPublicationBarriersForTest installs process-local publication
// barriers for in-process CreateFileFromBlocks characterization. Hooks run only
// when the request repoID matches. The returned restore function must run from
// t.Cleanup. HTTP commits in other processes are unaffected.
func SetFileFromBlocksPublicationBarriersForTest(repoID string, afterVerified, afterBorrowedPin, afterStaged func(), beforeHead func() error) func() {
	fileFromBlocksBarrierMu.Lock()
	previous := fileFromBlocksPublicationBarriersInstalled
	fileFromBlocksPublicationBarriersInstalled = &fileFromBlocksPublicationBarriers{
		repoID:           repoID,
		afterVerified:    afterVerified,
		afterBorrowedPin: afterBorrowedPin,
		afterStaged:      afterStaged,
		beforeHead:       beforeHead,
	}
	fileFromBlocksBarrierMu.Unlock()
	return func() {
		fileFromBlocksBarrierMu.Lock()
		fileFromBlocksPublicationBarriersInstalled = previous
		fileFromBlocksBarrierMu.Unlock()
	}
}

func fileFromBlocksPublicationHooksForRepo(repoID string) *fileFromBlocksPublicationBarriers {
	fileFromBlocksBarrierMu.Lock()
	hooks := fileFromBlocksPublicationBarriersInstalled
	fileFromBlocksBarrierMu.Unlock()
	if hooks == nil || hooks.repoID == "" || hooks.repoID != repoID {
		return nil
	}
	return hooks
}

func fileFromBlocksAfterVerifiedBarrier(repoID string) {
	hooks := fileFromBlocksPublicationHooksForRepo(repoID)
	if hooks == nil || hooks.afterVerified == nil {
		return
	}
	hooks.afterVerified()
}

// SetFileFromBlocksOwnLivenessFailureForTest makes BorrowedFS own-liveness
// writes fail while the returned restore function is installed. It exists only
// to prove that publication cannot proceed when the safety pin is unavailable.
func SetFileFromBlocksOwnLivenessFailureForTest(err error) func() {
	previous := registerUploadedBlockAddProvisionalRefFn
	registerUploadedBlockAddProvisionalRefFn = func(*FSHelper, string, string, string, string, string, time.Time) error {
		return err
	}
	return func() { registerUploadedBlockAddProvisionalRefFn = previous }
}

func fileFromBlocksAfterBorrowedLivenessBarrier(repoID string) {
	hooks := fileFromBlocksPublicationHooksForRepo(repoID)
	if hooks == nil || hooks.afterBorrowedPin == nil {
		return
	}
	hooks.afterBorrowedPin()
}

func fileFromBlocksAfterStagedBarrier(repoID string) {
	hooks := fileFromBlocksPublicationHooksForRepo(repoID)
	if hooks == nil || hooks.afterStaged == nil {
		return
	}
	hooks.afterStaged()
}

func fileFromBlocksBeforeHeadBarrier(repoID string) error {
	hooks := fileFromBlocksPublicationHooksForRepo(repoID)
	if hooks == nil || hooks.beforeHead == nil {
		return nil
	}
	return hooks.beforeHead()
}
