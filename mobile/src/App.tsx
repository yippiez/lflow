import { useEffect, useRef, useState } from 'react'
import { useOutline } from './hooks'
import { ROOT, store } from './store'
import { renderName } from './tags'
import {
  IcAt,
  IcCheck,
  IcChevronLeft,
  IcCode,
  IcHome,
  IcIndent,
  IcKebab,
  IcKeyboardDown,
  IcMenu,
  IcOutdent,
  IcPencil,
  IcPlus,
  IcRedo,
  IcRun,
  IcSearch,
  IcUndo,
} from './icons'
import { Row, type EditController, type RowCallbacks } from './components/Row'
import { Search } from './components/Search'
import { Sheet, KebabMenu, MirrorPicker, Settings, TypePicker } from './components/Sheets'
import { Sidebar } from './components/Sidebar'
import { nodeColor } from './tags'

type SheetKind = null | { kind: 'type' | 'menu' | 'settings' | 'mirror' }

export default function App() {
  useOutline()
  const [zoom, setZoom] = useState<string>(ROOT)
  const [editing, setEditing] = useState<string | null>(null)
  const [noteEditing, setNoteEditing] = useState<string | null>(null)
  const [sidebar, setSidebar] = useState(false)
  const [search, setSearch] = useState<{ open: boolean; q: string }>({ open: false, q: '' })
  const [sheet, setSheet] = useState<SheetKind>(null)
  const [titleEdit, setTitleEdit] = useState(false)
  const [showCompleted, setShowCompleted] = useState(
    localStorage.getItem('lflow.showCompleted') !== '0',
  )
  const scrollRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    void store.start()
    return () => store.stop()
  }, [])

  const zoomed = store.get(zoom) ?? store.get(ROOT)
  const zoomID = zoomed?.uuid ?? ROOT
  const children = store.children(zoomID)
  const crumbs = zoomed ? store.ancestors(zoomID) : []

  const stopEdit = () => {
    setEditing(null)
    setNoteEditing(null)
  }

  const edit: EditController = {
    editing,
    noteEditing,
    start: (uuid) => {
      setNoteEditing(null)
      setEditing(uuid)
    },
    startNote: (uuid) => {
      setEditing(null)
      setNoteEditing(uuid)
    },
    stop: stopEdit,
    enterAfter: (uuid) => {
      const n = store.get(uuid)
      if (!n) return
      void store
        .create(n.parent_uuid === '' ? zoomID : n.parent_uuid, { after: uuid })
        .then((created) => {
          store.markDirty(created.uuid)
          setEditing(created.uuid)
        })
    },
    deleteEmpty: (uuid) => {
      const n = store.get(uuid)
      if (!n || store.hasChildren(uuid)) return
      const sibs = store.children(n.parent_uuid)
      const i = sibs.findIndex((s) => s.uuid === uuid)
      const prev = i > 0 ? sibs[i - 1] : null
      void store.remove(uuid)
      if (prev && !prev.readonly) {
        store.markDirty(prev.uuid)
        setEditing(prev.uuid)
      } else {
        stopEdit()
      }
    },
  }

  const cb: RowCallbacks = {
    onZoom: (uuid) => {
      stopEdit()
      setZoom(uuid)
      scrollRef.current?.scrollTo(0, 0)
    },
    onTag: (tag) => setSearch({ open: true, q: tag }),
    showCompleted,
    edit,
  }

  const toggleCompleted = () => {
    const next = !showCompleted
    setShowCompleted(next)
    localStorage.setItem('lflow.showCompleted', next ? '1' : '0')
  }

  // the ⋮ menu operates on the node being edited, else the zoomed page node
  const menuTarget = editing ?? (zoomID !== ROOT ? zoomID : '')

  // @-button: insert at the caret of the active inline editor
  const insertAtCaret = (text: string) => {
    const el = document.activeElement
    if (el instanceof HTMLTextAreaElement) {
      el.focus()
      document.execCommand('insertText', false, text)
    }
  }

  const addNode = () => {
    void store.create(zoomID, { position: 'bottom' }).then((created) => {
      store.markDirty(created.uuid)
      setEditing(created.uuid)
    })
  }

  const back = () => {
    stopEdit()
    if (zoomed && zoomed.uuid !== ROOT && zoomed.parent_uuid) setZoom(zoomed.parent_uuid)
  }

  if (!store.loaded) {
    return (
      <div className="boot">
        <div className="boot-title">lflow</div>
        {store.loadError ? (
          <>
            <div className="boot-err">cannot reach the server: {store.loadError}</div>
            <button className="btn" onClick={() => setSheet({ kind: 'settings' })}>
              Server settings
            </button>
          </>
        ) : (
          <div className="boot-dim">connecting …</div>
        )}
        <Sheet open={sheet?.kind === 'settings'} onClose={() => setSheet(null)}>
          <Settings onClose={() => setSheet(null)} />
        </Sheet>
      </div>
    )
  }

  return (
    <div className="app">
      <div className="topbar">
        <span className="crumb-home" onClick={() => cb.onZoom(ROOT)}>
          <IcHome size={22} />
        </span>
        {crumbs
          .filter((c) => c.uuid !== ROOT || zoomID !== ROOT)
          .map((c) => (
            <span key={c.uuid} className="crumb" onClick={() => cb.onZoom(c.uuid)}>
              <span className="crumb-sep">›</span>
              {c.uuid === ROOT ? 'Home' : c.name || 'untitled'}
            </span>
          ))}
        {zoomID !== ROOT && (
          <span className="crumb current">
            <span className="crumb-sep">›</span>
            {zoomed?.name || 'untitled'}
          </span>
        )}
        <span className="top-spacer" />
        <span className={'live-dot' + (store.live ? ' on' : '')} title="live sync" />
        <span className="icon-btn kebab" onClick={() => setSheet({ kind: 'menu' })}>
          <IcKebab size={22} />
        </span>
      </div>

      <div className="scroll" ref={scrollRef}>
        {titleEdit && zoomed && zoomID !== ROOT ? (
          <input
            className="title-input"
            autoFocus
            defaultValue={zoomed.name}
            onBlur={(e) => {
              setTitleEdit(false)
              if (e.target.value !== zoomed.name) void store.setName(zoomID, e.target.value)
            }}
            onKeyDown={(e) => {
              if (e.key === 'Enter') (e.target as HTMLInputElement).blur()
            }}
          />
        ) : (
          <div
            className="title"
            style={zoomed && nodeColor(zoomed.style) ? { color: nodeColor(zoomed.style) } : undefined}
            onClick={() => {
              if (zoomID !== ROOT && !zoomed?.readonly) setTitleEdit(true)
            }}
          >
            {zoomID === ROOT ? 'Home' : renderName(zoomed?.name || 'untitled', cb.onTag)}
          </div>
        )}
        {zoomed?.note && zoomID !== ROOT && <div className="title-note">{zoomed.note}</div>}

        <div className="outline">
          {children
            .filter((c) => showCompleted || c.completed_at === 0)
            .map((c) => (
              <Row key={c.uuid} node={c} depth={0} cb={cb} />
            ))}
          <div className="add-row" onClick={addNode}>
            <IcPlus size={24} />
          </div>
        </div>
      </div>

      {editing ? (
        <div className="toolbar-wrap">
          <div className="toolbar">
            <button className="tb" onClick={() => void store.outdent(editing)}>
              <IcOutdent size={23} />
            </button>
            <button className="tb" onClick={() => void store.indent(editing)}>
              <IcIndent size={23} />
            </button>
            <button className="tb" disabled={!store.canUndo()} onClick={() => void store.undo()}>
              <IcUndo size={23} />
            </button>
            <button className="tb" disabled={!store.canRedo()} onClick={() => void store.redo()}>
              <IcRedo size={23} />
            </button>
            <button
              className="tb"
              onClick={() => {
                const n = store.get(editing)
                if (n) void store.setCompleted(editing, !(n.completed_at > 0))
              }}
            >
              <IcCheck size={23} />
            </button>
            <button className="tb" onClick={() => edit.startNote(editing)}>
              <IcPencil size={23} />
            </button>
            <button className="tb" onClick={() => insertAtCaret('@')}>
              <IcAt size={23} />
            </button>
            <button
              className="tb"
              onClick={() => {
                const n = store.get(editing)
                if (n) void store.setType(editing, n.type === 'code' ? 'bullets' : 'code')
              }}
            >
              <IcCode size={23} />
            </button>
            <span className="tb-gap" />
            <button className="tb tb-kbd" onClick={stopEdit}>
              <IcKeyboardDown size={25} />
            </button>
          </div>
        </div>
      ) : (
        <div className="bottombar">
          <button className="bb" onClick={() => setSidebar(true)}>
            <IcMenu size={27} />
          </button>
          <button className="bb" disabled={zoomID === ROOT} onClick={back}>
            <IcChevronLeft size={27} />
          </button>
          <button className="bb add" onClick={addNode}>
            <IcPlus size={29} />
          </button>
          {/* Run: reserved for a coming feature — present but disabled */}
          <button className="bb" disabled>
            <IcRun size={26} />
          </button>
          <button className="bb" onClick={() => setSearch({ open: true, q: '' })}>
            <IcSearch size={26} />
          </button>
        </div>
      )}

      <Sidebar
        open={sidebar}
        onClose={() => setSidebar(false)}
        onZoom={cb.onZoom}
        onNewNode={addNode}
      />
      <Search
        open={search.open}
        initial={search.q}
        onClose={() => setSearch({ open: false, q: '' })}
        onZoom={cb.onZoom}
      />
      <Sheet open={sheet !== null} onClose={() => setSheet(null)}>
        {sheet?.kind === 'type' && menuTarget && (
          <TypePicker uuid={menuTarget} onClose={() => setSheet(null)} />
        )}
        {sheet?.kind === 'menu' && (
          <KebabMenu
            target={menuTarget}
            zoom={zoomID}
            showCompleted={showCompleted}
            live={store.live}
            onClose={() => setSheet(null)}
            onType={() => setSheet({ kind: 'type' })}
            onMirror={() => setSheet({ kind: 'mirror' })}
            onSettings={() => setSheet({ kind: 'settings' })}
            onToggleCompleted={toggleCompleted}
          />
        )}
        {sheet?.kind === 'mirror' && menuTarget && (
          <MirrorPicker
            target={menuTarget}
            onClose={() => setSheet(null)}
            onZoom={cb.onZoom}
          />
        )}
        {sheet?.kind === 'settings' && <Settings onClose={() => setSheet(null)} />}
      </Sheet>
    </div>
  )
}
