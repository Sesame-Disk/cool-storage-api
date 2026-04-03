package api

import (
	"testing"

	"github.com/gin-gonic/gin"
)

func TestBuildAppBootstrapPageOptionsIncludesSettingsBasics(t *testing.T) {
	s := createTestServer()
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
	if enabled, ok := pageOptions["enableUserSetContactEmail"].(bool); !ok || !enabled {
		t.Fatalf("enableUserSetContactEmail = %v, want true", pageOptions["enableUserSetContactEmail"])
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
	if backendRoutes["passwordChange"] != "accounts/password/change/" {
		t.Fatalf("backendRoutes.passwordChange = %v, want %q", backendRoutes["passwordChange"], "accounts/password/change/")
	}
}
