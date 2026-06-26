// Palette ported 1:1 from the lflow TUI "system" theme — which is VS Code
// Dark+ (pkg/tui/editor/theme.go + render.go). The background is VS Code's
// editor background #1e1e1e (the terminal lflow draws into); the slate
// #1e2230 in render.go is only the bash-output block, not the page.
export const colors = {
  bg: "#1e1e1e", // VS Code Dark+ editor background
  panel: "#252526", // slightly lighter panel/header surface
  border: "#333333",
  fg: "#d4d4d4", // default text
  dim: "#7a7a7a", // glyphs, tree connectors, muted text, #tags
  accent: "#569cd6", // blue / links
  red: "#f44747", // cursor, selected glyph, errors
  orange: "#ce9178",
  yellow: "#dcdcaa", // heading digits
  green: "#6a9955",
  cyan: "#4ec9b0", // links, secondary accent, path chips
  purple: "#c586c0",
  bgCode: "#1f1f1f", // code block background
  bgTerm: "#1e2230", // bash/terminal block background
  bgPill: "#264f78", // date pill background
};

// The eight /color swatches → palette (blue→accent, gray→dim, rest by name).
export const styleColor: Record<string, string> = {
  red: colors.red,
  orange: colors.orange,
  yellow: colors.yellow,
  green: colors.green,
  cyan: colors.cyan,
  blue: colors.accent,
  purple: colors.purple,
  gray: colors.dim,
};

// Glyphs (locked set from render.go).
export const glyph = {
  open: "○",
  collapsed: "●",
  mirror: "◆",
  todo: "□",
  todoDone: "■",
  quoteBar: "▎",
  log: "→",
  discClosed: "▸",
  discOpen: "▾",
};

// JetBrains Mono is bundled (see App.tsx useFonts) so web/iOS/Android render
// identically with a clean terminal monospace.
export const font = {
  regular: "JetBrainsMono_400Regular",
  bold: "JetBrainsMono_700Bold",
  italic: "JetBrainsMono_400Regular_Italic",
  boldItalic: "JetBrainsMono_700Bold_Italic",
};

export function fontFamily(bold: boolean, italic: boolean): string {
  if (bold && italic) return font.boldItalic;
  if (bold) return font.bold;
  if (italic) return font.italic;
  return font.regular;
}

// Font sizing. Body is a compact terminal scale; headings step up so the type
// reads at a glance (the terminal can't resize, so this is mobile-native).
export const size = {
  base: 13,
  line: 19,
  glyph: 13,
  h1: { fs: 20, lh: 27 },
  h2: { fs: 16, lh: 23 },
  h3: { fs: 14, lh: 21 },
};
