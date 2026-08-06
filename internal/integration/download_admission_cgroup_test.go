//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// TestDownloadAdmissionDerivesCapacityFromTheCgroupLimit is the only place the
// auto-capacity cgroup path is exercised end to end.
//
// Every other node in the stack runs without a memory limit, so its container
// reports "max" and the derivation always takes the reference fallback — which
// means the branch that will size every real deployment never runs outside unit
// tests with an injected value. The `sesamefs-cgroup-probe` service exists for
// this: it declares a 4 GiB limit and pins every input the arithmetic depends
// on, so the whole chain is checkable against exact numbers.
//
//	4 GiB limit -> 25% budget = 1 GiB -> 20% headroom = 819.2 MiB
//	raw 192 MiB, stream 72 MiB -> 2 raw + 6 stream = 816 MiB -> 8 slots
//
// It asserts the budget *source* as well as the numbers, because those numbers
// alone cannot tell a derived budget from an explicit one of the same size: the
// claim being made is that the container limit was read, not that the result
// looked plausible.
//
// Run deliberately:
//
//	docker compose --profile test up -d --wait sesamefs-cgroup-probe
//	docker compose --profile test run --rm --entrypoint sh \
//	  -e SESAMEFS_CGROUP_PROBE_URL=http://sesamefs-cgroup-probe:8080 go-integration-test \
//	  -c 'export PATH=$PATH:/usr/local/go/bin && SESAMEFS_DOWNLOAD_PROBE=1 \
//	      go test -tags integration -run TestDownloadAdmissionDerivesCapacity -v -count=1 ./internal/integration/'
func TestDownloadAdmissionDerivesCapacityFromTheCgroupLimit(t *testing.T) {
	requireDownloadProbe(t)

	base := strings.TrimSpace(os.Getenv("SESAMEFS_CGROUP_PROBE_URL"))
	if base == "" {
		t.Skip("SESAMEFS_CGROUP_PROBE_URL is not set; start the sesamefs-cgroup-probe service")
	}
	probe := newTestClient(base, "dev-token-admin")
	waitForProbeReady(t, probe, 90*time.Second)

	// Derived, not configured. Without this the assertions below would pass on a
	// node that never consulted its cgroup.
	if source := scrapeDownloadMetric(t, probe, "download_admission_budget_source", `source="cgroup"`); source != 1 {
		configured := scrapeDownloadMetric(t, probe, "download_admission_budget_source", `source="configured"`)
		fallback := scrapeDownloadMetric(t, probe, "download_admission_budget_source", `source="fallback"`)
		t.Fatalf("budget did not come from the cgroup limit (cgroup=%.0f configured=%.0f fallback=%.0f); "+
			"the probe service must leave memory_budget_bytes at 0 so auto mode derives it", source, configured, fallback)
	}

	// The exact chain from a 4 GiB container, with every input pinned by the
	// service definition. Comparing against another node's numbers instead would
	// make this depend on two processes and two configurations to prove one.
	for _, want := range []struct {
		what  string
		value int
	}{
		{"max_active_per_node", 8},
		{"max_active_raw", 2},
		{"max_active_file", 6},
		{"max_active_block", 6},
	} {
		if got := effectiveCapacity(t, probe, want.what); got != want.value {
			t.Fatalf("%s = %d, want %d from a 4 GiB container", want.what, got, want.value)
		}
	}
	if got := int64(scrapeDownloadMetric(t, probe, "download_admission_memory_budget_bytes", "")); got != 1<<30 {
		t.Fatalf("derived budget = %d, want 25%% of the 4 GiB container limit", got)
	}

	// Informational only: the unlimited node is a different configuration and
	// must not be part of what this test proves.
	if main := strings.TrimSpace(os.Getenv("SESAMEFS_URL")); main != "" {
		unlimited := effectiveCapacity(t, newTestClient(main, "dev-token-admin"), "max_active_per_node")
		t.Logf("4 GiB container derived 8 slots; the unlimited node derived %d", unlimited)
	}
}

// waitForProbeReady covers the gap between "container started" and "the process
// finished bootstrapping and published its capacity". `up -d` only guarantees
// the former, so scraping immediately is a race that surfaces as a connection
// refusal rather than as a real failure.
func waitForProbeReady(t *testing.T, c *testClient, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodGet, c.baseURL+"/metrics", nil)
		if err != nil {
			t.Fatalf("build probe readiness request: %v", err)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			last = err.Error()
			time.Sleep(500 * time.Millisecond)
			continue
		}
		body := responseBody(t, resp)
		if resp.StatusCode == http.StatusOK && strings.Contains(body, "download_admission_capacity") {
			return
		}
		last = fmt.Sprintf("GET /metrics = %d without the capacity series", resp.StatusCode)
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("cgroup probe never became ready within %s: %s", timeout, last)
}
