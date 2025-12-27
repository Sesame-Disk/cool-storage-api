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

// RestoreHandler handles Glacier restore-related API requests
type RestoreHandler struct {
	db     *db.DB
	config *config.Config
}

// RegisterRestoreRoutes registers restore routes
func RegisterRestoreRoutes(rg *gin.RouterGroup, database *db.DB, cfg *config.Config) {
	h := &RestoreHandler{db: database, config: cfg}

	// Restore operations on files
	rg.POST("/repos/:repo_id/file/restore", h.InitiateRestore)
	rg.GET("/repos/:repo_id/file/restore-status", h.GetRestoreStatus)

	// List all restore jobs
	rg.GET("/restore-jobs", h.ListRestoreJobs)
	rg.GET("/restore-jobs/:job_id", h.GetRestoreJob)
}

// InitiateRestoreRequest represents the request for initiating a restore
type InitiateRestoreRequest struct {
	Path string `json:"path" form:"path" binding:"required"`
}

// InitiateRestore initiates a restore job for a file in cold storage
func (h *RestoreHandler) InitiateRestore(c *gin.Context) {
	repoID := c.Param("repo_id")

	var req InitiateRestoreRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	orgID := c.GetString("org_id")

	repoUUID, _ := uuid.Parse(repoID)
	orgUUID, _ := uuid.Parse(orgID)
	newJobID := uuid.New()

	// TODO: Get file info and check if it's in cold storage
	// TODO: Get block IDs for the file
	// TODO: Initiate Glacier restore job

	// For now, create a placeholder restore job
	job := models.RestoreJob{
		JobID:       newJobID,
		OrgID:       orgUUID,
		LibraryID:   repoUUID,
		BlockIDs:    []string{}, // TODO: populate from file
		Status:      "pending",
		RequestedAt: time.Now(),
	}

	// Insert into database (use strings for UUIDs)
	if err := h.db.Session().Query(`
		INSERT INTO restore_jobs (
			org_id, job_id, library_id, block_ids, status, requested_at
		) VALUES (?, ?, ?, ?, ?, ?)
	`, orgID, newJobID.String(), repoID, job.BlockIDs, job.Status, job.RequestedAt,
	).Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create restore job"})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"job_id":  job.JobID.String(),
		"status":  job.Status,
		"message": "Restore job initiated. This may take several hours for Glacier storage.",
	})
}

// GetRestoreStatus returns the status of a restore operation for a file
func (h *RestoreHandler) GetRestoreStatus(c *gin.Context) {
	repoID := c.Param("repo_id")
	filePath := c.Query("p")

	if filePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}

	orgID := c.GetString("org_id")

	// Find the most recent restore job for this file (use strings for UUIDs)
	// TODO: Add file path to restore jobs for better tracking
	iter := h.db.Session().Query(`
		SELECT job_id, status, requested_at, completed_at
		FROM restore_jobs
		WHERE org_id = ? AND library_id = ?
		ALLOW FILTERING
	`, orgID, repoID).Iter()

	var latestJobID, latestStatus string
	var latestRequestedAt time.Time
	var latestCompletedAt *time.Time
	var found bool

	var jobID, status string
	var requestedAt time.Time
	var completedAt *time.Time

	for iter.Scan(&jobID, &status, &requestedAt, &completedAt) {
		if !found || requestedAt.After(latestRequestedAt) {
			latestJobID = jobID
			latestStatus = status
			latestRequestedAt = requestedAt
			latestCompletedAt = completedAt
			found = true
		}
	}
	iter.Close()

	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "no restore job found for this file"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"job_id":       latestJobID,
		"status":       latestStatus,
		"requested_at": latestRequestedAt,
		"completed_at": latestCompletedAt,
	})
}

// ListRestoreJobs returns all restore jobs for the organization
func (h *RestoreHandler) ListRestoreJobs(c *gin.Context) {
	orgID := c.GetString("org_id")
	orgUUID, _ := uuid.Parse(orgID)

	iter := h.db.Session().Query(`
		SELECT job_id, library_id, status, requested_at, completed_at, expires_at
		FROM restore_jobs WHERE org_id = ?
	`, orgID).Iter()

	var jobs []models.RestoreJob
	var jobID, libID, status string
	var requestedAt time.Time
	var completedAt, expiresAt *time.Time

	for iter.Scan(
		&jobID, &libID, &status,
		&requestedAt, &completedAt, &expiresAt,
	) {
		jobUUID, _ := uuid.Parse(jobID)
		libUUID, _ := uuid.Parse(libID)
		jobs = append(jobs, models.RestoreJob{
			JobID:       jobUUID,
			OrgID:       orgUUID,
			LibraryID:   libUUID,
			Status:      status,
			RequestedAt: requestedAt,
			CompletedAt: completedAt,
			ExpiresAt:   expiresAt,
		})
	}

	if err := iter.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list restore jobs"})
		return
	}

	if jobs == nil {
		jobs = []models.RestoreJob{}
	}

	c.JSON(http.StatusOK, jobs)
}

// GetRestoreJob returns a specific restore job
func (h *RestoreHandler) GetRestoreJob(c *gin.Context) {
	jobIDParam := c.Param("job_id")
	orgID := c.GetString("org_id")

	if _, err := uuid.Parse(jobIDParam); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid job_id"})
		return
	}

	orgUUID, _ := uuid.Parse(orgID)

	var jobID, libID, glacierJobID, status string
	var blockIDs []string
	var requestedAt time.Time
	var completedAt, expiresAt *time.Time

	if err := h.db.Session().Query(`
		SELECT job_id, library_id, block_ids, glacier_job_id, status,
			   requested_at, completed_at, expires_at
		FROM restore_jobs WHERE org_id = ? AND job_id = ?
	`, orgID, jobIDParam).Scan(
		&jobID, &libID, &blockIDs, &glacierJobID,
		&status, &requestedAt, &completedAt, &expiresAt,
	); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "restore job not found"})
		return
	}

	jobUUID, _ := uuid.Parse(jobID)
	libUUID, _ := uuid.Parse(libID)

	job := models.RestoreJob{
		JobID:        jobUUID,
		OrgID:        orgUUID,
		LibraryID:    libUUID,
		BlockIDs:     blockIDs,
		GlacierJobID: glacierJobID,
		Status:       status,
		RequestedAt:  requestedAt,
		CompletedAt:  completedAt,
		ExpiresAt:    expiresAt,
	}

	c.JSON(http.StatusOK, job)
}
