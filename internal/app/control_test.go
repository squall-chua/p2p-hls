package app

import (
	"testing"

	"github.com/squall-chua/p2p-hls/internal/identity"
)

func TestNodeSelfReportsDisplayName(t *testing.T) {
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	n := &Node{self: id, displayName: "Alice"}
	if got := n.Self().DisplayName; got != "Alice" {
		t.Fatalf("self %+v", n.Self())
	}
}
