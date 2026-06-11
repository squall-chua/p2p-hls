// Live-party status for one Title on a peer's library, from the lightweight poll.
export interface LivePartyStatus { contentId: string; viewers: number }

// applyLiveParties returns titles with partyLive/partyViewers refreshed from the
// live-party list. Returns the SAME array reference when nothing changed, so the
// caller can skip a needless re-render.
export function applyLiveParties<T extends { contentId: string; partyLive: boolean; partyViewers: number }>(
  titles: T[],
  live: LivePartyStatus[],
): T[] {
  const viewersById = new Map(live.map(p => [p.contentId, p.viewers]))
  let changed = false
  const next = titles.map((t) => {
    const isLive = viewersById.has(t.contentId)
    const viewers = viewersById.get(t.contentId) ?? 0
    if (isLive === t.partyLive && viewers === t.partyViewers) return t
    changed = true
    return { ...t, partyLive: isLive, partyViewers: viewers }
  })
  return changed ? next : titles
}
