package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/gin-gonic/gin"
)

// TestSupportedLocalesMatchCanonicalJSON guards the single source of truth shared
// with the frontend: internal/api supportedLocales must mirror
// frontend/src/utils/supported-locales.json exactly (codes, names and order). If
// they drift, the selector and the i18n whitelist disagree about what can load.
func TestSupportedLocalesMatchCanonicalJSON(t *testing.T) {
	path := filepath.Join("..", "..", "frontend", "src", "utils", "supported-locales.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("canonical locale file not available (%v); skipping drift guard", err)
	}

	var canonical []supportedLocale
	if err := json.Unmarshal(raw, &canonical); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}

	if len(canonical) != len(supportedLocales) {
		t.Fatalf("canonical JSON has %d locales, backend has %d — they must match", len(canonical), len(supportedLocales))
	}
	for i, want := range canonical {
		got := supportedLocales[i]
		if got.Code != want.Code || got.Name != want.Name {
			t.Fatalf("locale[%d] = {%q,%q}, canonical JSON has {%q,%q} — sync internal/api/bootstrap.go with supported-locales.json",
				i, got.Code, got.Name, want.Code, want.Name)
		}
	}
}

func TestBuildAppBootstrapPageOptionsIncludesSettingsBasics(t *testing.T) {
	s := createTestServer()
	s.config.Accounts.DeleteAccountURL = "https://accounts.example.com/accounts/delete/"
	identity := bootstrapIdentity{UserID: "user-1", OrgID: "org-1", Role: "user", Email: "user@example.com"}
	userData := bootstrapUserData{Email: "user@example.com", Name: "Test User", Role: "user"}

	pageOptions := s.buildAppBootstrapPageOptions(identity, userData, bootstrapOrgData{}, "en")

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

// TestBootstrapLangListOffersAllSupportedLocales guards the original bug: the
// selector must offer more than English. It collapsed to a single hard-coded
// English entry before, so a regression here is exactly what we want to catch.
func TestBootstrapLangListOffersAllSupportedLocales(t *testing.T) {
	s := createTestServer()
	identity := bootstrapIdentity{UserID: "user-1", OrgID: "org-1", Role: "user", Email: "user@example.com"}

	pageOptions := s.buildAppBootstrapPageOptions(identity, bootstrapUserData{}, bootstrapOrgData{}, "en")

	langList, ok := pageOptions["langList"].([]gin.H)
	if !ok {
		t.Fatalf("langList has unexpected type: %T", pageOptions["langList"])
	}
	if len(langList) <= 1 {
		t.Fatalf("langList offers %d locale(s), want more than 1 (English-only regression)", len(langList))
	}
	if len(langList) != len(supportedLocales) {
		t.Fatalf("langList has %d entries, want %d (must mirror supportedLocales)", len(langList), len(supportedLocales))
	}

	want := map[string]string{"en": "English", "es": "Español", "zh-CN": "中文"}
	got := make(map[string]string, len(langList))
	for _, item := range langList {
		code, _ := item["langCode"].(string)
		name, _ := item["langName"].(string)
		got[code] = name
		if code == "" || name == "" || name == code {
			t.Fatalf("langList entry %+v has empty or unlabeled code/name", item)
		}
	}
	for code, name := range want {
		if got[code] != name {
			t.Fatalf("langList[%q] = %q, want %q", code, got[code], name)
		}
	}
}

// TestHandleLanguageChange covers the selector backend end-to-end: a supported
// locale persists the cookie and redirects back to the originating page; an
// unsupported one writes no cookie but still redirects so the user isn't stranded.
func TestHandleLanguageChange(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := createTestServer()

	t.Run("supported locale sets cookie and redirects to referer", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/i18n/?lang=fr", nil)
		c.Request.Header.Set("Referer", "https://example.com/profile/")

		s.handleLanguageChange(c)

		if w.Code != http.StatusFound {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusFound)
		}
		if loc := w.Header().Get("Location"); loc != "/profile/" {
			t.Fatalf("Location = %q, want %q", loc, "/profile/")
		}
		setCookie := w.Header().Get("Set-Cookie")
		if !strings.Contains(setCookie, localeCookieName+"=fr") {
			t.Fatalf("Set-Cookie = %q, want it to set %s=fr", setCookie, localeCookieName)
		}
	})

	t.Run("unsupported locale writes no cookie and redirects home", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/i18n/?lang=klingon", nil)

		s.handleLanguageChange(c)

		if w.Code != http.StatusFound {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusFound)
		}
		if loc := w.Header().Get("Location"); loc != "/" {
			t.Fatalf("Location = %q, want %q", loc, "/")
		}
		if setCookie := w.Header().Get("Set-Cookie"); setCookie != "" {
			t.Fatalf("Set-Cookie = %q, want no cookie for unsupported locale", setCookie)
		}
	})

	t.Run("cross-origin referer is ignored", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/i18n/?lang=es", nil)
		c.Request.Host = "example.com"
		c.Request.Header.Set("Referer", "https://evil.example.net/phish/")

		s.handleLanguageChange(c)

		if loc := w.Header().Get("Location"); loc != "/" {
			t.Fatalf("Location = %q, want %q (cross-origin referer must not be honored)", loc, "/")
		}
	})

	t.Run("protocol-relative same-origin referer is rejected", func(t *testing.T) {
		// "https://example.com//evil.example/phish" parses with Host "example.com"
		// (same origin) but a path of "//evil.example/phish" — returning that as
		// Location would be a scheme-relative open redirect.
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/i18n/?lang=es", nil)
		c.Request.Host = "example.com"
		c.Request.Header.Set("Referer", "https://example.com//evil.example/phish")

		s.handleLanguageChange(c)

		if loc := w.Header().Get("Location"); loc != "/" {
			t.Fatalf("Location = %q, want %q (protocol-relative path must not be honored)", loc, "/")
		}
	})
}

// TestResolveBootstrapLocale verifies the cookie drives the resolved locale and
// that unknown/absent values fall back to English.
func TestResolveBootstrapLocale(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name   string
		cookie string
		want   string
	}{
		{"valid cookie", "es-MX", "es-MX"},
		{"unsupported cookie", "klingon", "en"},
		{"no cookie", "", "en"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/api/v2.1/bootstrap/", nil)
			if tc.cookie != "" {
				c.Request.AddCookie(&http.Cookie{Name: localeCookieName, Value: tc.cookie})
			}
			if got := resolveBootstrapLocale(c); got != tc.want {
				t.Fatalf("resolveBootstrapLocale() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestBuildAppBootstrapPageOptionsReflectsLocale verifies the active locale flows
// into langCode/currentLang so the selector shows the user's choice after a reload.
func TestBuildAppBootstrapPageOptionsReflectsLocale(t *testing.T) {
	s := createTestServer()
	identity := bootstrapIdentity{UserID: "user-1", OrgID: "org-1", Role: "user", Email: "user@example.com"}

	cases := []struct {
		locale   string
		wantCode string
		wantName string
	}{
		{"fr", "fr", "Français"},
		{"zh-CN", "zh-CN", "中文"},
		{"", "en", "English"},        // empty falls back to English
		{"klingon", "en", "English"}, // unsupported falls back to English
	}
	for _, tc := range cases {
		pageOptions := s.buildAppBootstrapPageOptions(identity, bootstrapUserData{}, bootstrapOrgData{}, tc.locale)
		if pageOptions["langCode"] != tc.wantCode {
			t.Fatalf("locale %q: langCode = %v, want %q", tc.locale, pageOptions["langCode"], tc.wantCode)
		}
		currentLang, ok := pageOptions["currentLang"].(gin.H)
		if !ok {
			t.Fatalf("locale %q: currentLang has unexpected type: %T", tc.locale, pageOptions["currentLang"])
		}
		if currentLang["langCode"] != tc.wantCode {
			t.Fatalf("locale %q: currentLang.langCode = %v, want %q", tc.locale, currentLang["langCode"], tc.wantCode)
		}
		if currentLang["langName"] != tc.wantName {
			t.Fatalf("locale %q: currentLang.langName = %v, want %q", tc.locale, currentLang["langName"], tc.wantName)
		}
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
		"hot-usa": {Bucket: "usa"},
		"hot-eu":  {Bucket: "eu"},
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

func TestBuildBootstrapStorageOptionsUsesConfiguredClassLabels(t *testing.T) {
	s := createTestServer()
	s.config.Storage.DefaultClass = "hot-na"
	s.config.Storage.EndpointRegions = map[string]string{
		"na.example.com": "na",
	}
	s.config.Storage.RegionClasses = map[string]config.RegionClassConfig{
		"na": {Hot: "hot-na"},
		"eu": {Hot: "hot-eu"},
	}
	s.config.Storage.Classes = map[string]config.StorageClassConfig{
		"hot-na": {Label: "North America", Bucket: "na"},
		"hot-eu": {Label: "Europe", Bucket: "eu"},
	}
	s.config.Storage.Backends = map[string]config.BackendConfig{}

	options := s.buildBootstrapStorageOptions("na.example.com")
	for _, option := range options {
		if option["id"] == "hot-na" {
			if option["name"] != "North America" {
				t.Fatalf("na option name = %v, want %q", option["name"], "North America")
			}
			if option["is_default"] != true {
				t.Fatalf("na option is_default = %v, want true", option["is_default"])
			}
			return
		}
	}

	t.Fatalf("expected hot-na option in %v", options)
}

func TestBuildBootstrapStorageOptionsUsesConfiguredClassLabelsWithoutRegionClasses(t *testing.T) {
	s := createTestServer()
	s.config.Storage.DefaultClass = "archive-hot"
	s.config.Storage.EndpointRegions = map[string]string{}
	s.config.Storage.RegionClasses = map[string]config.RegionClassConfig{}
	s.config.Storage.Classes = map[string]config.StorageClassConfig{
		"archive-hot": {Label: "Archive Storage", Bucket: "archive"},
		"hot-zeta":    {Bucket: "zeta"},
	}
	s.config.Storage.Backends = map[string]config.BackendConfig{}

	options := s.buildBootstrapStorageOptions("unknown.example.com")
	for _, option := range options {
		if option["id"] == "archive-hot" {
			if option["name"] != "Archive Storage" {
				t.Fatalf("archive-hot option name = %v, want %q", option["name"], "Archive Storage")
			}
			return
		}
	}

	t.Fatalf("expected archive-hot option in %v", options)
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
		"hot-usa": {Bucket: "usa"},
		"hot-eu":  {Bucket: "eu"},
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
		"hot-usa": {Bucket: "usa"},
		"hot-eu":  {Bucket: "eu"},
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
		"hot-zeta":  {Bucket: "zeta"},
		"hot-alpha": {Bucket: "alpha"},
	}
	s.config.Storage.Backends = map[string]config.BackendConfig{}

	if got := s.resolveBootstrapDefaultStorageClass("unknown.example.com"); got != "hot-alpha" {
		t.Fatalf("resolveBootstrapDefaultStorageClass = %q, want %q", got, "hot-alpha")
	}
}

func TestBuildBootstrapStorageOptionsUsesRegisteredBackends(t *testing.T) {
	s := createTestServer()
	s.config.Storage.DefaultClass = "hot-broken"
	s.config.Storage.Classes = map[string]config.StorageClassConfig{
		"hot-broken": {Bucket: "broken"},
		"hot-good":   {Bucket: "good"},
	}
	s.config.Storage.Backends = map[string]config.BackendConfig{}
	s.storageManager = storage.NewManager()
	s.storageManager.RegisterBackend("hot-good", &storage.S3Store{}, "")

	options := s.buildBootstrapStorageOptions("unknown.example.com")
	for _, option := range options {
		if option["id"] == "hot-broken" {
			t.Fatalf("bootstrap advertised an unregistered storage class: %v", options)
		}
	}
	if len(options) != 1 || options[0]["id"] != "hot-good" {
		t.Fatalf("bootstrap options = %v, want only hot-good", options)
	}
}

func TestBuildAppBootstrapPageOptionsIncludesOrgStoragePolicy(t *testing.T) {
	s := createTestServer()
	identity := bootstrapIdentity{UserID: "user-1", OrgID: "org-1", Role: "user", Email: "user@example.com"}
	userData := bootstrapUserData{Email: "user@example.com", Name: "Test User", Role: "user"}
	orgData := bootstrapOrgData{StorageConfig: map[string]string{"data_residency": "strict", "default_region": "usa"}}

	pageOptions := s.buildAppBootstrapPageOptions(identity, userData, orgData, "en")
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
