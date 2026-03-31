package v2

import "testing"

func TestBuildPublicLinkPasswordCookie(t *testing.T) {
	shareName, shareValue := buildPublicLinkPasswordCookie("share", "token12345", "hash-value", "secret-key")
	uploadName, uploadValue := buildPublicLinkPasswordCookie("upload", "token12345", "hash-value", "secret-key")

	if shareName != "sesamefs_slpwd_token123" {
		t.Fatalf("share cookie name = %q, want %q", shareName, "sesamefs_slpwd_token123")
	}
	if uploadName != "sesamefs_ulpwd_token123" {
		t.Fatalf("upload cookie name = %q, want %q", uploadName, "sesamefs_ulpwd_token123")
	}
	if shareValue == "" || uploadValue == "" {
		t.Fatal("cookie HMAC values must not be empty")
	}
	if shareValue == uploadValue {
		t.Fatal("share and upload cookie HMACs must differ for the same token/hash pair")
	}

	shareName2, shareValue2 := buildPublicLinkPasswordCookie("share", "token12345", "hash-value", "secret-key")
	if shareName2 != shareName || shareValue2 != shareValue {
		t.Fatal("cookie generation must be stable for identical inputs")
	}
}