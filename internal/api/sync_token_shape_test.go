package api

import "testing"

// The route-level contract for ISSUE-SYNC-LINK-TOKEN-AUTH-01, pinned without a
// server. The integration suite proves the middleware enforces it end to end;
// this states the rule itself, so a future edit to the predicate fails here
// first and with a name that says which clause moved.
func TestIsRepositorySyncToken(t *testing.T) {
	const repo = "11111111-2222-3333-4444-555555555555"
	const otherRepo = "99999999-8888-7777-6666-555555555555"

	valid := func() *AccessToken {
		return &AccessToken{Source: "", Path: "/", RepoID: repo}
	}

	tests := []struct {
		name        string
		token       *AccessToken
		routeRepoID string
		want        bool
		why         string
	}{
		{
			name:        "repository sync token",
			token:       valid(),
			routeRepoID: repo,
			want:        true,
			why:         "the shape GetDownloadInfo issues must keep working",
		},
		{
			name:        "share-link token for one file",
			token:       &AccessToken{Source: "link", Path: "/shared.txt", RepoID: repo},
			routeRepoID: repo,
			want:        false,
		},
		{
			name:        "share-link token for the library root",
			token:       &AccessToken{Source: "link", Path: "/", RepoID: repo},
			routeRepoID: repo,
			want:        false,
			why:         "only the source clause can refuse this one: the path and the repository both match",
		},
		{
			name:        "authenticated file-scoped download token",
			token:       &AccessToken{Source: "", Path: "/report.pdf", RepoID: repo},
			routeRepoID: repo,
			want:        false,
			why:         "a token issued to read one file is not a repository credential",
		},
		{
			name:        "sync token aimed at a different repository",
			token:       valid(),
			routeRepoID: otherRepo,
			want:        false,
			why:         "handlers read the route parameter, so an unbound token operates on the wrong library",
		},
		{
			name:        "unknown future source with the right shape",
			token:       &AccessToken{Source: "onlyoffice", Path: "/", RepoID: repo},
			routeRepoID: repo,
			want:        false,
			why:         "the allowlist is the point: a new source must be admitted deliberately, not by default",
		},
		{
			name:        "repository id differing only in case",
			token:       &AccessToken{Source: "", Path: "/", RepoID: "11111111-2222-3333-4444-555555555555"},
			routeRepoID: "11111111-2222-3333-4444-555555555555",
			want:        true,
		},
		{
			name:        "empty route repository",
			token:       valid(),
			routeRepoID: "",
			want:        false,
			why:         "an empty route parameter must never match an empty token field into an accept",
		},
		{
			name:        "nil token",
			token:       nil,
			routeRepoID: repo,
			want:        false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isRepositorySyncToken(tc.token, tc.routeRepoID)
			if got != tc.want {
				msg := tc.why
				if msg == "" {
					msg = "a token of this shape must not authenticate the sync surface"
				}
				t.Errorf("isRepositorySyncToken() = %v, want %v: %s", got, tc.want, msg)
			}
		})
	}
}
