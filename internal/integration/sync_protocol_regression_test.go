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

func containsStringForTest(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
