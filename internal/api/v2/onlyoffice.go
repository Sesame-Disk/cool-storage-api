package v2

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/crypto"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/httputil"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// OnlyOfficeHandler handles OnlyOffice integration
type OnlyOfficeHandler struct {
	db             *db.DB
	config         *config.Config
	storage        *storage.S3Store
	blockStore     *storage.BlockStore
	storageManager *storage.Manager
	tokenCreator   TokenCreator
	serverURL      string
	permMiddleware *middleware.PermissionMiddleware
}

// RegisterOnlyOfficeRoutes registers OnlyOffice routes
func RegisterOnlyOfficeRoutes(rg *gin.RouterGroup, database *db.DB, cfg *config.Config, s3Store *storage.S3Store, blockStore *storage.BlockStore, storageManager *storage.Manager, tokenCreator TokenCreator, serverURL string) {
	permMiddleware := middleware.NewPermissionMiddleware(database)
	h := &OnlyOfficeHandler{
		db:             database,
		config:         cfg,
		storage:        s3Store,
		blockStore:     blockStore,
		storageManager: storageManager,
		tokenCreator:   tokenCreator,
		serverURL:      serverURL,
		permMiddleware: permMiddleware,
	}

	// v2.1 API endpoint for getting OnlyOffice editor config
	repos := rg.Group("/repos/:repo_id")
	{
		repos.GET("/onlyoffice", h.GetEditorConfig)
		repos.GET("/onlyoffice/", h.GetEditorConfig)
	}
}

// RegisterOnlyOfficeCallbackRoutes registers the callback route (under /onlyoffice/)
func RegisterOnlyOfficeCallbackRoutes(rg *gin.RouterGroup, database *db.DB, cfg *config.Config, s3Store *storage.S3Store, blockStore *storage.BlockStore, storageManager *storage.Manager, serverURL string) {
	permMiddleware := middleware.NewPermissionMiddleware(database)
	h := &OnlyOfficeHandler{
		db:             database,
		config:         cfg,
		storage:        s3Store,
		blockStore:     blockStore,
		storageManager: storageManager,
		serverURL:      serverURL,
		permMiddleware: permMiddleware,
	}

	rg.POST("/editor-callback", h.EditorCallback)
	rg.POST("/editor-callback/", h.EditorCallback)
}

// OnlyOfficeDocument represents the document configuration
type OnlyOfficeDocument struct {
	FileType    string                 `json:"fileType"`
	Key         string                 `json:"key"`
	Title       string                 `json:"title"`
	URL         string                 `json:"url"`
	Permissions *OnlyOfficePermissions `json:"permissions,omitempty"`
}

// OnlyOfficeUser represents user info for OnlyOffice
type OnlyOfficeUser struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// OnlyOfficePermissions represents editing permissions
type OnlyOfficePermissions struct {
	Edit      bool `json:"edit"`
	Download  bool `json:"download"`
	Print     bool `json:"print"`
	Copy      bool `json:"copy"`
	Review    bool `json:"review"`
	Comment   bool `json:"comment"`
	FillForms bool `json:"fillForms"`
}

// OnlyOfficeCustomization represents editor customization options (minimal, like Seahub)
type OnlyOfficeCustomization struct {
	Forcesave      bool `json:"forcesave"`
	SubmitForm     bool `json:"submitForm,omitempty"`
	CompactToolbar bool `json:"compactToolbar"`
	CompactHeader  bool `json:"compactHeader"`
}

// OnlyOfficeEditorConfig represents the editor configuration
type OnlyOfficeEditorConfig struct {
	CallbackURL   string                   `json:"callbackUrl"`
	Mode          string                   `json:"mode"` // "edit" or "view"
	User          OnlyOfficeUser           `json:"user"`
	Customization *OnlyOfficeCustomization `json:"customization,omitempty"`
}

// OnlyOfficeConfig represents the full configuration returned to the frontend
type OnlyOfficeConfig struct {
	Document     OnlyOfficeDocument     `json:"document"`
	DocumentType string                 `json:"documentType"`
	EditorConfig OnlyOfficeEditorConfig `json:"editorConfig"`
	Token        string                 `json:"token,omitempty"`
}

func (h *OnlyOfficeHandler) lookupLibraryStorageClass(orgID, repoID string) string {
	if h == nil || h.db == nil || orgID == "" || repoID == "" {
		return ""
	}

	var storageClass string
	if err := h.db.Session().Query(`
		SELECT storage_class FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&storageClass); err != nil {
		return ""
	}

	return storageClass
}

func (h *OnlyOfficeHandler) resolveLibraryBlockStore(orgID, repoID string) (*storage.BlockStore, string, error) {
	libraryClass := h.lookupLibraryStorageClass(orgID, repoID)
	if h.storageManager != nil {
		preferredClass := h.storageManager.ResolveStorageClass("", libraryClass, "hot")
		return h.storageManager.GetHealthyBlockStore(preferredClass)
	}
	if libraryClass == "" && h.config != nil {
		libraryClass = h.config.Storage.DefaultClass
	}
	if h.blockStore != nil {
		return h.blockStore, libraryClass, nil
	}
	if h.storage != nil {
		return storage.NewBlockStore(h.storage, "blocks/"), libraryClass, nil
	}
	return nil, libraryClass, fmt.Errorf("block storage not available")
}

// OnlyOfficeResponse represents the API response
type OnlyOfficeResponse struct {
	Doc      OnlyOfficeConfig `json:"doc"`
	APIJSURL string           `json:"api_js_url"`
}

func resolveOnlyOfficeServerURL(c *gin.Context, onlyOfficeServerURL, serverURL string) string {
	if trimmed := strings.TrimSuffix(strings.TrimSpace(onlyOfficeServerURL), "/"); trimmed != "" {
		return trimmed
	}

	return httputil.GetBrowserURL(c, serverURL)
}

// generateDocKey generates a unique document key for OnlyOffice
// Format: MD5(repo_id + file_path + file_id) truncated to 20 chars
// The fileID changes whenever the file content changes (new commit), so it
// naturally invalidates the key without needing a timestamp. Using a timestamp
// caused the key to rotate every minute, which made OnlyOffice lose its session
// and grey out the toolbar.
func generateDocKey(repoID, filePath, fileID string) string {
	data := fmt.Sprintf("%s%s%s", repoID, filePath, fileID)
	hash := md5.Sum([]byte(data))
	return hex.EncodeToString(hash[:])[:20]
}

// getDocumentType returns the OnlyOffice document type based on file extension
func getDocumentType(filename string) string {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), "."))
	switch ext {
	case "doc", "docx", "odt", "fodt", "rtf", "txt", "html", "htm", "epub", "xps", "djvu":
		return "word"
	case "xls", "xlsx", "ods", "fods", "csv":
		return "cell"
	case "ppt", "pptx", "odp", "fodp":
		return "slide"
	case "pdf":
		return "pdf"
	default:
		return "word"
	}
}

// canEditFile checks if the file extension can be edited (not just viewed)
func (h *OnlyOfficeHandler) canEditFile(filename string) bool {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), "."))
	for _, editExt := range h.config.OnlyOffice.EditExtensions {
		if ext == editExt {
			return true
		}
	}
	return false
}

// canViewFile checks if the file extension is supported by OnlyOffice
func (h *OnlyOfficeHandler) canViewFile(filename string) bool {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), "."))
	for _, viewExt := range h.config.OnlyOffice.ViewExtensions {
		if ext == viewExt {
			return true
		}
	}
	return false
}

// signJWT creates a JWT token for OnlyOffice authentication
func (h *OnlyOfficeHandler) signJWT(payload interface{}) (string, error) {
	secret := strings.TrimSpace(h.config.OnlyOffice.JWTSecret)
	if secret == "" {
		return "", fmt.Errorf("OnlyOffice JWT secret is not configured")
	}

	// Convert payload to map for JWT claims
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	var claims jwt.MapClaims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return "", err
	}

	// Token lifetime from config (default 1h, max 8h).
	ttl := time.Duration(h.config.OnlyOffice.JWTTTLSeconds) * time.Second
	claims["exp"] = time.Now().Add(ttl).Unix()

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// GetEditorConfig returns the OnlyOffice editor configuration
// Implements: GET /api/v2.1/repos/:repo_id/onlyoffice/?p=/path
func (h *OnlyOfficeHandler) GetEditorConfig(c *gin.Context) {
	// Check if OnlyOffice is enabled
	if !h.config.OnlyOffice.Enabled {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error_msg": "OnlyOffice is not enabled"})
		return
	}
	if strings.TrimSpace(h.config.OnlyOffice.JWTSecret) == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error_msg": "OnlyOffice JWT secret is not configured"})
		return
	}

	repoID := c.Param("repo_id")
	filePath := c.Query("p")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	if filePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error_msg": "File path is required"})
		return
	}

	// Normalize path
	filePath = normalizePath(filePath)
	filename := filepath.Base(filePath)

	// Check if file type is supported
	if !h.canViewFile(filename) {
		c.JSON(http.StatusBadRequest, gin.H{"error_msg": "File type not supported by OnlyOffice"})
		return
	}

	// Get file ID from fs_objects
	fileID, err := h.getFileID(repoID, orgID, filePath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error_msg": "File not found"})
		return
	}

	// Generate document key
	docKey := generateDocKey(repoID, filePath, fileID)

	// Determine edit mode
	mode := "view"
	if h.canEditFile(filename) {
		mode = "edit"
	}

	// Generate download URL for OnlyOffice to fetch the file
	downloadToken, err := h.tokenCreator.CreateDownloadToken(orgID, repoID, filePath, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error_msg": "Failed to generate download token"})
		return
	}

	// When no OnlyOffice-specific override is configured, use the current
	// browser-facing SesameFS origin so a separate OnlyOffice deployment only
	// needs api_js_url + jwt_secret.
	ooServerURL := resolveOnlyOfficeServerURL(c, h.config.OnlyOffice.ServerURL, h.serverURL)
	downloadURL := fmt.Sprintf("%s/seafhttp/files/%s/%s", ooServerURL, downloadToken, filename)

	// The callback only carries doc_key. The server resolves repo/file/user from the
	// stored mapping so callback callers cannot override them with request params.
	callbackURL := fmt.Sprintf("%s/onlyoffice/editor-callback/?doc_key=%s",
		ooServerURL, url.QueryEscape(docKey))

	// Get user info
	userName := strings.Split(userID, "@")[0]
	if userName == userID {
		userName = userID
	}

	// Build OnlyOffice configuration (minimal, like Seahub)
	canEdit := mode == "edit"
	docConfig := OnlyOfficeConfig{
		Document: OnlyOfficeDocument{
			FileType: strings.TrimPrefix(filepath.Ext(filename), "."),
			Key:      docKey,
			Title:    filename,
			URL:      downloadURL,
			Permissions: &OnlyOfficePermissions{
				Edit:      canEdit,
				Download:  true,
				Print:     true,
				Copy:      true,
				Review:    canEdit,
				Comment:   canEdit,
				FillForms: canEdit,
			},
		},
		DocumentType: getDocumentType(filename),
		EditorConfig: OnlyOfficeEditorConfig{
			CallbackURL: callbackURL,
			Mode:        mode,
			User: OnlyOfficeUser{
				ID:   userID,
				Name: userName,
			},
			Customization: &OnlyOfficeCustomization{
				Forcesave:      canEdit,
				SubmitForm:     canEdit,
				CompactToolbar: false,
				CompactHeader:  false,
			},
		},
	}

	// Sign JWT. OnlyOffice sessions must never be served without a token.
	token, err := h.signJWT(docConfig)
	if err != nil {
		log.Printf("Failed to sign OnlyOffice JWT: %v", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error_msg": "Failed to initialize OnlyOffice JWT"})
		return
	}
	docConfig.Token = token

	// Store doc_key mapping in database for callback lookup. Without this mapping,
	// the callback cannot be bound safely to the original document and user.
	if err := h.saveDocKeyMapping(docKey, userID, repoID, filePath); err != nil {
		log.Printf("Failed to save doc_key mapping: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error_msg": "Failed to initialize OnlyOffice session"})
		return
	}

	// Return response
	response := OnlyOfficeResponse{
		Doc:      docConfig,
		APIJSURL: h.config.OnlyOffice.APIJSURL,
	}

	c.JSON(http.StatusOK, response)
}

// getFileID retrieves the file ID from fs_objects by traversing the path
func (h *OnlyOfficeHandler) getFileID(repoID, orgID, filePath string) (string, error) {
	if h == nil || h.db == nil {
		return "", fmt.Errorf("database not available")
	}

	// Get library's head_commit_id using libraries_by_id (no org_id dependency)
	// This avoids failures when org_id doesn't match the library's partition key
	var headCommitID string
	err := h.db.Session().Query(`
		SELECT head_commit_id FROM libraries_by_id
		WHERE library_id = ?
	`, repoID).Scan(&headCommitID)
	if err != nil {
		return "", fmt.Errorf("library not found: %w", err)
	}

	// Get root_fs_id from the head commit
	var rootFSID string
	err = h.db.Session().Query(`
		SELECT root_fs_id FROM commits
		WHERE library_id = ? AND commit_id = ?
	`, repoID, headCommitID).Scan(&rootFSID)
	if err != nil {
		return "", fmt.Errorf("commit not found: %w", err)
	}

	// Traverse to the file
	parts := strings.Split(strings.Trim(filePath, "/"), "/")
	currentFSID := rootFSID

	for i, part := range parts {
		if part == "" {
			continue
		}

		var entriesJSON string
		err = h.db.Session().Query(`
			SELECT dir_entries FROM fs_objects
			WHERE library_id = ? AND fs_id = ?
		`, repoID, currentFSID).Scan(&entriesJSON)
		if err != nil {
			return "", fmt.Errorf("fs_object not found: %w", err)
		}

		var entries []FSEntry
		if entriesJSON != "" && entriesJSON != "[]" {
			if err := json.Unmarshal([]byte(entriesJSON), &entries); err != nil {
				return "", fmt.Errorf("invalid directory data: %w", err)
			}
		}

		found := false
		for _, entry := range entries {
			if entry.Name == part {
				currentFSID = entry.ID
				found = true
				// If this is the last part, we found the file
				if i == len(parts)-1 {
					return entry.ID, nil
				}
				break
			}
		}

		if !found {
			return "", fmt.Errorf("path component not found: %s", part)
		}
	}

	return "", fmt.Errorf("file not found")
}

// saveDocKeyMapping stores the doc_key to file mapping for callback lookup
func (h *OnlyOfficeHandler) saveDocKeyMapping(docKey, userID, repoID, filePath string) error {
	if h == nil || h.db == nil {
		return fmt.Errorf("database not available")
	}

	return h.db.Session().Query(`
		INSERT INTO onlyoffice_doc_keys (doc_key, user_id, repo_id, file_path, created_at)
		VALUES (?, ?, ?, ?, ?)
		USING TTL 86400
	`, docKey, userID, repoID, filePath, time.Now()).Exec()
}

// getDocKeyMapping retrieves file info by doc_key
func (h *OnlyOfficeHandler) getDocKeyMapping(docKey string) (userID, repoID, filePath string, err error) {
	if h == nil || h.db == nil {
		return "", "", "", fmt.Errorf("database not available")
	}

	err = h.db.Session().Query(`
		SELECT user_id, repo_id, file_path FROM onlyoffice_doc_keys
		WHERE doc_key = ?
	`, docKey).Scan(&userID, &repoID, &filePath)
	return
}

// deleteDocKeyMapping removes the doc_key mapping
func (h *OnlyOfficeHandler) deleteDocKeyMapping(docKey string) error {
	if h == nil || h.db == nil {
		return fmt.Errorf("database not available")
	}

	return h.db.Session().Query(`
		DELETE FROM onlyoffice_doc_keys WHERE doc_key = ?
	`, docKey).Exec()
}

// OnlyOfficeCallbackRequest represents the callback request from OnlyOffice
type OnlyOfficeCallbackRequest struct {
	Status int      `json:"status"`
	URL    string   `json:"url,omitempty"`
	Key    string   `json:"key"`
	Users  []string `json:"users,omitempty"`
	Token  string   `json:"token,omitempty"` // JWT token from OnlyOffice (when JWT is configured)
}

func extractOnlyOfficeJWT(body []byte, authHeader string) (string, error) {
	authHeader = strings.TrimSpace(authHeader)
	if authHeader != "" {
		fields := strings.Fields(authHeader)
		if len(fields) == 1 {
			return fields[0], nil
		}
		if len(fields) == 2 {
			if strings.EqualFold(fields[0], "Bearer") || strings.EqualFold(fields[0], "Token") {
				return fields[1], nil
			}
		}
	}

	var outer struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &outer); err != nil {
		return "", fmt.Errorf("parse callback body: %w", err)
	}
	if strings.TrimSpace(outer.Token) == "" {
		return "", fmt.Errorf("missing JWT token in callback body or Authorization header")
	}
	return strings.TrimSpace(outer.Token), nil
}

// verifyCallbackJWT verifies the JWT token sent by OnlyOffice in the callback.
// When JWTSecret is configured, OnlyOffice wraps the real payload inside a JWT
// in the "token" field of the JSON body (or in the Authorization header).
// Returns the verified payload as an OnlyOfficeCallbackRequest, or an error.
func (h *OnlyOfficeHandler) verifyCallbackJWT(body []byte, authHeader string) (*OnlyOfficeCallbackRequest, error) {
	secret := strings.TrimSpace(h.config.OnlyOffice.JWTSecret)
	if secret == "" {
		return nil, fmt.Errorf("OnlyOffice JWT secret is not configured")
	}

	tokenString, err := extractOnlyOfficeJWT(body, authHeader)
	if err != nil {
		return nil, err
	}

	// Verify the JWT signature
	token, err := jwt.Parse(tokenString, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, fmt.Errorf("JWT verification failed: %w", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid JWT claims")
	}

	// OnlyOffice puts the real payload inside a "payload" claim
	payloadRaw, exists := claims["payload"]
	if !exists {
		// Some OnlyOffice versions put fields directly in claims
		payloadRaw = map[string]interface{}(claims)
	}

	// Re-marshal and unmarshal into our struct
	payloadBytes, err := json.Marshal(payloadRaw)
	if err != nil {
		return nil, fmt.Errorf("marshal JWT payload: %w", err)
	}

	var req OnlyOfficeCallbackRequest
	if err := json.Unmarshal(payloadBytes, &req); err != nil {
		return nil, fmt.Errorf("parse JWT payload: %w", err)
	}

	return &req, nil
}

// EditorCallback handles the OnlyOffice callback
// Implements: POST /onlyoffice/editor-callback/
//
// Status codes from OnlyOffice:
// 1 - Document is being edited
// 2 - Document is ready for saving
// 4 - Document closed with no changes
// 6 - Document editing error / force save in progress
func (h *OnlyOfficeHandler) EditorCallback(c *gin.Context) {
	if !h.config.OnlyOffice.Enabled {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": 1})
		return
	}
	if strings.TrimSpace(h.config.OnlyOffice.JWTSecret) == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": 1})
		return
	}

	// Read body once (needed for JWT verification)
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20)) // 1 MB max
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": 1})
		return
	}

	// Verify JWT signature BEFORE processing the payload.
	// When JWTSecret is configured, OnlyOffice signs the callback body.
	// This prevents unauthenticated callers from triggering SSRF via the URL field.
	reqPtr, err := h.verifyCallbackJWT(body, c.GetHeader("Authorization"))
	if err != nil {
		log.Printf("OnlyOffice callback: JWT verification failed: %v", err)
		c.JSON(http.StatusForbidden, gin.H{"error": 1})
		return
	}
	req := *reqPtr

	log.Printf("OnlyOffice callback: status=%d, key=%s, url=%s", req.Status, req.Key, req.URL)

	docKey := strings.TrimSpace(c.Query("doc_key"))
	if strings.TrimSpace(req.Key) != "" {
		docKey = strings.TrimSpace(req.Key)
	}

	switch req.Status {
	case 1:
		// Document is being edited - nothing to do
		c.JSON(http.StatusOK, gin.H{"error": 0})

	case 4:
		// Document closed with no changes. If the mapping is already gone, keep the
		// callback idempotent and return success.
		if docKey != "" {
			if err := h.deleteDocKeyMapping(docKey); err != nil {
				log.Printf("OnlyOffice callback: failed to delete doc_key mapping: %v", err)
			}
		}
		c.JSON(http.StatusOK, gin.H{"error": 0})

	case 2, 6:
		// Document ready for saving (2) or force save (6)
		if req.URL == "" {
			log.Printf("OnlyOffice callback: no URL provided for save")
			c.JSON(http.StatusOK, gin.H{"error": 0})
			return
		}
		if docKey == "" {
			log.Printf("OnlyOffice callback: missing doc_key for save callback")
			c.JSON(http.StatusOK, gin.H{"error": 1})
			return
		}

		userID, repoID, filePath, err := h.getDocKeyMapping(docKey)
		if err != nil {
			log.Printf("OnlyOffice callback: failed to get doc_key mapping: %v", err)
			c.JSON(http.StatusOK, gin.H{"error": 1})
			return
		}
		filePath = normalizePath(filePath)

		// ========================================================================
		// PERMISSION CHECK: User must have write permission to save edits
		// ========================================================================
		if h.permMiddleware != nil {
			// Get org_id from context or database
			orgID := c.GetString("org_id")
			if orgID == "" {
				// Try to get from database using repo_id
				var libOrgID string
				err := h.db.Session().Query(`
					SELECT org_id FROM libraries_by_id WHERE library_id = ?
				`, repoID).Scan(&libOrgID)
				if err == nil {
					orgID = libOrgID
				}
			}

			if orgID != "" {
				hasWrite, err := h.permMiddleware.HasLibraryAccessCtx(c, orgID, userID, repoID, middleware.PermissionRW)
				if err != nil {
					log.Printf("[EditorCallback] Failed to check permissions: %v", err)
					c.JSON(http.StatusOK, gin.H{"error": 1})
					return
				}

				if !hasWrite {
					log.Printf("[EditorCallback] Permission denied: user %q does not have write permission to library %q", userID, repoID)
					c.JSON(http.StatusOK, gin.H{"error": 1})
					return
				}
			}
		}

		// Download the edited document from OnlyOffice
		err = h.saveEditedDocument(c.Request.Context(), repoID, filePath, req.URL, userID)
		if err != nil {
			log.Printf("OnlyOffice callback: failed to save document: %v", err)
			c.JSON(http.StatusOK, gin.H{"error": 1})
			return
		}

		// Delete doc_key mapping if status is 2 (close)
		if req.Status == 2 && docKey != "" {
			if err := h.deleteDocKeyMapping(docKey); err != nil {
				log.Printf("OnlyOffice callback: failed to delete doc_key mapping: %v", err)
			}
		}

		c.JSON(http.StatusOK, gin.H{"error": 0})

	default:
		log.Printf("OnlyOffice callback: unknown status %d", req.Status)
		c.JSON(http.StatusOK, gin.H{"error": 0})
	}
}

// onlyOfficeHTTPClient is a hardened HTTP client for downloading documents from OnlyOffice.
// Timeout prevents hung connections; CheckRedirect prevents redirect-based SSRF.
var onlyOfficeHTTPClient = &http.Client{
	Timeout: 60 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return fmt.Errorf("too many redirects")
		}
		return nil
	},
}

func readOnlyOfficeDocument(reader io.Reader, maxDocSize int64) ([]byte, error) {
	limited := &io.LimitedReader{R: reader, N: maxDocSize + 1}
	content, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > maxDocSize {
		return nil, fmt.Errorf("document exceeds maximum allowed size of %d bytes", maxDocSize)
	}
	return content, nil
}

// validateOnlyOfficeDownloadURL checks that the translated download URL points to
// the configured OnlyOffice internal host. This prevents SSRF even if JWT is
// compromised — the attacker cannot redirect downloads to arbitrary hosts.
func (h *OnlyOfficeHandler) validateOnlyOfficeDownloadURL(downloadURL string) error {
	parsed, err := url.Parse(downloadURL)
	if err != nil {
		return fmt.Errorf("invalid download URL: %w", err)
	}

	// Must be http or https
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("download URL scheme %q not allowed", parsed.Scheme)
	}

	// If InternalURL is configured, the download must be on that host
	if h.config.OnlyOffice.InternalURL != "" {
		allowed, err := url.Parse(h.config.OnlyOffice.InternalURL)
		if err != nil {
			return fmt.Errorf("invalid OnlyOffice internal_url config: %w", err)
		}
		if !strings.EqualFold(parsed.Host, allowed.Host) {
			return fmt.Errorf("download URL host %q does not match configured internal_url host %q", parsed.Host, allowed.Host)
		}
		return nil
	}

	// If APIJSURL is configured, allow that host too (non-internal setup)
	if h.config.OnlyOffice.APIJSURL != "" {
		apiParsed, err := url.Parse(h.config.OnlyOffice.APIJSURL)
		if err == nil && strings.EqualFold(parsed.Host, apiParsed.Host) {
			return nil
		}
	}

	return fmt.Errorf("download URL host %q does not match any configured OnlyOffice host", parsed.Host)
}

// saveEditedDocument downloads the edited document and saves it to storage
func (h *OnlyOfficeHandler) saveEditedDocument(ctx context.Context, repoID, filePath, downloadURL, userID string) error {
	// OnlyOffice sends URLs with the browser-accessible URL (api_js_url host).
	// We need to translate this to the internal Docker network URL (internal_url).
	// Example: http://localhost:8088/... -> http://onlyoffice:80/...
	internalURL := downloadURL
	if h.config.OnlyOffice.InternalURL != "" && h.config.OnlyOffice.APIJSURL != "" {
		// Extract the base URL from api_js_url (e.g., "http://localhost:8088" from "http://localhost:8088/web-apps/...")
		apiJSURL := h.config.OnlyOffice.APIJSURL
		if idx := strings.Index(apiJSURL, "/web-apps"); idx > 0 {
			externalBase := apiJSURL[:idx]
			internalURL = strings.Replace(internalURL, externalBase, h.config.OnlyOffice.InternalURL, 1)
		}
	}

	// Validate the download URL points to a known OnlyOffice host (SSRF protection)
	if err := h.validateOnlyOfficeDownloadURL(internalURL); err != nil {
		return fmt.Errorf("SSRF protection: %w", err)
	}

	log.Printf("OnlyOffice: downloading document from %s", internalURL)

	// Download the document from OnlyOffice using a hardened HTTP client
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, internalURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create download request: %w", err)
	}
	resp, err := onlyOfficeHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download document: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned status %d", resp.StatusCode)
	}

	// Read the content with the configured size limit.
	maxDocSize := h.config.OnlyOffice.MaxDocumentBytes
	content, err := readOnlyOfficeDocument(resp.Body, maxDocSize)
	if err != nil {
		return fmt.Errorf("failed to read document: %w", err)
	}

	// Track original file size and content (before encryption)
	originalFileSize := int64(len(content))
	originalContent := content // Save for SHA-1 hash calculation

	// Get org_id and encryption info from library lookup table
	var orgID string
	var encrypted bool
	err = h.db.Session().Query(`
		SELECT org_id, encrypted FROM libraries_by_id WHERE library_id = ?
	`, repoID).Scan(&orgID, &encrypted)
	if err != nil {
		return fmt.Errorf("library not found: %w", err)
	}

	// If library is encrypted, encrypt the content before storage
	if encrypted {
		// Get file key from decrypt session (user must have unlocked the library)
		fileKey := GetDecryptSessions().GetFileKey(userID, repoID)
		if fileKey == nil {
			return fmt.Errorf("library is encrypted but not unlocked - cannot save")
		}

		// Encrypt the content using Seafile block encryption format
		originalSize := len(content)
		encryptedContent, err := crypto.EncryptBlock(content, fileKey)
		if err != nil {
			return fmt.Errorf("failed to encrypt content: %w", err)
		}
		content = encryptedContent
		log.Printf("OnlyOffice: encrypted content for library %s (original: %d bytes, encrypted: %d bytes)", repoID, originalSize, len(content))
	}

	// Calculate SHA-1 hash of ORIGINAL content for external block ID (Seafile client compatibility)
	sha1Hash := sha1.Sum(originalContent)
	externalBlockID := hex.EncodeToString(sha1Hash[:])

	// Calculate SHA-256 hash for internal storage (hash of stored content, encrypted or not)
	sha256Hash := sha256.Sum256(content)
	internalBlockID := hex.EncodeToString(sha256Hash[:])

	blockStore, storageClass, err := h.resolveLibraryBlockStore(orgID, repoID)
	if err != nil || blockStore == nil {
		return fmt.Errorf("failed to resolve block storage: %w", err)
	}

	storageKey, err := blockStore.PutBlockData(ctx, &storage.BlockData{
		Hash: internalBlockID,
		Data: content,
		Size: int64(len(content)),
	})
	if err != nil {
		return fmt.Errorf("failed to store block: %w", err)
	}

	// Create SHA-1 → SHA-256 mapping for sync protocol compatibility (dual-write: forward + reverse)
	if err := h.db.Session().Query(`
		INSERT INTO block_id_mappings (org_id, external_id, internal_id) VALUES (?, ?, ?)
	`, orgID, externalBlockID, internalBlockID).Exec(); err != nil {
		log.Printf("OnlyOffice: Warning - failed to create block mapping: %v", err)
	} else {
		log.Printf("OnlyOffice: Created block mapping: %s → %s", externalBlockID[:16], internalBlockID[:16])
	}
	if err := h.db.Session().Query(`
		INSERT INTO block_id_mappings_by_internal (org_id, internal_id, external_id, created_at) VALUES (?, ?, ?, toTimestamp(now()))
	`, orgID, internalBlockID, externalBlockID).Exec(); err != nil {
		log.Printf("OnlyOffice: Warning - failed to create reverse block mapping: %v", err)
	}

	// Store block metadata using internal (SHA-256) ID
	if err := NewFSHelper(h.db).IncrementOrCreateBlock(orgID, internalBlockID, len(content), storageClass, storageKey); err != nil {
		log.Printf("Failed to store block metadata: %v", err)
	}

	// Use FSHelper to properly update the file tree and create a commit
	fsHelper := NewFSHelper(h.db)
	now := time.Now()
	filename := path.Base(filePath)

	// Create new FS object for the file (use external SHA-1 block ID for Seafile client compatibility)
	newFileFSID, err := fsHelper.CreateFileFSObject(repoID, filename, originalFileSize, []string{externalBlockID})
	if err != nil {
		return fmt.Errorf("failed to create file fs_object: %w", err)
	}

	// Traverse to the file's location
	result, err := fsHelper.TraverseToPath(repoID, filePath)
	if err != nil {
		return fmt.Errorf("failed to traverse to path: %w", err)
	}

	// Update the entry in parent directory
	updatedEntries := make([]FSEntry, 0, len(result.Entries))
	fileUpdated := false
	for _, entry := range result.Entries {
		if entry.Name == filename {
			// Update the file entry with new fs_id (use original size, not encrypted size)
			entry.ID = newFileFSID
			entry.Size = originalFileSize
			entry.MTime = now.Unix()
			entry.Modifier = userID + "@sesamefs.local" // CRITICAL: Required for correct fs_id hash
			fileUpdated = true
		}
		updatedEntries = append(updatedEntries, entry)
	}

	// If file wasn't found in entries, add it (shouldn't happen for edit, but handle it)
	if !fileUpdated {
		updatedEntries = append(updatedEntries, FSEntry{
			ID:       newFileFSID,
			Name:     filename,
			Mode:     ModeFile,
			MTime:    now.Unix(),
			Size:     originalFileSize,
			Modifier: userID + "@sesamefs.local", // CRITICAL: Required for correct fs_id hash
		})
	}

	// Create new parent directory fs_object
	newParentFSID, err := fsHelper.CreateDirectoryFSObject(repoID, updatedEntries)
	if err != nil {
		return fmt.Errorf("failed to create parent fs_object: %w", err)
	}

	// Rebuild path to root
	newRootFSID, err := fsHelper.RebuildPathToRoot(repoID, result, newParentFSID)
	if err != nil {
		return fmt.Errorf("failed to rebuild path: %w", err)
	}

	// Get current head commit
	headCommitID, err := fsHelper.GetHeadCommitID(repoID)
	if err != nil {
		return fmt.Errorf("failed to get head commit: %w", err)
	}

	// Create new commit
	commitDesc := fmt.Sprintf("Modified \"%s\" via OnlyOffice", filename)
	newCommitID, err := fsHelper.CreateCommit(repoID, userID, newRootFSID, headCommitID, commitDesc)
	if err != nil {
		return fmt.Errorf("failed to create commit: %w", err)
	}

	// Update library head
	if err := fsHelper.UpdateLibraryHead(orgID, repoID, newCommitID); err != nil {
		return fmt.Errorf("failed to update library head: %w", err)
	}

	log.Printf("OnlyOffice: saved document %s with block %s (internal: %s), new commit %s", filePath, externalBlockID[:16], internalBlockID[:16], newCommitID)
	return nil
}

// generateFSID creates a unique FS object ID (SHA-1 hash of content)
func generateFSID(content []byte) string {
	hash := sha1.Sum(content)
	return hex.EncodeToString(hash[:])
}
