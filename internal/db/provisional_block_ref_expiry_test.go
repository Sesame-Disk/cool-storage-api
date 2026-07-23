package db

import (
	"errors"
	"testing"
	"time"
)

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
	oldRead := releaseProvisionalReadExpiryFn
	oldRemove := releaseProvisionalRemoveReferenceFn
	oldDelete := releaseProvisionalDeleteTrackerFn
	t.Cleanup(func() {
		releaseProvisionalReadExpiryFn = oldRead
		releaseProvisionalRemoveReferenceFn = oldRemove
		releaseProvisionalDeleteTrackerFn = oldDelete
	})

	observed := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Millisecond)
	renewed := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Millisecond)

	// tracker models the single canonical row both the releaser and the renewing
	// upload write to.
	tracker := observed

	releaseProvisionalReadExpiryFn = func(*DB, string, string, string) (time.Time, error) {
		return tracker, nil
	}
	releaseProvisionalRemoveReferenceFn = func(*DB, string, string, string) error {
		// The renewal lands here: after the release captured its observation and
		// dropped the old reference, a retry of the same block under the same
		// session writes a fresh reference and moves the tracker forward.
		tracker = renewed
		return nil
	}
	var deletedWith []time.Time
	releaseProvisionalDeleteTrackerFn = func(_ *DB, _, _, _ string, expiresAt time.Time) (bool, error) {
		deletedWith = append(deletedWith, expiresAt)
		if !tracker.Equal(expiresAt) {
			return false, nil
		}
		tracker = time.Time{}
		return true, nil
	}

	if err := (&DB{}).ReleaseProvisionalBlockReference("org-1", "block-1", "up:session-1"); err != nil {
		t.Fatalf("ReleaseProvisionalBlockReference() error = %v, want nil", err)
	}

	if len(deletedWith) != 1 {
		t.Fatalf("tracker delete attempts = %d, want exactly 1", len(deletedWith))
	}
	// Conditioning on the observation is the whole fix: passing the re-read value
	// (or no condition at all) is what deletes the renewal's tracker.
	if !deletedWith[0].Equal(observed) {
		t.Fatalf("tracker delete conditioned on %v, want the observed %v", deletedWith[0], observed)
	}
	if tracker.IsZero() {
		t.Fatal("release deleted the tracker of a reference renewed mid-release: that reference is now undiscoverable by GC (F10)")
	}
	if !tracker.Equal(renewed) {
		t.Fatalf("tracker = %v, want the renewed %v", tracker, renewed)
	}
}

// With no renewal the release must still fully clean up, or every completed upload
// would leave a tracker behind for the scanner to walk until its deadline.
func TestReleaseProvisionalBlockReference_RetiresTheObservedPair(t *testing.T) {
	oldRead := releaseProvisionalReadExpiryFn
	oldRemove := releaseProvisionalRemoveReferenceFn
	oldDelete := releaseProvisionalDeleteTrackerFn
	t.Cleanup(func() {
		releaseProvisionalReadExpiryFn = oldRead
		releaseProvisionalRemoveReferenceFn = oldRemove
		releaseProvisionalDeleteTrackerFn = oldDelete
	})

	observed := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Millisecond)
	tracker := observed
	removed := false

	releaseProvisionalReadExpiryFn = func(*DB, string, string, string) (time.Time, error) { return tracker, nil }
	releaseProvisionalRemoveReferenceFn = func(*DB, string, string, string) error {
		removed = true
		return nil
	}
	releaseProvisionalDeleteTrackerFn = func(_ *DB, _, _, _ string, expiresAt time.Time) (bool, error) {
		if !tracker.Equal(expiresAt) {
			return false, nil
		}
		tracker = time.Time{}
		return true, nil
	}

	if err := (&DB{}).ReleaseProvisionalBlockReference("org-1", "block-1", "up:session-1"); err != nil {
		t.Fatalf("ReleaseProvisionalBlockReference() error = %v, want nil", err)
	}
	if !removed {
		t.Fatal("expected the provisional reference to be removed")
	}
	if !tracker.IsZero() {
		t.Fatalf("expected the observed tracker to be retired, got %v", tracker)
	}
}

// Ordering is not a style choice: the reference must go first. A tracker without a
// reference is recoverable — Phase 0 sees the reference is gone, judges liveness and
// retires the tracker — while a reference without a tracker is invisible forever. A
// crash between the two steps must therefore land on the recoverable side.
func TestReleaseProvisionalBlockReference_DropsTheReferenceBeforeTheTracker(t *testing.T) {
	oldRead := releaseProvisionalReadExpiryFn
	oldRemove := releaseProvisionalRemoveReferenceFn
	oldDelete := releaseProvisionalDeleteTrackerFn
	t.Cleanup(func() {
		releaseProvisionalReadExpiryFn = oldRead
		releaseProvisionalRemoveReferenceFn = oldRemove
		releaseProvisionalDeleteTrackerFn = oldDelete
	})

	var order []string
	releaseProvisionalReadExpiryFn = func(*DB, string, string, string) (time.Time, error) {
		order = append(order, "read")
		return time.Now().UTC().Add(time.Hour), nil
	}
	releaseProvisionalRemoveReferenceFn = func(*DB, string, string, string) error {
		order = append(order, "remove_reference")
		return nil
	}
	releaseProvisionalDeleteTrackerFn = func(*DB, string, string, string, time.Time) (bool, error) {
		order = append(order, "delete_tracker")
		return true, nil
	}

	if err := (&DB{}).ReleaseProvisionalBlockReference("org-1", "block-1", "up:session-1"); err != nil {
		t.Fatalf("ReleaseProvisionalBlockReference() error = %v", err)
	}
	want := []string{"read", "remove_reference", "delete_tracker"}
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
	oldRead := releaseProvisionalReadExpiryFn
	oldRemove := releaseProvisionalRemoveReferenceFn
	oldDelete := releaseProvisionalDeleteTrackerFn
	t.Cleanup(func() {
		releaseProvisionalReadExpiryFn = oldRead
		releaseProvisionalRemoveReferenceFn = oldRemove
		releaseProvisionalDeleteTrackerFn = oldDelete
	})

	boom := errors.New("cassandra timeout")
	releaseProvisionalReadExpiryFn = func(*DB, string, string, string) (time.Time, error) {
		return time.Now().UTC().Add(time.Hour), nil
	}
	releaseProvisionalRemoveReferenceFn = func(*DB, string, string, string) error { return boom }
	releaseProvisionalDeleteTrackerFn = func(*DB, string, string, string, time.Time) (bool, error) {
		t.Fatal("tracker must not be retired when the reference is still there")
		return false, nil
	}

	err := (&DB{}).ReleaseProvisionalBlockReference("org-1", "block-1", "up:session-1")
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want wrapped %v", err, boom)
	}
}
