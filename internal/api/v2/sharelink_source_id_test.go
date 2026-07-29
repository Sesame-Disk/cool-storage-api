package v2

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestPublicLinkSourceID(t *testing.T) {
	token := "public-bearer-token"
	got := publicLinkSourceID("upload-link", token)

	if got == token {
		t.Fatal("source ID must not contain the public bearer token")
	}
	if len(got) != 64 {
		t.Fatalf("source ID length = %d, want 64 hex characters", len(got))
	}
	if got != publicLinkSourceID("upload-link", token) {
		t.Fatal("source ID must be stable for one public link")
	}
	if got == publicLinkSourceID("upload-link", "different-public-link") {
		t.Fatal("distinct public links must have distinct source IDs")
	}
	if got == publicLinkSourceID("share-link", token) {
		t.Fatal("upload-link and share-link identities must be domain-separated")
	}
}

type sourceIDTokenCreator struct {
	sourceIDs []string
}

func (m *sourceIDTokenCreator) CreateUploadToken(string, string, string, string) (string, error) {
	return "upload-token", nil
}
func (m *sourceIDTokenCreator) CreateUpdateToken(string, string, string, string) (string, error) {
	return "update-token", nil
}
func (m *sourceIDTokenCreator) CreateDownloadToken(string, string, string, string) (string, error) {
	return "download-token", nil
}
func (m *sourceIDTokenCreator) CreateLinkUploadToken(_, _, _, _, sourceID string) (string, error) {
	m.sourceIDs = append(m.sourceIDs, sourceID)
	return "minted-upload-token", nil
}
func (m *sourceIDTokenCreator) CreateLinkDownloadToken(string, string, string, string) (string, error) {
	return "link-download-token", nil
}

func invokeSourceIDHandler(t *testing.T, route, bearer string, handler gin.HandlerFunc) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "token", Value: bearer}}
	c.Request = httptest.NewRequest(http.MethodGet, route, nil)
	handler(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
}

func assertStableOpaqueSourceIDs(t *testing.T, bearer string, sourceIDs []string) {
	t.Helper()
	if len(sourceIDs) != 2 {
		t.Fatalf("mint calls = %d, want 2", len(sourceIDs))
	}
	if sourceIDs[0] != sourceIDs[1] {
		t.Fatalf("remint source IDs differ: %q != %q", sourceIDs[0], sourceIDs[1])
	}
	if len(sourceIDs[0]) != 64 || strings.Contains(sourceIDs[0], bearer) {
		t.Fatalf("source ID %q must be a full opaque SHA-256 digest", sourceIDs[0])
	}
}

func TestGetUploadLinkUploadURLRemintsWithStableOpaqueSourceID(t *testing.T) {
	const bearer = "raw-upload-link-bearer"
	tokens := &sourceIDTokenCreator{}
	h := &ShareLinkViewHandler{
		tokenCreator: tokens,
		serverURL:    "https://files.example",
		uploadLinkResolver: func(got string) (*uploadLinkData, error) {
			if got != bearer {
				t.Fatalf("resolver token = %q, want %q", got, bearer)
			}
			return &uploadLinkData{orgID: "org", libraryID: "repo", filePath: "/drop", createdBy: "user", active: true}, nil
		},
	}
	for range 2 {
		invokeSourceIDHandler(t, "/api/v2.1/upload-links/"+bearer+"/upload/", bearer, h.GetUploadLinkUploadURL)
	}
	assertStableOpaqueSourceIDs(t, bearer, tokens.sourceIDs)
}

func TestGetShareLinkUploadURLRemintsWithStableOpaqueSourceID(t *testing.T) {
	const bearer = "raw-share-link-bearer"
	tokens := &sourceIDTokenCreator{}
	h := &ShareLinkViewHandler{
		tokenCreator: tokens,
		serverURL:    "https://files.example",
		shareLinkResolver: func(got string) (*shareLinkData, error) {
			if got != bearer {
				t.Fatalf("resolver token = %q, want %q", got, bearer)
			}
			return &shareLinkData{orgID: "org", libraryID: "repo", filePath: "/shared", createdBy: "user", canUpload: true}, nil
		},
	}
	for range 2 {
		invokeSourceIDHandler(t, "/api/v2.1/share-links/"+bearer+"/upload/", bearer, h.GetShareLinkUploadURL)
	}
	assertStableOpaqueSourceIDs(t, bearer, tokens.sourceIDs)
	if tokens.sourceIDs[0] == publicLinkSourceID("upload-link", bearer) {
		t.Fatal("share-link endpoint used the upload-link identity domain")
	}
}
