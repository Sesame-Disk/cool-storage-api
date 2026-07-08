package db

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestIsCanonicalBlockRepresentationID(t *testing.T) {
	libID := uuid.New().String()

	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"plain", PlainBlockRepresentationID, true},
		{"encrypted library", EncryptedLibraryBlockRepresentationID(libID), true},
		{"encrypted library padded", "  library:" + libID + "  ", true},
		{"empty", "", false},
		{"garbage", "foo", false},
		{"library prefix without uuid", "library:", false},
		{"library prefix bad uuid", "library:not-a-uuid", false},
		{"wrong plain version", "plain:v2", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsCanonicalBlockRepresentationID(tc.input); got != tc.want {
				t.Fatalf("IsCanonicalBlockRepresentationID(%q) = %v, want %v", strings.TrimSpace(tc.input), got, tc.want)
			}
		})
	}
}
