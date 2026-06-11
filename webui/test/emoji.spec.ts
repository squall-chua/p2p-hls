import { describe, it, expect } from 'vitest'
import { expandShortcodes, EMOJI_SHORTCODES } from '../app/lib/emoji'

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
