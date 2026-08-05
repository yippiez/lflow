// Built-in node types, one registry entry each — the mobile mirror of the
// TUI's registry.go nodeTypes slice. Text types style the inline name; block
// types (code, json) draw their own DOM through the same ExtensionHost API a
// custom extension gets, so there is exactly one rendering path.
import { registerExtension } from './registry'
import type { ExtensionHost } from './types'

function el(tag: string, cls: string, text?: string): HTMLElement {
  const e = document.createElement(tag)
  e.className = cls
  if (text !== undefined) e.textContent = text
  return e
}

// code: borderless gray block — dim line number, vertical rule to its right,
// then the code line (the TUI's codeBlockLines look, translated to CSS)
function renderCode(host: ExtensionHost) {
  host.el.textContent = ''
  const block = el('div', 'code-block')
  const lines = host.node.name === '' ? [''] : host.node.name.split('\n')
  for (let i = 0; i < lines.length; i++) {
    const row = el('div', 'code-line')
    row.appendChild(el('span', 'code-num', String(i + 1)))
    row.appendChild(el('span', 'code-text', lines[i]))
    block.appendChild(row)
  }
  host.el.appendChild(block)
}

function renderJSON(host: ExtensionHost) {
  host.el.textContent = ''
  const block = el('div', 'code-block json-block')
  let body = host.node.name
  try {
    body = JSON.stringify(JSON.parse(host.node.name), null, 2)
  } catch {
    /* show raw text until it parses */
  }
  const lines = body === '' ? [''] : body.split('\n')
  for (let i = 0; i < lines.length; i++) {
    const row = el('div', 'code-line')
    row.appendChild(el('span', 'code-num', String(i + 1)))
    row.appendChild(el('span', 'code-text', lines[i]))
    block.appendChild(row)
  }
  host.el.appendChild(block)
}

function renderDivider(host: ExtensionHost) {
  host.el.textContent = ''
  host.el.appendChild(el('div', 'divider-rule'))
}

export function registerBuiltins() {
  registerExtension({ type: 'bullets', label: 'Bullet', glyph: '•' })
  registerExtension({ type: 'todo', label: 'Todo', glyph: '○', control: 'todo' })
  registerExtension({ type: 'h1', label: 'Heading 1', glyph: 'H', textClass: 'h1' })
  registerExtension({ type: 'h2', label: 'Heading 2', glyph: 'H', textClass: 'h2' })
  registerExtension({ type: 'h3', label: 'Heading 3', glyph: 'H', textClass: 'h3' })
  registerExtension({ type: 'quote', label: 'Quote', glyph: '❝', textClass: 'quote' })
  registerExtension({ type: 'log', label: 'Log', glyph: '≡', textClass: 'log' })
  registerExtension({ type: 'code', label: 'Code', glyph: '{}', render: renderCode })
  registerExtension({ type: 'json', label: 'JSON', glyph: '{}', render: renderJSON })
  // divider is a legacy stored type (not in ValidTypes): render old rows, never offer it
  registerExtension({ type: 'divider', label: 'Divider', glyph: '—', render: renderDivider, inlineEditable: false, pickable: false })
  registerExtension({ type: 'query', label: 'Query', glyph: '?', textClass: 'query', pickable: false })
  // internal / terminal-bound types render read-only-ish but stay visible
  registerExtension({ type: 'agent', label: 'Agent reply', glyph: '✦', pickable: false, textClass: 'agent' })
  registerExtension({ type: 'voice', label: 'Voice', glyph: '♪', pickable: false })
  registerExtension({ type: 'image', label: 'Image', glyph: '▣', pickable: false })
  registerExtension({ type: 'wf', label: 'Workflowy', glyph: '◉', pickable: false })
  registerExtension({ type: 'nlpcompute', label: 'NLP compute', glyph: 'ƒ', pickable: false })
}
