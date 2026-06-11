package v2

import (
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

// OrgEnforcementCtx holds the resolved enforcement context for an org.
type OrgEnforcementCtx struct {
	QuotaPolicy string
	Profile     config.EnforcementProfile
}

// GetOrgEnforcement fetches the org's quota_policy and resolves the enforcement profile.
func GetOrgEnforcement(database *db.DB, orgID string, cfg *config.Config) OrgEnforcementCtx {
	var quotaPolicy string
	orgUUID, err := gocql.ParseUUID(orgID)
	if err == nil {
		_ = database.Session().Query(
			`SELECT quota_policy FROM organizations WHERE org_id = ?`, orgUUID,
		).Scan(&quotaPolicy)
	}
	return OrgEnforcementCtx{
		QuotaPolicy: quotaPolicy,
		Profile:     cfg.GetEnforcementProfile(quotaPolicy),
	}
}

// CountShareLinks counts all share links (link_type="share") for an org for enforcement.
func CountShareLinks(database *db.DB, orgID string) int {
	if _, err := gocql.ParseUUID(orgID); err != nil {
		return 0
	}
	count, err := db.CountAdminOrgLinks(database.Session(), orgID, "share")
	if err != nil {
		return 0
	}
	return count
}

// CountUploadLinks counts all upload links (link_type="upload") for an org for enforcement.
func CountUploadLinks(database *db.DB, orgID string) int {
	if _, err := gocql.ParseUUID(orgID); err != nil {
		return 0
	}
	count, err := db.CountAdminOrgLinks(database.Session(), orgID, "upload")
	if err != nil {
		return 0
	}
	return count
}

// CountActiveLibraries counts non-deleted libraries for an org from the
// libraries_by_org_updated projection (single org partition, kept in sync with
// the canonical libraries table on every mutation) instead of scanning the
// tombstone-heavy canonical org partition. Callers must treat read errors as
// enforcement failures rather than silently allowing writes.
func CountActiveLibraries(database *db.DB, orgID string) (int, error) {
	iter := database.Session().Query(
		`SELECT deleted_at FROM libraries_by_org_updated WHERE org_id = ?`, orgID,
	).Iter()

	var deletedAt time.Time
	count := 0
	for iter.Scan(&deletedAt) {
		if deletedAt.IsZero() {
			count++
		}
		deletedAt = time.Time{}
	}
	if err := iter.Close(); err != nil {
		return 0, err
	}
	return count, nil
}
