package file

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"

	"github.com/lflow/lflow/packages/app"
	"github.com/lflow/lflow/packages/cli/infra"
	"github.com/lflow/lflow/packages/database"
	"github.com/lflow/lflow/packages/editor"
	"github.com/lflow/lflow/packages/nodes"
	"github.com/lflow/lflow/packages/utils/log"
)

// newOpenCmd returns the file open command.
func newOpenCmd(ctx app.Ctx) *cobra.Command {
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

func runOpen(ctx app.Ctx, path string) error {
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
	// the scratch DB is discarded only after a clean exit: if the session ends
	// on an error (say the file changed on disk and the final save refused to
	// overwrite), the DB is the ONLY surviving copy of the edits — keep it and
	// say where it is instead of deleting the user's work with the error.
	keepScratch := false
	defer func() {
		if keepScratch {
			log.Plainf("→ session edits kept in %s\n", scratchDir)
			return
		}
		os.RemoveAll(scratchDir)
	}()

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

	if err := nodes.ParseIntoDB(db, database.RootUUID, codec, src); err != nil {
		return errors.Wrapf(err, "parsing %s", filepath.Base(path))
	}

	fileCtx := ctx
	fileCtx.DB = db
	fileCtx.Live = nil // direct scratch handle: no daemon, no live sync

	onSave := func() error {
		out, err := nodes.RenderFromDB(db, database.RootUUID, codec)
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

	if err := editor.RunFile(fileCtx, database.RootUUID, editor.FileSession{
		OnSave:       onSave,
		AllowedTypes: codec.Allowed(),
		DefaultType:  codec.DefaultType(),
	}); err != nil {
		keepScratch = true
		return err
	}
	log.Plainf("→ wrote %s\n", path)
	return nil
}

// readSourceMaxAttempts bounds the stat-read-stat retry in readSource.
const readSourceMaxAttempts = 3

// readSource loads the file, tolerating a missing one (a fresh document that
// materializes on first save). It stats both before and after the read: a
// write landing between the two (some other process saving mid-ReadFile)
// would otherwise be silently adopted as our baseline mtime and then
// clobbered by our own first save. A changed mtime retries the whole
// read a bounded number of times before giving up.
func readSource(path string) (string, time.Time, error) {
	for attempt := 0; attempt < readSourceMaxAttempts; attempt++ {
		before, err := os.Stat(path)
		if os.IsNotExist(err) {
			return "", time.Time{}, nil
		}
		if err != nil {
			return "", time.Time{}, errors.Wrap(err, "stating file")
		}
		b, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			return "", time.Time{}, nil
		}
		if err != nil {
			return "", time.Time{}, errors.Wrap(err, "reading file")
		}
		after, err := os.Stat(path)
		if os.IsNotExist(err) {
			return "", time.Time{}, nil
		}
		if err != nil {
			return "", time.Time{}, errors.Wrap(err, "stating file")
		}
		if before.ModTime().Equal(after.ModTime()) {
			return string(b), after.ModTime(), nil
		}
		// the file changed while we were reading it — retry against the new state
	}
	return "", time.Time{}, errors.Errorf("%s kept changing while opening — try again", filepath.Base(path))
}

// writeSource writes the rendered document atomically (temp file + rename in
// the same directory) with a last-writer guard: if someone else touched the
// file since we read or last wrote it, refuse instead of clobbering — the
// edits stay in the editor and save again after the conflict is resolved.
// lastMtime.IsZero() means the file did not exist when the session opened
// (readSource's missing-file case) — that is its own state, distinct from
// "existed and we've saved since": if the file now exists, someone created
// it while we were editing, and the first save must refuse rather than
// silently overwrite their work.
func writeSource(path, content string, lastMtime time.Time) (time.Time, error) {
	mode := os.FileMode(0o644) // fresh file default; an existing file keeps its bits
	if fi, err := os.Stat(path); err == nil {
		if lastMtime.IsZero() {
			return time.Time{}, errors.Errorf("%s created on disk — not overwriting", filepath.Base(path))
		}
		if !fi.ModTime().Equal(lastMtime) {
			return time.Time{}, errors.Errorf("%s changed on disk — not overwriting", filepath.Base(path))
		}
		mode = fi.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".lflow-*")
	if err != nil {
		return time.Time{}, errors.Wrap(err, "creating temp file")
	}
	// CreateTemp makes 0600 — restore the target's own permissions (exec bits,
	// group readability) so saving never strips them.
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return time.Time{}, errors.Wrap(err, "restoring file mode")
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
