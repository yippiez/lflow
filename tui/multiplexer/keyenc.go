package multiplexer

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// EncodeKey turns a bubbletea key into the bytes a terminal would send for it,
// so the attached view can forward keystrokes to the session's PTY. appCursor
// switches the arrows/home/end to their SS3 forms (DECCKM), which is what a
// TUI that enabled application cursor keys expects back.
//
// The vocabulary is what agent TUIs actually read: text, Enter, Esc, Tab,
// Backspace, arrows, paging, delete, and ctrl chords. Anything unrecognized
// encodes to nil and is simply not sent.
func EncodeKey(k tea.KeyMsg, appCursor bool) []byte {
	if k.Type == tea.KeyRunes || k.Type == tea.KeySpace {
		b := []byte(string(k.Runes))
		if k.Type == tea.KeySpace {
			b = []byte(" ")
		}
		if k.Alt {
			b = append([]byte{0x1b}, b...)
		}
		return b
	}

	arrow := func(csi, ss3 byte) []byte {
		if appCursor {
			return []byte{0x1b, 'O', ss3}
		}
		return []byte{0x1b, '[', csi}
	}
	var out []byte
	switch k.Type {
	case tea.KeyEnter:
		out = []byte("\r")
	case tea.KeyBackspace:
		out = []byte{0x7f}
	case tea.KeyTab:
		out = []byte("\t")
	case tea.KeyShiftTab:
		out = []byte("\x1b[Z")
	case tea.KeyEscape:
		out = []byte{0x1b}
	case tea.KeyUp:
		out = arrow('A', 'A')
	case tea.KeyDown:
		out = arrow('B', 'B')
	case tea.KeyRight:
		out = arrow('C', 'C')
	case tea.KeyLeft:
		out = arrow('D', 'D')
	case tea.KeyHome:
		out = arrow('H', 'H')
	case tea.KeyEnd:
		out = arrow('F', 'F')
	case tea.KeyPgUp:
		out = []byte("\x1b[5~")
	case tea.KeyPgDown:
		out = []byte("\x1b[6~")
	case tea.KeyDelete:
		out = []byte("\x1b[3~")
	case tea.KeyInsert:
		out = []byte("\x1b[2~")
	default:
		// ctrl chords arrive as their own key types; recover the byte from the
		// name rather than pinning bubbletea's internal numbering
		s := k.String()
		if c, ok := strings.CutPrefix(s, "ctrl+"); ok && len(c) == 1 {
			ch := c[0]
			switch {
			case ch >= 'a' && ch <= 'z':
				out = []byte{ch - 'a' + 1}
			case ch == '@':
				out = []byte{0}
			case ch == '\\':
				out = []byte{0x1c}
			case ch == ']':
				out = []byte{0x1d}
			case ch == '^':
				out = []byte{0x1e}
			case ch == '_':
				out = []byte{0x1f}
			}
		}
	}
	if k.Alt && len(out) > 0 && out[0] != 0x1b {
		out = append([]byte{0x1b}, out...)
	}
	return out
}
