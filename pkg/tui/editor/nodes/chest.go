package nodes

import (
	"context"
	"fmt"
	"strings"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/editor"
)

// The chest ▤ — the factory family's storage (see factory.go for the belt-line
// model). It runs nothing: when the belt reaches it, it holds a copy of the
// arriving payload (the yellow ⟨3L · 142b⟩ chip; alt+e opens it) and passes
// the payload on unchanged, so a chest mid-line is a tap and a chest at the
// end is the delivery box. Its text is a free label. What a chest holds is
// ephemeral run output — never persisted, gone on restart.

func init() {
	facStagers[database.TypeChest] = chestStage
	editor.RegisterNodePlugin(editor.NodePlugin{
		Key: database.TypeChest, Label: "Chest",
		InlineEditable: true,
		Glyph:          func() (string, string) { return "▤", facBlue },
		BaseColor:      func() string { return facBlue },
		Prefix:         facPrefix,
		Run:            facRun,
		View:           facView{},
		ToContext:      chestContext,
		OnRemove:       facOnRemove,
	})
}

func chestStage(_ context.Context, _, payload string) facOut {
	if payload == "" {
		return facOut{note: "empty"}
	}
	lines := strings.Count(payload, "\n") + 1
	return facOut{payload: payload, note: fmt.Sprintf("%dL · %s", lines, facSize(len(payload)))}
}

// chestContext hands an agent what the chest currently holds — label line
// first, then the payload (ephemeral, so only ever spoken from the live
// editor, never stored).
func chestContext(h editor.NodeHost, n editor.NodeRef) (string, string, string) {
	if st := facStats[n.UUID()]; st != nil && st.payload != "" {
		return "chest", "", n.Text() + "\n" + st.payload
	}
	return "chest", "", ""
}
