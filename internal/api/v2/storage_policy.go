package v2

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
)

const (
	orgDataResidencyFlexible = "flexible"
	orgDataResidencyStrict   = "strict"
)

type orgStoragePolicy struct {
	DataResidency string
	DefaultRegion string
}

func defaultOrgStoragePolicy() orgStoragePolicy {
	return orgStoragePolicy{DataResidency: orgDataResidencyFlexible}
}

func normalizeOrgStoragePolicy(raw map[string]string) (orgStoragePolicy, error) {
	policy := defaultOrgStoragePolicy()
	if len(raw) == 0 {
		return policy, nil
	}

	if residency := strings.ToLower(strings.TrimSpace(raw["data_residency"])); residency != "" {
		switch residency {
		case orgDataResidencyFlexible, orgDataResidencyStrict:
			policy.DataResidency = residency
		default:
			return orgStoragePolicy{}, fmt.Errorf("data_residency must be 'strict' or 'flexible'")
		}
	}
	policy.DefaultRegion = strings.ToLower(strings.TrimSpace(raw["default_region"]))

	return policy, nil
}

func (p orgStoragePolicy) storageConfig() map[string]string {
	configMap := map[string]string{
		"data_residency": p.DataResidency,
	}
	if p.DefaultRegion != "" {
		configMap["default_region"] = p.DefaultRegion
	}
	return configMap
}

func isKnownStorageClass(cfg *config.Config, class string) bool {
	class = strings.TrimSpace(class)
	if class == "" || cfg == nil {
		return false
	}
	if _, ok := cfg.Storage.Classes[class]; ok {
		return true
	}
	if _, ok := cfg.Storage.Backends[class]; ok {
		return true
	}
	return false
}

func isHotStorageClass(cfg *config.Config, class string) bool {
	class = strings.TrimSpace(class)
	if class == "" || cfg == nil {
		return false
	}
	if classCfg, ok := cfg.Storage.Classes[class]; ok {
		return strings.ToLower(strings.TrimSpace(classCfg.Tier)) != "cold"
	}
	_, ok := cfg.Storage.Backends[class]
	return ok
}

func resolveEndpointRegion(cfg *config.Config, hostname string) string {
	if cfg == nil {
		return "default"
	}

	return storage.ResolveEndpointRegion(hostname, cfg.Storage.EndpointRegions, cfg.Server.Region)
}

func hotStorageClassForRegion(cfg *config.Config, region string) string {
	if cfg == nil {
		return ""
	}
	region = strings.ToLower(strings.TrimSpace(region))
	if region == "" {
		return ""
	}
	for configuredRegion, regionConfig := range cfg.Storage.RegionClasses {
		if strings.EqualFold(configuredRegion, region) && regionConfig.Hot != "" && isHotStorageClass(cfg, regionConfig.Hot) {
			return regionConfig.Hot
		}
	}
	return ""
}

func storageClassRegion(cfg *config.Config, storageClass string) string {
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

func resolveGlobalDefaultHotStorageClass(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}

	if isHotStorageClass(cfg, cfg.Storage.DefaultClass) {
		return strings.TrimSpace(cfg.Storage.DefaultClass)
	}

	classNames := make([]string, 0, len(cfg.Storage.Classes))
	for name := range cfg.Storage.Classes {
		classNames = append(classNames, name)
	}
	sort.Strings(classNames)
	for _, name := range classNames {
		if isHotStorageClass(cfg, name) {
			return name
		}
	}

	backendNames := make([]string, 0, len(cfg.Storage.Backends))
	for name := range cfg.Storage.Backends {
		backendNames = append(backendNames, name)
	}
	sort.Strings(backendNames)
	for _, name := range backendNames {
		if isHotStorageClass(cfg, name) {
			return name
		}
	}

	return ""
}

func resolveDefaultHotStorageClass(cfg *config.Config, hostname string) string {
	if region := resolveEndpointRegion(cfg, hostname); region != "" && region != "default" {
		if storageClass := hotStorageClassForRegion(cfg, region); storageClass != "" {
			return storageClass
		}
	}
	return resolveGlobalDefaultHotStorageClass(cfg)
}

func validateRequestedCreateStorageClass(cfg *config.Config, requestedClass string) error {
	if !isKnownStorageClass(cfg, requestedClass) {
		return fmt.Errorf("invalid storage class")
	}
	if !isHotStorageClass(cfg, requestedClass) {
		return fmt.Errorf("storage class must use hot tier")
	}
	return nil
}

func validateOrgStoragePolicy(cfg *config.Config, policy orgStoragePolicy) error {
	if policy.DataResidency == orgDataResidencyStrict && policy.DefaultRegion == "" {
		return fmt.Errorf("strict data residency requires a default_region")
	}
	if policy.DefaultRegion == "" {
		return nil
	}
	if hotStorageClassForRegion(cfg, policy.DefaultRegion) == "" {
		return fmt.Errorf("default_region must reference a configured region with a hot storage class")
	}
	return nil
}

func resolveFlexibleCreateStorageClass(cfg *config.Config, policy orgStoragePolicy, hostname, requestedClass string) (string, error) {
	requestedClass = strings.TrimSpace(requestedClass)
	if requestedClass != "" {
		if err := validateRequestedCreateStorageClass(cfg, requestedClass); err != nil {
			return "", err
		}
		return requestedClass, nil
	}

	if region := resolveEndpointRegion(cfg, hostname); region != "" && region != "default" {
		if storageClass := hotStorageClassForRegion(cfg, region); storageClass != "" {
			return storageClass, nil
		}
	}
	if policy.DefaultRegion != "" {
		if storageClass := hotStorageClassForRegion(cfg, policy.DefaultRegion); storageClass != "" {
			return storageClass, nil
		}
	}

	if storageClass := resolveGlobalDefaultHotStorageClass(cfg); storageClass != "" {
		return storageClass, nil
	}
	return "", fmt.Errorf("no valid hot storage class configured")
}

func resolveStrictCreateStorageClass(cfg *config.Config, policy orgStoragePolicy, hostname, requestedClass string) (string, error) {
	allowedRegion := strings.ToLower(strings.TrimSpace(policy.DefaultRegion))
	if allowedRegion == "" {
		return "", fmt.Errorf("organization data residency policy is misconfigured; contact an administrator or support")
	}
	if hotStorageClassForRegion(cfg, allowedRegion) == "" {
		return "", fmt.Errorf("organization data residency policy is misconfigured; contact an administrator or support")
	}

	requestedClass = strings.TrimSpace(requestedClass)
	if requestedClass != "" {
		if err := validateRequestedCreateStorageClass(cfg, requestedClass); err != nil {
			return "", err
		}
		if !strings.EqualFold(storageClassRegion(cfg, requestedClass), allowedRegion) {
			return "", fmt.Errorf("storage class is not allowed by organization data residency policy")
		}
		return requestedClass, nil
	}

	if storageClass := hotStorageClassForRegion(cfg, allowedRegion); storageClass != "" {
		return storageClass, nil
	}
	return "", fmt.Errorf("organization data residency policy is misconfigured; contact an administrator or support")
}

func resolveCreateStorageClass(cfg *config.Config, policy orgStoragePolicy, hostname, requestedClass string) (string, error) {
	if policy.DataResidency == orgDataResidencyStrict {
		return resolveStrictCreateStorageClass(cfg, policy, hostname, requestedClass)
	}
	return resolveFlexibleCreateStorageClass(cfg, policy, hostname, requestedClass)
}

func readOrgStoragePolicy(database *db.DB, orgID string) (orgStoragePolicy, error) {
	policy := defaultOrgStoragePolicy()
	if database == nil || database.Session() == nil || strings.TrimSpace(orgID) == "" {
		return policy, nil
	}

	var storageConfig map[string]string
	if err := database.Session().Query(`
		SELECT storage_config FROM organizations WHERE org_id = ?
	`, orgID).Scan(&storageConfig); err != nil {
		return orgStoragePolicy{}, err
	}
	return normalizeOrgStoragePolicy(storageConfig)
}

func resolveCreateStorageClassForOrg(database *db.DB, cfg *config.Config, orgID, hostname, requestedClass string) (string, error) {
	policy, err := readOrgStoragePolicy(database, orgID)
	if err != nil {
		return "", err
	}
	return resolveCreateStorageClass(cfg, policy, hostname, requestedClass)
}

func listConfiguredStorageRegions(cfg *config.Config) []string {
	return storage.ListConfiguredStorageRegions(cfg)
}

func listConfiguredStorageRegionLabels(cfg *config.Config) map[string]string {
	return storage.ListConfiguredStorageRegionLabels(cfg)
}

func displayStorageNameForConfig(cfg *config.Config, storageClass string) string {
	return storage.DisplayStorageName(cfg, storageClass)
}
