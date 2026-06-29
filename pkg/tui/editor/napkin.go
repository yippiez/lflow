package editor

import (
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/browser"
	"github.com/lflow/lflow/pkg/tui/database"
)

// A napkin node is a little drawing. alt+e launches a small drawing app in the
// browser (lflow serves it from a temporary localhost server and opens it); when
// you click Save the canvas PNG is posted back and stored as a local file
// (~/.local/share/lflow/napkin/<uuid>.png), never the synced DB — same rule as
// voice audio. In the outline the node shows a dark-gray ◼ square plus a small
// preview of the drawing next to its text: the real image via a terminal graphics
// protocol (Kitty/iTerm2) when the terminal supports one, otherwise a downsampled
// half-block thumbnail.

func sgrFG(c [3]int) string { return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", c[0], c[1], c[2]) }
func sgrBG(c [3]int) string { return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", c[0], c[1], c[2]) }

// napkinGlyph is the dark-gray square shown for every napkin node.
func napkinGlyph(it *item) (string, string) { return glyphNapkin, cDim }

func (m *Model) napkinPath(uuid string) string {
	return filepath.Join(m.ctx.Paths.Data, "lflow", "napkin", uuid+".png")
}

// napkinSavedMsg is delivered when the browser app posts a drawing back (ok) or
// the page is closed without saving (ok=false).
type napkinSavedMsg struct {
	uuid string
	ok   bool
}

// napkinBodySuffix appends a napkin's inline drawing preview to its rendered body;
// a no-op for other node types. Centralizes the per-row hook so every render site
// (outline, final view, temp panel) stays in sync.
func (m *Model) napkinBodySuffix(it *item, body string) string {
	if it.typ == database.TypeNapkin {
		return body + "  " + m.napkinThumb(it)
	}
	return body
}

// napkinThumb is the inline preview next to the node text. It decodes the PNG and
// renders a compact half-block thumbnail (the protocol-agnostic fallback), cached
// by file modtime so the PNG is decoded only when it changes. An empty napkin
// shows a dim "⌥e draw" hint.
func (m *Model) napkinThumb(it *item) string {
	path := m.napkinPath(it.uuid)
	fi, err := os.Stat(path)
	if err != nil {
		return cDim + "⌥e draw" + cReset
	}
	d := m.nodeStore(it.uuid)
	mod := fi.ModTime().UnixNano()
	if d["napkinThumbMod"] == mod {
		if s, ok := d["napkinThumb"].(string); ok {
			return s
		}
	}
	s := cDim + "(image)" + cReset
	if f, err := os.Open(path); err == nil {
		if img, err := png.Decode(f); err == nil {
			s = napkinThumbFromImage(img)
		}
		f.Close()
	}
	d["napkinThumb"] = s
	d["napkinThumbMod"] = mod
	return s
}

const napkinThumbCells = 24

// napkinThumbFromImage downsamples an image to a one-line half-block strip (▀ with
// fg = top half color, bg = bottom half color). Mostly-transparent cells render as
// a space so sparse drawings keep their gaps.
func napkinThumbFromImage(img image.Image) string {
	bnd := img.Bounds()
	W, H := bnd.Dx(), bnd.Dy()
	if W == 0 || H == 0 {
		return cDim + "⌥e draw" + cReset
	}
	avg := func(cx, band int) ([3]int, bool) {
		x0 := bnd.Min.X + cx*W/napkinThumbCells
		x1 := bnd.Min.X + (cx+1)*W/napkinThumbCells
		y0 := bnd.Min.Y + band*H/2
		y1 := bnd.Min.Y + (band+1)*H/2
		var rs, gs, bs, n int
		for y := y0; y < y1; y++ {
			for x := x0; x < x1; x++ {
				r, g, b, a := img.At(x, y).RGBA()
				if a < 0x8000 {
					continue // transparent
				}
				rs += int(r >> 8)
				gs += int(g >> 8)
				bs += int(b >> 8)
				n++
			}
		}
		total := (x1 - x0) * (y1 - y0)
		if total <= 0 || n*4 < total { // <25% opaque → treat as empty
			return [3]int{}, false
		}
		return [3]int{rs / n, gs / n, bs / n}, true
	}
	var b strings.Builder
	for cx := 0; cx < napkinThumbCells; cx++ {
		t, ti := avg(cx, 0)
		bt, bi := avg(cx, 1)
		switch {
		case !ti && !bi:
			b.WriteString(" ")
		case ti && !bi:
			b.WriteString(sgrFG(t) + "▀" + cReset)
		case !ti && bi:
			b.WriteString(sgrFG(bt) + "▄" + cReset)
		default:
			b.WriteString(sgrFG(t) + sgrBG(bt) + "▀" + cReset)
		}
	}
	return b.String()
}

// launchNapkin (alt+e) serves the drawing app from a temporary localhost server,
// opens it in the browser, and returns a Cmd that waits for the Save/close
// callback and then shuts the server down. The returned Cmd runs in its own
// goroutine and touches no Model state.
func (m *Model) launchNapkin(it *item) tea.Cmd {
	uuid := it.uuid
	path := m.napkinPath(uuid)
	existing, _ := os.ReadFile(path) // nil when there is no drawing yet

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		m.err = err
		return nil
	}
	done := make(chan bool, 1)
	signal := func(v bool) {
		select {
		case done <- v:
		default:
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, napkinAppHTML)
	})
	mux.HandleFunc("/image", func(w http.ResponseWriter, r *http.Request) {
		if existing == nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(existing)
	})
	mux.HandleFunc("/save", func(w http.ResponseWriter, r *http.Request) {
		if body, err := io.ReadAll(r.Body); err == nil {
			if data, err := decodeDataURL(body); err == nil {
				_ = os.MkdirAll(filepath.Dir(path), 0o755)
				_ = os.WriteFile(path, data, 0o644)
			}
		}
		io.WriteString(w, "ok")
		signal(true)
	})
	mux.HandleFunc("/cancel", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
		signal(false)
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)

	url := "http://" + ln.Addr().String() + "/"
	if err := browser.Open(url); err != nil {
		m.flash = "draw at " + url
	} else {
		m.flash = "drawing in browser…"
	}
	return func() tea.Msg {
		ok := <-done
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		return napkinSavedMsg{uuid: uuid, ok: ok}
	}
}

// decodeDataURL extracts the PNG bytes from a "data:image/png;base64,…" payload.
func decodeDataURL(b []byte) ([]byte, error) {
	s := string(b)
	i := strings.Index(s, "base64,")
	if i < 0 {
		return nil, fmt.Errorf("napkin: not a data url")
	}
	return base64.StdEncoding.DecodeString(strings.TrimSpace(s[i+7:]))
}
