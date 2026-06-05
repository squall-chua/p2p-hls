package swarm

import (
	"math/rand"
	"testing"
	"time"

	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/stretchr/testify/require"
)

func TestGossipTargetsAreDeterministicForSeed(t *testing.T) {
	clk := &vclock{t: time.Unix(1_700_000_000, 0)}
	mk := func() *Swarm {
		s := New("self", clk, DefaultConfig(), rand.New(rand.NewSource(42)))
		s.SetPeers([]identity.NodeID{"a", "b", "c", "d", "e"})
		for _, id := range []identity.NodeID{"a", "b", "c", "d", "e"} {
			s.OnRTT(id, 10*time.Millisecond)
		}
		return s
	}
	require.Equal(t, mk().GossipTargets(), mk().GossipTargets()) // same seed => same picks
}

func TestGossipTargetsNeverExceedFanoutEvenIfRandomLinksLarger(t *testing.T) {
	clk := &vclock{t: time.Unix(1_700_000_000, 0)}
	cfg := DefaultConfig()
	cfg.Fanout = 2
	cfg.RandomLinks = 5 // misconfigured: more random links than fanout
	s := New("self", clk, cfg, rand.New(rand.NewSource(1)))
	s.SetPeers([]identity.NodeID{"a", "b", "c", "d", "e"})
	require.LessOrEqual(t, len(s.GossipTargets()), 2)
}

func TestGossipTargetsRespectFanoutAndExcludeSelf(t *testing.T) {
	clk := &vclock{t: time.Unix(1_700_000_000, 0)}
	s := New("self", clk, DefaultConfig(), rand.New(rand.NewSource(7)))
	s.SetPeers([]identity.NodeID{"a", "b", "c", "d", "e"})
	tg := s.GossipTargets()
	require.LessOrEqual(t, len(tg), DefaultConfig().Fanout)
	require.NotContains(t, tg, identity.NodeID("self"))
	seen := map[identity.NodeID]bool{}
	for _, id := range tg {
		require.False(t, seen[id], "no duplicate targets")
		seen[id] = true
	}
}
