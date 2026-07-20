// REST + SSE client for the lflow daemon's HTTP API (`lflow serve --http`).
// The browser build talks to its own origin; the Capacitor (Android) build
// talks to the server URL saved in Settings.

export interface NodeData {
  rowid: number
  uuid: string
  parent_uuid: string
  rank: number
  name: string
  note: string
  type: string
  style: string
  mirror_of: string
  completed_at: number
  added_on: number
  edited_on: number
  deleted: boolean
  collapsed: boolean
  readonly: boolean
  lock?: number
  starred: boolean
  priority: string
}

export interface ChangeEvent {
  seq: number
  instance?: string
  name?: string
  nodes?: NodeData[]
  aux?: boolean
}

// One id per app session: the daemon stamps our writes with it, so our own
// SSE echoes are recognizable (echo suppression, same trick as the TUI).
export const instanceId = Math.random().toString(36).slice(2, 14)

const SERVER_KEY = 'lflow.server'
const TOKEN_KEY = 'lflow.token'

export function serverBase(): string {
  return localStorage.getItem(SERVER_KEY) ?? ''
}

export function setServer(url: string, token: string) {
  if (url) localStorage.setItem(SERVER_KEY, url.replace(/\/+$/, ''))
  else localStorage.removeItem(SERVER_KEY)
  if (token) localStorage.setItem(TOKEN_KEY, token)
  else localStorage.removeItem(TOKEN_KEY)
}

function headers(): Record<string, string> {
  const h: Record<string, string> = {
    'Content-Type': 'application/json',
    'X-Lflow-Instance': instanceId,
  }
  const token = localStorage.getItem(TOKEN_KEY)
  if (token) h['Authorization'] = `Bearer ${token}`
  return h
}

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(serverBase() + path, {
    method,
    headers: headers(),
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (!res.ok) {
    let msg = `${res.status}`
    try {
      msg = (await res.json()).error ?? msg
    } catch {
      /* keep status */
    }
    throw new Error(msg)
  }
  return res.json()
}

export const api = {
  info: () => call<{ version: string; nodes: number; root: string }>('GET', '/api/info'),

  outline: () => call<{ root: string; nodes: NodeData[] }>('GET', '/api/outline'),

  create: (req: {
    parent_uuid: string
    name?: string
    note?: string
    type?: string
    after?: string
    position?: 'top' | 'bottom' | ''
  }) => call<NodeData>('POST', '/api/nodes', req),

  patch: (
    uuid: string,
    req: Partial<{
      name: string
      note: string
      type: string
      style: string
      collapsed: boolean
      starred: boolean
      completed: boolean
      priority: string
    }>,
  ) => call<NodeData>('PATCH', `/api/nodes/${uuid}`, req),

  move: (uuid: string, req: { parent_uuid?: string; after?: string; position?: string }) =>
    call<NodeData>('POST', `/api/nodes/${uuid}/move`, req),

  remove: (uuid: string) => call<{ deleted: number }>('DELETE', `/api/nodes/${uuid}`),

  search: (q: string, completed = false) =>
    call<{ nodes: NodeData[] }>(
      'GET',
      `/api/search?q=${encodeURIComponent(q)}${completed ? '&completed=1' : ''}`,
    ),
}

// subscribe opens the SSE feed. EventSource cannot set headers, so an auth
// token rides the query string. Reconnection is native; onReconnect lets the
// store refetch the outline after a gap (nothing is silently missed).
export function subscribe(
  onEvent: (ev: ChangeEvent) => void,
  onStatus: (live: boolean) => void,
): () => void {
  let es: EventSource | null = null
  let closed = false
  let everOpened = false

  const open = () => {
    if (closed) return
    const token = localStorage.getItem(TOKEN_KEY)
    const url = serverBase() + '/api/events' + (token ? `?token=${encodeURIComponent(token)}` : '')
    es = new EventSource(url)
    es.onopen = () => {
      onStatus(true)
      everOpened = true
    }
    es.onmessage = (m) => {
      try {
        onEvent(JSON.parse(m.data))
      } catch {
        /* skip malformed frame */
      }
    }
    es.onerror = () => {
      onStatus(false)
      // EventSource retries by itself while the object lives; if it died
      // (readyState CLOSED), reopen after a beat.
      if (es && es.readyState === EventSource.CLOSED) {
        es.close()
        setTimeout(open, everOpened ? 1000 : 3000)
      }
    }
  }
  open()
  return () => {
    closed = true
    es?.close()
  }
}
