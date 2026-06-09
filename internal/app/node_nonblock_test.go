package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/squall-chua/p2p-hls/internal/peer"
	"github.com/squall-chua/p2p-hls/internal/signalserver"
	peerv1 "github.com/squall-chua/p2p-hls/proto/peer/v1"
	"github.com/stretchr/testify/require"
)

// A swarm send to an in-party peer we are the dialer for, but that never answers,
// must return promptly with ErrUnavailable. Gossip is a periodic, idempotent loop;
// it must never block a whole round inside Dial waiting on one unreachable peer.
func TestPeerSessionDoesNotBlockOnUnreachableDialerEdge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(signalserver.New().HandleWS))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	self, _ := identity.Generate()
	// target is a NodeID we are the lower-of (so peerSession takes the dialer path)
	// but is never connected to signaling, so the edge can never become ready.
	var target identity.NodeID
	for {
		id, _ := identity.Generate()
		if shouldDial(self.NodeID(), id.NodeID()) {
			target = id.NodeID()
			break
		}
	}

	n, err := NewNode(ctx, self, "self", Config{SignalingURL: wsURL})
	require.NoError(t, err)
	defer n.Close()

	done := make(chan error, 1)
	go func() {
		done <- n.sendTo(target, &peerv1.Envelope{Body: &peerv1.Envelope_SwarmHave{SwarmHave: &peerv1.SwarmHave{}}})
	}()
	select {
	case err := <-done:
		require.ErrorIs(t, err, peer.ErrUnavailable)
	case <-time.After(3 * time.Second):
		t.Fatal("sendTo blocked on an unreachable dialer edge (peerSession must not block on Dial)")
	}
}
