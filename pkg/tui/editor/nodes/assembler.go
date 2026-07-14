package nodes

import (
	"context"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/editor"
)

// The assembler ▣ — the factory family's transformer (see factory.go for the
// belt-line model). Its text is a shell command run with the incoming payload
// on stdin; its stdout is what leaves on the belt. An assembler with no
// command is a bare belt segment — the payload passes through unchanged. A
// nonzero exit fails the line.

func init() {
	facStagers[database.TypeAssembler] = assembleStage
	editor.RegisterNodePlugin(editor.NodePlugin{
		Key: database.TypeAssembler, Label: "Assembler",
		InlineEditable: true,
		CLIDeps:        []string{"bash"},
		Glyph:          func() (string, string) { return "▣", facBlue },
		BaseColor:      func() string { return facBlue },
		Prefix:         facPrefix,
		Run:            facRun,
		View:           facView{},
		ToContext:      facContext("assembler"),
		OnRemove:       facOnRemove,
	})
}

func assembleStage(ctx context.Context, cmd, payload string) facOut {
	if cmd == "" { // a bare belt: pass the payload through
		return facOut{payload: payload, note: facSize(len(payload))}
	}
	out, exit, note := facExecCmd(ctx, cmd, payload)
	if exit != 0 {
		return facOut{err: note}
	}
	return facOut{payload: out, note: facSize(len(out))}
}
