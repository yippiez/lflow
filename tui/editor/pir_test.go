package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/tui/database"
)

// TestMagicKeywordsShineOnlyOnPir: the shine is a mark, not a spellchecker. It
// says "this row is an instruction", which it can only say if it stays off every
// other row — including the notes that discuss the keyword by name.
func TestMagicKeywordsShineOnlyOnPir(t *testing.T) {
	animFrame = 3 // a fixed frame so the colors are deterministic

	pir := &item{uuid: "p", typ: database.TypePir, name: "ultracode the parser"}
	plain := &item{uuid: "b", typ: database.TypeBullets, name: "ultracode the parser"}

	if !magicKeywordRow(pir) {
		t.Error("a Pir row is not recognized as the keywords' home")
	}
	for _, it := range []*item{plain,
		{uuid: "c", typ: database.TypeCode, name: "ultraloop"},
		{uuid: "t", typ: database.TypeTodo, name: "write about ultraloop"},
		nil,
	} {
		if magicKeywordRow(it) {
			t.Errorf("%v claimed the keyword animation", it)
		}
	}

	// the animated color is a truecolor foreground the plain palette never uses
	shine := shineColorAt(len("ultracode"), 0, animFrame,
		magicKeywords[0].speed, magicKeywords[0].base, magicKeywords[0].peak)
	if got := renderBodyFor(pir); !strings.Contains(got, shine) {
		t.Errorf("the Pir row did not shine: %q", stripSGR(got))
	}
	if got := renderBodyFor(plain); strings.Contains(got, shine) {
		t.Errorf("a bullet row shone: %q", stripSGR(got))
	}
}

// renderBodyFor renders one row's body the way the outline does, keyword gate
// included.
func renderBodyFor(it *item) string {
	return renderBody(it, it.name, -1, false, nil)
}

// TestMagicKeywordTickFollowsPir: the animation clock is only worth running when
// something on screen actually animates.
func TestMagicKeywordTickFollowsPir(t *testing.T) {
	m := newTestModel(80, "ultracode everywhere", "ultraloop too")
	if m.hasMagicKeyword() {
		t.Error("plain bullets kept the animation clock running")
	}
	m.rows[0].it.typ = database.TypePir
	if !m.hasMagicKeyword() {
		t.Error("a Pir row did not arm the animation clock")
	}
}

// TestPirIsARegisteredType: it appears in the /type picker like any other node,
// and it is an ordinary editable row until its own behaviour is written.
func TestPirIsARegisteredType(t *testing.T) {
	if !database.ValidTypes[database.TypePir] {
		t.Fatal("pir is not a valid node type")
	}
	d := typeOf(database.TypePir)
	if d.key != database.TypePir {
		t.Fatalf("no descriptor for pir: %+v", d)
	}
	if d.label != "Pir" {
		t.Errorf("label = %q", d.label)
	}
	if !d.inlineEditable {
		t.Error("a Pir row should type like an ordinary row for now")
	}
}
