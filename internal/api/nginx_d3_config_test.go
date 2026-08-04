package api

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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

func TestSupportedNginxConfigsBoundDownstreamSendTimeout(t *testing.T) {
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
			config, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatalf("read %s: %v", configPath, err)
			}
			found := false
			for _, line := range strings.Split(string(config), "\n") {
				line = strings.TrimSpace(line)
				if !strings.HasPrefix(line, "send_timeout ") {
					continue
				}
				found = true
				if normalized := strings.Join(strings.Fields(line), " "); normalized != "send_timeout 120s;" {
					t.Fatalf("downstream send timeout = %q, want send_timeout 120s", line)
				}
			}
			if !found {
				t.Fatal("no direct send_timeout directive found")
			}
		})
	}
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
