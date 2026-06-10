package app

import (
	"context"
	"encoding/base64"

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

func (n *Node) Reject(peerID string) error { return n.RejectAccess(identity.NodeID(peerID)) }

func (n *Node) LeaveParty() { n.party.LeaveParty() }

func (n *Node) EndParty(reason string) { n.party.EndParty(reason) }

func (n *Node) Audience() []bridge.PeerView {
	members := n.party.audienceView()
	out := make([]bridge.PeerView, 0, len(members))
	for _, m := range members {
		out = append(out, bridge.PeerView{NodeID: m.GetNodeId(), DisplayName: m.GetDisplayName(), Online: true})
	}
	return out
}

func (n *Node) CurrentParty() bridge.CurrentPartyView {
	cp := n.party.currentParty()
	if !cp.active {
		return bridge.CurrentPartyView{}
	}
	return bridge.CurrentPartyView{
		Active:    true,
		Role:      cp.role,
		Host:      string(cp.host),
		ContentID: cp.contentID,
		Title:     n.titleFor(cp.contentID),
		Viewers:   cp.viewers,
	}
}

// titleFor returns the local library display title for contentID, or "" when not
// found (e.g. a viewer watching a remote host's content this node doesn't hold).
func (n *Node) titleFor(contentID string) string {
	if contentID == "" {
		return ""
	}
	lib, err := n.Library()
	if err != nil {
		return ""
	}
	for _, t := range lib {
		if t.ContentID == contentID {
			return t.DisplayTitle
		}
	}
	return ""
}

func toTitleViews(metas []*peerv1.TitleMeta) []bridge.TitleView {
	out := make([]bridge.TitleView, 0, len(metas))
	for _, m := range metas {
		thumb := ""
		if b := m.GetThumbnail(); len(b) > 0 {
			thumb = "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(b)
		}
		out = append(out, bridge.TitleView{
			ContentID:    m.GetContentId(),
			DisplayTitle: m.GetDisplayTitle(),
			DurationMs:   m.GetDurationMs(),
			PartyLive:    m.GetPartyLive(),
			PartyViewers: int(m.GetPartyViewers()),
			Thumbnail:    thumb,
			RelDir:       m.GetRelDir(),
			RootLabel:    m.GetRootLabel(),
		})
	}
	return out
}
