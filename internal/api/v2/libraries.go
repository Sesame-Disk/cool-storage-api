package v2

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// LibraryTokenCreator is an interface for creating sync tokens
type LibraryTokenCreator interface {
	CreateDownloadToken(orgID, repoID, path, userID string) (string, error)
}

// LibraryHandler handles library-related API requests
type LibraryHandler struct {
	db           *db.DB
	config       *config.Config
	tokenCreator LibraryTokenCreator
}

// RegisterLibraryRoutes registers library routes
func RegisterLibraryRoutes(rg *gin.RouterGroup, database *db.DB, cfg *config.Config) {
	RegisterLibraryRoutesWithToken(rg, database, cfg, nil)
}

// RegisterLibraryRoutesWithToken registers library routes with token creator
func RegisterLibraryRoutesWithToken(rg *gin.RouterGroup, database *db.DB, cfg *config.Config, tokenCreator LibraryTokenCreator) {
	h := &LibraryHandler{db: database, config: cfg, tokenCreator: tokenCreator}

	repos := rg.Group("/repos")
	{
		repos.GET("", h.ListLibraries)
		repos.POST("", h.CreateLibrary)
		repos.GET("/:repo_id", h.GetLibrary)
		repos.PUT("/:repo_id", h.UpdateLibrary)
		repos.DELETE("/:repo_id", h.DeleteLibrary)
		repos.POST("/:repo_id/storage-class", h.ChangeStorageClass)
	}
}

// ListLibraries returns all libraries for the authenticated user
func (h *LibraryHandler) ListLibraries(c *gin.Context) {
	orgID := c.GetString("org_id")
	if orgID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing org_id"})
		return
	}

	if _, err := uuid.Parse(orgID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid org_id"})
		return
	}

	// Query libraries from database (use string for UUID binding)
	iter := h.db.Session().Query(`
		SELECT library_id, owner_id, name, description, encrypted,
			   storage_class, size_bytes, file_count, created_at, updated_at
		FROM libraries WHERE org_id = ?
	`, orgID).Iter()

	var libraries []models.Library
	var libID, ownerID string
	var name, description, storageClass string
	var encrypted bool
	var sizeBytes, fileCount int64
	var createdAt, updatedAt time.Time

	for iter.Scan(
		&libID, &ownerID, &name, &description,
		&encrypted, &storageClass, &sizeBytes,
		&fileCount, &createdAt, &updatedAt,
	) {
		libUUID, _ := uuid.Parse(libID)
		ownerUUID, _ := uuid.Parse(ownerID)
		orgUUID, _ := uuid.Parse(orgID)

		libraries = append(libraries, models.Library{
			LibraryID:    libUUID,
			OrgID:        orgUUID,
			OwnerID:      ownerUUID,
			Owner:        ownerID + "@sesamefs.local", // Seafile expects email
			Name:         name,
			Description:  description,
			Encrypted:    encrypted,
			StorageClass: storageClass,
			SizeBytes:    sizeBytes,
			FileCount:    fileCount,
			MTime:        updatedAt.Unix(), // Unix timestamp for Seafile
			Type:         "repo",           // Seafile library type
			Permission:   "rw",             // TODO: Check actual permissions
			CreatedAt:    createdAt,
			UpdatedAt:    updatedAt,
		})
	}

	if err := iter.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list libraries", "details": err.Error()})
		return
	}

	// Return empty array instead of null
	if libraries == nil {
		libraries = []models.Library{}
	}

	c.JSON(http.StatusOK, libraries)
}

// CreateLibraryRequest represents the request body for creating a library
type CreateLibraryRequest struct {
	Name        string `json:"name" form:"name"`
	Description string `json:"description" form:"desc"` // Seafile uses "desc" in form
	Encrypted   bool   `json:"encrypted" form:"encrypted"`
	Password    string `json:"password,omitempty" form:"passwd"` // Seafile uses "passwd" in form
}

// CreateLibrary creates a new library
func (h *LibraryHandler) CreateLibrary(c *gin.Context) {
	var req CreateLibraryRequest

	// Try JSON first, then fall back to form data (Seafile desktop uses form data)
	contentType := c.GetHeader("Content-Type")
	if strings.Contains(contentType, "application/json") {
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	} else {
		// Form data (application/x-www-form-urlencoded)
		req.Name = c.PostForm("name")
		req.Description = c.PostForm("desc")
		req.Password = c.PostForm("passwd")
		req.Encrypted = c.PostForm("encrypted") == "true" || c.PostForm("encrypted") == "1"
	}

	// Validate required field
	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}

	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	// Check if a library with this name already exists for this user
	var existingName string
	iter := h.db.Session().Query(`
		SELECT name FROM libraries WHERE org_id = ? AND owner_id = ? ALLOW FILTERING
	`, orgID, userID).Iter()
	for iter.Scan(&existingName) {
		if existingName == req.Name {
			iter.Close()
			c.JSON(http.StatusConflict, gin.H{"error": "a library with this name already exists"})
			return
		}
	}
	iter.Close()

	orgUUID, _ := uuid.Parse(orgID)
	userUUID, _ := uuid.Parse(userID)
	newLibID := uuid.New()

	now := time.Now()
	library := models.Library{
		LibraryID:      newLibID,
		OrgID:          orgUUID,
		OwnerID:        userUUID,
		Name:           req.Name,
		Description:    req.Description,
		Encrypted:      req.Encrypted,
		StorageClass:   h.config.Storage.DefaultClass,
		SizeBytes:      0,
		FileCount:      0,
		VersionTTLDays: h.config.Versioning.DefaultTTLDays,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	// Create empty root directory fs_object
	// Seafile uses a specific format for empty directories - the fs_id is the SHA-1 hash
	// of the serialized directory content. For an empty dir, we use a well-known empty dir hash.
	emptyDirEntries := "[]" // Empty JSON array for directory entries
	emptyDirData := fmt.Sprintf("%d\n%s", 1, emptyDirEntries) // version + entries
	emptyDirHash := sha1.Sum([]byte(emptyDirData))
	rootFSID := hex.EncodeToString(emptyDirHash[:])

	// Store empty root directory in fs_objects
	if err := h.db.Session().Query(`
		INSERT INTO fs_objects (library_id, fs_id, obj_type, obj_name, dir_entries, mtime)
		VALUES (?, ?, ?, ?, ?, ?)
	`, newLibID.String(), rootFSID, "dir", "", emptyDirEntries, now.Unix()).Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create root directory", "details": err.Error()})
		return
	}

	// Generate initial commit ID (SHA-1 hash of repo creation data)
	commitData := fmt.Sprintf("%s:%s:%d", newLibID.String(), req.Name, now.UnixNano())
	commitHash := sha1.Sum([]byte(commitData))
	headCommitID := hex.EncodeToString(commitHash[:])

	// Insert into database with head_commit_id
	if err := h.db.Session().Query(`
		INSERT INTO libraries (
			org_id, library_id, owner_id, name, description, encrypted,
			storage_class, size_bytes, file_count, version_ttl_days,
			head_commit_id, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, orgID, newLibID.String(), userID, library.Name,
		library.Description, library.Encrypted, library.StorageClass,
		library.SizeBytes, library.FileCount, library.VersionTTLDays,
		headCommitID, library.CreatedAt, library.UpdatedAt,
	).Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create library", "details": err.Error()})
		return
	}

	// Create initial commit record with root_fs_id pointing to empty root directory
	if err := h.db.Session().Query(`
		INSERT INTO commits (library_id, commit_id, root_fs_id, creator_id, description, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, newLibID.String(), headCommitID, rootFSID, userID, "Initial commit", now).Exec(); err != nil {
		// Non-fatal - library was created
	}

	// Get user email for response
	userEmail := c.GetString("user_email")
	if userEmail == "" {
		userEmail = userID + "@sesamefs.local"
	}

	// Generate sync token if token creator is available
	syncToken := ""
	if h.tokenCreator != nil {
		token, err := h.tokenCreator.CreateDownloadToken(orgID, newLibID.String(), "/", userID)
		if err == nil {
			syncToken = token
		}
	}

	// Get server port for relay info
	serverPort := "8080"
	if h.config != nil && h.config.Server.Port != "" {
		serverPort = strings.TrimPrefix(h.config.Server.Port, ":")
	}

	// Return Seafile-compatible response (HTTP 200, not 201)
	// This format matches what Seafile returns and includes sync info
	response := gin.H{
		"relay_id":        "localhost",
		"relay_addr":      "localhost",
		"relay_port":      serverPort,
		"email":           userEmail,
		"token":           syncToken,
		"repo_id":         newLibID.String(),
		"repo_name":       req.Name,
		"repo_desc":       req.Description,
		"repo_size":       0,
		"repo_size_formatted": "0 bytes",
		"mtime":           now.Unix(),
		"mtime_relative":  "",
		"encrypted":       "",
		"enc_version":     0,
		"salt":            "",
		"magic":           "",
		"random_key":      "",
		"repo_version":    1,
		"head_commit_id":  headCommitID,
		"permission":      "rw",
	}

	// Set encrypted fields if library is encrypted
	if req.Encrypted {
		response["encrypted"] = true
		// TODO: Handle encrypted library setup (magic, random_key, salt)
	}

	c.JSON(http.StatusOK, response)
}

// GetLibrary returns a single library by ID
func (h *LibraryHandler) GetLibrary(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")

	if _, err := uuid.Parse(repoID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid repo_id"})
		return
	}

	var libID, ownerID string
	var name, description, storageClass string
	var encrypted bool
	var sizeBytes, fileCount int64
	var versionTTLDays int
	var createdAt, updatedAt time.Time

	if err := h.db.Session().Query(`
		SELECT library_id, owner_id, name, description, encrypted,
			   storage_class, size_bytes, file_count, version_ttl_days,
			   created_at, updated_at
		FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(
		&libID, &ownerID, &name, &description,
		&encrypted, &storageClass, &sizeBytes,
		&fileCount, &versionTTLDays, &createdAt, &updatedAt,
	); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	libUUID, _ := uuid.Parse(libID)
	ownerUUID, _ := uuid.Parse(ownerID)
	orgUUID, _ := uuid.Parse(orgID)

	lib := models.Library{
		LibraryID:      libUUID,
		OrgID:          orgUUID,
		OwnerID:        ownerUUID,
		Name:           name,
		Description:    description,
		Encrypted:      encrypted,
		StorageClass:   storageClass,
		SizeBytes:      sizeBytes,
		FileCount:      fileCount,
		VersionTTLDays: versionTTLDays,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
	}

	c.JSON(http.StatusOK, lib)
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

	updates = append(updates, "updated_at = ?")
	values = append(values, time.Now())
	values = append(values, orgID, repoID) // Use strings for UUIDs

	query := "UPDATE libraries SET "
	for i, u := range updates {
		if i > 0 {
			query += ", "
		}
		query += u
	}
	query += " WHERE org_id = ? AND library_id = ?"

	if err := h.db.Session().Query(query, values...).Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update library"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// DeleteLibrary deletes a library
func (h *LibraryHandler) DeleteLibrary(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")

	// TODO: Delete all files, blocks, commits, etc.
	// For now, just delete the library record

	if err := h.db.Session().Query(`
		DELETE FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete library"})
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
	if _, ok := h.config.Storage.Backends[req.StorageClass]; !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid storage class"})
		return
	}

	if err := h.db.Session().Query(`
		UPDATE libraries SET storage_class = ?, updated_at = ?
		WHERE org_id = ? AND library_id = ?
	`, req.StorageClass, time.Now(), orgID, repoID).Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update storage class"})
		return
	}

	// TODO: Trigger background job to migrate blocks to new storage class

	c.JSON(http.StatusOK, gin.H{"success": true})
}
