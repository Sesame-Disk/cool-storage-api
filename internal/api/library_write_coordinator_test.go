package api

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestCoordinatorSerializesPerLibrary verifies that two goroutines targeting the
// same library cannot hold the lock simultaneously, while goroutines for
// different libraries proceed concurrently.
func TestCoordinatorSerializesPerLibrary(t *testing.T) {
	c := newLibraryWriteCoordinator()

	const org = "org1"
	const repo = "repo1"

	var inCritical atomic.Int32
	var overlap atomic.Bool
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release := c.Acquire(org, repo)
			defer release()

			// Check for overlap inside the critical section.
			if inCritical.Add(1) > 1 {
				overlap.Store(true)
			}
			time.Sleep(time.Millisecond) // hold long enough to create overlap if broken
			inCritical.Add(-1)
		}()
	}

	wg.Wait()

	if overlap.Load() {
		t.Fatal("two goroutines were inside the critical section simultaneously for the same library")
	}
}

// TestCoordinatorDifferentLibrariesAreConcurrent verifies that two goroutines
// targeting different libraries do not block each other.
func TestCoordinatorDifferentLibrariesAreConcurrent(t *testing.T) {
	c := newLibraryWriteCoordinator()

	var started sync.WaitGroup
	started.Add(2)

	release1Ready := make(chan struct{})
	release2Ready := make(chan struct{})

	go func() {
		release := c.Acquire("org1", "repoA")
		started.Done()
		<-release1Ready
		release()
	}()

	go func() {
		release := c.Acquire("org1", "repoB")
		started.Done()
		<-release2Ready
		release()
	}()

	// Both goroutines must be able to acquire their locks at the same time.
	done := make(chan struct{})
	go func() {
		started.Wait()
		close(done)
	}()

	select {
	case <-done:
		// success — both held their locks concurrently
	case <-time.After(2 * time.Second):
		t.Fatal("different-library acquisitions blocked each other (deadlock or unexpected serialization)")
	}

	close(release1Ready)
	close(release2Ready)
}

// TestCoordinatorMapCleansUpAfterRelease verifies that the internal map does
// not leak entries after all goroutines have released.
func TestCoordinatorMapCleansUpAfterRelease(t *testing.T) {
	c := newLibraryWriteCoordinator()

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release := c.Acquire("orgX", "repoY")
			release()
		}()
	}
	wg.Wait()

	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.locks) != 0 {
		t.Fatalf("coordinator lock map leaked %d entries after all releases", len(c.locks))
	}
}

// TestCoordinatorAcquireIsReentrantSafe verifies that the same goroutine can
// acquire locks for many different libraries in sequence without deadlocking.
func TestCoordinatorAcquireIsReentrantSafe(t *testing.T) {
	c := newLibraryWriteCoordinator()

	for i := 0; i < 100; i++ {
		release := c.Acquire("org", "repo")
		release()
	}
}
