package catalog_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/squall-chua/p2p-hls/internal/catalog"
	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/squall-chua/p2p-hls/internal/library"
	"github.com/squall-chua/p2p-hls/internal/peer"
	"github.com/stretchr/testify/require"
)

func newServiceWithTitle(t *testing.T) (*catalog.Service, *catalog.Policy, *catalog.Requests, string) {
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
	cache := t.TempDir()
	return catalog.NewService(store, policy, reqs, cache, nil), policy, reqs, cache
}

func writeThumb(t *testing.T, cache, cid string, b []byte) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(cache, cid), 0o700))
	require.NoError(t, os.WriteFile(library.ThumbPath(cache, cid), b, 0o600))
}

func TestServiceBrowseDeniedByDefault(t *testing.T) {
	svc, _, _, _ := newServiceWithTitle(t)
	_, err := svc.Browse(identity.NodeID("bob"))
	require.ErrorIs(t, err, peer.ErrDenied)
}

func TestServiceBrowseAfterAllow(t *testing.T) {
	svc, policy, _, _ := newServiceWithTitle(t)
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
	svc, policy, _, _ := newServiceWithTitle(t)
	policy.AddAllow(identity.NodeID("bob"))
	_, err := svc.GetMetadata(identity.NodeID("bob"), "nope")
	require.ErrorIs(t, err, peer.ErrNotFound)
}

func TestServiceRequestAccessRecorded(t *testing.T) {
	svc, _, reqs, _ := newServiceWithTitle(t)
	require.NoError(t, svc.RequestAccess(identity.NodeID("bob"), "pls"))
	require.Equal(t, []identity.NodeID{identity.NodeID("bob")}, reqs.List())
}

func TestServiceRejectClearsPendingWithoutAllowing(t *testing.T) {
	svc, _, reqs, _ := newServiceWithTitle(t)
	require.NoError(t, svc.RequestAccess(identity.NodeID("bob"), "pls"))
	svc.Reject(identity.NodeID("bob"))
	require.Empty(t, reqs.List(), "rejected request is cleared from pending")
	require.False(t, svc.Allowed(identity.NodeID("bob")), "reject must not grant access")
}

func TestServiceGetMetadataDeniedDoesNotProbeExistence(t *testing.T) {
	svc, _, _, _ := newServiceWithTitle(t)
	// A denied peer gets ErrDenied for an EXISTING content id (cid-1), proving the
	// deny-check runs before the store lookup — denied peers can't probe existence.
	_, err := svc.GetMetadata(identity.NodeID("bob"), "cid-1")
	require.ErrorIs(t, err, peer.ErrDenied)
	// ...and the same for a missing id.
	_, err = svc.GetMetadata(identity.NodeID("bob"), "nope")
	require.ErrorIs(t, err, peer.ErrDenied)
}

func TestBrowseEmbedsThumbnailWhenPresent(t *testing.T) {
	svc, policy, _, cache := newServiceWithTitle(t)
	policy.AddAllow(identity.NodeID("bob"))
	writeThumb(t, cache, "cid-1", []byte("JPEGBYTES"))

	titles, err := svc.Browse(identity.NodeID("bob"))
	require.NoError(t, err)
	require.Equal(t, []byte("JPEGBYTES"), titles[0].GetThumbnail())
}

func TestBrowseOmitsThumbnailWhenAbsent(t *testing.T) {
	svc, policy, _, _ := newServiceWithTitle(t)
	policy.AddAllow(identity.NodeID("bob"))
	titles, err := svc.Browse(identity.NodeID("bob"))
	require.NoError(t, err)
	require.Empty(t, titles[0].GetThumbnail())
}

func TestBrowseOmitsOversizeThumbnail(t *testing.T) {
	svc, policy, _, cache := newServiceWithTitle(t)
	policy.AddAllow(identity.NodeID("bob"))
	writeThumb(t, cache, "cid-1", make([]byte, 65*1024)) // > 64 KB cap

	titles, err := svc.Browse(identity.NodeID("bob"))
	require.NoError(t, err)
	require.Empty(t, titles[0].GetThumbnail())
}

func TestLibraryDoesNotEmbedThumbnail(t *testing.T) {
	svc, _, _, cache := newServiceWithTitle(t)
	writeThumb(t, cache, "cid-1", []byte("JPEGBYTES"))
	titles, err := svc.Library()
	require.NoError(t, err)
	require.Empty(t, titles[0].GetThumbnail(), "owner library uses the local stream URL, not embedded bytes")
}

func TestLibraryReportsFolderForTitle(t *testing.T) {
	root := filepath.Join(t.TempDir(), "MyMovies")
	require.NoError(t, os.MkdirAll(root, 0o755))
	store, err := library.OpenStore(filepath.Join(t.TempDir(), "i.db"))
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	require.NoError(t, store.Upsert(library.Title{
		ContentID:    "cid-1",
		DisplayTitle: "Movie",
		Path:         filepath.Join(root, "Movies", "Action", "x.mkv"),
	}))
	svc := catalog.NewService(store, catalog.NewPolicy(catalog.VisibilityRestricted), catalog.NewRequests(), t.TempDir(), []string{root})

	titles, err := svc.Library()
	require.NoError(t, err)
	require.Equal(t, "Movies/Action", titles[0].GetRelDir())
	require.Equal(t, "MyMovies", titles[0].GetRootLabel())
}
