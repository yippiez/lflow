import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import type { NodeData } from '../api'
import { getExtension } from '../extensions/registry'
import { store } from '../store'
import { renderName } from '../tags'
import { HostMount } from './HostMount'

export interface RowCallbacks {
  onZoom(uuid: string): void
  onTag(tag: string): void
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

function autosize(el: HTMLTextAreaElement | null) {
  if (!el) return
  el.style.height = '0'
  el.style.height = el.scrollHeight + 'px'
}

// TextEdit is the inline editor for a node's name (or note): a debounced
// auto-flushing textarea — there is no unsaved state, matching the TUI's
// livesync (~1s flush after typing pauses).
function TextEdit(props: {
  value: string
  className: string
  placeholder?: string
  multilineEnter?: boolean // true for notes/code: Enter inserts a newline
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

  useLayoutEffect(() => {
    autosize(ref.current)
  }, [text])

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
    <textarea
      ref={ref}
      className={props.className}
      value={text}
      rows={1}
      placeholder={props.placeholder}
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
  )
}

// Row renders one node: chevron, bullet, typed content, note — then its
// children, indented under a guide line, Workflowy-style.
export function Row({ node, depth, cb }: { node: NodeData; depth: number; cb: RowCallbacks }) {
  const ext = getExtension(node.type)
  const children = store.children(node.uuid)
  const hasKids = children.length > 0
  const editing = cb.edit.editing === node.uuid
  const noteEditing = cb.edit.noteEditing === node.uuid
  const done = node.completed_at > 0
  const locked = node.readonly

  const startEdit = () => {
    if (locked || ext?.inlineEditable === false) return
    store.markDirty(node.uuid)
    cb.edit.start(node.uuid)
  }

  const nameClass = ['row-name', ext?.textClass ?? '', done ? 'done' : '', node.type === 'agent' ? 'agent' : '']
    .filter(Boolean)
    .join(' ')

  const blockContent = ext?.render && !editing

  return (
    <div className={'row-tree' + (depth > 0 ? ' indented' : '')}>
      <div className={'row' + (editing ? ' editing' : '')} data-uuid={node.uuid}>
        <div
          className={'chevron' + (hasKids ? '' : ' hidden')}
          onClick={() => store.setCollapsed(node.uuid, !node.collapsed)}
        >
          {node.collapsed ? '▸' : '▾'}
        </div>
        <div
          className={'bullet' + (node.collapsed && hasKids ? ' halo' : '')}
          onClick={() => cb.onZoom(node.uuid)}
        >
          <span className="dot">{node.type === 'agent' ? '✦' : '●'}</span>
        </div>
        <div className="row-body">
          {ext?.control === 'todo' && (
            <span
              className={'todo-box' + (done ? ' done' : '')}
              onClick={() => store.setCompleted(node.uuid, !done)}
            >
              {done ? '●' : '○'}
            </span>
          )}
          {blockContent ? (
            <div onClick={startEdit}>
              <HostMount ext={ext!} node={node} />
            </div>
          ) : editing ? (
            <TextEdit
              value={node.name}
              className={nameClass + ' edit'}
              multilineEnter={node.type === 'code' || node.type === 'json'}
              onSave={(t) => void store.setName(node.uuid, t)}
              onEnter={() => cb.edit.enterAfter(node.uuid)}
              onDeleteEmpty={() => cb.edit.deleteEmpty(node.uuid)}
            />
          ) : (
            <div className={nameClass} onClick={startEdit}>
              {node.name === '' ? (
                <span className="placeholder"> </span>
              ) : (
                renderName(node.name, cb.onTag)
              )}
              {node.starred && <span className="star-mark"> ◆</span>}
            </div>
          )}
          {noteEditing ? (
            <TextEdit
              value={node.note}
              className="row-note edit"
              placeholder="Add a note…"
              multilineEnter
              onSave={(t) => void store.setNote(node.uuid, t)}
              onBlur={() => cb.edit.stop()}
            />
          ) : (
            node.note !== '' && (
              <div
                className="row-note"
                onClick={() => {
                  store.markDirty(node.uuid)
                  cb.edit.startNote(node.uuid)
                }}
              >
                {node.note}
              </div>
            )
          )}
        </div>
      </div>
      {hasKids && !node.collapsed && (
        <div className="children">
          {children.map((c) => (
            <Row key={c.uuid} node={c} depth={depth + 1} cb={cb} />
          ))}
        </div>
      )}
    </div>
  )
}
