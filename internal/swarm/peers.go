package swarm

import (
	"time"

	"github.com/squall-chua/p2p-hls/internal/identity"
)

// SetPeers reconciles the active peer set with the party Audience (minus self).
// New peers get an empty record; departed peers are dropped.
func (s *Swarm) SetPeers(members []identity.NodeID) {
	want := map[identity.NodeID]bool{}
	for _, m := range members {
		if m == s.self {
			continue
		}
		want[m] = true
		if _, ok := s.peers[m]; !ok {
			s.peers[m] = &peerInfo{have: map[int]bool{}}
		}
	}
	for id := range s.peers {
		if !want[id] {
			delete(s.peers, id)
		}
	}
}

// Peers returns the current active peer NodeIDs (excluding demoted peers).
func (s *Swarm) Peers() []identity.NodeID {
	out := make([]identity.NodeID, 0, len(s.peers))
	for id, p := range s.peers {
		if !p.demoted {
			out = append(out, id)
		}
	}
	return out
}

// OnPeerHave merges a received have-map, ignoring epochs <= the last seen for that
// peer. A higher epoch fully replaces the peer's set (have-maps are snapshots).
func (s *Swarm) OnPeerHave(node identity.NodeID, base uint32, bitmap []byte, epoch uint64, now time.Time) {
	p := s.peers[node]
	if p == nil {
		p = &peerInfo{have: map[int]bool{}}
		s.peers[node] = p
	}
	p.lastSeen = now
	if p.have != nil && epoch <= p.epoch && p.epoch != 0 {
		return
	}
	p.epoch = epoch
	p.have = map[int]bool{}
	for _, idx := range decodeBitmap(base, bitmap) {
		p.have[idx] = true
	}
}

func (s *Swarm) peerHas(node identity.NodeID, idx int) bool {
	p := s.peers[node]
	return p != nil && p.have[idx]
}

// ExpireStale drops have-maps from peers unheard-from beyond HaveTTL (keeps the peer
// record so it can re-gossip; just clears its haves).
func (s *Swarm) ExpireStale(now time.Time) {
	for _, p := range s.peers {
		if !p.lastSeen.IsZero() && now.Sub(p.lastSeen) > s.cfg.HaveTTL {
			p.have = map[int]bool{}
			p.epoch = 0
		}
	}
}
