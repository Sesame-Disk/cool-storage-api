//go:build integration

package integration

import (
	"fmt"
	"net/url"
	"testing"
	"time"
)

func TestAdminAndOrgLinkCursorPagination(t *testing.T) {
	ensureDefaultOrgSupportsGroups(t)

	unique := fmt.Sprintf("inttest-link-cursor-%d", time.Now().UnixNano())
	defaultRepoOne := createTestLibrary(t, adminClient, unique+"-default-1")
	defaultRepoTwo := createTestLibrary(t, adminClient, unique+"-default-2")
	platformRepo := createTestLibrary(t, superadminClient, unique+"-platform")

	defaultShareOne := createShareLinkForTest(t, adminClient, defaultRepoOne, "/")
	defaultShareTwo := createShareLinkForTest(t, adminClient, defaultRepoTwo, "/")
	platformShare := createShareLinkForTest(t, superadminClient, platformRepo, "/")
	defaultUploadOne := createUploadLinkForTest(t, adminClient, defaultRepoOne, "/")
	defaultUploadTwo := createUploadLinkForTest(t, adminClient, defaultRepoTwo, "/")
	platformUpload := createUploadLinkForTest(t, superadminClient, platformRepo, "/")

	t.Run("sysadmin share links paginate by cursor", func(t *testing.T) {
		first := getJSONMap(t, superadminClient.Get(t, "/api/v2.1/admin/share-links/?search="+url.QueryEscape(unique)+"&per_page=1&cursor="))
		firstToken := singleLinkToken(t, first, "share_link_list")
		cursor := requireNextCursor(t, first)
		second := getJSONMap(t, superadminClient.Get(t, "/api/v2.1/admin/share-links/?search="+url.QueryEscape(unique)+"&per_page=1&cursor="+url.QueryEscape(cursor)))
		secondToken := singleLinkToken(t, second, "share_link_list")
		assertDistinctTokens(t, firstToken, secondToken)
		assertContainsOneOfTokens(t, []string{defaultShareOne, defaultShareTwo, platformShare}, firstToken)
		assertContainsOneOfTokens(t, []string{defaultShareOne, defaultShareTwo, platformShare}, secondToken)
	})

	t.Run("org admin share links paginate by cursor", func(t *testing.T) {
		first := getJSONMap(t, adminClient.Get(t, "/api/v2.1/org/admin/links/?search="+url.QueryEscape(unique)+"&per_page=1&cursor="))
		firstToken := singleLinkToken(t, first, "link_list")
		cursor := requireNextCursor(t, first)
		second := getJSONMap(t, adminClient.Get(t, "/api/v2.1/org/admin/links/?search="+url.QueryEscape(unique)+"&per_page=1&cursor="+url.QueryEscape(cursor)))
		secondToken := singleLinkToken(t, second, "link_list")
		assertDistinctTokens(t, firstToken, secondToken)
		assertContainsOneOfTokens(t, []string{defaultShareOne, defaultShareTwo}, firstToken)
		assertContainsOneOfTokens(t, []string{defaultShareOne, defaultShareTwo}, secondToken)
	})

	t.Run("sysadmin upload links paginate by cursor", func(t *testing.T) {
		first := getJSONMap(t, superadminClient.Get(t, "/api/v2.1/admin/upload-links/?search="+url.QueryEscape(unique)+"&per_page=1&cursor="))
		firstToken := singleLinkToken(t, first, "upload_link_list")
		cursor := requireNextCursor(t, first)
		second := getJSONMap(t, superadminClient.Get(t, "/api/v2.1/admin/upload-links/?search="+url.QueryEscape(unique)+"&per_page=1&cursor="+url.QueryEscape(cursor)))
		secondToken := singleLinkToken(t, second, "upload_link_list")
		assertDistinctTokens(t, firstToken, secondToken)
		assertContainsOneOfTokens(t, []string{defaultUploadOne, defaultUploadTwo, platformUpload}, firstToken)
		assertContainsOneOfTokens(t, []string{defaultUploadOne, defaultUploadTwo, platformUpload}, secondToken)
	})

	t.Run("org admin upload links paginate by cursor", func(t *testing.T) {
		first := getJSONMap(t, adminClient.Get(t, "/api/v2.1/org/admin/upload-links/?search="+url.QueryEscape(unique)+"&per_page=1&cursor="))
		firstToken := singleLinkToken(t, first, "upload_link_list")
		cursor := requireNextCursor(t, first)
		second := getJSONMap(t, adminClient.Get(t, "/api/v2.1/org/admin/upload-links/?search="+url.QueryEscape(unique)+"&per_page=1&cursor="+url.QueryEscape(cursor)))
		secondToken := singleLinkToken(t, second, "upload_link_list")
		assertDistinctTokens(t, firstToken, secondToken)
		assertContainsOneOfTokens(t, []string{defaultUploadOne, defaultUploadTwo}, firstToken)
		assertContainsOneOfTokens(t, []string{defaultUploadOne, defaultUploadTwo}, secondToken)
	})
}

func singleLinkToken(t *testing.T, payload map[string]interface{}, key string) string {
	t.Helper()
	entries, ok := payload[key].([]interface{})
	if !ok {
		t.Fatalf("expected %s array in response, got %v", key, payload)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 entry in %s, got %d (%v)", key, len(entries), payload)
	}
	entry, ok := entries[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected map entry in %s, got %T", key, entries[0])
	}
	token, _ := entry["token"].(string)
	if token == "" {
		t.Fatalf("expected token in %s entry, got %v", key, entry)
	}
	return token
}

func requireNextCursor(t *testing.T, payload map[string]interface{}) string {
	t.Helper()
	hasNext, _ := payload["has_next_page"].(bool)
	if !hasNext {
		t.Fatalf("expected has_next_page=true, got %v", payload)
	}
	cursor, _ := payload["next_cursor"].(string)
	if cursor == "" {
		t.Fatalf("expected next_cursor in payload, got %v", payload)
	}
	return cursor
}

func assertDistinctTokens(t *testing.T, first, second string) {
	t.Helper()
	if first == second {
		t.Fatalf("expected distinct tokens across cursor pages, got %q", first)
	}
}

func assertContainsOneOfTokens(t *testing.T, want []string, got string) {
	t.Helper()
	for _, candidate := range want {
		if got == candidate {
			return
		}
	}
	t.Fatalf("token %q was not one of expected values %v", got, want)
}
