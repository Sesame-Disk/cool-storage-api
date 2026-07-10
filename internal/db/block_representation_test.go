package db

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestIsCanonicalBlockRepresentationID(t *testing.T) {
	libID := uuid.New().String()
	upperLibID := strings.ToUpper(libID)

	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"plain", PlainBlockRepresentationID, true},
		{"encrypted library", EncryptedLibraryBlockRepresentationID(libID), true},
		{"encrypted library padded", "  library:" + libID + "  ", true},
		{"encrypted library missing hyphens", "library:" + strings.ReplaceAll(libID, "-", ""), false},
		{"encrypted library braced", "library:{" + libID + "}", false},
		{"encrypted library uppercase", "library:" + upperLibID, false},
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

func TestNewLibraryBlockRepresentationID(t *testing.T) {
	libraryID := uuid.NewString()
	if got := NewLibraryBlockRepresentationID(libraryID, false); got != PlainBlockRepresentationID {
		t.Fatalf("plaintext representation = %q, want %q", got, PlainBlockRepresentationID)
	}
	wantEncrypted := EncryptedLibraryBlockRepresentationID(libraryID)
	if got := NewLibraryBlockRepresentationID(libraryID, true); got != wantEncrypted {
		t.Fatalf("encrypted representation = %q, want %q", got, wantEncrypted)
	}
}

func TestIsCanonicalBlockRepresentationForLibrary(t *testing.T) {
	libID := uuid.New()
	otherLibID := uuid.New()

	cases := []struct {
		name             string
		representationID string
		libraryID        uuid.UUID
		want             bool
	}{
		{"plain matches any library", PlainBlockRepresentationID, libID, true},
		{"encrypted matching library", EncryptedLibraryBlockRepresentationID(libID.String()), libID, true},
		{"encrypted other library", EncryptedLibraryBlockRepresentationID(otherLibID.String()), libID, false},
		{"encrypted malformed uuid", "library:" + strings.ReplaceAll(libID.String(), "-", ""), libID, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsCanonicalBlockRepresentationForLibrary(tc.representationID, tc.libraryID); got != tc.want {
				t.Fatalf("IsCanonicalBlockRepresentationForLibrary(%q, %s) = %v, want %v", tc.representationID, tc.libraryID, got, tc.want)
			}
		})
	}
}

func TestDeleteBlockRepresentationFromState(t *testing.T) {
	libID := uuid.New()
	otherID := uuid.New()

	cases := []struct {
		name      string
		state     LibraryState
		want      string
		wantError bool
	}{
		{
			name:  "plaintext empty derives plain:v1",
			state: LibraryState{LibraryID: libID.String(), Encrypted: false, BlockRepresentationID: ""},
			want:  PlainBlockRepresentationID,
		},
		{
			name:  "encrypted empty derives library:<id>",
			state: LibraryState{LibraryID: libID.String(), Encrypted: true, BlockRepresentationID: ""},
			want:  EncryptedLibraryBlockRepresentationID(libID.String()),
		},
		{
			name:  "explicit plain:v1 preserved",
			state: LibraryState{LibraryID: libID.String(), BlockRepresentationID: PlainBlockRepresentationID},
			want:  PlainBlockRepresentationID,
		},
		{
			name:  "encrypted explicit library:<id> preserved",
			state: LibraryState{LibraryID: libID.String(), Encrypted: true, BlockRepresentationID: EncryptedLibraryBlockRepresentationID(libID.String())},
			want:  EncryptedLibraryBlockRepresentationID(libID.String()),
		},
		{
			name:      "garbage stored value rejected",
			state:     LibraryState{LibraryID: libID.String(), BlockRepresentationID: "garbage"},
			wantError: true,
		},
		{
			name:      "representation of a different library rejected",
			state:     LibraryState{LibraryID: libID.String(), BlockRepresentationID: EncryptedLibraryBlockRepresentationID(otherID.String())},
			wantError: true,
		},
		{
			name:      "encrypted library stamped plain:v1 rejected (domain cross)",
			state:     LibraryState{LibraryID: libID.String(), Encrypted: true, BlockRepresentationID: PlainBlockRepresentationID},
			wantError: true,
		},
		{
			name:      "plaintext library stamped library:<same-id> rejected (domain cross)",
			state:     LibraryState{LibraryID: libID.String(), Encrypted: false, BlockRepresentationID: EncryptedLibraryBlockRepresentationID(libID.String())},
			wantError: true,
		},
		{
			name:      "invalid library uuid rejected",
			state:     LibraryState{LibraryID: "not-a-uuid", BlockRepresentationID: PlainBlockRepresentationID},
			wantError: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := deleteBlockRepresentationFromState(tc.state)
			if tc.wantError {
				if err == nil {
					t.Fatalf("deleteBlockRepresentationFromState(%+v) = %q, want error", tc.state, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("deleteBlockRepresentationFromState(%+v) unexpected error: %v", tc.state, err)
			}
			if got != tc.want {
				t.Fatalf("deleteBlockRepresentationFromState(%+v) = %q, want %q", tc.state, got, tc.want)
			}
		})
	}
}
