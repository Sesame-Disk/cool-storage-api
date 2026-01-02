package v2

import (
	"testing"
)

// Test normalizePath function (additional cases)
func TestNormalizePath_Additional(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty path", "", "/"},
		{"root path", "/", "/"},
		{"simple path", "/foo", "/foo"},
		{"path without leading slash", "foo", "/foo"},
		{"path with trailing slash", "/foo/", "/foo"},
		{"nested path", "/foo/bar/baz", "/foo/bar/baz"},
		{"nested path without leading slash", "foo/bar/baz", "/foo/bar/baz"},
		{"double slashes cleaned", "/foo//bar", "/foo/bar"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizePath(tt.input)
			if result != tt.expected {
				t.Errorf("normalizePath(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// Test RemoveEntryFromList function
func TestRemoveEntryFromList(t *testing.T) {
	entries := []FSEntry{
		{Name: "file1.txt", ID: "id1", Mode: ModeFile, Size: 100},
		{Name: "file2.txt", ID: "id2", Mode: ModeFile, Size: 200},
		{Name: "dir1", ID: "id3", Mode: ModeDir},
	}

	tests := []struct {
		name       string
		entries    []FSEntry
		removeName string
		wantLen    int
	}{
		{"remove first", entries, "file1.txt", 2},
		{"remove middle", entries, "file2.txt", 2},
		{"remove last", entries, "dir1", 2},
		{"remove non-existent", entries, "notfound.txt", 3},
		{"remove from empty", []FSEntry{}, "file.txt", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RemoveEntryFromList(tt.entries, tt.removeName)
			if len(result) != tt.wantLen {
				t.Errorf("RemoveEntryFromList() len = %d, want %d", len(result), tt.wantLen)
			}
			// Verify the removed entry is not in result
			for _, entry := range result {
				if entry.Name == tt.removeName {
					t.Errorf("RemoveEntryFromList() still contains %q", tt.removeName)
				}
			}
		})
	}
}

// Test FindEntryInList function
func TestFindEntryInList(t *testing.T) {
	entries := []FSEntry{
		{Name: "file1.txt", ID: "id1", Mode: ModeFile, Size: 100},
		{Name: "file2.txt", ID: "id2", Mode: ModeFile, Size: 200},
		{Name: "dir1", ID: "id3", Mode: ModeDir},
	}

	tests := []struct {
		name     string
		entries  []FSEntry
		findName string
		wantNil  bool
		wantID   string
	}{
		{"find first", entries, "file1.txt", false, "id1"},
		{"find middle", entries, "file2.txt", false, "id2"},
		{"find last", entries, "dir1", false, "id3"},
		{"find non-existent", entries, "notfound.txt", true, ""},
		{"find in empty", []FSEntry{}, "file.txt", true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FindEntryInList(tt.entries, tt.findName)
			if tt.wantNil {
				if result != nil {
					t.Errorf("FindEntryInList() = %v, want nil", result)
				}
			} else {
				if result == nil {
					t.Error("FindEntryInList() = nil, want entry")
				} else if result.ID != tt.wantID {
					t.Errorf("FindEntryInList().ID = %q, want %q", result.ID, tt.wantID)
				}
			}
		})
	}
}

// Test UpdateEntryInList function
func TestUpdateEntryInList(t *testing.T) {
	entries := []FSEntry{
		{Name: "file1.txt", ID: "id1", Mode: ModeFile, Size: 100},
		{Name: "file2.txt", ID: "id2", Mode: ModeFile, Size: 200},
	}

	tests := []struct {
		name    string
		oldName string
		newName string
	}{
		{"rename first", "file1.txt", "renamed1.txt"},
		{"rename second", "file2.txt", "renamed2.txt"},
		{"rename non-existent", "notfound.txt", "new.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := UpdateEntryInList(entries, tt.oldName, tt.newName)
			if len(result) != len(entries) {
				t.Errorf("UpdateEntryInList() changed length from %d to %d", len(entries), len(result))
			}
			// Check if rename happened
			foundOld := false
			foundNew := false
			for _, entry := range result {
				if entry.Name == tt.oldName {
					foundOld = true
				}
				if entry.Name == tt.newName {
					foundNew = true
				}
			}
			// If old name existed, it should be renamed
			oldExists := false
			for _, entry := range entries {
				if entry.Name == tt.oldName {
					oldExists = true
					break
				}
			}
			if oldExists {
				if foundOld {
					t.Errorf("UpdateEntryInList() old name %q still exists", tt.oldName)
				}
				if !foundNew {
					t.Errorf("UpdateEntryInList() new name %q not found", tt.newName)
				}
			}
		})
	}
}

// Test AddEntryToList function
func TestAddEntryToList(t *testing.T) {
	entries := []FSEntry{
		{Name: "file1.txt", ID: "id1", Mode: ModeFile, Size: 100},
	}

	newEntry := FSEntry{Name: "file2.txt", ID: "id2", Mode: ModeFile, Size: 200}

	result := AddEntryToList(entries, newEntry)

	if len(result) != 2 {
		t.Errorf("AddEntryToList() len = %d, want 2", len(result))
	}

	found := false
	for _, entry := range result {
		if entry.Name == "file2.txt" {
			found = true
			if entry.ID != "id2" {
				t.Errorf("AddEntryToList() added entry ID = %q, want %q", entry.ID, "id2")
			}
		}
	}
	if !found {
		t.Error("AddEntryToList() did not add new entry")
	}
}

// Test AddEntryToList with empty list
func TestAddEntryToList_Empty(t *testing.T) {
	entries := []FSEntry{}
	newEntry := FSEntry{Name: "file1.txt", ID: "id1", Mode: ModeFile, Size: 100}

	result := AddEntryToList(entries, newEntry)

	if len(result) != 1 {
		t.Errorf("AddEntryToList() len = %d, want 1", len(result))
	}
	if result[0].Name != "file1.txt" {
		t.Errorf("AddEntryToList()[0].Name = %q, want %q", result[0].Name, "file1.txt")
	}
}

// Test FSEntry mode constants
func TestFSEntryModeConstants(t *testing.T) {
	// Verify ModeDir and ModeFile are distinct
	if ModeDir == ModeFile {
		t.Error("ModeDir and ModeFile should be different")
	}

	// Test mode detection
	dirEntry := FSEntry{Name: "dir", Mode: ModeDir}
	fileEntry := FSEntry{Name: "file", Mode: ModeFile}

	if dirEntry.Mode != ModeDir {
		t.Errorf("dirEntry.Mode = %d, want %d (ModeDir)", dirEntry.Mode, ModeDir)
	}
	if fileEntry.Mode != ModeFile {
		t.Errorf("fileEntry.Mode = %d, want %d (ModeFile)", fileEntry.Mode, ModeFile)
	}
}

// Test PathTraverseResult struct
func TestPathTraverseResult_Struct(t *testing.T) {
	result := &PathTraverseResult{
		TargetFSID: "target123",
		TargetEntry: &FSEntry{
			Name: "file.txt",
			ID:   "entry123",
			Mode: ModeFile,
			Size: 1024,
		},
		ParentFSID:   "parent123",
		ParentPath:   "/path/to",
		Ancestors:    []string{"root", "path", "to"},
		AncestorPath: []string{"/", "/path", "/path/to"},
		Entries: []FSEntry{
			{Name: "file.txt", ID: "entry123"},
			{Name: "other.txt", ID: "other123"},
		},
	}

	if result.TargetFSID != "target123" {
		t.Errorf("TargetFSID = %q, want %q", result.TargetFSID, "target123")
	}
	if result.TargetEntry == nil {
		t.Error("TargetEntry should not be nil")
	}
	if result.TargetEntry.Name != "file.txt" {
		t.Errorf("TargetEntry.Name = %q, want %q", result.TargetEntry.Name, "file.txt")
	}
	if result.ParentFSID != "parent123" {
		t.Errorf("ParentFSID = %q, want %q", result.ParentFSID, "parent123")
	}
	if result.ParentPath != "/path/to" {
		t.Errorf("ParentPath = %q, want %q", result.ParentPath, "/path/to")
	}
	if len(result.Ancestors) != 3 {
		t.Errorf("len(Ancestors) = %d, want 3", len(result.Ancestors))
	}
	if len(result.Entries) != 2 {
		t.Errorf("len(Entries) = %d, want 2", len(result.Entries))
	}
}
