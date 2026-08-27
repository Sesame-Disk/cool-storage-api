//go:build integration

package integration

import (
	"testing"
	"time"

	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/google/uuid"
)

// R26 says every durable GC surface must be able to retire exactly its own row.
// gc_failed_items_by_expiry is the one surface whose PARTITION KEY is not read
// back from Cassandra but recomputed in Go, because its bucket is a hash — and
// two of the values it hashes are timestamps.
//
// That makes it the one place where the identity can decay in transit. FailItem
// hashes the worker's clock, which is time.Now and carries nanoseconds; every
// later delete hashes the same instant after a round-trip through a Cassandra
// TIMESTAMP, which keeps milliseconds. When those disagree the DELETE names a
// different partition than the INSERT did, Cassandra reports success, and the
// expiry row outlives both its canonical DLQ row and every attempt to remove it:
// the sweep walks all buckets, finds it again, takes the orphan branch, and
// recomputes the same wrong bucket forever.
//
// It cannot be caught by a mock — MockStore keeps a time.Time and loses nothing —
// and the existing real-Cassandra fixtures all truncate to milliseconds before
// they start, which is exactly the input that hides it. So this test deliberately
// does NOT truncate: it fails an item at an instant with a sub-millisecond
// remainder, the way production does.
func TestR26_DLQExpiryProjectionRetiresItselfAfterTheCassandraRoundTrip(t *testing.T) {
	requireCassandra(t)
	gate := r26RequireEvidence(t)

	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)

	orgID := uuid.New()
	itemID := uuid.New().String()
	// A user cascade: no block representation and no candidate identity to carry,
	// so the only thing under test is the expiry projection's own key.
	const itemType = gcpkg.ItemUserCascade

	queuedAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
	if err := store.EnqueueItem(orgID, queuedAt, itemType, itemID, uuid.Nil, "", 0); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// THE POINT OF THE TEST: a failure instant Cassandra cannot store exactly.
	failedAt := time.Now().UTC().Truncate(time.Millisecond).Add(456789 * time.Nanosecond)
	if failedAt.Equal(time.UnixMilli(failedAt.UnixMilli()).UTC()) {
		t.Fatal("fixture is vacuous: failedAt must carry a sub-millisecond remainder")
	}

	item := gcpkg.QueueItem{
		OrgID:      orgID,
		QueuedAt:   queuedAt,
		IdentityAt: queuedAt,
		ItemType:   itemType,
		ItemID:     itemID,
		LibraryID:  uuid.Nil,
	}
	if err := store.FailItem(item, failedAt, "forced failure", "test"); err != nil {
		t.Fatalf("FailItem: %v", err)
	}
	t.Cleanup(func() {
		_ = database.Session().Query(
			`DELETE FROM gc_failed_items WHERE org_id = ?`, orgID.String()).Exec()
	})

	// Find the expiry row the way the scanner does: walk every bucket of the day.
	// Recomputing the bucket here would test the code against itself.
	expiresAtDay := failedAt.Add(gcFailedItemRetentionForTest)
	expiry, foundBucket, found := findFailedItemExpiry(t, store, expiresAtDay, orgID, itemID)
	if !found {
		t.Fatalf("no expiry projection row was written for %s/%s", orgID, itemID)
	}
	// Retire the projection too if an assertion below fails: the bucket comes from the
	// partition the finder actually walked, never from the formula under test, so a
	// broken formula cannot leave this cleanup pointing at the wrong partition.
	t.Cleanup(func() {
		_ = database.Session().Query(
			`DELETE FROM gc_failed_items_by_expiry WHERE expiry_day = ? AND bucket = ? AND expires_at = ? AND org_id = ? AND failed_at = ? AND item_type = ? AND item_id = ? AND candidate_storage_class = ? AND candidate_storage_key = ? AND identity_at = ?`,
			dbpkg.GCProjectionUTCDate(expiry.ExpiresAt), foundBucket, expiry.ExpiresAt.UTC(), orgID.String(),
			expiry.FailedAt.UTC(), string(expiry.ItemType), expiry.ItemID, "", "", expiry.IdentityAt.UTC()).Exec()
	})
	gate.observed = true

	// Expire it: this is the scanner's Phase 10 path.
	deleted, err := store.DeleteExpiredFailedItem(expiry, expiry.ExpiresAt.Add(time.Second))
	if err != nil {
		t.Fatalf("DeleteExpiredFailedItem: %v", err)
	}
	if !deleted {
		t.Fatalf("DeleteExpiredFailedItem reported nothing expired for a row past its expiry")
	}

	// The projection must be gone. A surviving row here is not cosmetic: the
	// canonical DLQ row it points at has just been deleted, so every later sweep
	// re-enumerates it, fails to find its canonical half, and recomputes the same
	// unreachable bucket — an expiry row nothing in the system can remove.
	if _, _, stillThere := findFailedItemExpiry(t, store, expiresAtDay, orgID, itemID); stillThere {
		t.Fatalf("the expiry projection for %s/%s survived its own deletion: the DELETE hashed a different "+
			"bucket than the INSERT because the failure instant lost its sub-millisecond part in Cassandra. "+
			"Nothing can retire that row afterwards.", orgID, itemID)
	}
}

// gcFailedItemRetentionForTest mirrors the store's DLQ retention. The projection
// is keyed by the expiry DAY, so the test only needs the right day, and reading
// the unexported constant from another package is not worth a seam.
const gcFailedItemRetentionForTest = 30 * 24 * time.Hour

// findFailedItemExpiry walks every discovery bucket for a day, the way the
// scanner does, and returns the row for one item.
func findFailedItemExpiry(t *testing.T, store *gcpkg.CassandraStore, day time.Time, orgID uuid.UUID, itemID string) (gcpkg.GCFailedItemExpiryInfo, int, bool) {
	t.Helper()
	for bucket := 0; bucket < dbpkg.GCDiscoveryBucketCount; bucket++ {
		expiries, err := store.ListFailedItemExpiriesByDay(day, bucket)
		if err != nil {
			t.Fatalf("ListFailedItemExpiriesByDay(day=%s bucket=%d): %v", day.Format(time.RFC3339), bucket, err)
		}
		for _, expiry := range expiries {
			if expiry.OrgID == orgID && expiry.ItemID == itemID {
				return expiry, bucket, true
			}
		}
	}
	return gcpkg.GCFailedItemExpiryInfo{}, 0, false
}

// TestCassandraTimestampBindingNormalizesSubMillisecondParameters pins the OTHER half
// of the precision boundary, and it exists because getting this half wrong produced a
// defect report against code that is correct.
//
// The rule is not "sub-millisecond timestamps are dangerous". It is narrower:
//
//   - a value that reaches Cassandra as a QUERY PARAMETER is normalized by the driver
//     on the way out — gocql marshals a time.Time to `Unix()*1e3 + Nanosecond()/1e6` —
//     so a caller holding nanoseconds still matches the stored millisecond value, and
//     no WHERE clause needs help;
//   - a value the code turns into a KEY ITSELF, in Go, never meets the driver. That is
//     the expiry projection's bucket, which is why GCFailedItemExpiryBucket has to
//     normalize explicitly and why the test above exists.
//
// A reviewer reading only the second half will "find" a bug in the first: an admin DLQ
// selector parses RFC3339Nano, so surely a sub-millisecond identity_at matches nothing?
// It matches. This proves it against the real engine rather than against a reading of
// the driver, so the claim cannot be re-derived from first principles a third time.
func TestCassandraTimestampBindingNormalizesSubMillisecondParameters(t *testing.T) {
	requireCassandra(t)
	database := shareProjectionDBForTest(t)

	orgID := uuid.New()
	itemID := uuid.New().String()
	stored := time.Now().UTC().Truncate(time.Millisecond)
	subMilli := stored.Add(456789 * time.Nanosecond)
	if stored.Equal(subMilli) {
		t.Fatal("fixture is vacuous: the two values must differ in Go")
	}

	// Write with the truncated value, exactly as a row read back from Cassandra would
	// carry it.
	if err := database.Session().Query(
		`INSERT INTO gc_failed_items (org_id, failed_at, item_type, item_id, candidate_storage_class, candidate_storage_key, identity_at, expires_at, resolution_status)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		orgID.String(), stored, string(gcpkg.ItemUserCascade), itemID, "", "", stored, stored.Add(time.Hour), "open",
	).Exec(); err != nil {
		t.Fatalf("seed gc_failed_items: %v", err)
	}
	t.Cleanup(func() {
		_ = database.Session().Query(`DELETE FROM gc_failed_items WHERE org_id = ?`, orgID.String()).Exec()
	})

	// Select it with the SUB-MILLISECOND value in every timestamp position of the key.
	var got string
	err := database.Session().Query(
		`SELECT item_id FROM gc_failed_items
		 WHERE org_id = ? AND failed_at = ? AND item_type = ? AND item_id = ? AND candidate_storage_class = ? AND candidate_storage_key = ? AND identity_at = ?`,
		orgID.String(), subMilli, string(gcpkg.ItemUserCascade), itemID, "", "", subMilli,
	).Scan(&got)
	if err != nil {
		t.Fatalf("a sub-millisecond time.Time parameter failed to address the row stored under its "+
			"truncated value: %v.\nIf this ever starts failing, every WHERE clause in the GC store that "+
			"binds a caller-held timestamp needs explicit normalization, not just the bucket hash.", err)
	}
	if got != itemID {
		t.Fatalf("matched the wrong row: %s", got)
	}
}
