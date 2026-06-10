package catalog

import (
	"testing"

	"github.com/squall-chua/p2p-hls/internal/identity"
)

func TestRequestsPendingReturnsMessages(t *testing.T) {
	r := NewRequests()
	r.Add("n7", "let me in")
	r.Add("n8", "")
	got := r.Pending()
	if len(got) != 2 {
		t.Fatalf("want 2 pending, got %d", len(got))
	}
	msgs := map[identity.NodeID]string{}
	for _, p := range got {
		msgs[p.Node] = p.Message
	}
	if msgs["n7"] != "let me in" || msgs["n8"] != "" {
		t.Fatalf("messages not preserved: %+v", msgs)
	}
}

func TestRequestsOnAddFires(t *testing.T) {
	r := NewRequests()
	var got identity.NodeID
	r.OnAdd = func(n identity.NodeID) { got = n }
	r.Add("n7", "hi")
	if got != "n7" {
		t.Fatalf("OnAdd not fired: %q", got)
	}
}
