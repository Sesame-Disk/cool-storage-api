// Package auth provides authentication functionality for SesameFS
package auth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// OIDCClient handles OIDC authentication flows
type OIDCClient struct {
	appConfig *config.Config
	config    *config.OIDCConfig
	db        *db.DB
	sessions  *SessionManager

	// Cached OIDC discovery document
	discoveryMu sync.RWMutex
	discovery   *OIDCDiscovery
	discoveryAt time.Time

	// Cached JWKS keys for JWT signature verification
	jwksMu   sync.RWMutex
	jwksKeys map[string]crypto.PublicKey // kid -> public key
	jwksAt   time.Time

	// PKCE state storage (state -> code_verifier). Bounded in size and
	// swept by a background goroutine to cap memory use under load or
	// under a state-flooding attack (M-6).
	stateMu     sync.RWMutex
	states      map[string]*AuthState
	stateSweep  chan struct{} // closed to stop the sweeper goroutine
	sweepCtlMu  sync.Mutex
	sweepActive bool
}

// maxPendingStates caps the PKCE state map. An attacker who can reach
// /oauth/authorize unauthenticated could otherwise flood the map. Each
// AuthState is small (~few hundred bytes), so 10k entries ≈ a couple MB;
// plenty of headroom for real traffic, small enough to bound blast radius.
const maxPendingStates = 10000

// stateTTL is how long a PKCE state is honored before the sweeper reaps it.
// Matches pruneExpiredStatesLocked (10 minutes).
const stateTTL = 10 * time.Minute

// OIDCDiscovery represents the OIDC discovery document
type OIDCDiscovery struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	UserInfoEndpoint      string   `json:"userinfo_endpoint"`
	JwksURI               string   `json:"jwks_uri"`
	ScopesSupported       []string `json:"scopes_supported"`
	ClaimsSupported       []string `json:"claims_supported"`
	EndSessionEndpoint    string   `json:"end_session_endpoint"`
}

// AuthState holds the state for an ongoing authorization request
type AuthState struct {
	State        string
	Nonce        string
	CodeVerifier string // For PKCE
	RedirectURI  string
	CreatedAt    time.Time
	ReturnURL    string // Where to redirect after successful auth
}

// TokenResponse represents the OIDC token endpoint response
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	IDToken      string `json:"id_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

// IDTokenClaims represents the claims in an OIDC ID token
type IDTokenClaims struct {
	// Standard OIDC claims
	Issuer    string `json:"iss"`
	Subject   string `json:"sub"`
	Audience  string `json:"aud"`
	ExpiresAt int64  `json:"exp"`
	IssuedAt  int64  `json:"iat"`
	Nonce     string `json:"nonce,omitempty"`

	// Profile claims
	Name              string `json:"name,omitempty"`
	GivenName         string `json:"given_name,omitempty"`
	FamilyName        string `json:"family_name,omitempty"`
	PreferredUsername string `json:"preferred_username,omitempty"`
	Picture           string `json:"picture,omitempty"`

	// Email claims
	Email         string `json:"email,omitempty"`
	EmailVerified bool   `json:"email_verified,omitempty"`

	// Custom claims (will be extracted dynamically)
	Extra map[string]interface{} `json:"-"`
}

// jwksResponse represents the JSON Web Key Set response from the OIDC provider
type jwksResponse struct {
	Keys []jwkKey `json:"keys"`
}

// jwkKey represents a single JSON Web Key
type jwkKey struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Alg string `json:"alg,omitempty"`
	Use string `json:"use,omitempty"`
	N   string `json:"n,omitempty"`   // RSA modulus
	E   string `json:"e,omitempty"`   // RSA exponent
	Crv string `json:"crv,omitempty"` // EC curve
	X   string `json:"x,omitempty"`   // EC x coordinate
	Y   string `json:"y,omitempty"`   // EC y coordinate
}

// UserInfo represents the user information from OIDC
type UserInfo struct {
	Subject       string   `json:"sub"`
	Email         string   `json:"email"`
	EmailVerified bool     `json:"email_verified"`
	Name          string   `json:"name"`
	Picture       string   `json:"picture"`
	Locale        string   `json:"locale"`
	OrgID         string   `json:"org_id,omitempty"` // Extracted from custom claim
	Roles         []string `json:"roles,omitempty"`  // Extracted from custom claim
}

// AuthResult represents the result of a successful authentication
type AuthResult struct {
	UserID       string
	OrgID        string
	Email        string
	Name         string
	Role         string
	SessionToken string
	ExpiresAt    time.Time
	IsNewUser    bool
	ReturnURL    string // Original return URL from the auth state (carries sso_token for desktop client)
}

// NewOIDCClient creates a new OIDC client
func NewOIDCClient(appCfg *config.Config, database *db.DB, sessions *SessionManager) *OIDCClient {
	c := &OIDCClient{
		appConfig:  appCfg,
		config:     &appCfg.Auth.OIDC,
		db:         database,
		sessions:   sessions,
		states:     make(map[string]*AuthState),
		jwksKeys:   make(map[string]crypto.PublicKey),
		stateSweep: make(chan struct{}),
	}
	c.startStateSweeper()
	return c
}

// startStateSweeper launches a background goroutine that periodically reaps
// expired PKCE states. The lazy prune inside storeState/consumeState only
// runs on traffic — if a burst of states arrives and then traffic stops,
// the map would pin memory until the next hit. This ticker guarantees
// cleanup even under idle conditions.
func (c *OIDCClient) startStateSweeper() {
	c.sweepCtlMu.Lock()
	if c.sweepActive {
		c.sweepCtlMu.Unlock()
		return
	}
	c.sweepActive = true
	c.sweepCtlMu.Unlock()

	go func() {
		ticker := time.NewTicker(stateTTL / 2)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				c.stateMu.Lock()
				c.pruneExpiredStatesLocked()
				c.stateMu.Unlock()
			case <-c.stateSweep:
				return
			}
		}
	}()
}

// StopStateSweeper halts the background sweeper. Safe to call multiple times.
func (c *OIDCClient) StopStateSweeper() {
	c.sweepCtlMu.Lock()
	defer c.sweepCtlMu.Unlock()
	if !c.sweepActive {
		return
	}
	close(c.stateSweep)
	c.sweepActive = false
}

// IsEnabled returns whether OIDC authentication is enabled
func (c *OIDCClient) IsEnabled() bool {
	return c.config.Enabled && c.config.Issuer != "" && c.config.ClientID != ""
}

// GetDiscovery fetches and caches the OIDC discovery document
func (c *OIDCClient) GetDiscovery(ctx context.Context) (*OIDCDiscovery, error) {
	c.discoveryMu.RLock()
	if c.discovery != nil && time.Since(c.discoveryAt) < 1*time.Hour {
		d := c.discovery
		c.discoveryMu.RUnlock()
		return d, nil
	}
	c.discoveryMu.RUnlock()

	// Fetch discovery document
	discoveryURL := strings.TrimSuffix(c.config.Issuer, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, "GET", discoveryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create discovery request: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch discovery document: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("discovery endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var discovery OIDCDiscovery
	if err := json.NewDecoder(resp.Body).Decode(&discovery); err != nil {
		return nil, fmt.Errorf("failed to parse discovery document: %w", err)
	}

	// Cache the discovery document
	c.discoveryMu.Lock()
	c.discovery = &discovery
	c.discoveryAt = time.Now()
	c.discoveryMu.Unlock()

	return &discovery, nil
}

// GetAuthorizationURL returns the URL to redirect users to for authentication
func (c *OIDCClient) GetAuthorizationURL(ctx context.Context, redirectURI, returnURL string) (string, error) {
	// Validate redirect URI
	if !c.isValidRedirectURI(redirectURI) {
		return "", fmt.Errorf("invalid redirect URI: %s", redirectURI)
	}

	// Get discovery document
	discovery, err := c.GetDiscovery(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get discovery: %w", err)
	}

	// Generate state and nonce
	state, err := generateRandomString(32)
	if err != nil {
		return "", fmt.Errorf("failed to generate state: %w", err)
	}
	nonce, err := generateRandomString(32)
	if err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Generate PKCE code verifier and challenge
	var codeVerifier, codeChallenge string
	if c.config.RequirePKCE {
		codeVerifier, err = generateRandomString(64)
		if err != nil {
			return "", fmt.Errorf("failed to generate code verifier: %w", err)
		}
		codeChallenge = generateCodeChallenge(codeVerifier)
	}

	// Store state for validation
	authState := &AuthState{
		State:        state,
		Nonce:        nonce,
		CodeVerifier: codeVerifier,
		RedirectURI:  redirectURI,
		CreatedAt:    time.Now(),
		ReturnURL:    returnURL,
	}
	c.storeState(state, authState)

	// Build authorization URL
	authURL, err := url.Parse(discovery.AuthorizationEndpoint)
	if err != nil {
		return "", fmt.Errorf("invalid authorization endpoint: %w", err)
	}

	params := url.Values{}
	params.Set("client_id", c.config.ClientID)
	params.Set("response_type", "code")
	params.Set("redirect_uri", redirectURI)
	params.Set("state", state)
	params.Set("nonce", nonce)
	params.Set("scope", strings.Join(c.config.Scopes, " "))

	if c.config.RequirePKCE {
		params.Set("code_challenge", codeChallenge)
		params.Set("code_challenge_method", "S256")
	}

	authURL.RawQuery = params.Encode()
	return authURL.String(), nil
}

// ExchangeCode exchanges an authorization code for tokens
func (c *OIDCClient) ExchangeCode(ctx context.Context, code, state, redirectURI string) (*AuthResult, error) {
	// Validate and consume state
	authState, err := c.consumeState(state)
	if err != nil {
		return nil, fmt.Errorf("invalid state: %w", err)
	}

	if !c.isValidRedirectURI(redirectURI) {
		return nil, fmt.Errorf("invalid redirect URI: %s", redirectURI)
	}

	// Verify redirect URI matches
	if authState.RedirectURI != redirectURI {
		return nil, fmt.Errorf("redirect URI mismatch")
	}

	// Get discovery document
	discovery, err := c.GetDiscovery(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get discovery: %w", err)
	}

	// Build token request
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("client_id", c.config.ClientID)
	data.Set("client_secret", c.config.ClientSecret)
	data.Set("code", code)
	data.Set("redirect_uri", redirectURI)

	if authState.CodeVerifier != "" {
		data.Set("code_verifier", authState.CodeVerifier)
	}

	// Exchange code for tokens
	req, err := http.NewRequestWithContext(ctx, "POST", discovery.TokenEndpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange code: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	// Parse ID token to get user claims
	claims, err := c.parseIDToken(tokenResp.IDToken, authState.Nonce)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ID token: %w", err)
	}

	// Get additional user info if needed
	userInfo, err := c.getUserInfo(ctx, tokenResp.AccessToken)
	if err != nil {
		// Log but don't fail - we have basic info from ID token
		fmt.Printf("Warning: failed to get userinfo: %v\n", err)
	}

	// Merge userinfo with ID token claims
	if userInfo != nil {
		if claims.Email == "" {
			claims.Email = userInfo.Email
		}
		if claims.Name == "" {
			claims.Name = userInfo.Name
		}
	}

	// Provision user (find existing or create new)
	result, err := c.provisionUser(ctx, claims, userInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to provision user: %w", err)
	}

	// Create session — use long-lived API token TTL for desktop/mobile sync clients
	// (identified by the seafile:// return URL scheme set during client SSO flow).
	var session *Session
	if strings.HasPrefix(authState.ReturnURL, "seafile://") {
		session, err = c.sessions.CreateAPITokenSession(result.UserID, result.OrgID, result.Email, result.Role)
	} else {
		session, err = c.sessions.CreateSession(result.UserID, result.OrgID, result.Email, result.Role)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	result.SessionToken = session.Token
	result.ExpiresAt = session.ExpiresAt
	result.ReturnURL = authState.ReturnURL

	return result, nil
}

// parseIDToken parses and validates an OIDC ID token with full JWT signature verification.
// It fetches the provider's JWKS keys, verifies the token signature, and validates claims.
func (c *OIDCClient) parseIDToken(idToken, expectedNonce string) (*IDTokenClaims, error) {
	if idToken == "" {
		return nil, fmt.Errorf("empty ID token")
	}

	ctx := context.Background()

	// Parse and verify JWT signature using JWKS
	token, err := jwt.Parse(idToken, func(token *jwt.Token) (interface{}, error) {
		// Validate signing method (only RSA and ECDSA are acceptable for OIDC)
		switch token.Method.(type) {
		case *jwt.SigningMethodRSA:
			// RS256, RS384, RS512
		case *jwt.SigningMethodECDSA:
			// ES256, ES384, ES512
		default:
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}

		// Get kid from header
		kid, _ := token.Header["kid"].(string)
		if kid == "" {
			// Some providers omit kid when they have a single key
			return c.getSingleSigningKey(ctx)
		}

		return c.getSigningKey(ctx, kid)
	}, jwt.WithLeeway(c.config.AllowedClockSkew), jwt.WithExpirationRequired())
	if err != nil {
		return nil, fmt.Errorf("JWT verification failed: %w", err)
	}

	mapClaims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}

	// Convert MapClaims to IDTokenClaims
	claims := &IDTokenClaims{
		Extra: map[string]interface{}(mapClaims),
	}
	if v, ok := mapClaims["iss"].(string); ok {
		claims.Issuer = v
	}
	if v, ok := mapClaims["sub"].(string); ok {
		claims.Subject = v
	}
	// `aud` can be a string or an array of strings per RFC 7519. Normalize to
	// a slice so we can enforce audience below regardless of provider shape.
	var audiences []string
	switch v := mapClaims["aud"].(type) {
	case string:
		claims.Audience = v
		audiences = []string{v}
	case []interface{}:
		for _, item := range v {
			if s, ok := item.(string); ok {
				audiences = append(audiences, s)
			}
		}
		if len(audiences) > 0 {
			claims.Audience = audiences[0]
		}
	}
	if v, ok := mapClaims["exp"].(float64); ok {
		claims.ExpiresAt = int64(v)
	}
	if v, ok := mapClaims["iat"].(float64); ok {
		claims.IssuedAt = int64(v)
	}
	if v, ok := mapClaims["nonce"].(string); ok {
		claims.Nonce = v
	}
	if v, ok := mapClaims["name"].(string); ok {
		claims.Name = v
	}
	if v, ok := mapClaims["given_name"].(string); ok {
		claims.GivenName = v
	}
	if v, ok := mapClaims["family_name"].(string); ok {
		claims.FamilyName = v
	}
	if v, ok := mapClaims["preferred_username"].(string); ok {
		claims.PreferredUsername = v
	}
	if v, ok := mapClaims["picture"].(string); ok {
		claims.Picture = v
	}
	if v, ok := mapClaims["email"].(string); ok {
		claims.Email = v
	}
	if v, ok := mapClaims["email_verified"].(bool); ok {
		claims.EmailVerified = v
	}

	// Validate issuer
	expectedIssuer := strings.TrimSuffix(c.config.Issuer, "/")
	actualIssuer := strings.TrimSuffix(claims.Issuer, "/")
	if actualIssuer != expectedIssuer {
		return nil, fmt.Errorf("issuer mismatch: expected %s, got %s", expectedIssuer, actualIssuer)
	}

	// Validate audience. Per OpenID Connect Core 1.0 §3.1.3.7 step 3, the RP
	// MUST reject an ID token whose `aud` does not list the configured client
	// ID. This prevents tokens minted for a different RP from being replayed
	// against SesameFS. Respect ValidateAudience so deployments can opt out
	// explicitly, but keep the secure default enabled.
	if c.config.ValidateAudience && c.config.ClientID != "" {
		if len(audiences) == 0 {
			return nil, fmt.Errorf("audience mismatch: token is missing a usable aud claim")
		}
		matched := false
		for _, aud := range audiences {
			if aud == c.config.ClientID {
				matched = true
				break
			}
		}
		if !matched {
			return nil, fmt.Errorf("audience mismatch: token aud does not include client ID %s", c.config.ClientID)
		}
	}

	// Validate nonce if provided
	if expectedNonce != "" && claims.Nonce != expectedNonce {
		return nil, fmt.Errorf("nonce mismatch")
	}

	return claims, nil
}

// =============================================================================
// JWKS Key Fetching, Caching, and Rotation
// =============================================================================

// fetchJWKS fetches and parses the JWKS from the provider's jwks_uri endpoint.
func (c *OIDCClient) fetchJWKS(ctx context.Context) error {
	discovery, err := c.GetDiscovery(ctx)
	if err != nil {
		return fmt.Errorf("failed to get discovery for JWKS: %w", err)
	}
	if discovery.JwksURI == "" {
		return fmt.Errorf("no jwks_uri in discovery document")
	}

	req, err := http.NewRequestWithContext(ctx, "GET", discovery.JwksURI, nil)
	if err != nil {
		return fmt.Errorf("failed to create JWKS request: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch JWKS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("JWKS endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var jwks jwksResponse
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return fmt.Errorf("failed to parse JWKS: %w", err)
	}

	keys := make(map[string]crypto.PublicKey)
	for _, k := range jwks.Keys {
		if k.Use != "" && k.Use != "sig" {
			continue // skip encryption keys
		}
		pub, err := parseJWK(k)
		if err != nil {
			continue // skip keys we can't parse
		}
		keys[k.Kid] = pub
	}

	c.jwksMu.Lock()
	c.jwksKeys = keys
	c.jwksAt = time.Now()
	c.jwksMu.Unlock()

	return nil
}

// getSigningKey returns the public key for the given kid.
// If the kid is not in cache, it refreshes the JWKS once to handle key rotation.
func (c *OIDCClient) getSigningKey(ctx context.Context, kid string) (crypto.PublicKey, error) {
	// Try cache first
	c.jwksMu.RLock()
	if c.jwksKeys != nil && time.Since(c.jwksAt) < 1*time.Hour {
		if key, ok := c.jwksKeys[kid]; ok {
			c.jwksMu.RUnlock()
			return key, nil
		}
	}
	c.jwksMu.RUnlock()

	// Key not found or cache expired — refresh JWKS
	if err := c.fetchJWKS(ctx); err != nil {
		return nil, fmt.Errorf("failed to refresh JWKS: %w", err)
	}

	// Try again after refresh
	c.jwksMu.RLock()
	defer c.jwksMu.RUnlock()
	if key, ok := c.jwksKeys[kid]; ok {
		return key, nil
	}

	return nil, fmt.Errorf("signing key with kid %q not found in JWKS", kid)
}

// getSingleSigningKey returns the only key in JWKS when the JWT has no kid header.
func (c *OIDCClient) getSingleSigningKey(ctx context.Context) (crypto.PublicKey, error) {
	c.jwksMu.RLock()
	if c.jwksKeys == nil || time.Since(c.jwksAt) >= 1*time.Hour {
		c.jwksMu.RUnlock()
		if err := c.fetchJWKS(ctx); err != nil {
			return nil, fmt.Errorf("failed to fetch JWKS: %w", err)
		}
		c.jwksMu.RLock()
	}
	defer c.jwksMu.RUnlock()

	if len(c.jwksKeys) == 1 {
		for _, key := range c.jwksKeys {
			return key, nil
		}
	}
	return nil, fmt.Errorf("JWT has no kid and JWKS has %d keys (expected 1)", len(c.jwksKeys))
}

// parseJWK converts a JWK JSON structure into a crypto.PublicKey.
func parseJWK(k jwkKey) (crypto.PublicKey, error) {
	switch k.Kty {
	case "RSA":
		return parseRSAJWK(k)
	case "EC":
		return parseECJWK(k)
	default:
		return nil, fmt.Errorf("unsupported key type: %s", k.Kty)
	}
}

func parseRSAJWK(k jwkKey) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, fmt.Errorf("failed to decode RSA N: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, fmt.Errorf("failed to decode RSA E: %w", err)
	}
	n := new(big.Int).SetBytes(nBytes)
	e := new(big.Int).SetBytes(eBytes)
	return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
}

func parseECJWK(k jwkKey) (*ecdsa.PublicKey, error) {
	var curve elliptic.Curve
	switch k.Crv {
	case "P-256":
		curve = elliptic.P256()
	case "P-384":
		curve = elliptic.P384()
	default:
		return nil, fmt.Errorf("unsupported EC curve: %s", k.Crv)
	}
	xBytes, err := base64.RawURLEncoding.DecodeString(k.X)
	if err != nil {
		return nil, fmt.Errorf("failed to decode EC X: %w", err)
	}
	yBytes, err := base64.RawURLEncoding.DecodeString(k.Y)
	if err != nil {
		return nil, fmt.Errorf("failed to decode EC Y: %w", err)
	}
	return &ecdsa.PublicKey{
		Curve: curve,
		X:     new(big.Int).SetBytes(xBytes),
		Y:     new(big.Int).SetBytes(yBytes),
	}, nil
}

// getUserInfo fetches user information from the userinfo endpoint
func (c *OIDCClient) getUserInfo(ctx context.Context, accessToken string) (*UserInfo, error) {
	discovery, err := c.GetDiscovery(ctx)
	if err != nil {
		return nil, err
	}

	if discovery.UserInfoEndpoint == "" {
		return nil, nil // No userinfo endpoint
	}

	req, err := http.NewRequestWithContext(ctx, "GET", discovery.UserInfoEndpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("userinfo endpoint returned %d", resp.StatusCode)
	}

	var userInfo UserInfo
	if err := json.NewDecoder(resp.Body).Decode(&userInfo); err != nil {
		return nil, err
	}

	return &userInfo, nil
}

// provisionUser finds or creates a user based on OIDC claims
func (c *OIDCClient) provisionUser(ctx context.Context, claims *IDTokenClaims, userInfo *UserInfo) (*AuthResult, error) {
	// Extract org ID from custom claim
	orgID := c.extractOrgID(claims, userInfo)
	if orgID == "" {
		if c.config.DefaultOrgID != "" {
			orgID = c.config.DefaultOrgID
		} else {
			// Create a deterministic org ID from the OIDC subject
			orgID = uuid.NewSHA1(uuid.NameSpaceURL, []byte(c.config.Issuer+"/orgs/default")).String()
		}
	}

	// Extract roles from custom claim
	roles := c.extractRoles(claims, userInfo)
	oidcProvidedRole := len(roles) > 0
	blockedPrivilegedOIDCRole := oidcProvidedRole && isPrivilegedOIDCRoleClaim(roles[0])
	role := c.config.DefaultRole
	if oidcProvidedRole {
		role = c.mapOIDCRole(roles[0])
	}
	role = c.normalizeRoleForOrg(orgID, role)

	// Auto-provision organization if it doesn't exist
	isNewOrg := false
	if c.config.AutoProvision && orgID != "" {
		var existingOrgID string
		orgErr := c.db.Session().Query(`
			SELECT org_id FROM organizations WHERE org_id = ?
		`, orgID).Scan(&existingOrgID)
		if orgErr != nil {
			// Org doesn't exist - create it with the configured default org template.
			isNewOrg = true
			orgName := c.config.DefaultOrgName
			if orgName == "" {
				orgName = "Auto-provisioned Organization"
			}
			now := time.Now()
			template := c.appConfig.GetOrganizationTemplate("")
			createErr := c.createOrganizationWithAdminReadModel(orgID, orgName, template, now)
			if createErr != nil {
				fmt.Printf("Warning: failed to auto-provision org %s: %v\n", orgID, createErr)
				// Continue - the org might have been created concurrently
			}
		}
	}

	// Look up user by OIDC subject
	var userID string
	var oidcOrgID string
	err := c.db.Session().Query(`
		SELECT user_id, org_id FROM users_by_oidc
		WHERE oidc_issuer = ? AND oidc_sub = ?
	`, c.config.Issuer, claims.Subject).Scan(&userID, &oidcOrgID)

	isNewUser := false
	if err != nil {
		// User not found by OIDC sub — check if email is already mapped
		// (e.g., user was promoted to superadmin via make-superadmin.sh)
		email := claims.Email
		if email == "" && userInfo != nil {
			email = userInfo.Email
		}

		var emailUserID, emailOrgID string
		if email != "" {
			emailErr := c.db.Session().Query(`
				SELECT user_id, org_id FROM users_by_email WHERE email = ?
			`, email).Scan(&emailUserID, &emailOrgID)

			if emailErr != nil {
				// users_by_email index missing (user created before dual-write) —
				// fall back to a global scan of the users table and backfill the index.
				iter := c.db.Session().Query(`
					SELECT user_id, org_id FROM users WHERE email = ? ALLOW FILTERING
				`, email).Iter()
				iter.Scan(&emailUserID, &emailOrgID)
				if closeErr := iter.Close(); closeErr == nil && emailUserID != "" {
					// Backfill index so next login is fast
					_ = c.db.Session().Query(`
						INSERT INTO users_by_email (email, user_id, org_id) VALUES (?, ?, ?)
					`, email, emailUserID, emailOrgID).Exec()
					emailErr = nil
				}
			}

			if emailErr == nil && emailUserID != "" {
				// User exists by email — adopt their org and user_id,
				// and create the OIDC mapping so future logins are fast
				userID = emailUserID
				orgID = emailOrgID
				attachedOIDCIdentity := false

				// Check the role in their actual org
				var dbRole string
				if roleErr := c.db.Session().Query(`
					SELECT role FROM users WHERE org_id = ? AND user_id = ?
				`, orgID, userID).Scan(&dbRole); roleErr == nil {
					normalizedDBRole := c.normalizeRoleForOrg(orgID, dbRole)
					if normalizedDBRole != dbRole {
						if attachErr := c.updateUserRoleAttachOIDCIdentityAndAdminReadModels(orgID, userID, email, claims.Subject, normalizedDBRole); attachErr != nil {
							return nil, fmt.Errorf("failed to normalize role and attach OIDC identity: %w", attachErr)
						}
						attachedOIDCIdentity = true
					}
					role = normalizedDBRole
				}

				if !attachedOIDCIdentity {
					if err := c.attachOIDCIdentity(userID, orgID, email, claims.Subject); err != nil {
						return nil, fmt.Errorf("failed to attach OIDC identity: %w", err)
					}
				}

				goto userReady
			}
		}

		// User truly not found — provision new user if enabled
		if !c.config.AutoProvision {
			return nil, fmt.Errorf("user not found and auto-provisioning is disabled")
		}

		isNewUser = true
		userID = uuid.New().String()

		// Determine email and name
		if email == "" {
			email = claims.Subject + "@" + strings.TrimPrefix(c.config.Issuer, "https://")
		}

		name := claims.Name
		if name == "" {
			name = claims.PreferredUsername
		}
		if name == "" && userInfo != nil {
			name = userInfo.Name
		}
		if name == "" {
			name = email
		}

		// First user in a new org gets role=owner
		if isNewOrg {
			role = "owner"
		}

		// Check max_users quota before creating (skip for new orgs — first user is always allowed)
		if !isNewOrg {
			if checker := traffic.GetChecker(); checker != nil {
				if st, _ := checker.CheckMaxUsers(orgID); !st.Allowed {
					return nil, fmt.Errorf("organization user limit reached")
				}
			}
		}

		// Create user record
		if err := c.createUser(ctx, userID, orgID, email, name, role, claims.Subject); err != nil {
			return nil, fmt.Errorf("failed to create user: %w", err)
		}
	} else {
		// Existing user re-login: use the org_id stored in users_by_oidc
		// (this respects make-superadmin.sh which updates users_by_oidc to platform org)
		if oidcOrgID != "" {
			orgID = oidcOrgID
		}

		// Sync role from OIDC when it provides an allowed role claim. Claims that
		// attempt to grant superadmin-like access are ignored for existing users:
		// they must not escalate privileges, but they also must not downgrade a
		// persisted owner/admin role during relogin.
		var dbRole string
		roleErr := c.db.Session().Query(`
			SELECT role FROM users WHERE org_id = ? AND user_id = ?
		`, orgID, userID).Scan(&dbRole)
		if roleErr == nil {
			normalizedDBRole := c.normalizeRoleForOrg(orgID, dbRole)
			if normalizedDBRole != dbRole {
				if updateErr := c.updateUserRoleAndAdminReadModels(orgID, userID, normalizedDBRole); updateErr != nil {
					fmt.Printf("Warning: failed to normalize role from DB: %v\n", updateErr)
				}
				dbRole = normalizedDBRole
			}

			role = c.normalizeRoleForOrg(orgID, role)
			if dbRole == "superadmin" && orgID == c.config.PlatformOrgID {
				// Superadmin promoted via script — preserve their role
				role = dbRole
			} else if blockedPrivilegedOIDCRole {
				// Ignore forbidden superadmin-like claims for existing users.
				role = dbRole
			} else if !oidcProvidedRole {
				// OIDC did not send a role claim — keep the DB role as-is
				role = dbRole
			} else if dbRole != role {
				if updateErr := c.updateUserRoleAndAdminReadModels(orgID, userID, role); updateErr != nil {
					fmt.Printf("Warning: failed to sync role from OIDC: %v\n", updateErr)
				}
			}
		}
	}
userReady:

	// Enforce lifecycle status — reject login for deactivated/deleted users and orgs.
	if !isNewUser {
		var userStatus string
		if err := c.db.Session().Query(
			`SELECT status FROM users WHERE org_id = ? AND user_id = ?`, orgID, userID,
		).Scan(&userStatus); err == nil && userStatus != "" && userStatus != "active" {
			return nil, fmt.Errorf("account %s", userStatus)
		}
		var orgStatus string
		if err := c.db.Session().Query(
			`SELECT status FROM organizations WHERE org_id = ?`, orgID,
		).Scan(&orgStatus); err == nil && orgStatus != "" && orgStatus != "active" {
			return nil, fmt.Errorf("organization %s", orgStatus)
		}
	}

	// Sync group memberships from OIDC claims
	if c.config.SyncGroupsOnLogin {
		groups := c.extractGroups(claims, userInfo)
		if len(groups) > 0 {
			if syncErr := c.syncGroupMembership(ctx, orgID, userID, claims.Email, groups); syncErr != nil {
				fmt.Printf("Warning: failed to sync group memberships: %v\n", syncErr)
			}
		}
	}

	// Sync department memberships from OIDC claims
	if c.config.SyncDeptsOnLogin {
		depts := c.extractDepartments(claims, userInfo)
		if len(depts) > 0 {
			if syncErr := c.syncDepartmentMembership(ctx, orgID, userID, claims.Email, depts); syncErr != nil {
				fmt.Printf("Warning: failed to sync department memberships: %v\n", syncErr)
			}
		}
	}

	// Get user details
	var email, name string
	err = c.db.Session().Query(`
		SELECT email, name FROM users
		WHERE org_id = ? AND user_id = ?
	`, orgID, userID).Scan(&email, &name)
	if err != nil {
		// Use claims data as fallback
		email = claims.Email
		name = claims.Name
	}

	return &AuthResult{
		UserID:    userID,
		OrgID:     orgID,
		Email:     email,
		Name:      name,
		Role:      role,
		IsNewUser: isNewUser,
	}, nil
}

func (c *OIDCClient) attachOIDCIdentity(userID, orgID, email, oidcSub string) error {
	batch := c.db.Session().Batch(gocql.LoggedBatch)
	addAttachOIDCIdentityQueries(batch, c.config.Issuer, userID, orgID, email, oidcSub)
	if err := batch.Exec(); err != nil {
		return fmt.Errorf("failed to persist OIDC identity mapping: %w", err)
	}
	return nil
}

func addAttachOIDCIdentityQueries(batch *gocql.Batch, issuer, userID, orgID, email, oidcSub string) {
	batch.Query(`
		INSERT INTO users_by_oidc (oidc_issuer, oidc_sub, user_id, org_id)
		VALUES (?, ?, ?, ?)
	`, issuer, oidcSub, userID, orgID)
	batch.Query(`
		UPDATE users SET oidc_sub = ? WHERE org_id = ? AND user_id = ?
	`, oidcSub, orgID, userID)
	if email != "" {
		batch.Query(`
			INSERT INTO users_by_email (email, user_id, org_id)
			VALUES (?, ?, ?)
		`, email, userID, orgID)
	}
}

func (c *OIDCClient) createOrganizationWithAdminReadModel(orgID, orgName string, template config.OrganizationTemplate, now time.Time) error {
	periodEnd := template.PeriodEnd(now)
	if err := db.CreateOrganizationWithUsersAndReadModels(c.db.Session(), db.AdminOrganizationWriteSpec{
		OrgID:                  orgID,
		Name:                   orgName,
		Status:                 "active",
		Settings:               template.Settings,
		StorageQuota:           template.StorageQuota,
		StorageUsed:            int64(0),
		ChunkingPolynomial:     template.ChunkingPolynomial,
		StorageConfig:          template.StorageConfig,
		CreatedAt:              now,
		Plan:                   template.Plan,
		QuotaPolicy:            template.QuotaPolicy,
		BillingCycle:           template.BillingCycle,
		TrafficQuota:           template.TrafficQuota,
		TrafficUploadQuota:     template.TrafficUploadQuota,
		TrafficDownloadQuota:   template.TrafficDownloadQuota,
		MaxUsers:               template.MaxUsers,
		CurrentPeriodStartedAt: now,
		CurrentPeriodEndsAt:    periodEnd,
	}, nil); err != nil {
		return fmt.Errorf("failed to create org records: %w", err)
	}
	return nil
}

func (c *OIDCClient) updateUserRoleAndAdminReadModels(orgID, userID, role string) error {
	return db.UpdateUserRoleAndAdminReadModels(c.db.Session(), orgID, userID, role)
}

func (c *OIDCClient) updateUserRoleAttachOIDCIdentityAndAdminReadModels(orgID, userID, email, oidcSub, role string) error {
	return db.UpdateUserRoleAttachOIDCIdentityAndAdminReadModels(c.db.Session(), orgID, userID, email, c.config.Issuer, oidcSub, role)
}

// createUser creates a new user record in the database
func (c *OIDCClient) createUser(ctx context.Context, userID, orgID, email, name, role, oidcSub string) error {
	_ = ctx
	now := time.Now()
	if err := db.CreateUserWithLookupsAndReadModels(c.db.Session(), db.AdminUserWriteSpec{
		OrgID:      orgID,
		UserID:     userID,
		Email:      email,
		Name:       name,
		Role:       role,
		Status:     "active",
		QuotaBytes: int64(-2),
		UsedBytes:  int64(0),
		CreatedAt:  now,
		OIDCIssuer: c.config.Issuer,
		OIDCSub:    oidcSub,
	}); err != nil {
		return fmt.Errorf("failed to create user records: %w", err)
	}

	return nil
}

// extractOrgID extracts the organization ID from OIDC claims.
// If the org claim value matches PlatformOrgClaimValue, returns PlatformOrgID.
func (c *OIDCClient) extractOrgID(claims *IDTokenClaims, userInfo *UserInfo) string {
	if c.config.OrgClaim == "" {
		return ""
	}

	var orgClaimValue string

	// Check ID token extra claims
	if claims.Extra != nil {
		if v, ok := claims.Extra[c.config.OrgClaim].(string); ok {
			orgClaimValue = v
		}
	}

	// Check userinfo as fallback
	if orgClaimValue == "" && userInfo != nil && userInfo.OrgID != "" {
		orgClaimValue = userInfo.OrgID
	}

	if orgClaimValue == "" {
		return ""
	}

	// Map platform org claim value to platform org UUID
	if c.config.PlatformOrgClaimValue != "" && orgClaimValue == c.config.PlatformOrgClaimValue {
		return c.config.PlatformOrgID
	}

	return orgClaimValue
}

// extractRoles extracts roles from OIDC claims
func (c *OIDCClient) extractRoles(claims *IDTokenClaims, userInfo *UserInfo) []string {
	if c.config.RolesClaim == "" {
		return nil
	}

	// Check ID token extra claims
	if claims.Extra != nil {
		if roles, ok := claims.Extra[c.config.RolesClaim].([]interface{}); ok {
			result := make([]string, 0, len(roles))
			for _, r := range roles {
				if s, ok := r.(string); ok {
					result = append(result, s)
				}
			}
			return result
		}
		if role, ok := claims.Extra[c.config.RolesClaim].(string); ok {
			return []string{role}
		}
	}

	// Check userinfo
	if userInfo != nil && len(userInfo.Roles) > 0 {
		return userInfo.Roles
	}

	return nil
}

// mapOIDCRole maps an OIDC role claim to a SesameFS role.
//
// Security (H-4): superadmin is NEVER granted from a claim. Any IdP that can
// mint tokens for a SesameFS user could otherwise take over the platform by
// asserting `role: superadmin`. Superadmin is a DB-only role, bootstrapped
// manually and resolved from the stored user record (see the DB role paths
// that call normalizeRoleForOrg). Claims asking for superadmin are downgraded
// to the configured DefaultRole.
func (c *OIDCClient) mapOIDCRole(oidcRole string) string {
	switch strings.ToLower(oidcRole) {
	case "superadmin", "super_admin", "platform_admin":
		return c.config.DefaultRole
	case "owner", "org_owner":
		return "owner"
	case "admin", "administrator", "tenant_admin":
		return "admin"
	case "user", "member":
		return "user"
	case "readonly", "read-only", "viewer":
		return "readonly"
	case "guest":
		return "guest"
	default:
		return c.config.DefaultRole
	}
}

func isPrivilegedOIDCRoleClaim(oidcRole string) bool {
	switch strings.ToLower(strings.TrimSpace(oidcRole)) {
	case "superadmin", "super_admin", "platform_admin":
		return true
	default:
		return false
	}
}

func (c *OIDCClient) normalizeRoleForOrg(orgID, role string) string {
	if role == string(middleware.RoleSuperAdmin) && orgID != c.config.PlatformOrgID {
		return string(middleware.RoleOwner)
	}
	return role
}

// isValidRedirectURI checks if a redirect URI is in the allowed list
func (c *OIDCClient) isValidRedirectURI(uri string) bool {
	if len(c.config.RedirectURIs) == 0 {
		return false
	}
	for _, allowed := range c.config.RedirectURIs {
		allowed = strings.TrimSpace(allowed)
		if allowed != "" && uri == allowed {
			return true
		}
	}
	return false
}

// storeState stores an auth state for later validation. If the map is at
// capacity even after pruning expired entries, the oldest in-window state is
// evicted. This bounds memory under a state-flooding attack (M-6).
func (c *OIDCClient) storeState(state string, authState *AuthState) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	c.pruneExpiredStatesLocked()

	if len(c.states) >= maxPendingStates {
		// Evict the oldest entry by CreatedAt so honest clients in-flight
		// still succeed as long as they complete within stateTTL.
		var oldestKey string
		var oldestTime time.Time
		for k, v := range c.states {
			if oldestKey == "" || v.CreatedAt.Before(oldestTime) {
				oldestKey = k
				oldestTime = v.CreatedAt
			}
		}
		if oldestKey != "" {
			delete(c.states, oldestKey)
		}
	}

	c.states[state] = authState
}

// consumeState retrieves and removes an auth state
func (c *OIDCClient) consumeState(state string) (*AuthState, error) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	c.pruneExpiredStatesLocked()

	authState, ok := c.states[state]
	if !ok {
		return nil, fmt.Errorf("state not found")
	}

	delete(c.states, state)
	return authState, nil
}

func (c *OIDCClient) pruneExpiredStatesLocked() {
	cutoff := time.Now().Add(-10 * time.Minute)
	for s, as := range c.states {
		if as.CreatedAt.Before(cutoff) {
			delete(c.states, s)
		}
	}
}

// GetLogoutURL returns the URL to redirect users to for logout
func (c *OIDCClient) GetLogoutURL(ctx context.Context, idToken, postLogoutRedirectURI string) (string, error) {
	discovery, err := c.GetDiscovery(ctx)
	if err != nil {
		return "", err
	}

	if discovery.EndSessionEndpoint == "" {
		return "", nil // No logout endpoint
	}

	logoutURL, err := url.Parse(discovery.EndSessionEndpoint)
	if err != nil {
		return "", err
	}

	params := url.Values{}
	params.Set("client_id", c.config.ClientID)
	if idToken != "" {
		params.Set("id_token_hint", idToken)
	}
	if postLogoutRedirectURI != "" {
		params.Set("post_logout_redirect_uri", postLogoutRedirectURI)
	}

	logoutURL.RawQuery = params.Encode()
	return logoutURL.String(), nil
}

// Helper functions

// generateRandomString generates a cryptographically secure random string
func generateRandomString(length int) (string, error) {
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b)[:length], nil
}

// generateCodeChallenge generates a PKCE code challenge from a code verifier
func generateCodeChallenge(verifier string) string {
	// S256: BASE64URL(SHA256(ASCII(code_verifier)))
	h := sha256Sum([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// sha256Sum computes SHA-256 hash
func sha256Sum(data []byte) [32]byte {
	return sha256.Sum256(data)
}

// =============================================================================
// OIDC Group & Department Claim Sync
// =============================================================================

// GroupClaim represents a group membership from OIDC claims.
type GroupClaim struct {
	ID   string `json:"id"`   // External group ID
	Name string `json:"name"` // Group display name
}

// DepartmentClaim represents a department membership from OIDC claims.
type DepartmentClaim struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ParentID string `json:"parent_id,omitempty"`
}

// extractGroups extracts group claims from the ID token or userinfo.
// Supports both array-of-strings and array-of-objects formats.
func (c *OIDCClient) extractGroups(claims *IDTokenClaims, userInfo *UserInfo) []GroupClaim {
	if c.config.GroupsClaim == "" {
		return nil
	}

	raw := c.getClaimValue(claims, c.config.GroupsClaim)
	if raw == nil {
		return nil
	}

	return parseGroupClaims(raw)
}

// extractDepartments extracts department claims from the ID token or userinfo.
// Supports both array-of-strings and array-of-objects formats.
func (c *OIDCClient) extractDepartments(claims *IDTokenClaims, userInfo *UserInfo) []DepartmentClaim {
	if c.config.DepartmentsClaim == "" {
		return nil
	}

	raw := c.getClaimValue(claims, c.config.DepartmentsClaim)
	if raw == nil {
		return nil
	}

	return parseDepartmentClaims(raw)
}

// getClaimValue retrieves a claim value from the ID token extra claims.
func (c *OIDCClient) getClaimValue(claims *IDTokenClaims, claimName string) interface{} {
	if claims.Extra != nil {
		if v, ok := claims.Extra[claimName]; ok {
			return v
		}
	}
	return nil
}

// parseGroupClaims parses a raw claim value into GroupClaim slice.
// Supports: ["group1", "group2"] or [{"id": "abc", "name": "Engineering"}]
func parseGroupClaims(raw interface{}) []GroupClaim {
	arr, ok := raw.([]interface{})
	if !ok {
		return nil
	}

	var groups []GroupClaim
	for _, item := range arr {
		switch v := item.(type) {
		case string:
			groups = append(groups, GroupClaim{ID: v, Name: v})
		case map[string]interface{}:
			gc := GroupClaim{}
			if id, ok := v["id"].(string); ok {
				gc.ID = id
			}
			if name, ok := v["name"].(string); ok {
				gc.Name = name
			}
			if gc.ID == "" && gc.Name != "" {
				gc.ID = gc.Name
			}
			if gc.ID != "" {
				if gc.Name == "" {
					gc.Name = gc.ID
				}
				groups = append(groups, gc)
			}
		}
	}
	return groups
}

// parseDepartmentClaims parses a raw claim value into DepartmentClaim slice.
// Supports: ["dept1", "dept2"] or [{"id": "abc", "name": "Engineering", "parent_id": "xyz"}]
func parseDepartmentClaims(raw interface{}) []DepartmentClaim {
	arr, ok := raw.([]interface{})
	if !ok {
		return nil
	}

	var depts []DepartmentClaim
	for _, item := range arr {
		switch v := item.(type) {
		case string:
			depts = append(depts, DepartmentClaim{ID: v, Name: v})
		case map[string]interface{}:
			dc := DepartmentClaim{}
			if id, ok := v["id"].(string); ok {
				dc.ID = id
			}
			if name, ok := v["name"].(string); ok {
				dc.Name = name
			}
			if pid, ok := v["parent_id"].(string); ok {
				dc.ParentID = pid
			}
			if dc.ID == "" && dc.Name != "" {
				dc.ID = dc.Name
			}
			if dc.ID != "" {
				if dc.Name == "" {
					dc.Name = dc.ID
				}
				depts = append(depts, dc)
			}
		}
	}
	return depts
}

// orgNamespace is a UUID namespace for generating deterministic group UUIDs from OIDC claims.
var orgNamespace = uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8") // URL namespace

// syncGroupMembership syncs a user's group memberships from OIDC claims.
func (c *OIDCClient) syncGroupMembership(ctx context.Context, orgID, userID, email string, groups []GroupClaim) error {
	now := time.Now()
	claimedGroupIDs := make(map[string]bool)

	for _, g := range groups {
		// Generate deterministic UUID from external group ID
		groupUUID := uuid.NewSHA1(orgNamespace, []byte(orgID+":group:"+g.ID))
		groupIDStr := groupUUID.String()
		claimedGroupIDs[groupIDStr] = true

		// Upsert group (INSERT IF NOT EXISTS equivalent - Cassandra uses LWT)
		c.db.Session().Query(`
			INSERT INTO groups (org_id, group_id, name, creator_id, is_department, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?) IF NOT EXISTS
		`, orgID, groupIDStr, g.Name, userID, false, now, now).Exec()

		// Add to groups_by_id lookup (upsert)
		c.db.Session().Query(`
			INSERT INTO groups_by_id (group_id, org_id, name) VALUES (?, ?, ?)
		`, groupIDStr, orgID, g.Name).Exec()

		// Add user to group_members (upsert)
		c.db.Session().Query(`
			INSERT INTO group_members (group_id, user_id, role, added_at)
			VALUES (?, ?, ?, ?)
		`, groupIDStr, userID, "member", now).Exec()

		// Add to lookup table (upsert)
		c.db.Session().Query(`
			INSERT INTO groups_by_member (org_id, user_id, group_id, group_name, role, added_at)
			VALUES (?, ?, ?, ?, ?, ?)
		`, orgID, userID, groupIDStr, g.Name, "member", now).Exec()
	}

	// Full sync: remove from groups not in claims
	if c.config.FullSyncGroups {
		iter := c.db.Session().Query(`
			SELECT group_id FROM groups_by_member WHERE org_id = ? AND user_id = ?
		`, orgID, userID).Iter()

		var existingGroupID string
		for iter.Scan(&existingGroupID) {
			if !claimedGroupIDs[existingGroupID] {
				// Check if this group was OIDC-synced (deterministic UUID pattern)
				// Remove membership
				c.db.Session().Query(`
					DELETE FROM group_members WHERE group_id = ? AND user_id = ?
				`, existingGroupID, userID).Exec()
				c.db.Session().Query(`
					DELETE FROM groups_by_member WHERE org_id = ? AND user_id = ? AND group_id = ?
				`, orgID, userID, existingGroupID).Exec()
			}
		}
		iter.Close()
	}

	return nil
}

// syncDepartmentMembership syncs a user's department memberships from OIDC claims.
func (c *OIDCClient) syncDepartmentMembership(ctx context.Context, orgID, userID, email string, depts []DepartmentClaim) error {
	now := time.Now()
	claimedDeptIDs := make(map[string]bool)

	for _, d := range depts {
		// Generate deterministic UUID from external department ID
		deptUUID := uuid.NewSHA1(orgNamespace, []byte(orgID+":dept:"+d.ID))
		deptIDStr := deptUUID.String()
		claimedDeptIDs[deptIDStr] = true

		// Resolve parent group ID if specified
		var parentGroupIDStr string
		if d.ParentID != "" {
			parentUUID := uuid.NewSHA1(orgNamespace, []byte(orgID+":dept:"+d.ParentID))
			parentGroupIDStr = parentUUID.String()
		}

		// Upsert department as a group with is_department=true
		if parentGroupIDStr != "" {
			c.db.Session().Query(`
				INSERT INTO groups (org_id, group_id, name, creator_id, parent_group_id, is_department, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?) IF NOT EXISTS
			`, orgID, deptIDStr, d.Name, userID, parentGroupIDStr, true, now, now).Exec()
		} else {
			c.db.Session().Query(`
				INSERT INTO groups (org_id, group_id, name, creator_id, is_department, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?) IF NOT EXISTS
			`, orgID, deptIDStr, d.Name, userID, true, now, now).Exec()
		}

		// Add to groups_by_id lookup (upsert)
		c.db.Session().Query(`
			INSERT INTO groups_by_id (group_id, org_id, name) VALUES (?, ?, ?)
		`, deptIDStr, orgID, d.Name).Exec()

		// Add user to group_members
		c.db.Session().Query(`
			INSERT INTO group_members (group_id, user_id, role, added_at)
			VALUES (?, ?, ?, ?)
		`, deptIDStr, userID, "member", now).Exec()

		// Add to lookup table
		c.db.Session().Query(`
			INSERT INTO groups_by_member (org_id, user_id, group_id, group_name, role, added_at)
			VALUES (?, ?, ?, ?, ?, ?)
		`, orgID, userID, deptIDStr, d.Name, "member", now).Exec()
	}

	// Full sync: remove from departments not in claims
	if c.config.FullSyncDepts {
		iter := c.db.Session().Query(`
			SELECT group_id FROM groups_by_member WHERE org_id = ? AND user_id = ?
		`, orgID, userID).Iter()

		var existingGroupID string
		for iter.Scan(&existingGroupID) {
			if !claimedDeptIDs[existingGroupID] {
				// Check if this is a department before removing
				var isDept bool
				if err := c.db.Session().Query(`
					SELECT is_department FROM groups WHERE org_id = ? AND group_id = ?
				`, orgID, existingGroupID).Scan(&isDept); err == nil && isDept {
					c.db.Session().Query(`
						DELETE FROM group_members WHERE group_id = ? AND user_id = ?
					`, existingGroupID, userID).Exec()
					c.db.Session().Query(`
						DELETE FROM groups_by_member WHERE org_id = ? AND user_id = ? AND group_id = ?
					`, orgID, userID, existingGroupID).Exec()
				}
			}
		}
		iter.Close()
	}

	return nil
}
