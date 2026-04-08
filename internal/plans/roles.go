package plans

import "github.com/Sesame-Disk/sesamefs/internal/config"

// AllFeatureFlags is the canonical list of all capability flags.
// Order matters for deterministic upgrade_features output.
var AllFeatureFlags = []string{
	"can_add_repo",
	"can_share_repo",
	"can_add_group",
	"can_generate_share_link",
	"can_generate_upload_link",
	"can_send_share_link_mail",
	"can_publish_repo",
	"can_use_global_address_book",
	"can_connect_with_desktop_clients",
	"can_connect_with_android_clients",
	"can_connect_with_ios_clients",
	"can_export_files_via_mobile_client",
}

// allTrue is a convenience map with all flags set to true.
var allTrue = func() map[string]bool {
	m := make(map[string]bool, len(AllFeatureFlags))
	for _, f := range AllFeatureFlags {
		m[f] = true
	}
	return m
}()

// connectivityOnly has only the 4 connectivity flags set to true.
var connectivityOnly = map[string]bool{
	"can_connect_with_desktop_clients":  true,
	"can_connect_with_android_clients":  true,
	"can_connect_with_ios_clients":      true,
	"can_export_files_via_mobile_client": true,
}

// RolePermissions maps each org role to its feature-flag ceiling.
// These define the maximum capabilities a role can attempt —
// the enforcement profile further restricts based on quota_policy.
var RolePermissions = map[string]map[string]bool{
	"superadmin": allTrue,
	"owner":      allTrue,
	"admin":      allTrue,
	"user":       allTrue,
	"readonly":   connectivityOnly,
	"guest":      {}, // no capabilities
}

// ProfileFeatureMap converts an EnforcementFeatures struct into a map[string]bool
// keyed by the canonical flag names.
func ProfileFeatureMap(f config.EnforcementFeatures) map[string]bool {
	return map[string]bool{
		"can_add_repo":                      f.CanAddRepo,
		"can_share_repo":                    f.CanShareRepo,
		"can_add_group":                     f.CanAddGroup,
		"can_generate_share_link":           f.CanGenerateShareLink,
		"can_generate_upload_link":          f.CanGenerateUploadLink,
		"can_send_share_link_mail":          f.CanSendShareLinkMail,
		"can_publish_repo":                  f.CanPublishRepo,
		"can_use_global_address_book":       f.CanUseGlobalAddressBook,
		"can_connect_with_desktop_clients":  f.CanConnectWithDesktopClients,
		"can_connect_with_android_clients":  f.CanConnectWithAndroidClients,
		"can_connect_with_ios_clients":      f.CanConnectWithIOSClients,
		"can_export_files_via_mobile_client": f.CanExportFilesViaMobileClient,
	}
}
