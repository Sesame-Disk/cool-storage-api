package v2

import (
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/config"
)

func testStoragePolicyConfig() *config.Config {
	return &config.Config{
		Storage: config.StorageConfig{
			DefaultClass: "hot-s3-usa",
			Classes: map[string]config.StorageClassConfig{
				"hot-s3-usa": {Tier: "hot", Bucket: "usa"},
				"hot-s3-eu":  {Tier: "hot", Bucket: "eu"},
				"cold-s3-eu": {Tier: "cold", Bucket: "archive"},
			},
			EndpointRegions: map[string]string{
				"eu.example.com": "eu",
			},
			RegionClasses: map[string]config.RegionClassConfig{
				"usa": {Hot: "hot-s3-usa"},
				"eu":  {Hot: "hot-s3-eu", Cold: "cold-s3-eu"},
			},
		},
	}
}

func TestNormalizeOrgStoragePolicy(t *testing.T) {
	t.Run("defaults to flexible", func(t *testing.T) {
		policy, err := normalizeOrgStoragePolicy(nil)
		if err != nil {
			t.Fatalf("normalizeOrgStoragePolicy returned error: %v", err)
		}
		if policy.DataResidency != orgDataResidencyFlexible {
			t.Fatalf("DataResidency = %q, want %q", policy.DataResidency, orgDataResidencyFlexible)
		}
		if policy.DefaultRegion != "" {
			t.Fatalf("DefaultRegion = %q, want empty", policy.DefaultRegion)
		}
	})

	t.Run("rejects invalid residency", func(t *testing.T) {
		_, err := normalizeOrgStoragePolicy(map[string]string{"data_residency": "locked"})
		if err == nil {
			t.Fatal("expected error for invalid data_residency")
		}
	})
}

func TestIsKnownStorageClassRequiresCanonicalName(t *testing.T) {
	cfg := testStoragePolicyConfig()
	cfg.Storage.Classes["empty"] = config.StorageClassConfig{Tier: "hot"}

	if !isKnownStorageClass(cfg, "hot-s3-usa") {
		t.Fatal("canonical storage class should be known")
	}
	if isKnownStorageClass(cfg, "empty") {
		t.Fatal("storage class without a registrable bucket should not be known")
	}
	for _, name := range []string{" hot-s3-usa ", "hot_s3_usa", "Hot-s3-usa", "   "} {
		if isKnownStorageClass(cfg, name) {
			t.Fatalf("storage class %q should not be accepted as canonical", name)
		}
	}
}

func TestResolveCreateStorageClass(t *testing.T) {
	cfg := testStoragePolicyConfig()

	t.Run("flexible prefers endpoint region over org default", func(t *testing.T) {
		resolved, err := resolveCreateStorageClass(cfg, orgStoragePolicy{
			DataResidency: orgDataResidencyFlexible,
			DefaultRegion: "usa",
		}, "eu.example.com", "")
		if err != nil {
			t.Fatalf("resolveCreateStorageClass returned error: %v", err)
		}
		if resolved != "hot-s3-eu" {
			t.Fatalf("resolved = %q, want %q", resolved, "hot-s3-eu")
		}
	})

	t.Run("flexible falls back to server region for shared hostnames", func(t *testing.T) {
		cfg.Server.Region = "eu"
		cfg.Storage.EndpointRegions = map[string]string{}
		resolved, err := resolveCreateStorageClass(cfg, orgStoragePolicy{
			DataResidency: orgDataResidencyFlexible,
		}, "files.example.com", "")
		if err != nil {
			t.Fatalf("resolveCreateStorageClass returned error: %v", err)
		}
		if resolved != "hot-s3-eu" {
			t.Fatalf("resolved = %q, want %q", resolved, "hot-s3-eu")
		}
	})

	t.Run("flexible honors wildcard endpoint routing before server region fallback", func(t *testing.T) {
		cfg.Server.Region = "eu"
		cfg.Storage.EndpointRegions = map[string]string{
			"*.example.com": "usa",
			"*":             "eu",
		}
		resolved, err := resolveCreateStorageClass(cfg, orgStoragePolicy{
			DataResidency: orgDataResidencyFlexible,
		}, "files.example.com", "")
		if err != nil {
			t.Fatalf("resolveCreateStorageClass returned error: %v", err)
		}
		if resolved != "hot-s3-usa" {
			t.Fatalf("resolved = %q, want %q", resolved, "hot-s3-usa")
		}
	})

	t.Run("strict pins to org default region", func(t *testing.T) {
		resolved, err := resolveCreateStorageClass(cfg, orgStoragePolicy{
			DataResidency: orgDataResidencyStrict,
			DefaultRegion: "usa",
		}, "eu.example.com", "")
		if err != nil {
			t.Fatalf("resolveCreateStorageClass returned error: %v", err)
		}
		if resolved != "hot-s3-usa" {
			t.Fatalf("resolved = %q, want %q", resolved, "hot-s3-usa")
		}
	})

	t.Run("strict rejects cross-region explicit class", func(t *testing.T) {
		_, err := resolveCreateStorageClass(cfg, orgStoragePolicy{
			DataResidency: orgDataResidencyStrict,
			DefaultRegion: "usa",
		}, "eu.example.com", "hot-s3-eu")
		if err == nil {
			t.Fatal("expected strict policy rejection")
		}
	})

	t.Run("rejects cold storage class at create time", func(t *testing.T) {
		_, err := resolveCreateStorageClass(cfg, defaultOrgStoragePolicy(), "eu.example.com", "cold-s3-eu")
		if err == nil {
			t.Fatal("expected cold tier rejection")
		}
	})
}

func TestValidateMutableStorageClassStrictPolicy(t *testing.T) {
	cfg := testStoragePolicyConfig()
	policy := orgStoragePolicy{DataResidency: orgDataResidencyStrict, DefaultRegion: "eu"}

	if err := validateMutableStorageClass(cfg, policy, "hot-s3-eu"); err != nil {
		t.Fatalf("same-region hot class rejected: %v", err)
	}
	for _, class := range []string{"cold-s3-eu", "hot-s3-usa"} {
		if err := validateMutableStorageClass(cfg, policy, class); err == nil {
			t.Fatalf("strict policy accepted %q", class)
		}
	}
}

// Create is the other door onto the same field ChangeStorageClass guards, and both
// must answer the same way about the same raw value. Normalizing the request here
// would persist an identity the caller never named while the change endpoint
// refuses it -- the asymmetry, not the padding, is the defect.
func TestResolveCreateStorageClassRejectsNonCanonicalRequests(t *testing.T) {
	cfg := testStoragePolicyConfig()

	for _, requested := range []string{" hot-s3-eu", "hot-s3-eu ", " hot-s3-eu ", "Hot-S3-EU", "hot_s3_eu"} {
		t.Run("flexible/"+requested, func(t *testing.T) {
			resolved, err := resolveCreateStorageClass(cfg, orgStoragePolicy{
				DataResidency: orgDataResidencyFlexible,
			}, "eu.example.com", requested)
			if err == nil {
				t.Fatalf("resolveCreateStorageClass(%q) = %q, want rejection", requested, resolved)
			}
		})

		t.Run("strict/"+requested, func(t *testing.T) {
			resolved, err := resolveCreateStorageClass(cfg, orgStoragePolicy{
				DataResidency: orgDataResidencyStrict,
				DefaultRegion: "eu",
			}, "eu.example.com", requested)
			if err == nil {
				t.Fatalf("resolveCreateStorageClass(%q) = %q, want rejection", requested, resolved)
			}
		})
	}
}

func TestValidateOrgStoragePolicy(t *testing.T) {
	cfg := testStoragePolicyConfig()

	if err := validateOrgStoragePolicy(cfg, orgStoragePolicy{DataResidency: orgDataResidencyFlexible, DefaultRegion: "usa"}); err != nil {
		t.Fatalf("validateOrgStoragePolicy returned unexpected error: %v", err)
	}

	if err := validateOrgStoragePolicy(cfg, orgStoragePolicy{DataResidency: orgDataResidencyStrict}); err == nil {
		t.Fatal("expected error for strict policy without default_region")
	}

	if err := validateOrgStoragePolicy(cfg, orgStoragePolicy{DataResidency: orgDataResidencyStrict, DefaultRegion: "apac"}); err == nil {
		t.Fatal("expected error for unknown default_region")
	}
}

func TestListConfiguredStorageRegions(t *testing.T) {
	regions := listConfiguredStorageRegions(testStoragePolicyConfig())
	if len(regions) != 2 {
		t.Fatalf("len(regions) = %d, want 2", len(regions))
	}
	if regions[0] != "eu" || regions[1] != "usa" {
		t.Fatalf("regions = %v, want [eu usa]", regions)
	}
}

func TestListConfiguredStorageRegionLabelsUsesClassLabels(t *testing.T) {
	cfg := testStoragePolicyConfig()
	cfg.Storage.Classes = map[string]config.StorageClassConfig{
		"hot-s3-usa": {Tier: "hot", Label: "North America"},
		"hot-s3-eu":  {Tier: "hot", Label: "Europe"},
		"cold-s3-eu": {Tier: "cold"},
	}

	labels := listConfiguredStorageRegionLabels(cfg)
	if labels["usa"] != "North America" {
		t.Fatalf("labels[usa] = %q, want %q", labels["usa"], "North America")
	}
	if labels["eu"] != "Europe" {
		t.Fatalf("labels[eu] = %q, want %q", labels["eu"], "Europe")
	}
}

func TestDisplayStorageNameForConfigUsesClassLabel(t *testing.T) {
	cfg := testStoragePolicyConfig()
	cfg.Storage.Classes = map[string]config.StorageClassConfig{
		"hot-s3-usa": {Tier: "hot", Label: "North America"},
		"hot-s3-eu":  {Tier: "hot"},
		"cold-s3-eu": {Tier: "cold"},
	}

	if got := displayStorageNameForConfig(cfg, "hot-s3-usa"); got != "North America" {
		t.Fatalf("displayStorageNameForConfig = %q, want %q", got, "North America")
	}
}

func TestResolveStrictCreateStorageClassRequiresConfiguredRegion(t *testing.T) {
	_, err := resolveCreateStorageClass(testStoragePolicyConfig(), orgStoragePolicy{DataResidency: orgDataResidencyStrict}, "eu.example.com", "")
	if err == nil {
		t.Fatal("expected error for strict policy without configured region")
	}
}
