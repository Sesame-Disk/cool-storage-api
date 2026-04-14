package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSecurityHeadersSetsBaselineHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(SecurityHeaders())
	router.GET("/ping", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/ping", nil)
	router.ServeHTTP(recorder, request)

	if got := recorder.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want %q", got, "nosniff")
	}
	if got := recorder.Header().Get("Referrer-Policy"); got != "strict-origin-when-cross-origin" {
		t.Fatalf("Referrer-Policy = %q, want %q", got, "strict-origin-when-cross-origin")
	}
	if got := recorder.Header().Get("Strict-Transport-Security"); got != "max-age=31536000; includeSubDomains" {
		t.Fatalf("Strict-Transport-Security = %q, want %q", got, "max-age=31536000; includeSubDomains")
	}
	if got := recorder.Header().Get("Content-Security-Policy"); got != defaultCSP {
		t.Fatalf("Content-Security-Policy = %q, want %q", got, defaultCSP)
	}
	if got := recorder.Header().Get("Permissions-Policy"); got != defaultPermissionsPolicy {
		t.Fatalf("Permissions-Policy = %q, want %q", got, defaultPermissionsPolicy)
	}
}
