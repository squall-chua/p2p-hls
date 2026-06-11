import { describe, it, expect } from 'vitest'
import { applyLiveParties } from '../app/lib/liveParties'

const title = (contentId: string, partyLive = false, partyViewers = 0) => ({
  contentId, partyLive, partyViewers, displayTitle: contentId,
})

describe('applyLiveParties', () => {
  it('marks a title live with its viewer count', () => {
    const out = applyLiveParties([title('a'), title('b')], [{ contentId: 'b', viewers: 3 }])
    expect(out.find(t => t.contentId === 'b')).toMatchObject({ partyLive: true, partyViewers: 3 })
    expect(out.find(t => t.contentId === 'a')).toMatchObject({ partyLive: false, partyViewers: 0 })
  })
  it('clears a title that is no longer live', () => {
    const out = applyLiveParties([title('a', true, 2)], [])
    expect(out[0]).toMatchObject({ partyLive: false, partyViewers: 0 })
  })
  it('updates a changed viewer count', () => {
    const out = applyLiveParties([title('a', true, 2)], [{ contentId: 'a', viewers: 5 }])
    expect(out[0]!.partyViewers).toBe(5)
  })
  it('returns the same array reference when nothing changed', () => {
    const input = [title('a', true, 2), title('b')]
    const out = applyLiveParties(input, [{ contentId: 'a', viewers: 2 }])
    expect(out).toBe(input)
  })
})
