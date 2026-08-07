//go:build integration

package integration

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"
)

// ISSUE-SYNC-LINK-TOKEN-AUTH-01.
//
// A public share link hands its anonymous visitor a temporary download bearer:
// `?dl=1` answers with a 302 to /seafhttp/files/<token>/<filename>, so the token
// is in the visitor's address bar, history and any intermediary's logs. That is
// by design for the file fetch it authorizes.
//
// What must not follow is that the same bearer authenticates the repository
// sync surface. syncAuthMiddleware accepts any TokenTypeDownload token and
// installs the token's UserID and OrgID as the caller identity, so a token
// minted for one shared file can be replayed as the link creator against
// /seafhttp/repo/:repo_id/*. checkSyncPermission then evaluates the creator's
// library permissions rather than the link's narrower grant.
//
// These tests assert the closed behaviour. Against a server without the fix
// they fail, which is the point: each one is a live proof of the gap.
func TestSyncAuthRejectsPublicShareLinkDownloadToken(t *testing.T) {
	requireCassandra(t)

	client := adminClient
	repoID := createTestLibrary(t, client, syncLinkTestName("link-token"))
	const fileName = "shared.txt"
	uploadTestFile(t, client, repoID, "/", fileName, "shared file body")

	shareToken := createShareLinkForTest(t, client, repoID, "/"+fileName)
	linkToken := harvestShareLinkDownloadToken(t, client, shareToken, fileName)

	// Precondition: the bearer really is usable for what it was issued for.
	// Without this the rejections below could pass against an inert string.
	resp := client.DoAnonymous(t, http.MethodGet, "/seafhttp/files/"+linkToken+"/"+fileName)
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("harvested link token could not fetch the shared file (status=%d); the replay tests below would be vacuous", resp.StatusCode)
	}
	resp.Body.Close()

	// Every route under the sync group, read and write. A partial fix that
	// guards the obvious read paths and leaves a writer open is still a hole.
	replays := []struct {
		name   string
		method string
		path   string
	}{
		{"head commit", http.MethodGet, "/commit/HEAD"},
		{"fs id list", http.MethodGet, "/fs-id-list"},
		{"permission check", http.MethodGet, "/permission-check"},
		{"quota check", http.MethodGet, "/quota-check"},
		{"check blocks", http.MethodPost, "/check-blocks"},
		{"pack fs", http.MethodPost, "/pack-fs"},
		{"check fs", http.MethodPost, "/check-fs"},
		{"recv fs", http.MethodPost, "/recv-fs"},
		{"update branch", http.MethodPost, "/update-branch"},
	}

	for _, replay := range replays {
		t.Run(replay.name, func(t *testing.T) {
			status := replaySyncToken(t, client, replay.method, fmt.Sprintf("/seafhttp/repo/%s%s", repoID, replay.path), linkToken)
			if status != http.StatusUnauthorized && status != http.StatusForbidden {
				t.Errorf("%s %s with a share-link download token = %d; want 401 or 403. A public bearer authenticated the sync surface as the link creator.",
					replay.method, replay.path, status)
			}
		})
	}
}

// The sharpest consequence: /download-info mints a repository-root sync token
// with Source == "" for whoever reaches it. If a link bearer can reach it, a
// narrow, path-scoped, one-hour public credential is traded for a full
// repository sync credential — the escalation, not just the bypass.
func TestSyncAuthLinkTokenCannotMintRepositorySyncToken(t *testing.T) {
	requireCassandra(t)

	client := adminClient
	repoID := createTestLibrary(t, client, syncLinkTestName("link-mint"))
	const fileName = "shared.txt"
	uploadTestFile(t, client, repoID, "/", fileName, "shared file body")

	shareToken := createShareLinkForTest(t, client, repoID, "/"+fileName)
	linkToken := harvestShareLinkDownloadToken(t, client, shareToken, fileName)

	resp := replaySyncTokenResponse(t, client, http.MethodGet, fmt.Sprintf("/seafhttp/repo/%s/download-info", repoID), linkToken)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		body := responseBody(t, resp)
		t.Fatalf("download-info with a share-link download token = %d; want 401 or 403. Body: %s\n"+
			"A path-scoped public bearer was exchanged for a repository-root sync token.", resp.StatusCode, body)
	}
}

// The middleware sets repo_id from the token but every sync handler reads
// c.Param("repo_id"), so nothing binds the token's repository to the route's.
// A token minted for one library therefore reaches another, bounded only by
// what the token's owner happens to be allowed to do there.
func TestSyncAuthTokenIsBoundToItsOwnRepository(t *testing.T) {
	requireCassandra(t)

	client := adminClient
	sharedRepo := createTestLibrary(t, client, syncLinkTestName("bind-shared"))
	otherRepo := createTestLibrary(t, client, syncLinkTestName("bind-other"))
	const fileName = "shared.txt"
	uploadTestFile(t, client, sharedRepo, "/", fileName, "shared file body")
	uploadTestFile(t, client, otherRepo, "/", "private.txt", "never shared")

	shareToken := createShareLinkForTest(t, client, sharedRepo, "/"+fileName)
	linkToken := harvestShareLinkDownloadToken(t, client, shareToken, fileName)

	// otherRepo was never shared. The link bearer names sharedRepo. Crossing to
	// otherRepo must fail on the token/route mismatch alone, regardless of what
	// the token's owner may do in otherRepo.
	status := replaySyncToken(t, client, http.MethodGet, fmt.Sprintf("/seafhttp/repo/%s/commit/HEAD", otherRepo), linkToken)
	if status != http.StatusUnauthorized && status != http.StatusForbidden {
		t.Fatalf("commit/HEAD on an unshared library with a token minted for a different library = %d; want 401 or 403. "+
			"The token's RepoID is not bound to the route's :repo_id.", status)
	}
}

// Isolates the repository binding. Every other negative test here starts from a
// Source=="link" token, so a fix that only rejects link tokens would pass them
// all while leaving the binding absent. This one carries a fully legitimate
// repository sync token — Source=="", Path=="/" — for a library the caller
// genuinely owns, and points it at a different library they also own. Only
// `token.RepoID == c.Param("repo_id")` can refuse it.
func TestSyncAuthLegitimateTokenIsBoundToItsRepository(t *testing.T) {
	requireCassandra(t)

	client := adminClient
	repoA := createTestLibrary(t, client, syncLinkTestName("bind-legit-a"))
	repoB := createTestLibrary(t, client, syncLinkTestName("bind-legit-b"))
	uploadTestFile(t, client, repoA, "/", "a.txt", "library a")
	uploadTestFile(t, client, repoB, "/", "b.txt", "library b")

	syncToken := mintRepositorySyncToken(t, client, repoA)

	// Same owner on both sides, so checkSyncPermission would happily allow this.
	// A refusal can only come from the token naming repoA while the route names
	// repoB — which is the whole point of the assertion.
	status := replaySyncToken(t, client, http.MethodGet, fmt.Sprintf("/seafhttp/repo/%s/commit/HEAD", repoB), syncToken)
	if status != http.StatusUnauthorized && status != http.StatusForbidden {
		t.Fatalf("commit/HEAD on repoB with a legitimate sync token minted for repoA = %d; want 401 or 403. "+
			"Nothing compares the token's RepoID with the route's :repo_id.", status)
	}
}

// Isolates the source check, and covers the one link-token shape the other two
// conditions cannot refuse.
//
// A file share link mints a bearer carrying that file's path, so the Path=="/"
// rule alone would catch it. A link that shares the *library root* is different:
// its zip bearer carries Path=="/" and the shared library's own RepoID, so it
// satisfies both the path rule and the repository binding. Only Source=="" says
// no. "Share my whole library" is an ordinary thing to do, which is why this
// case has to be pinned rather than left to the other two checks.
func TestSyncAuthRejectsRootDirectoryShareLinkToken(t *testing.T) {
	requireCassandra(t)

	client := adminClient
	repoID := createTestLibrary(t, client, syncLinkTestName("root-dir-link"))
	uploadTestFile(t, client, repoID, "/", "inside.txt", "inside the shared library")

	// The whole library, not one file.
	shareToken := createShareLinkForTest(t, client, repoID, "/")

	resp := client.DoAnonymous(t, http.MethodGet, fmt.Sprintf("/api/v2.1/share-link-zip-task/?share_link_token=%s&path=/", shareToken))
	expectStatus(t, resp, http.StatusOK)
	payload := responseJSON(t, resp)
	zipToken, _ := payload["zip_token"].(string)
	if zipToken == "" {
		t.Fatalf("share-link zip task returned no zip_token: %v", payload)
	}

	status := replaySyncToken(t, client, http.MethodGet, fmt.Sprintf("/seafhttp/repo/%s/commit/HEAD", repoID), zipToken)
	if status != http.StatusUnauthorized && status != http.StatusForbidden {
		t.Fatalf("commit/HEAD with a root-directory share-link bearer = %d; want 401 or 403. "+
			"This token has Path==\"/\" and the right RepoID, so only the source check can refuse it.", status)
	}
}

// Isolates the token-shape check. This bearer is not a link token — it is an
// ordinary authenticated file-download token, Source=="", minted by the regular
// file API — but it is scoped to one file path rather than to the repository
// root. Replayed against the sync surface of its own repository, neither the
// source check nor the repository binding can refuse it; only `Path == "/"` can.
func TestSyncAuthRejectsFileScopedDownloadToken(t *testing.T) {
	requireCassandra(t)

	client := adminClient
	repoID := createTestLibrary(t, client, syncLinkTestName("file-scoped"))
	const fileName = "scoped.txt"
	uploadTestFile(t, client, repoID, "/", fileName, "file scoped body")

	resp := client.Get(t, fmt.Sprintf("/api2/repos/%s/file/?p=/%s", repoID, fileName))
	expectStatus(t, resp, http.StatusOK)
	fileURL := strings.Trim(responseBody(t, resp), "\" \n\r")
	fileToken := tokenFromSeafhttpFilesURL(t, fileURL)

	// Same repository, non-link source: this isolates the path shape.
	status := replaySyncToken(t, client, http.MethodGet, fmt.Sprintf("/seafhttp/repo/%s/commit/HEAD", repoID), fileToken)
	if status != http.StatusUnauthorized && status != http.StatusForbidden {
		t.Fatalf("commit/HEAD with a file-scoped download token for the same repository = %d; want 401 or 403. "+
			"A token issued to read one file authenticates the whole sync surface.", status)
	}
}

// Turns the write finding from inferred into observed. The earlier replay table
// records 400 on the write routes, which proves authentication and
// authorization were both passed and only body parsing failed — but a 400 is
// not a mutation. This drives a real PutBlock with a well-formed body into a
// library that was never shared, then asks the owner whether the object landed.
func TestSyncAuthLinkTokenCannotWriteToAnotherLibrary(t *testing.T) {
	requireCassandra(t)

	client := adminClient
	sharedRepo := createTestLibrary(t, client, syncLinkTestName("write-shared"))
	victimRepo := createTestLibrary(t, client, syncLinkTestName("write-victim"))
	const fileName = "shared.txt"
	uploadTestFile(t, client, sharedRepo, "/", fileName, "shared file body")
	uploadTestFile(t, client, victimRepo, "/", "victim.txt", "victim library")

	shareToken := createShareLinkForTest(t, client, sharedRepo, "/"+fileName)
	linkToken := harvestShareLinkDownloadToken(t, client, shareToken, fileName)

	payload := []byte(fmt.Sprintf("planted by a public share-link visitor %d", time.Now().UnixNano()))
	blockID := fmt.Sprintf("%x", sha256.Sum256(payload))

	// The owner confirms the block is absent first, so "absent afterwards" below
	// means the write was refused rather than that it was never distinguishable.
	if !blockIsNeeded(t, client, victimRepo, blockID) {
		t.Fatalf("block %s already exists in the victim library before the drill", blockID)
	}

	req, err := http.NewRequest(http.MethodPut,
		fmt.Sprintf("%s/seafhttp/repo/%s/block/%s?hash_type=sha256", client.baseURL, victimRepo, blockID),
		bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("building PutBlock request: %v", err)
	}
	req.Header.Set("Seafile-Repo-Token", linkToken)
	putResp, err := client.http.Do(req)
	if err != nil {
		t.Fatalf("PutBlock failed: %v", err)
	}
	putStatus := putResp.StatusCode
	putResp.Body.Close()

	// Both halves matter. A refusal that still wrote the block would be a broken
	// fix, and a 200 that wrote nothing would still be an authorization failure.
	landed := !blockIsNeeded(t, client, victimRepo, blockID)
	if landed {
		t.Errorf("a public share-link visitor wrote block %s into a library that was never shared "+
			"(PutBlock=%d, and the owner's check-blocks no longer reports it as needed). "+
			"This is an unauthorized block write into the victim library, not just an authorization bypass.", blockID, putStatus)
	}
	if putStatus != http.StatusUnauthorized && putStatus != http.StatusForbidden {
		t.Errorf("PutBlock into an unshared library with a share-link token = %d; want 401 or 403", putStatus)
	}
}

// The two credentials must not cross surfaces in either direction.
//
// Splitting TokenTypeSync out of TokenTypeDownload is only worth having if the
// separation holds both ways: a sync credential must not fetch file bytes, and
// a file bearer must not reach sync. The second direction is covered above;
// this pins the first, and pins that ordinary downloads did not break in the
// process — which is the regression a type split is most likely to cause.
func TestSyncAndDownloadTokensDoNotCrossSurfaces(t *testing.T) {
	requireCassandra(t)

	client := adminClient
	repoID := createTestLibrary(t, client, syncLinkTestName("cross-surface"))
	const fileName = "crossing.txt"
	uploadTestFile(t, client, repoID, "/", fileName, "crossing body")

	syncToken := mintRepositorySyncToken(t, client, repoID)

	resp := client.DoAnonymous(t, http.MethodGet, "/seafhttp/files/"+syncToken+"/"+fileName)
	status := resp.StatusCode
	resp.Body.Close()
	if status == http.StatusOK {
		t.Errorf("a repository sync token fetched file bytes from /seafhttp/files/ (status 200); "+
			"the download surface must not accept a sync credential")
	}

	// And the ordinary download path still works, with its own token.
	fileResp := client.Get(t, fmt.Sprintf("/api2/repos/%s/file/?p=/%s", repoID, fileName))
	expectStatus(t, fileResp, http.StatusOK)
	fileURL := strings.Trim(responseBody(t, fileResp), "\" \n\r")
	downloadToken := tokenFromSeafhttpFilesURL(t, fileURL)

	dl := client.DoAnonymous(t, http.MethodGet, "/seafhttp/files/"+downloadToken+"/"+fileName)
	defer dl.Body.Close()
	if dl.StatusCode != http.StatusOK {
		t.Fatalf("ordinary file download = %d, want 200; the token split broke downloads", dl.StatusCode)
	}
	body, err := io.ReadAll(dl.Body)
	if err != nil {
		t.Fatalf("reading download body: %v", err)
	}
	if string(body) != "crossing body" {
		t.Errorf("download body = %q, want %q", string(body), "crossing body")
	}
}

// An ordinary repository sync token must keep working, or the fix is a
// regression dressed up as a security improvement.
func TestSyncAuthStillAcceptsRepositorySyncTokens(t *testing.T) {
	requireCassandra(t)

	client := adminClient
	repoID := createTestLibrary(t, client, syncLinkTestName("normal-token"))
	uploadTestFile(t, client, repoID, "/", "file.txt", "body")

	infoResp := client.Get(t, fmt.Sprintf("/seafhttp/repo/%s/download-info", repoID))
	expectStatus(t, infoResp, http.StatusOK)
	payload := responseJSON(t, infoResp)
	syncToken, _ := payload["token"].(string)
	if syncToken == "" {
		t.Fatalf("download-info returned no sync token: %v", payload)
	}

	for _, path := range []string{"/commit/HEAD", "/permission-check", "/fs-id-list"} {
		status := replaySyncToken(t, client, http.MethodGet, "/seafhttp/repo/"+repoID+path, syncToken)
		if status != http.StatusOK {
			t.Errorf("GET %s with a legitimate repository sync token = %d; want 200", path, status)
		}
	}
}

// harvestShareLinkDownloadToken plays the anonymous visitor: request ?dl=1
// without following the redirect and read the bearer straight out of the
// Location header, which is exactly what a browser address bar would show.
func harvestShareLinkDownloadToken(t *testing.T, c *testClient, shareToken, fileName string) string {
	t.Helper()
	location := c.AnonymousRedirectLocation(t, fmt.Sprintf("/d/%s/?dl=1", shareToken))
	return tokenFromSeafhttpFilesURL(t, location)
}

// tokenFromSeafhttpFilesURL pulls the bearer out of a /seafhttp/files/<token>/<name>
// URL, which is the shape both the share-link redirect and the authenticated
// file API hand back.
func tokenFromSeafhttpFilesURL(t *testing.T, rawURL string) string {
	t.Helper()

	const marker = "/seafhttp/files/"
	idx := strings.Index(rawURL, marker)
	if idx < 0 {
		t.Fatalf("%q carries no /seafhttp/files/ bearer", rawURL)
	}
	rest := rawURL[idx+len(marker):]
	slash := strings.Index(rest, "/")
	if slash < 0 {
		t.Fatalf("%q has no filename segment after the token", rawURL)
	}
	token := rest[:slash]
	if token == "" {
		t.Fatalf("%q yielded an empty token", rawURL)
	}
	return token
}

// mintRepositorySyncToken obtains a genuine repository sync credential the way
// the desktop client does: an authenticated download-info call, which returns a
// token with Source=="" and Path=="/".
func mintRepositorySyncToken(t *testing.T, c *testClient, repoID string) string {
	t.Helper()

	resp := c.Get(t, fmt.Sprintf("/seafhttp/repo/%s/download-info", repoID))
	expectStatus(t, resp, http.StatusOK)
	payload := responseJSON(t, resp)
	token, _ := payload["token"].(string)
	if token == "" {
		t.Fatalf("download-info for %s returned no sync token: %v", repoID, payload)
	}
	return token
}

// blockIsNeeded asks, as the library owner, whether check-blocks still lists the
// block as missing. It is the owner's own authenticated view, so it reports
// what actually landed in storage rather than what the attacker was told.
func blockIsNeeded(t *testing.T, c *testClient, repoID, blockID string) bool {
	t.Helper()

	resp := c.PostJSON(t, fmt.Sprintf("/seafhttp/repo/%s/check-blocks", repoID), []string{blockID})
	expectStatus(t, resp, http.StatusOK)
	defer resp.Body.Close()

	var needed []string
	if err := json.NewDecoder(resp.Body).Decode(&needed); err != nil {
		t.Fatalf("decoding check-blocks response: %v", err)
	}
	for _, id := range needed {
		if strings.EqualFold(id, blockID) {
			return true
		}
	}
	return false
}

func replaySyncToken(t *testing.T, c *testClient, method, path, token string) int {
	t.Helper()
	resp := replaySyncTokenResponse(t, c, method, path, token)
	defer resp.Body.Close()
	return resp.StatusCode
}

// replaySyncTokenResponse sends the token the way a desktop client would, in
// the Seafile-Repo-Token header, and carries no other credential. POST bodies
// are empty JSON objects: the routes reject malformed payloads with 400, which
// is neither the 401/403 these tests demand nor a pass, so an empty document
// keeps the failure attributable to authentication rather than to parsing.
func replaySyncTokenResponse(t *testing.T, c *testClient, method, path, token string) *http.Response {
	t.Helper()

	var body *bytes.Buffer
	if method == http.MethodPost {
		body = bytes.NewBufferString("{}")
	}

	var req *http.Request
	var err error
	if body != nil {
		req, err = http.NewRequest(method, c.baseURL+path, body)
	} else {
		req, err = http.NewRequest(method, c.baseURL+path, nil)
	}
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Seafile-Repo-Token", token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("%s %s failed: %v", method, path, err)
	}
	return resp
}

// DoAnonymous sends a request with no credential of any kind.
func (c *testClient) DoAnonymous(t *testing.T, method, path string) *http.Response {
	t.Helper()

	req, err := http.NewRequest(method, c.baseURL+path, nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("%s %s failed: %v", method, path, err)
	}
	return resp
}

// AnonymousRedirectLocation performs an unauthenticated GET without following
// redirects and returns the Location header, which is how a share-link visitor
// first sees the download bearer.
func (c *testClient) AnonymousRedirectLocation(t *testing.T, path string) string {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	noFollow := &http.Client{
		Timeout: c.http.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := noFollow.Do(req)
	if err != nil {
		t.Fatalf("GET %s failed: %v", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 300 || resp.StatusCode > 399 {
		t.Fatalf("GET %s = %d; expected a redirect carrying the download bearer", path, resp.StatusCode)
	}
	location := resp.Header.Get("Location")
	if location == "" {
		t.Fatalf("GET %s returned %d with no Location header", path, resp.StatusCode)
	}
	return location
}

func syncLinkTestName(suffix string) string {
	return fmt.Sprintf("inttest-sync-%s-%d", suffix, time.Now().UnixNano())
}

func uploadTestFile(t *testing.T, c *testClient, repoID, parentDir, fileName, content string) {
	t.Helper()

	resp := c.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=%s", repoID, parentDir))
	expectStatus(t, resp, http.StatusOK)
	uploadURL := strings.Trim(responseBody(t, resp), "\" \n\r")

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatalf("writing upload content: %v", err)
	}
	if err := writer.WriteField("parent_dir", parentDir); err != nil {
		t.Fatalf("WriteField: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("closing multipart writer: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, uploadURL, &buf)
	if err != nil {
		t.Fatalf("building upload request: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Token "+c.token)

	uploadResp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("upload of %s failed: %v", fileName, err)
	}
	defer uploadResp.Body.Close()
	if uploadResp.StatusCode != http.StatusOK {
		t.Fatalf("upload of %s = %d", fileName, uploadResp.StatusCode)
	}
}
