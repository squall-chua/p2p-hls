package catalog

import (
	"sync"

	"github.com/squall-chua/p2p-hls/internal/identity"
)

// Requests holds pending access requests awaiting the User's approval.
type Requests struct {
	mu      sync.Mutex
	pending map[identity.NodeID]string

	OnAdd func(node identity.NodeID) // optional; fired (outside the lock) after a new/updated request
}

// NewRequests creates an empty register.
func NewRequests() *Requests {
	return &Requests{pending: map[identity.NodeID]string{}}
}

// Add records (or updates) a pending request from node with an optional message.
func (r *Requests) Add(node identity.NodeID, message string) {
	r.mu.Lock()
	r.pending[node] = message
	cb := r.OnAdd
	r.mu.Unlock()
	if cb != nil {
		cb(node)
	}
}

// List returns the Node IDs with pending requests.
func (r *Requests) List() []identity.NodeID {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]identity.NodeID, 0, len(r.pending))
	for n := range r.pending {
		out = append(out, n)
	}
	return out
}

// Take removes and returns a pending request's message.
func (r *Requests) Take(node identity.NodeID) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	msg, ok := r.pending[node]
	delete(r.pending, node)
	return msg, ok
}
