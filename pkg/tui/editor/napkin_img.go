package editor

import (
	"encoding/base64"
	"os"
	"strings"
)

// Terminal inline-image support. Some terminals can display real images via an
// escape protocol; this detects the protocol from the environment and emits the
// image inline. When none is available the caller falls back to the half-block
// thumbnail (napkinThumbFromImage). Detection is environment-based (no DA query),
// which is reliable for the common emulators and never blocks.

type imgProto int

const (
	protoNone  imgProto = iota
	protoKitty          // Kitty graphics protocol (kitty, WezTerm, Ghostty, Konsole)
	protoITerm          // iTerm2 inline images (iTerm2, WezTerm, VS Code, mintty)
)

// detectImgProtoEnv classifies a terminal from its environment variables.
func detectImgProtoEnv(get func(string) string) imgProto {
	term := strings.ToLower(get("TERM"))
	termProg := get("TERM_PROGRAM")
	switch {
	case get("KITTY_WINDOW_ID") != "", strings.Contains(term, "kitty"),
		termProg == "ghostty", get("GHOSTTY_RESOURCES_DIR") != "":
		return protoKitty
	case get("WEZTERM_EXECUTABLE") != "", termProg == "WezTerm":
		return protoKitty // WezTerm speaks Kitty (and iTerm); prefer Kitty
	case termProg == "iTerm.app", get("LC_TERMINAL") == "iTerm2",
		termProg == "vscode", strings.Contains(term, "mintty"):
		return protoITerm
	default:
		return protoNone
	}
}

func detectImgProto() imgProto { return detectImgProtoEnv(os.Getenv) }

// napkinImageEscape renders the raw PNG bytes as a single inline-image escape for
// the given protocol, sized to cols×rows terminal cells. Returns "", false when
// the protocol can't display it, so callers fall back to the thumbnail.
func napkinImageEscape(proto imgProto, png []byte, cols, rows int) (string, bool) {
	if len(png) == 0 {
		return "", false
	}
	b64 := base64.StdEncoding.EncodeToString(png)
	switch proto {
	case protoITerm:
		// OSC 1337 File: base64 payload, sized in cells, preserveAspectRatio.
		return "\x1b]1337;File=inline=1;preserveAspectRatio=1;" +
			"width=" + itoa(cols) + ";height=" + itoa(rows) + ":" + b64 + "\a", true
	case protoKitty:
		// f=100 (PNG), a=T (transmit+display), c/r = display size in cells. The
		// payload is chunked (m=1 until the final chunk) per the protocol.
		var sb strings.Builder
		const chunk = 4096
		first := true
		for len(b64) > 0 {
			n := min(chunk, len(b64))
			piece := b64[:n]
			b64 = b64[n:]
			more := 0
			if len(b64) > 0 {
				more = 1
			}
			sb.WriteString("\x1b_G")
			if first {
				sb.WriteString("f=100,a=T,c=" + itoa(cols) + ",r=" + itoa(rows) + ",m=" + itoa(more))
				first = false
			} else {
				sb.WriteString("m=" + itoa(more))
			}
			sb.WriteString(";")
			sb.WriteString(piece)
			sb.WriteString("\x1b\\")
		}
		return sb.String(), true
	default:
		return "", false
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
