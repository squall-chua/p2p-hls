package bridge_test

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/squall-chua/p2p-hls/internal/bridge"
	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/stretchr/testify/require"
)

type fakeStreamer struct{}

func (fakeStreamer) Playlist(_ context.Context, _ identity.NodeID, _, name string) ([]byte, string, error) {
	if name == "playlist.m3u8" {
		return []byte("#EXTM3U\nindex.m3u8\n"), "application/vnd.apple.mpegurl", nil
	}
	return []byte("#EXTM3U\nseg00000.ts\n#EXT-X-ENDLIST\n"), "application/vnd.apple.mpegurl", nil
}
func (fakeStreamer) Segment(_ context.Context, _ identity.NodeID, _, _ string) ([]byte, string, error) {
	return []byte("TSBYTES"), "video/mp2t", nil
}

func TestBridgeServesPlaylistAndSegmentWithToken(t *testing.T) {
	b := bridge.New(fakeStreamer{}, "secret-token")
	require.NoError(t, b.Start("127.0.0.1:0"))
	defer b.Close()
	base := b.BaseURL()
	node := "nodeabc"

	// Correct token + path.
	resp, err := http.Get(base + "/s/secret-token/" + node + "/cid/playlist.m3u8")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	require.Contains(t, string(body), "#EXTM3U")
	require.Equal(t, "application/vnd.apple.mpegurl", resp.Header.Get("Content-Type"))

	seg, err := http.Get(base + "/s/secret-token/" + node + "/cid/seg00000.ts")
	require.NoError(t, err)
	defer seg.Body.Close()
	require.Equal(t, http.StatusOK, seg.StatusCode)
	require.Equal(t, "video/mp2t", seg.Header.Get("Content-Type"))
}

func TestBridgeRejectsBadToken(t *testing.T) {
	b := bridge.New(fakeStreamer{}, "secret-token")
	require.NoError(t, b.Start("127.0.0.1:0"))
	defer b.Close()
	resp, err := http.Get(b.BaseURL() + "/s/wrong/nodeabc/cid/playlist.m3u8")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestBridgeRefusesNonLoopbackBind(t *testing.T) {
	b := bridge.New(fakeStreamer{}, "tok")
	require.Error(t, b.Start("0.0.0.0:0"), "must refuse to bind a non-loopback address")
}
