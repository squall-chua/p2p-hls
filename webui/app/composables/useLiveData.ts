import { useBridge } from './useBridge'
import { refetchFor } from '../lib/store'

// useLiveData opens the single SSE stream and invokes onRefetch(kind) per event.
export function useLiveData(onRefetch: (kind: string) => void) {
  const bridge = useBridge()
  let es: EventSource | null = null
  function start() {
    es = new EventSource(bridge.eventsURL())
    es.onmessage = (m) => {
      try {
        const ev = JSON.parse(m.data)
        for (const kind of refetchFor(ev.type)) onRefetch(kind)
      } catch { /* ignore malformed */ }
    }
  }
  function stop() { es?.close(); es = null }
  return { start, stop }
}
