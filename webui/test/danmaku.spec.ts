import { describe, it, expect } from 'vitest'
import { LaneAllocator, pushBounded, splitEmojiRuns, LANES, LANE_GAP_MS } from '../app/lib/danmaku'

describe('LaneAllocator', () => {
  it('hands out each lane once, then reports full', () => {
    const la = new LaneAllocator()
    const seen = new Set<number>()
    for (let i = 0; i < LANES; i++) seen.add(la.allocate(0))
    expect(seen.size).toBe(LANES)
    expect(la.allocate(0)).toBe(-1) // all lanes busy at the same instant
  })

  it('frees a lane after the gap elapses', () => {
    const la = new LaneAllocator()
    for (let i = 0; i < LANES; i++) la.allocate(0)
    expect(la.allocate(LANE_GAP_MS)).toBeGreaterThanOrEqual(0)
  })
})

describe('pushBounded', () => {
  it('appends within the cap', () => {
    expect(pushBounded([1, 2], 3, 5)).toEqual([1, 2, 3])
  })
  it('drops the oldest past the cap', () => {
    expect(pushBounded([1, 2, 3], 4, 3)).toEqual([2, 3, 4])
  })
})

describe('splitEmojiRuns', () => {
  it('keeps plain text as a single non-emoji run', () => {
    expect(splitEmojiRuns('gg wp')).toEqual([{ text: 'gg wp', emoji: false }])
  })
  it('splits text from a trailing emoji', () => {
    expect(splitEmojiRuns('gg 🔥')).toEqual([
      { text: 'gg ', emoji: false },
      { text: '🔥', emoji: true },
    ])
  })
  it('splits an emoji embedded in text', () => {
    expect(splitEmojiRuns('a🔥b')).toEqual([
      { text: 'a', emoji: false },
      { text: '🔥', emoji: true },
      { text: 'b', emoji: false },
    ])
  })
  it('merges consecutive emoji into one run', () => {
    expect(splitEmojiRuns('🔥🔥🎉')).toEqual([{ text: '🔥🔥🎉', emoji: true }])
  })
  it('treats a flag (regional-indicator pair) as one emoji run', () => {
    expect(splitEmojiRuns('🇯🇵')).toEqual([{ text: '🇯🇵', emoji: true }])
  })
  it('treats a ZWJ family as one emoji run', () => {
    expect(splitEmojiRuns('👨‍👩‍👧‍👦!')).toEqual([
      { text: '👨‍👩‍👧‍👦', emoji: true },
      { text: '!', emoji: false },
    ])
  })
})
