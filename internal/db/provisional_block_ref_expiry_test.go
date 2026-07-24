package db

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// provisionalPairFixture models the two rows a release looks at — the reference and
// its tracker, both stamped with the same generation — so a renewal can be injected
// at any step and the surviving state inspected.
type provisionalPairFixture struct {
	generationID uuid.UUID
	expiresAt    time.Time
	trackerFound bool
	refPresent   bool
}

func newProvisionalPairFixture() *provisionalPairFixture {
	return &provisionalPairFixture{
		generationID: uuid.New(),
		expiresAt:    time.Now().UTC().Add(2 * time.Hour).Truncate(time.Millisecond),
		trackerFound: true,
		refPresent:   true,
	}
}

// renew models AddProvisionalBlockReferenceWithExpiry: one new generation stamped on
// both rows in a single batch.
func (f *provisionalPairFixture) renew() uuid.UUID {
	f.generationID = uuid.New()
	f.expiresAt = time.Now().UTC().Add(48 * time.Hour).Truncate(time.Millisecond)
	f.trackerFound = true
	f.refPresent = true
	return f.generationID
}

func (f *provisionalPairFixture) install(t *testing.T) {
	t.Helper()
	oldRead := releaseProvisionalReadGenerationFn
	oldRemove := releaseProvisionalRemoveReferenceFn
	t.Cleanup(func() {
		releaseProvisionalReadGenerationFn = oldRead
		releaseProvisionalRemoveReferenceFn = oldRemove
	})

	releaseProvisionalReadGenerationFn = func(*DB, string, string, string) (ProvisionalBlockRefGeneration, error) {
		return ProvisionalBlockRefGeneration{
			GenerationID: f.generationID,
			ExpiresAt:    f.expiresAt,
			Found:        f.trackerFound,
		}, nil
	}
	releaseProvisionalRemoveReferenceFn = func(_ *DB, _, _, _ string, generationID uuid.UUID) (provisionalReferenceReleaseOutcome, error) {
		if !f.refPresent {
			return provisionalReferenceAbsent, nil
		}
		if f.generationID != generationID {
			return provisionalReferenceRenewed, nil
		}
		f.refPresent = false
		return provisionalReferenceRemoved, nil
	}
}

// A renewal landing after the release observed the pair must keep BOTH its rows: the
// compare refuses the delete, and the caller is told nothing was removed so it cannot
// run a liveness check on a block a live upload still owns.
func TestReleaseProvisionalBlockReference_KeepsARenewalThatLandsMidRelease(t *testing.T) {
	fixture := newProvisionalPairFixture()
	fixture.install(t)

	// Renew between the observation and the delete — the window that previously
	// produced a mixed identity and let the delete through.
	inner := releaseProvisionalReadGenerationFn
	releaseProvisionalReadGenerationFn = func(d *DB, orgID, blockID, referrer string) (ProvisionalBlockRefGeneration, error) {
		observed, err := inner(d, orgID, blockID, referrer)
		fixture.renew()
		return observed, err
	}

	removed, err := (&DB{}).ReleaseProvisionalBlockReference("org-1", "block-1", "up:session-1")
	if err != nil {
		t.Fatalf("ReleaseProvisionalBlockReference() error = %v, want nil", err)
	}
	if !fixture.refPresent {
		t.Fatal("release deleted a reference that had already been renewed: the live upload is unpinned")
	}
	if removed {
		t.Fatal("a release that removed nothing must not report a removal: the caller would promote a still-pinned block to a GC candidate")
	}
}

// TestReleaseProvisionalBlockReference_TakesTheIdentityFromASingleRead is the
// regression for the mixed-observation bug. The identity used to be composed from two
// reads — the tracker's expires_at and the reference's created_at — so a renewal
// landing between them produced an identity that matched the NEW reference, and the
// compare deleted a reference belonging to a live upload. One read cannot mix.
func TestReleaseProvisionalBlockReference_TakesTheIdentityFromASingleRead(t *testing.T) {
	fixture := newProvisionalPairFixture()
	fixture.install(t)

	reads := 0
	inner := releaseProvisionalReadGenerationFn
	releaseProvisionalReadGenerationFn = func(d *DB, orgID, blockID, referrer string) (ProvisionalBlockRefGeneration, error) {
		reads++
		return inner(d, orgID, blockID, referrer)
	}

	var comparedWith uuid.UUID
	innerRemove := releaseProvisionalRemoveReferenceFn
	releaseProvisionalRemoveReferenceFn = func(d *DB, orgID, blockID, referrer string, generationID uuid.UUID) (provisionalReferenceReleaseOutcome, error) {
		comparedWith = generationID
		return innerRemove(d, orgID, blockID, referrer, generationID)
	}

	observedGeneration := fixture.generationID
	if _, err := (&DB{}).ReleaseProvisionalBlockReference("org-1", "block-1", "up:session-1"); err != nil {
		t.Fatalf("ReleaseProvisionalBlockReference() error = %v", err)
	}

	if reads != 1 {
		t.Fatalf("identity reads = %d, want exactly 1 (a second read can observe a different generation and mix the two)", reads)
	}
	if comparedWith != observedGeneration {
		t.Fatalf("compared with %v, want the single observed generation %v", comparedWith, observedGeneration)
	}
}

// With no renewal the observed reference is released and reported.
func TestReleaseProvisionalBlockReference_RemovesTheObservedReference(t *testing.T) {
	fixture := newProvisionalPairFixture()
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
}

// TestReleaseProvisionalBlockReference_NeverRetiresTheTracker pins the property that
// makes the zero-reference transition durable. Phase 0 is the only path allowed to
// retire tracking, and it does so only after resolving liveness. If the release
// retired the tracker — even successfully — then a crash, a failed liveness check or
// a failed enqueue immediately afterwards would leave the block at zero references
// with no candidate and no tracking left to rediscover it.
func TestReleaseProvisionalBlockReference_NeverRetiresTheTracker(t *testing.T) {
	fixture := newProvisionalPairFixture()
	fixture.install(t)

	if _, err := (&DB{}).ReleaseProvisionalBlockReference("org-1", "block-1", "up:session-1"); err != nil {
		t.Fatalf("ReleaseProvisionalBlockReference() error = %v", err)
	}

	if !fixture.trackerFound {
		t.Fatal("the release retired the tracker: a downstream failure would now leave the block undiscoverable")
	}
	if !fixture.expiresAt.Equal(fixture.expiresAt) || fixture.generationID == uuid.Nil {
		t.Fatal("the tracker must be left exactly as observed")
	}
}

// Without a tracker there is no generation to compare against, so the reference is
// left alone rather than deleted unguarded.
func TestReleaseProvisionalBlockReference_LeavesReferenceWhenTrackerIsMissing(t *testing.T) {
	fixture := newProvisionalPairFixture()
	fixture.trackerFound = false
	fixture.install(t)

	releaseProvisionalRemoveReferenceFn = func(*DB, string, string, string, uuid.UUID) (provisionalReferenceReleaseOutcome, error) {
		t.Fatal("no tracker means no observed generation: deleting the reference here would be the unguarded delete")
		return provisionalReferenceAbsent, nil
	}

	removed, err := (&DB{}).ReleaseProvisionalBlockReference("org-1", "block-1", "up:session-1")
	if err != nil {
		t.Fatalf("ReleaseProvisionalBlockReference() error = %v", err)
	}
	if removed {
		t.Fatal("nothing was removed, so nothing may be reported")
	}
}

// An already-absent reference is a successful idempotent release: the caller may go
// on to check liveness, because no reference of this referrer is pinning the block.
func TestReleaseProvisionalBlockReference_ReportsAbsentReferenceAsReleased(t *testing.T) {
	fixture := newProvisionalPairFixture()
	fixture.refPresent = false
	fixture.install(t)

	removed, err := (&DB{}).ReleaseProvisionalBlockReference("org-1", "block-1", "up:session-1")
	if err != nil {
		t.Fatalf("ReleaseProvisionalBlockReference() error = %v", err)
	}
	if !removed {
		t.Fatal("an already-absent reference must not block the caller's liveness check")
	}
}

// A failed delete must not be reported as a removal, or the caller would run its
// liveness check while the reference is still there.
func TestReleaseProvisionalBlockReference_DoesNotReportRemovalOnError(t *testing.T) {
	fixture := newProvisionalPairFixture()
	fixture.install(t)

	boom := errors.New("cassandra timeout")
	releaseProvisionalRemoveReferenceFn = func(*DB, string, string, string, uuid.UUID) (provisionalReferenceReleaseOutcome, error) {
		return provisionalReferenceRenewed, boom
	}

	removed, err := (&DB{}).ReleaseProvisionalBlockReference("org-1", "block-1", "up:session-1")
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want wrapped %v", err, boom)
	}
	if removed {
		t.Fatal("a failed reference delete must not be reported as a removal")
	}
}
