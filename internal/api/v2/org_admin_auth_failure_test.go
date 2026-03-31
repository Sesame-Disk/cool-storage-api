package v2

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/gin-gonic/gin"
)

// fixedRoleGetter is a test double for orgRoleGetter that returns a
// predetermined role for one specific org and "not found" for all others.
type fixedRoleGetter struct {
	orgID string
	role  middleware.OrganizationRole
}

func (m *fixedRoleGetter) GetUserOrgRole(orgID, _ string) (middleware.OrganizationRole, error) {
	if orgID == m.orgID {
		return m.role, nil
	}
	return middleware.RoleGuest, fmt.Errorf("user not found in org")
}

// injectIdentity returns a gin middleware that sets org_id and user_id in the
// request context, simulating a successfully authenticated session.
func injectIdentity(orgID, userID string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("org_id", orgID)
		c.Set("user_id", userID)
		c.Next()
	}
}

func TestOrgAdminRequireOrgAccess_Unauthenticated(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := &OrgAdminHandler{}
	r := gin.New()

	r.GET("/org/:org_id/admin/devices/", h.ListOrgDevices)
	r.DELETE("/org/:org_id/admin/devices/", h.UnlinkOrgDevice)
	r.GET("/org/:org_id/admin/devices-errors/", h.ListOrgDeviceErrors)
	r.DELETE("/org/:org_id/admin/devices-errors/", h.ClearOrgDeviceErrors)
	r.DELETE("/org/:org_id/admin/trash-libraries/", h.CleanOrgTrashLibraries)
	r.DELETE("/org/:org_id/admin/trash-libraries/:rid/", h.DeleteOrgTrashLibrary)
	r.DELETE("/org/:org_id/admin/groups/:gid/group-owned-libraries/:rid/", h.DeleteOrgGroupOwnedLibrary)
	r.GET("/org/:org_id/admin/saml-config/", h.GetOrgSAMLConfig)
	r.PUT("/org/:org_id/admin/saml-config/", h.UpdateOrgSAMLConfig)
	r.PUT("/org/:org_id/admin/verify-domain/", h.VerifyOrgDomain)

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "list devices unauthenticated", method: http.MethodGet, path: "/org/00000000-0000-0000-0000-000000000001/admin/devices/"},
		{name: "unlink device unauthenticated", method: http.MethodDelete, path: "/org/00000000-0000-0000-0000-000000000001/admin/devices/"},
		{name: "list device errors unauthenticated", method: http.MethodGet, path: "/org/00000000-0000-0000-0000-000000000001/admin/devices-errors/"},
		{name: "clear device errors unauthenticated", method: http.MethodDelete, path: "/org/00000000-0000-0000-0000-000000000001/admin/devices-errors/"},
		{name: "clean trash unauthenticated", method: http.MethodDelete, path: "/org/00000000-0000-0000-0000-000000000001/admin/trash-libraries/"},
		{name: "delete trash library unauthenticated", method: http.MethodDelete, path: "/org/00000000-0000-0000-0000-000000000001/admin/trash-libraries/11111111-1111-1111-1111-111111111111/"},
		{name: "delete group-owned library unauthenticated", method: http.MethodDelete, path: "/org/00000000-0000-0000-0000-000000000001/admin/groups/22222222-2222-2222-2222-222222222222/group-owned-libraries/11111111-1111-1111-1111-111111111111/"},
		{name: "get saml config unauthenticated", method: http.MethodGet, path: "/org/00000000-0000-0000-0000-000000000001/admin/saml-config/"},
		{name: "update saml config unauthenticated", method: http.MethodPut, path: "/org/00000000-0000-0000-0000-000000000001/admin/saml-config/"},
		{name: "verify domain unauthenticated", method: http.MethodPut, path: "/org/00000000-0000-0000-0000-000000000001/admin/verify-domain/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(tt.method, tt.path, nil)
			if err != nil {
				t.Fatalf("failed to build request: %v", err)
			}

			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusUnauthorized, w.Body.String())
			}

			var payload map[string]interface{}
			if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
				t.Fatalf("response is not valid JSON: %v", err)
			}
			if payload["error"] != "authentication required" {
				t.Fatalf("error = %v, want %q", payload["error"], "authentication required")
			}
		})
	}
}

// TestOrgAdminRequireOrgAccess_CrossOrgForbidden verifies that an admin of
// org A is rejected with 403 when targeting org B's resources.
//
// This exercises the org-mismatch branch of requireOrgAccess without a real
// database: fixedRoleGetter returns RoleAdmin for orgA and an error for any
// other org, so the handler never reaches data-access code.
func TestOrgAdminRequireOrgAccess_CrossOrgForbidden(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const orgA = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	const orgB = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	const adminUser = "user-admin-a"

	h := &OrgAdminHandler{
		permMiddleware: &fixedRoleGetter{orgID: orgA, role: middleware.RoleAdmin},
	}

	tests := []struct {
		name   string
		method string
		path   string
		setup  func(r *gin.Engine)
	}{
		{
			name:   "list users org B rejected for admin of org A",
			method: http.MethodGet,
			path:   "/org/" + orgB + "/admin/users/",
			setup: func(r *gin.Engine) {
				r.GET("/org/:org_id/admin/users/", h.ListOrgUsers)
			},
		},
		{
			name:   "list repos org B rejected for admin of org A",
			method: http.MethodGet,
			path:   "/org/" + orgB + "/admin/repos/",
			setup: func(r *gin.Engine) {
				r.GET("/org/:org_id/admin/repos/", h.ListOrgRepos)
			},
		},
		{
			name:   "delete repo org B rejected for admin of org A",
			method: http.MethodDelete,
			path:   "/org/" + orgB + "/admin/repos/repo-b/",
			setup: func(r *gin.Engine) {
				r.DELETE("/org/:org_id/admin/repos/:rid/", h.DeleteOrgRepo)
			},
		},
		{
			name:   "get saml config org B rejected for admin of org A",
			method: http.MethodGet,
			path:   "/org/" + orgB + "/admin/saml-config/",
			setup: func(r *gin.Engine) {
				r.GET("/org/:org_id/admin/saml-config/", h.GetOrgSAMLConfig)
			},
		},
		{
			// ListOrgLinks uses c.GetString("org_id") from the auth context (always
			// the caller's own org), so cross-org cannot be attempted via the URL.
			// ClearOrgDeviceErrors uses requireOrgAccess with c.Param("org_id") and
			// is a representative destructive link-adjacent operation.
			name:   "clear device errors org B rejected for admin of org A",
			method: http.MethodDelete,
			path:   "/org/" + orgB + "/admin/devices-errors/",
			setup: func(r *gin.Engine) {
				r.DELETE("/org/:org_id/admin/devices-errors/", h.ClearOrgDeviceErrors)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := gin.New()
			r.Use(injectIdentity(orgA, adminUser))
			tt.setup(r)

			req, err := http.NewRequest(tt.method, tt.path, nil)
			if err != nil {
				t.Fatalf("failed to build request: %v", err)
			}

			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d (cross-org access must be rejected), body=%s",
					w.Code, http.StatusForbidden, w.Body.String())
			}

			var payload map[string]interface{}
			if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
				t.Fatalf("response is not valid JSON: %v", err)
			}
			if payload["error"] != "insufficient permissions" {
				t.Fatalf("error = %v, want %q", payload["error"], "insufficient permissions")
			}
		})
	}
}

func TestOrgAdminListDeviceErrors_ResponseShape(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const orgID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	const adminUser = "user-admin-a"

	h := &OrgAdminHandler{
		permMiddleware: &fixedRoleGetter{orgID: orgID, role: middleware.RoleAdmin},
	}

	r := gin.New()
	r.Use(injectIdentity(orgID, adminUser))
	r.GET("/org/:org_id/admin/devices-errors/", h.ListOrgDeviceErrors)

	req, err := http.NewRequest(http.MethodGet, "/org/"+orgID+"/admin/devices-errors/", nil)
	if err != nil {
		t.Fatalf("failed to build request: %v", err)
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}

	if _, ok := payload["device_errors"]; !ok {
		t.Fatalf("response missing device_errors key: %v", payload)
	}
	if _, ok := payload["devices"]; ok {
		t.Fatalf("response must not expose legacy devices key: %v", payload)
	}
	if _, ok := payload["page_info"]; !ok {
		t.Fatalf("response missing page_info key: %v", payload)
	}
	if _, ok := payload["device_errors"].([]interface{}); !ok {
		t.Fatalf("device_errors = %T, want array", payload["device_errors"])
	}
}
