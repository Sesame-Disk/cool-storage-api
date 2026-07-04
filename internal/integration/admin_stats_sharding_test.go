//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
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

	seedPlatformStorageCounterDeltasForTest(t, session, storageDeltas)
	seedPlatformTrafficCounterDeltasForTest(t, session, trafficDeltas)

	const adminStatsPollTimeout = 20 * time.Second
	var lastMismatch string
	deadline := time.Now().Add(adminStatsPollTimeout)
	for {
		matched := func() bool {
			expectedStorage := readPlatformStorageSnapshotForTest(t, session)
			expectedMonthTraffic := readPlatformTrafficUsageForMonthsForTest(t, session, []string{today.Format("200601")})
			expectedYearTraffic := readPlatformTrafficUsageForMonthsForTest(t, session, yearMonthKeysForTest(today))
			expectedHistoricalStorage := expectedStorage.BytesUsed - readPlatformStorageDayBytesForTest(t, session, today)
			expectedTrafficByType := readPlatformTrafficByDayForTest(t, session, today)

			sysinfo := getJSONMap(t, superadminClient.Get(t, "/api/v2.1/admin/sysinfo/"))
			if jsonInt64(sysinfo, "total_storage") != expectedStorage.BytesUsed {
				lastMismatch = fmt.Sprintf("sysinfo total_storage=%d want=%d", jsonInt64(sysinfo, "total_storage"), expectedStorage.BytesUsed)
				return false
			}
			if jsonInt64(sysinfo, "total_files_count") != expectedStorage.FileCount {
				lastMismatch = fmt.Sprintf("sysinfo total_files_count=%d want=%d", jsonInt64(sysinfo, "total_files_count"), expectedStorage.FileCount)
				return false
			}
			if jsonInt64(sysinfo, "traffic_month_total") != expectedMonthTraffic.Combined {
				lastMismatch = fmt.Sprintf("sysinfo traffic_month_total=%d want=%d", jsonInt64(sysinfo, "traffic_month_total"), expectedMonthTraffic.Combined)
				return false
			}
			if jsonInt64(sysinfo, "traffic_month_upload") != expectedMonthTraffic.Upload {
				lastMismatch = fmt.Sprintf("sysinfo traffic_month_upload=%d want=%d", jsonInt64(sysinfo, "traffic_month_upload"), expectedMonthTraffic.Upload)
				return false
			}
			if jsonInt64(sysinfo, "traffic_month_download") != expectedMonthTraffic.Download {
				lastMismatch = fmt.Sprintf("sysinfo traffic_month_download=%d want=%d", jsonInt64(sysinfo, "traffic_month_download"), expectedMonthTraffic.Download)
				return false
			}
			if jsonInt64(sysinfo, "traffic_year_total") != expectedYearTraffic.Combined {
				lastMismatch = fmt.Sprintf("sysinfo traffic_year_total=%d want=%d", jsonInt64(sysinfo, "traffic_year_total"), expectedYearTraffic.Combined)
				return false
			}
			if jsonInt64(sysinfo, "traffic_year_upload") != expectedYearTraffic.Upload {
				lastMismatch = fmt.Sprintf("sysinfo traffic_year_upload=%d want=%d", jsonInt64(sysinfo, "traffic_year_upload"), expectedYearTraffic.Upload)
				return false
			}
			if jsonInt64(sysinfo, "traffic_year_download") != expectedYearTraffic.Download {
				lastMismatch = fmt.Sprintf("sysinfo traffic_year_download=%d want=%d", jsonInt64(sysinfo, "traffic_year_download"), expectedYearTraffic.Download)
				return false
			}

			storageRow := adminStatsRowForDate(
				t,
				fmt.Sprintf("/api/v2.1/admin/statistics/total-storage/?start=%s&end=%s", yesterday.Format("2006-01-02"), yesterday.Format("2006-01-02")),
				yesterday,
			)
			if jsonInt64(storageRow, "total_storage") != expectedHistoricalStorage {
				lastMismatch = fmt.Sprintf("storage row total_storage=%d want=%d", jsonInt64(storageRow, "total_storage"), expectedHistoricalStorage)
				return false
			}

			trafficRow := adminStatsRowForDate(
				t,
				fmt.Sprintf("/api/v2.1/admin/statistics/system-traffic/?start=%s&end=%s", today.Format("2006-01-02"), today.Format("2006-01-02")),
				today,
			)
			if jsonInt64(trafficRow, traffic.WebUpload) != expectedTrafficByType[traffic.WebUpload] {
				lastMismatch = fmt.Sprintf("traffic row %s=%d want=%d", traffic.WebUpload, jsonInt64(trafficRow, traffic.WebUpload), expectedTrafficByType[traffic.WebUpload])
				return false
			}
			if jsonInt64(trafficRow, traffic.LinkDownload) != expectedTrafficByType[traffic.LinkDownload] {
				lastMismatch = fmt.Sprintf("traffic row %s=%d want=%d", traffic.LinkDownload, jsonInt64(trafficRow, traffic.LinkDownload), expectedTrafficByType[traffic.LinkDownload])
				return false
			}
			return true
		}()
		if matched {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for admin shard-aware platform stats to reflect seeded counter deltas; last mismatch: %s", lastMismatch)
		}
		time.Sleep(pollInterval)
	}

	t.Logf("verified admin shard fan-out with storage +%d bytes/%d files and traffic +%d upload/+%d download; last mismatch before success: %s", wantStorageBytes, wantStorageFiles, wantUploadBytes, wantDownloadBytes, lastMismatch)
	if wantHistoricalStorageBytes <= 0 || wantTrafficTotal <= 0 {
		t.Fatalf("test setup invalid: wantHistoricalStorageBytes=%d wantTrafficTotal=%d", wantHistoricalStorageBytes, wantTrafficTotal)
	}
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

func seedPlatformTrafficCounterDeltasForTest(t *testing.T, session *gocql.Session, deltas []platformTrafficCounterDeltaForTest) {
	t.Helper()

	for _, delta := range deltas {
		applyPlatformTrafficCounterDeltaForTest(t, session, delta)
		t.Cleanup(func() {
			revert := delta
			revert.bytes = -revert.bytes
			applyPlatformTrafficCounterDeltaForTest(t, session, revert)
		})
	}
}

func applyPlatformTrafficCounterDeltaForTest(t *testing.T, session *gocql.Session, delta platformTrafficCounterDeltaForTest) {
	t.Helper()

	if err := session.Query(`
		UPDATE traffic_counters SET bytes_transferred = bytes_transferred + ?
		WHERE org_id = ? AND month = ? AND shard = ? AND day = ? AND user_id = ? AND traffic_type = ?
	`, delta.bytes, gocql.UUID{}, delta.day.UTC().Format("200601"), delta.shard, delta.day, delta.userID, delta.trafficType).Exec(); err != nil {
		t.Fatalf("failed to update platform traffic counter shard %d type %s day %s: %v", delta.shard, delta.trafficType, delta.day.Format(time.RFC3339), err)
	}
}

func readPlatformStorageSnapshotForTest(t *testing.T, session *gocql.Session) traffic.StorageSnapshot {
	t.Helper()

	var snapshot traffic.StorageSnapshot
	traffic.ForEachCounterShard(func(shard int) {
		var bytesUsed, fileCount int64
		if err := session.Query(
			`SELECT bytes_used, file_count FROM storage_counters WHERE scope = ? AND shard = ? AND day = ?`,
			traffic.PlatformStorageScope(), shard, platformStorageTotalDayForTest,
		).Scan(&bytesUsed, &fileCount); err != nil && !errorsIsNotFoundForTest(err) {
			t.Fatalf("failed to read platform storage snapshot shard %d: %v", shard, err)
		}
		snapshot.BytesUsed += maxInt64ForTest(bytesUsed, 0)
		snapshot.FileCount += maxInt64ForTest(fileCount, 0)
	})
	return snapshot
}

func readPlatformStorageDayBytesForTest(t *testing.T, session *gocql.Session, day time.Time) int64 {
	t.Helper()

	var total int64
	traffic.ForEachCounterShard(func(shard int) {
		var bytesUsed int64
		if err := session.Query(
			`SELECT bytes_used FROM storage_counters WHERE scope = ? AND shard = ? AND day = ?`,
			traffic.PlatformStorageScope(), shard, day,
		).Scan(&bytesUsed); err != nil && !errorsIsNotFoundForTest(err) {
			t.Fatalf("failed to read platform storage day bytes shard %d day %s: %v", shard, day.Format("2006-01-02"), err)
		}
		total += bytesUsed
	})
	return total
}

func readPlatformTrafficUsageForMonthsForTest(t *testing.T, session *gocql.Session, months []string) traffic.MonthlyTransferUsage {
	t.Helper()

	usage := traffic.MonthlyTransferUsage{}
	for _, month := range months {
		traffic.ForEachCounterShard(func(shard int) {
			iter := session.Query(
				`SELECT user_id, traffic_type, bytes_transferred FROM traffic_counters WHERE org_id = ? AND month = ? AND shard = ?`,
				gocql.UUID{}, month, shard,
			).Iter()
			var userUUID gocql.UUID
			var trafficType string
			var bytes int64
			for iter.Scan(&userUUID, &trafficType, &bytes) {
				if userUUID != (gocql.UUID{}) {
					continue
				}
				usage.Combined += bytes
				if strings.HasSuffix(trafficType, "-upload") {
					usage.Upload += bytes
				} else if strings.HasSuffix(trafficType, "-download") {
					usage.Download += bytes
				}
			}
			if err := iter.Close(); err != nil {
				t.Fatalf("failed to read platform traffic usage month %s shard %d: %v", month, shard, err)
			}
		})
	}
	return usage
}

func readPlatformTrafficByDayForTest(t *testing.T, session *gocql.Session, day time.Time) map[string]int64 {
	t.Helper()

	byType := map[string]int64{}
	month := day.UTC().Format("200601")
	traffic.ForEachCounterShard(func(shard int) {
		iter := session.Query(
			`SELECT day, user_id, traffic_type, bytes_transferred FROM traffic_counters WHERE org_id = ? AND month = ? AND shard = ?`,
			gocql.UUID{}, month, shard,
		).Iter()
		var rowDay time.Time
		var userUUID gocql.UUID
		var trafficType string
		var bytes int64
		for iter.Scan(&rowDay, &userUUID, &trafficType, &bytes) {
			if userUUID != (gocql.UUID{}) || !rowDay.UTC().Equal(day.UTC()) {
				continue
			}
			byType[trafficType] += bytes
		}
		if err := iter.Close(); err != nil {
			t.Fatalf("failed to read platform traffic day %s shard %d: %v", day.Format("2006-01-02"), shard, err)
		}
	})
	return byType
}

func yearMonthKeysForTest(now time.Time) []string {
	months := make([]string, 0, int(now.Month()))
	for month := time.January; month <= now.Month(); month++ {
		months = append(months, time.Date(now.Year(), month, 1, 0, 0, 0, 0, time.UTC).Format("200601"))
	}
	sort.Strings(months)
	return months
}

func errorsIsNotFoundForTest(err error) bool {
	return err == nil || err == gocql.ErrNotFound
}

func maxInt64ForTest(value, floor int64) int64 {
	if value < floor {
		return floor
	}
	return value
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
