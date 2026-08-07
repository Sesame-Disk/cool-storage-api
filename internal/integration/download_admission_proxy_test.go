//go:build integration

package integration

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// Criterion 10 is the one the contract refuses to accept a backend test for:
//
//	D6 must run a slow-client test through the real nginx topology as well as
//	directly against Go. A backend TCP test alone does not prove that the
//	browser/client is controlling admission lifetime.
//
// The reason is the proxy itself. If nginx buffers the response, it drains the
// backend at its own pace and the application never sees a slow client at all:
// the idle-write deadline would measure nginx, the slot would free on schedule,
// and the guard would look healthy while a real browser could still hold it. The
// supported configuration therefore sets proxy_buffering off and gzip off on
// every transfer location, and this is what checks that end to end.
//
// Run deliberately:
//
//	docker compose --profile test run --rm --build --entrypoint sh go-integration-test \
//	  -c 'export PATH=$PATH:/usr/local/go/bin && SESAMEFS_DOWNLOAD_PROBE=1 \
//	      go test -tags integration -run TestDownloadAdmissionThroughProxy -v -count=1 ./internal/integration/'
func TestDownloadAdmissionThroughProxyReleasesStalledClient(t *testing.T) {
	client := requireDownloadProbe(t)
	// The application owns the 60s idle deadline. Keep the client deadline well
	// beyond the drill so a broken server-side guard cannot pass by observing a
	// client cancellation at requireDownloadProbe's old two-minute timeout.
	client.http.Timeout = 5 * time.Minute
	proxyURL := strings.TrimSpace(os.Getenv("SESAMEFS_PROXY_URL"))
	if proxyURL == "" {
		t.Skip("SESAMEFS_PROXY_URL is not set; the drill needs the supported frontend nginx")
	}

	repoID := createDisposableTestLibrary(t, client, "inttest-d6-proxy")
	fileName := "d6-proxy.bin"
	// Larger than anything nginx would hold in its own buffers, so a client that
	// stops reading really does stall the transfer rather than letting the proxy
	// absorb it and complete the backend request early.
	uploadProbeFile(t, client, repoID, fileName, 24<<20)

	if active := scrapeDownloadGaugeInt(t, client, "download_admission_active_current", true); active != 0 {
		t.Fatalf("node already has %d active admissions; the drill needs an idle node", active)
	}
	idleBefore := scrapeDownloadMetric(t, client, "download_admission_released_total", `cause="idle_write_timeout"`)
	disconnectBefore := scrapeDownloadMetric(t, client, "download_admission_released_total", `cause="client_disconnect"`)
	deadlineBefore := scrapeDownloadMetric(t, client, "download_admission_deadline_expired_total", `phase="idle_write"`)

	proxyRawURL := fmt.Sprintf("%s/repo/%s/raw/%s", strings.TrimRight(proxyURL, "/"), repoID, fileName)

	req, err := http.NewRequest(http.MethodGet, proxyRawURL, nil)
	if err != nil {
		t.Fatalf("build proxied request: %v", err)
	}
	req.Header.Set("Authorization", "Token "+client.token)
	// Ask for gzip explicitly: the transfer locations disable it, and a
	// compressed response here would mean the writer was wrapped and the deadline
	// came from somewhere other than the connection.
	req.Header.Set("Accept-Encoding", "gzip")

	resp, err := client.http.Do(req)
	if err != nil {
		t.Fatalf("proxied download: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("proxied download = %d, want 200", resp.StatusCode)
	}
	if encoding := resp.Header.Get("Content-Encoding"); encoding != "" {
		t.Fatalf("Content-Encoding = %q through the proxy; the transfer locations must disable gzip", encoding)
	}

	// Take the slot, then stop reading. This is the browser that opened a
	// download and went away without closing the connection.
	one := make([]byte, 1)
	if _, err := resp.Body.Read(one); err != nil {
		t.Fatalf("read first byte through proxy: %v", err)
	}

	// The slot must be occupied while the client is stalled. If nginx were
	// buffering, the backend would already have finished and this would be zero —
	// which is exactly the failure this drill exists to catch.
	waitForDownloadActiveAtLeast(t, client, 1, 15*time.Second)
	t.Log("stalled client through nginx is holding an admission")

	// And it must be released without the client doing anything, on the
	// application's own idle-write deadline rather than a proxy timeout.
	before := time.Now()
	waitForDownloadActive(t, client, 0, 2*time.Minute)
	elapsed := time.Since(before)
	idleAfter := waitForDownloadMetricIncrease(t, client, "download_admission_released_total", `cause="idle_write_timeout"`, idleBefore, 10*time.Second)
	deadlineAfter := scrapeDownloadMetric(t, client, "download_admission_deadline_expired_total", `phase="idle_write"`)
	disconnectAfter := scrapeDownloadMetric(t, client, "download_admission_released_total", `cause="client_disconnect"`)
	if elapsed < 30*time.Second || elapsed > 2*time.Minute {
		t.Fatalf("stalled proxied transfer released after %s; expected the configured idle deadline window", elapsed.Round(time.Second))
	}
	if deadlineAfter <= deadlineBefore {
		t.Fatalf("idle-write deadline metric did not increase: %.0f -> %.0f", deadlineBefore, deadlineAfter)
	}
	if disconnectAfter > disconnectBefore {
		t.Fatalf("proxy drill was classified as client_disconnect: %.0f -> %.0f", disconnectBefore, disconnectAfter)
	}
	t.Logf("stalled proxied transfer released after %s with idle_write_timeout (release counter %.0f)", elapsed.Round(time.Second), idleAfter)
}

func waitForDownloadMetricIncrease(t *testing.T, c *testClient, metric, labelMatch string, before float64, timeout time.Duration) float64 {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		after := scrapeDownloadMetric(t, c, metric, labelMatch)
		if after > before {
			return after
		}
		time.Sleep(200 * time.Millisecond)
	}
	after := scrapeDownloadMetric(t, c, metric, labelMatch)
	t.Fatalf("metric %s{%s} did not increase from %.0f within %s", metric, labelMatch, before, timeout)
	return after
}

// TestDownloadAdmissionThroughProxyDeliversCompleteBytes is the other half of
// criterion 10: the proxy configuration that makes a stalled client visible must
// not have broken ordinary delivery. A guard that bounds transfers by corrupting
// them would pass every saturation test in this package.
func TestDownloadAdmissionThroughProxyDeliversCompleteBytes(t *testing.T) {
	client := requireDownloadProbe(t)
	proxyURL := strings.TrimSpace(os.Getenv("SESAMEFS_PROXY_URL"))
	if proxyURL == "" {
		t.Skip("SESAMEFS_PROXY_URL is not set; the drill needs the supported frontend nginx")
	}

	repoID := createDisposableTestLibrary(t, client, "inttest-d6-proxy-bytes")
	fileName := "d6-proxy-bytes.bin"
	const size = 6 << 20
	uploadProbeFile(t, client, repoID, fileName, size)

	direct := readAllFrom(t, client, client.baseURL+fmt.Sprintf("/repo/%s/raw/%s", repoID, fileName))
	proxied := readAllFrom(t, client, fmt.Sprintf("%s/repo/%s/raw/%s", strings.TrimRight(proxyURL, "/"), repoID, fileName))

	if len(direct) != len(proxied) {
		t.Fatalf("proxied length %d != direct length %d", len(proxied), len(direct))
	}
	// Byte-for-byte rather than length-only: a truncation that happened to
	// preserve the size would otherwise slip through.
	for i := range direct {
		if direct[i] != proxied[i] {
			t.Fatalf("byte %d differs between direct and proxied delivery", i)
		}
	}
	t.Logf("delivered %d bytes identically direct and through nginx", len(direct))

	waitForDownloadActive(t, client, 0, 30*time.Second)
}

func readAllFrom(t *testing.T, c *testClient, target string) []byte {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d", target, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", target, err)
	}
	return body
}
