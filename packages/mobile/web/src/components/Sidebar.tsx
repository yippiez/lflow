import { useEffect, useState } from 'react'
import type { NodeData } from '../api'
import { api } from '../api'
import { IcArrowLeft, IcPlus } from '../icons'
import { ROOT, store } from '../store'
import { renderName } from '../tags'

// TreeItem: one row of the sidebar's outline browser — expand chevron + name,
// expansion is sidebar-local state (it never touches the outline's collapsed).
function TreeItem(props: {
  node: NodeData
  depth: number
  expanded: Set<string>
  toggle(uuid: string): void
  onJump(uuid: string): void
}) {
  const { node } = props
  const kids = store.children(node.uuid)
  const open = props.expanded.has(node.uuid)
  return (
    <>
      <div className="side-item" style={{ paddingLeft: 4 + props.depth * 18 }}>
        <span
          className={'side-chev' + (kids.length ? '' : ' hidden')}
          onClick={() => props.toggle(node.uuid)}
        >
          {open ? '▾' : '▸'}
        </span>
        <span
          className={'side-name' + (node.type === 'agent' ? ' agent' : '')}
          onClick={() => props.onJump(node.uuid)}
        >
          {renderName(node.name || 'untitled')}
        </span>
      </div>
      {open &&
        kids.map((c) => (
          <TreeItem
            key={c.uuid}
            node={c}
            depth={props.depth + 1}
            expanded={props.expanded}
            toggle={props.toggle}
            onJump={props.onJump}
          />
        ))}
    </>
  )
}

// Sidebar: the slide-over panel — Jump to… live search, Starred, then the
// outline tree, with a New node pill pinned at the bottom.
export function Sidebar(props: {
  open: boolean
  onClose(): void
  onZoom(uuid: string): void
  onNewNode(): void
}) {
  const [q, setQ] = useState('')
  const [hits, setHits] = useState<NodeData[]>([])
  const [expanded, setExpanded] = useState<Set<string>>(new Set())

  useEffect(() => {
    if (!props.open) return
    if (q.trim() === '') {
      setHits([])
      return
    }
    const t = window.setTimeout(async () => {
      try {
        const { nodes } = await api.search(q)
        setHits(nodes.slice(0, 20))
      } catch {
        setHits([])
      }
    }, 200)
    return () => window.clearTimeout(t)
  }, [q, props.open])

  const jump = (uuid: string) => {
    props.onZoom(uuid)
    props.onClose()
    setQ('')
  }

  const toggle = (uuid: string) =>
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(uuid)) next.delete(uuid)
      else next.add(uuid)
      return next
    })

  const starred = store.starred()

  return (
    <>
      <div className={'scrim' + (props.open ? ' show' : '')} onClick={props.onClose} />
      <div className={'sidebar' + (props.open ? ' open' : '')}>
        <div className="side-top">
          <input
            className="jump"
            placeholder="Jump to …"
            value={q}
            onChange={(e) => setQ(e.target.value)}
          />
          <button className="icon-btn side-back" onClick={props.onClose}>
            <IcArrowLeft size={24} />
          </button>
        </div>
        <div className="side-scroll">
          {q.trim() !== '' ? (
            <div className="side-section">
              <div className="side-head">Results</div>
              {hits.length === 0 ? (
                <div className="side-empty">no matches</div>
              ) : (
                hits.map((n) => (
                  <div key={n.uuid} className="side-item" onClick={() => jump(n.uuid)}>
                    <span className="side-chev">{n.starred ? '★' : '•'}</span>
                    <span className="side-name">{renderName(n.name || 'untitled')}</span>
                  </div>
                ))
              )}
            </div>
          ) : (
            <>
              {starred.length > 0 && (
                <div className="side-section">
                  <div className="side-head">▾ Starred</div>
                  {starred.map((n) => (
                    <TreeItem
                      key={'star-' + n.uuid}
                      node={n}
                      depth={0}
                      expanded={expanded}
                      toggle={toggle}
                      onJump={jump}
                    />
                  ))}
                </div>
              )}
              <div className="side-section">
                <div className="side-head" onClick={() => jump(ROOT)}>
                  ▾ Home
                </div>
                {store.children(ROOT).map((n) => (
                  <TreeItem
                    key={n.uuid}
                    node={n}
                    depth={0}
                    expanded={expanded}
                    toggle={toggle}
                    onJump={jump}
                  />
                ))}
              </div>
            </>
          )}
        </div>
        <div className="side-bottom">
          <button
            className="new-node-pill"
            onClick={() => {
              props.onClose()
              props.onNewNode()
            }}
          >
            <IcPlus size={19} /> New node
          </button>
        </div>
      </div>
    </>
  )
}
