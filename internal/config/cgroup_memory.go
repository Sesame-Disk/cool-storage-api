package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

var cgroupMemoryLimitPaths = []string{
	"/sys/fs/cgroup/memory.max",
	"/sys/fs/cgroup/memory/memory.limit_in_bytes",
}

// cgroupMemoryLimit is a seam. Auto capacity derives from the host's memory
// limit, so a test that exercises it is otherwise a function of whatever the
// runner happens to impose — green on a laptop, red on a constrained CI agent,
// for reasons that have nothing to do with the change under test.
var cgroupMemoryLimit = cgroupMemoryLimitBytes

func parseCgroupMemoryLimit(value string) (int64, bool) {
	value = strings.TrimSpace(value)
	if value == "" || value == "max" {
		return 0, false
	}
	limit, err := strconv.ParseInt(value, 10, 64)
	if err != nil || limit <= 0 || limit >= 1<<60 {
		return 0, false
	}
	return limit, true
}

// cgroupMemoryLimitBytes reads the limit from the two paths a containerised
// deployment exposes at its cgroup root, which is the supported topology.
//
// It does not walk /proc/self/cgroup to find a limit imposed by an ancestor, so
// a systemd unit in a nested slice, or a v1 host without a namespace, reads as
// unlimited and falls back to the reference default rather than to a wrong
// number. That is not automatically the safe direction: on a host smaller than
// the 8 GiB baseline the fallback budget is larger than the machine warrants,
// which is why a deployment that cannot expose a limit should set the budget
// explicitly. This is container-limit detection, not a general effective-limit
// resolver.
func cgroupMemoryLimitBytes() (int64, bool) {
	for _, path := range cgroupMemoryLimitPaths {
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if limit, ok := parseCgroupMemoryLimit(string(body)); ok {
			return limit, true
		}
	}
	return 0, false
}

// validateDownloadAdmissionCgroupBudget charges the budget against the limit
// resolveDownloadAdmissionCapacity already observed, rather than taking a second
// reading of its own.
//
// Two reads inside one Validate() could disagree — the value can be rewritten
// while the process starts — and then a node would derive its capacities from
// one container size and be admitted or refused against another. Neither answer
// would be wrong on its own; the pair is what has no meaning. That snapshot is
// taken above every early return in the resolver precisely so this guard, which
// applies in manual mode too, always has it.
func (c *Config) validateDownloadAdmissionCgroupBudget() error {
	if c == nil || !c.DownloadAdmission.Enabled || c.downloadCgroupLimit <= 0 {
		return nil
	}
	return validateDownloadAdmissionCgroupBudgetForLimit(c.DownloadAdmission, c.downloadCgroupLimit)
}

// validateDownloadAdmissionCgroupBudgetForLimit keeps an explicit byte budget
// inside the same share the percentage setting would have derived. Enforcing a
// fixed 25% here while memory_budget_percent accepted more turned every larger
// share into a configuration that derived successfully and then refused to
// start, with an error telling the operator to lower the very value the setting
// had produced.
func validateDownloadAdmissionCgroupBudgetForLimit(d DownloadAdmissionConfig, limit int64) error {
	if !d.Enabled || limit <= 0 {
		return nil
	}
	percent := d.MemoryBudgetPercent
	if percent <= 0 || percent > MaxDownloadAdmissionMemoryBudgetPercent {
		percent = DefaultDownloadAdmissionMemoryBudgetPercent
	}
	allowed, ok := checkedNonNegativeMultiply(limit, int64(percent))
	if !ok {
		return fmt.Errorf("download admission cgroup budget check overflows")
	}
	allowed /= 100
	if d.MemoryBudgetBytes > allowed {
		return fmt.Errorf("download_admission.memory_budget_bytes=%d exceeds %d%% of the detected cgroup memory limit %d; lower the budget and the admission caps together, or raise download_admission.memory_budget_percent", d.MemoryBudgetBytes, percent, limit)
	}
	return nil
}
