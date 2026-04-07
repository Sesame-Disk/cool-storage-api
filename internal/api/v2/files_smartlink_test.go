package v2

import (
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/config"
)

func TestBuildSmartLinkRedirectURL(t *testing.T) {
	previewExtensions := config.DefaultConfig().FileView.PreviewExtensions

	tests := []struct {
		name     string
		filePath string
		isDir    bool
		want     string
	}{
		{
			name:     "previewable file uses frontend preview shell",
			filePath: "/docs/readme.md",
			isDir:    false,
			want:     "http://localhost:3000/file-preview/?p=%2Fdocs%2Freadme.md&repo_id=repo-1",
		},
		{
			name:     "non previewable file uses legacy file route",
			filePath: "/office/report.docx",
			isDir:    false,
			want:     "http://localhost:3000/lib/repo-1/file/office/report.docx",
		},
		{
			name:     "directory uses library route",
			filePath: "/docs/guides/",
			isDir:    true,
			want:     "http://localhost:3000/library/repo-1/My%20Library/docs/guides",
		},
		{
			name:     "directory without trailing slash still uses library route",
			filePath: "/pepep",
			isDir:    true,
			want:     "http://localhost:3000/library/repo-1/My%20Library/pepep",
		},
		{
			name:     "root directory uses library route with trailing slash",
			filePath: "/",
			isDir:    true,
			want:     "http://localhost:3000/library/repo-1/My%20Library/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildSmartLinkRedirectURL("http://localhost:3000", "repo-1", "My Library", tt.filePath, previewExtensions, tt.isDir)
			if got != tt.want {
				t.Fatalf("buildSmartLinkRedirectURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizeSmartLinkPath(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		isDir bool
		want  string
	}{
		{name: "file keeps normalized path", path: "docs/readme.md", isDir: false, want: "/docs/readme.md"},
		{name: "dir gets trailing slash", path: "/pepep", isDir: true, want: "/pepep/"},
		{name: "root dir stays root", path: "/", isDir: true, want: "/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeSmartLinkPath(tt.path, tt.isDir)
			if got != tt.want {
				t.Fatalf("normalizeSmartLinkPath() = %q, want %q", got, tt.want)
			}
		})
	}
}
