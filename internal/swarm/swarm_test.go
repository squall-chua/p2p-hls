package swarm

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	c := DefaultConfig()
	require.Equal(t, 1*time.Second, c.GossipInterval)
	require.GreaterOrEqual(t, c.Fanout, 1)
	require.GreaterOrEqual(t, c.RandomLinks, 1)
}

func TestSegIndex(t *testing.T) {
	i, ok := SegIndex("seg00042.ts")
	require.True(t, ok)
	require.Equal(t, 42, i)
	_, ok = SegIndex("index.m3u8")
	require.False(t, ok)
}
