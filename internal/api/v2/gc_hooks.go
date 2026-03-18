package v2

import "sync"

// gcHooks holds package-level GC hooks that are set once by the server.
// This avoids threading the GC service through every Register* function signature.
var gcHooks struct {
	mu              sync.RWMutex
	blockEnqueuer   GCEnqueuer
	libraryEnqueuer LibraryGCEnqueuer
	commitEnqueuer  CommitGCEnqueuer
}

// SetGCHooks sets the package-level GC hooks.
// Called once during server initialization.
func SetGCHooks(blockEnqueuer GCEnqueuer, libraryEnqueuer LibraryGCEnqueuer, commitEnqueuer CommitGCEnqueuer) {
	gcHooks.mu.Lock()
	defer gcHooks.mu.Unlock()
	gcHooks.blockEnqueuer = blockEnqueuer
	gcHooks.libraryEnqueuer = libraryEnqueuer
	gcHooks.commitEnqueuer = commitEnqueuer
}

// getBlockEnqueuer returns the global block GC enqueuer (may be nil).
func getBlockEnqueuer() GCEnqueuer {
	gcHooks.mu.RLock()
	defer gcHooks.mu.RUnlock()
	return gcHooks.blockEnqueuer
}

// getLibraryEnqueuer returns the global library GC enqueuer (may be nil).
func getLibraryEnqueuer() LibraryGCEnqueuer {
	gcHooks.mu.RLock()
	defer gcHooks.mu.RUnlock()
	return gcHooks.libraryEnqueuer
}

// getCommitEnqueuer returns the global commit GC enqueuer (may be nil).
func getCommitEnqueuer() CommitGCEnqueuer {
	gcHooks.mu.RLock()
	defer gcHooks.mu.RUnlock()
	return gcHooks.commitEnqueuer
}

// GetBlockEnqueuerFunc returns the global block GC enqueuer for use by external callers.
func GetBlockEnqueuerFunc() GCEnqueuer {
	return getBlockEnqueuer()
}
