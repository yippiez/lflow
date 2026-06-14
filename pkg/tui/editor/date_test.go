package editor

import (
	"testing"
	"time"
)

var clock = time.Date(2026, time.June, 12, 19, 4, 0, 0, time.UTC)

func TestDetectDateTurkishFullPhrase(t *testing.T) {
	d := detectDate("toplantı 11 şubat 2025 saat 15:20 ofiste", 0, clock)
	if d == nil {
		t.Fatal("phrase not detected")
	}
	if d.phrase != "11 şubat 2025 saat 15:20" {
		t.Errorf("phrase: %q", d.phrase)
	}
	if !d.hasTime || d.canonical() != "2025-02-11 15:20" {
		t.Errorf("canonical: %q hasTime=%v", d.canonical(), d.hasTime)
	}
}

func TestDetectDateEnglishNamedMonth(t *testing.T) {
	d := detectDate("ship 11 february 2025 at 15:20", 0, clock)
	if d == nil {
		t.Fatal("phrase not detected")
	}
	if d.canonical() != "2025-02-11 15:20" {
		t.Errorf("canonical: %q", d.canonical())
	}
}

func TestDetectDateNamedMonthWithoutClock(t *testing.T) {
	d := detectDate("due 1 mayıs 2025", 0, clock)
	if d == nil {
		t.Fatal("phrase not detected")
	}
	if d.hasTime || d.canonical() != "2025-05-01" {
		t.Errorf("canonical: %q hasTime=%v", d.canonical(), d.hasTime)
	}
}

func TestDetectDateRelativeWords(t *testing.T) {
	cases := []struct {
		text      string
		canonical string
		hasTime   bool
	}{
		{"do it now", "2026-06-12 19:04", true},
		{"şimdi başla", "2026-06-12 19:04", true},
		{"bugün bitir", "2026-06-12", false},
		{"finish today", "2026-06-12", false},
		{"yarın sabah", "2026-06-13", false},
		{"tomorrow first thing", "2026-06-13", false},
		{"dün oldu", "2026-06-11", false},
	}
	for _, tc := range cases {
		d := detectDate(tc.text, 0, clock)
		if d == nil {
			t.Errorf("%q: not detected", tc.text)
			continue
		}
		if d.canonical() != tc.canonical || d.hasTime != tc.hasTime {
			t.Errorf("%q: canonical %q hasTime=%v, want %q %v", tc.text, d.canonical(), d.hasTime, tc.canonical, tc.hasTime)
		}
	}
}

func TestDetectDateISOAndNumeric(t *testing.T) {
	d := detectDate("retro 2025-02-11 14:00 cal", 0, clock)
	if d == nil || d.canonical() != "2025-02-11 14:00" {
		t.Fatalf("iso: %+v", d)
	}

	d = detectDate("son tarih 11/02/2025", 0, clock)
	if d == nil || d.canonical() != "2025-02-11" {
		t.Fatalf("numeric slash: %+v", d)
	}

	d = detectDate("11.02.2025 saat 9.30", 0, clock)
	if d == nil || d.canonical() != "2025-02-11 09:30" {
		t.Fatalf("numeric dot with clock: %+v", d)
	}
}

func TestDetectDateRejectsNoise(t *testing.T) {
	for _, text := range []string{
		"nowhere to go",      // "now" glued inside a word
		"the showdown ended", // "down" is not "dün"
		"version 30.02.2025", // february 30 is not a date
		"",
	} {
		if d := detectDate(text, 0, clock); d != nil {
			t.Errorf("%q: false positive %q", text, d.phrase)
		}
	}
}

func TestDetectDatePicksLeftmost(t *testing.T) {
	// caret at the start: nearest phrase is the leftmost one.
	d := detectDate("2025-02-11 then 2026-01-01", 0, clock)
	if d == nil || d.phrase != "2025-02-11" {
		t.Fatalf("leftmost not picked: %+v", d)
	}
}

func TestDetectDateCaretAware(t *testing.T) {
	text := "tomorrow and yesterday"
	// caret sits inside "yesterday" (begins at rune offset 13).
	d := detectDate(text, 16, clock)
	if d == nil {
		t.Fatal("phrase not detected")
	}
	if d.phrase != "yesterday" {
		t.Errorf("caret in second phrase: picked %q, want %q", d.phrase, "yesterday")
	}
	if d.canonical() != "2026-06-11" {
		t.Errorf("canonical: %q", d.canonical())
	}

	// caret in the first phrase still converts the first phrase.
	if d := detectDate(text, 2, clock); d == nil || d.phrase != "tomorrow" {
		t.Fatalf("caret in first phrase: %+v", d)
	}

	// caret between the two phrases falls to the nearer one.
	if d := detectDate(text, 10, clock); d == nil || d.phrase != "tomorrow" {
		t.Fatalf("caret nearest first: %+v", d)
	}
}
