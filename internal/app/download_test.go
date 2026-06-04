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
	"github.com/squall-chua/p2p-hls/internal/media"
	"github.com/squall-chua/p2p-hls/internal/signalserver"
	"github.com/stretchr/testify/require"
)

func TestNodeDownloadVerifiesHash(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(signalserver.New().HandleWS))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Host shares a file; Content ID = its BLAKE3 hash.
	root := t.TempDir()
	srcPath := filepath.Join(root, "clip.mp4")
	require.NoError(t, os.WriteFile(srcPath, []byte("the-original-bytes"), 0o600))
	cid, err := library.HashFile(srcPath)
	require.NoError(t, err)

	store, err := library.OpenStore(filepath.Join(t.TempDir(), "h.db"))
	require.NoError(t, err)
	defer store.Close()
	require.NoError(t, store.Upsert(library.Title{ContentID: cid, Path: srcPath, VideoCodec: "h264", AudioCodecs: []string{"aac"}}))

	policy := catalog.NewPolicy(catalog.VisibilityPublic)
	mediaSvc := media.NewService(media.NewEngine(store, &fakeRunner2{}, t.TempDir()), policy)

	idHost, _ := identity.Generate()
	idViewer, _ := identity.Generate()
	cfg := app.Config{SignalingURL: wsURL}

	host, err := app.NewNode(ctx, idHost, "host", cfg)
	require.NoError(t, err)
	defer host.Close()
	host.SetMedia(mediaSvc)

	viewer, err := app.NewNode(ctx, idViewer, "viewer", cfg)
	require.NoError(t, err)
	defer viewer.Close()

	require.Eventually(t, func() bool { return viewer.Sees(idHost.NodeID()) }, 5*time.Second, 25*time.Millisecond)

	dest := filepath.Join(t.TempDir(), "downloaded.mp4")
	require.NoError(t, viewer.Download(ctx, idHost.NodeID(), cid, dest))

	got, err := os.ReadFile(dest)
	require.NoError(t, err)
	require.Equal(t, "the-original-bytes", string(got))
}

func TestNodeDownloadRejectsHashMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(signalserver.New().HandleWS))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	root := t.TempDir()
	srcPath := filepath.Join(root, "clip.mp4")
	require.NoError(t, os.WriteFile(srcPath, []byte("the-original-bytes"), 0o600))

	// Host advertises a WRONG Content ID for this file (a forged/corrupt index entry):
	// the file's real BLAKE3 hash is NOT 64 zeros.
	wrongCID := strings.Repeat("0", 64)
	store, err := library.OpenStore(filepath.Join(t.TempDir(), "h.db"))
	require.NoError(t, err)
	defer store.Close()
	require.NoError(t, store.Upsert(library.Title{ContentID: wrongCID, Path: srcPath, VideoCodec: "h264", AudioCodecs: []string{"aac"}}))

	policy := catalog.NewPolicy(catalog.VisibilityPublic)
	mediaSvc := media.NewService(media.NewEngine(store, &fakeRunner2{}, t.TempDir()), policy)

	idHost, _ := identity.Generate()
	idViewer, _ := identity.Generate()
	cfg := app.Config{SignalingURL: wsURL}

	host, err := app.NewNode(ctx, idHost, "host", cfg)
	require.NoError(t, err)
	defer host.Close()
	host.SetMedia(mediaSvc)

	viewer, err := app.NewNode(ctx, idViewer, "viewer", cfg)
	require.NoError(t, err)
	defer viewer.Close()

	require.Eventually(t, func() bool { return viewer.Sees(idHost.NodeID()) }, 5*time.Second, 25*time.Millisecond)

	dest := filepath.Join(t.TempDir(), "out.mp4")
	err = viewer.Download(ctx, idHost.NodeID(), wrongCID, dest)
	require.Error(t, err)
	require.Contains(t, err.Error(), "integrity")
	require.NoFileExists(t, dest)
}

type fakeRunner2 struct{}

func (fakeRunner2) Run(_ context.Context, _ []string) error { return nil }
