//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// Criterion 4 is the fairness invariant the D0 contract states as:
//
//	Public-link traffic never consumes the authenticated-user admission budget
//	of the link owner.
//
// It is the half of the design that a saturation test alone cannot show, because
// both directions have to hold: a hammered public link must not lock the owner
// out of their own library, and an owner who has filled their personal budget
// must not take the link down for anonymous visitors.
//
// The two are separate dimensions on purpose — link traffic keys on the stable
// SourceID and the client-link pair, authenticated traffic on (org, user) — so
// this drill is what proves the wiring matches the design rather than sharing a
// gate by accident.
//
// Run deliberately:
//
//	docker compose --profile test run --rm --build --entrypoint sh go-integration-test \
//	  -c 'export PATH=$PATH:/usr/local/go/bin && SESAMEFS_DOWNLOAD_PROBE=1 \
//	      go test -tags integration -run TestDownloadAdmissionFairness -v -count=1 ./internal/integration/'
func TestDownloadAdmissionFairnessIsolatesLinkAndOwner(t *testing.T) {
	client := requireDownloadProbe(t)
	repoID := createDisposableTestLibrary(t, client, "inttest-d6-fairness")
	fileName := "d6-fairness.bin"
	uploadProbeFile(t, client, repoID, fileName, 6<<20)

	shareToken := createShareLinkForFairness(t, client, repoID, "/"+fileName)
	linkRawURL := fmt.Sprintf("%s/d/%s/?raw=1", client.baseURL, shareToken)
	tokenResp := client.Get(t, fmt.Sprintf("/api2/repos/%s/file/?p=/%s", repoID, fileName))
	expectStatus(t, tokenResp, http.StatusOK)
	ownerFileURL := strings.Trim(responseBody(t, tokenResp), "\" \n\r")

	if active := scrapeDownloadGaugeInt(t, client, "download_admission_active_current", true); active != 0 {
		t.Fatalf("node already has %d active admissions; the drill needs an idle node", active)
	}

	t.Run("a saturated public link does not lock out the owner", func(t *testing.T) {
		stop := make(chan struct{})
		var wg sync.WaitGroup
		// Enough anonymous readers to fill the public-link identity budget several
		// times over, so any leakage into the owner's gate would be unmistakable.
		// Which of the two public gates closes first is not the claim here: every
		// reader shares one client address, so max_active_per_client_link is
		// reached before max_active_per_link_source. The isolation being tested is
		// public versus authenticated, and waitForRefusal below gates on the
		// public side genuinely refusing rather than on which cap did it.
		for i := 0; i < 12; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				holdAnonymousDownloadSlot(client, linkRawURL, stop)
			}()
		}
		defer func() {
			close(stop)
			wg.Wait()
			waitForDownloadActive(t, client, 0, 30*time.Second)
		}()

		// Wait for the precondition rather than for "some slot taken": the claim
		// only means something once the link budget is genuinely full, and
		// sampling earlier would let this pass against an idle node.
		waitForRefusal(t, client, linkRawURL, false, 30*time.Second,
			"the link budget was never filled, so the isolation claim is untested")

		// The owner's authenticated budget is a different dimension, so their own
		// download must still be admitted while the link is saturated.
		status, _ := probeDownload(t, client, ownerFileURL)
		if status != http.StatusOK {
			t.Fatalf("owner download = %d while their public link was saturated; link traffic consumed the owner's budget", status)
		}
	})

	waitForDownloadActive(t, client, 0, 30*time.Second)

	t.Run("a saturated owner does not take the public link down", func(t *testing.T) {
		stop := make(chan struct{})
		var wg sync.WaitGroup
		// The file profile cap is 16 while max_active_per_auth_user is 6, so this
		// fills the owner's identity budget rather than an ambiguous profile cap.
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				holdDownloadSlot(client, ownerFileURL, stop)
			}()
		}
		defer func() {
			close(stop)
			wg.Wait()
			waitForDownloadActive(t, client, 0, 30*time.Second)
		}()

		waitForRefusal(t, client, ownerFileURL, true, 30*time.Second,
			"the owner budget was never filled, so the isolation claim is untested")

		status, _ := probeAnonymousDownload(t, client, linkRawURL)
		if status != http.StatusOK {
			t.Fatalf("public link download = %d while the owner was saturated; the owner's budget refused link traffic", status)
		}
	})
}

func createShareLinkForFairness(t *testing.T, client *testClient, repoID, path string) string {
	t.Helper()

	resp := client.PostJSON(t, "/api/v2.1/share-links/", map[string]interface{}{
		"repo_id":     repoID,
		"path":        path,
		"permissions": "preview_download",
	})
	if resp.StatusCode == http.StatusForbidden {
		payload := responseJSON(t, resp)
		if payload["error"] == "Share link limit reached" {
			deleteFirstOrgShareLinkForTest(t, client)
			resp = client.PostJSON(t, "/api/v2.1/share-links/", map[string]interface{}{
				"repo_id":     repoID,
				"path":        path,
				"permissions": "preview_download",
			})
		}
	}
	expectStatus(t, resp, http.StatusOK)

	payload := responseJSON(t, resp)
	token, _ := payload["token"].(string)
	if token == "" {
		t.Fatalf("expected share link token, got %v", payload)
	}
	return token
}

// holdAnonymousDownloadSlot is the public-link counterpart of holdDownloadSlot:
// no Authorization header, so the request is attributed to the link's stable
// source identity rather than to any user.
func holdAnonymousDownloadSlot(c *testClient, target string, stop <-chan struct{}) {
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return
	}
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

func probeAnonymousDownload(t *testing.T, c *testClient, target string) (int, string) {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("build anonymous probe: %v", err)
	}
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("anonymous probe: %v", err)
	}
	defer resp.Body.Close()
	drainBody(resp)
	return resp.StatusCode, resp.Header.Get("Retry-After")
}

func drainBody(resp *http.Response) {
	buf := make([]byte, 32<<10)
	for {
		if _, err := resp.Body.Read(buf); err != nil {
			return
		}
	}
}

// waitForRefusal blocks until the given route actually refuses, which is the
// precondition every fairness claim here rests on. Waiting for the refusal
// itself removes the guesswork in "have the holders taken their slots yet".
func waitForRefusal(t *testing.T, c *testClient, target string, authenticated bool, timeout time.Duration, why string) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	last := 0
	for time.Now().Before(deadline) {
		if authenticated {
			last, _ = probeDownload(t, c, target)
		} else {
			last, _ = probeAnonymousDownload(t, c, target)
		}
		if last == http.StatusServiceUnavailable {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("probe still returned %d after %s; %s", last, timeout, why)
}
