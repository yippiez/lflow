package editor

// A picker that has to read the disk opens FIRST and fills as the disk answers,
// rather than making the whole editor wait on it.
//
// Two of them did the reading on the update goroutine, which is bubbletea's
// single event loop: nothing repaints and no key is read while it runs, so the
// editor froze for as long as the read took. The Zotero picker paid a library
// read; the session picker walked the CLI stores and parsed the head of every
// transcript in them.
//
// Both now hand that work to a goroutine and take the answer as a message. The
// picker component needs to know nothing about this — it calls src.items() on
// every frame, so a source whose data grows simply draws more rows next frame.
//
// A fill that arrives in pieces (the session scan, which has a row to show per
// file) streams them over a channel, the same shape bash output uses: a command
// that blocks on a receive, and a handler that re-issues it until the channel
// says done.

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// pickerFill is a background read behind an open picker: how many rows have
// landed so far, whether it finished, and what went wrong if it did not.
type pickerFill struct {
	n     int    // rows delivered so far
	done  bool   // the source has nothing left to send
	err   string // non-empty when the read failed
	start time.Time
}

// begin starts a fill, replacing whatever was there — a picker reopened is a
// fill restarted.
func (f *pickerFill) begin() { *f = pickerFill{start: time.Now()} }

// running reports whether a fill is in flight, which is what keeps the
// animation tick alive so the header's spinner turns.
func (f *pickerFill) running() bool { return f != nil && !f.start.IsZero() && !f.done }

// waitFill blocks on the next batch and hands it to the update loop. Re-issued
// by the handler for as long as the channel has more to say.
func waitFill(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

// fillSpinner turns while a picker is filling. Braille rather than the run
// band's shimmer: a header is one line of text, not a block to slide light
// across.
var fillSpinner = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

// fillMark is the header tail a filling picker wears: a turning spinner and the
// count so far, so a slow disk reads as progress rather than as a hang. A
// finished fill adds nothing — the rows themselves are the answer.
func fillMark(f *pickerFill, noun string) string {
	switch {
	case f == nil || f.start.IsZero() || f.done && f.err == "":
		return ""
	case f.err != "":
		return " " + cRed + f.err + cReset
	}
	spin := string(fillSpinner[animFrame%len(fillSpinner)])
	if f.n == 0 {
		return " " + cDim + spin + cReset
	}
	return " " + cDim + fmt.Sprintf("%s %d %s", spin, f.n, noun) + cReset
}
