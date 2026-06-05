package swarm

import (
	"math/rand"
	"testing"
	"time"

	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/stretchr/testify/require"
)

type vclock struct{ t time.Time }

func (c *vclock) Now() time.Time          { return c.t }
func (c *vclock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newAt(clk Clock) *Swarm {
	return New("self", clk, DefaultConfig(), rand.New(rand.NewSource(1)))
}

func TestPeerHaveMergeRespectsEpoch(t *testing.T) {
	clk := &vclock{t: time.Unix(1_700_000_000, 0)}
	s := newAt(clk)
	s.SetPeers([]identity.NodeID{"p1"})

	s.OnPeerHave("p1", 0, bitmapOf(2, 4), 5, clk.Now())
	require.True(t, s.peerHas("p1", 2))
	require.True(t, s.peerHas("p1", 4))

	// Stale epoch ignored.
	s.OnPeerHave("p1", 0, bitmapOf(9), 5, clk.Now())
	require.False(t, s.peerHas("p1", 9))

	// Higher epoch replaces.
	s.OnPeerHave("p1", 0, bitmapOf(9), 6, clk.Now())
	require.True(t, s.peerHas("p1", 9))
	require.False(t, s.peerHas("p1", 2))
}

func TestExpireStaleDropsSilentPeers(t *testing.T) {
	clk := &vclock{t: time.Unix(1_700_000_000, 0)}
	s := newAt(clk)
	s.SetPeers([]identity.NodeID{"p1"})
	s.OnPeerHave("p1", 0, bitmapOf(1), 1, clk.Now())
	clk.advance(DefaultConfig().HaveTTL + time.Second)
	s.ExpireStale(clk.Now())
	require.False(t, s.peerHas("p1", 1))
}

func bitmapOf(idxs ...int) []byte {
	max := 0
	for _, i := range idxs {
		if i > max {
			max = i
		}
	}
	b := make([]byte, max/8+1)
	for _, i := range idxs {
		b[i/8] |= 1 << uint(i%8)
	}
	return b
}
