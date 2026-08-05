package nodes

import (
	"strings"
	"time"

	"github.com/lflow/lflow/packages/database"
)

// The log node type — a timestamped journal line. It began life as the log.js
// NodeMod (external twin: github.com/yippiez/lflow-log) and was compiled in
// when the extension system was removed; the look is the mod's: a → glyph,
// dim body (a /color overrides), a dim "(YYYY-MM-DD HH:MM)" time chip from
// the node's creation time, and a muted tail from the first " · ".
func init() {
	Register(Plugin{
		Key:             database.TypeLog,
		Label:           "Log",
		InlineEditable:  true,
		ContinueOnEnter: true,
		GlyphRef:        logGlyph,
		Prefix:          logPrefix,
		BaseColor:       func() string { return Theme().Dim }, // /color overrides (render.go)
		MuteFrom:        logMuteFrom,
	})
}

func logGlyph(n Ref) (string, string) {
	if c := n.StyleColor(); c != "" {
		return "→", c
	}
	return "→", Theme().Dim
}

func logPrefix(n Ref) string {
	th := Theme()
	t := time.Now()
	if a := n.AddedOn(); a > 0 {
		t = time.Unix(0, a)
	}
	return th.Dim + "(" + t.Format("2006-01-02 15:04") + ") " + th.Reset
}

// logMuteFrom is the rune index the muted tail starts at — the first " · "
// separator, so trailing metadata reads quiet; -1 mutes nothing.
func logMuteFrom(name string) int {
	i := strings.Index(name, " · ")
	if i < 0 {
		return -1
	}
	return len([]rune(name[:i]))
}
