//go:build integration

package integration

import (
	"os"
	"strings"
	"testing"
)

const sessionUploadOwnLivenessEnv = "SESAMEFS_REQUIRE_SESSIONUPLOAD_OWN_LIVENESS_EVIDENCE"

// sessionUploadOwnLivenessEvidenceState records each W2 SessionUpload-parity
// leg by name. Completeness is the conjunction of these fields, never a
// counter: marking one leg twice cannot hide another. Kept as a separate
// state/gate from borrowedFSOwnLivenessEvidenceState (internal/integration/borrowedfs_head_evidence_test.go)
// rather than extending it: that struct's field list is pinned by
// TestBorrowedFSOwnLivenessNamesArePinned and is explicitly W1/BorrowedFS-scoped.
type sessionUploadOwnLivenessEvidenceState struct {
	renewalVisibleBeforeHead     bool
	renewalExtendsNearExpiredTTL bool
	writerFirst                  bool
	gcFirst                      bool
	gcFullyRetiredBeforeRenewal  bool
	renewalRetryIsIdempotent     bool
}

func (state sessionUploadOwnLivenessEvidenceState) namedLegs() []struct {
	name string
	seen bool
} {
	return []struct {
		name string
		seen bool
	}{
		{"renewalVisibleBeforeHead", state.renewalVisibleBeforeHead},
		{"renewalExtendsNearExpiredTTL", state.renewalExtendsNearExpiredTTL},
		{"writerFirst", state.writerFirst},
		{"gcFirst", state.gcFirst},
		{"gcFullyRetiredBeforeRenewal", state.gcFullyRetiredBeforeRenewal},
		{"renewalRetryIsIdempotent", state.renewalRetryIsIdempotent},
	}
}

func (state sessionUploadOwnLivenessEvidenceState) complete() bool {
	for _, leg := range state.namedLegs() {
		if !leg.seen {
			return false
		}
	}
	return true
}

func (state sessionUploadOwnLivenessEvidenceState) missing() []string {
	missing := make([]string, 0, len(state.namedLegs()))
	for _, leg := range state.namedLegs() {
		if !leg.seen {
			missing = append(missing, leg.name)
		}
	}
	return missing
}

var sessionUploadOwnLivenessEvidence sessionUploadOwnLivenessEvidenceState

type sessionUploadOwnLivenessEvidenceGate struct{ observed bool }

func sessionUploadRequireOwnLivenessEvidence(t *testing.T) *sessionUploadOwnLivenessEvidenceGate {
	t.Helper()
	gate := &sessionUploadOwnLivenessEvidenceGate{}
	if os.Getenv(sessionUploadOwnLivenessEnv) != "1" {
		return gate
	}
	t.Cleanup(func() {
		if t.Skipped() {
			t.Errorf("%s=1 requires real Cassandra+MinIO SessionUpload own-liveness evidence, but the test skipped", sessionUploadOwnLivenessEnv)
		} else if !t.Failed() && !gate.observed {
			t.Errorf("%s=1 incomplete SessionUpload own-liveness evidence; missing=%s", sessionUploadOwnLivenessEnv, strings.Join(sessionUploadOwnLivenessEvidence.missing(), ","))
		}
	})
	return gate
}

func TestSessionUploadOwnLivenessEvidenceRequiresEveryNamedLeg(t *testing.T) {
	partial := sessionUploadOwnLivenessEvidenceState{renewalVisibleBeforeHead: true, writerFirst: true}
	if partial.complete() {
		t.Fatal("partial SessionUpload own-liveness evidence must not satisfy the package gate")
	}
	if got := strings.Join(partial.missing(), ","); !strings.Contains(got, "renewalExtendsNearExpiredTTL") || !strings.Contains(got, "gcFullyRetiredBeforeRenewal") || !strings.Contains(got, "renewalRetryIsIdempotent") {
		t.Fatalf("missing() must name absent legs individually, got %q", got)
	}
	full := sessionUploadOwnLivenessEvidenceState{
		renewalVisibleBeforeHead:     true,
		renewalExtendsNearExpiredTTL: true,
		writerFirst:                  true,
		gcFirst:                      true,
		gcFullyRetiredBeforeRenewal:  true,
		renewalRetryIsIdempotent:     true,
	}
	if !full.complete() || len(full.missing()) != 0 || len(full.namedLegs()) != 6 {
		t.Fatalf("all 6 named legs should satisfy the package gate; missing=%v legs=%d", full.missing(), len(full.namedLegs()))
	}
	twice := partial
	twice.renewalVisibleBeforeHead = true
	if twice.complete() {
		t.Fatal("marking one leg twice must not hide a different required leg")
	}
}
