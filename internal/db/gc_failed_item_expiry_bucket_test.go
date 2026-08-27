package db

import (
	"testing"
	"time"
)

// The expiry projection is the ONE GC discovery bucket whose input includes
// timestamps: every other one hashes ids and tokens only. That makes it the only
// place where the bucket can stop being reproducible, because a Cassandra
// TIMESTAMP holds milliseconds and a Go time.Time holds nanoseconds.
//
// The write side hashes a value that has never been to Cassandra — FailItem takes
// the worker's clock, which is time.Now — while every later delete hashes the same
// instant AFTER a round-trip, where the sub-millisecond part is gone. If the hash
// disagrees, the DELETE names a different partition than the INSERT did: Cassandra
// reports success, the row survives in its original bucket, and the sweep that
// enumerates every bucket finds it again on the next pass, forever. The canonical
// DLQ row is gone by then, so the orphan branch runs and cannot clear it either.
//
// So the bucket must be computed from the durable representation of the instant,
// not from the caller's. These pin that both timestamp inputs are normalized;
// GCProjectionUTCDate already collapses to a date and cannot drift.
func TestGCFailedItemExpiryBucketIsStableAcrossCassandraPrecision(t *testing.T) {
	const (
		orgID       = "3f2504e0-4f89-11d3-9a0c-0305e82c3301"
		itemType    = "block"
		itemID      = "0f1e2d3c4b5a"
		storageCls  = "hot"
		storageKey  = "blocks/3f2504e0/0f1e2d3c4b5a"
		nanosOffset = 456789 * time.Nanosecond
	)

	// A millisecond boundary plus a sub-millisecond remainder: what time.Now hands
	// FailItem, and what Cassandra will not keep.
	base := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC).Add(123 * time.Millisecond)
	subMilli := base.Add(nanosOffset)
	asStored := time.UnixMilli(subMilli.UnixMilli()).UTC()

	if subMilli.Equal(asStored) {
		t.Fatal("fixture is vacuous: the sub-millisecond value must differ from what Cassandra keeps")
	}

	t.Run("failed_at", func(t *testing.T) {
		identityAt := base
		before := GCFailedItemExpiryBucket(orgID, subMilli, itemType, itemID, storageCls, storageKey, identityAt)
		after := GCFailedItemExpiryBucket(orgID, asStored, itemType, itemID, storageCls, storageKey, identityAt)
		if before != after {
			t.Errorf("bucket for failed_at %s = %d but %d once Cassandra has truncated it to %s.\n"+
				"The INSERT would land in one partition and every later DELETE would name another, so the "+
				"expiry row survives its own deletion and the sweep rediscovers it forever.",
				subMilli.Format(time.RFC3339Nano), before, after, asStored.Format(time.RFC3339Nano))
		}
	})

	t.Run("identity_at", func(t *testing.T) {
		failedAt := base
		before := GCFailedItemExpiryBucket(orgID, failedAt, itemType, itemID, storageCls, storageKey, subMilli)
		after := GCFailedItemExpiryBucket(orgID, failedAt, itemType, itemID, storageCls, storageKey, asStored)
		if before != after {
			t.Errorf("bucket for identity_at %s = %d but %d once Cassandra has truncated it to %s",
				subMilli.Format(time.RFC3339Nano), before, after, asStored.Format(time.RFC3339Nano))
		}
	})

	// Normalizing must not collapse distinct lifecycles into one input: two
	// instants a millisecond apart are still two different rows, and the hash has
	// to keep seeing them as different strings.
	t.Run("distinct milliseconds stay distinct inputs", func(t *testing.T) {
		a := GCFailedItemExpiryBucket(orgID, base, itemType, itemID, storageCls, storageKey, base)
		b := GCFailedItemExpiryBucket(orgID, base.Add(time.Millisecond), itemType, itemID, storageCls, storageKey, base)
		// Buckets are a hash modulo 32, so a collision is legal; what would not be
		// legal is normalizing a millisecond away. Assert on the normalized value
		// the bucket is derived from rather than on the bucket itself.
		if cassandraTimestamp(base).Equal(cassandraTimestamp(base.Add(time.Millisecond))) {
			t.Fatal("normalization collapsed two instants a millisecond apart into one")
		}
		_, _ = a, b
	})

	// The timezone a caller happens to carry must not change the partition either:
	// the same instant expressed in another zone is the same row.
	t.Run("zone does not change the bucket", func(t *testing.T) {
		zone := time.FixedZone("UTC+5", 5*60*60)
		utc := GCFailedItemExpiryBucket(orgID, base, itemType, itemID, storageCls, storageKey, base)
		elsewhere := GCFailedItemExpiryBucket(orgID, base.In(zone), itemType, itemID, storageCls, storageKey, base.In(zone))
		if utc != elsewhere {
			t.Errorf("same instant in another zone produced bucket %d instead of %d", elsewhere, utc)
		}
	})
}
