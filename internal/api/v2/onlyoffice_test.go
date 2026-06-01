package v2

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/crypto"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// TestGenerateDocKey tests the document key generation
func TestGenerateDocKey(t *testing.T) {
	tests := []struct {
		name     string
		repoID   string
		filePath string
		fileID   string
	}{
		{
			name:     "basic document",
			repoID:   "repo-123",
			filePath: "/documents/test.docx",
			fileID:   "file-456",
		},
		{
			name:     "root file",
			repoID:   "abc-def-ghi",
			filePath: "/readme.md",
			fileID:   "xyz-789",
		},
		{
			name:     "nested path",
			repoID:   "repo-1",
			filePath: "/a/b/c/d/e/file.xlsx",
			fileID:   "file-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := generateDocKey(tt.repoID, tt.filePath, tt.fileID)

			// Should be exactly 20 characters (truncated MD5)
			if len(result) != 20 {
				t.Errorf("generateDocKey() length = %d, want 20", len(result))
			}

			// Should be deterministic - same inputs = same output
			result2 := generateDocKey(tt.repoID, tt.filePath, tt.fileID)
			if result != result2 {
				t.Errorf("generateDocKey() not deterministic: %s != %s", result, result2)
			}

			// Should be hex string
			for _, c := range result {
				if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
					t.Errorf("generateDocKey() contains non-hex char: %c", c)
				}
			}
		})
	}

	// Different inputs should produce different keys
	key1 := generateDocKey("repo1", "/file1.doc", "id1")
	key2 := generateDocKey("repo2", "/file1.doc", "id1")
	key3 := generateDocKey("repo1", "/file2.doc", "id1")
	key4 := generateDocKey("repo1", "/file1.doc", "id2")

	if key1 == key2 {
		t.Error("Different repoID should produce different keys")
	}
	if key1 == key3 {
		t.Error("Different filePath should produce different keys")
	}
	if key1 == key4 {
		t.Error("Different fileID should produce different keys")
	}
}

func TestOnlyOfficeResolveLibraryBlockStoreUsesLocalRegion(t *testing.T) {
	manager := storage.NewManager()
	manager.SetDefaultClass("hot-s3-usa")
	manager.SetLocalRegion("eu")
	manager.SetRegionClasses(map[string]storage.RegionClassConfig{
		"usa": {Hot: "hot-s3-usa"},
		"eu":  {Hot: "hot-s3-eu"},
	})
	manager.RegisterBackend("hot-s3-usa", &storage.S3Store{}, "")
	manager.RegisterBackend("hot-s3-eu", &storage.S3Store{}, "")

	h := &OnlyOfficeHandler{
		config: &config.Config{
			Server: config.ServerConfig{Region: "eu"},
			Storage: config.StorageConfig{
				DefaultClass: "hot-s3-usa",
			},
		},
		storageManager: manager,
	}

	blockStore, storageClass, err := h.resolveLibraryBlockStore("org-id", "repo-id")
	if err != nil {
		t.Fatalf("resolveLibraryBlockStore returned error: %v", err)
	}
	if blockStore == nil {
		t.Fatal("expected blockStore, got nil")
	}
	if storageClass != "hot-s3-eu" {
		t.Fatalf("storageClass = %q, want %q", storageClass, "hot-s3-eu")
	}
}

func TestCleanupOnlyOfficeFailedPublishAttempt_JoinsRepairCleanupAndQueueClear(t *testing.T) {
	oldCleanup := onlyOfficeCleanupFailedPublishAttemptFn
	oldClear := onlyOfficeClearPendingPublishedFileRepairsFn
	oldRelease := releasePendingPublishedFileOwnersFn
	t.Cleanup(func() {
		onlyOfficeCleanupFailedPublishAttemptFn = oldCleanup
		onlyOfficeClearPendingPublishedFileRepairsFn = oldClear
		releasePendingPublishedFileOwnersFn = oldRelease
	})

	cleanupErr := errors.New("cleanup failed")
	clearErr := errors.New("clear failed")
	cleanupCalls := 0
	onlyOfficeCleanupFailedPublishAttemptFn = func(database *db.DB, orgID, repoID, attemptID, commitID string, fsIDs, blockIDs []string) error {
		cleanupCalls++
		if orgID != "org-1" || repoID != "repo-1" || attemptID != "commit-1" || commitID != "commit-1" {
			t.Fatalf("cleanup args = %s/%s/%s/%s, want org-1/repo-1/commit-1/commit-1", orgID, repoID, attemptID, commitID)
		}
		if len(fsIDs) != 1 || fsIDs[0] != "fs-1" {
			t.Fatalf("cleanup fsIDs = %#v, want []string{\"fs-1\"}", fsIDs)
		}
		if len(blockIDs) != 1 || blockIDs[0] != "queued-block-1" {
			t.Fatalf("cleanup blockIDs = %#v, want []string{\"queued-block-1\"}", blockIDs)
		}
		return cleanupErr
	}
	releaseCalls := 0
	releasePendingPublishedFileOwnersFn = func(database *db.DB, repoID string, pendingFiles []*pendingPublishedFile) error {
		releaseCalls++
		return nil
	}
	clearCalls := 0
	onlyOfficeClearPendingPublishedFileRepairsFn = func(database *db.DB, orgID, repoID, commitID string, pendingFiles []*pendingPublishedFile) error {
		clearCalls++
		if len(pendingFiles) != 1 || pendingFiles[0] == nil || pendingFiles[0].fsID != "fs-1" {
			t.Fatalf("pendingFiles = %#v, want one fs-1 pending file", pendingFiles)
		}
		return clearErr
	}

	err := cleanupOnlyOfficeFailedPublishAttempt(nil, "org-1", "repo-1", "commit-1", []*pendingPublishedFile{{fsID: "fs-1", internalBlockIDs: []string{"queued-block-1"}}})
	if !errors.Is(err, cleanupErr) {
		t.Fatalf("cleanupOnlyOfficeFailedPublishAttempt() error = %v, want cleanupErr %v", err, cleanupErr)
	}
	if errors.Is(err, clearErr) {
		t.Fatalf("cleanupOnlyOfficeFailedPublishAttempt() error = %v, do not want clearErr %v", err, clearErr)
	}
	if cleanupCalls != 1 {
		t.Fatalf("cleanupCalls = %d, want 1", cleanupCalls)
	}
	if releaseCalls != 0 {
		t.Fatalf("releaseCalls = %d, want 0", releaseCalls)
	}
	if clearCalls != 0 {
		t.Fatalf("clearCalls = %d, want 0", clearCalls)
	}
}

func TestFinalizeSuccessfulOnlyOfficeEdit_ReleasesProvisionalRefBeforeCleanup(t *testing.T) {
	oldRelease := onlyOfficeReleaseUploadedBlockRefsFn
	oldDelete := onlyOfficeDeletePendingBlockFn
	oldAdjust := onlyOfficeAdjustStorageCountersFn
	t.Cleanup(func() {
		onlyOfficeReleaseUploadedBlockRefsFn = oldRelease
		onlyOfficeDeletePendingBlockFn = oldDelete
		onlyOfficeAdjustStorageCountersFn = oldAdjust
	})

	h := &OnlyOfficeHandler{}
	var calls []string
	onlyOfficeReleaseUploadedBlockRefsFn = func(database *db.DB, orgID, repoID, operationID string, blockIDs []string) {
		calls = append(calls, "release")
		if orgID != "org-1" || repoID != "repo-1" || operationID != "rollback-1" {
			t.Fatalf("release args = %s/%s/%s, want org-1/repo-1/rollback-1", orgID, repoID, operationID)
		}
		if len(blockIDs) != 1 || blockIDs[0] != "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" {
			t.Fatalf("release blockIDs = %#v, want the internal block id", blockIDs)
		}
	}
	onlyOfficeDeletePendingBlockFn = func(handler *OnlyOfficeHandler, orgID, operationID string) error {
		calls = append(calls, "delete")
		if handler != h {
			t.Fatal("delete should receive the same handler")
		}
		if orgID != "org-1" || operationID != "rollback-1" {
			t.Fatalf("delete args = %s/%s, want org-1/rollback-1", orgID, operationID)
		}
		return nil
	}
	onlyOfficeAdjustStorageCountersFn = func(database traffic.DBSession, orgID, userID, repoID string, deltaBytes, deltaFiles int64) error {
		calls = append(calls, "traffic")
		if orgID != "org-1" || userID != "user-1" || repoID != "repo-1" {
			t.Fatalf("traffic args = %s/%s/%s, want org-1/user-1/repo-1", orgID, userID, repoID)
		}
		if deltaBytes != 123 || deltaFiles != 1 {
			t.Fatalf("traffic deltas = %d/%d, want 123/1", deltaBytes, deltaFiles)
		}
		return nil
	}

	h.finalizeSuccessfulOnlyOfficeEdit(
		"org-1",
		"repo-1",
		"user-1",
		"rollback-1",
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"/docs/test.docx",
		"0123456789abcdef0123456789abcdef01234567",
		"0123456789abcdef0123456789abcdef01234567",
		123,
		1,
	)

	want := []string{"release", "delete", "traffic"}
	if len(calls) != len(want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("calls[%d] = %q, want %q (full=%#v)", i, calls[i], want[i], calls)
		}
	}
}

func TestResolveOnlyOfficeServerURL(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("uses explicit onlyoffice server url", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		req := httptest.NewRequest(http.MethodGet, "/api/v2.1/repos/repo-1/onlyoffice?p=/doc.docx", nil)
		req.Host = "files.example.com"
		c.Request = req

		got := resolveOnlyOfficeServerURL(c, "https://office-facing.example.com/", "", "https://office-facing.example.com/web-apps/apps/api/documents/api.js")
		if got != "https://office-facing.example.com" {
			t.Fatalf("resolveOnlyOfficeServerURL() = %q, want %q", got, "https://office-facing.example.com")
		}
	})

	t.Run("falls back to current browser url", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		req := httptest.NewRequest(http.MethodGet, "/api/v2.1/repos/repo-1/onlyoffice?p=/doc.docx", nil)
		req.Host = "files.example.com"
		req.Header.Set("X-Forwarded-Proto", "https")
		c.Request = req

		got := resolveOnlyOfficeServerURL(c, "", "", "https://office.example.com/web-apps/apps/api/documents/api.js")
		if got != "https://files.example.com" {
			t.Fatalf("resolveOnlyOfficeServerURL() = %q, want %q", got, "https://files.example.com")
		}
	})

	t.Run("uses docker frontend host when browser and api urls are loopback", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		req := httptest.NewRequest(http.MethodGet, "/api/v2.1/repos/repo-1/onlyoffice?p=/doc.docx", nil)
		req.Host = "localhost:3000"
		c.Request = req

		got := resolveOnlyOfficeServerURL(c, "", "", "http://localhost:8088/web-apps/apps/api/documents/api.js")
		if got != dockerComposeFrontendURL {
			t.Fatalf("resolveOnlyOfficeServerURL() = %q, want %q", got, dockerComposeFrontendURL)
		}
	})
}

func TestBuildOnlyOfficeDownloadURL(t *testing.T) {
	got := buildOnlyOfficeDownloadURL("https://files.example.com", "token-123", "Westpark Terriories Aug 2018.docx")
	want := "https://files.example.com/seafhttp/files/token-123/Westpark%20Terriories%20Aug%202018.docx"
	if got != want {
		t.Fatalf("buildOnlyOfficeDownloadURL() = %q, want %q", got, want)
	}
}

func TestResolveOnlyOfficeInternalURL(t *testing.T) {
	t.Run("uses explicit internal url", func(t *testing.T) {
		got := resolveOnlyOfficeInternalURL("http://localhost:8088/web-apps/apps/api/documents/api.js", "http://onlyoffice:80")
		if got != "http://onlyoffice:80" {
			t.Fatalf("resolveOnlyOfficeInternalURL() = %q, want %q", got, "http://onlyoffice:80")
		}
	})

	t.Run("auto-detects docker onlyoffice service for loopback api url", func(t *testing.T) {
		got := resolveOnlyOfficeInternalURL("http://localhost:8088/web-apps/apps/api/documents/api.js", "")
		if got != dockerComposeOnlyOfficeURL {
			t.Fatalf("resolveOnlyOfficeInternalURL() = %q, want %q", got, dockerComposeOnlyOfficeURL)
		}
	})
}

func TestWriteBlockIDMappingDualWrite(t *testing.T) {
	t.Run("writes forward then reverse", func(t *testing.T) {
		calls := make([]string, 0, 2)
		err := db.WriteBlockIDMappingDualWrite(
			func() error {
				calls = append(calls, "forward")
				return nil
			},
			func() error {
				calls = append(calls, "rollback")
				return nil
			},
			func() error {
				calls = append(calls, "reverse")
				return nil
			},
		)
		if err != nil {
			t.Fatalf("writeBlockIDMappingDualWrite returned error: %v", err)
		}
		want := []string{"forward", "reverse"}
		if len(calls) != len(want) {
			t.Fatalf("call count = %d, want %d (%v)", len(calls), len(want), calls)
		}
		for i := range want {
			if calls[i] != want[i] {
				t.Fatalf("calls[%d] = %q, want %q (full=%v)", i, calls[i], want[i], calls)
			}
		}
	})

	t.Run("stops on forward failure", func(t *testing.T) {
		reverseCalls := 0
		rollbackCalls := 0
		wantErr := errors.New("forward failed")
		err := db.WriteBlockIDMappingDualWrite(
			func() error { return wantErr },
			func() error {
				rollbackCalls++
				return nil
			},
			func() error {
				reverseCalls++
				return nil
			},
		)
		if !errors.Is(err, wantErr) {
			t.Fatalf("error = %v, want %v", err, wantErr)
		}
		if reverseCalls != 0 {
			t.Fatalf("reverse calls = %d, want 0 after forward failure", reverseCalls)
		}
		if rollbackCalls != 0 {
			t.Fatalf("rollback calls = %d, want 0 after forward failure", rollbackCalls)
		}
	})

	t.Run("fails closed on reverse failure", func(t *testing.T) {
		calls := make([]string, 0, 2)
		wantErr := errors.New("reverse failed")
		err := db.WriteBlockIDMappingDualWrite(
			func() error {
				calls = append(calls, "forward")
				return nil
			},
			func() error {
				calls = append(calls, "rollback")
				return nil
			},
			func() error {
				calls = append(calls, "reverse")
				return wantErr
			},
		)
		if !errors.Is(err, wantErr) {
			t.Fatalf("error = %v, want %v", err, wantErr)
		}
		want := []string{"forward", "reverse", "rollback"}
		if len(calls) != len(want) {
			t.Fatalf("call count = %d, want %d (%v)", len(calls), len(want), calls)
		}
		for i := range want {
			if calls[i] != want[i] {
				t.Fatalf("calls[%d] = %q, want %q (full=%v)", i, calls[i], want[i], calls)
			}
		}
	})

	t.Run("preserves forward mapping when rollback is nil", func(t *testing.T) {
		calls := make([]string, 0, 2)
		wantErr := errors.New("reverse failed")
		err := db.WriteBlockIDMappingDualWrite(
			func() error {
				calls = append(calls, "forward")
				return nil
			},
			nil,
			func() error {
				calls = append(calls, "reverse")
				return wantErr
			},
		)
		if !errors.Is(err, wantErr) {
			t.Fatalf("error = %v, want %v", err, wantErr)
		}
		want := []string{"forward", "reverse"}
		if len(calls) != len(want) {
			t.Fatalf("call count = %d, want %d (%v)", len(calls), len(want), calls)
		}
		for i := range want {
			if calls[i] != want[i] {
				t.Fatalf("calls[%d] = %q, want %q (full=%v)", i, calls[i], want[i], calls)
			}
		}
	})

	t.Run("joins rollback failure after reverse failure", func(t *testing.T) {
		wantReverseErr := errors.New("reverse failed")
		wantRollbackErr := errors.New("rollback failed")
		err := db.WriteBlockIDMappingDualWrite(
			func() error { return nil },
			func() error { return wantRollbackErr },
			func() error { return wantReverseErr },
		)
		if !errors.Is(err, wantReverseErr) {
			t.Fatalf("error = %v, want joined reverse error %v", err, wantReverseErr)
		}
		if !errors.Is(err, wantRollbackErr) {
			t.Fatalf("error = %v, want joined rollback error %v", err, wantRollbackErr)
		}
	})
}

// TestGetDocumentType tests document type detection
func TestGetDocumentType(t *testing.T) {
	tests := []struct {
		filename string
		expected string
	}{
		// Word documents
		{"document.doc", "word"},
		{"document.docx", "word"},
		{"document.odt", "word"},
		{"document.fodt", "word"},
		{"document.rtf", "word"},
		{"readme.txt", "word"},
		{"page.html", "word"},
		{"page.htm", "word"},
		{"book.epub", "word"},
		{"document.xps", "word"},
		{"scan.djvu", "word"},

		// Spreadsheets
		{"data.xls", "cell"},
		{"data.xlsx", "cell"},
		{"data.ods", "cell"},
		{"data.fods", "cell"},
		{"data.csv", "cell"},

		// Presentations
		{"slides.ppt", "slide"},
		{"slides.pptx", "slide"},
		{"slides.odp", "slide"},
		{"slides.fodp", "slide"},

		// PDF
		{"document.pdf", "pdf"},

		// Unknown extensions default to word
		{"image.png", "word"},
		{"video.mp4", "word"},
		{"archive.zip", "word"},
		{"noextension", "word"},

		// Case insensitivity (extension extracted and lowercased)
		{"DOCUMENT.DOCX", "word"},
		{"DATA.XLSX", "cell"},
		{"SLIDES.PPTX", "slide"},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			result := getDocumentType(tt.filename)
			if result != tt.expected {
				t.Errorf("getDocumentType(%q) = %q, want %q", tt.filename, result, tt.expected)
			}
		})
	}
}

// TestCanEditFile tests the edit permission checker
func TestCanEditFile(t *testing.T) {
	h := &OnlyOfficeHandler{
		config: &config.Config{
			OnlyOffice: config.OnlyOfficeConfig{
				EditExtensions: []string{"docx", "xlsx", "pptx"},
			},
		},
	}

	tests := []struct {
		filename string
		expected bool
	}{
		{"document.docx", true},
		{"spreadsheet.xlsx", true},
		{"presentation.pptx", true},
		{"document.doc", false}, // Old format - view only
		{"document.odt", false}, // ODF - view only
		{"document.pdf", false}, // PDF - view only
		{"image.png", false},
		{"DOCUMENT.DOCX", true}, // Case insensitive
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			result := h.canEditFile(tt.filename)
			if result != tt.expected {
				t.Errorf("canEditFile(%q) = %v, want %v", tt.filename, result, tt.expected)
			}
		})
	}
}

// TestCanViewFile tests the view permission checker
func TestCanViewFile(t *testing.T) {
	h := &OnlyOfficeHandler{
		config: &config.Config{
			OnlyOffice: config.OnlyOfficeConfig{
				ViewExtensions: []string{"doc", "docx", "xls", "xlsx", "ppt", "pptx", "pdf", "odt"},
			},
		},
	}

	tests := []struct {
		filename string
		expected bool
	}{
		{"document.docx", true},
		{"document.doc", true},
		{"spreadsheet.xlsx", true},
		{"spreadsheet.xls", true},
		{"presentation.pptx", true},
		{"presentation.ppt", true},
		{"document.pdf", true},
		{"document.odt", true},
		{"image.png", false},
		{"video.mp4", false},
		{"archive.zip", false},
		{"DOCUMENT.DOCX", true}, // Case insensitive
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			result := h.canViewFile(tt.filename)
			if result != tt.expected {
				t.Errorf("canViewFile(%q) = %v, want %v", tt.filename, result, tt.expected)
			}
		})
	}
}

// TestSignJWT tests JWT token signing
func TestSignJWT(t *testing.T) {
	t.Run("with secret", func(t *testing.T) {
		h := &OnlyOfficeHandler{
			config: &config.Config{
				OnlyOffice: config.OnlyOfficeConfig{
					JWTSecret: "test-secret-key-12345",
				},
			},
		}

		payload := OnlyOfficeConfig{
			Document: OnlyOfficeDocument{
				FileType: "docx",
				Key:      "abc123",
				Title:    "test.docx",
				URL:      "http://example.com/test.docx",
			},
			DocumentType: "word",
		}

		token, err := h.signJWT(payload)
		if err != nil {
			t.Fatalf("signJWT() error = %v", err)
		}

		// JWT should have 3 parts separated by dots
		parts := strings.Split(token, ".")
		if len(parts) != 3 {
			t.Errorf("JWT should have 3 parts, got %d", len(parts))
		}

		// Token should not be empty
		if token == "" {
			t.Error("signJWT() returned empty token")
		}
	})

	t.Run("without secret", func(t *testing.T) {
		h := &OnlyOfficeHandler{
			config: &config.Config{
				OnlyOffice: config.OnlyOfficeConfig{
					JWTSecret: "", // Empty secret
				},
			},
		}

		payload := map[string]string{"test": "data"}
		token, err := h.signJWT(payload)

		if err == nil {
			t.Fatal("signJWT() accepted an empty JWT secret")
		}
		if token != "" {
			t.Errorf("signJWT() with empty secret should return empty token, got %q", token)
		}
	})

	t.Run("deterministic", func(t *testing.T) {
		h := &OnlyOfficeHandler{
			config: &config.Config{
				OnlyOffice: config.OnlyOfficeConfig{
					JWTSecret: "fixed-secret",
				},
			},
		}

		payload := map[string]string{"key": "value"}

		token1, _ := h.signJWT(payload)
		token2, _ := h.signJWT(payload)

		// Same payload + same secret = same token (header.payload will be same, signature will be same)
		if token1 != token2 {
			t.Error("signJWT() should be deterministic for same inputs")
		}
	})
}

func TestVerifyCallbackJWT(t *testing.T) {
	newToken := func(t *testing.T, secret string, payload map[string]any) string {
		t.Helper()
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"payload": payload})
		signed, err := token.SignedString([]byte(secret))
		if err != nil {
			t.Fatalf("SignedString() error = %v", err)
		}
		return signed
	}

	t.Run("accepts token from body", func(t *testing.T) {
		secret := "test-secret-key-12345"
		h := &OnlyOfficeHandler{config: &config.Config{OnlyOffice: config.OnlyOfficeConfig{JWTSecret: secret}}}
		payload := map[string]any{
			"status": 2,
			"key":    "doc-key-123",
			"url":    "http://onlyoffice/files/doc.docx",
			"users":  []string{"user-1"},
		}
		body, err := json.Marshal(map[string]string{"token": newToken(t, secret, payload)})
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}

		req, err := h.verifyCallbackJWT(body, "")
		if err != nil {
			t.Fatalf("verifyCallbackJWT() error = %v", err)
		}
		if req.Key != "doc-key-123" {
			t.Fatalf("Key = %q, want %q", req.Key, "doc-key-123")
		}
		if req.URL != "http://onlyoffice/files/doc.docx" {
			t.Fatalf("URL = %q, want callback URL", req.URL)
		}
	})

	t.Run("accepts token from authorization header", func(t *testing.T) {
		secret := "test-secret-key-12345"
		h := &OnlyOfficeHandler{config: &config.Config{OnlyOffice: config.OnlyOfficeConfig{JWTSecret: secret}}}
		payload := map[string]any{
			"status": 6,
			"key":    "doc-key-456",
			"url":    "http://onlyoffice/files/doc.docx",
		}
		body := []byte(`{"status":6}`)

		req, err := h.verifyCallbackJWT(body, "Bearer "+newToken(t, secret, payload))
		if err != nil {
			t.Fatalf("verifyCallbackJWT() error = %v", err)
		}
		if req.Key != "doc-key-456" {
			t.Fatalf("Key = %q, want %q", req.Key, "doc-key-456")
		}
	})

	t.Run("rejects missing token when secret is configured", func(t *testing.T) {
		h := &OnlyOfficeHandler{config: &config.Config{OnlyOffice: config.OnlyOfficeConfig{JWTSecret: "test-secret-key-12345"}}}
		if _, err := h.verifyCallbackJWT([]byte(`{"status":2}`), ""); err == nil {
			t.Fatal("verifyCallbackJWT() accepted a callback without token")
		}
	})

	t.Run("rejects plain body when jwt secret is missing", func(t *testing.T) {
		h := &OnlyOfficeHandler{config: &config.Config{OnlyOffice: config.OnlyOfficeConfig{}}}
		if _, err := h.verifyCallbackJWT([]byte(`{"status":2,"key":"doc-key-789","url":"http://onlyoffice/files/doc.docx"}`), ""); err == nil {
			t.Fatal("verifyCallbackJWT() accepted a callback without configured JWT secret")
		}
	})
}

func TestReadOnlyOfficeDocument(t *testing.T) {
	t.Run("reads document within limit", func(t *testing.T) {
		content, err := readOnlyOfficeDocument(strings.NewReader("12345"), 5)
		if err != nil {
			t.Fatalf("readOnlyOfficeDocument() error = %v", err)
		}
		if got := string(content); got != "12345" {
			t.Fatalf("content = %q, want %q", got, "12345")
		}
	})

	t.Run("rejects document larger than limit", func(t *testing.T) {
		content, err := readOnlyOfficeDocument(strings.NewReader("123456"), 5)
		if err == nil {
			t.Fatal("readOnlyOfficeDocument() accepted oversized content")
		}
		if content != nil {
			t.Fatalf("content = %q, want nil", string(content))
		}
	})
}

func TestDefaultOnlyOfficeMaxDocumentBytes(t *testing.T) {
	cfg := config.DefaultConfig()
	if cfg.OnlyOffice.MaxDocumentBytes != 500*1024*1024 {
		t.Fatalf("OnlyOffice.MaxDocumentBytes = %d, want %d", cfg.OnlyOffice.MaxDocumentBytes, int64(500*1024*1024))
	}
}

func TestOnlyOfficeStorageDeltaTreatsExistingEmptyFileAsReplacement(t *testing.T) {
	existing := &FSEntry{Name: "empty.docx", Size: 0}

	bytesDelta, filesDelta := onlyOfficeStorageDelta(existing, 128)
	if bytesDelta != 128 {
		t.Fatalf("bytesDelta = %d, want 128", bytesDelta)
	}
	if filesDelta != 0 {
		t.Fatalf("filesDelta = %d, want 0 for replacement of existing empty file", filesDelta)
	}

	bytesDelta, filesDelta = onlyOfficeStorageDelta(nil, 128)
	if bytesDelta != 128 {
		t.Fatalf("bytesDelta for new file = %d, want 128", bytesDelta)
	}
	if filesDelta != 1 {
		t.Fatalf("filesDelta for new file = %d, want 1", filesDelta)
	}
}

func TestShouldRollbackOnlyOfficeMaterializedBlock(t *testing.T) {
	tests := []struct {
		name                    string
		blockMetadataRegistered bool
		publishErr              error
		want                    bool
	}{
		{name: "registered block and publish failure", blockMetadataRegistered: true, publishErr: errors.New("boom"), want: true},
		{name: "registered block and no error", blockMetadataRegistered: true, want: false},
		{name: "unregistered block and publish failure", blockMetadataRegistered: false, publishErr: errors.New("boom"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldRollbackOnlyOfficeMaterializedBlock(tt.blockMetadataRegistered, tt.publishErr); got != tt.want {
				t.Fatalf("shouldRollbackOnlyOfficeMaterializedBlock() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEncryptOnlyOfficeContentUsesSeafileFormatWhenIVPresent(t *testing.T) {
	userID := "user-onlyoffice-seafile"
	repoID := "repo-onlyoffice-seafile"
	fileKey := make([]byte, crypto.FileKeySize)
	fileIV := make([]byte, crypto.IVSize)
	for i := range fileKey {
		fileKey[i] = byte(i + 1)
	}
	for i := range fileIV {
		fileIV[i] = byte(i + 2)
	}

	GetDecryptSessions().Unlock(userID, repoID, fileKey, fileIV)
	t.Cleanup(func() {
		GetDecryptSessions().Lock(userID, repoID)
	})

	encrypted, err := encryptOnlyOfficeContent(userID, repoID, []byte("onlyoffice content"))
	if err != nil {
		t.Fatalf("encryptOnlyOfficeContent returned error: %v", err)
	}

	decrypted, err := crypto.DecryptBlockSeafile(encrypted, fileKey, fileIV)
	if err != nil {
		t.Fatalf("DecryptBlockSeafile returned error: %v", err)
	}
	if !bytes.Equal(decrypted, []byte("onlyoffice content")) {
		t.Fatalf("decrypted = %q, want %q", decrypted, "onlyoffice content")
	}
}

func TestEncryptOnlyOfficeContentFallsBackWhenIVMissing(t *testing.T) {
	userID := "user-onlyoffice-legacy"
	repoID := "repo-onlyoffice-legacy"
	fileKey := make([]byte, crypto.FileKeySize)
	for i := range fileKey {
		fileKey[i] = byte(i + 3)
	}

	GetDecryptSessions().Unlock(userID, repoID, fileKey, nil)
	t.Cleanup(func() {
		GetDecryptSessions().Lock(userID, repoID)
	})

	encrypted, err := encryptOnlyOfficeContent(userID, repoID, []byte("legacy onlyoffice content"))
	if err != nil {
		t.Fatalf("encryptOnlyOfficeContent returned error: %v", err)
	}

	decrypted, err := crypto.DecryptBlock(encrypted, fileKey)
	if err != nil {
		t.Fatalf("DecryptBlock returned error: %v", err)
	}
	if !bytes.Equal(decrypted, []byte("legacy onlyoffice content")) {
		t.Fatalf("decrypted = %q, want %q", decrypted, "legacy onlyoffice content")
	}
}

func TestOnlyOfficeRollbackOperationKey(t *testing.T) {
	got := onlyOfficeRollbackOperationKey("  rollback-123  ")
	want := "onlyoffice-publish-failed:rollback-123"
	if got != want {
		t.Fatalf("onlyOfficeRollbackOperationKey() = %q, want %q", got, want)
	}
}

func TestOnlyOfficeCommitReachable(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		head    string
		parents map[string]string
		want    bool
		wantErr bool
	}{
		{
			name:   "head commit is reachable",
			target: "c3",
			head:   "c3",
			want:   true,
		},
		{
			name:   "ancestor commit is reachable",
			target: "c1",
			head:   "c3",
			parents: map[string]string{
				"c3": "c2",
				"c2": "c1",
				"c1": "",
			},
			want: true,
		},
		{
			name:   "missing commit is not reachable",
			target: "missing",
			head:   "c3",
			parents: map[string]string{
				"c3": "c2",
				"c2": "c1",
				"c1": "",
			},
			want: false,
		},
		{
			name:   "cycle returns error",
			target: "c1",
			head:   "c3",
			parents: map[string]string{
				"c3": "c2",
				"c2": "c3",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := onlyOfficeCommitReachable(tt.target, tt.head, func(commitID string) (string, error) {
				return tt.parents[commitID], nil
			})
			if tt.wantErr {
				if err == nil {
					t.Fatal("onlyOfficeCommitReachable() error = nil, want non-nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("onlyOfficeCommitReachable() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("onlyOfficeCommitReachable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestShouldTreatOnlyOfficeHeadLookupAsMissing(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "direct not found", err: gocql.ErrNotFound, want: true},
		{name: "wrapped not found", err: fmt.Errorf("head lookup: %w", gocql.ErrNotFound), want: true},
		{name: "transient error", err: errors.New("cassandra timeout"), want: false},
		{name: "nil error", err: nil, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldTreatOnlyOfficeHeadLookupAsMissing(tt.err); got != tt.want {
				t.Fatalf("shouldTreatOnlyOfficeHeadLookupAsMissing() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestGenerateFSID tests FS object ID generation
func TestGenerateFSID(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
	}{
		{"empty", []byte{}},
		{"simple", []byte("hello world")},
		{"binary", []byte{0x00, 0x01, 0x02, 0xFF, 0xFE}},
		{"large", make([]byte, 10000)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := generateFSID(tt.content)

			// SHA-1 produces 40 character hex string
			if len(result) != 40 {
				t.Errorf("generateFSID() length = %d, want 40", len(result))
			}

			// Should be hex string
			for _, c := range result {
				if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
					t.Errorf("generateFSID() contains non-hex char: %c", c)
				}
			}

			// Should be deterministic
			result2 := generateFSID(tt.content)
			if result != result2 {
				t.Errorf("generateFSID() not deterministic")
			}
		})
	}

	// Different content should produce different IDs
	id1 := generateFSID([]byte("content1"))
	id2 := generateFSID([]byte("content2"))
	if id1 == id2 {
		t.Error("Different content should produce different FS IDs")
	}
}

// TestGenerateDocKeyFormat tests that doc key matches expected format
func TestGenerateDocKeyFormat(t *testing.T) {
	// Known input should produce known output (for regression testing)
	key := generateDocKey("repo-id", "/path/to/file.docx", "file-id-123")

	// Verify it's a valid hex string of length 20
	if len(key) != 20 {
		t.Errorf("Expected length 20, got %d", len(key))
	}

	// The key should be stable - this is the MD5 of "repo-id/path/to/file.docxfile-id-123"
	// truncated to 20 chars
	// We don't hardcode the expected value to avoid brittleness, but we verify format
	for _, c := range key {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Errorf("Invalid hex character: %c", c)
		}
	}
}
