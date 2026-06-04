package signaling

import (
	"context"
	"fmt"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/squall-chua/p2p-hls/internal/identity"
)

// Client is a Node's connection to the signaling server.
type Client struct {
	conn   *websocket.Conn
	relays chan Relay
	done   chan struct{}

	mu        sync.RWMutex
	peers     map[string]PeerInfo
	writeMu   sync.Mutex
	closeOnce sync.Once
}

// Dial connects, completes challenge/register, and starts the read loop.
func Dial(ctx context.Context, url string, id *identity.Identity, displayName string) (*Client, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
	if err != nil {
		return nil, fmt.Errorf("dial signaling: %w", err)
	}

	_, raw, err := conn.ReadMessage()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("read challenge: %w", err)
	}
	typ, env, err := Unmarshal(raw)
	if err != nil || typ != TypeChallenge {
		conn.Close()
		return nil, fmt.Errorf("expected challenge, got %q", typ)
	}
	ch := env.(*Challenge)

	reg, _ := Marshal(Register{
		NodeID:      string(id.NodeID()),
		PublicKey:   id.PublicKey(),
		DisplayName: displayName,
		Signature:   id.Sign(ch.Nonce),
	})
	if err := conn.WriteMessage(websocket.TextMessage, reg); err != nil {
		conn.Close()
		return nil, err
	}

	_, raw, err = conn.ReadMessage()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("read snapshot: %w", err)
	}
	typ, env, err = Unmarshal(raw)
	if err != nil || typ != TypePresenceSnapshot {
		conn.Close()
		return nil, fmt.Errorf("expected snapshot, got %q", typ)
	}

	c := &Client{
		conn:   conn,
		relays: make(chan Relay, 32),
		done:   make(chan struct{}),
		peers:  make(map[string]PeerInfo),
	}
	for _, p := range env.(*PresenceSnapshot).Peers {
		c.peers[p.NodeID] = p
	}
	go c.readLoop()
	return c, nil
}

func (c *Client) readLoop() {
	defer close(c.relays)
	for {
		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		typ, env, err := Unmarshal(raw)
		if err != nil {
			continue
		}
		switch typ {
		case TypePresenceJoin:
			p := env.(*PresenceJoin).Peer
			c.mu.Lock()
			c.peers[p.NodeID] = p
			c.mu.Unlock()
		case TypePresenceLeave:
			c.mu.Lock()
			delete(c.peers, env.(*PresenceLeave).NodeID)
			c.mu.Unlock()
		case TypeRelay:
			select {
			case c.relays <- *env.(*Relay):
			case <-c.done:
				return // shutting down; abandon the in-flight relay
			}
		}
	}
}

// Peers returns a snapshot of currently-online Nodes.
func (c *Client) Peers() []PeerInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]PeerInfo, 0, len(c.peers))
	for _, p := range c.peers {
		out = append(out, p)
	}
	return out
}

// Relays delivers incoming relay messages. Closed when the connection ends.
func (c *Client) Relays() <-chan Relay { return c.relays }

// SendRelay sends an opaque payload to another Node.
func (c *Client) SendRelay(to identity.NodeID, payload []byte) error {
	b, err := Marshal(Relay{To: string(to), Payload: payload})
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.WriteMessage(websocket.TextMessage, b)
}

// Close shuts the connection. Safe to call multiple times.
func (c *Client) Close() error {
	c.closeOnce.Do(func() { close(c.done) })
	return c.conn.Close()
}
