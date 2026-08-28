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

	store.AddBlock(orgID, "block-repair", "hot", 0)
	store.AddBlockGCCandidate(orgID, "block-repair", "hot", candidateAt)
	store.DeleteBlockGCCandidateProjectionForTest(orgID, "block-repair", candidateAt)

	candidate, err := store.EnsureBlockGCCandidateExact(orgID, "block-repair", "cold", time.Now())
	if err != nil {
		t.Fatalf("EnsureBlockGCCandidateExact failed: %v", err)
	}
	if !candidate.CandidateAt.Equal(candidateAt) {
		t.Fatalf("effective candidate_at = %v, want %v", candidate.CandidateAt, candidateAt)
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
	if candidates[0].StorageClass() != "hot" {
		t.Fatalf("projection storage_class = %q, want %q", candidates[0].StorageClass(), "hot")
	}
}

func TestStore_EnsureBlockGCCandidate_PrefersEarlierCandidateAt(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	later := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Millisecond)
	earlier := later.Add(-24 * time.Hour).UTC().Truncate(time.Millisecond)

	store.AddBlock(orgID, "block-earlier", "hot", 0)
	store.AddBlockGCCandidate(orgID, "block-earlier", "hot", later)

	candidate, err := store.EnsureBlockGCCandidateExact(orgID, "block-earlier", "cold", earlier)
	if err != nil {
		t.Fatalf("EnsureBlockGCCandidateExact failed: %v", err)
	}
	if !candidate.CandidateAt.Equal(earlier) {
		t.Fatalf("effective candidate_at = %v, want %v", candidate.CandidateAt, earlier)
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
	if olderDayCandidates[0].StorageClass() != "hot" {
		t.Fatalf("earlier projection storage_class = %q, want %q", olderDayCandidates[0].StorageClass(), "hot")
	}

	laterDayCandidates, err := store.ListBlockGCCandidatesByDay(later, db.GCDiscoveryBucket(orgID.String(), "block-earlier"))
	if err != nil {
		t.Fatalf("ListBlockGCCandidatesByDay(later) failed: %v", err)
	}
	if len(laterDayCandidates) != 0 {
		t.Fatalf("expected stale later projection to be removed, got %d rows", len(laterDayCandidates))
	}
}

func TestStore_BlockGCCandidatesKeepMultipleIncarnationsAndRequireExactIdentity(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	now := time.Now().UTC()
	p1At := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.UTC).Add(-24 * time.Hour)
	p2At := p1At.Add(time.Hour)

	store.AddBlock(orgID, "block-incarnations", "hot", 0)
	p1, err := store.EnsureBlockGCCandidateExact(orgID, "block-incarnations", "hot", p1At)
	if err != nil {
		t.Fatalf("ensure P1: %v", err)
	}
	store.SetBlockStorageKeyForTest(orgID, "block-incarnations", p1.Target.StorageKey+".remint")
	p2, err := store.EnsureBlockGCCandidateExact(orgID, "block-incarnations", "hot", p2At)
	if err != nil {
		t.Fatalf("ensure P2: %v", err)
	}
	if p1.Target == p2.Target {
		t.Fatal("P2 candidate reused P1's physical identity")
	}
	if got := store.AllBlockGCCandidates(); len(got) != 2 {
		t.Fatalf("candidate rows = %d, want 2 coexisting incarnations", len(got))
	}
	bucket := db.GCDiscoveryBucket(orgID.String(), "block-incarnations")
	if projected, err := store.ListBlockGCCandidatesByDay(p1At, bucket); err != nil || len(projected) != 2 {
		t.Fatalf("coexisting projections: rows=%d err=%v, want 2", len(projected), err)
	}
	if _, ok, err := store.GetBlockGCCandidateExact(orgID, "block-incarnations", p1.Identity()); err != nil || !ok {
		t.Fatalf("exact P1 get: ok=%v err=%v", ok, err)
	}
	stale := p1.Identity()
	stale.CandidateAt = stale.CandidateAt.Add(time.Millisecond)
	if _, ok, err := store.GetBlockGCCandidateExact(orgID, "block-incarnations", stale); err != nil || ok {
		t.Fatalf("stale exact get: ok=%v err=%v, want stale refusal", ok, err)
	}
	if err := store.DeleteBlockGCCandidate(orgID, "block-incarnations", p1.Identity()); err != nil {
		t.Fatalf("delete P1: %v", err)
	}
	if projected, err := store.ListBlockGCCandidatesByDay(p1At, bucket); err != nil || len(projected) != 1 || projected[0].Target != p2.Target {
		t.Fatalf("P1 projection cleanup: rows=%+v err=%v, want only P2", projected, err)
	}
	if _, ok, err := store.GetBlockGCCandidateExact(orgID, "block-incarnations", p2.Identity()); err != nil || !ok {
		t.Fatalf("exact P2 after P1 delete: ok=%v err=%v", ok, err)
	}
}

func TestStore_StartBlockDeleteOrphan_SameTargetUsesStoredFirstSeenAndRepairsProjection(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	firstSeenAt := time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Millisecond)

	seedS3Orphan(t, store, orgID, "orph-repair", "hot", "sha1-old", "prev", firstSeenAt)
	store.DeleteS3OrphanProjectionForTest(orgID, "orph-repair", firstSeenAt)

	proposedFirstSeenAt := firstSeenAt.Add(24 * time.Hour)
	result := store.StartBlockDeleteOrphan(orgID, "orph-repair", "hot", MockCanonicalStorageKey(orgID.String(), "orph-repair"), "sha1-new", proposedFirstSeenAt)
	if result.Outcome != StartBlockDeleteOrphanSameTarget {
		t.Fatalf("StartBlockDeleteOrphan outcome = %s, want same_target (cause=%v)", result.Outcome, result.Cause)
	}
	if !result.FirstSeenAt.Equal(firstSeenAt) {
		t.Fatalf("effective first_seen_at = %v, want stored %v", result.FirstSeenAt, firstSeenAt)
	}

	discovery, err := store.ListS3OrphansByDay(firstSeenAt, db.GCDiscoveryBucket(orgID.String(), "orph-repair"), 10)
	if err != nil {
		t.Fatalf("ListS3OrphansByDay failed: %v", err)
	}
	if len(discovery) != 1 {
		t.Fatalf("expected repaired S3 orphan discovery row, got %d rows", len(discovery))
	}
	if !discovery[0].FirstSeenAt.Equal(firstSeenAt) {
		t.Fatalf("projection first_seen_at = %v, want %v", discovery[0].FirstSeenAt, firstSeenAt)
	}
	// The repaired row is checked by identity, not payload: since R22b the
	// projection has no payload to re-derive, and a same-target resume must use
	// the stored lifecycle token rather than the new proposed timestamp.
	projection, ok := store.GetS3OrphanProjectionForTest(orgID, "orph-repair", firstSeenAt)
	if !ok {
		t.Fatal("expected repaired discovery row")
	}
	if projection.OrgID != orgID || projection.BlockID != "orph-repair" || !projection.FirstSeenAt.Equal(firstSeenAt) {
		t.Fatalf("repaired discovery identity = %+v, want org=%s block=%s first_seen_at=%v", projection, orgID, "orph-repair", firstSeenAt)
	}
}

func TestStore_StartBlockDeleteOrphan_DifferentTargetDoesNotOverwrite(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	firstSeenAt := time.Now().Add(-24 * time.Hour).UTC().Truncate(time.Millisecond)

	seedS3Orphan(t, store, orgID, "orph-conflict", "hot", "sha1-old", "prev", firstSeenAt)
	result := store.StartBlockDeleteOrphan(orgID, "orph-conflict", "cold", MockCanonicalStorageKey(orgID.String(), "orph-conflict"), "sha1-new", time.Now().UTC())
	if result.Outcome != StartBlockDeleteOrphanDifferentTarget {
		t.Fatalf("StartBlockDeleteOrphan outcome = %s, want different_target (cause=%v)", result.Outcome, result.Cause)
	}
	if result.ExistingTarget != (BlockDeleteTarget{StorageClass: "hot", StorageKey: MockCanonicalStorageKey(orgID.String(), "orph-conflict")}) {
		t.Fatalf("existing target = %+v, want the stored target", result.ExistingTarget)
	}

	orphans := store.AllS3Orphans()
	if len(orphans) != 1 || orphans[0].StorageClass != "hot" || orphans[0].ExternalSHA1 != "sha1-old" || orphans[0].RecoveryPhase != S3OrphanPhasePendingS3 || orphans[0].LastError != "prev" {
		t.Fatalf("existing orphan was overwritten: %+v", orphans)
	}
	discovery, err := store.ListS3OrphansByDay(firstSeenAt, db.GCDiscoveryBucket(orgID.String(), "orph-conflict"), 10)
	if err != nil {
		t.Fatalf("ListS3OrphansByDay failed: %v", err)
	}
	if len(discovery) != 1 || !discovery[0].FirstSeenAt.Equal(firstSeenAt) {
		t.Fatalf("existing discovery row changed: %+v", discovery)
	}
}

func TestStore_StartBlockDeleteOrphan_MalformedExistingIsInvalid(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	firstSeenAt := time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Millisecond)

	seedS3Orphan(t, store, orgID, "orph-malformed", "hot", "sha1-old", "prev", firstSeenAt)
	store.SetS3OrphanStorageKeyForTest(orgID, "orph-malformed", " ")
	result := store.StartBlockDeleteOrphan(orgID, "orph-malformed", "hot", MockCanonicalStorageKey(orgID.String(), "orph-malformed"), "sha1-new", time.Now().UTC())
	if result.Outcome != StartBlockDeleteOrphanInvalid {
		t.Fatalf("StartBlockDeleteOrphan outcome = %s, want invalid", result.Outcome)
	}
	if store.AllS3Orphans()[0].StorageKey != " " {
		t.Fatal("malformed existing orphan was repaired or overwritten")
	}
}
