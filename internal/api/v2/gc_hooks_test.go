package v2

import (
	"testing"
	"time"
)

// mockGCEnqueuer implements GCEnqueuer for testing
type mockGCEnqueuer struct {
	calls []gcEnqueueCall
}

type gcEnqueueCall struct {
	orgID        string
	blockIDs     []string
	storageClass string
}

func (m *mockGCEnqueuer) EnqueueBlocks(orgID string, blockIDs []string, storageClass string) {
	m.calls = append(m.calls, gcEnqueueCall{orgID, blockIDs, storageClass})
}

// mockLibraryGCEnqueuer implements LibraryGCEnqueuer for testing
type mockLibraryGCEnqueuer struct {
	calls []libGCEnqueueCall
}

type libGCEnqueueCall struct {
	orgID                 string
	libraryID             string
	blockRepresentationID string
	storageClass          string
	deletedAt             time.Time
}

func (m *mockLibraryGCEnqueuer) EnqueueLibraryCascade(orgID, libraryID, blockRepresentationID, storageClass string, deletedAt time.Time) {
	m.calls = append(m.calls, libGCEnqueueCall{orgID, libraryID, blockRepresentationID, storageClass, deletedAt})
}

// mockCommitGCEnqueuer implements CommitGCEnqueuer for testing
type mockCommitGCEnqueuer struct {
	calls []commitGCEnqueueCall
}

type commitGCEnqueueCall struct {
	orgID     string
	libraryID string
	commitIDs []string
}

func (m *mockCommitGCEnqueuer) EnqueueCommits(orgID, libraryID string, commitIDs []string) {
	m.calls = append(m.calls, commitGCEnqueueCall{orgID, libraryID, commitIDs})
}

func TestSetGCHooks(t *testing.T) {
	// Reset hooks at end of test
	defer SetGCHooks(nil, nil, nil)

	blockEnq := &mockGCEnqueuer{}
	libEnq := &mockLibraryGCEnqueuer{}
	commitEnq := &mockCommitGCEnqueuer{}

	SetGCHooks(blockEnq, libEnq, commitEnq)

	got := getBlockEnqueuer()
	if got != blockEnq {
		t.Error("getBlockEnqueuer() should return the set enqueuer")
	}

	gotLib := getLibraryEnqueuer()
	if gotLib != libEnq {
		t.Error("getLibraryEnqueuer() should return the set enqueuer")
	}

	gotCommit := getCommitEnqueuer()
	if gotCommit != commitEnq {
		t.Error("getCommitEnqueuer() should return the set enqueuer")
	}
}

func TestGetBlockEnqueuerFunc(t *testing.T) {
	defer SetGCHooks(nil, nil, nil)

	blockEnq := &mockGCEnqueuer{}
	SetGCHooks(blockEnq, nil, nil)

	got := GetBlockEnqueuerFunc()
	if got != blockEnq {
		t.Error("GetBlockEnqueuerFunc() should return the set enqueuer")
	}
}

func TestGCHooks_NilByDefault(t *testing.T) {
	defer SetGCHooks(nil, nil, nil)

	// Reset to nil
	SetGCHooks(nil, nil, nil)

	if got := getBlockEnqueuer(); got != nil {
		t.Error("getBlockEnqueuer() should be nil when not set")
	}

	if got := getLibraryEnqueuer(); got != nil {
		t.Error("getLibraryEnqueuer() should be nil when not set")
	}

	if got := getCommitEnqueuer(); got != nil {
		t.Error("getCommitEnqueuer() should be nil when not set")
	}
}

func TestGCHooks_ConcurrentAccess(t *testing.T) {
	defer SetGCHooks(nil, nil, nil)

	done := make(chan struct{})

	// Concurrent reads and writes
	for i := 0; i < 50; i++ {
		go func() {
			SetGCHooks(&mockGCEnqueuer{}, &mockLibraryGCEnqueuer{}, &mockCommitGCEnqueuer{})
			done <- struct{}{}
		}()
		go func() {
			_ = getBlockEnqueuer()
			_ = getLibraryEnqueuer()
			_ = getCommitEnqueuer()
			done <- struct{}{}
		}()
	}

	for i := 0; i < 100; i++ {
		<-done
	}
}

func TestGCEnqueuer_Interface(t *testing.T) {
	// Verify the mock satisfies the interface at compile time
	var _ GCEnqueuer = (*mockGCEnqueuer)(nil)
	var _ LibraryGCEnqueuer = (*mockLibraryGCEnqueuer)(nil)
	var _ CommitGCEnqueuer = (*mockCommitGCEnqueuer)(nil)
}

func TestMockGCEnqueuer_RecordsCalls(t *testing.T) {
	m := &mockGCEnqueuer{}

	m.EnqueueBlocks("org-1", []string{"block-a", "block-b"}, "hot")
	m.EnqueueBlocks("org-2", []string{"block-c"}, "cold")

	if len(m.calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(m.calls))
	}

	if m.calls[0].orgID != "org-1" {
		t.Errorf("call[0].orgID = %q, want org-1", m.calls[0].orgID)
	}
	if len(m.calls[0].blockIDs) != 2 {
		t.Errorf("call[0].blockIDs = %d items, want 2", len(m.calls[0].blockIDs))
	}
	if m.calls[1].storageClass != "cold" {
		t.Errorf("call[1].storageClass = %q, want cold", m.calls[1].storageClass)
	}
}

func TestMockLibraryGCEnqueuer_RecordsCalls(t *testing.T) {
	m := &mockLibraryGCEnqueuer{}

	m.EnqueueLibraryCascade("org-1", "lib-a", "plain:v1", "hot", time.Now())

	if len(m.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(m.calls))
	}

	if m.calls[0].libraryID != "lib-a" {
		t.Errorf("call[0].libraryID = %q, want lib-a", m.calls[0].libraryID)
	}
}
