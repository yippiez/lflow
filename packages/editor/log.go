package editor

import (
	"strings"
	"time"
)

// The log node type — a timestamped journal line. It began life as the log.js
// NodeMod (external twin: github.com/yippiez/lflow-log) and was compiled in
// when the extension system was removed; the look is the mod's: a → glyph,
// dim body (a /color overrides), a dim "(YYYY-MM-DD HH:MM)" time chip from
// the node's creation time, and a muted tail from the first " · ".

func logGlyph(it *item) (string, string) {
	if c := styleBaseColor(it.style); c != "" {
		return "→", c
	}
	return "→", cDim
}

func logPrefix(it *item) string {
	t := time.Now()
	if it.addedOn > 0 {
		t = time.Unix(0, it.addedOn)
	}
	return cDim + "(" + t.Format("2006-01-02 15:04") + ") " + cReset
}

// logToContext carries the timestamp the prefix draws, so structured context reads the
// log line with its time instead of a bare bullet.
func logToContext(it *item) contextXML {
	x := contextXML{tag: "log"}
	if it.addedOn > 0 {
		x.attrs = `time="` + time.Unix(0, it.addedOn).Format("2006-01-02 15:04") + `"`
	}
	return x
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
