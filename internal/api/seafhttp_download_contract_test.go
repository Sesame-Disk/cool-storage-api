package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http/httptest"
	"strings"
	"testing"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
)

const (
	dirEntryIDA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	dirEntryIDB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// A validated listing that does not name the entry is the ONLY thing that may
// report absence. Every corrupt shape below must fail closed instead, because
// reporting absence from an unvalidated listing is what makes a sync client
// delete a file that is still there.
func TestFindDirEntryIDAbsenceRequiresAValidatedListing(t *testing.T) {
	tests := []struct {
		name       string
		rawEntries string
		lookFor    string
		wantAbsent bool
		wantID     string
	}{
		{
			name:       "well-formed listing without the entry is the only absence",
			rawEntries: fmt.Sprintf(`[{"name":"other.txt","id":"%s"}]`, dirEntryIDA),
			lookFor:    "wanted.txt",
			wantAbsent: true,
		},
		{
			name:       "empty listing is absence",
			rawEntries: `[]`,
			lookFor:    "wanted.txt",
			wantAbsent: true,
		},
		{
			name:       "match resolves",
			rawEntries: fmt.Sprintf(`[{"name":"wanted.txt","id":"%s"}]`, dirEntryIDA),
			lookFor:    "wanted.txt",
			wantID:     dirEntryIDA,
		},
		{
			name:       "blank payload is not absence",
			rawEntries: "",
			lookFor:    "wanted.txt",
		},
		{
			name:       "whitespace-only payload is not absence",
			rawEntries: "   \n\t ",
			lookFor:    "wanted.txt",
		},
		{
			name:       "JSON null payload is not absence",
			rawEntries: "null",
			lookFor:    "wanted.txt",
		},
		{
			name:       "unparseable payload is not absence",
			rawEntries: `[{"name":`,
			lookFor:    "wanted.txt",
		},
		{
			name:       "null entry is not absence",
			rawEntries: `[null]`,
			lookFor:    "wanted.txt",
		},
		{
			name:       "non-object entry is not absence",
			rawEntries: `["wanted.txt"]`,
			lookFor:    "wanted.txt",
		},
		{
			name:       "non-string name is not absence",
			rawEntries: fmt.Sprintf(`[{"name":42,"id":"%s"}]`, dirEntryIDA),
			lookFor:    "wanted.txt",
		},
		{
			name:       "empty name is not absence",
			rawEntries: fmt.Sprintf(`[{"name":"","id":"%s"}]`, dirEntryIDA),
			lookFor:    "wanted.txt",
		},
		{
			name:       "missing id is not absence",
			rawEntries: `[{"name":"other.txt"}]`,
			lookFor:    "wanted.txt",
		},
		{
			name:       "non-40-hex id is not absence",
			rawEntries: `[{"name":"other.txt","id":"nothex"}]`,
			lookFor:    "wanted.txt",
		},
		{
			name:       "non-string id is not absence",
			rawEntries: `[{"name":"other.txt","id":null}]`,
			lookFor:    "wanted.txt",
		},
		{
			// encoding/json silently keeps the LAST value, which would serve B.
			name:       "duplicate id key on the requested entry fails closed",
			rawEntries: fmt.Sprintf(`[{"name":"wanted.txt","id":"%s","id":"%s"}]`, dirEntryIDA, dirEntryIDB),
			lookFor:    "wanted.txt",
		},
		{
			// encoding/json would hide "wanted.txt" entirely and report absence.
			name:       "duplicate name key hiding the requested entry fails closed",
			rawEntries: fmt.Sprintf(`[{"name":"wanted.txt","name":"other.txt","id":"%s"}]`, dirEntryIDA),
			lookFor:    "wanted.txt",
		},
		{
			name:       "duplicate names for the requested entry fail closed",
			rawEntries: fmt.Sprintf(`[{"name":"wanted.txt","id":"%s"},{"name":"wanted.txt","id":"%s"}]`, dirEntryIDA, dirEntryIDB),
			lookFor:    "wanted.txt",
		},
		{
			name:       "duplicate names elsewhere block an absence claim",
			rawEntries: fmt.Sprintf(`[{"name":"dup","id":"%s"},{"name":"dup","id":"%s"}]`, dirEntryIDA, dirEntryIDB),
			lookFor:    "wanted.txt",
		},
		{
			name:       "a corrupt sibling blocks an absence claim",
			rawEntries: fmt.Sprintf(`[{"name":"other.txt","id":"%s"},{"name":"bad.txt","id":"short"}]`, dirEntryIDA),
			lookFor:    "wanted.txt",
		},
		{
			// One bad sibling must not make a healthy file unreadable.
			name:       "a corrupt sibling does not hide a valid match",
			rawEntries: fmt.Sprintf(`[{"name":"bad.txt","id":"short"},{"name":"wanted.txt","id":"%s"}]`, dirEntryIDA),
			lookFor:    "wanted.txt",
			wantID:     dirEntryIDA,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := findDirEntryID(tt.rawEntries, tt.lookFor)
			switch {
			case tt.wantID != "":
				if err != nil || id != tt.wantID {
					t.Fatalf("findDirEntryID() = (%q, %v), want (%q, nil)", id, err, tt.wantID)
				}
			case tt.wantAbsent:
				if !errors.Is(err, errDirEntryAbsent) {
					t.Fatalf("findDirEntryID() error = %v, want errDirEntryAbsent", err)
				}
			default:
				if err == nil {
					t.Fatalf("findDirEntryID() = (%q, nil), want a non-absence error", id)
				}
				if errors.Is(err, errDirEntryAbsent) {
					t.Fatalf("findDirEntryID() reported absence from an unvalidated listing: %v", err)
				}
			}
		})
	}
}

// Property: absence is never reported unless the listing is well formed AND
// contains no matching name. Three separate audit rounds each found a corrupt
// shape hand-written cases had missed, so the shapes here are generated.
func TestFindDirEntryIDNeverInventsAbsence(t *testing.T) {
	rng := rand.New(rand.NewSource(20260722))
	ids := []string{dirEntryIDA, dirEntryIDB, "short", "", "NOTHEXNOTHEXNOTHEXNOTHEXNOTHEXNOTHEXNOTH"}
	names := []string{"wanted.txt", "other.txt", "", "dup"}
	const target = "wanted.txt"

	for i := 0; i < 3000; i++ {
		entryCount := rng.Intn(4)
		rawParts := make([]string, 0, entryCount)
		for j := 0; j < entryCount; j++ {
			switch rng.Intn(6) {
			case 0:
				rawParts = append(rawParts, "null")
			case 1:
				rawParts = append(rawParts, `"scalar"`)
			case 2: // duplicate name key
				rawParts = append(rawParts, fmt.Sprintf(`{"name":%q,"name":%q,"id":%q}`,
					names[rng.Intn(len(names))], names[rng.Intn(len(names))], ids[rng.Intn(len(ids))]))
			case 3: // duplicate id key
				rawParts = append(rawParts, fmt.Sprintf(`{"name":%q,"id":%q,"id":%q}`,
					names[rng.Intn(len(names))], ids[rng.Intn(len(ids))], ids[rng.Intn(len(ids))]))
			case 4: // missing id
				rawParts = append(rawParts, fmt.Sprintf(`{"name":%q}`, names[rng.Intn(len(names))]))
			default:
				rawParts = append(rawParts, fmt.Sprintf(`{"name":%q,"id":%q}`,
					names[rng.Intn(len(names))], ids[rng.Intn(len(ids))]))
			}
		}
		raw := "[" + strings.Join(rawParts, ",") + "]"

		id, err := findDirEntryID(raw, target)
		if !errors.Is(err, errDirEntryAbsent) {
			if err == nil && id == "" {
				t.Fatalf("findDirEntryID(%s) returned an empty id with no error", raw)
			}
			continue
		}

		// Absence was claimed — independently prove the listing really is
		// well formed and really has no matching entry.
		var entries []json.RawMessage
		if jsonErr := json.Unmarshal([]byte(raw), &entries); jsonErr != nil {
			t.Fatalf("absence claimed for unparseable listing %s", raw)
		}
		for _, entry := range entries {
			name, entryID, entryErr := dirEntryNameAndID(entry)
			if entryErr != nil {
				t.Fatalf("absence claimed despite invalid entry %s in %s: %v", entry, raw, entryErr)
			}
			if name == target {
				t.Fatalf("absence claimed for listing %s that names %q (id %s)", raw, target, entryID)
			}
		}
	}
}

// A referenced row that is simply missing is dangling metadata, not proof the
// path is gone. It must never become a 404.
func TestRespondSeafHTTPDownloadErrorMapsOnlyProvenAbsenceTo404(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "proven absence", err: fmt.Errorf("%w: wanted.txt", errDirEntryAbsent), wantStatus: 404},
		{name: "wrapped proven absence", err: fmt.Errorf("file not found: %s: %w", "wanted.txt", fmt.Errorf("%w: wanted.txt", errDirEntryAbsent)), wantStatus: 404},
		{name: "bare gocql not found on a referenced row", err: fmt.Errorf("commit not found: %w", gocql.ErrNotFound), wantStatus: 503},
		{name: "corrupt listing", err: errors.New("directory abc: malformed directory listing"), wantStatus: 503},
		{name: "cassandra timeout", err: errors.New("gocql: no response received from cassandra within timeout period"), wantStatus: 503},
		{name: "block store unavailable", err: errors.New("block store not available"), wantStatus: 503},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("GET", "/seafhttp/files/tok/f.txt", nil)

			respondSeafHTTPDownloadError(c, "repo-1", "/f.txt", tt.err)

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body=%s)", w.Code, tt.wantStatus, w.Body.String())
			}
			if tt.wantStatus == 503 && w.Header().Get("Retry-After") == "" {
				t.Fatal("503 must carry Retry-After so the client knows to retry")
			}
		})
	}
}

// Once streaming has committed headers there is no status left to write; the
// handler must not try to emit one on top of a partial body.
func TestRespondSeafHTTPDownloadErrorDoesNotRewriteACommittedResponse(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/seafhttp/files/tok/f.txt", nil)
	c.Status(200)
	if _, err := c.Writer.Write([]byte("partial")); err != nil {
		t.Fatalf("seed write failed: %v", err)
	}

	respondSeafHTTPDownloadError(c, "repo-1", "/f.txt", fmt.Errorf("%w: f.txt", errDirEntryAbsent))

	if w.Code != 200 || w.Body.String() != "partial" {
		t.Fatalf("committed response was rewritten: status=%d body=%q", w.Code, w.Body.String())
	}
}
