package peer

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRelayEnvelopeRoundTrip(t *testing.T) {
	raw, err := EncodeRelay(RelayKindSignal, json.RawMessage(`{"sdp":"x"}`))
	require.NoError(t, err)
	kind, data, err := DecodeRelay(raw)
	require.NoError(t, err)
	require.Equal(t, RelayKindSignal, kind)
	require.JSONEq(t, `{"sdp":"x"}`, string(data))
}

// The dial nudge carries no payload: the receiver acts on the relay Kind plus
// the signaling-server-set sender, never a decoded body.
func TestRelayDialNudgeIsKindOnly(t *testing.T) {
	raw, err := EncodeRelay(RelayKindSwarmDial, nil)
	require.NoError(t, err)
	kind, _, err := DecodeRelay(raw)
	require.NoError(t, err)
	require.Equal(t, RelayKindSwarmDial, kind)
}
