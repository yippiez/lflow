package chiptext

import "testing"

// TestServiceForRecognizesTheSuite: the doc family splits by path on one shared
// host, the standalone services by host, and everything else is not a service.
func TestServiceForRecognizesTheSuite(t *testing.T) {
	cases := []struct{ url, key string }{
		{"https://docs.google.com/spreadsheets/d/1abc/edit#gid=0", "sheets"},
		{"https://docs.google.com/presentation/d/1abc/edit", "slides"},
		{"https://drive.google.com/drive/folders/1abc", "drive"},
		{"https://drive.google.com/file/d/1abc/view", "drive"},
		{"https://mail.google.com/mail/u/0/#inbox/abc", "gmail"},
		{"https://colab.research.google.com/drive/1aBcD", "colab"},
		{"https://colab.google/", "colab"},
		{"https://gemini.google.com/app/1a2b", "gemini"},
		// a scheme is optional — a bare paste is recognized the same way
		{"docs.google.com/spreadsheets/d/1abc", "sheets"},
		// www. is stripped before matching
		{"https://www.drive.google.com/drive/my-drive", "drive"},
	}
	for _, c := range cases {
		svc, ok := ServiceFor(c.url)
		if !ok {
			t.Errorf("%s: not recognized", c.url)
			continue
		}
		if svc.Key != c.key {
			t.Errorf("%s: key = %q, want %q", c.url, svc.Key, c.key)
		}
	}
}

// TestServiceForIgnoresEverythingElse: only the registered services dress up —
// a bare docs.google.com (no doc path), a lookalike host, another site, a node
// link and empty text are all plain links.
func TestServiceForIgnoresEverythingElse(t *testing.T) {
	for _, s := range []string{
		"https://docs.google.com/",
		// unlisted docs.google.com apps stay ordinary links
		"https://docs.google.com/document/d/1abc/edit",
		"https://docs.google.com/forms/d/1abc/viewform",
		"https://calendar.google.com/calendar/u/0/r/eventedit/abc",
		"https://meet.google.com/abc-defg-hij",
		"https://docs.google.com.evil.example/spreadsheets/d/1",
		"https://example.com/spreadsheets/d/1",
		"lflow://node/abc-123",
		"",
		"   ",
	} {
		if svc, ok := ServiceFor(s); ok {
			t.Errorf("%q wrongly matched %s", s, svc.Key)
		}
	}
}

// TestServiceDisplayTitleFallback: the chip's own title wins; a chip with none
// falls back to the service name, so a service link is never blank.
func TestServiceDisplayTitleFallback(t *testing.T) {
	svc, ok := ServiceFor("https://docs.google.com/spreadsheets/d/1abc/edit")
	if !ok {
		t.Fatal("sheets URL not recognized")
	}
	if got := ServiceDisplay(svc, "Q3 budget"); got != "▦ Q3 budget" {
		t.Errorf("display = %q, want ▦ Q3 budget", got)
	}
	if got := ServiceDisplay(svc, ""); got != "▦ Sheets" {
		t.Errorf("untitled display = %q, want ▦ Sheets", got)
	}
}
