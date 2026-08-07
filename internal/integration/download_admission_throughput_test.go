//go:build integration

package integration

import (
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"
)

// Criterion 12 asks for measured egress, and the D0 contract is explicit that
// concurrency alone is not a byte-rate shaper: N admitted transfers each run as
// fast as the link allows, so the node's egress ceiling is
//
//	max_active_per_node × per-transfer throughput
//
// That product is the number the byte-rate residual is about. This measures the
// per-transfer figure and the aggregate under a full budget so the residual is
// stated with evidence rather than asserted.
//
// It deliberately asserts almost nothing about the value. Throughput on a
// container stack on developer hardware is not a production number, and a
// threshold here would either be meaningless or flaky. The test records the
// single-transfer and aggregate measurements; it does not claim to prove a
// scaling law with a fragile timing assertion. If byte-rate shaping is added,
// the recorded relationship and this residual must be re-evaluated.
//
// Run deliberately:
//
//	docker compose --profile test run --rm --build --entrypoint sh go-integration-test \
//	  -c 'export PATH=$PATH:/usr/local/go/bin && SESAMEFS_DOWNLOAD_PROBE=1 \
//	      go test -tags integration -run TestDownloadAdmissionEgress -v -count=1 ./internal/integration/'
func TestDownloadAdmissionEgressScalesWithConcurrency(t *testing.T) {
	client := requireDownloadProbe(t)
	repoID := createDisposableTestLibrary(t, client, "inttest-d6-egress")
	fileName := "d6-egress.bin"
	const size = 16 << 20
	uploadProbeFile(t, client, repoID, fileName, size)

	target := client.baseURL + fmt.Sprintf("/repo/%s/raw/%s", repoID, fileName)

	if active := scrapeDownloadGaugeInt(t, client, "download_admission_active_current", true); active != 0 {
		t.Fatalf("node already has %d active admissions; the measurement needs an idle node", active)
	}

	single := measureTransfer(t, client, target)
	t.Logf("single transfer: %.1f MiB in %s = %.1f MiB/s",
		float64(size)/(1<<20), single.Round(time.Millisecond), mibPerSecond(size, single))

	// Fill the identity budget and measure the aggregate. The point is the
	// relationship, not the absolute rate.
	const concurrent = 6
	var wg sync.WaitGroup
	durations := make([]time.Duration, concurrent)
	start := time.Now()
	for i := 0; i < concurrent; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			durations[idx] = measureTransfer(t, client, target)
		}(i)
	}
	wg.Wait()
	wall := time.Since(start)

	totalBytes := int64(size) * concurrent
	aggregate := mibPerSecond(int(totalBytes), wall)
	t.Logf("%d concurrent transfers: %.1f MiB in %s = %.1f MiB/s aggregate",
		concurrent, float64(totalBytes)/(1<<20), wall.Round(time.Millisecond), aggregate)

	slowest := time.Duration(0)
	for _, d := range durations {
		if d > slowest {
			slowest = d
		}
	}
	t.Logf("slowest concurrent transfer %s vs %s alone", slowest.Round(time.Millisecond), single.Round(time.Millisecond))

	// The residual in one line: nothing here caps bytes per second, so the node's
	// egress can scale with the concurrency cap. Recording the multiple makes the
	// observed result concrete for a capacity plan; this is measurement evidence,
	// not a portable automated threshold.
	t.Logf("egress residual: aggregate was %.1fx the single-transfer rate at a %d-way budget",
		aggregate/mibPerSecond(size, single), concurrent)

	if aggregate <= 0 {
		t.Fatal("aggregate throughput measured as zero; the measurement did not run")
	}

	waitForDownloadActive(t, client, 0, 30*time.Second)
}

func measureTransfer(t *testing.T, c *testClient, target string) time.Duration {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Errorf("build request: %v", err)
		return 0
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Accept-Encoding", "identity")

	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		t.Errorf("transfer: %v", err)
		return 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("transfer = %d, want 200", resp.StatusCode)
		return 0
	}
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Errorf("drain transfer: %v", err)
		return 0
	}
	return time.Since(start)
}

func mibPerSecond(bytes int, d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	return (float64(bytes) / (1 << 20)) / d.Seconds()
}
