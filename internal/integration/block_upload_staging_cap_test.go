//go:build integration

package integration

import (
	"errors"
	"fmt"
	"sync"
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

func testSessionAdmission(declared *int64) dbpkg.BlockUploadSessionAdmission {
	admission := dbpkg.BlockUploadSessionAdmission{
		BlockSizeBytes:    8 * 1024 * 1024,
		StagedBucketCount: 8,
		StagedBucketCap:   8,
	}
	if declared != nil {
		admission.ExpectedSize = *declared
		admission.ExpectedSizeDeclared = true
	}
	return admission
}

func TestBlockUploadSessionSlotCapAtomicAndFreedOnCleanup(t *testing.T) {
	db := shareProjectionDBForTest(t)
	orgID, userID, repoID := newStagingCapIDs()
	const cap = 3

	var sessions []dbpkg.BlockUploadSession
	for i := 0; i < cap; i++ {
		s, err := db.CreateAdmittedBlockUploadSession(orgID, userID, repoID, "/", testSessionAdmission(nil), cap)
		if err != nil {
			t.Fatalf("create session %d: %v", i, err)
		}
		if s.Slot < 0 || s.Slot >= cap {
			t.Fatalf("session %d claimed slot %d, want 0..%d", i, s.Slot, cap-1)
		}
		sessions = append(sessions, s)
	}

	// The (cap+1)-th create must be rejected — every slot is taken.
	if _, err := db.CreateAdmittedBlockUploadSession(orgID, userID, repoID, "/", testSessionAdmission(nil), cap); !errors.Is(err, dbpkg.ErrBlockUploadSessionSlotsExhausted) {
		t.Fatalf("expected ErrBlockUploadSessionSlotsExhausted, got %v", err)
	}

	// Committing/cleaning one session frees its slot; a new create then succeeds.
	if err := db.CleanupCommittedBlockUploadSessionCaps(sessions[0]); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if _, err := db.CreateAdmittedBlockUploadSession(orgID, userID, repoID, "/", testSessionAdmission(nil), cap); err != nil {
		t.Fatalf("create after freeing a slot should succeed, got %v", err)
	}
}

func TestBlockUploadSessionSlotCapDisabled(t *testing.T) {
	db := shareProjectionDBForTest(t)
	orgID, userID, repoID := newStagingCapIDs()

	// cap <= 0 disables the per-user cap: many creates all succeed with slot = -1.
	for i := 0; i < 5; i++ {
		s, err := db.CreateAdmittedBlockUploadSession(orgID, userID, repoID, "/", testSessionAdmission(nil), 0)
		if err != nil {
			t.Fatalf("create %d with disabled cap: %v", i, err)
		}
		if s.Slot != -1 {
			t.Fatalf("disabled cap should store slot=-1, got %d", s.Slot)
		}
	}
}

// blockIDInBucket returns a hex-ish block id that hashes into the target bucket,
// so a test can put two DISTINCT blocks into the SAME (session, bucket) partition
// deterministically instead of hoping a hardcoded id collides.
func blockIDInBucket(bucket, bucketCount int) string {
	for i := 0; ; i++ {
		id := fmt.Sprintf("blk-%d", i)
		if dbpkg.StagedBlockBucket(id, bucketCount) == bucket {
			return id
		}
	}
}

func distinctBlockIDInBucket(bucket, bucketCount int, exclude string) string {
	for i := 0; ; i++ {
		id := fmt.Sprintf("blk-distinct-%d", i)
		if id != exclude && dbpkg.StagedBlockBucket(id, bucketCount) == bucket {
			return id
		}
	}
}

func TestBlockUploadStagedBlockLedgerIdempotentCountAndReserve(t *testing.T) {
	db := shareProjectionDBForTest(t)
	const bucketCount = 8
	sessionID := gocql.TimeUUID().String()
	blockA := blockIDInBucket(0, bucketCount)
	bucket := dbpkg.StagedBlockBucket(blockA, bucketCount)

	// Empty ledger reads zero, and the block is not yet present.
	if n, err := db.CountSessionStagedBlocksInBucket(sessionID, bucket, 10); err != nil || n != 0 {
		t.Fatalf("empty bucket count = %d err = %v, want 0/nil", n, err)
	}
	if ok, err := db.SessionStagedBlockExists(sessionID, bucket, blockA); err != nil || ok {
		t.Fatalf("exists on empty = %v err = %v, want false/nil", ok, err)
	}

	// Reserving the same block twice is idempotent (same PK) — count stays 1.
	for i := 0; i < 2; i++ {
		if err := db.ReserveSessionStagedBlock(sessionID, bucket, blockA, 8*1024*1024); err != nil {
			t.Fatalf("reserve %d: %v", i, err)
		}
	}
	if n, err := db.CountSessionStagedBlocksInBucket(sessionID, bucket, 10); err != nil || n != 1 {
		t.Fatalf("after double reserve count = %d err = %v, want 1/nil (idempotent)", n, err)
	}
	if ok, err := db.SessionStagedBlockExists(sessionID, bucket, blockA); err != nil || !ok {
		t.Fatalf("exists after reserve = %v err = %v, want true/nil", ok, err)
	}

	// A DISTINCT block deterministically placed in the SAME bucket increments count.
	blockB := distinctBlockIDInBucket(bucket, bucketCount, blockA)
	if err := db.ReserveSessionStagedBlock(sessionID, bucket, blockB, 1024); err != nil {
		t.Fatalf("reserve distinct block in same bucket: %v", err)
	}
	if n, _ := db.CountSessionStagedBlocksInBucket(sessionID, bucket, 10); n != 2 {
		t.Fatalf("count after a distinct block in the same bucket = %d, want 2", n)
	}
}

// TestBlockUploadStagedBucketConcurrentReserveBoundedOvershoot documents that the
// per-block cap is NOT atomic (no per-block Paxos): concurrent reserves of DISTINCT
// blocks into one bucket can overshoot the cap, but only by up to the concurrency
// (each racing reserve sees the same pre-count). The exact per-file bound is
// enforced at commit; this ledger is a bounded anti-abuse backstop.
func TestBlockUploadStagedBucketConcurrentReserveBoundedOvershoot(t *testing.T) {
	db := shareProjectionDBForTest(t)
	const bucketCount = 8
	sessionID := gocql.TimeUUID().String()
	bucket := 0

	const parallel = 12
	ids := make([]string, parallel)
	for i := range ids {
		id := blockIDInBucket(bucket, bucketCount) + fmt.Sprintf("-%d", i)
		// Keep it in the target bucket even with the suffix.
		for dbpkg.StagedBlockBucket(id, bucketCount) != bucket {
			id += "z"
		}
		ids[i] = id
	}

	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(blockID string) {
			defer wg.Done()
			_ = db.ReserveSessionStagedBlock(sessionID, bucket, blockID, 1024)
		}(id)
	}
	wg.Wait()

	// All distinct reserves landed as distinct rows (idempotency is per PK, so
	// distinct ids are all counted) — the ledger records exactly what was staged.
	n, err := db.CountSessionStagedBlocksInBucket(sessionID, bucket, parallel+5)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != parallel {
		t.Fatalf("concurrent distinct reserves recorded %d rows, want %d", n, parallel)
	}
}

func TestBlockUploadSessionStoresExpectedSize(t *testing.T) {
	db := shareProjectionDBForTest(t)
	orgID, userID, repoID := newStagingCapIDs()
	declared := int64(123456789)

	s, err := db.CreateAdmittedBlockUploadSession(orgID, userID, repoID, "/", testSessionAdmission(&declared), 0)
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
	if !got.ExpectedSizeDeclared {
		t.Fatal("expected_size_declared = false, want true")
	}
	if got.BlockSizeBytes == 0 || got.StagedBucketCount == 0 || got.StagedBucketCap == 0 {
		t.Fatalf("persisted admission params missing: block_size=%d buckets=%d cap=%d", got.BlockSizeBytes, got.StagedBucketCount, got.StagedBucketCap)
	}
}
