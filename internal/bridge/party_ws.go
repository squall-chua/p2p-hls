package bridge

import (
	"net/http"
	"strings"

	"github.com/gorilla/websocket"
)

// SetPartyHandler registers the function invoked with each upgraded /party
// WebSocket connection. The bridge stays a dumb conduit: it authenticates and
// upgrades, then hands the raw connection to the app, which owns the protocol.
func (b *Bridge) SetPartyHandler(fn func(*websocket.Conn)) {
	b.mu.Lock()
	b.partyHandler = fn
	b.mu.Unlock()
}

// handleParty serves /party/{token}: origin-checked, token-gated, then upgraded.
func (b *Bridge) handleParty(w http.ResponseWriter, r *http.Request) {
	if !b.originOK(r) {
		http.Error(w, "bad origin", http.StatusForbidden)
		return
	}
	token := strings.TrimPrefix(r.URL.Path, "/party/")
	if token != b.token {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	b.mu.Lock()
	fn := b.partyHandler
	b.mu.Unlock()
	if fn == nil {
		http.NotFound(w, r)
		return
	}
	conn, err := b.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrade already wrote the error response
	}
	fn(conn)
}
