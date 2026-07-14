package nodes

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/editor"
)

// The factory family — Factorio-style automation machines. Each machine is a
// single-line node (the image-header look: blue glyph, editable command text,
// a compact status chip) and machines COMPOSE: contiguous factory-typed
// SIBLINGS form one belt line, read top-down. alt+r on any machine runs the
// whole line — payloads flow stdout → stdin from machine to machine:
//
//	▼ miner       its text is a shell command; ignores input, emits stdout
//	▣ assembler   pipes the payload through its command (empty = a bare belt)
//	◇ combinator  a gate: its predicate's nonzero exit BLOCKS the line
//	▤ chest       holds the arriving payload (alt+e views it), passes it on
//
// Lines under different parents are independent systems; a bullet between
// machines splits the belt. The look is dark blue (machine chrome) + yellow
// (flow + payload chips). alt+r while a line runs stops it.
//
// WARNING (invariant): payloads and machine status are EPHEMERAL run output —
// they live in facStats (package state, event-loop only), never the DB, never
// synced, gone on restart. Machines run on alt+r only, never automatically.
//
// This file owns the shared machinery (line walking, run engine, chips, the
// payload view); each machine's semantics live in its own file (miner.go,
// assembler.go, combinator.go, chest.go), registered via facStagers.

// The family palette: dark blue chrome + the theme's yellow for flow.
const (
	facBlue = "\x1b[38;2;100;148;220m" // steel blue — machine text + glyphs
	facDeep = "\x1b[38;2;58;92;150m"   // dark blue — chrome, frames, idle belts
)

const (
	facStageTimeout = 60 * time.Second
	facPayloadCap   = 256 * 1024 // a belt carries at most 256 KiB per item
)

// machine states (facStat.state); "" is idle.
const (
	facQueued  = "queued"
	facRunning = "running"
	facOK      = "ok"
	facBlocked = "blocked"
	facError   = "error"
)

// facStat is one machine's ephemeral status — the chip, the last payload that
// left it, and the running line's cancel. Package state keyed by uuid (the
// Prefix hook has no NodeHost); every mutation happens on the event loop.
type facStat struct {
	state   string
	note    string // chip text: "1.2k", "pass", "3L · 142b", "exit 1"
	payload string // what left the machine (chest: what it holds)
	cancel  context.CancelFunc
}

var facStats = map[string]*facStat{}

func facStatOf(uuid string) *facStat {
	st := facStats[uuid]
	if st == nil {
		st = &facStat{}
		facStats[uuid] = st
	}
	return st
}

// facStagers maps a type key to its stage semantics — each machine file
// registers its own at init, so the engine stays generic. A stager receives
// the machine's trimmed command text and the incoming payload.
var facStagers = map[string]func(ctx context.Context, cmd, payload string) facOut{}

// facOut is what one machine does to the belt.
type facOut struct {
	payload string // what leaves the machine
	note    string // status chip text
	err     string // non-empty → the line fails here
	blocked bool   // combinator gate closed → the line halts here
}

// facIsFactory reports whether a type key belongs to the family.
func facIsFactory(typ string) bool { _, ok := facStagers[typ]; return ok }

// facLine returns the belt line containing n: the maximal run of contiguous
// factory-typed siblings around it, in sibling (top-down) order.
func facLine(n editor.NodeRef) []editor.NodeRef {
	sibs := n.Siblings()
	i := -1
	for j, s := range sibs {
		if s.Is(n) {
			i = j
			break
		}
	}
	if i < 0 {
		return []editor.NodeRef{n}
	}
	lo, hi := i, i
	for lo > 0 && facIsFactory(sibs[lo-1].Type()) {
		lo--
	}
	for hi+1 < len(sibs) && facIsFactory(sibs[hi+1].Type()) {
		hi++
	}
	return sibs[lo : hi+1]
}

// facStage is the launch-time snapshot of one machine — the engine goroutine
// must never touch NodeRefs off the event loop.
type facStage struct{ uuid, typ, cmd string }

// facRun (alt+r on any machine) runs the belt line — or stops it when it is
// already running. Shared by all four types.
func facRun(h editor.NodeHost, n editor.NodeRef) tea.Cmd {
	line := facLine(n)
	for _, mc := range line {
		if st := facStats[mc.UUID()]; st != nil && st.cancel != nil {
			st.cancel() // the engine notices, the channel closes, facSettle runs
			return nil
		}
	}
	stages := make([]facStage, 0, len(line))
	for _, mc := range line {
		stages = append(stages, facStage{uuid: mc.UUID(), typ: mc.Type(), cmd: mc.Text()})
	}
	ctx, cancel := context.WithCancel(context.Background())
	for _, s := range stages {
		st := facStatOf(s.uuid)
		st.state, st.note, st.payload, st.cancel = facQueued, "", "", cancel
	}
	// the line always contains n, so the loop ran and every machine holds the
	// shared cancel; this no-op re-anchor is the unconditional use vet's
	// lostcancel needs to see
	facStatOf(stages[0].uuid).cancel = cancel
	// the launcher carries the animating flag: it keeps the belt animation
	// ticking and folds the line into the status bar's "N thinking" tally
	h.NodeStore(n.UUID())["animating"] = true
	ch := make(chan facEvent, 16)
	go facExec(ctx, stages, ch)
	return facWaitCmd(n.UUID(), stages, ch)
}

// facOnRemove cancels the line a removed machine belongs to and drops its
// ephemeral status.
func facOnRemove(h editor.NodeHost, uuid string) {
	if st := facStats[uuid]; st != nil {
		if st.cancel != nil {
			st.cancel()
		}
		delete(facStats, uuid)
	}
	delete(h.NodeStore(uuid), "animating")
}

// ── the engine ──────────────────────────────────────────────────────────────

// facEvent is one engine → editor transition.
type facEvent struct {
	idx     int    // stage index; -1 for line-level events
	kind    string // "start", "ok", "blocked", "error", "done", "canceled"
	payload string
	note    string
}

func facExec(ctx context.Context, stages []facStage, ch chan facEvent) {
	defer close(ch)
	send := func(ev facEvent) bool {
		if ctx.Err() != nil {
			return false // a stopped line settles as "canceled", never as its last event
		}
		select {
		case ch <- ev:
			return true
		case <-ctx.Done():
			return false
		}
	}
	payload := ""
	for i, st := range stages {
		if !send(facEvent{idx: i, kind: "start"}) {
			return
		}
		stager := facStagers[st.typ]
		if stager == nil { // unreachable — the line is built from family types
			continue
		}
		o := stager(ctx, strings.TrimSpace(st.cmd), payload)
		switch {
		case o.err != "":
			send(facEvent{idx: i, kind: "error", note: o.err})
			send(facEvent{idx: -1, kind: "done", note: "line failed · " + o.err})
			return
		case o.blocked:
			send(facEvent{idx: i, kind: "blocked"})
			send(facEvent{idx: -1, kind: "done", note: "line blocked at the combinator"})
			return
		}
		payload = facCap(o.payload)
		if !send(facEvent{idx: i, kind: "ok", payload: payload, note: o.note}) {
			return
		}
	}
	send(facEvent{idx: -1, kind: "done", note: "line delivered · " + facSize(len(payload))})
}

// facEvMsg carries one engine event back onto the event loop
// (editor.NodePluginMsg).
type facEvMsg struct {
	launcher string
	stages   []facStage
	ev       facEvent
	ch       chan facEvent
}

func facWaitCmd(launcher string, stages []facStage, ch chan facEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return facEvMsg{launcher: launcher, stages: stages, ev: facEvent{idx: -1, kind: "canceled"}}
		}
		return facEvMsg{launcher: launcher, stages: stages, ev: ev, ch: ch}
	}
}

// HandleNodePlugin lands one engine event: status chips update per stage, the
// terminal event settles the line.
func (msg facEvMsg) HandleNodePlugin(h editor.NodeHost) tea.Cmd {
	uuid := ""
	if msg.ev.idx >= 0 && msg.ev.idx < len(msg.stages) {
		uuid = msg.stages[msg.ev.idx].uuid
	}
	switch msg.ev.kind {
	case "start":
		facStatOf(uuid).state = facRunning
	case "ok":
		st := facStatOf(uuid)
		st.state, st.note, st.payload = facOK, msg.ev.note, msg.ev.payload
	case "blocked":
		st := facStatOf(uuid)
		st.state, st.note = facBlocked, "blocked"
	case "error":
		st := facStatOf(uuid)
		st.state, st.note = facError, msg.ev.note
	case "done":
		facSettle(h, msg.launcher, msg.stages)
		h.NodeFlash(msg.ev.note)
		return nil
	case "canceled":
		facSettle(h, msg.launcher, msg.stages)
		h.NodeFlash("line stopped")
		return nil
	}
	return facWaitCmd(msg.launcher, msg.stages, msg.ch)
}

// facSettle parks the line: cancel released, still-pending machines back to
// idle (terminal chips — ok/blocked/error — stay up as the line's readout).
func facSettle(h editor.NodeHost, launcher string, stages []facStage) {
	delete(h.NodeStore(launcher), "animating")
	for _, s := range stages {
		st := facStats[s.uuid]
		if st == nil {
			continue
		}
		if st.cancel != nil {
			st.cancel() // release the context; idempotent
			st.cancel = nil
		}
		if st.state == facQueued || st.state == facRunning {
			st.state, st.note = "", ""
		}
	}
}

// ── executing one machine ───────────────────────────────────────────────────

// facExecCmd runs `bash -c cmd` with the payload on stdin. exit is 0 on
// success, the exit code on a nonzero exit, -1 on spawn failure or timeout;
// note is the human chip/flash text ("" on success).
func facExecCmd(ctx context.Context, cmd, stdin string) (out string, exit int, note string) {
	cctx, cancel := context.WithTimeout(ctx, facStageTimeout)
	defer cancel()
	c := exec.CommandContext(cctx, "bash", "-c", cmd)
	c.Stdin = strings.NewReader(stdin)
	var ob, eb bytes.Buffer
	c.Stdout, c.Stderr = &ob, &eb
	err := c.Run()
	if err == nil {
		return ob.String(), 0, ""
	}
	if cctx.Err() == context.DeadlineExceeded {
		return ob.String(), -1, "timeout"
	}
	exit = -1
	note = err.Error()
	if ee, ok := err.(*exec.ExitError); ok {
		exit = ee.ExitCode()
		note = fmt.Sprintf("exit %d", exit)
	}
	if l := strings.TrimSpace(strings.SplitN(eb.String(), "\n", 2)[0]); l != "" {
		if r := []rune(l); len(r) > 40 {
			l = string(r[:40]) + "…"
		}
		note += " · " + l
	}
	return ob.String(), exit, note
}

// facCap bounds a payload to what a belt carries.
func facCap(s string) string {
	if len(s) <= facPayloadCap {
		return s
	}
	return s[:facPayloadCap]
}

// facSize renders a byte count as a compact chip ("142b", "1.2k", "3.0m").
func facSize(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%db", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1fk", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1fm", float64(n)/(1024*1024))
	}
}

// ── the look ────────────────────────────────────────────────────────────────

// facPrefix is the family's status chip, rendered before the caret-editable
// command text (the Prefix hook): idle machines stay clean, a running one
// wears the animated yellow belt, a finished one its yellow payload chip.
func facPrefix(uuid string) string {
	st := facStats[uuid]
	if st == nil || st.state == "" {
		return ""
	}
	th := editor.NodeTheme()
	switch st.state {
	case facQueued:
		return facDeep + "▸▸▸ " + th.Reset
	case facRunning: // one yellow item travels the dark-blue belt
		lit := editor.NodeAnimFrame() / 4 % 3
		var b strings.Builder
		for i := 0; i < 3; i++ {
			if i == lit {
				b.WriteString(th.Yellow)
			} else {
				b.WriteString(facDeep)
			}
			b.WriteString("▸")
		}
		b.WriteString(th.Reset + " ")
		return b.String()
	case facOK:
		return facDeep + "⟨" + th.Yellow + st.note + facDeep + "⟩ " + th.Reset
	case facBlocked:
		return th.Yellow + "⊘ blocked " + th.Reset
	case facError:
		return th.Red + "✗ " + st.note + " " + th.Reset
	}
	return ""
}

// facContext is the trivial toContext hook — the machine's element name alone
// (<miner>date</miner>); the command text stays the element body.
func facContext(tag string) func(editor.NodeHost, editor.NodeRef) (string, string, string) {
	return func(editor.NodeHost, editor.NodeRef) (string, string, string) {
		return tag, "", ""
	}
}

// ── the payload view (alt+e) ────────────────────────────────────────────────

// facView shows the payload that last left the machine (a chest: what it
// holds) — read-only band lines behind a dark-blue rule, scrolled with its own
// offset in the node store. Shared by all four machine types.
type facView struct{}

func (facView) Enter(h editor.NodeHost, n editor.NodeRef) bool {
	st := facStats[n.UUID()]
	if st == nil || st.payload == "" {
		h.NodeFlash("no payload yet · ⌥r runs the line")
		return false
	}
	h.NodeStore(n.UUID())["facScroll"] = 0
	return true
}

func (facView) Leave(editor.NodeHost, editor.NodeRef) {}

func (facView) Lines(h editor.NodeHost, n editor.NodeRef, width int) int {
	st := facStats[n.UUID()]
	if st == nil || st.payload == "" {
		return 0
	}
	return 1 + len(strings.Split(st.payload, "\n"))
}

func (facView) Key(h editor.NodeHost, n editor.NodeRef, k tea.KeyMsg) (tea.Cmd, bool) {
	sc, _ := h.NodeStore(n.UUID())["facScroll"].(int)
	switch k.String() {
	case "alt+r":
		return facRun(h, n), true
	case "down", "j":
		sc++
	case "up", "k":
		sc--
	case "pgdown":
		sc += 10
	case "pgup":
		sc -= 10
	case "home", "g":
		sc = 0
	case "end", "G":
		sc = 1 << 30 // Bands clamps to the last page
	default:
		return nil, false // esc, ctrl+c … → central
	}
	if sc < 0 {
		sc = 0
	}
	h.NodeStore(n.UUID())["facScroll"] = sc
	return nil, true
}

func (facView) Bands(h editor.NodeHost, n editor.NodeRef, rail string, width, scroll, winH int, focused bool) []string {
	st := facStats[n.UUID()]
	if st == nil {
		return nil
	}
	th := editor.NodeTheme()
	hdr := "  " + n.Type() + " · " + st.note + " · ↑↓ scroll · esc close"
	content := []string{editor.NodeClip(rail+th.Reset+th.Dim+hdr+th.Reset, width)}
	for _, l := range strings.Split(st.payload, "\n") {
		content = append(content, editor.NodeClip(rail+th.Reset+"  "+facDeep+"▏"+th.Reset+" "+l, width))
	}
	sc, _ := h.NodeStore(n.UUID())["facScroll"].(int)
	if max := len(content) - winH; sc > max {
		sc = max
	}
	if sc < 0 {
		sc = 0
	}
	return editor.NodeWindowBands(content, sc, winH)
}
