package editor

import (
	"bytes"
	"encoding/json"
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
	"github.com/lflow/lflow/pkg/tui/tag"
)

// NodeMods are runtime-installed node types (and chip kinds) — "mod" in the
// UI. One mod per entry in <config>/lflow/mods: either a flat <key>.js (hand-
// or agent-written) or a <key>/ directory installed from git ("lflow node
// install <url>") whose mod.json names the entry JS. A ".disabled" suffix on
// either form turns the mod off without losing it. The directory is evaluated
// at editor start via goja and reloaded when /type opens and after every
// agent turn — an agent (or the user) edits the files directly and the
// running editor picks the change up. Each program runs in its own goja
// runtime and calls lflow.registerType / lflow.registerChip; the bridge turns
// those into regular nodeType descriptors appended to the compiled-in
// registry, so the picker, glyphs and rendering treat both kinds identically.
// See AGENTS.md.
//
// WARNING (invariant): a mod is a file, never schema — and a node OF a mod
// type is a normal node row whose type is a free string. Removing the mod
// leaves its nodes rendering as plain bullets (text intact, via typeOf's
// fallback); reinstalling lights them back up. Installing a mod is a file
// write, never a DB migration.
//
// NodeMod JS is TRUSTED — this is a single-user local tool, so hooks may call
// lflow.exec (synchronous shell) even on the render path; a slow hook slows
// the frame and that is the mod's own problem. All hooks run on the bubbletea
// goroutine, so no locking is needed anywhere in the bridge.

const modDisabledExt = ".disabled"

// modManifest is a directory mod's mod.json.
type modManifest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Entry       string `json:"entry"`
	Version     string `json:"version"`
}

// nodeMod pairs one mods-dir entry with its load state for the /type rows.
type nodeMod struct {
	Key     string // the nodes.type it serves — the filename, or mod.json's name
	Label   string // the registered label (the key, title-cased, until eval)
	Source  string
	Enabled bool
	path    string // the file or directory behind it — the rename/delete target
	loadErr string // non-empty when the JS failed to evaluate (files kept, hooks off)
}

var (
	modsDir    string     // <config>/lflow/mods — set in Run; tests point it at a temp dir
	modTypes   []nodeType // runtime-registered node types, in load order
	modByKey   = map[string]nodeType{}
	loadedMods []nodeMod
)

// initNodeMods points the registry at <configDir>/lflow/mods and loads it.
// The first run migrates, oldest form first: the pre-rename "nodes" dir moves
// wholesale; failing that, legacy artifacts-table rows export as files. A fresh
// install starts empty — every node type is an external mod (lflow node install).
func initNodeMods(configDir string, db *database.DB) {
	base := filepath.Join(configDir, consts.LflowDirName)
	modsDir = filepath.Join(base, "mods")
	tag.SetModsDir(modsDir) // pi's system prompt points at the same dir
	if _, err := os.Stat(modsDir); os.IsNotExist(err) {
		if fi, err := os.Stat(filepath.Join(base, "nodes")); err == nil && fi.IsDir() {
			_ = os.Rename(filepath.Join(base, "nodes"), modsDir)
		}
	}
	if _, err := os.Stat(modsDir); os.IsNotExist(err) {
		if err := os.MkdirAll(modsDir, 0o755); err == nil {
			exportLegacyArtifacts(db)
		}
	}
	loadNodeMods()
}

// exportLegacyArtifacts writes the pre-file artifacts table out as one file
// per row, preserving each row's enabled state in the filename. A DB with no
// such rows leaves the mods dir empty — fresh installs ship no built-in mods.
func exportLegacyArtifacts(db *database.DB) {
	rows, _ := database.ListArtifacts(db) // no table / no rows → nothing to export
	for _, a := range rows {
		name := a.Key + ".js"
		if !a.Enabled {
			name += modDisabledExt
		}
		_ = os.WriteFile(filepath.Join(modsDir, name), []byte(a.Source), 0o644)
	}
}

// loadNodeMods (re)builds the runtime registry from the mods directory.
// A broken mod never blocks the editor: its row is listed with the error in
// /type and its nodes fall back to bullets.
func loadNodeMods() {
	if modsDir == "" {
		return // not initialized (bare test models) — keep the registry as-is
	}
	modTypes = nil
	modByKey = map[string]nodeType{}
	loadedMods = nil

	entries, err := os.ReadDir(modsDir)
	if err != nil {
		return
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue // dotfiles and half-finished install stages
		}
		enabled := !strings.HasSuffix(name, modDisabledExt)
		base := strings.TrimSuffix(name, modDisabledExt)
		path := filepath.Join(modsDir, name)

		var n nodeMod
		if e.IsDir() {
			n = loadDirMod(base, path)
		} else {
			key := strings.TrimSuffix(base, ".js")
			if key == "" || key == base { // not a .js file
				continue
			}
			n = nodeMod{Key: key, Label: titleKey(key), path: path}
			if b, err := os.ReadFile(path); err != nil {
				n.loadErr = err.Error()
			} else {
				n.Source = string(b)
			}
		}
		if n.Key == "" {
			continue
		}
		n.Enabled = enabled
		if enabled && n.loadErr == "" {
			if err := evalNodeMod(n.Key, n.Source); err != nil {
				n.loadErr = err.Error()
			}
		}
		if nt, ok := modByKey[n.Key]; ok {
			n.Label = nt.label
		}
		loadedMods = append(loadedMods, n)
	}
}

// loadDirMod reads a git-installed mod: <dir>/mod.json names the entry JS.
func loadDirMod(base, path string) nodeMod {
	n := nodeMod{Key: base, Label: titleKey(base), path: path}
	b, err := os.ReadFile(filepath.Join(path, "mod.json"))
	if err != nil {
		n.loadErr = "mod.json: " + err.Error()
		return n
	}
	var mf modManifest
	if err := json.Unmarshal(b, &mf); err != nil {
		n.loadErr = "mod.json: " + err.Error()
		return n
	}
	if mf.Name != "" {
		n.Key = mf.Name
		n.Label = titleKey(mf.Name)
	}
	if mf.Entry == "" {
		n.loadErr = "mod.json: no entry"
		return n
	}
	src, err := os.ReadFile(filepath.Join(path, mf.Entry))
	if err != nil {
		n.loadErr = err.Error()
		return n
	}
	n.Source = string(src)
	return n
}

// installNodeMod writes <key>.js and hot-loads it — the new type is usable
// in /type immediately. This is the offline mock's install path; a real agent
// writes the file itself and the after-turn reload picks it up.
func installNodeMod(key, source string) error {
	if key == "" || modsDir == "" {
		return fmt.Errorf("nodemod: no key or mods dir")
	}
	_ = os.Remove(filepath.Join(modsDir, key+".js"+modDisabledExt))
	if err := os.WriteFile(filepath.Join(modsDir, key+".js"), []byte(source), 0o644); err != nil {
		return err
	}
	loadNodeMods()
	return nil
}

// setNodeModEnabled toggles a mod by renaming its file or directory with the
// .disabled suffix — the on/off state lives in the filename, visible to
// anything that can list the directory.
func setNodeModEnabled(key string, enabled bool) {
	for _, mod := range loadedMods {
		if mod.Key != key {
			continue
		}
		if enabled {
			_ = os.Rename(mod.path, strings.TrimSuffix(mod.path, modDisabledExt))
		} else if !strings.HasSuffix(mod.path, modDisabledExt) {
			_ = os.Rename(mod.path, mod.path+modDisabledExt)
		}
		break
	}
	loadNodeMods()
}

// deleteNodeMod removes the mod's file or directory. Nodes of its type stay
// untouched and render as bullets until the mod is reinstalled.
func deleteNodeMod(key string) {
	for _, mod := range loadedMods {
		if mod.Key == key {
			_ = os.RemoveAll(mod.path)
			break
		}
	}
	loadNodeMods()
}

// evalNodeMod runs one program in a fresh runtime and registers what it
// declares.
func evalNodeMod(key, source string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("mod %s panicked: %v", key, r)
		}
	}()
	vm := goja.New()
	if err := vm.Set("lflow", lflowAPI(vm)); err != nil {
		return err
	}
	if _, err := vm.RunScript(key, source); err != nil {
		return fmt.Errorf("mod %s: %v", key, err)
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

	modTypes = append(modTypes, nt)
	modByKey[key] = nt
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
