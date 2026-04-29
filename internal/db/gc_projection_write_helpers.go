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
