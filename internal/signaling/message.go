// Package signaling defines the JSON messages exchanged between a Node and the
// signaling server, plus a client to speak them.
package signaling

import (
	"encoding/json"
	"fmt"
)

// MsgType identifies a signaling message.
type MsgType string

const (
	TypeChallenge        MsgType = "challenge"
	TypeRegister         MsgType = "register"
	TypePresenceSnapshot MsgType = "presence_snapshot"
	TypePresenceJoin     MsgType = "presence_join"
	TypePresenceLeave    MsgType = "presence_leave"
	TypeRelay            MsgType = "relay"
	TypeError            MsgType = "error"
)

// PeerInfo describes an online Node as seen via presence.
type PeerInfo struct {
	NodeID      string `json:"node_id"`
	PublicKey   []byte `json:"public_key"`
	DisplayName string `json:"display_name"`
}

// Challenge is sent by the server on connect; the client must sign Nonce.
type Challenge struct {
	Nonce []byte `json:"nonce"`
}

// Register is the client's reply: identity plus a signature over the challenge nonce.
type Register struct {
	NodeID      string `json:"node_id"`
	PublicKey   []byte `json:"public_key"`
	DisplayName string `json:"display_name"`
	Signature   []byte `json:"signature"` // Sign(nonce)
}

// PresenceSnapshot is the full set of other online Nodes, sent right after register.
type PresenceSnapshot struct {
	Peers []PeerInfo `json:"peers"`
}

// PresenceJoin / PresenceLeave are incremental presence updates.
type PresenceJoin struct {
	Peer PeerInfo `json:"peer"`
}
type PresenceLeave struct {
	NodeID string `json:"node_id"`
}

// Relay carries an opaque payload from one Node to another. The server sets From.
type Relay struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Payload []byte `json:"payload"`
}

// Error is a server-reported failure.
type Error struct {
	Message string `json:"message"`
}

type envelope struct {
	Type MsgType         `json:"type"`
	Data json.RawMessage `json:"data"`
}

// Marshal wraps a message value into a typed envelope.
func Marshal(msg any) ([]byte, error) {
	var typ MsgType
	switch msg.(type) {
	case Challenge, *Challenge:
		typ = TypeChallenge
	case Register, *Register:
		typ = TypeRegister
	case PresenceSnapshot, *PresenceSnapshot:
		typ = TypePresenceSnapshot
	case PresenceJoin, *PresenceJoin:
		typ = TypePresenceJoin
	case PresenceLeave, *PresenceLeave:
		typ = TypePresenceLeave
	case Relay, *Relay:
		typ = TypeRelay
	case Error, *Error:
		typ = TypeError
	default:
		return nil, fmt.Errorf("signaling: cannot marshal unknown message %T", msg)
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	return json.Marshal(envelope{Type: typ, Data: data})
}

// Unmarshal decodes an envelope into its concrete message (returned as a pointer).
func Unmarshal(raw []byte) (MsgType, any, error) {
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", nil, err
	}
	var dst any
	switch env.Type {
	case TypeChallenge:
		dst = &Challenge{}
	case TypeRegister:
		dst = &Register{}
	case TypePresenceSnapshot:
		dst = &PresenceSnapshot{}
	case TypePresenceJoin:
		dst = &PresenceJoin{}
	case TypePresenceLeave:
		dst = &PresenceLeave{}
	case TypeRelay:
		dst = &Relay{}
	case TypeError:
		dst = &Error{}
	default:
		return "", nil, fmt.Errorf("signaling: unknown message type %q", env.Type)
	}
	if err := json.Unmarshal(env.Data, dst); err != nil {
		return "", nil, err
	}
	return env.Type, dst, nil
}
