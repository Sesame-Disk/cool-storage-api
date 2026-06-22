package v2

import "testing"

// composeLastModifierIdentity is the identity-selection logic behind file/detail's
// last_modifier_* fields. The original bug was attributing the file to whoever made the
// request; these tests pin that it never does, that an unresolved modifier stays empty
// rather than guessed, and that a known user id resolves to the real account.
func TestComposeLastModifierIdentity(t *testing.T) {
	// knownUsers stands in for the users table. Only these ids resolve to a real
	// account; anything else looks up empty (deleted/unknown user).
	knownUsers := map[string][2]string{
		"11111111-1111-1111-1111-111111111111": {"real@example.com", "Real Person"},
		"22222222-2222-2222-2222-222222222222": {"noname@example.com", ""},
	}
	lookup := func(uid string) (string, string) {
		if u, ok := knownUsers[uid]; ok {
			return u[0], u[1]
		}
		return "", ""
	}

	// knownEmails stands in for resolving a real address (desktop client modifier) to a
	// display name. Addresses not listed resolve empty, forcing the local-part fallback.
	knownEmails := map[string]string{
		"bob@example.com":      "Bob Builder",
		"admin@sesamefs.local": "Admin User",
		"33333333-3333-3333-3333-333333333333@sesamefs.local": "UUID Local User",
	}
	nameForEmail := func(email string) string {
		return knownEmails[email]
	}

	tests := []struct {
		name          string
		entryModifier string
		blameUID      string
		wantEmail     string
		wantName      string
	}{
		{
			// Seafile desktop client stores a real address directly; use it as-is and
			// derive the name from the local-part when the account is not resolvable.
			name:          "real address on entry wins and ignores blame",
			entryModifier: "alice@example.com",
			blameUID:      "11111111-1111-1111-1111-111111111111",
			wantEmail:     "alice@example.com",
			wantName:      "alice",
		},
		{
			// A real address that IS a known account resolves to its display name, so the
			// same user reads identically whether stamped as an address or a user id.
			name:          "real address resolves to account display name",
			entryModifier: "bob@example.com",
			blameUID:      "",
			wantEmail:     "bob@example.com",
			wantName:      "Bob Builder",
		},
		{
			// Friendly local emails are real accounts too; only <uuid>@sesamefs.local is
			// synthetic, so admin@sesamefs.local must not be treated as an internal uid.
			name:          "friendly local email is treated as real account",
			entryModifier: "admin@sesamefs.local",
			blameUID:      "",
			wantEmail:     "admin@sesamefs.local",
			wantName:      "Admin User",
		},
		{
			// Even a UUID-shaped local email is real if the account exists; the lookup must
			// win before the reserved synthetic pattern is considered.
			name:          "uuid-shaped local email can still be a real account",
			entryModifier: "33333333-3333-3333-3333-333333333333@sesamefs.local",
			blameUID:      "",
			wantEmail:     "33333333-3333-3333-3333-333333333333@sesamefs.local",
			wantName:      "UUID Local User",
		},
		{
			// Synthetic <uid>@sesamefs.local resolves to the real account.
			name:          "synthetic entry modifier resolves to real account",
			entryModifier: "11111111-1111-1111-1111-111111111111@sesamefs.local",
			blameUID:      "",
			wantEmail:     "real@example.com",
			wantName:      "Real Person",
		},
		{
			// Blame uid resolves to the real account when the entry has no modifier.
			name:          "blame uid resolves to real account",
			entryModifier: "",
			blameUID:      "11111111-1111-1111-1111-111111111111",
			wantEmail:     "real@example.com",
			wantName:      "Real Person",
		},
		{
			// Account exists but has no display name: derive name from the email.
			name:          "known account without name derives name from email",
			entryModifier: "",
			blameUID:      "22222222-2222-2222-2222-222222222222",
			wantEmail:     "noname@example.com",
			wantName:      "noname",
		},
		{
			// Unknown id (deleted user) falls back to the synthetic address, never
			// to the requester.
			name:          "unknown id falls back to synthetic address",
			entryModifier: "",
			blameUID:      "99999999-9999-9999-9999-999999999999",
			wantEmail:     "99999999-9999-9999-9999-999999999999@sesamefs.local",
			wantName:      "99999999-9999-9999-9999-999999999999",
		},
		{
			// Regression guard for the original bug: with nothing resolvable the
			// field must be empty, NEVER the requesting user.
			name:          "unresolved stays empty, never falls back to requester",
			entryModifier: "",
			blameUID:      "",
			wantEmail:     "",
			wantName:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			email, name := composeLastModifierIdentity(tt.entryModifier, tt.blameUID, lookup, nameForEmail)
			if email != tt.wantEmail {
				t.Errorf("email = %q, want %q", email, tt.wantEmail)
			}
			if name != tt.wantName {
				t.Errorf("name = %q, want %q", name, tt.wantName)
			}
		})
	}
}

func TestContentModifiedFileEntry(t *testing.T) {
	src := FSEntry{
		Name:     "draft.docx",
		ID:       "historic-fsid",
		Mode:     ModeFile,
		MTime:    100,
		Size:     4096,
		Modifier: "original-author@sesamefs.local",
	}

	got := contentModifiedFileEntry(src, "restored.docx", "reverter-uid", 200)

	if got.Name != "restored.docx" {
		t.Errorf("Name = %q, want %q", got.Name, "restored.docx")
	}
	if got.ID != src.ID {
		t.Errorf("ID = %q, want %q", got.ID, src.ID)
	}
	if got.Mode != src.Mode {
		t.Errorf("Mode = %d, want %d", got.Mode, src.Mode)
	}
	if got.Size != src.Size {
		t.Errorf("Size = %d, want %d", got.Size, src.Size)
	}
	if got.MTime != 200 {
		t.Errorf("MTime = %d, want %d", got.MTime, 200)
	}
	if got.Modifier != "reverter-uid@sesamefs.local" {
		t.Errorf("Modifier = %q, want %q", got.Modifier, "reverter-uid@sesamefs.local")
	}
}

func TestPublicModifierIdentity(t *testing.T) {
	tests := []struct {
		name      string
		email     string
		person    string
		wantEmail string
		wantName  string
	}{
		{name: "real email is preserved", email: "real@example.com", person: "Real Person", wantEmail: "real@example.com", wantName: "Real Person"},
		{name: "friendly local email is preserved", email: "admin@sesamefs.local", person: "Admin User", wantEmail: "admin@sesamefs.local", wantName: "Admin User"},
		{name: "synthetic fallback is hidden", email: "11111111-1111-1111-1111-111111111111@sesamefs.local", person: "11111111-1111-1111-1111-111111111111", wantEmail: "", wantName: ""},
		{name: "empty stays empty", email: "", person: "", wantEmail: "", wantName: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			email, name := publicModifierIdentity(tt.email, tt.person)
			if email != tt.wantEmail {
				t.Errorf("email = %q, want %q", email, tt.wantEmail)
			}
			if name != tt.wantName {
				t.Errorf("name = %q, want %q", name, tt.wantName)
			}
		})
	}
}

func TestApplyResolvedModifierToDirent(t *testing.T) {
	var dirent Dirent
	applyResolvedModifierToDirent(&dirent, "real@example.com", "Real Person")
	if dirent.ModifierEmail != "real@example.com" {
		t.Errorf("ModifierEmail = %q, want %q", dirent.ModifierEmail, "real@example.com")
	}
	if dirent.ModifierContactEmail != "real@example.com" {
		t.Errorf("ModifierContactEmail = %q, want %q", dirent.ModifierContactEmail, "real@example.com")
	}
	if dirent.ModifierName != "Real Person" {
		t.Errorf("ModifierName = %q, want %q", dirent.ModifierName, "Real Person")
	}

	var empty Dirent
	applyResolvedModifierToDirent(&empty, "", "ignored")
	if empty.ModifierEmail != "" || empty.ModifierContactEmail != "" || empty.ModifierName != "" {
		t.Errorf("empty dirent modifier fields = %#v, want zero values", empty)
	}
}

// walkFixture builds the loadCommit / fileFSIDAt closures from an in-memory commit
// graph so the blame walk can be exercised without a database.
type walkCommit struct {
	parent     string
	root       string
	creator    string
	fsidAtPath string // the file's fs_id in this commit's tree (empty = absent)
}

func newWalkLoaders(graph map[string]walkCommit) (
	func(string) (commitWalkNode, bool),
	func(string) string,
) {
	rootToFSID := make(map[string]string, len(graph))
	for _, c := range graph {
		rootToFSID[c.root] = c.fsidAtPath
	}
	loadCommit := func(id string) (commitWalkNode, bool) {
		c, ok := graph[id]
		if !ok {
			return commitWalkNode{}, false
		}
		return commitWalkNode{rootFSID: c.root, parentID: c.parent, creator: c.creator}, true
	}
	fileFSIDAt := func(root string) string { return rootToFSID[root] }
	return loadCommit, fileFSIDAt
}

func TestResolveLastModifierFromWalk(t *testing.T) {
	const fsid = "CURRENT_FSID"
	const other = "OLD_FSID"

	tests := []struct {
		name    string
		head    string
		current string
		cap     int
		graph   map[string]walkCommit
		want    string
	}{
		{
			name:    "introduced a few commits back, then fs_id changes",
			head:    "c3",
			current: fsid,
			cap:     64,
			graph: map[string]walkCommit{
				// c3 (newest) and c2 carry the current version; c1 had the old one.
				"c3": {parent: "c2", root: "r3", creator: "userC", fsidAtPath: fsid},
				"c2": {parent: "c1", root: "r2", creator: "userB", fsidAtPath: fsid},
				"c1": {parent: "", root: "r1", creator: "userA", fsidAtPath: other},
			},
			// userB authored c2, the oldest contiguous commit still holding fsid.
			want: "userB",
		},
		{
			name:    "file present all the way to the creating root commit",
			head:    "c2",
			current: fsid,
			cap:     64,
			graph: map[string]walkCommit{
				"c2": {parent: "c1", root: "r2", creator: "userB", fsidAtPath: fsid},
				"c1": {parent: "", root: "r1", creator: "userA", fsidAtPath: fsid},
			},
			want: "userA",
		},
		{
			name:    "single commit, parent empty",
			head:    "c1",
			current: fsid,
			cap:     64,
			graph: map[string]walkCommit{
				"c1": {parent: "", root: "r1", creator: "userA", fsidAtPath: fsid},
			},
			want: "userA",
		},
		{
			name:    "cap exhausted before reaching boundary returns empty",
			head:    "c3",
			current: fsid,
			cap:     2, // chain is longer than the cap and never hits a boundary
			graph: map[string]walkCommit{
				"c3": {parent: "c2", root: "r3", creator: "userC", fsidAtPath: fsid},
				"c2": {parent: "c1", root: "r2", creator: "userB", fsidAtPath: fsid},
				"c1": {parent: "", root: "r1", creator: "userA", fsidAtPath: fsid},
			},
			want: "", // unresolved: do not misattribute to userB (the 2nd commit)
		},
		{
			name:    "head tree lacks current fs_id resolves to empty",
			head:    "c1",
			current: fsid,
			cap:     64,
			graph: map[string]walkCommit{
				"c1": {parent: "", root: "r1", creator: "userA", fsidAtPath: other},
			},
			want: "",
		},
		{
			name:    "missing commit row mid-walk returns empty",
			head:    "c2",
			current: fsid,
			cap:     64,
			graph: map[string]walkCommit{
				// c2 holds fsid but its parent c1 is absent from the graph, so the
				// boundary is never reached.
				"c2": {parent: "c1", root: "r2", creator: "userB", fsidAtPath: fsid},
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loadCommit, fileFSIDAt := newWalkLoaders(tt.graph)
			got := resolveLastModifierFromWalk(tt.head, tt.current, tt.cap, loadCommit, fileFSIDAt)
			if got != tt.want {
				t.Errorf("resolveLastModifierFromWalk = %q, want %q", got, tt.want)
			}
		})
	}
}
