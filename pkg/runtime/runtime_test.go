package runtime

import (
	"testing"
	"time"
)

func TestUUIDToNbHash(t *testing.T) {
	got := uuidToNbHash("550e8400-e29b-41d4-a716-446655440000")
	want := "550e8400_e29b_41d4_a716_446655440000........"
	if got != want {
		t.Fatalf("uuidToNbHash = %q, want %q", got, want)
	}
	if len(got) != 44 {
		t.Fatalf("nbHash length = %d, want 44", len(got))
	}
}

func TestStripXSSI(t *testing.T) {
	in := []byte(")]}'\n{\"ok\":true}")
	if got := string(stripXSSI(in)); got != `{"ok":true}` {
		t.Fatalf("stripXSSI = %q", got)
	}
	plain := []byte(`{"ok":true}`)
	if got := string(stripXSSI(plain)); got != `{"ok":true}` {
		t.Fatalf("stripXSSI changed plain JSON: %q", got)
	}
}

func TestRequestedAccelerator(t *testing.T) {
	if got := requestedAccelerator("t4", false); got != "T4" {
		t.Fatalf("gpu accelerator = %q, want T4", got)
	}
	if got := requestedAccelerator("t4", true); got != "NONE" {
		t.Fatalf("cpu accelerator = %q, want NONE", got)
	}
}

func TestParseTokenTTLSeconds(t *testing.T) {
	secs, err := parseTokenTTLSeconds("3600s")
	if err != nil || secs != 3600 {
		t.Fatalf("parseTokenTTLSeconds(3600s) = %d, %v", secs, err)
	}
	if _, err := parseTokenTTLSeconds(""); err == nil {
		t.Fatal("expected error for empty TTL")
	}
}

func TestValidateRuntimeProxyURL(t *testing.T) {
	ok := []string{
		"https://abc.prod.colab.dev",
		"https://x.colab.googleusercontent.com/",
	}
	for _, u := range ok {
		if _, err := validateRuntimeProxyURL(u); err != nil {
			t.Errorf("validateRuntimeProxyURL(%q) unexpected error: %v", u, err)
		}
	}
	bad := []string{
		"",
		"http://abc.prod.colab.dev",       // not https
		"https://evil.example.com",        // not allowlisted
		"https://1.2.3.4",                 // IP
		"https://abc.prod.colab.dev/path", // path
	}
	for _, u := range bad {
		if _, err := validateRuntimeProxyURL(u); err == nil {
			t.Errorf("validateRuntimeProxyURL(%q) expected error, got nil", u)
		}
	}
}

func TestTokenValid(t *testing.T) {
	if (&Token{}).Valid() {
		t.Fatal("empty token should be invalid")
	}
	if (&Token{AccessToken: "a", Expiry: time.Now().Add(-time.Hour)}).Valid() {
		t.Fatal("expired token should be invalid")
	}
	if !(&Token{AccessToken: "a", Expiry: time.Now().Add(time.Hour)}).Valid() {
		t.Fatal("fresh token should be valid")
	}
	if !(&Token{AccessToken: "a"}).Valid() {
		t.Fatal("token with no expiry should be valid")
	}
}
