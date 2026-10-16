//go:build integration

package integration

import (
	"testing"

	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

// e1DisposeOwnedRecovery runs only after E1-10e/E1-17's original metadata and
// exact-K teardown. It disposes test data, not productive recovery evidence.
// The callers register it before x1Cleanup (Cleanup runs in reverse order),
// and capture the real authority without changing COMMITTED outcome assertions.
func e1DisposeOwnedRecovery(t *testing.T, fx *w2CreateFileFixture, authority gcpkg.BlockDeleteAuthority) {
	t.Helper()
	for _, table := range []string{"blocks", "block_references", "gc_block_delete_lifecycles"} {
		var count int
		if err := fx.database.Session().Query("SELECT count(*) FROM "+table+" WHERE org_id=? AND block_id=?", fx.orgID, fx.blockID).Consistency(gocql.EachQuorum).Scan(&count); err != nil || count != 0 {
			t.Errorf("refuse owned disposal: %s count=%d err=%v block=%s", table, count, err, fx.blockID)
			return
		}
	}
	if !authority.IsZero() {
		if authority.Target != fx.target {
			t.Error("refuse owned disposal: authority differs from fixture's original target")
			return
		}
		store := gcpkg.NewCassandraStore(fx.database)
		orphan, orphanFound, err := store.GetS3OrphanExact(fx.orgUUID, fx.blockID, authority)
		if err != nil {
			t.Errorf("read owned orphan: %v", err)
			return
		}
		root, rootFound, err := store.GetS3OrphanRecoveryRootExact(fx.orgUUID, fx.blockID, authority)
		if err != nil {
			t.Errorf("read owned root: %v", err)
			return
		}
		firstSeen := orphan.FirstSeenAt
		if !orphanFound {
			firstSeen = root.FirstSeenAt
		}
		if orphanFound || rootFound {
			if err := store.DeleteS3Orphan(fx.orgUUID, fx.blockID, authority, firstSeen); err != nil {
				t.Errorf("dispose owned recovery: %v", err)
				return
			}
		}
	}
	clean := true
	for _, table := range []string{"gc_s3_orphans", "gc_s3_orphan_recovery_roots", "gc_s3_orphans_by_day"} {
		var count int
		if err := fx.database.Session().Query("SELECT count(*) FROM "+table+" WHERE org_id=? AND block_id=? ALLOW FILTERING", fx.orgID, fx.blockID).Consistency(gocql.EachQuorum).Scan(&count); err != nil || count != 0 {
			clean = false
			t.Errorf("owned teardown %s count=%d err=%v block=%s", table, count, err, fx.blockID)
		}
	}
	if clean {
		t.Logf("E1_OWNED_TEARDOWN_CLEAN block=%s", fx.blockID)
	}
}
