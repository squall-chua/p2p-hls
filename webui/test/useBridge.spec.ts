import { describe, it, expect, vi } from 'vitest'
import { readBootstrap, useBridge } from '../app/composables/useBridge'

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
