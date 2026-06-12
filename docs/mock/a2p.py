#!/usr/bin/env python3
"""ANSI -> Pango markup. Port of pi-prompt-chain's tmux-shot converter.

Reads `tmux capture-pane -e -p` output on stdin, emits Pango markup with
per-cell fg/bg. Pads every row to the full pane grid so background runs
render as solid blocks.
"""
import argparse
import re
import sys

SGR_RE = re.compile(r"\x1b\[([0-9;]*)m")
# strip OSC, APC, DCS, charset, cursor and any CSI that is not SGR
OTHER_ESC = re.compile(
    r"\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)"   # OSC
    r"|\x1b[PX^_][^\x1b]*\x1b\\"            # DCS/APC/PM
    r"|\x1b\[[0-9;?]*[A-LN-Za-ln-z]"        # CSI not ending in m/M
    r"|\x1b[()][0-9A-Za-z]"                 # charset
    r"|\x1b[=>]"                            # keypad
)

BASE16 = [
    "#000000", "#cd3131", "#0dbc79", "#e5e510", "#2472c8", "#bc3fbc", "#11a8cd", "#e5e5e5",
    "#666666", "#f14c4c", "#23d18b", "#f5f543", "#3b8eea", "#d670d6", "#29b8db", "#ffffff",
]

DEF_FG = "#d4d4d4"
DEF_BG = "#000000"


def xterm256(n):
    if n < 16:
        return BASE16[n]
    if n < 232:
        n -= 16
        r, g, b = n // 36, (n % 36) // 6, n % 6
        conv = lambda v: 0 if v == 0 else 55 + v * 40
        return "#%02x%02x%02x" % (conv(r), conv(g), conv(b))
    v = 8 + (n - 232) * 10
    return "#%02x%02x%02x" % (v, v, v)


def esc(s):
    return s.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")


class State:
    def __init__(self):
        self.reset()

    def reset(self):
        self.fg, self.bg, self.rev, self.bold = DEF_FG, DEF_BG, False, False

    def apply(self, params):
        p = [int(x) if x else 0 for x in params.split(";")] if params else [0]
        i = 0
        while i < len(p):
            c = p[i]
            if c == 0:
                self.reset()
            elif c == 1:
                self.bold = True
            elif c == 22:
                self.bold = False
            elif c == 7:
                self.rev = True
            elif c == 27:
                self.rev = False
            elif 30 <= c <= 37:
                self.fg = BASE16[c - 30]
            elif 90 <= c <= 97:
                self.fg = BASE16[c - 90 + 8]
            elif 40 <= c <= 47:
                self.bg = BASE16[c - 40]
            elif 100 <= c <= 107:
                self.bg = BASE16[c - 100 + 8]
            elif c == 39:
                self.fg = DEF_FG
            elif c == 49:
                self.bg = DEF_BG
            elif c in (38, 48):
                tgt = "fg" if c == 38 else "bg"
                if i + 1 < len(p) and p[i + 1] == 5 and i + 2 < len(p):
                    setattr(self, tgt, xterm256(p[i + 2])); i += 2
                elif i + 1 < len(p) and p[i + 1] == 2 and i + 4 < len(p):
                    setattr(self, tgt, "#%02x%02x%02x" % (p[i + 2], p[i + 3], p[i + 4])); i += 4
            i += 1

    def span(self, text):
        fg, bg = (self.bg, self.fg) if self.rev else (self.fg, self.bg)
        w = ' weight="bold"' if self.bold else ""
        return f'<span foreground="{fg}" background="{bg}"{w}>{esc(text)}</span>'


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--rows", type=int, required=True)
    ap.add_argument("--cols", type=int, required=True)
    a = ap.parse_args()

    st = State()
    out_rows = []
    for raw in sys.stdin.read().split("\n"):
        line = OTHER_ESC.sub("", raw)
        spans, width, pos = [], 0, 0
        for m in SGR_RE.finditer(line):
            chunk = line[pos:m.start()]
            if chunk:
                spans.append(st.span(chunk)); width += len(chunk)
            st.apply(m.group(1)); pos = m.end()
        tail = line[pos:]
        if tail:
            spans.append(st.span(tail)); width += len(tail)
        if width < a.cols:  # pad to full grid (capture-pane trims trailing cells)
            pad = State(); pad.bg = st.bg if False else DEF_BG
            spans.append(pad.span(" " * (a.cols - width)))
        out_rows.append("".join(spans))
    while len(out_rows) < a.rows:
        blank = State()
        out_rows.append(blank.span(" " * a.cols))
    print("\n".join(out_rows[: a.rows]))


if __name__ == "__main__":
    main()
