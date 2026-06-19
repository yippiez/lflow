package editor

import (
	"os"
	"path/filepath"
	"testing"

	tuictx "github.com/lflow/lflow/pkg/tui/context"
)

// TestVoiceRenderNilMaps guards the panic where voiceRender wrote to a nil voice
// map when a voice node was loaded from disk (runVoice never ran). Voice state now
// lives in the per-node store (created on demand), so rendering must never panic.
func TestVoiceRenderNilMaps(t *testing.T) {
	dir := t.TempDir()
	m := &Model{ctx: tuictx.DnoteCtx{Paths: tuictx.Paths{Data: dir}}}
	it := &item{uuid: "v1", typ: "voice"}

	// no wav on disk, all maps nil
	if got := m.voiceRender(it); got == "" {
		t.Fatal("expected empty-state label, got blank")
	}

	// wav on disk, maps still nil — this is the path that panicked
	wav := m.voicePath(it.uuid)
	if err := os.MkdirAll(filepath.Dir(wav), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wav, make([]byte, 44), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = m.voiceRender(it) // must not panic
}
