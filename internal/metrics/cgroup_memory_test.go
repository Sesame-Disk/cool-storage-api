package metrics

import (
	"errors"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestCgroupMemoryCollectorFallsBackAfterMalformedV2Value(t *testing.T) {
	for _, malformed := range []string{"not-a-number", "NaN", "+Inf"} {
		t.Run(malformed, func(t *testing.T) {
			c := newCgroupMemoryCollector().(*cgroupMemoryCollector)
			c.paths = []string{"v2", "v1"}
			c.readFile = func(path string) ([]byte, error) {
				switch path {
				case "v2":
					return []byte(malformed), nil
				case "v1":
					return []byte("12345\n"), nil
				default:
					return nil, errors.New("unexpected path")
				}
			}

			want := `
# HELP process_cgroup_memory_current_bytes Current memory charged to this process's container cgroup, when available.
# TYPE process_cgroup_memory_current_bytes gauge
process_cgroup_memory_current_bytes 12345
`
			if err := testutil.CollectAndCompare(c, strings.NewReader(want), "process_cgroup_memory_current_bytes"); err != nil {
				t.Fatal(err)
			}
		})
	}
}
