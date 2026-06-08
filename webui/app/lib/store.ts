// refetchFor maps an SSE event type to which snapshot(s) the SPA should refetch.
// Events carry only a type (Slice 6a); the SPA pulls the authoritative snapshot.
export function refetchFor(type: string): string[] {
  switch (type) {
    case 'presence': return ['presence']
    case 'request': return ['requests']
    case 'audience': return ['audience']
    case 'party-ended': return ['audience']
    default: return []
  }
}
