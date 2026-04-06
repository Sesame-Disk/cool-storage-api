package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrSessionInvalid  = errors.New("invalid session")
	ErrSessionNotFound = errors.New("session not found")
	ErrSessionExpired  = errors.New("session has expired")
	ErrSessionRevoked  = errors.New("session revoked")
)

func IsSessionInvalid(err error) bool {
	return errors.Is(err, ErrSessionInvalid)
}

func IsSessionNotFound(err error) bool {
	return errors.Is(err, ErrSessionNotFound)
}

func IsSessionExpired(err error) bool {
	return errors.Is(err, ErrSessionExpired)
}

func IsSessionRevoked(err error) bool {
	return errors.Is(err, ErrSessionRevoked)
}

// Session represents an authenticated user session
type Session struct {
	Token            string    `json:"token"`
	UserID           string    `json:"user_id"`
	OrgID            string    `json:"org_id"`
	Email            string    `json:"email"`
	Role             string    `json:"role"`
	CreatedAt        time.Time `json:"created_at"`
	ExpiresAt        time.Time `json:"expires_at"`
	APIKeyScope      string    `json:"-"`
	SourceAPIKeyHash string    `json:"-"`
}

// SessionClaims represents the JWT claims for a session token
type SessionClaims struct {
	jwt.RegisteredClaims
	UserID      string `json:"user_id"`
	OrgID       string `json:"org_id"`
	Email       string `json:"email"`
	Role        string `json:"role"`
	APIKeyScope string `json:"api_key_scope,omitempty"`
}

// SessionManager handles session creation and validation
type SessionManager struct {
	config *config.OIDCConfig
	db     *db.DB

	// In-memory session cache for fast validation
	// In production with multiple instances, sessions should be in database
	cacheMu sync.RWMutex
	cache   map[string]*Session
}

// NewSessionManager creates a new session manager
func NewSessionManager(cfg *config.OIDCConfig, database *db.DB) *SessionManager {
	sm := &SessionManager{
		config: cfg,
		db:     database,
		cache:  make(map[string]*Session),
	}

	// Start background cleanup goroutine
	go sm.cleanupLoop()

	return sm
}

// CreateSession creates a new session for a user using the default SessionTTL (web sessions).
func (sm *SessionManager) CreateSession(userID, orgID, email, role string) (*Session, error) {
	return sm.CreateSessionWithTTL(userID, orgID, email, role, sm.config.SessionTTL)
}

// CreateAPITokenSession creates a long-lived session for desktop/mobile sync clients.
// Seafile/SeaDrive clients don't support token refresh, so this uses APITokenTTL (default 180 days).
func (sm *SessionManager) CreateAPITokenSession(userID, orgID, email, role string) (*Session, error) {
	return sm.CreateAPITokenSessionFromAPIKey(userID, orgID, email, role, "", "", nil)
}

// CreateAPITokenSessionFromAPIKey creates a long-lived session derived from an API key exchange.
func (sm *SessionManager) CreateAPITokenSessionFromAPIKey(userID, orgID, email, role, sourceAPIKeyHash, apiKeyScope string, apiKeyExpiresAt *time.Time) (*Session, error) {
	ttl := sm.config.APITokenTTL
	if ttl <= 0 {
		ttl = sm.config.SessionTTL // fallback to session TTL if not configured
	}

	now := time.Now()
	expiresAt := now.Add(ttl)
	if apiKeyExpiresAt != nil && apiKeyExpiresAt.Before(expiresAt) {
		expiresAt = *apiKeyExpiresAt
	}
	if !expiresAt.After(now) {
		return nil, ErrSessionExpired
	}

	return sm.createSessionWithExpiry(userID, orgID, email, role, now, expiresAt, sourceAPIKeyHash, apiKeyScope)
}

// CreateSessionWithTTL creates a new session with a custom TTL.
func (sm *SessionManager) CreateSessionWithTTL(userID, orgID, email, role string, ttl time.Duration) (*Session, error) {
	now := time.Now()
	expiresAt := now.Add(ttl)
	return sm.createSessionWithExpiry(userID, orgID, email, role, now, expiresAt, "", "")
}

func (sm *SessionManager) createSessionWithExpiry(userID, orgID, email, role string, createdAt, expiresAt time.Time, sourceAPIKeyHash, apiKeyScope string) (*Session, error) {
	if !expiresAt.After(createdAt) {
		return nil, ErrSessionExpired
	}

	// Generate session token
	var token string
	var err error

	if sm.config.JWTSigningKey != "" {
		// Create JWT token
		token, err = sm.createJWT(userID, orgID, email, role, apiKeyScope, expiresAt)
		if err != nil {
			return nil, fmt.Errorf("failed to create JWT: %w", err)
		}
	} else {
		// Create random token
		token, err = generateSecureToken(32)
		if err != nil {
			return nil, fmt.Errorf("failed to generate token: %w", err)
		}
	}

	session := &Session{
		Token:            token,
		UserID:           userID,
		OrgID:            orgID,
		Email:            email,
		Role:             role,
		CreatedAt:        createdAt,
		ExpiresAt:        expiresAt,
		APIKeyScope:      apiKeyScope,
		SourceAPIKeyHash: sourceAPIKeyHash,
	}

	// Store session in database
	if sm.db != nil {
		if err := sm.storeSession(session); err != nil {
			return nil, fmt.Errorf("failed to store session: %w", err)
		}
		sm.touchUserLastLogin(userID, orgID, createdAt)
	}

	// Cache session
	sm.cacheMu.Lock()
	sm.cache[token] = session
	sm.cacheMu.Unlock()

	return session, nil
}

// ValidateSession validates a session token and returns the session
func (sm *SessionManager) ValidateSession(token string) (*Session, error) {
	// Check cache first
	sm.cacheMu.RLock()
	session, ok := sm.cache[token]
	sm.cacheMu.RUnlock()

	if ok {
		if time.Now().After(session.ExpiresAt) {
			// Session expired, remove from cache
			sm.cacheMu.Lock()
			delete(sm.cache, token)
			sm.cacheMu.Unlock()
			return nil, ErrSessionExpired
		}

		if sm.config.JWTSigningKey != "" {
			if err := sm.validateRevocableJWTState(token); err != nil {
				if IsSessionRevoked(err) || IsSessionNotFound(err) {
					sm.cacheMu.Lock()
					delete(sm.cache, token)
					sm.cacheMu.Unlock()
				}
				return nil, err
			}
		}
		return session, nil
	}

	// If using JWT, validate the token directly then check DB for revocation
	if sm.config.JWTSigningKey != "" {
		session, err := sm.validateJWT(token)
		if err != nil {
			return nil, err
		}
		if err := sm.validateRevocableJWTState(token); err != nil {
			return nil, err
		}
		// Cache the validated session
		sm.cacheMu.Lock()
		sm.cache[token] = session
		sm.cacheMu.Unlock()
		return session, nil
	}

	// Look up session in database
	if sm.db != nil {
		session, err := sm.loadSession(token)
		if err != nil {
			return nil, err
		}
		if time.Now().After(session.ExpiresAt) {
			return nil, ErrSessionExpired
		}
		// Cache the loaded session
		sm.cacheMu.Lock()
		sm.cache[token] = session
		sm.cacheMu.Unlock()
		return session, nil
	}

	return nil, ErrSessionNotFound
}

func (sm *SessionManager) validateRevocableJWTState(token string) error {
	if sm.db == nil {
		return nil
	}

	if _, err := sm.loadSession(token); err != nil {
		if IsSessionNotFound(err) {
			return ErrSessionRevoked
		}
		return fmt.Errorf("validate revocable session state: %w", err)
	}

	return nil
}

// InvalidateSession invalidates a session token
func (sm *SessionManager) InvalidateSession(token string) error {
	// Remove from cache
	sm.cacheMu.Lock()
	delete(sm.cache, token)
	sm.cacheMu.Unlock()

	// Remove from database
	if sm.db != nil {
		return sm.deleteSession(token)
	}

	return nil
}

// createJWT creates a JWT token for a session
func (sm *SessionManager) createJWT(userID, orgID, email, role, apiKeyScope string, expiresAt time.Time) (string, error) {
	claims := SessionClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "sesamefs",
			Subject:   userID,
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ID:        generateTokenID(),
		},
		UserID:      userID,
		OrgID:       orgID,
		Email:       email,
		Role:        role,
		APIKeyScope: apiKeyScope,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(sm.config.JWTSigningKey))
}

// validateJWT validates a JWT token and returns the session
func (sm *SessionManager) validateJWT(tokenString string) (*Session, error) {
	token, err := jwt.ParseWithClaims(tokenString, &SessionClaims{}, func(token *jwt.Token) (interface{}, error) {
		// Validate signing method
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(sm.config.JWTSigningKey), nil
	})

	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSessionInvalid, err)
	}

	claims, ok := token.Claims.(*SessionClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("%w: invalid token claims", ErrSessionInvalid)
	}

	return &Session{
		Token:       tokenString,
		UserID:      claims.UserID,
		OrgID:       claims.OrgID,
		Email:       claims.Email,
		Role:        claims.Role,
		APIKeyScope: claims.APIKeyScope,
		ExpiresAt:   claims.ExpiresAt.Time,
	}, nil
}

// storeSession stores a session in the database
func (sm *SessionManager) storeSession(session *Session) error {
	// Use a hash of the token as the key to avoid storing raw tokens
	tokenHash := hashToken(session.Token)

	// Use the actual session duration for the Cassandra TTL, not the default SessionTTL.
	// This ensures API token sessions (180 days) get the correct TTL in the database.
	ttlSeconds := int(session.ExpiresAt.Sub(session.CreatedAt).Seconds())
	if ttlSeconds <= 0 {
		ttlSeconds = int(sm.config.SessionTTL.Seconds())
	}

	batch := sm.db.Session().Batch(gocql.LoggedBatch) // atomic: both inserts must succeed together
	batch.Query(`
		INSERT INTO sessions (token_hash, user_id, org_id, email, role, created_at, expires_at, source_api_key_hash, api_key_scope)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		USING TTL ?
	`, tokenHash, session.UserID, session.OrgID, session.Email, session.Role,
		session.CreatedAt, session.ExpiresAt, session.SourceAPIKeyHash, session.APIKeyScope,
		ttlSeconds)
	batch.Query(`
		INSERT INTO sessions_by_user (org_id, user_id, token_hash, source_api_key_hash)
		VALUES (?, ?, ?, ?)
		USING TTL ?
	`, session.OrgID, session.UserID, tokenHash, session.SourceAPIKeyHash, ttlSeconds)
	batch.Query(`
		INSERT INTO sessions_by_org (org_id, token_hash, user_id, source_api_key_hash)
		VALUES (?, ?, ?, ?)
		USING TTL ?
	`, session.OrgID, tokenHash, session.UserID, session.SourceAPIKeyHash, ttlSeconds)
	if session.SourceAPIKeyHash != "" {
		batch.Query(`
			INSERT INTO sessions_by_api_key (api_key_hash, token_hash, org_id, user_id)
			VALUES (?, ?, ?, ?)
			USING TTL ?
		`, session.SourceAPIKeyHash, tokenHash, session.OrgID, session.UserID, ttlSeconds)
	}
	return batch.Exec()
}

func (sm *SessionManager) touchUserLastLogin(userID, orgID string, at time.Time) {
	if sm.db == nil || userID == "" || orgID == "" {
		return
	}
	row, err := db.ReadAdminUserProjectionRow(sm.db.Session(), orgID, userID)
	if err != nil {
		log.Printf("[auth] failed to build admin user projection org=%s user=%s: %v", orgID, userID, err)
		return
	}
	row.LastLoginAt = &at

	batch := sm.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		UPDATE users SET last_login_at = ? WHERE org_id = ? AND user_id = ?
	`, at, orgID, userID)
	db.AddUpsertAdminUserReadModelQuery(batch, row)
	if err := batch.Exec(); err != nil {
		log.Printf("[auth] failed to update last_login_at org=%s user=%s: %v", orgID, userID, err)
	}
}

// loadSession loads a session from the database
func (sm *SessionManager) loadSession(token string) (*Session, error) {
	tokenHash := hashToken(token)

	var session Session
	session.Token = token

	err := sm.db.Session().Query(`
		SELECT user_id, org_id, email, role, created_at, expires_at, source_api_key_hash, api_key_scope
		FROM sessions WHERE token_hash = ?
	`, tokenHash).Scan(&session.UserID, &session.OrgID, &session.Email, &session.Role,
		&session.CreatedAt, &session.ExpiresAt, &session.SourceAPIKeyHash, &session.APIKeyScope)

	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return nil, ErrSessionNotFound
		}
		return nil, fmt.Errorf("load session: %w", err)
	}

	return &session, nil
}

// deleteSession deletes a session from the database (both tables)
func (sm *SessionManager) deleteSession(token string) error {
	tokenHash := hashToken(token)

	// Also need org_id/user_id to delete from sessions_by_user.
	// Read them from the primary table first.
	var orgID, userID, sourceAPIKeyHash string
	if err := sm.db.Session().Query(
		`SELECT org_id, user_id, source_api_key_hash FROM sessions WHERE token_hash = ?`, tokenHash,
	).Scan(&orgID, &userID, &sourceAPIKeyHash); err == nil && orgID != "" {
		batch := sm.db.Session().Batch(gocql.UnloggedBatch)
		batch.Query(
			`DELETE FROM sessions_by_user WHERE org_id = ? AND user_id = ? AND token_hash = ?`,
			orgID, userID, tokenHash,
		)
		batch.Query(
			`DELETE FROM sessions_by_org WHERE org_id = ? AND token_hash = ?`,
			orgID, tokenHash,
		)
		if sourceAPIKeyHash != "" {
			batch.Query(
				`DELETE FROM sessions_by_api_key WHERE api_key_hash = ? AND token_hash = ?`,
				sourceAPIKeyHash, tokenHash,
			)
		}
		batch.Query(`DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
		return batch.Exec()
	}

	return sm.db.Session().Query(`
		DELETE FROM sessions WHERE token_hash = ?
	`, tokenHash).Exec()
}

// InvalidateUserSessions invalidates ALL sessions for a given user.
// Used when a user is deactivated or deleted so the middleware doesn't need
// per-request status checks.
func (sm *SessionManager) InvalidateUserSessions(orgID, userID string) error {
	if sm.db == nil {
		return nil
	}

	// 1. Read all token_hashes from the reverse-index table
	iter := sm.db.Session().Query(
		`SELECT token_hash, source_api_key_hash FROM sessions_by_user WHERE org_id = ? AND user_id = ?`,
		orgID, userID,
	).Iter()

	type sessionRef struct {
		tokenHash        string
		sourceAPIKeyHash string
	}
	var refs []sessionRef
	var ref sessionRef
	for iter.Scan(&ref.tokenHash, &ref.sourceAPIKeyHash) {
		refs = append(refs, ref)
	}
	if err := iter.Close(); err != nil {
		return fmt.Errorf("read sessions_by_user: %w", err)
	}

	if len(refs) == 0 {
		return nil
	}

	// 2. Batch-delete from both tables (chunks of 25)
	for i := 0; i < len(refs); i += 25 {
		end := i + 25
		if end > len(refs) {
			end = len(refs)
		}
		batch := sm.db.Session().Batch(gocql.UnloggedBatch) // deletes are idempotent, no log needed
		for _, item := range refs[i:end] {
			batch.Query(`DELETE FROM sessions WHERE token_hash = ?`, item.tokenHash)
			batch.Query(`DELETE FROM sessions_by_user WHERE org_id = ? AND user_id = ? AND token_hash = ?`,
				orgID, userID, item.tokenHash)
			batch.Query(`DELETE FROM sessions_by_org WHERE org_id = ? AND token_hash = ?`,
				orgID, item.tokenHash)
			if item.sourceAPIKeyHash != "" {
				batch.Query(`DELETE FROM sessions_by_api_key WHERE api_key_hash = ? AND token_hash = ?`,
					item.sourceAPIKeyHash, item.tokenHash)
			}
		}
		if err := batch.Exec(); err != nil {
			return fmt.Errorf("invalidate user sessions batch: %w", err)
		}
	}

	// 3. Evict from in-memory cache (scan for matching user)
	sm.cacheMu.Lock()
	for tok, sess := range sm.cache {
		if sess.UserID == userID && sess.OrgID == orgID {
			delete(sm.cache, tok)
		}
	}
	sm.cacheMu.Unlock()

	return nil
}

// InvalidateAPIKeySessions invalidates all sessions minted from a specific API key.
func (sm *SessionManager) InvalidateAPIKeySessions(apiKeyHash string) error {
	if sm.db == nil || apiKeyHash == "" {
		return nil
	}

	iter := sm.db.Session().Query(
		`SELECT token_hash, org_id, user_id FROM sessions_by_api_key WHERE api_key_hash = ?`,
		apiKeyHash,
	).Iter()

	type apiKeySessionRef struct {
		tokenHash string
		orgID     string
		userID    string
	}
	var refs []apiKeySessionRef
	var ref apiKeySessionRef
	for iter.Scan(&ref.tokenHash, &ref.orgID, &ref.userID) {
		refs = append(refs, ref)
	}
	if err := iter.Close(); err != nil {
		return fmt.Errorf("read sessions_by_api_key: %w", err)
	}

	if len(refs) == 0 {
		return nil
	}

	for i := 0; i < len(refs); i += 25 {
		end := i + 25
		if end > len(refs) {
			end = len(refs)
		}
		batch := sm.db.Session().Batch(gocql.UnloggedBatch)
		for _, item := range refs[i:end] {
			batch.Query(`DELETE FROM sessions WHERE token_hash = ?`, item.tokenHash)
			batch.Query(`DELETE FROM sessions_by_user WHERE org_id = ? AND user_id = ? AND token_hash = ?`,
				item.orgID, item.userID, item.tokenHash)
			batch.Query(`DELETE FROM sessions_by_org WHERE org_id = ? AND token_hash = ?`,
				item.orgID, item.tokenHash)
			batch.Query(`DELETE FROM sessions_by_api_key WHERE api_key_hash = ? AND token_hash = ?`,
				apiKeyHash, item.tokenHash)
		}
		if err := batch.Exec(); err != nil {
			return fmt.Errorf("invalidate api key sessions batch: %w", err)
		}
	}

	sm.cacheMu.Lock()
	for token, sess := range sm.cache {
		if sess.SourceAPIKeyHash == apiKeyHash {
			delete(sm.cache, token)
		}
	}
	sm.cacheMu.Unlock()

	return nil
}

// InvalidateOrgSessions invalidates all active sessions for an organization.
func (sm *SessionManager) InvalidateOrgSessions(orgID string) error {
	if sm.db == nil || orgID == "" {
		return nil
	}

	iter := sm.db.Session().Query(
		`SELECT token_hash, user_id, source_api_key_hash FROM sessions_by_org WHERE org_id = ?`,
		orgID,
	).Iter()

	type orgSessionRef struct {
		tokenHash        string
		userID           string
		sourceAPIKeyHash string
	}
	var refs []orgSessionRef
	var ref orgSessionRef
	for iter.Scan(&ref.tokenHash, &ref.userID, &ref.sourceAPIKeyHash) {
		refs = append(refs, ref)
	}
	if err := iter.Close(); err != nil {
		return fmt.Errorf("read sessions_by_org: %w", err)
	}

	for i := 0; i < len(refs); i += 25 {
		end := i + 25
		if end > len(refs) {
			end = len(refs)
		}
		batch := sm.db.Session().Batch(gocql.UnloggedBatch)
		for _, item := range refs[i:end] {
			batch.Query(`DELETE FROM sessions WHERE token_hash = ?`, item.tokenHash)
			batch.Query(`DELETE FROM sessions_by_org WHERE org_id = ? AND token_hash = ?`,
				orgID, item.tokenHash)
			batch.Query(`DELETE FROM sessions_by_user WHERE org_id = ? AND user_id = ? AND token_hash = ?`,
				orgID, item.userID, item.tokenHash)
			if item.sourceAPIKeyHash != "" {
				batch.Query(`DELETE FROM sessions_by_api_key WHERE api_key_hash = ? AND token_hash = ?`,
					item.sourceAPIKeyHash, item.tokenHash)
			}
		}
		if err := batch.Exec(); err != nil {
			return fmt.Errorf("invalidate org sessions batch: %w", err)
		}
	}

	sm.cacheMu.Lock()
	for token, sess := range sm.cache {
		if sess.OrgID == orgID {
			delete(sm.cache, token)
		}
	}
	sm.cacheMu.Unlock()

	return nil
}

// cleanupLoop periodically cleans up expired sessions from the cache
func (sm *SessionManager) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		sm.cleanupExpiredSessions()
	}
}

// cleanupExpiredSessions removes expired sessions from the cache
func (sm *SessionManager) cleanupExpiredSessions() {
	now := time.Now()

	sm.cacheMu.Lock()
	defer sm.cacheMu.Unlock()

	for token, session := range sm.cache {
		if now.After(session.ExpiresAt) {
			delete(sm.cache, token)
		}
	}
}

// Helper functions

// generateSecureToken generates a cryptographically secure random token
func generateSecureToken(length int) (string, error) {
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// generateTokenID generates a unique token ID
func generateTokenID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// hashToken creates a hash of a token for storage
func hashToken(token string) string {
	h := sha256Sum([]byte(token))
	return hex.EncodeToString(h[:])
}
