package v2

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/crypto"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/Sesame-Disk/sesamefs/internal/models"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// LibraryTokenCreator mints the credentials the library surface needs. It
// carries both constructors because it serves two callers: library creation
// wants a sync token, and the v2 share-link zip task wants a path-scoped
// download token (see zipTokenCreator in files.go). Narrowing this to
// CreateSyncToken alone would break the zip task.
type LibraryTokenCreator interface {
	CreateDownloadToken(orgID, repoID, path, userID string) (string, error)
	CreateSyncToken(orgID, repoID, userID string) (string, error)
}

// apiPermission converts internal permission levels to Seafile-compatible API values.
// Seafile frontend expects "rw" for libraries where the user can write (including owner).
// This ensures copy/move dialogs correctly show all writable libraries.
func apiPermission(perm middleware.LibraryPermission) string {
	if perm == middleware.PermissionOwner || perm == middleware.PermissionRW {
		return "rw"
	}
	return string(perm) // "r" or other values pass through unchanged
}

// LibraryGCEnqueuer is the interface for enqueuing library contents for garbage collection.
type LibraryGCEnqueuer interface {
	// EnqueueLibraryCascade immediately queues the durable library cascade for a permanently
	// deleted library, deduplicated against scanner Phase 13 (same deletedAt identity), so
	// reclamation starts promptly instead of after up to a full ScanInterval. Best-effort:
	// the durable purge_requested_at marker recovers it if this enqueue is lost.
	EnqueueLibraryCascade(orgID, libraryID, blockRepresentationID, storageClass string, deletedAt time.Time)
}

// LibraryHandler handles library-related API requests
type LibraryHandler struct {
	db             *db.DB
	config         *config.Config
	tokenCreator   LibraryTokenCreator
	permMiddleware *middleware.PermissionMiddleware
	gcEnqueuer     LibraryGCEnqueuer
}

// SetGCEnqueuer sets the GC enqueuer for library deletion cleanup.
func (h *LibraryHandler) SetGCEnqueuer(enqueuer LibraryGCEnqueuer) {
	h.gcEnqueuer = enqueuer
}

// resolveOwnerEmail looks up the real email for a user by org_id and user_id.
// Falls back to userID@sesamefs.local if the user record is not found (e.g. deleted
// user, incomplete migration). This fallback keeps responses structurally valid for
// the Seafile desktop client while making the anomaly visible in the address.
func (h *LibraryHandler) resolveOwnerEmail(orgID, userID string) string {
	var email string
	if err := h.db.Session().Query(`
		SELECT email FROM users WHERE org_id = ? AND user_id = ?
	`, orgID, userID).Scan(&email); err != nil || email == "" {
		return userID + "@sesamefs.local"
	}
	return email
}

func (h *LibraryHandler) ownerHasActiveLibraryNamed(orgID, ownerID, libraryName string) (bool, error) {
	// Read the owner's libraries from the libraries_by_owner projection (single
	// (org_id, owner_id) partition) instead of scanning the whole org partition
	// of the canonical libraries table.
	iter := h.db.Session().Query(`
		SELECT name, deleted_at FROM libraries_by_owner WHERE org_id = ? AND owner_id = ?
	`, orgID, ownerID).Iter()

	var existingName string
	var existingDeletedAt time.Time
	for iter.Scan(&existingName, &existingDeletedAt) {
		if !existingDeletedAt.IsZero() {
			existingDeletedAt = time.Time{}
			continue
		}
		if existingName == libraryName {
			if err := iter.Close(); err != nil {
				return false, err
			}
			return true, nil
		}
		existingDeletedAt = time.Time{}
	}
	if err := iter.Close(); err != nil {
		return false, err
	}
	return false, nil
}

// RegisterLibraryRoutes registers library routes
func RegisterLibraryRoutes(rg *gin.RouterGroup, database *db.DB, cfg *config.Config) {
	RegisterLibraryRoutesWithToken(rg, database, cfg, nil)
}

// RegisterLibraryRoutesWithToken registers library routes with token creator
func RegisterLibraryRoutesWithToken(rg *gin.RouterGroup, database *db.DB, cfg *config.Config, tokenCreator LibraryTokenCreator) {
	permMiddleware := middleware.NewPermissionMiddleware(database)
	h := &LibraryHandler{db: database, config: cfg, tokenCreator: tokenCreator, permMiddleware: permMiddleware, gcEnqueuer: getLibraryEnqueuer()}
	sh := NewFileShareHandler(database, permMiddleware)

	repos := rg.Group("/repos")
	{
		repos.GET("", h.ListLibraries)
		repos.POST("", h.CreateLibrary)
		repos.GET("/:repo_id", h.GetLibrary)
		repos.GET("/:repo_id/", h.GetLibrary)
		repos.PUT("/:repo_id", h.UpdateLibrary)
		repos.PUT("/:repo_id/", h.UpdateLibrary)
		repos.POST("/:repo_id", h.LibraryOperation) // handles op=rename
		repos.POST("/:repo_id/", h.LibraryOperation)
		repos.DELETE("/:repo_id", h.DeleteLibrary)
		repos.DELETE("/:repo_id/", h.DeleteLibrary)
		repos.POST("/:repo_id/storage-class", h.ChangeStorageClass)

		// File/folder sharing to users and groups (seafile-js uses /api2/ prefix)
		repos.GET("/:repo_id/dir/shared_items", sh.ListSharedItems)
		repos.GET("/:repo_id/dir/shared_items/", sh.ListSharedItems)
		repos.PUT("/:repo_id/dir/shared_items", sh.CreateShare)
		repos.PUT("/:repo_id/dir/shared_items/", sh.CreateShare)
		repos.POST("/:repo_id/dir/shared_items", sh.UpdateSharePermission)
		repos.POST("/:repo_id/dir/shared_items/", sh.UpdateSharePermission)
		repos.DELETE("/:repo_id/dir/shared_items", sh.DeleteShare)
		repos.DELETE("/:repo_id/dir/shared_items/", sh.DeleteShare)
	}
}

// RegisterV21LibraryRoutes registers v2.1 library routes with Seahub-compatible response format
func RegisterV21LibraryRoutes(rg *gin.RouterGroup, database *db.DB, cfg *config.Config, tokenCreator LibraryTokenCreator, s3Store *storage.S3Store, storageManager *storage.Manager, serverURL string) {
	permMiddleware := middleware.NewPermissionMiddleware(database)
	h := &LibraryHandler{db: database, config: cfg, tokenCreator: tokenCreator, permMiddleware: permMiddleware, gcEnqueuer: getLibraryEnqueuer()}
	fh := &FileHandler{db: database, config: cfg, serverURL: serverURL, permMiddleware: permMiddleware, gcEnqueuer: getBlockEnqueuer(), zipTokenCreator: tokenCreator, storageManager: storageManager}
	eh := NewEncryptionHandler(database)
	sh := NewFileShareHandler(database, permMiddleware)

	// Pass raw storage for org-scoped Office file template creation.
	if s3Store != nil {
		fh.storage = s3Store
	}

	repos := rg.Group("/repos")
	{
		repos.GET("", h.ListLibrariesV21)
		repos.POST("", h.CreateLibrary)  // Create new library
		repos.POST("/", h.CreateLibrary) // With trailing slash
		repos.GET("/:repo_id", h.GetLibraryV21)
		repos.DELETE("/:repo_id", h.DeleteLibrary)
		repos.DELETE("/:repo_id/", h.DeleteLibrary)
		repos.GET("/:repo_id/dir", fh.ListDirectoryV21)
		repos.GET("/:repo_id/dir/", fh.ListDirectoryV21)

		// Encrypted library password endpoints
		repos.POST("/:repo_id/set-password", eh.SetPassword)
		repos.POST("/:repo_id/set-password/", eh.SetPassword)
		repos.PUT("/:repo_id/set-password", eh.ChangePassword)
		repos.PUT("/:repo_id/set-password/", eh.ChangePassword)

		// File operations (CRUD)
		repos.GET("/:repo_id/file", fh.GetFileInfo)
		repos.GET("/:repo_id/file/", fh.GetFileInfo)
		repos.DELETE("/:repo_id/file", fh.DeleteFile)
		repos.DELETE("/:repo_id/file/", fh.DeleteFile)
		repos.POST("/:repo_id/file", fh.FileOperation)  // rename, create
		repos.POST("/:repo_id/file/", fh.FileOperation) // rename, create
		repos.PUT("/:repo_id/file", fh.LockFile)        // lock, unlock
		repos.PUT("/:repo_id/file/", fh.LockFile)       // lock, unlock
		repos.GET("/:repo_id/file/detail", fh.GetFileDetail)
		repos.GET("/:repo_id/file/detail/", fh.GetFileDetail)

		// Directory operations
		repos.DELETE("/:repo_id/dir", fh.DeleteDirectory)
		repos.DELETE("/:repo_id/dir/", fh.DeleteDirectory)
		repos.POST("/:repo_id/dir", fh.DirectoryOperation)  // mkdir, rename
		repos.POST("/:repo_id/dir/", fh.DirectoryOperation) // mkdir, rename
		repos.GET("/:repo_id/dir/detail", fh.GetDirDetail)
		repos.GET("/:repo_id/dir/detail/", fh.GetDirDetail)

		// Move/Copy operations
		repos.POST("/:repo_id/file/move", fh.MoveFile)
		repos.POST("/:repo_id/file/move/", fh.MoveFile)
		repos.POST("/:repo_id/file/copy", fh.CopyFile)
		repos.POST("/:repo_id/file/copy/", fh.CopyFile)

		// Resumable upload support
		repos.GET("/:repo_id/file-uploaded-bytes", fh.GetFileUploadedBytes)
		repos.GET("/:repo_id/file-uploaded-bytes/", fh.GetFileUploadedBytes)

		// Share info endpoint (stub - returns empty shares)
		repos.GET("/:repo_id/share-info", h.GetRepoFolderShareInfo)
		repos.GET("/:repo_id/share-info/", h.GetRepoFolderShareInfo)

		// Custom share permissions
		repos.GET("/:repo_id/custom-share-permissions", sh.ListCustomSharePermissions)
		repos.GET("/:repo_id/custom-share-permissions/", sh.ListCustomSharePermissions)
		repos.POST("/:repo_id/custom-share-permissions", sh.CreateCustomSharePermission)
		repos.POST("/:repo_id/custom-share-permissions/", sh.CreateCustomSharePermission)
		repos.GET("/:repo_id/custom-share-permissions/:perm_id", sh.GetCustomSharePermission)
		repos.GET("/:repo_id/custom-share-permissions/:perm_id/", sh.GetCustomSharePermission)
		repos.PUT("/:repo_id/custom-share-permissions/:perm_id", sh.UpdateCustomSharePermission)
		repos.PUT("/:repo_id/custom-share-permissions/:perm_id/", sh.UpdateCustomSharePermission)
		repos.DELETE("/:repo_id/custom-share-permissions/:perm_id", sh.DeleteCustomSharePermission)
		repos.DELETE("/:repo_id/custom-share-permissions/:perm_id/", sh.DeleteCustomSharePermission)

		// File/folder sharing to users and groups
		repos.GET("/:repo_id/dir/shared_items", sh.ListSharedItems)
		repos.GET("/:repo_id/dir/shared_items/", sh.ListSharedItems)
		repos.PUT("/:repo_id/dir/shared_items", sh.CreateShare)
		repos.PUT("/:repo_id/dir/shared_items/", sh.CreateShare)
		repos.POST("/:repo_id/dir/shared_items", sh.UpdateSharePermission)
		repos.POST("/:repo_id/dir/shared_items/", sh.UpdateSharePermission)
		repos.DELETE("/:repo_id/dir/shared_items", sh.DeleteShare)
		repos.DELETE("/:repo_id/dir/shared_items/", sh.DeleteShare)

		// Zip download task - creates a download token for ZIP generation
		repos.POST("/:repo_id/zip-task", fh.CreateZipTask)
		repos.POST("/:repo_id/zip-task/", fh.CreateZipTask)
	}

	// File/folder trash (recycle bin) routes
	RegisterTrashRoutes(rg, database)

	// Deleted libraries (library recycle bin) routes
	RegisterDeletedLibraryRoutes(rg, database, h)
}

// ListLibraries returns all libraries for the authenticated user
// This endpoint uses the api2 format expected by Seafile desktop client
// (id, name, mtime) rather than the v2.1 web UI format (repo_id, repo_name, last_modified)
func (h *LibraryHandler) ListLibraries(c *gin.Context) {
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")
	if orgID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing org_id"})
		return
	}

	if _, err := uuid.Parse(orgID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid org_id"})
		return
	}

	// ========================================================================
	// PERMISSION FILTER: Only return libraries user has access to
	// ========================================================================
	// Get all libraries user has access to (owned + shared)
	accessibleLibs, err := h.permMiddleware.GetUserLibraries(orgID, userID)
	if err != nil {
		log.Printf("[ListLibraries] Failed to get user libraries: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get libraries"})
		return
	}

	// Build map of accessible library IDs for quick lookup
	accessibleMap := make(map[string]middleware.LibraryPermission)
	for _, lib := range accessibleLibs {
		accessibleMap[lib.LibraryID.String()] = lib.Permission
	}

	// If user has no accessible libraries, return empty array
	if len(accessibleMap) == 0 {
		c.JSON(http.StatusOK, []gin.H{})
		return
	}

	// Query libraries from database (use string for UUID binding)
	// Include encryption fields (enc_version, magic, random_key, salt) for encrypted libraries
	iter := h.db.Session().Query(`
		SELECT library_id, owner_id, name, description, encrypted,
			   storage_class, size_bytes, file_count, head_commit_id, created_at, updated_at,
			   enc_version, magic, random_key, salt, deleted_at
		FROM libraries WHERE org_id = ?
	`, orgID).Iter()

	var libraries []gin.H
	var libID, ownerID string
	var name, description, storageClass string
	var headCommitID string
	var encrypted bool
	var sizeBytes, fileCount int64
	var createdAt, updatedAt time.Time
	var encVersion int
	var magic, randomKey, salt string
	var deletedAt time.Time

	for iter.Scan(
		&libID, &ownerID, &name, &description,
		&encrypted, &storageClass, &sizeBytes,
		&fileCount, &headCommitID, &createdAt, &updatedAt,
		&encVersion, &magic, &randomKey, &salt, &deletedAt,
	) {
		// Skip soft-deleted libraries
		if !deletedAt.IsZero() {
			continue
		}

		// ========================================================================
		// PERMISSION FILTER: Skip libraries user doesn't have access to
		// ========================================================================
		permission, hasAccess := accessibleMap[libID]
		if !hasAccess {
			continue // Skip this library - user doesn't have access
		}

		ownerEmail := h.resolveOwnerEmail(orgID, ownerID)

		// Convert encrypted bool to int (0/1) for Seafile frontend compatibility
		encryptedInt := 0
		if encrypted {
			encryptedInt = 1
		}

		// Seafile desktop client expects these specific field names:
		// - id (not repo_id)
		// - name (not repo_name)
		// - mtime (not last_modified)
		// - owner (not owner_email)
		// - desc (not description)
		//
		// CRITICAL field formats (verified against stock Seafile):
		// - root: empty string "" (not "0000...000")
		// - salt: always present (empty string "" for unencrypted)
		// - modifier_email, modifier_contact_email, modifier_name: required by desktop client
		// - encrypted: integer 0 or 1 (not boolean)
		lib := gin.H{
			"type":                   "repo",
			"id":                     libID,
			"name":                   name,
			"desc":                   description,
			"owner":                  ownerEmail,
			"owner_name":             strings.Split(ownerEmail, "@")[0],
			"owner_contact_email":    ownerEmail,
			"modifier_email":         ownerEmail, // Desktop client requires these
			"modifier_contact_email": ownerEmail,
			"modifier_name":          strings.Split(ownerEmail, "@")[0],
			"mtime":                  updatedAt.Unix(),
			"mtime_relative":         "", // Optional human-readable time
			"encrypted":              encryptedInt,
			"permission":             apiPermission(permission), // Use Seafile-compatible permission level
			"virtual":                false,
			"root":                   "", // CRITICAL: empty string (stock Seafile format)
			"head_commit_id":         headCommitID,
			"version":                1,
			"size":                   sizeBytes,
			"size_formatted":         formatSize(sizeBytes),
			"salt":                   "", // CRITICAL: always present (stock Seafile format)
			"file_count":             fileCount,
			"storage_id":             storageClass,
			"storage_name":           h.displayStorageName(storageClass),
		}

		// Add encryption fields for encrypted libraries
		// Client needs these to prompt for password
		if encrypted {
			// Return enc_version 2 for Seafile client compatibility (we store 12 for dual-mode)
			lib["enc_version"] = 2
			lib["magic"] = magic
			lib["random_key"] = randomKey
			// Override salt with actual value for encrypted libraries
			if salt != "" {
				lib["salt"] = salt
			}
		}

		libraries = append(libraries, lib)
	}

	if err := iter.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list libraries", "details": err.Error()})
		return
	}

	// Return empty array instead of null
	if libraries == nil {
		libraries = []gin.H{}
	}

	c.JSON(http.StatusOK, libraries)
}

// formatSize returns a human-readable size string using decimal (SI) units
// to match the frontend's bytesToSize() which also uses base-1000.
// Seafile desktop client compatibility uses FormatSizeSeafile (base-1024) instead.
func formatSize(bytes int64) string {
	const unit = 1000
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func (h *LibraryHandler) isKnownStorageClass(class string) bool {
	if h == nil {
		return false
	}
	return isKnownStorageClass(h.config, class)
}

func (h *LibraryHandler) resolveEndpointRegion(hostname string) string {
	if h == nil {
		return "default"
	}
	return resolveEndpointRegion(h.config, hostname)
}

func (h *LibraryHandler) resolveRequestedStorageClass(hostname, requestedClass string) (string, error) {
	if h == nil {
		return "", fmt.Errorf("no valid storage class configured")
	}
	return resolveCreateStorageClass(h.config, defaultOrgStoragePolicy(), hostname, requestedClass)
}

func (h *LibraryHandler) displayStorageName(storageClass string) string {
	if h == nil {
		return strings.TrimSpace(storageClass)
	}
	return displayStorageNameForConfig(h.config, storageClass)
}

// CreateLibraryRequest represents the request body for creating a library
type CreateLibraryRequest struct {
	Name         string `json:"name" form:"name"`
	RepoName     string `json:"repo_name" form:"repo_name"` // Seafile v2.1 API uses repo_name
	Description  string `json:"description" form:"desc"`    // Seafile uses "desc" in form
	Encrypted    bool   `json:"encrypted" form:"encrypted"`
	Password     string `json:"passwd,omitempty" form:"passwd"` // Seafile uses "passwd" everywhere
	StorageID    string `json:"storage_id,omitempty" form:"storage_id"`
	StorageClass string `json:"storage_class,omitempty" form:"storage_class"`
}

// CreateLibrary creates a new library
func (h *LibraryHandler) CreateLibrary(c *gin.Context) {
	var req CreateLibraryRequest

	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// password implies encrypted (mirrors original behavior)
	if req.Password != "" {
		req.Encrypted = true
	}

	// Support both "name" (v2) and "repo_name" (v2.1) fields
	if req.Name == "" && req.RepoName != "" {
		req.Name = req.RepoName
	}

	// Validate required field
	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name or repo_name is required"})
		return
	}

	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	log.Printf("[CreateLibrary] Creating library: name=%q, encrypted=%v, orgID=%q, userID=%q", req.Name, req.Encrypted, orgID, userID)

	// ========================================================================
	// PERMISSION CHECK: Require at least "user" role to create libraries
	// ========================================================================
	// Readonly and guest users cannot create libraries
	userRole, err := h.permMiddleware.GetUserOrgRole(orgID, userID)
	if err != nil {
		log.Printf("[CreateLibrary] Failed to check user role: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check permissions"})
		return
	}

	// Check role hierarchy: user role must be at least "user" (not readonly or guest)
	if !middleware.HasRequiredOrgRole(userRole, middleware.RoleUser) {
		log.Printf("[CreateLibrary] Permission denied: user has role %q, requires at least 'user'", userRole)
		c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions: creating libraries requires 'user' role or higher"})
		return
	}
	log.Printf("[CreateLibrary] Permission granted: user has role %q", userRole)

	// ENFORCEMENT CHECK: feature flag + numeric limit
	if h.config != nil {
		enforcement := GetOrgEnforcement(h.db, orgID, h.config)
		if !enforcement.Profile.Features.CanAddRepo {
			c.JSON(http.StatusForbidden, gin.H{
				"error":            "Library creation is not available on your plan",
				"upgrade_required": true,
			})
			return
		}
		if enforcement.Profile.Limits.MaxLibraries > 0 {
			count, err := CountActiveLibraries(h.db, orgID)
			if err != nil {
				log.Printf("[CreateLibrary] Failed to count active libraries for org %q: %v", orgID, err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to validate library limit"})
				return
			}
			if count >= enforcement.Profile.Limits.MaxLibraries {
				c.JSON(http.StatusForbidden, gin.H{
					"error":   "Library limit reached",
					"limit":   enforcement.Profile.Limits.MaxLibraries,
					"current": count,
				})
				return
			}
		}
	}

	// Check if a library with this name already exists for this user via the
	// libraries_by_owner projection (the owner's single partition).
	hasDuplicate, err := h.ownerHasActiveLibraryNamed(orgID, userID, req.Name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check existing libraries"})
		return
	}
	if hasDuplicate {
		log.Printf("[CreateLibrary] Conflict: library with name %q already exists", req.Name)
		c.JSON(http.StatusConflict, gin.H{"error": "a library with this name already exists"})
		return
	}

	orgUUID, _ := uuid.Parse(orgID)
	userUUID, _ := uuid.Parse(userID)
	newLibID := uuid.New()
	requestedStorageClass := req.StorageID
	if requestedStorageClass == "" {
		requestedStorageClass = req.StorageClass
	}
	resolvedStorageClass, err := resolveCreateStorageClassForOrg(h.db, h.config, orgID, routingHostname(c, h.config), requestedStorageClass)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	now := time.Now()
	library := models.Library{
		LibraryID:      newLibID,
		OrgID:          orgUUID,
		OwnerID:        userUUID,
		Name:           req.Name,
		Description:    req.Description,
		Encrypted:      req.Encrypted,
		StorageClass:   resolvedStorageClass,
		SizeBytes:      0,
		FileCount:      0,
		VersionTTLDays: h.config.Versioning.DefaultTTLDays,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	// Create empty root directory fs_object
	// Seafile uses a specific format for empty directories - the fs_id is the SHA-1 hash
	// of the serialized directory content. For an empty dir, we use a well-known empty dir hash.
	emptyDirEntries := "[]"                                   // Empty JSON array for directory entries
	emptyDirData := fmt.Sprintf("%d\n%s", 1, emptyDirEntries) // version + entries
	emptyDirHash := sha1.Sum([]byte(emptyDirData))
	rootFSID := hex.EncodeToString(emptyDirHash[:])

	// Generate initial commit ID (SHA-1 hash of repo creation data)
	commitData := fmt.Sprintf("%s:%s:%d", newLibID.String(), req.Name, now.UnixNano())
	commitHash := sha1.Sum([]byte(commitData))
	headCommitID := hex.EncodeToString(commitHash[:])

	// Generate encryption params if library is encrypted
	var encParams *crypto.EncryptionParams
	if req.Encrypted {
		if req.Password == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "password is required for encrypted library"})
			return
		}
		var err error
		encParams, err = crypto.CreateEncryptedLibrary(req.Password, newLibID.String())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create encryption params"})
			return
		}
	}

	// Insert into database with head_commit_id and encryption params
	// Use batched writes to maintain consistency between libraries and libraries_by_id
	userEmail := c.GetString("user_email")
	if userEmail == "" {
		userEmail = h.resolveOwnerEmail(orgID, userID)
	}
	ownerName := strings.Split(userEmail, "@")[0]
	blockRepresentationID := db.NewLibraryBlockRepresentationID(newLibID.String(), library.Encrypted)
	batch := h.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		INSERT INTO fs_objects (library_id, fs_id, obj_type, obj_name, dir_entries, mtime)
		VALUES (?, ?, ?, ?, ?, ?)
	`, newLibID.String(), rootFSID, "dir", "", emptyDirEntries, now.Unix())

	if req.Encrypted && encParams != nil {
		batch.Query(`
			INSERT INTO libraries (
				org_id, library_id, owner_id, name, description, encrypted,
				block_representation_id,
				enc_version, salt, magic, random_key, magic_strong, random_key_strong,
				storage_class, size_bytes, file_count, version_ttl_days,
				head_commit_id, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, orgID, newLibID.String(), userID, library.Name,
			library.Description, library.Encrypted,
			blockRepresentationID,
			encParams.EncVersion, encParams.Salt, encParams.Magic, encParams.RandomKey,
			encParams.MagicStrong, encParams.RandomKeyStrong,
			library.StorageClass, library.SizeBytes, library.FileCount, library.VersionTTLDays,
			headCommitID, library.CreatedAt, library.UpdatedAt,
		)

		// Dual-write to lookup table
		batch.Query(`
			INSERT INTO libraries_by_id (
				library_id, org_id, owner_id, name, head_commit_id, encrypted, block_representation_id,
				enc_version, magic, random_key, salt, magic_strong, random_key_strong
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, newLibID.String(), orgID, userID, library.Name, headCommitID, library.Encrypted,
			blockRepresentationID,
			encParams.EncVersion, encParams.Magic, encParams.RandomKey, encParams.Salt,
			encParams.MagicStrong, encParams.RandomKeyStrong,
		)
	} else {
		batch.Query(`
			INSERT INTO libraries (
				org_id, library_id, owner_id, name, description, encrypted,
				block_representation_id,
				storage_class, size_bytes, file_count, version_ttl_days,
				head_commit_id, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, orgID, newLibID.String(), userID, library.Name,
			library.Description, library.Encrypted, blockRepresentationID, library.StorageClass,
			library.SizeBytes, library.FileCount, library.VersionTTLDays,
			headCommitID, library.CreatedAt, library.UpdatedAt,
		)

		// Dual-write to lookup table (unencrypted)
		batch.Query(`
			INSERT INTO libraries_by_id (
				library_id, org_id, owner_id, name, head_commit_id, encrypted, block_representation_id
			) VALUES (?, ?, ?, ?, ?, ?, ?)
		`, newLibID.String(), orgID, userID, library.Name, headCommitID, false, blockRepresentationID,
		)
	}
	if library.VersionTTLDays > 0 {
		db.AddUpsertLibraryPolicyQuery(batch, db.GCLibraryPolicyVersionTTL, orgID, newLibID.String(), library.VersionTTLDays, headCommitID, library.UpdatedAt)
	}

	batch.Query(`
		INSERT INTO commits (library_id, commit_id, root_fs_id, creator_id, description, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, newLibID.String(), headCommitID, rootFSID, userID, "Initial commit", now)
	addAdminLibraryReadModelRefreshQueries(batch, db.AdminLibraryProjectionRow{
		OrgID:        orgID,
		LibraryID:    newLibID.String(),
		OwnerID:      userID,
		OwnerEmail:   userEmail,
		OwnerName:    ownerName,
		Name:         library.Name,
		Encrypted:    library.Encrypted,
		StorageClass: library.StorageClass,
		SizeBytes:    library.SizeBytes,
		FileCount:    library.FileCount,
		CreatedAt:    library.CreatedAt,
		UpdatedAt:    library.UpdatedAt,
	}, nil)

	if err := batch.Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create library", "details": err.Error()})
		return
	}

	// Generate sync token if token creator is available
	syncToken := ""
	if h.tokenCreator != nil {
		token, err := h.tokenCreator.CreateSyncToken(orgID, newLibID.String(), userID)
		if err == nil {
			syncToken = token
		}
	}

	relayHost := effectiveHostname(c, h.config)

	// Return Seafile-compatible response (HTTP 200, not 201)
	// This format matches what Seafile returns and includes sync info
	response := gin.H{
		"relay_id":            relayHost,
		"relay_addr":          relayHost,
		"relay_port":          relayPortFromRequest(c, h.config),
		"email":               userEmail,
		"token":               syncToken,
		"repo_id":             newLibID.String(),
		"repo_name":           req.Name,
		"repo_desc":           req.Description,
		"repo_size":           0,
		"repo_size_formatted": formatSizeSeafile(0),
		"mtime":               now.Unix(),
		"mtime_relative":      formatRelativeTimeHTML(now),
		"encrypted":           false,
		"enc_version":         0,
		"salt":                "",
		"magic":               "",
		"random_key":          "",
		"storage_id":          library.StorageClass,
		"storage_name":        h.displayStorageName(library.StorageClass),
		"repo_version":        1,
		"head_commit_id":      headCommitID,
		"permission":          "rw", // Owner always has rw
	}

	// Set encrypted fields if library is encrypted
	// Translate enc_version for Seafile desktop client compatibility
	if req.Encrypted && encParams != nil {
		response["encrypted"] = 1 // Seafile uses 1 for encrypted (not true)
		// Translate enc_version 12 (dual-mode) to 2 for Seafile client
		clientEncVersion := encParams.EncVersion
		if clientEncVersion == 12 || clientEncVersion == 10 {
			clientEncVersion = 2
		}
		response["enc_version"] = clientEncVersion
		// CRITICAL: For Seafile v2, salt must be empty string (uses static hardcoded salt)
		// Don't expose internal Argon2id salt to Seafile clients
		response["salt"] = ""
		response["magic"] = encParams.Magic
		response["random_key"] = encParams.RandomKey
	}

	c.JSON(http.StatusOK, response)
}

// getLibraryPermissionFn is a seam over PermissionMiddleware.GetLibraryPermission
// so the GetLibrary/GetLibraryV21 handlers can be unit-tested without a live DB
// (the underlying lookup hits Cassandra). Mirrors the other *Fn seams in this package.
var getLibraryPermissionFn = func(pm *middleware.PermissionMiddleware, orgID, userID, repoID string) (middleware.LibraryPermission, error) {
	return pm.GetLibraryPermission(orgID, userID, repoID)
}

// GetLibrary returns a single library by ID
// This endpoint uses the api2 format expected by Seafile desktop client
func (h *LibraryHandler) GetLibrary(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	if _, err := uuid.Parse(repoID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid repo_id"})
		return
	}

	// ========================================================================
	// PERMISSION CHECK: User must have at least read access
	// ========================================================================
	var userPermission middleware.LibraryPermission

	// Check if authenticated via repo API token
	if isRepoToken, _ := c.Get("repo_api_token"); isRepoToken == true {
		tokenRepoID := c.GetString("repo_api_token_repo_id")
		tokenPerm := c.GetString("repo_api_token_permission")
		if tokenRepoID != repoID {
			c.JSON(http.StatusForbidden, gin.H{"error": "API token does not have access to this library"})
			return
		}
		if tokenPerm == "rw" {
			userPermission = middleware.PermissionRW
		} else {
			userPermission = middleware.PermissionR
		}
	} else {
		var err error
		userPermission, err = getLibraryPermissionFn(h.permMiddleware, orgID, userID, repoID)
		if err != nil {
			log.Printf("[GetLibrary] Failed to check permissions: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check permissions"})
			return
		}
	}

	if userPermission == middleware.PermissionNone {
		// GetLibraryPermission returns PermissionNone for both "library missing" and
		// "no access"; a missing/soft-deleted library must surface as 404, otherwise
		// the frontend renders a misleading "Permission denied / Leave Share" screen.
		if respondIfLibraryMissing(c, h.db.Session(), orgID, repoID) {
			return
		}
		log.Printf("[GetLibrary] Permission denied: user %q does not have access to library %q", userID, repoID)
		c.JSON(http.StatusForbidden, gin.H{"error": "you do not have access to this library"})
		return
	}

	var libID, ownerID string
	var name, description, storageClass string
	var headCommitID string
	var encrypted bool
	var encVersion int
	var salt, magic, randomKey string
	var sizeBytes, fileCount int64
	var versionTTLDays int
	var createdAt, updatedAt time.Time
	var deletedAt time.Time

	if err := h.db.Session().Query(`
		SELECT library_id, owner_id, name, description, encrypted,
			   enc_version, salt, magic, random_key,
			   storage_class, size_bytes, file_count, version_ttl_days,
			   head_commit_id, created_at, updated_at, deleted_at
		FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(
		&libID, &ownerID, &name, &description,
		&encrypted, &encVersion, &salt, &magic, &randomKey,
		&storageClass, &sizeBytes,
		&fileCount, &versionTTLDays, &headCommitID, &createdAt, &updatedAt,
		&deletedAt,
	); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	// Soft-deleted libraries should not be accessible
	if !deletedAt.IsZero() {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	ownerEmail := h.resolveOwnerEmail(orgID, ownerID)

	// Return api2 format for Seafile desktop client compatibility
	// CRITICAL: encrypted must be integer (0/1) not boolean for Seafile frontend compatibility
	encryptedInt := 0
	if encrypted {
		encryptedInt = 1
	}

	response := gin.H{
		"id":                  libID,
		"name":                name,
		"desc":                description,
		"owner":               ownerEmail,
		"owner_email":         ownerEmail, // Used by share dialog
		"owner_name":          strings.Split(ownerEmail, "@")[0],
		"owner_contact_email": ownerEmail,
		"mtime":               updatedAt.Unix(),
		"mtime_relative":      "",
		"encrypted":           encryptedInt,
		"permission":          apiPermission(userPermission),
		"virtual":             false,
		"root":                "0000000000000000000000000000000000000000",
		"head_commit_id":      headCommitID,
		"version":             1,
		"type":                "repo",
		"size":                sizeBytes,
		"size_formatted":      formatSize(sizeBytes),
		"file_count":          fileCount,
		"storage_id":          storageClass,
		"storage_name":        h.displayStorageName(storageClass),
	}

	// Add encryption fields if library is encrypted
	// Translate enc_version for Seafile desktop client compatibility
	if encrypted {
		clientEncVersion := encVersion
		if encVersion == 12 || encVersion == 10 {
			clientEncVersion = 2
		}
		response["enc_version"] = clientEncVersion
		response["salt"] = salt
		response["magic"] = magic
		response["random_key"] = randomKey
	}

	c.JSON(http.StatusOK, response)
}

// UpdateLibraryRequest represents the request body for updating a library
type UpdateLibraryRequest struct {
	Name           *string `json:"name,omitempty"`
	Description    *string `json:"description,omitempty"`
	VersionTTLDays *int    `json:"version_ttl_days,omitempty"`
}

// UpdateLibrary updates a library's properties
func (h *LibraryHandler) UpdateLibrary(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")

	var req UpdateLibraryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Build dynamic update query
	updates := []string{}
	values := []interface{}{}

	if req.Name != nil {
		updates = append(updates, "name = ?")
		values = append(values, *req.Name)
	}
	if req.Description != nil {
		updates = append(updates, "description = ?")
		values = append(values, *req.Description)
	}
	if req.VersionTTLDays != nil {
		if *req.VersionTTLDays < h.config.Versioning.MinTTLDays && *req.VersionTTLDays != 0 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "version_ttl_days must be 0 (forever) or >= min_ttl_days",
			})
			return
		}
		updates = append(updates, "version_ttl_days = ?")
		values = append(values, *req.VersionTTLDays)
	}

	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no updates provided"})
		return
	}

	libraryState, err := readLiveLibraryStateFn(h.db.Session(), orgID, repoID)
	if err != nil {
		writeLiveLibraryStateError(c, err)
		return
	}

	now := time.Now()
	updates = append(updates, "updated_at = ?")
	values = append(values, now)
	values = append(values, orgID, repoID) // Use strings for UUIDs

	query := "UPDATE libraries SET "
	for i, u := range updates {
		if i > 0 {
			query += ", "
		}
		query += u
	}
	query += " WHERE org_id = ? AND library_id = ?"

	batch := h.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(query, values...)
	if req.VersionTTLDays != nil {
		if *req.VersionTTLDays > 0 {
			db.AddUpsertLibraryPolicyQuery(batch, db.GCLibraryPolicyVersionTTL, orgID, repoID, *req.VersionTTLDays, libraryState.HeadCommitID, now)
		} else {
			db.AddDeleteLibraryPolicyQuery(batch, db.GCLibraryPolicyVersionTTL, orgID, repoID)
		}
	}
	if req.Name != nil {
		batch.Query(`
			UPDATE libraries_by_id SET name = ?
			WHERE library_id = ?
		`, *req.Name, repoID)
	}
	previousRow, err := db.ReadAdminLibraryProjectionRow(h.db.Session(), orgID, repoID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read library projection row"})
		return
	}
	projectionRow := previousRow
	if req.Name != nil {
		projectionRow.Name = *req.Name
	}
	projectionRow.UpdatedAt = now
	addAdminLibraryReadModelRefreshQueries(batch, projectionRow, &previousRow)

	if err := batch.Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update library"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// DeleteLibrary deletes a library
func (h *LibraryHandler) DeleteLibrary(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	// Validate inputs
	if orgID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing org_id"})
		return
	}

	if repoID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing repo_id"})
		return
	}

	if _, err := readLiveLibraryStateFn(h.db.Session(), orgID, repoID); err != nil {
		writeLiveLibraryStateError(c, err)
		return
	}

	// ========================================================================
	// PERMISSION CHECK: Require library ownership to delete
	// ========================================================================
	isOwner, err := h.permMiddleware.IsLibraryOwner(orgID, userID, repoID)
	if err != nil {
		log.Printf("[DeleteLibrary] Failed to check ownership: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check permissions"})
		return
	}

	if !isOwner {
		log.Printf("[DeleteLibrary] Permission denied: user %q is not owner of library %q", userID, repoID)
		c.JSON(http.StatusForbidden, gin.H{"error": "only library owner can delete the library"})
		return
	}
	log.Printf("[DeleteLibrary] Permission granted: user %q is owner of library %q", userID, repoID)

	// Soft-delete: set deleted_at + adjust storage counters.
	// ownerID = userID here because the permission check above ensures only the
	// owner can delete.
	if err := softDeleteLibrary(h.db, orgID, userID, userID, repoID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete library"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// LibraryOperation handles POST operations on a library based on 'op' query parameter
// Implements Seafile API: POST /api2/repos/:repo_id/?op=rename
func (h *LibraryHandler) LibraryOperation(c *gin.Context) {
	op := c.Query("op")

	switch op {
	case "rename":
		h.RenameLibrary(c)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported operation: " + op})
	}
}

// RenameLibraryRequest represents the request body for renaming a library
type RenameLibraryRequest struct {
	RepoName string `json:"repo_name" form:"repo_name"`
}

// RenameLibrary renames a library
// Implements Seafile API: POST /api2/repos/:repo_id/?op=rename
func (h *LibraryHandler) RenameLibrary(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")

	var req RenameLibraryRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.RepoName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "repo_name is required"})
		return
	}

	if _, err := readLiveLibraryStateFn(h.db.Session(), orgID, repoID); err != nil {
		writeLiveLibraryStateError(c, err)
		return
	}

	now := time.Now()
	previousRow, err := db.ReadAdminLibraryProjectionRow(h.db.Session(), orgID, repoID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read library projection row"})
		return
	}
	projectionRow := previousRow
	projectionRow.Name = req.RepoName
	projectionRow.UpdatedAt = now
	batch := h.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		UPDATE libraries SET name = ?, updated_at = ?
		WHERE org_id = ? AND library_id = ?
	`, req.RepoName, now, orgID, repoID)
	batch.Query(`
		UPDATE libraries_by_id SET name = ?
		WHERE library_id = ?
	`, req.RepoName, repoID)
	addAdminLibraryReadModelRefreshQueries(batch, projectionRow, &previousRow)

	if err := batch.Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to rename library"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ChangeStorageClassRequest represents the request body for changing storage class
type ChangeStorageClassRequest struct {
	StorageClass string `json:"storage_class" binding:"required"`
}

// ChangeStorageClass changes a library's storage class
func (h *LibraryHandler) ChangeStorageClass(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")

	var req ChangeStorageClassRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate storage class
	if !h.isKnownStorageClass(req.StorageClass) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid storage class"})
		return
	}

	if _, err := readLiveLibraryStateFn(h.db.Session(), orgID, repoID); err != nil {
		writeLiveLibraryStateError(c, err)
		return
	}

	now := time.Now()
	previousRow, err := db.ReadAdminLibraryProjectionRow(h.db.Session(), orgID, repoID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read library projection row"})
		return
	}
	projectionRow := previousRow
	projectionRow.StorageClass = req.StorageClass
	projectionRow.UpdatedAt = now
	batch := h.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		UPDATE libraries SET storage_class = ?, updated_at = ?
		WHERE org_id = ? AND library_id = ?
	`, req.StorageClass, now, orgID, repoID)
	addAdminLibraryReadModelRefreshQueries(batch, projectionRow, &previousRow)
	if err := batch.Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update storage class"})
		return
	}

	// TODO: Trigger background job to migrate blocks to new storage class

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// GetRepoFolderShareInfo returns share information for a folder in a repository
// GET /api/v2.1/repos/:repo_id/share-info?path=/folder
// For now, this is a stub that returns empty shares
func (h *LibraryHandler) GetRepoFolderShareInfo(c *gin.Context) {
	// Return empty share info - folder is not shared to anyone
	// Full implementation would query the shares table
	c.JSON(http.StatusOK, gin.H{
		"shared_user_emails": []string{},
		"shared_group_ids":   []int{},
	})
}

// V21Library represents a library in v2.1 API format
// This format uses different field names and ISO date format for Seahub frontend compatibility
type V21Library struct {
	Type                 string `json:"type"`
	RepoID               string `json:"repo_id"`
	RepoName             string `json:"repo_name"`
	OwnerEmail           string `json:"owner_email"`
	OwnerName            string `json:"owner_name"`
	OwnerContactEmail    string `json:"owner_contact_email"`
	LastModified         string `json:"last_modified"` // ISO 8601 format
	ModifierEmail        string `json:"modifier_email"`
	ModifierName         string `json:"modifier_name"`
	ModifierContactEmail string `json:"modifier_contact_email"`
	Size                 int64  `json:"size"`
	Encrypted            int    `json:"encrypted"` // CRITICAL: Must be int (0/1) not bool for Seafile frontend
	LibNeedDecrypt       bool   `json:"lib_need_decrypt"`
	Permission           string `json:"permission"`
	Starred              bool   `json:"starred"`
	Monitored            bool   `json:"monitored"`
	Status               string `json:"status"`
	Salt                 string `json:"salt"`
	StorageName          string `json:"storage_name,omitempty"`
}

// V21LibraryResponse represents the v2.1 API response for listing libraries
type V21LibraryResponse struct {
	Repos []V21Library `json:"repos"`
}

// ListLibrariesV21 returns all libraries in v2.1 API format for Seahub frontend
func (h *LibraryHandler) ListLibrariesV21(c *gin.Context) {
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")
	if orgID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing org_id"})
		return
	}

	if _, err := uuid.Parse(orgID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid org_id"})
		return
	}

	// ========================================================================
	// PERMISSION FILTER: Only return libraries user has access to
	// ========================================================================
	// Get all libraries user has access to (owned + shared)
	accessibleLibs, err := h.permMiddleware.GetUserLibraries(orgID, userID)
	if err != nil {
		log.Printf("[ListLibrariesV21] Failed to get user libraries: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get libraries"})
		return
	}

	// Build map of accessible library IDs for quick lookup
	accessibleMap := make(map[string]middleware.LibraryPermission)
	for _, lib := range accessibleLibs {
		accessibleMap[lib.LibraryID.String()] = lib.Permission
	}

	// If user has no accessible libraries, return empty array
	if len(accessibleMap) == 0 {
		c.JSON(http.StatusOK, V21LibraryResponse{Repos: []V21Library{}})
		return
	}

	// Query starred libraries for this user (path="/" means the library itself)
	// Note: We query all starred items and filter for path="/" in Go because Cassandra's
	// primary key ((user_id), repo_id, path) doesn't allow filtering by path alone
	starredLibs := make(map[string]bool)
	if userID != "" {
		starIter := h.db.Session().Query(`
			SELECT repo_id, path FROM starred_files WHERE user_id = ?
		`, userID).Iter()
		var starredRepoID, starredPath string
		for starIter.Scan(&starredRepoID, &starredPath) {
			if starredPath == "/" {
				starredLibs[starredRepoID] = true
			}
		}
		starIter.Close()
	}

	// Query monitored libraries for this user
	monitoredLibs := make(map[string]bool)
	if userID != "" {
		monIter := h.db.Session().Query(`
			SELECT repo_id FROM monitored_repos WHERE user_id = ?
		`, userID).Iter()
		var monRepoID string
		for monIter.Scan(&monRepoID) {
			monitoredLibs[monRepoID] = true
		}
		monIter.Close()
	}

	// Read type filter: "mine", "shared", or "" (all)
	typeFilter := c.Query("type")

	// Query libraries from database
	iter := h.db.Session().Query(`
		SELECT library_id, owner_id, name, description, encrypted,
			   storage_class, size_bytes, file_count, created_at, updated_at, deleted_at
		FROM libraries WHERE org_id = ?
	`, orgID).Iter()

	var libraries []V21Library
	var libID, ownerID string
	var name, description, storageClass string
	var encrypted bool
	var sizeBytes, fileCount int64
	var createdAt, updatedAt time.Time
	var deletedAt time.Time

	for iter.Scan(
		&libID, &ownerID, &name, &description,
		&encrypted, &storageClass, &sizeBytes,
		&fileCount, &createdAt, &updatedAt, &deletedAt,
	) {
		// Skip soft-deleted libraries
		if !deletedAt.IsZero() {
			continue
		}

		// ========================================================================
		// PERMISSION FILTER: Skip libraries user doesn't have access to
		// ========================================================================
		permission, hasAccess := accessibleMap[libID]
		if !hasAccess {
			continue // Skip this library - user doesn't have access
		}

		ownerEmail := h.resolveOwnerEmail(orgID, ownerID)

		// Determine library type (mine, shared, public)
		libType := "mine"
		if ownerID != userID {
			libType = "shared"
		}

		// Apply type filter if specified
		if typeFilter != "" && libType != typeFilter {
			continue
		}

		// Check if this library is starred
		isStarred := starredLibs[libID]

		// Check if encrypted library needs decryption (not yet unlocked by user)
		libNeedDecrypt := false
		if encrypted && userID != "" {
			libNeedDecrypt = !GetDecryptSessions().IsUnlocked(userID, libID)
		}

		// Convert encrypted bool to int (0/1) for Seafile frontend compatibility
		encryptedInt := 0
		if encrypted {
			encryptedInt = 1
		}

		libraries = append(libraries, V21Library{
			Type:                 libType,
			RepoID:               libID,
			RepoName:             name,
			OwnerEmail:           ownerEmail,
			OwnerName:            strings.Split(ownerEmail, "@")[0], // Extract name from email
			OwnerContactEmail:    ownerEmail,
			LastModified:         updatedAt.Format(time.RFC3339), // ISO 8601 format
			ModifierEmail:        ownerEmail,
			ModifierName:         strings.Split(ownerEmail, "@")[0],
			ModifierContactEmail: ownerEmail,
			Size:                 sizeBytes,
			Encrypted:            encryptedInt,
			LibNeedDecrypt:       libNeedDecrypt,
			Permission:           apiPermission(permission), // Use Seafile-compatible permission level
			Starred:              isStarred,
			Monitored:            monitoredLibs[libID],
			Status:               "normal",
			Salt:                 "",
			StorageName:          h.displayStorageName(storageClass),
		})
	}

	if err := iter.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list libraries", "details": err.Error()})
		return
	}

	// Return empty array instead of null
	if libraries == nil {
		libraries = []V21Library{}
	}

	c.JSON(http.StatusOK, V21LibraryResponse{Repos: libraries})
}

// GetLibraryV21 returns a single library in v2.1 API format
func (h *LibraryHandler) GetLibraryV21(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	if _, err := uuid.Parse(repoID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid repo_id"})
		return
	}

	// ========================================================================
	// PERMISSION CHECK: User must have at least read access
	// ========================================================================
	var userPermission middleware.LibraryPermission

	if isRepoToken, _ := c.Get("repo_api_token"); isRepoToken == true {
		tokenRepoID := c.GetString("repo_api_token_repo_id")
		tokenPerm := c.GetString("repo_api_token_permission")
		if tokenRepoID != repoID {
			c.JSON(http.StatusForbidden, gin.H{"error": "API token does not have access to this library"})
			return
		}
		if tokenPerm == "rw" {
			userPermission = middleware.PermissionRW
		} else {
			userPermission = middleware.PermissionR
		}
	} else {
		var err error
		userPermission, err = getLibraryPermissionFn(h.permMiddleware, orgID, userID, repoID)
		if err != nil {
			log.Printf("[GetLibraryV21] Failed to check permissions: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check permissions"})
			return
		}
	}

	if userPermission == middleware.PermissionNone {
		// GetLibraryPermission returns PermissionNone for both "library missing" and
		// "no access"; a missing/soft-deleted library must surface as 404, otherwise
		// the frontend renders a misleading "Permission denied / Leave Share" screen.
		if respondIfLibraryMissing(c, h.db.Session(), orgID, repoID) {
			return
		}
		log.Printf("[GetLibraryV21] Permission denied: user %q does not have access to library %q", userID, repoID)
		c.JSON(http.StatusForbidden, gin.H{"error": "you do not have access to this library"})
		return
	}

	// Get raw permission string (may be "custom-{uuid}" for custom permissions)
	rawPermission := apiPermission(userPermission)
	if h.permMiddleware != nil {
		if rp, err := h.permMiddleware.GetLibraryPermissionRaw(orgID, userID, repoID); err == nil && rp != "" {
			rawPermission = rp
		}
	}

	var libID, ownerID string
	var name, description, storageClass string
	var encrypted bool
	var sizeBytes, fileCount int64
	var headCommitID string
	var createdAt, updatedAt time.Time
	var deletedAtV21 time.Time

	if err := h.db.Session().Query(`
		SELECT library_id, owner_id, name, description, encrypted,
			   storage_class, size_bytes, file_count, head_commit_id,
			   created_at, updated_at, deleted_at
		FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(
		&libID, &ownerID, &name, &description,
		&encrypted, &storageClass, &sizeBytes,
		&fileCount, &headCommitID, &createdAt, &updatedAt,
		&deletedAtV21,
	); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	// Soft-deleted libraries should not be accessible
	if !deletedAtV21.IsZero() {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	ownerEmail := h.resolveOwnerEmail(orgID, ownerID)

	// Determine is_admin: true for owners and rw-shared users
	isAdmin := userPermission == middleware.PermissionOwner || userPermission == middleware.PermissionRW

	// Check if this library is starred by the user
	isStarred := false
	if userID != "" {
		var starredAt time.Time
		err := h.db.Session().Query(`
			SELECT starred_at FROM starred_files WHERE user_id = ? AND repo_id = ? AND path = ?
		`, userID, libID, "/").Scan(&starredAt)
		isStarred = (err == nil)
	}

	// Check if encrypted library needs decryption
	libNeedDecrypt := false
	if encrypted && userID != "" {
		// Check if user has unlocked this library
		libNeedDecrypt = !GetDecryptSessions().IsUnlocked(userID, libID)
	}

	// Return v2.1 format response (matches Seafile's /api/v2.1/repos/:id/ format)
	response := gin.H{
		"repo_id":             libID,
		"repo_name":           name,
		"owner_email":         ownerEmail,
		"owner_name":          strings.Split(ownerEmail, "@")[0],
		"owner_contact_email": ownerEmail,
		"size":                sizeBytes,
		"encrypted":           encrypted,
		"file_count":          fileCount,
		"permission":          rawPermission,
		"no_quota":            true,
		"is_admin":            isAdmin,
		"is_virtual":          false,
		"has_been_shared_out": false,
		"lib_need_decrypt":    libNeedDecrypt,
		"last_modified":       updatedAt.Format(time.RFC3339),
		"status":              "normal",
		"starred":             isStarred,
	}

	c.JSON(http.StatusOK, response)
}
