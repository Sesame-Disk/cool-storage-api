//go:build integration

package integration

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Subcontract C (= registry X11) of ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01, at the
// HTTP boundary and against real Cassandra and MinIO.
//
// The unit suite in internal/api proves the mechanics against a hand-built
// router with stubbed lookups. This one proves the guard is reachable in a real
// process — real route registration, real sync auth, real config load — and that
// the lookups it bounds are the real ones. A wiring regression that constructed
// the limiter but never consulted it would pass every unit test and fail here.
//
// It runs against node 3, which docker-compose starts with deliberately tiny
// check-blocks caps and a short admission wait.
const (
	// Must match the values docker-compose sets on sesamefs-node-3.
	checkBlocksTestNodeCap = 2
	checkBlocksTestWait    = 250 * time.Millisecond
)

// postCheckBlocks issues one authenticated check-blocks request and reports the
// status plus any Retry-After it advertised.
func postCheckBlocks(c *testClient, repoID string, body io.Reader) (int, string, error) {
	req, err := http.NewRequest(http.MethodPost,
		fmt.Sprintf("%s/seafhttp/repo/%s/check-blocks", c.baseURL, repoID), body)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, resp.Header.Get("Retry-After"), nil
}

// scrapeCheckBlocksGauge reads the in-flight gauge for this route. Reading the
// gauge rather than inferring occupancy from timing is what makes the saturation
// tests deterministic.
func scrapeCheckBlocksGauge(c *testClient, metric string) (float64, error) {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+"/metrics", nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return 0, fmt.Errorf("GET /metrics = %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != metric {
			continue
		}
		return strconv.ParseFloat(fields[1], 64)
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return 0, fmt.Errorf("metric %s not exported; the series is the only visibility this guard has", metric)
}

func waitForCheckBlocksInflight(t *testing.T, c *testClient, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last float64
	for time.Now().Before(deadline) {
		value, err := scrapeCheckBlocksGauge(c, "sync_check_blocks_inflight_current")
		if err == nil {
			last = value
			if int(value) == want {
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("sync_check_blocks_inflight_current = %.0f, want %d", last, want)
}

// checkBlocksHeldBody parks the server inside its body read, which is where an
// admitted check-blocks request spends its slot before doing any lookup.
type checkBlocksHeldBody struct {
	started chan<- struct{}
	release <-chan struct{}
	once    sync.Once
}

func (b *checkBlocksHeldBody) Read(p []byte) (int, error) {
	b.once.Do(func() { b.started <- struct{}{} })
	<-b.release
	if len(p) == 0 {
		return 0, io.EOF
	}
	p[0] = '\n'
	return 1, io.EOF
}

// runHeldCheckBlocks occupies n admissions and returns the release func.
func runHeldCheckBlocks(t *testing.T, holders []struct {
	client *testClient
	repoID string
}) func() {
	t.Helper()
	started := make(chan struct{}, len(holders))
	release := make(chan struct{})
	var wg sync.WaitGroup
	for i, holder := range holders {
		wg.Add(1)
		go func(i int, holder struct {
			client *testClient
			repoID string
		}) {
			defer wg.Done()
			body := &checkBlocksHeldBody{started: started, release: release}
			if _, _, err := postCheckBlocks(holder.client, holder.repoID, body); err != nil {
				t.Errorf("held check-blocks %d: %v", i, err)
			}
		}(i, holder)
	}
	for range holders {
		select {
		case <-started:
		case <-time.After(10 * time.Second):
			close(release)
			wg.Wait()
			t.Fatal("held check-blocks never reached the server body read, so it never held an admission")
		}
	}
	return func() {
		close(release)
		wg.Wait()
	}
}

// TestCheckBlocksAdmissionRefusesWith503UnderSaturation pins the contract the
// desktop sync client depends on: overflow is refused 503 with a Retry-After,
// never 429, and only after the bounded wait rather than immediately.
func TestCheckBlocksAdmissionRefusesWith503UnderSaturation(t *testing.T) {
	requireCassandra(t)
	admin := admissionTestClient(t)
	user := newTestClient(admin.baseURL, "dev-token-user")
	if err := verifyIntegrationAuth(admin.baseURL, user.token); err != nil {
		t.Fatalf("secondary user auth probe: %v", err)
	}
	adminRepo := createTestLibrary(t, admin, fmt.Sprintf("inttest-check-blocks-admin-%d", time.Now().UnixNano()))
	userRepo := createTestLibrary(t, user, fmt.Sprintf("inttest-check-blocks-user-%d", time.Now().UnixNano()))

	assertOverflow := func(t *testing.T, client *testClient, repoID string) {
		t.Helper()
		started := time.Now()
		code, retryAfter, err := postCheckBlocks(client, repoID, strings.NewReader(`[]`))
		if err != nil {
			t.Fatalf("overflow request: %v", err)
		}
		if code == http.StatusTooManyRequests {
			t.Fatal("overflow returned 429; the desktop client only retries 503")
		}
		if code != http.StatusServiceUnavailable {
			t.Fatalf("overflow returned %d, want 503", code)
		}
		seconds, err := strconv.Atoi(retryAfter)
		if err != nil || seconds < 1 {
			t.Fatalf("Retry-After = %q, want a positive integer", retryAfter)
		}
		if elapsed := time.Since(started); elapsed < checkBlocksTestWait/2 {
			t.Fatalf("overflow rejected after %s, want the bounded wait near %s", elapsed, checkBlocksTestWait)
		}
	}

	t.Run("per-user gate", func(t *testing.T) {
		release := runHeldCheckBlocks(t, []struct {
			client *testClient
			repoID string
		}{{admin, adminRepo}, {admin, adminRepo}})
		waitForCheckBlocksInflight(t, admin, checkBlocksTestNodeCap)
		assertOverflow(t, admin, adminRepo)
		release()
		waitForCheckBlocksInflight(t, admin, 0)
	})

	t.Run("node gate across users", func(t *testing.T) {
		release := runHeldCheckBlocks(t, []struct {
			client *testClient
			repoID string
		}{{admin, adminRepo}, {user, userRepo}})
		waitForCheckBlocksInflight(t, admin, checkBlocksTestNodeCap)
		assertOverflow(t, admin, adminRepo)
		release()
		waitForCheckBlocksInflight(t, admin, 0)
	})
}

// TestCheckBlocksAdmissionIsSeparateFromBlockUploads is the cross-route claim
// that cannot be made in a unit test: with check-blocks saturated on a real
// node, block uploads must keep flowing. Sharing one budget between the two
// would turn a burst of cheap metadata requests into an upload outage.
func TestCheckBlocksAdmissionIsSeparateFromBlockUploads(t *testing.T) {
	requireCassandra(t)
	client := admissionTestClient(t)
	repoID := createTestLibrary(t, client, fmt.Sprintf("inttest-check-blocks-isolation-%d", time.Now().UnixNano()))

	release := runHeldCheckBlocks(t, []struct {
		client *testClient
		repoID string
	}{{client, repoID}, {client, repoID}})
	defer release()
	waitForCheckBlocksInflight(t, client, checkBlocksTestNodeCap)

	payload := []byte(fmt.Sprintf("upload during check-blocks saturation %d", time.Now().UnixNano()))
	code, _, err := putBlockOnNode(client, repoID, syncSHA256HexForTest(payload), payload)
	if err != nil {
		t.Fatalf("block upload during check-blocks saturation: %v", err)
	}
	if code != http.StatusOK {
		t.Fatalf("block upload during check-blocks saturation = %d, want 200; the two routes must hold separate capacity", code)
	}
}

// TestCheckBlocksDeduplicatesLookups drives the real Cassandra path with a list
// of one id repeated many times and reads the lookup counter across the request.
// Before deduplication that body cost one point read per copy, which is the
// cheapest way there was to turn a small request into six figures of database
// work.
func TestCheckBlocksDeduplicatesLookups(t *testing.T) {
	requireCassandra(t)
	client := admissionTestClient(t)
	repoID := createTestLibrary(t, client, fmt.Sprintf("inttest-check-blocks-dedup-%d", time.Now().UnixNano()))

	const repeats = 200
	// A legacy SHA-1 id: this is the shape the desktop client sends, and the only
	// one that reaches the mapping table at all.
	legacyID := strings.Repeat("a", 39) + "b"
	ids := make([]string, repeats)
	for i := range ids {
		ids[i] = fmt.Sprintf("%q", legacyID)
	}
	body := "[" + strings.Join(ids, ",") + "]"

	// A counter with no observations yet is absent from /metrics rather than
	// zero, and on a fresh node that is the normal state.
	before, err := scrapeCheckBlocksGauge(client, `sync_check_blocks_lookups_total{phase="mapping"}`)
	if err != nil && !strings.Contains(err.Error(), "not exported") {
		t.Fatalf("scrape mapping lookups before: %v", err)
	}

	code, _, err := postCheckBlocks(client, repoID, strings.NewReader(body))
	if err != nil {
		t.Fatalf("check-blocks: %v", err)
	}
	if code != http.StatusOK {
		t.Fatalf("check-blocks = %d, want 200", code)
	}

	after, err := scrapeCheckBlocksGauge(client, `sync_check_blocks_lookups_total{phase="mapping"}`)
	if err != nil {
		t.Fatalf("scrape mapping lookups after: %v", err)
	}
	if delta := after - before; delta != 1 {
		t.Fatalf("mapping lookups = %.0f for %d copies of one id, want 1", delta, repeats)
	}
}

// TestCheckBlocksReleasesSlotsAfterBurst is the leak check at process level: a
// stranded admission would not fail the tests above, it would quietly shrink the
// node's capacity after every spike.
func TestCheckBlocksReleasesSlotsAfterBurst(t *testing.T) {
	requireCassandra(t)
	client := admissionTestClient(t)
	repoID := createTestLibrary(t, client, fmt.Sprintf("inttest-check-blocks-drain-%d", time.Now().UnixNano()))

	const concurrency = 16
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := fmt.Sprintf(`["%s"]`, strings.Repeat("c", 63)+strconv.Itoa(i%10))
			_, _, _ = postCheckBlocks(client, repoID, strings.NewReader(body))
		}(i)
	}
	wg.Wait()

	waitForCheckBlocksInflight(t, client, 0)

	for i := 0; i < checkBlocksTestNodeCap+1; i++ {
		started := time.Now()
		code, _, err := postCheckBlocks(client, repoID, strings.NewReader(`[]`))
		if err != nil {
			t.Fatalf("probe %d failed: %v", i, err)
		}
		if code != http.StatusOK {
			t.Fatalf("probe %d after the burst = %d, want 200; the burst stranded an admission", i, code)
		}
		if waited := time.Since(started); waited > checkBlocksTestWait {
			t.Fatalf("probe %d waited %s for admission on an idle node; slots are leaking", i, waited)
		}
	}
}
