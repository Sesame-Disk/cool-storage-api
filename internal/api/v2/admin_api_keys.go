package v2

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/apikeys"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
)

type adminAPIKeyManager interface {
	CreateKey(userID, orgID gocql.UUID, label, scope string, expiresAt *time.Time) (string, *apikeys.APIKey, error)
	ListUserKeys(orgID, userID gocql.UUID) ([]apikeys.APIKey, error)
	GetOwnedKey(orgID, userID gocql.UUID, keyHash string) (*apikeys.APIKey, error)
	RestoreKey(key *apikeys.APIKey) error
	RevokeKey(orgID, userID gocql.UUID, keyHash string) error
	InvalidateUserAPIKeys(orgID, userID gocql.UUID) error
}

func (h *AdminHandler) getAdminAPIKeyManager() (adminAPIKeyManager, bool) {
	if h.apiKeys == nil {
		return nil, false
	}
	mgr, ok := h.apiKeys.(adminAPIKeyManager)
	return mgr, ok
}

func (h *AdminHandler) lookupPlatformUserAPIKeyTarget(email string) (gocql.UUID, gocql.UUID, string, string, error) {
	userID, orgID, err := h.lookupUserByEmail(email)
	if err != nil {
		return gocql.UUID{}, gocql.UUID{}, "", "", err
	}
	if orgID != middleware.PlatformOrgID {
		return gocql.UUID{}, gocql.UUID{}, "", "", errors.New("api key admin management is limited to platform users")
	}

	userUUID, err := gocql.ParseUUID(userID)
	if err != nil {
		return gocql.UUID{}, gocql.UUID{}, "", "", err
	}
	orgUUID, err := gocql.ParseUUID(orgID)
	if err != nil {
		return gocql.UUID{}, gocql.UUID{}, "", "", err
	}

	var role, status string
	if err := h.db.Session().Query(`
		SELECT role, status FROM users WHERE org_id = ? AND user_id = ?
	`, orgID, userID).Scan(&role, &status); err != nil {
		return gocql.UUID{}, gocql.UUID{}, "", "", err
	}

	return userUUID, orgUUID, role, status, nil
}

func (h *AdminHandler) resolvedAPIKeyHash(c *gin.Context) string {
	if v, ok := c.Get("resolved_api_key_hash"); ok {
		if hash, hashOK := v.(string); hashOK {
			return strings.TrimSpace(hash)
		}
	}
	return ""
}

// AdminListUserAPIKeys lists API keys for a platform-org user identified by email.
// GET /admin/users/:email/api-keys/
func (h *AdminHandler) AdminListUserAPIKeys(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	mgr, ok := h.getAdminAPIKeyManager()
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "api key manager unavailable"})
		return
	}

	email := h.getResolvedUserParam(c)
	userUUID, orgUUID, _, _, err := h.lookupPlatformUserAPIKeyTarget(email)
	if err != nil {
		status := http.StatusNotFound
		if strings.Contains(err.Error(), "limited to platform users") {
			status = http.StatusForbidden
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	keys, err := mgr.ListUserKeys(orgUUID, userUUID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list API keys"})
		return
	}

	result := make([]gin.H, 0, len(keys))
	for _, key := range keys {
		result = append(result, gin.H{
			"key_hash":     key.KeyHash,
			"key_prefix":   key.KeyPrefix,
			"label":        key.Label,
			"scope":        key.Scope,
			"created_at":   key.CreatedAt,
			"last_used_at": key.LastUsedAt,
			"expires_at":   key.ExpiresAt,
			"owner_email":  email,
		})
	}

	c.JSON(http.StatusOK, result)
}

// AdminCreateUserAPIKey creates an API key for a platform-org user identified by email.
// POST /admin/users/:email/api-keys/
func (h *AdminHandler) AdminCreateUserAPIKey(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	mgr, ok := h.getAdminAPIKeyManager()
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "api key manager unavailable"})
		return
	}

	email := h.getResolvedUserParam(c)
	userUUID, orgUUID, role, status, err := h.lookupPlatformUserAPIKeyTarget(email)
	if err != nil {
		statusCode := http.StatusNotFound
		if strings.Contains(err.Error(), "limited to platform users") {
			statusCode = http.StatusForbidden
		}
		c.JSON(statusCode, gin.H{"error": err.Error()})
		return
	}
	if !IsUserUsable(status) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "target user is not active"})
		return
	}

	var req struct {
		Label         string `json:"label"`
		Scope         string `json:"scope"`
		ExpiresInDays *int   `json:"expires_in_days"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	req.Label = strings.TrimSpace(req.Label)
	if req.Label == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "label is required"})
		return
	}
	if !apikeys.IsValidScope(req.Scope) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "scope must be 'read', 'read-write', or 'admin'"})
		return
	}
	if req.Scope == apikeys.ScopeAdmin && role != "superadmin" && role != "owner" && role != "admin" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "admin scope requires an admin-capable target user"})
		return
	}
	if req.ExpiresInDays != nil && *req.ExpiresInDays <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expires_in_days must be greater than zero"})
		return
	}

	var expiresAt *time.Time
	if req.ExpiresInDays != nil {
		t := time.Now().UTC().AddDate(0, 0, *req.ExpiresInDays)
		expiresAt = &t
	}

	rawToken, key, err := mgr.CreateKey(userUUID, orgUUID, req.Label, req.Scope, expiresAt)
	if err != nil {
		switch {
		case errors.Is(err, apikeys.ErrKeyLimitReached):
			c.JSON(http.StatusConflict, gin.H{"error": "maximum number of API keys reached"})
		case errors.Is(err, apikeys.ErrInvalidExpiry):
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid API key expiry"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create API key"})
		}
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"key":         rawToken,
		"key_hash":    key.KeyHash,
		"key_prefix":  key.KeyPrefix,
		"label":       key.Label,
		"scope":       key.Scope,
		"created_at":  key.CreatedAt,
		"expires_at":  key.ExpiresAt,
		"owner_email": email,
	})
}

// AdminRevokeUserAPIKey revokes an API key for a platform-org user identified by email.
// DELETE /admin/users/:email/api-keys/:key_hash/
func (h *AdminHandler) AdminRevokeUserAPIKey(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	mgr, ok := h.getAdminAPIKeyManager()
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "api key manager unavailable"})
		return
	}

	email := h.getResolvedUserParam(c)
	keyHash := h.resolvedAPIKeyHash(c)
	if keyHash == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key_hash is required"})
		return
	}

	userUUID, orgUUID, _, _, err := h.lookupPlatformUserAPIKeyTarget(email)
	if err != nil {
		statusCode := http.StatusNotFound
		if strings.Contains(err.Error(), "limited to platform users") {
			statusCode = http.StatusForbidden
		}
		c.JSON(statusCode, gin.H{"error": err.Error()})
		return
	}

	key, err := mgr.GetOwnedKey(orgUUID, userUUID, keyHash)
	if err != nil {
		switch {
		case errors.Is(err, apikeys.ErrKeyNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "API key not found"})
		case errors.Is(err, apikeys.ErrNotOwner):
			c.JSON(http.StatusForbidden, gin.H{"error": "API key does not belong to this user"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to revoke API key"})
		}
		return
	}

	if err := mgr.RevokeKey(orgUUID, userUUID, keyHash); err != nil {
		switch {
		case errors.Is(err, apikeys.ErrKeyNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "API key not found"})
		case errors.Is(err, apikeys.ErrNotOwner):
			c.JSON(http.StatusForbidden, gin.H{"error": "API key does not belong to this user"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to revoke API key"})
		}
		return
	}

	if h.sessions != nil {
		if err := h.sessions.InvalidateAPIKeySessions(keyHash); err != nil {
			if restoreErr := mgr.RestoreKey(key); restoreErr != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to revoke API key"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to revoke API key"})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}
