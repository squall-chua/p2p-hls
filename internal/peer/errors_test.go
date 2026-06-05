package peer

import (
	"testing"

	peerv1 "github.com/squall-chua/p2p-hls/proto/peer/v1"
	"github.com/stretchr/testify/require"
)

func TestBusyRoundTrips(t *testing.T) {
	env := errEnvelope(7, ErrBusy)
	require.Equal(t, peerv1.Error_BUSY, env.GetError().GetStatus())
	require.ErrorIs(t, statusErr(env.GetError()), ErrBusy)
}
