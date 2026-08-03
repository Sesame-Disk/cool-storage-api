package v2

import (
	"context"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/crypto"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ErrLibraryEncryptedNotUnlocked signals that an encrypted library has no active
// decrypt session for the caller. Upload/finalize paths that cannot render a
// gin.Context response inline return it so the transport layer can emit the
// app-wide 403 { lib_need_decrypt: true } contract the frontend keys off to
// re-open the repo password dialog (see isLibraryEncryptedError in
// frontend/src/utils/upload-finalization.js) instead of a generic 500.
var ErrLibraryEncryptedNotUnlocked = errors.New("library is encrypted and not unlocked")

// libraryIsEncrypted probes the library encryption flag and PROPAGATES the error.
//
// The bug this exists to prevent: `Scan(&encrypted)` with the error discarded (or
// turned into "allow") leaves encrypted=false on any transient Cassandra failure,
// which is indistinguishable from "this library is not encrypted". Callers then
// take the plaintext branch — storing plaintext into an encrypted library, serving
// ciphertext as content, or skipping the decrypt-session gate entirely. Every
// caller must fail closed in whatever way its own contract allows; see
// ISSUE-ENCRYPTED-FLAG-UNCHECKED-01.
// It is a var so tests can drive the probe-failure branch directly; there is no
// other way to make a real session fail on demand.
var libraryIsEncrypted = func(database *db.DB, orgID, repoID string) (bool, error) {
	return libraryIsEncryptedContext(context.Background(), database, orgID, repoID)
}

func libraryIsEncryptedContext(ctx context.Context, database *db.DB, orgID, repoID string) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var encrypted bool
	err := database.Session().Query(`
		SELECT encrypted FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).WithContext(ctx).Scan(&encrypted)
	return encrypted, err
}

// respondEncryptionProbeUnavailable emits the retryable answer for a failed
// encryption probe. It is deliberately NOT the 403 lib_need_decrypt contract:
// we do not know whether the library is encrypted, so the client must retry
// rather than be told to prompt for a password.
func respondEncryptionProbeUnavailable(c *gin.Context) {
	c.Header("Retry-After", "1")
	c.JSON(http.StatusServiceUnavailable, gin.H{
		"error": "library encryption state is temporarily unavailable; retry",
	})
}

// DecryptSession tracks which libraries a user has unlocked and their file keys
type DecryptSession struct {
	UnlockedAt        time.Time
	OrgID             string
	RepoID            string
	UpdatedAt         time.Time
	lastResolverCheck time.Time // last time we queried the DB to validate UpdatedAt
	FileKey           []byte    // The decrypted file encryption key (32 bytes)
	FileIV            []byte    // The derived file encryption IV (16 bytes) - for Seafile v2 compat
}

type libraryUpdatedAtResolver func(orgID, repoID string) (time.Time, error)
type libraryUpdatedAtResolverContext func(context.Context, string, string) (time.Time, error)

// resolverCheckInterval is the minimum time between DB lookups for a single
// session. Prevents hammering Cassandra on encrypted-file downloads where
// GetFileKeyAndIV is called per block.
const resolverCheckInterval = 30 * time.Second

// DecryptSessionManager manages library decrypt sessions
// Libraries are unlocked for 1 hour after password verification
type DecryptSessionManager struct {
	mu                       sync.RWMutex
	sessions                 map[string]*DecryptSession // key: "userID:repoID"
	ttl                      time.Duration
	updatedAtResolver        libraryUpdatedAtResolver
	updatedAtResolverContext libraryUpdatedAtResolverContext
}

// Global session manager
var decryptSessions = &DecryptSessionManager{
	sessions: make(map[string]*DecryptSession),
	ttl:      1 * time.Hour,
}

// IsUnlocked checks if a library is unlocked for a user
func (m *DecryptSessionManager) IsUnlocked(userID, repoID string) bool {
	_, _, ok := m.getActiveSession(userID, repoID)
	return ok
}

// Unlock marks a library as unlocked for a user and stores the file key and IV
func (m *DecryptSessionManager) Unlock(userID, repoID string, fileKey, fileIV []byte) {
	m.UnlockForLibrary(userID, "", repoID, time.Time{}, fileKey, fileIV)
}

// UnlockForLibrary stores decrypt material plus the library revision observed
// when the password was verified so other replicas can invalidate stale cached
// sessions after a password rotation.
func (m *DecryptSessionManager) UnlockForLibrary(userID, orgID, repoID string, libraryUpdatedAt time.Time, fileKey, fileIV []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := userID + ":" + repoID
	m.sessions[key] = &DecryptSession{
		UnlockedAt: time.Now(),
		OrgID:      orgID,
		RepoID:     repoID,
		UpdatedAt:  libraryUpdatedAt,
		FileKey:    fileKey,
		FileIV:     fileIV,
	}
}

// GetFileKey returns the file key for an unlocked library
// Returns nil if library is not unlocked or session expired
func (m *DecryptSessionManager) GetFileKey(userID, repoID string) []byte {
	session, _, ok := m.getActiveSession(userID, repoID)
	if !ok {
		return nil
	}
	return session.FileKey
}

// GetFileKeyAndIV returns both the file key and IV for an unlocked library
// Returns nil, nil if library is not unlocked or session expired
func (m *DecryptSessionManager) GetFileKeyAndIV(userID, repoID string) ([]byte, []byte) {
	return m.GetFileKeyAndIVContext(context.Background(), userID, repoID)
}

// GetFileKeyAndIVContext is GetFileKeyAndIV bound to ctx for request-scoped
// encrypted reads. The cross-replica invalidation lookup observes the same
// preparation deadline as the rest of the request's metadata work.
func (m *DecryptSessionManager) GetFileKeyAndIVContext(ctx context.Context, userID, repoID string) ([]byte, []byte) {
	session, _, ok := m.getActiveSessionContext(ctx, userID, repoID)
	if !ok {
		return nil, nil
	}
	return session.FileKey, session.FileIV
}

func (m *DecryptSessionManager) getActiveSession(userID, repoID string) (*DecryptSession, string, bool) {
	return m.getActiveSessionContext(context.Background(), userID, repoID)
}

func (m *DecryptSessionManager) getActiveSessionContext(ctx context.Context, userID, repoID string) (*DecryptSession, string, bool) {
	if ctx == nil {
		ctx = context.Background()
	}
	key := userID + ":" + repoID

	m.mu.RLock()
	session, ok := m.sessions[key]
	if !ok {
		m.mu.RUnlock()
		return nil, key, false
	}
	if time.Since(session.UnlockedAt) > m.ttl {
		m.mu.RUnlock()
		m.Lock(userID, repoID)
		return nil, key, false
	}
	resolver := m.updatedAtResolver
	resolverContext := m.updatedAtResolverContext
	snapshot := *session
	m.mu.RUnlock()

	// Cross-replica invalidation: check whether the library's updated_at has
	// advanced (meaning another replica changed the password). Rate-limited to
	// avoid a Cassandra query on every block download.
	if (resolverContext != nil || resolver != nil) && snapshot.OrgID != "" && !snapshot.UpdatedAt.IsZero() &&
		time.Since(snapshot.lastResolverCheck) > resolverCheckInterval {
		var currentUpdatedAt time.Time
		var err error
		if resolverContext != nil {
			currentUpdatedAt, err = resolverContext(ctx, snapshot.OrgID, snapshot.RepoID)
		} else {
			currentUpdatedAt, err = resolver(snapshot.OrgID, snapshot.RepoID)
		}
		if err == nil && currentUpdatedAt.After(snapshot.UpdatedAt) {
			// Password was rotated on another replica — evict.
			m.Lock(userID, repoID)
			return nil, key, false
		}
		// Regardless of success or transient error, update the check timestamp
		// so we don't re-query immediately. A transient DB error should NOT
		// revoke the session — it just means we couldn't verify this time.
		m.mu.Lock()
		if s, ok := m.sessions[key]; ok {
			s.lastResolverCheck = time.Now()
		}
		m.mu.Unlock()
	}

	return &snapshot, key, true
}

// Lock marks a library as locked for a user (e.g., after password change)
func (m *DecryptSessionManager) Lock(userID, repoID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := userID + ":" + repoID
	delete(m.sessions, key)
}

// LockAllForRepo invalidates every active decrypt session for the given
// library across all users. Called after a password change so that clients
// holding a previously-unlocked session can't decrypt with the old file key.
// Returns the number of sessions evicted.
func (m *DecryptSessionManager) LockAllForRepo(repoID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	suffix := ":" + repoID
	evicted := 0
	for key := range m.sessions {
		if len(key) > len(suffix) && key[len(key)-len(suffix):] == suffix {
			delete(m.sessions, key)
			evicted++
		}
	}
	return evicted
}

// SetUpdatedAtResolver configures how decrypt sessions validate that the
// library password state has not changed on another replica.
func (m *DecryptSessionManager) SetUpdatedAtResolver(resolver libraryUpdatedAtResolver) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updatedAtResolver = resolver
	m.updatedAtResolverContext = nil
}

// SetUpdatedAtResolverContext configures a cancellation-aware resolver for
// request-scoped decrypt-session validation.
func (m *DecryptSessionManager) SetUpdatedAtResolverContext(resolver libraryUpdatedAtResolverContext) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updatedAtResolverContext = resolver
}

// GetDecryptSessions returns the global session manager
func GetDecryptSessions() *DecryptSessionManager {
	return decryptSessions
}

// EncryptionHandler handles encrypted library password operations
type EncryptionHandler struct {
	db             *db.DB
	permMiddleware *middleware.PermissionMiddleware
	rateLimiter    *PasswordRateLimiter
}

// NewEncryptionHandler creates a new encryption handler
func NewEncryptionHandler(database *db.DB) *EncryptionHandler {
	if database != nil {
		decryptSessions.SetUpdatedAtResolverContext(func(ctx context.Context, orgID, repoID string) (time.Time, error) {
			var updatedAt time.Time
			err := database.Session().Query(`
				SELECT updated_at FROM libraries WHERE org_id = ? AND library_id = ?
			`, orgID, repoID).WithContext(ctx).Scan(&updatedAt)
			return updatedAt, err
		})
	}
	return &EncryptionHandler{
		db:             database,
		permMiddleware: middleware.NewPermissionMiddleware(database),
		rateLimiter:    NewPasswordRateLimiter(database),
	}
}

func (h *EncryptionHandler) requireLibraryAccess(c *gin.Context, repoID string, required middleware.LibraryPermission) bool {
	if h == nil || h.permMiddleware == nil {
		return true
	}

	hasAccess, err := h.permMiddleware.HasLibraryAccessCtx(
		c,
		c.GetString("org_id"),
		c.GetString("user_id"),
		repoID,
		required,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check library permissions"})
		return false
	}
	if !hasAccess {
		c.JSON(http.StatusForbidden, gin.H{"error": "insufficient library permissions"})
		return false
	}
	return true
}

// passwordActorKey identifies the caller for rate-limiting purposes.
// Prefer user_id (authenticated), fall back to client IP so that unauthenticated
// brute force via shared tokens is still tracked.
func passwordActorKey(c *gin.Context) string {
	if uid := c.GetString("user_id"); uid != "" {
		return "u:" + uid
	}
	return "ip:" + c.ClientIP()
}

// writePasswordRateLimitError responds with 429 + Retry-After when the limiter
// is active. Central so both SetPassword and ChangePassword behave identically.
func writePasswordRateLimitError(c *gin.Context, retryAfter time.Duration) {
	seconds := int(retryAfter.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	c.Header("Retry-After", strconv.Itoa(seconds))
	c.JSON(http.StatusTooManyRequests, gin.H{
		"error_msg":           "Too many password attempts. Try again later.",
		"retry_after_seconds": seconds,
	})
}

// SetPasswordRequest is the request body for setting/verifying password
type SetPasswordRequest struct {
	Password string `json:"password" form:"password"`
}

// ChangePasswordRequest is the request body for changing password
type ChangePasswordRequest struct {
	OldPassword string `json:"old_password" form:"old_password"`
	NewPassword string `json:"new_password" form:"new_password"`
}

// SetPassword handles POST /api/v2.1/repos/:repo_id/set-password/
// This endpoint verifies the password for an encrypted library (unlocks it).
func (h *EncryptionHandler) SetPassword(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")

	if _, err := uuid.Parse(repoID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid repo_id"})
		return
	}
	if !h.requireLibraryAccess(c, repoID, middleware.PermissionR) {
		return
	}

	var req SetPasswordRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "password is required"})
		return
	}

	if req.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "password is required"})
		return
	}

	actorKey := passwordActorKey(c)
	if retryAfter, err := h.rateLimiter.Check(repoID, actorKey); err != nil {
		if errors.Is(err, ErrPasswordRateLimited) {
			writePasswordRateLimitError(c, retryAfter)
			return
		}
		log.Printf("[encryption] rate limit check failed: %v", err)
		// Fail closed on limiter errors: an attacker benefits if we fail open.
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "rate limiter unavailable"})
		return
	}

	// Get library encryption info
	var encrypted bool
	var encVersion int
	var magic, salt, magicStrong, randomKey string
	var updatedAt time.Time

	if err := h.db.Session().Query(`
		SELECT encrypted, enc_version, magic, salt, magic_strong, random_key, updated_at
		FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&encrypted, &encVersion, &magic, &salt, &magicStrong, &randomKey, &updatedAt); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	if !encrypted {
		c.JSON(http.StatusBadRequest, gin.H{"error": "library is not encrypted"})
		return
	}

	// Decode salt bytes once for all operations
	var saltBytes []byte
	if salt != "" {
		saltBytes, _ = hex.DecodeString(salt)
	}

	// Verify password
	// Try strong verification first (Argon2id), fall back to Seafile compat (PBKDF2)
	verified := false

	if magicStrong != "" && len(saltBytes) > 0 {
		// Use strong verification (Argon2id)
		verified = crypto.VerifyPasswordStrong(req.Password, repoID, magicStrong, saltBytes)
	}

	if !verified && magic != "" {
		// Fall back to Seafile-compatible verification (PBKDF2)
		verified = crypto.VerifyPasswordSeafile(req.Password, repoID, magic, saltBytes, encVersion)
	}

	if !verified {
		if err := h.rateLimiter.RecordFailure(repoID, actorKey); err != nil {
			log.Printf("[encryption] record failure: %v", err)
		}
		c.JSON(http.StatusBadRequest, gin.H{"error_msg": "Wrong password"})
		return
	}

	// Password verified successfully - decrypt the file key and IV, then unlock the library

	fileKey, fileIV, err := crypto.GetFileKeyAndIVFromPassword(req.Password, repoID, saltBytes, randomKey, encVersion)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to decrypt file key"})
		return
	}

	// Store the file key and IV in session for this user
	userID := c.GetString("user_id")
	decryptSessions.UnlockForLibrary(userID, orgID, repoID, updatedAt, fileKey, fileIV)

	if err := h.rateLimiter.RecordSuccess(repoID, actorKey); err != nil {
		log.Printf("[encryption] record success: %v", err)
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ChangePassword handles PUT /api/v2.1/repos/:repo_id/set-password/
// This endpoint changes the password for an encrypted library.
func (h *EncryptionHandler) ChangePassword(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")

	if _, err := uuid.Parse(repoID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid repo_id"})
		return
	}
	if !h.requireLibraryAccess(c, repoID, middleware.PermissionRW) {
		return
	}

	var req ChangePasswordRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "old_password and new_password are required"})
		return
	}

	if req.OldPassword == "" || req.NewPassword == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "old_password and new_password are required"})
		return
	}

	actorKey := passwordActorKey(c)
	if retryAfter, err := h.rateLimiter.Check(repoID, actorKey); err != nil {
		if errors.Is(err, ErrPasswordRateLimited) {
			writePasswordRateLimitError(c, retryAfter)
			return
		}
		log.Printf("[encryption] rate limit check failed: %v", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "rate limiter unavailable"})
		return
	}

	// Get library encryption info
	var encrypted bool
	var encVersion int
	var magic, salt, randomKey, magicStrong, randomKeyStrong string

	if err := h.db.Session().Query(`
		SELECT encrypted, enc_version, magic, salt, random_key, magic_strong, random_key_strong
		FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&encrypted, &encVersion, &magic, &salt, &randomKey, &magicStrong, &randomKeyStrong); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	if !encrypted {
		c.JSON(http.StatusBadRequest, gin.H{"error": "library is not encrypted"})
		return
	}

	// Verify old password first
	oldParams := &crypto.EncryptionParams{
		EncVersion: encVersion,
		Salt:       salt,
		Magic:      magic,
		RandomKey:  randomKey,
	}

	// Change password using crypto package
	newParams, err := crypto.ChangePassword(req.OldPassword, req.NewPassword, repoID, oldParams)
	if err != nil {
		if err.Error() == "wrong password" {
			if rerr := h.rateLimiter.RecordFailure(repoID, actorKey); rerr != nil {
				log.Printf("[encryption] record failure: %v", rerr)
			}
			c.JSON(http.StatusBadRequest, gin.H{"error_msg": "Wrong password"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to change password"})
		return
	}

	// Update database with new encryption params
	now := time.Now()
	previousRow, err := db.ReadAdminLibraryProjectionRow(h.db.Session(), orgID, repoID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read library projection row"})
		return
	}
	projectionRow := previousRow
	projectionRow.UpdatedAt = now
	batch := h.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		UPDATE libraries SET
			enc_version = ?,
			salt = ?,
			magic = ?,
			random_key = ?,
			magic_strong = ?,
			random_key_strong = ?,
			updated_at = ?
		WHERE org_id = ? AND library_id = ?
	`, newParams.EncVersion, newParams.Salt, newParams.Magic, newParams.RandomKey,
		newParams.MagicStrong, newParams.RandomKeyStrong, now,
		orgID, repoID)
	addAdminLibraryReadModelRefreshQueries(batch, projectionRow, &previousRow)
	if err := batch.Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update library"})
		return
	}

	// Password rotated — force every active decrypt session for this library
	// to re-unlock. Without this, any user who unlocked with the old password
	// could continue reading files via the cached file key for up to ttl.
	decryptSessions.LockAllForRepo(repoID)

	if err := h.rateLimiter.RecordSuccess(repoID, actorKey); err != nil {
		log.Printf("[encryption] record success: %v", err)
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}
