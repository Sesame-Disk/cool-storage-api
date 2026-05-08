package traffic

import "time"

// StorageQuotaPrechecker is the subset of Checker used by upload handlers.
type StorageQuotaPrechecker interface {
	CheckStorageQuota(orgID string, additionalBytes int64) (QuotaStatus, error)
}

// TrafficQuotaPrechecker is the subset of Checker used by request handlers.
type TrafficQuotaPrechecker interface {
	CheckTrafficQuota(orgID, userID, direction string, additionalBytes int64) (QuotaStatus, error)
}

// UploadQuotaPrechecker is the subset of Checker required for upload preflight.
type UploadQuotaPrechecker interface {
	StorageQuotaPrechecker
	TrafficQuotaPrechecker
}

// UploadQuotaCheckResult keeps the storage and traffic outcomes separate so
// handlers can preserve existing response semantics while sharing the preflight.
type UploadQuotaCheckResult struct {
	StorageStatus QuotaStatus
	TrafficStatus QuotaStatus
}

// CheckUploadQuotaWithChecker evaluates storage quota first and traffic quota
// second. A storage rejection short-circuits traffic evaluation because the
// handler will fail before reading any bytes. Negative additionalBytes (e.g.
// chunked uploads with no Content-Length) are treated as 0 so the projection
// arithmetic in the underlying checker stays well-defined.
func CheckUploadQuotaWithChecker(checker UploadQuotaPrechecker, orgID, userID string, additionalBytes int64) (UploadQuotaCheckResult, error) {
	result := UploadQuotaCheckResult{
		StorageStatus: QuotaStatus{Allowed: true},
		TrafficStatus: QuotaStatus{Allowed: true},
	}
	if checker == nil {
		return result, nil
	}
	if additionalBytes < 0 {
		additionalBytes = 0
	}

	storageStatus, err := checker.CheckStorageQuota(orgID, additionalBytes)
	if err != nil {
		return result, err
	}
	result.StorageStatus = storageStatus
	if !storageStatus.Allowed {
		return result, nil
	}

	trafficStatus, err := checker.CheckTrafficQuota(orgID, userID, "upload", additionalBytes)
	if err != nil {
		return result, err
	}
	result.TrafficStatus = trafficStatus
	return result, nil
}

// TrafficPeriodRecorder is the subset of Recorder used after quota pre-checks.
type TrafficPeriodRecorder interface {
	RecordWithPeriod(orgID, userID, trafficType string, bytes int64, periodStartedAt time.Time)
}

// CheckTrafficQuotaWithChecker evaluates traffic quota with the supplied checker.
// A nil checker means quota enforcement is disabled for this request path.
func CheckTrafficQuotaWithChecker(checker TrafficQuotaPrechecker, orgID, userID, direction string, additionalBytes int64) (QuotaStatus, error) {
	if checker == nil {
		return QuotaStatus{Allowed: true}, nil
	}
	return checker.CheckTrafficQuota(orgID, userID, direction, additionalBytes)
}

// RecordCheckedTransfer records bytes using the period resolved during the
// earlier traffic quota pre-check. When quotaStatus.PeriodStartedAt is zero,
// the recorder falls back to its legacy DB lookup path.
func RecordCheckedTransfer(recorder TrafficPeriodRecorder, quotaStatus QuotaStatus, orgID, userID, trafficType string, bytes int64) {
	if recorder == nil || bytes <= 0 {
		return
	}
	recorder.RecordWithPeriod(orgID, userID, trafficType, bytes, quotaStatus.PeriodStartedAt)
}

// TrafficQuotaWarningHeader returns the value that should be written to the
// X-Quota-Warning header when the status represents a soft warning.
func TrafficQuotaWarningHeader(quotaStatus QuotaStatus) (string, bool) {
	if !quotaStatus.Warning || quotaStatus.Reason == "" {
		return "", false
	}
	return quotaStatus.Reason, true
}

// TrafficQuotaExceededResponse builds a consistent JSON payload for blocked
// traffic requests while allowing each handler to choose its user-facing message
// and whether to expose the internal reason code.
func TrafficQuotaExceededResponse(quotaStatus QuotaStatus, message string, includeReason bool) map[string]interface{} {
	response := map[string]interface{}{"error": message}
	if includeReason && quotaStatus.Reason != "" {
		response["reason"] = quotaStatus.Reason
	}
	return response
}

// StorageQuotaExceededResponse builds the JSON payload for blocked uploads due
// to storage quota. The shape mirrors TrafficQuotaExceededResponse so the
// frontend can render storage and traffic rejections through the same path,
// and includes usage/limit metadata so the UI can show "X of Y used".
func StorageQuotaExceededResponse(quotaStatus QuotaStatus, message string) map[string]interface{} {
	response := map[string]interface{}{
		"error":  message,
		"reason": "storage",
	}
	if quotaStatus.UsedBytes > 0 {
		response["used_bytes"] = quotaStatus.UsedBytes
	}
	if quotaStatus.LimitBytes > 0 {
		response["limit_bytes"] = quotaStatus.LimitBytes
	}
	if quotaStatus.Plan != "" {
		response["plan"] = quotaStatus.Plan
	}
	return response
}
