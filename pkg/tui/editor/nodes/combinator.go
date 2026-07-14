package nodes

import (
	"context"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/editor"
)

// The combinator ◇ — the factory family's circuit gate (see factory.go for
// the belt-line model). Its text is a predicate command run with the incoming
// payload on stdin: exit 0 passes the payload through untouched, a nonzero
// exit CLOSES the gate and the line halts there (the ⊘ blocked chip — the
// deliberate stop, not an error). Spawn failures and timeouts fail the line
// instead. An empty combinator is an open gate.

func init() {
	facStagers[database.TypeCombinator] = combineStage
	editor.RegisterNodePlugin(editor.NodePlugin{
		Key: database.TypeCombinator, Label: "Combinator",
		InlineEditable: true,
		CLIDeps:        []string{"bash"},
		Glyph:          func() (string, string) { return "◇", facBlue },
		BaseColor:      func() string { return facBlue },
		Prefix:         facPrefix,
		Run:            facRun,
		View:           facView{},
		ToContext:      facContext("combinator"),
		OnRemove:       facOnRemove,
	})
}

func combineStage(ctx context.Context, cmd, payload string) facOut {
	if cmd == "" { // an open gate
		return facOut{payload: payload, note: "pass"}
	}
	_, exit, note := facExecCmd(ctx, cmd, payload)
	switch {
	case exit == 0:
		return facOut{payload: payload, note: "pass"}
	case exit > 0: // the predicate said no — the gate closes
		return facOut{blocked: true}
	default: // spawn failure / timeout — a broken gate, not a closed one
		return facOut{err: note}
	}
}
