package e2e_test

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

func TestFoundationEndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(signalserver.New().HandleWS))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	idHost, _ := identity.Generate()
	idViewer, _ := identity.Generate()
	cfg := app.Config{SignalingURL: wsURL}

	host, err := app.NewNode(ctx, idHost, "host", cfg)
	require.NoError(t, err)
	defer host.Close()

	viewer, err := app.NewNode(ctx, idViewer, "viewer", cfg)
	require.NoError(t, err)
	defer viewer.Close()

	// Viewer waits until it sees the Host in presence.
	require.Eventually(t, func() bool { return viewer.Sees(idHost.NodeID()) },
		5*time.Second, 25*time.Millisecond, "viewer should see host via presence")

	// Viewer dials Host directly over WebRTC and pings.
	sess, err := viewer.Dial(ctx, idHost.NodeID())
	require.NoError(t, err)
	defer sess.Close()

	echoed, err := sess.Ping(ctx, "foundation-ok")
	require.NoError(t, err)
	require.Equal(t, "foundation-ok", echoed)
}
