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

	handler         RequestHandler
	onAccessGranted func(identity.NodeID)
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
		// TODO(slice-3): thread a real context here; the answerer's ICE gather currently has no timeout.
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
		case *peerv1.Envelope_Browse:
			s.handleBrowse(env.RequestId)
		case *peerv1.Envelope_GetMetadata:
			s.handleGetMetadata(env.RequestId, body.GetMetadata.GetContentId())
		case *peerv1.Envelope_RequestAccess:
			s.handleRequestAccess(env.RequestId, body.RequestAccess.GetMessage())
		case *peerv1.Envelope_AccessGranted:
			s.mu.Lock()
			fn := s.onAccessGranted
			s.mu.Unlock()
			if fn != nil {
				fn(s.remote)
			}
		case *peerv1.Envelope_Pong, *peerv1.Envelope_Catalog,
			*peerv1.Envelope_TitleMeta, *peerv1.Envelope_Ack, *peerv1.Envelope_Error:
			s.deliver(env)
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
	resp, err := s.call(ctx, &peerv1.Envelope{Body: &peerv1.Envelope_Ping{Ping: &peerv1.Ping{Nonce: nonce}}})
	if err != nil {
		return "", err
	}
	if e := resp.GetError(); e != nil {
		return "", statusErr(e)
	}
	return resp.GetPong().GetNonce(), nil
}

// Close tears down the connection.
// TODO(slice-3): cancel in-flight Pings (close a done channel) and tear down pending when Close gains real connection lifecycle management.
func (s *Session) Close() error { return s.pc.Close() }

// RequestHandler answers inbound RPCs. Installed per Session by the app layer.
type RequestHandler interface {
	Browse(remote identity.NodeID) ([]*peerv1.TitleMeta, error)
	GetMetadata(remote identity.NodeID, contentID string) (*peerv1.TitleMeta, error)
	RequestAccess(remote identity.NodeID, message string) error
}

// SetHandler installs the inbound-request handler.
func (s *Session) SetHandler(h RequestHandler) {
	s.mu.Lock()
	s.handler = h
	s.mu.Unlock()
}

// currentHandler returns the installed handler under lock.
func (s *Session) currentHandler() RequestHandler {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.handler
}

// OnAccessGranted registers a callback fired when the remote grants us access.
func (s *Session) OnAccessGranted(fn func(identity.NodeID)) {
	s.mu.Lock()
	s.onAccessGranted = fn
	s.mu.Unlock()
}

// call sends a request envelope and waits for the correlated response.
func (s *Session) call(ctx context.Context, env *peerv1.Envelope) (*peerv1.Envelope, error) {
	id := s.ids.next()
	env.RequestId = id
	ch := make(chan *peerv1.Envelope, 1)
	s.mu.Lock()
	s.pending[id] = ch
	s.mu.Unlock()
	if err := s.send(env); err != nil {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, err
	}
	select {
	case resp := <-ch:
		return resp, nil
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (s *Session) deliver(env *peerv1.Envelope) {
	s.mu.Lock()
	ch, ok := s.pending[env.RequestId]
	delete(s.pending, env.RequestId)
	s.mu.Unlock()
	if ok {
		ch <- env
	}
}

// Browse fetches the remote's Catalog.
func (s *Session) Browse(ctx context.Context) ([]*peerv1.TitleMeta, error) {
	resp, err := s.call(ctx, &peerv1.Envelope{Body: &peerv1.Envelope_Browse{Browse: &peerv1.Browse{}}})
	if err != nil {
		return nil, err
	}
	if e := resp.GetError(); e != nil {
		return nil, statusErr(e)
	}
	return resp.GetCatalog().GetTitles(), nil
}

// GetMetadata fetches one Title's metadata.
func (s *Session) GetMetadata(ctx context.Context, contentID string) (*peerv1.TitleMeta, error) {
	resp, err := s.call(ctx, &peerv1.Envelope{Body: &peerv1.Envelope_GetMetadata{GetMetadata: &peerv1.GetMetadata{ContentId: contentID}}})
	if err != nil {
		return nil, err
	}
	if e := resp.GetError(); e != nil {
		return nil, statusErr(e)
	}
	return resp.GetTitleMeta(), nil
}

// RequestAccess asks the remote Host to allow this Node.
func (s *Session) RequestAccess(ctx context.Context, message string) error {
	resp, err := s.call(ctx, &peerv1.Envelope{Body: &peerv1.Envelope_RequestAccess{RequestAccess: &peerv1.RequestAccess{Message: message}}})
	if err != nil {
		return err
	}
	if e := resp.GetError(); e != nil {
		return statusErr(e)
	}
	return nil // Ack
}

// SendAccessGranted notifies the remote Viewer that access was approved.
func (s *Session) SendAccessGranted() error {
	return s.send(&peerv1.Envelope{Body: &peerv1.Envelope_AccessGranted{AccessGranted: &peerv1.AccessGranted{}}})
}

func (s *Session) handleBrowse(reqID uint64) {
	h := s.currentHandler()
	if h == nil {
		_ = s.send(errEnvelope(reqID, ErrUnavailable))
		return
	}
	titles, err := h.Browse(s.remote)
	if err != nil {
		_ = s.send(errEnvelope(reqID, err))
		return
	}
	_ = s.send(&peerv1.Envelope{RequestId: reqID, Body: &peerv1.Envelope_Catalog{Catalog: &peerv1.Catalog{Titles: titles}}})
}

func (s *Session) handleGetMetadata(reqID uint64, contentID string) {
	h := s.currentHandler()
	if h == nil {
		_ = s.send(errEnvelope(reqID, ErrUnavailable))
		return
	}
	meta, err := h.GetMetadata(s.remote, contentID)
	if err != nil {
		_ = s.send(errEnvelope(reqID, err))
		return
	}
	_ = s.send(&peerv1.Envelope{RequestId: reqID, Body: &peerv1.Envelope_TitleMeta{TitleMeta: meta}})
}

func (s *Session) handleRequestAccess(reqID uint64, message string) {
	h := s.currentHandler()
	if h == nil {
		_ = s.send(errEnvelope(reqID, ErrUnavailable))
		return
	}
	if err := h.RequestAccess(s.remote, message); err != nil {
		_ = s.send(errEnvelope(reqID, err))
		return
	}
	_ = s.send(&peerv1.Envelope{RequestId: reqID, Body: &peerv1.Envelope_Ack{Ack: &peerv1.Ack{}}})
}
