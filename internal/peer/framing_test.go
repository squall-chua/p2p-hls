package peer

import (
	"testing"

	peerv1 "github.com/squall-chua/p2p-hls/proto/peer/v1"
	"github.com/stretchr/testify/require"
)

func TestEncodeDecodeEnvelope(t *testing.T) {
	env := &peerv1.Envelope{
		RequestId: 42,
		Body:      &peerv1.Envelope_Ping{Ping: &peerv1.Ping{Nonce: "xyz"}},
	}
	raw, err := encodeEnvelope(env)
	require.NoError(t, err)

	got, err := decodeEnvelope(raw)
	require.NoError(t, err)
	require.Equal(t, uint64(42), got.RequestId)
	require.Equal(t, "xyz", got.GetPing().GetNonce())
}

func TestRequestIDsAreMonotonic(t *testing.T) {
	var a requestIDs
	require.Equal(t, uint64(1), a.next())
	require.Equal(t, uint64(2), a.next())
	require.Equal(t, uint64(3), a.next())
}
