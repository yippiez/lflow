package editor

// GROUP A — modal list picker (the "list above the status bar" family).
//
// SCAFFOLD ONLY: signatures + data structures + TODOs for review. No behavior is
// implemented yet; bodies panic so the package still builds. Once the shapes are
// agreed we migrate the slash menu, /type, /style, /theme, and the inline
// completer onto this one component.
//
// Today each of those five re-implements the same skeleton by hand:
//   - state on Model:  a `<x>Sel int` and (for searchable ones) a `<x>Query string`
//   - a `filtered<X>()` substring filter
//   - a `handle<X>Key` with an identical esc/up/down/enter/backspace/runes switch
//   - a near-verbatim render block (scrollStart window + "▸" mark + clip)
// listPicker owns all of that; each concrete picker only supplies its items and
// what Enter does.

import (
	tea "github.com/charmbracelet/bubbletea"
)

// listPicker is the shared modal picker drawn as a bounded, scrolling list above
// the status bar. It owns the selection index, the optional search query, and
// the scroll window. The item source and the commit action are supplied per use
// via pickerSource.
type listPicker struct {
	sel   int    // highlighted row, index into the *filtered* items
	query string // live search query; always "" for static (A2) pickers

	// searchable splits the family: A1 (slash, /type, completer) filter on typed
	// runes; A2 (/style, /theme) ignore them and just navigate a fixed list.
	searchable bool

	// inlineText mirrors the trigger+query into the node's text as you type (the
	// slash menu and the completer do this so the raw text stays literal on esc).
	// TODO: decide whether this belongs here or stays a wrapper concern — the
	// completer also needs the trigger's start rune index (see complState.start).
	inlineText bool
}

// pickerItem is one row. label is shown; value is the opaque payload handed to
// onSelect; desc is an optional dim hint on the right (used by the completer's
// ":after:" descriptions and the slash menu's command help).
type pickerItem struct {
	label string
	value string
	desc  string
	// TODO: some rows need richer decoration than a string (style swatches for
	// /style and /theme, "(on)" active markers). Consider a `render func() string`
	// escape hatch or a small variant enum instead of forcing everything through
	// label. Revisit when migrating /style + /theme.
}

// pickerSource supplies the rows for a listPicker and commits the chosen one.
// Static pickers return the same slice regardless of query; searchable ones
// apply their own filter (so callers keep control of match semantics — substring
// today, fuzzy later).
type pickerSource interface {
	// items returns the rows to display for the picker's current query.
	// TODO: pass the query explicitly rather than reading p.query, so a source is
	// testable without a live listPicker.
	items(m *Model, p *listPicker) []pickerItem

	// onSelect commits the highlighted row. It may mutate the model (set a type,
	// toggle a style, insert a chip) and returns an optional command.
	onSelect(m *Model, p *listPicker, it pickerItem) (tea.Model, tea.Cmd)

	// header returns an optional line drawn above the list (the /type picker's
	// "type: <query>" search header). "" means no header.
	// TODO: confirm the completer/slash inline-text cases don't also want a header.
	header(m *Model, p *listPicker) string
}

// open resets the picker to a fresh state before it is shown.
// TODO: pre-select the row already "in effect" where relevant (/type pre-selects
// the current type, /style the first active toggle, /theme the active theme).
func (p *listPicker) open(searchable, inlineText bool) {
	panic("TODO: implement listPicker.open")
}

// moveUp / moveDown clamp-and-move the selection within [0, n).
func (p *listPicker) moveUp() {
	panic("TODO: implement listPicker.moveUp")
}

func (p *listPicker) moveDown(n int) {
	panic("TODO: implement listPicker.moveDown")
}

// handleKey is the shared esc/up/down/enter/backspace/runes skeleton every A
// picker duplicates today. Returns whether the key was consumed alongside the
// usual bubbletea pair.
//   - esc:        close (A1 leaves typed text literal; caller decides via inlineText)
//   - up/down:    move selection
//   - enter/tab:  src.onSelect on the highlighted row
//   - backspace:  trim query (searchable) else close; drop trigger when empty
//   - runes/space: append to query (searchable) and reset sel to 0
//
// TODO: reconcile the two backspace behaviors — the completer also deletes the
// mirrored rune from cur.name (delCharBeforeCaret); wire that through inlineText.
// TODO: space is a commit-and-exit for the tag completer but a query char for
// /type and the finder — needs a per-source policy, not a global rule.
func (p *listPicker) handleKey(m *Model, k tea.KeyMsg, src pickerSource) (consumed bool, _ tea.Model, _ tea.Cmd) {
	panic("TODO: implement listPicker.handleKey")
}

// render draws the header (if any) plus a scrollStart-windowed slice of items,
// each with the "▸"/"  " selection mark, and clips to maxLine. This replaces the
// five copy-pasted render blocks in View.
//
// TODO: promote the `pickerMaxRows` const (currently local to View) to a package
// const so both View's layout math and this renderer share one number.
// TODO: re-clamp sel here defensively, as the current blocks do, or guarantee it
// upstream in handleKey and drop the duplicate clamp.
func (p *listPicker) render(m *Model, src pickerSource, maxLine int) []string {
	panic("TODO: implement listPicker.render")
}
