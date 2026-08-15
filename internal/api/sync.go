package api

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"

	v2 "github.com/Sesame-Disk/sesamefs/internal/api/v2"
	"github.com/Sesame-Disk/sesamefs/internal/apikeys"
	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/downloadadmission"
	"github.com/Sesame-Disk/sesamefs/internal/httputil"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/Sesame-Disk/sesamefs/internal/streaming"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	"github.com/aws/smithy-go"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"
)

// ErrHeadConflict indicates that the HEAD was modified concurrently (CAS failure)
var ErrHeadConflict = fmt.Errorf("HEAD was modified concurrently")

var errSyncHeadAutoMergeConflict = errors.New("sync head auto-merge conflict")
var errSyncHeadRepairPending = errors.New("sync head publish pending background repair")
var errSeafileBlockIDsUnavailable = errors.New("seafile sha1 block ids unavailable")

// SyncTokenCreator interface for creating sync tokens
type SyncTokenCreator interface {
	CreateSyncToken(orgID, repoID, userID string) (string, error)
}

// SyncTokenValidator resolves a sync token string to its access metadata. The
// production TokenStore implements this; locked-files uses it to authenticate
// each per-repo token the desktop client sends in its request body.
type SyncTokenValidator interface {
	GetToken(tokenStr string, expectedType TokenType) (*AccessToken, bool)
}

// SyncAccountStatusChecker revalidates that the token's user/org are still
// active before a body-authenticated locked-files entry is honored.
// It is intentionally side-effect free: locked-files omits rejected entries
// per repo instead of writing a route-level HTTP error.
type SyncAccountStatusChecker func(userID, orgID string) error

// SyncHandler handles Seafile sync protocol operations
// These endpoints are used by the Seafile Desktop client for file synchronization
type SyncHandler struct {
	db             *db.DB
	storage        *storage.S3Store // Legacy single store
	storageManager *storage.Manager // Multi-backend storage manager
	config         *config.Config
	tokenCreator   SyncTokenCreator   // Token creator for download-info
	tokenValidator SyncTokenValidator // Validates per-repo tokens in locked-files bodies
	accountStatus  SyncAccountStatusChecker
	permMiddleware *middleware.PermissionMiddleware

	// repairSyncHeadDerivedStateIfDriftedFn is an optional test seam used by
	// unit tests to inject a canary repair failure without spinning up a
	// real DB. Production code leaves it nil; the idempotent fast-path then
	// falls back to repairSyncHeadDerivedStateIfDrifted.
	repairSyncHeadDerivedStateIfDriftedFn func(orgID, repoID, targetHead string) error

	// repairPublishedSyncCommitBlockDeltaFn is an optional test seam used by
	// unit tests to observe or fail the idempotent block-reference repair path
	// without a real DB session. Production code leaves it nil; the helper then
	// runs the real block-delta reconciliation.
	repairPublishedSyncCommitBlockDeltaFn func(orgID, repoID, targetHead string) error

	// finalizedBlockDeltas memoizes (repo, head) pairs whose sync block-reference
	// delta this process has fully finalized, so the idempotent retry path can skip
	// the costly full-tree repair. Nil-safe: a handler built without it just always
	// runs the full (idempotent) repair.
	finalizedBlockDeltas *syncFinalizedDeltaSet

	// blockRepresentationIDs memoizes immutable per-library representation IDs so
	// legacy SHA-1 sync reads do not re-read the libraries row on every block
	// request. A miss only falls back to the authoritative DB read.
	blockRepresentationIDs *syncBlockRepresentationIDCache

	// blockInflight bounds how many block uploads may hold a buffered body at
	// once, per user and across this process. Nil when disabled by configuration,
	// in which case sync_block_max_bytes is again only a per-request bound.
	blockInflight *syncBlockInflightLimiter

	// checkBlocksInflight bounds how many check-blocks requests may be resolving
	// metadata at once, per user and across this process (subcontract C / X11).
	// It is a separate instance from blockInflight, with its own capacity: the
	// two routes exhaust different resources, and one storming must not spend the
	// other's budget. Nil when disabled by configuration, in which case
	// check_blocks_max_ids is again only a per-request bound.
	checkBlocksInflight *syncAdmissionLimiter

	// downloadAdmission is the process-wide D coordinator shared with the other
	// download producers. GetBlock uses ProfileBlock; PutBlock and CheckBlocks
	// keep their separate B/C syncAdmissionLimiter budgets.
	downloadAdmission *downloadadmission.Coordinator
}

// NewSyncHandler creates a new sync protocol handler
func NewSyncHandler(database *db.DB, s3Store *storage.S3Store, storageManager *storage.Manager, cfg *config.Config, permMiddleware *middleware.PermissionMiddleware) *SyncHandler {
	return &SyncHandler{
		db:                     database,
		storage:                s3Store,
		storageManager:         storageManager,
		config:                 cfg,
		permMiddleware:         permMiddleware,
		finalizedBlockDeltas:   newSyncFinalizedDeltaSet(),
		blockRepresentationIDs: newSyncBlockRepresentationIDCache(),
		blockInflight:          newSyncBlockInflightLimiter(cfg),
		checkBlocksInflight:    newCheckBlocksInflightLimiter(cfg),
	}
}

// syncFinalizedDeltaShardCap bounds each generation of the finalized-delta memo.
// At most 2x this many entries are retained (current + previous generation), so
// memory stays capped without per-entry timestamps or a background sweeper.
const syncFinalizedDeltaShardCap = 4096

// syncBlockRepresentationIDCache bounds the number of memoized
// (org, repo)->representation_id entries. Representation IDs are immutable for a
// library in the current model, so eviction or restart can only cause an extra
// DB read, never a stale correctness bug.
const syncBlockRepresentationIDCacheCap = 4096

// syncFinalizedDeltaSet is a bounded, thread-safe set of "(repo, head) finalized"
// markers. It exists purely to let handleSyncHeadIdempotentSuccess skip the
// full-tree block-reference repair when this process already finalized the exact
// head. It is intentionally conservative: a missing entry just means the caller
// runs the full (idempotent) reconciliation, so eviction, restarts, and
// multi-instance deployments can never cause incorrect behavior — only an extra
// repair. Entries stay valid for as long as the commit remains the head, because
// finalized fs: references are permanent until a later head transition removes
// them (which also changes the head, so the idempotent path no longer matches).
type syncFinalizedDeltaSet struct {
	mu   sync.Mutex
	cur  map[string]struct{}
	prev map[string]struct{}
}

func newSyncFinalizedDeltaSet() *syncFinalizedDeltaSet {
	return &syncFinalizedDeltaSet{cur: make(map[string]struct{}, syncFinalizedDeltaShardCap)}
}

func (s *syncFinalizedDeltaSet) mark(repoID, commitID string) {
	if s == nil || repoID == "" || commitID == "" {
		return
	}
	key := repoID + ":" + commitID
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.cur) >= syncFinalizedDeltaShardCap {
		s.prev = s.cur
		s.cur = make(map[string]struct{}, syncFinalizedDeltaShardCap)
	}
	s.cur[key] = struct{}{}
}

func (s *syncFinalizedDeltaSet) contains(repoID, commitID string) bool {
	if s == nil || repoID == "" || commitID == "" {
		return false
	}
	key := repoID + ":" + commitID
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.cur[key]; ok {
		return true
	}
	_, ok := s.prev[key]
	return ok
}

type syncBlockRepresentationIDCache struct {
	mu   sync.Mutex
	cur  map[string]string
	prev map[string]string
}

func newSyncBlockRepresentationIDCache() *syncBlockRepresentationIDCache {
	return &syncBlockRepresentationIDCache{cur: make(map[string]string, syncBlockRepresentationIDCacheCap)}
}

func (c *syncBlockRepresentationIDCache) get(orgID, repoID string) (string, bool) {
	if c == nil || orgID == "" || repoID == "" {
		return "", false
	}
	key := orgID + ":" + repoID
	c.mu.Lock()
	defer c.mu.Unlock()
	if value, ok := c.cur[key]; ok {
		return value, true
	}
	value, ok := c.prev[key]
	return value, ok
}

func (c *syncBlockRepresentationIDCache) put(orgID, repoID, representationID string) {
	if c == nil || orgID == "" || repoID == "" || representationID == "" {
		return
	}
	key := orgID + ":" + repoID
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.cur) >= syncBlockRepresentationIDCacheCap {
		c.prev = c.cur
		c.cur = make(map[string]string, syncBlockRepresentationIDCacheCap)
	}
	c.cur[key] = representationID
}

func (h *SyncHandler) resolveSyncBlockRepresentationID(orgID, repoID string) (string, error) {
	if h == nil || h.db == nil {
		return db.PlainBlockRepresentationID, nil
	}
	if representationID, ok := h.blockRepresentationIDs.get(orgID, repoID); ok {
		return representationID, nil
	}
	representationID, err := db.ResolveBlockRepresentationID(h.db.Session(), orgID, repoID)
	if err != nil {
		return "", err
	}
	h.blockRepresentationIDs.put(orgID, repoID, representationID)
	return representationID, nil
}

// checkSyncPermission verifies the user has the required permission level on the library.
// Returns true if access is granted, false if denied (response already sent).
func (h *SyncHandler) checkSyncPermission(c *gin.Context, repoID string, required middleware.LibraryPermission) bool {
	if h.permMiddleware == nil {
		return true
	}
	if scope, ok := c.Get("api_key_scope"); ok {
		scopeStr, _ := scope.(string)
		switch required {
		case middleware.PermissionRW, middleware.PermissionCloudEdit, middleware.PermissionAdmin, middleware.PermissionOwner:
			if !apikeys.ScopeAllows(scopeStr, apikeys.ScopeReadWrite) {
				c.JSON(http.StatusForbidden, gin.H{"error": "insufficient api key scope"})
				c.Abort()
				return false
			}
		}
	}
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")
	hasAccess, err := h.permMiddleware.HasLibraryAccess(orgID, userID, repoID, required)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check permissions"})
		c.Abort()
		return false
	}
	if !hasAccess {
		c.JSON(http.StatusForbidden, gin.H{"error": "permission denied"})
		c.Abort()
		return false
	}
	return true
}

// SetTokenCreator sets the token creator for download-info endpoint. When the
// creator also implements SyncTokenValidator (the production TokenStore does),
// it doubles as the validator for per-repo tokens sent in locked-files request
// bodies — one wiring point keeps issuance and validation on the same store.
func (h *SyncHandler) SetTokenCreator(tc SyncTokenCreator) {
	h.tokenCreator = tc
	if v, ok := tc.(SyncTokenValidator); ok {
		h.tokenValidator = v
	}
}

// SetAccountStatusChecker injects the same repo-token account/org usability
// check the route-level sync auth middleware applies on :repo_id endpoints.
// The checker must be pure: locked-files treats rejection like any other
// per-entry validation failure and silently omits that repo from the result.
func (h *SyncHandler) SetAccountStatusChecker(checker SyncAccountStatusChecker) {
	h.accountStatus = checker
}

// SetDownloadAdmissionCoordinator makes the process-wide coordinator available
// for the D5 SyncHandler.GetBlock producer. PutBlock and CheckBlocks do not use
// this coordinator.
func (h *SyncHandler) SetDownloadAdmissionCoordinator(coordinator *downloadadmission.Coordinator) {
	h.downloadAdmission = coordinator
}

func (h *SyncHandler) lookupLibraryStorageClass(orgID, repoID string) string {
	if h == nil || h.db == nil || orgID == "" || repoID == "" {
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

var lookupLibraryStorageClassForSyncFn = func(h *SyncHandler, orgID, repoID string) string {
	return h.lookupLibraryStorageClass(orgID, repoID)
}

func (h *SyncHandler) resolvePreferredLibraryStorageClass(c *gin.Context, orgID, repoID string) string {
	libraryClass := lookupLibraryStorageClassForSyncFn(h, orgID, repoID)
	if h.storageManager != nil {
		configuredURL := ""
		if h.config != nil {
			configuredURL = h.config.Server.URL
		}
		return h.storageManager.ResolveStorageClass(httputil.GetRoutingHostname(c, configuredURL), libraryClass, "hot")
	}
	if libraryClass != "" {
		return libraryClass
	}
	return "hot"
}

func (h *SyncHandler) resolveBlockLookupFallbackClass(c *gin.Context, orgID, repoID, storageClass string) string {
	preferredClass := strings.TrimSpace(h.resolvePreferredLibraryStorageClass(c, orgID, repoID))
	if preferredClass != "" {
		return preferredClass
	}
	storageClass = strings.TrimSpace(storageClass)
	if storageClass != "" {
		return storageClass
	}
	return "hot"
}

func (h *SyncHandler) resolveBlockStoreForLookup(c *gin.Context, orgID, repoID, storageClass string) (*storage.BlockStore, string, error) {
	storageClass = strings.TrimSpace(storageClass)
	if h.storageManager != nil {
		if storageClass != "" {
			blockStore, err := h.storageManager.GetBlockStoreForOrg(orgID, storageClass)
			if err == nil {
				return blockStore, storageClass, nil
			}
			log.Printf("resolveBlockStoreForLookup: storage class %s unavailable: %v", storageClass, err)
		}

		fallbackClass := h.resolveBlockLookupFallbackClass(c, orgID, repoID, storageClass)
		blockStore, actualClass, err := h.storageManager.GetHealthyBlockStoreForOrg(orgID, fallbackClass)
		if err != nil {
			return nil, fallbackClass, err
		}
		return blockStore, actualClass, nil
	}

	// Fallback: org-scoped store from the raw S3 store; never the org-less singleton.
	if h.storage != nil {
		bs, err := storage.NewOrgBlockStore(h.storage, "blocks/", orgID)
		if err != nil {
			return nil, storageClass, err
		}
		return bs, storageClass, nil
	}

	return nil, storageClass, fmt.Errorf("block storage not available")
}

func (h *SyncHandler) resolvePreferredBlockStore(c *gin.Context, orgID, repoID string) (*storage.BlockStore, string, error) {
	preferredClass := h.resolvePreferredLibraryStorageClass(c, orgID, repoID)
	if h.storageManager != nil {
		return h.storageManager.GetHealthyBlockStoreForOrg(orgID, preferredClass)
	}
	// Fallback: org-scoped store from the raw S3 store; never the org-less singleton.
	if h.storage != nil {
		bs, err := storage.NewOrgBlockStore(h.storage, "blocks/", orgID)
		if err != nil {
			return nil, preferredClass, err
		}
		return bs, preferredClass, nil
	}
	return nil, preferredClass, fmt.Errorf("block storage not available")
}

// formatSizeSeafile delegates to httputil.FormatSizeSeafile.
var formatSizeSeafile = httputil.FormatSizeSeafile
var syncBlockExistsFn = func(ctx context.Context, blockStore *storage.BlockStore, hash string) (bool, error) {
	return blockStore.BlockExists(ctx, hash)
}
var syncPutBlockDataFn = func(ctx context.Context, blockStore *storage.BlockStore, block *storage.BlockData) (string, error) {
	return blockStore.PutBlockData(ctx, block)
}
var syncPutBlockAutoDirectFn = func(ctx context.Context, blockStore *storage.BlockStore, hash string, data []byte) (string, error) {
	return blockStore.PutBlockAutoDirect(ctx, hash, data)
}
var syncProbeUploadedBlockReuseFn = v2.ProbeUploadedBlockReuse
var syncPrepareUploadedBlockProbeFn = v2.PrepareUploadedBlockProbe
var syncResolveNeedsPutBlockStoreFn = v2.ResolveNeedsPutBlockStore
var syncEnsureReusableBlockPresentFn = v2.EnsureReusableBlockPresent
var registerUploadedBlockAndMappingForSyncFn = v2.RegisterUploadedBlockAndMapping
var syncRetryUploadedBlockMaterializationFn = v2.RetryUploadedBlockMaterializationContext
var syncNewCanonicalBlockReaderFn = streaming.NewCanonicalBlockReader
var syncNewCanonicalBlockCheckReaderFn = streaming.NewCanonicalBlockCheckReaderWithFanout

// The publication handshake seams. SeafHTTP already routes its stage/promote
// pair through equivalent vars (stageSeafHTTPPublishAttemptReferencesFn,
// promoteSeafHTTPPublishAttemptReferencesFn); sync did not, which is why no test
// could observe that one of its entry points promotes without staging (R25 in
// docs/GC-X1-CLOSURE-OPTIONS.md). Production wiring is unchanged.
var stageSyncPublishAttemptReferencesFn = db.StagePublishAttemptReferences
var promoteSyncPublishAttemptReferencesFn = db.PromotePublishAttemptReferences

// buildSyncCommitBlockDeltaFn lets a test supply the delta without a DB session,
// so the handshake property can be asserted over the real control flow of each
// entry point rather than over a reconstruction of it.
var buildSyncCommitBlockDeltaFn = func(h *SyncHandler, repoID, targetCommitID string) (syncCommitBlockDelta, error) {
	return h.buildSyncCommitBlockDelta(repoID, targetCommitID)
}

var resolveSyncBlockIDsFn = func(h *SyncHandler, orgID, repoID string, blockIDs []string) ([]string, error) {
	return h.resolveSyncBlockIDs(orgID, repoID, blockIDs)
}

// syncGetBlockIDMappingFn is the legacy SHA-1 -> internal SHA-256 resolution
// used by check-blocks. It is a variable both for testing and because the
// context argument is the point: bulk resolution must be abandonable, and the
// contextless db.GetBlockIDMapping cannot be.
var syncGetBlockIDMappingFn = func(ctx context.Context, database *db.DB, orgID, representationID, externalID string) (string, bool, error) {
	return database.GetBlockIDMappingContext(ctx, orgID, representationID, externalID)
}
// syncTouchBlockLastAccessFn runs inside the D5 admission slot, so it takes the
// caller's context: without it a Cassandra stall would hold the slot for the
// gocql driver timeout rather than the configured preparation deadline. The
// write itself keeps its historical placement and best-effort semantics — the
// error is still ignored, and a cancelled context simply skips a bookkeeping
// update on a request that is no longer going to send a body.
var syncTouchBlockLastAccessFn = func(ctx context.Context, database *db.DB, orgID, blockID string, accessedAt time.Time) {
	_ = database.Session().Query(`
		UPDATE blocks SET last_accessed = ? WHERE org_id = ? AND block_id = ?
	`, accessedAt, orgID, blockID).WithContext(ctx).Exec()
}

// recordSyncBlockDownloadTrafficFn is the seam that makes block-GET accounting
// assertable. It is deliberately separate from the SeafHTTP download recorder so
// a test can stub one producer without intercepting the other.
var recordSyncBlockDownloadTrafficFn = func(status traffic.QuotaStatus, orgID, userID string, bytes int64) {
	if rec := traffic.Get(); rec != nil {
		traffic.RecordCheckedTransfer(rec, status, orgID, userID, traffic.SyncDownload, bytes)
	}
}

func isSyncCanonicalBlockNotFound(err error) bool {
	if errors.Is(err, streaming.ErrCanonicalBlockMetadataNotFound) {
		return true
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.ErrorCode() {
	case "NoSuchKey", "NotFound":
		return true
	default:
		return false
	}
}

// formatRelativeTimeHTML delegates to httputil.FormatRelativeTimeHTML.
var formatRelativeTimeHTML = httputil.FormatRelativeTimeHTML

// RegisterSyncRoutes registers the sync protocol routes
func (h *SyncHandler) RegisterSyncRoutes(router *gin.Engine, authMiddleware gin.HandlerFunc) {
	// Protocol version endpoint (no auth required)
	router.GET("/seafhttp/protocol-version", h.GetProtocolVersion)

	// Multi-repo head commits endpoint (for checking multiple repos at once)
	// NOTE: No auth middleware — the official Seafile fileserver registers this endpoint
	// without any token validation (confirmed in fileserver/sync_api.go v11.0.13).
	// The desktop client calls this every ~30s without any auth headers. The endpoint
	// only returns commit hashes; repo UUIDs are unguessable, so exposure is minimal.
	router.POST("/seafhttp/repo/head-commits-multi", h.GetHeadCommitsMulti)
	router.POST("/seafhttp/repo/head-commits-multi/", h.GetHeadCommitsMulti)

	// Folder permissions — no auth required. SeaDrive sends GET and POST to
	// /seafhttp/repo/folder-perm?repo_id=XXX without any auth token. The response
	// is always [] (no folder-level restrictions), so no auth is needed.
	// Registered before the wildcard group so Gin matches exactly.
	router.GET("/seafhttp/repo/folder-perm", h.GetFolderPerm)
	router.POST("/seafhttp/repo/folder-perm", h.GetFolderPerm)

	// Locked-files polling — no route-level auth middleware because this is a
	// multi-repo endpoint with no :repo_id in the path. It is still
	// authenticated per body entry via the repo token in that JSON payload.
	// Verified live against a genuine Seafile Pro 11.0.16 instance (2026-07-02):
	// only POST is accepted (GET 400s with an empty-body decode error there too).
	router.POST("/seafhttp/repo/locked-files", h.GetLockedFiles)
	router.POST("/seafhttp/repo/locked-files/", h.GetLockedFiles)

	// Sync protocol routes under /seafhttp/repo/
	repo := router.Group("/seafhttp/repo/:repo_id")
	repo.Use(authMiddleware)
	{
		// Commit operations
		repo.GET("/commit/HEAD", h.GetHeadCommit)
		repo.GET("/commit/:commit_id", h.GetCommit)
		repo.PUT("/commit/:commit_id", h.PutCommit)

		// Block operations
		repo.GET("/block/:block_id", h.GetBlock)
		repo.PUT("/block/:block_id", h.PutBlock)
		repo.POST("/check-blocks", h.CheckBlocks)
		repo.POST("/check-blocks/", h.CheckBlocks)

		// Filesystem operations
		repo.GET("/fs-id-list", h.GetFSIDList)
		repo.GET("/fs-id-list/", h.GetFSIDList)
		repo.GET("/fs/:fs_id", h.GetFSObject)
		repo.POST("/pack-fs", h.PackFS)
		repo.POST("/pack-fs/", h.PackFS)
		repo.POST("/recv-fs", h.RecvFS)
		repo.POST("/recv-fs/", h.RecvFS)
		repo.POST("/check-fs", h.CheckFS)
		repo.POST("/check-fs/", h.CheckFS)

		// Permission and quota
		repo.GET("/permission-check", h.PermissionCheck)
		repo.GET("/permission-check/", h.PermissionCheck)
		repo.GET("/quota-check", h.QuotaCheck)
		repo.GET("/quota-check/", h.QuotaCheck)

		// Update branch (for committing changes)
		repo.POST("/update-branch", h.UpdateBranch)
		repo.POST("/update-branch/", h.UpdateBranch)

		// Download info (for encrypted libraries)
		repo.GET("/download-info", h.GetDownloadInfo)
		repo.GET("/download-info/", h.GetDownloadInfo)
	}
}

// GetProtocolVersion returns the sync protocol version
// GET /seafhttp/protocol-version
func (h *SyncHandler) GetProtocolVersion(c *gin.Context) {
	// Seafile protocol version 2 is the current version used by desktop clients
	c.JSON(http.StatusOK, gin.H{
		"version": 2,
	})
}

// GetFolderPerm returns folder-level permission rules for a repository.
// GET/POST /seafhttp/repo/folder-perm
// SeaDrive calls this during sync to check if any sub-folders have restricted
// permissions. Wire format confirmed live against a genuine Seafile Pro 11.0.16
// instance (2026-07-02): POST body is a JSON array of {repo_id, token, ts} and
// the response is a JSON array, not an object — a repo with no folder-level
// restrictions is simply absent from the response (empty array overall). We
// have no folder-permission feature implemented yet, so every request gets
// that same "no restrictions anywhere" answer.
func (h *SyncHandler) GetFolderPerm(c *gin.Context) {
	c.JSON(http.StatusOK, []struct{}{})
}

// lockedFilesRequestEntry is one element of the JSON array the desktop/SeaDrive
// client POSTs to /seafhttp/repo/locked-files. Token is the per-repo sync token
// the client obtained from download-info; each entry is authenticated with it
// before any lock data is returned. Ts is the client's last-seen lock timestamp
// (accepted but unused — we always return current state; the client re-applies
// idempotently).
type lockedFilesRequestEntry struct {
	RepoID string `json:"repo_id"`
	Token  string `json:"token"`
	Ts     int64  `json:"ts"`
}

// maxLockedFilesRepos bounds how many per-repo entries a single locked-files
// request may carry. The desktop client sends one entry per synced library, so
// real requests stay far below this; the cap only exists so an abusive caller
// cannot turn one unauthenticated-route POST into thousands of token lookups.
const maxLockedFilesRepos = 500

// maxLockedFilesBodyBytes bounds the request body before JSON decoding starts.
// 500 entries of {repo_id, token, ts} fit comfortably under 256 KiB; without
// this, a caller could stream an arbitrarily large body into ShouldBindJSON
// on a route that carries no middleware limits.
const maxLockedFilesBodyBytes = 256 * 1024

// readLimitedRequestBody reads the request body with a hard byte cap. It swaps the
// body for an http.MaxBytesReader so an oversized body is never fully buffered in
// memory: on overflow it writes a 413 and returns ok=false; a plain read error
// becomes a 400. The caller MUST return immediately when ok is false — the response
// has already been written.
func readLimitedRequestBody(c *gin.Context, maxBytes int64) ([]byte, bool) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
	data, err := io.ReadAll(c.Request.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "request body too large", "max_bytes": maxBytes})
			return nil, false
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read request body"})
		return nil, false
	}
	return data, true
}

const emptySyncFSID40 = "0000000000000000000000000000000000000000"

// lockedFileEntry is one locked path within a repo's response entry.
type lockedFileEntry struct {
	Path string `json:"path"`
	ByMe bool   `json:"by_me"`
}

// lockedFilesResponseEntry is the per-repo answer.
type lockedFilesResponseEntry struct {
	RepoID      string            `json:"repo_id"`
	Ts          int64             `json:"ts"`
	LockedFiles []lockedFileEntry `json:"locked_files"`
}

type lockedFilesSeenKey struct {
	repoID string
	userID string
	orgID  string
}

// listRepoLocksFn is a test seam for db.ListRepoLocks, so unit tests can exercise
// GetLockedFiles without a real Cassandra session.
var listRepoLocksFn = func(h *SyncHandler, repoID string) ([]db.RepoLockedFile, error) {
	return db.ListRepoLocks(h.db.Session(), repoID)
}

var loadSyncFSObjectFn = func(h *SyncHandler, repoID, fsID string) (string, string, []string, error) {
	var objType string
	var dirEntries string
	var blockIDs []string
	err := h.db.Session().Query(`
		SELECT obj_type, dir_entries, block_ids FROM fs_objects WHERE library_id = ? AND fs_id = ?
	`, repoID, fsID).Scan(&objType, &dirEntries, &blockIDs)
	if err != nil {
		return "", "", nil, err
	}
	return objType, dirEntries, blockIDs, nil
}

var loadSyncFileBlockIDsFn = func(h *SyncHandler, repoID string, fileIDs []string) (map[string][]string, error) {
	return h.loadSyncFileBlockIDs(repoID, fileIDs)
}

// GetLockedFiles returns the currently locked paths for each requested repo.
// POST /seafhttp/repo/locked-files
// Registered without the route-level auth middleware because it is multi-repo
// (no :repo_id in the path) — but it is NOT unauthenticated: every body entry
// carries the per-repo sync token the client got from download-info, and lock
// data is only returned for entries whose token resolves to that same repo.
// Entries that fail validation are silently omitted, indistinguishable from
// "no locks", so the endpoint never confirms whether a guessed repo_id exists.
// Wire format confirmed live against a genuine Seafile Pro 11.0.16 instance
// (2026-07-02): a repo with no (visible) locks is omitted from the response
// array entirely, rather than included with an empty locked_files list.
// "by_me" compares the lock holder against the user the entry's token was
// issued to.
func (h *SyncHandler) GetLockedFiles(c *gin.Context) {
	body, ok := readLimitedRequestBody(c, maxLockedFilesBodyBytes)
	if !ok {
		return
	}

	var reqs []lockedFilesRequestEntry
	if err := json.Unmarshal(body, &reqs); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON array"})
		return
	}
	if len(reqs) > maxLockedFilesRepos {
		c.JSON(http.StatusBadRequest, gin.H{"error": "too many repos in request"})
		return
	}

	// Fail closed: without a validator there is no way to authenticate the
	// per-repo tokens, so no lock data may be returned at all.
	if h.tokenValidator == nil {
		c.JSON(http.StatusOK, []lockedFilesResponseEntry{})
		return
	}

	now := time.Now().Unix()
	seenRepoUsers := make(map[lockedFilesSeenKey]struct{}, len(reqs))
	respondedRepos := make(map[string]struct{}, len(reqs))
	result := make([]lockedFilesResponseEntry, 0, len(reqs))
	for _, req := range reqs {
		if req.RepoID == "" || req.Token == "" {
			continue
		}

		// Only a repo-level sync token may enumerate a repo's locks.
		// TokenTypeDownload is shared with path-scoped file-download tokens and
		// share-link tokens, and neither of those narrower grants should widen
		// into repo-wide lock visibility.
		//
		// This route has no :repo_id, so the binding is against the repository
		// named in this body entry rather than a route parameter — but the
		// accepted shape is the same one the route-level middleware enforces,
		// so both go through isRepositorySyncToken. Keeping one predicate means
		// a change to what counts as a sync credential cannot apply to one
		// surface and miss the other; this check previously spelled its source
		// clause as "not a link", which would have admitted any future source
		// value that the middleware's allowlist refuses.
		accessToken, valid := h.tokenValidator.GetToken(req.Token, TokenTypeSync)
		if !valid || !isRepositorySyncToken(accessToken, req.RepoID) {
			continue
		}
		// Dedupe repeated work for the same authenticated repo/user/org tuple
		// so identical duplicates do not repeat account checks or lock lookups.
		// Repo-level response dedupe stays separate: a later entry for the same
		// repo but a different authenticated user must still get a chance after
		// this one, because one token could fail account-status while the next
		// remains valid.
		seenKey := lockedFilesSeenKey{
			repoID: req.RepoID,
			userID: accessToken.UserID,
			orgID:  accessToken.OrgID,
		}
		if _, dup := seenRepoUsers[seenKey]; dup {
			continue
		}
		seenRepoUsers[seenKey] = struct{}{}

		if h.accountStatus != nil {
			if err := h.accountStatus(accessToken.UserID, accessToken.OrgID); err != nil {
				continue
			}
		}
		if _, dup := respondedRepos[req.RepoID]; dup {
			continue
		}

		locks, err := listRepoLocksFn(h, req.RepoID)
		if err != nil {
			// Auth/account validation failures are per-entry and omitted.
			// Lock backend failures are global fail-closed: omitting an
			// authorized repo here would falsely report "no locks".
			status := http.StatusInternalServerError
			message := "failed to list locked files"
			if errors.Is(err, db.ErrFileLockStatusUnavailable) {
				status = http.StatusServiceUnavailable
				message = "file lock status unavailable"
			}
			c.JSON(status, gin.H{"error": message})
			return
		}
		respondedRepos[req.RepoID] = struct{}{}
		if len(locks) == 0 {
			continue
		}
		entry := lockedFilesResponseEntry{
			RepoID:      req.RepoID,
			Ts:          now,
			LockedFiles: make([]lockedFileEntry, 0, len(locks)),
		}
		for _, lock := range locks {
			entry.LockedFiles = append(entry.LockedFiles, lockedFileEntry{
				Path: lock.Path,
				ByMe: strings.EqualFold(lock.LockedBy, accessToken.UserID),
			})
		}
		result = append(result, entry)
	}

	c.JSON(http.StatusOK, result)
}

// Commit represents a Seafile commit object
type Commit struct {
	CommitID       string  `json:"commit_id"`
	RepoID         string  `json:"repo_id"`
	RootID         string  `json:"root_id"`          // Root FS object ID
	ParentID       *string `json:"parent_id"`        // Parent commit ID (null for first commit)
	SecondParentID *string `json:"second_parent_id"` // For merge commits (null if none)
	Description    string  `json:"description"`
	Creator        string  `json:"creator"`
	CreatorName    string  `json:"creator_name"`
	Ctime          int64   `json:"ctime"`                      // Creation time (Unix timestamp)
	Version        int     `json:"version"`                    // Commit version (currently 1)
	RepoName       string  `json:"repo_name,omitempty"`        // Repository name
	RepoDesc       string  `json:"repo_desc"`                  // Repository description (always included, even when empty)
	RepoCategory   *string `json:"repo_category"`              // Repository category (null)
	NoLocalHistory int     `json:"no_local_history,omitempty"` // 1 = no local history (only if set)
	Encrypted      string  `json:"encrypted,omitempty"`        // "true" as string, not bool (Seafile compat)
	EncVersion     int     `json:"enc_version,omitempty"`
	Magic          string  `json:"magic,omitempty"`
	Key            string  `json:"key,omitempty"` // Seafile uses "key" not "random_key" in commit
}

// FSObject represents a Seafile filesystem object (file or directory)
type FSObject struct {
	Type     int        `json:"type"` // 1 = file, 3 = directory
	ID       string     `json:"id"`   // SHA-1 hash of contents
	Name     string     `json:"name,omitempty"`
	Mode     int        `json:"mode,omitempty"`      // Unix file mode
	Mtime    int64      `json:"mtime,omitempty"`     // Modification time
	Size     int64      `json:"size,omitempty"`      // File size
	BlockIDs []string   `json:"block_ids,omitempty"` // Block IDs for files
	Entries  *[]FSEntry `json:"dirents,omitempty"`   // Directory entries (pointer to distinguish nil from empty)
}

// FSEntry represents a directory entry
// CRITICAL: Field order MUST be alphabetical to match Seafile JSON format.
// Seafile uses alphabetical key ordering in JSON which affects fs_id hash computation.
type FSEntry struct {
	ID       string `json:"id"`   // FS object ID
	Mode     int    `json:"mode"` // Unix file mode (33188 = regular file, 16384 = directory)
	Modifier string `json:"modifier,omitempty"`
	Mtime    int64  `json:"mtime"`
	Name     string `json:"name"`
	Size     int64  `json:"size,omitempty"`
}

// CorrectedFSObject holds the computed fs_id and properly-formed JSON for an fs_object
type CorrectedFSObject struct {
	ComputedFSID  string // SHA-1 of properly ordered JSON
	StoredFSID    string // Original fs_id from database
	CorrectedJSON []byte // JSON with alphabetical keys and corrected child ids
}

// computeCorrectedObject recursively computes the correct fs_id for an fs_object
// It handles directories by first computing children's correct fs_ids and using those in dirents
// Returns nil if object not found
func (h *SyncHandler) computeCorrectedObject(repoID, storedFSID string, cache map[string]*CorrectedFSObject) (*CorrectedFSObject, error) {
	// Check cache first
	if cached, ok := cache[storedFSID]; ok {
		return cached, nil
	}

	// Query the fs_object
	var fsType string
	var size int64
	var entriesJSON string
	var blockIDs []string
	var seafileBlockIDs []string
	err := h.db.Session().Query(`
		SELECT obj_type, size_bytes, dir_entries, block_ids, seafile_block_ids_sha1 FROM fs_objects WHERE library_id = ? AND fs_id = ?
	`, repoID, storedFSID).Scan(&fsType, &size, &entriesJSON, &blockIDs, &seafileBlockIDs)

	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}

	var jsonObj map[string]interface{}

	if fsType == "dir" {
		// Parse entries and recursively compute children's correct fs_ids
		var dirents []map[string]interface{}
		if entriesJSON != "" && entriesJSON != "[]" {
			var entries []FSEntry
			if err := json.Unmarshal([]byte(entriesJSON), &entries); err != nil {
				return nil, err
			}
			for _, entry := range entries {
				// Recursively compute child's correct fs_id
				childCorrect, err := h.computeCorrectedObject(repoID, entry.ID, cache)
				if err != nil {
					return nil, err
				}
				childID := entry.ID // Default to stored if child not found
				if childCorrect != nil {
					childID = childCorrect.ComputedFSID
				}

				dirent := map[string]interface{}{
					"id":    childID, // Use COMPUTED child id
					"mode":  entry.Mode,
					"mtime": entry.Mtime,
					"name":  entry.Name,
				}
				if entry.Modifier != "" {
					dirent["modifier"] = entry.Modifier
				}
				if entry.Size > 0 {
					dirent["size"] = entry.Size
				}
				dirents = append(dirents, dirent)
			}
		} else {
			dirents = []map[string]interface{}{}
		}
		jsonObj = map[string]interface{}{
			"dirents": dirents,
			"type":    3,
			"version": 1,
		}
	} else {
		// File: no children to fix. The computed fs_id must match what the Seafile
		// client computes, which hashes the SHA-1 block-id list — so serialize the
		// SHA-1 list (seafile_block_ids_sha1), falling back to block_ids when empty.
		// See seafileServeBlockIDs.
		serveBlockIDs, ok := seafileServeBlockIDs(blockIDs, seafileBlockIDs)
		if !ok {
			log.Printf("[computeCorrectedObject] fs_object %s has SHA-256 block_ids without seafile_block_ids_sha1; cannot recompute a Seafile fs_id", storedFSID)
			return nil, errSeafileBlockIDsUnavailable
		}
		jsonObj = map[string]interface{}{
			"block_ids": serveBlockIDs,
			"size":      size,
			"type":      1,
			"version":   1,
		}
	}

	// Serialize and compute hash
	jsonBytes, err := json.Marshal(jsonObj)
	if err != nil {
		return nil, err
	}
	computedHash := sha1.Sum(jsonBytes)
	computedFSID := hex.EncodeToString(computedHash[:])

	result := &CorrectedFSObject{
		ComputedFSID:  computedFSID,
		StoredFSID:    storedFSID,
		CorrectedJSON: jsonBytes,
	}

	// Cache result
	cache[storedFSID] = result

	return result, nil
}

// buildFSIDMapping builds a complete mapping of computed→stored fs_ids for a repo tree
// Starting from a root stored fs_id, recursively computes all correct fs_ids
func (h *SyncHandler) buildFSIDMapping(repoID, rootStoredFSID string) (computedToStored map[string]string, storedToCorrected map[string]*CorrectedFSObject, err error) {
	computedToStored = make(map[string]string)
	storedToCorrected = make(map[string]*CorrectedFSObject)

	// Recursively compute all objects starting from root
	if err := h.collectCorrectedObjects(repoID, rootStoredFSID, storedToCorrected); err != nil {
		return nil, nil, err
	}

	// Build the reverse mapping
	for storedID, corrected := range storedToCorrected {
		computedToStored[corrected.ComputedFSID] = storedID
	}

	return computedToStored, storedToCorrected, nil
}

// collectCorrectedObjects recursively collects all corrected fs_objects
func (h *SyncHandler) collectCorrectedObjects(repoID, storedFSID string, cache map[string]*CorrectedFSObject) error {
	if storedFSID == "" || len(storedFSID) != 40 {
		return nil
	}
	if _, ok := cache[storedFSID]; ok {
		return nil // Already processed
	}

	// Compute this object (will recurse into children)
	_, err := h.computeCorrectedObject(repoID, storedFSID, cache)
	return err
}

// GetHeadCommit returns the HEAD commit for a repository
// GET /seafhttp/repo/:repo_id/commit/HEAD
func (h *SyncHandler) GetHeadCommit(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	if !h.checkSyncPermission(c, repoID, middleware.PermissionR) {
		return
	}

	// Check if database is available
	if h.db == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "database not available"})
		return
	}

	// Get head commit from database
	var headCommitID string
	err := h.db.Session().Query(`
		SELECT head_commit_id FROM libraries
		WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&headCommitID)

	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	// If no head commit exists, create an initial commit
	if headCommitID == "" {
		headCommitID, err = h.createInitialCommit(repoID, orgID, userID)
		if err != nil {
			// Log error but return empty - client can handle this
			c.JSON(http.StatusOK, gin.H{"is_corrupted": 0, "head_commit_id": ""})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"is_corrupted":   0, // Seafile uses integer 0, not boolean false
		"head_commit_id": headCommitID,
	})
}

// createInitialCommit creates the first commit for an empty repository
func (h *SyncHandler) createInitialCommit(repoID, orgID, userID string) (string, error) {
	now := time.Now()

	// Create empty root directory FS object using content-addressable hash.
	// This matches the v2 REST API approach in libraries.go:
	// the fs_id is the SHA-1 of the serialized directory content ("1\n[]").
	// Previously this used a hardcoded all-zeros ID (fmt.Sprintf("%040x", 0)),
	// which caused special-casing issues throughout the codebase because the
	// all-zeros ID doesn't exist as a real fs_object and required checks
	// in CheckFS, ListDirectory, and GetFSIDList to avoid errors.
	emptyDirEntries := "[]"
	emptyDirData := fmt.Sprintf("%d\n%s", 1, emptyDirEntries) // Seafile format: version + entries
	emptyDirHash := sha1.Sum([]byte(emptyDirData))
	rootID := hex.EncodeToString(emptyDirHash[:])

	// Store the empty root FS object
	err := h.db.Session().Query(`
		INSERT INTO fs_objects (library_id, fs_id, obj_type, obj_name, dir_entries, size_bytes, mtime)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, repoID, rootID, "dir", "", emptyDirEntries, 0, now.Unix()).Exec()
	if err != nil {
		return "", fmt.Errorf("failed to create root fs object: %w", err)
	}

	// Create initial commit
	// Commit ID is a hash of the content - use deterministic ID for initial (40 chars like SHA-1)
	commitID := sha1Hex(fmt.Sprintf("%s-%s-%d", repoID, rootID, now.Unix()))

	err = h.db.Session().Query(`
		INSERT INTO commits (library_id, commit_id, parent_id, root_fs_id, creator_id, description, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, repoID, commitID, "", rootID, userID, "Initial commit", now).Exec()
	if err != nil {
		return "", fmt.Errorf("failed to create initial commit: %w", err)
	}

	// Update library's head_commit_id with stats recalculation
	batch := h.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		UPDATE libraries SET head_commit_id = ?, root_commit_id = ?, size_bytes = ?, file_count = ?, updated_at = ?
		WHERE org_id = ? AND library_id = ?
	`, commitID, commitID, int64(0), int64(0), now, orgID, repoID)
	batch.Query(`
		UPDATE libraries_by_id SET head_commit_id = ?
		WHERE library_id = ?
	`, commitID, repoID)
	if err := batch.Exec(); err != nil {
		return "", fmt.Errorf("failed to update library head: %w", err)
	}

	return commitID, nil
}

// sha1Hex returns the SHA1 hash of a string as hex (40 chars, Seafile compatible)
func sha1Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	// Return only first 40 chars to match Seafile's SHA-1 format
	return hex.EncodeToString(h[:20])
}

// GetCommit returns a specific commit object
// GET /seafhttp/repo/:repo_id/commit/:commit_id
func (h *SyncHandler) GetCommit(c *gin.Context) {
	repoID := c.Param("repo_id")
	commitID := c.Param("commit_id")
	orgID := c.GetString("org_id")

	if !h.checkSyncPermission(c, repoID, middleware.PermissionR) {
		return
	}

	// Query commit from database
	var commit Commit
	var parentID, rootID, description, creator string
	var ctime time.Time

	err := h.db.Session().Query(`
		SELECT commit_id, parent_id, root_fs_id, description, creator_id, created_at
		FROM commits WHERE library_id = ? AND commit_id = ?
	`, repoID, commitID).Scan(
		&commit.CommitID, &parentID, &rootID, &description, &creator, &ctime,
	)

	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "commit not found"})
		return
	}

	// Get library info for repo_name, repo_desc, and encryption info
	var repoName, repoDesc string
	var encrypted bool
	var encVersion int
	var magic, randomKey string
	h.db.Session().Query(`
		SELECT name, description, encrypted, enc_version, magic, random_key
		FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&repoName, &repoDesc, &encrypted, &encVersion, &magic, &randomKey)

	commit.RepoID = repoID

	// CRITICAL: Use the STORED root_fs_id from database, not a computed one
	// Computing a "corrected" fs_id breaks sync because the client requests
	// fs_objects using IDs that don't exist in the database.
	// The stored fs_id is what was originally created and matches database records.
	commit.RootID = rootID

	commit.Description = description
	// Seafile uses 40 zeros for creator ID format
	commit.Creator = strings.Repeat("0", 40)
	commit.CreatorName = creator + "@sesamefs.local"
	commit.Ctime = ctime.Unix()
	commit.Version = 1 // Seafile commit format version 1
	commit.RepoName = repoName
	commit.RepoDesc = "" // Seafile returns empty string in commit objects

	// Add encryption fields if library is encrypted
	if encrypted {
		commit.Encrypted = "true" // Seafile uses string "true" not boolean
		// Return enc_version 2 for Seafile client compatibility (we store 12 for dual-mode)
		commit.EncVersion = 2
		commit.Magic = magic
		commit.Key = randomKey // Seafile uses "key" in commit response
		// NOTE: no_local_history is NOT included by stock Seafile server
	}

	// Set pointer fields - null if empty, pointer to value otherwise
	if parentID == "" {
		commit.ParentID = nil
	} else {
		commit.ParentID = &parentID
	}
	commit.SecondParentID = nil // Always null for now

	// CRITICAL: Seafile returns empty string for repo_category, not null
	emptyCategory := ""
	commit.RepoCategory = &emptyCategory

	// Return commit as JSON
	c.JSON(http.StatusOK, commit)
}

// PutCommit stores a new commit object or updates the HEAD pointer
// PUT /seafhttp/repo/:repo_id/commit/:commit_id
// PUT /seafhttp/repo/:repo_id/commit/HEAD?head=<commit_id> (update HEAD pointer)
func (h *SyncHandler) PutCommit(c *gin.Context) {
	repoID := c.Param("repo_id")
	commitID := c.Param("commit_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	if !h.checkSyncPermission(c, repoID, middleware.PermissionRW) {
		return
	}

	// Special case: PUT /commit/HEAD?head=<commit_id> updates the HEAD pointer
	if commitID == "HEAD" {
		headCommitID := c.Query("head")
		if headCommitID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing head parameter"})
			return
		}

		h.handleSyncHeadPromotion(c, orgID, userID, repoID, headCommitID, "PutCommit HEAD")
		return
	}

	// Read commit data from body
	body, ok := readLimitedRequestBody(c, maxPutCommitBodyBytes)
	if !ok {
		return
	}

	var commit Commit
	if err := json.Unmarshal(body, &commit); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid commit format"})
		return
	}

	// Verify commit ID matches
	if commit.CommitID != "" && commit.CommitID != commitID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "commit ID mismatch"})
		return
	}

	// Store commit in database
	now := time.Now()
	err := h.db.Session().Query(`
		INSERT INTO commits (library_id, commit_id, parent_id, root_fs_id, creator_id, description, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, repoID, commitID, commit.ParentID, commit.RootID, userID, commit.Description, now).Exec()

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to store commit"})
		return
	}

	// NOTE: Do NOT update HEAD here. The Seafile protocol has a separate step
	// (PUT /commit/HEAD or POST /update-branch) to advance HEAD. Updating HEAD
	// on every commit store causes race conditions where a stale/retried commit
	// from the desktop client can overwrite a HEAD that was advanced by web uploads.
	log.Printf("PutCommit: stored commit %s for repo %s (parent=%v, root=%s)",
		commitID, repoID, commit.ParentID, commit.RootID)

	c.Status(http.StatusOK)
}

// GetBlock retrieves a block by ID
// GET /seafhttp/repo/:repo_id/block/:block_id
// Supports both SHA-1 (40 chars, Seafile legacy) and SHA-256 (64 chars, new clients)
func (h *SyncHandler) GetBlock(c *gin.Context) {
	repoID := c.Param("repo_id")
	externalID := c.Param("block_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	if !h.checkSyncPermission(c, repoID, middleware.PermissionR) {
		return
	}

	// Client-facing readers are strict: only hex SHA-1 or SHA-256 ids are valid.
	classifiedID, err := classifyClientReadableBlockID(externalID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid block id"})
		return
	}
	var internalID string

	if h.db != nil && classifiedID.isLegacySHA1 {
		representationID, err := h.resolveSyncBlockRepresentationID(orgID, repoID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve block mapping"})
			return
		}
		mappedID, ok, err := h.db.GetBlockIDMapping(orgID, representationID, classifiedID.normalized)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve block mapping"})
			return
		}
		if !ok || strings.TrimSpace(mappedID) == "" {
			c.JSON(http.StatusNotFound, gin.H{"error": "block not found"})
			return
		}
		internalID, err = normalizeResolvedInternalBlockID(mappedID)
		if err != nil {
			log.Printf("GetBlock: invalid mapped internal id for %s: %q", externalID, mappedID)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve block mapping"})
			return
		}
		log.Printf("GetBlock: resolved %s → %s\n", externalID, internalID)
	} else {
		internalID = classifiedID.normalized
	}

	lifecycle, ok := h.acquireSyncBlockDownloadAdmission(c, orgID, userID)
	if !ok {
		return
	}
	defer lifecycle.FinishHandler()
	preparationCtx := lifecycle.PreparationContext()

	var (
		blockSize  int64
		openReader func(context.Context) (io.ReadCloser, error)
	)
	if h.db != nil {
		var fallbackStore *storage.BlockStore
		var fallbackClass string
		if h.storageManager == nil {
			fallbackStore, fallbackClass, err = h.resolvePreferredBlockStore(c, orgID, repoID)
			if err != nil || fallbackStore == nil {
				lifecycle.ReleasePreparationError(err)
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "block storage not available"})
				return
			}
		}
		reader, resolveErr := syncNewCanonicalBlockReaderFn(preparationCtx, h.db, h.storageManager, orgID, []string{internalID}, fallbackStore, fallbackClass)
		if resolveErr != nil {
			lifecycle.ReleasePreparationError(resolveErr)
			if errors.Is(resolveErr, streaming.ErrCanonicalBlockMetadataNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "block not found"})
			} else {
				log.Printf("GetBlock: failed to resolve canonical block %s (internal: %s): %v", externalID, internalID, resolveErr)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve block storage"})
			}
			return
		}
		blockSize, err = reader.GetBlockSize(preparationCtx, internalID)
		if err != nil {
			lifecycle.ReleasePreparationError(err)
			if isSyncCanonicalBlockNotFound(err) {
				log.Printf("GetBlock: block %s (internal: %s) not found: %v\n", externalID, internalID, err)
				c.JSON(http.StatusNotFound, gin.H{"error": "block not found"})
			} else {
				log.Printf("GetBlock: failed to size canonical block %s (internal: %s): %v\n", externalID, internalID, err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read block storage"})
			}
			return
		}
		openReader = func(ctx context.Context) (io.ReadCloser, error) {
			return reader.GetBlockReader(ctx, internalID)
		}
	} else {
		// Preserve the legacy no-metadata routed fallback.
		storageClass := h.resolveBlockLookupFallbackClass(c, orgID, repoID, "")
		blockStore, _, resolveErr := h.resolveBlockStoreForLookup(c, orgID, repoID, storageClass)
		if resolveErr != nil || blockStore == nil {
			lifecycle.ReleasePreparationError(resolveErr)
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "block storage not available"})
			return
		}
		blockSize, err = blockStore.GetBlockSize(preparationCtx, internalID)
		if err != nil {
			lifecycle.ReleasePreparationError(err)
			log.Printf("GetBlock: block %s (internal: %s) not found: %v\n", externalID, internalID, err)
			c.JSON(http.StatusNotFound, gin.H{"error": "block not found"})
			return
		}
		openReader = func(ctx context.Context) (io.ReadCloser, error) {
			return blockStore.GetBlockReader(ctx, internalID)
		}
	}

	// NOTE: For encrypted libraries, blocks are stored encrypted:
	// - Sync protocol: Client encrypts blocks locally before upload, server stores as-is
	// - Web uploads: Server encrypts blocks before storage
	// In both cases, blocks are returned as-is - NO re-encryption needed.
	// The client will decrypt using its locally-derived file key.

	// Quota pre-check: reject if download traffic quota exceeded.
	downloadTrafficStatus := traffic.QuotaStatus{Allowed: true}
	if checker := traffic.GetChecker(); checker != nil {
		// Bound to the preparation deadline: the quota lookup is Cassandra work
		// done inside the slot, so it must not outlive the admitted preparation
		// phase the way a context.Background() lookup would.
		downloadTrafficStatus, _ = checker.CheckTrafficQuotaContext(preparationCtx, orgID, userID, "download", blockSize)
		if !downloadTrafficStatus.Allowed {
			c.JSON(http.StatusForbidden, gin.H{"error": "download traffic quota exceeded"})
			return
		}
	}

	// Update last accessed time (if DB available) after quota and before the body.
	if h.db != nil {
		syncTouchBlockLastAccessFn(preparationCtx, h.db, orgID, internalID, time.Now())
	}

	streamCtx, err := lifecycle.StartStreaming()
	if err != nil {
		if !c.Writer.Written() {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "block download temporarily unavailable"})
		}
		return
	}

	blockReader, err := openReader(streamCtx)
	if err != nil {
		lifecycle.FailStreamError(syncBlockDownloadStreamCause(err))
		if !c.Writer.Written() {
			if h.db != nil && !isSyncCanonicalBlockNotFound(err) {
				log.Printf("GetBlock: failed to open canonical block %s (internal: %s): %v\n", externalID, internalID, err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read block storage"})
			} else {
				log.Printf("GetBlock: block %s (internal: %s) not found: %v\n", externalID, internalID, err)
				c.JSON(http.StatusNotFound, gin.H{"error": "block not found"})
			}
		}
		return
	}
	defer blockReader.Close()

	c.Header("Content-Type", "application/octet-stream")
	if blockSize > 0 {
		c.Header("Content-Length", strconv.FormatInt(blockSize, 10))
	}
	c.Status(http.StatusOK)

	bytesBefore := int64(c.Writer.Size())
	copied, copyErr := io.Copy(c.Writer, blockReader)
	bytesWritten := httputil.ResponseBytesSince(c.Writer, bytesBefore)
	if bytesWritten > 0 {
		recordSyncBlockDownloadTrafficFn(downloadTrafficStatus, orgID, userID, bytesWritten)
	}
	// A reader that reaches EOF early returns no error, so a short block would
	// otherwise be released as `completed` while the client received fewer bytes
	// than the Content-Length promised. Content addressing makes that divergence
	// extraordinary; the check is what turns that structural expectation into a
	// verified invariant instead of an assumption.
	if copyErr == nil && copied != blockSize {
		copyErr = fmt.Errorf("block size mismatch: streamed %d bytes, expected %d", copied, blockSize)
	}
	if copyErr != nil {
		lifecycle.FailStreamError(syncBlockDownloadStreamCause(copyErr))
		log.Printf("GetBlock: stream failed for %s (internal: %s) after %d bytes: %v\n", externalID, internalID, bytesWritten, copyErr)
	}
}

// acquireSyncBlockDownloadAdmission starts the shared D5 lifetime after the
// endpoint's authentication, permission, and cheap mapping gates have passed.
func (h *SyncHandler) acquireSyncBlockDownloadAdmission(c *gin.Context, orgID, userID string) (*httputil.DownloadAdmission, bool) {
	cfg := config.DownloadAdmissionConfig{}
	if h != nil && h.config != nil {
		cfg = h.config.DownloadAdmission
	}

	request := downloadadmission.AdmissionRequest{}
	if cfg.Enabled {
		var err error
		request, err = downloadadmission.NewAuthenticatedRequest(downloadadmission.ProfileBlock, orgID, userID)
		if err != nil {
			log.Printf("GetBlock: build download admission request: %v", err)
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "block download temporarily unavailable"})
			return nil, false
		}
	}

	lifecycle, reason, err := httputil.AcquireDownloadAdmission(c, h.downloadAdmission, cfg, request)
	if err != nil {
		log.Printf("GetBlock: acquire download admission: %v", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "block download temporarily unavailable"})
		return nil, false
	}
	if reason != "" {
		httputil.RenderDownloadAdmissionRefusal(c, h.downloadAdmission)
		return nil, false
	}
	return lifecycle, true
}

func syncBlockDownloadStreamCause(err error) downloadadmission.ReleaseCause {
	if err == nil {
		return downloadadmission.ReleaseResponseError
	}
	if errors.Is(err, streaming.ErrStreamResponse) {
		return downloadadmission.ReleaseResponseError
	}
	if errors.Is(err, context.Canceled) {
		return downloadadmission.ReleaseClientDisconnect
	}
	return downloadadmission.ReleaseStorageError
}

// Body-size bounds for the legacy/sync seafhttp routes. Before these, PutBlock and
// CheckBlocks read the whole request body with an unbounded io.ReadAll, so a client
// could drive memory use arbitrarily high with one oversized body or a huge id list
// (F12). The check-blocks caps bound both the raw body and the number of ids parsed
// from it; the block cap is now configuration, see syncBlockMaxBytes below.
const (
	maxCheckBlocksBodyBytes = 16 * 1024 * 1024 // 16 MiB

	// PutCommit, PackFS and CheckFS were the other three handlers still reading
	// an unbounded body (ISSUE-SYNC-UNBOUNDED-BODIES-01 / X9) — the same defect
	// F12 fixed for PutBlock/CheckBlocks. Plain consts, not configuration: a
	// commit object and an id list (PackFS/CheckFS request the same 40-char hex
	// fs-id shape check-blocks does) are small, protocol-shaped payloads, not a
	// variable-sized bulk payload like recv-fs below.
	maxPutCommitBodyBytes = 1 * 1024 * 1024  // 1 MiB
	maxPackFSBodyBytes    = 16 * 1024 * 1024 // 16 MiB, same shape as check-blocks
	maxCheckFSBodyBytes   = 16 * 1024 * 1024 // 16 MiB, same shape as check-blocks

	// minFSIDWireBytes is the smallest on-the-wire cost of one well-formed 40-hex
	// fs id, which the newline format sets: 40 characters plus one delimiter. JSON
	// is strictly less dense — each id is also quoted, so ~43 bytes — and therefore
	// cannot bind first; TestFSIDCapsCannotCutWellFormedBodies asserts both.
	//
	// Deriving the id caps below from the byte caps above makes them unreachable for
	// any well-formed body — 16 MiB cannot carry more ids than this — so they only
	// ever fire on degenerate input, which is precisely the amplification they exist
	// to stop and not a new limit on real clients.
	//
	// Without them, a body *under* the byte cap still expands ~17x: 16 MiB of bare
	// newlines becomes ~16.7M string headers (~268 MB). See parseBoundedIDList.
	minFSIDWireBytes = 41
	maxPackFSIDs     = maxPackFSBodyBytes / minFSIDWireBytes
	maxCheckFSIDs    = maxCheckFSBodyBytes / minFSIDWireBytes
)

// checkBlocksMaxIDs resolves the accepted id-count cap.
//
// The nil-config fallback is the package default rather than "unlimited", for
// the same reason as syncBlockMaxBytes: a handler without config is a wiring
// bug, and failing open here would restore the unbounded list the cap exists to
// prevent.
func (h *SyncHandler) checkBlocksMaxIDs() int {
	if h == nil || h.config == nil || h.config.SeafHTTP.CheckBlocksMaxIDs <= 0 {
		return config.DefaultCheckBlocksMaxIDs
	}
	return h.config.SeafHTTP.CheckBlocksMaxIDs
}

// checkBlocksLookupFanout resolves the per-request metadata concurrency. It
// bounds both lookup phases, so the node's aggregate budget is
// (check_blocks_max_inflight_per_node x this value) — the product validation
// enforces at boot.
func (h *SyncHandler) checkBlocksLookupFanout() int {
	if h == nil || h.config == nil || h.config.SeafHTTP.CheckBlocksLookupFanout <= 0 {
		return config.DefaultCheckBlocksLookupFanout
	}
	return h.config.SeafHTTP.CheckBlocksLookupFanout
}

// syncBlockMaxBytes resolves the per-request body cap for PutBlock.
//
// It used to be a 257 MiB constant derived from the web uploader's 256 MiB
// adaptive-chunk ceiling — the wrong domain: that ceiling governs browser
// chunking and never applied to this route. The 16 MiB default leaves ample
// headroom over the official client's 4 MiB CDC maximum and SesameFS's related
// 8 MiB server-side split; operators can raise it up to the validated ceiling.
//
// The nil-config fallback is the package default rather than something
// permissive: a handler without config is a wiring bug, and failing open here
// would restore the unbounded read F12 exists to prevent.
func (h *SyncHandler) syncBlockMaxBytes() int64 {
	if h == nil || h.config == nil || h.config.SeafHTTP.SyncBlockMaxBytes <= 0 {
		return config.DefaultSyncBlockMaxBytes
	}
	return h.config.SeafHTTP.SyncBlockMaxBytes
}

// syncRecvFSMaxBytes resolves the per-request body cap for RecvFS.
//
// Unlike maxPutCommitBodyBytes/maxPackFSBodyBytes/maxCheckFSBodyBytes above,
// this is configuration rather than a const: recv-fs carries a real batch of
// packed FS objects, not a small id list, and there is no measured client
// batch size or protocol-documented ceiling to anchor a fixed number on. The
// default is deliberately generous so it is unlikely to cut a legitimate large
// commit; an operator can raise it further without a code change.
//
// The nil-config fallback is the package default rather than something
// permissive, for the same reason as syncBlockMaxBytes: a handler without
// config is a wiring bug, and failing open here would restore the unbounded
// read this cap exists to prevent.
func (h *SyncHandler) syncRecvFSMaxBytes() int64 {
	if h == nil || h.config == nil || h.config.SeafHTTP.RecvFSMaxBytes <= 0 {
		return config.DefaultRecvFSMaxBytes
	}
	return h.config.SeafHTTP.RecvFSMaxBytes
}

func (h *SyncHandler) syncBlockAdmittedLifetime() time.Duration {
	if h == nil || h.config == nil || h.config.SeafHTTP.SyncBlockAdmittedLifetime <= 0 {
		return config.DefaultSyncBlockAdmittedLifetime
	}
	return h.config.SeafHTTP.SyncBlockAdmittedLifetime
}

func (h *SyncHandler) checkBlocksAdmittedLifetime() time.Duration {
	if h == nil || h.config == nil || h.config.SeafHTTP.CheckBlocksAdmittedLifetime <= 0 {
		return config.DefaultCheckBlocksAdmittedLifetime
	}
	return h.config.SeafHTTP.CheckBlocksAdmittedLifetime
}

// syncAdmittedLifetime is one route's processing budget plus everything needed
// to report exceeding it. Like the admission limiter, the mechanism is shared
// between the block and check-blocks routes while the numbers and the series
// stay separate, so an operator can always tell which route ran out of time.
type syncAdmittedLifetime struct {
	duration             time.Duration
	timeouts             *prometheus.CounterVec
	deadlineUnsupported  prometheus.Counter
	unprotectedLogSample *rate.Sometimes
	logPrefix            string
	// noun names the work in client-facing messages ("block upload"), so a
	// timeout says which request died rather than "the request".
	// downstreamNoun names what timed out when the deadline came from a backend
	// rather than from this request's own budget, which is a different thing to
	// tell an operator reading a client log.
	noun           string
	downstreamNoun string
}

func (h *SyncHandler) syncBlockLifetime() syncAdmittedLifetime {
	return syncAdmittedLifetime{
		duration:             h.syncBlockAdmittedLifetime(),
		timeouts:             metrics.SyncPutBlockTimeoutsTotal,
		deadlineUnsupported:  metrics.SyncPutBlockReadDeadlineUnsupportedTotal,
		unprotectedLogSample: &syncPutBlockUnprotectedLogSample,
		logPrefix:            "[PutBlock]",
		noun:                 "block upload",
		downstreamNoun:       "block storage",
	}
}

func (h *SyncHandler) checkBlocksLifetime() syncAdmittedLifetime {
	return syncAdmittedLifetime{
		duration:             h.checkBlocksAdmittedLifetime(),
		timeouts:             metrics.SyncCheckBlocksTimeoutsTotal,
		deadlineUnsupported:  metrics.SyncCheckBlocksReadDeadlineUnsupportedTotal,
		unprotectedLogSample: &syncCheckBlocksUnprotectedLogSample,
		logPrefix:            "[CheckBlocks]",
		noun:                 "check-blocks request",
		downstreamNoun:       "check-blocks metadata lookup",
	}
}

var errSyncBlockAdmittedLifetime = errors.New("sync block admitted lifetime exceeded")

// beginSyncBlockAdmittedLifetime starts after admission so queue time does not
// consume the processing budget. Replacing the request context propagates the
// deadline to object-storage I/O; Cassandra callback boundaries also check it,
// while each already-running query remains bounded by the driver's required
// positive timeout.
//
// The request body needs a *connection* deadline, not only a context. Cancelling
// the context cannot interrupt a handler already parked inside a body read:
// net/http's body Read and Close share one mutex, so the reader holds it while
// waiting on the socket and any Close from another goroutine blocks behind it
// rather than unblocking it — and with server.read_timeout deliberately 0 (large
// uploads legitimately take minutes) nothing else would ever break the park. A
// stalled peer would hold its admission indefinitely, which is far more damaging
// now that the admission is a scarce resource: a handful of stalled but
// authenticated connections would deny block upload to every other client on the
// node. SetReadDeadline reaches the connection and makes the pending read fail,
// which is the only mechanism that actually bounds this.
//
// The server rearms its own read deadline at the start of every request on a
// keep-alive connection, so the deadline set here cannot leak into the next one.
func (h *SyncHandler) beginAdmittedLifetime(c *gin.Context, lt syncAdmittedLifetime) (func(), bool) {
	startedAt := time.Now()
	admittedDeadline := startedAt.Add(lt.duration)
	ctx, cancel := context.WithDeadlineCause(c.Request.Context(), admittedDeadline, errSyncBlockAdmittedLifetime)
	c.Request = c.Request.WithContext(ctx)
	effectiveDeadline, _ := ctx.Deadline()

	// Preserve a configured server deadline when its duration is already no
	// greater than the effective request budget. Otherwise an earlier inherited
	// request deadline must reach the socket; context cancellation alone cannot
	// interrupt net/http's blocked body read.
	serverDeadlineIsStronger := h != nil && h.config != nil && h.config.Server.ReadTimeout > 0 &&
		h.config.Server.ReadTimeout <= effectiveDeadline.Sub(startedAt)
	if !serverDeadlineIsStronger {
		if err := http.NewResponseController(c.Writer).SetReadDeadline(effectiveDeadline); err != nil {
			// On a real connection this is the whole guard failing, not a
			// degraded mode: without a socket deadline nothing can interrupt a
			// parked body read, so a stalled peer would hold its admission until
			// the process restarts. That is how a middleware wrapping c.Writer
			// without Unwrap() — gin-contrib/gzip is exactly such a wrapper —
			// would silently disarm subcontract B. Refuse the request instead,
			// loudly — drop the connection and increment a counter — rather than
			// a capacity leak nobody notices. A 503 is not deliverable here while
			// the unread body remains undrained without a socket deadline.
			if isServerHandledRequest(c.Request) {
				cancel()
				rejectAdmittedUnprotected(c, lt)
				return nil, false
			}
			// Synthetic writers (test recorders, exotic ResponseWriters) never
			// had a connection to begin with. Their bodies are ordinary readers
			// whose Close is not bound to net/http's read mutex, so closing on
			// cancellation genuinely does end them.
			return h.closeBodyOnCancel(c, ctx, cancel), true
		}
		return cancel, true
	}
	// An earlier server read timeout already owns the socket and is stricter, so
	// overwriting it would weaken the bound. Keep the body-close fallback for the
	// non-connection cases that still reach here.
	return h.closeBodyOnCancel(c, ctx, cancel), true
}

func (h *SyncHandler) closeBodyOnCancel(c *gin.Context, ctx context.Context, cancel context.CancelFunc) func() {
	body := c.Request.Body
	stopClose := context.AfterFunc(ctx, func() {
		if body != nil {
			_ = body.Close()
		}
	})
	return func() {
		stopClose()
		cancel()
	}
}

// isServerHandledRequest reports whether the request was dispatched by net/http's
// server, which is the only case that has a connection to put a deadline on.
// net/http sets LocalAddrContextKey on every served request; httptest recorders
// and hand-built contexts do not.
func isServerHandledRequest(r *http.Request) bool {
	if r == nil || r.Context() == nil {
		return false
	}
	return r.Context().Value(http.LocalAddrContextKey) != nil
}

// rejectAdmittedUnprotected ends a request whose admitted lifetime could not be
// installed on the connection.
//
// It drops the connection instead of answering 503, because a 503 here is not
// deliverable: the peer announced a body it has not finished sending, and
// net/http drains the unread remainder before the response reaches the socket.
// With no deadline installed, that drain is precisely the unbounded wait being
// refused — the handler would return, the admission would be released, and the
// server goroutine would still sit in the drain forever. Closing the connection
// is the only action that actually ends the situation, and the sync client
// classifies a dropped connection as a transient network error and retries,
// which is the same contract the unreachable 503 would have carried.
//
// The log is sampled: a middleware that broke the unwrap chain would otherwise
// make every affected request write a line.
func rejectAdmittedUnprotected(c *gin.Context, lt syncAdmittedLifetime) {
	lt.deadlineUnsupported.Inc()
	lt.unprotectedLogSample.Do(func() {
		log.Printf("%s dropping %s: the admitted-lifetime read deadline could not be installed on the connection, so a stalled body could hold this admission forever. A middleware is wrapping the response writer without implementing Unwrap(); sampled, see the read_deadline_unsupported_total series for this route", lt.logPrefix, lt.noun)
	})
	c.Abort()
	// gin.ResponseWriter embeds http.Hijacker, so this works even through a
	// wrapper that hid SetReadDeadline from us.
	if conn, _, err := c.Writer.Hijack(); err == nil {
		_ = conn.Close()
		return
	}
	// Nothing to hijack. Say something rather than nothing; if the peer has
	// already sent its whole body this is deliverable, and if it has not, this
	// is no worse than the silence.
	c.Header("Retry-After", "1")
	c.Header("Connection", "close")
	c.Request.Close = true
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": lt.noun + " cannot be time-bounded on this connection; retry"})
}

var (
	syncPutBlockUnprotectedLogSample    = rate.Sometimes{Interval: time.Minute}
	syncCheckBlocksUnprotectedLogSample = rate.Sometimes{Interval: time.Minute}
)

func rejectAdmittedTimeout(c *gin.Context, lt syncAdmittedLifetime, phase string, err error) bool {
	ctxErr := c.Request.Context().Err()
	if errors.Is(context.Cause(c.Request.Context()), errSyncBlockAdmittedLifetime) {
		lt.timeouts.WithLabelValues(phase).Inc()
		c.Header("Retry-After", "1")
		c.Header("Connection", "close")
		c.Request.Close = true
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": lt.noun + " timed out; retry"})
		return true
	}
	// netpoll may report the socket deadline just before the context timer runs.
	// net/http can also cancel the parent request context from that read error
	// before our child timer installs its cause, so this must precede ctxErr.
	// Limit it to body reads: storage transports have independent socket deadlines
	// and can return the same os sentinel while this request is otherwise healthy.
	if phase == "body" && errors.Is(err, os.ErrDeadlineExceeded) {
		lt.timeouts.WithLabelValues(phase).Inc()
		c.Header("Retry-After", "1")
		c.Header("Connection", "close")
		c.Request.Close = true
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": lt.noun + " read timed out; retry"})
		return true
	}
	// An inherited client deadline/disconnect gets no response: the peer has
	// already abandoned the request. Do not infer that from err alone because
	// storage transports have independent deadlines that can return the same
	// sentinel while the request itself is still healthy.
	if ctxErr != nil {
		c.Abort()
		return true
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		lt.timeouts.WithLabelValues(phase).Inc()
		c.Header("Retry-After", "1")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": lt.downstreamNoun + " timed out; retry"})
		return true
	}
	return false
}

// readLimitedSyncBlockRequestBody reads an admitted body and, on failure,
// returns the SyncPutBlockRejectedTotal reason the caller should record. That
// reason is empty when the failure was already accounted elsewhere — a timeout
// counted under sync_put_block_timeouts_total, or a client that vanished and got
// no response — because folding those into "read_error" would make the size-cap
// versus read-failure dial read as malformed traffic during ordinary timeouts.
func readLimitedAdmittedRequestBody(c *gin.Context, lt syncAdmittedLifetime, maxBytes int64) ([]byte, string, bool) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
	data, err := io.ReadAll(c.Request.Body)
	if err == nil {
		return data, "", true
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "request body too large", "max_bytes": maxBytes})
		return nil, "too_large", false
	}
	if rejectAdmittedTimeout(c, lt, "body", err) {
		return nil, "", false
	}
	c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read request body"})
	return nil, "read_error", false
}

// PutBlock stores a block
// PUT /seafhttp/repo/:repo_id/block/:block_id
// Supports both SHA-1 (40 chars, Seafile legacy) and SHA-256 (64 chars, new clients)
// Internally always stores blocks using SHA-256 for consistency
func (h *SyncHandler) PutBlock(c *gin.Context) {
	repoID := c.Param("repo_id")
	externalID := c.Param("block_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")
	hashType := c.DefaultQuery("hash_type", "") // Optional: "sha256" for new clients

	if !h.checkSyncPermission(c, repoID, middleware.PermissionRW) {
		return
	}

	log.Printf("PutBlock: externalID=%s, len=%d\n", externalID, len(externalID))

	// Quota pre-check: reject early on upload traffic; storage quota is enforced
	// only if this block is new after deduplication lookup.
	uploadTrafficStatus := traffic.QuotaStatus{Allowed: true}
	if checker := getAPIQuotaChecker(); checker != nil {
		contentLen := c.Request.ContentLength
		if contentLen > 0 {
			uploadTrafficStatus, _ = traffic.CheckTrafficQuotaWithChecker(checker, orgID, userID, "upload", contentLen)
			if !uploadTrafficStatus.Allowed {
				c.JSON(http.StatusForbidden, gin.H{"error": "upload traffic quota exceeded"})
				return
			}
		}
	}

	classifiedID, err := classifySyncUploadBlockID(externalID, hashType)
	if err != nil {
		log.Printf("PutBlock: invalid block id %q (hash_type=%q)\n", externalID, hashType)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid block id"})
		return
	}

	maxBlockBytes := h.syncBlockMaxBytes()

	// Reject an oversized block before reading it. The declared length is a fast
	// path for honest clients; the MaxBytesReader in readLimitedRequestBody is the
	// hard enforcement for a chunked or lying ContentLength.
	if c.Request.ContentLength > maxBlockBytes {
		metrics.SyncPutBlockRejectedTotal.WithLabelValues("too_large").Inc()
		log.Printf("PutBlock: declared body %d exceeds max %d for block %s\n", c.Request.ContentLength, maxBlockBytes, externalID)
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "block too large", "max_bytes": maxBlockBytes})
		return
	}

	// Take an in-flight admission before anything buffers the body. Everything
	// above this point is a cheap rejection — no permission, an unparseable id,
	// an over-cap declared length — and none of it should wait for a slot or
	// consume one.
	//
	// The admission is held until the handler returns, not until the read
	// finishes: `data` stays live through hashing and the storage write, and that
	// whole span is what the memory bound covers.
	releaseInflight, admitted := h.acquireSyncBlockInflight(c, orgID, userID)
	if !admitted {
		return
	}
	defer releaseInflight()
	lifetime := h.syncBlockLifetime()
	endLifetime, protected := h.beginAdmittedLifetime(c, lifetime)
	if !protected {
		return
	}
	defer endLifetime()

	// Read block data (bounded per request; bounded in aggregate by the
	// admission above)
	data, rejectReason, ok := readLimitedAdmittedRequestBody(c, lifetime, maxBlockBytes)
	if !ok {
		// The body reader has already written 413 (over the cap), 503 (admitted
		// lifetime), or 400 (another read failure), and reports which of those is
		// a SyncPutBlockRejectedTotal reason. An empty reason means it was
		// already counted under a metric that fits it better.
		if rejectReason != "" {
			metrics.SyncPutBlockRejectedTotal.WithLabelValues(rejectReason).Inc()
		}
		return
	}

	// Observed after the read succeeds, so the histogram describes request bodies
	// that passed the size gate. Rejected oversize attempts are counted separately
	// above; mixing them in would bias the distribution used to size the cap.
	metrics.SyncPutBlockBodyBytes.Observe(float64(len(data)))

	log.Printf("PutBlock: received %d bytes for block %s\n", len(data), externalID)
	if rejectAdmittedTimeout(c, lifetime, "hash", c.Request.Context().Err()) {
		return
	}

	// Always compute SHA-256 as the internal storage ID
	sha256Hash := sha256.Sum256(data)
	internalID := hex.EncodeToString(sha256Hash[:])
	if rejectAdmittedTimeout(c, lifetime, "hash", c.Request.Context().Err()) {
		return
	}

	// Verify hash for SHA-256 clients
	if classifiedID.isDirectSHA256 && classifiedID.normalized != internalID {
		log.Printf("PutBlock: SHA-256 hash mismatch, expected %s got %s\n", externalID, internalID)
		c.JSON(http.StatusBadRequest, gin.H{"error": "block hash mismatch"})
		return
	}

	blockStore, storageClass, err := h.resolvePreferredBlockStore(c, orgID, repoID)
	if err != nil || blockStore == nil {
		log.Printf("PutBlock: block storage not available\n")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "block storage not available"})
		return
	}

	log.Printf("PutBlock: storing block external=%s internal=%s in storage class %s\n",
		externalID, internalID, storageClass)

	// Store block using internal SHA-256 ID
	blockData := &storage.BlockData{
		Data: data,
		Hash: internalID, // Always use SHA-256 for storage
	}
	errSyncStorageQuotaExceeded := errors.New("sync storage quota exceeded")
	errSyncBlockExistenceCheck := errors.New("sync block existence check failed")
	errSyncStoreBackend := errors.New("sync store backend failed")

	// Store block metadata and mapping (if DB available)
	if h.db != nil {
		// Store block metadata + a deterministic provisional reference so the
		// later sync head publish can promote or release exactly this upload pin,
		// then write the legacy SHA-1 mapping only if the client needs it.
		operationID := syncBlockUploadOperationID(repoID, internalID)
		externalMappingID := ""
		if classifiedID.isLegacySHA1 {
			externalMappingID = classifiedID.normalized
		}
		materializedStorageClass := storageClass
		// IMPORTANT: this store callback can be re-invoked by
		// RetryUploadedBlockMaterialization (the retry path fires when store or
		// materialize returns a retryable sentinel: ErrBlockDeleteInProgress or
		// ErrBlockMaterializationTransient). It is therefore only safe to write a
		// gin response (c.JSON) here because every branch that writes one returns a
		// NON-retryable sentinel (errSyncStorageQuotaExceeded,
		// errSyncBlockExistenceCheck, errSyncStoreBackend) — those short-circuit
		// the retry loop, so the callback runs exactly once and never double-writes.
		// The retryable returns write no response. If you add a new response-writing
		// branch, it MUST return a non-retryable error or the response will be
		// written twice on retry.
		if err := syncRetryUploadedBlockMaterializationFn(c.Request.Context(), "PutBlock", internalID, func() error {
			if err := c.Request.Context().Err(); err != nil {
				return err
			}
			materializedStorageClass = storageClass
			probe, probeErr := syncProbeUploadedBlockReuseFn(h.db, orgID, internalID)
			if probeErr != nil {
				return fmt.Errorf("probe block reuse for %s: %w", internalID, probeErr)
			}
			if err := c.Request.Context().Err(); err != nil {
				return err
			}
			probe, probeErr = syncPrepareUploadedBlockProbeFn(h.db, orgID, internalID, probe)
			if probeErr != nil {
				return probeErr
			}
			if err := c.Request.Context().Err(); err != nil {
				return err
			}
			switch probe.Decision {
			case db.BlockReuseReusable:
				materializedStorageClass = strings.TrimSpace(probe.StorageClass)
				_, ensureErr := syncEnsureReusableBlockPresentFn(c.Request.Context(), internalID, probe, data, h.storageManager, blockStore, storageClass, orgID)
				return ensureErr
			case db.BlockReuseNeedsPut:
				if checker := getAPIQuotaChecker(); checker != nil {
					if qs, _ := checker.CheckStorageQuota(orgID, userID, int64(len(data))); !qs.Allowed {
						c.JSON(http.StatusForbidden, gin.H{"error": "storage quota exceeded"})
						return errSyncStorageQuotaExceeded
					}
				}
				putStore, resolvedClass, _, resolveErr := syncResolveNeedsPutBlockStoreFn(h.storageManager, blockStore, storageClass, probe, orgID, internalID)
				if resolveErr != nil {
					return resolveErr
				}
				materializedStorageClass = resolvedClass
				if _, putErr := syncPutBlockAutoDirectFn(c.Request.Context(), putStore, internalID, data); putErr != nil {
					return fmt.Errorf("%w: %w", errSyncStoreBackend, putErr)
				}
				return nil
			case db.BlockReuseBlockedByGC:
				return v2.ErrBlockDeleteInProgress
			}
			return fmt.Errorf("unexpected block reuse decision %d for %s", probe.Decision, internalID)
		}, func() error {
			// Checked before the write, never after it. The register is not
			// cancellable once issued, so a deadline that expires mid-flight
			// leaves it durable, and re-reporting that as this callback's failure
			// only obscures where the request actually stopped.
			//
			// It does NOT make the outcome a 200: the reconfirming store() phase
			// of store→materialize→store still refuses to run on a dead context,
			// so the request ends 503. That is deliberate — the second store is
			// the GC-race reconfirmation, and reporting success without it would
			// fail open. A committed register plus a 503 costs a redundant
			// re-upload; the alternative costs correctness.
			if err := c.Request.Context().Err(); err != nil {
				return err
			}
			return registerUploadedBlockAndMappingForSyncFn(h.db, orgID, repoID, internalID, operationID, len(data), materializedStorageClass, "", externalMappingID)
		}, nil, nil); err != nil {
			if rejectAdmittedTimeout(c, lifetime, "storage", err) {
				return
			}
			if errors.Is(err, v2.ErrBlockMappingWriteFailed) {
				log.Printf("PutBlock: failed to store block mapping org=%s ext=%s int=%s: %v", orgID, externalID, internalID, err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create block mapping"})
				return
			}
			log.Printf("PutBlock: failed to store block metadata org=%s block=%s: %v", orgID, internalID, err)
			if errors.Is(err, v2.ErrBlockDeleteInProgress) {
				c.JSON(http.StatusConflict, gin.H{"error": "block is being deleted; retry the upload"})
			} else if errors.Is(err, errSyncStorageQuotaExceeded) {
				return
			} else if errors.Is(err, errSyncBlockExistenceCheck) {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check block existence"})
			} else if errors.Is(err, errSyncStoreBackend) {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to store block"})
			} else {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to store block metadata"})
			}
			return
		}

		// If the client addressed the upload by legacy SHA-1, we stored the
		// representation-scoped SHA-1 → SHA-256 mapping above.
		if classifiedID.isLegacySHA1 {
			log.Printf("PutBlock: stored mapping %s → %s\n", externalID, internalID)
		}
	} else {
		exists, err := syncBlockExistsFn(c.Request.Context(), blockStore, internalID)
		if err != nil {
			log.Printf("PutBlock: failed to check block existence: %v\n", err)
			if rejectAdmittedTimeout(c, lifetime, "storage", err) {
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check block existence"})
			return
		}
		if !exists {
			if checker := getAPIQuotaChecker(); checker != nil {
				if qs, _ := checker.CheckStorageQuota(orgID, userID, int64(len(data))); !qs.Allowed {
					c.JSON(http.StatusForbidden, gin.H{"error": "storage quota exceeded"})
					return
				}
			}
			_, err = syncPutBlockDataFn(c.Request.Context(), blockStore, blockData)
			if err != nil {
				log.Printf("PutBlock: failed to store in backend: %v\n", err)
				if rejectAdmittedTimeout(c, lifetime, "storage", err) {
					return
				}
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to store block"})
				return
			}
		}
	}

	// No timeout check here on purpose. Every phase above stops work that has not
	// happened yet; by this point the bytes are stored and the mapping is
	// registered, so a deadline that expired during the last of them has nothing
	// left to prevent. Answering 503 for committed work would only buy a needless
	// re-upload of the whole block.
	c.Status(http.StatusOK)

	// Record sync upload traffic — fire-and-forget.
	if rec := traffic.Get(); rec != nil {
		traffic.RecordCheckedTransfer(rec, uploadTrafficStatus, orgID, userID, traffic.SyncUpload, int64(len(data)))
	}
}

// CheckBlocksRequest represents the request to check which blocks exist
type CheckBlocksRequest struct {
	BlockIDs []string `json:"block_ids"`
}

// idListSpec describes one route's id-list parse: how many ids it accepts and how
// it names them when it refuses. The naming is per-route rather than generic
// because check-blocks' 413 body — {"error":"too many block ids","max_block_ids":N}
// — is an existing client-visible shape, and generalizing this parser must not
// silently change it.
type idListSpec struct {
	route      string // log prefix on a malformed body, e.g. "check-blocks"
	maxIDs     int
	tooManyMsg string // 413 "error" text, e.g. "too many block ids"
	maxField   string // 413 field carrying the cap, e.g. "max_block_ids"
}

// checkBlocksIDListSpec preserves check-blocks' pre-existing 413 body verbatim
// ({"error":"too many block ids","max_block_ids":N}); the cap stays configuration.
func checkBlocksIDListSpec(maxIDs int) idListSpec {
	return idListSpec{route: "check-blocks", maxIDs: maxIDs, tooManyMsg: "too many block ids", maxField: "max_block_ids"}
}

// packFSIDListSpec and checkFSIDListSpec name the id-list parse for the two fs
// routes. Their 413 shape is new (neither route bounded id count before), so it is
// named for fs ids rather than borrowing check-blocks' block-id wording, and their
// caps are derived consts rather than configuration — see minFSIDWireBytes.
func packFSIDListSpec() idListSpec {
	return idListSpec{route: "pack-fs", maxIDs: maxPackFSIDs, tooManyMsg: "too many fs ids", maxField: "max_fs_ids"}
}

func checkFSIDListSpec() idListSpec {
	return idListSpec{route: "check-fs", maxIDs: maxCheckFSIDs, tooManyMsg: "too many fs ids", maxField: "max_fs_ids"}
}

// parseBoundedIDList parses a newline-separated or JSON-array id body into ids,
// bounding the id count *before* the list is materialized. Checking the count
// after the parse would allocate exactly what the cap exists to bound: within a
// 16 MiB body cap a caller can still reach ~16.7M ids via one-byte entries
// ("a\n\n\n…\na", which TrimSpace cannot collapse because both ends are
// non-space), and strings.Split pre-sizes the slice in a single make — ~272 MB of
// string headers. The JSON path is the same class (~5.6M empty strings, ~198 MB)
// because json.Unmarshal grows the slice to completion first.
//
// A byte cap alone does not close this: readLimitedRequestBody bounds the body,
// this bounds what the body can expand into. check-fs and pack-fs carry the same
// 40-hex id shape as check-blocks and reached this parser late (their byte caps
// landed first, in ISSUE-SYNC-UNBOUNDED-BODIES-01 / X9), which is why the parser
// is shared rather than reimplemented per route.
//
// Returns ok=false after writing the response; the caller must return immediately.
func parseBoundedIDList(c *gin.Context, body []byte, spec idListSpec) ([]string, bool) {
	tooMany := func() ([]string, bool) {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": spec.tooManyMsg, spec.maxField: spec.maxIDs})
		return nil, false
	}
	invalid := func(err error) ([]string, bool) {
		log.Printf("%s: failed to parse JSON array: %v", spec.route, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON array"})
		return nil, false
	}

	bodyStr := strings.TrimSpace(string(body))

	// Newline-separated format: count the delimiters first, since strings.Split
	// allocates one slice entry per id up front.
	if !strings.HasPrefix(bodyStr, "[") {
		if strings.Count(bodyStr, "\n")+1 > spec.maxIDs {
			return tooMany()
		}
		return strings.Split(bodyStr, "\n"), true
	}

	// JSON array format: decode element by element so an oversized array is
	// rejected without ever materializing it.
	dec := json.NewDecoder(strings.NewReader(bodyStr))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
		return invalid(err)
	}
	externalIDs := make([]string, 0, 64)
	for dec.More() {
		if len(externalIDs) >= spec.maxIDs {
			return tooMany()
		}
		var id string
		if err := dec.Decode(&id); err != nil {
			return invalid(err)
		}
		externalIDs = append(externalIDs, id)
	}
	// Require the closing bracket and reject trailing content, preserving the
	// strictness json.Unmarshal enforced before.
	if tok, err := dec.Token(); err != nil || tok != json.Delim(']') {
		return invalid(err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return invalid(err)
	}
	return externalIDs, true
}

// checkBlocksRequestedBlock keeps one requested id next to its classification so
// the response can be built in request order without re-classifying.
type checkBlocksRequestedBlock struct {
	external   string
	classified classifiedClientBlockID
}

// classifyCheckBlockIDs validates every requested id and reports the distinct
// legacy SHA-1 ids among them.
//
// The distinct set is the whole point: the request list is client-supplied and
// nothing stops it from being one id repeated to the cap. Resolving per
// occurrence made that list cost as much as a list of distinct ids while being
// far cheaper to send.
//
// Returns ok=false after writing the response; the caller must return
// immediately.
func classifyCheckBlockIDs(c *gin.Context, externalIDs []string) ([]checkBlocksRequestedBlock, []string, bool) {
	requestedBlocks := make([]checkBlocksRequestedBlock, 0, len(externalIDs))
	uniqueLegacyIDs := make([]string, 0, 16)
	seenLegacy := make(map[string]struct{}, 16)

	for _, extID := range externalIDs {
		if strings.TrimSpace(extID) == "" {
			continue
		}
		classifiedID, classifyErr := classifyClientReadableBlockID(extID)
		if classifyErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid block id"})
			return nil, nil, false
		}
		requestedBlocks = append(requestedBlocks, checkBlocksRequestedBlock{
			external:   extID,
			classified: classifiedID,
		})
		if !classifiedID.isLegacySHA1 {
			continue
		}
		if _, seen := seenLegacy[classifiedID.normalized]; seen {
			continue
		}
		seenLegacy[classifiedID.normalized] = struct{}{}
		uniqueLegacyIDs = append(uniqueLegacyIDs, classifiedID.normalized)
	}
	return requestedBlocks, uniqueLegacyIDs, true
}

// resolveCheckBlockMappings resolves distinct legacy SHA-1 ids to their internal
// SHA-256 identities, at most fanout reads at a time.
//
// Absent mappings are recorded as an empty internal id rather than as an error:
// "we have never seen this block" is the ordinary answer for an id the client is
// about to upload. A malformed *stored* id is an error, because that is
// corruption rather than absence and the fail-closed contract of this route says
// so.
//
// Cancellation is the property that matters as much as the bound. Every worker
// checks the group context before issuing a read, so a client that disconnects
// or a request that exceeds its admitted lifetime stops the remaining lookups
// instead of driving them to completion for nobody.
func (h *SyncHandler) resolveCheckBlockMappings(ctx context.Context, orgID, representationID string, externalIDs []string, fanout int) (map[string]string, error) {
	resolved := make(map[string]string, len(externalIDs))
	if len(externalIDs) == 0 {
		return resolved, nil
	}
	if fanout < 1 {
		fanout = 1
	}

	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	slots := make(chan struct{}, fanout)
dispatchMappings:
	for _, id := range externalIDs {
		if err := gctx.Err(); err != nil {
			break
		}
		select {
		case slots <- struct{}{}:
		case <-gctx.Done():
			break dispatchMappings
		}
		if err := gctx.Err(); err != nil {
			<-slots
			break
		}
		externalID := id
		g.Go(func() error {
			defer func() { <-slots }()
			if err := gctx.Err(); err != nil {
				return err
			}
			metrics.SyncCheckBlocksLookupsTotal.WithLabelValues("mapping").Inc()
			mappedID, ok, err := syncGetBlockIDMappingFn(gctx, h.db, orgID, representationID, externalID)
			if err != nil {
				return fmt.Errorf("read block id mapping for %s: %w", externalID, err)
			}
			internalID := ""
			if ok && strings.TrimSpace(mappedID) != "" {
				internalID, err = normalizeResolvedInternalBlockID(mappedID)
				if err != nil {
					log.Printf("CheckBlocks: invalid mapped internal id for %s: %q", externalID, mappedID)
					return fmt.Errorf("invalid mapped internal id for %s: %w", externalID, err)
				}
			}
			mu.Lock()
			resolved[externalID] = internalID
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return resolved, nil
}

// CheckBlocks checks which blocks already exist (for deduplication)
// POST /seafhttp/repo/:repo_id/check-blocks
// Supports both SHA-1 (40 chars, Seafile legacy) and SHA-256 (64 chars, new clients)
// Translates SHA-1 external IDs to internal SHA-256 IDs for storage lookup
//
// Subcontract C (= registry X11) of ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01 is what
// gives this handler its shape. The id cap bounds the *parse*; it never bounded
// the work an accepted list triggers. Four things do, and they are all here:
//
//  1. Admission before the body read, so an over-capacity node refuses at a cost
//     of one parked goroutine instead of buffering 16 MiB and then resolving
//     100k ids.
//  2. An admitted lifetime, so a slot is always recoverable and the metadata
//     work stops when the deadline passes.
//  3. Deduplication, so the cost tracks *unique* ids. A list of one id repeated
//     100k times used to cost 100k Cassandra reads.
//  4. A bounded fan-out on both lookup phases, so per-request latency improves
//     without handing one request an unbounded slice of the driver pool. With
//     the node cap this is the aggregate bound: node cap x fan-out.
func (h *SyncHandler) CheckBlocks(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	if !h.checkSyncPermission(c, repoID, middleware.PermissionR) {
		return
	}

	// Everything above is a cheap rejection and must not wait for a slot or
	// consume one. Everything below is the work being bounded.
	releaseInflight, admitted := h.acquireCheckBlocksInflight(c, orgID, userID)
	if !admitted {
		return
	}
	defer releaseInflight()

	lifetime := h.checkBlocksLifetime()
	endLifetime, protected := h.beginAdmittedLifetime(c, lifetime)
	if !protected {
		return
	}
	defer endLifetime()

	// Read block IDs from body (bounded per request; bounded in aggregate by the
	// admission above)
	body, _, ok := readLimitedAdmittedRequestBody(c, lifetime, maxCheckBlocksBodyBytes)
	if !ok {
		return
	}
	// Parse the body - can be JSON array or newline-separated
	externalIDs, ok := parseBoundedIDList(c, body, checkBlocksIDListSpec(h.checkBlocksMaxIDs()))
	if !ok {
		return
	}
	// Observe the parsed cardinality even when classification later rejects a
	// malformed id. This series is operational evidence for lowering the cap;
	// excluding rejected bodies would hide precisely the traffic most likely to
	// exercise that cap.
	metrics.SyncCheckBlocksIDsPerRequest.Observe(float64(len(externalIDs)))

	requestedBlocks, uniqueLegacyIDs, ok := classifyCheckBlockIDs(c, externalIDs)
	if !ok {
		return
	}

	fanout := h.checkBlocksLookupFanout()

	// Resolve the legacy SHA-1 domain first, once per unique id.
	legacyToInternal := map[string]string{}
	if h.db != nil && len(uniqueLegacyIDs) > 0 {
		representationID, err := h.resolveSyncBlockRepresentationID(orgID, repoID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve block mapping"})
			return
		}
		legacyToInternal, err = h.resolveCheckBlockMappings(c.Request.Context(), orgID, representationID, uniqueLegacyIDs, fanout)
		if err != nil {
			if rejectAdmittedTimeout(c, lifetime, "mapping", err) {
				return
			}
			log.Printf("CheckBlocks: failed to resolve block id mappings: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve block mapping"})
			return
		}
	}

	// Project the resolution back onto every requested id, and collect the unique
	// internal ids the existence phase has to look at. An id that resolves to
	// nothing is already known to be needed and is not looked up at all.
	externalToInternal := make(map[string]string, len(requestedBlocks))
	internalIDs := make([]string, 0, len(requestedBlocks))
	seenInternal := make(map[string]struct{}, len(requestedBlocks))
	for _, reqBlock := range requestedBlocks {
		internalID := reqBlock.classified.normalized
		if h.db != nil && reqBlock.classified.isLegacySHA1 {
			internalID = legacyToInternal[reqBlock.classified.normalized]
		}
		externalToInternal[reqBlock.external] = internalID
		if internalID == "" {
			continue
		}
		if _, seen := seenInternal[internalID]; seen {
			continue
		}
		seenInternal[internalID] = struct{}{}
		internalIDs = append(internalIDs, internalID)
	}

	var existMap map[string]bool
	var err error
	if h.db != nil {
		var fallbackStore *storage.BlockStore
		var fallbackClass string
		if h.storageManager == nil {
			fallbackStore, fallbackClass, err = h.resolvePreferredBlockStore(c, orgID, repoID)
			if err != nil || fallbackStore == nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "block storage not available"})
				return
			}
		}
		// The location phase is dispatched by the canonical reader below. Count
		// this branch only; the legacy fallback has no location phase.
		metrics.SyncCheckBlocksLookupsTotal.WithLabelValues("location").Add(float64(len(internalIDs)))
		reader, resolveErr := syncNewCanonicalBlockCheckReaderFn(c.Request.Context(), h.db, h.storageManager, orgID, internalIDs, fallbackStore, fallbackClass, fanout)
		if resolveErr != nil {
			if rejectAdmittedTimeout(c, lifetime, "location", resolveErr) {
				return
			}
			log.Printf("CheckBlocks: failed to resolve canonical block locations: %v", resolveErr)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check blocks"})
			return
		}
		metrics.SyncCheckBlocksLookupsTotal.WithLabelValues("existence").Add(float64(len(internalIDs)))
		existMap, err = reader.CheckBlocksExist(c.Request.Context(), internalIDs, fanout)
	} else {
		// Preserve the legacy no-metadata routed fallback.
		blockStore, _, resolveErr := h.resolvePreferredBlockStore(c, orgID, repoID)
		if resolveErr != nil || blockStore == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "block storage not available"})
			return
		}
		metrics.SyncCheckBlocksLookupsTotal.WithLabelValues("existence").Add(float64(len(internalIDs)))
		existMap, err = blockStore.CheckBlocksParallel(c.Request.Context(), internalIDs, fanout)
	}
	if err != nil {
		if rejectAdmittedTimeout(c, lifetime, "existence", err) {
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check blocks"})
		return
	}

	// Return list of missing blocks using external IDs (client expects these)
	// Initialize as empty slice so JSON serializes as [] not null
	needed := make([]string, 0)
	for _, reqBlock := range requestedBlocks {
		internalID := externalToInternal[reqBlock.external]
		if internalID == "" {
			needed = append(needed, reqBlock.external)
			continue
		}
		if !existMap[internalID] {
			needed = append(needed, reqBlock.external)
		}
	}

	// Return as JSON array (Seafile format)
	c.JSON(http.StatusOK, needed)
}

// GetFSIDList returns the list of FS object IDs for sync
// GET /seafhttp/repo/:repo_id/fs-id-list
// Must return ALL fs_ids recursively: directories AND files (seafile objects)
func (h *SyncHandler) GetFSIDList(c *gin.Context) {
	repoID := c.Param("repo_id")
	serverHead := c.Query("server-head")
	clientHead := c.Query("client-head")
	dirOnly := c.Query("dir-only") == "1"

	if !h.checkSyncPermission(c, repoID, middleware.PermissionR) {
		return
	}

	_ = clientHead // Used for incremental sync

	// Get FS object IDs by traversing from server head commit
	// Initialize as empty slice (not nil) so JSON serializes as [] not null
	fsIDs := make([]string, 0)

	if serverHead != "" {
		// Query root FS ID from commit
		var rootFSID string
		err := h.db.Session().Query(`
			SELECT root_fs_id FROM commits WHERE library_id = ? AND commit_id = ?
		`, repoID, serverHead).Scan(&rootFSID)

		if err == nil && rootFSID != "" && rootFSID != strings.Repeat("0", 40) {
			// Recursively collect all fs_ids starting from root
			h.collectFSIDs(repoID, rootFSID, dirOnly, &fsIDs)
		}
	}

	// Return as JSON array (matches stock Seafile server)
	c.JSON(http.StatusOK, fsIDs)
}

// collectFSIDs recursively collects all fs_ids from a directory tree
// CRITICAL: Must return STORED fs_ids from database, not computed ones
// CRITICAL: Must return parent (root) FIRST, then children (breadth-first order)
func (h *SyncHandler) collectFSIDs(repoID, storedFSID string, dirOnly bool, fsIDs *[]string) {
	if storedFSID == "" || len(storedFSID) != 40 {
		return
	}

	// Track which IDs have been added to avoid duplicates
	added := make(map[string]bool)
	h.collectStoredFSIDsWithFilter(repoID, storedFSID, dirOnly, fsIDs, added)
}

// collectStoredFSIDsWithFilter recursively collects STORED fs_ids from database with dir-only filter support
// IMPORTANT: Returns parent (root) FIRST, then children (breadth-first order)
// This matches Seafile server behavior and ensures client can build directory tree in order
func (h *SyncHandler) collectStoredFSIDsWithFilter(repoID, storedFSID string, dirOnly bool, fsIDs *[]string, added map[string]bool) {
	if storedFSID == "" || len(storedFSID) != 40 {
		return
	}

	// Query the object type first
	var fsType string
	var entriesJSON string
	err := h.db.Session().Query(`
		SELECT obj_type, dir_entries FROM fs_objects WHERE library_id = ? AND fs_id = ?
	`, repoID, storedFSID).Scan(&fsType, &entriesJSON)

	if err != nil {
		return
	}

	// Parse entries for directories
	var entries []FSEntry
	if fsType == "dir" && entriesJSON != "" && entriesJSON != "[]" {
		json.Unmarshal([]byte(entriesJSON), &entries)
	}

	// Add THIS object (parent) FIRST if not already added
	if !added[storedFSID] {
		*fsIDs = append(*fsIDs, storedFSID)
		added[storedFSID] = true
	}

	// Then add children AFTER parent
	for _, entry := range entries {
		if entry.ID == "" || len(entry.ID) != 40 {
			continue
		}
		isDir := (entry.Mode & 0040000) != 0
		if dirOnly && !isDir {
			continue
		}

		// Add this child's STORED ID (from directory entry)
		if !added[entry.ID] {
			*fsIDs = append(*fsIDs, entry.ID)
			added[entry.ID] = true
		}

		// Recursively collect grandchildren
		h.collectStoredFSIDsWithFilter(repoID, entry.ID, dirOnly, fsIDs, added)
	}
}

// seafileServeBlockIDs returns the SHA-1 block-id list for any Seafile-boundary
// file fs_object operation: serializing an object to the desktop/mobile client
// (GetFSObject/PackFS) AND recomputing a Seafile-compatible fs_id
// (computeCorrectedObject/CheckFS). In both cases the client identifies a file
// object by its fs_id (= SHA-1 of the object JSON, which embeds the block-id
// list), so the list MUST be the SHA-1 one. After the SHA-256 canonicalization
// that list lives in fs_objects.seafile_block_ids_sha1 while fs_objects.block_ids
// holds the internal SHA-256 ids. Falls back to block_ids when the SHA-1 column
// is empty (rows written before the PR4 writer flip), which is still SHA-1 then.
//
// ok is false (fail-closed guard) when the SHA-1 column is empty AND block_ids
// already holds a non-40-hex (SHA-256) id: that is the dangerous post-flip state
// where a writer stored SHA-256 block_ids without the SHA-1 column, and serving
// or hashing those would hand the client an id it cannot parse / that does not
// match the requested fs_id. Callers MUST refuse to serve in that case rather
// than silently corrupt. See docs/SHA256-CANONICAL-BLOCK-IDS.md.
func seafileServeBlockIDs(blockIDs, seafileSHA1 []string) ([]string, bool) {
	if len(seafileSHA1) > 0 {
		if len(seafileSHA1) != len(blockIDs) {
			return nil, false
		}
		for _, id := range seafileSHA1 {
			if !isHexN(id, 40) {
				return nil, false
			}
		}
		return seafileSHA1, true
	}
	for _, id := range blockIDs {
		if !isHexN(id, 40) {
			return nil, false
		}
	}
	return blockIDs, true
}

func isHexN(s string, n int) bool {
	s = strings.TrimSpace(s)
	if len(s) != n {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// GetFSObject retrieves a filesystem object
// GET /seafhttp/repo/:repo_id/fs/:fs_id
// Returns zlib-compressed JSON in Seafile format:
// - For dirs: {"version": 1, "type": 3, "dirents": [...]}
// - For files: {"version": 1, "type": 1, "block_ids": [...], "size": N}
func (h *SyncHandler) GetFSObject(c *gin.Context) {
	repoID := c.Param("repo_id")
	fsID := c.Param("fs_id")

	if !h.checkSyncPermission(c, repoID, middleware.PermissionR) {
		return
	}

	// Query FS object from database
	var fsType string
	var name string
	var size int64
	var mtime int64
	var entriesJSON string
	var blockIDs []string
	var seafileBlockIDs []string

	err := h.db.Session().Query(`
		SELECT obj_type, obj_name, size_bytes, mtime, dir_entries, block_ids, seafile_block_ids_sha1
		FROM fs_objects WHERE library_id = ? AND fs_id = ?
	`, repoID, fsID).Scan(&fsType, &name, &size, &mtime, &entriesJSON, &blockIDs, &seafileBlockIDs)

	if err != nil {
		log.Printf("[GetFSObject] fs_object %s not found in repo %s: %v", fsID, repoID, err)
		c.JSON(http.StatusNotFound, gin.H{"error": "fs object not found"})
		return
	}

	// Build JSON object matching Seafile's exact format
	var jsonObj interface{}

	if fsType == "dir" {
		// Directory format: {"version": 1, "type": 3, "dirents": [...]}
		var dirents []map[string]interface{}
		if entriesJSON != "" && entriesJSON != "[]" {
			if err := json.Unmarshal([]byte(entriesJSON), &dirents); err != nil {
				log.Printf("[GetFSObject] failed to parse dirents for %s: %v", fsID, err)
				dirents = []map[string]interface{}{}
			}
		} else {
			dirents = []map[string]interface{}{}
		}
		jsonObj = map[string]interface{}{
			"version": 1,
			"type":    3, // SEAF_METADATA_TYPE_DIR
			"dirents": dirents,
		}
	} else {
		// File format: {"version": 1, "type": 1, "block_ids": [...], "size": N}
		// Serve the SHA-1 list (Seafile boundary); see seafileServeBlockIDs.
		serveBlockIDs, ok := seafileServeBlockIDs(blockIDs, seafileBlockIDs)
		if !ok {
			log.Printf("[GetFSObject] fs_object %s has SHA-256 block_ids without seafile_block_ids_sha1; refusing to serve a non-SHA-1 list", fsID)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "fs object block ids unavailable"})
			return
		}
		jsonObj = map[string]interface{}{
			"version":   1,
			"type":      1, // SEAF_METADATA_TYPE_FILE
			"block_ids": serveBlockIDs,
			"size":      size,
		}
	}

	// Serialize to JSON
	jsonBytes, err := json.Marshal(jsonObj)
	if err != nil {
		log.Printf("[GetFSObject] failed to marshal fs_object %s: %v", fsID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to serialize object"})
		return
	}

	// Compress with zlib (Seafile client expects zlib-compressed data)
	var compressed bytes.Buffer
	zlibWriter := zlib.NewWriter(&compressed)
	zlibWriter.Write(jsonBytes)
	zlibWriter.Close()

	log.Printf("[GetFSObject] Returning fs_object %s (type=%s, compressed=%d bytes)", fsID, fsType, compressed.Len())

	c.Data(http.StatusOK, "application/octet-stream", compressed.Bytes())
}

// PackFS packs multiple FS objects into a single response
// POST /seafhttp/repo/:repo_id/pack-fs
// Returns binary packed format that Seafile client expects:
// For each object: 40-byte hex ID + object size (4 bytes BE) + zlib-compressed JSON
// NOTE: Seafile server stores fs objects compressed, so pack-fs sends compressed data.
// Client stores as-is and decompresses when reading.
func (h *SyncHandler) PackFS(c *gin.Context) {
	repoID := c.Param("repo_id")

	if !h.checkSyncPermission(c, repoID, middleware.PermissionR) {
		return
	}

	// Read FS IDs from body
	body, ok := readLimitedRequestBody(c, maxPackFSBodyBytes)
	if !ok {
		return
	}

	// Parse the body - can be JSON array or newline-separated
	requestedFSIDs, ok := parseBoundedIDList(c, body, packFSIDListSpec())
	if !ok {
		return
	}

	// Build binary response
	var buf bytes.Buffer

	for _, requestedFSID := range requestedFSIDs {
		if requestedFSID == "" || len(requestedFSID) != 40 {
			continue
		}

		// Query fs_object directly from database using the requested fs_id
		var fsType string
		var size int64
		var entriesJSON string
		var blockIDs []string
		var seafileBlockIDs []string

		err := h.db.Session().Query(`
			SELECT obj_type, size_bytes, dir_entries, block_ids, seafile_block_ids_sha1
			FROM fs_objects WHERE library_id = ? AND fs_id = ?
		`, repoID, requestedFSID).Scan(&fsType, &size, &entriesJSON, &blockIDs, &seafileBlockIDs)

		if err != nil {
			log.Printf("pack-fs: object %s not found: %v", requestedFSID, err)
			continue
		}

		// Build JSON matching Seafile format
		var jsonObj map[string]interface{}
		if fsType == "dir" {
			var dirents []map[string]interface{}
			if entriesJSON != "" && entriesJSON != "[]" {
				// Parse entries and return them as-is (using STORED child IDs)
				if err := json.Unmarshal([]byte(entriesJSON), &dirents); err != nil {
					log.Printf("pack-fs: failed to parse dirents for %s: %v", requestedFSID, err)
					dirents = []map[string]interface{}{}
				}
			} else {
				dirents = []map[string]interface{}{}
			}
			jsonObj = map[string]interface{}{
				"dirents": dirents,
				"type":    3,
				"version": 1,
			}
		} else {
			serveBlockIDs, ok := seafileServeBlockIDs(blockIDs, seafileBlockIDs)
			if !ok {
				log.Printf("pack-fs: object %s has SHA-256 block_ids without seafile_block_ids_sha1; refusing to serve a non-SHA-1 list", requestedFSID)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "fs object block ids unavailable"})
				return
			}
			jsonObj = map[string]interface{}{
				"block_ids": serveBlockIDs,
				"size":      size,
				"type":      1,
				"version":   1,
			}
		}

		jsonBytes, err := json.Marshal(jsonObj)
		if err != nil {
			log.Printf("pack-fs: failed to marshal object %s: %v", requestedFSID, err)
			continue
		}

		// Compress with zlib
		var compressed bytes.Buffer
		zlibWriter := zlib.NewWriter(&compressed)
		zlibWriter.Write(jsonBytes)
		zlibWriter.Close()

		// Write the REQUESTED fs_id (same as what's stored)
		buf.WriteString(requestedFSID)

		// Write object size (4 bytes, network byte order)
		binary.Write(&buf, binary.BigEndian, uint32(compressed.Len()))

		// Write zlib-compressed content
		buf.Write(compressed.Bytes())
	}

	c.Data(http.StatusOK, "application/octet-stream", buf.Bytes())
}

// RecvFS receives and stores FS objects from client
// POST /seafhttp/repo/:repo_id/recv-fs
// Seafile sends packed FS objects in binary format:
// For each object: 40-byte hex ID + 4-byte size (BE) + zlib-compressed JSON
func (h *SyncHandler) RecvFS(c *gin.Context) {
	repoID := c.Param("repo_id")

	if !h.checkSyncPermission(c, repoID, middleware.PermissionRW) {
		return
	}

	// Read FS objects from body
	body, ok := readLimitedRequestBody(c, h.syncRecvFSMaxBytes())
	if !ok {
		return
	}

	if len(body) < 44 { // At least 40 (ID) + 4 (size)
		c.JSON(http.StatusBadRequest, gin.H{"error": "body too short"})
		return
	}

	// Parse packed FS objects
	// Format: each object is [40-char hex ID][4-byte size][zlib-compressed JSON]
	offset := 0
	objectsStored := 0

	for offset+44 <= len(body) {
		// Read 40-char hex FS ID
		fsID := string(body[offset : offset+40])
		offset += 40

		// Read 4-byte size (big-endian)
		objSize := binary.BigEndian.Uint32(body[offset : offset+4])
		offset += 4

		// Read the compressed object data
		if offset+int(objSize) > len(body) {
			log.Printf("recv-fs: truncated object data for %s", fsID)
			break
		}
		compressedData := body[offset : offset+int(objSize)]
		offset += int(objSize)

		// Decompress with zlib
		zlibReader, err := zlib.NewReader(bytes.NewReader(compressedData))
		if err != nil {
			log.Printf("recv-fs: failed to create zlib reader for %s: %v", fsID, err)
			continue
		}
		jsonData, err := io.ReadAll(zlibReader)
		zlibReader.Close()
		if err != nil {
			log.Printf("recv-fs: failed to decompress object %s: %v", fsID, err)
			continue
		}

		// CRITICAL: We must preserve the EXACT JSON bytes for dirents because
		// the fs_id is the SHA1 hash of the exact JSON content. Re-marshaling
		// would change the key order and break hash verification.
		//
		// Use json.RawMessage to extract the dirents without re-marshaling.
		var rawObj struct {
			Type     int             `json:"type"`
			Version  int             `json:"version"`
			Dirents  json.RawMessage `json:"dirents,omitempty"`
			BlockIDs []string        `json:"block_ids,omitempty"`
			Size     int64           `json:"size,omitempty"`
		}
		if err := json.Unmarshal(jsonData, &rawObj); err != nil {
			log.Printf("recv-fs: failed to parse JSON for %s: %v", fsID, err)
			continue
		}

		fsType := "dir"
		var size int64
		var blockIDs []string
		var entriesJSON string = "[]"

		if rawObj.Type == 1 {
			// File object
			fsType = "file"
			size = rawObj.Size
			blockIDs = rawObj.BlockIDs
		} else if rawObj.Type == 3 {
			// Directory object - preserve exact bytes of dirents
			if len(rawObj.Dirents) > 0 {
				entriesJSON = string(rawObj.Dirents)
			}
		}

		now := time.Now().Unix()

		err = h.db.Session().Query(`
			INSERT INTO fs_objects (library_id, fs_id, obj_type, obj_name, size_bytes, mtime, dir_entries, block_ids)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, repoID, fsID, fsType, "", size, now, entriesJSON, blockIDs).Exec()

		if err != nil {
			log.Printf("recv-fs: Failed to store object %s: %v", fsID, err)
		} else {
			objectsStored++

			// For directories, update child obj_names for search indexing
			if fsType == "dir" && len(rawObj.Dirents) > 0 {
				var dirContent struct {
					Dirents []FSEntry `json:"dirents"`
				}
				if err := json.Unmarshal(rawObj.Dirents, &dirContent); err == nil {
					for _, entry := range dirContent.Dirents {
						if entry.Name != "" && entry.ID != "" {
							// Update the child's obj_name (upsert pattern)
							h.db.Session().Query(`
								UPDATE fs_objects SET obj_name = ? WHERE library_id = ? AND fs_id = ?
							`, entry.Name, repoID, entry.ID).Exec()
						}
					}
				}
			}
		}
	}

	log.Printf("recv-fs: Stored %d objects for repo %s", objectsStored, repoID)
	c.Status(http.StatusOK)
}

// isHexString checks if bytes are valid hex characters
func isHexString(b []byte) bool {
	for _, c := range b {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// CheckFS checks which FS objects already exist
// POST /seafhttp/repo/:repo_id/check-fs
func (h *SyncHandler) CheckFS(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")

	if !h.checkSyncPermission(c, repoID, middleware.PermissionR) {
		return
	}

	// Read FS IDs from body
	body, ok := readLimitedRequestBody(c, maxCheckFSBodyBytes)
	if !ok {
		return
	}

	// Parse the body - can be JSON array or newline-separated
	fsIDs, ok := parseBoundedIDList(c, body, checkFSIDListSpec())
	if !ok {
		return
	}

	// CRITICAL: Client sends COMPUTED fs_ids (SHA-1 of corrected JSON),
	// but we store objects with their ORIGINAL (stored) fs_ids.
	// We need to build the computed→stored mapping to check correctly.

	// Get HEAD commit's root_fs_id to build the mapping
	var headCommitID string
	err := h.db.Session().Query(`
		SELECT head_commit_id FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&headCommitID)
	if err != nil {
		log.Printf("check-fs: failed to get HEAD commit for repo %s (org %s): %v", repoID, orgID, err)
		// Fallback: check without mapping (will likely fail but better than error)
		c.JSON(http.StatusOK, fsIDs)
		return
	}

	var rootFSID string
	err = h.db.Session().Query(`
		SELECT root_fs_id FROM commits WHERE library_id = ? AND commit_id = ?
	`, repoID, headCommitID).Scan(&rootFSID)
	if err != nil || rootFSID == "" || rootFSID == strings.Repeat("0", 40) {
		log.Printf("check-fs: failed to get root_fs_id for commit %s: %v", headCommitID, err)
		// Fallback: check without mapping
		rootFSID = ""
	}

	// Build the computed→stored mapping
	computedToStored := make(map[string]string)
	if rootFSID != "" {
		var mapErr error
		computedToStored, _, mapErr = h.buildFSIDMapping(repoID, rootFSID)
		if mapErr != nil {
			log.Printf("check-fs: failed to build fs_id mapping for repo %s root %s: %v", repoID, rootFSID, mapErr)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to validate fs objects"})
			return
		}
	}

	log.Printf("[CheckFS] Checking %d FS IDs for repo %s (have %d mappings)", len(fsIDs), repoID, len(computedToStored))

	// Check which FS objects DON'T exist on server
	// Returns array of IDs that the server doesn't have
	// Initialize as empty slice so JSON serializes as [] not null
	missing := make([]string, 0)
	for _, computedFSID := range fsIDs {
		if computedFSID == "" || len(computedFSID) != 40 {
			continue
		}

		// EMPTY_SHA1 is Seafile's canonical empty root directory.
		// The desktop client never uploads it via recv-fs, so reporting
		// it as missing creates a permanent sync stall.
		if computedFSID == strings.Repeat("0", 40) {
			continue
		}

		// Map computed ID → stored ID
		storedFSID, hasMapping := computedToStored[computedFSID]
		if !hasMapping {
			// Fallback: maybe the requested ID is already a stored ID (for compatibility)
			storedFSID = computedFSID
		}

		// Check if the STORED ID exists in database
		var exists string
		err := h.db.Session().Query(`
			SELECT fs_id FROM fs_objects WHERE library_id = ? AND fs_id = ? LIMIT 1
		`, repoID, storedFSID).Scan(&exists)

		if err != nil {
			// FS object doesn't exist on server
			log.Printf("[CheckFS] Missing: computed=%s, stored=%s", computedFSID, storedFSID)
			missing = append(missing, computedFSID)
		}
	}

	log.Printf("[CheckFS] Result: %d missing out of %d requested", len(missing), len(fsIDs))

	// Return as JSON array (Seafile format)
	c.JSON(http.StatusOK, missing)
}

// PermissionCheck checks user permissions for the repository
// GET /seafhttp/repo/:repo_id/permission-check
// Seafile desktop client expects 200 OK (empty body) for access, 403 for denied.
func (h *SyncHandler) PermissionCheck(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	if h.permMiddleware == nil {
		c.Status(http.StatusOK)
		return
	}

	perm, err := h.permMiddleware.GetLibraryPermission(orgID, userID, repoID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check permissions"})
		return
	}
	if perm == middleware.PermissionNone {
		c.JSON(http.StatusForbidden, gin.H{"error": "no access"})
		return
	}
	c.Status(http.StatusOK)
}

// QuotaCheck checks if user has enough quota for upload
// GET /seafhttp/repo/:repo_id/quota-check
func (h *SyncHandler) QuotaCheck(c *gin.Context) {
	repoID := c.Param("repo_id")

	if !h.checkSyncPermission(c, repoID, middleware.PermissionR) {
		return
	}

	orgID := c.GetString("org_id")

	checker := traffic.GetChecker()
	if checker == nil {
		// Quota system not initialized — allow (graceful degradation).
		c.JSON(http.StatusOK, gin.H{"has_quota": true})
		return
	}

	// Check storage quota with 0 additional bytes to see if the org is already over limit.
	userID := c.GetString("user_id")
	st, _ := checker.CheckStorageQuota(orgID, userID, 0)
	c.JSON(http.StatusOK, gin.H{"has_quota": st.Allowed})
}

// GetHeadCommitsMulti returns head commits for multiple repositories at once
// POST /seafhttp/repo/head-commits-multi
// This endpoint is public (no auth middleware) — mirrors official Seafile fileserver behavior.
// The desktop client calls this every ~30s without any auth headers. Repo UUIDs are
// unguessable and only commit hashes are returned, so exposure is minimal.
func (h *SyncHandler) GetHeadCommitsMulti(c *gin.Context) {
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")
	isAuthenticated := userID != ""

	// Stock Seafile expects JSON array: ["repo-id-1", "repo-id-2"]
	// Verified: 2026-01-18 against app.nihaoconsult.com
	var repoIDs []string
	if err := c.BindJSON(&repoIDs); err != nil {
		log.Printf("[GetHeadCommitsMulti] Failed to parse JSON array: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON array"})
		return
	}

	if len(repoIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "empty repo list"})
		return
	}

	log.Printf("[GetHeadCommitsMulti] Checking %d repos (authenticated=%v, org=%s)", len(repoIDs), isAuthenticated, orgID)

	// Build response map of repo_id -> head_commit_id
	result := make(map[string]string)

	for _, repoID := range repoIDs {
		if repoID == "" {
			continue
		}

		// Permission check only when we have a user context (authenticated requests).
		// Unauthenticated callers (desktop client polling) get results from libraries_by_id
		// without ACL filtering — matching stock Seafile fileserver behavior.
		if isAuthenticated && h.permMiddleware != nil {
			hasAccess, err := h.permMiddleware.HasLibraryAccess(orgID, userID, repoID, middleware.PermissionR)
			if err != nil || !hasAccess {
				continue
			}
		}

		var headCommitID string
		var err error

		// Authenticated: query by org_id partition (fast path).
		// Unauthenticated: skip directly to libraries_by_id (no org context available).
		if isAuthenticated && orgID != "" {
			err = h.db.Session().Query(`
				SELECT head_commit_id FROM libraries WHERE org_id = ? AND library_id = ?
			`, orgID, repoID).Scan(&headCommitID)
		}

		// Fallback to org lookup + canonical head when unauthenticated or the
		// authenticated org partition missed. This keeps HEAD reads anchored on the
		// authoritative libraries row even if libraries_by_id lags transiently.
		if !isAuthenticated || err != nil || headCommitID == "" {
			var lookupOrgID string
			err = h.db.Session().Query(`
				SELECT org_id FROM libraries_by_id WHERE library_id = ?
			`, repoID).Scan(&lookupOrgID)
			if err == nil && lookupOrgID != "" {
				err = h.db.Session().Query(`
					SELECT head_commit_id FROM libraries WHERE org_id = ? AND library_id = ?
				`, lookupOrgID, repoID).Scan(&headCommitID)
			}
		}

		if err == nil && headCommitID != "" {
			result[repoID] = headCommitID
			log.Printf("[GetHeadCommitsMulti] Repo %s HEAD: %s", repoID[:8], headCommitID[:8])
		} else {
			log.Printf("[GetHeadCommitsMulti] Repo %s not found or no HEAD (err=%v)", repoID[:8], err)
		}
	}

	c.JSON(http.StatusOK, result)
}

// UpdateBranch updates the head commit of a repository branch
// POST /seafhttp/repo/:repo_id/update-branch
func (h *SyncHandler) UpdateBranch(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	if !h.checkSyncPermission(c, repoID, middleware.PermissionRW) {
		return
	}

	// Get new head commit from query params
	newHead := c.Query("head")
	if newHead == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing head parameter"})
		return
	}

	h.handleSyncHeadPromotion(c, orgID, userID, repoID, newHead, "UpdateBranch")
}

// GetDownloadInfo returns repository sync information for desktop client
// GET /seafhttp/repo/:repo_id/download-info
func (h *SyncHandler) GetDownloadInfo(c *gin.Context) {
	repoID := c.Param("repo_id")
	if repoID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "repo_id is required"})
		return
	}
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	if !h.checkSyncPermission(c, repoID, middleware.PermissionR) {
		return
	}

	// Get library info from database
	var libID, ownerID, name, description, headCommitID string
	var encrypted bool
	var encVersion int
	var magic, randomKey string
	var sizeBytes int64
	var updatedAt time.Time

	err := h.db.Session().Query(`
		SELECT library_id, owner_id, name, description, encrypted, enc_version,
		       magic, random_key, head_commit_id, size_bytes, updated_at
		FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(
		&libID, &ownerID, &name, &description, &encrypted, &encVersion,
		&magic, &randomKey, &headCommitID, &sizeBytes, &updatedAt,
	)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	// Generate a sync token if we have a token creator
	token := ""
	if h.tokenCreator != nil {
		token, _ = h.tokenCreator.CreateSyncToken(orgID, repoID, userID)
	}

	// Format repo size in Seafile's human-readable format
	repoSizeFormatted := formatSizeSeafile(sizeBytes)

	// Format mtime as relative time HTML (Seafile format)
	mtimeRelative := formatRelativeTimeHTML(updatedAt)

	// Resolve actual permission for the user
	perm := "rw"
	if h.permMiddleware != nil {
		actualPerm, err := h.permMiddleware.GetLibraryPermission(orgID, userID, repoID)
		if err == nil && actualPerm != "" {
			perm = string(actualPerm)
			if perm == "owner" {
				perm = "rw"
			}
		}
	}

	// Build response in Seafile format
	// Convert encrypted bool to int (Seafile uses 1/0, not true/false in download-info)
	encryptedInt := 0
	if encrypted {
		encryptedInt = 1
	}
	configuredURL := ""
	if h.config != nil {
		configuredURL = h.config.Server.URL
	}
	relayHost := getEffectiveHostname(c, configuredURL)
	response := gin.H{
		"relay_id":            relayHost,
		"relay_addr":          relayHost,
		"relay_port":          getRelayPortFromRequest(c, configuredURL),
		"email":               userID + "@sesamefs.local",
		"token":               token,
		"repo_id":             repoID,
		"repo_name":           name,
		"repo_desc":           "", // Seafile returns empty string in download-info
		"repo_size":           sizeBytes,
		"repo_size_formatted": repoSizeFormatted,
		"repo_version":        1, // Standard Seafile repo version
		"mtime":               updatedAt.Unix(),
		"mtime_relative":      mtimeRelative,
		"encrypted":           encryptedInt, // Seafile uses int (1/0), not bool
		"permission":          perm,
		"head_commit_id":      headCommitID,
		// NOTE: is_corrupted is NOT included in download-info, only in commit/HEAD
	}

	// Add encryption fields if encrypted
	// Translate enc_version for Seafile desktop client compatibility
	if encrypted {
		clientEncVersion := encVersion
		if encVersion == 12 || encVersion == 10 {
			clientEncVersion = 2
		}
		response["enc_version"] = clientEncVersion
		// CRITICAL: For Seafile v2, salt must be empty string (not null)
		response["salt"] = ""
		response["magic"] = magic
		response["random_key"] = randomKey
	}

	c.JSON(http.StatusOK, response)
}

// updateFullPaths traverses the directory tree from root and updates obj_name and full_path
// for all fs_objects. This is called after a commit is received to ensure search indexing works.
// It runs asynchronously to not block the sync response.
func (h *SyncHandler) updateFullPaths(libraryID, rootFSID string) {
	if rootFSID == "" || rootFSID == strings.Repeat("0", 40) {
		return
	}

	// Recursive function to traverse directory tree
	var traverseDir func(fsID, parentPath string) int
	traverseDir = func(fsID, parentPath string) int {
		updated := 0

		// Get directory entries
		var dirEntries string
		err := h.db.Session().Query(`
			SELECT dir_entries FROM fs_objects WHERE library_id = ? AND fs_id = ?
		`, libraryID, fsID).Scan(&dirEntries)
		if err != nil || dirEntries == "" || dirEntries == "[]" {
			return 0
		}

		// Parse entries - try both formats
		var content struct {
			Dirents []FSEntry `json:"dirents"`
		}
		if err := json.Unmarshal([]byte(dirEntries), &content); err != nil {
			var entries []FSEntry
			if err := json.Unmarshal([]byte(dirEntries), &entries); err != nil {
				return 0
			}
			content.Dirents = entries
		}

		// Update each child
		for _, entry := range content.Dirents {
			if entry.Name == "" || entry.ID == "" {
				continue
			}

			// Compute full path
			var fullPath string
			if parentPath == "/" {
				fullPath = "/" + entry.Name
			} else {
				fullPath = parentPath + "/" + entry.Name
			}

			// Update obj_name and full_path
			err := h.db.Session().Query(`
				UPDATE fs_objects SET obj_name = ?, full_path = ? WHERE library_id = ? AND fs_id = ?
			`, entry.Name, fullPath, libraryID, entry.ID).Exec()
			if err == nil {
				updated++
			}

			// If this is a directory (mode 16384 = directory), recurse
			if entry.Mode == 16384 {
				updated += traverseDir(entry.ID, fullPath)
			}
		}

		return updated
	}

	// Start traversal from root
	updated := traverseDir(rootFSID, "/")
	if updated > 0 {
		log.Printf("updateFullPaths: updated %d paths for library %s", updated, libraryID)
	}
}

// updateLibraryHeadWithStats advances the canonical head plus canonical stats in
// one conditional update, applies the matching storage-counter delta, then
// synchronizes the derived projection rows. The baseline for the counter delta
// is derived from the previous commit's immutable tree rather than from
// libraries.size_bytes / file_count: a stale-read of those mutable columns
// (the same lag this function's notBefore retry guards against on the read
// side) would otherwise produce an incorrect delta. Counters run before the
// projection because counters are accumulative and cannot be safely re-applied
// from a fresh caller after the ErrHeadConflict the retry would now hit; the
// projection sync is fail-hard so the integration contract that "after 200,
// libraries_by_id and the admin projection are visible" is preserved.
func (h *SyncHandler) updateLibraryHeadWithStats(orgID, repoID, commitID, userID, expectedHead string) error {
	now := time.Now().Truncate(time.Millisecond)
	previousCommitID := expectedHead
	previousSize, previousFileCount := h.commitTreeStats(repoID, previousCommitID)
	totalSize, fileCount, err := h.commitTreeStatsStrict(repoID, commitID)
	if err != nil {
		return err
	}
	casState := map[string]interface{}{}
	applied, err := h.db.Session().Query(`
		UPDATE libraries SET head_commit_id = ?, updated_at = ?, size_bytes = ?, file_count = ?
		WHERE org_id = ? AND library_id = ?
		IF head_commit_id = ?
	`, commitID, now, totalSize, fileCount, orgID, repoID, expectedHead).MapScanCAS(casState)
	if err != nil {
		return fmt.Errorf("conditional head update failed: %w", err)
	}
	if !applied {
		currentHead, _ := casState["head_commit_id"].(string)
		return fmt.Errorf("%w: expected %s but found %s", ErrHeadConflict, expectedHead, currentHead)
	}

	if err := h.applySyncHeadPostCASMutations(orgID, repoID, userID, commitID, now, totalSize, fileCount, totalSize-previousSize, fileCount-previousFileCount); err != nil {
		return err
	}

	log.Printf("[updateLibraryStats] Updated library %s: size=%d bytes, files=%d", repoID, totalSize, fileCount)
	return nil
}

func (h *SyncHandler) applySyncHeadPostCASMutations(orgID, repoID, userID, commitID string, notBefore time.Time, totalSize, fileCount, deltaBytes, deltaFiles int64) error {
	if err := traffic.AdjustStorageCountersByDeltaSync(h.db, orgID, userID, repoID, deltaBytes, deltaFiles); err != nil {
		if repairErr := h.repairPublishedSyncHeadAfterCounterFailure(orgID, repoID, commitID, notBefore, totalSize, fileCount); repairErr != nil {
			return errors.Join(
				fmt.Errorf("sync storage counters for %s: %w", repoID, err),
				repairErr,
			)
		}
		return errors.Join(
			errSyncHeadRepairPending,
			fmt.Errorf("sync storage counters for %s: %w", repoID, err),
		)
	}

	if err := h.syncLibraryHeadDerivedState(orgID, repoID, commitID, notBefore, totalSize, fileCount); err != nil {
		if retryErr := h.syncLibraryHeadDerivedState(orgID, repoID, commitID, notBefore, totalSize, fileCount); retryErr != nil {
			return errors.Join(
				fmt.Errorf("sync derived state for %s: %w", repoID, err),
				retryErr,
			)
		}
	}
	return nil
}

func (h *SyncHandler) readLibraryHead(orgID, repoID string) (string, error) {
	var currentHead string
	if err := h.db.Session().Query(`
		SELECT head_commit_id FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&currentHead); err != nil {
		return "", err
	}
	return currentHead, nil
}

func (h *SyncHandler) readSyncTargetCommit(repoID, commitID string) (string, string, error) {
	var parentID *string
	var rootFSID string
	if err := h.db.Session().Query(`
		SELECT parent_id, root_fs_id FROM commits WHERE library_id = ? AND commit_id = ?
	`, repoID, commitID).Scan(&parentID, &rootFSID); err != nil {
		return "", "", err
	}
	commitParent := ""
	if parentID != nil {
		commitParent = *parentID
	}
	return commitParent, rootFSID, nil
}

func (h *SyncHandler) readCommitRootFSID(repoID, commitID string) (string, error) {
	if commitID == "" {
		return "", nil
	}

	var rootFSID string
	if err := h.db.Session().Query(`
		SELECT root_fs_id FROM commits WHERE library_id = ? AND commit_id = ?
	`, repoID, commitID).Scan(&rootFSID); err != nil {
		return "", err
	}
	return rootFSID, nil
}

func (h *SyncHandler) syncCommitHasAncestor(repoID, commitID, ancestorID string) (bool, error) {
	if commitID == "" || ancestorID == "" {
		return false, nil
	}

	current := commitID
	for depth := 0; depth < 1024; depth++ {
		if current == ancestorID {
			return true, nil
		}

		var parentID *string
		if err := h.db.Session().Query(`
			SELECT parent_id FROM commits WHERE library_id = ? AND commit_id = ?
		`, repoID, current).Scan(&parentID); err != nil {
			return false, err
		}
		if parentID == nil || *parentID == "" {
			return false, nil
		}
		current = *parentID
	}

	return false, fmt.Errorf("commit ancestry walk exceeded limit for repo %s from %s toward %s", repoID, commitID, ancestorID)
}

func (h *SyncHandler) readSyncDirectoryEntries(repoID, fsID string) ([]FSEntry, error) {
	if fsID == "" || fsID == strings.Repeat("0", 40) {
		return []FSEntry{}, nil
	}

	var objType string
	var entriesJSON string
	err := h.db.Session().Query(`
		SELECT obj_type, dir_entries FROM fs_objects WHERE library_id = ? AND fs_id = ?
	`, repoID, fsID).Scan(&objType, &entriesJSON)
	if err != nil {
		return nil, err
	}
	if objType != "dir" {
		return nil, fmt.Errorf("fs_object %s is %s, want dir", fsID, objType)
	}
	if entriesJSON == "" || entriesJSON == "[]" {
		return []FSEntry{}, nil
	}

	var entries []FSEntry
	if err := json.Unmarshal([]byte(entriesJSON), &entries); err != nil {
		return nil, fmt.Errorf("failed to parse dir entries for %s: %w", fsID, err)
	}
	return entries, nil
}

func (h *SyncHandler) createSyncDirectoryFSObject(repoID string, entries []FSEntry) (string, error) {
	ordered := append([]FSEntry(nil), entries...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Name < ordered[j].Name
	})

	entriesJSON, err := json.Marshal(ordered)
	if err != nil {
		return "", fmt.Errorf("failed to marshal merged directory entries: %w", err)
	}

	fsContent := map[string]interface{}{
		"version": 1,
		"type":    3,
		"dirents": json.RawMessage(entriesJSON),
	}
	fsContentJSON, err := json.Marshal(fsContent)
	if err != nil {
		return "", fmt.Errorf("failed to marshal merged directory object: %w", err)
	}

	hash := sha1.Sum(fsContentJSON)
	fsID := hex.EncodeToString(hash[:])
	if err := h.db.Session().Query(`
		INSERT INTO fs_objects (library_id, fs_id, obj_type, dir_entries, mtime)
		VALUES (?, ?, ?, ?, ?)
	`, repoID, fsID, "dir", string(entriesJSON), time.Now().Unix()).Exec(); err != nil {
		return "", fmt.Errorf("failed to store merged directory %s: %w", fsID, err)
	}

	return fsID, nil
}

func (h *SyncHandler) createSyncAutoMergeCommit(repoID, userID, parentCommitID, rootFSID, description string) (string, error) {
	commitData := fmt.Sprintf("%s:%s:%s:%d", repoID, rootFSID, description, time.Now().UnixNano())
	hash := sha1.Sum([]byte(commitData))
	commitID := hex.EncodeToString(hash[:])

	if err := h.db.Session().Query(`
		INSERT INTO commits (library_id, commit_id, parent_id, root_fs_id, creator_id, description, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, repoID, commitID, parentCommitID, rootFSID, userID, description, time.Now()).Exec(); err != nil {
		return "", fmt.Errorf("failed to create auto-merge commit: %w", err)
	}

	return commitID, nil
}

func syncEntryEquivalent(a, b *FSEntry) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.ID == b.ID && a.Mode == b.Mode
}

func syncEntrySlicesEquivalent(left, right []FSEntry) bool {
	if len(left) != len(right) {
		return false
	}

	leftOrdered := append([]FSEntry(nil), left...)
	rightOrdered := append([]FSEntry(nil), right...)
	sort.SliceStable(leftOrdered, func(i, j int) bool {
		return leftOrdered[i].Name < leftOrdered[j].Name
	})
	sort.SliceStable(rightOrdered, func(i, j int) bool {
		return rightOrdered[i].Name < rightOrdered[j].Name
	})

	for idx := range leftOrdered {
		if leftOrdered[idx].Name != rightOrdered[idx].Name {
			return false
		}
		if !syncEntryEquivalent(&leftOrdered[idx], &rightOrdered[idx]) {
			return false
		}
	}

	return true
}

func syncEntryIsDir(entry *FSEntry) bool {
	if entry == nil {
		return false
	}
	return (entry.Mode & 0040000) != 0
}

func preferredSyncMergeEntry(current, target, base *FSEntry, mergedID string) FSEntry {
	chosen := current
	if chosen == nil {
		chosen = target
	}
	if chosen == nil {
		chosen = base
	}
	entry := *chosen
	if target != nil && (current == nil || target.Mtime > current.Mtime) {
		entry = *target
	}
	if mergedID != "" {
		entry.ID = mergedID
	}
	return entry
}

func (h *SyncHandler) mergeSyncDirectoryEntry(repoID string, base, current, target *FSEntry) (FSEntry, bool, error) {
	switch {
	case base == nil && current == nil && target == nil:
		return FSEntry{}, false, nil
	case base == nil && current != nil && target == nil:
		return *current, true, nil
	case base == nil && current == nil && target != nil:
		return *target, true, nil
	case base == nil && current != nil && target != nil:
		if syncEntryEquivalent(current, target) {
			return preferredSyncMergeEntry(current, target, nil, current.ID), true, nil
		}
		if syncEntryIsDir(current) && syncEntryIsDir(target) {
			mergedChildFSID, err := h.mergeSyncDirectoryTrees(repoID, "", current.ID, target.ID)
			if err != nil {
				return FSEntry{}, false, err
			}
			return preferredSyncMergeEntry(current, target, nil, mergedChildFSID), true, nil
		}
		return FSEntry{}, false, errSyncHeadAutoMergeConflict
	case base != nil && current == nil && target == nil:
		return FSEntry{}, false, nil
	case base != nil && current != nil && target == nil:
		if syncEntryEquivalent(current, base) {
			return FSEntry{}, false, nil
		}
		return FSEntry{}, false, errSyncHeadAutoMergeConflict
	case base != nil && current == nil && target != nil:
		if syncEntryEquivalent(target, base) {
			return FSEntry{}, false, nil
		}
		return FSEntry{}, false, errSyncHeadAutoMergeConflict
	default:
		if syncEntryEquivalent(current, base) && syncEntryEquivalent(target, base) {
			return *base, true, nil
		}
		if syncEntryEquivalent(current, base) {
			return *target, true, nil
		}
		if syncEntryEquivalent(target, base) {
			return *current, true, nil
		}
		if syncEntryEquivalent(current, target) {
			return preferredSyncMergeEntry(current, target, base, current.ID), true, nil
		}
		if syncEntryIsDir(base) && syncEntryIsDir(current) && syncEntryIsDir(target) {
			mergedChildFSID, err := h.mergeSyncDirectoryTrees(repoID, base.ID, current.ID, target.ID)
			if err != nil {
				return FSEntry{}, false, err
			}
			return preferredSyncMergeEntry(current, target, base, mergedChildFSID), true, nil
		}
		return FSEntry{}, false, errSyncHeadAutoMergeConflict
	}
}

func (h *SyncHandler) mergeSyncDirectoryTrees(repoID, baseFSID, currentFSID, targetFSID string) (string, error) {
	baseEntries, err := h.readSyncDirectoryEntries(repoID, baseFSID)
	if err != nil {
		return "", fmt.Errorf("failed to read base dir %s: %w", baseFSID, err)
	}
	currentEntries, err := h.readSyncDirectoryEntries(repoID, currentFSID)
	if err != nil {
		return "", fmt.Errorf("failed to read current dir %s: %w", currentFSID, err)
	}
	targetEntries, err := h.readSyncDirectoryEntries(repoID, targetFSID)
	if err != nil {
		return "", fmt.Errorf("failed to read target dir %s: %w", targetFSID, err)
	}

	baseByName := make(map[string]FSEntry, len(baseEntries))
	for _, entry := range baseEntries {
		baseByName[entry.Name] = entry
	}
	currentByName := make(map[string]FSEntry, len(currentEntries))
	for _, entry := range currentEntries {
		currentByName[entry.Name] = entry
	}
	targetByName := make(map[string]FSEntry, len(targetEntries))
	for _, entry := range targetEntries {
		targetByName[entry.Name] = entry
	}

	nameSet := make(map[string]struct{}, len(baseByName)+len(currentByName)+len(targetByName))
	for name := range baseByName {
		nameSet[name] = struct{}{}
	}
	for name := range currentByName {
		nameSet[name] = struct{}{}
	}
	for name := range targetByName {
		nameSet[name] = struct{}{}
	}

	names := make([]string, 0, len(nameSet))
	for name := range nameSet {
		names = append(names, name)
	}
	sort.Strings(names)

	mergedEntries := make([]FSEntry, 0, len(names))
	for _, name := range names {
		var baseEntry, currentEntry, targetEntry *FSEntry
		if entry, ok := baseByName[name]; ok {
			entryCopy := entry
			baseEntry = &entryCopy
		}
		if entry, ok := currentByName[name]; ok {
			entryCopy := entry
			currentEntry = &entryCopy
		}
		if entry, ok := targetByName[name]; ok {
			entryCopy := entry
			targetEntry = &entryCopy
		}

		mergedEntry, keep, err := h.mergeSyncDirectoryEntry(repoID, baseEntry, currentEntry, targetEntry)
		if err != nil {
			return "", err
		}
		if keep {
			mergedEntries = append(mergedEntries, mergedEntry)
		}
	}

	if syncEntrySlicesEquivalent(mergedEntries, currentEntries) {
		return currentFSID, nil
	}
	if syncEntrySlicesEquivalent(mergedEntries, targetEntries) {
		return targetFSID, nil
	}

	return h.createSyncDirectoryFSObject(repoID, mergedEntries)
}

func shortSyncCommitID(commitID string) string {
	if len(commitID) <= 8 {
		return commitID
	}
	return commitID[:8]
}

func isEmptySyncFSID(fsID string) bool {
	fsID = strings.TrimSpace(fsID)
	return fsID == "" || fsID == emptySyncFSID40
}

type syncCommitFileReference struct {
	fsID     string
	blockIDs []string
}

const syncReachableFileBatchSize = 128

type syncCommitBlockDelta struct {
	addedFiles            []syncCommitFileReference
	removedFiles          []syncCommitFileReference
	resolvedAddedBlockIDs []string
}

func (d syncCommitBlockDelta) addedBlockIDs() []string {
	if len(d.addedFiles) == 0 {
		return nil
	}
	blockIDs := make([]string, 0)
	for _, file := range d.addedFiles {
		blockIDs = append(blockIDs, file.blockIDs...)
	}
	return blockIDs
}

func syncBlockUploadOperationID(repoID, blockID string) string {
	return "sync:" + repoID + ":" + blockID
}

func (h *SyncHandler) loadSyncFileBlockIDs(repoID string, fileIDs []string) (map[string][]string, error) {
	resolved := make(map[string][]string, len(fileIDs))
	if len(fileIDs) == 0 {
		return resolved, nil
	}

	want := make(map[string]struct{}, len(fileIDs))
	ordered := make([]string, 0, len(fileIDs))
	for _, fileID := range fileIDs {
		fileID = strings.TrimSpace(fileID)
		if fileID == "" {
			continue
		}
		if _, seen := want[fileID]; seen {
			continue
		}
		want[fileID] = struct{}{}
		ordered = append(ordered, fileID)
	}

	for start := 0; start < len(ordered); start += syncReachableFileBatchSize {
		end := start + syncReachableFileBatchSize
		if end > len(ordered) {
			end = len(ordered)
		}

		iter := h.db.Session().Query(`
			SELECT fs_id, obj_type, block_ids FROM fs_objects WHERE library_id = ? AND fs_id IN ?
		`, repoID, ordered[start:end]).Iter()

		var fsID string
		var objType string
		var blockIDs []string
		for iter.Scan(&fsID, &objType, &blockIDs) {
			if objType != "file" {
				_ = iter.Close()
				return nil, fmt.Errorf("unsupported fs_object type %q for %s", objType, fsID)
			}
			normalized := db.NormalizeBlockIDs(blockIDs)
			if len(normalized) == 0 {
				resolved[fsID] = nil
				continue
			}
			resolved[fsID] = normalized
		}
		if err := iter.Close(); err != nil {
			return nil, fmt.Errorf("load file fs_objects %v: %w", ordered[start:end], err)
		}
	}

	for _, fileID := range ordered {
		if _, ok := resolved[fileID]; !ok {
			return nil, fmt.Errorf("load fs_object %s: not found", fileID)
		}
	}

	return resolved, nil
}

func (h *SyncHandler) collectSyncReachableFiles(repoID, rootFSID string) (map[string][]string, error) {
	files := make(map[string][]string)
	if isEmptySyncFSID(rootFSID) {
		return files, nil
	}
	visited := make(map[string]struct{})
	var walk func(string) error
	walk = func(fsID string) error {
		if isEmptySyncFSID(fsID) {
			return nil
		}
		fsID = strings.TrimSpace(fsID)
		if _, seen := visited[fsID]; seen {
			return nil
		}
		visited[fsID] = struct{}{}

		objType, dirEntries, blockIDs, err := loadSyncFSObjectFn(h, repoID, fsID)
		if err != nil {
			return fmt.Errorf("load fs_object %s: %w", fsID, err)
		}

		switch objType {
		case "file":
			normalized := db.NormalizeBlockIDs(blockIDs)
			if len(normalized) != 0 {
				files[fsID] = normalized
			}
			return nil
		case "dir":
			if dirEntries == "" || dirEntries == "[]" {
				return nil
			}
			var entries []FSEntry
			if err := json.Unmarshal([]byte(dirEntries), &entries); err != nil {
				return fmt.Errorf("parse dir_entries for %s: %w", fsID, err)
			}
			fileIDs := make([]string, 0, len(entries))
			dirIDs := make([]string, 0, len(entries))
			fallbackIDs := make([]string, 0)
			for _, entry := range entries {
				if isEmptySyncFSID(entry.ID) {
					continue
				}
				switch entry.Mode {
				case 33188:
					fileIDs = append(fileIDs, entry.ID)
				case 16384:
					dirIDs = append(dirIDs, entry.ID)
				default:
					fallbackIDs = append(fallbackIDs, entry.ID)
				}
			}
			fileBlockIDs, err := loadSyncFileBlockIDsFn(h, repoID, fileIDs)
			if err != nil {
				return err
			}
			for fileID, resolvedBlockIDs := range fileBlockIDs {
				if len(resolvedBlockIDs) == 0 {
					continue
				}
				files[fileID] = append([]string(nil), resolvedBlockIDs...)
			}
			for _, childID := range dirIDs {
				if err := walk(childID); err != nil {
					return err
				}
			}
			for _, childID := range fallbackIDs {
				if err := walk(childID); err != nil {
					return err
				}
			}
			return nil
		default:
			return fmt.Errorf("unsupported fs_object type %q for %s", objType, fsID)
		}
	}

	if err := walk(rootFSID); err != nil {
		return nil, err
	}
	return files, nil
}

func (h *SyncHandler) collectSyncCommitFiles(repoID, commitID string) (map[string][]string, error) {
	commitID = strings.TrimSpace(commitID)
	if commitID == "" {
		return map[string][]string{}, nil
	}
	rootFSID, err := h.readCommitRootFSID(repoID, commitID)
	if err != nil {
		return nil, fmt.Errorf("read root fs_id for commit %s: %w", commitID, err)
	}
	return h.collectSyncReachableFiles(repoID, rootFSID)
}

// buildSyncCommitBlockDelta computes the file-level block reference delta between
// targetCommitID and its immediate parent.
//
// INVARIANT (load-bearing): the head only ever advances by exactly one generation
// — either a direct fast-forward where the target's parent IS the current head
// (enforced in handleSyncHeadPromotion) or an auto-merge commit whose parent IS
// the current head (createSyncAutoMergeCommit). A single-generation diff therefore
// captures every newly-reachable file, so registering fs: references for the added
// files covers the whole transition. If a future change ever allows the head to
// jump multiple generations at once, files added by skipped intermediate commits
// would never get their permanent references and this delta must be revisited.
func (h *SyncHandler) buildSyncCommitBlockDelta(repoID, targetCommitID string) (syncCommitBlockDelta, error) {
	parentCommitID, _, err := h.readSyncTargetCommit(repoID, targetCommitID)
	if err != nil {
		return syncCommitBlockDelta{}, fmt.Errorf("read target commit %s: %w", targetCommitID, err)
	}

	parentFiles, err := h.collectSyncCommitFiles(repoID, parentCommitID)
	if err != nil {
		return syncCommitBlockDelta{}, fmt.Errorf("collect parent commit %s files: %w", parentCommitID, err)
	}
	targetFiles, err := h.collectSyncCommitFiles(repoID, targetCommitID)
	if err != nil {
		return syncCommitBlockDelta{}, fmt.Errorf("collect target commit %s files: %w", targetCommitID, err)
	}

	delta := syncCommitBlockDelta{}
	for fsID, blockIDs := range targetFiles {
		if _, existed := parentFiles[fsID]; existed {
			continue
		}
		delta.addedFiles = append(delta.addedFiles, syncCommitFileReference{fsID: fsID, blockIDs: append([]string(nil), blockIDs...)})
	}
	for fsID, blockIDs := range parentFiles {
		if _, stillPresent := targetFiles[fsID]; stillPresent {
			continue
		}
		delta.removedFiles = append(delta.removedFiles, syncCommitFileReference{fsID: fsID, blockIDs: append([]string(nil), blockIDs...)})
	}
	sort.Slice(delta.addedFiles, func(i, j int) bool { return delta.addedFiles[i].fsID < delta.addedFiles[j].fsID })
	sort.Slice(delta.removedFiles, func(i, j int) bool { return delta.removedFiles[i].fsID < delta.removedFiles[j].fsID })
	return delta, nil
}

func (h *SyncHandler) resolveSyncBlockIDs(orgID, repoID string, blockIDs []string) ([]string, error) {
	blockIDs = db.NormalizeBlockIDs(blockIDs)
	if len(blockIDs) == 0 {
		return nil, nil
	}
	representationID, err := h.resolveSyncBlockRepresentationID(orgID, repoID)
	if err != nil {
		return nil, err
	}
	resolved, err := streaming.BatchResolveBlockIDs(h.db, orgID, representationID, blockIDs)
	if err != nil {
		return nil, err
	}
	return db.NormalizeBlockIDs(resolved), nil
}

func (h *SyncHandler) stageSyncCommitBlockDelta(orgID, repoID, targetCommitID string) (syncCommitBlockDelta, error) {
	delta, err := buildSyncCommitBlockDeltaFn(h, repoID, targetCommitID)
	if err != nil {
		return syncCommitBlockDelta{}, err
	}
	resolved, err := stageSyncPublishAttemptReferencesFn(h.db, orgID, repoID, targetCommitID, delta.addedBlockIDs(), func(blockIDs []string) ([]string, error) {
		return resolveSyncBlockIDsFn(h, orgID, repoID, blockIDs)
	})
	if err != nil {
		return syncCommitBlockDelta{}, fmt.Errorf("stage publish-attempt refs for sync commit %s: %w", targetCommitID, err)
	}
	delta.resolvedAddedBlockIDs = resolved
	return delta, nil
}

func (h *SyncHandler) removeSyncCommitFileReferences(orgID, repoID string, removedFiles []syncCommitFileReference) error {
	// fs:* references track persisted fs_objects, not membership in the current
	// HEAD. The retention/reachability GC owns removing these rows after the
	// fs_object itself is no longer retained.
	return nil
}

func (h *SyncHandler) finalizeSyncCommitBlockDelta(orgID, repoID, targetCommitID string, delta syncCommitBlockDelta) error {
	if len(delta.resolvedAddedBlockIDs) == 0 && len(delta.addedFiles) > 0 {
		resolved, err := resolveSyncBlockIDsFn(h, orgID, repoID, delta.addedBlockIDs())
		if err != nil {
			return fmt.Errorf("resolve added block IDs for sync commit %s: %w", targetCommitID, err)
		}
		delta.resolvedAddedBlockIDs = resolved
	}

	fsHelper := v2.NewFSHelper(h.db)
	if err := promoteSyncPublishAttemptReferencesFn(h.db, orgID, targetCommitID, delta.resolvedAddedBlockIDs, func() error {
		for _, file := range delta.addedFiles {
			if err := fsHelper.RegisterFSObjectBlockReferences(orgID, repoID, file.fsID, file.blockIDs); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("promote publish-attempt refs for sync commit %s: %w", targetCommitID, err)
	}
	if err := h.removeSyncCommitFileReferences(orgID, repoID, delta.removedFiles); err != nil {
		return err
	}
	// Sync upload up: references remain until Cassandra TTL. Phase 0 discovers
	// their trackers after expiry; permanent fs: refs now own published liveness.
	h.finalizedBlockDeltas.mark(repoID, targetCommitID)
	return nil
}

// repairPublishedSyncCommitBlockDelta re-runs the block-reference reconciliation
// for an already-published head on the idempotent retry path, healing a prior
// publish whose finalize did not complete. It is a no-op when this process already
// finalized the exact (repo, head): the full-tree reachability walk is the costly
// part, so skipping it spares every idempotent retry once finalize has succeeded
// here. A miss (cold process, another instance, evicted entry) safely falls back
// to the full reconciliation, which is idempotent.
func (h *SyncHandler) repairPublishedSyncCommitBlockDelta(orgID, repoID, targetCommitID string) error {
	if h == nil {
		return nil
	}
	if h.finalizedBlockDeltas.contains(repoID, targetCommitID) {
		return nil
	}
	if h.repairPublishedSyncCommitBlockDeltaFn != nil {
		return h.repairPublishedSyncCommitBlockDeltaFn(orgID, repoID, targetCommitID)
	}
	if h.db == nil {
		return nil
	}
	delta, err := buildSyncCommitBlockDeltaFn(h, repoID, targetCommitID)
	if err != nil {
		return err
	}
	return h.finalizeSyncCommitBlockDelta(orgID, repoID, targetCommitID, delta)
}

func (h *SyncHandler) tryAutoMergeSyncHeadPromotion(c *gin.Context, orgID, userID, repoID, currentHead, targetHead, baseHead, operation string) (bool, error) {
	baseRootFSID, err := h.readCommitRootFSID(repoID, baseHead)
	if err != nil {
		return false, fmt.Errorf("read base root fs_id: %w", err)
	}
	currentRootFSID, err := h.readCommitRootFSID(repoID, currentHead)
	if err != nil {
		return false, fmt.Errorf("read current root fs_id: %w", err)
	}
	targetRootFSID, err := h.readCommitRootFSID(repoID, targetHead)
	if err != nil {
		return false, fmt.Errorf("read target root fs_id: %w", err)
	}

	mergedRootFSID, err := h.mergeSyncDirectoryTrees(repoID, baseRootFSID, currentRootFSID, targetRootFSID)
	if err != nil {
		return false, err
	}

	if mergedRootFSID == currentRootFSID {
		log.Printf("%s: auto-merge found repo %s already converged (current=%s target=%s mergedRoot=%s)",
			operation, repoID, shortSyncCommitID(currentHead), shortSyncCommitID(targetHead), shortSyncCommitID(mergedRootFSID))
		c.Status(http.StatusOK)
		return true, nil
	}

	mergedSize := int64(-1)
	if traffic.GetChecker() != nil {
		mergedSize = h.syncCommitPublishedSize(repoID, mergedRootFSID)
	}
	if !h.checkSyncCommitStorageQuotaWithNewSize(c, orgID, userID, repoID, currentHead, mergedSize) {
		return true, nil
	}

	description := fmt.Sprintf("Auto merged concurrent sync update (%s + %s)", shortSyncCommitID(currentHead), shortSyncCommitID(targetHead))
	mergedCommitID, err := h.createSyncAutoMergeCommit(repoID, userID, currentHead, mergedRootFSID, description)
	if err != nil {
		return false, err
	}

	delta, err := h.stageSyncCommitBlockDelta(orgID, repoID, mergedCommitID)
	if err != nil {
		return false, err
	}
	cleanupStaged := true
	defer func() {
		if !cleanupStaged {
			return
		}
		if cleanupErr := db.RemovePublishAttemptReferences(h.db, orgID, mergedCommitID, delta.resolvedAddedBlockIDs); cleanupErr != nil {
			log.Printf("%s: failed to cleanup staged refs for auto-merged commit %s in repo %s: %v", operation, mergedCommitID, repoID, cleanupErr)
		}
	}()

	if err := h.updateLibraryHeadWithStats(orgID, repoID, mergedCommitID, userID, currentHead); err != nil {
		if errors.Is(err, errSyncHeadRepairPending) {
			cleanupStaged = false
			if finalizeErr := h.finalizeSyncCommitBlockDelta(orgID, repoID, mergedCommitID, delta); finalizeErr != nil {
				return false, fmt.Errorf("finalize auto-merged sync commit %s after publish: %w", mergedCommitID, finalizeErr)
			}
			go h.updateFullPaths(repoID, mergedRootFSID)
			c.Header("Retry-After", "1")
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "sync head publish pending storage reconciliation; retry"})
			return true, nil
		}
		return false, err
	}
	cleanupStaged = false
	if err := h.finalizeSyncCommitBlockDelta(orgID, repoID, mergedCommitID, delta); err != nil {
		go h.updateFullPaths(repoID, mergedRootFSID)
		c.Header("Retry-After", "1")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "sync head publish block-reference reconciliation pending; retry"})
		return true, nil
	}

	go h.updateFullPaths(repoID, mergedRootFSID)
	log.Printf("%s: auto-merged repo %s current=%s target=%s into merged=%s",
		operation, repoID, shortSyncCommitID(currentHead), shortSyncCommitID(targetHead), shortSyncCommitID(mergedCommitID))
	c.Status(http.StatusOK)
	return true, nil
}

func (h *SyncHandler) handleSyncHeadPromotion(c *gin.Context, orgID, userID, repoID, targetHead, operation string) {
	maxAttempts := v2.RetryAttempts()

	commitParent, rootFSID, err := h.readSyncTargetCommit(repoID, targetHead)
	if err != nil {
		log.Printf("%s: commit %s not found for repo %s: %v", operation, targetHead, repoID, err)
		c.JSON(http.StatusNotFound, gin.H{"error": "commit not found"})
		return
	}

	cachedNewSize := int64(-1)
	if traffic.GetChecker() != nil {
		cachedNewSize = h.syncCommitPublishedSize(repoID, rootFSID)
	}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		currentHead, err := h.readLibraryHead(orgID, repoID)
		if err != nil {
			log.Printf("%s: failed to read current head for repo %s: %v", operation, repoID, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read current head"})
			return
		}

		if currentHead == targetHead {
			h.handleSyncHeadIdempotentSuccess(c, orgID, repoID, targetHead, operation)
			return
		}

		if commitParent != currentHead {
			log.Printf("%s: parent mismatch for repo %s on attempt %d/%d - commit %s expects parent %s but current HEAD is %s",
				operation, repoID, attempt+1, maxAttempts, targetHead, commitParent, currentHead)

			canAutoMerge := false
			if commitParent != "" {
				canAutoMerge, err = h.syncCommitHasAncestor(repoID, currentHead, commitParent)
				if err != nil {
					log.Printf("%s: failed to evaluate ancestry for repo %s current=%s parent=%s: %v",
						operation, repoID, currentHead, commitParent, err)
					c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to inspect sync commit ancestry"})
					return
				}
			}

			if canAutoMerge {
				resolved, mergeErr := h.tryAutoMergeSyncHeadPromotion(c, orgID, userID, repoID, currentHead, targetHead, commitParent, operation)
				switch {
				case mergeErr == nil && resolved:
					return
				case errors.Is(mergeErr, ErrHeadConflict):
					log.Printf("%s: auto-merge CAS conflict for repo %s on attempt %d/%d", operation, repoID, attempt+1, maxAttempts)
				case errors.Is(mergeErr, errSyncHeadAutoMergeConflict):
					log.Printf("%s: auto-merge could not safely resolve repo %s on attempt %d/%d; retrying direct promotion fallback",
						operation, repoID, attempt+1, maxAttempts)
				case mergeErr != nil:
					log.Printf("%s: auto-merge failed for repo %s: %v", operation, repoID, mergeErr)
					c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to auto-merge sync head"})
					return
				}
			} else {
				log.Printf("%s: current HEAD %s is not a descendant of target parent %s for repo %s on attempt %d/%d; waiting for direct promotion fallback",
					operation, currentHead, commitParent, repoID, attempt+1, maxAttempts)
			}

			if attempt == maxAttempts-1 {
				log.Printf("%s: parent mismatch retry budget exhausted for repo %s targeting %s after %d attempts; returning 503 so clients preserve local state and retry",
					operation, repoID, targetHead, maxAttempts)
				c.Header("Retry-After", "1")
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "sync head publish conflicted; retry"})
				return
			}
			time.Sleep(v2.RetryBackoff(attempt + 1))
			continue
		}

		log.Printf("%s: updating repo %s head to %s (parent=%s, currentHead=%s, attempt=%d/%d)",
			operation, repoID, targetHead, commitParent, currentHead, attempt+1, maxAttempts)

		if !h.checkSyncCommitStorageQuotaWithNewSize(c, orgID, userID, repoID, currentHead, cachedNewSize) {
			return
		}

		delta, err := h.stageSyncCommitBlockDelta(orgID, repoID, targetHead)
		if err != nil {
			log.Printf("%s: failed to stage block refs for repo %s head %s: %v", operation, repoID, targetHead, err)
			c.Header("Retry-After", "1")
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "sync head publish block references pending; retry"})
			return
		}
		cleanupStaged := true

		cleanupAttempt := func() {
			if !cleanupStaged {
				return
			}
			if cleanupErr := db.RemovePublishAttemptReferences(h.db, orgID, targetHead, delta.resolvedAddedBlockIDs); cleanupErr != nil {
				log.Printf("%s: failed to cleanup staged refs for repo %s head %s: %v", operation, repoID, targetHead, cleanupErr)
			}
		}

		if err := h.updateLibraryHeadWithStats(orgID, repoID, targetHead, userID, currentHead); err != nil {
			if !errors.Is(err, ErrHeadConflict) {
				if errors.Is(err, errSyncHeadRepairPending) {
					cleanupStaged = false
					if finalizeErr := h.finalizeSyncCommitBlockDelta(orgID, repoID, targetHead, delta); finalizeErr != nil {
						log.Printf("%s: published repo %s head to %s but block-reference reconciliation failed: %v", operation, repoID, targetHead, finalizeErr)
						if rootFSID != "" {
							go h.updateFullPaths(repoID, rootFSID)
						}
						c.Header("Retry-After", "1")
						c.JSON(http.StatusServiceUnavailable, gin.H{"error": "sync head publish block-reference reconciliation pending; retry"})
						return
					}
					log.Printf("%s: published repo %s head to %s but aggregate storage reconciliation is still pending: %v", operation, repoID, targetHead, err)
					if rootFSID != "" {
						go h.updateFullPaths(repoID, rootFSID)
					}
					c.Header("Retry-After", "1")
					c.JSON(http.StatusServiceUnavailable, gin.H{"error": "sync head publish pending storage reconciliation; retry"})
					return
				}
				cleanupAttempt()
				log.Printf("%s: failed to update head for repo %s: %v", operation, repoID, err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update head"})
				return
			}

			cleanupAttempt()
			log.Printf("%s: CAS conflict for repo %s on attempt %d/%d", operation, repoID, attempt+1, maxAttempts)
			if attempt == maxAttempts-1 {
				log.Printf("%s: CAS conflict budget exhausted for repo %s targeting %s after %d attempts; returning 503 so clients preserve local state and retry",
					operation, repoID, targetHead, maxAttempts)
				c.Header("Retry-After", "1")
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "sync head publish conflicted; retry"})
				return
			}
			time.Sleep(v2.RetryBackoff(attempt + 1))
			continue
		}
		cleanupStaged = false
		if err := h.finalizeSyncCommitBlockDelta(orgID, repoID, targetHead, delta); err != nil {
			log.Printf("%s: published repo %s head to %s but block-reference reconciliation failed: %v", operation, repoID, targetHead, err)
			if rootFSID != "" {
				go h.updateFullPaths(repoID, rootFSID)
			}
			c.Header("Retry-After", "1")
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "sync head publish block-reference reconciliation pending; retry"})
			return
		}

		if rootFSID != "" {
			go h.updateFullPaths(repoID, rootFSID)
		}
		c.Status(http.StatusOK)
		return
	}
}

func (h *SyncHandler) syncCommitPublishedSize(repoID, rootFSID string) int64 {
	if rootFSID == "" || rootFSID == strings.Repeat("0", 40) {
		return 0
	}
	newSize, _ := h.calculateDirStats(repoID, rootFSID)
	return newSize
}

func (h *SyncHandler) checkSyncCommitStorageQuotaWithNewSize(c *gin.Context, orgID, userID, repoID, currentHead string, newSize int64) bool {
	checker := traffic.GetChecker()
	if checker == nil {
		return true
	}

	currentSize, _ := h.commitTreeStats(repoID, currentHead)

	additionalBytes := newSize - currentSize
	if additionalBytes <= 0 {
		return true
	}

	st, _ := checker.CheckStorageQuota(orgID, userID, additionalBytes)
	if !st.Allowed {
		c.JSON(http.StatusForbidden, gin.H{"error": "storage quota exceeded"})
		return false
	}
	return true
}

func (h *SyncHandler) commitTreeStats(repoID, commitID string) (int64, int64) {
	if commitID == "" {
		return 0, 0
	}

	var rootFSID string
	err := h.db.Session().Query(`
		SELECT root_fs_id FROM commits WHERE library_id = ? AND commit_id = ?
	`, repoID, commitID).Scan(&rootFSID)
	if err != nil || rootFSID == "" || rootFSID == strings.Repeat("0", 40) {
		return 0, 0
	}
	return h.calculateDirStats(repoID, rootFSID)
}

func (h *SyncHandler) commitTreeStatsStrict(repoID, commitID string) (int64, int64, error) {
	rootFSID, err := h.readCommitRootFSID(repoID, commitID)
	if err != nil {
		return 0, 0, fmt.Errorf("get root_fs_id for %s: %w", repoID, err)
	}
	if rootFSID == "" || rootFSID == strings.Repeat("0", 40) {
		return 0, 0, nil
	}
	totalSize, fileCount := h.calculateDirStats(repoID, rootFSID)
	return totalSize, fileCount, nil
}

type syncLibraryDerivedState struct {
	HeadCommitID  string
	ProjectionRow db.AdminLibraryProjectionRow
}

type syncAdminProjectionCanary struct {
	Present   bool
	OwnerID   string
	UpdatedAt time.Time
	SizeBytes int64
	FileCount int64
}

func (c syncAdminProjectionCanary) matches(ownerID string, updatedAt time.Time, sizeBytes, fileCount int64) bool {
	if !c.Present {
		return false
	}
	return c.OwnerID == ownerID &&
		c.UpdatedAt.Equal(updatedAt) &&
		c.SizeBytes == sizeBytes &&
		c.FileCount == fileCount
}

type syncDerivedStateCanary struct {
	Canonical        syncLibraryDerivedState
	LookupHead       string
	OrgProjection    syncAdminProjectionCanary
	OwnerProjection  syncAdminProjectionCanary
	GlobalProjection syncAdminProjectionCanary
}

func (c syncDerivedStateCanary) alignedWith(targetHead string) bool {
	row := c.Canonical.ProjectionRow
	return c.Canonical.HeadCommitID == targetHead &&
		c.LookupHead == targetHead &&
		c.OrgProjection.matches(row.OwnerID, row.UpdatedAt, row.SizeBytes, row.FileCount) &&
		c.OwnerProjection.matches(row.OwnerID, row.UpdatedAt, row.SizeBytes, row.FileCount) &&
		c.GlobalProjection.matches(row.OwnerID, row.UpdatedAt, row.SizeBytes, row.FileCount)
}

func retrySyncDerivedStateCanaryRead(targetHead string, readCanary func() (syncDerivedStateCanary, error), sleep func(time.Duration)) (syncDerivedStateCanary, error) {
	var lastErr error
	for attempt := 1; attempt <= libraryStatsProjectionRetryAttempts; attempt++ {
		canary, err := readCanary()
		if err != nil {
			lastErr = err
		} else if canary.Canonical.HeadCommitID != targetHead {
			lastErr = fmt.Errorf("stale canonical sync state: head_commit_id=%s want=%s", canary.Canonical.HeadCommitID, targetHead)
		} else {
			return canary, nil
		}
		if attempt < libraryStatsProjectionRetryAttempts && sleep != nil {
			sleep(libraryStatsProjectionRetryDelay)
		}
	}
	return syncDerivedStateCanary{}, lastErr
}

func (h *SyncHandler) fetchSyncLibraryDerivedState(orgID, repoID string) (syncLibraryDerivedState, error) {
	var state syncLibraryDerivedState
	row := db.AdminLibraryProjectionRow{OrgID: orgID, LibraryID: repoID}
	var deletedAt time.Time
	if err := h.db.Session().Query(`
		SELECT head_commit_id, owner_id, name, encrypted, storage_class, size_bytes, file_count, created_at, updated_at, deleted_at
		FROM libraries
		WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(
		&state.HeadCommitID,
		&row.OwnerID,
		&row.Name,
		&row.Encrypted,
		&row.StorageClass,
		&row.SizeBytes,
		&row.FileCount,
		&row.CreatedAt,
		&row.UpdatedAt,
		&deletedAt,
	); err != nil {
		return syncLibraryDerivedState{}, fmt.Errorf("read canonical sync state: %w", err)
	}
	if !deletedAt.IsZero() {
		deletedCopy := deletedAt
		row.DeletedAt = &deletedCopy
	}
	row.OwnerEmail, row.OwnerName = db.ResolveAdminLibraryOwnerFields(h.db.Session(), orgID, row.OwnerID)
	state.ProjectionRow = row
	return state, nil
}

// retrySyncDerivedStateRead invokes fetch up to libraryStatsProjectionRetryAttempts
// times, accepting the first state whose UpdatedAt is not before notBefore. This
// absorbs the stale-read window that can follow a successful LWT commit when the
// subsequent SELECT is served by a replica that has not yet applied the commit.
func retrySyncDerivedStateReadUntil(repoID string, fetch func() (syncLibraryDerivedState, error), validate func(syncLibraryDerivedState) error, sleep func(time.Duration)) (syncLibraryDerivedState, error) {
	var lastErr error
	for attempt := 1; attempt <= libraryStatsProjectionRetryAttempts; attempt++ {
		state, err := fetch()
		switch {
		case err != nil:
			lastErr = err
		case validate != nil:
			lastErr = validate(state)
			if lastErr == nil {
				return state, nil
			}
		default:
			return state, nil
		}
		if attempt < libraryStatsProjectionRetryAttempts && sleep != nil {
			sleep(libraryStatsProjectionRetryDelay)
		}
	}
	return syncLibraryDerivedState{}, lastErr
}

func retrySyncDerivedStateRead(repoID string, notBefore time.Time, fetch func() (syncLibraryDerivedState, error), sleep func(time.Duration)) (syncLibraryDerivedState, error) {
	return retrySyncDerivedStateReadUntil(
		repoID,
		fetch,
		func(state syncLibraryDerivedState) error {
			if state.ProjectionRow.UpdatedAt.Before(notBefore) {
				return fmt.Errorf("stale canonical sync state for %s: updated_at=%s want>=%s",
					repoID,
					state.ProjectionRow.UpdatedAt.Format(time.RFC3339Nano),
					notBefore.Format(time.RFC3339Nano))
			}
			return nil
		},
		sleep,
	)
}

// syncLibraryHeadDerivedState reconciles libraries_by_id and the admin projection
// against the canonical libraries row. The CAS-controlled fields (head_commit_id,
// size_bytes, file_count, updated_at) are taken directly from the values the CAS
// just wrote rather than from the read-back: depending on read-after-write
// convergence is unnecessary when the writer already holds the authoritative
// values. The read+stale-guard is still required to pick up admin-controlled
// fields (owner, name, encrypted, storage_class, deleted_at) at a snapshot that
// is at least as fresh as the CAS commit moment.
func (h *SyncHandler) syncLibraryHeadDerivedState(orgID, repoID, commitID string, notBefore time.Time, totalSize, fileCount int64) error {
	return syncLibraryHeadDerivedStateUsing(
		repoID,
		notBefore,
		func() (syncLibraryDerivedState, error) {
			return h.fetchSyncLibraryDerivedState(orgID, repoID)
		},
		func(repoID string, state syncLibraryDerivedState) error {
			overrideSyncLibraryCASFields(&state, commitID, notBefore, totalSize, fileCount)
			return h.writeSyncLibraryDerivedState(repoID, state)
		},
		time.Sleep,
	)
}

// overrideSyncLibraryCASFields applies the post-CAS authoritative values to a
// state derived from a read of libraries. It is invoked at write time, after
// retrySyncDerivedStateRead has accepted a row whose UpdatedAt is at least as
// fresh as notBefore; the override then ensures the projection write reflects
// exactly what the CAS committed instead of relying on the read carrying it.
func overrideSyncLibraryCASFields(state *syncLibraryDerivedState, commitID string, updatedAt time.Time, sizeBytes, fileCount int64) {
	state.HeadCommitID = commitID
	state.ProjectionRow.SizeBytes = sizeBytes
	state.ProjectionRow.FileCount = fileCount
	state.ProjectionRow.UpdatedAt = updatedAt
}

// syncLibraryHeadDerivedStateUsing wires the read-with-stale-guard and the
// batch-write into a single sequence. It is exported via lowercase package
// linkage so tests can inject deterministic read/write closures and assert
// the read-side stale loop and the write-side error propagation in isolation.
func syncLibraryHeadDerivedStateUsing(
	repoID string,
	notBefore time.Time,
	read func() (syncLibraryDerivedState, error),
	write func(repoID string, state syncLibraryDerivedState) error,
	sleep func(time.Duration),
) error {
	state, err := retrySyncDerivedStateRead(repoID, notBefore, read, sleep)
	if err != nil {
		return fmt.Errorf("failed to read library state after sync update: %w", err)
	}
	if err := write(repoID, state); err != nil {
		return fmt.Errorf("failed to sync head lookup/admin projection: %w", err)
	}
	return nil
}

func repairPublishedSyncHeadAfterCounterFailureUsing(
	repoID string,
	notBefore time.Time,
	read func() (syncLibraryDerivedState, error),
	reconcileLibraryStorage func(syncLibraryDerivedState) error,
	queueAggregateReconciliation func(syncLibraryDerivedState) error,
	write func(syncLibraryDerivedState) error,
	sleep func(time.Duration),
) error {
	state, err := retrySyncDerivedStateRead(repoID, notBefore, read, sleep)
	if err != nil {
		return fmt.Errorf("failed to read canonical library state for sync head repair: %w", err)
	}
	if err := reconcileLibraryStorage(state); err != nil {
		return fmt.Errorf("failed to reconcile sync library storage state: %w", err)
	}
	if err := queueAggregateReconciliation(state); err != nil {
		return fmt.Errorf("failed to queue aggregate storage reconciliation: %w", err)
	}
	if err := write(state); err != nil {
		return fmt.Errorf("failed to sync head lookup/admin projection: %w", err)
	}
	return nil
}

// repairPublishedSyncHeadAfterCounterFailure runs when the post-CAS storage
// counter delta failed. The CAS-controlled fields (commitID, updatedAt,
// totalSize, fileCount) are propagated authoritatively from the publisher's
// scope to (1) seed the lib-scope counter reconciliation with the truth that
// the CAS just committed and (2) keep the projection row consistent with the
// canonical row without relying on read-after-write convergence. The override
// happens at consume-time (not read-time) so the read-side stale-guard still
// rejects pre-CAS replica snapshots for the admin-controlled fields.
func (h *SyncHandler) repairPublishedSyncHeadAfterCounterFailure(orgID, repoID, commitID string, notBefore time.Time, totalSize, fileCount int64) error {
	return repairPublishedSyncHeadAfterCounterFailureUsing(
		repoID,
		notBefore,
		func() (syncLibraryDerivedState, error) {
			return h.fetchSyncLibraryDerivedState(orgID, repoID)
		},
		func(state syncLibraryDerivedState) error {
			overrideSyncLibraryCASFields(&state, commitID, notBefore, totalSize, fileCount)
			return h.reconcilePublishedSyncLibraryStorage(orgID, repoID, state)
		},
		func(state syncLibraryDerivedState) error {
			return h.queueAggregateStorageReconciliation(orgID, state.ProjectionRow.OwnerID)
		},
		func(state syncLibraryDerivedState) error {
			overrideSyncLibraryCASFields(&state, commitID, notBefore, totalSize, fileCount)
			return h.writeSyncLibraryDerivedState(repoID, state)
		},
		time.Sleep,
	)
}

// handleSyncHeadIdempotentSuccess runs the canary-then-repair flow when the
// caller's targetHead already equals canonical libraries.head_commit_id. A
// previous attempt may have failed AFTER the CAS while running post-CAS side
// effects (storage counters, aggregate reconciliation queue, libraries_by_id
// or admin projection writes). If any of those left derived state behind,
// returning 200 here would silently bless that drift and the client would
// never trigger another publish. The canary check uses libraries_by_id plus
// the active admin projection tables: if they all still reflect canonical,
// derived state is in sync and the caller short-circuits with 200; otherwise
// it re-runs the full post-CAS repair flow before reporting success, and
// returns 503 + Retry-After if the repair itself fails so the client retries
// instead of silently moving on. The repair function is dispatched through
// repairSyncHeadDerivedStateIfDriftedFn when set so unit tests can inject a
// failing canary without needing a real DB session.
func (h *SyncHandler) handleSyncHeadIdempotentSuccess(c *gin.Context, orgID, repoID, targetHead, operation string) {
	if err := h.repairPublishedSyncCommitBlockDelta(orgID, repoID, targetHead); err != nil {
		log.Printf("%s: idempotent path detected block-reference drift for repo %s but repair failed: %v", operation, repoID, err)
		c.Header("Retry-After", "1")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "sync head publish block-reference reconciliation pending; retry"})
		return
	}
	repair := h.repairSyncHeadDerivedStateIfDriftedFn
	if repair == nil {
		repair = h.repairSyncHeadDerivedStateIfDrifted
	}
	if err := repair(orgID, repoID, targetHead); err != nil {
		log.Printf("%s: idempotent path detected derived-state drift for repo %s but repair failed: %v", operation, repoID, err)
		c.Header("Retry-After", "1")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "sync head publish derived state repair pending; retry"})
		return
	}
	log.Printf("%s: repo %s head already at %s; treating as idempotent success", operation, repoID, targetHead)
	c.Status(http.StatusOK)
}

// repairSyncHeadDerivedStateIfDrifted is invoked from the idempotent fast-path
// in handleSyncHeadPromotion. It uses an O(1) canary over libraries_by_id plus
// the active admin projection tables against the canonical libraries row. When
// they all still reflect the published head/stats, derived state is consistent
// and the caller can short-circuit. When any surface drifted, a prior publish
// attempt left derived state behind (counter delta failure with failed repair,
// aggregate-reconcile queue insert failure, or a partial lookup/admin
// projection write). In that case it re-runs the full post-CAS repair flow
// against the canonical row, guaranteeing that an otherwise-200-returning
// idempotent retry actually converges derived state.
func (h *SyncHandler) repairSyncHeadDerivedStateIfDrifted(orgID, repoID, targetHead string) error {
	return repairSyncHeadDerivedStateIfDriftedUsing(
		targetHead,
		func() (syncDerivedStateCanary, error) { return h.readSyncDerivedStateCanary(orgID, repoID) },
		func(updatedAt time.Time, totalSize, fileCount int64) error {
			return h.repairPublishedSyncHeadAfterCounterFailure(orgID, repoID, targetHead, updatedAt, totalSize, fileCount)
		},
		time.Sleep,
	)
}

// repairSyncHeadDerivedStateIfDriftedUsing is the testable seam: it composes
// the canary read and the repair invocation so unit tests can drive each
// branch with deterministic closures.
func repairSyncHeadDerivedStateIfDriftedUsing(
	targetHead string,
	readCanary func() (syncDerivedStateCanary, error),
	repair func(updatedAt time.Time, totalSize, fileCount int64) error,
	sleep func(time.Duration),
) error {
	canary, err := retrySyncDerivedStateCanaryRead(targetHead, readCanary, sleep)
	if err != nil {
		return fmt.Errorf("read sync derived-state canary: %w", err)
	}
	if canary.alignedWith(targetHead) {
		return nil
	}
	if err := repair(
		canary.Canonical.ProjectionRow.UpdatedAt,
		canary.Canonical.ProjectionRow.SizeBytes,
		canary.Canonical.ProjectionRow.FileCount,
	); err != nil {
		return fmt.Errorf("repair drifted sync derived state: %w", err)
	}
	return nil
}

func (h *SyncHandler) readSyncDerivedStateCanary(orgID, repoID string) (syncDerivedStateCanary, error) {
	state, err := h.fetchSyncLibraryDerivedState(orgID, repoID)
	if err != nil {
		return syncDerivedStateCanary{}, err
	}
	lookupHead, err := h.readLookupHead(repoID)
	if err != nil {
		return syncDerivedStateCanary{}, err
	}
	orgProjection, err := h.readOrgProjectionCanary(orgID, repoID)
	if err != nil {
		return syncDerivedStateCanary{}, err
	}
	ownerProjection, err := h.readOwnerProjectionCanary(orgID, state.ProjectionRow.OwnerID, repoID)
	if err != nil {
		return syncDerivedStateCanary{}, err
	}
	globalProjection, err := h.readGlobalProjectionCanary(db.AdminLibraryBucketDay(state.ProjectionRow.CreatedAt), orgID, repoID)
	if err != nil {
		return syncDerivedStateCanary{}, err
	}
	return syncDerivedStateCanary{
		Canonical:        state,
		LookupHead:       lookupHead,
		OrgProjection:    orgProjection,
		OwnerProjection:  ownerProjection,
		GlobalProjection: globalProjection,
	}, nil
}

func (h *SyncHandler) readLookupHead(repoID string) (string, error) {
	var lookupHead string
	if err := h.db.Session().Query(`
		SELECT head_commit_id FROM libraries_by_id WHERE library_id = ?
	`, repoID).Scan(&lookupHead); err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return "", nil
		}
		return "", fmt.Errorf("read libraries_by_id head: %w", err)
	}
	return lookupHead, nil
}

func (h *SyncHandler) readOrgProjectionCanary(orgID, repoID string) (syncAdminProjectionCanary, error) {
	var canary syncAdminProjectionCanary
	if err := h.db.Session().Query(`
		SELECT owner_id, updated_at, size_bytes, file_count FROM libraries_by_org_updated WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&canary.OwnerID, &canary.UpdatedAt, &canary.SizeBytes, &canary.FileCount); err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return syncAdminProjectionCanary{}, nil
		}
		return syncAdminProjectionCanary{}, fmt.Errorf("read org admin projection canary: %w", err)
	}
	canary.Present = true
	return canary, nil
}

func (h *SyncHandler) readOwnerProjectionCanary(orgID, ownerID, repoID string) (syncAdminProjectionCanary, error) {
	var canary syncAdminProjectionCanary
	if err := h.db.Session().Query(`
		SELECT updated_at, size_bytes, file_count FROM libraries_by_owner WHERE org_id = ? AND owner_id = ? AND library_id = ?
	`, orgID, ownerID, repoID).Scan(&canary.UpdatedAt, &canary.SizeBytes, &canary.FileCount); err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return syncAdminProjectionCanary{}, nil
		}
		return syncAdminProjectionCanary{}, fmt.Errorf("read owner admin projection canary: %w", err)
	}
	canary.Present = true
	canary.OwnerID = ownerID
	return canary, nil
}

func (h *SyncHandler) readGlobalProjectionCanary(bucketDay, orgID, repoID string) (syncAdminProjectionCanary, error) {
	var canary syncAdminProjectionCanary
	if err := h.db.Session().Query(`
		SELECT owner_id, updated_at, size_bytes, file_count FROM libraries_admin_global_by_updated WHERE bucket_day = ? AND org_id = ? AND library_id = ?
	`, bucketDay, orgID, repoID).Scan(&canary.OwnerID, &canary.UpdatedAt, &canary.SizeBytes, &canary.FileCount); err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return syncAdminProjectionCanary{}, nil
		}
		return syncAdminProjectionCanary{}, fmt.Errorf("read global admin projection canary: %w", err)
	}
	canary.Present = true
	return canary, nil
}

func (h *SyncHandler) reconcilePublishedSyncLibraryStorage(orgID, repoID string, state syncLibraryDerivedState) error {
	if err := traffic.ReconcileStorageScope(h.db, traffic.LibraryStorageScope(orgID, repoID), traffic.StorageSnapshot{
		BytesUsed: state.ProjectionRow.SizeBytes,
		FileCount: state.ProjectionRow.FileCount,
	}); err != nil {
		return fmt.Errorf("reconcile library scope: %w", err)
	}
	return nil
}

func (h *SyncHandler) queueAggregateStorageReconciliation(orgID, ownerID string) error {
	batch := h.db.Session().Batch(gocql.LoggedBatch)
	traffic.AddAggregateStorageReconciliationQueries(batch, orgID, ownerID, time.Now().UTC())
	if err := batch.Exec(); err != nil {
		return fmt.Errorf("queue aggregate storage reconciliation: %w", err)
	}
	return nil
}

func (h *SyncHandler) writeSyncLibraryDerivedState(repoID string, state syncLibraryDerivedState) error {
	batch := h.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		UPDATE libraries_by_id SET head_commit_id = ?
		WHERE library_id = ?
	`, state.HeadCommitID, repoID)
	db.AddUpsertAdminLibraryReadModelQuery(batch, state.ProjectionRow)
	return batch.Exec()
}

const (
	libraryStatsProjectionRetryAttempts = 3
	libraryStatsProjectionRetryDelay    = 50 * time.Millisecond
)

func retryLibraryStatsProjectionSync(sync func() error, sleep func(time.Duration)) error {
	var lastErr error
	for attempt := 1; attempt <= libraryStatsProjectionRetryAttempts; attempt++ {
		if err := sync(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if attempt < libraryStatsProjectionRetryAttempts && sleep != nil {
			sleep(libraryStatsProjectionRetryDelay)
		}
	}
	return lastErr
}

// calculateDirStats recursively calculates total size and file count for a directory.
func (h *SyncHandler) calculateDirStats(repoID, dirFSID string) (totalSize int64, fileCount int64) {
	var dirEntriesJSON string
	err := h.db.Session().Query(`
		SELECT dir_entries FROM fs_objects WHERE library_id = ? AND fs_id = ?
	`, repoID, dirFSID).Scan(&dirEntriesJSON)
	if err != nil || dirEntriesJSON == "" || dirEntriesJSON == "[]" {
		return 0, 0
	}

	var entries []map[string]interface{}
	if err := json.Unmarshal([]byte(dirEntriesJSON), &entries); err != nil {
		return 0, 0
	}

	for _, entry := range entries {
		mode, ok := entry["mode"].(float64)
		if !ok {
			continue
		}
		if mode == 16384 { // Directory
			childID, ok := entry["id"].(string)
			if !ok {
				continue
			}
			childSize, childCount := h.calculateDirStats(repoID, childID)
			totalSize += childSize
			fileCount += childCount
		} else if mode == 33188 { // Regular file
			if size, ok := entry["size"].(float64); ok {
				totalSize += int64(size)
			} else if sizeInt, ok := entry["size"].(int64); ok {
				totalSize += sizeInt
			}
			fileCount++
		}
	}

	return totalSize, fileCount
}
