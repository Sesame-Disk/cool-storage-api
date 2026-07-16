package storage

import (
	"io"
	"testing"
)

// =============================================================================
// BlockStore Tests (pure Go, no external dependencies)
// =============================================================================

// TestNewBlockStore tests BlockStore creation
func TestNewBlockStore(t *testing.T) {
	tests := []struct {
		name           string
		prefix         string
		expectedPrefix string
	}{
		{
			name:           "empty prefix uses default",
			prefix:         "",
			expectedPrefix: "blocks/",
		},
		{
			name:           "prefix without trailing slash",
			prefix:         "data",
			expectedPrefix: "data/",
		},
		{
			name:           "prefix with trailing slash",
			prefix:         "data/",
			expectedPrefix: "data/",
		},
		{
			name:           "nested prefix",
			prefix:         "org/blocks",
			expectedPrefix: "org/blocks/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// We pass nil for s3Store since we're only testing prefix handling
			bs := NewBlockStore(nil, tt.prefix)
			if bs == nil {
				t.Fatal("NewBlockStore returned nil")
			}
			if bs.prefix != tt.expectedPrefix {
				t.Errorf("prefix = %q, want %q", bs.prefix, tt.expectedPrefix)
			}
		})
	}
}

// TestBlockStoreHashToKey tests the hash to S3 key conversion
func TestBlockStoreHashToKey(t *testing.T) {
	bs := NewBlockStore(nil, "blocks/")

	tests := []struct {
		name     string
		hash     string
		expected string
	}{
		{
			name:     "SHA-256 hash (64 chars)",
			hash:     "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			expected: "blocks/e3/b0/e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
		{
			name:     "SHA-1 hash (40 chars)",
			hash:     "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
			expected: "blocks/a1/b2/a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		},
		{
			name:     "short hash (less than 4 chars)",
			hash:     "abc",
			expected: "blocks/abc",
		},
		{
			name:     "exactly 4 chars",
			hash:     "abcd",
			expected: "blocks/ab/cd/abcd",
		},
		{
			name:     "5 chars",
			hash:     "abcde",
			expected: "blocks/ab/cd/abcde",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := bs.hashToKey(tt.hash)
			if result != tt.expected {
				t.Errorf("hashToKey(%q) = %q, want %q", tt.hash, result, tt.expected)
			}
		})
	}
}

// TestBlockStoreHashToKeyWithCustomPrefix tests hashToKey with different prefixes
func TestBlockStoreHashToKeyWithCustomPrefix(t *testing.T) {
	hash := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

	tests := []struct {
		prefix   string
		expected string
	}{
		{
			prefix:   "org-123/blocks/",
			expected: "org-123/blocks/e3/b0/e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
		{
			prefix:   "data/",
			expected: "data/e3/b0/e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
	}

	for _, tt := range tests {
		t.Run(tt.prefix, func(t *testing.T) {
			bs := NewBlockStore(nil, tt.prefix)
			result := bs.hashToKey(hash)
			if result != tt.expected {
				t.Errorf("hashToKey with prefix %q = %q, want %q", tt.prefix, result, tt.expected)
			}
		})
	}
}

// TestBlockInfoStruct tests the BlockInfo struct
func TestBlockInfoStruct(t *testing.T) {
	info := BlockInfo{
		Hash:         "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		Size:         1024,
		StorageClass: "hot",
		Exists:       true,
	}

	if info.Hash != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Errorf("Hash mismatch")
	}
	if info.Size != 1024 {
		t.Errorf("Size = %d, want 1024", info.Size)
	}
	if info.StorageClass != "hot" {
		t.Errorf("StorageClass = %s, want hot", info.StorageClass)
	}
	if !info.Exists {
		t.Error("Exists should be true")
	}
}

// TestBlockDataStruct tests the BlockData struct
func TestBlockDataStruct(t *testing.T) {
	data := BlockData{
		Hash: "abc123",
		Data: []byte("hello world"),
		Size: 11,
	}

	if data.Hash != "abc123" {
		t.Errorf("Hash = %s, want abc123", data.Hash)
	}
	if string(data.Data) != "hello world" {
		t.Errorf("Data = %q, want hello world", data.Data)
	}
	if data.Size != 11 {
		t.Errorf("Size = %d, want 11", data.Size)
	}
}

// TestBlockStatsStruct tests the BlockStats struct
func TestBlockStatsStruct(t *testing.T) {
	stats := BlockStats{
		TotalBlocks:     1000,
		TotalSize:       1024 * 1024 * 100, // 100 MB
		UniqueBlocks:    800,
		DeduplicatedPct: 20.0,
	}

	if stats.TotalBlocks != 1000 {
		t.Errorf("TotalBlocks = %d, want 1000", stats.TotalBlocks)
	}
	if stats.UniqueBlocks != 800 {
		t.Errorf("UniqueBlocks = %d, want 800", stats.UniqueBlocks)
	}
	if stats.DeduplicatedPct != 20.0 {
		t.Errorf("DeduplicatedPct = %f, want 20.0", stats.DeduplicatedPct)
	}
}

// TestBytesReader tests the bytesReader implementation
func TestBytesReader(t *testing.T) {
	data := []byte("hello world")
	reader := &bytesReader{data: data}

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

// TestBytesReaderEmpty tests bytesReader with empty data
func TestBytesReaderEmpty(t *testing.T) {
	reader := &bytesReader{data: []byte{}}
	buf := make([]byte, 10)

	n, err := reader.Read(buf)
	if err != io.EOF {
		t.Errorf("err = %v, want EOF", err)
	}
	if n != 0 {
		t.Errorf("n = %d, want 0", n)
	}
}

// TestBytesReaderLargeRead tests reading more than available
func TestBytesReaderLargeRead(t *testing.T) {
	data := []byte("abc")
	reader := &bytesReader{data: data}

	buf := make([]byte, 100)
	n, err := reader.Read(buf)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if n != 3 {
		t.Errorf("n = %d, want 3", n)
	}
	if string(buf[:n]) != "abc" {
		t.Errorf("buf = %q, want abc", buf[:n])
	}
}

// =============================================================================
// Org-scoped block store (ISSUE-GC-CROSS-ORG-BLOCK-DELETE-01, PR-1)
// =============================================================================

const (
	testOrgA   = "3fa85f64-5717-4562-b3fc-2c963f66afa6"
	testOrgB   = "6b2f9c1e-7a3d-4c5b-8e1f-0a9b8c7d6e5f"
	testHash64 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

// TestNewOrgBlockStore_FailsClosedOnInvalidOrg proves the constructor rejects any
// org id that could produce a global or malformed key, instead of silently falling
// back. This is the structural guarantee behind the P10 fix.
func TestNewOrgBlockStore_FailsClosedOnInvalidOrg(t *testing.T) {
	invalid := []struct {
		name  string
		orgID string
	}{
		{"empty", ""},
		{"whitespace only", "   "},
		{"not a uuid", "not-an-org"},
		{"path traversal", "../../etc"},
		{"contains slash", "org/blocks"},
		{"truncated uuid", "3fa85f64-5717-4562-b3fc"},
	}

	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			bs, err := NewOrgBlockStore(nil, "blocks/", tt.orgID)
			if err == nil {
				t.Fatalf("NewOrgBlockStore(%q) = nil error, want fail-closed error", tt.orgID)
			}
			if bs != nil {
				t.Fatalf("NewOrgBlockStore(%q) returned a store; want nil on rejection", tt.orgID)
			}
		})
	}
}

// TestNewOrgBlockStore_NormalizesOrg confirms the org id is normalized to its
// canonical UUID form so the derived key is deterministic regardless of input
// casing/format.
func TestNewOrgBlockStore_NormalizesOrg(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"canonical", testOrgA, testOrgA},
		{"uppercase", "3FA85F64-5717-4562-B3FC-2C963F66AFA6", testOrgA},
		{"braced", "{3fa85f64-5717-4562-b3fc-2c963f66afa6}", testOrgA},
		{"platform org", "00000000-0000-0000-0000-000000000000", "00000000-0000-0000-0000-000000000000"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			bs, err := NewOrgBlockStore(nil, "blocks/", tt.input)
			if err != nil {
				t.Fatalf("NewOrgBlockStore(%q) error: %v", tt.input, err)
			}
			if bs.orgID != tt.want {
				t.Errorf("orgID = %q, want normalized %q", bs.orgID, tt.want)
			}
			want := "blocks/" + tt.want + "/e3/b0/" + testHash64
			if got := bs.hashToKey(testHash64); got != want {
				t.Errorf("hashToKey = %q, want %q", got, want)
			}
		})
	}
}

// TestOrgBlockStoreHashToKey_IsOrgScoped is the core of the fix: identical content
// in different orgs must map to DISTINCT physical keys, so one org's GC can never
// delete another org's object.
func TestOrgBlockStoreHashToKey_IsOrgScoped(t *testing.T) {
	a, err := NewOrgBlockStore(nil, "blocks/", testOrgA)
	if err != nil {
		t.Fatalf("org A: %v", err)
	}
	b, err := NewOrgBlockStore(nil, "blocks/", testOrgB)
	if err != nil {
		t.Fatalf("org B: %v", err)
	}

	keyA := a.hashToKey(testHash64)
	keyB := b.hashToKey(testHash64)

	wantA := "blocks/" + testOrgA + "/e3/b0/" + testHash64
	if keyA != wantA {
		t.Errorf("org A key = %q, want %q", keyA, wantA)
	}
	if keyA == keyB {
		t.Fatalf("identical content produced the SAME key for two orgs (%q) — cross-org delete hazard", keyA)
	}
	// StorageKeyForHash (the public locator) must agree with the internal key.
	if a.StorageKeyForHash(testHash64) != keyA {
		t.Errorf("StorageKeyForHash disagrees with hashToKey for org A")
	}
}

// TestNewBlockStore_LegacyKeyUnchanged pins the legacy global layout so PR-1 is a
// pure addition with no behavior change for existing (not-yet-migrated) callers.
func TestNewBlockStore_LegacyKeyUnchanged(t *testing.T) {
	bs := NewBlockStore(nil, "blocks/")
	if bs.orgID != "" {
		t.Fatalf("legacy NewBlockStore has orgID %q, want empty", bs.orgID)
	}
	want := "blocks/e3/b0/" + testHash64
	if got := bs.hashToKey(testHash64); got != want {
		t.Errorf("legacy hashToKey = %q, want %q", got, want)
	}
}

// TestHashSharding tests that hash sharding produces expected structure
func TestHashSharding(t *testing.T) {
	bs := NewBlockStore(nil, "blocks/")

	// Test that similar hashes are grouped together
	hash1 := "abcd1234567890"
	hash2 := "abcd9876543210"
	hash3 := "efgh1234567890"

	key1 := bs.hashToKey(hash1)
	key2 := bs.hashToKey(hash2)
	key3 := bs.hashToKey(hash3)

	// hash1 and hash2 should share the first two levels (ab/cd)
	if key1[:15] != key2[:15] { // "blocks/ab/cd/"
		t.Errorf("Hashes starting with 'abcd' should share prefix, got %s and %s", key1, key2)
	}

	// hash3 should have different first level (ef)
	if key1[:10] == key3[:10] { // "blocks/ab" vs "blocks/ef"
		t.Errorf("Hashes starting with 'abcd' and 'efgh' should have different first level")
	}
}
