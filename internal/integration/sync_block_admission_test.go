//go:build integration

package integration

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Subcontract B (= registry X10) of ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01, at the
// HTTP boundary.
//
// The unit suite in internal/api proves the limiter's mechanics against a
// hand-built router. This one proves the guard is actually reachable in a real
// process: through the real route registration, the real sync auth middleware,
// and a real config load. A wiring regression that constructed the limiter but
// never consulted it, or a route group that lost the handler, would pass every
// unit test and fail here.
//
// It runs against node 3, which docker-compose starts with deliberately tiny
// caps (SEAFHTTP_SYNC_BLOCK_MAX_INFLIGHT_PER_NODE / _PER_USER) and a short
// admission wait. Saturating the shared node would make every other integration
// test flaky, which is why the squeezed configuration is confined to one
// instance.
const (
	// Must match the values docker-compose sets on sesamefs-node-3.
	admissionTestNodeCap = 2
	admissionTestWait    = 250 * time.Millisecond

	// Sized so one admission is slow enough that a burst cannot drain inside the
	// wait. This matters more than it looks: at the production 10s wait, a block
	// against local MinIO clears in tens of milliseconds and a burst of any size
	// a test can issue simply queues and completes — which is the bounded wait
	// doing its job, not the gate failing to fire. Reaching the refusal path in a
	// test needs the wait squeezed and the work per slot non-trivial.
	admissionTestBlockLen = 1024 * 1024
)

func admissionTestClient(t *testing.T) *testClient {
	t.Helper()

	baseURL := strings.TrimSpace(os.Getenv("SESAMEFS_URL_3"))
	if baseURL == "" {
		t.Skip("SESAMEFS_URL_3 not set; the squeezed-admission node is only started by the compose test profile")
	}
	if err := verifyIntegrationAuth(baseURL, "dev-token-admin"); err != nil {
		t.Fatalf("SESAMEFS_URL_3 auth probe failed: %v", err)
	}
	return newTestClient(baseURL, "dev-token-admin")
}

// putBlockOnNode issues one authenticated block PUT and reports the status plus
// the Retry-After it advertised.
func putBlockOnNode(c *testClient, repoID, blockID string, payload []byte) (int, string, error) {
	req, err := http.NewRequest(http.MethodPut,
		fmt.Sprintf("%s/seafhttp/repo/%s/block/%s", c.baseURL, repoID, blockID),
		bytes.NewReader(payload))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, resp.Header.Get("Retry-After"), nil
}

type admissionHeldBody struct {
	started chan<- struct{}
	release <-chan struct{}
	value   byte
	once    sync.Once
}

func (b *admissionHeldBody) Read(p []byte) (int, error) {
	b.once.Do(func() { b.started <- struct{}{} })
	<-b.release
	if len(p) == 0 {
		return 0, io.EOF
	}
	p[0] = b.value
	return 1, io.EOF
}

func putHeldAdmissionBlock(c *testClient, repoID, blockID string, body io.Reader) (int, error) {
	req, err := http.NewRequest(http.MethodPut,
		fmt.Sprintf("%s/seafhttp/repo/%s/block/%s", c.baseURL, repoID, blockID), body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

func waitForAdmissionInflight(t *testing.T, c *testClient, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		gauges, err := scrapeNodeMemoryGauges(c)
		if err == nil && int(gauges.inflight) == want {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	gauges, err := scrapeNodeMemoryGauges(c)
	if err != nil {
		t.Fatalf("scrape admission gauge: %v", err)
	}
	t.Fatalf("inflight = %.0f, want %d", gauges.inflight, want)
}

func runHeldAdmissions(t *testing.T, holders []struct {
	client *testClient
	repoID string
}) func() {
	t.Helper()
	started := make(chan struct{}, len(holders))
	release := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, len(holders))
	for i, holder := range holders {
		wg.Add(1)
		go func(i int, holder struct {
			client *testClient
			repoID string
		}) {
			defer wg.Done()
			value := byte(i + 1)
			body := &admissionHeldBody{started: started, release: release, value: value}
			code, err := putHeldAdmissionBlock(holder.client, holder.repoID, syncSHA256HexForTest([]byte{value}), body)
			if err != nil {
				errs <- err
				return
			}
			if code != http.StatusOK {
				errs <- fmt.Errorf("holder %d returned %d", i, code)
			}
		}(i, holder)
	}
	for range holders {
		select {
		case <-started:
		case <-time.After(10 * time.Second):
			close(release)
			wg.Wait()
			t.Fatal("holder did not reach the server body read")
		}
	}
	return func() {
		close(release)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Errorf("held admission: %v", err)
			}
		}
	}
}

// TestSyncBlockAdmissionRefusesWith503UnderSaturation drives more concurrent
// block PUTs than the node will admit and pins the contract the desktop sync
// client depends on: the overflow is refused 503 with a Retry-After, and never
// 429, which that client does not treat as retryable.
func TestSyncBlockAdmissionRefusesWith503UnderSaturation(t *testing.T) {
	requireCassandra(t)
	admin := admissionTestClient(t)
	user := newTestClient(admin.baseURL, "dev-token-user")
	if err := verifyIntegrationAuth(admin.baseURL, user.token); err != nil {
		t.Fatalf("secondary user auth probe: %v", err)
	}
	adminRepo := createTestLibrary(t, admin, fmt.Sprintf("inttest-block-admission-admin-%d", time.Now().UnixNano()))
	userRepo := createTestLibrary(t, user, fmt.Sprintf("inttest-block-admission-user-%d", time.Now().UnixNano()))

	assertOverflow := func(t *testing.T, client *testClient, repoID string) {
		t.Helper()
		payload := []byte("deterministic overflow")
		started := time.Now()
		code, retryAfter, err := putBlockOnNode(client, repoID, syncSHA256HexForTest(payload), payload)
		if err != nil {
			t.Fatalf("overflow request: %v", err)
		}
		if code == http.StatusTooManyRequests {
			t.Fatal("overflow returned 429; desktop client only retries 503")
		}
		if code != http.StatusServiceUnavailable {
			t.Fatalf("overflow returned %d, want 503", code)
		}
		seconds, err := strconv.Atoi(retryAfter)
		if err != nil || seconds < 1 {
			t.Fatalf("Retry-After = %q, want positive integer", retryAfter)
		}
		if elapsed := time.Since(started); elapsed < admissionTestWait/2 {
			t.Fatalf("overflow rejected after %s, want bounded wait near %s", elapsed, admissionTestWait)
		}
	}

	t.Run("per-user gate", func(t *testing.T) {
		release := runHeldAdmissions(t, []struct {
			client *testClient
			repoID string
		}{{admin, adminRepo}, {admin, adminRepo}})
		waitForAdmissionInflight(t, admin, admissionTestNodeCap)
		assertOverflow(t, admin, adminRepo)
		release()
		waitForAdmissionInflight(t, admin, 0)
	})

	t.Run("node gate across users", func(t *testing.T) {
		release := runHeldAdmissions(t, []struct {
			client *testClient
			repoID string
		}{{admin, adminRepo}, {user, userRepo}})
		waitForAdmissionInflight(t, admin, admissionTestNodeCap)
		assertOverflow(t, admin, adminRepo)
		release()
		waitForAdmissionInflight(t, admin, 0)
	})
}

// TestSyncBlockAdmissionWaitAbsorbsBurstAtCap is the other half of the contract
// and the reason the gate waits instead of refusing outright: traffic that fits
// the cap must pass untouched. A limiter that refused here would be punishing
// exactly the honest desktop client the bounded wait exists to protect.
func TestSyncBlockAdmissionWaitAbsorbsBurstAtCap(t *testing.T) {
	requireCassandra(t)
	client := admissionTestClient(t)

	repoID := createTestLibrary(t, client, fmt.Sprintf("inttest-block-admission-absorb-%d", time.Now().UnixNano()))

	var wg sync.WaitGroup
	codes := make([]int, admissionTestNodeCap)
	for i := 0; i < admissionTestNodeCap; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			payload := []byte(fmt.Sprintf("at-cap admission %d %d", i, time.Now().UnixNano()))
			code, _, err := putBlockOnNode(client, repoID, syncSHA256HexForTest(payload), payload)
			if err != nil {
				t.Errorf("request %d failed: %v", i, err)
				return
			}
			codes[i] = code
		}(i)
	}
	wg.Wait()

	for i, code := range codes {
		if code != http.StatusOK {
			t.Fatalf("request %d = %d with concurrency exactly at the node cap, want 200; traffic that fits must not be refused", i, code)
		}
	}
}

// TestSyncBlockAdmissionReleasesSlotsAfterBurst is the leak check at process
// level: once the burst drains, capacity must be fully available again. A
// stranded slot would not show up as an error in the test above — it would show
// up here, and in production as a node that quietly loses throughput after every
// spike.
func TestSyncBlockAdmissionReleasesSlotsAfterBurst(t *testing.T) {
	requireCassandra(t)
	client := admissionTestClient(t)

	repoID := createTestLibrary(t, client, fmt.Sprintf("inttest-block-admission-drain-%d", time.Now().UnixNano()))

	const concurrency = 16
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			payload := bytes.Repeat([]byte{byte(i + 1)}, admissionTestBlockLen)
			_, _, _ = putBlockOnNode(client, repoID, syncSHA256HexForTest(payload), payload)
		}(i)
	}
	wg.Wait()

	// Give in-flight releases a moment to settle, then prove the node admits
	// serial traffic again without waiting on anything.
	time.Sleep(500 * time.Millisecond)

	for i := 0; i < admissionTestNodeCap+1; i++ {
		payload := []byte(fmt.Sprintf("post-burst admission probe %d %d", i, time.Now().UnixNano()))
		started := time.Now()
		code, _, err := putBlockOnNode(client, repoID, syncSHA256HexForTest(payload), payload)
		if err != nil {
			t.Fatalf("probe %d failed: %v", i, err)
		}
		if code != http.StatusOK {
			t.Fatalf("probe %d after the burst = %d, want 200; the burst stranded an admission", i, code)
		}
		if waited := time.Since(started); waited > admissionTestWait {
			t.Fatalf("probe %d waited %s for admission on an idle node; slots are leaking", i, waited)
		}
	}
}
