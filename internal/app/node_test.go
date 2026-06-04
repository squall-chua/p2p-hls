package app_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/squall-chua/p2p-hls/internal/app"
	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/squall-chua/p2p-hls/internal/signalserver"
	"github.com/stretchr/testify/require"
)

func TestNodeDialAndPing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(signalserver.New().HandleWS))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	idA, _ := identity.Generate()
	idB, _ := identity.Generate()
	cfg := app.Config{SignalingURL: wsURL}

	nodeA, err := app.NewNode(ctx, idA, "alice", cfg)
	require.NoError(t, err)
	defer nodeA.Close()
	nodeB, err := app.NewNode(ctx, idB, "bob", cfg)
	require.NoError(t, err)
	defer nodeB.Close()

	// A waits to see B online, then dials and pings.
	require.Eventually(t, func() bool { return nodeA.Sees(idB.NodeID()) }, 3*time.Second, 25*time.Millisecond)

	sess, err := nodeA.Dial(ctx, idB.NodeID())
	require.NoError(t, err)
	pong, err := sess.Ping(ctx, "hello")
	require.NoError(t, err)
	require.Equal(t, "hello", pong)
}
