//go:build integration

package integration

import (
	"os"
	"strings"
	"testing"
)

// TestDownloadAdmissionDerivesCapacityFromTheCgroupLimit is the only place the
// auto-capacity cgroup path is exercised end to end.
//
// Every other node in the stack runs without a memory limit, so its container
// reports "max" and the derivation always takes the conservative fallback —
// which means the branch that will size every real deployment never runs
// outside unit tests with an injected value. The `sesamefs-cgroup-probe`
// service exists for this: it declares a 4 GiB limit, so the capacities it
// reports must come from that limit and must differ from the fallback's.
// Asserting "different" rather than only "equal to the expected numbers" is
// what makes this a proof that the limit was read at all.
//
// Run deliberately:
//
//	docker compose --profile test up -d sesamefs-cgroup-probe
//	docker compose --profile test run --rm --entrypoint sh go-integration-test \
//	  -c 'export PATH=$PATH:/usr/local/go/bin && SESAMEFS_DOWNLOAD_PROBE=1 \
//	      go test -tags integration -run TestDownloadAdmissionDerivesCapacity -v -count=1 ./internal/integration/'
func TestDownloadAdmissionDerivesCapacityFromTheCgroupLimit(t *testing.T) {
	requireDownloadProbe(t)

	base := strings.TrimSpace(os.Getenv("SESAMEFS_CGROUP_PROBE_URL"))
	if base == "" {
		t.Skip("SESAMEFS_CGROUP_PROBE_URL is not set; start the sesamefs-cgroup-probe service")
	}
	probe := newTestClient(base, "dev-token-admin")

	const limit = int64(4 * 1024 * 1024 * 1024)
	const percent = 25

	budget := int64(scrapeDownloadMetric(t, probe, "download_admission_memory_budget_bytes", ""))
	// Same order as the derivation: multiplying first keeps the truncation where
	// the code puts it, so this compares against the value rather than against a
	// second, slightly different rounding of it.
	if want := limit * percent / 100; budget != want {
		t.Fatalf("derived budget = %d, want %d%% of the %d-byte container limit; the cgroup limit was not read",
			budget, percent, limit)
	}

	nodeCap := effectiveCapacity(t, probe, "max_active_per_node")
	if nodeCap <= 0 {
		t.Fatalf("probe reported node cap %d", nodeCap)
	}

	// The fallback budget is larger than this container's share, so a node that
	// fell back would report a strictly larger ceiling. Equality here would mean
	// the limit was ignored and the numbers coincided.
	fallbackCap := effectiveCapacity(t, newTestClient(strings.TrimSpace(os.Getenv("SESAMEFS_URL")), "dev-token-admin"), "max_active_per_node")
	if fallbackCap > 0 && nodeCap >= fallbackCap {
		t.Fatalf("cgroup-limited node derived %d slots and the unlimited node %d; a smaller container must derive a smaller ceiling",
			nodeCap, fallbackCap)
	}
	t.Logf("4 GiB container derived %d slots against the unlimited node's %d, from a %d-byte budget",
		nodeCap, fallbackCap, budget)
}
