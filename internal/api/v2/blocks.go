package v2

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/gin-gonic/gin"
)

// BlockHandler handles block-level API operations
type BlockHandler struct {
	blockStore *storage.BlockStore
	config     *config.Config
}

// RegisterBlockRoutes registers the block API routes
func RegisterBlockRoutes(rg *gin.RouterGroup, blockStore *storage.BlockStore, cfg *config.Config) {
	h := &BlockHandler{
		blockStore: blockStore,
		config:     cfg,
	}

	blocks := rg.Group("/blocks")
	{
		// Check which blocks exist (for deduplication and resume)
		blocks.POST("/check", h.CheckBlocks)

		// Upload a single block
		blocks.POST("/upload", h.UploadBlock)

		// Download a block by hash
		blocks.GET("/:hash", h.DownloadBlock)

		// Check if a single block exists
		blocks.HEAD("/:hash", h.BlockExists)
	}
}

// CheckBlocksRequest is the request body for checking blocks
type CheckBlocksRequest struct {
	Hashes []string `json:"hashes" binding:"required"`
}

// CheckBlocksResponse is the response for the check blocks endpoint
type CheckBlocksResponse struct {
	// Existing contains hashes of blocks that already exist
	Existing []string `json:"existing"`
	// Missing contains hashes of blocks that need to be uploaded
	Missing []string `json:"missing"`
}

// CheckBlocks checks which blocks from a list already exist
// POST /api/v2/blocks/check
// This is the key endpoint for deduplication and resumable uploads
func (h *BlockHandler) CheckBlocks(c *gin.Context) {
	var req CheckBlocksRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if len(req.Hashes) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "hashes array is required"})
		return
	}

	// Limit the number of hashes per request
	if len(req.Hashes) > 10000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "too many hashes, maximum is 10000"})
		return
	}

	// Check blocks in parallel for better performance
	existsMap, err := h.blockStore.CheckBlocksParallel(c.Request.Context(), req.Hashes, 20)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check blocks"})
		return
	}

	// Separate into existing and missing
	var existing, missing []string
	for _, hash := range req.Hashes {
		if existsMap[hash] {
			existing = append(existing, hash)
		} else {
			missing = append(missing, hash)
		}
	}

	c.JSON(http.StatusOK, CheckBlocksResponse{
		Existing: existing,
		Missing:  missing,
	})
}

// UploadBlockResponse is the response after uploading a block
type UploadBlockResponse struct {
	Hash string `json:"hash"`
	Size int64  `json:"size"`
	New  bool   `json:"new"` // true if this was a new block, false if it already existed
}

// UploadBlock uploads a single block
// POST /api/v2/blocks/upload
// The block content is sent in the request body
// The hash is computed server-side and verified
func (h *BlockHandler) UploadBlock(c *gin.Context) {
	// Check content length
	if c.Request.ContentLength <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "content length required"})
		return
	}

	// Check against maximum block size
	maxSize := h.config.Chunking.Adaptive.AbsoluteMax
	if c.Request.ContentLength > maxSize {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":    "block too large",
			"max_size": maxSize,
		})
		return
	}

	// Read the block data
	data, err := io.ReadAll(io.LimitReader(c.Request.Body, maxSize+1))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read block data"})
		return
	}

	if int64(len(data)) > maxSize {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":    "block too large",
			"max_size": maxSize,
		})
		return
	}

	// Compute hash
	hashBytes := sha256.Sum256(data)
	hash := hex.EncodeToString(hashBytes[:])

	// Optional: Verify client-provided hash if present
	clientHash := c.GetHeader("X-Block-Hash")
	if clientHash != "" && clientHash != hash {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":         "hash mismatch",
			"expected_hash": clientHash,
			"actual_hash":   hash,
		})
		return
	}

	// Check if block already exists
	exists, err := h.blockStore.BlockExists(c.Request.Context(), hash)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check block existence"})
		return
	}

	if exists {
		// Block already exists (deduplication)
		c.JSON(http.StatusOK, UploadBlockResponse{
			Hash: hash,
			Size: int64(len(data)),
			New:  false,
		})
		return
	}

	// Store the block
	block := &storage.BlockData{
		Hash: hash,
		Data: data,
		Size: int64(len(data)),
	}

	_, err = h.blockStore.PutBlockData(c.Request.Context(), block)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to store block"})
		return
	}

	c.JSON(http.StatusCreated, UploadBlockResponse{
		Hash: hash,
		Size: int64(len(data)),
		New:  true,
	})
}

// DownloadBlock downloads a block by its hash
// GET /api/v2/blocks/:hash
func (h *BlockHandler) DownloadBlock(c *gin.Context) {
	hash := c.Param("hash")

	if len(hash) != 64 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid hash format, expected 64 hex characters"})
		return
	}

	// Get the block
	data, err := h.blockStore.GetBlock(c.Request.Context(), hash)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "block not found"})
		return
	}

	// Set headers
	c.Header("Content-Type", "application/octet-stream")
	c.Header("X-Block-Hash", hash)

	c.Data(http.StatusOK, "application/octet-stream", data)
}

// BlockExists checks if a block exists (HEAD request)
// HEAD /api/v2/blocks/:hash
func (h *BlockHandler) BlockExists(c *gin.Context) {
	hash := c.Param("hash")

	if len(hash) != 64 {
		c.Status(http.StatusBadRequest)
		return
	}

	exists, err := h.blockStore.BlockExists(c.Request.Context(), hash)
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}

	if exists {
		c.Status(http.StatusOK)
	} else {
		c.Status(http.StatusNotFound)
	}
}
