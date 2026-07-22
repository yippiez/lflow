// The outline store: one flat map of nodes plus a children index, fed by the
// initial /api/outline fetch and folded-into by SSE change events — the same
// model the TUI's live sync keeps. Local edits apply optimistically and a
// dirty shield keeps a node being edited here from adopting a remote version
// (the pending write lands within a second and wins — last writer wins).
import { api, subscribe, type ChangeEvent, type NodeData, instanceId } from './api'

export const ROOT = 'root'

type Listener = () => void

type UndoEntry = { uuid: string; field: 'name' | 'note'; before: string; after: string }

class OutlineStore {
  nodes = new Map<string, NodeData>()
  private childIndex = new Map<string, string[]>() // parent → child uuids sorted by rank
  private listeners = new Set<Listener>()
  private dirty = new Set<string>() // uuids with local edits not yet acked
  private unsub: (() => void) | null = null

  live = false
  loaded = false
  loadError = ''
  version = 0 // bumped on every change; components subscribe via useSyncExternalStore

  // text-edit undo: one entry per flushed name/note save. Structural changes
  // (moves, deletes) are not undoable — matching the reference app's Undo,
  // which covers typing.
  private undoStack: UndoEntry[] = []
  private redoStack: UndoEntry[] = []

  async start() {
    this.unsub?.()
    this.unsub = subscribe(
      (ev) => this.applyEvent(ev),
      (live) => {
        const wasLive = this.live
        this.live = live
        if (live && !wasLive && this.loaded) this.refetch() // gap: resync wholesale
        this.bump()
      },
    )
    await this.refetch()
  }

  stop() {
    this.unsub?.()
    this.unsub = null
  }

  async refetch() {
    try {
      const { nodes } = await api.outline()
      this.nodes.clear()
      for (const n of nodes) this.nodes.set(n.uuid, n)
      this.reindex()
      this.loaded = true
      this.loadError = ''
    } catch (e) {
      this.loadError = e instanceof Error ? e.message : String(e)
    }
    this.bump()
  }

  private applyEvent(ev: ChangeEvent) {
    if (!ev.nodes?.length) return
    for (const n of ev.nodes) {
      // dirty shield: never let a remote row clobber a node mid-edit here,
      // unless the event is our own write echoing back
      if (this.dirty.has(n.uuid) && ev.instance !== instanceId) continue
      if (n.deleted) this.nodes.delete(n.uuid)
      else this.nodes.set(n.uuid, n)
    }
    this.reindex()
    this.bump()
  }

  private reindex() {
    this.childIndex.clear()
    for (const n of this.nodes.values()) {
      const list = this.childIndex.get(n.parent_uuid)
      if (list) list.push(n.uuid)
      else this.childIndex.set(n.parent_uuid, [n.uuid])
    }
    for (const list of this.childIndex.values()) {
      list.sort((a, b) => {
        const na = this.nodes.get(a)!
        const nb = this.nodes.get(b)!
        return na.rank - nb.rank || na.added_on - nb.added_on
      })
    }
  }

  // --- reads ---

  get(uuid: string): NodeData | undefined {
    return this.nodes.get(uuid)
  }

  children(uuid: string): NodeData[] {
    return (this.childIndex.get(uuid) ?? []).map((id) => this.nodes.get(id)!)
  }

  hasChildren(uuid: string): boolean {
    return (this.childIndex.get(uuid)?.length ?? 0) > 0
  }

  ancestors(uuid: string): NodeData[] {
    const path: NodeData[] = []
    let cur = this.nodes.get(uuid)
    while (cur && cur.parent_uuid && cur.parent_uuid !== cur.uuid) {
      const p = this.nodes.get(cur.parent_uuid)
      if (!p) break
      path.unshift(p)
      cur = p
    }
    return path
  }

  starred(): NodeData[] {
    return [...this.nodes.values()]
      .filter((n) => n.starred)
      .sort((a, b) => b.edited_on - a.edited_on)
  }

  recent(limit = 30): NodeData[] {
    return [...this.nodes.values()]
      .filter((n) => n.uuid !== ROOT && n.name !== '')
      .sort((a, b) => b.edited_on - a.edited_on)
      .slice(0, limit)
  }

  // --- writes (optimistic; server echo confirms) ---

  private patchLocal(uuid: string, fields: Partial<NodeData>) {
    const n = this.nodes.get(uuid)
    if (!n) return
    this.nodes.set(uuid, { ...n, ...fields })
    this.reindex()
    this.bump()
  }

  markDirty(uuid: string) {
    this.dirty.add(uuid)
  }

  clearDirty(uuid: string) {
    this.dirty.delete(uuid)
  }

  async create(
    parent: string,
    opts: { after?: string; position?: 'top' | 'bottom' | ''; name?: string; type?: string; mirrorOf?: string } = {},
  ) {
    const n = await api.create({
      parent_uuid: parent,
      name: opts.name ?? '',
      type: opts.type ?? 'bullets',
      mirror_of: opts.mirrorOf,
      after: opts.after,
      position: opts.position ?? '',
    })
    this.nodes.set(n.uuid, n)
    // an insert after a sibling shifted later ranks server-side; refetch is
    // overkill, nudge local ranks the same way instead
    if (opts.after) {
      for (const sib of this.children(parent)) {
        if (sib.uuid !== n.uuid && sib.rank >= n.rank) {
          this.nodes.set(sib.uuid, { ...sib, rank: sib.rank + 1 })
        }
      }
    }
    this.reindex()
    this.bump()
    return n
  }

  async setName(uuid: string, name: string) {
    this.pushUndo(uuid, 'name', name)
    this.patchLocal(uuid, { name })
    const fresh = await api.patch(uuid, { name })
    this.clearDirty(uuid)
    this.patchLocal(uuid, fresh)
  }

  async setNote(uuid: string, note: string) {
    this.pushUndo(uuid, 'note', note)
    this.patchLocal(uuid, { note })
    await api.patch(uuid, { note })
    this.clearDirty(uuid)
  }

  private pushUndo(uuid: string, field: 'name' | 'note', after: string) {
    const before = this.nodes.get(uuid)?.[field]
    if (before === undefined || before === after) return
    this.undoStack.push({ uuid, field, before, after })
    if (this.undoStack.length > 200) this.undoStack.shift()
    this.redoStack = []
  }

  canUndo() {
    return this.undoStack.length > 0
  }

  canRedo() {
    return this.redoStack.length > 0
  }

  async undo() {
    const e = this.undoStack.pop()
    if (!e) return
    this.redoStack.push(e)
    this.patchLocal(e.uuid, { [e.field]: e.before })
    await api.patch(e.uuid, { [e.field]: e.before })
  }

  async redo() {
    const e = this.redoStack.pop()
    if (!e) return
    this.undoStack.push(e)
    this.patchLocal(e.uuid, { [e.field]: e.after })
    await api.patch(e.uuid, { [e.field]: e.after })
  }

  // subtree walk for expand/collapse-all and export
  walk(uuid: string, fn: (n: NodeData, depth: number) => void, depth = 0) {
    for (const c of this.children(uuid)) {
      fn(c, depth)
      this.walk(c.uuid, fn, depth + 1)
    }
  }

  async setAllCollapsed(rootUUID: string, collapsed: boolean) {
    const targets: string[] = []
    this.walk(rootUUID, (n) => {
      if (this.hasChildren(n.uuid) && n.collapsed !== collapsed) targets.push(n.uuid)
    })
    for (const id of targets) this.patchLocal(id, { collapsed })
    await Promise.all(targets.map((id) => api.patch(id, { collapsed })))
  }

  // exportText renders the subtree as indented plain text (Workflowy-style)
  exportText(rootUUID: string): string {
    const lines: string[] = []
    const root = this.get(rootUUID)
    if (root && rootUUID !== ROOT) lines.push(root.name)
    this.walk(rootUUID, (n, depth) => {
      const pad = '  '.repeat(depth + (rootUUID === ROOT ? 0 : 1))
      lines.push(`${pad}- ${n.name.replace(/\n/g, '\n' + pad + '  ')}`)
      if (n.note) lines.push(`${pad}  ${n.note.replace(/\n/g, '\n' + pad + '  ')}`)
    })
    return lines.join('\n') + '\n'
  }

  async setType(uuid: string, type: string) {
    this.patchLocal(uuid, { type })
    await api.patch(uuid, { type })
  }

  // setStyle helpers rewrite tokens in the node's style string (other tokens
  // survive). Style is a per-node attribute, never markup in the text — the
  // no-markup invariant. Token vocabulary matches the TUI's pkg/tui/style.
  private async rewriteStyle(uuid: string, drop: (t: string) => boolean, add?: string) {
    const n = this.nodes.get(uuid)
    if (!n) return
    const tokens = n.style.split(',').filter((t) => t !== '' && !drop(t))
    if (add) tokens.push(add)
    const style = tokens.join(',')
    this.patchLocal(uuid, { style })
    await api.patch(uuid, { style })
  }

  async setColor(uuid: string, color: string) {
    await this.rewriteStyle(uuid, (t) => t.startsWith('color:'), color ? 'color:' + color : undefined)
  }

  async setHighlight(uuid: string, color: string) {
    await this.rewriteStyle(uuid, (t) => t.startsWith('bg:'), color ? 'bg:' + color : undefined)
  }

  async toggleAttr(uuid: string, attr: string) {
    const has = this.nodes.get(uuid)?.style.split(',').includes(attr)
    await this.rewriteStyle(uuid, (t) => t === attr, has ? undefined : attr)
  }

  // duplicate copies the node (name, note, type, style, mirror target) and its
  // whole subtree, landing right after the original.
  async duplicate(uuid: string): Promise<NodeData | undefined> {
    const n = this.nodes.get(uuid)
    if (!n) return
    const copySubtree = async (src: NodeData, parent: string, after?: string) => {
      const made = await api.create({
        parent_uuid: parent,
        name: src.name,
        note: src.note,
        type: src.type,
        style: src.style,
        mirror_of: src.mirror_of || undefined,
        after,
        position: after ? '' : 'bottom',
      })
      this.nodes.set(made.uuid, made)
      for (const c of this.children(src.uuid)) {
        await copySubtree(c, made.uuid)
      }
      return made
    }
    const made = await copySubtree(n, n.parent_uuid, n.uuid)
    this.reindex()
    this.bump()
    return made
  }

  async setCollapsed(uuid: string, collapsed: boolean) {
    this.patchLocal(uuid, { collapsed })
    await api.patch(uuid, { collapsed })
  }

  async setStarred(uuid: string, starred: boolean) {
    this.patchLocal(uuid, { starred })
    await api.patch(uuid, { starred })
  }

  async setCompleted(uuid: string, done: boolean) {
    this.patchLocal(uuid, { completed_at: done ? Date.now() * 1e6 : 0 })
    await api.patch(uuid, { completed: done })
  }

  async move(uuid: string, parent: string, opts: { after?: string; position?: string } = {}) {
    const fresh = await api.move(uuid, { parent_uuid: parent, ...opts })
    this.patchLocal(uuid, fresh)
  }

  async remove(uuid: string) {
    const drop = (id: string) => {
      for (const c of this.children(id)) drop(c.uuid)
      this.nodes.delete(id)
    }
    drop(uuid)
    this.reindex()
    this.bump()
    await api.remove(uuid)
  }

  // indent: node becomes last child of its previous sibling
  async indent(uuid: string) {
    const n = this.nodes.get(uuid)
    if (!n) return
    const sibs = this.children(n.parent_uuid)
    const i = sibs.findIndex((s) => s.uuid === uuid)
    if (i <= 0) return
    const newParent = sibs[i - 1]
    this.patchLocal(uuid, { parent_uuid: newParent.uuid })
    if (newParent.collapsed) this.setCollapsed(newParent.uuid, false)
    await this.move(uuid, newParent.uuid, { position: 'bottom' })
  }

  // outdent: node becomes the sibling right after its parent
  async outdent(uuid: string) {
    const n = this.nodes.get(uuid)
    if (!n) return
    const parent = this.nodes.get(n.parent_uuid)
    if (!parent || parent.uuid === ROOT || !parent.parent_uuid) return
    this.patchLocal(uuid, { parent_uuid: parent.parent_uuid })
    await this.move(uuid, parent.parent_uuid, { after: parent.uuid })
  }

  // --- subscription plumbing ---

  private bump() {
    this.version++
    for (const l of this.listeners) l()
  }

  onChange(l: Listener): () => void {
    this.listeners.add(l)
    return () => this.listeners.delete(l)
  }
}

export const store = new OutlineStore()
