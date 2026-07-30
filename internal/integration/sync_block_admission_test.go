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

// TestSyncBlockAdmissionRefusesWith503UnderSaturation drives more concurrent
// block PUTs than the node will admit and pins the contract the desktop sync
// client depends on: the overflow is refused 503 with a Retry-After, and never
// 429, which that client does not treat as retryable.
func TestSyncBlockAdmissionRefusesWith503UnderSaturation(t *testing.T) {
	requireCassandra(t)
	client := admissionTestClient(t)

	repoID := createTestLibrary(t, client, fmt.Sprintf("inttest-block-admission-%d", time.Now().UnixNano()))

	// Enough concurrency that the queue cannot drain within the admission wait.
	// Each admission costs a real storage round trip, so 64 requests through 2
	// slots is far more work than 250ms of waiting can cover.
	const concurrency = 64

	var wg sync.WaitGroup
	codes := make([]int, concurrency)
	retryAfters := make([]string, concurrency)
	errs := make([]error, concurrency)

	payloads := make([][]byte, concurrency)
	for i := range payloads {
		// Distinct payloads so dedup cannot short-circuit the work and free slots
		// faster than the burst arrives.
		payloads[i] = bytes.Repeat([]byte{byte(i + 1)}, admissionTestBlockLen)
	}

	start := make(chan struct{})
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			blockID := syncSHA256HexForTest(payloads[i])
			codes[i], retryAfters[i], errs[i] = putBlockOnNode(client, repoID, blockID, payloads[i])
		}(i)
	}
	close(start)
	wg.Wait()

	var ok, refused, other int
	for i := 0; i < concurrency; i++ {
		if errs[i] != nil {
			t.Fatalf("request %d failed at the transport level: %v", i, errs[i])
		}
		switch codes[i] {
		case http.StatusOK:
			ok++
		case http.StatusServiceUnavailable:
			refused++
			// A refusal the client cannot act on is not a working contract.
			seconds, err := strconv.Atoi(retryAfters[i])
			if err != nil || seconds < 1 {
				t.Errorf("request %d refused 503 with Retry-After=%q, want a positive integer", i, retryAfters[i])
			}
		case http.StatusTooManyRequests:
			t.Fatalf("request %d answered 429; the desktop sync client does not retry 429, this route must answer 503", i)
		default:
			other++
			t.Errorf("request %d = %d, want 200 or 503", i, codes[i])
		}
	}

	if ok == 0 {
		t.Fatalf("no request succeeded (%d refused, %d other); the gate is refusing traffic it should admit", refused, other)
	}
	// Without the guard every request would be admitted and the process would
	// buffer all of them at once, which is the defect subcontract B closes. With
	// it, a burst that cannot drain inside the wait has to shed load.
	//
	// If this fails, check the drain time before concluding the gate is missing:
	// admissions that complete quickly enough for the whole queue to clear within
	// SEAFHTTP_SYNC_BLOCK_ADMISSION_WAIT are supposed to all succeed.
	if refused == 0 {
		t.Fatalf("all %d concurrent block PUTs were admitted against a node cap of %d with a %s wait; either the in-flight gate is not wired into this route, or the burst drained inside the wait and this fixture is no longer saturating",
			concurrency, admissionTestNodeCap, admissionTestWait)
	}
	t.Logf("burst of %d against node cap %d: %d admitted, %d refused 503", concurrency, admissionTestNodeCap, ok, refused)
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
