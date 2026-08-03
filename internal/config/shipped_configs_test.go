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

			if err := validateDownloadAdmissionConfig(cfg.DownloadAdmission, cfg.Server.WriteTimeout); err != nil {
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

// TestShippedConfigsCapIWorkPreviewSource guards the measured memory budget. The
// preview branch materialises the whole source document, and D6 measured the
// peak at roughly 4x the source plaintext and 6x encrypted. Left on the general
// 1 GiB preview limit, one request can touch several gigabytes, and the only
// remaining bound would be max_active_raw — which also governs ordinary raw
// streams that cost a single block.
func TestShippedConfigsCapIWorkPreviewSource(t *testing.T) {
	const measuredEncryptedPeakRatio = 6
	const budgetBytes = 2 << 30 // the stated per-node download budget

	for _, path := range shippedConfigPaths(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			cfg := loadShippedConfig(t, path)

			source := cfg.FileView.MaxIWorkSourceBytes
			if source <= 0 {
				t.Fatal("max_iwork_source_bytes is unset; the preview branch would inherit the 1 GiB general limit")
			}
			if source > cfg.FileView.MaxPreviewBytes {
				t.Fatalf("iWork source cap %d exceeds the general preview limit %d", source, cfg.FileView.MaxPreviewBytes)
			}

			raw := int64(cfg.DownloadAdmission.MaxActiveRaw)
			if raw <= 0 {
				t.Fatal("max_active_raw is unset, so the iWork peak has no concurrency bound")
			}
			worst := raw * source * measuredEncryptedPeakRatio
			if worst > budgetBytes {
				t.Fatalf("max_active_raw(%d) x source(%d B) x measured %dx peak = %d B, over the stated %d B budget",
					raw, source, measuredEncryptedPeakRatio, worst, budgetBytes)
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
