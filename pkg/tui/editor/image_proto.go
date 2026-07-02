package editor

import (
	"bytes"
	"encoding/base64"
	"image"
	"os"
	"os/exec"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lflow/lflow/pkg/tui/database"
)

// Tier 3: a true, protocol-native image. The inline (no-alt-screen) renderer can
// never carry a graphics escape in a live frame (visibleWidth only understands
// SGR sequences ending in 'm'; a kitty/iTerm2 payload would desync the diff), so
// the full-resolution image is shown OUT-OF-BAND: tea.ExecProcess suspends the
// editor and hands the terminal to a child that blits the sequence and waits for
// a key — exactly how the file picker / $EDITOR already suspend. On return the
// editor repaints; the image stays in scrollback, consistent with lflow's design.
//
// Enter (while the alt+e view is focused) or the flash "show" action triggers it.
// When no protocol is detected the half-block alt+e view is the fallback.

type graphicsProto int

const (
	protoNone graphicsProto = iota
	protoKitty
	protoITerm
	protoSixel
)

// detectGraphicsProto guesses the terminal's image protocol from the environment.
// Kitty's protocol is preferred (higher fidelity, file-transmit) and is spoken by
// kitty, Ghostty, WezTerm and modern Konsole; iTerm2's inline-image protocol
// covers iTerm2; sixel covers foot / mlterm / contour / xterm-with-sixel. Env
// heuristics beat a runtime DA query here — the query/response dance is fragile
// mid-program — and are imperfect for sixel, so LFLOW_IMAGE_PROTO overrides the
// guess (kitty|iterm|sixel|none) for terminals we can't sniff or to force a mode.
func detectGraphicsProto() graphicsProto {
	switch strings.ToLower(os.Getenv("LFLOW_IMAGE_PROTO")) {
	case "kitty":
		return protoKitty
	case "iterm", "iterm2":
		return protoITerm
	case "sixel":
		return protoSixel
	case "none", "off":
		return protoNone
	}
	term := os.Getenv("TERM")
	termProg := os.Getenv("TERM_PROGRAM")
	switch {
	case os.Getenv("KITTY_WINDOW_ID") != "" || strings.Contains(term, "kitty"):
		return protoKitty
	case strings.Contains(term, "ghostty") || os.Getenv("GHOSTTY_RESOURCES_DIR") != "":
		return protoKitty
	case termProg == "WezTerm" || os.Getenv("WEZTERM_PANE") != "":
		return protoKitty
	case os.Getenv("KONSOLE_VERSION") != "":
		return protoKitty
	case termProg == "iTerm.app" || os.Getenv("LC_TERMINAL") == "iTerm2":
		return protoITerm
	case strings.Contains(term, "foot") || strings.Contains(term, "mlterm") ||
		strings.Contains(term, "contour") || strings.Contains(term, "sixel") ||
		strings.Contains(term, "yaft"):
		return protoSixel
	}
	return protoNone
}

// imageShowDo is the flash "show" handler — a package func (not a closure in the
// registry literal) to keep the static reference out of the nodeTypes init graph.
func imageShowDo(m *Model, it *item) tea.Cmd { return m.showImageProtocol(it) }

// showImageProtocol blits the node's image at full resolution via the terminal's
// graphics protocol, suspending the editor for the duration. It falls back to a
// flash (the half-block alt+e view is the visual fallback) when there is no image
// or no supported protocol.
func (m *Model) showImageProtocol(it *item) tea.Cmd {
	if it == nil {
		return nil
	}
	blob, ok, err := database.GetBlob(m.db, it.uuid)
	if err != nil || !ok {
		m.flash = "image: nothing to show"
		return nil
	}
	proto := detectGraphicsProto()
	if proto == protoNone {
		m.flash = "no graphics protocol — ⌥e for preview"
		return nil
	}
	cols := m.width - 2
	if cols < 10 {
		cols = 60
	}
	seq, tempPaths, err := buildGraphicsSequence(proto, blob.Bytes, cols)
	if err != nil {
		m.flash = "image: " + err.Error()
		return nil
	}
	seqFile, err := os.CreateTemp("", "lflow-imgseq-*")
	if err != nil {
		m.flash = "image: " + err.Error()
		return nil
	}
	_, _ = seqFile.Write(seq)
	seqFile.Close()
	tempPaths = append(tempPaths, seqFile.Name())

	// clear the screen, cat the raw protocol bytes to the tty, then wait for Enter.
	// Reading from /dev/tty keeps the wait robust regardless of stdin.
	script := "clear; cat " + shQuote(seqFile.Name()) +
		"; printf '\\n  \\033[2mpress enter to return\\033[0m'; read _ </dev/tty"
	c := exec.Command("sh", "-c", script)
	return tea.ExecProcess(c, func(error) tea.Msg {
		for _, p := range tempPaths {
			_ = os.Remove(p)
		}
		return nil
	})
}

// buildGraphicsSequence returns the raw escape bytes that display the PNG at the
// given cell width (aspect preserved), plus any temp files the caller must remove
// after the child has consumed them.
//   - kitty transmits by file path (t=f) — no 4KB chunking, high fidelity.
//   - iTerm2 inlines the base64 PNG in an OSC 1337 sequence.
func buildGraphicsSequence(proto graphicsProto, png []byte, cols int) (seq []byte, tempPaths []string, err error) {
	switch proto {
	case protoKitty:
		f, err := os.CreateTemp("", "lflow-img-*.png")
		if err != nil {
			return nil, nil, err
		}
		if _, err := f.Write(png); err != nil {
			f.Close()
			os.Remove(f.Name())
			return nil, nil, err
		}
		f.Close()
		b64path := base64.StdEncoding.EncodeToString([]byte(f.Name()))
		// APC _G <control data> ; <payload> ST. a=T transmit+display, f=100 PNG,
		// t=f file medium, c=cols (rows inferred to preserve aspect).
		s := "\x1b_Ga=T,f=100,t=f,c=" + strconv.Itoa(cols) + ";" + b64path + "\x1b\\"
		return []byte(s), []string{f.Name()}, nil
	case protoITerm:
		b64 := base64.StdEncoding.EncodeToString(png)
		s := "\x1b]1337;File=inline=1;preserveAspectRatio=1;width=" + strconv.Itoa(cols) +
			";size=" + strconv.Itoa(len(png)) + ":" + b64 + "\a"
		return []byte(s), nil, nil
	case protoSixel:
		img, _, err := image.Decode(bytes.NewReader(png))
		if err != nil {
			return nil, nil, err
		}
		// target pixel width from the cell width (~6px/cell), capped so a big
		// screenshot can't produce a huge, slow sixel stream.
		pxW := cols * 6
		if pxW > 1000 {
			pxW = 1000
		}
		return sixelEncode(img, pxW), nil, nil
	}
	return nil, nil, nil
}

// shQuote single-quotes a path for a POSIX shell. Temp paths never contain a
// single quote; guard anyway so a crafted TMPDIR can't break out.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
