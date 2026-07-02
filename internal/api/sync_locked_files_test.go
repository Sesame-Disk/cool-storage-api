package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/gin-gonic/gin"
)

func withListRepoLocksStub(t *testing.T, stub func(h *SyncHandler, repoID string) ([]db.RepoLockedFile, error)) {
	t.Helper()
	old := listRepoLocksFn
	listRepoLocksFn = stub
	t.Cleanup(func() {
		listRepoLocksFn = old
	})
}

// stubTokenValidator maps token string -> AccessToken, standing in for the
// production TokenStore in SyncTokenValidator position.
type stubTokenValidator struct {
	tokens map[string]*AccessToken
}

func (s *stubTokenValidator) GetToken(tokenStr string, expectedType TokenType) (*AccessToken, bool) {
	tok, ok := s.tokens[tokenStr]
	if !ok || tok.Type != expectedType {
		return nil, false
	}
	return tok, true
}

func newTestSyncHandler() *SyncHandler {
	return NewSyncHandler(nil, nil, nil, nil, nil, nil)
}

func withAccountStatusStub(h *SyncHandler, stub SyncAccountStatusChecker) {
	h.accountStatus = stub
}

func newLockedFilesRouter(h *SyncHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/seafhttp/repo/locked-files", h.GetLockedFiles)
	return r
}

func postLockedFiles(r *gin.Engine, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/seafhttp/repo/locked-files", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// downloadTokenFor models the repo-level sync token download-info issues:
// TokenTypeDownload with root path and non-link source.
func downloadTokenFor(repoID, userID string) *AccessToken {
	return &AccessToken{Type: TokenTypeDownload, RepoID: repoID, UserID: userID, Path: "/"}
}

// GetFolderPerm's wire format was confirmed live against a genuine Seafile Pro
// 11.0.16 instance (2026-07-02): the response must be a JSON array, not an
// object — the previous `{}` response was not protocol-correct even though it
// never produced a visible client error.
func TestGetFolderPerm_ReturnsEmptyArrayNotObject(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := newTestSyncHandler()

	r := gin.New()
	r.GET("/seafhttp/repo/folder-perm", handler.GetFolderPerm)
	r.POST("/seafhttp/repo/folder-perm", handler.GetFolderPerm)

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		req := httptest.NewRequest(method, "/seafhttp/repo/folder-perm", strings.NewReader(`[{"repo_id":"r1","token":"t","ts":0}]`))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", method, w.Code)
		}
		if got := strings.TrimSpace(w.Body.String()); got != "[]" {
			t.Fatalf("%s body = %q, want %q", method, got, "[]")
		}
	}
}

func TestGetLockedFiles_InvalidJSONReturns400(t *testing.T) {
	handler := newTestSyncHandler()
	w := postLockedFiles(newLockedFilesRouter(handler), `not json`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestGetLockedFiles_TooManyEntriesReturns400(t *testing.T) {
	handler := newTestSyncHandler()
	handler.tokenValidator = &stubTokenValidator{}

	entries := make([]string, maxLockedFilesRepos+1)
	for i := range entries {
		entries[i] = fmt.Sprintf(`{"repo_id":"repo-%d","token":"t","ts":0}`, i)
	}
	w := postLockedFiles(newLockedFilesRouter(handler), "["+strings.Join(entries, ",")+"]")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// Without a wired token validator there is no way to authenticate the per-repo
// tokens, so the handler must fail closed and return no lock data at all.
func TestGetLockedFiles_NoValidatorFailsClosed(t *testing.T) {
	handler := newTestSyncHandler()
	locksQueried := false
	withListRepoLocksStub(t, func(h *SyncHandler, repoID string) ([]db.RepoLockedFile, error) {
		locksQueried = true
		return []db.RepoLockedFile{{Path: "/secret.docx", LockedBy: "user-1"}}, nil
	})

	w := postLockedFiles(newLockedFilesRouter(handler), `[{"repo_id":"repo-1","token":"anything","ts":0}]`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := strings.TrimSpace(w.Body.String()); got != "[]" {
		t.Fatalf("body = %q, want %q", got, "[]")
	}
	if locksQueried {
		t.Fatal("lock data must not be queried when no token validator is wired")
	}
}

// A missing, unknown, or cross-repo token must yield the same response as a
// repo with no locks — the entry is omitted, never an error that would confirm
// the repo exists.
func TestGetLockedFiles_InvalidTokenOmitsRepoWithoutQuerying(t *testing.T) {
	handler := newTestSyncHandler()
	handler.tokenValidator = &stubTokenValidator{tokens: map[string]*AccessToken{
		"tok-other-repo": downloadTokenFor("repo-OTHER", "user-1"),
	}}
	locksQueried := false
	withListRepoLocksStub(t, func(h *SyncHandler, repoID string) ([]db.RepoLockedFile, error) {
		locksQueried = true
		return []db.RepoLockedFile{{Path: "/secret.docx", LockedBy: "user-1"}}, nil
	})

	body := `[
		{"repo_id":"repo-1","token":"","ts":0},
		{"repo_id":"repo-1","token":"unknown-token","ts":0},
		{"repo_id":"repo-1","token":"tok-other-repo","ts":0}
	]`
	w := postLockedFiles(newLockedFilesRouter(handler), body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := strings.TrimSpace(w.Body.String()); got != "[]" {
		t.Fatalf("body = %q, want %q", got, "[]")
	}
	if locksQueried {
		t.Fatal("lock data must not be queried for entries that fail token validation")
	}
}

// TokenTypeDownload is shared with narrower grants: path-scoped file-download
// tokens and share-link tokens (Source=="link"). Neither may widen into
// repo-wide lock enumeration — only the root-path, non-link sync token from
// download-info qualifies.
func TestGetLockedFiles_RejectsNarrowerDownloadTokens(t *testing.T) {
	handler := newTestSyncHandler()
	handler.tokenValidator = &stubTokenValidator{tokens: map[string]*AccessToken{
		"tok-file-scoped": {Type: TokenTypeDownload, RepoID: "repo-1", UserID: "user-1", Path: "/docs/report.docx"},
		"tok-share-link":  {Type: TokenTypeDownload, RepoID: "repo-1", UserID: "user-1", Path: "/", Source: "link"},
	}}
	locksQueried := false
	withListRepoLocksStub(t, func(h *SyncHandler, repoID string) ([]db.RepoLockedFile, error) {
		locksQueried = true
		return []db.RepoLockedFile{{Path: "/secret.docx", LockedBy: "user-2"}}, nil
	})

	body := `[
		{"repo_id":"repo-1","token":"tok-file-scoped","ts":0},
		{"repo_id":"repo-1","token":"tok-share-link","ts":0}
	]`
	w := postLockedFiles(newLockedFilesRouter(handler), body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := strings.TrimSpace(w.Body.String()); got != "[]" {
		t.Fatalf("body = %q, want %q (narrow tokens must not enumerate locks)", got, "[]")
	}
	if locksQueried {
		t.Fatal("lock data must not be queried for path-scoped or share-link tokens")
	}
}

func TestGetLockedFiles_OversizedBodyReturns400(t *testing.T) {
	handler := newTestSyncHandler()
	handler.tokenValidator = &stubTokenValidator{}

	// A single oversized entry: the body must be rejected by the byte limit
	// before entry-count or token checks ever run.
	body := `[{"repo_id":"repo-1","token":"` + strings.Repeat("x", maxLockedFilesBodyBytes) + `","ts":0}]`
	w := postLockedFiles(newLockedFilesRouter(handler), body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestGetLockedFiles_LockBackendErrorFailsClosed(t *testing.T) {
	handler := newTestSyncHandler()
	handler.tokenValidator = &stubTokenValidator{tokens: map[string]*AccessToken{
		"tok-1": downloadTokenFor("repo-1", "user-1"),
	}}
	withListRepoLocksStub(t, func(h *SyncHandler, repoID string) ([]db.RepoLockedFile, error) {
		return nil, db.ErrFileLockStatusUnavailable
	})

	w := postLockedFiles(newLockedFilesRouter(handler), `[{"repo_id":"repo-1","token":"tok-1","ts":0}]`)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	if !strings.Contains(w.Body.String(), "file lock status unavailable") {
		t.Fatalf("body = %q, want file lock status unavailable error", w.Body.String())
	}
}

// A duplicate entry with an invalid token must not shadow a later entry for
// the same repo carrying a valid token — dedupe runs after validation.
func TestGetLockedFiles_InvalidDuplicateDoesNotShadowValidEntry(t *testing.T) {
	handler := newTestSyncHandler()
	handler.tokenValidator = &stubTokenValidator{tokens: map[string]*AccessToken{
		"tok-valid": downloadTokenFor("repo-1", "user-1"),
	}}
	withListRepoLocksStub(t, func(h *SyncHandler, repoID string) ([]db.RepoLockedFile, error) {
		return []db.RepoLockedFile{{Path: "/a.txt", LockedBy: "user-1"}}, nil
	})

	body := `[
		{"repo_id":"repo-1","token":"tok-stale","ts":0},
		{"repo_id":"repo-1","token":"tok-valid","ts":0}
	]`
	w := postLockedFiles(newLockedFilesRouter(handler), body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"repo_id":"repo-1"`) {
		t.Fatalf("body = %q, want repo-1's locks (valid entry shadowed by invalid duplicate)", w.Body.String())
	}
}

// Verified live against a genuine Seafile Pro 11.0.16 instance (2026-07-02):
// repos with no locks are omitted entirely from the response array, rather
// than included with an empty locked_files list.
func TestGetLockedFiles_OmitsAuthorizedReposWithNoLocks(t *testing.T) {
	handler := newTestSyncHandler()
	handler.tokenValidator = &stubTokenValidator{tokens: map[string]*AccessToken{
		"tok-1": downloadTokenFor("repo-1", "user-1"),
	}}
	withListRepoLocksStub(t, func(h *SyncHandler, repoID string) ([]db.RepoLockedFile, error) {
		return nil, nil
	})

	w := postLockedFiles(newLockedFilesRouter(handler), `[{"repo_id":"repo-1","token":"tok-1","ts":0}]`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := strings.TrimSpace(w.Body.String()); got != "[]" {
		t.Fatalf("body = %q, want %q", got, "[]")
	}
}

func TestGetLockedFiles_ByMeReflectsTokenUser(t *testing.T) {
	handler := newTestSyncHandler()
	handler.tokenValidator = &stubTokenValidator{tokens: map[string]*AccessToken{
		"tok-1": downloadTokenFor("repo-1", "user-1"),
	}}
	withListRepoLocksStub(t, func(h *SyncHandler, repoID string) ([]db.RepoLockedFile, error) {
		return []db.RepoLockedFile{
			{Path: "/mine.docx", LockedBy: "USER-1"}, // case differs from token's user-1
			{Path: "/theirs.docx", LockedBy: "user-2"},
		}, nil
	})

	w := postLockedFiles(newLockedFilesRouter(handler), `[{"repo_id":"repo-1","token":"tok-1","ts":0}]`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp []struct {
		RepoID      string `json:"repo_id"`
		LockedFiles []struct {
			Path string `json:"path"`
			ByMe bool   `json:"by_me"`
		} `json:"locked_files"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not a JSON array: %v (body %q)", err, w.Body.String())
	}
	if len(resp) != 1 || resp[0].RepoID != "repo-1" || len(resp[0].LockedFiles) != 2 {
		t.Fatalf("unexpected response shape: %q", w.Body.String())
	}
	byPath := map[string]bool{}
	for _, lf := range resp[0].LockedFiles {
		byPath[lf.Path] = lf.ByMe
	}
	if !byPath["/mine.docx"] {
		t.Fatal("by_me = false for the token user's own lock (case-insensitive match expected)")
	}
	if byPath["/theirs.docx"] {
		t.Fatal("by_me = true for another user's lock")
	}
}

func TestGetLockedFiles_DeduplicatesRepoEntries(t *testing.T) {
	handler := newTestSyncHandler()
	handler.tokenValidator = &stubTokenValidator{tokens: map[string]*AccessToken{
		"tok-1": downloadTokenFor("repo-1", "user-1"),
	}}
	queryCount := 0
	accountChecks := 0
	withAccountStatusStub(handler, func(c *gin.Context, userID, orgID string) error {
		accountChecks++
		return nil
	})
	withListRepoLocksStub(t, func(h *SyncHandler, repoID string) ([]db.RepoLockedFile, error) {
		queryCount++
		return []db.RepoLockedFile{{Path: "/a.txt", LockedBy: "user-2"}}, nil
	})

	body := `[
		{"repo_id":"repo-1","token":"tok-1","ts":0},
		{"repo_id":"repo-1","token":"tok-1","ts":0},
		{"repo_id":"repo-1","token":"tok-1","ts":0}
	]`
	w := postLockedFiles(newLockedFilesRouter(handler), body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if queryCount != 1 {
		t.Fatalf("lock queries = %d, want 1 (duplicates must be skipped)", queryCount)
	}
	if accountChecks != 1 {
		t.Fatalf("account status checks = %d, want 1 (duplicates must be skipped before account-status validation)", accountChecks)
	}
	if got := strings.Count(w.Body.String(), `"repo_id":"repo-1"`); got != 1 {
		t.Fatalf("repo-1 appears %d times in response, want 1", got)
	}
}

func TestGetLockedFiles_AccountStatusCheckCanRejectToken(t *testing.T) {
	handler := newTestSyncHandler()
	handler.tokenValidator = &stubTokenValidator{tokens: map[string]*AccessToken{
		"tok-1": downloadTokenFor("repo-1", "user-1"),
	}}
	locksQueried := false
	withAccountStatusStub(handler, func(c *gin.Context, userID, orgID string) error {
		c.JSON(http.StatusForbidden, gin.H{"error": "account deactivated"})
		c.Abort()
		return fmt.Errorf("account deactivated")
	})
	withListRepoLocksStub(t, func(h *SyncHandler, repoID string) ([]db.RepoLockedFile, error) {
		locksQueried = true
		return []db.RepoLockedFile{{Path: "/a.txt", LockedBy: "user-1"}}, nil
	})

	w := postLockedFiles(newLockedFilesRouter(handler), `[{"repo_id":"repo-1","token":"tok-1","ts":0}]`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if locksQueried {
		t.Fatal("lock data must not be queried when account status check rejects the token")
	}
}

// SetTokenCreator must pick up the validator role when the provided creator
// also implements SyncTokenValidator — this is how production wiring
// (server_routes.go passing the full TokenStore) enables token validation.
func TestSetTokenCreator_WiresValidatorWhenAvailable(t *testing.T) {
	handler := newTestSyncHandler()
	store := NewMockTokenStore()
	handler.SetTokenCreator(store)
	if handler.tokenValidator == nil {
		t.Fatal("SetTokenCreator did not wire the token validator from a full TokenStore")
	}

	tokenStr, err := store.CreateDownloadToken("org1", "repo-1", "/", "user-1")
	if err != nil {
		t.Fatalf("CreateDownloadToken() error = %v", err)
	}
	withListRepoLocksStub(t, func(h *SyncHandler, repoID string) ([]db.RepoLockedFile, error) {
		return []db.RepoLockedFile{{Path: "/a.txt", LockedBy: "user-1"}}, nil
	})

	w := postLockedFiles(newLockedFilesRouter(handler),
		`[{"repo_id":"repo-1","token":"`+tokenStr+`","ts":0}]`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"by_me":true`) {
		t.Fatalf("body = %q, want the token user's lock reported with by_me:true", w.Body.String())
	}
}
