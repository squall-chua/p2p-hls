package peer

import (
	"context"

	"github.com/squall-chua/p2p-hls/internal/identity"
	peerv1 "github.com/squall-chua/p2p-hls/proto/peer/v1"
)

// SwarmHandler answers inbound swarm messages from a peer Viewer in a party.
type SwarmHandler interface {
	OnSwarmHave(remote identity.NodeID, h *peerv1.SwarmHave)
	SwarmSegment(remote identity.NodeID, req *peerv1.GetSwarmSegment) ([]byte, func(), error)
}

// SetSwarmHandler installs the handler for inbound SwarmHave / GetSwarmSegment.
func (s *Session) SetSwarmHandler(h SwarmHandler) {
	s.mu.Lock()
	s.swarmHandler = h
	s.mu.Unlock()
}

func (s *Session) currentSwarmHandler() SwarmHandler {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.swarmHandler
}

// SendSwarmHave gossips a have-map to this peer over the control channel.
func (s *Session) SendSwarmHave(h *peerv1.SwarmHave) error {
	return s.send(&peerv1.Envelope{Body: &peerv1.Envelope_SwarmHave{SwarmHave: h}})
}

// GetSwarmSegment pulls a cached Segment from this peer over the bulk channel.
func (s *Session) GetSwarmSegment(ctx context.Context, req *peerv1.GetSwarmSegment) ([]byte, error) {
	return s.fetchBulk(ctx, &peerv1.Envelope{Body: &peerv1.Envelope_GetSwarmSegment{GetSwarmSegment: req}})
}

// handleGetSwarmSegment serves a Segment to a peer. It runs on a goroutine so a slow
// bulk upload never head-of-line-blocks this Viewer's gossip or party sync.
func (s *Session) handleGetSwarmSegment(reqID uint64, req *peerv1.GetSwarmSegment) {
	h := s.currentSwarmHandler()
	if h == nil {
		_ = s.send(errEnvelope(reqID, ErrUnavailable))
		return
	}
	go func() {
		data, release, err := h.SwarmSegment(s.remote, req)
		if err != nil {
			_ = s.send(errEnvelope(reqID, err))
			return
		}
		defer release()
		if serr := s.sendBulk(reqID, data); serr != nil {
			_ = s.send(errEnvelope(reqID, serr))
		}
	}()
}
