package db

import (
	"fmt"
	"hash/fnv"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

const GCDiscoveryBucketCount = 32

const (
	GCLibraryPolicyVersionTTL = "version_ttl"
	GCLibraryPolicyAutoDelete = "auto_delete"
)

func GCDiscoveryBucket(parts ...string) int {
	hasher := fnv.New32a()
	for _, part := range parts {
		_, _ = hasher.Write([]byte(part))
		_, _ = hasher.Write([]byte{0})
	}
	return int(hasher.Sum32() % GCDiscoveryBucketCount)
}

func GCProjectionUTCDate(ts time.Time) time.Time {
	utc := ts.UTC()
	return time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
}

func GCProjectionDateString(ts time.Time) string {
	return GCProjectionUTCDate(ts).Format("2006-01-02")
}

func ParseGCProjectionDate(value string) (time.Time, error) {
	day, err := time.ParseInLocation("2006-01-02", value, time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse GC projection date %q: %w", value, err)
	}
	return day, nil
}

func AddUpsertDeletedUserDiscoveryQuery(batch *gocql.Batch, orgID, userID string, deletedAt time.Time) {
	batch.Query(`
		INSERT INTO gc_deleted_users_by_deleted_day (deleted_day, bucket, deleted_at, org_id, user_id)
		VALUES (?, ?, ?, ?, ?)
	`, GCProjectionUTCDate(deletedAt), GCDiscoveryBucket(orgID, userID), deletedAt.UTC(), orgID, userID)
}

func AddDeleteDeletedUserDiscoveryQuery(batch *gocql.Batch, orgID, userID string, deletedAt time.Time) {
	batch.Query(`
		DELETE FROM gc_deleted_users_by_deleted_day
		WHERE deleted_day = ? AND bucket = ? AND deleted_at = ? AND org_id = ? AND user_id = ?
	`, GCProjectionUTCDate(deletedAt), GCDiscoveryBucket(orgID, userID), deletedAt.UTC(), orgID, userID)
}

func AddUpsertShareLinkExpiryQuery(batch *gocql.Batch, token, orgID, libraryID, createdBy, linkType string, createdAt, expiresAt time.Time) {
	if expiresAt.IsZero() {
		return
	}
	batch.Query(`
		INSERT INTO gc_share_links_by_expiry (
			expiry_day, bucket, expires_at, link_token, org_id, library_id, created_by, created_at, link_type
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, GCProjectionUTCDate(expiresAt), GCDiscoveryBucket(token), expiresAt.UTC(), token, orgID, libraryID, createdBy, createdAt.UTC(), linkType)
}

func AddDeleteShareLinkExpiryQuery(batch *gocql.Batch, token string, expiresAt time.Time) {
	if expiresAt.IsZero() {
		return
	}
	batch.Query(`
		DELETE FROM gc_share_links_by_expiry
		WHERE expiry_day = ? AND bucket = ? AND expires_at = ? AND link_token = ?
	`, GCProjectionUTCDate(expiresAt), GCDiscoveryBucket(token), expiresAt.UTC(), token)
}

func AddUpsertShareExpiryQuery(batch *gocql.Batch, orgID, libraryID, shareID, sharedTo, sharedToType, sharedBy string, createdAt, expiresAt time.Time) {
	if expiresAt.IsZero() {
		return
	}
	batch.Query(`
		INSERT INTO gc_shares_by_expiry (
			expiry_day, bucket, expires_at, org_id, library_id, share_id, shared_to, shared_to_type, shared_by, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, GCProjectionUTCDate(expiresAt), GCDiscoveryBucket(shareID), expiresAt.UTC(), orgID, libraryID, shareID, sharedTo, sharedToType, sharedBy, createdAt.UTC())
}

func AddDeleteShareExpiryQuery(batch *gocql.Batch, shareID string, expiresAt time.Time, orgID, libraryID string) {
	if expiresAt.IsZero() {
		return
	}
	batch.Query(`
		DELETE FROM gc_shares_by_expiry
		WHERE expiry_day = ? AND bucket = ? AND expires_at = ? AND org_id = ? AND library_id = ? AND share_id = ?
	`, GCProjectionUTCDate(expiresAt), GCDiscoveryBucket(shareID), expiresAt.UTC(), orgID, libraryID, shareID)
}

func AddUpsertLibraryPolicyQuery(batch *gocql.Batch, policyType, orgID, libraryID string, days int, cachedHeadCommitID string, policyUpdatedAt time.Time) {
	if days <= 0 {
		return
	}
	batch.Query(`
		INSERT INTO gc_libraries_by_policy (
			policy_type, bucket, org_id, library_id, days, cached_head_commit_id, policy_updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`, policyType, GCDiscoveryBucket(libraryID), orgID, libraryID, days, cachedHeadCommitID, policyUpdatedAt.UTC())
}

func AddDeleteLibraryPolicyQuery(batch *gocql.Batch, policyType, orgID, libraryID string) {
	batch.Query(`
		DELETE FROM gc_libraries_by_policy
		WHERE policy_type = ? AND bucket = ? AND org_id = ? AND library_id = ?
	`, policyType, GCDiscoveryBucket(libraryID), orgID, libraryID)
}

func AddUpsertProvisionalBlockRefExpiryDiscoveryQuery(batch *gocql.Batch, orgID, blockID, referrer, storageClass string, expiresAt time.Time) {
	batch.Query(`
		INSERT INTO gc_provisional_block_refs_by_day (expiry_day, bucket, expires_at, org_id, block_id, referrer, storage_class)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, GCProjectionUTCDate(expiresAt), GCDiscoveryBucket(orgID, blockID, referrer), expiresAt.UTC(), orgID, blockID, referrer, storageClass)
}

func AddDeleteProvisionalBlockRefExpiryDiscoveryQuery(batch *gocql.Batch, orgID, blockID, referrer string, expiresAt time.Time) {
	batch.Query(`
		DELETE FROM gc_provisional_block_refs_by_day
		WHERE expiry_day = ? AND bucket = ? AND expires_at = ? AND org_id = ? AND block_id = ? AND referrer = ?
	`, GCProjectionUTCDate(expiresAt), GCDiscoveryBucket(orgID, blockID, referrer), expiresAt.UTC(), orgID, blockID, referrer)
}

// The gc_s3_orphans_by_day batch helpers that used to sit here were removed by
// R22a. They had no production caller and wrote a partial payload (storage_class
// only, no recovery_phase) with no canonical-row counterpart in the same batch —
// the same "second creator waiting to be wired up" shape R21 removed from the
// canonical table. Since R22a, recovery fails closed and retains the day cursor
// if such a discovery row is encountered, so wiring such a helper up could hold
// the cursor while the row remains in the overlap or leave stale state behind
// the cursor until the 90-day TTL. gc_s3_orphans_by_day is now written only by
// the canonical orphan store (upsertS3OrphanProjection / DeleteS3Orphan);
// TestR22aDiscoveryWriterSurface fails if a second writer or helper caller
// reappears.

// cassandraTimestamp collapses an instant to the precision a Cassandra TIMESTAMP
// actually stores: milliseconds since the epoch, in UTC.
//
// IT EXISTS FOR HASHING, AND ONLY FOR HASHING. A value that reaches Cassandra as a
// query PARAMETER needs no help: gocql marshals a time.Time as
// `Unix()*1e3 + Nanosecond()/1e6`, so the driver normalizes it on the way out and a
// caller holding nanoseconds still addresses the row stored under the truncated
// value. Do not add normalization to WHERE clauses on the strength of this helper —
// TestCassandraTimestampBindingNormalizesSubMillisecondParameters pins that they do
// not need it, against the real engine.
//
// What does need help is a value the code turns into a KEY ITSELF, in Go, which never
// meets the driver at all. That is the bucket below.
func cassandraTimestamp(ts time.Time) time.Time {
	return time.UnixMilli(ts.UnixMilli()).UTC()
}

// GCFailedItemExpiryBucket is the ONE definition of the expiry projection's
// bucket. Both halves of the key — the columns and this hash — must be derived
// identically by every writer, so the formula is named and exported rather than
// re-spelled at each call site.
//
// IT HASHES THE DURABLE FORM OF EACH TIMESTAMP, NOT THE CALLER'S. This is the only
// GC discovery bucket whose input includes timestamps; every other one hashes ids
// and tokens, which survive a round-trip unchanged. Here the write side hashes a
// value that has never been to Cassandra — FailItem takes the worker's clock, which
// is time.Now and carries nanoseconds — while every delete hashes the same instant
// after it has come back as a TIMESTAMP, holding milliseconds. Hashing the raw
// values made those two disagree, so the DELETE named a different partition than
// the INSERT: Cassandra reported success, the row stayed where it was, and the
// sweep that walks every bucket rediscovered it forever. Its canonical DLQ row was
// gone by then, so the orphan branch ran and recomputed the same unreachable
// bucket. Normalizing first makes the bucket a property of the instant rather than
// of who is holding it.
func GCFailedItemExpiryBucket(orgID string, failedAt time.Time, itemType, itemID, candidateStorageClass, candidateStorageKey string, identityAt time.Time) int {
	return GCDiscoveryBucket(
		orgID, itemType, itemID,
		cassandraTimestamp(failedAt).Format(time.RFC3339Nano),
		candidateStorageClass, candidateStorageKey,
		cassandraTimestamp(identityAt).Format(time.RFC3339Nano),
	)
}

func AddUpsertFailedItemExpiryQuery(batch *gocql.Batch, orgID string, failedAt time.Time, itemType, itemID string, expiresAt time.Time, candidateStorageClass, candidateStorageKey string, identityAt time.Time) {
	batch.Query(`
		INSERT INTO gc_failed_items_by_expiry (
			expiry_day, bucket, expires_at, org_id, failed_at, item_type, item_id,
			candidate_storage_class, candidate_storage_key, identity_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, GCProjectionUTCDate(expiresAt), GCFailedItemExpiryBucket(orgID, failedAt, itemType, itemID, candidateStorageClass, candidateStorageKey, identityAt), expiresAt.UTC(), orgID, failedAt.UTC(), itemType, itemID, candidateStorageClass, candidateStorageKey, identityAt.UTC())
}

func AddDeleteFailedItemExpiryQuery(batch *gocql.Batch, orgID string, failedAt time.Time, itemType, itemID string, expiresAt time.Time, candidateStorageClass, candidateStorageKey string, identityAt time.Time) {
	if expiresAt.IsZero() {
		return
	}
	batch.Query(`
		DELETE FROM gc_failed_items_by_expiry
		WHERE expiry_day = ? AND bucket = ? AND expires_at = ? AND org_id = ? AND failed_at = ? AND item_type = ? AND item_id = ? AND candidate_storage_class = ? AND candidate_storage_key = ? AND identity_at = ?
	`, GCProjectionUTCDate(expiresAt), GCFailedItemExpiryBucket(orgID, failedAt, itemType, itemID, candidateStorageClass, candidateStorageKey, identityAt), expiresAt.UTC(), orgID, failedAt.UTC(), itemType, itemID, candidateStorageClass, candidateStorageKey, identityAt.UTC())
}
