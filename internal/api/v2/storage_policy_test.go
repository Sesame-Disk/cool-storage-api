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
				"hot-s3-usa": {Tier: "hot"},
				"hot-s3-eu":  {Tier: "hot"},
				"cold-s3-eu": {Tier: "cold"},
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

func TestResolveStrictCreateStorageClassRequiresConfiguredRegion(t *testing.T) {
	_, err := resolveCreateStorageClass(testStoragePolicyConfig(), orgStoragePolicy{DataResidency: orgDataResidencyStrict}, "eu.example.com", "")
	if err == nil {
		t.Fatal("expected error for strict policy without configured region")
	}
}
