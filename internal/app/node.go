package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
	"github.com/squall-chua/p2p-hls/internal/catalog"
	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/squall-chua/p2p-hls/internal/media"
	"github.com/squall-chua/p2p-hls/internal/party"
	"github.com/squall-chua/p2p-hls/internal/peer"
	"github.com/squall-chua/p2p-hls/internal/signaling"
	peerv1 "github.com/squall-chua/p2p-hls/proto/peer/v1"
)

// Node is a running app instance: a signaling client plus its peer sessions.
type Node struct {
	self   *identity.Identity
	client *signaling.Client
	rtcCfg webrtc.Configuration

	mu       sync.Mutex
	sessions map[identity.NodeID]*peer.Session
	catalog  *catalog.Service
	media    *media.Service
	party    *partyCoordinator
}

// relaySignaler adapts the signaling client to peer.Signaler.
type relaySignaler struct {
	client *signaling.Client
}

func (r relaySignaler) SendSignal(to identity.NodeID, s peer.SignedSignal) error {
	payload, err := s.Encode()
	if err != nil {
		return err
	}
	wrapped, err := peer.EncodeRelay(peer.RelayKindSignal, json.RawMessage(payload))
	if err != nil {
		return err
	}
	return r.client.SendRelay(to, wrapped)
}

// NewNode connects to signaling and starts routing inbound relays.
func NewNode(ctx context.Context, self *identity.Identity, displayName string, cfg Config) (*Node, error) {
	client, err := signaling.Dial(ctx, cfg.SignalingURL, self, displayName)
	if err != nil {
		return nil, err
	}
	rtcCfg := webrtc.Configuration{}
	for _, s := range cfg.STUNServers {
		rtcCfg.ICEServers = append(rtcCfg.ICEServers, webrtc.ICEServer{URLs: []string{s}})
	}
	n := &Node{
		self:     self,
		client:   client,
		rtcCfg:   rtcCfg,
		sessions: make(map[identity.NodeID]*peer.Session),
	}
	n.party = newPartyCoordinator(n, self.NodeID(), party.RealClock(), party.DefaultConfig())
	go n.routeRelays()
	return n, nil
}

func (n *Node) routeRelays() {
	for rel := range n.client.Relays() {
		from := identity.NodeID(rel.From)
		kind, data, err := peer.DecodeRelay(rel.Payload)
		if err != nil {
			continue
		}
		switch kind {
		case peer.RelayKindSignal:
			sig, derr := peer.DecodeSignedSignal(data)
			if derr != nil {
				continue
			}
			sess, serr := n.sessionFor(from, false)
			if serr != nil {
				slog.Warn("failed to create session for inbound relay", "from", from, "err", serr)
				continue
			}
			_ = sess.HandleSignal(sig)
		case peer.RelayKindSwarmDial:
			// A peer wants us to dial it. We are the lower NodeID for this edge.
			if shouldDial(n.self.NodeID(), from) {
				go func(to identity.NodeID) {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					_, _ = n.Dial(ctx, to)
				}(from)
			}
		}
	}
}

// shouldDial implements the glare rule: the lower NodeID is the initiator.
func shouldDial(self, peer identity.NodeID) bool { return self < peer }

// ensurePeer makes sure a session to node exists, applying glare resolution. If we
// are the lower NodeID we dial; otherwise we send a SwarmDial nudge so the peer
// (the lower NodeID) dials us. Non-blocking for the nudge path.
func (n *Node) ensurePeer(node identity.NodeID) error {
	n.mu.Lock()
	_, have := n.sessions[node]
	n.mu.Unlock()
	if have {
		return nil
	}
	if shouldDial(n.self.NodeID(), node) {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, _ = n.Dial(ctx, node)
		}()
		return nil
	}
	payload, err := peer.EncodeRelay(peer.RelayKindSwarmDial,
		peer.SwarmDial{PartyID: n.party.activePartyID(), From: string(n.self.NodeID())})
	if err != nil {
		return err
	}
	return n.client.SendRelay(node, payload)
}

// sendTo implements sender: sends an Envelope to a remote node.
func (n *Node) sendTo(node identity.NodeID, env *peerv1.Envelope) error {
	s, err := n.session(context.Background(), node)
	if err != nil {
		return err
	}
	return s.SendControl(env)
}

// fetchSwarmSegment pulls a Segment from a peer Viewer over its bulk channel.
func (n *Node) fetchSwarmSegment(ctx context.Context, node identity.NodeID, req *peerv1.GetSwarmSegment) ([]byte, error) {
	s, err := n.session(ctx, node)
	if err != nil {
		return nil, err
	}
	return s.GetSwarmSegment(ctx, req)
}

// hostPlaylist fetches a playlist directly from the Host (the integrity trust anchor).
func (n *Node) hostPlaylist(ctx context.Context, host identity.NodeID, contentID, name string) ([]byte, error) {
	s, err := n.session(ctx, host)
	if err != nil {
		return nil, err
	}
	data, _, _, err := s.GetPlaylist(ctx, contentID, name)
	return data, err
}

// hostSegment fetches a Segment directly from the Host (last-resort source).
func (n *Node) hostSegment(ctx context.Context, host identity.NodeID, contentID, name string) ([]byte, error) {
	s, err := n.session(ctx, host)
	if err != nil {
		return nil, err
	}
	return s.GetSegment(ctx, contentID, name)
}

// measureRTT implements sender: times a Ping round trip to a remote node.
func (n *Node) measureRTT(ctx context.Context, node identity.NodeID) (time.Duration, error) {
	s, err := n.session(ctx, node)
	if err != nil {
		return 0, err
	}
	return s.MeasureRTT(ctx)
}

// sessionFor returns the existing session for remote, or creates one. When
// created as an answerer (initiator=false) it is started immediately.
func (n *Node) sessionFor(remote identity.NodeID, initiator bool) (*peer.Session, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if s, ok := n.sessions[remote]; ok {
		return s, nil
	}
	s, err := peer.NewSession(n.self, remote, n.rtcCfg, relaySignaler{client: n.client})
	if err != nil {
		return nil, err
	}
	n.sessions[remote] = s
	if n.catalog != nil {
		s.SetHandler(n.catalog)
	}
	if n.media != nil {
		s.SetMediaHandler(n.media)
	}
	if n.party != nil {
		s.SetPartyHandler(n.party)
		s.SetSwarmHandler(n.party)
		s.SetOnClose(func(node identity.NodeID) { n.party.OnLeaveParty(node, "") })
	}
	if !initiator {
		_ = s.Start(context.Background(), false)
	}
	return s, nil
}

// Sees reports whether remote is currently in presence.
func (n *Node) Sees(remote identity.NodeID) bool {
	for _, p := range n.client.Peers() {
		if p.NodeID == string(remote) {
			return true
		}
	}
	return false
}

// Dial opens (or returns) a session to remote and blocks until it is ready.
// NOTE(slice-3): if a session for remote was already created as an answerer, calling Start(true) here would double-negotiate (dial-vs-answer glare). Only one side dials in Slice 1; glare resolution is future work.
func (n *Node) Dial(ctx context.Context, remote identity.NodeID) (*peer.Session, error) {
	sess, err := n.sessionFor(remote, true)
	if err != nil {
		return nil, err
	}
	if err := sess.Start(ctx, true); err != nil {
		return nil, err
	}
	select {
	case <-sess.Ready():
		return sess, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// SetCatalog installs the Service that answers inbound browse RPCs. Existing and
// future sessions use it as their request handler.
func (n *Node) SetCatalog(svc *catalog.Service) {
	n.mu.Lock()
	n.catalog = svc
	for _, s := range n.sessions {
		s.SetHandler(svc)
	}
	n.mu.Unlock()
	svc.SetPartyProvider(n.party)
	n.party.setAllowed(svc.Allowed)
}

// SetMedia installs the streaming handler on existing and future sessions.
func (n *Node) SetMedia(svc *media.Service) {
	n.mu.Lock()
	n.media = svc
	for _, s := range n.sessions {
		s.SetMediaHandler(svc)
	}
	n.mu.Unlock()
}

// Playlist implements bridge.Streamer.
func (n *Node) Playlist(ctx context.Context, host identity.NodeID, contentID, name string) ([]byte, string, error) {
	sess, err := n.session(ctx, host)
	if err != nil {
		return nil, "", err
	}
	data, ct, _, err := sess.GetPlaylist(ctx, contentID, name)
	return data, ct, err
}

// Segment implements bridge.Streamer.
func (n *Node) Segment(ctx context.Context, host identity.NodeID, contentID, name string) ([]byte, string, error) {
	if ss := n.party.swarmFor(host, contentID); ss != nil {
		data, err := ss.FetchSegment(ctx, name)
		if err != nil {
			return nil, "", err
		}
		return data, contentTypeFor(name), nil
	}
	sess, err := n.session(ctx, host)
	if err != nil {
		return nil, "", err
	}
	data, err := sess.GetSegment(ctx, contentID, name)
	if err != nil {
		return nil, "", err
	}
	return data, contentTypeFor(name), nil
}

func contentTypeFor(name string) string {
	switch {
	case strings.HasSuffix(name, ".ts"):
		return "video/mp2t"
	case strings.HasSuffix(name, ".vtt"):
		return "text/vtt"
	default:
		return "application/octet-stream"
	}
}

// Browse returns the remote Host's Catalog.
func (n *Node) Browse(ctx context.Context, remote identity.NodeID) ([]*peerv1.TitleMeta, error) {
	sess, err := n.session(ctx, remote)
	if err != nil {
		return nil, err
	}
	return sess.Browse(ctx)
}

// RequestAccess asks the remote Host to allow this Node.
func (n *Node) RequestAccess(ctx context.Context, remote identity.NodeID, message string) error {
	sess, err := n.session(ctx, remote)
	if err != nil {
		return err
	}
	return sess.RequestAccess(ctx, message)
}

// PendingRequests lists Node IDs awaiting our approval.
func (n *Node) PendingRequests() []identity.NodeID {
	n.mu.Lock()
	svc := n.catalog
	n.mu.Unlock()
	if svc == nil {
		return nil
	}
	return svc.Requests().List()
}

// ApproveAccess allows remote, then notifies it via AccessGranted.
func (n *Node) ApproveAccess(remote identity.NodeID) error {
	n.mu.Lock()
	svc := n.catalog
	sess := n.sessions[remote]
	n.mu.Unlock()
	if svc == nil {
		return fmt.Errorf("app: no catalog installed")
	}
	svc.Approve(remote)
	if sess != nil {
		return sess.SendAccessGranted()
	}
	return nil
}

// session returns a ready session to remote, dialing if necessary.
func (n *Node) session(ctx context.Context, remote identity.NodeID) (*peer.Session, error) {
	n.mu.Lock()
	s, ok := n.sessions[remote]
	n.mu.Unlock()
	if ok {
		return s, nil
	}
	return n.Dial(ctx, remote)
}

// StartParty opens a Watch Party for contentID and returns the party_id.
func (n *Node) StartParty(contentID string) string { return n.party.StartParty(contentID) }

// JoinParty dials host (if needed) and joins the party for contentID.
func (n *Node) JoinParty(ctx context.Context, host identity.NodeID, contentID string) error {
	s, err := n.session(ctx, host)
	if err != nil {
		return err
	}
	return n.party.JoinParty(ctx, host, contentID, func(ctx context.Context) (*peerv1.PartyWelcome, error) {
		return s.JoinParty(ctx, contentID)
	})
}

// PartyViewerDecide exposes the viewer correction for tests/e2e (a Go actuator).
func (n *Node) PartyViewerDecide(posMS int64, playing bool) party.Action {
	return n.party.viewerDecide(posMS, playing, party.RealClock().Now())
}

// IngestHostPlayer feeds host player events from a Go actuator (tests/e2e).
func (n *Node) IngestHostPlayer(kind string, posMS int64) {
	now := party.RealClock().Now()
	n.party.mu.Lock()
	h := n.party.host
	n.party.mu.Unlock()
	if h == nil {
		return
	}
	switch kind {
	case "play":
		h.OnPlay(posMS, now)
	case "pause":
		h.OnPause(posMS, now)
	case "seek":
		h.OnSeek(posMS, now)
	case "report":
		h.OnReport(posMS, now)
	}
}

// PartyWS returns the loopback WebSocket handler for external bridge wiring.
func (n *Node) PartyWS() func(*websocket.Conn) { return n.party.serveWS }

// Close shuts the node down.
// NOTE(slice-3): relays already buffered in the client channel may drive routeRelays to create a session after Close returns; such a late session isn't tracked/closed here. Benign at process exit.
func (n *Node) Close() error {
	n.mu.Lock()
	for _, s := range n.sessions {
		_ = s.Close()
	}
	n.mu.Unlock()
	return n.client.Close()
}
