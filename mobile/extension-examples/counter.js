// Example custom note extension: a tap counter. Paste this into
// Settings → Custom note extensions in the mobile app (or load it from a
// file) and "Counter" appears in the type picker as a first-class node type.
//
// The contract: default-export {type, label, glyph, render(host)}. The
// renderer owns host.el (plain DOM, no framework); host.update persists
// fields through the server, so the count survives restarts and syncs live
// to every other client — the node's name IS the stored count.
export default {
  type: 'counter',
  label: 'Counter',
  glyph: '#',
  inlineEditable: false,
  render(host) {
    const count = parseInt(host.node.name, 10) || 0

    host.el.textContent = ''
    const wrap = document.createElement('div')
    wrap.style.cssText = 'display:flex;align-items:center;gap:12px;padding:4px 0'

    const btn = (label, delta) => {
      const b = document.createElement('button')
      b.textContent = label
      b.style.cssText =
        'background:#34393c;border:1px solid #3a4145;border-radius:8px;' +
        'color:#d9dcde;font:inherit;padding:4px 14px;cursor:pointer'
      b.onclick = () => host.update({ name: String(count + delta) })
      return b
    }

    const value = document.createElement('span')
    value.textContent = String(count)
    value.style.cssText = 'font-size:20px;font-weight:700;min-width:3ch;text-align:center'

    wrap.append(btn('−', -1), value, btn('+', +1))
    host.el.appendChild(wrap)
  },
}
