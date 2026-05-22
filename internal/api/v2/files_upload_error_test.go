package v2

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestWriteUploadFileError_MapsSentinelErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantError  string
	}{
		{name: "head conflict", err: ErrLibraryHeadConflict, wantStatus: http.StatusConflict, wantError: "library was modified concurrently; retry the upload"},
		{name: "wrapped head conflict (mirrors retry_exhausted production path)", err: fmt.Errorf("%w: failed to finalize upload metadata after 20 attempts", ErrLibraryHeadConflict), wantStatus: http.StatusConflict, wantError: "library was modified concurrently; retry the upload"},
		{name: "quota exceeded", err: errUploadStorageQuotaExceeded, wantStatus: http.StatusForbidden, wantError: "storage quota exceeded"},
		{name: "generic", err: errors.New("boom"), wantStatus: http.StatusInternalServerError, wantError: "failed to update library"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			writeUploadFileError(c, tt.err)

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", w.Code, tt.wantStatus)
			}

			var resp map[string]interface{}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			if got := resp["error"]; got != tt.wantError {
				t.Fatalf("error = %v, want %q", got, tt.wantError)
			}
		})
	}
}
