package catalog

import (
	"testing"

	"github.com/squall-chua/p2p-hls/internal/identity"
)

func TestRequestsOnAddFires(t *testing.T) {
	r := NewRequests()
	var got identity.NodeID
	r.OnAdd = func(n identity.NodeID) { got = n }
	r.Add("n7", "hi")
	if got != "n7" {
		t.Fatalf("OnAdd not fired: %q", got)
	}
}
