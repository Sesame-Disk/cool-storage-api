package config

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// D6 ships the first positive download-admission values, which turns a whole
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

func TestShippedConfigsPassDownloadAdmissionValidation(t *testing.T) {
	for _, path := range shippedConfigPaths(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			cfg := loadShippedConfig(t, path)

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
// budget. The preview branch materialises the whole source document, while
// encrypted streaming scales with the maximum sync block admitted by the
// server. Both terms must fit the stated process-local D6 budget together.
func TestShippedConfigsCapIWorkPreviewSource(t *testing.T) {
	for _, path := range shippedConfigPaths(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			cfg := loadShippedConfig(t, path)

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
