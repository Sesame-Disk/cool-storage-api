package v2

import (
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/crypto"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// DecryptSession tracks which libraries a user has unlocked and their file keys
type DecryptSession struct {
	UnlockedAt time.Time
	FileKey    []byte // The decrypted file encryption key (32 bytes)
	FileIV     []byte // The derived file encryption IV (16 bytes) - for Seafile v2 compat
}

// DecryptSessionManager manages library decrypt sessions
// Libraries are unlocked for 1 hour after password verification
type DecryptSessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*DecryptSession // key: "userID:repoID"
	ttl      time.Duration
}

// Global session manager
var decryptSessions = &DecryptSessionManager{
	sessions: make(map[string]*DecryptSession),
	ttl:      1 * time.Hour,
}

// IsUnlocked checks if a library is unlocked for a user
func (m *DecryptSessionManager) IsUnlocked(userID, repoID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	key := userID + ":" + repoID
	session, ok := m.sessions[key]
	if !ok {
		return false
	}

	// Check if session has expired
	if time.Since(session.UnlockedAt) > m.ttl {
		return false
	}

	return true
}

// Unlock marks a library as unlocked for a user and stores the file key and IV
func (m *DecryptSessionManager) Unlock(userID, repoID string, fileKey, fileIV []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := userID + ":" + repoID
	m.sessions[key] = &DecryptSession{
		UnlockedAt: time.Now(),
		FileKey:    fileKey,
		FileIV:     fileIV,
	}
}

// GetFileKey returns the file key for an unlocked library
// Returns nil if library is not unlocked or session expired
func (m *DecryptSessionManager) GetFileKey(userID, repoID string) []byte {
	m.mu.RLock()
	defer m.mu.RUnlock()

	key := userID + ":" + repoID
	session, ok := m.sessions[key]
	if !ok {
		return nil
	}

	// Check if session has expired
	if time.Since(session.UnlockedAt) > m.ttl {
		return nil
	}

	return session.FileKey
}

// GetFileKeyAndIV returns both the file key and IV for an unlocked library
// Returns nil, nil if library is not unlocked or session expired
func (m *DecryptSessionManager) GetFileKeyAndIV(userID, repoID string) ([]byte, []byte) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	key := userID + ":" + repoID
	session, ok := m.sessions[key]
	if !ok {
		return nil, nil
	}

	// Check if session has expired
	if time.Since(session.UnlockedAt) > m.ttl {
		return nil, nil
	}

	return session.FileKey, session.FileIV
}

// Lock marks a library as locked for a user (e.g., after password change)
func (m *DecryptSessionManager) Lock(userID, repoID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := userID + ":" + repoID
	delete(m.sessions, key)
}

// GetDecryptSessions returns the global session manager
func GetDecryptSessions() *DecryptSessionManager {
	return decryptSessions
}

// EncryptionHandler handles encrypted library password operations
type EncryptionHandler struct {
	db *db.DB
}

// NewEncryptionHandler creates a new encryption handler
func NewEncryptionHandler(db *db.DB) *EncryptionHandler {
	return &EncryptionHandler{db: db}
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

	var req SetPasswordRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "password is required"})
		return
	}

	if req.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "password is required"})
		return
	}

	// Get library encryption info
	var encrypted bool
	var encVersion int
	var magic, salt, magicStrong, randomKey string

	if err := h.db.Session().Query(`
		SELECT encrypted, enc_version, magic, salt, magic_strong, random_key
		FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&encrypted, &encVersion, &magic, &salt, &magicStrong, &randomKey); err != nil {
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
	decryptSessions.Unlock(userID, repoID, fileKey, fileIV)

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

	var req ChangePasswordRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "old_password and new_password are required"})
		return
	}

	if req.OldPassword == "" || req.NewPassword == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "old_password and new_password are required"})
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
			c.JSON(http.StatusBadRequest, gin.H{"error_msg": "Wrong password"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to change password"})
		return
	}

	// Update database with new encryption params
	if err := h.db.Session().Query(`
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
		newParams.MagicStrong, newParams.RandomKeyStrong, time.Now(),
		orgID, repoID).Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update library"})
		return
	}
	if err := syncAdminLibraryReadModel(h.db, orgID, repoID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to sync library read model"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}
