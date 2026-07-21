import type { CSSProperties, ReactNode } from 'react'

// One small palette serves both faces of color in the app: #tags get a
// tinted BACKGROUND pill (stable hash off the tag text), and whole nodes can
// be colored via the style column's color:<name> token. The literal #word
// stays in the stored name (no markup invariant); color is render-time only.
export const NODE_COLORS: Record<string, string> = {
  red: '#e06c75',
  yellow: '#e5c07b',
  green: '#98c379',
  blue: '#61afef',
  purple: '#c678dd',
  cyan: '#56b6c2',
}

const palette = Object.values(NODE_COLORS)

export function tagColor(tag: string): string {
  let h = 0
  for (let i = 0; i < tag.length; i++) h = (h * 31 + tag.charCodeAt(i)) >>> 0
  return palette[h % palette.length]
}

// tagStyle: tinted background, near-white text — the pill look.
export function tagStyle(tag: string): CSSProperties {
  return { backgroundColor: tagColor(tag) + '46' } // ~27% alpha tint
}

// nodeColor resolves the color:<name> token of a style string to a hex color.
export function nodeColor(style: string): string | undefined {
  for (const token of style.split(',')) {
    if (token.startsWith('color:')) return NODE_COLORS[token.slice(6)]
  }
  return undefined
}

const reTag = /(^|[\s(])#([\p{L}\p{N}_-]+)/gu

// renderName splits a node name into text and tappable tag pills.
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
        style={tagStyle(tag)}
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
