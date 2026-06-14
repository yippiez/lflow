#!/usr/bin/env python3
"""Render an ANSI (SGR) terminal capture to a PNG.

Reads `tmux capture-pane -ep` style input on stdin and paints each cell with a
DejaVu Sans Mono face, honoring truecolor fg/bg, bold, italic, underline,
inverse and strike. Good enough to show the lflow editor's styled rows.
"""
import sys
import re
from PIL import Image, ImageDraw, ImageFont

FONT_DIR = "/usr/share/fonts/truetype/dejavu"
SIZE = 22
CW, CH = 13, 28  # cell width/height
PAD = 16
BG = (24, 24, 24)
FG = (212, 212, 212)

fonts = {
    (False, False): ImageFont.truetype(f"{FONT_DIR}/DejaVuSansMono.ttf", SIZE),
    (True, False): ImageFont.truetype(f"{FONT_DIR}/DejaVuSansMono-Bold.ttf", SIZE),
    (False, True): ImageFont.truetype(f"{FONT_DIR}/DejaVuSansMono-Oblique.ttf", SIZE),
    (True, True): ImageFont.truetype(f"{FONT_DIR}/DejaVuSansMono-BoldOblique.ttf", SIZE),
}

SGR = re.compile(r"\x1b\[([0-9;]*)m")


class State:
    def __init__(self):
        self.reset()

    def reset(self):
        self.fg = FG
        self.bg = None
        self.bold = False
        self.italic = False
        self.underline = False
        self.inverse = False
        self.strike = False


def apply(state, params):
    nums = [int(x) if x else 0 for x in params.split(";")] if params else [0]
    i = 0
    while i < len(nums):
        n = nums[i]
        if n == 0:
            state.reset()
        elif n == 1:
            state.bold = True
        elif n == 3:
            state.italic = True
        elif n == 4:
            state.underline = True
        elif n == 7:
            state.inverse = True
        elif n == 9:
            state.strike = True
        elif n == 39:
            state.fg = FG
        elif n == 49:
            state.bg = None
        elif n == 38 and i + 1 < len(nums) and nums[i + 1] == 2:
            state.fg = (nums[i + 2], nums[i + 3], nums[i + 4])
            i += 4
        elif n == 48 and i + 1 < len(nums) and nums[i + 1] == 2:
            state.bg = (nums[i + 2], nums[i + 3], nums[i + 4])
            i += 4
        i += 1


def parse_line(line):
    """Yield (char, snapshot-of-state) cells for one line."""
    state = State()
    cells = []
    pos = 0
    for m in SGR.finditer(line):
        for ch in line[pos:m.start()]:
            cells.append((ch, snapshot(state)))
        apply(state, m.group(1))
        pos = m.end()
    for ch in line[pos:]:
        cells.append((ch, snapshot(state)))
    return cells


def snapshot(s):
    o = State()
    o.__dict__.update(s.__dict__)
    return o


def main():
    raw = sys.stdin.buffer.read().decode("utf-8", "replace")
    lines = raw.split("\n")
    while lines and lines[-1].strip() == "":
        lines.pop()
    cols = 0
    parsed = [parse_line(ln) for ln in lines]
    for c in parsed:
        cols = max(cols, len(c))
    W = PAD * 2 + cols * CW
    H = PAD * 2 + len(parsed) * CH
    img = Image.new("RGB", (W, H), BG)
    d = ImageDraw.Draw(img)
    for r, cells in enumerate(parsed):
        y = PAD + r * CH
        for c, (ch, st) in enumerate(cells):
            x = PAD + c * CW
            fg, bg = st.fg, st.bg
            if st.inverse:
                fg, bg = (bg or BG), (fg or FG)
            if bg:
                d.rectangle([x, y, x + CW, y + CH], fill=bg)
            if ch not in (" ", ""):
                d.text((x, y), ch, font=fonts[(st.bold, st.italic)], fill=fg)
            if st.underline:
                d.line([x, y + CH - 3, x + CW, y + CH - 3], fill=fg, width=2)
            if st.strike:
                d.line([x, y + CH // 2, x + CW, y + CH // 2], fill=fg, width=2)
    img.save(sys.argv[1] if len(sys.argv) > 1 else "/tmp/ansishot.png")


if __name__ == "__main__":
    main()
