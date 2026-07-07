//go:build integration

package integration

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/google/uuid"
)

// Regression coverage for the "block id mapping conflict" (409) bug: the forward
// SHA-1 -> SHA-256 mapping used to be keyed globally per org, so the same plaintext
// SHA-1 could map to only ONE internal. Uploading a file to an encrypted library
// (external=SHA1(plaintext) -> internal=SHA256(ciphertext)) then the same content to
// a normal library (external=SHA1(plaintext) -> internal=SHA256(plaintext)) tripped
// the verified read-before-write guard and returned 409. Migration 009 scopes the
// mapping by block-representation domain (plain:v1 vs library:<id>) so both coexist.

func mcSHA1(b []byte) string { s := sha1.Sum(b); return hex.EncodeToString(s[:]) }
func mcSHA256(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// TestBlockMappingRepresentationScoped_NoCrossDomainConflict is the DB-level
// reproduction: the same external SHA-1 mapping to different internals in two
// different representation domains must NOT conflict, while a genuine same-domain
// external->different-internal collision still fails closed.
func TestBlockMappingRepresentationScoped_NoCrossDomainConflict(t *testing.T) {
	requireCassandra(t)
	database := shareProjectionDBForTest(t)

	orgID := uuid.NewString()
	encLibraryID := uuid.NewString()
	plainRep := dbpkg.PlainBlockRepresentationID
	encRep := dbpkg.EncryptedLibraryBlockRepresentationID(encLibraryID)

	content := []byte(fmt.Sprintf("shared-plaintext-block-%d", time.Now().UnixNano()))
	externalSHA1 := mcSHA1(content)
	internalPlain := mcSHA256(content)
	internalCipher := mcSHA256(append(content, []byte("-ciphertext")...))

	t.Cleanup(func() {
		_ = database.Session().Query(`DELETE FROM block_id_mappings WHERE org_id = ? AND representation_id = ? AND external_id = ?`, orgID, plainRep, externalSHA1).Exec()
		_ = database.Session().Query(`DELETE FROM block_id_mappings WHERE org_id = ? AND representation_id = ? AND external_id = ?`, orgID, encRep, externalSHA1).Exec()
	})

	// Encrypted-library domain: legacy seafhttp path (plain WriteBlockIDMapping).
	if err := database.WriteBlockIDMapping(orgID, encRep, externalSHA1, internalCipher, time.Now().UTC()); err != nil {
		t.Fatalf("seed encrypted-domain mapping: %v", err)
	}
	// Plaintext domain, verified web writer: before migration 009 this returned
	// ErrBlockIDMappingConflict; now it must succeed — different representation.
	if err := database.WriteVerifiedWebBlockMapping(orgID, plainRep, externalSHA1, internalPlain, time.Now().UTC()); err != nil {
		t.Fatalf("plaintext-domain web mapping must not conflict across representations, got: %v", err)
	}

	if got, ok, err := database.GetBlockIDMapping(orgID, encRep, externalSHA1); err != nil || !ok || got != internalCipher {
		t.Fatalf("encrypted-domain resolve = (%q, %v, %v), want (%q, true, nil)", got, ok, err, internalCipher)
	}
	if got, ok, err := database.GetBlockIDMapping(orgID, plainRep, externalSHA1); err != nil || !ok || got != internalPlain {
		t.Fatalf("plaintext-domain resolve = (%q, %v, %v), want (%q, true, nil)", got, ok, err, internalPlain)
	}

	// Idempotent re-write with the SAME internal is a no-op, not a conflict.
	if err := database.WriteVerifiedWebBlockMapping(orgID, plainRep, externalSHA1, internalPlain, time.Now().UTC()); err != nil {
		t.Fatalf("idempotent re-write must not conflict, got: %v", err)
	}
	// The legacy writer must reject the same-domain remap too.
	tamperedCipher := mcSHA256(append(content, []byte("-ciphertext-tampered")...))
	if err := database.WriteBlockIDMapping(orgID, encRep, externalSHA1, tamperedCipher, time.Now().UTC()); !errors.Is(err, dbpkg.ErrBlockIDMappingConflict) {
		t.Fatalf("legacy same-domain external->different-internal must conflict, got: %v", err)
	}
	// The integrity guard still fires on a genuine SAME-domain collision.
	tampered := mcSHA256(append(content, []byte("-tampered")...))
	if err := database.WriteVerifiedWebBlockMapping(orgID, plainRep, externalSHA1, tampered, time.Now().UTC()); !errors.Is(err, dbpkg.ErrBlockIDMappingConflict) {
		t.Fatalf("same-domain external->different-internal must conflict, got: %v", err)
	}
}

// TestBlockMappingConflict_EncryptedThenNormal_E2E is the end-to-end reproduction of
// the user-reported scenario: upload a file to an encrypted library, then the SAME
// content to a normal library. Before migration 009 the second upload failed with
// 409 {"error":"block id mapping conflict"}; now both succeed and download intact.
func TestBlockMappingConflict_EncryptedThenNormal_E2E(t *testing.T) {
	content := fmt.Sprintf("shared content across encrypted and normal libs %d\n", time.Now().UnixNano())

	encName := fmt.Sprintf("inttest-bmc-enc-%d", time.Now().UnixNano())
	password := "test-password-123"
	encRepo := createLibraryWithBody(t, adminClient, encName, map[string]interface{}{
		"repo_name": encName,
		"encrypted": true,
		"passwd":    password,
	}, true)
	setPassResp := adminClient.PostForm(t, fmt.Sprintf("/api/v2.1/repos/%s/set-password/", encRepo), url.Values{"password": {password}})
	expectStatus(t, setPassResp, http.StatusOK)
	setPassResp.Body.Close()

	encUploadURL := getUploadURL(t, adminClient, encRepo)
	uploadFileThroughLink(t, adminClient, encUploadURL, "shared.txt", "/", content)

	normalName := fmt.Sprintf("inttest-bmc-normal-%d", time.Now().UnixNano())
	normalRepo := createTestLibrary(t, adminClient, normalName)

	resp := uploadFileViaBlocksFlow(t, adminClient, normalRepo, "/", "shared.txt", [][]byte{[]byte(content)}, false)
	if resp.StatusCode == http.StatusConflict {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("web block upload to normal library returned 409 (the pre-009 bug): %s", body)
	}
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	if got := downloadRepoFile(t, adminClient, normalRepo, "/shared.txt"); string(got) != content {
		t.Fatalf("normal library download mismatch:\n  got:  %q\n  want: %q", string(got), content)
	}
	if got := downloadRepoFile(t, adminClient, encRepo, "/shared.txt"); string(got) != content {
		t.Fatalf("encrypted library download mismatch:\n  got:  %q\n  want: %q", string(got), content)
	}
}

func TestBlockMappingConflict_NormalThenEncrypted_E2E(t *testing.T) {
	content := fmt.Sprintf("shared content across normal and encrypted libs %d\n", time.Now().UnixNano())

	normalName := fmt.Sprintf("inttest-bmc-normal-first-%d", time.Now().UnixNano())
	normalRepo := createTestLibrary(t, adminClient, normalName)
	resp := uploadFileViaBlocksFlow(t, adminClient, normalRepo, "/", "shared.txt", [][]byte{[]byte(content)}, false)
	if resp.StatusCode == http.StatusConflict {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("web block upload to normal library returned 409: %s", body)
	}
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	encName := fmt.Sprintf("inttest-bmc-enc-second-%d", time.Now().UnixNano())
	password := "test-password-123"
	encRepo := createLibraryWithBody(t, adminClient, encName, map[string]interface{}{
		"repo_name": encName,
		"encrypted": true,
		"passwd":    password,
	}, true)
	setPassResp := adminClient.PostForm(t, fmt.Sprintf("/api/v2.1/repos/%s/set-password/", encRepo), url.Values{"password": {password}})
	expectStatus(t, setPassResp, http.StatusOK)
	setPassResp.Body.Close()

	encUploadURL := getUploadURL(t, adminClient, encRepo)
	uploadFileThroughLink(t, adminClient, encUploadURL, "shared.txt", "/", content)

	if got := downloadRepoFile(t, adminClient, normalRepo, "/shared.txt"); string(got) != content {
		t.Fatalf("normal library download mismatch:\n  got:  %q\n  want: %q", string(got), content)
	}
	if got := downloadRepoFile(t, adminClient, encRepo, "/shared.txt"); string(got) != content {
		t.Fatalf("encrypted library download mismatch:\n  got:  %q\n  want: %q", string(got), content)
	}
}

func TestBlockMappingConflict_EncryptedThenNormal_Multiblock_E2E(t *testing.T) {
	// The web block flow (validateManifest) requires every non-final block to be
	// exactly the CAS block size (v2.WebUploadBlockSize = 8 MiB); only the final
	// block may be shorter. Build a real 2-block file: one full 8 MiB block plus a
	// small tail. seafhttp chunks the encrypted upload on the same 8 MiB boundary,
	// so both libraries store the same 2-block decomposition of identical content.
	const webBlockSize = 8 * 1024 * 1024
	blocks := [][]byte{
		[]byte(strings.Repeat("A", webBlockSize)),
		[]byte(fmt.Sprintf("multiblock-tail-%d\n", time.Now().UnixNano())),
	}
	var builder strings.Builder
	for _, block := range blocks {
		builder.Write(block)
	}
	content := builder.String()

	encName := fmt.Sprintf("inttest-bmc-enc-multiblock-%d", time.Now().UnixNano())
	password := "test-password-123"
	encRepo := createLibraryWithBody(t, adminClient, encName, map[string]interface{}{
		"repo_name": encName,
		"encrypted": true,
		"passwd":    password,
	}, true)
	setPassResp := adminClient.PostForm(t, fmt.Sprintf("/api/v2.1/repos/%s/set-password/", encRepo), url.Values{"password": {password}})
	expectStatus(t, setPassResp, http.StatusOK)
	setPassResp.Body.Close()

	encUploadURL := getUploadURL(t, adminClient, encRepo)
	uploadFileThroughLink(t, adminClient, encUploadURL, "shared-multi.txt", "/", content)

	normalName := fmt.Sprintf("inttest-bmc-normal-multiblock-%d", time.Now().UnixNano())
	normalRepo := createTestLibrary(t, adminClient, normalName)
	resp := uploadFileViaBlocksFlow(t, adminClient, normalRepo, "/", "shared-multi.txt", blocks, false)
	if resp.StatusCode == http.StatusConflict {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("web block upload multiblock to normal library returned 409: %s", body)
	}
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	if got := downloadRepoFile(t, adminClient, normalRepo, "/shared-multi.txt"); string(got) != content {
		t.Fatalf("normal multiblock download mismatch: got %d bytes, want %d", len(got), len(content))
	}
	if got := downloadRepoFile(t, adminClient, encRepo, "/shared-multi.txt"); string(got) != content {
		t.Fatalf("encrypted multiblock download mismatch: got %d bytes, want %d", len(got), len(content))
	}
}
