package editor

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/lflow/lflow/tui/database"
)

// The paste parser: ONE reader for text arriving from anywhere — a browser, a
// Notion page, Slack, an email client, `tree` output, a spreadsheet, another
// lflow. It turns that text into the same neutral SrcNode forest the file
// codecs speak (see codec.go), so a paste lands as a TYPED TREE instead of a
// stack of rows whose names begin with spaces and bullet glyphs.
//
// WHY PLAIN UNICODE AND NOT A PRIVATE CLIPBOARD FORMAT: a custom text/html or
// XML flavor would round-trip lflow→lflow perfectly and be worthless
// everywhere else — most of what people paste was never copied out of lflow,
// and most places they paste lflow's copy (a terminal, a chat box, a commit
// message) carry one flavor only. So the wire format is the plain text every
// program already produces, and the intelligence lives HERE, in a parser that
// assumes its input is broken. Nothing in this file may reject input: every
// unrecognized shape degrades to a bullet, never to an error.
//
// The signals it reads, in the order the classifier tries them:
//
//	```lang … ```  ~~~ …        → code (json body → a json node)
//	$$ … $$   \[ … \]           → math
//	| a | b |   │ a │ b │       → table (columns are children)
//	a<TAB>b runs                → table (a spreadsheet paste)
//	# / ## / ###…, Title + ===  → h1/h2/h3
//	--- *** ═══ , --- text ---  → divider
//	> quoted, >> nested         → quote
//	<!-- c -->                  → comment (lflow markers restore their type)
//	- [ ] / ☐ / □ / - [x] / ☑   → todo, checked or not
//	- • ◦ ▪ ‣ ○ ● * + – 1. a) i) → bullets, marker stripped
//	├── └── │                   → bullets, the rail read as depth
//	$ cmd   ❯ cmd   ➜ cmd       → bash
//	anything else               → bullets
//
// Depth comes from indentation first (tabs count 4 columns, the whole block's
// common margin is dropped), and from the BULLET GLYPH when indentation says
// nothing — Word and Notion paste a nested list as • / ◦ / ▪ with every line
// flush left, and that progression is the only structure left in the text.

// parsePaste reads pasted text as a node forest. It never fails: text it
// cannot classify comes back as bullets, and empty input as no nodes at all.
func parsePaste(raw string) []*SrcNode {
	text := normalizePaste(raw)
	if strings.TrimSpace(text) == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	// three whole-paste shapes are decided before line-by-line reading, because
	// their lines mean nothing apart: a JSON document, a patch, fence-less code
	if doc, ok := pasteWholeJSON(text); ok {
		return doc
	}
	if doc, ok := pasteWholeDiff(lines); ok {
		return doc
	}
	if doc, ok := pasteWholeCode(lines); ok {
		return doc
	}
	p := &pasteParser{lines: lines, base: pasteBaseIndent(lines), root: &SrcNode{}}
	p.secs = []pasteSec{{n: p.root}}
	p.run()
	return p.root.Kids
}

// ── normalization ───────────────────────────────────────────────────────────

// ansiSeqRe matches a WHOLE escape sequence — CSI (colors, cursor moves, the
// bracketed-paste markers), OSC (window titles, the hyperlinks a terminal copy
// carries) and the two-byte escapes. sanitizeName drops lone control BYTES and
// so leaves the printable tail of a sequence behind ("\x1b[H" → "[H"); text
// scraped off a colored terminal is mostly escapes, so the paste path removes
// them whole first and lets sanitizeName be the net that catches the rest.
var ansiSeqRe = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)|\x1b[@-Z\\\\-_]")

// normalizePaste flattens the byte-level noise every real paste carries: escape
// sequences, the CR/LF zoo (tmux ONLCR turns \r\n into \r\r\n), the unicode
// line separators word processors emit, the invisible characters that survive a
// copy out of a web page, and the exotic spaces PDFs and Word use for
// indentation. The chip sentinel goes too — a U+FFFC arriving from outside
// names no chip record here and would render as a ghost anchor.
func normalizePaste(s string) string {
	s = ansiSeqRe.ReplaceAllString(s, "")
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\u200b', r == '\u200c', r == '\u200d', r == '\u2060',
			r == '\ufeff', r == '\u00ad', r == database.ChipSentinel:
			return -1 // zero-width joiners, soft hyphen, BOM, chip anchor
		case r == '\u2028', r == '\u2029', r == '\u0085':
			return '\n' // LS / PS / NEL: line breaks in disguise
		case r == '\u00a0', r == '\u202f', r == '\u3000', r >= '\u2000' && r <= '\u200a':
			return ' ' // NBSP and the typographic spaces, so the indent math sees them
		}
		return r
	}, s)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// pasteBaseIndent is the common left margin to drop — text copied out of an
// indented block (a docstring, a reply-quoted mail, a nested list) arrives with
// every line pushed right, and without this the whole paste would land one
// level deeper than it reads.
func pasteBaseIndent(lines []string) int {
	base := -1
	for _, l := range lines {
		w, trimmed := indentWidth(l)
		if trimmed == "" {
			continue
		}
		if base < 0 || w < base {
			base = w
		}
	}
	return max(base, 0)
}

// pasteClean finalizes one line's text: interior tabs become spaces (a tab
// inside a name is invisible in the outline and sanitizeName would delete it
// outright) and the control-byte net runs last.
func pasteClean(s string) string {
	return strings.TrimRight(sanitizeName(strings.ReplaceAll(s, "\t", " ")), " ")
}

// pasteBreakRe matches a run of line breaks in any of their spellings.
var pasteBreakRe = regexp.MustCompile("[\r\n\u2028\u2029\u0085]+")

// pasteFlat is the text a ONE-LINE field takes — the /note editor, the link
// editor. Line breaks become single spaces instead of being deleted outright:
// sanitizeName drops \n as a control byte, which welded the last word of each
// pasted line onto the first of the next. It trims nothing, because it is on
// the path of every typed character too and a typed space is a space.
func pasteFlat(s string) string {
	s = pasteBreakRe.ReplaceAllString(normalizePaste(s), " ")
	return sanitizeName(strings.ReplaceAll(s, "\t", " "))
}

// pasteFirstLine is the paste's first line of actual content, markers and all —
// what an insert at the caret should read, since a caret in the middle of a
// name has no way to wear a type.
func pasteFirstLine(s string) string {
	for _, l := range strings.Split(normalizePaste(s), "\n") {
		if _, trimmed := indentWidth(l); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// pasteBlock is the paste a MULTI-LINE buffer takes — the code and json views,
// where newlines and indentation are the content. Only the escape noise and the
// stray control bytes come out; \n and \t stay exactly where they were.
func pasteBlock(s string) string {
	return strings.Map(func(r rune) rune {
		if (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f {
			return -1
		}
		return r
	}, normalizePaste(s))
}

// ── the tree builder ────────────────────────────────────────────────────────

// pasteSec is an open heading: everything that follows hangs under it until a
// heading of the same or a shallower level closes it.
type pasteSec struct {
	n  *SrcNode
	lv int
}

// pasteLevel is an open list item — a candidate parent for the lines below it,
// remembered with BOTH depth signals so the next row can be compared against it.
type pasteLevel struct {
	n      *SrcNode
	indent int
	rank   int
}

type pasteParser struct {
	lines []string
	base  int
	root  *SrcNode
	secs  []pasteSec
	list  []pasteLevel
}

func (p *pasteParser) section() *SrcNode { return p.secs[len(p.secs)-1].n }

// parentFor pops the open-item chain until its top is strictly shallower than
// (indent, rank) and returns what the row should hang under. Comparison is
// lexicographic on the two signals: deeper indentation always wins, and an
// equal indent with a deeper bullet glyph nests too, which is the only thing
// that saves a flush-left • / ◦ / ▪ list.
func (p *pasteParser) parentFor(indent, rank int) *SrcNode {
	for len(p.list) > 0 {
		top := p.list[len(p.list)-1]
		if indent > top.indent || (indent == top.indent && rank > top.rank) {
			break
		}
		p.list = p.list[:len(p.list)-1]
	}
	if len(p.list) > 0 {
		return p.list[len(p.list)-1].n
	}
	return p.section()
}

// add hangs a node at (indent, rank). Rows that can hold children of their own
// (list items, quotes, plain lines) stay open; a block (code, table, divider)
// is closed the moment it lands — the lines after it belong to whatever was
// open before, not inside the block.
func (p *pasteParser) add(indent, rank int, n *SrcNode, open bool) {
	p.parentFor(indent, rank).Kid(n)
	if open {
		p.list = append(p.list, pasteLevel{n: n, indent: indent, rank: rank})
	}
}

// addHeading closes the heading chain down to this level and opens a section.
// A heading also ends every open list: a list that survived a heading would
// swallow the next section's content.
func (p *pasteParser) addHeading(lv int, n *SrcNode) {
	for len(p.secs) > 1 && p.secs[len(p.secs)-1].lv >= lv {
		p.secs = p.secs[:len(p.secs)-1]
	}
	p.section().Kid(n)
	p.secs = append(p.secs, pasteSec{n: n, lv: lv})
	p.list = nil
}

// run walks the lines, letting the multi-line block readers consume what they
// own and classifying everything else one row at a time.
func (p *pasteParser) run() {
	for i := 0; i < len(p.lines); i++ {
		line := p.lines[i]
		indent, trimmed := indentWidth(line)
		indent = max(indent-p.base, 0)
		if trimmed == "" {
			// blank lines are separators, never nodes, and they do NOT close the
			// open items: a list with air between its rows is still one list
			continue
		}
		switch {
		case pasteFenceOpen(trimmed) != "":
			i = p.readFence(i, indent, trimmed)
		case pasteMathOpen(trimmed) != "":
			i = p.readMath(i, indent, trimmed)
		case pasteIsTableRow(trimmed):
			i = p.readTable(i, indent)
		case pasteIsTableRule(trimmed) && i+1 < len(p.lines) &&
			pasteIsTableRow(strings.TrimSpace(p.lines[i+1])):
			// the lid of a box-drawn grid: the table starts one line above its
			// first row (└──┴──┘ closes it, and readTable eats that too)
			i = p.readTable(i, indent)
		case pasteIsTSVRow(line):
			if end, ok := p.readTSV(i, indent); ok {
				i = end
				continue
			}
			p.addRow(indent, trimmed, i)
		default:
			p.addRow(indent, trimmed, i)
		}
	}
}

// addRow classifies one line and hangs it. The setext lookahead lives here
// because it is the one shape that needs the NEXT line to decide the current
// one: a plain line underlined with "===" is a heading, not a line and a rule.
func (p *pasteParser) addRow(indent int, trimmed string, i int) {
	r := classifyPasteRow(trimmed)
	if r.head == 0 && r.n.Type == "" && i+1 < len(p.lines) && setextRe.MatchString(strings.TrimSpace(p.lines[i+1])) {
		r.head, r.n.Type = 1, database.TypeH1
		p.lines[i+1] = "" // the rule is consumed by the heading it underlines
	}
	if r.n.Text == "" && !pasteTextless[r.n.Type] {
		// the line sanitized down to nothing (a run of control bytes, a stray
		// marker with no text behind it): no node, so a paste never leaves a
		// nameless ghost row between two real ones
		return
	}
	if r.head > 0 {
		p.addHeading(r.head, r.n)
		return
	}
	p.add(indent+r.indent, r.rank, r.n, r.open)
}

// pasteTextless is the set of types that mean something with no text of their
// own — a rule, a grid, a fenced block.
var pasteTextless = map[string]bool{
	database.TypeDivider: true, database.TypeTable: true,
	database.TypeCode: true, database.TypeJSON: true, database.TypeMath: true,
}

// setextRe is the "===" underline of a setext heading. The "---" underline is
// deliberately NOT read this way: in text that was never a tidy markdown file,
// a row of dashes between two lines is far more often a divider, and guessing
// heading there would silently eat a rule the writer meant.
var setextRe = regexp.MustCompile(`^={2,}$`)

// ── single-row classification ───────────────────────────────────────────────

// pasteRow is one classified line: the node it becomes, plus how deep it sits.
type pasteRow struct {
	n      *SrcNode
	indent int  // extra columns the row's own markers imply (quote nesting, tree rails)
	rank   int  // bullet-glyph depth, the tiebreak when indentation is flat
	head   int  // heading level 1..3, 0 when the row is not a heading
	open   bool // may hold the following deeper rows as children
}

// classifyPasteRow reads ONE already-trimmed line. Order matters: the shapes
// that would be mistaken for a bullet (headings, rules, quotes, prompts) are
// tried before the bullet markers, and the bullet strip runs before the
// checkbox one so "- [x] done" and a bare "☑ done" reach the same place.
func classifyPasteRow(s string) pasteRow {
	if lv, text, ok := pasteHeading(s); ok {
		return pasteRow{n: &SrcNode{Type: headingTypes[lv], Text: pasteClean(text)}, head: lv}
	}
	if text, ok := pasteDivider(s); ok {
		return pasteRow{n: &SrcNode{Type: database.TypeDivider, Text: pasteClean(text)}}
	}
	if text, lv, ok := pasteQuote(s); ok {
		return pasteRow{n: &SrcNode{Type: database.TypeQuote, Text: pasteClean(text)},
			indent: 2 * (lv - 1), open: true}
	}
	if n, ok := pasteComment(s); ok {
		return pasteRow{n: n, open: true}
	}
	if cmd, ok := pasteShellPrompt(s); ok {
		return pasteRow{n: &SrcNode{Type: database.TypeBash, Text: pasteClean(cmd)}, open: true}
	}
	// the tree-drawing rail of `tree`, `git log --graph`, an npm dependency
	// dump: the glyphs ARE the indentation, so they are charged as columns
	extra, rest := stripTreeRail(s)
	if extra > 0 {
		r := classifyPasteRow(rest)
		r.indent += extra
		return r
	}
	// "- item", "• item", "1. item", "a) item" are all the same item; only the
	// glyph markers carry a depth rank, a number carries none
	rank, rest, listed := stripBullet(s)
	if !listed {
		if stripped := stripOrdered(s); stripped != s {
			rest, listed = stripped, true
		}
	}
	if listed {
		if text, done, boxed := stripCheckbox(rest); boxed {
			return pasteRow{n: &SrcNode{Type: database.TypeTodo, Text: pasteClean(text), Completed: done},
				rank: rank, open: true}
		}
		if text, done := stripStrikethrough(rest); done {
			// a struck-through list item is a finished one — Slack, Notion and
			// half the world's meeting notes cross a task out instead of ticking it
			return pasteRow{n: &SrcNode{Type: database.TypeTodo, Text: pasteClean(text), Completed: true},
				rank: rank, open: true}
		}
		return pasteRow{n: &SrcNode{Type: database.TypeBullets, Text: pasteClean(rest)},
			rank: rank, open: true}
	}
	// an unbulleted checkbox is still a todo — Things, Reminders and every
	// screenshot of lflow's own outline paste their boxes with no marker
	if text, done, boxed := stripCheckbox(s); boxed {
		return pasteRow{n: &SrcNode{Type: database.TypeTodo, Text: pasteClean(text), Completed: done}, open: true}
	}
	return pasteRow{n: &SrcNode{Text: pasteClean(s)}, open: true}
}

// pasteHeading reads an ATX heading. Levels past three collapse onto h3 (lflow
// has three), and the closing hashes of "## Title ##" come off.
func pasteHeading(s string) (int, string, bool) {
	n := 0
	for n < len(s) && s[n] == '#' {
		n++
	}
	if n == 0 || n >= len(s) || (s[n] != ' ' && s[n] != '\t') {
		return 0, "", false // "#tag" is a tag, not a heading
	}
	text := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(s[n:]), "#"))
	return min(n, 3), text, true
}

// dividerRe matches a rule of any of the characters that draw one, spaced or
// not: --- , *** , ___ , ═══ , ─────, - - -.
var dividerRe = regexp.MustCompile(`^[-*_=─━═–—][ \t]*(?:[-*_=─━═–—][ \t]*){2,}$`)

// titledDividerRe matches a rule with a caption in it — "──── notes ────" —
// which is how a rule with text is written in plain text (and how lflow's own
// copy renders a named divider, so it comes home as one).
var titledDividerRe = regexp.MustCompile(`^[-*_=─━═–—]{3,}[ \t]+(.+?)[ \t]+[-*_=─━═–—]{3,}$`)

func pasteDivider(s string) (string, bool) {
	if m := titledDividerRe.FindStringSubmatch(s); m != nil {
		return m[1], true
	}
	if dividerRe.MatchString(s) {
		return "", true
	}
	return "", false
}

// pasteQuote reads a mail/markdown block quote, returning its nesting level so
// ">> reply inside a reply" lands under "> reply".
func pasteQuote(s string) (string, int, bool) {
	lv := 0
	for {
		rest, ok := strings.CutPrefix(s, ">")
		if !ok {
			break
		}
		lv++
		s = strings.TrimPrefix(rest, " ")
	}
	if lv == 0 {
		return "", 0, false
	}
	return s, lv, true
}

// pasteComment reads an HTML comment. lflow's own marker comments (what the
// markdown codec writes for a type markdown cannot spell) are decoded through
// the codec's classifier, so an exotic type survives a copy through a .md file
// and back.
func pasteComment(s string) (*SrcNode, bool) {
	body, ok := strings.CutPrefix(s, "<!--")
	if !ok {
		return nil, false
	}
	body, ok = strings.CutSuffix(body, "-->")
	if !ok {
		return nil, false
	}
	body = strings.TrimSpace(body)
	if n := classifyMarkerBody(body, false); n != nil {
		n.Text = pasteClean(n.Text)
		return n, true
	}
	return &SrcNode{Type: database.TypeComment, Text: pasteClean(body)}, true
}

// shellPrompts are the prompt leads a copied terminal line carries. "%" and
// ">" are left out on purpose: they open far too much ordinary prose (and ">"
// is the quote marker). "▶" stays out too — it is a bullet in these ranks.
var shellPrompts = []string{"$ ", "❯ ", "➜ "}

func pasteShellPrompt(s string) (string, bool) {
	for _, p := range shellPrompts {
		if cmd, ok := strings.CutPrefix(s, p); ok && strings.TrimSpace(cmd) != "" {
			return strings.TrimSpace(cmd), true
		}
	}
	return "", false
}

// bulletRanks maps a list glyph to the depth it implies. The ranks encode the
// progression Word, Google Docs and Notion use when they flatten a nested list
// into text — • at the top, ◦ under it, ▪ under that — and are consulted only
// when indentation cannot separate two rows. lflow's own screen glyphs (○ for
// a node, ● for a collapsed one) read as bullets too, so a paste scraped
// straight off the terminal comes back as an outline.
var bulletRanks = map[rune]int{
	'-': 0, '*': 0, '+': 0, '•': 0, '●': 0, '·': 0, '‒': 0, '–': 0, '—': 0,
	'▸': 0, '▶': 0, '➤': 0, '➔': 0, '»': 0, '⁌': 0,
	'◦': 1, '○': 1, '‣': 1, '∙': 1, '▹': 1, '⁍': 1,
	'▪': 2, '▫': 2, '⁃': 2, '◾': 2, '▬': 2,
}

// asciiBullets are the markers that MUST be followed by a space to count: "-",
// "*" and "+" open too much text ("-5°C", "*bold*", "+1") to be read as
// bullets on their own.
const asciiBullets = "-*+"

// stripBullet takes the list marker off a line, reporting the depth its glyph
// implies. It refuses a marker with nothing after it (a bare "-" is a rule or
// a shrug, not an empty list item).
func stripBullet(s string) (int, string, bool) {
	r := []rune(s)
	if len(r) == 0 {
		return 0, s, false
	}
	rank, ok := bulletRanks[r[0]]
	if !ok {
		return 0, s, false
	}
	rest := string(r[1:])
	spaced := strings.HasPrefix(rest, " ") || strings.HasPrefix(rest, "\t")
	if strings.ContainsRune(asciiBullets, r[0]) && !spaced {
		return 0, s, false
	}
	rest = strings.TrimLeft(rest, " \t")
	if rest == "" {
		return 0, s, false
	}
	return rank, rest, true
}

// orderedRe matches an ordered-list lead: "1." "2)" "(3)" "a." "iv)". lflow
// has no ordered list — the number is positional information the outline
// already carries — so the marker comes off and the item is a bullet.
var orderedRe = regexp.MustCompile(`^(?:\(\d{1,3}\)|\d{1,3}[.)]|[a-zA-Z][.)]|[ivxlcIVXLC]{2,6}[.)])[ \t]+`)

func stripOrdered(s string) string {
	if m := orderedRe.FindString(s); m != "" {
		if rest := strings.TrimLeft(s[len(m):], " \t"); rest != "" {
			return rest
		}
	}
	return s
}

// checkboxDone / checkboxOpen are the single-glyph boxes. A crossed-out box
// counts as DONE: the item is off the list either way, and reading it as open
// would resurrect work someone explicitly closed. The ticks and crosses are
// written as escapes — the house style keeps those glyphs out of string
// literals (rules/no-status-emoji.yml), and here they are input to recognize
// rather than output to print: \u2713 \u2714 are ✓ ✔ and \u2717 \u2718 are the crosses.
const (
	checkboxDone = "☑☒✅■▣◼🗹❌\u2713\u2714\u2717\u2718"
	checkboxOpen = "☐□▢◻⬜🔲"
	// checkboxTicks are the marks that fill a bracket box: "[✓]", "[✗]".
	checkboxTicks = "\u2713\u2714\u2717\u2718"
)

// bracketBoxRe matches the markdown checkbox, tolerant about what is inside it
// and about the space after: "[ ]", "[x]", "[X]", "[-]", "[]" and the ticked
// spellings (the escapes are the tick/cross glyphs, see checkboxDone).
var bracketBoxRe = regexp.MustCompile("^\\[([ xX~\\-" + checkboxTicks + "])?\\][ \\t]*")

// stripCheckbox reads a checkbox off the front of a line, returning the rest,
// whether it was ticked, and whether there was a box at all.
func stripCheckbox(s string) (string, bool, bool) {
	if m := bracketBoxRe.FindStringSubmatch(s); m != nil {
		rest := strings.TrimLeft(s[len(m[0]):], " \t")
		done := m[1] != "" && strings.ContainsAny(m[1], "xX"+checkboxTicks)
		if rest == "" && !done {
			return s, false, false // a bare "[]" is punctuation, not a task
		}
		return rest, done, true
	}
	r := []rune(s)
	if len(r) == 0 {
		return s, false, false
	}
	done := strings.ContainsRune(checkboxDone, r[0])
	if !done && !strings.ContainsRune(checkboxOpen, r[0]) {
		return s, false, false
	}
	rest := strings.TrimLeft(string(r[1:]), " \t")
	if rest == "" {
		return s, false, false
	}
	return rest, done, true
}

// strikeRe matches a whole line wrapped in markdown strikethrough.
var strikeRe = regexp.MustCompile(`^~~(.+?)~~$`)

func stripStrikethrough(s string) (string, bool) {
	if m := strikeRe.FindStringSubmatch(strings.TrimSpace(s)); m != nil {
		return m[1], true
	}
	return s, false
}

// stripTreeRail eats the box-drawing rail `tree`-style output draws to the left
// of a name, returning how many columns it covered so the rail becomes depth.
// A row of a box-drawn TABLE is not a rail — the table reader has already
// claimed those lines before this runs.
func stripTreeRail(s string) (int, string) {
	n, boxed := 0, false
	for _, r := range s {
		switch {
		case r >= '─' && r <= '╿':
			boxed = true
		case r == ' ':
		default:
			if !boxed {
				return 0, s
			}
			rest := strings.TrimLeft(string([]rune(s)[n:]), " ")
			if rest == "" {
				return 0, s
			}
			return n, rest
		}
		n++
	}
	return 0, s // the whole line is rail: nothing to hang
}

// ── multi-line blocks ───────────────────────────────────────────────────────

// fenceRe matches a fence opener and captures its language tag.
var fenceRe = regexp.MustCompile("^(?:`{3,}|~{3,})[ \t]*([A-Za-z0-9_+#.-]*)")

// fenceCloseRe matches a bare closing fence.
var fenceCloseRe = regexp.MustCompile("^(?:`{3,}|~{3,})[ \t]*$")

// pasteFenceOpen reports the fence marker of an opening line ("" when the line
// opens no fence).
func pasteFenceOpen(trimmed string) string {
	if !strings.HasPrefix(trimmed, "```") && !strings.HasPrefix(trimmed, "~~~") {
		return ""
	}
	return trimmed[:3]
}

// readFence consumes a fenced block and returns the index of its last line.
// An UNTERMINATED fence still becomes a code node holding the rest of the
// paste: half a code block is the most ordinary broken paste there is, and
// dropping back to bullets would shred it into one node per line.
func (p *pasteParser) readFence(i, indent int, trimmed string) int {
	lang := ""
	if m := fenceRe.FindStringSubmatch(trimmed); m != nil {
		lang = m[1]
	}
	var body []string
	j := i + 1
	for ; j < len(p.lines); j++ {
		if fenceCloseRe.MatchString(strings.TrimSpace(p.lines[j])) {
			break
		}
		body = append(body, pasteDedent(p.lines[j], p.base+indent))
	}
	code := strings.TrimRight(strings.Join(body, "\n"), "\n")
	n := &SrcNode{Type: database.TypeCode, Text: code, Note: lang}
	// a fence whose body IS a json document becomes a json node: it has a
	// richer face here than a code block, and the fence said as much
	if strings.EqualFold(lang, "json") && json.Valid([]byte(code)) {
		n = &SrcNode{Type: database.TypeJSON, Text: code}
	}
	p.add(indent, 0, n, false)
	return min(j, len(p.lines)-1)
}

// pasteDedent removes up to n leading columns from a block body line, so a
// fence indented under a list item keeps its INTERNAL indentation and loses
// only the margin it was pasted at.
func pasteDedent(line string, n int) string {
	w := 0
	i := 0
	for ; i < len(line) && w < n; i++ {
		switch line[i] {
		case ' ':
			w++
		case '\t':
			w += 4
		default:
			return line[i:]
		}
	}
	return line[i:]
}

// pasteMathOpen reports the opener of a display-math block.
func pasteMathOpen(trimmed string) string {
	switch {
	case strings.HasPrefix(trimmed, "$$"):
		return "$$"
	case strings.HasPrefix(trimmed, `\[`):
		return `\[`
	}
	return ""
}

// readMath consumes display math, one line ("$$ x = 1 $$") or several.
func (p *pasteParser) readMath(i, indent int, trimmed string) int {
	open := pasteMathOpen(trimmed)
	close := "$$"
	if open == `\[` {
		close = `\]`
	}
	if inner, ok := strings.CutPrefix(trimmed, open); ok {
		if body, ok := strings.CutSuffix(strings.TrimSpace(inner), close); ok && strings.TrimSpace(body) != "" {
			p.add(indent, 0, &SrcNode{Type: database.TypeMath, Text: pasteClean(strings.TrimSpace(body))}, false)
			return i
		}
	}
	var body []string
	j := i + 1
	for ; j < len(p.lines); j++ {
		t := strings.TrimSpace(p.lines[j])
		if t == close {
			break
		}
		body = append(body, t)
	}
	p.add(indent, 0, &SrcNode{Type: database.TypeMath, Text: pasteClean(strings.Join(body, " "))}, false)
	return min(j, len(p.lines)-1)
}

// tableSeps are the two characters a text table divides its cells with: the
// markdown pipe and the box-drawing bar every CLI table tool prints.
const tableSeps = "|│"

// pasteIsTableRow reports a row of a pipe- or box-drawn table: it opens with a
// separator and carries at least two of them. The trailing pipe is optional —
// GFM allows "| a | b" and plenty of tools print it that way — but the second
// separator is not, which is what keeps a tree rail ("│   └── x", one bar) out
// of the table reader.
func pasteIsTableRow(s string) bool {
	r := []rune(s)
	if len(r) < 3 || !strings.ContainsRune(tableSeps, r[0]) {
		return false
	}
	return strings.Count(s, string(r[0])) >= 2
}

// tableRuleRe matches a row of a table that carries no data — the separator
// under a header and the lids of a box, in both the markdown (|---|---|) and
// the box-drawing (┌───┬───┐) spelling.
var tableRuleRe = regexp.MustCompile(`^[|│+├┼┤└┴┘┌┬┐╞╪╡╘╧╛╒╤╕\-=:─━═\s]+$`)

// pasteIsTableRule is tableRuleRe plus the requirement that carries the whole
// distinction: a joint character. Without it "---" — an ordinary divider —
// would open a table of nothing.
func pasteIsTableRule(s string) bool {
	return strings.ContainsAny(s, "|│+├┼┤└┴┘┌┬┐╞╪╡╘╧╛╒╤╕") && tableRuleRe.MatchString(s)
}

// readTable consumes a run of table rows into a table node — columns are its
// children, a column's children are its cells, matching what the markdown
// codec builds so the two spellings of a table agree.
func (p *pasteParser) readTable(i, indent int) int {
	var grid [][]string
	j := i
	for ; j < len(p.lines); j++ {
		t := strings.TrimSpace(p.lines[j])
		if pasteIsTableRule(t) {
			continue // the header rule and the box's own lids carry no data
		}
		if !pasteIsTableRow(t) {
			break
		}
		grid = append(grid, pasteTableCells(t))
	}
	if len(grid) > 0 {
		p.add(indent, 0, tableFromGrid(grid), false)
	}
	return j - 1
}

// pasteTableCells splits one row on whichever separator it uses.
func pasteTableCells(s string) []string {
	sep := "|"
	if strings.HasPrefix(s, "│") {
		sep = "│"
	}
	s = strings.Trim(s, sep)
	parts := strings.Split(s, sep)
	out := make([]string, len(parts))
	for i, c := range parts {
		out[i] = pasteClean(strings.TrimSpace(c))
	}
	return out
}

// pasteIsTSVRow reports a tab-separated data row — what a spreadsheet, a
// database client or `column -t -s$'\t'` puts on the clipboard. A LEADING tab
// is indentation, not a cell separator, so such a line is never a TSV row.
func pasteIsTSVRow(line string) bool {
	if line == "" || line[0] == '\t' || line[0] == ' ' {
		return false
	}
	return strings.Count(line, "\t") >= 1
}

// readTSV consumes a run of tab-separated rows of the SAME width into a table.
// One row alone is not a table (it is a line that happens to hold a tab), and a
// run whose widths disagree is not one either — both fall back to plain rows.
func (p *pasteParser) readTSV(i, indent int) (int, bool) {
	cols := strings.Count(p.lines[i], "\t") + 1
	var grid [][]string
	j := i
	for ; j < len(p.lines); j++ {
		if !pasteIsTSVRow(p.lines[j]) || strings.Count(p.lines[j], "\t")+1 != cols {
			break
		}
		cells := strings.Split(p.lines[j], "\t")
		for k := range cells {
			cells[k] = pasteClean(strings.TrimSpace(cells[k]))
		}
		grid = append(grid, cells)
	}
	if len(grid) < 2 {
		return i, false
	}
	p.add(indent, 0, tableFromGrid(grid), false)
	return j - 1, true
}

// ── whole-paste shapes ──────────────────────────────────────────────────────

// pasteWholeJSON claims a paste that is one JSON document — a fetched
// response, a config file, a log record. It becomes a json node, which is the
// type that can actually show it.
func pasteWholeJSON(text string) ([]*SrcNode, bool) {
	t := strings.TrimSpace(text)
	if len(t) < 2 || (t[0] != '{' && t[0] != '[') {
		return nil, false
	}
	if !json.Valid([]byte(t)) {
		return nil, false
	}
	return []*SrcNode{{Type: database.TypeJSON, Text: t}}, true
}

// codeHintRe matches the things a line of code has and a line of prose does
// not: a brace/semicolon tail, a declaration keyword, a comment lead, an
// arrow, a namespace path.
var codeHintRe = regexp.MustCompile(`(?:^|\s)(?:func|def|class|struct|impl|fn|package|import|from|return|const|let|var|public|private|static|void|async|await|elif|#include|SELECT|INSERT|UPDATE|DELETE)\s|[{};]\s*$|^\s*(?://|#!|/\*|\*\s)|=>|->|::|\(\)`)

// diffLeadRe matches the headers only a patch has.
var diffLeadRe = regexp.MustCompile(`^(?:diff --git |@@ -\d|index [0-9a-f]{7,}|--- a/|\+\+\+ b/)`)

// pasteWholeDiff claims a patch. A diff's own +/- leads read as bullet markers
// and its hunk headers as prose, so left to the row classifier a patch shreds
// into one node per line with the signs eaten off — the one shape where the
// generic reading is actively destructive. It becomes a code block tagged
// "diff", which is exactly how it is meant to be read.
func pasteWholeDiff(lines []string) ([]*SrcNode, bool) {
	leads := 0
	for _, l := range lines {
		if diffLeadRe.MatchString(l) {
			leads++
		}
	}
	if leads == 0 {
		return nil, false
	}
	code := strings.Trim(strings.Join(lines, "\n"), "\n")
	return []*SrcNode{{Type: database.TypeCode, Text: code, Note: "diff"}}, true
}

// pasteWholeCode claims a paste that is UNFENCED code — dragged out of an
// editor, a Stack Overflow answer, a log of a build. The bar is deliberately
// high: at least two lines, at least two code signals covering two thirds of
// the lines, and few enough document markers that the paste cannot be a
// document (a heading is not a veto on its own — "#" is a comment in half the
// languages there are, and a "* " line is how a C comment block continues).
//
// Below that bar the paste stays an outline. Reading a list as one code block
// is the worse mistake of the two: an outline that should have been code is one
// /type away, while code that swallowed a list has to be taken apart by hand.
func pasteWholeCode(lines []string) ([]*SrcNode, bool) {
	body, hits, markup := 0, 0, 0
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		body++
		if pasteFenceOpen(t) != "" || pasteIsTableRow(t) {
			return nil, false // a fence or a grid is never part of raw code
		}
		if pasteHasMarkup(t) {
			markup++
		}
		if codeHintRe.MatchString(t) {
			hits++
		}
	}
	if body < 2 || hits < 2 || hits*3 < body*2 || markup*3 >= body {
		return nil, false
	}
	base := pasteBaseIndent(lines)
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, pasteDedent(l, base))
	}
	code := strings.Trim(strings.Join(out, "\n"), "\n")
	return []*SrcNode{{Type: database.TypeCode, Text: code}}, true
}

// pasteHasMarkup reports the document markers that count against the code
// reading — the ones a list, a quote or a rule would show.
func pasteHasMarkup(trimmed string) bool {
	if strings.HasPrefix(trimmed, "<!--") {
		return true // an HTML comment ends in "-->", which reads as an arrow
	}
	if _, _, ok := stripBullet(trimmed); ok {
		return true
	}
	if _, _, ok := pasteQuote(trimmed); ok {
		return true
	}
	if _, ok := pasteDivider(trimmed); ok {
		return true
	}
	_, _, heading := pasteHeading(trimmed)
	return heading
}
