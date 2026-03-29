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

// CountActiveShareLinks counts active share links (link_type="share") for an org.
// Uses the share_links_by_org table (partition by org_id).
func CountActiveShareLinks(database *db.DB, orgID string) int {
	orgUUID, err := gocql.ParseUUID(orgID)
	if err != nil {
		return 0
	}
	iter := database.Session().Query(
		`SELECT link_type, active FROM share_links_by_org WHERE org_id = ?`, orgUUID,
	).Iter()

	var linkType string
	var active bool
	count := 0
	for iter.Scan(&linkType, &active) {
		if linkType == "share" && active {
			count++
		}
	}
	iter.Close()
	return count
}

// CountActiveUploadLinks counts active upload links (link_type="upload") for an org.
func CountActiveUploadLinks(database *db.DB, orgID string) int {
	orgUUID, err := gocql.ParseUUID(orgID)
	if err != nil {
		return 0
	}
	iter := database.Session().Query(
		`SELECT link_type, active FROM share_links_by_org WHERE org_id = ?`, orgUUID,
	).Iter()

	var linkType string
	var active bool
	count := 0
	for iter.Scan(&linkType, &active) {
		if linkType == "upload" && active {
			count++
		}
	}
	iter.Close()
	return count
}

// CountActiveLibraries counts non-deleted libraries for an org.
func CountActiveLibraries(database *db.DB, orgID string) int {
	iter := database.Session().Query(
		`SELECT deleted_at FROM libraries WHERE org_id = ?`, orgID,
	).Iter()

	var deletedAt time.Time
	count := 0
	for iter.Scan(&deletedAt) {
		if deletedAt.IsZero() {
			count++
		}
	}
	iter.Close()
	return count
}
