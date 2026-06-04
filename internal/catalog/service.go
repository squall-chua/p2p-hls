package catalog

import (
	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/squall-chua/p2p-hls/internal/library"
	"github.com/squall-chua/p2p-hls/internal/peer"
	peerv1 "github.com/squall-chua/p2p-hls/proto/peer/v1"
)

// Service answers browse RPCs from Viewers, enforcing the access Policy.
// It implements peer.RequestHandler.
type Service struct {
	store  *library.Store
	policy *Policy
	reqs   *Requests
}

// NewService wires the Store, Policy, and Requests together.
func NewService(store *library.Store, policy *Policy, reqs *Requests) *Service {
	return &Service{store: store, policy: policy, reqs: reqs}
}

// Browse returns the Catalog visible to remote, or peer.ErrDenied.
func (s *Service) Browse(remote identity.NodeID) ([]*peerv1.TitleMeta, error) {
	if !s.policy.Allowed(remote) {
		return nil, peer.ErrDenied
	}
	titles, err := s.store.All()
	if err != nil {
		return nil, err
	}
	out := make([]*peerv1.TitleMeta, 0, len(titles))
	for _, t := range titles {
		out = append(out, toMeta(t))
	}
	return out, nil
}

// GetMetadata returns one Title's metadata, or peer.ErrDenied/peer.ErrNotFound.
func (s *Service) GetMetadata(remote identity.NodeID, contentID string) (*peerv1.TitleMeta, error) {
	if !s.policy.Allowed(remote) {
		return nil, peer.ErrDenied
	}
	t, ok, err := s.store.Get(contentID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, peer.ErrNotFound
	}
	return toMeta(t), nil
}

// RequestAccess records a pending access request from remote.
func (s *Service) RequestAccess(remote identity.NodeID, message string) error {
	s.reqs.Add(remote, message)
	return nil
}

// Requests exposes the pending-request register.
func (s *Service) Requests() *Requests { return s.reqs }

// Approve allows the Node and clears its pending request.
func (s *Service) Approve(node identity.NodeID) {
	s.reqs.Take(node)
	s.policy.AddAllow(node)
}

func toMeta(t library.Title) *peerv1.TitleMeta {
	m := &peerv1.TitleMeta{
		ContentId:     t.ContentID,
		DisplayTitle:  t.DisplayTitle,
		DurationMs:    t.DurationMS,
		Container:     t.Container,
		VideoCodec:    t.VideoCodec,
		AudioCodecs:   t.AudioCodecs,
		Width:         int32(t.Width),
		Height:        int32(t.Height),
		SizeBytes:     t.Size,
		HlsCompatible: t.HLSCompatible,
	}
	for _, sub := range t.Subtitles {
		m.Subtitles = append(m.Subtitles, &peerv1.SubtitleTrack{
			Id: sub.ID, Language: sub.Language, Label: sub.Label, Kind: sub.Kind,
		})
	}
	return m
}
