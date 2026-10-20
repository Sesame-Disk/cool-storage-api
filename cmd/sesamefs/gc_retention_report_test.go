package main

import (
	"testing"

	"github.com/google/uuid"
)

const nilUUID = "00000000-0000-0000-0000-000000000000"

// Usage errors stop in argument parsing (exit 1), before any configuration or
// database access, so a typo can neither scan the whole deployment nor look like
// "incomplete" (exit 2). Asserting on the parser, not the whole command, keeps a
// later configuration failure from passing this test by accident.
func TestGCMetadataRetentionReportRejectsBadUsage(t *testing.T) {
	for name, args := range map[string][]string{
		"positional library id": {"6f0c2a1e-0000-4000-8000-000000000000"},
		"flag after positional": {"foo", "-library", "6f0c2a1e-0000-4000-8000-000000000000"},
		"unknown flag":          {"-limit", "5"},
		"invalid library":       {"-library", "not-a-uuid"},
		"nil library":           {"-library", nilUUID},
		"empty library =":       {"-library="},
		"empty library arg":     {"-library", ""},
		"empty org =":           {"-org="},
		"empty org arg":         {"-org", ""},
		"invalid org":           {"-org", "nope"},
		"negative limit":        {"-max-libraries", "-1"},
	} {
		if scope, exit, ok := parseGCMetadataRetentionArgs(args); ok || exit != 1 {
			t.Errorf("%s: ok=%v exit=%d scope=%+v, want a usage error (exit 1)", name, ok, exit, scope)
		}
	}
	if _, exit, ok := parseGCMetadataRetentionArgs([]string{"-h"}); ok || exit != 0 {
		t.Errorf("-h: ok=%v exit=%d, want exit 0", ok, exit)
	}
	if got := gcMetadataRetentionReport([]string{"-library", nilUUID}); got != 1 {
		t.Errorf("command with nil -library: exit %d, want 1", got)
	}
}

// The all-zero UUID names the platform org, so -org keeps it as an explicit
// filter instead of reading it as "any org".
func TestParseGCMetadataRetentionArgsKeepsThePlatformOrgFilter(t *testing.T) {
	scope, _, ok := parseGCMetadataRetentionArgs([]string{"-org", nilUUID})
	if !ok || !scope.OrgSet || scope.OrgID != uuid.Nil || scope.LibraryID != uuid.Nil {
		t.Fatalf("-org <platform org>: scope=%+v ok=%v", scope, ok)
	}
	scope, _, ok = parseGCMetadataRetentionArgs(nil)
	if !ok || scope.OrgSet || scope.LibraryID != uuid.Nil {
		t.Fatalf("no flags must scope nothing: scope=%+v ok=%v", scope, ok)
	}
	lib := uuid.New()
	scope, _, ok = parseGCMetadataRetentionArgs([]string{"-library", lib.String(), "-max-libraries", "3"})
	if !ok || scope.LibraryID != lib || scope.OrgSet || scope.MaxLibraries != 3 {
		t.Fatalf("-library: scope=%+v ok=%v", scope, ok)
	}
}
