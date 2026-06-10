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

// RequestView is one pending library-access request shown to the owner.
type RequestView struct {
	NodeID      string `json:"nodeId"`
	DisplayName string `json:"displayName"` // resolved from presence; "" when unknown
	Message     string `json:"message"`     // optional message the requester attached
}
type TitleView struct {
	ContentID    string `json:"contentId"`
	DisplayTitle string `json:"displayTitle"`
	DurationMs   int64  `json:"durationMs"`
	PartyLive    bool   `json:"partyLive"`
	PartyViewers int    `json:"partyViewers"`
	Thumbnail    string `json:"thumbnail"` // data: URL for peer entries; empty for own library (UI builds the stream URL)
}

// CurrentPartyView is the node's active watch party for the "Now watching" panel.
// Active is false when the node is not in a party (other fields zero).
type CurrentPartyView struct {
	Active    bool   `json:"active"`
	Role      string `json:"role"` // "host" | "viewer"
	Host      string `json:"host"` // host nodeId; powers the /watch/{host}/{contentId} link
	ContentID string `json:"contentId"`
	Title     string `json:"title"` // display title when locally known (host side); else ""
	Viewers   int    `json:"viewers"`
}

// Control is the set of Node operations the API exposes. Bridge depends on this
// interface, not on *app.Node, so handlers unit-test against a fake.
type Control interface {
	Self() SelfView
	Presence() []PeerView
	Library() ([]TitleView, error)
	Catalog(ctx context.Context, peer string) ([]TitleView, error) // ErrDenied -> 403
	RequestAccess(ctx context.Context, peer, message string) error
	PendingRequests() []RequestView
	Approve(peer string) error
	Reject(peer string) error
	StartParty(contentID string) string
	JoinParty(ctx context.Context, host, contentID string) error
	LeaveParty()
	EndParty(reason string)
	Audience() []PeerView
	CurrentParty() CurrentPartyView
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

// splitPeerPath returns the path segment after prefix, and the trailing action (or "").
// e.g. peers/n9/catalog -> ("n9", "catalog").
func splitPeerPath(path, prefix string) (id, action string, ok bool) {
	rest := strings.TrimPrefix(path, prefix)
	if rest == path {
		return "", "", false
	}
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) == 1 {
		return parts[0], "", true
	}
	return parts[0], parts[1], true
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
	case path == "presence" && r.Method == http.MethodGet:
		writeJSON(w, c.Presence())
	case path == "library" && r.Method == http.MethodGet:
		lib, err := c.Library()
		if err != nil {
			http.Error(w, err.Error(), statusForErr(err))
			return
		}
		writeJSON(w, lib)
	case path == "requests" && r.Method == http.MethodGet:
		writeJSON(w, c.PendingRequests())
	case strings.HasPrefix(path, "peers/"):
		id, action, _ := splitPeerPath(path, "peers/")
		switch {
		case action == "catalog" && r.Method == http.MethodGet:
			cat, err := c.Catalog(r.Context(), id)
			if err != nil {
				http.Error(w, err.Error(), statusForErr(err))
				return
			}
			writeJSON(w, cat)
		case action == "request-access" && r.Method == http.MethodPost:
			var body struct {
				Message string `json:"message"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if err := c.RequestAccess(r.Context(), id, body.Message); err != nil {
				http.Error(w, err.Error(), statusForErr(err))
				return
			}
			w.WriteHeader(http.StatusAccepted)
		default:
			http.NotFound(w, r)
		}
	case strings.HasPrefix(path, "requests/") && r.Method == http.MethodPost:
		id, action, _ := splitPeerPath(path, "requests/")
		var err error
		switch action {
		case "approve":
			err = c.Approve(id)
		case "reject":
			err = c.Reject(id)
		default:
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), statusForErr(err))
			return
		}
		w.WriteHeader(http.StatusOK)
	case path == "party/audience" && r.Method == http.MethodGet:
		writeJSON(w, c.Audience())
	case path == "party/current" && r.Method == http.MethodGet:
		writeJSON(w, c.CurrentParty())
	case strings.HasPrefix(path, "party/") && r.Method == http.MethodPost:
		switch strings.TrimPrefix(path, "party/") {
		case "start":
			var body struct {
				ContentID string `json:"contentId"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			writeJSON(w, map[string]string{"partyId": c.StartParty(body.ContentID)})
		case "join":
			var body struct {
				HostNodeID string `json:"hostNodeId"`
				ContentID  string `json:"contentId"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if err := c.JoinParty(r.Context(), body.HostNodeID, body.ContentID); err != nil {
				http.Error(w, err.Error(), statusForErr(err))
				return
			}
			w.WriteHeader(http.StatusOK)
		case "leave":
			c.LeaveParty()
			w.WriteHeader(http.StatusOK)
		case "end":
			c.EndParty("host ended the party")
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}
