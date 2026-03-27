package v2

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/stretchr/testify/assert"
)

func TestApplyLegacyStaffToggle(t *testing.T) {
	tests := []struct {
		name     string
		role     string
		isStaff  bool
		expected string
	}{
		{"owner stays owner when set staff true", "owner", true, "owner"},
		{"owner stays owner when set staff false", "owner", false, "owner"},
		{"admin demotes to user when staff false", "admin", false, "user"},
		{"user promotes to admin when staff true", "user", true, "admin"},
		{"readonly promotes to admin when staff true", "readonly", true, "admin"},
		{"superadmin remains superadmin", "superadmin", true, "superadmin"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, applyLegacyStaffToggle(tt.role, tt.isStaff))
		})
	}
}

func TestBuildOwnershipTransferPlan(t *testing.T) {
	tests := []struct {
		name              string
		isSuperAdmin      bool
		callerUserID      string
		existingOwnerID   string
		newOwnerUserID    string
		callerRole        middleware.OrganizationRole
		newOwnerRole      middleware.OrganizationRole
		wantErr           string
		wantDemoteOwnerID string
		wantPromoteUserID string
		wantNoOp          bool
		wantBootstrap     bool
	}{
		{
			name:              "owner can transfer to admin",
			callerUserID:      "owner-1",
			existingOwnerID:   "owner-1",
			newOwnerUserID:    "admin-1",
			callerRole:        middleware.RoleOwner,
			newOwnerRole:      middleware.RoleAdmin,
			wantDemoteOwnerID: "owner-1",
			wantPromoteUserID: "admin-1",
		},
		{
			name:              "superadmin can bootstrap owner",
			isSuperAdmin:      true,
			callerUserID:      "super-1",
			newOwnerUserID:    "admin-1",
			callerRole:        middleware.RoleSuperAdmin,
			newOwnerRole:      middleware.RoleAdmin,
			wantPromoteUserID: "admin-1",
			wantBootstrap:     true,
		},
		{
			name:            "owner cannot transfer to self",
			callerUserID:    "owner-1",
			existingOwnerID: "owner-1",
			newOwnerUserID:  "owner-1",
			callerRole:      middleware.RoleOwner,
			newOwnerRole:    middleware.RoleOwner,
			wantErr:         "cannot transfer ownership to yourself",
		},
		{
			name:            "new owner must be admin or above",
			callerUserID:    "owner-1",
			existingOwnerID: "owner-1",
			newOwnerUserID:  "user-1",
			callerRole:      middleware.RoleOwner,
			newOwnerRole:    middleware.RoleUser,
			wantErr:         "new owner must be an admin",
		},
		{
			name:              "same owner is noop",
			isSuperAdmin:      true,
			callerUserID:      "super-1",
			existingOwnerID:   "owner-1",
			newOwnerUserID:    "owner-1",
			callerRole:        middleware.RoleSuperAdmin,
			newOwnerRole:      middleware.RoleOwner,
			wantNoOp:          true,
			wantPromoteUserID: "owner-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := buildOwnershipTransferPlan(tt.isSuperAdmin, tt.callerUserID, tt.existingOwnerID, tt.newOwnerUserID, tt.callerRole, tt.newOwnerRole)
			if tt.wantErr != "" {
				assert.EqualError(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.wantDemoteOwnerID, plan.DemoteOwnerID)
			assert.Equal(t, tt.wantPromoteUserID, plan.PromoteUserID)
			assert.Equal(t, tt.wantNoOp, plan.NoOp)
			assert.Equal(t, tt.wantBootstrap, plan.BootstrapOwner)
		})
	}
}

func TestOrgUserRow_JSONFormatWithOrgQuotaFields(t *testing.T) {
	createdAt := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	lastLogin := time.Date(2025, 6, 20, 8, 30, 0, 0, time.UTC)

	row := buildOrgUserRowWithTraffic(
		"user@example.com",
		"Quota User",
		"admin",
		"active",
		"org-123",
		1099511627776,
		524288,
		2097152,
		4194304,
		createdAt,
		lastLogin,
	)
	row.OrgStorageQuota = 2199023255552
	row.OrgTrafficQuota = 10737418240
	row.OrgTrafficUploadQuota = 5368709120
	row.OrgTrafficDownloadQuota = 8589934592

	data, err := json.Marshal(row)
	assert.NoError(t, err)

	var parsed map[string]interface{}
	err = json.Unmarshal(data, &parsed)
	assert.NoError(t, err)

	assert.Equal(t, "user@example.com", parsed["id"])
	assert.Equal(t, "user@example.com", parsed["email"])
	assert.Equal(t, "Quota User", parsed["name"])
	assert.Equal(t, true, parsed["is_active"])
	assert.Equal(t, true, parsed["is_org_staff"])
	assert.Equal(t, float64(1099511627776), parsed["quota_total"])
	assert.Equal(t, float64(524288), parsed["quota_usage"])
	assert.Equal(t, float64(2097152), parsed["traffic_upload_quota"])
	assert.Equal(t, float64(4194304), parsed["traffic_download_quota"])
	assert.Equal(t, float64(2199023255552), parsed["org_storage_quota"])
	assert.Equal(t, float64(10737418240), parsed["org_traffic_quota"])
	assert.Equal(t, float64(5368709120), parsed["org_traffic_upload_quota"])
	assert.Equal(t, float64(8589934592), parsed["org_traffic_download_quota"])
	assert.Equal(t, createdAt.Format(time.RFC3339), parsed["ctime"])
	assert.Equal(t, lastLogin.Format(time.RFC3339), parsed["last_login"])

	expectedKeys := []string{
		"id", "email", "name", "owner_contact_email", "status", "is_active", "is_org_staff",
		"role", "quota_total", "quota_usage", "traffic_upload_quota", "traffic_download_quota",
		"ctime", "last_login", "org_id", "avatar_url",
		"org_storage_quota", "org_traffic_quota", "org_traffic_upload_quota", "org_traffic_download_quota",
	}
	for _, key := range expectedKeys {
		_, exists := parsed[key]
		assert.True(t, exists, "expected key %q in JSON output", key)
	}
	assert.Equal(t, len(expectedKeys), len(parsed), "unexpected extra keys in JSON output")
}

func TestOrgUserRow_JSONFormatOmitsOrgQuotaFieldsWhenZero(t *testing.T) {
	createdAt := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	row := buildOrgUserRowWithTraffic(
		"user@example.com",
		"Quota User",
		"user",
		"active",
		"org-123",
		0,
		0,
		0,
		0,
		createdAt,
		time.Time{},
	)

	data, err := json.Marshal(row)
	assert.NoError(t, err)

	var parsed map[string]interface{}
	err = json.Unmarshal(data, &parsed)
	assert.NoError(t, err)

	_, hasStorage := parsed["org_storage_quota"]
	_, hasCombined := parsed["org_traffic_quota"]
	_, hasUpload := parsed["org_traffic_upload_quota"]
	_, hasDownload := parsed["org_traffic_download_quota"]
	assert.False(t, hasStorage)
	assert.False(t, hasCombined)
	assert.False(t, hasUpload)
	assert.False(t, hasDownload)
}
