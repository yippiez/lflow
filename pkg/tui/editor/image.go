package editor

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"  // register decoders so clipboard images in any of these decode
	_ "image/jpeg" // …
	"image/png"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/pkg/errors"
)

// An image node: alt+r pastes the host clipboard image, alt+e opens a scrollable
// half-block preview. The pixels are a PNG BLOB in the node_blobs table keyed by
// node uuid — so the whole outline stays a single portable SQLite file (copy the
// .db and the images travel with it). The display degrades gracefully: inline it
// is a one-line color strip + dimensions, and the alt+e view is a larger
// half-block render, both built from SGR-colored cells so they are safe for the
// inline (no-alt-screen) renderer and survive over SSH and in the scrollback
// dump. A true, protocol-native image (kitty/sixel/iTerm2) is a later tier that
// blits out-of-band.

// halfBlock is the upper-half-block glyph: its foreground paints the top pixel of
// a cell and its background the bottom, so one text row shows two pixel rows.
const halfBlock = "▀"

const imageStripCols = 16 // inline color-strip width (one text row, "compact" mode)

// "true" preview mode hangs an aspect-correct half-block thumbnail beneath the
// node, bounded so it stays a preview and never dominates the outline.
const (
	thumbMaxCols = 32
	thumbMaxRows = 12
)

// pasteSpinner animates the "pasting…" indicator while the async clipboard grab
// (powershell in WSL can be slow) is in flight, so the node never looks frozen.
var pasteSpinner = []rune("⣾⣽⣻⢿⡿⣟⣯⣷")

// imageMaxBytes caps a pasted image so the DB can't balloon; a screenshot is
// well under this. Larger pastes are refused with a flash.
const imageMaxBytes = 10 << 20 // 10 MB

// imagePastedMsg carries the result of an async clipboard grab back to Update,
// which does the DB write on the main goroutine (no concurrent DB access).
type imagePastedMsg struct {
	uuid string
	data []byte // normalized PNG bytes; nil on error
	w, h int
	err  error
}

// ── decoded-image cache (ephemeral per-node store, like the voice envelope) ──

type imageInfo struct {
	img  image.Image
	w, h int
	size int64
}

// imageLoad returns the node's decoded image, loading it from its DB blob and
// caching the decode on first use. ok=false means there is no (valid) image yet.
func (m *Model) imageLoad(uuid string) (imageInfo, bool) {
	d := m.nodeStore(uuid)
	if info, ok := d["imgInfo"].(imageInfo); ok {
		return info, true
	}
	blob, ok, err := database.GetBlob(m.db, uuid)
	if err != nil || !ok {
		return imageInfo{}, false
	}
	img, _, err := image.Decode(bytes.NewReader(blob.Bytes))
	if err != nil {
		return imageInfo{}, false
	}
	b := img.Bounds()
	info := imageInfo{img: img, w: b.Dx(), h: b.Dy(), size: int64(len(blob.Bytes))}
	d["imgInfo"] = info
	return info, true
}

// imageInvalidate drops the cached decode so the next render reloads from the DB —
// called after a fresh paste replaces the blob.
func (m *Model) imageInvalidate(uuid string) { delete(m.nodeStore(uuid), "imgInfo") }

// imagePasting reports whether a clipboard grab is in flight for the node, and
// anyImagePasting whether any node has one — used to keep the animation tick alive
// so the spinner spins.
func (m *Model) imagePasting(uuid string) bool {
	p, _ := m.nodeStore(uuid)["imgPasting"].(bool)
	return p
}
func (m *Model) setImagePasting(uuid string, v bool) {
	if v {
		m.nodeStore(uuid)["imgPasting"] = true
	} else {
		delete(m.nodeStore(uuid), "imgPasting")
	}
}
func (m *Model) anyImagePasting() bool {
	for _, d := range m.nodeData {
		if p, _ := d["imgPasting"].(bool); p {
			return true
		}
	}
	return false
}

// imageRender is the inline header line: a ▦ marker, dimensions and size. In
// "compact" preview mode it prefixes a one-row half-block color strip; in "true"
// mode the picture hangs beneath as a thumbnail band (see imageBandLines) so the
// header omits the strip. While a paste is in flight it shows an animated spinner;
// empty nodes prompt to paste. An optional caption (the node name) tails the line.
func (m *Model) imageRender(it *item) string {
	caption := strings.TrimSpace(stripControlBytes(it.name))
	tail := ""
	if caption != "" {
		tail = cDim + " · " + cReset + cFG + caption + cReset
	}
	if m.imagePasting(it.uuid) {
		spin := string(pasteSpinner[animFrame%len(pasteSpinner)])
		return cDim + "▦ pasting… " + cReset + cAccent + spin + cReset + tail
	}
	info, ok := m.imageLoad(it.uuid)
	if !ok {
		if caption != "" {
			return cDim + "▦ " + cReset + cFG + caption + cReset + cDim + " · empty · ⌥r paste" + cReset
		}
		return cDim + "▦ empty · ⌥r paste" + cReset
	}
	meta := fmt.Sprintf("%d×%d · %s", info.w, info.h, humanSize(info.size))
	if m.setting("image.preview") == "true" {
		return cDim + "▦ " + cReset + cDim + meta + " · ⌥e larger" + cReset + tail
	}
	strip := strings.Join(halfBlockRender(info.img, imageStripCols, 1), "")
	return cDim + "▦ " + cReset + strip + cReset + "  " + cDim + meta + " · ⌥e view" + cReset + tail
}

// imageBandLines renders the aspect-correct half-block thumbnail as bands beneath
// the node (like the note / run-output bands) when the "true" preview mode is on.
// Returns nil in "compact" mode, when there is no image, or when the node is
// focused (the larger alt+e view takes over — handled by the caller).
func (m *Model) imageBandLines(r row, subtreeBelow bool, maxLine int) []string {
	if m.setting("image.preview") != "true" {
		return nil
	}
	info, ok := m.imageLoad(r.it.uuid)
	if !ok {
		return nil
	}
	rail := continuationPrefix(r, subtreeBelow)
	avail := maxLine - visibleWidth(rail) - 2
	if avail < 4 {
		return nil
	}
	cols, rows := halfBlockBox(info.img, min(avail, thumbMaxCols), thumbMaxRows)
	out := make([]string, 0, rows)
	for _, l := range halfBlockRender(info.img, cols, rows) {
		out = append(out, clip(rail+cReset+"  "+l, maxLine))
	}
	return out
}

// imageFlashActions names an image node's flash actions: alt+r pastes (re-pastes
// if an image is already present), alt+e views the half-block preview.
func imageFlashActions(m *Model, it *item) []flashAction {
	paste := "paste"
	if _, ok := m.imageLoad(it.uuid); ok {
		paste = "repaste"
	}
	acts := []flashAction{{verb: paste, color: cGreen, do: runImagePaste}}
	if _, ok := m.imageLoad(it.uuid); ok {
		acts = append(acts,
			flashAction{verb: "view", color: cCyan, do: imageExpandDo},
			flashAction{verb: "show", color: cMagenta, do: imageShowDo}, // full-res via graphics protocol
		)
	}
	return acts
}

// imageExpandDo focuses the inline half-block view. It is a direct handler (not
// the generic flashExpandDo, which reaches typeOf/nodeViewOf) so the registry's
// static reference to it doesn't form a package init cycle.
func imageExpandDo(m *Model, it *item) tea.Cmd {
	if (imageView{}).Enter(m, it) {
		m.focused = true
		m.focusScroll = 0
	}
	return nil
}

// runImagePaste grabs the host clipboard image asynchronously (powershell.exe in
// WSL can be slow) and hands the normalized PNG bytes to Update via imagePastedMsg,
// which does the DB write on the main goroutine.
func runImagePaste(m *Model, it *item) tea.Cmd {
	uuid := it.uuid
	m.setImagePasting(uuid, true) // spinner on until imagePastedMsg clears it
	return func() tea.Msg {
		data, w, h, err := grabClipboardImage()
		return imagePastedMsg{uuid: uuid, data: data, w: w, h: h, err: err}
	}
}

// ── clipboard ingestion (cross-host) ────────────────────────────────────────

// grabClipboardImage returns the host clipboard's image as normalized PNG bytes
// plus its dimensions. It probes the platforms lflow runs on, first hit wins:
// WSL/Windows via powershell, macOS via pngpaste, Wayland via wl-paste, X11 via
// xclip. Over SSH this reads the clipboard of the machine running lflow — paste on
// that host.
func grabClipboardImage() ([]byte, int, int, error) {
	// WSL/Windows: powershell can't stream binary to stdout cleanly, so it saves to
	// a temp file we read and remove.
	if raw, ok := clipWindowsGrab(); ok {
		return normalizePNG(raw)
	}
	// stdout-based tools: capture bytes directly.
	for _, c := range []struct {
		name string
		args []string
	}{
		{"pngpaste", []string{"-"}},                                             // macOS
		{"wl-paste", []string{"--no-newline", "--type", "image/png"}},           // Wayland
		{"xclip", []string{"-selection", "clipboard", "-t", "image/png", "-o"}}, // X11
	} {
		if _, err := exec.LookPath(c.name); err != nil {
			continue
		}
		out, err := exec.Command(c.name, c.args...).Output()
		if err != nil || len(out) == 0 {
			continue
		}
		return normalizePNG(out)
	}
	return nil, 0, 0, errors.New("no clipboard image (need wl-paste/xclip/pngpaste, or paste on the host running lflow)")
}

// clipWindowsGrab asks powershell.exe (present under WSL) to save the clipboard
// image to a temp file, then reads and removes it. Returns ok=false when
// powershell is absent or the clipboard holds no image.
func clipWindowsGrab() ([]byte, bool) {
	if _, err := exec.LookPath("powershell.exe"); err != nil {
		return nil, false
	}
	tmp, err := os.CreateTemp("", "lflow-clip-*.png")
	if err != nil {
		return nil, false
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	win, err := exec.Command("wslpath", "-w", tmp.Name()).Output()
	winPath := strings.TrimSpace(string(win))
	if err != nil || winPath == "" || strings.ContainsAny(winPath, "'\n") {
		return nil, false
	}
	ps := "Add-Type -AssemblyName System.Windows.Forms,System.Drawing; " +
		"$i=[Windows.Forms.Clipboard]::GetImage(); " +
		"if($i -ne $null){ $i.Save('" + winPath + "',[System.Drawing.Imaging.ImageFormat]::Png) }"
	if err := exec.Command("powershell.exe", "-NoProfile", "-Command", ps).Run(); err != nil {
		return nil, false
	}
	raw, err := os.ReadFile(tmp.Name())
	if err != nil || len(raw) == 0 {
		return nil, false
	}
	return raw, true
}

// normalizePNG decodes clipboard bytes (png/jpeg/gif), enforces the size cap and
// re-encodes as PNG, returning the bytes and dimensions.
func normalizePNG(data []byte) ([]byte, int, int, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, errors.Wrap(err, "decoding clipboard image")
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, 0, 0, errors.Wrap(err, "encoding png")
	}
	if buf.Len() > imageMaxBytes {
		return nil, 0, 0, errors.Errorf("image too large (%s > %s)", humanSize(int64(buf.Len())), humanSize(imageMaxBytes))
	}
	b := img.Bounds()
	return buf.Bytes(), b.Dx(), b.Dy(), nil
}

// ── half-block rendering (renderer-safe: SGR-colored cells only) ─────────────

// halfBlockRender samples img into a cols×(rows*2) pixel grid and returns rows
// text lines of ▀ cells — top pixel as foreground, bottom as background — so each
// text row shows two pixel rows. Aspect is the caller's responsibility (see
// halfBlockFit); this maps the grid onto the image by nearest neighbor.
func halfBlockRender(img image.Image, cols, rows int) []string {
	if cols < 1 || rows < 1 {
		return nil
	}
	b := img.Bounds()
	iw, ih := b.Dx(), b.Dy()
	if iw < 1 || ih < 1 {
		return nil
	}
	sample := func(cx, cy, gw, gh int) (uint8, uint8, uint8) {
		px := b.Min.X + cx*iw/gw
		py := b.Min.Y + cy*ih/gh
		r, g, bl, _ := img.At(px, py).RGBA()
		return uint8(r >> 8), uint8(g >> 8), uint8(bl >> 8)
	}
	gh := rows * 2
	out := make([]string, 0, rows)
	for row := 0; row < rows; row++ {
		var sb strings.Builder
		for col := 0; col < cols; col++ {
			tr, tg, tb := sample(col, row*2, cols, gh)
			br, bg, bb := sample(col, row*2+1, cols, gh)
			fmt.Fprintf(&sb, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm%s", tr, tg, tb, br, bg, bb, halfBlock)
		}
		sb.WriteString(cReset)
		out = append(out, sb.String())
	}
	return out
}

// halfBlockBox fits the image into a maxCols×maxRows cell box, preserving aspect:
// it fills the width, and if that overflows the height it shrinks the width to fit
// the row cap instead. Keeps a tall image from running off the outline.
func halfBlockBox(img image.Image, maxCols, maxRows int) (cols, rows int) {
	b := img.Bounds()
	iw, ih := b.Dx(), b.Dy()
	if iw < 1 || ih < 1 || maxCols < 1 || maxRows < 1 {
		return 1, 1
	}
	cols = maxCols
	rows = cols * ih / iw / 2
	if rows < 1 {
		rows = 1
	}
	if rows > maxRows {
		rows = maxRows
		cols = rows * 2 * iw / ih
		if cols < 1 {
			cols = 1
		}
		if cols > maxCols {
			cols = maxCols
		}
	}
	return cols, rows
}

// halfBlockFit picks a rows count for a target column width that preserves the
// image's aspect ratio, given that each text row is two pixels tall and a
// terminal cell is roughly twice as tall as it is wide.
func halfBlockFit(img image.Image, cols int) int {
	b := img.Bounds()
	if b.Dx() < 1 {
		return 1
	}
	rows := cols * b.Dy() / b.Dx() / 2
	if rows < 1 {
		rows = 1
	}
	return rows
}

// humanSize renders a byte count as a compact human string (B/KB/MB).
func humanSize(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%dB", n)
	case n < 1024*1024:
		return fmt.Sprintf("%dKB", n/1024)
	default:
		return fmt.Sprintf("%.1fMB", float64(n)/(1024*1024))
	}
}

// ── imageView: the alt+e inline expanded preview (half-block bands) ──────────

// imageView renders the image as a scrollable half-block picture in bands beneath
// the node — never a separate screen. It is stateless; the decoded image lives in
// the per-node store. It captures no text keys (nothing to edit) so arrows/esc
// fall through to central scroll/defocus handling.
type imageView struct{}

func (imageView) Enter(m *Model, it *item) bool {
	_, ok := m.imageLoad(it.uuid)
	return ok // decline if there is no image to show
}

func (imageView) Leave(m *Model, it *item) {}

// imageViewLines is the total band height at a given width: a header line plus the
// aspect-fit half-block rows.
func imageViewLines(m *Model, it *item, width int) int {
	info, ok := m.imageLoad(it.uuid)
	if !ok {
		return 1
	}
	cols := width - 4
	if cols < 1 {
		cols = 1
	}
	return 1 + halfBlockFit(info.img, cols)
}

func (imageView) Lines(m *Model, it *item, width int) int { return imageViewLines(m, it, width) }

// Key: enter blits the full-resolution image via the terminal graphics protocol
// (out-of-band suspend); up/down (and pgup/pgdn) scroll the view so a tall image
// whose bottom is off-screen can be read — the render loop clamps the offset to
// the content height. esc (defocus) is handled centrally.
func (imageView) Key(m *Model, it *item, k tea.KeyMsg) (tea.Cmd, bool) {
	switch k.String() {
	case "enter":
		return m.showImageProtocol(it), true
	case "up", "k":
		if m.focusScroll > 0 {
			m.focusScroll--
		}
		return nil, true
	case "down", "j":
		m.focusScroll++ // upper bound is clamped where the view is rendered
		return nil, true
	case "pgup":
		m.focusScroll -= 6
		if m.focusScroll < 0 {
			m.focusScroll = 0
		}
		return nil, true
	case "pgdown":
		m.focusScroll += 6
		return nil, true
	}
	return nil, false
}

// Bands renders the header + half-block picture, self-windowed to [scroll,
// scroll+winH).
func (imageView) Bands(m *Model, it *item, rail string, width, scroll, winH int, focused bool) []string {
	info, ok := m.imageLoad(it.uuid)
	if !ok {
		return []string{clip(rail+cReset+cDim+"  image · empty · ⌥r paste"+cReset, width)}
	}
	// fit the picture to the space left of the tree rail and the 2-space indent, so
	// the row never overflows and gets clip-truncated with an "…". Lines() slightly
	// overestimates (it has no rail) — safe, it only loosens the scroll clamp.
	cols := width - visibleWidth(rail) - 3
	if cols < 1 {
		cols = 1
	}
	rows := halfBlockFit(info.img, cols)
	var content []string
	action := "esc close"
	if detectGraphicsProto() != protoNone {
		action = "enter: full-res · esc close"
	}
	header := fmt.Sprintf("  image · %d×%d · %s · %s", info.w, info.h, humanSize(info.size), action)
	content = append(content, clip(rail+cReset+cDim+header+cReset, width))
	for _, line := range halfBlockRender(info.img, cols, rows) {
		content = append(content, clip(rail+cReset+"  "+line, width))
	}
	if scroll > len(content) {
		scroll = len(content)
	}
	end := scroll + winH
	if end > len(content) {
		end = len(content)
	}
	return content[scroll:end]
}
