package v2

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/crypto"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/httputil"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/Sesame-Disk/sesamefs/internal/streaming"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
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
}

type pageBootstrapResponse struct {
	RenderMode  string `json:"render_mode"`
	Bundle      string `json:"bundle,omitempty"`
	Title       string `json:"title"`
	PageOptions any    `json:"page_options,omitempty"`
}

// NewShareLinkViewHandler creates a new ShareLinkViewHandler for public share/upload link APIs.
func NewShareLinkViewHandler(database *db.DB, cfg *config.Config, s3Store *storage.S3Store, storageManager *storage.Manager, tokenCreator TokenCreator, serverURL string) *ShareLinkViewHandler {
	return &ShareLinkViewHandler{
		db:             database,
		config:         cfg,
		storage:        s3Store,
		storageManager: storageManager,
		tokenCreator:   tokenCreator,
		serverURL:      serverURL,
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

type pathSegment struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type sharedMarkdownSmartLinkTarget struct {
	Path  string `json:"path"`
	IsDir bool   `json:"isDir"`
}

var sharedMarkdownSmartLinkPattern = regexp.MustCompile(`(?:https?://[^\s)"'>]+)?/smart-link/([A-Za-z0-9_-]+)`)

func marshalPageOptionsJSON(pageOptions any) string {
	data, err := json.Marshal(pageOptions)
	if err != nil {
		return `{}`
	}
	return string(data)
}

// incrementViewCount increments the view_count for a share link asynchronously.
// Call this AFTER verifying the link is active, not expired, and password-verified.
func (h *ShareLinkViewHandler) incrementViewCount(token string) {
	go func() {
		if err := incrementShareLinkCounterDualWrite(h.db, token, "view_count", time.Now()); err != nil {
			log.Printf("[incrementViewCount] failed to update view_count for token %s: %v", token, err)
		}
	}()
}

func buildZippedPathSegments(rootName, relativePath string) []pathSegment {
	segments := []pathSegment{{Name: rootName, Path: "/"}}

	if relativePath != "/" && relativePath != "" {
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

	return segments
}

func buildRawPath(sl *shareLinkData) string {
	if sl.isDirShareLink {
		return fmt.Sprintf("/d/%s/files/?p=%s&raw=1", sl.token, url.QueryEscape(sl.fileSubPath))
	}
	return fmt.Sprintf("/d/%s?raw=1", sl.token)
}

func (h *ShareLinkViewHandler) buildSharedDirPageBootstrap(c *gin.Context, sl *shareLinkData) pageBootstrapResponse {
	dirName := filepath.Base(sl.filePath)
	if sl.filePath == "/" || sl.filePath == "" {
		dirName = sl.repoName
	}

	relativePath := c.DefaultQuery("p", "/")
	if relativePath == "" {
		relativePath = "/"
	}
	mode := c.DefaultQuery("mode", "list")
	thumbnailSize := 48
	zipped := buildZippedPathSegments(dirName, relativePath)

	dirPath := sl.filePath
	if dirPath == "" || dirPath == "/" {
		dirPath = relativePath
	} else if relativePath != "/" {
		dirPath = strings.TrimSuffix(sl.filePath, "/") + "/" + strings.TrimPrefix(relativePath, "/")
	}

	passwordVerified := h.verifyShareLinkPasswordCookie(c, sl.token, sl.passwordHash)
	noPassword := sl.passwordHash == "" || passwordVerified
	needPassword := sl.passwordHash != "" && !passwordVerified

	pageOptions := map[string]any{
		"token":                sl.token,
		"repoID":               sl.libraryID,
		"repoName":             sl.repoName,
		"path":                 sl.filePath,
		"dirName":              dirName,
		"dirPath":              dirPath,
		"relativePath":         relativePath,
		"mode":                 mode,
		"thumbnailSize":        thumbnailSize,
		"zipped":               zipped,
		"canDownload":          sl.canDownload,
		"canUpload":            sl.canUpload,
		"sharedBy":             sl.creatorName,
		"noPassword":           noPassword,
		"needPassword":         needPassword,
		"noQuota":              false,
		"trafficOverLimit":     false,
		"enableVideoThumbnail": false,
		"permissions": map[string]bool{
			"can_edit":     sl.canEdit,
			"can_download": sl.canDownload,
			"can_upload":   sl.canUpload,
		},
	}

	return pageBootstrapResponse{
		RenderMode:  "bundle",
		Bundle:      "sharedDirView",
		Title:       dirName + " - SesameFS",
		PageOptions: pageOptions,
	}
}

func (h *ShareLinkViewHandler) buildSharedFileBundleBootstrap(c *gin.Context, sl *shareLinkData, bundleName, rawPath, filename, ext string, fileSize int64, fileContent string, smartLinkMap map[string]sharedMarkdownSmartLinkTarget) pageBootstrapResponse {
	passwordVerified := h.verifyShareLinkPasswordCookie(c, sl.token, sl.passwordHash)
	noPassword := sl.passwordHash == "" || passwordVerified
	needPassword := sl.passwordHash != "" && !passwordVerified
	rawContentType := resolveInlineContentType(ext)

	pageOptions := map[string]any{
		"sharedToken":                sl.token,
		"repoID":                     sl.libraryID,
		"commitID":                   sl.commitID,
		"filePath":                   sl.filePath,
		"fileName":                   filename,
		"fileSize":                   fileSize,
		"rawPath":                    rawPath,
		"rawContentType":             rawContentType,
		"downloadPath":               buildShareDownloadPath(sl),
		"canDownload":                sl.canDownload,
		"canEdit":                    sl.canEdit,
		"sharedBy":                   sl.creatorName,
		"noPassword":                 noPassword,
		"needPassword":               needPassword,
		"trafficOverLimit":           false,
		"fileExt":                    ext,
		"siteName":                   "SesameFS",
		"enableWatermark":            false,
		"zipped":                     nil,
		"enableShareLinkReportAbuse": false,
		"fileContent":                fileContent,
		"smartLinkMap":               smartLinkMap,
		"err":                        "",
	}

	return pageBootstrapResponse{
		RenderMode:  "bundle",
		Bundle:      bundleName,
		Title:       filename + " - SesameFS",
		PageOptions: pageOptions,
	}
}

func buildShareDownloadPath(sl *shareLinkData) string {
	if sl.isDirShareLink {
		return fmt.Sprintf("/d/%s/files/?p=%s&dl=1", sl.token, url.QueryEscape(sl.fileSubPath))
	}
	return fmt.Sprintf("/d/%s?dl=1", sl.token)
}

func extractSharedMarkdownSmartLinkTokens(content string) []string {
	if content == "" {
		return nil
	}

	matches := sharedMarkdownSmartLinkPattern.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(matches))
	tokens := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 || match[1] == "" {
			continue
		}
		if _, ok := seen[match[1]]; ok {
			continue
		}
		seen[match[1]] = struct{}{}
		tokens = append(tokens, match[1])
	}

	return tokens
}

func (h *ShareLinkViewHandler) buildSharedMarkdownSmartLinkMap(sl *shareLinkData, content string) map[string]sharedMarkdownSmartLinkTarget {
	tokens := extractSharedMarkdownSmartLinkTokens(content)
	if len(tokens) == 0 {
		return nil
	}

	result := make(map[string]sharedMarkdownSmartLinkTarget, len(tokens))
	for _, token := range tokens {
		var linkType, orgID, libraryID, filePath string
		var active bool
		err := h.db.Session().Query(`
			SELECT link_type, org_id, library_id, file_path, active
			FROM share_links WHERE link_token = ?
		`, token).Scan(&linkType, &orgID, &libraryID, &filePath, &active)
		if err != nil || linkType != "internal" || !active {
			continue
		}
		if orgID != sl.orgID || libraryID != sl.libraryID {
			continue
		}

		isDir, dirErr := resolveLibraryPathIsDir(h.db, libraryID, filePath)
		if dirErr != nil {
			isDir = filePath == "/" || strings.HasSuffix(filePath, "/")
		}

		result[token] = sharedMarkdownSmartLinkTarget{
			Path:  filePath,
			IsDir: isDir,
		}
	}

	if len(result) == 0 {
		return nil
	}

	return result
}

func (h *ShareLinkViewHandler) buildOnlyOfficeShareBootstrap(sl *shareLinkData, filename, ext string, fileSize int64) (pageBootstrapResponse, error) {
	downloadToken, err := h.tokenCreator.CreateLinkDownloadToken(sl.orgID, sl.libraryID, sl.filePath, sl.createdBy)
	if err != nil {
		return pageBootstrapResponse{}, fmt.Errorf("failed to create download token: %w", err)
	}

	ooServerURL := h.config.OnlyOffice.ServerURL
	if ooServerURL == "" {
		ooServerURL = h.serverURL
	}
	downloadURL := ooServerURL + "/seafhttp/files/" + downloadToken + "/" + filename

	fileID := ""
	if sl.targetEntry != nil {
		fileID = sl.targetEntry.ID
	}
	docKey := generateDocKey(sl.libraryID, sl.filePath, fileID)

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

	if strings.TrimSpace(h.config.OnlyOffice.JWTSecret) == "" {
		return pageBootstrapResponse{}, fmt.Errorf("OnlyOffice JWT secret is not configured")
	}
	ooHandler := &OnlyOfficeHandler{db: h.db, config: h.config}
	token, signErr := ooHandler.signJWT(docConfig)
	if signErr != nil {
		return pageBootstrapResponse{}, fmt.Errorf("failed to sign OnlyOffice JWT: %w", signErr)
	}
	docConfig.Token = token

	return pageBootstrapResponse{
		RenderMode: "onlyoffice",
		Title:      filename + " - SesameFS",
		PageOptions: map[string]any{
			"fileName":         filename,
			"fileSize":         fileSize,
			"sharedBy":         sl.creatorName,
			"canDownload":      sl.canDownload,
			"downloadPath":     buildShareDownloadPath(sl),
			"apiJSURL":         h.config.OnlyOffice.APIJSURL,
			"onlyOfficeConfig": docConfig,
		},
	}, nil
}

func (h *ShareLinkViewHandler) buildUploadLinkPageBootstrap(token, libraryID, filePath, dirName, creatorName string, needPassword bool) pageBootstrapResponse {
	pageOptions := map[string]any{
		"token":             token,
		"repoID":            libraryID,
		"path":              filePath,
		"dirName":           dirName,
		"sharedBy":          map[string]string{"name": creatorName, "avatar": ""},
		"noQuota":           false,
		"maxUploadFileSize": nil,
		"needPassword":      needPassword,
	}

	return pageBootstrapResponse{
		RenderMode:  "bundle",
		Bundle:      "uploadLink",
		Title:       dirName + " - Upload - SesameFS",
		PageOptions: pageOptions,
	}
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

// respondShareLinkUnavailable emits a uniform 404 for every "not available"
// reason: nonexistent token, expired, disabled. The previous code returned
// 404 on miss and 410 with distinct bodies on expired/disabled, which acted
// as an enumeration oracle — an unauthenticated attacker could probe random
// tokens and distinguish "never existed" from "real but stale". Collapsing
// all of those into the same response closes the oracle (H-5). Viewer HTML
// pages still show a generic "link unavailable" state; the precise reason is
// intentionally not disclosed to unauthenticated clients.
func respondShareLinkUnavailable(c *gin.Context) {
	c.JSON(http.StatusNotFound, gin.H{"error": "share link unavailable"})
}

// respondUploadLinkUnavailable emits the same opaque 404 for public upload-link
// endpoints. These routes are also keyed only by an opaque token, so leaking
// whether the token exists, expired, or was disabled creates the same class of
// enumeration signal as public share links.
func respondUploadLinkUnavailable(c *gin.Context) {
	c.JSON(http.StatusNotFound, gin.H{"error": "upload link unavailable"})
}

type uploadLinkData struct {
	orgID        string
	libraryID    string
	filePath     string
	createdBy    string
	passwordHash string
	expiresAt    *time.Time
	active       bool
	singleUse    bool
	createdAt    time.Time
}

func (h *ShareLinkViewHandler) resolveUploadLink(token string) (*uploadLinkData, error) {
	data := &uploadLinkData{}
	err := h.db.Session().Query(`
		SELECT org_id, library_id, file_path, created_by, password_hash, expires_at, active, single_use, created_at
		FROM share_links WHERE link_token = ?
	`, token).Scan(
		&data.orgID, &data.libraryID, &data.filePath, &data.createdBy,
		&data.passwordHash, &data.expiresAt, &data.active, &data.singleUse, &data.createdAt,
	)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func (ul *uploadLinkData) isUnavailable() bool {
	if !ul.active {
		return true
	}
	return ul.expiresAt != nil && time.Now().After(*ul.expiresAt)
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
	if c.Query("dl") != "1" && c.Query("raw") != "1" {
		c.JSON(http.StatusNotFound, gin.H{"error": "public share pages are served by the frontend shell"})
		return
	}

	token := c.Param("token")
	sl, err := h.resolveShareLink(token, false)
	if err != nil {
		respondShareLinkUnavailable(c)
		return
	}
	if sl.isExpired || sl.isDisabled {
		respondShareLinkUnavailable(c)
		return
	}

	fsHelper := NewFSHelper(h.db)
	rootFSID, _, err := fsHelper.GetRootFSID(sl.libraryID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to access shared library"})
		return
	}

	sharePath := sl.filePath
	if sharePath == "" {
		sharePath = "/"
	}
	if sharePath == "/" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "raw/download is only available for shared files"})
		return
	}

	result, err := fsHelper.TraverseToPathFromRoot(sl.libraryID, rootFSID, sharePath)
	if err != nil || result.TargetEntry == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "shared file not found"})
		return
	}

	sl.targetEntry = result.TargetEntry
	sl.isDir = result.TargetEntry.Mode == ModeDir || result.TargetEntry.Mode&0170000 == 040000
	if sl.isDir {
		c.JSON(http.StatusBadRequest, gin.H{"error": "raw/download is only available for shared files"})
		return
	}
	if sl.passwordHash != "" && !h.verifyShareLinkPasswordCookie(c, token, sl.passwordHash) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Password required"})
		return
	}

	if c.Query("dl") == "1" {
		h.handleShareLinkDownload(c, sl, fsHelper, rootFSID)
		return
	}
	h.handleShareLinkRaw(c, sl)
}

// GetShareLinkBootstrap returns the frontend bootstrap payload for GET /d/:token.
func (h *ShareLinkViewHandler) GetShareLinkBootstrap(c *gin.Context) {
	token := c.Param("token")

	sl, err := h.resolveShareLink(token, false)
	if err != nil {
		respondShareLinkUnavailable(c)
		return
	}

	if sl.isExpired || sl.isDisabled {
		respondShareLinkUnavailable(c)
		return
	}

	fsHelper := NewFSHelper(h.db)
	rootFSID, _, err := fsHelper.GetRootFSID(sl.libraryID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to access shared library"})
		return
	}

	sharePath := sl.filePath
	if sharePath == "" {
		sharePath = "/"
	}

	if sharePath == "/" {
		sl.isDir = true
		bootstrap := h.buildSharedDirPageBootstrap(c, sl)
		if pageOptions, ok := bootstrap.PageOptions.(map[string]any); ok {
			if noPassword, _ := pageOptions["noPassword"].(bool); noPassword {
				h.incrementViewCount(sl.token)
			}
		}
		c.JSON(http.StatusOK, bootstrap)
		return
	}

	result, err := fsHelper.TraverseToPathFromRoot(sl.libraryID, rootFSID, sharePath)
	if err != nil || result.TargetEntry == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "shared file or folder not found"})
		return
	}

	sl.targetEntry = result.TargetEntry
	sl.isDir = result.TargetEntry.Mode == ModeDir || result.TargetEntry.Mode&0170000 == 040000
	if sl.isDir {
		bootstrap := h.buildSharedDirPageBootstrap(c, sl)
		if pageOptions, ok := bootstrap.PageOptions.(map[string]any); ok {
			if noPassword, _ := pageOptions["noPassword"].(bool); noPassword {
				h.incrementViewCount(sl.token)
			}
		}
		c.JSON(http.StatusOK, bootstrap)
		return
	}

	bootstrap, status, err := h.buildShareFileBootstrapResponse(c, sl)
	if err != nil {
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	if pageOptions, ok := bootstrap.PageOptions.(map[string]any); ok {
		if noPassword, _ := pageOptions["noPassword"].(bool); noPassword {
			h.incrementViewCount(sl.token)
		}
	}

	c.JSON(http.StatusOK, bootstrap)
}

// GetShareLinkFileBootstrap returns the frontend bootstrap payload for GET /d/:token/files/?p=...
func (h *ShareLinkViewHandler) GetShareLinkFileBootstrap(c *gin.Context) {
	token := c.Param("token")
	filePath := c.Query("p")
	if filePath == "" {
		filePath = "/"
	}

	sl, err := h.resolveShareLink(token, false)
	if err != nil {
		respondShareLinkUnavailable(c)
		return
	}

	if sl.isExpired || sl.isDisabled {
		respondShareLinkUnavailable(c)
		return
	}

	fullPath, err := buildShareLinkFullPath(sl.filePath, filePath)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "invalid path"})
		return
	}

	sl.filePath = fullPath
	sl.isDirShareLink = true
	sl.fileSubPath = filePath

	fsHelper := NewFSHelper(h.db)
	rootFSID, _, err := fsHelper.GetRootFSID(sl.libraryID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to access shared library"})
		return
	}

	result, err := fsHelper.TraverseToPathFromRoot(sl.libraryID, rootFSID, fullPath)
	if err != nil || result.TargetEntry == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "shared file not found"})
		return
	}

	sl.targetEntry = result.TargetEntry
	sl.isDir = false

	bootstrap, status, err := h.buildShareFileBootstrapResponse(c, sl)
	if err != nil {
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	if pageOptions, ok := bootstrap.PageOptions.(map[string]any); ok {
		if noPassword, _ := pageOptions["noPassword"].(bool); noPassword {
			h.incrementViewCount(sl.token)
		}
	}

	c.JSON(http.StatusOK, bootstrap)
}

// handleShareLinkDownload handles ?dl=1 for file share links
func (h *ShareLinkViewHandler) handleShareLinkDownload(c *gin.Context, sl *shareLinkData, fsHelper *FSHelper, rootFSID string) {
	// Check download permission
	if !sl.canDownload {
		redirectToFrontendErrorPage(c, http.StatusForbidden, "Download Disabled", "Downloading is not allowed for this share link.")
		return
	}

	filename := filepath.Base(sl.filePath)

	// Generate download token using the share link creator's user ID
	downloadToken, err := h.tokenCreator.CreateLinkDownloadToken(sl.orgID, sl.libraryID, sl.filePath, sl.createdBy)
	if err != nil {
		redirectToFrontendErrorPage(c, http.StatusInternalServerError, "Download Error", "Failed to generate download link.")
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

	downloadURL := httputil.GetBrowserURL(c, h.serverURL) + "/seafhttp/files/" + downloadToken + "/" + filename
	c.Redirect(http.StatusFound, downloadURL)
}

// handleShareLinkRaw serves the raw file content for inline preview (images, PDFs, videos, etc.)
func (h *ShareLinkViewHandler) handleShareLinkRaw(c *gin.Context, sl *shareLinkData) {
	// Record traffic after the response is fully written (covers all return paths).
	// shareLinkDownloadPeriod is set by the quota pre-check below and captured by the defer closure.
	shareLinkDownloadStatus := traffic.QuotaStatus{Allowed: true}
	bytesBefore := int64(c.Writer.Size())
	defer func() {
		if rec := traffic.Get(); rec != nil {
			sent := int64(c.Writer.Size()) - bytesBefore
			if sent > 0 {
				traffic.RecordCheckedTransfer(rec, shareLinkDownloadStatus, sl.orgID, sl.createdBy, traffic.LinkDownload, sent)
			}
		}
	}()

	// Quota pre-check: reject if the org's download traffic quota is exhausted.
	if checker := traffic.GetChecker(); checker != nil {
		shareLinkDownloadStatus, _ = traffic.CheckTrafficQuotaWithChecker(checker, sl.orgID, sl.createdBy, "download", 0)
		if !shareLinkDownloadStatus.Allowed {
			c.JSON(http.StatusForbidden, traffic.TrafficQuotaExceededResponse(shareLinkDownloadStatus, "traffic quota exceeded", false))
			return
		} else {
			if warning, ok := traffic.TrafficQuotaWarningHeader(shareLinkDownloadStatus); ok {
				c.Header("X-Quota-Warning", warning)
			}
		}
	}

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

	blockStore, _, err := resolveLibraryBlockStoreForRequest(c, h.db, h.config, h.storageManager, h.storage, sl.orgID, sl.libraryID)
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
	mimeType := resolveInlineContentType(ext)

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
		c.Header("Content-Disposition", resolveContentDisposition(ext, filename))
		c.Header("Content-Type", mimeType)
		http.ServeContent(c.Writer, c.Request, filename, time.Time{}, rs)
		return
	}

	// Non-video/audio: stream block-by-block, O(block_size) RAM
	c.Header("Content-Disposition", resolveContentDisposition(ext, filename))
	c.Header("Content-Type", mimeType)
	if fileSize > 0 && !encrypted {
		c.Header("Content-Length", strconv.FormatInt(fileSize, 10))
	}
	c.Status(http.StatusOK)

	streaming.StreamBlocks(c, ctx, blockStore, resolvedIDs, fileKeyParam, "ShareLinkRaw")
}

// serveSharedDirPage renders the shared directory view
func (h *ShareLinkViewHandler) serveSharedDirPage(c *gin.Context, sl *shareLinkData) {
	c.JSON(http.StatusNotFound, gin.H{"error": "public share pages are served by the frontend shell"})
}

// buildZippedPath builds the breadcrumb JSON array for shared dir navigation
// Returns JSON like [{"name":"Root","path":"/"},{"name":"subfolder","path":"/subfolder/"}]
func buildZippedPath(rootName, relativePath string) string {
	data, err := json.Marshal(buildZippedPathSegments(rootName, relativePath))
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

	blockStore, _, err := resolveLibraryBlockStoreForRequest(nil, h.db, h.config, h.storageManager, h.storage, sl.orgID, sl.libraryID)
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
	c.JSON(http.StatusNotFound, gin.H{"error": "public share pages are served by the frontend shell"})
}

func (h *ShareLinkViewHandler) buildShareFileBootstrapResponse(c *gin.Context, sl *shareLinkData) (pageBootstrapResponse, int, error) {
	filename := filepath.Base(sl.filePath)
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), "."))

	rawPath := buildRawPath(sl)
	var fileSize int64
	if sl.targetEntry != nil {
		fileSize = sl.targetEntry.Size
	}

	if ext != "pdf" && h.config.OnlyOffice.Enabled && isOnlyOfficeViewable(ext) {
		bootstrap, err := h.buildOnlyOfficeShareBootstrap(sl, filename, ext, fileSize)
		if err == nil {
			return bootstrap, http.StatusOK, nil
		}
		slog.Warn("OnlyOffice preview bootstrap failed, falling back to bundle", "file", filename, "error", err)
	}

	bundleName := extensionToBundleName(ext)

	fileContent := ""
	var smartLinkMap map[string]sharedMarkdownSmartLinkTarget
	if bundleName == "sharedFileViewText" || bundleName == "sharedFileViewMarkdown" {
		fileContent = h.readFileContentAsText(sl)
		if bundleName == "sharedFileViewMarkdown" {
			smartLinkMap = h.buildSharedMarkdownSmartLinkMap(sl, fileContent)
		}
	}

	return h.buildSharedFileBundleBootstrap(c, sl, bundleName, rawPath, filename, ext, fileSize, fileContent, smartLinkMap), http.StatusOK, nil
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
		respondShareLinkUnavailable(c)
		return
	}

	if sl.isExpired || sl.isDisabled {
		respondShareLinkUnavailable(c)
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
		respondShareLinkUnavailable(c)
		return
	}

	if sl.isExpired || sl.isDisabled {
		respondShareLinkUnavailable(c)
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
	if c.Query("dl") != "1" && c.Query("raw") != "1" {
		c.JSON(http.StatusNotFound, gin.H{"error": "public share pages are served by the frontend shell"})
		return
	}

	token := c.Param("token")
	filePath := c.Query("p")
	if filePath == "" {
		filePath = "/"
	}

	sl, err := h.resolveShareLink(token, false)
	if err != nil {
		respondShareLinkUnavailable(c)
		return
	}
	if sl.isExpired || sl.isDisabled {
		respondShareLinkUnavailable(c)
		return
	}

	fullPath, err := buildShareLinkFullPath(sl.filePath, filePath)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "invalid path"})
		return
	}

	sl.filePath = fullPath
	sl.isDirShareLink = true
	sl.fileSubPath = filePath

	fsHelper := NewFSHelper(h.db)
	rootFSID, _, err := fsHelper.GetRootFSID(sl.libraryID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to access shared library"})
		return
	}

	result, err := fsHelper.TraverseToPathFromRoot(sl.libraryID, rootFSID, fullPath)
	if err != nil || result.TargetEntry == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "shared file not found"})
		return
	}

	sl.targetEntry = result.TargetEntry
	sl.isDir = false
	if sl.passwordHash != "" && !h.verifyShareLinkPasswordCookie(c, token, sl.passwordHash) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Password required"})
		return
	}

	if c.Query("dl") == "1" {
		h.handleShareLinkDownload(c, sl, fsHelper, rootFSID)
		return
	}
	h.handleShareLinkRaw(c, sl)
}

// GetShareLinkZipTask handles GET /api/v2.1/share-link-zip-task/
// Creates a zip download task for a shared directory and returns a zip token.
func (h *ShareLinkViewHandler) GetShareLinkZipTask(c *gin.Context) {
	token := c.Query("share_link_token")
	path := c.DefaultQuery("path", "/")

	sl, err := h.resolveShareLink(token, false)
	if err != nil {
		respondShareLinkUnavailable(c)
		return
	}

	if sl.isExpired || sl.isDisabled {
		respondShareLinkUnavailable(c)
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
	zipToken, err := h.tokenCreator.CreateLinkDownloadToken(sl.orgID, sl.libraryID, fullPath, sl.createdBy)
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
	c.JSON(http.StatusNotFound, gin.H{"error": "public upload pages are served by the frontend shell"})
}

// GetUploadLinkBootstrap returns the frontend bootstrap payload for GET /u/d/:token.
func (h *ShareLinkViewHandler) GetUploadLinkBootstrap(c *gin.Context) {
	token := c.Param("token")

	ul, err := h.resolveUploadLink(token)
	if err != nil {
		respondUploadLinkUnavailable(c)
		return
	}

	if ul.isUnavailable() {
		respondUploadLinkUnavailable(c)
		return
	}

	needPassword := ul.passwordHash != "" && !h.verifyUploadLinkPasswordCookie(c, token, ul.passwordHash)

	var repoName string
	h.db.Session().Query(`SELECT name FROM libraries_by_id WHERE library_id = ?`, ul.libraryID).Scan(&repoName)
	if repoName == "" {
		repoName = "Shared folder"
	}

	dirName := filepath.Base(ul.filePath)
	if ul.filePath == "/" || ul.filePath == "" {
		dirName = repoName
	}

	var creatorName, creatorEmail string
	h.db.Session().Query(`SELECT name, email FROM users WHERE org_id = ? AND user_id = ?`, ul.orgID, ul.createdBy).Scan(&creatorName, &creatorEmail)
	if creatorName == "" {
		creatorName = creatorEmail
	}
	if creatorName == "" {
		creatorName = "Unknown"
	}

	c.JSON(http.StatusOK, h.buildUploadLinkPageBootstrap(token, ul.libraryID, ul.filePath, dirName, creatorName, needPassword))
}

// GetUploadLinkUploadURL handles GET /api/v2.1/upload-links/:token/upload/
// Returns the upload URL for an upload link.
func (h *ShareLinkViewHandler) GetUploadLinkUploadURL(c *gin.Context) {
	token := c.Param("token")

	ul, err := h.resolveUploadLink(token)
	if err != nil {
		respondUploadLinkUnavailable(c)
		return
	}

	if ul.isUnavailable() {
		respondUploadLinkUnavailable(c)
		return
	}
	if ul.passwordHash != "" && !h.verifyUploadLinkPasswordCookie(c, token, ul.passwordHash) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Password required"})
		return
	}

	// Generate an upload URL using the seafhttp upload mechanism
	// Create a token that the file-upload handler will accept
	uploadToken, err := h.tokenCreator.CreateLinkUploadToken(ul.orgID, ul.libraryID, ul.filePath, ul.createdBy)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate upload URL"})
		return
	}

	uploadURL := httputil.GetBrowserURL(c, h.serverURL) + "/seafhttp/upload-api/" + uploadToken
	c.JSON(http.StatusOK, gin.H{
		"upload_link": uploadURL,
	})
}

// PostUploadLinkDone handles POST /api/v2.1/upload-links/:token/upload-done/
// Notification that a file upload has been completed via an upload link.
func (h *ShareLinkViewHandler) PostUploadLinkDone(c *gin.Context) {
	token := c.Param("token")

	ul, err := h.resolveUploadLink(token)
	if err != nil {
		respondUploadLinkUnavailable(c)
		return
	}

	if ul.isUnavailable() {
		respondUploadLinkUnavailable(c)
		return
	}
	if ul.passwordHash != "" && !h.verifyUploadLinkPasswordCookie(c, token, ul.passwordHash) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Password required"})
		return
	}

	// Increment upload_count or, for single-use links, delete from all tables (fire-and-forget)
	go func() {
		if ul.singleUse {
			deleteConsumedShareLink(h.db, token, ul.orgID, ul.libraryID, ul.createdBy, ul.createdAt)
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
		respondShareLinkUnavailable(c)
		return
	}

	if sl.isExpired || sl.isDisabled {
		respondShareLinkUnavailable(c)
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
	uploadToken, err := h.tokenCreator.CreateLinkUploadToken(sl.orgID, sl.libraryID, fullPath, sl.createdBy)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate upload URL"})
		return
	}

	uploadURL := httputil.GetBrowserURL(c, h.serverURL) + "/seafhttp/upload-api/" + uploadToken
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
		respondShareLinkUnavailable(c)
		return
	}

	if sl.isExpired || sl.isDisabled {
		respondShareLinkUnavailable(c)
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

func (h *ShareLinkViewHandler) verifyShareLinkPasswordCookie(c *gin.Context, token, passwordHash string) bool {
	if passwordHash == "" {
		return true // No password required
	}
	cookieName, expected := buildPublicLinkPasswordCookie("share", token, passwordHash, h.config.Auth.ShareLinkHMACKey)
	cookieValue, err := c.Cookie(cookieName)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookieValue), []byte(expected)) == 1
}

func buildPublicLinkPasswordCookie(linkType, token, passwordHash, hmacKey string) (string, string) {
	mac := hmac.New(sha256.New, []byte(hmacKey))
	switch linkType {
	case "upload":
		mac.Write([]byte("upload_" + token))
		mac.Write([]byte(passwordHash))
		return "sesamefs_ulpwd_" + token[:8], hex.EncodeToString(mac.Sum(nil))
	default:
		mac.Write([]byte(token))
		mac.Write([]byte(passwordHash))
		return "sesamefs_slpwd_" + token[:8], hex.EncodeToString(mac.Sum(nil))
	}
}

// CheckPublicLinkPassword verifies the password for a public link and sets the correct HMAC cookie.
func (h *ShareLinkViewHandler) CheckPublicLinkPassword(c *gin.Context) {
	token := c.Param("token")

	var req struct {
		Password string `json:"password" form:"password"`
	}
	if err := c.ShouldBind(&req); err != nil || req.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "password required"})
		return
	}

	var linkType, passwordHash string
	var expiresAt *time.Time
	var active bool
	var downloadCount int
	var maxDownloads *int
	err := h.db.Session().Query(
		`SELECT link_type, password_hash, expires_at, active, download_count, max_downloads FROM share_links WHERE link_token = ?`, token,
	).Scan(&linkType, &passwordHash, &expiresAt, &active, &downloadCount, &maxDownloads)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "public link unavailable"})
		return
	}
	if !active {
		c.JSON(http.StatusNotFound, gin.H{"error": "public link unavailable"})
		return
	}
	if expiresAt != nil && time.Now().After(*expiresAt) {
		c.JSON(http.StatusNotFound, gin.H{"error": "public link unavailable"})
		return
	}
	if linkType == "share" && maxDownloads != nil && downloadCount >= *maxDownloads {
		c.JSON(http.StatusNotFound, gin.H{"error": "public link unavailable"})
		return
	}
	if passwordHash == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "public link unavailable"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.Password)); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "Incorrect password"})
		return
	}

	cookieName, cookieValue := buildPublicLinkPasswordCookie(linkType, token, passwordHash, h.config.Auth.ShareLinkHMACKey)
	isSecure := c.Request.TLS != nil
	c.SetCookie(cookieName, cookieValue, 3600*24, "/", "", isSecure, true)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// verifyUploadLinkPasswordCookie checks if the client has a valid HMAC cookie for a password-protected upload link.
func (h *ShareLinkViewHandler) verifyUploadLinkPasswordCookie(c *gin.Context, token, passwordHash string) bool {
	if passwordHash == "" {
		return true
	}
	cookieName, expected := buildPublicLinkPasswordCookie("upload", token, passwordHash, h.config.Auth.ShareLinkHMACKey)
	cookieValue, err := c.Cookie(cookieName)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookieValue), []byte(expected)) == 1
}
