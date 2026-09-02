//go:build integration

package v2

import "sync"

type fileFromBlocksPublicationBarriers struct {
	repoID        string
	afterVerified func()
	afterStaged   func()
	beforeHead    func() error
}

var (
	fileFromBlocksBarrierMu                    sync.Mutex
	fileFromBlocksPublicationBarriersInstalled *fileFromBlocksPublicationBarriers
)

// SetFileFromBlocksPublicationBarriersForTest installs process-local publication
// barriers for in-process CreateFileFromBlocks characterization. Hooks run only
// when the request repoID matches. The returned restore function must run from
// t.Cleanup. HTTP commits in other processes are unaffected.
func SetFileFromBlocksPublicationBarriersForTest(repoID string, afterVerified, afterStaged func(), beforeHead func() error) func() {
	fileFromBlocksBarrierMu.Lock()
	previous := fileFromBlocksPublicationBarriersInstalled
	fileFromBlocksPublicationBarriersInstalled = &fileFromBlocksPublicationBarriers{
		repoID:        repoID,
		afterVerified: afterVerified,
		afterStaged:   afterStaged,
		beforeHead:    beforeHead,
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
