package peer

import (
	"testing"

	"github.com/pion/webrtc/v4"
	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/stretchr/testify/require"
)

func TestSignVerifySignalRoundTrip(t *testing.T) {
	id, _ := identity.Generate()
	desc := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: "v=0\r\na=fingerprint:sha-256 AA:BB\r\n"}

	s, err := SignSignal(id, desc)
	require.NoError(t, err)
	require.Equal(t, string(id.NodeID()), s.From)

	gotDesc, err := VerifySignal(s)
	require.NoError(t, err)
	require.Equal(t, desc.SDP, gotDesc.SDP)
	require.Equal(t, desc.Type, gotDesc.Type)
}

func TestVerifyRejectsTamperedSDP(t *testing.T) {
	id, _ := identity.Generate()
	desc := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: "original"}
	s, _ := SignSignal(id, desc)
	s.SDP = []byte(`{"type":"offer","sdp":"tampered"}`)
	_, err := VerifySignal(s)
	require.Error(t, err)
}

func TestVerifyRejectsMismatchedNodeID(t *testing.T) {
	id, _ := identity.Generate()
	desc := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: "x"}
	s, _ := SignSignal(id, desc)
	s.From = "not-the-real-fingerprint"
	_, err := VerifySignal(s)
	require.Error(t, err)
}
