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

// protectedNginxLocation is one location that must carry the long transfer
// timeouts, and how many copies of it the file is expected to contain. The count
// is not cosmetic: the multiregion router repeats the same block once per region
// server, and a guard that checked a single copy would let the other two regress
// while staying green.
type protectedNginxLocation struct {
	header string
	copies int
}

// TestSupportedNginxTimeoutsNeverPreemptDownloadAdmission is the operational
// half of criterion 10. D3 and §9 claim the application-owned deadline is the
// authoritative one on the connection, and that is only true while the supported
// proxy's own timers are strictly longer than every deadline validation accepts.
//
// Every protected location must carry an *explicit* override: inheriting the
// server default is a failure, because that default is deliberately short. The
// check is per location rather than a file scan for that reason, and a scan
// would be wrong in both directions: a long timeout on an unrelated location
// would satisfy it while a D route still inherited the short default.
//
// "Protected location" is not the same as "download route". /seafhttp/ is a
// whole prefix, so its sync siblings — block PUT, check-blocks, the upload
// routes — inherit these timers as well; several are the same URI under a
// different method and cannot be separated by location at all. Each is bounded
// by its applicable application-level admission and deadline controls, so the
// proxy is not what was holding them. What this test asserts is that no *listed*
// location falls back to the short default; it does not assert that every other
// route in the file still has it.
func TestSupportedNginxTimeoutsNeverPreemptDownloadAdmission(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")

	for _, tc := range []struct {
		path      string
		locations []protectedNginxLocation
	}{
		{
			filepath.Join(root, "frontend", "nginx.conf"),
			[]protectedNginxLocation{
				{"location ~ ^/repo/[^/]+/raw/ {", 1},
				{"location ~ ^/repo/[^/]+/history/(download|view|raw)/?$ {", 1},
				{"location @share_link_backend {", 1},
				{"location /d/ {", 1},
				{"location ~ ^/api/v2\\.1/share-links/[^/]+/(?:files/)?bootstrap/?$ {", 1},
				{"location ^~ /seafhttp/ {", 1},
			},
		},
		{
			filepath.Join(root, "mobile-frontend", "nginx.conf"),
			[]protectedNginxLocation{
				{"location /seafhttp/ {", 1},
				{"location ~ ^/(d/|repo/[^/]+/(raw|history)/|api/v2\\.1/share-links/[^/]+/(files/)?bootstrap) {", 1},
			},
		},
		{
			filepath.Join(root, "configs", "nginx-multiregion.conf"),
			[]protectedNginxLocation{
				{"location ~ ^/(seafhttp/|d/|repo/[^/]+/(raw|history)/|api/v2\\.1/share-links/[^/]+/(files/)?bootstrap) {", 3},
			},
		},
	} {
		t.Run(filepath.Base(filepath.Dir(tc.path)), func(t *testing.T) {
			raw, err := os.ReadFile(tc.path)
			if err != nil {
				t.Fatalf("read %s: %v", tc.path, err)
			}
			contents := string(raw)

			for _, location := range tc.locations {
				bodies := nginxLocationBodies(contents, location.header)
				if len(bodies) != location.copies {
					t.Fatalf("found %d copies of protected location %q, want %d; the guard cannot be enforced by absence",
						len(bodies), location.header, location.copies)
				}
				for i, body := range bodies {
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
							t.Fatalf("%s (copy %d): no %s inside the location, so it inherits the short default",
								location.header, i+1, directive.name)
						}
						if value <= directive.floor {
							t.Fatalf("%s (copy %d): %s = %s, must exceed %s: %s",
								location.header, i+1, directive.name, value, directive.floor, directive.why)
						}
					}
				}
			}
		})
	}
}

// TestNginxLocationBodiesScopeToTheirOwnBlock pins the helper the guard above
// rests on, using the shape that defeated its predecessor: repeated identical
// locations, indented past four spaces, where only the last copy is correct.
// Reading to end of file made that fixture pass while two routers were
// unprotected, so a regression here is invisible in the config tests themselves.
func TestNginxLocationBodiesScopeToTheirOwnBlock(t *testing.T) {
	const header = "location ~ ^/protected/ {"
	fixture := `http {
    server {
        location ~ ^/protected/ {
            send_timeout 60s;
        }
        location /other/ {
            send_timeout 2700s;
        }
    }
    server {
        location ~ ^/protected/ {
            send_timeout 2700s;
        }
    }
}`

	bodies := nginxLocationBodies(fixture, header)
	if len(bodies) != 2 {
		t.Fatalf("found %d bodies, want one per copy", len(bodies))
	}

	first, found := nginxDirectiveDuration(t, bodies[0], "send_timeout")
	if !found || first != 60*time.Second {
		t.Fatalf("first copy resolved to (%s, %v); it must read its own block, not a later one", first, found)
	}
	second, found := nginxDirectiveDuration(t, bodies[1], "send_timeout")
	if !found || second != 2700*time.Second {
		t.Fatalf("second copy resolved to (%s, %v)", second, found)
	}

	// A location that carries no directive must report absence rather than
	// borrowing one from whatever follows it in the file.
	if _, found := nginxDirectiveDuration(t, mustLocationBody(t, fixture, "location /other/ {"), "proxy_read_timeout"); found {
		t.Fatal("absent directive reported as present; inheritance is not what this guard measures")
	}
}

func mustLocationBody(t *testing.T, config, header string) string {
	t.Helper()
	body, ok := nginxLocationBody(config, header)
	if !ok {
		t.Fatalf("fixture location %q not found", header)
	}
	return body
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

// nginxLocationBodies returns the body of every location with this exact header,
// each closed by counting braces the way nginx does.
//
// The previous version ended a body at the next four-space-indented `location`,
// which held only for `frontend/nginx.conf`. Mobile indents two and multiregion
// eight, so there was no terminator to find and a single "body" ran to end of
// file: the multiregion check read the last directive in the file — the
// aggregate router's — while reporting on the USA one, and deleting the USA or
// EU override left the guard green. Braces are what nginx itself uses, so no
// config is a special case, and none of the three has a brace inside a string.
func nginxLocationBodies(config, header string) []string {
	var bodies []string
	for searched := 0; searched < len(config); {
		idx := strings.Index(config[searched:], header)
		if idx < 0 {
			return bodies
		}
		start := searched + idx
		// Every header this test uses ends with the block's own opening brace.
		end := nginxBlockEnd(config, start+len(header)-1)
		bodies = append(bodies, config[start:end])
		searched = end
	}
	return bodies
}

// nginxBlockEnd returns the index just past the `}` closing the block whose `{`
// is at open. An unbalanced file yields the remainder rather than a silent skip,
// so a malformed config fails the assertions instead of passing vacuously.
func nginxBlockEnd(config string, open int) int {
	depth := 0
	for i := open; i < len(config); i++ {
		switch config[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return len(config)
}

func nginxLocationBody(config, header string) (string, bool) {
	bodies := nginxLocationBodies(config, header)
	if len(bodies) == 0 {
		return "", false
	}
	return bodies[0], true
}
