package editor

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/dop251/goja"

	"github.com/lflow/lflow/pkg/tui/database"
)

// This file is the non-rendering half of the NodeMod view SDK: the width-aware
// text kit, the truecolor cell canvas, and the durable per-node data store. The
// rendering/reducer bridge is in nodemod_view.go.

// ── text: width-aware layout so a mod never miscounts terminal cells ─────────

func modTextAPI() map[string]interface{} {
	return map[string]interface{}{
		// width is the display width in terminal cells, ignoring ANSI escapes and
		// counting wide (CJK/emoji) runes as 2 — the number to lay out against.
		"width": func(s string) int { return visibleWidth(s) },
		// truncate clips s to w cells (ANSI-aware), appending an ellipsis if cut.
		"truncate": func(s string, w int) string { return clip(s, w) },
		// pad grows s to exactly w cells; align is "left" (default), "right", or
		// "center". A string already wider than w is returned unchanged.
		"pad": func(s string, w int, align string) string { return modPad(s, w, align) },
		// repeat is display-width-correct string repetition to fill w cells.
		"repeat": func(s string, w int) string { return modRepeat(s, w) },
		// hrule is a full-width rule of ch (e.g. "─") clipped to w cells.
		"hrule": func(ch string, w int) string { return modRepeat(ch, w) },
	}
}

// modPad pads s to w display cells with spaces per alignment.
func modPad(s string, w int, align string) string {
	gap := w - visibleWidth(s)
	if gap <= 0 {
		return s
	}
	switch align {
	case "right":
		return strings.Repeat(" ", gap) + s
	case "center":
		l := gap / 2
		return strings.Repeat(" ", l) + s + strings.Repeat(" ", gap-l)
	default:
		return s + strings.Repeat(" ", gap)
	}
}

// modRepeat repeats s until it fills exactly w display cells (last copy clipped).
func modRepeat(s string, w int) string {
	if w <= 0 || s == "" {
		return ""
	}
	sw := visibleWidth(s)
	if sw == 0 {
		return ""
	}
	var b strings.Builder
	for visibleWidth(b.String()) < w {
		b.WriteString(s)
	}
	return clipStr(b.String(), w)
}

// ── canvas: an absolute truecolor cell grid, the graphics escape hatch ───────

type canvasCell struct {
	ch     rune
	fg, bg string // resolved SGR color codes, or "" for the terminal default
}

// modCanvas returns a fresh w×h cell grid exposed to JS as {set, bands, width,
// height}. set(x,y,ch,fg,bg) paints one cell (fg/bg accept "#rrggbb", a palette
// name, or ""); bands() flattens the grid to styled band strings the view's
// render returns. This is how a mod draws an image (half-blocks), a chart, etc.
func modCanvas(w, h int) map[string]interface{} {
	if w < 0 {
		w = 0
	}
	if h < 0 {
		h = 0
	}
	grid := make([][]canvasCell, h)
	for y := range grid {
		grid[y] = make([]canvasCell, w)
		for x := range grid[y] {
			grid[y][x].ch = ' '
		}
	}
	set := func(x, y int, ch, fg, bg string) {
		if y < 0 || y >= h || x < 0 || x >= w {
			return
		}
		r := ' '
		if rs := []rune(ch); len(rs) > 0 {
			r = rs[0]
		}
		grid[y][x] = canvasCell{ch: r, fg: modColor(fg, false), bg: modColor(bg, true)}
	}
	bands := func() []string {
		out := make([]string, h)
		for y := 0; y < h; y++ {
			var b strings.Builder
			cur := ""
			for x := 0; x < w; x++ {
				c := grid[y][x]
				sgr := c.fg + c.bg
				if sgr != cur {
					b.WriteString(cReset)
					b.WriteString(sgr)
					cur = sgr
				}
				b.WriteRune(c.ch)
			}
			b.WriteString(cReset)
			out[y] = b.String()
		}
		return out
	}
	return map[string]interface{}{
		"set": set, "bands": bands, "width": w, "height": h,
	}
}

// modColor resolves a canvas color: "#rrggbb" → truecolor SGR, a palette/theme
// name → its code, "" → none. bg selects the background SGR (48 vs 38).
func modColor(name string, bg bool) string {
	if name == "" {
		return ""
	}
	if strings.HasPrefix(name, "#") && len(name) == 7 {
		r, e1 := strconv.ParseInt(name[1:3], 16, 0)
		g, e2 := strconv.ParseInt(name[3:5], 16, 0)
		b, e3 := strconv.ParseInt(name[5:7], 16, 0)
		if e1 == nil && e2 == nil && e3 == nil {
			lead := 38
			if bg {
				lead = 48
			}
			return fmt.Sprintf("\x1b[%d;2;%d;%d;%dm", lead, r, g, b)
		}
	}
	// a named color from the /style palette (colorCode gives the fg form); for a
	// background we can't remap the SGR generically, so named colors are fg-only.
	if bg {
		return ""
	}
	return colorCode(name)
}

// ── durable per-node store (node_mod_data), used by lflow.getData/setData ─────

// modDataGet returns the parsed JSON a mod stored for a node, or nil.
func modDataGet(uuid string) interface{} {
	if modDB == nil {
		return nil
	}
	raw, err := database.GetModData(modDB, uuid)
	if err != nil || raw == "" {
		return nil
	}
	var v interface{}
	if json.Unmarshal([]byte(raw), &v) != nil {
		return nil
	}
	return v
}

// modDataSet persists a mod's per-node state as JSON. A null/undefined value
// clears the row.
func modDataSet(uuid string, val goja.Value) {
	if modDB == nil {
		return
	}
	if val == nil || goja.IsUndefined(val) || goja.IsNull(val) {
		_ = database.PutModData(modDB, uuid, "")
		return
	}
	b, err := json.Marshal(val.Export())
	if err != nil {
		return
	}
	_ = database.PutModData(modDB, uuid, string(b))
}
