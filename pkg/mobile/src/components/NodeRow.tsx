import React from "react";
import { Pressable, StyleSheet, Text, View } from "react-native";
import { colors, glyph, mono, styleColor } from "../theme";
import {
  chipDisplay,
  parseStyle,
  splitName,
  type TreeNode,
  type WireChip,
} from "../protocol";

const INDENT = 18;

interface Props {
  node: TreeNode;
  chips: Record<string, WireChip>;
  onToggleCollapse: (node: TreeNode) => void;
  onPress: (node: TreeNode) => void;
  onToggleDone: (node: TreeNode) => void;
}

// bulletFor returns the glyph + color for a node, mirroring glyphFor in
// render.go (mirror ◆, todo □/■, collapsed ●, open ○).
function bulletFor(node: TreeNode): { ch: string; color: string } {
  if (node.mirrorOf) return { ch: glyph.mirror, color: colors.dim };
  if (node.type === "todo") {
    return {
      ch: node.completedAt > 0 ? glyph.todoDone : glyph.todo,
      color: colors.dim,
    };
  }
  if (node.children.length > 0) return { ch: glyph.collapsed, color: colors.dim };
  return { ch: glyph.open, color: colors.dim };
}

// baseColor matches renderBody: log is dim, a /color wins, else default fg.
function baseColor(node: TreeNode): string {
  const st = parseStyle(node.style);
  if (st.color && styleColor[st.color]) return styleColor[st.color];
  if (node.type === "log") return colors.dim;
  return colors.fg;
}

export default function NodeRow({
  node,
  chips,
  onToggleCollapse,
  onPress,
  onToggleDone,
}: Props) {
  const st = parseStyle(node.style);
  const hasChildren = node.children.length > 0;
  const bullet = bulletFor(node);

  const textStyle = {
    color: baseColor(node),
    fontWeight: (st.bold || node.type.startsWith("h")
      ? "700"
      : "400") as "700" | "400",
    fontStyle: (st.italic || node.type === "quote" ? "italic" : "normal") as
      | "italic"
      | "normal",
    textDecorationLine: decoration(st, node.completedAt > 0),
  };

  const segs = splitName(node.name, chips);

  return (
    <View style={[styles.row, { paddingLeft: 6 + node.depth * INDENT }]}>
      {/* disclosure triangle (only when there are children) */}
      <Pressable
        hitSlop={8}
        onPress={() => hasChildren && onToggleCollapse(node)}
        style={styles.disc}
      >
        <Text style={[styles.glyph, { color: colors.dim }]}>
          {hasChildren ? (node.collapsed ? glyph.discClosed : glyph.discOpen) : " "}
        </Text>
      </Pressable>

      {/* bullet — tap toggles a todo */}
      <Pressable hitSlop={6} onPress={() => onToggleDone(node)} style={styles.bulletBox}>
        <Text style={[styles.glyph, { color: bullet.color }]}>{bullet.ch}</Text>
      </Pressable>

      {/* node text */}
      <Pressable style={styles.textBox} onPress={() => onPress(node)}>
        <Text style={[styles.text, textStyle]}>
          {node.type === "quote" && (
            <Text style={{ color: colors.accent }}>{glyph.quoteBar} </Text>
          )}
          {segs.map((seg, i) =>
            seg.kind === "text" ? (
              <Text key={i}>{seg.text}</Text>
            ) : (
              <ChipText key={i} chip={seg.chip} />
            ),
          )}
          {node.name === "" && <Text style={{ color: colors.dim }}>(empty)</Text>}
        </Text>
      </Pressable>
    </View>
  );
}

// ChipText renders one inline chip: tags muted gray, date on a blue pill, paths
// (and others) cyan — matching the chip colors in render.go.
function ChipText({ chip }: { chip: WireChip }) {
  const label = chipDisplay(chip);
  if (chip.kind === "tag") {
    return <Text style={{ color: colors.dim, textDecorationLine: "underline" }}>{label}</Text>;
  }
  if (chip.kind === "date") {
    return (
      <Text style={{ color: colors.fg, backgroundColor: colors.bgPill }}>
        {" "}
        {label}{" "}
      </Text>
    );
  }
  return <Text style={{ color: colors.cyan }}>{label}</Text>;
}

function decoration(
  st: ReturnType<typeof parseStyle>,
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
    paddingVertical: 3,
    paddingRight: 10,
  },
  disc: { width: 16, alignItems: "center" },
  bulletBox: { width: 18, alignItems: "center" },
  glyph: { fontFamily: mono, fontSize: 14, lineHeight: 21 },
  textBox: { flex: 1, paddingLeft: 4 },
  text: { fontFamily: mono, fontSize: 14, lineHeight: 21 },
});
