// Pure helpers for the Danmaku overlay: lane allocation and a bounded queue.
// No DOM, no Vue — unit-tested in isolation.

export const MAX_DANMAKU_LEN = 100
export const LANES = 10
export const QUEUE_MAX = 30
export const TRAVEL_MS = 7000 // keep in sync with the CSS animation in DanmakuOverlay.vue
export const LANE_GAP_MS = 1500 // min spacing before a lane accepts the next item

// LaneAllocator hands out a free vertical lane for a new Danmaku, or -1 when every
// lane is still busy. `now` is wall-clock ms (e.g. performance.now()).
export class LaneAllocator {
  private freeAt: number[]
  constructor(private lanes = LANES, private gapMs = LANE_GAP_MS) {
    this.freeAt = new Array(lanes).fill(0)
  }
  allocate(now: number): number {
    for (let i = 0; i < this.lanes; i++) {
      if ((this.freeAt[i] ?? 0) <= now) {
        this.freeAt[i] = now + this.gapMs
        return i
      }
    }
    return -1
  }
}

// pushBounded appends item, dropping oldest entries so the result is at most `max`.
export function pushBounded<T>(queue: T[], item: T, max = QUEUE_MAX): T[] {
  const next = queue.length >= max ? queue.slice(queue.length - max + 1) : queue.slice()
  next.push(item)
  return next
}

export interface TextRun { text: string; emoji: boolean }

// Matches a maximal run of emoji: flag pairs (two regional indicators), or an
// Extended_Pictographic base plus its VS16 selectors (\uFE0F), skin-tone modifiers,
// and ZWJ-joined (\u200D) parts (so a family/profession sequence stays whole).
const EMOJI_RUN = /(?:\p{Regional_Indicator}{2}|\p{Extended_Pictographic}(?:\uFE0F|\p{Emoji_Modifier}|\u200D\p{Extended_Pictographic})*)+/gu

// splitEmojiRuns breaks text into consecutive emoji / non-emoji runs so the overlay
// can render emoji larger than the surrounding words. Pure; no DOM.
export function splitEmojiRuns(text: string): TextRun[] {
  const runs: TextRun[] = []
  let last = 0
  for (const m of text.matchAll(EMOJI_RUN)) {
    const start = m.index ?? 0
    if (start > last) runs.push({ text: text.slice(last, start), emoji: false })
    runs.push({ text: m[0], emoji: true })
    last = start + m[0].length
  }
  if (last < text.length) runs.push({ text: text.slice(last), emoji: false })
  return runs
}
