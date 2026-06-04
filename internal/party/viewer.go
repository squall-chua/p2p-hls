package party

import (
	"sync"
	"time"
)

// Viewer is a follower's sync engine. It is safe for concurrent use: OnState and
// OnRTT are called from the peer read path while Decide is called from the WS
// tick loop.
type Viewer struct {
	clock Clock
	cfg   Config

	mu        sync.Mutex
	haveState bool
	last      State
	recvAt    time.Time
	owdMS     int64
}

// NewViewer constructs a Viewer engine.
func NewViewer(clock Clock, cfg Config) *Viewer {
	return &Viewer{clock: clock, cfg: cfg}
}

// OnState ingests a received PartyState, ignoring stale (<= current) seq values.
func (v *Viewer) OnState(s State, now time.Time) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.haveState && s.Seq <= v.last.Seq {
		return
	}
	v.last = s
	v.recvAt = now
	v.haveState = true
}

// OnRTT updates the one-way-delay estimate (RTT/2, capped) from a round trip.
func (v *Viewer) OnRTT(rtt time.Duration) {
	owd := rtt.Milliseconds() / 2
	if owd > v.cfg.MaxOWDMS {
		owd = v.cfg.MaxOWDMS
	}
	if owd < 0 {
		owd = 0
	}
	v.mu.Lock()
	v.owdMS = owd
	v.mu.Unlock()
}

// Decide computes the actuator Action for the player's current position/state.
func (v *Viewer) Decide(playerPosMS int64, playerPlaying bool, now time.Time) Action {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.haveState {
		return Action{Play: playerPlaying, Rate: 1.0}
	}
	h := v.expectedHostPosLocked(now)
	drift := playerPosMS - h // + => viewer ahead
	abs := drift
	if abs < 0 {
		abs = -abs
	}
	switch {
	case abs > v.cfg.SeekThresholdMS:
		return Action{Play: v.last.Playing, Seek: true, SeekMS: h, Rate: 1.0}
	case abs > v.cfg.DeadbandMS:
		rate := clamp(1.0-v.cfg.Kp*(float64(drift)/1000.0), v.cfg.MinRate, v.cfg.MaxRate)
		return Action{Play: v.last.Playing, Rate: rate}
	default:
		return Action{Play: v.last.Playing, Rate: 1.0}
	}
}

// expectedHostPosLocked extrapolates the Host position at `now`. While paused the
// Host is not advancing, so owd is irrelevant and the position is exact (ADR 0004).
func (v *Viewer) expectedHostPosLocked(now time.Time) int64 {
	if !v.last.Playing {
		return v.last.PositionMS
	}
	return v.last.PositionMS + v.owdMS + now.Sub(v.recvAt).Milliseconds()
}
