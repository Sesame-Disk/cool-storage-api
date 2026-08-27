//go:build integration

package integration

import (
	"fmt"
	"testing"
	"time"

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
// MockStore cannot show this. Its RequeueItem searches for the old row first and no-ops
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
