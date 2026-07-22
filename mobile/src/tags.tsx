import type { CSSProperties, ReactNode } from 'react'

// One palette serves both faces of color in the app: #tags get a tinted
// BACKGROUND pill (stable hash off the tag text), and whole nodes carry the
// style column's tokens — the TUI's exact vocabulary (pkg/tui/style):
// bold/italic/underline/strike attributes, color:<name>, and the eight color
// names. Highlight rides a bg:<name> token (mobile-only; the TUI ignores
// tokens it does not know). The literal #word stays in the stored name
// (no markup invariant); all color is render-time only.
export const NODE_COLORS: Record<string, string> = {
  red: '#e06c75',
  orange: '#d19a66',
  yellow: '#e5c07b',
  green: '#98c379',
  cyan: '#56b6c2',
  blue: '#61afef',
  purple: '#c678dd',
  gray: '#8b9398',
}

export const STYLE_ATTRS = ['bold', 'italic', 'underline', 'strike'] as const

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

// nodeBg resolves the bg:<name> highlight token to a hex color.
export function nodeBg(style: string): string | undefined {
  for (const token of style.split(',')) {
    if (token.startsWith('bg:')) return NODE_COLORS[token.slice(3)]
  }
  return undefined
}

export function hasAttr(style: string, attr: string): boolean {
  return style.split(',').includes(attr)
}

// styleCSS turns a node's style tokens into inline CSS for its text.
export function styleCSS(style: string): CSSProperties | undefined {
  if (!style) return undefined
  const css: CSSProperties = {}
  const color = nodeColor(style)
  if (color) css.color = color
  const bg = nodeBg(style)
  if (bg) {
    css.backgroundColor = bg + '55'
    css.borderRadius = 4
  }
  if (hasAttr(style, 'bold')) css.fontWeight = 700
  if (hasAttr(style, 'italic')) css.fontStyle = 'italic'
  const deco: string[] = []
  if (hasAttr(style, 'underline')) deco.push('underline')
  if (hasAttr(style, 'strike')) deco.push('line-through')
  if (deco.length) css.textDecoration = deco.join(' ')
  return Object.keys(css).length ? css : undefined
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
