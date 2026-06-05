package swarm

import (
	"sort"
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

// GossipTargets picks up to Fanout peers to push a have-map to this round: the
// (Fanout-RandomLinks) lowest-RTT peers plus RandomLinks uniformly-random others.
// The random links preserve epidemic mixing so proximity bias cannot cluster the
// mesh. Uses the injected RNG, so a seeded RNG makes selection deterministic.
func (s *Swarm) GossipTargets() []identity.NodeID {
	peers := s.Peers() // excludes demoted; excludes self (not in peers)
	if len(peers) <= s.cfg.Fanout {
		return peers
	}
	// Stable order for determinism before RTT sort / shuffle.
	sort.Slice(peers, func(i, j int) bool { return peers[i] < peers[j] })

	near := s.cfg.Fanout - s.cfg.RandomLinks
	if near < 0 {
		near = 0
	}
	byRTT := make([]identity.NodeID, len(peers))
	copy(byRTT, peers)
	sort.SliceStable(byRTT, func(i, j int) bool {
		return s.rttOf(byRTT[i]) < s.rttOf(byRTT[j])
	})

	chosen := map[identity.NodeID]bool{}
	out := []identity.NodeID{}
	for i := 0; i < near && i < len(byRTT); i++ {
		chosen[byRTT[i]] = true
		out = append(out, byRTT[i])
	}
	// Random links from the remaining peers.
	var pool []identity.NodeID
	for _, id := range peers {
		if !chosen[id] {
			pool = append(pool, id)
		}
	}
	for i := 0; i < s.cfg.RandomLinks && len(out) < s.cfg.Fanout && len(pool) > 0; i++ {
		k := s.rng.Intn(len(pool))
		out = append(out, pool[k])
		pool = append(pool[:k], pool[k+1:]...)
	}
	return out
}

func (s *Swarm) rttOf(id identity.NodeID) time.Duration {
	if p := s.peers[id]; p != nil && p.haveRTT {
		return p.rtt
	}
	return time.Hour
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
		if !found || rtt < bestRTT || (rtt == bestRTT && id < best) {
			found = true
			best = id
			bestRTT = rtt
		}
	}
	return best, found
}
