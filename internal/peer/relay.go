package peer

import "encoding/json"

// RelayKind discriminates the opaque payloads carried inside a signaling Relay.
type RelayKind string

const (
	RelayKindSignal    RelayKind = "signal" // a SignedSignal (SDP/ICE)
	RelayKindSwarmDial RelayKind = "dial"   // a SwarmDial "dial-me" nudge
)

// RelayEnvelope wraps a relay payload with its kind.
type RelayEnvelope struct {
	Kind RelayKind       `json:"kind"`
	Data json.RawMessage `json:"data"`
}

// SwarmDial nudges a lower-NodeID peer to dial the sender, resolving the mesh glare
// hazard (only the lower NodeID dials).
type SwarmDial struct {
	PartyID string `json:"party_id"`
	From    string `json:"from"`
}

// EncodeRelay wraps v as a tagged relay payload.
func EncodeRelay(kind RelayKind, v any) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.Marshal(RelayEnvelope{Kind: kind, Data: data})
}

// DecodeRelay unwraps a tagged relay payload.
func DecodeRelay(raw []byte) (RelayKind, json.RawMessage, error) {
	var e RelayEnvelope
	if err := json.Unmarshal(raw, &e); err != nil {
		return "", nil, err
	}
	return e.Kind, e.Data, nil
}

// DecodeSwarmDial decodes a RelayKindSwarmDial payload.
func DecodeSwarmDial(data json.RawMessage) (SwarmDial, error) {
	var d SwarmDial
	err := json.Unmarshal(data, &d)
	return d, err
}
