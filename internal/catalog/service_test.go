package catalog_test

import (
	"path/filepath"
	"testing"

	"github.com/squall-chua/p2p-hls/internal/catalog"
	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/squall-chua/p2p-hls/internal/library"
	"github.com/squall-chua/p2p-hls/internal/peer"
	"github.com/stretchr/testify/require"
)

func newServiceWithTitle(t *testing.T) (*catalog.Service, *catalog.Policy, *catalog.Requests) {
	t.Helper()
	store, err := library.OpenStore(filepath.Join(t.TempDir(), "i.db"))
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	require.NoError(t, store.Upsert(library.Title{
		ContentID: "cid-1", DisplayTitle: "Movie", DurationMS: 5000,
		VideoCodec: "h264", AudioCodecs: []string{"aac"}, Width: 1920, Height: 1080,
		HLSCompatible: true,
		Subtitles:     []library.SubtitleTrack{{ID: "embedded:2", Language: "eng", Kind: "text"}},
	}))
	policy := catalog.NewPolicy(catalog.VisibilityRestricted)
	reqs := catalog.NewRequests()
	return catalog.NewService(store, policy, reqs), policy, reqs
}

func TestServiceBrowseDeniedByDefault(t *testing.T) {
	svc, _, _ := newServiceWithTitle(t)
	_, err := svc.Browse(identity.NodeID("bob"))
	require.ErrorIs(t, err, peer.ErrDenied)
}

func TestServiceBrowseAfterAllow(t *testing.T) {
	svc, policy, _ := newServiceWithTitle(t)
	policy.AddAllow(identity.NodeID("bob"))
	titles, err := svc.Browse(identity.NodeID("bob"))
	require.NoError(t, err)
	require.Len(t, titles, 1)
	require.Equal(t, "cid-1", titles[0].GetContentId())
	require.True(t, titles[0].GetHlsCompatible())
	require.Len(t, titles[0].GetSubtitles(), 1)
	require.Equal(t, "eng", titles[0].GetSubtitles()[0].GetLanguage())
}

func TestServiceGetMetadataNotFound(t *testing.T) {
	svc, policy, _ := newServiceWithTitle(t)
	policy.AddAllow(identity.NodeID("bob"))
	_, err := svc.GetMetadata(identity.NodeID("bob"), "nope")
	require.ErrorIs(t, err, peer.ErrNotFound)
}

func TestServiceRequestAccessRecorded(t *testing.T) {
	svc, _, reqs := newServiceWithTitle(t)
	require.NoError(t, svc.RequestAccess(identity.NodeID("bob"), "pls"))
	require.Equal(t, []identity.NodeID{identity.NodeID("bob")}, reqs.List())
}

func TestServiceGetMetadataDeniedDoesNotProbeExistence(t *testing.T) {
	svc, _, _ := newServiceWithTitle(t)
	// A denied peer gets ErrDenied for an EXISTING content id (cid-1), proving the
	// deny-check runs before the store lookup — denied peers can't probe existence.
	_, err := svc.GetMetadata(identity.NodeID("bob"), "cid-1")
	require.ErrorIs(t, err, peer.ErrDenied)
	// ...and the same for a missing id.
	_, err = svc.GetMetadata(identity.NodeID("bob"), "nope")
	require.ErrorIs(t, err, peer.ErrDenied)
}
