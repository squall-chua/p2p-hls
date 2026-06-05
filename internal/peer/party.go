package peer

import (
	"context"
	"time"

	"github.com/squall-chua/p2p-hls/internal/identity"
	peerv1 "github.com/squall-chua/p2p-hls/proto/peer/v1"
)

// CapParty is the handshake capability string for watch-party support.
const CapParty = "party/v1"

// PartyHandler receives inbound watch-party messages. A Node implements it once
// and the same handler covers both host-side (OnJoinParty/OnLeaveParty) and
// viewer-side (OnParty*) messages, since which role applies depends on the peer.
type PartyHandler interface {
	OnJoinParty(remote identity.NodeID, contentID string) (*peerv1.PartyWelcome, error)
	OnLeaveParty(remote identity.NodeID, partyID string)
	OnPartyState(remote identity.NodeID, s *peerv1.PartyState)
	OnPartyAudience(remote identity.NodeID, a *peerv1.PartyAudience)
	OnPartyInvite(remote identity.NodeID, inv *peerv1.PartyInvite)
	OnPartyEnded(remote identity.NodeID, e *peerv1.PartyEnded)
}

// SetPartyHandler installs the handler for inbound party messages.
func (s *Session) SetPartyHandler(h PartyHandler) {
	s.mu.Lock()
	s.partyHandler = h
	s.mu.Unlock()
}

func (s *Session) currentPartyHandler() PartyHandler {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.partyHandler
}

// SendControl sends a fire-and-forget Envelope on the control channel. Used for
// PartyState/PartyAudience/PartyInvite/PartyEnded/LeaveParty.
func (s *Session) SendControl(env *peerv1.Envelope) error { return s.send(env) }

// JoinParty sends a JoinParty request and awaits the PartyWelcome response.
func (s *Session) JoinParty(ctx context.Context, contentID string) (*peerv1.PartyWelcome, error) {
	resp, err := s.call(ctx, &peerv1.Envelope{
		Body: &peerv1.Envelope_JoinParty{JoinParty: &peerv1.JoinParty{ContentId: contentID}},
	})
	if err != nil {
		return nil, err
	}
	if e := resp.GetError(); e != nil {
		return nil, statusErr(e)
	}
	w := resp.GetPartyWelcome()
	if w == nil {
		return nil, ErrUnavailable
	}
	return w, nil
}

// MeasureRTT times a Ping round trip.
func (s *Session) MeasureRTT(ctx context.Context) (time.Duration, error) {
	start := time.Now()
	if _, err := s.Ping(ctx, "rtt"); err != nil {
		return 0, err
	}
	return time.Since(start), nil
}

// HasCapability reports whether the remote advertised the given capability.
func (s *Session) HasCapability(cap string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.remoteCaps {
		if c == cap {
			return true
		}
	}
	return false
}

// SetOnClose registers a callback fired when the peer connection drops.
func (s *Session) SetOnClose(fn func(identity.NodeID)) {
	s.mu.Lock()
	s.onClose = fn
	s.mu.Unlock()
}

func (s *Session) handleJoinParty(reqID uint64, contentID string) {
	h := s.currentPartyHandler()
	if h == nil {
		_ = s.send(errEnvelope(reqID, ErrUnavailable))
		return
	}
	w, err := h.OnJoinParty(s.remote, contentID)
	if err != nil {
		_ = s.send(errEnvelope(reqID, err))
		return
	}
	_ = s.send(&peerv1.Envelope{RequestId: reqID, Body: &peerv1.Envelope_PartyWelcome{PartyWelcome: w}})
}
