package db

import (
	"errors"
	"testing"
	"time"
)

// provisionalPairFixture models the two rows a release mutates — the reference
// (identified by created_at) and its tracker (identified by expires_at) — so a
// renewal can be injected at any step and the surviving state inspected.
type provisionalPairFixture struct {
	refCreatedAt   time.Time
	refPresent     bool
	trackerExpires time.Time
}

func (f *provisionalPairFixture) renew(createdAt, expiresAt time.Time) {
	f.refCreatedAt = createdAt
	f.refPresent = true
	f.trackerExpires = expiresAt
}

func (f *provisionalPairFixture) install(t *testing.T) {
	t.Helper()
	oldReadExpiry := releaseProvisionalReadExpiryFn
	oldReadRef := releaseProvisionalReadReferenceFn
	oldRemove := releaseProvisionalRemoveReferenceFn
	oldDelete := releaseProvisionalDeleteTrackerFn
	t.Cleanup(func() {
		releaseProvisionalReadExpiryFn = oldReadExpiry
		releaseProvisionalReadReferenceFn = oldReadRef
		releaseProvisionalRemoveReferenceFn = oldRemove
		releaseProvisionalDeleteTrackerFn = oldDelete
	})

	releaseProvisionalReadExpiryFn = func(*DB, string, string, string) (time.Time, error) {
		return f.trackerExpires, nil
	}
	releaseProvisionalReadReferenceFn = func(*DB, string, string, string) (time.Time, bool, error) {
		return f.refCreatedAt, f.refPresent, nil
	}
	releaseProvisionalRemoveReferenceFn = func(_ *DB, _, _, _ string, createdAt time.Time) (provisionalReferenceReleaseOutcome, error) {
		if !f.refPresent {
			return provisionalReferenceAbsent, nil
		}
		if !f.refCreatedAt.Equal(createdAt) {
			return provisionalReferenceRenewed, nil
		}
		f.refPresent = false
		return provisionalReferenceRemoved, nil
	}
	releaseProvisionalDeleteTrackerFn = func(_ *DB, _, _, _ string, expiresAt time.Time) (bool, error) {
		if f.trackerExpires.IsZero() || !f.trackerExpires.Equal(expiresAt) {
			return false, nil
		}
		f.trackerExpires = time.Time{}
		return true, nil
	}
}

// The release path is the second half of F10. Creation is atomic now, but a
// release that reads the tracker AFTER dropping the reference reads whatever is
// there at that moment — including a tracker a concurrent renewal just wrote —
// and an unconditional delete then removes it while the renewal's reference lives
// on. That leaves a reference with no tracker, the one state nothing recovers
// from: GC Phase 0 discovers provisional refs only through the tracker's by-day
// projection, so when the TTL finally retires that reference nothing runs the
// zero-ref transition and the block, its metadata and its S3 object are retained
// forever.
//
// The release must therefore retire only the pair it observed.
func TestReleaseProvisionalBlockReference_KeepsARenewalThatLandsMidRelease(t *testing.T) {
	oldRemove := releaseProvisionalRemoveReferenceFn

	observedCreated := time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	observedExpiry := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Millisecond)
	renewedCreated := time.Now().UTC().Truncate(time.Millisecond)
	renewedExpiry := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Millisecond)

	fixture := &provisionalPairFixture{refCreatedAt: observedCreated, refPresent: true, trackerExpires: observedExpiry}
	fixture.install(t)

	// The renewal lands after the reference delete: a retry of the same block under
	// the same session writes a fresh reference and moves the tracker forward.
	inner := releaseProvisionalRemoveReferenceFn
	releaseProvisionalRemoveReferenceFn = func(d *DB, orgID, blockID, referrer string, createdAt time.Time) (provisionalReferenceReleaseOutcome, error) {
		outcome, err := inner(d, orgID, blockID, referrer, createdAt)
		fixture.renew(renewedCreated, renewedExpiry)
		return outcome, err
	}
	t.Cleanup(func() { releaseProvisionalRemoveReferenceFn = oldRemove })

	removed, err := (&DB{}).ReleaseProvisionalBlockReference("org-1", "block-1", "up:session-1")
	if err != nil {
		t.Fatalf("ReleaseProvisionalBlockReference() error = %v, want nil", err)
	}
	if !removed {
		t.Fatal("the observed reference was removed, so the caller must be told so")
	}

	if fixture.trackerExpires.IsZero() {
		t.Fatal("release deleted the tracker of a reference renewed mid-release: that reference is now undiscoverable by GC (F10)")
	}
	if !fixture.trackerExpires.Equal(renewedExpiry) {
		t.Fatalf("tracker = %v, want the renewed %v", fixture.trackerExpires, renewedExpiry)
	}
	if !fixture.refPresent {
		t.Fatal("the renewal's reference must survive")
	}
}

// TestReleaseProvisionalBlockReference_KeepsARenewalThatLandsBeforeTheDelete is the
// mirror window: the renewal completes between the release's observation and its
// delete. Deleting the reference unconditionally there destroys the renewal's
// reference while the CAS keeps its tracker — and worse, the caller then sees zero
// references and promotes a block whose upload is still running to a GC candidate.
// That is F9's failure mode arriving through the release path, so the reference
// delete has to be tied to the observed generation too.
func TestReleaseProvisionalBlockReference_KeepsARenewalThatLandsBeforeTheDelete(t *testing.T) {
	oldRead := releaseProvisionalReadReferenceFn

	observedCreated := time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	observedExpiry := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Millisecond)
	renewedCreated := time.Now().UTC().Truncate(time.Millisecond)
	renewedExpiry := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Millisecond)

	fixture := &provisionalPairFixture{refCreatedAt: observedCreated, refPresent: true, trackerExpires: observedExpiry}
	fixture.install(t)

	// Renew right after the release observes the reference, before it deletes.
	inner := releaseProvisionalReadReferenceFn
	releaseProvisionalReadReferenceFn = func(d *DB, orgID, blockID, referrer string) (time.Time, bool, error) {
		createdAt, present, err := inner(d, orgID, blockID, referrer)
		fixture.renew(renewedCreated, renewedExpiry)
		return createdAt, present, err
	}
	t.Cleanup(func() { releaseProvisionalReadReferenceFn = oldRead })

	removed, err := (&DB{}).ReleaseProvisionalBlockReference("org-1", "block-1", "up:session-1")
	if err != nil {
		t.Fatalf("ReleaseProvisionalBlockReference() error = %v, want nil", err)
	}

	if !fixture.refPresent {
		t.Fatal("release deleted a reference that had already been renewed: the live upload is unpinned (F9 through the release path)")
	}
	if !fixture.trackerExpires.Equal(renewedExpiry) {
		t.Fatalf("tracker = %v, want the renewed %v (the pair must survive together)", fixture.trackerExpires, renewedExpiry)
	}
	// Reporting "removed" here would make the caller run its liveness check and
	// enqueue a block that a live upload still owns.
	if removed {
		t.Fatal("a release that removed nothing must not report a removal: the caller would promote a still-pinned block to a GC candidate")
	}
}

// With no renewal the release must still fully clean up, or every completed upload
// would leave a tracker behind for the scanner to walk until its deadline.
func TestReleaseProvisionalBlockReference_RetiresTheObservedPair(t *testing.T) {
	fixture := &provisionalPairFixture{
		refCreatedAt:   time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond),
		refPresent:     true,
		trackerExpires: time.Now().UTC().Add(2 * time.Hour).Truncate(time.Millisecond),
	}
	fixture.install(t)

	removed, err := (&DB{}).ReleaseProvisionalBlockReference("org-1", "block-1", "up:session-1")
	if err != nil {
		t.Fatalf("ReleaseProvisionalBlockReference() error = %v, want nil", err)
	}
	if !removed {
		t.Fatal("expected the provisional reference to be reported removed")
	}
	if fixture.refPresent {
		t.Fatal("expected the provisional reference to be removed")
	}
	if !fixture.trackerExpires.IsZero() {
		t.Fatalf("expected the observed tracker to be retired, got %v", fixture.trackerExpires)
	}
}

// A tracker-cleanup failure must not hide the fact that the reference is gone. The
// caller uses that signal to run its liveness check; swallowing it leaves a block
// at zero references with no candidate and — once the orphaned projection is swept —
// no discovery path at all.
func TestReleaseProvisionalBlockReference_ReportsRemovalEvenIfTrackerCleanupFails(t *testing.T) {
	fixture := &provisionalPairFixture{
		refCreatedAt:   time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond),
		refPresent:     true,
		trackerExpires: time.Now().UTC().Add(2 * time.Hour).Truncate(time.Millisecond),
	}
	fixture.install(t)

	boom := errors.New("projection delete failed")
	releaseProvisionalDeleteTrackerFn = func(*DB, string, string, string, time.Time) (bool, error) {
		return true, boom
	}

	removed, err := (&DB{}).ReleaseProvisionalBlockReference("org-1", "block-1", "up:session-1")
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want wrapped %v", err, boom)
	}
	if !removed {
		t.Fatal("reference removal must still be reported so the caller can promote a now-zero-ref block")
	}
}

// Ordering is not a style choice: the reference must go first. A tracker without a
// reference is recoverable — Phase 0 sees the reference is gone, judges liveness and
// retires the tracker — while a reference without a tracker is invisible forever. A
// crash between the two steps must therefore land on the recoverable side.
func TestReleaseProvisionalBlockReference_DropsTheReferenceBeforeTheTracker(t *testing.T) {
	oldReadExpiry := releaseProvisionalReadExpiryFn
	oldReadRef := releaseProvisionalReadReferenceFn
	oldRemove := releaseProvisionalRemoveReferenceFn
	oldDelete := releaseProvisionalDeleteTrackerFn
	t.Cleanup(func() {
		releaseProvisionalReadExpiryFn = oldReadExpiry
		releaseProvisionalReadReferenceFn = oldReadRef
		releaseProvisionalRemoveReferenceFn = oldRemove
		releaseProvisionalDeleteTrackerFn = oldDelete
	})

	var order []string
	releaseProvisionalReadExpiryFn = func(*DB, string, string, string) (time.Time, error) {
		order = append(order, "read_tracker")
		return time.Now().UTC().Add(time.Hour), nil
	}
	releaseProvisionalReadReferenceFn = func(*DB, string, string, string) (time.Time, bool, error) {
		order = append(order, "read_reference")
		return time.Now().UTC(), true, nil
	}
	releaseProvisionalRemoveReferenceFn = func(*DB, string, string, string, time.Time) (provisionalReferenceReleaseOutcome, error) {
		order = append(order, "remove_reference")
		return provisionalReferenceRemoved, nil
	}
	releaseProvisionalDeleteTrackerFn = func(*DB, string, string, string, time.Time) (bool, error) {
		order = append(order, "delete_tracker")
		return true, nil
	}

	if _, err := (&DB{}).ReleaseProvisionalBlockReference("org-1", "block-1", "up:session-1"); err != nil {
		t.Fatalf("ReleaseProvisionalBlockReference() error = %v", err)
	}
	want := []string{"read_tracker", "read_reference", "remove_reference", "delete_tracker"}
	if len(order) != len(want) {
		t.Fatalf("order = %#v, want %#v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order[%d] = %q, want %q (full=%#v)", i, order[i], want[i], order)
		}
	}
}

// A failed reference delete must not go on to retire the tracker: that would
// manufacture the forbidden state directly.
func TestReleaseProvisionalBlockReference_KeepsTrackerWhenReferenceDeleteFails(t *testing.T) {
	fixture := &provisionalPairFixture{
		refCreatedAt:   time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond),
		refPresent:     true,
		trackerExpires: time.Now().UTC().Add(time.Hour).Truncate(time.Millisecond),
	}
	fixture.install(t)

	boom := errors.New("cassandra timeout")
	releaseProvisionalRemoveReferenceFn = func(*DB, string, string, string, time.Time) (provisionalReferenceReleaseOutcome, error) {
		return provisionalReferenceRenewed, boom
	}
	releaseProvisionalDeleteTrackerFn = func(*DB, string, string, string, time.Time) (bool, error) {
		t.Fatal("tracker must not be retired when the reference is still there")
		return false, nil
	}

	removed, err := (&DB{}).ReleaseProvisionalBlockReference("org-1", "block-1", "up:session-1")
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want wrapped %v", err, boom)
	}
	if removed {
		t.Fatal("a failed reference delete must not be reported as a removal")
	}
}
