package v2

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	"github.com/gin-gonic/gin"
	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"
)

// blockProbeConcurrency bounds parallel per-block classification for
// session-mode /blocks/check so a large hash list does not become serial
// Cassandra/S3 round-trips.
const blockProbeConcurrency = 20

var errSessionCheckBlockStoreUnavailable = errors.New("block storage not available")

var checkBlocksProbeReuseFn = func(database *db.DB, orgID, hash string) (db.BlockReuseProbe, error) {
	return database.ProbeBlockReuse(orgID, hash)
}

var checkBlocksClassifyOwnershipFn = classifyBlockOwnership

// checkBlocksReusableCandidatesParallel probes the metadata plane first and
// returns only the hashes whose blocks are logically reusable
// (ProbeBlockReuse == Reusable). Session-mode /blocks/check uses this before
// any S3 HEAD/Exists work so files that are mostly new do not pay thousands of
// unnecessary object-store existence probes.
func checkBlocksReusableCandidatesParallel(ctx context.Context, database *db.DB, orgID string, hashes []string, concurrency int) (map[string]bool, []string, error) {
	result := make(map[string]bool, len(hashes))
	reusable := make([]string, 0, len(hashes))
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
			probe, err := checkBlocksProbeReuseFn(database, orgID, hash)
			if err != nil {
				return err
			}
			isReusable := probe.Decision == db.BlockReuseReusable
			mu.Lock()
			result[hash] = isReusable
			if isReusable {
				reusable = append(reusable, hash)
			}
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, nil, err
	}
	return result, reusable, nil
}

// checkBlocksReadyParallel classifies, per hash already known reusable in the
// metadata plane, whether the block is truly commit-ready for session-mode
// /blocks/check: physically present in S3 and owned by this session or
// permanently referenced (classifyBlockOwnership — the same helper
// file_from_blocks.go's classifyBlockForCommit uses). This is the commit's
// classifier minus the size check (check has no declared per-block sizes to
// compare against; sizes only arrive in the commit manifest), so a block
// /blocks/check reports "existing" is exactly the set the commit will accept
// modulo size — closing the "check says existing, commit says needs_upload"
// gap for anything both endpoints can see.
func checkBlocksReadyParallel(ctx context.Context, database *db.DB, orgID, referrer string, hashes []string, existsMap map[string]bool, concurrency int) (map[string]bool, error) {
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
			if !existsMap[hash] {
				mu.Lock()
				result[hash] = false
				mu.Unlock()
				return nil
			}
			ready, err := checkBlocksClassifyOwnershipFn(database, orgID, referrer, hash)
			if err != nil {
				return err
			}
			mu.Lock()
			result[hash] = ready
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
	storage          *storage.S3Store // Legacy single S3 store (fallback source; org-scoped per request)
	storageManager   *storage.Manager // Multi-backend storage manager
	config           *config.Config
	db               *db.DB                           // Optional: enables session-scoped materialization for the web flow
	permMiddleware   *middleware.PermissionMiddleware // Optional: re-verifies upload permission for long-lived sessions (R12)
	sessionPermCache *sessionPermissionCache          // Bounds how often that re-verification runs (see sessionPermissionCache)
	uploadLimiter    *blockUploadConcurrencyLimiter   // Bounds concurrent session-mode /blocks/upload per user (item 18)
}

// RegisterBlockRoutes registers block API routes with either a raw S3 fallback
// or a multi-region StorageManager. Both resolve an org-scoped store per request.
func RegisterBlockRoutes(rg *gin.RouterGroup, database *db.DB, s3Store *storage.S3Store, storageManager *storage.Manager, cfg *config.Config, permMiddleware *middleware.PermissionMiddleware) {
	maxConcurrentUploads := 0
	if cfg != nil {
		maxConcurrentUploads = cfg.WebUploads.MaxConcurrentBlockUploadsPerUser
	}
	h := &BlockHandler{
		storage:          s3Store,
		storageManager:   storageManager,
		config:           cfg,
		db:               database,
		permMiddleware:   permMiddleware,
		sessionPermCache: &sessionPermissionCache{},
		uploadLimiter:    newBlockUploadConcurrencyLimiter(maxConcurrentUploads),
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

		// There is deliberately NO GET/HEAD /blocks/:hash. Even though S3 keys are
		// now org-scoped (blocks/<org_id>/...), block-level reads still cannot be
		// authorized against a library permission without a repo context — a
		// bare-hash read endpoint is an intra-org content oracle by construction.
		// Nothing consumes it: web downloads go
		// through file paths and desktop sync uses the repo-scoped, permission-
		// checked /seafhttp/repo/:repo_id/block/:block_id. If a client-side block
		// download flow is ever needed, reintroduce it repo-scoped like seafhttp
		// (repos/:repo_id/blocks/:hash + library permission + the block's
		// canonical storage class). See docs/WEB-BLOCK-UPLOAD.md finding 11.
	}
}

// getBlockStore returns the appropriate org-scoped BlockStore based on hostname
// routing. The org comes from the authenticated request context; without it (or
// on any resolution error) it fails closed (nil), never an org-less global store.
func (h *BlockHandler) getBlockStore(c *gin.Context) (*storage.BlockStore, string) {
	orgID := c.GetString("org_id")
	if h.storageManager == nil {
		bs := h.buildFallbackOrgBlockStore(orgID)
		if bs == nil {
			return nil, "legacy"
		}
		return bs, "legacy"
	}

	// Resolve storage class based on hostname
	hostname := routingHostname(c, h.config)
	storageClass := h.storageManager.ResolveStorageClass(hostname, "", "hot")

	// Get healthy org-scoped BlockStore with failover
	blockStore, actualClass, err := h.storageManager.GetHealthyBlockStoreForOrg(orgID, storageClass)
	if err != nil {
		log.Printf("v2/blocks: failed to get healthy backend for %s: %v\n", storageClass, err)
		return nil, storageClass
	}

	return blockStore, actualClass
}

// buildFallbackOrgBlockStore builds an org-scoped store from the legacy single S3
// store for the no-storage-manager path. Returns nil (fail closed) when the raw
// store is missing or the org id is invalid — the org-less singleton is never
// served, as that would reintroduce the cross-org block-delete hazard (P10).
func (h *BlockHandler) buildFallbackOrgBlockStore(orgID string) *storage.BlockStore {
	if h.storage == nil {
		return nil
	}
	bs, err := storage.NewOrgBlockStore(h.storage, "blocks/", orgID)
	if err != nil {
		log.Printf("v2/blocks: cannot build org-scoped fallback block store: %v\n", err)
		return nil
	}
	return bs
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
		bs := h.buildFallbackOrgBlockStore(orgID)
		if bs == nil {
			return nil, "legacy"
		}
		return bs, "legacy"
	}
	libraryClass := h.lookupLibraryStorageClass(orgID, repoID)
	preferred := h.storageManager.ResolveStorageClass(routingHostname(c, h.config), libraryClass, "hot")
	blockStore, actualClass, err := h.storageManager.GetHealthyBlockStoreForOrg(orgID, preferred)
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

// sessionIDFromRequest reads the web block-upload session id from the
// X-Block-Upload-Session header only. The legacy ?session= transport is
// intentionally rejected rather than silently treated as "no session", because
// falling back to the legacy no-session path would be both misleading and
// unsafe for an outdated client.
func sessionIDFromRequest(c *gin.Context) (sessionID string, usedLegacyQuery bool) {
	if h := strings.TrimSpace(c.GetHeader("X-Block-Upload-Session")); h != "" {
		return h, false
	}
	if q := strings.TrimSpace(c.Query("session")); q != "" {
		return "", true
	}
	return "", false
}

// sessionPermissionRecheckInterval bounds how often resolveUploadSession
// re-verifies the caller's upload permission for a session's repo (finding
// 12). A block-upload session lives up to 48h; if upload access is revoked
// mid-session (independent of org role/account status, which already
// invalidate the auth token via invalidateSessionsOnDemotion /
// invalidateUserCredentials), staging should stop well before the TTL. But
// re-running the full library-permission resolution (a shares-table scan) on
// EVERY /blocks/upload call — up to thousands per large file — would make
// this "cheap fix" the most expensive query in the hot path. Caching the
// outcome for a short interval keeps staleness bounded to minutes instead of
// 48h while adding no cost to the overwhelming majority of requests. The
// commit (file-from-blocks) always re-verifies permission fresh regardless
// (requireWritePermission + RequirePermFlag), so this is defense in depth,
// not the sole enforcement point.
const sessionPermissionRecheckInterval = 2 * time.Minute

// sessionPermissionCacheSweepSize bounds the cache's memory: once it holds
// this many entries, the next insert sweeps stale ones inline and, if that is
// still insufficient during a burst of distinct live sessions, trims the
// oldest confirmations so the map stays hard-bounded instead of growing
// indefinitely.
const sessionPermissionCacheSweepSize = 10000

// Trim below the hard cap once it is reached so a burst of many fresh session
// ids does not trigger an O(n) oldest-entry scan on every single subsequent
// insert. Keeping a small buffer amortizes trim work while preserving the same
// short-lived, best-effort semantics.
const sessionPermissionCacheTrimTo = sessionPermissionCacheSweepSize - (sessionPermissionCacheSweepSize / 10)

// sessionPermissionCache remembers, per session id, the last time upload
// permission was confirmed. It is per-process (not shared across nodes) —
// acceptable because it only narrows an already-bounded exposure window, and
// every node still converges on the same fresh check within the interval.
type sessionPermissionCache struct {
	mu      sync.Mutex
	checked map[string]time.Time
}

type sessionCacheEntry struct {
	sessionID string
	checkedAt time.Time
}

// allow reports whether the session's permission was confirmed within the
// last sessionPermissionRecheckInterval; otherwise it runs verify() and caches
// a fresh confirmation only on success (a denial is never cached, so a
// mid-window permission fix is picked up on the very next request rather than
// waiting out the interval).
func (c *sessionPermissionCache) allow(sessionID string, verify func() bool) bool {
	c.mu.Lock()
	if t, ok := c.checked[sessionID]; ok && time.Since(t) < sessionPermissionRecheckInterval {
		c.mu.Unlock()
		return true
	}
	c.mu.Unlock()

	if !verify() {
		return false
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.checked == nil {
		c.checked = make(map[string]time.Time)
	}
	now := time.Now()
	if len(c.checked) >= sessionPermissionCacheSweepSize {
		c.trimLocked(now)
	}
	c.checked[sessionID] = now
	return true
}

func (c *sessionPermissionCache) trimLocked(now time.Time) {
	cutoff := now.Add(-sessionPermissionRecheckInterval)
	for id, t := range c.checked {
		if t.Before(cutoff) {
			delete(c.checked, id)
		}
	}
	if len(c.checked) < sessionPermissionCacheSweepSize {
		return
	}

	entries := make([]sessionCacheEntry, 0, len(c.checked))
	for id, t := range c.checked {
		entries = append(entries, sessionCacheEntry{sessionID: id, checkedAt: t})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].checkedAt.Before(entries[j].checkedAt)
	})

	trimCount := len(c.checked) - sessionPermissionCacheTrimTo + 1
	if trimCount < 1 {
		trimCount = 1
	}
	if trimCount > len(entries) {
		trimCount = len(entries)
	}
	for i := 0; i < trimCount; i++ {
		delete(c.checked, entries[i].sessionID)
	}
}

// blockUploadConcurrencyLimiter bounds how many concurrent session-mode
// /blocks/upload requests a single user may have in flight. Each such request
// buffers a whole block (~8–16 MB) in memory (io.ReadAll in UploadBlock), so
// without a cap a burst from one authenticated user is real RAM pressure under
// abuse (docs/WEB-BLOCK-UPLOAD.md item 18). The slot is acquired BEFORE the body
// is read, so a rejected request never allocates the buffer.
//
// It is per-process (not shared across nodes) — like sessionPermissionCache and
// the per-IP rate limiter (finding 13): with N nodes the effective budget is N×,
// which is acceptable for an anti-abuse backstop. The map is keyed by
// "org_id:user_id" and self-cleans (a key is deleted when its count returns to
// zero), so it stays bounded to currently-active users. max <= 0 disables the cap.
type blockUploadConcurrencyLimiter struct {
	max      int
	mu       sync.Mutex
	inflight map[string]int
}

func newBlockUploadConcurrencyLimiter(max int) *blockUploadConcurrencyLimiter {
	return &blockUploadConcurrencyLimiter{max: max, inflight: make(map[string]int)}
}

// tryAcquire reserves a slot for userKey. It returns false (without mutating the
// map) when the user is already at the cap; the caller must NOT release in that
// case. A non-positive max means the cap is disabled and every acquire succeeds.
func (l *blockUploadConcurrencyLimiter) tryAcquire(userKey string) bool {
	if l == nil || l.max <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inflight[userKey] >= l.max {
		return false
	}
	l.inflight[userKey]++
	return true
}

// release returns a slot previously taken by a successful tryAcquire. It deletes
// the key at zero so the map does not retain idle users. It is a no-op when the
// cap is disabled (tryAcquire never recorded anything).
func (l *blockUploadConcurrencyLimiter) release(userKey string) {
	if l == nil || l.max <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if n := l.inflight[userKey]; n <= 1 {
		delete(l.inflight, userKey)
	} else {
		l.inflight[userKey] = n - 1
	}
}

// blockBodyLimit is the maximum request body a /blocks/upload call may buffer in
// memory (io.ReadAll). For the web (session) flow every block is exactly the
// CAS block size frozen onto the session at creation time (the last block is ≤
// it), so a session request is bounded to THAT size — NOT
// chunking.adaptive.absolute_max, which can be 256 MB and would let one
// authenticated user force a 256 MB allocation per request, making the per-user
// concurrency cap (item 18) almost useless for RAM protection (cap × 256 MB =
// GBs). The legacy no-session path (desktop/mobile sync, variable FastCDC blocks
// up to absolute_max) keeps the larger bound.
func (h *BlockHandler) blockBodyLimit(resolution uploadSessionResolution, session db.BlockUploadSession) int64 {
	if resolution == uploadSessionValid {
		if session.BlockSizeBytes > 0 {
			return session.BlockSizeBytes
		}
		return h.config.WebBlockUploadBlockSize()
	}
	return h.config.Chunking.Adaptive.AbsoluteMax
}

// staged-block bucket cap headroom. The per-bucket cap is
// stagedBlockBucketCapFactor × perBucket + stagedBlockBucketSlack so hash-collision
// variance never falsely rejects a legitimate file. This makes the *ledger* a
// deliberately loose anti-abuse backstop (roughly 2x per-bucket headroom plus
// slack; up to 5x for a one-block ceiling), not an exact byte cap. The EXACT
// per-file size is still enforced elsewhere: the declared-size fail-fast at
// session creation and the commit's `manifest.size == expected_size` guard. See
// docs/WEB-BLOCK-UPLOAD.md item 1.
const stagedBlockBucketCapFactor = 2
const stagedBlockBucketSlack = 3

const blockUploadStagingCapReachedCode = "staging_cap_reached"

// blockUploadStagingAdmissionFromConfig derives the immutable staging-admission
// contract for a NEW session from the then-current config. The result is frozen
// onto block_upload_sessions so live config changes cannot move a retry to a
// different bucket or change the accepted block size mid-session.
func blockUploadStagingAdmissionFromConfig(cfg *config.Config) db.BlockUploadSessionAdmission {
	if cfg == nil {
		return db.BlockUploadSessionAdmission{}
	}
	ceiling := cfg.EffectiveMaxStagedBytesPerSession()
	blockSize := cfg.WebBlockUploadBlockSize()
	if ceiling <= 0 || blockSize <= 0 {
		return db.BlockUploadSessionAdmission{BlockSizeBytes: blockSize}
	}
	maxBlocks := (ceiling + blockSize - 1) / blockSize // >= 1
	buckets := int64(db.BlockUploadStagedBlockBuckets)
	if maxBlocks < buckets {
		buckets = maxBlocks
	}
	perBucket := (maxBlocks + buckets - 1) / buckets
	return db.BlockUploadSessionAdmission{
		BlockSizeBytes:    blockSize,
		StagedBucketCount: int(buckets),
		StagedBucketCap:   int(perBucket)*stagedBlockBucketCapFactor + stagedBlockBucketSlack,
	}
}

// stagedBlockBucketCap returns, for the session flow, the effective bucket count,
// the per-bucket staged-block cap, and whether the per-session cap is enabled. A
// persisted session contract wins; older sessions without frozen params fall back
// to the current config for compatibility during rollout.
func (h *BlockHandler) stagedBlockBucketCap(session db.BlockUploadSession) (bucketCount, bucketCap int, enabled bool) {
	if session.StagedBucketCount > 0 && session.StagedBucketCap > 0 {
		return session.StagedBucketCount, session.StagedBucketCap, true
	}
	if h.db == nil {
		return 0, 0, false
	}
	admission := blockUploadStagingAdmissionFromConfig(h.config)
	if admission.StagedBucketCount <= 0 || admission.StagedBucketCap <= 0 {
		return 0, 0, false
	}
	return admission.StagedBucketCount, admission.StagedBucketCap, true
}

// tryAdmitSessionUpload enforces the per-user concurrency cap for the web
// (session) flow. For a valid session it reserves a slot keyed by
// "org_id:user_id"; if the user is already at the cap it emits 429 + Retry-After
// (counted by BlockUploadConcurrencyRejectionsTotal) and returns ok=false. When
// ok is true the caller MUST defer the returned release. Non-session requests
// (legacy sync path) are never capped — release is a no-op and ok is always
// true. A 429 is retryable: the web client's withRetry loop backs off and
// re-sends, so legitimate bursts self-throttle instead of failing.
func (h *BlockHandler) tryAdmitSessionUpload(c *gin.Context, resolution uploadSessionResolution) (release func(), ok bool) {
	if resolution != uploadSessionValid {
		return func() {}, true
	}
	userKey := c.GetString("org_id") + ":" + c.GetString("user_id")
	if !h.uploadLimiter.tryAcquire(userKey) {
		metrics.BlockUploadConcurrencyRejectionsTotal.Inc()
		c.Header("Retry-After", "1")
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many concurrent block uploads; retry shortly"})
		return func() {}, false
	}
	return func() { h.uploadLimiter.release(userKey) }, true
}

type uploadSessionResolution int

const (
	uploadSessionAbsent uploadSessionResolution = iota
	uploadSessionValid
	uploadSessionInvalid
	uploadSessionUnavailable
	uploadSessionHeaderRequired
	uploadSessionPermissionDenied
	uploadSessionCommitted
)

func (r uploadSessionResolution) deniedStatus() int {
	if r == uploadSessionUnavailable {
		return http.StatusServiceUnavailable
	}
	if r == uploadSessionPermissionDenied {
		return http.StatusForbidden
	}
	if r == uploadSessionHeaderRequired {
		return http.StatusBadRequest
	}
	if r == uploadSessionCommitted {
		return http.StatusConflict
	}
	return http.StatusUnauthorized
}

func (r uploadSessionResolution) deniedMessage() string {
	if r == uploadSessionHeaderRequired {
		return "block upload session must be sent via X-Block-Upload-Session header"
	}
	if r == uploadSessionUnavailable {
		return "upload session unavailable"
	}
	if r == uploadSessionPermissionDenied {
		return "upload is no longer allowed for this session"
	}
	if r == uploadSessionCommitted {
		return "upload session already committed; start a new upload"
	}
	return "invalid or expired upload session"
}

// resolveUploadSession reads the web content-addressed upload session id (see
// sessionIDFromRequest) and validates it. The returned resolution
// distinguishes "no session supplied" from "invalid/expired" and "permission
// revoked" so callers can respond accurately.
func (h *BlockHandler) resolveUploadSession(c *gin.Context) (db.BlockUploadSession, uploadSessionResolution) {
	sessionID, usedLegacyQuery := sessionIDFromRequest(c)
	if usedLegacyQuery {
		return db.BlockUploadSession{}, uploadSessionHeaderRequired
	}
	if sessionID == "" {
		return db.BlockUploadSession{}, uploadSessionAbsent
	}
	if !h.blockUploadFlowEnabled() {
		return db.BlockUploadSession{}, uploadSessionInvalid
	}
	if h.db == nil {
		return db.BlockUploadSession{}, uploadSessionUnavailable
	}
	session, found, err := h.db.GetBlockUploadSession(sessionID)
	if err != nil {
		return db.BlockUploadSession{}, uploadSessionUnavailable
	}
	if !found {
		return db.BlockUploadSession{}, uploadSessionInvalid
	}
	if session.OrgID != c.GetString("org_id") || session.UserID != c.GetString("user_id") {
		return db.BlockUploadSession{}, uploadSessionInvalid
	}
	// A committed session is TERMINAL for /blocks/check and /blocks/upload: its file
	// is already published and its per-user slot was freed at commit, so continuing
	// to stage blocks under it would (a) leak provisional refs that can never be
	// committed and (b) defeat max_uncommitted_block_sessions_per_user (a client
	// could commit once to recover its budget, then keep staging for the 48h TTL).
	// Commit idempotency (R7) is handled on the commit path, not here.
	if session.Committed {
		return db.BlockUploadSession{}, uploadSessionCommitted
	}
	if h.permMiddleware != nil {
		allowed := h.sessionPermCache.allow(session.SessionID, func() bool {
			return h.permMiddleware.RequirePermFlagForRepo(c, session.RepoID, "upload")
		})
		if !allowed {
			return db.BlockUploadSession{}, uploadSessionPermissionDenied
		}
	}
	return session, uploadSessionValid
}

func dedupePreserveOrder(values []string) []string {
	if len(values) < 2 {
		return values
	}
	seen := make(map[string]struct{}, len(values))
	deduped := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		deduped = append(deduped, value)
	}
	return deduped
}

func (h *BlockHandler) checkBlocksForSession(c *gin.Context, session db.BlockUploadSession, hashes []string) (CheckBlocksResponse, error) {
	orgID := c.GetString("org_id")
	uniqueHashes := dedupePreserveOrder(hashes)
	reusableByHash, reusableHashes, err := checkBlocksReusableCandidatesParallel(c.Request.Context(), h.db, orgID, uniqueHashes, blockProbeConcurrency)
	if err != nil {
		return CheckBlocksResponse{}, err
	}

	if len(reusableHashes) == 0 {
		return CheckBlocksResponse{Existing: nil, Missing: append([]string(nil), hashes...)}, nil
	}

	blockStore, _ := h.getBlockStoreForRepo(c, session.OrgID, session.RepoID)
	if blockStore == nil {
		return CheckBlocksResponse{}, errSessionCheckBlockStoreUnavailable
	}

	existsMap, err := blockStore.CheckBlocksParallel(c.Request.Context(), reusableHashes, blockProbeConcurrency)
	if err != nil {
		return CheckBlocksResponse{}, err
	}

	referrer := db.BlockReferrerForUpload(session.SessionID)
	ready, err := checkBlocksReadyParallel(c.Request.Context(), h.db, orgID, referrer, reusableHashes, existsMap, blockProbeConcurrency)
	if err != nil {
		return CheckBlocksResponse{}, err
	}

	var existing, missing []string
	for _, hash := range hashes {
		if reusableByHash[hash] && ready[hash] {
			existing = append(existing, hash)
		} else {
			missing = append(missing, hash)
		}
	}
	return CheckBlocksResponse{Existing: existing, Missing: missing}, nil
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
// mapping path now uses the same fail-closed conflict contract. isNew labels the
// staging metric (finding 8) by whether the underlying S3 PUT was a fresh
// write or a dedup no-op — governance work happens either way (R9).
func (h *BlockHandler) materializeUploadedBlock(session db.BlockUploadSession, sha256ID, sha1ID string, size int, storageClass string, isNew bool) error {
	if err := RegisterWebUploadedBlockAndMapping(h.db, session.OrgID, session.RepoID, sha256ID, session.SessionID, size, storageClass, "", sha1ID); err != nil {
		return err
	}
	metrics.BlockUploadStagedBlocksTotal.WithLabelValues(strconv.FormatBool(isNew)).Inc()
	return nil
}

// respondBlockMaterializeError maps a materializeUploadedBlock failure to the web
// funnel's HTTP response and reports whether it wrote one (the caller must return
// when it did). It is the single place the two UploadBlock materialize sites share.
//
// A verified external→different-internal mapping conflict is a permanent 409. Every
// other error — including ErrBlockDeleteInProgress, which the metadata-upsert backstop
// now raises on a lost stub-repair race — is a fail-closed 500. This is the deliberate
// PR-2 boundary: unlike the six funnels wrapped in RetryUploadedBlockMaterialization,
// this unprobed web-session funnel has no bounded retry yet. PR-5 must consciously
// change ErrBlockDeleteInProgress here to a 409 + Retry-After once traffic accounting
// and the staged-block reservation are hoisted so a retry cannot double-charge.
func respondBlockMaterializeError(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, db.ErrBlockIDMappingConflict) {
		c.JSON(http.StatusConflict, gin.H{"error": "block id mapping conflict"})
		return true
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to register block"})
	return true
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
	for i, hash := range req.Hashes {
		if !isHex64(hash) {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("hashes[%d]: invalid sha256", i)})
			return
		}
	}

	session, resolution := h.resolveUploadSession(c)
	if resolution != uploadSessionAbsent && resolution != uploadSessionValid {
		c.JSON(resolution.deniedStatus(), gin.H{"error": resolution.deniedMessage()})
		return
	}

	// Session mode (web flow, R3 + finding 3): a block counts as "existing" only
	// when checkBlocksReadyParallel agrees it is truly commit-ready — live,
	// physically present in S3, AND owned by this session or permanently
	// referenced — the same classifier the commit uses (minus the size check,
	// which check cannot evaluate without the manifest's declared sizes).
	// Anything less is reported missing so the client (re)uploads it under the
	// session and materializes its own reference.
	if resolution == uploadSessionValid {
		resp, err := h.checkBlocksForSession(c, session, req.Hashes)
		if err != nil {
			if errors.Is(err, errSessionCheckBlockStoreUnavailable) {
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "block storage not available"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check blocks"})
			return
		}
		c.JSON(http.StatusOK, resp)
		return
	}

	// Get appropriate BlockStore based on hostname routing
	blockStore, _ := h.getBlockStore(c)
	if blockStore == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "block storage not available"})
		return
	}

	// Legacy physical-existence oracle (desktop/mobile sync, no session).
	uniqueHashes := dedupePreserveOrder(req.Hashes)
	existsMap, err := blockStore.CheckBlocksParallel(c.Request.Context(), uniqueHashes, 20)
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
	session, resolution := h.resolveUploadSession(c)
	if resolution != uploadSessionAbsent && resolution != uploadSessionValid {
		c.JSON(resolution.deniedStatus(), gin.H{"error": resolution.deniedMessage()})
		return
	}

	// Per-user concurrency cap for the web (session) flow. Acquired BEFORE the
	// block body is read into RAM (io.ReadAll below), so a rejected request never
	// allocates the ~8–16 MB buffer — this is what bounds a single user's
	// instantaneous memory footprint under a burst (item 18).
	release, ok := h.tryAdmitSessionUpload(c, resolution)
	if !ok {
		return
	}
	defer release()

	// Check content length
	if c.Request.ContentLength <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "content length required"})
		return
	}

	// Check against maximum block size. For the session (web) flow this is the
	// configured CAS block size, not chunking.absolute_max — so one authenticated
	// user cannot force a 256 MB buffer per request and defeat the per-user
	// concurrency cap (item 18). Legacy sync keeps the larger absolute_max bound.
	maxSize := h.blockBodyLimit(resolution, session)
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
	if resolution == uploadSessionValid {
		blockStore, storageClass = h.getBlockStoreForRepo(c, session.OrgID, session.RepoID)
	} else {
		blockStore, storageClass = h.getBlockStore(c)
	}
	if blockStore == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "block storage not available"})
		return
	}

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
		if resolution == uploadSessionValid {
			if respondBlockMaterializeError(c, h.materializeUploadedBlock(session, hash, sha1Hash, len(data), storageClass, false)) {
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

	// Session (web) flow: per-session staged-block admission (item 1). This block
	// is NEW (it did not exist above), so reserve a ledger slot BEFORE storing it
	// (reserve-before-PUT, fail-closed): if the session's bucket is already at its
	// cap, reject; otherwise record the reservation and proceed. The reserve is
	// idempotent by (session, bucket, block_id) — a retried block never
	// double-counts — and self-expires with the session TTL (no Cassandra COUNTER).
	// If the PUT below fails after the reserve, the row lingers and TTL reclaims it.
	if resolution == uploadSessionValid {
		if bucketCount, bucketCap, enabled := h.stagedBlockBucketCap(session); enabled {
			bucket := db.StagedBlockBucket(hash, bucketCount)
			staged, err := h.db.CountSessionStagedBlocksInBucket(session.SessionID, bucket, bucketCap+1)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check staged blocks"})
				return
			}
			if staged >= bucketCap {
				// The bucket is full — but let a RETRY of an already-reserved block
				// through (its PUT may have failed): it is already counted, so
				// admitting it does not grow the bound. Only a genuinely new block
				// is rejected.
				reserved, err := h.db.SessionStagedBlockExists(session.SessionID, bucket, hash)
				if err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check staged blocks"})
					return
				}
				if !reserved {
					metrics.BlockUploadSessionAdmissionRejectionsTotal.WithLabelValues("staged_blocks").Inc()
					c.Header("Retry-After", "1")
					c.JSON(http.StatusTooManyRequests, gin.H{
						"error": "session staging limit reached; commit the file or start a new upload",
						"code":  blockUploadStagingCapReachedCode,
					})
					return
				}
			} else if err := h.db.ReserveSessionStagedBlock(session.SessionID, bucket, hash, int64(len(data))); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to reserve staged block"})
				return
			}
		}
	}

	// Logical storage quota is NOT applied per block in the session (web) flow.
	// R5: the user's repo/storage quota is a property of the FINAL file delta and
	// is decided exactly once at file-from-blocks (finalizeStoredUploadMetadata) —
	// a staged block is transient, governed by a provisional ref + TTL. Charging
	// it here would wrongly reject e.g. a same-size overwrite (logical delta ≈ 0)
	// at the first new block. Traffic is still charged per block (above).
	//
	// NOTE: this is NOT the LOGICAL quota. CheckStorageQuota reads
	// storage_used/quota counters, not physical staging. Total uncommitted staged
	// bytes ARE now bounded for the session flow by the per-session staged-block
	// ledger above (item 1) plus the per-user session cap; the legacy no-session
	// path below keeps its own per-block logical check because it predates this
	// flow. See docs/WEB-BLOCK-UPLOAD.md.
	if resolution != uploadSessionValid {
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
	if resolution == uploadSessionValid {
		if respondBlockMaterializeError(c, h.materializeUploadedBlock(session, hash, sha1Hash, len(data), storageClass, true)) {
			return
		}
	}

	c.JSON(http.StatusCreated, UploadBlockResponse{
		Hash: hash,
		Size: int64(len(data)),
		New:  true,
	})
}
