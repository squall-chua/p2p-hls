package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/squall-chua/p2p-hls/internal/peer"
)

type SelfView struct {
	NodeID      string `json:"nodeId"`
	DisplayName string `json:"displayName"`
}
type PeerView struct {
	NodeID      string `json:"nodeId"`
	DisplayName string `json:"displayName"`
	Online      bool   `json:"online"`
}
type TitleView struct {
	ContentID    string `json:"contentId"`
	DisplayTitle string `json:"displayTitle"`
	DurationMs   int64  `json:"durationMs"`
	PartyLive    bool   `json:"partyLive"`
	PartyViewers int    `json:"partyViewers"`
}

// Control is the set of Node operations the API exposes. Bridge depends on this
// interface, not on *app.Node, so handlers unit-test against a fake.
type Control interface {
	Self() SelfView
	Presence() []PeerView
	Library() ([]TitleView, error)
	Catalog(ctx context.Context, peer string) ([]TitleView, error) // ErrDenied -> 403
	RequestAccess(ctx context.Context, peer, message string) error
	PendingRequests() []string
	Approve(peer string) error
	StartParty(contentID string) string
	JoinParty(ctx context.Context, host, contentID string) error
	LeaveParty()
	EndParty(reason string)
}

// SetControl installs the Node adapter that backs the /api/* command handlers.
func (b *Bridge) SetControl(c Control) {
	b.mu.Lock()
	b.control = c
	b.mu.Unlock()
}

// apiAuthOK checks the bearer token (Authorization header) and origin.
func (b *Bridge) apiAuthOK(r *http.Request) bool {
	if !b.originOK(r) {
		return false
	}
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return tok == b.token
}

// writeJSON encodes v as JSON with status 200.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// statusForErr maps domain errors to HTTP status codes.
func statusForErr(err error) int {
	switch {
	case errors.Is(err, peer.ErrDenied):
		return http.StatusForbidden
	case errors.Is(err, peer.ErrNotFound):
		return http.StatusNotFound
	default:
		return http.StatusBadGateway
	}
}

func (b *Bridge) handleAPI(w http.ResponseWriter, r *http.Request) {
	if !b.apiAuthOK(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	b.mu.Lock()
	c := b.control
	b.mu.Unlock()
	if c == nil {
		http.NotFound(w, r)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/")
	switch {
	case path == "self" && r.Method == http.MethodGet:
		writeJSON(w, c.Self())
	default:
		http.NotFound(w, r)
	}
}
