import { useState } from 'react'
import { serverBase, setServer } from '../api'
import {
  IcCheck,
  IcCollapse,
  IcExpand,
  IcExport,
  IcGear,
  IcRedo,
  IcStar,
  IcStarFilled,
  IcSwap,
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

// Bottom sheets: type picker, node menu, settings/extensions. One scrim, one
// sliding panel, content varies.
export function Sheet(props: { open: boolean; onClose(): void; children: React.ReactNode }) {
  return (
    <>
      <div className={'scrim' + (props.open ? ' show' : '')} onClick={props.onClose} />
      <div className={'sheet' + (props.open ? ' open' : '')}>{props.children}</div>
    </>
  )
}

export function TypePicker(props: { uuid: string; onClose(): void }) {
  const current = store.get(props.uuid)?.type
  return (
    <div>
      <div className="sheet-head">Node type</div>
      <div className="type-grid">
        {pickableTypes().map((t) => (
          <div
            key={t.type}
            className={'type-cell' + (t.type === current ? ' current' : '')}
            onClick={() => {
              void store.setType(props.uuid, t.type)
              props.onClose()
            }}
          >
            <span className="type-glyph">{t.glyph}</span>
            <span>{t.label}</span>
          </div>
        ))}
      </div>
    </div>
  )
}

// KebabMenu is the ⋮ menu, mirroring the reference app: Undo/Redo, Turn
// into…, Add to Starred, Export, Expand/Collapse all, Delete, Show completed,
// Settings, autosave line. target is the node being edited, else the zoomed
// node ('' at Home — the node rows disable themselves).
export function KebabMenu(props: {
  target: string
  zoom: string
  showCompleted: boolean
  live: boolean
  onClose(): void
  onType(): void
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
  return (
    <div className="kebab-menu">
      <div className="menu-split">
        <button className="menu-half" disabled={!store.canUndo()} onClick={act(() => void store.undo())}>
          <IcUndo size={21} /> Undo
        </button>
        <button className="menu-half" disabled={!store.canRedo()} onClick={act(() => void store.redo())}>
          <IcRedo size={21} /> Redo
        </button>
      </div>
      <div className="menu-list">
        <div className={'menu-item' + (n ? '' : ' disabled')} onClick={n ? props.onType : undefined}>
          <span className="menu-glyph"><IcSwap size={21} /></span> Turn into…
        </div>
        <div
          className={'menu-item' + (n ? '' : ' disabled')}
          onClick={n ? act(() => void store.setStarred(n.uuid, !n.starred)) : undefined}
        >
          <span className="menu-glyph">{n?.starred ? <IcStarFilled size={21} /> : <IcStar size={21} />}</span>
          {n?.starred ? 'Remove from Starred' : 'Add to Starred'}
        </div>
        <div className="menu-item" onClick={act(exportText)}>
          <span className="menu-glyph"><IcExport size={21} /></span> Export
        </div>
        <div className="menu-item" onClick={act(() => void store.setAllCollapsed(props.zoom, false))}>
          <span className="menu-glyph"><IcExpand size={20} /></span> Expand all
        </div>
        <div className="menu-item" onClick={act(() => void store.setAllCollapsed(props.zoom, true))}>
          <span className="menu-glyph"><IcCollapse size={20} /></span> Collapse all
        </div>
        <div
          className={'menu-item' + (n ? '' : ' disabled')}
          onClick={n ? act(() => void store.remove(n.uuid)) : undefined}
        >
          <span className="menu-glyph"><IcTrash size={21} /></span> Delete
        </div>
        <div className="menu-item" onClick={props.onToggleCompleted}>
          <span className="menu-glyph"><IcCheck size={21} /></span> Show completed
          <span className={'toggle' + (props.showCompleted ? ' on' : '')}>
            <span className="knob" />
          </span>
        </div>
        <div className="menu-item" onClick={props.onSettings}>
          <span className="menu-glyph"><IcGear size={21} /></span> Settings
        </div>
      </div>
      <div className="menu-foot">{props.live ? 'Autosaved · live sync on' : 'Autosaved · reconnecting …'}</div>
    </div>
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
