//go:build integration

package integration

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

var (
	shareProjectionDBOnce sync.Once
	shareProjectionDB     *dbpkg.DB
	shareProjectionDBErr  error
)

type shareProjectionState struct {
	OrgID         string
	LibraryID     string
	ShareID       string
	SharedBy      string
	SharedByEmail string
	SharedByName  string
	SharedTo      string
	SharedToType  string
	RepoName      string
	Permission    string
	Encrypted     bool
	SizeBytes     int64
	CreatedAt     time.Time
}

type shareReadModelState struct {
	Permission    string
	SharedBy      string
	SharedByEmail string
	SharedByName  string
	RepoName      string
	Encrypted     bool
	SizeBytes     int64
}

type shareCreatorState struct {
	SharedTo     string
	SharedToType string
	Permission   string
}

type shareRecipientState struct {
	SharedBy   string
	Permission string
}

type legacyUserShareState struct {
	SharedToType string
	Permission   string
	SharedBy     string
}

func TestShareProjectionConsistency_UserShareLifecycle(t *testing.T) {
	repoName := fmt.Sprintf("inttest-share-user-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, repoName)

	createResp := adminClient.PutJSON(t, fmt.Sprintf("/api2/repos/%s/dir/shared_items/?p=/", repoID), map[string]interface{}{
		"share_type": "user",
		"username":   []string{defaultUserEmail},
		"permission": "r",
	})
	expectStatus(t, createResp, http.StatusOK)
	createResp.Body.Close()

	var state shareProjectionState
	waitForIntegrationCondition(t, "user share projections after create", func() bool {
		current, ok := singleShareStateByType(t, repoID, "user")
		if !ok {
			return false
		}
		if !baseShareMatchesExpectation(current, repoName, "r", "user") {
			return false
		}
		if !userShareMatchesAllTables(t, current) {
			return false
		}
		state = current
		return true
	})

	updateResp := adminClient.PostJSON(t,
		fmt.Sprintf("/api2/repos/%s/dir/shared_items/?p=/&share_type=user&username=%s", repoID, url.QueryEscape(defaultUserEmail)),
		map[string]string{"permission": "rw"},
	)
	expectStatus(t, updateResp, http.StatusOK)
	updateResp.Body.Close()

	waitForIntegrationCondition(t, "user share projections after permission update", func() bool {
		current, ok := singleShareStateByType(t, repoID, "user")
		if !ok {
			return false
		}
		if !baseShareMatchesExpectation(current, repoName, "rw", "user") {
			return false
		}
		state = current
		return userShareMatchesAllTables(t, current)
	})

	deleteResp := adminClient.Delete(t,
		fmt.Sprintf("/api2/repos/%s/dir/shared_items/?p=/&share_type=user&username=%s", repoID, url.QueryEscape(defaultUserEmail)),
	)
	expectStatus(t, deleteResp, http.StatusOK)
	deleteResp.Body.Close()

	waitForIntegrationCondition(t, "user share projections after delete", func() bool {
		_, ok := singleShareStateByType(t, repoID, "user")
		if ok {
			return false
		}
		return shareAbsentFromAllTables(t, state)
	})
}

func TestShareProjectionConsistency_GroupShareLifecycle(t *testing.T) {
	groupName := fmt.Sprintf("inttest-share-group-%d", time.Now().UnixNano())
	groupID := createGroupForRegressionTest(t, adminClient, groupName)
	repoName := fmt.Sprintf("inttest-share-group-lib-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, repoName)

	createResp := adminClient.PutJSON(t, fmt.Sprintf("/api2/repos/%s/dir/shared_items/?p=/", repoID), map[string]interface{}{
		"share_type": "group",
		"group_id":   []string{groupID},
		"permission": "r",
	})
	expectStatus(t, createResp, http.StatusOK)
	createResp.Body.Close()

	var state shareProjectionState
	waitForIntegrationCondition(t, "group share projections after create", func() bool {
		current, ok := singleShareStateByType(t, repoID, "group")
		if !ok {
			return false
		}
		if !baseShareMatchesExpectation(current, repoName, "r", "group") || current.SharedTo != groupID {
			return false
		}
		if !groupShareMatchesAllTables(t, current) {
			return false
		}
		state = current
		return true
	})

	updateResp := adminClient.PostJSON(t,
		fmt.Sprintf("/api2/repos/%s/dir/shared_items/?p=/&share_type=group&group_id=%s", repoID, url.QueryEscape(groupID)),
		map[string]string{"permission": "rw"},
	)
	expectStatus(t, updateResp, http.StatusOK)
	updateResp.Body.Close()

	waitForIntegrationCondition(t, "group share projections after permission update", func() bool {
		current, ok := singleShareStateByType(t, repoID, "group")
		if !ok {
			return false
		}
		if !baseShareMatchesExpectation(current, repoName, "rw", "group") || current.SharedTo != groupID {
			return false
		}
		state = current
		return groupShareMatchesAllTables(t, current)
	})

	deleteResp := adminClient.Delete(t,
		fmt.Sprintf("/api2/repos/%s/dir/shared_items/?p=/&share_type=group&group_id=%s", repoID, url.QueryEscape(groupID)),
	)
	expectStatus(t, deleteResp, http.StatusOK)
	deleteResp.Body.Close()

	waitForIntegrationCondition(t, "group share projections after delete", func() bool {
		_, ok := singleShareStateByType(t, repoID, "group")
		if ok {
			return false
		}
		return shareAbsentFromAllTables(t, state)
	})
}

func singleShareStateByType(t *testing.T, repoID, shareType string) (shareProjectionState, bool) {
	t.Helper()

	iter := shareProjectionDBForTest(t).Session().Query(`
		SELECT share_id, org_id, shared_by, shared_by_email, shared_by_name,
		       shared_to, shared_to_type, repo_name, encrypted, size_bytes,
		       permission, created_at
		FROM shares WHERE library_id = ?
	`, repoID).Iter()

	var found shareProjectionState
	var row shareProjectionState
	count := 0
	for iter.Scan(
		&row.ShareID,
		&row.OrgID,
		&row.SharedBy,
		&row.SharedByEmail,
		&row.SharedByName,
		&row.SharedTo,
		&row.SharedToType,
		&row.RepoName,
		&row.Encrypted,
		&row.SizeBytes,
		&row.Permission,
		&row.CreatedAt,
	) {
		if row.SharedToType != shareType {
			row = shareProjectionState{}
			continue
		}
		row.LibraryID = repoID
		found = row
		count++
		row = shareProjectionState{}
	}
	if err := iter.Close(); err != nil {
		t.Fatalf("list shares for repo %s failed: %v", repoID, err)
	}
	if count == 0 {
		return shareProjectionState{}, false
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 %s share for repo %s, found %d", shareType, repoID, count)
	}
	return found, true
}

func baseShareMatchesExpectation(state shareProjectionState, repoName, permission, shareType string) bool {
	return state.OrgID == defaultOrgID &&
		state.Permission == permission &&
		state.SharedToType == shareType &&
		state.RepoName == repoName &&
		state.SharedByEmail == defaultAdminEmail &&
		state.ShareID != "" &&
		state.SharedBy != "" &&
		state.SharedTo != ""
}

func userShareMatchesAllTables(t *testing.T, state shareProjectionState) bool {
	t.Helper()

	legacy, ok := legacyUserShareRow(t, state)
	if !ok || legacy.SharedToType != "user" || legacy.Permission != state.Permission || legacy.SharedBy != state.SharedBy {
		return false
	}

	if _, ok := shareByGroupRow(t, state); ok {
		return false
	}

	userOrg, ok := shareByUserOrgRow(t, state)
	if !ok || !readModelMatchesBase(userOrg, state) {
		return false
	}

	creator, ok := shareByCreatorRow(t, state)
	if !ok || creator.SharedTo != state.SharedTo || creator.SharedToType != state.SharedToType || creator.Permission != state.Permission {
		return false
	}

	recipient, ok := shareByRecipientRow(t, state)
	if !ok || recipient.SharedBy != state.SharedBy || recipient.Permission != state.Permission {
		return false
	}

	return true
}

func groupShareMatchesAllTables(t *testing.T, state shareProjectionState) bool {
	t.Helper()

	if _, ok := legacyUserShareRow(t, state); ok {
		return false
	}
	if _, ok := shareByUserOrgRow(t, state); ok {
		return false
	}

	groupRow, ok := shareByGroupRow(t, state)
	if !ok || !readModelMatchesBase(groupRow, state) {
		return false
	}

	creator, ok := shareByCreatorRow(t, state)
	if !ok || creator.SharedTo != state.SharedTo || creator.SharedToType != state.SharedToType || creator.Permission != state.Permission {
		return false
	}

	recipient, ok := shareByRecipientRow(t, state)
	if !ok || recipient.SharedBy != state.SharedBy || recipient.Permission != state.Permission {
		return false
	}

	return true
}

func shareAbsentFromAllTables(t *testing.T, state shareProjectionState) bool {
	t.Helper()

	if shareBaseRowExists(t, state) {
		return false
	}
	if _, ok := legacyUserShareRow(t, state); ok {
		return false
	}
	if _, ok := shareByGroupRow(t, state); ok {
		return false
	}
	if _, ok := shareByUserOrgRow(t, state); ok {
		return false
	}
	if _, ok := shareByCreatorRow(t, state); ok {
		return false
	}
	if _, ok := shareByRecipientRow(t, state); ok {
		return false
	}
	return true
}

func readModelMatchesBase(row shareReadModelState, state shareProjectionState) bool {
	return row.Permission == state.Permission &&
		row.SharedBy == state.SharedBy &&
		row.SharedByEmail == state.SharedByEmail &&
		row.SharedByName == state.SharedByName &&
		row.RepoName == state.RepoName &&
		row.Encrypted == state.Encrypted &&
		row.SizeBytes == state.SizeBytes
}

func shareBaseRowExists(t *testing.T, state shareProjectionState) bool {
	t.Helper()

	var shareID string
	err := shareProjectionDBForTest(t).Session().Query(`
		SELECT share_id FROM shares WHERE library_id = ? AND share_id = ?
	`, state.LibraryID, state.ShareID).Scan(&shareID)
	return scanFound(t, err, "shares", state.LibraryID, state.ShareID)
}

func legacyUserShareRow(t *testing.T, state shareProjectionState) (legacyUserShareState, bool) {
	t.Helper()

	var row legacyUserShareState
	err := shareProjectionDBForTest(t).Session().Query(`
		SELECT shared_to_type, permission, shared_by
		FROM shares_by_user WHERE shared_to = ? AND library_id = ?
	`, state.SharedTo, state.LibraryID).Scan(&row.SharedToType, &row.Permission, &row.SharedBy)
	return row, scanFound(t, err, "shares_by_user", state.SharedTo, state.LibraryID)
}

func shareByGroupRow(t *testing.T, state shareProjectionState) (shareReadModelState, bool) {
	t.Helper()

	var row shareReadModelState
	err := shareProjectionDBForTest(t).Session().Query(`
		SELECT permission, shared_by, shared_by_email, shared_by_name, repo_name, encrypted, size_bytes
		FROM shares_by_group
		WHERE org_id = ? AND group_id = ? AND created_at = ? AND library_id = ? AND share_id = ?
	`, state.OrgID, state.SharedTo, state.CreatedAt, state.LibraryID, state.ShareID).Scan(
		&row.Permission,
		&row.SharedBy,
		&row.SharedByEmail,
		&row.SharedByName,
		&row.RepoName,
		&row.Encrypted,
		&row.SizeBytes,
	)
	return row, scanFound(t, err, "shares_by_group", state.OrgID, state.SharedTo, state.ShareID)
}

func shareByUserOrgRow(t *testing.T, state shareProjectionState) (shareReadModelState, bool) {
	t.Helper()

	var row shareReadModelState
	err := shareProjectionDBForTest(t).Session().Query(`
		SELECT permission, shared_by, shared_by_email, shared_by_name, repo_name, encrypted, size_bytes
		FROM shares_by_user_org
		WHERE org_id = ? AND user_id = ? AND created_at = ? AND library_id = ? AND share_id = ?
	`, state.OrgID, state.SharedTo, state.CreatedAt, state.LibraryID, state.ShareID).Scan(
		&row.Permission,
		&row.SharedBy,
		&row.SharedByEmail,
		&row.SharedByName,
		&row.RepoName,
		&row.Encrypted,
		&row.SizeBytes,
	)
	return row, scanFound(t, err, "shares_by_user_org", state.OrgID, state.SharedTo, state.ShareID)
}

func shareByCreatorRow(t *testing.T, state shareProjectionState) (shareCreatorState, bool) {
	t.Helper()

	var row shareCreatorState
	err := shareProjectionDBForTest(t).Session().Query(`
		SELECT shared_to, shared_to_type, permission
		FROM shares_by_creator
		WHERE org_id = ? AND shared_by = ? AND created_at = ? AND library_id = ? AND share_id = ?
	`, state.OrgID, state.SharedBy, state.CreatedAt, state.LibraryID, state.ShareID).Scan(
		&row.SharedTo,
		&row.SharedToType,
		&row.Permission,
	)
	return row, scanFound(t, err, "shares_by_creator", state.OrgID, state.SharedBy, state.ShareID)
}

func shareByRecipientRow(t *testing.T, state shareProjectionState) (shareRecipientState, bool) {
	t.Helper()

	var row shareRecipientState
	err := shareProjectionDBForTest(t).Session().Query(`
		SELECT shared_by, permission
		FROM shares_by_recipient
		WHERE org_id = ? AND shared_to_type = ? AND shared_to = ? AND created_at = ? AND library_id = ? AND share_id = ?
	`, state.OrgID, state.SharedToType, state.SharedTo, state.CreatedAt, state.LibraryID, state.ShareID).Scan(
		&row.SharedBy,
		&row.Permission,
	)
	return row, scanFound(t, err, "shares_by_recipient", state.OrgID, state.SharedTo, state.ShareID)
}

func scanFound(t *testing.T, err error, table string, keyParts ...string) bool {
	t.Helper()

	if err == nil {
		return true
	}
	if errors.Is(err, gocql.ErrNotFound) {
		return false
	}
	t.Fatalf("query %s for %s failed: %v", table, strings.Join(keyParts, "/"), err)
	return false
}

func shareProjectionDBForTest(t *testing.T) *dbpkg.DB {
	t.Helper()

	shareProjectionDBOnce.Do(func() {
		cfg := config.DatabaseConfig{
			Hosts:       splitEnvOrDefault("CASSANDRA_HOSTS", "cassandra:9042"),
			Keyspace:    envOrDefault("CASSANDRA_KEYSPACE", "sesamefs"),
			Consistency: envOrDefault("CASSANDRA_CONSISTENCY", "LOCAL_QUORUM"),
			LocalDC:     envOrDefault("CASSANDRA_LOCAL_DC", "datacenter1"),
			Username:    os.Getenv("CASSANDRA_USERNAME"),
			Password:    os.Getenv("CASSANDRA_PASSWORD"),
		}
		shareProjectionDB, shareProjectionDBErr = dbpkg.New(cfg)
	})
	if shareProjectionDBErr != nil {
		t.Fatalf("failed to connect to Cassandra for integration share assertions: %v", shareProjectionDBErr)
	}
	return shareProjectionDB
}

func splitEnvOrDefault(envKey, fallback string) []string {
	value := envOrDefault(envKey, fallback)
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	if len(result) == 0 {
		return []string{fallback}
	}
	return result
}

func envOrDefault(envKey, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(envKey)); value != "" {
		return value
	}
	return fallback
}
