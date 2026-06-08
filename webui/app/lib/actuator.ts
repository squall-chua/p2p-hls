export interface ViewerAction { play: boolean; seek: boolean; seekMs: number; rate: number; driftMs: number }
export interface Actuation { seekTo: number | null; rate: number; play: boolean }

// planViewerActuation turns a server Action into concrete player operations.
export function planViewerActuation(a: ViewerAction): Actuation {
  return { seekTo: a.seek ? a.seekMs / 1000 : null, rate: a.rate || 1, play: a.play }
}

export type HostEvent = 'play' | 'pause' | 'seek' | 'timeupdate'
export interface PlayerSnapshot { currentTime: number; paused: boolean }
export interface HostMessage { type: 'play' | 'pause' | 'seek' | 'report'; posMs: number; playing: boolean }

// hostMessageFor maps a <video> event into the player WS up-message.
export function hostMessageFor(ev: HostEvent, p: PlayerSnapshot): HostMessage {
  const type = ev === 'timeupdate' ? 'report' : ev
  return { type, posMs: Math.round(p.currentTime * 1000), playing: !p.paused }
}

// formatDrift renders a signed-seconds drift indicator, e.g. "+0.2s".
export function formatDrift(driftMs: number): string {
  const s = driftMs / 1000
  const sign = driftMs > 0 ? '+' : driftMs < 0 ? '-' : '±'
  return `${sign}${Math.abs(s).toFixed(1)}s`
}
