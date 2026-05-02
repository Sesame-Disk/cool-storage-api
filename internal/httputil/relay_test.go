package httputil

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestGetRoutingHostnamePrefersRequestHostOverServerURL(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v2.1/bootstrap/", nil)
	c.Request.Host = "eu.example.com"

	if got := GetRoutingHostname(c, "https://files.example.com"); got != "eu.example.com" {
		t.Fatalf("GetRoutingHostname = %q, want %q", got, "eu.example.com")
	}
}

func TestGetRoutingHostnamePrefersForwardedHost(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v2.1/bootstrap/", nil)
	c.Request.Host = "internal.example.local"
	c.Request.Header.Set("X-Forwarded-Host", "us.example.com")

	if got := GetRoutingHostname(c, "https://files.example.com"); got != "us.example.com" {
		t.Fatalf("GetRoutingHostname = %q, want %q", got, "us.example.com")
	}
}

func TestGetEffectiveHostnameStillPrefersServerURL(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v2.1/bootstrap/", nil)
	c.Request.Host = "eu.example.com"

	if got := GetEffectiveHostname(c, "https://files.example.com"); got != "files.example.com" {
		t.Fatalf("GetEffectiveHostname = %q, want %q", got, "files.example.com")
	}
}

func TestGetBrowserURL(t *testing.T) {
	tests := []struct {
		name          string
		host          string
		xProto        string
		xHost         string
		configuredURL string
		want          string
	}{
		{
			name:          "configured URL takes priority over different host",
			host:          "localhost:3000",
			xProto:        "https",
			configuredURL: "http://localhost:8080",
			want:          "http://localhost:8080",
		},
		{
			name:          "configured http URL upgrades to https for same host",
			host:          "sfs.nihaoshares.com",
			xProto:        "https",
			configuredURL: "http://sfs.nihaoshares.com",
			want:          "https://sfs.nihaoshares.com",
		},
		{
			name:          "configured http URL with same host and port upgrades to https",
			host:          "sfs.nihaoshares.com:443",
			xProto:        "https",
			configuredURL: "http://sfs.nihaoshares.com:443",
			want:          "https://sfs.nihaoshares.com:443",
		},
		{
			name:          "configured URL keeps alternate host",
			host:          "sfs.nihaoshares.com",
			xProto:        "https",
			configuredURL: "http://sesamefs.internal",
			want:          "http://sesamefs.internal",
		},
		{
			name:          "auto-detect with forwarded host and proto",
			host:          "internal:8080",
			xHost:         "files.example.com",
			xProto:        "https",
			configuredURL: "",
			want:          "https://files.example.com",
		},
		{
			name:          "auto-detect without proxy headers",
			host:          "localhost:3000",
			configuredURL: "",
			want:          "http://localhost:3000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/test", nil)
			if tt.host != "" {
				c.Request.Host = tt.host
			}
			if tt.xHost != "" {
				c.Request.Header.Set("X-Forwarded-Host", tt.xHost)
			}
			if tt.xProto != "" {
				c.Request.Header.Set("X-Forwarded-Proto", tt.xProto)
			}

			got := GetBrowserURL(c, tt.configuredURL)
			if got != tt.want {
				t.Fatalf("GetBrowserURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetRelayPortFromRequest(t *testing.T) {
	tests := []struct {
		name      string
		serverURL string
		host      string
		xHost     string
		xProto    string
		want      string
	}{
		{
			name:      "server url explicit port wins",
			serverURL: "https://files.example.com:8443",
			host:      "internal:8080",
			want:      "8443",
		},
		{
			name:   "forwarded host port wins over internal host",
			host:   "frontend:80",
			xHost:  "files.example.com:9443",
			xProto: "https",
			want:   "9443",
		},
		{
			name: "request host port used when no forwarded host",
			host: "localhost:3000",
			want: "3000",
		},
		{
			name:   "https proto defaults to 443",
			host:   "files.example.com",
			xProto: "https",
			want:   "443",
		},
		{
			name: "http defaults to 80",
			host: "files.example.com",
			want: "80",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/test", nil)
			c.Request.Host = tt.host
			if tt.xHost != "" {
				c.Request.Header.Set("X-Forwarded-Host", tt.xHost)
			}
			if tt.xProto != "" {
				c.Request.Header.Set("X-Forwarded-Proto", tt.xProto)
			}

			if got := GetRelayPortFromRequest(c, tt.serverURL); got != tt.want {
				t.Fatalf("GetRelayPortFromRequest() = %q, want %q", got, tt.want)
			}
		})
	}
}
