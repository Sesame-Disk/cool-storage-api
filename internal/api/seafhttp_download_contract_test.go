package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v2 "github.com/Sesame-Disk/sesamefs/internal/api/v2"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
)

const (
	dirEntryIDA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	dirEntryIDB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// A validated listing may distinguish a missing entry internally, but HTTP still
// maps that observation to 503 because LOCAL_QUORUM may have returned an older
// cross-DC snapshot. Every corrupt shape below must also fail closed.
func TestFindValidatedDirEntryAbsenceRequiresAValidatedListing(t *testing.T) {
	tests := []struct {
		name       string
		rawEntries string
		lookFor    string
		wantAbsent bool
		wantID     string
	}{
		{
			name:       "well-formed listing without the entry is an internal absence",
			rawEntries: fmt.Sprintf(`[{"name":"other.txt","id":"%s","mode":33188}]`, dirEntryIDA),
			lookFor:    "wanted.txt",
			wantAbsent: true,
		},
		{
			name:       "empty listing is absence",
			rawEntries: `[]`,
			lookFor:    "wanted.txt",
			wantAbsent: true,
		},
		{
			name:       "match resolves",
			rawEntries: fmt.Sprintf(`[{"name":"wanted.txt","id":"%s","mode":33188}]`, dirEntryIDA),
			lookFor:    "wanted.txt",
			wantID:     dirEntryIDA,
		},
		{
			name:       "blank payload is not absence",
			rawEntries: "",
			lookFor:    "wanted.txt",
		},
		{
			name:       "whitespace-only payload is not absence",
			rawEntries: "   \n\t ",
			lookFor:    "wanted.txt",
		},
		{
			name:       "JSON null payload is not absence",
			rawEntries: "null",
			lookFor:    "wanted.txt",
		},
		{
			name:       "unparseable payload is not absence",
			rawEntries: `[{"name":`,
			lookFor:    "wanted.txt",
		},
		{
			name:       "null entry is not absence",
			rawEntries: `[null]`,
			lookFor:    "wanted.txt",
		},
		{
			name:       "non-object entry is not absence",
			rawEntries: `["wanted.txt"]`,
			lookFor:    "wanted.txt",
		},
		{
			name:       "non-string name is not absence",
			rawEntries: fmt.Sprintf(`[{"name":42,"id":"%s","mode":33188}]`, dirEntryIDA),
			lookFor:    "wanted.txt",
		},
		{
			name:       "empty name is not absence",
			rawEntries: fmt.Sprintf(`[{"name":"","id":"%s","mode":33188}]`, dirEntryIDA),
			lookFor:    "wanted.txt",
		},
		{
			name:       "missing id is not absence",
			rawEntries: `[{"name":"other.txt","mode":33188}]`,
			lookFor:    "wanted.txt",
		},
		{
			name:       "non-40-hex id is not absence",
			rawEntries: `[{"name":"other.txt","id":"nothex","mode":33188}]`,
			lookFor:    "wanted.txt",
		},
		{
			name:       "non-string id is not absence",
			rawEntries: `[{"name":"other.txt","id":null,"mode":33188}]`,
			lookFor:    "wanted.txt",
		},
		{
			// encoding/json silently keeps the LAST value, which would serve B.
			name:       "duplicate id key on the requested entry fails closed",
			rawEntries: fmt.Sprintf(`[{"name":"wanted.txt","id":"%s","id":"%s","mode":33188}]`, dirEntryIDA, dirEntryIDB),
			lookFor:    "wanted.txt",
		},
		{
			// encoding/json would hide "wanted.txt" entirely and report absence.
			name:       "duplicate name key hiding the requested entry fails closed",
			rawEntries: fmt.Sprintf(`[{"name":"wanted.txt","name":"other.txt","id":"%s","mode":33188}]`, dirEntryIDA),
			lookFor:    "wanted.txt",
		},
		{
			name:       "duplicate names for the requested entry fail closed",
			rawEntries: fmt.Sprintf(`[{"name":"wanted.txt","id":"%s","mode":33188},{"name":"wanted.txt","id":"%s","mode":33188}]`, dirEntryIDA, dirEntryIDB),
			lookFor:    "wanted.txt",
		},
		{
			name:       "duplicate names elsewhere block an absence claim",
			rawEntries: fmt.Sprintf(`[{"name":"dup","id":"%s","mode":33188},{"name":"dup","id":"%s","mode":33188}]`, dirEntryIDA, dirEntryIDB),
			lookFor:    "wanted.txt",
		},
		{
			name:       "a corrupt sibling blocks an absence claim",
			rawEntries: fmt.Sprintf(`[{"name":"other.txt","id":"%s","mode":33188},{"name":"bad.txt","id":"short","mode":33188}]`, dirEntryIDA),
			lookFor:    "wanted.txt",
		},
		{
			// Reported by review: the earlier "corrupt sibling" exception returned
			// the valid copy here, even though the listing is ambiguous about the
			// requested name and the other copy might be the real one.
			name:       "a corrupt copy of the requested name fails closed",
			rawEntries: fmt.Sprintf(`[{"name":"wanted.txt","id":"%s","mode":33188},{"name":"wanted.txt","id":"short","mode":33188}]`, dirEntryIDA),
			lookFor:    "wanted.txt",
		},
		{
			name:       "a corrupt copy of the requested name fails closed in either order",
			rawEntries: fmt.Sprintf(`[{"name":"wanted.txt","id":"short","mode":33188},{"name":"wanted.txt","id":"%s","mode":33188}]`, dirEntryIDA),
			lookFor:    "wanted.txt",
		},
		{
			// A name hidden behind a repeated key could be the requested one.
			name:       "a sibling hiding a name behind a repeated key fails closed",
			rawEntries: fmt.Sprintf(`[{"name":"a","name":"b","id":"%s","mode":33188},{"name":"wanted.txt","id":"%s","mode":33188}]`, dirEntryIDA, dirEntryIDB),
			lookFor:    "wanted.txt",
		},
		{
			// All-or-nothing: any corrupt entry blocks resolution, not just absence.
			name:       "a corrupt sibling fails the whole listing closed",
			rawEntries: fmt.Sprintf(`[{"name":"bad.txt","id":"short","mode":33188},{"name":"wanted.txt","id":"%s","mode":33188}]`, dirEntryIDA),
			lookFor:    "wanted.txt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry, err := findValidatedDirEntry(tt.rawEntries, tt.lookFor)
			switch {
			case tt.wantID != "":
				if err != nil || entry.id != tt.wantID {
					t.Fatalf("findValidatedDirEntry() = (%q, %v), want (%q, nil)", entry.id, err, tt.wantID)
				}
			case tt.wantAbsent:
				if !errors.Is(err, errDirEntryAbsent) {
					t.Fatalf("findValidatedDirEntry() error = %v, want errDirEntryAbsent", err)
				}
			default:
				if err == nil {
					t.Fatalf("findValidatedDirEntry() = (%q, nil), want a non-absence error", entry.id)
				}
				if errors.Is(err, errDirEntryAbsent) {
					t.Fatalf("findValidatedDirEntry() reported absence from an unvalidated listing: %v", err)
				}
			}
		})
	}
}

// Property: absence is never reported unless the listing is well formed AND
// contains no matching name. Three separate audit rounds each found a corrupt
// shape hand-written cases had missed, so the shapes here are generated.
func TestFindValidatedDirEntryNeverInventsAbsence(t *testing.T) {
	rng := rand.New(rand.NewSource(20260722))
	ids := []string{dirEntryIDA, dirEntryIDB, "short", "", "NOTHEXNOTHEXNOTHEXNOTHEXNOTHEXNOTHEXNOTH"}
	names := []string{"wanted.txt", "other.txt", "", "dup"}
	const target = "wanted.txt"

	for i := 0; i < 3000; i++ {
		entryCount := rng.Intn(4)
		rawParts := make([]string, 0, entryCount)
		for j := 0; j < entryCount; j++ {
			switch rng.Intn(10) {
			case 0:
				rawParts = append(rawParts, "null")
			case 1:
				rawParts = append(rawParts, `"scalar"`)
			case 2: // duplicate name key
				rawParts = append(rawParts, fmt.Sprintf(`{"name":%q,"name":%q,"id":%q,"mode":33188}`,
					names[rng.Intn(len(names))], names[rng.Intn(len(names))], ids[rng.Intn(len(ids))]))
			case 3: // duplicate id key
				rawParts = append(rawParts, fmt.Sprintf(`{"name":%q,"id":%q,"id":%q,"mode":33188}`,
					names[rng.Intn(len(names))], ids[rng.Intn(len(ids))], ids[rng.Intn(len(ids))]))
			case 4: // missing id
				rawParts = append(rawParts, fmt.Sprintf(`{"name":%q,"mode":33188}`, names[rng.Intn(len(names))]))
			case 5: // missing mode
				rawParts = append(rawParts, fmt.Sprintf(`{"name":%q,"id":%q}`,
					names[rng.Intn(len(names))], ids[rng.Intn(len(ids))]))
			case 6: // null or fractional mode
				modes := []string{"null", "16384.5", "0", "999999"}
				rawParts = append(rawParts, fmt.Sprintf(`{"name":%q,"id":%q,"mode":%s}`,
					names[rng.Intn(len(names))], ids[rng.Intn(len(ids))], modes[rng.Intn(len(modes))]))
			case 7: // unsafe archive path component
				unsafeNames := []string{".", "..", "../escape", `dir\escape`, "nul\x00name"}
				encodedName, _ := json.Marshal(unsafeNames[rng.Intn(len(unsafeNames))])
				rawParts = append(rawParts, fmt.Sprintf(`{"name":%s,"id":%q,"mode":33188}`,
					encodedName, ids[rng.Intn(len(ids))]))
			default: // potentially valid entry
				rawParts = append(rawParts, fmt.Sprintf(`{"name":%q,"id":%q,"mode":33188}`,
					names[rng.Intn(len(names))], ids[rng.Intn(len(ids))]))
			}
		}
		raw := "[" + strings.Join(rawParts, ",") + "]"

		entry, err := findValidatedDirEntry(raw, target)
		if !errors.Is(err, errDirEntryAbsent) {
			if err == nil && entry.id == "" {
				t.Fatalf("findValidatedDirEntry(%s) returned an empty id with no error", raw)
			}
			continue
		}

		// Absence was claimed — independently prove the listing really is
		// well formed and really has no matching entry.
		var entries []json.RawMessage
		if jsonErr := json.Unmarshal([]byte(raw), &entries); jsonErr != nil {
			t.Fatalf("absence claimed for unparseable listing %s", raw)
		}
		for _, entry := range entries {
			parsed, entryErr := parseValidatedDirEntry(entry)
			if entryErr != nil {
				t.Fatalf("absence claimed despite invalid entry %s in %s: %v", entry, raw, entryErr)
			}
			if parsed.name == target {
				t.Fatalf("absence claimed for listing %s that names %q (id %s)", raw, target, parsed.id)
			}
		}
	}
}

// LOCAL_QUORUM cannot prove global absence: a valid listing may be an older
// cross-DC snapshot. Absence and dangling metadata must both remain retryable.
func TestRespondSeafHTTPDownloadErrorNeverMapsLocalAbsenceTo404(t *testing.T) {
	tests := []struct {
		name               string
		err                error
		wantStatus         int
		wantLibNeedDecrypt bool
	}{
		{name: "local absence may be a stale cross-DC snapshot", err: fmt.Errorf("%w: wanted.txt", errDirEntryAbsent), wantStatus: 503},
		{name: "wrapped local absence remains retryable", err: fmt.Errorf("file not found: %s: %w", "wanted.txt", fmt.Errorf("%w: wanted.txt", errDirEntryAbsent)), wantStatus: 503},
		{name: "bare gocql not found on a referenced row", err: fmt.Errorf("commit not found: %w", gocql.ErrNotFound), wantStatus: 503},
		// Retrying without a decrypt session can never succeed, so this must not
		// join the retryable bucket; the zip path already answered 403.
		{name: "encrypted library without a decrypt session", err: v2.ErrLibraryEncryptedNotUnlocked, wantStatus: 403, wantLibNeedDecrypt: true},
		{name: "wrapped encrypted library error", err: fmt.Errorf("lookup: %w", v2.ErrLibraryEncryptedNotUnlocked), wantStatus: 403, wantLibNeedDecrypt: true},
		{name: "corrupt listing", err: errors.New("directory abc: malformed directory listing"), wantStatus: 503},
		{name: "cassandra timeout", err: errors.New("gocql: no response received from cassandra within timeout period"), wantStatus: 503},
		{name: "block store unavailable", err: errors.New("block store not available"), wantStatus: 503},
		{name: "encryption probe failure is retryable", err: fmt.Errorf("failed to check library encryption: %w", errors.New("cassandra timeout")), wantStatus: 503},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("GET", "/seafhttp/files/tok/f.txt", nil)

			respondSeafHTTPDownloadError(c, "repo-1", "/f.txt", tt.err)

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body=%s)", w.Code, tt.wantStatus, w.Body.String())
			}
			if tt.wantStatus == 503 && w.Header().Get("Retry-After") == "" {
				t.Fatal("503 must carry Retry-After so the client knows to retry")
			}
			if tt.wantLibNeedDecrypt {
				var body map[string]interface{}
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatalf("decode 403 body: %v", err)
				}
				if body["lib_need_decrypt"] != true {
					t.Fatalf("lib_need_decrypt = %v, want true (body=%s)", body["lib_need_decrypt"], w.Body.String())
				}
			}
		})
	}
}

// Once streaming has committed headers there is no status left to write; the
// handler must not try to emit one on top of a partial body.
func TestRespondSeafHTTPDownloadErrorDoesNotRewriteACommittedResponse(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/seafhttp/files/tok/f.txt", nil)
	c.Status(200)
	if _, err := c.Writer.Write([]byte("partial")); err != nil {
		t.Fatalf("seed write failed: %v", err)
	}

	respondSeafHTTPDownloadError(c, "repo-1", "/f.txt", fmt.Errorf("%w: f.txt", errDirEntryAbsent))

	if w.Code != 200 || w.Body.String() != "partial" {
		t.Fatalf("committed response was rewritten: status=%d body=%q", w.Code, w.Body.String())
	}
}

// The zip walk shares this parser. It previously used a map-based parse that kept
// the last value of a repeated key and silently skipped entries without a name or
// id, so a corrupt listing produced a 200 zip with wrong or missing content.
func TestParseValidatedDirEntriesServesTheZipWalk(t *testing.T) {
	t.Run("directory mode is recognised", func(t *testing.T) {
		entries, err := parseValidatedDirEntries(fmt.Sprintf(
			`[{"name":"sub","id":"%s","mode":16384},{"name":"f.txt","id":"%s","mode":33188}]`,
			dirEntryIDA, dirEntryIDB))
		if err != nil {
			t.Fatalf("parseValidatedDirEntries() error = %v", err)
		}
		if len(entries) != 2 {
			t.Fatalf("len(entries) = %d, want 2", len(entries))
		}
		if !entries[0].isDir() {
			t.Fatal("entry with mode 16384 must be a directory")
		}
		if entries[1].isDir() {
			t.Fatal("entry with mode 33188 must not be a directory")
		}
	})

	t.Run("a repeated id key is rejected instead of resolving to the last value", func(t *testing.T) {
		if _, err := parseValidatedDirEntries(fmt.Sprintf(
			`[{"name":"f.txt","id":"%s","id":"%s"}]`, dirEntryIDA, dirEntryIDB)); err == nil {
			t.Fatal("repeated id key must fail closed")
		}
	})

	t.Run("an entry without an id is rejected instead of skipped", func(t *testing.T) {
		if _, err := parseValidatedDirEntries(`[{"name":"f.txt","mode":33188}]`); err == nil {
			t.Fatal("entry without an id must fail closed, not be silently dropped")
		}
	})

	t.Run("a blank listing is rejected instead of read as empty", func(t *testing.T) {
		if _, err := parseValidatedDirEntries(""); err == nil {
			t.Fatal("blank listing must fail closed, not look like an empty directory")
		}
	})

	t.Run("unsafe archive path components are rejected", func(t *testing.T) {
		for _, name := range []string{".", "..", "../escape.txt", `dir\escape.txt`, "nul\x00name"} {
			encodedName, err := json.Marshal(name)
			if err != nil {
				t.Fatalf("marshal unsafe name %q: %v", name, err)
			}
			raw := fmt.Sprintf(`[{"name":%s,"id":"%s","mode":33188}]`, encodedName, dirEntryIDA)
			if _, err := parseValidatedDirEntries(raw); err == nil {
				t.Fatalf("unsafe name %q must fail closed", name)
			}
		}
	})

	t.Run("mode must be present and an exact recognised integer", func(t *testing.T) {
		modes := []string{"", `,"mode":null`, `,"mode":"16384"`, `,"mode":16384.5`, `,"mode":0`, `,"mode":999999`}
		for _, mode := range modes {
			raw := fmt.Sprintf(`[{"name":"f.txt","id":"%s"%s}]`, dirEntryIDA, mode)
			if _, err := parseValidatedDirEntries(raw); err == nil {
				t.Fatalf("invalid mode fragment %q must fail closed", mode)
			}
		}
	})
}

// ZIP used to ignore the encrypted-probe error and stream ciphertext as a 200.
// The probe runs before any Cassandra head/commit walk so these cases stay
// unit-testable without a live session.
func TestHandleZipDownloadEncryptionProbeFailsClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newZipHandler := func(t *testing.T) (*SeafHTTPHandler, string) {
		t.Helper()
		tokenStore := NewMockTokenStore()
		tok, err := tokenStore.CreateDownloadToken("org-1", "repo-1", "/", "user-1")
		if err != nil {
			t.Fatalf("CreateDownloadToken: %v", err)
		}
		return &SeafHTTPHandler{
			tokenStore:     tokenStore,
			db:             &db.DB{},
			storageManager: &storage.Manager{},
		}, tok
	}

	t.Run("cassandra failure on encrypted probe is retryable 503", func(t *testing.T) {
		h, tok := newZipHandler(t)
		old := seafHTTPLookupLibraryEncryptedFn
		t.Cleanup(func() { seafHTTPLookupLibraryEncryptedFn = old })
		seafHTTPLookupLibraryEncryptedFn = func(context.Context, *SeafHTTPHandler, string, string) (bool, error) {
			return false, errors.New("cassandra timeout")
		}

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/seafhttp/zip/"+tok, nil)
		c.Params = gin.Params{{Key: "token", Value: tok}}

		h.HandleZipDownload(c)

		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503 (body=%s)", w.Code, w.Body.String())
		}
		if w.Header().Get("Retry-After") == "" {
			t.Fatal("503 must carry Retry-After")
		}
		if strings.Contains(w.Header().Get("Content-Type"), "application/zip") {
			t.Fatalf("must not commit zip headers after encryption probe failure: %q", w.Header().Get("Content-Type"))
		}
	})

	t.Run("encrypted library without decrypt session emits lib_need_decrypt", func(t *testing.T) {
		h, tok := newZipHandler(t)
		old := seafHTTPLookupLibraryEncryptedFn
		t.Cleanup(func() { seafHTTPLookupLibraryEncryptedFn = old })
		seafHTTPLookupLibraryEncryptedFn = func(context.Context, *SeafHTTPHandler, string, string) (bool, error) {
			return true, nil
		}

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/seafhttp/zip/"+tok, nil)
		c.Params = gin.Params{{Key: "token", Value: tok}}

		h.HandleZipDownload(c)

		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 (body=%s)", w.Code, w.Body.String())
		}
		var body map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode 403 body: %v", err)
		}
		if body["lib_need_decrypt"] != true {
			t.Fatalf("lib_need_decrypt = %v, want true (body=%s)", body["lib_need_decrypt"], w.Body.String())
		}
	})
}

// A download token stays valid for up to an hour, so an admin can revoke the
// granular "download" flag while leaving read access intact. The two endpoints
// used to carry separate copies of the gate and drifted: HandleDownload
// re-checked the flag, HandleZipDownload did not, so the same revoked user could
// still pull the whole directory as a ZIP. Both now share one gate.
func TestBothDownloadSurfacesShareOneAuthorizationGate(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newHandler := func(t *testing.T) (*SeafHTTPHandler, string) {
		t.Helper()
		tokenStore := NewMockTokenStore()
		tok, err := tokenStore.CreateDownloadToken("org-1", "repo-1", "/f.txt", "user-1")
		if err != nil {
			t.Fatalf("CreateDownloadToken: %v", err)
		}
		return &SeafHTTPHandler{
			tokenStore:     tokenStore,
			db:             &db.DB{},
			storageManager: &storage.Manager{},
		}, tok
	}

	surfaces := map[string]struct {
		path   string
		invoke func(*SeafHTTPHandler, *gin.Context)
	}{
		"single file": {path: "/seafhttp/files/", invoke: (*SeafHTTPHandler).HandleDownload},
		"zip":         {path: "/seafhttp/zip/", invoke: (*SeafHTTPHandler).HandleZipDownload},
	}

	for name, surface := range surfaces {
		t.Run(name+" denies a revoked download permission", func(t *testing.T) {
			h, tok := newHandler(t)
			old := seafHTTPAuthorizeDownloadFn
			t.Cleanup(func() { seafHTTPAuthorizeDownloadFn = old })
			called := false
			seafHTTPAuthorizeDownloadFn = func(_ *SeafHTTPHandler, c *gin.Context, _ *AccessToken) bool {
				called = true
				c.JSON(http.StatusForbidden, gin.H{"error": "download is not allowed by your permission"})
				return false
			}

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, surface.path+tok, nil)
			c.Params = gin.Params{{Key: "token", Value: tok}, {Key: "filepath", Value: "/f.txt"}}

			surface.invoke(h, c)

			if !called {
				t.Fatal("handler did not consult the shared download gate")
			}
			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403 (body=%s)", w.Code, w.Body.String())
			}
			if strings.Contains(w.Header().Get("Content-Type"), "application/zip") {
				t.Fatalf("must not start a zip for a revoked download permission: %q", w.Header().Get("Content-Type"))
			}
		})
	}
}

func TestDownloadSurfacesRejectLinkTokensWithoutSourceID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tokenStore := NewMockTokenStore()
	const tokenString = "blank-source-download-token"
	tokenStore.tokens[tokenString] = &AccessToken{
		Token:  tokenString,
		Type:   TokenTypeDownload,
		Source: "link",
		OrgID:  "org-1",
		RepoID: "repo-1",
		Path:   "/f.txt",
		UserID: "user-1",
	}
	h := &SeafHTTPHandler{
		tokenStore:     tokenStore,
		db:             &db.DB{},
		storageManager: &storage.Manager{},
	}

	old := seafHTTPAuthorizeDownloadFn
	t.Cleanup(func() { seafHTTPAuthorizeDownloadFn = old })
	called := false
	seafHTTPAuthorizeDownloadFn = func(_ *SeafHTTPHandler, _ *gin.Context, _ *AccessToken) bool {
		called = true
		return true
	}

	surfaces := map[string]struct {
		path   string
		invoke func(*SeafHTTPHandler, *gin.Context)
	}{
		"single file": {path: "/seafhttp/files/", invoke: (*SeafHTTPHandler).HandleDownload},
		"zip":         {path: "/seafhttp/zip/", invoke: (*SeafHTTPHandler).HandleZipDownload},
	}
	for name, surface := range surfaces {
		t.Run(name, func(t *testing.T) {
			called = false
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, surface.path+tokenString, nil)
			c.Params = gin.Params{{Key: "token", Value: tokenString}, {Key: "filepath", Value: "/f.txt"}}

			surface.invoke(h, c)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401 (body=%s)", w.Code, w.Body.String())
			}
			if called {
				t.Fatal("authorization ran before blank-source token rejection")
			}
		})
	}
}

// fakeDownloadPermissions models a real permission state rather than stubbing
// the gate's answer, so the gate's own logic is what gets exercised.
type fakeDownloadPermissions struct {
	read         bool
	readErr      error
	downloadFlag bool
	flagAsked    bool
}

func (f *fakeDownloadPermissions) HasLibraryAccess(_, _, _ string, _ middleware.LibraryPermission) (bool, error) {
	return f.read, f.readErr
}

func (f *fakeDownloadPermissions) RequirePermFlagForRepo(_ *gin.Context, _ string, flag string) bool {
	if flag == "download" {
		f.flagAsked = true
	}
	return f.downloadFlag
}

// The revocation regression: read access survives, the granular download flag is
// taken away, and the gate must deny. A stubbed gate or an AST name check would
// both pass an implementation that calls RequirePermFlagForRepo and then ignores
// its result; this fails unless the returned value actually decides the outcome.
func TestAuthorizeDownloadDeniesWhenTheDownloadFlagIsRevoked(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name          string
		perms         *fakeDownloadPermissions
		wantAllowed   bool
		wantStatus    int
		wantFlagAsked bool
	}{
		{
			name:          "download revoked, read intact",
			perms:         &fakeDownloadPermissions{read: true, downloadFlag: false},
			wantStatus:    http.StatusForbidden,
			wantFlagAsked: true,
		},
		{
			name:          "both granted",
			perms:         &fakeDownloadPermissions{read: true, downloadFlag: true},
			wantAllowed:   true,
			wantStatus:    http.StatusOK,
			wantFlagAsked: true,
		},
		{
			name:       "read revoked short circuits before the flag",
			perms:      &fakeDownloadPermissions{read: false, downloadFlag: true},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "permission lookup failure fails closed",
			perms:      &fakeDownloadPermissions{read: true, readErr: errors.New("cassandra timeout"), downloadFlag: true},
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/seafhttp/zip/tok", nil)
			token := &AccessToken{OrgID: "org-1", UserID: "user-1", RepoID: "repo-1"}

			allowed := authorizeDownloadWithChecker(tt.perms, c, token)

			if allowed != tt.wantAllowed {
				t.Fatalf("allowed = %v, want %v (body=%s)", allowed, tt.wantAllowed, w.Body.String())
			}
			if !tt.wantAllowed && w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body=%s)", w.Code, tt.wantStatus, w.Body.String())
			}
			if tt.perms.flagAsked != tt.wantFlagAsked {
				t.Fatalf("download flag consulted = %v, want %v", tt.perms.flagAsked, tt.wantFlagAsked)
			}
			if strings.Contains(w.Header().Get("Content-Type"), "application/zip") {
				t.Fatalf("denied request must not begin a zip: %q", w.Header().Get("Content-Type"))
			}
		})
	}
}
