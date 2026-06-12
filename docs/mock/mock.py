#!/usr/bin/env python3
"""Print ANSI mockups of the lflow node editor, one named variant per run.

Run inside a tmux pane and capture with `tmux capture-pane -e` (see shoot.sh).
"""
import sys

W = 74

RESET = "\x1b[0m"
FG = lambda h: "\x1b[38;2;%d;%d;%dm" % (int(h[1:3], 16), int(h[3:5], 16), int(h[5:7], 16))
BG = lambda h: "\x1b[48;2;%d;%d;%dm" % (int(h[1:3], 16), int(h[3:5], 16), int(h[5:7], 16))

C_FG = "#d4d4d4"
C_DIM = "#7a7a7a"
C_ACC = "#569cd6"
C_RED = "#f44747"
C_GRN = "#6a9955"
C_YEL = "#dcdcaa"
C_PANEL = "#2a2a2a"
C_CURROW = "#3a3a48"
C_BLACK = "#000000"

# glyph kind, text, dim suffix, cursor?, connector prefix (aligned under the
# parent's bullet column), depth (for indent-only mode)
OUTLINE = [
    ("open",   "baseline numbers",        "",              False, "",       0),
    ("open",   "parse: 1.42s",            "",              False, "├─ ",    1),
    ("open",   "compile: 3.10s",          "",              False, "╰─ ",    1),
    ("closed", "attempt 2 — with cache",  " (3 children)", True,  "",       0),
    ("mirror", "shared methodology",      " (mirror)",     False, "",       0),
    ("todo",   "rerun with -race",        "",              False, "",       0),
    ("open",   "notes",                   "",              False, "",       0),
    ("open",   "cache hit ratio 94%",     "",              False, "├─ ",    1),
    ("open",   "warm runs only",          "",              False, "│  ╰─ ", 2),
    ("open",   "flaky on CI",             "",              False, "╰─ ",    1),
]

GLYPHS = {
    "circle": {"open": ("○", C_ACC), "closed": ("●", C_ACC), "mirror": ("◆", C_RED), "todo": ("□", C_FG), "wf": ("(4)", C_YEL)},
    "dot":    {"open": ("•", C_ACC), "closed": ("•", C_ACC), "mirror": ("◆", C_RED), "todo": ("□", C_FG), "wf": ("(4)", C_YEL)},
}

# locked design (2026-06-12 final): black bg, connectors, circles, ▌ cursor,
# minimal bottom bar. ALL mirrors are ◆ — local and workflowy look identical;
# sync cadence is never surfaced to the user.
OUTLINE_FINAL = [
    ("open",   "baseline numbers",       "",              False, "",    0),
    ("open",   "parse: 1.42s",           "",              False, "├─ ", 1),
    ("open",   "compile: 3.10s",         "",              False, "╰─ ", 1),
    ("closed", "attempt 2 — with cache", " (3 children)", True,  "",    0),
    ("mirror", "shared methodology",     " (mirror)",     False, "",    0),
    ("mirror", "reading list",           " (mirror)",     False, "",    0),
    ("todo",   "rerun with -race",       "",              False, "",    0),
    ("open",   "notes",                  "",              False, "",    0),
]


def cfg(variant):
    c = dict(bg="black", conn=True, glyph="circle", cursor="row", bar="info")
    overrides = {
        "panel-gray":   dict(bg="gray"),
        "panel-black":  dict(),
        "conn-tree":    dict(),
        "conn-indent":  dict(conn=False),
        "glyph-circle": dict(),
        "glyph-dot":    dict(glyph="dot"),
        "cursor-row":   dict(),
        "cursor-bar":   dict(cursor="bar"),
        "bottom-info":  dict(),
        "bottom-keys":  dict(bar="keys"),
        "bottom-min":   dict(bar="min"),
        "final":        dict(cursor="bar", bar="min"),
    }
    c.update(overrides[variant])
    return c


import re as _re
_SGR = _re.compile(r"\x1b\[[0-9;]*m")


def vislen(s):
    return len(_SGR.sub("", s))


def pad(content, visible, bg):
    return BG(bg) + content + BG(bg) + " " * max(0, W - 1 - visible) + RESET


def caption_bar(left, right, bg):
    body = f" {left} "
    tail = f" {right} "
    fill = "─" * max(0, W - 1 - 2 - len(body) - len(tail) - 2)
    vis = 2 + len(body) + len(fill) + 2 + len(tail)
    return pad(FG(C_DIM) + "──" + FG(C_FG) + body + FG(C_DIM) + fill + "──" + FG(C_FG) + tail, vis, bg)


def bottom(c, bg, total=10):
    # cursor sits on "attempt 2" (row 4), a bullet node
    if c["bar"] == "min":
        txt = " experiment results · 4/%d · dirty" % total
        return [pad(FG(C_DIM) + txt, len(txt), bg)]
    info = caption_bar("experiment results · h2", "cursor: ● bullet · 4/10 · dirty", bg)
    if c["bar"] == "info":
        return [info]
    keys = " tab indent · S-tab outdent · C-spc fold · C-d del · C-s save · C-q quit"
    return [info, pad(FG(C_DIM) + keys, len(keys), bg)]


# cursor sits on a fresh node where "/" was just typed
OUTLINE_SLASH = OUTLINE_FINAL[:3] + [("open", "/", "", True, "", 0)] + OUTLINE_FINAL[4:]


def render(variant):
    if variant == "picker":
        return render_picker()
    if variant == "summary":
        return render_summary()
    if variant == "slash-menu":
        return render_slash(variant)
    if variant == "slash-finder":
        return render_finder()
    c = cfg(variant)
    panel_bg = C_PANEL if c["bg"] == "gray" else C_BLACK
    lines = []
    rows = OUTLINE_FINAL if variant == "final" else OUTLINE
    for kind, text, suffix, cursor, conn_prefix, depth in rows:
        g, gcol = GLYPHS[c["glyph"]][kind]
        prefix = conn_prefix if c["conn"] else "  " * depth
        rowbg = C_CURROW if (cursor and c["cursor"] == "row") else panel_bg
        marker = FG(C_ACC) + "▌" if (cursor and c["cursor"] == "bar") else " "
        body = FG(C_ACC) + prefix + FG(gcol) + g + " " + FG(C_FG) + text
        if suffix:
            body += FG(C_DIM) + suffix
        vis = 1 + len(prefix) + len(g) + 1 + len(text) + len(suffix)
        lines.append(BG(rowbg) + marker + BG(rowbg) + body + BG(rowbg) + " " * max(0, W - 1 - vis) + RESET)
    lines += bottom(c, panel_bg, total=len(rows))
    return lines


def outline_block(rows, c):
    panel_bg = C_PANEL if c["bg"] == "gray" else C_BLACK
    lines = []
    for kind, text, suffix, cursor, conn_prefix, depth in rows:
        g, gcol = GLYPHS[c["glyph"]][kind]
        prefix = conn_prefix if c["conn"] else "  " * depth
        marker = FG(C_ACC) + "▌" if (cursor and c["cursor"] == "bar") else " "
        body = FG(C_ACC) + prefix + FG(gcol) + g + " " + FG(C_FG) + text
        if cursor:
            body += FG(C_ACC) + "▌"
        if suffix:
            body += FG(C_DIM) + suffix
        vis = 1 + len(prefix) + len(g) + 1 + len(text) + (1 if cursor else 0) + len(suffix)
        lines.append(BG(panel_bg) + marker + body + BG(panel_bg) + " " * max(0, W - 1 - vis) + RESET)
    return lines


def render_slash(which):
    c = cfg("final")
    lines = outline_block(OUTLINE_SLASH, c)
    lines += bottom(c, C_BLACK, total=len(OUTLINE_SLASH))
    menu = [
        ("▸", "/mirror", "mirror a node here (fuzzy finder)"),
        (" ", "/mirror_to", "mirror THIS node somewhere else"),
        (" ", "/move_to", "move this node under another node"),
        (" ", "/go", "jump the editor to another node"),
        (" ", "/complete", "toggle done"),
        (" ", "/h1", "make heading (also /h2 /h3 /todo /code /quote /bullet)"),
        (" ", "/note", "edit this node's note"),
    ]
    for mark, name, desc in menu:
        line = FG(C_ACC) + " " + mark + " " + FG(C_FG) + "%-11s" % name + FG(C_DIM) + " " + desc
        lines.append(pad(line, vislen(line), C_BLACK))
    return lines


def render_finder():
    # /mirror replaces the whole outline with a fuzzy finder; bar stays
    c = cfg("final")
    rows = [
        FG(C_DIM) + " /mirror " + FG(C_FG) + "read" + FG(C_ACC) + "▌" + FG(C_DIM) + "                                    local · " + FG(C_ACC) + "[workflowy]",
        FG(C_ACC) + " ▸ " + FG(C_FG) + "reading list                " + FG(C_DIM) + "14 nodes",
        FG(C_FG) + "   weekend reading             " + FG(C_DIM) + "3 nodes",
        FG(C_FG) + "   readme drafts               " + FG(C_DIM) + "5 nodes",
        FG(C_FG) + "   proofread blog post         " + FG(C_DIM) + "2 nodes",
        FG(C_DIM) + "   … 4 more",
        "",
        FG(C_DIM) + " enter mirror at cursor · tab source · esc back to outline",
    ]
    lines = [pad(r, vislen(r), C_BLACK) for r in rows]
    lines += bottom(c, C_BLACK, total=len(OUTLINE_FINAL))
    return lines


def render_picker():
    rows = [
        ("▸", "experiment results", "h2 · 12 nodes", True),
        (" ", "experiments / archive 2025", "○ · 48 nodes", False),
        (" ", "exp: tokenizer ideas", "○ · 5 nodes", False),
    ]
    lines = [pad(FG(C_DIM) + " find: " + FG(C_FG) + "experiment" + FG(C_ACC) + "▌", 18, C_BLACK)]
    for mark, name, meta, sel in rows:
        rowbg = C_CURROW if sel else C_BLACK
        gap = " " * max(1, W - 1 - 3 - len(name) - len(meta) - 2)
        lines.append(BG(rowbg) + " " + FG(C_ACC) + mark + " " + FG(C_FG) + name + gap + FG(C_DIM) + meta + BG(rowbg) + "  " + RESET)
    lines.append(pad(FG(C_DIM) + " enter open · esc cancel", 25, C_BLACK))
    return lines


def render_summary():
    return [
        FG(C_DIM) + "$ " + FG(C_FG) + "lflow find \"experiment results\"" + RESET,
        FG(C_GRN) + "✓ " + FG(C_FG) + "saved \"experiment results\"" + FG(C_DIM) + " · 12 nodes · 3 edited · dirty" + RESET,
        FG(C_DIM) + "$ " + RESET,
    ]


P = lambda: FG(C_DIM) + "$ " + FG(C_FG)          # prompt
OK = lambda s: FG(C_GRN) + "✓ " + FG(C_FG) + s    # success line
ERR = lambda s: FG(C_RED) + "✗ " + FG(C_FG) + s   # error line
HINT = lambda s: FG(C_DIM) + "  hint: " + s       # dim hint line

CMD = {
    "cmd-find-error": [
        P() + 'lflow find "quantum"',
        ERR('no node matching ' + FG(C_YEL) + '"quantum"'),
        HINT("lflow list --roots · add --all to include completed nodes"),
        FG(C_DIM) + "$ ",
    ],
    "cmd-find-best": [
        P() + 'lflow find "exp"',
        FG(C_DIM) + "→ opening " + FG(C_YEL) + '"experiment results"' + FG(C_DIM) + "  (best of 3 · --strict lists instead)",
        FG(C_DIM) + "  [editor opens inline]",
    ],
    "cmd-wf": [
        P() + 'lflow wf mirror https://workflowy.com/#/abc123 --into notes',
        OK("mirroring " + FG(C_YEL) + '"reading list"' + FG(C_FG) + " → notes"),
        P() + "lflow wf list",
        FG(C_RED) + "  ◆ " + FG(C_FG) + "reading list   " + FG(C_DIM) + "workflowy · last sync 4s ago · 14 nodes",
        FG(C_RED) + "  ◆ " + FG(C_FG) + "meeting notes  " + FG(C_DIM) + "workflowy · last sync 1m ago · 6 nodes",
        P() + "lflow wf pull",
        OK("pulled 2 changed " + FG(C_DIM) + "· pushed 1"),
        FG(C_DIM) + "$ ",
    ],
    "cmd-list": [
        P() + 'lflow list "experiment results" --depth 2',
        FG(C_FG) + "- baseline numbers",
        FG(C_FG) + "  - parse: 1.42s",
        FG(C_FG) + "  - compile: 3.10s",
        FG(C_FG) + "- attempt 2 — with cache",
        FG(C_DIM) + "  …",
        P() + 'lflow list "experiment results" --format json | jq -r .children[0].name',
        FG(C_FG) + "baseline numbers",
        FG(C_DIM) + "$ ",
    ],
    "cmd-append": [
        P() + 'make bench 2>&1 | lflow append "experiment results"',
        OK("appended 3 nodes to " + FG(C_YEL) + '"experiment results"'),
        P() + 'echo "retry flaky test" | lflow append "experiment results" --note',
        OK("noted on " + FG(C_YEL) + '"experiment results"'),
        FG(C_DIM) + "$ ",
    ],
    "cmd-sync-local": [
        P() + "lflow sync",
        OK("synced with lflow-server (self-hosted) " + FG(C_DIM) + "· ↑3 ↓5 · usn 1042"),
        P() + "lflow sync --dry-run",
        FG(C_DIM) + "  would push 2 · pull 1 (usn 1042 → 1045)",
        FG(C_DIM) + "$ ",
    ],
}


if __name__ == "__main__":
    v = sys.argv[1]
    lines = CMD[v] if v in CMD else render(v)
    # no trailing newline: it would scroll the first row out of an exact-height pane
    sys.stdout.write("\n".join(ln + RESET for ln in lines))
    sys.stdout.flush()
