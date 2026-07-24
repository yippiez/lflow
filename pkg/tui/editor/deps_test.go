package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
)

func TestMissingDepGates(t *testing.T) {
	m := newTestModel(80, "node")
	m.deps = map[string]bool{"pi": false, "ffmpeg": false}

	if bin, missing := m.typeDepMissing(database.TypeVoice); !missing || bin != "ffmpeg" {
		t.Fatalf("voice dep = %q missing=%v", bin, missing)
	}

	mm, _ := typeSource{}.onSelect(m, pickerItem{value: database.TypeVoice})
	if mm.(*Model).flash != "Missing dependency: ffmpeg" {
		t.Fatalf("type pick flash = %q", m.flash)
	}
	var row pickerItem
	for _, it := range (typeSource{}).items(m, "voice") {
		if it.value == database.TypeVoice {
			row = it
		}
	}
	if row.render == nil || !strings.Contains(row.render(false), "missing ffmpeg") {
		t.Fatal("voice row must render disabled")
	}

	m.deps = map[string]bool{"pi": true, "ffmpeg": true}
	if _, missing := m.typeDepMissing(database.TypeVoice); missing {
		t.Fatal("available dep must not gate")
	}
}
