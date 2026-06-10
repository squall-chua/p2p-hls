package party

import (
	"sync"
	"time"

	"github.com/squall-chua/p2p-hls/internal/identity"
)

// Host is the authoritative sync engine. It reads the local player's events via
// On* and exposes the current authoritative state via Snapshot. It is safe for
// concurrent use (player input, heartbeat ticks, and Audience changes race).
type Host struct {
	clock Clock
	cfg   Config

	mu        sync.Mutex
	partyID   string
	contentID string

	playing   bool
	posMS     int64
	sampledAt time.Time
	rate      float64
	seq       uint64

	// seek debounce
	scrubbing     bool
	pendingSeekMS int64
	lastSeekInput time.Time

	audience map[identity.NodeID]string
}

// NewHost constructs a Host engine for a party on contentID.
func NewHost(clock Clock, cfg Config, partyID, contentID string) *Host {
	return &Host{
		clock:     clock,
		cfg:       cfg,
		partyID:   partyID,
		contentID: contentID,
		rate:      1.0,
		sampledAt: clock.Now(),
		audience:  map[identity.NodeID]string{},
	}
}

func (h *Host) PartyID() string   { return h.partyID }
func (h *Host) ContentID() string { return h.contentID }

// OnPlay/OnPause are immediate state changes (bump seq now).
func (h *Host) OnPlay(posMS int64, now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.playing = true
	h.posMS = posMS
	h.sampledAt = now
	h.scrubbing = false
	h.seq++
}

func (h *Host) OnPause(posMS int64, now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.playing = false
	h.posMS = posMS
	h.sampledAt = now
	h.scrubbing = false
	h.seq++
}

// OnReport is a periodic position report while playing; it keeps the
// interpolation anchor fresh but does NOT bump seq (no observable change).
func (h *Host) OnReport(posMS int64, now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.scrubbing {
		return
	}
	h.posMS = posMS
	h.sampledAt = now
}

// OnSeek records a (possibly mid-scrub) seek. It does not commit immediately;
// Tick commits the final position once the scrub settles.
func (h *Host) OnSeek(posMS int64, now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.scrubbing = true
	h.pendingSeekMS = posMS
	h.lastSeekInput = now
}

// Tick commits a settled seek. It returns the new Snapshot and true iff a seek
// was committed this tick. Call it from the heartbeat loop.
func (h *Host) Tick(now time.Time) (State, bool) {
	h.mu.Lock()
	committed := false
	if h.scrubbing && now.Sub(h.lastSeekInput) >= h.cfg.SeekDebounce {
		h.posMS = h.pendingSeekMS
		h.sampledAt = now
		h.scrubbing = false
		h.seq++
		committed = true
	}
	h.mu.Unlock()
	return h.Snapshot(now), committed
}

// Snapshot returns the current authoritative state with interpolated position.
func (h *Host) Snapshot(now time.Time) State {
	h.mu.Lock()
	defer h.mu.Unlock()
	pos := h.posMS
	playing := h.playing
	if h.scrubbing {
		// Hold viewers during a scrub: report paused at the latest scrub target.
		return State{PartyID: h.partyID, Playing: false, PositionMS: h.pendingSeekMS, Rate: h.rate, Seq: h.seq}
	}
	if playing {
		pos += now.Sub(h.sampledAt).Milliseconds()
	}
	return State{PartyID: h.partyID, Playing: playing, PositionMS: pos, Rate: h.rate, Seq: h.seq}
}

// Join adds a Viewer to the Audience (idempotent).
func (h *Host) Join(node identity.NodeID, name string) {
	h.mu.Lock()
	h.audience[node] = name
	h.mu.Unlock()
}

// Leave removes a Viewer from the Audience.
func (h *Host) Leave(node identity.NodeID) {
	h.mu.Lock()
	delete(h.audience, node)
	h.mu.Unlock()
}

// Members returns the current Audience.
func (h *Host) Members() []Member {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Member, 0, len(h.audience))
	for n, name := range h.audience {
		out = append(out, Member{NodeID: n, DisplayName: name})
	}
	return out
}

// Member returns a Viewer's stored display name and whether it is in the Audience.
func (h *Host) Member(node identity.NodeID) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	name, ok := h.audience[node]
	return name, ok
}

// ViewerCount is the current Audience size.
func (h *Host) ViewerCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.audience)
}
