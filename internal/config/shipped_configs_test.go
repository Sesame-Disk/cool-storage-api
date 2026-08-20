package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// D6 ships the first positive download-admission values and auto-capacity
// policy, which turns a whole
// class of configuration defect from latent into fatal: with enabled: true the
// validator refuses zero caps and a non-zero server.write_timeout, so a single
// shipped file that missed a key now stops that deployment from starting.
//
// Subcontract B and C both had to fix "the key exists in the struct but only in
// some config files", and criterion 14 exists because of it. Loading every
// shipped configuration through the real validator is the only check that
// actually covers that, and it is cheap enough to keep forever.
func shippedConfigPaths(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("..", "..", "configs", "config*.yaml"))
	if err != nil {
		t.Fatalf("glob shipped configs: %v", err)
	}
	if len(paths) < 7 {
		t.Fatalf("found %d shipped configs, expected the full set; the glob or the layout changed", len(paths))
	}
	return paths
}

func loadShippedConfig(t *testing.T, path string) *Config {
	t.Helper()
	cfg := DefaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return cfg
}

// Some shipped production files intentionally leave class buckets empty because
// deployment injects them through S3_CLASS_<CLASS>_BUCKET. Reference validation
// below exercises the topology independently; fill those deployment placeholders
// with deterministic test buckets. Config.Validate still rejects an empty bucket
// when the deployment did not provide the required environment override.
func hydrateShippedStoragePlaceholders(cfg *Config) {
	for name, classCfg := range cfg.Storage.Classes {
		if strings.TrimSpace(classCfg.Bucket) == "" {
			classCfg.Bucket = "shipped-test-" + name
			cfg.Storage.Classes[name] = classCfg
		}
	}
}

func TestShippedConfigsPassDownloadAdmissionValidation(t *testing.T) {
	for _, path := range shippedConfigPaths(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			cfg := loadShippedConfig(t, path)

			if err := cfg.resolveDownloadAdmissionCapacity(); err != nil {
				t.Fatalf("shipped capacity derivation failed: %v", err)
			}
			if err := cfg.validateDownloadAdmissionBounds(); err != nil {
				t.Fatalf("shipped configuration would refuse to start: %v", err)
			}
		})
	}
}

// TestShippedConfigsEnableDownloadAdmission pins the D6 flip itself. Positive
// values that are never switched on protect nothing, and B4's finding is that
// the download surface is unprotected — so a config that quietly reverts to
// enabled: false is a regression, not a preference.
func TestShippedConfigsEnableDownloadAdmission(t *testing.T) {
	for _, path := range shippedConfigPaths(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			cfg := loadShippedConfig(t, path)

			if !cfg.DownloadAdmission.Enabled {
				t.Fatal("download admission is disabled; D6 measured the values and turned it on")
			}
			// server.write_timeout is process-wide and http.Server does not expose
			// the deadline it installed, so D cannot refresh a non-zero one without
			// risking extending it. The contract declares that combination
			// unsupported and the validator refuses it.
			if cfg.Server.WriteTimeout != 0 {
				t.Fatalf("server.write_timeout = %v; must stay 0 while download admission is enabled", cfg.Server.WriteTimeout)
			}
		})
	}
}

// TestShippedConfigsMatchTheCodeDefaults keeps the two places the D6 capacities
// live from drifting apart. Validation alone would not notice: a YAML file and
// DefaultConfig can both stay individually valid while describing different
// designs, and then "the measured values" means two things depending on whether
// a deployment pins the section. If a deployment ever needs different
// capacities, this test is where that decision gets recorded rather than
// happening silently.
func TestShippedConfigsMatchTheCodeDefaults(t *testing.T) {
	want := defaultDownloadAdmissionConfig()
	for _, path := range shippedConfigPaths(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			cfg := loadShippedConfig(t, path)

			if cfg.DownloadAdmission != want {
				t.Fatalf("shipped download admission = %#v, want the code defaults %#v", cfg.DownloadAdmission, want)
			}
		})
	}
}

// TestShippedConfigsCapIWorkPreviewSource guards the combined measured memory
// budget after auto capacity derivation. The preview branch materialises the
// whole source document, while encrypted streaming scales with the maximum sync
// block admitted by the server. Both terms must fit the safety-adjusted
// process-local D6 budget together.
func TestShippedConfigsCapIWorkPreviewSource(t *testing.T) {
	for _, path := range shippedConfigPaths(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			cfg := loadShippedConfig(t, path)
			if err := cfg.resolveDownloadAdmissionCapacity(); err != nil {
				t.Fatalf("shipped capacity derivation failed: %v", err)
			}

			if err := cfg.validateDownloadAdmissionMemoryBudget(); err != nil {
				t.Fatalf("shipped memory design is unsafe: %v", err)
			}
		})
	}
}

// TestShippedConfigsKeepProfileCapsUnderTheNodeCeiling restates criterion 3 as a
// configuration property: a profile cap is a sub-limit, never an escape hatch
// that lets the aggregate node bound be exceeded.
func TestShippedConfigsKeepProfileCapsUnderTheNodeCeiling(t *testing.T) {
	for _, path := range shippedConfigPaths(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			cfg := loadShippedConfig(t, path)
			d := cfg.DownloadAdmission

			for name, cap := range map[string]int{
				"max_active_block":       d.MaxActiveBlock,
				"max_active_file":        d.MaxActiveFile,
				"max_active_raw":         d.MaxActiveRaw,
				"max_active_history":     d.MaxActiveHistory,
				"max_active_link_raw":    d.MaxActiveLinkRaw,
				"max_active_zip":         d.MaxActiveZIP,
				"max_active_link_inline": d.MaxActiveLinkInline,
			} {
				if cap > d.MaxActivePerNode {
					t.Fatalf("%s = %d exceeds max_active_per_node = %d", name, cap, d.MaxActivePerNode)
				}
			}
		})
	}
}

// Nothing reserves "hot" globally -- what is enforced is that the class and legacy
// backend namespaces may not overlap. In practice that still rejects a modern class
// named "hot", but only because DefaultConfig ships a legacy "hot" backend and YAML
// decoding MERGES into that map rather than replacing it. That merge is the load
// bearing and non-obvious part: a `backends: {}` line looks like it clears the
// default and does not, while a null `backends:` does. The comment in DefaultConfig
// states the rule; this pins the behavior it rests on.
func TestModernHotClassCollidesWithTheShippedLegacyBackend(t *testing.T) {
	for _, tc := range []struct {
		name          string
		doc           string
		wantCollision bool
	}{
		{"backends omitted", `storage:
  classes:
    hot:
      type: s3
      bucket: b
`, true},
		{"empty mapping merges, does not clear", `storage:
  backends: {}
  classes:
    hot:
      type: s3
      bucket: b
`, true},
		{"nulled leaves no legacy loop to fall through to", `storage:
  backends:
  classes:
    hot:
      type: s3
      bucket: b
`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			if err := yaml.Unmarshal([]byte(tc.doc), cfg); err != nil {
				t.Fatalf("parse: %v", err)
			}
			if _, legacyHot := cfg.Storage.Backends["hot"]; legacyHot != tc.wantCollision {
				t.Fatalf("legacy backends[hot] present = %v, want %v", legacyHot, tc.wantCollision)
			}

			err := validateStorageClassNames(cfg.Storage)
			if tc.wantCollision {
				if err == nil || !strings.Contains(err.Error(), "collides") {
					t.Fatalf("error = %v, want a collision rejection", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v, want acceptance: with no legacy entry there is no second binding to fall through to", err)
			}
		})
	}
}

func TestShippedConfigsUseCanonicalStorageClassNames(t *testing.T) {
	for _, path := range shippedConfigPaths(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			cfg := loadShippedConfig(t, path)
			if err := validateStorageClassNames(cfg.Storage); err != nil {
				t.Fatalf("shipped configuration has invalid storage class identity: %v", err)
			}
		})
	}
}

func TestShippedActiveStorageEntriesDeclareRegions(t *testing.T) {
	for _, path := range shippedConfigPaths(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			cfg := loadShippedConfig(t, path)
			if err := cfg.validateActiveStorageRegions(); err != nil {
				t.Fatalf("shipped active storage entry has no explicit region: %v", err)
			}
		})
	}
}

// Every storage class a shipped config REFERENCES must resolve to one it
// declares. A typo in failover_class is invisible until the primary backend is
// down, so the repo's own files are the cheapest place to catch it.
func TestShippedConfigsResolveEveryStorageClassReference(t *testing.T) {
	for _, path := range shippedConfigPaths(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			cfg := loadShippedConfig(t, path)
			hydrateShippedStoragePlaceholders(cfg)
			if err := cfg.validateStorageClassIdentity(); err != nil {
				t.Fatalf("shipped configuration would refuse to start: %v", err)
			}
		})
	}
}

// R23b states the second half of the storage identity contract: a class name is
// the permanent identity of ONE physical namespace, so one namespace may not
// answer to two names. config.docker.yaml keeps both modern and legacy names for
// compatibility, but the legacy "hot" backend uses a separate MinIO bucket from
// hot-minio-local, so the dev stack does not give two identities one org key space.
//
// Scope limit worth knowing: hydrateShippedStoragePlaceholders fills empty buckets
// with a per-name placeholder, so classes whose bucket comes from the deployment
// environment are made distinct here by construction. This test proves the shipped
// FILES declare no alias; a deployment that points two of those env vars at one
// bucket is caught by Config.Validate at startup, not here.
func TestShippedConfigsDeclareOneClassPerPhysicalNamespace(t *testing.T) {
	for _, path := range shippedConfigPaths(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			cfg := loadShippedConfig(t, path)
			hydrateShippedStoragePlaceholders(cfg)
			if err := cfg.validateStorageClassNamespaceAliasing(); err != nil {
				t.Fatalf("shipped configuration aliases a storage namespace: %v", err)
			}
		})
	}
}

func TestDockerConfigKeepsLegacyBackendInSeparateBucket(t *testing.T) {
	var dockerPath string
	for _, path := range shippedConfigPaths(t) {
		if filepath.Base(path) == "config.docker.yaml" {
			dockerPath = path
			break
		}
	}
	if dockerPath == "" {
		t.Fatal("config.docker.yaml was not found")
	}

	cfg := loadShippedConfig(t, dockerPath)
	legacy, ok := cfg.Storage.Backends["hot"]
	if !ok || strings.TrimSpace(legacy.Bucket) == "" {
		t.Fatal("config.docker.yaml must keep a configured legacy hot backend")
	}
	modern, ok := cfg.Storage.Classes["hot-minio-local"]
	if !ok || strings.TrimSpace(modern.Bucket) == "" {
		t.Fatal("config.docker.yaml must keep the modern local storage class")
	}
	if legacy.Bucket == modern.Bucket && legacy.Endpoint == modern.Endpoint {
		t.Fatalf("legacy hot and modern local class share a physical namespace: legacy=%#v modern=%#v", legacy, modern)
	}
	if err := cfg.validateStorageClassNamespaceAliasing(); err != nil {
		t.Fatalf("config.docker.yaml aliases a storage namespace: %v", err)
	}
}

func TestDockerComposePinsLocalSesameFSS3Namespace(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docker-compose.yaml"))
	if err != nil {
		t.Fatalf("read docker-compose.yaml: %v", err)
	}
	var compose struct {
		Services map[string]struct {
			Build       any      `yaml:"build"`
			EnvFile     []string `yaml:"env_file"`
			Environment []string `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(data, &compose); err != nil {
		t.Fatalf("parse docker-compose.yaml: %v", err)
	}

	want := map[string]string{
		"S3_REGION":   "us-east-1",
		"S3_ENDPOINT": "http://minio:9000",
		"S3_BUCKET":   "sesamefs-legacy-blocks",
	}
	checked := 0
	for name, service := range compose.Services {
		if !strings.HasPrefix(name, "sesamefs") || service.Build == nil || len(service.EnvFile) == 0 {
			continue
		}
		checked++
		environment := make(map[string]string, len(service.Environment))
		for _, assignment := range service.Environment {
			key, value, ok := strings.Cut(assignment, "=")
			if ok {
				environment[key] = value
			}
		}
		for key, value := range want {
			if got := environment[key]; got != value {
				t.Errorf("services.%s.environment %s = %q, want fixed value %q", name, key, got, value)
			}
		}
	}
	if checked != 4 {
		t.Fatalf("checked %d local SesameFS services, want 4", checked)
	}
}
