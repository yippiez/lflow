import { useEffect, useRef, useState, type CSSProperties, type ReactNode } from 'react'
import type { NodeData } from '../api'
import { getExtension } from '../extensions/registry'
import { store } from '../store'
import { renderName, styleCSS } from '../tags'
import { HostMount } from './HostMount'

export interface RowCallbacks {
  onZoom(uuid: string): void
  onTag(tag: string): void
  showCompleted: boolean
  edit: EditController
}

// EditController is owned by App: which node is being edited, and the verbs
// the edit toolbar triggers on it.
export interface EditController {
  editing: string | null
  noteEditing: string | null
  start(uuid: string): void
  startNote(uuid: string): void
  stop(): void
  enterAfter(uuid: string): void
  deleteEmpty(uuid: string): void
}

// TextEdit is the inline editor — WYSIWYG. A transparent-text textarea sits
// exactly on top of a backdrop div rendering the text the same way the
// display row does (tag pills included), so nothing shifts when editing
// starts or while typing; only the caret gives the edit away. Saves
// auto-flush on a debounce, matching the TUI's livesync (no unsaved state).
function TextEdit(props: {
  value: string
  className: string
  style?: CSSProperties
  placeholder?: string
  multilineEnter?: boolean // true for notes/code: Enter inserts a newline
  renderBackdrop?: (text: string) => ReactNode
  onSave(text: string): void
  onEnter?(): void
  onDeleteEmpty?(): void
  onBlur?(): void
}) {
  const [text, setText] = useState(props.value)
  const ref = useRef<HTMLTextAreaElement>(null)
  const timer = useRef<number>()
  const latest = useRef(text)
  latest.current = text

  useEffect(() => {
    const el = ref.current
    if (el) {
      el.focus()
      el.setSelectionRange(el.value.length, el.value.length)
    }
    return () => {
      window.clearTimeout(timer.current)
      if (latest.current !== props.value) props.onSave(latest.current)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const queueSave = (t: string) => {
    setText(t)
    window.clearTimeout(timer.current)
    timer.current = window.setTimeout(() => props.onSave(t), 800)
  }

  return (
    <div className="edit-wrap">
      <div className={props.className + ' edit-backdrop'} style={props.style} aria-hidden>
        {text === '' ? (
          <span className="placeholder">{props.placeholder ?? ' '}</span>
        ) : props.renderBackdrop ? (
          props.renderBackdrop(text)
        ) : (
          text
        )}
        {text.endsWith('\n') && ' '}
      </div>
      <textarea
        ref={ref}
        className={props.className + ' edit-overlay'}
        value={text}
        onChange={(e) => queueSave(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' && !props.multilineEnter) {
            e.preventDefault()
            window.clearTimeout(timer.current)
            props.onSave(latest.current)
            props.onEnter?.()
          } else if (e.key === 'Backspace' && latest.current === '' && props.onDeleteEmpty) {
            e.preventDefault()
            props.onDeleteEmpty()
          } else if (e.key === 'Escape') {
            ;(e.target as HTMLTextAreaElement).blur()
          }
        }}
        onBlur={() => {
          window.clearTimeout(timer.current)
          if (latest.current !== props.value) props.onSave(latest.current)
          props.onBlur?.()
        }}
      />
    </div>
  )
}

// Row renders one node: chevron, bullet, typed content, note — then its
// children, indented under a guide line, Workflowy-style. A mirror node
// (mirror_of set) renders its original's content and children live, wearing a
// diamond bullet; `trail` carries the mirror targets already open above so a
// mirror inside its own subtree cannot loop the render.
export function Row({
  node,
  depth,
  cb,
  trail,
}: {
  node: NodeData
  depth: number
  cb: RowCallbacks
  trail?: ReadonlySet<string>
}) {
  const mirrorTarget = node.mirror_of ? store.get(node.mirror_of) : undefined
  const isMirror = node.mirror_of !== ''
  const shown = mirrorTarget ?? node // the content-bearing node
  const looped = isMirror && (trail?.has(shown.uuid) ?? false)

  const ext = getExtension(shown.type)
  const children = looped
    ? []
    : store.children(shown.uuid).filter((c) => cb.showCompleted || c.completed_at === 0)
  const hasKids = children.length > 0
  const editing = cb.edit.editing === node.uuid
  const noteEditing = cb.edit.noteEditing === node.uuid
  const done = shown.completed_at > 0
  const locked = node.readonly || isMirror // mirrors reshape at the original

  const childTrail = isMirror ? new Set([...(trail ?? []), shown.uuid]) : trail

  const startEdit = () => {
    if (isMirror) {
      cb.onZoom(shown.uuid) // tap a mirror → go edit the original
      return
    }
    if (locked || ext?.inlineEditable === false) return
    store.markDirty(node.uuid)
    cb.edit.start(node.uuid)
  }

  const styled = styleCSS(shown.style)
  const nameClass = [
    'row-name',
    ext?.textClass ?? '',
    done ? 'done' : '',
    shown.type === 'agent' ? 'agent' : '',
    isMirror ? 'mirrored' : '',
  ]
    .filter(Boolean)
    .join(' ')

  const blockContent = ext?.render && !editing

  return (
    <div className={'row-tree' + (depth > 0 ? ' indented' : '')}>
      <div className={'row' + (editing ? ' editing' : '')} data-uuid={node.uuid}>
        <div
          className={'bullet' + (node.collapsed && hasKids ? ' halo' : '')}
          onClick={() => cb.onZoom(shown.uuid)}
        >
          {shown.type === 'agent' ? (
            <span className="dot-agent">✦</span>
          ) : isMirror ? (
            <span className="dot-mirror" />
          ) : (
            <span className="dot" />
          )}
        </div>
        <div className="row-body">
          {ext?.control === 'todo' && (
            <span
              className={'todo-box' + (done ? ' done' : '')}
              onClick={() => store.setCompleted(shown.uuid, !done)}
            >
              {done && (
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round">
                  <path d="m5 13 4.5 4.5L19 7" />
                </svg>
              )}
            </span>
          )}
          {blockContent ? (
            <div onClick={startEdit}>
              <HostMount ext={ext!} node={shown} />
            </div>
          ) : editing ? (
            <TextEdit
              value={node.name}
              className={nameClass}
              style={styled}
              multilineEnter={node.type === 'code' || node.type === 'json'}
              renderBackdrop={(t) => renderName(t)}
              onSave={(t) => void store.setName(node.uuid, t)}
              onEnter={() => cb.edit.enterAfter(node.uuid)}
              onDeleteEmpty={() => cb.edit.deleteEmpty(node.uuid)}
            />
          ) : (
            <div className={nameClass} style={styled} onClick={startEdit}>
              {shown.name === '' ? (
                <span className="placeholder"> </span>
              ) : (
                renderName(shown.name, cb.onTag)
              )}
              {shown.starred && <span className="star-mark"> ★</span>}
              {looped && <span className="mirror-loop"> ↩ mirror of an ancestor</span>}
            </div>
          )}
          {noteEditing ? (
            <TextEdit
              value={node.note}
              className="row-note"
              placeholder="Add a note…"
              multilineEnter
              onSave={(t) => void store.setNote(node.uuid, t)}
              onBlur={() => cb.edit.stop()}
            />
          ) : (
            shown.note !== '' && (
              <div
                className="row-note"
                onClick={() => {
                  if (isMirror) return
                  store.markDirty(node.uuid)
                  cb.edit.startNote(node.uuid)
                }}
              >
                {shown.note}
              </div>
            )
          )}
        </div>
        {hasKids && (
          <div
            className="chevron-right"
            onClick={() => store.setCollapsed(node.uuid, !node.collapsed)}
          >
            {node.collapsed ? '▸' : '▾'}
          </div>
        )}
      </div>
      {hasKids && !node.collapsed && (
        <div className="children">
          {children.map((c) => (
            <Row key={c.uuid} node={c} depth={depth + 1} cb={cb} trail={childTrail} />
          ))}
        </div>
      )}
    </div>
  )
}
