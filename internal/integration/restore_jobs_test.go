//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestRestoreJobsRegression_CreateListStatusAndDetail(t *testing.T) {
	repoName := fmt.Sprintf("inttest-restore-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, repoName)
	restorePath := "/archive/restore-target.bin"

	initiateResp := adminClient.PostJSON(t, fmt.Sprintf("/api/v2/repos/%s/file/restore", repoID), map[string]string{
		"path": restorePath,
	})
	if initiateResp.StatusCode != http.StatusAccepted {
		body := responseBody(t, initiateResp)
		t.Fatalf("restore initiation returned status=%d body=%s", initiateResp.StatusCode, body)
	}
	initiateResult := responseJSON(t, initiateResp)
	jobID, _ := initiateResult["job_id"].(string)
	if jobID == "" {
		t.Fatalf("restore initiation response missing job_id: %v", initiateResult)
	}
	if got, _ := initiateResult["status"].(string); got != "pending" {
		t.Fatalf("restore initiation status = %q, want pending", got)
	}

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
