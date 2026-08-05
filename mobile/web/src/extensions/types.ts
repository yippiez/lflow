// The node-type extension SDK. Every node type the app renders — built-in or
// user-supplied — goes through one registry of NodeTypeExtension descriptors,
// mirroring the TUI's registry.go. Custom extensions are plain ES modules
// (framework-agnostic: they get a DOM element, not React), which is what will
// let a future "custom note" carry its own JavaScript and render on the spot.
import type { NodeData } from '../api'

// ExtensionHost is what a custom renderer receives: its DOM slot, the node,
// and a small API back into the outline. It never touches React or the store
// directly, so extensions stay sandboxable and framework-independent.
export interface ExtensionHost {
  // the element the extension owns; render into it freely
  el: HTMLElement
  // the node being rendered (frozen snapshot; re-render delivers a fresh one)
  node: NodeData
  // update fields on the node (name, note) — persisted through the server
  update(fields: { name?: string; note?: string }): Promise<void>
  // children of the node, for extensions that render their own subtree
  children(): NodeData[]
}

export interface NodeTypeExtension {
  // the nodes.type string this extension renders
  type: string
  // short label for the type picker
  label: string
  // one-character glyph shown in pickers and badges (unicode, no emoji)
  glyph: string
  // offered in the type picker? (internal types like `agent` are not)
  pickable?: boolean
  // CSS class applied to the inline name text (headings, quote, done-dim …)
  textClass?: string
  // extra row control: 'todo' draws the completion circle before the name
  control?: 'todo'
  // when set, the row's content area is this renderer's DOM instead of the
  // inline name text; re-invoked whenever the node changes, the returned
  // cleanup runs first. Tapping still opens the inline editor unless
  // inlineEditable is false.
  render?(host: ExtensionHost): void | (() => void)
  // false = the extension owns all interaction; tapping never opens the
  // plain-text editor (default true)
  inlineEditable?: boolean
}

// A custom-extension module (pasted source or fetched URL) must export a
// NodeTypeExtension as its default export, or call the `register` it is given:
//   export default { type: 'counter', label: 'Counter', glyph: '#', render(host) {…} }
export type ExtensionModule = { default?: NodeTypeExtension }
