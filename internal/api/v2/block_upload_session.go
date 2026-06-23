package v2

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// WebUploadBlockSize is the fixed plaintext block size used by the web
// content-addressed upload flow. It MUST match api.uploadBlockSize (8 MB) so a
// file uploaded by the web client splits into the same blocks the rest of the
// system expects on download/streaming.
const WebUploadBlockSize = 8 * 1024 * 1024

// libraryEncrypted reports whether a library is encrypted. Unlike
// requireDecryptSession (which permits access when a decrypt session exists),
// the block upload flow rejects encrypted libraries outright: SHA-256 block IDs
// are computed over plaintext on the client, which is incompatible with the
// server-side Seafile block encryption used for encrypted libraries.
func (h *FileHandler) libraryEncrypted(orgID, repoID string) (bool, error) {
	if h.db == nil {
		return false, nil
	}
	var encrypted bool
	err := h.db.Session().Query(`
		SELECT encrypted FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&encrypted)
	if err != nil {
		return false, err
	}
	return encrypted, nil
}

// CreateBlockUploadSession mints a server-issued session for the web
// content-addressed (block) upload flow. The returned session_id scopes the
// subsequent /blocks/upload calls and the final file-from-blocks commit to this
// (org, user, repo), and doubles as the provisional block reference owner.
//
// POST /api/v2.1/repos/:repo_id/block-upload-session/
func (h *FileHandler) CreateBlockUploadSession(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	if !h.requireWritePermission(c, orgID, userID) {
		return
	}
	if h.permMiddleware != nil && !h.permMiddleware.RequirePermFlag(c, "upload") {
		c.JSON(http.StatusForbidden, gin.H{"error": "upload is not allowed by your permission"})
		return
	}
	if h.db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
		return
	}

	encrypted, err := h.libraryEncrypted(orgID, repoID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}
	if encrypted {
		c.JSON(http.StatusConflict, gin.H{"error": "encrypted libraries are not supported by the block upload flow"})
		return
	}

	var req struct {
		ParentDir string `json:"parent_dir"`
	}
	_ = c.ShouldBindJSON(&req)
	parentDir := normalizePath(req.ParentDir)
	if parentDir == "" {
		parentDir = "/"
	}

	session, err := h.db.CreateBlockUploadSession(orgID, userID, repoID, parentDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create upload session"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"session_id": session.SessionID,
		"repo_id":    repoID,
		"parent_dir": session.ParentDir,
		"block_size": WebUploadBlockSize,
		"expires_at": session.ExpiresAt.Unix(),
	})
}
