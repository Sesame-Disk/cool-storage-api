package streaming

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

// sha1Hex / sha256Hex produce deterministic, unique hex IDs of the exact
// lengths resolveBlockIDs keys off: 40 chars = SHA-1 (needs resolution),
// 64 chars = SHA-256 (returned as-is).
func sha1Hex(n int) string   { return fmt.Sprintf("%040x", n) }
func sha256Hex(n int) string { return fmt.Sprintf("%064x", n) }

// TestResolveBlockIDs_PreservesOrderAndMixedHashes verifies that when every
// SHA-1 entry resolves, an interleaved mix of SHA-256 (pass-through) and SHA-1
// (resolved) entries comes back in the original positions with a nil error, and
// that lookup is invoked only for the SHA-1 entries.
func TestResolveBlockIDs_PreservesOrderAndMixedHashes(t *testing.T) {
	blockIDs := []string{
		sha256Hex(0), // pass-through
		sha1Hex(1),   // -> sha256Hex(1001)
		sha1Hex(2),   // -> sha256Hex(1002)
		sha256Hex(3), // pass-through
		sha1Hex(4),   // -> sha256Hex(1004)
	}
	mapping := map[string]string{
		sha1Hex(1): sha256Hex(1001),
		sha1Hex(2): sha256Hex(1002),
		sha1Hex(4): sha256Hex(1004),
	}

	var calls atomic.Int32
	lookup := func(idx int) (string, error) {
		calls.Add(1)
		if len(blockIDs[idx]) != 40 {
			t.Errorf("lookup called for non-SHA-1 index %d (%s)", idx, blockIDs[idx])
		}
		if v, ok := mapping[blockIDs[idx]]; ok {
			return v, nil
		}
		return "", gocql.ErrNotFound
	}

	got, err := resolveBlockIDs("org", blockIDs, 32, lookup)
	if err != nil {
		t.Fatalf("resolveBlockIDs() error = %v, want nil", err)
	}
	want := []string{sha256Hex(0), sha256Hex(1001), sha256Hex(1002), sha256Hex(3), sha256Hex(1004)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolveBlockIDs() = %v, want %v", got, want)
	}
	if n := calls.Load(); n != 3 {
		t.Errorf("lookup called %d times, want 3 (only SHA-1 entries)", n)
	}
}

// TestResolveBlockIDs_MissingMappingIsFatal verifies that a missing mapping row
// (gocql.ErrNotFound) aborts the whole resolution with a nil slice and a non-nil
// error that wraps the underlying cause — a stale SHA-1 must never be streamed.
func TestResolveBlockIDs_MissingMappingIsFatal(t *testing.T) {
	blockIDs := []string{sha1Hex(1), sha1Hex(2)}
	lookup := func(idx int) (string, error) {
		if idx == 0 {
			return sha256Hex(101), nil
		}
		return "", gocql.ErrNotFound // no mapping row for blockIDs[1]
	}

	got, err := resolveBlockIDs("org", blockIDs, 8, lookup)
	if err == nil {
		t.Fatal("resolveBlockIDs() error = nil, want non-nil on missing mapping")
	}
	if !errors.Is(err, gocql.ErrNotFound) {
		t.Errorf("error %v does not wrap gocql.ErrNotFound", err)
	}
	if got != nil {
		t.Errorf("resolveBlockIDs() = %v, want nil slice on error", got)
	}
}

// TestResolveBlockIDs_QueryErrorIsFatal verifies that a real (non-NotFound)
// lookup error aborts the whole resolution: nil slice, and the error wraps the
// underlying cause so the caller can log/branch on it.
func TestResolveBlockIDs_QueryErrorIsFatal(t *testing.T) {
	blockIDs := []string{sha1Hex(1), sha1Hex(2), sha1Hex(3)}
	boom := errors.New("cassandra read timeout")
	lookup := func(idx int) (string, error) {
		if idx == 1 {
			return "", boom
		}
		return sha256Hex(100 + idx), nil
	}

	got, err := resolveBlockIDs("org", blockIDs, 8, lookup)
	if err == nil {
		t.Fatal("resolveBlockIDs() error = nil, want non-nil on query failure")
	}
	if !errors.Is(err, boom) {
		t.Errorf("error %v does not wrap underlying cause %v", err, boom)
	}
	if got != nil {
		t.Errorf("resolveBlockIDs() = %v, want nil slice on error", got)
	}
}

// TestResolveBlockIDs_JoinsMultipleCauses verifies that all failing blocks are
// drained and every distinct cause is preserved in the joined error, rather than
// short-circuiting on the first failure.
func TestResolveBlockIDs_JoinsMultipleCauses(t *testing.T) {
	blockIDs := []string{sha1Hex(1), sha1Hex(2), sha1Hex(3)}
	boom := errors.New("unavailable")
	lookup := func(idx int) (string, error) {
		switch idx {
		case 0:
			return "", boom // transient query error
		case 2:
			return "", gocql.ErrNotFound // missing mapping
		default:
			return sha256Hex(100 + idx), nil
		}
	}

	_, err := resolveBlockIDs("org", blockIDs, 8, lookup)
	if err == nil {
		t.Fatal("resolveBlockIDs() error = nil, want joined error")
	}
	if !errors.Is(err, boom) || !errors.Is(err, gocql.ErrNotFound) {
		t.Errorf("joined error %v must wrap both the query error and ErrNotFound", err)
	}
}

// TestResolveBlockIDs_EmptyMappingIsFatal verifies that a row that exists but
// carries an empty internal_id is treated as unresolved (fatal), not silently
// left as the original SHA-1.
func TestResolveBlockIDs_EmptyMappingIsFatal(t *testing.T) {
	blockIDs := []string{sha1Hex(1)}
	lookup := func(idx int) (string, error) { return "", nil }

	got, err := resolveBlockIDs("org", blockIDs, 8, lookup)
	if err == nil {
		t.Fatal("resolveBlockIDs() error = nil, want non-nil on empty internal_id")
	}
	if got != nil {
		t.Errorf("resolveBlockIDs() = %v, want nil slice on error", got)
	}
}

// TestResolveBlockIDs_NoResolutionNeeded verifies that an all-SHA-256 input
// short-circuits without calling lookup, returns a nil error, and returns a fresh
// copy that does not alias the caller's slice.
func TestResolveBlockIDs_NoResolutionNeeded(t *testing.T) {
	blockIDs := []string{sha256Hex(1), sha256Hex(2)}
	lookup := func(idx int) (string, error) {
		t.Fatalf("lookup must not be called when no SHA-1 IDs are present (idx=%d)", idx)
		return "", nil
	}

	got, err := resolveBlockIDs("org", blockIDs, 8, lookup)
	if err != nil {
		t.Fatalf("resolveBlockIDs() error = %v, want nil", err)
	}
	want := []string{sha256Hex(1), sha256Hex(2)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolveBlockIDs() = %v, want %v", got, want)
	}

	got[0] = "mutated"
	if blockIDs[0] == "mutated" {
		t.Error("resolveBlockIDs returned a slice aliased to its input")
	}
}

// TestResolveBlockIDs_ConcurrentResolutionPreservesOrder stresses the bounded
// fan-out across concurrency limits below, between, at, and above the work size
// (and the < 1 guard) to catch ordering bugs and data races (run with -race).
func TestResolveBlockIDs_ConcurrentResolutionPreservesOrder(t *testing.T) {
	const n = 500
	blockIDs := make([]string, n)
	want := make([]string, n)
	for i := range blockIDs {
		blockIDs[i] = sha1Hex(i)
		want[i] = sha256Hex(1_000_000 + i)
	}
	lookup := func(idx int) (string, error) {
		return sha256Hex(1_000_000 + idx), nil
	}

	for _, concurrency := range []int{0, 1, 7, 32, 1000} {
		got, err := resolveBlockIDs("org", blockIDs, concurrency, lookup)
		if err != nil {
			t.Fatalf("concurrency=%d: unexpected error %v", concurrency, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("concurrency=%d: order/value mismatch", concurrency)
		}
	}
}

// TestResolveBlockIDs_CanonicalizesBeforeAndAfterLookup verifies the "trimmed
// lowercase" contract holds end-to-end: an uppercase SHA-1, a space-padded SHA-1,
// and an uppercase SHA-256 are all canonicalized BEFORE classification, and an
// uppercase internal_id returned by the mapping is canonicalized before it is
// handed back. The lookup normalizes its key exactly like the Cassandra query.
func TestResolveBlockIDs_CanonicalizesBeforeAndAfterLookup(t *testing.T) {
	upperSHA1 := strings.ToUpper(sha1Hex(1)) // classified as SHA-1 after normalize
	paddedSHA1 := "  " + sha1Hex(2) + "  "   // 44 chars raw, 40 after trim
	upperSHA256 := strings.ToUpper(sha256Hex(3))
	blockIDs := []string{upperSHA1, paddedSHA1, upperSHA256, sha1Hex(4)}

	mapping := map[string]string{
		sha1Hex(1): sha256Hex(1001),
		sha1Hex(2): sha256Hex(1002),
		sha1Hex(4): strings.ToUpper(sha256Hex(1004)), // uppercase internal_id
	}
	lookup := func(idx int) (string, error) {
		key := db.NormalizeBlockID(blockIDs[idx])
		if v, ok := mapping[key]; ok {
			return v, nil
		}
		return "", gocql.ErrNotFound
	}

	got, err := resolveBlockIDs("org", blockIDs, 32, lookup)
	if err != nil {
		t.Fatalf("resolveBlockIDs() error = %v, want nil", err)
	}
	want := []string{sha256Hex(1001), sha256Hex(1002), sha256Hex(3), sha256Hex(1004)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolveBlockIDs() = %v, want %v (canonicalized)", got, want)
	}
}

// TestResolveBlockIDs_RejectsNonSHAInternalID verifies that a mapping row whose
// internal_id is not a hex 64-char SHA-256 aborts resolution rather than streaming
// a bogus block id to storage — both a short non-hex value and a 64-char non-hex
// value (right length, wrong content) must fail.
func TestResolveBlockIDs_RejectsNonSHAInternalID(t *testing.T) {
	for name, internal := range map[string]string{
		"short-non-hex":    "not-a-sha256",
		"64-char-non-hex":  strings.Repeat("g", 64), // g is not a hex digit
		"64-char-with-dot": strings.Repeat("a", 63) + ".",
	} {
		t.Run(name, func(t *testing.T) {
			lookup := func(idx int) (string, error) { return internal, nil }
			got, err := resolveBlockIDs("org", []string{sha1Hex(1)}, 8, lookup)
			if err == nil {
				t.Fatalf("resolveBlockIDs() error = nil, want non-nil on internal_id %q", internal)
			}
			if got != nil {
				t.Errorf("resolveBlockIDs() = %v, want nil slice on error", got)
			}
		})
	}
}

// TestResolveBlockIDs_RejectsNonHexBlockIDs verifies that an input id of the right
// LENGTH but wrong CONTENT (40 or 64 non-hex chars) is a fatal, pre-lookup error,
// so the "valid hex SHA-1/SHA-256" contract is enforced on content, not length.
func TestResolveBlockIDs_RejectsNonHexBlockIDs(t *testing.T) {
	for name, id := range map[string]string{
		"40-char-non-hex": strings.Repeat("z", 40),
		"64-char-non-hex": strings.Repeat("z", 64),
	} {
		t.Run(name, func(t *testing.T) {
			var called atomic.Int32
			lookup := func(idx int) (string, error) {
				called.Add(1)
				return sha256Hex(1), nil
			}
			got, err := resolveBlockIDs("org", []string{id}, 8, lookup)
			if err == nil {
				t.Fatalf("resolveBlockIDs() error = nil, want non-nil on non-hex id %q", id)
			}
			if got != nil {
				t.Errorf("resolveBlockIDs() = %v, want nil slice on error", got)
			}
			if n := called.Load(); n != 0 {
				t.Errorf("lookup called %d times, want 0 (non-hex id rejected before lookup)", n)
			}
		})
	}
}

// TestResolveBlockIDs_RejectsInvalidLengthID verifies that an id which is neither a
// 40-char SHA-1 nor a 64-char SHA-256 is a fatal, pre-lookup error.
func TestResolveBlockIDs_RejectsInvalidLengthID(t *testing.T) {
	blockIDs := []string{sha256Hex(0), "deadbeef"} // second is 8 chars
	var called atomic.Int32
	lookup := func(idx int) (string, error) {
		called.Add(1)
		return sha256Hex(1), nil
	}
	got, err := resolveBlockIDs("org", blockIDs, 8, lookup)
	if err == nil {
		t.Fatal("resolveBlockIDs() error = nil, want non-nil on invalid-length id")
	}
	if got != nil {
		t.Errorf("resolveBlockIDs() = %v, want nil slice on error", got)
	}
	if n := called.Load(); n != 0 {
		t.Errorf("lookup called %d times, want 0 (invalid id rejected before lookup)", n)
	}
}

// TestContainsLegacySHA1 verifies the fast-path guard callers use to skip the
// representation_id lookup on all-SHA-256 block lists.
func TestContainsLegacySHA1(t *testing.T) {
	if ContainsLegacySHA1([]string{sha256Hex(0), sha256Hex(1)}) {
		t.Error("ContainsLegacySHA1 = true for all-SHA-256 list, want false")
	}
	if !ContainsLegacySHA1([]string{sha256Hex(0), sha1Hex(1)}) {
		t.Error("ContainsLegacySHA1 = false with a SHA-1 present, want true")
	}
	if !ContainsLegacySHA1([]string{"  " + strings.ToUpper(sha1Hex(2)) + "  "}) {
		t.Error("ContainsLegacySHA1 = false for a padded/uppercase SHA-1, want true")
	}
}
