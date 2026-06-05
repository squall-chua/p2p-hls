package peer

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRelayEnvelopeRoundTrip(t *testing.T) {
	raw, err := EncodeRelay(RelayKindSwarmDial, SwarmDial{PartyID: "p1", From: "nodeA"})
	require.NoError(t, err)
	kind, data, err := DecodeRelay(raw)
	require.NoError(t, err)
	require.Equal(t, RelayKindSwarmDial, kind)
	d, err := DecodeSwarmDial(data)
	require.NoError(t, err)
	require.Equal(t, "p1", d.PartyID)
	require.Equal(t, "nodeA", d.From)
}
