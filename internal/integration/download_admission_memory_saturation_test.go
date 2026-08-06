//go:build integration

package integration

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestDownloadAdmissionMemoryUnderSaturation is an opt-in full-lifetime probe.
// It holds the auto-derived node ceiling with real storage readers and the real
// HTTP middleware, then compares correlated RSS/heap/cgroup deltas with the
// safety-adjusted design budget. The per-admission benchmarks remain the source
// for the raw/iWork and encrypted-stream costs; this test checks that the real
// process does not erase the aggregate headroom.
//
// Run deliberately:
//
//	docker compose --profile test run --rm --build --entrypoint sh go-integration-test \
//	  -c 'export PATH=$PATH:/usr/local/go/bin && SESAMEFS_DOWNLOAD_PROBE=1 SESAMEFS_DOWNLOAD_MEMORY_PROBE=1 \
//	      go test -tags integration -run TestDownloadAdmissionMemoryUnderSaturation -v -count=1 ./internal/integration/'
func TestDownloadAdmissionMemoryUnderSaturation(t *testing.T) {
	if os.Getenv("SESAMEFS_DOWNLOAD_MEMORY_PROBE") != "1" {
		t.Skip("set SESAMEFS_DOWNLOAD_MEMORY_PROBE=1 to run the real download memory probe")
	}
	client := requireDownloadProbe(t)
	clients := []*testClient{
		client,
		newTestClient(client.baseURL, "dev-token-user"),
		newTestClient(client.baseURL, "dev-token-superadmin"),
	}
	for _, probeClient := range clients {
		probeClient.http.Timeout = 2 * time.Minute
		if err := verifyIntegrationAuth(probeClient.baseURL, probeClient.token); err != nil {
			t.Fatalf("download memory probe auth for %q: %v", probeClient.token, err)
		}
	}

	// Everything the probe needs comes from the node. Pinning the baseline here
	// defeated the point of discovering the capacity: on a host that derives a
	// different ceiling this either refused to run or measured the real peak
	// against a budget the node never used.
	nodeCap := effectiveCapacity(t, client, "max_active_per_node")
	perIdentity := effectiveCapacity(t, client, "max_active_per_auth_user")
	if nodeCap <= 0 || perIdentity <= 0 {
		t.Fatalf("node reported capacity node=%d per-identity=%d", nodeCap, perIdentity)
	}
	if fillable := len(clients) * perIdentity; fillable < nodeCap {
		t.Skipf("this fixture can hold %d admissions but the node derived a ceiling of %d; "+
			"the probe measures a full node or nothing", fillable, nodeCap)
	}
	runID := fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	repoIDs := make([]string, len(clients))
	fileURLs := make([]string, len(clients))
	rawURLs := make([]string, len(clients))
	for i, probeClient := range clients {
		repoIDs[i] = createDisposableTestLibrary(t, probeClient, "inttest-d6-memory-"+runID+fmt.Sprintf("-%d", i))
		fileName := "d6-memory.bin"
		uploadProbeFile(t, probeClient, repoIDs[i], fileName, 24<<20)
		fileResp := probeClient.Get(t, fmt.Sprintf("/api2/repos/%s/file/?p=/%s", repoIDs[i], fileName))
		expectStatus(t, fileResp, 200)
		fileURLs[i] = strings.Trim(responseBody(t, fileResp), "\" \n\r")
		rawURLs[i] = fmt.Sprintf("%s/repo/%s/raw/%s", probeClient.baseURL, repoIDs[i], fileName)
	}

	_, baseline := waitForStableMemoryBaseline(t, client, runID)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var stopOnce sync.Once
	t.Cleanup(func() {
		stopOnce.Do(func() { close(stop) })
		wg.Wait()
		waitForDownloadActive(t, client, 0, 30*time.Second)
	})

	sampler := startMemoryProbeSampler(client, 0)
	t.Cleanup(func() { _, _ = sampler.stop() })
	rawSlots := effectiveCapacity(t, client, "max_active_raw")
	for i := 0; i < nodeCap; i++ {
		probeClient := clients[i%len(clients)]
		target := fileURLs[i%len(fileURLs)]
		if i < rawSlots {
			target = rawURLs[i%len(rawURLs)]
		}
		wg.Add(1)
		go func(probeClient *testClient, target string) {
			defer wg.Done()
			holdDownloadSlot(probeClient, target, stop)
		}(probeClient, target)
	}

	waitForDownloadActive(t, client, nodeCap, 30*time.Second)
	time.Sleep(2 * memoryProbeSamplePeriod)
	samples, sampleErrors := sampler.stop()
	if len(sampleErrors) > 0 {
		t.Fatalf("memory probe sampler failed: %v", sampleErrors[0])
	}
	if len(samples) == 0 {
		t.Fatal("memory probe collected no load samples")
	}
	stopOnce.Do(func() { close(stop) })
	wg.Wait()
	waitForDownloadActive(t, client, 0, 30*time.Second)

	peakRSS, peakHeap, peakCgroup := int64(0), int64(0), int64(0)
	for _, sample := range samples {
		if delta := sample.rss - baseline.rss; delta > peakRSS {
			peakRSS = delta
		}
		if delta := sample.heap - baseline.heap; delta > peakHeap {
			peakHeap = delta
		}
		if baseline.cgroupAvailable && sample.cgroupAvailable {
			if delta := sample.cgroup - baseline.cgroup; delta > peakCgroup {
				peakCgroup = delta
			}
		}
	}

	// The safety-adjusted budget the node actually sized itself against, read
	// from the node. Reconstructing it from a constant and an env var meant
	// guessing both the budget and the margin, and either guess measures the
	// real peak against a ceiling the process never used.
	limit := int64(scrapeDownloadMetric(t, client, "download_admission_memory_budget_effective_bytes", ""))
	if limit <= 0 {
		t.Fatalf("node reported an effective memory budget of %d", limit)
	}
	t.Logf("download memory probe: active=%d rss_delta=%d heap_delta=%d cgroup_delta=%d cgroup_available=%t safety_adjusted_budget=%d", nodeCap, peakRSS, peakHeap, peakCgroup, samples[len(samples)-1].cgroupAvailable, limit)
	if peakRSS > limit || peakHeap > limit || (peakCgroup > 0 && peakCgroup > limit) {
		t.Fatalf("real download memory peak exceeded safety-adjusted budget %d: rss=%d heap=%d cgroup=%d", limit, peakRSS, peakHeap, peakCgroup)
	}
}
