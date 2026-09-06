package v2

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/Sesame-Disk/sesamefs/internal/streaming"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	"github.com/gin-gonic/gin"
	"golang.org/x/sync/errgroup"
)

// Per-block commit-readiness classification.
const (
	blockStatusReady = iota
	blockStatusNeedsUpload
	blockStatusSizeMismatch
)

// blockCommitLivenessProvenance records which reference keeps a reusable block
// alive for this commit. BorrowedFS is accepted only after the commit creates
// its own provisional up:<session> liveness and validates the final GC fence.
type blockCommitLivenessProvenance uint8

const (
	blockCommitLivenessNone blockCommitLivenessProvenance = iota
	blockCommitLivenessSessionUpload
	blockCommitLivenessBorrowedFS
)

// commitBlockPlacement carries the canonical physical placement observed by
// ProbeBlockReuse for one distinct ready manifest block, regardless of
// provenance. It is retained for the verification-to-publication handoff
// (own-liveness pin/renewal, then the pre-HEAD exact-placement check); the
// liveness row itself remains block-scoped, as required by the reference
// model. provenance is carried for diagnostics/metrics only -- both
// ensureCommitBlockOwnLiveness and validateCommitBlockPublicationFences
// treat every provenance identically. See
// db.ValidateBorrowedFSPublicationAuthority's doc comment for why the same
// ordering proof that protects a fresh BorrowedFS pin also protects a
// renewed SessionUpload one.
type commitBlockPlacement struct {
	blockID      string
	provenance   blockCommitLivenessProvenance
	storageClass string
	storageKey   string
}

const blockVerifyConcurrency = 20

const (
	blockUploadCommitInProgressCode               = "commit_in_progress"
	blockUploadCommittedDifferentFileConflictCode = "session_committed_different_file"
)

func classifyBlockUploadCommitConflict(session db.BlockUploadSession, found bool, digest string) (resultName, errorCode, errorMessage string) {
	if found {
		if session.ResultFilename != "" && session.ManifestDigest == digest {
			return session.ResultFilename, "", ""
		}
		if session.ManifestDigest != "" && session.ManifestDigest != digest {
			return "", blockUploadCommittedDifferentFileConflictCode, "session already committed a different file"
		}
	}
	return "", blockUploadCommitInProgressCode, "commit still in progress; retry"
}

func committedFileIDFromSession(session db.BlockUploadSession) string {
	fileID := strings.TrimSpace(session.ResultCommitID)
	if isHex40(fileID) {
		return fileID
	}
	return ""
}

// waitForBlockUploadResult polls for the result of a concurrent winner's commit
// on the same session, so a losing/retried request returns the same file
// (idempotency, R7) instead of duplicating it. Bounded (~10s); returns ok=false
// on timeout or if a different manifest won the session. Observes the wait
// duration (finding 8) so operators can see how often losers approach the
// ~10s bound instead of resolving quickly.
func (h *FileHandler) waitForBlockUploadResult(sessionID, digest string) (db.BlockUploadSession, bool) {
	start := time.Now()
	defer func() { metrics.BlockUploadSessionWaitDuration.Observe(time.Since(start).Seconds()) }()
	for i := 0; i < 50; i++ {
		s, ok, err := h.db.GetBlockUploadSession(sessionID)
		if err != nil || !ok {
			return db.BlockUploadSession{}, false
		}
		if s.Committed && s.ManifestDigest != digest {
			return db.BlockUploadSession{}, false
		}
		if s.ResultFilename != "" && s.ManifestDigest == digest {
			return s, true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return db.BlockUploadSession{}, false
}

// Manifest limits for the web content-addressed upload flow (R6). A 131072-block
// manifest at 8 MB/block bounds a single committed file at ~1 TiB, which is far
// beyond any realistic web upload while still rejecting absurd manifests early.
// The total-size cap scales with the configured block size (see validateManifest).
const maxBlocksPerManifest = 131072

// fileFromBlocksBlock is one ordered entry of the commit manifest. The client now
// sends ONLY the SHA-256 (the internal/storage identity: S3 key, blocks row, refs,
// GC, dedup) and the block size. The external Seafile block ID (SHA-1) is no longer
// client-asserted: the server already computed it from the real bytes at UploadBlock
// time and stored it in blocks.sha1, so the commit derives the SHA-1 server-side
// (see ProbeBlockReuse.Sha1) — never trusting a client hash, never minting one. A
// legacy `sha1` field in the JSON is simply ignored.
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
		// The digest is over the true content identity (ordered SHA-256s + sizes).
		// The fs_object's SHA-1 block ids are derived deterministically from these
		// SHA-256s server-side, so they add nothing to the logical identity.
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

func isHex40(s string) bool {
	if len(s) != 40 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// validateManifest enforces the structural rules (R6): fixed blockSize blocks
// except the last, total size consistency, hash format, and manifest bounds. It
// does NOT touch storage — physical/liveness checks happen per block in the
// handler. blockSize is the configured CAS block size (bytes).
func validateManifest(req *fileFromBlocksRequest, blockSize int64) error {
	if blockSize <= 0 {
		blockSize = WebUploadBlockSize
	}
	maxTotalSize := int64(maxBlocksPerManifest) * blockSize
	// The block flow does NOT commit empty (0-byte / 0-block) files by design:
	// files below blockUploadThresholdMB (default 64 MB) never enter this flow — the
	// frontend routes them to the legacy resumable uploader — so an empty-file commit
	// cannot legitimately arrive here. A 0-block or size<=0 manifest is therefore
	// rejected rather than special-cased. (A size=0 declared at session creation is
	// still distinguished from "size missing"; it simply cannot reach a valid commit.)
	if len(req.Blocks) == 0 {
		return fmt.Errorf("blocks is required")
	}
	if len(req.Blocks) > maxBlocksPerManifest {
		return fmt.Errorf("too many blocks (max %d)", maxBlocksPerManifest)
	}
	if req.Size <= 0 || req.Size > maxTotalSize {
		return fmt.Errorf("invalid size")
	}
	if strings.TrimSpace(req.Filename) == "" ||
		strings.ContainsAny(req.Filename, "/\\") {
		return fmt.Errorf("invalid filename")
	}

	var total int64
	lastIdx := len(req.Blocks) - 1
	// R6: repeated blocks are allowed (a file may reference the same content
	// several times), but only when every occurrence of a SHA-256 declares the
	// SAME size. Content determines size, so an honest client always does; a
	// manifest that declares one hash with two sizes is rejected here so a lie
	// cannot survive the later last-wins dedup in sizeByHash and corrupt the
	// committed file's size/offsets.
	sizeSeen := make(map[string]int64, len(req.Blocks))
	for i, b := range req.Blocks {
		if !isHex64(b.SHA256) {
			return fmt.Errorf("block %d: invalid sha256", i)
		}
		b.SHA256 = db.NormalizeBlockID(b.SHA256)
		req.Blocks[i].SHA256 = b.SHA256
		if prev, ok := sizeSeen[b.SHA256]; ok && prev != b.Size {
			return fmt.Errorf("block %d: sha256 %s declared with conflicting sizes (%d and %d)", i, b.SHA256, prev, b.Size)
		}
		sizeSeen[b.SHA256] = b.Size
		if i < lastIdx {
			if b.Size != blockSize {
				return fmt.Errorf("block %d: non-final blocks must be exactly %d bytes", i, blockSize)
			}
		} else {
			if b.Size <= 0 || b.Size > blockSize {
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
// POST /api/v2/repos/:repo_id/file-from-blocks/ (also mounted under /api2/)
func (h *FileHandler) CreateFileFromBlocks(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	if h.config == nil || !h.config.WebUploads.EnableWebBlockUpload {
		c.JSON(http.StatusNotFound, gin.H{"error": "web block upload is not enabled"})
		return
	}
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
	// R7: validate the server-issued session and bind it to this caller + repo
	// BEFORE validating the manifest, so the manifest is checked against the CAS
	// block size FROZEN on the session at creation — not live config. Otherwise a
	// config change during the 48h session could accept blocks at the frozen size
	// in /blocks/upload yet reject the very same manifest here (item 1, frozen
	// admission contract).
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

	commitBlockSize := session.BlockSizeBytes
	if commitBlockSize <= 0 {
		commitBlockSize = h.webBlockUploadBlockSize() // pre-freeze session fallback
	}
	if err := validateManifest(&req, commitBlockSize); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// The session captures the commit intent: it must target the parent_dir it was
	// minted for (defends the session as an intent token, not just an auth scope).
	if normalizePath(session.ParentDir) != req.ParentDir {
		c.JSON(http.StatusConflict, gin.H{"error": "session parent_dir does not match this commit"})
		return
	}
	// If the client declared a size at session creation (fail-fast staging check,
	// item 1), the committed manifest must match it — a cheap extra guard on top of
	// R6's sum(sizes)==size that ties the commit to the declared intent.
	if session.ExpectedSizeDeclared && req.Size != session.ExpectedSize {
		c.JSON(http.StatusConflict, gin.H{"error": "manifest size does not match the size declared at session creation"})
		return
	}

	digest := req.manifestDigest()

	// R7: idempotent replay. A retried commit with the same manifest returns the
	// original result instead of auto-renaming a duplicate.
	if session.Committed {
		if session.ManifestDigest != digest {
			c.JSON(http.StatusConflict, gin.H{
				"error": "session already committed a different file",
				"code":  blockUploadCommittedDifferentFileConflictCode,
			})
			return
		}
		if session.ResultFilename != "" {
			c.JSON(http.StatusOK, fileFromBlocksResponse(&req, session.ResultFilename, committedFileIDFromSession(session)))
			return
		}
		// Claimed but finalize not finished yet (concurrent commit in flight).
		if winner, ok := h.waitForBlockUploadResult(session.SessionID, digest); ok {
			c.JSON(http.StatusOK, fileFromBlocksResponse(&req, winner.ResultFilename, committedFileIDFromSession(winner)))
			return
		}
		c.JSON(http.StatusConflict, gin.H{
			"error": "commit still in progress; retry",
			"code":  blockUploadCommitInProgressCode,
		})
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

	// Resolve the preferred block store only as the single-store fallback. Each
	// block's metadata determines the canonical backend checked below.
	// ProbeBlockReuse proves liveness/governance but not that the S3 object still
	// exists (metadata can outlive a lost object); a commit must require both, or
	// it could publish a file pointing at a missing block.
	blockStore, blockStoreClass, err := h.resolveLibraryBlockStore(c, orgID, repoID)
	if err != nil || blockStore == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "block storage not available"})
		return
	}
	uniqueHashes := make([]string, 0, len(req.Blocks))
	sizeByHash := make(map[string]int64, len(req.Blocks))
	for _, b := range req.Blocks {
		if _, ok := sizeByHash[b.SHA256]; !ok {
			uniqueHashes = append(uniqueHashes, b.SHA256)
		}
		sizeByHash[b.SHA256] = b.Size
	}
	canonicalReader, err := streaming.NewCanonicalBlockCheckReader(c.Request.Context(), h.db, h.storageManager, orgID, uniqueHashes, blockStore, blockStoreClass)
	if err != nil {
		metrics.BlockUploadVerifyErrorsTotal.WithLabelValues("presence").Inc()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve block placement"})
		return
	}
	existsMap, err := canonicalReader.CheckBlocksExist(c.Request.Context(), uniqueHashes, blockVerifyConcurrency)
	if err != nil {
		metrics.BlockUploadVerifyErrorsTotal.WithLabelValues("presence").Inc()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to verify block presence"})
		return
	}

	// R1/R8/R11: per block, never trust S3 existence alone — checked in parallel
	// (bounded concurrency) since a large manifest is thousands of blocks.
	referrer := db.BlockReferrerForUpload(session.SessionID)
	// sha1ByHash256 is the SERVER-derived external SHA-1 per block, read from
	// blocks.sha1 (which UploadBlock wrote from the block's real bytes) via
	// ProbeBlockReuse. The client no longer sends a SHA-1: the server owns it.
	verifyStart := time.Now()
	statuses, sha1ByHash256, placementByHash, err := h.verifyManifestBlocks(c.Request.Context(), orgID, referrer, uniqueHashes, sizeByHash, existsMap)
	if err != nil {
		metrics.BlockUploadVerifyErrorsTotal.WithLabelValues("classify").Inc()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to verify blocks"})
		return
	}

	_, needsUpload, sizeMismatchHash := summarizeBlockVerification(verifyStart, req.Blocks, uniqueHashes, statuses, sha1ByHash256)

	if sizeMismatchHash != "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "block size mismatch", "sha256": sizeMismatchHash})
		return
	}

	if len(needsUpload) > 0 {
		c.JSON(http.StatusConflict, gin.H{
			"error":        "some blocks must be (re)uploaded before commit",
			"needs_upload": needsUpload,
		})
		return
	}

	// This test-only observation point deliberately remains before the own pin:
	// the integration race can therefore model GC winning before a late writer
	// liveness write. Production is a no-op under !integration.
	fileFromBlocksAfterVerifiedBarrier(repoID)
	commitBlocks := orderedCommitBlockPlacements(uniqueHashes, statuses, placementByHash)
	if err := h.ensureCommitBlockOwnLiveness(c.Request.Context(), orgID, repoID, session.SessionID, commitBlocks); err != nil {
		writeCreateFileError(c, err)
		return
	}
	fileFromBlocksAfterBorrowedLivenessBarrier(repoID)

	// externalBlockIDs is the ordered SHA-1 list written into the file fs_object so
	// the desktop/mobile Seafile client can parse and download the file. Each SHA-1
	// is server-derived from blocks.sha1 and validated 40-hex above.
	externalBlockIDs := make([]string, len(req.Blocks))
	for i, b := range req.Blocks {
		externalBlockIDs[i] = sha1ByHash256[b.SHA256]
	}

	// R7 concurrency: atomically claim the session for this commit. Exactly one
	// concurrent request wins and runs finalize; the others wait for and return
	// the same result, so a double/concurrent commit never creates a duplicate
	// auto-renamed file. Claim outcomes are counted (finding 8) so operators can
	// see how often concurrent commits actually contend on a session.
	applied, err := h.db.ClaimBlockUploadSessionForCommit(session.SessionID, digest)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to claim upload session"})
		return
	}
	if !applied {
		// We lost the race (or it was already committed). Return the winner's
		// result if the manifest matches; otherwise it's a different file.
		winner, ok := h.waitForBlockUploadResult(session.SessionID, digest)
		if ok {
			metrics.BlockUploadSessionClaimTotal.WithLabelValues("lost_result_ready").Inc()
			c.JSON(http.StatusOK, fileFromBlocksResponse(&req, winner.ResultFilename, committedFileIDFromSession(winner)))
			return
		}
		current, found, readErr := h.db.GetBlockUploadSession(session.SessionID)
		if readErr == nil {
			if resultName, code, message := classifyBlockUploadCommitConflict(current, found, digest); resultName != "" {
				metrics.BlockUploadSessionClaimTotal.WithLabelValues("lost_result_ready").Inc()
				c.JSON(http.StatusOK, fileFromBlocksResponse(&req, resultName, committedFileIDFromSession(current)))
				return
			} else {
				metrics.BlockUploadSessionClaimTotal.WithLabelValues("lost_conflict").Inc()
				c.JSON(http.StatusConflict, gin.H{
					"error": message,
					"code":  code,
				})
				return
			}
		}
		metrics.BlockUploadSessionClaimTotal.WithLabelValues("lost_timeout").Inc()
		c.JSON(http.StatusConflict, gin.H{
			"error": "commit still in progress; retry",
			"code":  blockUploadCommitInProgressCode,
		})
		return
	}
	metrics.BlockUploadSessionClaimTotal.WithLabelValues("won").Inc()

	// Commit the file from the ordered manifest. finalize handles
	// replace/autorename, the logical storage-delta quota (R5), HEAD CAS with
	// retry, and promotion of provisional → permanent block references. It is
	// given the SHA-1 (external) IDs: those derive the fs_object's id and are
	// stored in fs_objects.seafile_block_ids_sha1 for desktop compat, while
	// fs_objects.block_ids stays the SHA-256 canonical id (post PR1–PR5, see
	// docs/WEB-BLOCK-UPLOAD.md); either direction resolves through the mappings
	// UploadBlock already wrote from verified bytes (no mapping is minted here).
	actualFilename, storageDeltaBytes, storageDeltaFiles, err := h.finalizeStoredUploadMetadata(
		orgID, userID, repoID, req.ParentDir, req.Filename, externalBlockIDs, req.Size, req.Replace, commitBlocks)
	if err != nil {
		// Release the claim so the client can retry this exact commit.
		if relErr := h.db.ReleaseBlockUploadSessionCommit(session.SessionID); relErr != nil {
			log.Printf("[CreateFileFromBlocks] WARNING: failed to release commit claim after finalize error: %v", relErr)
		}
		// The session's provisional up: references remain until their Cassandra
		// TTL; Phase 0 handles delayed cleanup after failed publication.
		writeUploadFileError(c, err)
		return
	}

	// R7: the file is published — record the idempotent result IMMEDIATELY, before
	// any further best-effort work. If we waited until after the counter update and
	// that failed, the session would be left committed=true with no ResultFilename,
	// and every retry would hang on "commit still in progress" even though the file
	// exists. Persisting first makes a retry return the same file deterministically.
	resultPath := req.ParentDir
	if resultPath == "/" {
		resultPath = "/" + actualFilename
	} else {
		resultPath = req.ParentDir + "/" + actualFilename
	}
	// Persist with bounded retries. If this row is never written the session stays
	// committed=true (from the LWT claim) WITHOUT a result, so every retry would get
	// "commit still in progress" until the TTL even though the file already exists.
	// The write is an idempotent single-row INSERT, so retrying is safe; this closes
	// all but a pathological sustained-Cassandra-outage window (documented in
	// docs/WEB-BLOCK-UPLOAD.md as a deferred edge).
	var markErr error
	fileID, fileIDErr := buildFileFSObjectID(externalBlockIDs, req.Size)
	if fileIDErr != nil {
		log.Printf("[CreateFileFromBlocks] WARNING: failed to derive committed fs_object id for session result (filename=%q size=%d): %v", actualFilename, req.Size, fileIDErr)
		fileID = ""
	}
	for attempt := 0; attempt < 3; attempt++ {
		if markErr = h.db.MarkBlockUploadSessionCommitted(session, digest, resultPath, actualFilename, fileID); markErr == nil {
			break
		}
		time.Sleep(time.Duration(attempt+1) * 100 * time.Millisecond)
	}
	if markErr != nil {
		log.Printf("[CreateFileFromBlocks] WARNING: commit succeeded but failed to record session idempotency after retries: %v", markErr)
	}

	// Free the session's staging caps (per-user slot) now that it is committed —
	// BEST-EFFORT and strictly AFTER the critical idempotency write above, never
	// coupled to it: a cleanup failure must not make a committed file look
	// uncommitted. A lingering slot self-expires at the session TTL (fail-safe).
	if err := h.db.CleanupCommittedBlockUploadSessionCaps(session); err != nil {
		log.Printf("[CreateFileFromBlocks] WARNING: committed but failed to release session staging slot (self-expires at TTL): %v", err)
	}

	// The session's provisional up: refs deliberately remain until Cassandra TTL.
	// Permanent fs: refs now carry published liveness independently.

	// R5: traffic was already charged per block at /blocks/upload; only the
	// logical storage delta is accounted here. Best-effort: the file is already
	// published, so a counter failure must NOT fail the request (it would falsely
	// tell the client the upload failed). Counters are reconcilable out of band.
	if err := traffic.AdjustStorageCountersByDeltaSync(h.db, orgID, userID, repoID, storageDeltaBytes, storageDeltaFiles); err != nil {
		log.Printf("[CreateFileFromBlocks] WARNING: file committed but failed to update storage counters: %v", err)
	}

	c.JSON(http.StatusOK, fileFromBlocksResponse(&req, actualFilename, fileID))
}

// fileFromBlocksResponse mirrors the Seafile-compatible upload response shape.
// The file id must match the published fs_object id. Callers should therefore
// pass the committed/stored fs_id, not a best-effort recomputation.
func fileFromBlocksResponse(req *fileFromBlocksRequest, actualFilename, fileID string) []gin.H {
	return []gin.H{{
		"name": actualFilename,
		"id":   fileID,
		"size": strconv.FormatInt(req.Size, 10),
	}}
}

// observeBlockVerification records the commit's per-block verification pass
// duration and per-status counts (finding 8). Called only after a successful
// pass — an infrastructure error (caller returns 500 before this runs) is not
// a verification outcome to report a duration for. needsUpload contains any
// extra fail-closed rejections that happened after the primary classifier, such
// as a malformed server-derived SHA-1.
func observeBlockVerification(start time.Time, uniqueHashes []string, statuses map[string]int, forcedNeedsUpload map[string]struct{}) {
	result := "ready"
	var ready, needsUpload, sizeMismatch int
	for _, hash := range uniqueHashes {
		if _, forced := forcedNeedsUpload[hash]; forced {
			needsUpload++
			continue
		}
		switch statuses[hash] {
		case blockStatusReady:
			ready++
		case blockStatusSizeMismatch:
			sizeMismatch++
		default: // blockStatusNeedsUpload
			needsUpload++
		}
	}
	if needsUpload > 0 || sizeMismatch > 0 {
		result = "needs_upload"
	}
	metrics.BlockUploadVerifyDuration.WithLabelValues(result).Observe(time.Since(start).Seconds())
	if ready > 0 {
		metrics.BlockUploadVerifyBlocksTotal.WithLabelValues("ready").Add(float64(ready))
	}
	if needsUpload > 0 {
		metrics.BlockUploadVerifyBlocksTotal.WithLabelValues("needs_upload").Add(float64(needsUpload))
	}
	if sizeMismatch > 0 {
		metrics.BlockUploadVerifyBlocksTotal.WithLabelValues("size_mismatch").Add(float64(sizeMismatch))
	}
}

// verifyManifestBlocks classifies every distinct manifest block for commit
// readiness with bounded concurrency (a large manifest is thousands of blocks,
// so a sequential probe-per-block would be thousands of serial round-trips).
// Returns a hard error only on infrastructure failure (caller should 500).
//
// The returned placement map covers EVERY ready block regardless of
// provenance (SessionUpload as well as BorrowedFS): classifyBlockForCommit
// already resolves the canonical BlockPhysicalLocation for both, so retaining
// it here costs nothing extra -- it is what lets the pre-HEAD exact-placement
// check (validateCommitBlockPublicationFences) cover the full commit, not
// just its BorrowedFS subset.
func (h *FileHandler) verifyManifestBlocks(ctx context.Context, orgID, referrer string, uniqueHashes []string, sizeByHash map[string]int64, existsMap map[string]bool) (map[string]int, map[string]string, map[string]commitBlockPlacement, error) {
	result := make(map[string]int, len(uniqueHashes))
	sha1ByHash := make(map[string]string, len(uniqueHashes))
	placementByHash := make(map[string]commitBlockPlacement)
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	sem := make(chan struct{}, blockVerifyConcurrency)
	for _, hash := range uniqueHashes {
		hash := hash
		g.Go(func() error {
			select {
			case sem <- struct{}{}:
			case <-gctx.Done():
				return gctx.Err()
			}
			defer func() { <-sem }()
			status, sha1, provenance, location, err := h.classifyBlockForCommit(orgID, referrer, hash, sizeByHash[hash], existsMap[hash])
			if err != nil {
				return err
			}
			mu.Lock()
			result[hash] = status
			sha1ByHash[hash] = sha1
			if status == blockStatusReady {
				placementByHash[hash] = commitBlockPlacement{
					blockID:      hash,
					provenance:   provenance,
					storageClass: location.StorageClass,
					storageKey:   location.StorageKey,
				}
			}
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, nil, nil, err
	}
	return result, sha1ByHash, placementByHash, nil
}

// orderedCommitBlockPlacements turns the distinct manifest order into the
// exact set of ready blocks that need own-liveness/exact-placement
// protection. Repeated manifest entries therefore produce one liveness write,
// not one write per file occurrence.
func orderedCommitBlockPlacements(uniqueHashes []string, statuses map[string]int, placementByHash map[string]commitBlockPlacement) []commitBlockPlacement {
	placements := make([]commitBlockPlacement, 0, len(placementByHash))
	for _, hash := range uniqueHashes {
		if statuses[hash] != blockStatusReady {
			continue
		}
		if block, ok := placementByHash[hash]; ok {
			placements = append(placements, block)
		}
	}
	return placements
}

// ensureCommitBlockOwnLiveness makes the writer's liveness explicit, for
// every distinct ready block in this commit, before it can stage
// publication. For a BorrowedFS block this creates a new up:<session> pin
// (unchanged from W1). For a SessionUpload block this upserts the SAME
// up:<session> identity RegisterUploadedBlockTarget wrote at /blocks/upload
// time (db.BlockReferrerForUpload(sessionID)) via
// db.AddProvisionalBlockReferenceWithExpiry, which is a plain upsert with no
// existence check: it RENEWS the deadline when that reference is still
// present, and RECREATES it when the original has already lapsed. Either way
// this is an idempotent overwrite of one Cassandra key, never a second
// semantic pin, and recreating it here does NOT by itself revoke any delete
// GC already committed against the block's prior placement -- that safety
// comes from validateCommitBlockPublicationFences re-validating the exact
// placement immediately before HEAD. A single expiry is shared by this
// commit's blocks and the writes remain bounded/concurrent like verification.
func (h *FileHandler) ensureCommitBlockOwnLiveness(ctx context.Context, orgID, libraryID, sessionID string, blocks []commitBlockPlacement) error {
	if len(blocks) == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	referrer := db.BlockReferrerForUpload(sessionID)
	expiresAt := time.Now().UTC().Add(time.Duration(db.ProvisionalBlockReferenceTTLSeconds) * time.Second)
	fsHelper := NewFSHelper(h.db)
	g, gctx := errgroup.WithContext(ctx)
	sem := make(chan struct{}, blockVerifyConcurrency)
	seen := make(map[string]struct{}, len(blocks))
	uniqueBlocks := make([]commitBlockPlacement, 0, len(blocks))
	for _, block := range blocks {
		if _, ok := seen[block.blockID]; ok {
			continue
		}
		seen[block.blockID] = struct{}{}
		uniqueBlocks = append(uniqueBlocks, block)
	}
	for _, block := range uniqueBlocks {
		if strings.TrimSpace(block.storageClass) == "" || strings.TrimSpace(block.storageKey) != block.storageKey || block.storageKey == "" {
			return fmt.Errorf("%w: incomplete canonical placement for %s", db.ErrBlockMetadataPermanent, block.blockID)
		}
		block := block
		g.Go(func() error {
			select {
			case sem <- struct{}{}:
			case <-gctx.Done():
				return gctx.Err()
			}
			defer func() { <-sem }()
			if err := registerUploadedBlockAddProvisionalRefFn(fsHelper, orgID, block.blockID, referrer, libraryID, block.storageClass, expiresAt); err != nil {
				if errors.Is(err, db.ErrBlockMetadataPermanent) {
					return fmt.Errorf("add own liveness for %s: %w", block.blockID, err)
				}
				return fmt.Errorf("%w: add own liveness for %s: %w", ErrBlockMaterializationTransient, block.blockID, err)
			}
			return nil
		})
	}
	return g.Wait()
}

// validateCommitBlockPublicationFences is the final writer-side check, run
// immediately before HEAD CAS, over EVERY distinct ready block in this
// commit (both SessionUpload and BorrowedFS). The GC-side destructive proof
// remains BlockHasReferencesGlobal at EACH_QUORUM.
//
// A fence-only check ("does GC currently claim or orphan this block?") cannot
// see the terminal state where GC has ALREADY fully retired the observed
// placement between this commit's earlier verification and this call: once
// FinalizeBlockDelete has removed the canonical row and the orphan has
// settled (DeleteS3Orphan), there is no gc_state='deleting' row and no
// orphan row left to report a fence, yet the exact physical placement this
// commit is about to publish against no longer exists. Re-validating the
// exact observed (storage_class, storage_key) via
// ValidateBorrowedFSPublicationAuthority closes that gap: its Blocked outcome
// reports an active claim or a pending orphan (a strict superset of the old
// fence-only check), and its Changed outcome additionally reports a canonical
// row that is now absent or now names a different placement.
//
// This reads at LOCAL_QUORUM (BlockAuthorityAdvisory), not SERIAL: unlike the
// pre-PUT repair boundary (ValidateBlockRepairAuthority), safety here does not
// come from a downstream CAS -- it comes from the caller having already
// durably written (or, for SessionUpload, renewed) its own up:<session> pin
// before this runs. See db.ValidateBorrowedFSPublicationAuthority for the
// full four-case ordering proof -- do not duplicate it here. Paying a
// Paxos round trip per block on this dedup hot path would buy nothing that
// ordering does not already give.
func (h *FileHandler) validateCommitBlockPublicationFences(orgID string, blocks []commitBlockPlacement) error {
	if len(blocks) == 0 {
		return nil
	}
	g, gctx := errgroup.WithContext(context.Background())
	sem := make(chan struct{}, blockVerifyConcurrency)
	for _, block := range blocks {
		block := block
		g.Go(func() error {
			select {
			case sem <- struct{}{}:
			case <-gctx.Done():
				return gctx.Err()
			}
			defer func() { <-sem }()
			outcome, err := validateBorrowedFSPublicationAuthorityFn(h.db, orgID, block.blockID, db.BlockPhysicalLocation{
				StorageClass: block.storageClass,
				StorageKey:   block.storageKey,
			})
			switch outcome {
			case db.BlockRepairAuthorityAuthorized:
				return nil
			case db.BlockRepairAuthorityBlocked, db.BlockRepairAuthorityChanged:
				return fmt.Errorf("%w: block %s is no longer safe to publish against: %w", ErrBlockDeleteInProgress, block.blockID, err)
			case db.BlockRepairAuthorityPermanent:
				return fmt.Errorf("validate exact physical authority for %s: %w", block.blockID, err)
			default:
				return fmt.Errorf("%w: validate exact physical authority for %s: %w", ErrBlockMaterializationTransient, block.blockID, err)
			}
		})
	}
	return g.Wait()
}

// summarizeBlockVerification translates the raw per-distinct-block verification
// statuses into the ordered handler outcome, including fail-closed SHA-1
// downgrades. It ALWAYS emits verification metrics before returning so
// size-mismatch exits still show up in observability.
func summarizeBlockVerification(start time.Time, blocks []fileFromBlocksBlock, uniqueHashes []string, statuses map[string]int, sha1ByHash256 map[string]string) ([]string, []string, string) {
	// blockIDs is the ordered SHA-256 list (verification, refs, GC, quota).
	blockIDs := make([]string, len(blocks))
	var needsUpload []string
	var sizeMismatchHash string
	needsSeen := make(map[string]struct{})
	addNeedsUpload := func(sha256ID string) {
		if _, ok := needsSeen[sha256ID]; !ok {
			needsSeen[sha256ID] = struct{}{}
			needsUpload = append(needsUpload, sha256ID)
		}
	}
	for i, b := range blocks {
		blockIDs[i] = b.SHA256
		switch statuses[b.SHA256] {
		case blockStatusSizeMismatch:
			if sizeMismatchHash == "" {
				sizeMismatchHash = b.SHA256
			}
		case blockStatusNeedsUpload:
			addNeedsUpload(b.SHA256)
		}
	}

	// Fail-closed validation of the SERVER-derived SHA-1 (blocks.sha1). For a block
	// to be committed, the server must hold a well-formed 40-hex SHA-1 for it; that
	// SHA-1 was written from the block's real bytes at UploadBlock time. A ready
	// block whose blocks.sha1 is missing or malformed is treated as needs_upload —
	// the re-upload recomputes and rewrites a verified SHA-1 — so a half-written or
	// pre-PR2 block can never put an unvalidated id into an fs_object.
	for _, sha256ID := range uniqueHashes {
		if statuses[sha256ID] != blockStatusReady {
			continue
		}
		if !isHex40(sha1ByHash256[sha256ID]) {
			addNeedsUpload(sha256ID)
		}
	}
	observeBlockVerification(start, uniqueHashes, statuses, needsSeen)
	return blockIDs, needsUpload, sizeMismatchHash
}

// classifyBlockForCommit decides whether one block is commit-ready (R1/R8/R11):
// live (ProbeBlockReuse == Reusable), physically present, at the declared size,
// and backed by a currently accepted liveness provenance.
func (h *FileHandler) classifyBlockForCommit(orgID, referrer, hash string, declaredSize int64, exists bool) (int, string, blockCommitLivenessProvenance, db.BlockPhysicalLocation, error) {
	probe, err := h.db.ProbeBlockReuse(orgID, hash)
	if err != nil {
		return 0, "", blockCommitLivenessNone, db.BlockPhysicalLocation{}, err
	}
	sha1 := strings.TrimSpace(probe.Sha1)
	if probe.Decision != db.BlockReuseReusable || !exists {
		return blockStatusNeedsUpload, sha1, blockCommitLivenessNone, db.BlockPhysicalLocation{}, nil
	}
	if int64(probe.SizeBytes) != declaredSize {
		return blockStatusSizeMismatch, sha1, blockCommitLivenessNone, db.BlockPhysicalLocation{}, nil
	}
	provenance, err := classifyBlockOwnership(h.db, orgID, referrer, hash)
	if err != nil {
		return 0, "", blockCommitLivenessNone, db.BlockPhysicalLocation{}, err
	}
	if blockCommitProvenanceCurrentlyReady(provenance) {
		return blockStatusReady, sha1, provenance, db.BlockPhysicalLocation{
			StorageClass: probe.StorageClass,
			StorageKey:   probe.StorageKey,
		}, nil
	}
	return blockStatusNeedsUpload, sha1, blockCommitLivenessNone, db.BlockPhysicalLocation{}, nil
}

// classifyBlockReferrerProvenance classifies the reference provenance for one
// commit without performing I/O. The session's exact up: reference has
// precedence over any fs: reference so the result is independent of Cassandra
// row order.
func classifyBlockReferrerProvenance(referrers []string, sessionReferrer string) blockCommitLivenessProvenance {
	hasBorrowedFS := false
	for _, referrer := range referrers {
		if referrer == sessionReferrer {
			return blockCommitLivenessSessionUpload
		}
		if strings.HasPrefix(referrer, "fs:") {
			hasBorrowedFS = true
		}
	}
	if hasBorrowedFS {
		return blockCommitLivenessBorrowedFS
	}
	return blockCommitLivenessNone
}

// blockCommitProvenanceCurrentlyReady preserves the existing admission policy.
// BorrowedFS remains accepted because CreateFileFromBlocks immediately upgrades
// it to own liveness before claiming the session.
func blockCommitProvenanceCurrentlyReady(provenance blockCommitLivenessProvenance) bool {
	return provenance == blockCommitLivenessSessionUpload ||
		provenance == blockCommitLivenessBorrowedFS
}

// classifyBlockOwnership reports the liveness provenance for a block: this
// session's own provisional reference, a borrowed fs: reference
// ("fs:<library>:<fs_id>" prefix), or no accepted reference. It deliberately
// does NOT count a foreign publish-attempt ref ("pub:<attempt>") as permanent:
// that ref is transient and disappears if the foreign attempt loses its HEAD
// CAS and cleans up, which would leave this commit pointing at a block that can
// be GC'd.
//
// This reads the block's reference partition ONCE via ListBlockReferrers
// (finding 14): the previous version ran BlockHasReferrer (exact-match read)
// first and, only on a miss, a second ListBlockReferrers (full-partition read)
// to check for a permanent ref — two queries in the common "not owned by this
// session, but permanently referenced" case. ListBlockReferrers already
// returns every referrer, a superset of what BlockHasReferrer checks, so a
// single partition read now answers both questions.
func classifyBlockOwnership(database *db.DB, orgID, referrer, blockID string) (blockCommitLivenessProvenance, error) {
	referrers, err := database.ListBlockReferrers(orgID, blockID)
	if err != nil {
		return blockCommitLivenessNone, err
	}
	return classifyBlockReferrerProvenance(referrers, referrer), nil
}
