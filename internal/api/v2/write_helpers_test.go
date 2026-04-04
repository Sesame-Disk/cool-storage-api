package v2

import (
	"errors"
	"testing"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/stretchr/testify/assert"
)

type fakeQuotaDB struct{}

func (fakeQuotaDB) Session() *gocql.Session { return nil }

type fakeSessionInvalidator struct {
	called [][2]string
}

func (f *fakeSessionInvalidator) InvalidateUserSessions(orgID, userID string) error {
	f.called = append(f.called, [2]string{orgID, userID})
	return nil
}

func (f *fakeSessionInvalidator) InvalidateAPIKeySessions(apiKeyHash string) error {
	return nil
}

type fakeAPIKeyInvalidator struct {
	called [][2]gocql.UUID
}

func (f *fakeAPIKeyInvalidator) InvalidateUserAPIKeys(orgID, userID gocql.UUID) error {
	f.called = append(f.called, [2]gocql.UUID{orgID, userID})
	return nil
}

func TestValidateUserQuotaAgainstOrg(t *testing.T) {
	tests := []struct {
		name      string
		userValue int64
		orgLimit  int64
		field     string
		want      string
	}{
		{
			name:      "org unlimited allows explicit user quota",
			userValue: 200,
			orgLimit:  -1,
			field:     "storage quota",
			want:      "",
		},
		{
			name:      "user inherit allowed under capped org",
			userValue: 0,
			orgLimit:  100,
			field:     "storage quota",
			want:      "",
		},
		{
			name:      "explicit user quota above org is rejected",
			userValue: 101,
			orgLimit:  100,
			field:     "storage quota",
			want:      "storage quota (101) exceeds organization limit (100)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, validateUserQuotaAgainstOrg(tt.userValue, tt.orgLimit, tt.field))
		})
	}
}

func TestValidateUserTrafficQuotasAgainstOrg(t *testing.T) {
	tests := []struct {
		name          string
		uploadQuota   int64
		downloadQuota int64
		orgQuotas     orgQuotas
		want          string
	}{
		{
			name:          "per direction upload limit enforced",
			uploadQuota:   101,
			downloadQuota: 10,
			orgQuotas: orgQuotas{
				TrafficQuota:       -1,
				TrafficUploadQuota: 100,
			},
			want: "upload quota (101) exceeds organization limit (100)",
		},
		{
			name:          "combined limit enforced on individual quota",
			uploadQuota:   101,
			downloadQuota: -1,
			orgQuotas: orgQuotas{
				TrafficQuota: 100,
			},
			want: "upload quota (101) exceeds organization combined traffic limit (100)",
		},
		{
			name:          "combined limit enforced on sum",
			uploadQuota:   60,
			downloadQuota: 50,
			orgQuotas: orgQuotas{
				TrafficQuota: 100,
			},
			want: "upload + download quota sum (110) exceeds organization combined traffic limit (100)",
		},
		{
			name:          "inherit on one side skips sum validation",
			uploadQuota:   -1,
			downloadQuota: 90,
			orgQuotas: orgQuotas{
				TrafficQuota: 100,
			},
			want: "",
		},
		{
			name:          "valid explicit quotas within all org caps",
			uploadQuota:   40,
			downloadQuota: 50,
			orgQuotas: orgQuotas{
				TrafficQuota:         100,
				TrafficUploadQuota:   80,
				TrafficDownloadQuota: 70,
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, validateUserTrafficQuotasAgainstOrg(tt.uploadQuota, tt.downloadQuota, tt.orgQuotas))
		})
	}
}

func TestReadAndValidateUserQuotaLimits(t *testing.T) {
	originalReader := readOrgQuotasFn
	t.Cleanup(func() {
		readOrgQuotasFn = originalReader
	})

	t.Run("returns internal error when org quota read fails", func(t *testing.T) {
		readOrgQuotasFn = func(db interface{ Session() *gocql.Session }, orgID string) (orgQuotas, error) {
			return orgQuotas{}, errors.New("db unavailable")
		}

		_, err := readAndValidateUserQuotaLimits(fakeQuotaDB{}, "org-1", 10, 20, 30)
		if assert.NotNil(t, err) {
			assert.Equal(t, 500, err.StatusCode)
			assert.Equal(t, "failed to read organization quotas", err.Message)
		}
	})

	t.Run("returns bad request when storage exceeds org limit", func(t *testing.T) {
		readOrgQuotasFn = func(db interface{ Session() *gocql.Session }, orgID string) (orgQuotas, error) {
			return orgQuotas{StorageQuota: 100}, nil
		}

		_, err := readAndValidateUserQuotaLimits(fakeQuotaDB{}, "org-1", 101, 20, 30)
		if assert.NotNil(t, err) {
			assert.Equal(t, 400, err.StatusCode)
			assert.Equal(t, "storage quota (101) exceeds organization limit (100)", err.Message)
		}
	})

	t.Run("returns bad request when traffic sum exceeds combined org limit", func(t *testing.T) {
		readOrgQuotasFn = func(db interface{ Session() *gocql.Session }, orgID string) (orgQuotas, error) {
			return orgQuotas{TrafficQuota: 100}, nil
		}

		_, err := readAndValidateUserQuotaLimits(fakeQuotaDB{}, "org-1", 10, 60, 50)
		if assert.NotNil(t, err) {
			assert.Equal(t, 400, err.StatusCode)
			assert.Equal(t, "upload + download quota sum (110) exceeds organization combined traffic limit (100)", err.Message)
		}
	})

	t.Run("returns org quotas when values are valid", func(t *testing.T) {
		readOrgQuotasFn = func(db interface{ Session() *gocql.Session }, orgID string) (orgQuotas, error) {
			return orgQuotas{
				StorageQuota:         100,
				TrafficQuota:         100,
				TrafficUploadQuota:   70,
				TrafficDownloadQuota: 80,
			}, nil
		}

		oq, err := readAndValidateUserQuotaLimits(fakeQuotaDB{}, "org-1", 50, 40, 50)
		assert.Nil(t, err)
		assert.Equal(t, int64(100), oq.StorageQuota)
		assert.Equal(t, int64(100), oq.TrafficQuota)
		assert.Equal(t, int64(70), oq.TrafficUploadQuota)
		assert.Equal(t, int64(80), oq.TrafficDownloadQuota)
	})
}

func TestInvalidateUserCredentials_InvalidatesSessionsAndAPIKeys(t *testing.T) {
	orgID := gocql.TimeUUID().String()
	userID := gocql.TimeUUID().String()
	sessions := &fakeSessionInvalidator{}
	apiKeys := &fakeAPIKeyInvalidator{}

	invalidateUserCredentials(sessions, apiKeys, orgID, userID)

	assert.Equal(t, [][2]string{{orgID, userID}}, sessions.called)
	if assert.Len(t, apiKeys.called, 1) {
		assert.Equal(t, orgID, apiKeys.called[0][0].String())
		assert.Equal(t, userID, apiKeys.called[0][1].String())
	}
}

func TestInvalidateUserCredentials_InvalidatesAPIKeysWithoutSessionInvalidator(t *testing.T) {
	orgID := gocql.TimeUUID().String()
	userID := gocql.TimeUUID().String()
	apiKeys := &fakeAPIKeyInvalidator{}

	invalidateUserCredentials(nil, apiKeys, orgID, userID)

	if assert.Len(t, apiKeys.called, 1) {
		assert.Equal(t, orgID, apiKeys.called[0][0].String())
		assert.Equal(t, userID, apiKeys.called[0][1].String())
	}
}

func TestInvalidateUserCredentials_SkipsAPIKeyInvalidationForInvalidUUIDs(t *testing.T) {
	sessions := &fakeSessionInvalidator{}
	apiKeys := &fakeAPIKeyInvalidator{}

	invalidateUserCredentials(sessions, apiKeys, "not-a-uuid", "also-not-a-uuid")

	assert.Equal(t, [][2]string{{"not-a-uuid", "also-not-a-uuid"}}, sessions.called)
	assert.Empty(t, apiKeys.called)
}
