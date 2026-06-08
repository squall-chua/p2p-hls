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
	"github.com/squall-chua/p2p-hls/internal/party"
	"github.com/squall-chua/p2p-hls/internal/peer"
	"github.com/squall-chua/p2p-hls/internal/signaling"
	peerv1 "github.com/squall-chua/p2p-hls/proto/peer/v1"
)

// localMedia is the owner-playback subset of the media service (no access policy).
type localMedia interface {
	LocalPlaylist(contentID, name string) ([]byte, string, bool, error)
	LocalSegment(contentID, name string) ([]byte, error)
}

// Node is a running app instance: a signaling client plus its peer sessions.
type Node struct {
	self        *identity.Identity
	displayName string
	client      *signaling.Client
	rtcCfg      webrtc.Configuration

	mu       sync.Mutex
	sessions map[identity.NodeID]*peer.Session
	catalog  *catalog.Service
	media    peer.MediaHandler
	party    *partyCoordinator
	hub      *hub
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
		self:        self,
		displayName: displayName,
		client:      client,
		rtcCfg:      rtcCfg,
		sessions:    make(map[identity.NodeID]*peer.Session),
	}
	n.party = newPartyCoordinator(n, self.NodeID(), party.RealClock(), party.DefaultConfig())
	n.hub = newHub()
	n.party.onAudience = func() { n.hub.publish(Event{Type: "audience"}) }
	n.party.onPartyEnded = func() { n.hub.publish(Event{Type: "party-ended"}) }
	client.SetOnPresenceChange(func() { n.hub.publish(Event{Type: "presence"}) })
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
			sess, _, serr := n.sessionFor(from, false)
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

// peerSession returns a ready session to a swarm peer, honoring the glare rule.
// Unlike session (which always dials, correct for the viewer->Host edge), a mesh
// edge is symmetric: only the lower NodeID dials. If we are the dialer we dial;
// otherwise we nudge the peer to dial us and report ErrUnavailable so the caller
// (a periodic, idempotent gossip/pull) skips this round and retries once the
// answerer session is up. This prevents two simultaneous offers from colliding.
func (n *Node) peerSession(ctx context.Context, node identity.NodeID) (*peer.Session, error) {
	n.mu.Lock()
	s, ok := n.sessions[node]
	n.mu.Unlock()
	if ok {
		select {
		case <-s.Ready():
			return s, nil
		default:
			return nil, peer.ErrUnavailable
		}
	}
	if shouldDial(n.self.NodeID(), node) {
		return n.Dial(ctx, node)
	}
	if err := n.ensurePeer(node); err != nil {
		return nil, err
	}
	return nil, peer.ErrUnavailable
}

// sendTo implements sender: sends an Envelope to a remote node.
func (n *Node) sendTo(node identity.NodeID, env *peerv1.Envelope) error {
	s, err := n.peerSession(context.Background(), node)
	if err != nil {
		return err
	}
	return s.SendControl(env)
}

// fetchSwarmSegment pulls a Segment from a peer Viewer over its bulk channel.
func (n *Node) fetchSwarmSegment(ctx context.Context, node identity.NodeID, req *peerv1.GetSwarmSegment) ([]byte, error) {
	s, err := n.peerSession(ctx, node)
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
	s, err := n.peerSession(ctx, node)
	if err != nil {
		return 0, err
	}
	return s.MeasureRTT(ctx)
}

// sessionFor returns the existing session for remote, or creates one. When
// created as an answerer (initiator=false) it is started immediately. The
// returned bool reports whether this call created the session (false if it
// already existed), so callers can avoid re-negotiating an established edge.
func (n *Node) sessionFor(remote identity.NodeID, initiator bool) (*peer.Session, bool, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if s, ok := n.sessions[remote]; ok {
		return s, false, nil
	}
	s, err := peer.NewSession(n.self, remote, n.rtcCfg, relaySignaler{client: n.client})
	if err != nil {
		return nil, false, err
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
	return s, true, nil
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
// It only drives the offer (Start as initiator) when it actually created the
// session; an already-existing session (created earlier as initiator or as an
// answerer) is just awaited, so concurrent Dials never re-negotiate and tear
// down an established edge.
func (n *Node) Dial(ctx context.Context, remote identity.NodeID) (*peer.Session, error) {
	sess, created, err := n.sessionFor(remote, true)
	if err != nil {
		return nil, err
	}
	if created {
		if err := sess.Start(ctx, true); err != nil {
			return nil, err
		}
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
	// Set OnAdd before installing handlers below: the SetHandler ordering under
	// n.mu establishes happens-before with any session goroutine that calls
	// Requests.Add, so this cross-mutex write is race-free.
	svc.Requests().OnAdd = func(identity.NodeID) { n.hub.publish(Event{Type: "request"}) }
	for _, s := range n.sessions {
		s.SetHandler(svc)
	}
	n.mu.Unlock()
	svc.SetPartyProvider(n.party)
	n.party.setAllowed(svc.Allowed)
}

// SetMedia installs the streaming handler on existing and future sessions.
func (n *Node) SetMedia(svc peer.MediaHandler) {
	n.mu.Lock()
	n.media = svc
	for _, s := range n.sessions {
		s.SetMediaHandler(svc)
	}
	n.mu.Unlock()
}

// Playlist implements bridge.Streamer.
func (n *Node) Playlist(ctx context.Context, host identity.NodeID, contentID, name string) ([]byte, string, error) {
	if host == n.self.NodeID() {
		n.mu.Lock()
		lm, ok := n.media.(localMedia)
		n.mu.Unlock()
		if ok {
			data, ct, _, err := lm.LocalPlaylist(contentID, name)
			return data, ct, err
		}
	}
	sess, err := n.session(ctx, host)
	if err != nil {
		return nil, "", err
	}
	data, ct, _, err := sess.GetPlaylist(ctx, contentID, name)
	return data, ct, err
}

// Segment implements bridge.Streamer.
func (n *Node) Segment(ctx context.Context, host identity.NodeID, contentID, name string) ([]byte, string, error) {
	if host == n.self.NodeID() {
		n.mu.Lock()
		lm, ok := n.media.(localMedia)
		n.mu.Unlock()
		if ok {
			data, err := lm.LocalSegment(contentID, name)
			if err != nil {
				return nil, "", err
			}
			return data, contentTypeFor(name), nil
		}
	}
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
func (n *Node) RequestAccess(ctx context.Context, peerID, message string) error {
	sess, err := n.session(ctx, identity.NodeID(peerID))
	if err != nil {
		return err
	}
	return sess.RequestAccess(ctx, message)
}

// PendingRequests lists Node IDs awaiting our approval.
func (n *Node) PendingRequests() []string {
	n.mu.Lock()
	svc := n.catalog
	n.mu.Unlock()
	if svc == nil {
		return nil
	}
	ids := svc.Requests().List()
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = string(id)
	}
	return out
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
func (n *Node) JoinParty(ctx context.Context, host, contentID string) error {
	hostID := identity.NodeID(host)
	s, err := n.session(ctx, hostID)
	if err != nil {
		return err
	}
	return n.party.JoinParty(ctx, hostID, contentID, func(ctx context.Context) (*peerv1.PartyWelcome, error) {
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
