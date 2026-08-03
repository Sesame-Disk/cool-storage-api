package api

import (
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
)

func TestServerInitializesAndWiresDownloadAdmissionCoordinator(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.DownloadAdmission = config.DownloadAdmissionConfig{
		Enabled:                true,
		MaxActivePerNode:       8,
		MaxActivePerAuthUser:   2,
		MaxActivePerLinkSource: 4,
		MaxActivePerClientLink: 2,
		MaxWaitersPerIdentity:  4,
		MaxWaitersPerNode:      8,
		AdmissionWait:          time.Second,
		PreparationDeadline:    time.Minute,
		IdleWriteTimeout:       time.Minute,
		RetryAfter:             2 * time.Second,
	}
	s := &Server{config: cfg}
	s.initializeDownloadAdmissionCoordinator()

	if s.downloadAdmission == nil {
		t.Fatal("server download admission coordinator = nil")
	}
	if got := s.downloadAdmission.RetryAfterSeconds(); got != 2 {
		t.Fatalf("coordinator retry-after = %d, want 2", got)
	}

	seafHTTPHandler := NewSeafHTTPHandler(nil, nil, nil, nil, cfg, nil)
	seafHTTPHandler.SetDownloadAdmissionCoordinator(s.downloadAdmission)
	if seafHTTPHandler.downloadAdmission != s.downloadAdmission {
		t.Fatal("SeafHTTP handler received a different download admission coordinator")
	}
}
