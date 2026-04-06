//go:build integration

package integration

import (
	"context"
	crand "crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	authpkg "github.com/Sesame-Disk/sesamefs/internal/auth"
	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const oidcProjectionTestRedirectURI = "http://localhost:3000/sso"

type oidcProjectionIdentity struct {
	Subject string
	Email   string
	Name    string
	OrgID   string
	Roles   []string
}

type oidcProjectionMockProvider struct {
	t          *testing.T
	identity   oidcProjectionIdentity
	privateKey *rsa.PrivateKey
	kid        string
	nonce      string
	server     *httptest.Server
}

func newOIDCProjectionMockProvider(t *testing.T, identity oidcProjectionIdentity) *oidcProjectionMockProvider {
	t.Helper()

	privateKey, err := rsa.GenerateKey(crand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key for OIDC mock provider failed: %v", err)
	}

	provider := &oidcProjectionMockProvider{
		t:          t,
		identity:   identity,
		privateKey: privateKey,
		kid:        "oidc-projection-test-key",
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(authpkg.OIDCDiscovery{
			Issuer:                provider.server.URL,
			AuthorizationEndpoint: provider.server.URL + "/authorize",
			TokenEndpoint:         provider.server.URL + "/token",
			UserInfoEndpoint:      provider.server.URL + "/userinfo",
			JwksURI:               provider.server.URL + "/.well-known/jwks.json",
			ScopesSupported:       []string{"openid", "profile", "email"},
			ClaimsSupported:       []string{"sub", "email", "name", "tenant_id", "roles"},
		})
	})
	mux.HandleFunc("/.well-known/jwks.json", func(w http.ResponseWriter, r *http.Request) {
		pub := &provider.privateKey.PublicKey
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"keys": []map[string]string{{
				"kty": "RSA",
				"kid": provider.kid,
				"use": "sig",
				"alg": "RS256",
				"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
			}},
		})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if r.FormValue("code") == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(authpkg.TokenResponse{
			AccessToken: "oidc-projection-access-token",
			TokenType:   "Bearer",
			ExpiresIn:   3600,
			IDToken:     provider.signedIDToken(),
		})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(authpkg.UserInfo{
			Subject: provider.identity.Subject,
			Email:   provider.identity.Email,
			Name:    provider.identity.Name,
			OrgID:   provider.identity.OrgID,
			Roles:   provider.identity.Roles,
		})
	})

	provider.server = httptest.NewServer(mux)
	t.Cleanup(provider.server.Close)
	return provider
}

func (p *oidcProjectionMockProvider) signedIDToken() string {
	p.t.Helper()

	claims := jwt.MapClaims{
		"iss":   p.server.URL,
		"sub":   p.identity.Subject,
		"aud":   "oidc-projection-client",
		"exp":   time.Now().Add(1 * time.Hour).Unix(),
		"iat":   time.Now().Add(-1 * time.Minute).Unix(),
		"nonce": p.nonce,
		"email": p.identity.Email,
		"name":  p.identity.Name,
	}
	if p.identity.OrgID != "" {
		claims["tenant_id"] = p.identity.OrgID
	}
	if len(p.identity.Roles) > 0 {
		claims["roles"] = p.identity.Roles
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = p.kid
	signed, err := token.SignedString(p.privateKey)
	if err != nil {
		p.t.Fatalf("sign OIDC test token failed: %v", err)
	}
	return signed
}

func newOIDCProjectionTestClient(t *testing.T, issuer, defaultOrgName string) *authpkg.OIDCClient {
	t.Helper()

	database := shareProjectionDBForTest(t)
	appCfg := &config.Config{
		Storage: config.StorageConfig{
			DefaultClass: "s3",
		},
		Organizations: config.OrganizationsConfig{
			DefaultTemplate: "free",
		},
		Auth: config.AuthConfig{
			OIDC: config.OIDCConfig{
				Enabled:          true,
				Issuer:           issuer,
				ClientID:         "oidc-projection-client",
				ClientSecret:     "oidc-projection-secret",
				RedirectURIs:     []string{oidcProjectionTestRedirectURI},
				Scopes:           []string{"openid", "profile", "email"},
				OrgClaim:         "tenant_id",
				RolesClaim:       "roles",
				AutoProvision:    true,
				DefaultRole:      "user",
				DefaultOrgName:   defaultOrgName,
				PlatformOrgID:    "00000000-0000-0000-0000-000000000000",
				SessionTTL:       24 * time.Hour,
				APITokenTTL:      180 * 24 * time.Hour,
				RequirePKCE:      true,
				AllowedClockSkew: 2 * time.Minute,
			},
		},
	}

	sessions := authpkg.NewSessionManager(&appCfg.Auth.OIDC, database)
	return authpkg.NewOIDCClient(appCfg, database, sessions)
}

func prepareOIDCProjectionExchange(t *testing.T, client *authpkg.OIDCClient, provider *oidcProjectionMockProvider) string {
	t.Helper()

	authURL, err := client.GetAuthorizationURL(context.Background(), oidcProjectionTestRedirectURI, oidcProjectionTestRedirectURI)
	if err != nil {
		t.Fatalf("prepare OIDC authorization URL failed: %v", err)
	}
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse authorization URL failed: %v", err)
	}
	state := parsed.Query().Get("state")
	provider.nonce = parsed.Query().Get("nonce")
	if state == "" || provider.nonce == "" {
		t.Fatalf("expected non-empty state and nonce in authorization URL: %s", authURL)
	}
	return state
}

func forceCanonicalUserRole(t *testing.T, orgID, userID, role string) {
	t.Helper()
	if err := shareProjectionDBForTest(t).Session().Query(`
		UPDATE users SET role = ? WHERE org_id = ? AND user_id = ?
	`, role, orgID, userID).Exec(); err != nil {
		t.Fatalf("force canonical role %s for %s/%s failed: %v", role, orgID, userID, err)
	}
}

func attachOIDCIdentityForTest(t *testing.T, issuer, subject, orgID, userID string) {
	t.Helper()
	session := shareProjectionDBForTest(t).Session()
	if err := session.Query(`
		INSERT INTO users_by_oidc (oidc_issuer, oidc_sub, user_id, org_id)
		VALUES (?, ?, ?, ?)
	`, issuer, subject, userID, orgID).Exec(); err != nil {
		t.Fatalf("insert OIDC mapping for test failed: %v", err)
	}
	if err := session.Query(`
		UPDATE users SET oidc_sub = ? WHERE org_id = ? AND user_id = ?
	`, subject, orgID, userID).Exec(); err != nil {
		t.Fatalf("set canonical oidc_sub for test failed: %v", err)
	}
}

func oidcMappingForSubject(t *testing.T, issuer, subject string) (string, string, bool) {
	t.Helper()
	var userID, orgID string
	err := shareProjectionDBForTest(t).Session().Query(`
		SELECT user_id, org_id FROM users_by_oidc WHERE oidc_issuer = ? AND oidc_sub = ?
	`, issuer, subject).Scan(&userID, &orgID)
	if err != nil {
		return "", "", false
	}
	return userID, orgID, true
}

func canonicalUserRole(t *testing.T, orgID, userID string) string {
	t.Helper()
	var role string
	if err := shareProjectionDBForTest(t).Session().Query(`
		SELECT role FROM users WHERE org_id = ? AND user_id = ?
	`, orgID, userID).Scan(&role); err != nil {
		t.Fatalf("read canonical role for %s/%s failed: %v", orgID, userID, err)
	}
	return role
}

func canonicalUserOIDCSub(t *testing.T, orgID, userID string) string {
	t.Helper()
	var oidcSub string
	if err := shareProjectionDBForTest(t).Session().Query(`
		SELECT oidc_sub FROM users WHERE org_id = ? AND user_id = ?
	`, orgID, userID).Scan(&oidcSub); err != nil {
		t.Fatalf("read canonical oidc_sub for %s/%s failed: %v", orgID, userID, err)
	}
	return oidcSub
}

func TestOIDCProjectionRegression_AutoProvisionCreatesAlignedAdminReadModels(t *testing.T) {
	orgID := uuid.NewString()
	orgName := fmt.Sprintf("inttest-oidc-org-%d", time.Now().UnixNano())
	email := fmt.Sprintf("inttest-oidc-provision-%d@sesamefs.local", time.Now().UnixNano())
	subject := fmt.Sprintf("oidc-provision-%d", time.Now().UnixNano())

	provider := newOIDCProjectionMockProvider(t, oidcProjectionIdentity{
		Subject: subject,
		Email:   email,
		Name:    "OIDC Provisioned User",
		OrgID:   orgID,
		Roles:   []string{"admin"},
	})
	client := newOIDCProjectionTestClient(t, provider.server.URL, orgName)
	state := prepareOIDCProjectionExchange(t, client, provider)

	result, err := client.ExchangeCode(context.Background(), "oidc-provision-code", state, oidcProjectionTestRedirectURI)
	if err != nil {
		t.Fatalf("OIDC auto-provision exchange failed: %v", err)
	}
	if !result.IsNewUser {
		t.Fatalf("expected new user from OIDC auto-provision")
	}
	if result.OrgID != orgID {
		t.Fatalf("result org_id = %s, want %s", result.OrgID, orgID)
	}
	if result.Role != "owner" {
		t.Fatalf("result role = %s, want owner", result.Role)
	}

	t.Cleanup(func() {
		deleteScopedTestOrganization(t, orgID)
	})

	waitForIntegrationCondition(t, "OIDC auto-provision to align canonical rows, admin projections, and admin APIs", func() bool {
		mappedUserID, mappedOrgID, ok := oidcMappingForSubject(t, provider.server.URL, subject)
		if !ok || mappedOrgID != orgID {
			return false
		}
		if canonicalUserRole(t, orgID, mappedUserID) != "owner" {
			return false
		}
		if canonicalUserOIDCSub(t, orgID, mappedUserID) != subject {
			return false
		}
		orgRow, ok := adminOrganizationProjectionByID(t, orgID)
		if !ok || orgRow.Name != orgName || orgRow.OwnerEmail != email || orgRow.UsersCount != 1 || orgRow.Status != "active" {
			return false
		}
		userRow, ok := adminUserProjectionByEmail(t, email)
		if !ok || userRow.OrgID != orgID || userRow.UserID != mappedUserID || userRow.Role != "owner" || userRow.Status != "active" {
			return false
		}
		return adminOrganizationPresentInList(t, orgID, orgName, email) && adminUserPresentInSearch(t, email, "owner")
	})
}

func TestOIDCProjectionRegression_EmailAdoptionNormalizesLegacyTenantSuperadmin(t *testing.T) {
	orgName := fmt.Sprintf("inttest-oidc-adopt-org-%d", time.Now().UnixNano())
	ownerEmail := fmt.Sprintf("inttest-oidc-adopt-owner-%d@sesamefs.local", time.Now().UnixNano())
	orgID := createAdminIdentityTestOrganization(t, orgName, ownerEmail)
	email := fmt.Sprintf("inttest-oidc-adopt-user-%d@sesamefs.local", time.Now().UnixNano())
	subject := fmt.Sprintf("oidc-adopt-%d", time.Now().UnixNano())

	createResp := superadminClient.PostJSON(t, "/api/v2.1/admin/organizations/"+orgID+"/users/", map[string]string{
		"email": email,
		"name":  "OIDC Adopted User",
	})
	expectStatus(t, createResp, http.StatusCreated)
	createResp.Body.Close()

	userID, found := lookupUserIDByEmail(t, email)
	if !found {
		t.Fatalf("expected user_id for %s", email)
	}
	forceCanonicalUserRole(t, orgID, userID, "superadmin")

	provider := newOIDCProjectionMockProvider(t, oidcProjectionIdentity{
		Subject: subject,
		Email:   email,
		Name:    "OIDC Adopted User",
		OrgID:   orgID,
		Roles:   []string{"user"},
	})
	client := newOIDCProjectionTestClient(t, provider.server.URL, "unused")
	state := prepareOIDCProjectionExchange(t, client, provider)

	result, err := client.ExchangeCode(context.Background(), "oidc-adopt-code", state, oidcProjectionTestRedirectURI)
	if err != nil {
		t.Fatalf("OIDC email adoption exchange failed: %v", err)
	}
	if result.IsNewUser {
		t.Fatalf("expected existing user adoption, not auto-provision")
	}
	if result.UserID != userID {
		t.Fatalf("result user_id = %s, want %s", result.UserID, userID)
	}
	if result.Role != "owner" {
		t.Fatalf("result role = %s, want owner", result.Role)
	}

	waitForIntegrationCondition(t, "OIDC email adoption to normalize legacy tenant superadmin and sync projections", func() bool {
		mappedUserID, mappedOrgID, ok := oidcMappingForSubject(t, provider.server.URL, subject)
		if !ok || mappedUserID != userID || mappedOrgID != orgID {
			return false
		}
		if canonicalUserRole(t, orgID, userID) != "owner" {
			return false
		}
		if canonicalUserOIDCSub(t, orgID, userID) != subject {
			return false
		}
		userRow, ok := adminUserProjectionByEmail(t, email)
		if !ok || userRow.OrgID != orgID || userRow.Role != "owner" || userRow.Status != "active" {
			return false
		}
		orgRow, ok := adminOrganizationProjectionByID(t, orgID)
		if !ok || orgRow.UsersCount != 2 {
			return false
		}
		return adminUserPresentInSearch(t, email, "owner")
	})
}

func TestOIDCProjectionRegression_ExistingOIDCUserReloginReconcilesRoleToProjection(t *testing.T) {
	orgName := fmt.Sprintf("inttest-oidc-relogin-org-%d", time.Now().UnixNano())
	ownerEmail := fmt.Sprintf("inttest-oidc-relogin-owner-%d@sesamefs.local", time.Now().UnixNano())
	orgID := createAdminIdentityTestOrganization(t, orgName, ownerEmail)
	email := fmt.Sprintf("inttest-oidc-relogin-user-%d@sesamefs.local", time.Now().UnixNano())
	subject := fmt.Sprintf("oidc-relogin-%d", time.Now().UnixNano())

	createResp := superadminClient.PostJSON(t, "/api/v2.1/admin/organizations/"+orgID+"/users/", map[string]string{
		"email": email,
		"name":  "OIDC Relogin User",
	})
	expectStatus(t, createResp, http.StatusCreated)
	createResp.Body.Close()

	userID, found := lookupUserIDByEmail(t, email)
	if !found {
		t.Fatalf("expected user_id for %s", email)
	}

	provider := newOIDCProjectionMockProvider(t, oidcProjectionIdentity{
		Subject: subject,
		Email:   email,
		Name:    "OIDC Relogin User",
		OrgID:   orgID,
		Roles:   []string{"superadmin"},
	})
	attachOIDCIdentityForTest(t, provider.server.URL, subject, orgID, userID)
	client := newOIDCProjectionTestClient(t, provider.server.URL, "unused")
	state := prepareOIDCProjectionExchange(t, client, provider)

	result, err := client.ExchangeCode(context.Background(), "oidc-relogin-code", state, oidcProjectionTestRedirectURI)
	if err != nil {
		t.Fatalf("OIDC relogin exchange failed: %v", err)
	}
	if result.IsNewUser {
		t.Fatalf("expected existing mapped user relogin")
	}
	if result.Role != "owner" {
		t.Fatalf("result role = %s, want owner", result.Role)
	}

	waitForIntegrationCondition(t, "OIDC relogin to reconcile canonical role and admin user projection", func() bool {
		mappedUserID, mappedOrgID, ok := oidcMappingForSubject(t, provider.server.URL, subject)
		if !ok || mappedUserID != userID || mappedOrgID != orgID {
			return false
		}
		if canonicalUserRole(t, orgID, userID) != "owner" {
			return false
		}
		userRow, ok := adminUserProjectionByEmail(t, email)
		if !ok || userRow.OrgID != orgID || userRow.Role != "owner" || userRow.Status != "active" {
			return false
		}
		return adminUserPresentInSearch(t, email, "owner")
	})
}
