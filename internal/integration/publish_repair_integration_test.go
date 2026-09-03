//go:build integration

package integration

import (
	"errors"
	"fmt"
	"hash/fnv"
	"net/url"
	"strings"
	"testing"
	"time"

	v2api "github.com/Sesame-Disk/sesamefs/internal/api/v2"
	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

type publishRepairIntegrationFileState struct {
	orgID            string
	headCommitID     string
	fsID             string
	internalBlockIDs []string
}

func TestPublishedBlockReferenceRepairWorker_ReplaysReachableQueuedRepairAfterRestart(t *testing.T) {
	requireCassandra(t)

	database := shareProjectionDBForTest(t)
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-publish-repair-replay-%d", time.Now().UnixNano()))
	fileName := "repair-replay.txt"
	fileContent := fmt.Sprintf("repair replay content %d\n", time.Now().UnixNano())

	uploadURL := getUploadLink(t, adminClient, repoID, "/")
	uploadFileThroughLink(t, adminClient, uploadURL, fileName, "/", fileContent)

	state := publishRepairIntegrationReadFileState(t, repoID, "/", fileName)
	if len(state.internalBlockIDs) != 1 {
		t.Fatalf("internalBlockIDs = %v, want exactly one block for focused repair replay test", state.internalBlockIDs)
	}

	fsReferrer := dbpkg.BlockReferrerForFSObject(repoID, state.fsID)
	pubReferrer := dbpkg.BlockReferrerForPublishAttempt(state.headCommitID)
	for _, blockID := range state.internalBlockIDs {
		if err := database.RemoveBlockReference(state.orgID, blockID, fsReferrer); err != nil {
			t.Fatalf("failed to remove fs ref %q for block %s: %v", fsReferrer, blockID, err)
		}
		if err := database.AddBlockReference(state.orgID, blockID, pubReferrer, repoID, 0); err != nil {
			t.Fatalf("failed to add pub ref %q for block %s: %v", pubReferrer, blockID, err)
		}
	}
	if err := v2api.QueuePublishedFSObjectBlockReferenceRepair(database, state.orgID, repoID, state.headCommitID, state.fsID, state.internalBlockIDs); err != nil {
		t.Fatalf("failed to queue durable publish repair: %v", err)
	}

	bucket := publishRepairIntegrationBucket(state.orgID, repoID, state.headCommitID, state.fsID)
	staleCreatedAt := time.Now().UTC().Add(-time.Minute)
	leaseExpiresAt := time.Now().UTC().Add(4 * time.Minute)
	if err := database.Session().Query(`
		UPDATE published_block_reference_repairs
		SET created_at = ?, lease_expires_at = ?
		WHERE bucket = ? AND org_id = ? AND repo_id = ? AND commit_id = ? AND fs_id = ?
	`, staleCreatedAt, leaseExpiresAt, bucket, state.orgID, repoID, state.headCommitID, state.fsID).Exec(); err != nil {
		t.Fatalf("failed to backdate queued publish repair row: %v", err)
	}

	t.Cleanup(func() {
		_ = database.Session().Query(`
			DELETE FROM published_block_reference_repairs
			WHERE bucket = ? AND org_id = ? AND repo_id = ? AND commit_id = ? AND fs_id = ?
		`, bucket, state.orgID, repoID, state.headCommitID, state.fsID).Exec()
		for _, blockID := range state.internalBlockIDs {
			_ = database.RemoveBlockReference(state.orgID, blockID, pubReferrer)
			_ = database.AddBlockReference(state.orgID, blockID, fsReferrer, repoID, 0)
		}
	})

	publishRepairIntegrationAssertReferrers(t, repoID, "/", fileName, func(referrers []string) {
		if !publishRepairIntegrationHasReferrer(referrers, pubReferrer) {
			t.Fatalf("expected seeded pub ref %q before replay, got %v", pubReferrer, referrers)
		}
		if publishRepairIntegrationHasReferrer(referrers, fsReferrer) {
			t.Fatalf("expected fs ref %q to be removed before replay, got %v", fsReferrer, referrers)
		}
	})
	if !publishRepairIntegrationRepairRowExists(t, bucket, state.orgID, repoID, state.headCommitID, state.fsID) {
		t.Fatal("queued publish repair row missing before worker start")
	}

	v2api.StartPublishedBlockReferenceRepairer(database)

	// StartPublishedBlockReferenceRepairer's initial sweep is sync.Once-gated
	// process-wide: whichever test in this binary calls it FIRST gets that
	// immediate pass, and every other caller -- including this test, once
	// internal/integration/createfilefromblocks_ambiguous_head_test.go also
	// started calling this same function -- must wait for the next periodic
	// tick (publishedBlockReferenceRepairSweepInterval, ~1 minute) instead.
	// 10s only ever worked because this used to be the only caller.
	if !pollUntil(t, 75*time.Second, time.Second, func() bool {
		referrers := uploadedFileBlockReferrers(t, repoID, "/", fileName)
		return publishRepairIntegrationHasReferrer(referrers, fsReferrer) &&
			!publishRepairIntegrationHasReferrer(referrers, pubReferrer) &&
			!publishRepairIntegrationRepairRowExists(t, bucket, state.orgID, repoID, state.headCommitID, state.fsID)
	}) {
		referrers := uploadedFileBlockReferrers(t, repoID, "/", fileName)
		t.Fatalf("timed out waiting for durable publish replay; referrers=%v rowExists=%v", referrers, publishRepairIntegrationRepairRowExists(t, bucket, state.orgID, repoID, state.headCommitID, state.fsID))
	}
}

func publishRepairIntegrationReadFileState(t *testing.T, repoID, dirPath, fileName string) publishRepairIntegrationFileState {
	t.Helper()

	orgID := resolveOrgID(t, repoID)
	session := shareProjectionDBForTest(t).Session()
	fileFSID := publishRepairIntegrationLookupFileFSID(t, repoID, dirPath, fileName)

	var externalBlockIDs []string
	if err := session.Query(`SELECT block_ids FROM fs_objects WHERE library_id = ? AND fs_id = ?`, repoID, fileFSID).Scan(&externalBlockIDs); err != nil {
		t.Fatalf("failed to load block ids for %s/%s: %v", repoID, fileFSID, err)
	}
	if len(externalBlockIDs) == 0 {
		t.Fatalf("file %s/%s has no block ids", repoID, fileFSID)
	}

	internalBlockIDs := make([]string, 0, len(externalBlockIDs))
	for _, externalBlockID := range externalBlockIDs {
		var internalBlockID string
		err := session.Query(`SELECT internal_id FROM block_id_mappings WHERE org_id = ? AND representation_id = ? AND external_id = ?`, orgID, dbpkg.PlainBlockRepresentationID, externalBlockID).Scan(&internalBlockID)
		if err != nil {
			if errors.Is(err, gocql.ErrNotFound) {
				internalBlockID = externalBlockID
			} else {
				t.Fatalf("failed to resolve block mapping for %s/%s: %v", orgID, externalBlockID, err)
			}
		}
		internalBlockIDs = append(internalBlockIDs, internalBlockID)
	}

	var headCommitID string
	if err := session.Query(`SELECT head_commit_id FROM libraries WHERE org_id = ? AND library_id = ?`, orgID, repoID).Scan(&headCommitID); err != nil {
		t.Fatalf("failed to read head commit for repo %s: %v", repoID, err)
	}
	if strings.TrimSpace(headCommitID) == "" {
		t.Fatalf("repo %s has empty head commit id", repoID)
	}

	return publishRepairIntegrationFileState{
		orgID:            orgID,
		headCommitID:     headCommitID,
		fsID:             fileFSID,
		internalBlockIDs: internalBlockIDs,
	}
}

func publishRepairIntegrationLookupFileFSID(t *testing.T, repoID, dirPath, fileName string) string {
	t.Helper()

	listResp := adminClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=%s", repoID, url.QueryEscape(dirPath)))
	expectStatus(t, listResp, 200)
	listResult := responseJSON(t, listResp)
	entries, _ := listResult["dirent_list"].([]interface{})
	for _, rawEntry := range entries {
		entry, _ := rawEntry.(map[string]interface{})
		if name, _ := entry["name"].(string); name == fileName {
			if fsID, _ := entry["id"].(string); strings.TrimSpace(fsID) != "" {
				return fsID
			}
		}
	}
	t.Fatalf("file %q not found in repo=%s dir=%s", fileName, repoID, dirPath)
	return ""
}

func publishRepairIntegrationRepairRowExists(t *testing.T, bucket int, orgID, repoID, commitID, fsID string) bool {
	t.Helper()

	var storedFSID string
	err := shareProjectionDBForTest(t).Session().Query(`
		SELECT fs_id FROM published_block_reference_repairs
		WHERE bucket = ? AND org_id = ? AND repo_id = ? AND commit_id = ? AND fs_id = ?
	`, bucket, orgID, repoID, commitID, fsID).Scan(&storedFSID)
	if errors.Is(err, gocql.ErrNotFound) {
		return false
	}
	if err != nil {
		t.Fatalf("failed to read queued publish repair row: %v", err)
	}
	return storedFSID != ""
}

func publishRepairIntegrationAssertReferrers(t *testing.T, repoID, dirPath, fileName string, assertFn func([]string)) {
	t.Helper()
	assertFn(uploadedFileBlockReferrers(t, repoID, dirPath, fileName))
}

func publishRepairIntegrationHasReferrer(referrers []string, want string) bool {
	for _, referrer := range referrers {
		if referrer == want {
			return true
		}
	}
	return false
}

func publishRepairIntegrationBucket(orgID, repoID, commitID, fsID string) int {
	hasher := fnv.New32a()
	for _, part := range []string{orgID, repoID, commitID, fsID} {
		_, _ = hasher.Write([]byte(part))
		_, _ = hasher.Write([]byte{0})
	}
	return int(hasher.Sum32() % 32)
}
