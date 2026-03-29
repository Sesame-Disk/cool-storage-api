package traffic

import (
	"errors"
	"testing"
	"time"
)

type fakeTrafficChecker struct {
	status          QuotaStatus
	err             error
	called          bool
	orgID           string
	userID          string
	direction       string
	additionalBytes int64
}

func (f *fakeTrafficChecker) CheckTrafficQuota(orgID, userID, direction string, additionalBytes int64) (QuotaStatus, error) {
	f.called = true
	f.orgID = orgID
	f.userID = userID
	f.direction = direction
	f.additionalBytes = additionalBytes
	return f.status, f.err
}

type fakeTrafficRecorder struct {
	called          bool
	orgID           string
	userID          string
	trafficType     string
	bytes           int64
	periodStartedAt time.Time
}

func (f *fakeTrafficRecorder) RecordWithPeriod(orgID, userID, trafficType string, bytes int64, periodStartedAt time.Time) {
	f.called = true
	f.orgID = orgID
	f.userID = userID
	f.trafficType = trafficType
	f.bytes = bytes
	f.periodStartedAt = periodStartedAt
}

func TestCheckTrafficQuotaWithChecker_AllowsWhenCheckerMissing(t *testing.T) {
	status, err := CheckTrafficQuotaWithChecker(nil, "org-1", "user-1", "download", 123)
	if err != nil {
		t.Fatalf("CheckTrafficQuotaWithChecker(nil) error = %v", err)
	}
	if !status.Allowed {
		t.Fatalf("CheckTrafficQuotaWithChecker(nil) Allowed = false, want true")
	}
	if !status.PeriodStartedAt.IsZero() {
		t.Fatalf("CheckTrafficQuotaWithChecker(nil) PeriodStartedAt = %s, want zero", status.PeriodStartedAt)
	}
}

func TestCheckTrafficQuotaWithChecker_DelegatesToChecker(t *testing.T) {
	wantPeriod := time.Date(2026, time.March, 15, 0, 0, 0, 0, time.UTC)
	fake := &fakeTrafficChecker{
		status: QuotaStatus{Allowed: true, Warning: true, Reason: "traffic-download", PeriodStartedAt: wantPeriod},
	}

	status, err := CheckTrafficQuotaWithChecker(fake, "org-1", "user-1", "download", 456)
	if err != nil {
		t.Fatalf("CheckTrafficQuotaWithChecker(fake) error = %v", err)
	}
	if !fake.called {
		t.Fatal("expected checker to be called")
	}
	if fake.orgID != "org-1" || fake.userID != "user-1" || fake.direction != "download" || fake.additionalBytes != 456 {
		t.Fatalf("checker called with unexpected args: %+v", fake)
	}
	if !status.Warning || status.Reason != "traffic-download" {
		t.Fatalf("unexpected status: %+v", status)
	}
	if !status.PeriodStartedAt.Equal(wantPeriod) {
		t.Fatalf("PeriodStartedAt = %s, want %s", status.PeriodStartedAt, wantPeriod)
	}
}

func TestCheckTrafficQuotaWithChecker_PropagatesError(t *testing.T) {
	wantErr := errors.New("boom")
	fake := &fakeTrafficChecker{err: wantErr}

	_, err := CheckTrafficQuotaWithChecker(fake, "org-1", "user-1", "upload", 789)
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
}

func TestRecordCheckedTransfer_UsesResolvedPeriod(t *testing.T) {
	wantPeriod := time.Date(2026, time.April, 1, 0, 0, 0, 0, time.UTC)
	fake := &fakeTrafficRecorder{}
	status := QuotaStatus{Allowed: true, PeriodStartedAt: wantPeriod}

	RecordCheckedTransfer(fake, status, "org-1", "user-1", WebDownload, 2048)

	if !fake.called {
		t.Fatal("expected recorder to be called")
	}
	if fake.orgID != "org-1" || fake.userID != "user-1" || fake.trafficType != WebDownload || fake.bytes != 2048 {
		t.Fatalf("recorder called with unexpected args: %+v", fake)
	}
	if !fake.periodStartedAt.Equal(wantPeriod) {
		t.Fatalf("periodStartedAt = %s, want %s", fake.periodStartedAt, wantPeriod)
	}
}

func TestRecordCheckedTransfer_NoOpWithoutRecorderOrBytes(t *testing.T) {
	status := QuotaStatus{Allowed: true, PeriodStartedAt: time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)}
	RecordCheckedTransfer(nil, status, "org-1", "user-1", WebUpload, 10)

	fake := &fakeTrafficRecorder{}
	RecordCheckedTransfer(fake, status, "org-1", "user-1", WebUpload, 0)
	if fake.called {
		t.Fatal("expected zero-byte record to be ignored")
	}
}

func TestTrafficQuotaWarningHeader(t *testing.T) {
	warning, ok := TrafficQuotaWarningHeader(QuotaStatus{Warning: true, Reason: "traffic-upload"})
	if !ok || warning != "traffic-upload" {
		t.Fatalf("TrafficQuotaWarningHeader() = (%q, %v), want (traffic-upload, true)", warning, ok)
	}

	warning, ok = TrafficQuotaWarningHeader(QuotaStatus{Warning: false, Reason: "traffic-upload"})
	if ok || warning != "" {
		t.Fatalf("TrafficQuotaWarningHeader(non-warning) = (%q, %v), want (\"\", false)", warning, ok)
	}
}

func TestTrafficQuotaExceededResponse(t *testing.T) {
	response := TrafficQuotaExceededResponse(QuotaStatus{Reason: "traffic-download"}, "traffic quota exceeded", true)
	if response["error"] != "traffic quota exceeded" {
		t.Fatalf("response[error] = %v, want traffic quota exceeded", response["error"])
	}
	if response["reason"] != "traffic-download" {
		t.Fatalf("response[reason] = %v, want traffic-download", response["reason"])
	}

	response = TrafficQuotaExceededResponse(QuotaStatus{Reason: "traffic-download"}, "traffic quota exceeded", false)
	if _, exists := response["reason"]; exists {
		t.Fatal("did not expect reason when includeReason=false")
	}
}
