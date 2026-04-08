package plans

import (
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/config"
)

func TestResolveCapabilities_UserHard(t *testing.T) {
	profile := config.DefaultHardProfile()
	result := ResolveCapabilities("user", profile)

	// User + hard: can_add_group should be false (profile blocks it)
	if result.Capabilities["can_add_group"] {
		t.Error("user+hard: can_add_group should be false")
	}
	// can_add_repo should be true (both role and profile allow it)
	if !result.Capabilities["can_add_repo"] {
		t.Error("user+hard: can_add_repo should be true")
	}

	// upgrade_features should use short names (without "can_" prefix) — frontend contract
	found := map[string]bool{}
	for _, f := range result.UpgradeFeatures {
		found[f] = true
	}
	expectedUpgrade := []string{
		"add_group", "publish_repo",
		"send_share_link_mail", "use_global_address_book",
	}
	for _, f := range expectedUpgrade {
		if !found[f] {
			t.Errorf("user+hard: %q should be in upgrade_features", f)
		}
	}
	// full can_X names should NOT appear in upgrade_features
	if found["can_add_group"] {
		t.Error("user+hard: upgrade_features must use short name 'add_group', not 'can_add_group'")
	}
	// Flags allowed by both should NOT be in upgrade_features
	if found["add_repo"] {
		t.Error("user+hard: add_repo should NOT be in upgrade_features")
	}
}

func TestResolveCapabilities_UserSoft(t *testing.T) {
	profile := config.DefaultSoftProfile()
	result := ResolveCapabilities("user", profile)

	// All flags should be true
	for _, flag := range AllFeatureFlags {
		if !result.Capabilities[flag] {
			t.Errorf("user+soft: %q should be true", flag)
		}
	}
	// No upgrade features
	if len(result.UpgradeFeatures) != 0 {
		t.Errorf("user+soft: upgrade_features should be empty, got %v", result.UpgradeFeatures)
	}
}

func TestResolveCapabilities_ReadonlyHard(t *testing.T) {
	profile := config.DefaultHardProfile()
	result := ResolveCapabilities("readonly", profile)

	// Readonly: 9 content flags false (blocked by role, not plan)
	if result.Capabilities["can_add_repo"] {
		t.Error("readonly+hard: can_add_repo should be false")
	}
	if result.Capabilities["can_add_group"] {
		t.Error("readonly+hard: can_add_group should be false")
	}
	// 4 connectivity flags: role allows, profile allows → true
	if !result.Capabilities["can_connect_with_desktop_clients"] {
		t.Error("readonly+hard: can_connect_with_desktop_clients should be true")
	}

	// upgrade_features should NOT contain role-blocked flags
	// (those flags are blocked by role, upgrading plan won't help)
	// Also: names must be short (no "can_" prefix)
	for _, f := range result.UpgradeFeatures {
		if f == "add_repo" || f == "share_repo" {
			t.Errorf("readonly+hard: %q should NOT be in upgrade_features (role-blocked)", f)
		}
		if len(f) > 4 && f[:4] == "can_" {
			t.Errorf("readonly+hard: upgrade_features entry %q has unexpected 'can_' prefix", f)
		}
	}
}

func TestResolveCapabilities_Guest(t *testing.T) {
	profile := config.DefaultSoftProfile()
	result := ResolveCapabilities("guest", profile)

	// Guest: all flags false
	for _, flag := range AllFeatureFlags {
		if result.Capabilities[flag] {
			t.Errorf("guest+soft: %q should be false", flag)
		}
	}
	// No upgrade features (role blocks everything, not plan)
	if len(result.UpgradeFeatures) != 0 {
		t.Errorf("guest+soft: upgrade_features should be empty, got %v", result.UpgradeFeatures)
	}
}

// TestUpgradeFeaturesNaming verifies the frontend contract: upgrade_features must
// use short names without the "can_" prefix (e.g. "add_group", not "can_add_group").
func TestUpgradeFeaturesNaming(t *testing.T) {
	profile := config.DefaultHardProfile()
	result := ResolveCapabilities("user", profile)

	for _, f := range result.UpgradeFeatures {
		if len(f) >= 4 && f[:4] == "can_" {
			t.Errorf("upgrade_features entry %q has 'can_' prefix; frontend contract expects short names like 'add_group'", f)
		}
	}

	// Spot-check expected short names
	found := map[string]bool{}
	for _, f := range result.UpgradeFeatures {
		found[f] = true
	}
	if !found["add_group"] {
		t.Error("expected 'add_group' in upgrade_features, got full flag name or missing")
	}
}

func TestResolveCapabilities_UnknownRole(t *testing.T) {
	profile := config.DefaultSoftProfile()
	result := ResolveCapabilities("bogus", profile)

	for _, flag := range AllFeatureFlags {
		if result.Capabilities[flag] {
			t.Errorf("bogus+soft: %q should be false", flag)
		}
	}
}

func TestResolveCapabilities_Limits(t *testing.T) {
	profile := config.DefaultHardProfile()
	result := ResolveCapabilities("user", profile)

	if result.Limits.MaxLibraries != 3 {
		t.Errorf("hard limits: MaxLibraries = %d, want 3", result.Limits.MaxLibraries)
	}
	if result.Limits.MaxShareLinks != 3 {
		t.Errorf("hard limits: MaxShareLinks = %d, want 3", result.Limits.MaxShareLinks)
	}
}

func TestResolveCapabilities_UpgradeFeaturesSorted(t *testing.T) {
	profile := config.DefaultHardProfile()
	result := ResolveCapabilities("user", profile)

	for i := 1; i < len(result.UpgradeFeatures); i++ {
		if result.UpgradeFeatures[i] < result.UpgradeFeatures[i-1] {
			t.Errorf("upgrade_features not sorted: %q before %q",
				result.UpgradeFeatures[i-1], result.UpgradeFeatures[i])
		}
	}
}

// --- IsHardEnforcement tests ---

func TestIsHardEnforcement(t *testing.T) {
	tests := []struct {
		policy   string
		expected bool
	}{
		{"", true},
		{"hard", true},
		{"soft", false},
		{"HARD", false},
		{"unknown", false},
	}
	for _, tt := range tests {
		if got := IsHardEnforcement(tt.policy); got != tt.expected {
			t.Errorf("IsHardEnforcement(%q) = %v, want %v", tt.policy, got, tt.expected)
		}
	}
}

// --- ComputeCanUpgrade tests ---

func TestComputeCanUpgrade_FreeOwner(t *testing.T) {
	if !ComputeCanUpgrade("owner", "hard", 0, 0, false, false) {
		t.Error("free owner should always get can_upgrade=true")
	}
}

func TestComputeCanUpgrade_FreeOwnerEmptyPolicy(t *testing.T) {
	if !ComputeCanUpgrade("owner", "", 10, 10, false, false) {
		t.Error("owner with empty policy should get can_upgrade=true")
	}
}

func TestComputeCanUpgrade_PaidOwnerUnderLimits(t *testing.T) {
	if ComputeCanUpgrade("owner", "soft", 50, 30, false, false) {
		t.Error("paid owner under limits should get can_upgrade=false")
	}
}

func TestComputeCanUpgrade_PaidOwnerAt80Pct(t *testing.T) {
	if !ComputeCanUpgrade("owner", "soft", 80, 30, false, false) {
		t.Error("paid owner at 80% storage should get can_upgrade=true")
	}
	if !ComputeCanUpgrade("owner", "soft", 30, 85, false, false) {
		t.Error("paid owner at 85% traffic should get can_upgrade=true")
	}
}

func TestComputeCanUpgrade_PaidOwnerOverQuota(t *testing.T) {
	if !ComputeCanUpgrade("owner", "soft", 100, 50, true, false) {
		t.Error("paid owner over storage quota should get can_upgrade=true")
	}
	if !ComputeCanUpgrade("owner", "soft", 50, 100, false, true) {
		t.Error("paid owner over traffic quota should get can_upgrade=true")
	}
}

func TestComputeCanUpgrade_NonOwner(t *testing.T) {
	for _, role := range []string{"admin", "user", "readonly", "guest", "superadmin"} {
		if ComputeCanUpgrade(role, "hard", 0, 0, false, false) {
			t.Errorf("non-owner role %q should get can_upgrade=false", role)
		}
	}
}
