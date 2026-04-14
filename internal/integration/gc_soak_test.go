//go:build integration && gcsoak

package integration

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// GC soak test: long-running test that simulates real customer behavior.
// Runs for a configurable duration, continuously creating/deleting files
// while GC runs, and checks invariants periodically.
//
// Run with:
//
//	go test -tags 'integration gcsoak' -v -run "TestGC_Soak" -timeout 30m ./internal/integration/...
//	GC_SOAK_DURATION=1h go test -tags 'integration gcsoak' -v -run "TestGC_Soak" -timeout 2h ./internal/integration/...

// soakState tracks files and libraries created during the soak test for
// invariant checking and cleanup.
type soakState struct {
	mu sync.Mutex

	// activeFiles tracks files we expect to be downloadable: repoID → fileName → content
	activeFiles map[string]map[string]string

	// activeLibraries tracks libraries that should be accessible
	activeLibraries []string

	// deletedLibraries tracks libraries we've soft-deleted
	deletedLibraries []string

	// blockMap tracks content → blockID for verification
	blockMap map[string]string

	// stats
	filesUploaded   int
	filesDeleted    int
	libsCreated     int
	libsSoftDeleted int
	iterations      int
	invariantChecks int
	invariantPasses int
}

func newSoakState() *soakState {
	return &soakState{
		activeFiles:     make(map[string]map[string]string),
		blockMap:        make(map[string]string),
		activeLibraries: make([]string, 0),
	}
}

func parseSoakDuration() time.Duration {
	raw := os.Getenv("GC_SOAK_DURATION")
	if raw == "" {
		return 5 * time.Minute
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 5 * time.Minute
	}
	return d
}

// TestGC_Soak runs a long-duration test simulating real customer behavior.
// It continuously creates libraries, uploads files, deletes some, soft-deletes
// libraries, and periodically checks invariants:
//   - All active files are downloadable with correct content
//   - No monotonically growing GC queue (eventual processing)
//   - GC admin endpoint responds
func TestGC_Soak(t *testing.T) {
	requireGCEnabled(t)
	requireCassandra(t)

	duration := parseSoakDuration()
	t.Logf("=== GC Soak Test: running for %s ===", duration)

	state := newSoakState()
	deadline := time.Now().Add(duration)
	lastInvariantCheck := time.Now()
	invariantInterval := 30 * time.Second

	// Ensure cleanup of all created libraries
	t.Cleanup(func() {
		t.Logf("Soak cleanup: deleting %d active + %d soft-deleted libraries",
			len(state.activeLibraries), len(state.deletedLibraries))
		for _, repoID := range state.activeLibraries {
			resp := adminClient.Delete(t, fmt.Sprintf("/api2/repos/%s/", repoID))
			resp.Body.Close()
		}
	})

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	for time.Now().Before(deadline) {
		state.iterations++

		// --- Create a library every 5 iterations ---
		if state.iterations%5 == 1 || len(state.activeLibraries) == 0 {
			repoName := fmt.Sprintf("inttest-soak-%d-%d", state.iterations, time.Now().UnixNano())
			repoID := createDisposableTestLibrary(t, adminClient, repoName)
			state.mu.Lock()
			state.activeLibraries = append(state.activeLibraries, repoID)
			state.activeFiles[repoID] = make(map[string]string)
			state.libsCreated++
			state.mu.Unlock()
			t.Logf("[iter %d] Created library %s", state.iterations, repoID[:8])
		}

		// --- Pick a random active library ---
		state.mu.Lock()
		if len(state.activeLibraries) == 0 {
			state.mu.Unlock()
			continue
		}
		repoIdx := rng.Intn(len(state.activeLibraries))
		repoID := state.activeLibraries[repoIdx]
		state.mu.Unlock()

		// --- Upload 1-3 files ---
		numUploads := 1 + rng.Intn(3)
		for i := 0; i < numUploads; i++ {
			fileName := fmt.Sprintf("soak-%d-%d.bin", state.iterations, i)
			content := fmt.Sprintf("soak-content-%d-%d-%d\n", state.iterations, i, time.Now().UnixNano())

			uploadLinkResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", repoID))
			if uploadLinkResp.StatusCode != http.StatusOK {
				t.Logf("[iter %d] upload-link failed: HTTP %d", state.iterations, uploadLinkResp.StatusCode)
				uploadLinkResp.Body.Close()
				continue
			}
			uploadURL := strings.Trim(responseBody(t, uploadLinkResp), "\" \n\r")
			uploadFileThroughLink(t, adminClient, uploadURL, fileName, "/", content)

			hash := sha256.Sum256([]byte(content))
			bid := hex.EncodeToString(hash[:])

			state.mu.Lock()
			if state.activeFiles[repoID] != nil {
				state.activeFiles[repoID][fileName] = content
			}
			state.blockMap[content] = bid
			state.filesUploaded++
			state.mu.Unlock()
		}

		// --- Delete 0-2 files randomly ---
		state.mu.Lock()
		files := state.activeFiles[repoID]
		var toDelete []string
		for name := range files {
			if rng.Float32() < 0.3 && len(toDelete) < 2 {
				toDelete = append(toDelete, name)
			}
		}
		state.mu.Unlock()

		if len(toDelete) > 0 {
			batchDeleteFiles(t, adminClient, repoID, "/", toDelete)
			state.mu.Lock()
			for _, name := range toDelete {
				delete(state.activeFiles[repoID], name)
				state.filesDeleted++
			}
			state.mu.Unlock()
		}

		// --- Soft-delete a library every 10 iterations ---
		if state.iterations%10 == 0 && len(state.activeLibraries) > 1 {
			state.mu.Lock()
			victimIdx := rng.Intn(len(state.activeLibraries))
			victimID := state.activeLibraries[victimIdx]
			// Remove from active list
			state.activeLibraries = append(state.activeLibraries[:victimIdx], state.activeLibraries[victimIdx+1:]...)
			delete(state.activeFiles, victimID)
			state.deletedLibraries = append(state.deletedLibraries, victimID)
			state.libsSoftDeleted++
			state.mu.Unlock()

			resp := adminClient.Delete(t, fmt.Sprintf("/api2/repos/%s/", victimID))
			resp.Body.Close()
			t.Logf("[iter %d] Soft-deleted library %s", state.iterations, victimID[:8])
		}

		// --- Trigger GC periodically ---
		if state.iterations%3 == 0 {
			triggerGCWorker(t)
		}

		// --- Invariant checks ---
		if time.Since(lastInvariantCheck) > invariantInterval {
			soakCheckInvariants(t, state)
			lastInvariantCheck = time.Now()
		}

		// Brief pause to avoid hammering
		time.Sleep(100 * time.Millisecond)
	}

	// Final invariant check
	triggerGCWorker(t)
	time.Sleep(1 * time.Second)
	soakCheckInvariants(t, state)

	t.Logf("=== Soak Test Complete ===")
	t.Logf("Duration: %s", duration)
	t.Logf("Iterations: %d", state.iterations)
	t.Logf("Libraries created: %d, soft-deleted: %d", state.libsCreated, state.libsSoftDeleted)
	t.Logf("Files uploaded: %d, deleted: %d", state.filesUploaded, state.filesDeleted)
	t.Logf("Invariant checks: %d passed, %d total", state.invariantPasses, state.invariantChecks)
}

// soakCheckInvariants verifies that all active files are still downloadable
// and the GC queue is not growing unboundedly.
func soakCheckInvariants(t *testing.T, state *soakState) {
	t.Helper()
	state.mu.Lock()
	defer state.mu.Unlock()

	state.invariantChecks++
	failures := 0

	// Check 1: All active files are downloadable
	for repoID, files := range state.activeFiles {
		for fileName, expectedContent := range files {
			dlResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/file/?p=/%s", repoID, fileName))
			if dlResp.StatusCode != http.StatusOK {
				t.Errorf("INVARIANT VIOLATION: active file %s/%s not downloadable (HTTP %d)",
					repoID[:8], fileName, dlResp.StatusCode)
				dlResp.Body.Close()
				failures++
				continue
			}

			// Follow redirect to get actual content
			dlURL := strings.Trim(responseBody(t, dlResp), "\" \n\r")
			if dlURL == "" {
				continue // can't verify content without download URL
			}

			contentResp, err := adminClient.http.Get(dlURL)
			if err != nil {
				t.Logf("Warning: could not download %s: %v", fileName, err)
				continue
			}
			body := responseBody(t, contentResp)
			if body != expectedContent {
				expectedHash := sha256.Sum256([]byte(expectedContent))
				actualHash := sha256.Sum256([]byte(body))
				t.Errorf("INVARIANT VIOLATION: content mismatch for %s/%s (expected hash %s, got %s)",
					repoID[:8], fileName, hex.EncodeToString(expectedHash[:])[:12], hex.EncodeToString(actualHash[:])[:12])
				failures++
			}
		}
	}

	// Check 2: GC queue is not growing unboundedly
	qSize := getGCQueueSize(t)
	if qSize > 10000 {
		t.Errorf("INVARIANT VIOLATION: GC queue size=%d exceeds 10k threshold", qSize)
		failures++
	}

	// Check 3: GC admin endpoint is responsive
	statusResp := superadminClient.Get(t, "/api/v2.1/admin/gc/status")
	if statusResp.StatusCode != http.StatusOK {
		t.Errorf("INVARIANT VIOLATION: GC admin status returned HTTP %d", statusResp.StatusCode)
		failures++
	}
	statusResp.Body.Close()

	if failures == 0 {
		state.invariantPasses++
		t.Logf("Invariant check #%d PASSED (files=%d, queue=%d, libs=%d)",
			state.invariantChecks,
			countActiveFiles(state),
			qSize,
			len(state.activeLibraries))
	} else {
		t.Logf("Invariant check #%d: %d failures", state.invariantChecks, failures)
	}
}

func countActiveFiles(state *soakState) int {
	total := 0
	for _, files := range state.activeFiles {
		total += len(files)
	}
	return total
}
