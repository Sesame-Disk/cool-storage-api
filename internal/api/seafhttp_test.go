package api

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// ============================================================================
// TokenManager Tests (pure Go, no external dependencies)
// ============================================================================

func TestNewTokenManager(t *testing.T) {
	tests := []struct {
		name        string
		ttl         time.Duration
		expectedTTL time.Duration
	}{
		{
			name:        "custom TTL",
			ttl:         30 * time.Minute,
			expectedTTL: 30 * time.Minute,
		},
		{
			name:        "zero TTL uses default",
			ttl:         0,
			expectedTTL: DefaultTokenTTL,
		},
		{
			name:        "negative TTL uses default",
			ttl:         -1 * time.Hour,
			expectedTTL: DefaultTokenTTL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tm := NewTokenManager(tt.ttl)
			if tm == nil {
				t.Fatal("NewTokenManager returned nil")
			}
			if tm.tokenTTL != tt.expectedTTL {
				t.Errorf("tokenTTL = %v, want %v", tm.tokenTTL, tt.expectedTTL)
			}
			if tm.tokens == nil {
				t.Error("tokens map should be initialized")
			}
		})
	}
}

func TestTokenManagerCreateToken(t *testing.T) {
	tm := NewTokenManager(1 * time.Hour)

	token, err := tm.CreateToken(TokenTypeUpload, "org1", "repo1", "/path", "user1", 1*time.Hour)
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	if token == nil {
		t.Fatal("token should not be nil")
	}
	if token.Token == "" {
		t.Error("token string should not be empty")
	}
	if len(token.Token) != 32 { // 16 bytes = 32 hex chars
		t.Errorf("token length = %d, want 32", len(token.Token))
	}
	if token.Type != TokenTypeUpload {
		t.Errorf("Type = %s, want %s", token.Type, TokenTypeUpload)
	}
	if token.OrgID != "org1" {
		t.Errorf("OrgID = %s, want org1", token.OrgID)
	}
	if token.RepoID != "repo1" {
		t.Errorf("RepoID = %s, want repo1", token.RepoID)
	}
	if token.Path != "/path" {
		t.Errorf("Path = %s, want /path", token.Path)
	}
	if token.UserID != "user1" {
		t.Errorf("UserID = %s, want user1", token.UserID)
	}
	if token.ExpiresAt.Before(time.Now()) {
		t.Error("ExpiresAt should be in the future")
	}
}

func TestTokenManagerCreateUploadToken(t *testing.T) {
	tm := NewTokenManager(1 * time.Hour)

	tokenStr, err := tm.CreateUploadToken("org1", "repo1", "/upload/path", "user1")
	if err != nil {
		t.Fatalf("CreateUploadToken failed: %v", err)
	}

	if tokenStr == "" {
		t.Error("token string should not be empty")
	}

	// Verify we can retrieve it
	token, ok := tm.GetToken(tokenStr, TokenTypeUpload)
	if !ok {
		t.Error("token should be retrievable")
	}
	if token.Type != TokenTypeUpload {
		t.Errorf("Type = %s, want %s", token.Type, TokenTypeUpload)
	}
}

func TestTokenManagerCreateDownloadToken(t *testing.T) {
	tm := NewTokenManager(1 * time.Hour)

	tokenStr, err := tm.CreateDownloadToken("org1", "repo1", "/file.txt", "user1")
	if err != nil {
		t.Fatalf("CreateDownloadToken failed: %v", err)
	}

	if tokenStr == "" {
		t.Error("token string should not be empty")
	}

	// Verify we can retrieve it
	token, ok := tm.GetToken(tokenStr, TokenTypeDownload)
	if !ok {
		t.Error("token should be retrievable")
	}
	if token.Type != TokenTypeDownload {
		t.Errorf("Type = %s, want %s", token.Type, TokenTypeDownload)
	}
}

func TestTokenManagerGetToken(t *testing.T) {
	tm := NewTokenManager(1 * time.Hour)

	// Create tokens
	uploadToken, _ := tm.CreateUploadToken("org1", "repo1", "/", "user1")
	downloadToken, _ := tm.CreateDownloadToken("org1", "repo1", "/file.txt", "user1")

	tests := []struct {
		name         string
		tokenStr     string
		expectedType TokenType
		wantOK       bool
	}{
		{
			name:         "valid upload token",
			tokenStr:     uploadToken,
			expectedType: TokenTypeUpload,
			wantOK:       true,
		},
		{
			name:         "valid download token",
			tokenStr:     downloadToken,
			expectedType: TokenTypeDownload,
			wantOK:       true,
		},
		{
			name:         "upload token with wrong type",
			tokenStr:     uploadToken,
			expectedType: TokenTypeDownload,
			wantOK:       false,
		},
		{
			name:         "download token with wrong type",
			tokenStr:     downloadToken,
			expectedType: TokenTypeUpload,
			wantOK:       false,
		},
		{
			name:         "non-existent token",
			tokenStr:     "nonexistent",
			expectedType: TokenTypeUpload,
			wantOK:       false,
		},
		{
			name:         "empty token",
			tokenStr:     "",
			expectedType: TokenTypeUpload,
			wantOK:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token, ok := tm.GetToken(tt.tokenStr, tt.expectedType)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if tt.wantOK && token == nil {
				t.Error("token should not be nil when ok is true")
			}
		})
	}
}

func TestTokenManagerGetTokenExpired(t *testing.T) {
	tm := NewTokenManager(1 * time.Hour)

	// Create a token with very short TTL
	token, _ := tm.CreateToken(TokenTypeUpload, "org1", "repo1", "/", "user1", 1*time.Millisecond)

	// Wait for it to expire
	time.Sleep(10 * time.Millisecond)

	// Should not be retrievable
	_, ok := tm.GetToken(token.Token, TokenTypeUpload)
	if ok {
		t.Error("expired token should not be retrievable")
	}
}

func TestTokenManagerDeleteToken(t *testing.T) {
	tm := NewTokenManager(1 * time.Hour)

	tokenStr, _ := tm.CreateUploadToken("org1", "repo1", "/", "user1")

	// Verify token exists
	_, ok := tm.GetToken(tokenStr, TokenTypeUpload)
	if !ok {
		t.Fatal("token should exist before deletion")
	}

	// Delete token
	err := tm.DeleteToken(tokenStr)
	if err != nil {
		t.Fatalf("DeleteToken failed: %v", err)
	}

	// Verify token is gone
	_, ok = tm.GetToken(tokenStr, TokenTypeUpload)
	if ok {
		t.Error("token should not exist after deletion")
	}
}

func TestTokenManagerDeleteNonExistent(t *testing.T) {
	tm := NewTokenManager(1 * time.Hour)

	// Deleting non-existent token should not error
	err := tm.DeleteToken("nonexistent")
	if err != nil {
		t.Errorf("DeleteToken should not error for non-existent token: %v", err)
	}
}

func TestTokenManagerTokenUniqueness(t *testing.T) {
	tm := NewTokenManager(1 * time.Hour)

	tokens := make(map[string]bool)
	for i := 0; i < 100; i++ {
		tokenStr, err := tm.CreateUploadToken("org", "repo", "/", "user")
		if err != nil {
			t.Fatalf("CreateUploadToken failed: %v", err)
		}
		if tokens[tokenStr] {
			t.Errorf("duplicate token generated: %s", tokenStr)
		}
		tokens[tokenStr] = true
	}
}

func TestTokenManagerImplementsInterface(t *testing.T) {
	// Compile-time check that TokenManager implements TokenStore
	var _ TokenStore = (*TokenManager)(nil)
}

func TestTokenTypeConstants(t *testing.T) {
	if TokenTypeUpload != "upload" {
		t.Errorf("TokenTypeUpload = %s, want upload", TokenTypeUpload)
	}
	if TokenTypeDownload != "download" {
		t.Errorf("TokenTypeDownload = %s, want download", TokenTypeDownload)
	}
}

func TestDefaultTokenTTL(t *testing.T) {
	if DefaultTokenTTL != 1*time.Hour {
		t.Errorf("DefaultTokenTTL = %v, want 1h", DefaultTokenTTL)
	}
}

// ============================================================================
// AccessToken struct tests
// ============================================================================

func TestAccessTokenFields(t *testing.T) {
	now := time.Now()
	token := AccessToken{
		Token:     "abc123",
		Type:      TokenTypeUpload,
		OrgID:     "org-1",
		RepoID:    "repo-1",
		Path:      "/documents/file.txt",
		UserID:    "user-1",
		ExpiresAt: now.Add(1 * time.Hour),
		CreatedAt: now,
	}

	if token.Token != "abc123" {
		t.Errorf("Token = %s, want abc123", token.Token)
	}
	if token.Type != TokenTypeUpload {
		t.Errorf("Type = %s, want upload", token.Type)
	}
	if token.Path != "/documents/file.txt" {
		t.Errorf("Path = %s, want /documents/file.txt", token.Path)
	}
}

// ============================================================================
// SeafHTTPHandler tests
// ============================================================================

// MockTokenStore implements TokenStore for testing
type MockTokenStore struct {
	tokens map[string]*AccessToken
}

func NewMockTokenStore() *MockTokenStore {
	return &MockTokenStore{
		tokens: make(map[string]*AccessToken),
	}
}

func (m *MockTokenStore) CreateUploadToken(orgID, repoID, path, userID string) (string, error) {
	token := &AccessToken{
		Token:     "mock-upload-token",
		Type:      TokenTypeUpload,
		OrgID:     orgID,
		RepoID:    repoID,
		Path:      path,
		UserID:    userID,
		ExpiresAt: time.Now().Add(1 * time.Hour),
		CreatedAt: time.Now(),
	}
	m.tokens[token.Token] = token
	return token.Token, nil
}

func (m *MockTokenStore) CreateDownloadToken(orgID, repoID, path, userID string) (string, error) {
	token := &AccessToken{
		Token:     "mock-download-token",
		Type:      TokenTypeDownload,
		OrgID:     orgID,
		RepoID:    repoID,
		Path:      path,
		UserID:    userID,
		ExpiresAt: time.Now().Add(1 * time.Hour),
		CreatedAt: time.Now(),
	}
	m.tokens[token.Token] = token
	return token.Token, nil
}

func (m *MockTokenStore) GetToken(tokenStr string, expectedType TokenType) (*AccessToken, bool) {
	token, ok := m.tokens[tokenStr]
	if !ok || token.Type != expectedType {
		return nil, false
	}
	return token, true
}

func (m *MockTokenStore) DeleteToken(tokenStr string) error {
	delete(m.tokens, tokenStr)
	return nil
}

func TestNewSeafHTTPHandler(t *testing.T) {
	tokenStore := NewMockTokenStore()
	handler := NewSeafHTTPHandler(nil, nil, nil, tokenStore)

	if handler == nil {
		t.Fatal("NewSeafHTTPHandler returned nil")
	}
	if handler.tokenStore == nil {
		t.Error("tokenStore should be set")
	}
}

func TestSeafHTTPHandlerUploadNoStorage(t *testing.T) {
	tokenStore := NewMockTokenStore()
	// Add a valid upload token
	tokenStore.CreateUploadToken("org1", "repo1", "/", "user1")

	handler := NewSeafHTTPHandler(nil, nil, nil, tokenStore) // nil storage

	r := gin.New()
	r.POST("/seafhttp/upload-api/:token", handler.HandleUpload)

	// Create multipart form
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, _ := writer.CreateFormFile("file", "test.txt")
	part.Write([]byte("test content"))
	writer.Close()

	req, _ := http.NewRequest("POST", "/seafhttp/upload-api/mock-upload-token", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Should fail because storage is nil
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestSeafHTTPHandlerUploadInvalidToken(t *testing.T) {
	tokenStore := NewMockTokenStore()
	handler := NewSeafHTTPHandler(nil, nil, nil, tokenStore)

	r := gin.New()
	r.POST("/seafhttp/upload-api/:token", handler.HandleUpload)

	// Create multipart form
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, _ := writer.CreateFormFile("file", "test.txt")
	part.Write([]byte("test content"))
	writer.Close()

	req, _ := http.NewRequest("POST", "/seafhttp/upload-api/invalid-token", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestSeafHTTPHandlerUploadNoFile(t *testing.T) {
	tokenStore := NewMockTokenStore()
	tokenStore.CreateUploadToken("org1", "repo1", "/", "user1")
	handler := NewSeafHTTPHandler(nil, nil, nil, tokenStore)

	r := gin.New()
	r.POST("/seafhttp/upload-api/:token", handler.HandleUpload)

	// Request without file - but storage is nil, so returns 503 first
	req, _ := http.NewRequest("POST", "/seafhttp/upload-api/mock-upload-token", nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Storage check happens before file check, so we get 503
	// Testing "no file" scenario requires integration testing with real storage
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestSeafHTTPHandlerDownloadInvalidToken(t *testing.T) {
	tokenStore := NewMockTokenStore()
	handler := NewSeafHTTPHandler(nil, nil, nil, tokenStore)

	r := gin.New()
	r.GET("/seafhttp/files/:token/*filepath", handler.HandleDownload)

	req, _ := http.NewRequest("GET", "/seafhttp/files/invalid-token/file.txt", nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestSeafHTTPHandlerDownloadNoStorage(t *testing.T) {
	tokenStore := NewMockTokenStore()
	tokenStore.CreateDownloadToken("org1", "repo1", "/file.txt", "user1")
	handler := NewSeafHTTPHandler(nil, nil, nil, tokenStore) // nil storage

	r := gin.New()
	r.GET("/seafhttp/files/:token/*filepath", handler.HandleDownload)

	req, _ := http.NewRequest("GET", "/seafhttp/files/mock-download-token/file.txt", nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestGenerateFileID(t *testing.T) {
	id1 := generateFileID("key1")
	id2 := generateFileID("key2")

	// Should be 40 hex chars (20 bytes)
	if len(id1) != 40 {
		t.Errorf("id length = %d, want 40", len(id1))
	}

	// Should be unique (random)
	if id1 == id2 {
		t.Error("generateFileID should produce unique IDs")
	}
}

func TestBytesReader(t *testing.T) {
	data := []byte("hello world")
	reader := newBytesReader(data)

	// Read in parts
	buf := make([]byte, 5)
	n, err := reader.Read(buf)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if n != 5 {
		t.Errorf("n = %d, want 5", n)
	}
	if string(buf) != "hello" {
		t.Errorf("buf = %q, want hello", buf)
	}

	// Read rest
	buf = make([]byte, 10)
	n, err = reader.Read(buf)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if n != 6 {
		t.Errorf("n = %d, want 6", n)
	}
	if string(buf[:n]) != " world" {
		t.Errorf("buf = %q, want ' world'", buf[:n])
	}

	// Read at EOF
	n, err = reader.Read(buf)
	if err != io.EOF {
		t.Errorf("err = %v, want EOF", err)
	}
	if n != 0 {
		t.Errorf("n = %d, want 0", n)
	}
}

func TestBytesReaderEmpty(t *testing.T) {
	reader := newBytesReader([]byte{})
	buf := make([]byte, 10)

	n, err := reader.Read(buf)
	if err != io.EOF {
		t.Errorf("err = %v, want EOF", err)
	}
	if n != 0 {
		t.Errorf("n = %d, want 0", n)
	}
}

func TestRegisterSeafHTTPRoutes(t *testing.T) {
	tokenStore := NewMockTokenStore()
	handler := NewSeafHTTPHandler(nil, nil, nil, tokenStore)

	r := gin.New()
	handler.RegisterSeafHTTPRoutes(r)

	// Test that routes are registered by checking they don't 404
	tests := []struct {
		method string
		path   string
	}{
		{"POST", "/seafhttp/upload-api/test-token"},
		{"GET", "/seafhttp/files/test-token/file.txt"},
	}

	for _, tt := range tests {
		req, _ := http.NewRequest(tt.method, tt.path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		// Should not be 404 (route exists, just may fail auth)
		if w.Code == http.StatusNotFound {
			t.Errorf("%s %s returned 404, route not registered", tt.method, tt.path)
		}
	}
}
