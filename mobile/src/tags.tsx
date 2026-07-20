import type { ReactNode } from 'react'

// #tags get a stable color from a small palette hashed off the tag text —
// matching the colored tags in the sidebar/outline look. The literal #word
// stays in the stored name (no markup invariant); color is render-time only.
const palette = ['#e06c75', '#98c379', '#e5c07b', '#61afef', '#c678dd', '#56b6c2']

export function tagColor(tag: string): string {
  let h = 0
  for (let i = 0; i < tag.length; i++) h = (h * 31 + tag.charCodeAt(i)) >>> 0
  return palette[h % palette.length]
}

const reTag = /(^|[\s(])#([\p{L}\p{N}_-]+)/gu

// renderName splits a node name into text and tappable tag spans.
export function renderName(name: string, onTag?: (tag: string) => void): ReactNode[] {
  const out: ReactNode[] = []
  let last = 0
  let m: RegExpExecArray | null
  reTag.lastIndex = 0
  while ((m = reTag.exec(name)) !== null) {
    const start = m.index + m[1].length
    if (start > last) out.push(name.slice(last, start))
    const tag = m[2]
    out.push(
      <span
        key={`${start}-${tag}`}
        className="tag"
        style={{ color: tagColor(tag) }}
        onClick={(e) => {
          if (!onTag) return
          e.stopPropagation()
          onTag('#' + tag)
        }}
      >
        #{tag}
      </span>,
    )
    last = start + tag.length + 1
  }
  if (last < name.length) out.push(name.slice(last))
  return out
}
