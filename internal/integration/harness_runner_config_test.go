//go:build integration

package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
)

const runnerConfigurationChild = "SESAMEFS_RUNNER_CONFIG_CHILD"
const runnerConfigurationComplete = "MULTIDC_RUNNER_CONFIGURATION_LOADED"

func TestMultidcRunnerConfigurationChild(t *testing.T) {
	if os.Getenv(runnerConfigurationChild) != "1" {
		t.Skip("runner configuration child only")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	wantDCs := map[string]int{"dc-na": 1, "dc-eu": 1, "dc-asia": 1}
	if !cfg.Auth.DevMode || !reflect.DeepEqual(cfg.Database.Hosts, []string{"cassandra-na:9042"}) || cfg.Database.LocalDC != "dc-na" || cfg.Database.ReplicationClass != "NetworkTopologyStrategy" || !reflect.DeepEqual(cfg.Database.ReplicationDCs, wantDCs) {
		t.Fatal("standalone runner lost explicit development mode or declared Cassandra topology")
	}
	t.Log(runnerConfigurationComplete)
}

// Execute each real runner_env with docker exec replaced by direct execution.
// The subprocess receives no inherited auth, storage or Compose configuration.
// This exercises full config.Load without needing Cassandra or starting a stack.
func TestMultidcRunnerConfiguration(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"h1-initial-head-multidc-validation.sh",
		"w2-post-head-multidc-validation.sh",
		"w2-sync-putblock-xdc-provenance-validation.sh",
		"library-head-serial-domain-multidc-validation.sh",
	} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "..", "scripts", name))
			if err != nil {
				t.Fatal(err)
			}
			source := strings.ReplaceAll(string(data), "\r\n", "\n")
			start := strings.Index(source, "runner_env() {")
			if start < 0 {
				t.Fatal("runner_env missing")
			}
			end := strings.Index(source[start:], "\n}")
			if end < 0 {
				t.Fatal("runner_env terminator missing")
			}
			function := source[start : start+end+2]
			script := "set -eu\nRUNNER=configuration-probe\ndocker() { test \"$1\" = exec; shift 2; \"$@\"; }\n" + function + "\nrunner_env dc-na \"$@\"\n"
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, "bash", "-c", script, "runner-config", binary, "-test.run=^TestMultidcRunnerConfigurationChild$", "-test.v", "-test.count=1")
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "CONFIG_PATH=/nonexistent/runner-config-probe.yaml", "SESAMEFS_W2_PROCESS_CHILD=1", runnerConfigurationChild + "=1"}
			output, err := cmd.CombinedOutput()
			if err != nil || !strings.Contains(string(output), runnerConfigurationComplete) {
				t.Fatalf("standalone runner configuration not proved: %v\n%s", err, output)
			}
			t.Logf("%s", output)
		})
	}
}
