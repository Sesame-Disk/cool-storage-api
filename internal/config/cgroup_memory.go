package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const downloadAdmissionMemoryBudgetFractionDenominator int64 = 4

var cgroupMemoryLimitPaths = []string{
	"/sys/fs/cgroup/memory.max",
	"/sys/fs/cgroup/memory/memory.limit_in_bytes",
}

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

func validateDownloadAdmissionCgroupBudget(d DownloadAdmissionConfig) error {
	if !d.Enabled {
		return nil
	}
	limit, ok := cgroupMemoryLimitBytes()
	if !ok {
		return nil
	}
	return validateDownloadAdmissionCgroupBudgetForLimit(d, limit)
}

func validateDownloadAdmissionCgroupBudgetForLimit(d DownloadAdmissionConfig, limit int64) error {
	if !d.Enabled || limit <= 0 {
		return nil
	}
	allowed := limit / downloadAdmissionMemoryBudgetFractionDenominator
	if d.MemoryBudgetBytes > allowed {
		return fmt.Errorf("download_admission.memory_budget_bytes=%d exceeds 25%% of the detected cgroup memory limit %d; lower the budget and the admission caps together", d.MemoryBudgetBytes, limit)
	}
	return nil
}
