package peer

import (
	"context"
	"fmt"
	"sync"

	"github.com/pion/webrtc/v4"
	"github.com/squall-chua/p2p-hls/internal/identity"
	peerv1 "github.com/squall-chua/p2p-hls/proto/peer/v1"
)

// ProtocolVersion is the wire-protocol version exchanged at handshake.
const ProtocolVersion = 1

// Signaler ships a signed signal to a remote Node (over the signaling server).
type Signaler interface {
	SendSignal(to identity.NodeID, s SignedSignal) error
}

// Session is one identity-verified WebRTC connection to a remote Node.
type Session struct {
	self     *identity.Identity
	remote   identity.NodeID
	signaler Signaler

	pc *webrtc.PeerConnection

	ids     requestIDs
	mu      sync.Mutex
	control *webrtc.DataChannel
	pending map[uint64]chan *peerv1.Envelope

	readyOnce sync.Once
	ready     chan struct{}
}

// NewSession builds a Session and its underlying PeerConnection.
func NewSession(self *identity.Identity, remote identity.NodeID, cfg webrtc.Configuration, sig Signaler) (*Session, error) {
	pc, err := webrtc.NewPeerConnection(cfg)
	if err != nil {
		return nil, fmt.Errorf("new peer connection: %w", err)
	}
	s := &Session{
		self:     self,
		remote:   remote,
		signaler: sig,
		pc:       pc,
		pending:  make(map[uint64]chan *peerv1.Envelope),
		ready:    make(chan struct{}),
	}
	// Answerer receives channels created by the initiator.
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		if dc.Label() == "control" {
			s.bindControl(dc)
		}
	})
	return s, nil
}

// Start drives the offer/answer exchange. initiator=true creates channels + offer.
func (s *Session) Start(ctx context.Context, initiator bool) error {
	if !initiator {
		return nil // answerer acts on the incoming offer in HandleSignal
	}
	control, err := s.pc.CreateDataChannel("control", nil)
	if err != nil {
		return err
	}
	s.bindControl(control)
	if _, err := s.pc.CreateDataChannel("bulk", nil); err != nil { // opened now, used in Slice 3
		return err
	}
	offer, err := s.pc.CreateOffer(nil)
	if err != nil {
		return err
	}
	return s.setLocalAndSend(ctx, offer)
}

// HandleSignal processes an inbound offer or answer.
func (s *Session) HandleSignal(sig SignedSignal) error {
	if sig.From != string(s.remote) {
		return fmt.Errorf("peer: signal from unexpected node %s", sig.From)
	}
	desc, err := VerifySignal(sig)
	if err != nil {
		return err
	}
	if err := s.pc.SetRemoteDescription(desc); err != nil {
		return err
	}
	if desc.Type == webrtc.SDPTypeOffer {
		answer, err := s.pc.CreateAnswer(nil)
		if err != nil {
			return err
		}
		return s.setLocalAndSend(context.Background(), answer)
	}
	return nil
}

// setLocalAndSend sets the local description, waits for non-trickle ICE gathering,
// then signs and relays the complete SDP.
func (s *Session) setLocalAndSend(ctx context.Context, desc webrtc.SessionDescription) error {
	gatherComplete := webrtc.GatheringCompletePromise(s.pc)
	if err := s.pc.SetLocalDescription(desc); err != nil {
		return err
	}
	select {
	case <-gatherComplete:
	case <-ctx.Done():
		return ctx.Err()
	}
	signed, err := SignSignal(s.self, *s.pc.LocalDescription())
	if err != nil {
		return err
	}
	return s.signaler.SendSignal(s.remote, signed)
}

// bindControl wires the control channel: send a Handshake on open, dispatch
// inbound Envelopes (Handshake -> ready, Ping -> Pong, Pong -> pending).
func (s *Session) bindControl(dc *webrtc.DataChannel) {
	s.mu.Lock()
	s.control = dc
	s.mu.Unlock()
	dc.OnOpen(func() {
		_ = s.send(&peerv1.Envelope{
			Body: &peerv1.Envelope_Handshake{Handshake: &peerv1.Handshake{
				ProtocolVersion: ProtocolVersion,
			}},
		})
	})
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		env, err := decodeEnvelope(msg.Data)
		if err != nil {
			return
		}
		switch body := env.Body.(type) {
		case *peerv1.Envelope_Handshake:
			if body.Handshake.GetProtocolVersion() == ProtocolVersion {
				s.readyOnce.Do(func() { close(s.ready) })
			}
		case *peerv1.Envelope_Ping:
			_ = s.send(&peerv1.Envelope{
				RequestId: env.RequestId,
				Body:      &peerv1.Envelope_Pong{Pong: &peerv1.Pong{Nonce: body.Ping.GetNonce()}},
			})
		case *peerv1.Envelope_Pong:
			s.mu.Lock()
			ch, ok := s.pending[env.RequestId]
			delete(s.pending, env.RequestId)
			s.mu.Unlock()
			if ok {
				ch <- env
			}
		}
	})
}

func (s *Session) send(env *peerv1.Envelope) error {
	raw, err := encodeEnvelope(env)
	if err != nil {
		return err
	}
	s.mu.Lock()
	dc := s.control
	s.mu.Unlock()
	if dc == nil {
		return fmt.Errorf("peer: control channel not open")
	}
	return dc.Send(raw)
}

// Ready is closed once the version handshake succeeds.
func (s *Session) Ready() <-chan struct{} { return s.ready }

// Ping sends a ping and returns the echoed nonce.
func (s *Session) Ping(ctx context.Context, nonce string) (string, error) {
	id := s.ids.next()
	ch := make(chan *peerv1.Envelope, 1)
	s.mu.Lock()
	s.pending[id] = ch
	s.mu.Unlock()

	if err := s.send(&peerv1.Envelope{
		RequestId: id,
		Body:      &peerv1.Envelope_Ping{Ping: &peerv1.Ping{Nonce: nonce}},
	}); err != nil {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return "", err
	}
	select {
	case env := <-ch:
		return env.GetPong().GetNonce(), nil
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return "", ctx.Err()
	}
}

// Close tears down the connection.
func (s *Session) Close() error { return s.pc.Close() }
