// Package install adds `lflow node install <git-url>`: clone a NodeMod
// repository into <config>/lflow/mods/<name>, replacing any mod of the same
// name — re-running the command is the update path. The editor picks the mod
// up when /type opens or on its next start.
package install

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lflow/lflow/pkg/tui/consts"
	"github.com/lflow/lflow/pkg/tui/context"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

// manifest mirrors a mod repo's mod.json (see pkg/tui/editor/nodemod.go).
type manifest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Entry       string `json:"entry"`
	Version     string `json:"version"`
}

// NewCmd returns the install command.
func NewCmd(ctx context.DnoteCtx) *cobra.Command {
	return &cobra.Command{
		Use:     "install <git-url>",
		Short:   "Install a NodeMod from a git repository",
		Example: "  lflow node install https://github.com/yippiez/lflow-log",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(ctx, args[0])
		},
	}
}

func run(ctx context.DnoteCtx, url string) error {
	modsDir := filepath.Join(ctx.Paths.Config, consts.LflowDirName, "mods")
	if err := os.MkdirAll(modsDir, 0o755); err != nil {
		return errors.Wrap(err, "creating mods dir")
	}

	// clone into a dot-prefixed stage INSIDE the mods dir (the loader skips
	// dotfiles), so the final move is a same-filesystem rename
	stage, err := os.MkdirTemp(modsDir, ".install-")
	if err != nil {
		return errors.Wrap(err, "creating staging dir")
	}
	defer os.RemoveAll(stage)

	clone := exec.Command("git", "clone", "--depth", "1", url, stage)
	if out, err := clone.CombinedOutput(); err != nil {
		return errors.Errorf("git clone failed: %s", lastLine(string(out)))
	}

	b, err := os.ReadFile(filepath.Join(stage, "mod.json"))
	if err != nil {
		return errors.New("not a NodeMod repository: no mod.json at its root")
	}
	var mf manifest
	if err := json.Unmarshal(b, &mf); err != nil {
		return errors.Wrap(err, "reading mod.json")
	}
	if mf.Name == "" || mf.Entry == "" {
		return errors.New("mod.json must set name and entry")
	}
	if _, err := os.Stat(filepath.Join(stage, mf.Entry)); err != nil {
		return errors.Errorf("mod.json entry %q not found in the repository", mf.Entry)
	}

	// the clone becomes the mod; its history is baggage
	_ = os.RemoveAll(filepath.Join(stage, ".git"))

	// replace every prior form of the same mod: flat file or directory,
	// enabled or disabled
	dest := filepath.Join(modsDir, mf.Name)
	for _, stale := range []string{dest, dest + ".disabled", dest + ".js", dest + ".js.disabled"} {
		_ = os.RemoveAll(stale)
	}
	if err := os.Rename(stage, dest); err != nil {
		return errors.Wrap(err, "placing the mod")
	}

	line := "→ installed " + mf.Name
	if mf.Version != "" {
		line += " " + mf.Version
	}
	if mf.Description != "" {
		line += " · " + mf.Description
	}
	fmt.Println(line)
	return nil
}

// lastLine is the tail of a command's output — the line that names the error.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}
