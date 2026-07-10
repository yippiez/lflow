package editor

import (
	"bytes"
	"io"
	"net/http"
	"os/exec"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/dop251/goja"

	"github.com/lflow/lflow/pkg/tui/database"
)

// jsView is the bridge that lets a NodeMod supply the FULL inline expanded view
// (alt+e), not just glyph/render one-liners — the piece that makes a mod as rich
// as a compiled-in type like image. It implements the Go nodeView interface by
// calling into the mod's JS `view` object, which is an Elm-style reducer:
//
//	view: {
//	  init(node)             -> state          // seed; may call lflow.getData
//	  lines(node,state,ctx)  -> N              // optional; else derived from render
//	  render(node,state,ctx) -> [line,line,…]  // FULL list of styled band strings
//	  key(node,state,ctx,k)  -> {state?,effect?}
//	  update(node,state,msg) -> {state?,effect?}   // msg = an effect's result
//	  enter(node,state)      -> bool|{state?,effect?,focus?}
//	  leave(node,state)                            // persist via lflow.setData
//	}
//
// render is PURE and cheap (it runs on the frame goroutine); side effects come
// only from key/update/enter as an EFFECT descriptor the Go side runs as a
// tea.Cmd and feeds back into update(). State is kept as plain Go values (JSON-
// shaped) in the ephemeral nodeStore so it survives the mod reload that happens
// after every agent turn; durable-across-restart state is the mod's own
// lflow.setData → node_mod_data.
//
// WARNING (invariant): a mod view renders BANDS beneath the node in the outline
// flow — never an alt-screen. Go owns the tree rail, the 2-space indent, width
// clipping, and the [scroll, scroll+winH) window; the mod returns inner content.
type jsView struct {
	vm       *goja.Runtime
	key      string
	initFn   goja.Callable
	linesFn  goja.Callable
	renderFn goja.Callable
	keyFn    goja.Callable
	updateFn goja.Callable
	enterFn  goja.Callable
	leaveFn  goja.Callable
}

// modDB is the database the setData/getData helpers read and write. Set in
// initNodeMods; nil in bare test models (getData/setData then no-op).
var modDB *database.DB

// modUpdateMsg carries an effect's result back to a mod view's update() hook.
type modUpdateMsg struct {
	key  string
	uuid string
	msg  map[string]any
}

// modTickMsg is a mod animation frame — delivered only while the mod's view is
// the focused one, so a tick loop cannot animate a node the user has left.
type modTickMsg struct {
	key  string
	uuid string
}

// buildJSView reads a mod's `view` object into a jsView, or returns nil when the
// descriptor has no (or a malformed) view — such a mod is glyph/render only.
func buildJSView(vm *goja.Runtime, viewVal goja.Value) *jsView {
	if viewVal == nil || goja.IsUndefined(viewVal) || goja.IsNull(viewVal) {
		return nil
	}
	obj, ok := viewVal.(*goja.Object)
	if !ok {
		return nil
	}
	v := &jsView{vm: vm}
	v.initFn, _ = jsFunc(vm, obj.Get("init"))
	v.linesFn, _ = jsFunc(vm, obj.Get("lines"))
	v.renderFn, _ = jsFunc(vm, obj.Get("render"))
	v.keyFn, _ = jsFunc(vm, obj.Get("key"))
	v.updateFn, _ = jsFunc(vm, obj.Get("update"))
	v.enterFn, _ = jsFunc(vm, obj.Get("enter"))
	v.leaveFn, _ = jsFunc(vm, obj.Get("leave"))
	if v.renderFn == nil {
		return nil // a view with no render draws nothing — treat as no view
	}
	return v
}

// ── state marshaling: Go-native ⇄ JS, keyed in the ephemeral nodeStore ────────

func (v *jsView) stateKey() string { return "mod:" + v.key }

func (v *jsView) getState(m *Model, it *item) any {
	return m.nodeStore(it.uuid)[v.stateKey()]
}

func (v *jsView) setState(m *Model, it *item, st any) {
	m.nodeStore(it.uuid)[v.stateKey()] = st
}

// toJS turns Go-native state into a JS value; nil becomes null.
func (v *jsView) toJS(st any) goja.Value {
	if st == nil {
		return goja.Null()
	}
	return v.vm.ToValue(st)
}

// parseStep reads a hook's {state?, effect?} return: a missing state keeps prev,
// a present effect is exported as a map for modEffectCmd.
func parseStep(res goja.Value, prev any) (state any, effect map[string]any) {
	state = prev
	if res == nil || goja.IsUndefined(res) || goja.IsNull(res) {
		return
	}
	obj, ok := res.(*goja.Object)
	if !ok {
		return
	}
	if s := obj.Get("state"); s != nil && !goja.IsUndefined(s) {
		state = s.Export()
	}
	if e := obj.Get("effect"); e != nil && !goja.IsUndefined(e) && !goja.IsNull(e) {
		if mp, ok := e.Export().(map[string]any); ok {
			effect = mp
		}
	}
	return
}

// ctxObj is the render/edit context a hook receives: usable inner width, focus,
// and the current scroll window (info only — Go still owns the actual windowing).
func (v *jsView) ctxObj(width int, focused bool, scroll, winH int) goja.Value {
	o := v.vm.NewObject()
	_ = o.Set("width", width)
	_ = o.Set("focused", focused)
	_ = o.Set("scroll", scroll)
	_ = o.Set("winH", winH)
	return o
}

// ── nodeView implementation ───────────────────────────────────────────────────

// Enter seeds state (init, which may pull persisted data via lflow.getData) the
// first time a node's view is opened, then asks the mod whether to focus. An
// enter that returns an effect queues its Cmd on m.modPending (Enter can't
// return one) for the KeyMsg path to drain.
func (v *jsView) Enter(m *Model, it *item) bool {
	if _, seeded := m.nodeStore(it.uuid)[v.stateKey()]; !seeded {
		var st any = map[string]any{}
		if v.initFn != nil {
			if res, ok := callJS(v.vm, v.initFn, jsNodeObj(v.vm, it, it.name)); ok {
				st = jsExport(res)
			}
		}
		v.setState(m, it, st)
	}
	if v.enterFn == nil {
		return true
	}
	res, ok := callJS(v.vm, v.enterFn, jsNodeObj(v.vm, it, it.name), v.toJS(v.getState(m, it)))
	if !ok {
		return true
	}
	// a boolean return is pure focus intent; an object may carry state/effect/focus.
	if b, isBool := res.Export().(bool); isBool {
		return b
	}
	newState, effect := parseStep(res, v.getState(m, it))
	v.setState(m, it, newState)
	if c := modEffectCmd(v.key, it.uuid, effect); c != nil {
		m.modPending = append(m.modPending, c)
	}
	focus := true
	if o, ok := res.(*goja.Object); ok {
		if f := o.Get("focus"); f != nil && !goja.IsUndefined(f) {
			focus = f.ToBoolean()
		}
	}
	return focus
}

// Leave lets the mod flush durable state (typically lflow.setData). Ephemeral
// state stays in nodeStore so re-entering this session resumes where it left.
func (v *jsView) Leave(m *Model, it *item) {
	if v.leaveFn == nil {
		return
	}
	callJS(v.vm, v.leaveFn, jsNodeObj(v.vm, it, it.name), v.toJS(v.getState(m, it)))
}

// renderLines calls the mod's render and returns its band strings (inner, no
// rail). A throwing/garbled render degrades to a one-line placeholder.
func (v *jsView) renderLines(m *Model, it *item, width int, focused bool, scroll, winH int) []string {
	res, ok := callJS(v.vm, v.renderFn,
		jsNodeObj(v.vm, it, it.name), v.toJS(v.getState(m, it)), v.ctxObj(width, focused, scroll, winH))
	if !ok {
		return []string{cRed + "mod render error" + cReset}
	}
	// ExportTo coerces any JS array-like (a JS array OR a wrapped Go []string,
	// e.g. canvas.bands()) into []string — a plain Export() type-switch would miss
	// the []string case and collapse the whole view to one line.
	var out []string
	if err := v.vm.ExportTo(res, &out); err != nil || out == nil {
		return []string{cDim + jsString(res) + cReset}
	}
	if len(out) == 0 {
		out = append(out, cDim+"  (empty)"+cReset)
	}
	return out
}

// Lines reports the full band count so the central loop can clamp the scroll.
// A cheap lines() hook is preferred; otherwise it falls back to counting render.
func (v *jsView) Lines(m *Model, it *item, width int) int {
	inner := width - 2
	if inner < 1 {
		inner = 1
	}
	if v.linesFn != nil {
		if res, ok := callJS(v.vm, v.linesFn,
			jsNodeObj(v.vm, it, it.name), v.toJS(v.getState(m, it)), v.ctxObj(inner, false, 0, 0)); ok {
			return int(res.ToInteger())
		}
	}
	return len(v.renderLines(m, it, inner, false, 0, 0))
}

// Bands renders the visible window: the mod returns the full inner-content list,
// Go prepends the rail + 2-space indent, clips to width, and slices to the
// [scroll, scroll+winH) window (mirroring jsonView/runOutView).
func (v *jsView) Bands(m *Model, it *item, rail string, width, scroll, winH int, focused bool) []string {
	inner := width - visibleWidth(rail) - 2
	if inner < 1 {
		inner = 1
	}
	lines := v.renderLines(m, it, inner, focused, scroll, winH)
	if scroll < 0 {
		scroll = 0
	}
	if scroll > len(lines) {
		scroll = len(lines)
	}
	end := scroll + winH
	if end > len(lines) {
		end = len(lines)
	}
	win := lines[scroll:end]
	out := make([]string, len(win))
	for i, ln := range win {
		out[i] = clip(rail+cReset+"  "+ln, width)
	}
	return out
}

// Key hands the keystroke to the mod. A handled key may mutate state and raise
// an effect (returned as the Cmd); an unhandled key (handled=false) falls through
// to central handling — so a mod that ignores a key still gets esc-to-close.
func (v *jsView) Key(m *Model, it *item, k tea.KeyMsg) (tea.Cmd, bool) {
	if v.keyFn == nil {
		return nil, false
	}
	res, ok := callJS(v.vm, v.keyFn,
		jsNodeObj(v.vm, it, it.name), v.toJS(v.getState(m, it)),
		v.ctxObj(m.width, true, m.focusScroll, 0), v.vm.ToValue(k.String()))
	if !ok {
		return nil, false
	}
	// undefined/false = not handled → central (esc/ctrl+c) still works.
	if res == nil || goja.IsUndefined(res) || goja.IsNull(res) {
		return nil, false
	}
	if b, isBool := res.Export().(bool); isBool && !b {
		return nil, false
	}
	newState, effect := parseStep(res, v.getState(m, it))
	v.setState(m, it, newState)
	return modEffectCmd(v.key, it.uuid, effect), true
}

// update feeds an effect result (or a tick) into the mod's update() hook and
// returns any follow-on effect's Cmd, so an animation/poll loop continues.
func (v *jsView) update(m *Model, it *item, msg map[string]any) tea.Cmd {
	if v.updateFn == nil {
		return nil
	}
	res, ok := callJS(v.vm, v.updateFn,
		jsNodeObj(v.vm, it, it.name), v.toJS(v.getState(m, it)), v.vm.ToValue(msg))
	if !ok {
		return nil
	}
	newState, effect := parseStep(res, v.getState(m, it))
	v.setState(m, it, newState)
	return modEffectCmd(v.key, it.uuid, effect)
}

// jsExport turns a JS value into Go-native state (nil for undefined/null).
func jsExport(v goja.Value) any {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil
	}
	return v.Export()
}

// ── effects: an effect descriptor → a tea.Cmd whose result routes to update() ─

// modEffectCmd translates one {kind, …} effect into a tea.Cmd. Unknown kinds and
// a nil effect are no-ops. exec/fetch run off the event-loop goroutine and post a
// modUpdateMsg; tick posts a gated modTickMsg; batch fans out.
func modEffectCmd(key, uuid string, effect map[string]any) tea.Cmd {
	if effect == nil {
		return nil
	}
	kind, _ := effect["kind"].(string)
	switch kind {
	case "exec":
		cmd, _ := effect["cmd"].(string)
		return func() tea.Msg {
			out, errb, code := runExecSync(cmd)
			return modUpdateMsg{key, uuid, map[string]any{
				"kind": "exec", "stdout": out, "stderr": errb, "code": code,
			}}
		}
	case "fetch":
		url, _ := effect["url"].(string)
		return func() tea.Msg {
			status, body := httpGet(url)
			return modUpdateMsg{key, uuid, map[string]any{
				"kind": "fetch", "status": status, "body": body,
			}}
		}
	case "tick":
		ms := toInt(effect["ms"])
		if ms <= 0 {
			ms = 16
		}
		return tea.Tick(time.Duration(ms)*time.Millisecond, func(time.Time) tea.Msg {
			return modTickMsg{key, uuid}
		})
	case "batch":
		arr, _ := effect["effects"].([]any)
		var cmds []tea.Cmd
		for _, e := range arr {
			if mp, ok := e.(map[string]any); ok {
				if c := modEffectCmd(key, uuid, mp); c != nil {
					cmds = append(cmds, c)
				}
			}
		}
		return tea.Batch(cmds...)
	}
	return nil
}

// runExecSync runs a shell command to completion and returns its output — the
// synchronous sibling of lflow.exec, used off the event loop by an exec effect.
func runExecSync(cmd string) (stdout, stderr string, code int) {
	r := execShell(cmd)
	return r.stdout, r.stderr, r.code
}

// httpGet is the fetch effect's transport: a bounded GET, body capped so a
// runaway response can't balloon the update loop.
func httpGet(url string) (status int, body string) {
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, string(b)
}

// handleModUpdate routes an effect result to the live view of the owning node.
// The view is looked up fresh (typeOf → current runtime), so a mod reload between
// firing an effect and its result never delivers into a stale runtime.
func (m *Model) handleModUpdate(key, uuid string, msg map[string]any) tea.Cmd {
	it := m.tree.byUUID[uuid]
	if it == nil {
		return nil
	}
	v, ok := typeOf(it.typ).view.(*jsView)
	if !ok || v.key != key {
		return nil
	}
	return v.update(m, it, msg)
}

// drainModPending returns the Cmds queued by Enter and clears the queue.
func (m *Model) drainModPending() tea.Cmd {
	if len(m.modPending) == 0 {
		return nil
	}
	cmds := m.modPending
	m.modPending = nil
	return tea.Batch(cmds...)
}

// toInt coerces a JS-exported number (float64/int64/int) to int.
func toInt(v any) int {
	switch n := v.(type) {
	case int64:
		return int(n)
	case int:
		return n
	case float64:
		return int(n)
	}
	return 0
}

// ── shared exec (used by lflow.exec and the exec effect) ─────────────────────

type execResult struct {
	stdout, stderr string
	code           int
}

// execShell runs `bash -c cmd` to completion — the shared engine behind both
// lflow.exec (synchronous, callable from any hook) and the async exec effect.
func execShell(cmd string) execResult {
	c := exec.Command("bash", "-c", cmd)
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
	code := 0
	if err := c.Run(); err != nil {
		code = 1
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
	}
	return execResult{stdout: out.String(), stderr: errb.String(), code: code}
}
