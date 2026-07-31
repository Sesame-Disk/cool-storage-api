//go:build integration

package integration

import (
	"bufio"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
)

const (
	memoryProbeSamplePeriod = 50 * time.Millisecond
	memoryProbeBodyBytes    = config.DefaultSyncBlockMaxBytes
	memoryProbeNodeCap      = config.DefaultSyncBlockMaxInflightPerNode
	memoryProbeUserCap      = config.DefaultSyncBlockMaxInflightPerUser
	memoryProbeBudgetBytes  = int64(2 * 1024 * 1024 * 1024)
	memoryProbeSafetyNum    = int64(5) // 1.25x, represented exactly below.
	memoryProbeSafetyDen    = int64(4)
)

// TestSyncBlockMemoryUnderSaturation is an opt-in measurement for the default
// sync block node cap. It samples complete metric tuples at a fixed cadence,
// subtracts a stable idle baseline, and holds cap-sized request bodies resident
// long enough to observe a deterministic full-node plateau.
//
// Run deliberately; this sends more than 500 MiB to a real node:
//
//	docker compose --profile test run --rm --build --entrypoint sh go-integration-test \
//	  -c 'export PATH=$PATH:/usr/local/go/bin && SESAMEFS_MEMORY_PROBE=1 \
//	      go test -tags integration -run TestSyncBlockMemoryUnderSaturation -v -count=1 ./internal/integration/'
func TestSyncBlockMemoryUnderSaturation(t *testing.T) {
	if os.Getenv("SESAMEFS_MEMORY_PROBE") != "1" {
		t.Skip("set SESAMEFS_MEMORY_PROBE=1 to run the block-PUT saturation measurement")
	}
	requireCassandra(t)

	baseURL := strings.TrimSpace(os.Getenv("SESAMEFS_URL_2"))
	if baseURL == "" {
		t.Skip("SESAMEFS_URL_2 not set; the measurement needs a node running the default caps")
	}

	runID := fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	clients := []*testClient{
		newTestClient(baseURL, "dev-token-admin"),
		newTestClient(baseURL, "dev-token-user"),
	}
	for _, client := range clients {
		client.http.Timeout = 2 * time.Minute
		if err := verifyIntegrationAuth(baseURL, client.token); err != nil {
			t.Fatalf("memory probe auth for token %q: %v", client.token, err)
		}
	}
	if len(clients)*memoryProbeUserCap <= memoryProbeNodeCap {
		t.Fatalf("memory probe identities provide %d admissions, need more than node cap %d",
			len(clients)*memoryProbeUserCap, memoryProbeNodeCap)
	}

	repos := []string{
		createTestLibrary(t, clients[0], "inttest-block-memprobe-admin-"+runID),
		createTestLibrary(t, clients[1], "inttest-block-memprobe-user-"+runID),
	}

	baselineSamples, baseline := waitForStableMemoryBaseline(t, clients[0], runID)
	logMemoryProbeEvent(t, map[string]any{
		"event":            "baseline",
		"run_id":           runID,
		"samples":          len(baselineSamples),
		"rss_bytes":        baseline.rss,
		"go_heap_bytes":    baseline.heap,
		"cgroup_available": baseline.cgroupAvailable,
		"cgroup_bytes":     optionalMetricValue(baseline.cgroupAvailable, baseline.cgroup),
	})

	// Two identities can fill the node cap without either identity exceeding the
	// per-user cap. Extra requests prove that the observed plateau is the node
	// gate, rather than the amount of work the client happened to issue.
	requestCount := memoryProbeNodeCap + len(clients)
	releaseBodies := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseBodies) }) })
	fullBodies := make(chan struct{}, requestCount)
	startRequests := make(chan struct{})
	results := make([]memoryProbeRequestResult, requestCount)
	readers := make([]*heldDeterministicReader, requestCount)

	var requests sync.WaitGroup
	for i := 0; i < requestCount; i++ {
		clientIndex := i % len(clients)
		readers[i] = newHeldDeterministicReader(memoryProbeBodyBytes, fmt.Sprintf("%s-%d", runID, i), fullBodies, releaseBodies)
		requests.Add(1)
		go func(i, clientIndex int) {
			defer requests.Done()
			<-startRequests
			externalID := sha1.Sum([]byte(fmt.Sprintf("%s-%d", runID, i)))
			results[i] = putHeldBlockOnNode(clients[clientIndex], repos[clientIndex], hex.EncodeToString(externalID[:]), readers[i])
		}(i, clientIndex)
	}

	sampler := startMemoryProbeSampler(clients[0], int64(len(baselineSamples)))
	t.Cleanup(func() { _, _ = sampler.stop() })
	close(startRequests)

	waitForFullNodePlateau(t, sampler, fullBodies, memoryProbeNodeCap, 45*time.Second)
	// Let the final network buffers drain into the server while every reader still
	// withholds EOF. This separates ramp-up from the measured full-body plateau.
	time.Sleep(20 * memoryProbeSamplePeriod)
	latest := sampler.latest.Load()
	if latest == nil || latest.inflight != memoryProbeNodeCap {
		observed := float64(0)
		if latest != nil {
			observed = latest.inflight
		}
		t.Fatalf("node cap did not remain held through plateau settling: inflight=%.0f want=%d", observed, memoryProbeNodeCap)
	}
	plateauStart := time.Now()
	// Forty scheduled samples make the plateau long enough to survive scheduler
	// jitter and GC while remaining well below the default admission wait.
	time.Sleep(40 * memoryProbeSamplePeriod)
	plateauEnd := time.Now()
	releaseOnce.Do(func() { close(releaseBodies) })

	requestsDone := make(chan struct{})
	go func() {
		requests.Wait()
		close(requestsDone)
	}()
	select {
	case <-requestsDone:
	case <-time.After(90 * time.Second):
		t.Fatal("memory probe requests did not drain after releasing held bodies")
	}
	allSamples, sampleErrors := sampler.stop()
	if len(sampleErrors) > 0 {
		for _, err := range sampleErrors {
			t.Errorf("memory probe sample failed: %v", err)
		}
		t.FailNow()
	}

	for i, result := range results {
		if result.err != nil {
			t.Errorf("request %d failed: %v", i, result.err)
			continue
		}
		if result.status != http.StatusOK {
			t.Errorf("request %d returned status %d body=%q", i, result.status, result.body)
		}
	}
	if t.Failed() {
		t.FailNow()
	}

	plateauSamples := samplesBetween(allSamples, plateauStart, plateauEnd)
	if len(plateauSamples) < 20 {
		t.Fatalf("only %d valid plateau samples; need at least 20", len(plateauSamples))
	}
	for _, sample := range plateauSamples {
		if sample.inflight != memoryProbeNodeCap {
			t.Fatalf("plateau sample %d observed inflight=%.0f, want node cap %d", sample.sequence, sample.inflight, memoryProbeNodeCap)
		}
	}
	validateOptionalCgroupSeries(t, baselineSamples, plateauSamples)

	if os.Getenv("SESAMEFS_MEMORY_PROBE_SAMPLES") == "1" {
		for _, sample := range append(append([]memoryProbeSample(nil), baselineSamples...), plateauSamples...) {
			logMemoryProbeSample(t, runID, sample)
		}
	}

	rssPeak := peakCorrelatedSample(plateauSamples, func(sample memoryProbeSample) int64 { return sample.rss })
	heapPeak := peakCorrelatedSample(plateauSamples, func(sample memoryProbeSample) int64 { return sample.heap })
	logDerivedMemoryResult(t, runID, "process_rss", rssPeak, baseline.rss, memoryProbeBudgetBytes)
	logDerivedMemoryResult(t, runID, "go_heap_inuse", heapPeak, baseline.heap, memoryProbeBudgetBytes)

	if baseline.cgroupAvailable {
		cgroupPeak := peakCorrelatedSample(plateauSamples, func(sample memoryProbeSample) int64 { return sample.cgroup })
		logDerivedMemoryResult(t, runID, "cgroup_memory_current", cgroupPeak, baseline.cgroup, memoryProbeBudgetBytes)
	} else {
		logMemoryProbeEvent(t, map[string]any{
			"event":    "metric_unavailable",
			"run_id":   runID,
			"metric":   "cgroup_memory_current",
			"fallback": "process_rss",
		})
	}

	waitForMemoryProbeDrain(t, clients[0])
}

type nodeMemoryGauges struct {
	rss             int64
	heap            int64
	inflight        float64
	cgroup          int64
	cgroupAvailable bool
}

type memoryProbeSample struct {
	sequence int64
	at       time.Time
	phase    string
	nodeMemoryGauges
}

type memoryProbeRequestResult struct {
	status int
	body   string
	err    error
}

// heldDeterministicReader emits exactly a full configured body without keeping
// a client-side copy, then withholds EOF. The server's MaxBytesReader attempts
// one more read to prove the chunked body is within the cap, leaving the complete
// io.ReadAll allocation resident until release is closed.
type heldDeterministicReader struct {
	remaining int64
	pattern   [sha256.Size]byte
	offset    int
	full      chan<- struct{}
	release   <-chan struct{}
	signaled  bool
}

func newHeldDeterministicReader(size int64, seed string, full chan<- struct{}, release <-chan struct{}) *heldDeterministicReader {
	return &heldDeterministicReader{remaining: size, pattern: sha256.Sum256([]byte(seed)), full: full, release: release}
}

func (r *heldDeterministicReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		if !r.signaled {
			r.signaled = true
			r.full <- struct{}{}
		}
		<-r.release
		return 0, io.EOF
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	for i := range p {
		p[i] = r.pattern[r.offset%len(r.pattern)]
		r.offset++
	}
	r.remaining -= int64(len(p))
	return len(p), nil
}

func putHeldBlockOnNode(c *testClient, repoID, blockID string, body io.Reader) memoryProbeRequestResult {
	// A legacy SHA-1-shaped ID is accepted without content verification. Payloads
	// remain distinct by construction, so storage deduplication cannot shorten the
	// measured path after the held plateau is released.
	req, err := http.NewRequest(http.MethodPut,
		fmt.Sprintf("%s/seafhttp/repo/%s/block/%s", c.baseURL, repoID, blockID), body)
	if err != nil {
		return memoryProbeRequestResult{err: err}
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Content-Type", "application/octet-stream")
	// Leave ContentLength at -1 so net/http uses chunked transfer and the server
	// must observe EOF before accepting the exact-cap body.
	req.ContentLength = -1

	resp, err := c.http.Do(req)
	if err != nil {
		return memoryProbeRequestResult{err: err}
	}
	defer resp.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return memoryProbeRequestResult{status: resp.StatusCode, body: string(responseBody), err: readErr}
}

type memoryProbeSampler struct {
	client *testClient
	stopCh chan struct{}
	done   chan struct{}
	first  int64

	mu       sync.Mutex
	samples  []memoryProbeSample
	errors   []error
	latest   atomic.Pointer[memoryProbeSample]
	stopOnce sync.Once
}

func startMemoryProbeSampler(client *testClient, firstSequence int64) *memoryProbeSampler {
	s := &memoryProbeSampler{
		client: client,
		stopCh: make(chan struct{}),
		done:   make(chan struct{}),
		first:  firstSequence,
	}
	go s.run()
	return s
}

func (s *memoryProbeSampler) run() {
	defer close(s.done)
	ticker := time.NewTicker(memoryProbeSamplePeriod)
	defer ticker.Stop()
	sequence := s.first
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			at := time.Now()
			gauges, err := scrapeNodeMemoryGauges(s.client)
			if err != nil {
				s.mu.Lock()
				s.errors = append(s.errors, fmt.Errorf("sample %d at %s: %w", sequence, at.UTC().Format(time.RFC3339Nano), err))
				s.mu.Unlock()
				sequence++
				continue
			}
			sample := memoryProbeSample{sequence: sequence, at: at, phase: "load", nodeMemoryGauges: gauges}
			s.mu.Lock()
			s.samples = append(s.samples, sample)
			s.mu.Unlock()
			latest := sample
			s.latest.Store(&latest)
			sequence++
		}
	}
}

func (s *memoryProbeSampler) stop() ([]memoryProbeSample, []error) {
	s.stopOnce.Do(func() { close(s.stopCh) })
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]memoryProbeSample(nil), s.samples...), append([]error(nil), s.errors...)
}

func waitForStableMemoryBaseline(t *testing.T, client *testClient, runID string) ([]memoryProbeSample, nodeMemoryGauges) {
	t.Helper()

	const stableSampleCount = 20
	deadline := time.Now().Add(20 * time.Second)
	ticker := time.NewTicker(memoryProbeSamplePeriod)
	defer ticker.Stop()

	var samples []memoryProbeSample
	var sequence int64
	for time.Now().Before(deadline) {
		<-ticker.C
		at := time.Now()
		gauges, err := scrapeNodeMemoryGauges(client)
		if err != nil {
			t.Fatalf("baseline sample %d failed closed: %v", sequence, err)
		}
		sample := memoryProbeSample{sequence: sequence, at: at, phase: "baseline", nodeMemoryGauges: gauges}
		samples = append(samples, sample)
		sequence++
		if len(samples) < stableSampleCount {
			continue
		}

		window := samples[len(samples)-stableSampleCount:]
		baseline, stable := stableBaseline(window)
		if stable {
			return append([]memoryProbeSample(nil), window...), baseline
		}
	}
	logMemoryProbeEvent(t, map[string]any{"event": "baseline_unstable", "run_id": runID, "samples": len(samples)})
	t.Fatalf("memory metrics did not produce a stable idle baseline within 20s")
	return nil, nodeMemoryGauges{}
}

func stableBaseline(samples []memoryProbeSample) (nodeMemoryGauges, bool) {
	baseline := nodeMemoryGauges{
		rss:             medianMetric(samples, func(s memoryProbeSample) int64 { return s.rss }),
		heap:            medianMetric(samples, func(s memoryProbeSample) int64 { return s.heap }),
		cgroupAvailable: samples[0].cgroupAvailable,
	}
	if baseline.cgroupAvailable {
		baseline.cgroup = medianMetric(samples, func(s memoryProbeSample) int64 { return s.cgroup })
	}
	for _, sample := range samples {
		if sample.inflight != 0 || sample.cgroupAvailable != baseline.cgroupAvailable {
			return nodeMemoryGauges{}, false
		}
	}
	if metricSpread(samples, func(s memoryProbeSample) int64 { return s.rss }) > stabilityTolerance(baseline.rss) ||
		metricSpread(samples, func(s memoryProbeSample) int64 { return s.heap }) > stabilityTolerance(baseline.heap) {
		return nodeMemoryGauges{}, false
	}
	if baseline.cgroupAvailable && metricSpread(samples, func(s memoryProbeSample) int64 { return s.cgroup }) > stabilityTolerance(baseline.cgroup) {
		return nodeMemoryGauges{}, false
	}
	return baseline, true
}

func stabilityTolerance(value int64) int64 {
	const minimum = int64(8 * 1024 * 1024)
	if onePercent := value / 100; onePercent > minimum {
		return onePercent
	}
	return minimum
}

func medianMetric(samples []memoryProbeSample, metric func(memoryProbeSample) int64) int64 {
	values := make([]int64, len(samples))
	for i, sample := range samples {
		values[i] = metric(sample)
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return values[len(values)/2]
}

func metricSpread(samples []memoryProbeSample, metric func(memoryProbeSample) int64) int64 {
	minimum, maximum := metric(samples[0]), metric(samples[0])
	for _, sample := range samples[1:] {
		value := metric(sample)
		if value < minimum {
			minimum = value
		}
		if value > maximum {
			maximum = value
		}
	}
	return maximum - minimum
}

func waitForFullNodePlateau(t *testing.T, sampler *memoryProbeSampler, fullBodies <-chan struct{}, want int, timeout time.Duration) {
	t.Helper()

	deadline := time.NewTimer(timeout)
	poll := time.NewTicker(memoryProbeSamplePeriod)
	defer deadline.Stop()
	defer poll.Stop()
	fullCount := 0
	for {
		select {
		case <-fullBodies:
			fullCount++
		case <-poll.C:
			latest := sampler.latest.Load()
			if fullCount >= want && latest != nil && latest.inflight == float64(want) {
				return
			}
		case <-deadline.C:
			latest := sampler.latest.Load()
			observed := float64(0)
			if latest != nil {
				observed = latest.inflight
			}
			t.Fatalf("timed out filling node plateau: full_bodies=%d observed_inflight=%.0f want=%d", fullCount, observed, want)
		}
	}
}

func samplesBetween(samples []memoryProbeSample, start, end time.Time) []memoryProbeSample {
	var selected []memoryProbeSample
	for _, sample := range samples {
		if !sample.at.Before(start) && !sample.at.After(end) {
			sample.phase = "plateau"
			selected = append(selected, sample)
		}
	}
	return selected
}

func peakCorrelatedSample(samples []memoryProbeSample, metric func(memoryProbeSample) int64) memoryProbeSample {
	peak := samples[0]
	for _, sample := range samples[1:] {
		if metric(sample) > metric(peak) {
			peak = sample
		}
	}
	return peak
}

func validateOptionalCgroupSeries(t *testing.T, baseline, plateau []memoryProbeSample) {
	t.Helper()
	want := baseline[0].cgroupAvailable
	for _, sample := range append(append([]memoryProbeSample(nil), baseline...), plateau...) {
		if sample.cgroupAvailable != want {
			t.Fatalf("cgroup current metric availability changed during measurement at sample %d", sample.sequence)
		}
	}
}

func logDerivedMemoryResult(t *testing.T, runID, metric string, peak memoryProbeSample, baseline, budget int64) {
	t.Helper()
	delta := peakMetricValue(metric, peak) - baseline
	if delta <= 0 || peak.inflight <= 0 {
		t.Fatalf("%s did not grow over baseline: baseline=%d peak=%d inflight=%.0f", metric, baseline, peakMetricValue(metric, peak), peak.inflight)
	}
	inflight := int64(peak.inflight)
	perAdmission := ceilDiv(delta, inflight)
	withSafety := ceilDiv(perAdmission*memoryProbeSafetyNum, memoryProbeSafetyDen)
	safeCap := budget / withSafety
	logMemoryProbeEvent(t, map[string]any{
		"event":                         "derived_result",
		"run_id":                        runID,
		"metric":                        metric,
		"baseline_bytes":                baseline,
		"peak_bytes":                    peakMetricValue(metric, peak),
		"peak_sample_sequence":          peak.sequence,
		"correlated_rss_bytes":          peak.rss,
		"correlated_go_heap_bytes":      peak.heap,
		"correlated_cgroup_bytes":       optionalMetricValue(peak.cgroupAvailable, peak.cgroup),
		"correlated_inflight":           peak.inflight,
		"baseline_subtracted_bytes":     delta,
		"bytes_per_admission_ceiling":   perAdmission,
		"safety_factor_numerator":       memoryProbeSafetyNum,
		"safety_factor_denominator":     memoryProbeSafetyDen,
		"safe_bytes_per_admission":      withSafety,
		"memory_budget_bytes":           budget,
		"calculated_cap_floor":          safeCap,
		"configured_node_cap":           memoryProbeNodeCap,
		"configured_cap_budgeted_bytes": withSafety * int64(memoryProbeNodeCap),
	})
}

func peakMetricValue(metric string, sample memoryProbeSample) int64 {
	switch metric {
	case "process_rss":
		return sample.rss
	case "go_heap_inuse":
		return sample.heap
	case "cgroup_memory_current":
		return sample.cgroup
	default:
		panic("unknown memory probe metric: " + metric)
	}
}

func ceilDiv(numerator, denominator int64) int64 {
	return (numerator + denominator - 1) / denominator
}

func optionalMetricValue(available bool, value int64) any {
	if !available {
		return nil
	}
	return value
}

func logMemoryProbeSample(t *testing.T, runID string, sample memoryProbeSample) {
	t.Helper()
	logMemoryProbeEvent(t, map[string]any{
		"event":            "sample",
		"run_id":           runID,
		"phase":            sample.phase,
		"sequence":         sample.sequence,
		"timestamp":        sample.at.UTC().Format(time.RFC3339Nano),
		"rss_bytes":        sample.rss,
		"go_heap_bytes":    sample.heap,
		"inflight":         sample.inflight,
		"cgroup_available": sample.cgroupAvailable,
		"cgroup_bytes":     optionalMetricValue(sample.cgroupAvailable, sample.cgroup),
	})
}

func logMemoryProbeEvent(t *testing.T, fields map[string]any) {
	t.Helper()
	encoded, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal memory probe log: %v", err)
	}
	t.Logf("MEMORY_PROBE_JSON %s", encoded)
}

func waitForMemoryProbeDrain(t *testing.T, client *testClient) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for time.Now().Before(deadline) {
		gauges, err := scrapeNodeMemoryGauges(client)
		if err != nil {
			t.Fatalf("drain sample failed closed: %v", err)
		}
		if gauges.inflight == 0 {
			return
		}
		<-ticker.C
	}
	gauges, err := scrapeNodeMemoryGauges(client)
	if err != nil {
		t.Fatalf("final drain sample failed closed: %v", err)
	}
	t.Fatalf("sync_put_block_inflight_current stayed at %.0f after requests drained", gauges.inflight)
}

// scrapeNodeMemoryGauges reads one correlated tuple from the node's /metrics.
// RSS and Go heap are required. A future node may expose either supported
// cgroup-current name; until then it remains explicitly unavailable and RSS is
// the production-relevant process-memory fallback.
func scrapeNodeMemoryGauges(c *testClient) (nodeMemoryGauges, error) {
	var gauges nodeMemoryGauges
	var foundRSS, foundHeap, foundInflight bool

	req, err := http.NewRequest(http.MethodGet, c.baseURL+"/metrics", nil)
	if err != nil {
		return gauges, err
	}
	req.Header.Set("Accept-Encoding", "identity")

	resp, err := c.http.Do(req)
	if err != nil {
		return gauges, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return gauges, fmt.Errorf("GET /metrics = %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := fields[0]
		if brace := strings.IndexByte(name, '{'); brace >= 0 {
			name = name[:brace]
		}
		targetMetric := name == "process_resident_memory_bytes" ||
			name == "go_memstats_heap_inuse_bytes" ||
			name == "sync_put_block_inflight_current" ||
			name == "process_cgroup_memory_current_bytes" ||
			name == "cgroup_memory_current_bytes"
		if !targetMetric {
			continue
		}
		value, parseErr := strconv.ParseFloat(fields[1], 64)
		if parseErr != nil {
			return nodeMemoryGauges{}, fmt.Errorf("parse metric %s value %q: %w", name, fields[1], parseErr)
		}
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return nodeMemoryGauges{}, fmt.Errorf("metric %s has non-finite value %q", name, fields[1])
		}
		switch name {
		case "process_resident_memory_bytes":
			gauges.rss, foundRSS = int64(value), true
		case "go_memstats_heap_inuse_bytes":
			gauges.heap, foundHeap = int64(value), true
		case "sync_put_block_inflight_current":
			gauges.inflight, foundInflight = value, true
		case "process_cgroup_memory_current_bytes", "cgroup_memory_current_bytes":
			gauges.cgroup, gauges.cgroupAvailable = int64(value), true
		}
	}
	if err := scanner.Err(); err != nil {
		return nodeMemoryGauges{}, err
	}
	if !foundRSS || !foundHeap || !foundInflight {
		return nodeMemoryGauges{}, fmt.Errorf("required metrics missing: process_rss=%t go_heap=%t sync_inflight=%t", foundRSS, foundHeap, foundInflight)
	}
	if gauges.rss <= 0 || gauges.heap <= 0 || gauges.inflight < 0 || math.Trunc(gauges.inflight) != gauges.inflight || (gauges.cgroupAvailable && gauges.cgroup <= 0) {
		return nodeMemoryGauges{}, fmt.Errorf("invalid metric tuple: rss=%d heap=%d inflight=%v cgroup=%d available=%t",
			gauges.rss, gauges.heap, gauges.inflight, gauges.cgroup, gauges.cgroupAvailable)
	}
	return gauges, nil
}
