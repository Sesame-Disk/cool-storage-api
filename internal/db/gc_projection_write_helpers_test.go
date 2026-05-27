package db

import (
	"testing"
	"time"
)

func TestGCDiscoveryBucketIsDeterministic(t *testing.T) {
	a := GCDiscoveryBucket("org-1", "block-abc")
	b := GCDiscoveryBucket("org-1", "block-abc")
	if a != b {
		t.Fatalf("GCDiscoveryBucket must be deterministic, got %d then %d", a, b)
	}
	if a < 0 || a >= GCDiscoveryBucketCount {
		t.Fatalf("bucket %d outside expected range [0, %d)", a, GCDiscoveryBucketCount)
	}
}

func TestGCDiscoveryBucketDistributesAcrossKeys(t *testing.T) {
	// 1000 distinct (org, block) pairs against 32 buckets should populate
	// every bucket at least once. The test guards against accidentally
	// reverting to a hash that collapses to a single value (e.g. unseeded).
	seen := make(map[int]struct{}, GCDiscoveryBucketCount)
	for i := 0; i < 1000; i++ {
		b := GCDiscoveryBucket("org", "block-"+time.Unix(int64(i), 0).Format(time.RFC3339Nano))
		seen[b] = struct{}{}
	}
	if len(seen) < GCDiscoveryBucketCount {
		t.Fatalf("expected %d buckets covered, got %d", GCDiscoveryBucketCount, len(seen))
	}
}

func TestGCProjectionUTCDateTruncatesToMidnightUTC(t *testing.T) {
	// Any timestamp on a UTC day must collapse to that day's 00:00 UTC.
	in := time.Date(2026, 5, 26, 23, 59, 59, 999, time.UTC)
	got := GCProjectionUTCDate(in)
	want := time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("GCProjectionUTCDate(%v) = %v, want %v", in, got, want)
	}
}

func TestGCProjectionUTCDateNormalizesOtherZones(t *testing.T) {
	// 2026-05-26 21:00 -05:00 is the same instant as 2026-05-27 02:00 UTC,
	// so the projected day must be the UTC day, not the local day.
	loc := time.FixedZone("CDT", -5*3600)
	in := time.Date(2026, 5, 26, 21, 0, 0, 0, loc)
	got := GCProjectionUTCDate(in)
	want := time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("GCProjectionUTCDate(%v) = %v, want %v", in, got, want)
	}
}
