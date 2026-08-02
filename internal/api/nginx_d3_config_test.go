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
