// Package mux drives the multiplexer from the command line, outside the
// editor: run any program on a session PTY, feed it input on a timeline, and
// dump the screen the session's VT holds — plain or styled. It exists so the
// multiplexer can be exercised and inspected by scripts and agents: the same
// engine the editor's agent chips ride, minus the editor.
//
//	lflow mux run --dur 6s --snap 2s --send '3s:hello' -- opencode
//	lflow mux run --ansi --key '1s:down' --key '2s:enter' -- vim README.md
//	lflow mux colors
package mux

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/lflow/lflow/tui/multiplexer"
)

// NewCmd returns the mux command tree. It needs no database and no daemon.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mux",
		Short: "Drive a multiplexer session from the CLI",
	}
	cmd.AddCommand(newRunCmd(), newColorsCmd())
	return cmd
}

func newColorsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "colors",
		Short: "Print the host terminal colors sessions answer queries with",
		Run: func(cmd *cobra.Command, args []string) {
			multiplexer.CaptureHostColors()
			fmt.Printf("fg %s\nbg %s\n", multiplexer.ReplyFG, multiplexer.ReplyBG)
		},
	}
}

// action is one timed step of the run: a text send, a key, or a snapshot.
type action struct {
	at   time.Duration
	kind string // send | key | snap
	arg  string
}

func newRunCmd() *cobra.Command {
	var (
		cols, rows int
		dur        time.Duration
		agent      string
		ansi       bool
		sends      []string
		types      []string
		keys       []string
		snaps      []string
	)
	cmd := &cobra.Command{
		Use:   "run [flags] -- command [args...]",
		Short: "Run a command on a session PTY and dump its screen",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			multiplexer.CaptureHostColors()
			timeline, err := parseTimeline(sends, types, keys, snaps)
			if err != nil {
				return err
			}
			return runSession(args, cols, rows, dur, agent, ansi, timeline)
		},
	}
	cmd.Flags().IntVar(&cols, "cols", 100, "PTY columns")
	cmd.Flags().IntVar(&rows, "rows", 28, "PTY rows")
	cmd.Flags().DurationVar(&dur, "dur", 5*time.Second, "how long the session runs before it is stopped")
	cmd.Flags().StringVar(&agent, "agent", "", "agent id for status detection (claude, pi, opencode, prime-agent)")
	cmd.Flags().BoolVar(&ansi, "ansi", false, "dump styled rows (SGR escapes) instead of plain text")
	cmd.Flags().StringArrayVar(&sends, "send", nil, "send a MESSAGE at an offset, e.g. '2s:hello there' — paste semantics (bracketed if the app asked) with Enter following; what the editor's quick reply does")
	cmd.Flags().StringArrayVar(&types, "type", nil, "type text at an offset as raw KEYSTROKES, e.g. '1s:jjZZ' — no paste guards, no Enter; what drives vim-style normal modes")
	cmd.Flags().StringArrayVar(&keys, "key", nil, "send a key at an offset, e.g. '1s:enter' — a name (enter, esc, tab, arrows, space, backspace, ctrl+<x>) or a single character")
	cmd.Flags().StringArrayVar(&snaps, "snap", nil, "capture the screen at an offset, e.g. '2s' (the end is always captured)")
	return cmd
}

func parseTimeline(sends, types, keys, snaps []string) ([]action, error) {
	var out []action
	split := func(s string) (time.Duration, string, error) {
		at, arg, ok := strings.Cut(s, ":")
		if !ok {
			return 0, "", fmt.Errorf("want '<offset>:<value>', got %q", s)
		}
		d, err := time.ParseDuration(at)
		if err != nil {
			return 0, "", fmt.Errorf("bad offset in %q: %w", s, err)
		}
		return d, arg, nil
	}
	for _, s := range sends {
		d, arg, err := split(s)
		if err != nil {
			return nil, err
		}
		out = append(out, action{d, "send", arg})
	}
	for _, s := range types {
		d, arg, err := split(s)
		if err != nil {
			return nil, err
		}
		out = append(out, action{d, "type", arg})
	}
	for _, s := range keys {
		d, arg, err := split(s)
		if err != nil {
			return nil, err
		}
		out = append(out, action{d, "key", arg})
	}
	for _, s := range snaps {
		d, err := time.ParseDuration(s)
		if err != nil {
			return nil, fmt.Errorf("bad snap offset %q: %w", s, err)
		}
		out = append(out, action{d, "snap", ""})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].at < out[j].at })
	return out, nil
}

// namedKeys is the CLI's key vocabulary — raw terminal bytes, the same ones
// the attach view forwards.
var namedKeys = map[string]string{
	"enter": "\r", "esc": "\x1b", "tab": "\t", "space": " ",
	"backspace": "\x7f", "up": "\x1b[A", "down": "\x1b[B",
	"right": "\x1b[C", "left": "\x1b[D", "home": "\x1b[H", "end": "\x1b[F",
	"pgup": "\x1b[5~", "pgdown": "\x1b[6~", "delete": "\x1b[3~",
}

func keyBytes(name string) ([]byte, error) {
	if b, ok := namedKeys[name]; ok {
		return []byte(b), nil
	}
	if c, ok := strings.CutPrefix(name, "ctrl+"); ok && len(c) == 1 && c[0] >= 'a' && c[0] <= 'z' {
		return []byte{c[0] - 'a' + 1}, nil
	}
	// a single character is itself a key
	if r := []rune(name); len(r) == 1 {
		return []byte(name), nil
	}
	return nil, fmt.Errorf("unknown key %q", name)
}

func runSession(argv []string, cols, rows int, dur time.Duration, agent string, ansi bool, timeline []action) error {
	mx := multiplexer.NewManager(5000)
	s := mx.Start("cli", agent, strings.Join(argv, " "), "", argv, cols, rows)
	started := time.Now()

	dump := func(label string) {
		fmt.Printf("--- snap %s ---\n", label)
		if ansi {
			for _, row := range s.Scr.GridLines(false) {
				fmt.Println(row)
			}
		} else {
			for _, row := range s.Scr.GridPlain() {
				fmt.Println(row)
			}
		}
	}

	// the status timeline: every transition, stamped with its offset
	var transitions []string
	last := multiplexer.Status(-1)
	note := func() {
		if st := s.UpdateStatus(); st != last {
			transitions = append(transitions, fmt.Sprintf("%s@%s", st, time.Since(started).Round(100*time.Millisecond)))
			last = st
		}
	}

	deadline := time.After(dur)
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	next := 0
	exit := "killed at --dur"
loop:
	for {
		var timer <-chan time.Time
		if next < len(timeline) {
			wait := time.Until(started.Add(timeline[next].at))
			if wait < 0 {
				wait = 0
			}
			timer = time.After(wait)
		}
		select {
		case ev, ok := <-s.Events():
			if !ok || ev.Done {
				exit = "exited"
				break loop
			}
			if ev.Err != nil {
				return ev.Err
			}
			if len(ev.Data) > 0 {
				s.Scr.Write(ev.Data)
				note()
			}
		case <-timer:
			a := timeline[next]
			next++
			switch a.kind {
			case "send":
				_ = s.SendText(a.arg)
			case "type":
				_ = s.Write([]byte(a.arg))
			case "key":
				b, err := keyBytes(a.arg)
				if err != nil {
					return err
				}
				_ = s.Write(b)
			case "snap":
				dump(a.at.String())
			}
		case <-tick.C:
			note() // the debounce clock advances even when the program is silent
		case <-deadline:
			break loop
		}
	}
	note()
	dump("end")
	fmt.Println("--- summary ---")
	fmt.Printf("title: %s\n", s.Scr.Title())
	fmt.Printf("status: %s\n", strings.Join(transitions, " "))
	fmt.Printf("exit: %s\n", exit)
	fmt.Printf("colors: fg=%s bg=%s\n", multiplexer.ReplyFG, multiplexer.ReplyBG)
	if s.Live() {
		s.Kill()
	}
	// give the kill a moment to reap, then let process exit clean up the rest
	select {
	case <-s.Events():
	case <-time.After(time.Second):
	}
	_ = os.Stdout.Sync()
	return nil
}
