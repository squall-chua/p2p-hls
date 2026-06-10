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
