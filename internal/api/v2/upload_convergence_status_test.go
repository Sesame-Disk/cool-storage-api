package v2

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// ErrBlockCanonicalStateNotVisible means the block IS installed and durable and only
// this request could not observe its canonical row before the confirmation budget ran
// out. That is the same transient class as a GC delete fence, and the three block
// funnels (web /blocks/upload, seafhttp, sync PutBlock) already answer 409 + Retry-After.
//
// The file-level surfaces fell through to a 500, so one transient state was reported
// two different ways: a healthy block looked like a server fault on UploadFile and
// CreateFile, and was indistinguishable from a real failure in monitoring. These tests
// pin the uniform answer so it cannot silently drift back to 500.

func TestWriteUploadFileErrorMapsCanonicalConvergenceTo409(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	writeUploadFileError(c, fmt.Errorf("materialize: %w", ErrBlockCanonicalStateNotVisible))

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; the block is installed, this is convergence, not a server fault", w.Code)
	}
	if retryAfter := w.Header().Get("Retry-After"); retryAfter == "" {
		t.Errorf("Retry-After = %q, want a retry hint like the fence response", retryAfter)
	}
}

// The sibling sentinel must keep its existing answer; the new case is additive.
func TestWriteUploadFileErrorStillMapsDeleteFenceTo409(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	writeUploadFileError(c, fmt.Errorf("materialize: %w", ErrBlockDeleteInProgress))

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
}

// An unrelated failure must still be a 500: the point is to classify one specific
// transient state, not to soften every materialization error into a retryable answer.
func TestWriteUploadFileErrorKeeps500ForUnknownFailures(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	writeUploadFileError(c, fmt.Errorf("something genuinely broken"))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
}
