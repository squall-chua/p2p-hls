// Package signalserver implements the trust-minimized WebRTC signaling server:
// presence tracking and opaque relay routing. It never inspects relay payloads.
package signalserver

import (
	"crypto/ed25519"
	"crypto/rand"
	"log/slog"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/squall-chua/p2p-hls/internal/signaling"
)

type client struct {
	info signaling.PeerInfo
	send chan []byte
}

// Server tracks online clients and routes relays.
type Server struct {
	upgrader websocket.Upgrader
	mu       sync.Mutex
	clients  map[string]*client // keyed by NodeID
}

// New constructs an empty Server.
func New() *Server {
	return &Server{
		upgrader: websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
		clients:  make(map[string]*client),
	}
}

// HandleWS upgrades and serves one Node connection.
func (s *Server) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// 1. Challenge.
	nonce := make([]byte, 32)
	_, _ = rand.Read(nonce)
	if err := writeMsg(conn, signaling.Challenge{Nonce: nonce}); err != nil {
		return
	}

	// 2. Await Register and verify.
	_, raw, err := conn.ReadMessage()
	if err != nil {
		return
	}
	typ, env, err := signaling.Unmarshal(raw)
	if err != nil || typ != signaling.TypeRegister {
		_ = writeMsg(conn, signaling.Error{Message: "expected register"})
		return
	}
	reg := env.(*signaling.Register)
	if !validRegister(reg, nonce) {
		_ = writeMsg(conn, signaling.Error{Message: "invalid registration"})
		return
	}

	c := &client{
		info: signaling.PeerInfo{NodeID: reg.NodeID, PublicKey: reg.PublicKey, DisplayName: reg.DisplayName},
		send: make(chan []byte, 32),
	}

	// 3. Register: snapshot to this client, join broadcast to others.
	snapshot := s.add(c)
	_ = writeMsg(conn, signaling.PresenceSnapshot{Peers: snapshot})
	s.broadcastExcept(c.info.NodeID, signaling.PresenceJoin{Peer: c.info})
	defer func() {
		s.remove(c.info.NodeID)
		s.broadcastExcept(c.info.NodeID, signaling.PresenceLeave{NodeID: c.info.NodeID})
	}()

	// 4. Writer goroutine.
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case b := <-c.send:
				if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
					return
				}
			}
		}
	}()
	defer close(done)

	// 5. Read loop: only Relay is accepted post-register.
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		typ, env, err := signaling.Unmarshal(raw)
		if err != nil {
			continue
		}
		if typ != signaling.TypeRelay {
			continue
		}
		rel := env.(*signaling.Relay)
		rel.From = c.info.NodeID // server authoritatively stamps the sender
		s.routeRelay(*rel)
	}
}

func validRegister(reg *signaling.Register, nonce []byte) bool {
	pub := ed25519.PublicKey(reg.PublicKey)
	if len(pub) != ed25519.PublicKeySize {
		return false
	}
	if identity.NodeIDFromPublicKey(pub) != identity.NodeID(reg.NodeID) {
		return false
	}
	return identity.Verify(pub, nonce, reg.Signature)
}

func (s *Server) add(c *client) []signaling.PeerInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	others := make([]signaling.PeerInfo, 0, len(s.clients))
	for _, existing := range s.clients {
		others = append(others, existing.info)
	}
	s.clients[c.info.NodeID] = c
	return others
}

func (s *Server) remove(nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.clients, nodeID)
}

func (s *Server) broadcastExcept(exceptNodeID string, msg any) {
	b, err := signaling.Marshal(msg)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, c := range s.clients {
		if id == exceptNodeID {
			continue
		}
		select {
		case c.send <- b:
		default:
			slog.Warn("dropping message to slow client", "node", id)
		}
	}
}

func (s *Server) routeRelay(rel signaling.Relay) {
	b, err := signaling.Marshal(rel)
	if err != nil {
		return
	}
	s.mu.Lock()
	target, ok := s.clients[rel.To]
	s.mu.Unlock()
	if !ok {
		return
	}
	select {
	case target.send <- b:
	default:
		slog.Warn("dropping relay to slow client", "node", rel.To)
	}
}

func writeMsg(conn *websocket.Conn, msg any) error {
	b, err := signaling.Marshal(msg)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, b)
}
