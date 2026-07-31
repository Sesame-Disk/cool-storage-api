package api

import (
	"bufio"
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

// Subcontract C inherits subcontract B's socket problem, and this is the
// regression that would have caught it before it shipped rather than after.
//
// The check-blocks admitted lifetime only bounds anything if its deadline
// reaches the connection, and http.NewResponseController can only reach the
// connection while nothing in the writer chain hides Unwrap(). gin-contrib/gzip
// hides it. B excluded the block route and left check-blocks inside gzip, which
// was harmless while check-blocks had no lifetime — and became a total outage
// the moment it got one: every request on the real stack was answered by
// dropping the connection.
//
// A plain-router unit test cannot see this. The only difference between the two
// cases is one request header, and the whole failure lives in the middleware
// stack the shipped server actually builds.

// stalledCheckBlocks opens a raw connection, announces a body, sends part of it
// and goes silent — a stalled client, not a disconnected one.
func stalledCheckBlocks(t *testing.T, addr string, extraHeaders string) (*http.Response, error) {
	t.Helper()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if _, err := fmt.Fprintf(conn,
		"POST /seafhttp/repo/repo/check-blocks HTTP/1.1\r\nHost: sesamefs\r\n%sContent-Length: 4096\r\n\r\n[\"partial",
		extraHeaders); err != nil {
		t.Fatalf("write request: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(6 * time.Second)); err != nil {
		t.Fatalf("set client read deadline: %v", err)
	}
	return http.ReadResponse(bufio.NewReader(conn), nil)
}

func startStalledCheckBlocksServer(t *testing.T, h *SyncHandler, middleware ...gin.HandlerFunc) *httptest.Server {
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
	r.POST("/seafhttp/repo/:repo_id/check-blocks", h.CheckBlocks)

	srv := httptest.NewUnstartedServer(r)
	// Mirror the shipped config: no whole-body read timeout, which is what makes
	// the admitted lifetime the only bound on a stalled read.
	srv.Config.ReadTimeout = 0
	srv.Config.ReadHeaderTimeout = 10 * time.Second
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

// TestCheckBlocksAdmittedLifetimeSurvivesGzipNegotiation drives the real shipped
// middleware stack: the request must be answered 503 by its own deadline, not
// dropped by the fail-closed path.
func TestCheckBlocksAdmittedLifetimeSurvivesGzipNegotiation(t *testing.T) {
	cfg := checkBlocksTestConfig(1, 1, 2*time.Second)
	cfg.SeafHTTP.CheckBlocksAdmittedLifetime = 300 * time.Millisecond
	h := newCheckBlocksTestHandler(t, cfg)

	srv := startStalledCheckBlocksServer(t, h, gingzip.Gzip(gingzip.DefaultCompression,
		gingzip.WithExcludedPathsRegexs(gzipExcludedPathsRegexs("/metrics")),
	))

	before := testutil.ToFloat64(metrics.SyncCheckBlocksReadDeadlineUnsupportedTotal)

	resp, err := stalledCheckBlocks(t, srv.Listener.Addr().String(), "Accept-Encoding: gzip\r\n")
	if err != nil {
		t.Fatalf("stalled gzip-negotiating check-blocks was never answered; the admitted lifetime did not reach the connection: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	// The route must be excluded outright, not merely survive: a compressed
	// response would mean the writer was wrapped and the deadline came from
	// somewhere else.
	if got := resp.Header.Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want the check-blocks route excluded from gzip", got)
	}
	// And it must be the deadline that ended it, not the fail-closed refusal —
	// which is exactly the difference between a working guard and a route that
	// drops every request.
	if after := testutil.ToFloat64(metrics.SyncCheckBlocksReadDeadlineUnsupportedTotal); after != before {
		t.Fatalf("read-deadline-unsupported counter moved from %v to %v; the deadline should install cleanly on this route", before, after)
	}
	requireEventuallyDrained(t, h.checkBlocksInflight)
}

// TestCheckBlocksFailsClosedWhenReadDeadlineCannotBeInstalled keeps the class of
// defect from recurring silently: excluding one route from one middleware fixes
// today's instance, and the guard has to survive the next middleware somebody
// adds.
func TestCheckBlocksFailsClosedWhenReadDeadlineCannotBeInstalled(t *testing.T) {
	cfg := checkBlocksTestConfig(1, 1, 2*time.Second)
	cfg.SeafHTTP.CheckBlocksAdmittedLifetime = 300 * time.Millisecond
	h := newCheckBlocksTestHandler(t, cfg)

	srv := startStalledCheckBlocksServer(t, h, func(c *gin.Context) {
		c.Writer = unwrappableWriter{ResponseWriter: c.Writer}
		c.Next()
	})

	before := testutil.ToFloat64(metrics.SyncCheckBlocksReadDeadlineUnsupportedTotal)

	if _, err := stalledCheckBlocks(t, srv.Listener.Addr().String(), ""); err == nil {
		t.Fatal("request behind an unwrappable writer got a response; it must be dropped, since a response cannot be delivered before the unread body is drained")
	}

	if after := testutil.ToFloat64(metrics.SyncCheckBlocksReadDeadlineUnsupportedTotal); after != before+1 {
		t.Fatalf("read-deadline-unsupported counter = %v, want %v; an unprotectable request must be observable", after, before+1)
	}
	requireEventuallyDrained(t, h.checkBlocksInflight)
}
