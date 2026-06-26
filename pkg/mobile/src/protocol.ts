// Wire protocol shared with the Go server (pkg/tui/live). Keep these shapes in
// sync with snapshot.go / ops.go.

export interface WireNode {
  uuid: string;
  parentUuid: string;
  rank: number;
  name: string;
  note: string;
  type: string;
  style: string;
  mirrorOf: string;
  linkTo: string;
  completedAt: number;
  collapsed: boolean;
  readonly: boolean;
  addedOn: number;
  editedOn: number;
}

export interface WireChip {
  kind: string; // "path" | "tag" | "date" | ...
  value: string;
}

export interface Snapshot {
  type: "snapshot";
  root: string;
  nodes: WireNode[];
  chips: Record<string, WireChip>;
}

// An edit op sent to the server. Only the fields relevant to `op` are set.
export type Op =
  | { op: "update_name"; uuid: string; name: string }
  | { op: "toggle_done"; uuid: string }
  | { op: "set_collapsed"; uuid: string; collapsed: boolean }
  | { op: "add"; parentUuid: string; name: string; nodeType?: string }
  | { op: "delete"; uuid: string }
  | { op: "move"; uuid: string; parentUuid: string; rank: number };

// TreeNode is a WireNode plus its resolved children, ordered by rank.
export interface TreeNode extends WireNode {
  children: TreeNode[];
  depth: number;
}

// buildTree turns the flat node list into a tree rooted at `rootUuid`,
// returning that root's children (the top-level outline). Siblings are ordered
// by rank, matching GetChildren on the server.
export function buildTree(nodes: WireNode[], rootUuid: string): TreeNode[] {
  const byParent = new Map<string, WireNode[]>();
  for (const n of nodes) {
    const arr = byParent.get(n.parentUuid) ?? [];
    arr.push(n);
    byParent.set(n.parentUuid, arr);
  }
  const build = (parentUuid: string, depth: number): TreeNode[] => {
    const kids = (byParent.get(parentUuid) ?? [])
      .slice()
      .sort((a, b) => a.rank - b.rank);
    return kids.map((n) => ({
      ...n,
      depth,
      children: build(n.uuid, depth + 1),
    }));
  };
  return build(rootUuid, 0);
}

// ---- chips ----------------------------------------------------------------

// U+FFFC sentinel surrounds a chip id inside a node name: "￼<id>￼".
const CHIP_SENTINEL = "￼";

export type NameSeg =
  | { kind: "text"; text: string }
  | { kind: "chip"; chip: WireChip; id: string };

// splitName breaks a node name into plain-text and chip segments, resolving
// each anchor against the chip store (mirrors database.resolveAnchors).
export function splitName(
  name: string,
  chips: Record<string, WireChip>,
): NameSeg[] {
  if (!name.includes(CHIP_SENTINEL)) return [{ kind: "text", text: name }];
  const segs: NameSeg[] = [];
  let i = 0;
  while (i < name.length) {
    const open = name.indexOf(CHIP_SENTINEL, i);
    if (open === -1) {
      segs.push({ kind: "text", text: name.slice(i) });
      break;
    }
    if (open > i) segs.push({ kind: "text", text: name.slice(i, open) });
    const close = name.indexOf(CHIP_SENTINEL, open + 1);
    if (close === -1) {
      segs.push({ kind: "text", text: name.slice(open) });
      break;
    }
    const id = name.slice(open + 1, close);
    const chip = chips[id] ?? { kind: "", value: "@?" };
    segs.push({ kind: "chip", chip, id });
    i = close + 1;
  }
  return segs;
}

function basename(p: string): string {
  const parts = p.replace(/\/+$/, "").split("/");
  const b = parts[parts.length - 1];
  return b || p;
}

// chipDisplay is a chip's compact form (mirrors database.ChipDisplay).
export function chipDisplay(c: WireChip): string {
  switch (c.kind) {
    case "path":
      return "›" + basename(c.value);
    case "tag":
      return "#" + c.value;
    default:
      return c.value;
  }
}

// ---- style ----------------------------------------------------------------

export interface ParsedStyle {
  color?: string; // a styleColor key (red/orange/.../gray)
  bold: boolean;
  italic: boolean;
  underline: boolean;
  strike: boolean;
}

export function parseStyle(style: string): ParsedStyle {
  const out: ParsedStyle = {
    bold: false,
    italic: false,
    underline: false,
    strike: false,
  };
  for (const tok of style.split(",")) {
    if (!tok) continue;
    if (tok === "bold") out.bold = true;
    else if (tok === "italic") out.italic = true;
    else if (tok === "underline") out.underline = true;
    else if (tok === "strike") out.strike = true;
    else if (tok.startsWith("color:")) out.color = tok.slice("color:".length);
  }
  return out;
}
