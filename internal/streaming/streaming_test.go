package streaming

import (
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"

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
