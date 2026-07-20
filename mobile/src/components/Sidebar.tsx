import { useEffect, useState } from 'react'
import type { NodeData } from '../api'
import { api } from '../api'
import { store } from '../store'
import { renderName } from '../tags'

// Sidebar: the slide-over panel — Jump to… live search, Starred, Recent.
export function Sidebar(props: {
  open: boolean
  onClose(): void
  onZoom(uuid: string): void
  onSettings(): void
}) {
  const [q, setQ] = useState('')
  const [hits, setHits] = useState<NodeData[]>([])

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

  const item = (n: NodeData) => (
    <div key={n.uuid} className="side-item" onClick={() => jump(n.uuid)}>
      <span className="side-glyph">{n.starred ? '◆' : '•'}</span>
      <span className="side-name">{renderName(n.name || 'untitled')}</span>
    </div>
  )

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
          <button className="icon-btn" onClick={props.onClose}>
            ←
          </button>
        </div>
        {q.trim() !== '' ? (
          <div className="side-section">
            <div className="side-head">Results</div>
            {hits.length === 0 ? <div className="side-empty">no matches</div> : hits.map(item)}
          </div>
        ) : (
          <>
            <div className="side-section">
              <div className="side-head">▾ Starred</div>
              {store.starred().length === 0 ? (
                <div className="side-empty">nothing starred yet</div>
              ) : (
                store.starred().map(item)
              )}
            </div>
            <div className="side-section">
              <div className="side-head">▾ Recent</div>
              {store.recent(15).map(item)}
            </div>
            <div className="side-section side-foot">
              <div className="side-item" onClick={props.onSettings}>
                <span className="side-glyph">⚙</span>
                <span className="side-name">Settings &amp; extensions</span>
              </div>
            </div>
          </>
        )}
      </div>
    </>
  )
}
