package app

import (
	"context"
	"sync"

	"github.com/pion/webrtc/v4"
	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/squall-chua/p2p-hls/internal/peer"
	"github.com/squall-chua/p2p-hls/internal/signaling"
)

// Node is a running app instance: a signaling client plus its peer sessions.
type Node struct {
	self   *identity.Identity
	client *signaling.Client
	rtcCfg webrtc.Configuration

	mu       sync.Mutex
	sessions map[identity.NodeID]*peer.Session
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
	return r.client.SendRelay(to, payload)
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
	go n.routeRelays()
	return n, nil
}

func (n *Node) routeRelays() {
	for rel := range n.client.Relays() {
		from := identity.NodeID(rel.From)
		sig, err := peer.DecodeSignedSignal(rel.Payload)
		if err != nil {
			continue
		}
		sess := n.sessionFor(from, false)
		_ = sess.HandleSignal(sig)
	}
}

// sessionFor returns the existing session for remote, or creates one. When
// created as an answerer (initiator=false) it is started immediately.
func (n *Node) sessionFor(remote identity.NodeID, initiator bool) *peer.Session {
	n.mu.Lock()
	defer n.mu.Unlock()
	if s, ok := n.sessions[remote]; ok {
		return s
	}
	s, err := peer.NewSession(n.self, remote, n.rtcCfg, relaySignaler{client: n.client})
	if err != nil {
		return nil
	}
	n.sessions[remote] = s
	if !initiator {
		_ = s.Start(context.Background(), false)
	}
	return s
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
func (n *Node) Dial(ctx context.Context, remote identity.NodeID) (*peer.Session, error) {
	sess := n.sessionFor(remote, true)
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

// Close shuts the node down.
func (n *Node) Close() error {
	n.mu.Lock()
	for _, s := range n.sessions {
		_ = s.Close()
	}
	n.mu.Unlock()
	return n.client.Close()
}
