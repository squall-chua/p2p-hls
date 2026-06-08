// Package bridge runs the loopback HTTP server that hls.js plays from, pulling
// media over P2P sessions and hiding the P2P layer from the player.
package bridge

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/squall-chua/p2p-hls/internal/identity"
)

// Streamer fetches playlists and segments from a remote Host session.
type Streamer interface {
	Playlist(ctx context.Context, host identity.NodeID, contentID, name string) (data []byte, contentType string, err error)
	Segment(ctx context.Context, host identity.NodeID, contentID, name string) (data []byte, contentType string, err error)
}

// Bridge is the loopback HTTP server.
type Bridge struct {
	streamer Streamer
	token    string
	srv      *http.Server
	ln       net.Listener

	mu           sync.Mutex
	partyHandler func(*websocket.Conn)
	upgrader     websocket.Upgrader
	control      Control
	events       Subscriber
	selfNodeID   string
	selfName     string
}

// New constructs a Bridge that requires the given session token.
func New(streamer Streamer, token string) *Bridge {
	b := &Bridge{streamer: streamer, token: token}
	b.upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return b.originOK(r) }}
	return b
}

// Start binds addr (use "127.0.0.1:0" for an ephemeral port) and serves. It
// refuses to bind a non-loopback address so the bridge can never be exposed
// off-host.
func (b *Bridge) Start(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if !isLoopbackHost(host) {
		return fmt.Errorf("bridge: refusing to bind non-loopback address %q", addr)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	b.ln = ln
	mux := http.NewServeMux()
	mux.HandleFunc("/s/", b.handleStream)
	mux.HandleFunc("/party/", b.handleParty)
	mux.HandleFunc("/api/events", b.handleSSE)
	mux.HandleFunc("/api/", b.handleAPI)
	mux.HandleFunc("/", b.handleStatic)
	b.srv = &http.Server{Handler: mux}
	go b.srv.Serve(ln)
	return nil
}

// isLoopbackHost reports whether host resolves to the loopback interface.
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// BaseURL returns the http://127.0.0.1:PORT base.
func (b *Bridge) BaseURL() string { return "http://" + b.ln.Addr().String() }

// Close stops the server.
func (b *Bridge) Close() error { return b.srv.Close() }

// handleStream serves /s/{token}/{node}/{cid}/{name}.
func (b *Bridge) handleStream(w http.ResponseWriter, r *http.Request) {
	if !b.originOK(r) {
		http.Error(w, "bad origin", http.StatusForbidden)
		return
	}
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/s/"), "/", 4)
	if len(parts) != 4 {
		http.NotFound(w, r)
		return
	}
	token, node, cid, name := parts[0], parts[1], parts[2], parts[3]
	if token != b.token {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	var (
		data []byte
		ct   string
		err  error
	)
	if strings.HasSuffix(name, ".m3u8") {
		data, ct, err = b.streamer.Playlist(r.Context(), identity.NodeID(node), cid, name)
	} else {
		data, ct, err = b.streamer.Segment(r.Context(), identity.NodeID(node), cid, name)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", ct)
	_, _ = w.Write(data)
}

// originOK allows same-origin/no-origin requests (the embedded UI is same-origin).
func (b *Bridge) originOK(r *http.Request) bool {
	port := ""
	if b.ln != nil {
		_, port, _ = net.SplitHostPort(b.ln.Addr().String())
	}
	return originAllowed(r.Header.Get("Origin"), port)
}

// originAllowed reports whether origin is an exact loopback origin bound to port.
// An empty origin is allowed (hls.js segment/playlist fetches are same-origin and
// send no Origin header). Host is matched exactly against the loopback set so a
// spoof like http://127.0.0.1.evil.com is rejected.
func originAllowed(origin, port string) bool {
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || !isLoopbackHost(u.Hostname()) {
		return false
	}
	return port == "" || u.Port() == port
}
