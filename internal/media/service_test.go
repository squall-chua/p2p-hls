package media_test

import (
	"io"
	"testing"
	"time"

	"github.com/squall-chua/p2p-hls/internal/catalog"
	"github.com/squall-chua/p2p-hls/internal/identity"
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
