//go:build integration

package integration

import (
	"fmt"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/google/uuid"
)

// X1 / R19 — UpdateS3OrphanAttempt must never bring an orphan into existence.
//
// This needs a real Cassandra because the defect and the fix both live in CQL
// semantics that no mock reproduces: a plain UPDATE is an upsert, so writing
// last_attempt_at / retry_count / last_error against a cleared row recreated it
// from those three columns alone — no storage_class, no first_seen_at, no
// recovery_phase, and no gc_s3_orphans_by_day entry.
//
// That shape matters because under A+ any orphan row is a writer fence:
// ProbeBlockReuse answers BlockedByGC on mere existence, and both fence reads
// select only block_id, which such a row still returns. So the resurrected row
// blocks every upload of that content while being invisible to the recovery
// sweep, which enumerates through the projection.
//
// MockStore.UpdateS3OrphanAttempt has always been correct here — it only mutates
// a matching lifecycle that is already present — which is exactly why the unit
// suite agreed with the fix while production carried the defect. Gate the real
// conditional statement.
func TestGC_UpdateS3OrphanAttempt_DoesNotResurrectClearedOrphan(t *testing.T) {
	requireCassandra(t)

	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	orgID := uuid.New()
	blockID := fmt.Sprintf("orph-no-resurrect-%d", time.Now().UnixNano())
	firstSeenAt := time.Now().UTC().Truncate(time.Millisecond)

	// Establish a normal orphan through the only mutation allowed to create one.
	if _, err := store.StartBlockDeleteOrphan(orgID, blockID, "hot", db.PlainBlockRepresentationID, "", firstSeenAt); err != nil {
		t.Fatalf("StartBlockDeleteOrphan: %v", err)
	}
	t.Cleanup(func() {
		if err := store.DeleteS3Orphan(orgID, blockID, firstSeenAt); err != nil {
			t.Logf("cleanup DeleteS3Orphan(%s): %v", blockID, err)
		}
	})

	// A failed S3 delete records an attempt while the row is live. This must
	// work: refusing here would break the diagnostic the recovery sweep reports.
	if err := store.UpdateS3OrphanAttempt(orgID, blockID, firstSeenAt, "first failure", time.Now().UTC()); err != nil {
		t.Fatalf("UpdateS3OrphanAttempt on a live row: %v", err)
	}
	if !gcS3OrphanExists(t, orgID.String(), blockID) {
		t.Fatal("orphan row vanished after recording an attempt on it")
	}

	// Another path clears the lifecycle — recovery succeeded, or the block was
	// resurrected and the stale row discarded.
	if err := store.DeleteS3Orphan(orgID, blockID, firstSeenAt); err != nil {
		t.Fatalf("DeleteS3Orphan: %v", err)
	}
	if gcS3OrphanExists(t, orgID.String(), blockID) {
		t.Fatal("orphan row still present after DeleteS3Orphan; the rest of this test would prove nothing")
	}

	// The losing recoverer's in-flight retry lands after the clear. Before the
	// fix this recreated the row.
	if err := store.UpdateS3OrphanAttempt(orgID, blockID, firstSeenAt, "late failure after clear", time.Now().UTC()); err != nil {
		t.Fatalf("UpdateS3OrphanAttempt after clear returned error: %v", err)
	}

	if gcS3OrphanExists(t, orgID.String(), blockID) {
		t.Fatal("UpdateS3OrphanAttempt resurrected a cleared orphan row (R19). " +
			"Only StartBlockDeleteOrphan may create orphan state; every other " +
			"mutation must be conditional and fail when the row is gone. " +
			"Under A+ this row is a writer fence with no discovery entry, so it " +
			"blocks uploads of that content while no sweep can enumerate it.")
	}
	if gcS3OrphanProjectionExists(t, orgID.String(), blockID, firstSeenAt) {
		t.Fatal("discovery projection reappeared for a cleared orphan")
	}
}

// TestGC_UpdateS3OrphanAttempt_RejectsDifferentLifecycleToken is the R19 stale-token
// gate. A non-creating condition prevents an update after a clear from
// recreating a row, but existence alone does not stop that same delayed update
// from landing on a new P2 row with the same primary key when its observed
// first_seen_at differs. It is a stale-token guard, not a unique lifecycle ID;
// StartBlockDeleteOrphan can reuse the value when resetting an existing row.
func TestGC_UpdateS3OrphanAttempt_RejectsDifferentLifecycleToken(t *testing.T) {
	requireCassandra(t)

	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	orgID := uuid.New()
	blockID := fmt.Sprintf("orph-incarnation-%d", time.Now().UnixNano())
	p1FirstSeenAt := time.Now().UTC().Truncate(time.Millisecond)
	p2FirstSeenAt := p1FirstSeenAt.Add(time.Second)

	if _, err := store.StartBlockDeleteOrphan(orgID, blockID, "hot", db.PlainBlockRepresentationID, "sha1-p1", p1FirstSeenAt); err != nil {
		t.Fatalf("StartBlockDeleteOrphan P1: %v", err)
	}
	if err := store.DeleteS3Orphan(orgID, blockID, p1FirstSeenAt); err != nil {
		t.Fatalf("DeleteS3Orphan P1: %v", err)
	}
	if _, err := store.StartBlockDeleteOrphan(orgID, blockID, "cold", db.PlainBlockRepresentationID, "sha1-p2", p2FirstSeenAt); err != nil {
		t.Fatalf("StartBlockDeleteOrphan P2: %v", err)
	}
	t.Cleanup(func() {
		if err := store.DeleteS3Orphan(orgID, blockID, p2FirstSeenAt); err != nil {
			t.Logf("cleanup DeleteS3Orphan(%s): %v", blockID, err)
		}
	})

	// This is an in-flight P1 failure arriving after P2 has taken the key.
	if err := store.UpdateS3OrphanAttempt(orgID, blockID, p1FirstSeenAt, "late P1 failure", p2FirstSeenAt.Add(time.Second)); err != nil {
		t.Fatalf("UpdateS3OrphanAttempt for stale P1: %v", err)
	}

	var storedFirstSeenAt time.Time
	var retryCount int
	var lastError string
	if err := database.Session().Query(`
		SELECT first_seen_at, retry_count, last_error
		FROM gc_s3_orphans WHERE org_id = ? AND block_id = ?
	`, orgID.String(), blockID).Scan(&storedFirstSeenAt, &retryCount, &lastError); err != nil {
		t.Fatalf("read P2 orphan after stale update: %v", err)
	}
	if !storedFirstSeenAt.Equal(p2FirstSeenAt) {
		t.Fatalf("first_seen_at = %v, want P2 %v", storedFirstSeenAt, p2FirstSeenAt)
	}
	if retryCount != 0 {
		t.Fatalf("retry_count = %d after stale P1 update, want 0", retryCount)
	}
	if lastError != "" {
		t.Fatalf("last_error = %q after stale P1 update, want empty", lastError)
	}
}

// TestGC_UpdateS3OrphanAttempt_AnchorsDiagnosticTTLOnFirstSeenAt is the R28 half
// against a real cluster.
//
// GETTING THIS TEST RIGHT TOOK TWO TRIES; THE FIRST VERSION WAS DECORATIVE.
// It backdated first_seen_at by 30 days, inserted, updated, and asserted
// TTL(last_error) <= TTL(storage_class). That passes on the UNFIXED code,
// because Cassandra counts a cell's TTL from its WRITE, not from any value in
// the row: both columns had just been written, so both read back ~90 days and
// the assertion never fired. A gate that cannot go red is not a gate.
//
// What actually distinguishes the two implementations is the ANCHOR. The fix
// derives the diagnostic TTL from the row's first_seen_at; the old code took the
// table default from the write clock. So backdate first_seen_at and the two
// diverge by exactly that amount:
//
//	unfixed: TTL(last_error) ≈ TTL(storage_class)          (both ~90d)
//	fixed:   TTL(last_error) ≈ TTL(storage_class) − 30d
//
// Asserting the gap is what makes this detect the defect. The equivalent
// wall-clock experiment — insert, wait, update — would need a real sleep to
// separate the write times, and would only separate them by the sleep.
func TestGC_UpdateS3OrphanAttempt_AnchorsDiagnosticTTLOnFirstSeenAt(t *testing.T) {
	requireCassandra(t)

	const backdate = 30 * 24 * time.Hour

	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	orgID := uuid.New()
	blockID := fmt.Sprintf("orph-ttl-anchor-%d", time.Now().UnixNano())

	firstSeenAt := time.Now().UTC().Add(-backdate).Truncate(time.Millisecond)
	if _, err := store.StartBlockDeleteOrphan(orgID, blockID, "hot", db.PlainBlockRepresentationID, "", firstSeenAt); err != nil {
		t.Fatalf("StartBlockDeleteOrphan: %v", err)
	}
	t.Cleanup(func() {
		if err := store.DeleteS3Orphan(orgID, blockID, firstSeenAt); err != nil {
			t.Logf("cleanup DeleteS3Orphan(%s): %v", blockID, err)
		}
	})

	if err := store.UpdateS3OrphanAttempt(orgID, blockID, firstSeenAt, "aged failure", time.Now().UTC()); err != nil {
		t.Fatalf("UpdateS3OrphanAttempt: %v", err)
	}

	var identityTTL, diagnosticTTL int
	if err := database.Session().Query(`
		SELECT TTL(storage_class), TTL(last_error) FROM gc_s3_orphans
		WHERE org_id = ? AND block_id = ?
	`, orgID.String(), blockID).Scan(&identityTTL, &diagnosticTTL); err != nil {
		t.Fatalf("read per-column TTLs: %v", err)
	}
	if identityTTL <= 0 {
		t.Fatalf("TTL(storage_class) = %d; the row should still hold most of its term", identityTTL)
	}

	// One day of slack absorbs clock granularity and test runtime; the signal
	// being measured is 30 days wide.
	wantAtMost := identityTTL - int(backdate.Seconds()) + 86400
	if diagnosticTTL > wantAtMost {
		t.Fatalf("TTL(last_error) = %d, want <= %d (TTL(storage_class) = %d minus the %.0f-day backdate).\n"+
			"The diagnostic columns were written on the retry's clock instead of being anchored on\n"+
			"first_seen_at, so they outlive the identity columns they annotate. Cassandra applies\n"+
			"default_time_to_live per written value, so what survives is a live primary key with no\n"+
			"storage_class, no first_seen_at and no projection row — a writer fence that no sweep can\n"+
			"enumerate (R28 in docs/GC-X1-CLOSURE-OPTIONS.md).",
			diagnosticTTL, wantAtMost, identityTTL, backdate.Hours()/24)
	}

	// The invariant the gap is protecting, stated plainly.
	if diagnosticTTL > identityTTL {
		t.Fatalf("TTL(last_error) = %d exceeds TTL(storage_class) = %d: an annotation may not outlive what it annotates",
			diagnosticTTL, identityTTL)
	}
}
