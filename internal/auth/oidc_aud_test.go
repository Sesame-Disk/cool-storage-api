package auth

import (
	"strings"
	"testing"
	"time"
)

// L-1 regression: parseIDToken must reject tokens whose `aud` does not list
// the configured ClientID. Previously the audience claim was extracted but
// never compared, so a token minted for a different RP could be replayed
// against SesameFS.
func TestParseIDToken_AudienceMismatch(t *testing.T) {
	srv := jwksTestServer(t)
	defer srv.Close()

	client := newTestOIDCClient(srv.URL)
	client.config.ClientID = "sesamefs-client"
	client.config.ValidateAudience = true

	token := signedJWT(t, map[string]interface{}{
		"iss": srv.URL,
		"sub": "user-1",
		"aud": "some-other-rp",
		"exp": time.Now().Add(1 * time.Hour).Unix(),
		"iat": time.Now().Unix(),
	})

	_, err := client.parseIDToken(token, "")
	if err == nil {
		t.Fatal("parseIDToken() should reject token with wrong audience")
	}
	if !strings.Contains(err.Error(), "audience mismatch") {
		t.Errorf("error should mention 'audience mismatch', got: %v", err)
	}
}

func TestParseIDToken_AudienceMatch(t *testing.T) {
	srv := jwksTestServer(t)
	defer srv.Close()

	client := newTestOIDCClient(srv.URL)
	client.config.ClientID = "sesamefs-client"
	client.config.ValidateAudience = true

	token := signedJWT(t, map[string]interface{}{
		"iss": srv.URL,
		"sub": "user-1",
		"aud": "sesamefs-client",
		"exp": time.Now().Add(1 * time.Hour).Unix(),
		"iat": time.Now().Unix(),
	})

	if _, err := client.parseIDToken(token, ""); err != nil {
		t.Fatalf("parseIDToken() with matching aud should succeed, got: %v", err)
	}
}

func TestParseIDToken_AudienceArrayMatch(t *testing.T) {
	srv := jwksTestServer(t)
	defer srv.Close()

	client := newTestOIDCClient(srv.URL)
	client.config.ClientID = "sesamefs-client"
	client.config.ValidateAudience = true

	token := signedJWT(t, map[string]interface{}{
		"iss": srv.URL,
		"sub": "user-1",
		"aud": []interface{}{"some-other-rp", "sesamefs-client"},
		"exp": time.Now().Add(1 * time.Hour).Unix(),
		"iat": time.Now().Unix(),
	})

	if _, err := client.parseIDToken(token, ""); err != nil {
		t.Fatalf("parseIDToken() with aud array containing client ID should succeed, got: %v", err)
	}
}

func TestParseIDToken_AudienceArrayMismatch(t *testing.T) {
	srv := jwksTestServer(t)
	defer srv.Close()

	client := newTestOIDCClient(srv.URL)
	client.config.ClientID = "sesamefs-client"
	client.config.ValidateAudience = true

	token := signedJWT(t, map[string]interface{}{
		"iss": srv.URL,
		"sub": "user-1",
		"aud": []interface{}{"rp-a", "rp-b"},
		"exp": time.Now().Add(1 * time.Hour).Unix(),
		"iat": time.Now().Unix(),
	})

	_, err := client.parseIDToken(token, "")
	if err == nil {
		t.Fatal("parseIDToken() should reject aud array without client ID")
	}
	if !strings.Contains(err.Error(), "audience mismatch") {
		t.Errorf("error should mention 'audience mismatch', got: %v", err)
	}
}

func TestParseIDToken_MissingAudienceRejectedWhenValidationEnabled(t *testing.T) {
	srv := jwksTestServer(t)
	defer srv.Close()

	client := newTestOIDCClient(srv.URL)
	client.config.ClientID = "sesamefs-client"
	client.config.ValidateAudience = true

	token := signedJWT(t, map[string]interface{}{
		"iss": srv.URL,
		"sub": "user-1",
		"exp": time.Now().Add(1 * time.Hour).Unix(),
		"iat": time.Now().Unix(),
	})

	_, err := client.parseIDToken(token, "")
	if err == nil {
		t.Fatal("parseIDToken() should reject token without aud when audience validation is enabled")
	}
	if !strings.Contains(err.Error(), "audience mismatch") {
		t.Errorf("error should mention 'audience mismatch', got: %v", err)
	}
}

func TestParseIDToken_MissingAudienceAllowedWhenValidationDisabled(t *testing.T) {
	srv := jwksTestServer(t)
	defer srv.Close()

	client := newTestOIDCClient(srv.URL)
	client.config.ClientID = "sesamefs-client"
	client.config.ValidateAudience = false

	token := signedJWT(t, map[string]interface{}{
		"iss": srv.URL,
		"sub": "user-1",
		"exp": time.Now().Add(1 * time.Hour).Unix(),
		"iat": time.Now().Unix(),
	})

	if _, err := client.parseIDToken(token, ""); err != nil {
		t.Fatalf("parseIDToken() should allow missing aud when validation is disabled, got: %v", err)
	}
}
