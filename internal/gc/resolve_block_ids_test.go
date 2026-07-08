package gc

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

// sha1Hex / sha256Hex produce deterministic, unique hex IDs of the exact
// lengths resolveBlockIDsConcurrent keys off: 40 chars = SHA-1 (needs
// resolution), 64 chars = SHA-256 (returned as-is).
func sha1Hex(n int) string   { return fmt.Sprintf("%040x", n) }
func sha256Hex(n int) string { return fmt.Sprintf("%064x", n) }

// TestResolveBlockIDsConcurrent_PreservesOrderAndMixedHashes verifies an
// interleaved mix of SHA-256 (pass-through) and SHA-1 (resolved) entries comes
// back in the original positions, lookup runs only for SHA-1 entries, and the
// fully-resolved batch returns a nil error.
func TestResolveBlockIDsConcurrent_PreservesOrderAndMixedHashes(t *testing.T) {
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

	got, err := resolveBlockIDsConcurrent(uuid.Nil, blockIDs, 32, lookup)
	if err != nil {
		t.Fatalf("resolveBlockIDsConcurrent() error = %v, want nil", err)
	}
	want := []string{sha256Hex(0), sha256Hex(1001), sha256Hex(1002), sha256Hex(3), sha256Hex(1004)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolveBlockIDsConcurrent() = %v, want %v", got, want)
	}
	if n := calls.Load(); n != 3 {
		t.Errorf("lookup called %d times, want 3 (only SHA-1 entries)", n)
	}
}

// TestResolveBlockIDsConcurrent_NotFoundLeavesOriginalID verifies that a missing
// mapping row (gocql.ErrNotFound) leaves the original SHA-1 ID in place and is
// NOT treated as a fatal error.
func TestResolveBlockIDsConcurrent_NotFoundLeavesOriginalID(t *testing.T) {
	blockIDs := []string{sha1Hex(1), sha1Hex(2)}
	lookup := func(idx int) (string, error) {
		if idx == 0 {
			return sha256Hex(101), nil
		}
		return "", gocql.ErrNotFound // no mapping row for blockIDs[1]
	}

	got, err := resolveBlockIDsConcurrent(uuid.Nil, blockIDs, 8, lookup)
	if err != nil {
		t.Fatalf("resolveBlockIDsConcurrent() error = %v, want nil (NotFound is not fatal)", err)
	}
	want := []string{sha256Hex(101), sha1Hex(2)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolveBlockIDsConcurrent() = %v, want %v", got, want)
	}
}

// TestResolveBlockIDsConcurrent_GarbageInternalIDLeavesOriginal verifies the
// deliberate GC leniency: a mapping whose internal_id is not a hex SHA-256 is
// SKIPPED (original SHA-1 kept), not fatal — GC must not wedge on a garbage/stale
// mapping row, and must not poison the reference key with a non-canonical id.
func TestResolveBlockIDsConcurrent_GarbageInternalIDLeavesOriginal(t *testing.T) {
	blockIDs := []string{sha1Hex(1), sha1Hex(2)}
	lookup := func(idx int) (string, error) {
		if idx == 0 {
			return sha256Hex(101), nil
		}
		return strings.Repeat("g", 64), nil // right length, non-hex garbage
	}

	got, err := resolveBlockIDsConcurrent(uuid.Nil, blockIDs, 8, lookup)
	if err != nil {
		t.Fatalf("resolveBlockIDsConcurrent() error = %v, want nil (garbage internal_id is not fatal)", err)
	}
	want := []string{sha256Hex(101), sha1Hex(2)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolveBlockIDsConcurrent() = %v, want %v (garbage kept as original SHA-1)", got, want)
	}
}

// TestResolveBlockIDsConcurrent_NonHexInputIsLenient pins the deliberate GC
// leniency for malformed INPUT ids so a future refactor cannot silently make GC
// strict and wedge cleanups over damaged metadata: a 40-char non-hex id is not
// looked up (it is not a valid SHA-1) and a wrong-length id is kept as-is, both
// without error.
func TestResolveBlockIDsConcurrent_NonHexInputIsLenient(t *testing.T) {
	nonHex40 := strings.Repeat("z", 40)
	wrongLen := strings.Repeat("a", 30)
	blockIDs := []string{nonHex40, wrongLen, sha1Hex(1)}

	var called atomic.Int32
	lookup := func(idx int) (string, error) {
		called.Add(1)
		if blockIDs[idx] != sha1Hex(1) {
			t.Errorf("lookup called for non-SHA-1 index %d (%q)", idx, blockIDs[idx])
		}
		return sha256Hex(101), nil
	}

	got, err := resolveBlockIDsConcurrent(uuid.Nil, blockIDs, 8, lookup)
	if err != nil {
		t.Fatalf("resolveBlockIDsConcurrent() error = %v, want nil (malformed input is not fatal)", err)
	}
	want := []string{nonHex40, wrongLen, sha256Hex(101)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolveBlockIDsConcurrent() = %v, want %v (malformed inputs kept, only SHA-1 resolved)", got, want)
	}
	if n := called.Load(); n != 1 {
		t.Errorf("lookup called %d times, want 1 (only the hex SHA-1 entry)", n)
	}
}

// TestResolveBlockIDsConcurrent_RealErrorFailsBatch verifies that a real
// (non-NotFound) lookup error fails the whole resolution: the slice is nil and
// the returned error wraps the underlying cause (callers must not act on a
// partially-resolved slice).
func TestResolveBlockIDsConcurrent_RealErrorFailsBatch(t *testing.T) {
	blockIDs := []string{sha1Hex(1), sha1Hex(2), sha1Hex(3)}
	boom := errors.New("cassandra read timeout")
	lookup := func(idx int) (string, error) {
		if idx == 1 {
			return "", boom
		}
		return sha256Hex(100 + idx), nil
	}

	got, err := resolveBlockIDsConcurrent(uuid.Nil, blockIDs, 8, lookup)
	if err == nil {
		t.Fatal("resolveBlockIDsConcurrent() error = nil, want non-nil on real lookup failure")
	}
	if !errors.Is(err, boom) {
		t.Errorf("error %v does not wrap underlying cause %v", err, boom)
	}
	if got != nil {
		t.Errorf("resolveBlockIDsConcurrent() = %v, want nil slice on error", got)
	}
}

// TestResolveBlockIDsConcurrent_JoinsMultipleErrors verifies that every failing
// block is drained and its cause is preserved in the joined error, rather than
// short-circuiting on the first failure.
func TestResolveBlockIDsConcurrent_JoinsMultipleErrors(t *testing.T) {
	blockIDs := []string{sha1Hex(1), sha1Hex(2), sha1Hex(3)}
	errA := errors.New("timeout-a")
	errB := errors.New("unavailable-b")
	lookup := func(idx int) (string, error) {
		switch idx {
		case 0:
			return "", errA
		case 2:
			return "", errB
		default:
			return sha256Hex(100 + idx), nil
		}
	}

	_, err := resolveBlockIDsConcurrent(uuid.Nil, blockIDs, 8, lookup)
	if err == nil {
		t.Fatal("resolveBlockIDsConcurrent() error = nil, want joined error")
	}
	if !errors.Is(err, errA) || !errors.Is(err, errB) {
		t.Errorf("joined error %v must wrap both errA and errB", err)
	}
}

// TestResolveBlockIDsConcurrent_EmptyMappingLeavesOriginalID verifies that a row
// that exists but carries an empty internal_id leaves the original ID in place
// without producing an error.
func TestResolveBlockIDsConcurrent_EmptyMappingLeavesOriginalID(t *testing.T) {
	blockIDs := []string{sha1Hex(1)}
	lookup := func(idx int) (string, error) { return "", nil }

	got, err := resolveBlockIDsConcurrent(uuid.Nil, blockIDs, 8, lookup)
	if err != nil {
		t.Fatalf("resolveBlockIDsConcurrent() error = %v, want nil", err)
	}
	want := []string{sha1Hex(1)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolveBlockIDsConcurrent() = %v, want %v", got, want)
	}
}

// TestResolveBlockIDsConcurrent_NoResolutionNeeded verifies that an all-SHA-256
// input short-circuits without calling lookup and returns a fresh copy that does
// not alias the caller's slice.
func TestResolveBlockIDsConcurrent_NoResolutionNeeded(t *testing.T) {
	blockIDs := []string{sha256Hex(1), sha256Hex(2)}
	lookup := func(idx int) (string, error) {
		t.Fatalf("lookup must not be called when no SHA-1 IDs are present (idx=%d)", idx)
		return "", nil
	}

	got, err := resolveBlockIDsConcurrent(uuid.Nil, blockIDs, 8, lookup)
	if err != nil {
		t.Fatalf("resolveBlockIDsConcurrent() error = %v, want nil", err)
	}
	want := []string{sha256Hex(1), sha256Hex(2)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolveBlockIDsConcurrent() = %v, want %v", got, want)
	}

	got[0] = "mutated"
	if blockIDs[0] == "mutated" {
		t.Error("resolveBlockIDsConcurrent returned a slice aliased to its input")
	}
}

// TestResolveBlockIDsConcurrent_ConcurrentResolutionPreservesOrder stresses the
// bounded fan-out across concurrency limits below, between, at, and above the
// work size (and the < 1 guard) to catch ordering bugs and data races (run with
// -race).
func TestResolveBlockIDsConcurrent_ConcurrentResolutionPreservesOrder(t *testing.T) {
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
		got, err := resolveBlockIDsConcurrent(uuid.Nil, blockIDs, concurrency, lookup)
		if err != nil {
			t.Fatalf("concurrency=%d: unexpected error %v", concurrency, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("concurrency=%d: order/value mismatch", concurrency)
		}
	}
}
