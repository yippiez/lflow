package browser

import (
	"runtime"
	"testing"
)

func TestIsURL(t *testing.T) {
	urls := []string{
		"https://example.com",
		"http://example.com/path?q=1",
		"ftp://files.example.com",
		"www.example.com",
	}
	for _, u := range urls {
		if !IsURL(u) {
			t.Errorf("IsURL(%q) = false, want true", u)
		}
	}
	notURLs := []string{
		"bd1714",                               // a node uuid
		"buy milk",                             // a node search
		"a3f2b1c0-1234-5678-9abc-def012345678", // a full uuid
		"example.com",                          // bare host, no scheme or www. (ambiguous → not a URL)
		"",
	}
	for _, s := range notURLs {
		if IsURL(s) {
			t.Errorf("IsURL(%q) = true, want false", s)
		}
	}
}

func TestNormalize(t *testing.T) {
	if got := Normalize("www.example.com"); got != "https://example.com" && got != "https://www.example.com" {
		t.Errorf("Normalize(www.) = %q, want https:// prefixed", got)
	}
	if got := Normalize("  https://x.com  "); got != "https://x.com" {
		t.Errorf("Normalize trims + keeps scheme: %q", got)
	}
}

func TestUnderWSL(t *testing.T) {
	if runtime.GOOS != "linux" {
		if underWSL() {
			t.Error("underWSL must be false off Linux")
		}
		return
	}
	// the interop env vars are the cheap, definitive signal
	t.Setenv("WSL_DISTRO_NAME", "Ubuntu")
	if !underWSL() {
		t.Error("WSL_DISTRO_NAME set but underWSL says no")
	}
	t.Setenv("WSL_DISTRO_NAME", "")
	t.Setenv("WSL_INTEROP", "/run/WSL/1_interop")
	if !underWSL() {
		t.Error("WSL_INTEROP set but underWSL says no")
	}
}
