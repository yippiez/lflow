package editor

import (
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/dop251/goja"

	"github.com/lflow/lflow/pkg/tui/database"
)

// Artifacts are runtime-loaded node types (and chip kinds): one JS program per
// row in the artifacts table, evaluated at editor start (and hot-reloaded when
// an agent installs one). Each program runs in its own goja runtime and calls
// lflow.registerType / lflow.registerChip; the bridge turns those into regular
// nodeType descriptors appended to the compiled-in registry, so the picker,
// glyphs and rendering treat both kinds identically. See AGENTS.md.
//
// Artifact JS is TRUSTED — this is a single-user local tool, so hooks may call
// lflow.exec (synchronous shell) even on the render path; a slow hook slows the
// frame and that is the artifact's own problem. All hooks run on the bubbletea
// goroutine, so no locking is needed anywhere in the bridge.

// loadedArtifact pairs a DB row with its load state for the /artifacts view.
type loadedArtifact struct {
	database.Artifact
	loadErr string // non-empty when the JS failed to evaluate (row kept, hooks off)
}

var (
	artifactTypes   []nodeType // runtime-registered node types, in load order
	artifactByKey   = map[string]nodeType{}
	loadedArtifacts []loadedArtifact
)

// loadArtifacts (re)builds the runtime registry from the artifacts table.
// A broken artifact never blocks the editor: its row is listed with the error
// in /artifacts and its nodes fall back to bullets.
func loadArtifacts(db *database.DB) {
	artifactTypes = nil
	artifactByKey = map[string]nodeType{}
	loadedArtifacts = nil

	rows, err := database.ListArtifacts(db)
	if err != nil {
		return // no table yet (pre-migration DB) — plain built-ins only
	}
	for _, a := range rows {
		la := loadedArtifact{Artifact: a}
		if a.Enabled {
			if err := evalArtifact(a); err != nil {
				la.loadErr = err.Error()
			}
		}
		loadedArtifacts = append(loadedArtifacts, la)
	}
}

// installArtifact upserts the artifact and hot-loads it into the running
// registry — the new type is available in /type immediately.
func installArtifact(db *database.DB, a database.Artifact) error {
	if err := a.Upsert(db); err != nil {
		return err
	}
	loadArtifacts(db)
	return nil
}

// evalArtifact runs one artifact program in a fresh runtime and registers what
// it declares.
func evalArtifact(a database.Artifact) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("artifact %s panicked: %v", a.Key, r)
		}
	}()
	vm := goja.New()
	if err := vm.Set("lflow", lflowAPI(vm, a)); err != nil {
		return err
	}
	if _, err := vm.RunScript(a.Key, a.Source); err != nil {
		return fmt.Errorf("artifact %s: %v", a.Key, err)
	}
	return nil
}

// lflowAPI is the whole JS SDK surface: two registration calls plus the
// helpers hooks lean on (style/time/exec).
func lflowAPI(vm *goja.Runtime, a database.Artifact) map[string]interface{} {
	return map[string]interface{}{
		"registerType": func(desc *goja.Object) { registerJSType(vm, a, desc) },
		"registerChip": func(desc *goja.Object) { registerJSChip(vm, desc) },
		"style": func(text, color string) string {
			return colorCode(color) + text + cReset
		},
		"time": func(addedOn string) string {
			nanos, _ := strconv.ParseInt(addedOn, 10, 64)
			t := time.Now()
			if nanos > 0 {
				t = time.Unix(0, nanos)
			}
			return t.Format("2006-01-02 15:04")
		},
		"exec": func(cmd string) map[string]interface{} {
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
			return map[string]interface{}{
				"stdout": out.String(), "stderr": errb.String(), "code": code,
			}
		},
	}
}

// registerJSType converts a JS type descriptor into a nodeType and appends it
// to the runtime registry. A compiled-in key cannot be shadowed (log is free
// because it deliberately left the compiled-in registry).
func registerJSType(vm *goja.Runtime, a database.Artifact, desc *goja.Object) {
	key := jsString(desc.Get("key"))
	label := jsString(desc.Get("label"))
	if key == "" || label == "" {
		return
	}
	if _, builtin := byType[key]; builtin {
		return // compiled-in types win; artifacts extend, never override
	}

	nt := nodeType{
		key:            key,
		label:          label,
		sign:           jsString(desc.Get("sign")),
		inlineEditable: jsBool(desc.Get("inlineEditable")),
	}

	if fn, ok := jsFunc(vm, desc.Get("glyph")); ok {
		nt.glyph = func(it *item) (string, string) {
			v, ok := callJS(vm, fn, jsNodeObj(vm, it, it.name))
			if !ok {
				return glyphOpen, cDim
			}
			var pair []string
			if err := vm.ExportTo(v, &pair); err != nil || len(pair) == 0 {
				return glyphOpen, cDim
			}
			color := cDim
			if len(pair) > 1 {
				color = colorCode(pair[1])
			}
			return pair[0], color
		}
	}
	if fn, ok := jsFunc(vm, desc.Get("baseColor")); ok {
		nt.baseColor = func(it *item) string {
			if v, ok := callJS(vm, fn, jsNodeObj(vm, it, it.name)); ok {
				return colorCode(jsString(v))
			}
			return ""
		}
	}
	if fn, ok := jsFunc(vm, desc.Get("prefix")); ok {
		nt.prefix = func(it *item) string {
			if v, ok := callJS(vm, fn, jsNodeObj(vm, it, it.name)); ok {
				return jsString(v) + cReset
			}
			return ""
		}
	}
	if fn, ok := jsFunc(vm, desc.Get("muteFrom")); ok {
		nt.muteFrom = func(name string) int {
			if v, ok := callJS(vm, fn, vm.ToValue(name)); ok {
				return utf16ToRuneIndex(name, int(v.ToInteger()))
			}
			return -1
		}
	}
	if fn, ok := jsFunc(vm, desc.Get("render")); ok {
		nt.render = func(it *item, name string) string {
			if v, ok := callJS(vm, fn, jsNodeObj(vm, it, name), vm.ToValue(name)); ok {
				return jsString(v)
			}
			return name
		}
	}
	if fn, ok := jsFunc(vm, desc.Get("run")); ok {
		// the JS hook returns a shell command; the Go side streams it through
		// the same ephemeral run machinery as a bash node (run output is never
		// persisted — the invariant holds for artifacts too).
		nt.run = func(m *Model, it *item) tea.Cmd {
			v, ok := callJS(vm, fn, jsNodeObj(vm, it, it.name))
			if !ok {
				return nil
			}
			cmd := jsString(v)
			if cmd == "" {
				return nil
			}
			return runShell(m, it, cmd)
		}
		nt.view = bashView{} // alt+e: the generic scrollable run-output viewer
	}

	artifactTypes = append(artifactTypes, nt)
	artifactByKey[key] = nt
}

// registerJSChip converts a JS chip descriptor into a chipKind. Built-in kinds
// cannot be shadowed.
func registerJSChip(vm *goja.Runtime, desc *goja.Object) {
	key := jsString(desc.Get("key"))
	if key == "" {
		return
	}
	if _, exists := chipKinds[key]; exists {
		return
	}
	marker := jsString(desc.Get("marker"))
	ck := chipKind{
		key:     key,
		color:   colorCode(jsString(desc.Get("color"))),
		display: func(v string) string { return marker + v },
		expand:  func(v string) string { return v },
	}
	if fn, ok := jsFunc(vm, desc.Get("display")); ok {
		ck.display = func(value string) string {
			if v, ok := callJS(vm, fn, vm.ToValue(value)); ok {
				return jsString(v)
			}
			return marker + value
		}
	}
	if fn, ok := jsFunc(vm, desc.Get("expand")); ok {
		ck.expand = func(value string) string {
			if v, ok := callJS(vm, fn, vm.ToValue(value)); ok {
				return jsString(v)
			}
			return value
		}
	}
	chipKinds[key] = ck
}

// jsNodeObj is the read-only node view a hook receives. addedOn crosses as a
// string of UnixNanos — a JS number would round above 2^53.
func jsNodeObj(vm *goja.Runtime, it *item, name string) goja.Value {
	o := vm.NewObject()
	_ = o.Set("uuid", it.uuid)
	_ = o.Set("name", name)
	_ = o.Set("type", it.typ)
	_ = o.Set("color", styleColor(it.style))
	_ = o.Set("addedOn", strconv.FormatInt(it.addedOn, 10))
	_ = o.Set("completed", it.completedAt > 0)
	_ = o.Set("collapsed", it.collapsed)
	_ = o.Set("children", len(it.children))
	return o
}

// callJS invokes a hook defensively: a throwing or panicking artifact never
// takes the editor down, it just falls back to the default rendering.
func callJS(vm *goja.Runtime, fn goja.Callable, args ...goja.Value) (v goja.Value, ok bool) {
	defer func() {
		if recover() != nil {
			v, ok = nil, false
		}
	}()
	v, err := fn(goja.Undefined(), args...)
	if err != nil {
		return nil, false
	}
	return v, true
}

func jsString(v goja.Value) string {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return ""
	}
	return v.String()
}

func jsBool(v goja.Value) bool {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return false
	}
	return v.ToBoolean()
}

func jsFunc(vm *goja.Runtime, v goja.Value) (goja.Callable, bool) {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil, false
	}
	fn, ok := goja.AssertFunction(v)
	return fn, ok
}

// colorCode resolves a JS color name against the active theme: the /style
// palette names plus fg/dim/accent. Unknown names keep the default foreground.
func colorCode(name string) string {
	switch name {
	case "fg":
		return cFG
	case "dim", "":
		return cDim
	case "accent":
		return cAccent
	}
	if c, ok := styleColorCode[name]; ok {
		return c
	}
	return cFG
}

// utf16ToRuneIndex maps a JS string index (UTF-16 units) back to a rune index,
// so muteFrom stays correct on names with astral-plane characters.
func utf16ToRuneIndex(s string, idx int) int {
	if idx < 0 {
		return -1
	}
	units := 0
	for ri, r := range []rune(s) {
		if units >= idx {
			return ri
		}
		if r > 0xFFFF {
			units += 2
		} else {
			units++
		}
	}
	return len([]rune(s))
}
