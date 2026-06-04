package signaling_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/squall-chua/p2p-hls/internal/signaling"
	"github.com/squall-chua/p2p-hls/internal/signalserver"
	"github.com/stretchr/testify/require"
)

func clientServerURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(signalserver.New().HandleWS))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
}

func TestClientConnectsRegistersAndRelays(t *testing.T) {
	wsURL := clientServerURL(t)
	idA, _ := identity.Generate()
	idB, _ := identity.Generate()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ca, err := signaling.Dial(ctx, wsURL, idA, "alice")
	require.NoError(t, err)
	defer ca.Close()

	cb, err := signaling.Dial(ctx, wsURL, idB, "bob")
	require.NoError(t, err)
	defer cb.Close()

	// B should appear in A's presence within a moment.
	require.Eventually(t, func() bool {
		for _, p := range ca.Peers() {
			if p.NodeID == string(idB.NodeID()) {
				return true
			}
		}
		return false
	}, 2*time.Second, 20*time.Millisecond)

	// A relays a payload to B.
	require.NoError(t, ca.SendRelay(idB.NodeID(), []byte("hi-bob")))
	select {
	case rel := <-cb.Relays():
		require.Equal(t, string(idA.NodeID()), rel.From)
		require.Equal(t, []byte("hi-bob"), rel.Payload)
	case <-time.After(2 * time.Second):
		t.Fatal("B never received the relay")
	}
}
