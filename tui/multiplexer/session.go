package multiplexer

import (
	"context"
	"os"
	"os/exec"
	"sync/atomic"
	"time"
)

// A Session is one process on one PTY with one Screen: a bash node's run, or
// an agent CLI resumed in the background. The producer goroutines pump raw
// output onto Events in time-batched chunks; the OWNER (the editor's update
// loop) drains them, writes the bytes into Scr, and re-arms — the Screen is
// only ever touched from that one goroutine, the same discipline the run
// bands always had.

// Event is one delivery from a session's stream: an output batch, a launch
// failure, or the end of the stream.
type Event struct {
	ID   string
	Data []byte // raw PTY output; nil on Err/Done
	Err  error  // the launch failed; a Done follows
	Done bool   // the stream ended (exit, kill, or launch failure)
}

// Producer-side batching knobs: a batch ships when the flush window elapses
// (so a trickle still appears promptly) or when it hits the size cap (so a
// torrent cannot grow an unbounded transient batch — the capped send then
// backpressures the reader through the channel).
const (
	flushEvery = 50 * time.Millisecond
	batchCap   = 64 << 10
)

// enterDelay is how long SendText waits before shipping the Enter that
// submits: a TUI agent that reads text and Enter in the same read swallows
// the Enter as part of the paste, so the submit goes as its own late write.
const enterDelay = 300 * time.Millisecond

// Session is a live (or just-exited) PTY session.
type Session struct {
	ID      string
	Agent   string // agent variant id ("claude"); "" for a plain shell
	Label   string // what to call it in chrome: a command line or session name
	Dir     string
	Scr     *Screen
	Started time.Time

	events chan Event
	ptmx   *os.File
	cancel context.CancelFunc
	ctx    context.Context
	exited atomic.Bool

	// the status machine's state — owner-goroutine only, like Scr
	status      Status
	visible     bool // the current status is backed by visible chrome
	candidate   Status
	candidateAt time.Time
}

// Events is the session's delivery stream. It is closed after Done.
func (s *Session) Events() <-chan Event { return s.events }

// Live reports whether the process is still running.
func (s *Session) Live() bool { return !s.exited.Load() }

// Elapsed is how long the session has been running.
func (s *Session) Elapsed() time.Duration { return time.Since(s.Started) }

// Write sends raw bytes to the process's stdin (the PTY line discipline).
func (s *Session) Write(p []byte) error {
	_, err := s.ptmx.Write(p)
	return err
}

// SendText ships a message to the program: the text as a paste (bracketed only
// if the program asked for bracketed paste), then Enter on its own delayed
// write — see enterDelay.
func (s *Session) SendText(text string) error {
	payload := []byte(text)
	if s.Scr.BracketedPaste() {
		payload = append(append([]byte("\x1b[200~"), payload...), "\x1b[201~"...)
	}
	if err := s.Write(payload); err != nil {
		return err
	}
	ptmx := s.ptmx
	time.AfterFunc(enterDelay, func() { _, _ = ptmx.Write([]byte("\r")) })
	return nil
}

// Resize sets the PTY size and regrows the screen; the program repaints on the
// SIGWINCH the kernel raises.
func (s *Session) Resize(cols, rows int) {
	_ = resizePTY(s.ptmx, cols, rows)
	s.Scr.Resize(cols, rows)
}

// Kill stops the process. The stream still delivers its Done, which is where
// the owner cleans up.
func (s *Session) Kill() { s.cancel() }

// start launches the process on a fresh PTY and starts the reader/batcher
// pair. On a launch failure the error is delivered as an Event so the owner's
// one drain path sees it.
func (mx *Manager) start(s *Session, argv []string, cols, rows int) {
	ctx, cancel := context.WithCancel(context.Background())
	s.ctx, s.cancel = ctx, cancel
	s.events = make(chan Event, 1024)
	s.Scr = NewScreen(cols, rows, mx.ScrollbackCap)
	s.Started = time.Now()
	s.status, s.candidate = StatusWorking, StatusWorking

	send := func(ev Event) bool {
		select {
		case s.events <- ev:
			return true
		case <-ctx.Done():
			return false
		}
	}

	c := exec.CommandContext(ctx, argv[0], argv[1:]...)
	c.Dir = s.Dir
	// TERM is what the program keys its capabilities off; the size is passed on
	// the PTY itself, so COLUMNS/LINES need no faking.
	c.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
	ptmx, err := startPTY(c, cols, rows)
	if err != nil {
		s.exited.Store(true)
		go func() {
			defer close(s.events)
			send(Event{ID: s.ID, Err: err})
			send(Event{ID: s.ID, Done: true})
		}()
		return
	}
	s.ptmx = ptmx

	go func() {
		// closing the master is what makes the reader return once the child is
		// gone (and what unblocks it on a kill)
		defer close(s.events)
		defer ptmx.Close()
		go func() {
			<-ctx.Done()
			ptmx.Close()
		}()

		chunks := make(chan []byte, 64)
		go func() {
			defer close(chunks)
			buf := make([]byte, 32<<10)
			for {
				n, err := ptmx.Read(buf)
				if n > 0 {
					b := make([]byte, n)
					copy(b, buf[:n])
					select {
					case chunks <- b:
					case <-ctx.Done():
						return
					}
				}
				if err != nil {
					return // EIO on child exit, or the close above
				}
			}
		}()

		// the batcher: coalesce reads, flush a bounded batch per window
		var batch []byte
		flush := func() bool {
			if len(batch) == 0 {
				return true
			}
			ok := send(Event{ID: s.ID, Data: batch})
			batch = nil
			return ok
		}
		tick := time.NewTicker(flushEvery)
		defer tick.Stop()
		alive := true
	collect:
		for {
			select {
			case b, open := <-chunks:
				if !open {
					break collect
				}
				batch = append(batch, b...)
				if len(batch) >= batchCap && !flush() {
					alive = false
					break collect
				}
			case <-tick.C:
				if !flush() {
					alive = false
					break collect
				}
			case <-ctx.Done():
				alive = false
				break collect
			}
		}
		if alive {
			flush()
		}
		_ = c.Wait() // reap — killed or exited, no zombies either way
		s.exited.Store(true)
		if alive {
			send(Event{ID: s.ID, Done: true})
		}
	}()
}
