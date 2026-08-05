// One registry drives everything type-specific: the row renderer asks it how
// to draw a node, the type picker lists it. Built-ins register at import;
// custom extensions (JS source pasted in Settings, persisted locally) load at
// startup and register the same way — add a type here, no schema change,
// exactly like the TUI's registry.
import type { NodeTypeExtension } from './types'

const registry = new Map<string, NodeTypeExtension>()

export function registerExtension(ext: NodeTypeExtension) {
  registry.set(ext.type, ext)
  notify()
}

export function getExtension(type: string): NodeTypeExtension | undefined {
  return registry.get(type)
}

export function pickableTypes(): NodeTypeExtension[] {
  return [...registry.values()].filter((e) => e.pickable !== false)
}

// --- registry change feed (the type picker re-renders when a custom
// extension lands) ---
const listeners = new Set<() => void>()
function notify() {
  for (const l of listeners) l()
}
export function onRegistryChange(l: () => void): () => void {
  listeners.add(l)
  return () => listeners.delete(l)
}

// --- custom extensions: ES-module source stored locally, loaded via a blob
// URL so it is a real module (imports allowed, strict mode, own scope) ---

const CUSTOM_KEY = 'lflow.extensions'

export interface CustomExtension {
  name: string
  source: string
  error?: string
}

export function customExtensions(): CustomExtension[] {
  try {
    return JSON.parse(localStorage.getItem(CUSTOM_KEY) ?? '[]')
  } catch {
    return []
  }
}

function saveCustom(list: CustomExtension[]) {
  localStorage.setItem(CUSTOM_KEY, JSON.stringify(list))
}

export async function loadExtensionSource(source: string): Promise<NodeTypeExtension> {
  const url = URL.createObjectURL(new Blob([source], { type: 'text/javascript' }))
  try {
    const mod = await import(/* @vite-ignore */ url)
    const ext = mod.default as NodeTypeExtension | undefined
    if (!ext || typeof ext.type !== 'string' || typeof ext.render !== 'function') {
      throw new Error('extension must default-export {type, label, glyph, render}')
    }
    registerExtension(ext)
    return ext
  } finally {
    URL.revokeObjectURL(url)
  }
}

export async function addCustomExtension(name: string, source: string): Promise<void> {
  await loadExtensionSource(source) // throws on a bad module; nothing saved
  const list = customExtensions().filter((c) => c.name !== name)
  list.push({ name, source })
  saveCustom(list)
}

export function removeCustomExtension(name: string) {
  saveCustom(customExtensions().filter((c) => c.name !== name))
  // the registry entry stays until reload; removal is rare and reload is cheap
}

// loadCustomExtensions runs at startup: every saved module registers itself.
// A module that fails to load is kept but marked, never fatal to the app.
export async function loadCustomExtensions() {
  const list = customExtensions()
  let changed = false
  for (const c of list) {
    try {
      await loadExtensionSource(c.source)
      if (c.error) {
        delete c.error
        changed = true
      }
    } catch (e) {
      c.error = e instanceof Error ? e.message : String(e)
      changed = true
    }
  }
  if (changed) saveCustom(list)
}
