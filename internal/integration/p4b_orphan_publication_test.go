//go:build integration

package integration

import (
	"fmt"
	"os"
	"testing"
	"time"

	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

const p4bRequireEvidenceEnv = "SESAMEFS_REQUIRE_P4B_EVIDENCE"

type p4bEvidenceGate struct{ observed bool }

func p4bRequireEvidence(t *testing.T) *p4bEvidenceGate {
	t.Helper()
	gate := &p4bEvidenceGate{}
	if os.Getenv(p4bRequireEvidenceEnv) != "1" {
		return gate
	}
	t.Cleanup(func() {
		if t.Skipped() {
			t.Errorf("%s=1 requires real Cassandra P4b evidence, but the test skipped", p4bRequireEvidenceEnv)
		} else if !t.Failed() && !gate.observed {
			t.Errorf("%s=1 completed without orphan-publication evidence", p4bRequireEvidenceEnv)
		}
	})
	return gate
}

// TestP4B_OrphanPublicationIsWriteOnceAtRealCassandra proves the part a mock
// cannot prove: Cassandra's actual IF NOT EXISTS response and the durable row
// values that a retry observes. A same-target retry must preserve the original
// lifecycle token and payload, while a different target must be reported without
// modifying the existing row. The projection is checked separately because the
// canonical and discovery tables are different partitions.
func TestP4B_OrphanPublicationIsWriteOnceAtRealCassandra(t *testing.T) {
	requireCassandra(t)
	gate := p4bRequireEvidence(t)

	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	orgID := uuid.New()
	blockID := fmt.Sprintf("p4b-write-once-%d", time.Now().UnixNano())
	firstSeenAt := time.Now().UTC().Truncate(time.Millisecond)
	storageKey := syntheticCanonicalStorageKeyForTest(orgID.String(), blockID)
	t.Cleanup(func() {
		if err := store.DeleteS3Orphan(orgID, blockID, firstSeenAt); err != nil {
			t.Logf("cleanup DeleteS3Orphan: %v", err)
		}
	})

	created := store.StartBlockDeleteOrphan(orgID, blockID, "hot", storageKey, "sha1-first", firstSeenAt)
	if created.Outcome != gcpkg.StartBlockDeleteOrphanCreated || !created.FirstSeenAt.Equal(firstSeenAt) {
		t.Fatalf("first publication = outcome:%s first_seen_at:%v cause:%v, want created at %v", created.Outcome, created.FirstSeenAt, created.Cause, firstSeenAt)
	}

	readCanonical := func() (storageClass, storedKey, externalSHA1, phase string, storedFirstSeenAt time.Time, retryCount int, lastError string) {
		err := database.Session().Query(`
			SELECT storage_class, storage_key, external_sha1, recovery_phase,
			       first_seen_at, retry_count, last_error
			FROM gc_s3_orphans
			WHERE org_id = ? AND block_id = ?
		`, orgID.String(), blockID).Consistency(gocql.EachQuorum).Scan(
			&storageClass, &storedKey, &externalSHA1, &phase, &storedFirstSeenAt, &retryCount, &lastError,
		)
		if err != nil {
			t.Fatalf("read canonical orphan: %v", err)
		}
		return
	}
	assertCanonical := func(label string) {
		storageClass, storedKey, externalSHA1, phase, storedFirstSeenAt, retryCount, lastError := readCanonical()
		if storageClass != "hot" || storedKey != storageKey || externalSHA1 != "sha1-first" || phase != gcpkg.S3OrphanPhasePendingS3 || !storedFirstSeenAt.Equal(firstSeenAt) || retryCount != 0 || lastError != "" {
			t.Fatalf("%s changed canonical orphan: class=%q key=%q sha1=%q phase=%q first_seen_at=%v retries=%d error=%q", label, storageClass, storedKey, externalSHA1, phase, storedFirstSeenAt, retryCount, lastError)
		}
	}
	assertCanonical("first publication")
	if err := database.Session().Query(`
		DELETE FROM gc_s3_orphans_by_day
		WHERE first_seen_day = ? AND bucket = ? AND first_seen_at = ? AND org_id = ? AND block_id = ?
	`, dbpkg.GCProjectionUTCDate(firstSeenAt), dbpkg.GCDiscoveryBucket(orgID.String(), blockID), firstSeenAt.UTC(), orgID.String(), blockID).Exec(); err != nil {
		t.Fatalf("delete discovery projection before same-target repair: %v", err)
	}
	if discovery, err := store.ListS3OrphansByDay(firstSeenAt, dbpkg.GCDiscoveryBucket(orgID.String(), blockID), 10); err != nil {
		t.Fatalf("list discovery projection after delete: %v", err)
	} else if len(discovery) != 0 {
		t.Fatalf("discovery projection still present before repair: %+v", discovery)
	}

	sameTarget := store.StartBlockDeleteOrphan(orgID, blockID, "hot", storageKey, "sha1-second", firstSeenAt.Add(time.Hour))
	if sameTarget.Outcome != gcpkg.StartBlockDeleteOrphanSameTarget || !sameTarget.FirstSeenAt.Equal(firstSeenAt) {
		t.Fatalf("same-target retry = outcome:%s first_seen_at:%v cause:%v, want same_target at %v", sameTarget.Outcome, sameTarget.FirstSeenAt, sameTarget.Cause, firstSeenAt)
	}
	assertCanonical("same-target retry")

	differentTarget := store.StartBlockDeleteOrphan(orgID, blockID, "cold", storageKey, "sha1-third", firstSeenAt.Add(2*time.Hour))
	if differentTarget.Outcome != gcpkg.StartBlockDeleteOrphanDifferentTarget {
		t.Fatalf("different-target retry = outcome:%s existing=%+v cause:%v, want different_target", differentTarget.Outcome, differentTarget.ExistingTarget, differentTarget.Cause)
	}
	assertCanonical("different-target retry")

	discovery, err := store.ListS3OrphansByDay(firstSeenAt, dbpkg.GCDiscoveryBucket(orgID.String(), blockID), 10)
	if err != nil {
		t.Fatalf("list orphan discovery projection: %v", err)
	}
	if len(discovery) != 1 || discovery[0].OrgID != orgID || discovery[0].BlockID != blockID || !discovery[0].FirstSeenAt.Equal(firstSeenAt) {
		t.Fatalf("discovery projection = %+v, want one identity row for the stored lifecycle", discovery)
	}

	gate.observed = true
	t.Logf("P4B_ORPHAN_PUBLICATION_EVIDENCE created=1 same_target=1 different_target=1 projection=1 first_seen_at_preserved=1")
}
