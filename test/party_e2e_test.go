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
	"github.com/squall-chua/p2p-hls/internal/signalserver"
	"github.com/stretchr/testify/require"
)

func TestWatchPartySyncEndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(signalserver.New().HandleWS))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	// Host shares one Title and allows the viewer.
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "The.Matrix.1999.mkv"), []byte("video"), 0o600))
	store, err := library.OpenStore(filepath.Join(t.TempDir(), "host.db"))
	require.NoError(t, err)
	defer store.Close()
	require.NoError(t, library.NewScanner(store, e2eProber{}, []string{root}).ScanOnce(ctx))
	svc := catalog.NewService(store, catalog.NewPolicy(catalog.VisibilityPublic), catalog.NewRequests())

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

	// Find the host's content ID via Browse.
	titles, err := viewer.Browse(ctx, idHost.NodeID())
	require.NoError(t, err)
	require.Len(t, titles, 1)
	cid := titles[0].GetContentId()

	// Host opens a party and starts playing; a Go actuator reports its position.
	host.StartParty(cid)
	host.IngestHostPlayer("play", 60_000)

	// Viewer joins; party_id welcome should arrive and seed initial state.
	require.NoError(t, viewer.JoinParty(ctx, string(idHost.NodeID()), cid))

	// Browse again: the Title is now annotated as a live party with 1 viewer.
	require.Eventually(t, func() bool {
		ts, err := viewer.Browse(ctx, idHost.NodeID())
		return err == nil && len(ts) == 1 && ts[0].GetPartyLive() && ts[0].GetPartyViewers() == 1
	}, 5*time.Second, 100*time.Millisecond)

	// A Go actuator: a far-behind viewer player must be told to SEEK toward ~60s.
	require.Eventually(t, func() bool {
		act := viewer.PartyViewerDecide(0, true)
		return act.Seek && act.SeekMS >= 59_000
	}, 5*time.Second, 100*time.Millisecond)
}
