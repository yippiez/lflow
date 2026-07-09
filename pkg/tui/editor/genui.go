package editor

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/dop251/goja"

	"github.com/lflow/lflow/pkg/tui/consts"
	"github.com/lflow/lflow/pkg/tui/database"
)

// GenUI nodes are runtime-installed node types (and chip kinds), "nodes" to
// the user: one JS file per type in <config>/lflow/nodes — log.js serves the
// type "log"; renaming it log.js.disabled turns the type off without losing
// it. The directory is evaluated at editor start via goja, and reloaded when
// /type opens and after every agent turn — an agent (or the user) edits the
// files directly and the running editor picks the change up. Each program runs
// in its own goja runtime and calls lflow.registerType / lflow.registerChip;
// the bridge turns those into regular nodeType descriptors appended to the
// compiled-in registry, so the picker, glyphs and rendering treat both kinds
// identically. See AGENTS.md.
//
// WARNING (invariant): a genui node is a file, never schema — installing one
// is a file write, never a DB migration. A node whose type file is missing or
// disabled falls back to bullets via typeOf, so the outline always loads.
//
// GenUI JS is TRUSTED — this is a single-user local tool, so hooks may call
// lflow.exec (synchronous shell) even on the render path; a slow hook slows
// the frame and that is the file's own problem. All hooks run on the bubbletea
// goroutine, so no locking is needed anywhere in the bridge.

const genUIDisabledExt = ".disabled"

// genUINode pairs one <key>.js file with its load state for the /type rows.
type genUINode struct {
	Key     string // filename without extension — the nodes.type it serves
	Label   string // the registered label (the key, title-cased, until eval)
	Source  string
	Enabled bool
	loadErr string // non-empty when the JS failed to evaluate (file kept, hooks off)
}

var (
	genUIDir    string     // <config>/lflow/nodes — set in Run; tests point it at a temp dir
	genUITypes  []nodeType // runtime-registered node types, in load order
	genUIByKey  = map[string]nodeType{}
	loadedGenUI []genUINode
)

// initGenUINodes points the registry at <configDir>/lflow/nodes and loads it.
// The first run creates the directory and migrates: any rows in the legacy
// artifacts table are exported as files (the table is never read again); a
// fresh install gets the reference log.js instead.
func initGenUINodes(configDir string, db *database.DB) {
	genUIDir = filepath.Join(configDir, consts.LflowDirName, "nodes")
	if _, err := os.Stat(genUIDir); os.IsNotExist(err) {
		if err := os.MkdirAll(genUIDir, 0o755); err == nil {
			exportLegacyArtifacts(db)
		}
	}
	loadGenUINodes()
}

// exportLegacyArtifacts writes the pre-file artifacts table out as one file
// per row, preserving each row's enabled state in the filename.
func exportLegacyArtifacts(db *database.DB) {
	rows, _ := database.ListArtifacts(db) // no table / no rows → seed below
	wrote := false
	for _, a := range rows {
		name := a.Key + ".js"
		if !a.Enabled {
			name += genUIDisabledExt
		}
		if os.WriteFile(filepath.Join(genUIDir, name), []byte(a.Source), 0o644) == nil {
			wrote = true
		}
	}
	if !wrote {
		_ = os.WriteFile(filepath.Join(genUIDir, "log.js"), []byte(database.SeedLogArtifactSource), 0o644)
	}
}

// loadGenUINodes (re)builds the runtime registry from the nodes directory.
// A broken file never blocks the editor: its row is listed with the error in
// /type and its nodes fall back to bullets.
func loadGenUINodes() {
	if genUIDir == "" {
		return // not initialized (bare test models) — keep the registry as-is
	}
	genUITypes = nil
	genUIByKey = map[string]nodeType{}
	loadedGenUI = nil

	entries, err := os.ReadDir(genUIDir)
	if err != nil {
		return
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, e := range entries {
		name := e.Name()
		enabled := strings.HasSuffix(name, ".js")
		if !enabled && !strings.HasSuffix(name, ".js"+genUIDisabledExt) {
			continue
		}
		key := strings.TrimSuffix(strings.TrimSuffix(name, genUIDisabledExt), ".js")
		if key == "" {
			continue
		}
		n := genUINode{Key: key, Label: titleKey(key), Enabled: enabled}
		if b, err := os.ReadFile(filepath.Join(genUIDir, name)); err != nil {
			n.loadErr = err.Error()
		} else {
			n.Source = string(b)
			if enabled {
				if err := evalGenUINode(key, n.Source); err != nil {
					n.loadErr = err.Error()
				}
			}
		}
		if nt, ok := genUIByKey[key]; ok {
			n.Label = nt.label
		}
		loadedGenUI = append(loadedGenUI, n)
	}
}

// installGenUINode writes <key>.js and hot-loads it — the new type is usable
// in /type immediately. This is the offline mock's install path; a real agent
// writes the file itself and the after-turn reload picks it up.
func installGenUINode(key, source string) error {
	if key == "" || genUIDir == "" {
		return fmt.Errorf("genui: no key or nodes dir")
	}
	_ = os.Remove(filepath.Join(genUIDir, key+".js"+genUIDisabledExt))
	if err := os.WriteFile(filepath.Join(genUIDir, key+".js"), []byte(source), 0o644); err != nil {
		return err
	}
	loadGenUINodes()
	return nil
}

// setGenUINodeEnabled renames <key>.js ↔ <key>.js.disabled — the on/off state
// lives in the filename, visible to anything that can list the directory.
func setGenUINodeEnabled(key string, enabled bool) {
	on := filepath.Join(genUIDir, key+".js")
	if enabled {
		_ = os.Rename(on+genUIDisabledExt, on)
	} else {
		_ = os.Rename(on, on+genUIDisabledExt)
	}
	loadGenUINodes()
}

// deleteGenUINode removes the type's file. Nodes of its type stay untouched
// and render as bullets until the type is reinstalled.
func deleteGenUINode(key string) {
	on := filepath.Join(genUIDir, key+".js")
	_ = os.Remove(on)
	_ = os.Remove(on + genUIDisabledExt)
	loadGenUINodes()
}

// evalGenUINode runs one program in a fresh runtime and registers what it
// declares.
func evalGenUINode(key, source string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("node type %s panicked: %v", key, r)
		}
	}()
	vm := goja.New()
	if err := vm.Set("lflow", lflowAPI(vm)); err != nil {
		return err
	}
	if _, err := vm.RunScript(key, source); err != nil {
		return fmt.Errorf("node type %s: %v", key, err)
	}
	return nil
}

// titleKey is the display label a file gets before (or without) its JS
// registering one: the key with its first rune upper-cased.
func titleKey(key string) string {
	r := []rune(key)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// lflowAPI is the whole JS SDK surface: two registration calls plus the
// helpers hooks lean on (style/time/exec).
func lflowAPI(vm *goja.Runtime) map[string]interface{} {
	return map[string]interface{}{
		"registerType": func(desc *goja.Object) { registerJSType(vm, desc) },
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
func registerJSType(vm *goja.Runtime, desc *goja.Object) {
	key := jsString(desc.Get("key"))
	label := jsString(desc.Get("label"))
	if key == "" || label == "" {
		return
	}
	if _, builtin := byType[key]; builtin {
		return // compiled-in types win; genui extends, never overrides
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
		// persisted — the invariant holds for genui types too).
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

	genUITypes = append(genUITypes, nt)
	genUIByKey[key] = nt
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

// callJS invokes a hook defensively: a throwing or panicking program never
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
