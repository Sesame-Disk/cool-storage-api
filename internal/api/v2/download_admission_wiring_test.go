package v2

import (
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/downloadadmission"
)

func TestDownloadAdmissionCoordinatorSetters(t *testing.T) {
	coordinator, err := downloadadmission.New(&config.DownloadAdmissionConfig{})
	if err != nil {
		t.Fatalf("new download admission coordinator: %v", err)
	}

	fileViewHandler := &FileViewHandler{}
	fileViewHandler.SetDownloadAdmissionCoordinator(coordinator)
	if fileViewHandler.downloadAdmission != coordinator {
		t.Fatal("file view handler received a different download admission coordinator")
	}

	shareLinkViewHandler := NewShareLinkViewHandler(nil, config.DefaultConfig(), nil, nil, nil, "")
	shareLinkViewHandler.SetDownloadAdmissionCoordinator(coordinator)
	if shareLinkViewHandler.downloadAdmission != coordinator {
		t.Fatal("share link view handler received a different download admission coordinator")
	}
}
