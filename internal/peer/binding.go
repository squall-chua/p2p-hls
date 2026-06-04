package peer

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"

	"github.com/pion/webrtc/v4"
	"github.com/squall-chua/p2p-hls/internal/identity"
)

// SignedSignal is a WebRTC SDP carried over the (untrusted) signaling server,
// signed by the sender's Ed25519 key. Signing the SDP binds the Node identity to
// the DTLS fingerprint inside it (ADR 0003).
type SignedSignal struct {
	From      string `json:"from"`       // sender NodeID
	PublicKey []byte `json:"public_key"` // sender Ed25519 public key
	SDP       []byte `json:"sdp"`        // json-encoded webrtc.SessionDescription
	Signature []byte `json:"signature"`  // Sign(SDP)
}

// SignSignal serializes and signs a session description.
func SignSignal(id *identity.Identity, desc webrtc.SessionDescription) (SignedSignal, error) {
	sdp, err := json.Marshal(desc)
	if err != nil {
		return SignedSignal{}, err
	}
	return SignedSignal{
		From:      string(id.NodeID()),
		PublicKey: id.PublicKey(),
		SDP:       sdp,
		Signature: id.Sign(sdp),
	}, nil
}

// VerifySignal checks the identity binding and returns the session description.
func VerifySignal(s SignedSignal) (webrtc.SessionDescription, error) {
	pub := ed25519.PublicKey(s.PublicKey)
	if len(pub) != ed25519.PublicKeySize {
		return webrtc.SessionDescription{}, fmt.Errorf("peer: bad public key length")
	}
	if identity.NodeIDFromPublicKey(pub) != identity.NodeID(s.From) {
		return webrtc.SessionDescription{}, fmt.Errorf("peer: From does not match public key fingerprint")
	}
	if !identity.Verify(pub, s.SDP, s.Signature) {
		return webrtc.SessionDescription{}, fmt.Errorf("peer: signature verification failed")
	}
	var desc webrtc.SessionDescription
	if err := json.Unmarshal(s.SDP, &desc); err != nil {
		return webrtc.SessionDescription{}, err
	}
	return desc, nil
}

// Encode serializes a SignedSignal to JSON for relay transport.
func (s SignedSignal) Encode() ([]byte, error) { return json.Marshal(s) }

// DecodeSignedSignal deserializes a SignedSignal from relay JSON.
func DecodeSignedSignal(raw []byte) (SignedSignal, error) {
	var s SignedSignal
	err := json.Unmarshal(raw, &s)
	return s, err
}
