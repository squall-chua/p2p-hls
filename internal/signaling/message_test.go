package signaling_test

import (
	"testing"

	"github.com/squall-chua/p2p-hls/internal/signaling"
	"github.com/stretchr/testify/require"
)

func TestEnvelopeRoundTrip(t *testing.T) {
	in := signaling.Register{
		NodeID:      "abc",
		PublicKey:   []byte{1, 2, 3},
		DisplayName: "alice",
		Signature:   []byte{9, 9},
	}
	raw, err := signaling.Marshal(in)
	require.NoError(t, err)

	typ, env, err := signaling.Unmarshal(raw)
	require.NoError(t, err)
	require.Equal(t, signaling.TypeRegister, typ)

	out, ok := env.(*signaling.Register)
	require.True(t, ok)
	require.Equal(t, in.NodeID, out.NodeID)
	require.Equal(t, in.PublicKey, out.PublicKey)
	require.Equal(t, in.DisplayName, out.DisplayName)
}

func TestUnmarshalUnknownTypeErrors(t *testing.T) {
	_, _, err := signaling.Unmarshal([]byte(`{"type":"bogus","data":{}}`))
	require.Error(t, err)
}
