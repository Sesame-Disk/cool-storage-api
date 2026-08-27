//go:build integration

package integration

import (
	"fmt"
	"sync"
	"testing"
	"time"

	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/google/uuid"
)

// p4aBlockQueueRows returns every live gc_queue row for one org, newest cutoff, so a
// duplicate is visible as a count rather than inferred.
func p4aBlockQueueRows(t *testing.T, store *gcpkg.CassandraStore, orgID uuid.UUID, blockID string) []gcpkg.QueueItem {
	t.Helper()
	items, err := store.DequeueBatch(orgID, 100, time.Now().UTC().Add(24*time.Hour))
	if err != nil {
		t.Fatalf("DequeueBatch: %v", err)
	}
	var rows []gcpkg.QueueItem
	for _, item := range items {
		if item.ItemID == blockID {
			rows = append(rows, item)
		}
	}
	return rows
}

// TestP4A_RequeueNeverResurrectsAnAlreadyAdvancedQueueRow is the DURABLE half of the
// late-loser rule, and it needs the real engine for the same reason R19 did.
//
// "Postpone without spending a retry" is implemented as RequeueItem, and RequeueItem is a
// logged batch of DELETE(old row) + INSERT(new row). In Cassandra a DELETE of an absent
// row is a valid no-op and the INSERT applies regardless — so a worker whose copy of the
// row had already been moved by somebody else did not MOVE a row, it created a second
// one. DequeueBatch is a plain SELECT with no lease, so two workers holding the same row
// is not an exotic interleaving; it is the premise the claim protocol exists to survive.
//
// This leg covers the SEQUENTIAL case: the row is already gone when the second worker
// arrives. TestP4A_ConcurrentRequeueOfOneRowAppliesExactlyOnce covers the case a pre-read
// could never have closed, where both workers observe the row and then both mutate.
//
// MockStore cannot show either. Its RequeueItem searches for the old row first and no-ops
// when it is absent, which is the behaviour we WANT and not the behaviour Cassandra had —
// so the entire unit suite agreed with the code while production could double the row.
// That is the exact shape of R19, and it is why this gate lives here.
//
// After R26 the two rows are durably distinct: same block, same identity_at, different
// queued_at, and queued_at is part of the primary key. Nothing collapses them again.
func TestP4A_RequeueNeverResurrectsAnAlreadyAdvancedQueueRow(t *testing.T) {
	requireCassandra(t)
	gate := p4aRequireEvidence(t)

	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	orgID := uuid.New()
	blockID := fmt.Sprintf("p4a-requeue-%d", time.Now().UnixNano())
	t.Cleanup(func() { cleanupGCBlockRowsForTest(t, orgID, blockID) })

	seedCanonicalBlockRowForTest(t, database, orgID, blockID, "hot")
	candidate, err := store.EnsureBlockGCCandidateExact(orgID, blockID, "hot", time.Now().UTC().Add(-2*time.Hour).Truncate(time.Millisecond))
	if err != nil {
		t.Fatalf("EnsureBlockGCCandidateExact: %v", err)
	}
	if err := enqueueExactBlockCandidateForTest(store, candidate, candidate.CandidateAt); err != nil {
		t.Fatalf("enqueue candidate: %v", err)
	}

	// Both workers dequeue the same row. This is one SELECT away from being the normal
	// state of the system.
	rows := p4aBlockQueueRows(t, store, orgID, blockID)
	if len(rows) != 1 {
		t.Fatalf("queue rows after enqueue = %d, want 1", len(rows))
	}
	q0 := rows[0]

	requeue := func(from, to time.Time, retryCount int) error {
		return store.RequeueItem(orgID, from, to, q0.ItemType, q0.ItemID, q0.LibraryID,
			q0.BlockRepresentationID, q0.StorageClass, retryCount, q0.Identity(),
			q0.RequiresLibraryDeletedCheck, q0.LibraryGuardMode)
	}

	// Worker B gets there first and advances the lifecycle: Q0 -> Q1.
	q1At := time.Now().UTC().Truncate(time.Millisecond)
	if err := requeue(q0.QueuedAt, q1At, q0.RetryCount); err != nil {
		t.Fatalf("worker B requeue Q0 -> Q1: %v", err)
	}

	// Worker A wakes up much later, discovers at its release that another lifecycle owns
	// the fence, and postpones — still holding its stale copy of Q0. Q0 is gone.
	q2At := q1At.Add(time.Second)
	if err := requeue(q0.QueuedAt, q2At, q0.RetryCount); err != nil {
		t.Fatalf("worker A postpone on a stale queue row must be a no-op, not an error: %v", err)
	}

	after := p4aBlockQueueRows(t, store, orgID, blockID)
	if len(after) != 1 {
		t.Fatalf("live queue rows = %d, want 1: a stale worker resurrected its own copy of the row alongside the live one; after R26 both are durable and only queued_at tells them apart: %+v", len(after), after)
	}
	if !after[0].QueuedAt.Equal(q1At) {
		t.Fatalf("surviving queue row is queued_at=%s, want Q1 at %s: the live lifecycle's row must be the one that stands", after[0].QueuedAt.Format(time.RFC3339Nano), q1At.Format(time.RFC3339Nano))
	}

	// And the requeue that DOES address a live row must still move it, or the guard above
	// would pass by breaking postponement outright.
	q3At := q2At.Add(time.Second)
	if err := requeue(q1At, q3At, q0.RetryCount+1); err != nil {
		t.Fatalf("requeue of the live row: %v", err)
	}
	moved := p4aBlockQueueRows(t, store, orgID, blockID)
	if len(moved) != 1 || !moved[0].QueuedAt.Equal(q3At) {
		t.Fatalf("requeue of a live row must move it: got %+v, want exactly one row at %s", moved, q3At.Format(time.RFC3339Nano))
	}
	if moved[0].RetryCount != q0.RetryCount+1 {
		t.Fatalf("retry_count = %d, want %d: the existence check must not swallow the payload update", moved[0].RetryCount, q0.RetryCount+1)
	}

	gate.observed = true
}

// TestP4A_ConcurrentRequeueOfOneRowAppliesExactlyOnce is the leg that a read-before-batch
// implementation cannot pass, and the reason the move is a conditional batch rather than
// a SELECT followed by an unconditional one.
//
// The sequential leg above only proves that a row already absent on entry is not
// recreated. A pre-read passes that just as well. What it cannot pass is this:
//
//	A: observes Q0 -> exists
//	B: observes Q0 -> exists
//	B: DELETE Q0 + INSERT Q1   (applies)
//	A: DELETE Q0 (no-op) + INSERT Q2   (applies anyway)
//
// Raising the read to SERIAL does not help. A linearizable read still only says the row
// existed at that instant; it does not stop the other worker from moving it immediately
// afterwards. The existence of the old row has to be a condition OF the mutation.
//
// With the conditional batch, Paxos serializes the racers on the gc_queue partition and
// exactly one move applies — so this assertion holds no matter how the goroutines
// interleave, which is what makes the test non-flaky rather than merely lucky. Several
// rounds are run because a race test that never actually overlaps proves nothing about
// the window even when it passes.
func TestP4A_ConcurrentRequeueOfOneRowAppliesExactlyOnce(t *testing.T) {
	requireCassandra(t)
	gate := p4aRequireEvidence(t)

	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	orgID := uuid.New()

	const racers = 4
	for round := 0; round < 5; round++ {
		blockID := fmt.Sprintf("p4a-requeue-race-%d-%d", time.Now().UnixNano(), round)
		func() {
			defer cleanupGCBlockRowsForTest(t, orgID, blockID)

			seedCanonicalBlockRowForTest(t, database, orgID, blockID, "hot")
			candidate, err := store.EnsureBlockGCCandidateExact(orgID, blockID, "hot", time.Now().UTC().Add(-2*time.Hour).Truncate(time.Millisecond))
			if err != nil {
				t.Fatalf("round %d: EnsureBlockGCCandidateExact: %v", round, err)
			}
			if err := enqueueExactBlockCandidateForTest(store, candidate, candidate.CandidateAt); err != nil {
				t.Fatalf("round %d: enqueue candidate: %v", round, err)
			}
			rows := p4aBlockQueueRows(t, store, orgID, blockID)
			if len(rows) != 1 {
				t.Fatalf("round %d: queue rows after enqueue = %d, want 1", round, len(rows))
			}
			q0 := rows[0]

			// Every racer holds the same Q0, which is what DequeueBatch hands out: it is a
			// plain SELECT and takes no lease.
			start := make(chan struct{})
			var wg sync.WaitGroup
			errs := make([]error, racers)
			targets := make([]time.Time, racers)
			for i := 0; i < racers; i++ {
				targets[i] = time.Now().UTC().Add(time.Duration(i+1) * time.Second).Truncate(time.Millisecond)
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					errs[i] = store.RequeueItem(orgID, q0.QueuedAt, targets[i], q0.ItemType, q0.ItemID,
						q0.LibraryID, q0.BlockRepresentationID, q0.StorageClass, q0.RetryCount,
						q0.Identity(), q0.RequiresLibraryDeletedCheck, q0.LibraryGuardMode)
				}(i)
			}
			close(start)
			wg.Wait()

			for i, err := range errs {
				if err != nil {
					t.Fatalf("round %d: racer %d: losing a conditional move is a no-op, not an error: %v", round, i, err)
				}
			}

			after := p4aBlockQueueRows(t, store, orgID, blockID)
			if len(after) != 1 {
				t.Fatalf("round %d: live queue rows = %d, want 1: workers that all observed Q0 each created a durable row; after R26 they differ only in queued_at and nothing collapses them again: %+v", round, len(after), after)
			}
			if after[0].QueuedAt.Equal(q0.QueuedAt) {
				t.Fatalf("round %d: Q0 is still the live row; no move applied at all", round)
			}
			var matched bool
			for _, target := range targets {
				if after[0].QueuedAt.Equal(target) {
					matched = true
				}
			}
			if !matched {
				t.Fatalf("round %d: surviving row is at %s, which is none of the racers' targets", round, after[0].QueuedAt.Format(time.RFC3339Nano))
			}
		}()
	}

	gate.observed = true
}

// TestP4A_StaleRequeueCannotResurrectCompletedPendingMarker covers the other race
// boundary: CompleteItem removes both the queue row and its pending membership, then a
// worker with a stale copy of that row attempts to postpone it. A losing queue CAS must
// not recreate gc_pending_items, because that table has no TTL and non-block dedup probes
// intentionally ask about any lifecycle of the item.
func TestP4A_StaleRequeueCannotResurrectCompletedPendingMarker(t *testing.T) {
	requireCassandra(t)
	gate := p4aRequireEvidence(t)

	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	orgID, libraryID := uuid.New(), uuid.New()
	itemID := fmt.Sprintf("p4a-completed-requeue-%d", time.Now().UnixNano())
	queuedAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)

	t.Cleanup(func() {
		_ = database.Session().Query(`
			DELETE FROM gc_queue WHERE org_id = ? AND bucket = ?
		`, orgID.String(), gcpkg.QueueBucket(orgID, gcpkg.ItemCommit, itemID)).Exec()
		deleteGCPendingItemsByIdentity(t, orgID, libraryID, gcpkg.ItemCommit, itemID)
	})

	q := gcpkg.QueueItem{
		OrgID:                 orgID,
		QueuedAt:              queuedAt,
		IdentityAt:            queuedAt,
		ItemType:              gcpkg.ItemCommit,
		ItemID:                itemID,
		LibraryID:             libraryID,
		BlockRepresentationID: dbpkg.PlainBlockRepresentationID,
		StorageClass:          "hot",
	}
	if err := store.EnqueueBatch([]gcpkg.QueueItem{q}); err != nil {
		t.Fatalf("enqueue Q0: %v", err)
	}

	items, err := store.DequeueBatch(orgID, 10, time.Now().UTC())
	if err != nil {
		t.Fatalf("dequeue Q0: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("dequeued rows = %d, want exactly Q0", len(items))
	}
	q0 := items[0]
	identity := q0.Identity()
	if exists, err := store.PendingItemExists(orgID, libraryID, gcpkg.ItemCommit, itemID, gcpkg.AnyGCItemIdentity()); err != nil || !exists {
		t.Fatalf("pending marker before completion: exists=%v err=%v, want true", exists, err)
	}

	// B completes the lifecycle and removes both Q0 and its pending membership.
	if err := store.CompleteItem(orgID, q0.QueuedAt, q0.ItemType, q0.ItemID, identity); err != nil {
		t.Fatalf("complete Q0: %v", err)
	}
	if exists, err := store.QueueItemExists(orgID, q0.QueuedAt, q0.ItemType, q0.ItemID, identity); err != nil || exists {
		t.Fatalf("queue after completion: exists=%v err=%v, want false", exists, err)
	}
	if exists, err := store.PendingItemExists(orgID, libraryID, gcpkg.ItemCommit, itemID, gcpkg.AnyGCItemIdentity()); err != nil || exists {
		t.Fatalf("pending marker after completion: exists=%v err=%v, want false", exists, err)
	}

	// A retains the stale Q0 copy. Its CAS loses, and must not recreate the pending marker.
	if err := store.RequeueItem(orgID, q0.QueuedAt, q0.QueuedAt.Add(time.Minute), q0.ItemType, q0.ItemID,
		q0.LibraryID, q0.BlockRepresentationID, q0.StorageClass, q0.RetryCount, identity,
		q0.RequiresLibraryDeletedCheck, q0.LibraryGuardMode); err != nil {
		t.Fatalf("stale requeue after completion: %v", err)
	}
	if exists, err := store.QueueItemExists(orgID, q0.QueuedAt.Add(time.Minute), q0.ItemType, q0.ItemID, identity); err != nil || exists {
		t.Fatalf("queue after losing stale requeue: exists=%v err=%v, want false", exists, err)
	}
	if exists, err := store.PendingItemExists(orgID, libraryID, gcpkg.ItemCommit, itemID, gcpkg.AnyGCItemIdentity()); err != nil || exists {
		t.Fatalf("pending marker after losing stale requeue: exists=%v err=%v, want false", exists, err)
	}

	gate.observed = true
}
