package v2

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	"github.com/gin-gonic/gin"
)

// waitForBlockUploadResult polls for the result of a concurrent winner's commit
// on the same session, so a losing/retried request returns the same file
// (idempotency, R7) instead of duplicating it. Bounded (~10s); returns ok=false
// on timeout or if a different manifest won the session.
func (h *FileHandler) waitForBlockUploadResult(sessionID, digest string) (string, bool) {
	for i := 0; i < 50; i++ {
		s, ok, err := h.db.GetBlockUploadSession(sessionID)
		if err != nil || !ok {
			return "", false
		}
		if s.Committed && s.ManifestDigest != digest {
			return "", false
		}
		if s.ResultFilename != "" && s.ManifestDigest == digest {
			return s.ResultFilename, true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return "", false
}

// Manifest limits for the web content-addressed upload flow (R6). A 131072-block
// manifest at 8 MB/block bounds a single committed file at ~1 TiB, which is far
// beyond any realistic web upload while still rejecting absurd manifests early.
const (
	maxBlocksPerManifest       = 131072
	maxFileFromBlocksTotalSize = int64(maxBlocksPerManifest) * WebUploadBlockSize
)

// fileFromBlocksBlock is one ordered entry of the commit manifest. Only the
// SHA-256 (storage/internal ID) and the plaintext block size are needed; the web
// flow uses SHA-256 as the external block ID too (no SHA-1, no mapping — R10).
type fileFromBlocksBlock struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// fileFromBlocksRequest is the commit manifest posted after the client has
// uploaded all missing blocks under the session.
type fileFromBlocksRequest struct {
	Session   string                `json:"session"`
	ParentDir string                `json:"parent_dir"`
	Filename  string                `json:"filename"`
	Replace   bool                  `json:"replace"`
	Size      int64                 `json:"size"`
	Blocks    []fileFromBlocksBlock `json:"blocks"`
}

// manifestDigest is a stable fingerprint of the commit intent, used to make a
// retried commit idempotent (R7): the same session + same digest returns the
// previously committed result instead of creating a duplicate.
func (r *fileFromBlocksRequest) manifestDigest() string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\n%s\n%t\n%d\n", r.ParentDir, r.Filename, r.Replace, r.Size)
	for _, b := range r.Blocks {
		fmt.Fprintf(h, "%s:%d\n", b.SHA256, b.Size)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// validateManifest enforces the structural rules (R6): fixed 8 MB blocks except
// the last, total size consistency, hash format, and manifest bounds. It does
// NOT touch storage — physical/liveness checks happen per block in the handler.
func validateManifest(req *fileFromBlocksRequest) error {
	if len(req.Blocks) == 0 {
		return fmt.Errorf("blocks is required")
	}
	if len(req.Blocks) > maxBlocksPerManifest {
		return fmt.Errorf("too many blocks (max %d)", maxBlocksPerManifest)
	}
	if req.Size <= 0 || req.Size > maxFileFromBlocksTotalSize {
		return fmt.Errorf("invalid size")
	}
	if strings.TrimSpace(req.Filename) == "" ||
		strings.ContainsAny(req.Filename, "/\\") {
		return fmt.Errorf("invalid filename")
	}

	var total int64
	lastIdx := len(req.Blocks) - 1
	for i, b := range req.Blocks {
		if !isHex64(b.SHA256) {
			return fmt.Errorf("block %d: invalid sha256", i)
		}
		if i < lastIdx {
			if b.Size != WebUploadBlockSize {
				return fmt.Errorf("block %d: non-final blocks must be exactly %d bytes", i, WebUploadBlockSize)
			}
		} else {
			if b.Size <= 0 || b.Size > WebUploadBlockSize {
				return fmt.Errorf("block %d: final block size out of range", i)
			}
		}
		total += b.Size
	}
	if total != req.Size {
		return fmt.Errorf("sum of block sizes (%d) does not match size (%d)", total, req.Size)
	}
	return nil
}

// CreateFileFromBlocks commits a file from a manifest of already-uploaded blocks
// (the web content-addressed upload flow). See the design rules R1/R5/R6/R7/R8/R11
// in the plan: it never trusts S3 existence (R1), validates the manifest (R6),
// verifies each block is live+present+owned at the declared size (R8/R11), uses
// the logical storage delta for quota (R5), and is idempotent per session (R7).
//
// POST /api/v2.1/repos/:repo_id/file-from-blocks/
func (h *FileHandler) CreateFileFromBlocks(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	if !h.requireWritePermission(c, orgID, userID) {
		return
	}
	if h.permMiddleware != nil && !h.permMiddleware.RequirePermFlag(c, "upload") {
		c.JSON(http.StatusForbidden, gin.H{"error": "upload is not allowed by your permission"})
		return
	}
	if h.db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
		return
	}

	var req fileFromBlocksRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	req.ParentDir = normalizePath(req.ParentDir)
	if req.ParentDir == "" {
		req.ParentDir = "/"
	}
	if err := validateManifest(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// R7: validate the server-issued session and bind it to this caller + repo.
	session, ok, err := h.db.GetBlockUploadSession(req.Session)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read upload session"})
		return
	}
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired upload session"})
		return
	}
	if session.OrgID != orgID || session.UserID != userID || session.RepoID != repoID {
		c.JSON(http.StatusForbidden, gin.H{"error": "session does not belong to this request"})
		return
	}

	digest := req.manifestDigest()

	// R7: idempotent replay. A retried commit with the same manifest returns the
	// original result instead of auto-renaming a duplicate.
	if session.Committed {
		if session.ManifestDigest != digest {
			c.JSON(http.StatusConflict, gin.H{"error": "session already committed a different file"})
			return
		}
		if session.ResultFilename != "" {
			c.JSON(http.StatusOK, fileFromBlocksResponse(&req, session.ResultFilename))
			return
		}
		// Claimed but finalize not finished yet (concurrent commit in flight).
		if name, ok := h.waitForBlockUploadResult(session.SessionID, digest); ok {
			c.JSON(http.StatusOK, fileFromBlocksResponse(&req, name))
			return
		}
		c.JSON(http.StatusConflict, gin.H{"error": "commit still in progress; retry"})
		return
	}

	// Reject encrypted libraries (plaintext SHA-256 only).
	if encrypted, err := h.libraryEncrypted(orgID, repoID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	} else if encrypted {
		c.JSON(http.StatusConflict, gin.H{"error": "encrypted libraries are not supported by the block upload flow"})
		return
	}

	// LOCK ENFORCEMENT: overwriting a file locked by another user is forbidden.
	if req.Replace && !h.requireFileNotLockedByOther(c, repoID, req.ParentDir+"/"+req.Filename, userID) {
		return
	}

	// Resolve the block store so we can confirm PHYSICAL presence per block.
	// ProbeBlockReuse proves liveness/governance but not that the S3 object still
	// exists (metadata can outlive a lost object); a commit must require both, or
	// it could publish a file pointing at a missing block.
	blockStore, _, err := h.resolveLibraryBlockStore(c, orgID, repoID)
	if err != nil || blockStore == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "block storage not available"})
		return
	}
	uniqueHashes := make([]string, 0, len(req.Blocks))
	seen := make(map[string]struct{}, len(req.Blocks))
	for _, b := range req.Blocks {
		if _, ok := seen[b.SHA256]; !ok {
			seen[b.SHA256] = struct{}{}
			uniqueHashes = append(uniqueHashes, b.SHA256)
		}
	}
	existsMap, err := blockStore.CheckBlocksParallel(c.Request.Context(), uniqueHashes, 20)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to verify block presence"})
		return
	}

	// R1/R8/R11: per block, never trust S3 existence alone. Each block must be
	// live (ProbeBlockReuse == Reusable), physically present, owned by this session
	// or permanently referenced, and at the declared size. Blocks that are not
	// commit-ready are returned as needs_upload so the client (re)uploads them
	// under the session, which materializes metadata + a session-owned ref.
	referrer := db.BlockReferrerForUpload(session.SessionID)
	blockIDs := make([]string, len(req.Blocks))
	var needsUpload []string
	for i, b := range req.Blocks {
		blockIDs[i] = b.SHA256
		probe, perr := h.db.ProbeBlockReuse(orgID, b.SHA256)
		if perr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to verify block"})
			return
		}
		if probe.Decision != db.BlockReuseReusable || !existsMap[b.SHA256] {
			needsUpload = append(needsUpload, b.SHA256)
			continue
		}
		if int64(probe.SizeBytes) != b.Size {
			c.JSON(http.StatusUnprocessableEntity, gin.H{
				"error":  "block size mismatch",
				"sha256": b.SHA256,
			})
			return
		}
		// R8: a block is publishable if it is owned by this session's provisional
		// reference OR already permanently referenced by a committed file (dedup
		// hit the client legitimately skipped). The publish-attempt staging in
		// finalize then pins every block under this commit before the HEAD CAS,
		// so a concurrent rollback of someone else's provisional ref cannot drop
		// liveness for this file.
		if owned, oerr := h.db.BlockHasReferrer(orgID, b.SHA256, referrer); oerr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to verify block ownership"})
			return
		} else if !owned {
			permanent, derr := blockHasPermanentReference(h.db, orgID, b.SHA256)
			if derr != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to verify block ownership"})
				return
			}
			if !permanent {
				needsUpload = append(needsUpload, b.SHA256)
			}
		}
	}
	if len(needsUpload) > 0 {
		c.JSON(http.StatusConflict, gin.H{
			"error":        "some blocks must be (re)uploaded before commit",
			"needs_upload": needsUpload,
		})
		return
	}

	// R7 concurrency: atomically claim the session for this commit. Exactly one
	// concurrent request wins and runs finalize; the others wait for and return
	// the same result, so a double/concurrent commit never creates a duplicate
	// auto-renamed file.
	applied, err := h.db.ClaimBlockUploadSessionForCommit(session.SessionID, digest)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to claim upload session"})
		return
	}
	if !applied {
		// We lost the race (or it was already committed). Return the winner's
		// result if the manifest matches; otherwise it's a different file.
		resultName, ok := h.waitForBlockUploadResult(session.SessionID, digest)
		if ok {
			c.JSON(http.StatusOK, fileFromBlocksResponse(&req, resultName))
			return
		}
		c.JSON(http.StatusConflict, gin.H{"error": "session already committed a different file or commit is still in progress"})
		return
	}

	// Commit the file from the ordered SHA-256 manifest. finalize handles
	// replace/autorename, the logical storage-delta quota (R5), HEAD CAS with
	// retry, and promotion of provisional → permanent block references.
	actualFilename, storageDeltaBytes, storageDeltaFiles, err := h.finalizeStoredUploadMetadata(
		orgID, userID, repoID, req.ParentDir, req.Filename, blockIDs, req.Size, req.Replace)
	if err != nil {
		// Release the claim so the client can retry this exact commit.
		if relErr := h.db.ReleaseBlockUploadSessionCommit(session.SessionID); relErr != nil {
			log.Printf("[CreateFileFromBlocks] WARNING: failed to release commit claim after finalize error: %v", relErr)
		}
		handleStoredUploadMetadataError(h.db, orgID, repoID, session.SessionID, blockIDs, err)
		writeUploadFileError(c, err)
		return
	}

	// Release the session's provisional refs now that permanent fs: refs exist.
	ReleaseUploadedBlockRefs(h.db, orgID, repoID, session.SessionID, blockIDs)

	// R5: traffic was already charged per block at /blocks/upload; only the
	// logical storage delta is accounted here.
	if err := traffic.AdjustStorageCountersByDeltaSync(h.db, orgID, userID, repoID, storageDeltaBytes, storageDeltaFiles); err != nil {
		log.Printf("[CreateFileFromBlocks] failed to update storage counters: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update storage counters"})
		return
	}

	// R7: record the committed result for idempotent replay.
	resultPath := req.ParentDir
	if resultPath == "/" {
		resultPath = "/" + actualFilename
	} else {
		resultPath = req.ParentDir + "/" + actualFilename
	}
	if markErr := h.db.MarkBlockUploadSessionCommitted(session, digest, resultPath, actualFilename, ""); markErr != nil {
		log.Printf("[CreateFileFromBlocks] WARNING: commit succeeded but failed to record session idempotency: %v", markErr)
	}

	c.JSON(http.StatusOK, fileFromBlocksResponse(&req, actualFilename))
}

// fileFromBlocksResponse mirrors the Seafile-compatible upload response shape.
// The file id is the deterministic content-addressed fs_object id derived from
// the manifest (block_ids + size), so it is recomputable on idempotent replay.
func fileFromBlocksResponse(req *fileFromBlocksRequest, actualFilename string) []gin.H {
	blockIDs := make([]string, len(req.Blocks))
	for i, b := range req.Blocks {
		blockIDs[i] = b.SHA256
	}
	fileID, _ := buildFileFSObjectID(blockIDs, req.Size)
	return []gin.H{{
		"name": actualFilename,
		"id":   fileID,
		"size": strconv.FormatInt(req.Size, 10),
	}}
}

// blockHasPermanentReference reports whether a block is kept alive by a
// non-provisional reference (a committed fs_object "fs:" ref or a publish
// attempt "pub:" ref) rather than only a provisional upload ("up:") ref. Used
// to allow committing legitimate cross-file dedup hits the client skipped.
func blockHasPermanentReference(database *db.DB, orgID, blockID string) (bool, error) {
	referrers, err := database.ListBlockReferrers(orgID, blockID)
	if err != nil {
		return false, err
	}
	for _, r := range referrers {
		if !strings.HasPrefix(r, "up:") {
			return true, nil
		}
	}
	return false, nil
}
