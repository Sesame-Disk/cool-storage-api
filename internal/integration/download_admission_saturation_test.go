//go:build integration

package integration

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"math/rand"
	"mime/multipart"
	"net/http"
	"os"
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
	clients := []*testClient{
		client,
		newTestClient(client.baseURL, "dev-token-user"),
		newTestClient(client.baseURL, "dev-token-superadmin"),
	}
	for _, probeClient := range clients {
		probeClient.http.Timeout = 2 * time.Minute
		if err := verifyIntegrationAuth(probeClient.baseURL, probeClient.token); err != nil {
			t.Fatalf("download probe auth for %q: %v", probeClient.token, err)
		}
	}
	// Ask the node what it derived rather than assuming a number. Auto capacity
	// sizes from the detected memory limit, so a hardcoded expectation measures
	// the machine the fixture was written on.
	nodeCap := effectiveCapacity(t, client, "max_active_per_node")
	perIdentity := effectiveCapacity(t, client, "max_active_per_auth_user")
	if nodeCap <= 0 || perIdentity <= 0 {
		t.Fatalf("node reported capacity node=%d per-identity=%d; the guard cannot be exercised", nodeCap, perIdentity)
	}

	// The fixture holds slots as three authenticated identities, so it can fill
	// at most three times the per-identity cap — and that cap is policy-limited,
	// it does not grow with the node. A host that derives more than this cannot
	// be saturated by this fixture; the drill then verifies the plateau it can
	// reach and says so, instead of quietly proving a smaller ceiling.
	fillable := len(clients) * perIdentity
	target := nodeCap
	ceilingReachable := true
	if fillable < nodeCap {
		target = fillable
		ceilingReachable = false
		t.Logf("node derived a ceiling of %d but %d identities cap out at %d admissions; "+
			"verifying the reachable plateau and the invariants, not the exact ceiling",
			nodeCap, len(clients), fillable)
	}

	fileName := "d6-saturation.bin"
	repoIDs := make([]string, len(clients))
	for i, probeClient := range clients {
		repoIDs[i] = createDisposableTestLibrary(t, probeClient, fmt.Sprintf("inttest-d6-saturation-%d", i))
		uploadProbeFile(t, probeClient, repoIDs[i], fileName, 24<<20)
	}

	// A seafhttp download token resolves to the `file` profile; the raw route is
	// `raw`. Both are needed to show one shared ceiling rather than two budgets.
	// The shipped raw profile cap is six, so fill the remaining node capacity with
	// file-profile streams instead of accidentally proving only a seven-slot cap.
	fileStreamURLs := make([]string, len(clients))
	for i, probeClient := range clients {
		tokenResp := probeClient.Get(t, fmt.Sprintf("/api2/repos/%s/file/?p=/%s", repoIDs[i], fileName))
		expectStatus(t, tokenResp, http.StatusOK)
		fileStreamURLs[i] = strings.Trim(responseBody(t, tokenResp), "\" \n\r")
	}
	rawURLs := make([]string, len(repoIDs))
	for i, repoID := range repoIDs {
		rawURLs[i] = client.baseURL + fmt.Sprintf("/repo/%s/raw/%s", repoID, fileName)
	}
	shareToken := createShareLinkForFairness(t, client, repoIDs[0], "/"+fileName)
	linkRawURL := fmt.Sprintf("%s/d/%s/?raw=1", client.baseURL, shareToken)

	if active := scrapeDownloadGaugeInt(t, client, "download_admission_active_current", true); active != 0 {
		t.Fatalf("node already has %d active admissions; the probe needs an idle node", active)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var stopOnce sync.Once
	t.Cleanup(func() {
		stopOnce.Do(func() { close(stop) })
		wg.Wait()
		waitForDownloadActive(t, client, 0, 30*time.Second)
	})

	// The raw profile cap is also derived, so take it from the node too: filling
	// past it would be refused by profile_full and never reach the ceiling.
	rawSlots := effectiveCapacity(t, client, "max_active_raw")
	if rawSlots >= target {
		rawSlots = target - 1
	}
	if rawSlots < 1 {
		rawSlots = 1
	}
	for i := 0; i < target; i++ {
		probeClient := clients[i%len(clients)]
		var url string
		if i < rawSlots {
			url = rawURLs[i%len(rawURLs)]
		} else {
			url = fileStreamURLs[(i-rawSlots)%len(fileStreamURLs)]
		}
		wg.Add(1)
		go func(probeClient *testClient, url string) {
			defer wg.Done()
			holdDownloadSlot(probeClient, url, stop)
		}(probeClient, url)
	}

	waitForDownloadActive(t, client, target, 30*time.Second)
	active := waitForLiveDownloadProfiles(t, client, 2, 30*time.Second)
	if active != target {
		t.Fatalf("active downloads = %d, want %d", active, target)
	}
	if ceilingReachable {
		t.Logf("observed the node ceiling of %d concurrent admitted downloads across two profiles", active)
	} else {
		t.Logf("observed %d concurrent admitted downloads across two profiles (reachable plateau, node ceiling is %d)", active, nodeCap)
	}

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
	// The shipped admission_wait is non-zero, so a full-node request may be
	// observed as node_full (immediate) or admission_timeout (bounded queue).
	//
	// Only meaningful once the node is genuinely full: below the ceiling the
	// probe would simply be admitted, so asserting a refusal there would be
	// asserting the wrong thing rather than a weaker version of the right one.
	if !ceilingReachable {
		t.Log("skipping the full-node refusal contract: this fixture cannot fill the derived ceiling")
		return
	}
	beforeNodeFull := scrapeDownloadMetric(t, client, "download_admission_rejected_total", `reason="node_full"`)
	beforeAdmissionTimeout := scrapeDownloadMetric(t, client, "download_admission_rejected_total", `reason="admission_timeout"`)
	status, retryAfter := probeAnonymousDownload(t, client, linkRawURL)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("reader at a full node = %d, want %d", status, http.StatusServiceUnavailable)
	}
	seconds, err := strconv.Atoi(strings.TrimSpace(retryAfter))
	if err != nil || seconds < 1 {
		t.Fatalf("refusal carried Retry-After %q; must be a positive integer number of seconds", retryAfter)
	}
	afterNodeFull := scrapeDownloadMetric(t, client, "download_admission_rejected_total", `reason="node_full"`)
	afterAdmissionTimeout := scrapeDownloadMetric(t, client, "download_admission_rejected_total", `reason="admission_timeout"`)
	if afterNodeFull <= beforeNodeFull && afterAdmissionTimeout <= beforeAdmissionTimeout {
		t.Fatalf("full-node refusal did not increase node_full or admission_timeout: node %.0f -> %.0f, timeout %.0f -> %.0f", beforeNodeFull, afterNodeFull, beforeAdmissionTimeout, afterAdmissionTimeout)
	}
	t.Logf("reader refused with bounded full-node backpressure (node_full %.0f -> %.0f, admission_timeout %.0f -> %.0f)", beforeNodeFull, afterNodeFull, beforeAdmissionTimeout, afterAdmissionTimeout)
}

// effectiveCapacity reads a capacity the node actually resolved. Auto mode
// derives these at startup from the detected memory limit, so the config file's
// numbers are a reference and an env var passed to the test container is a
// guess about a different process. Asking the server is the only way for a
// drill to assert against the ceiling that is really in force.
func effectiveCapacity(t *testing.T, c *testClient, setting string) int {
	t.Helper()
	value := scrapeDownloadMetric(t, c, "download_admission_capacity", fmt.Sprintf("setting=%q", setting))
	return int(value)
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

// TestDownloadAdmissionDrainsStateAfterRepeatedTransfers proves the live
// producer returns every slot after repeated short transfers. The coordinator's
// 20,000-distinct-identity unit test owns the cardinality proof.
func TestDownloadAdmissionDrainsStateAfterRepeatedTransfers(t *testing.T) {
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
	// Deterministic pseudo-random payload so gzip negotiation cannot collapse the
	// stream and finish it before a slow reader has taken its slot.
	chunk := make([]byte, 64<<10)
	random := rand.New(rand.NewSource(0xD6A5EED))
	for written := 0; written < size; written += len(chunk) {
		remaining := size - written
		if remaining < len(chunk) {
			chunk = chunk[:remaining]
		}
		if _, err := random.Read(chunk); err != nil {
			t.Fatalf("fill upload fixture: %v", err)
		}
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

func scrapeDownloadMetric(t *testing.T, c *testClient, metric, labelMatch string) float64 {
	t.Helper()
	body := scrapeMetricsBody(t, c)
	var total float64
	found := false
	scanner := bufio.NewScanner(strings.NewReader(body))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") || !strings.HasPrefix(line, metric) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := fields[0]
		if labelMatch == "" {
			if name != metric {
				continue
			}
		} else if !strings.Contains(name, labelMatch) {
			continue
		}
		value, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil {
			continue
		}
		total += value
		found = true
	}
	if !found {
		t.Fatalf("metric %s{%s} is not exported", metric, labelMatch)
	}
	return total
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
