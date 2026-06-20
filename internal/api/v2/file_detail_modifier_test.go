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

	tests := []struct {
		name          string
		entryModifier string
		blameUID      string
		wantEmail     string
		wantName      string
	}{
		{
			// Seafile desktop client stores a real address directly; use it as-is.
			name:          "real address on entry wins and ignores blame",
			entryModifier: "alice@example.com",
			blameUID:      "11111111-1111-1111-1111-111111111111",
			wantEmail:     "alice@example.com",
			wantName:      "alice",
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
			email, name := composeLastModifierIdentity(tt.entryModifier, tt.blameUID, lookup)
			if email != tt.wantEmail {
				t.Errorf("email = %q, want %q", email, tt.wantEmail)
			}
			if name != tt.wantName {
				t.Errorf("name = %q, want %q", name, tt.wantName)
			}
		})
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
