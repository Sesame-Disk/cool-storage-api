package v2

import (
	"bytes"
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
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// OnlyOfficeHandler handles OnlyOffice integration
type OnlyOfficeHandler struct {
	db           *db.DB
	config       *config.Config
	storage      *storage.S3Store
	tokenCreator TokenCreator
	serverURL    string
}

// RegisterOnlyOfficeRoutes registers OnlyOffice routes
func RegisterOnlyOfficeRoutes(rg *gin.RouterGroup, database *db.DB, cfg *config.Config, s3Store *storage.S3Store, tokenCreator TokenCreator, serverURL string) {
	h := &OnlyOfficeHandler{
		db:           database,
		config:       cfg,
		storage:      s3Store,
		tokenCreator: tokenCreator,
		serverURL:    serverURL,
	}

	// v2.1 API endpoint for getting OnlyOffice editor config
	repos := rg.Group("/repos/:repo_id")
	{
		repos.GET("/onlyoffice", h.GetEditorConfig)
		repos.GET("/onlyoffice/", h.GetEditorConfig)
	}
}

// RegisterOnlyOfficeCallbackRoutes registers the callback route (under /onlyoffice/)
func RegisterOnlyOfficeCallbackRoutes(rg *gin.RouterGroup, database *db.DB, cfg *config.Config, s3Store *storage.S3Store, serverURL string) {
	h := &OnlyOfficeHandler{
		db:        database,
		config:    cfg,
		storage:   s3Store,
		serverURL: serverURL,
	}

	rg.POST("/editor-callback", h.EditorCallback)
	rg.POST("/editor-callback/", h.EditorCallback)
}

// OnlyOfficeDocument represents the document configuration
type OnlyOfficeDocument struct {
	FileType string `json:"fileType"`
	Key      string `json:"key"`
	Title    string `json:"title"`
	URL      string `json:"url"`
}

// OnlyOfficeUser represents user info for OnlyOffice
type OnlyOfficeUser struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// OnlyOfficeEditorConfig represents the editor configuration
type OnlyOfficeEditorConfig struct {
	CallbackURL string         `json:"callbackUrl"`
	Mode        string         `json:"mode"` // "edit" or "view"
	User        OnlyOfficeUser `json:"user"`
}

// OnlyOfficeConfig represents the full configuration returned to the frontend
type OnlyOfficeConfig struct {
	Document     OnlyOfficeDocument     `json:"document"`
	DocumentType string                 `json:"documentType"`
	EditorConfig OnlyOfficeEditorConfig `json:"editorConfig"`
	Token        string                 `json:"token,omitempty"`
}

// OnlyOfficeResponse represents the API response
type OnlyOfficeResponse struct {
	Doc      OnlyOfficeConfig `json:"doc"`
	APIJSURL string           `json:"api_js_url"`
}

// generateDocKey generates a unique document key for OnlyOffice
// Format: MD5(repo_id + file_path + file_id) truncated to 20 chars
// This matches Seahub's implementation in seahub/onlyoffice/utils.py
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
	if h.config.OnlyOffice.JWTSecret == "" {
		return "", nil
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

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(h.config.OnlyOffice.JWTSecret))
}

// GetEditorConfig returns the OnlyOffice editor configuration
// Implements: GET /api/v2.1/repos/:repo_id/onlyoffice/?p=/path
func (h *OnlyOfficeHandler) GetEditorConfig(c *gin.Context) {
	// Check if OnlyOffice is enabled
	if !h.config.OnlyOffice.Enabled {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error_msg": "OnlyOffice is not enabled"})
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

	// Use OnlyOffice-specific server URL if configured, otherwise fall back to general server URL
	// This allows configuring a public URL that OnlyOffice server can reach
	ooServerURL := h.config.OnlyOffice.ServerURL
	if ooServerURL == "" {
		ooServerURL = h.serverURL
	}
	downloadURL := fmt.Sprintf("%s/seafhttp/files/%s/%s", ooServerURL, downloadToken, filename)

	// Generate callback URL
	callbackURL := fmt.Sprintf("%s/onlyoffice/editor-callback/?repo_id=%s&file_path=%s&doc_key=%s",
		ooServerURL, repoID, filePath, docKey)

	// Get user info
	userName := strings.Split(userID, "@")[0]
	if userName == userID {
		userName = userID
	}

	// Build OnlyOffice configuration
	docConfig := OnlyOfficeConfig{
		Document: OnlyOfficeDocument{
			FileType: strings.TrimPrefix(filepath.Ext(filename), "."),
			Key:      docKey,
			Title:    filename,
			URL:      downloadURL,
		},
		DocumentType: getDocumentType(filename),
		EditorConfig: OnlyOfficeEditorConfig{
			CallbackURL: callbackURL,
			Mode:        mode,
			User: OnlyOfficeUser{
				ID:   userID,
				Name: userName,
			},
		},
	}

	// Sign JWT if secret is configured
	if h.config.OnlyOffice.JWTSecret != "" {
		token, err := h.signJWT(docConfig)
		if err != nil {
			log.Printf("Failed to sign OnlyOffice JWT: %v", err)
		} else {
			docConfig.Token = token
		}
	}

	// Store doc_key mapping in database for callback lookup
	if err := h.saveDocKeyMapping(docKey, userID, repoID, filePath); err != nil {
		log.Printf("Failed to save doc_key mapping: %v", err)
		// Non-fatal, continue
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
	// Get library's head_commit_id
	var headCommitID string
	err := h.db.Session().Query(`
		SELECT head_commit_id FROM libraries
		WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&headCommitID)
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
	return h.db.Session().Query(`
		INSERT INTO onlyoffice_doc_keys (doc_key, user_id, repo_id, file_path, created_at)
		VALUES (?, ?, ?, ?, ?)
		USING TTL 86400
	`, docKey, userID, repoID, filePath, time.Now()).Exec()
}

// getDocKeyMapping retrieves file info by doc_key
func (h *OnlyOfficeHandler) getDocKeyMapping(docKey string) (userID, repoID, filePath string, err error) {
	err = h.db.Session().Query(`
		SELECT user_id, repo_id, file_path FROM onlyoffice_doc_keys
		WHERE doc_key = ?
	`, docKey).Scan(&userID, &repoID, &filePath)
	return
}

// deleteDocKeyMapping removes the doc_key mapping
func (h *OnlyOfficeHandler) deleteDocKeyMapping(docKey string) error {
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
	var req OnlyOfficeCallbackRequest

	// Read the body for JWT verification if needed
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": 1})
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewBuffer(body))

	// Parse the request
	if err := json.Unmarshal(body, &req); err != nil {
		log.Printf("OnlyOffice callback: failed to parse request: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": 1})
		return
	}

	log.Printf("OnlyOffice callback: status=%d, key=%s, url=%s", req.Status, req.Key, req.URL)

	// Get document info from query params or database
	repoID := c.Query("repo_id")
	filePath := c.Query("file_path")
	docKey := c.Query("doc_key")

	// If not in query params, try to get from database using the key
	if repoID == "" || filePath == "" {
		if req.Key != "" {
			docKey = req.Key
		}
		if docKey != "" {
			var userID string
			userID, repoID, filePath, err = h.getDocKeyMapping(docKey)
			if err != nil {
				log.Printf("OnlyOffice callback: failed to get doc_key mapping: %v", err)
				// Still return success to OnlyOffice
				c.JSON(http.StatusOK, gin.H{"error": 0})
				return
			}
			_ = userID // May be used for permissions in the future
		}
	}

	switch req.Status {
	case 1:
		// Document is being edited - nothing to do
		c.JSON(http.StatusOK, gin.H{"error": 0})

	case 2, 6:
		// Document ready for saving (2) or force save (6)
		if req.URL == "" {
			log.Printf("OnlyOffice callback: no URL provided for save")
			c.JSON(http.StatusOK, gin.H{"error": 0})
			return
		}

		// Download the edited document from OnlyOffice
		err := h.saveEditedDocument(c.Request.Context(), repoID, filePath, req.URL)
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

	case 4:
		// Document closed with no changes
		if docKey != "" {
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

// saveEditedDocument downloads the edited document and saves it to storage
func (h *OnlyOfficeHandler) saveEditedDocument(ctx context.Context, repoID, filePath, downloadURL string) error {
	// Download the document from OnlyOffice
	resp, err := http.Get(downloadURL)
	if err != nil {
		return fmt.Errorf("failed to download document: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned status %d", resp.StatusCode)
	}

	// Read the content
	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read document: %w", err)
	}

	// Get org_id from library
	var orgID string
	err = h.db.Session().Query(`
		SELECT org_id FROM libraries WHERE library_id = ? ALLOW FILTERING
	`, repoID).Scan(&orgID)
	if err != nil {
		return fmt.Errorf("library not found: %w", err)
	}

	// Calculate SHA-256 hash for block ID
	hash := sha256.Sum256(content)
	blockID := hex.EncodeToString(hash[:])

	// Store the block
	storageKey := fmt.Sprintf("%s/%s", orgID, blockID)
	if h.storage != nil {
		_, err = h.storage.Put(ctx, storageKey, bytes.NewReader(content), int64(len(content)))
		if err != nil {
			return fmt.Errorf("failed to store block: %w", err)
		}
	}

	// Store block metadata
	now := time.Now()
	if err := h.db.Session().Query(`
		INSERT INTO blocks (org_id, block_id, size_bytes, storage_class, storage_key, ref_count, created_at, last_accessed)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, orgID, blockID, len(content), h.config.Storage.DefaultClass, storageKey, 1, now, now).Exec(); err != nil {
		log.Printf("Failed to store block metadata: %v", err)
	}

	// Get current head commit
	var headCommitID string
	err = h.db.Session().Query(`
		SELECT head_commit_id FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&headCommitID)
	if err != nil {
		return fmt.Errorf("failed to get head commit: %w", err)
	}

	// Create new FS object for the file
	filename := path.Base(filePath)
	fsID := generateFSID(content)

	// Store FS object
	if err := h.db.Session().Query(`
		INSERT INTO fs_objects (library_id, fs_id, obj_type, obj_name, file_size, block_ids, mtime)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, repoID, fsID, "file", filename, len(content), []string{blockID}, now.Unix()).Exec(); err != nil {
		log.Printf("Failed to store fs_object: %v", err)
	}

	// TODO: Update directory structure and create new commit
	// This requires traversing the tree and updating parent directories
	// For now, we just store the file - full implementation needs commit logic

	log.Printf("OnlyOffice: saved document %s with block %s", filePath, blockID)
	return nil
}

// generateFSID creates a unique FS object ID (SHA-1 hash of content)
func generateFSID(content []byte) string {
	hash := sha1.Sum(content)
	return hex.EncodeToString(hash[:])
}
