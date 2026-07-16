package v2

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
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

var onlyOfficeCleanupFailedPublishAttemptFn = func(database *db.DB, orgID, repoID, attemptID, commitID string, fsIDs, blockIDs []string) error {
	return CleanupFailedPublishArtifacts(database, orgID, repoID, attemptID, commitID, fsIDs, blockIDs)
}

var onlyOfficeClearPendingPublishedFileRepairsFn = clearPendingPublishedFileRepairs

var onlyOfficeReleaseUploadedBlockRefsFn = ReleaseUploadedBlockRefs

var onlyOfficeDeletePendingBlockFn = func(h *OnlyOfficeHandler, orgID, operationID string) error {
	return h.deleteOnlyOfficePendingBlock(orgID, operationID)
}

var onlyOfficeAdjustStorageCountersFn = traffic.AdjustStorageCountersByDeltaSync

func cleanupOnlyOfficeFailedPublishAttempt(database *db.DB, orgID, repoID, commitID string, pendingFiles []*pendingPublishedFile) error {
	fsIDs := pendingPublishedFileFSIDs(pendingFiles)
	blockIDs := pendingPublishedFileInternalBlockIDs(pendingFiles)
	cleanupErr := onlyOfficeCleanupFailedPublishAttemptFn(database, orgID, repoID, commitID, commitID, fsIDs, blockIDs)
	if cleanupErr != nil {
		return cleanupErr
	}
	ownerErr := releasePendingPublishedFileOwnersFn(database, repoID, pendingFiles)
	clearErr := onlyOfficeClearPendingPublishedFileRepairsFn(database, orgID, repoID, commitID, pendingFiles)
	return errors.Join(ownerErr, clearErr)
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
		return h.storageManager.GetHealthyBlockStoreForOrg(orgID, preferredClass)
	}
	if libraryClass == "" && h.config != nil {
		libraryClass = h.config.Storage.DefaultClass
	}
	// Fallback: org-scoped store from the raw S3 store; never the org-less singleton.
	if h.storage != nil {
		bs, err := storage.NewOrgBlockStore(h.storage, "blocks/", orgID)
		if err != nil {
			return nil, libraryClass, err
		}
		return bs, libraryClass, nil
	}
	return nil, libraryClass, fmt.Errorf("block storage not available")
}

func shouldRollbackOnlyOfficeMaterializedBlock(blockMetadataRegistered bool, publishErr error) bool {
	return blockMetadataRegistered && publishErr != nil
}

func onlyOfficeRollbackOperationKey(rollbackID string) string {
	return "onlyoffice-publish-failed:" + strings.TrimSpace(rollbackID)
}

const onlyOfficePendingBlockStaleAfter = 5 * time.Minute

type onlyOfficePendingBlock struct {
	OperationID     string
	RepoID          string
	FilePath        string
	InternalBlockID string
	ExternalBlockID string
	StorageClass    string
	PublishCommitID string
	CreatedAt       time.Time
}

func onlyOfficeCommitReachable(targetCommitID, headCommitID string, parentLookup func(string) (string, error)) (bool, error) {
	targetCommitID = strings.TrimSpace(targetCommitID)
	headCommitID = strings.TrimSpace(headCommitID)
	if targetCommitID == "" || headCommitID == "" {
		return false, nil
	}

	visited := make(map[string]struct{})
	currentCommitID := headCommitID
	for currentCommitID != "" {
		if currentCommitID == targetCommitID {
			return true, nil
		}
		if _, seen := visited[currentCommitID]; seen {
			return false, fmt.Errorf("detected commit ancestry cycle at %s", currentCommitID)
		}
		visited[currentCommitID] = struct{}{}

		parentCommitID, err := parentLookup(currentCommitID)
		if err != nil {
			return false, err
		}
		currentCommitID = strings.TrimSpace(parentCommitID)
	}

	return false, nil
}

func shouldTreatOnlyOfficeHeadLookupAsMissing(err error) bool {
	return errors.Is(err, gocql.ErrNotFound)
}

func encryptOnlyOfficeContent(userID, repoID string, content []byte) ([]byte, error) {
	fileKey, fileIV := GetDecryptSessions().GetFileKeyAndIV(userID, repoID)
	if fileKey == nil {
		return nil, fmt.Errorf("library is encrypted but not unlocked - cannot save")
	}
	if len(fileIV) == crypto.IVSize {
		return crypto.EncryptBlockSeafile(content, fileKey, fileIV)
	}
	return crypto.EncryptBlock(content, fileKey)
}

func (h *OnlyOfficeHandler) saveOnlyOfficePendingBlock(orgID string, pending onlyOfficePendingBlock) error {
	if h == nil || h.db == nil {
		return fmt.Errorf("database not available")
	}
	now := time.Now()
	if pending.CreatedAt.IsZero() {
		pending.CreatedAt = now
	}

	return h.db.Session().Query(`
		INSERT INTO onlyoffice_pending_blocks (
			org_id, operation_id, repo_id, file_path, internal_block_id,
			external_block_id, storage_class, publish_commit_id, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, orgID, pending.OperationID, pending.RepoID, pending.FilePath, pending.InternalBlockID,
		pending.ExternalBlockID, pending.StorageClass, pending.PublishCommitID, pending.CreatedAt, now).Exec()
}

func (h *OnlyOfficeHandler) updateOnlyOfficePendingBlockCommitID(orgID, operationID, commitID string) error {
	if h == nil || h.db == nil {
		return fmt.Errorf("database not available")
	}
	return h.db.Session().Query(`
		UPDATE onlyoffice_pending_blocks
		SET publish_commit_id = ?, updated_at = ?
		WHERE org_id = ? AND operation_id = ?
	`, commitID, time.Now(), orgID, operationID).Exec()
}

func (h *OnlyOfficeHandler) deleteOnlyOfficePendingBlock(orgID, operationID string) error {
	if h == nil || h.db == nil {
		return fmt.Errorf("database not available")
	}
	return h.db.Session().Query(`
		DELETE FROM onlyoffice_pending_blocks WHERE org_id = ? AND operation_id = ?
	`, orgID, operationID).Exec()
}

func (h *OnlyOfficeHandler) listOnlyOfficePendingBlocks(orgID string) ([]onlyOfficePendingBlock, error) {
	if h == nil || h.db == nil {
		return nil, fmt.Errorf("database not available")
	}

	iter := h.db.Session().Query(`
		SELECT operation_id, repo_id, file_path, internal_block_id, external_block_id,
		       storage_class, publish_commit_id, created_at
		FROM onlyoffice_pending_blocks WHERE org_id = ?
	`, orgID).Iter()

	var pending []onlyOfficePendingBlock
	var row onlyOfficePendingBlock
	for iter.Scan(
		&row.OperationID,
		&row.RepoID,
		&row.FilePath,
		&row.InternalBlockID,
		&row.ExternalBlockID,
		&row.StorageClass,
		&row.PublishCommitID,
		&row.CreatedAt,
	) {
		pending = append(pending, row)
		row = onlyOfficePendingBlock{}
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return pending, nil
}

func (h *OnlyOfficeHandler) isOnlyOfficeCommitReachableFromHead(repoID, targetCommitID string) (bool, error) {
	if h == nil || h.db == nil {
		return false, fmt.Errorf("database not available")
	}

	fsHelper := NewFSHelper(h.db)
	headCommitID, err := fsHelper.GetHeadCommitID(repoID)
	if err != nil {
		if shouldTreatOnlyOfficeHeadLookupAsMissing(err) {
			return false, nil
		}
		return false, fmt.Errorf("lookup current head for repo %s: %w", repoID, err)
	}

	return onlyOfficeCommitReachable(targetCommitID, headCommitID, func(commitID string) (string, error) {
		var parentCommitID string
		err := h.db.Session().Query(`
			SELECT parent_id FROM commits WHERE library_id = ? AND commit_id = ?
		`, repoID, commitID).Scan(&parentCommitID)
		if err != nil {
			if errors.Is(err, gocql.ErrNotFound) {
				return "", nil
			}
			return "", fmt.Errorf("lookup parent for commit %s: %w", commitID, err)
		}
		return parentCommitID, nil
	})
}

func (h *OnlyOfficeHandler) reconcileStaleOnlyOfficePendingBlocks(orgID string) {
	if err := h.ReconcileOnlyOfficePendingBlocks(orgID); err != nil {
		log.Printf("OnlyOffice: reconcile pending blocks for org %s: %v", orgID, err)
	}
}

// ReconcileOnlyOfficePendingBlocksForOrg is the entry point used by the GC
// scanner. It instantiates a minimal handler bound to the database and
// delegates to ReconcileOnlyOfficePendingBlocks, so other packages can drive
// reconciliation without depending on the full request-scoped handler.
func ReconcileOnlyOfficePendingBlocksForOrg(database *db.DB, orgID string) error {
	h := &OnlyOfficeHandler{db: database}
	return h.ReconcileOnlyOfficePendingBlocks(orgID)
}

// ReconcileOnlyOfficePendingBlocks scans the org's onlyoffice_pending_blocks
// rows and either drops rows whose publish commit is reachable from the
// current library head or rolls back materialized blocks for stale rows that
// were never published. Safe to call repeatedly; per-block decrement guards
// against double-decrement via DecrementBlockRefCountsOnce.
func (h *OnlyOfficeHandler) ReconcileOnlyOfficePendingBlocks(orgID string) error {
	if h == nil || h.db == nil {
		return fmt.Errorf("database not available")
	}
	if strings.TrimSpace(orgID) == "" {
		return nil
	}

	pendingBlocks, err := h.listOnlyOfficePendingBlocks(orgID)
	if err != nil {
		return fmt.Errorf("list pending block cleanups for org %s: %w", orgID, err)
	}
	if len(pendingBlocks) == 0 {
		return nil
	}

	cutoff := time.Now().Add(-onlyOfficePendingBlockStaleAfter)
	fsHelper := NewFSHelper(h.db)
	var firstErr error
	for _, pending := range pendingBlocks {
		if !pending.CreatedAt.IsZero() && pending.CreatedAt.After(cutoff) {
			continue
		}

		reachable := false
		if strings.TrimSpace(pending.PublishCommitID) != "" {
			reachable, err = h.isOnlyOfficeCommitReachableFromHead(pending.RepoID, pending.PublishCommitID)
			if err != nil {
				log.Printf("OnlyOffice: failed to reconcile pending block %s reachability: %v", pending.OperationID, err)
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
		}

		if reachable {
			if err := h.deleteOnlyOfficePendingBlock(orgID, pending.OperationID); err != nil {
				log.Printf("OnlyOffice: failed to clear reachable pending block %s: %v", pending.OperationID, err)
				if firstErr == nil {
					firstErr = err
				}
			}
			continue
		}

		if strings.TrimSpace(pending.InternalBlockID) != "" {
			zeroRefBlocks := fsHelper.ReleaseUploadReferences(orgID, pending.RepoID, pending.OperationID, []string{pending.InternalBlockID})
			enqueueZeroRefBlocks(h.db, orgID, pending.RepoID, zeroRefBlocks)
		}
		if err := h.deleteOnlyOfficePendingBlock(orgID, pending.OperationID); err != nil {
			log.Printf("OnlyOffice: failed to delete rolled back pending block %s: %v", pending.OperationID, err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// OnlyOfficeResponse represents the API response
type OnlyOfficeResponse struct {
	Doc      OnlyOfficeConfig `json:"doc"`
	APIJSURL string           `json:"api_js_url"`
}

const (
	dockerComposeFrontendURL   = "http://frontend"
	dockerComposeOnlyOfficeURL = "http://onlyoffice"
)

func isLoopbackURL(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func onlyOfficeAPIBaseURL(apiJSURL string) string {
	trimmed := strings.TrimSpace(apiJSURL)
	if trimmed == "" {
		return ""
	}
	if idx := strings.Index(trimmed, "/web-apps"); idx > 0 {
		return strings.TrimSuffix(trimmed[:idx], "/")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return strings.TrimSuffix(parsed.Scheme+"://"+parsed.Host, "/")
}

func resolveOnlyOfficeInternalURL(apiJSURL, internalURL string) string {
	if trimmed := strings.TrimSuffix(strings.TrimSpace(internalURL), "/"); trimmed != "" {
		return trimmed
	}
	if isLoopbackURL(apiJSURL) {
		return dockerComposeOnlyOfficeURL
	}
	return ""
}

func resolveOnlyOfficeServerURL(c *gin.Context, onlyOfficeServerURL, serverURL, apiJSURL string) string {
	if trimmed := strings.TrimSuffix(strings.TrimSpace(onlyOfficeServerURL), "/"); trimmed != "" {
		return trimmed
	}
	browserURL := httputil.GetBrowserURL(c, serverURL)
	if isLoopbackURL(browserURL) && isLoopbackURL(apiJSURL) {
		return dockerComposeFrontendURL
	}

	return browserURL
}

func buildOnlyOfficeDownloadURL(serverURL, downloadToken, filename string) string {
	return fmt.Sprintf("%s/seafhttp/files/%s/%s", serverURL, downloadToken, url.PathEscape(filename))
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
	ooServerURL := resolveOnlyOfficeServerURL(c, h.config.OnlyOffice.ServerURL, h.serverURL, h.config.OnlyOffice.APIJSURL)
	downloadURL := buildOnlyOfficeDownloadURL(ooServerURL, downloadToken, filename)

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
func (h *OnlyOfficeHandler) getFileID(repoID, _ string, filePath string) (string, error) {
	if h == nil || h.db == nil {
		return "", fmt.Errorf("database not available")
	}

	// Resolve the canonical live library row first so soft-deleted libraries are
	// fenced before OnlyOffice traverses the tree.
	libraryState, err := resolveLiveLibraryStateByIDFn(h.db.Session(), repoID)
	if err != nil {
		return "", wrapLiveLibraryStateError(err)
	}

	// Get root_fs_id from the head commit
	var rootFSID string
	err = h.db.Session().Query(`
		SELECT root_fs_id FROM commits
		WHERE library_id = ? AND commit_id = ?
	`, repoID, libraryState.HeadCommitID).Scan(&rootFSID)
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

	// If InternalURL is configured, or auto-detected for local Docker, the
	// download must be on that host.
	if resolvedInternalURL := resolveOnlyOfficeInternalURL(h.config.OnlyOffice.APIJSURL, h.config.OnlyOffice.InternalURL); resolvedInternalURL != "" {
		allowed, err := url.Parse(resolvedInternalURL)
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

func onlyOfficeStorageDelta(existing *FSEntry, newFileSize int64) (int64, int64) {
	if existing == nil {
		return newFileSize, 1
	}
	return newFileSize - existing.Size, 0
}

// saveEditedDocument downloads the edited document and saves it to storage
func (h *OnlyOfficeHandler) saveEditedDocument(ctx context.Context, repoID, filePath, downloadURL, userID string) error {
	// OnlyOffice sends URLs with the browser-accessible URL (api_js_url host).
	// We need to translate this to the internal Docker network URL (internal_url).
	// Example: http://localhost:8088/... -> http://onlyoffice:80/...
	internalURL := downloadURL
	if resolvedInternalURL := resolveOnlyOfficeInternalURL(h.config.OnlyOffice.APIJSURL, h.config.OnlyOffice.InternalURL); resolvedInternalURL != "" {
		if externalBase := onlyOfficeAPIBaseURL(h.config.OnlyOffice.APIJSURL); externalBase != "" {
			internalURL = strings.Replace(internalURL, externalBase, resolvedInternalURL, 1)
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

	fsHelper := NewFSHelper(h.db)
	filename := path.Base(filePath)
	h.reconcileStaleOnlyOfficePendingBlocks(orgID)

	// If library is encrypted, encrypt the content before storage
	if encrypted {
		// Encrypt the content using Seafile block encryption format
		originalSize := len(content)
		encryptedContent, err := encryptOnlyOfficeContent(userID, repoID, content)
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
	rollbackID := uuid.NewString()
	blockMetadataRegistered := false

	blockStore, storageClass, err := h.resolveLibraryBlockStore(orgID, repoID)
	if err != nil || blockStore == nil {
		return fmt.Errorf("failed to resolve block storage: %w", err)
	}

	// Insert the pending-cleanup row BEFORE the S3 PUT. If the server crashes
	// at any point between here and a successful publish, the reconciler can
	// find this row by org and either roll back the materialized block (after
	// onlyOfficePendingBlockStaleAfter) or detect that the publish commit is
	// reachable from HEAD and drop the row. Recording the block id ahead of
	// time means the reconciler always has the data it needs to decrement
	// refcounts, even on a crash before IncrementOrCreateBlock.
	if err := h.saveOnlyOfficePendingBlock(orgID, onlyOfficePendingBlock{
		OperationID:     rollbackID,
		RepoID:          repoID,
		FilePath:        filePath,
		InternalBlockID: internalBlockID,
		ExternalBlockID: externalBlockID,
		StorageClass:    storageClass,
	}); err != nil {
		return fmt.Errorf("failed to persist pending OnlyOffice block cleanup: %w", err)
	}

	var storageKey string
	if err := RetryUploadedBlockMaterialization("OnlyOffice", internalBlockID, func() error {
		probe, probeErr := probeUploadedBlockReuseFn(h.db, orgID, internalBlockID)
		if probeErr == nil {
			switch probe.Decision {
			case db.BlockReuseReusable:
				var ensureErr error
				storageKey, ensureErr = EnsureReusableBlockPresent(ctx, internalBlockID, probe, content, h.storageManager, blockStore, storageClass, orgID)
				return ensureErr
			case db.BlockReuseNeedsPut:
				var putErr error
				storageKey, putErr = putUploadedBlockAutoDirectFn(ctx, blockStore, internalBlockID, content)
				if putErr != nil {
					return fmt.Errorf("failed to store block: %w", putErr)
				}
				return nil
			case db.BlockReuseBlockedByGC:
				return ErrBlockDeleteInProgress
			}
		} else {
			log.Printf("OnlyOffice: block reuse probe unavailable for block %s; falling back to legacy Exists+PUT path: %v", internalBlockID[:16], probeErr)
		}
		blockKey, putErr := blockStore.PutBlockData(ctx, &storage.BlockData{
			Hash: internalBlockID,
			Data: content,
			Size: int64(len(content)),
		})
		if putErr != nil {
			return fmt.Errorf("failed to store block: %w", putErr)
		}
		storageKey = blockKey
		return nil
	}, func() error {
		// Materialize block metadata/provisional ref first and then the sync mapping.
		// Keep the pending-cleanup row on mapping failure so the reconciler can finish
		// cleanup even if the immediate rollback path was only partially successful.
		return RegisterUploadedBlockAndMapping(h.db, orgID, repoID, internalBlockID, rollbackID, len(content), storageClass, storageKey, externalBlockID)
	}, nil, nil); err != nil {
		if errors.Is(err, ErrBlockMappingWriteFailed) {
			log.Printf("OnlyOffice: CRITICAL - failed to create block mapping org=%s ext=%s int=%s: %v", orgID, externalBlockID[:16], internalBlockID[:16], err)
			return fmt.Errorf("failed to create block mapping: %w", err)
		}
		if deleteErr := h.deleteOnlyOfficePendingBlock(orgID, rollbackID); deleteErr != nil {
			log.Printf("OnlyOffice: failed to clear pending block cleanup %s after block-metadata failure: %v", rollbackID, deleteErr)
		}
		return fmt.Errorf("failed to store block metadata: %w", err)
	}
	log.Printf("OnlyOffice: Created block mapping: %s → %s", externalBlockID[:16], internalBlockID[:16])
	blockMetadataRegistered = true

	storageDeltaBytes, storageDeltaFiles, newCommitID, err := h.publishEditedDocumentMetadata(fsHelper, orgID, repoID, filePath, filename, userID, originalFileSize, externalBlockID, rollbackID)
	if err != nil {
		if shouldRollbackOnlyOfficeMaterializedBlock(blockMetadataRegistered, err) {
			zeroRefBlocks := fsHelper.ReleaseUploadReferences(orgID, repoID, rollbackID, []string{internalBlockID})
			enqueueZeroRefBlocks(h.db, orgID, repoID, zeroRefBlocks)
			if deleteErr := h.deleteOnlyOfficePendingBlock(orgID, rollbackID); deleteErr != nil {
				log.Printf("OnlyOffice: failed to clear pending block cleanup %s after rollback: %v", rollbackID, deleteErr)
			}
			log.Printf("OnlyOffice: rolled back materialized block %s after metadata publish failure: %v", internalBlockID[:16], err)
		}
		return err
	}

	h.finalizeSuccessfulOnlyOfficeEdit(orgID, repoID, userID, rollbackID, internalBlockID, filePath, externalBlockID, newCommitID, storageDeltaBytes, storageDeltaFiles)
	return nil
}

func (h *OnlyOfficeHandler) finalizeSuccessfulOnlyOfficeEdit(orgID, repoID, userID, rollbackID, internalBlockID, filePath, externalBlockID, newCommitID string, storageDeltaBytes, storageDeltaFiles int64) {
	onlyOfficeReleaseUploadedBlockRefsFn(h.db, orgID, repoID, rollbackID, []string{internalBlockID})

	if err := onlyOfficeDeletePendingBlockFn(h, orgID, rollbackID); err != nil {
		log.Printf("OnlyOffice: failed to clear pending block cleanup %s after publish success: %v", rollbackID, err)
	}

	if err := onlyOfficeAdjustStorageCountersFn(h.db, orgID, userID, repoID, storageDeltaBytes, storageDeltaFiles); err != nil {
		log.Printf("OnlyOffice: failed to apply storage counter delta for %s: %v", filePath, err)
	}

	log.Printf("OnlyOffice: saved document %s with block %s (internal: %s), new commit %s", filePath, externalBlockID[:16], internalBlockID[:16], newCommitID)
}

func (h *OnlyOfficeHandler) publishEditedDocumentMetadata(fsHelper *FSHelper, orgID, repoID, filePath, filename, userID string, originalFileSize int64, externalBlockID, pendingOperationID string) (int64, int64, string, error) {
	var storageDeltaBytes int64
	var storageDeltaFiles int64
	var newCommitID string

	err := retryLibraryHeadMutation("OnlyOffice", func() error {
		result, snapshot, err := fsHelper.TraverseToPathAtHead(repoID, filePath)
		if err != nil {
			return fmt.Errorf("failed to traverse to path: %w", err)
		}

		var existingEntry *FSEntry
		if existing := FindEntryInList(result.Entries, filename); existing != nil {
			ent := *existing
			existingEntry = &ent
		}

		currentDeltaBytes, currentDeltaFiles := onlyOfficeStorageDelta(existingEntry, originalFileSize)
		if currentDeltaBytes > 0 {
			if checker := traffic.GetChecker(); checker != nil {
				if st, _ := checker.CheckStorageQuota(orgID, userID, currentDeltaBytes); !st.Allowed {
					return ErrStorageQuotaExceeded
				}
			}
		}

		now := time.Now()
		pendingFile, err := fsHelper.prepareFileFSObjectForPublish(repoID, filename, originalFileSize, []string{externalBlockID})
		if err != nil {
			return fmt.Errorf("failed to create file fs_object: %w", err)
		}
		cleanupPendingFilePublish := func() {
			if cleanupErr := CleanupFailedPublishAttempt(h.db, orgID, repoID, "", "", []*pendingPublishedFile{pendingFile}); cleanupErr != nil {
				log.Printf("OnlyOffice: failed to clean up pending fs_object %s before commit publish: %v", pendingFile.fsID, cleanupErr)
			}
		}

		updatedEntries := make([]FSEntry, 0, len(result.Entries))
		fileUpdated := false
		for _, entry := range result.Entries {
			if entry.Name == filename {
				entry.ID = pendingFile.fsID
				entry.Size = originalFileSize
				entry.MTime = now.Unix()
				entry.Modifier = modifierIdentityForUser(userID)
				fileUpdated = true
			}
			updatedEntries = append(updatedEntries, entry)
		}

		if !fileUpdated {
			updatedEntries = append(updatedEntries, FSEntry{
				ID:       pendingFile.fsID,
				Name:     filename,
				Mode:     ModeFile,
				MTime:    now.Unix(),
				Size:     originalFileSize,
				Modifier: modifierIdentityForUser(userID),
			})
		}

		newParentFSID, err := fsHelper.CreateDirectoryFSObject(repoID, updatedEntries)
		if err != nil {
			cleanupPendingFilePublish()
			return fmt.Errorf("failed to create parent fs_object: %w", err)
		}

		newRootFSID, err := fsHelper.RebuildPathToRoot(repoID, result, newParentFSID)
		if err != nil {
			cleanupPendingFilePublish()
			return fmt.Errorf("failed to rebuild path: %w", err)
		}

		commitDesc := fmt.Sprintf("Modified \"%s\" via OnlyOffice", filename)
		commitCreatedAt := time.Now().UTC()
		commitID := buildCommitID(repoID, newRootFSID, commitDesc, commitCreatedAt)
		if err := fsHelper.stagePendingPublishedFiles(orgID, repoID, commitID, []*pendingPublishedFile{pendingFile}); err != nil {
			if cleanupErr := cleanupOnlyOfficeFailedPublishAttempt(h.db, orgID, repoID, commitID, []*pendingPublishedFile{pendingFile}); cleanupErr != nil {
				return errors.Join(
					fmt.Errorf("failed to stage publish-attempt block references for commit %s: %w", commitID, err),
					fmt.Errorf("cleanup failed publish commit %s: %w", commitID, cleanupErr),
				)
			}
			return fmt.Errorf("failed to stage publish-attempt block references for commit %s: %w", commitID, err)
		}
		if err := queuePendingPublishedFileRepairs(h.db, orgID, repoID, commitID, []*pendingPublishedFile{pendingFile}); err != nil {
			cleanupErr := cleanupOnlyOfficeFailedPublishAttempt(h.db, orgID, repoID, commitID, []*pendingPublishedFile{pendingFile})
			return errors.Join(
				fmt.Errorf("failed to queue durable publish repair for commit %s: %w", commitID, err),
				cleanupErr,
			)
		}
		if err := fsHelper.insertCommit(repoID, commitID, userID, newRootFSID, snapshot.HeadCommitID, commitDesc, commitCreatedAt); err != nil {
			cleanupErr := cleanupOnlyOfficeFailedPublishAttempt(h.db, orgID, repoID, commitID, []*pendingPublishedFile{pendingFile})
			clearErr := clearPendingPublishedFileRepairs(h.db, orgID, repoID, commitID, []*pendingPublishedFile{pendingFile})
			return errors.Join(
				fmt.Errorf("failed to create commit: %w", err),
				cleanupErr,
				clearErr,
			)
		}

		if err := h.updateOnlyOfficePendingBlockCommitID(orgID, pendingOperationID, commitID); err != nil {
			if cleanupErr := cleanupOnlyOfficeFailedPublishAttempt(h.db, orgID, repoID, commitID, []*pendingPublishedFile{pendingFile}); cleanupErr != nil {
				return errors.Join(
					fmt.Errorf("failed to persist OnlyOffice pending commit id: %w", err),
					fmt.Errorf("clean up publish attempt %s: %w", commitID, cleanupErr),
				)
			}
			return fmt.Errorf("failed to persist OnlyOffice pending commit id: %w", err)
		}

		if err := fsHelper.UpdateLibraryHeadFromSnapshot(snapshot, repoID, commitID, snapshot.HeadCommitID); err != nil {
			if errors.Is(err, ErrLibraryHeadConflict) {
				if cleanupErr := cleanupOnlyOfficeFailedPublishAttempt(h.db, orgID, repoID, commitID, []*pendingPublishedFile{pendingFile}); cleanupErr != nil {
					return fmt.Errorf("failed to clean up conflict publish attempt %s: %w", commitID, cleanupErr)
				}
			}
			return fmt.Errorf("failed to update library head: %w", err)
		}
		if ownerErr := clearPendingPublishedFileOwnersFn(h.db, repoID, []*pendingPublishedFile{pendingFile}); ownerErr != nil {
			log.Printf("OnlyOffice: published repo=%s commit=%s but failed to clear pending fs_object owners: %v", repoID, commitID, ownerErr)
		}
		if err := fsHelper.promotePendingPublishedFiles(orgID, repoID, commitID, []*pendingPublishedFile{pendingFile}); err != nil {
			log.Printf("OnlyOffice: WARNING: head updated for repo=%s commit=%s but failed to promote block references for fs_object %s: %v", repoID, commitID, pendingFile.fsID, err)
			schedulePendingPublishedFileRepairs(h.db, orgID, repoID, commitID, []*pendingPublishedFile{pendingFile}, "OnlyOffice")
		} else if clearErr := clearPendingPublishedFileRepairs(h.db, orgID, repoID, commitID, []*pendingPublishedFile{pendingFile}); clearErr != nil {
			log.Printf("OnlyOffice: published repo=%s commit=%s but failed to clear queued publish repair for fs_object %s: %v", repoID, commitID, pendingFile.fsID, clearErr)
		}

		storageDeltaBytes = currentDeltaBytes
		storageDeltaFiles = currentDeltaFiles
		newCommitID = commitID
		log.Printf("OnlyOffice: published edited document %s via snapshot head %s", filePath, snapshot.HeadCommitID)
		return nil
	})
	if err != nil {
		return 0, 0, "", err
	}

	return storageDeltaBytes, storageDeltaFiles, newCommitID, nil
}

// generateFSID creates a unique FS object ID (SHA-1 hash of content)
func generateFSID(content []byte) string {
	hash := sha1.Sum(content)
	return hex.EncodeToString(hash[:])
}
