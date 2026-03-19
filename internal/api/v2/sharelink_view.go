package v2

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	htmltemplate "html/template"
	"log"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/crypto"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/Sesame-Disk/sesamefs/internal/streaming"
	"github.com/Sesame-Disk/sesamefs/internal/templates"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

// ShareLinkViewHandler serves the public share link pages and APIs
type ShareLinkViewHandler struct {
	db             *db.DB
	config         *config.Config
	storage        *storage.S3Store
	storageManager *storage.Manager
	tokenCreator   TokenCreator
	serverURL      string
	// bundleMap maps entry point names (e.g. "sharedDirView") to hashed filenames
	jsBundleMap  map[string]string
	cssBundleMap map[string]string
}

// NewShareLinkViewHandler creates a new ShareLinkViewHandler and scans frontend bundles
func NewShareLinkViewHandler(database *db.DB, cfg *config.Config, s3Store *storage.S3Store, storageManager *storage.Manager, tokenCreator TokenCreator, serverURL string) *ShareLinkViewHandler {
	h := &ShareLinkViewHandler{
		db:             database,
		config:         cfg,
		storage:        s3Store,
		storageManager: storageManager,
		tokenCreator:   tokenCreator,
		serverURL:      serverURL,
	}
	h.jsBundleMap = scanBundles("./frontend/build/static/js", ".js")
	h.cssBundleMap = scanBundles("./frontend/build/static/css", ".css")
	return h
}

// scanBundles scans a directory for hashed bundle files and returns a map
// of entry name -> hashed filename (e.g. "sharedDirView" -> "sharedDirView.ef3d8149.js")
func scanBundles(dir, ext string) map[string]string {
	result := make(map[string]string)
	entries, err := os.ReadDir(dir)
	if err != nil {
		slog.Warn("Failed to scan bundle directory, using fallback bundle names", "dir", dir, "error", err)
		// Return fallback bundle names when directory scan fails
		// These are the bundle names from the frontend build
		if ext == ".js" {
			return getJSBundleFallbacks()
		}
		if ext == ".css" {
			return getCSSBundleFallbacks()
		}
		return result
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, ext) && !strings.HasSuffix(name, ".map") && !strings.HasSuffix(name, ".LICENSE.txt") {
			// Extract entry name: "sharedDirView.ef3d8149.js" -> "sharedDirView"
			parts := strings.SplitN(name, ".", 2)
			if len(parts) >= 2 {
				result[parts[0]] = name
			}
		}
	}
	return result
}

// getJSBundleFallbacks returns hardcoded JS bundle filenames
// These should be updated when the frontend is rebuilt
func getJSBundleFallbacks() map[string]string {
	return map[string]string{
		"runtime":                   "runtime.b5726b5c.js",
		"commons":                   "commons.e950012e.js",
		"sharedDirView":             "sharedDirView.ef3d8149.js",
		"sharedFileViewAudio":       "sharedFileViewAudio.cedd033e.js",
		"sharedFileViewDocument":    "sharedFileViewDocument.c3f72eff.js",
		"sharedFileViewImage":       "sharedFileViewImage.9d0dda04.js",
		"sharedFileViewMarkdown":    "sharedFileViewMarkdown.f8135e49.js",
		"sharedFileViewPDF":         "sharedFileViewPDF.a00415f0.js",
		"sharedFileViewSdoc":        "sharedFileViewSdoc.00bab9a5.js",
		"sharedFileViewSpreadsheet": "sharedFileViewSpreadsheet.ea813efa.js",
		"sharedFileViewSVG":         "sharedFileViewSVG.5fd43385.js",
		"sharedFileViewText":        "sharedFileViewText.757e8d1a.js",
		"sharedFileViewUnknown":     "sharedFileViewUnknown.a0e468e0.js",
		"sharedFileViewVideo":       "sharedFileViewVideo.6af2fa31.js",
		"uploadLink":                "uploadLink.5d49e522.js",
	}
}

// getCSSBundleFallbacks returns hardcoded CSS bundle filenames
func getCSSBundleFallbacks() map[string]string {
	return map[string]string{
		"commons":                   "commons.82d1af8c.css",
		"sharedDirView":             "sharedDirView.b715f1e6.css",
		"sharedFileViewSpreadsheet": "sharedFileViewSpreadsheet.ff1ddac7.css",
		"uploadLink":                "uploadLink.d59e882a.css",
	}
}

// shareLinkData holds the resolved share link info for rendering
type shareLinkData struct {
	token       string
	orgID       string
	libraryID   string
	filePath    string
	permission  string
	createdBy   string
	creatorName string
	createdAt   time.Time
	// isExpired: link has exceeded its validity (time, max downloads).
	isExpired bool
	// isDisabled: link was explicitly disabled by an admin action (user/org deactivated or deleted).
	// Semantically distinct from expiration — the link can be re-enabled on reactivation.
	isDisabled   bool
	repoName     string
	commitID     string
	isDir        bool
	targetEntry  *FSEntry
	passwordHash string
	singleUse    bool
	// Parsed permissions (handles both string and JSON formats)
	canEdit     bool
	canDownload bool
	canUpload   bool
	// isDirShareLink indicates this file is being accessed via a directory share link
	// (i.e., /d/:token/files/?p=path rather than /d/:token directly)
	isDirShareLink bool
	// fileSubPath is the relative path within the shared directory (the ?p= parameter)
	fileSubPath string
}

// resolveShareLink looks up and validates a share link token from the unified share_links table.
// When countView is true, it increments the view counter.
func (h *ShareLinkViewHandler) resolveShareLink(token string, countView bool) (*shareLinkData, error) {
	var orgID, libraryID, filePath, permission, createdBy, passwordHash string
	var expiresAt *time.Time
	var createdAt time.Time
	var downloadCount, uploadCount int
	var maxDownloads *int
	var active, singleUse bool

	err := h.db.Session().Query(`
		SELECT org_id, library_id, file_path, permission, created_by, expires_at,
		       download_count, upload_count, max_downloads, password_hash, active, single_use, created_at
		FROM share_links WHERE link_token = ?
	`, token).Scan(&orgID, &libraryID, &filePath, &permission, &createdBy, &expiresAt,
		&downloadCount, &uploadCount, &maxDownloads, &passwordHash, &active, &singleUse, &createdAt)
	if err != nil {
		return nil, fmt.Errorf("share link not found")
	}

	// active=false means the link was disabled by an admin (user/org deactivated or deleted).
	// This is semantically distinct from expiration — a disabled link can be re-enabled on reactivation.
	isDisabled := !active

	// Check time expiration and download limit (true "expired" states, unrelated to admin disable)
	isExpired := false
	if expiresAt != nil && time.Now().After(*expiresAt) {
		isExpired = true
	}
	if maxDownloads != nil && downloadCount >= *maxDownloads {
		isExpired = true
	}

	if countView {
		// Increment view_count (fire-and-forget, approximate counter)
		now := time.Now()
		go func() {
			if err := incrementShareLinkCounterDualWrite(h.db, token, "view_count", now); err != nil {
				log.Printf("[resolveShareLink] failed to update view_count for token %s: %v", token, err)
			}
		}()
	}

	// Get library name and head commit ID
	var repoName, commitID string
	h.db.Session().Query(`SELECT name, head_commit_id FROM libraries WHERE org_id = ? AND library_id = ?`, orgID, libraryID).Scan(&repoName, &commitID)
	if repoName == "" {
		repoName = "Shared"
	}

	// Parse permissions - handles both JSON and legacy string formats
	canEdit, canDownload, canUpload := parseShareLinkPermission(permission)

	// Look up creator name
	var creatorName, creatorEmail string
	h.db.Session().Query(`SELECT name, email FROM users WHERE org_id = ? AND user_id = ?`, orgID, createdBy).Scan(&creatorName, &creatorEmail)
	if creatorName == "" {
		creatorName = creatorEmail
	}
	if creatorName == "" {
		creatorName = createdBy
	}

	// Creator/org lifecycle is enforced via the `active` flag on share links:
	// when a user/org is deactivated or deleted, their share links are set active=false.
	// No runtime status queries needed here.

	return &shareLinkData{
		token:        token,
		orgID:        orgID,
		libraryID:    libraryID,
		filePath:     filePath,
		permission:   permission,
		createdBy:    createdBy,
		creatorName:  creatorName,
		createdAt:    createdAt,
		isExpired:    isExpired,
		isDisabled:   isDisabled,
		repoName:     repoName,
		commitID:     commitID,
		canEdit:      canEdit,
		canDownload:  canDownload,
		canUpload:    canUpload,
		passwordHash: passwordHash,
		singleUse:    singleUse,
	}, nil
}

// unavailableJSON returns the appropriate error payload based on why the link is unavailable.
func (sl *shareLinkData) unavailableJSON() gin.H {
	if sl.isDisabled {
		return gin.H{"error": "share link has been disabled"}
	}
	return gin.H{"error": "share link has expired"}
}

// parseShareLinkPermission parses permission which can be either:
// - A simple string: "download", "preview_download", "preview_only", "upload", "edit"
// - A JSON object: {"can_edit":false,"can_download":true,"can_upload":false}
func parseShareLinkPermission(permission string) (canEdit, canDownload, canUpload bool) {
	// Try parsing as JSON first
	if strings.HasPrefix(permission, "{") {
		var perms struct {
			CanEdit     bool `json:"can_edit"`
			CanDownload bool `json:"can_download"`
			CanUpload   bool `json:"can_upload"`
		}
		if err := json.Unmarshal([]byte(permission), &perms); err == nil {
			return perms.CanEdit, perms.CanDownload, perms.CanUpload
		}
	}

	// Handle string format
	switch permission {
	case "edit":
		return true, true, true
	case "upload":
		return false, false, true
	case "download", "preview_download":
		return false, true, false
	case "preview_only":
		return false, false, false
	default:
		// Default to download allowed for backwards compatibility
		return false, true, false
	}
}

// ServeShareLinkPage handles GET /d/:token
func (h *ShareLinkViewHandler) ServeShareLinkPage(c *gin.Context) {
	token := c.Param("token")

	sl, err := h.resolveShareLink(token, true)
	if err != nil {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusNotFound, errorPageHTML("Not Found", "This share link does not exist."))
		return
	}

	if sl.isExpired || sl.isDisabled {
		c.Header("Content-Type", "text/html; charset=utf-8")
		if sl.isDisabled {
			c.String(http.StatusGone, errorPageHTML("Link Disabled", "This share link has been disabled by an administrator."))
		} else {
			c.String(http.StatusGone, errorPageHTML("Link Expired", "This share link has expired or reached its download limit."))
		}
		return
	}

	// Determine if this is a file or directory share
	fsHelper := NewFSHelper(h.db)
	rootFSID, _, err := fsHelper.GetRootFSID(sl.libraryID)
	if err != nil {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusInternalServerError, errorPageHTML("Error", "Failed to access the shared library."))
		return
	}

	sharePath := sl.filePath
	if sharePath == "" {
		sharePath = "/"
	}

	isDir := false

	if sharePath == "/" {
		isDir = true
	} else {
		result, err := fsHelper.TraverseToPathFromRoot(sl.libraryID, rootFSID, sharePath)
		if err != nil {
			c.Header("Content-Type", "text/html; charset=utf-8")
			c.String(http.StatusNotFound, errorPageHTML("Not Found", "The shared file or folder could not be found."))
			return
		}
		if result.TargetEntry != nil {
			sl.targetEntry = result.TargetEntry
			isDir = result.TargetEntry.Mode == ModeDir || result.TargetEntry.Mode&0170000 == 040000
		} else {
			// Path not found in the FS tree
			c.Header("Content-Type", "text/html; charset=utf-8")
			c.String(http.StatusNotFound, errorPageHTML("Not Found", "The shared file or folder could not be found."))
			return
		}
	}

	sl.isDir = isDir

	// Check password for download/raw/page access
	passwordOK := h.verifyShareLinkPasswordCookie(c, sl.token, sl.passwordHash)

	// Handle direct download (?dl=1)
	if c.Query("dl") == "1" && !isDir {
		if sl.passwordHash != "" && !passwordOK {
			c.Header("Content-Type", "text/html; charset=utf-8")
			c.String(http.StatusForbidden, errorPageHTML("Password Required", "This share link is password-protected."))
			return
		}
		h.handleShareLinkDownload(c, sl, fsHelper, rootFSID)
		return
	}

	// Handle raw file content (?raw=1) for inline preview (images, PDFs, etc.)
	if c.Query("raw") == "1" && !isDir {
		if sl.passwordHash != "" && !passwordOK {
			c.Header("Content-Type", "text/html; charset=utf-8")
			c.String(http.StatusForbidden, errorPageHTML("Password Required", "This share link is password-protected."))
			return
		}
		h.handleShareLinkRaw(c, sl)
		return
	}

	// Serve the appropriate HTML page
	if isDir {
		h.serveSharedDirPage(c, sl)
	} else {
		h.serveSharedFilePage(c, sl)
	}
}

// handleShareLinkDownload handles ?dl=1 for file share links
func (h *ShareLinkViewHandler) handleShareLinkDownload(c *gin.Context, sl *shareLinkData, fsHelper *FSHelper, rootFSID string) {
	// Check download permission
	if !sl.canDownload {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusForbidden, errorPageHTML("Download Disabled", "Downloading is not allowed for this share link."))
		return
	}

	filename := filepath.Base(sl.filePath)

	// Generate download token using the share link creator's user ID
	downloadToken, err := h.tokenCreator.CreateDownloadToken(sl.orgID, sl.libraryID, sl.filePath, sl.createdBy)
	if err != nil {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusInternalServerError, errorPageHTML("Download Error", "Failed to generate download link."))
		return
	}

	// Increment download_count or, for single-use links, delete from all tables (fire-and-forget)
	go func() {
		if sl.singleUse {
			// Single-use link consumed: delete from all tables so the row doesn't linger.
			// Future accesses return 404 (link not found).
			deleteConsumedShareLink(h.db, sl.token, sl.orgID, sl.libraryID, sl.createdBy, sl.createdAt)
		} else {
			now := time.Now()
			if err := incrementShareLinkCounterDualWrite(h.db, sl.token, "download_count", now); err != nil {
				log.Printf("[handleShareLinkDownload] failed to update download_count for token %s: %v", sl.token, err)
			}
		}
	}()

	downloadURL := getBrowserURL(c, h.serverURL) + "/seafhttp/files/" + downloadToken + "/" + filename
	c.Redirect(http.StatusFound, downloadURL)
}

// handleShareLinkRaw serves the raw file content for inline preview (images, PDFs, videos, etc.)
func (h *ShareLinkViewHandler) handleShareLinkRaw(c *gin.Context, sl *shareLinkData) {
	if sl.targetEntry == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}

	filename := filepath.Base(sl.filePath)
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), "."))

	// Get the file's block IDs and size from the fs_object
	var blockIDs []string
	var fileSize int64
	err := h.db.Session().Query(`
		SELECT block_ids, size_bytes FROM fs_objects WHERE library_id = ? AND fs_id = ?
	`, sl.libraryID, sl.targetEntry.ID).Scan(&blockIDs, &fileSize)
	if err != nil {
		slog.Error("Failed to get file block IDs for share link raw", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get file metadata"})
		return
	}

	blockStore, _, err := h.storageManager.GetHealthyBlockStore("")
	if err != nil {
		slog.Error("Block store not available for share link raw", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "storage not available"})
		return
	}

	// Check if library is encrypted
	var encrypted bool
	h.db.Session().Query(`SELECT encrypted FROM libraries WHERE org_id = ? AND library_id = ?`,
		sl.orgID, sl.libraryID).Scan(&encrypted)

	var fileKey []byte
	if encrypted {
		fileKey = GetDecryptSessions().GetFileKey(sl.createdBy, sl.libraryID)
		if fileKey == nil {
			c.JSON(http.StatusForbidden, gin.H{"error": "library is encrypted but not unlocked"})
			return
		}
	}

	// ETag-based cache validation: fs_id changes on every file update
	if setCacheHeaders(c, sl.targetEntry.ID) {
		return
	}

	// Determine MIME type from extension
	mimeType := mime.TypeByExtension("." + ext)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	// Batch resolve all block IDs upfront to avoid per-block Cassandra queries
	resolvedIDs := streaming.BatchResolveBlockIDs(h.db, sl.orgID, blockIDs)

	var fileKeyParam []byte
	if encrypted {
		fileKeyParam = fileKey
	}

	ctx := c.Request.Context()

	// For video/audio files, use BlockReadSeeker so http.ServeContent can handle
	// Range requests (HTTP 206) without buffering the entire file. Only O(1 block) RAM.
	if isVideoFile(ext) || isAudioFile(ext) {
		blockSizes, err := streaming.QueryBlockSizes(ctx, h.db, sl.orgID, blockStore, resolvedIDs)
		if err != nil {
			slog.Error("Failed to query block sizes for share link", "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read file metadata"})
			return
		}

		rs := streaming.NewBlockReadSeeker(ctx, blockStore, resolvedIDs, blockSizes, fileSize, fileKeyParam)
		c.Header("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, filename))
		http.ServeContent(c.Writer, c.Request, filename, time.Time{}, rs)
		return
	}

	// Non-video/audio: stream block-by-block, O(block_size) RAM
	c.Header("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, filename))
	c.Header("Content-Type", mimeType)
	if fileSize > 0 && !encrypted {
		c.Header("Content-Length", strconv.FormatInt(fileSize, 10))
	}
	c.Status(http.StatusOK)

	streaming.StreamBlocks(c, ctx, blockStore, resolvedIDs, fileKeyParam, "ShareLinkRaw")
}

// serveSharedDirPage renders the shared directory view
func (h *ShareLinkViewHandler) serveSharedDirPage(c *gin.Context, sl *shareLinkData) {
	dirName := filepath.Base(sl.filePath)
	if sl.filePath == "/" || sl.filePath == "" {
		dirName = sl.repoName
	}

	// Get browsing path from query parameter (for navigating into subdirectories)
	relativePath := c.DefaultQuery("p", "/")
	if relativePath == "" {
		relativePath = "/"
	}
	mode := c.DefaultQuery("mode", "list")
	thumbnailSize := 48

	// Build the zipped breadcrumb path array
	// zipped is [{name, path}, ...] for breadcrumb navigation
	zippedJSON := buildZippedPath(dirName, relativePath)

	// dirPath is the full filesystem path: sharePath + relativePath
	dirPath := sl.filePath
	if dirPath == "" || dirPath == "/" {
		dirPath = relativePath
	} else if relativePath != "/" {
		dirPath = strings.TrimSuffix(sl.filePath, "/") + "/" + strings.TrimPrefix(relativePath, "/")
	}

	passwordVerified := h.verifyShareLinkPasswordCookie(c, sl.token, sl.passwordHash)
	noPassword := sl.passwordHash == "" || passwordVerified
	needPassword := sl.passwordHash != "" && !passwordVerified

	pageOptions := fmt.Sprintf(`{
		"token": %q,
		"repoID": %q,
		"repoName": %q,
		"path": %q,
		"dirName": %q,
		"dirPath": %q,
		"relativePath": %q,
		"mode": %q,
		"thumbnailSize": %d,
		"zipped": %s,
		"canDownload": %t,
		"canUpload": %t,
		"sharedBy": %q,
		"noPassword": %t,
		"needPassword": %t,
		"noQuota": false,
		"trafficOverLimit": false,
		"enableVideoThumbnail": false,
		"permissions": {"can_edit": %t, "can_download": %t, "can_upload": %t}
	}`,
		sl.token,
		sl.libraryID,
		html.EscapeString(sl.repoName),
		sl.filePath,
		html.EscapeString(dirName),
		dirPath,
		relativePath,
		mode,
		thumbnailSize,
		zippedJSON,
		sl.canDownload,
		sl.canUpload,
		html.EscapeString(sl.creatorName),
		noPassword,
		needPassword,
		sl.canEdit,
		sl.canDownload,
		sl.canUpload,
	)

	htmlPage := h.buildSharePageHTML("sharedDirView", dirName+" - SesameFS", pageOptions)
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, htmlPage)
}

// buildZippedPath builds the breadcrumb JSON array for shared dir navigation
// Returns JSON like [{"name":"Root","path":"/"},{"name":"subfolder","path":"/subfolder/"}]
func buildZippedPath(rootName, relativePath string) string {
	type pathSegment struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}

	segments := []pathSegment{{Name: rootName, Path: "/"}}

	if relativePath != "/" && relativePath != "" {
		// Split path and build cumulative breadcrumbs
		parts := strings.Split(strings.Trim(relativePath, "/"), "/")
		cumPath := "/"
		for _, part := range parts {
			if part == "" {
				continue
			}
			cumPath += part + "/"
			segments = append(segments, pathSegment{Name: part, Path: cumPath})
		}
	}

	data, err := json.Marshal(segments)
	if err != nil {
		return `[{"name":"Root","path":"/"}]`
	}
	return string(data)
}

// readFileContentAsText reads the file content from block storage and returns it as a string.
// Used for embedding text file content directly in page options (for the text/markdown React views).
// Returns empty string on any error. Limited to 1MB to avoid huge page payloads.
func (h *ShareLinkViewHandler) readFileContentAsText(sl *shareLinkData) string {
	if sl.targetEntry == nil {
		return ""
	}

	const maxTextSize = 1 * 1024 * 1024 // 1MB limit for inline text content

	var blockIDs []string
	var fileSize int64
	err := h.db.Session().Query(`
		SELECT block_ids, size_bytes FROM fs_objects WHERE library_id = ? AND fs_id = ?
	`, sl.libraryID, sl.targetEntry.ID).Scan(&blockIDs, &fileSize)
	if err != nil {
		slog.Error("Failed to get file block IDs for text content", "error", err)
		return ""
	}

	if fileSize > maxTextSize {
		return ""
	}

	blockStore, _, err := h.storageManager.GetHealthyBlockStore("")
	if err != nil {
		return ""
	}

	// Check if library is encrypted
	var encrypted bool
	h.db.Session().Query(`SELECT encrypted FROM libraries WHERE org_id = ? AND library_id = ?`,
		sl.orgID, sl.libraryID).Scan(&encrypted)

	var fileKey []byte
	if encrypted {
		fileKey = GetDecryptSessions().GetFileKey(sl.createdBy, sl.libraryID)
		if fileKey == nil {
			return ""
		}
	}

	ctx := context.Background()
	resolvedIDs := streaming.BatchResolveBlockIDs(h.db, sl.orgID, blockIDs)
	var buf strings.Builder
	for idx := range blockIDs {
		internalID := resolvedIDs[idx]

		if encrypted && fileKey != nil {
			blockData, err := blockStore.GetBlock(ctx, internalID)
			if err != nil {
				return ""
			}
			blockData, err = crypto.DecryptBlock(blockData, fileKey)
			if err != nil {
				return ""
			}
			buf.Write(blockData)
		} else {
			blockData, err := blockStore.GetBlock(ctx, internalID)
			if err != nil {
				return ""
			}
			buf.Write(blockData)
		}
	}

	return buf.String()
}

// serveSharedFilePage renders the shared file view
func (h *ShareLinkViewHandler) serveSharedFilePage(c *gin.Context, sl *shareLinkData) {
	filename := filepath.Base(sl.filePath)
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), "."))

	// Check password protection BEFORE any rendering
	if sl.passwordHash != "" && !h.verifyShareLinkPasswordCookie(c, sl.token, sl.passwordHash) {
		h.servePasswordPage(c, sl.token, "d", filename)
		return
	}

	// Build raw file path for preview (serves actual file content with correct MIME type)
	// For files inside a shared directory, we need /d/{token}/files/?p={path}&raw=1
	// For direct file share links, we use /d/{token}?raw=1
	var rawPath string
	if sl.isDirShareLink {
		rawPath = fmt.Sprintf("/d/%s/files/?p=%s&raw=1", sl.token, url.QueryEscape(sl.fileSubPath))
	} else {
		rawPath = fmt.Sprintf("/d/%s?raw=1", sl.token)
	}

	var fileSize int64
	if sl.targetEntry != nil {
		fileSize = sl.targetEntry.Size
	}

	// For PDFs and certain file types, use server-rendered preview page with embedded viewer
	if useEmbeddedPreview(ext) {
		htmlPage := h.buildEmbeddedPreviewPage(filename, ext, rawPath, fileSize, sl)
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusOK, htmlPage)
		return
	}

	// For document types (docx, xlsx, pptx, etc.), embed OnlyOffice viewer in the preview page
	if h.config.OnlyOffice.Enabled && isOnlyOfficeViewable(ext) {
		htmlPage, err := h.buildOnlyOfficePreviewPage(filename, ext, fileSize, sl)
		if err == nil {
			c.Header("Content-Type", "text/html; charset=utf-8")
			c.String(http.StatusOK, htmlPage)
			return
		}
		// Fall through to React bundle if OnlyOffice preview fails
		slog.Warn("OnlyOffice preview failed, falling back to React bundle", "file", filename, "error", err)
	}

	bundleName := extensionToBundleName(ext)

	// For unknown file types (no preview), show a clean download page instead of a broken React bundle
	if bundleName == "sharedFileViewUnknown" {
		htmlPage := h.buildEmbeddedPreviewPage(filename, ext, "", fileSize, sl)
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusOK, htmlPage)
		return
	}

	// For text files, read file content and embed it directly (the React component expects fileContent)
	var fileContentJSON string
	if bundleName == "sharedFileViewText" || bundleName == "sharedFileViewMarkdown" {
		content := h.readFileContentAsText(sl)
		contentBytes, err := json.Marshal(content)
		if err != nil {
			fileContentJSON = `""`
		} else {
			fileContentJSON = string(contentBytes)
		}
	} else {
		fileContentJSON = `""`
	}

	passwordVerified := h.verifyShareLinkPasswordCookie(c, sl.token, sl.passwordHash)
	noPassword := sl.passwordHash == "" || passwordVerified
	needPassword := sl.passwordHash != "" && !passwordVerified

	pageOptions := fmt.Sprintf(`{
		"sharedToken": %q,
		"repoID": %q,
		"commitID": %q,
		"filePath": %q,
		"fileName": %q,
		"fileSize": %d,
		"rawPath": %q,
		"canDownload": %t,
		"canEdit": %t,
		"sharedBy": %q,
		"noPassword": %t,
		"needPassword": %t,
		"trafficOverLimit": false,
		"fileExt": %q,
		"siteName": "SesameFS",
		"enableWatermark": false,
		"zipped": null,
		"enableShareLinkReportAbuse": false,
		"fileContent": %s,
		"err": ""
	}`,
		sl.token,
		sl.libraryID,
		sl.commitID,
		sl.filePath,
		html.EscapeString(filename),
		fileSize,
		rawPath,
		sl.canDownload,
		sl.canEdit,
		html.EscapeString(sl.creatorName),
		noPassword,
		needPassword,
		ext,
		fileContentJSON,
	)

	htmlPage := h.buildSharePageHTML(bundleName, filename+" - SesameFS", pageOptions)
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, htmlPage)
}

// useEmbeddedPreview returns true for file types that should use the server-rendered
// embedded preview page instead of React bundles
func useEmbeddedPreview(ext string) bool {
	switch ext {
	case "pdf":
		return true
	case "png", "jpg", "jpeg", "gif", "bmp", "webp", "svg", "ico", "tiff", "tif":
		return true
	case "mp4", "webm", "ogg", "mov":
		return true
	case "mp3", "wav", "flac", "aac":
		return true
	}
	return false
}

// buildEmbeddedPreviewPage generates a clean HTML page with embedded file preview
func (h *ShareLinkViewHandler) buildEmbeddedPreviewPage(filename, ext, rawPath string, fileSize int64, sl *shareLinkData) string {
	var downloadLink string
	if sl.isDirShareLink {
		downloadLink = fmt.Sprintf("/d/%s/files/?p=%s&dl=1", sl.token, url.QueryEscape(sl.fileSubPath))
	} else {
		downloadLink = fmt.Sprintf("/d/%s?dl=1", sl.token)
	}

	previewContent := buildPreviewContent(ext, rawPath, filename)

	var downloadBtn string
	if sl.canDownload {
		fileSizeStr := formatFileSize(fileSize)
		downloadBtn = fmt.Sprintf(`<a href="%s" class="btn-download">Download (%s)</a>`, html.EscapeString(downloadLink), fileSizeStr)
	}

	data := templates.SharePreviewData{
		Filename:       filename,
		SharedBy:       sl.creatorName,
		DownloadBtn:    htmltemplate.HTML(downloadBtn),
		PreviewContent: htmltemplate.HTML(previewContent),
	}

	s, err := templates.RenderString("share_file_preview.html", data)
	if err != nil {
		log.Printf("[buildEmbeddedPreviewPage] template error: %v", err)
		return "<html><body><h1>Internal Error</h1></body></html>"
	}
	return s
}

// formatFileSize formats bytes into a human-readable string
func formatFileSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	if size < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(size)/1024)
	}
	if size < 1024*1024*1024 {
		return fmt.Sprintf("%.1f MB", float64(size)/(1024*1024))
	}
	return fmt.Sprintf("%.1f GB", float64(size)/(1024*1024*1024))
}

// isOnlyOfficeViewable checks if a file extension can be viewed with OnlyOffice
func isOnlyOfficeViewable(ext string) bool {
	switch ext {
	case "doc", "docx", "odt", "fodt", "rtf",
		"xls", "xlsx", "ods", "fods", "csv",
		"ppt", "pptx", "odp", "fodp",
		"pdf":
		return true
	}
	return false
}

// buildOnlyOfficePreviewPage generates an embedded preview page with the OnlyOffice viewer
// inside the standard share link layout (header + preview container), not a full-page editor.
func (h *ShareLinkViewHandler) buildOnlyOfficePreviewPage(filename, ext string, fileSize int64, sl *shareLinkData) (string, error) {
	// Generate download token so OnlyOffice server can fetch the document
	downloadToken, err := h.tokenCreator.CreateDownloadToken(sl.orgID, sl.libraryID, sl.filePath, sl.createdBy)
	if err != nil {
		return "", fmt.Errorf("failed to create download token: %w", err)
	}

	// Use OnlyOffice-specific server URL for download (URL that OnlyOffice can reach)
	ooServerURL := h.config.OnlyOffice.ServerURL
	if ooServerURL == "" {
		ooServerURL = h.serverURL
	}
	downloadURL := ooServerURL + "/seafhttp/files/" + downloadToken + "/" + filename

	// Generate document key
	fileID := ""
	if sl.targetEntry != nil {
		fileID = sl.targetEntry.ID
	}
	docKey := generateDocKey(sl.libraryID, sl.filePath, fileID)

	// Build OnlyOffice config in view-only mode
	docConfig := OnlyOfficeConfig{
		Document: OnlyOfficeDocument{
			FileType: ext,
			Key:      docKey,
			Title:    filename,
			URL:      downloadURL,
			Permissions: &OnlyOfficePermissions{
				Edit:      false,
				Download:  sl.canDownload,
				Print:     sl.canDownload,
				Copy:      true,
				Review:    false,
				Comment:   false,
				FillForms: false,
			},
		},
		DocumentType: getDocumentType(filename),
		EditorConfig: OnlyOfficeEditorConfig{
			Mode: "view",
			User: OnlyOfficeUser{
				ID:   "anonymous",
				Name: "Anonymous",
			},
			Customization: &OnlyOfficeCustomization{
				Forcesave:  false,
				SubmitForm: false,
			},
		},
	}

	// Sign JWT if secret is configured
	if h.config.OnlyOffice.JWTSecret != "" {
		ooHandler := &OnlyOfficeHandler{
			db:     h.db,
			config: h.config,
		}
		token, signErr := ooHandler.signJWT(docConfig)
		if signErr == nil {
			docConfig.Token = token
		}
	}

	configJSON, err := json.Marshal(docConfig)
	if err != nil {
		return "", fmt.Errorf("failed to marshal OnlyOffice config: %w", err)
	}

	downloadLink := fmt.Sprintf("/d/%s?dl=1", sl.token)
	var downloadBtn string
	if sl.canDownload {
		fileSizeStr := formatFileSize(fileSize)
		downloadBtn = fmt.Sprintf(`<a href="%s" class="btn-download">Download (%s)</a>`, html.EscapeString(downloadLink), fileSizeStr)
	}

	data := templates.ShareOOPreviewData{
		Filename:    filename,
		SharedBy:    sl.creatorName,
		DownloadBtn: htmltemplate.HTML(downloadBtn),
		APIJSURL:    h.config.OnlyOffice.APIJSURL,
		ConfigJSON:  htmltemplate.JS(configJSON),
	}

	htmlPage, renderErr := templates.RenderString("share_onlyoffice_preview.html", data)
	if renderErr != nil {
		return "", fmt.Errorf("failed to render template: %w", renderErr)
	}
	return htmlPage, nil
}

// serveSharedFileOnlyOffice renders the OnlyOffice viewer for a shared file
func (h *ShareLinkViewHandler) serveSharedFileOnlyOffice(c *gin.Context, sl *shareLinkData, filename, ext string) {
	// Generate download token so OnlyOffice server can fetch the document
	downloadToken, err := h.tokenCreator.CreateDownloadToken(sl.orgID, sl.libraryID, sl.filePath, sl.createdBy)
	if err != nil {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusInternalServerError, errorPageHTML("Error", "Failed to generate document access token."))
		return
	}

	// Use OnlyOffice-specific server URL for download (URL that OnlyOffice can reach)
	ooServerURL := h.config.OnlyOffice.ServerURL
	if ooServerURL == "" {
		ooServerURL = h.serverURL
	}
	downloadURL := ooServerURL + "/seafhttp/files/" + downloadToken + "/" + filename

	// Generate document key
	fileID := ""
	if sl.targetEntry != nil {
		fileID = sl.targetEntry.ID
	}
	docKey := generateDocKey(sl.libraryID, sl.filePath, fileID)

	// Build OnlyOffice config in view-only mode
	docConfig := OnlyOfficeConfig{
		Document: OnlyOfficeDocument{
			FileType: ext,
			Key:      docKey,
			Title:    filename,
			URL:      downloadURL,
			Permissions: &OnlyOfficePermissions{
				Edit:      false,
				Download:  sl.canDownload,
				Print:     sl.canDownload,
				Copy:      true,
				Review:    false,
				Comment:   false,
				FillForms: false,
			},
		},
		DocumentType: getDocumentType(filename),
		EditorConfig: OnlyOfficeEditorConfig{
			Mode: "view",
			User: OnlyOfficeUser{
				ID:   "anonymous",
				Name: "Anonymous",
			},
			Customization: &OnlyOfficeCustomization{
				Forcesave:  false,
				SubmitForm: false,
			},
		},
	}

	// Sign JWT if secret is configured
	if h.config.OnlyOffice.JWTSecret != "" {
		ooHandler := &OnlyOfficeHandler{
			db:     h.db,
			config: h.config,
		}
		token, err := ooHandler.signJWT(docConfig)
		if err == nil {
			docConfig.Token = token
		}
	}

	htmlPage := onlyOfficeEditorHTML(h.config.OnlyOffice.APIJSURL, docConfig, filename)
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, htmlPage)
}

// buildSharePageHTML generates the HTML page that loads the appropriate bundle
func (h *ShareLinkViewHandler) buildSharePageHTML(bundleName, title, pageOptionsJSON string) string {
	data := h.buildSharePageData(bundleName, title, pageOptionsJSON)
	s, err := templates.RenderString("share_page.html", data)
	if err != nil {
		log.Printf("[buildSharePageHTML] template error: %v", err)
		return "<html><body><h1>Internal Error</h1></body></html>"
	}
	return s
}

// buildSharePageData builds the template data for share/upload page templates.
func (h *ShareLinkViewHandler) buildSharePageData(bundleName, title, pageOptionsJSON string) templates.SharePageData {
	runtimeJS := h.resolveJSBundle("runtime")
	commonsJS := h.resolveJSBundle("commons")
	entryJS := h.resolveJSBundle(bundleName)
	commonsCSS := h.resolveCSSBundle("commons")
	entryCSS := h.resolveCSSBundle(bundleName)

	var cssLinks []string
	cssLinks = append(cssLinks, "/static/css/seahub.css")
	if commonsCSS != "" {
		cssLinks = append(cssLinks, "/static/css/"+commonsCSS)
	}
	if entryCSS != "" {
		cssLinks = append(cssLinks, "/static/css/"+entryCSS)
	}

	var scriptTags []string
	if runtimeJS != "" {
		scriptTags = append(scriptTags, "/static/js/"+runtimeJS)
	}
	if commonsJS != "" {
		scriptTags = append(scriptTags, "/static/js/"+commonsJS)
	}
	if entryJS != "" {
		scriptTags = append(scriptTags, "/static/js/"+entryJS)
	}

	return templates.SharePageData{
		Title:           title,
		CSSLinks:        cssLinks,
		ScriptTags:      scriptTags,
		PageOptionsJSON: htmltemplate.JS(pageOptionsJSON),
	}
}

func (h *ShareLinkViewHandler) resolveJSBundle(name string) string {
	if f, ok := h.jsBundleMap[name]; ok {
		return f
	}
	return ""
}

func (h *ShareLinkViewHandler) resolveCSSBundle(name string) string {
	if f, ok := h.cssBundleMap[name]; ok {
		return f
	}
	return ""
}

// extensionToBundleName maps a file extension to the appropriate shared view bundle
func extensionToBundleName(ext string) string {
	switch ext {
	case "md", "markdown":
		return "sharedFileViewMarkdown"
	case "txt", "py", "js", "css", "html", "json", "xml", "yaml", "yml",
		"sh", "go", "rs", "java", "c", "cpp", "h", "rb", "php", "sql",
		"conf", "ini", "log", "csv", "tsv":
		return "sharedFileViewText"
	case "png", "jpg", "jpeg", "gif", "bmp", "webp", "ico", "tiff", "tif":
		return "sharedFileViewImage"
	case "mp4", "webm", "ogg", "mov", "avi", "mkv":
		return "sharedFileViewVideo"
	case "mp3", "wav", "flac", "aac", "wma":
		return "sharedFileViewAudio"
	case "pdf":
		return "sharedFileViewPDF"
	case "svg":
		return "sharedFileViewSVG"
	case "doc", "docx", "ppt", "pptx":
		// Office documents require a converter (LibreOffice/OnlyOffice) for in-browser preview.
		// Without one configured, use the download-only view.
		return "sharedFileViewUnknown"
	case "xls", "xlsx":
		return "sharedFileViewUnknown"
	default:
		return "sharedFileViewUnknown"
	}
}

// buildShareLinkFullPath constructs and validates the full path for share link access.
// It ensures the resulting path stays within the share link's base directory,
// preventing path traversal attacks (e.g., ?path=../../secret).
func buildShareLinkFullPath(basePath, subPath string) (string, error) {
	// Clean the base path to ensure a consistent starting point
	cleanBase := path.Clean("/" + basePath)
	if cleanBase == "." || cleanBase == "" {
		cleanBase = "/"
	}

	// Join the base and the subpath
	// Do NOT clean subPath with a leading slash first, as that strips leading ../
	fullPath := path.Join(cleanBase, subPath)

	// Clean the resulting joined path
	cleanFull := path.Clean(fullPath)

	// Check if the resulting path is still within the base path
	if cleanBase != "/" {
		// If it's exactly the base, or it starts with the base + slash
		if cleanFull != cleanBase && !strings.HasPrefix(cleanFull, cleanBase+"/") {
			return "", fmt.Errorf("path traversal detected")
		}
	}

	return cleanFull, nil
}

// ListShareLinkDirents lists directory entries for a shared directory
// GET /api/v2.1/share-links/:token/dirents/
func (h *ShareLinkViewHandler) ListShareLinkDirents(c *gin.Context) {
	token := c.Param("token")

	sl, err := h.resolveShareLink(token, false)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "share link not found"})
		return
	}

	if sl.isExpired || sl.isDisabled {
		c.JSON(http.StatusGone, sl.unavailableJSON())
		return
	}

	if sl.passwordHash != "" && !h.verifyShareLinkPasswordCookie(c, token, sl.passwordHash) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Password required"})
		return
	}

	// Get the requested sub-path within the shared directory
	requestedPath := c.DefaultQuery("path", "/")
	if requestedPath == "" {
		requestedPath = "/"
	}

	// Build the full path with traversal protection
	fullPath, err := buildShareLinkFullPath(sl.filePath, requestedPath)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "invalid path"})
		return
	}

	// Traverse to the directory
	fsHelper := NewFSHelper(h.db)
	rootFSID, _, err := fsHelper.GetRootFSID(sl.libraryID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to access library"})
		return
	}

	var entries []FSEntry
	if fullPath == "/" {
		entries, err = fsHelper.GetDirectoryEntries(sl.libraryID, rootFSID)
	} else {
		result, traverseErr := fsHelper.TraverseToPathFromRoot(sl.libraryID, rootFSID, fullPath)
		if traverseErr != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "directory not found"})
			return
		}
		// If we traversed to a directory, get its entries
		if result.TargetEntry != nil && (result.TargetEntry.Mode == ModeDir || result.TargetEntry.Mode&0170000 == 040000) {
			entries, err = fsHelper.GetDirectoryEntries(sl.libraryID, result.TargetFSID)
		} else if result.TargetEntry == nil && result.TargetFSID != "" {
			// TraverseToPath for root returns TargetEntry=nil but TargetFSID set
			entries, err = fsHelper.GetDirectoryEntries(sl.libraryID, result.TargetFSID)
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": "path is not a directory"})
			return
		}
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list directory"})
		return
	}

	// Build response in Seafile format
	// The frontend (shared-dir-view.js) expects:
	//   For files: file_name, file_path, size, last_modified
	//   For dirs:  folder_name, folder_path, last_modified
	type DirentResponse struct {
		FileName     string `json:"file_name,omitempty"`
		FolderName   string `json:"folder_name,omitempty"`
		FilePath     string `json:"file_path,omitempty"`
		FolderPath   string `json:"folder_path,omitempty"`
		FileSize     int64  `json:"file_size"`
		Size         int64  `json:"size"`
		IsDir        bool   `json:"is_dir"`
		LastModified int64  `json:"last_modified"`
	}

	dirents := make([]DirentResponse, 0, len(entries))
	for _, entry := range entries {
		isDir := entry.Mode == ModeDir || entry.Mode&0170000 == 040000

		// Build the path relative to the share link root
		var entryRelPath string
		if requestedPath == "/" {
			entryRelPath = "/" + entry.Name
		} else {
			entryRelPath = strings.TrimSuffix(requestedPath, "/") + "/" + entry.Name
		}

		// Convert Unix seconds to milliseconds for moment.js compatibility
		// moment(number) interprets as milliseconds, so raw Unix seconds would show wrong dates
		lastModifiedMs := entry.MTime * 1000

		d := DirentResponse{
			FileSize:     entry.Size,
			Size:         entry.Size,
			IsDir:        isDir,
			LastModified: lastModifiedMs,
		}
		if isDir {
			d.FolderName = entry.Name
			d.FolderPath = entryRelPath + "/"
			d.FileName = entry.Name // also set for compatibility
		} else {
			d.FileName = entry.Name
			d.FilePath = entryRelPath
		}
		dirents = append(dirents, d)
	}

	c.JSON(http.StatusOK, gin.H{
		"dirent_list": dirents,
	})
}

// GetShareLinkRepoTags returns the repository tags for a shared directory
// GET /api/v2.1/share-links/:token/repo-tags/
func (h *ShareLinkViewHandler) GetShareLinkRepoTags(c *gin.Context) {
	token := c.Param("token")

	sl, err := h.resolveShareLink(token, false)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "share link not found"})
		return
	}

	if sl.isExpired || sl.isDisabled {
		c.JSON(http.StatusGone, sl.unavailableJSON())
		return
	}

	// Return empty repo_tags array - tags are not typically shown for share links
	// as they're used for personal organization, not sharing
	c.JSON(http.StatusOK, gin.H{
		"repo_tags": []interface{}{},
	})
}

// ServeShareLinkFilePage handles GET /d/:token/files/
// This is the route used when clicking a file inside a shared directory.
// The frontend constructs URLs like: /d/{token}/files/?p=/path/to/file.txt
func (h *ShareLinkViewHandler) ServeShareLinkFilePage(c *gin.Context) {
	token := c.Param("token")
	filePath := c.Query("p")

	sl, err := h.resolveShareLink(token, true)
	if err != nil {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusNotFound, errorPageHTML("Not Found", "This share link does not exist."))
		return
	}

	if sl.isExpired || sl.isDisabled {
		c.Header("Content-Type", "text/html; charset=utf-8")
		if sl.isDisabled {
			c.String(http.StatusGone, errorPageHTML("Link Disabled", "This share link has been disabled by an administrator."))
		} else {
			c.String(http.StatusGone, errorPageHTML("Link Expired", "This share link has expired or reached its download limit."))
		}
		return
	}

	if sl.passwordHash != "" && !h.verifyShareLinkPasswordCookie(c, token, sl.passwordHash) {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusForbidden, errorPageHTML("Password Required", "This share link is password-protected."))
		return
	}

	// Build full path with traversal protection
	if filePath == "" {
		filePath = "/"
	}
	fullPath, err := buildShareLinkFullPath(sl.filePath, filePath)
	if err != nil {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusForbidden, errorPageHTML("Forbidden", "Invalid path."))
		return
	}

	// Override the share link's file path with the specific file
	sl.filePath = fullPath
	sl.isDirShareLink = true
	sl.fileSubPath = filePath

	fsHelper := NewFSHelper(h.db)
	rootFSID, _, err := fsHelper.GetRootFSID(sl.libraryID)
	if err != nil {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusInternalServerError, errorPageHTML("Error", "Failed to access the shared library."))
		return
	}

	result, err := fsHelper.TraverseToPathFromRoot(sl.libraryID, rootFSID, fullPath)
	if err != nil || result.TargetEntry == nil {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusNotFound, errorPageHTML("Not Found", "The shared file could not be found."))
		return
	}
	sl.targetEntry = result.TargetEntry
	sl.isDir = false

	// Handle direct download (?dl=1)
	if c.Query("dl") == "1" {
		h.handleShareLinkDownload(c, sl, fsHelper, rootFSID)
		return
	}

	// Handle raw file content (?raw=1)
	if c.Query("raw") == "1" {
		h.handleShareLinkRaw(c, sl)
		return
	}

	// Serve the file view page
	h.serveSharedFilePage(c, sl)
}

// GetShareLinkZipTask handles GET /api/v2.1/share-link-zip-task/
// Creates a zip download task for a shared directory and returns a zip token.
func (h *ShareLinkViewHandler) GetShareLinkZipTask(c *gin.Context) {
	token := c.Query("share_link_token")
	path := c.DefaultQuery("path", "/")

	sl, err := h.resolveShareLink(token, false)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "share link not found"})
		return
	}

	if sl.isExpired || sl.isDisabled {
		c.JSON(http.StatusGone, sl.unavailableJSON())
		return
	}

	if sl.passwordHash != "" && !h.verifyShareLinkPasswordCookie(c, token, sl.passwordHash) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Password required"})
		return
	}

	if !sl.canDownload {
		c.JSON(http.StatusForbidden, gin.H{"error": "download not permitted"})
		return
	}

	// Build the full path with traversal protection
	fullPath, err := buildShareLinkFullPath(sl.filePath, path)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "invalid path"})
		return
	}

	// Generate a download token for the zip
	// We reuse the download token mechanism — the zip will be created on-the-fly
	zipToken, err := h.tokenCreator.CreateDownloadToken(sl.orgID, sl.libraryID, fullPath, sl.createdBy)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create zip download token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"zip_token": zipToken,
	})
}

// PostShareLinkZipTask handles POST /api/v2.1/share-link-zip-task/
// Creates a zip download task for specific items in a shared directory.
func (h *ShareLinkViewHandler) PostShareLinkZipTask(c *gin.Context) {
	// Same behavior as GET for now — the token approach handles both cases
	h.GetShareLinkZipTask(c)
}

// ServeUploadLinkPage handles GET /u/d/:token
// Renders the upload link page that allows anonymous file uploads.
func (h *ShareLinkViewHandler) ServeUploadLinkPage(c *gin.Context) {
	token := c.Param("token")

	// Resolve the upload link from DB
	var orgID, libraryID, filePath, createdBy, passwordHash string
	var expiresAt *time.Time
	var active bool

	err := h.db.Session().Query(`
		SELECT org_id, library_id, file_path, created_by, password_hash, expires_at, active
		FROM share_links WHERE link_token = ?
	`, token).Scan(&orgID, &libraryID, &filePath, &createdBy, &passwordHash, &expiresAt, &active)
	if err != nil {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusNotFound, errorPageHTML("Not Found", "This upload link does not exist."))
		return
	}

	// Check disabled (admin deactivated owner or org) — distinct from time expiration
	if !active {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusGone, errorPageHTML("Link Disabled", "This upload link has been disabled by an administrator."))
		return
	}

	// Check expiration
	if expiresAt != nil && time.Now().After(*expiresAt) {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusGone, errorPageHTML("Link Expired", "This upload link has expired."))
		return
	}

	// Check password-protected upload links
	needPassword := false
	if passwordHash != "" {
		if !h.verifyUploadLinkPasswordCookie(c, token, passwordHash) {
			needPassword = true
		}
	}

	// Get library name
	var repoName string
	h.db.Session().Query(`SELECT name FROM libraries_by_id WHERE library_id = ?`, libraryID).Scan(&repoName)
	if repoName == "" {
		repoName = "Shared folder"
	}

	// Get uploader display name
	dirName := filepath.Base(filePath)
	if filePath == "/" || filePath == "" {
		dirName = repoName
	}

	// Get creator info
	var creatorName, creatorEmail string
	h.db.Session().Query(`SELECT name, email FROM users WHERE org_id = ? AND user_id = ?`, orgID, createdBy).Scan(&creatorName, &creatorEmail)
	if creatorName == "" {
		creatorName = creatorEmail
	}
	if creatorName == "" {
		creatorName = "Unknown"
	}

	// Build shared_by object matching frontend expectations
	sharedByJSON := fmt.Sprintf(`{"name": %q, "avatar": ""}`, creatorName)

	// Build pageOptions for the uploadLink bundle
	pageOptions := fmt.Sprintf(`{
		"token": %q,
		"repoID": %q,
		"path": %q,
		"dirName": %q,
		"sharedBy": %s,
		"noQuota": false,
		"maxUploadFileSize": null,
		"needPassword": %t
	}`,
		token,
		libraryID,
		filePath,
		html.EscapeString(dirName),
		sharedByJSON,
		needPassword,
	)

	// Use buildSharePageHTML but with "uploadLink" bundle and window.uploadLink instead of window.shared.pageOptions
	htmlPage := h.buildUploadLinkPageHTML("uploadLink", dirName+" - Upload - SesameFS", pageOptions)
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, htmlPage)
}

// buildUploadLinkPageHTML is similar to buildSharePageHTML but injects window.uploadLink
func (h *ShareLinkViewHandler) buildUploadLinkPageHTML(bundleName, title, pageOptionsJSON string) string {
	data := h.buildSharePageData(bundleName, title, pageOptionsJSON)
	s, err := templates.RenderString("upload_link_page.html", data)
	if err != nil {
		log.Printf("[buildUploadLinkPageHTML] template error: %v", err)
		return "<html><body><h1>Internal Error</h1></body></html>"
	}
	return s
}

// GetUploadLinkUploadURL handles GET /api/v2.1/upload-links/:token/upload/
// Returns the upload URL for an upload link.
func (h *ShareLinkViewHandler) GetUploadLinkUploadURL(c *gin.Context) {
	token := c.Param("token")

	// Resolve upload link
	var orgID, libraryID, filePath, createdBy string
	var expiresAt *time.Time
	var active bool
	err := h.db.Session().Query(`
		SELECT org_id, library_id, file_path, created_by, expires_at, active
		FROM share_links WHERE link_token = ?
	`, token).Scan(&orgID, &libraryID, &filePath, &createdBy, &expiresAt, &active)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "upload link not found"})
		return
	}

	// Check disabled (admin) and expiration separately
	if !active {
		c.JSON(http.StatusGone, gin.H{"error": "upload link has been disabled"})
		return
	}
	if expiresAt != nil && time.Now().After(*expiresAt) {
		c.JSON(http.StatusGone, gin.H{"error": "upload link has expired"})
		return
	}

	// Generate an upload URL using the seafhttp upload mechanism
	// Create a token that the file-upload handler will accept
	uploadToken, err := h.tokenCreator.CreateUploadToken(orgID, libraryID, filePath, createdBy)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate upload URL"})
		return
	}

	uploadURL := getBrowserURL(c, h.serverURL) + "/seafhttp/upload-api/" + uploadToken
	c.JSON(http.StatusOK, gin.H{
		"upload_link": uploadURL,
	})
}

// PostUploadLinkDone handles POST /api/v2.1/upload-links/:token/upload-done/
// Notification that a file upload has been completed via an upload link.
func (h *ShareLinkViewHandler) PostUploadLinkDone(c *gin.Context) {
	token := c.Param("token")

	// Increment upload_count or, for single-use links, delete from all tables (fire-and-forget)
	go func() {
		var singleUse bool
		var orgID2, libraryID2, createdBy2 string
		var createdAt2 time.Time
		if err := h.db.Session().Query(
			`SELECT single_use, org_id, library_id, created_by, created_at FROM share_links WHERE link_token = ?`,
			token,
		).Scan(&singleUse, &orgID2, &libraryID2, &createdBy2, &createdAt2); err != nil {
			return // row already gone (e.g. concurrent consumption)
		}
		if singleUse {
			deleteConsumedShareLink(h.db, token, orgID2, libraryID2, createdBy2, createdAt2)
		} else {
			now := time.Now()
			if err := incrementShareLinkCounterDualWrite(h.db, token, "upload_count", now); err != nil {
				log.Printf("[PostUploadLinkDone] failed to update upload_count for token %s: %v", token, err)
			}
		}
	}()

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// GetShareLinkUploadURL handles GET /api/v2.1/share-links/:token/upload/
// Returns the upload URL for a share link with upload permissions.
func (h *ShareLinkViewHandler) GetShareLinkUploadURL(c *gin.Context) {
	token := c.Param("token")
	path := c.DefaultQuery("path", "/")

	sl, err := h.resolveShareLink(token, false)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "share link not found"})
		return
	}

	if sl.isExpired || sl.isDisabled {
		c.JSON(http.StatusGone, sl.unavailableJSON())
		return
	}

	if sl.passwordHash != "" && !h.verifyShareLinkPasswordCookie(c, token, sl.passwordHash) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Password required"})
		return
	}

	// Check if upload is allowed for this share link
	if !sl.canUpload {
		c.JSON(http.StatusForbidden, gin.H{"error": "upload not permitted"})
		return
	}

	// Build the full path with traversal protection
	fullPath, err := buildShareLinkFullPath(sl.filePath, path)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "invalid path"})
		return
	}

	// Generate an upload URL using the seafhttp upload mechanism
	// Create a token that the file-upload handler will accept
	uploadToken, err := h.tokenCreator.CreateUploadToken(sl.orgID, sl.libraryID, fullPath, sl.createdBy)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate upload URL"})
		return
	}

	uploadURL := getBrowserURL(c, h.serverURL) + "/seafhttp/upload-api/" + uploadToken
	c.JSON(http.StatusOK, gin.H{
		"upload_link": uploadURL,
	})
}

// PostShareLinkUploadDone handles POST /api/v2.1/share-links/:token/upload-done/
// Notification that a file upload has been completed via a share link.
func (h *ShareLinkViewHandler) PostShareLinkUploadDone(c *gin.Context) {
	token := c.Param("token")

	// Validate the share link exists and has upload permissions
	sl, err := h.resolveShareLink(token, false)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "share link not found"})
		return
	}

	if sl.isExpired || sl.isDisabled {
		c.JSON(http.StatusGone, sl.unavailableJSON())
		return
	}

	if sl.passwordHash != "" && !h.verifyShareLinkPasswordCookie(c, token, sl.passwordHash) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Password required"})
		return
	}

	if !sl.canUpload {
		c.JSON(http.StatusForbidden, gin.H{"error": "upload not permitted"})
		return
	}

	// Increment upload_count or, for single-use links, delete from all tables (fire-and-forget)
	go func() {
		if sl.singleUse {
			deleteConsumedShareLink(h.db, sl.token, sl.orgID, sl.libraryID, sl.createdBy, sl.createdAt)
		} else {
			now := time.Now()
			if err := incrementShareLinkCounterDualWrite(h.db, sl.token, "upload_count", now); err != nil {
				log.Printf("[PostShareLinkUploadDone] failed to update upload_count for token %s: %v", sl.token, err)
			}
		}
	}()

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// verifyShareLinkPasswordCookie checks if the client has a valid HMAC cookie for a password-protected share link.
// servePasswordPage renders a server-side password prompt page for password-protected share/upload links.
// This is used for embedded preview types (images, videos, PDFs) that don't go through the React bundle.
func (h *ShareLinkViewHandler) servePasswordPage(c *gin.Context, token, tokenType, filename string) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>%s - SesameFS</title>
    <link rel="icon" type="image/x-icon" href="/favicon.png">
    <style>
        body { margin: 0; font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; background: #f5f5f5; display: flex; align-items: center; justify-content: center; min-height: 100vh; }
        .card { background: #fff; border-radius: 8px; padding: 40px; width: 400px; max-width: 90%%; box-shadow: 0 4px 20px rgba(0,0,0,0.15); text-align: center; }
        h4 { margin: 0 0 8px; }
        .desc { color: #666; margin-bottom: 24px; }
        input[type=password] { width: 100%%; padding: 8px 12px; border: 1px solid #ccc; border-radius: 4px; margin-bottom: 12px; box-sizing: border-box; font-size: 14px; }
        .err { color: #dc3545; font-size: 14px; margin-bottom: 12px; display: none; }
        button { width: 100%%; padding: 10px; background: #3572b0; color: #fff; border: none; border-radius: 4px; font-size: 14px; cursor: pointer; }
        button:hover { background: #2a5d8f; }
        button:disabled { opacity: 0.6; cursor: not-allowed; }
    </style>
</head>
<body>
    <div class="card">
        <h4>Password Protected</h4>
        <p class="desc">This link is protected. Please enter the password to continue.</p>
        <form id="pwform">
            <input type="password" id="pw" placeholder="Password" autofocus />
            <p class="err" id="err"></p>
            <button type="submit" id="btn">Submit</button>
        </form>
    </div>
    <script>
    document.getElementById('pwform').addEventListener('submit', function(e) {
        e.preventDefault();
        var pw = document.getElementById('pw').value;
        var errEl = document.getElementById('err');
        var btn = document.getElementById('btn');
        if (!pw) { errEl.textContent = 'Please enter the password.'; errEl.style.display = 'block'; return; }
        btn.disabled = true; btn.textContent = 'Verifying...'; errEl.style.display = 'none';
        fetch('/%s/%s/check-password/', { method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({password: pw}) })
            .then(function(res) { if (res.ok) { window.location.reload(); } else { errEl.textContent = 'Incorrect password'; errEl.style.display = 'block'; btn.disabled = false; btn.textContent = 'Submit'; } })
            .catch(function() { errEl.textContent = 'Network error. Please try again.'; errEl.style.display = 'block'; btn.disabled = false; btn.textContent = 'Submit'; });
    });
    </script>
</body>
</html>`, html.EscapeString(filename), tokenType, html.EscapeString(token)))
}

func (h *ShareLinkViewHandler) verifyShareLinkPasswordCookie(c *gin.Context, token, passwordHash string) bool {
	if passwordHash == "" {
		return true // No password required
	}
	cookieName := "sesamefs_slpwd_" + token[:8]
	cookieValue, err := c.Cookie(cookieName)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(h.config.Auth.ShareLinkHMACKey))
	mac.Write([]byte(token))
	mac.Write([]byte(passwordHash))
	expected := hex.EncodeToString(mac.Sum(nil))
	return cookieValue == expected
}

// CheckShareLinkPassword verifies the password for a share link and sets an HMAC cookie on success.
func (h *ShareLinkViewHandler) CheckShareLinkPassword(c *gin.Context) {
	token := c.Param("token")

	var req struct {
		Password string `json:"password" form:"password"`
	}
	if err := c.ShouldBind(&req); err != nil || req.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "password required"})
		return
	}

	var passwordHash string
	err := h.db.Session().Query(
		`SELECT password_hash FROM share_links WHERE link_token = ?`, token,
	).Scan(&passwordHash)
	if err != nil || passwordHash == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "share link not found"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.Password)); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "Incorrect password"})
		return
	}

	mac := hmac.New(sha256.New, []byte(h.config.Auth.ShareLinkHMACKey))
	mac.Write([]byte(token))
	mac.Write([]byte(passwordHash))
	cookieValue := hex.EncodeToString(mac.Sum(nil))
	isSecure := c.Request.TLS != nil
	cookieName := "sesamefs_slpwd_" + token[:8]
	c.SetCookie(cookieName, cookieValue, 3600*24, "/", "", isSecure, true)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// CheckUploadLinkPassword verifies the password for an upload link and sets an HMAC cookie on success.
func (h *ShareLinkViewHandler) CheckUploadLinkPassword(c *gin.Context) {
	token := c.Param("token")

	var req struct {
		Password string `json:"password" form:"password"`
	}
	if err := c.ShouldBind(&req); err != nil || req.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "password required"})
		return
	}

	var passwordHash string
	err := h.db.Session().Query(
		`SELECT password_hash FROM share_links WHERE link_token = ?`, token,
	).Scan(&passwordHash)
	if err != nil || passwordHash == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "upload link not found"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.Password)); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "Incorrect password"})
		return
	}

	mac := hmac.New(sha256.New, []byte(h.config.Auth.ShareLinkHMACKey))
	mac.Write([]byte("upload_" + token))
	mac.Write([]byte(passwordHash))
	cookieValue := hex.EncodeToString(mac.Sum(nil))
	isSecure := c.Request.TLS != nil
	cookieName := "sesamefs_ulpwd_" + token[:8]
	c.SetCookie(cookieName, cookieValue, 3600*24, "/", "", isSecure, true)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// verifyUploadLinkPasswordCookie checks if the client has a valid HMAC cookie for a password-protected upload link.
func (h *ShareLinkViewHandler) verifyUploadLinkPasswordCookie(c *gin.Context, token, passwordHash string) bool {
	if passwordHash == "" {
		return true
	}
	cookieName := "sesamefs_ulpwd_" + token[:8]
	cookieValue, err := c.Cookie(cookieName)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(h.config.Auth.ShareLinkHMACKey))
	mac.Write([]byte("upload_" + token))
	mac.Write([]byte(passwordHash))
	expected := hex.EncodeToString(mac.Sum(nil))
	return cookieValue == expected
}
