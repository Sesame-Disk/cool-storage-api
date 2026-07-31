package metrics

import (
	"os"
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
)

type cgroupMemoryCollector struct {
	current *prometheus.Desc
}

func newCgroupMemoryCollector() prometheus.Collector {
	return &cgroupMemoryCollector{current: prometheus.NewDesc(
		"process_cgroup_memory_current_bytes",
		"Current memory charged to this process's container cgroup, when available.",
		nil,
		nil,
	)}
}

func (c *cgroupMemoryCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.current
}

func (c *cgroupMemoryCollector) Collect(ch chan<- prometheus.Metric) {
	for _, path := range []string{
		"/sys/fs/cgroup/memory.current",
		"/sys/fs/cgroup/memory/memory.usage_in_bytes",
	} {
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		value, err := strconv.ParseFloat(strings.TrimSpace(string(body)), 64)
		if err != nil || value < 0 {
			return
		}
		ch <- prometheus.MustNewConstMetric(c.current, prometheus.GaugeValue, value)
		return
	}
}
