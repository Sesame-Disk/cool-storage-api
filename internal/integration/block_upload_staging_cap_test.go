//go:build integration

package integration

import (
	"errors"
	"testing"

	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

// These DB-level integration tests exercise the staging caps
// (docs/WEB-BLOCK-UPLOAD.md item 1) deterministically: a fresh random (org, user)
// per test isolates the per-user slot partition from other tests, and the cap is
// passed explicitly rather than read from the shared server config.

func newStagingCapIDs() (orgID, userID, repoID string) {
	return gocql.TimeUUID().String(), gocql.TimeUUID().String(), gocql.TimeUUID().String()
}

func TestBlockUploadSessionSlotCapAtomicAndFreedOnCleanup(t *testing.T) {
	db := shareProjectionDBForTest(t)
	orgID, userID, repoID := newStagingCapIDs()
	const cap = 3

	var sessions []dbpkg.BlockUploadSession
	for i := 0; i < cap; i++ {
		s, err := db.CreateAdmittedBlockUploadSession(orgID, userID, repoID, "/", 0, cap)
		if err != nil {
			t.Fatalf("create session %d: %v", i, err)
		}
		if s.Slot < 0 || s.Slot >= cap {
			t.Fatalf("session %d claimed slot %d, want 0..%d", i, s.Slot, cap-1)
		}
		sessions = append(sessions, s)
	}

	// The (cap+1)-th create must be rejected — every slot is taken.
	if _, err := db.CreateAdmittedBlockUploadSession(orgID, userID, repoID, "/", 0, cap); !errors.Is(err, dbpkg.ErrBlockUploadSessionSlotsExhausted) {
		t.Fatalf("expected ErrBlockUploadSessionSlotsExhausted, got %v", err)
	}

	// Committing/cleaning one session frees its slot; a new create then succeeds.
	if err := db.CleanupCommittedBlockUploadSessionCaps(sessions[0]); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if _, err := db.CreateAdmittedBlockUploadSession(orgID, userID, repoID, "/", 0, cap); err != nil {
		t.Fatalf("create after freeing a slot should succeed, got %v", err)
	}
}

func TestBlockUploadSessionSlotCapDisabled(t *testing.T) {
	db := shareProjectionDBForTest(t)
	orgID, userID, repoID := newStagingCapIDs()

	// cap <= 0 disables the per-user cap: many creates all succeed with slot = -1.
	for i := 0; i < 5; i++ {
		s, err := db.CreateAdmittedBlockUploadSession(orgID, userID, repoID, "/", 0, 0)
		if err != nil {
			t.Fatalf("create %d with disabled cap: %v", i, err)
		}
		if s.Slot != -1 {
			t.Fatalf("disabled cap should store slot=-1, got %d", s.Slot)
		}
	}
}

func TestBlockUploadStagedBlockLedgerIdempotentCountAndReserve(t *testing.T) {
	db := shareProjectionDBForTest(t)
	sessionID := gocql.TimeUUID().String()
	blockID := "abc123"
	bucket := dbpkg.StagedBlockBucket(blockID)

	// Empty ledger reads zero.
	if n, err := db.CountSessionStagedBlocksInBucket(sessionID, bucket, 10); err != nil || n != 0 {
		t.Fatalf("empty bucket count = %d err = %v, want 0/nil", n, err)
	}

	// Reserving the same block twice is idempotent (same PK) — count stays 1.
	for i := 0; i < 2; i++ {
		if err := db.ReserveSessionStagedBlock(sessionID, bucket, blockID, 8*1024*1024); err != nil {
			t.Fatalf("reserve %d: %v", i, err)
		}
	}
	if n, err := db.CountSessionStagedBlocksInBucket(sessionID, bucket, 10); err != nil || n != 1 {
		t.Fatalf("after double reserve count = %d err = %v, want 1/nil (idempotent)", n, err)
	}

	// A different block in the same bucket increments the count.
	other := "def456"
	if dbpkg.StagedBlockBucket(other) == bucket {
		if err := db.ReserveSessionStagedBlock(sessionID, bucket, other, 1024); err != nil {
			t.Fatalf("reserve other: %v", err)
		}
		if n, _ := db.CountSessionStagedBlocksInBucket(sessionID, bucket, 10); n != 2 {
			t.Fatalf("count after a distinct block = %d, want 2", n)
		}
	}
}

func TestBlockUploadSessionStoresExpectedSize(t *testing.T) {
	db := shareProjectionDBForTest(t)
	orgID, userID, repoID := newStagingCapIDs()
	const declared = int64(123456789)

	s, err := db.CreateAdmittedBlockUploadSession(orgID, userID, repoID, "/", declared, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, ok, err := db.GetBlockUploadSession(s.SessionID)
	if err != nil || !ok {
		t.Fatalf("get session: ok=%v err=%v", ok, err)
	}
	if got.ExpectedSize != declared {
		t.Fatalf("expected_size = %d, want %d", got.ExpectedSize, declared)
	}
}
