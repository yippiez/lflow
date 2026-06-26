import React from "react";
import { Pressable, StyleSheet, Text, View } from "react-native";
import { colors, fontFamily, glyph, size, styleColor } from "../theme";
import {
  chipDisplay,
  parseStyle,
  splitName,
  type ParsedStyle,
  type TreeNode,
  type WireChip,
} from "../protocol";

const INDENT = 16;

interface Props {
  node: TreeNode;
  chips: Record<string, WireChip>;
  onToggleCollapse: (node: TreeNode) => void;
  onPress: (node: TreeNode) => void;
  onToggleDone: (node: TreeNode) => void;
}

const SIGNS: Record<string, string> = { bash: "$ ", query: "⌕ " };

function isHeading(t: string) {
  return t === "h1" || t === "h2" || t === "h3";
}

// glyphFor mirrors render.go: heading digit (yellow bold), todo box, log →,
// mirror ◆, collapsed ●, open ○. Returns null for a divider (no glyph).
function glyphFor(node: TreeNode): { ch: string; color: string } | null {
  if (node.type === "divider") return null;
  if (node.mirrorOf) return { ch: glyph.mirror, color: colors.dim };
  if (isHeading(node.type)) {
    const digit = node.type[1];
    return { ch: digit, color: colors.yellow };
  }
  if (node.type === "todo")
    return {
      ch: node.completedAt > 0 ? glyph.todoDone : glyph.todo,
      color: colors.dim,
    };
  if (node.type === "log") {
    const st = parseStyle(node.style);
    return { ch: glyph.log, color: st.color ? styleColor[st.color] : colors.dim };
  }
  if (node.children.length > 0) return { ch: glyph.collapsed, color: colors.dim };
  return { ch: glyph.open, color: colors.dim };
}

function baseColor(node: TreeNode): string {
  const st = parseStyle(node.style);
  if (st.color && styleColor[st.color]) return styleColor[st.color];
  if (node.type === "log") return colors.dim;
  return colors.fg;
}

function fontSizing(node: TreeNode): { fs: number; lh: number } {
  if (node.type === "h1") return size.h1;
  if (node.type === "h2") return size.h2;
  if (node.type === "h3") return size.h3;
  return { fs: size.base, lh: size.line };
}

function logTime(addedOn: number): string {
  if (!addedOn) return "";
  const d = new Date(addedOn / 1e6);
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(
    d.getHours(),
  )}:${p(d.getMinutes())}`;
}

function jsonPreview(name: string): string {
  try {
    return JSON.stringify(JSON.parse(name)).slice(0, 120);
  } catch {
    return name;
  }
}

export default function NodeRow({
  node,
  chips,
  onToggleCollapse,
  onPress,
  onToggleDone,
}: Props) {
  const hasChildren = node.children.length > 0;

  // divider: a full-width rule, no glyph or text.
  if (node.type === "divider") {
    return (
      <View style={[styles.row, { paddingLeft: 6 + node.depth * INDENT }]}>
        <View style={styles.discSpacer} />
        <Pressable style={styles.rule} onPress={() => onPress(node)}>
          <View style={styles.ruleLine} />
        </Pressable>
      </View>
    );
  }

  const g = glyphFor(node);
  const st = parseStyle(node.style);
  const sz = fontSizing(node);
  const headingBold = isHeading(node.type);
  const bold = st.bold || headingBold;
  const italic = st.italic || node.type === "quote";

  const textStyle = {
    color: baseColor(node),
    fontFamily: fontFamily(bold, italic),
    fontSize: sz.fs,
    lineHeight: sz.lh,
    textDecorationLine: decoration(st, node.completedAt > 0),
  };

  const block = node.type === "code" || node.type === "bash";
  // code uses the lighter panel surface so it reads as a block against the
  // near-identical #1f1f1f code bg; bash keeps its slate terminal tint.
  const blockBg = node.type === "bash" ? colors.bgTerm : colors.panel;
  const sign = SIGNS[node.type] ?? "";

  const body = (() => {
    if (node.type === "json") {
      return <Text style={[textStyle, { color: colors.cyan }]}>{jsonPreview(node.name)}</Text>;
    }
    if (node.type === "voice") {
      return (
        <Text style={[textStyle, { color: colors.dim }]}>
          ▸ {node.name || "voice memo"}
        </Text>
      );
    }
    const segs = splitName(node.name, chips);
    return (
      <Text style={textStyle}>
        {node.type === "quote" && (
          <Text style={{ color: colors.accent }}>{glyph.quoteBar} </Text>
        )}
        {node.type === "log" && node.addedOn > 0 && (
          <Text style={{ color: colors.dim }}>({logTime(node.addedOn)}) </Text>
        )}
        {sign !== "" && <Text style={{ color: colors.dim }}>{sign}</Text>}
        {segs.map((seg, i) =>
          seg.kind === "text" ? (
            <Text key={i}>{seg.text}</Text>
          ) : (
            <ChipText key={i} chip={seg.chip} />
          ),
        )}
        {node.name === "" && node.type !== "bash" && (
          <Text style={{ color: colors.dim }}>(empty)</Text>
        )}
      </Text>
    );
  })();

  return (
    <View style={[styles.row, { paddingLeft: 6 + node.depth * INDENT }]}>
      {/* disclosure triangle */}
      <Pressable
        hitSlop={8}
        onPress={() => hasChildren && onToggleCollapse(node)}
        style={styles.disc}
      >
        <Text style={[styles.glyph, { color: colors.dim }]}>
          {hasChildren ? (node.collapsed ? glyph.discClosed : glyph.discOpen) : " "}
        </Text>
      </Pressable>

      {/* glyph slot */}
      <Pressable hitSlop={6} onPress={() => onToggleDone(node)} style={styles.bulletBox}>
        {g && (
          <Text
            style={[
              styles.glyph,
              {
                color: g.color,
                fontFamily: fontFamily(isHeading(node.type), false),
                fontSize: isHeading(node.type) ? sz.fs : size.glyph,
                lineHeight: sz.lh,
              },
            ]}
          >
            {g.ch}
          </Text>
        )}
      </Pressable>

      {/* body */}
      <Pressable style={styles.textBox} onPress={() => onPress(node)}>
        {block ? (
          <View style={[styles.block, { backgroundColor: blockBg }]}>{body}</View>
        ) : (
          body
        )}
      </Pressable>
    </View>
  );
}

// ChipText: tags muted+underlined, dates on a blue pill, paths/others cyan.
function ChipText({ chip }: { chip: WireChip }) {
  const label = chipDisplay(chip);
  if (chip.kind === "tag")
    return (
      <Text style={{ color: colors.dim, textDecorationLine: "underline" }}>{label}</Text>
    );
  if (chip.kind === "date")
    return (
      <Text style={{ color: colors.fg, backgroundColor: colors.bgPill }}>
        {" "}
        {label}{" "}
      </Text>
    );
  return <Text style={{ color: colors.cyan }}>{label}</Text>;
}

function decoration(
  st: ParsedStyle,
  done: boolean,
): "none" | "underline" | "line-through" | "underline line-through" {
  const u = st.underline;
  const s = st.strike || done;
  if (u && s) return "underline line-through";
  if (u) return "underline";
  if (s) return "line-through";
  return "none";
}

const styles = StyleSheet.create({
  row: {
    flexDirection: "row",
    alignItems: "flex-start",
    paddingVertical: 2,
    paddingRight: 12,
  },
  disc: { width: 15, alignItems: "center" },
  discSpacer: { width: 15 },
  bulletBox: { width: 17, alignItems: "center" },
  glyph: { fontFamily: font_regular(), fontSize: size.glyph, lineHeight: size.line },
  textBox: { flex: 1, paddingLeft: 4 },
  block: {
    borderRadius: 4,
    paddingHorizontal: 8,
    paddingVertical: 4,
    alignSelf: "flex-start",
    maxWidth: "100%",
  },
  rule: { flex: 1, justifyContent: "center", height: size.line },
  ruleLine: { height: 1, backgroundColor: colors.border, marginTop: 2 },
});

// font_regular avoids importing the name twice; keeps StyleSheet literal tidy.
function font_regular() {
  return fontFamily(false, false);
}
