import { describe, it, expect } from 'vitest'
import { expandShortcodes, matchShortcodes, activeShortcodeToken, EMOJI_SHORTCODES } from '../app/lib/emoji'

describe('expandShortcodes', () => {
  it('replaces a known shortcode', () => {
    expect(expandShortcodes(':fire:')).toBe('🔥')
  })
  it('replaces within surrounding text', () => {
    expect(expandShortcodes('gg :fire: nice')).toBe('gg 🔥 nice')
  })
  it('replaces multiple, including adjacent', () => {
    expect(expandShortcodes(':fire::tada:')).toBe('🔥🎉')
  })
  it('is case-insensitive', () => {
    expect(expandShortcodes(':FIRE:')).toBe('🔥')
  })
  it('supports +1 / -1 aliases', () => {
    expect(expandShortcodes(':+1: :-1:')).toBe('👍 👎')
  })
  it('leaves unknown shortcodes untouched', () => {
    expect(expandShortcodes(':notacode:')).toBe(':notacode:')
  })
  it('leaves an incomplete shortcode alone', () => {
    expect(expandShortcodes(':fire')).toBe(':fire')
  })
  it('agrees with the map', () => {
    expect(expandShortcodes(':heart:')).toBe(EMOJI_SHORTCODES.heart)
  })
})

describe('matchShortcodes', () => {
  it('prefix-matches a name and returns its emoji', () => {
    expect(matchShortcodes('fi')).toEqual([{ name: 'fire', emoji: '🔥' }])
  })
  it('ranks alphabetically', () => {
    expect(matchShortcodes('th').map(r => r.name)).toEqual(['thinking', 'thumbsdown', 'thumbsup'])
  })
  it('is case-insensitive', () => {
    expect(matchShortcodes('FI')).toEqual([{ name: 'fire', emoji: '🔥' }])
  })
  it('returns nothing for an empty query', () => {
    expect(matchShortcodes('')).toEqual([])
  })
  it('returns nothing for an unknown prefix', () => {
    expect(matchShortcodes('zzz')).toEqual([])
  })
  it('caps the result count to the limit', () => {
    expect(matchShortcodes('s', 2)).toHaveLength(2)
  })
})

describe('activeShortcodeToken', () => {
  it('detects a partial token at the caret', () => {
    expect(activeShortcodeToken(':fir', 4)).toEqual({ query: 'fir', start: 0 })
  })
  it('detects a token mid-string', () => {
    expect(activeShortcodeToken('gg :fi', 6)).toEqual({ query: 'fi', start: 3 })
  })
  it('reports an empty query once the closing colon is typed', () => {
    expect(activeShortcodeToken(':fire:', 6)?.query).toBe('')
  })
  it('returns null in plain text with no opening colon', () => {
    expect(activeShortcodeToken('hello', 5)).toBeNull()
  })
  it('returns null when whitespace breaks the run', () => {
    expect(activeShortcodeToken(':a b', 4)).toBeNull()
  })
})
