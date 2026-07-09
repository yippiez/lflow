package editor

// GROUP A — the shared modal list picker (the "list above the status bar"
// family): the slash menu, /type, /style, /theme, and the inline "#"/":"
// completer. Each concrete picker is a pickerSource; the picker state (selection
// + query + scroll window) and the esc/up/down/enter/backspace/runes skeleton
// live here, once, instead of five hand-rolled copies.
//
// Only one Group-A picker is ever active at a time (mode is exclusive), so the
// Model keeps a single `list listPicker`. Text-mirroring pickers (slash,
// completer) keep their own anchor state on the Model (slashStart/slashInline,
// compl.kind/compl.start) and opt into inlineTextSource.

import (
	tea "github.com/charmbracelet/bubbletea"
)

// pickerMaxRows is the most option rows any picker shows before it scrolls. It
// was a const local to View; promoted here so View's layout math and the shared
// renderer share one number.
const pickerMaxRows = 8

// listPicker owns the selection index, the optional search query, and the scroll
// window for whichever Group-A picker is open.
type listPicker struct {
	sel   int    // highlighted row, index into the filtered items; handleKey keeps it valid
	query string // live search query; always "" for static (non-searchable) pickers

	// searchable splits the family: slash, /type, and the completer filter on typed
	// runes; /style and /theme ignore them and just navigate a fixed list.
	searchable bool
}

// pickerItem is one row. label/value/desc cover the plain cases; render, when
// set, fully owns the row's on-screen form after the selection mark (the /style
// swatch, the /theme palette strip + "(on)" marker) and label/desc are ignored.
type pickerItem struct {
	label string
	value string
	desc  string

	// render, if non-nil, returns the formatted row content (ANSI included, minus
	// the leading " " + selection mark, which the component always prepends) for
	// the given selection state. Plain sources leave it nil.
	render func(selected bool) string
}

// pickerSource supplies the rows for a listPicker and commits the chosen one.
// items applies its own match semantics, so the component never filters.
type pickerSource interface {
	// items returns the rows to display for the given query, passed explicitly so
	// a source is testable without a live listPicker.
	items(m *Model, query string) []pickerItem

	// onSelect commits the chosen row (a zero pickerItem when nothing valid is
	// highlighted). It owns the resulting mode: static pickers set modeOutline; the
	// slash menu hands off to runSlash, which may open another mode.
	onSelect(m *Model, it pickerItem) (tea.Model, tea.Cmd)

	// header returns an optional line drawn above the list (the /type picker's
	// "type: <query>" search header). "" means no header.
	header(m *Model, p *listPicker) string

	// initialSel returns the row to pre-select when the picker opens.
	initialSel(m *Model) int
}

// inlineTextSource is the opt-in hook for pickers that mirror the typed
// trigger+query INTO the node text as you type — the slash menu and the
// tag/query completer. When a pickerSource also satisfies this interface,
// handleKey routes rune/space/backspace through it (so node text and popup stay
// in sync) and honors its "close now" verdict. Static pickers don't implement it.
// pickerKeySource lets a source claim extra keys (management chords like the
// /type picker's space-toggle and ctrl+d on artifact rows) before the shared
// space/rune handling runs.
type pickerKeySource interface {
	onKey(m *Model, p *listPicker, key string, items []pickerItem) (handled bool)
}

type inlineTextSource interface {
	// onRune handles a typed rune: append to p.query, splice into the node text,
	// and report whether the picker should now close (the slash menu closes when
	// nothing matches).
	onRune(m *Model, p *listPicker, r []rune) (closePicker bool)
	// onSpace handles space (the tag completer commits "#word" and closes; the
	// slash menu treats it as an ordinary query rune).
	onSpace(m *Model, p *listPicker) (closePicker bool)
	// onBackspace deletes the mirrored rune; reports close when it eats the trigger.
	onBackspace(m *Model, p *listPicker) (closePicker bool)
}

// open resets the picker and pre-selects the in-effect row via src.initialSel.
func (p *listPicker) open(m *Model, src pickerSource, searchable bool) {
	p.query = ""
	p.searchable = searchable
	p.sel = src.initialSel(m)
}

// moveUp / moveDown clamp-and-move the selection within [0, n).
func (p *listPicker) moveUp() {
	if p.sel > 0 {
		p.sel--
	}
}

func (p *listPicker) moveDown(n int) {
	if p.sel < n-1 {
		p.sel++
	}
}

// handleKey is the shared esc/up/down/enter/backspace/runes skeleton. It is the
// sole owner of a valid sel: any key that changes the item set resets/clamps sel
// so render can trust it. Tab is accepted as a second select key across the
// family (previously only the completer honored it). Returns whether the key was
// consumed alongside the usual bubbletea pair.
func (p *listPicker) handleKey(m *Model, k tea.KeyMsg, src pickerSource) (consumed bool, _ tea.Model, _ tea.Cmd) {
	inline, _ := src.(inlineTextSource)
	items := src.items(m, p.query)

	switch k.String() {
	case "esc":
		m.mode = modeOutline
		return true, m, nil
	case "up":
		p.moveUp()
		return true, m, nil
	case "down":
		p.moveDown(len(items))
		return true, m, nil
	case "enter", "tab":
		var it pickerItem
		if p.sel >= 0 && p.sel < len(items) {
			it = items[p.sel]
		}
		mm, cmd := src.onSelect(m, it)
		return true, mm, cmd
	case "backspace":
		if inline != nil {
			if inline.onBackspace(m, p) {
				m.mode = modeOutline
			}
		} else if p.searchable {
			if qr := []rune(p.query); len(qr) > 0 {
				p.query = string(qr[:len(qr)-1])
				p.sel = 0
			} else {
				m.mode = modeOutline
			}
		}
		return true, m, nil
	}

	if ks, ok := src.(pickerKeySource); ok {
		key := k.String()
		if k.Type == tea.KeySpace && !k.Alt {
			key = " "
		}
		if ks.onKey(m, p, key, items) {
			return true, m, nil
		}
	}

	if k.Type == tea.KeySpace && !k.Alt {
		if inline != nil {
			if inline.onSpace(m, p) {
				m.mode = modeOutline
			}
		} else if p.searchable {
			p.query += " "
			p.sel = 0
		}
		return true, m, nil
	}
	if k.Type == tea.KeyRunes && !k.Alt {
		if inline != nil {
			if inline.onRune(m, p, k.Runes) {
				m.mode = modeOutline
			}
		} else if p.searchable {
			p.query += string(k.Runes)
			p.sel = 0
		}
		return true, m, nil
	}
	return true, m, nil
}

// counts reports the number of item rows and header rows the picker will draw,
// for View's body-budget math.
func (p *listPicker) counts(m *Model, src pickerSource) (items, header int) {
	items = len(src.items(m, p.query))
	if src.header(m, p) != "" {
		header = 1
	}
	return items, header
}

// render draws src.header (if any) plus a scrollStart-windowed slice of items —
// each via item.render or the default label+desc formatting, with the "→"/"  "
// selection mark — clipped to maxLine. Replaces the five copy-pasted blocks in
// View; trusts sel is in range (handleKey guarantees it).
func (p *listPicker) render(m *Model, src pickerSource, maxLine int) []string {
	var lines []string
	if h := src.header(m, p); h != "" {
		lines = append(lines, clip(h, maxLine))
	}
	items := src.items(m, p.query)
	if len(items) == 0 {
		return lines
	}
	win := pickerMaxRows
	s := scrollStart(p.sel, len(items), win)
	e := s + win
	if e > len(items) {
		e = len(items)
	}
	for i := s; i < e; i++ {
		mark := "  "
		if i == p.sel {
			mark = cAccent + "→ " + cReset
		}
		var content string
		if items[i].render != nil {
			content = items[i].render(i == p.sel)
		} else {
			content = cFG + items[i].label + cReset
			if items[i].desc != "" {
				content += cDim + "  " + items[i].desc + cReset
			}
		}
		lines = append(lines, clip(" "+mark+content, maxLine))
	}
	return lines
}
