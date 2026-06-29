package editor

import "strings"

import "testing"

func TestDetectImgProtoEnv(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want imgProto
	}{
		{"kitty", map[string]string{"KITTY_WINDOW_ID": "1"}, protoKitty},
		{"term-kitty", map[string]string{"TERM": "xterm-kitty"}, protoKitty},
		{"ghostty", map[string]string{"TERM_PROGRAM": "ghostty"}, protoKitty},
		{"wezterm", map[string]string{"TERM_PROGRAM": "WezTerm"}, protoKitty},
		{"iterm", map[string]string{"TERM_PROGRAM": "iTerm.app"}, protoITerm},
		{"vscode", map[string]string{"TERM_PROGRAM": "vscode"}, protoITerm},
		{"plain", map[string]string{"TERM": "xterm-256color"}, protoNone},
		{"empty", map[string]string{}, protoNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			get := func(k string) string { return c.env[k] }
			if got := detectImgProtoEnv(get); got != c.want {
				t.Fatalf("detectImgProtoEnv(%v) = %d, want %d", c.env, got, c.want)
			}
		})
	}
}

func TestNapkinImageEscape(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nfake-bytes-for-the-test-payload-long-enough")

	if _, ok := napkinImageEscape(protoNone, png, 10, 2); ok {
		t.Fatal("protoNone should not produce an escape")
	}
	if _, ok := napkinImageEscape(protoKitty, nil, 10, 2); ok {
		t.Fatal("empty png should not produce an escape")
	}

	it, ok := napkinImageEscape(protoITerm, png, 12, 3)
	if !ok || !strings.HasPrefix(it, "\x1b]1337;File=inline=1") || !strings.HasSuffix(it, "\a") {
		t.Fatalf("iterm escape malformed: %q", it)
	}
	if !strings.Contains(it, "width=12;height=3") {
		t.Fatalf("iterm escape missing size: %q", it)
	}

	k, ok := napkinImageEscape(protoKitty, png, 12, 3)
	if !ok || !strings.HasPrefix(k, "\x1b_G") || !strings.HasSuffix(k, "\x1b\\") {
		t.Fatalf("kitty escape malformed: %q", k)
	}
	if !strings.Contains(k, "f=100,a=T,c=12,r=3") {
		t.Fatalf("kitty escape missing header: %q", k)
	}
}

func TestItoa(t *testing.T) {
	for _, c := range []struct {
		n    int
		want string
	}{{0, "0"}, {7, "7"}, {42, "42"}, {-5, "-5"}, {1024, "1024"}} {
		if got := itoa(c.n); got != c.want {
			t.Fatalf("itoa(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}
