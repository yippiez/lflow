package file

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"

	"github.com/lflow/lflow/packages/cli/context"
	"github.com/lflow/lflow/packages/cli/infra"
	"github.com/lflow/lflow/packages/database"
	"github.com/lflow/lflow/packages/editor"
	"github.com/lflow/lflow/packages/nodes"
	"github.com/lflow/lflow/packages/utils/log"
)

// newOpenCmd returns the file open command.
func newOpenCmd(ctx context.DnoteCtx) *cobra.Command {
	return &cobra.Command{
		Use:   "open <path>",
		Short: "Edit a file as a node outline; saving writes the file back",
		Long: `Open a supported file in the inline node editor. The file parses into a
node tree in a throwaway scratch database — never the real outline — and
every save (ctrl+s, quit) serializes the tree back to the file.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return errors.New("missing file path")
			}
			return runOpen(ctx, args[0])
		},
	}
}

func runOpen(ctx context.DnoteCtx, path string) error {
	path, err := filepath.Abs(path)
	if err != nil {
		return errors.Wrap(err, "resolving path")
	}
	codec, ok := nodes.CodecForPath(path)
	if !ok {
		return errors.Errorf("unsupported file type %q: supported are %s",
			filepath.Ext(path), strings.Join(nodes.CodecExts(), ", "))
	}

	src, mtime, err := readSource(path)
	if err != nil {
		return err
	}

	// the scratch database: a throwaway file DB owned directly by this
	// process (no daemon), discarded on exit — the source file is the only
	// thing that persists.
	scratchDir, err := os.MkdirTemp("", "lflow-file-")
	if err != nil {
		return errors.Wrap(err, "creating scratch dir")
	}
	defer os.RemoveAll(scratchDir)

	db, err := database.Open(filepath.Join(scratchDir, "doc.db"))
	if err != nil {
		return errors.Wrap(err, "opening scratch db")
	}
	defer db.Close()
	if err := infra.PrepareDB(db, ctx.Version); err != nil {
		return errors.Wrap(err, "preparing scratch db")
	}
	if err := database.EnsureRoot(db); err != nil {
		return err
	}
	if err := database.EnsureTemp(db); err != nil {
		return err
	}
	// the editor header shows the file, not "Root"
	if _, err := db.Exec("UPDATE nodes SET name = ? WHERE uuid = ?",
		filepath.Base(path), database.RootUUID); err != nil {
		return errors.Wrap(err, "naming doc root")
	}

	if err := codec.Parse(db, database.RootUUID, src); err != nil {
		return errors.Wrapf(err, "parsing %s", filepath.Base(path))
	}

	fileCtx := ctx
	fileCtx.DB = db
	fileCtx.Live = nil // direct scratch handle: no daemon, no live sync

	onSave := func() error {
		out, err := codec.Render(db, database.RootUUID)
		if err != nil {
			return errors.Wrap(err, "rendering "+codec.Name())
		}
		newMtime, err := writeSource(path, out, mtime)
		if err != nil {
			return err
		}
		mtime = newMtime
		return nil
	}

	if err := editor.RunWithOnSave(fileCtx, database.RootUUID, onSave); err != nil {
		return err
	}
	log.Plainf("→ wrote %s\n", path)
	return nil
}

// readSource loads the file, tolerating a missing one (a fresh document that
// materializes on first save).
func readSource(path string) (string, time.Time, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", time.Time{}, nil
	}
	if err != nil {
		return "", time.Time{}, errors.Wrap(err, "reading file")
	}
	fi, err := os.Stat(path)
	if err != nil {
		return "", time.Time{}, errors.Wrap(err, "stating file")
	}
	return string(b), fi.ModTime(), nil
}

// writeSource writes the rendered document atomically (temp file + rename in
// the same directory) with a last-writer guard: if someone else touched the
// file since we read or last wrote it, refuse instead of clobbering — the
// edits stay in the editor and save again after the conflict is resolved.
func writeSource(path, content string, lastMtime time.Time) (time.Time, error) {
	if fi, err := os.Stat(path); err == nil {
		if !lastMtime.IsZero() && !fi.ModTime().Equal(lastMtime) {
			return time.Time{}, errors.Errorf("%s changed on disk — not overwriting", filepath.Base(path))
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".lflow-*")
	if err != nil {
		return time.Time{}, errors.Wrap(err, "creating temp file")
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return time.Time{}, errors.Wrap(err, "writing temp file")
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return time.Time{}, errors.Wrap(err, "closing temp file")
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return time.Time{}, errors.Wrap(err, "replacing file")
	}
	fi, err := os.Stat(path)
	if err != nil {
		return time.Time{}, errors.Wrap(err, "stating written file")
	}
	return fi.ModTime(), nil
}
