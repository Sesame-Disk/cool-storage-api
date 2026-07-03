package v2

import (
	"errors"
	"io"
	"net/http"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/gin-gonic/gin"
)

// WebUploadBlockSize is the DEFAULT plaintext block size for the web
// content-addressed upload flow, used when no config is wired (e.g. tests). The
// effective size is sourced from web_uploads.web_block_upload_block_size_mb via
// FileHandler.webBlockUploadBlockSize(); it MUST match api.uploadBlockSize (8 MB)
// so a file uploaded by the web client splits into the same blocks the rest of
// the system expects on download/streaming and for cross-flow dedup.
const WebUploadBlockSize = 8 * 1024 * 1024

// webBlockUploadBlockSize returns the effective CAS block size (bytes) for this
// handler, sourced from config and falling back to WebUploadBlockSize.
func (h *FileHandler) webBlockUploadBlockSize() int64 {
	if h != nil && h.config != nil {
		return h.config.WebBlockUploadBlockSize()
	}
	return WebUploadBlockSize
}

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
// POST /api/v2/repos/:repo_id/block-upload-session/ (also mounted under /api2/)
func (h *FileHandler) CreateBlockUploadSession(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	if h.config == nil || !h.config.WebUploads.EnableWebBlockUpload {
		c.JSON(http.StatusNotFound, gin.H{"error": "web block upload is not enabled"})
		return
	}
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

	// The body is optional (defaults parent_dir to "/"), but a non-empty body that
	// is malformed JSON should be a clear 400 rather than a silent default — only
	// an empty body (io.EOF) is treated as "no parent_dir given". `size` is the
	// client-declared file size (the browser knows file.size); it lets us fail
	// fast before any hashing/upload and is re-checked against the manifest at
	// commit.
	var req struct {
		ParentDir string `json:"parent_dir"`
		Size      int64  `json:"size"`
	}
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	parentDir := normalizePath(req.ParentDir)
	if parentDir == "" {
		parentDir = "/"
	}
	if req.Size < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid size"})
		return
	}

	// When the per-session staging ceiling is enabled, `size` is REQUIRED so the
	// exact per-file bound (this fail-fast check + the commit's
	// manifest.size == expected_size guard) always applies — a client that omits it
	// could otherwise only be caught by the looser staged-block ledger backstop.
	if ceiling := h.config.EffectiveMaxStagedBytesPerSession(); ceiling > 0 {
		if req.Size == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "size is required"})
			return
		}
		// Fail fast: a declared size over the per-session ceiling (the maximum
		// web-block file size) is rejected before any hashing/upload (item 1).
		if req.Size > ceiling {
			metrics.BlockUploadSessionAdmissionRejectionsTotal.WithLabelValues("staged_blocks").Inc()
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{
				"error":    "file exceeds the maximum web-block upload size",
				"max_size": ceiling,
			})
			return
		}
	}

	session, err := h.db.CreateAdmittedBlockUploadSession(
		orgID, userID, repoID, parentDir, req.Size,
		h.config.WebUploads.MaxUncommittedBlockSessionsPerUser,
	)
	if err != nil {
		if errors.Is(err, db.ErrBlockUploadSessionSlotsExhausted) {
			metrics.BlockUploadSessionAdmissionRejectionsTotal.WithLabelValues("max_sessions").Inc()
			c.Header("Retry-After", "5")
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many concurrent uploads; retry shortly"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create upload session"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"session_id": session.SessionID,
		"repo_id":    repoID,
		"parent_dir": session.ParentDir,
		"block_size": h.webBlockUploadBlockSize(),
		"expires_at": session.ExpiresAt.Unix(),
	})
}
