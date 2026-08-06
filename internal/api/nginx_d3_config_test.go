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
// half of criterion 10. D3 and §9 claim the application-owned deadline is the
// authoritative one on the connection, and that is only true while the supported
// proxy's own timers are strictly longer than every deadline validation accepts.
//
// It checks the *effective* value for each protected location — its own
// directive, or what it inherits — rather than scanning the file for any
// occurrence. A flat scan gives both false results: a long timeout on an
// unrelated location would satisfy it while a D route still inherited a short
// default, and it would reject the short default that non-D routes should keep.
// Those defaults matter here: this PR closes an abuse finding, so raising every
// route's connection-retention window to 45 minutes would trade one resource
// vector for another.
func TestSupportedNginxTimeoutsNeverPreemptDownloadAdmission(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")

	for _, tc := range []struct {
		path      string
		locations []string
	}{
		{
			filepath.Join(root, "frontend", "nginx.conf"),
			[]string{
				"location ~ ^/repo/[^/]+/raw/ {",
				"location ~ ^/repo/[^/]+/history/(download|view|raw)/?$ {",
				"location @share_link_backend {",
				"location /d/ {",
				"location ^~ /seafhttp/ {",
			},
		},
		{
			filepath.Join(root, "mobile-frontend", "nginx.conf"),
			[]string{"location /seafhttp/ {"},
		},
		{
			filepath.Join(root, "configs", "nginx-multiregion.conf"),
			[]string{"location ~ ^/(seafhttp/|d/|repo/[^/]+/(raw|history)/|api/v2\\.1/share-links/[^/]+/(files/)?bootstrap) {"},
		},
	} {
		t.Run(filepath.Base(tc.path), func(t *testing.T) {
			raw, err := os.ReadFile(tc.path)
			if err != nil {
				t.Fatalf("read %s: %v", tc.path, err)
			}
			contents := string(raw)

			for _, header := range tc.locations {
				body, ok := nginxLocationBody(contents, header)
				if !ok {
					t.Fatalf("protected location %q not found; the guard cannot be enforced by absence", header)
				}
				for _, directive := range []struct {
					name  string
					floor time.Duration
					why   string
				}{
					{"send_timeout", config.MinNginxSendTimeout,
						"a stalled downstream client must be ended by the application's idle-write deadline"},
					{"proxy_read_timeout", config.MinNginxProxyReadTimeout,
						"preparation and the first storage read are both silent upstream"},
				} {
					value, found := nginxDirectiveDuration(t, body, directive.name)
					if !found {
						t.Fatalf("%s: no %s inside the location, so it inherits the short default", header, directive.name)
					}
					if value <= directive.floor {
						t.Fatalf("%s: %s = %s, must exceed %s: %s", header, directive.name, value, directive.floor, directive.why)
					}
				}
			}
		})
	}
}

// nginxDirectiveDuration reads a directive from one location body. Scoping the
// read to the body is the point: an inherited or unrelated value must not count.
func nginxDirectiveDuration(t *testing.T, body, directive string) (time.Duration, bool) {
	t.Helper()

	// nginx applies the last directive in a block, so the helper must too;
	// reading the first would let a superseded short value fail a correct file.
	var last time.Duration
	var found bool

	for _, line := range strings.Split(body, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 || fields[0] != directive {
			continue
		}
		value, err := time.ParseDuration(strings.TrimSuffix(fields[1], ";"))
		if err != nil {
			t.Fatalf("%s has unparseable value %q: %v", directive, fields[1], err)
		}
		last, found = value, true
	}
	return last, found
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
