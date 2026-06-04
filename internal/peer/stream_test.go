package peer

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/stretchr/testify/require"
)

type fakeMedia struct {
	playlist []byte
	segment  []byte
	file     string
}

func (m *fakeMedia) Playlist(_ identity.NodeID, _, name string) ([]byte, string, bool, error) {
	if name == "missing.m3u8" {
		return nil, "", false, ErrNotFound
	}
	return m.playlist, "application/vnd.apple.mpegurl", true, nil
}
func (m *fakeMedia) Segment(_ identity.NodeID, _, _ string) ([]byte, error) { return m.segment, nil }
func (m *fakeMedia) OpenFile(_ identity.NodeID, _ string) (io.ReadCloser, int64, error) {
	return io.NopCloser(strings.NewReader(m.file)), int64(len(m.file)), nil
}

func TestStreamingRPCs(t *testing.T) {
	viewer, host, _ := connectPair(t) // from rpc_test.go
	mh := &fakeMedia{
		playlist: []byte("#EXTM3U\n#EXT-X-ENDLIST\n"),
		segment:  bytes.Repeat([]byte("S"), 40000), // > 2 frames, exercises chunking
		file:     strings.Repeat("ORIGINAL", 10000),
	}
	host.SetMediaHandler(mh)
	ctx := context.Background()

	data, ct, complete, err := viewer.GetPlaylist(ctx, "cid", "playlist.m3u8")
	require.NoError(t, err)
	require.Contains(t, string(data), "ENDLIST")
	require.Equal(t, "application/vnd.apple.mpegurl", ct)
	require.True(t, complete)

	_, _, _, err = viewer.GetPlaylist(ctx, "cid", "missing.m3u8")
	require.ErrorIs(t, err, ErrNotFound)

	seg, err := viewer.GetSegment(ctx, "cid", "seg00000.ts")
	require.NoError(t, err)
	require.Equal(t, mh.segment, seg, "multi-frame segment reassembled exactly")

	var buf bytes.Buffer
	require.NoError(t, viewer.DownloadTo(ctx, "cid", &buf))
	require.Equal(t, mh.file, buf.String())
}

// errMedia fails every request, to exercise error propagation over the control channel.
type errMedia struct{}

func (errMedia) Playlist(identity.NodeID, string, string) ([]byte, string, bool, error) {
	return nil, "", false, ErrNotFound
}
func (errMedia) Segment(identity.NodeID, string, string) ([]byte, error) { return nil, ErrNotFound }
func (errMedia) OpenFile(identity.NodeID, string) (io.ReadCloser, int64, error) {
	return nil, 0, ErrNotFound
}

func TestGetSegmentHandlerErrorPropagates(t *testing.T) {
	viewer, host, _ := connectPair(t)
	host.SetMediaHandler(errMedia{})
	_, err := viewer.GetSegment(context.Background(), "cid", "seg00000.ts")
	require.ErrorIs(t, err, ErrNotFound)
}
