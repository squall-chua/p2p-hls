package app

import (
	"context"

	"github.com/squall-chua/p2p-hls/internal/bridge"
	"github.com/squall-chua/p2p-hls/internal/identity"
	peerv1 "github.com/squall-chua/p2p-hls/proto/peer/v1"
)

// Compile-time proof that *Node satisfies bridge.Control.
var _ bridge.Control = (*Node)(nil)
var _ bridge.Subscriber = (*hub)(nil)

// Events returns the node's event source for the bridge SSE handler.
func (n *Node) Events() bridge.Subscriber { return n.hub }

func (n *Node) Self() bridge.SelfView {
	return bridge.SelfView{NodeID: string(n.self.NodeID()), DisplayName: n.displayName}
}

func (n *Node) Presence() []bridge.PeerView {
	peers := n.client.Peers()
	out := make([]bridge.PeerView, 0, len(peers))
	for _, p := range peers {
		out = append(out, bridge.PeerView{NodeID: p.NodeID, DisplayName: p.DisplayName, Online: true})
	}
	return out
}

func (n *Node) Library() ([]bridge.TitleView, error) {
	n.mu.Lock()
	svc := n.catalog
	n.mu.Unlock()
	if svc == nil {
		return nil, nil
	}
	metas, err := svc.Library()
	if err != nil {
		return nil, err
	}
	return toTitleViews(metas), nil
}

func (n *Node) Catalog(ctx context.Context, peerID string) ([]bridge.TitleView, error) {
	metas, err := n.Browse(ctx, identity.NodeID(peerID))
	if err != nil {
		return nil, err
	}
	return toTitleViews(metas), nil
}

func (n *Node) Approve(peerID string) error { return n.ApproveAccess(identity.NodeID(peerID)) }

func (n *Node) LeaveParty() { n.party.LeaveParty() }

func (n *Node) EndParty(reason string) { n.party.EndParty(reason) }

func toTitleViews(metas []*peerv1.TitleMeta) []bridge.TitleView {
	out := make([]bridge.TitleView, 0, len(metas))
	for _, m := range metas {
		out = append(out, bridge.TitleView{
			ContentID:    m.GetContentId(),
			DisplayTitle: m.GetDisplayTitle(),
			DurationMs:   m.GetDurationMs(),
			PartyLive:    m.GetPartyLive(),
			PartyViewers: int(m.GetPartyViewers()),
		})
	}
	return out
}
