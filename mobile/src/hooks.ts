import { useSyncExternalStore } from 'react'
import { store } from './store'

// useOutline re-renders the caller on every store change. Selectors read the
// store directly afterwards — the outline is small, coarse invalidation keeps
// the model simple (same trade the TUI makes re-rendering its whole view).
export function useOutline(): number {
  return useSyncExternalStore(
    (cb) => store.onChange(cb),
    () => store.version,
  )
}
