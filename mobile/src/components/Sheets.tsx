import { useEffect, useState, type ReactNode } from 'react'
import type { NodeData } from '../api'
import { api, serverBase, setServer } from '../api'
import { NODE_COLORS, renderName } from '../tags'
import {
  IcBullets,
  IcCheck,
  IcChevronDown,
  IcCodeBlock,
  IcCollapse,
  IcDiamond,
  IcDuplicate,
  IcExpand,
  IcExport,
  IcGear,
  IcLogList,
  IcMoveTo,
  IcNote,
  IcQuoteBlock,
  IcRedo,
  IcStar,
  IcStarFilled,
  IcSwap,
  IcTodoList,
  IcTrash,
  IcUndo,
} from '../icons'
import {
  addCustomExtension,
  customExtensions,
  pickableTypes,
  removeCustomExtension,
} from '../extensions/registry'
import { store } from '../store'

// Bottom sheets: full-width flat panels, system-sans rows with hairline
// separators — the reference app's menu look. One scrim, content varies.
export function Sheet(props: { open: boolean; onClose(): void; children: React.ReactNode }) {
  return (
    <>
      <div className={'scrim' + (props.open ? ' show' : '')} onClick={props.onClose} />
      <div className={'sheet' + (props.open ? ' open' : '')}>{props.children}</div>
    </>
  )
}

// typeIcon maps a node type to its picker icon (thin stroke, like the rest).
function typeIcon(type: string, glyph: string): ReactNode {
  switch (type) {
    case 'bullets':
      return <IcBullets size={22} />
    case 'todo':
      return <IcTodoList size={22} />
    case 'h1':
      return <span className="type-h">H1</span>
    case 'h2':
      return <span className="type-h">H2</span>
    case 'h3':
      return <span className="type-h">H3</span>
    case 'quote':
      return <IcQuoteBlock size={22} />
    case 'code':
      return <IcCodeBlock size={22} />
    case 'json':
      return <span className="type-h">{'{}'}</span>
    case 'log':
      return <IcLogList size={22} />
    default:
      return <span className="type-h">{glyph}</span>
  }
}

// TypePicker: the "Turn into…" sheet — one full-width row per type.
export function TypePicker(props: { uuid: string; onClose(): void }) {
  const current = store.get(props.uuid)?.type
  return (
    <div className="menu">
      <div className="menu-list">
        {pickableTypes().map((t) => (
          <div
            key={t.type}
            className={'menu-item' + (t.type === current ? ' current' : '')}
            onClick={() => {
              void store.setType(props.uuid, t.type)
              props.onClose()
            }}
          >
            <span className="menu-glyph">{typeIcon(t.type, t.glyph)}</span>
            {t.label}
            {t.type === current && <span className="menu-right"><IcCheck size={19} /></span>}
          </div>
        ))}
      </div>
    </div>
  )
}

// ColorSheet: Text row (outlined A swatches) + Highlight row (filled A
// squares), first cell of each clears — the reference's color picker.
export function ColorSheet(props: { uuid: string; onClose(): void }) {
  const pick = (fn: () => Promise<void>) => () => {
    void fn()
    props.onClose()
  }
  return (
    <div className="menu color-sheet">
      <div className="set-label">Text</div>
      <div className="asw-row">
        <span className="asw" onClick={pick(() => store.setColor(props.uuid, ''))}>
          <span style={{ color: 'var(--dim)' }}>A</span>
        </span>
        {Object.entries(NODE_COLORS).map(([name, hex]) => (
          <span key={name} className="asw" onClick={pick(() => store.setColor(props.uuid, name))}>
            <span style={{ color: hex }}>A</span>
          </span>
        ))}
      </div>
      <div className="set-label">Highlight</div>
      <div className="asw-row">
        <span className="asw" onClick={pick(() => store.setHighlight(props.uuid, ''))}>
          <span style={{ color: 'var(--dim)' }}>A</span>
        </span>
        {Object.entries(NODE_COLORS).map(([name, hex]) => (
          <span
            key={name}
            className="asw filled"
            style={{ background: hex + '99', borderColor: 'transparent' }}
            onClick={pick(() => store.setHighlight(props.uuid, name))}
          >
            <span style={{ color: '#f2f4f5' }}>A</span>
          </span>
        ))}
      </div>
    </div>
  )
}

// KebabMenu is the ⋮ menu, mirroring the reference app's context menu.
// target is the node being edited, else the zoomed node ('' at Home — the
// node rows disable themselves).
export function KebabMenu(props: {
  target: string
  zoom: string
  showCompleted: boolean
  live: boolean
  onClose(): void
  onType(): void
  onColor(): void
  onMove(): void
  onMirror(): void
  onAddNote(uuid: string): void
  onSettings(): void
  onToggleCompleted(): void
}) {
  const n = props.target ? store.get(props.target) : undefined
  const act = (fn: () => void) => () => {
    fn()
    props.onClose()
  }
  const exportText = () => {
    const text = store.exportText(props.zoom)
    const name = (store.get(props.zoom)?.name || 'lflow') + '.txt'
    const a = document.createElement('a')
    a.href = URL.createObjectURL(new Blob([text], { type: 'text/plain' }))
    a.download = name
    a.click()
    URL.revokeObjectURL(a.href)
  }
  const nodeRow = (
    label: ReactNode,
    icon: ReactNode,
    onClick?: () => void,
    right?: ReactNode,
  ) => (
    <div className={'menu-item' + (n ? '' : ' disabled')} onClick={n ? onClick : undefined}>
      <span className="menu-glyph">{icon}</span>
      {label}
      {right && <span className="menu-right">{right}</span>}
    </div>
  )
  return (
    <div className="menu kebab-menu">
      <div className="menu-title">{n ? n.name || 'untitled' : 'Home'}</div>
      <div className="menu-split">
        <button className="menu-half" disabled={!store.canUndo()} onClick={act(() => void store.undo())}>
          <IcUndo size={20} /> Undo
        </button>
        <button className="menu-half" disabled={!store.canRedo()} onClick={act(() => void store.redo())}>
          <IcRedo size={20} /> Redo
        </button>
      </div>
      <div className="menu-list">
        {nodeRow('Turn into…', <IcSwap size={21} />, props.onType, <IcChevronDown size={19} />)}
        {nodeRow(
          n && n.completed_at > 0 ? 'Not complete' : 'Complete',
          <IcCheck size={21} />,
          n ? act(() => void store.setCompleted(n.uuid, !(n.completed_at > 0))) : undefined,
        )}
        {nodeRow('Add note', <IcNote size={20} />, n ? act(() => props.onAddNote(n.uuid)) : undefined)}
        {nodeRow('Move To…', <IcMoveTo size={21} />, props.onMove)}
        {nodeRow('Mirror To…', <IcDiamond size={19} />, props.onMirror)}
        {nodeRow('Duplicate', <IcDuplicate size={20} />, n ? act(() => void store.duplicate(n.uuid)) : undefined)}
        {nodeRow(
          'Color…',
          <span className="type-h" style={{ fontSize: 17 }}>A</span>,
          props.onColor,
          <IcChevronDown size={19} />,
        )}
        {nodeRow(
          n?.starred ? 'Remove from Starred' : 'Add to Starred',
          n?.starred ? <IcStarFilled size={20} /> : <IcStar size={20} />,
          n ? act(() => void store.setStarred(n.uuid, !n.starred)) : undefined,
        )}
        <div className="menu-item" onClick={act(exportText)}>
          <span className="menu-glyph"><IcExport size={20} /></span> Export
        </div>
        <div className="menu-item" onClick={act(() => void store.setAllCollapsed(props.zoom, false))}>
          <span className="menu-glyph"><IcExpand size={19} /></span> Expand all
        </div>
        <div className="menu-item" onClick={act(() => void store.setAllCollapsed(props.zoom, true))}>
          <span className="menu-glyph"><IcCollapse size={19} /></span> Collapse all
        </div>
        {nodeRow('Delete', <IcTrash size={20} />, n ? act(() => void store.remove(n.uuid)) : undefined)}
        <div className="menu-item" onClick={props.onToggleCompleted}>
          <span className="menu-glyph"><IcCheck size={20} /></span> Show completed
          <span className={'toggle' + (props.showCompleted ? ' on' : '')}>
            <span className="knob" />
          </span>
        </div>
        <div className="menu-item" onClick={props.onSettings}>
          <span className="menu-glyph"><IcGear size={20} /></span> Settings
        </div>
      </div>
      <div className="menu-foot">{props.live ? 'Autosaved · live sync on' : 'Autosaved · reconnecting …'}</div>
    </div>
  )
}

// DestPicker: shared destination chooser for Move To… and Mirror To… —
// search input, then two-line rows: node name over its dim breadcrumb path.
function DestPicker(props: {
  title: string
  exclude: string
  onPick(dest: NodeData): void
}) {
  const [q, setQ] = useState('')
  const [hits, setHits] = useState<NodeData[]>([])

  useEffect(() => {
    if (q.trim() === '') {
      setHits(store.recent(12).filter((n) => n.uuid !== props.exclude && n.mirror_of === ''))
      return
    }
    const t = window.setTimeout(async () => {
      try {
        const { nodes } = await api.search(q)
        setHits(nodes.filter((n) => n.uuid !== props.exclude && n.mirror_of === '').slice(0, 12))
      } catch {
        setHits([])
      }
    }, 150)
    return () => window.clearTimeout(t)
  }, [q, props.exclude])

  return (
    <div className="menu">
      <div className="menu-title dest-title">{props.title}</div>
      <input
        className="jump dest-input"
        autoFocus
        placeholder="Find location…"
        value={q}
        onChange={(e) => setQ(e.target.value)}
      />
      <div className="menu-list">
        {hits.map((n) => {
          const path = store
            .ancestors(n.uuid)
            .map((a) => (a.uuid === 'root' ? 'Home' : a.name || 'untitled'))
            .join(' › ')
          return (
            <div key={n.uuid} className="menu-item dest-item" onClick={() => props.onPick(n)}>
              <div className="dest-name">
                {n.starred && <span className="dest-star">★ </span>}
                {renderName(n.name || 'untitled')}
              </div>
              {path && <div className="dest-path">{path} ›</div>}
            </div>
          )
        })}
        {hits.length === 0 && <div className="side-empty">no matches</div>}
      </div>
    </div>
  )
}

// MirrorPicker: choose where a live mirror of `target` lands.
export function MirrorPicker(props: { target: string; onClose(): void; onZoom(uuid: string): void }) {
  if (!store.get(props.target)) return null
  return (
    <DestPicker
      title="MIRROR TO…"
      exclude={props.target}
      onPick={(dest) =>
        void store.create(dest.uuid, { mirrorOf: props.target }).then(() => {
          props.onClose()
          props.onZoom(dest.uuid)
        })
      }
    />
  )
}

// MovePicker: move the node (with its subtree) under a chosen parent.
export function MovePicker(props: { target: string; onClose(): void; onZoom(uuid: string): void }) {
  if (!store.get(props.target)) return null
  return (
    <DestPicker
      title="MOVE TO…"
      exclude={props.target}
      onPick={(dest) =>
        void store.move(props.target, dest.uuid, { position: 'bottom' }).then(() => {
          props.onClose()
          props.onZoom(dest.uuid)
        })
      }
    />
  )
}

export function Settings(props: { onClose(): void }) {
  const [url, setUrl] = useState(serverBase())
  const [token, setToken] = useState(localStorage.getItem('lflow.token') ?? '')
  const [extName, setExtName] = useState('')
  const [extSrc, setExtSrc] = useState('')
  const [extErr, setExtErr] = useState('')
  const [exts, setExts] = useState(customExtensions())

  return (
    <div className="settings">
      <div className="sheet-head">Settings</div>
      <div className="set-label">Server URL (empty = this origin)</div>
      <input
        className="jump"
        placeholder="http://192.168.1.10:7420"
        value={url}
        onChange={(e) => setUrl(e.target.value)}
      />
      <div className="set-label">API token (if the server sets one)</div>
      <input
        className="jump"
        placeholder="token"
        value={token}
        onChange={(e) => setToken(e.target.value)}
      />
      <button
        className="btn"
        onClick={() => {
          setServer(url.trim(), token.trim())
          location.reload()
        }}
      >
        Save &amp; reconnect
      </button>

      <div className="sheet-head">Custom note extensions</div>
      <div className="set-hint">
        An extension is an ES module that default-exports {'{type, label, glyph, render(host)}'} —
        it becomes a first-class node type, rendered by its own JavaScript.
      </div>
      {exts.map((c) => (
        <div key={c.name} className="ext-row">
          <span className="side-name">
            {c.name}
            {c.error && <span className="ext-err"> — {c.error}</span>}
          </span>
          <button
            className="icon-btn"
            onClick={() => {
              removeCustomExtension(c.name)
              setExts(customExtensions())
            }}
          >
            ✕
          </button>
        </div>
      ))}
      <input
        className="jump"
        placeholder="extension name"
        value={extName}
        onChange={(e) => setExtName(e.target.value)}
      />
      <textarea
        className="ext-src"
        placeholder={'export default {\n  type: "counter", label: "Counter", glyph: "#",\n  render(host) { … }\n}'}
        value={extSrc}
        onChange={(e) => setExtSrc(e.target.value)}
      />
      {extErr && <div className="ext-err">{extErr}</div>}
      <button
        className="btn"
        onClick={async () => {
          setExtErr('')
          try {
            await addCustomExtension(extName.trim() || 'extension', extSrc)
            setExts(customExtensions())
            setExtName('')
            setExtSrc('')
          } catch (e) {
            setExtErr(e instanceof Error ? e.message : String(e))
          }
        }}
      >
        Add extension
      </button>
    </div>
  )
}
