package app

import (
	"context"
	"encoding/hex"
	"testing"
	"time"

	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/squall-chua/p2p-hls/internal/peer"
	"github.com/squall-chua/p2p-hls/internal/swarm"
	peerv1 "github.com/squall-chua/p2p-hls/proto/peer/v1"
	"github.com/stretchr/testify/require"
	"github.com/zeebo/blake3"
)

// fakeTransport records sends and serves canned peer/host segments.
type fakeTransport struct {
	peerSeg  map[string][]byte // node|segName -> bytes
	hostSeg  map[string][]byte // segName -> bytes
	playlist []byte
}

func (f *fakeTransport) sendTo(identity.NodeID, *peerv1.Envelope) error { return nil }
func (f *fakeTransport) measureRTT(context.Context, identity.NodeID) (time.Duration, error) {
	return 10 * time.Millisecond, nil
}
func (f *fakeTransport) fetchSwarmSegment(_ context.Context, node identity.NodeID, req *peerv1.GetSwarmSegment) ([]byte, error) {
	b, ok := f.peerSeg[string(node)+"|"+req.GetSegName()]
	if !ok {
		return nil, peer.ErrNotFound
	}
	return b, nil
}
func (f *fakeTransport) ensurePeer(identity.NodeID) error { return nil }
func (f *fakeTransport) hostPlaylist(context.Context, identity.NodeID, string, string) ([]byte, error) {
	return f.playlist, nil
}
func (f *fakeTransport) hostSegment(_ context.Context, _ identity.NodeID, _, name string) ([]byte, error) {
	b, ok := f.hostSeg[name]
	if !ok {
		return nil, peer.ErrNotFound
	}
	return b, nil
}

func plHash(seg []byte, name string) []byte {
	sum := blake3.Sum256(seg)
	return []byte("#EXTINF:4.0,\n#EXT-X-P2P-HASH:" + hex.EncodeToString(sum[:]) + "\n" + name + "\n")
}

func TestFetchSegmentPrefersVerifiedPeerThenCaches(t *testing.T) {
	seg := []byte("good-segment-bytes")
	ft := &fakeTransport{
		peerSeg:  map[string][]byte{"peerX|seg00002.ts": seg},
		hostSeg:  map[string][]byte{},
		playlist: plHash(seg, "seg00002.ts"),
	}
	ss := newSwarmSession(ft, "self", "host", "cid", swarm.RealClock(), swarm.DefaultConfig())
	ss.setPeers([]identity.NodeID{"peerX"})
	ss.eng.OnPeerHave("peerX", 2, []byte{0x01}, 1, time.Now())

	got, err := ss.FetchSegment(context.Background(), "seg00002.ts")
	require.NoError(t, err)
	require.Equal(t, seg, got)

	require.True(t, ss.eng.Have(2))
	cached, ok := ss.cache.get(2)
	require.True(t, ok)
	require.Equal(t, seg, cached)
}

func TestFetchSegmentRejectsPoisonAndDemotesThenHostFallback(t *testing.T) {
	good := []byte("the-real-bytes")
	ft := &fakeTransport{
		peerSeg:  map[string][]byte{"liar|seg00002.ts": []byte("POISON")},
		hostSeg:  map[string][]byte{"seg00002.ts": good},
		playlist: plHash(good, "seg00002.ts"),
	}
	ss := newSwarmSession(ft, "self", "host", "cid", swarm.RealClock(), swarm.DefaultConfig())
	ss.setPeers([]identity.NodeID{"liar"})
	ss.eng.OnPeerHave("liar", 2, []byte{0x01}, 1, time.Now())

	got, err := ss.FetchSegment(context.Background(), "seg00002.ts")
	require.NoError(t, err)
	require.Equal(t, good, got)
	_, ok := ss.eng.PeerHasForTest("liar", 2)
	require.False(t, ok)
}
