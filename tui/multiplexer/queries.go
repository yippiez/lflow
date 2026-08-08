package multiplexer

import (
	"fmt"
	"os"
	"regexp"
)

// The query answerer runs IN THE READER GOROUTINE, replying the instant a
// query byte-sequence comes off the PTY. It exists for one reason: a TUI asks
// for the terminal's colors and capabilities in its first milliseconds and
// falls back — to a light theme, to degraded drawing — if the answer misses
// its startup window. Routing replies through the editor's event loop (50ms
// batching plus a frame) was exactly slow enough to lose that race; opencode
// themed itself light on it.
//
// The screen no longer answers these (it would double-reply, and stray reply
// bytes read as keyboard input); it keeps only the cursor-position report,
// which needs the grid's live state.

var (
	reQDA1  = regexp.MustCompile(`\x1b\[0?c`)
	reQDA2  = regexp.MustCompile(`\x1b\[>0?c`)
	reQDSR5 = regexp.MustCompile(`\x1b\[5n`)
	reQMode = regexp.MustCompile(`\x1b\[\?([0-9]+)\$p`)
	reQFG   = regexp.MustCompile(`\x1b\]10;\?(?:\x07|\x1b\\)`)
	reQBG   = regexp.MustCompile(`\x1b\]11;\?(?:\x07|\x1b\\)`)
	reQTcap = regexp.MustCompile(`\x1bP\+q[0-9A-Fa-f;]*(?:\x07|\x1b\\)`)
)

// queryAnswerer scans output chunks for terminal queries and answers them on
// the PTY immediately. It keeps a small overlap window so a query split
// across two reads still matches, and skips matches that fell entirely inside
// the previous window so nothing is answered twice.
type queryAnswerer struct {
	out  *os.File
	pend []byte // tail of the previous chunk, for split sequences
}

const qaOverlap = 48

func (qa *queryAnswerer) scan(chunk []byte) {
	buf := append(append([]byte{}, qa.pend...), chunk...)
	seen := len(qa.pend)
	reply := func(s string) { _, _ = qa.out.WriteString(s) }

	for _, m := range reQDA1.FindAllIndex(buf, -1) {
		if m[1] > seen {
			reply("\x1b[?62;22c")
		}
	}
	for _, m := range reQDA2.FindAllIndex(buf, -1) {
		if m[1] > seen {
			reply("\x1b[>1;10;0c")
		}
	}
	for _, m := range reQDSR5.FindAllIndex(buf, -1) {
		if m[1] > seen {
			reply("\x1b[0n")
		}
	}
	for _, m := range reQMode.FindAllSubmatchIndex(buf, -1) {
		if m[1] > seen {
			// "not recognized" is an honest ANSWER for modes the screen only
			// half-models — what matters is that the asker stops waiting
			reply(fmt.Sprintf("\x1b[?%s;0$y", buf[m[2]:m[3]]))
		}
	}
	for _, m := range reQFG.FindAllIndex(buf, -1) {
		if m[1] > seen {
			reply(fmt.Sprintf("\x1b]10;%s\x07", ReplyFG))
		}
	}
	for _, m := range reQBG.FindAllIndex(buf, -1) {
		if m[1] > seen {
			reply(fmt.Sprintf("\x1b]11;%s\x07", ReplyBG))
		}
	}
	for _, m := range reQTcap.FindAllIndex(buf, -1) {
		if m[1] > seen {
			reply("\x1bP0+r\x1b\\")
		}
	}

	if len(buf) > qaOverlap {
		buf = buf[len(buf)-qaOverlap:]
	}
	qa.pend = append(qa.pend[:0], buf...)
}
