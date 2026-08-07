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
		return &AccessToken{Type: TokenTypeSync, Source: "", Path: "/", RepoID: repo}
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
			token:       &AccessToken{Type: TokenTypeDownload, Source: "link", Path: "/shared.txt", RepoID: repo},
			routeRepoID: repo,
			want:        false,
		},
		{
			name:        "share-link token for the library root",
			token:       &AccessToken{Type: TokenTypeDownload, Source: "link", Path: "/", RepoID: repo},
			routeRepoID: repo,
			want:        false,
			why:         "only the source clause can refuse this one: the path and the repository both match",
		},
		{
			name:        "authenticated file-scoped download token",
			token:       &AccessToken{Type: TokenTypeDownload, Source: "", Path: "/report.pdf", RepoID: repo},
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
			token:       &AccessToken{Type: TokenTypeSync, Source: "onlyoffice", Path: "/", RepoID: repo},
			routeRepoID: repo,
			want:        false,
			why:         "the allowlist is the point: a new source must be admitted deliberately, not by default",
		},
		{
			name:        "repository id differing only in case",
			token:       &AccessToken{Type: TokenTypeSync, Source: "", Path: "/", RepoID: "abcdef01-2345-6789-abcd-ef0123456789"},
			routeRepoID: "ABCDEF01-2345-6789-ABCD-EF0123456789",
			want:        true,
			why:         "the same UUID arrives from a URL segment and from storage; casing must not decide authorization",
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
		{
			name:        "download token with the perfect sync shape",
			token:       &AccessToken{Type: TokenTypeDownload, Source: "", Path: "/", RepoID: repo},
			routeRepoID: repo,
			want:        false,
			why:         "rooted path, empty source, right repository — only the token type refuses it, which is what makes a dedicated type worth having",
		},
		{
			name:        "upload token with the sync shape",
			token:       &AccessToken{Type: TokenTypeUpload, Source: "", Path: "/", RepoID: repo},
			routeRepoID: repo,
			want:        false,
			why:         "an upload credential is not a sync credential either",
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
