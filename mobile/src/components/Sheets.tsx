import { useState } from 'react'
import { serverBase, setServer } from '../api'
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

export function NodeMenu(props: {
  uuid: string
  onClose(): void
  onZoom(uuid: string): void
  onType(): void
  onNote(): void
}) {
  const n = store.get(props.uuid)
  if (!n) return null
  const act = (fn: () => void) => () => {
    fn()
    props.onClose()
  }
  return (
    <div>
      <div className="sheet-head">{n.name || 'untitled'}</div>
      <div className="menu-list">
        <div className="menu-item" onClick={act(() => props.onZoom(n.uuid))}>
          <span className="type-glyph">⊙</span> Zoom in
        </div>
        <div className="menu-item" onClick={act(() => void store.setStarred(n.uuid, !n.starred))}>
          <span className="type-glyph">◆</span> {n.starred ? 'Unstar' : 'Star'}
        </div>
        <div className="menu-item" onClick={props.onType}>
          <span className="type-glyph">≔</span> Change type…
        </div>
        <div className="menu-item" onClick={props.onNote}>
          <span className="type-glyph">✎</span> {n.note ? 'Edit note' : 'Add note'}
        </div>
        <div
          className="menu-item"
          onClick={act(() =>
            void store.setCompleted(n.uuid, !(n.completed_at > 0)),
          )}
        >
          <span className="type-glyph">✓</span> {n.completed_at > 0 ? 'Mark not done' : 'Mark done'}
        </div>
        <div className="menu-item danger" onClick={act(() => void store.remove(n.uuid))}>
          <span className="type-glyph">✕</span> Delete
        </div>
      </div>
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
