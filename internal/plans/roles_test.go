package plans

import (
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/config"
)

func TestAllFeatureFlagsCount(t *testing.T) {
	if len(AllFeatureFlags) != 12 {
		t.Errorf("AllFeatureFlags has %d entries, want 12", len(AllFeatureFlags))
	}
}

func TestRolePermissions_AllRolesPresent(t *testing.T) {
	expected := []string{"superadmin", "owner", "admin", "user", "readonly", "guest"}
	for _, role := range expected {
		if _, ok := RolePermissions[role]; !ok {
			t.Errorf("RolePermissions missing role %q", role)
		}
	}
}

func TestRolePermissions_FullAccessRoles(t *testing.T) {
	for _, role := range []string{"superadmin", "owner", "admin", "user"} {
		caps := RolePermissions[role]
		for _, flag := range AllFeatureFlags {
			if !caps[flag] {
				t.Errorf("RolePermissions[%q][%q] = false, want true", role, flag)
			}
		}
	}
}

func TestRolePermissions_Readonly(t *testing.T) {
	caps := RolePermissions["readonly"]
	connectivityFlags := map[string]bool{
		"can_connect_with_desktop_clients":  true,
		"can_connect_with_android_clients":  true,
		"can_connect_with_ios_clients":      true,
		"can_export_files_via_mobile_client": true,
	}
	for _, flag := range AllFeatureFlags {
		expected := connectivityFlags[flag]
		if caps[flag] != expected {
			t.Errorf("RolePermissions[readonly][%q] = %v, want %v", flag, caps[flag], expected)
		}
	}
}

func TestRolePermissions_Guest(t *testing.T) {
	caps := RolePermissions["guest"]
	for _, flag := range AllFeatureFlags {
		if caps[flag] {
			t.Errorf("RolePermissions[guest][%q] = true, want false", flag)
		}
	}
}

func TestProfileFeatureMap_HardProfile(t *testing.T) {
	p := config.DefaultHardProfile()
	m := ProfileFeatureMap(p.Features)

	if m["can_add_group"] {
		t.Error("hard profile: can_add_group should be false")
	}
	if !m["can_add_repo"] {
		t.Error("hard profile: can_add_repo should be true")
	}
	if !m["can_connect_with_desktop_clients"] {
		t.Error("hard profile: can_connect_with_desktop_clients should be true")
	}
}

func TestProfileFeatureMap_SoftProfile(t *testing.T) {
	p := config.DefaultSoftProfile()
	m := ProfileFeatureMap(p.Features)

	for _, flag := range AllFeatureFlags {
		if !m[flag] {
			t.Errorf("soft profile: %q should be true", flag)
		}
	}
}

func TestProfileFeatureMap_KeyCount(t *testing.T) {
	p := config.DefaultSoftProfile()
	m := ProfileFeatureMap(p.Features)
	if len(m) != 12 {
		t.Errorf("ProfileFeatureMap returned %d keys, want 12", len(m))
	}
}
