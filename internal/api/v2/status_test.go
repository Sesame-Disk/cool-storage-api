package v2

import (
	"testing"
	"time"
)

func TestIsUserUsable(t *testing.T) {
	tests := []struct {
		status string
		want   bool
	}{
		{"active", true},
		{"", true},       // legacy: before migration, status is empty
		{"deactivated", false},
		{"deleted", false},
		{"unknown", false},
	}

	for _, tt := range tests {
		t.Run("status_"+tt.status, func(t *testing.T) {
			if got := IsUserUsable(tt.status); got != tt.want {
				t.Errorf("IsUserUsable(%q) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestIsOrgUsable(t *testing.T) {
	tests := []struct {
		status string
		want   bool
	}{
		{"active", true},
		{"", true},
		{"deactivated", false},
		{"deleted", false},
		{"unknown", false},
	}

	for _, tt := range tests {
		t.Run("status_"+tt.status, func(t *testing.T) {
			if got := IsOrgUsable(tt.status); got != tt.want {
				t.Errorf("IsOrgUsable(%q) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestStatusConstants(t *testing.T) {
	if StatusActive != "active" {
		t.Errorf("StatusActive = %q, want %q", StatusActive, "active")
	}
	if StatusDeactivated != "deactivated" {
		t.Errorf("StatusDeactivated = %q, want %q", StatusDeactivated, "deactivated")
	}
	if StatusDeleted != "deleted" {
		t.Errorf("StatusDeleted = %q, want %q", StatusDeleted, "deleted")
	}
}

func TestMakeAdminUserResponse_StatusField(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name       string
		role       string
		status     string
		wantActive bool
		wantStaff  bool
	}{
		{
			name:       "active admin",
			role:       "admin",
			status:     StatusActive,
			wantActive: true,
			wantStaff:  true,
		},
		{
			name:       "deactivated admin preserves role",
			role:       "admin",
			status:     StatusDeactivated,
			wantActive: false,
			wantStaff:  true, // role is preserved, only status changes
		},
		{
			name:       "deleted user preserves role",
			role:       "user",
			status:     StatusDeleted,
			wantActive: false,
			wantStaff:  false,
		},
		{
			name:       "legacy empty status treated as active",
			role:       "user",
			status:     "",
			wantActive: true,
			wantStaff:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := makeAdminUserResponse("test@example.com", "Test", tt.role, tt.status, 0, 0, now)

			if resp.IsActive != tt.wantActive {
				t.Errorf("IsActive = %v, want %v (role=%q, status=%q)", resp.IsActive, tt.wantActive, tt.role, tt.status)
			}
			if resp.IsStaff != tt.wantStaff {
				t.Errorf("IsStaff = %v, want %v (role=%q, status=%q)", resp.IsStaff, tt.wantStaff, tt.role, tt.status)
			}
			// Role should be preserved, not overwritten by status
			if resp.Role != tt.role {
				t.Errorf("Role = %q, want %q", resp.Role, tt.role)
			}
		})
	}
}
