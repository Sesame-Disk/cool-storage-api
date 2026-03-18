//go:build integration

package integration

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolveIntegrationBaseURL_HealthOK(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	t.Setenv("SESAMEFS_URL", ts.URL)

	resolved, err := resolveIntegrationBaseURL(ts.URL)
	if err != nil {
		t.Fatalf("resolveIntegrationBaseURL returned error: %v", err)
	}
	if resolved != ts.URL {
		t.Fatalf("resolved baseURL = %q, want %q", resolved, ts.URL)
	}
}

func TestResolveIntegrationBaseURL_HealthNon200(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	t.Setenv("SESAMEFS_URL", ts.URL)

	if _, err := resolveIntegrationBaseURL(ts.URL); err == nil {
		t.Fatal("resolveIntegrationBaseURL should fail when /health is non-200")
	}
}

func TestVerifyIntegrationAuth_Unauthorized(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api2/account/info/" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	if err := verifyIntegrationAuth(ts.URL, "dev-token-admin"); err == nil {
		t.Fatal("verifyIntegrationAuth should fail for 401 response")
	}
}

func TestVerifyIntegrationAuth_OK(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api2/account/info/" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	if err := verifyIntegrationAuth(ts.URL, "dev-token-admin"); err != nil {
		t.Fatalf("verifyIntegrationAuth returned error: %v", err)
	}
}
