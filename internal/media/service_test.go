package media_test

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/squall-chua/p2p-hls/internal/catalog"
	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/squall-chua/p2p-hls/internal/library"
	"github.com/squall-chua/p2p-hls/internal/media"
	"github.com/squall-chua/p2p-hls/internal/peer"
	"github.com/stretchr/testify/require"
)

func TestServiceAccessGatesStreaming(t *testing.T) {
	eng, cid := newEngineWithTitle(t)
	policy := catalog.NewPolicy(catalog.VisibilityRestricted)
	svc := media.NewService(eng, policy)
	bob := identity.NodeID("bob")

	_, _, _, err := svc.Playlist(bob, cid, "playlist.m3u8")
	require.ErrorIs(t, err, peer.ErrDenied)
	_, err = svc.Segment(bob, cid, "seg00000.ts")
	require.ErrorIs(t, err, peer.ErrDenied)
	_, _, err = svc.OpenFile(bob, cid)
	require.ErrorIs(t, err, peer.ErrDenied)

	policy.AddAllow(bob)
	data, ct, _, err := svc.Playlist(bob, cid, "playlist.m3u8")
	require.NoError(t, err)
	require.Contains(t, string(data), "#EXTM3U")
	require.Equal(t, "application/vnd.apple.mpegurl", ct)

	// Segment is produced by the async job; poll until ready.
	require.Eventually(t, func() bool {
		seg, e := svc.Segment(bob, cid, "seg00000.ts")
		return e == nil && len(seg) > 0
	}, 3*time.Second, 20*time.Millisecond)
}

func TestLocalServeBypassesPolicy(t *testing.T) {
	eng, cid := newEngineWithTitle(t)
	// A policy that denies every remote.
	svc := media.NewService(eng, catalog.NewPolicy(catalog.VisibilityRestricted))
	// LocalPlaylist must still serve the owner's own static master playlist.
	data, _, _, err := svc.LocalPlaylist(cid, "playlist.m3u8")
	require.NoError(t, err)
	require.NotEmpty(t, data)
}

func TestServiceOpenFileForDownload(t *testing.T) {
	eng, cid := newEngineWithTitle(t)
	policy := catalog.NewPolicy(catalog.VisibilityPublic)
	svc := media.NewService(eng, policy)
	rc, size, err := svc.OpenFile(identity.NodeID("anyone"), cid)
	require.NoError(t, err)
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	require.Equal(t, int64(len(b)), size)
}

func TestServiceLocalThumbnail(t *testing.T) {
	store, err := library.OpenStore(filepath.Join(t.TempDir(), "i.db"))
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	src := filepath.Join(t.TempDir(), "movie.mp4")
	require.NoError(t, os.WriteFile(src, []byte("x"), 0o600))
	require.NoError(t, store.Upsert(library.Title{
		ContentID: "cid", Path: src, DurationMS: 10000, Width: 1920, Height: 1080,
	}))

	cache := t.TempDir()
	// Pre-place a cached thumbnail so no real ffmpeg runs.
	require.NoError(t, os.MkdirAll(filepath.Join(cache, "cid"), 0o700))
	require.NoError(t, os.WriteFile(library.ThumbPath(cache, "cid"), []byte("JPEG"), 0o600))

	svc := media.NewService(media.NewEngine(store, &fakeRunner{}, cache), catalog.NewPolicy(catalog.VisibilityRestricted))
	data, err := svc.LocalThumbnail("cid")
	require.NoError(t, err)
	require.Equal(t, "JPEG", string(data))
}
