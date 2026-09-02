//go:build integration

package v2

import "testing"

func TestSetFileFromBlocksPublicationBarriersForTestRestoresAndScopes(t *testing.T) {
	called := 0
	restore := SetFileFromBlocksPublicationBarriersForTest("repo-a", func() { called++ }, nil, nil)
	fileFromBlocksAfterVerifiedBarrier("repo-b")
	if called != 0 {
		t.Fatal("foreign repoID must not run the installed afterVerified barrier")
	}
	fileFromBlocksAfterVerifiedBarrier("repo-a")
	if called != 1 {
		t.Fatal("matching repoID must run the installed afterVerified barrier")
	}
	restore()
	fileFromBlocksAfterVerifiedBarrier("repo-a")
	if called != 1 {
		t.Fatal("afterVerified barrier leaked after restore")
	}
}
