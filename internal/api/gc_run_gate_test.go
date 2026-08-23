package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// handleGCRun is the HTTP face of the GC kill switch. The Service-level guard is
// pinned in internal/gc; these tests pin the endpoint contract, because the two
// failures that actually reach an operator are HTTP-shaped: a disabled node
// reporting {"started":true}, and a disabled node accepting a config mutation for
// a service that will never run.

func newGCRunTestServer(t *testing.T, enabled bool, start bool) *Server {
	t.Helper()
	// Long intervals so the loops never fire on their own — these tests are about
	// the gate, not about what a run does.
	svc := gc.NewService(gc.NewMockStore(), nil, config.GCConfig{
		Enabled:        enabled,
		BatchSize:      10,
		DryRun:         false,
		WorkerInterval: 10 * time.Minute,
		ScanInterval:   10 * time.Minute,
		GracePeriod:    time.Hour,
	}, nil)
	if start {
		svc.Start()
		t.Cleanup(svc.Stop)
	}
	return &Server{gcService: svc}
}

func postGCRun(t *testing.T, s *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/gc/run", s.handleGCRun)

	req := httptest.NewRequest("POST", "/gc/run", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestHandleGCRun_DisabledNodeRefuses(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"worker", `{"type":"worker"}`},
		{"scanner", `{"type":"scanner"}`},
		{"defaulted type", `{}`},
		{"unparseable body", `not json`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newGCRunTestServer(t, false, true)
			w := postGCRun(t, s, tc.body)

			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
			}
			var resp map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("parse response: %v", err)
			}
			if started, _ := resp["started"].(bool); started {
				t.Errorf(`response reported started=true on a disabled node: %s`, w.Body.String())
			}
		})
	}
}

func TestHandleGCRun_DisabledNodeDoesNotApplyDryRunOverride(t *testing.T) {
	// The refusal must precede SetDryRun. Otherwise a disabled node silently
	// accepts a config mutation for a service it will never run — and on the next
	// enable it would start with a dry-run setting nobody chose at boot.
	s := newGCRunTestServer(t, false, true)

	if s.gcService.Status().DryRun {
		t.Fatal("precondition: DryRun should start false")
	}

	w := postGCRun(t, s, `{"type":"worker","dry_run":true}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	if s.gcService.Status().DryRun {
		t.Error("dry_run override was applied on a disabled node; the refusal must come first")
	}
}

func TestHandleGCRun_NotStartedRefuses(t *testing.T) {
	// Enabled but never started: still no consumer loop, so still not "started".
	s := newGCRunTestServer(t, true, false)

	w := postGCRun(t, s, `{"type":"worker"}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestHandleGCRun_EnabledAndStartedAccepts(t *testing.T) {
	s := newGCRunTestServer(t, true, true)

	for _, tc := range []struct{ name, body string }{
		{"worker", `{"type":"worker"}`},
		{"scanner", `{"type":"scanner"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := postGCRun(t, s, tc.body)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, http.StatusOK, w.Body.String())
			}
			var resp map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("parse response: %v", err)
			}
			if started, _ := resp["started"].(bool); !started {
				t.Errorf("started = false on an enabled, started node: %s", w.Body.String())
			}
		})
	}
}

func TestHandleGCRun_NoServiceReturnsUnavailable(t *testing.T) {
	s := &Server{}
	w := postGCRun(t, s, `{"type":"worker"}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestHandleGCFailedItemMutations_DisabledNodeReturns503(t *testing.T) {
	// The DLQ mutations claim the GC lease, so a disabled replica answering them
	// would take leadership from the datacenter that actually drains the queue.
	// They must refuse, and refuse as 503 — "this node will not serve it" — not as
	// a 500 that reads like a server fault.
	s := newGCRunTestServer(t, false, true)

	for _, tc := range []struct {
		name    string
		handler gin.HandlerFunc
	}{
		{"requeue", s.handleGCFailedItemRequeue},
		{"delete", s.handleGCFailedItemDelete},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			r := gin.New()
			r.POST("/gc/failed", tc.handler)

			target := "/gc/failed?org_id=" + uuid.New().String() +
				"&failed_at=" + url.QueryEscape(time.Now().UTC().Format(time.RFC3339Nano)) +
				"&item_type=block&item_id=block-1"
			req := httptest.NewRequest("POST", target, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, http.StatusServiceUnavailable, w.Body.String())
			}
		})
	}
}

func TestHandleGCFailedItemMutations_CancelledRequestReturns503(t *testing.T) {
	// Shutdown cancels the in-flight DLQ operation while the HTTP server is still
	// draining. The store binds ctx only to the read phase and re-checks it right
	// before the commit, so a cancelled request wrote nothing — the operator must
	// see 503 ("this node did not serve it, retry against the leader"), not a 500
	// that reads like a failed mutation of unknown outcome.
	s := newGCRunTestServer(t, true, true)

	for _, tc := range []struct {
		name    string
		handler gin.HandlerFunc
	}{
		{"requeue", s.handleGCFailedItemRequeue},
		{"delete", s.handleGCFailedItemDelete},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			r := gin.New()
			r.POST("/gc/failed", tc.handler)

			target := "/gc/failed?org_id=" + uuid.New().String() +
				"&failed_at=" + url.QueryEscape(time.Now().UTC().Format(time.RFC3339Nano)) +
				"&item_type=block&item_id=block-1"
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			req := httptest.NewRequest("POST", target, nil).WithContext(ctx)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, http.StatusServiceUnavailable, w.Body.String())
			}
		})
	}
}
