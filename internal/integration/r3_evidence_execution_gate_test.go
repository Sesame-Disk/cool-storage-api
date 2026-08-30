//go:build integration

package integration

// TestMain checks this after m.Run when R3 evidence is required. Keeping the
// observation at package scope means a -run filter that excludes the Cassandra
// race cannot silently satisfy SESAMEFS_REQUIRE_R3_CHARACTERIZATION=1.
var r3CharacterizationEvidenceObserved bool
