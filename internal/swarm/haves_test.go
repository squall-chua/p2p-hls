package swarm

import (
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"
)

func newTestSwarm() *Swarm {
	return New("self", RealClock(), DefaultConfig(), rand.New(rand.NewSource(1)))
}

func TestSetHaveAndHaveMsgRoundTrip(t *testing.T) {
	s := newTestSwarm()
	s.SetHave(3)
	s.SetHave(5)
	base, bitmap, epoch := s.HaveMsg()
	require.Equal(t, uint64(2), epoch) // bumped once per SetHave that changed state
	got := decodeBitmap(base, bitmap)
	require.ElementsMatch(t, []int{3, 5}, got)
}

func TestRetainEvictsOutsideWindow(t *testing.T) {
	s := newTestSwarm()
	for i := 0; i < 20; i++ {
		s.SetHave(i)
	}
	// Window around index 10 with lag/lead 6 keeps [4,16].
	s.Retain(4, 16)
	for i := 0; i < 20; i++ {
		require.Equal(t, i >= 4 && i <= 16, s.Have(i), "idx %d", i)
	}
}
