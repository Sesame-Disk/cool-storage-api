package metrics

import (
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
)

type cgroupMemoryCollector struct {
	current  *prometheus.Desc
	paths    []string
	readFile func(string) ([]byte, error)
}

func newCgroupMemoryCollector() prometheus.Collector {
	return &cgroupMemoryCollector{current: prometheus.NewDesc(
		"process_cgroup_memory_current_bytes",
		"Current memory charged to this process's container cgroup, when available.",
		nil,
		nil,
	), paths: []string{
		"/sys/fs/cgroup/memory.current",
		"/sys/fs/cgroup/memory/memory.usage_in_bytes",
	}, readFile: os.ReadFile}
}

func (c *cgroupMemoryCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.current
}

func (c *cgroupMemoryCollector) Collect(ch chan<- prometheus.Metric) {
	for _, path := range c.paths {
		body, err := c.readFile(path)
		if err != nil {
			continue
		}
		// An unreadable v2 file must not cost us the v1 fallback: on a partial or
		// unusual cgroup mount the first path can exist and still hold something
		// unparseable, and giving up there would drop the metric entirely.
		value, err := strconv.ParseFloat(strings.TrimSpace(string(body)), 64)
		if err != nil || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			continue
		}
		ch <- prometheus.MustNewConstMetric(c.current, prometheus.GaugeValue, value)
		return
	}
}
