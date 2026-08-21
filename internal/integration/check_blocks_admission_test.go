//go:build integration

package integration

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/apikeys"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
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

type checkBlocksTenant struct {
	admin *testClient
	user  *testClient
}

// provisionCheckBlocksTenant keeps the admission suite's storage and traffic
// accounting out of the shared dev organization. The second identity is used
// only where the node-level gate must span distinct users.
func provisionCheckBlocksTenant(t *testing.T, label string) checkBlocksTenant {
	t.Helper()

	tenant := provisionIsolatedTenant(t, "check-blocks-"+label)
	adminProbe := admissionTestClient(t)
	admin := newTestClient(adminProbe.baseURL, exchangeAPIKeyForSyncToken(t, adminProbe.baseURL, tenant.email, tenant.client.token))

	stamp := time.Now().UnixNano()
	email := fmt.Sprintf("inttest-check-blocks-%s-user-%d@sesamefs.local", label, stamp)
	resp := superadminClient.PostJSON(t, "/api/v2.1/admin/organizations/"+tenant.orgID+"/users/", map[string]string{
		"email": email,
		"name":  "check-blocks-user",
	})
	expectStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	userID, found := lookupUserIDByEmail(t, email)
	if !found {
		t.Fatalf("isolated check-blocks user %s was not created", email)
	}
	orgUUID, err := gocql.ParseUUID(tenant.orgID)
	if err != nil {
		t.Fatalf("parse isolated organization id %s: %v", tenant.orgID, err)
	}
	userUUID, err := gocql.ParseUUID(userID)
	if err != nil {
		t.Fatalf("parse isolated user id %s: %v", userID, err)
	}

	mgr := apikeys.NewManager(shareProjectionDBForTest(t))
	t.Cleanup(mgr.Stop)
	rawToken, key, err := mgr.CreateKey(userUUID, orgUUID, "check-blocks-"+label, apikeys.ScopeReadWrite, nil)
	if err != nil {
		t.Fatalf("mint API key for isolated check-blocks user: %v", err)
	}
	t.Cleanup(func() {
		if err := mgr.RevokeKey(orgUUID, userUUID, key.KeyHash); err != nil {
			t.Logf("cleanup: revoke isolated check-blocks API key: %v", err)
		}
	})

	return checkBlocksTenant{
		admin: admin,
		user:  newTestClient(adminProbe.baseURL, exchangeAPIKeyForSyncToken(t, adminProbe.baseURL, email, rawToken)),
	}
}

// syncAuthMiddleware accepts server-issued sessions rather than raw API keys.
// Exchange each disposable key through the same endpoint the desktop client uses.
func exchangeAPIKeyForSyncToken(t *testing.T, baseURL, email, apiKey string) string {
	t.Helper()

	resp, err := http.PostForm(baseURL+"/api2/auth-token/", url.Values{
		"username": {email},
		"password": {apiKey},
	})
	if err != nil {
		t.Fatalf("exchange disposable API key for sync token: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("exchange disposable API key for sync token = %d: %s", resp.StatusCode, body)
	}
	var result struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode disposable sync token: %v", err)
	}
	if result.Token == "" {
		t.Fatal("disposable API key exchange returned an empty sync token")
	}
	return result.Token
}

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
	tenant := provisionCheckBlocksTenant(t, "refusal")
	admin := tenant.admin
	user := tenant.user
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
	client := provisionCheckBlocksTenant(t, "cross-route").admin
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
	client := provisionCheckBlocksTenant(t, "dedup").admin
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
	client := provisionCheckBlocksTenant(t, "drain").admin
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

// TestCheckBlocksLargeCardinalityLifetime is an opt-in Docker probe for the
// compatibility ceiling. It stays out of the default suite because two
// 100k-id requests deliberately put sustained load on real Cassandra. The
// legacy case exercises the mapping phase; the canonical case seeds temporary
// metadata and exercises both location and real object-store existence checks.
// Rows are removed in bounded cleanup batches.
func TestCheckBlocksLargeCardinalityLifetime(t *testing.T) {
	if os.Getenv("CHECK_BLOCKS_LARGE_PROBE") != "1" {
		t.Skip("set CHECK_BLOCKS_LARGE_PROBE=1 for the real 100k-id lifetime probe")
	}
	requireCassandra(t)
	client := provisionCheckBlocksTenant(t, "large-lifetime").admin
	client.http.Timeout = 6 * time.Minute
	repoID := createTestLibrary(t, client, fmt.Sprintf("inttest-check-blocks-large-lifetime-%d", time.Now().UnixNano()))

	for _, width := range []int{40, 64} {
		t.Run(fmt.Sprintf("%d-character-ids", width), func(t *testing.T) {
			const count = 100000
			prefix := randomCheckBlocksProbePrefix(t, width)
			ids := make([]string, count)
			var body strings.Builder
			body.Grow(count * (width + 1))
			for i := 0; i < count; i++ {
				ids[i] = prefix + fmt.Sprintf("%016x", i)
				if i > 0 {
					body.WriteByte('\n')
				}
				body.WriteString(ids[i])
			}

			if width == 64 {
				orgID := resolveOrgID(t, repoID)
				_, sampleBlockID := uploadUniqueFile(t, client, repoID, "large-lifetime-storage-class.txt", "/")
				var storageClass string
				session := shareProjectionDBForTest(t).Session()
				if err := session.Query(
					`SELECT storage_class FROM blocks WHERE org_id = ? AND block_id = ?`,
					orgID, sampleBlockID,
				).Scan(&storageClass); err != nil || storageClass == "" {
					t.Fatalf("read storage class for existence probe: %v", err)
				}
				seedCanonicalCheckRows(t, orgID, ids, storageClass)
			}

			started := time.Now()
			code, _, err := postCheckBlocks(client, repoID, strings.NewReader(body.String()))
			elapsed := time.Since(started)
			if err != nil {
				t.Fatalf("100k-id check-blocks request: %v", err)
			}
			if code != http.StatusOK {
				t.Fatalf("100k-id check-blocks request = %d after %s, want 200 before the shipped 5m lifetime", code, elapsed)
			}
			if elapsed >= 5*time.Minute {
				t.Fatalf("100k-id check-blocks request took %s, which exceeds the shipped 5m admitted lifetime", elapsed)
			}
			t.Logf("100k %d-character ids completed in %s", width, elapsed)
		})
	}
}

// randomCheckBlocksProbePrefix makes temporary probe rows unique within the
// disposable organization, so cleanup targets only this invocation's rows.
func randomCheckBlocksProbePrefix(t *testing.T, width int) string {
	t.Helper()
	if width <= 16 || (width-16)%2 != 0 {
		t.Fatalf("invalid check-blocks probe id width %d", width)
	}
	prefix := make([]byte, (width-16)/2)
	if _, err := rand.Read(prefix); err != nil {
		t.Fatalf("random check-blocks probe prefix: %v", err)
	}
	return hex.EncodeToString(prefix)
}

// seedCanonicalCheckRows creates metadata without physical objects so the
// 100k canonical probe exercises the real MinIO existence phase. Rows are
// cleaned in bounded unlogged batches when the subtest exits.
func seedCanonicalCheckRows(t *testing.T, orgID string, ids []string, storageClass string) {
	t.Helper()
	session := shareProjectionDBForTest(t).Session()
	const batchSize = 50
	insert := `INSERT INTO blocks (org_id, block_id, size_bytes, storage_class, storage_key, created_at) VALUES (?, ?, ?, ?, ?, ?)`
	delete := `DELETE FROM blocks WHERE org_id = ? AND block_id = ?`
	createdAt := time.Now().UTC()
	t.Cleanup(func() {
		for start := 0; start < len(ids); start += batchSize {
			end := start + batchSize
			if end > len(ids) {
				end = len(ids)
			}
			batch := session.Batch(gocql.UnloggedBatch)
			for _, id := range ids[start:end] {
				batch.Query(delete, orgID, id)
			}
			if err := session.ExecuteBatch(batch); err != nil {
				t.Errorf("cleanup canonical check rows: %v", err)
				return
			}
		}
	})

	flush := func(batch *gocql.Batch, operation string) {
		if err := session.ExecuteBatch(batch); err != nil {
			t.Fatalf("%s canonical check rows: %v", operation, err)
		}
	}
	for start := 0; start < len(ids); start += batchSize {
		end := start + batchSize
		if end > len(ids) {
			end = len(ids)
		}
		batch := session.Batch(gocql.UnloggedBatch)
		for _, id := range ids[start:end] {
			batch.Query(insert, orgID, id, 1, storageClass, fmt.Sprintf("blocks/%s/aa/aa/%s", orgID, id), createdAt)
		}
		flush(batch, "insert")
	}
}
