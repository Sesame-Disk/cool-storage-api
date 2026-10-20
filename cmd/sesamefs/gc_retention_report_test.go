package main

import "testing"

// Usage errors exit 1 before any configuration or database access, so a typo
// can neither scan the whole deployment nor look like "incomplete" (exit 2).
func TestGCMetadataRetentionReportRejectsBadUsage(t *testing.T) {
	for name, args := range map[string][]string{
		"positional library id": {"6f0c2a1e-0000-4000-8000-000000000000"},
		"flag after positional": {"foo", "-library", "6f0c2a1e-0000-4000-8000-000000000000"},
		"unknown flag":          {"-limit", "5"},
		"invalid library":       {"-library", "not-a-uuid"},
		"invalid org":           {"-org", "nope"},
		"negative limit":        {"-max-libraries", "-1"},
	} {
		if got := gcMetadataRetentionReport(args); got != 1 {
			t.Errorf("%s: exit %d, want 1", name, got)
		}
	}
	if got := gcMetadataRetentionReport([]string{"-h"}); got != 0 {
		t.Errorf("-h: exit %d, want 0", got)
	}
}
