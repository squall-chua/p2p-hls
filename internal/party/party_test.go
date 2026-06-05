package party_test

import (
	"testing"

	"github.com/squall-chua/p2p-hls/internal/party"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfigSane(t *testing.T) {
	c := party.DefaultConfig()
	require.Equal(t, int64(1000), c.SeekThresholdMS)
	require.Equal(t, int64(80), c.DeadbandMS)
	require.Less(t, c.MinRate, 1.0)
	require.Greater(t, c.MaxRate, 1.0)
	require.Positive(t, c.HeartbeatInterval)
}

func TestRealClockMonotonicish(t *testing.T) {
	c := party.RealClock()
	a := c.Now()
	require.False(t, a.IsZero())
}
