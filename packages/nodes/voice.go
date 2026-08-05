package nodes

import "github.com/lflow/lflow/packages/database"

// A voice note: alt+r records (toggle) via ffmpeg, alt+e plays via ffplay. The
// audio is a local wav (~/.local/share/lflow/voice/<uuid>.wav) — never in the DB
// or sync. Inline it shows a ▸ waveform of varying-height bars + duration.
//
// Every hook here is Model-bound (recording state, wav files on disk, ffmpeg/
// ffplay subprocesses) — none of it is pure over Ref/Theme, so renderM/run/
// expand/flashActions all stay in the editor's attachment (see voice.go's
// attachCoreHooks in packages/editor).
func init() {
	Register(Plugin{
		Key:            database.TypeVoice,
		Label:          "Voice",
		InlineEditable: false,
		CLIDeps:        []string{"ffmpeg"},
	})
}
