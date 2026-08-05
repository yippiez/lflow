import { useEffect, useRef } from 'react'
import type { NodeData } from '../api'
import type { NodeTypeExtension } from '../extensions/types'
import { store } from '../store'

// HostMount is the bridge between React rows and the DOM-based extension SDK:
// it owns one div, hands it to the extension's render as an ExtensionHost, and
// re-renders on node change. Cleanup from the previous render runs first.
export function HostMount({ ext, node }: { ext: NodeTypeExtension; node: NodeData }) {
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const el = ref.current
    if (!el || !ext.render) return
    const cleanup = ext.render({
      el,
      node,
      update: async (fields) => {
        if (fields.name !== undefined) await store.setName(node.uuid, fields.name)
        if (fields.note !== undefined) await store.setNote(node.uuid, fields.note)
      },
      children: () => store.children(node.uuid),
    })
    return () => {
      if (typeof cleanup === 'function') cleanup()
    }
  }, [ext, node])

  return <div className="ext-host" ref={ref} />
}
