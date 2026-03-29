package plans

import (
	"sort"
	"strings"

	"github.com/Sesame-Disk/sesamefs/internal/config"
)

// CapabilityResult holds the resolved capabilities for a user.
type CapabilityResult struct {
	// Capabilities maps each can_X flag to its resolved value (role AND profile).
	Capabilities map[string]bool
	// UpgradeFeatures lists flags blocked by the enforcement profile (not by role).
	// These drive upgrade CTAs in the frontend.
	UpgradeFeatures []string
	// Limits contains the numeric limits from the enforcement profile.
	Limits config.EnforcementLimits
}

// ResolveCapabilities intersects role permissions with the enforcement profile.
// Pure function — no DB dependency.
//
// For each flag:
//   - caps[flag] = RolePermissions[role][flag] AND profile.Features[flag]
//   - If role allows but profile doesn't → flag goes to UpgradeFeatures
//     (user would gain this by upgrading the plan, not by changing role)
func ResolveCapabilities(role string, profile config.EnforcementProfile) CapabilityResult {
	roleCaps := RolePermissions[role]
	if roleCaps == nil {
		roleCaps = map[string]bool{} // unknown role → no permissions
	}
	profileCaps := ProfileFeatureMap(profile.Features)

	caps := make(map[string]bool, len(AllFeatureFlags))
	var upgradeFeatures []string

	for _, flag := range AllFeatureFlags {
		roleAllows := roleCaps[flag]
		profileAllows := profileCaps[flag]
		caps[flag] = roleAllows && profileAllows

		if roleAllows && !profileAllows {
			// Strip the "can_" prefix: frontend contract uses "add_group", not "can_add_group"
			upgradeFeatures = append(upgradeFeatures, strings.TrimPrefix(flag, "can_"))
		}
	}

	sort.Strings(upgradeFeatures)

	return CapabilityResult{
		Capabilities:    caps,
		UpgradeFeatures: upgradeFeatures,
		Limits:          profile.Limits,
	}
}

// IsHardEnforcement returns true if the quota_policy indicates hard enforcement
// (free tier). Empty defaults to "hard" — safe default.
func IsHardEnforcement(quotaPolicy string) bool {
	return quotaPolicy == "" || quotaPolicy == "hard"
}

// ComputeCanUpgrade determines whether the upgrade CTA should be shown.
//
// Rules:
//   - Only owners can initiate upgrades
//   - Free owner: always show (upgrade to paid)
//   - Paid owner: show when storage or traffic >= 80% or over quota
//   - Non-owner: never show
func ComputeCanUpgrade(role, quotaPolicy string, storagePct, trafficPct float64, storageOver, trafficOver bool) bool {
	if role != "owner" {
		return false
	}
	if IsHardEnforcement(quotaPolicy) {
		return true // free owner always sees upgrade
	}
	// Paid owner: only when approaching or exceeding limits
	if storageOver || trafficOver {
		return true
	}
	if storagePct >= 80.0 || trafficPct >= 80.0 {
		return true
	}
	return false
}
