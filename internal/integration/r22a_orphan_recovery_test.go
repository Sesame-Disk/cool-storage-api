//go:build integration

package integration

import (
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/google/uuid"
)

func TestGC_R22aCanonicalOrphanReadIgnoresProjectionPayload(t *testing.T) {
	requireCassandra(t)

	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	orgID := uuid.New()
	blockID := "r22a-canonical-" + uuid.NewString()
	firstSeenAt := time.Now().UTC().Truncate(time.Millisecond)
	effectiveFirstSeenAt := seedS3Orphan(t, store, orgID, blockID, "hot", db.PlainBlockRepresentationID, "sha1-canonical", "seed", firstSeenAt)
	t.Cleanup(func() {
		if err := store.DeleteS3Orphan(orgID, blockID, effectiveFirstSeenAt); err != nil {
			t.Errorf("cleanup DeleteS3Orphan: %v", err)
		}
	})

	projectionDay := db.GCProjectionUTCDate(firstSeenAt)
	bucket := db.GCDiscoveryBucket(orgID.String(), blockID)
	if err := database.Session().Query(`
		UPDATE gc_s3_orphans_by_day
		SET storage_class = ?, representation_id = ?, external_sha1 = ?, recovery_phase = ?
		WHERE first_seen_day = ? AND bucket = ? AND first_seen_at = ? AND org_id = ? AND block_id = ?
	`, "evil", "wrong-representation", "wrong-sha1", gcpkg.S3OrphanPhasePendingS3,
		projectionDay, bucket, firstSeenAt, orgID.String(), blockID).Exec(); err != nil {
		t.Fatalf("mutate stale discovery payload: %v", err)
	}

	canonical, found, err := store.GetS3OrphanGlobal(orgID, blockID)
	if err != nil {
		t.Fatalf("GetS3OrphanGlobal: %v", err)
	}
	if !found {
		t.Fatal("canonical orphan not found")
	}
	if canonical.StorageClass != "hot" || canonical.RepresentationID != db.PlainBlockRepresentationID || canonical.ExternalSHA1 != "sha1-canonical" {
		t.Fatalf("canonical orphan was affected by projection payload: %+v", canonical)
	}

	discovery, err := store.ListS3OrphansByDay(firstSeenAt, bucket, 10)
	if err != nil {
		t.Fatalf("ListS3OrphansByDay: %v", err)
	}
	if len(discovery) != 1 || discovery[0].OrgID != orgID || discovery[0].BlockID != blockID || !discovery[0].FirstSeenAt.Equal(firstSeenAt) {
		t.Fatalf("unexpected discovery identity: %+v", discovery)
	}
}
