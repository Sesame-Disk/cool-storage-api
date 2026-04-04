// Package apikeys provides unified API key management for user API access,
// Accounts M2M authentication, and Seafile legacy client compatibility.
//
// Tokens are prefixed with "sk_" and stored as SHA-256 hashes only.
// The raw token is returned once on creation and never persisted.
package apikeys

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

const (
	ScopeRead      = "read"
	ScopeReadWrite = "read-write"
	ScopeAdmin     = "admin"

	tokenPrefix    = "sk_"
	tokenBytes     = 32 // 32 bytes = 64 hex chars → "sk_" + 64 = 67 chars total
	prefixLen      = 8  // visible prefix for identification: "sk_a3f8b2c1"
	maxKeysPerUser = 20

	cacheTTL = 5 * time.Minute
)

var (
	ErrKeyNotFound     = errors.New("api key not found")
	ErrKeyExpired      = errors.New("api key expired")
	ErrInvalidExpiry   = errors.New("invalid api key expiry")
	ErrKeyLimitReached = errors.New("maximum api keys per user reached")
	ErrInvalidScope    = errors.New("invalid scope")
	ErrNotOwner        = errors.New("api key does not belong to this user")
)

// APIKey represents a stored API key (without the raw token).
type APIKey struct {
	KeyHash    string     `json:"key_hash"`
	KeyPrefix  string     `json:"key_prefix"`
	UserID     gocql.UUID `json:"user_id"`
	OrgID      gocql.UUID `json:"org_id"`
	Label      string     `json:"label"`
	Scope      string     `json:"scope"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

type cacheEntry struct {
	key      *APIKey
	cachedAt time.Time
}

// Manager handles API key creation, validation, and revocation.
type Manager struct {
	db *db.DB

	cacheMu sync.RWMutex
	cache   map[string]*cacheEntry // keyed by key_hash
}

// NewManager creates a new API key manager.
func NewManager(database *db.DB) *Manager {
	m := &Manager{
		db:    database,
		cache: make(map[string]*cacheEntry),
	}
	go m.cleanupLoop()
	return m
}

// ValidScopes returns the set of valid scope values.
func ValidScopes() []string {
	return []string{ScopeRead, ScopeReadWrite, ScopeAdmin}
}

// IsValidScope checks if a scope string is valid.
func IsValidScope(scope string) bool {
	switch scope {
	case ScopeRead, ScopeReadWrite, ScopeAdmin:
		return true
	}
	return false
}

// ScopeAllows checks if keyScope grants access to the required scope level.
// Hierarchy: admin ⊃ read-write ⊃ read.
func ScopeAllows(keyScope, required string) bool {
	switch required {
	case ScopeRead:
		return keyScope == ScopeRead || keyScope == ScopeReadWrite || keyScope == ScopeAdmin
	case ScopeReadWrite:
		return keyScope == ScopeReadWrite || keyScope == ScopeAdmin
	case ScopeAdmin:
		return keyScope == ScopeAdmin
	default:
		return false
	}
}

// CreateKey generates a new API key, stores its hash, and returns the raw token once.
func (m *Manager) CreateKey(userID, orgID gocql.UUID, label, scope string, expiresAt *time.Time) (string, *APIKey, error) {
	if !IsValidScope(scope) {
		return "", nil, ErrInvalidScope
	}

	// Enforce per-user limit.
	count, err := m.countUserKeys(orgID, userID)
	if err != nil {
		return "", nil, fmt.Errorf("count user keys: %w", err)
	}
	if count >= maxKeysPerUser {
		return "", nil, ErrKeyLimitReached
	}

	// Generate random token.
	rawBytes := make([]byte, tokenBytes)
	if _, err := rand.Read(rawBytes); err != nil {
		return "", nil, fmt.Errorf("generate token: %w", err)
	}
	rawToken := tokenPrefix + hex.EncodeToString(rawBytes)
	keyHash := HashToken(rawToken)
	keyPrefix := rawToken[:len(tokenPrefix)+prefixLen] + "..."

	now := time.Now().UTC()
	ttlSeconds, err := ttlFromExpiry(now, expiresAt)
	if err != nil {
		return "", nil, err
	}

	key := &APIKey{
		KeyHash:   keyHash,
		KeyPrefix: keyPrefix,
		UserID:    userID,
		OrgID:     orgID,
		Label:     label,
		Scope:     scope,
		CreatedAt: now,
		ExpiresAt: expiresAt,
	}

	// Dual-write: primary + reverse index in a logged batch.
	batch := m.db.Session().Batch(gocql.LoggedBatch)
	if ttlSeconds > 0 {
		batch.Query(`
			INSERT INTO api_keys (key_hash, key_prefix, user_id, org_id, label, scope, created_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			USING TTL ?
		`, keyHash, keyPrefix, userID, orgID, label, scope, now, expiresAt, ttlSeconds)
		batch.Query(`
			INSERT INTO api_keys_by_user (org_id, user_id, key_hash, key_prefix, label, scope, created_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			USING TTL ?
		`, orgID, userID, keyHash, keyPrefix, label, scope, now, expiresAt, ttlSeconds)
	} else {
		batch.Query(`
			INSERT INTO api_keys (key_hash, key_prefix, user_id, org_id, label, scope, created_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, keyHash, keyPrefix, userID, orgID, label, scope, now, expiresAt)
		batch.Query(`
			INSERT INTO api_keys_by_user (org_id, user_id, key_hash, key_prefix, label, scope, created_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, orgID, userID, keyHash, keyPrefix, label, scope, now, expiresAt)
	}

	if err := batch.Exec(); err != nil {
		return "", nil, fmt.Errorf("store api key: %w", err)
	}

	return rawToken, key, nil
}

// ValidateKey validates a raw token and returns the associated API key.
// Returns ErrKeyNotFound if the token doesn't exist, ErrKeyExpired if expired.
func (m *Manager) ValidateKey(rawToken string) (*APIKey, error) {
	keyHash := HashToken(rawToken)

	// Check cache first.
	m.cacheMu.RLock()
	if entry, ok := m.cache[keyHash]; ok && time.Since(entry.cachedAt) < cacheTTL {
		m.cacheMu.RUnlock()
		if err := checkExpiry(entry.key); err != nil {
			return nil, err
		}
		go m.updateLastUsed(entry.key)
		return entry.key, nil
	}
	m.cacheMu.RUnlock()

	// Cache miss — query DB.
	key := &APIKey{KeyHash: keyHash}
	err := m.db.Session().Query(`
		SELECT key_prefix, user_id, org_id, label, scope, created_at, last_used_at, expires_at
		FROM api_keys WHERE key_hash = ?
	`, keyHash).Scan(
		&key.KeyPrefix, &key.UserID, &key.OrgID, &key.Label, &key.Scope,
		&key.CreatedAt, &key.LastUsedAt, &key.ExpiresAt,
	)
	if err != nil {
		return nil, ErrKeyNotFound
	}

	if err := checkExpiry(key); err != nil {
		return nil, err
	}

	// Cache the result.
	m.cacheMu.Lock()
	m.cache[keyHash] = &cacheEntry{key: key, cachedAt: time.Now()}
	m.cacheMu.Unlock()

	go m.updateLastUsed(key)
	return key, nil
}

// RevokeKey deletes an API key. Verifies ownership before deletion.
func (m *Manager) RevokeKey(orgID, userID gocql.UUID, keyHash string) error {
	key, err := m.GetOwnedKey(orgID, userID, keyHash)
	if err != nil {
		return err
	}

	// Delete from both tables.
	batch := m.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`DELETE FROM api_keys WHERE key_hash = ?`, keyHash)
	batch.Query(`DELETE FROM api_keys_by_user WHERE org_id = ? AND user_id = ? AND created_at = ?`,
		orgID, userID, key.CreatedAt)
	if err := batch.Exec(); err != nil {
		return fmt.Errorf("revoke api key: %w", err)
	}

	// Evict from cache.
	m.cacheMu.Lock()
	delete(m.cache, keyHash)
	m.cacheMu.Unlock()

	return nil
}

// GetOwnedKey verifies ownership of an API key and returns its metadata.
func (m *Manager) GetOwnedKey(orgID, userID gocql.UUID, keyHash string) (*APIKey, error) {
	// Verify ownership by reading the key.
	key := &APIKey{KeyHash: keyHash}
	err := m.db.Session().Query(`
		SELECT key_prefix, user_id, org_id, label, scope, created_at, last_used_at, expires_at
		FROM api_keys WHERE key_hash = ?
	`, keyHash).Scan(
		&key.KeyPrefix, &key.UserID, &key.OrgID, &key.Label, &key.Scope,
		&key.CreatedAt, &key.LastUsedAt, &key.ExpiresAt,
	)
	if err != nil {
		return nil, ErrKeyNotFound
	}
	if key.UserID != userID || key.OrgID != orgID {
		return nil, ErrNotOwner
	}

	return key, nil
}

// RestoreKey recreates a previously deleted API key row pair.
func (m *Manager) RestoreKey(key *APIKey) error {
	if key == nil {
		return errors.New("api key is required")
	}

	now := time.Now().UTC()
	ttlSeconds, err := ttlFromExpiry(now, key.ExpiresAt)
	if err != nil && !errors.Is(err, ErrInvalidExpiry) {
		return err
	}
	if errors.Is(err, ErrInvalidExpiry) {
		return nil
	}

	batch := m.db.Session().Batch(gocql.LoggedBatch)
	if ttlSeconds > 0 {
		batch.Query(`
			INSERT INTO api_keys (key_hash, key_prefix, user_id, org_id, label, scope, created_at, last_used_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			USING TTL ?
		`, key.KeyHash, key.KeyPrefix, key.UserID, key.OrgID, key.Label, key.Scope, key.CreatedAt, key.LastUsedAt, key.ExpiresAt, ttlSeconds)
		batch.Query(`
			INSERT INTO api_keys_by_user (org_id, user_id, key_hash, key_prefix, label, scope, created_at, last_used_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			USING TTL ?
		`, key.OrgID, key.UserID, key.KeyHash, key.KeyPrefix, key.Label, key.Scope, key.CreatedAt, key.LastUsedAt, key.ExpiresAt, ttlSeconds)
	} else {
		batch.Query(`
			INSERT INTO api_keys (key_hash, key_prefix, user_id, org_id, label, scope, created_at, last_used_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, key.KeyHash, key.KeyPrefix, key.UserID, key.OrgID, key.Label, key.Scope, key.CreatedAt, key.LastUsedAt, key.ExpiresAt)
		batch.Query(`
			INSERT INTO api_keys_by_user (org_id, user_id, key_hash, key_prefix, label, scope, created_at, last_used_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, key.OrgID, key.UserID, key.KeyHash, key.KeyPrefix, key.Label, key.Scope, key.CreatedAt, key.LastUsedAt, key.ExpiresAt)
	}

	if err := batch.Exec(); err != nil {
		return fmt.Errorf("restore api key: %w", err)
	}

	m.cacheMu.Lock()
	m.cache[key.KeyHash] = &cacheEntry{key: key, cachedAt: now}
	m.cacheMu.Unlock()

	return nil
}

// ListUserKeys returns all API keys for a user (without raw tokens).
func (m *Manager) ListUserKeys(orgID, userID gocql.UUID) ([]APIKey, error) {
	iter := m.db.Session().Query(`
		SELECT key_hash, key_prefix, label, scope, created_at, last_used_at, expires_at
		FROM api_keys_by_user WHERE org_id = ? AND user_id = ?
	`, orgID, userID).Iter()

	var keys []APIKey
	var key APIKey
	for iter.Scan(&key.KeyHash, &key.KeyPrefix, &key.Label, &key.Scope,
		&key.CreatedAt, &key.LastUsedAt, &key.ExpiresAt) {
		key.UserID = userID
		key.OrgID = orgID
		keys = append(keys, key)
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("list api keys: %w", err)
	}
	return keys, nil
}

// InvalidateUserAPIKeys revokes ALL API keys for a user.
// Used when a user is deactivated or deleted.
func (m *Manager) InvalidateUserAPIKeys(orgID, userID gocql.UUID) error {
	if m.db == nil {
		return nil
	}

	// Read all key hashes + created_at from reverse index.
	iter := m.db.Session().Query(
		`SELECT key_hash, created_at FROM api_keys_by_user WHERE org_id = ? AND user_id = ?`,
		orgID, userID,
	).Iter()

	type keyRef struct {
		hash      string
		createdAt time.Time
	}
	var refs []keyRef
	var ref keyRef
	for iter.Scan(&ref.hash, &ref.createdAt) {
		refs = append(refs, ref)
	}
	if err := iter.Close(); err != nil {
		return fmt.Errorf("read api_keys_by_user: %w", err)
	}

	if len(refs) == 0 {
		return nil
	}

	// Batch-delete in chunks of 25 (same pattern as sessions).
	for i := 0; i < len(refs); i += 25 {
		end := i + 25
		if end > len(refs) {
			end = len(refs)
		}
		batch := m.db.Session().Batch(gocql.UnloggedBatch)
		for _, r := range refs[i:end] {
			batch.Query(`DELETE FROM api_keys WHERE key_hash = ?`, r.hash)
			batch.Query(`DELETE FROM api_keys_by_user WHERE org_id = ? AND user_id = ? AND created_at = ?`,
				orgID, userID, r.createdAt)
		}
		batch.Exec() //nolint:errcheck
	}

	// Evict from cache.
	m.cacheMu.Lock()
	for hash, entry := range m.cache {
		if entry.key.UserID == userID && entry.key.OrgID == orgID {
			delete(m.cache, hash)
		}
	}
	m.cacheMu.Unlock()

	return nil
}

// HashToken creates a SHA-256 hash of a raw token for storage/lookup.
func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// --- internal helpers ---

func checkExpiry(key *APIKey) error {
	if key.ExpiresAt != nil && time.Now().UTC().After(*key.ExpiresAt) {
		return ErrKeyExpired
	}
	return nil
}

func ttlFromExpiry(now time.Time, expiresAt *time.Time) (int, error) {
	if expiresAt == nil {
		return 0, nil
	}
	ttlSeconds := int(expiresAt.Sub(now).Seconds())
	if ttlSeconds <= 0 {
		return 0, ErrInvalidExpiry
	}
	return ttlSeconds, nil
}

func (m *Manager) updateLastUsed(key *APIKey) {
	if m.db == nil || key == nil {
		return
	}

	now := time.Now().UTC()
	batch := m.db.Session().Batch(gocql.UnloggedBatch)
	batch.Query(`
		UPDATE api_keys SET last_used_at = ? WHERE key_hash = ?
	`, now, key.KeyHash)
	batch.Query(`
		UPDATE api_keys_by_user SET last_used_at = ? WHERE org_id = ? AND user_id = ? AND created_at = ?
	`, now, key.OrgID, key.UserID, key.CreatedAt)
	if err := batch.Exec(); err != nil {
		log.Printf("[apikeys] failed to update last_used_at for %s: %v", key.KeyHash[:16], err)
		return
	}

	m.cacheMu.Lock()
	if entry, ok := m.cache[key.KeyHash]; ok && entry.key != nil {
		entry.key.LastUsedAt = &now
		entry.cachedAt = time.Now()
	}
	m.cacheMu.Unlock()
}

func (m *Manager) countUserKeys(orgID, userID gocql.UUID) (int, error) {
	var count int
	err := m.db.Session().Query(`
		SELECT COUNT(*) FROM api_keys_by_user WHERE org_id = ? AND user_id = ?
	`, orgID, userID).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (m *Manager) cleanupLoop() {
	ticker := time.NewTicker(cacheTTL)
	defer ticker.Stop()
	for range ticker.C {
		m.cleanupExpired()
	}
}

func (m *Manager) cleanupExpired() {
	now := time.Now()
	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()
	for hash, entry := range m.cache {
		if now.Sub(entry.cachedAt) >= cacheTTL {
			delete(m.cache, hash)
		}
	}
}
