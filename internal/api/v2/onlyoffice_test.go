package v2

import (
	"strings"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/config"
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
		{"document.doc", false},  // Old format - view only
		{"document.odt", false},  // ODF - view only
		{"document.pdf", false},  // PDF - view only
		{"image.png", false},
		{"DOCUMENT.DOCX", true},  // Case insensitive
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
		{"DOCUMENT.DOCX", true},  // Case insensitive
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

		if err != nil {
			t.Fatalf("signJWT() error = %v", err)
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
