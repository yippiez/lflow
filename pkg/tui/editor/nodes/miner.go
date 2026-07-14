package nodes

import (
	"context"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/editor"
)

// The miner ▼ — the factory family's source machine (see factory.go for the
// belt-line model). Its text is a shell command; on a line run it ignores the
// incoming payload and emits its stdout as a fresh one, so a miner mid-line
// restarts the belt with new material. An empty miner emits nothing. A
// nonzero exit fails the line.

func init() {
	facStagers[database.TypeMiner] = mineStage
	editor.RegisterNodePlugin(editor.NodePlugin{
		Key: database.TypeMiner, Label: "Miner",
		InlineEditable: true,
		CLIDeps:        []string{"bash"},
		Glyph:          func() (string, string) { return "▼", facBlue },
		BaseColor:      func() string { return facBlue },
		Prefix:         facPrefix,
		Run:            facRun,
		View:           facView{},
		ToContext:      facContext("miner"),
		OnRemove:       facOnRemove,
	})
}

func mineStage(ctx context.Context, cmd, _ string) facOut {
	if cmd == "" {
		return facOut{note: "0b"}
	}
	out, exit, note := facExecCmd(ctx, cmd, "")
	if exit != 0 {
		return facOut{err: note}
	}
	return facOut{payload: out, note: facSize(len(out))}
}
