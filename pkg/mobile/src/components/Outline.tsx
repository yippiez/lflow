import React, { useMemo, useState } from "react";
import {
  FlatList,
  Pressable,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { colors, font, size } from "../theme";
import { buildTree, type Op, type Snapshot, type TreeNode } from "../protocol";
import NodeRow from "./NodeRow";

interface Props {
  snapshot: Snapshot;
  connected: boolean;
  onDisconnect: () => void;
  sendOp: (op: Op) => void;
}

// flatten walks the tree depth-first, emitting only visible rows (children of a
// collapsed node are hidden).
function flatten(roots: TreeNode[]): TreeNode[] {
  const out: TreeNode[] = [];
  const walk = (n: TreeNode) => {
    out.push(n);
    if (!n.collapsed) n.children.forEach(walk);
  };
  roots.forEach(walk);
  return out;
}

export default function Outline({
  snapshot,
  connected,
  onDisconnect,
  sendOp,
}: Props) {
  const [editingUuid, setEditingUuid] = useState<string | null>(null);
  const [editText, setEditText] = useState("");

  const roots = useMemo(
    () => buildTree(snapshot.nodes, snapshot.root),
    [snapshot],
  );
  const rows = useMemo(() => flatten(roots), [roots]);

  const startEdit = (n: TreeNode) => {
    setEditingUuid(n.uuid);
    setEditText(n.name);
  };
  const commitEdit = () => {
    if (editingUuid !== null) {
      sendOp({ op: "update_name", uuid: editingUuid, name: editText });
    }
    setEditingUuid(null);
  };

  const renderRow = ({ item }: { item: TreeNode }) => {
    if (item.uuid === editingUuid) {
      return (
        <View style={[styles.editRow, { paddingLeft: 6 + item.depth * 16 }]}>
          <TextInput
            style={styles.editInput}
            value={editText}
            onChangeText={setEditText}
            autoFocus
            multiline
            onSubmitEditing={commitEdit}
            onBlur={commitEdit}
            blurOnSubmit
          />
          <Pressable
            hitSlop={8}
            style={styles.miniBtn}
            onPress={() => {
              sendOp({ op: "add", parentUuid: item.uuid, name: "" });
              commitEdit();
            }}
          >
            <Text style={styles.miniBtnText}>＋child</Text>
          </Pressable>
          <Pressable
            hitSlop={8}
            style={styles.miniBtn}
            onPress={() => {
              sendOp({ op: "delete", uuid: item.uuid });
              setEditingUuid(null);
            }}
          >
            <Text style={[styles.miniBtnText, { color: colors.red }]}>del</Text>
          </Pressable>
        </View>
      );
    }
    return (
      <NodeRow
        node={item}
        chips={snapshot.chips}
        onToggleCollapse={(n) =>
          sendOp({ op: "set_collapsed", uuid: n.uuid, collapsed: !n.collapsed })
        }
        onToggleDone={(n) => sendOp({ op: "toggle_done", uuid: n.uuid })}
        onPress={startEdit}
      />
    );
  };

  return (
    <View style={styles.wrap}>
      <View style={styles.header}>
        <View style={styles.headerLeft}>
          <View
            style={[
              styles.dot,
              { backgroundColor: connected ? colors.green : colors.red },
            ]}
          />
          <Text style={styles.headerTitle}>lflow</Text>
          <Text style={styles.headerCount}>· {snapshot.nodes.length} nodes</Text>
        </View>
        <View style={styles.headerLeft}>
          <Pressable
            hitSlop={8}
            onPress={() => sendOp({ op: "add", parentUuid: snapshot.root, name: "" })}
          >
            <Text style={styles.headerBtn}>＋ node</Text>
          </Pressable>
          <Pressable hitSlop={8} onPress={onDisconnect}>
            <Text style={[styles.headerBtn, { color: colors.dim }]}>disconnect</Text>
          </Pressable>
        </View>
      </View>

      <FlatList
        data={rows}
        keyExtractor={(n) => n.uuid}
        renderItem={renderRow}
        style={styles.list}
        contentContainerStyle={{ paddingVertical: 8 }}
        keyboardShouldPersistTaps="handled"
      />
    </View>
  );
}

const styles = StyleSheet.create({
  wrap: { flex: 1, backgroundColor: colors.bg },
  header: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 14,
    paddingVertical: 12,
    borderBottomWidth: 1,
    borderBottomColor: colors.border,
  },
  headerLeft: { flexDirection: "row", alignItems: "center", gap: 8 },
  dot: { width: 8, height: 8, borderRadius: 4 },
  headerTitle: { color: colors.fg, fontFamily: font.bold, fontSize: 15 },
  headerCount: { color: colors.dim, fontFamily: font.regular, fontSize: 12 },
  headerBtn: { color: colors.accent, fontFamily: font.regular, fontSize: 13 },
  list: { flex: 1 },
  editRow: {
    flexDirection: "row",
    alignItems: "center",
    paddingRight: 10,
    paddingVertical: 1,
    gap: 8,
  },
  editInput: {
    flex: 1,
    backgroundColor: colors.bgCode,
    color: colors.fg,
    fontFamily: font.regular,
    fontSize: size.base,
    lineHeight: size.line,
    borderRadius: 4,
    paddingHorizontal: 8,
    paddingVertical: 4,
    borderWidth: 1,
    borderColor: colors.accent,
  },
  miniBtn: { paddingHorizontal: 4 },
  miniBtnText: { color: colors.accent, fontFamily: font.regular, fontSize: 12 },
});
