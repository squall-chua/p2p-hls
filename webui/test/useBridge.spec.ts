import { describe, it, expect, vi } from 'vitest'
import { readBootstrap, resolveIdentity, useBridge } from '../app/composables/useBridge'

describe('readBootstrap', () => {
  it('prefers window.__P2P__', () => {
    const b = readBootstrap({ __P2P__: { token: 't1', nodeId: 'n1', name: 'A' } } as any, '?token=zzz')
    expect(b.token).toBe('t1')
    expect(b.nodeId).toBe('n1')
  })
  it('falls back to ?token= in dev', () => {
    const b = readBootstrap({} as any, '?token=devtok')
    expect(b.token).toBe('devtok')
  })
})

describe('resolveIdentity', () => {
  it('uses the injected bootstrap identity when nodeId present (prod), without fetching', async () => {
    const fetchSelf = vi.fn()
    const id = await resolveIdentity({ nodeId: 'n1', name: 'Alice' }, fetchSelf)
    expect(id).toEqual({ nodeId: 'n1', displayName: 'Alice' })
    expect(fetchSelf).not.toHaveBeenCalled()
  })
  it('falls back to fetchSelf (GET /api/self) when nodeId is empty (dev)', async () => {
    const fetchSelf = vi.fn(async () => ({ nodeId: 'n2', displayName: 'Bob' }))
    const id = await resolveIdentity({ nodeId: '', name: '' }, fetchSelf)
    expect(id).toEqual({ nodeId: 'n2', displayName: 'Bob' })
    expect(fetchSelf).toHaveBeenCalledOnce()
  })
})

describe('useBridge resolveSelf', () => {
  it('memoizes the dev /api/self fetch so repeated calls hit the network once', async () => {
    const fetch = vi.fn(async () => new Response(
      JSON.stringify({ nodeId: 'nX', displayName: 'X' }),
      { status: 200, headers: { 'content-type': 'application/json' } },
    ))
    vi.stubGlobal('fetch', fetch)
    const b = useBridge()
    const a1 = await b.resolveSelf()
    const a2 = await b.resolveSelf()
    expect(a1).toEqual({ nodeId: 'nX', displayName: 'X' })
    expect(a2).toEqual({ nodeId: 'nX', displayName: 'X' })
    expect(fetch).toHaveBeenCalledOnce()
    expect((fetch.mock.calls[0]![0] as string)).toBe('/api/self')
    vi.unstubAllGlobals()
  })
})

describe('useBridge api() body handling', () => {
  it('resolves bodiless 200 responses to undefined (join/leave/end/approve)', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('', { status: 200 })))
    await expect(useBridge().joinParty('h', 'c')).resolves.toBeUndefined()
    await expect(useBridge().approve('n')).resolves.toBeUndefined()
    await expect(useBridge().leaveParty()).resolves.toBeUndefined()
    vi.unstubAllGlobals()
  })
  it('resolves bodiless 202 (request-access) to undefined', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('', { status: 202 })))
    await expect(useBridge().requestAccess('n', 'hi')).resolves.toBeUndefined()
    vi.unstubAllGlobals()
  })
  it('parses JSON bodies (startParty)', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ partyId: 'p1' }), { status: 200, headers: { 'content-type': 'application/json' } })))
    await expect(useBridge().startParty('c')).resolves.toEqual({ partyId: 'p1' })
    vi.unstubAllGlobals()
  })
  it('throws an error carrying .status on non-ok', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('denied', { status: 403 })))
    await expect(useBridge().catalog('n')).rejects.toMatchObject({ status: 403 })
    vi.unstubAllGlobals()
  })
})
