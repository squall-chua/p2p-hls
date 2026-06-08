export interface Bootstrap { token: string; nodeId: string; name: string }

// readBootstrap resolves the session token from the injected global (prod) or the
// URL query (dev). Pure + injectable for tests.
export function readBootstrap(win: any, search: string): Bootstrap {
  const g = win?.__P2P__
  if (g?.token) return { token: g.token, nodeId: g.nodeId ?? '', name: g.name ?? '' }
  const t = new URLSearchParams(search).get('token') ?? ''
  return { token: t, nodeId: '', name: '' }
}

let cached: Bootstrap | null = null
function boot(): Bootstrap {
  if (cached) return cached
  cached = readBootstrap(typeof window !== 'undefined' ? window : {}, typeof location !== 'undefined' ? location.search : '')
  return cached
}

// useBridge returns the typed REST + SSE client, authenticated with the token.
export function useBridge() {
  const { token, nodeId, name } = boot()
  const headers = { Authorization: `Bearer ${token}` }
  const api = <T>(path: string, init?: RequestInit) =>
    fetch(path, { ...init, headers: { ...headers, ...(init?.headers || {}) } }).then(async (r) => {
      if (!r.ok) throw Object.assign(new Error(`api ${path} ${r.status}`), { status: r.status })
      const text = await r.text()
      return (text ? JSON.parse(text) : undefined) as T
    })
  return {
    token, nodeId, name,
    self: () => api<{ nodeId: string; displayName: string }>('/api/self'),
    presence: () => api<any[]>('/api/presence'),
    library: () => api<any[]>('/api/library'),
    catalog: (id: string) => api<any[]>(`/api/peers/${id}/catalog`),
    requestAccess: (id: string, message: string) =>
      api<void>(`/api/peers/${id}/request-access`, { method: 'POST', body: JSON.stringify({ message }) }),
    requests: () => api<string[]>('/api/requests'),
    approve: (id: string) => api<void>(`/api/requests/${id}/approve`, { method: 'POST' }),
    startParty: (contentId: string) => api<{ partyId: string }>('/api/party/start', { method: 'POST', body: JSON.stringify({ contentId }) }),
    joinParty: (hostNodeId: string, contentId: string) => api<void>('/api/party/join', { method: 'POST', body: JSON.stringify({ hostNodeId, contentId }) }),
    leaveParty: () => api<void>('/api/party/leave', { method: 'POST' }),
    endParty: () => api<void>('/api/party/end', { method: 'POST' }),
    audience: () => api<any[]>('/api/party/audience'),
    streamURL: (host: string, contentId: string) => `/s/${token}/${host}/${contentId}/index.m3u8`,
    eventsURL: () => `/api/events?token=${encodeURIComponent(token)}`,
    partyWSURL: () => {
      const proto = location.protocol === 'https:' ? 'wss' : 'ws'
      return `${proto}://${location.host}/party/${token}`
    },
  }
}
