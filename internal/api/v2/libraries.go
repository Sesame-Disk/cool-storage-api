package v2

import (
	"net/http"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// LibraryHandler handles library-related API requests
type LibraryHandler struct {
	db     *db.DB
	config *config.Config
}

// RegisterLibraryRoutes registers library routes
func RegisterLibraryRoutes(rg *gin.RouterGroup, database *db.DB, cfg *config.Config) {
	h := &LibraryHandler{db: database, config: cfg}

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
			Name:         name,
			Description:  description,
			Encrypted:    encrypted,
			StorageClass: storageClass,
			SizeBytes:    sizeBytes,
			FileCount:    fileCount,
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
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
	Encrypted   bool   `json:"encrypted"`
	Password    string `json:"password,omitempty"` // For encrypted libraries
}

// CreateLibrary creates a new library
func (h *LibraryHandler) CreateLibrary(c *gin.Context) {
	var req CreateLibraryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

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

	// TODO: Handle encrypted library setup (magic, random_key)

	// Insert into database (pass UUIDs as strings)
	if err := h.db.Session().Query(`
		INSERT INTO libraries (
			org_id, library_id, owner_id, name, description, encrypted,
			storage_class, size_bytes, file_count, version_ttl_days,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, orgID, newLibID.String(), userID, library.Name,
		library.Description, library.Encrypted, library.StorageClass,
		library.SizeBytes, library.FileCount, library.VersionTTLDays,
		library.CreatedAt, library.UpdatedAt,
	).Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create library", "details": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, library)
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
