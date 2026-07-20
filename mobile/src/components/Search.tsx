import { useEffect, useRef, useState } from 'react'
import type { NodeData } from '../api'
import { api } from '../api'
import { IcX } from '../icons'
import { store } from '../store'
import { renderName } from '../tags'

// Search: the full-screen search overlay (bottom-bar magnifier). Results come
// from the server's SearchNodes — exact/prefix/substring + FTS, starred first.
export function Search(props: {
  open: boolean
  initial: string
  onClose(): void
  onZoom(uuid: string): void
}) {
  const [q, setQ] = useState(props.initial)
  const [hits, setHits] = useState<NodeData[]>([])
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (props.open) {
      setQ(props.initial)
      window.setTimeout(() => inputRef.current?.focus(), 50)
    }
  }, [props.open, props.initial])

  useEffect(() => {
    if (!props.open || q.trim() === '') {
      setHits([])
      return
    }
    const t = window.setTimeout(async () => {
      try {
        const { nodes } = await api.search(q, true)
        setHits(nodes.slice(0, 50))
      } catch {
        setHits([])
      }
    }, 200)
    return () => window.clearTimeout(t)
  }, [q, props.open])

  if (!props.open) return null

  return (
    <div className="search-overlay">
      <div className="search-top">
        <input
          ref={inputRef}
          className="jump"
          placeholder="Search the outline …"
          value={q}
          onChange={(e) => setQ(e.target.value)}
        />
        <button className="icon-btn" onClick={props.onClose}>
          <IcX size={22} />
        </button>
      </div>
      <div className="search-hits">
        {hits.map((n) => {
          const path = store
            .ancestors(n.uuid)
            .filter((a) => a.uuid !== 'root')
            .map((a) => a.name || 'untitled')
            .join(' › ')
          return (
            <div
              key={n.uuid}
              className="side-item"
              onClick={() => {
                props.onZoom(n.uuid)
                props.onClose()
              }}
            >
              <span className="side-glyph">{n.starred ? '◆' : '•'}</span>
              <span className="side-name">
                {renderName(n.name || 'untitled')}
                {path && <span className="side-path"> — {path}</span>}
              </span>
            </div>
          )
        })}
        {q.trim() !== '' && hits.length === 0 && <div className="side-empty">no matches</div>}
      </div>
    </div>
  )
}
