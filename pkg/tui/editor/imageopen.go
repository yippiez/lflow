package editor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/pkg/errors"
)

// alt+o: show the picture in the HOST's image viewer, outside the terminal.
//
// A terminal cell cannot carry pixels, and the alternative — blitting a
// kitty/sixel/iTerm2 payload out-of-band — was a bad trade: it suspends the
// editor, works on a minority of terminals, cannot be screenshotted or reviewed,
// and degrades to a blank screen everywhere else. The desktop already owns a
// good image viewer, so alt+o writes the node's blob to a cache file and hands
// it over. Inline rendering stays what it always was: unicode half-blocks.
//
// WARNING (invariant): the editor never blocks on the viewer. The open runs in
// a tea.Cmd and the child is Start-ed, not Wait-ed — the picture opens in its own
// window while the outline keeps taking keys.

// imageOpenedMsg reports an alt+o back to Update, which flashes the outcome.
type imageOpenedMsg struct {
	uuid string
	via  string // the opener that ran, named in the flash
	err  error
}

// imageCachePath is where a node's blob is materialized for the host viewer:
// one stable file per node under the cache dir, overwritten on re-open, so
// repeated alt+o cannot litter /tmp. It is a CACHE — never the source of truth,
// which stays the node_blobs row.
func imageCachePath(uuid string) (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "lflow", "images")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", errors.Wrap(err, "creating the image cache dir")
	}
	return filepath.Join(dir, uuid+".png"), nil
}

// isWSL reports whether lflow runs under WSL, where the "host" is Windows and
// the viewer must be launched through the interop layer. WSL_DISTRO_NAME is set
// by WSL2 itself; /proc/version carries "microsoft" on both WSL 1 and 2, so the
// pair covers the cases where a login shell has scrubbed the environment.
func isWSL() bool {
	if os.Getenv("WSL_DISTRO_NAME") != "" || os.Getenv("WSL_INTEROP") != "" {
		return true
	}
	b, err := os.ReadFile("/proc/version")
	return err == nil && strings.Contains(strings.ToLower(string(b)), "microsoft")
}

// hostOpener returns the command that opens path in the desktop's default app,
// and the name to flash. First hit wins:
//
//   - WSL: wslview (wslu) when installed — it is the supported way to hand a
//     path to Windows. Otherwise explorer.exe with a translated \\wsl.localhost
//     path, which opens the file with its default Windows program. (explorer.exe
//     exits 1 even on success, so the caller must not read its status.)
//   - macOS: open. Linux/BSD: xdg-open.
//
// ok=false means there is no way to reach a desktop from here — a bare SSH
// session, say — and alt+o says so instead of failing silently.
func hostOpener(path string) (cmd *exec.Cmd, via string, ok bool) {
	if isWSL() {
		if bin, err := exec.LookPath("wslview"); err == nil {
			return exec.Command(bin, path), "wslview", true
		}
		if bin, err := exec.LookPath("explorer.exe"); err == nil {
			win := path
			if out, err := exec.Command("wslpath", "-w", path).Output(); err == nil {
				win = strings.TrimSpace(string(out))
			}
			return exec.Command(bin, win), "explorer.exe", true
		}
		return nil, "", false
	}
	for _, bin := range []string{"open", "xdg-open"} {
		if p, err := exec.LookPath(bin); err == nil {
			return exec.Command(p, path), bin, true
		}
	}
	return nil, "", false
}

// imageOpenHost (alt+o, and the image type's "open" flash action) materializes
// the node's PNG and launches the host viewer on it. Everything slow — the blob
// read, the wslpath translation, the spawn — happens inside the returned Cmd, so
// the editor never stalls on a cold interop layer.
func imageOpenHost(m *Model, it *item) tea.Cmd {
	if it == nil {
		return nil
	}
	uuid := it.uuid
	if _, ok := m.imageLoad(uuid); !ok {
		m.flash = "image: nothing to open — ⌥r pastes one"
		return nil
	}
	db := m.db
	return func() tea.Msg {
		blob, ok, err := database.GetBlob(db, uuid)
		if err != nil || !ok {
			if err == nil {
				err = errors.New("no image data")
			}
			return imageOpenedMsg{uuid: uuid, err: err}
		}
		path, err := imageCachePath(uuid)
		if err != nil {
			return imageOpenedMsg{uuid: uuid, err: err}
		}
		if err := os.WriteFile(path, blob.Bytes, 0o644); err != nil {
			return imageOpenedMsg{uuid: uuid, err: errors.Wrap(err, "writing the image cache")}
		}
		cmd, via, ok := hostOpener(path)
		if !ok {
			return imageOpenedMsg{uuid: uuid, err: errors.New("no host image viewer (no wslview/explorer.exe/open/xdg-open)")}
		}
		// Start, never Wait: the viewer owns its own window and may outlive the
		// editor. Its output would otherwise land on the terminal mid-frame.
		cmd.Stdout, cmd.Stderr = nil, nil
		if err := cmd.Start(); err != nil {
			return imageOpenedMsg{uuid: uuid, via: via, err: errors.Wrapf(err, "launching %s", via)}
		}
		go func() { _ = cmd.Wait() }() // reap it; the editor does not care when
		return imageOpenedMsg{uuid: uuid, via: via}
	}
}
