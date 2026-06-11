import { describe, it, expect } from 'vitest'
import { planViewerActuation, hostMessageFor, formatDrift, formatTime, parsePartyMessage } from '../app/lib/actuator'

describe('planViewerActuation', () => {
  it('hard-seeks when action.seek', () => {
    const p = planViewerActuation({ play: true, seek: true, seekMs: 42000, rate: 1, driftMs: 1500 })
    expect(p).toEqual({ seekTo: 42, rate: 1, play: true })
  })
  it('rate-nudges without seeking', () => {
    const p = planViewerActuation({ play: true, seek: false, seekMs: 0, rate: 1.05, driftMs: -200 })
    expect(p).toEqual({ seekTo: null, rate: 1.05, play: true })
  })
  it('pauses when action.play is false', () => {
    const p = planViewerActuation({ play: false, seek: false, seekMs: 0, rate: 1, driftMs: 0 })
    expect(p.play).toBe(false)
  })
})

describe('hostMessageFor', () => {
  it('maps a play event', () => {
    expect(hostMessageFor('play', { currentTime: 12.4, paused: false })).toEqual({ type: 'play', posMs: 12400, playing: true })
  })
  it('maps a timeupdate to a report', () => {
    expect(hostMessageFor('timeupdate', { currentTime: 30, paused: false })).toEqual({ type: 'report', posMs: 30000, playing: true })
  })
})

describe('formatDrift', () => {
  it('renders signed seconds', () => {
    expect(formatDrift(200)).toBe('+0.2s')
    expect(formatDrift(-1500)).toBe('-1.5s')
    expect(formatDrift(0)).toBe('±0.0s')
  })
})

describe('formatTime', () => {
  it('renders m:ss under an hour', () => {
    expect(formatTime(0)).toBe('0:00')
    expect(formatTime(5)).toBe('0:05')
    expect(formatTime(75)).toBe('1:15')
  })
  it('renders h:mm:ss at or over an hour, zero-padding minutes', () => {
    expect(formatTime(6300)).toBe('1:45:00')
    expect(formatTime(3661)).toBe('1:01:01')
  })
  it('clamps non-finite or negative input to 0:00', () => {
    expect(formatTime(NaN)).toBe('0:00')
    expect(formatTime(Infinity)).toBe('0:00')
    expect(formatTime(-3)).toBe('0:00')
  })
})

describe('parsePartyMessage', () => {
  it('parses a danmaku push', () => {
    const m = parsePartyMessage(JSON.stringify({ type: 'danmaku', text: 'hi', sender: 'alice' }))
    expect(m).toEqual({ kind: 'danmaku', danmaku: { text: 'hi', sender: 'alice' } })
  })
  it('parses a viewer action (no type field)', () => {
    const m = parsePartyMessage(JSON.stringify({ play: true, seek: false, seekMs: 0, rate: 1, driftMs: 12 }))
    expect(m?.kind).toBe('action')
    if (m?.kind === 'action') expect(m.action.driftMs).toBe(12)
  })
  it('returns null on malformed JSON', () => {
    expect(parsePartyMessage('not json')).toBeNull()
  })
})
