//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
)

func TestNeedsPutUsesCanonicalMinIOBucket(t *testing.T) {
	ctx := context.Background()
	newStore := func(bucket string) *storage.S3Store {
		t.Helper()
		store, err := storage.NewS3Store(ctx, storage.S3Config{
			Endpoint:        envOrDefault("S3_ENDPOINT", "http://minio:9000"),
			Bucket:          bucket,
			Region:          envOrDefault("S3_REGION", "us-east-1"),
			AccessKeyID:     envOrDefault("S3_ACCESS_KEY_ID", "minioadmin"),
			SecretAccessKey: envOrDefault("S3_SECRET_ACCESS_KEY", "minioadmin"),
			UsePathStyle:    true,
		})
		if err != nil {
			t.Fatalf("NewS3Store(%s): %v", bucket, err)
		}
		if err := store.HeadBucket(ctx); err != nil {
			t.Fatalf("required MinIO bucket %s unavailable: %v", bucket, err)
		}
		return store
	}

	preferredBucket := envOrDefault("S3_TEST_PREFERRED_BUCKET", "sesamefs-eu")
	canonicalBucket := envOrDefault("S3_TEST_CANONICAL_BUCKET", "sesamefs-usa")
	if preferredBucket == canonicalBucket {
		t.Fatalf("preferred and canonical MinIO buckets must differ, both are %q", preferredBucket)
	}
	preferredS3 := newStore(preferredBucket)
	canonicalS3 := newStore(canonicalBucket)
	name := fmt.Sprintf("inttest-canonical-needs-put-%d", time.Now().UnixNano())
	createResp := adminClient.PostJSONWithHost(t, "/api2/repos/", map[string]string{
		"name":       name,
		"storage_id": "hot-s3-eu",
	}, "eu.sesamefs.local")
	expectStatus(t, createResp, http.StatusOK)
	created := responseJSON(t, createResp)
	repoID, _ := created["repo_id"].(string)
	if repoID == "" {
		t.Fatalf("create library response missing repo_id: %v", created)
	}
	t.Cleanup(func() {
		resp := adminClient.Delete(t, fmt.Sprintf("/api/v2.1/repos/%s/", repoID))
		resp.Body.Close()
	})

	database := shareProjectionDBForTest(t)
	var orgID string
	if err := database.Session().Query(`SELECT org_id FROM libraries_by_id WHERE library_id = ?`, repoID).Scan(&orgID); err != nil {
		t.Fatalf("resolve library org: %v", err)
	}
	preferredStore, err := storage.NewOrgBlockStore(preferredS3, "blocks/", orgID)
	if err != nil {
		t.Fatalf("preferred block store: %v", err)
	}
	canonicalStore, err := storage.NewOrgBlockStore(canonicalS3, "blocks/", orgID)
	if err != nil {
		t.Fatalf("canonical block store: %v", err)
	}
	data := []byte("canonical NeedsPut integration payload")
	sha1Bytes := sha1.Sum(data)
	externalBlockID := hex.EncodeToString(sha1Bytes[:])
	hashBytes := sha256.Sum256(data)
	blockID := hex.EncodeToString(hashBytes[:])
	canonicalKey := canonicalStore.StorageKeyForHash(blockID)
	preferredKey := preferredStore.StorageKeyForHash(blockID)

	operationID := "sync:" + repoID + ":" + blockID
	referrer := db.BlockReferrerForUpload(operationID)
	webFilename := "canonical-session.bin"
	webFSID := webFileFSID(t, []string{externalBlockID}, len(data))
	webReferrer := db.BlockReferrerForFSObject(repoID, webFSID)
	t.Cleanup(func() {
		for _, cleanupReferrer := range []string{referrer, webReferrer} {
			if err := database.RemoveBlockReference(orgID, blockID, cleanupReferrer); err != nil {
				t.Errorf("cleanup block reference %s: %v", cleanupReferrer, err)
			}
		}
		if err := database.DeleteProvisionalBlockReferenceExpiry(orgID, blockID, referrer, time.Time{}); err != nil {
			t.Errorf("cleanup provisional expiry: %v", err)
		}
		if err := database.Session().Query(`DELETE FROM block_id_mappings WHERE org_id = ? AND representation_id = ? AND external_id = ?`, orgID, db.PlainBlockRepresentationID, externalBlockID).Exec(); err != nil {
			t.Errorf("cleanup block mapping: %v", err)
		}
		if err := database.Session().Query(`DELETE FROM blocks WHERE org_id = ? AND block_id = ?`, orgID, blockID).Exec(); err != nil {
			t.Errorf("cleanup block metadata: %v", err)
		}
		if err := preferredStore.DeleteBlock(ctx, blockID); err != nil {
			t.Errorf("cleanup preferred object: %v", err)
		}
		if err := canonicalStore.DeleteBlock(ctx, blockID); err != nil {
			t.Errorf("cleanup canonical object: %v", err)
		}
	})
	if err := preferredStore.DeleteBlock(ctx, blockID); err != nil {
		t.Fatalf("remove stale preferred object: %v", err)
	}
	if err := canonicalStore.DeleteBlock(ctx, blockID); err != nil {
		t.Fatalf("remove stale canonical object: %v", err)
	}

	if err := database.UpsertBlockMetadata(orgID, blockID, len(data), "hot-s3-usa", canonicalKey); err != nil {
		t.Fatalf("seed canonical block metadata: %v", err)
	}
	if hasReferences, err := database.BlockHasReferences(orgID, blockID); err != nil || hasReferences {
		t.Fatalf("seeded block references = %v, %v; want false, nil", hasReferences, err)
	}
	probe, err := database.ProbeBlockReuse(orgID, blockID)
	if err != nil {
		t.Fatalf("ProbeBlockReuse(NeedsPut): %v", err)
	}
	if probe.Decision != db.BlockReuseNeedsPut || probe.StorageClass != "hot-s3-usa" || probe.StorageKey != canonicalKey {
		t.Fatalf("probe = %+v, want existing canonical NeedsPut metadata", probe)
	}

	putResp := doSyncProtocolRequestForTest(t, http.MethodPut, fmt.Sprintf("/seafhttp/repo/%s/block/%s", repoID, externalBlockID), data, "application/octet-stream")
	expectStatus(t, putResp, http.StatusOK)
	canonicalExists, err := canonicalStore.ObjectExists(ctx, canonicalKey)
	if err != nil {
		t.Fatalf("canonical ObjectExists(%q): %v", canonicalKey, err)
	}
	preferredExists, err := preferredStore.ObjectExists(ctx, preferredKey)
	if err != nil {
		t.Fatalf("preferred ObjectExists(%q): %v", preferredKey, err)
	}
	if !canonicalExists || preferredExists {
		t.Fatalf("canonical/preferred existence = %v/%v, want true/false", canonicalExists, preferredExists)
	}

	location, found, err := database.GetBlockStorageLocation(ctx, orgID, blockID)
	if err != nil || !found {
		t.Fatalf("GetBlockStorageLocation() = %+v, %v, %v", location, found, err)
	}
	if location.StorageClass != "hot-s3-usa" || location.StorageKey != canonicalKey {
		t.Fatalf("canonical metadata = %+v, want hot-s3-usa/%s", location, canonicalKey)
	}
	if mapped, ok, err := database.GetBlockIDMapping(orgID, db.PlainBlockRepresentationID, externalBlockID); err != nil || !ok || mapped != blockID {
		t.Fatalf("mapping = %q, %v, %v; want %s/true/nil", mapped, ok, err, blockID)
	}

	getResp := doSyncProtocolRequestForTest(t, http.MethodGet, fmt.Sprintf("/seafhttp/repo/%s/block/%s", repoID, externalBlockID), nil, "")
	expectStatus(t, getResp, http.StatusOK)
	got, err := io.ReadAll(getResp.Body)
	getResp.Body.Close()
	if err != nil {
		t.Fatalf("read Sync GET body: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("Sync GET bytes = %q, want %q", got, data)
	}

	// Recreate the original session regression: metadata remains pinned to USA,
	// its canonical object disappears, and the library still prefers EU. The web
	// session must repair USA, verify USA at commit, and remain readable afterward.
	if err := canonicalStore.DeleteBlock(ctx, blockID); err != nil {
		t.Fatalf("remove canonical object before session repair: %v", err)
	}
	sessionID := webCreateBlockSession(t, adminClient, repoID, "/", int64(len(data)))
	existing, missing := webCheckBlocks(t, adminClient, sessionID, []string{strings.ToUpper(blockID)})
	if len(existing) != 0 || len(missing) != 1 || missing[0] != strings.ToUpper(blockID) {
		t.Fatalf("session check existing/missing = %v/%v, want []/[%s]", existing, missing, strings.ToUpper(blockID))
	}
	uploadResp := webUploadBlock(t, adminClient, sessionID, data)
	if uploadResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(uploadResp.Body)
		uploadResp.Body.Close()
		t.Fatalf("session upload status %d: %s", uploadResp.StatusCode, body)
	}
	uploadResp.Body.Close()
	canonicalExists, err = canonicalStore.ObjectExists(ctx, canonicalKey)
	if err != nil {
		t.Fatalf("canonical ObjectExists after session upload: %v", err)
	}
	preferredExists, err = preferredStore.ObjectExists(ctx, preferredKey)
	if err != nil {
		t.Fatalf("preferred ObjectExists after session upload: %v", err)
	}
	if !canonicalExists || preferredExists {
		t.Fatalf("session canonical/preferred existence = %v/%v, want true/false", canonicalExists, preferredExists)
	}
	commitResp := webCommit(t, adminClient, repoID, map[string]interface{}{
		"session": sessionID, "parent_dir": "/", "filename": webFilename,
		"replace": false, "size": len(data),
		"blocks": []map[string]interface{}{{"sha256": strings.ToUpper(blockID), "size": len(data)}},
	})
	expectStatus(t, commitResp, http.StatusOK)
	commitResp.Body.Close()
	if downloaded := downloadRepoFile(t, adminClient, repoID, "/"+webFilename); !bytes.Equal(downloaded, data) {
		t.Fatalf("web session download bytes = %q, want %q", downloaded, data)
	}
}
