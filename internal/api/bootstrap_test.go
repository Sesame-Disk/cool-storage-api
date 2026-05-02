package api

import (
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/gin-gonic/gin"
)

func TestBuildAppBootstrapPageOptionsIncludesSettingsBasics(t *testing.T) {
	s := createTestServer()
	s.config.Accounts.DeleteAccountURL = "https://accounts.example.com/accounts/delete/"
	identity := bootstrapIdentity{UserID: "user-1", OrgID: "org-1", Role: "user", Email: "user@example.com"}
	userData := bootstrapUserData{Email: "user@example.com", Name: "Test User", Role: "user"}

	pageOptions := s.buildAppBootstrapPageOptions(identity, userData, bootstrapOrgData{})

	if pageOptions["name"] != "Test User" {
		t.Fatalf("name = %v, want %q", pageOptions["name"], "Test User")
	}
	if pageOptions["username"] != "user@example.com" {
		t.Fatalf("username = %v, want %q", pageOptions["username"], "user@example.com")
	}
	if pageOptions["contactEmail"] != "user@example.com" {
		t.Fatalf("contactEmail = %v, want %q", pageOptions["contactEmail"], "user@example.com")
	}
	if pageOptions["orgID"] != "org-1" {
		t.Fatalf("orgID = %v, want %q", pageOptions["orgID"], "org-1")
	}
	if pageOptions["avatarURL"] != "/static/img/default-avatar.png" {
		t.Fatalf("avatarURL = %v, want %q", pageOptions["avatarURL"], "/static/img/default-avatar.png")
	}
	if enabled, ok := pageOptions["enableUpdateUserInfo"].(bool); !ok || !enabled {
		t.Fatalf("enableUpdateUserInfo = %v, want true", pageOptions["enableUpdateUserInfo"])
	}
	if enabled, ok := pageOptions["enableUserSetContactEmail"].(bool); !ok || enabled {
		t.Fatalf("enableUserSetContactEmail = %v, want false", pageOptions["enableUserSetContactEmail"])
	}
	if enabled, ok := pageOptions["enableUserSetName"].(bool); !ok || !enabled {
		t.Fatalf("enableUserSetName = %v, want true", pageOptions["enableUserSetName"])
	}
	if pageOptions["nameLabel"] != "Name:" {
		t.Fatalf("nameLabel = %v, want %q", pageOptions["nameLabel"], "Name:")
	}
	currentLang, ok := pageOptions["currentLang"].(gin.H)
	if !ok {
		t.Fatalf("currentLang has unexpected type: %T", pageOptions["currentLang"])
	}
	if currentLang["langCode"] != "en" {
		t.Fatalf("currentLang.langCode = %v, want %q", currentLang["langCode"], "en")
	}
	backendRoutes, ok := pageOptions["backendRoutes"].(gin.H)
	if !ok {
		t.Fatalf("backendRoutes has unexpected type: %T", pageOptions["backendRoutes"])
	}
	if backendRoutes["deleteAccount"] != "accounts/delete/" {
		t.Fatalf("backendRoutes.deleteAccount = %v, want %q", backendRoutes["deleteAccount"], "accounts/delete/")
	}
	if enabled, ok := pageOptions["enableAPIKeys"].(bool); !ok || !enabled {
		t.Fatalf("enableAPIKeys = %v, want true", pageOptions["enableAPIKeys"])
	}
	if enabled, ok := pageOptions["enableDeleteAccount"].(bool); !ok || !enabled {
		t.Fatalf("enableDeleteAccount = %v, want true", pageOptions["enableDeleteAccount"])
	}
	inlinePreviewExtensions, ok := pageOptions["inlinePreviewExtensions"].([]string)
	if !ok {
		t.Fatalf("inlinePreviewExtensions has unexpected type: %T", pageOptions["inlinePreviewExtensions"])
	}
	if len(inlinePreviewExtensions) == 0 {
		t.Fatalf("inlinePreviewExtensions should not be empty")
	}
	if inlinePreviewExtensions[0] != s.config.FileView.PreviewExtensions[0] {
		t.Fatalf("inlinePreviewExtensions[0] = %q, want %q", inlinePreviewExtensions[0], s.config.FileView.PreviewExtensions[0])
	}
}

func TestBuildBootstrapStorageOptionsUsesRegionLabelsAndDefault(t *testing.T) {
	s := createTestServer()
	s.config.Storage.DefaultClass = "hot-usa"
	s.config.Storage.EndpointRegions = map[string]string{
		"us.example.com": "usa",
		"eu.example.com": "eu",
	}
	s.config.Storage.RegionClasses = map[string]config.RegionClassConfig{
		"usa": {Hot: "hot-usa"},
		"eu":  {Hot: "hot-eu"},
	}
	s.config.Storage.Classes = map[string]config.StorageClassConfig{
		"hot-usa": {},
		"hot-eu":  {},
	}
	s.config.Storage.Backends = map[string]config.BackendConfig{}

	options := s.buildBootstrapStorageOptions("eu.example.com")
	if len(options) < 2 {
		t.Fatalf("len(options) = %d, want at least 2", len(options))
	}

	var foundEU bool
	for _, option := range options {
		if option["id"] == "hot-eu" {
			foundEU = true
			if option["name"] != "EU" {
				t.Fatalf("eu option name = %v, want %q", option["name"], "EU")
			}
			if option["region"] != "eu" {
				t.Fatalf("eu option region = %v, want %q", option["region"], "eu")
			}
			if option["is_default"] != true {
				t.Fatalf("eu option is_default = %v, want true", option["is_default"])
			}
		}
	}

	if !foundEU {
		t.Fatalf("expected hot-eu option in %v", options)
	}
}

func TestBuildBootstrapStorageOptionsFallsBackToServerRegion(t *testing.T) {
	s := createTestServer()
	s.config.Server.Region = "eu"
	s.config.Storage.DefaultClass = "hot-usa"
	s.config.Storage.EndpointRegions = map[string]string{
		"*": "usa",
	}
	s.config.Storage.RegionClasses = map[string]config.RegionClassConfig{
		"usa": {Hot: "hot-usa"},
		"eu":  {Hot: "hot-eu"},
	}
	s.config.Storage.Classes = map[string]config.StorageClassConfig{
		"hot-usa": {},
		"hot-eu":  {},
	}
	s.config.Storage.Backends = map[string]config.BackendConfig{}

	options := s.buildBootstrapStorageOptions("files.example.com")
	for _, option := range options {
		if option["id"] == "hot-eu" {
			if option["is_default"] != true {
				t.Fatalf("eu option is_default = %v, want true", option["is_default"])
			}
			return
		}
	}

	t.Fatalf("expected hot-eu option in %v", options)
}

func TestBuildBootstrapStorageOptionsWildcardRoutingOverridesServerRegion(t *testing.T) {
	s := createTestServer()
	s.config.Server.Region = "eu"
	s.config.Storage.DefaultClass = "hot-usa"
	s.config.Storage.EndpointRegions = map[string]string{
		"*.example.com": "usa",
		"*":             "eu",
	}
	s.config.Storage.RegionClasses = map[string]config.RegionClassConfig{
		"usa": {Hot: "hot-usa"},
		"eu":  {Hot: "hot-eu"},
	}
	s.config.Storage.Classes = map[string]config.StorageClassConfig{
		"hot-usa": {},
		"hot-eu":  {},
	}
	s.config.Storage.Backends = map[string]config.BackendConfig{}

	options := s.buildBootstrapStorageOptions("files.example.com")
	for _, option := range options {
		if option["id"] == "hot-usa" {
			if option["is_default"] != true {
				t.Fatalf("usa option is_default = %v, want true", option["is_default"])
			}
			return
		}
	}

	t.Fatalf("expected hot-usa option in %v", options)
}

func TestBuildOrgBootstrapPageOptionsIncludesAccountsOrgManagementURL(t *testing.T) {
	s := createTestServer()
	s.config.Accounts.OrgUserManagementURL = "https://accounts.example.com/orgs/{org_id}/users/"

	pageOptions := s.buildOrgBootstrapPageOptions("org-123", bootstrapOrgData{Loaded: true, Name: "Test Org"})

	if pageOptions["accountsOrgUserManagementURL"] != "https://accounts.example.com/orgs/org-123/users/" {
		t.Fatalf("accountsOrgUserManagementURL = %v, want %q", pageOptions["accountsOrgUserManagementURL"], "https://accounts.example.com/orgs/org-123/users/")
	}
}

func TestResolveBootstrapDefaultStorageClassUsesDeterministicSortedFallback(t *testing.T) {
	s := createTestServer()
	s.config.Storage.DefaultClass = "missing"
	s.config.Storage.Classes = map[string]config.StorageClassConfig{
		"hot-zeta":  {},
		"hot-alpha": {},
	}
	s.config.Storage.Backends = map[string]config.BackendConfig{}

	if got := s.resolveBootstrapDefaultStorageClass("unknown.example.com"); got != "hot-alpha" {
		t.Fatalf("resolveBootstrapDefaultStorageClass = %q, want %q", got, "hot-alpha")
	}
}

func TestBuildAppBootstrapPageOptionsIncludesOrgStoragePolicy(t *testing.T) {
	s := createTestServer()
	identity := bootstrapIdentity{UserID: "user-1", OrgID: "org-1", Role: "user", Email: "user@example.com"}
	userData := bootstrapUserData{Email: "user@example.com", Name: "Test User", Role: "user"}
	orgData := bootstrapOrgData{StorageConfig: map[string]string{"data_residency": "strict", "default_region": "usa"}}

	pageOptions := s.buildAppBootstrapPageOptions(identity, userData, orgData)
	policy, ok := pageOptions["orgStoragePolicy"].(map[string]string)
	if !ok {
		t.Fatalf("orgStoragePolicy has unexpected type: %T", pageOptions["orgStoragePolicy"])
	}
	if policy["data_residency"] != "strict" {
		t.Fatalf("data_residency = %q, want %q", policy["data_residency"], "strict")
	}
	if policy["default_region"] != "usa" {
		t.Fatalf("default_region = %q, want %q", policy["default_region"], "usa")
	}
}
