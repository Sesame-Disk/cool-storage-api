//go:build integration

package integration

import (
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	apipkg "github.com/Sesame-Disk/sesamefs/internal/api"
	"github.com/Sesame-Disk/sesamefs/internal/db"
)

func TestSyncRecvFSBeforePutBlockPublishesDownloadableFile(t *testing.T) {
	requireCassandra(t)

	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-sync-protocol-%d", time.Now().UnixNano()))
	session := shareProjectionDBForTest(t).Session()
	initial := readLibrarySyncHeadState(t, session, repoID)

	fileName := "sync-local-file.txt"
	fileData := []byte("sync regression local-to-remote payload\nwith deterministic content\n")
	externalBlockID := syncSHA1HexForTest(fileData)
	internalBlockID := syncSHA256HexForTest(fileData)
	mtime := time.Now().Unix()

	fileObjectJSON := mustMarshalSyncObjectForTest(t, map[string]interface{}{
		"block_ids": []string{externalBlockID},
		"size":      int64(len(fileData)),
		"type":      1,
		"version":   1,
	})
	fileFSID := syncSHA1HexForTest(fileObjectJSON)

	rootObjectJSON := mustMarshalSyncObjectForTest(t, map[string]interface{}{
		"dirents": []apipkg.FSEntry{{
			ID:    fileFSID,
			Mode:  33188,
			Mtime: mtime,
			Name:  fileName,
			Size:  int64(len(fileData)),
		}},
		"type":    3,
		"version": 1,
	})
	rootFSID := syncSHA1HexForTest(rootObjectJSON)
	commitID := syncSHA1HexForTest([]byte(fmt.Sprintf("sync-regression-%s-%d", repoID, time.Now().UnixNano())))

	commitPayload := map[string]interface{}{
		"commit_id":   commitID,
		"repo_id":     repoID,
		"root_id":     rootFSID,
		"parent_id":   initial.HeadCommitID,
		"description": "integration sync recv-fs before put-block",
		"ctime":       time.Now().Unix(),
		"version":     1,
	}

	resp := doSyncProtocolRequestForTest(t, http.MethodPut, fmt.Sprintf("/seafhttp/repo/%s/commit/%s", repoID, commitID), mustMarshalSyncObjectForTest(t, commitPayload), "application/json")
	expectStatus(t, resp, http.StatusOK)

	packedFS := packSyncFSObjectsForTest(t,
		syncPackedFSObject{fsID: fileFSID, jsonData: fileObjectJSON},
		syncPackedFSObject{fsID: rootFSID, jsonData: rootObjectJSON},
	)
	resp = doSyncProtocolRequestForTest(t, http.MethodPost, fmt.Sprintf("/seafhttp/repo/%s/recv-fs", repoID), packedFS, "application/octet-stream")
	expectStatus(t, resp, http.StatusOK)

	resp = doSyncProtocolRequestForTest(t, http.MethodPut, fmt.Sprintf("/seafhttp/repo/%s/block/%s", repoID, externalBlockID), fileData, "application/octet-stream")
	expectStatus(t, resp, http.StatusOK)

	resp = doSyncProtocolRequestForTest(t, http.MethodPut, fmt.Sprintf("/seafhttp/repo/%s/commit/HEAD?head=%s", repoID, url.QueryEscape(commitID)), nil, "")
	expectStatus(t, resp, http.StatusOK)

	linkResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/file/?p=/%s", repoID, url.PathEscape(fileName)))
	expectStatus(t, linkResp, http.StatusOK)
	downloadURL := strings.Trim(responseBody(t, linkResp), "\" \n\r")
	if downloadURL == "" {
		t.Fatal("download URL is empty after sync HEAD publish")
	}

	req, err := http.NewRequest(http.MethodGet, downloadURL, nil)
	if err != nil {
		t.Fatalf("failed to create download request: %v", err)
	}
	downloadResp, err := adminClient.http.Do(req)
	if err != nil {
		t.Fatalf("download request failed: %v", err)
	}
	defer downloadResp.Body.Close()
	if downloadResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(downloadResp.Body)
		t.Fatalf("download returned status=%d body=%s", downloadResp.StatusCode, string(body))
	}
	downloaded, err := io.ReadAll(downloadResp.Body)
	if err != nil {
		t.Fatalf("failed to read downloaded payload: %v", err)
	}
	if !bytes.Equal(downloaded, fileData) {
		t.Fatalf("downloaded payload mismatch: got %q want %q", string(downloaded), string(fileData))
	}

	referrers := uploadedFileBlockReferrers(t, repoID, "/", fileName)
	permanentRef := db.BlockReferrerForFSObject(repoID, fileFSID)
	if !containsStringForTest(referrers, permanentRef) {
		t.Fatalf("block referrers = %v, want permanent ref %q", referrers, permanentRef)
	}
	if containsStringForTest(referrers, db.BlockReferrerForPublishAttempt(commitID)) {
		t.Fatalf("block referrers leaked publish-attempt ref: %v", referrers)
	}
	if containsStringForTest(referrers, db.BlockReferrerForUpload("sync:"+repoID+":"+internalBlockID)) {
		t.Fatalf("block referrers leaked sync upload ref: %v", referrers)
	}
}

type syncPackedFSObject struct {
	fsID     string
	jsonData []byte
}

func doSyncProtocolRequestForTest(t *testing.T, method, path string, body []byte, contentType string) *http.Response {
	t.Helper()

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, adminClient.baseURL+path, reader)
	if err != nil {
		t.Fatalf("failed to create %s %s request: %v", method, path, err)
	}
	req.Header.Set("Authorization", "Token "+adminClient.token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := adminClient.http.Do(req)
	if err != nil {
		t.Fatalf("%s %s failed: %v", method, path, err)
	}
	return resp
}

func packSyncFSObjectsForTest(t *testing.T, objects ...syncPackedFSObject) []byte {
	t.Helper()
	var packed bytes.Buffer
	for _, object := range objects {
		var compressed bytes.Buffer
		writer := zlib.NewWriter(&compressed)
		if _, err := writer.Write(object.jsonData); err != nil {
			t.Fatalf("failed to compress fs object %s: %v", object.fsID, err)
		}
		if err := writer.Close(); err != nil {
			t.Fatalf("failed to finalize compressed fs object %s: %v", object.fsID, err)
		}

		packed.WriteString(object.fsID)
		if err := binary.Write(&packed, binary.BigEndian, uint32(compressed.Len())); err != nil {
			t.Fatalf("failed to encode fs object length for %s: %v", object.fsID, err)
		}
		packed.Write(compressed.Bytes())
	}
	return packed.Bytes()
}

func mustMarshalSyncObjectForTest(t *testing.T, value interface{}) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("failed to marshal sync object: %v", err)
	}
	return data
}

func syncSHA1HexForTest(data []byte) string {
	hash := sha1.Sum(data)
	return hex.EncodeToString(hash[:])
}

func syncSHA256HexForTest(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

// TestSyncServesSHA1BlockIDsForCanonicalFSObject verifies the PR4 serve path on a
// post-flip file fs_object stored in the SHA-256-canonical layout (block_ids =
// SHA-256 storage ids, seafile_block_ids_sha1 = SHA-1): GetFSObject must serve the
// SHA-1 list to the Seafile client, and the served JSON must re-hash to the
// requested fs_id (otherwise the desktop client rejects the object).
func TestSyncServesSHA1BlockIDsForCanonicalFSObject(t *testing.T) {
	requireCassandra(t)

	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-sha1-serve-%d", time.Now().UnixNano()))
	session := shareProjectionDBForTest(t).Session()

	fileData := []byte("post-flip canonical serve payload\n")
	externalBlockID := syncSHA1HexForTest(fileData)   // SHA-1 (Seafile boundary id)
	internalBlockID := syncSHA256HexForTest(fileData) // SHA-256 (storage id)

	// fs_id is SHA-1 of the file-object JSON built from the SHA-1 block list — the
	// exact JSON GetFSObject re-emits (json.Marshal sorts the map keys).
	fileObj := map[string]interface{}{
		"block_ids": []string{externalBlockID},
		"size":      int64(len(fileData)),
		"type":      1,
		"version":   1,
	}
	canonicalJSON := mustMarshalSyncObjectForTest(t, fileObj)
	fileFSID := syncSHA1HexForTest(canonicalJSON)

	if err := session.Query(`
		INSERT INTO fs_objects (library_id, fs_id, obj_type, obj_name, size_bytes, mtime, block_ids, seafile_block_ids_sha1)
		VALUES (?, ?, 'file', '', ?, ?, ?, ?)
	`, repoID, fileFSID, int64(len(fileData)), time.Now().Unix(), []string{internalBlockID}, []string{externalBlockID}).Exec(); err != nil {
		t.Fatalf("failed to seed canonical fs_object: %v", err)
	}

	resp := doSyncProtocolRequestForTest(t, http.MethodGet, fmt.Sprintf("/seafhttp/repo/%s/fs/%s", repoID, fileFSID), nil, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET fs object status = %d, want 200", resp.StatusCode)
	}
	compressed, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	zr, err := zlib.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("zlib reader: %v", err)
	}
	served, err := io.ReadAll(zr)
	zr.Close()
	if err != nil {
		t.Fatalf("decompress served fs object: %v", err)
	}

	var parsed struct {
		BlockIDs []string `json:"block_ids"`
	}
	if err := json.Unmarshal(served, &parsed); err != nil {
		t.Fatalf("parse served fs object: %v", err)
	}
	if len(parsed.BlockIDs) != 1 || parsed.BlockIDs[0] != externalBlockID {
		t.Fatalf("served block_ids = %v, want SHA-1 [%s] (not the SHA-256 storage id %s)", parsed.BlockIDs, externalBlockID, internalBlockID)
	}
	if got := syncSHA1HexForTest(served); got != fileFSID {
		t.Fatalf("served JSON re-hash = %s, want fs_id %s (desktop would reject a mismatch)", got, fileFSID)
	}
}

// TestSyncRefusesToServeSHA256BlockIDsWithoutSHA1Column verifies the PR4 fail-closed
// guard (blocker #5): a row stuck with SHA-256 block_ids and an empty
// seafile_block_ids_sha1 must NOT be served, since handing the client a 64-hex
// SHA-256 list would corrupt its fs_id verification.
func TestSyncRefusesToServeSHA256BlockIDsWithoutSHA1Column(t *testing.T) {
	requireCassandra(t)

	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-sha1-guard-%d", time.Now().UnixNano()))
	session := shareProjectionDBForTest(t).Session()

	internalBlockID := syncSHA256HexForTest([]byte("guard payload"))
	fsID := syncSHA1HexForTest([]byte(fmt.Sprintf("guard-%s-%d", repoID, time.Now().UnixNano())))

	// Deliberately broken row: SHA-256 block_ids, no seafile_block_ids_sha1.
	if err := session.Query(`
		INSERT INTO fs_objects (library_id, fs_id, obj_type, obj_name, size_bytes, mtime, block_ids)
		VALUES (?, ?, 'file', '', ?, ?, ?)
	`, repoID, fsID, int64(13), time.Now().Unix(), []string{internalBlockID}).Exec(); err != nil {
		t.Fatalf("failed to seed broken fs_object: %v", err)
	}

	resp := doSyncProtocolRequestForTest(t, http.MethodGet, fmt.Sprintf("/seafhttp/repo/%s/fs/%s", repoID, fsID), nil, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("GET broken fs object status = %d, want 500 (fail closed)", resp.StatusCode)
	}
}

func containsStringForTest(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
