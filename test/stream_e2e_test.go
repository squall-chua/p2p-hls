package e2e_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/squall-chua/p2p-hls/internal/app"
	"github.com/squall-chua/p2p-hls/internal/bridge"
	"github.com/squall-chua/p2p-hls/internal/catalog"
	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/squall-chua/p2p-hls/internal/library"
	"github.com/squall-chua/p2p-hls/internal/media"
	"github.com/squall-chua/p2p-hls/internal/signalserver"
	"github.com/stretchr/testify/require"
)

func TestStreamAndDownloadEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	// Generate a real 2s H.264/AAC sample.
	root := t.TempDir()
	src := filepath.Join(root, "sample.mp4")
	require.NoError(t, exec.Command("ffmpeg", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=320x240:rate=15",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-c:v", "libx264", "-c:a", "aac", "-shortest", src).Run())

	srv := httptest.NewServer(http.HandlerFunc(signalserver.New().HandleWS))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Host library (real ffprobe) + media service.
	store, err := library.OpenStore(filepath.Join(t.TempDir(), "h.db"))
	require.NoError(t, err)
	defer store.Close()
	require.NoError(t, library.NewScanner(store, library.FFProbe{}, []string{root}).ScanOnce(ctx))
	all, _ := store.All()
	require.Len(t, all, 1)
	cid := all[0].ContentID

	policy := catalog.NewPolicy(catalog.VisibilityPublic)
	mediaSvc := media.NewService(media.NewEngine(store, media.ExecRunner{}, t.TempDir()), policy)

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

	// Bridge in front of the viewer.
	br := bridge.New(viewer, "tok")
	require.NoError(t, br.Start("127.0.0.1:0"))
	defer br.Close()
	prefix := br.BaseURL() + "/s/tok/" + string(idHost.NodeID()) + "/" + cid + "/"

	// Master playlist fetch through the bridge.
	resp, err := http.Get(prefix + "playlist.m3u8")
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, string(body), "#EXTM3U")

	// Poll the media (index) playlist until it lists a segment (growing playlist).
	var seg string
	require.Eventually(t, func() bool {
		r, e := http.Get(prefix + "index.m3u8")
		if e != nil {
			return false
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasSuffix(strings.TrimSpace(line), ".ts") {
				seg = strings.TrimSpace(line)
				return true
			}
		}
		return false
	}, 30*time.Second, 500*time.Millisecond)

	// Fetch the first segment through the bridge.
	sr, err := http.Get(prefix + seg)
	require.NoError(t, err)
	sb, _ := io.ReadAll(sr.Body)
	sr.Body.Close()
	require.Equal(t, http.StatusOK, sr.StatusCode)
	require.NotEmpty(t, sb)

	// Verified download of the original.
	dest := filepath.Join(t.TempDir(), "out.mp4")
	require.NoError(t, viewer.Download(ctx, idHost.NodeID(), cid, dest))
	di, _ := os.Stat(dest)
	si, _ := os.Stat(src)
	require.Equal(t, si.Size(), di.Size(), "downloaded original matches source size")
}
