import { describe, it, expect } from 'vitest'
import { readBootstrap } from '../app/composables/useBridge'

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
