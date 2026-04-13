package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/golang-jwt/jwt/v5"
)

// mockOIDCProvider creates a mock OIDC provider for testing.
// It serves discovery, JWKS, token, userinfo, and logout endpoints.
func mockOIDCProvider(t *testing.T) *httptest.Server {
	var server *httptest.Server
	mux := http.NewServeMux()

	// Discovery endpoint (uses server.URL via closure)
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		discovery := OIDCDiscovery{
			Issuer:                server.URL,
			AuthorizationEndpoint: server.URL + "/authorize",
			TokenEndpoint:         server.URL + "/token",
			UserInfoEndpoint:      server.URL + "/userinfo",
			JwksURI:               server.URL + "/.well-known/jwks.json",
			EndSessionEndpoint:    server.URL + "/logout",
			ScopesSupported:       []string{"openid", "profile", "email"},
			ClaimsSupported:       []string{"sub", "email", "name"},
		}
		json.NewEncoder(w).Encode(discovery)
	})

	// JWKS endpoint
	pub := &testKeyPair.PublicKey
	mux.HandleFunc("/.well-known/jwks.json", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"keys": []map[string]string{{
				"kty": "RSA", "kid": testKid, "use": "sig", "alg": "RS256",
				"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
				"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
			}},
		})
	})

	// Token endpoint
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Check for authorization code
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}

		code := r.FormValue("code")
		if code == "" || code == "invalid_code" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}

		// Return mock tokens
		// Note: This is a simplified mock - real JWT would need proper signing
		resp := TokenResponse{
			AccessToken: "mock-access-token",
			TokenType:   "Bearer",
			ExpiresIn:   3600,
			IDToken:     createMockIDToken(t),
		}
		json.NewEncoder(w).Encode(resp)
	})

	// UserInfo endpoint
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		userInfo := UserInfo{
			Subject:       "user-12345",
			Email:         "test@example.com",
			EmailVerified: true,
			Name:          "Test User",
		}
		json.NewEncoder(w).Encode(userInfo)
	})

	// Logout endpoint
	mux.HandleFunc("/logout", func(w http.ResponseWriter, r *http.Request) {
		redirectURI := r.URL.Query().Get("post_logout_redirect_uri")
		if redirectURI != "" {
			http.Redirect(w, r, redirectURI, http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	server = httptest.NewServer(mux)
	return server
}

// createMockIDToken creates a properly RSA-signed mock ID token for testing.
func createMockIDToken(t *testing.T) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss":   "http://localhost",
		"sub":   "user-12345",
		"aud":   "test-client",
		"exp":   9999999999,
		"iat":   1600000000,
		"email": "test@example.com",
		"name":  "Test User",
	})
	token.Header["kid"] = testKid
	signed, err := token.SignedString(testKeyPair)
	if err != nil {
		t.Fatalf("failed to sign mock ID token: %v", err)
	}
	return signed
}

// TestOIDCClient_IsEnabled tests the IsEnabled method
func TestOIDCClient_IsEnabled(t *testing.T) {
	tests := []struct {
		name   string
		config *config.OIDCConfig
		want   bool
	}{
		{
			name: "enabled with all required fields",
			config: &config.OIDCConfig{
				Enabled:  true,
				Issuer:   "https://example.com",
				ClientID: "test-client",
			},
			want: true,
		},
		{
			name: "disabled explicitly",
			config: &config.OIDCConfig{
				Enabled:  false,
				Issuer:   "https://example.com",
				ClientID: "test-client",
			},
			want: false,
		},
		{
			name: "missing issuer",
			config: &config.OIDCConfig{
				Enabled:  true,
				Issuer:   "",
				ClientID: "test-client",
			},
			want: false,
		},
		{
			name: "missing client ID",
			config: &config.OIDCConfig{
				Enabled:  true,
				Issuer:   "https://example.com",
				ClientID: "",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &OIDCClient{allowPrivateIPs: true, config: tt.config}
			if got := c.IsEnabled(); got != tt.want {
				t.Errorf("IsEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestOIDCClient_GetDiscovery tests OIDC discovery document fetching
func TestOIDCClient_GetDiscovery(t *testing.T) {
	server := mockOIDCProvider(t)
	defer server.Close()

	cfg := &config.OIDCConfig{
		Enabled:  true,
		Issuer:   server.URL,
		ClientID: "test-client",
	}

	client := &OIDCClient{allowPrivateIPs: true,
		config: cfg,
		states: make(map[string]*AuthState),
	}

	t.Run("fetch discovery document", func(t *testing.T) {
		discovery, err := client.GetDiscovery(context.Background())
		if err != nil {
			t.Fatalf("GetDiscovery() error = %v", err)
		}

		if discovery.AuthorizationEndpoint != server.URL+"/authorize" {
			t.Errorf("AuthorizationEndpoint = %v, want %v", discovery.AuthorizationEndpoint, server.URL+"/authorize")
		}
		if discovery.TokenEndpoint != server.URL+"/token" {
			t.Errorf("TokenEndpoint = %v, want %v", discovery.TokenEndpoint, server.URL+"/token")
		}
		if discovery.EndSessionEndpoint != server.URL+"/logout" {
			t.Errorf("EndSessionEndpoint = %v, want %v", discovery.EndSessionEndpoint, server.URL+"/logout")
		}
	})

	t.Run("cache discovery document", func(t *testing.T) {
		// First call
		d1, _ := client.GetDiscovery(context.Background())

		// Should return cached version
		d2, _ := client.GetDiscovery(context.Background())

		if d1 != d2 {
			t.Error("Second call should return cached discovery")
		}
	})
}

// TestOIDCClient_GetDiscovery_InvalidIssuer tests error handling for invalid issuer
func TestOIDCClient_GetDiscovery_InvalidIssuer(t *testing.T) {
	cfg := &config.OIDCConfig{
		Enabled:  true,
		Issuer:   "http://invalid-issuer-that-does-not-exist.local",
		ClientID: "test-client",
	}

	client := &OIDCClient{allowPrivateIPs: true,
		config: cfg,
		states: make(map[string]*AuthState),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := client.GetDiscovery(ctx)
	if err == nil {
		t.Error("GetDiscovery() should fail for invalid issuer")
	}
}

// TestOIDCClient_ValidateRedirectURI tests redirect URI validation
func TestOIDCClient_ValidateRedirectURI(t *testing.T) {
	tests := []struct {
		name          string
		allowedURIs   []string
		testURI       string
		shouldBeValid bool
	}{
		{
			name:          "valid URI in list",
			allowedURIs:   []string{"http://localhost:3000/sso", "https://example.com/sso"},
			testURI:       "http://localhost:3000/sso",
			shouldBeValid: true,
		},
		{
			name:          "URI not in list",
			allowedURIs:   []string{"http://localhost:3000/sso"},
			testURI:       "http://attacker.com/sso",
			shouldBeValid: false,
		},
		{
			name:          "empty allowed list rejects all",
			allowedURIs:   []string{},
			testURI:       "http://anything.com/callback",
			shouldBeValid: false,
		},
		{
			name:          "case sensitive match",
			allowedURIs:   []string{"http://localhost:3000/sso"},
			testURI:       "http://localhost:3000/SSO",
			shouldBeValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &OIDCClient{allowPrivateIPs: true,
				config: &config.OIDCConfig{
					RedirectURIs: tt.allowedURIs,
				},
			}

			got := client.isValidRedirectURI(tt.testURI)
			if got != tt.shouldBeValid {
				t.Errorf("isValidRedirectURI(%q) = %v, want %v", tt.testURI, got, tt.shouldBeValid)
			}
		})
	}
}

// TestOIDCClient_GetAuthorizationURL tests authorization URL generation
func TestOIDCClient_GetAuthorizationURL(t *testing.T) {
	server := mockOIDCProvider(t)
	defer server.Close()

	cfg := &config.OIDCConfig{
		Enabled:      true,
		Issuer:       server.URL,
		ClientID:     "test-client",
		RedirectURIs: []string{"http://localhost:3000/sso"},
		Scopes:       []string{"openid", "profile", "email"},
	}

	client := &OIDCClient{allowPrivateIPs: true,
		config: cfg,
		states: make(map[string]*AuthState),
	}

	t.Run("generate valid authorization URL", func(t *testing.T) {
		url, err := client.GetAuthorizationURL(context.Background(), "http://localhost:3000/sso", "/dashboard")
		if err != nil {
			t.Fatalf("GetAuthorizationURL() error = %v", err)
		}

		// Check URL contains required parameters
		if !strings.Contains(url, "client_id=test-client") {
			t.Error("URL should contain client_id")
		}
		if !strings.Contains(url, "response_type=code") {
			t.Error("URL should contain response_type=code")
		}
		if !strings.Contains(url, "redirect_uri=") {
			t.Error("URL should contain redirect_uri")
		}
		if !strings.Contains(url, "state=") {
			t.Error("URL should contain state")
		}
		if !strings.Contains(url, "scope=") {
			t.Error("URL should contain scope")
		}
	})

	t.Run("reject invalid redirect URI", func(t *testing.T) {
		_, err := client.GetAuthorizationURL(context.Background(), "http://attacker.com/callback", "/")
		if err == nil {
			t.Error("GetAuthorizationURL() should reject invalid redirect URI")
		}
	})

	t.Run("reject when redirect allowlist is empty", func(t *testing.T) {
		emptyListClient := &OIDCClient{allowPrivateIPs: true,
			config: &config.OIDCConfig{
				Enabled:      true,
				Issuer:       server.URL,
				ClientID:     "test-client",
				RedirectURIs: nil,
				Scopes:       []string{"openid", "profile", "email"},
			},
			states: make(map[string]*AuthState),
		}

		_, err := emptyListClient.GetAuthorizationURL(context.Background(), "http://localhost:3000/sso", "/")
		if err == nil {
			t.Error("GetAuthorizationURL() should reject empty redirect allowlist")
		}
	})

	t.Run("generate URL with PKCE", func(t *testing.T) {
		pkceClient := &OIDCClient{allowPrivateIPs: true,
			config: &config.OIDCConfig{
				Enabled:      true,
				Issuer:       server.URL,
				ClientID:     "test-client",
				RedirectURIs: []string{"http://localhost:3000/sso"},
				Scopes:       []string{"openid"},
				RequirePKCE:  true,
			},
			states: make(map[string]*AuthState),
		}

		url, err := pkceClient.GetAuthorizationURL(context.Background(), "http://localhost:3000/sso", "/")
		if err != nil {
			t.Fatalf("GetAuthorizationURL() error = %v", err)
		}

		if !strings.Contains(url, "code_challenge=") {
			t.Error("URL should contain code_challenge for PKCE")
		}
		if !strings.Contains(url, "code_challenge_method=S256") {
			t.Error("URL should contain code_challenge_method=S256")
		}
	})
}

func TestOIDCClient_ExchangeCodeRejectsInvalidRedirectURI(t *testing.T) {
	client := &OIDCClient{allowPrivateIPs: true,
		config: &config.OIDCConfig{
			RedirectURIs: []string{"http://localhost:3000/sso"},
		},
		states: make(map[string]*AuthState),
	}
	client.storeState("test-state", &AuthState{
		State:       "test-state",
		RedirectURI: "http://attacker.com/callback",
		CreatedAt:   time.Now(),
	})

	_, err := client.ExchangeCode(context.Background(), "code", "test-state", "http://attacker.com/callback")
	if err == nil {
		t.Fatal("ExchangeCode() should reject invalid redirect URI")
	}
	if _, err := client.consumeState("test-state"); err == nil {
		t.Fatal("ExchangeCode() should consume state even when redirect URI is invalid")
	}
}

// TestOIDCClient_StateManagement tests state storage and consumption
func TestOIDCClient_StateManagement(t *testing.T) {
	client := &OIDCClient{allowPrivateIPs: true,
		config: &config.OIDCConfig{},
		states: make(map[string]*AuthState),
	}

	t.Run("store and consume state", func(t *testing.T) {
		state := &AuthState{
			State:       "test-state-123",
			Nonce:       "test-nonce",
			RedirectURI: "http://localhost:3000/sso",
			CreatedAt:   time.Now(),
		}

		client.storeState("test-state-123", state)

		// Consume the state
		consumed, err := client.consumeState("test-state-123")
		if err != nil {
			t.Errorf("consumeState() error = %v", err)
		}
		if consumed.Nonce != "test-nonce" {
			t.Error("Consumed state should match stored state")
		}

		// Second consumption should fail
		_, err = client.consumeState("test-state-123")
		if err == nil {
			t.Error("Second consumeState() should fail")
		}
	})

	t.Run("expired state", func(t *testing.T) {
		expiredState := &AuthState{
			State:     "expired-state",
			CreatedAt: time.Now().Add(-15 * time.Minute), // 15 minutes old
		}
		client.storeState("expired-state", expiredState)

		_, err := client.consumeState("expired-state")
		if err == nil {
			t.Error("consumeState() should fail for expired state")
		}
	})

	t.Run("nonexistent state", func(t *testing.T) {
		_, err := client.consumeState("nonexistent-state")
		if err == nil {
			t.Error("consumeState() should fail for nonexistent state")
		}
	})
}

// TestOIDCClient_GetLogoutURL tests logout URL generation
func TestOIDCClient_GetLogoutURL(t *testing.T) {
	server := mockOIDCProvider(t)
	defer server.Close()

	cfg := &config.OIDCConfig{
		Enabled:  true,
		Issuer:   server.URL,
		ClientID: "test-client",
	}

	client := &OIDCClient{allowPrivateIPs: true,
		config: cfg,
		states: make(map[string]*AuthState),
	}

	t.Run("generate logout URL with post_logout_redirect_uri", func(t *testing.T) {
		logoutURL, err := client.GetLogoutURL(context.Background(), "", "http://localhost:3000/login/")
		if err != nil {
			t.Fatalf("GetLogoutURL() error = %v", err)
		}

		if !strings.Contains(logoutURL, "client_id=test-client") {
			t.Error("Logout URL should contain client_id")
		}
		if !strings.Contains(logoutURL, "post_logout_redirect_uri=") {
			t.Error("Logout URL should contain post_logout_redirect_uri")
		}
	})

	t.Run("generate logout URL with id_token_hint", func(t *testing.T) {
		logoutURL, err := client.GetLogoutURL(context.Background(), "mock-id-token", "http://localhost:3000/login/")
		if err != nil {
			t.Fatalf("GetLogoutURL() error = %v", err)
		}

		if !strings.Contains(logoutURL, "id_token_hint=mock-id-token") {
			t.Error("Logout URL should contain id_token_hint")
		}
	})
}

// TestOIDCClient_MapOIDCRole tests role mapping
func TestOIDCClient_MapOIDCRole(t *testing.T) {
	client := &OIDCClient{allowPrivateIPs: true,
		config: &config.OIDCConfig{
			DefaultRole: "user",
		},
	}

	tests := []struct {
		oidcRole     string
		expectedRole string
	}{
		{"superadmin", "user"},
		{"super_admin", "user"},
		{"platform_admin", "user"},
		{"Superadmin", "user"},
		{"SUPER_ADMIN", "user"},
		{"admin", "admin"},
		{"administrator", "admin"},
		{"Admin", "admin"},
		{"user", "user"},
		{"member", "user"},
		{"readonly", "readonly"},
		{"read-only", "readonly"},
		{"viewer", "readonly"},
		{"guest", "guest"},
		{"unknown-role", "user"}, // Falls back to default
	}

	for _, tt := range tests {
		t.Run(tt.oidcRole, func(t *testing.T) {
			got := client.mapOIDCRole(tt.oidcRole)
			if got != tt.expectedRole {
				t.Errorf("mapOIDCRole(%q) = %q, want %q", tt.oidcRole, got, tt.expectedRole)
			}
		})
	}
}

func TestIsPrivilegedOIDCRoleClaim(t *testing.T) {
	tests := []struct {
		role string
		want bool
	}{
		{role: "superadmin", want: true},
		{role: "super_admin", want: true},
		{role: "platform_admin", want: true},
		{role: " SuperAdmin ", want: true},
		{role: "owner", want: false},
		{role: "admin", want: false},
		{role: "unknown-role", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			if got := isPrivilegedOIDCRoleClaim(tt.role); got != tt.want {
				t.Fatalf("isPrivilegedOIDCRoleClaim(%q) = %v, want %v", tt.role, got, tt.want)
			}
		})
	}
}

func TestOIDCClient_NormalizeRoleForOrg(t *testing.T) {
	client := &OIDCClient{allowPrivateIPs: true,
		config: &config.OIDCConfig{
			PlatformOrgID: "00000000-0000-0000-0000-000000000000",
		},
	}

	tests := []struct {
		name     string
		orgID    string
		role     string
		expected string
	}{
		{name: "platform keeps superadmin", orgID: "00000000-0000-0000-0000-000000000000", role: "superadmin", expected: "superadmin"},
		{name: "tenant clamps superadmin to owner", orgID: "tenant-org", role: "superadmin", expected: "owner"},
		{name: "tenant keeps owner", orgID: "tenant-org", role: "owner", expected: "owner"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := client.normalizeRoleForOrg(tt.orgID, tt.role)
			if got != tt.expected {
				t.Fatalf("normalizeRoleForOrg(%q, %q) = %q, want %q", tt.orgID, tt.role, got, tt.expected)
			}
		})
	}
}

// TestGenerateRandomString tests random string generation
func TestGenerateRandomString(t *testing.T) {
	t.Run("generates correct length", func(t *testing.T) {
		s, err := generateRandomString(32)
		if err != nil {
			t.Fatalf("generateRandomString() error = %v", err)
		}
		if len(s) != 32 {
			t.Errorf("len(s) = %d, want 32", len(s))
		}
	})

	t.Run("generates unique strings", func(t *testing.T) {
		s1, _ := generateRandomString(32)
		s2, _ := generateRandomString(32)
		if s1 == s2 {
			t.Error("Generated strings should be unique")
		}
	})
}

// TestGenerateCodeChallenge tests PKCE code challenge generation
func TestGenerateCodeChallenge(t *testing.T) {
	t.Run("generates valid code challenge", func(t *testing.T) {
		verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
		challenge := generateCodeChallenge(verifier)

		// Challenge should be URL-safe base64 encoded
		if strings.Contains(challenge, "+") || strings.Contains(challenge, "/") {
			t.Error("Challenge should be URL-safe base64")
		}

		// Should not be empty
		if challenge == "" {
			t.Error("Challenge should not be empty")
		}
	})

	t.Run("same verifier produces same challenge", func(t *testing.T) {
		verifier := "test-verifier-12345"
		c1 := generateCodeChallenge(verifier)
		c2 := generateCodeChallenge(verifier)
		if c1 != c2 {
			t.Error("Same verifier should produce same challenge")
		}
	})

	t.Run("different verifiers produce different challenges", func(t *testing.T) {
		c1 := generateCodeChallenge("verifier-1")
		c2 := generateCodeChallenge("verifier-2")
		if c1 == c2 {
			t.Error("Different verifiers should produce different challenges")
		}
	})
}

// TestOIDCClient_ExtractOrgID tests org ID extraction from claims
func TestOIDCClient_ExtractOrgID(t *testing.T) {
	tests := []struct {
		name      string
		orgClaim  string
		claims    *IDTokenClaims
		userInfo  *UserInfo
		wantOrgID string
	}{
		{
			name:     "extract from ID token extra claims",
			orgClaim: "tenant_id",
			claims: &IDTokenClaims{
				Extra: map[string]interface{}{
					"tenant_id": "org-12345",
				},
			},
			wantOrgID: "org-12345",
		},
		{
			name:     "extract from userinfo",
			orgClaim: "org_id",
			claims:   &IDTokenClaims{Extra: map[string]interface{}{}},
			userInfo: &UserInfo{
				OrgID: "org-from-userinfo",
			},
			wantOrgID: "org-from-userinfo",
		},
		{
			name:      "no org claim configured",
			orgClaim:  "",
			claims:    &IDTokenClaims{Extra: map[string]interface{}{"tenant_id": "org-12345"}},
			wantOrgID: "",
		},
		{
			name:      "org claim missing from token",
			orgClaim:  "missing_claim",
			claims:    &IDTokenClaims{Extra: map[string]interface{}{}},
			wantOrgID: "",
		},
		{
			name:     "platform org claim value maps to platform org ID",
			orgClaim: "tenant_id",
			claims: &IDTokenClaims{
				Extra: map[string]interface{}{
					"tenant_id": "platform",
				},
			},
			wantOrgID: "00000000-0000-0000-0000-000000000000",
		},
		{
			name:     "non-platform claim value passes through",
			orgClaim: "tenant_id",
			claims: &IDTokenClaims{
				Extra: map[string]interface{}{
					"tenant_id": "tenant-abc",
				},
			},
			wantOrgID: "tenant-abc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &OIDCClient{allowPrivateIPs: true,
				config: &config.OIDCConfig{
					OrgClaim:              tt.orgClaim,
					PlatformOrgID:         "00000000-0000-0000-0000-000000000000",
					PlatformOrgClaimValue: "platform",
				},
			}

			got := client.extractOrgID(tt.claims, tt.userInfo)
			if got != tt.wantOrgID {
				t.Errorf("extractOrgID() = %q, want %q", got, tt.wantOrgID)
			}
		})
	}
}

// --- parseIDToken direct tests (with real RSA-signed JWTs + mock JWKS) ---

// testKeyPair holds a pre-generated RSA key for tests.
var testKeyPair *rsa.PrivateKey

func init() {
	// Generate a 2048-bit RSA key once for all tests.
	var err error
	testKeyPair, err = rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic("failed to generate test RSA key: " + err.Error())
	}
}

const testKid = "test-key-1"

// signedJWT creates an RS256-signed JWT with the given claims map.
func signedJWT(t *testing.T, claimsMap map[string]interface{}) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims(claimsMap))
	token.Header["kid"] = testKid
	signed, err := token.SignedString(testKeyPair)
	if err != nil {
		t.Fatalf("failed to sign JWT: %v", err)
	}
	return signed
}

// mockJWKSServer starts an HTTP server serving a JWKS endpoint with the test RSA public key
// and an OIDC discovery document pointing to it.
func mockJWKSServer(t *testing.T, issuer string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		// Use the actual server URL (set after server creation via closure)
		json.NewEncoder(w).Encode(map[string]string{
			"issuer":   issuer,
			"jwks_uri": issuer + "/.well-known/jwks.json",
		})
	})

	pub := &testKeyPair.PublicKey
	mux.HandleFunc("/.well-known/jwks.json", func(w http.ResponseWriter, r *http.Request) {
		jwks := map[string]interface{}{
			"keys": []map[string]string{
				{
					"kty": "RSA",
					"kid": testKid,
					"use": "sig",
					"alg": "RS256",
					"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
					"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
				},
			},
		}
		json.NewEncoder(w).Encode(jwks)
	})

	return httptest.NewServer(mux)
}

// newTestOIDCClient creates an OIDCClient pointing at the mock server.
func newTestOIDCClient(serverURL string) *OIDCClient {
	return &OIDCClient{allowPrivateIPs: true,
		config: &config.OIDCConfig{
			Issuer:           serverURL,
			AllowedClockSkew: 2 * time.Minute,
		},
		states:   make(map[string]*AuthState),
		jwksKeys: make(map[string]crypto.PublicKey),
	}
}

// b64 encodes JSON bytes as base64url (no padding).
func b64(jsonStr string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(jsonStr))
}

func TestParseIDToken_ValidToken(t *testing.T) {
	server := mockJWKSServer(t, "PLACEHOLDER")
	defer server.Close()
	// Re-create with actual URL as issuer
	server.Close()
	server = mockJWKSServer(t, server.URL)
	// We need a fresh server — use a helper approach
	server.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()

	// Create server with self-referencing issuer
	var jwksSrv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"issuer":   jwksSrv.URL,
			"jwks_uri": jwksSrv.URL + "/.well-known/jwks.json",
		})
	})
	pub := &testKeyPair.PublicKey
	mux.HandleFunc("/.well-known/jwks.json", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"keys": []map[string]string{{
				"kty": "RSA", "kid": testKid, "use": "sig", "alg": "RS256",
				"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
				"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
			}},
		})
	})
	jwksSrv = httptest.NewServer(mux)
	defer jwksSrv.Close()

	issuer := jwksSrv.URL
	client := newTestOIDCClient(issuer)

	token := signedJWT(t, map[string]interface{}{
		"iss": issuer, "sub": "user-1", "aud": "client-1",
		"exp": time.Now().Add(1 * time.Hour).Unix(), "iat": 1600000000,
		"email": "test@example.com", "name": "Test User", "nonce": "test-nonce",
	})

	claims, err := client.parseIDToken(token, "test-nonce")
	if err != nil {
		t.Fatalf("parseIDToken() error = %v", err)
	}
	if claims.Subject != "user-1" {
		t.Errorf("Subject = %q, want %q", claims.Subject, "user-1")
	}
	if claims.Email != "test@example.com" {
		t.Errorf("Email = %q, want %q", claims.Email, "test@example.com")
	}
	if claims.Name != "Test User" {
		t.Errorf("Name = %q, want %q", claims.Name, "Test User")
	}
	if claims.Issuer != issuer {
		t.Errorf("Issuer = %q, want %q", claims.Issuer, issuer)
	}
}

// jwksTestServer creates a self-referencing mock OIDC server for tests.
// Returns the server (caller must defer Close()) and its URL.
func jwksTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	mux := http.NewServeMux()
	pub := &testKeyPair.PublicKey
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"issuer":   srv.URL,
			"jwks_uri": srv.URL + "/.well-known/jwks.json",
		})
	})
	mux.HandleFunc("/.well-known/jwks.json", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"keys": []map[string]string{{
				"kty": "RSA", "kid": testKid, "use": "sig", "alg": "RS256",
				"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
				"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
			}},
		})
	})
	srv = httptest.NewServer(mux)
	return srv
}

func TestParseIDToken_ExpiredToken(t *testing.T) {
	srv := jwksTestServer(t)
	defer srv.Close()
	client := newTestOIDCClient(srv.URL)

	token := signedJWT(t, map[string]interface{}{
		"iss": srv.URL, "sub": "user-1", "aud": "client-1",
		"exp": time.Now().Add(-1 * time.Hour).Unix(), "iat": 1600000000,
	})

	_, err := client.parseIDToken(token, "")
	if err == nil {
		t.Error("parseIDToken() should fail for expired token")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("error should mention 'expired', got: %v", err)
	}
}

func TestParseIDToken_IssuerMismatch(t *testing.T) {
	srv := jwksTestServer(t)
	defer srv.Close()
	client := newTestOIDCClient(srv.URL)

	token := signedJWT(t, map[string]interface{}{
		"iss": "https://wrong-issuer.com", "sub": "user-1", "aud": "client-1",
		"exp": time.Now().Add(1 * time.Hour).Unix(), "iat": 1600000000,
	})

	_, err := client.parseIDToken(token, "")
	if err == nil {
		t.Error("parseIDToken() should fail for issuer mismatch")
	}
	if !strings.Contains(err.Error(), "issuer mismatch") {
		t.Errorf("error should mention 'issuer mismatch', got: %v", err)
	}
}

func TestParseIDToken_NonceMismatch(t *testing.T) {
	srv := jwksTestServer(t)
	defer srv.Close()
	client := newTestOIDCClient(srv.URL)

	token := signedJWT(t, map[string]interface{}{
		"iss": srv.URL, "sub": "user-1", "aud": "client-1",
		"exp": time.Now().Add(1 * time.Hour).Unix(), "iat": 1600000000,
		"nonce": "token-nonce",
	})

	_, err := client.parseIDToken(token, "different-nonce")
	if err == nil {
		t.Error("parseIDToken() should fail for nonce mismatch")
	}
	if !strings.Contains(err.Error(), "nonce mismatch") {
		t.Errorf("error should mention 'nonce mismatch', got: %v", err)
	}
}

func TestParseIDToken_InvalidFormat(t *testing.T) {
	srv := jwksTestServer(t)
	defer srv.Close()
	client := newTestOIDCClient(srv.URL)

	_, err := client.parseIDToken("not-a-jwt-token", "")
	if err == nil {
		t.Error("parseIDToken() should fail for invalid format")
	}
}

func TestParseIDToken_EmptyToken(t *testing.T) {
	client := &OIDCClient{allowPrivateIPs: true,
		config: &config.OIDCConfig{
			Issuer: "https://auth.example.com",
		},
	}

	_, err := client.parseIDToken("", "")
	if err == nil {
		t.Error("parseIDToken() should fail for empty token")
	}
	if !strings.Contains(err.Error(), "empty ID token") {
		t.Errorf("error should mention 'empty ID token', got: %v", err)
	}
}

func TestParseIDToken_CustomClaims(t *testing.T) {
	srv := jwksTestServer(t)
	defer srv.Close()
	client := newTestOIDCClient(srv.URL)

	token := signedJWT(t, map[string]interface{}{
		"iss": srv.URL, "sub": "user-1", "aud": "client-1",
		"exp": time.Now().Add(1 * time.Hour).Unix(), "iat": 1600000000,
		"tenant_id": "org-abc", "roles": []string{"admin", "user"}, "custom_field": "custom_value",
	})

	claims, err := client.parseIDToken(token, "")
	if err != nil {
		t.Fatalf("parseIDToken() error = %v", err)
	}

	if claims.Extra == nil {
		t.Fatal("Extra claims should not be nil")
	}
	if claims.Extra["tenant_id"] != "org-abc" {
		t.Errorf("Extra[tenant_id] = %v, want %q", claims.Extra["tenant_id"], "org-abc")
	}
	if claims.Extra["custom_field"] != "custom_value" {
		t.Errorf("Extra[custom_field] = %v, want %q", claims.Extra["custom_field"], "custom_value")
	}
	roles, ok := claims.Extra["roles"].([]interface{})
	if !ok {
		t.Fatal("Extra[roles] should be a slice")
	}
	if len(roles) != 2 || roles[0] != "admin" || roles[1] != "user" {
		t.Errorf("Extra[roles] = %v, want [admin, user]", roles)
	}
}

func TestParseIDToken_TrailingSlashIssuer(t *testing.T) {
	srv := jwksTestServer(t)
	defer srv.Close()
	client := newTestOIDCClient(srv.URL)

	// Token issuer has trailing slash, config does not
	token := signedJWT(t, map[string]interface{}{
		"iss": srv.URL + "/", "sub": "user-1", "aud": "client-1",
		"exp": time.Now().Add(1 * time.Hour).Unix(), "iat": 1600000000,
	})

	claims, err := client.parseIDToken(token, "")
	if err != nil {
		t.Fatalf("parseIDToken() should handle trailing slash, got error: %v", err)
	}
	if claims.Subject != "user-1" {
		t.Errorf("Subject = %q, want %q", claims.Subject, "user-1")
	}
}

func TestParseIDToken_InvalidSignature(t *testing.T) {
	srv := jwksTestServer(t)
	defer srv.Close()
	client := newTestOIDCClient(srv.URL)

	// Sign with a DIFFERENT key than what the JWKS serves
	wrongKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": srv.URL, "sub": "user-1", "aud": "client-1",
		"exp": time.Now().Add(1 * time.Hour).Unix(), "iat": 1600000000,
	})
	token.Header["kid"] = testKid
	signed, _ := token.SignedString(wrongKey)

	_, err := client.parseIDToken(signed, "")
	if err == nil {
		t.Error("parseIDToken() should fail for invalid signature")
	}
}

func TestParseRSAJWK(t *testing.T) {
	pub := &testKeyPair.PublicKey
	k := jwkKey{
		Kid: "test", Kty: "RSA", Use: "sig", Alg: "RS256",
		N: base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		E: base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}
	parsed, err := parseRSAJWK(k)
	if err != nil {
		t.Fatalf("parseRSAJWK() error = %v", err)
	}
	if parsed.N.Cmp(pub.N) != 0 {
		t.Error("parsed key N does not match original")
	}
	if parsed.E != pub.E {
		t.Errorf("parsed key E = %d, want %d", parsed.E, pub.E)
	}
}

// TestParseGroupClaims tests parsing of group claims in various formats
func TestParseGroupClaims(t *testing.T) {
	tests := []struct {
		name string
		raw  interface{}
		want []GroupClaim
	}{
		{
			name: "string array",
			raw:  []interface{}{"engineering", "ops"},
			want: []GroupClaim{
				{ID: "engineering", Name: "engineering"},
				{ID: "ops", Name: "ops"},
			},
		},
		{
			name: "object array with id and name",
			raw: []interface{}{
				map[string]interface{}{"id": "grp-1", "name": "Engineering"},
				map[string]interface{}{"id": "grp-2", "name": "Operations"},
			},
			want: []GroupClaim{
				{ID: "grp-1", Name: "Engineering"},
				{ID: "grp-2", Name: "Operations"},
			},
		},
		{
			name: "object with name only uses name as id",
			raw: []interface{}{
				map[string]interface{}{"name": "Marketing"},
			},
			want: []GroupClaim{
				{ID: "Marketing", Name: "Marketing"},
			},
		},
		{
			name: "object with id only uses id as name",
			raw: []interface{}{
				map[string]interface{}{"id": "grp-3"},
			},
			want: []GroupClaim{
				{ID: "grp-3", Name: "grp-3"},
			},
		},
		{
			name: "empty object is skipped",
			raw: []interface{}{
				map[string]interface{}{},
			},
			want: nil,
		},
		{
			name: "mixed string and object",
			raw: []interface{}{
				"simple-group",
				map[string]interface{}{"id": "grp-1", "name": "Complex Group"},
			},
			want: []GroupClaim{
				{ID: "simple-group", Name: "simple-group"},
				{ID: "grp-1", Name: "Complex Group"},
			},
		},
		{
			name: "nil input",
			raw:  nil,
			want: nil,
		},
		{
			name: "wrong type (string instead of array)",
			raw:  "not-an-array",
			want: nil,
		},
		{
			name: "empty array",
			raw:  []interface{}{},
			want: nil,
		},
		{
			name: "unsupported element type is skipped",
			raw:  []interface{}{42, true},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseGroupClaims(tt.raw)
			if len(got) != len(tt.want) {
				t.Fatalf("parseGroupClaims() returned %d groups, want %d", len(got), len(tt.want))
			}
			for i, g := range got {
				if g.ID != tt.want[i].ID {
					t.Errorf("group[%d].ID = %q, want %q", i, g.ID, tt.want[i].ID)
				}
				if g.Name != tt.want[i].Name {
					t.Errorf("group[%d].Name = %q, want %q", i, g.Name, tt.want[i].Name)
				}
			}
		})
	}
}

// TestParseDepartmentClaims tests parsing of department claims in various formats
func TestParseDepartmentClaims(t *testing.T) {
	tests := []struct {
		name string
		raw  interface{}
		want []DepartmentClaim
	}{
		{
			name: "string array",
			raw:  []interface{}{"hr", "finance"},
			want: []DepartmentClaim{
				{ID: "hr", Name: "hr"},
				{ID: "finance", Name: "finance"},
			},
		},
		{
			name: "object array with parent_id",
			raw: []interface{}{
				map[string]interface{}{"id": "dept-1", "name": "Engineering", "parent_id": "dept-root"},
			},
			want: []DepartmentClaim{
				{ID: "dept-1", Name: "Engineering", ParentID: "dept-root"},
			},
		},
		{
			name: "object without parent_id",
			raw: []interface{}{
				map[string]interface{}{"id": "dept-2", "name": "Sales"},
			},
			want: []DepartmentClaim{
				{ID: "dept-2", Name: "Sales"},
			},
		},
		{
			name: "name only uses name as id",
			raw: []interface{}{
				map[string]interface{}{"name": "Support"},
			},
			want: []DepartmentClaim{
				{ID: "Support", Name: "Support"},
			},
		},
		{
			name: "id only uses id as name",
			raw: []interface{}{
				map[string]interface{}{"id": "dept-3"},
			},
			want: []DepartmentClaim{
				{ID: "dept-3", Name: "dept-3"},
			},
		},
		{
			name: "empty object is skipped",
			raw: []interface{}{
				map[string]interface{}{},
			},
			want: nil,
		},
		{
			name: "nil input",
			raw:  nil,
			want: nil,
		},
		{
			name: "wrong type",
			raw:  "not-an-array",
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseDepartmentClaims(tt.raw)
			if len(got) != len(tt.want) {
				t.Fatalf("parseDepartmentClaims() returned %d depts, want %d", len(got), len(tt.want))
			}
			for i, d := range got {
				if d.ID != tt.want[i].ID {
					t.Errorf("dept[%d].ID = %q, want %q", i, d.ID, tt.want[i].ID)
				}
				if d.Name != tt.want[i].Name {
					t.Errorf("dept[%d].Name = %q, want %q", i, d.Name, tt.want[i].Name)
				}
				if d.ParentID != tt.want[i].ParentID {
					t.Errorf("dept[%d].ParentID = %q, want %q", i, d.ParentID, tt.want[i].ParentID)
				}
			}
		})
	}
}

// TestOIDCClient_GetClaimValue tests claim value retrieval from ID token extra claims
func TestOIDCClient_GetClaimValue(t *testing.T) {
	client := &OIDCClient{allowPrivateIPs: true, config: &config.OIDCConfig{}}

	t.Run("existing claim", func(t *testing.T) {
		claims := &IDTokenClaims{
			Extra: map[string]interface{}{
				"groups": []interface{}{"eng", "ops"},
			},
		}
		got := client.getClaimValue(claims, "groups")
		arr, ok := got.([]interface{})
		if !ok || len(arr) != 2 {
			t.Errorf("getClaimValue() = %v, want []interface{} with 2 elements", got)
		}
	})

	t.Run("missing claim", func(t *testing.T) {
		claims := &IDTokenClaims{
			Extra: map[string]interface{}{},
		}
		got := client.getClaimValue(claims, "nonexistent")
		if got != nil {
			t.Errorf("getClaimValue() = %v, want nil", got)
		}
	})

	t.Run("nil extra", func(t *testing.T) {
		claims := &IDTokenClaims{Extra: nil}
		got := client.getClaimValue(claims, "anything")
		if got != nil {
			t.Errorf("getClaimValue() = %v, want nil", got)
		}
	})
}

// TestOIDCClient_ExtractGroups tests group extraction from OIDC claims
func TestOIDCClient_ExtractGroups(t *testing.T) {
	t.Run("extracts groups from claims", func(t *testing.T) {
		client := &OIDCClient{allowPrivateIPs: true,
			config: &config.OIDCConfig{GroupsClaim: "groups"},
		}
		claims := &IDTokenClaims{
			Extra: map[string]interface{}{
				"groups": []interface{}{"eng", "ops"},
			},
		}
		groups := client.extractGroups(claims, nil)
		if len(groups) != 2 {
			t.Fatalf("extractGroups() returned %d groups, want 2", len(groups))
		}
		if groups[0].ID != "eng" || groups[1].ID != "ops" {
			t.Errorf("extractGroups() = %v, want eng and ops", groups)
		}
	})

	t.Run("no groups claim configured", func(t *testing.T) {
		client := &OIDCClient{allowPrivateIPs: true,
			config: &config.OIDCConfig{GroupsClaim: ""},
		}
		claims := &IDTokenClaims{
			Extra: map[string]interface{}{
				"groups": []interface{}{"eng"},
			},
		}
		groups := client.extractGroups(claims, nil)
		if groups != nil {
			t.Errorf("extractGroups() = %v, want nil when no claim configured", groups)
		}
	})

	t.Run("claim not present in token", func(t *testing.T) {
		client := &OIDCClient{allowPrivateIPs: true,
			config: &config.OIDCConfig{GroupsClaim: "groups"},
		}
		claims := &IDTokenClaims{
			Extra: map[string]interface{}{},
		}
		groups := client.extractGroups(claims, nil)
		if groups != nil {
			t.Errorf("extractGroups() = %v, want nil when claim missing", groups)
		}
	})
}

// TestOIDCClient_ExtractDepartments tests department extraction from OIDC claims
func TestOIDCClient_ExtractDepartments(t *testing.T) {
	t.Run("extracts departments from claims", func(t *testing.T) {
		client := &OIDCClient{allowPrivateIPs: true,
			config: &config.OIDCConfig{DepartmentsClaim: "departments"},
		}
		claims := &IDTokenClaims{
			Extra: map[string]interface{}{
				"departments": []interface{}{
					map[string]interface{}{"id": "dept-1", "name": "Engineering", "parent_id": "root"},
				},
			},
		}
		depts := client.extractDepartments(claims, nil)
		if len(depts) != 1 {
			t.Fatalf("extractDepartments() returned %d depts, want 1", len(depts))
		}
		if depts[0].ID != "dept-1" || depts[0].Name != "Engineering" || depts[0].ParentID != "root" {
			t.Errorf("extractDepartments() = %v", depts[0])
		}
	})

	t.Run("no departments claim configured", func(t *testing.T) {
		client := &OIDCClient{allowPrivateIPs: true,
			config: &config.OIDCConfig{DepartmentsClaim: ""},
		}
		claims := &IDTokenClaims{
			Extra: map[string]interface{}{
				"departments": []interface{}{"hr"},
			},
		}
		depts := client.extractDepartments(claims, nil)
		if depts != nil {
			t.Errorf("extractDepartments() = %v, want nil", depts)
		}
	})
}

// TestOIDCClient_GetUserInfo tests the getUserInfo method with mock server
func TestOIDCClient_GetUserInfo(t *testing.T) {
	server := mockOIDCProvider(t)
	defer server.Close()

	t.Run("successful userinfo fetch", func(t *testing.T) {
		// Pre-cache the discovery with the mock server's actual URL
		client := &OIDCClient{allowPrivateIPs: true,
			config: &config.OIDCConfig{
				Enabled:  true,
				Issuer:   server.URL,
				ClientID: "test-client",
			},
			states: make(map[string]*AuthState),
			discovery: &OIDCDiscovery{
				Issuer:           server.URL,
				UserInfoEndpoint: server.URL + "/userinfo",
			},
			discoveryAt: time.Now(),
		}

		userInfo, err := client.getUserInfo(context.Background(), "valid-access-token")
		if err != nil {
			t.Fatalf("getUserInfo() error = %v", err)
		}
		if userInfo.Email != "test@example.com" {
			t.Errorf("Email = %q, want %q", userInfo.Email, "test@example.com")
		}
		if userInfo.Name != "Test User" {
			t.Errorf("Name = %q, want %q", userInfo.Name, "Test User")
		}
		if userInfo.Subject != "user-12345" {
			t.Errorf("Subject = %q, want %q", userInfo.Subject, "user-12345")
		}
	})

	t.Run("no userinfo endpoint", func(t *testing.T) {
		// Create a server without userinfo endpoint in discovery
		noUserInfoMux := http.NewServeMux()
		noUserInfoMux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
			discovery := OIDCDiscovery{
				Issuer:                "http://localhost",
				AuthorizationEndpoint: "http://localhost/authorize",
				TokenEndpoint:         "http://localhost/token",
				UserInfoEndpoint:      "", // no userinfo
			}
			json.NewEncoder(w).Encode(discovery)
		})
		noUIServer := httptest.NewServer(noUserInfoMux)
		defer noUIServer.Close()

		client := &OIDCClient{allowPrivateIPs: true,
			config: &config.OIDCConfig{
				Enabled:  true,
				Issuer:   noUIServer.URL,
				ClientID: "test-client",
			},
			states: make(map[string]*AuthState),
		}

		userInfo, err := client.getUserInfo(context.Background(), "token")
		if err != nil {
			t.Errorf("getUserInfo() should not error for missing endpoint: %v", err)
		}
		if userInfo != nil {
			t.Errorf("getUserInfo() should return nil for missing endpoint")
		}
	})
}

// TestSha256Sum tests the sha256Sum helper function
func TestSha256Sum(t *testing.T) {
	t.Run("consistent output", func(t *testing.T) {
		h1 := sha256Sum([]byte("hello"))
		h2 := sha256Sum([]byte("hello"))
		if h1 != h2 {
			t.Error("same input should produce same hash")
		}
	})

	t.Run("different input different output", func(t *testing.T) {
		h1 := sha256Sum([]byte("hello"))
		h2 := sha256Sum([]byte("world"))
		if h1 == h2 {
			t.Error("different input should produce different hash")
		}
	})

	t.Run("output is 32 bytes", func(t *testing.T) {
		h := sha256Sum([]byte("test"))
		if len(h) != 32 {
			t.Errorf("sha256Sum output length = %d, want 32", len(h))
		}
	})
}

// TestOIDCClient_ExtractRoles tests roles extraction from claims
func TestOIDCClient_ExtractRoles(t *testing.T) {
	tests := []struct {
		name       string
		rolesClaim string
		claims     *IDTokenClaims
		userInfo   *UserInfo
		wantRoles  []string
	}{
		{
			name:       "extract array of roles from claims",
			rolesClaim: "roles",
			claims: &IDTokenClaims{
				Extra: map[string]interface{}{
					"roles": []interface{}{"admin", "user"},
				},
			},
			wantRoles: []string{"admin", "user"},
		},
		{
			name:       "extract single role string from claims",
			rolesClaim: "role",
			claims: &IDTokenClaims{
				Extra: map[string]interface{}{
					"role": "admin",
				},
			},
			wantRoles: []string{"admin"},
		},
		{
			name:       "extract from userinfo",
			rolesClaim: "roles",
			claims:     &IDTokenClaims{Extra: map[string]interface{}{}},
			userInfo: &UserInfo{
				Roles: []string{"user", "viewer"},
			},
			wantRoles: []string{"user", "viewer"},
		},
		{
			name:       "no roles claim configured",
			rolesClaim: "",
			claims:     &IDTokenClaims{Extra: map[string]interface{}{"roles": []interface{}{"admin"}}},
			wantRoles:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &OIDCClient{allowPrivateIPs: true,
				config: &config.OIDCConfig{
					RolesClaim: tt.rolesClaim,
				},
			}

			got := client.extractRoles(tt.claims, tt.userInfo)

			if len(got) != len(tt.wantRoles) {
				t.Errorf("extractRoles() returned %d roles, want %d", len(got), len(tt.wantRoles))
				return
			}

			for i, role := range got {
				if role != tt.wantRoles[i] {
					t.Errorf("role[%d] = %q, want %q", i, role, tt.wantRoles[i])
				}
			}
		})
	}
}
