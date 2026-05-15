package api

import "github.com/Sesame-Disk/sesamefs/internal/traffic"

type apiQuotaChecker interface {
	traffic.TrafficQuotaPrechecker
	CheckStorageQuota(orgID, userID string, additionalBytes int64) (traffic.QuotaStatus, error)
}

var getAPIQuotaChecker = func() apiQuotaChecker {
	checker := traffic.GetChecker()
	if checker == nil {
		return nil
	}
	return checker
}