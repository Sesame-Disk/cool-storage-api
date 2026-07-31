package api

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gingzip "github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/Sesame-Disk/sesamefs/internal/metrics"
)

// Subcontract B's admitted lifetime only works if it reaches the socket. These
// regressions cover the way it can silently stop doing so: a middleware that
// replaces c.Writer.
//
// http.NewResponseController walks the writer chain looking for SetReadDeadline,
// following Unwrap() when it does not find it. gin's concrete *responseWriter
// implements Unwrap, but the gin.ResponseWriter *interface* does not declare it,
// so any wrapper that embeds the interface — gin-contrib/gzip's gzipWriter is
// exactly that — exposes neither method and terminates the walk. The deadline
// then cannot be installed, and the body-close fallback cannot interrupt a
// parked read because net/http's body Read and Close share one mutex.
//
// The consequence is not cosmetic: an authenticated client that sends
// Accept-Encoding: gzip, announces a body, writes a few bytes and goes silent
// would hold its admission until the process restarts. Enough of them take the
// node's whole block-upload capacity.

// stalledBlockPUT opens a raw connection, announces a body, sends part of it and
// then goes silent — a stalled uploader, not a disconnected one — and returns
// whatever the server answers.
func stalledBlockPUT(t *testing.T, addr string, extraHeaders string) (*http.Response, error) {
	t.Helper()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if _, err := fmt.Fprintf(conn,
		"PUT /seafhttp/repo/repo/block/%s HTTP/1.1\r\nHost: sesamefs\r\n%sContent-Length: 4096\r\n\r\npartial",
		inflightTestBlockID, extraHeaders); err != nil {
		t.Fatalf("write request: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(6 * time.Second)); err != nil {
		t.Fatalf("set client read deadline: %v", err)
	}
	return http.ReadResponse(bufio.NewReader(conn), nil)
}

func startStalledBlockServer(t *testing.T, h *SyncHandler, middleware ...gin.HandlerFunc) *httptest.Server {
	t.Helper()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	for _, m := range middleware {
		r.Use(m)
	}
	r.Use(func(c *gin.Context) {
		c.Set("org_id", "00000000-0000-0000-0000-000000000001")
		c.Set("user_id", "user")
		c.Next()
	})
	r.PUT("/seafhttp/repo/:repo_id/block/:block_id", h.PutBlock)

	srv := httptest.NewUnstartedServer(r)
	// Mirror the shipped config: no whole-body read timeout, because large
	// uploads legitimately take minutes. That is what makes the admitted
	// lifetime the only bound on a stalled read.
	srv.Config.ReadTimeout = 0
	srv.Config.ReadHeaderTimeout = 10 * time.Second
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

// TestPutBlockAdmittedLifetimeSurvivesGzipNegotiation drives the real shipped
// middleware stack. Before the block route was excluded from gzip, this exact
// request — identical to the passing plain-router regression except for one
// request header — was never answered at all.
func TestPutBlockAdmittedLifetimeSurvivesGzipNegotiation(t *testing.T) {
	cfg := syncInflightConfig(1, 1, 5*time.Second)
	cfg.SeafHTTP.SyncBlockAdmittedLifetime = 300 * time.Millisecond
	h := newInflightTestHandler(t, cfg)

	srv := startStalledBlockServer(t, h, gingzip.Gzip(gingzip.DefaultCompression,
		gingzip.WithExcludedPathsRegexs(gzipExcludedPathsRegexs("/metrics")),
	))

	before := testutil.ToFloat64(metrics.SyncPutBlockReadDeadlineUnsupportedTotal)

	resp, err := stalledBlockPUT(t, srv.Listener.Addr().String(), "Accept-Encoding: gzip\r\n")
	if err != nil {
		t.Fatalf("stalled gzip-negotiating upload was never answered; the admitted lifetime did not reach the connection: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	if got := resp.Header.Get("Retry-After"); got == "" {
		t.Fatal("stalled-upload 503 has no Retry-After")
	}
	// The route must be excluded outright, not merely survive: a compressed
	// response here would mean the writer was wrapped and the deadline came from
	// somewhere else.
	if got := resp.Header.Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want the block route excluded from gzip", got)
	}
	// And it must be the deadline that ended it, not the fail-closed refusal.
	if after := testutil.ToFloat64(metrics.SyncPutBlockReadDeadlineUnsupportedTotal); after != before {
		t.Fatalf("read-deadline-unsupported counter moved from %v to %v; the deadline should install cleanly on this route", before, after)
	}
	requireEventuallyDrained(t, h.blockInflight)
}

// unwrappableWriter is the shape of the problem: it embeds the gin.ResponseWriter
// interface, so it inherits no Unwrap() and hides the connection, exactly like
// gin-contrib/gzip's writer.
type unwrappableWriter struct {
	gin.ResponseWriter
}

// TestPutBlockFailsClosedWhenReadDeadlineCannotBeInstalled pins the behaviour
// that keeps this class of defect from recurring silently. Excluding one route
// from one middleware fixes today's instance; the guard has to survive the next
// middleware somebody adds.
func TestPutBlockFailsClosedWhenReadDeadlineCannotBeInstalled(t *testing.T) {
	cfg := syncInflightConfig(1, 1, 5*time.Second)
	cfg.SeafHTTP.SyncBlockAdmittedLifetime = 300 * time.Millisecond
	h := newInflightTestHandler(t, cfg)

	srv := startStalledBlockServer(t, h, func(c *gin.Context) {
		c.Writer = unwrappableWriter{ResponseWriter: c.Writer}
		c.Next()
	})

	before := testutil.ToFloat64(metrics.SyncPutBlockReadDeadlineUnsupportedTotal)

	// The connection must be dropped, not answered. A 503 is undeliverable here:
	// net/http drains the body the peer never finished sending before the
	// response reaches the socket, and with no deadline installed that drain is
	// the unbounded wait being refused. The client therefore sees the connection
	// end, which its retry logic treats as a transient network error.
	_, err := stalledBlockPUT(t, srv.Listener.Addr().String(), "")
	if err == nil {
		t.Fatal("request behind an unwrappable writer got a response; it must be dropped, since a response cannot be delivered before the unread body is drained")
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		t.Fatalf("connection was left hanging instead of dropped: %v", err)
	}

	if after := testutil.ToFloat64(metrics.SyncPutBlockReadDeadlineUnsupportedTotal); after != before+1 {
		t.Fatalf("read-deadline-unsupported counter = %v, want %v; an unprotectable request must be observable", after, before+1)
	}
	// Dropped before the body was read, so no admission is held.
	requireEventuallyDrained(t, h.blockInflight)
}

// TestSyntheticWritersKeepTheBodyCloseFallback guards the other side: unit tests
// and non-net/http callers have no connection to deadline, and their bodies are
// ordinary readers whose Close does end them. Failing those closed would make
// the guard untestable without a socket.
func TestSyntheticWritersKeepTheBodyCloseFallback(t *testing.T) {
	cfg := syncInflightConfig(1, 1, 5*time.Second)
	cfg.SeafHTTP.SyncBlockAdmittedLifetime = 200 * time.Millisecond
	h := newInflightTestHandler(t, cfg)
	r := putBlockRouterFor(h, "00000000-0000-0000-0000-000000000001", "user")

	before := testutil.ToFloat64(metrics.SyncPutBlockReadDeadlineUnsupportedTotal)

	body := &lifetimeBlockingBody{started: make(chan struct{}), closed: make(chan struct{})}
	req := httptest.NewRequest(http.MethodPut, "/seafhttp/repo/repo/block/"+inflightTestBlockID, body)
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		r.ServeHTTP(w, req)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("synthetic stalled body was never released by the admitted lifetime")
	}

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	if after := testutil.ToFloat64(metrics.SyncPutBlockReadDeadlineUnsupportedTotal); after != before {
		t.Fatalf("synthetic writer counted as an unprotectable connection (%v -> %v); only server-handled requests should fail closed", before, after)
	}
}
