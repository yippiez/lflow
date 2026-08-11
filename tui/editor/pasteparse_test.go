package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/tui/database"
)

// The paste parser's corpus. Every case here is a paste that really happens —
// a Notion list that lost its indentation, a mail reply, `tree` output, a
// spreadsheet column, a half-copied code block — and the assertion is the shape
// lflow makes of it. dumpPaste prints a forest as "type text" lines, two spaces
// per level, so a case reads as the outline it should become.

// dumpPaste renders a parsed forest for comparison: type key (bullets when
// unset), a (done) tick, a (note) payload, then the text with newlines shown as
// ⏎ so a multi-line code body stays one assertion line.
func dumpPaste(doc []*SrcNode) string {
	var b strings.Builder
	var walk func(kids []*SrcNode, depth int)
	walk = func(kids []*SrcNode, depth int) {
		for _, k := range kids {
			b.WriteString(strings.Repeat("  ", depth))
			typ := k.Type
			if typ == "" {
				typ = database.TypeBullets
			}
			b.WriteString(typ)
			if k.Completed {
				b.WriteString("(done)")
			}
			if k.Note != "" {
				b.WriteString("(" + k.Note + ")")
			}
			if k.Text != "" {
				b.WriteString(" " + strings.ReplaceAll(k.Text, "\n", "⏎"))
			}
			b.WriteByte('\n')
			walk(k.Kids, depth+1)
		}
	}
	walk(doc, 0)
	return b.String()
}

// checkPaste parses in and compares the forest against want, which is written
// with a leading newline and tabs stripped so the cases stay readable inline.
func checkPaste(t *testing.T, in, want string) {
	t.Helper()
	got := dumpPaste(parsePaste(in))
	if got != strings.TrimPrefix(want, "\n") {
		t.Fatalf("paste %q\n got:\n%s\nwant:\n%s", in, got, strings.TrimPrefix(want, "\n"))
	}
}

// ── the shapes people paste ─────────────────────────────────────────────────

// TestPasteMarkdownDocument: the everyday paste — a markdown README dragged out
// of a browser. Headings own the sections beneath them, the list nests by
// indentation, and the checkboxes come back as todos.
func TestPasteMarkdownDocument(t *testing.T) {
	checkPaste(t, `# Release 2.1

Some intro prose.

## Shipping
- [x] cut the tag
- [ ] write the notes
  - draft is in the wiki
### Later
- backlog item
`, `
h1 Release 2.1
  bullets Some intro prose.
  h2 Shipping
    todo(done) cut the tag
    todo write the notes
      bullets draft is in the wiki
    h3 Later
      bullets backlog item
`)
}

// TestPasteFlushLeftGlyphList is the Word / Google Docs / Notion paste: the
// nesting survives ONLY in the bullet glyph, every line flush left. The glyph
// ranks are what rebuild the tree.
func TestPasteFlushLeftGlyphList(t *testing.T) {
	// \u2022 is •, the glyph the house style keeps out of string literals
	checkPaste(t, "\u2022 fruit\n◦ apple\n▪ granny smith\n◦ pear\n\u2022 veg\n◦ leek\n", `
bullets fruit
  bullets apple
    bullets granny smith
  bullets pear
bullets veg
  bullets leek
`)
}

// TestPasteIndentationIsIrregular: real pastes indent by 1, 3, 7 spaces or a
// tab, and mix them in one block. Depth is relative — any increase opens a
// level, any decrease closes back to the nearest shallower one — and a tab is
// measured in columns (four), so it can sit between two space indents.
func TestPasteIndentationIsIrregular(t *testing.T) {
	checkPaste(t, "top\n   one\n       two\n\tone again\n  back down\nback to top\n", `
bullets top
  bullets one
    bullets two
    bullets one again
  bullets back down
bullets back to top
`)
}

// TestPasteCommonMarginDropped: text copied out of an indented block arrives
// with every line pushed right; the whole paste must not land a level deep.
func TestPasteCommonMarginDropped(t *testing.T) {
	checkPaste(t, "        alpha\n          beta\n        gamma\n", `
bullets alpha
  bullets beta
bullets gamma
`)
}

// TestPasteBulletGlyphZoo: every marker the world puts in front of a list item
// comes off, and the text behind it is the node.
func TestPasteBulletGlyphZoo(t *testing.T) {
	checkPaste(t, "- dash\n* star\n+ plus\n\u2022 bullet\n· middot\n"+
		"– en dash\n— em dash\n▸ triangle\n➤ arrow\n» guillemet\n", `
bullets dash
bullets star
bullets plus
bullets bullet
bullets middot
bullets en dash
bullets em dash
bullets triangle
bullets arrow
bullets guillemet
`)
}

// TestPasteNonBulletsStayText: the markers that look like bullets but are not.
// A "-" with no space is a minus sign, "*bold*" is emphasis, "#tag" is a tag,
// and "1984." is a year at the head of a sentence, not an ordered item.
func TestPasteNonBulletsStayText(t *testing.T) {
	checkPaste(t, "-5 degrees\n*bold* text\n#tag line\n1984. was a year\n", `
bullets -5 degrees
bullets *bold* text
bullets #tag line
bullets 1984. was a year
`)
}

// TestPasteOrderedLists: numbers, letters and roman numerals all open list
// items. lflow has no ordered type — the outline is the order — so the marker
// comes off and the nesting is kept.
func TestPasteOrderedLists(t *testing.T) {
	checkPaste(t, `1. first
2) second
   a. sub one
   b) sub two
      iv. deep roman
(3) third
`, `
bullets first
bullets second
  bullets sub one
  bullets sub two
    bullets deep roman
bullets third
`)
}

// TestPasteCheckboxes: every spelling of "this is a task" and of "this one is
// finished", from markdown boxes to a struck-out line to lflow's own □ / ■.
func TestPasteCheckboxes(t *testing.T) {
	// the tick and cross glyphs go in escaped, as they do in the parser itself
	checkPaste(t, "- [ ] plain\n- [x] ticked\n* [X] shouty\n1. [ ] numbered\n"+
		"- [\u2713] tick in a box\n- [\u2717] crossed off\n- [-] half done\n"+
		"☐ box\n☑ ticked box\n✅ green tick\n□ lflow open\n■ lflow done\n- ~~struck out~~\n", `
todo plain
todo(done) ticked
todo(done) shouty
todo numbered
todo(done) tick in a box
todo(done) crossed off
todo half done
todo box
todo(done) ticked box
todo(done) green tick
todo lflow open
todo(done) lflow done
todo(done) struck out
`)
}

// TestPasteNestedTodoList: a task list keeps its subtasks.
func TestPasteNestedTodoList(t *testing.T) {
	checkPaste(t, `- [ ] ship the parser
    - [x] write it
    - [ ] test it
        - [ ] the broken cases
`, `
todo ship the parser
  todo(done) write it
  todo test it
    todo the broken cases
`)
}

// TestPasteOutlinerExport is the Workflowy / Dynalist / org-mode shape: "- "
// markers indented with TABS, which is what those exports and half of every
// editor's auto-indent produce.
func TestPasteOutlinerExport(t *testing.T) {
	checkPaste(t, "- project\n\t- milestone\n\t\t- task\n\t- another milestone\n", `
bullets project
  bullets milestone
    bullets task
  bullets another milestone
`)
}

// TestPasteOutdentSkipsLevels: a line that drops back two levels at once lands
// at the level it names, not one step up.
func TestPasteOutdentSkipsLevels(t *testing.T) {
	checkPaste(t, "a\n  b\n    c\n      d\n  e\nf\n", `
bullets a
  bullets b
    bullets c
      bullets d
  bullets e
bullets f
`)
}

// TestPasteChildDeeperThanItsParentsSibling: an indent that matches NO open
// level (7 spaces under a 4-space item that follows a 2-space one) still lands
// under the nearest shallower row instead of being dropped or promoted.
func TestPasteChildDeeperThanItsParentsSibling(t *testing.T) {
	checkPaste(t, "root\n  two\n    four\n   three\nback\n", `
bullets root
  bullets two
    bullets four
    bullets three
bullets back
`)
}

// TestPasteFencedCode: a fence becomes ONE code node holding every line, with
// its language tag, and the outline around it is untouched.
func TestPasteFencedCode(t *testing.T) {
	checkPaste(t, "before\n```go\nfunc main() {\n\tprintln(\"hi\")\n}\n```\nafter\n", `
bullets before
code(go) func main() {⏎	println("hi")⏎}
bullets after
`)
}

// TestPasteFenceUnderBullet: a fence indented under a list item is that item's
// child, and only the paste's own margin comes off — the code keeps its shape.
func TestPasteFenceUnderBullet(t *testing.T) {
	checkPaste(t, "- run it\n  ```sh\n  make test\n    --verbose\n  ```\n", `
bullets run it
  code(sh) make test⏎  --verbose
`)
}

// TestPasteUnterminatedFence: the most ordinary broken paste there is — half a
// code block. The rest of the text is still code, not one node per line.
func TestPasteUnterminatedFence(t *testing.T) {
	checkPaste(t, "```python\nx = 1\ny = 2\n", `
code(python) x = 1⏎y = 2
`)
}

// TestPasteTildeFence: ~~~ opens a fence too.
func TestPasteTildeFence(t *testing.T) {
	checkPaste(t, "~~~\nraw text\n~~~\n", `
code raw text
`)
}

// TestPasteJSONFence: a fence that says json and holds json becomes a json
// node, which is the type that can actually show it.
func TestPasteJSONFence(t *testing.T) {
	checkPaste(t, "```json\n{\"a\": 1}\n```\n", `
json {"a": 1}
`)
}

// TestPasteWholeJSONDocument: a bare API response becomes one json node.
func TestPasteWholeJSONDocument(t *testing.T) {
	checkPaste(t, "{\n  \"ok\": true,\n  \"items\": [1, 2]\n}\n", `
json {⏎  "ok": true,⏎  "items": [1, 2]⏎}
`)
}

// TestPasteInvalidJSONStaysOutline: half a JSON object is not a document —
// it falls back to rows rather than pretending to be a json node.
func TestPasteInvalidJSONStaysOutline(t *testing.T) {
	doc := parsePaste("{\n  \"ok\": tru\n")
	if len(doc) == 0 || doc[0].Type == database.TypeJSON {
		t.Fatalf("invalid json was claimed as a json node: %s", dumpPaste(doc))
	}
}

// TestPasteUnfencedCode: code dragged straight out of an editor, with no fence
// around it, still lands as a code block instead of one node per line.
func TestPasteUnfencedCode(t *testing.T) {
	checkPaste(t, "func add(a, b int) int {\n\treturn a + b\n}\n", `
code func add(a, b int) int {⏎	return a + b⏎}
`)
}

// TestPasteProseIsNeverCode: prose that happens to hold a semicolon or the word
// "return" stays an outline. Reading a list as a code block is the worse
// mistake of the two, so the code heuristic has to lose ties.
func TestPasteProseIsNeverCode(t *testing.T) {
	checkPaste(t, "We should return to this;\nit came up in standup.\n", `
bullets We should return to this;
bullets it came up in standup.
`)
}

// TestPasteDiff: a patch's own +/- leads read as bullet markers, so a diff has
// to be claimed whole or it shreds into rows with the signs eaten off.
func TestPasteDiff(t *testing.T) {
	checkPaste(t, `diff --git a/x.go b/x.go
@@ -1,3 +1,3 @@
 ctx
-old line
+new line
`, `
code(diff) diff --git a/x.go b/x.go⏎@@ -1,3 +1,3 @@⏎ ctx⏎-old line⏎+new line
`)
}

// TestPasteMarkdownTable: a GFM grid becomes a table node — columns are its
// children, a column's children are its cells — the same shape the markdown
// codec builds, and the rule row carries no data.
func TestPasteMarkdownTable(t *testing.T) {
	checkPaste(t, `| name | qty |
| --- | --- |
| bolt | 4 |
| nut | 8 |
`, `
table
  bullets name
    bullets bolt
    bullets nut
  bullets qty
    bullets 4
    bullets 8
`)
}

// TestPasteTableWithoutClosingPipe: plenty of tools print a grid without the
// trailing pipe, and GFM accepts it — so does the reader.
func TestPasteTableWithoutClosingPipe(t *testing.T) {
	checkPaste(t, "| name | qty\n| bolt | 4\n", `
table
  bullets name
    bullets bolt
  bullets qty
    bullets 4
`)
}

// TestPasteBoxDrawnTable: the grid a CLI tool prints (psql, `rich`, docker) is
// a table too — the bars are just drawn with box characters.
func TestPasteBoxDrawnTable(t *testing.T) {
	checkPaste(t, `┌──────┬─────┐
│ name │ qty │
├──────┼─────┤
│ bolt │ 4   │
└──────┴─────┘
`, `
table
  bullets name
    bullets bolt
  bullets qty
    bullets 4
`)
}

// TestPasteSpreadsheetTSV: cells copied out of Excel, Sheets or a database
// client arrive tab separated — a table, not a stack of rows with tabs in them.
func TestPasteSpreadsheetTSV(t *testing.T) {
	checkPaste(t, "region\ttotal\nEMEA\t1200\nAPAC\t900\n", `
table
  bullets region
    bullets EMEA
    bullets APAC
  bullets total
    bullets 1200
    bullets 900
`)
}

// TestPasteLoneTabbedLineIsNotATable: one line with a tab in it is a line, and
// rows of disagreeing widths are not a grid.
func TestPasteLoneTabbedLineIsNotATable(t *testing.T) {
	checkPaste(t, "name\tqty\n", `
bullets name qty
`)
}

// TestPasteQuotes: a mail or Slack quote, including the reply-inside-a-reply
// that nests by the number of ">" marks.
func TestPasteQuotes(t *testing.T) {
	checkPaste(t, "> she wrote this\n>> and he had written this\n> back to her\n", `
quote she wrote this
  quote and he had written this
quote back to her
`)
}

// TestPasteDividers: every way a rule gets written, plus the captioned rule
// lflow's own copy produces.
func TestPasteDividers(t *testing.T) {
	checkPaste(t, "one\n---\ntwo\n***\nthree\n═══════\n- - -\n──── notes ────\n", `
bullets one
divider
bullets two
divider
bullets three
divider
divider
divider notes
`)
}

// TestPasteSetextHeading: a title underlined with "=" is a heading. The "-"
// underline deliberately stays a divider — in text that was never a tidy
// markdown file, a row of dashes is far more often a rule.
func TestPasteSetextHeading(t *testing.T) {
	checkPaste(t, "Chapter One\n===========\nbody line\n\nsomething\n---\nelse\n", `
h1 Chapter One
  bullets body line
  bullets something
  divider
  bullets else
`)
}

// TestPasteTreeCommandOutput: `tree` (and every dependency printer that copies
// its rail) draws depth with box characters instead of spaces.
func TestPasteTreeCommandOutput(t *testing.T) {
	checkPaste(t, `src
├── editor
│   ├── paste.go
│   └── keys.go
└── database
    └── nodes.go
`, `
bullets src
  bullets editor
    bullets paste.go
    bullets keys.go
  bullets database
    bullets nodes.go
`)
}

// TestPasteShellTranscript: a copied terminal session — the prompt lines are
// commands, everything else is output. The output stays a SIBLING: a bash
// node's children are the parts of its command line (see bashnode.go), so
// hanging output under it would rewrite the command.
func TestPasteShellTranscript(t *testing.T) {
	checkPaste(t, "$ make test\nok  editor 0.5s\n❯ git push\n➜ deploy prod\n", `
bash make test
bullets ok  editor 0.5s
bash git push
bash deploy prod
`)
}

// TestPasteComments: an HTML comment is a comment node, and lflow's own marker
// comments (what the markdown codec writes for a type markdown cannot spell)
// restore the type they were hiding.
func TestPasteComments(t *testing.T) {
	checkPaste(t, "<!-- just a note -->\n<!-- lflow barplot: Q3 revenue -->\n", `
comment just a note
barplot Q3 revenue
`)
}

// TestPasteMath: display math, block and inline, becomes a math node.
func TestPasteMath(t *testing.T) {
	checkPaste(t, "$$\na^2 + b^2 = c^2\n$$\n$$ e = mc^2 $$\n", `
math a^2 + b^2 = c^2
math e = mc^2
`)
}

// ── the broken part ─────────────────────────────────────────────────────────

// TestPasteTerminalScrape: text dragged off a colored terminal carries SGR
// sequences, an OSC 8 hyperlink and a bracketed-paste wrapper. All of it must
// come out WHOLE — sanitizeName alone would leave the printable tails behind.
func TestPasteTerminalScrape(t *testing.T) {
	in := "\x1b[200~\x1b[1;32m# Results\x1b[0m\n\x1b]8;;https://x.test\x07- \x1b[31mred item\x1b[0m\x1b]8;;\x07\n\x1b[201~"
	checkPaste(t, in, `
h1 Results
  bullets red item
`)
}

// TestPasteInvisibleJunk: a copy out of a web page or a PDF drags zero-width
// characters, a BOM, non-breaking spaces (which are the INDENTATION in a Word
// paste) and soft hyphens along. Emoji and smart quotes are content and stay.
func TestPasteInvisibleJunk(t *testing.T) {
	in := "\ufeff\u200bplan “Q3” 🎯\n\u00a0\u00a0soft\u00adhyphen child\n"
	checkPaste(t, in, `
bullets plan “Q3” 🎯
  bullets softhyphen child
`)
}

// TestPasteChipSentinelStripped: U+FFFC is the chip anchor. One arriving from
// outside names no chip record here and would render as a ghost anchor.
func TestPasteChipSentinelStripped(t *testing.T) {
	checkPaste(t, "a \ufffcdead-chip\ufffc b\n", `
bullets a dead-chip b
`)
}

// TestPasteCRLFAndTmuxDoubling: CRLF, a lone CR, and the \r\r\n tmux ONLCR
// produces all collapse to one break — no ghost rows between the real ones.
func TestPasteCRLFAndTmuxDoubling(t *testing.T) {
	checkPaste(t, "one\r\n\r\ntwo\r\r\nthree\r", `
bullets one
bullets two
bullets three
`)
}

// TestPasteControlOnlyLineMakesNoNode: a line that sanitizes down to nothing
// creates no node, so a paste never leaves a nameless row between two real ones.
func TestPasteControlOnlyLineMakesNoNode(t *testing.T) {
	checkPaste(t, "hello world\n\x01\x02\x03\x7f\ngoodbye world\n", `
bullets hello world
bullets goodbye world
`)
}

// TestPasteMarkerWithNothingBehindIt: a bare marker is punctuation, not an
// empty node — a lone "-" is a shrug, "[]" is brackets.
func TestPasteMarkerWithNothingBehindIt(t *testing.T) {
	checkPaste(t, "-\nreal line\n[]\n#\n", `
bullets -
bullets real line
bullets []
bullets #
`)
}

// TestPasteBlankLinesKeepTheList: air between the rows of a list does not
// flatten what follows — a blank line is a separator, not a dedent.
func TestPasteBlankLinesKeepTheList(t *testing.T) {
	checkPaste(t, "- parent\n\n  - child\n\n\n  - sibling\n", `
bullets parent
  bullets child
  bullets sibling
`)
}

// TestPasteWhitespaceOnly: nothing to land.
func TestPasteWhitespaceOnly(t *testing.T) {
	if doc := parsePaste("   \n\n\t\n"); len(doc) != 0 {
		t.Fatalf("blank paste made %d nodes: %s", len(doc), dumpPaste(doc))
	}
	if doc := parsePaste(""); doc != nil {
		t.Fatalf("empty paste made nodes: %s", dumpPaste(doc))
	}
}

// TestPasteEverythingAtOnce is the pathological paste: a document that mixes
// every shape, with the indentation half broken. Nothing may be dropped and
// nothing may nest under the wrong parent.
func TestPasteEverythingAtOnce(t *testing.T) {
	checkPaste(t, `# Sprint

> goal: ship the parser

## Tasks
- [x] parse markdown
- [ ] parse the rest
	* tabs happen
---
| k | v |
| - | - |
| a | 1 |

$ go test ./...
<!-- reviewed -->
`, `
h1 Sprint
  quote goal: ship the parser
  h2 Tasks
    todo(done) parse markdown
    todo parse the rest
      bullets tabs happen
    divider
    table
      bullets k
        bullets a
      bullets v
        bullets 1
    bash go test ./...
    comment reviewed
`)
}

// TestPasteURLStaysPlain: a pasted link is text at the caret — the link chip
// machinery (link.go, service.go) owns that gesture, and the parser must not
// steal it by inventing a type.
func TestPasteURLStaysPlain(t *testing.T) {
	checkPaste(t, "https://example.test/a?b=1&c=2\n", `
bullets https://example.test/a?b=1&c=2
`)
}

// ── the one-line and field helpers ──────────────────────────────────────────

// TestPasteFlatKeepsWords: a multi-line paste into a one-line field (a note,
// the link editor) joins with spaces instead of welding the words together,
// and a typed space is still a space.
func TestPasteFlatKeepsWords(t *testing.T) {
	if got, want := pasteFlat("first line\nsecond line"), "first line second line"; got != want {
		t.Fatalf("pasteFlat = %q, want %q", got, want)
	}
	if got := pasteFlat(" "); got != " " {
		t.Fatalf("a typed space came out %q", got)
	}
	if got := pasteFlat("a\r\n\r\nb"); got != "a b" {
		t.Fatalf("CRLF run came out %q", got)
	}
}

// TestPasteBlockKeepsLayout: a paste into a multi-line buffer (the code and
// json views) keeps its newlines and indentation, and loses only the noise.
func TestPasteBlockKeepsLayout(t *testing.T) {
	got := pasteBlock("\x1b[200~def f():\r\n\treturn 1\r\n\x1b[201~")
	if want := "def f():\n\treturn 1\n"; got != want {
		t.Fatalf("pasteBlock = %q, want %q", got, want)
	}
}
