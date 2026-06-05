package swarm

import (
	"time"

	"github.com/squall-chua/p2p-hls/internal/identity"
)

// OnRTT records the latest round-trip time to a peer (the ADR-0004 Ping/Pong).
func (s *Swarm) OnRTT(node identity.NodeID, rtt time.Duration) {
	p := s.peers[node]
	if p == nil {
		return
	}
	p.rtt = rtt
	p.haveRTT = true
}

// MarkBusy records that a peer rejected a pull with ErrBusy; it is skipped for
// BusyCooldown. The peer is honest (just loaded) so it is NOT demoted.
func (s *Swarm) MarkBusy(node identity.NodeID, now time.Time) {
	if p := s.peers[node]; p != nil {
		p.busyUntil = now.Add(s.cfg.BusyCooldown)
	}
}

// Demote drops a peer from selection for the rest of the party (have-map lie or
// poison). It stays in the peer set but is never chosen as a source again.
func (s *Swarm) Demote(node identity.NodeID) {
	if p := s.peers[node]; p != nil {
		p.demoted = true
		p.have = map[int]bool{}
	}
}

// SelectSource returns the lowest-RTT eligible peer that advertises Segment idx, or
// (_, false) if none — in which case the caller pulls from the Host. Eligible =
// not demoted, not in busy cooldown, advertises idx. Peers without an RTT sample
// rank after those with one.
func (s *Swarm) SelectSource(idx int, now time.Time) (identity.NodeID, bool) {
	var best identity.NodeID
	var bestRTT time.Duration
	found := false
	for id, p := range s.peers {
		if p.demoted || !p.have[idx] {
			continue
		}
		if now.Before(p.busyUntil) {
			continue
		}
		rtt := p.rtt
		if !p.haveRTT {
			rtt = time.Hour // deprioritize unmeasured peers
		}
		if !found || rtt < bestRTT {
			found = true
			best = id
			bestRTT = rtt
		}
	}
	return best, found
}
