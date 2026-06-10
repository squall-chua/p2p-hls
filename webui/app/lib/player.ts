import Hls from 'hls.js'
import { hostMessageFor, planViewerActuation, parsePartyMessage } from './actuator'

export type Role = 'solo' | 'host' | 'viewer'

// attachPlayer wires hls.js to <video> and, for host/viewer, the /party WS.
// onDrift is called with each viewer driftMs for the indicator.
export function attachPlayer(opts: {
  video: HTMLVideoElement
  src: string
  role: Role
  wsURL: string
  onDrift?: (driftMs: number) => void
  onDanmaku?: (d: { text: string; sender?: string }) => void
}) {
  let hls: Hls | null = null
  if (Hls.isSupported()) {
    hls = new Hls()
    hls.loadSource(opts.src)
    hls.attachMedia(opts.video)
  } else {
    // Safari / native HLS
    opts.video.src = opts.src
  }
  const destroyHls = () => { hls?.destroy(); hls = null }
  if (opts.role === 'solo') return { close: destroyHls, sendDanmaku: (_text: string) => {} }

  const ws = new WebSocket(opts.wsURL)
  ws.onopen = () => ws.send(JSON.stringify({ type: 'hello', role: opts.role }))
  const sendDanmaku = (text: string) => {
    if (ws.readyState === ws.OPEN) ws.send(JSON.stringify({ type: 'danmaku', text }))
  }

  if (opts.role === 'host') {
    const send = (ev: 'play' | 'pause' | 'seek' | 'timeupdate') =>
      ws.readyState === ws.OPEN && ws.send(JSON.stringify(hostMessageFor(ev, opts.video)))
    opts.video.addEventListener('play', () => send('play'))
    opts.video.addEventListener('pause', () => send('pause'))
    opts.video.addEventListener('seeked', () => send('seek'))
    opts.video.addEventListener('timeupdate', () => send('timeupdate'))
    ws.onmessage = (m) => {
      const msg = parsePartyMessage(m.data)
      if (msg?.kind === 'danmaku') opts.onDanmaku?.(msg.danmaku)
    }
  } else {
    // viewer: report position; apply server Actions
    const report = setInterval(() => {
      if (ws.readyState === ws.OPEN)
        ws.send(JSON.stringify({ type: 'report', posMs: Math.round(opts.video.currentTime * 1000), playing: !opts.video.paused }))
    }, 500)
    ws.onmessage = (m) => {
      const msg = parsePartyMessage(m.data)
      if (!msg) return
      if (msg.kind === 'danmaku') { opts.onDanmaku?.(msg.danmaku); return }
      const a = msg.action
      const plan = planViewerActuation(a)
      if (plan.seekTo !== null) opts.video.currentTime = plan.seekTo
      opts.video.playbackRate = plan.rate
      if (plan.play && opts.video.paused) opts.video.play().catch(() => {})
      if (!plan.play && !opts.video.paused) opts.video.pause()
      opts.onDrift?.(a.driftMs)
    }
    ws.addEventListener('close', () => clearInterval(report))
  }
  return { close: () => { ws.close(); destroyHls() }, sendDanmaku }
}
