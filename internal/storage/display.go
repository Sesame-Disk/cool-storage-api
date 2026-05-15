package storage

import (
	"sort"
	"strings"

	"github.com/Sesame-Disk/sesamefs/internal/config"
)

func FormatRegionLabel(region string) string {
	region = strings.TrimSpace(region)
	if region == "" {
		return ""
	}

	parts := strings.FieldsFunc(region, func(r rune) bool {
		return r == '-' || r == '_' || r == ' '
	})
	for i, part := range parts {
		lower := strings.ToLower(part)
		switch lower {
		case "us", "usa", "eu", "uk", "uae", "na", "apac":
			parts[i] = strings.ToUpper(lower)
		default:
			parts[i] = strings.ToUpper(lower[:1]) + lower[1:]
		}
	}

	return strings.Join(parts, " ")
}

func ConfiguredStorageLabel(cfg *config.Config, storageClass string) string {
	storageClass = strings.TrimSpace(storageClass)
	if storageClass == "" || cfg == nil {
		return ""
	}

	if classConfig, ok := cfg.Storage.Classes[storageClass]; ok {
		if label := strings.TrimSpace(classConfig.Label); label != "" {
			return label
		}
	}

	if backendConfig, ok := cfg.Storage.Backends[storageClass]; ok {
		if label := strings.TrimSpace(backendConfig.Label); label != "" {
			return label
		}
	}

	return ""
}

func StorageClassRegion(cfg *config.Config, storageClass string) string {
	if cfg == nil {
		return ""
	}
	storageClass = strings.TrimSpace(storageClass)
	if storageClass == "" {
		return ""
	}
	for region, regionConfig := range cfg.Storage.RegionClasses {
		if regionConfig.Hot == storageClass || regionConfig.Cold == storageClass {
			return strings.ToLower(strings.TrimSpace(region))
		}
	}
	return ""
}

func DisplayStorageRegion(cfg *config.Config, region string) string {
	region = strings.ToLower(strings.TrimSpace(region))
	if region == "" {
		return ""
	}
	if cfg != nil {
		for configuredRegion, regionConfig := range cfg.Storage.RegionClasses {
			if !strings.EqualFold(configuredRegion, region) {
				continue
			}
			if label := ConfiguredStorageLabel(cfg, regionConfig.Hot); label != "" {
				return label
			}
			return FormatRegionLabel(configuredRegion)
		}
	}

	return FormatRegionLabel(region)
}

func DisplayStorageName(cfg *config.Config, storageClass string) string {
	storageClass = strings.TrimSpace(storageClass)
	if storageClass == "" || cfg == nil {
		return storageClass
	}

	if label := ConfiguredStorageLabel(cfg, storageClass); label != "" {
		return label
	}

	if region := StorageClassRegion(cfg, storageClass); region != "" {
		return DisplayStorageRegion(cfg, region)
	}

	return storageClass
}

func ListConfiguredStorageRegions(cfg *config.Config) []string {
	if cfg == nil {
		return []string{}
	}

	regions := make([]string, 0, len(cfg.Storage.RegionClasses))
	for region, regionConfig := range cfg.Storage.RegionClasses {
		if hotStorageClassForRegion(cfg, regionConfig.Hot) {
			regions = append(regions, strings.ToLower(strings.TrimSpace(region)))
		}
	}
	sort.Strings(regions)
	return regions
}

func ListConfiguredStorageRegionLabels(cfg *config.Config) map[string]string {
	labels := make(map[string]string)
	for _, region := range ListConfiguredStorageRegions(cfg) {
		labels[region] = DisplayStorageRegion(cfg, region)
	}
	return labels
}

func hotStorageClassForRegion(cfg *config.Config, storageClass string) bool {
	storageClass = strings.TrimSpace(storageClass)
	if cfg == nil || storageClass == "" {
		return false
	}
	if classConfig, ok := cfg.Storage.Classes[storageClass]; ok {
		return strings.ToLower(strings.TrimSpace(classConfig.Tier)) != string(AccessDelayed)
	}
	_, ok := cfg.Storage.Backends[storageClass]
	return ok
}
