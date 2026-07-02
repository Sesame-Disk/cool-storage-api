package v2

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	"github.com/gin-gonic/gin"
	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"
)

// blockProbeConcurrency bounds parallel ProbeBlockReuse calls for session-mode
// /blocks/check so a large hash list does not become serial Cassandra round-trips.
const blockProbeConcurrency = 20

// probeBlocksReusableParallel returns, per hash, whether the block is commit-ready
// (ProbeBlockReuse == Reusable), computed with bounded concurrency.
func probeBlocksReusableParallel(ctx context.Context, database *db.DB, orgID string, hashes []string, concurrency int) (map[string]bool, error) {
	result := make(map[string]bool, len(hashes))
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	sem := make(chan struct{}, concurrency)
	for _, hash := range hashes {
		hash := hash
		g.Go(func() error {
			select {
			case sem <- struct{}{}:
			case <-gctx.Done():
				return gctx.Err()
			}
			defer func() { <-sem }()
			probe, err := database.ProbeBlockReuse(orgID, hash)
			if err != nil {
				return err
			}
			mu.Lock()
			result[hash] = probe.Decision == db.BlockReuseReusable
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return result, nil
}

// BlockHandler handles block-level API operations
type BlockHandler struct {
	blockStore     *storage.BlockStore // Legacy single store (fallback)
	storageManager *storage.Manager    // Multi-backend storage manager
	config         *config.Config
	db             *db.DB // Optional: enables session-scoped materialization for the web flow
}

// RegisterBlockRoutes registers the block API routes
// Supports both legacy single BlockStore and multi-region StorageManager
func RegisterBlockRoutes(rg *gin.RouterGroup, database *db.DB, blockStore *storage.BlockStore, storageManager *storage.Manager, cfg *config.Config) {
	h := &BlockHandler{
		blockStore:     blockStore,
		storageManager: storageManager,
		config:         cfg,
		db:             database,
	}

	// Per-IP rate limit for the block existence oracle.
	// 60 req/min with burst 120 lets legitimate clients batch-check during
	// resumable uploads while cutting off hash-enumeration probes.
	checkBlocksLimiter := middleware.NewRateLimiter(rate.Every(time.Second), 120)

	blocks := rg.Group("/blocks")
	{
		// Check which blocks exist (for deduplication and resume)
		blocks.POST("/check", checkBlocksLimiter.Limit(), h.CheckBlocks)

		// Upload a single block
		blocks.POST("/upload", h.UploadBlock)

		// There is deliberately NO GET/HEAD /blocks/:hash. S3 block keys are
		// global content-addressed objects with no org scoping, and block-level
		// reads cannot be authorized against a library permission without a repo
		// context — a bare-hash read endpoint is a cross-tenant (and intra-org)
		// content oracle by construction. Nothing consumes it: web downloads go
		// through file paths and desktop sync uses the repo-scoped, permission-
		// checked /seafhttp/repo/:repo_id/block/:block_id. If a client-side block
		// download flow is ever needed, reintroduce it repo-scoped like seafhttp
		// (repos/:repo_id/blocks/:hash + library permission + the block's
		// canonical storage class). See docs/WEB-BLOCK-UPLOAD.md finding 11.
	}
}

// getBlockStore returns the appropriate BlockStore based on hostname routing.
// When StorageManager is configured, failed manager resolution does not fall
// back to the legacy singleton store.
func (h *BlockHandler) getBlockStore(c *gin.Context) (*storage.BlockStore, string) {
	if h.storageManager == nil {
		return h.blockStore, "legacy"
	}

	// Resolve storage class based on hostname
	hostname := routingHostname(c, h.config)
	storageClass := h.storageManager.ResolveStorageClass(hostname, "", "hot")

	// Get healthy BlockStore with failover
	blockStore, actualClass, err := h.storageManager.GetHealthyBlockStore(storageClass)
	if err != nil {
		log.Printf("v2/blocks: failed to get healthy backend for %s: %v\n", storageClass, err)
		return nil, storageClass
	}

	return blockStore, actualClass
}

// lookupLibraryStorageClass reads the storage class a library's blocks live in.
func (h *BlockHandler) lookupLibraryStorageClass(orgID, repoID string) string {
	if h.db == nil || orgID == "" || repoID == "" {
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

// getBlockStoreForRepo resolves the BlockStore for a specific library, honoring
// its storage class. The session upload path MUST use this (not the generic
// hostname/"hot" getBlockStore), or it could store/materialize a block in a
// different backend than the one file-from-blocks later verifies for that repo —
// which would make the commit report the block missing even though it was sent.
func (h *BlockHandler) getBlockStoreForRepo(c *gin.Context, orgID, repoID string) (*storage.BlockStore, string) {
	if h.storageManager == nil {
		return h.blockStore, "legacy"
	}
	libraryClass := h.lookupLibraryStorageClass(orgID, repoID)
	preferred := h.storageManager.ResolveStorageClass(routingHostname(c, h.config), libraryClass, "hot")
	blockStore, actualClass, err := h.storageManager.GetHealthyBlockStore(preferred)
	if err != nil {
		log.Printf("v2/blocks: failed to get healthy backend for repo %s (%s): %v\n", repoID, preferred, err)
		return nil, preferred
	}
	return blockStore, actualClass
}

// blockUploadFlowEnabled reports whether the web block-upload flow is enabled
// server-side. The routes are always registered, so this flag is the real gate.
func (h *BlockHandler) blockUploadFlowEnabled() bool {
	return h.config != nil && h.config.WebUploads.EnableWebBlockUpload
}

// resolveUploadSession reads an optional ?session= query parameter for the web
// content-addressed upload flow. Returns (session, present, ok): present=false
// means no session was supplied (legacy desktop/mobile behavior); ok=false means
// a session was supplied but the flow is disabled, or it is invalid/expired or
// does not belong to the caller.
func (h *BlockHandler) resolveUploadSession(c *gin.Context) (db.BlockUploadSession, bool, bool) {
	sessionID := c.Query("session")
	if sessionID == "" {
		return db.BlockUploadSession{}, false, true
	}
	if h.db == nil || !h.blockUploadFlowEnabled() {
		return db.BlockUploadSession{}, true, false
	}
	session, found, err := h.db.GetBlockUploadSession(sessionID)
	if err != nil || !found {
		return db.BlockUploadSession{}, true, false
	}
	if session.OrgID != c.GetString("org_id") || session.UserID != c.GetString("user_id") {
		return db.BlockUploadSession{}, true, false
	}
	return session, true, true
}

// materializeUploadedBlock records block metadata plus a provisional reference
// owned by the session ("up:<session_id>") so the block is GC-governed and
// commit-ready (R9). It ALSO writes the authoritative SHA-1 → SHA-256 mapping
// (R10, dual-hash) via the WEB-only verified writer: sha1ID is computed by the
// server from the block's real bytes in UploadBlock, so this mapping is verified
// content — not client-asserted — and a conflicting external→different-internal
// remap fails closed (db.ErrBlockIDMappingConflict). The commit (file-from-blocks)
// only ever READS this mapping; it never mints one from the manifest, which is why
// a forged manifest SHA-1 cannot poison resolution. The shared legacy/seafhttp
// mapping path (WriteBlockIDMapping) is left untouched.
func (h *BlockHandler) materializeUploadedBlock(session db.BlockUploadSession, sha256ID, sha1ID string, size int, storageClass string) error {
	return RegisterWebUploadedBlockAndMapping(h.db, session.OrgID, session.RepoID, sha256ID, session.SessionID, size, storageClass, "", sha1ID)
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

	session, present, ok := h.resolveUploadSession(c)
	if present && !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired upload session"})
		return
	}

	// Session mode (web flow, R3): a block counts as "existing" only when it is
	// commit-ready (ProbeBlockReuse == Reusable). A block that exists physically
	// in S3 but has no live metadata/reference is reported as missing so the
	// client (re)uploads it under the session and materializes it — avoiding the
	// "S3 exists but unmaterialized → commit says NeedsPut" trap of the raw
	// physical oracle.
	if present {
		_ = session
		orgID := c.GetString("org_id")
		reusable, perr := probeBlocksReusableParallel(c.Request.Context(), h.db, orgID, req.Hashes, blockProbeConcurrency)
		if perr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check blocks"})
			return
		}
		var existing, missing []string
		for _, hash := range req.Hashes {
			if reusable[hash] {
				existing = append(existing, hash)
			} else {
				missing = append(missing, hash)
			}
		}
		c.JSON(http.StatusOK, CheckBlocksResponse{Existing: existing, Missing: missing})
		return
	}

	// Get appropriate BlockStore based on hostname routing
	blockStore, _ := h.getBlockStore(c)
	if blockStore == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "block storage not available"})
		return
	}

	// Legacy physical-existence oracle (desktop/mobile sync, no session).
	existsMap, err := blockStore.CheckBlocksParallel(c.Request.Context(), req.Hashes, 20)
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
	// Optional web-flow session: when present, the block is materialized
	// (metadata + provisional ref owned by the session) after storage, so an
	// abandoned upload self-expires and a later commit can publish it (R9).
	session, sessionPresent, sessionOK := h.resolveUploadSession(c)
	if sessionPresent && !sessionOK {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired upload session"})
		return
	}

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

	uploadTrafficStatus := traffic.QuotaStatus{Allowed: true}
	if checker := traffic.GetChecker(); checker != nil {
		orgID := c.GetString("org_id")
		userID := c.GetString("user_id")
		uploadTrafficStatus, _ = traffic.CheckTrafficQuotaWithChecker(
			checker,
			orgID,
			userID,
			"upload",
			c.Request.ContentLength,
		)
		if !uploadTrafficStatus.Allowed {
			c.JSON(http.StatusForbidden, traffic.TrafficQuotaExceededResponse(uploadTrafficStatus, "traffic quota exceeded", true))
			return
		}
		if warning, ok := traffic.TrafficQuotaWarningHeader(uploadTrafficStatus); ok {
			c.Header("X-Quota-Warning", warning)
		}
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
	sha1Bytes := sha1.Sum(data)
	sha1Hash := hex.EncodeToString(sha1Bytes[:])

	// Optional: verify the client-provided SHA-256 if present. The SHA-1 is no
	// longer client-asserted (PR5): the server derives it from the real bytes
	// (sha1Hash above) and stores it in blocks.sha1, so there is no X-Block-Hash-SHA1
	// to cross-check anymore.
	clientHash := c.GetHeader("X-Block-Hash")
	if clientHash != "" && clientHash != hash {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":         "hash mismatch",
			"algorithm":     "sha256",
			"expected_hash": clientHash,
			"actual_hash":   hash,
		})
		return
	}

	// Resolve the BlockStore. With a session, use the session repo's storage
	// class so the block lands in the SAME backend file-from-blocks will verify
	// for that repo (HIGH-2); otherwise use hostname/default routing.
	var blockStore *storage.BlockStore
	var storageClass string
	if sessionPresent {
		blockStore, storageClass = h.getBlockStoreForRepo(c, session.OrgID, session.RepoID)
	} else {
		blockStore, storageClass = h.getBlockStore(c)
	}
	if blockStore == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "block storage not available"})
		return
	}

	log.Printf("v2/blocks: uploading block %s to storage class %s\n", hash[:12], storageClass)

	// Check if block already exists
	exists, err := blockStore.BlockExists(c.Request.Context(), hash)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check block existence"})
		return
	}

	if exists {
		// Block already exists (deduplication) — data was still transferred over the network.
		if rec := traffic.Get(); rec != nil {
			traffic.RecordCheckedTransfer(rec, uploadTrafficStatus, c.GetString("org_id"), c.GetString("user_id"), traffic.WebUpload, int64(len(data)))
		}
		// R9: materialize even when S3 already has the object — the goal is to
		// GOVERN the block (metadata + provisional ref) so the session can commit
		// it and GC can reclaim it, not merely to store bytes.
		if sessionPresent {
			if err := h.materializeUploadedBlock(session, hash, sha1Hash, len(data), storageClass); err != nil {
				if errors.Is(err, db.ErrBlockIDMappingConflict) {
					c.JSON(http.StatusConflict, gin.H{"error": "block id mapping conflict"})
					return
				}
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to register block"})
				return
			}
		}
		c.JSON(http.StatusOK, UploadBlockResponse{
			Hash: hash,
			Size: int64(len(data)),
			New:  false,
		})
		return
	}

	// Logical storage quota is NOT applied per block in the session (web) flow.
	// R5: the user's repo/storage quota is a property of the FINAL file delta and
	// is decided exactly once at file-from-blocks (finalizeStoredUploadMetadata) —
	// a staged block is transient, governed by a provisional ref + TTL. Charging
	// it here would wrongly reject e.g. a same-size overwrite (logical delta ≈ 0)
	// at the first new block. Traffic is still charged per block (above).
	//
	// NOTE: this is NOT a staging-bytes cap. CheckStorageQuota is a LOGICAL quota
	// (it reads storage_used/quota counters, not physical staging). The legacy
	// no-session path below keeps running it per block only because it predates
	// this flow. Neither path bounds total uncommitted staged bytes; for the web
	// flow that is bounded instead by the upload traffic quota (charged above) and
	// the 48h provisional-ref TTL + GC. See docs/WEB-BLOCK-UPLOAD.md.
	if !sessionPresent {
		if checker := traffic.GetChecker(); checker != nil {
			if st, _ := checker.CheckStorageQuota(c.GetString("org_id"), c.GetString("user_id"), int64(len(data))); !st.Allowed {
				c.JSON(http.StatusForbidden, gin.H{"error": "storage quota exceeded"})
				return
			}
		}
	}

	// Store the block
	block := &storage.BlockData{
		Hash: hash,
		Data: data,
		Size: int64(len(data)),
	}

	_, err = blockStore.PutBlockData(c.Request.Context(), block)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to store block"})
		return
	}

	// Record traffic for newly stored block.
	if rec := traffic.Get(); rec != nil {
		traffic.RecordCheckedTransfer(rec, uploadTrafficStatus, c.GetString("org_id"), c.GetString("user_id"), traffic.WebUpload, int64(len(data)))
	}

	// R9: govern the freshly stored block under the session.
	if sessionPresent {
		if err := h.materializeUploadedBlock(session, hash, sha1Hash, len(data), storageClass); err != nil {
			if errors.Is(err, db.ErrBlockIDMappingConflict) {
				c.JSON(http.StatusConflict, gin.H{"error": "block id mapping conflict"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to register block"})
			return
		}
	}

	c.JSON(http.StatusCreated, UploadBlockResponse{
		Hash: hash,
		Size: int64(len(data)),
		New:  true,
	})
}
