//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRestoreJobsRegression_CreateListStatusAndDetail(t *testing.T) {
	repoName := fmt.Sprintf("inttest-restore-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, repoName)
	session := shareProjectionDBForTest(t).Session()
	restorePath := "/archive/restore-target.bin"
	jobID := uuid.NewString()
	requestedAt := time.Now().UTC().Truncate(time.Millisecond)

	var orgID string
	if err := session.Query(`
		SELECT org_id FROM libraries_by_id WHERE library_id = ?
	`, repoID).Scan(&orgID); err != nil {
		t.Fatalf("failed to resolve org_id for repo %s: %v", repoID, err)
	}

	if err := session.Query(`
		INSERT INTO restore_jobs (org_id, job_id, library_id, block_ids, status, requested_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, orgID, jobID, repoID, []string{"block-a"}, "pending", requestedAt).Exec(); err != nil {
		t.Fatalf("failed to seed restore job %s: %v", jobID, err)
	}
	t.Cleanup(func() {
		if err := session.Query(`
			DELETE FROM restore_jobs WHERE org_id = ? AND library_id = ? AND job_id = ?
		`, orgID, repoID, jobID).Exec(); err != nil {
			t.Errorf("cleanup restore job %s failed: %v", jobID, err)
		}
	})

	statusResp := adminClient.Get(t, fmt.Sprintf("/api/v2/repos/%s/file/restore-status?p=%s", repoID, url.QueryEscape(restorePath)))
	if statusResp.StatusCode != http.StatusOK {
		body := responseBody(t, statusResp)
		t.Fatalf("restore-status returned status=%d body=%s", statusResp.StatusCode, body)
	}
	statusResult := responseJSON(t, statusResp)
	if got, _ := statusResult["job_id"].(string); got != jobID {
		t.Fatalf("restore status job_id = %q, want %q", got, jobID)
	}
	if got, _ := statusResult["status"].(string); got != "pending" {
		t.Fatalf("restore status = %q, want pending", got)
	}

	listResp := adminClient.Get(t, "/api/v2/restore-jobs")
	if listResp.StatusCode != http.StatusOK {
		body := responseBody(t, listResp)
		t.Fatalf("restore-jobs list returned status=%d body=%s", listResp.StatusCode, body)
	}
	var jobs []map[string]interface{}
	decodeJSON(t, listResp, &jobs)

	var listed bool
	for _, job := range jobs {
		listedJobID, _ := job["job_id"].(string)
		listedRepoID, _ := job["library_id"].(string)
		if listedJobID == jobID && listedRepoID == repoID {
			listed = true
			if got, _ := job["status"].(string); got != "pending" {
				t.Fatalf("listed restore job status = %q, want pending", got)
			}
			break
		}
	}
	if !listed {
		t.Fatalf("restore job %s for repo %s not found in list response: %v", jobID, repoID, jobs)
	}

	detailResp := adminClient.Get(t, fmt.Sprintf("/api/v2/repos/%s/restore-jobs/%s", repoID, jobID))
	if detailResp.StatusCode != http.StatusOK {
		body := responseBody(t, detailResp)
		t.Fatalf("restore job detail returned status=%d body=%s", detailResp.StatusCode, body)
	}
	detailResult := responseJSON(t, detailResp)
	if got, _ := detailResult["job_id"].(string); got != jobID {
		t.Fatalf("restore detail job_id = %q, want %q", got, jobID)
	}
	if got, _ := detailResult["library_id"].(string); got != repoID {
		t.Fatalf("restore detail library_id = %q, want %q", got, repoID)
	}
	if got, _ := detailResult["status"].(string); got != "pending" {
		t.Fatalf("restore detail status = %q, want pending", got)
	}
}