package v2

import (
	"log"
	"strings"
	"sync"
	"time"
)

var scheduledPublishedBlockReferenceRepairs sync.Map

var schedulePublishedBlockReferenceRepairSleepFn = time.Sleep

var schedulePublishedBlockReferenceRepairRunFn = func(repair func()) {
	go repair()
}

// SchedulePublishedBlockReferenceRepair runs one additional repair pass for a
// published fs_object's block references outside the request path. repairKey
// deduplicates concurrent schedulers for the same publish attempt.
func SchedulePublishedBlockReferenceRepair(repairKey, label string, repair func() error) {
	repairKey = strings.TrimSpace(repairKey)
	if repairKey == "" || repair == nil {
		return
	}
	if _, loaded := scheduledPublishedBlockReferenceRepairs.LoadOrStore(repairKey, struct{}{}); loaded {
		return
	}
	schedulePublishedBlockReferenceRepairRunFn(func() {
		defer scheduledPublishedBlockReferenceRepairs.Delete(repairKey)
		sleepFor := RetryBackoff(1)
		if sleepFor > 0 {
			schedulePublishedBlockReferenceRepairSleepFn(sleepFor)
		}
		if err := repair(); err != nil {
			log.Printf("[%s] WARNING: background block-reference repair failed for %s: %v", label, repairKey, err)
			return
		}
		log.Printf("[%s] INFO: background block-reference repair completed for %s", label, repairKey)
	})
}
