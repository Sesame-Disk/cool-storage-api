//go:build integration

package integration

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Step 5.2 of subcontract B: the end-to-end half of the memory measurement that
// sizes seafhttp.sync_block_max_inflight_per_node.
//
// BenchmarkPutBlockBodyMemory in internal/api measures one request in isolation
// (~19.7 MiB retained for a 16 MiB body). That number is only half an argument:
// it says what one admission holds, not what a whole process costs while several
// are resident and the collector is running. This drives a real node with
// maximum-size bodies and reads its own metrics to get the other half:
//
//	heap cost per admission ~= peak go_memstats_heap_inuse_bytes / peak sync_put_block_inflight_current
//
// That quotient is the divisor in
//
//	node cap = (memory budget for block PUT) / (heap cost per admission)
//
// and re-running this is how to redo that division after changing
// sync_block_max_bytes, the storage backend, or the Go version.
//
// It is opt-in because it is a measurement, not an assertion: it pushes hundreds
// of megabytes through the node, its numbers depend on the host, and a threshold
// tight enough to be meaningful would be flaky in CI. Run it deliberately:
//
//	docker compose --profile test run --rm --build --entrypoint sh go-integration-test \
//	  -c 'export PATH=$PATH:/usr/local/go/bin && SESAMEFS_MEMORY_PROBE=1 \
//	      go test -tags integration -run TestSyncBlockMemoryUnderSaturation -v -count=1 ./internal/integration/'
func TestSyncBlockMemoryUnderSaturation(t *testing.T) {
	if os.Getenv("SESAMEFS_MEMORY_PROBE") != "1" {
		t.Skip("set SESAMEFS_MEMORY_PROBE=1 to run the block-PUT saturation measurement")
	}
	requireCassandra(t)

	// Node 2 runs the shipped defaults, which is the configuration the numbers
	// need to describe. Node 3 is deliberately squeezed and would measure the
	// fixture instead.
	baseURL := strings.TrimSpace(os.Getenv("SESAMEFS_URL_2"))
	if baseURL == "" {
		t.Skip("SESAMEFS_URL_2 not set; the measurement needs a node running the default caps")
	}
	client := newTestClient(baseURL, "dev-token-admin")
	client.http.Timeout = 2 * time.Minute

	repoID := createTestLibrary(t, client, fmt.Sprintf("inttest-block-memprobe-%d", time.Now().UnixNano()))

	const (
		bodyBytes   = 16 * 1024 * 1024 // the configured per-request cap
		concurrency = 24               // above the per-user cap, so the gate is genuinely contended
	)

	base := readNodeMemoryGauges(t, client)
	t.Logf("before: rss=%s heap=%s inflight=%.0f", mib(base.rss), mib(base.heap), base.inflight)

	var peak nodeMemoryGauges
	var sampling atomic.Bool
	sampling.Store(true)
	var sampler sync.WaitGroup
	sampler.Add(1)
	go func() {
		defer sampler.Done()
		for sampling.Load() {
			s := readNodeMemoryGaugesQuiet(client)
			if s.rss > peak.rss {
				peak.rss = s.rss
			}
			if s.heap > peak.heap {
				peak.heap = s.heap
			}
			if s.inflight > peak.inflight {
				peak.inflight = s.inflight
			}
		}
	}()

	var wg sync.WaitGroup
	var admitted, refused int64
	start := make(chan struct{})
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Random tails keep dedup from collapsing the work; the bodies must
			// actually be buffered and hashed for the measurement to mean anything.
			payload := make([]byte, bodyBytes)
			if _, err := rand.Read(payload[:4096]); err != nil {
				t.Errorf("payload %d: %v", i, err)
				return
			}
			<-start
			code, _, err := putBlockOnNode(client, repoID, syncSHA256HexForTest(payload), payload)
			if err != nil {
				t.Errorf("request %d: %v", i, err)
				return
			}
			switch code {
			case http.StatusOK:
				atomic.AddInt64(&admitted, 1)
			case http.StatusServiceUnavailable:
				atomic.AddInt64(&refused, 1)
			default:
				t.Errorf("request %d = %d", i, code)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	sampling.Store(false)
	sampler.Wait()

	after := readNodeMemoryGauges(t, client)

	t.Logf("peak:   rss=%s heap=%s max_inflight=%.0f", mib(peak.rss), mib(peak.heap), peak.inflight)
	t.Logf("after:  rss=%s heap=%s inflight=%.0f", mib(after.rss), mib(after.heap), after.inflight)
	t.Logf("outcome: %d admitted, %d refused 503 of %d at %s bodies", admitted, refused, concurrency, mib(bodyBytes))

	if peak.inflight > 0 {
		t.Logf("DERIVED heap cost per admission = %s (peak heap %s / %.0f concurrent admissions)",
			mib(int64(float64(peak.heap)/peak.inflight)), mib(peak.heap), peak.inflight)
		t.Logf("DERIVED rss growth per admission = %s over baseline",
			mib(int64(float64(peak.rss-base.rss)/peak.inflight)))
	} else {
		t.Log("sampler never observed a non-zero in-flight gauge; the burst finished between scrapes")
	}

	// The one hard assertion: whatever the numbers, admissions must not be
	// stranded. A gauge that stays up after the burst is a leak, and every cap
	// derived from these measurements would decay with it.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if readNodeMemoryGaugesQuiet(client).inflight == 0 {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("sync_put_block_inflight_current stayed at %.0f after the burst drained; admissions are leaking",
		readNodeMemoryGaugesQuiet(client).inflight)
}

type nodeMemoryGauges struct {
	rss      int64
	heap     int64
	inflight float64
}

func mib(bytes int64) string {
	return fmt.Sprintf("%.1f MiB", float64(bytes)/(1024*1024))
}

func readNodeMemoryGauges(t *testing.T, c *testClient) nodeMemoryGauges {
	t.Helper()
	g, err := scrapeNodeMemoryGauges(c)
	if err != nil {
		t.Fatalf("scrape /metrics: %v", err)
	}
	return g
}

func readNodeMemoryGaugesQuiet(c *testClient) nodeMemoryGauges {
	g, _ := scrapeNodeMemoryGauges(c)
	return g
}

// scrapeNodeMemoryGauges reads the three series this measurement needs straight
// off the node's own /metrics. The endpoint is InternalOnly, which the compose
// network satisfies: the test container's address is private.
func scrapeNodeMemoryGauges(c *testClient) (nodeMemoryGauges, error) {
	var g nodeMemoryGauges

	req, err := http.NewRequest(http.MethodGet, c.baseURL+"/metrics", nil)
	if err != nil {
		return g, err
	}
	// Ask for an uncompressed body explicitly. /metrics currently comes back
	// double-gzipped when the client negotiates compression — promhttp does its
	// own gzip and the engine's gzip middleware does not exclude this path — so
	// Go's transport strips one layer and leaves the other, which no metrics
	// parser can read. That is a separate defect from subcontract B; this
	// measurement just refuses to depend on it.
	req.Header.Set("Accept-Encoding", "identity")

	resp, err := c.http.Do(req)
	if err != nil {
		return g, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return g, fmt.Errorf("GET /metrics = %d (InternalOnly rejects non-private client addresses)", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return g, err
	}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			continue
		}
		switch name {
		case "process_resident_memory_bytes":
			g.rss = int64(parsed)
		case "go_memstats_heap_inuse_bytes":
			g.heap = int64(parsed)
		case "sync_put_block_inflight_current":
			g.inflight = parsed
		}
	}
	return g, scanner.Err()
}
