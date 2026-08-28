//go:build integration

package integration

import (
	"errors"
	"fmt"
	"os"
	"strings"
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
	visible, found, err := store.GetS3OrphanGlobal(orgID, blockID)
	if err != nil || !found || visible.StorageClass != "hot" || visible.StorageKey != storageKey || !visible.FirstSeenAt.Equal(firstSeenAt) {
		t.Fatalf("same-target must confirm canonical EACH_QUORUM visibility: found=%v err=%v info=%+v", found, err, visible)
	}

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
	t.Logf("P4B_ORPHAN_PUBLICATION_EVIDENCE created=1 same_target=1 different_target=1 projection=1 first_seen_at_preserved=1 canonical_each_quorum=1")
}

// TestP4B_SerialSettlementClassifiesRealCassandra proves the SERIAL SELECT used
// after an uncertain LWT against real Cassandra. Row classification lives in
// package gc unit tests; this leg must not call an exported SERIAL-only helper
// that skips EACH_QUORUM confirmation.
func TestP4B_SerialSettlementClassifiesRealCassandra(t *testing.T) {
	requireCassandra(t)
	gate := p4bRequireEvidence(t)

	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	orgID := uuid.New()
	absentID := fmt.Sprintf("p4b-serial-absent-%d", time.Now().UnixNano())
	presentID := fmt.Sprintf("p4b-serial-present-%d", time.Now().UnixNano())
	firstSeenAt := time.Now().UTC().Truncate(time.Millisecond)
	storageKey := syntheticCanonicalStorageKeyForTest(orgID.String(), presentID)
	proposed := gcpkg.BlockDeleteTarget{StorageClass: "hot", StorageKey: storageKey}
	t.Cleanup(func() {
		if err := store.DeleteS3Orphan(orgID, presentID, firstSeenAt); err != nil {
			t.Logf("cleanup DeleteS3Orphan: %v", err)
		}
	})

	if found := scanCanonicalOrphanSERIAL(t, database, orgID, absentID); found {
		t.Fatal("SERIAL absence must not find a canonical orphan")
	}

	created := store.StartBlockDeleteOrphan(orgID, presentID, "hot", storageKey, "sha1-serial", firstSeenAt)
	if created.Outcome != gcpkg.StartBlockDeleteOrphanCreated {
		t.Fatalf("seed publication = %s cause=%v, want created", created.Outcome, created.Cause)
	}

	class, key, storedFirstSeenAt, found := mustScanCanonicalOrphanSERIAL(t, database, orgID, presentID)
	if !found || class != proposed.StorageClass || key != proposed.StorageKey || !storedFirstSeenAt.Equal(firstSeenAt) {
		t.Fatalf("SERIAL same-target row = found:%v class:%q key:%q first_seen_at:%v, want hot/%s at %v", found, class, key, storedFirstSeenAt, proposed.StorageKey, firstSeenAt)
	}
	other := gcpkg.BlockDeleteTarget{StorageClass: "cold", StorageKey: storageKey}
	if class == other.StorageClass && key == other.StorageKey {
		t.Fatal("SERIAL row matched a different-class proposal; exact P is (storage_class, storage_key)")
	}

	gate.observed = true
	t.Logf("P4B_SERIAL_SETTLEMENT_EVIDENCE absent=1 same_identity=1 different_class_mismatch=1")
}

func TestP4B_LifecycleAdvancedAtRealCassandra(t *testing.T) {
	requireCassandra(t)
	gate := p4bRequireEvidence(t)

	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	orgID := uuid.New()
	blockID := fmt.Sprintf("p4b-phase-%d", time.Now().UnixNano())
	firstSeenAt := time.Now().UTC().Truncate(time.Millisecond)
	storageKey := syntheticCanonicalStorageKeyForTest(orgID.String(), blockID)
	t.Cleanup(func() {
		if err := store.DeleteS3Orphan(orgID, blockID, firstSeenAt); err != nil {
			t.Logf("cleanup DeleteS3Orphan: %v", err)
		}
	})

	created := store.StartBlockDeleteOrphan(orgID, blockID, "hot", storageKey, "sha1-phase", firstSeenAt)
	if created.Outcome != gcpkg.StartBlockDeleteOrphanCreated {
		t.Fatalf("seed publication = %s cause=%v, want created", created.Outcome, created.Cause)
	}
	if err := store.MarkS3OrphanMappingCleanupPending(orgID, blockID, "sha1-phase", firstSeenAt.Add(time.Minute)); err != nil {
		t.Fatalf("advance recovery phase: %v", err)
	}

	advanced := store.StartBlockDeleteOrphan(orgID, blockID, "hot", storageKey, "sha1-new", firstSeenAt.Add(time.Hour))
	if advanced.Outcome != gcpkg.StartBlockDeleteOrphanLifecycleAdvanced || !advanced.FirstSeenAt.Equal(firstSeenAt) {
		t.Fatalf("advanced-phase retry = outcome:%s first_seen_at:%v cause:%v, want lifecycle_advanced at %v", advanced.Outcome, advanced.FirstSeenAt, advanced.Cause, firstSeenAt)
	}

	var phase string
	if err := database.Session().Query(`
		SELECT recovery_phase FROM gc_s3_orphans WHERE org_id = ? AND block_id = ?
	`, orgID.String(), blockID).Consistency(gocql.EachQuorum).Scan(&phase); err != nil {
		t.Fatalf("read recovery_phase: %v", err)
	}
	if phase != gcpkg.S3OrphanPhasePendingMappingCleanup {
		t.Fatalf("canonical phase = %q, want unchanged pending_mapping_cleanup", phase)
	}

	gate.observed = true
	t.Logf("P4B_LIFECYCLE_ADVANCED_EVIDENCE lifecycle_advanced=1 phase_preserved=1")
}

func TestP4B_CanonicalOrphanReadRepairIsBlocking(t *testing.T) {
	requireCassandra(t)
	gate := p4bRequireEvidence(t)

	keyspace := envOrDefault("CASSANDRA_KEYSPACE", "sesamefs")
	var readRepair string
	if err := shareProjectionDBForTest(t).Session().Query(`
		SELECT read_repair
		FROM system_schema.tables
		WHERE keyspace_name = ? AND table_name = ?
	`, keyspace, "gc_s3_orphans").Scan(&readRepair); err != nil {
		t.Fatalf("read effective read_repair for gc_s3_orphans: %v", err)
	}
	if strings.ToUpper(strings.TrimSpace(readRepair)) != "BLOCKING" {
		t.Fatalf("gc_s3_orphans effective read_repair = %q, want BLOCKING; empty is not treated as the default. SameTarget confirmation relies on blocking read repair at EACH_QUORUM", readRepair)
	}

	gate.observed = true
	t.Logf("P4B_READ_REPAIR_EVIDENCE blocking=1 value=%q", readRepair)
}

// scanCanonicalOrphanSERIAL issues the same SERIAL SELECT as settleS3OrphanState.
func scanCanonicalOrphanSERIAL(t *testing.T, database *dbpkg.DB, orgID uuid.UUID, blockID string) bool {
	t.Helper()
	_, _, _, found := readCanonicalOrphanSERIAL(t, database, orgID, blockID)
	return found
}

func mustScanCanonicalOrphanSERIAL(t *testing.T, database *dbpkg.DB, orgID uuid.UUID, blockID string) (string, string, time.Time, bool) {
	t.Helper()
	class, key, firstSeenAt, found := readCanonicalOrphanSERIAL(t, database, orgID, blockID)
	if !found {
		t.Fatal("SERIAL settlement read found no canonical orphan")
	}
	return class, key, firstSeenAt, found
}

func readCanonicalOrphanSERIAL(t *testing.T, database *dbpkg.DB, orgID uuid.UUID, blockID string) (string, string, time.Time, bool) {
	t.Helper()
	var storageClass, storageKey *string
	var firstSeenAt *time.Time
	err := database.Session().Query(`
		SELECT storage_class, storage_key, first_seen_at
		FROM gc_s3_orphans
		WHERE org_id = ? AND block_id = ?
	`, orgID.String(), blockID).
		Consistency(gocql.Serial).
		Scan(&storageClass, &storageKey, &firstSeenAt)
	if errors.Is(err, gocql.ErrNotFound) {
		return "", "", time.Time{}, false
	}
	if err != nil {
		t.Fatalf("SERIAL settlement read: %v", err)
	}
	class, key := "", ""
	if storageClass != nil {
		class = *storageClass
	}
	if storageKey != nil {
		key = *storageKey
	}
	seen := time.Time{}
	if firstSeenAt != nil {
		seen = firstSeenAt.UTC()
	}
	return class, key, seen, true
}
