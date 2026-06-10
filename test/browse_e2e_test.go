package e2e_test

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

type e2eProber struct{}

func (e2eProber) Probe(context.Context, string) (library.Probe, error) {
	return library.Probe{
		DurationMS: 2000, Container: "matroska",
		Video: []library.VideoStream{{Codec: "h264", Width: 1920, Height: 1080}},
		Audio: []library.AudioStream{{Codec: "aac"}},
	}, nil
}

func TestBrowseAfterApprovalEndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(signalserver.New().HandleWS))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	// Host library with one Title.
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "The.Matrix.1999.mkv"), []byte("video"), 0o600))
	store, err := library.OpenStore(filepath.Join(t.TempDir(), "host.db"))
	require.NoError(t, err)
	defer store.Close()
	require.NoError(t, library.NewScanner(store, e2eProber{}, []string{root}).ScanOnce(ctx))
	svc := catalog.NewService(store, catalog.NewPolicy(catalog.VisibilityRestricted), catalog.NewRequests(), t.TempDir(), nil)

	idHost, _ := identity.Generate()
	idViewer, _ := identity.Generate()
	cfg := app.Config{SignalingURL: wsURL}

	host, err := app.NewNode(ctx, idHost, "host", cfg)
	require.NoError(t, err)
	defer host.Close()
	host.SetCatalog(svc)

	viewer, err := app.NewNode(ctx, idViewer, "viewer", cfg)
	require.NoError(t, err)
	defer viewer.Close()

	require.Eventually(t, func() bool { return viewer.Sees(idHost.NodeID()) }, 5*time.Second, 25*time.Millisecond)

	_, err = viewer.Browse(ctx, idHost.NodeID())
	require.ErrorIs(t, err, peer.ErrDenied)

	require.NoError(t, viewer.RequestAccess(ctx, string(idHost.NodeID()), "hi"))
	require.Eventually(t, func() bool { return len(host.PendingRequests()) == 1 }, 3*time.Second, 25*time.Millisecond)
	require.NoError(t, host.ApproveAccess(idViewer.NodeID()))

	titles, err := viewer.Browse(ctx, idHost.NodeID())
	require.NoError(t, err)
	require.Len(t, titles, 1)
	require.Equal(t, "The Matrix 1999", titles[0].GetDisplayTitle())
	require.True(t, titles[0].GetHlsCompatible())
}
