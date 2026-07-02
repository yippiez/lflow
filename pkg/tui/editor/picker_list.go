package editor

// GROUP A — modal list picker (the "list above the status bar" family).
//
// SCAFFOLD ONLY: signatures + data structures + TODOs for review. Real logic
// panics so the package still builds. Once the shapes are agreed we migrate the
// slash menu, /type, /style, /theme, and the inline completer onto this one
// component.
//
// Today each of those five re-implements the same skeleton by hand:
//   - state on Model:  a `<x>Sel int` and (for searchable ones) a `<x>Query string`
//   - a `filtered<X>()` substring filter
//   - a `handle<X>Key` with an identical esc/up/down/enter/backspace/runes switch
//   - a near-verbatim render block (scrollStart window + "▸" mark + clip)
// listPicker owns all of that; each concrete picker only supplies its items and
// what Enter does.
//
// Design decisions locked in review (see method docs for the resulting shapes):
//   - rows: optional per-item render func (swatches, "(on)", desc hints)
//   - inline-text pickers (slash, completer): opt-in inlineTextSource hooks
//   - open pre-selects the row in effect via pickerSource.initialSel
//   - pickerMaxRows promoted to a package const (below)
//   - items() takes the query explicitly (testable without a live picker)
//   - handleKey is the sole owner of a valid sel; render trusts it
//   - the search header is per-source, opt-in (only /type shows one today)

import (
	tea "github.com/charmbracelet/bubbletea"
)

// pickerMaxRows is the most option rows any picker shows before it scrolls. It
// was a const local to View; promoted here so View's layout math and the shared
// renderer share one number.
// TODO: during migration, delete the `const pickerMaxRows = 8` inside View and
// point that code at this const.
const pickerMaxRows = 8

// listPicker is the shared modal picker drawn as a bounded, scrolling list above
// the status bar. It owns the selection index, the optional search query, and
// the scroll window. The item source and the commit action come from a
// pickerSource; text-mirroring pickers additionally implement inlineTextSource.
type listPicker struct {
	sel   int    // highlighted row, index into the *filtered* items; handleKey keeps it valid
	query string // live search query; always "" for static (A2) pickers

	// searchable splits the family: A1 (slash, /type, completer) filter on typed
	// runes; A2 (/style, /theme) ignore them and just navigate a fixed list.
	searchable bool
}

// pickerItem is one row. label/value/desc cover the plain cases; render, when
// set, fully owns the row's on-screen form (the /style swatch, the /theme
// palette strip + "(on)" marker) and label/desc are ignored.
type pickerItem struct {
	label string
	value string
	desc  string

	// render, if non-nil, returns the fully formatted row (ANSI included) for the
	// given selection state; the component draws it verbatim (after clip). Sources
	// with plain text leave this nil and the component formats label + desc.
	// TODO: keep the "▸ " selection mark owned by the component, or hand it to
	// render too? Leaning component-owned so alignment stays uniform.
	render func(selected bool) string
}

// pickerSource supplies the rows for a listPicker and commits the chosen one.
// items applies its own match semantics (substring today, fuzzy later), so the
// component never filters.
type pickerSource interface {
	// items returns the rows to display for the given query. Query is passed
	// explicitly so a source is testable without a live listPicker.
	items(m *Model, query string) []pickerItem

	// onSelect commits the highlighted row. It may mutate the model (set a type,
	// toggle a style, insert a chip) and returns an optional command.
	onSelect(m *Model, p *listPicker, it pickerItem) (tea.Model, tea.Cmd)

	// header returns an optional line drawn above the list (the /type picker's
	// "type: <query>" search header). "" means no header — slash/completer/style/
	// theme all return "" and look exactly as they do today.
	header(m *Model, p *listPicker) string

	// initialSel returns the row to pre-select when the picker opens (/type → the
	// current type, /style → first active toggle, /theme → active theme). Slash and
	// the completer return 0.
	initialSel(m *Model) int
}

// inlineTextSource is the opt-in policy hook for Group-A pickers that mirror the
// typed trigger+query INTO the node text as you type — the slash menu and the
// tag/query completer. When a pickerSource also satisfies this interface,
// handleKey routes rune/space/backspace edits through it so the node text and
// the popup stay in sync. Static pickers (/type, /style, /theme) don't implement
// it and the shared loop edits only p.query.
type inlineTextSource interface {
	// onRune splices typed runes into the node text at the caret (and the source
	// bumps p.query itself, or the component does — decide during impl).
	// TODO: settle who appends to p.query — component before the hook, or the hook.
	onRune(m *Model, p *listPicker, r []rune)

	// onBackspace deletes the mirrored rune from the node text. It reports whether
	// the picker should now close (the completer closes when it eats its trigger).
	onBackspace(m *Model, p *listPicker) (closePicker bool)

	// onSpace decides what space does: the tag completer commits "#word" into a
	// chip and closes; a query command leaves the text literal and closes; /type
	// treats space as a query char (so /type is NOT an inlineTextSource and never
	// reaches here). Reports whether the picker closes.
	onSpace(m *Model, p *listPicker) (closePicker bool)
}

// open resets the picker and pre-selects the in-effect row via src.initialSel.
func (p *listPicker) open(m *Model, src pickerSource, searchable bool) {
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
// picker duplicates today. It is the SOLE owner of a valid sel: any key that
// changes the item set re-clamps sel here so render can trust it.
//   - esc:        close (A1 leaves typed text literal)
//   - up/down:    move selection, re-clamp
//   - enter/tab:  src.onSelect on the highlighted row
//   - backspace:  inlineTextSource.onBackspace if implemented, else trim p.query;
//                 close when empty
//   - runes:      inlineTextSource.onRune if implemented, else append to p.query
//                 (searchable only); reset sel to 0 and re-clamp
//   - space:      inlineTextSource.onSpace if implemented, else a normal rune
//
// Returns whether the key was consumed alongside the usual bubbletea pair.
func (p *listPicker) handleKey(m *Model, k tea.KeyMsg, src pickerSource) (consumed bool, _ tea.Model, _ tea.Cmd) {
	panic("TODO: implement listPicker.handleKey")
}

// render draws src.header (if any) plus a scrollStart-windowed slice of items —
// each via item.render or the default label+desc formatting, with the "▸"/"  "
// selection mark — clipped to maxLine. Replaces the five copy-pasted blocks in
// View. render trusts sel is in range (handleKey guarantees it).
func (p *listPicker) render(m *Model, src pickerSource, maxLine int) []string {
	panic("TODO: implement listPicker.render")
}
