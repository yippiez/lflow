import { Platform } from "react-native";

// Palette ported 1:1 from the lflow TUI "system" theme
// (pkg/tui/editor/theme.go + render.go). These are the exact RGB values the
// terminal renders, so the app matches the editor pixel-for-pixel.
export const colors = {
  bg: "#1e2230", // bgTerm slate — the dark background in the screenshot
  fg: "#d4d4d4", // default text
  dim: "#7a7a7a", // glyphs, tree connectors, muted text, #tags
  accent: "#569cd6", // blue / links (the "blue" /color swatch)
  red: "#f44747", // cursor, selected glyph, errors
  orange: "#ce9178",
  yellow: "#dcdcaa", // heading digits
  green: "#6a9955",
  cyan: "#4ec9b0", // links, secondary accent, path chips
  purple: "#c586c0",
  bgCode: "#1f1f1f", // code block background
  bgPill: "#264f78", // date pill background
};

// The eight /color swatches, mapped to palette colors exactly as applyTheme does
// (blue→accent, gray→dim, the rest by name).
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
  discClosed: "▸",
  discOpen: "▾",
};

// A monospace family so the outline lines up like the terminal.
export const mono = Platform.select({
  ios: "Menlo",
  android: "monospace",
  default: "ui-monospace, Menlo, Consolas, monospace",
}) as string;
