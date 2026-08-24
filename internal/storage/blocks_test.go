package storage

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// =============================================================================
// BlockStore Tests (pure Go, no external dependencies)
// =============================================================================

func mustNewTestOrgBlockStore(t *testing.T, prefix string) *BlockStore {
	t.Helper()
	bs, err := NewOrgBlockStore(nil, prefix, testOrgA)
	if err != nil {
		t.Fatalf("NewOrgBlockStore() error: %v", err)
	}
	return bs
}

// TestNewOrgBlockStorePrefix tests BlockStore prefix normalization.
func TestNewOrgBlockStorePrefix(t *testing.T) {
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
			bs := mustNewTestOrgBlockStore(t, tt.prefix)
			if bs.prefix != tt.expectedPrefix {
				t.Errorf("prefix = %q, want %q", bs.prefix, tt.expectedPrefix)
			}
		})
	}
}

// TestBlockStoreHashToKey tests the hash to S3 key conversion
func TestBlockStoreHashToKey(t *testing.T) {
	bs := mustNewTestOrgBlockStore(t, "blocks/")
	orgPrefix := "blocks/" + testOrgA + "/"

	tests := []struct {
		name     string
		hash     string
		expected string
	}{
		{
			name:     "SHA-256 hash (64 chars)",
			hash:     "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			expected: orgPrefix + "e3/b0/e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
		{
			name:     "SHA-1 hash (40 chars)",
			hash:     "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
			expected: orgPrefix + "a1/b2/a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		},
		{
			name:     "short hash (less than 4 chars)",
			hash:     "abc",
			expected: orgPrefix + "abc",
		},
		{
			name:     "exactly 4 chars",
			hash:     "abcd",
			expected: orgPrefix + "ab/cd/abcd",
		},
		{
			name:     "5 chars",
			hash:     "abcde",
			expected: orgPrefix + "ab/cd/abcde",
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
			expected: "org-123/blocks/" + testOrgA + "/e3/b0/e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
		{
			prefix:   "data/",
			expected: "data/" + testOrgA + "/e3/b0/e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
	}

	for _, tt := range tests {
		t.Run(tt.prefix, func(t *testing.T) {
			bs := mustNewTestOrgBlockStore(t, tt.prefix)
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

type recordingBlockBackend struct {
	mu       sync.Mutex
	requests []string
}

func newRecordingBlockStore(t *testing.T, prefix string) (*BlockStore, *recordingBlockBackend) {
	t.Helper()
	backend := &recordingBlockBackend{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backend.mu.Lock()
		backend.requests = append(backend.requests, r.Method+" "+r.URL.Path)
		backend.mu.Unlock()
		w.Header().Set("Content-Length", "7")
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, "payload")
		}
	}))
	t.Cleanup(server.Close)

	s3Store, err := NewS3Store(context.Background(), S3Config{
		Endpoint:        server.URL,
		Bucket:          "test-bucket",
		Region:          "us-east-1",
		AccessKeyID:     "test",
		SecretAccessKey: "test",
		UsePathStyle:    true,
	})
	if err != nil {
		t.Fatalf("NewS3Store() error = %v", err)
	}
	blockStore, err := NewOrgBlockStore(s3Store, prefix, testOrgA)
	if err != nil {
		t.Fatalf("NewOrgBlockStore() error = %v", err)
	}
	return blockStore, backend
}

func (b *recordingBlockBackend) snapshot() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.requests...)
}

func TestBlockStoreExplicitStorageKeyOperations(t *testing.T) {
	blockStore, backend := newRecordingBlockStore(t, "blocks/")
	ctx := context.Background()
	legacyKey := blockStore.StorageKeyForHash(testHash64)
	mintedKey := legacyKey + ".8f14e45f-ea4d-4f73-9f7c-63f4e7a5bc21"
	var want []string
	for _, key := range []string{legacyKey, mintedKey} {
		if got, err := blockStore.PutObjectAutoDirect(ctx, key, []byte("payload")); err != nil || got != key {
			t.Fatalf("PutObjectAutoDirect(%q) = %q, %v", key, got, err)
		}
		data, err := blockStore.GetBlockByStorageKey(ctx, key)
		if err != nil || string(data) != "payload" {
			t.Fatalf("GetBlockByStorageKey(%q) = %q, %v", key, data, err)
		}
		reader, err := blockStore.GetBlockReaderByStorageKey(ctx, key)
		if err != nil {
			t.Fatalf("GetBlockReaderByStorageKey(%q) error = %v", key, err)
		}
		readerData, readErr := io.ReadAll(reader)
		_ = reader.Close()
		if readErr != nil || string(readerData) != "payload" {
			t.Fatalf("explicit reader for %q = %q, %v", key, readerData, readErr)
		}
		size, err := blockStore.GetBlockSizeByStorageKey(ctx, key)
		if err != nil || size != 7 {
			t.Fatalf("GetBlockSizeByStorageKey(%q) = %d, %v", key, size, err)
		}
		exists, err := blockStore.ObjectExists(ctx, key)
		if err != nil || !exists {
			t.Fatalf("ObjectExists(%q) = %t, %v", key, exists, err)
		}
		if err := blockStore.DeleteBlockByStorageKey(ctx, key); err != nil {
			t.Fatalf("DeleteBlockByStorageKey(%q) error = %v", key, err)
		}
		wantPath := "/test-bucket/" + key
		want = append(want, "PUT "+wantPath, "GET "+wantPath, "GET "+wantPath, "HEAD "+wantPath, "HEAD "+wantPath, "DELETE "+wantPath)
	}
	requests := backend.snapshot()
	if len(requests) != len(want) {
		t.Fatalf("requests = %v, want %v", requests, want)
	}
	for i := range want {
		if requests[i] != want[i] {
			t.Fatalf("requests = %v, want %v", requests, want)
		}
	}
}

func TestBlockStoreExplicitStorageKeyGuard(t *testing.T) {
	blockStore, backend := newRecordingBlockStore(t, "blocks/")
	ctx := context.Background()
	foreignKey := "blocks/" + testOrgB + "/e3/b0/" + testHash64
	suffixCollision := "blocks/" + testOrgA + "-other/e3/b0/" + testHash64

	operations := []struct {
		name string
		call func(string) error
	}{
		{"PutObjectAutoDirect", func(key string) error {
			_, err := blockStore.PutObjectAutoDirect(ctx, key, []byte("payload"))
			return err
		}},
		{"ObjectExists", func(key string) error { _, err := blockStore.ObjectExists(ctx, key); return err }},
		{"GetBlockByStorageKey", func(key string) error { _, err := blockStore.GetBlockByStorageKey(ctx, key); return err }},
		{"GetBlockReaderByStorageKey", func(key string) error { _, err := blockStore.GetBlockReaderByStorageKey(ctx, key); return err }},
		{"GetBlockSizeByStorageKey", func(key string) error { _, err := blockStore.GetBlockSizeByStorageKey(ctx, key); return err }},
		{"DeleteBlockByStorageKey", func(key string) error { return blockStore.DeleteBlockByStorageKey(ctx, key) }},
		{"ValidatePhysicalLocator", func(key string) error { return blockStore.ValidatePhysicalLocator(testHash64, key) }},
	}
	rejected := []struct {
		name string
		key  string
	}{
		{"empty", ""},
		{"whitespace", " \t\r\n"},
		{"foreign org", foreignKey},
		{"org suffix collision", suffixCollision},
		{"bare tenant prefix", "blocks/" + testOrgA + "/"},
	}

	for _, key := range rejected {
		for _, operation := range operations {
			t.Run(key.name+"/"+operation.name, func(t *testing.T) {
				if err := operation.call(key.key); err == nil {
					t.Fatalf("%s(%q) error = nil, want tenant guard rejection", operation.name, key.key)
				}
			})
		}
	}
	if requests := backend.snapshot(); len(requests) != 0 {
		t.Fatalf("rejected keys reached backend: %v", requests)
	}
}

func TestBlockStoreExplicitStorageKeyGuardCustomPrefix(t *testing.T) {
	blockStore, backend := newRecordingBlockStore(t, "custom/objects")
	customKey := blockStore.StorageKeyForHash(testHash64)
	rawKeyWithTrailingSpace := customKey + " "
	defaultKey := "blocks/" + testOrgA + "/e3/b0/" + testHash64

	if _, err := blockStore.ObjectExists(context.Background(), customKey); err != nil {
		t.Fatalf("ObjectExists(custom key) error = %v", err)
	}
	if _, err := blockStore.ObjectExists(context.Background(), rawKeyWithTrailingSpace); err != nil {
		t.Fatalf("ObjectExists(raw key with trailing space) error = %v", err)
	}
	if _, err := blockStore.ObjectExists(context.Background(), defaultKey); err == nil {
		t.Fatal("ObjectExists(default-prefix key) error = nil, want rejection")
	}
	want := []string{"HEAD /test-bucket/" + customKey, "HEAD /test-bucket/" + rawKeyWithTrailingSpace}
	if got := backend.snapshot(); len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("requests = %v, want %v", got, want)
	}
}

func TestBlockStoreMintStorageKey(t *testing.T) {
	blockStore := mustNewTestOrgBlockStore(t, "custom/objects")
	base := "custom/objects/" + testOrgA + "/e3/b0/" + testHash64
	first, err := blockStore.MintStorageKey(testHash64)
	if err != nil {
		t.Fatalf("MintStorageKey(first) error = %v", err)
	}
	second, err := blockStore.MintStorageKey(testHash64)
	if err != nil {
		t.Fatalf("MintStorageKey(second) error = %v", err)
	}
	if first == second {
		t.Fatalf("two mints returned the same key %q", first)
	}
	for _, key := range []string{first, second} {
		if !strings.HasPrefix(key, base+".") {
			t.Fatalf("minted key = %q, want layout %q.<uuid>", key, base)
		}
		suffix := strings.TrimPrefix(key, base+".")
		parsed, parseErr := uuid.Parse(suffix)
		if parseErr != nil || parsed.String() != suffix {
			t.Fatalf("minted suffix %q is not a canonical UUID: %v", suffix, parseErr)
		}
	}
	for _, blockID := range []string{"", strings.Repeat("z", 64)} {
		if key, mintErr := blockStore.MintStorageKey(blockID); mintErr == nil || key != "" {
			t.Fatalf("MintStorageKey(%q) = %q, %v, want empty key and error", blockID, key, mintErr)
		}
	}
}

func TestBlockStoreValidatePhysicalLocator(t *testing.T) {
	blockStore, backend := newRecordingBlockStore(t, "blocks/")
	base := blockStore.StorageKeyForHash(testHash64)
	minted := base + ".8f14e45f-ea4d-4f73-9f7c-63f4e7a5bc21"
	otherHash := "a3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	foreignBase := "blocks/" + testOrgB + "/e3/b0/" + testHash64

	for _, key := range []string{base, minted} {
		if err := blockStore.ValidatePhysicalLocator(testHash64, key); err != nil {
			t.Fatalf("ValidatePhysicalLocator(%q) error = %v", key, err)
		}
	}
	rejected := []string{
		blockStore.StorageKeyForHash(otherHash),
		base + ".not-a-uuid",
		base + ".8F14E45F-EA4D-4F73-9F7C-63F4E7A5BC21",
		base + ".{8f14e45f-ea4d-4f73-9f7c-63f4e7a5bc21}",
		base + ".8f14e45f-ea4d-4f73-9f7c-63f4e7a5bc21.extra",
		base + "/8f14e45f-ea4d-4f73-9f7c-63f4e7a5bc21",
		minted + " ",
		foreignBase + ".8f14e45f-ea4d-4f73-9f7c-63f4e7a5bc21",
	}
	for _, key := range rejected {
		if err := blockStore.ValidatePhysicalLocator(testHash64, key); err == nil {
			t.Fatalf("ValidatePhysicalLocator(%q) error = nil, want refusal", key)
		}
	}
	if got := backend.snapshot(); len(got) != 0 {
		t.Fatalf("locator validation reached backend: %v", got)
	}
}

func TestBlockStoreValidatePhysicalLocatorCustomPrefix(t *testing.T) {
	blockStore := mustNewTestOrgBlockStore(t, "custom/objects")
	base := blockStore.StorageKeyForHash(testHash64)
	if err := blockStore.ValidatePhysicalLocator(testHash64, base+".8f14e45f-ea4d-4f73-9f7c-63f4e7a5bc21"); err != nil {
		t.Fatalf("ValidatePhysicalLocator(custom minted key) error = %v", err)
	}
	defaultKey := "blocks/" + testOrgA + "/e3/b0/" + testHash64
	if err := blockStore.ValidatePhysicalLocator(testHash64, defaultKey); err == nil {
		t.Fatal("ValidatePhysicalLocator(default-prefix key) error = nil, want refusal")
	}
}

// A short block id makes hashToKey skip the sharded layout and return the bare
// tenant prefix plus the id, so the deterministic equality is satisfied by a key
// that addresses nothing in particular. Both cases below passed before the block
// id was validated first, which is the whole reason the ordering is fixed.
func TestBlockStoreValidatePhysicalLocatorRejectsMalformedBlockID(t *testing.T) {
	blockStore, backend := newRecordingBlockStore(t, "blocks/")
	tenantPrefix := "blocks/" + testOrgA + "/"

	malformed := []struct {
		name    string
		blockID string
		key     string
	}{
		{"empty id derives the bare prefix", "", tenantPrefix},
		{"two-char id skips sharding", "ab", tenantPrefix + "ab"},
		{"three-char id skips sharding", "abc", tenantPrefix + "abc"},
		{"sha-1 external id", strings.Repeat("a", 40), blockStore.StorageKeyForHash(strings.Repeat("a", 40))},
		{"64 chars but not hex", strings.Repeat("z", 64), blockStore.StorageKeyForHash(strings.Repeat("z", 64))},
	}
	for _, c := range malformed {
		t.Run(c.name, func(t *testing.T) {
			if err := blockStore.ValidatePhysicalLocator(c.blockID, c.key); err == nil {
				t.Fatalf("ValidatePhysicalLocator(%q, %q) error = nil, want malformed block id rejection", c.blockID, c.key)
			}
		})
	}

	// Uppercase hex is a well-formed content address, and its derived key matches
	// itself, so the validator must not reject it: db.IsSHA256BlockID accepts
	// either case and the two predicates have to agree.
	upper := strings.ToUpper(testHash64)
	if err := blockStore.ValidatePhysicalLocator(upper, blockStore.StorageKeyForHash(upper)); err != nil {
		t.Fatalf("ValidatePhysicalLocator(uppercase hex) error = %v, want nil", err)
	}

	if got := backend.snapshot(); len(got) != 0 {
		t.Fatalf("validation must not touch the backend, got %v", got)
	}
}

// TestHashSharding tests that hash sharding produces expected structure
func TestHashSharding(t *testing.T) {
	bs := mustNewTestOrgBlockStore(t, "blocks/")

	// Test that similar hashes are grouped together
	hash1 := "abcd1234567890"
	hash2 := "abcd9876543210"
	hash3 := "efgh1234567890"

	key1 := bs.hashToKey(hash1)
	key2 := bs.hashToKey(hash2)
	key3 := bs.hashToKey(hash3)

	// hash1 and hash2 should share the first two levels (ab/cd)
	wantSharedPrefix := "blocks/" + testOrgA + "/ab/cd/"
	if !strings.HasPrefix(key1, wantSharedPrefix) || !strings.HasPrefix(key2, wantSharedPrefix) {
		t.Errorf("Hashes starting with 'abcd' should share prefix, got %s and %s", key1, key2)
	}

	// hash3 should have different first level (ef)
	if strings.HasPrefix(key3, wantSharedPrefix) {
		t.Errorf("Hashes starting with 'abcd' and 'efgh' should have different first level")
	}
}
