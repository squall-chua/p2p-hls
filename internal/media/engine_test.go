package media_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/squall-chua/p2p-hls/internal/library"
	"github.com/squall-chua/p2p-hls/internal/media"
	"github.com/squall-chua/p2p-hls/internal/peer"
	"github.com/stretchr/testify/require"
)

// fakeRunner simulates ffmpeg by writing a playlist + one segment into outDir.
type fakeRunner struct{ outDir string }

func (f *fakeRunner) Run(_ context.Context, args []string) error {
	playlist := args[len(args)-1] // last arg is the playlist path
	dir := filepath.Dir(playlist)
	if err := os.WriteFile(filepath.Join(dir, "seg00000.ts"), []byte("TSDATA"), 0o600); err != nil {
		return err
	}
	return os.WriteFile(playlist,
		[]byte("#EXTM3U\n#EXTINF:4.0,\nseg00000.ts\n#EXT-X-ENDLIST\n"), 0o600)
}

func newEngineWithTitle(t *testing.T) (*media.Engine, string) {
	t.Helper()
	store, err := library.OpenStore(filepath.Join(t.TempDir(), "i.db"))
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	src := filepath.Join(t.TempDir(), "movie.mp4")
	require.NoError(t, os.WriteFile(src, []byte("x"), 0o600))
	require.NoError(t, store.Upsert(library.Title{
		ContentID: "cid", Path: src, VideoCodec: "h264", AudioCodecs: []string{"aac"},
		Width: 1920, Height: 1080, HLSCompatible: true,
	}))
	cache := t.TempDir()
	eng := media.NewEngine(store, &fakeRunner{}, cache)
	return eng, "cid"
}

func TestEngineServesMasterAndSegment(t *testing.T) {
	eng, cid := newEngineWithTitle(t)
	ctx := context.Background()

	// The master playlist is static and available immediately.
	master, complete, err := eng.File(ctx, cid, "playlist.m3u8")
	require.NoError(t, err)
	require.Contains(t, string(master), "#EXT-X-STREAM-INF")
	require.True(t, complete) // master is static => complete

	// The video playlist + segment are produced by the async job; poll until ready
	// (this mirrors the real "not ready, retry" growing-playlist behavior).
	require.Eventually(t, func() bool {
		idx, _, e := eng.File(ctx, cid, "index.m3u8")
		return e == nil && strings.Contains(string(idx), "seg00000.ts")
	}, 3*time.Second, 20*time.Millisecond)

	require.Eventually(t, func() bool {
		seg, _, e := eng.File(ctx, cid, "seg00000.ts")
		return e == nil && string(seg) == "TSDATA"
	}, 3*time.Second, 20*time.Millisecond)
}

func TestEngineUnknownContentNotFound(t *testing.T) {
	eng, _ := newEngineWithTitle(t)
	_, _, err := eng.File(context.Background(), "missing", "playlist.m3u8")
	require.ErrorIs(t, err, peer.ErrNotFound)
}

// blockingRunner blocks in Run until released, so the job stays in-flight.
type blockingRunner struct{ release chan struct{} }

func (b *blockingRunner) Run(_ context.Context, _ []string) error {
	<-b.release
	return nil
}

func TestEngineSegmentNotReadyReturnsUnavailable(t *testing.T) {
	store, err := library.OpenStore(filepath.Join(t.TempDir(), "i.db"))
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	src := filepath.Join(t.TempDir(), "movie.mp4")
	require.NoError(t, os.WriteFile(src, []byte("x"), 0o600))
	require.NoError(t, store.Upsert(library.Title{
		ContentID: "cid", Path: src, VideoCodec: "h264", AudioCodecs: []string{"aac"},
		Width: 1920, Height: 1080, HLSCompatible: true,
	}))

	br := &blockingRunner{release: make(chan struct{})}
	eng := media.NewEngine(store, br, t.TempDir())

	// The ffmpeg job is in-flight (runner blocked), so the segment isn't on disk
	// and the job isn't complete: File must return ErrUnavailable ("not ready, retry"),
	// NOT ErrNotFound and NOT a generic error.
	_, _, err = eng.File(context.Background(), "cid", "seg00000.ts")
	require.ErrorIs(t, err, peer.ErrUnavailable)

	close(br.release) // release the job so its goroutine finishes
}
