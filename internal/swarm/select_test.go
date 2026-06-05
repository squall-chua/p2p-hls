package swarm

import (
	"testing"
	"time"

	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/stretchr/testify/require"
)

func TestSelectSourcePicksLowestRTTHaver(t *testing.T) {
	clk := &vclock{t: time.Unix(1_700_000_000, 0)}
	s := newAt(clk)
	s.SetPeers([]identity.NodeID{"slow", "fast", "none"})
	s.OnPeerHave("slow", 0, bitmapOf(7), 1, clk.Now())
	s.OnPeerHave("fast", 0, bitmapOf(7), 1, clk.Now())
	s.OnRTT("slow", 200*time.Millisecond)
	s.OnRTT("fast", 20*time.Millisecond)

	got, ok := s.SelectSource(7, clk.Now())
	require.True(t, ok)
	require.Equal(t, identity.NodeID("fast"), got)
}

func TestSelectSourceSkipsBusyAndDemoted(t *testing.T) {
	clk := &vclock{t: time.Unix(1_700_000_000, 0)}
	s := newAt(clk)
	s.SetPeers([]identity.NodeID{"a", "b"})
	s.OnPeerHave("a", 0, bitmapOf(7), 1, clk.Now())
	s.OnPeerHave("b", 0, bitmapOf(7), 1, clk.Now())
	s.OnRTT("a", 10*time.Millisecond)
	s.OnRTT("b", 50*time.Millisecond)

	s.MarkBusy("a", clk.Now()) // a is fastest but busy
	got, ok := s.SelectSource(7, clk.Now())
	require.True(t, ok)
	require.Equal(t, identity.NodeID("b"), got)

	s.Demote("b")
	_, ok = s.SelectSource(7, clk.Now())
	require.False(t, ok) // no eligible peer -> caller falls back to Host
}
