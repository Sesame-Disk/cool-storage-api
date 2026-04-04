package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
)

// TestSessionManager_CreateSession tests session creation
func TestSessionManager_CreateSession(t *testing.T) {
	tests := []struct {
		name     string
		config   *config.OIDCConfig
		userID   string
		orgID    string
		email    string
		role     string
		wantErr  bool
		checkJWT bool
	}{
		{
			name: "create session with random token",
			config: &config.OIDCConfig{
				SessionTTL: 24 * time.Hour,
			},
			userID:  "user-123",
			orgID:   "org-456",
			email:   "test@example.com",
			role:    "user",
			wantErr: false,
		},
		{
			name: "create session with JWT",
			config: &config.OIDCConfig{
				SessionTTL:    24 * time.Hour,
				JWTSigningKey: "test-secret-key-at-least-32-chars",
			},
			userID:   "user-123",
			orgID:    "org-456",
			email:    "test@example.com",
			role:     "admin",
			wantErr:  false,
			checkJWT: true,
		},
		{
			name: "create session with short TTL",
			config: &config.OIDCConfig{
				SessionTTL: 1 * time.Minute,
			},
			userID:  "user-789",
			orgID:   "org-012",
			email:   "short@example.com",
			role:    "readonly",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := &SessionManager{
				config: tt.config,
				cache:  make(map[string]*Session),
			}

			session, err := sm.CreateSession(tt.userID, tt.orgID, tt.email, tt.role)

			if (err != nil) != tt.wantErr {
				t.Errorf("CreateSession() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if err != nil {
				return
			}

			// Verify session fields
			if session.UserID != tt.userID {
				t.Errorf("UserID = %v, want %v", session.UserID, tt.userID)
			}
			if session.OrgID != tt.orgID {
				t.Errorf("OrgID = %v, want %v", session.OrgID, tt.orgID)
			}
			if session.Email != tt.email {
				t.Errorf("Email = %v, want %v", session.Email, tt.email)
			}
			if session.Role != tt.role {
				t.Errorf("Role = %v, want %v", session.Role, tt.role)
			}

			// Verify token is not empty
			if session.Token == "" {
				t.Error("Token should not be empty")
			}

			// Verify expiration time
			expectedExpiry := time.Now().Add(tt.config.SessionTTL)
			if session.ExpiresAt.Before(expectedExpiry.Add(-1*time.Second)) ||
				session.ExpiresAt.After(expectedExpiry.Add(1*time.Second)) {
				t.Errorf("ExpiresAt = %v, want approximately %v", session.ExpiresAt, expectedExpiry)
			}

			// Verify session is cached
			sm.cacheMu.RLock()
			cachedSession, ok := sm.cache[session.Token]
			sm.cacheMu.RUnlock()

			if !ok {
				t.Error("Session should be cached")
			}
			if cachedSession.UserID != tt.userID {
				t.Error("Cached session has wrong UserID")
			}

			// If using JWT, verify we can validate the token
			if tt.checkJWT {
				validated, err := sm.validateJWT(session.Token)
				if err != nil {
					t.Errorf("Failed to validate JWT: %v", err)
				}
				if validated.UserID != tt.userID {
					t.Errorf("Validated JWT UserID = %v, want %v", validated.UserID, tt.userID)
				}
			}
		})
	}
}

// TestSessionManager_ValidateSession tests session validation
func TestSessionManager_ValidateSession(t *testing.T) {
	cfg := &config.OIDCConfig{
		SessionTTL: 24 * time.Hour,
	}
	sm := &SessionManager{
		config: cfg,
		cache:  make(map[string]*Session),
	}

	// Create a valid session
	session, err := sm.CreateSession("user-123", "org-456", "test@example.com", "user")
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	t.Run("validate valid session", func(t *testing.T) {
		validated, err := sm.ValidateSession(session.Token)
		if err != nil {
			t.Errorf("ValidateSession() error = %v", err)
			return
		}
		if validated.UserID != session.UserID {
			t.Errorf("UserID = %v, want %v", validated.UserID, session.UserID)
		}
	})

	t.Run("validate invalid token", func(t *testing.T) {
		_, err := sm.ValidateSession("invalid-token-12345")
		if err == nil {
			t.Error("ValidateSession() should fail for invalid token")
		}
		if !errors.Is(err, ErrSessionNotFound) {
			t.Errorf("ValidateSession() error = %v, want ErrSessionNotFound", err)
		}
	})

	t.Run("validate expired session", func(t *testing.T) {
		// Manually add an expired session to cache
		expiredSession := &Session{
			Token:     "expired-token",
			UserID:    "user-expired",
			OrgID:     "org-expired",
			Email:     "expired@example.com",
			Role:      "user",
			ExpiresAt: time.Now().Add(-1 * time.Hour),
		}
		sm.cacheMu.Lock()
		sm.cache["expired-token"] = expiredSession
		sm.cacheMu.Unlock()

		_, err := sm.ValidateSession("expired-token")
		if err == nil {
			t.Error("ValidateSession() should fail for expired session")
		}
		if !errors.Is(err, ErrSessionExpired) {
			t.Errorf("ValidateSession() error = %v, want ErrSessionExpired", err)
		}
	})
}

func TestSessionErrorHelpers(t *testing.T) {
	if !IsSessionInvalid(ErrSessionInvalid) {
		t.Error("IsSessionInvalid should detect ErrSessionInvalid")
	}
	if !IsSessionNotFound(ErrSessionNotFound) {
		t.Error("IsSessionNotFound should detect ErrSessionNotFound")
	}
	if !IsSessionExpired(ErrSessionExpired) {
		t.Error("IsSessionExpired should detect ErrSessionExpired")
	}
	if !IsSessionRevoked(ErrSessionRevoked) {
		t.Error("IsSessionRevoked should detect ErrSessionRevoked")
	}
}

// TestSessionManager_ValidateSession_JWT tests JWT validation
func TestSessionManager_ValidateSession_JWT(t *testing.T) {
	cfg := &config.OIDCConfig{
		SessionTTL:    24 * time.Hour,
		JWTSigningKey: "test-secret-key-at-least-32-chars-long",
	}
	sm := &SessionManager{
		config: cfg,
		cache:  make(map[string]*Session),
	}

	session, err := sm.CreateSession("user-jwt", "org-jwt", "jwt@example.com", "admin")
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Clear cache to force JWT validation
	sm.cacheMu.Lock()
	delete(sm.cache, session.Token)
	sm.cacheMu.Unlock()

	t.Run("validate JWT from token string", func(t *testing.T) {
		validated, err := sm.ValidateSession(session.Token)
		if err != nil {
			t.Errorf("ValidateSession() error = %v", err)
			return
		}
		if validated.UserID != "user-jwt" {
			t.Errorf("UserID = %v, want user-jwt", validated.UserID)
		}
		if validated.Role != "admin" {
			t.Errorf("Role = %v, want admin", validated.Role)
		}
	})

	t.Run("reject tampered JWT", func(t *testing.T) {
		// Tamper with the token
		tamperedToken := session.Token + "tampered"
		_, err := sm.ValidateSession(tamperedToken)
		if err == nil {
			t.Error("ValidateSession() should reject tampered JWT")
		}
	})

	t.Run("reject JWT with wrong signing key", func(t *testing.T) {
		// Create a new session manager with different key
		sm2 := &SessionManager{
			config: &config.OIDCConfig{
				SessionTTL:    24 * time.Hour,
				JWTSigningKey: "different-secret-key-at-least-32-chars",
			},
			cache: make(map[string]*Session),
		}

		_, err := sm2.ValidateSession(session.Token)
		if err == nil {
			t.Error("ValidateSession() should reject JWT with wrong key")
		}
	})
}

// TestSessionManager_InvalidateSession tests session invalidation
func TestSessionManager_InvalidateSession(t *testing.T) {
	cfg := &config.OIDCConfig{
		SessionTTL: 24 * time.Hour,
	}
	sm := &SessionManager{
		config: cfg,
		cache:  make(map[string]*Session),
	}

	// Create a session
	session, err := sm.CreateSession("user-123", "org-456", "test@example.com", "user")
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Verify it exists
	_, err = sm.ValidateSession(session.Token)
	if err != nil {
		t.Errorf("Session should be valid before invalidation: %v", err)
	}

	// Invalidate the session
	err = sm.InvalidateSession(session.Token)
	if err != nil {
		t.Errorf("InvalidateSession() error = %v", err)
	}

	// Verify it's removed from cache
	sm.cacheMu.RLock()
	_, exists := sm.cache[session.Token]
	sm.cacheMu.RUnlock()

	if exists {
		t.Error("Session should be removed from cache after invalidation")
	}
}

// TestSessionManager_CleanupExpiredSessions tests expired session cleanup
func TestSessionManager_CleanupExpiredSessions(t *testing.T) {
	cfg := &config.OIDCConfig{
		SessionTTL: 24 * time.Hour,
	}
	sm := &SessionManager{
		config: cfg,
		cache:  make(map[string]*Session),
	}

	// Add a mix of valid and expired sessions
	sm.cacheMu.Lock()
	sm.cache["valid-1"] = &Session{
		Token:     "valid-1",
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}
	sm.cache["valid-2"] = &Session{
		Token:     "valid-2",
		ExpiresAt: time.Now().Add(2 * time.Hour),
	}
	sm.cache["expired-1"] = &Session{
		Token:     "expired-1",
		ExpiresAt: time.Now().Add(-1 * time.Hour),
	}
	sm.cache["expired-2"] = &Session{
		Token:     "expired-2",
		ExpiresAt: time.Now().Add(-30 * time.Minute),
	}
	sm.cacheMu.Unlock()

	// Run cleanup
	sm.cleanupExpiredSessions()

	// Verify results
	sm.cacheMu.RLock()
	defer sm.cacheMu.RUnlock()

	if len(sm.cache) != 2 {
		t.Errorf("Expected 2 sessions after cleanup, got %d", len(sm.cache))
	}

	if _, ok := sm.cache["valid-1"]; !ok {
		t.Error("valid-1 should still exist")
	}
	if _, ok := sm.cache["valid-2"]; !ok {
		t.Error("valid-2 should still exist")
	}
	if _, ok := sm.cache["expired-1"]; ok {
		t.Error("expired-1 should be removed")
	}
	if _, ok := sm.cache["expired-2"]; ok {
		t.Error("expired-2 should be removed")
	}
}

// TestSessionManager_CreateAPITokenSession tests long-lived API token session creation
func TestSessionManager_CreateAPITokenSession(t *testing.T) {
	cfg := &config.OIDCConfig{
		SessionTTL:  24 * time.Hour,
		APITokenTTL: 180 * 24 * time.Hour,
	}
	sm := &SessionManager{
		config: cfg,
		cache:  make(map[string]*Session),
	}

	session, err := sm.CreateAPITokenSession("user-api", "org-api", "api@example.com", "user")
	if err != nil {
		t.Fatalf("CreateAPITokenSession() error = %v", err)
	}

	// API token session should expire in ~180 days, not 24h
	expectedExpiry := time.Now().Add(180 * 24 * time.Hour)
	if session.ExpiresAt.Before(expectedExpiry.Add(-1*time.Minute)) ||
		session.ExpiresAt.After(expectedExpiry.Add(1*time.Minute)) {
		t.Errorf("ExpiresAt = %v, want approximately %v (180 days)", session.ExpiresAt, expectedExpiry)
	}

	// Verify it's much longer than the web session TTL
	webSession, err := sm.CreateSession("user-web", "org-web", "web@example.com", "user")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	apiDuration := session.ExpiresAt.Sub(session.CreatedAt)
	webDuration := webSession.ExpiresAt.Sub(webSession.CreatedAt)
	if apiDuration <= webDuration {
		t.Errorf("API token duration (%v) should be longer than web session duration (%v)", apiDuration, webDuration)
	}
	if session.SourceAPIKeyHash != "" {
		t.Errorf("SourceAPIKeyHash = %q, want empty for non-derived API token sessions", session.SourceAPIKeyHash)
	}
}

func TestSessionManager_CreateAPITokenSessionFromAPIKey(t *testing.T) {
	cfg := &config.OIDCConfig{
		SessionTTL:  24 * time.Hour,
		APITokenTTL: 180 * 24 * time.Hour,
	}
	sm := &SessionManager{
		config: cfg,
		cache:  make(map[string]*Session),
	}

	session, err := sm.CreateAPITokenSessionFromAPIKey("user-api", "org-api", "api@example.com", "user", "hash-123", "read-write", nil)
	if err != nil {
		t.Fatalf("CreateAPITokenSessionFromAPIKey() error = %v", err)
	}
	if session.SourceAPIKeyHash != "hash-123" {
		t.Fatalf("SourceAPIKeyHash = %q, want %q", session.SourceAPIKeyHash, "hash-123")
	}
	if session.APIKeyScope != "read-write" {
		t.Fatalf("APIKeyScope = %q, want %q", session.APIKeyScope, "read-write")
	}
}

func TestSessionManager_CreateAPITokenSessionFromAPIKey_InheritsShorterKeyExpiry(t *testing.T) {
	cfg := &config.OIDCConfig{
		SessionTTL:  24 * time.Hour,
		APITokenTTL: 180 * 24 * time.Hour,
	}
	sm := &SessionManager{
		config: cfg,
		cache:  make(map[string]*Session),
	}

	apiKeyExpiresAt := time.Now().Add(2 * time.Hour)
	session, err := sm.CreateAPITokenSessionFromAPIKey("user-api", "org-api", "api@example.com", "user", "hash-123", "read-write", &apiKeyExpiresAt)
	if err != nil {
		t.Fatalf("CreateAPITokenSessionFromAPIKey() error = %v", err)
	}
	if session.ExpiresAt.After(apiKeyExpiresAt.Add(100 * time.Millisecond)) {
		t.Fatalf("session.ExpiresAt = %v, want <= apiKeyExpiresAt %v", session.ExpiresAt, apiKeyExpiresAt)
	}
	if session.ExpiresAt.Before(apiKeyExpiresAt.Add(-1 * time.Second)) {
		t.Fatalf("session.ExpiresAt = %v, want close to apiKeyExpiresAt %v", session.ExpiresAt, apiKeyExpiresAt)
	}
}

// TestSessionManager_CreateSessionWithTTL tests custom TTL session creation
func TestSessionManager_CreateSessionWithTTL(t *testing.T) {
	cfg := &config.OIDCConfig{
		SessionTTL:  24 * time.Hour,
		APITokenTTL: 180 * 24 * time.Hour,
	}
	sm := &SessionManager{
		config: cfg,
		cache:  make(map[string]*Session),
	}

	customTTL := 7 * 24 * time.Hour // 7 days
	session, err := sm.CreateSessionWithTTL("user-custom", "org-custom", "custom@example.com", "user", customTTL)
	if err != nil {
		t.Fatalf("CreateSessionWithTTL() error = %v", err)
	}

	// Session should expire in ~7 days
	expectedExpiry := time.Now().Add(customTTL)
	if session.ExpiresAt.Before(expectedExpiry.Add(-1*time.Second)) ||
		session.ExpiresAt.After(expectedExpiry.Add(1*time.Second)) {
		t.Errorf("ExpiresAt = %v, want approximately %v (7 days)", session.ExpiresAt, expectedExpiry)
	}

	// Verify CreatedAt-based duration matches the custom TTL
	actualDuration := session.ExpiresAt.Sub(session.CreatedAt)
	if actualDuration < customTTL-time.Second || actualDuration > customTTL+time.Second {
		t.Errorf("Session duration = %v, want approximately %v", actualDuration, customTTL)
	}
}

// TestSessionManager_StoreSessionTTL verifies storeSession derives TTL from session duration
func TestSessionManager_StoreSessionTTL(t *testing.T) {
	cfg := &config.OIDCConfig{
		SessionTTL:  24 * time.Hour,
		APITokenTTL: 180 * 24 * time.Hour,
	}
	sm := &SessionManager{
		config: cfg,
		cache:  make(map[string]*Session),
	}

	// Create API token session (180 days)
	apiSession, err := sm.CreateAPITokenSession("user-store", "org-store", "store@example.com", "user")
	if err != nil {
		t.Fatalf("CreateAPITokenSession() error = %v", err)
	}

	// Verify the session's ExpiresAt-CreatedAt duration matches APITokenTTL
	ttlSeconds := int(apiSession.ExpiresAt.Sub(apiSession.CreatedAt).Seconds())
	expectedSeconds := int((180 * 24 * time.Hour).Seconds())
	tolerance := 2 // 2 second tolerance
	if ttlSeconds < expectedSeconds-tolerance || ttlSeconds > expectedSeconds+tolerance {
		t.Errorf("Cassandra TTL would be %d seconds, want approximately %d seconds (180 days)", ttlSeconds, expectedSeconds)
	}

	// Create web session (24h) and verify different TTL
	webSession, err := sm.CreateSession("user-web2", "org-web2", "web2@example.com", "user")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	webTTLSeconds := int(webSession.ExpiresAt.Sub(webSession.CreatedAt).Seconds())
	webExpectedSeconds := int((24 * time.Hour).Seconds())
	if webTTLSeconds < webExpectedSeconds-tolerance || webTTLSeconds > webExpectedSeconds+tolerance {
		t.Errorf("Web session Cassandra TTL would be %d seconds, want approximately %d seconds (24h)", webTTLSeconds, webExpectedSeconds)
	}

	// API TTL should be significantly larger than web TTL
	if ttlSeconds <= webTTLSeconds*2 {
		t.Errorf("API TTL (%d) should be much larger than web TTL (%d)", ttlSeconds, webTTLSeconds)
	}
}

// TestSessionManager_CreateAPITokenSession_Fallback tests fallback when APITokenTTL is not set
func TestSessionManager_CreateAPITokenSession_Fallback(t *testing.T) {
	cfg := &config.OIDCConfig{
		SessionTTL: 24 * time.Hour,
		// APITokenTTL not set (zero value)
	}
	sm := &SessionManager{
		config: cfg,
		cache:  make(map[string]*Session),
	}

	session, err := sm.CreateAPITokenSession("user-fb", "org-fb", "fb@example.com", "user")
	if err != nil {
		t.Fatalf("CreateAPITokenSession() error = %v", err)
	}

	// Should fall back to SessionTTL (24h)
	expectedExpiry := time.Now().Add(24 * time.Hour)
	if session.ExpiresAt.Before(expectedExpiry.Add(-1*time.Second)) ||
		session.ExpiresAt.After(expectedExpiry.Add(1*time.Second)) {
		t.Errorf("ExpiresAt = %v, want approximately %v (fallback to 24h)", session.ExpiresAt, expectedExpiry)
	}
}

// TestGenerateSecureToken tests secure token generation
func TestGenerateSecureToken(t *testing.T) {
	tests := []struct {
		name   string
		length int
	}{
		{"16 byte token", 16},
		{"32 byte token", 32},
		{"64 byte token", 64},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token, err := generateSecureToken(tt.length)
			if err != nil {
				t.Errorf("generateSecureToken() error = %v", err)
				return
			}

			// Token should be hex encoded, so length should be 2x
			expectedLen := tt.length * 2
			if len(token) != expectedLen {
				t.Errorf("token length = %d, want %d", len(token), expectedLen)
			}

			// Generate another token and verify they're different
			token2, _ := generateSecureToken(tt.length)
			if token == token2 {
				t.Error("Tokens should be unique")
			}
		})
	}
}

// TestHashToken tests token hashing
func TestHashToken(t *testing.T) {
	t.Run("hash produces consistent output", func(t *testing.T) {
		token := "test-token-12345"
		hash1 := hashToken(token)
		hash2 := hashToken(token)

		if hash1 != hash2 {
			t.Error("Same token should produce same hash")
		}
	})

	t.Run("different tokens produce different hashes", func(t *testing.T) {
		hash1 := hashToken("token-1")
		hash2 := hashToken("token-2")

		if hash1 == hash2 {
			t.Error("Different tokens should produce different hashes")
		}
	})

	t.Run("hash is hex encoded", func(t *testing.T) {
		hash := hashToken("any-token")
		// SHA-256 produces 64 hex characters
		if len(hash) != 64 {
			t.Errorf("Hash length = %d, want 64", len(hash))
		}
	})
}

// TestSessionManager_ConcurrentCreateValidate tests concurrent session operations
func TestSessionManager_ConcurrentCreateValidate(t *testing.T) {
	cfg := &config.OIDCConfig{
		SessionTTL:    24 * time.Hour,
		JWTSigningKey: "test-secret-key-at-least-32-chars-long",
	}
	sm := &SessionManager{
		config: cfg,
		cache:  make(map[string]*Session),
	}

	done := make(chan struct{})
	const numGoroutines = 50

	// Concurrently create and validate sessions
	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer func() { done <- struct{}{} }()

			session, err := sm.CreateSession(
				"user-"+string(rune('0'+idx%10)),
				"org-1",
				"test@example.com",
				"user",
			)
			if err != nil {
				t.Errorf("CreateSession failed: %v", err)
				return
			}

			// Validate the created session
			validated, err := sm.ValidateSession(session.Token)
			if err != nil {
				t.Errorf("ValidateSession failed: %v", err)
				return
			}
			if validated.Token != session.Token {
				t.Errorf("Token mismatch after validation")
			}
		}(i)
	}

	for i := 0; i < numGoroutines; i++ {
		<-done
	}
}

// TestSessionManager_ConcurrentCleanup tests cleanup under concurrent access
func TestSessionManager_ConcurrentCleanup(t *testing.T) {
	cfg := &config.OIDCConfig{
		SessionTTL: 24 * time.Hour,
	}
	sm := &SessionManager{
		config: cfg,
		cache:  make(map[string]*Session),
	}

	// Add mix of valid and expired sessions
	sm.cacheMu.Lock()
	for i := 0; i < 100; i++ {
		token := "token-" + string(rune('A'+i%26)) + string(rune('0'+i%10))
		expiry := time.Now().Add(1 * time.Hour)
		if i%3 == 0 {
			expiry = time.Now().Add(-1 * time.Hour)
		}
		sm.cache[token] = &Session{
			Token:     token,
			ExpiresAt: expiry,
		}
	}
	sm.cacheMu.Unlock()

	// Run cleanup concurrently with reads
	done := make(chan struct{})
	go func() {
		sm.cleanupExpiredSessions()
		done <- struct{}{}
	}()
	go func() {
		sm.cacheMu.RLock()
		_ = len(sm.cache)
		sm.cacheMu.RUnlock()
		done <- struct{}{}
	}()

	<-done
	<-done

	// Verify no expired sessions remain
	sm.cacheMu.RLock()
	defer sm.cacheMu.RUnlock()
	for token, session := range sm.cache {
		if time.Now().After(session.ExpiresAt) {
			t.Errorf("expired session %s still in cache", token)
		}
	}
}

// TestSessionManager_InvalidateNonExistent tests invalidating a session that doesn't exist
func TestSessionManager_InvalidateNonExistent(t *testing.T) {
	cfg := &config.OIDCConfig{
		SessionTTL: 24 * time.Hour,
	}
	sm := &SessionManager{
		config: cfg,
		cache:  make(map[string]*Session),
	}

	// Should not error when invalidating non-existent session
	err := sm.InvalidateSession("non-existent-token")
	if err != nil {
		t.Errorf("InvalidateSession should not error for non-existent token: %v", err)
	}
}

// TestGenerateTokenID tests token ID generation uniqueness
func TestGenerateTokenID(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := generateTokenID()
		if id == "" {
			t.Error("generateTokenID returned empty string")
		}
		if seen[id] {
			t.Errorf("duplicate token ID generated: %s", id)
		}
		seen[id] = true
	}
}

// TestCreateJWT tests JWT creation
func TestCreateJWT(t *testing.T) {
	cfg := &config.OIDCConfig{
		JWTSigningKey: "test-secret-key-at-least-32-chars-long",
	}
	sm := &SessionManager{
		config: cfg,
		cache:  make(map[string]*Session),
	}

	expiresAt := time.Now().Add(24 * time.Hour)
	token, err := sm.createJWT("user-123", "org-456", "test@example.com", "admin", "admin", expiresAt)
	if err != nil {
		t.Fatalf("createJWT() error = %v", err)
	}

	// JWT should have 3 parts separated by dots
	parts := len(token)
	if parts < 100 { // JWT is typically at least 100 chars
		t.Error("JWT seems too short")
	}

	// Validate the JWT
	session, err := sm.validateJWT(token)
	if err != nil {
		t.Errorf("validateJWT() error = %v", err)
	}

	if session.UserID != "user-123" {
		t.Errorf("UserID = %v, want user-123", session.UserID)
	}
	if session.OrgID != "org-456" {
		t.Errorf("OrgID = %v, want org-456", session.OrgID)
	}
	if session.Email != "test@example.com" {
		t.Errorf("Email = %v, want test@example.com", session.Email)
	}
	if session.Role != "admin" {
		t.Errorf("Role = %v, want admin", session.Role)
	}
	if session.APIKeyScope != "admin" {
		t.Errorf("APIKeyScope = %v, want admin", session.APIKeyScope)
	}
}
