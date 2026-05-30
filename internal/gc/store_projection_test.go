package gc

import (
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/google/uuid"
)

func TestStore_EnsureBlockGCCandidate_RepairsMissingProjection(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	candidateAt := time.Now().Add(-24 * time.Hour).UTC().Truncate(time.Millisecond)

	store.AddBlockGCCandidate(orgID, "block-repair", "hot", candidateAt)
	store.DeleteBlockGCCandidateProjectionForTest(orgID, "block-repair", candidateAt)

	effectiveCandidateAt, err := store.EnsureBlockGCCandidate(orgID, "block-repair", "cold", time.Now())
	if err != nil {
		t.Fatalf("EnsureBlockGCCandidate failed: %v", err)
	}
	if !effectiveCandidateAt.Equal(candidateAt) {
		t.Fatalf("effective candidate_at = %v, want %v", effectiveCandidateAt, candidateAt)
	}

	candidates, err := store.ListBlockGCCandidatesByDay(candidateAt, db.GCDiscoveryBucket(orgID.String(), "block-repair"))
	if err != nil {
		t.Fatalf("ListBlockGCCandidatesByDay failed: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected repaired discovery row, got %d candidates", len(candidates))
	}
	if !candidates[0].CandidateAt.Equal(candidateAt) {
		t.Fatalf("projection candidate_at = %v, want %v", candidates[0].CandidateAt, candidateAt)
	}
	if candidates[0].StorageClass != "hot" {
		t.Fatalf("projection storage_class = %q, want %q", candidates[0].StorageClass, "hot")
	}
}

func TestStore_EnsureBlockGCCandidate_PrefersEarlierCandidateAt(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	later := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Millisecond)
	earlier := later.Add(-24 * time.Hour).UTC().Truncate(time.Millisecond)

	store.AddBlockGCCandidate(orgID, "block-earlier", "hot", later)

	effectiveCandidateAt, err := store.EnsureBlockGCCandidate(orgID, "block-earlier", "cold", earlier)
	if err != nil {
		t.Fatalf("EnsureBlockGCCandidate failed: %v", err)
	}
	if !effectiveCandidateAt.Equal(earlier) {
		t.Fatalf("effective candidate_at = %v, want %v", effectiveCandidateAt, earlier)
	}

	olderDayCandidates, err := store.ListBlockGCCandidatesByDay(earlier, db.GCDiscoveryBucket(orgID.String(), "block-earlier"))
	if err != nil {
		t.Fatalf("ListBlockGCCandidatesByDay(earlier) failed: %v", err)
	}
	if len(olderDayCandidates) != 1 {
		t.Fatalf("expected 1 earlier candidate row, got %d", len(olderDayCandidates))
	}
	if !olderDayCandidates[0].CandidateAt.Equal(earlier) {
		t.Fatalf("earlier projection candidate_at = %v, want %v", olderDayCandidates[0].CandidateAt, earlier)
	}
	if olderDayCandidates[0].StorageClass != "hot" {
		t.Fatalf("earlier projection storage_class = %q, want %q", olderDayCandidates[0].StorageClass, "hot")
	}

	laterDayCandidates, err := store.ListBlockGCCandidatesByDay(later, db.GCDiscoveryBucket(orgID.String(), "block-earlier"))
	if err != nil {
		t.Fatalf("ListBlockGCCandidatesByDay(later) failed: %v", err)
	}
	if len(laterDayCandidates) != 0 {
		t.Fatalf("expected stale later projection to be removed, got %d rows", len(laterDayCandidates))
	}
}

func TestStore_RecordS3Orphan_RepairsMissingProjectionAndPreservesFirstSeenAt(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	firstSeenAt := time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Millisecond)

	if _, err := store.RecordS3Orphan(orgID, "orph-repair", "hot", "prev", firstSeenAt); err != nil {
		t.Fatalf("initial RecordS3Orphan failed: %v", err)
	}
	store.DeleteS3OrphanProjectionForTest(orgID, "orph-repair", firstSeenAt)

	effectiveFirstSeenAt, err := store.RecordS3Orphan(orgID, "orph-repair", "cold", "", time.Now())
	if err != nil {
		t.Fatalf("repair RecordS3Orphan failed: %v", err)
	}
	if !effectiveFirstSeenAt.Equal(firstSeenAt) {
		t.Fatalf("effective first_seen_at = %v, want %v", effectiveFirstSeenAt, firstSeenAt)
	}

	orphans, err := store.ListS3OrphansByDay(firstSeenAt, db.GCDiscoveryBucket(orgID.String(), "orph-repair"), 10)
	if err != nil {
		t.Fatalf("ListS3OrphansByDay failed: %v", err)
	}
	if len(orphans) != 1 {
		t.Fatalf("expected repaired S3 orphan discovery row, got %d rows", len(orphans))
	}
	if !orphans[0].FirstSeenAt.Equal(firstSeenAt) {
		t.Fatalf("projection first_seen_at = %v, want %v", orphans[0].FirstSeenAt, firstSeenAt)
	}
	if orphans[0].StorageClass != "hot" {
		t.Fatalf("projection storage_class = %q, want %q", orphans[0].StorageClass, "hot")
	}
}
