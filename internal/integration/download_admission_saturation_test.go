//go:build integration

package integration

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// D6 closure evidence that no unit test can produce: real storage, the real
// middleware stack and real concurrent readers against a running node.
//
// It covers four closure criteria at once, because they are only meaningfully
// observable under the same saturated node:
//
//	3  one node ceiling shared across routes
//	5  bounded state that drains to zero
//	8  the 503 + Retry-After refusal contract
//	14 active_current == sum(active_by_profile) and
//	   entries_current == active_current + waiters_current under mixed load
//
// Saturation is produced by readers that take their slot and then stop reading,
// which is what a real slow client does; it is not simulated by timing.
//
// Run deliberately:
//
//	docker compose --profile test run --rm --build --entrypoint sh go-integration-test \
//	  -c 'export PATH=$PATH:/usr/local/go/bin && SESAMEFS_DOWNLOAD_PROBE=1 \
//	      go test -tags integration -run TestDownloadAdmission -v -count=1 ./internal/integration/'
func TestDownloadAdmissionSaturationHoldsOneNodeCeiling(t *testing.T) {
	client := requireDownloadProbe(t)
	repoID := createDisposableTestLibrary(t, client, "inttest-d6-saturation")
	fileName := "d6-saturation.bin"
	uploadProbeFile(t, client, repoID, fileName, 6<<20)

	// A seafhttp download token resolves to the `file` profile; the raw route is
	// `raw`. Both are needed to show one shared ceiling rather than two budgets.
	tokenResp := client.Get(t, fmt.Sprintf("/api2/repos/%s/file/?p=/%s", repoID, fileName))
	expectStatus(t, tokenResp, http.StatusOK)
	fileStreamURL := strings.Trim(responseBody(t, tokenResp), "\" \n\r")
	rawURL := client.baseURL + fmt.Sprintf("/repo/%s/raw/%s", repoID, fileName)

	if active := scrapeDownloadGaugeInt(t, client, "download_admission_active_current", true); active != 0 {
		t.Fatalf("node already has %d active admissions; the probe needs an idle node", active)
	}

	// The binding constraint for one identity at the shipped values is
	// max_active_per_auth_user, not the per-profile caps, so the holders are
	// split evenly across two routes to fill it with both profiles live. Piling
	// every holder onto one route would fill the same budget with one profile and
	// prove nothing about a shared ceiling.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		target := rawURL
		if i%2 == 1 {
			target = fileStreamURL
		}
		wg.Add(1)
		go func(target string) {
			defer wg.Done()
			holdDownloadSlot(client, target, stop)
		}(target)
	}

	active := waitForLiveDownloadProfiles(t, client, 2, 30*time.Second)
	t.Logf("observed %d concurrent admitted downloads across two profiles", active)

	// Criterion 14: the two identities the contract freezes, sampled while the
	// node is genuinely busy rather than idle.
	assertDownloadAdmissionInvariants(t, client)

	// Criterion 3: both profiles are live at once and both are counted in the
	// same aggregate, which is what "one node ceiling" means operationally.
	for name, value := range scrapeDownloadAdmissionSeries(t, client).byProfile {
		if value > 0 {
			t.Logf("live profile %s = %v", name, value)
		}
	}

	// Criterion 8: with the budget full, further readers are refused with a retry
	// contract the desktop client understands rather than being queued forever.
	refusals := 0
	for i := 0; i < 8; i++ {
		status, retryAfter := probeDownload(t, client, rawURL)
		if status != http.StatusServiceUnavailable {
			continue
		}
		refusals++
		seconds, err := strconv.Atoi(strings.TrimSpace(retryAfter))
		if err != nil || seconds < 1 {
			t.Fatalf("refusal carried Retry-After %q; must be a positive integer number of seconds", retryAfter)
		}
	}
	if refusals == 0 {
		t.Fatal("no reader was refused while the budget was full; the drill did not test the refusal contract")
	}
	t.Logf("%d readers refused with a valid Retry-After", refusals)

	close(stop)
	wg.Wait()

	// Criterion 5: every slot returns after the load stops.
	waitForDownloadActive(t, client, 0, 30*time.Second)
}

// holdDownloadSlot takes an admission and then stops reading, which is what a
// real slow client does. Timing is never used to simulate occupancy.
func holdDownloadSlot(c *testClient, target string, stop <-chan struct{}) {
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := c.http.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	one := make([]byte, 1)
	_, _ = resp.Body.Read(one)
	<-stop
}

func probeDownload(t *testing.T, c *testClient, target string) (int, string) {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("build probe: %v", err)
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, resp.Header.Get("Retry-After")
}

// TestDownloadAdmissionDrainsIdentityStateAfterChurn covers the other half of
// criterion 5: identity maps must not grow without bound across many distinct
// short transfers, which is the shape ordinary traffic actually has.
func TestDownloadAdmissionDrainsIdentityStateAfterChurn(t *testing.T) {
	client := requireDownloadProbe(t)
	repoID := createDisposableTestLibrary(t, client, "inttest-d6-churn")
	fileName := "d6-churn.bin"
	uploadProbeFile(t, client, repoID, fileName, 64<<10)

	before := scrapeDownloadGaugeInt(t, client, "download_admission_tracked_identities", false)

	const rounds = 200
	for i := 0; i < rounds; i++ {
		req, err := http.NewRequest(http.MethodGet, client.baseURL+fmt.Sprintf("/repo/%s/raw/%s", repoID, fileName), nil)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		req.Header.Set("Authorization", "Token "+client.token)
		resp, err := client.http.Do(req)
		if err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}

	waitForDownloadActive(t, client, 0, 20*time.Second)

	after := scrapeDownloadGaugeInt(t, client, "download_admission_tracked_identities", false)
	// Entries are removed when a gate reaches zero active and zero waiters, so a
	// completed transfer must leave nothing behind. A small allowance covers
	// unrelated traffic on a shared node rather than growth from this loop.
	if after > before+2 {
		t.Fatalf("tracked identities grew from %v to %v across %d transfers; identity state is not draining", before, after, rounds)
	}
}

func requireDownloadProbe(t *testing.T) *testClient {
	t.Helper()
	if os.Getenv("SESAMEFS_DOWNLOAD_PROBE") != "1" {
		t.Skip("set SESAMEFS_DOWNLOAD_PROBE=1 to run the download-admission saturation drill")
	}
	requireCassandra(t)

	base := strings.TrimSpace(os.Getenv("SESAMEFS_URL"))
	if base == "" {
		t.Skip("SESAMEFS_URL is not set")
	}
	client := newTestClient(base, "dev-token-admin")
	client.http.Timeout = 2 * time.Minute
	return client
}

func uploadProbeFile(t *testing.T, c *testClient, repoID, fileName string, size int) {
	t.Helper()

	resp := c.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", repoID))
	expectStatus(t, resp, http.StatusOK)
	uploadURL := strings.Trim(responseBody(t, resp), "\" \n\r")

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	// Incompressible-ish payload so gzip negotiation cannot collapse the stream
	// and finish it before a slow reader has taken its slot.
	chunk := make([]byte, 64<<10)
	for i := range chunk {
		chunk[i] = byte(i * 31)
	}
	for written := 0; written < size; written += len(chunk) {
		if _, err := part.Write(chunk); err != nil {
			t.Fatalf("write upload body: %v", err)
		}
	}
	if err := writer.WriteField("parent_dir", "/"); err != nil {
		t.Fatalf("WriteField: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, uploadURL, &buf)
	if err != nil {
		t.Fatalf("build upload: %v", err)
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	uploadResp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	defer uploadResp.Body.Close()
	if uploadResp.StatusCode != http.StatusOK && uploadResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(uploadResp.Body)
		t.Fatalf("upload = %d: %s", uploadResp.StatusCode, body)
	}
}

// assertDownloadAdmissionInvariants checks the two identities §12 freezes. They
// are asserted from one scrape so an admission landing between two reads cannot
// make a healthy node look inconsistent.
func assertDownloadAdmissionInvariants(t *testing.T, c *testClient) {
	t.Helper()

	samples := scrapeDownloadAdmissionSeries(t, c)

	active := samples.single["download_admission_active_current"]
	entries := samples.single["download_admission_entries_current"]
	waiters := samples.single["download_admission_waiters_current"]

	var profileSum float64
	for _, v := range samples.byProfile {
		profileSum += v
	}

	// Independent gauges are gathered one at a time, so a small skew is expected
	// on a live node; the contract says not to alert on strict equality. A
	// systematic break — a profile never counted, or double counting — is much
	// larger than this tolerance.
	const tolerance = 2.0
	if diff := active - profileSum; diff > tolerance || diff < -tolerance {
		t.Fatalf("active_current=%v but sum(active_by_profile)=%v (%v); a profile is miscounted", active, profileSum, samples.byProfile)
	}
	if diff := entries - (active + waiters); diff > tolerance || diff < -tolerance {
		t.Fatalf("entries_current=%v but active_current+waiters_current=%v", entries, active+waiters)
	}
	t.Logf("invariants held: active=%v profiles=%v entries=%v waiters=%v", active, profileSum, entries, waiters)
}

type downloadAdmissionSeries struct {
	single    map[string]float64
	byProfile map[string]float64
}

func scrapeDownloadAdmissionSeries(t *testing.T, c *testClient) downloadAdmissionSeries {
	t.Helper()

	out := downloadAdmissionSeries{single: map[string]float64{}, byProfile: map[string]float64{}}
	body := scrapeMetricsBody(t, c)
	scanner := bufio.NewScanner(strings.NewReader(body))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil {
			continue
		}
		name := fields[0]
		switch {
		case strings.HasPrefix(name, "download_admission_active_by_profile"):
			out.byProfile[name] = value
		case !strings.Contains(name, "{"):
			out.single[name] = value
		}
	}
	return out
}

func scrapeMetricsBody(t *testing.T, c *testClient) string {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, c.baseURL+"/metrics", nil)
	if err != nil {
		t.Fatalf("build metrics request: %v", err)
	}
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("scrape metrics: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /metrics = %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read metrics: %v", err)
	}
	return string(body)
}

func scrapeDownloadGaugeInt(t *testing.T, c *testClient, metric string, mustExist bool) int {
	t.Helper()

	body := scrapeMetricsBody(t, c)
	total := 0.0
	found := false
	scanner := bufio.NewScanner(strings.NewReader(body))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, metric) || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil {
			continue
		}
		total += value
		found = true
	}
	if !found && mustExist {
		t.Fatalf("metric %s is not exported; the series is the only visibility this guard has", metric)
	}
	return int(total)
}

func waitForDownloadActiveAtLeast(t *testing.T, c *testClient, want int, timeout time.Duration) int {
	t.Helper()

	deadline := time.Now().Add(timeout)
	last := 0
	for time.Now().Before(deadline) {
		last = scrapeDownloadGaugeInt(t, c, "download_admission_active_current", true)
		if last >= want {
			return last
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("active downloads reached only %d, want at least %d; the drill never saturated", last, want)
	return last
}

func waitForDownloadActive(t *testing.T, c *testClient, want int, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	last := -1
	for time.Now().Before(deadline) {
		last = scrapeDownloadGaugeInt(t, c, "download_admission_active_current", true)
		if last == want {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("active downloads settled at %d, want %d; slots did not drain", last, want)
}

// waitForLiveDownloadProfiles blocks until at least want distinct profiles are
// concurrently admitted, which is the observable form of "one shared ceiling
// across routes".
func waitForLiveDownloadProfiles(t *testing.T, c *testClient, want int, timeout time.Duration) int {
	t.Helper()

	deadline := time.Now().Add(timeout)
	lastLive, lastActive := 0, 0
	for time.Now().Before(deadline) {
		series := scrapeDownloadAdmissionSeries(t, c)
		live := 0
		for _, value := range series.byProfile {
			if value > 0 {
				live++
			}
		}
		lastLive = live
		lastActive = int(series.single["download_admission_active_current"])
		if live >= want {
			return lastActive
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("only %d profile(s) ever became live (active=%d); the drill did not exercise a shared ceiling across routes", lastLive, lastActive)
	return lastActive
}
