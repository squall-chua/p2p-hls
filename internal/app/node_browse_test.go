package app_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/squall-chua/p2p-hls/internal/app"
	"github.com/squall-chua/p2p-hls/internal/catalog"
	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/squall-chua/p2p-hls/internal/library"
	"github.com/squall-chua/p2p-hls/internal/peer"
	"github.com/squall-chua/p2p-hls/internal/signalserver"
	"github.com/stretchr/testify/require"
)

type stubProber struct{}

func (stubProber) Probe(context.Context, string) (library.Probe, error) {
	return library.Probe{
		DurationMS: 1000, Container: "mp4",
		Video: []library.VideoStream{{Codec: "h264", Width: 1280, Height: 720}},
		Audio: []library.AudioStream{{Codec: "aac"}},
	}, nil
}

func hostLibrary(t *testing.T) *catalog.Service {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "Film.mp4"), []byte("bytes"), 0o600))
	store, err := library.OpenStore(filepath.Join(t.TempDir(), "i.db"))
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	require.NoError(t, library.NewScanner(store, stubProber{}, []string{root}).ScanOnce(context.Background()))
	return catalog.NewService(store, catalog.NewPolicy(catalog.VisibilityRestricted), catalog.NewRequests(), t.TempDir())
}

func TestNodeBrowseDeniedThenApprovedFlow(t *testing.T) {
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
	host.SetCatalog(hostLibrary(t))

	viewer, err := app.NewNode(ctx, idViewer, "viewer", cfg)
	require.NoError(t, err)
	defer viewer.Close()

	require.Eventually(t, func() bool { return viewer.Sees(idHost.NodeID()) }, 5*time.Second, 25*time.Millisecond)

	// Denied first.
	_, err = viewer.Browse(ctx, idHost.NodeID())
	require.ErrorIs(t, err, peer.ErrDenied)

	// Request access; host sees it and approves.
	require.NoError(t, viewer.RequestAccess(ctx, string(idHost.NodeID()), "friend here"))
	require.Eventually(t, func() bool { return len(host.PendingRequests()) == 1 }, 3*time.Second, 25*time.Millisecond)
	require.NoError(t, host.ApproveAccess(idViewer.NodeID()))

	// Now browse succeeds.
	titles, err := viewer.Browse(ctx, idHost.NodeID())
	require.NoError(t, err)
	require.Len(t, titles, 1)
	require.Equal(t, "Film", titles[0].GetDisplayTitle())
}
