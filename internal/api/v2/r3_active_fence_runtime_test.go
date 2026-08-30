package v2

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
)

func TestR3RegisterUploadedBlockTargetActiveFenceHasNoMetadataSideEffects(t *testing.T) {
	oldAdd := registerUploadedBlockAddProvisionalRefFn
	oldFence := registerUploadedBlockFenceActiveFn
	oldRepair := registerUploadedBlockRepairMetadataFn
	oldResolve := resolveFreshInstallRepresentationFn
	oldInstall := registerUploadedBlockInstallMetadataFn
	t.Cleanup(func() {
		registerUploadedBlockAddProvisionalRefFn = oldAdd
		registerUploadedBlockFenceActiveFn = oldFence
		registerUploadedBlockRepairMetadataFn = oldRepair
		resolveFreshInstallRepresentationFn = oldResolve
		registerUploadedBlockInstallMetadataFn = oldInstall
	})

	var calls []string
	registerUploadedBlockAddProvisionalRefFn = func(*FSHelper, string, string, string, string, string, time.Time) error {
		calls = append(calls, "up")
		return nil
	}
	registerUploadedBlockFenceActiveFn = func(*FSHelper, string, string) (bool, error) {
		calls = append(calls, "fence")
		return true, nil
	}
	registerUploadedBlockRepairMetadataFn = func(*FSHelper, string, string, string, string, int, BlockMaterializationTarget) error {
		t.Fatal("R3 HANDSHAKE: active fence reached repair metadata")
		return nil
	}
	resolveFreshInstallRepresentationFn = func(context.Context, *FSHelper, string, string) (string, error) {
		t.Fatal("R3 HANDSHAKE: active fence reached fresh representation resolution")
		return "", nil
	}
	registerUploadedBlockInstallMetadataFn = func(context.Context, *FSHelper, string, string, string, string, int, BlockMaterializationTarget) db.InstallBlockMetadataResult {
		t.Fatal("R3 HANDSHAKE: active fence reached metadata install")
		return db.InstallBlockMetadataResult{}
	}

	err := (&FSHelper{}).RegisterUploadedBlockTarget(
		context.Background(), "org", "repo", uploadReuseTestBlockID, "fenced-op", 1,
		BlockMaterializationTarget{StorageClass: "hot", StorageKey: "key"}, "",
	)
	if !errors.Is(err, ErrBlockDeleteInProgress) {
		t.Fatalf("R3 HANDSHAKE: active fence error = %v, want ErrBlockDeleteInProgress", err)
	}
	if got := strings.Join(calls, ","); got != "up,fence" {
		t.Fatalf("R3 HANDSHAKE: active fence calls = %v, want [up fence] with zero metadata side effects", calls)
	}
}
