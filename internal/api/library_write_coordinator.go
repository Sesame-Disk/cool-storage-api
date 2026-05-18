package api

import "sync"

// LibraryWriteCoordinator serializes the metadata phase of concurrent uploads
// to the same library. S3 block uploads happen fully outside the lock; only the
// critical section (read HEAD → build tree → insert commit → update HEAD) is
// serialized. This prevents the last-writer-wins HEAD overwrite that silently
// orphans commits when multiple uploads finalize at the same time.
type LibraryWriteCoordinator struct {
	mu    sync.Mutex
	locks map[string]*libLock
}

type libLock struct {
	mu   sync.Mutex
	refs int
}

func newLibraryWriteCoordinator() *LibraryWriteCoordinator {
	return &LibraryWriteCoordinator{locks: make(map[string]*libLock)}
}

// Acquire returns a release func that must be deferred by the caller.
// Multiple goroutines targeting the same (orgID, repoID) pair will queue here;
// goroutines for different libraries proceed concurrently.
func (c *LibraryWriteCoordinator) Acquire(orgID, repoID string) (release func()) {
	key := orgID + "\x00" + repoID

	c.mu.Lock()
	e, ok := c.locks[key]
	if !ok {
		e = &libLock{}
		c.locks[key] = e
	}
	e.refs++
	c.mu.Unlock()

	e.mu.Lock()

	return func() {
		e.mu.Unlock()

		c.mu.Lock()
		e.refs--
		if e.refs == 0 {
			delete(c.locks, key)
		}
		c.mu.Unlock()
	}
}
