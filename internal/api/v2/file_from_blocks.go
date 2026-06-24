package v2

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
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

// fileFromBlocksBlock is one ordered entry of the commit manifest. It carries
// BOTH content hashes of the same 8 MB block:
//   - SHA256: the internal/storage identity (S3 key, blocks row, refs, GC, dedup).
//   - SHA1:   the EXTERNAL Seafile block ID written into the file fs_object so the
//     desktop/mobile sync client (which requires 40-hex SHA-1 block IDs) can parse
//     and download the file. UploadBlock writes the verified SHA-1->SHA-256
//     mapping, and the commit validates that forward mapping before using the
//     manifest SHA-1 in the fs_object.
//
// Both hashes derive from the same bytes, so for any honest client a given SHA256
// always pairs with the same SHA1 (enforced in validateManifest).
type fileFromBlocksBlock struct {
	SHA1   string `json:"sha1"`
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
		// SHA-1 is part of the committed file's logical identity: the fs_object
		// stores SHA-1 block IDs, so its id derives from them. Including SHA-1 here
		// makes a replay with the same SHA-256s but a different SHA-1 a "different
		// file" (409) instead of a silent mismatch between the returned id and the
		// persisted object.
		fmt.Fprintf(h, "%s:%s:%d\n", b.SHA1, b.SHA256, b.Size)
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
	// R6: repeated blocks are allowed (a file may reference the same content
	// several times), but only when every occurrence of a SHA-256 declares the
	// SAME size. Content determines size, so an honest client always does; a
	// manifest that declares one hash with two sizes is rejected here so a lie
	// cannot survive the later last-wins dedup in sizeByHash and corrupt the
	// committed file's size/offsets.
	sizeSeen := make(map[string]int64, len(req.Blocks))
	// SHA-1 is the external Seafile block ID; SHA-256 is the storage identity.
	// Both derive from the same bytes, so the pairing must be 1:1 across the whole
	// manifest. A manifest that maps one SHA-256 to two SHA-1s (or vice versa) is a
	// lie that would corrupt the SHA-1→SHA-256 mapping the desktop download relies
	// on, so it is rejected here before any mapping row is written.
	sha1BySha256 := make(map[string]string, len(req.Blocks))
	sha256BySha1 := make(map[string]string, len(req.Blocks))
	for i, b := range req.Blocks {
		if !isHex64(b.SHA256) {
			return fmt.Errorf("block %d: invalid sha256", i)
		}
		if !isHex40(b.SHA1) {
			return fmt.Errorf("block %d: invalid sha1", i)
		}
		if prev, ok := sha1BySha256[b.SHA256]; ok && prev != b.SHA1 {
			return fmt.Errorf("block %d: sha256 %s mapped to conflicting sha1 (%s and %s)", i, b.SHA256, prev, b.SHA1)
		}
		if prev, ok := sha256BySha1[b.SHA1]; ok && prev != b.SHA256 {
			return fmt.Errorf("block %d: sha1 %s mapped to conflicting sha256 (%s and %s)", i, b.SHA1, prev, b.SHA256)
		}
		sha1BySha256[b.SHA256] = b.SHA1
		sha256BySha1[b.SHA1] = b.SHA256
		if prev, ok := sizeSeen[b.SHA256]; ok && prev != b.Size {
			return fmt.Errorf("block %d: sha256 %s declared with conflicting sizes (%d and %d)", i, b.SHA256, prev, b.Size)
		}
		sizeSeen[b.SHA256] = b.Size
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
	// The session captures the commit intent: it must target the parent_dir it was
	// minted for (defends the session as an intent token, not just an auth scope).
	if normalizePath(session.ParentDir) != req.ParentDir {
		c.JSON(http.StatusConflict, gin.H{"error": "session parent_dir does not match this commit"})
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
			c.JSON(http.StatusOK, fileFromBlocksResponse(&req, session.ResultFilename))
			return
		}
		// Claimed but finalize not finished yet (concurrent commit in flight).
		if name, ok := h.waitForBlockUploadResult(session.SessionID, digest); ok {
			c.JSON(http.StatusOK, fileFromBlocksResponse(&req, name))
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
	sizeByHash := make(map[string]int64, len(req.Blocks))
	// sha1ByHash is the client's CLAIMED SHA-1 per block. It is only cross-checked
	// against the server-verified mapping below (never trusted to write one), so a
	// forged manifest SHA-1 is rejected rather than persisted.
	sha1ByHash := make(map[string]string, len(req.Blocks))
	for _, b := range req.Blocks {
		if _, ok := sizeByHash[b.SHA256]; !ok {
			uniqueHashes = append(uniqueHashes, b.SHA256)
		}
		sizeByHash[b.SHA256] = b.Size
		sha1ByHash[b.SHA256] = b.SHA1
	}
	existsMap, err := blockStore.CheckBlocksParallel(c.Request.Context(), uniqueHashes, blockVerifyConcurrency)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to verify block presence"})
		return
	}

	// R1/R8/R11: per block, never trust S3 existence alone — checked in parallel
	// (bounded concurrency) since a large manifest is thousands of blocks.
	referrer := db.BlockReferrerForUpload(session.SessionID)
	statuses, err := h.verifyManifestBlocks(c.Request.Context(), orgID, referrer, uniqueHashes, sizeByHash, existsMap)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to verify blocks"})
		return
	}

	// blockIDs is the ordered SHA-256 list (verification, refs, GC, quota).
	blockIDs := make([]string, len(req.Blocks))
	var needsUpload []string
	needsSeen := make(map[string]struct{})
	addNeedsUpload := func(sha256ID string) {
		if _, ok := needsSeen[sha256ID]; !ok {
			needsSeen[sha256ID] = struct{}{}
			needsUpload = append(needsUpload, sha256ID)
		}
	}
	for i, b := range req.Blocks {
		blockIDs[i] = b.SHA256
		switch statuses[b.SHA256] {
		case blockStatusSizeMismatch:
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "block size mismatch", "sha256": b.SHA256})
			return
		case blockStatusNeedsUpload:
			addNeedsUpload(b.SHA256)
		}
	}

	// Validate the manifest's SHA-1 against the FORWARD mapping (sha1 -> sha256)
	// that UploadBlock wrote from the block's REAL bytes. The forward table is the
	// authoritative, client-visible row (download resolution uses it too); the
	// reverse table is only a GC/repair projection and is NOT used here because its
	// schema allows several SHA-1 aliases per SHA-256 and a reverse row can lag a
	// forward one. The server never trusts the client's SHA-1 blindly, but it also
	// never invents one: a client SHA-1 is accepted only if the forward mapping
	// proves it resolves to the declared SHA-256.
	//
	// The lookups run in bounded parallel (like the block-presence/liveness checks
	// above), since a large manifest is thousands of blocks; the decisions below are
	// then applied in manifest order so the needs_upload list and the first 400 are
	// deterministic.
	//   - forward missing        -> needs_upload (re-upload writes the verified row)
	//   - forward != declared    -> 400 (SHA-1 does not belong to this content)
	//   - forward == declared    -> accept the manifest SHA-1 for the fs_object
	forwardMappings, err := h.resolveManifestForwardMappings(c.Request.Context(), orgID, uniqueHashes, sha1ByHash, statuses)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve block id mapping"})
		return
	}
	for _, sha256ID := range uniqueHashes {
		if statuses[sha256ID] != blockStatusReady {
			continue
		}
		internal, found := forwardMappings[sha256ID]
		if !found {
			addNeedsUpload(sha256ID)
			continue
		}
		if internal != sha256ID {
			c.JSON(http.StatusBadRequest, gin.H{"error": "manifest sha1 does not match the block content", "sha256": sha256ID})
			return
		}
	}

	if len(needsUpload) > 0 {
		c.JSON(http.StatusConflict, gin.H{
			"error":        "some blocks must be (re)uploaded before commit",
			"needs_upload": needsUpload,
		})
		return
	}

	// externalBlockIDs is the ordered SHA-1 list written into the file fs_object so
	// the desktop/mobile Seafile client can parse and download the file. The
	// manifest SHA-1 is safe to use here: the forward-mapping check above proved
	// each one resolves to its declared SHA-256.
	externalBlockIDs := make([]string, len(req.Blocks))
	for i, b := range req.Blocks {
		externalBlockIDs[i] = b.SHA1
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
		current, found, readErr := h.db.GetBlockUploadSession(session.SessionID)
		if readErr == nil {
			if resultName, code, message := classifyBlockUploadCommitConflict(current, found, digest); resultName != "" {
				c.JSON(http.StatusOK, fileFromBlocksResponse(&req, resultName))
				return
			} else {
				c.JSON(http.StatusConflict, gin.H{
					"error": message,
					"code":  code,
				})
				return
			}
		}
		c.JSON(http.StatusConflict, gin.H{
			"error": "commit still in progress; retry",
			"code":  blockUploadCommitInProgressCode,
		})
		return
	}

	// Commit the file from the ordered manifest. finalize handles
	// replace/autorename, the logical storage-delta quota (R5), HEAD CAS with
	// retry, and promotion of provisional → permanent block references. It is given
	// the SHA-1 (external) IDs: those go into the fs_object and derive its id, and
	// stage/promote resolve them back to SHA-256 via the mappings UploadBlock
	// already wrote from verified bytes (no mapping is minted here).
	actualFilename, storageDeltaBytes, storageDeltaFiles, err := h.finalizeStoredUploadMetadata(
		orgID, userID, repoID, req.ParentDir, req.Filename, externalBlockIDs, req.Size, req.Replace)
	if err != nil {
		// Release the claim so the client can retry this exact commit.
		if relErr := h.db.ReleaseBlockUploadSessionCommit(session.SessionID); relErr != nil {
			log.Printf("[CreateFileFromBlocks] WARNING: failed to release commit claim after finalize error: %v", relErr)
		}
		handleStoredUploadMetadataError(h.db, orgID, repoID, session.SessionID, blockIDs, err)
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
	for attempt := 0; attempt < 3; attempt++ {
		if markErr = h.db.MarkBlockUploadSessionCommitted(session, digest, resultPath, actualFilename, ""); markErr == nil {
			break
		}
		time.Sleep(time.Duration(attempt+1) * 100 * time.Millisecond)
	}
	if markErr != nil {
		log.Printf("[CreateFileFromBlocks] WARNING: commit succeeded but failed to record session idempotency after retries: %v", markErr)
	}

	// Release the session's provisional refs now that permanent fs: refs exist.
	ReleaseUploadedBlockRefs(h.db, orgID, repoID, session.SessionID, blockIDs)

	// R5: traffic was already charged per block at /blocks/upload; only the
	// logical storage delta is accounted here. Best-effort: the file is already
	// published, so a counter failure must NOT fail the request (it would falsely
	// tell the client the upload failed). Counters are reconcilable out of band.
	if err := traffic.AdjustStorageCountersByDeltaSync(h.db, orgID, userID, repoID, storageDeltaBytes, storageDeltaFiles); err != nil {
		log.Printf("[CreateFileFromBlocks] WARNING: file committed but failed to update storage counters: %v", err)
	}

	c.JSON(http.StatusOK, fileFromBlocksResponse(&req, actualFilename))
}

// fileFromBlocksResponse mirrors the Seafile-compatible upload response shape.
// The file id is the deterministic content-addressed fs_object id derived from
// the manifest (block_ids + size), so it is recomputable on idempotent replay.
// The fs_object stores the SHA-1 (external) block IDs, so the id MUST be derived
// from those to match the persisted fs_id.
func fileFromBlocksResponse(req *fileFromBlocksRequest, actualFilename string) []gin.H {
	blockIDs := make([]string, len(req.Blocks))
	for i, b := range req.Blocks {
		blockIDs[i] = b.SHA1
	}
	fileID, err := buildFileFSObjectID(blockIDs, req.Size)
	if err != nil {
		// The file is already committed; the response id is derived, not load-bearing
		// for storage. Log so a (rare) marshaling/input failure is debuggable instead
		// of silently returning an empty id.
		log.Printf("[CreateFileFromBlocks] WARNING: failed to derive fs_object id for response (filename=%q size=%d): %v", actualFilename, req.Size, err)
	}
	return []gin.H{{
		"name": actualFilename,
		"id":   fileID,
		"size": strconv.FormatInt(req.Size, 10),
	}}
}

// verifyManifestBlocks classifies every distinct manifest block for commit
// readiness with bounded concurrency (a large manifest is thousands of blocks,
// so a sequential probe-per-block would be thousands of serial round-trips).
// Returns a hard error only on infrastructure failure (caller should 500).
func (h *FileHandler) verifyManifestBlocks(ctx context.Context, orgID, referrer string, uniqueHashes []string, sizeByHash map[string]int64, existsMap map[string]bool) (map[string]int, error) {
	result := make(map[string]int, len(uniqueHashes))
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
			status, err := h.classifyBlockForCommit(orgID, referrer, hash, sizeByHash[hash], existsMap[hash])
			if err != nil {
				return err
			}
			mu.Lock()
			result[hash] = status
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return result, nil
}

// resolveManifestForwardMappings looks up, in bounded parallel, the FORWARD
// SHA-1 -> SHA-256 mapping row for each ready block's claimed SHA-1. Only blocks
// whose status is blockStatusReady are queried (a needs_upload block is re-uploaded
// regardless, which rewrites the verified mapping). The returned map is keyed by
// SHA-256; a key being ABSENT means "no forward mapping row" (caller treats it as
// needs_upload), while a present value is the internal SHA-256 the SHA-1 resolves
// to (caller compares it against the declared SHA-256). Concurrency matches the
// block-presence checks so a multi-thousand-block manifest does not serialize a
// point read per block.
func (h *FileHandler) resolveManifestForwardMappings(ctx context.Context, orgID string, uniqueHashes []string, sha1ByHash map[string]string, statuses map[string]int) (map[string]string, error) {
	result := make(map[string]string, len(uniqueHashes))
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	sem := make(chan struct{}, blockVerifyConcurrency)
	for _, sha256ID := range uniqueHashes {
		if statuses[sha256ID] != blockStatusReady {
			continue
		}
		sha256ID := sha256ID
		g.Go(func() error {
			select {
			case sem <- struct{}{}:
			case <-gctx.Done():
				return gctx.Err()
			}
			defer func() { <-sem }()
			internal, found, err := h.db.GetBlockIDMapping(orgID, sha1ByHash[sha256ID])
			if err != nil {
				return err
			}
			if !found {
				return nil
			}
			mu.Lock()
			result[sha256ID] = internal
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return result, nil
}

// classifyBlockForCommit decides whether one block is commit-ready (R1/R8/R11):
// live (ProbeBlockReuse == Reusable), physically present, at the declared size,
// and owned by this session OR kept alive by a committed file ("fs:") reference.
func (h *FileHandler) classifyBlockForCommit(orgID, referrer, hash string, declaredSize int64, exists bool) (int, error) {
	probe, err := h.db.ProbeBlockReuse(orgID, hash)
	if err != nil {
		return 0, err
	}
	if probe.Decision != db.BlockReuseReusable || !exists {
		return blockStatusNeedsUpload, nil
	}
	if int64(probe.SizeBytes) != declaredSize {
		return blockStatusSizeMismatch, nil
	}
	owned, err := h.db.BlockHasReferrer(orgID, hash, referrer)
	if err != nil {
		return 0, err
	}
	if owned {
		return blockStatusReady, nil
	}
	permanent, err := blockHasPermanentReference(h.db, orgID, hash)
	if err != nil {
		return 0, err
	}
	if permanent {
		return blockStatusReady, nil
	}
	return blockStatusNeedsUpload, nil
}

// blockHasPermanentReference reports whether a block is kept alive by a DURABLE
// committed-file reference ("fs:<library>:<fs_id>"). It deliberately does NOT
// count a publish-attempt ref ("pub:<attempt>") as permanent: that ref is
// transient and disappears if the foreign attempt loses its HEAD CAS and cleans
// up, which would leave this commit pointing at a block that can be GC'd. A
// block alive only via a foreign pub: ref is therefore treated as needs_upload,
// so the client re-uploads it and we materialize our own session-owned ref.
func blockHasPermanentReference(database *db.DB, orgID, blockID string) (bool, error) {
	referrers, err := database.ListBlockReferrers(orgID, blockID)
	if err != nil {
		return false, err
	}
	for _, r := range referrers {
		if strings.HasPrefix(r, "fs:") {
			return true, nil
		}
	}
	return false, nil
}
