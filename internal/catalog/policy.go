// Package catalog enforces access control and answers browse RPCs.
package catalog

import (
	"sync"

	"github.com/squall-chua/p2p-hls/internal/identity"
)

// Visibility is a Library's default access posture.
type Visibility string

const (
	VisibilityRestricted Visibility = "restricted"
	VisibilityPublic     Visibility = "public"
)

// Policy decides whether a remote Node may see this Node's Catalog.
type Policy struct {
	mu    sync.RWMutex
	def   Visibility
	allow map[identity.NodeID]bool
	block map[identity.NodeID]bool
}

// NewPolicy creates a Policy with the given default visibility.
func NewPolicy(def Visibility) *Policy {
	return &Policy{def: def, allow: map[identity.NodeID]bool{}, block: map[identity.NodeID]bool{}}
}

// Allowed evaluates: block always denies; restricted => allow-list only;
// public => everyone except block.
func (p *Policy) Allowed(node identity.NodeID) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.block[node] {
		return false
	}
	if p.def == VisibilityPublic {
		return true
	}
	return p.allow[node]
}

func (p *Policy) AddAllow(node identity.NodeID) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.allow[node] = true
}

func (p *Policy) AddBlock(node identity.NodeID) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.block[node] = true
}
