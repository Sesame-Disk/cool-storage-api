package v2

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/crypto"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// TestSetPasswordRequest_Binding tests SetPasswordRequest binding
func TestSetPasswordRequest_Binding(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		wantPwd     string
	}{
		{
			name:        "form data",
			contentType: "application/x-www-form-urlencoded",
			body:        "password=TestPassword123",
			wantPwd:     "TestPassword123",
		},
		{
			name:        "json data",
			contentType: "application/json",
			body:        `{"password":"TestPassword123"}`,
			wantPwd:     "TestPassword123",
		},
		{
			name:        "empty password form",
			contentType: "application/x-www-form-urlencoded",
			body:        "password=",
			wantPwd:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req SetPasswordRequest

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tt.body))
			c.Request.Header.Set("Content-Type", tt.contentType)

			err := c.ShouldBind(&req)
			if err != nil {
				t.Logf("Binding error (may be expected): %v", err)
			}

			if req.Password != tt.wantPwd {
				t.Errorf("Password = %q, want %q", req.Password, tt.wantPwd)
			}
		})
	}
}

// TestChangePasswordRequest_Binding tests ChangePasswordRequest binding
func TestChangePasswordRequest_Binding(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		wantOld     string
		wantNew     string
	}{
		{
			name:        "form data",
			contentType: "application/x-www-form-urlencoded",
			body:        "old_password=OldPass123&new_password=NewPass456",
			wantOld:     "OldPass123",
			wantNew:     "NewPass456",
		},
		{
			name:        "json data",
			contentType: "application/json",
			body:        `{"old_password":"OldPass123","new_password":"NewPass456"}`,
			wantOld:     "OldPass123",
			wantNew:     "NewPass456",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req ChangePasswordRequest

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			c.Request = httptest.NewRequest(http.MethodPut, "/", strings.NewReader(tt.body))
			c.Request.Header.Set("Content-Type", tt.contentType)

			err := c.ShouldBind(&req)
			if err != nil {
				t.Fatalf("Binding failed: %v", err)
			}

			if req.OldPassword != tt.wantOld {
				t.Errorf("OldPassword = %q, want %q", req.OldPassword, tt.wantOld)
			}
			if req.NewPassword != tt.wantNew {
				t.Errorf("NewPassword = %q, want %q", req.NewPassword, tt.wantNew)
			}
		})
	}
}

// TestSetPassword_Validation tests input validation for SetPassword
func TestSetPassword_Validation(t *testing.T) {
	tests := []struct {
		name       string
		repoID     string
		password   string
		wantStatus int
		wantError  string
	}{
		{
			name:       "invalid repo_id",
			repoID:     "not-a-uuid",
			password:   "TestPassword",
			wantStatus: http.StatusBadRequest,
			wantError:  "invalid repo_id",
		},
		{
			name:       "empty password",
			repoID:     "543f7a13-7145-4d85-a768-8c91755cfb77",
			password:   "",
			wantStatus: http.StatusBadRequest,
			wantError:  "password is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create handler without database (will fail on DB access, but we're testing validation)
			h := &EncryptionHandler{db: nil}

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			// Set up request
			form := url.Values{}
			form.Set("password", tt.password)

			c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
			c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			c.Params = gin.Params{{Key: "repo_id", Value: tt.repoID}}
			c.Set("org_id", "00000000-0000-0000-0000-000000000001")

			h.SetPassword(c)

			if w.Code != tt.wantStatus {
				t.Errorf("Status = %d, want %d", w.Code, tt.wantStatus)
			}

			var resp map[string]interface{}
			json.Unmarshal(w.Body.Bytes(), &resp)
			if errMsg, ok := resp["error"].(string); ok {
				if !strings.Contains(errMsg, tt.wantError) {
					t.Errorf("Error = %q, want to contain %q", errMsg, tt.wantError)
				}
			}
		})
	}
}

// TestChangePassword_Validation tests input validation for ChangePassword
func TestChangePassword_Validation(t *testing.T) {
	tests := []struct {
		name        string
		repoID      string
		oldPassword string
		newPassword string
		wantStatus  int
		wantError   string
	}{
		{
			name:        "invalid repo_id",
			repoID:      "not-a-uuid",
			oldPassword: "OldPass",
			newPassword: "NewPass",
			wantStatus:  http.StatusBadRequest,
			wantError:   "invalid repo_id",
		},
		{
			name:        "empty old password",
			repoID:      "543f7a13-7145-4d85-a768-8c91755cfb77",
			oldPassword: "",
			newPassword: "NewPass",
			wantStatus:  http.StatusBadRequest,
			wantError:   "required",
		},
		{
			name:        "empty new password",
			repoID:      "543f7a13-7145-4d85-a768-8c91755cfb77",
			oldPassword: "OldPass",
			newPassword: "",
			wantStatus:  http.StatusBadRequest,
			wantError:   "required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &EncryptionHandler{db: nil}

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			form := url.Values{}
			form.Set("old_password", tt.oldPassword)
			form.Set("new_password", tt.newPassword)

			c.Request = httptest.NewRequest(http.MethodPut, "/", strings.NewReader(form.Encode()))
			c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			c.Params = gin.Params{{Key: "repo_id", Value: tt.repoID}}
			c.Set("org_id", "00000000-0000-0000-0000-000000000001")

			h.ChangePassword(c)

			if w.Code != tt.wantStatus {
				t.Errorf("Status = %d, want %d", w.Code, tt.wantStatus)
			}

			var resp map[string]interface{}
			json.Unmarshal(w.Body.Bytes(), &resp)
			if errMsg, ok := resp["error"].(string); ok {
				if !strings.Contains(errMsg, tt.wantError) {
					t.Errorf("Error = %q, want to contain %q", errMsg, tt.wantError)
				}
			}
		})
	}
}

func TestEncryptionHandler_RequireLibraryAccess_RepoToken(t *testing.T) {
	h := &EncryptionHandler{permMiddleware: middleware.NewPermissionMiddleware(nil)}

	tests := []struct {
		name       string
		tokenRepo  string
		tokenPerm  string
		required   middleware.LibraryPermission
		wantOK     bool
		wantStatus int
	}{
		{
			name:      "set-password allows read token",
			tokenRepo: "repo-1",
			tokenPerm: "r",
			required:  middleware.PermissionR,
			wantOK:    true,
		},
		{
			name:       "change-password rejects read token",
			tokenRepo:  "repo-1",
			tokenPerm:  "r",
			required:   middleware.PermissionRW,
			wantOK:     false,
			wantStatus: http.StatusForbidden,
		},
		{
			name:      "change-password allows write token",
			tokenRepo: "repo-1",
			tokenPerm: "rw",
			required:  middleware.PermissionRW,
			wantOK:    true,
		},
		{
			name:       "repo token cannot access other repo",
			tokenRepo:  "repo-2",
			tokenPerm:  "rw",
			required:   middleware.PermissionR,
			wantOK:     false,
			wantStatus: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Set("org_id", "org-1")
			c.Set("user_id", "user-1")
			c.Set("repo_api_token", true)
			c.Set("repo_api_token_repo_id", tt.tokenRepo)
			c.Set("repo_api_token_permission", tt.tokenPerm)

			ok := h.requireLibraryAccess(c, "repo-1", tt.required)
			if ok != tt.wantOK {
				t.Fatalf("requireLibraryAccess() = %v, want %v", ok, tt.wantOK)
			}
			if !tt.wantOK && w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestEncryptionHandler_RequireLibraryAccess_NilPermMiddleware(t *testing.T) {
	h := &EncryptionHandler{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	if !h.requireLibraryAccess(c, "repo-1", middleware.PermissionR) {
		t.Fatal("nil perm middleware should not block validation-only paths")
	}
}

// TestEncryptionParams_JSON tests JSON serialization of encryption params
func TestEncryptionParams_JSON(t *testing.T) {
	params := &crypto.EncryptionParams{
		EncVersion:      12,
		Salt:            "abcd1234",
		Magic:           "magic123",
		MagicStrong:     "strongmagic456",
		RandomKey:       "randomkey789",
		RandomKeyStrong: "strongrandom012",
	}

	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded crypto.EncryptionParams
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.EncVersion != params.EncVersion {
		t.Errorf("EncVersion = %d, want %d", decoded.EncVersion, params.EncVersion)
	}
	if decoded.Salt != params.Salt {
		t.Errorf("Salt = %q, want %q", decoded.Salt, params.Salt)
	}
	if decoded.Magic != params.Magic {
		t.Errorf("Magic = %q, want %q", decoded.Magic, params.Magic)
	}
}

// TestEncryptionResponseFormat tests the response format matches Seafile
func TestEncryptionResponseFormat(t *testing.T) {
	// Test success response
	successResp := gin.H{"success": true}
	data, _ := json.Marshal(successResp)
	if !bytes.Contains(data, []byte(`"success":true`)) {
		t.Error("Success response should contain success:true")
	}

	// Test error response (Seafile format)
	errorResp := gin.H{"error_msg": "Wrong password"}
	data, _ = json.Marshal(errorResp)
	if !bytes.Contains(data, []byte(`"error_msg":"Wrong password"`)) {
		t.Error("Error response should use error_msg field")
	}
}

// TestCreateLibraryRequest_EncryptedFields tests encrypted library creation request
func TestCreateLibraryRequest_EncryptedFields(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantEnc bool
		wantPwd string
	}{
		{
			name:    "non-encrypted",
			body:    "name=TestLib",
			wantEnc: false,
			wantPwd: "",
		},
		{
			name:    "encrypted with password",
			body:    "name=TestLib&encrypted=true&passwd=Secret123",
			wantEnc: true,
			wantPwd: "Secret123",
		},
		{
			name:    "encrypted=1 format",
			body:    "name=TestLib&encrypted=1&passwd=Secret123",
			wantEnc: true,
			wantPwd: "Secret123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tt.body))
			c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			// Parse like the handler does
			name := c.PostForm("name")
			password := c.PostForm("passwd")
			encrypted := c.PostForm("encrypted") == "true" || c.PostForm("encrypted") == "1"

			if name == "" {
				t.Error("Name should be parsed")
			}
			if encrypted != tt.wantEnc {
				t.Errorf("Encrypted = %v, want %v", encrypted, tt.wantEnc)
			}
			if password != tt.wantPwd {
				t.Errorf("Password = %q, want %q", password, tt.wantPwd)
			}
		})
	}
}

// TestEncryptionVersion tests that we use the correct encryption version
func TestEncryptionVersion(t *testing.T) {
	if crypto.EncVersionDual != 12 {
		t.Errorf("EncVersionDual = %d, want 12", crypto.EncVersionDual)
	}
	if crypto.EncVersionSesameFS != 10 {
		t.Errorf("EncVersionSesameFS = %d, want 10", crypto.EncVersionSesameFS)
	}
	if crypto.EncVersionSeafileV2 != 2 {
		t.Errorf("EncVersionSeafileV2 = %d, want 2", crypto.EncVersionSeafileV2)
	}
}

// TestPasswordMinLength tests password validation constants
func TestPasswordMinLength(t *testing.T) {
	// Seafile default minimum is 8 characters
	minLen := 8

	validPasswords := []string{
		"12345678",
		"Password123!",
		"abcdefghijklmnop",
	}

	invalidPasswords := []string{
		"1234567",
		"abc",
		"",
	}

	for _, pwd := range validPasswords {
		if len(pwd) < minLen {
			t.Errorf("Password %q should be valid (len=%d >= %d)", pwd, len(pwd), minLen)
		}
	}

	for _, pwd := range invalidPasswords {
		if len(pwd) >= minLen {
			t.Errorf("Password %q should be invalid (len=%d < %d)", pwd, len(pwd), minLen)
		}
	}
}

// ============================================================================
// DecryptSessionManager Tests (pure in-memory, no DB)
// ============================================================================

func newTestSessionManager(ttl time.Duration) *DecryptSessionManager {
	return &DecryptSessionManager{
		sessions: make(map[string]*DecryptSession),
		ttl:      ttl,
	}
}

func TestDecryptSessionManager_UnlockAndIsUnlocked(t *testing.T) {
	m := newTestSessionManager(1 * time.Hour)

	if m.IsUnlocked("user-1", "repo-1") {
		t.Error("new session manager should not have unlocked repos")
	}

	fileKey := []byte("0123456789abcdef0123456789abcdef")
	fileIV := []byte("0123456789abcdef")
	m.Unlock("user-1", "repo-1", fileKey, fileIV)

	if !m.IsUnlocked("user-1", "repo-1") {
		t.Error("repo should be unlocked after Unlock()")
	}

	// Different user/repo should not be unlocked
	if m.IsUnlocked("user-2", "repo-1") {
		t.Error("different user should not be unlocked")
	}
	if m.IsUnlocked("user-1", "repo-2") {
		t.Error("different repo should not be unlocked")
	}
}

func TestDecryptSessionManager_GetFileKey(t *testing.T) {
	m := newTestSessionManager(1 * time.Hour)

	// Not unlocked -> nil
	if m.GetFileKey("user-1", "repo-1") != nil {
		t.Error("GetFileKey should return nil for locked repo")
	}

	fileKey := []byte("0123456789abcdef0123456789abcdef")
	fileIV := []byte("0123456789abcdef")
	m.Unlock("user-1", "repo-1", fileKey, fileIV)

	got := m.GetFileKey("user-1", "repo-1")
	if got == nil {
		t.Fatal("GetFileKey should return key for unlocked repo")
	}
	if string(got) != string(fileKey) {
		t.Errorf("GetFileKey = %x, want %x", got, fileKey)
	}
}

func TestDecryptSessionManager_GetFileKeyAndIV(t *testing.T) {
	m := newTestSessionManager(1 * time.Hour)

	// Not unlocked -> nil, nil
	key, iv := m.GetFileKeyAndIV("user-1", "repo-1")
	if key != nil || iv != nil {
		t.Error("should return nil, nil for locked repo")
	}

	fileKey := []byte("0123456789abcdef0123456789abcdef")
	fileIV := []byte("0123456789abcdef")
	m.Unlock("user-1", "repo-1", fileKey, fileIV)

	key, iv = m.GetFileKeyAndIV("user-1", "repo-1")
	if key == nil || iv == nil {
		t.Fatal("should return key and IV for unlocked repo")
	}
	if string(key) != string(fileKey) {
		t.Errorf("key = %x, want %x", key, fileKey)
	}
	if string(iv) != string(fileIV) {
		t.Errorf("iv = %x, want %x", iv, fileIV)
	}
}

func TestDecryptSessionManager_Lock(t *testing.T) {
	m := newTestSessionManager(1 * time.Hour)

	fileKey := []byte("key")
	fileIV := []byte("iv")
	m.Unlock("user-1", "repo-1", fileKey, fileIV)

	if !m.IsUnlocked("user-1", "repo-1") {
		t.Fatal("should be unlocked before Lock()")
	}

	m.Lock("user-1", "repo-1")

	if m.IsUnlocked("user-1", "repo-1") {
		t.Error("should be locked after Lock()")
	}
	if m.GetFileKey("user-1", "repo-1") != nil {
		t.Error("GetFileKey should return nil after Lock()")
	}
}

func TestDecryptSessionManager_Expiry(t *testing.T) {
	m := newTestSessionManager(50 * time.Millisecond)

	m.Unlock("user-1", "repo-1", []byte("key"), []byte("iv"))

	if !m.IsUnlocked("user-1", "repo-1") {
		t.Fatal("should be unlocked immediately")
	}

	// Wait for expiry
	time.Sleep(100 * time.Millisecond)

	if m.IsUnlocked("user-1", "repo-1") {
		t.Error("should be expired after TTL")
	}
	if m.GetFileKey("user-1", "repo-1") != nil {
		t.Error("GetFileKey should return nil after TTL")
	}
	key, iv := m.GetFileKeyAndIV("user-1", "repo-1")
	if key != nil || iv != nil {
		t.Error("GetFileKeyAndIV should return nil after TTL")
	}
}

func TestDecryptSessionManager_Concurrent(t *testing.T) {
	m := newTestSessionManager(1 * time.Hour)
	var wg sync.WaitGroup

	// Concurrent unlock/check/lock
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			userID := "user-1"
			repoID := "repo-1"
			m.Unlock(userID, repoID, []byte("key"), []byte("iv"))
			m.IsUnlocked(userID, repoID)
			m.GetFileKey(userID, repoID)
			m.GetFileKeyAndIV(userID, repoID)
			if idx%3 == 0 {
				m.Lock(userID, repoID)
			}
		}(i)
	}

	wg.Wait()
}

func TestDecryptSessionManager_MultipleRepos(t *testing.T) {
	m := newTestSessionManager(1 * time.Hour)

	m.Unlock("user-1", "repo-1", []byte("key1"), []byte("iv1"))
	m.Unlock("user-1", "repo-2", []byte("key2"), []byte("iv2"))
	m.Unlock("user-2", "repo-1", []byte("key3"), []byte("iv3"))

	// Each should have its own key
	if string(m.GetFileKey("user-1", "repo-1")) != "key1" {
		t.Error("wrong key for user-1:repo-1")
	}
	if string(m.GetFileKey("user-1", "repo-2")) != "key2" {
		t.Error("wrong key for user-1:repo-2")
	}
	if string(m.GetFileKey("user-2", "repo-1")) != "key3" {
		t.Error("wrong key for user-2:repo-1")
	}

	// Locking one shouldn't affect others
	m.Lock("user-1", "repo-1")
	if m.IsUnlocked("user-1", "repo-1") {
		t.Error("user-1:repo-1 should be locked")
	}
	if !m.IsUnlocked("user-1", "repo-2") {
		t.Error("user-1:repo-2 should still be unlocked")
	}
	if !m.IsUnlocked("user-2", "repo-1") {
		t.Error("user-2:repo-1 should still be unlocked")
	}
}

func TestGetDecryptSessions(t *testing.T) {
	sessions := GetDecryptSessions()
	if sessions == nil {
		t.Fatal("GetDecryptSessions should return global instance")
	}
	if sessions.sessions == nil {
		t.Error("sessions map should be initialized")
	}
	if sessions.ttl != 1*time.Hour {
		t.Errorf("ttl = %v, want 1h", sessions.ttl)
	}
}

func TestNewEncryptionHandler(t *testing.T) {
	h := NewEncryptionHandler(nil)
	if h == nil {
		t.Fatal("NewEncryptionHandler should not return nil")
	}
	if h.db != nil {
		t.Error("db should be nil when created with nil")
	}
}

// ============================================================================
// LockAllForRepo / ChangePassword session invalidation
// ============================================================================

// TestDecryptSessionManager_LockAllForRepo_EvictsAllUsersForRepo confirms that
// calling LockAllForRepo invalidates sessions for all users on that repo while
// sessions for other repos remain active.
func TestDecryptSessionManager_LockAllForRepo_EvictsAllUsersForRepo(t *testing.T) {
	m := newTestSessionManager(time.Hour)

	key, iv := []byte("filekey"), []byte("fileiv_")

	// Two users unlocked on the same repo.
	m.Unlock("user-A", "repo-X", key, iv)
	m.Unlock("user-B", "repo-X", key, iv)
	// One user on a different repo — must survive.
	m.Unlock("user-A", "repo-Y", key, iv)

	evicted := m.LockAllForRepo("repo-X")
	if evicted != 2 {
		t.Errorf("LockAllForRepo evicted %d sessions, want 2", evicted)
	}
	if m.IsUnlocked("user-A", "repo-X") {
		t.Error("user-A:repo-X should be locked after LockAllForRepo")
	}
	if m.IsUnlocked("user-B", "repo-X") {
		t.Error("user-B:repo-X should be locked after LockAllForRepo")
	}
	if !m.IsUnlocked("user-A", "repo-Y") {
		t.Error("user-A:repo-Y should NOT be affected by LockAllForRepo(repo-X)")
	}
}

// TestDecryptSessionManager_LockAllForRepo_EmptyMap ensures no panic when the
// session map is empty.
func TestDecryptSessionManager_LockAllForRepo_EmptyMap(t *testing.T) {
	m := newTestSessionManager(time.Hour)
	evicted := m.LockAllForRepo("repo-Z")
	if evicted != 0 {
		t.Errorf("expected 0 evictions on empty map, got %d", evicted)
	}
}

// TestDecryptSessionManager_LockAllForRepo_NoMatchingRepo confirms that sessions
// for other repos are untouched even when the target repo has no sessions.
func TestDecryptSessionManager_LockAllForRepo_NoMatchingRepo(t *testing.T) {
	m := newTestSessionManager(time.Hour)
	m.Unlock("user-A", "repo-1", []byte("k"), []byte("v"))
	m.Unlock("user-B", "repo-2", []byte("k"), []byte("v"))

	evicted := m.LockAllForRepo("repo-nonexistent")
	if evicted != 0 {
		t.Errorf("expected 0 evictions, got %d", evicted)
	}
	if !m.IsUnlocked("user-A", "repo-1") {
		t.Error("unrelated sessions should remain unlocked")
	}
	if !m.IsUnlocked("user-B", "repo-2") {
		t.Error("unrelated sessions should remain unlocked")
	}
}

// TestDecryptSessionManager_LockAllForRepo_Concurrent verifies there is no data
// race when LockAllForRepo runs concurrently with Unlock/IsUnlocked.
func TestDecryptSessionManager_LockAllForRepo_Concurrent(t *testing.T) {
	m := newTestSessionManager(time.Hour)
	repoID := "repo-shared"
	key, iv := []byte("k"), []byte("v")

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines * 3)

	for i := 0; i < goroutines; i++ {
		userID := "user-" + string(rune('A'+i%26))
		go func(uid string) {
			defer wg.Done()
			m.Unlock(uid, repoID, key, iv)
		}(userID)
		go func(uid string) {
			defer wg.Done()
			m.IsUnlocked(uid, repoID)
		}(userID)
		go func() {
			defer wg.Done()
			m.LockAllForRepo(repoID)
		}()
	}
	wg.Wait()
	// Just verify no panic and the manager is still usable.
	m.Unlock("final", repoID, key, iv)
	if !m.IsUnlocked("final", repoID) {
		t.Error("manager should be functional after concurrent stress")
	}
}

func TestDecryptSessionManager_ResolverInvalidatesStaleRepoSession(t *testing.T) {
	m := newTestSessionManager(time.Hour)
	baseUpdatedAt := time.Now().Add(-time.Minute)
	m.SetUpdatedAtResolver(func(orgID, repoID string) (time.Time, error) {
		if orgID != "org-1" || repoID != "repo-1" {
			t.Fatalf("unexpected resolver lookup %s/%s", orgID, repoID)
		}
		return baseUpdatedAt.Add(time.Minute), nil
	})

	m.UnlockForLibrary("user-1", "org-1", "repo-1", baseUpdatedAt, []byte("key"), []byte("iv"))

	if m.IsUnlocked("user-1", "repo-1") {
		t.Fatal("session should be invalidated when library updated_at advances")
	}
	if m.GetFileKey("user-1", "repo-1") != nil {
		t.Fatal("stale session key should not be returned after invalidation")
	}
}

func TestDecryptSessionManager_ResolverKeepsCurrentRepoSession(t *testing.T) {
	m := newTestSessionManager(time.Hour)
	baseUpdatedAt := time.Now().Add(-time.Minute)
	m.SetUpdatedAtResolver(func(orgID, repoID string) (time.Time, error) {
		return baseUpdatedAt, nil
	})

	m.UnlockForLibrary("user-1", "org-1", "repo-1", baseUpdatedAt, []byte("key"), []byte("iv"))

	if !m.IsUnlocked("user-1", "repo-1") {
		t.Fatal("session should remain valid when library updated_at is unchanged")
	}
}

// TestDecryptSessionManager_ResolverTransientErrorKeepsSession verifies that a
// transient DB error (e.g. Cassandra timeout) does NOT revoke the session.
// The session should survive and the resolver should be retried later.
func TestDecryptSessionManager_ResolverTransientErrorKeepsSession(t *testing.T) {
	m := newTestSessionManager(time.Hour)
	baseUpdatedAt := time.Now().Add(-time.Minute)

	calls := 0
	m.SetUpdatedAtResolver(func(orgID, repoID string) (time.Time, error) {
		calls++
		return time.Time{}, errors.New("cassandra timeout")
	})

	m.UnlockForLibrary("user-1", "org-1", "repo-1", baseUpdatedAt, []byte("key"), []byte("iv"))

	if !m.IsUnlocked("user-1", "repo-1") {
		t.Fatal("transient resolver error should NOT revoke the session")
	}
	if fk := m.GetFileKey("user-1", "repo-1"); fk == nil {
		t.Fatal("file key should still be available after transient resolver error")
	}
	if calls != 1 {
		t.Fatalf("expected exactly 1 resolver call, got %d", calls)
	}
}

// TestDecryptSessionManager_ResolverRateLimited verifies that the resolver is
// not called on every access — only once per resolverCheckInterval.
func TestDecryptSessionManager_ResolverRateLimited(t *testing.T) {
	m := newTestSessionManager(time.Hour)
	baseUpdatedAt := time.Now().Add(-time.Minute)

	calls := 0
	m.SetUpdatedAtResolver(func(orgID, repoID string) (time.Time, error) {
		calls++
		return baseUpdatedAt, nil // unchanged
	})

	m.UnlockForLibrary("user-1", "org-1", "repo-1", baseUpdatedAt, []byte("key"), []byte("iv"))

	// Call IsUnlocked 50 times in rapid succession — should only trigger 1
	// resolver call since all calls are within resolverCheckInterval.
	for i := 0; i < 50; i++ {
		if !m.IsUnlocked("user-1", "repo-1") {
			t.Fatalf("session should remain valid on call %d", i)
		}
	}

	if calls != 1 {
		t.Errorf("resolver called %d times, want 1 (rate-limited)", calls)
	}
}

// TestDecryptSessionManager_ResolverSkippedForLegacyUnlock verifies that
// sessions created via the old Unlock() (no orgID/updatedAt) never trigger
// the resolver, preserving backward compatibility.
func TestDecryptSessionManager_ResolverSkippedForLegacyUnlock(t *testing.T) {
	m := newTestSessionManager(time.Hour)

	m.SetUpdatedAtResolver(func(orgID, repoID string) (time.Time, error) {
		t.Fatal("resolver should NOT be called for legacy sessions")
		return time.Time{}, nil
	})

	m.Unlock("user-1", "repo-1", []byte("key"), []byte("iv"))

	if !m.IsUnlocked("user-1", "repo-1") {
		t.Fatal("legacy session should remain valid")
	}
}
