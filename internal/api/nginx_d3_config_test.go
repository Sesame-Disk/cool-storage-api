package api

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
)

func TestFrontendNginxDisablesGzipForDWriterRoutes(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	configPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "frontend", "nginx.conf")
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read %s: %v", configPath, err)
	}

	locations := map[string]string{
		"raw":        "location ~ ^/repo/[^/]+/raw/ {",
		"history":    "location ~ ^/repo/[^/]+/history/(download|view|raw)/?$ {",
		"share root": "location @share_link_backend {",
		"share path": "location /d/ {",
		"bootstrap":  "location ~ ^/api/v2\\.1/share-links/[^/]+/(?:files/)?bootstrap/?$ {",
		"seafhttp":   "location ^~ /seafhttp/ {",
	}
	for name, header := range locations {
		body, found := nginxLocationBody(string(config), header)
		if !found || !strings.Contains(body, "proxy_buffering         off;") || !strings.Contains(body, "gzip                    off;") {
			t.Errorf("%s D writer location does not disable both proxy buffering and gzip", name)
		}
	}
}

// TestSupportedNginxTimeoutsNeverPreemptDownloadAdmission is the operational
// half of criterion 10. D3/D9 claim the application-owned deadline is the
// authoritative one on the connection, and that claim is only true while the
// supported proxy's own timers are strictly longer than every deadline the
// application will accept.
//
// It asserts the relationship rather than a literal, because a literal is what
// froze the previous mismatch in place: nginx cut a stalled downstream at 120s
// while validateDownloadAdmissionConfig happily accepted idle_write_timeout up
// to 15m. Configurations passed validation and then died on nginx's clock, with
// the operator's tolerance silently doing nothing and the release recorded as a
// client disconnect rather than an idle-write timeout.
//
// The two directives cover phases that are silent in opposite directions:
// proxy_read_timeout bounds a backend that is producing nothing, which is what
// preparation looks like from nginx; send_timeout bounds nginx being unable to
// write downstream, which is what a stalled client looks like.
func TestSupportedNginxTimeoutsNeverPreemptDownloadAdmission(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	configs := []string{
		filepath.Join(root, "frontend", "nginx.conf"),
		filepath.Join(root, "mobile-frontend", "nginx.conf"),
		filepath.Join(root, "configs", "nginx-multiregion.conf"),
	}

	for _, configPath := range configs {
		t.Run(filepath.Base(configPath), func(t *testing.T) {
			raw, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatalf("read %s: %v", configPath, err)
			}

			for _, directive := range []struct {
				name  string
				floor time.Duration
				why   string
			}{
				{"send_timeout", config.MinNginxSendTimeout,
					"a stalled downstream client must be ended by the application's idle-write deadline"},
				{"proxy_read_timeout", config.MinNginxProxyReadTimeout,
					"a backend still in preparation produces nothing to read"},
			} {
				values := nginxDirectiveDurations(t, string(raw), directive.name)
				if len(values) == 0 {
					t.Fatalf("no %s directive found; the floor cannot be enforced by absence", directive.name)
				}
				for _, value := range values {
					if value <= directive.floor {
						t.Fatalf("%s = %s, must exceed %s: %s", directive.name, value, directive.floor, directive.why)
					}
				}
			}
		})
	}
}

// nginxDirectiveDurations returns every value of a directive, so a single
// permissive server-level setting cannot be undone by a stricter location.
func nginxDirectiveDurations(t *testing.T, config, directive string) []time.Duration {
	t.Helper()

	var out []time.Duration
	for _, line := range strings.Split(config, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 || fields[0] != directive {
			continue
		}
		value, err := time.ParseDuration(strings.TrimSuffix(fields[1], ";"))
		if err != nil {
			t.Fatalf("%s has unparseable value %q: %v", directive, fields[1], err)
		}
		out = append(out, value)
	}
	return out
}

func nginxLocationBody(config, header string) (string, bool) {
	start := strings.Index(config, header)
	if start < 0 {
		return "", false
	}
	body := config[start:]
	if end := strings.Index(body, "\n    location "); end >= 0 {
		body = body[:end]
	}
	return body, true
}
