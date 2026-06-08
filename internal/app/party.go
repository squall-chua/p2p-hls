package app

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/squall-chua/p2p-hls/internal/party"
	"github.com/squall-chua/p2p-hls/internal/peer"
	"github.com/squall-chua/p2p-hls/internal/swarm"
	peerv1 "github.com/squall-chua/p2p-hls/proto/peer/v1"
)

// sender abstracts the Node's session access so the coordinator can be unit-
// tested with a nil sender (host/viewer state paths don't touch the network).
type sender interface {
	sendTo(node identity.NodeID, env *peerv1.Envelope) error
	measureRTT(ctx context.Context, node identity.NodeID) (time.Duration, error)
}

// swarmTransport is the superset of sender used by the viewer swarm session.
type swarmTransport interface {
	sendTo(node identity.NodeID, env *peerv1.Envelope) error
	measureRTT(ctx context.Context, node identity.NodeID) (time.Duration, error)
	fetchSwarmSegment(ctx context.Context, node identity.NodeID, req *peerv1.GetSwarmSegment) ([]byte, error)
	ensurePeer(node identity.NodeID) error
	hostPlaylist(ctx context.Context, host identity.NodeID, contentID, name string) ([]byte, error)
	hostSegment(ctx context.Context, host identity.NodeID, contentID, name string) ([]byte, error)
}

type partyCoordinator struct {
	send       sender
	self       identity.NodeID
	clock      party.Clock
	cfg        party.Config
	allowed    func(identity.NodeID) bool
	onAudience func()

	mu         sync.Mutex
	host       *party.Host
	viewer     *party.Viewer
	viewerHost identity.NodeID
	swarm      *swarmSession
	stopHB     chan struct{}
}

func newPartyCoordinator(s sender, self identity.NodeID, clk party.Clock, cfg party.Config) *partyCoordinator {
	return &partyCoordinator{send: s, self: self, clock: clk, cfg: cfg}
}

// --- entry points ---

func (pc *partyCoordinator) setAllowed(fn func(identity.NodeID) bool) {
	pc.mu.Lock()
	pc.allowed = fn
	pc.mu.Unlock()
}

// StartParty opens a Watch Party on contentID and returns the new party_id.
// party_id is derived from the host node + content (stable, opaque to viewers).
func (pc *partyCoordinator) StartParty(contentID string) string {
	pid := string(pc.self) + ":" + contentID
	pc.mu.Lock()
	if pc.stopHB != nil {
		close(pc.stopHB)
		pc.stopHB = nil
	}
	pc.host = party.NewHost(pc.clock, pc.cfg, pid, contentID)
	pc.stopHB = make(chan struct{})
	stop := pc.stopHB
	h := pc.host
	pc.mu.Unlock()
	go pc.heartbeat(h, stop)
	return pid
}

// EndParty stops the active party and notifies the Audience.
func (pc *partyCoordinator) EndParty(reason string) {
	pc.mu.Lock()
	h := pc.host
	pc.host = nil
	if pc.stopHB != nil {
		close(pc.stopHB)
		pc.stopHB = nil
	}
	pc.mu.Unlock()
	if h == nil {
		return
	}
	if pc.send == nil {
		return
	}
	for _, m := range h.Members() {
		_ = pc.send.sendTo(m.NodeID, &peerv1.Envelope{
			Body: &peerv1.Envelope_PartyEnded{PartyEnded: &peerv1.PartyEnded{PartyId: h.PartyID(), Reason: reason}},
		})
	}
}

func (pc *partyCoordinator) beginViewer(host identity.NodeID) {
	pc.mu.Lock()
	pc.viewer = party.NewViewer(pc.clock, pc.cfg)
	pc.viewerHost = host
	pc.mu.Unlock()
}

// JoinParty connects (must already have a session) and joins host's party for
// contentID. The caller passes a function that performs the JoinParty RPC.
func (pc *partyCoordinator) JoinParty(ctx context.Context, host identity.NodeID, contentID string,
	do func(ctx context.Context) (*peerv1.PartyWelcome, error)) error {
	w, err := do(ctx)
	if err != nil {
		return err
	}
	pc.beginViewer(host)
	if tr, ok := pc.send.(swarmTransport); ok { // fake senders in unit tests skip the swarm
		ss := newSwarmSession(tr, pc.self, host, contentID, swarm.RealClock(), swarm.DefaultConfig())
		ss.setPartyID(w.GetPartyId())
		pc.mu.Lock()
		old := pc.swarm
		pc.swarm = ss
		pc.mu.Unlock()
		if old != nil { // re-join: stop the prior session's gossip loop
			old.close()
		}
		// seed peers from the welcome's audience, then start gossiping
		if a := w.GetAudience(); a != nil {
			members := make([]identity.NodeID, 0, len(a.GetMembers()))
			for _, m := range a.GetMembers() {
				members = append(members, identity.NodeID(m.GetNodeId()))
			}
			ss.setPeers(members)
		}
		ss.start()
	}
	if init := w.GetInitial(); init != nil {
		pc.OnPartyState(host, init)
	}
	return nil
}

// --- catalog.PartyProvider ---

func (pc *partyCoordinator) LiveParty(contentID string) (bool, int) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if pc.host != nil && pc.host.ContentID() == contentID {
		return true, pc.host.ViewerCount()
	}
	return false, 0
}

// --- peer.PartyHandler ---

func (pc *partyCoordinator) OnJoinParty(remote identity.NodeID, contentID string) (*peerv1.PartyWelcome, error) {
	pc.mu.Lock()
	h := pc.host
	allowed := pc.allowed
	pc.mu.Unlock()
	if h == nil || h.ContentID() != contentID {
		return nil, peer.ErrNotFound
	}
	if allowed != nil && !allowed(remote) {
		return nil, peer.ErrDenied
	}
	h.Join(remote, string(remote))
	pc.broadcastAudience(h)
	st := h.Snapshot(pc.clock.Now())
	return &peerv1.PartyWelcome{
		PartyId:  h.PartyID(),
		Initial:  toWireState(st),
		Audience: toWireAudience(h),
	}, nil
}

func (pc *partyCoordinator) OnLeaveParty(remote identity.NodeID, _ string) {
	pc.mu.Lock()
	h := pc.host
	pc.mu.Unlock()
	if h == nil {
		return
	}
	h.Leave(remote)
	pc.broadcastAudience(h)
}

func (pc *partyCoordinator) OnPartyState(remote identity.NodeID, s *peerv1.PartyState) {
	pc.mu.Lock()
	v, vh := pc.viewer, pc.viewerHost
	pc.mu.Unlock()
	if v == nil || remote != vh {
		return
	}
	v.OnState(fromWireState(s), pc.clock.Now())
}

func (pc *partyCoordinator) OnPartyAudience(_ identity.NodeID, a *peerv1.PartyAudience) {
	pc.mu.Lock()
	ss := pc.swarm
	pc.mu.Unlock()
	if ss == nil {
		return
	}
	members := make([]identity.NodeID, 0, len(a.GetMembers()))
	for _, m := range a.GetMembers() {
		members = append(members, identity.NodeID(m.GetNodeId()))
	}
	ss.setPeers(members)
	pc.mu.Lock()
	cb := pc.onAudience
	pc.mu.Unlock()
	if cb != nil {
		cb()
	}
}

func (pc *partyCoordinator) OnPartyInvite(identity.NodeID, *peerv1.PartyInvite) {
	// Invite is a UI signal; a real UI would surface it and let the user call
	// JoinParty. No-op acceptance for this slice.
}

func (pc *partyCoordinator) OnPartyEnded(remote identity.NodeID, _ *peerv1.PartyEnded) {
	pc.mu.Lock()
	var ss *swarmSession
	if remote == pc.viewerHost {
		pc.viewer = nil
		ss = pc.swarm
		pc.swarm = nil
	}
	pc.mu.Unlock()
	if ss != nil {
		ss.close()
	}
}

// --- heartbeat ---

func (pc *partyCoordinator) heartbeat(h *party.Host, stop chan struct{}) {
	t := time.NewTicker(pc.cfg.HeartbeatInterval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			if pc.send == nil {
				continue
			}
			now := pc.clock.Now()
			st, _ := h.Tick(now) // commits any settled seek; bumps seq if so
			env := &peerv1.Envelope{Body: &peerv1.Envelope_PartyState{PartyState: toWireState(st)}}
			for _, m := range h.Members() {
				_ = pc.send.sendTo(m.NodeID, env)
			}
		}
	}
}

func (pc *partyCoordinator) broadcastAudience(h *party.Host) {
	if pc.send == nil {
		return
	}
	a := toWireAudience(h)
	env := &peerv1.Envelope{Body: &peerv1.Envelope_PartyAudience{PartyAudience: a}}
	for _, m := range h.Members() {
		_ = pc.send.sendTo(m.NodeID, env)
	}
	pc.mu.Lock()
	cb := pc.onAudience
	pc.mu.Unlock()
	if cb != nil {
		cb()
	}
}

func (pc *partyCoordinator) OnSwarmHave(remote identity.NodeID, h *peerv1.SwarmHave) {
	pc.mu.Lock()
	ss := pc.swarm
	pc.mu.Unlock()
	if ss != nil {
		ss.OnSwarmHave(remote, h)
	}
}

func (pc *partyCoordinator) SwarmSegment(remote identity.NodeID, req *peerv1.GetSwarmSegment) ([]byte, func(), error) {
	pc.mu.Lock()
	ss := pc.swarm
	pc.mu.Unlock()
	if ss == nil {
		return nil, nil, peer.ErrUnavailable
	}
	return ss.SwarmSegment(remote, req)
}

// swarmFor returns the active viewer swarm session iff it matches (host, contentID).
func (pc *partyCoordinator) swarmFor(host identity.NodeID, contentID string) *swarmSession {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if pc.swarm != nil && pc.swarm.host == host && pc.swarm.contentID == contentID {
		return pc.swarm
	}
	return nil
}

// activePartyID returns the viewed party id, or "" if none.
func (pc *partyCoordinator) activePartyID() string {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if pc.swarm != nil {
		return pc.swarm.partyID
	}
	return ""
}

// viewerDecide is a test seam for the viewer correction (used by the WS loop).
func (pc *partyCoordinator) viewerDecide(posMS int64, playing bool, now time.Time) party.Action {
	pc.mu.Lock()
	v := pc.viewer
	pc.mu.Unlock()
	if v == nil {
		return party.Action{Play: playing, Rate: 1.0}
	}
	return v.Decide(posMS, playing, now)
}

// refreshViewerRTT measures the round trip to the current Host and feeds the
// viewer engine a fresh one-way-delay estimate (ADR 0004's RTT/2 clock model).
func (pc *partyCoordinator) refreshViewerRTT() {
	pc.mu.Lock()
	v, host, s := pc.viewer, pc.viewerHost, pc.send
	pc.mu.Unlock()
	if v == nil || s == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if rtt, err := s.measureRTT(ctx, host); err == nil {
		v.OnRTT(rtt)
	}
}

// --- wire conversions ---

func toWireState(s party.State) *peerv1.PartyState {
	return &peerv1.PartyState{PartyId: s.PartyID, Playing: s.Playing, PositionMs: s.PositionMS, Rate: s.Rate, Seq: s.Seq}
}
func fromWireState(s *peerv1.PartyState) party.State {
	return party.State{PartyID: s.GetPartyId(), Playing: s.GetPlaying(), PositionMS: s.GetPositionMs(), Rate: s.GetRate(), Seq: s.GetSeq()}
}
func toWireAudience(h *party.Host) *peerv1.PartyAudience {
	a := &peerv1.PartyAudience{PartyId: h.PartyID()}
	for _, m := range h.Members() {
		a.Members = append(a.Members, &peerv1.AudienceMember{NodeId: string(m.NodeID), DisplayName: m.DisplayName})
	}
	return a
}

// --- WS loop (player <-> engine over the loopback /party socket) ---

type playerMsg struct {
	Type    string `json:"type"`
	Role    string `json:"role,omitempty"`
	PosMS   int64  `json:"posMs"`
	Playing bool   `json:"playing"`
}

// serveWS runs the loopback player loop. Host role: read player events into the
// host engine. Viewer role: read reports and push Actions on a ticker.
func (pc *partyCoordinator) serveWS(conn *websocket.Conn) {
	defer conn.Close()
	_, raw, err := conn.ReadMessage()
	if err != nil {
		return
	}
	var hello playerMsg
	if json.Unmarshal(raw, &hello) != nil || hello.Type != "hello" {
		return
	}
	if hello.Role == "host" {
		pc.serveHostWS(conn)
		return
	}
	pc.serveViewerWS(conn)
}

func (pc *partyCoordinator) serveHostWS(conn *websocket.Conn) {
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var m playerMsg
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		pc.mu.Lock()
		h := pc.host
		pc.mu.Unlock()
		if h == nil {
			return
		}
		now := pc.clock.Now()
		switch m.Type {
		case "play":
			h.OnPlay(m.PosMS, now)
		case "pause":
			h.OnPause(m.PosMS, now)
		case "seek":
			h.OnSeek(m.PosMS, now)
		case "report":
			h.OnReport(m.PosMS, now)
		}
	}
}

func (pc *partyCoordinator) serveViewerWS(conn *websocket.Conn) {
	reports := make(chan playerMsg, 8)
	go func() {
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				close(reports)
				return
			}
			var m playerMsg
			if json.Unmarshal(raw, &m) == nil {
				select {
				case reports <- m:
				default:
				}
			}
		}
	}()
	rttDone := make(chan struct{})
	defer close(rttDone)
	go func() {
		pc.refreshViewerRTT() // immediate first estimate
		tk := time.NewTicker(2 * time.Second)
		defer tk.Stop()
		for {
			select {
			case <-rttDone:
				return
			case <-tk.C:
				pc.refreshViewerRTT()
			}
		}
	}()
	last := playerMsg{}
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case m, ok := <-reports:
			if !ok {
				return
			}
			last = m
		case <-t.C:
			act := pc.viewerDecide(last.PosMS, last.Playing, pc.clock.Now())
			b, _ := json.Marshal(act)
			if conn.WriteMessage(websocket.TextMessage, b) != nil {
				return
			}
		}
	}
}
