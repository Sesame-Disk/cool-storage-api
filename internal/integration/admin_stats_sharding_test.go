//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

var platformStorageTotalDayForTest = time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)

type platformStorageCounterDeltaForTest struct {
	shard     int
	day       time.Time
	bytesUsed int64
	fileCount int64
}

type platformTrafficCounterDeltaForTest struct {
	shard       int
	day         time.Time
	userID      gocql.UUID
	trafficType string
	bytes       int64
}

func TestAdminStatsFanOutAcrossPlatformShards(t *testing.T) {
	session := shareProjectionDBForTest(t).Session()
	today := time.Now().UTC().Truncate(24 * time.Hour)
	yesterday := today.AddDate(0, 0, -1)
	currentMonth := today.Format("200601")

	const (
		storageShardA = 3
		storageShardB = 11
		trafficShardA = 5
		trafficShardB = 17
	)

	todayStorageDeltaBytesA := int64(321_000)
	todayStorageDeltaBytesB := int64(765_000)
	todayStorageDeltaFilesA := int64(1)
	todayStorageDeltaFilesB := int64(2)

	storageDeltas := []platformStorageCounterDeltaForTest{
		{shard: storageShardA, day: platformStorageTotalDayForTest, bytesUsed: 4_321_000, fileCount: 2},
		{shard: storageShardB, day: platformStorageTotalDayForTest, bytesUsed: 8_765_000, fileCount: 5},
		// Seed today's sharded daily deltas as well. ReconstructStorageHistory
		// walks backward from the running total, so yesterday's row should rise by
		// (running total - today's delta), proving the historical path fans out
		// across shards instead of reading only the sentinel total row.
		{shard: storageShardA, day: today, bytesUsed: todayStorageDeltaBytesA, fileCount: todayStorageDeltaFilesA},
		{shard: storageShardB, day: today, bytesUsed: todayStorageDeltaBytesB, fileCount: todayStorageDeltaFilesB},
	}
	trafficDeltas := []platformTrafficCounterDeltaForTest{
		{shard: trafficShardA, day: today, userID: gocql.UUID{}, trafficType: traffic.WebUpload, bytes: 111_000},
		{shard: trafficShardB, day: today, userID: gocql.UUID{}, trafficType: traffic.WebUpload, bytes: 222_000},
		{shard: trafficShardA, day: today, userID: gocql.UUID{}, trafficType: traffic.LinkDownload, bytes: 333_000},
		{shard: trafficShardB, day: today, userID: gocql.UUID{}, trafficType: traffic.LinkDownload, bytes: 444_000},
	}

	wantStorageBytes := int64(0)
	wantStorageFiles := int64(0)
	for _, delta := range storageDeltas {
		if delta.day == platformStorageTotalDayForTest {
			wantStorageBytes += delta.bytesUsed
			wantStorageFiles += delta.fileCount
		}
	}
	wantHistoricalStorageBytes := wantStorageBytes - (todayStorageDeltaBytesA + todayStorageDeltaBytesB)

	wantUploadBytes := int64(111_000 + 222_000)
	wantDownloadBytes := int64(333_000 + 444_000)
	wantTrafficTotal := wantUploadBytes + wantDownloadBytes

	baselineSysinfo := getJSONMap(t, superadminClient.Get(t, "/api/v2.1/admin/sysinfo/"))
	baselineStorageRow := adminStatsRowForDate(
		t,
		fmt.Sprintf("/api/v2.1/admin/statistics/total-storage/?start=%s&end=%s", yesterday.Format("2006-01-02"), yesterday.Format("2006-01-02")),
		yesterday,
	)
	baselineTrafficRow := adminStatsRowForDate(
		t,
		fmt.Sprintf("/api/v2.1/admin/statistics/system-traffic/?start=%s&end=%s", today.Format("2006-01-02"), today.Format("2006-01-02")),
		today,
	)

	seedPlatformStorageCounterDeltasForTest(t, session, storageDeltas)
	seedPlatformTrafficCounterDeltasForTest(t, session, currentMonth, trafficDeltas)

	waitForCondition(t, "admin shard-aware platform stats to reflect seeded counter deltas", func() bool {
		sysinfo := getJSONMap(t, superadminClient.Get(t, "/api/v2.1/admin/sysinfo/"))
		if jsonInt64(sysinfo, "total_storage") != jsonInt64(baselineSysinfo, "total_storage")+wantStorageBytes {
			return false
		}
		if jsonInt64(sysinfo, "total_files_count") != jsonInt64(baselineSysinfo, "total_files_count")+wantStorageFiles {
			return false
		}
		if jsonInt64(sysinfo, "traffic_month_total") != jsonInt64(baselineSysinfo, "traffic_month_total")+wantTrafficTotal {
			return false
		}
		if jsonInt64(sysinfo, "traffic_month_upload") != jsonInt64(baselineSysinfo, "traffic_month_upload")+wantUploadBytes {
			return false
		}
		if jsonInt64(sysinfo, "traffic_month_download") != jsonInt64(baselineSysinfo, "traffic_month_download")+wantDownloadBytes {
			return false
		}
		if jsonInt64(sysinfo, "traffic_year_total") != jsonInt64(baselineSysinfo, "traffic_year_total")+wantTrafficTotal {
			return false
		}
		if jsonInt64(sysinfo, "traffic_year_upload") != jsonInt64(baselineSysinfo, "traffic_year_upload")+wantUploadBytes {
			return false
		}
		if jsonInt64(sysinfo, "traffic_year_download") != jsonInt64(baselineSysinfo, "traffic_year_download")+wantDownloadBytes {
			return false
		}

		storageRow := adminStatsRowForDate(
			t,
			fmt.Sprintf("/api/v2.1/admin/statistics/total-storage/?start=%s&end=%s", yesterday.Format("2006-01-02"), yesterday.Format("2006-01-02")),
			yesterday,
		)
		if jsonInt64(storageRow, "total_storage") != jsonInt64(baselineStorageRow, "total_storage")+wantHistoricalStorageBytes {
			return false
		}

		trafficRow := adminStatsRowForDate(
			t,
			fmt.Sprintf("/api/v2.1/admin/statistics/system-traffic/?start=%s&end=%s", today.Format("2006-01-02"), today.Format("2006-01-02")),
			today,
		)
		if jsonInt64(trafficRow, traffic.WebUpload) != jsonInt64(baselineTrafficRow, traffic.WebUpload)+wantUploadBytes {
			return false
		}
		if jsonInt64(trafficRow, traffic.LinkDownload) != jsonInt64(baselineTrafficRow, traffic.LinkDownload)+wantDownloadBytes {
			return false
		}

		return true
	})
}

func seedPlatformStorageCounterDeltasForTest(t *testing.T, session *gocql.Session, deltas []platformStorageCounterDeltaForTest) {
	t.Helper()

	for _, delta := range deltas {
		applyPlatformStorageCounterDeltaForTest(t, session, delta)
		t.Cleanup(func() {
			revert := delta
			revert.bytesUsed = -revert.bytesUsed
			revert.fileCount = -revert.fileCount
			applyPlatformStorageCounterDeltaForTest(t, session, revert)
		})
	}
}

func applyPlatformStorageCounterDeltaForTest(t *testing.T, session *gocql.Session, delta platformStorageCounterDeltaForTest) {
	t.Helper()

	if err := session.Query(`
		UPDATE storage_counters SET bytes_used = bytes_used + ?, file_count = file_count + ?
		WHERE scope = ? AND shard = ? AND day = ?
	`, delta.bytesUsed, delta.fileCount, traffic.PlatformStorageScope(), delta.shard, delta.day).Exec(); err != nil {
		t.Fatalf("failed to update platform storage counter shard %d day %s: %v", delta.shard, delta.day.Format(time.RFC3339), err)
	}
}

func seedPlatformTrafficCounterDeltasForTest(t *testing.T, session *gocql.Session, month string, deltas []platformTrafficCounterDeltaForTest) {
	t.Helper()

	for _, delta := range deltas {
		applyPlatformTrafficCounterDeltaForTest(t, session, month, delta)
		t.Cleanup(func() {
			revert := delta
			revert.bytes = -revert.bytes
			applyPlatformTrafficCounterDeltaForTest(t, session, month, revert)
		})
	}
}

func applyPlatformTrafficCounterDeltaForTest(t *testing.T, session *gocql.Session, month string, delta platformTrafficCounterDeltaForTest) {
	t.Helper()

	if err := session.Query(`
		UPDATE traffic_counters SET bytes_transferred = bytes_transferred + ?
		WHERE org_id = ? AND month = ? AND shard = ? AND day = ? AND user_id = ? AND traffic_type = ?
	`, delta.bytes, gocql.UUID{}, month, delta.shard, delta.day, delta.userID, delta.trafficType).Exec(); err != nil {
		t.Fatalf("failed to update platform traffic counter shard %d type %s day %s: %v", delta.shard, delta.trafficType, delta.day.Format(time.RFC3339), err)
	}
}

func adminStatsRowForDate(t *testing.T, path string, day time.Time) map[string]interface{} {
	t.Helper()

	rows := getJSONRows(t, superadminClient.Get(t, path))
	wantDatetime := day.UTC().Format("2006-01-02T00:00:00+00:00")
	for _, row := range rows {
		if got, _ := row["datetime"].(string); got == wantDatetime {
			return row
		}
	}
	t.Fatalf("expected stats row for %s in %s, got %v", wantDatetime, path, rows)
	return nil
}

func getJSONRows(t *testing.T, resp *http.Response) []map[string]interface{} {
	t.Helper()

	expectStatus(t, resp, http.StatusOK)
	var rows []map[string]interface{}
	decodeJSON(t, resp, &rows)
	return rows
}
