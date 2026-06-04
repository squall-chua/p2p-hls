// Package bridge runs the loopback HTTP server that hls.js plays from, pulling
// media over P2P sessions and hiding the P2P layer from the player.
package bridge

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"

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
}

// New constructs a Bridge that requires the given session token.
func New(streamer Streamer, token string) *Bridge {
	return &Bridge{streamer: streamer, token: token}
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
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // hls.js segment/playlist fetches are typically same-origin (no Origin header)
	}
	return strings.HasPrefix(origin, "http://127.0.0.1") || strings.HasPrefix(origin, "http://localhost")
}
