package v2

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestOrgAdminRequireOrgAccess_Unauthenticated(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := &OrgAdminHandler{}
	r := gin.New()

	r.GET("/org/:org_id/admin/devices/", h.ListOrgDevices)
	r.DELETE("/org/:org_id/admin/devices/", h.UnlinkOrgDevice)
	r.GET("/org/:org_id/admin/devices-errors/", h.ListOrgDeviceErrors)
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
