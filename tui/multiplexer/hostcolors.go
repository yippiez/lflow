package multiplexer

import (
	"fmt"
	"os"

	"github.com/muesli/termenv"
)

// CaptureHostColors asks the REAL terminal for its fore/background — callers
// run it before any TUI takes the tty — and files the answers where sessions
// serve them back out: a program on a session PTY queries its "terminal" for
// colors on startup, and the truthful answer is what lets it paint the same
// background the outer terminal has instead of guessing at a theme.
func CaptureHostColors() {
	f := os.Stdout
	if st, err := f.Stat(); err != nil || st.Mode()&os.ModeCharDevice == 0 {
		return // not a terminal: keep the defaults
	}
	out := termenv.NewOutput(f)
	if s, ok := xColor(out.ForegroundColor()); ok {
		ReplyFG = s
	}
	if s, ok := xColor(out.BackgroundColor()); ok {
		ReplyBG = s
	}
}

// xColor renders a termenv color as the X11 rgb spec OSC answers use.
func xColor(c termenv.Color) (string, bool) {
	if c == nil {
		return "", false
	}
	if _, no := c.(termenv.NoColor); no {
		return "", false
	}
	col := termenv.ConvertToRGB(c)
	r, g, b := col.RGB255()
	return fmt.Sprintf("rgb:%02x%02x/%02x%02x/%02x%02x", r, r, g, g, b, b), true
}
