package httputil

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestGetRoutingHostnamePrefersRequestHostOverServerURL(t *testing.T) {
	t.Setenv("SERVER_URL", "https://files.example.com")

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v2.1/bootstrap/", nil)
	c.Request.Host = "eu.example.com"

	if got := GetRoutingHostname(c); got != "eu.example.com" {
		t.Fatalf("GetRoutingHostname = %q, want %q", got, "eu.example.com")
	}
}

func TestGetRoutingHostnamePrefersForwardedHost(t *testing.T) {
	t.Setenv("SERVER_URL", "https://files.example.com")

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v2.1/bootstrap/", nil)
	c.Request.Host = "internal.example.local"
	c.Request.Header.Set("X-Forwarded-Host", "us.example.com")

	if got := GetRoutingHostname(c); got != "us.example.com" {
		t.Fatalf("GetRoutingHostname = %q, want %q", got, "us.example.com")
	}
}

func TestGetEffectiveHostnameStillPrefersServerURL(t *testing.T) {
	t.Setenv("SERVER_URL", "https://files.example.com")

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v2.1/bootstrap/", nil)
	c.Request.Host = "eu.example.com"

	if got := GetEffectiveHostname(c); got != "files.example.com" {
		t.Fatalf("GetEffectiveHostname = %q, want %q", got, "files.example.com")
	}
}
